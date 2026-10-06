package workflow

import (
	"testing"
)

// TestParseStateV3RoundTrip 覆盖 P2 新 schema：完整 v3 state.yaml 能正确解析到 generic reservations。
func TestParseStateV3RoundTrip(t *testing.T) {
	yml := `schema_version: 3
task_id: flash-sale-v2
contract:
  status: APPROVED
  target: a1b2c3d
resources:
  reservations:
    migration_version:
      - "20261001000011"
    error_code_domain:
      - "12000-12999"
review:
  status: CLEAN
  target: c1d2e3f
owner:
  status: ACCEPTED
  review_target: c1d2e3f
delivery:
  status: PASS
  review_target: c1d2e3f
  feature_head: f1a2b3c
  develop_base: d1e2f3a
blocked:
  active: false
  by: ""
  reason: ""
`
	s, err := parseState([]byte(yml))
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if s.SchemaVersion != SchemaV3 {
		t.Fatalf("SchemaVersion = %d, 期望 %d", s.SchemaVersion, SchemaV3)
	}
	if s.TaskID != "flash-sale-v2" {
		t.Fatalf("TaskID = %q", s.TaskID)
	}
	if s.Contract.Status != ContractApproved || s.Contract.Target != "a1b2c3d" {
		t.Fatalf("Contract = %+v", s.Contract)
	}
	if got := s.Resources.Reservations["migration_version"]; len(got) != 1 || got[0] != "20261001000011" {
		t.Fatalf("migration_version = %+v", got)
	}
	if got := s.Resources.Reservations["error_code_domain"]; len(got) != 1 || got[0] != "12000-12999" {
		t.Fatalf("error_code_domain = %+v", got)
	}
	if s.Review.Status != ReviewClean || s.Review.Target != "c1d2e3f" {
		t.Fatalf("Review = %+v", s.Review)
	}
	if s.Owner.Status != OwnerAccepted || s.Owner.ReviewTarget != "c1d2e3f" {
		t.Fatalf("Owner = %+v", s.Owner)
	}
	if s.Delivery.Status != DeliveryPass || s.Delivery.ReviewTarget != "c1d2e3f" || s.Delivery.FeatureHead != "f1a2b3c" || s.Delivery.DevelopBase != "d1e2f3a" {
		t.Fatalf("Delivery = %+v", s.Delivery)
	}
	if s.Blocked.Active {
		t.Fatalf("Blocked.Active = true, 期望 false")
	}
}

// TestEnumValidators 覆盖四个枚举的合法/非法判定。
func TestEnumValidators(t *testing.T) {
	for _, c := range []ContractStatus{ContractPending, ContractApproved, ContractRejected, ContractNotRequired} {
		if !ValidContractStatus(c) {
			t.Fatalf("ValidContractStatus(%s) 应为 true", c)
		}
	}
	for _, r := range []ReviewStatus{ReviewNotRequested, ReviewPending, ReviewClean, ReviewChangesRequired} {
		if !ValidReviewStatus(r) {
			t.Fatalf("ValidReviewStatus(%s) 应为 true", r)
		}
	}
	for _, o := range []OwnerStatus{OwnerPending, OwnerAccepted, OwnerRejected, OwnerNotRequired} {
		if !ValidOwnerStatus(o) {
			t.Fatalf("ValidOwnerStatus(%s) 应为 true", o)
		}
	}
	for _, d := range []DeliveryStatus{DeliveryNotRun, DeliveryPass, DeliveryFail, DeliveryBlocked} {
		if !ValidDeliveryStatus(d) {
			t.Fatalf("ValidDeliveryStatus(%s) 应为 true", d)
		}
	}

	if ValidContractStatus("BOGUS") || ValidReviewStatus("STALE") || ValidOwnerStatus("BOGUS") || ValidDeliveryStatus("CONDITIONAL_PASS") {
		t.Fatal("非法枚举值不应通过校验")
	}
}

// TestHasRequiredResources 覆盖资源声明判定。
func TestHasRequiredResources(t *testing.T) {
	if (State{}).HasRequiredResources() {
		t.Fatal("空 State 不应声明资源")
	}
	if (State{Resources: Resources{Reservations: map[string][]string{}}}).HasRequiredResources() {
		t.Fatal("空 reservations 不应声明资源")
	}
	s := State{Resources: Resources{Reservations: map[string][]string{"migration_version": {"20261001000011"}}}}
	if !s.HasRequiredResources() {
		t.Fatal("声明 reservation 后 HasRequiredResources 应为 true")
	}
}

// TestValidResourceKind 覆盖 resource kind 命名约束（lower_snake_case）。
func TestValidResourceKind(t *testing.T) {
	valid := []string{"migration_version", "error_code_domain", "im_migration_version", "idp_migration_version", "some_other_shared_resource", "a", "a1_b2"}
	for _, k := range valid {
		if !ValidResourceKind(k) {
			t.Fatalf("ValidResourceKind(%q) 应为 true", k)
		}
	}
	invalid := []string{"", "MigrationVersion", "migration-version", "../foo", "foo.bar", "1foo", "_foo", "foo bar"}
	for _, k := range invalid {
		if ValidResourceKind(k) {
			t.Fatalf("ValidResourceKind(%q) 应为 false", k)
		}
	}
}
