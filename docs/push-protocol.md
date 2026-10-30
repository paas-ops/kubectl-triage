# Push 协议 1.0（企业桥公开协议）

| 项 | 内容 |
| --- | --- |
| 协议版本 | `protocol_version` = `"1.0"`（冻结） |
| 状态 | M4 企业桥；随 `v0.4.0` 发布客户端与本文档 |
| 客户端 | `kubectl-triage push <dir>`（OSS，Apache-2.0） |
| 服务端 | 企业控制平面（CloudOps Agent，商业版；不在本仓库） |
| 契约来源 | [ADR-006](adr/ADR-006-M4实现口径.md) |
| 关联 | [开源版与企业版](enterprise.md)、[安全与数据流](security.md)、[评测集契约](eval-contract.md) |

本文是 **企业控制平面服务端实现者** 的公开契约：按此实现 `POST /v1/push` 即可接收
kubectl-triage 推送。OSS 仓库只包含客户端与本文档；接收端、事故库、评测集托管、审计与私有部署
由商业版交付（G2–G5，见[拆分计划 §4.7](拆分计划.md)）。

## 范围与设计原则

- **显式推送**：只有用户显式执行 `push` 并配置端点与令牌时才联网；本地诊断永远免费、不依赖服务端；
- **默认数据最小化**：默认只发送报告与每条证据的哈希，**不上传证据正文**；
  `--include-evidence` 才上传脱敏证据包（tar.gz + base64）；
- **显式协议版本**：`protocol_version` 冻结为 `1.0`；服务端必须拒绝未知主版本；
- **幂等**：同一次推送的重试复用同一 `push_id` / `Idempotency-Key`，服务端按此去重；
- **不跟随重定向**：客户端禁用 HTTP 重定向，服务端不得依赖重定向；
- **TLS 不可降级**：客户端不提供关闭证书校验的开关。

## 传输与端点

```text
POST {endpoint}/v1/push
```

| 项 | 规则 |
| --- | --- |
| 端点配置 | `--endpoint` 或环境变量 `KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT`（flag 优先） |
| scheme | 必须 `https`；仅 loopback（`127.0.0.1` / `localhost` / `::1`）允许 `http` 本地联调 |
| URL 限制 | 不得包含 userinfo、query、fragment；base path 会被保留，例如 `https://host/api` → `https://host/api/v1/push` |
| 令牌 | 仅环境变量 `KUBECTL_TRIAGE_ENTERPRISE_TOKEN`；无 flag；不写入报告/日志/错误信息 |
| 超时 | `--timeout`（默认 `30s`）约束整个推送（含重试与打包）；单次 HTTP 请求同时受该值约束 |
| 代理 | 遵循 Go `net/http` 标准行为（`HTTPS_PROXY` / `NO_PROXY` 等由运行环境决定） |
| TLS | 使用系统信任链；企业自签 CA 需导入操作系统/运行时信任链（Linux 下 Go 支持 `SSL_CERT_FILE`/`SSL_CERT_DIR`，macOS 使用系统钥匙串），客户端不提供跳过校验选项 |

端点/令牌缺失、端点非法 → 客户端退出码 `1`，**零网络请求**。

## 认证

```http
Authorization: Bearer <token>
```

- 令牌是不透明字符串，由企业侧签发与轮换；协议 1.0 不定义 OAuth/刷新流程；
- 服务端应校验令牌并返回 `401` / `403` + 错误 JSON；客户端把 4xx 映射为退出码 `1`；
- 客户端把服务端错误消息中出现的令牌替换为 `[REDACTED]`（纵深防御），服务端仍**不得**在响应中回显令牌。

## 请求头

| 头 | 值 | 说明 |
| --- | --- | --- |
| `Authorization` | `Bearer <token>` | 必选 |
| `Content-Type` | `application/json` | 请求体为 UTF-8 JSON |
| `Idempotency-Key` | `<push_id>` | 必选；与 body 的 `push_id` 相同，重试不变 |
| `User-Agent` | `kubectl-triage/<cli-version>` | 版本取自报告 `versions.cli`（Release 构建通常不带 `v`，如 `0.4.0`；源码构建可能为 `dev`） |
| `Accept` | `application/json` | 期望 JSON 响应 |

## 请求体

`protocol_version 1.0` 的请求体字段固定如下（新增字段保持向后兼容，见“版本演进”）：

| 字段 | 类型 | 必选 | 说明 |
| --- | --- | --- | --- |
| `protocol_version` | string | ✅ | 固定 `"1.0"` |
| `push_id` | string | ✅ | 小写 UUID v4（`crypto/rand`）；同一次推送的重试保持不变 |
| `created_at` | string | ✅ | RFC3339 UTC，秒级精度，如 `2026-10-30T12:00:00Z` |
| `cli.version` | string | ✅ | CLI 版本（报告 `versions.cli`；缺省为 `unknown`） |
| `cli.rules` | string | ✅ | 规则版本（报告 `versions.rules`；缺省为 `unknown`） |
| `cli.model` | string | ✅ | 模型标识（报告 `versions.model`；缺省为 `none`） |
| `report` | object | ✅ | **完整 `report.json`**（`schema_version 1.0`），见 [report schema](../pkg/schema/report.schema.json) |
| `report_sha256` | string | ✅ | `"sha256:<hex>"`，对 `report` 成员的规范化 JSON 字节做 SHA-256 |
| `evidence` | array | ✅ | 证据哈希引用，每项 `{id, source, content_ref, sha256}`；默认不含正文 |
| `bundle` | object | ✅ | `{included:false}` 或包含证据包字段（见下） |

### report 与 report_sha256 的规范化

- 客户端把 `report.json` 解析为报告结构后重新序列化为**紧凑 JSON**（报告的 `MarshalJSON` 会把
  nil 切片规范为 `[]`、按 map 键排序），请求体中的 `report` 即这串字节；
- `report_sha256 = "sha256:" + hex(SHA-256(紧凑 report JSON 字节))`；
- 服务端校验时，应从请求体中提取 `report` 成员的**原始字节**（例如 Go 的 `json.RawMessage`）再计算哈希，
  **不要**先解析为通用 map 再重新序列化——键顺序/空白差异会导致哈希不一致；
- 参考：客户端实现位于 `internal/push/payload.go`（OSS，可审阅）。

### evidence 哈希引用

`evidence[]` 与报告中的 `report.evidence[]` 一一对应（顺序一致），字段原样复制：

```json
{
  "id": "ev-001",
  "source": "k8s.events",
  "content_ref": "k8s/events.json#items/0",
  "sha256": "sha256:9f86d0…"
}
```

- 用途：企业侧可在**不上传证据正文**的前提下做去重、完整性与审计核对；
- `sha256` 为报告契约中的哈希字符串（`sha256:<hex>`）；服务端可选地校验其与 `report.evidence[]` 一致。

### 默认 payload（不含证据正文）示例

```jsonc
{
  "protocol_version": "1.0",
  "push_id": "8f14e45f-ea2b-4c3a-9d1f-0a2b3c4d5e6f",
  "created_at": "2026-10-30T12:00:00Z",
  "cli": {
    "version": "0.4.0",
    "rules": "rule-v0.2",
    "model": "none"
  },
  "report": {
    // 完整 report.json（schema_version 1.0）；此处省略中间字段
    "schema_version": "1.0",
    "incident": {"id": "2026-10-12-checkout-oom", "alertname": "KubePodCrashLooping", "cluster": "prod-shanghai", "namespace": "checkout"},
    "generated_at": "2026-10-30T12:00:00Z",
    "mode": "offline",
    "degraded": false,
    "hypotheses": [],
    "evidence": [],
    "missing_data": [],
    "recommended_checks": [],
    "limitations": [],
    "versions": {"cli": "0.4.0", "rules": "rule-v0.2", "model": "none"}
  },
  "report_sha256": "sha256:3a7bd3e2…",
  "evidence": [
    {"id": "ev-001", "source": "k8s.events", "content_ref": "k8s/events.json#items/0", "sha256": "sha256:9f86d0…"}
  ],
  "bundle": {"included": false}
}
```

### 可选证据包（`--include-evidence`）

```jsonc
{
  "bundle": {
    "included": true,
    "content_type": "application/gzip",
    "sha256": "sha256:<压缩后 tar.gz 的 SHA-256>",
    "size_bytes": 12345,
    "content_base64": "<tar.gz 字节的标准 base64>"
  }
}
```

- `content_base64`：标准 base64（RFC 4648，带 padding），解码后即 `content_type` 指定的字节；
- `sha256` 对**压缩后**的 tar.gz 字节计算；`size_bytes` 为压缩后字节数；
- 默认大小上限：`--max-bundle-bytes` 默认 `5 MiB`（5,242,880 字节）；超过上限时客户端在本地报错并退出
  `1`，**不发起请求**；
- 证据包内容是事故包中**已脱敏的输入**，不是新的数据源；是否上传必须由用户显式 flag 决定。

#### 确定性打包规则

同一份事故包在同一客户端版本下会生成**逐字节相同**的 tar.gz（便于服务端按 `sha256` 去重/校验）：

- 条目 = `incident.yaml`（元数据）+ 事故包内文本文件（`bundle.WalkTextFiles()` 白名单扩展名：
  `.yaml` / `.yml` / `.json` / `.log` / `.txt` / `.md`，包含 k8s/metrics/logs/changes 证据与
  README/runbook/notes 等）；
- 按**相对路径（POSIX `/`）字典序**排序；
- 排除报告输出 `report.*`、`.git/` 与 `node_modules/`；不写入目录项、符号链接等额外条目；
- tar 头固定：`mode 0644`、`mtime 1970-01-01T00:00:00Z`（UTC）、`uid/gid 0`、`Uname/Gname` 为空、
  普通文件类型 `TypeReg`；
- gzip 使用 Go `gzip.NewWriter` 默认头（无 Name/Comment、ModTime 零、OS=255），同输入逐字节一致；
- 文件名均为相对于事故包根目录的相对路径，不包含绝对路径或 `..`。

> 服务端应把证据包当作**不可信压缩输入**处理：限制解压后总大小、拒绝绝对路径/路径穿越、拒绝非普通文件。

## 响应

### 成功（2xx）

```json
{
  "accepted": true,
  "protocol_version": "1.0",
  "incident_id": "inc_01J9X3K2P0"
}
```

- 客户端仅在 2xx 且响应体可解析为 `accepted:true` 时判定成功并退出 `0`；
- `incident_id` 由服务端生成，用于企业侧事故库定位（客户端仅在输出中展示，不写回本地报告）；
- 响应缺少 `protocol_version` 时客户端回退使用请求的 `1.0`（仅影响本地展示与审计）；服务端应始终回显；
- 客户端接受并忽略未知响应字段；响应体读取上限 `1 MiB`，请保持简短。

> 2xx 但响应体不是合法 accepted JSON 时，客户端视为传输异常并重试；重试耗尽后退出 `3`。

### 失败（4xx）

```json
{
  "error": {
    "code": "auth_failed",
    "message": "invalid enterprise token"
  }
}
```

推荐的错误码（协议不强制枚举，客户端只透传展示；`message` 必须简短且不含令牌）：

| HTTP | `error.code`（建议） | 含义 |
| --- | --- | --- |
| 400 | `invalid_payload` | JSON/字段/哈希不合法 |
| 401 / 403 | `auth_failed` | 令牌缺失、无效或无权限 |
| 409 | `duplicate` | 幂等键已存在（客户端把 409 视为退出 1 并提示重复） |
| 413 | `payload_too_large` | 请求体过大（尤其含证据包） |
| 422 | `unsupported_protocol_version` | 未知主版本（如 `2.0`），服务端**必须拒绝** |
| 429 | `rate_limited` | 触发限流（可重试；建议带 `Retry-After`，客户端固定退避 500ms/1s） |

- 4xx **不重试**；客户端退出码 `1`（配置/鉴权/被拒绝）；
- 服务端错误消息会被客户端去除其中出现的令牌后展示，但仍**不得**回显令牌、请求体或内部堆栈。

## 幂等、重试与超时

| 项 | 规则 |
| --- | --- |
| `push_id` | UUID v4；同一次命令的重试复用同一个值，并同时用于 `Idempotency-Key` |
| 服务端去重 | 按 `Idempotency-Key`（= `push_id`）去重；建议保留 ≥24h；重复请求推荐返回 2xx（同一 `incident_id`），也可返回 409 |
| 可重试 | 网络错误、HTTP `429`、HTTP `5xx`、2xx 但响应体非法（在总预算内最多重试 2 次） |
| 重试次数 | 首次 + 最多 2 次重试（共 3 次尝试） |
| 退避 | 固定 `500ms`，然后 `1s`（无 jitter；**不处理 `Retry-After`**） |
| 不可重试 | 4xx（含 400/401/403/409/413/422） |
| 超时 | `--timeout`（默认 30s）是**整体预算**：预算耗尽立即停止重试并退出 `3`（不是单次超时后再重试） |
| 重定向 | 客户端不跟随 3xx（`CheckRedirect` 拒绝）；按网络错误处理，重试耗尽退出 `3` |
| payload 复用 | 所有重试复用完全相同的请求字节（`push_id` / `Idempotency-Key` 不变） |
| TLS | 证书校验始终开启 |

## 客户端退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 2xx accepted，推送成功 |
| `1` | 配置/鉴权/被服务端拒绝（端点或令牌缺失、端点非法、4xx、报告缺失/schema 不合法、证据包超限） |
| `3` | 网络错误、超时、429、5xx（重试耗尽），或 2xx 响应非法 |
| `4` | 推送前安全校验失败（事故包或报告命中敏感模式） |

退出码 `2` 在 `push` 中不使用。推送失败**不影响**本地报告：`report.md`/`report.json` 已落盘。

## 客户端行为与安全约束

推送前（不联网）按顺序执行：

1. 校验端点/令牌/超时/大小上限（配置错误 → `1`）；
2. `bundle.Load` + `bundle.Validate`（强制 `strict` 脱敏模式；命中敏感模式 → `4`）；
3. 读取 `report.json`：缺失 → `1`（提示先运行 `analyze`）；`schema.ValidateReport` 失败 → `1`；
4. 对报告 JSON 做敏感模式扫描（`redact.Scrub` 比对，命中 → `4`，不发送）；
5. 构建 payload（必要时打包证据包、计算哈希），然后才发起网络请求。

其他约束：

- 令牌只从 `KUBECTL_TRIAGE_ENTERPRISE_TOKEN` 读取，不进入 payload、`--dry-run` 摘要、`--out` 文件或日志；
- payload 不含事故包绝对路径、模型 key、kubeconfig 内容；
- 客户端不会访问除配置端点之外的任何主机（不跟随重定向、不访问证据中的 URL）；
- `--dry-run` 构建 payload 与摘要但不联网，退出 `0`；仍需端点与令牌已配置（仅本地校验），
  输出为**中文文本摘要**（不是 JSON）：端点、协议版本、`push_id`、`created_at`、报告 sha256、
  证据条数、证据包是否包含及大小；用 `--out PATH` 另存 payload JSON（不含令牌）用于审计/联调/
  手工 `curl`（此时摘要末尾额外输出 payload 文件路径）；
- `--out` 也可用于正式推送：客户端在发送前把实际 payload 写入文件（推送失败文件仍保留），
  文件权限 `0644`，含报告（`--include-evidence` 时还含证据包 base64）——属于敏感数据，
  请按企业规范保管并及时删除。

## CLI 用法

```bash
kubectl-triage push <dir> [flags]

# 端点（flag 优先）/ 令牌（仅环境变量）
--endpoint URL               # 或 KUBECTL_TRIAGE_ENTERPRISE_ENDPOINT
                             # 令牌：KUBECTL_TRIAGE_ENTERPRISE_TOKEN

# 数据与传输
--include-evidence           # 显式包含脱敏证据包（默认只发报告 + 证据哈希）
--max-bundle-bytes N         # 证据包压缩后大小上限，默认 5242880（5 MiB）
--timeout D                  # 整个推送超时，默认 30s（Go duration，如 45s）

# 审计/联调
--dry-run                    # 构建 payload 与摘要，不联网，退出 0
--out PATH                   # 将 payload JSON 写入文件（无令牌）；dry-run 或正式推送均可用，
                             # 正式推送会在发送前写盘，供审计/手工测试
```

> `--dry-run` 仍要求端点与令牌已配置（只做本地校验，**绝不发送请求、绝不打印令牌**）。

示例：

```bash
export KUBECTL_TRIAGE_ENTERPRISE_TOKEN='<enterprise-token>'

# 1) 先 dry-run，确认 report_sha256 / evidence_count / bundle 摘要
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com --dry-run

# 2) 正式推送（默认只发报告与证据哈希）
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com

# 3) 显式包含脱敏证据包
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com --include-evidence
```

## 服务端实现提示（最小闭环）

1. **TLS 与鉴权**：只在 TLS 后暴露 `/v1/push`；校验 `Authorization: Bearer`，失败返回 401/403；
2. **限制请求体**：设置合理的 body 上限（含 base64 证据包，注意 base64 膨胀约 33%），超限返回 413；
3. **解析与版本**：确认 `Content-Type: application/json`、`protocol_version == "1.0"`，否则返回 4xx；
4. **校验哈希**：以原始字节提取 `report` 成员并重算 `report_sha256`；可选校验 `evidence[]` 与报告一致；
5. **校验报告**：用公开 schema 包（`github.com/paas-ops/kubectl-triage/pkg/schema`，Apache-2.0，
   可复用）做 `ValidateReport`；
6. **幂等**：以 `Idempotency-Key` 为键存储 `push_id → incident_id`，重复请求返回 2xx（或 409）；
7. **可选证据包**：base64 解码 → 校验 `sha256`/`size_bytes` → 安全解压（限制解压大小、拒绝绝对路径
   与路径穿越）→ 存入企业事故库；默认请求没有正文，不要向客户端索取；
8. **审计**：记录请求时间、`push_id`、`incident_id`、令牌主体、协议版本与结果；**不要记录令牌明文与证据正文**；
9. **响应**：成功返回 `{"accepted":true,"protocol_version":"1.0","incident_id":"…"}`；
   失败返回 4xx `{"error":{"code":"…","message":"…"}}`；限流返回 429，临时故障返回 5xx（客户端会重试）；
10. **展示安全**：报告与证据包含自由文本，Web/工单渲染时必须转义，避免存储型 XSS。

### curl 联调

用 `--dry-run --out` 生成与客户端**完全一致**的 payload，再手工请求：

```bash
kubectl-triage push ./incidents/2026-10-12-checkout-oom \
  --endpoint https://control.example.com --dry-run --out /tmp/push-payload.json

curl -sS -D - https://control.example.com/v1/push \
  -H "Authorization: Bearer $KUBECTL_TRIAGE_ENTERPRISE_TOKEN" \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: $(jq -r '.push_id' /tmp/push-payload.json)" \
  -H "User-Agent: kubectl-triage/0.4.0" \
  --data-binary @/tmp/push-payload.json
```

> `/tmp/push-payload.json` 含报告与证据哈希（`--include-evidence` 时含 base64 证据包），按企业数据
> 规范保管与删除；它**不包含**企业令牌。

## 版本演进

- `protocol_version` 主版本不兼容变更才递增（`2.0`）；服务端**必须拒绝**未知主版本（4xx）；
- `1.x` 内的新增字段必须可选、向后兼容：服务端应忽略未知字段；客户端应忽略未知响应字段；
- 客户端只发送 `1.0`；本文档与客户端实现同步更新（若实现与文档有出入，以发版实现为准并修订本文）；
- 服务端在成功响应中回显 `protocol_version`，便于灰度与审计。

## 一致性检查清单（服务端实现者）

- [ ] 只接受 HTTPS（本地联调可放宽，但生产必须 TLS）；
- [ ] 校验 Bearer 令牌，错误响应不回显令牌；
- [ ] 校验 `protocol_version == "1.0"`，未知主版本拒绝；
- [ ] 以原始字节校验 `report_sha256`，并用公开 schema 校验报告；
- [ ] 按 `Idempotency-Key` 幂等去重，重试不产生重复事故；
- [ ] 限制请求体与解压后证据包大小，拒绝路径穿越；
- [ ] 2xx 返回 `accepted:true` + `incident_id`；4xx 返回 `error.code/message`；
- [ ] 429/5xx 可被客户端安全重试；
- [ ] 渲染报告/证据时转义自由文本；
- [ ] 审计日志不含令牌明文与事故正文（除非企业策略明确要求且受控）。

## 相关文档

- [开源版与企业版](enterprise.md)：OSS 与企业版边界、G2–G5 交付位置
- [安全与数据流](security.md)：数据流、权限清单、遥测隐私
- [评测集契约](eval-contract.md)：企业评测集目录/数据契约
- [试点运营手册](pilot-playbook.md)：28 天试点与 push 联调流程
- [ADR-006：M4 实现口径](adr/ADR-006-M4实现口径.md) · [拆分计划 §4.7 Epic G](拆分计划.md)
- 报告契约：[pkg/schema/report.schema.json](../pkg/schema/report.schema.json) · [需求文档 §六](需求文档.md)
