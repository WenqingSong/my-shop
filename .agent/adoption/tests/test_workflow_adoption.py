"""End-to-end tests for Workflow V3 install/upgrade/adoption tooling.

These tests only use temporary host repositories.  They never install into a
real business project and never create tags, releases or remote branches.
"""

from __future__ import annotations

import hashlib
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
TOOL = REPO / ".agent/adoption/workflow-adoption"
FORMAL_REPOSITORY = "https://cnb.cool/framework-station/agent-workflow"
ROLE_PROMPTS = (
    "TaskBuilderPrompt.md",
    "AnalystAgent.md",
    "ArchitectAgent.md",
    "CoderAgent.md",
    "CleanerAgent.md",
    "OwnerGateAgent.md",
    "DelivererAgent.md",
)
FORBIDDEN_PACKAGE_PARTS = (
    "/.git/",
    "/.agent/workflow.yaml",
    "/.agent/upstream.yaml",
    "/.agent/tasks/",
    "/.agent/registry/",
    "/.agent/bin/workflow-check",
    "/AGENTS.md",
    "/tmp.md",
)


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    return sha256_bytes(path.read_bytes())


def run(cmd: list[str], cwd: Path | None = None, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    proc = subprocess.run(
        cmd,
        cwd=str(cwd) if cwd else None,
        capture_output=True,
        text=True,
        env=env,
        timeout=600,
    )
    if proc.returncode != 0:
        raise AssertionError(
            f"command failed ({proc.returncode}): {cmd}\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}"
        )
    return proc


def git(root: Path, *args: str) -> str:
    return run(["git", "-C", str(root), *args]).stdout.strip()


def init_git(root: Path, branch: str = "main") -> None:
    run(["git", "-C", str(root), "init", "-q"])
    run(["git", "-C", str(root), "checkout", "-q", "-b", branch])
    for key, value in (
        ("user.name", "adoption-test"),
        ("user.email", "adoption-test@example.invalid"),
        ("commit.gpgsign", "false"),
    ):
        run(["git", "-C", str(root), "config", key, value])


def write(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def commit_all(root: Path, message: str) -> str:
    run(["git", "-C", str(root), "add", "-A"])
    run(["git", "-C", str(root), "commit", "-q", "-m", message])
    return git(root, "rev-parse", "HEAD")


def tracked_source_paths(root: Path) -> list[str]:
    """返回进入 Fixture 的源文件集合：Git tracked 文件。

    正式 `workflow-adoption dist` 也只打包 tracked 文件，因此 Fixture 的输入
    必须与「一个可分发 Commit 的源码快照」严格一致。
    """
    proc = subprocess.run(
        ["git", "-C", str(root), "ls-files", "-z", "--cached"],
        capture_output=True,
    )
    if proc.returncode != 0:
        raise AssertionError(
            f"git ls-files 失败（{root}）: {proc.stderr.decode('utf-8', 'replace').strip()}"
        )
    return [item for item in proc.stdout.decode("utf-8").split("\0") if item]


def copy_source(dest: Path, root: Path | None = None) -> str:
    """用 Git tracked 文件快照构造临时源仓库（默认取本仓库）。

    真实 `.cnb.yml` 的顺序是 tag-guard -> release-context -> engine-verification
    -> adoption-e2e，所以本测试运行时仓库里已经存在 guard / repository 阶段写入的
    `dist/reports/*.json`。这些是 CI 生成产物而非上游源码；旧实现复制整个工作树
    再 `git add -A`，会把它们变成 Fixture 的 tracked 文件，触发
    `SOURCE_UNCLASSIFIED`。

    这里隔离的是 Fixture 的输入（只复制 tracked 文件），不改变正式 Distribution
    的任何分类 / 归档安全 / 来源验证规则：未分类的 tracked 源文件仍必须被拒绝。
    """
    source_root = Path(root or REPO).resolve()
    dest.mkdir(parents=True)
    for relative in tracked_source_paths(source_root):
        src = source_root / relative
        dst = dest / relative
        dst.parent.mkdir(parents=True, exist_ok=True)
        if src.is_symlink():
            os.symlink(os.readlink(src), dst)
        elif src.is_file():
            shutil.copy2(src, dst)
        else:
            raise AssertionError(f"Git tracked 文件在磁盘上缺失: {relative}（{source_root}）")
    init_git(dest, "source")
    return commit_all(dest, "source baseline")


def make_host(
    dest: Path,
    *,
    integration_branch: str = "release/integration",
    agents: str | None = None,
    business: bool = True,
) -> Path:
    dest.mkdir(parents=True)
    init_git(dest, "main")
    if business:
        write(dest / "README.md", "# Host README\n\nHost-owned.\n")
        write(dest / ".cnb.yml", "host:\n  ci: true\n")
        write(dest / "docs/design/business.md", "# Host business design\n")
        write(dest / "src/app.py", "print('host')\n")
    else:
        write(dest / "src/keep.txt", "host data\n")
    if agents is not None:
        write(dest / "AGENTS.md", agents)
    commit_all(dest, "host baseline")
    head = git(dest, "rev-parse", "HEAD")
    run(["git", "-C", str(dest), "update-ref", "refs/remotes/origin/main", head])
    run(["git", "-C", str(dest), "update-ref", f"refs/remotes/origin/{integration_branch}", head])
    return dest


def run_tool(*args: str, env: dict[str, str] | None = None) -> subprocess.CompletedProcess[str]:
    full_env = os.environ.copy()
    full_env["PYTHONDONTWRITEBYTECODE"] = "1"
    if env:
        full_env.update(env)
    return subprocess.run(
        [sys.executable, str(TOOL), *args],
        capture_output=True,
        text=True,
        env=full_env,
        timeout=600,
    )


def tool_json(proc: subprocess.CompletedProcess[str]) -> dict:
    try:
        return json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise AssertionError(
            f"tool did not return JSON (exit {proc.returncode})\nstdout:\n{proc.stdout}\nstderr:\n{proc.stderr}"
        ) from exc


def dist(source: Path, output: Path) -> dict:
    proc = run_tool("dist", "--source", str(source), "--output", str(output), "--json")
    assert proc.returncode == 0, proc.stdout + proc.stderr
    return tool_json(proc)


def install(
    host: Path,
    source: Path | None = None,
    package: Path | None = None,
    source_commit: str | None = None,
    distribution_sha256: str | None = None,
    integration_branch: str = "release/integration",
    extra: list[str] | None = None,
) -> tuple[subprocess.CompletedProcess[str], dict]:
    assert bool(source) != bool(package)
    commit = source_commit or git(source, "rev-parse", "HEAD")  # type: ignore[arg-type]
    args = [
        "install",
        "--target",
        str(host),
        "--source-commit",
        commit,
        "--integration-branch",
        integration_branch,
        "--owner",
        "test-owner",
        "--owner-authorized",
        "--execute",
        "--json",
    ]
    if source:
        args += ["--source", str(source)]
    else:
        args += ["--package", str(package), "--distribution-sha256", str(distribution_sha256)]
    if extra:
        args += extra
    proc = run_tool(*args)
    return proc, tool_json(proc)


def upgrade(
    host: Path,
    source: Path | None = None,
    package: Path | None = None,
    source_commit: str | None = None,
    distribution_sha256: str | None = None,
    extra: list[str] | None = None,
    env: dict[str, str] | None = None,
) -> tuple[subprocess.CompletedProcess[str], dict]:
    assert bool(source) != bool(package)
    commit = source_commit or git(source, "rev-parse", "HEAD")  # type: ignore[arg-type]
    args = [
        "upgrade",
        "--target",
        str(host),
        "--source-commit",
        commit,
        "--owner",
        "test-owner",
        "--owner-authorized",
        "--execute",
        "--json",
    ]
    if source:
        args += ["--source", str(source)]
    else:
        args += ["--package", str(package), "--distribution-sha256", str(distribution_sha256)]
    if extra:
        args += extra
    proc = run_tool(*args, env=env)
    return proc, tool_json(proc)


def verify(host: Path) -> tuple[subprocess.CompletedProcess[str], dict]:
    proc = run_tool("verify", "--target", str(host), "--json")
    return proc, tool_json(proc)


def workflow_root(host: Path) -> Path:
    out = git(host, "rev-parse", "--path-format=absolute", "--git-path", "workflow-adoption")
    return Path(out)


def upstream_commit(host: Path) -> str:
    text = (host / ".agent/upstream.yaml").read_text(encoding="utf-8")
    for line in text.splitlines():
        if line.strip().startswith("commit:"):
            return line.split(":", 1)[1].strip().strip('"')
    raise AssertionError("upstream.yaml missing commit")


class DistributionContractTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="adoption-dist-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.source = self.tmp / "source"
        self.commit = copy_source(self.source)
        self.out = self.tmp / "out"
        self.dist = dist(self.source, self.out)

    def test_source_distribution_is_clean_classified_and_pinned(self) -> None:
        data = self.dist
        self.assertEqual(data["source_commit"], self.commit)
        self.assertTrue(data["sha256"])
        archive = Path(data["archive"])
        manifest = data["manifest"]
        self.assertEqual(manifest["source"]["commit"], self.commit)
        self.assertEqual(manifest["compatibility"], {"config_schema": 1, "state_schema": [2, 3]})
        self.assertEqual(manifest["release"]["tag"], "")
        self.assertEqual(manifest["release"]["url"], "")

        with tarfile.open(archive, "r:gz") as tar:
            names = tar.getnames()
        for name in names:
            for forbidden in FORBIDDEN_PACKAGE_PARTS:
                self.assertNotIn(forbidden, "/" + name)
        self.assertIn(f"agent-workflow-src-{self.commit[:12]}/MANIFEST.json", names)
        self.assertIn(f"agent-workflow-src-{self.commit[:12]}/README.md", names)
        for role in ROLE_PROMPTS:
            self.assertIn(f"agent-workflow-src-{self.commit[:12]}/.agent/roles/{role}", names, role)
            self.assertIn(f".agent/roles/{role}", manifest["install"]["managed"], role)
        self.assertIn(f"agent-workflow-src-{self.commit[:12]}/.agent/engine/go.mod", names)
        self.assertNotIn(f"agent-workflow-src-{self.commit[:12]}/.agent/tasks/x/state.yaml", names)

        by_kind = {entry["path"]: entry["install"] for entry in manifest["files"]}
        self.assertEqual(
            set(manifest["install"]["managed"]),
            {path for path, kind in by_kind.items() if kind == "managed"},
        )
        self.assertEqual(
            set(manifest["install"]["package_only"]),
            {path for path, kind in by_kind.items() if kind == "package_only"},
        )
        self.assertIn(".agent/roles/AnalystAgent.md", manifest["install"]["managed"])
        self.assertIn(".agent/adoption/workflow_adoption.py", manifest["install"]["managed"])
        self.assertNotIn(".agent/adoption/tests/test_workflow_adoption.py", manifest["install"]["managed"])
        self.assertIn(".agent/adoption/tests/test_workflow_adoption.py", manifest["install"]["package_only"])
        self.assertIn("README.md", manifest["install"]["package_only"])
        self.assertIn("docs/design/agent-workflow.md", manifest["install"]["package_only"])
        self.assertNotIn("README.md", manifest["install"]["managed"])

        proc = run_tool(
            "inspect",
            "--package",
            str(archive),
            "--expected-sha256",
            data["sha256"],
            "--json",
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        inspected = tool_json(proc)
        self.assertEqual(inspected["status"], "INSPECT_OK")
        self.assertEqual(inspected["commit"], self.commit)

    def test_manifest_does_not_claim_an_unpublished_release(self) -> None:
        # 版本必须来自 .agent/VERSION 且与分发包一致；具体版本号由发布任务推进，
        # 这里只校验“不得声称已发布”的契约，不绑定某个字面版本。
        version = (REPO / ".agent/VERSION").read_text(encoding="utf-8").strip()
        self.assertRegex(version, r"^\d+\.\d+\.\d+$")
        self.assertEqual(self.dist["framework_version"], version)
        self.assertEqual(self.dist["manifest"]["release"]["tag"], "")
        self.assertEqual(self.dist["manifest"]["release"]["url"], "")
        for relative in (
            "README.md",
            "PACKAGE_MANIFEST.md",
            ".agent/specs/WorkflowAdoption.md",
            ".agent/adoption/README.md",
        ):
            text = (REPO / relative).read_text(encoding="utf-8")
            self.assertNotIn("releases/download", text, relative)
            self.assertNotIn("github.com", text.lower(), relative)

    def test_tracked_host_data_is_rejected(self) -> None:
        write(self.source / ".agent/workflow.yaml", "schema_version: 1\n")
        commit_all(self.source, "tracked host config")
        proc = run_tool("dist", "--source", str(self.source), "--output", str(self.tmp / "out2"), "--json")
        self.assertNotEqual(proc.returncode, 0)
        self.assertEqual(tool_json(proc)["error_code"], "SOURCE_INVALID")

    def test_untracked_host_data_is_not_a_reproducible_source(self) -> None:
        write(self.source / ".agent/tasks/secret/state.yaml", "schema_version: 3\n")
        proc = run_tool("dist", "--source", str(self.source), "--output", str(self.tmp / "out3"), "--json")
        self.assertNotEqual(proc.returncode, 0)
        self.assertEqual(tool_json(proc)["error_code"], "SOURCE_UNTRACKED")

    def test_archive_traversal_and_symlink_are_rejected(self) -> None:
        archive = self.tmp / "evil.tar.gz"
        with tarfile.open(archive, "w:gz") as tar:
            info = tarfile.TarInfo("../escape.txt")
            payload = b"escape"
            info.size = len(payload)
            tar.addfile(info, io.BytesIO(payload))
        proc = run_tool("inspect", "--package", str(archive), "--json")
        self.assertNotEqual(proc.returncode, 0)
        self.assertEqual(tool_json(proc)["error_code"], "ARCHIVE_UNSAFE_PATH")

        archive2 = self.tmp / "symlink.tar.gz"
        with tarfile.open(archive2, "w:gz") as tar:
            info = tarfile.TarInfo("top/link")
            info.type = tarfile.SYMTYPE
            info.linkname = "/etc/passwd"
            tar.addfile(info)
        proc = run_tool("inspect", "--package", str(archive2), "--json")
        self.assertNotEqual(proc.returncode, 0)
        self.assertEqual(tool_json(proc)["error_code"], "ARCHIVE_SYMLINK")


def directory_state(root: Path) -> dict[str, tuple[int, int, str]]:
    """记录目录下每个文件的大小 / mtime / SHA256，用于证明产物未被改写。"""
    state: dict[str, tuple[int, int, str]] = {}
    if not root.exists():
        return state
    for path in sorted(root.rglob("*")):
        if path.is_file():
            stat = path.stat()
            state[path.relative_to(root).as_posix()] = (
                stat.st_size,
                stat.st_mtime_ns,
                sha256_file(path),
            )
    return state


class CiGeneratedArtifactIsolationTests(unittest.TestCase):
    """真实 .cnb.yml 顺序下 Fixture 的源码输入隔离。

    tag-guard / release-context 阶段在 adoption-e2e 之前写入 `dist/reports/*.json`；
    Fixture 只能把 Git tracked 文件当作上游源码，CI 生成产物不得通过 `git add -A`
    隐式进入 Fixture。正式 Distribution 的 fail-closed 分类规则必须保持不变。
    """

    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="adoption-ci-order-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.upstream = self.tmp / "upstream"
        self.upstream_commit = copy_source(self.upstream)
        run(["git", "-C", str(self.upstream), "remote", "add", "origin", FORMAL_REPOSITORY])

    def _report(self, relative: str, payload: dict) -> None:
        write(self.upstream / relative, json.dumps(payload, indent=2, sort_keys=True) + "\n")

    def _fixture(self, name: str) -> tuple[Path, dict]:
        source = self.tmp / f"fixture-{name}"
        copy_source(source, root=self.upstream)
        tracked = git(source, "ls-files").splitlines()
        self.assertFalse(
            [path for path in tracked if path.startswith("dist/")],
            f"CI 生成产物进入了 Fixture: {tracked}",
        )
        self.assertEqual(tracked, tracked_source_paths(self.upstream))
        data = dist(source, self.tmp / f"out-{name}")
        self.assertEqual(data["status"], "DIST_OK")
        with tarfile.open(data["archive"], "r:gz") as tar:
            names = tar.getnames()
        self.assertFalse([name for name in names if "/dist/" in name], names)
        return source, data

    def test_clean_source_tree_is_distributable(self) -> None:
        source, data = self._fixture("clean")
        self.assertEqual(data["source_commit"], git(source, "rev-parse", "HEAD"))
        self.assertGreater(data["file_count"], 0)

    def test_guard_report_alone_does_not_enter_fixture(self) -> None:
        self._report("dist/reports/guard.json", {"status": "GUARD_OK", "tag": "v3.1.0"})
        self._fixture("guard")

    def test_guard_and_release_context_reports_do_not_enter_fixture(self) -> None:
        self._report("dist/reports/guard.json", {"status": "GUARD_OK", "tag": "v3.1.0"})
        self._report(
            "dist/reports/release-context.json",
            {"status": "PASS", "repository_mode": "formal"},
        )
        self._fixture("context")

    def test_other_generated_artifacts_do_not_enter_fixture(self) -> None:
        for relative in (
            "dist/reports/local-verification.json",
            "dist/reports/snapshot.json",
            "dist/reports/expected-assets.json",
            "dist/reports/pipeline-coverage.json",
            "dist/expected/SHA256SUMS",
            "dist/workflow-check_3.1.0_linux_amd64",
            "dist/build.log",
            "expected/agent-workflow-src.tar.gz",
        ):
            write(self.upstream / relative, "{}\n")
        self._fixture("artifacts")

    def test_tracked_unclassified_source_is_still_rejected(self) -> None:
        source = self.tmp / "unclassified-source"
        copy_source(source)
        write(source / "notes/unknown-source.txt", "unknown origin\n")
        commit_all(source, "unknown tracked source")
        proc = run_tool(
            "dist",
            "--source",
            str(source),
            "--output",
            str(self.tmp / "out-unclassified"),
            "--json",
        )
        self.assertNotEqual(proc.returncode, 0)
        data = tool_json(proc)
        self.assertEqual(data["error_code"], "SOURCE_UNCLASSIFIED")
        self.assertIn("notes/unknown-source.txt", data["error_details"]["unclassified"])

    def test_ci_ordered_release_stages_do_not_poison_fixture(self) -> None:
        # 真实 .cnb.yml：tag-guard -> release-context -> adoption-e2e
        release_tool = REPO / "scripts/release/release.py"
        commit = self.upstream_commit
        # Tag 必须跟随被测试仓库的真实 VERSION，不能写死某个历史版本。
        tag = f"v{(self.upstream / '.agent' / 'VERSION').read_text(encoding='utf-8').strip()}"
        run(["git", "-C", str(self.upstream), "tag", "-a", tag, "-m", "ci order fixture"])
        env = {key: value for key, value in os.environ.items() if not key.startswith("CNB_")}
        env.update(CNB_BRANCH=tag, CNB_COMMIT=commit)
        for args in (
            (
                "guard",
                "--repo",
                ".",
                "--tag",
                tag,
                "--expected-commit",
                commit,
                "--expected-commit-source",
                "CNB_COMMIT",
                "--report",
                "dist/reports/guard.json",
                "--json",
            ),
            (
                "repository",
                "--repo",
                ".",
                "--tag",
                tag,
                "--report",
                "dist/reports/release-context.json",
                "--json",
            ),
        ):
            run([sys.executable, str(release_tool), *args], cwd=self.upstream, env=env)

        reports = {
            "dist/reports/guard.json": "GUARD_OK",
            "dist/reports/release-context.json": "PASS",
        }
        for relative, expected in reports.items():
            payload = json.loads((self.upstream / relative).read_text(encoding="utf-8"))
            self.assertEqual(payload["status"], expected, relative)
        # CI 生成产物在真实仓库里必须是未跟踪文件；否则本用例并未构造出污染形态。
        self.assertEqual(git(self.upstream, "ls-files", "dist"), "")
        before = directory_state(self.upstream)

        self._fixture("ci-order")

        self.assertEqual(directory_state(self.upstream), before, "上游阶段报告被 Fixture 改写")

    def test_workspace_reports_are_not_rewritten(self) -> None:
        reports = REPO / "dist"
        before = directory_state(reports)
        status_before = git(REPO, "status", "--porcelain")
        source = self.tmp / "fixture-workspace"
        copy_source(source)
        dist(source, self.tmp / "out-workspace")
        self.assertEqual(directory_state(reports), before, "原工作区生成报告被重写或删除")
        self.assertEqual(git(REPO, "status", "--porcelain"), status_before)


class InstallE2ETests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="adoption-install-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.source = self.tmp / "source"
        self.commit = copy_source(self.source)

    def test_install_empty_host_does_not_create_agents_readme_ci_or_design(self) -> None:
        host = make_host(self.tmp / "host", business=False)
        proc, report = install(host, source=self.source, source_commit=self.commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "INSTALLED")
        self.assertEqual(report["source_commit"], self.commit)
        self.assertEqual(report["integration_branch"], "release/integration")

        self.assertFalse((host / "AGENTS.md").exists())
        self.assertFalse((host / "README.md").exists())
        self.assertFalse((host / ".cnb.yml").exists())
        self.assertFalse((host / "docs/design").exists())
        self.assertTrue((host / ".agent/workflow.yaml").is_file())
        self.assertTrue((host / ".agent/upstream.yaml").is_file())
        self.assertTrue((host / ".agent/bin/workflow-check").is_file())
        self.assertFalse((host / ".agent/registry").exists())
        self.assertFalse((host / ".agent/tasks").exists())
        installed_roles = sorted(p.name for p in (host / ".agent/roles").glob("*.md"))
        self.assertEqual(installed_roles, sorted(ROLE_PROMPTS))
        config = (host / ".agent/workflow.yaml").read_text(encoding="utf-8")
        self.assertIn('integration_branch: "release/integration"', config)
        self.assertIn("resources: {}", config)
        self.assertEqual(upstream_commit(host), self.commit)
        upstream = (host / ".agent/upstream.yaml").read_text(encoding="utf-8")
        self.assertIn('release: ""', upstream)
        self.assertIn("previous_commit: \"\"", upstream)
        self.assertTrue(
            (workflow_root(host) / "snapshots" / self.commit / "manifest.json").is_file()
        )
        self.assertEqual(git(host, "branch", "--show-current"), "main")
        self.assertEqual(git(host, "remote").strip(), "")

        proc, verified = verify(host)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(verified["status"], "VERIFIED")
        self.assertEqual(verified["verification"]["gate"], "PASS")
        self.assertEqual(verified["verification"]["files"], "PASS")

    def test_install_preserves_host_agents_readme_ci_and_design(self) -> None:
        host = make_host(
            self.tmp / "host2",
            integration_branch="main",
            agents="# Host AGENTS\n\nBusiness rule: do not rewrite.\n",
        )
        protected = [
            host / "AGENTS.md",
            host / "README.md",
            host / ".cnb.yml",
            host / "docs/design/business.md",
            host / "src/app.py",
        ]
        before = {str(p.relative_to(host)): sha256_file(p) for p in protected}
        proc, report = install(
            host, source=self.source, source_commit=self.commit, integration_branch="main"
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "INSTALLED")
        after = {str(p.relative_to(host)): sha256_file(p) for p in protected}
        self.assertEqual(before, after)
        self.assertIn("Business rule", (host / "AGENTS.md").read_text(encoding="utf-8"))
        self.assertIn("Host README", (host / "README.md").read_text(encoding="utf-8"))
        self.assertEqual(git(host, "branch", "--show-current"), "main")

    def test_install_uses_owner_branch_and_real_gate(self) -> None:
        host = make_host(self.tmp / "host3", integration_branch="team/integration-42")
        proc, report = install(
            host,
            source=self.source,
            source_commit=self.commit,
            integration_branch="team/integration-42",
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        config = (host / ".agent/workflow.yaml").read_text(encoding="utf-8")
        self.assertIn('integration_branch: "team/integration-42"', config)
        self.assertEqual(report["verification"]["gate"], "PASS")
        proc, verified = verify(host)
        self.assertEqual(proc.returncode, 0)
        self.assertEqual(verified["verification"]["gate"], "PASS")

    def test_registry_is_opt_in_and_starts_empty(self) -> None:
        host = make_host(self.tmp / "host4")
        proc, report = install(
            host,
            source=self.source,
            source_commit=self.commit,
            extra=["--resource", "migration_version=.agent/registry/migrations.md"],
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        registry = host / ".agent/registry/migrations.md"
        self.assertTrue(registry.is_file())
        body = registry.read_text(encoding="utf-8")
        self.assertIn("| 值 |", body)
        self.assertNotIn("202610", body)
        data_rows = [
            line
            for line in body.splitlines()
            if line.startswith("|") and "---" not in line and "值" not in line
        ]
        self.assertEqual(data_rows, [])
        config = (host / ".agent/workflow.yaml").read_text(encoding="utf-8")
        self.assertIn("migration_version:", config)
        self.assertIn(".agent/registry/migrations.md", config)
        self.assertFalse((host / ".agent/tasks").exists())

    def test_install_conflict_fails_closed_without_partial_overwrite(self) -> None:
        host = make_host(self.tmp / "host5")
        existing = host / ".agent/roles/AnalystAgent.md"
        write(existing, "# host custom analyst\n")
        commit_all(host, "host custom framework path")
        proc, report = install(host, source=self.source, source_commit=self.commit)
        self.assertEqual(proc.returncode, 3, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "CONFLICT")
        self.assertTrue(report["conflicts"])
        self.assertEqual(existing.read_text(encoding="utf-8"), "# host custom analyst\n")
        self.assertFalse((host / ".agent/workflow.yaml").exists())
        self.assertFalse((host / ".agent/upstream.yaml").exists())

    def test_external_binary_requires_hash_and_matching_commit(self) -> None:
        binary = self.tmp / "workflow-check"
        env = os.environ.copy()
        env["GOWORK"] = "off"
        run(
            ["go", "build", "-trimpath", "-o", str(binary), "./cmd/workflow-check"],
            cwd=self.source / ".agent/engine",
            env=env,
        )
        binary_sha = sha256_file(binary)
        host = make_host(self.tmp / "host-ext", business=False)
        proc, report = install(
            host,
            source=self.source,
            source_commit=self.commit,
            extra=[
                "--workflow-check",
                str(binary),
                "--workflow-check-sha256",
                binary_sha,
                "--workflow-check-commit",
                self.commit,
            ],
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "INSTALLED")
        self.assertEqual(report["runtime"]["provenance"], "external-verified")
        self.assertIn('provenance: "external-verified"', (host / ".agent/upstream.yaml").read_text(encoding="utf-8"))

        host2 = make_host(self.tmp / "host-ext2", business=False)
        proc2, report2 = install(
            host2,
            source=self.source,
            source_commit=self.commit,
            extra=[
                "--workflow-check",
                str(binary),
                "--workflow-check-sha256",
                binary_sha,
                "--workflow-check-commit",
                "0" * 40,
            ],
        )
        self.assertNotEqual(proc2.returncode, 0)
        self.assertEqual(report2["error_code"], "BINARY_COMMIT_MISMATCH")
        self.assertFalse((host2 / ".agent/workflow.yaml").exists())

    def test_installed_manifest_matches_host_selection(self) -> None:
        host = make_host(self.tmp / "host6", business=False)
        proc, report = install(host, source=self.source, source_commit=self.commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        manifest = self.dist_for_source()["manifest"]
        for path in manifest["install"]["managed"]:
            self.assertTrue((host / path).is_file(), path)
        for path in manifest["install"]["package_only"]:
            if path in ("README.md", "PACKAGE_MANIFEST.md", ".cnb.yml", "docs/design/agent-workflow.md"):
                self.assertFalse((host / path).exists(), path)
        self.assertTrue((host / ".agent/workflow.yaml").is_file())
        self.assertTrue((host / ".agent/upstream.yaml").is_file())
        self.assertTrue((host / ".agent/bin/workflow-check").is_file())

        # 从分发包构建的 binary 必须自证来源：版本与 Commit 来自分发包，
        # build_source=source 表示“由源码构建”，与发布资产 build_source=release 区分。
        version_proc = subprocess.run(
            [str(host / ".agent/bin/workflow-check"), "version", "--json"],
            capture_output=True,
            text=True,
        )
        self.assertEqual(version_proc.returncode, 0, version_proc.stdout + version_proc.stderr)
        info = json.loads(version_proc.stdout)
        self.assertEqual(info["framework_version"], manifest["framework_version"])
        self.assertEqual(info["source_commit"], self.commit)
        self.assertEqual(info["build_source"], "source")

    def dist_for_source(self) -> dict:
        # A separate output dir avoids depending on a previous test's dist.
        return dist(self.source, self.tmp / "dist-for-manifest")


class UpgradeE2ETests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="adoption-upgrade-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.source = self.tmp / "source"
        self.base_commit = copy_source(self.source)
        self.host = make_host(self.tmp / "host")
        proc, report = install(self.host, source=self.source, source_commit=self.base_commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "INSTALLED")
        commit_all(self.host, "record install")
        self.spec = self.host / ".agent/specs/WorkflowAdoption.md"
        self.base_spec = self.spec.read_bytes()

    def modify_source(self, relative: str, content: str, message: str = "new upstream") -> str:
        path = self.source / relative
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")
        return commit_all(self.source, message)

    def test_upgrade_updates_l_equals_base_and_records_provenance(self) -> None:
        new_commit = self.modify_source(
            ".agent/specs/WorkflowAdoption.md",
            self.base_spec.decode("utf-8") + "\n<!-- N upgrade marker -->\n",
        )
        proc, report = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "UPGRADED")
        self.assertIn("N upgrade marker", self.spec.read_text(encoding="utf-8"))
        self.assertEqual(upstream_commit(self.host), new_commit)
        upstream = (self.host / ".agent/upstream.yaml").read_text(encoding="utf-8")
        self.assertIn("action: \"upgrade\"", upstream)
        self.assertIn(f'from_commit: "{self.base_commit}"', upstream)
        self.assertIn(f'to_commit: "{new_commit}"', upstream)
        self.assertTrue((workflow_root(self.host) / "backups" / report["backup_id"]).is_dir())
        self.assertTrue(
            any(
                d["path"] == ".agent/specs/WorkflowAdoption.md" and d["decision"] == "UPDATE"
                for d in report["decisions"]
            )
        )
        proc, verified = verify(self.host)
        self.assertEqual(proc.returncode, 0)
        self.assertEqual(verified["status"], "VERIFIED")

    def test_upgrade_preserves_local_change_when_base_equals_new(self) -> None:
        local_path = self.host / ".agent/specs/AgentCollaborationSpecification.md"
        write(local_path, local_path.read_text(encoding="utf-8") + "\n<!-- LOCAL KEEP -->\n")
        commit_all(self.host, "local change")
        new_commit = self.modify_source(
            ".agent/specs/WorkflowAdoption.md",
            self.base_spec.decode("utf-8") + "\n<!-- N2 -->\n",
        )
        proc, report = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "UPGRADED")
        self.assertIn("LOCAL KEEP", local_path.read_text(encoding="utf-8"))
        self.assertTrue(
            any(
                d["path"] == ".agent/specs/AgentCollaborationSpecification.md"
                and d["decision"] == "KEEP_LOCAL"
                for d in report["decisions"]
            )
        )
        self.assertEqual(upstream_commit(self.host), new_commit)

    def test_upgrade_l_equals_new_is_no_file_change(self) -> None:
        new_content = self.base_spec.decode("utf-8") + "\n<!-- L EQUALS N -->\n"
        new_commit = self.modify_source(".agent/specs/WorkflowAdoption.md", new_content)
        write(self.spec, new_content)
        commit_all(self.host, "host already at N")
        proc, report = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "UPGRADED")
        self.assertEqual(report["changes"], [])
        self.assertTrue(
            any(
                d["path"] == ".agent/specs/WorkflowAdoption.md" and d["decision"] == "KEEP_IDENTICAL"
                for d in report["decisions"]
            )
        )
        self.assertEqual(upstream_commit(self.host), new_commit)

    def test_upgrade_conflict_keeps_local_and_does_not_write(self) -> None:
        write(self.spec, self.base_spec.decode("utf-8") + "\n<!-- LOCAL CONFLICT -->\n")
        commit_all(self.host, "local conflict")
        local_bytes = self.spec.read_bytes()
        new_commit = self.modify_source(
            ".agent/specs/WorkflowAdoption.md",
            self.base_spec.decode("utf-8") + "\n<!-- NEW CONFLICT -->\n",
        )
        upstream_before = (self.host / ".agent/upstream.yaml").read_bytes()
        proc, report = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 3, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "CONFLICT")
        self.assertEqual(self.spec.read_bytes(), local_bytes)
        self.assertEqual((self.host / ".agent/upstream.yaml").read_bytes(), upstream_before)
        self.assertFalse(
            (workflow_root(self.host) / "snapshots" / new_commit).exists()
        )
        self.assertTrue(
            any(d["decision"] == "CONFLICT_ALL_DIFFER" for d in report["decisions"])
        )

    def test_upgrade_missing_base_snapshot_is_base_unknown(self) -> None:
        new_commit = self.modify_source(
            ".agent/specs/WorkflowAdoption.md",
            self.base_spec.decode("utf-8") + "\n<!-- N3 -->\n",
        )
        snapshot = workflow_root(self.host) / "snapshots" / self.base_commit
        shutil.rmtree(snapshot)
        before = self.spec.read_bytes()
        upstream_before = (self.host / ".agent/upstream.yaml").read_bytes()
        proc, report = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 4, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "BASE_UNKNOWN")
        self.assertEqual(self.spec.read_bytes(), before)
        self.assertEqual((self.host / ".agent/upstream.yaml").read_bytes(), upstream_before)

    def test_upgrade_preserves_tasks_registry_blocked_evidence_and_reports_f03(self) -> None:
        task_dir = self.host / ".agent/tasks/legacy"
        task_dir.mkdir(parents=True)
        # 合法历史短 SHA：必须是本仓库真实且无歧义解析的 7 位 Commit 前缀。
        legacy_short = git(self.host, "rev-parse", "HEAD")[:7]
        state = f"""schema_version: 2
task_id: legacy
contract:
  status: NOT_REQUIRED
  target: "{legacy_short}"
resources:
  migrations:
    - "20261001000001"
review:
  status: CLEAN
  target: HEAD
owner:
  status: ACCEPTED
  review_target: main
delivery:
  status: PASS
  review_target: origin/legacy
  feature_head: HEAD~1
  develop_base: v1.0.0
blocked:
  active: true
  by: Coder
  reason: historical danger
"""
        (task_dir / "state.yaml").write_text(state, encoding="utf-8")
        (task_dir / "findings.md").write_text("# historical findings\n", encoding="utf-8")
        modern_dir = self.host / ".agent/tasks/modern"
        modern_dir.mkdir(parents=True)
        modern_state = """schema_version: 3
task_id: modern
contract:
  status: NOT_REQUIRED
  target: ""
resources:
  reservations: {}
review:
  status: NOT_REQUESTED
  target: ""
owner:
  status: PENDING
  review_target: ""
delivery:
  status: NOT_RUN
  review_target: ""
  feature_head: ""
  develop_base: ""
blocked:
  active: false
  by: ""
  reason: ""
"""
        (modern_dir / "state.yaml").write_text(modern_state, encoding="utf-8")
        registry = self.host / ".agent/registry/migrations.md"
        registry.parent.mkdir(parents=True)
        registry.write_text(
            "# migrations\n| 值 | 标题 | 拥有方（任务） | 状态 | 备注 |\n"
            "| --- | --- | --- | --- | --- |\n"
            "| 20261001000001 | baseline | legacy | ACTIVE | historical |\n",
            encoding="utf-8",
        )
        config_before = (self.host / ".agent/workflow.yaml").read_bytes()
        commit_all(self.host, "historical evidence")
        before = {
            "state": (task_dir / "state.yaml").read_bytes(),
            "findings": (task_dir / "findings.md").read_bytes(),
            "registry": registry.read_bytes(),
            "modern": (modern_dir / "state.yaml").read_bytes(),
            "config": config_before,
        }
        new_commit = self.modify_source(
            ".agent/specs/WorkflowAdoption.md",
            self.base_spec.decode("utf-8") + "\n<!-- N4 -->\n",
        )
        proc, report = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "UPGRADED")
        self.assertEqual((task_dir / "state.yaml").read_bytes(), before["state"])
        self.assertEqual((task_dir / "findings.md").read_bytes(), before["findings"])
        self.assertEqual(registry.read_bytes(), before["registry"])
        self.assertEqual((modern_dir / "state.yaml").read_bytes(), before["modern"])
        self.assertEqual((self.host / ".agent/workflow.yaml").read_bytes(), before["config"])
        self.assertIn("schema_version: 1", (self.host / ".agent/upstream.yaml").read_text(encoding="utf-8"))
        self.assertIn("legacy", report["task_reverification_required"])
        classes = {(item["task"], item["field"], item["class"]) for item in report["legacy_evidence"]}
        self.assertIn(("legacy", "target", "legacy_short_sha"), classes)
        self.assertIn(("legacy", "review_target", "invalid_immutable_ref"), classes)
        self.assertIn(("legacy", "feature_head", "invalid_immutable_ref"), classes)

    def test_upgrade_delete_with_local_modification_is_preserved(self) -> None:
        target = self.host / ".agent/templates/registry/registry.template.md"
        write(target, target.read_text(encoding="utf-8") + "\n<!-- LOCAL DELETE CONFLICT -->\n")
        commit_all(self.host, "local modify template")
        (self.source / ".agent/templates/registry/registry.template.md").unlink()
        new_commit = commit_all(self.source, "upstream delete template")
        local_bytes = target.read_bytes()
        proc, report = upgrade(
            self.host,
            source=self.source,
            source_commit=new_commit,
            extra=["--allow-deletes"],
        )
        self.assertEqual(proc.returncode, 3, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "CONFLICT")
        self.assertEqual(target.read_bytes(), local_bytes)
        self.assertEqual(upstream_commit(self.host), self.base_commit)

    def test_upgrade_delete_without_authorization_does_not_write(self) -> None:
        target = self.host / ".agent/templates/registry/registry.template.md"
        (self.source / ".agent/templates/registry/registry.template.md").unlink()
        new_commit = commit_all(self.source, "upstream delete template")
        upstream_before = (self.host / ".agent/upstream.yaml").read_bytes()
        proc, report = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 5, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "AUTHORIZATION_REQUIRED")
        self.assertIn(".agent/templates/registry/registry.template.md", report["deletes_planned"])
        self.assertTrue(target.is_file())
        self.assertEqual((self.host / ".agent/upstream.yaml").read_bytes(), upstream_before)

    def test_upgrade_delete_is_authorized_backed_up_and_applied(self) -> None:
        target = self.host / ".agent/templates/registry/registry.template.md"
        self.assertTrue(target.is_file())
        (self.source / ".agent/templates/registry/registry.template.md").unlink()
        new_commit = commit_all(self.source, "upstream delete template")
        proc, report = upgrade(
            self.host,
            source=self.source,
            source_commit=new_commit,
            extra=["--allow-deletes"],
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "UPGRADED")
        self.assertFalse(target.exists())
        self.assertIn(".agent/templates/registry/registry.template.md", report["deletes_planned"])
        backup = workflow_root(self.host) / "backups" / report["backup_id"] / "before" / ".agent/templates/registry/registry.template.md"
        self.assertTrue(backup.is_file())

    def test_upgrade_failure_rolls_back_and_restore_recovers(self) -> None:
        new_commit = self.modify_source(
            ".agent/specs/WorkflowAdoption.md",
            self.base_spec.decode("utf-8") + "\n<!-- N5 -->\n",
        )
        upstream_before = (self.host / ".agent/upstream.yaml").read_bytes()
        proc, report = upgrade(
            self.host,
            source=self.source,
            source_commit=new_commit,
            env={"WORKFLOW_ADOPTION_TEST_FAIL_AFTER_WRITE": "1"},
        )
        self.assertEqual(proc.returncode, 1, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "FAILED")
        self.assertTrue(report["rollback"]["ok"], report)
        self.assertEqual(self.spec.read_bytes(), self.base_spec)
        self.assertEqual((self.host / ".agent/upstream.yaml").read_bytes(), upstream_before)
        self.assertFalse((workflow_root(self.host) / "snapshots" / new_commit).exists())

        # A successful upgrade records a backup of the pre-upgrade (B) state;
        # restore must return the host to exactly that state.
        proc, report2 = upgrade(self.host, source=self.source, source_commit=new_commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        write(self.spec, "damaged after upgrade\n")
        restore_proc = run_tool(
            "restore",
            "--target",
            str(self.host),
            "--backup",
            report2["backup_id"],
            "--execute",
            "--json",
        )
        self.assertEqual(restore_proc.returncode, 0, restore_proc.stdout + restore_proc.stderr)
        restored = tool_json(restore_proc)
        self.assertEqual(restored["status"], "RESTORED")
        self.assertEqual(self.spec.read_bytes(), self.base_spec)
        self.assertEqual(upstream_commit(self.host), self.base_commit)


class LegacyEvidenceClassificationTests(unittest.TestCase):
    """F-03 分类必须与 Engine E-2 的解析语义一致。

    合法短 SHA 只是「历史非规范写入」，Engine 仍能解析，不得升级为必须重新审查；
    不存在 / 有歧义 / 非 Commit 的十六进制字面值则不是合法 Evidence。
    """

    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="adoption-legacy-class-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.source = self.tmp / "source"
        self.commit = copy_source(self.source)
        self.host = make_host(self.tmp / "host")

    def _write_state(self, task: str, value: str) -> None:
        write(
            self.host / f".agent/tasks/{task}/state.yaml",
            f'schema_version: 3\ntask_id: {task}\ncontract:\n  status: NOT_REQUIRED\n  target: "{value}"\n',
        )

    def test_scan_uses_real_commit_resolution(self) -> None:
        proc, report = install(self.host, source=self.source, source_commit=self.commit)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "INSTALLED")

        commit = commit_all(self.host, "evidence fixture base")
        blob = git(self.host, "rev-parse", "HEAD:README.md")
        git(self.host, "tag", "v-legacy-fixture", commit)

        cases = {
            "full40": commit,
            "short7": commit[:7],
            "short12": commit[:12],
            "headref": "HEAD",
            "branchref": "main",
            "tagref": "v-legacy-fixture",
            "originref": "origin/main",
            "missing": "0" * 7,
            "noncommit": blob[:7],
        }
        for task, value in cases.items():
            self._write_state(task, value)
        commit_all(self.host, "evidence fixture")

        proc, data = verify(self.host)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        classes = {
            (item["task"], item["field"]): item["class"] for item in data["legacy_evidence"]
        }
        self.assertNotIn(("full40", "target"), classes)
        self.assertEqual(classes[("short7", "target")], "legacy_short_sha")
        self.assertEqual(classes[("short12", "target")], "legacy_short_sha")
        for task in ("headref", "branchref", "tagref", "originref"):
            self.assertEqual(classes[(task, "target")], "invalid_immutable_ref", task)
        for task in ("missing", "noncommit"):
            self.assertEqual(classes[(task, "target")], "invalid_commit_sha", task)

        reverify = set(data["task_reverification_required"])
        self.assertEqual(
            reverify,
            {"headref", "branchref", "tagref", "originref", "missing", "noncommit"},
        )


class PackageInstallTests(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = Path(tempfile.mkdtemp(prefix="adoption-package-test-"))
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.source = self.tmp / "source"
        self.commit = copy_source(self.source)
        self.dist = dist(self.source, self.tmp / "out")

    def test_install_from_verified_archive(self) -> None:
        host = make_host(self.tmp / "host", business=False)
        proc, report = install(
            host,
            package=Path(self.dist["archive"]),
            source_commit=self.commit,
            distribution_sha256=self.dist["sha256"],
        )
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.assertEqual(report["status"], "INSTALLED")
        self.assertEqual(upstream_commit(host), self.commit)
        self.assertFalse((host / "README.md").exists())

    def test_install_rejects_unpinned_or_wrong_archive(self) -> None:
        host = make_host(self.tmp / "host2", business=False)
        proc, report = install(
            host,
            package=Path(self.dist["archive"]),
            source_commit=self.commit,
            distribution_sha256="0" * 64,
        )
        self.assertNotEqual(proc.returncode, 0)
        self.assertEqual(report["error_code"], "DIST_SHA_MISMATCH")
        self.assertFalse((host / ".agent/workflow.yaml").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
