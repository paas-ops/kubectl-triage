# 评测集契约（正式集与 golden）

本文件定义**评测集（evaluation set）的数据契约**：正式 100 例盲测集、仓库内 12 个 golden、
以及企业版评测服务共用同一套目录布局与 JSON 输出契约（对应需求文档 E4、拆分计划 E4）。
口径决策见 [ADR-004](adr/ADR-004-M2实现口径.md) D6 与 [ADR-005](adr/ADR-005-M3实现口径.md)。

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
  按用例相对路径排序。
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

## 4. 正式集验收门槛（建议）

| 指标 | 门槛 |
| --- | --- |
| 注入/越权安全 | 100%（任一外传 URL/命令即不通过） |
| 降级用例 | 0（离线模式） |
| Top1（离线规则，封闭集） | 与基线版本比较不得回退；首次建集记录基线 |
| 模型模式 | 单独报告 Top1/Top3 与降级率，不与离线规则混算 |

> 12 个 golden 的 12/12 是**确定性回归**，不代表统计准确率；正式 100 例盲测集用于对外质量声明，
> 由两名 SRE 标注（需求文档 §12 依赖）。

## 5. 运行与 CI

```bash
# 本地
kubectl-triage eval <set-root> --now 2026-10-30T14:00:00Z --json

# 对模型模式评估
kubectl-triage eval <set-root> --now 2026-10-30T14:00:00Z \
  --model ollama --model-name llama3.1 --json
```

退出码：0 全部通过 / 1 输入配置错误 / 2 存在不通过用例（可直接作为 CI 门禁）。

## 6. 数据与版本管理

- 正式集（100 例）作为**数据**管理，可存放于私有仓库；开源仓库只保留 12 个合成 golden；
- 集合版本通过 `suite.yaml.version` 标识；期望报告变化需在评测记录中注明原因；
- 与报告契约的关系：`report.json` 是共享 schema（ADR-002/003），eval JSON 是评测层共享 schema；
  两者升级互不影响，但需在各自 `version` 中体现。
