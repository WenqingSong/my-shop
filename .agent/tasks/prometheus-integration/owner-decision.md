# Owner Decision

## Review Target
753228e99e386989e23e6c71757d6d31460656a6

## Core Logic
- CL-001：Prometheus 抓取配置与容器网络连通性（scrape job 的 job_name/target/scrape_interval 与 host-gateway 打通决定 target UP 与指标进入 TSDB；明确区分「静态配置校验」与「真实网络抓取验证」，metrics_path 为防御式显式声明）。
- CL-002：指标端到端对账（histogram count/sum/bucket 在请求停止、指标稳定且完成新一轮 scrape 后，按同标签、同采样时点与 /metrics 直读一致，不要求任意时刻实时相等）。

## Owner Decision
ACCEPTED

## Decision Evidence
Owner 于 2026-10-09 明确 ACCEPT，接受 review.target=753228e99e386989e23e6c71757d6d31460656a6 这一 CLEAN snapshot 的核心机制。期间 Owner 提出两点澄清并确认验证卡修正：
1. CL-002 对账须在指标稳定且完成新一轮 scrape 后进行同标签、同采样时点对账，不要求任意时刻实时相等；
2. CL-001 删除 metrics_path 不造成真实抓取失败（Prometheus 默认路径即 /metrics），须区分静态配置校验与真实网络抓取验证。

Owner 确认接受本次验证卡修正（core-logic.md），review.target 不变、CLEAN 仍有效。本次决定仅绑定 review.target=753228e 这一具体 snapshot，不泛指任务整体。
