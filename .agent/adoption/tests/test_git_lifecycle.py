#!/usr/bin/env python3
"""七角色 Git Lifecycle 的真实 Git / Gate 行为测试。

覆盖 .agent/specs/WorkflowGitLifecycle.md §13 的 GL-01 ~ GL-13：
- 真实 git init / clone / bare remote / commit / push；
- 真实 workflow-check binary 的 Gate 判定；
- 真实只读自检工具的 scope / 远端核验。

不使用 Markdown 文本搜索替代行为验证。
"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
TOOL = REPO / ".agent/specs/tools/git-lifecycle-check"
ENGINE = REPO / ".agent/engine"

TASK = "demo"
BRANCH = "feature/demo"
ZERO = "0" * 40


def run(cmd: list[str], cwd: Path, env: dict[str, str] | None = None) -> subprocess.CompletedProcess:
    base = os.environ.copy()
    base.update(
        {
            "GIT_AUTHOR_NAME": "test",
            "GIT_AUTHOR_EMAIL": "test@example.com",
            "GIT_COMMITTER_NAME": "test",
            "GIT_COMMITTER_EMAIL": "test@example.com",
            "GIT_CONFIG_NOSYSTEM": "1",
        }
    )
    if env:
        base.update(env)
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, env=base)


class Fixture:
    """一个 bare remote + 两个本地 clone 的最小真实仓库。"""

    def __init__(self, engine_bin: Path):
        self.tmp = Path(tempfile.mkdtemp(prefix="lifecycle-"))
        self.engine_bin = engine_bin
        self.remote = self.tmp / "remote.git"
        self.seed = self.tmp / "seed"
        self.work = self.tmp / "work"
        run(["git", "init", "--bare", "-q", str(self.remote)], self.tmp).check_returncode()
        run(["git", "init", "-q", str(self.seed)], self.tmp).check_returncode()
        self._config(self.seed)
        self._write(self.seed, ".agent/workflow.yaml", CONFIG)
        self._write(self.seed, ".agent/registry/migrations.md", "# migrations\n\n| version | owner | status |\n|---|---|---|\n")
        self._write(self.seed, ".agent/bin/.gitignore", "*\n!.gitignore\n")
        self._write(self.seed, "README.md", "# demo\n")
        self._commit(self.seed, "chore: baseline", ["."])
        self._git(self.seed, "branch", "-M", "develop")
        self._git(self.seed, "remote", "add", "origin", str(self.remote))
        self._git(self.seed, "push", "-q", "-u", "origin", "develop")

        self._git(self.tmp, "clone", "-q", "-b", "develop", str(self.remote), str(self.work))
        self._config(self.work)
        self._git(self.work, "switch", "-q", "-c", BRANCH)
        self._write(self.work, f".agent/tasks/{TASK}/task.md", "# task\n")
        self._write(self.work, f".agent/tasks/{TASK}/state.yaml", state_text())
        self._commit(self.work, "chore(task): 初始化任务 demo", [".agent/tasks/demo"])
        self._git(self.work, "push", "-q", "-u", "origin", BRANCH)
        bin_dir = self.work / ".agent/bin"
        bin_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(self.engine_bin, bin_dir / "workflow-check")
        os.chmod(bin_dir / "workflow-check", 0o755)

    def close(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _config(self, repo: Path) -> None:
        self._git(repo, "config", "user.name", "test")
        self._git(repo, "config", "user.email", "test@example.com")
        self._git(repo, "config", "commit.gpgsign", "false")

    def _git(self, repo: Path, *args: str, check: bool = True) -> subprocess.CompletedProcess:
        proc = run(["git", *args], repo)
        if check and proc.returncode != 0:
            raise AssertionError(f"git {' '.join(args)} 失败: {proc.stdout}\n{proc.stderr}")
        return proc

    def _write(self, repo: Path, rel: str, text: str) -> None:
        path = repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def _commit(self, repo: Path, message: str, paths: list[str]) -> str:
        self._git(repo, "add", "--", *paths)
        self._git(repo, "commit", "-q", "-m", message)
        return self._git(repo, "rev-parse", "HEAD").stdout.strip()

    def write(self, rel: str, text: str) -> None:
        self._write(self.work, rel, text)

    def state(self, **kwargs: str) -> None:
        self.write(f".agent/tasks/{TASK}/state.yaml", state_text(**kwargs))

    def commit(self, message: str, paths: list[str]) -> str:
        return self._commit(self.work, message, paths)

    def push(self, branch: str = BRANCH) -> subprocess.CompletedProcess:
        return self._git(self.work, "push", "-q", "origin", branch)

    def head(self) -> str:
        return self._git(self.work, "rev-parse", "HEAD").stdout.strip()

    def add(self, *paths: str) -> None:
        self._git(self.work, "add", "--", *paths)

    def tool(self, *args: str) -> subprocess.CompletedProcess:
        return run(["python3", str(TOOL), *args, "--root", str(self.work)], self.work)

    def tool_no_root(self, *args: str) -> subprocess.CompletedProcess:
        return run(["python3", str(TOOL), *args], self.work)

    def gate(self, name: str) -> subprocess.CompletedProcess:
        return run(
            [
                str(self.work / ".agent/bin/workflow-check"),
                "gate",
                name,
                f".agent/tasks/{TASK}",
                "--root",
                str(self.work),
            ],
            self.work,
        )

    def push_expect_failure(self) -> subprocess.CompletedProcess:
        return self._git(self.work, "push", "origin", BRANCH, check=False)

    def second_clone(self) -> Path:
        other = self.tmp / "other"
        self._git(self.tmp, "clone", "-q", "-b", BRANCH, str(self.remote), str(other))
        self._config(other)
        return other


CONFIG = """schema_version: 1
git:
  integration_branch: develop
resources:
  migration_version:
    registry: .agent/registry/migrations.md
"""


def state_text(
    task: str = TASK,
    contract_status: str = "NOT_REQUIRED",
    contract_target: str = "",
    review_status: str = "NOT_REQUESTED",
    review_target: str = "",
    owner_status: str = "PENDING",
    owner_target: str = "",
    delivery_status: str = "NOT_RUN",
    delivery_target: str = "",
    feature_head: str = "",
    develop_base: str = "",
    blocked: bool = False,
    blocked_by: str = "",
    blocked_reason: str = "",
) -> str:
    return f"""schema_version: 3
task_id: {task}
contract:
  status: {contract_status}
  target: "{contract_target}"
resources:
  reservations: {{}}
review:
  status: {review_status}
  target: "{review_target}"
owner:
  status: {owner_status}
  review_target: "{owner_target}"
delivery:
  status: {delivery_status}
  review_target: "{delivery_target}"
  feature_head: "{feature_head}"
  develop_base: "{develop_base}"
blocked:
  active: {str(blocked).lower()}
  by: "{blocked_by}"
  reason: "{blocked_reason}"
"""


def build_engine() -> Path:
    out_dir = Path(tempfile.mkdtemp(prefix="engine-bin-"))
    binary = out_dir / "workflow-check"
    env = os.environ.copy()
    env["GOWORK"] = "off"
    proc = subprocess.run(
        ["go", "build", "-o", str(binary), "./cmd/workflow-check"],
        cwd=ENGINE,
        capture_output=True,
        text=True,
        env=env,
    )
    if proc.returncode != 0:
        raise RuntimeError(f"构建 workflow-check 失败:\n{proc.stdout}\n{proc.stderr}")
    return binary


class LifecycleTest(unittest.TestCase):
    engine_bin: Path
    engine_dir: Path

    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("go") is None:
            raise unittest.SkipTest("缺少 go，无法运行真实 Gate 测试")
        cls.engine_bin = build_engine()
        cls.engine_dir = cls.engine_bin.parent

    @classmethod
    def tearDownClass(cls) -> None:
        shutil.rmtree(cls.engine_dir, ignore_errors=True)

    def setUp(self) -> None:
        self.fx = Fixture(self.engine_bin)

    def tearDown(self) -> None:
        self.fx.close()

    # ---------- 辅助：各阶段证据 ----------

    def make_c1(self, name: str = "internal/foo.go", content: str = "package internal\n") -> str:
        self.fx.write(name, content)
        self.fx.add(name)
        check = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "evidence")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        return self.fx.commit("feat: 实现 demo", [name])

    def make_c2(self, c1: str, extra: list[str] | None = None) -> str:
        self.fx.state(review_status="PENDING", review_target=c1)
        paths = [f".agent/tasks/{TASK}/state.yaml"] + (extra or [])
        self.fx.add(*paths)
        check = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "metadata")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        return self.fx.commit("chore(task): 请求 Review", paths)

    def make_review_ready(self) -> tuple[str, str]:
        c1 = self.make_c1()
        c2 = self.make_c2(c1)
        self.fx.push()
        self.assertEqual(self.fx.gate("cleaner-start").returncode, 0)
        return c1, c2

    def make_clean(self, c1: str) -> str:
        self.fx.write(f".agent/tasks/{TASK}/findings.md", "# Findings\n\nResult: CLEAN\n")
        self.fx.write(f".agent/tasks/{TASK}/core-logic.md", "# Core Logic\n\n## CL-001\n")
        self.fx.state(review_status="CLEAN", review_target=c1)
        paths = [
            f".agent/tasks/{TASK}/findings.md",
            f".agent/tasks/{TASK}/core-logic.md",
            f".agent/tasks/{TASK}/state.yaml",
        ]
        self.fx.add(*paths)
        check = self.fx.tool(
            "commit-check", "--role", "Cleaner", "--task", TASK, "--mode", "artifacts"
        )
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        sha = self.fx.commit("docs(review): CLEAN", paths)
        self.fx.push()
        return sha

    def make_accepted(self, c1: str) -> str:
        self.fx.write(f".agent/tasks/{TASK}/owner-decision.md", "# Owner Decision\n\nACCEPTED\n")
        self.fx.state(review_status="CLEAN", review_target=c1, owner_status="ACCEPTED", owner_target=c1)
        paths = [f".agent/tasks/{TASK}/owner-decision.md", f".agent/tasks/{TASK}/state.yaml"]
        self.fx.add(*paths)
        check = self.fx.tool(
            "commit-check", "--role", "OwnerGate", "--task", TASK, "--mode", "artifacts", "--phase", "v2"
        )
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        sha = self.fx.commit("docs(owner): ACCEPTED", paths)
        self.fx.push()
        return sha

    # ---------- GL-01 ~ GL-03：合法两阶段 Evidence ----------

    def test_gl01_analyst_a1_a2_coder_start(self) -> None:
        self.fx.write(f".agent/tasks/{TASK}/contract.md", "# Contract\n\nAPPROVED\n")
        self.fx.add(f".agent/tasks/{TASK}/contract.md")
        check = self.fx.tool("commit-check", "--role", "Analyst", "--task", TASK, "--mode", "evidence")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        a1 = self.fx.commit("docs(contract): A1", [f".agent/tasks/{TASK}/contract.md"])
        self.assertRegex(a1, r"^[0-9a-f]{40}$")

        self.fx.state(contract_status="APPROVED", contract_target=a1)
        self.fx.add(f".agent/tasks/{TASK}/state.yaml")
        check = self.fx.tool("commit-check", "--role", "Analyst", "--task", TASK, "--mode", "metadata")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("chore(contract): A2", [f".agent/tasks/{TASK}/state.yaml"])
        self.fx.push()

        gate = self.fx.gate("coder-start")
        self.assertEqual(gate.returncode, 0, gate.stdout + gate.stderr)

        # A2 混入业务代码 / A1 混入 state.yaml 都必须被前置自检拒绝
        self.fx.write("internal/mixed.go", "package internal\n")
        self.fx.add("internal/mixed.go")
        bad = self.fx.tool("commit-check", "--role", "Analyst", "--task", TASK, "--mode", "metadata")
        self.assertEqual(bad.returncode, 1)
        self.assertIn("超出", bad.stdout)

    def test_gl02_coder_c1_c2_cleaner_start(self) -> None:
        c1, c2 = self.make_review_ready()
        self.assertEqual(len(c1), 40)
        self.assertEqual(len(c2), 40)
        state = (self.fx.work / f".agent/tasks/{TASK}/state.yaml").read_text(encoding="utf-8")
        self.assertIn(f'target: "{c1}"', state)

        # EVIDENCE 混入 state.yaml 必须拒绝
        self.fx.write("internal/second.go", "package internal\n")
        self.fx.state(review_status="CLEAN", review_target=c1)
        self.fx.add("internal/second.go", f".agent/tasks/{TASK}/state.yaml")
        bad = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "evidence")
        self.assertEqual(bad.returncode, 1)
        self.assertIn("state.yaml", bad.stdout)

    def test_gl03_architect_d1_d2_cleaner_start(self) -> None:
        self.fx.write(f".agent/tasks/{TASK}/architecture.md", "# Architecture\n\n## 模块边界\n")
        self.fx.write("internal/arch/arch.go", "package arch\n")
        self.fx.write("internal/arch/arch_test.go", "package arch\n\nimport \"testing\"\n\nfunc TestStructure(t *testing.T) {}\n")
        paths = [
            f".agent/tasks/{TASK}/architecture.md",
            "internal/arch/arch.go",
            "internal/arch/arch_test.go",
        ]
        self.fx.add(*paths)
        check = self.fx.tool("commit-check", "--role", "Architect", "--task", TASK, "--mode", "evidence")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        d1 = self.fx.commit("feat(arch): D1 结构快照", paths)
        self.fx.state(review_status="PENDING", review_target=d1)
        paths2 = [f".agent/tasks/{TASK}/state.yaml"]
        self.fx.add(*paths2)
        check = self.fx.tool("commit-check", "--role", "Architect", "--task", TASK, "--mode", "metadata")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("chore(arch): D2 Review Request", paths2)
        self.fx.push()
        gate = self.fx.gate("cleaner-start")
        self.assertEqual(gate.returncode, 0, gate.stdout + gate.stderr)

        # ARCHITECT OwnerGate V1：只写 findings/core-logic/review.*，完成后才能 owner-gate-start
        self.fx.write(f".agent/tasks/{TASK}/findings.md", "# Findings\n\nResult: CLEAN\n")
        self.fx.write(f".agent/tasks/{TASK}/core-logic.md", "# Core Logic\n\n## CL-001\n")
        self.fx.state(review_status="CLEAN", review_target=d1)
        v1_paths = [
            f".agent/tasks/{TASK}/findings.md",
            f".agent/tasks/{TASK}/core-logic.md",
            f".agent/tasks/{TASK}/state.yaml",
        ]
        self.fx.add(*v1_paths)
        check = self.fx.tool(
            "commit-check", "--role", "OwnerGate", "--task", TASK, "--mode", "artifacts", "--phase", "v1"
        )
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("docs(arch): V1 独立结构验证", v1_paths)
        self.fx.push()
        v1_gate = self.fx.gate("owner-gate-start")
        self.assertEqual(v1_gate.returncode, 0, v1_gate.stdout + v1_gate.stderr)

    def test_taskbuilder_bootstrap_scope(self) -> None:
        other = "demo2"
        self.fx.write(f".agent/tasks/{other}/task.md", "# task2\n")
        self.fx.write(f".agent/tasks/{other}/state.yaml", state_text(task=other))
        paths = [f".agent/tasks/{other}/task.md", f".agent/tasks/{other}/state.yaml"]
        self.fx.add(*paths)
        check = self.fx.tool("commit-check", "--role", "TaskBuilder", "--task", other, "--mode", "bootstrap")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("chore(task): 初始化 demo2", paths)

        self.fx.write("internal/bootstrap-leak.go", "package internal\n")
        self.fx.add("internal/bootstrap-leak.go")
        bad = self.fx.tool("commit-check", "--role", "TaskBuilder", "--task", other, "--mode", "bootstrap")
        self.assertEqual(bad.returncode, 1)
        self.assertIn("超出", bad.stdout)

    # ---------- GL-04：未完成 Evidence 阶段不得 HANDOFF ----------

    def test_gl04_incomplete_evidence_blocks_handoff(self) -> None:
        c1 = self.make_c1()
        self.fx.push()
        # 只有 C1，Review Metadata 尚未提交（dirty）
        self.fx.state(review_status="PENDING", review_target=c1)
        gate = self.fx.gate("cleaner-start")
        self.assertEqual(gate.returncode, 1, gate.stdout)
        handoff = self.fx.tool("handoff-check", "--role", "Coder", "--task", TASK)
        self.assertEqual(handoff.returncode, 1)
        self.assertIn("dirty", handoff.stdout)

        # 提交 C2 但不 push：本地 != 远端，Gate 仍必须 FAIL
        self.fx.add(f".agent/tasks/{TASK}/state.yaml")
        self.fx.commit("chore(task): 请求 Review", [f".agent/tasks/{TASK}/state.yaml"])
        gate = self.fx.gate("cleaner-start")
        self.assertEqual(gate.returncode, 1, gate.stdout)
        self.assertIn("push", gate.stdout)
        handoff = self.fx.tool("handoff-check", "--role", "Coder", "--task", TASK)
        self.assertEqual(handoff.returncode, 1)

        self.fx.push()
        self.assertEqual(self.fx.gate("cleaner-start").returncode, 0)
        self.assertEqual(self.fx.tool("handoff-check", "--role", "Coder", "--task", TASK).returncode, 0)

    # ---------- GL-05：Commit 后 Push 失败 ----------

    def test_gl05_push_failure_must_not_claim_synced(self) -> None:
        c1, _ = self.make_review_ready()

        other = self.fx.second_clone()
        self.fx._write(other, "remote-advance.txt", "other writer\n")
        self.fx._commit(other, "chore: other writer", ["remote-advance.txt"])
        self.fx._git(other, "push", "-q", "origin", BRANCH)

        # 本地再形成一个 commit，push 必然被拒
        self.fx.write("internal/local.go", "package internal\n")
        self.fx.add("internal/local.go")
        self.fx.commit("feat: local change", ["internal/local.go"])
        failed = self.fx.push_expect_failure()
        self.assertNotEqual(failed.returncode, 0, "push 应被 non-fast-forward 拒绝")

        verify = self.fx.tool("verify-remote", "--task", TASK)
        self.assertEqual(verify.returncode, 1)
        self.assertIn("不一致", verify.stdout)
        handoff = self.fx.tool("handoff-check", "--role", "Coder", "--task", TASK)
        self.assertEqual(handoff.returncode, 1)
        gate = self.fx.gate("cleaner-start")
        self.assertEqual(gate.returncode, 1, gate.stdout)

    # ---------- GL-06：Push 成功后 Gate FAIL ----------

    def test_gl06_gate_fail_after_push_blocks_handoff(self) -> None:
        c1 = self.make_c1()
        # 故意把业务代码混入 metadata commit（绕过前置自检，模拟直接 git 写）
        self.fx.write("internal/sneaky.go", "package internal\n")
        self.fx.state(review_status="PENDING", review_target=c1)
        paths = ["internal/sneaky.go", f".agent/tasks/{TASK}/state.yaml"]
        self.fx.add(*paths)
        check = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "metadata")
        self.assertEqual(check.returncode, 1, "前置自检必须拒绝 metadata 混入业务代码")
        self.fx.commit("chore(task): 请求 Review", paths)
        self.fx.push()

        gate = self.fx.gate("cleaner-start")
        self.assertEqual(gate.returncode, 1, gate.stdout)
        self.assertIn("sneaky.go", gate.stdout)
        # Git 事实本身是同步的，但 Gate FAIL 仍然禁止 HANDOFF
        self.assertEqual(self.fx.tool("verify-remote", "--task", TASK).returncode, 0)

    # ---------- GL-07：Dirty Worktree ----------

    def test_gl07_dirty_worktree_blocks_handoff(self) -> None:
        c1, _ = self.make_review_ready()
        self.fx.write("scratch.txt", "uncommitted\n")
        gate = self.fx.gate("cleaner-start")
        self.assertEqual(gate.returncode, 1, gate.stdout)
        handoff = self.fx.tool("handoff-check", "--role", "Coder", "--task", TASK)
        self.assertEqual(handoff.returncode, 1)
        self.assertIn("dirty", handoff.stdout)

    # ---------- GL-08：错误远端 SHA ----------

    def test_gl08_moved_remote_detected_by_preflight(self) -> None:
        self.make_review_ready()
        other = self.fx.second_clone()
        self.fx._write(other, "remote-only.txt", "remote moved\n")
        self.fx._commit(other, "chore: advance remote", ["remote-only.txt"])
        self.fx._git(other, "push", "-q", "origin", BRANCH)

        verify = self.fx.tool("verify-remote", "--task", TASK)
        self.assertEqual(verify.returncode, 1, verify.stdout)
        self.assertIn("不一致", verify.stdout)
        # 已知 Engine 限制：handoffReady 只看本地 tracking ref，远端被推进时仍可能 PASS。
        gate = self.fx.gate("cleaner-start")
        self.assertEqual(gate.returncode, 0, gate.stdout + gate.stderr)

    # ---------- GL-09：DANGER write-ahead ----------

    def test_gl09_danger_forced_evidence_blocks_all_gates(self) -> None:
        ledger = (
            "# Loop Ledger\n\n- Task ID: demo\n- Ledger Version: 1\n\n"
            "## EV-0001 DANGER OWNER_DECISION:example S1 round=2\n\n"
            "- event_type: DANGER\n- outcome: DANGER\n"
        )
        self.fx.write(f".agent/tasks/{TASK}/loop-ledger.md", ledger)
        self.fx.write(f".agent/tasks/{TASK}/danger-report.md", "# DANGER Report\n\n## 12. 结论\n\n停止。\n")
        self.fx.state(blocked=True, blocked_by="Coder", blocked_reason="loop_key=OWNER_DECISION:example; rounds=2")
        paths = [
            f".agent/tasks/{TASK}/loop-ledger.md",
            f".agent/tasks/{TASK}/danger-report.md",
            f".agent/tasks/{TASK}/state.yaml",
        ]
        self.fx.add(*paths)
        check = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "forced")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("chore(workflow): DANGER 停机", paths)
        self.fx.push()

        for gate in ("coder-start", "cleaner-start", "owner-gate-start", "delivery-start", "merge-ready"):
            result = self.fx.gate(gate)
            self.assertEqual(result.returncode, 1, f"{gate}: {result.stdout}")
            self.assertIn("blocked", result.stdout)

        # FORCED_EVIDENCE 不允许混入业务代码
        self.fx.write("internal/not-danger.go", "package internal\n")
        self.fx.add("internal/not-danger.go")
        bad = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "forced")
        self.assertEqual(bad.returncode, 1)
        self.assertIn("超出", bad.stdout)

    def test_gl09b_write_ahead_ledger_only_is_allowed(self) -> None:
        self.fx.write(
            f".agent/tasks/{TASK}/loop-ledger.md",
            "# Loop Ledger\n\n## EV-0001 REQUEST OWNER_DECISION:example S1 round=1\n",
        )
        self.fx.add(f".agent/tasks/{TASK}/loop-ledger.md")
        check = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "forced")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("chore(workflow): write-ahead EV-0001", [f".agent/tasks/{TASK}/loop-ledger.md"])
        self.fx.push()
        self.assertEqual(self.fx.tool("verify-remote", "--task", TASK).returncode, 0)

        # 账本提交不得夹带 Review 状态变化
        self.fx.write(
            f".agent/tasks/{TASK}/loop-ledger.md",
            "# Loop Ledger\n\n## EV-0001 REQUEST OWNER_DECISION:example S1 round=1\n\n## EV-0002 CLOSE\n",
        )
        self.fx.state(review_status="CLEAN", review_target=ZERO)
        self.fx.add(f".agent/tasks/{TASK}/loop-ledger.md", f".agent/tasks/{TASK}/state.yaml")
        bad = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "forced")
        self.assertEqual(bad.returncode, 1)
        self.assertIn("不允许的字段", bad.stdout)

    # ---------- GL-10 / GL-11：失败证据 ----------

    def test_gl10_owner_reject_is_persisted_and_blocks_delivery(self) -> None:
        c1, _ = self.make_review_ready()
        self.make_clean(c1)
        self.fx.write(f".agent/tasks/{TASK}/owner-decision.md", "# Owner Decision\n\nREJECTED: 缺少关键错误路径\n")
        self.fx.state(
            review_status="CLEAN",
            review_target=c1,
            owner_status="REJECTED",
            owner_target=c1,
        )
        paths = [f".agent/tasks/{TASK}/owner-decision.md", f".agent/tasks/{TASK}/state.yaml"]
        self.fx.add(*paths)
        check = self.fx.tool(
            "commit-check", "--role", "OwnerGate", "--task", TASK, "--mode", "artifacts", "--phase", "v2"
        )
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("docs(owner): REJECTED", paths)
        self.fx.push()

        delivery = self.fx.gate("delivery-start")
        self.assertEqual(delivery.returncode, 1)
        self.assertIn("ACCEPTED", delivery.stdout)
        self.assertEqual(self.fx.tool("handoff-check", "--role", "OwnerGate", "--task", TASK).returncode, 0)
        remote_state = self.fx._git(
            self.fx.work, "show", f"origin/{BRANCH}:.agent/tasks/{TASK}/state.yaml"
        ).stdout
        self.assertIn("REJECTED", remote_state)

        # failure evidence 不允许混入业务代码
        self.fx.write("internal/reject-code.go", "package internal\n")
        self.fx.add("internal/reject-code.go")
        bad = self.fx.tool(
            "commit-check", "--role", "OwnerGate", "--task", TASK, "--mode", "artifacts", "--phase", "v2"
        )
        self.assertEqual(bad.returncode, 1)

    def test_gl11_cleaner_changes_required_evidence(self) -> None:
        c1, _ = self.make_review_ready()
        self.fx.write(f".agent/tasks/{TASK}/findings.md", "# CLEAN-001\n\nStatus: OPEN\n")
        self.fx.state(review_status="CHANGES_REQUIRED", review_target=c1)
        paths = [f".agent/tasks/{TASK}/findings.md", f".agent/tasks/{TASK}/state.yaml"]
        self.fx.add(*paths)
        check = self.fx.tool("commit-check", "--role", "Cleaner", "--task", TASK, "--mode", "artifacts")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("docs(review): CHANGES_REQUIRED CLEAN-001", paths)
        self.fx.push()

        self.assertEqual(self.fx.gate("cleaner-start").returncode, 1)
        self.assertEqual(self.fx.gate("owner-gate-start").returncode, 1)
        self.assertEqual(self.fx.tool("verify-remote", "--task", TASK).returncode, 0)
        remote_findings = self.fx._git(
            self.fx.work, "show", f"origin/{BRANCH}:.agent/tasks/{TASK}/findings.md"
        ).stdout
        self.assertIn("CLEAN-001", remote_findings)

    # ---------- GL-12：Registry-only 边界 ----------

    def test_gl12_registry_only_boundary(self) -> None:
        self.fx._git(self.fx.work, "switch", "-q", "develop")
        self.fx.write(".agent/registry/migrations.md", "# migrations\n\n| 1001 | demo | RESERVED |\n")
        self.fx.add(".agent/registry/migrations.md")
        check = self.fx.tool("commit-check", "--role", "Analyst", "--task", TASK, "--mode", "registry")
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("chore(registry): RESERVED 1001", [".agent/registry/migrations.md"])
        push = self.fx.tool("push-check", "--role", "Analyst", "--task", TASK, "--mode", "registry")
        self.assertEqual(push.returncode, 0, push.stdout)
        self.fx.push("develop")
        self.assertEqual(self.fx.tool("verify-remote", "--task", TASK).returncode, 0)

        # 业务角色不能 push develop；Registry commit 不能混入业务文件
        bad_push = self.fx.tool("push-check", "--role", "Coder", "--task", TASK)
        self.assertEqual(bad_push.returncode, 1)
        self.assertIn("integration", bad_push.stdout)
        bad_mode = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "registry")
        self.assertEqual(bad_mode.returncode, 1)

        self.fx.write("internal/not-registry.go", "package internal\n")
        self.fx.add("internal/not-registry.go")
        bad_scope = self.fx.tool("commit-check", "--role", "Analyst", "--task", TASK, "--mode", "registry")
        self.assertEqual(bad_scope.returncode, 1)
        self.assertIn("超出", bad_scope.stdout)

    # ---------- GL-13：正常完成自行 Commit / Push ----------

    def test_gl13_normal_completion_self_commit_push(self) -> None:
        c1, _ = self.make_review_ready()
        self.make_clean(c1)
        self.fx.write(f".agent/tasks/{TASK}/owner-decision.md", "# Owner Decision\n\nACCEPTED\n")
        self.fx.state(
            review_status="CLEAN",
            review_target=c1,
            owner_status="ACCEPTED",
            owner_target=c1,
        )
        paths = [f".agent/tasks/{TASK}/owner-decision.md", f".agent/tasks/{TASK}/state.yaml"]
        self.fx.add(*paths)
        check = self.fx.tool(
            "commit-check", "--role", "OwnerGate", "--task", TASK, "--mode", "artifacts", "--phase", "v2"
        )
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("docs(owner): ACCEPTED", paths)
        self.fx.push()
        self.assertEqual(self.fx.gate("delivery-start").returncode, 0)

        feature_head = self.fx.head()
        develop_base = self.fx._git(self.fx.work, "rev-parse", "origin/develop").stdout.strip()
        self.fx.write(f".agent/tasks/{TASK}/delivery.md", "# Delivery\n\nPASS\n")
        self.fx.state(
            review_status="CLEAN",
            review_target=c1,
            owner_status="ACCEPTED",
            owner_target=c1,
            delivery_status="PASS",
            delivery_target=c1,
            feature_head=feature_head,
            develop_base=develop_base,
        )
        paths = [f".agent/tasks/{TASK}/delivery.md", f".agent/tasks/{TASK}/state.yaml"]
        self.fx.add(*paths)
        check = self.fx.tool(
            "commit-check", "--role", "Deliverer", "--task", TASK, "--mode", "artifacts"
        )
        self.assertEqual(check.returncode, 0, check.stdout + check.stderr)
        self.fx.commit("docs(delivery): PASS", paths)
        self.fx.push()
        self.assertEqual(self.fx.gate("merge-ready").returncode, 0)

        verify = self.fx.tool("verify-remote", "--task", TASK)
        self.assertEqual(verify.returncode, 0, verify.stdout)
        handoff = self.fx.tool(
            "handoff-check", "--role", "Deliverer", "--task", TASK, "--gate", "merge-ready"
        )
        self.assertEqual(handoff.returncode, 0, handoff.stdout + handoff.stderr)
        status = self.fx._git(self.fx.work, "status", "--porcelain").stdout
        self.assertEqual(status.strip(), "")

    # ---------- 工具自身的前置检查 ----------

    def test_negative_scope_phase_and_evidence_sha(self) -> None:
        """非法 staged scope、PROGRESS 式提交、V1/V2 混淆与无效 SHA 必须 FAIL。"""
        state_path = f".agent/tasks/{TASK}/state.yaml"

        # EVIDENCE 混入 state.yaml → FAIL（state.yaml 必须真的发生变化才会被 stage）
        self.fx.write("internal/foo.go", "package internal\n")
        self.fx.write(state_path, state_text(review_status="PENDING", review_target="HEAD"))
        self.fx.add("internal/foo.go", state_path)
        proc = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "evidence")
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("state.yaml", proc.stdout)
        self.fx._git(self.fx.work, "restore", "--staged", ".")
        self.fx._git(self.fx.work, "checkout", "--", state_path)
        (self.fx.work / "internal/foo.go").unlink()

        # PROGRESS 式提交：Cleaner 夹带业务文件 → FAIL
        self.fx.write("internal/progress.txt", "wip\n")
        self.fx.add("internal/progress.txt")
        proc = self.fx.tool("commit-check", "--role", "Cleaner", "--task", TASK, "--mode", "artifacts")
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("超出", proc.stdout)
        self.fx._git(self.fx.work, "restore", "--staged", ".")
        (self.fx.work / "internal/progress.txt").unlink()

        # OwnerGate 未区分 V1 / V2 → FAIL
        owner_path = f".agent/tasks/{TASK}/owner-decision.md"
        self.fx.write(owner_path, "# Owner Decision\n")
        self.fx.add(owner_path)
        proc = self.fx.tool("commit-check", "--role", "OwnerGate", "--task", TASK, "--mode", "artifacts")
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("--phase", proc.stdout)
        self.fx._git(self.fx.work, "restore", "--staged", ".")
        (self.fx.work / owner_path).unlink()

        # 无效 Evidence SHA：可移动引用必须拒绝
        self.fx.state(review_status="PENDING", review_target="HEAD")
        self.fx.add(state_path)
        proc = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "metadata")
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertIn("40", proc.stdout)
        self.fx._git(self.fx.work, "restore", "--staged", ".")
        self.fx._git(self.fx.work, "checkout", "--", state_path)

    def test_foreign_changes_do_not_block_own_commit(self) -> None:
        """§3.3：其他角色的无关修改保留现场，不阻断本角色提交，也不得被提交。"""
        foreign = ".agent/registry/analyst-notes.md"
        self.fx.write(foreign, "# analyst work in progress\n")
        c1 = self.make_c1()
        show = self.fx._git(self.fx.work, "show", "--name-only", "--format=", c1).stdout
        self.assertNotIn("analyst-notes.md", show, "他人修改不得被顺手提交")
        handoff = self.fx.tool("handoff-check", "--role", "Coder", "--task", TASK)
        self.assertEqual(handoff.returncode, 1, "他人现场未处置时不得 HANDOFF")
        self.assertIn("dirty", handoff.stdout)
        (self.fx.work / foreign).unlink()

        # 本角色 scope 内的残留（未提交业务代码）仍必须 FAIL。
        self.fx.state(review_status="PENDING", review_target=c1)
        self.fx.add(f".agent/tasks/{TASK}/state.yaml")
        self.fx.write("internal/leftover.go", "package internal\n")
        proc = self.fx.tool("commit-check", "--role", "Coder", "--task", TASK, "--mode", "metadata")
        self.assertEqual(proc.returncode, 1, proc.stdout)
        self.assertIn("范围内", proc.stdout)
        (self.fx.work / "internal/leftover.go").unlink()
        self.fx._git(self.fx.work, "restore", "--staged", ".")
        self.fx._git(self.fx.work, "checkout", "--", f".agent/tasks/{TASK}/state.yaml")

    def test_tool_preflight_and_push_targets(self) -> None:
        pre = self.fx.tool("preflight", "--role", "Coder", "--task", TASK)
        self.assertEqual(pre.returncode, 0, pre.stdout + pre.stderr)
        self.fx.write("dirty.txt", "dirty\n")
        dirty = self.fx.tool("preflight", "--role", "Coder", "--task", TASK)
        self.assertEqual(dirty.returncode, 1)
        (self.fx.work / "dirty.txt").unlink()

        self.fx._git(self.fx.work, "switch", "-q", "develop")
        coder = self.fx.tool("push-check", "--role", "Coder", "--task", TASK)
        self.assertEqual(coder.returncode, 1)
        registry = self.fx.tool(
            "push-check", "--role", "Analyst", "--task", TASK, "--mode", "registry"
        )
        self.assertEqual(registry.returncode, 0, registry.stdout)


ROLE_PROMPTS = {
    "TaskBuilder": ".agent/roles/TaskBuilderPrompt.md",
    "Analyst": ".agent/roles/AnalystAgent.md",
    "Architect": ".agent/roles/ArchitectAgent.md",
    "Coder": ".agent/roles/CoderAgent.md",
    "Cleaner": ".agent/roles/CleanerAgent.md",
    "OwnerGate": ".agent/roles/OwnerGateAgent.md",
    "Deliverer": ".agent/roles/DelivererAgent.md",
}

# 每个角色在自己的 Prompt 中必须出现的 commit 模式（工具的真实参数形态）。
ROLE_COMMIT_MODES = {
    "TaskBuilder": ["--mode bootstrap"],
    "Analyst": ["--mode evidence", "--mode metadata", "--mode registry"],
    "Architect": ["--mode evidence", "--mode metadata"],
    "Coder": ["--mode evidence", "--mode metadata", "--mode forced"],
    "Cleaner": ["--mode artifacts"],
    "OwnerGate": ["--mode artifacts --phase v1", "--mode artifacts --phase v2"],
    "Deliverer": ["--mode artifacts"],
}


class RoleWiringTest(unittest.TestCase):
    """C4：自检工具必须真的接进七个角色的执行规范，而不只是存在于目录里。"""

    def test_all_seven_roles_reference_the_tool(self) -> None:
        for role, rel in ROLE_PROMPTS.items():
            text = (REPO / rel).read_text(encoding="utf-8")
            with self.subTest(role=role):
                self.assertIn(".agent/specs/tools/git-lifecycle-check", text, rel)
                self.assertIn(f"preflight --role {role}", text, rel)
                self.assertIn(f"handoff-check --role {role}", text, rel)
                self.assertIn("verify-remote", text, rel)
                self.assertIn("push-check", text, rel)
                self.assertIn("不替代 Gate", text, rel)
                for mode in ROLE_COMMIT_MODES[role]:
                    self.assertIn(mode, text, f"{rel} 缺少 {mode}")

    def test_tool_is_executable_and_exposes_all_subcommands(self) -> None:
        self.assertTrue(TOOL.is_file(), "缺少 .agent/specs/tools/git-lifecycle-check")
        self.assertTrue(TOOL.stat().st_mode & 0o111, "git-lifecycle-check 必须可执行（100755）")
        proc = run(["python3", str(TOOL), "--help"], REPO)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        for sub in ("preflight", "commit-check", "push-check", "verify-remote", "handoff-check"):
            self.assertIn(sub, proc.stdout)

    def test_danger_and_blocked_wording_kept_in_all_roles(self) -> None:
        """DANGER / BLOCKED 的 NEXT_ROLE=NONE 语义不得被 Git 自检章节改写。"""
        for role, rel in ROLE_PROMPTS.items():
            text = (REPO / rel).read_text(encoding="utf-8")
            with self.subTest(role=role):
                self.assertIn("NEXT_ROLE=NONE", text, rel)


if __name__ == "__main__":
    unittest.main(verbosity=2)
