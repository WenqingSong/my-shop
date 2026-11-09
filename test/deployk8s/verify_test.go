package deployk8s

import (
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1-03：发布成功判定。
//
// 关键场景是「rollout status 会看到别人的结果」：脚本必须自己比对
// Deployment 的镜像/版本标记/副本/generation 与实际 Pod，任何一项不符都不得打印成功。

// TestVerifySuccess 覆盖目标版本成功：只有全部一致才打印 DEPLOY_K8S_SUCCESS。
func TestVerifySuccess(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "发布验证通过")
	f.requireNoEvent(t, "lease.conflict")
}

// TestVerifyImageMismatchFails 覆盖「Deployment 模板镜像与目标不一致」。
func TestVerifyImageMismatchFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		other := repository + ":" + f.shas["c2"]
		applied := readyDeployment(f.shas["target"], 4)
		applied.Image = other
		applied.Pods[0].Image = other
		st.DeploymentAfterApply = []fakestate.Deployment{applied}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, "Pod 模板镜像", "期望 = "+repository+":"+f.shas["target"])
}

// TestVerifyVersionMismatchFails 覆盖「版本标记与目标 commit 不一致」。
func TestVerifyVersionMismatchFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		applied := readyDeployment(f.shas["target"], 4)
		applied.Version = "deadbee"
		st.DeploymentAfterApply = []fakestate.Deployment{applied}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, "app.kubernetes.io/version = deadbee", "期望 = "+f.shas["target"])
}

// TestVerifyNotConvergedFails 覆盖「Deployment 尚未完成」（observedGeneration 未追上）。
func TestVerifyNotConvergedFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		pending := readyDeployment(f.shas["target"], 4)
		pending.ObservedGeneration = 3
		st.DeploymentAfterApply = []fakestate.Deployment{pending}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "1")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, "未在 1s 内收敛", "observedGeneration=3 未追上 generation=4")
}

// TestVerifyPodNotReadyFails 覆盖「Pod 未 Ready」。
func TestVerifyPodNotReadyFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		applied := readyDeployment(f.shas["target"], 4)
		applied.Pods[0].Ready = false
		st.DeploymentAfterApply = []fakestate.Deployment{applied}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, "容器未 Ready")
}

// TestVerifyRolloutChangedByAnotherActorFails 覆盖「等待期间 Rollout 被其他操作改写」：
// 收敛判定通过，但最终验证必须发现实际版本不是本次目标。
func TestVerifyRolloutChangedByAnotherActorFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		ours := readyDeployment(f.shas["target"], 4)
		theirs := readyDeployment(f.shas["c2"], 5)
		st.DeploymentAfterApply = []fakestate.Deployment{ours, theirs}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, repository+":"+f.shas["c2"], "期望 = "+repository+":"+f.shas["target"])
}

// TestVerifyMissingPodsFails 覆盖「Pod 数量与期望副本不一致」。
func TestVerifyMissingPodsFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		applied := readyDeployment(f.shas["target"], 4)
		applied.Pods = nil
		st.DeploymentAfterApply = []fakestate.Deployment{applied}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, "Pod 数量 = 0，期望 = 1")
}
