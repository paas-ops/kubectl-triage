# kubectl-triage

轻量、离线优先的 Kubernetes 事故诊断 CLI。开发者在本地对历史故障数据做一次可追溯的诊断，
不依赖控制平面、不上传证据；企业版再把单次诊断升级为团队事故库、评测、审计与私有部署。

> 当前为 **M3 Beta 分发阶段**（Beta 发布由 `v0.3.0` tag 触发）：`init` / `import` / `validate` /
> `analyze` / `report` / `eval` / `telemetry` 可用，支持五类故障确定性规则、证据引用/反证/缺失说明、
> 可选模型（默认关闭、失败自动降级）、本地 eval，以及预编译、签名、SBOM、Docker、Homebrew 的分发流水线。
> 企业 `push`（M4）尚未实现。正式评测前不得把结果作为生产决策唯一依据。
>
> 文档站（静态、可离线打开）：**https://paas-ops.github.io/kubectl-triage/** ·
> 站点源码 [site/index.html](site/index.html)（无构建步骤、无外部依赖）。

## 快速开始

```bash
# 从源码安装（Go 1.24+；Release 二进制 / Docker / Homebrew 见安装矩阵）
go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@latest

# 生成示例事故包并完成一次本地诊断
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

5 分钟完整流程（含退出码与常见问题）见 [docs/quickstart.md](docs/quickstart.md)。

## 安装矩阵

| 方式 | 命令 / 产物 | 状态 |
| --- | --- | --- |
| 源码（Go 1.24+） | `go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@latest` | ✅ 立即可用 |
| Release 二进制 | `kubectl-triage_<version>_<os>_<arch>.tar.gz`（darwin/linux × amd64/arm64） | ✅ `v0.3.0` 起 |
| Docker / GHCR | `docker run --rm ghcr.io/paas-ops/kubectl-triage:v0.3.0 version` | ✅ `v0.3.0` 起 |
| Homebrew | `brew tap paas-ops/homebrew-tap && brew install kubectl-triage` | ✅ tap 已建立（v0.3.0 formula；新版本手工同步） |

- 每个 Release 资产提供 sha256（`checksums.txt`）、cosign keyless 签名（`.sig` / `.pem`）与
  CycloneDX SBOM（`*.cdx.json`）；校验步骤见 [docs/install.md](docs/install.md)。
- Homebrew formula 目前作为 Release 资产发布，tap 自动推送需要 `HOMEBREW_TAP_GITHUB_TOKEN`；
  现状与 bootstrap 步骤见 [docs/homebrew.md](docs/homebrew.md)。
- 没有预编译产物的旧版本（v0.1.0 / v0.2.0）以源码安装为准。

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
| `telemetry status\|enable\|disable` | ✅ 匿名遥测 opt-in，默认关闭；字段白名单，绝不采集事故内容 |
| 预编译 / 签名 / SBOM / Docker / Homebrew | ✅ `v0.3.0` tag 触发 release workflow 生成；Homebrew tap 已建立（新版本手工同步） |
| `push`（企业版） | ⏳ M4 |

## 离线、安全与遥测

- **默认离线**：不访问集群、不访问网络；模型调用仅在显式 `--model` 时发生，且只访问用户配置的端点；
  `--offline` 是硬保证，与 `--model`（非 none）互斥。
- **只读**：不读取 Secret、不解析 `KUBECONFIG`、不执行 `exec/apply/delete`；所有修复建议均为只读，
  不自动执行。
- **输入安全**：导入/校验时扫描常见凭据模式，报告不回显密钥；模型输出经 schema 校验、证据引用闭合与
  文本中和后才进入报告。
- **供应链可校验**：sha256 + cosign keyless 签名 + CycloneDX SBOM；容器镜像以 digest 签名、以 nonroot 运行。
- **遥测默认关闭**：不发送任何数据；显式 `telemetry enable` 后仅上报 10 个白名单字段
  （版本 / OS / 架构 / 模式 / 耗时 / 证据条数等），绝不采集集群名、命名空间、告警名、日志或证据正文、
  模型 key、文件路径；端点由用户配置，未配置则丢弃。`KUBECTL_TRIAGE_TELEMETRY=off` 可强制关闭。

完整的数据流图、权限清单、遥测字段白名单与漏洞上报流程见 [docs/security.md](docs/security.md)。

## 文档

- 文档站：**https://paas-ops.github.io/kubectl-triage/**（静态站点，源码 [site/index.html](site/index.html)）
- [5 分钟快速开始](docs/quickstart.md)：安装、init、validate、analyze、读报告、退出码与常见问题
- [安装与校验](docs/install.md)：Release 资产、sha256 / cosign / SBOM、Docker、go install、Homebrew
- [Homebrew 现状与 tap bootstrap](docs/homebrew.md)：formula 资产、`HOMEBREW_TAP_GITHUB_TOKEN`、后续启用
- [安全与数据流](docs/security.md)：只读/网络边界、数据流图、遥测隐私、权限清单、漏洞上报
- [模型模式](docs/model.md)：Ollama / OpenAI-compatible 配置、预算、降级与安全边界
- [本地评测](docs/eval.md)：eval 用法、评分口径、JSON 契约与 CI 示例
- [企业试点](docs/pilot.md)：试点资格、28 天流程、验收口径与申请入口
- [ADR-005 M3 实现口径](docs/adr/ADR-005-M3实现口径.md)：发布产物、签名/SBOM、Docker/Homebrew、文档站、遥测
- [ADR-002 契约口径](docs/adr/ADR-002-契约口径.md) · [ADR-003 M1](docs/adr/ADR-003-M1实现口径.md) ·
  [ADR-004 M2](docs/adr/ADR-004-M2实现口径.md)
- [需求文档](docs/需求文档.md)：产品定位、CLI 交互、证据规范、开源/商业边界、指标与验收标准
- [拆分计划](docs/拆分计划.md)：仓库拆分、模块结构、12 周里程碑、任务拆分、发布与度量计划
- [CHANGELOG](CHANGELOG.md)：版本变更与迁移说明

> 规划文档引用的 `CloudOps-Agent/创业商业方案.md` 属外部商业仓库，不随本仓库发布。

## 企业试点

用你们脱敏的真实历史事故验证它：团队 ≥2 名 SRE/平台工程师、≥3 个集群、≥10 起脱敏事故，
接受 28 天试点与验收口径。申请入口：

- [提交试点申请](https://github.com/paas-ops/kubectl-triage/issues/new?template=pilot-request.yml)
  （表单字段见 [.github/ISSUE_TEMPLATE/pilot-request.yml](.github/ISSUE_TEMPLATE/pilot-request.yml)）
- [试点指南](docs/pilot.md)

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

# 本地预览文档站（纯静态，无构建步骤）
python3 -m http.server 8000 --directory site
```

CI 见 [.github/workflows/ci.yml](.github/workflows/ci.yml)；文档站由
[.github/workflows/pages.yml](.github/workflows/pages.yml) 在 `site/**` 变更时部署到 GitHub Pages
（发布工程文件 `.goreleaser.yaml`、`Dockerfile`、`.github/workflows/release.yml` 由 `v*` tag 触发）。

## License

Apache-2.0。见 [LICENSE](LICENSE)。
