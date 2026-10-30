package kubectl

import (
	"fmt"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

// pod is the diagnostic subset of a core/v1 Pod. It also owns the apiVersion and
// kind fields so the normalized YAML keeps the per-item type that analyze uses to
// classify resources.
type pod struct {
	APIVersion string    `yaml:"apiVersion,omitempty"`
	Kind       string    `yaml:"kind,omitempty"`
	Metadata   metadata  `yaml:"metadata"`
	Spec       podSpec   `yaml:"spec"`
	Status     podStatus `yaml:"status"`
}

type podSpec struct {
	NodeName   string      `yaml:"nodeName,omitempty"`
	Containers []container `yaml:"containers,omitempty"`
}

type podStatus struct {
	Phase                 string            `yaml:"phase,omitempty"`
	ContainerStatuses     []containerStatus `yaml:"containerStatuses,omitempty"`
	InitContainerStatuses []containerStatus `yaml:"initContainerStatuses,omitempty"`
}

// containerStatus keeps the restart count plus current and last state, which is
// what CrashLoopBackOff/OOMKilled diagnosis needs.
type containerStatus struct {
	Name         string          `yaml:"name"`
	RestartCount int32           `yaml:"restartCount"`
	State        *containerState `yaml:"state,omitempty"`
	LastState    *containerState `yaml:"lastState,omitempty"`
}

// podList is the normalized k8s/pods.yaml layout: the kubectl-native generic List
// wrapper with each Pod trimmed to diagnostics.
type podList struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Items      []pod  `yaml:"items"`
}

func trimPod(p pod) pod {
	p.APIVersion = "v1"
	p.Kind = "Pod"
	p.Spec.Containers = trimContainers(p.Spec.Containers)
	p.Status.ContainerStatuses = trimStates(p.Status.ContainerStatuses)
	p.Status.InitContainerStatuses = trimStates(p.Status.InitContainerStatuses)
	return p
}

func trimStates(in []containerStatus) []containerStatus {
	for i := range in {
		if in[i].State.empty() {
			in[i].State = nil
		}
		if in[i].LastState.empty() {
			in[i].LastState = nil
		}
	}
	return in
}

func importPods(doc []byte) (*importers.Output, error) {
	var wrapper struct {
		Items []pod `yaml:"items"`
	}
	if err := yamlUnmarshal(doc, &wrapper); err != nil {
		return nil, fmt.Errorf("kubectl: parse pods: %w", err)
	}
	if wrapper.Items == nil {
		return nil, fmt.Errorf("kubectl: PodList has no items")
	}
	for i := range wrapper.Items {
		wrapper.Items[i] = trimPod(wrapper.Items[i])
	}
	return writePods(wrapper.Items)
}

func writePods(items []pod) (*importers.Output, error) {
	if items == nil {
		items = []pod{}
	}
	data, err := importers.MarshalYAML(podList{APIVersion: "v1", Kind: "List", Items: items})
	if err != nil {
		return nil, fmt.Errorf("kubectl: encode %s: %w", podsFile, err)
	}
	return importers.Single(podsFile, data), nil
}
