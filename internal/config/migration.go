package config

import (
	"context"
	"fmt"
	"strings"
)

// migrationDatabaseKeys 是 migration 连接数据库所需的全部配置项。
//
// 这里显式列出而不是复用 Loader.Validate 的全量注册表：migration 不需要
// JWT/七牛/Kafka/秒杀等业务配置，用全量校验会让「跑一次 DDL」被无关配置的
// 拼写错误挡住，也会给运维错误的责任归属。
var migrationDatabaseKeys = []Key{
	KeyDatabaseDefaultType,
	KeyDatabaseDefaultHost,
	KeyDatabaseDefaultPort,
	KeyDatabaseDefaultUser,
	KeyDatabaseDefaultPass,
	KeyDatabaseDefaultName,
	KeyDatabaseDefaultCharset,
	KeyDatabaseDefaultDebug,
}

// ValidateMigration 执行 migrate 子命令专用的启动前配置校验。
//
// 与应用启动（ValidateConfig → ValidateExternalEndpoints）的差异只有一处：
// 不要求 REDIS_DEFAULT_ADDRESS，因为 migration 不连接 Redis。数据库部分的要求
// 与应用完全一致——必须由环境变量显式提供、不得回退回环地址、密码必须显式注入。
//
// 该方法只读配置、不建立任何连接，必须在 sql.Open / Ping 之前调用（cmd 层保证顺序）。
// 错误信息只包含配置项路径、环境变量名与解析原因，绝不输出数据库密码。
func (l *Loader) ValidateMigration(ctx context.Context) error {
	problems := make([]string, 0, len(migrationDatabaseKeys)+1)
	for _, key := range migrationDatabaseKeys {
		if err := l.get(ctx, key); err != nil {
			problems = append(problems, err.Error())
		}
	}
	// 类型解析失败时不再追加密码校验结论，避免同一个根因产生两条互相矛盾的信息。
	if len(problems) == 0 {
		pass, err := l.String(ctx, KeyDatabaseDefaultPass)
		if err != nil {
			return err
		}
		if pass == "" {
			problems = append(problems, "未配置 database.default.pass（DATABASE_DEFAULT_PASS）：数据库密码无内置默认值，migration 必须显式注入")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf(
			"migration 配置校验失败（%d 项）：\n  - %s",
			len(problems), strings.Join(problems, "\n  - "),
		)
	}
	return l.validateStrictEndpoints(ctx, migrationStrictEndpoints, "Kubernetes 严格配置模式（migration）")
}

// ValidateMigration 使用进程级默认读取器执行 migration 前置配置校验（见 Loader.ValidateMigration）。
func ValidateMigration(ctx context.Context) error {
	return Default().ValidateMigration(ctx)
}
