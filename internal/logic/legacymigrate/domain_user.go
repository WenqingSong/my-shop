package legacymigrate

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// legacyPasswordResetMarker 是历史用户的密码占位值。
// 旧库密码为无盐 MD5，不能迁移为可登录密码；该值不是 bcrypt 哈希，
// IAM Login 的 bcrypt.CompareHashAndPassword 必然失败，因此旧密码无法通过正常登录。
// 用户必须走后续「重置密码」流程；迁移过程不读取、不输出原始密码或哈希。
const legacyPasswordResetMarker = "!legacy-md5-forced-reset"

// regionDomain 迁移 system_city → regions（省/市/区字典）。
type regionDomain struct{}

func (regionDomain) name() string { return "regions" }

// legacyRegion 是 system_city 参与迁移的字段。
type legacyRegion struct {
	ID       int64
	ParentID int64
	Level    int
	Name     string
}

func (regionDomain) run(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("regions")
	rows, err := dc.source.QueryContext(ctx,
		"SELECT city_id, parent_id, level, name FROM system_city ORDER BY level, city_id")
	if err != nil {
		return fmt.Errorf("读取 system_city: %w", err)
	}
	defer rows.Close()

	var regions []*legacyRegion
	for rows.Next() {
		var r legacyRegion
		if err := rows.Scan(&r.ID, &r.ParentID, &r.Level, &r.Name); err != nil {
			return fmt.Errorf("扫描 system_city: %w", err)
		}
		regions = append(regions, &r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 system_city: %w", err)
	}
	stat.SourceTotal = len(regions)

	byID := make(map[int64]*legacyRegion, len(regions))
	for _, r := range regions {
		if _, dup := byID[r.ID]; dup {
			dc.skip("regions", r.ID, "city_id 重复")
			continue
		}
		byID[r.ID] = r
	}

	existing, err := loadExistingRegionIDs(ctx, dc.target)
	if err != nil {
		return err
	}

	var valid []*legacyRegion
	for _, r := range regions {
		if reason := validateRegion(r, byID); reason != "" {
			dc.skip("regions", r.ID, reason)
			continue
		}
		stat.Eligible++
		if _, done := existing[r.ID]; done {
			dc.skipAlready("regions")
			continue
		}
		valid = append(valid, r)
	}

	if dc.dryRun {
		stat.Migrated = len(valid)
		return nil
	}
	if len(valid) == 0 {
		return nil
	}

	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启地区迁移事务: %w", err)
	}
	defer tx.Rollback()

	for _, r := range valid {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO regions (id, parent_id, level, name) VALUES (?, ?, ?, ?)",
			r.ID, r.ParentID, r.Level+1, r.Name); err != nil {
			dc.fail("regions", "地区 city_id=%d 写入失败: %v", r.ID, err)
			return nil
		}
		stat.Migrated++
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交地区迁移事务: %w", err)
	}
	return nil
}

// validateRegion 校验父子存在性与层级一致性（regions 无外键，必须由导入方保证）。
func validateRegion(r *legacyRegion, byID map[int64]*legacyRegion) string {
	if r.Level < 0 || r.Level > 2 {
		return fmt.Sprintf("非法层级 level=%d", r.Level)
	}
	if strings.TrimSpace(r.Name) == "" {
		return "地区名为空"
	}
	if len([]rune(r.Name)) > 64 {
		return "地区名超过 64 字符"
	}
	if r.Level == 0 {
		if r.ParentID != 0 {
			return fmt.Sprintf("省级地区 parent_id=%d 应为 0", r.ParentID)
		}
		return ""
	}
	parent, ok := byID[r.ParentID]
	if !ok {
		return fmt.Sprintf("父地区 city_id=%d 不存在", r.ParentID)
	}
	if parent.Level != r.Level-1 {
		return fmt.Sprintf("父地区层级 %d 与当前层级 %d 不一致", parent.Level, r.Level)
	}
	return ""
}

// loadExistingRegionIDs 读取目标库已有地区 ID（重复执行时跳过，不重复插入）。
func loadExistingRegionIDs(ctx context.Context, target *sql.DB) (map[int64]struct{}, error) {
	rows, err := target.QueryContext(ctx, "SELECT id FROM regions")
	if err != nil {
		if isTableNotExist(err) {
			return nil, fmt.Errorf("目标库缺少 regions 表，请先执行 `my-shop migrate up`")
		}
		return nil, fmt.Errorf("读取 regions: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]struct{})
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("扫描 regions: %w", err)
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// userDomain 迁移 eshop_user → users。
type userDomain struct{}

func (userDomain) name() string { return "users" }

// legacyUser 是 eshop_user 参与迁移的字段（不含原始密码哈希的输出路径）。
type legacyUser struct {
	UID        int64
	Username   string
	Password   string
	Nickname   string
	Avatar     string
	Phone      string
	Status     int
	IsDel      int
	CreateTime sql.NullTime
}

func (userDomain) run(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("users")
	rows, err := dc.source.QueryContext(ctx,
		"SELECT uid, username, password, nickname, avatar, phone, status, is_del, create_time FROM eshop_user ORDER BY uid")
	if err != nil {
		return fmt.Errorf("读取 eshop_user: %w", err)
	}
	defer rows.Close()

	var users []*legacyUser
	for rows.Next() {
		var u legacyUser
		var username, password, nickname, avatar, phone sql.NullString
		if err := rows.Scan(&u.UID, &username, &password, &nickname, &avatar, &phone,
			&u.Status, &u.IsDel, &u.CreateTime); err != nil {
			return fmt.Errorf("扫描 eshop_user: %w", err)
		}
		u.Username = strings.TrimSpace(username.String)
		u.Password = password.String
		u.Nickname = strings.TrimSpace(nickname.String)
		u.Avatar = strings.TrimSpace(avatar.String)
		u.Phone = strings.TrimSpace(phone.String)
		users = append(users, &u)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 eshop_user: %w", err)
	}
	stat.SourceTotal = len(users)

	for _, u := range users {
		if u.IsDel == 1 {
			dc.skip("users", u.UID, "源用户已软删除")
			continue
		}
		stat.Eligible++
		if _, ok := dc.mapped(entityUser, u.UID); ok {
			dc.skipAlready("users")
			continue
		}

		username := u.Username
		if username == "" {
			username = fmt.Sprintf("legacy_user_%d", u.UID)
		}
		if len([]rune(username)) > 24 {
			dc.skip("users", u.UID, "username 超过 24 字符")
			continue
		}
		status := 0
		if u.Status == 1 {
			status = 1
		}

		if dc.dryRun {
			stat.Migrated++
			dc.plan(entityUser, u.UID)
			continue
		}

		if err := writeUser(ctx, dc, u, username, status); err != nil {
			if isDuplicateKeyError(err) {
				dc.skip("users", u.UID, "username 或 phone 与目标库已有用户冲突")
				continue
			}
			dc.fail("users", "用户 old_id=%d 迁移失败（已回滚）: %v", u.UID, err)
			continue
		}
		stat.Migrated++
	}
	return nil
}

// writeUser 在事务内写入用户与映射；密码使用不可登录占位值（强制重置）。
func writeUser(ctx context.Context, dc *domainContext, u *legacyUser, username string, status int) error {
	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var phone any
	if u.Phone != "" {
		phone = u.Phone
	}
	var createdAt any
	if u.CreateTime.Valid {
		createdAt = u.CreateTime.Time
	}

	res, err := tx.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, nickname, avatar_url, phone, status, created_at) VALUES (?, ?, ?, ?, ?, ?, COALESCE(?, CURRENT_TIMESTAMP))",
		username, legacyPasswordResetMarker, u.Nickname, u.Avatar, phone, status, createdAt)
	if err != nil {
		return fmt.Errorf("插入 users: %w", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取用户新 ID: %w", err)
	}
	if err := dc.record(ctx, tx, entityUser, u.UID, newID); err != nil {
		return err
	}
	return tx.Commit()
}

// addressDomain 迁移 user_address → addresses。
type addressDomain struct{}

func (addressDomain) name() string { return "addresses" }

type legacyAddress struct {
	ID         int64
	UID        int64
	RealName   string
	Phone      string
	Province   string
	City       string
	District   string
	Detail     string
	CityID     sql.NullInt64
	IsDefault  int
	IsDel      int
	CreateTime sql.NullTime
}

func (addressDomain) run(ctx context.Context, dc *domainContext) error {
	stat := dc.stat("addresses")
	rows, err := dc.source.QueryContext(ctx,
		"SELECT id, uid, real_name, phone, province, city, district, detail, city_id, is_default, is_del, create_time FROM user_address ORDER BY id")
	if err != nil {
		return fmt.Errorf("读取 user_address: %w", err)
	}
	defer rows.Close()

	var addresses []*legacyAddress
	for rows.Next() {
		var a legacyAddress
		if err := rows.Scan(&a.ID, &a.UID, &a.RealName, &a.Phone, &a.Province, &a.City,
			&a.District, &a.Detail, &a.CityID, &a.IsDefault, &a.IsDel, &a.CreateTime); err != nil {
			return fmt.Errorf("扫描 user_address: %w", err)
		}
		addresses = append(addresses, &a)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历 user_address: %w", err)
	}
	stat.SourceTotal = len(addresses)

	defaulted := make(map[int64]bool)
	for _, a := range addresses {
		if a.IsDel == 1 {
			stat.Skipped++
			continue
		}
		userNewID, ok := dc.mapped(entityUser, a.UID)
		if !ok {
			dc.skip("addresses", a.ID, fmt.Sprintf("引用不存在或未迁移的用户 uid=%d", a.UID))
			continue
		}
		stat.Eligible++
		if _, ok := dc.mapped(entityAddress, a.ID); ok {
			dc.skipAlready("addresses")
			continue
		}

		isDefault := 0
		if a.IsDefault == 1 {
			if defaulted[a.UID] {
				dc.warn("addresses", "地址 old_id=%d 的用户 uid=%d 已存在默认地址，is_default 置 0", a.ID, a.UID)
			} else {
				defaulted[a.UID] = true
				isDefault = 1
			}
		}

		if dc.dryRun {
			stat.Migrated++
			dc.plan(entityAddress, a.ID)
			continue
		}

		if err := writeAddress(ctx, dc, a, userNewID, isDefault); err != nil {
			dc.fail("addresses", "地址 old_id=%d 迁移失败（已回滚）: %v", a.ID, err)
			continue
		}
		stat.Migrated++
	}
	return nil
}

func writeAddress(ctx context.Context, dc *domainContext, a *legacyAddress, userNewID int64, isDefault int) error {
	tx, err := dc.target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var createdAt any
	if a.CreateTime.Valid {
		createdAt = a.CreateTime.Time
	}
	res, err := tx.ExecContext(ctx,
		"INSERT INTO addresses (user_id, recipient_name, phone, province, city, district, detail, is_default, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, COALESCE(?, CURRENT_TIMESTAMP))",
		userNewID, truncateRunes(a.RealName, 32), a.Phone, a.Province, a.City, a.District, truncateRunes(a.Detail, 255), isDefault, createdAt)
	if err != nil {
		return fmt.Errorf("插入 addresses: %w", err)
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("读取地址新 ID: %w", err)
	}
	if err := dc.record(ctx, tx, entityAddress, a.ID, newID); err != nil {
		return err
	}
	return tx.Commit()
}

// truncateRunes 按字符截断，避免 utf8mb4 多字节被截成非法字节。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
