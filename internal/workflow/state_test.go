package workflow

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestStateSchemaRoundTrip 覆盖 Design Freeze §1：完整 V2 state.yaml 能正确反序列化到全部字段。
func TestStateSchemaRoundTrip(t *testing.T) {
	yml := `schema_version: 2
task_id: flash-sale-v2
contract:
  status: APPROVED
  target: a1b2c3d
resources:
  migrations:
    - "20261001000011"
  error_code_domains:
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
	var s State
	if err := yaml.Unmarshal([]byte(yml), &s); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if s.SchemaVersion != 2 {
		t.Fatalf("SchemaVersion = %d, 期望 2", s.SchemaVersion)
	}
	if s.TaskID != "flash-sale-v2" {
		t.Fatalf("TaskID = %q", s.TaskID)
	}
	if s.Contract.Status != ContractApproved || s.Contract.Target != "a1b2c3d" {
		t.Fatalf("Contract = %+v", s.Contract)
	}
	if len(s.Resources.Migrations) != 1 || s.Resources.Migrations[0] != "20261001000011" {
		t.Fatalf("Resources.Migrations = %+v", s.Resources.Migrations)
	}
	if len(s.Resources.ErrorCodeDomains) != 1 || s.Resources.ErrorCodeDomains[0] != "12000-12999" {
		t.Fatalf("Resources.ErrorCodeDomains = %+v", s.Resources.ErrorCodeDomains)
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
	s := State{Resources: Resources{Migrations: []string{"20261001000011"}}}
	if !s.HasRequiredResources() {
		t.Fatal("声明 migration 后 HasRequiredResources 应为 true")
	}
	s = State{Resources: Resources{ErrorCodeDomains: []string{"12000-12999"}}}
	if !s.HasRequiredResources() {
		t.Fatal("声明错误码域后 HasRequiredResources 应为 true")
	}
}
