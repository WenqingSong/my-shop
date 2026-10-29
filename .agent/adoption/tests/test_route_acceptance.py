#!/usr/bin/env python3
"""R1 验收：TaskBuilder 路线选择的真实任务 Fixture。

用真实 Git 仓库 + 真实 `workflow-check` binary 覆盖：

- 三条路线的初始化与出口；
- Owner 明确路线约束（Prompt 层，Engine 不校验授权来源）；
- route 与 `task.md` Complexity 冲突；
- ARCHITECT + NOT_REQUIRED；
- 非法 route 值；
- 路线变更未重新审批 / 已重新审批；
- 历史任务无 route（strict / --allow-legacy）。

不声称 Engine 能判断自然语言需求本身属于哪条路线，也不认证 Session 身份。
"""

from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
ENGINE = REPO / ".agent/engine"
TASK = "demo"
BRANCH = "feature/demo"
CONFIG = """schema_version: 1
git:
  integration_branch: develop
resources: {}
"""


def run(cmd: list[str], cwd: Path) -> subprocess.CompletedProcess:
    env = os.environ.copy()
    env.update(
        {
            "GIT_AUTHOR_NAME": "test",
            "GIT_AUTHOR_EMAIL": "test@example.com",
            "GIT_COMMITTER_NAME": "test",
            "GIT_COMMITTER_EMAIL": "test@example.com",
            "GIT_CONFIG_NOSYSTEM": "1",
            "GOWORK": "off",
        }
    )
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, env=env)


def build_engine() -> Path:
    out = Path(tempfile.mkdtemp(prefix="route-engine-")) / "workflow-check"
    proc = run(["go", "build", "-o", str(out), "./cmd/workflow-check"], ENGINE)
    if proc.returncode != 0:
        raise RuntimeError(f"构建 workflow-check 失败:\n{proc.stdout}\n{proc.stderr}")
    return out


def state_text(
    route: str | None = "NORMAL",
    contract_status: str = "NOT_REQUIRED",
    contract_target: str = "",
    review_status: str = "NOT_REQUESTED",
    review_target: str = "",
) -> str:
    lines = ["schema_version: 3"]
    if route is not None:
        lines.append(f"route: {route}")
    lines += [
        f"task_id: {TASK}",
        "contract:",
        f"  status: {contract_status}",
        f'  target: "{contract_target}"',
        "resources:",
        "  reservations: {}",
        "review:",
        f"  status: {review_status}",
        f'  target: "{review_target}"',
        "owner:",
        "  status: PENDING",
        '  review_target: ""',
        "delivery:",
        "  status: NOT_RUN",
        '  review_target: ""',
        '  feature_head: ""',
        '  develop_base: ""',
        "blocked:",
        "  active: false",
        '  by: ""',
        '  reason: ""',
    ]
    return "\n".join(lines) + "\n"


def task_md(complexity: str, extra: str = "") -> str:
    return f"# Task: demo\n\n## Goal\n交付可观察行为。\n\n## Complexity\n{complexity}\n\n原因：夹具。\n{extra}"


class Fixture:
    def __init__(self, engine: Path):
        self.tmp = Path(tempfile.mkdtemp(prefix="route-accept-"))
        self.remote = self.tmp / "remote.git"
        self.seed = self.tmp / "seed"
        self.work = self.tmp / "work"
        run(["git", "init", "--bare", "-q", str(self.remote)], self.tmp).check_returncode()
        run(["git", "init", "-q", str(self.seed)], self.tmp).check_returncode()
        self._git(self.seed, "config", "user.name", "test")
        self._git(self.seed, "config", "user.email", "test@example.com")
        self._write(self.seed, ".agent/workflow.yaml", CONFIG)
        self._write(self.seed, "README.md", "# demo\n")
        self._git(self.seed, "add", "-A")
        self._git(self.seed, "commit", "-q", "-m", "chore: baseline")
        self._git(self.seed, "branch", "-M", "develop")
        self._git(self.seed, "remote", "add", "origin", str(self.remote))
        self._git(self.seed, "push", "-q", "-u", "origin", "develop")

        self._git(self.tmp, "clone", "-q", "-b", "develop", str(self.remote), str(self.work))
        self._git(self.work, "config", "user.name", "test")
        self._git(self.work, "config", "user.email", "test@example.com")
        self._git(self.work, "switch", "-q", "-c", BRANCH)
        bin_dir = self.work / ".agent/bin"
        bin_dir.mkdir(parents=True, exist_ok=True)
        shutil.copy2(engine, bin_dir / "workflow-check")
        os.chmod(bin_dir / "workflow-check", 0o755)

    def close(self) -> None:
        shutil.rmtree(self.tmp, ignore_errors=True)

    def _git(self, repo: Path, *args: str) -> subprocess.CompletedProcess:
        proc = run(["git", *args], repo)
        if proc.returncode != 0:
            raise AssertionError(f"git {' '.join(args)} 失败: {proc.stdout}\n{proc.stderr}")
        return proc

    def _write(self, repo: Path, rel: str, text: str) -> None:
        path = repo / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")

    def init_task(self, *, route: str | None, complexity: str, **kwargs: str) -> str:
        self._write(self.work, f".agent/tasks/{TASK}/task.md", task_md(complexity))
        self._write(self.work, f".agent/tasks/{TASK}/state.yaml", state_text(route, **kwargs))
        return self.commit("chore(task): 初始化任务 demo")

    def commit(self, message: str) -> str:
        self._git(self.work, "add", "-A")
        self._git(self.work, "commit", "-q", "-m", message)
        sha = self._git(self.work, "rev-parse", "HEAD").stdout.strip()
        self._git(self.work, "push", "-q", "origin", BRANCH)
        return sha

    def write(self, rel: str, text: str) -> None:
        self._write(self.work, rel, text)

    def head(self) -> str:
        return self._git(self.work, "rev-parse", "HEAD").stdout.strip()

    def check(self, *args: str) -> subprocess.CompletedProcess:
        return run([str(self.work / ".agent/bin/workflow-check"), *args, "--root", str(self.work)], self.work)

    def route(self, *extra: str) -> subprocess.CompletedProcess:
        return self.check("route", f".agent/tasks/{TASK}", *extra)

    def gate(self, name: str) -> subprocess.CompletedProcess:
        return self.check("gate", name, f".agent/tasks/{TASK}")


class RouteAcceptanceTest(unittest.TestCase):
    engine: Path

    @classmethod
    def setUpClass(cls) -> None:
        if shutil.which("go") is None:
            raise unittest.SkipTest("缺少 go，无法运行真实 Gate 测试")
        cls.engine = build_engine()

    @classmethod
    def tearDownClass(cls) -> None:
        shutil.rmtree(cls.engine.parent, ignore_errors=True)

    def setUp(self) -> None:
        self.fx = Fixture(self.engine)

    def tearDown(self) -> None:
        self.fx.close()

    def approve(self, route: str) -> str:
        """模拟 Analyst A1（contract 内容）+ A2（APPROVED metadata）。"""
        self.fx.write(f".agent/tasks/{TASK}/contract.md", "# contract\n")
        a1 = self.fx.commit("docs(contract): A1")
        self.fx.write(
            f".agent/tasks/{TASK}/state.yaml",
            state_text(route, contract_status="APPROVED", contract_target=a1),
        )
        self.fx.commit("chore(task): A2")
        return a1

    # 1. 简单实现 → NORMAL
    def test_normal_route_persisted_and_handoff(self) -> None:
        self.fx.init_task(route="NORMAL", complexity="NORMAL")
        rep = self.fx.route()
        self.assertEqual(rep.returncode, 0, rep.stdout + rep.stderr)
        self.assertIn("ROUTE_OK", rep.stdout)
        self.assertIn("route=NORMAL", rep.stdout)
        self.assertEqual(self.fx.gate("coder-start").returncode, 0)

    # 2. 需要独立 Contract → COMPLEX
    def test_complex_requires_contract_before_coder(self) -> None:
        self.fx.init_task(route="COMPLEX", complexity="COMPLEX", contract_status="PENDING")
        self.assertEqual(self.fx.route().returncode, 0)
        failed = self.fx.gate("coder-start")
        self.assertEqual(failed.returncode, 1, failed.stdout)
        self.assertIn("Contract", failed.stdout)
        self.approve("COMPLEX")
        self.assertEqual(self.fx.gate("coder-start").returncode, 0)

    # 3. 纯结构交付 → ARCHITECT
    def test_architect_route_then_analyst(self) -> None:
        self.fx.init_task(route="ARCHITECT", complexity="ARCHITECT", contract_status="PENDING")
        self.assertEqual(self.fx.route().returncode, 0)
        self.assertEqual(self.fx.gate("coder-start").returncode, 1)
        self.approve("ARCHITECT")
        self.assertEqual(self.fx.gate("coder-start").returncode, 0)

    # 4. Owner 明确路线约束：Engine 只读 route，不认证 Owner 授权来源（SPEC_ONLY）
    def test_owner_constraint_is_not_machine_verifiable(self) -> None:
        self.fx.init_task(
            route="ARCHITECT",
            complexity="ARCHITECT",
            contract_status="PENDING",
        )
        self.fx.write(
            f".agent/tasks/{TASK}/task.md",
            task_md("ARCHITECT", "\nOwner 原话：必须走 ARCHITECT 路线。\n"),
        )
        self.fx.commit("chore(task): 记录 Owner 路线约束")
        self.assertEqual(self.fx.route().returncode, 0)
        # Owner 约束本身由 Prompt / Session 执行；Engine 无法区分真实指令与自述。
        self.assertIn("route=ARCHITECT", self.fx.route().stdout)

    # 5. route 与 Complexity 冲突
    def test_route_and_complexity_conflict(self) -> None:
        self.fx.init_task(route="NORMAL", complexity="COMPLEX")
        rep = self.fx.route()
        self.assertEqual(rep.returncode, 1, rep.stdout)
        self.assertIn("Route Task Document", rep.stdout)
        self.assertEqual(self.fx.gate("coder-start").returncode, 1)

    # 6. ARCHITECT 配置为 NOT_REQUIRED
    def test_architect_not_required_rejected(self) -> None:
        self.fx.init_task(route="ARCHITECT", complexity="ARCHITECT")
        rep = self.fx.route()
        self.assertEqual(rep.returncode, 1, rep.stdout)
        self.assertIn("Route Contract", rep.stdout)
        gate = self.fx.gate("coder-start")
        self.assertEqual(gate.returncode, 1, gate.stdout)

    # 7. 非法路线值
    def test_illegal_route_value(self) -> None:
        for bad in ("ARCH", "complex", "NORMAL,COMPLEX"):
            with self.subTest(route=bad):
                self.fx.init_task(route=bad, complexity="NORMAL")
                rep = self.fx.route()
                self.assertEqual(rep.returncode, 1, rep.stdout)
                self.assertIn("route", rep.stdout.lower())

    # 8. 未重新审批的路线变化
    def test_route_change_without_reapproval_rejected(self) -> None:
        self.fx.init_task(route="ARCHITECT", complexity="ARCHITECT", contract_status="PENDING")
        self.approve("ARCHITECT")
        self.assertEqual(self.fx.gate("coder-start").returncode, 0)
        self.fx.write(f".agent/tasks/{TASK}/task.md", task_md("COMPLEX"))
        self.fx.write(
            f".agent/tasks/{TASK}/state.yaml",
            state_text("COMPLEX", contract_status="APPROVED", contract_target=self.fx.head()),
        )
        self.fx.commit("chore(task): 仅改路线，未重新审批")
        rep = self.fx.route()
        self.assertEqual(rep.returncode, 1, rep.stdout)
        self.assertIn("Route Contract Evidence", rep.stdout)

    # 9. 合法重新审批的路线变化
    def test_route_change_with_reapproval_passes(self) -> None:
        self.fx.init_task(route="ARCHITECT", complexity="ARCHITECT", contract_status="PENDING")
        self.fx.write(f".agent/tasks/{TASK}/task.md", task_md("COMPLEX"))
        self.fx.write(
            f".agent/tasks/{TASK}/state.yaml",
            state_text("COMPLEX", contract_status="PENDING"),
        )
        self.fx.commit("chore(task): Owner 授权切换路线为 COMPLEX")
        self.approve("COMPLEX")
        rep = self.fx.route()
        self.assertEqual(rep.returncode, 0, rep.stdout + rep.stderr)
        self.assertEqual(self.fx.gate("coder-start").returncode, 0)

    # 10. 历史任务无 route
    def test_legacy_task_strict_and_allow_legacy(self) -> None:
        self.fx.init_task(route=None, complexity="NORMAL")
        strict = self.fx.route()
        self.assertEqual(strict.returncode, 1, strict.stdout)
        self.assertIn("Route Authority", strict.stdout)
        legacy = self.fx.route("--allow-legacy")
        self.assertEqual(legacy.returncode, 0, legacy.stdout + legacy.stderr)
        self.assertIn("ROUTE_LEGACY", legacy.stdout)
        # 历史任务仍按既有 Gate 语义通过，不被推断为任何路线。
        self.assertEqual(self.fx.gate("coder-start").returncode, 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
