#!/usr/bin/env python3
"""Workflow V4 adoption tooling.

子命令:
    dist      从干净 commit 构建源码分发包
    inspect   校验并查看源码分发包
    install   首次安装到宿主仓库
    upgrade   B/L/N 升级（哈希三方比较）
    rollback  回滚最近一次（或指定）事务
    verify    校验宿主安装完整性
    check     仓库一致性静态检查

设计约束:
- 不依赖第三方库；只用标准库与 Git。
- 不构建、不安装任何二进制；V4 没有 Engine。
- 所有写操作事务化：先备份、再写、失败自动回滚。
"""

from __future__ import annotations

import argparse
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
from datetime import datetime, timezone
from pathlib import Path

sys.dont_write_bytecode = True

FORMAT = 1
KIND = "agent-workflow-source-distribution"
VERSION_FILE = ".agent/VERSION"
UPSTREAM_FILE = ".agent/upstream.json"
MANIFEST_IN_ARCHIVE = "MANIFEST.json"

MANAGED_PREFIXES = (
    ".agent/specs/",
    ".agent/roles/",
    ".agent/templates/",
    ".agent/tools/",
    ".agent/adoption/",
)
MANAGED_EXACT = (
    ".agent/README.md",
    VERSION_FILE,
)
PACKAGE_ONLY_PREFIXES = (
    "docs/",
    "dogfood/",
    "scripts/release/",
    ".agent/adoption/tests/",
)
PACKAGE_ONLY_EXACT = (
    "README.md",
    "PACKAGE_MANIFEST.md",
    ".cnb.yml",
    ".gitignore",
)
# 绝不进入分发包（即使被 tracked）
EXCLUDED_EXACT = (
    UPSTREAM_FILE,
    ".agent/workflow.yaml",
    "AGENTS.md",
)
EXCLUDED_PREFIXES = (
    ".agent/tasks/",
    ".agent/registry/",
    ".agent/bin/",
    ".git/",
    "dist/",
)

REQUIRED_SPECS = (
    "WorkflowAgentDirective.md",
    "AgentCollaborationSpecification.md",
    "Five-AgentResponsibilityBoundary.md",
    "WorkflowHandoffContract.md",
    "WorkflowGitLifecycle.md",
    "WorkflowLoopCircuitBreaker.md",
    "WorkflowAdoption.md",
)
REQUIRED_ROLES = (
    "TaskBuilderPrompt.md",
    "AnalystAgent.md",
    "ArchitectAgent.md",
    "CoderAgent.md",
    "CleanerAgent.md",
    "OwnerGateAgent.md",
    "DelivererAgent.md",
)
RECORD_FILES = ("task.md", "plan.md", "handoff.md", "findings.md", "delivery.md", "decisions.md")

FORBIDDEN_ARTIFACTS = (
    ".agent/engine",
    ".agent/bin",
    ".agent/workflow.yaml",
    ".agent/specs/WorkflowRouteSelection.md",
    ".agent/specs/tools",
    ".agent/templates/workflow.yaml.template",
    ".agent/templates/registry",
)

FORBIDDEN_TERMS = (
    "state.yaml",
    "workflow-check",
    "coder-start",
    "cleaner-start",
    "owner-gate-start",
    "delivery-start",
    "merge-ready",
    "HANDOFF_PROMPT",
    "state_schema",
    "schema_version",
)


class AdoptionError(RuntimeError):
    """可预期的操作失败：输出 BLOCKED 风格的报告并以非零退出。"""

    def __init__(self, code: str, message: str, detail: dict | None = None):
        super().__init__(message)
        self.code = code
        self.message = message
        self.detail = detail or {}


# --------------------------------------------------------------------------- #
# 基础工具
# --------------------------------------------------------------------------- #


def sha256_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def read_json(path: Path) -> dict:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        raise AdoptionError("MISSING", f"文件不存在: {path}")
    except json.JSONDecodeError as exc:
        raise AdoptionError("INVALID_JSON", f"JSON 无法解析: {path}: {exc}")


def write_json(path: Path, payload: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_text(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    os.replace(tmp, path)


def utc_now() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def run_git(repo: Path, *args: str) -> str:
    proc = subprocess.run(
        ["git", "-C", str(repo), *args],
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise AdoptionError(
            "GIT_FAILED",
            f"git {' '.join(args)} 失败: {proc.stderr.strip()}",
        )
    return proc.stdout.strip()


def require_git_repo(path: Path) -> Path:
    if not path.exists():
        raise AdoptionError("TARGET_MISSING", f"目标不存在: {path}")
    try:
        root = run_git(path, "rev-parse", "--show-toplevel")
    except AdoptionError:
        raise AdoptionError("NOT_A_GIT_REPO", f"目标不是 Git 仓库: {path}")
    return Path(root).resolve()


def read_version(repo: Path) -> str:
    path = repo / VERSION_FILE
    if not path.is_file():
        raise AdoptionError("VERSION_MISSING", f"缺少 {VERSION_FILE}")
    version = path.read_text(encoding="utf-8").strip()
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise AdoptionError("VERSION_INVALID", f"{VERSION_FILE} 不是 x.y.z: {version!r}")
    return version


# --------------------------------------------------------------------------- #
# 分类与收集
# --------------------------------------------------------------------------- #


def is_excluded(path: str) -> bool:
    if path in EXCLUDED_EXACT:
        return True
    return any(path.startswith(prefix) for prefix in EXCLUDED_PREFIXES)


def classify(path: str) -> str | None:
    """返回 managed / package_only / None（不进入分发包）。"""
    if is_excluded(path):
        return None
    if path.startswith(".agent/adoption/tests/"):
        return "package_only"
    if path in MANAGED_EXACT:
        return "managed"
    if any(path.startswith(prefix) for prefix in MANAGED_PREFIXES):
        return "managed"
    if path in PACKAGE_ONLY_EXACT:
        return "package_only"
    if any(path.startswith(prefix) for prefix in PACKAGE_ONLY_PREFIXES):
        return "package_only"
    return None


def tracked_files(repo: Path) -> list[str]:
    out = run_git(repo, "ls-files", "-z")
    return sorted(item for item in out.split("\0") if item)


def collect_distribution_files(repo: Path) -> tuple[dict[str, str], list[str]]:
    """返回 (path -> class, 未分类文件列表)。"""
    files: dict[str, str] = {}
    unknown: list[str] = []
    for path in tracked_files(repo):
        klass = classify(path)
        if klass is None:
            if is_excluded(path):
                continue
            unknown.append(path)
            continue
        files[path] = klass
    return files, unknown


# --------------------------------------------------------------------------- #
# dist
# --------------------------------------------------------------------------- #


def build_dist(source: Path, output: Path, expected_commit: str | None = None,
               repository: str | None = None) -> dict:
    repo = require_git_repo(source)

    status = run_git(repo, "status", "--porcelain")
    if status:
        raise AdoptionError(
            "SOURCE_DIRTY",
            "源仓库不干净，拒绝构建分发包",
            {"status": status.splitlines()[:20]},
        )

    head = run_git(repo, "rev-parse", "HEAD")
    tree = run_git(repo, "rev-parse", "HEAD^{tree}")
    if expected_commit and expected_commit != head:
        raise AdoptionError(
            "COMMIT_MISMATCH",
            f"期望 commit {expected_commit} 与实际 HEAD {head} 不一致",
        )

    version = read_version(repo)
    files, unknown = collect_distribution_files(repo)
    if unknown:
        raise AdoptionError(
            "UNCLASSIFIED_FILES",
            "存在未分类的 tracked 文件，必须先显式归类",
            {"files": unknown},
        )

    if repository is None:
        try:
            repository = run_git(repo, "remote", "get-url", "origin")
        except AdoptionError:
            repository = ""

    members = []
    for path in sorted(files):
        full = repo / path
        data = full.read_bytes()
        mode = "100755" if os.access(full, os.X_OK) else "100644"
        members.append({
            "path": path,
            "sha256": sha256_bytes(data),
            "mode": mode,
            "install": files[path],
            "data": data,
        })

    manifest = {
        "format": FORMAT,
        "kind": KIND,
        "framework_version": version,
        "source": {
            "repository": repository,
            "commit": head,
            "tree": tree,
        },
        "files": [
            {k: m[k] for k in ("path", "sha256", "mode", "install")} for m in members
        ],
        "excluded": sorted(EXCLUDED_EXACT) + sorted(EXCLUDED_PREFIXES),
        "release": {"tag": "", "url": ""},
        # 不写入 generated_at 等易变字段：归档必须可复现。
    }

    output.mkdir(parents=True, exist_ok=True)
    top = f"agent-workflow-src-{version}"
    archive_path = output / f"{top}.tar.gz"

    raw = io.BytesIO()
    with tarfile.open(fileobj=raw, mode="w") as tar:
        def add(name: str, data: bytes, mode: int) -> None:
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = mode
            info.mtime = 0
            info.uid = 0
            info.gid = 0
            info.uname = ""
            info.gname = ""
            tar.addfile(info, io.BytesIO(data))

        add(f"{top}/{MANIFEST_IN_ARCHIVE}",
            (json.dumps(manifest, ensure_ascii=False, indent=2, sort_keys=True) + "\n").encode(),
            0o644)
        for member in members:
            add(f"{top}/{member['path']}", member["data"], int(member["mode"], 8))

    # gzip 头默认写入当前时间；固定为 0 才能保证同一 commit 的归档逐字节一致。
    compressed = io.BytesIO()
    with gzip.GzipFile(filename="", mode="wb", fileobj=compressed, mtime=0, compresslevel=9) as gz:
        gz.write(raw.getvalue())

    archive_path.write_bytes(compressed.getvalue())
    digest = sha256_file(archive_path)
    checksum_path = output / f"{archive_path.name}.sha256"
    checksum_path.write_text(f"{digest}  {archive_path.name}\n", encoding="utf-8")

    return {
        "archive": str(archive_path),
        "sha256": digest,
        "checksum": str(checksum_path),
        "version": version,
        "commit": head,
        "files": len(members),
        "managed": sum(1 for m in members if m["install"] == "managed"),
        "package_only": sum(1 for m in members if m["install"] == "package_only"),
    }


# --------------------------------------------------------------------------- #
# 归档读取
# --------------------------------------------------------------------------- #


def _safe_name(name: str) -> bool:
    if name.startswith("/") or name.startswith("../") or "/../" in name:
        return False
    return True


def load_package(package: Path, expected_sha256: str | None = None) -> dict:
    if not package.is_file():
        raise AdoptionError("PACKAGE_MISSING", f"分发包不存在: {package}")

    digest = sha256_file(package)
    if expected_sha256 and digest.lower() != expected_sha256.lower():
        raise AdoptionError(
            "CHECKSUM_MISMATCH",
            "分发包 SHA256 与期望不一致",
            {"actual": digest, "expected": expected_sha256},
        )

    files: dict[str, dict] = {}
    manifest: dict | None = None
    top: str | None = None

    try:
        with tarfile.open(package, "r:gz") as tar:
            for member in tar.getmembers():
                if not _safe_name(member.name):
                    raise AdoptionError("UNSAFE_ARCHIVE", f"归档包含不安全路径: {member.name}")
                if member.issym() or member.islnk():
                    raise AdoptionError("UNSAFE_ARCHIVE", f"归档包含链接: {member.name}")
                if not member.isfile():
                    continue
                parts = member.name.split("/", 1)
                if len(parts) != 2:
                    raise AdoptionError("UNSAFE_ARCHIVE", f"归档成员缺少顶层目录: {member.name}")
                if top is None:
                    top = parts[0]
                elif top != parts[0]:
                    raise AdoptionError("UNSAFE_ARCHIVE", "归档存在多个顶层目录")
                rel = parts[1]
                handle = tar.extractfile(member)
                if handle is None:
                    raise AdoptionError("UNSAFE_ARCHIVE", f"无法读取成员: {member.name}")
                data = handle.read()
                if rel == MANIFEST_IN_ARCHIVE:
                    manifest = json.loads(data.decode("utf-8"))
                else:
                    files[rel] = {"data": data, "mode": member.mode}
    except (tarfile.TarError, json.JSONDecodeError) as exc:
        raise AdoptionError("PACKAGE_INVALID", f"分发包无法解析: {exc}")

    if manifest is None:
        raise AdoptionError("PACKAGE_INVALID", "分发包缺少 MANIFEST.json")
    if manifest.get("kind") != KIND or manifest.get("format") != FORMAT:
        raise AdoptionError("PACKAGE_INVALID", "MANIFEST.json 的 kind/format 不匹配")

    declared = {entry["path"]: entry for entry in manifest.get("files", [])}
    if set(declared) != set(files):
        missing = sorted(set(declared) - set(files))
        extra = sorted(set(files) - set(declared))
        raise AdoptionError(
            "PACKAGE_INVALID",
            "MANIFEST.json 与实际成员不一致",
            {"missing": missing, "extra": extra},
        )

    for path, entry in declared.items():
        actual = sha256_bytes(files[path]["data"])
        if actual != entry["sha256"]:
            raise AdoptionError(
                "PACKAGE_INVALID",
                f"成员哈希不匹配: {path}",
                {"actual": actual, "expected": entry["sha256"]},
            )
        if entry.get("install") not in ("managed", "package_only"):
            raise AdoptionError("PACKAGE_INVALID", f"成员缺少合法 install 分类: {path}")

    return {
        "path": str(package),
        "sha256": digest,
        "manifest": manifest,
        "files": files,
        "version": manifest["framework_version"],
        "commit": manifest["source"]["commit"],
        "top": top,
    }


def managed_files(package: dict) -> dict[str, dict]:
    declared = {entry["path"]: entry for entry in package["manifest"]["files"]}
    return {
        path: package["files"][path]
        for path, entry in declared.items()
        if entry["install"] == "managed"
    }


# --------------------------------------------------------------------------- #
# 事务
# --------------------------------------------------------------------------- #


def transaction_root(target: Path) -> Path:
    return target / ".agent" / ".transactions"


def new_transaction(target: Path, action: str) -> tuple[str, Path]:
    txn_id = f"{time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())}-{action}"
    root = transaction_root(target)
    root.mkdir(parents=True, exist_ok=True)
    counter = 0
    while (root / txn_id).exists():
        counter += 1
        txn_id = f"{time.strftime('%Y%m%dT%H%M%SZ', time.gmtime())}-{action}-{counter}"
    path = root / txn_id
    (path / "backup").mkdir(parents=True)
    return txn_id, path


def apply_plan(target: Path, plan: dict, txn_dir: Path, payload: dict[str, bytes],
               modes: dict[str, int], upstream_payload: dict | None) -> None:
    """备份 → 写入 → 删除 → 写 upstream.json；异常时自动回滚。"""
    backup = txn_dir / "backup"
    entries = []

    for action in ("write", "delete"):
        for rel in plan[action]:
            dest = target / rel
            existed = dest.exists()
            if existed:
                backup_path = backup / rel
                backup_path.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(dest, backup_path)
            entries.append({
                "path": rel,
                "action": action,
                "existed": existed,
                "sha256": sha256_file(dest) if existed else None,
            })

    upstream_dest = target / UPSTREAM_FILE
    upstream_backup = None
    if upstream_dest.exists():
        upstream_backup = backup / UPSTREAM_FILE
        upstream_backup.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(upstream_dest, upstream_backup)

    plan_doc = {
        "transaction": txn_dir.name,
        "created_at": utc_now(),
        "plan": plan,
        "entries": entries,
        "upstream_existed": upstream_backup is not None,
        "status": "applying",
    }
    write_json(txn_dir / "plan.json", plan_doc)

    written: list[str] = []
    try:
        for rel in plan["write"]:
            dest = target / rel
            dest.parent.mkdir(parents=True, exist_ok=True)
            data = payload[rel]
            fd, tmp = tempfile.mkstemp(dir=str(dest.parent), prefix=".wf-")
            with os.fdopen(fd, "wb") as handle:
                handle.write(data)
            os.replace(tmp, dest)
            os.chmod(dest, modes.get(rel, 0o644))
            written.append(rel)
        for rel in plan["delete"]:
            dest = target / rel
            if dest.exists():
                dest.unlink()
            written.append(rel)
        if upstream_payload is not None:
            write_json(upstream_dest, upstream_payload)
        plan_doc["status"] = "applied"
        write_json(txn_dir / "plan.json", plan_doc)
    except Exception:
        rollback_transaction(target, txn_dir)
        raise


def rollback_transaction(target: Path, txn_dir: Path) -> dict:
    plan_doc = read_json(txn_dir / "plan.json")
    backup = txn_dir / "backup"
    restored, removed = [], []

    for entry in plan_doc["entries"]:
        rel = entry["path"]
        dest = target / rel
        src = backup / rel
        if entry["existed"] and src.exists():
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(src, dest)
            restored.append(rel)
        elif not entry["existed"] and dest.exists():
            dest.unlink()
            removed.append(rel)

    if plan_doc.get("upstream_existed"):
        src = backup / UPSTREAM_FILE
        if src.exists():
            shutil.copy2(src, target / UPSTREAM_FILE)
    else:
        if (target / UPSTREAM_FILE).exists():
            (target / UPSTREAM_FILE).unlink()

    plan_doc["status"] = "rolled_back"
    write_json(txn_dir / "plan.json", plan_doc)
    return {"transaction": txn_dir.name, "restored": restored, "removed": removed}


# --------------------------------------------------------------------------- #
# install / upgrade
# --------------------------------------------------------------------------- #


def _install_payload(package: dict) -> tuple[dict[str, bytes], dict[str, str], dict[str, int]]:
    files = managed_files(package)
    payload = {path: entry["data"] for path, entry in files.items()}
    hashes = {path: sha256_bytes(entry["data"]) for path, entry in files.items()}
    modes = {path: (entry["mode"] & 0o777) for path, entry in files.items()}
    return payload, hashes, modes


def plan_install(target: Path, package: dict) -> dict:
    payload, _, _ = _install_payload(package)
    create, overwrite, identical = [], [], []

    for rel in sorted(payload):
        dest = target / rel
        if not dest.exists():
            create.append(rel)
        elif sha256_file(dest) == sha256_bytes(payload[rel]):
            identical.append(rel)
        else:
            overwrite.append(rel)

    if overwrite:
        raise AdoptionError(
            "ALREADY_INSTALLED",
            "目标已存在内容不同的 managed 文件；首次安装拒绝覆盖，请改用 upgrade",
            {"conflicts": overwrite},
        )

    return {
        "action": "install",
        "version": package["version"],
        "commit": package["commit"],
        "create": create,
        "overwrite": [],
        "delete": [],
        "identical": identical,
    }


def plan_upgrade(target: Path, package: dict, keep_local: set[str]) -> dict:
    try:
        upstream = read_json(target / UPSTREAM_FILE)
    except AdoptionError:
        raise AdoptionError(
            "BASE_UNKNOWN",
            f"缺少或无法解析 {UPSTREAM_FILE}；无法确定安装基线 B，拒绝升级",
        )
    if upstream.get("format") != 1 or not isinstance(upstream.get("files"), dict):
        raise AdoptionError("BASE_UNKNOWN", "upstream.json 结构不支持，拒绝升级")

    base: dict[str, str] = upstream["files"]
    _, local, _ = _install_payload(package)

    create, overwrite, delete, kept_local, kept_deleted, conflicts = [], [], [], [], [], []
    all_paths = sorted(set(base) | set(local))

    for rel in all_paths:
        dest = target / rel
        host = sha256_file(dest) if dest.exists() else None
        b = base.get(rel)
        l = local.get(rel)

        if rel in keep_local:
            if l is not None:
                kept_local.append(rel)
            continue

        if l is None:  # 上游删除
            if host is None:
                continue
            if host == b:
                delete.append(rel)
            else:
                conflicts.append({"path": rel, "reason": "本地已修改但上游删除"})
            continue

        if host is None:
            if b is None:
                create.append(rel)
            elif l == b:
                kept_deleted.append(rel)
            else:
                conflicts.append({"path": rel, "reason": "本地删除但上游已更新"})
            continue

        if host == l:
            continue
        if host == b:
            overwrite.append(rel)
            continue
        if l == b:
            kept_local.append(rel)
            continue
        conflicts.append({"path": rel, "reason": "本地与上游同时修改"})

    if conflicts:
        raise AdoptionError(
            "CONFLICT",
            "升级存在冲突，未写入任何文件",
            {"conflicts": conflicts},
        )

    return {
        "action": "upgrade",
        "from_version": upstream.get("framework_version"),
        "to_version": package["version"],
        "commit": package["commit"],
        "create": create,
        "overwrite": overwrite,
        "delete": delete,
        "kept_local": kept_local,
        "kept_deleted": kept_deleted,
    }


def _upstream_doc(target: Path, package: dict, hashes: dict[str, str],
                  integration_branch: str, owner: str) -> dict:
    previous = {}
    if (target / UPSTREAM_FILE).exists():
        try:
            previous = read_json(target / UPSTREAM_FILE)
        except AdoptionError:
            previous = {}
    return {
        "format": 1,
        "framework_version": package["version"],
        "source_commit": package["commit"],
        "source_repository": package["manifest"]["source"].get("repository", ""),
        "distribution_sha256": package["sha256"],
        "package": Path(package["path"]).name,
        "integration_branch": integration_branch or previous.get("integration_branch", ""),
        "owner": owner or previous.get("owner", ""),
        "installed_at": utc_now(),
        "previous_version": previous.get("framework_version"),
        "files": dict(sorted(hashes.items())),
    }


def execute_transaction(target: Path, plan: dict, package: dict,
                        integration_branch: str, owner: str) -> dict:
    payload, hashes, modes = _install_payload(package)

    # 写入后的哈希：被删除的路径不再登记
    final_hashes = {path: digest for path, digest in hashes.items() if path not in plan["delete"]}
    upstream_doc = _upstream_doc(target, package, final_hashes, integration_branch, owner)

    txn_id, txn_dir = new_transaction(target, plan["action"])

    write_payload = {rel: payload[rel] for rel in plan["create"] + plan["overwrite"]}
    write_plan = {
        "write": sorted(write_payload),
        "delete": sorted(plan["delete"]),
    }
    apply_plan(target, write_plan, txn_dir, write_payload, modes, upstream_doc)

    return {"transaction": txn_id, "plan": plan, "upstream": str(target / UPSTREAM_FILE)}


# --------------------------------------------------------------------------- #
# verify / check
# --------------------------------------------------------------------------- #


def verify_target(target: Path) -> dict:
    repo = require_git_repo(target)
    upstream_path = repo / UPSTREAM_FILE
    upstream = read_json(upstream_path)

    if upstream.get("format") != 1:
        raise AdoptionError("VERIFY_FAILED", "upstream.json format 不受支持")

    problems = []
    files: dict[str, str] = upstream.get("files", {})

    version_path = repo / VERSION_FILE
    if not version_path.is_file():
        problems.append({"path": VERSION_FILE, "error": "缺失"})
    else:
        version = version_path.read_text(encoding="utf-8").strip()
        if version != upstream.get("framework_version"):
            problems.append({
                "path": VERSION_FILE,
                "error": f"版本不一致: 文件 {version} / 记录 {upstream.get('framework_version')}",
            })

    for rel, expected in sorted(files.items()):
        dest = repo / rel
        if not dest.is_file():
            problems.append({"path": rel, "error": "缺失"})
            continue
        actual = sha256_file(dest)
        if actual != expected:
            problems.append({"path": rel, "error": "哈希不一致", "actual": actual, "expected": expected})

    for rel in FORBIDDEN_ARTIFACTS:
        if (repo / rel).exists():
            problems.append({"path": rel, "error": "存在 V3 机制残留（V4 已删除）"})

    if problems:
        raise AdoptionError("VERIFY_FAILED", "安装校验失败", {"problems": problems})

    return {
        "target": str(repo),
        "version": upstream.get("framework_version"),
        "source_commit": upstream.get("source_commit"),
        "files": len(files),
        "status": "PASS",
    }


def _is_historical(line: str) -> bool:
    return "V3" in line or "历史" in line or "v3" in line


def check_repository(repo: Path) -> dict:
    repo = require_git_repo(repo)
    failures: list[dict] = []
    warnings: list[dict] = []

    try:
        version = read_version(repo)
    except AdoptionError as exc:
        return {"status": "FAIL", "failures": [{"check": "version", "error": exc.message}]}

    if not version.startswith("4."):
        failures.append({"check": "version", "error": f"V4 仓库版本必须为 4.x，实际 {version}"})

    for name in REQUIRED_SPECS:
        path = repo / ".agent/specs" / name
        if not path.is_file():
            failures.append({"check": "specs", "error": f"缺少规范: {name}"})
        elif len(path.read_text(encoding="utf-8").strip()) < 200:
            failures.append({"check": "specs", "error": f"规范内容过短: {name}"})

    for name in REQUIRED_ROLES:
        path = repo / ".agent/roles" / name
        if not path.is_file():
            failures.append({"check": "roles", "error": f"缺少角色: {name}"})
        elif "## 角色" not in path.read_text(encoding="utf-8"):
            failures.append({"check": "roles", "error": f"角色 Prompt 缺少「## 角色」章节: {name}"})

    for rel in FORBIDDEN_ARTIFACTS:
        if (repo / rel).exists():
            failures.append({"check": "legacy", "error": f"存在 V3 机制残留: {rel}"})

    for state_file in repo.rglob("state.yaml"):
        if "docs/history" in state_file.as_posix() or ".git" in state_file.parts:
            continue
        failures.append({"check": "legacy", "error": f"存在 state.yaml: {state_file.relative_to(repo)}"})

    for base in (repo / ".agent/specs", repo / ".agent/roles"):
        if not base.is_dir():
            continue
        for path in sorted(base.glob("**/*.md")):
            for lineno, line in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
                if _is_historical(line):
                    continue
                for term in FORBIDDEN_TERMS:
                    if term in line:
                        failures.append({
                            "check": "forbidden-term",
                            "error": f"{path.relative_to(repo)}:{lineno} 出现 V3 术语 {term}",
                        })
                        break

    template_dir = repo / ".agent/templates/task"
    for name in RECORD_FILES:
        path = template_dir / name
        if not path.is_file():
            failures.append({"check": "templates", "error": f"缺少记录模板: {name}"})

    history = repo / "docs/history/v3.4.0"
    for rel in ("README.md", "specs/WorkflowAgentDirective.md", "roles/CoderAgent.md"):
        if not (history / rel).is_file():
            failures.append({"check": "history", "error": f"V3 归档缺失: {rel}"})
    if (repo / ".agent/specs/WorkflowRouteSelection.md").exists():
        failures.append({"check": "history", "error": "V3 Route 规范仍在当前规范目录"})

    tool = repo / ".agent/tools/git-facts"
    if not tool.is_file():
        failures.append({"check": "tools", "error": "缺少 .agent/tools/git-facts"})
    elif not os.access(tool, os.X_OK):
        failures.append({"check": "tools", "error": ".agent/tools/git-facts 不可执行"})

    dogfood = repo / "dogfood/ledgerd"
    if dogfood.is_dir():
        if not (dogfood / "go.mod").is_file():
            failures.append({"check": "dogfood", "error": "dogfood/ledgerd 缺少 go.mod"})
        tasks_root = dogfood / ".agent/tasks"
        slugs = [p for p in tasks_root.iterdir() if p.is_dir()] if tasks_root.is_dir() else []
        if not slugs:
            failures.append({"check": "dogfood", "error": "dogfood 缺少任务记录"})
        for slug in slugs:
            for name in RECORD_FILES:
                if not (slug / name).is_file():
                    failures.append({
                        "check": "dogfood",
                        "error": f"任务记录缺少 {name}: {slug.name}",
                    })
            plan = slug / "plan.md"
            if plan.is_file() and "## Next Action" not in plan.read_text(encoding="utf-8"):
                failures.append({"check": "dogfood", "error": f"plan.md 缺少 Next Action: {slug.name}"})

    if (repo / ".agent/registry").exists():
        warnings.append({"check": "registry", "warning": ".agent/registry 是 V3 机制，建议移除或改为普通 Markdown"})

    return {
        "repo": str(repo),
        "version": version,
        "status": "PASS" if not failures else "FAIL",
        "failures": failures,
        "warnings": warnings,
        "checks": {
            "specs": len(REQUIRED_SPECS),
            "roles": len(REQUIRED_ROLES),
            "forbidden_artifacts": len(FORBIDDEN_ARTIFACTS),
        },
    }


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #


def emit(payload: dict, as_json: bool, ok: bool = True) -> None:
    if as_json:
        print(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True))
    else:
        for key, value in payload.items():
            if isinstance(value, (dict, list)):
                print(f"{key}: {json.dumps(value, ensure_ascii=False)}")
            else:
                print(f"{key}: {value}")
    if not ok:
        return


def print_plan(plan: dict) -> None:
    print(f"ACTION : {plan['action']}")
    if plan.get("from_version"):
        print(f"FROM   : {plan['from_version']}")
    print(f"TO     : {plan.get('to_version') or plan.get('version')}")
    print(f"COMMIT : {plan.get('commit')}")
    for key in ("create", "overwrite", "delete", "identical", "kept_local", "kept_deleted"):
        items = plan.get(key) or []
        print(f"{key:11s}: {len(items)}")
        for item in items[:10]:
            print(f"  - {item}")
        if len(items) > 10:
            print(f"  ... 共 {len(items)} 项")


def cmd_dist(args) -> int:
    result = build_dist(Path(args.source), Path(args.output), args.expected_commit, args.repository)
    emit(result, args.json)
    return 0


def cmd_inspect(args) -> int:
    package = load_package(Path(args.package), args.expected_sha256)
    managed = sum(1 for f in package["manifest"]["files"] if f["install"] == "managed")
    result = {
        "package": package["path"],
        "sha256": package["sha256"],
        "version": package["version"],
        "commit": package["commit"],
        "files": len(package["files"]),
        "managed": managed,
        "top": package["top"],
        "status": "PASS",
    }
    emit(result, args.json)
    return 0


def _require_authorization(args) -> None:
    if not args.owner_authorized:
        raise AdoptionError("NOT_AUTHORIZED", "缺少 --owner-authorized：安装/升级必须由 Owner 授权")


def cmd_install(args) -> int:
    _require_authorization(args)
    target = require_git_repo(Path(args.target))
    package = load_package(Path(args.package), args.distribution_sha256)
    if args.source_commit != package["commit"]:
        raise AdoptionError(
            "COMMIT_MISMATCH",
            "传入的 --source-commit 与包内 MANIFEST 不一致",
            {"package": package["commit"], "argument": args.source_commit},
        )
    if not args.integration_branch:
        raise AdoptionError("MISSING_ARGUMENT", "缺少 --integration-branch")

    plan = plan_install(target, package)
    if not args.json:
        print_plan(plan)
    if not args.execute:
        if not args.json:
            print("DRY-RUN: 未写入任何文件；确认后加 --execute")
        else:
            emit({"status": "DRY_RUN", "plan": plan}, True)
        return 0

    result = execute_transaction(target, plan, package, args.integration_branch, args.owner)
    emit(result, args.json)
    return 0


def cmd_upgrade(args) -> int:
    _require_authorization(args)
    target = require_git_repo(Path(args.target))
    package = load_package(Path(args.package), args.distribution_sha256)
    if args.source_commit != package["commit"]:
        raise AdoptionError(
            "COMMIT_MISMATCH",
            "传入的 --source-commit 与包内 MANIFEST 不一致",
            {"package": package["commit"], "argument": args.source_commit},
        )

    keep_local = set(args.keep_local or [])
    plan = plan_upgrade(target, package, keep_local)
    if not args.json:
        print_plan(plan)
    if not args.execute:
        if not args.json:
            print("DRY-RUN: 未写入任何文件；确认后加 --execute")
        else:
            emit({"status": "DRY_RUN", "plan": plan}, True)
        return 0

    result = execute_transaction(target, plan, package, args.integration_branch, args.owner)
    emit(result, args.json)
    return 0


def cmd_rollback(args) -> int:
    target = require_git_repo(Path(args.target))
    root = transaction_root(target)
    if not root.is_dir():
        raise AdoptionError("NO_TRANSACTION", "没有可回滚的事务")

    if args.transaction:
        txn_dir = root / args.transaction
        if not txn_dir.is_dir():
            raise AdoptionError("NO_TRANSACTION", f"事务不存在: {args.transaction}")
    else:
        candidates = sorted(p for p in root.iterdir() if p.is_dir())
        if not candidates:
            raise AdoptionError("NO_TRANSACTION", "没有可回滚的事务")
        txn_dir = candidates[-1]

    plan_doc = read_json(txn_dir / "plan.json")
    if plan_doc.get("status") == "rolled_back":
        raise AdoptionError("ALREADY_ROLLED_BACK", f"事务已回滚: {txn_dir.name}")

    if not args.execute:
        print(f"DRY-RUN: 将回滚事务 {txn_dir.name}，影响 {len(plan_doc['entries'])} 个文件")
        return 0

    result = rollback_transaction(target, txn_dir)
    emit(result, args.json)
    return 0


def cmd_verify(args) -> int:
    result = verify_target(Path(args.target))
    emit(result, args.json)
    return 0


def cmd_check(args) -> int:
    result = check_repository(Path(args.repo))
    emit(result, args.json, ok=result["status"] == "PASS")
    return 0 if result["status"] == "PASS" else 1


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="workflow-adoption", description="Workflow V4 adoption tooling")
    sub = parser.add_subparsers(dest="command", required=True)

    p_dist = sub.add_parser("dist", help="构建源码分发包")
    p_dist.add_argument("--source", required=True)
    p_dist.add_argument("--output", required=True)
    p_dist.add_argument("--expected-commit")
    p_dist.add_argument("--repository")
    p_dist.add_argument("--json", action="store_true")
    p_dist.set_defaults(func=cmd_dist)

    p_inspect = sub.add_parser("inspect", help="校验源码分发包")
    p_inspect.add_argument("--package", required=True)
    p_inspect.add_argument("--expected-sha256")
    p_inspect.add_argument("--json", action="store_true")
    p_inspect.set_defaults(func=cmd_inspect)

    p_install = sub.add_parser("install", help="首次安装")
    p_install.add_argument("--target", required=True)
    p_install.add_argument("--package", required=True)
    p_install.add_argument("--distribution-sha256", required=True)
    p_install.add_argument("--source-commit", required=True)
    p_install.add_argument("--integration-branch", required=True)
    p_install.add_argument("--owner", required=True)
    p_install.add_argument("--owner-authorized", action="store_true")
    p_install.add_argument("--execute", action="store_true")
    p_install.add_argument("--json", action="store_true")
    p_install.set_defaults(func=cmd_install)

    p_upgrade = sub.add_parser("upgrade", help="B/L/N 升级")
    p_upgrade.add_argument("--target", required=True)
    p_upgrade.add_argument("--package", required=True)
    p_upgrade.add_argument("--distribution-sha256", required=True)
    p_upgrade.add_argument("--source-commit", required=True)
    p_upgrade.add_argument("--integration-branch", default="")
    p_upgrade.add_argument("--owner", default="")
    p_upgrade.add_argument("--owner-authorized", action="store_true")
    p_upgrade.add_argument("--keep-local", action="append", default=[])
    p_upgrade.add_argument("--execute", action="store_true")
    p_upgrade.add_argument("--json", action="store_true")
    p_upgrade.set_defaults(func=cmd_upgrade)

    p_rollback = sub.add_parser("rollback", help="回滚事务")
    p_rollback.add_argument("--target", required=True)
    p_rollback.add_argument("--transaction")
    p_rollback.add_argument("--execute", action="store_true")
    p_rollback.add_argument("--json", action="store_true")
    p_rollback.set_defaults(func=cmd_rollback)

    p_verify = sub.add_parser("verify", help="校验宿主安装")
    p_verify.add_argument("--target", required=True)
    p_verify.add_argument("--json", action="store_true")
    p_verify.set_defaults(func=cmd_verify)

    p_check = sub.add_parser("check", help="仓库一致性静态检查")
    p_check.add_argument("--repo", required=True)
    p_check.add_argument("--json", action="store_true")
    p_check.set_defaults(func=cmd_check)

    return parser


def main(argv: list[str] | None = None) -> int:
    parser = build_parser()
    args = parser.parse_args(argv)
    try:
        return args.func(args)
    except BrokenPipeError:
        # 输出被 head/less 提前关闭：静默退出，不打印 traceback。
        return 1
    except AdoptionError as exc:
        payload = {
            "status": "BLOCKED",
            "code": exc.code,
            "message": exc.message,
        }
        if exc.detail:
            payload["detail"] = exc.detail
        if getattr(args, "json", False):
            print(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True))
        else:
            print(f"BLOCKED [{exc.code}] {exc.message}")
            for key, value in (exc.detail or {}).items():
                print(f"  {key}: {json.dumps(value, ensure_ascii=False)}")
        return 2


if __name__ == "__main__":
    sys.exit(main())
