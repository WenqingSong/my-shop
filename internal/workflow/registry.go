package workflow

import (
	"fmt"
	"strconv"
	"strings"
)

// 本文件解析 .agent/registry/* 的 Markdown 分配表，并实现 Resource Authority 校验。
//
// 原则（INV-2）：Task State records the fact; shared develop Registry authorizes the fact.
// state.yaml.resources 只声明「需要什么」，资源是否满足由 Validator 机械验证
// shared develop 上的 Registry 权威事实；不读取 state.yaml 自证字段。
// Feature Branch 自行写 RESERVED 均不构成有效 Reservation。
//
// 资源值必须是纯机器值（如 "20261001000011"、"12000-12999"），
// 带说明的非法值（如 "12000-12999（秒杀）"）在 Resource Schema 层直接 FAIL。

// MigrationEntry 是 migrations.md 分配表的一行。
type MigrationEntry struct {
	Version string
	Title   string
	Owner   string
	Status  string
}

// ErrorCodeDomainEntry 是 error-codes.md 分配表的一行。
type ErrorCodeDomainEntry struct {
	Start  int
	End    int
	Range  string // 原始区间字符串，如 "9000-9999"
	Owner  string
	Status string
}

// Registry 是解析后的全局资源 Registry。
type Registry struct {
	Migrations   []MigrationEntry
	ErrorDomains []ErrorCodeDomainEntry
}

// parseTable 把 Markdown 表格拆分为行字段（按 | 分割并 trim）。
// 自动跳过表头分隔行（---）与空行。
func parseTable(content string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "|") {
			continue
		}
		parts := strings.Split(line, "|")
		var cells []string
		for _, p := range parts {
			c := strings.TrimSpace(p)
			if c == "" {
				continue
			}
			cells = append(cells, c)
		}
		// 跳过表头分隔行（全部由 - 或 : 组成）。
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

// ParseMigrations 解析 migrations.md 分配表（列：version | title | 拥有方 | 状态 | 备注）。
func ParseMigrations(content string) ([]MigrationEntry, error) {
	var entries []MigrationEntry
	for _, row := range parseTable(content) {
		if len(row) < 4 {
			continue
		}
		version := row[0]
		if _, err := strconv.ParseInt(version, 10, 64); err != nil {
			continue // 非数字行（如表头），跳过
		}
		entries = append(entries, MigrationEntry{
			Version: version,
			Title:   row[1],
			Owner:   row[2],
			Status:  row[3],
		})
	}
	return entries, nil
}

// ParseErrorDomains 解析 error-codes.md 分配表（列：域区间 | 拥有方 | 状态 | 备注）。
func ParseErrorDomains(content string) ([]ErrorCodeDomainEntry, error) {
	var entries []ErrorCodeDomainEntry
	for _, row := range parseTable(content) {
		if len(row) < 3 {
			continue
		}
		rangeStr := row[0]
		start, end, err := parseDomainRange(rangeStr)
		if err != nil {
			continue // 非合法区间行（如表头），跳过
		}
		entries = append(entries, ErrorCodeDomainEntry{
			Start:  start,
			End:    end,
			Range:  rangeStr,
			Owner:  row[1],
			Status: row[2],
		})
	}
	return entries, nil
}

func parseDomainRange(s string) (int, int, error) {
	idx := strings.Index(s, "-")
	if idx <= 0 {
		return 0, 0, fmt.Errorf("非法域区间 %q", s)
	}
	start, err := strconv.Atoi(strings.TrimSpace(s[:idx]))
	if err != nil {
		return 0, 0, err
	}
	end, err := strconv.Atoi(strings.TrimSpace(s[idx+1:]))
	if err != nil {
		return 0, 0, err
	}
	return start, end, nil
}

// validMigrationValue 报告迁移 version 是否为纯机器值（纯数字串）。
func validMigrationValue(v string) bool {
	if v == "" {
		return false
	}
	if _, err := strconv.ParseInt(v, 10, 64); err != nil {
		return false
	}
	return true
}

// validDomainValue 报告错误码域区间是否为纯机器值（合法 "<int>-<int>" 且 start<=end）。
func validDomainValue(d string) bool {
	start, end, err := parseDomainRange(d)
	if err != nil {
		return false
	}
	return start <= end
}

// resourceActive 报告状态是否构成有效 Reservation（RESERVED 或 ACTIVE）。
func resourceActive(status string) bool {
	return status == "RESERVED" || status == "ACTIVE"
}

// checkResourceAuthority 校验 resources 是否在 shared develop Registry 上
// 有与当前 Task 一致（owner task 一致、状态有效）的权威事实，且资源值为纯机器值。
// 返回 nil 表示全部满足。
func checkResourceAuthority(task string, state State, reg Registry) []Issue {
	var issues []Issue
	check := "Resource Authority (INV-2)"

	for _, v := range state.Resources.Migrations {
		if !validMigrationValue(v) {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Resource Schema (INV-2)",
				Actual:   v,
				Expected: "纯数字 migration version（如 20261001000011）",
				Reason:   "migration 资源值必须是纯机器值，不得含说明性文本",
			})
			continue
		}
		found := false
		for _, e := range reg.Migrations {
			if e.Version != v {
				continue
			}
			found = true
			switch {
			case !resourceActive(e.Status):
				issues = append(issues, Issue{
					Task:     task,
					Check:    check,
					Actual:   fmt.Sprintf("migration %s status=%s", v, e.Status),
					Expected: "RESERVED 或 ACTIVE",
					Reason:   "develop Registry 中该 migration 状态无效，不构成有效 Reservation",
				})
			case e.Owner != task:
				issues = append(issues, Issue{
					Task:     task,
					Check:    check,
					Actual:   fmt.Sprintf("migration %s owner=%s", v, e.Owner),
					Expected: task,
					Reason:   "develop Registry 中该 migration 拥有方与当前 Task 不一致",
				})
			}
		}
		if !found {
			issues = append(issues, Issue{
				Task:     task,
				Check:    check,
				Actual:   fmt.Sprintf("migration %s 未在 develop Registry 登记", v),
				Expected: "develop Registry 中存在 RESERVED/ACTIVE 记录",
				Reason:   "Feature Branch 自行声明不构成有效 Reservation",
			})
		}
	}

	for _, d := range state.Resources.ErrorCodeDomains {
		if !validDomainValue(d) {
			issues = append(issues, Issue{
				Task:     task,
				Check:    "Resource Schema (INV-2)",
				Actual:   d,
				Expected: "合法错误码域区间（如 12000-12999）",
				Reason:   "error_code_domains 资源值必须是纯机器值，不得含说明性文本",
			})
			continue
		}
		start, end, _ := parseDomainRange(d)
		found := false
		for _, e := range reg.ErrorDomains {
			if e.Start != start || e.End != end {
				continue
			}
			found = true
			switch {
			case !resourceActive(e.Status):
				issues = append(issues, Issue{
					Task:     task,
					Check:    check,
					Actual:   fmt.Sprintf("domain %s status=%s", d, e.Status),
					Expected: "RESERVED 或 ACTIVE",
					Reason:   "develop Registry 中该错误码域状态无效，不构成有效 Reservation",
				})
			case e.Owner != task:
				issues = append(issues, Issue{
					Task:     task,
					Check:    check,
					Actual:   fmt.Sprintf("domain %s owner=%s", d, e.Owner),
					Expected: task,
					Reason:   "develop Registry 中该错误码域拥有方与当前 Task 不一致",
				})
			}
		}
		if !found {
			issues = append(issues, Issue{
				Task:     task,
				Check:    check,
				Actual:   fmt.Sprintf("domain %s 未在 develop Registry 登记", d),
				Expected: "develop Registry 中存在 RESERVED/ACTIVE 记录",
				Reason:   "Feature Branch 自行声明不构成有效 Reservation",
			})
		}
	}

	return issues
}
