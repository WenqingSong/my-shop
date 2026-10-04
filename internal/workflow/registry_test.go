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

func TestParseMigrations(t *testing.T) {
	entries, err := ParseMigrations(migrationsFixture)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("期望 4 条，实际 %d：%v", len(entries), entries)
	}
	if entries[2].Version != "20261001000009" || entries[2].Owner != "order-v2" || entries[2].Status != "RESERVED" {
		t.Fatalf("第 3 条解析错误: %+v", entries[2])
	}
}

func TestParseErrorDomains(t *testing.T) {
	entries, err := ParseErrorDomains(errorDomainsFixture)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("期望 3 条，实际 %d：%v", len(entries), entries)
	}
	if entries[2].Start != 10000 || entries[2].End != 10999 || entries[2].Owner != "order-v2" {
		t.Fatalf("第 3 条解析错误: %+v", entries[2])
	}
}

func TestCheckResourceAuthority(t *testing.T) {
	reg := Registry{
		Migrations:   mustMigrations(t),
		ErrorDomains: mustDomains(t),
	}

	tests := []struct {
		name      string
		task      string
		state     State
		wantIssue bool
		wantCheck string
	}{
		{
			// 测试要求 12：develop Registry 有合法 Reservation → 通过。
			name: "合法 migration Reservation",
			task: "order-v2",
			state: State{
				RequiredResources: RequiredResources{Migrations: []string{"20261001000009"}},
			},
			wantIssue: false,
		},
		{
			// 测试要求 11：develop Registry 无记录 → FAIL。
			name: "migration 未登记",
			task: "order-v2",
			state: State{
				RequiredResources: RequiredResources{Migrations: []string{"20261001000099"}},
			},
			wantIssue: true,
			wantCheck: "Resource Authority (INV-006)",
		},
		{
			// owner 不一致 → FAIL。
			name: "migration owner 不一致",
			task: "other-task",
			state: State{
				RequiredResources: RequiredResources{Migrations: []string{"20261001000009"}},
			},
			wantIssue: true,
			wantCheck: "Resource Authority (INV-006)",
		},
		{
			// RELEASED 状态无效 → FAIL。
			name: "migration RELEASED 无效",
			task: "cancelled-task",
			state: State{
				RequiredResources: RequiredResources{Migrations: []string{"20261001000010"}},
			},
			wantIssue: true,
			wantCheck: "Resource Authority (INV-006)",
		},
		{
			// 错误码域合法 Reservation → 通过。
			name: "合法错误码域 Reservation",
			task: "order-v2",
			state: State{
				RequiredResources: RequiredResources{ErrorCodeDomains: []string{"10000-10999"}},
			},
			wantIssue: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := checkResourceAuthority(tt.task, tt.state, reg)
			if tt.wantIssue != (len(issues) > 0) {
				t.Fatalf("期望 wantIssue=%v，实际 issues=%v", tt.wantIssue, issues)
			}
			if tt.wantIssue && !issueCheck(t, issues, tt.wantCheck) {
				t.Fatalf("期望 issue 含 %q，实际 %v", tt.wantCheck, issues)
			}
		})
	}
}

func mustMigrations(t *testing.T) []MigrationEntry {
	t.Helper()
	entries, err := ParseMigrations(migrationsFixture)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	return entries
}

func mustDomains(t *testing.T) []ErrorCodeDomainEntry {
	t.Helper()
	entries, err := ParseErrorDomains(errorDomainsFixture)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	return entries
}
