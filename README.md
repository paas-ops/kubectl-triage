# kubectl-triage（暂定名）

轻量、离线优先的 Kubernetes 事故诊断 CLI。开发者在本地对历史故障数据做一次可追溯的诊断，
不依赖控制平面、不上传证据；企业版再把单次诊断升级为团队事故库、评测、审计与私有部署。

> 当前为 **v0.0.1 初始化骨架**：`init` / `validate` / `analyze --offline` 可运行，规则覆盖
> OOMKilled、ImagePullBackOff、CrashLoopBackOff、Pending 的签名匹配。正式评测前不得把结果
> 作为生产决策唯一依据。

## 快速开始

```bash
# 从源码安装（Go 1.24+）
go install github.com/bavocado/kubectl-triage/cmd/kubectl-triage@latest

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
| `validate <dir>` | ✅ schema/时间戳/敏感内容扫描 |
| `analyze <dir> --offline` | ✅ 规则分析 + Markdown/JSON 报告 |
| `--model` / Ollama | ⏳ M2 |
| `import alertmanager/kubectl/prometheus/loki` | ⏳ M1 |
| `eval` | ⏳ M2 |
| `push`（企业版） | ⏳ M4 |

## 安全边界

- 默认离线，不访问集群、不访问网络；
- 不读取 Secret、不执行 `exec/apply/delete`；
- 导入时扫描常见凭据模式，报告不回显密钥；
- 所有修复建议均为只读，不自动执行。

## 文档

- [需求文档](docs/需求文档.md)：产品定位、CLI 交互、证据规范、开源/商业边界、指标与验收标准
- [拆分计划](docs/拆分计划.md)：仓库拆分、模块结构、12 周里程碑、任务拆分、发布与度量计划

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
