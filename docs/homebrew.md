# Homebrew 安装（现状与 tap bootstrap）

口径见 [ADR-005 D4](adr/ADR-005-M3实现口径.md)。一句话概括：
**formula 随每个 Release 作为资产发布，并已同步到 `paas-ops/homebrew-tap`（当前 `v0.3.0`）；
`brew tap paas-ops/homebrew-tap && brew install kubectl-triage` 可用。**

## 当前状态

| 项 | 状态 |
| --- | --- |
| GoReleaser 生成 Homebrew formula | ✅ 每个 `v*` Release 产出 `kubectl-triage.rb`（`brews` 配置） |
| formula 作为 Release 资产上传 | ✅ 与 `tar.gz` / `checksums.txt` 同一 Release |
| tap 仓库 `paas-ops/homebrew-tap` | ✅ 已建立（`Formula/kubectl-triage.rb`） |
| formula 自动同步 | ✅ tap 仓库 `.github/workflows/sync-formula.yml`（每日 03:00 UTC + 手动 dispatch，无需 PAT） |
| 用户命令 `brew install kubectl-triage` | ✅ 可用 |

formula 由 GoReleaser 生成后在主仓库 Release 中提供（`skip_upload: true`），其中已包含各平台
归档的 `url` 与 `sha256`；tap 已同步该 formula，不需要手写版本号。

## formula 自动同步（无需 PAT）

tap 仓库内置 `.github/workflows/sync-formula.yml`：

- **触发**：每日 03:00 UTC 定时任务；也可手动触发（`workflow_dispatch`，可指定 tag）；
- **行为**：读取 `paas-ops/kubectl-triage` 的最新 Release（或指定 tag）→ 下载 `kubectl-triage.rb` →
  与 `Formula/kubectl-triage.rb` 比较 → 有变化才提交；
- **权限**：使用 tap 仓库自身的 `GITHUB_TOKEN`（读取公开主仓库 Release、写本仓库），
  **不需要跨仓库 PAT / `HOMEBREW_TAP_GITHUB_TOKEN`**。

手动立即同步：

```bash
gh workflow run sync-formula.yml -R paas-ops/homebrew-tap
# 或指定版本
gh workflow run sync-formula.yml -R paas-ops/homebrew-tap -f tag=v0.3.0
```

同步后用户侧：`brew update && brew upgrade kubectl-triage`。

## 用户安装命令（已可用）

```bash
brew tap paas-ops/homebrew-tap
brew install kubectl-triage

# 升级 / 卸载
brew update && brew upgrade kubectl-triage
brew uninstall kubectl-triage
brew untap paas-ops/homebrew-tap
```

## tap bootstrap 记录（2026-10-30 已完成，留作复现参考）

`v0.3.0` Release 产物齐全后执行：

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
   “tap 待建立”改为“可用”（2026-10-30 已更新）。

## 为什么不用跨仓库 PAT

另一种做法是让主仓库 release workflow 直接推送 tap，但跨仓库写入需要长期 PAT（
`HOMEBREW_TAP_GITHUB_TOKEN`），带来密钥轮换与泄露面。当前方案把"拉取上游 Release + 写本仓库"
放在 tap 自己的 workflow 里，用 GitHub 自动签发的仓库级 token 完成，权限最小、无长期密钥。
主仓库 Release 仍将 formula 作为资产发布，作为兜底与审计来源。

## tap 建立前能否从 formula 资产安装？

部分 Homebrew 版本支持直接安装本地 formula：

```bash
brew install --formula ./kubectl-triage.rb
```

该路径依赖你的 Homebrew 版本与安全策略，**不是官方承诺的安装方式**；若报错，请使用
[Release 二进制](install.md#2-release-二进制)。tap 已建立，以 `brew tap && brew install` 为准。

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
