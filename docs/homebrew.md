# Homebrew 安装（现状与 tap bootstrap）

口径见 [ADR-005 D4](adr/ADR-005-M3实现口径.md)。一句话概括：
**formula 会随每个 Release 作为资产发布；`paas-ops/homebrew-tap` 建立前，Homebrew 安装尚不可用。**
在此之前请使用 [源码安装](install.md#1-源码安装) 或 [Release 二进制](install.md#2-release-二进制)。

## 当前状态

| 项 | 状态 |
| --- | --- |
| GoReleaser 生成 Homebrew formula | ✅ 每个 `v*` Release 产出 `kubectl-triage.rb`（`brews` 配置） |
| formula 作为 Release 资产上传 | ✅ 与 `tar.gz` / `checksums.txt` 同一 Release |
| tap 仓库 `paas-ops/homebrew-tap` | ⏳ 尚未建立 |
| tap 自动推送（`HOMEBREW_TAP_GITHUB_TOKEN`） | ⏳ 未配置 secret |
| 用户命令 `brew install kubectl-triage` | ⏳ tap 建立后可用 |

formula 由 GoReleaser 生成后在主仓库 Release 中提供（`skip_upload: true`），其中已包含各平台
归档的 `url` 与 `sha256`；tap 建立后可直接使用，不需要手写版本号。

## tap 建立后：用户安装命令

```bash
brew tap paas-ops/homebrew-tap
brew install kubectl-triage

# 升级 / 卸载
brew update && brew upgrade kubectl-triage
brew uninstall kubectl-triage
brew untap paas-ops/homebrew-tap
```

## tap bootstrap 步骤（维护者）

tap 建立前，先照常发布 `v0.3.0`（release workflow 跑完、Release 资产齐全）。

1. **创建 tap 仓库**：在 `paas-ops` 下创建公开仓库 `homebrew-tap`，初始化为空仓库并保护 `main`
   （PR + 1 review，与主仓库一致）。
2. **取出 formula**：从 [v0.3.0 Release](https://github.com/paas-ops/kubectl-triage/releases) 下载
   `kubectl-triage.rb`，人工核对一次：
   - `version` 与本次 tag 一致（不带 `v`，例如 `0.3.0`）；
   - 四个平台归档的 `url` 指向本 Release；
   - `sha256` 与 Release 的 `checksums.txt` 中对应归档一致。
3. **提交到 tap**：放到 `Formula/kubectl-triage.rb`（旧版 Homebrew 兼容路径），提交并 push：

   ```bash
   git clone git@github.com:paas-ops/homebrew-tap.git
   cd homebrew-tap
   mkdir -p Formula
   cp /path/to/kubectl-triage.rb Formula/kubectl-triage.rb
   git add Formula/kubectl-triage.rb
   git commit -m "kubectl-triage 0.3.0"
   git push
   ```

4. **本地验证**（在 macOS / Linuxbrew 上）：

   ```bash
   brew tap paas-ops/homebrew-tap
   brew install kubectl-triage
   kubectl-triage version
   brew audit --strict --online kubectl-triage
   ```

   formula 是否带 `test do` 由 GoReleaser `brews.test` 决定；未配置时 `brew test` 会提示
   没有测试定义，改用上面的 `kubectl-triage version` 冒烟即可。

5. **更新 README/站点**：把 [docs/install.md](install.md) 与文档站中的 Homebrew 状态从
   “tap 待建立”改为“可用”。这一步不要提前做——文档不承诺尚未验证的安装路径。

## 后续启用自动推送（可选）

tap 建立并验证后，可让 release workflow 在每个 tag 自动更新 formula，不再手工复制：

1. 生成 **fine-grained PAT**（或 classic PAT），对 `paas-ops/homebrew-tap` 授予
   `Contents: Read and write`（classic PAT 需要 `repo` scope）。令牌只授权 tap 仓库，
   不要给主仓库写权限。
2. 在主仓库 `paas-ops/kubectl-triage` → Settings → Secrets and variables → Actions 新增
   secret `HOMEBREW_TAP_GITHUB_TOKEN`。secret 不会出现在日志中；不要把它写进 workflow 文件。
3. release-eng 需要做两处改动（当前尚未启用，以 ADR-005 D4 为准）：
   - `.goreleaser.yaml` 的 `brews` 去掉 `skip_upload: true`（或在 workflow 中改用
     `HOMEBREW_TAP_GITHUB_TOKEN` 环境变量），配置 `repository: owner: paas-ops, name: homebrew-tap`；
   - `.github/workflows/release.yml` 在 GoReleaser 步骤注入
     `HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}`。
4. 打一个 dry-run tag 或使用 `workflow_dispatch` 验证：tap 中 `Formula/kubectl-triage.rb` 出现新
   commit，`brew update && brew upgrade kubectl-triage` 能升级。若 secret 缺失，GoReleaser 应
   跳过 tap 上传而不是让整个 Release 失败——formula 仍作为 Release 资产兜底。

在没有完成第 1–5 步之前，不要把 `HOMEBREW_TAP_GITHUB_TOKEN` 当作发布前置条件：ADR-005 明确
tap 属外部依赖，缺失不阻塞 Release 产物。

## tap 建立前能否从 formula 资产安装？

部分 Homebrew 版本支持直接安装本地 formula：

```bash
brew install --formula ./kubectl-triage.rb
```

该路径依赖你的 Homebrew 版本与安全策略，**不是官方承诺的安装方式**；若报错，请使用
[Release 二进制](install.md#2-release-二进制)。tap 建立后以 `brew tap && brew install` 为准。

## 供应链与校验

- formula 内的 `sha256` 来自本次 Release 的归档，与 `checksums.txt` 一致；
- Homebrew 安装时会自动校验归档哈希，但仍建议对下载的 `kubectl-triage.rb` 与
  [Release 资产](https://github.com/paas-ops/kubectl-triage/releases)比对，避免使用他人的
  formula；
- Homebrew 的匿名 analytics 由 Homebrew 控制，不是 kubectl-triage 的遥测；kubectl-triage 自身
  遥测默认关闭，见 [docs/security.md](security.md#遥测隐私默认关闭)；
- tap 属于发布供应链的一部分：保护 `main`、PR 审核 formula diff（`version` 与 `sha256` 是主要
  审阅点）。

## 相关文档

- [安装与校验](install.md)：Release 资产、sha256 / cosign / SBOM、Docker、go install
- [5 分钟快速开始](quickstart.md)
- [ADR-005：M3 实现口径](adr/ADR-005-M3实现口径.md)（D4 Homebrew）
- [CHANGELOG](../CHANGELOG.md)
