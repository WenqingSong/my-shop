# 项目接入记录（草稿）

> 由 Adoption 的 DISCOVER / RESOLVE 阶段填写，成功安装后删除或并入宿主记录。
> 不要提交未确认的猜测值。

## 1. 宿主事实

| 项目 | 值 | 来源 |
| --- | --- | --- |
| 仓库根 | <path> | `git rev-parse --show-toplevel` |
| 当前分支 | <branch> | `git branch --show-current` |
| 基线 Commit | <40-hex> | `git rev-parse HEAD` |
| 集成分支 | <branch> | Owner 确认 |
| 已有 `.agent/` | 有 / 无 | 文件检查 |

## 2. 验证命令

| 用途 | 命令 |
| --- | --- |
| 构建 | <command> |
| 单元测试 | <command> |
| 集成测试 | <command> |

## 3. 受保护路径

- <宿主业务目录、CI 配置、AGENTS.md 等>

## 4. Owner 确认

- Owner: <owner>
- 授权: <允许安装/升级的范围>
- 时间: <ISO>

## 5. 待确认

- <未解决的事实；安装前必须清零>
