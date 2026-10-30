# 安装与校验（M3 Beta）

kubectl-triage 是单二进制 CLI，支持 macOS / Linux 的 amd64 与 arm64。四种安装方式按“最短路径”
排序；**仓库内 Markdown 文档是安装口径的事实源**，站点只做摘要。

安装产物由 `v*` tag 触发的 [release workflow](../.github/workflows/release.yml) 生成
（GoReleaser v2 + cosign keyless + syft SBOM），口径见
[ADR-005 D1–D3](adr/ADR-005-M3实现口径.md)。

## 安装矩阵

| 方式 | 适用场景 | 状态 |
| --- | --- | --- |
| [源码 `go install`](#1-源码安装) | 有 Go 1.24+ 工具链；任何版本都可装 | ✅ 立即可用 |
| [Release 二进制](#2-release-二进制) | 不想装 Go；需要校验 / 签名 / SBOM | ✅ `v0.3.0` 起 |
| [Docker / GHCR](#4-docker--ghcr) | CI、临时排查、隔离环境 | ✅ `v0.3.0` 起 |
| [Homebrew](#5-homebrew) | macOS / Linuxbrew 日常使用 | ✅ tap 已建立（v0.3.0 formula；新版本手工同步，见 [docs/homebrew.md](homebrew.md)） |

> 没有预编译产物的版本（v0.1.0 / v0.2.0）以源码安装为准；Beta 起以 Release 资产为准。
> 运行时不需要网络：安装阶段需要，之后默认零网络。

## 1. 源码安装

需要 [Go](https://go.dev/dl/) 1.24 或更高版本（CI 使用 1.26 验证）。

```bash
go version   # go1.24 或更高
go install github.com/paas-ops/kubectl-triage/cmd/kubectl-triage@latest
kubectl-triage version
```

- 固定版本：把 `@latest` 换成 `@v0.3.0`。
- 二进制安装到 `$(go env GOBIN)`；未设置时为 `$(go env GOPATH)/bin`。确认该目录在 `PATH` 中。
- 作为 kubectl 插件：只要 `$PATH` 中存在名为 `kubectl-triage` 的可执行文件，即可使用
  `kubectl triage <子命令>`，无需额外配置。
- 首次下载模块需要能访问 Go module proxy（可自行配置企业代理/镜像）；CLI 运行本身不联网。

## 2. Release 二进制

每个 `v*` Release 包含以下资产（4 个归档 + 3 个校验文件 + 4 个 SBOM + 1 个 formula，共 12 个）：

| 资产 | 说明 |
| --- | --- |
| `kubectl-triage_<version>_<os>_<arch>.tar.gz` × 4 | darwin/linux × amd64/arm64；内含二进制 + README + LICENSE + CHANGELOG |
| `checksums.txt` | 全部 `tar.gz` 的 sha256 |
| `checksums.txt.sig` / `checksums.txt.pem` | cosign keyless 对 `checksums.txt` 的签名与证书 |
| `kubectl-triage_<version>_<os>_<arch>.cdx.json` × 4 | 每个归档的 CycloneDX JSON SBOM |
| `kubectl-triage.rb` | Homebrew formula（已同步到 `paas-ops/homebrew-tap`） |

`<version>` 不含 `v` 前缀（tag `v0.3.0` → 文件名 `..._0.3.0_...`）。

### 2.1 下载

以 `v0.3.0` / macOS arm64 为例，按需替换 `VERSION` / `OS` / `ARCH`：

```bash
VERSION=0.3.0
OS=darwin          # darwin | linux
ARCH=arm64         # amd64 | arm64
BASE="https://github.com/paas-ops/kubectl-triage/releases/download/v${VERSION}"

curl -fLO "$BASE/kubectl-triage_${VERSION}_${OS}_${ARCH}.tar.gz"
curl -fLO "$BASE/checksums.txt"
curl -fLO "$BASE/checksums.txt.sig"
curl -fLO "$BASE/checksums.txt.pem"
```

### 2.2 校验 sha256

```bash
# Linux（GNU coreutils）：只检查已下载的归档，忽略其它平台的条目
sha256sum --check --ignore-missing checksums.txt

# macOS / BSD（shasum）：同一格式的 "hash  filename" 行
grep "kubectl-triage_${VERSION}_${OS}_${ARCH}.tar.gz$" checksums.txt \
  | shasum -a 256 -c -
```

输出 `...: OK` 即一致；出现 `FAILED` 说明下载损坏或被篡改，请重新下载。
校验不通过的包不要安装。

### 2.3 校验 cosign 签名（keyless）

签名由 release workflow 通过 GitHub OIDC 完成，无需维护公钥。先
[安装 cosign](https://docs.sigstore.dev/cosign/system_config/installation/)（如 `brew install cosign`）：

```bash
cosign verify-blob \
  --certificate checksums.txt.pem \
  --signature checksums.txt.sig \
  --certificate-identity \
    "https://github.com/paas-ops/kubectl-triage/.github/workflows/release.yml@refs/tags/v${VERSION}" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  checksums.txt
```

- 只有签名覆盖的 `checksums.txt` 通过校验，才能相信其中列出的各归档 sha256；**先验签名，再比对哈希**。
- `--certificate-identity` 必须精确对应 tag 版本（把 `VERSION` 换成本次下载的版本）；tag 不匹配会失败。
- 成功输出包含 `Verified OK`；失败时不要继续安装。

### 2.4 解压与安装

```bash
tar -xzf "kubectl-triage_${VERSION}_${OS}_${ARCH}.tar.gz"
sudo install -m 0755 kubectl-triage /usr/local/bin/kubectl-triage
kubectl-triage version
```

macOS 提示 “cannot be opened because the developer cannot be verified” 时，说明归档带有
Gatekeeper quarantine 标记（Beta 阶段未做 Apple notarization）。确认来源与校验无误后：

```bash
xattr -d com.apple.quarantine ./kubectl-triage || true
```

### 2.5 SBOM（CycloneDX）

每个归档对应一个同名 `*.cdx.json`，列出该二进制的依赖组件与版本，用于供应链核对。可用
`syft` 或任意 CycloneDX 查看器阅读：

```bash
# 用 syft 直接扫描归档（与 Release 中 *.cdx.json 内容同源）
syft "kubectl-triage_${VERSION}_${OS}_${ARCH}.tar.gz" -o table

# 或直接读 JSON（需要 jq）
jq -r '.components[] | "\(.name) \(.version // "")"' \
  "kubectl-triage_${VERSION}_${OS}_${ARCH}.cdx.json"
```

本仓库只依赖 Go 标准库与 `spf13/cobra`、`gopkg.in/yaml.v3`；SBOM 中如出现其它组件，
说明下载物与本仓库源码不一致。注意：`checksums.txt` 与 cosign 签名覆盖的是 `tar.gz` 归档，
SBOM 文件作为附加资产提供，不在签名范围内。

## 3. 升级与卸载

| 方式 | 升级 | 卸载 |
| --- | --- | --- |
| 源码 | 重新执行 `go install ...@<new-version>` | 删除 `$(go env GOPATH)/bin/kubectl-triage` |
| Release | 下载新版本并按 2.1–2.4 重新校验、替换二进制 | 删除安装位置的二进制 |
| Homebrew | `brew update && brew upgrade kubectl-triage` | `brew uninstall kubectl-triage` |
| Docker | 拉取新 tag | `docker rmi ghcr.io/paas-ops/kubectl-triage:<tag>` |

`v0.x` 允许 schema 小版本演进；升级前看 [CHANGELOG](../CHANGELOG.md) 的迁移说明。

## 4. Docker / GHCR

镜像 `ghcr.io/paas-ops/kubectl-triage`，tag 为 `v0.3.0`（与 Release tag 一致，保留 `v`）和
`latest`；多阶段构建，运行层为 distroless static、以非 root 用户 `nonroot:nonroot` 运行，
镜像内**不含 kubeconfig、集群凭据或 shell**。

```bash
docker run --rm ghcr.io/paas-ops/kubectl-triage:v0.3.0 version

# 只读挂载事故包，报告写到单独的宿主目录
mkdir -p reports
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/incidents:/incidents:ro" \
  -v "$PWD/reports:/reports" \
  ghcr.io/paas-ops/kubectl-triage:v0.3.0 \
  analyze /incidents/2026-10-12-checkout-oom --out /reports
```

- 容器默认以 uid 65532（nonroot）运行；挂载的宿主目录若不允许该用户写报告，可像上面一样用
  `--user` 对齐宿主 uid/gid，或只把报告写到可写目录。
- 容器内同样默认离线；需要模型模式时显式传 `--model` 与端点，并确保容器可访问该端点。
- 生产/CI 建议固定到 digest：`docker pull ghcr.io/paas-ops/kubectl-triage@sha256:<digest>`；
  镜像以 digest 为单位做 cosign keyless 签名，可用 `cosign verify` 按 release workflow 身份验证：

```bash
cosign verify ghcr.io/paas-ops/kubectl-triage:v0.3.0 \
  --certificate-identity \
    "https://github.com/paas-ops/kubectl-triage/.github/workflows/release.yml@refs/tags/v0.3.0" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
```

`v0.3.0` 起才有镜像；之前的版本无容器产物。

## 5. Homebrew

tap 仓库 `paas-ops/homebrew-tap` **已建立**，包含 `v0.3.0` formula：

```bash
brew tap paas-ops/homebrew-tap
brew install kubectl-triage

# 升级 / 卸载
brew update && brew upgrade kubectl-triage
brew uninstall kubectl-triage
```

后续版本的 formula 目前由维护者手工同步（下载 Release 资产 `kubectl-triage.rb` → 提交到 tap 的
`Formula/`）；自动推送待配置 `HOMEBREW_TAP_GITHUB_TOKEN`。同步与审阅要点见
[docs/homebrew.md](homebrew.md)。

## 6. 验证安装

```bash
kubectl-triage version
# 预期形如：kubectl-triage 0.3.0 (commit abc1234, built 2026-10-30T...)

kubectl-triage init /tmp/triage-install-check
kubectl-triage validate /tmp/triage-install-check
kubectl-triage analyze /tmp/triage-install-check
# 退出码 0，且 /tmp/triage-install-check/report.md 与 report.json 已生成
```

作为 kubectl 插件验证：`kubectl triage version`（kubectl 在 `PATH` 中若能找到
`kubectl-triage` 即可）。

## 7. 故障排查

| 现象 | 原因 / 处理 |
| --- | --- |
| `kubectl-triage: command not found` | 安装目录不在 `PATH`；把 `$(go env GOPATH)/bin` 或 `/usr/local/bin` 加入 `PATH` |
| `go install` 报 Go 版本过低 | 需要 Go 1.24+；升级工具链，或改用 Release 二进制 |
| sha256 校验失败 | 下载截断 / 平台选错 / 镜像缓存；重新下载并核对文件名中的 os/arch |
| `cosign verify-blob` 校验失败 | 版本字符串与 tag 不一致；`--certificate-identity` 必须精确到 `refs/tags/v<version>` |
| Docker 写报告 Permission denied | 镜像以 nonroot 运行；用 `--user "$(id -u):$(id -g)"` 或挂载可写目录 |
| macOS 提示无法验证开发者 | Beta 未 notarize；校验 sha256 / cosign 后 `xattr -d com.apple.quarantine` |
| 公司网络无法访问 GitHub | 使用内部镜像/制品库；或源码安装（Go proxy 走内部镜像） |
| 运行时报退出码 3 | 模型模式运行时失败并已降级；检查 `--model-url`/网络，或去掉 `--model` 走纯离线 |

## 相关文档

- [5 分钟快速开始](quickstart.md)：首次诊断完整流程、退出码表与常见问题
- [Homebrew 现状](homebrew.md)：formula 资产、tap bootstrap、自动推送
- [安全与数据流](security.md)：只读边界、网络边界、遥测隐私、权限清单
- [模型模式](model.md)：Ollama / OpenAI-compatible 配置、预算与降级
- [本地评测](eval.md)：eval 用法与 JSON 契约
- [ADR-005：M3 实现口径](adr/ADR-005-M3实现口径.md)：发布产物、签名、SBOM、Docker、Homebrew 口径
- [CHANGELOG](../CHANGELOG.md)：版本变更与迁移说明
