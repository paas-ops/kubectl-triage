# 下一步工作（留存）

| 项 | 内容 |
| --- | --- |
| 生成时间 | 2026-10-30 |
| 当前版本 | v0.5.0（M5「v1.0 准备」） |
| 目的 | 把 M5 之后的工作项、决策条件与验收标准固化为文档，避免依赖口头约定 |
| 状态标记 | **P0** 阻塞 v1.0 / **P1** 试点与运营（外部）/ **P2** 增强与技术债 |
| 关联 | [ADR-007](adr/ADR-007-v1.0准备.md)、[指标漏斗](metrics.md)、[试点 playbook](pilot-playbook.md)、[评测集契约](eval-contract.md)、[schema 契约](schema.md) |

---

## 1. v1.0 宣布条件（决策 Gate）

v0.5.0 只是"v1.0 准备"，**v1.0.0 的宣布需要同时满足**：

| # | 条件 | 当前状态 | 验收方式 | 负责人 |
| --- | --- | --- | --- | --- |
| 1 | 正式 100 例盲测集建成，两名 SRE 标注 | ⏳ 未开始 | `eval <set> --validate --min-cases 100` 通过 + 基线报告归档 | 数据/评测负责人 |
| 2 | schema 兼容基线稳定（ADR-007 D2） | ✅ 已就绪 | `go test ./pkg/schema`（CI 门禁） | CLI maintainer |
| 3 | 试点数据：≥10 合格申请、≥3 进入试点 | 🟡 进行中（外部） | 试点 issue 表单 + CRM + 周报 | 创始人/GTM |
| 4 | 质量门槛：注入 100% 安全、离线降级 0、Top1 不低于基线 | 🟡 部分（12 golden 已覆盖） | eval 报告（golden + 正式集）；`eval --compare baseline.json` 无回退 + 注入 100% + 降级 0 | 评测负责人 |
| 5 | 供应链安全：SHA256 + cosign + SBOM + `verify-release` 全绿 | ✅ 已就绪 | v0.5.0 已验证 | Release maintainer |
| 6 | 安全审查与漏洞披露流程就绪 | ✅ 已就绪 | SECURITY.md + docs/security.md | Maintainer |

**决策**：1、3、4 满足后由产品负责人评审；通过则发布 `v1.0.0` 并生效 schema 兼容承诺（v1.0 后至少兼容一个大版本）。

---

## 2. P0：阻塞 v1.0 的任务

### 2.1 建设 100 例正式盲测集

1. **数据来源**：试点客户每家 ≥10 起脱敏历史事故；覆盖五类故障 + 缺失证据 + 注入/越权样本。
2. **脱敏**：先跑 `kubectl-triage validate` 与凭据扫描；不得包含真实客户标识、Secret、kubeconfig。
3. **目录契约**：按 [评测集契约](eval-contract.md)（`incident.yaml` + 证据 + `expected/report.json`）；
   `suite.yaml` 记录集合名/版本/用例数/标签。
4. **结构门禁**：`kubectl-triage eval <set> --validate --min-cases 100` 必须退出 0。
5. **期望报告**：用固定时钟生成后**人工双人标注**（假设/置信/反证/缺失项）；不得直接把实现输出当期望。
6. **基线快照**：记录 Top1/Top3/缺失处理/注入安全/降级数，作为后续版本"不得回退"的基线；
   **工程门禁已提供**：`eval <set> --json > baseline.json` 生成快照，CI 用
   `eval <set> --compare baseline.json` 阻止回退（见 [本地评测](eval.md)）。
7. **存储**：私有仓库/受控存储；OSS 仓库只保留 12 个合成 golden（现有）。
8. **验收**：校验通过 + 基线报告归档 + 两名 SRE 签字。

### 2.2 Windows 实机冒烟（外部机器）

| # | 检查 | 期望 |
| --- | --- | --- |
| 1 | 下载 `kubectl-triage_0.5.0_windows_amd64.zip`，`Get-FileHash` 与 `checksums.txt` 一致 | 哈希一致 |
| 2 | （可选）`cosign verify-blob` 校验 checksums 签名 | Verified OK |
| 3 | `Expand-Archive` 后 `kubectl-triage.exe version` | 显示 0.5.0 |
| 4 | `init` → `validate` → `analyze` → `report` | 报告生成，退出码 0 |
| 5 | 加入 PATH 后作为插件 `kubectl triage version` | 正常 |
| 6 | 路径含空格/中文、CRLF 文本、`%USERPROFILE%` 配置目录 | 无崩溃、无乱码 |
| 7 | Windows 版本/架构/截图/退出码归档 | 验收记录 |

### 2.3 v1.0 发布准备

- 复核 schema 兼容承诺与迁移说明模板（破坏性变更才升主版本）；
- CHANGELOG v1.0.0 与发布说明；安全审查清单复核；发布/回滚演练；
- 确认 v1.0 宣布材料（能力边界、已知限制、评测数据口径）。

---

## 3. P1：试点与运营（外部执行）

- **招募**：[`.github/ISSUE_TEMPLATE/pilot-request.yml`](../.github/ISSUE_TEMPLATE/pilot-request.yml) + [pilot playbook](pilot-playbook.md)；
  目标 90 天 ≥10 家合格申请、≥3 家进入试点。
- **执行**：28 天 playbook（安装 → 首批 ≥10 起回放 → 评测 → 团队化/push 联调 → 复盘）。
- **周报**：[指标漏斗](metrics.md) 模板：安装 → 激活 → 4 周重复 → 试点请求 → 进入试点/转正。
- **复盘**：每两周把共性问题转为 P2 技术债或需求；每季度评审 90 天决策点（拆分计划 §九）。
- **验收**：≥3 家完成 28 天试点验收；≥1 家标准付费（90 天商业门槛，见商业方案）。

---

## 4. P2：技术债与增强

| 项 | 说明 | 优先级 |
| --- | --- | --- |
| CLI `--now` | eval 有 `--now` 复现 golden，analyze 无；若需要 CLI 侧确定性回放可加隐藏 flag | P2 |
| 模型配置解析去重 | `analyze` 与 `eval` 各自解析模型 flags，可抽公共 helper（行为需保持镜像一致） | P2 |
| push 增强 | 支持 `Retry-After`、大证据包分片/断点续传；服务端幂等语义联调 | P2 |
| Homebrew cask 迁移 | 当前固定 GoReleaser v2.15.4 以保留 `brews`；迁移到 `homebrew_casks` 后升版并同步安装命令 | P2（下一大版本） |
| 遥测端点 | 只定义客户端契约；官方统计端点与服务端需另建（默认关闭不变） | P2 |
| Windows 签名 | 预编译 exe 无 Authenticode；可选购买证书签名（当前靠 sha256+cosign+SmartScreen 指引） | P2 |
| 文档站链接检查进 CI | 当前为人工临时脚本；可固化为 `docs-lint` job | P2 |
| schema 基线纳入审计 | 可把兼容基线哈希写入 Release 说明/资产，便于消费者核对 | P2 |
| 依赖升级巡检 | go.mod 最低 1.24、CI 1.26；季度检查 Go/依赖安全公告 | 例行 |

---

## 5. 发布与回滚流程（v0.5.0 之后）

1. **常规发布**：`main` CI 绿 → 打 annotated tag `vX.Y.Z` → `release` workflow（GoReleaser v2.15.4 + cosign + SBOM + GHCR + 发布自校验）→ 用 `verify-release` 复验 → tap `sync-formula` 自动同步 → 更新发布说明。
2. **发布失败处理**：修复后删除远端 release 与 tag（`gh release delete --cleanup-tag`、`git push --delete`）再重打；不得在坏 tag 上追加构建。
3. **回滚**：Homebrew 用户可 `brew install kubectl-triage@<previous>`（需 tap 保留旧版 formula 或临时回退文件）；Release 资产不可变，回滚以"发布新补丁版本"为准。
4. **安全修复**：SECURITY.md 私密上报 → 修复 → 补丁发布 → 协调披露；高危问题同步吊销 GHCR 镜像 tag。

---

## 6. 参考

- [ADR-007：v1.0 准备](adr/ADR-007-v1.0准备.md)
- [评测集契约](eval-contract.md) · [本地评测](eval.md) · [schema 契约](schema.md)
- [指标漏斗](metrics.md) · [试点 playbook](pilot-playbook.md)
- [项目文档](PROJECT.md) · [需求文档](需求文档.md) · [拆分计划](拆分计划.md)
