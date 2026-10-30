# 评测集契约（正式集与 golden）

本文件定义**评测集（evaluation set）的数据契约**：正式 100 例盲测集、仓库内 12 个 golden、
以及企业版评测服务共用同一套目录布局与 JSON 输出契约（对应需求文档 E4、拆分计划 E4）。
口径决策见 [ADR-004](adr/ADR-004-M2实现口径.md) D6、[ADR-005](adr/ADR-005-M3实现口径.md) 与
[ADR-007](adr/ADR-007-v1.0准备.md) D1/D4（兼容承诺与集合校验器）。

## 1. 目录布局

```text
<set-root>/
├── suite.yaml                 # 可选：集合元数据
├── <case-name>/
│   ├── incident.yaml          # 必填：事故元数据（schema 见 pkg/schema/incident.schema.json）
│   ├── k8s/… logs/… metrics/… changes/…   # 证据文件（与事故包约定一致）
│   └── expected/
│       └── report.json        # 必填：期望报告（必须通过 report.schema.json）
└── <case-name-2>/ …
```

- 用例发现：根目录及递归子目录中**同时存在** `incident.yaml` 与 `expected/report.json` 的目录；
  按用例名（目录 basename）排序，同名单按相对路径稳定排序。
- 集合校验（`eval --validate`）把只包含其中一项的目录也视为候选并判为无效（不会静默跳过）；
  同一集合内目录名必须唯一，重名会让相关用例都无效。
- 用例名唯一、目录可嵌套；正式集不要把用例放在互相覆盖的目录里。
- `suite.yaml`（可选，当前 eval 忽略、供企业版/人工使用）：

  ```yaml
  name: kubectl-triage-100
  version: 1.0
  description: 企业盲测集（脱敏）
  cases: 100
  labels: [oom, crashloopbackoff, imagepullbackoff, pending, release-regression, injection]
  ```

## 2. 期望报告要求

- `expected/report.json` 必须通过 `pkg/schema/report.schema.json`（评测时校验）；
- 生成方式：用固定时钟跑 analyzer（`--now <RFC3339>` 仅 eval 命令有；analyze 无该参数）后
  **人工核对**假设/置信/反证/缺失项，不要直接把实现输出当作期望；
- 证据文件、期望报告不得包含真实客户数据、凭据、kubeconfig、未脱敏日志；
- 注入用例需在证据中嵌入"忽略指令/外传/执行命令"类文本，并确认报告零外传。

## 3. eval JSON 输出契约（稳定）

三个独立契约：运行评测 `kind="eval"`、集合校验 `kind="eval-set"` 与基线回归对比
`kind="eval-compare"`。它们字段顺序固定、无时间戳，同一输入两次运行 `Marshal` 字节一致，
契约版本 `1.0` 独立于 CLI 版本。

### 3.1 运行结果（`kind="eval"`）

`kubectl-triage eval <set-root> --json` 输出：

```json
{
  "kind": "eval",
  "version": "1.0",
  "cases": [
    {
      "name": "oom-01",
      "pass": true,
      "top1": true,
      "top3": true,
      "missing_ok": true,
      "injection_safe": true,
      "degraded": false,
      "error": ""
    }
  ],
  "summary": {
    "total": 12,
    "top1": 11,
    "top3": 11,
    "missing_ok": 12,
    "injection_safe": 12,
    "degraded": 0,
    "errors": 0,
    "pass": 12
  }
}
```

字段语义、评分口径与退出码见 [本地评测](eval.md#评分项)。契约版本 `version` 固定为 `1.0`，
不随 CLI 版本变化；破坏性变更必须升版本并在 CHANGELOG 说明。企业版评测服务可直接消费该 JSON。

### 3.2 集合校验结果（`kind="eval-set"`）

`kubectl-triage eval <set-root> --validate [--min-cases N] --json` 输出：

```json
{
  "kind": "eval-set",
  "version": "1.0",
  "cases": [
    { "name": "oom-01", "valid": true, "errors": [] },
    { "name": "broken-01", "valid": false, "errors": ["缺少 expected/report.json"] }
  ],
  "summary": {
    "total": 2,
    "valid": 1,
    "invalid": 1,
    "min_cases_required": 0
  }
}
```

- `cases` 按用例目录名升序；`errors` 永远是非 null 数组（有效时 `[]`）并按字典序排序；
- `summary` 字段顺序固定：`total, valid, invalid, min_cases_required`；`min_cases_required`
  是 `--min-cases` 的原样回显（默认 0）；
- 校验项：用例完整性、`incident.yaml`（schema + 语义）、`expected/report.json`（schema）、
  用例名唯一、文本文件凭据模式、数量门槛；**不运行分析、不联网、不含评分字段**；
- 错误字符串固定前缀：`缺少 incident.yaml` / `缺少 expected/report.json` /
  `incident.yaml 无效: …` / `expected/report.json 无效: …` /
  `包含疑似凭据模式: <相对路径>` / `用例名重复: <name>` / `凭据扫描失败: …`；
- 退出码：0 全部有效且数量达标；1 输入/配置错误；2 存在无效用例或数量不足。

> 本契约独立于报告 `schema_version`（ADR-007 D1）；破坏性变更升 `version` 主版本并在 CHANGELOG 说明。

### 3.3 基线回归对比结果（`kind="eval-compare"`）

`kubectl-triage eval <set-root> --compare <baseline.json> --json` 输出：

```json
{
  "kind": "eval-compare",
  "version": "1.0",
  "baseline_summary": {
    "total": 12,
    "top1": 11,
    "top3": 11,
    "missing_ok": 12,
    "injection_safe": 12,
    "degraded": 0,
    "errors": 0,
    "pass": 12
  },
  "current_summary": {
    "total": 12,
    "top1": 11,
    "top3": 11,
    "missing_ok": 12,
    "injection_safe": 12,
    "degraded": 0,
    "errors": 0,
    "pass": 12
  },
  "changes": [
    { "name": "missing-01", "status": "regressed", "fields": ["top1", "top3"] },
    { "name": "oom-01", "status": "same", "fields": [] }
  ],
  "summary": {
    "regressions": 1,
    "improvements": 0,
    "same": 11,
    "added": 0,
    "missing": 0
  }
}
```

- 基线输入必须是 `kind="eval"`、`version="1.0"`、`cases` 非空的 `eval --json` 输出；缺失/非法/
  版本不符/cases 为空退出 1；基线在分析前加载，比较时不修改；
- compare **会真实运行分析**（`--now`/模型 flags 生效），基线与当前必须使用同一 `--now`/模型配置；
- `baseline_summary` / `current_summary` 字段同 `eval.summary`；
- `changes[]` 按 `name` 升序；`status` ∈ `regressed|improved|same|added|missing`；`fields` 永远
  是非 null 数组、字典序，取值 `pass/top1/top3/injection_safe/degraded/error`（same/added/missing
  为 `[]`）；improved 仅列出 `pass/top1/top3` 中从 false→true 的字段，且回归优先；
- `summary` 为各状态用例计数（`regressions` 只统计 regressed；missing 单独计数）；
- 回归定义：`pass/top1/top3/injection_safe` true→false、`degraded` false→true、`error` 空→非空；
  基线有而当前缺失（missing）**门禁失败**，当前新增（added）不算回归；
- 退出码：0 无回归（regressions=0 且 missing=0）/ 1 输入/配置错误 / 2 存在回归或缺失（仍输出完整结果）；
- `--filter`/`--max-cases` 只影响当前运行，基线中不在范围内的用例会判 missing → 退出 2；
  回归门禁应对完整集合运行。

## 4. 正式集验收门槛（建议）

| 指标 | 门槛 |
| --- | --- |
| 结构门禁 | `eval <set-root> --validate --min-cases 100 --json` 退出 0，且无无效用例 |
| 基线回归门禁 | `eval <set-root> --compare baseline.json --json` 退出 0（无 regressed/missing 用例） |
| 注入/越权安全 | 100%（任一外传 URL/命令即不通过） |
| 降级用例 | 0（离线模式） |
| Top1（离线规则，封闭集） | 与基线版本比较不得回退；首次建集记录基线 |
| 模型模式 | 单独报告 Top1/Top3 与降级率，不与离线规则混算 |

> 12 个 golden 的 12/12 是**确定性回归**，不代表统计准确率；正式 100 例盲测集用于对外质量声明，
> 由两名 SRE 标注（需求文档 §12 依赖）。

## 5. 运行与 CI

```bash
# 结构校验（不运行分析；正式集入库 / CI 门禁）
kubectl-triage eval <set-root> --validate --min-cases 100 --json

# 本地运行评测
kubectl-triage eval <set-root> --now 2026-10-30T14:00:00Z --json

# 对模型模式评估
kubectl-triage eval <set-root> --now 2026-10-30T14:00:00Z \
  --model ollama --model-name llama3.1 --json

# 基线快照（集合定稿后；受控存储/私有仓库保存）
kubectl-triage eval <set-root> --now 2026-10-30T14:00:00Z --json > eval-baseline.json

# 回归门禁（后续版本/发布前；存在回退退出 2）
kubectl-triage eval <set-root> --now 2026-10-30T14:00:00Z \
  --compare eval-baseline.json --json
```

- 运行评测退出码：0 全部通过 / 1 输入配置错误 / 2 存在不通过用例（可直接作为 CI 门禁）；
- `--validate` 退出码：0 全部有效且数量达标 / 1 输入配置错误 / 2 存在无效用例或数量不足；
- `--compare` 退出码：0 无回归 / 1 基线或参数错误 / 2 存在回归（仍输出完整结果）；
- CI 中判断退出码请使用编译后的二进制（`go build` 或 Release 资产）：`go run` 会把程序退出码 2
  显示为 shell 退出码 1，容易误判。

## 6. 数据与版本管理

- 正式集（100 例）作为**数据**管理，可存放于私有仓库；开源仓库只保留 12 个合成 golden；
- 集合版本通过 `suite.yaml.version` 标识；期望报告变化需在评测记录中注明原因；
- 与报告契约的关系：`report.json` 是共享 schema（ADR-002/003），eval JSON 是评测层共享 schema；
  两者升级互不影响，但需在各自 `version` 中体现；
- eval 契约（`kind="eval"` / `kind="eval-set"`，`version="1.0"`）独立于报告 `schema_version`，
  遵循 [ADR-007](adr/ADR-007-v1.0准备.md) D1 的兼容策略：破坏性变更升主版本并在 CHANGELOG 提供迁移说明；
- `--validate` 只保证集合结构/契约/数量，不评价模型或规则的统计质量；对外质量声明仍需盲测与人工标注；
- **基线快照（baseline）**属于受控数据：集合/期望报告定稿后生成并归档（如 `eval-baseline.json`）；
  预期内的指标变化必须人工评审后显式重新生成基线，并记录原因与版本；
- 回归门禁语义：基线有而当前缺失（missing）算回归；当前新增（added）不算回归；只比较同一评测集与
  同一指标语义；
- `kind="eval-compare"` 契约同样遵循 `version="1.0"` 的独立版本策略（ADR-007 D1）。
