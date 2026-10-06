package workflow

import (
	"fmt"
	"strings"
)

// 本文件解析 .agent/registry/* 的 Markdown 分配表，并实现 Generic Resource Authority 校验。
//
// 原则（INV-2）：Task State records the fact; shared develop Registry authorizes the fact.
// state.yaml.resources.reservations 只声明「需要什么」，资源是否满足由 Validator 机械验证
// shared develop 上的 Registry 权威事实；不读取 state.yaml 自证字段。
// Feature Branch 自行写 RESERVED 均不构成有效 Reservation。
//
// P2 起 Registry 的业务含义泛化：Workflow Core 不认识 migration / error code 等具体类型，
// 每个 Registry row 只抽象为 Value（第 1 列）、Owner（倒数第 3 列）、Status（倒数第 2 列）。

// RegistryEntry 是任意 Registry 分配表的一行。
// Value 是 opaque non-empty string，Workflow Core 不校验其业务格式。
type RegistryEntry struct {
	Value  string
	Owner  string
	Status string
}

// ParseRegistry 解析一个通用 Registry Markdown 表。
//
// 位置语义（与现有 .agent/registry/* 实际格式对齐）：
//   - Value  = 第 1 列；
//   - Owner  = 倒数第 3 列；
//   - Status = 倒数第 2 列。
//
// 该「从行尾向前」的定位同时兼容两种现有列布局：
//
//	5 列：| Value | Name | Owner | Status | Description |（migrations.md）
//	4 列：| Value | Owner | Status | Description |（error-codes.md）
//
// 从而无需修改任何历史 Allocation Data。表头行与分隔行被跳过。
func ParseRegistry(content string) []RegistryEntry {
	rows := parseTable(content)
	if len(rows) == 0 {
		return nil
	}
	// 第一行是表头（header），跳过。
	var entries []RegistryEntry
	for _, row := range rows[1:] {
		if len(row) < 4 {
			continue // 至少需要 Value + Owner + Status + 一列（保持倒数定位无歧义）
		}
		value := row[0]
		if value == "" {
			continue
		}
		entries = append(entries, RegistryEntry{
			Value:  value,
			Owner:  row[len(row)-3],
			Status: row[len(row)-2],
		})
	}
	return entries
}

// parseTable 把 Markdown 表格拆分为行字段（按 | 分割并 trim）。
// 保留空单元格（含行尾空 备注），以保证「从行尾向前」的 Owner/Status 定位稳定。
// 自动跳过表头分隔行（---）与非表格行。
func parseTable(content string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "|") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 2 {
			continue
		}
		// parts[0] 与 parts[len-1] 是首尾 | 产生的空串，去掉。
		cells := make([]string, 0, len(parts)-2)
		for _, p := range parts[1 : len(parts)-1] {
			cells = append(cells, strings.TrimSpace(p))
		}
		if isSeparatorRow(cells) {
			continue
		}
		rows = append(rows, cells)
	}
	return rows
}

func isSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		c = strings.TrimSpace(strings.ReplaceAll(c, ":", ""))
		if c == "" {
			continue
		}
		for _, r := range c {
			if r != '-' {
				return false
			}
		}
	}
	return true
}

// resourceActive 报告状态是否构成有效 Reservation（RESERVED 或 ACTIVE）。
func resourceActive(status string) bool {
	return status == "RESERVED" || status == "ACTIVE"
}

// checkResourceAuthority 校验 state 声明的每个 resource kind 的每个 reservation value
// 是否在对应 Registry 上由当前 Task 持有且状态有效。
// 返回 nil 表示全部满足；否则返回问题列表（业务 Gate FAIL，exit 1）。
//
// 不校验 value 的具体格式（那是 Project Policy），只做 opaque 精确匹配。
func checkResourceAuthority(task string, reservations map[string][]string, registries map[string][]RegistryEntry) []Issue {
	var issues []Issue
	check := "Resource Authority (INV-2)"

	for _, kind := range sortedKeys(reservations) {
		entries := registries[kind]
		for _, value := range reservations[kind] {
			found := false
			for _, e := range entries {
				if e.Value != value {
					continue
				}
				found = true
				switch {
				case !resourceActive(e.Status):
					issues = append(issues, Issue{
						Task:     task,
						Check:    check,
						Actual:   fmt.Sprintf("resource %s value=%s status=%s", kind, value, e.Status),
						Expected: "RESERVED 或 ACTIVE",
						Reason:   "develop Registry 中该资源状态无效，不构成有效 Reservation",
					})
				case e.Owner != task:
					issues = append(issues, Issue{
						Task:     task,
						Check:    check,
						Actual:   fmt.Sprintf("resource %s value=%s owner=%s", kind, value, e.Owner),
						Expected: task,
						Reason:   "develop Registry 中该资源拥有方与当前 Task 不一致",
					})
				}
				break
			}
			if !found {
				issues = append(issues, Issue{
					Task:     task,
					Check:    check,
					Actual:   fmt.Sprintf("resource %s value=%s 未在 develop Registry 登记", kind, value),
					Expected: "develop Registry 中存在 RESERVED/ACTIVE 记录",
					Reason:   "Feature Branch 自行声明不构成有效 Reservation",
				})
			}
		}
	}

	return issues
}
