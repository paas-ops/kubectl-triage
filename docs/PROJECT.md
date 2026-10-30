# kubectl-triage 项目文档

> **版本 v0.5.0** · 2026-10-30 · Apache-2.0 · https://github.com/paas-ops/kubectl-triage
>
> 本文档是项目的综合说明与事实源入口：定位、架构、数据契约、命令、安全、质量、里程碑与路线图。
> 详细口径以各链接文档与 ADR 为准。

## 1. 项目定位

kubectl-triage 是一个**轻量、离线优先的 Kubernetes 事故诊断 CLI**：开发者拿到一份脱敏的事故证据包，
在本地执行一条命令，得到一份**可复现、可追溯、可评审**的 Markdown/JSON 诊断报告——不依赖控制平面、
不上传生产证据、不要求账号。

产品承诺：

- **5 分钟内完成第一次本地诊断**：无账号、无登录、无云端依赖，`init` 生成可跑通示例；
- **历史事故可复盘**：证据包可存档、可分享、可回归（golden + 本地 eval）；
- **可信度来自证据**：每条结论引用证据，并给出反证、缺失说明与置信分层，而不是模型口吻；
- **默认安全**：默认零网络、只读、不读 Secret；模型与推送均为显式 opt-in。

## 2. 目标用户与典型场景

| 用户 | 场景 | 成功标准 |
| --- | --- | --- |
| 应用开发者 | 收到告警，本地只有 `kubectl` 输出和几段日志 | 不登平台即得到有证据的根因草稿 |
| 值班 SRE | 事故后复盘，需要证据与结论留档 | 一份可审阅的 Markdown/JSON 报告 |
| 平台工程师 | 评估诊断工具是否值得团队采购 | 用自己脱敏的历史事故复跑与评测 |
| 安全/合规 | 生产日志不能出域 | 离线模式运行、证据哈希可核验 |
| 企业购买者 | 团队需要事故库、评测与审计 | 从 CLI 试点升级到企业版 |

## 3. 开源版与企业版边界

**开源 CLI 免费做“一次、本地、单事故”的诊断；企业版卖“团队、评测、审计、私有化”。**

| 能力 | 开源 CLI（免费） | 企业版（CloudOps Agent） |
| --- | --- | --- |
| 单次本地诊断 / 离线规则 / 可选模型 / MD+JSON 报告 | ✅ 不限次数 | ✅ |
| 本地 eval（golden + 正式集校验） | ✅ | ✅（另有团队评测服务） |
| 团队事故库 / 检索 / 负责人 | ❌ | ✅ |
| 审计（谁在何时看了什么、模型/版本） | ❌ | ✅ |
| SSO/RBAC/多租户/私有部署/SLA | ❌ | ✅ |
| 企业推送 | ✅ 客户端（显式 opt-in） | ✅ 服务端（库/去重/评测/审计） |

详细边界与 FAQ 见 [企业版边界](enterprise.md)；企业侧 G2–G5 不进入本仓库。

## 4. 核心概念

| 概念 | 说明 |
| --- | --- |
| **事故包（bundle）** | 一个目录：`incident.yaml` + 证据（k8s/metrics/logs/changes/alert.json）；本地、可存档 |
| **证据（evidence）** | 规范化条目：id / source / observed_at / collected_at / summary / content_ref / sha256 / stale |
| **报告（report）** | `report.json`（schema_version 1.0，稳定接口）+ 由其渲染的 `report.md` |
| **假设（hypothesis）** | 最多 3 条，按置信排序；每条 ≥1 条有效证据；可带反证与缺失说明 |
| **评测集（eval set）** | 与事故包同构的用例集合 + `expected/report.json`；golden 与正式 100 例共用契约 |
| **企业推送（push）** | 显式配置的客户端：默认只发报告 + 证据哈希，可选脱敏证据包；协议 v1.0 公开 |

## 5. 系统架构

### 5.1 模块

| 模块 | 职责 |
| --- | --- |
| `cmd/kubectl-triage` | 程序入口，装配 CLI 与版本信息（支持 `go install @version` 回退） |
| `internal/cli` | cobra 命令、flag、退出码、输出与人性化错误 |
| `internal/bundle` | 事故包加载/校验/脚手架、路径穿越防护、strict/standard/off 脱敏模式 |
| `internal/redact` | 凭据模式识别与 `[REDACTED]` 清洗（导入前、推送前复用） |
| `internal/evidence` | 证据规范化：content_ref、时间、哈希、stale、时钟偏差、裁剪 |
| `internal/analyze` | 分析编排：证据抽取、规则引擎、反证/置信/缺失、报告组装 |
| `internal/analyze/rule` | 五类故障确定性规则（rule-v0.2） |
| `internal/analyze/model` | 可选模型：OpenAI-compatible/Ollama、预算/超时/重试、输出校验与中和 |
| `internal/report` | 报告结构、JSON 序列化、Markdown 渲染、schema 校验 |
| `internal/importers` | Alertmanager/kubectl/Prometheus/Loki 导入器（确定性归一化） |
| `internal/eval` | 本地评测：Top1/Top3/缺失/注入/降级评分、集合校验（`--validate`） |
| `internal/push` | 企业推送客户端：协议 1.0、幂等重试、dry-run、安全预检 |
| `internal/telemetry` | opt-in 匿名遥测：默认关闭、10 字段白名单、best-effort |
| `pkg/schema` | 公开 JSON Schema + 零依赖子集校验器 + 兼容性基线测试 |

### 5.2 数据流

```text
事故包目录 ──► bundle(Load/Validate, redact) ──► evidence(规范化/裁剪)
                                              │
                                              ├─► rule 引擎（五类规则）──► 报告（离线）
                                              └─► model(可选) ── 校验/中和 ──► 报告（增强/降级）
报告 ◄──────────────────────────────────────────┘
  ├─► report.md / report.json（本地）
  ├─► eval（与 expected 对比：Top1/Top3/缺失/注入/降级）
  └─► push（显式配置；报告+证据哈希，可选证据包）
```

## 6. 命令参考

| 命令 | 用途 | 关键 flags |
| --- | --- | --- |
| `version` | 版本/构建信息 | — |
| `init <dir>` | 生成最小可跑事故包 | — |
| `import <source> <dir>` | 导入告警/事件/指标/日志 | `--from`、`--name`、`--force` |
| `validate <dir>` | schema、时间、脱敏与证据完整性校验 | `--redact strict|standard|off` |
| `analyze <dir>` | 规则分析 + 报告 | `--offline`、`--model`、模型 4 flag、`--format`、`--out`、`--max-evidence`、`--redact`、`--fail-on-missing` |
| `report <dir>` | 重新渲染已有报告（不重新分析） | `--format`、`--out` |
| `eval <cases-dir>` | 本地评测 | `--json`、`--filter`、`--max-cases`、`--now`、`--validate`、`--min-cases`、模型 flags |
| `telemetry status|enable|disable` | opt-in 匿名遥测管理 | — |
| `push <dir>` | 推送报告到企业控制平面（显式 opt-in） | `--endpoint`、`--include-evidence`、`--max-bundle-bytes`、`--timeout`、`--dry-run`、`--out` |

### 退出码

| 码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 输入/配置错误（含 `--offline` 与 `--model` 冲突、push 配置/鉴权失败） |
| 2 | 证据不足（`--fail-on-missing`）或评测存在不通过用例 |
| 3 | 模型/网络失败已降级（报告已写出并标注 degraded） |
| 4 | 安全校验失败（发现敏感内容/越权数据） |

## 7. 数据契约

### 7.1 事故包（输入）

`incident.yaml` 必填字段：`id, alertname, severity(critical|warning|info), cluster, namespace,
workload, observed_at, collected_at, source`（RFC3339 时间）。

证据文件（可选，按需）：`alert.json`、`k8s/events.json`、`k8s/pods.yaml`、
`k8s/deployments.yaml`、`metrics/*.json`、`logs/*.log`、
`changes/rollout.json`、`runbook.md`、`notes.md`。

### 7.2 报告（输出）

顶层字段：`schema_version("1.0"), incident, generated_at, mode(offline|model), degraded, summary,
evidence[], hypotheses[≤3], missing_data[], recommended_checks[], suggested_fix, limitations[], versions{}`。

- 证据字段：`id, source, observed_at, collected_at, summary, content_ref, query?, redaction?,
  sha256, stale, skew_seconds?`；
- `content_ref` 约定：整文件 `<rel>`、JSON 条目 `<rel>#items/<n>`、日志行
  `<rel>#L<n>`；
- 每条结论必须引用存在证据；反证与支持证据不得同 id；建议均为只读、不生成可执行脚本。

### 7.3 版本与兼容

- 报告 `schema_version` 固定 `1.0`；v0.x 允许破坏性变更但需 CHANGELOG + 迁移说明；
- v1.0 起至少兼容一个大版本；新增可选字段不破坏旧消费者（策略见 [schema 契约](schema.md) 与
  [ADR-007](adr/ADR-007-v1.0准备.md)）；
- 兼容性由 `pkg/schema` 基线测试守护（`UPDATE_COMPAT=1` 显式变更 + review）。

## 8. 分析引擎

### 8.1 五类故障规则（确定性，rule-v0.2）

| 故障类 | 典型信号 | 默认置信 |
| --- | --- | --- |
| OOMKilled | `OOMKilled`、`exitCode=137`、内存 limit 相关 | high（≥2 独立来源且新鲜） |
| CrashLoopBackOff | `CrashLoopBackOff`、`Back-off restarting` | mid |
| ImagePullBackOff | `ErrImagePull`、`pull access denied`、`manifest unknown` | high |
| Pending | `FailedScheduling`、资源不足/亲和性 | mid |
| 发布回归 | `changes/` 记录 + ReplicaSet/镜像差异 | mid/low |

每个假设带：证据引用、反证（如镜像曾成功拉取、探针失败、发布早于故障窗口）、置信分层
（`high/mid/low`，stale/反证会降级）、缺失证据与下一步、只读修复建议。

### 8.2 可选模型（默认关闭）

- provider：OpenAI-compatible（`/v1/chat/completions`）/ Ollama（`/api/chat`）；
  温度 0、仅 JSON；
- 只访问配置端点、禁用重定向；证据视为不可信数据；模型输出经 schema + 引用闭合 + 文本中和；
- 预算/超时/轮次受控（默认 $1 / 60s / 3 轮）；任何运行时失败保留离线报告并标 `degraded`，
  退出码 3；配置错误退出码 1。详见 [模型模式](model.md)。

## 9. 安全与隐私

- **默认离线**：不访问集群、不访问网络；模型、push、遥测均显式 opt-in；
- **只读**：不读 Secret、不解析 kubeconfig、不执行 `exec/apply/delete`；修复建议均只读；
- **输入安全**：导入/校验/推送前扫描凭据模式；报告不回显密钥；越权/越界证据引用拒绝；
- **注入防护**：证据中“忽略指令/执行命令/外传”类文本不执行、不进入报告，模型输出二次中和；
- **遥测默认关闭**：仅 10 个白名单字段（版本/OS/架构/模式/耗时/证据条数等），绝不采集集群、
  命名空间、日志、证据正文、路径、密钥；`KUBECTL_TRIAGE_TELEMETRY=off` 强制关闭；
- **供应链**：Release 提供 sha256 + cosign keyless 签名 + CycloneDX SBOM；GHCR 镜像按 digest 签名。

详见 [安全与数据流](security.md)。

## 10. 分发与供应链

| 渠道 | 状态 |
| --- | --- |
| 源码 `go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@vX.Y.Z` | ✅ |
| Release 资产（darwin/linux × amd64/arm64 + windows/amd64） | ✅ v0.5.0 起含 Windows |
| Docker：`ghcr.io/paas-ops/kubectl-triage:vX.Y.Z`（distroless/nonroot） | ✅ v0.3.0 起 |
| Homebrew：`brew tap paas-ops/homebrew-tap && brew install kubectl-triage` | ✅ 自动同步 formula |
| 文档站：https://paas-ops.github.io/kubectl-triage/ | ✅ |
| 签名/SBOM 校验 | ✅ `checksums.txt` + `.sig/.pem` + `*.cdx.json`，`verify-release` 可复验 |

发布流程：tag push → release workflow（GoReleaser v2.15.4 + cosign + SBOM + GHCR + 发布自校验）→
`verify-release` 复验 → tap 自动同步。安装与校验见 [安装与校验](install.md)。

## 11. 质量工程

| 项 | 数值/做法（v0.5.0） |
| --- | --- |
| Go 包 | 18 个（16 个有测试，2 个无测试包为装配层） |
| 代码规模 | 约 19k 行 Go（其中测试约 10k 行） |
| golden 用例 | 12 个（五类×2 + 缺失 + 注入），严格比对 + 确定性 + 反证不相交 |
| 评测 | `eval` 评分（Top1/Top3/缺失/注入/降级）+ `--validate` 集合结构门禁 |
| CI | 4 个 workflow：ci（test/cross-build/windows-test/release-check/docker-build）、release、verify-release、pages |
| 契约门禁 | schema 基线测试、golden expected schema 校验、release 自验签 |
| 默认离线测试 | push/model/telemetry 全部 stub/httptest，不触网 |

## 12. 里程碑与版本

| 里程碑 | 版本 | 交付 | 验证 |
| --- | --- | --- | --- |
| M0 立项/骨架 | — | 仓库、license、schema v0、CLI 骨架、CI | 初始提交，CI 绿 |
| M1 离线 MVP | v0.1.0 | 契约（schema/退出码）、四导入器、脱敏、五类规则、12 golden、报告 | 13 包测试、12/12 golden、CI 5/5 |
| M2 模型/评测 | v0.2.0 | OpenAI-compatible/Ollama、预算/超时/降级、注入防护、本地 eval | 15 包测试、模型 stub E2E、eval 12/12 |
| M3 Beta 分发 | v0.3.0 | GoReleaser 产物、cosign/SBOM、GHCR、Homebrew、Pages 站点、opt-in 遥测 | Release 12 资产、verify-release 绿 |
| M3.1 加固 | — | tap 自动同步（无 PAT）、发布自验签、评测集契约 | tap sync 成功、verify-release 复验 |
| M4 企业桥 | v0.4.0 | push 客户端（协议 v1.0、幂等/重试/dry-run/证据包）、企业边界/试点/指标文档 | E2E 8 场景、Release 12 资产、verify-release |
| M5 v1.0 准备 | v0.5.0 | schema 兼容基线、Windows amd64、eval --validate | 16 测试包、windows-test、Release 14 资产 |

> 完整 ADR：契约口径（ADR-002）、M1–M4 实现口径（ADR-003–006）、v1.0 准备（ADR-007）。

## 13. 指标与运营

北极星不是 Star，而是四层漏斗：

1. **实际安装**：Release 下载 / Homebrew analytics / GHCR 拉取（去重）；
2. **激活**：opt-in `analyze` 事件（首次成功生成报告）；
3. **重复使用**：激活后 4 周内 ≥2 次成功诊断（install_id 去重）；
4. **企业试点**：合格申请 ≥10、进入试点 ≥3、转正/付费（90 天商业门槛）。

当前状态：工具链已就绪（遥测客户端、试点 issue 表单、playbook、周报模板），**真实数字依赖对外发布与运营**
（截至 v0.5.0 尚未开始外部推广）。定义与周报模板见 [指标漏斗](metrics.md)。

## 14. 路线图与下一步

| 优先级 | 事项 | 负责方 |
| --- | --- | --- |
| P0 | 建设正式 100 例盲测集（脱敏、契约校验、双人标注、基线快照） | 数据/评测 |
| P0 | Windows 实机冒烟验收（zip/哈希/插件/路径/CJK/CRLF） | Release/QA |
| P0 | v1.0 宣布条件评审（评测 + 试点 + 兼容基线） | 产品负责人 |
| P1 | 试点招募与 28 天 playbook 执行、周报漏斗 | GTM/创始人 |
| P2 | CLI `--now`、模型配置去重、push Retry-After/分片、Homebrew cask 迁移、文档站链接检查进 CI、Windows 代码签名等 | Maintainer |

完整清单、验收标准与回滚流程见 [下一步工作](next-steps.md)。

## 15. 项目结构

```text
kubectl-triage/
├── cmd/kubectl-triage/          # 程序入口
├── internal/
│   ├── cli/                     # 命令、flag、退出码
│   ├── bundle/                  # 事故包加载/校验/脚手架/脱敏
│   ├── redact/                  # 凭据识别与清洗
│   ├── evidence/                # 证据规范化/裁剪/stale/偏差
│   ├── analyze/                 # 分析编排 + rule/ + model/
│   ├── report/                  # JSON/Markdown 渲染
│   ├── importers/               # alertmanager/kubectl/prometheus/loki
│   ├── eval/                    # 本地评测与集合校验
│   ├── push/                    # 企业推送客户端
│   └── telemetry/               # opt-in 匿名遥测
├── pkg/schema/                  # 公开 schema + 校验器 + 兼容基线
├── testdata/golden/             # 12 个 golden 事故包
├── examples/oom/                # 5 分钟示例
├── docs/                        # 需求/计划/ADR/安装/安全/评测/协议…
├── site/                        # 静态文档站（GitHub Pages）
└── .github/workflows/           # ci / release / verify-release / pages
```

## 16. 文档索引

| 文档 | 内容 |
| --- | --- |
| [需求文档](需求文档.md) | 产品定位、CLI 交互、证据规范、开源/商业边界、指标与验收 |
| [拆分计划](拆分计划.md) | 仓库拆分、模块结构、12 周里程碑、WBS、发布与度量 |
| [ADR-002](adr/ADR-002-契约口径.md) ~ [ADR-007](adr/ADR-007-v1.0准备.md) | 各阶段冻结的实现口径 |
| [5 分钟快速开始](quickstart.md) | 安装、init、analyze、读报告、退出码 |
| [安装与校验](install.md) | Release 资产、sha256/cosign/SBOM、Docker、Windows、Homebrew |
| [安全与数据流](security.md) | 边界、权限清单、数据流、遥测隐私、供应链 |
| [模型模式](model.md) | Ollama/OpenAI-compatible、预算、降级与安全 |
| [本地评测](eval.md) · [评测集契约](eval-contract.md) | eval 用法、评分、正式集契约与 --validate |
| [push 协议 v1.0](push-protocol.md) · [企业版边界](enterprise.md) | 企业推送协议与开源/商业边界 |
| [试点 playbook](pilot-playbook.md) · [指标漏斗](metrics.md) | 28 天试点运营与北极星指标 |
| [schema 契约](schema.md) | 报告/incident 字段与兼容策略 |
| [下一步工作](next-steps.md) | v1.0 条件、P0/P1/P2、发布与回滚 |

## 17. 贡献、许可与安全披露

- 贡献流程：[CONTRIBUTING.md](../CONTRIBUTING.md)（DCO，无需 CLA；不提交真实客户数据/凭据）；
- 许可：Apache-2.0（[LICENSE](../LICENSE)）；
- 安全披露：请勿公开 issue，按 [SECURITY.md](../SECURITY.md) 私密上报；
- 行为准则：[CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md)。
