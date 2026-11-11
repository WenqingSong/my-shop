package regiondata

// 地区数据文件的纯内存测试：不连接数据库。
//
// 覆盖：数据规模/层级分布（数据版本事实）、ID 唯一与三级链路、字段映射结果的
// 关键样本（北京 1→2→3、西城区 203）、以及 Validate 对各类脏数据的拒绝。

import (
	"strings"
	"testing"
)

// TestDatasetIntegrity 校验嵌入数据文件的结构完整性与关键样本。
func TestDatasetIntegrity(t *testing.T) {
	recs, err := Records()
	if err != nil {
		t.Fatalf("加载地区数据: %v", err)
	}

	// 数据版本事实：来自 system_city 的 3938 条（省 34 / 市 369 / 区县 3535）。
	// 这不是「业务规则」，而是本数据版本的规模断言；数据更新时必须同步修改并递增 DataVersion。
	if len(recs) != 3938 {
		t.Fatalf("地区总数 = %d，期望 3938", len(recs))
	}
	meta := Metadata()
	if meta.Provinces != 34 || meta.Cities != 369 || meta.Districts != 3535 {
		t.Fatalf("层级分布 = 省%d/市%d/区县%d，期望 34/369/3535", meta.Provinces, meta.Cities, meta.Districts)
	}
	if meta.Total != len(recs) || meta.DataVersion != DataVersion {
		t.Fatalf("元信息不一致: %+v", meta)
	}

	byID := make(map[int64]Record, len(recs))
	for _, r := range recs {
		if _, dup := byID[r.ID]; dup {
			t.Fatalf("地区 id 重复: %d", r.ID)
		}
		byID[r.ID] = r
	}

	// 关键样本：北京市 1（省）→ 北京市 2（市）→ 东城区 3（区县）。
	chain := []struct {
		id       int64
		level    int
		parentID int64
		name     string
	}{
		{1, levelProvince, 0, "北京市"},
		{2, levelCity, 1, "北京市"},
		{3, levelDistrict, 2, "东城区"},
		{203, levelDistrict, 2, "西城区"},
	}
	for _, want := range chain {
		got, ok := byID[want.id]
		if !ok {
			t.Fatalf("缺少地区 id=%d", want.id)
		}
		if got.Level != want.level || got.ParentID != want.parentID || got.Name != want.name {
			t.Fatalf("地区 id=%d = %+v，期望 level=%d parent=%d name=%q",
				want.id, got, want.level, want.parentID, want.name)
		}
	}

	// id 升序稳定（Ensure 依赖该顺序生成确定性的 INSERT）。
	for i := 1; i < len(recs); i++ {
		if recs[i-1].Level > recs[i].Level {
			t.Fatalf("数据未按 level 升序排列: %+v -> %+v", recs[i-1], recs[i])
		}
		if recs[i-1].Level == recs[i].Level && recs[i-1].ID >= recs[i].ID {
			t.Fatalf("同层级未按 id 升序: %d -> %d", recs[i-1].ID, recs[i].ID)
		}
	}
}

// TestMetadataSource 校验来源元信息完整（可追溯）。
func TestMetadataSource(t *testing.T) {
	meta := Metadata()
	if meta.DataVersion != "v1" {
		t.Fatalf("DataVersion = %q", meta.DataVersion)
	}
	if meta.SourceFile != "tmp/shopping.sql" || meta.SourceTable != "system_city" {
		t.Fatalf("来源信息不完整: %+v", meta)
	}
	if len(meta.SourceSHA256) != 64 || strings.Trim(meta.SourceSHA256, "0123456789abcdef") != "" {
		t.Fatalf("SourceSHA256 不是合法 sha256: %q", meta.SourceSHA256)
	}
	if meta.SourceExportDate != "2021-09-27" {
		t.Fatalf("SourceExportDate = %q", meta.SourceExportDate)
	}
}

// TestValidateRejectsBrokenData 覆盖校验器对脏数据的拒绝（合成数据，不代表真实地区）。
func TestValidateRejectsBrokenData(t *testing.T) {
	base := []Record{
		{ID: 1, ParentID: 0, Level: 1, Name: "省A"},
		{ID: 2, ParentID: 1, Level: 2, Name: "市A"},
		{ID: 3, ParentID: 2, Level: 3, Name: "区A"},
	}
	if err := Validate(base); err != nil {
		t.Fatalf("合法数据不应报错: %v", err)
	}

	cases := []struct {
		name string
		recs []Record
		want string
	}{
		{"空数据", nil, "为空"},
		{"重复 id", []Record{{ID: 1, Level: 1, Name: "省A"}, {ID: 1, Level: 1, Name: "省B"}}, "重复"},
		{"非法 id", []Record{{ID: 0, Level: 1, Name: "省A"}}, "非法地区 id"},
		{"非法层级", []Record{{ID: 1, Level: 4, Name: "省A"}}, "非法层级"},
		{"空名称", []Record{{ID: 1, Level: 1, Name: "  "}}, "名称为空"},
		{"名称过长", []Record{{ID: 1, Level: 1, Name: strings.Repeat("省", 65)}}, "超过"},
		{"省级带父级", []Record{{ID: 1, ParentID: 2, Level: 1, Name: "省A"}}, "应为 0"},
		{"孤儿节点", []Record{{ID: 1, Level: 1, Name: "省A"}, {ID: 3, ParentID: 99, Level: 2, Name: "市A"}}, "不存在"},
		{"层级不连续", []Record{
			{ID: 1, Level: 1, Name: "省A"},
			{ID: 3, ParentID: 1, Level: 3, Name: "区A"},
		}, "层级不连续"},
		{"成环", []Record{
			{ID: 1, ParentID: 3, Level: 1, Name: "省A"},
			{ID: 2, ParentID: 1, Level: 2, Name: "市A"},
			{ID: 3, ParentID: 2, Level: 3, Name: "区A"},
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := Validate(c.recs)
			if err == nil {
				t.Fatalf("期望校验失败，实际通过")
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息 %q 未包含 %q", err.Error(), c.want)
			}
		})
	}
}
