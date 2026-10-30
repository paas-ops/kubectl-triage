# 开源版与企业版（企业桥边界）

本文说明 kubectl-triage 的开源版（OSS，Apache-2.0）与商业版（CloudOps Agent）之间的能力边界，
以及 M4 企业桥的唯一 OSS 交付物：**`push` 客户端 + 公开协议**。口径见
[需求文档 §三](需求文档.md)、[需求文档 §2.1](需求文档.md)、[ADR-006](adr/ADR-006-M4实现口径.md) 与
[拆分计划 §4.7 Epic G](拆分计划.md)。

一句话：**开源 CLI 免费做“一次、本地、单事故”的诊断；企业版卖“团队、评测、审计、私有化”。**

## 能力对照

| 能力 | 开源 CLI（免费） | 企业版（CloudOps Agent） |
| --- | --- | --- |
| 单次本地诊断 | ✅ 不限次数，一次一个事故包 | ✅ |
| 离线规则分析 | ✅ | ✅ |
| 可选本地/自带模型 | ✅（用户自己的 key/Ollama） | ✅ + 统一模型网关 / 配额 |
| Markdown/JSON 报告 | ✅ | ✅ |
| 本地单机 eval | ✅（`eval` 命令 + 公开用例契约） | ✅ 版本对比、盲测、质量看板 |
| 企业推送客户端 | ✅ `push`（显式配置，见下） | ✅ 接收端与治理 |
| 团队事故库 / 检索 / 标签 / 负责人 | ❌ | ✅ |
| 评测集管理 / 团队评测服务 | ❌（仅本地单机） | ✅ |
| 审计（谁在何时看了什么、模型/版本） | ❌ | ✅ 不可变审计、导出 |
| SSO / RBAC / 多租户 | ❌ | ✅ |
| 私有化 / 专有网络 / 离线部署 | ❌（CLI 本身离线） | ✅ 交付与支持 |
| 工单 / 知识库 / 告警平台集成 | 基础导入 | ✅ 双向同步与治理 |
| 支持 | 社区 | 商业 SLA |

**边界原则**：开源版永远可以完成“一次完整诊断”；企业版不通过故意阉割单次诊断来收费，
而是通过团队协作、治理、评估与部署效率收费。这样既符合商业方案，也避免 open-core 反弹。

## “免费单次诊断”的定义

与[需求文档 §3.1](需求文档.md)一致：

- 免费版**不限制诊断次数**，但**一次命令只处理一个事故包**；
- 不提供账号、云端存储、团队共享、批量评测服务、审计或私有部署支持；
- 不强制遥测；匿名使用统计必须显式 opt-in（见 [安全与数据流](security.md#遥测隐私默认关闭)）；
- 模型费用由用户自己的 key / 本地模型承担；
- 企业推送是**可选客户端**，不登录、不注册也能完成全部本地诊断。

## OSS 仓库包含什么

Apache-2.0 发布，包含：

- 单二进制 CLI：`init` / `import` / `validate` / `analyze` / `report` / `eval` / `telemetry` / `push`；
- 公开的证据与报告 schema（`pkg/schema`，报告 `schema_version 1.0`）；
- 12 个 golden 事故包与本地 eval harness；
- 可选模型（Ollama / OpenAI-compatible，默认关闭、失败降级）；
- 预编译、签名、SBOM、Docker、Homebrew 分发；
- **`push` 客户端与公开协议 [`docs/push-protocol.md`](push-protocol.md)——企业服务端实现的公开契约**。

OSS **不包含**：`/v1/push` 接收端、团队事故库、评测集托管、审计、SSO/RBAC、多租户、
私有部署包、SLA 与商业支持。这些在 CloudOps Agent 仓库/商业版交付。

## 企业版包含什么

CloudOps Agent（商业版）在 OSS 协议之上提供：

- **团队事故库**：检索、标签、负责人、归档与分享；
- **评测集管理**：正式 100 例评测集、版本对比、盲测、质量看板（契约见
  [评测集契约](eval-contract.md)）；
- **审计**：谁在何时看了什么、模型与版本、可导出、不可变；
- **SSO / RBAC / 多租户**：企业身份与权限治理；
- **私有化 / 专有网络 / 离线部署**：交付、升级与 SLA；
- **集成与治理**：工单 / 知识库 / 告警平台双向同步；
- 可选统一模型网关与配额。

## 企业桥：push 客户端（OSS 侧）

`push` 把**已脱敏的本地报告**显式同步到企业控制平面，是 M4 唯一的 OSS 企业桥能力。

```bash
export KUBECTL_TRIAGE_ENTERPRISE_TOKEN=...          # 仅环境变量；不接受 flag

# 先 dry-run：构建 payload 与摘要，不联网，可 --out 落盘 JSON 供审计
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com --dry-run

# 正式推送（默认只发报告 + 证据哈希）
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com

# 显式包含脱敏证据包（tar.gz + base64）
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com --include-evidence
```

关键约束：

- **显式配置才联网**：`--endpoint` 或 `KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT`；令牌只有
  `KUBECTL_TRIAGE_ENTERPRISE_TOKEN`（不写入报告/日志/错误信息）；
- 端点必须 `https`；仅 `127.0.0.1` / `localhost` / `::1` loopback 允许 http（本地联调）；
- **默认不上传证据正文**，只发报告与证据哈希；证据包必须显式 `--include-evidence`；
- 推送前做 `bundle.Load + Validate`（strict）、报告 schema 校验与敏感模式扫描，命中退出 4、不发送；
- 失败不影响本地报告：报告已落盘，push 只读它；
- 退出码：`0` 成功；`1` 配置/鉴权/被拒绝（含 4xx）；`3` 网络/超时/429/5xx（重试耗尽）；`4` 推送前安全校验失败。

请求/响应字段、幂等、重试、证据包格式与版本演进见公开协议
[**docs/push-protocol.md**](push-protocol.md)（protocol_version `1.0`）。

## 不在本仓库的部分（G2–G5）

| ID | 能力 | 交付位置 |
| --- | --- | --- |
| G1 | `push` 客户端与公开协议 | ✅ 本仓库（OSS） |
| G2 | 企业事故库导入 / 去重 | CloudOps Agent（商业版） |
| G3 | 评测集上传 / 版本对比 | CloudOps Agent（商业版） |
| G4 | 审计事件（谁/何时/模型/版本） | CloudOps Agent（商业版） |
| G5 | 私有部署文档 / 离线包 | CloudOps Agent（商业交付） |

> 拆分计划 §4.7 的约定：G2–G5 在 CloudOps Agent 仓库实现，**不进入**本仓库；OSS 只保留
> `push` 客户端与公开协议。

## 典型旅程（需求 §2.1）

1. `kubectl triage init ./incidents/checkout-oom` 生成事故包骨架；
2. 从 Alertmanager / 工单 / `kubectl` / Prometheus / Loki 导出数据（先脱敏）；
3. `kubectl triage validate ...` 检查 schema、时间戳、敏感内容；
4. `kubectl triage analyze ...` 本地离线诊断（可选 `--model ollama`）；
5. 读 `report.md` / `report.json`，接入自己的工单或知识库；
6. **可选**：`kubectl triage push ...` 把已脱敏报告同步到企业事故库（显式配置端点和令牌）。

第 1–5 步在 OSS 内永远免费、无需登录；第 6 步是显式 opt-in 的企业能力。

## 试点与采购

- 企业试点资格、28 天流程与验收口径见 [企业试点指南](pilot.md) 与
  [试点运营手册](pilot-playbook.md)；**试点不是购买前置条件**；
- 开源版可以长期单独使用，不限次数、不强制遥测、不强制登录；
- 进入试点后由商业版提供团队化能力与支持；是否转正以试点验收为准。

## 常见问题

**OSS 能推送到自建控制平面吗？**
可以。按 [push-protocol.md](push-protocol.md) 实现 `POST /v1/push` 即可；本仓库不提供服务端实现。

**push 会“偷偷”上传证据吗？**
不会。默认只发送报告与每条证据的哈希；证据正文只在显式 `--include-evidence` 时以确定性
tar.gz + base64 上传，且内容是事故包中已脱敏的输入。

**开源版会不会被故意限制？**
不会。单次诊断永远免费完整；企业版收费点是团队协作、治理、评测与部署。

**报告 schema 两边共用吗？**
是。`report.json` `schema_version 1.0` 是公开契约（`pkg/schema`），企业版复用，便于审计与归档。

**不使用企业版会被限制或强制遥测吗？**
不会。遥测默认关闭；模型与 push 都需显式启用。

## 相关文档

- [Push 协议 1.0](push-protocol.md)：服务端实现者公开契约
- [企业试点指南](pilot.md) · [试点运营手册](pilot-playbook.md) · [指标漏斗与周报](metrics.md)
- [安全与数据流](security.md)：只读/网络边界、遥测隐私、权限清单
- [本地评测](eval.md) · [评测集契约](eval-contract.md)
- [需求文档](需求文档.md) §2.1/§3/§12 · [拆分计划](拆分计划.md) §4.7 Epic G · [ADR-006](adr/ADR-006-M4实现口径.md)
