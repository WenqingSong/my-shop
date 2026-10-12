# =============================================================================
# my-shop 统一生命周期入口
#
# 开发者 / Agent / CI 共用同一套命令管理项目的启动、停止、状态、健康、测试与清理。
# Makefile 仅做薄封装与转发，复杂逻辑位于 scripts/ 目录。
# =============================================================================

SHELL := /bin/bash
.DEFAULT_GOAL := help

SCRIPTS := scripts

.PHONY: help bootstrap init up down restart status logs health test test-prometheus test-container test-nginx test-lib test-storage clean

help: ## 列出所有可用目标及说明
	@bash $(SCRIPTS)/help.sh

bootstrap: ## 一键初始化开发环境（依赖容器就绪、构建并启动应用）
	@bash $(SCRIPTS)/bootstrap.sh

init: ## 两阶段初始化第一阶段：准备依赖容器与 .env（不要求七牛凭据、不启动后端）
	@bash $(SCRIPTS)/init.sh

up: ## 构建并启动完整容器栈（app/mysql/redis/prometheus），幂等，需 .env 已填七牛与超管配置
	@bash $(SCRIPTS)/up.sh

down: ## 停止并移除应用与依赖容器（保留 MySQL/Redis/本地存储数据卷）
	@bash $(SCRIPTS)/down.sh

restart: ## 先停止再启动，恢复服务可用
	@bash $(SCRIPTS)/restart.sh

status: ## 查看 App / MySQL / Redis / Prometheus 运行状态
	@bash $(SCRIPTS)/status.sh

logs: ## 查看应用与依赖容器日志
	@bash $(SCRIPTS)/logs.sh

health: ## 校验 App / MySQL / Redis / Prometheus 健康状态
	@bash $(SCRIPTS)/health.sh

test: ## 执行 go vet 与 go test（隔离 .env，不受本地环境变量污染）
	@bash $(SCRIPTS)/test.sh

test-prometheus: ## 校验 Prometheus 抓取配置与 compose 服务定义
	@bash $(SCRIPTS)/test-prometheus.sh

test-container: ## 校验容器化配置（Dockerfile / .dockerignore / compose / 凭据分离）
	@bash $(SCRIPTS)/test-container.sh

test-nginx: ## 校验 Nginx 反向代理配置（信任边界 / 重试边界 / DNS 再解析 / 日志关联 / 编排）
	@bash $(SCRIPTS)/test-nginx.sh

test-lib: ## 校验 lib.sh 加载语义（空值不覆盖默认值等，AC-003 回归）
	@bash $(SCRIPTS)/test-lib.sh

test-storage: ## 独立真实存储 E2E：签发→直传真实 PNG→校验 final_url→删除（需真实七牛凭据）
	@bash $(SCRIPTS)/test-storage.sh

clean: ## 清理编译产物与临时文件（不删除源码与持久数据）
	@bash $(SCRIPTS)/clean.sh
