package config

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/container/gvar"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/os/gcfg"
	"github.com/gogf/gf/v2/util/gconv"
)

// Source 表示配置值的实际来源，用于排查「配置没生效」与自动化断言优先级。
type Source string

const (
	// SourceEnv 表示取自环境变量（最高优先级）。
	SourceEnv Source = "env"
	// SourceFile 表示取自 YAML 配置文件。
	SourceFile Source = "file"
	// SourceDefault 表示配置文件与环境变量都不存在，使用代码默认值。
	SourceDefault Source = "default"
)

// Value 是一次配置解析的结果（含来源信息，便于诊断）。
type Value struct {
	// Spec 是该配置项的契约。
	Spec Spec
	// Source 是实际生效的来源。
	Source Source
	// Text 是来源提供的原始文本；Source==SourceDefault 时为空（默认值在 Spec 中）。
	Text string
}

// sourceLabel 返回来源的中文描述，用于错误信息。
func (v Value) sourceLabel() string {
	switch v.Source {
	case SourceEnv:
		return "环境变量"
	case SourceFile:
		return "配置文件"
	default:
		return "默认值"
	}
}

// describe 返回「配置项 + 环境变量名 + 来源」的统一前缀，错误信息必须包含它，
// 便于运维直接定位到需要修改的键或环境变量。
func (v Value) describe() string {
	return fmt.Sprintf("配置项 %s（环境变量 %s）来自%s", v.Spec.Path(), v.Spec.Env, v.sourceLabel())
}

// displayValue 返回可写入错误信息的值文本：敏感配置一律隐藏，避免密码/密钥进日志。
func (v Value) displayValue() string {
	if v.Spec.Sensitive {
		return "<敏感值已隐藏>"
	}
	return strconv.Quote(v.Text)
}

// wrongTypeError 构造「值无法按目标类型解析」的错误。
func (v Value) wrongTypeError(expected string) error {
	return fmt.Errorf("%s，需要%s，实际为 %s", v.describe(), expected, v.displayValue())
}

// emptyError 构造「环境变量/配置项已设置但为空」的错误。
//
// 空字符串是「显式存在」的值，不能被当成「未设置」而静默回退默认值：
// 否则运维把 DB 密码改成空串时会被默认值掩盖，问题被推迟到运行期。
func (v Value) emptyError(expected string) error {
	return fmt.Errorf("%s，值为空字符串，需要%s；空值不视为「未设置」，如需使用默认值请删除该环境变量", v.describe(), expected)
}

// Loader 是统一配置读取器。
//
// 生产路径使用 Default()（绑定 GoFrame 默认配置中心与进程环境变量）；
// 测试可注入隔离的 *gcfg.Config 与自定义环境变量查找函数，避免污染进程级状态。
type Loader struct {
	cfg       *gcfg.Config
	lookupEnv func(string) (string, bool)
}

// NewLoader 构造一个绑定指定配置对象与环境变量查找函数的读取器。
//
// 该构造函数是测试注入点：用临时 YAML 文件构造独立 *gcfg.Config，再配合 t.Setenv，
// 即可在真实 GoFrame gcfg 实现上验证「环境变量 > 文件 > 默认值」的优先级。
func NewLoader(cfg *gcfg.Config, lookupEnv func(string) (string, bool)) (*Loader, error) {
	if cfg == nil {
		return nil, fmt.Errorf("构造配置读取器：配置对象不能为空")
	}
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	return &Loader{cfg: cfg, lookupEnv: lookupEnv}, nil
}

// Default 返回绑定进程级 GoFrame 配置中心（g.Cfg()）与 os.LookupEnv 的读取器。
func Default() *Loader {
	return &Loader{cfg: g.Cfg(), lookupEnv: os.LookupEnv}
}

// Resolve 按「环境变量 > YAML 配置文件 > 代码默认值」解析配置项并返回来源。
//
// 文件读取使用不做键归一化的 cfg.Get：GoFrame 的 GetEffective 会把配置键里的
// "_" 替换成 "."（internal/utils.FormatCmdKey），使 flash_sale.* 等带下划线的键
// 永远读不到 YAML 值。环境变量使用契约中显式登记的名称，不做任何推导。
func (l *Loader) Resolve(ctx context.Context, key Key) (Value, error) {
	spec, ok := SpecOf(key)
	if !ok {
		return Value{}, fmt.Errorf("配置项 %s 未登记到配置契约注册表（internal/config/contract.go）", key)
	}
	// 1. 环境变量（按存在性判断，空字符串也算存在）。
	if raw, exists := l.lookupEnv(spec.Env); exists {
		return Value{Spec: spec, Source: SourceEnv, Text: raw}, nil
	}
	// 2. YAML 配置文件（不做键归一化）。
	v, err := l.cfg.Get(ctx, spec.Path())
	if err != nil {
		return Value{}, fmt.Errorf("读取配置项 %s（环境变量 %s）失败: %w", spec.Path(), spec.Env, err)
	}
	if v == nil || v.IsNil() {
		// 3. 代码默认值（由 Spec.Default 提供）。
		return Value{Spec: spec, Source: SourceDefault}, nil
	}
	text, err := textualValue(spec, v)
	if err != nil {
		return Value{}, err
	}
	return Value{Spec: spec, Source: SourceFile, Text: text}, nil
}

// textualValue 把配置文件中的值统一转成待解析文本。
//
// 字符串列表在 YAML 中是数组，这里先拼成逗号分隔文本，使文件与环境变量两条路径
// 共用同一个列表解析分支。
func textualValue(spec Spec, v *gvar.Var) (string, error) {
	if spec.Kind != KindStringList {
		return v.String(), nil
	}
	switch raw := v.Val().(type) {
	case []string:
		return strings.Join(raw, ","), nil
	case []any:
		items := make([]string, 0, len(raw))
		for _, item := range raw {
			items = append(items, gconv.String(item))
		}
		return strings.Join(items, ","), nil
	default:
		return v.String(), nil
	}
}

// String 读取字符串配置。
func (l *Loader) String(ctx context.Context, key Key) (string, error) {
	value, err := l.Resolve(ctx, key)
	if err != nil {
		return "", err
	}
	if value.Source == SourceDefault {
		def, ok := value.Spec.Default.(string)
		if !ok {
			return "", fmt.Errorf("配置契约错误：%s 的默认值类型不是 string", key)
		}
		return def, nil
	}
	return value.Text, nil
}

// Bool 读取布尔配置（strconv.ParseBool 严格解析）。
func (l *Loader) Bool(ctx context.Context, key Key) (bool, error) {
	value, err := l.Resolve(ctx, key)
	if err != nil {
		return false, err
	}
	if value.Source == SourceDefault {
		def, ok := value.Spec.Default.(bool)
		if !ok {
			return false, fmt.Errorf("配置契约错误：%s 的默认值类型不是 bool", key)
		}
		return def, nil
	}
	text := strings.TrimSpace(value.Text)
	if text == "" {
		return false, value.emptyError("布尔值（true/false）")
	}
	parsed, err := strconv.ParseBool(text)
	if err != nil {
		return false, value.wrongTypeError("布尔值（true/false）")
	}
	return parsed, nil
}

// Int 读取 int 配置。
func (l *Loader) Int(ctx context.Context, key Key) (int, error) {
	value, err := l.Resolve(ctx, key)
	if err != nil {
		return 0, err
	}
	if value.Source == SourceDefault {
		def, ok := value.Spec.Default.(int)
		if !ok {
			return 0, fmt.Errorf("配置契约错误：%s 的默认值类型不是 int", key)
		}
		return def, nil
	}
	text := strings.TrimSpace(value.Text)
	if text == "" {
		return 0, value.emptyError("整数")
	}
	parsed, err := strconv.Atoi(text)
	if err != nil {
		return 0, value.wrongTypeError("整数")
	}
	return parsed, nil
}

// Int64 读取 int64 配置。
func (l *Loader) Int64(ctx context.Context, key Key) (int64, error) {
	value, err := l.Resolve(ctx, key)
	if err != nil {
		return 0, err
	}
	if value.Source == SourceDefault {
		def, ok := value.Spec.Default.(int64)
		if !ok {
			return 0, fmt.Errorf("配置契约错误：%s 的默认值类型不是 int64", key)
		}
		return def, nil
	}
	text := strings.TrimSpace(value.Text)
	if text == "" {
		return 0, value.emptyError("整数")
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, value.wrongTypeError("整数")
	}
	return parsed, nil
}

// Duration 读取 Go duration 配置（如 200ms/5s/30s），必须大于 0。
func (l *Loader) Duration(ctx context.Context, key Key) (time.Duration, error) {
	value, err := l.Resolve(ctx, key)
	if err != nil {
		return 0, err
	}
	if value.Source == SourceDefault {
		def, ok := value.Spec.Default.(time.Duration)
		if !ok {
			return 0, fmt.Errorf("配置契约错误：%s 的默认值类型不是 time.Duration", key)
		}
		return def, nil
	}
	text := strings.TrimSpace(value.Text)
	if text == "" {
		return 0, value.emptyError("Go duration（如 5s/60s/200ms）")
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return 0, value.wrongTypeError("Go duration（如 5s/60s/200ms）")
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s，需要大于 0 的 Go duration，实际为 %s", value.describe(), parsed)
	}
	return parsed, nil
}

// Strings 读取字符串列表配置：YAML 数组按元素取值，环境变量按逗号分隔（逐项 trim、丢弃空项）。
func (l *Loader) Strings(ctx context.Context, key Key) ([]string, error) {
	value, err := l.Resolve(ctx, key)
	if err != nil {
		return nil, err
	}
	if value.Source == SourceDefault {
		def, ok := value.Spec.Default.([]string)
		if !ok {
			return nil, fmt.Errorf("配置契约错误：%s 的默认值类型不是 []string", key)
		}
		return append([]string(nil), def...), nil
	}
	text := strings.TrimSpace(value.Text)
	if text == "" {
		return []string{}, nil
	}
	parts := strings.Split(text, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		if item := strings.TrimSpace(part); item != "" {
			items = append(items, item)
		}
	}
	return items, nil
}

// get 按契约类型分派读取，供 Validate 使用。
func (l *Loader) get(ctx context.Context, key Key) error {
	switch MustSpecOf(key).Kind {
	case KindString:
		_, err := l.String(ctx, key)
		return err
	case KindBool:
		_, err := l.Bool(ctx, key)
		return err
	case KindInt:
		_, err := l.Int(ctx, key)
		return err
	case KindInt64:
		_, err := l.Int64(ctx, key)
		return err
	case KindDuration:
		_, err := l.Duration(ctx, key)
		return err
	case KindStringList:
		_, err := l.Strings(ctx, key)
		return err
	default:
		return fmt.Errorf("配置项 %s 的契约类型 %q 未实现解析分支", key, MustSpecOf(key).Kind)
	}
}

// Validate 逐项解析全部已登记配置，把非法配置一次性汇总返回。
//
// 该方法是「配置错误必须明确暴露、不静默回退」的启动期闸门：serve 在建立任何
// 数据库/Redis 连接之前调用，任一配置项非法即启动失败。错误信息只包含配置项路径、
// 环境变量名与解析原因，绝不输出敏感值。
func (l *Loader) Validate(ctx context.Context) error {
	problems := make([]string, 0, 4)
	for _, spec := range specs {
		if err := l.get(ctx, spec.Key); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("配置校验失败（%d 项）：\n  - %s", len(problems), strings.Join(problems, "\n  - "))
}

// Validate 使用进程级默认读取器校验全部配置项（见 Loader.Validate）。
func Validate(ctx context.Context) error {
	return Default().Validate(ctx)
}

// String 使用进程级默认读取器读取字符串配置。
func String(ctx context.Context, key Key) (string, error) { return Default().String(ctx, key) }

// Bool 使用进程级默认读取器读取布尔配置。
func Bool(ctx context.Context, key Key) (bool, error) { return Default().Bool(ctx, key) }

// Int 使用进程级默认读取器读取 int 配置。
func Int(ctx context.Context, key Key) (int, error) { return Default().Int(ctx, key) }

// Int64 使用进程级默认读取器读取 int64 配置。
func Int64(ctx context.Context, key Key) (int64, error) { return Default().Int64(ctx, key) }

// Duration 使用进程级默认读取器读取 duration 配置。
func Duration(ctx context.Context, key Key) (time.Duration, error) {
	return Default().Duration(ctx, key)
}

// Strings 使用进程级默认读取器读取字符串列表配置。
func Strings(ctx context.Context, key Key) ([]string, error) { return Default().Strings(ctx, key) }
