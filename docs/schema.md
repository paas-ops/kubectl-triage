# Schema 契约（incident / report / eval JSON）

本文是 kubectl-triage 公开数据契约的**总览与版本策略**。唯一事实源是仓库内的两份 JSON Schema：

| 文档 | Schema（唯一事实源） | 用途 |
| --- | --- | --- |
| `incident.yaml`（事故元数据） | [`pkg/schema/incident.schema.json`](../pkg/schema/incident.schema.json) | 事故包输入；`validate` / `analyze` / `import` 校验 |
| `report.json`（诊断报告） | [`pkg/schema/report.schema.json`](../pkg/schema/report.schema.json) | 报告输出；`analyze` / `report` / `eval` / `push` 校验 |

> 本文与 Schema JSON 冲突时**以 Schema JSON 为准**；本文只解释字段、版本语义与变更流程。
> 校验器实现见 `pkg/schema`（`//go:embed` 嵌入两份 Schema，无外部依赖、完全离线）。

## 校验器能力

`pkg/schema` 实现 JSON Schema 2020-12 的一个**固定子集**，只覆盖本仓库数据契约需要的关键字：

- `$ref`（本地文件引用，如 `report.incident` → `incident.schema.json`）、`type`、`required`、
  `properties`、`additionalProperties`、`items`、`enum`、`const`、`minLength`、`minimum`、
  `maxItems`、`minItems`、`format=date-time`；
- 未知关键字被忽略（符合 JSON Schema 对 annotation 的扩展规则）；
- `format=date-time` 按 RFC3339 校验；两份 Schema 都是 `additionalProperties: false`：
  **未声明的字段会被拒绝**（这影响新增字段的兼容方式，见下文）；
- 错误信息形如 `report.schema.json: $.evidence[0].content_ref: ...`（`$.` JSONPath 风格定位）。

校验入口：`ValidateIncident(`incident.yaml 转 JSON 后的字节)`、`ValidateReport`。
以下路径都会经过校验：`validate`、`analyze`（生成后二次校验）、`report`（渲染前）、
golden 测试、`eval`（期望报告）、`push`（推送前）。

## incident.yaml 字段

`incident.yaml` 是**输入契约**，没有独立的 `schema_version`；随报告契约同步演进。
校验时 YAML 先转成 JSON，再套 `incident.schema.json`。必填字段如下
（`additionalProperties: false`，不允许额外顶层字段）：

| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `id` | string | 非空 | 事故 id |
| `alertname` | string | 非空 | 告警名 |
| `severity` | string | `critical` / `warning` / `info` | 严重级别 |
| `cluster` | string | 非空 | 集群标识 |
| `namespace` | string | 非空 | 命名空间 |
| `workload` | string | 非空 | 工作负载名 |
| `observed_at` | string | RFC3339 date-time | 观察时间 |
| `collected_at` | string | RFC3339 date-time | 采集时间 |
| `source` | string | 非空 | 数据来源（如 `alertmanager`、`example`） |

```yaml
id: 2026-10-12-checkout-oom
alertname: KubePodCrashLooping
severity: critical
cluster: prod-shanghai
namespace: checkout
workload: checkout-api
observed_at: 2026-10-12T09:14:00Z
collected_at: 2026-10-12T09:20:00Z
source: alertmanager
```

事故包内的证据文件（`k8s/`、`metrics/`、`logs/`、`changes/` 等）不是 JSON Schema 的一部分，
其目录约定见[需求文档 §五](需求文档.md)；证据在报告里被规范化为 `evidence[]`。

## report.json 字段

顶层（`additionalProperties: false`，以下字段全部必填）：

| 字段 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `schema_version` | string | `const "1.0"` | 报告 schema 版本 |
| `incident` | object | `$ref incident.schema.json` | 事故元数据 |
| `generated_at` | string | RFC3339 date-time | 报告生成时间 |
| `mode` | string | `offline` / `model` | 分析模式 |
| `degraded` | boolean | — | 是否降级（模型失败等真实降级） |
| `summary` | string | — | 一句话摘要 |
| `evidence` | array | 见下 | 证据时间线 |
| `hypotheses` | array | ≤3 条，见下 | 候选根因 |
| `missing_data` | array | 见下 | 缺失证据与下一步 |
| `recommended_checks` | array<string> | — | 下一步检查建议 |
| `suggested_fix` | string | — | 只读修复建议（不自动执行） |
| `limitations` | array<string> | — | 限制与降级原因 |
| `versions` | object | 值均为 string | `cli` / `rules` / `model`（模型模式可附 `model_rounds`、`model_cost_usd`） |

`evidence[]`（`additionalProperties: false`）：

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `id` | string | ✅ | 证据 id（如 `ev-001`） |
| `source` | string | ✅ | 来源（如 `k8s.events`、`logs`） |
| `observed_at` | string | ✅ | 观察时间（RFC3339） |
| `collected_at` | string | ✅ | 采集时间（RFC3339） |
| `summary` | string | ✅ | 证据摘要（已脱敏） |
| `content_ref` | string | ✅ | 包内引用（`<rel>` / `<rel>#items/<n>` / `<rel>#L<n>`） |
| `sha256` | string | ✅ | 内容哈希（`sha256:<hex>`） |
| `stale` | boolean | ✅ | 是否过期（默认 >15 分钟） |
| `query` | string | — | 查询语句（如 `kubectl get events -n checkout -o json`） |
| `redaction` | string | — | 脱敏级别（如 `strict`） |
| `skew_seconds` | integer | — | 观察时间晚于采集时间超过 9 秒时的时钟偏差 |

`hypotheses[]`（`maxItems 3`，`additionalProperties: false`）：

| 字段 | 类型 | 必填 | 约束 |
| --- | --- | --- | --- |
| `rank` | integer | ✅ | ≥1 |
| `statement` | string | ✅ | 结论一句话 |
| `confidence` | string | ✅ | `high` / `mid` / `low` |
| `evidence_ids` | array<string> | ✅ | 至少 1 条，必须引用报告内 `evidence[].id` |
| `contradictions` | array | ✅ | 每项 `{evidence_id, note}`；不得与支持证据同 id |

`missing_data[]`：每项 `{source, reason, next_step}` 均为必填字符串。
证据不足时允许 `hypotheses: []`，但必须给出 `missing_data` 与下一步（分析器保证，见[需求文档 §六](需求文档.md)）。

完整示例见 [`examples/oom/report.json`](../examples/oom/report.json)；Markdown 由 JSON 渲染，二者内容一致，
Markdown 不是独立契约。

## schema_version 语义

- `report.schema_version` 固定为 `"1.0"`（`const`），是**唯一**的顶层 schema 版本字段；
  `versions` 对象是运行期版本快照（CLI / 规则 / 模型），**不是** schema 版本，不要用它判断兼容性；
- 主版本（`2.0`）代表不兼容变更：字段删除、枚举收紧、必填项增加、语义变更；
- 次版本新增（`1.x`）代表向后兼容的可选字段扩充；
- `incident.yaml` 没有独立版本号：它随报告契约一起演进，校验始终使用当前内嵌 Schema；
- **eval JSON 契约独立版本**：`kind="eval"`（评测结果）与 `kind="eval-set"`（评测集校验）的
  `version` 均为 `"1.0"`，与报告 `schema_version` 互不影响，破坏性变更各自升版本。

## 兼容承诺（ADR-007 D1）

| 阶段 | 承诺 |
| --- | --- |
| `v0.x` | 允许破坏性变更，但必须在 [CHANGELOG](../CHANGELOG.md) 记录并提供迁移说明 |
| `v1.0` 起 | schema **至少兼容一个大版本**：允许新增可选字段；删除字段、收紧枚举/必填、变更语义属破坏性变更，必须提升主版本（`2.0`）并提供迁移说明 |

实现注意：两份 Schema 当前使用 `additionalProperties: false`，校验器不忽略未知字段。因此新增可选字段
虽然不改变必填/语义，但**消费方需要使用包含该字段的 schema 版本**（升级 CLI/校验器）；跨版本读写时以
CHANGELOG 的迁移说明为准。兼容承诺的核心是：同一大版本内，新版本必须能读取旧版本产生的文档。

`v0.1.0`–`v0.4.0` 的报告均使用 `schema_version 1.0`；v0.x 期间未发生破坏性 schema 变更。

## 破坏性变更流程

1. **提案**：issue/ADR 说明变更类型（新增可选字段 / 破坏性变更）、影响面与迁移方案；
2. **改 Schema**：直接修改 `pkg/schema/incident.schema.json` / `report.schema.json`（这是唯一事实源）；
   如用到新关键字，先扩展 `pkg/schema` 校验器子集并补测试；
3. **跑兼容基线**：`GOCACHE=$PWD/.gocache go test ./pkg/schema` 会因基线不一致而失败——这是预期门禁；
4. **显式更新基线**（显式写模式，不要在默认模式下改基线文件）：

   ```bash
   UPDATE_COMPAT=1 GOCACHE=$PWD/.gocache go test -run TestCompatibilityBaseline ./pkg/schema
   ```

   该命令重新生成 `pkg/schema/testdata/compat-baseline.json`；人工审阅 diff（必填字段/枚举/const/
   顶层结构），确认变更类型与提案一致。基线文件是生成的，不要手改；
5. **回归**：12 个 golden 的 `expected/report.json`、`examples/` 报告必须仍能通过当前 Schema；
   若生成语义变化，同步更新 golden 并人工核对（不要直接相信实现输出）；
6. **记录**：[CHANGELOG](../CHANGELOG.md) 增加条目，标明「新增可选字段」或「破坏性变更 + 迁移说明」；
   破坏性变更提升主版本（`2.0`）；
7. **同步文档**：更新本文、受影响的契约文档（如 [评测集契约](eval-contract.md)）与 README；
8. **发布**：`v0.x` 可随次版本发布；`v1.0` 起破坏性变更只能随主版本发布。

### 兼容基线测试（ADR-007 D2）

- 基线文件 `pkg/schema/testdata/compat-baseline.json`（`TestCompatibilityBaseline`）抽取两份 Schema
  的**必填字段、枚举值、const、顶层结构、type/`$ref`/format/边界**；Schema 漂移会让
  `go test ./pkg/schema` 失败并打印结构化 diff；
- 更新基线必须显式执行 `UPDATE_COMPAT=1 GOCACHE=$PWD/.gocache go test -run TestCompatibilityBaseline ./pkg/schema`，
  并在 PR/CHANGELOG 说明变更类型；
- 校验器本身不得放宽为“只检查部分字段”：Schema 未覆盖的字段语义不在兼容承诺内；
- 12 个 golden 与 examples 报告的 schema 校验在既有测试中持续执行，作为第二道门禁。

## 消费者建议

- 直接使用 [`pkg/schema/`](../pkg/schema)（Apache-2.0）做校验，或按两份 JSON Schema 生成自己的校验代码；
- 用 `schema_version` 做兼容判定，不要用 `versions` 字符串猜测；
- 对新增可选字段保持宽容：消费方应容忍未知字段（若使用本仓库校验器，则需要对应的 Schema 版本）；
- 报告中的自由文本（摘要/假设/证据摘要）应视为不可信数据，展示时转义（尤其 Web/工单）；
- 只读承诺：`suggested_fix` 是建议，不是可执行脚本；消费者不要自动执行。

## 相关文档

- Schema 事实源：[`incident.schema.json`](../pkg/schema/incident.schema.json) ·
  [`report.schema.json`](../pkg/schema/report.schema.json)
- [需求文档 §五/§六](需求文档.md)：事故包与诊断输出契约
- [拆分计划 §5.3](拆分计划.md)：版本与兼容计划
- [ADR-002](adr/ADR-002-契约口径.md)/[ADR-003](adr/ADR-003-M1实现口径.md)：契约与 M1 实现口径
- [ADR-007](adr/ADR-007-v1.0准备.md)：schema 兼容策略、Windows、评测集校验器
- [本地评测](eval.md) · [评测集契约](eval-contract.md)：eval / eval-set JSON 契约
- [安装与校验](install.md) · [CHANGELOG](../CHANGELOG.md)
