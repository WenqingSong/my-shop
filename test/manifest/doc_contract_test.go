package manifest

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 本文件是 T2-06.1.4 的**文档契约**断言：文档里写的数值/枚举/文件清单必须与
// `scripts/deploy-k8s.sh` 与 `manifest/kubernetes/examples/deploy-lock/` 的真实内容一致。
//
// 为什么需要它：文档漂移不会让任何代码测试失败，但会把运维引到错误的事实上。
// 审计已经出现过两类问题——帮助文本的默认值与代码不一致（F-03）、RBAC 示例漏了
// pods（F-01）。手工核对文档无法防止再次发生，所以把三方拉到一起自动比对：
//
//	脚本常量 / usage() 文本  ==  文档中的数值与枚举
//	RBAC 清单集合            ==  文档 §13.1 列出的文件
//	脚本权限声明             ⊆   RBAC 清单授权的权限
//
// 本文件**不允许**用 t.Skip 掩盖这些断言：相关产物在基线里已经存在
// （脚本、文档、RBAC 示例）。唯一例外是 A 尚未交付的「三份清单拆分」——
// 拆分不是文档能单方面决定的事实，因此该维度按「磁盘上实际存在的文件」断言：
// 文档必须与磁盘一致，磁盘变化时文档不同步就失败。

// ---------------------------------------------------------------------------
// 通用读取与解析
// ---------------------------------------------------------------------------

// docRead 读取仓库内文件，失败直接终止当前测试。
func docRead(t *testing.T, rel string) string {
	t.Helper()
	path := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s: %v", rel, err)
	}
	return string(raw)
}

// docIntConstant 解析脚本顶部的 `NAME=<整数>` 默认值。
func docIntConstant(t *testing.T, script, name string) int {
	t.Helper()
	match := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `=([0-9]+)$`).FindStringSubmatch(script)
	if match == nil {
		t.Fatalf("scripts/deploy-k8s.sh 缺少 %s=<整数> 默认值：文档数值失去事实来源", name)
	}
	value, err := strconv.Atoi(match[1])
	if err != nil {
		t.Fatalf("解析 %s: %v", name, err)
	}
	return value
}

// docSection 截取 markdown 中 [start, end) 之间的内容；找不到就失败。
func docSection(t *testing.T, doc, start, end string) string {
	t.Helper()
	from := strings.Index(doc, start)
	if from < 0 {
		t.Fatalf("文档缺少章节 %q（文档契约的定位锚点不能消失）", start)
	}
	rest := doc[from:]
	to := strings.Index(rest, end)
	if to < 0 {
		t.Fatalf("文档章节 %q 之后缺少结束锚点 %q", start, end)
	}
	return rest[:to]
}

// docOptionDefault 从「选项行」里抽取 `（默认 <N>` 的数字。
func docOptionDefault(t *testing.T, section, flag, source string) int {
	t.Helper()
	for _, line := range strings.Split(section, "\n") {
		if !strings.Contains(line, flag+" <sec>") {
			continue
		}
		match := regexp.MustCompile(`默认\s*([0-9]+)`).FindStringSubmatch(line)
		if match == nil {
			t.Fatalf("%s 中 %s 的行没有写默认值: %q", source, flag, strings.TrimSpace(line))
		}
		value, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("解析 %s 默认值: %v", flag, err)
		}
		return value
	}
	t.Fatalf("%s 缺少 %s <sec> 选项行", source, flag)
	return 0
}

// ---------------------------------------------------------------------------
// 1. CLI 参数默认值：脚本常量 == usage() == 文档
// ---------------------------------------------------------------------------

// TestDocCLIDefaultsMatchScript 断言 6 个带数值的 CLI 参数与 3 个开关在
// usage()、docs/development/kubernetes.md §9.2、docs/design/deploy-lock.md §7.3
// 三处一致，且数值来源是脚本常量而不是文档自己的重复抄写。
func TestDocCLIDefaultsMatchScript(t *testing.T) {
	script := docRead(t, "scripts/deploy-k8s.sh")
	usage := docSection(t, script, "用法：bash scripts/deploy-k8s.sh", "while [[ $# -gt 0 ]]")
	doc := docRead(t, "docs/development/kubernetes.md")
	docOptions := docSection(t, doc, "### 9.2 命令行参数", "### 9.3")

	numeric := []struct {
		flag     string
		constant string
	}{
		{"--migration-timeout", "MIGRATION_TIMEOUT"},
		{"--rollout-timeout", "ROLLOUT_TIMEOUT"},
		{"--lock-wait-timeout", "LOCK_WAIT_TIMEOUT"},
		{"--lease-duration", "LOCK_LEASE_DURATION"},
		{"--lease-renew-interval", "LOCK_RENEW_INTERVAL"},
		{"--lease-renew-timeout", "LOCK_RENEW_REQUEST_TIMEOUT"},
	}

	// 每个参数的默认值只有一个事实来源：脚本常量。
	for _, item := range numeric {
		want := docIntConstant(t, script, item.constant)
		if got := docOptionDefault(t, usage, item.flag, "scripts/deploy-k8s.sh usage()"); got != want {
			t.Errorf("usage() 中 %s 的默认值 = %d，但 %s = %d：同一份源码里的两处文字不一致",
				item.flag, got, item.constant, want)
		}
		if got := docOptionDefault(t, docOptions, item.flag, "docs/development/kubernetes.md §9.2"); got != want {
			t.Errorf("docs/development/kubernetes.md §9.2 中 %s 的默认值 = %d，但脚本 %s = %d：文档漂移",
				item.flag, got, item.constant, want)
		}
	}

	// 非数值开关：三处都必须出现（否则文档会漏掉人工降级/上下文选择这类高危开关）。
	for _, flag := range []string{"--context", "--allow-downgrade"} {
		if !strings.Contains(usage, flag) {
			t.Errorf("usage() 缺少 %s", flag)
		}
		if !strings.Contains(docOptions, flag) {
			t.Errorf("docs/development/kubernetes.md §9.2 缺少 %s", flag)
		}
	}

	// 选项集合必须完全一致：文档多写一个不存在的选项、或漏写一个真实选项都算漂移。
	// 只比较「选项：」块，避免把用法说明里的 `git fetch --unshallow` 之类命令当参数。
	flagPattern := regexp.MustCompile(`--[a-z][a-z-]+`)
	set := func(text string) map[string]bool {
		out := map[string]bool{}
		for _, flag := range flagPattern.FindAllString(text, -1) {
			out[flag] = true
		}
		return out
	}
	usageFlags, docFlags := set(docSection(t, usage, "选项：", "租期参数必须满足")), set(docOptions)
	for _, flag := range sortedKeys(usageFlags) {
		if !docFlags[flag] {
			t.Errorf("usage() 声明了 %s，但 docs/development/kubernetes.md §9.2 没有写：运维会以为该参数不存在", flag)
		}
	}
	for _, flag := range sortedKeys(docFlags) {
		if !usageFlags[flag] {
			t.Errorf("docs/development/kubernetes.md §9.2 写了 %s，但 usage() 没有这个选项：文档描述了不存在的接口", flag)
		}
	}

	// deploy-lock.md §7.3 的「新值」列也必须跟着代码走（该表只讨论续租相关参数）。
	design := docRead(t, "docs/design/deploy-lock.md")
	designTable := docSection(t, design, "### 7.3 默认值重新评估", "取舍被明确记录")
	for _, item := range numeric {
		if item.constant != "LOCK_LEASE_DURATION" &&
			item.constant != "LOCK_RENEW_INTERVAL" &&
			item.constant != "LOCK_RENEW_REQUEST_TIMEOUT" {
			continue
		}
		want := docIntConstant(t, script, item.constant)
		pattern := regexp.MustCompile(`(?m)^\| \x60` + item.flag + `\x60 \| [^|]+ \| \*{0,2}([0-9]+)\*{0,2} \|`)
		match := pattern.FindStringSubmatch(designTable)
		if match == nil {
			t.Errorf("docs/design/deploy-lock.md §7.3 缺少 %s 的「新值」行", item.flag)
			continue
		}
		if got, _ := strconv.Atoi(match[1]); got != want {
			t.Errorf("docs/design/deploy-lock.md §7.3 中 %s 的新值 = %d，但脚本 = %d", item.flag, got, want)
		}
	}
}

// ---------------------------------------------------------------------------
// 2. 退出码枚举：usage() == 文档 == 脚本真实使用
// ---------------------------------------------------------------------------

// TestDocExitCodesMatchScript 断言退出码集合在三方之间一致：
//
//	usage() 的「退出码：」块  ==  docs/development/kubernetes.md §9.4 表格（+ 143）
//	脚本里真实出现的 die/exit == 上述集合
//
// 143 只由信号路径产生（128+15），不会出现在 usage() 里，因此显式允许。
func TestDocExitCodesMatchScript(t *testing.T) {
	script := docRead(t, "scripts/deploy-k8s.sh")
	usage := docSection(t, script, "用法：bash scripts/deploy-k8s.sh", "while [[ $# -gt 0 ]]")
	exitBlock := docSection(t, usage, "退出码：", "前置条件：")

	// 同一行里可能写多个码（`2 迁移失败；3 发布锁失败；...`），因此按「行首或分号后」取值。
	codePattern := regexp.MustCompile(`(?:^|\n|；)\s*([0-9]{1,3})\s+\S`)
	usageCodes := map[int]bool{}
	for _, match := range codePattern.FindAllStringSubmatch(exitBlock, -1) {
		code, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		usageCodes[code] = true
	}
	if len(usageCodes) < 8 {
		t.Fatalf("usage() 的退出码块只解析出 %d 个码（%v）：解析规则或帮助文本被改坏了", len(usageCodes), sortedIntKeys(usageCodes))
	}

	doc := docRead(t, "docs/development/kubernetes.md")
	docTable := docSection(t, doc, "### 9.4 退出码", "### 9.5")
	docCodes := map[int]bool{}
	for _, match := range regexp.MustCompile("(?m)^\\| \x60([0-9]{1,3})\x60").FindAllStringSubmatch(docTable, -1) {
		code, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		docCodes[code] = true
	}
	if len(docCodes) < 8 {
		t.Fatalf("docs/development/kubernetes.md §9.4 只解析出 %d 个退出码（%v）", len(docCodes), sortedIntKeys(docCodes))
	}

	// usage() 声明的每个码都必须在手册里有解释。
	for _, code := range sortedIntKeys(usageCodes) {
		if !docCodes[code] {
			t.Errorf("usage() 声明退出码 %d，但 docs/development/kubernetes.md §9.4 没有解释它", code)
		}
	}
	// 手册不能发明 usage() 里没有、且不是信号码的退出码。
	const sigtermCode = 143
	for _, code := range sortedIntKeys(docCodes) {
		if !usageCodes[code] && code != sigtermCode {
			t.Errorf("docs/development/kubernetes.md §9.4 写了退出码 %d，但 usage() 没有声明它：文档与实现不一致", code)
		}
	}

	// 脚本真实使用的码必须被 usage() 覆盖（否则运维看到一个没被文档化的退出码）。
	usedPattern := regexp.MustCompile(`\b(?:die|exit)\s+([0-9]{1,3})\b`)
	for _, match := range usedPattern.FindAllStringSubmatch(script, -1) {
		code, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		if !usageCodes[code] && code != sigtermCode {
			t.Errorf("scripts/deploy-k8s.sh 会以退出码 %d 结束，但 usage() 没有声明它", code)
		}
	}

	// `1` 的双重语义与「客户端超时 ≠ 请求未生效」是 F-04 的核心，不能被改回单义。
	for _, fragment := range []string{"APPLICATION_OUTCOME_UNKNOWN", "不等于", "可能已发出"} {
		if !strings.Contains(usage, fragment) {
			t.Errorf("usage() 的退出码说明缺少 %q：退出码 1 的「请求可能已发出、结果未知」语义被削弱", fragment)
		}
	}
}

// ---------------------------------------------------------------------------
// 3. 续租预算：从脚本常量重算，文档必须一致
// ---------------------------------------------------------------------------

// TestDocLeaseBudgetMatchesScript 用脚本里的 LOCK_* 常量重算续租预算，
// 并断言 deploy-lock.md 与 kubernetes.md 里写的算式、续租链、可接管时刻都是重算值。
func TestDocLeaseBudgetMatchesScript(t *testing.T) {
	script := docRead(t, "scripts/deploy-k8s.sh")

	duration := docIntConstant(t, script, "LOCK_LEASE_DURATION")
	interval := docIntConstant(t, script, "LOCK_RENEW_INTERVAL")
	requestTimeout := docIntConstant(t, script, "LOCK_RENEW_REQUEST_TIMEOUT")
	attempts := docIntConstant(t, script, "LOCK_RENEW_MAX_ATTEMPTS")
	backoff := docIntConstant(t, script, "LOCK_RENEW_BACKOFF_BUDGET")
	margin := docIntConstant(t, script, "LOCK_SAFETY_MARGIN")

	attemptBudget := requestTimeout + requestTimeout
	budget := interval + attempts*attemptBudget + backoff + margin
	chain := attempts*attemptBudget + backoff
	detect := interval + chain
	earliestTakeover := duration - requestTimeout

	if budget >= duration {
		t.Fatalf("脚本常量本身不满足不变量：%d 必须严格小于 %d", budget, duration)
	}
	if detect >= earliestTakeover {
		t.Fatalf("续租链最坏 %ds 不早于最早可接管时刻 %ds：文档的失锁窗口推导前提不成立", detect, earliestTakeover)
	}

	budgetLine := fmt.Sprintf("%d + %d×(%d+%d) + %d + %d = %d < %d",
		interval, attempts, requestTimeout, requestTimeout, backoff, margin, budget, duration)

	design := docRead(t, "docs/design/deploy-lock.md")
	manual := docRead(t, "docs/development/kubernetes.md")
	deployment := docRead(t, "docs/design/deployment.md")

	for name, text := range map[string]string{
		"docs/design/deploy-lock.md":     design,
		"docs/development/kubernetes.md": manual,
		"docs/design/deployment.md":      deployment,
	} {
		if !strings.Contains(text, budgetLine) {
			t.Errorf("%s 中的续租预算算式与脚本常量不一致：应包含 %q", name, budgetLine)
		}
	}

	// 失锁窗口推导必须用重算值，不能用旧常量留下的数字。
	for _, fragment := range []string{
		fmt.Sprintf("t0+%ds", detect),
		fmt.Sprintf("t0+%ds", earliestTakeover),
	} {
		if !strings.Contains(design, fragment) {
			t.Errorf("docs/design/deploy-lock.md §10.3 缺少 %q：失锁窗口推导没有随脚本常量更新", fragment)
		}
	}
	if !strings.Contains(design, fmt.Sprintf("最坏 %ds", detect)) {
		t.Errorf("docs/design/deploy-lock.md §7.3 缺少续租链最坏耗时 %ds", detect)
	}
}

// ---------------------------------------------------------------------------
// 4. RBAC 清单集合与权限：磁盘 == 文档 == 脚本声明
// ---------------------------------------------------------------------------

// docRBACDocument 是 RBAC 清单的最小解析模型（不复用 rbac_test.go 的私有类型，
// 避免两个文件互相牵制）。
type docRBACDocument struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name      string `yaml:"name"`
		Namespace string `yaml:"namespace"`
	} `yaml:"metadata"`
	RoleRef struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"roleRef"`
	Subjects []struct {
		Kind string `yaml:"kind"`
		Name string `yaml:"name"`
	} `yaml:"subjects"`
	Rules []struct {
		APIGroups     []string `yaml:"apiGroups"`
		Resources     []string `yaml:"resources"`
		ResourceNames []string `yaml:"resourceNames"`
		Verbs         []string `yaml:"verbs"`
	} `yaml:"rules"`
}

// docRBACDir 是发布锁权限示例目录（A 交付的三份清单都放在这里）。
const docRBACDir = "manifest/kubernetes/examples/deploy-lock"

// docLoadRBACDir 读取目录下所有 YAML 清单。
func docLoadRBACDir(t *testing.T) ([]string, []docRBACDocument) {
	t.Helper()
	dir := filepath.Join(repoRoot(t), filepath.FromSlash(docRBACDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取 %s: %v（发布锁必须提供最小权限示例）", docRBACDir, err)
	}
	var names []string
	var docs []docRBACDocument
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		names = append(names, entry.Name())
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("读取 %s/%s: %v", docRBACDir, entry.Name(), err)
		}
		decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
		for {
			var doc docRBACDocument
			if err := decoder.Decode(&doc); err != nil {
				break
			}
			if doc.Kind != "" {
				docs = append(docs, doc)
			}
		}
	}
	if len(names) == 0 {
		t.Fatalf("%s 下没有任何 YAML 清单", docRBACDir)
	}
	sort.Strings(names)
	return names, docs
}

// docScriptRequiredPermissions 解析脚本声明的权限表。
//
// T2-06.1.4 起脚本用统一的 REQUIRED_PERMISSIONS；基线仍是 LEASE_PERMISSION_CHECKS。
// 两者都是 `"<verb> <resource.group>"` 形式，因此解析逻辑共用，但**优先**取新名字：
// 如果 A 已经改名而文档/测试仍读旧表，契约会悄悄失效。
func docScriptRequiredPermissions(t *testing.T, script string) []string {
	t.Helper()
	for _, name := range []string{"REQUIRED_PERMISSIONS", "LEASE_PERMISSION_CHECKS"} {
		block := regexp.MustCompile(`(?s)` + name + `=\((.*?)\n\)`).FindStringSubmatch(script)
		if block == nil {
			continue
		}
		var entries []string
		for _, line := range strings.Split(block[1], "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			match := regexp.MustCompile(`^"([a-z]+) ([^"]+)"$`).FindStringSubmatch(trimmed)
			if match == nil {
				t.Fatalf("%s 里的条目无法解析: %q（期望 \"<verb> <resource.group>\"）", name, trimmed)
			}
			entries = append(entries, match[1]+" "+match[2])
		}
		if len(entries) == 0 {
			t.Fatalf("%s 解析为空：权限声明的唯一事实来源不能为空", name)
		}
		return entries
	}
	t.Fatal("scripts/deploy-k8s.sh 既没有 REQUIRED_PERMISSIONS 也没有 LEASE_PERMISSION_CHECKS")
	return nil
}

// docNormalizeResource 把脚本写法（jobs.batch / leases.coordination.k8s.io / pods）
// 归一化成 group/resource。
func docNormalizeResource(ref string) (group, resource string) {
	if idx := strings.Index(ref, "."); idx >= 0 {
		return ref[idx+1:], ref[:idx]
	}
	return "", ref
}

// TestDocRBACManifestsAndPermissions 断言：
//
//	磁盘上的清单集合  ==  文档 §13.1 列出的文件
//	脚本声明的权限    ⊆   清单授权的权限（并集）
//	namespaces        ⇒  必须是 ClusterRole（namespaced Role 无法授权集群级资源）
func TestDocRBACManifestsAndPermissions(t *testing.T) {
	files, docs := docLoadRBACDir(t)
	script := docRead(t, "scripts/deploy-k8s.sh")
	manual := docRead(t, "docs/development/kubernetes.md")
	section := docSection(t, manual, "### 13.1 授予发布锁权限", "## 14. 验证分级")
	design := docRead(t, "docs/design/deploy-lock.md")

	// 1) 文档必须点名每一个清单文件：多一份没写 = 运维漏 apply；写了一份不存在的 = 文档撒谎。
	for _, name := range files {
		want := docRBACDir + "/" + name
		if !strings.Contains(manual, want) {
			t.Errorf("docs/development/kubernetes.md 没有提到 %s：磁盘上有这份清单，文档必须说明它的用途与 apply 顺序", want)
		}
	}
	mentioned := regexp.MustCompile(regexp.QuoteMeta(docRBACDir) + `/([A-Za-z0-9._-]+\.yaml)`)
	for _, match := range mentioned.FindAllStringSubmatch(manual, -1) {
		found := false
		for _, name := range files {
			if name == match[1] {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("docs/development/kubernetes.md 引用了不存在的清单 %s/%s：按文档操作会失败", docRBACDir, match[1])
		}
	}

	// 2) 脚本声明的每一项权限都必须被清单覆盖（否则按示例授权后会在预检失败）。
	granted := map[string]bool{}
	hasNamespaceRule := false
	clusterRoleGrantsNamespaces := false
	for _, doc := range docs {
		for _, rule := range doc.Rules {
			for _, group := range rule.APIGroups {
				for _, resource := range rule.Resources {
					if resource == "namespaces" {
						hasNamespaceRule = true
						if doc.Kind == "ClusterRole" {
							clusterRoleGrantsNamespaces = true
						}
					}
					for _, verb := range rule.Verbs {
						granted[verb+" "+group+"/"+resource] = true
					}
				}
			}
		}
	}
	required := docScriptRequiredPermissions(t, script)
	for _, entry := range required {
		parts := strings.SplitN(entry, " ", 2)
		group, resource := docNormalizeResource(parts[1])
		key := parts[0] + " " + group + "/" + resource
		if !granted[key] {
			t.Errorf("脚本声明需要 %q（%s），但 %s 下的清单没有授权：按示例配置的身份会在权限预检阶段失败（退出码 3）",
				entry, key, docRBACDir)
		}
	}

	// 3) namespaces 只能由 ClusterRole 授予（Role 的作用域是单个命名空间）。
	if hasNamespaceRule && !clusterRoleGrantsNamespaces {
		t.Error("清单里出现 namespaces 但没有任何 ClusterRole 授权它：Role 无法授予集群级资源")
	}
	if hasNamespaceRule || clusterRoleGrantsNamespaces {
		for _, fragment := range []string{"ClusterRole", "namespaces"} {
			if !strings.Contains(design, fragment) && !strings.Contains(section, fragment) {
				t.Errorf("文档没有解释 %q：namespaces 为什么必须走 ClusterRole 是运维必须知道的事实", fragment)
			}
		}
	}

	// 4) 拆分后的清单必须给出适用主体与 apply 顺序（否则三份文件的先后无法复现）。
	if len(files) > 1 {
		if !regexp.MustCompile(`(?s)顺序`).MatchString(section) {
			t.Error("docs/development/kubernetes.md §13.1 没有说明多份清单的 apply 顺序")
		}
		for _, doc := range docs {
			if doc.Kind == "RoleBinding" || doc.Kind == "ClusterRoleBinding" {
				if len(doc.Subjects) == 0 {
					t.Errorf("清单 %s/%s 的 %s 没有 subjects：无法确认它绑给谁",
						docRBACDir, doc.Metadata.Name, doc.Kind)
				}
			}
		}
		if !strings.Contains(section, "ServiceAccount") {
			t.Error("docs/development/kubernetes.md §13.1 没有说明适用主体（ServiceAccount/User/Group）")
		}
	}

	// 5) 交付示例不得授予集群级通配或删除权限。
	for _, doc := range docs {
		for _, rule := range doc.Rules {
			for _, verb := range rule.Verbs {
				if verb == "*" || verb == "delete" || verb == "deletecollection" {
					t.Errorf("%s 中的 %s 授予了 %q：发布脚本永不删除资源", docRBACDir, doc.Kind, verb)
				}
			}
			for _, group := range rule.APIGroups {
				if group == "*" {
					t.Errorf("%s 中的 %s 出现通配 apiGroups: *", docRBACDir, doc.Kind)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 5. 禁止再次出现的过度声明
// ---------------------------------------------------------------------------

// TestDocDoesNotClaimZeroOverlap 阻止「零重叠」这类过度声明回到文档。
//
// 正确表述是：脚本**不再发起新请求**这一侧有界；已经发出的在途请求无法撤销，
// 跨接管的在途写入仍然可能发生（AUD-05 = NOT_CLOSED）。
func TestDocDoesNotClaimZeroOverlap(t *testing.T) {
	forbidden := regexp.MustCompile(`零重叠|无重叠|不会重叠|重叠\s*0\s*s|重叠为\s*0`)
	for _, rel := range []string{
		"docs/design/deploy-lock.md",
		"docs/development/kubernetes.md",
		"docs/design/deployment.md",
		"docs/development/migrations.md",
	} {
		text := docRead(t, rel)
		for _, line := range strings.Split(text, "\n") {
			if forbidden.MatchString(line) {
				t.Errorf("%s 出现过度声明: %q；必须改写为「不发起新请求」有界 + 在途请求无法撤销", rel, strings.TrimSpace(line))
			}
		}
	}
}

// TestDocKeepsInFlightIrreversibility 断言两份主文档都明确写了
// 「客户端超时 ≠ 请求未生效」与「已发出的请求无法撤销」。
func TestDocKeepsInFlightIrreversibility(t *testing.T) {
	for _, rel := range []string{
		"docs/design/deploy-lock.md",
		"docs/development/kubernetes.md",
	} {
		text := docRead(t, rel)
		if !strings.Contains(text, "无法撤销") {
			t.Errorf("%s 没有写明「已发出的请求无法撤销」：AUD-05 的核心事实", rel)
		}
		if !regexp.MustCompile(`超时[\s\S]{0,40}不等于|不等于[\s\S]{0,40}未生效|≠`).MatchString(text) {
			t.Errorf("%s 没有写明「客户端超时 ≠ 请求未生效」", rel)
		}
	}
}

// ---------------------------------------------------------------------------
// 6. 审计结论不得被「顺手修复」
// ---------------------------------------------------------------------------

// TestDocKeepsAuditFindingsOpen 断言 AUD-02 / AUD-05 在文档中始终是 NOT_CLOSED。
func TestDocKeepsAuditFindingsOpen(t *testing.T) {
	open := regexp.MustCompile(`AUD-02[\s\S]{0,120}NOT_CLOSED`)
	open05 := regexp.MustCompile(`AUD-05[\s\S]{0,120}NOT_CLOSED`)
	closed := regexp.MustCompile(`AUD-0[25][\s\S]{0,20}(=\s*CLOSED|已关闭|已修复)`)

	for _, rel := range []string{
		"docs/design/deploy-lock.md",
		"docs/development/kubernetes.md",
	} {
		text := docRead(t, rel)
		if !open.MatchString(text) {
			t.Errorf("%s 不再把 AUD-02 标为 NOT_CLOSED：本任务没有实现 fencing", rel)
		}
		if !open05.MatchString(text) {
			t.Errorf("%s 不再把 AUD-05 标为 NOT_CLOSED：在途写入无法撤销", rel)
		}
		if match := closed.FindString(text); match != "" {
			t.Errorf("%s 把审计项标成了已关闭: %q", rel, match)
		}
	}
}

// TestDocPreservesNotVerified 断言已有的 NOT_VERIFIED 项没有被删除，
// 并且 deploy-lock.md 的关键未验证项仍然在表里。
func TestDocPreservesNotVerified(t *testing.T) {
	for _, rel := range []string{
		"docs/design/deploy-lock.md",
		"docs/development/kubernetes.md",
		"docs/design/deployment.md",
		"docs/development/migrations.md",
	} {
		if !strings.Contains(docRead(t, rel), "NOT_VERIFIED") {
			t.Errorf("%s 的 NOT_VERIFIED 记录被删除了：未验证项不允许被静默移除", rel)
		}
	}
	design := docRead(t, "docs/design/deploy-lock.md")
	for _, fact := range []string{
		"真实 API Server 的 Lease 竞争",
		"fencing 窗口的实际大小",
	} {
		if !strings.Contains(design, fact) {
			t.Errorf("docs/design/deploy-lock.md §13 缺少历史 NOT_VERIFIED 项 %q：只允许补充，不允许删除", fact)
		}
	}
}

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(set map[int]bool) []int {
	out := make([]int, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Ints(out)
	return out
}
