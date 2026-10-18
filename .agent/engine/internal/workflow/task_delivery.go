package workflow

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PlanTask prepares NORMAL/COMPLEX delivery without writing refs or artifacts.
// Delivery PASS is deliberately not required until execute; verify creates it.
func (dc *DeliveryController) PlanTask(id string) (DeliveryPlan, Result) {
	p := DeliveryPlan{Kind: DeliveryTask, TargetID: id, TestCommand: dc.TestCommand}
	if !ValidScopeID(id) {
		return p, Result{Status: StatusError, Error: "task ID 必须为单一路径标识"}
	}
	if dc.Config == nil || dc.Git == nil {
		return p, Result{Status: StatusError, Error: "Task 交付需要有效 workflow.yaml 与 Git"}
	}
	if err := dc.taskArtifactPaths(id); err != nil {
		return p, Result{Status: StatusError, Error: err.Error()}
	}
	s, err := LoadTaskStateFile(dc.Root, id)
	if err != nil {
		return p, Result{Status: StatusError, Error: err.Error()}
	}
	if s.SchemaVersion != SchemaV4 || (s.TaskType != TaskTypeNormal && s.TaskType != TaskTypeComplex) || s.TaskID != id {
		return p, failResult(id, "Delivery Task Type", s.TaskType, "schema 4 NORMAL/COMPLEX 且 task_id 匹配", "历史任务只读；DESIGN 使用专属入口")
	}
	branch, err := dc.Git.CurrentBranch()
	if err != nil {
		return p, Result{Status: StatusError, Error: err.Error()}
	}
	if s.Git == nil || branch != s.Git.TaskBranch || branch == s.Git.ParentBranch || s.Git.ParentBranch != dc.Config.Git.IntegrationBranch {
		return p, failResult(id, "Delivery Git Binding", branch, "当前 task_branch 与配置父分支匹配", "禁止从其他分支或重解释初始化基线交付")
	}
	v := &Validator{Root: dc.Root, Git: dc.Git, Config: dc.Config}
	dir := filepath.Join(".agent", "tasks", id)
	if res := v.GateDeliveryStart(dir); res.Status != StatusPass {
		return p, res
	}
	// Validate workflow-specific contract requirements as well as generic evidence.
	if res := v.coderEntryContract(id, s); res.Status != StatusPass {
		return p, res
	}
	if res := v.GateHandoffPrepared(dir); res.Status != StatusPass {
		return p, res
	}
	h := s.Handoff
	if h.Scope != HandoffScopeTask || h.ScopeID != id || h.Workflow != s.TaskType || h.NextRole != RoleMerger || h.Ack == nil || h.Ack.AckedBy != RoleMerger {
		return p, failResult(id, "Delivery Handoff", "缺少当前 Merger 接手确认", "匹配 Task/Workflow 的 Merger Handoff 与 ACK", "先按 HandoffProtocol 完成真实接手")
	}
	head, err := dc.Git.RevParse("HEAD")
	if err != nil {
		return p, Result{Status: StatusError, Error: err.Error()}
	}
	if ok, err := dc.Git.IsAncestor(s.Git.BaseCommit, head); err != nil {
		return p, Result{Status: StatusError, Error: err.Error()}
	} else if !ok {
		return p, failResult(id, "Delivery Initial Baseline", s.Git.BaseCommit, "来源历史中的初始化基线", "不得替换任务 Git 归属")
	}
	base, err := dc.Git.RevParse("origin/" + s.Git.ParentBranch)
	if err != nil {
		return p, Result{Status: StatusError, Error: err.Error()}
	}
	p.ParentBranch, p.ParentBase = s.Git.ParentBranch, base
	p.Source = DeliverySource{Branch: branch, SHA: head}
	p.FinalEvidenceSHA = s.Review.Target
	return p, Result{Status: StatusPass}
}

// VerifyTask runs tests in isolation, never updates the parent and never commits
// or pushes metadata. Only delivery fields and delivery.md are written on PASS.
func (dc *DeliveryController) VerifyTask(id string) (MergeReport, Result) {
	var rep MergeReport
	if dc.Merger == nil {
		return rep, Result{Status: StatusError, Error: "缺少 Merger"}
	}
	if err := dc.Merger.Git.Fetch(); err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	p, res := dc.PlanTask(id)
	if res.Status != StatusPass {
		return rep, res
	}
	var err error
	rep, err = dc.Merger.Integrate(MergeRequest{
		ParentBranch: p.ParentBranch, ParentBaseSHA: p.ParentBase,
		Sources:         []MergeSource{{Branch: p.Source.Branch, SHA: p.Source.SHA}},
		OwnerAuthorized: false, TestCommand: p.TestCommand,
	})
	if err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	if !rep.TestPassed || rep.ParentUpdated || rep.Level != MergeL1 {
		return rep, failResult(id, "Delivery Verify", rep.Reason, "隔离集成与真实测试通过，父分支不更新", "未产生新的 delivery PASS")
	}
	// Detect source changes, dirty artifacts and remote parent drift during tests.
	current, res := dc.PlanTask(id)
	if res.Status != StatusPass {
		return rep, res
	}
	if current.Source != p.Source || current.FinalEvidenceSHA != p.FinalEvidenceSHA {
		return rep, failResult(id, "Delivery Verify", "验证期间来源变化", p.Source.SHA, "重新验证新来源")
	}
	for branch, sha := range map[string]string{p.ParentBranch: p.ParentBase, p.Source.Branch: p.Source.SHA} {
		if err := dc.verifyRemoteParentHead(branch, sha); err != nil {
			return rep, failResult(id, "Delivery Verify Freshness", err.Error(), sha, "验证期间远端变化，不记录 PASS")
		}
	}
	if err := dc.recordTaskDelivery(id, p, rep); err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	return rep, Result{Status: StatusPass}
}

// ExecuteTask binds authorization to the exact current source tip, including
// committed delivery-neutral metadata, while preserving the tested FeatureHead.
func (dc *DeliveryController) ExecuteTask(id string, auth DeliveryAuthorization) (MergeReport, Result) {
	var rep MergeReport
	if dc.Merger == nil {
		return rep, Result{Status: StatusError, Error: "缺少 Merger"}
	}
	if err := dc.Merger.Git.Fetch(); err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	p, res := dc.PlanTask(id)
	if res.Status != StatusPass {
		return rep, res
	}
	s, err := LoadTaskStateFile(dc.Root, id)
	if err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	if s.Program != nil {
		return rep, failResult(id, "Delivery Program Child", s.Program.ID, "由 Collector 登记后整体集成", "子 Task 不直接更新 Program 父分支")
	}
	v := &Validator{Root: dc.Root, Git: dc.Git, Config: dc.Config}
	if res := v.GateMergeReady(filepath.Join(".agent", "tasks", id)); res.Status != StatusPass {
		return rep, res
	}
	// NORMAL/COMPLEX authorize the actual source, not just the reviewed snapshot.
	authorized := p
	authorized.FinalEvidenceSHA = p.Source.SHA
	if res := dc.authorize(id, authorized, auth); res.Status != StatusPass {
		return rep, res
	}
	p.ParentBase = s.Delivery.DevelopBase
	return dc.integrate(p)
}

// Refuse symlinks along all writable paths. Existing task data must stay local.
func (dc *DeliveryController) taskArtifactPaths(id string) error {
	for _, name := range []string{filepath.Join(".agent", "tasks", id, "state.yaml"), filepath.Join(".agent", "tasks", id, "delivery.md")} {
		part := dc.Root
		for _, component := range strings.Split(name, string(filepath.Separator)) {
			part = filepath.Join(part, component)
			st, err := os.Lstat(part)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if st.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("拒绝符号链接 Task 路径: %s", part)
			}
		}
	}
	return nil
}

func (dc *DeliveryController) recordTaskDelivery(id string, p DeliveryPlan, rep MergeReport) error {
	if err := dc.taskArtifactPaths(id); err != nil {
		return err
	}
	dir := filepath.Join(dc.Root, ".agent", "tasks", id)
	path := filepath.Join(dir, "state.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return err
	}
	if len(node.Content) != 1 || node.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("state.yaml 在验证后变化或不再是映射")
	}
	delivery := Delivery{Status: DeliveryPass, ReviewTarget: p.FinalEvidenceSHA, FeatureHead: p.Source.SHA, DevelopBase: p.ParentBase}
	var replacement yaml.Node
	if err := replacement.Encode(delivery); err != nil {
		return err
	}
	mapping := node.Content[0]
	found := false
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "delivery" {
			mapping.Content[i+1] = &replacement
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("缺少 delivery section")
	}
	updated, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}
	// Write the report first; a partial failure must not create a new PASS state.
	next := "提交并推送 delivery.md 和 state.yaml；Program 子 Task 交 Collector，独立 NORMAL/COMPLEX 再运行 merge-ready。"
	if p.Kind == DeliveryDesign {
		next = "提交并推送 delivery.md 和 state.yaml 后交 Collector；DESIGN 不运行通用 merge-ready。"
	}
	report := fmt.Sprintf("# Delivery verification\n\nstatus: PASS\nreview_target: %s\nfeature_head: %s\ndevelop_base: %s\nparent_branch: %s\ntest: %s\n\n隔离合并后真实测试通过；未更新父分支。%s\n", p.FinalEvidenceSHA, p.Source.SHA, p.ParentBase, p.ParentBranch, strings.Join(p.TestCommand, " "), next)
	report += fmt.Sprintf("\nmerge_commit: %s\n\n测试输出（最多 4096 字节）：\n\n```text\n%s\n```\n", rep.MergeCommit, rep.TestOutput)
	if err := writeDeliveryFile(filepath.Join(dir, "delivery.md"), []byte(report)); err != nil {
		return err
	}
	return writeDeliveryFile(path, updated)
}

func writeDeliveryFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".delivery-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// VerifyDesign supplies delivery evidence for a DESIGN Program child. It uses
// the DESIGN final approval, never the NORMAL/COMPLEX gates, and never pushes.
func (dc *DeliveryController) VerifyDesign(id string) (MergeReport, Result) {
	var rep MergeReport
	if !ValidScopeID(id) || dc.Merger == nil || len(dc.TestCommand) == 0 {
		return rep, Result{Status: StatusError, Error: "valid DESIGN ID, Merger and test command required"}
	}
	if err := dc.taskArtifactPaths(id); err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	if err := dc.Merger.Git.Fetch(); err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	p, res := dc.PlanDesign(id)
	if res.Status != StatusPass {
		return rep, res
	}
	v := &Validator{Root: dc.Root, Git: dc.Git, Config: dc.Config}
	if res = v.GateHandoffPrepared(filepath.Join(".agent", "tasks", id)); res.Status != StatusPass {
		return rep, res
	}
	s, err := LoadTaskStateFile(dc.Root, id)
	if err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	h := s.Handoff
	if s.SchemaVersion != SchemaV4 || s.TaskID != id || h == nil || h.Scope != HandoffScopeTask || h.ScopeID != id || h.Workflow != TaskTypeDesign || h.NextRole != RoleMerger || h.Ack == nil || h.Ack.AckedBy != RoleMerger {
		return rep, failResult(id, "DESIGN delivery handoff", "missing Merger ACK", "Merger Handoff/ACK", "先完成正式交接")
	}
	branch, e := dc.Git.CurrentBranch()
	if e != nil {
		return rep, Result{Status: StatusError, Error: e.Error()}
	}
	if dc.Config == nil || branch != s.Git.TaskBranch || p.ParentBranch != dc.Config.Git.IntegrationBranch {
		return rep, failResult(id, "DESIGN verify baseline", branch, "bound task/parent branch", "拒绝错误分支验证")
	}
	rep, err = dc.Merger.Integrate(MergeRequest{ParentBranch: p.ParentBranch, ParentBaseSHA: p.ParentBase, Sources: []MergeSource{{Branch: p.Source.Branch, SHA: p.Source.SHA}}, TestCommand: p.TestCommand})
	if err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	if rep.Level != MergeL1 || !rep.Merged || !rep.TestPassed || rep.ParentUpdated {
		return rep, failResult(id, "DESIGN verify", rep.Reason, "isolated tests PASS", "no new PASS")
	}
	current, res := dc.PlanDesign(id)
	if res.Status != StatusPass {
		return rep, res
	}
	if current.Source != p.Source || current.FinalEvidenceSHA != p.FinalEvidenceSHA {
		return rep, failResult(id, "DESIGN verify", "source changed", p.Source.SHA, "重新验证")
	}
	for branch, sha := range map[string]string{p.ParentBranch: p.ParentBase, p.Source.Branch: p.Source.SHA} {
		if err := dc.verifyRemoteParentHead(branch, sha); err != nil {
			return rep, failResult(id, "DESIGN verify freshness", err.Error(), sha, "no PASS")
		}
	}
	if err := dc.recordTaskDelivery(id, p, rep); err != nil {
		return rep, Result{Status: StatusError, Error: err.Error()}
	}
	return rep, Result{Status: StatusPass}
}
