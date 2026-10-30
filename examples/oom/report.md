# 事故诊断：KubePodCrashLooping

- 事故：`example-oom`
- 集群 / 命名空间：`local` / `checkout`
- 工作负载：`checkout-api`
- 生成时间：2026-10-30T06:08:23Z
- 模式：offline

## 摘要

容器因内存不足被 OOMKilled（需核对 memory limit 与工作集峰值）

## 证据时间线

| ID | 来源 | 观察时间 | 摘要 |
| --- | --- | --- | --- |
| ev-001 | `k8s/events.json` | 2026-10-30T05:58:23Z | k8s/events.json (JSON snapshot, 251 bytes) |
| ev-002 | `logs/app.log` | 2026-10-30T05:58:23Z | 2026-10-12T09:13:50Z INFO starting checkout-api |
| ev-003 | `metrics/memory.json` | 2026-10-30T05:58:23Z | metrics/memory.json (JSON snapshot, 182 bytes) |

## 候选根因

### 1. 容器因内存不足被 OOMKilled（需核对 memory limit 与工作集峰值）

- 置信：high
- 证据：ev-001, ev-002

## 下一步检查

- 核对容器 memory limit 与最近一次发布是否降低 limit
- 查看同一节点其他 Pod 的内存压力与驱逐事件
- 对比故障前 30 分钟容器 working set 与 limit

## 修复建议（只读 · 不自动执行）

只读建议：在确认峰值后评估提高 memory limit 或修复内存泄漏；不自动修改工作负载。

## 限制

- v0.0.1 离线规则仅做签名匹配，正式评测前不得作为生产决策唯一依据。
- 未接入模型；不读取集群、不访问网络。
- 所有建议均为只读，不自动执行任何修复。
