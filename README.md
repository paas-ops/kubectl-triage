# kubectl-triage（暂定名）

轻量、离线优先的 Kubernetes 事故诊断 CLI。开发者在本地对历史故障数据做一次可追溯的诊断，
不依赖控制平面、不上传证据；企业版再把单次诊断升级为团队事故库、评测、审计与私有部署。

> 当前为 **M2 模型与本地评测**：`init` / `import` / `validate` / `analyze` / `report` / `eval` 可用，
> 支持五类故障确定性规则、证据引用/反证/缺失说明、可选模型（Ollama / OpenAI-compatible，
> 默认关闭，失败自动降级）与本地 eval。企业 `push`（M4）尚未实现。正式评测前不得把结果
> 作为生产决策唯一依据。

## 快速开始

```bash
# 从源码安装（Go 1.24+）
go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@latest

# 或者本地构建
go build -o kubectl-triage ./cmd/kubectl-triage

# 生成示例事故包
kubectl-triage init /tmp/triage-demo
kubectl-triage validate /tmp/triage-demo
kubectl-triage analyze /tmp/triage-demo

# 也可以作为 kubectl 插件
kubectl triage analyze /tmp/triage-demo
```

输出：

```text
/tmp/triage-demo/
├── incident.yaml
├── k8s/events.json
├── logs/app.log
├── metrics/memory.json
├── report.md
└── report.json
```

## 当前能力

| 命令 | 状态 |
| --- | --- |
| `init <dir>` | ✅ 生成最小可跑事故包 |
| `validate <dir>` | ✅ JSON Schema 校验、RFC3339 时间、敏感内容扫描（`--redact strict\|standard\|off`） |
| `analyze <dir>` | ✅ 五类故障规则分析 + Markdown/JSON 报告（`--format`/`--out`/`--max-evidence`/`--fail-on-missing`，退出码 0/1/2/3/4） |
| `report <dir>` | ✅ 从已有 `report.json` 重新渲染，不重新分析 |
| `import alertmanager/kubectl/prometheus/loki` | ✅ 导入时扫描凭据，命中退出码 4 |
| `--model`（Ollama / OpenAI-compatible） | ✅ 可选模型模式，默认关闭；预算/超时/轮次，失败降级退出码 3 |
| `eval <dir>` | ✅ 本地评测（Top1/Top3/缺失/注入/降级；`--json`、`--now` 复现 golden） |
| `push`（企业版） | ⏳ M4 |

## 安全边界

- 默认离线，不访问集群、不访问网络；模型调用仅在显式 `--model` 时发生，且只访问用户配置的端点；
- 不读取 Secret、不执行 `exec/apply/delete`；
- 导入时扫描常见凭据模式，报告不回显密钥；
- 模型输出经 schema 校验、证据引用闭合与文本中和后才进入报告；所有修复建议均为只读，不自动执行。

## 文档

- [需求文档](docs/需求文档.md)：产品定位、CLI 交互、证据规范、开源/商业边界、指标与验收标准
- [拆分计划](docs/拆分计划.md)：仓库拆分、模块结构、12 周里程碑、任务拆分、发布与度量计划
- [ADR-002 契约口径](docs/adr/ADR-002-契约口径.md)：golden 基线、CLI 参数默认值等 6 项口径决策
- [ADR-003 M1 实现口径](docs/adr/ADR-003-M1实现口径.md)：证据/content_ref、退出码、脱敏、schema 校验与 CI 门禁
- [ADR-004 M2 实现口径](docs/adr/ADR-004-M2实现口径.md)：模型 provider、预算/降级、注入防护与本地 eval
- [模型模式](docs/model.md)：Ollama / OpenAI-compatible 配置、预算、降级与安全边界
- [本地评测](docs/eval.md)：eval 用法、评分口径、JSON 契约与 CI 示例

> 规划文档引用的 `CloudOps-Agent/创业商业方案.md` 属外部商业仓库，不随本仓库发布。

## 开源与商业边界

**开源 CLI 免费做“一次、本地、单事故”的诊断；企业版卖“团队、评测、审计、私有化”。**

- 开源版：Apache-2.0，单次诊断不限次数、一次一个事故包；
- 企业版（CloudOps Agent）：团队事故库、评测、审计、SSO/RBAC、私有部署与 SLA；
- 不通过阉割单次诊断收费。

## 北极星指标

不是 GitHub Star，而是：

1. **实际安装**：Release / Homebrew / 容器拉取的去重安装数
2. **重复使用**：完成首次诊断后 4 周内再次诊断的开发者比例
3. **企业试点请求**：来自 CLI/文档/评测内容的合格试点申请数

## 开发

```bash
go test ./...
go vet ./...
go build ./...
```

CI 见 [.github/workflows/ci.yml](.github/workflows/ci.yml)。

## License

Apache-2.0。见 [LICENSE](LICENSE)。
