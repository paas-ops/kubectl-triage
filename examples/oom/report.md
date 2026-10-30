# 事故诊断：KubePodCrashLooping

- 事故：`example-oom`
- 集群 / 命名空间：`local` / `checkout`
- 工作负载：`checkout-api`
- 观察时间：2026-10-30T06:58:49Z
- 生成时间：2026-10-30T07:00:55Z
- 模式：offline

## 摘要

容器因内存不足被 OOMKilled，需核对 memory limit 与工作集峰值

## 证据时间线

| ID | 来源 | 观察时间 | 采集时间 | 内容引用 | 摘要 |
| --- | --- | --- | --- | --- | --- |
| ev-002 | `logs` | 2026-10-30T06:58:49Z | 2026-10-30T06:58:54Z | `logs/app.log#L1` | 日志摘录 logs/app.log：2026-10-30T06:58:49Z INFO starting checkout-api |
| ev-003 | `prometheus.metrics` | 2026-10-30T06:58:51Z | 2026-10-30T06:58:54Z | `metrics/memory.json` | Prometheus 指标快照 metrics/memory.json（resultType=matrix，1 个序列，2 个采样点，最新采样 2026-10-30T06:58:51Z） |
| ev-001 | `k8s.events` | 2026-10-30T06:58:52Z | 2026-10-30T06:58:54Z | `k8s/events.json#items/0` | Warning OOMKilled: Container checkout-api was OOMKilled (exitCode=137)（Pod checkout-api-7d9f） |

### 证据引用细节

- `ev-002`：查询：loki query_range；脱敏级别：strict；sha256：sha256:fc56e1a4b24d21a27ee567f4f12575d8c13465a1bf6bc4bfcc984550c39d4202
- `ev-003`：查询：prometheus query_range；脱敏级别：strict；sha256：sha256:d7d9e150f617c8b1f4a0ce7d4ab05137a349d4261dce3ca584d9ffb123ba26fa
- `ev-001`：查询：kubectl get events -n checkout -o json；脱敏级别：strict；sha256：sha256:ed08a47b093745a08a8f8ce08c0259238f8571c42757ac6a7d39b1530fc2fed6

## 候选根因

### 1. 容器因内存不足被 OOMKilled，需核对 memory limit 与工作集峰值

- 置信：high
- 证据：`ev-001`, `ev-002`

## 缺失证据

- **changes**：事故包缺少 changes/ 发布变更记录；下一步：补充发布/镜像/commit 变更记录到 changes/rollout.json

## 下一步检查

- 核对容器 memory limit 与最近一次发布是否降低 limit
- 查看同一节点其他 Pod 的内存压力与驱逐事件
- 对比故障前 30 分钟容器 working set 与 limit

## 修复建议（只读 · 不自动执行）

只读建议：确认峰值后评估提高 memory limit 或修复内存泄漏；不自动修改工作负载。

## 限制

- 离线规则版本 rule-v0.2；未接入模型，不读取集群、不访问网络。
- 所有建议均为只读，不自动执行任何修复。
- 报告仅基于事故包内证据，未核验集群实时状态。
- 证据内容按不可执行数据处理：日志与事件中的指令性文本不会被执行或外传。

## 版本

- cli：dev
- model：none
- rules：rule-v0.2

> 本报告由离线规则生成，只读；不访问集群、不上传证据、不自动执行修复。
