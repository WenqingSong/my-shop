package workflow

import (
	"os"
	"path/filepath"
)

// 本文件实现 TaskBuilder Gate 的共享只读辅助校验。
// GateTaskBuilderInit 主体位于 taskbuilder_gate.go，这里只放其复用的文件事实检查，
// 避免在每个 Gate 中重复拼路径与 os.ReadFile。

// fileNonEmpty 报告仓库根下 rel 文件是否存在且非空（只读，不写）。
func (v *Validator) fileNonEmpty(rel string) bool {
	b, err := os.ReadFile(filepath.Join(v.Root, rel))
	return err == nil && len(b) > 0
}
