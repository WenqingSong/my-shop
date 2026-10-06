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

// TestValidResourceValues 覆盖 Resource 纯机器值校验。
func TestValidResourceValues(t *testing.T) {
	if !validMigrationValue("20261001000011") {
		t.Fatal("纯数字 migration 应为合法")
	}
	if validMigrationValue("20261001000011 flash sale") {
		t.Fatal("带说明的 migration 应为非法")
	}
	if validMigrationValue("") {
		t.Fatal("空 migration 应为非法")
	}
	if validMigrationValue("abc") {
		t.Fatal("非数字 migration 应为非法")
	}

	if !validDomainValue("12000-12999") {
		t.Fatal("合法域区间应为合法")
	}
	if validDomainValue("12000-12999（秒杀）") {
		t.Fatal("带说明的域区间应为非法")
	}
	if validDomainValue("12999-12000") {
		t.Fatal("start>end 的域区间应为非法")
	}
	if validDomainValue("12000") {
		t.Fatal("缺少 - 的域区间应为非法")
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
			name: "合法 migration Reservation",
			task: "order-v2",
			state: State{
				Resources: Resources{Migrations: []string{"20261001000009"}},
			},
			wantIssue: false,
		},
		{
			name: "migration 未登记",
			task: "order-v2",
			state: State{
				Resources: Resources{Migrations: []string{"20261001000099"}},
			},
			wantIssue: true,
			wantCheck: "Resource Authority (INV-2)",
		},
		{
			name: "migration owner 不一致",
			task: "other-task",
			state: State{
				Resources: Resources{Migrations: []string{"20261001000009"}},
			},
			wantIssue: true,
			wantCheck: "Resource Authority (INV-2)",
		},
		{
			name: "migration RELEASED 无效",
			task: "cancelled-task",
			state: State{
				Resources: Resources{Migrations: []string{"20261001000010"}},
			},
			wantIssue: true,
			wantCheck: "Resource Authority (INV-2)",
		},
		{
			name: "migration 带说明非法值",
			task: "order-v2",
			state: State{
				Resources: Resources{Migrations: []string{"20261001000009 flash sale"}},
			},
			wantIssue: true,
			wantCheck: "Resource Schema (INV-2)",
		},
		{
			name: "合法错误码域 Reservation",
			task: "order-v2",
			state: State{
				Resources: Resources{ErrorCodeDomains: []string{"10000-10999"}},
			},
			wantIssue: false,
		},
		{
			name: "错误码域带说明非法值",
			task: "order-v2",
			state: State{
				Resources: Resources{ErrorCodeDomains: []string{"10000-10999（订单）"}},
			},
			wantIssue: true,
			wantCheck: "Resource Schema (INV-2)",
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
