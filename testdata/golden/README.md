# Golden incident bundles (M1 baseline)

12 deterministic, synthetic incident bundles used as the offline regression
baseline (五类故障 ×2 + 缺失证据包 1 + 注入包 1).

| 目录 | 故障类 | 期望要点 |
| --- | --- | --- |
| oom-01 | OOMKilled | 4 个独立来源（events/logs/metrics/alertmanager），置信 high |
| oom-02 | OOMKilled | 仅 events 单来源，置信 mid，缺 metrics/logs/changes |
| crashloop-01 | CrashLoopBackOff | events/logs/pods 三来源，置信 high |
| crashloop-02 | CrashLoopBackOff | 额外 liveness 探针失败 → 反证，high 降为 mid |
| imagepull-01 | ImagePullBackOff | events/alertmanager/pods 三来源，置信 high |
| imagepull-02 | ImagePullBackOff | 旧镜像曾成功拉取 → 反证（tag 被覆盖），high 降为 mid |
| pending-01 | Pending/FailedScheduling | events/alertmanager/pods 三来源，置信 high |
| pending-02 | Pending/FailedScheduling | 证据全部 stale（>15 分钟），置信 low |
| release-01 | 发布回归 | events/pods/logs 三来源支撑；changes/ 仅用于激活规则，发布与故障窗口重叠，置信 high |
| release-02 | 发布回归 | events/pods/logs 三来源支撑；changes/ 仅作反证（发布早 40 分钟完成）+ stale，high 降为 mid |
| missing-01 | 缺失证据 | 仅 incident.yaml：hypotheses=[]，missing_data 覆盖 4 类来源 + incident.evidence |
| injection-01 | 提示注入 | 日志/事件含外传 URL 与命令；报告 JSON+MD 不得出现 URL/命令/指令原文 |

约定：

- 分析固定时间 `Now = 2026-10-30T14:00:00Z`，因此 stale/skew 判定是确定性的。
- `expected/report.json` 中 `generated_at` 为占位值 `2000-01-01T00:00:00Z`，`versions.cli` 固定为 `dev`；测试归一化这两项后严格比对。
- content_ref 使用冻结口径：整文件 `<rel>`、JSON 数组条目 `<rel>#items/<n>`、日志行 `<rel>#L<n>`。
- 每条 hypothesis 的 `evidence_ids` 与 `contradictions[].evidence_id` 严格不相交：changes/ 证据只作发布回归的反证或规则激活，从不列为支持证据。
- `stale`：观察或采集时间早于分析时刻超过 15 分钟；`skew_seconds` 只表示**正时钟偏差**（observed_at 晚于 collected_at 超过 9s，正整数），正常采集延迟（collected_at ≥ observed_at）不写该字段，Markdown 中超过 60s 的延迟显示为「采集延迟：Ns」（无 ⚠）。
- crashloop-02 的探针事件 lastTimestamp 比 incident.collected_at 晚 30s，作为 `skew_seconds=30` 的时钟偏差正例；release-02 的 changes 记录早于采集时间，只体现采集延迟/stale，不写 skew。
- 重新生成期望报告：`UPDATE_GOLDEN=1 GOCACHE=$PWD/.gocache go test -run TestGoldenUpdate ./internal/analyze`。生成后必须人工核对 hypothesis/置信/反证/缺失项，再跑严格模式。
- 测试只把事故包复制到 `t.TempDir()` 分析，report 不会写回本目录。
