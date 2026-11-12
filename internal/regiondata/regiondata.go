// Package regiondata 提供地区字典（regions 表）的嵌入式数据文件、完整性校验与幂等初始化。
//
// 背景（B1-04-FIX-02）：
//   - regions 表只建结构，历史数据来自旧项目 MySQL 的 system_city 表（B1-03-04 一次性迁移）；
//   - 旧 staging 库与 shopping.sql 不随仓库长期保留，导致新建隔离测试库时地区字典为空，
//     Region/Address 的 5 个用例无法验证；
//   - 这里把「从 system_city 提取出的地区字典」作为受版本管理的数据文件随仓库分发，
//     并提供结构校验 + 可重复执行（幂等）的导入实现，供 CLI（`my-shop region-seed`）与
//     集成测试初始化共用，避免第二套导入逻辑。
//
// 数据文件规则：
//   - 只包含地区字典，不包含任何用户/订单等业务数据；
//   - id 复用 system_city.city_id（不重新编号），parent_id/name 原样，level = 原始 level + 1；
//   - 文件头的 source_sha256 / data_version 记录来源与版本，见 Metadata()。
package regiondata

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/go-sql-driver/mysql"
	"github.com/gogf/gf/v2/database/gdb"
)

//go:embed regions_v1.tsv
var dataFS embed.FS

const (
	// DataVersion 是地区数据文件版本；数据内容变化时必须递增并同步文档。
	DataVersion = "v1"
	// SourceFile 是原始数据文件（Owner 提供，未纳入版本库，仅作溯源记录）。
	SourceFile = "tmp/shopping.sql"
	// SourceTable 是原始数据表。
	SourceTable = "system_city"
	// SourceExportDate 是原始 SQL 的导出日期（取自文件头 Date 字段）。
	SourceExportDate = "2021-09-27"
	// SourceSHA256 是原始 SQL 文件的 SHA-256（导入前必须与本地原始文件一致）。
	SourceSHA256 = "568da34e4cd084e497450e07e75709966b67153daa99e68ae14e291a01cc23c2"

	// dataFileName 是嵌入的数据文件。
	dataFileName = "regions_v1.tsv"

	// 地区层级：1=省、2=市、3=区/县（与 regions.level 一致）。
	levelProvince = 1
	levelCity     = 2
	levelDistrict = 3
	// maxDepth 是地区链的最大层级（省→市→区县），超过即视为数据异常/成环。
	maxDepth = 3
	// maxNameRunes 与 regions.name VARCHAR(64) 对齐。
	maxNameRunes = 64
	// insertBatchSize 是单条 INSERT 的最大行数（控制 SQL 长度与包体大小）。
	insertBatchSize = 500
)

// Record 是一个地区节点（对应 regions 一行）。
type Record struct {
	ID       int64
	ParentID int64
	Level    int
	Name     string
}

// Meta 是数据文件的来源与规模元信息。
type Meta struct {
	DataVersion      string
	SourceFile       string
	SourceTable      string
	SourceSHA256     string
	SourceExportDate string
	Total            int
	Provinces        int
	Cities           int
	Districts        int
}

// Stats 是一次初始化/校验的结果。
type Stats struct {
	Meta     Meta
	DryRun   bool
	Existing int // 目标库已有节点数（dry-run 时为当前值）
	Matched  int // 与数据文件完全一致、无需写入的节点数
	Inserted int // 实际写入（或 dry-run 下预计写入）的节点数
}

var (
	loadOnce sync.Once
	records  []Record
	meta     Meta
	loadErr  error
)

// Records 返回数据文件中的全部地区节点（按 level、id 升序）。
//
// 返回副本，调用方修改不会影响后续使用。
func Records() ([]Record, error) {
	if err := load(); err != nil {
		return nil, err
	}
	out := make([]Record, len(records))
	copy(out, records)
	return out, nil
}

// Metadata 返回数据文件的来源与规模元信息。
func Metadata() Meta {
	_ = load()
	return meta
}

// load 解析并校验嵌入数据文件（进程内只执行一次）。
func load() error {
	loadOnce.Do(func() {
		raw, err := dataFS.ReadFile(dataFileName)
		if err != nil {
			loadErr = fmt.Errorf("读取地区数据文件 %s: %w", dataFileName, err)
			return
		}
		recs, err := parse(string(raw))
		if err != nil {
			loadErr = err
			return
		}
		if err := Validate(recs); err != nil {
			loadErr = fmt.Errorf("地区数据文件 %s 校验失败: %w", dataFileName, err)
			return
		}
		records = recs
		meta = buildMeta(recs)
	})
	return loadErr
}

// parse 解析 TSV：忽略 # 注释行与表头行，字段为 id/parent_id/level/name。
func parse(raw string) ([]Record, error) {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	recs := make([]Record, 0, 4096)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			return nil, fmt.Errorf("第 %d 行字段数 = %d，期望 4（id/parent_id/level/name）", lineNo, len(fields))
		}
		id, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
		if err != nil {
			// 表头行（id parent_id level name）不是错误，跳过。
			if strings.EqualFold(strings.TrimSpace(fields[0]), "id") {
				continue
			}
			return nil, fmt.Errorf("第 %d 行 id 非法: %v", lineNo, err)
		}
		parentID, err := strconv.ParseInt(strings.TrimSpace(fields[1]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("第 %d 行 parent_id 非法: %v", lineNo, err)
		}
		level, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil {
			return nil, fmt.Errorf("第 %d 行 level 非法: %v", lineNo, err)
		}
		recs = append(recs, Record{ID: id, ParentID: parentID, Level: level, Name: fields[3]})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取地区数据文件: %w", err)
	}
	if len(recs) == 0 {
		return nil, fmt.Errorf("地区数据文件为空")
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].Level != recs[j].Level {
			return recs[i].Level < recs[j].Level
		}
		return recs[i].ID < recs[j].ID
	})
	return recs, nil
}

// Validate 校验地区数据的结构完整性：ID 唯一、层级合法、名称非空且不超长、
// 省级 parent_id=0、子级父节点存在且层级连续、链路深度 ≤ 3（同时覆盖环检测）。
//
// 该校验只描述结构不变量，不把某个具体条数（如 3938）当作正确性标准；
// 条数由 Metadata/测试断言负责（数据版本事实）。
func Validate(recs []Record) error {
	if len(recs) == 0 {
		return fmt.Errorf("地区数据为空")
	}
	byID := make(map[int64]Record, len(recs))
	for _, r := range recs {
		if r.ID <= 0 {
			return fmt.Errorf("非法地区 id=%d", r.ID)
		}
		if _, dup := byID[r.ID]; dup {
			return fmt.Errorf("地区 id 重复: %d", r.ID)
		}
		if r.Level < levelProvince || r.Level > levelDistrict {
			return fmt.Errorf("地区 id=%d 非法层级 level=%d（仅允许 1/2/3）", r.ID, r.Level)
		}
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("地区 id=%d 名称为空", r.ID)
		}
		if len([]rune(r.Name)) > maxNameRunes {
			return fmt.Errorf("地区 id=%d 名称超过 %d 字符", r.ID, maxNameRunes)
		}
		byID[r.ID] = r
	}

	for _, r := range recs {
		if r.Level == levelProvince {
			if r.ParentID != 0 {
				return fmt.Errorf("省级地区 id=%d 的 parent_id=%d，应为 0", r.ID, r.ParentID)
			}
			continue
		}
		parent, ok := byID[r.ParentID]
		if !ok {
			return fmt.Errorf("地区 id=%d 的父级 %d 不存在（孤儿节点）", r.ID, r.ParentID)
		}
		if parent.Level != r.Level-1 {
			return fmt.Errorf("地区 id=%d（level=%d）的父级 id=%d 层级为 %d，层级不连续",
				r.ID, r.Level, parent.ID, parent.Level)
		}
	}

	// 链路深度检查：每个节点最多 3 跳应到达省级；超过即存在环或脏链。
	for _, r := range recs {
		cur := r
		for depth := 1; depth <= maxDepth; depth++ {
			if cur.Level == levelProvince {
				break
			}
			parent, ok := byID[cur.ParentID]
			if !ok {
				return fmt.Errorf("地区 id=%d 链路断链（父级 %d 缺失）", r.ID, cur.ParentID)
			}
			cur = parent
			if depth == maxDepth && cur.Level != levelProvince {
				return fmt.Errorf("地区 id=%d 链路超过 %d 层或存在环", r.ID, maxDepth)
			}
		}
		if cur.Level != levelProvince {
			return fmt.Errorf("地区 id=%d 无法追溯到省级根节点（疑似成环）", r.ID)
		}
	}
	return nil
}

// buildMeta 汇总元信息。
func buildMeta(recs []Record) Meta {
	m := Meta{
		DataVersion:      DataVersion,
		SourceFile:       SourceFile,
		SourceTable:      SourceTable,
		SourceSHA256:     SourceSHA256,
		SourceExportDate: SourceExportDate,
		Total:            len(recs),
	}
	for _, r := range recs {
		switch r.Level {
		case levelProvince:
			m.Provinces++
		case levelCity:
			m.Cities++
		case levelDistrict:
			m.Districts++
		}
	}
	return m
}

// Ensure 以幂等方式把地区字典写入 regions 表。
//
// 语义：
//   - 目标表为空：一次性写入全部节点（单事务、分批 INSERT）；
//   - 已有部分节点：只插入缺失节点，已存在且内容一致的行跳过；
//   - 内容冲突（同一 id 的 parent_id/level/name 不一致）或存在数据文件之外的额外节点：
//     不做任何写入，返回错误并列出示例（绝不无条件覆盖）；
//   - dryRun=true：只做校验与差异统计，不写数据库。
//
// 该函数不删除/不更新已有行，不触碰 regions 之外的任何表。
func Ensure(ctx context.Context, db gdb.DB, dryRun bool) (Stats, error) {
	recs, err := Records()
	if err != nil {
		return Stats{}, err
	}
	stats := Stats{Meta: Metadata(), DryRun: dryRun}

	existing, err := loadExisting(ctx, db)
	if err != nil {
		return stats, err
	}
	stats.Existing = len(existing)

	want := make(map[int64]Record, len(recs))
	for _, r := range recs {
		want[r.ID] = r
	}

	var extra []int64
	var conflicts []string
	for id, cur := range existing {
		expected, ok := want[id]
		if !ok {
			extra = append(extra, id)
			continue
		}
		if cur.ParentID != expected.ParentID || cur.Level != expected.Level || cur.Name != expected.Name {
			conflicts = append(conflicts, fmt.Sprintf("id=%d 现有(parent=%d,level=%d,name=%q) ≠ 期望(parent=%d,level=%d,name=%q)",
				id, cur.ParentID, cur.Level, cur.Name, expected.ParentID, expected.Level, expected.Name))
		}
	}

	var missing []Record
	for _, r := range recs {
		if _, ok := existing[r.ID]; !ok {
			missing = append(missing, r)
		}
	}
	stats.Matched = len(recs) - len(missing)
	stats.Inserted = len(missing)

	if len(conflicts) > 0 || len(extra) > 0 {
		sort.Slice(extra, func(i, j int) bool { return extra[i] < extra[j] })
		return stats, fmt.Errorf(
			"地区字典与目标库不一致：内容冲突 %d 个、数据文件之外的额外节点 %d 个；拒绝写入（不覆盖）。冲突示例: %s；额外 id 示例: %s",
			len(conflicts), len(extra), sampleStrings(conflicts, 3), sampleIDs(extra, 5))
	}
	if dryRun || len(missing) == 0 {
		return stats, nil
	}

	if err := insertMissing(ctx, db, missing, len(recs)); err != nil {
		stats.Inserted = 0
		return stats, err
	}
	return stats, nil
}

// loadExisting 读取目标库已有地区节点（按 id）。
func loadExisting(ctx context.Context, db gdb.DB) (map[int64]Record, error) {
	rows, err := db.GetAll(ctx, "SELECT id, parent_id, level, name FROM regions")
	if err != nil {
		if isTableNotExist(err) {
			return nil, fmt.Errorf("目标库缺少 regions 表，请先执行 `my-shop migrate up`")
		}
		return nil, fmt.Errorf("读取 regions 现有数据: %w", err)
	}
	out := make(map[int64]Record, len(rows))
	for _, row := range rows {
		out[row["id"].Int64()] = Record{
			ID:       row["id"].Int64(),
			ParentID: row["parent_id"].Int64(),
			Level:    row["level"].Int(),
			Name:     row["name"].String(),
		}
	}
	return out, nil
}

// insertMissing 在单事务内分批写入缺失节点，并在提交前复核总行数。
//
// expectedTotal 是数据文件的总行数；Ensure 已确认目标库没有数据文件之外的额外行，
// 因此事务内写入后行数必须恰好等于该值，否则整体回滚（不留下部分数据）。
func insertMissing(ctx context.Context, db gdb.DB, missing []Record, expectedTotal int) error {
	sort.Slice(missing, func(i, j int) bool { return missing[i].ID < missing[j].ID })
	return db.Transaction(ctx, func(ctx context.Context, tx gdb.TX) error {
		for start := 0; start < len(missing); start += insertBatchSize {
			end := start + insertBatchSize
			if end > len(missing) {
				end = len(missing)
			}
			chunk := missing[start:end]
			var sb strings.Builder
			sb.WriteString("INSERT INTO regions (id, parent_id, level, name) VALUES ")
			args := make([]any, 0, len(chunk)*4)
			for i, r := range chunk {
				if i > 0 {
					sb.WriteString(",")
				}
				sb.WriteString("(?,?,?,?)")
				args = append(args, r.ID, r.ParentID, r.Level, r.Name)
			}
			if _, err := tx.Ctx(ctx).Exec(sb.String(), args...); err != nil {
				return fmt.Errorf("写入地区字典（id=%d 起共 %d 行）: %w", chunk[0].ID, len(chunk), err)
			}
		}
		cnt, err := tx.Ctx(ctx).GetCount("SELECT COUNT(*) FROM regions")
		if err != nil {
			return fmt.Errorf("复核 regions 行数: %w", err)
		}
		if int(cnt) != expectedTotal {
			return fmt.Errorf("地区字典写入后总行数 = %d，期望 %d（整体回滚）", cnt, expectedTotal)
		}
		return nil
	})
}

// sampleStrings 返回最多 n 个示例。
func sampleStrings(items []string, n int) string {
	if len(items) == 0 {
		return "无"
	}
	if len(items) > n {
		items = items[:n]
	}
	return strings.Join(items, "；")
}

// sampleIDs 返回最多 n 个示例 id。
func sampleIDs(ids []int64, n int) string {
	if len(ids) == 0 {
		return "无"
	}
	if len(ids) > n {
		ids = ids[:n]
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}

// isTableNotExist 判断 MySQL 1146（表不存在）。
func isTableNotExist(err error) bool {
	var mysqlErr *mysql.MySQLError
	if errors.As(err, &mysqlErr) {
		return mysqlErr.Number == 1146
	}
	return strings.Contains(err.Error(), "1146")
}
