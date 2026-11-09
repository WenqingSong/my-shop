package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gogf/gf/v2/os/gcfg"
)

// newTestLoader 用临时 YAML 文件构造隔离的 GoFrame 配置对象（真实 gcfg 文件适配器）。
//
// 不使用 g.Cfg()：进程级配置中心会被其它测试/环境变量影响，无法证明优先级。
// 返回的 Loader 绑定 os.LookupEnv，配合 t.Setenv 即可验证「环境变量 > 文件 > 默认值」。
func newTestLoader(t *testing.T, yaml string) *Loader {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("写入临时配置: %v", err)
	}
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("创建配置适配器: %v", err)
	}
	if err := adapter.SetPath(dir); err != nil {
		t.Fatalf("设置配置目录: %v", err)
	}
	adapter.SetFileName("config.yaml")
	loader, err := NewLoader(gcfg.NewWithAdapter(adapter), os.LookupEnv)
	if err != nil {
		t.Fatalf("构造配置读取器: %v", err)
	}
	return loader
}

// newRepositoryLoader 加载仓库自带的 manifest/config/config.yaml（真实文件 + 真实 gcfg）。
//
// 用于断言「注册表登记的所有路径在仓库配置文件中确实存在」与「基线默认值未被改动」。
func newRepositoryLoader(t *testing.T) *Loader {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "manifest", "config"))
	if err != nil {
		t.Fatalf("解析配置目录: %v", err)
	}
	adapter, err := gcfg.NewAdapterFile()
	if err != nil {
		t.Fatalf("创建配置适配器: %v", err)
	}
	if err := adapter.SetPath(dir); err != nil {
		t.Fatalf("设置配置目录: %v", err)
	}
	adapter.SetFileName("config.yaml")
	loader, err := NewLoader(gcfg.NewWithAdapter(adapter), os.LookupEnv)
	if err != nil {
		t.Fatalf("构造配置读取器: %v", err)
	}
	return loader
}

// isolateRegisteredEnv 取消全部已登记环境变量并在测试结束后恢复原值，
// 保证断言「YAML / 默认值」的用例不被开发机或 CI 上已有的同名变量污染。
func isolateRegisteredEnv(t *testing.T) {
	t.Helper()
	for _, spec := range Specs() {
		value, ok := os.LookupEnv(spec.Env)
		if !ok {
			continue
		}
		key, original := spec.Env, value
		t.Cleanup(func() { _ = os.Setenv(key, original) })
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("清理环境变量 %s: %v", key, err)
		}
	}
}
