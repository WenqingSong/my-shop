package workflow

import (
	"strings"
	"testing"
)

func mustParse(t *testing.T, yml string) State {
	t.Helper()
	s, err := parseState([]byte(yml))
	if err != nil {
		t.Fatalf("解析应成功，实际 %v", err)
	}
	return s
}

func mustParseError(t *testing.T, yml, wantSub string) {
	t.Helper()
	_, err := parseState([]byte(yml))
	if err == nil {
		t.Fatalf("期望错误含 %q，实际 nil", wantSub)
	}
	if wantSub != "" && !strings.Contains(err.Error(), wantSub) {
		t.Fatalf("期望错误含 %q，实际 %v", wantSub, err)
	}
}

// TestStateV3GenericReservations 覆盖 schema v3 多 kind 正常解析。
func TestStateV3GenericReservations(t *testing.T) {
	s := mustParse(t, `schema_version: 3
task_id: demo
resources:
  reservations:
    migration_version:
      - "20261001000012"
    error_code_domain:
      - "12000-12999"
`)
	if len(s.Resources.Reservations) != 2 {
		t.Fatalf("期望 2 个 kind，实际 %+v", s.Resources.Reservations)
	}
	if s.Resources.Reservations["migration_version"][0] != "20261001000012" {
		t.Fatalf("migration_version = %+v", s.Resources.Reservations["migration_version"])
	}
}

// TestStateV3EmptyReservations 覆盖 schema v3 空 reservations（无资源需求）。
func TestStateV3EmptyReservations(t *testing.T) {
	for _, yml := range []string{
		"schema_version: 3\ntask_id: demo\nresources:\n  reservations: {}\n",
		"schema_version: 3\ntask_id: demo\nresources: {}\n",
		"schema_version: 3\ntask_id: demo\n",
	} {
		s := mustParse(t, yml)
		if s.HasRequiredResources() {
			t.Fatalf("空 reservations 不应声明资源: %q", yml)
		}
	}
}

// TestStateV3InvalidResourceKind 覆盖 schema v3 非法 resource kind → ERROR。
func TestStateV3InvalidResourceKind(t *testing.T) {
	mustParseError(t, `schema_version: 3
task_id: demo
resources:
  reservations:
    MigrationVersion:
      - "20261001000012"
`, "非法 resource kind")
}

// TestStateV3EmptyReservationValue 覆盖 schema v3 空 reservation value → ERROR。
func TestStateV3EmptyReservationValue(t *testing.T) {
	mustParseError(t, `schema_version: 3
task_id: demo
resources:
  reservations:
    migration_version:
      - ""
`, "不得为空")
}

// TestStateV3LegacyFieldsError 覆盖 schema v3 使用 legacy 字段 → ERROR。
func TestStateV3LegacyFieldsError(t *testing.T) {
	mustParseError(t, `schema_version: 3
task_id: demo
resources:
  migrations:
    - "20261001000012"
`, "不允许 legacy")
}

// TestStateV3MixedError 覆盖 schema v3 同时混用 reservations 与 legacy 字段 → ERROR。
func TestStateV3MixedError(t *testing.T) {
	mustParseError(t, `schema_version: 3
task_id: demo
resources:
  reservations:
    migration_version:
      - "20261001000012"
  error_code_domains:
    - "12000-12999"
`, "不允许 legacy")
}

// TestStateV2MigrationsNormalize 覆盖 schema v2 migrations → migration_version。
func TestStateV2MigrationsNormalize(t *testing.T) {
	s := mustParse(t, `schema_version: 2
task_id: demo
resources:
  migrations:
    - "20261001000012"
  error_code_domains: []
`)
	got := s.Resources.Reservations["migration_version"]
	if len(got) != 1 || got[0] != "20261001000012" {
		t.Fatalf("migration_version = %+v", got)
	}
	if _, ok := s.Resources.Reservations["error_code_domain"]; ok {
		t.Fatalf("空 error_code_domains 不应生成 error_code_domain kind")
	}
}

// TestStateV2ErrorCodeDomainsNormalize 覆盖 schema v2 error_code_domains → error_code_domain。
func TestStateV2ErrorCodeDomainsNormalize(t *testing.T) {
	s := mustParse(t, `schema_version: 2
task_id: demo
resources:
  migrations: []
  error_code_domains:
    - "12000-12999"
`)
	got := s.Resources.Reservations["error_code_domain"]
	if len(got) != 1 || got[0] != "12000-12999" {
		t.Fatalf("error_code_domain = %+v", got)
	}
	if _, ok := s.Resources.Reservations["migration_version"]; ok {
		t.Fatalf("空 migrations 不应生成 migration_version kind")
	}
}

// TestStateV2BothNormalize 覆盖 schema v2 两者同时存在正常 normalize。
func TestStateV2BothNormalize(t *testing.T) {
	s := mustParse(t, `schema_version: 2
task_id: demo
resources:
  migrations:
    - "20261001000012"
  error_code_domains:
    - "12000-12999"
`)
	if len(s.Resources.Reservations) != 2 {
		t.Fatalf("期望 2 个 kind，实际 %+v", s.Resources.Reservations)
	}
}

// TestStateV2ReservationsError 覆盖 schema v2 使用 generic reservations → ERROR。
func TestStateV2ReservationsError(t *testing.T) {
	mustParseError(t, `schema_version: 2
task_id: demo
resources:
  reservations:
    migration_version:
      - "20261001000012"
`, "不允许 generic")
}

// --- P2.1 Strict YAML Schema Hardening ---

// TestStateV3StrictUnknownResourceKey 覆盖 schema v3 下 resources 内 typo（reservationss）→ ERROR，
// 防止 typo 被静默忽略导致「错误跳过 Resource Authorization」。
func TestStateV3StrictUnknownResourceKey(t *testing.T) {
	mustParseError(t, `schema_version: 3
task_id: demo
resources:
  reservationss: {}
`, "未知字段")
}

// TestStateV3StrictTopLevelUnknownField 覆盖 schema v3 顶层未知字段（resourcess）→ ERROR。
func TestStateV3StrictTopLevelUnknownField(t *testing.T) {
	_, err := parseState([]byte(`schema_version: 3
task_id: demo
resourcess:
  reservations: {}
`))
	if err == nil {
		t.Fatal("顶层未知字段应报错")
	}
	if !strings.Contains(err.Error(), "resourcess") {
		t.Fatalf("期望错误含字段名 resourcess，实际 %v", err)
	}
}

// TestStateV3StrictNestedTypo 覆盖 contract/review/owner/delivery 内 typo → ERROR。
func TestStateV3StrictNestedTypo(t *testing.T) {
	tests := []struct {
		name string
		yml  string
		want string
	}{
		{"contract.targett", "schema_version: 3\ntask_id: demo\ncontract:\n  status: NOT_REQUIRED\n  targett: \"\"\n", "targett"},
		{"review.targett", "schema_version: 3\ntask_id: demo\nreview:\n  status: NOT_REQUESTED\n  targett: \"\"\n", "targett"},
		{"owner.review_targett", "schema_version: 3\ntask_id: demo\nowner:\n  status: PENDING\n  review_targett: \"\"\n", "review_targett"},
		{"delivery.feature_headd", "schema_version: 3\ntask_id: demo\ndelivery:\n  status: NOT_RUN\n  feature_headd: \"\"\n", "feature_headd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseState([]byte(tt.yml))
			if err == nil {
				t.Fatalf("期望错误含 %q，实际 nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("期望错误含 %q，实际 %v", tt.want, err)
			}
		})
	}
}

// TestStateV3StrictArbitraryValidKind 覆盖 schema v3 任意合法 resource kind → PASS。
func TestStateV3StrictArbitraryValidKind(t *testing.T) {
	s := mustParse(t, `schema_version: 3
task_id: demo
resources:
  reservations:
    custom_resource:
      - "alpha"
`)
	if got := s.Resources.Reservations["custom_resource"]; len(got) != 1 || got[0] != "alpha" {
		t.Fatalf("custom_resource = %+v", got)
	}
}

// TestStateV2StrictUnknownResourceKey 覆盖 schema v2 下 resources 内 typo（migrationss）→ ERROR，
// 同时保持 legacy migrations / error_code_domains 合法。
func TestStateV2StrictUnknownResourceKey(t *testing.T) {
	mustParseError(t, `schema_version: 2
task_id: demo
resources:
  migrationss:
    - "20261001000012"
`, "未知字段")
}

// TestStateUnsupportedSchemaVersion 覆盖不支持的 schema version → ERROR。
func TestStateUnsupportedSchemaVersion(t *testing.T) {
	for _, yml := range []string{
		"schema_version: 4\ntask_id: demo\n",
		"schema_version: 1\ntask_id: demo\nphase: NEW\n",
		"task_id: demo\n",
	} {
		mustParseError(t, yml, "schema_version")
	}
}

// TestParseStateDoesNotMutateInput 覆盖历史 v2 状态读取不要求磁盘 rewrite：
// parseState 是纯函数，只读取 raw bytes，不产生任何写副作用。
func TestParseStateDoesNotMutateInput(t *testing.T) {
	yml := `schema_version: 2
task_id: demo
resources:
  migrations:
    - "20261001000012"
  error_code_domains:
    - "12000-12999"
`
	before := append([]byte(nil), []byte(yml)...)
	if _, err := parseState([]byte(yml)); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if string(before) != yml {
		t.Fatal("parseState 不应修改输入字节")
	}
}
