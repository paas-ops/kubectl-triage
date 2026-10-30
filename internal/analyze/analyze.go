// Package analyze implements the initial deterministic offline analyzer.
//
// It is intentionally small: evidence is extracted from files in the bundle,
// then a handful of Kubernetes failure signatures produce cited hypotheses.
// Model-based analysis is added behind the same report contract in M2.
package analyze

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bavocado/kubectl-triage/internal/bundle"
	"github.com/bavocado/kubectl-triage/internal/report"
)

type rule struct {
	name       string
	markers    []string
	statement  string
	confidence string
	checks     []string
	fix        string
}

var rules = []rule{
	{
		name:       "oom",
		markers:    []string{"oomkilled", "out of memory", "exitcode=137", "exit code 137"},
		statement:  "容器因内存不足被 OOMKilled（需核对 memory limit 与工作集峰值）",
		confidence: "high",
		checks: []string{
			"核对容器 memory limit 与最近一次发布是否降低 limit",
			"查看同一节点其他 Pod 的内存压力与驱逐事件",
			"对比故障前 30 分钟容器 working set 与 limit",
		},
		fix: "只读建议：在确认峰值后评估提高 memory limit 或修复内存泄漏；不自动修改工作负载。",
	},
	{
		name:       "image",
		markers:    []string{"imagepullbackoff", "errimagepull", "pull access denied", "manifest unknown"},
		statement:  "镜像拉取失败（镜像名/标签/仓库凭证之一不可用）",
		confidence: "high",
		checks: []string{
			"核对镜像 tag 是否存在、是否被覆盖",
			"检查 imagePullSecret 与仓库权限",
			"确认节点到镜像仓库的网络与限流",
		},
		fix: "只读建议：修正镜像引用或仓库凭证后重新发布；不自动拉取或覆盖镜像。",
	},
	{
		name:       "crashloop",
		markers:    []string{"crashloopbackoff", "back-off restarting failed container"},
		statement:  "容器反复启动失败，进入 CrashLoopBackOff",
		confidence: "mid",
		checks: []string{
			"查看上一个容器实例日志与退出码",
			"核对启动探针、配置与依赖服务可用性",
			"对比最近发布是否改变启动参数",
		},
		fix: "只读建议：修复启动失败原因（配置/依赖/探针）后再发布；不自动重启或回滚。",
	},
	{
		name:       "pending",
		markers:    []string{"failedscheduling", "insufficient cpu", "insufficient memory", "0/"},
		statement:  "Pod 无法调度（节点资源或调度约束不满足）",
		confidence: "mid",
		checks: []string{
			"查看调度器事件中的节点过滤原因",
			"核对 requests/limits、污点与亲和性",
			"确认节点组是否有可扩容容量",
		},
		fix: "只读建议：调整 requests/亲和性或扩容节点；不自动驱逐/创建节点。",
	},
}

// Analyze runs the deterministic rule set over a validated bundle.
func Analyze(b *bundle.Bundle, cliVersion string) (*report.Report, error) {
	files, err := b.WalkTextFiles()
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	rep := report.New(b.Incident, cliVersion)
	evidenceByFile := map[string]string{}
	for _, rel := range files {
		if strings.EqualFold(filepath.Ext(rel), ".md") {
			continue // runbook/notes are context, not evidence
		}
		data, err := b.ReadFile(rel, 256<<10)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		sum := sha256.Sum256(data)
		summary := firstLine(data)
		if strings.EqualFold(filepath.Ext(rel), ".json") {
			summary = fmt.Sprintf("%s (JSON snapshot, %d bytes)", rel, len(data))
		}
		id := fmt.Sprintf("ev-%03d", len(rep.Evidence)+1)
		rep.Evidence = append(rep.Evidence, report.Evidence{
			ID:          id,
			Source:      rel,
			ObservedAt:  b.Incident.ObservedAt,
			CollectedAt: b.Incident.CollectedAt,
			Summary:     summary,
			Ref:         rel,
			SHA256:      "sha256:" + hex.EncodeToString(sum[:]),
		})
		evidenceByFile[rel] = id
	}

	var hypotheses []report.Hypothesis
	for _, r := range rules {
		var refs []string
		for rel := range evidenceByFile {
			data, err := b.ReadFile(rel, 256<<10)
			if err != nil {
				continue
			}
			lower := strings.ToLower(string(data))
			for _, marker := range r.markers {
				if strings.Contains(lower, strings.ToLower(marker)) {
					refs = append(refs, evidenceByFile[rel])
					break
				}
			}
		}
		if len(refs) == 0 {
			continue
		}
		sort.Strings(refs)
		conf := r.confidence
		if len(refs) == 1 && conf == "high" {
			conf = "mid"
		}
		hypotheses = append(hypotheses, report.Hypothesis{
			Rank:        len(hypotheses) + 1,
			Statement:   r.statement,
			Confidence:  conf,
			EvidenceIDs: refs,
		})
		if len(hypotheses) == 3 {
			break
		}
	}

	// Release regression is only claimed when the bundle explicitly contains a
	// changes/rollout file; generic ReplicaSet words are too noisy.
	for rel, id := range evidenceByFile {
		if strings.HasPrefix(rel, "changes/") && len(hypotheses) < 3 {
			hypotheses = append(hypotheses, report.Hypothesis{
				Rank:        len(hypotheses) + 1,
				Statement:   "近期发布变更与故障时间相关（需核对变更内容与回滚点）",
				Confidence:  "low",
				EvidenceIDs: []string{id},
			})
			break
		}
	}

	rep.Hypotheses = hypotheses
	if len(hypotheses) > 0 {
		rep.Summary = hypotheses[0].Statement
		rep.RecommendedChecks = unique(rulesFor(hypotheses[0].Statement))
		rep.SuggestedFix = fixFor(hypotheses[0].Statement)
	} else {
		rep.Summary = "现有证据不足以形成可引用的根因假设。"
		rep.MissingData = append(rep.MissingData, report.MissingData{
			Source:   "incident.evidence",
			Reason:   "未找到五类故障的明确签名，或证据文件不足",
			NextStep: "补充 k8s/events.json、logs/ 与 metrics/ 后重新分析",
		})
	}

	if !hasDir(b.Dir, "metrics") {
		rep.MissingData = append(rep.MissingData, report.MissingData{
			Source: "prometheus.metrics", Reason: "事故包没有 metrics/ 目录",
			NextStep: "导出故障前后 30 分钟的关键指标 query_range 结果",
		})
	}
	if !b.HasFile("k8s/events.json") {
		rep.MissingData = append(rep.MissingData, report.MissingData{
			Source: "k8s.events", Reason: "事故包没有 k8s/events.json",
			NextStep: "运行 kubectl get events -n <ns> -o json 并脱敏",
		})
	}
	rep.Limitations = []string{
		"v0.0.1 离线规则仅做签名匹配，正式评测前不得作为生产决策唯一依据。",
		"未接入模型；不读取集群、不访问网络。",
		"所有建议均为只读，不自动执行任何修复。",
	}
	return rep, nil
}

func rulesFor(statement string) []string {
	for _, r := range rules {
		if r.statement == statement {
			return r.checks
		}
	}
	return nil
}

func fixFor(statement string) string {
	for _, r := range rules {
		if r.statement == statement {
			return r.fix
		}
	}
	return "只读建议：补充证据后重新分析；不自动执行修复。"
}

func firstLine(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			if len(line) > 160 {
				return line[:160] + "…"
			}
			return line
		}
	}
	return "(empty file)"
}

func unique(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func hasDir(root, name string) bool {
	// filepath.Glob keeps this dependency-free and handles the empty case.
	m, _ := filepath.Glob(filepath.Join(root, name))
	return len(m) > 0
}
