package rule

import "github.com/paas-ops/kubectl-triage/internal/evidence"

// DefaultRules returns the five deterministic M1 failure-class rules in
// evaluation order. Order is also the tie-breaker when two candidates share the
// same confidence level.
func DefaultRules() []Rule {
	return []Rule{
		{
			ID:        "oom-killed",
			Priority:  10,
			Statement: "容器因内存不足被 OOMKilled，需核对 memory limit 与工作集峰值",
			Checks: []string{
				"核对容器 memory limit 与最近一次发布是否降低 limit",
				"查看同一节点其他 Pod 的内存压力与驱逐事件",
				"对比故障前 30 分钟容器 working set 与 limit",
			},
			SuggestedFix: "只读建议：确认峰值后评估提高 memory limit 或修复内存泄漏；不自动修改工作负载。",
			Markers: []string{
				"oomkilled",
				"out of memory",
				"exitcode=137",
				"exit code 137",
				"oom kill",
			},
		},
		{
			ID:        "crashloop-backoff",
			Priority:  20,
			Statement: "容器反复启动失败进入 CrashLoopBackOff，需定位启动期错误",
			Checks: []string{
				"查看上一个容器实例日志与退出码",
				"核对启动探针、配置与依赖服务可用性",
				"对比最近发布是否改变启动参数",
			},
			SuggestedFix: "只读建议：修复启动失败原因（配置/依赖/探针）后再发布；不自动重启或回滚。",
			Markers: []string{
				"crashloopbackoff",
				"back-off restarting failed container",
				"backoff restarting",
				"restarting failed container",
				"panic:",
				"fatal error:",
			},
			Counter: func(in Input, support []evidence.Item) []Contradiction {
				return ProbeCounter(in)
			},
		},
		{
			ID:        "image-pull",
			Priority:  30,
			Statement: "镜像拉取失败（ImagePullBackOff/ErrImagePull），镜像名、tag 或仓库凭证之一不可用",
			Checks: []string{
				"核对镜像 tag 是否存在、是否被覆盖",
				"检查 imagePullSecret 与仓库权限",
				"确认节点到镜像仓库的网络与限流",
			},
			SuggestedFix: "只读建议：修正镜像引用或仓库凭证后重新发布；不自动拉取或覆盖镜像。",
			Markers: []string{
				"imagepullbackoff",
				"errimagepull",
				"pull access denied",
				"manifest unknown",
				"failed to pull image",
				"image pull failed",
				"invalid image name",
			},
			Counter: func(in Input, support []evidence.Item) []Contradiction {
				return ImagePulledCounter(in)
			},
		},
		{
			ID:        "pending-scheduling",
			Priority:  40,
			Statement: "Pod 无法调度（Pending/FailedScheduling），节点资源或调度约束不满足",
			Checks: []string{
				"查看调度器事件中的节点过滤原因",
				"核对 requests/limits、污点与亲和性",
				"确认节点组是否有可扩容容量",
			},
			SuggestedFix: "只读建议：调整 requests/亲和性或扩容节点；不自动驱逐/创建节点。",
			Markers: []string{
				"failedscheduling",
				"insufficient cpu",
				"insufficient memory",
				"nodes are available",
				"unschedulable",
				"phase: pending",
			},
		},
		{
			ID:        "release-regression",
			Priority:  50,
			Statement: "故障窗口与近期发布变更重叠（镜像/ReplicaSet 变化），需核对变更内容与回滚点",
			Checks: []string{
				"核对发布变更的镜像、commit 与配置差异",
				"对比发布前后错误率、延迟与重启次数",
				"确认回滚点与回滚影响范围",
			},
			SuggestedFix: "只读建议：评估回滚或修正发布内容；只读核对，不自动执行回滚。",
			Markers: []string{
				"image",
				"rollout",
				"replica",
				"deploy",
			},
			RequireSource:      "changes",
			ExcludeFromSupport: "changes",
			Counter: func(in Input, support []evidence.Item) []Contradiction {
				return ReleaseTimingCounter(in, support)
			},
		},
	}
}

// DefaultRegistry returns a registry over DefaultRules.
func DefaultRegistry() *Registry {
	return NewRegistry(DefaultRules()...)
}
