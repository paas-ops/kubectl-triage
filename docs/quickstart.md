# 5 分钟快速开始

目标：**5 分钟内**完成第一次本地诊断——装好 CLI、生成示例事故包、跑出带证据引用的报告。
全程不需要集群账号、不需要上传任何数据；第一次分析与真实事故诊断的形状完全一样。

> 当前为 **M3 Beta**：`init` / `import` / `validate` / `analyze` / `report` / `eval` 可用，
> 可选模型与本地 eval 已就绪；企业 `push`（M4）尚未实现。安装方式见
> [docs/install.md](install.md)，本文默认已安装 `kubectl-triage`。

## 0. 安装

最短路径（需要 Go 1.24+）：

```bash
go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@latest
kubectl-triage version
```

不用 Go 的话，按 [安装与校验](install.md) 选择 Release 二进制或 Docker；Homebrew 在 tap
建立后可用。安装完成后再继续。

## 1. 生成示例事故包

```bash
kubectl-triage init /tmp/triage-demo
```

输出示例：

```text
created incident bundle: /tmp/triage-demo
next: edit incident.yaml, add redacted evidence, then run `kubectl-triage validate .`
```

生成的结构（`init` 不会覆盖已存在的 `incident.yaml`）：

```text
/tmp/triage-demo/
├── incident.yaml          # 事故元数据（必填）
├── k8s/events.json        # kubectl get events -o json 样例
├── logs/app.log           # 已脱敏日志摘录样例
├── metrics/memory.json    # Prometheus query_range 结果样例
└── README.md
```

真实事故的包结构见[需求文档 §五（事故包规范）](需求文档.md)；也可以从现有监控/工单导出数据，
用 `import` 写入标准目录（见第 5 节）。

## 2. 校验事故包

```bash
kubectl-triage validate /tmp/triage-demo
```

输出 `OK: <id> (<cluster>/<namespace>, N evidence files)` 即通过。校验内容：

- `incident.yaml` 必填字段与 JSON Schema；
- RFC3339 时间戳；
- 证据文件的敏感内容扫描（token / 密钥类模式）。

默认 `--redact strict`：命中敏感模式时**拒绝**（退出码 4），错误信息不回显原值。
`--redact standard` 会放行并在分析前把命中内容替换为 `[REDACTED]`；`--redact off` 仅用于
调试，不建议在真实事故上使用。

## 3. 本地诊断（默认离线）

```bash
kubectl-triage analyze /tmp/triage-demo
```

输出示例：

```text
report: /tmp/triage-demo/report.md, /tmp/triage-demo/report.json
summary: 容器因内存不足被 OOMKilled，需核对 memory limit 与工作集峰值
```

默认行为：

- **完全离线**：不访问网络、不访问集群；规则是确定性的，同一输入除时间戳/版本外输出一致；
- 生成 `report.md` + `report.json`（`--format md|json|both`）；
- 证据按“来源优先级 → 观察时间 → id”确定性排序，默认上限 200 条
  （`--max-evidence`，被裁剪项写入报告 `limitations`）。

常用参数：

| 参数 | 说明 |
| --- | --- |
| `--offline` | 硬性禁止网络；与 `--model`（非 none）互斥（同时给出退出码 1） |
| `--format` | `md` / `json` / `both`（默认 both） |
| `--out` | 输出目录（写 report.json/report.md），或单格式时指定文件路径 |
| `--fail-on-missing` | 证据缺失或无候选根因时退出码 2（适合 CI） |
| `--model` / `--model-url` / `--model-name` | 可选模型模式，见 [docs/model.md](model.md) |

## 4. 读报告

先看 `report.md`，它由 `report.json` 渲染，两者内容一致：

| 区块 | 读什么 |
| --- | --- |
| 摘要 | 一句话结论与影响范围 |
| 证据时间线 | 每条证据的 id、来源、观察/采集时间、内容引用、sha256 |
| 候选根因 | 最多 3 条，含置信度、引用证据与反证；引用必须来自报告证据集 |
| 缺失证据 | 缺什么、为什么重要、下一步补什么（避免编造根因） |
| 下一步检查 | 可执行的人工核对清单（只读） |
| 修复建议 | 明确标注“只读 · 不自动执行” |
| 限制 / 版本 | 规则版本、CLI 版本、模型信息（若有） |

- `report.json` 是稳定接口（`schema_version`），可接入工单/知识库；schema 见
  [`pkg/schema/report.schema.json`](../pkg/schema/report.schema.json)。
- 只想重新渲染已有报告、不重新分析：`kubectl-triage report /tmp/triage-demo`。
- 生成报告后，`analyze` 不再修改事故包里的证据文件。

## 5. 用真实事故跑一次（可选）

从已有监控系统导出数据后导入标准目录：

```bash
# 事故元数据（可直接编辑 incident.yaml）
kubectl-triage init ./incidents/2026-10-12-checkout-oom

# 从文件导入（省略 --from 则从 stdin 读取；可选 --name / --force）
kubectl-triage import alertmanager ./incidents/2026-10-12-checkout-oom --from alert.json
kubectl-triage import kubectl      ./incidents/2026-10-12-checkout-oom --from events.json --name events
kubectl-triage import prometheus   ./incidents/2026-10-12-checkout-oom --from query.json --name memory
kubectl-triage import loki         ./incidents/2026-10-12-checkout-oom --from loki.json --name app

kubectl-triage validate ./incidents/2026-10-12-checkout-oom
kubectl-triage analyze  ./incidents/2026-10-12-checkout-oom
```

导入前请先脱敏；`import` 自身也会扫描凭据模式，命中时退出码 4 且不写入任何文件。

## 6. 可选：模型模式

默认不联网。显式启用后才调用模型，且只访问你配置的端点；失败自动降级回离线规则并退出码 3。

```bash
# 本地 Ollama（需先 ollama pull <name>）
kubectl-triage analyze /tmp/triage-demo --model ollama --model-name llama3.1

# OpenAI-compatible 网关（密钥只从环境变量读取）
export KUBECTL_TRIAGE_MODEL_KEY=...
kubectl-triage analyze /tmp/triage-demo \
  --model openai-compatible --model-url https://api.example.com --model-name gpt-4o-mini
```

预算 / 超时 / 轮次与安全边界见 [docs/model.md](model.md)；模型输出必须通过 schema 校验、
证据引用闭合与文本中和，不会外传证据包以外的 URL。

## 7. 可选：本地 eval

对仓库内 12 个 golden 事故包做确定性评测，验证安装与升级没有回归。需要先 clone 仓库，
并在仓库根目录执行（`testdata/golden` 相对仓库根目录）：

```bash
kubectl-triage eval testdata/golden --now 2026-10-30T14:00:00Z
# 全部通过退出码 0

kubectl-triage eval testdata/golden --now 2026-10-30T14:00:00Z --json
```

用法、评分口径（Top1/Top3/缺失/注入/降级）与稳定 JSON 契约见 [docs/eval.md](eval.md)。

## 退出码

### `analyze` / `report`

| 码 | 含义 | 典型处理 |
| --- | --- | --- |
| 0 | 成功，报告已生成 | 读报告 / 接入 CI |
| 1 | 输入/配置错误（路径、schema、flag 组合、模型配置） | 按错误信息修复；`--offline` 与 `--model` 不能同时给 |
| 2 | 触发 `--fail-on-missing`（缺失证据或无候选根因） | 补证据；或按报告 `missing_data` 的 next_step 处理 |
| 3 | 模型模式运行时失败，已回退离线规则 | 报告含 `degraded: true`；检查模型端点/网络，或改走离线 |
| 4 | 安全校验失败（疑似 Secret / 越权数据） | 脱敏后重试；不要用 `--redact off` 绕过 |

### `validate` / `import`

| 码 | 含义 |
| --- | --- |
| 0 | 通过 / 写入成功 |
| 1 | 输入或配置错误（文件不存在、schema 不合法、参数错误） |
| 4 | 安全校验失败（敏感内容或越权数据；不写入任何文件） |

### `eval`

| 码 | 含义 |
| --- | --- |
| 0 | 全部用例通过 |
| 1 | 输入/配置错误 |
| 2 | 存在未通过用例（含 degraded / per-case error），可用于 CI 门禁 |

## 常见问题

**第一次运行需要联网吗？**
不需要。安装阶段需要（下载二进制或 Go 模块），安装完成后默认零网络；只有显式
`--model` 才会访问你配置的端点。

**`init` 报 “already exists”？**
目录里已有 `incident.yaml`；换一个新目录，或直接编辑现有包（`init` 不覆盖文件）。

**`validate` 退出码 4 怎么办？**
事故包里可能有 token/密钥类内容。把真实凭据替换为占位符后重试；只在临时调试时考虑
`--redact standard`（分析前自动替换为 `[REDACTED]`），生产数据不要用 `--redact off`。

**`analyze` 退出码 2？**
你开了 `--fail-on-missing`，且报告缺少关键证据或无候选根因。这是 CI 门禁语义，不是程序错误；
按报告 `missing_data` 补齐证据，或去掉该 flag。

**`analyze` 退出码 3？**
模型模式运行时失败（网络/超时/非 2xx/输出不合法/超预算/超轮次），CLI 已保留离线报告并标注
`degraded: true`。检查 `--model-url`、密钥与预算；或去掉 `--model` 使用纯离线。

**报告里为什么有 “采集延迟 / 时钟偏差 / stale”？**
观察时间与采集时间分离；只有观察时间晚于采集时间 >9 秒才判为时钟偏差，正常采集延迟在
Markdown 中作信息性提示；超过 15 分钟的证据标为 stale，不能作为高置信结论的唯一依据。

**能作为 kubectl 插件用吗？**
可以。`kubectl-triage` 在 `PATH` 中即可用 `kubectl triage <子命令>`，例如
`kubectl triage analyze /tmp/triage-demo`。

**怎么关掉匿名统计？**
默认就是关闭的；如果曾执行 `telemetry enable`，用 `kubectl-triage telemetry disable`，
或设置 `KUBECTL_TRIAGE_TELEMETRY=off`。字段白名单与删除 install_id 的方法见
[docs/security.md](security.md#遥测隐私默认关闭)。

**报告能分享给同事吗？**
事故包与报告仍可能包含业务信息（日志摘录、指标、告警名等）；分享前按团队脱敏规范处理。
CLI 不会上传任何内容。

## 下一步

- 安全与合规评审：[安全与数据流](security.md)
- 模型模式：[docs/model.md](model.md) · 本地评测：[docs/eval.md](eval.md)
- 安装与供应链校验：[docs/install.md](install.md)
- 企业试点（团队事故库 / 审计 / 私有化）：[docs/pilot.md](pilot.md)
