package deployk8s

import (
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1-02：版本降级保护。
//
// 每个用例都在临时 git 仓库里构造真实历史（main: c1→c2→c3，side 从 c2 分叉），
// 通过 Deployment 的 app.kubernetes.io/version 描述「线上正在跑的版本」，
// 由脚本自己用 git merge-base 判断祖先关系。

// TestFirstDeployAllowed 覆盖「Deployment 不存在 → 允许」。
func TestFirstDeployAllowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "首次部署")
}

// TestSameVersionIdempotent 覆盖「C == T → 允许幂等发布」。
func TestSameVersionIdempotent(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		target := f.shas["target"]
		st.Deployment.Version = target
		st.Deployment.Image = repository + ":" + target
		for i := range st.Deployment.Pods {
			st.Deployment.Pods[i].Version = target
			st.Deployment.Pods[i].Image = repository + ":" + target
		}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "幂等发布")
}

// TestUpgradeAllowed 覆盖「C 是 T 的祖先 → 允许升级」。
func TestUpgradeAllowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "允许升级")
}

// TestDowngradeRejected 覆盖「T 是 C 的祖先 → 拒绝降级」，且不产生任何资源变更。
func TestDowngradeRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	c1 := f.shas["c1"]
	c3 := f.shas["c3"]
	f.retag(c1)
	f.updateState(func(st *fakestate.State) {
		st.Deployment.Version = c3
		st.Deployment.Image = repository + ":" + c3
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	res.requireContains(t, "版本降级保护拒绝发布", "降级", "--allow-downgrade")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
	// 版本检查在锁内执行：先拿锁，再拒绝，最后释放。
	f.requireEventOrder(t, "lease.acquire", "lease.release")
}

// TestDivergedRejected 覆盖「C 与 T 分叉 → 默认拒绝」。
func TestDivergedRejected(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	side := f.shas["side"]
	f.updateState(func(st *fakestate.State) {
		st.Deployment.Version = side
		st.Deployment.Image = repository + ":" + side
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	res.requireContains(t, "分叉")
	f.requireNoEvent(t, "apply.application")
}

// TestUnknownCurrentVersionFailsClosed 覆盖「当前版本无法识别 → fail-closed」。
func TestUnknownCurrentVersionFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.Deployment.Version = ""
		st.Deployment.Image = repository + "@sha256:0000000000000000000000000000000000000000000000000000000000000000"
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	res.requireContains(t, "无法识别当前线上版本")
	f.requireNoEvent(t, "apply.application")
}

// TestUnknownTargetVersionFailsClosed 覆盖「目标版本不在 Git 历史 → 拒绝，不猜顺序」。
func TestUnknownTargetVersionFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{tag: "abcdef1"})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	res.requireContains(t, "无法在本地 Git 历史中解析")
	f.requireNoEvent(t, "apply.application")
}

// TestMissingGitHistoryFailsClosed 覆盖「Git 历史不足 → 明确失败」。
func TestMissingGitHistoryFailsClosed(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{
		noGit:          true,
		tag:            "abcdef1",
		currentVersion: "abcdef0",
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	res.requireContains(t, "版本降级保护拒绝发布")
	f.requireNoEvent(t, "apply.application")
}

// TestAllowDowngradeOverride 覆盖「显式 --allow-downgrade 才能绕过版本检查」，并记录授权。
func TestAllowDowngradeOverride(t *testing.T) {
	t.Parallel()
	reference := newFixture(t, fixtureOptions{})
	c1 := reference.shas["c1"]
	c3 := reference.shas["c3"]

	f := newFixture(t, fixtureOptions{tag: c1, currentVersion: c3})

	res := runDeploy(t, f, "--allow-downgrade", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "[AUDIT] 降级授权已生效", "数据库兼容性风险")
	f.requireEvent(t, "apply.application")
}

// TestAllowDowngradeBlockedInCI 覆盖「CI 不得默认携带降级授权」：
// 必须额外设置 DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1 才生效。
func TestAllowDowngradeBlockedInCI(t *testing.T) {
	t.Parallel()
	reference := newFixture(t, fixtureOptions{})
	c1 := reference.shas["c1"]
	c3 := reference.shas["c3"]

	f := newFixture(t, fixtureOptions{tag: c1, currentVersion: c3})
	f.env = append(f.env, "CI=true")

	res := runDeploy(t, f, "--allow-downgrade", "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	res.requireContains(t, "CI 环境禁止自动降级")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")

	// 人工应急：显式再加一层开关后允许。
	emergency := newFixture(t, fixtureOptions{tag: c1, currentVersion: c3})
	emergency.env = append(emergency.env, "CI=true", "DEPLOY_K8S_ALLOW_DOWNGRADE_IN_CI=1")
	allowed := runDeploy(t, emergency, "--allow-downgrade", "--migration-timeout", "10", "--rollout-timeout", "10")
	allowed.requireSuccess(t)
	allowed.requireContains(t, "[AUDIT] 降级授权已生效")
}
