package workflow

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 本文件实现 Merger 的受控隔离 Git 集成（Task 09）。
//
// Merger 是最终交付角色：在 Owner 明确授权下，把已通过独立审查与验收的 feature
// 受控集成进目标父分支。核心原则：
//
//   - 固定初始 Parent Branch / Base SHA，fetch 后复核父分支未漂移；
//   - 全部 merge 发生在新建 scratch branch/worktree 内，绝不直接在父分支工作区处理冲突；
//   - 冲突分级 MERGE-L1 / MERGE-L2 / MERGE-L3：L3（契约/架构/跨模块语义冲突）禁止 Merger 自行业务决策；
//   - 对最终合并结果运行真实测试，测试失败则父分支不变；
//   - 仅在 Owner 明确授权且全部通过时，以 fast-forward-only 推送精确目标 SHA，禁止 force push；
//   - 禁止通过删除冲突文件、删除测试获得虚假 PASS。
//
// 该隔离集成原语可被 Assembler 复用，但 Collector 不得调用业务代码合并操作。

// MergeConflictLevel 是隔离合并的冲突分级。
type MergeConflictLevel = string

const (
	// MergeL1 无阻断冲突，经测试后正常交付。
	MergeL1 MergeConflictLevel = "MERGE-L1"
	// MergeL2 局部冲突，由 Owner 指定受限修复，必须重新验证。
	MergeL2 MergeConflictLevel = "MERGE-L2"
	// MergeL3 重要契约、架构或跨模块语义冲突，Merger 不得自行决策，转入 Owner 批准的独立 COMPLEX Task。
	MergeL3 MergeConflictLevel = "MERGE-L3"
)

// MergeSource 是一次隔离合并的单个来源（分支名 + 固定 SHA）。
type MergeSource struct {
	Branch string `yaml:"branch"`
	SHA    string `yaml:"sha"`
}

// MergeRequest 描述一次受控隔离合并（固定 Parent / Base / 来源 SHA）。
type MergeRequest struct {
	ParentBranch    string        // 目标父分支名（如 develop）
	ParentBaseSHA   string        // 固定初始父分支 SHA
	Sources         []MergeSource // 待合并来源（按顺序合并）
	OwnerAuthorized bool          // Owner 是否已明确授权更新父分支
	TestCommand     []string      // 合并后运行的真实测试命令（空表示不运行，但交付前仍应提供）
	ScratchDir      string        // 隔离 worktree 绝对路径（空则由 Merger 自动生成并清理）
}

// MergeResult 是单次 git merge 的机器结果。
type MergeResult struct {
	Conflict    bool     // 是否存在未解决冲突（unmerged files）
	Conflicted  []string // 冲突文件相对路径
	MergeCommit string   // 合并后 HEAD 的完整 SHA（冲突时为空）
}

// MergeReport 是隔离合并的最终报告。
type MergeReport struct {
	Level          MergeConflictLevel
	ParentBaseSHA  string
	ParentFinalSHA string // 合并前复核的父分支 SHA
	Merged         bool   // 是否已在 scratch 内完成合并
	MergeCommit    string // 合并后 HEAD 的完整 SHA（隔离合并完成时非空；未完成/冲突/漂移为空）
	TestPassed     bool   // 合并结果测试是否通过
	TestOutput     string // 测试输出（截断前）
	ParentUpdated  bool   // 是否已更新父分支（仅 Owner 授权且全部通过）
	Reason         string // 停止/失败原因（空表示成功）
	CleanupWarning string // 清理失败不掩盖已发生的交付；保留精确资源供恢复
}

// Merger 执行受控隔离 Git 集成。
type Merger struct {
	Root string // 宿主仓库根目录
	Git  GitOps // git 读写原语（应指向 Root 同一仓库）
}

// 随机标识跨进程唯一，也避免并发调用共享可变计数。
func nextScratchBranch(base string) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", fmt.Errorf("生成 scratch 标识: %w", err)
	}
	return fmt.Sprintf("scratch/merger-%s-%x", short(base), id), nil
}

// short 截断 SHA 用于报告可读性。
func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

// Integrate 执行隔离集成。返回 MergeReport 与 error：
//   - error 仅表示运行级错误（git/测试无法执行、来源 SHA 无效等）；
//   - 业务结果（漂移、L2/L3 冲突、测试失败、未授权）通过 MergeReport 表达，不返回 error。
func (m *Merger) Integrate(req MergeRequest) (rep MergeReport, retErr error) {
	rep = MergeReport{Level: MergeL1, ParentBaseSHA: req.ParentBaseSHA}

	if req.ParentBranch == "" || req.ParentBaseSHA == "" {
		return rep, fmt.Errorf("MergeRequest 缺少 ParentBranch 或 ParentBaseSHA")
	}
	if len(req.Sources) == 0 {
		return rep, fmt.Errorf("MergeRequest 缺少待合并来源")
	}

	// 1. fetch 最新远端。
	if err := m.Git.Fetch(); err != nil {
		return rep, fmt.Errorf("fetch: %w", err)
	}

	// 2. 验证全部来源 SHA 与父基线 SHA 均为真实 commit。
	for _, src := range req.Sources {
		if err := m.validateSource(src); err != nil {
			return rep, err
		}
	}
	if ok, err := m.Git.CommitExists(req.ParentBaseSHA); err != nil {
		return rep, fmt.Errorf("check parent base %s: %w", req.ParentBaseSHA, err)
	} else if !ok {
		return rep, fmt.Errorf("父基线 SHA %s 不是真实 commit", req.ParentBaseSHA)
	}

	// 3. 复核目标父分支未漂移（相对固定基线）。
	parentRef := "origin/" + req.ParentBranch
	curParent, err := m.Git.RevParse(parentRef)
	if err != nil {
		return rep, fmt.Errorf("rev-parse %s: %w", parentRef, err)
	}
	rep.ParentFinalSHA = curParent
	if curParent != req.ParentBaseSHA {
		rep.Level = MergeL2
		rep.Reason = fmt.Sprintf("父分支 %s 已从固定基线 %s 漂移到 %s，拒绝继续（需重新固定基线）",
			req.ParentBranch, short(req.ParentBaseSHA), short(curParent))
		return rep, nil
	}

	// 4. 在新建 scratch branch/worktree 内执行隔离合并。
	scratchBranch, err := nextScratchBranch(req.ParentBaseSHA)
	if err != nil {
		return rep, err
	}
	scratchDir := req.ScratchDir
	cleanupRoot := ""
	if scratchDir == "" {
		// 生成一个唯一的父目录，worktree 落在其下不存在的子路径（git worktree add 要求路径不存在）。
		parent, err := os.MkdirTemp("", "merger-*")
		if err != nil {
			return rep, fmt.Errorf("创建 scratch 父目录: %w", err)
		}
		scratchDir = filepath.Join(parent, "wt")
		cleanupRoot = parent
	}
	if err := m.Git.WorktreeAdd(scratchDir, scratchBranch, req.ParentBaseSHA); err != nil {
		if cleanupRoot != "" {
			_ = os.Remove(cleanupRoot) // 只移除空目录，不删除可能存在的工作区
		}
		return rep, fmt.Errorf("创建 scratch worktree: %w", err)
	}
	if req.ScratchDir == "" {
		defer func() {
			if err := m.Git.WorktreeRemove(scratchDir); err != nil {
				rep.CleanupWarning = fmt.Sprintf("保留 %s (%s): %v", scratchBranch, scratchDir, err)
				return
			}
			expected := rep.MergeCommit
			if expected == "" {
				expected = req.ParentBaseSHA
			}
			if err := m.Git.DeleteScratchBranch(scratchBranch, expected); err != nil {
				rep.CleanupWarning = fmt.Sprintf("保留 %s: %v", scratchBranch, err)
			}
			_ = os.Remove(cleanupRoot)
		}()
	}

	// 5. 顺序合并各来源，任一冲突即分级并停止。
	for _, src := range req.Sources {
		res, err := m.Git.Merge(scratchDir, src.SHA)
		if err != nil {
			return rep, fmt.Errorf("merge %s: %w", src.SHA, err)
		}
		if res.Conflict {
			_ = m.Git.MergeAbort(scratchDir)
			rep.Level = classifyConflict(res.Conflicted)
			rep.Reason = fmt.Sprintf("合并来源 %s 产生冲突（%d 个文件），冲突分级 %s，禁止 Merger 自行修复",
				short(src.SHA), len(res.Conflicted), rep.Level)
			return rep, nil
		}
	}

	out, err := gitRun(scratchDir, "rev-parse", "HEAD")
	if err != nil {
		return rep, fmt.Errorf("scratch rev-parse HEAD: %w", err)
	}
	mergeSHA := strings.TrimSpace(out)
	rep.Merged = true
	rep.MergeCommit = mergeSHA
	if err := verifyScratchSnapshot(scratchDir, mergeSHA); err != nil {
		return rep, fmt.Errorf("验收前快照不一致: %w", err)
	}

	// 6. 对最终合并结果运行真实测试。
	if len(req.TestCommand) > 0 {
		out, terr := m.Git.RunTest(scratchDir, req.TestCommand)
		rep.TestOutput = truncate(out, 4096)
		if terr != nil {
			rep.TestPassed = false
			rep.Reason = fmt.Sprintf("合并结果测试失败：%v", terr)
			return rep, nil
		}
		if err := verifyScratchSnapshot(scratchDir, mergeSHA); err != nil {
			rep.Level = MergeL2
			rep.Reason = fmt.Sprintf("验收后快照不一致，拒绝交付: %v", err)
			return rep, nil
		}
		rep.TestPassed = true
	} else {
		rep.TestPassed = false
		rep.Reason = "未提供测试命令，交付前必须对合并结果运行真实测试"
		return rep, nil
	}

	// 7. 复核目标父分支在合并期间未移动。
	// Refresh after tests; cached origin refs cannot reveal live remote drift.
	if err := m.Git.Fetch(); err != nil {
		return rep, fmt.Errorf("测试后 fetch: %w", err)
	}
	for _, src := range req.Sources {
		if err := m.validateSource(src); err != nil {
			rep.Level = MergeL2
			rep.Reason = fmt.Sprintf("验证期间来源变化或无法复核: %v", err)
			return rep, nil
		}
	}
	finalParent, err := m.Git.RevParse(parentRef)
	if err != nil {
		return rep, fmt.Errorf("rev-parse %s (final): %w", parentRef, err)
	}
	if finalParent != req.ParentBaseSHA {
		rep.Level = MergeL2
		rep.Reason = fmt.Sprintf("合并期间父分支 %s 从 %s 漂移到 %s，拒绝更新父分支",
			req.ParentBranch, short(req.ParentBaseSHA), short(finalParent))
		return rep, nil
	}

	// 8. 仅在 Owner 明确授权时更新父分支（fast-forward-only，禁止 force）。
	if !req.OwnerAuthorized {
		rep.Reason = "Owner 未明确授权，不更新父分支（隔离合并已完成，等待 Owner 授权）"
		return rep, nil
	}
	if mergeSHA == req.ParentBaseSHA {
		// 已是最新（Already up-to-date），无需推送。
		rep.ParentUpdated = false
		rep.Reason = ""
		return rep, nil
	}
	if err := m.Git.PushFastForward("origin", mergeSHA, req.ParentBranch); err != nil {
		return rep, fmt.Errorf("更新父分支 %s 失败（fast-forward-only）: %w", req.ParentBranch, err)
	}
	rep.ParentUpdated = true
	return rep, nil
}

// validateSource 校验单个来源 SHA 为真实 commit，且确实是其分支的当前提交（来源 SHA 与分支绑定）。
func (m *Merger) validateSource(src MergeSource) error {
	if src.SHA == "" {
		return fmt.Errorf("来源 %q 缺少固定 SHA", src.Branch)
	}
	if ok, err := m.Git.CommitExists(src.SHA); err != nil {
		return fmt.Errorf("check source %s: %w", src.SHA, err)
	} else if !ok {
		return fmt.Errorf("来源 SHA %s 不是真实 commit", src.SHA)
	}
	if src.Branch != "" {
		remoteSHA, err := m.Git.RevParse("origin/" + src.Branch)
		if err != nil {
			return fmt.Errorf("rev-parse origin/%s: %w", src.Branch, err)
		}
		if remoteSHA != src.SHA {
			return fmt.Errorf("来源分支 %s 当前 SHA %s 与固定 SHA %s 不一致", src.Branch, short(remoteSHA), short(src.SHA))
		}
	}
	return nil
}

// classifyConflict 根据冲突文件集合分级。
// 无冲突 → L1；冲突触及契约/架构/共享权威/跨模块语义文件 → L3；其余局部实现冲突 → L2。
func classifyConflict(conflicted []string) MergeConflictLevel {
	if len(conflicted) == 0 {
		return MergeL1
	}
	for _, p := range conflicted {
		if isL3Path(p) {
			return MergeL3
		}
	}
	return MergeL2
}

// l3PathPrefixes 是判定 L3 语义冲突的共享权威/契约/架构路径前缀。
var l3PathPrefixes = []string{
	".agent/AGENTS.md",     // 框架通用 Agent 指令
	".agent/registry/",     // 共享资源分配权威
	".agent/specs/",        // 协作/协议规范（跨模块语义）
	".agent/workflow.yaml", // 机器配置契约
	"docs/design/",         // 项目级长期架构
	"go.mod",               // 依赖契约
	"go.sum",               // 依赖契约
}

// verifyScratchSnapshot checks the actual tested checkout, not merely remote
// refs. No ignored/untracked files are permitted: test output belongs in the
// external WORKFLOW_TEST_OUTPUT_DIR. This detects persistent changes, not an
// adversarial command which changes and restores content during execution.
func verifyScratchSnapshot(dir, expected string) error {
	head, err := gitRun(dir, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != expected {
		return fmt.Errorf("scratch HEAD changed from %s", expected)
	}
	entries, err := gitRun(dir, "ls-files", "-v")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(entries, "\n") {
		if entry != "" && entry[0] != 'H' {
			return fmt.Errorf("unsupported hidden/conflicted index entry: %s", entry)
		}
	}
	if _, err := gitRun(dir, "-c", "core.filemode=true", "-c", "core.fsmonitor=false", "diff", "--no-ext-diff", "--ignore-submodules=none", "--quiet", "HEAD", "--"); err != nil {
		return fmt.Errorf("tracked worktree/index differs from tested commit: %w", err)
	}
	status, err := gitRun(dir, "-c", "core.fsmonitor=false", "status", "--porcelain=v1", "--untracked-files=all", "--ignored=matching")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("scratch contains changed/untracked/ignored content: %s", status)
	}
	return nil
}

// isL3Path 报告路径是否属于 L3（重要契约/架构/跨模块语义）。
func isL3Path(p string) bool {
	p = filepath.ToSlash(p)
	// Go dependency contracts have the same authority in nested modules as at
	// the repository root. Preserve the historical prefix rules below while
	// matching nested dependency filenames exactly (not go.mod.backup).
	if name := filepath.Base(p); name == "go.mod" || name == "go.sum" {
		return true
	}
	for _, prefix := range l3PathPrefixes {
		if strings.HasPrefix(p, prefix) {
			return true
		}
	}
	// 任务契约文件（.agent/tasks/<task>/contract.md）属于重要契约。
	if strings.HasSuffix(p, "/contract.md") {
		return true
	}
	return false
}

// truncate 截断输出到 max 字节（用于报告，不改变真实结果）。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}
