# 指标漏斗与周报口径

本文定义 kubectl-triage 的**安装 → 激活 → 重复使用 → 企业试点**四层漏斗、数据来源与局限，
以及每周复盘用的周报模板。口径与[需求文档 §10.1/§10.2](需求文档.md)一致；遥测实现见
[安全与数据流](security.md#遥测隐私默认关闭)；试点运营见[试点运营手册](pilot-playbook.md)。

**不以 GitHub Star 为核心 KPI。** Star 只记录、不作目标；数据最小化优先于指标完备性。

## 四层漏斗

| 层 | 指标 | 定义 | 采集来源 | 90 天目标 |
| --- | --- | --- | --- | --- |
| 1 | 实际安装 | 去重后的 Release / Homebrew / 容器安装（估算） | Release 下载、Homebrew analytics、GHCR 拉取 | ≥1,000 |
| 2 | 激活 | 首次成功生成报告（opt-in 遥测，按 `install_id` 去重） | 遥测 `event=analyze` 首条事件 | ≥30% 安装 |
| 3 | 重复使用 | 激活后 28 天内 ≥2 次成功诊断的 `install_id` 占比 | 遥测 `event=analyze` | ≥25%（占激活） |
| 4 | 企业试点 | 合格试点申请 → 进入试点 → 转正/付费 | Issue 表单 + 资格访谈 + 内部 CRM | ≥10 申请 / ≥3 进入 / ≥1 付费 |

> 90 天目标属**假设，需验证**（需求 §10.2）；不承诺 Star 或“诊断准确率 99%”。

## 指标定义与采集

### 1. 实际安装（粗粒度估算）

三个渠道**分开统计、不做跨渠道去重**，因为公开数据无法识别同一用户：

| 渠道 | 采集方式 | 局限 |
| --- | --- | --- |
| Release 下载 | GitHub Releases API `assets[].download_count` 求和 | 含重复下载/CI/镜像站；无法去重 |
| Homebrew | Homebrew analytics（`brew analytics` 页面 / tap 安装统计） | 用户可关闭 analytics；第三方 tap 覆盖有限 |
| 容器 | GHCR 组织 Packages 页面（Pull 统计为粗粒度） | 无稳定公开 API，按月记录 |

操作示例（Release 下载）：

```bash
gh api repos/paas-ops/kubectl-triage/releases --paginate \
  | jq -r '.[] | [.tag_name, ([.assets[].download_count] | add // 0)] | @tsv'
```

**口径**：安装数报告为“估算区间 + 各渠道分项”，不对外宣称精确去重安装量。

### 2. 激活

- 定义：某个 `install_id` 的**第一条** `analyze` 遥测事件（报告已成功生成）；
- 需求 §10.1 的 `first_success` 在 M3 实现中收敛为 `event=analyze` 的首条事件；
  `outcome=ok` 表示有候选根因，`outcome=missing` 表示证据不足但报告仍生成——两者都算激活；
- 激活率 = 激活 `install_id` 数 ÷ 同期安装估算。**分子分母异源**（遥测 vs 下载），只作趋势参考，
  不作精确比例承诺。

### 3. 重复使用（4 周留存）

- 定义：激活后 **28 天内出现 ≥2 条** `analyze` 事件的 `install_id` 占比（占激活）；
- 需求 §10.1 的 `analyze_success` 对应当前 `event=analyze`；只使用版本/OS/架构/模式/耗时/
  证据条数等白名单字段，不含任何事故内容；
- 重复率 = 重复 `install_id` 数 ÷ 激活 `install_id` 数。

### 4. 企业试点

| 阶段 | 判定 | 来源 |
| --- | --- | --- |
| 试点申请 | 提交 Issue 表单（`.github/ISSUE_TEMPLATE/pilot-request.yml`） | GitHub Issues，`labels:pilot` |
| 合格申请 | 满足[试点资格](pilot.md)：≥2 SRE、≥3 集群、≥10 起脱敏事故、接受 28 天 | 资格访谈记录 |
| 进入试点 | 签收试点范围并排期开始 | 内部 CRM / 试点计划 |
| 转正 / 付费 | 商业化合同与交付 | 内部 CRM |

- 渠道来源：优先使用 Issue 表单“从哪里了解到 kubectl-triage”字段（site / README / 评测内容 /
  同事或社区推荐 / 其他）；对外投放链接统一加 UTM，并与该字段对齐；
- 合格率 = 合格申请 ÷ 申请；进入率 = 进入试点 ÷ 合格申请；转正率 = 转正 ÷ 进入试点；
- 企业能力的交付边界（G2–G5：事故库 / 评测集 / 审计 / 私有部署）见
  [开源版与企业版](enterprise.md)；OSS 只提供 `push` 客户端与公开协议。

## 遥测事件与命名映射

当前实现（ADR-005 D6）只发送一种事件 `event=analyze`，成功写出报告后 best-effort 发送一条：

```json
{
  "install_id": "<随机 UUID v4>",
  "event": "analyze",
  "version": "0.4.0",
  "os": "darwin",
  "arch": "arm64",
  "mode": "offline",
  "fault_class": "",
  "duration_ms": 1200,
  "evidence_count": 18,
  "outcome": "ok"
}
```

| 需求 §10.1 名称 | 当前实现映射 |
| --- | --- |
| `first_success` | 每个 `install_id` 的首条 `analyze` 事件 |
| `analyze_success` | `analyze` 事件（`outcome=ok` 表示有候选根因） |

约束：**默认关闭**、显式 `telemetry enable` 才发送、端点由用户/企业配置
（`KUBECTL_TRIAGE_TELEMETRY_URL`，未配置即丢弃）、单次最长 2s、失败静默、不重试。
事件字段白名单与“绝不采集”清单见[安全与数据流](security.md#遥测隐私默认关闭)。

**样本偏差**：opt-in 用户不是随机样本，遥测指标只能作为**下界与趋势**，不能当作全量用户的精确值。

## UTM 与渠道约定

对外链接（站点、文档、评测内容、社区/活动）统一使用：

| 参数 | 取值 |
| --- | --- |
| `utm_source` | `site` / `docs` / `readme` / `eval` / `cli` / `community` |
| `utm_medium` | `pilot_cta` / `doc_link` / `blog` / `event` |
| `utm_campaign` | `m4-pilot`（当前试点周期） |

由于 GitHub Issue 表单不保证记录 UTM 参数，**以表单“了解渠道”字段 + 资格访谈为准**；
投放链接的 UTM 仅用于投放侧点击统计，不承诺与 Issue 一一对应。

## 周报模板

每周一复盘使用（15–30 分钟）：

```markdown
# kubectl-triage 周报 YYYY-Www

## 漏斗
| 指标 | 本周新增 | 上周 | 累计 | 90 天目标 | 趋势 | 动作 |
| --- | --- | --- | --- | --- | --- | --- |
| Release 下载 |  |  |  | — |  |  |
| Homebrew 安装 |  |  |  | — |  |  |
| GHCR 拉取 |  |  |  | — |  |  |
| 激活 install_id |  |  |  | ≥30% 安装 |  |  |
| 4 周重复 install_id |  |  |  | ≥25% 激活 |  |  |
| 试点申请（合格/总数） |  |  |  | ≥10 合格 |  |  |
| 进入试点 |  |  |  | ≥3 |  |  |
| 转正/付费 |  |  |  | ≥1 |  |  |

## 本周进展
- 发布/版本：
- 文档/站点：
- 试点：
- 阻塞：

## 质量与安全（与增长分开看）
- 12 golden / eval：通过数/总数
- 发布自校验：通过/失败
- 安全事件：0 起（如有，记录处置）

## 下周动作（≤3 条，含负责人）
```

## 质量指标（不混入增长漏斗）

| 指标 | 来源 | 目标 |
| --- | --- | --- |
| 12 个 golden 确定性回归 | `go test` / `eval` | 12/12 |
| 本地 eval 通过率 | `kubectl-triage eval --json` | 100%（确定性用例） |
| 敏感内容泄漏 | 注入/脱敏负例 + 发布自校验 | 0 |
| 发布自校验（sha256/cosign/SBOM/镜像签名） | `verify-release` workflow | 每个 tag 通过 |
| 试点报告可用性 | 客户 SRE 评分（1–5） | ≥4 为可用 |

正式 100 例评测集作为**企业版/发布门槛**单独管理，见[评测集契约](eval-contract.md)；OSS 的
12/12 是回归证据，不是统计准确率。

## 反虚荣指标

- Star、fork、issue 数只记录，不作目标，不进入周报“进展”；
- 下载量 ≠ 安装量 ≠ 激活量，不把三者混成一个数字；
- 不对外宣称未经验证的准确率；质量结论以封闭评测与客户签收为准。

## 相关文档

- [安全与数据流](security.md#遥测隐私默认关闭)：遥测默认关闭、字段白名单、绝不采集
- [企业试点指南](pilot.md) · [试点运营手册](pilot-playbook.md)
- [本地评测](eval.md) · [评测集契约](eval-contract.md)
- [开源版与企业版](enterprise.md) · [需求文档 §十](需求文档.md) · [拆分计划 §六](拆分计划.md)
