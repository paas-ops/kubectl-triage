# 安装与校验（v0.5.0）

kubectl-triage 是单二进制 CLI，支持 macOS / Linux（tar.gz，amd64 / arm64）与
Windows（zip，amd64；`v0.5.0` 起）。安装方式按“最短路径”排序；
**仓库内 Markdown 文档是安装口径的事实源**，站点只做摘要。

安装产物由 `v*` tag 触发的 [release workflow](../.github/workflows/release.yml) 生成
（GoReleaser v2 + cosign keyless + syft SBOM），口径见
[ADR-005 D1–D3](adr/ADR-005-M3实现口径.md)。

## 安装矩阵

| 方式 | 适用场景 | 状态 |
| --- | --- | --- |
| [源码 `go install`](#1-源码安装) | 有 Go 1.24+ 工具链；任何版本都可装 | ✅ 立即可用 |
| [Release 二进制](#2-release-二进制) | 不想装 Go；需要校验 / 签名 / SBOM | ✅ `v0.3.0` 起 |
| [Windows（amd64）](#26-windowsamd64) | Windows 10/11、Server 2019+；zip 解压即用，可作 kubectl 插件 | ✅ `v0.5.0` 起 |
| [Docker / GHCR](#4-docker--ghcr) | CI、临时排查、隔离环境 | ✅ `v0.3.0` 起 |
| [Homebrew](#5-homebrew) | macOS / Linuxbrew 日常使用 | ✅ tap 已建立（v0.3.0 formula；新版本手工同步，见 [docs/homebrew.md](homebrew.md)） |

> 没有预编译产物的版本（v0.1.0 / v0.2.0）以源码安装为准；Beta 起以 Release 资产为准；
> Windows 产物从 `v0.5.0` 起提供。运行时不需要网络：安装阶段需要，之后默认零网络。

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

每个 `v*` Release 包含以下资产（`v0.5.0` 起为 4 个 tar.gz + 1 个 Windows zip + 校验文件 +
SBOM + formula）：

| 资产 | 说明 |
| --- | --- |
| `kubectl-triage_<version>_<os>_<arch>.tar.gz` × 4 | darwin/linux × amd64/arm64；内含二进制 + README + LICENSE + CHANGELOG |
| `kubectl-triage_<version>_windows_amd64.zip` × 1 | Windows amd64（`v0.5.0` 起）；解压后为 `kubectl-triage.exe` + README/LICENSE/CHANGELOG |
| `checksums.txt` | 全部归档（tar.gz + zip）的 sha256 |
| `checksums.txt.sig` / `checksums.txt.pem` | cosign keyless 对 `checksums.txt` 的签名与证书 |
| `kubectl-triage_<version>_<os>_<arch>.cdx.json` × 5 | 每个归档（darwin/linux tar.gz + Windows zip）的 CycloneDX JSON SBOM（见 2.5） |
| `kubectl-triage.rb` | Homebrew formula（已同步到 `paas-ops/homebrew-tap`） |

`<version>` 不含 `v` 前缀（tag `v0.5.0` → 文件名 `..._0.5.0_...`）。
Windows 归档使用 `.zip`（`format_overrides`），其余平台使用 `.tar.gz`；两类归档都在
`checksums.txt` 中并被 cosign 签名覆盖。

发布流水线在发布时自校验签名；此外仓库提供手动 [`verify-release`](../.github/workflows/verify-release.yml)
workflow，可对任意已发布 tag 复验 sha256、cosign 证书身份、SBOM 覆盖与 GHCR 镜像签名：

```bash
gh workflow run verify-release.yml -R paas-ops/kubectl-triage -f tag=v0.5.0
```

### 2.1 下载

以 `v0.5.0` / macOS arm64 为例，按需替换 `VERSION` / `OS` / `ARCH`（Windows 见 2.6）：

```bash
VERSION=0.5.0
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

每个归档（darwin/linux `*.tar.gz` 与 Windows `*.zip`）都提供同名 `*.cdx.json` SBOM，列出该二进制的
依赖组件与版本，用于供应链核对。可用 `syft` 或任意 CycloneDX 查看器阅读：

```bash
# 用 syft 直接扫描归档（与 Release 中 *.cdx.json 内容同源）
syft "kubectl-triage_${VERSION}_${OS}_${ARCH}.tar.gz" -o table

# Windows：把归档后缀换成 .zip 即可（SBOM 命名规则相同）
syft "kubectl-triage_${VERSION}_windows_amd64.zip" -o table

# 或直接读 JSON（需要 jq）
jq -r '.components[] | "\(.name) \(.version // "")"' \
  "kubectl-triage_${VERSION}_${OS}_${ARCH}.cdx.json"
```

本仓库只依赖 Go 标准库与 `spf13/cobra`、`gopkg.in/yaml.v3`；SBOM 中如出现其它组件，
说明下载物与本仓库源码不一致。注意：`checksums.txt` 与 cosign 签名覆盖的是全部归档（`tar.gz` +
Windows `zip`）；SBOM 文件作为附加资产提供，不在签名范围内（其对应归档本身已被签名覆盖）。

### 2.6 Windows（amd64）

`v0.5.0` 起提供 `kubectl-triage_<version>_windows_amd64.zip`：解压得到 `kubectl-triage.exe`，
与 macOS/Linux 功能一致（五类规则、可选模型、eval/--validate、telemetry、push 相同），同样**默认离线**。
Windows 二进制未做 Authenticode 代码签名，安装前请完成 sha256 校验（cosign 可选但推荐）；
zip 同样有同名 CycloneDX SBOM（`kubectl-triage_<version>_windows_amd64.cdx.json`）。

```powershell
# 1) 下载 zip 与校验文件（PowerShell 5+；curl.exe 亦可）
$Version = "0.5.0"
$Base = "https://github.com/paas-ops/kubectl-triage/releases/download/v$Version"
$Zip = "kubectl-triage_${Version}_windows_amd64.zip"
Invoke-WebRequest -Uri "$Base/$Zip" -OutFile $Zip
Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile checksums.txt
Invoke-WebRequest -Uri "$Base/checksums.txt.sig" -OutFile checksums.txt.sig
Invoke-WebRequest -Uri "$Base/checksums.txt.pem" -OutFile checksums.txt.pem

# 2) 校验 sha256（与 checksums.txt 中对应行比对）
$expected = (Select-String -Path checksums.txt -Pattern ([regex]::Escape($Zip) + '$')).Line.Split(' ')[0]
$actual = (Get-FileHash -Algorithm SHA256 $Zip).Hash.ToLower()
if ($actual -ne $expected) { throw "sha256 mismatch: $actual != $expected" }
"sha256 OK"

# 3) 可选但推荐：cosign 校验签名（先安装 cosign）
cosign verify-blob --certificate checksums.txt.pem --signature checksums.txt.sig `
  --certificate-identity "https://github.com/paas-ops/kubectl-triage/.github/workflows/release.yml@refs/tags/v$Version" `
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" checksums.txt

# 4) 解压并做冒烟测试
Expand-Archive -Path $Zip -DestinationPath .\kubectl-triage -Force
.\kubectl-triage\kubectl-triage.exe version
```

安装到当前用户并加入 `PATH`（新开终端后生效）：

```powershell
$dest = Join-Path $env:LOCALAPPDATA "Programs\kubectl-triage"
New-Item -ItemType Directory -Force -Path $dest | Out-Null
Copy-Item .\kubectl-triage\kubectl-triage.exe $dest -Force
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$dest*") {
  [Environment]::SetEnvironmentVariable("Path", "$userPath;$dest", "User")
}
kubectl-triage version        # 新终端中验证
kubectl triage version        # 作为 kubectl 插件验证（kubectl-triage.exe 在 PATH 中即可）
```

- 浏览器下载的 zip 可能带 Mark-of-the-Web，首次运行可能触发 SmartScreen；完成 sha256/cosign
  校验后可在文件属性中“解除锁定”，或执行 `Unblock-File .\kubectl-triage\kubectl-triage.exe`；
- 路径使用 Windows 形式（如 `C:\incidents\2026-10-12-checkout-oom`）；其余命令与 macOS/Linux 一致；
- 不需要管理员权限即可安装到用户目录；避免把事故包放在需要提权的目录；
- 无 WSL / Git Bash 依赖，原生 `kubectl-triage.exe` 可直接在 PowerShell / cmd 中运行。

## 3. 升级与卸载

| 方式 | 升级 | 卸载 |
| --- | --- | --- |
| 源码 | 重新执行 `go install ...@<new-version>` | 删除 `$(go env GOPATH)/bin/kubectl-triage` |
| Release | 下载新版本并按 2.1–2.4 重新校验、替换二进制 | 删除安装位置的二进制 |
| Windows | 下载新 zip 校验后替换 `kubectl-triage.exe` | 删除安装目录与用户 `PATH` 条目 |
| Homebrew | `brew update && brew upgrade kubectl-triage` | `brew uninstall kubectl-triage` |
| Docker | 拉取新 tag | `docker rmi ghcr.io/paas-ops/kubectl-triage:<tag>` |

`v0.x` 允许 schema 小版本演进；升级前看 [CHANGELOG](../CHANGELOG.md) 的迁移说明。

## 4. Docker / GHCR

镜像 `ghcr.io/paas-ops/kubectl-triage`，tag 与 Release tag 一致（保留 `v`，如 `v0.5.0`）和
`latest`；多阶段构建，运行层为 distroless static、以非 root 用户 `nonroot:nonroot` 运行，
镜像内**不含 kubeconfig、集群凭据或 shell**。

```bash
docker run --rm ghcr.io/paas-ops/kubectl-triage:v0.5.0 version

# 只读挂载事故包，报告写到单独的宿主目录
mkdir -p reports
docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$PWD/incidents:/incidents:ro" \
  -v "$PWD/reports:/reports" \
  ghcr.io/paas-ops/kubectl-triage:v0.5.0 \
  analyze /incidents/2026-10-12-checkout-oom --out /reports
```

- 容器默认以 uid 65532（nonroot）运行；挂载的宿主目录若不允许该用户写报告，可像上面一样用
  `--user` 对齐宿主 uid/gid，或只把报告写到可写目录。
- 容器内同样默认离线；需要模型模式时显式传 `--model` 与端点，并确保容器可访问该端点。
- 生产/CI 建议固定到 digest：`docker pull ghcr.io/paas-ops/kubectl-triage@sha256:<digest>`；
  镜像以 digest 为单位做 cosign keyless 签名，可用 `cosign verify` 按 release workflow 身份验证：

```bash
cosign verify ghcr.io/paas-ops/kubectl-triage:v0.5.0 \
  --certificate-identity \
    "https://github.com/paas-ops/kubectl-triage/.github/workflows/release.yml@refs/tags/v0.5.0" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com"
```

镜像从 `v0.3.0` 起提供；示例中的版本号按实际 Release 替换。

## 5. Homebrew

tap 仓库 `paas-ops/homebrew-tap` **已建立**，包含已发布版本的 formula（如 `v0.5.0`）：

```bash
brew tap paas-ops/homebrew-tap
brew install kubectl-triage

# 升级 / 卸载
brew update && brew upgrade kubectl-triage
brew uninstall kubectl-triage
```

后续版本的 formula 由 tap 仓库的 `sync-formula` workflow 自动同步（每日 03:00 UTC，也可手动
`gh workflow run sync-formula.yml -R paas-ops/homebrew-tap`），无需跨仓库 PAT。细节见
[docs/homebrew.md](homebrew.md)。

## 6. 验证安装

```bash
kubectl-triage version
# 预期形如：kubectl-triage 0.5.0 (commit abc1234, built 2026-10-30T...)

kubectl-triage init /tmp/triage-install-check
kubectl-triage validate /tmp/triage-install-check
kubectl-triage analyze /tmp/triage-install-check
# 退出码 0，且 /tmp/triage-install-check/report.md 与 report.json 已生成
```

Windows（PowerShell）：

```powershell
kubectl-triage.exe version
kubectl-triage.exe init $env:TEMP\triage-install-check
kubectl-triage.exe validate $env:TEMP\triage-install-check
kubectl-triage.exe analyze $env:TEMP\triage-install-check
```

作为 kubectl 插件验证：`kubectl triage version`（macOS/Linux 上为 `kubectl-triage`，Windows 上为
`kubectl-triage.exe`；只要在 `PATH` 中即可）。

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
| Windows 提示 “kubectl-triage 无法识别” | 安装目录未加入 `PATH`（新开终端生效）；`where.exe kubectl-triage.exe` 检查 |
| Windows SmartScreen / Defender 警告 | Beta 未做 Authenticode 签名；校验 sha256/cosign 后选择“仍要运行”，或 `Unblock-File` |
| Windows zip 解压后找不到 exe | 二进制名为 `kubectl-triage.exe`，位于解压子目录；不要直接在 zip 内运行 |
| Windows 路径含空格 / 中文报错 | 用引号包住路径；建议事故包放在简单英文路径（如 `C:\incidents\...`） |

## 相关文档

- [5 分钟快速开始](quickstart.md)：首次诊断完整流程、退出码表与常见问题
- [Homebrew 现状](homebrew.md)：formula 资产、tap bootstrap、自动推送
- [安全与数据流](security.md)：只读边界、网络边界、遥测隐私、权限清单
- [模型模式](model.md)：Ollama / OpenAI-compatible 配置、预算与降级
- [本地评测](eval.md)：eval 用法、`--validate` 评测集校验与 JSON 契约
- [Schema 契约](schema.md)：`incident.yaml` / `report.json` 字段与兼容承诺
- [ADR-005：M3 实现口径](adr/ADR-005-M3实现口径.md)：发布产物、签名、SBOM、Docker、Homebrew 口径
- [ADR-007：v1.0 准备](adr/ADR-007-v1.0准备.md)：schema 兼容、Windows 支持、评测集校验器
- [CHANGELOG](../CHANGELOG.md)：版本变更与迁移说明
