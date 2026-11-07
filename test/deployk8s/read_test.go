package deployk8s

import (
	"testing"

	"cnb.cool/go-cloud-devops/my-shop/test/deployk8s/fakestate"
)

// 本文件验证 T2-06.1.1 的读取类缺陷修复：
//   - AUD-08：Pod 查询失败 ≠ 没有 Pod；
//   - AUD-09：严格解析 NotFound（命名空间错误不得当成「首次部署」）、
//     线上版本标签必须从 Deployment 对象解析；
//   - AUD-11：多容器 Pod 只认 app 容器的状态与镜像。

// TestPodQueryFailureIsNotReportedAsNoPods 覆盖 AUD-08：
// 迁移超时时查询 Pod 失败，必须明确报告「状态未知」，而不是「没有查询到迁移 Pod」。
func TestPodQueryFailureIsNotReportedAsNoPods(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		running := fakestate.Job{
			Name:   "surgecart-migrate-" + f.shas["target"],
			UID:    "job-uid-" + f.shas["target"],
			Exists: true,
		}
		st.Job = running
		st.JobScript = []fakestate.Job{running}
		st.PodsGetFailures = 1
	})

	res := runDeploy(t, f, "--migration-timeout", "6")

	res.requireCode(t, 6)
	res.requireContains(t,
		"无法查询迁移 Pod 状态",
		"这不等于「没有 Pod」",
		"禁止删除 Job",
		"Pod 状态未知",
	)
	res.requireNotContains(t, "没有查询到迁移 Pod")
}

// TestNamespaceNotFoundIsNotFirstDeploy 覆盖 AUD-09：
// 读取 Deployment 时遇到「命名空间不存在」必须 fail-closed，
// 不能因为错误文本里含 "not found" 就当成「首次部署」并继续迁移。
func TestNamespaceNotFoundIsNotFirstDeploy(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.DeploymentGetError = `Error from server (NotFound): namespaces "surgecart" not found`
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 1)
	res.requireNoSuccess(t)
	res.requireContains(t, "读取 Deployment/surgecart 失败", "无法确认线上 Deployment 状态")
	res.requireNotContains(t, "首次部署")
	f.requireNoEvent(t, "apply.migration")
	f.requireNoEvent(t, "apply.application")
}

// TestDeploymentAbsentIsStillFirstDeploy 覆盖 AUD-09 的正向分支：
// 真正的 `deployments.apps "..." not found` 仍然是「首次部署」。
func TestDeploymentAbsentIsStillFirstDeploy(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{firstDeploy: true})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "首次部署")
}

// TestVersionLabelMissingDoesNotFallBackToImageTag 覆盖 AUD-09：
// Deployment 缺少 app.kubernetes.io/version 时不得回退到镜像 tag 猜版本。
func TestVersionLabelMissingDoesNotFallBackToImageTag(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		st.Deployment.Version = ""
		// 镜像 tag 本身是一个合法短 SHA：旧实现在这里会「猜」出一个版本并继续发布。
		st.Deployment.Image = repository + ":" + f.shas["c1"]
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 4)
	res.requireNoSuccess(t)
	res.requireContains(t, "无法识别当前线上版本", "不再从镜像 tag 猜测线上版本")
	res.requireNotContains(t, "回退到当前镜像标签")
	f.requireNoEvent(t, "apply.migration")
}

// TestMultiContainerPodUsesAppContainerOnly 覆盖 AUD-11（正向）：
// sidecar 未 Ready / 镜像不同，不影响发布判定 —— 只认 app 容器。
func TestMultiContainerPodUsesAppContainerOnly(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		applied := readyDeployment(f.shas["target"], 4)
		applied.Pods[0].Containers = []fakestate.Container{
			{Name: "app", Image: repository + ":" + f.shas["target"], Ready: true},
			{Name: "istio-proxy", Image: "proxy.example.com/envoy:v1", Ready: false},
		}
		st.DeploymentAfterApply = []fakestate.Deployment{applied}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireSuccess(t)
	res.requireContains(t, "发布验证通过")
}

// TestMultiContainerPodAppNotReadyFails 覆盖 AUD-11（反向）：
// sidecar Ready 但 app 未 Ready 必须判定失败（旧实现会把两个 ready 拼成 "truefalse"，
// 恰好不等于 "true" 才偶然失败；这里断言失败原因指向 app 容器）。
func TestMultiContainerPodAppNotReadyFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		applied := readyDeployment(f.shas["target"], 4)
		applied.Pods[0].Containers = []fakestate.Container{
			{Name: "app", Image: repository + ":" + f.shas["target"], Ready: false},
			{Name: "istio-proxy", Image: "proxy.example.com/envoy:v1", Ready: true},
		}
		st.DeploymentAfterApply = []fakestate.Deployment{applied}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, "app 容器未 Ready")
}

// TestPodWithoutAppContainerFails 覆盖 AUD-11：Pod 里没有 app 容器时，
// 不得用 sidecar 的镜像/状态冒充应用容器。
func TestPodWithoutAppContainerFails(t *testing.T) {
	t.Parallel()
	f := newFixture(t, fixtureOptions{})
	f.updateState(func(st *fakestate.State) {
		applied := readyDeployment(f.shas["target"], 4)
		applied.Pods[0].Containers = []fakestate.Container{
			{Name: "sidecar-only", Image: repository + ":" + f.shas["target"], Ready: true},
		}
		st.DeploymentAfterApply = []fakestate.Deployment{applied}
	})

	res := runDeploy(t, f, "--migration-timeout", "10", "--rollout-timeout", "10")

	res.requireCode(t, 5)
	res.requireNoSuccess(t)
	res.requireContains(t, "没有名为 app 的容器")
}
