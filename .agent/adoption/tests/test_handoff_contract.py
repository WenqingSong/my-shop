#!/usr/bin/env python3
"""WorkflowHandoffContract 静态一致性检查。

只检查文件与静态规范的一致性，不证明 LLM 在真实运行中遵守 Prompt，
也不证明 Session / 作者身份或验证真的被执行。
"""

from __future__ import annotations

import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
SPEC = ROOT / ".agent/specs/WorkflowHandoffContract.md"

ROLES = {
    "ANALYST": ".agent/roles/AnalystAgent.md",
    "ARCHITECT": ".agent/roles/ArchitectAgent.md",
    "CODER": ".agent/roles/CoderAgent.md",
    "CLEANER": ".agent/roles/CleanerAgent.md",
    "OWNER_GATE": ".agent/roles/OwnerGateAgent.md",
    "DELIVERER": ".agent/roles/DelivererAgent.md",
}
TASK_BUILDER = "TASK_BUILDER"
TASK_BUILDER_PROMPT = ".agent/roles/TaskBuilderPrompt.md"

GATES = {
    "coder-start",
    "cleaner-start",
    "owner-gate-start",
    "delivery-start",
    "merge-ready",
    "none",
}
ACTIONS = {"HANDOFF", "FAILURE_HANDOFF", "OWNER_DECISION", "SAME_SESSION", "STOP"}
NEXT_ROLES = {
    "ANALYST",
    "ARCHITECT",
    "CODER",
    "CLEANER",
    "OWNER_GATE",
    "DELIVERER",
    "TASK_BUILDER",
    "OWNER",
    "NONE",
}
PROMPTS = {"REQUIRED", "NONE"}
FIELDS = [
    "RESULT",
    "CURRENT_ROLE",
    "NEXT_ROLE",
    "NEXT_ACTION",
    "NEXT_TARGET",
    "HANDOFF_PROMPT",
]
REQUIRED_GATE_BY_ROLE = {
    "ANALYST": "coder-start",
    "ARCHITECT": "cleaner-start",
    "CODER": "cleaner-start",
    "CLEANER": "owner-gate-start",
    "OWNER_GATE": "delivery-start",
    "DELIVERER": "merge-ready",
    "TASK_BUILDER": "coder-start",
}
OWNER_WAIT_ROLES = {"ANALYST", "OWNER_GATE", "TASK_BUILDER"}
ALL_ROLES = dict(ROLES, **{TASK_BUILDER: TASK_BUILDER_PROMPT})


def fenced_rows(text: str, marker: str) -> list[list[str]]:
    """提取 marker 之后 fenced block 中以 | 分隔的表格行。"""
    lines = text.splitlines()
    start = None
    for i, line in enumerate(lines):
        if line.strip() == marker:
            start = i + 1
            break
    if start is None:
        raise AssertionError(f"缺少标记 {marker}")
    # 标记可以位于 fenced block 内部（矩阵）或其前面（角色出口表）。
    fences_before = sum(1 for line in lines[: start - 1] if line.strip().startswith("```"))
    in_fence = fences_before % 2 == 1
    rows: list[list[str]] = []
    seen_fence = in_fence
    for line in lines[start:]:
        stripped = line.strip()
        if stripped.startswith("```"):
            if not in_fence:
                in_fence = True
                seen_fence = True
                continue
            break
        if not in_fence:
            if stripped:
                raise AssertionError(f"{marker} 之后必须紧跟 fenced block 或位于其中")
            continue
        if not stripped or stripped.startswith("#"):
            continue
        cells = [c.strip() for c in stripped.split("|")]
        rows.append(cells)
    if not seen_fence:
        raise AssertionError(f"{marker} 未找到对应 fenced block")
    if not rows:
        raise AssertionError(f"{marker} 没有数据行")
    return rows


def parse_matrix(text: str) -> list[dict[str, str]]:
    rows = fenced_rows(text, "# handoff-matrix v1")
    header, body = rows[0], rows[1:]
    expected = ["role", "terminal", "result", "next_role", "action", "gate", "prompt", "enforced"]
    assert header == expected, f"矩阵表头必须为 {expected}，实际 {header}"
    out = []
    for cells in body:
        assert len(cells) == len(expected), f"矩阵行列数错误: {cells}"
        out.append(dict(zip(expected, cells)))
    return out


def parse_exits(text: str, role: str) -> set[tuple[str, str, str, str]]:
    rows = fenced_rows(text, "# handoff-exits")
    assert rows[0] == ["RESULT", "NEXT_ROLE", "NEXT_ACTION", "GATE"], f"{role} 出口表头错误: {rows[0]}"
    out = set()
    for cells in rows[1:]:
        assert len(cells) == 4, f"{role} 出口行列数错误: {cells}"
        out.add(tuple(cells))
    return out


class HandoffContractTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.spec_text = SPEC.read_text(encoding="utf-8")
        cls.matrix = parse_matrix(cls.spec_text)
        cls.role_texts = {role: (ROOT / path).read_text(encoding="utf-8") for role, path in ALL_ROLES.items()}

    def test_matrix_declares_all_seven_roles(self) -> None:
        roles = {row["role"] for row in self.matrix}
        self.assertEqual(roles, set(ALL_ROLES))

    def test_every_role_is_enforced_by_its_prompt(self) -> None:
        rows = [r for r in self.matrix if r["role"] == TASK_BUILDER]
        self.assertTrue(rows, "矩阵必须声明 TaskBuilder 出口")
        enforced = {r["role"] for r in self.matrix if r["enforced"] == "YES"}
        self.assertEqual(enforced, set(ALL_ROLES), "七个角色都必须被 Prompt 与静态检查强制")
        # 出口表与矩阵逐项比对见 test_role_exit_tables_match_matrix；这里额外确认
        # TaskBuilder 的 Prompt 真的产出了出口表，而不是只在矩阵里声明。
        self.assertIn("# handoff-exits", self.role_texts[TASK_BUILDER])

    def test_matrix_values_and_uniqueness(self) -> None:
        terminal_keys: set[tuple[str, str]] = set()
        for row in self.matrix:
            self.assertIn(row["next_role"], NEXT_ROLES, row)
            self.assertIn(row["action"], ACTIONS, row)
            self.assertIn(row["gate"], GATES, row)
            self.assertIn(row["prompt"], PROMPTS, row)
            self.assertIn(row["enforced"], {"YES", "NO"}, row)
            key = (row["role"], row["terminal"])
            self.assertNotIn(key, terminal_keys, f"重复终态: {key}")
            terminal_keys.add(key)

    def test_action_invariants(self) -> None:
        for row in self.matrix:
            action, next_role, prompt, gate, result = (
                row["action"],
                row["next_role"],
                row["prompt"],
                row["gate"],
                row["result"],
            )
            if action == "HANDOFF":
                self.assertNotEqual(next_role, "NONE", row)
                if gate == "none":
                    # 只有 TaskBuilder → Analyst（COMPLEX / ARCHITECT）没有入口 Gate。
                    self.assertEqual(row["role"], "TASK_BUILDER", f"正常交接必须有入口 Gate: {row}")
                    self.assertEqual(next_role, "ANALYST", row)
                expected = "NONE" if next_role == "OWNER" else "REQUIRED"
                self.assertEqual(prompt, expected, row)
            elif action == "FAILURE_HANDOFF":
                self.assertNotEqual(next_role, "NONE", f"失败交接必须有接收者: {row}")
                self.assertEqual(prompt, "REQUIRED", f"跨 Role 失败必须有完整 Prompt: {row}")
            elif action == "OWNER_DECISION":
                self.assertEqual(next_role, "OWNER", row)
                self.assertEqual(prompt, "NONE", f"Owner 决策等待不得伪造执行 Prompt: {row}")
                self.assertEqual(gate, "none", row)
            elif action == "SAME_SESSION":
                self.assertEqual(prompt, "NONE", f"同 Session 不得伪装为跨 Role 交接: {row}")
            elif action == "STOP":
                self.assertEqual(prompt, "NONE", f"停止不得生成下游执行 Prompt: {row}")
                if result == "DANGER":
                    self.assertEqual(next_role, "NONE", f"DANGER 必须 NEXT_ROLE=NONE: {row}")
                    self.assertEqual(gate, "none", row)

    def test_every_enforced_role_has_required_terminals(self) -> None:
        for role in ALL_ROLES:
            rows = [r for r in self.matrix if r["role"] == role]
            actions = {r["action"] for r in rows}
            for required in {"HANDOFF", "SAME_SESSION", "FAILURE_HANDOFF", "STOP"}:
                self.assertIn(required, actions, f"{role} 缺少 {required} 终态")
            self.assertTrue(any(r["result"] == "DANGER" for r in rows), f"{role} 缺少 DANGER 终态")
            self.assertTrue(any(r["result"] == "BLOCKED" for r in rows), f"{role} 缺少 BLOCKED 终态")
            if role in OWNER_WAIT_ROLES:
                self.assertTrue(
                    any(r["action"] == "OWNER_DECISION" for r in rows),
                    f"{role} 缺少 Owner 决策检查点",
                )

    def test_architect_route_never_hands_off_to_cleaner(self) -> None:
        for row in self.matrix:
            if row["role"] == "ARCHITECT":
                self.assertNotEqual(row["next_role"], "CLEANER", f"ARCHITECT 不得交 Cleaner: {row}")
            if row["role"] == "OWNER_GATE" and row["terminal"] == "V1_CHANGES_REQUIRED":
                self.assertEqual(row["next_role"], "ARCHITECT", row)

    def test_deliverer_never_routes_cleaner_directly(self) -> None:
        for row in self.matrix:
            if row["role"] == "DELIVERER":
                self.assertNotEqual(
                    row["next_role"], "CLEANER", f"CLEAN 后变化必须由 Coder 重建 Evidence: {row}"
                )
        stale = [r for r in self.matrix if r["terminal"] == "FAIL_STALE_REVIEW"]
        self.assertEqual(len(stale), 1)
        self.assertEqual(stale[0]["next_role"], "CODER")

    def test_deliverer_pass_goes_to_owner(self) -> None:
        pass_rows = [r for r in self.matrix if r["role"] == "DELIVERER" and r["terminal"] == "PASS"]
        self.assertEqual(len(pass_rows), 1)
        self.assertEqual(pass_rows[0]["next_role"], "OWNER")
        self.assertEqual(pass_rows[0]["gate"], "merge-ready")

    def test_owner_gate_v1_v2_is_same_session(self) -> None:
        rows = [r for r in self.matrix if r["terminal"] == "V1_CLEAN"]
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["action"], "SAME_SESSION")
        self.assertEqual(rows[0]["next_role"], "OWNER_GATE")
        self.assertEqual(rows[0]["prompt"], "NONE")

    def test_role_exit_tables_match_matrix(self) -> None:
        for role, path in ALL_ROLES.items():
            expected = {
                (r["result"], r["next_role"], r["action"], r["gate"])
                for r in self.matrix
                if r["role"] == role
            }
            actual = parse_exits((ROOT / path).read_text(encoding="utf-8"), role)
            self.assertEqual(actual, expected, f"{path} 出口表与统一矩阵不一致")

    def test_role_prompts_define_standard_fields(self) -> None:
        for role, path in ALL_ROLES.items():
            text = self.role_texts[role]
            self.assertIn("WorkflowHandoffContract.md", text, f"{path} 未引用统一契约")
            self.assertIn("## 出站交接", text, f"{path} 未固定标准块标题")
            for field in FIELDS:
                self.assertIn(field, text, f"{path} 缺少字段 {field}")
            self.assertIn("块之后", text, f"{path} 未约束标准块位置")
            self.assertIn("附加", text, f"{path} 未禁止块后追加文字")
            self.assertIn(REQUIRED_GATE_BY_ROLE[role], text, f"{path} 缺少本角色 Gate")

    def test_spec_block_order_and_footer_rule(self) -> None:
        ordered = re.search(
            r"RESULT:[^\n]*\nCURRENT_ROLE:[^\n]*\nNEXT_ROLE:[^\n]*\nNEXT_ACTION:[^\n]*\n"
            r"NEXT_TARGET:[^\n]*\nHANDOFF_PROMPT:[^\n]*",
            self.spec_text,
        )
        self.assertIsNotNone(ordered, "§2.2 必须给出固定顺序的标准块")
        self.assertIn("最后一个章节", self.spec_text)
        self.assertIn("不得再附加任何文字", self.spec_text)

    def test_failure_prompt_template_has_ten_items(self) -> None:
        for phrase in [
            "接收 Role",
            "Repository / Branch / Task Path",
            "当前 Commit 与证据状态",
            "`NORMAL` / `COMPLEX` / `ARCHITECT`",
            "具体失败原因",
            "接收者应做什么",
            "允许修改的范围",
            "应验证的 Gate 与完成条件",
            "不得修改的内容",
            "完成后的下一步",
        ]:
            self.assertIn(phrase, self.spec_text, f"失败 Prompt 模板缺少：{phrase}")
        section = self.spec_text.split("## 4. 失败后跨 Role HANDOFF", 1)[1].split("## 5.", 1)[0]
        for i in range(1, 11):
            self.assertRegex(section, rf"(?m)^\s*{i}\.\s", f"模板缺少第 {i} 项")

    def test_honesty_rules_for_unknown_facts(self) -> None:
        for phrase in ["不得伪造 Commit SHA", "不得伪造任务路径", "不得伪造 Owner 授权", "不得伪造测试"]:
            self.assertIn(phrase, self.spec_text, f"缺少诚实性规则：{phrase}")
        self.assertIn("UNKNOWN", self.spec_text)
        self.assertIn("NOT_EXECUTED", self.spec_text)

    def test_no_role_prompt_invents_new_gate(self) -> None:
        for role, text in self.role_texts.items():
            found = set(re.findall(r"\bgate ([a-z][a-z0-9-]*)", text))
            self.assertTrue(found.issubset(GATES), f"{role} 引入了未知 Gate: {sorted(found - GATES)}")

    def test_no_machine_certification_claim(self) -> None:
        self.assertIn("不能", self.spec_text)
        self.assertIn("机器认证", self.spec_text)
        for role, text in self.role_texts.items():
            self.assertNotIn("Engine 已认证", text, f"{role} 不得声称 Engine 已认证交接")


if __name__ == "__main__":
    unittest.main(verbosity=2)
