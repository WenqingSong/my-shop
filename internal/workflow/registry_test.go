package workflow

import "testing"

const migrationsFixture = `# 全局 Migration Version Registry

## 分配表

| version | title | 拥有方（任务） | 状态 | 备注 |
| --- | --- | --- | --- | --- |
| 20261001000001 | baseline | db-migration | ACTIVE | 基线 |
| 20261001000008 | refresh_tokens | iam-v4 | ACTIVE | 基线 |
| 20261001000009 | orders_v2 | order-v2 | RESERVED | feature 分支预留 |
| 20261001000010 | cancelled | cancelled-task | RELEASED | 已取消 |
`

const errorDomainsFixture = `# 全局错误码域 Registry

## 分配表

| 域区间 | 拥有方（任务/模块） | 状态 | 备注 |
| --- | --- | --- | --- |
| 1000-1999 | 通用 | ACTIVE | 基线 |
| 9000-9999 | order | ACTIVE | 基线 |
| 10000-10999 | order-v2 | RESERVED | feature 分支预留 |
`

// TestParseRegistryMigrationsLayout 覆盖 5 列布局（Value|Name|Owner|Status|Description）。
func TestParseRegistryMigrationsLayout(t *testing.T) {
	entries := ParseRegistry(migrationsFixture)
	if len(entries) != 4 {
		t.Fatalf("期望 4 条，实际 %d：%+v", len(entries), entries)
	}
	e := entries[2]
	if e.Value != "20261001000009" || e.Owner != "order-v2" || e.Status != "RESERVED" {
		t.Fatalf("第 3 条解析错误: %+v", e)
	}
}

// TestParseRegistryErrorCodesLayout 覆盖 4 列布局（Value|Owner|Status|Description）。
func TestParseRegistryErrorCodesLayout(t *testing.T) {
	entries := ParseRegistry(errorDomainsFixture)
	if len(entries) != 3 {
		t.Fatalf("期望 3 条，实际 %d：%+v", len(entries), entries)
	}
	e := entries[2]
	if e.Value != "10000-10999" || e.Owner != "order-v2" || e.Status != "RESERVED" {
		t.Fatalf("第 3 条解析错误: %+v", e)
	}
}

// TestParseRegistryTrailingEmptyDescription 覆盖 5 列布局行尾备注为空时 Owner/Status 定位稳定。
func TestParseRegistryTrailingEmptyDescription(t *testing.T) {
	content := `| version | title | 拥有方（任务） | 状态 | 备注 |
| --- | --- | --- | --- | --- |
| 20261001000009 | foo | demo | RESERVED | |
`
	entries := ParseRegistry(content)
	if len(entries) != 1 {
		t.Fatalf("期望 1 条，实际 %d：%+v", len(entries), entries)
	}
	if entries[0].Value != "20261001000009" || entries[0].Owner != "demo" || entries[0].Status != "RESERVED" {
		t.Fatalf("解析错误: %+v", entries[0])
	}
}

// TestCheckResourceAuthority 覆盖 generic 授权：owner/status 校验，与多 kind。
func TestCheckResourceAuthority(t *testing.T) {
	registries := map[string][]RegistryEntry{
		"migration_version": ParseRegistry(migrationsFixture),
		"error_code_domain": ParseRegistry(errorDomainsFixture),
	}

	tests := []struct {
		name         string
		task         string
		reservations map[string][]string
		wantIssue    bool
		wantCheck    string
	}{
		{
			name:         "合法 reservation（RESERVED + owner 匹配）",
			task:         "order-v2",
			reservations: map[string][]string{"migration_version": {"20261001000009"}},
			wantIssue:    false,
		},
		{
			name:         "value 未登记",
			task:         "order-v2",
			reservations: map[string][]string{"migration_version": {"20261001000099"}},
			wantIssue:    true,
			wantCheck:    "Resource Authority (INV-2)",
		},
		{
			name:         "owner 不一致",
			task:         "other-task",
			reservations: map[string][]string{"migration_version": {"20261001000009"}},
			wantIssue:    true,
			wantCheck:    "Resource Authority (INV-2)",
		},
		{
			name:         "RELEASED 状态无效",
			task:         "cancelled-task",
			reservations: map[string][]string{"migration_version": {"20261001000010"}},
			wantIssue:    true,
			wantCheck:    "Resource Authority (INV-2)",
		},
		{
			name:         "合法错误码域 reservation",
			task:         "order-v2",
			reservations: map[string][]string{"error_code_domain": {"10000-10999"}},
			wantIssue:    false,
		},
		{
			name: "多 kind 同时授权",
			task: "order-v2",
			reservations: map[string][]string{
				"migration_version": {"20261001000009"},
				"error_code_domain": {"10000-10999"},
			},
			wantIssue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := checkResourceAuthority(tt.task, tt.reservations, registries)
			if tt.wantIssue != (len(issues) > 0) {
				t.Fatalf("期望 wantIssue=%v，实际 issues=%v", tt.wantIssue, issues)
			}
			if tt.wantIssue && !issueCheck(t, issues, tt.wantCheck) {
				t.Fatalf("期望 issue 含 %q，实际 %v", tt.wantCheck, issues)
			}
		})
	}
}
