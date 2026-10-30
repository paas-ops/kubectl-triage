# 安全与数据流

本文是 kubectl-triage 的安全边界、数据流、权限清单与遥测隐私说明，面向安全/合规评审。
实现口径见 [ADR-002](adr/ADR-002-契约口径.md)、[ADR-003](adr/ADR-003-M1实现口径.md)、
[ADR-004](adr/ADR-004-M2实现口径.md)、[ADR-005](adr/ADR-005-M3实现口径.md)；漏洞上报见
[SECURITY.md](../SECURITY.md)。

一句话：**默认零网络、只读、不上传证据；模型、企业 push 与遥测都是显式 opt-in。**

## 安全边界

| 维度 | 承诺 | 实现方式 |
| --- | --- | --- |
| 数据出域 | 默认不发出任何数据 | 不传 `--model` 时不创建任何 outbound 请求；`--offline` 是硬保证 |
| 模型联网 | 只在显式 `--model` 时发生 | 只访问 `--model-url` 配置的端点；禁用 HTTP 重定向；不访问证据中的 URL |
| 企业推送 | 只在显式配置时联网 | 需 `--endpoint`（或 `KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT`）+ `KUBECTL_TRIAGE_ENTERPRISE_TOKEN`；只访问配置端点；TLS 强校验、禁用重定向；默认只发报告+证据哈希，`--include-evidence` 才上传脱敏证据包；推送前敏感预检失败 → 退出 4 |
| 集群访问 | 不连接集群 | 不读取 `KUBECONFIG`、不调用 Kubernetes API；只消费本地事故包 |
| 只读 | 不产生任何写操作 | 不调用 apply/delete/exec，不执行修复建议；报告只写入本地文件 |
| Secret | 不读取 Secret | CLI 不访问集群，因此无法读取 Secret；对用户提供的事故包做敏感模式扫描 |
| 凭据泄漏 | 报告/错误不回显原值 | `validate` 严格模式拒绝；`standard` 分析前替换为 `[REDACTED]`；schema 二次校验 |
| 越权证据 | 引用必须来自包内 | 证据 id 由本包生成；跨包/不存在引用视为报告失败；模型引用需闭合 |
| 提示注入 | 证据与模型输出按不可信数据处理 | 系统提示不执行证据中的指令；模型输出做 URL/命令/指令性文本中和 |
| 供应链 | 可校验分发产物 | `checksums.txt`（sha256）+ cosign keyless 签名 + CycloneDX SBOM；镜像以 digest 签名 |
| 遥测 | 默认关闭、字段最小化 | 显式 `telemetry enable` 才启用；10 字段白名单；不采集事故内容 |
| 权限 | 不需要 root / 集群凭据 | 普通用户运行；Docker 镜像以 nonroot 运行 |

### 不覆盖的范围

- 不防御已被攻破的本机或恶意替换的二进制——请用 sha256 + cosign 校验下载物；
- 不判断事故包内容本身的业务正确性，也不替代人工复核；
- Beta 阶段 macOS 二进制未做 Apple notarization，企业分发可自行签名/托管；
- 暂无 Windows 支持（规划中）。

## 数据流

```text
                    ┌───────────────────────────────────────────────┐
                    │ 本地事故包（用户准备，建议先脱敏）              │
                    │ incident.yaml / k8s/ / metrics/ / logs/ ...   │
                    └──────────────────────┬────────────────────────┘
                                           │ 只读（不修改证据文件）
                                           ▼
  validate ──► schema 校验 + RFC3339 时间 + 敏感内容扫描
               ├── strict：命中 → 退出码 4，不进入分析
               └── standard：命中内容在分析前替换为 [REDACTED]
                                           │
                                           ▼
  analyze ──► 证据规范化（id / content_ref / sha256 / observed_at / stale）
              五类故障确定性规则（离线、不联网）
                                           │
                     ┌─────────────────────┴──────────────────────┐
                     ▼                                            ▼
              report.json                                  report.md
              （稳定 schema，机器读）                       （由 JSON 渲染，人读）
                     └─────────────────────┬──────────────────────┘
                                           ▼
                              本地文件系统（包内或 --out）

可选路径：
  analyze --model <provider>
    └─► HTTP(S) POST 仅到达 --model-url 配置的端点（禁重定向；证据文本会发送给该端点）
         └─► 运行时失败：保留离线报告 + degraded=true + limitations 记录原因 + 退出码 3

企业推送路径（可选，显式配置）：
  push <dir> --endpoint <enterprise-control-plane>
    └─► KUBECTL_TRIAGE_ENTERPRISE_TOKEN 只从 env 读取；POST {endpoint}/v1/push
         └─► 默认只发报告 + 证据哈希；--include-evidence 才附加脱敏 tar.gz
              └─► 推送前 strict 校验 + 敏感扫描：失败退出 4、不发送；网络/429/5xx 重试耗尽退出 3

遥测路径（默认关闭）：
  analyze 成功写报告后
    └─► 若已显式 enable 且配置了端点：HTTP(S) POST 到 KUBECTL_TRIAGE_TELEMETRY_URL
         └─► 仅 10 个白名单字段；2s 超时；失败静默；禁重定向；未配置端点则直接丢弃
```

要点：

- **模型模式会把证据包内容发送给你配置的端点**——端点选择即数据边界决定；敏感环境请用本地
  Ollama 或企业自建网关；
- **企业 push 只把已脱敏报告发给显式配置的端点**：默认不含证据正文（只发哈希），
  `--include-evidence` 才附加脱敏证据包；令牌只在 `KUBECTL_TRIAGE_ENTERPRISE_TOKEN` 环境变量中；
- 遥测通路与诊断通路完全独立：遥测关闭/失败不影响分析，也不会把报告或证据写入遥测；
- 报告与事故包都留在本地；CLI 没有任何云端存储或账号体系。

## 权限清单

| 权限 / 资源 | 是否需要 | 说明 |
| --- | --- | --- |
| 读取事故包目录文件 | 需要 | 只读；`incident.yaml` 与证据文件（k8s/、metrics/、logs/、changes/ 等） |
| 写入 `report.md` / `report.json` | 需要 | 默认写事故包目录；`--out` 可指定目录或单格式文件 |
| 读取 `KUBECONFIG` / 集群 API / Secret | **不需要（且不使用）** | 不访问集群；不解析 kubeconfig；不存在 Secret 读取路径 |
| `exec` / `apply` / `delete` 等写操作 | **不执行** | 报告中的修复建议明确标注只读、不自动执行 |
| 网络访问 | 默认不需要 | 模型模式仅访问配置端点；企业 push 仅访问 `--endpoint`；遥测 opt-in 且需显式配置端点 |
| `KUBECTL_TRIAGE_MODEL_KEY` / `OPENAI_API_KEY` | 仅模型模式 | 只从环境变量读取；不写入 flag、报告、日志或遥测 |
| `KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT` | 仅 `push` | 企业控制平面端点（也可用 `--endpoint`）；必须 https，仅 loopback 允许 http |
| `KUBECTL_TRIAGE_ENTERPRISE_TOKEN` | 仅 `push` | 令牌只从环境变量读取；无对应 flag，不落盘、不在报告/日志/错误信息中回显 |
| `KUBECTL_TRIAGE_CONFIG_DIR` | 可选 | 覆盖遥测配置目录；见下文 |
| `KUBECTL_TRIAGE_TELEMETRY_URL` | 可选 | 遥测端点覆盖；未设置则事件丢弃 |
| `KUBECTL_TRIAGE_TELEMETRY` | 可选 | 设为 `off` 强制关闭遥测（大小写不敏感） |
| 遥测配置文件读写 | 仅 `telemetry enable/disable` | 文件权限 `0600`；`analyze` / `status` 只读不写 |
| 临时目录 | eval 内部使用 | 注入扫描渲染到系统临时目录后删除；事故包不被修改 |
| root 权限 | **不需要** | 普通用户即可运行；容器镜像以 `nonroot:nonroot` 运行 |

## 脱敏模式

| 模式 | 行为 | 使用建议 |
| --- | --- | --- |
| `strict`（默认） | 命中敏感模式即拒绝，退出码 4；错误不回显原值 | 真实事故、企业评审 |
| `standard` | `validate` 警告放行；分析前把命中内容替换为 `[REDACTED]` | 已知日志含疑似模式但需继续分析 |
| `off` | 跳过扫描 | 仅调试，禁止用于生产数据 |

敏感内容扫描是**尽力而为**的启发式规则，不是 DLP 产品；导入真实事故前仍应按团队规范人工脱敏。
`import` 写入前同样扫描，命中时退出码 4 且不写入任何文件。

## 供应链校验

安装文档给出完整命令；最小步骤：

1. 下载 `checksums.txt` / `checksums.txt.sig` / `checksums.txt.pem`；
2. `cosign verify-blob` 验证签名（keyless，证书身份为 release workflow 的 tag 引用）；
3. `sha256sum --check --ignore-missing checksums.txt`（macOS 用 `shasum -a 256 -c`）；
4. SBOM（`*.cdx.json`，CycloneDX）核对依赖清单；
5. 容器镜像用 `cosign verify` 验证 digest 签名，生产固定到 digest。

详见 [docs/install.md](install.md#2-release-二进制) 与
[ADR-005 D2](adr/ADR-005-M3实现口径.md)。

## 遥测隐私（默认关闭）

### 默认行为

- 未执行 `telemetry enable` 前：**不发送任何数据**，不创建配置文件，不写状态文件；
- 未配置端点（`endpoint` 为空且未设置 `KUBECTL_TRIAGE_TELEMETRY_URL`）：事件直接丢弃，
  不落盘、不重试；
- 未配置时每次 `analyze` 成功会在 **stderr** 打印一行提示（不交互、不写盘）：
  `匿名统计默认关闭；启用：kubectl-triage telemetry enable`；
- 设置 `KUBECTL_TRIAGE_TELEMETRY=off` 强制关闭（大小写不敏感）；没有 `=on`——开启只能显式
  `telemetry enable`。

### 命令

```bash
kubectl-triage telemetry status    # 当前状态、生效端点、配置文件路径、install_id 前 8 位
kubectl-triage telemetry enable    # 生成/更新配置文件，启用匿名事件
kubectl-triage telemetry disable   # 关闭；保留 install_id（便于重新启用时去重）
```

- 输出中文；退出码 `0` 成功，`1` 参数/IO/配置损坏错误；
- `status` 若被环境变量强制关闭会明确注明；端点未配置时显示“未配置（事件将被丢弃）”；
- `install_id` 在首次 `enable` 时用 `crypto/rand` 生成 UUID v4，仅存于本地配置；不含任何
  硬件/用户标识，`status` 只显示前 8 位。

### 配置文件

路径优先级：

1. `$KUBECTL_TRIAGE_CONFIG_DIR/config.yaml`
2. `$XDG_CONFIG_HOME/kubectl-triage/config.yaml`
3. `~/.config/kubectl-triage/config.yaml`

YAML 键（仅由 `telemetry enable/disable` 原子写入，权限 `0600`）：

```yaml
enabled: true        # 是否启用匿名事件
install_id: 8f14e45f-ea2b-4c3a-9d1f-0a2b3c4d5e6f
endpoint: https://telemetry.example.com/v1/events   # 为空则事件丢弃
```

`analyze` 只读配置；配置损坏（非法 YAML / 非法 install_id）时 `telemetry` 命令退出 1，
`analyze` 忽略损坏配置继续运行（不影响诊断）。

### 事件字段白名单（恰好 10 个，多一不可）

当前实现只发送一种事件 `event=analyze`，在成功写出报告后 best-effort 发送一条。

| 字段 | 类型 | 示例 | 说明 |
| --- | --- | --- | --- |
| `install_id` | string | `8f14e45f-…` | 首次 enable 时生成的随机 UUID v4 |
| `event` | string | `analyze` | 事件名（当前固定为 `analyze`） |
| `version` | string | `0.3.0` | CLI 版本 |
| `os` | string | `darwin` | 运行时操作系统 |
| `arch` | string | `arm64` | 运行时架构 |
| `mode` | string | `offline` / `model` | 报告分析模式 |
| `fault_class` | string | `""` | 预留故障类目；当前实现不填充 |
| `duration_ms` | number | `1200` | 本次分析耗时（毫秒） |
| `evidence_count` | number | `18` | 证据条数 |
| `outcome` | string | `ok` / `missing` | 有候选根因为 `ok`，无假设为 `missing`；失败路径不发送 |

> 需求文档 §10 中的 `first_success` / `analyze_success` 属指标层命名；当前实现收敛为单一
> `analyze` 事件，指标可从该事件派生（首次 / 重复使用）。口径变更需同步 ADR。

### 绝不采集

| 绝不采集 | 说明 |
| --- | --- |
| 集群名、命名空间、工作负载名 | 不进入任何事件字段 |
| 告警名 / 告警内容 | 不采集 |
| 日志正文、证据正文、指标数值 | 不采集；事件只含条数 |
| 报告内容、候选根因文本 | 不采集 |
| 模型 key / API token / 环境变量值 | 不采集 |
| 文件路径、主机名、用户名、IP | 不采集 |
| 硬件 / 用户标识 | `install_id` 只是随机 UUID，不含设备信息 |

### 传输与失败语义

- **HTTP(S) POST JSON**：端点完全由用户或企业配置（本地/内网自建端点可用 http；**生产端点应使用
  HTTPS**）；CLI 不内置任何遥测域名；
- **同步有界发送**：单次最长 **2s** 超时；超时或失败**静默**，不重试、不落盘、不在后台异步执行；
- HTTP 客户端**禁用重定向**（不跟随 302/307），端点无法把 `install_id` 转发到其它主机；
- 不影响退出码、标准输出与报告内容。

### 关闭与删除

```bash
kubectl-triage telemetry disable            # 关闭（保留 install_id）
KUBECTL_TRIAGE_TELEMETRY=off kubectl-triage analyze ...   # 单次强制关闭

# 彻底删除本地遥测状态（install_id 一并清除）
cfg="${KUBECTL_TRIAGE_CONFIG_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/kubectl-triage}"
echo "$cfg"   # 删除前先确认解析后的绝对路径
rm -rf "$cfg"
```

> 默认位置是 `~/.config/kubectl-triage/`；设置了 `KUBECTL_TRIAGE_CONFIG_DIR` 时为其指向的目录。
> 删除不可恢复，请先确认路径。

## 模型模式的安全边界

详见 [docs/model.md](model.md)。摘要：

- 只访问 `--model-url` 指定的端点，HTTP 客户端禁用重定向（`CheckRedirect` 返回错误）；
- 证据中的 URL 一律不访问；证据文本视为不可信数据；
- 模型输出必须通过 `report.json` schema 校验 + 证据引用闭合 + 文本中和
  （外部链接/命令/指令性内容替换为 `[已移除外部链接]` / `[已移除命令]` / `[已移除可疑指令内容]`）；
- 不启用工具 / function calling；模型不读集群、不执行命令；
- 任何运行时失败自动降级离线规则，报告 `degraded: true`，退出码 3。

## 报告与证据的数据处理

- 报告只写本地：事故包内 `report.json` / `report.md`，或 `--out` 指定路径；
- 报告正文不含 Secret、token、完整环境变量；Markdown 由 JSON 渲染，内容一致；
- 证据摘要会进入报告（供评审），**摘要由已脱敏证据生成**；分享报告前仍按团队规范确认；
- `eval` 只读用例目录，不修改 golden 数据；注入扫描的临时渲染文件用后删除；
- `push` 只读报告与证据包，不修改本地文件；推送失败不影响本地报告；证据正文仅显式
  `--include-evidence` 才上传；
- `--dry-run` / `--out` 写出的 payload 同样属于敏感数据（含报告，可能含证据包），但不含企业
  令牌；请按企业规范保管并及时删除。

## 漏洞上报

请**不要**开公开 issue。按 [SECURITY.md](../SECURITY.md) 私下联系维护者，附：

- 受影响版本 / commit；
- 最小复现（不含真实凭据或客户数据）；
- 影响范围，以及是否可能让数据离开本机。

我们会在 3 个工作日内确认，修复后协调披露。

## 相关文档

- [安装与校验](install.md)：sha256 / cosign / SBOM / Docker / go install
- [快速开始](quickstart.md)：退出码与常见问题
- [模型模式](model.md)：provider、预算、降级与注入防护
- [本地评测](eval.md)：注入安全评分与确定性回归
- [企业试点](pilot.md)：安全评审如何进入试点
- [Push 协议 1.0](push-protocol.md) · [开源版与企业版](enterprise.md)：企业桥网络边界与协议
- [ADR-005：M3 实现口径](adr/ADR-005-M3实现口径.md)（D2 签名/SBOM、D6 遥测）
- [SECURITY.md](../SECURITY.md)：漏洞上报流程
