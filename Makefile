# =============================================================================
# my-shop 统一生命周期入口
#
# 开发者 / Agent / CI 共用同一套命令管理项目的启动、停止、状态、健康、测试与清理。
# Makefile 仅做薄封装与转发，复杂逻辑位于 scripts/ 目录。
# =============================================================================

SHELL := /bin/bash
.DEFAULT_GOAL := help

SCRIPTS := scripts

.PHONY: help bootstrap up down restart status logs health test test-prometheus clean

help: ## 列出所有可用目标及说明
	@bash $(SCRIPTS)/help.sh

bootstrap: ## 一键初始化开发环境（依赖容器就绪、构建并启动应用）
	@bash $(SCRIPTS)/bootstrap.sh

up: ## 启动依赖容器并启动应用（幂等）
	@bash $(SCRIPTS)/up.sh

down: ## 停止应用与依赖容器（保留 MySQL/Redis 数据卷）
	@bash $(SCRIPTS)/down.sh

restart: ## 先停止再启动，恢复服务可用
	@bash $(SCRIPTS)/restart.sh

status: ## 查看 App / MySQL / Redis / Prometheus 运行状态
	@bash $(SCRIPTS)/status.sh

logs: ## 查看应用与依赖容器日志
	@bash $(SCRIPTS)/logs.sh

health: ## 校验 App / MySQL / Redis / Prometheus 健康状态
	@bash $(SCRIPTS)/health.sh

test: ## 执行 go vet 与 go test
	@bash $(SCRIPTS)/test.sh

test-prometheus: ## 校验 Prometheus 抓取配置与 compose 服务定义
	@bash $(SCRIPTS)/test-prometheus.sh

clean: ## 清理编译产物与临时文件（不删除源码与持久数据）
	@bash $(SCRIPTS)/clean.sh
