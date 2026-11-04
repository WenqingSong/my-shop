"""Workflow V3 source distribution, first-install and upgrade tooling.

This module is deliberately small and dependency-free (Python 3 standard
library only).  It is not a package manager: it installs the framework files
declared by a verified source distribution, protects host-owned files, and
performs a three-way (base/local/new) upgrade with a durable snapshot and
backup record.

The Go engine remains the only Gate implementation.  This tool builds or
accepts a verified ``workflow-check`` binary and runs a real Gate against a
minimal fixture; it never reimplements engine rules.
"""

from __future__ import annotations

import argparse
import dataclasses
import gzip
import hashlib
import io
import json
import os
import re
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import uuid
from contextlib import contextmanager
from pathlib import Path, PurePosixPath
from typing import Any, Iterable

DEFAULT_REPOSITORY = "https://cnb.cool/framework-station/agent-workflow"
DIST_FORMAT = 1
UPSTREAM_SCHEMA = 1
CONFIG_SCHEMA = 1
SUPPORTED_STATE_SCHEMAS = (2, 3)

EXIT_OK = 0
EXIT_FAIL = 1
EXIT_PRECONDITION = 2
EXIT_CONFLICT = 3
EXIT_BASE_UNKNOWN = 4
EXIT_AUTH_REQUIRED = 5

FULL_SHA = re.compile(r"^[0-9a-f]{40}$", re.IGNORECASE)
SHORT_SHA = re.compile(r"^[0-9a-f]{7,39}$", re.IGNORECASE)

# ---------------------------------------------------------------------------
# File classes
# ---------------------------------------------------------------------------

# These paths must never enter a source distribution.  They are host data or
# runtime evidence, not framework content.
NEVER_PACKAGE_FILES = {
    "AGENTS.md",
    ".agent/workflow.yaml",
    ".agent/upstream.yaml",
    ".agent/bin/workflow-check",
}
NEVER_PACKAGE_PREFIXES = (
    ".agent/tasks/",
    ".agent/registry/",
    ".git/",
)

# 框架仓库可以在自己的历史里保存开发期 Task（Owner 决策 B：self-hosting）。
# 这些文件仍不得进入分发包，但不构成 SOURCE_INVALID；其他宿主数据保持 fail-closed。
EXCLUDABLE_TASK_PREFIX = ".agent/tasks/"

# Files that are part of the distribution for reading/diffing/release
# provenance but are never copied over a host file.
PACKAGE_ONLY_FILES = {
    "README.md",
    "PACKAGE_MANIFEST.md",
    ".cnb.yml",
    "docs/design/agent-workflow.md",
}
PACKAGE_ONLY_PREFIXES = (
    ".agent/adoption/tests/",
    # P08 发布工程材料：随包分发用于版本追溯与可复现构建，但不安装到宿主。
    "scripts/release/",
    "docs/releases/",
)

# Framework-owned content installed into the host and eligible for upgrade.
MANAGED_FILES = {
    ".agent/README.md",
    ".agent/VERSION",
    ".agent/bin/.gitignore",
}
MANAGED_PREFIXES = (
    ".agent/roles/",
    ".agent/specs/",
    ".agent/templates/",
    ".agent/engine/",
    ".agent/adoption/",
)

PROTECTED_HOST_PATTERNS = [
    "AGENTS.md",
    "README.md",
    "PACKAGE_MANIFEST.md",
    ".cnb.yml",
    ".agent/workflow.yaml",
    ".agent/upstream.yaml",
    ".agent/tasks/",
    ".agent/registry/",
    ".agent/bin/workflow-check",
    "docs/design/",
    "<unmanaged host files>",
]

GENERATED_INSTALL_PATHS = [
    ".agent/workflow.yaml",
    ".agent/upstream.yaml",
    ".agent/bin/workflow-check",
]

DIST_EXCLUDED = [
    ".agent/workflow.yaml",
    ".agent/upstream.yaml",
    ".agent/tasks/",
    ".agent/registry/",
    ".agent/bin/workflow-check",
    ".git/",
    "AGENTS.md",
    "host business files",
    "secrets",
]


class AdoptionError(Exception):
    """A fail-closed error with a stable machine code and exit status."""

    def __init__(
        self,
        code: str,
        message: str,
        exit_code: int = EXIT_PRECONDITION,
        details: dict[str, Any] | None = None,
    ) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.exit_code = exit_code
        self.details = details or {}


# ---------------------------------------------------------------------------
# Generic helpers
# ---------------------------------------------------------------------------


def now_iso() -> str:
    return time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as fh:
        for chunk in iter(lambda: fh.read(1024 * 1024), b""):
            h.update(chunk)
    return h.hexdigest()


def short(sha: str | None) -> str:
    if sha is None:
        return "absent"
    return sha[:12]


def is_safe_rel_path(path: str) -> bool:
    if not path or path.startswith("/") or "\\" in path or "\x00" in path:
        return False
    p = PurePosixPath(path)
    if p.is_absolute():
        return False
    return all(part not in ("", ".", "..") for part in p.parts)


def ensure_safe_rel_path(path: str) -> None:
    if not is_safe_rel_path(path):
        raise AdoptionError("UNSAFE_PATH", f"不安全的相对路径: {path!r}")


def classify_path(path: str) -> str:
    """Return managed / package_only / never_package / unclassified."""
    if path in NEVER_PACKAGE_FILES or any(path.startswith(p) for p in NEVER_PACKAGE_PREFIXES):
        return "never_package"
    if path in PACKAGE_ONLY_FILES or any(path.startswith(p) for p in PACKAGE_ONLY_PREFIXES):
        return "package_only"
    if path in MANAGED_FILES or any(path.startswith(p) for p in MANAGED_PREFIXES):
        return "managed"
    return "unclassified"


def git_env() -> dict[str, str]:
    env = os.environ.copy()
    env.setdefault("GIT_TERMINAL_PROMPT", "0")
    return env


def git(root: Path | str, *args: str, check: bool = True) -> str:
    proc = subprocess.run(
        ["git", "-C", str(root), *args],
        capture_output=True,
        text=True,
        env=git_env(),
    )
    if check and proc.returncode != 0:
        raise AdoptionError(
            "GIT_ERROR",
            f"git {' '.join(args)} 失败（{root}）: {proc.stderr.strip() or proc.stdout.strip()}",
        )
    return proc.stdout.strip()


def git_bytes(root: Path | str, *args: str, check: bool = True) -> bytes:
    proc = subprocess.run(
        ["git", "-C", str(root), *args],
        capture_output=True,
        env=git_env(),
    )
    if check and proc.returncode != 0:
        raise AdoptionError(
            "GIT_ERROR",
            f"git {' '.join(args)} 失败（{root}）: {proc.stderr.decode('utf-8', 'replace').strip()}",
        )
    return proc.stdout


def git_root(path: Path) -> Path:
    out = git(path, "rev-parse", "--show-toplevel")
    return Path(out).resolve()


def require_git_root(target: Path) -> Path:
    resolved = target.resolve()
    if not resolved.is_dir():
        raise AdoptionError("TARGET_NOT_FOUND", f"目标目录不存在: {resolved}")
    try:
        root = git_root(resolved)
    except AdoptionError as exc:
        raise AdoptionError("TARGET_NOT_GIT", f"目标不是 Git 仓库: {resolved}") from exc
    if root != resolved:
        raise AdoptionError(
            "TARGET_NOT_REPOSITORY_ROOT",
            f"目标必须是 Git 仓库根目录: {resolved}（实际根 {root}）",
        )
    return root


def current_branch(root: Path) -> str:
    proc = subprocess.run(
        ["git", "-C", str(root), "symbolic-ref", "--quiet", "--short", "HEAD"],
        capture_output=True,
        text=True,
        env=git_env(),
    )
    if proc.returncode != 0:
        raise AdoptionError("DETACHED_HEAD", "目标仓库处于 detached HEAD，拒绝安装或升级")
    return proc.stdout.strip()


def tracked_dirty(root: Path) -> bool:
    return bool(git(root, "status", "--porcelain", "--untracked-files=no"))


def ref_exists(root: Path, ref: str) -> bool:
    proc = subprocess.run(
        ["git", "-C", str(root), "rev-parse", "--verify", "--quiet", ref],
        capture_output=True,
        text=True,
        env=git_env(),
    )
    return proc.returncode == 0


def validate_branch_name(root: Path, branch: str) -> None:
    if not branch or branch.startswith("-"):
        raise AdoptionError("INVALID_BRANCH", f"非法集成分支: {branch!r}")
    proc = subprocess.run(
        ["git", "-C", str(root), "check-ref-format", f"refs/heads/{branch}"],
        capture_output=True,
        text=True,
        env=git_env(),
    )
    if proc.returncode != 0:
        raise AdoptionError("INVALID_BRANCH", f"非法集成分支: {branch!r}")


def git_path(target: Path, name: str) -> Path:
    try:
        out = git(target, "rev-parse", "--path-format=absolute", "--git-path", name)
        p = Path(out)
    except AdoptionError:
        out = git(target, "rev-parse", "--git-path", name)
        p = Path(out)
        if not p.is_absolute():
            p = target / p
    return p.resolve()


# ---------------------------------------------------------------------------
# Minimal YAML subset (the files written by this tool plus workflow.yaml)
# ---------------------------------------------------------------------------


def _strip_yaml_comment(line: str) -> str:
    in_single = False
    in_double = False
    for idx, ch in enumerate(line):
        if ch == "'" and not in_double:
            in_single = not in_single
        elif ch == '"' and not in_single:
            in_double = not in_double
        elif ch == "#" and not in_single and not in_double:
            if idx == 0 or line[idx - 1].isspace():
                return line[:idx]
    return line


def _yaml_scalar(value: str) -> Any:
    value = value.strip()
    if value == "":
        return ""
    if (value.startswith('"') and value.endswith('"')) or (
        value.startswith("'") and value.endswith("'")
    ):
        if value.startswith('"'):
            try:
                return json.loads(value)
            except json.JSONDecodeError:
                return value[1:-1]
        return value[1:-1].replace("''", "'")
    if value == "{}":
        return {}
    if value == "[]":
        return []
    if value in ("true", "True"):
        return True
    if value in ("false", "False"):
        return False
    if value in ("null", "Null", "~"):
        return None
    if re.fullmatch(r"-?\d+", value):
        return int(value)
    return value


def parse_simple_yaml(text: str) -> dict[str, Any]:
    """Parse the mapping-only YAML subset written by this tool.

    Lists and anchors are intentionally rejected: `.agent/workflow.yaml` and
    `.agent/upstream.yaml` are small machine files, and silently guessing at a
    richer YAML dialect would weaken the fail-closed boundary.
    """
    root: dict[str, Any] = {}
    stack: list[tuple[int, dict[str, Any]]] = [(-1, root)]
    for lineno, raw in enumerate(text.splitlines(), 1):
        line = _strip_yaml_comment(raw).rstrip()
        if not line.strip():
            continue
        indent = len(line) - len(line.lstrip(" "))
        content = line.strip()
        if content.startswith("- "):
            raise AdoptionError("YAML_UNSUPPORTED", f"不支持 YAML 列表（第 {lineno} 行）")
        key, sep, raw_value = content.partition(":")
        if not sep:
            raise AdoptionError("YAML_PARSE", f"无法解析 YAML（第 {lineno} 行）: {content!r}")
        key = key.strip()
        if not key:
            raise AdoptionError("YAML_PARSE", f"空 key（第 {lineno} 行）")
        while stack and indent <= stack[-1][0]:
            stack.pop()
        if not stack:
            raise AdoptionError("YAML_PARSE", f"缩进异常（第 {lineno} 行）")
        parent = stack[-1][1]
        value = raw_value.strip()
        if value == "":
            child: dict[str, Any] = {}
            parent[key] = child
            stack.append((indent, child))
        else:
            parent[key] = _yaml_scalar(value)
    return root


def yaml_quote(value: str) -> str:
    if "\n" in value or "\x00" in value:
        raise AdoptionError("YAML_VALUE", "YAML 标量不得包含换行或 NUL")
    return json.dumps(value, ensure_ascii=False)


# ---------------------------------------------------------------------------
# Source distribution
# ---------------------------------------------------------------------------


@dataclasses.dataclass
class DistFile:
    path: str
    data: bytes
    mode: int
    sha256: str
    kind: str


@dataclasses.dataclass
class Distribution:
    root: Path
    commit: str
    framework_version: str
    repository: str
    manifest: dict[str, Any]
    files: dict[str, DistFile]
    package_sha256: str
    distribution_name: str
    staging_parent: Path | None

    @property
    def managed(self) -> dict[str, DistFile]:
        return {p: f for p, f in self.files.items() if f.kind == "managed"}

    @property
    def package_only(self) -> dict[str, DistFile]:
        return {p: f for p, f in self.files.items() if f.kind == "package_only"}

    def cleanup(self) -> None:
        if self.staging_parent and self.staging_parent.exists():
            shutil.rmtree(self.staging_parent, ignore_errors=True)

    def read(self, path: str) -> bytes:
        f = self.files.get(path)
        if f is None:
            full = self.root / path
            if not full.is_file():
                raise AdoptionError("DIST_FILE_MISSING", f"分发包缺少文件: {path}")
            return full.read_bytes()
        return f.data


def _git_tracked_entries(root: Path) -> list[tuple[str, str]]:
    raw = git_bytes(root, "ls-files", "-s", "-z")
    entries: list[tuple[str, str]] = []
    for record in raw.split(b"\0"):
        if not record:
            continue
        meta, _, path_bytes = record.partition(b"\t")
        parts = meta.split()
        if len(parts) != 3:
            raise AdoptionError("GIT_LS_FILES", f"无法解析 git ls-files 输出: {record!r}")
        mode = parts[0].decode("ascii")
        path = path_bytes.decode("utf-8", "strict")
        entries.append((mode, path))
    return entries


def _read_source_version(root: Path) -> str:
    version_path = root / ".agent/VERSION"
    if not version_path.is_file():
        raise AdoptionError("SOURCE_VERSION_MISSING", "源快照缺少 .agent/VERSION")
    value = version_path.read_text(encoding="utf-8").strip()
    if not value:
        raise AdoptionError("SOURCE_VERSION_EMPTY", ".agent/VERSION 为空")
    return value


def _manifest_compatibility() -> dict[str, Any]:
    return {
        "config_schema": CONFIG_SCHEMA,
        "state_schema": list(SUPPORTED_STATE_SCHEMAS),
    }


def build_manifest(
    root: Path,
    commit: str,
    tree: str,
    repository: str,
    version: str,
    files: dict[str, DistFile],
) -> dict[str, Any]:
    managed = sorted(p for p, f in files.items() if f.kind == "managed")
    package_only = sorted(p for p, f in files.items() if f.kind == "package_only")
    return {
        "format": DIST_FORMAT,
        "kind": "source-distribution",
        "framework_version": version,
        "source": {
            "repository": repository,
            "commit": commit,
            "tree": tree,
            "dirty": False,
        },
        "compatibility": _manifest_compatibility(),
        "files": [
            {
                "path": p,
                "sha256": files[p].sha256,
                "mode": "100755" if files[p].mode & 0o111 else "100644",
                "install": files[p].kind,
            }
            for p in sorted(files)
        ],
        "install": {
            "managed": managed,
            "package_only": package_only,
            "generated": list(GENERATED_INSTALL_PATHS),
            "protected": list(PROTECTED_HOST_PATTERNS),
            "runtime_binary": ".agent/bin/workflow-check",
        },
        "excluded": list(DIST_EXCLUDED),
        "release": {
            "tag": "",
            "url": "",
            "note": "Publication (tag, CI build, checksums, release assets) is owned by the release pipeline and is never inferred from the source tree.",
        },
    }


def canonical_manifest_bytes(manifest: dict[str, Any]) -> bytes:
    return (json.dumps(manifest, indent=2, sort_keys=True, ensure_ascii=False) + "\n").encode(
        "utf-8"
    )


def _materialize_dist(
    manifest: dict[str, Any], files: dict[str, DistFile]
) -> tuple[Path, Path]:
    parent = Path(tempfile.mkdtemp(prefix="workflow-adoption-dist-"))
    commit = manifest["source"]["commit"]
    top = f"agent-workflow-src-{commit[:12]}"
    root = parent / top
    root.mkdir(parents=True)
    for path in sorted(files):
        ensure_safe_rel_path(path)
        dest = root.joinpath(*PurePosixPath(path).parts)
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_bytes(files[path].data)
        os.chmod(dest, files[path].mode)
    (root / "MANIFEST.json").write_bytes(canonical_manifest_bytes(manifest))
    return parent, root


def create_source_distribution(
    source: Path,
    output_dir: Path,
    repository: str = DEFAULT_REPOSITORY,
) -> dict[str, Any]:
    root = require_git_root(source)
    output_dir = output_dir.resolve()
    if tracked_dirty(root):
        raise AdoptionError(
            "SOURCE_DIRTY",
            "源快照存在未提交的 tracked 变更，拒绝生成 Source Distribution",
        )
    status_lines = git(root, "status", "--porcelain").splitlines()
    if output_dir.is_relative_to(root):
        rel_output = output_dir.relative_to(root).as_posix()
        status_lines = [
            line
            for line in status_lines
            if not (
                line[3:].strip().strip('"').rstrip("/") == rel_output
                or line[3:].strip().strip('"').startswith(rel_output + "/")
            )
        ]
    if status_lines:
        raise AdoptionError(
            "SOURCE_UNTRACKED",
            "源快照存在未跟踪文件，拒绝生成可复现的 Source Distribution",
            details={"paths": status_lines},
        )
    commit = git(root, "rev-parse", "HEAD")
    if not FULL_SHA.match(commit):
        raise AdoptionError("SOURCE_COMMIT", f"无法获得完整 40 位 Commit SHA: {commit!r}")
    tree = git(root, "rev-parse", "HEAD^{tree}")
    version = _read_source_version(root)

    entries = _git_tracked_entries(root)
    violations: list[str] = []
    unclassified: list[str] = []
    files: dict[str, DistFile] = {}

    for mode, path in entries:
        if not is_safe_rel_path(path):
            violations.append(f"{path} (unsafe path)")
            continue
        kind = classify_path(path)
        if kind == "never_package":
            if path.startswith(EXCLUDABLE_TASK_PREFIX):
                # 框架仓库自托管：Task 数据可被跟踪，但绝不进入分发包。
                continue
            violations.append(f"{path} (host/protected data)")
            continue
        if kind == "unclassified":
            unclassified.append(path)
            continue
        if mode == "120000":
            violations.append(f"{path} (symbolic link)")
            continue
        if mode == "160000":
            violations.append(f"{path} (gitlink/submodule)")
            continue
        full = root.joinpath(*PurePosixPath(path).parts)
        if full.is_symlink() or not full.is_file():
            violations.append(f"{path} (not a regular file)")
            continue
        data = full.read_bytes()
        file_mode = 0o755 if mode == "100755" else 0o644
        files[path] = DistFile(
            path=path,
            data=data,
            mode=file_mode,
            sha256=sha256_bytes(data),
            kind=kind,
        )

    if violations:
        raise AdoptionError(
            "SOURCE_INVALID",
            "源快照包含不得打包的文件，拒绝生成 Source Distribution",
            details={"violations": sorted(violations)},
        )
    if unclassified:
        raise AdoptionError(
            "SOURCE_UNCLASSIFIED",
            "源快照存在未分类文件；请先在 PACKAGE_MANIFEST.md 与工具分类表中归类",
            details={"unclassified": sorted(unclassified)},
        )
    if not files:
        raise AdoptionError("SOURCE_EMPTY", "源快照没有可打包文件")

    manifest = build_manifest(root, commit, tree, repository, version, files)
    manifest_bytes = canonical_manifest_bytes(manifest)

    output_dir.mkdir(parents=True, exist_ok=True)
    archive_name = f"agent-workflow-src-{commit[:12]}.tar.gz"
    archive_path = output_dir / archive_name
    top = f"agent-workflow-src-{commit[:12]}"

    tmp_archive = archive_path.with_name(f".{archive_name}.tmp-{os.getpid()}")
    with tmp_archive.open("wb") as raw:
        with gzip.GzipFile(fileobj=raw, mode="wb", mtime=0, filename="") as gz:
            with tarfile.open(fileobj=gz, mode="w", format=tarfile.USTAR_FORMAT) as tar:
                _tar_add(tar, f"{top}/MANIFEST.json", manifest_bytes, 0o644)
                for path in sorted(files):
                    _tar_add(
                        tar,
                        f"{top}/{path}",
                        files[path].data,
                        files[path].mode,
                    )
    os.replace(tmp_archive, archive_path)
    os.chmod(archive_path, 0o644)
    archive_sha = sha256_file(archive_path)
    checksum_path = output_dir / f"{archive_name}.sha256"
    checksum_path.write_text(f"{archive_sha}  {archive_name}\n", encoding="utf-8")

    return {
        "status": "DIST_OK",
        "archive": str(archive_path),
        "checksum_file": str(checksum_path),
        "sha256": archive_sha,
        "commit": commit,
        "framework_version": version,
        "repository": repository,
        "top_level": top,
        "file_count": len(files),
        "manifest": manifest,
    }


def _tar_add(tar: tarfile.TarFile, name: str, data: bytes, mode: int) -> None:
    info = tarfile.TarInfo(name)
    info.size = len(data)
    info.mtime = 0
    info.mode = mode
    info.uid = 0
    info.gid = 0
    info.uname = ""
    info.gname = ""
    info.type = tarfile.REGTYPE
    tar.addfile(info, io.BytesIO(data))


def _validate_member_name(name: str) -> None:
    if not name or name.startswith("/") or "\\" in name or "\x00" in name:
        raise AdoptionError("ARCHIVE_UNSAFE_PATH", f"归档包含不安全路径: {name!r}")
    p = PurePosixPath(name)
    if p.is_absolute() or any(part in ("", ".", "..") for part in p.parts):
        raise AdoptionError("ARCHIVE_UNSAFE_PATH", f"归档包含目录穿越路径: {name!r}")


def _validate_manifest(manifest: Any, expected_commit: str | None) -> dict[str, Any]:
    if not isinstance(manifest, dict):
        raise AdoptionError("DIST_MANIFEST", "MANIFEST.json 不是对象")
    if manifest.get("format") != DIST_FORMAT:
        raise AdoptionError("DIST_FORMAT", f"不支持的 distribution format: {manifest.get('format')!r}")
    if manifest.get("kind") != "source-distribution":
        raise AdoptionError("DIST_KIND", f"不支持的 distribution kind: {manifest.get('kind')!r}")
    source = manifest.get("source")
    if not isinstance(source, dict):
        raise AdoptionError("DIST_MANIFEST", "MANIFEST.json 缺少 source")
    commit = str(source.get("commit", ""))
    if not FULL_SHA.match(commit):
        raise AdoptionError("DIST_COMMIT", f"MANIFEST.json source.commit 非法: {commit!r}")
    if expected_commit and commit.lower() != expected_commit.lower():
        raise AdoptionError(
            "DIST_COMMIT_MISMATCH",
            f"分发包 Commit {commit} 与指定来源 {expected_commit} 不一致",
        )
    compatibility = manifest.get("compatibility")
    if not isinstance(compatibility, dict):
        raise AdoptionError("DIST_COMPAT", "MANIFEST.json 缺少 compatibility")
    if compatibility.get("config_schema") != CONFIG_SCHEMA:
        raise AdoptionError(
            "DIST_COMPAT_CONFIG",
            f"分发包 Config Schema {compatibility.get('config_schema')!r} 不受支持",
        )
    if list(compatibility.get("state_schema") or []) != list(SUPPORTED_STATE_SCHEMAS):
        raise AdoptionError(
            "DIST_COMPAT_STATE",
            f"分发包 State Schema {compatibility.get('state_schema')!r} 不受支持",
        )
    version = str(manifest.get("framework_version", ""))
    if not version:
        raise AdoptionError("DIST_VERSION", "MANIFEST.json 缺少 framework_version")
    if not isinstance(manifest.get("files"), list):
        raise AdoptionError("DIST_MANIFEST", "MANIFEST.json 缺少 files 列表")
    return manifest


def _files_from_manifest(
    manifest: dict[str, Any], data_by_path: dict[str, bytes], mode_by_path: dict[str, int]
) -> dict[str, DistFile]:
    files: dict[str, DistFile] = {}
    for entry in manifest["files"]:
        if not isinstance(entry, dict):
            raise AdoptionError("DIST_MANIFEST", "MANIFEST.json files 条目非法")
        path = entry.get("path")
        if not isinstance(path, str) or not is_safe_rel_path(path):
            raise AdoptionError("DIST_MANIFEST", f"MANIFEST.json 包含不安全路径: {path!r}")
        kind = classify_path(path)
        if kind == "never_package":
            raise AdoptionError(
                "DIST_PROTECTED_MEMBER",
                f"分发包包含宿主/受保护数据: {path}",
            )
        if kind == "unclassified":
            raise AdoptionError(
                "DIST_UNCLASSIFIED_MEMBER",
                f"分发包包含未分类成员: {path}",
            )
        declared = entry.get("install")
        if declared != kind:
            raise AdoptionError(
                "DIST_CLASS_MISMATCH",
                f"分发包对 {path} 的 install 分类 {declared!r} 与工具分类 {kind!r} 不一致",
            )
        expected_sha = str(entry.get("sha256", ""))
        if not re.fullmatch(r"[0-9a-f]{64}", expected_sha):
            raise AdoptionError("DIST_MANIFEST", f"{path} 的 sha256 非法")
        if path not in data_by_path:
            raise AdoptionError("DIST_MEMBER_MISSING", f"分发包缺少 manifest 声明的成员: {path}")
        data = data_by_path[path]
        if sha256_bytes(data) != expected_sha:
            raise AdoptionError("DIST_HASH_MISMATCH", f"分发包成员 SHA256 不匹配: {path}")
        mode = mode_by_path.get(path, 0o644)
        files[path] = DistFile(
            path=path,
            data=data,
            mode=mode,
            sha256=expected_sha,
            kind=kind,
        )

    managed = sorted(p for p, f in files.items() if f.kind == "managed")
    package_only = sorted(p for p, f in files.items() if f.kind == "package_only")
    install = manifest.get("install") or {}
    if sorted(install.get("managed") or []) != managed:
        raise AdoptionError(
            "DIST_CLASS_MISMATCH",
            "MANIFEST.json install.managed 与实际 managed 成员不一致",
        )
    if sorted(install.get("package_only") or []) != package_only:
        raise AdoptionError(
            "DIST_CLASS_MISMATCH",
            "MANIFEST.json install.package_only 与实际 package_only 成员不一致",
        )
    return files


def _load_archive(
    package: Path,
    expected_commit: str | None,
    expected_sha256: str | None,
    repository: str,
    require_pinned: bool,
) -> Distribution:
    package = package.resolve()
    if not package.is_file():
        raise AdoptionError("DIST_NOT_FOUND", f"分发包不存在: {package}")
    actual_sha = sha256_file(package)
    if expected_sha256 and actual_sha.lower() != expected_sha256.lower():
        raise AdoptionError(
            "DIST_SHA_MISMATCH",
            f"分发包 SHA256 不匹配: 实际 {actual_sha}, 期望 {expected_sha256}",
        )
    if not expected_sha256 and require_pinned:
        raise AdoptionError(
            "DIST_SHA_REQUIRED",
            "使用 --package 时必须提供 --distribution-sha256，拒绝未固定的安装源",
        )

    data_by_path: dict[str, bytes] = {}
    mode_by_path: dict[str, int] = {}
    manifest_bytes: bytes | None = None
    member_names: set[str] = set()
    tops: set[str] = set()

    try:
        with tarfile.open(package, "r:gz") as tar:
            for member in tar.getmembers():
                _validate_member_name(member.name)
                if member.issym() or member.islnk():
                    raise AdoptionError(
                        "ARCHIVE_SYMLINK",
                        f"归档包含符号链接/硬链接，拒绝解析: {member.name}",
                    )
                if member.isdev() or member.isfifo():
                    raise AdoptionError(
                        "ARCHIVE_SPECIAL",
                        f"归档包含特殊文件，拒绝解析: {member.name}",
                    )
                if member.isdir():
                    continue
                if not member.isfile():
                    raise AdoptionError(
                        "ARCHIVE_TYPE",
                        f"归档包含非普通文件成员: {member.name}",
                    )
                parts = PurePosixPath(member.name).parts
                tops.add(parts[0])
                member_names.add(member.name)
                if len(parts) == 2 and parts[1] == "MANIFEST.json":
                    fh = tar.extractfile(member)
                    if fh is None:
                        raise AdoptionError("ARCHIVE_READ", f"无法读取 {member.name}")
                    manifest_bytes = fh.read()
                    continue
                rel = "/".join(parts[1:])
                if not is_safe_rel_path(rel):
                    raise AdoptionError("ARCHIVE_UNSAFE_PATH", f"归档成员路径非法: {member.name}")
                if rel in data_by_path:
                    raise AdoptionError("ARCHIVE_DUPLICATE", f"归档成员重复: {rel}")
                fh = tar.extractfile(member)
                if fh is None:
                    raise AdoptionError("ARCHIVE_READ", f"无法读取 {member.name}")
                data_by_path[rel] = fh.read()
                mode_by_path[rel] = 0o755 if member.mode & 0o111 else 0o644
    except tarfile.TarError as exc:
        raise AdoptionError("ARCHIVE_INVALID", f"无法解析 tar.gz: {exc}") from exc

    if len(tops) != 1:
        raise AdoptionError(
            "ARCHIVE_TOPLEVEL",
            f"分发包必须且只能有一个顶层目录，实际: {sorted(tops)}",
        )
    top = next(iter(tops))
    manifest_member = f"{top}/MANIFEST.json"
    if manifest_member not in member_names or manifest_bytes is None:
        raise AdoptionError("ARCHIVE_MANIFEST", "分发包顶层缺少 MANIFEST.json")

    try:
        manifest = json.loads(manifest_bytes.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise AdoptionError("DIST_MANIFEST", f"MANIFEST.json 无法解析: {exc}") from exc
    manifest = _validate_manifest(manifest, expected_commit)
    files = _files_from_manifest(manifest, data_by_path, mode_by_path)

    expected_members = {manifest_member} | {f"{top}/{p}" for p in files}
    if member_names != expected_members:
        missing = sorted(expected_members - member_names)
        extra = sorted(member_names - expected_members)
        raise AdoptionError(
            "DIST_MEMBERSHIP",
            "分发包成员与 MANIFEST.json 不一致",
            details={"missing": missing, "extra": extra},
        )

    parent, root = _materialize_dist(manifest, files)
    return Distribution(
        root=root,
        commit=manifest["source"]["commit"],
        framework_version=manifest["framework_version"],
        repository=str(manifest["source"].get("repository") or repository),
        manifest=manifest,
        files=files,
        package_sha256=actual_sha,
        distribution_name=package.name,
        staging_parent=parent,
    )


def _load_source_tree(
    source: Path,
    expected_commit: str | None,
    repository: str,
) -> Distribution:
    root = require_git_root(source)
    if tracked_dirty(root):
        raise AdoptionError("SOURCE_DIRTY", "安装源存在未提交的 tracked 变更")
    status = git(root, "status", "--porcelain")
    if status:
        raise AdoptionError(
            "SOURCE_UNTRACKED",
            "安装源存在未跟踪文件，无法证明来源内容",
            details={"paths": status.splitlines()},
        )
    commit = git(root, "rev-parse", "HEAD")
    if not FULL_SHA.match(commit):
        raise AdoptionError("SOURCE_COMMIT", f"安装源 Commit 非法: {commit!r}")
    if expected_commit and commit.lower() != expected_commit.lower():
        raise AdoptionError(
            "SOURCE_COMMIT_MISMATCH",
            f"安装源 Commit {commit} 与指定来源 {expected_commit} 不一致",
        )
    tree = git(root, "rev-parse", "HEAD^{tree}")
    version = _read_source_version(root)
    files: dict[str, DistFile] = {}
    unclassified: list[str] = []
    violations: list[str] = []
    for mode, path in _git_tracked_entries(root):
        if not is_safe_rel_path(path):
            violations.append(f"{path} (unsafe path)")
            continue
        kind = classify_path(path)
        if kind == "never_package":
            if path.startswith(EXCLUDABLE_TASK_PREFIX):
                # 与 dist 一致：框架仓库自托管的 Task 数据不安装、也不使来源无效。
                continue
            violations.append(f"{path} (host/protected data)")
            continue
        if kind == "unclassified":
            unclassified.append(path)
            continue
        if mode == "120000" or mode == "160000":
            violations.append(f"{path} (symlink/submodule)")
            continue
        full = root.joinpath(*PurePosixPath(path).parts)
        if not full.is_file():
            violations.append(f"{path} (not a regular file)")
            continue
        data = full.read_bytes()
        files[path] = DistFile(
            path=path,
            data=data,
            mode=0o755 if mode == "100755" else 0o644,
            sha256=sha256_bytes(data),
            kind=kind,
        )
    if violations:
        raise AdoptionError(
            "SOURCE_INVALID",
            "安装源包含不得打包/安装的文件",
            details={"violations": sorted(violations)},
        )
    if unclassified:
        raise AdoptionError(
            "SOURCE_UNCLASSIFIED",
            "安装源存在未分类文件",
            details={"unclassified": sorted(unclassified)},
        )
    manifest = build_manifest(root, commit, tree, repository, version, files)
    manifest_bytes = canonical_manifest_bytes(manifest)
    parent, staged = _materialize_dist(manifest, files)
    return Distribution(
        root=staged,
        commit=commit,
        framework_version=version,
        repository=repository,
        manifest=manifest,
        files=files,
        package_sha256=sha256_bytes(manifest_bytes),
        distribution_name=f"source-tree-{commit[:12]}",
        staging_parent=parent,
    )


def load_distribution(
    *,
    package: Path | None = None,
    source: Path | None = None,
    expected_commit: str | None = None,
    expected_sha256: str | None = None,
    repository: str = DEFAULT_REPOSITORY,
    require_pinned: bool = False,
) -> Distribution:
    if bool(package) == bool(source):
        raise AdoptionError("DIST_SOURCE", "必须且只能指定 --package 或 --source 之一")
    if expected_commit is not None and not FULL_SHA.match(expected_commit):
        raise AdoptionError(
            "DIST_COMMIT",
            f"--source-commit 必须是完整 40 位十六进制 SHA: {expected_commit!r}",
        )
    if package:
        return _load_archive(package, expected_commit, expected_sha256, repository, require_pinned)
    return _load_source_tree(source, expected_commit, repository)  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# Host path inspection and protected inventory
# ---------------------------------------------------------------------------


def inspect_host_path(target: Path, rel: str) -> tuple[str, bytes | None, int]:
    ensure_safe_rel_path(rel)
    parts = PurePosixPath(rel).parts
    cur = target
    for idx, part in enumerate(parts):
        cur = cur / part
        if cur.is_symlink():
            return "symlink", None, 0
        if not cur.exists():
            return "absent", None, 0
        if idx < len(parts) - 1 and not cur.is_dir():
            return "notdir", None, 0
    if cur.is_dir():
        return "dir", None, 0
    if not cur.is_file():
        return "special", None, 0
    return "file", cur.read_bytes(), stat.S_IMODE(cur.stat().st_mode)


def _inventory_walk(root: Path, rel_base: str, out: dict[str, str]) -> None:
    base = root.joinpath(*PurePosixPath(rel_base).parts)
    if not base.exists() and not base.is_symlink():
        return
    if base.is_symlink():
        out[rel_base] = "symlink:" + sha256_bytes(os.readlink(base).encode("utf-8"))
        return
    if base.is_file():
        out[rel_base] = sha256_file(base)
        return
    for dirpath, dirnames, filenames in os.walk(base, followlinks=False):
        current = Path(dirpath)
        for name in sorted(dirnames):
            p = current / name
            rel = p.relative_to(root).as_posix()
            if p.is_symlink():
                out[rel] = "symlink:" + sha256_bytes(os.readlink(p).encode("utf-8"))
        for name in sorted(filenames):
            p = current / name
            rel = p.relative_to(root).as_posix()
            if p.is_symlink():
                out[rel] = "symlink:" + sha256_bytes(os.readlink(p).encode("utf-8"))
            elif p.is_file():
                out[rel] = sha256_file(p)


def inventory_protected(target: Path) -> dict[str, str]:
    """Hash host-owned files that no installation/upgrade may touch."""
    out: dict[str, str] = {}
    for rel in ("AGENTS.md", "README.md", "PACKAGE_MANIFEST.md", ".cnb.yml", ".agent/workflow.yaml"):
        p = target / rel
        if p.exists() or p.is_symlink():
            _inventory_walk(target, rel, out)
    for rel in (".agent/tasks", ".agent/registry", "docs/design"):
        _inventory_walk(target, rel, out)
    return out


def diff_inventory(before: dict[str, str], after: dict[str, str]) -> dict[str, list[str]]:
    changed = sorted(k for k in before.keys() & after.keys() if before[k] != after[k])
    removed = sorted(before.keys() - after.keys())
    added = sorted(after.keys() - before.keys())
    return {"changed": changed, "removed": removed, "added": added}


# ---------------------------------------------------------------------------
# Upstream provenance and snapshots
# ---------------------------------------------------------------------------


def upstream_path(target: Path) -> Path:
    return target / ".agent/upstream.yaml"


def config_path(target: Path) -> Path:
    return target / ".agent/workflow.yaml"


def read_config(target: Path) -> tuple[str, dict[str, Any], str]:
    path = config_path(target)
    if not path.is_file():
        raise AdoptionError("CONFIG_MISSING", "缺少 .agent/workflow.yaml")
    text = path.read_text(encoding="utf-8")
    data = parse_simple_yaml(text)
    if data.get("schema_version") != CONFIG_SCHEMA:
        raise AdoptionError(
            "CONFIG_SCHEMA",
            f"不支持的 workflow.yaml schema_version={data.get('schema_version')!r}（期望 {CONFIG_SCHEMA}）",
        )
    git_cfg = data.get("git")
    if not isinstance(git_cfg, dict):
        raise AdoptionError("CONFIG_GIT", "workflow.yaml 缺少 git 配置")
    branch = str(git_cfg.get("integration_branch") or "")
    if not branch:
        raise AdoptionError("CONFIG_BRANCH", "workflow.yaml 缺少 git.integration_branch")
    resources = data.get("resources")
    if resources is None:
        resources = {}
    if not isinstance(resources, dict):
        raise AdoptionError("CONFIG_RESOURCES", "workflow.yaml resources 必须是映射")
    return text, data, branch


def read_upstream(target: Path) -> dict[str, Any]:
    path = upstream_path(target)
    if not path.is_file():
        raise AdoptionError("NOT_INSTALLED", "缺少 .agent/upstream.yaml，无法证明安装来源")
    text = path.read_text(encoding="utf-8")
    data = parse_simple_yaml(text)
    if data.get("schema_version") != UPSTREAM_SCHEMA:
        raise AdoptionError(
            "UPSTREAM_SCHEMA",
            f"不支持的 upstream schema_version={data.get('schema_version')!r}",
        )
    source = data.get("source")
    snapshot = data.get("snapshot")
    installed = data.get("installed")
    runtime = data.get("runtime")
    if not isinstance(source, dict) or not isinstance(snapshot, dict) or not isinstance(installed, dict):
        raise AdoptionError("UPSTREAM_FORMAT", "upstream.yaml 缺少 source/snapshot/installed 结构")
    commit = str(source.get("commit") or "")
    if not FULL_SHA.match(commit):
        raise AdoptionError("UPSTREAM_COMMIT", f"upstream.yaml source.commit 非法: {commit!r}")
    ref = str(snapshot.get("ref") or "")
    if not ref or not is_safe_rel_path(ref):
        raise AdoptionError("UPSTREAM_SNAPSHOT_REF", f"upstream.yaml snapshot.ref 非法: {ref!r}")
    if not isinstance(runtime, dict):
        data["runtime"] = {}
    return data


def render_upstream(data: dict[str, Any]) -> str:
    source = data["source"]
    installed = data["installed"]
    snapshot = data["snapshot"]
    runtime = data.get("runtime") or {}
    lines = [
        "# Workflow upstream provenance. Machine-written by workflow-adoption.",
        "# Do not edit by hand; it is only updated by a successful install/upgrade transaction.",
        "schema_version: 1",
        "",
        "source:",
        f"  repository: {yaml_quote(str(source['repository']))}",
        f"  kind: {yaml_quote(str(source.get('kind', 'source-distribution')))}",
        f"  name: {yaml_quote(str(source.get('name', '')))}",
        f"  sha256: {yaml_quote(str(source.get('sha256', '')))}",
        f"  framework_version: {yaml_quote(str(source['framework_version']))}",
        f"  commit: {yaml_quote(str(source['commit']))}",
        f"  release: {yaml_quote(str(source.get('release', '')))}",
        "",
        "installed:",
        f"  action: {yaml_quote(str(installed['action']))}",
        f"  at: {yaml_quote(str(installed['at']))}",
        f"  owner: {yaml_quote(str(installed.get('owner', '')))}",
        f"  branch: {yaml_quote(str(installed.get('branch', '')))}",
        f"  integration_branch: {yaml_quote(str(installed.get('integration_branch', '')))}",
        f"  previous_commit: {yaml_quote(str(installed.get('previous_commit', '')))}",
        "",
        "snapshot:",
        "  store: \"git-dir:workflow-adoption\"",
        f"  ref: {yaml_quote(str(snapshot['ref']))}",
        f"  manifest_sha256: {yaml_quote(str(snapshot['manifest_sha256']))}",
        f"  file_count: {int(snapshot['file_count'])}",
        "",
        "runtime:",
        "  workflow_check: \".agent/bin/workflow-check\"",
        f"  workflow_check_sha256: {yaml_quote(str(runtime.get('workflow_check_sha256', '')))}",
        f"  provenance: {yaml_quote(str(runtime.get('provenance', '')))}",
        "",
        "compatibility:",
        f"  config_schema: {CONFIG_SCHEMA}",
        "  state_schema: \"2,3\"",
    ]
    last = data.get("last_upgrade")
    if isinstance(last, dict):
        lines += [
            "",
            "last_upgrade:",
            f"  from_commit: {yaml_quote(str(last.get('from_commit', '')))}",
            f"  to_commit: {yaml_quote(str(last.get('to_commit', '')))}",
            f"  at: {yaml_quote(str(last.get('at', '')))}",
            f"  distribution_sha256: {yaml_quote(str(last.get('distribution_sha256', '')))}",
        ]
    return "\n".join(lines) + "\n"


def snapshot_root(target: Path) -> Path:
    return git_path(target, "workflow-adoption") / "snapshots"


def _snapshot_manifest(commit: str, dist: Distribution) -> dict[str, Any]:
    managed = dist.managed
    return {
        "format": 1,
        "commit": commit,
        "framework_version": dist.framework_version,
        "repository": dist.repository,
        "distribution_sha256": dist.package_sha256,
        "created_at": now_iso(),
        "files": [
            {
                "path": p,
                "sha256": managed[p].sha256,
                "mode": "100755" if managed[p].mode & 0o111 else "100644",
            }
            for p in sorted(managed)
        ],
    }


@dataclasses.dataclass
class Snapshot:
    commit: str
    root: Path
    files: dict[str, str]
    manifest_sha256: str
    manifest: dict[str, Any]
    created: bool = False

    def read(self, path: str) -> bytes:
        full = self.root / "files" / path
        if not full.is_file() or full.is_symlink():
            raise AdoptionError("SNAPSHOT_MISSING_FILE", f"B 快照缺少文件: {path}")
        return full.read_bytes()


def write_snapshot(target: Path, dist: Distribution) -> Snapshot:
    base = snapshot_root(target) / dist.commit
    manifest = _snapshot_manifest(dist.commit, dist)
    manifest_bytes = canonical_manifest_bytes(manifest)
    manifest_sha = sha256_bytes(manifest_bytes)

    if base.exists():
        existing = read_snapshot_at(target, f"snapshots/{dist.commit}")
        expected_files = {path: f.sha256 for path, f in dist.managed.items()}
        if existing.files != expected_files:
            raise AdoptionError(
                "SNAPSHOT_CONFLICT",
                f"已存在的 B 快照与当前分发包内容不一致: {base}",
            )
        # 快照内容一致即可复用；created_at 不参与内容身份判断。
        return dataclasses.replace(existing, created=False)

    base.parent.mkdir(parents=True, exist_ok=True)
    tmp = base.with_name(f".{base.name}.tmp-{os.getpid()}-{uuid.uuid4().hex[:6]}")
    try:
        (tmp / "files").mkdir(parents=True)
        for path, f in sorted(dist.managed.items()):
            dest = tmp / "files" / path
            dest.parent.mkdir(parents=True, exist_ok=True)
            dest.write_bytes(f.data)
            os.chmod(dest, 0o644)
        (tmp / "manifest.json").write_bytes(manifest_bytes)
        os.replace(tmp, base)
    except Exception:
        shutil.rmtree(tmp, ignore_errors=True)
        raise

    files = {path: f.sha256 for path, f in dist.managed.items()}
    return Snapshot(
        commit=dist.commit,
        root=base,
        files=files,
        manifest_sha256=manifest_sha,
        manifest=manifest,
        created=True,
    )


def read_snapshot_at(target: Path, ref: str) -> Snapshot:
    if not is_safe_rel_path(ref):
        raise AdoptionError("SNAPSHOT_REF", f"快照 ref 非法: {ref!r}")
    base = git_path(target, "workflow-adoption") / ref
    if not base.is_dir() or base.is_symlink():
        raise AdoptionError("BASE_UNKNOWN", f"找不到上次安装的 B 快照: {ref}")
    manifest_path = base / "manifest.json"
    if not manifest_path.is_file() or manifest_path.is_symlink():
        raise AdoptionError("BASE_UNKNOWN", f"B 快照缺少 manifest: {ref}")
    raw = manifest_path.read_bytes()
    try:
        manifest = json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise AdoptionError("BASE_UNKNOWN", f"B 快照 manifest 无法解析: {exc}") from exc
    if manifest.get("format") != 1:
        raise AdoptionError("BASE_UNKNOWN", "B 快照 manifest format 不受支持")
    commit = str(manifest.get("commit") or "")
    if not FULL_SHA.match(commit):
        raise AdoptionError("BASE_UNKNOWN", "B 快照 manifest commit 非法")
    files: dict[str, str] = {}
    entries = manifest.get("files")
    if not isinstance(entries, list):
        raise AdoptionError("BASE_UNKNOWN", "B 快照 manifest 缺少 files")
    for entry in entries:
        if not isinstance(entry, dict):
            raise AdoptionError("BASE_UNKNOWN", "B 快照 manifest files 条目非法")
        path = str(entry.get("path") or "")
        expected = str(entry.get("sha256") or "")
        if not is_safe_rel_path(path) or not re.fullmatch(r"[0-9a-f]{64}", expected):
            raise AdoptionError("BASE_UNKNOWN", f"B 快照 manifest 条目非法: {path!r}")
        full = base / "files" / path
        if not full.is_file() or full.is_symlink():
            raise AdoptionError("BASE_UNKNOWN", f"B 快照缺少内容: {path}")
        if sha256_file(full) != expected:
            raise AdoptionError("BASE_UNKNOWN", f"B 快照内容已损坏: {path}")
        files[path] = expected
    return Snapshot(
        commit=commit,
        root=base,
        files=files,
        manifest_sha256=sha256_bytes(raw),
        manifest=manifest,
        created=False,
    )


def load_base_snapshot(target: Path, upstream: dict[str, Any]) -> Snapshot:
    source = upstream["source"]
    snapshot = upstream["snapshot"]
    ref = str(snapshot.get("ref") or "")
    try:
        base = read_snapshot_at(target, ref)
    except AdoptionError as exc:
        if exc.code == "BASE_UNKNOWN":
            raise
        raise AdoptionError("BASE_UNKNOWN", f"无法读取 B 快照 {ref}: {exc.message}") from exc
    if base.commit.lower() != str(source.get("commit", "")).lower():
        raise AdoptionError(
            "BASE_UNKNOWN",
            f"B 快照 Commit {base.commit} 与 upstream.yaml 记录的 {source.get('commit')} 不一致",
        )
    recorded_manifest = str(snapshot.get("manifest_sha256") or "")
    if recorded_manifest and recorded_manifest != base.manifest_sha256:
        raise AdoptionError(
            "BASE_UNKNOWN",
            "B 快照 manifest SHA256 与 upstream.yaml 记录不一致",
        )
    return base


# ---------------------------------------------------------------------------
# Binary preparation and real Gate verification
# ---------------------------------------------------------------------------


@dataclasses.dataclass
class Binary:
    path: Path
    sha256: str
    provenance: str


def prepare_binary(
    dist: Distribution,
    workdir: Path,
    explicit: Path | None = None,
    explicit_sha256: str | None = None,
    explicit_commit: str | None = None,
) -> Binary:
    workdir.mkdir(parents=True, exist_ok=True)
    out = workdir / "workflow-check"
    if explicit is not None:
        explicit = explicit.resolve()
        if not explicit_sha256 or not re.fullmatch(r"[0-9a-fA-F]{64}", explicit_sha256):
            raise AdoptionError(
                "BINARY_UNPINNED",
                "外部 workflow-check 必须同时提供 --workflow-check-sha256",
            )
        if not explicit_commit or not FULL_SHA.match(explicit_commit):
            raise AdoptionError(
                "BINARY_COMMIT_MISSING",
                "外部 workflow-check 必须同时提供完整 --workflow-check-commit",
            )
        if explicit_commit.lower() != dist.commit.lower():
            raise AdoptionError(
                "BINARY_COMMIT_MISMATCH",
                f"外部 binary Commit {explicit_commit} 与分发包 Commit {dist.commit} 不一致",
            )
        if not explicit.is_file() or explicit.is_symlink():
            raise AdoptionError("BINARY_NOT_FOUND", f"workflow-check 不存在: {explicit}")
        actual = sha256_file(explicit)
        if actual.lower() != explicit_sha256.lower():
            raise AdoptionError(
                "BINARY_SHA_MISMATCH",
                f"workflow-check SHA256 不匹配: 实际 {actual}, 期望 {explicit_sha256}",
            )
        shutil.copyfile(explicit, out)
        os.chmod(out, 0o755)
        return Binary(path=out, sha256=actual, provenance="external-verified")

    engine = dist.root / ".agent/engine"
    go_mod = engine / "go.mod"
    if not go_mod.is_file():
        raise AdoptionError("ENGINE_MISSING", "分发包缺少 .agent/engine/go.mod，无法构建 workflow-check")
    module = ""
    for line in go_mod.read_text(encoding="utf-8").splitlines():
        if line.startswith("module "):
            module = line.split(None, 1)[1].strip()
            break
    if module != "workflow-v2-engine":
        raise AdoptionError("ENGINE_MODULE", f"Engine module 非预期: {module!r}")
    env = os.environ.copy()
    env["GOWORK"] = "off"
    env.setdefault("CGO_ENABLED", "0")
    # 从分发包内 Engine 构建时注入分发包自身的版本与 Commit：
    # 否则宿主的 `.agent/bin/workflow-check version` 只能输出 dev/unknown，
    # 已安装的 binary 无法自证来自哪个上游版本。
    # buildSource=source 表示“由源码构建”，与发布资产 buildSource=release 区分。
    ldflags = (
        f"-X main.version={dist.framework_version} "
        f"-X main.commit={dist.commit} "
        "-X main.buildSource=source"
    )
    proc = subprocess.run(
        [
            "go",
            "build",
            "-trimpath",
            "-buildvcs=false",
            "-ldflags",
            ldflags,
            "-o",
            str(out),
            "./cmd/workflow-check",
        ],
        cwd=str(engine),
        capture_output=True,
        text=True,
        env=env,
        timeout=300,
    )
    if proc.returncode != 0:
        raise AdoptionError(
            "BINARY_BUILD_FAILED",
            "从分发包 Engine 构建 workflow-check 失败",
            details={"stdout": proc.stdout, "stderr": proc.stderr},
        )
    os.chmod(out, 0o755)
    digest = sha256_file(out)
    return Binary(path=out, sha256=digest, provenance="local-build")


GATE_FIXTURE_STATE = """schema_version: 3
task_id: demo
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


def verify_gate_with_binary(binary: Path, config_text: str, integration_branch: str) -> dict[str, Any]:
    """Run a real `lint` Gate (coder-start) in a throwaway git repository."""
    with tempfile.TemporaryDirectory(prefix="workflow-adoption-gate-") as raw:
        root = Path(raw)
        (root / ".agent/tasks/demo").mkdir(parents=True)
        (root / ".agent/workflow.yaml").write_text(config_text, encoding="utf-8")
        (root / ".agent/tasks/demo/state.yaml").write_text(GATE_FIXTURE_STATE, encoding="utf-8")
        git(root, "init", "-q")
        git(root, "checkout", "-q", "-b", "feature/verify")
        git(root, "config", "user.name", "workflow-adoption")
        git(root, "config", "user.email", "workflow-adoption@example.invalid")
        git(root, "config", "commit.gpgsign", "false")
        git(root, "add", "-A")
        git(root, "commit", "-q", "-m", "gate fixture")
        head = git(root, "rev-parse", "HEAD")
        git(root, "update-ref", "refs/remotes/origin/feature/verify", head)
        git(root, "update-ref", f"refs/remotes/origin/{integration_branch}", head)
        proc = subprocess.run(
            [
                str(binary.resolve()),
                "gate",
                "coder-start",
                ".agent/tasks/demo",
                "--root",
                str(root),
            ],
            capture_output=True,
            text=True,
            timeout=60,
        )
        output = (proc.stdout + proc.stderr).strip()
        return {
            "gate": "coder-start",
            "exit_code": proc.returncode,
            "status": "PASS" if proc.returncode == 0 and "PASS" in proc.stdout else "FAIL",
            "output": output,
        }


# ---------------------------------------------------------------------------
# Configuration template and resource opt-in
# ---------------------------------------------------------------------------


def render_config(dist: Distribution, integration_branch: str, resources: dict[str, str]) -> str:
    template_path = ".agent/templates/workflow.yaml.template"
    try:
        text = dist.read(template_path).decode("utf-8")
    except AdoptionError:
        text = (
            "schema_version: 1\n\n"
            "git:\n"
            "  integration_branch: <ADOPTION_REQUIRED>\n\n"
            "resources: {}\n"
        )
    if "<ADOPTION_REQUIRED>" not in text:
        raise AdoptionError("CONFIG_TEMPLATE", "workflow.yaml 模板缺少 integration_branch 占位符")
    text = text.replace("<ADOPTION_REQUIRED>", yaml_quote(integration_branch))
    if resources:
        lines = ["resources:"]
        for kind in sorted(resources):
            lines.append(f"  {kind}:")
            lines.append(f"    registry: {yaml_quote(resources[kind])}")
        if "resources: {}" not in text:
            raise AdoptionError("CONFIG_TEMPLATE", "workflow.yaml 模板缺少 resources: {} 占位符")
        text = text.replace("resources: {}", "\n".join(lines))
    parsed = parse_simple_yaml(text)
    if parsed.get("schema_version") != CONFIG_SCHEMA:
        raise AdoptionError("CONFIG_TEMPLATE", "生成的 workflow.yaml 无法解析为 schema 1")
    return text


def parse_resource_specs(specs: Iterable[str]) -> dict[str, str]:
    resources: dict[str, str] = {}
    for spec in specs:
        kind, sep, path = spec.partition("=")
        if not sep or not kind or not path:
            raise AdoptionError("RESOURCE_SPEC", f"--resource 必须形如 KIND=PATH: {spec!r}")
        if not re.fullmatch(r"^[a-z][a-z0-9_]*$", kind):
            raise AdoptionError("RESOURCE_KIND", f"非法 resource kind: {kind!r}")
        if not is_safe_rel_path(path) or not path.startswith(".agent/registry/"):
            raise AdoptionError(
                "RESOURCE_PATH",
                f"registry 路径必须严格位于 .agent/registry/ 下: {path!r}",
            )
        if kind in resources:
            raise AdoptionError("RESOURCE_DUPLICATE", f"resource kind 重复: {kind}")
        resources[kind] = path
    return resources


def render_registry(dist: Distribution, kind: str) -> str:
    template_path = ".agent/templates/registry/registry.template.md"
    try:
        text = dist.read(template_path).decode("utf-8")
    except AdoptionError:
        text = (
            f"# {kind} Registry\n\n"
            "| 值 | 拥有方（任务） | 状态 | 备注 |\n"
            "| --- | --- | --- | --- |\n"
        )
    text = text.replace("<RESOURCE_KIND>", kind).replace("<kind>", kind)
    return text


# ---------------------------------------------------------------------------
# Legacy Evidence (F-03) scan
# ---------------------------------------------------------------------------

EVIDENCE_LINE = re.compile(
    r"^\s*(target|review_target|feature_head|develop_base)\s*:\s*(.*?)\s*$"
)


def resolve_evidence_commit(target: Path, literal: str) -> str | None:
    """按 Engine E-2 语义把十六进制字面值解析为唯一真实 Commit。

    与 `.agent/engine` 的 `resolveImmutableCommit` 对齐：字面值必须存在、能解析为
    Commit、且是解析结果的前缀。无法满足任一条（不存在 / 有歧义 / 指向非 Commit
    对象 / 十六进制样式的分支名）时返回 None，由调用方 fail closed。
    """
    try:
        git(target, "cat-file", "-e", f"{literal}^{{commit}}")
        full = git(target, "rev-parse", "--verify", f"{literal}^{{commit}}")
    except AdoptionError:
        return None
    if not FULL_SHA.match(full):
        return None
    if not full.lower().startswith(literal.lower()):
        return None
    return full


def scan_legacy_evidence(target: Path) -> tuple[list[dict[str, str]], list[str]]:
    tasks_root = target / ".agent/tasks"
    findings: list[dict[str, str]] = []
    reverify: set[str] = set()
    if not tasks_root.is_dir():
        return findings, []
    for state_path in sorted(tasks_root.glob("*/state.yaml")):
        task = state_path.parent.name
        try:
            lines = state_path.read_text(encoding="utf-8").splitlines()
        except OSError as exc:
            findings.append(
                {
                    "task": task,
                    "field": "<state.yaml>",
                    "value": "",
                    "class": "unreadable",
                    "action": "manual review required",
                    "reason": str(exc),
                }
            )
            reverify.add(task)
            continue
        for line in lines:
            m = EVIDENCE_LINE.match(line)
            if not m:
                continue
            field, raw = m.group(1), m.group(2)
            value = raw.strip()
            if value.startswith(('"', "'")):
                value = value[1:].split(value[0], 1)[0]
            value = value.split(" #", 1)[0].strip()
            if not value:
                continue
            if FULL_SHA.match(value):
                if resolve_evidence_commit(target, value):
                    continue
                findings.append(
                    {
                        "task": task,
                        "field": field,
                        "value": value,
                        "class": "invalid_commit_sha",
                        "action": "keep original evidence; task requires re-verification through role/Owner flow",
                        "reason": "完整 40 位十六进制字面值在本仓库中不是真实 Commit",
                    }
                )
                reverify.add(task)
                continue
            if SHORT_SHA.match(value):
                if resolve_evidence_commit(target, value):
                    findings.append(
                        {
                            "task": task,
                            "field": field,
                            "value": value,
                            "class": "legacy_short_sha",
                            "action": "accepted by Engine; write full 40-hex SHA on next valid evidence write",
                            "reason": "历史短 SHA 可解析为唯一真实 Commit，但不得作为新证据写入形态",
                        }
                    )
                    continue
                findings.append(
                    {
                        "task": task,
                        "field": field,
                        "value": value,
                        "class": "invalid_commit_sha",
                        "action": "keep original evidence; task requires re-verification through role/Owner flow",
                        "reason": "短 SHA 无法唯一解析为真实 Commit（不存在 / 有歧义 / 非 Commit 对象）",
                    }
                )
                reverify.add(task)
                continue
            findings.append(
                {
                    "task": task,
                    "field": field,
                    "value": value,
                    "class": "invalid_immutable_ref",
                    "action": "keep original evidence; task requires re-verification through role/Owner flow",
                    "reason": "HEAD / 分支 / 可移动 Tag / origin/* 等不是不可变 Commit SHA",
                }
            )
            reverify.add(task)
    return findings, sorted(reverify)


# ---------------------------------------------------------------------------
# Transaction, backups and restore
# ---------------------------------------------------------------------------


@dataclasses.dataclass
class Change:
    action: str  # create | update | delete
    path: str
    data: bytes | None = None
    mode: int = 0o644
    reason: str = ""


def _ensure_parent_dirs(root: Path, dest: Path) -> list[Path]:
    missing: list[Path] = []
    cur = dest.parent
    while cur != root and not cur.exists():
        missing.append(cur)
        cur = cur.parent
    for directory in reversed(missing):
        if directory.is_symlink():
            raise AdoptionError("SYMLINK_PATH", f"父目录是符号链接: {directory}")
        directory.mkdir()
    return missing


def write_atomic(dest: Path, data: bytes, mode: int) -> None:
    if dest.is_symlink():
        raise AdoptionError("SYMLINK_PATH", f"拒绝写入符号链接: {dest}")
    if dest.exists() and dest.is_dir():
        raise AdoptionError("PATH_IS_DIR", f"目标是目录，不能写文件: {dest}")
    tmp = dest.with_name(f".{dest.name}.adoption-tmp-{os.getpid()}-{uuid.uuid4().hex[:6]}")
    try:
        with tmp.open("xb") as fh:
            fh.write(data)
            fh.flush()
            os.fsync(fh.fileno())
        os.chmod(tmp, mode)
        os.replace(tmp, dest)
    finally:
        if tmp.exists():
            tmp.unlink(missing_ok=True)


class Transaction:
    def __init__(self, target: Path, command: str) -> None:
        self.target = target
        self.command = command
        self.id = time.strftime("%Y%m%dT%H%M%SZ", time.gmtime()) + "-" + uuid.uuid4().hex[:8]
        self.root = git_path(target, "workflow-adoption")
        self.backup_root = self.root / "backups" / self.id
        self.entries: list[dict[str, Any]] = []
        self.created_dirs: list[Path] = []
        self.applied: list[Change] = []
        self.backed_up = False

    def backup(self, changes: list[Change]) -> None:
        self.backup_root.mkdir(parents=True, exist_ok=False)
        seen: set[str] = set()
        for change in changes:
            if change.path in seen:
                continue
            seen.add(change.path)
            state, data, mode = inspect_host_path(self.target, change.path)
            if state == "symlink":
                raise AdoptionError("SYMLINK_PATH", f"宿主路径是符号链接，拒绝处理: {change.path}")
            if state == "dir":
                raise AdoptionError("PATH_IS_DIR", f"宿主路径是目录，拒绝处理: {change.path}")
            entry: dict[str, Any] = {
                "path": change.path,
                "existed": state == "file",
                "mode": mode if state == "file" else 0,
                "sha256": sha256_bytes(data) if data is not None else "",
                "backup": "",
            }
            if state == "file" and data is not None:
                rel = Path("before") / change.path
                dest = self.backup_root / rel
                dest.parent.mkdir(parents=True, exist_ok=True)
                dest.write_bytes(data)
                entry["backup"] = rel.as_posix()
            self.entries.append(entry)
        manifest = {
            "format": 1,
            "transaction_id": self.id,
            "command": self.command,
            "target": str(self.target),
            "created_at": now_iso(),
            "status": "in_progress",
            "entries": self.entries,
        }
        (self.backup_root / "manifest.json").write_bytes(canonical_manifest_bytes(manifest))
        self.backed_up = True

    def apply(self, changes: list[Change]) -> None:
        fail_after_raw = os.environ.get("WORKFLOW_ADOPTION_TEST_FAIL_AFTER_WRITE", "")
        fail_after = int(fail_after_raw) if fail_after_raw.isdigit() else 0
        for change in changes:
            dest = self.target.joinpath(*PurePosixPath(change.path).parts)
            if change.action in ("create", "update"):
                if change.data is None:
                    raise AdoptionError("CHANGE_DATA", f"{change.action} 缺少内容: {change.path}")
                self.created_dirs.extend(_ensure_parent_dirs(self.target, dest))
                write_atomic(dest, change.data, change.mode)
            elif change.action == "delete":
                if dest.is_symlink():
                    raise AdoptionError("SYMLINK_PATH", f"拒绝删除符号链接: {change.path}")
                if dest.exists():
                    if dest.is_dir():
                        raise AdoptionError("PATH_IS_DIR", f"拒绝删除目录: {change.path}")
                    dest.unlink()
            else:
                raise AdoptionError("CHANGE_ACTION", f"未知 change action: {change.action}")
            self.applied.append(change)
            if fail_after and len(self.applied) >= fail_after:
                raise RuntimeError("injected failure after write (test hook)")

    def rollback(self) -> dict[str, Any]:
        errors: list[str] = []
        for entry in reversed(self.entries):
            path = entry["path"]
            dest = self.target.joinpath(*PurePosixPath(path).parts)
            try:
                if entry["existed"]:
                    backup_file = self.backup_root / entry["backup"]
                    data = backup_file.read_bytes()
                    if sha256_bytes(data) != entry["sha256"]:
                        raise AdoptionError("BACKUP_CORRUPT", f"备份内容校验失败: {path}")
                    self.created_dirs.extend(_ensure_parent_dirs(self.target, dest))
                    write_atomic(dest, data, int(entry["mode"]) or 0o644)
                else:
                    if dest.is_symlink():
                        raise AdoptionError("SYMLINK_PATH", f"回滚遇到符号链接: {path}")
                    if dest.exists() and dest.is_file():
                        dest.unlink()
            except Exception as exc:  # noqa: BLE001 - report every rollback failure
                errors.append(f"{path}: {exc}")
        for directory in sorted(set(self.created_dirs), key=lambda p: len(p.parts), reverse=True):
            try:
                if directory.exists() and directory.is_dir() and not any(directory.iterdir()):
                    directory.rmdir()
            except OSError:
                pass
        verification: list[str] = []
        for entry in self.entries:
            state, data, _ = inspect_host_path(self.target, entry["path"])
            if entry["existed"]:
                if state != "file" or sha256_bytes(data or b"") != entry["sha256"]:
                    verification.append(entry["path"])
            else:
                if state == "file":
                    verification.append(entry["path"])
        return {"ok": not errors and not verification, "errors": errors, "mismatched": verification}

    def finalize(self, status: str) -> None:
        manifest_path = self.backup_root / "manifest.json"
        if not manifest_path.is_file():
            return
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        manifest["status"] = status
        manifest["finished_at"] = now_iso()
        write_atomic(manifest_path, canonical_manifest_bytes(manifest), 0o600)


@contextmanager
def adoption_lock(target: Path, command: str):
    lock = git_path(target, "workflow-adoption") / f"{command}.lock"
    lock.parent.mkdir(parents=True, exist_ok=True)
    try:
        fd = os.open(lock, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError as exc:
        raise AdoptionError(
            "LOCKED",
            f"另一个 adoption 事务正在运行（{lock}）；确认无进程后可手工删除该锁",
        ) from exc
    try:
        os.write(fd, f"pid={os.getpid()}\nstarted={now_iso()}\n".encode())
        os.close(fd)
        yield lock
    finally:
        try:
            lock.unlink(missing_ok=True)
        except OSError:
            pass


# ---------------------------------------------------------------------------
# Reports
# ---------------------------------------------------------------------------


def report_root(target: Path) -> Path:
    return git_path(target, "workflow-adoption") / "reports"


def write_report(target: Path, report: dict[str, Any]) -> Path:
    root = report_root(target)
    root.mkdir(parents=True, exist_ok=True)
    path = root / f"{report.get('transaction_id', uuid.uuid4().hex[:12])}.json"
    write_atomic(path, canonical_manifest_bytes(report), 0o600)
    report["report_path"] = str(path)
    return path


def base_report(command: str, target: Path | None = None) -> dict[str, Any]:
    return {
        "format": 1,
        "command": command,
        "status": "PLAN",
        "transaction_id": time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())
        + "-"
        + uuid.uuid4().hex[:8],
        "created_at": now_iso(),
        "target": str(target) if target else "",
        "owner_authorized": False,
        "changes": [],
        "conflicts": [],
        "deletes_planned": [],
        "legacy_evidence": [],
        "task_reverification_required": [],
        "verification": {},
    }


def emit(report: dict[str, Any], as_json: bool, exit_code: int) -> int:
    report["exit_code"] = exit_code
    if as_json:
        print(json.dumps(report, indent=2, sort_keys=True, ensure_ascii=False))
    else:
        print(f"STATUS: {report.get('status')}")
        for key in (
            "source_commit",
            "framework_version",
            "distribution_sha256",
            "integration_branch",
            "backup_id",
            "report_path",
        ):
            if report.get(key):
                print(f"{key}: {report[key]}")
        for item in report.get("changes", []):
            print(f"  {item['action']:8} {item['path']}")
        for item in report.get("conflicts", []):
            print(f"  CONFLICT {item['path']}: {item['decision']}")
        for task in report.get("task_reverification_required", []):
            print(f"  TASK_REVERIFICATION_REQUIRED {task}")
        verification = report.get("verification") or {}
        for key, value in verification.items():
            print(f"verify.{key}: {value}")
        if report.get("error"):
            print(f"ERROR: {report['error']}")
    return exit_code


# ---------------------------------------------------------------------------
# Install / upgrade planning helpers
# ---------------------------------------------------------------------------


def _resource_changes(
    target: Path, dist: Distribution, resources: dict[str, str]
) -> tuple[list[Change], list[str], dict[str, str]]:
    changes: list[Change] = []
    kept: list[str] = []
    expected: dict[str, str] = {}
    for kind, rel in sorted(resources.items()):
        state, data, _ = inspect_host_path(target, rel)
        if state in ("symlink", "dir", "notdir", "special"):
            raise AdoptionError("REGISTRY_PATH", f"Registry 路径不可用: {rel}")
        if state == "file":
            kept.append(rel)
            expected[rel] = sha256_bytes(data or b"")
            continue
        body = render_registry(dist, kind).encode("utf-8")
        changes.append(
            Change(
                action="create",
                path=rel,
                data=body,
                mode=0o644,
                reason=f"opt-in empty registry for {kind}",
            )
        )
        expected[rel] = sha256_bytes(body)
    return changes, kept, expected


def plan_install(
    target: Path,
    dist: Distribution,
    integration_branch: str,
    resources: dict[str, str],
    binary: Binary,
) -> dict[str, Any]:
    conflicts: list[dict[str, Any]] = []
    changes: list[Change] = []
    keep_identical: list[str] = []

    if upstream_path(target).exists():
        raise AdoptionError(
            "ALREADY_INSTALLED",
            "目标已有 .agent/upstream.yaml；这是升级场景，请使用 upgrade",
        )
    if config_path(target).exists():
        raise AdoptionError(
            "WORKFLOW_CONFIG_EXISTS",
            "目标已有 .agent/workflow.yaml；拒绝覆盖宿主配置，请先确认既有安装来源",
        )

    for path, f in sorted(dist.managed.items()):
        state, data, _ = inspect_host_path(target, path)
        if state in ("symlink", "dir", "notdir", "special"):
            conflicts.append(
                {
                    "path": path,
                    "decision": "EXISTING_NON_FILE",
                    "detail": f"host path state={state}",
                }
            )
            continue
        if state == "file":
            if sha256_bytes(data or b"") == f.sha256:
                keep_identical.append(path)
                continue
            conflicts.append(
                {
                    "path": path,
                    "decision": "EXISTING_FILE_DIFFERS",
                    "detail": "framework-managed path already exists with different content; provenance unknown",
                }
            )
            continue
        changes.append(Change(action="create", path=path, data=f.data, mode=f.mode))

    registry_changes, kept_registries, registry_expected = _resource_changes(
        target, dist, resources
    )
    changes.extend(registry_changes)

    binary_state, binary_data, _ = inspect_host_path(target, ".agent/bin/workflow-check")
    if binary_state in ("symlink", "dir", "notdir", "special"):
        conflicts.append(
            {
                "path": ".agent/bin/workflow-check",
                "decision": "EXISTING_NON_FILE",
                "detail": f"host path state={binary_state}",
            }
        )
    elif binary_state == "file" and sha256_bytes(binary_data or b"") != binary.sha256:
        conflicts.append(
            {
                "path": ".agent/bin/workflow-check",
                "decision": "EXISTING_BINARY_UNVERIFIED",
                "detail": "existing binary differs from verified build; refusing to overwrite on first install",
            }
        )

    config_text = render_config(dist, integration_branch, resources)
    changes.append(
        Change(
            action="create",
            path=".agent/workflow.yaml",
            data=config_text.encode("utf-8"),
            mode=0o644,
            reason="generated from Owner-confirmed adoption values",
        )
    )
    changes.append(
        Change(
            action="create",
            path=".agent/bin/workflow-check",
            data=binary.path.read_bytes(),
            mode=0o755,
            reason=f"workflow-check ({binary.provenance})",
        )
    )

    return {
        "changes": changes,
        "conflicts": conflicts,
        "keep_identical": keep_identical,
        "kept_registries": kept_registries,
        "registry_expected": registry_expected,
        "config_text": config_text,
        "resources": resources,
    }


def _decision(
    base_hash: str | None,
    local_hash: str | None,
    new_hash: str | None,
    base_present: bool,
    local_present: bool,
    new_present: bool,
) -> tuple[str, str]:
    """Return (decision, action).  Decision is human/machine stable."""
    if local_present == new_present and local_hash == new_hash:
        return "KEEP_IDENTICAL", "none"
    if local_present == base_present and local_hash == base_hash:
        if new_present:
            return (
                "UPDATE" if local_present else "CREATE",
                "update" if local_present else "create",
            )
        if base_present:
            return "DELETE_PLANNED", "delete"
        return "KEEP_IDENTICAL", "none"
    if base_present == new_present and base_hash == new_hash:
        return "KEEP_LOCAL", "none"
    if not new_present:
        if local_hash == base_hash:
            return "DELETE_PLANNED", "delete"
        return "CONFLICT_UPSTREAM_DELETE_LOCAL_MODIFIED", "none"
    if not base_present and not local_present:
        return "CREATE", "create"
    if not base_present and local_present:
        return "CONFLICT_LOCAL_ONLY_FRAMEWORK_PATH", "none"
    if base_present and not local_present:
        return "CONFLICT_LOCAL_DELETE_UPSTREAM_CHANGE", "none"
    return "CONFLICT_ALL_DIFFER", "none"


def plan_upgrade(
    target: Path,
    dist: Distribution,
    base: Snapshot,
    config_text: str,
    integration_branch: str,
    allow_deletes: bool,
) -> dict[str, Any]:
    decisions: list[dict[str, Any]] = []
    changes: list[Change] = []
    conflicts: list[dict[str, Any]] = []
    deletes: list[str] = []
    keep_local: list[str] = []
    keep_identical: list[str] = []

    paths = sorted(set(base.files) | set(dist.managed))
    for path in paths:
        if path in NEVER_PACKAGE_FILES or any(path.startswith(p) for p in NEVER_PACKAGE_PREFIXES):
            raise AdoptionError("UPGRADE_PROTECTED_PATH", f"managed 清单包含受保护路径: {path}")
        b = base.files.get(path)
        n_file = dist.managed.get(path)
        n = n_file.sha256 if n_file else None
        state, data, _ = inspect_host_path(target, path)
        if state == "symlink" or state == "dir" or state == "notdir" or state == "special":
            conflicts.append(
                {
                    "path": path,
                    "decision": f"CONFLICT_HOST_PATH_{state.upper()}",
                    "detail": "framework-managed path is not a regular file; refusing to touch it",
                }
            )
            decisions.append(
                {
                    "path": path,
                    "base": short(b),
                    "local": f"<{state}>",
                    "new": short(n),
                    "base_sha256": b or "",
                    "local_sha256": "",
                    "new_sha256": n or "",
                    "decision": f"CONFLICT_HOST_PATH_{state.upper()}",
                }
            )
            continue
        l = sha256_bytes(data) if state == "file" else None
        decision, action = _decision(
            b, l, n, b is not None, state == "file", n_file is not None
        )
        decisions.append(
            {
                "path": path,
                "base": short(b),
                "local": short(l),
                "new": short(n),
                "base_sha256": b or "",
                "local_sha256": l or "",
                "new_sha256": n or "",
                "decision": decision,
            }
        )
        if decision.startswith("CONFLICT"):
            conflicts.append({"path": path, "decision": decision, "detail": "local content preserved"})
            continue
        if decision == "KEEP_LOCAL":
            keep_local.append(path)
            continue
        if decision == "KEEP_IDENTICAL":
            keep_identical.append(path)
            continue
        if decision == "DELETE_PLANNED":
            deletes.append(path)
            if not allow_deletes:
                continue
            changes.append(Change(action="delete", path=path, reason="upstream removal with L=B"))
            continue
        if decision in ("UPDATE", "CREATE"):
            assert n_file is not None
            changes.append(
                Change(
                    action="update" if decision == "UPDATE" else "create",
                    path=path,
                    data=n_file.data,
                    mode=n_file.mode,
                )
            )
            continue
        raise AdoptionError("UPGRADE_DECISION", f"未处理的决策 {decision} for {path}")

    return {
        "changes": changes,
        "conflicts": conflicts,
        "deletes_planned": deletes,
        "keep_local": keep_local,
        "keep_identical": keep_identical,
        "decisions": decisions,
        "config_text": config_text,
        "integration_branch": integration_branch,
    }


def _upstream_data(
    *,
    dist: Distribution,
    action: str,
    owner: str,
    branch: str,
    integration_branch: str,
    previous_commit: str,
    snapshot: Snapshot,
    binary: Binary,
    last_upgrade: dict[str, Any] | None,
) -> dict[str, Any]:
    return {
        "schema_version": UPSTREAM_SCHEMA,
        "source": {
            "repository": dist.repository,
            "kind": "source-archive" if dist.package_sha256 and dist.distribution_name.endswith(".tar.gz") else "source-tree",
            "name": dist.distribution_name,
            "sha256": dist.package_sha256,
            "framework_version": dist.framework_version,
            "commit": dist.commit,
            "release": "",
        },
        "installed": {
            "action": action,
            "at": now_iso(),
            "owner": owner,
            "branch": branch,
            "integration_branch": integration_branch,
            "previous_commit": previous_commit,
        },
        "snapshot": {
            "ref": f"snapshots/{dist.commit}",
            "manifest_sha256": snapshot.manifest_sha256,
            "file_count": len(snapshot.files),
        },
        "runtime": {
            "workflow_check_sha256": binary.sha256,
            "provenance": binary.provenance,
        },
        "last_upgrade": last_upgrade,
    }


def _verify_expected_files(target: Path, expected: dict[str, str]) -> list[str]:
    mismatches: list[str] = []
    for path, sha in sorted(expected.items()):
        state, data, _ = inspect_host_path(target, path)
        if sha is None:
            if state == "file":
                mismatches.append(f"{path}: expected absent, found file")
        elif state != "file" or sha256_bytes(data or b"") != sha:
            mismatches.append(f"{path}: hash mismatch")
    return mismatches





# ---------------------------------------------------------------------------
# Commands
# ---------------------------------------------------------------------------


def cmd_dist(args: argparse.Namespace) -> int:
    result = create_source_distribution(Path(args.source), Path(args.output), args.repository)
    report = {
        "format": 1,
        "command": "dist",
        "status": result["status"],
        "source_commit": result["commit"],
        "commit": result["commit"],
        "framework_version": result["framework_version"],
        "distribution_sha256": result["sha256"],
        "sha256": result["sha256"],
        "archive": result["archive"],
        "checksum_file": result["checksum_file"],
        "file_count": result["file_count"],
        "manifest": result["manifest"],
    }
    if args.json:
        print(json.dumps(report, indent=2, sort_keys=True, ensure_ascii=False))
    else:
        print(f"STATUS: {report['status']}")
        print(f"source_commit: {result['commit']}")
        print(f"framework_version: {result['framework_version']}")
        print(f"archive: {result['archive']}")
        print(f"sha256: {result['sha256']}")
        print(f"file_count: {result['file_count']}")
    return EXIT_OK


def cmd_inspect(args: argparse.Namespace) -> int:
    dist = load_distribution(
        package=Path(args.package),
        expected_sha256=args.expected_sha256,
        require_pinned=False,
    )
    try:
        summary = {
            "status": "INSPECT_OK",
            "commit": dist.commit,
            "framework_version": dist.framework_version,
            "repository": dist.repository,
            "distribution_sha256": dist.package_sha256,
            "managed_files": len(dist.managed),
            "package_only_files": len(dist.package_only),
            "compatibility": dist.manifest.get("compatibility"),
            "release": dist.manifest.get("release"),
            "managed": sorted(dist.managed),
            "package_only": sorted(dist.package_only),
        }
        if args.json:
            print(json.dumps(summary, indent=2, sort_keys=True, ensure_ascii=False))
        else:
            print(f"STATUS: {summary['status']}")
            print(f"commit: {dist.commit}")
            print(f"framework_version: {dist.framework_version}")
            print(f"managed_files: {len(dist.managed)}")
            print(f"package_only_files: {len(dist.package_only)}")
        return EXIT_OK
    finally:
        dist.cleanup()


def _load_for_install(args: argparse.Namespace) -> Distribution:
    return load_distribution(
        package=Path(args.package) if args.package else None,
        source=Path(args.source) if args.source else None,
        expected_commit=args.source_commit,
        expected_sha256=args.distribution_sha256,
        require_pinned=True,
    )


def _prepare_common(args: argparse.Namespace, report: dict[str, Any]) -> Path:
    target = require_git_root(Path(args.target))
    report["target"] = str(target)
    if not args.owner:
        raise AdoptionError("OWNER_REQUIRED", "必须提供 --owner 记录授权主体")
    if not args.owner_authorized:
        raise AdoptionError(
            "OWNER_AUTHORIZATION_REQUIRED",
            "必须提供 --owner-authorized，确认 Owner 已授权安装/升级",
            exit_code=EXIT_AUTH_REQUIRED,
        )
    report["owner"] = args.owner
    report["owner_authorized"] = True
    report["branch"] = current_branch(target)
    if tracked_dirty(target):
        raise AdoptionError(
            "TARGET_DIRTY",
            "目标仓库存在未提交的 tracked 变更；请先提交或 stash，避免升级混入未知本地状态",
        )
    return target


def cmd_install(args: argparse.Namespace) -> int:
    report = base_report("install", Path(args.target))
    dist: Distribution | None = None
    binary_dir: tempfile.TemporaryDirectory[str] | None = None
    tx: Transaction | None = None
    snapshot: Snapshot | None = None
    try:
        target = _prepare_common(args, report)
        validate_branch_name(target, args.integration_branch)
        if not ref_exists(target, f"refs/remotes/origin/{args.integration_branch}"):
            raise AdoptionError(
                "INTEGRATION_BRANCH_UNKNOWN",
                f"目标仓库不存在 origin/{args.integration_branch}；Owner 指定的集成分支必须真实存在",
            )
        resources = parse_resource_specs(args.resource or [])
        dist = _load_for_install(args)
        report.update(
            {
                "source_commit": dist.commit,
                "framework_version": dist.framework_version,
                "distribution_sha256": dist.package_sha256,
                "distribution": dist.distribution_name,
                "integration_branch": args.integration_branch,
                "source_repository": dist.repository,
            }
        )
        binary_dir = tempfile.TemporaryDirectory(prefix="workflow-adoption-binary-")
        binary = prepare_binary(
            dist,
            Path(binary_dir.name),
            explicit=Path(args.workflow_check) if args.workflow_check else None,
            explicit_sha256=args.workflow_check_sha256,
            explicit_commit=args.workflow_check_commit,
        )
        report["runtime"] = {"provenance": binary.provenance, "sha256": binary.sha256}
        plan = plan_install(target, dist, args.integration_branch, resources, binary)
        report["changes"] = [
            {"action": c.action, "path": c.path, "reason": c.reason} for c in plan["changes"]
        ]
        report["conflicts"] = plan["conflicts"]
        report["keep_identical"] = plan["keep_identical"]
        report["resources"] = resources
        report["kept_registries"] = plan["kept_registries"]
        report["protected_before"] = inventory_protected(target)

        gate = verify_gate_with_binary(binary.path, plan["config_text"], args.integration_branch)
        report["verification"]["preflight_gate"] = gate["status"]
        if gate["status"] != "PASS":
            raise AdoptionError(
                "GATE_PRECHECK_FAILED",
                f"新配置在真实 Gate fixture 上失败: {gate['output']}",
                details=gate,
            )
        if plan["conflicts"]:
            report["status"] = "CONFLICT"
            write_report(target, report)
            return emit(report, args.json, EXIT_CONFLICT)
        if not args.execute:
            report["status"] = "PLAN"
            report["would_write"] = len(plan["changes"]) + 1
            return emit(report, args.json, EXIT_OK)

        with adoption_lock(target, "install"):
            if upstream_path(target).exists() or config_path(target).exists():
                raise AdoptionError(
                    "TARGET_CHANGED",
                    "预检查后目标出现 workflow.yaml / upstream.yaml；拒绝继续",
                )
            snapshot = write_snapshot(target, dist)
            upstream = _upstream_data(
                dist=dist,
                action="install",
                owner=args.owner,
                branch=report["branch"],
                integration_branch=args.integration_branch,
                previous_commit="",
                snapshot=snapshot,
                binary=binary,
                last_upgrade=None,
            )
            upstream_change = Change(
                action="create",
                path=".agent/upstream.yaml",
                data=render_upstream(upstream).encode("utf-8"),
                mode=0o644,
                reason="provenance record",
            )
            changes = plan["changes"] + [upstream_change]
            tx = Transaction(target, "install")
            tx.backup(changes)
            tx.apply(changes)

            expected: dict[str, str | None] = {p: f.sha256 for p, f in dist.managed.items()}
            expected[".agent/workflow.yaml"] = sha256_bytes(plan["config_text"].encode("utf-8"))
            expected[".agent/bin/workflow-check"] = binary.sha256
            expected.update(plan["registry_expected"])
            mismatches = _verify_expected_files(target, expected)
            if mismatches:
                raise AdoptionError("POST_VERIFY_FILES", "安装后文件校验失败", details={"mismatches": mismatches})

            upstream_after = read_upstream(target)
            if upstream_after["source"]["commit"].lower() != dist.commit.lower():
                raise AdoptionError("POST_VERIFY_UPSTREAM", "upstream.yaml Commit 与分发包不一致")
            base_check = read_snapshot_at(target, upstream_after["snapshot"]["ref"])
            if base_check.files != {p: f.sha256 for p, f in dist.managed.items()}:
                raise AdoptionError("POST_VERIFY_SNAPSHOT", "安装快照与分发包 managed 集合不一致")

            gate_after = verify_gate_with_binary(
                target / ".agent/bin/workflow-check", plan["config_text"], args.integration_branch
            )
            if gate_after["status"] != "PASS":
                raise AdoptionError(
                    "POST_VERIFY_GATE",
                    f"安装后的 workflow-check 未通过真实 Gate: {gate_after['output']}",
                )

            protected_after = inventory_protected(target)
            diff = diff_inventory(report["protected_before"], protected_after)
            allowed_new = {".agent/workflow.yaml"} | set(plan["registry_expected"].keys())
            diff["added"] = [p for p in diff["added"] if p not in allowed_new]
            if diff["changed"] or diff["removed"] or diff["added"]:
                raise AdoptionError(
                    "PROTECTED_CHANGED",
                    "安装过程改变了宿主保护文件，已回滚",
                    details=diff,
                )
            report["protected_after"] = protected_after
            report["verification"].update(
                {
                    "files": "PASS",
                    "upstream": "PASS",
                    "snapshot": "PASS",
                    "gate": gate_after["status"],
                    "protected_unchanged": True,
                }
            )
            tx.finalize("success")
            report["backup_id"] = tx.id
            report["backup_path"] = str(tx.backup_root)
            report["status"] = "INSTALLED"
            write_report(target, report)
            return emit(report, args.json, EXIT_OK)
    except AdoptionError as exc:
        rollback: dict[str, Any] | None = None
        if tx is not None:
            rollback = tx.rollback()
            tx.finalize("rolled_back" if rollback.get("ok") else "rollback_failed")
            report["rollback"] = rollback
        if snapshot is not None and snapshot.created:
            shutil.rmtree(snapshot.root, ignore_errors=True)
        report["status"] = "CONFLICT" if exc.exit_code == EXIT_CONFLICT else "FAILED"
        report["error"] = exc.message
        report["error_code"] = exc.code
        report["error_details"] = exc.details
        try:
            if report.get("target") and Path(report["target"]).is_dir():
                write_report(Path(report["target"]), report)
        except Exception:  # noqa: BLE001
            pass
        return emit(report, args.json, exc.exit_code)
    except Exception as exc:  # noqa: BLE001
        rollback = None
        if tx is not None:
            rollback = tx.rollback()
            tx.finalize("rolled_back" if rollback.get("ok") else "rollback_failed")
            report["rollback"] = rollback
        if snapshot is not None and snapshot.created:
            shutil.rmtree(snapshot.root, ignore_errors=True)
        report["status"] = "FAILED"
        report["error"] = str(exc)
        report["error_code"] = "INTERNAL"
        try:
            if report.get("target") and Path(report["target"]).is_dir():
                write_report(Path(report["target"]), report)
        except Exception:  # noqa: BLE001
            pass
        return emit(report, args.json, EXIT_FAIL)
    finally:
        if dist is not None:
            dist.cleanup()
        if binary_dir is not None:
            binary_dir.cleanup()


def cmd_upgrade(args: argparse.Namespace) -> int:
    report = base_report("upgrade", Path(args.target))
    dist: Distribution | None = None
    binary_dir: tempfile.TemporaryDirectory[str] | None = None
    tx: Transaction | None = None
    snapshot_new: Snapshot | None = None
    try:
        target = _prepare_common(args, report)
        previous = read_upstream(target)
        previous_commit = previous["source"]["commit"]
        config_text, config_data, integration_branch = read_config(target)
        if not ref_exists(target, f"refs/remotes/origin/{integration_branch}"):
            raise AdoptionError(
                "INTEGRATION_BRANCH_UNKNOWN",
                f"目标仓库不存在 origin/{integration_branch}；保留配置引用的集成分支必须真实存在",
            )
        report.update(
            {
                "base_commit": previous_commit,
                "base_snapshot": previous["snapshot"].get("ref"),
                "integration_branch": integration_branch,
                "config_preserved": True,
            }
        )
        try:
            base = load_base_snapshot(target, previous)
        except AdoptionError as exc:
            if exc.code != "BASE_UNKNOWN":
                raise
            report["status"] = "BASE_UNKNOWN"
            report["error"] = exc.message
            report["error_code"] = "BASE_UNKNOWN"
            report["legacy_evidence"], report["task_reverification_required"] = scan_legacy_evidence(target)
            write_report(target, report)
            return emit(report, args.json, EXIT_BASE_UNKNOWN)

        dist = _load_for_install(args)
        report.update(
            {
                "source_commit": dist.commit,
                "framework_version": dist.framework_version,
                "distribution_sha256": dist.package_sha256,
                "distribution": dist.distribution_name,
                "source_repository": dist.repository,
            }
        )
        binary_dir = tempfile.TemporaryDirectory(prefix="workflow-adoption-binary-")
        binary = prepare_binary(
            dist,
            Path(binary_dir.name),
            explicit=Path(args.workflow_check) if args.workflow_check else None,
            explicit_sha256=args.workflow_check_sha256,
            explicit_commit=args.workflow_check_commit,
        )
        report["runtime"] = {"provenance": binary.provenance, "sha256": binary.sha256}

        gate_pre = verify_gate_with_binary(binary.path, config_text, integration_branch)
        report["verification"]["preflight_gate"] = gate_pre["status"]
        if gate_pre["status"] != "PASS":
            raise AdoptionError(
                "CONFIG_GATE_FAILED",
                f"保留的 .agent/workflow.yaml 在真实 Gate fixture 上失败: {gate_pre['output']}",
                details=gate_pre,
            )

        plan = plan_upgrade(
            target,
            dist,
            base,
            config_text,
            integration_branch,
            args.allow_deletes,
        )
        report["decisions"] = plan["decisions"]
        report["changes"] = [
            {"action": c.action, "path": c.path, "reason": c.reason} for c in plan["changes"]
        ]
        report["conflicts"] = plan["conflicts"]
        report["deletes_planned"] = plan["deletes_planned"]
        report["delete_authorized"] = bool(args.allow_deletes)
        report["keep_local"] = plan["keep_local"]
        report["keep_identical"] = plan["keep_identical"]
        report["legacy_evidence"], report["task_reverification_required"] = scan_legacy_evidence(target)
        report["protected_before"] = inventory_protected(target)

        if plan["conflicts"]:
            report["status"] = "CONFLICT"
            write_report(target, report)
            return emit(report, args.json, EXIT_CONFLICT)
        if plan["deletes_planned"] and not args.allow_deletes:
            report["status"] = "AUTHORIZATION_REQUIRED"
            report["error"] = "存在上游删除计划，必须显式提供 --allow-deletes"
            report["error_code"] = "DELETE_NOT_AUTHORIZED"
            write_report(target, report)
            return emit(report, args.json, EXIT_AUTH_REQUIRED)

        binary_needed = True
        current_binary_state, current_binary_data, _ = inspect_host_path(
            target, ".agent/bin/workflow-check"
        )
        current_binary_sha = (
            sha256_bytes(current_binary_data or b"") if current_binary_state == "file" else ""
        )
        if current_binary_state == "file" and current_binary_sha == binary.sha256:
            binary_needed = False
        # 如果 Engine 内容完全未变，且现存 binary 与上次记录一致，
        # 则保留现有 binary（避免仅因 Go build ID 不同而制造无意义的二进制变更）。
        engine_paths = [p for p in set(base.files) | set(dist.managed) if p.startswith(".agent/engine/")]
        engine_unchanged = all(
            base.files.get(p) == (dist.managed[p].sha256 if p in dist.managed else None)
            for p in engine_paths
        )
        recorded_binary = str((previous.get("runtime") or {}).get("workflow_check_sha256") or "")
        if (
            current_binary_state == "file"
            and engine_unchanged
            and recorded_binary
            and current_binary_sha == recorded_binary
        ):
            binary_needed = False
        effective_binary = binary
        if not binary_needed and current_binary_state == "file":
            effective_binary = Binary(
                path=target / ".agent/bin/workflow-check",
                sha256=current_binary_sha,
                provenance=str((previous.get("runtime") or {}).get("provenance") or "preserved"),
            )
        report["runtime"] = {
            "provenance": effective_binary.provenance,
            "sha256": effective_binary.sha256,
        }
        if current_binary_state in ("symlink", "dir", "notdir", "special"):
            report["status"] = "CONFLICT"
            report["conflicts"].append(
                {
                    "path": ".agent/bin/workflow-check",
                    "decision": "CONFLICT_HOST_PATH",
                    "detail": f"state={current_binary_state}",
                }
            )
            write_report(target, report)
            return emit(report, args.json, EXIT_CONFLICT)

        if dist.commit.lower() == previous_commit.lower() and not plan["changes"] and not binary_needed:
            report["status"] = "NO_CHANGES"
            report["verification"]["files"] = "PASS"
            report["verification"]["gate"] = "PASS"
            return emit(report, args.json, EXIT_OK)

        if not args.execute:
            report["status"] = "PLAN"
            report["would_write"] = len(plan["changes"]) + 1 + (1 if binary_needed else 0)
            return emit(report, args.json, EXIT_OK)

        with adoption_lock(target, "upgrade"):
            snapshot_new = write_snapshot(target, dist)
            changes = list(plan["changes"])
            if binary_needed:
                changes.append(
                    Change(
                        action="update",
                        path=".agent/bin/workflow-check",
                        data=binary.path.read_bytes(),
                        mode=0o755,
                        reason=f"verified workflow-check upgrade ({binary.provenance})",
                    )
                )
            last_upgrade = {
                "from_commit": previous_commit,
                "to_commit": dist.commit,
                "at": now_iso(),
                "distribution_sha256": dist.package_sha256,
            }
            upstream = _upstream_data(
                dist=dist,
                action="upgrade",
                owner=args.owner,
                branch=report["branch"],
                integration_branch=integration_branch,
                previous_commit=previous_commit,
                snapshot=snapshot_new,
                binary=effective_binary,
                last_upgrade=last_upgrade,
            )
            changes.append(
                Change(
                    action="update",
                    path=".agent/upstream.yaml",
                    data=render_upstream(upstream).encode("utf-8"),
                    mode=0o644,
                    reason="successful upgrade provenance",
                )
            )
            tx = Transaction(target, "upgrade")
            tx.backup(changes)
            tx.apply(changes)

            expected: dict[str, str | None] = {}
            for item in plan["decisions"]:
                path = item["path"]
                if item["decision"] in ("UPDATE", "CREATE"):
                    assert dist.managed[path].sha256
                    expected[path] = dist.managed[path].sha256
                elif item["decision"] in ("KEEP_LOCAL", "KEEP_IDENTICAL"):
                    state, data, _ = inspect_host_path(target, path)
                    expected[path] = sha256_bytes(data) if state == "file" and data is not None else None
                elif item["decision"] == "DELETE_PLANNED":
                    expected[path] = None
            expected[".agent/bin/workflow-check"] = effective_binary.sha256
            mismatches = _verify_expected_files(target, expected)
            if mismatches:
                raise AdoptionError("POST_VERIFY_FILES", "升级后文件校验失败", details={"mismatches": mismatches})

            upstream_after = read_upstream(target)
            if upstream_after["source"]["commit"].lower() != dist.commit.lower():
                raise AdoptionError("POST_VERIFY_UPSTREAM", "upstream.yaml 未更新到目标 Commit")
            snapshot_after = read_snapshot_at(target, upstream_after["snapshot"]["ref"])
            if snapshot_after.files != {p: f.sha256 for p, f in dist.managed.items()}:
                raise AdoptionError("POST_VERIFY_SNAPSHOT", "新快照与分发包不一致")

            gate_after = verify_gate_with_binary(
                target / ".agent/bin/workflow-check", config_text, integration_branch
            )
            if gate_after["status"] != "PASS":
                raise AdoptionError(
                    "POST_VERIFY_GATE",
                    f"升级后的 workflow-check 未通过真实 Gate: {gate_after['output']}",
                )

            protected_after = inventory_protected(target)
            diff = diff_inventory(report["protected_before"], protected_after)
            if diff["changed"] or diff["removed"] or diff["added"]:
                raise AdoptionError(
                    "PROTECTED_CHANGED",
                    "升级过程改变了宿主保护文件，已回滚",
                    details=diff,
                )
            report["protected_after"] = protected_after
            report["verification"].update(
                {
                    "files": "PASS",
                    "upstream": "PASS",
                    "snapshot": "PASS",
                    "gate": gate_after["status"],
                    "protected_unchanged": True,
                    "config_schema": CONFIG_SCHEMA,
                    "state_schema_preserved": "2,3",
                }
            )
            tx.finalize("success")
            report["backup_id"] = tx.id
            report["backup_path"] = str(tx.backup_root)
            report["status"] = "UPGRADED"
            write_report(target, report)
            return emit(report, args.json, EXIT_OK)
    except AdoptionError as exc:
        rollback: dict[str, Any] | None = None
        if tx is not None:
            rollback = tx.rollback()
            tx.finalize("rolled_back" if rollback.get("ok") else "rollback_failed")
            report["rollback"] = rollback
        if snapshot_new is not None and snapshot_new.created:
            shutil.rmtree(snapshot_new.root, ignore_errors=True)
        report["status"] = "CONFLICT" if exc.exit_code == EXIT_CONFLICT else "FAILED"
        report["error"] = exc.message
        report["error_code"] = exc.code
        report["error_details"] = exc.details
        try:
            if report.get("target") and Path(report["target"]).is_dir():
                write_report(Path(report["target"]), report)
        except Exception:  # noqa: BLE001
            pass
        return emit(report, args.json, exc.exit_code)
    except Exception as exc:  # noqa: BLE001
        rollback = None
        if tx is not None:
            rollback = tx.rollback()
            tx.finalize("rolled_back" if rollback.get("ok") else "rollback_failed")
            report["rollback"] = rollback
        if snapshot_new is not None and snapshot_new.created:
            shutil.rmtree(snapshot_new.root, ignore_errors=True)
        report["status"] = "FAILED"
        report["error"] = str(exc)
        report["error_code"] = "INTERNAL"
        try:
            if report.get("target") and Path(report["target"]).is_dir():
                write_report(Path(report["target"]), report)
        except Exception:  # noqa: BLE001
            pass
        return emit(report, args.json, EXIT_FAIL)
    finally:
        if dist is not None:
            dist.cleanup()
        if binary_dir is not None:
            binary_dir.cleanup()


def cmd_verify(args: argparse.Namespace) -> int:
    report = base_report("verify", Path(args.target))
    try:
        target = require_git_root(Path(args.target))
        upstream = read_upstream(target)
        base = load_base_snapshot(target, upstream)
        mismatches: list[str] = []
        for path, expected in sorted(base.files.items()):
            state, data, _ = inspect_host_path(target, path)
            if state != "file" or sha256_bytes(data or b"") != expected:
                mismatches.append(path)
        config_text, _, integration_branch = read_config(target)
        if not ref_exists(target, f"refs/remotes/origin/{integration_branch}"):
            raise AdoptionError(
                "INTEGRATION_BRANCH_UNKNOWN",
                f"目标仓库不存在 origin/{integration_branch}",
            )
        binary_rel = ".agent/bin/workflow-check"
        state, data, _ = inspect_host_path(target, binary_rel)
        runtime = upstream.get("runtime") or {}
        binary_sha = sha256_bytes(data or b"") if state == "file" else ""
        expected_binary = str(runtime.get("workflow_check_sha256") or "")
        binary_ok = state == "file" and bool(expected_binary) and binary_sha == expected_binary
        findings, reverify = scan_legacy_evidence(target)
        gate = verify_gate_with_binary(target / binary_rel, config_text, integration_branch)
        report.update(
            {
                "source_commit": upstream["source"]["commit"],
                "base_commit": base.commit,
                "framework_version": upstream["source"]["framework_version"],
                "integration_branch": integration_branch,
                "legacy_evidence": findings,
                "task_reverification_required": reverify,
            }
        )
        report["verification"] = {
            "files": "PASS" if not mismatches else "FAIL",
            "binary": "PASS" if binary_ok else "FAIL",
            "gate": gate["status"],
            "config_schema": CONFIG_SCHEMA,
            "state_schema_preserved": "2,3",
        }
        if mismatches:
            report["verification"]["mismatches"] = mismatches
        ok = not mismatches and binary_ok and gate["status"] == "PASS"
        report["status"] = "VERIFIED" if ok else "DRIFT"
        return emit(report, args.json, EXIT_OK if ok else EXIT_FAIL)
    except AdoptionError as exc:
        report["status"] = "FAILED"
        report["error"] = exc.message
        report["error_code"] = exc.code
        return emit(report, args.json, exc.exit_code)


def cmd_restore(args: argparse.Namespace) -> int:
    report = base_report("restore", Path(args.target))
    try:
        target = require_git_root(Path(args.target))
        backup_root = git_path(target, "workflow-adoption") / "backups" / args.backup
        manifest_path = backup_root / "manifest.json"
        if not manifest_path.is_file():
            raise AdoptionError("BACKUP_NOT_FOUND", f"找不到备份: {args.backup}")
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
        if manifest.get("format") != 1 or not isinstance(manifest.get("entries"), list):
            raise AdoptionError("BACKUP_FORMAT", "备份 manifest 非法")
        report["backup_id"] = args.backup
        report["backup_path"] = str(backup_root)
        report["changes"] = [
            {"action": "restore" if e.get("existed") else "remove", "path": e.get("path", "")}
            for e in manifest["entries"]
        ]
        if not args.execute:
            report["status"] = "PLAN"
            return emit(report, args.json, EXIT_OK)
        errors: list[str] = []
        with adoption_lock(target, "restore"):
            for entry in reversed(manifest["entries"]):
                path = str(entry.get("path") or "")
                ensure_safe_rel_path(path)
                dest = target.joinpath(*PurePosixPath(path).parts)
                if entry.get("existed"):
                    src = backup_root / str(entry.get("backup") or "")
                    if not src.is_file():
                        errors.append(f"{path}: 备份内容缺失")
                        continue
                    data = src.read_bytes()
                    if sha256_bytes(data) != entry.get("sha256"):
                        errors.append(f"{path}: 备份 SHA256 不匹配")
                        continue
                    _ensure_parent_dirs(target, dest)
                    write_atomic(dest, data, int(entry.get("mode") or 0o644))
                else:
                    if dest.is_symlink():
                        errors.append(f"{path}: 符号链接，拒绝删除")
                    elif dest.is_file():
                        dest.unlink()
            verify_errors: list[str] = []
            for entry in manifest["entries"]:
                state, data, _ = inspect_host_path(target, str(entry.get("path") or ""))
                if entry.get("existed"):
                    if state != "file" or sha256_bytes(data or b"") != entry.get("sha256"):
                        verify_errors.append(str(entry.get("path")))
                elif state == "file":
                    verify_errors.append(str(entry.get("path")))
        report["rollback"] = {"ok": not errors and not verify_errors, "errors": errors, "mismatched": verify_errors}
        report["status"] = "RESTORED" if not errors and not verify_errors else "FAILED"
        return emit(report, args.json, EXIT_OK if report["status"] == "RESTORED" else EXIT_FAIL)
    except AdoptionError as exc:
        report["status"] = "FAILED"
        report["error"] = exc.message
        report["error_code"] = exc.code
        return emit(report, args.json, exc.exit_code)


# ---------------------------------------------------------------------------
# CLI
# ---------------------------------------------------------------------------


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        prog="workflow-adoption",
        description="Workflow V3 source distribution, install, upgrade and provenance tool",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    p_dist = sub.add_parser("dist", help="build a deterministic source distribution from a clean commit")
    p_dist.add_argument("--source", required=True, help="clean Git repository at the exact source commit")
    p_dist.add_argument("--output", required=True, help="output directory for tar.gz and .sha256")
    p_dist.add_argument("--repository", default=DEFAULT_REPOSITORY)
    p_dist.add_argument("--json", action="store_true")
    p_dist.set_defaults(func=cmd_dist)

    p_inspect = sub.add_parser("inspect", help="verify and inspect a source distribution archive")
    p_inspect.add_argument("--package", required=True)
    p_inspect.add_argument("--expected-sha256")
    p_inspect.add_argument("--json", action="store_true")
    p_inspect.set_defaults(func=cmd_inspect)

    def add_source_args(p: argparse.ArgumentParser) -> None:
        group = p.add_mutually_exclusive_group(required=True)
        group.add_argument("--source", help="clean Git repository at the exact source commit")
        group.add_argument("--package", help="source distribution tar.gz")
        p.add_argument("--source-commit", required=True, help="exact 40-hex upstream commit")
        p.add_argument("--distribution-sha256", help="required with --package")
        p.add_argument("--owner", required=True)
        p.add_argument("--owner-authorized", action="store_true")
        p.add_argument("--workflow-check", help="verified external workflow-check binary")
        p.add_argument("--workflow-check-sha256")
        p.add_argument("--workflow-check-commit")
        p.add_argument("--execute", action="store_true", help="apply the plan (default: dry-run)")
        p.add_argument("--json", action="store_true")

    p_install = sub.add_parser("install", help="first install into a host Git repository")
    p_install.add_argument("--target", required=True)
    p_install.add_argument("--integration-branch", required=True, help="Owner-confirmed real branch")
    p_install.add_argument("--resource", action="append", default=[], help="KIND=.agent/registry/FILE.md (opt-in)")
    add_source_args(p_install)
    p_install.set_defaults(func=cmd_install)

    p_upgrade = sub.add_parser("upgrade", help="three-way upgrade using .agent/upstream.yaml snapshot as B")
    p_upgrade.add_argument("--target", required=True)
    p_upgrade.add_argument("--allow-deletes", action="store_true", help="authorize upstream file removals")
    add_source_args(p_upgrade)
    p_upgrade.set_defaults(func=cmd_upgrade)

    p_verify = sub.add_parser("verify", help="verify installed files, provenance, binary and real Gate")
    p_verify.add_argument("--target", required=True)
    p_verify.add_argument("--json", action="store_true")
    p_verify.set_defaults(func=cmd_verify)

    p_restore = sub.add_parser("restore", help="restore files from a transaction backup")
    p_restore.add_argument("--target", required=True)
    p_restore.add_argument("--backup", required=True)
    p_restore.add_argument("--execute", action="store_true")
    p_restore.add_argument("--json", action="store_true")
    p_restore.set_defaults(func=cmd_restore)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return int(args.func(args))
    except AdoptionError as exc:
        if getattr(args, "json", False):
            print(
                json.dumps(
                    {
                        "status": "ERROR",
                        "error_code": exc.code,
                        "error": exc.message,
                        "error_details": exc.details,
                        "exit_code": exc.exit_code,
                    },
                    indent=2,
                    sort_keys=True,
                    ensure_ascii=False,
                )
            )
        else:
            print(f"ERROR[{exc.code}]: {exc.message}", file=sys.stderr)
        return exc.exit_code
    except KeyboardInterrupt:
        print("INTERRUPTED", file=sys.stderr)
        return EXIT_FAIL
    except Exception as exc:  # noqa: BLE001
        if getattr(args, "json", False):
            print(
                json.dumps(
                    {"status": "ERROR", "error_code": "INTERNAL", "error": str(exc), "exit_code": EXIT_FAIL},
                    indent=2,
                    sort_keys=True,
                    ensure_ascii=False,
                )
            )
        else:
            print(f"ERROR[INTERNAL]: {exc}", file=sys.stderr)
        return EXIT_FAIL


if __name__ == "__main__":
    raise SystemExit(main())
