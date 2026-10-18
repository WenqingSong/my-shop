package workflow

import "fmt"

// 本文件实现 V4 Contract Revision 的证据有效性校验。
//
// 它直接消费已有 Schema（Contract.Status / Target / Revision / History），不重复定义数据结构，
// 只补充机器可验证的版本关系。V2/V3 历史 Contract（无 revision / history）不受影响：
// revision==0 且 history 为空时视为「未版本化」，直接 PASS，保持 V2/V3 兼容。
//
// 机器可验证的版本关系：
//   - 版本化 contract 必须 APPROVED（revision 语义是「当前批准版本号」，非批准状态不携带版本号）；
//   - history 严格按版本升序，无重复、无倒序，且每个历史版本严格小于当前 revision；
//   - 每个历史 Evidence SHA 必须是真实 commit、位于当前 feature 历史中、且确实包含 contract.md；
//   - 当前 contract.target 不得复用任何历史 Evidence SHA（新版本不得沿用旧批准对象）。
//
// 能力边界：本引擎无法从机器层证明「真实 Owner 身份」。Schema 不包含可编辑的 approved_by 字符串，
// 身份由 Owner 在 Git 侧完成（Evidence Commit 提交者 + shared branch protection）并在集成侧事后审计；
// 本校验只证明 Evidence SHA 真实性、内容一致性与版本链一致性，不虚构身份认证能力。

// contractRevisionValid 校验 V4 Contract Revision 的版本关系与证据可验证性。
// 未版本化（revision==0 且 history 空）直接 PASS；版本化则做 fail-closed 的完整性校验。
func (v *Validator) contractRevisionValid(taskSlug string, s State) Result {
	c := s.Contract
	if c.Revision == 0 && len(c.History) == 0 {
		return Result{Status: StatusPass}
	}

	check := "Contract Revision (INV-2)"
	if c.Status != ContractApproved {
		return failResult(taskSlug, check, fmt.Sprintf("contract.status=%s", c.Status), "APPROVED", "版本化 contract 必须为 APPROVED")
	}
	if c.Revision <= 0 {
		return failResult(taskSlug, check, fmt.Sprintf("contract.revision=%d 但存在 history", c.Revision), "revision 为正数", "存在历史版本却未声明当前批准版本号")
	}
	if c.Target == "" {
		return failResult(taskSlug, check, "contract.target 为空", "<evidence-commit-sha>", "版本化 contract 必须绑定当前批准版本 Evidence Commit")
	}

	head, err := v.featureHead()
	if err != nil {
		return Result{Status: StatusError, Error: err.Error()}
	}

	seenTargets := map[string]struct{}{}
	prev := 0
	for i, h := range c.History {
		if h.Revision <= prev {
			return failResult(taskSlug, check,
				fmt.Sprintf("contract.history[%d].revision=%d", i, h.Revision),
				"严格递增的版本号",
				"版本历史存在重复或倒序记录")
		}
		if h.Revision >= c.Revision {
			return failResult(taskSlug, check,
				fmt.Sprintf("contract.history[%d].revision=%d >= contract.revision=%d", i, h.Revision, c.Revision),
				"history 版本严格小于当前 revision",
				"历史版本不得等于或大于当前版本")
		}
		if ok, err := v.Git.CommitExists(h.Target); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("check contract.history[%d].target %s: %v", i, h.Target, err)}
		} else if !ok {
			return failResult(taskSlug, check,
				fmt.Sprintf("contract.history[%d].target=%s", i, h.Target),
				"真实 commit",
				"历史 Evidence SHA 不是真实 commit")
		}
		if ok, err := v.Git.IsAncestor(h.Target, head); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("is-ancestor %s HEAD: %v", h.Target, err)}
		} else if !ok {
			return failResult(taskSlug, check,
				fmt.Sprintf("contract.history[%d].target=%s", i, h.Target),
				"当前 feature HEAD 的祖先",
				"历史 Evidence SHA 不在当前 feature 历史中")
		}
		if ok, err := v.Git.PathExistsAt(h.Target, contractPath(taskSlug)); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("check contract.md@%s: %v", h.Target, err)}
		} else if !ok {
			return failResult(taskSlug, check,
				fmt.Sprintf("contract.history[%d].target=%s 不含 contract.md", i, h.Target),
				"包含 contract.md",
				"历史 Evidence Commit 缺少 contract.md")
		}
		seenTargets[h.Target] = struct{}{}
		prev = h.Revision
	}

	if _, reused := seenTargets[c.Target]; reused {
		return failResult(taskSlug, check,
			fmt.Sprintf("contract.target=%s 复用历史版本 Evidence", c.Target),
			"新的 Evidence Commit",
			"新版本不得沿用旧批准对象")
	}

	return Result{Status: StatusPass}
}
