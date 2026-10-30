package kubectl

import (
	"fmt"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

// deployment is the diagnostic subset of an apps/v1 Deployment.
type deployment struct {
	APIVersion string           `yaml:"apiVersion,omitempty"`
	Kind       string           `yaml:"kind,omitempty"`
	Metadata   metadata         `yaml:"metadata"`
	Spec       deploymentSpec   `yaml:"spec"`
	Status     deploymentStatus `yaml:"status"`
}

type deploymentSpec struct {
	Replicas *int32             `yaml:"replicas,omitempty"`
	Template deploymentTemplate `yaml:"template"`
}

type deploymentTemplate struct {
	Spec deploymentTemplateSpec `yaml:"spec"`
}

type deploymentTemplateSpec struct {
	Containers []container `yaml:"containers,omitempty"`
}

type deploymentStatus struct {
	ReadyReplicas      int32                 `yaml:"readyReplicas,omitempty"`
	UpdatedReplicas    int32                 `yaml:"updatedReplicas,omitempty"`
	ObservedGeneration int64                 `yaml:"observedGeneration,omitempty"`
	Conditions         []deploymentCondition `yaml:"conditions,omitempty"`
}

type deploymentCondition struct {
	Type           string `yaml:"type"`
	Status         string `yaml:"status"`
	Reason         string `yaml:"reason,omitempty"`
	Message        string `yaml:"message,omitempty"`
	LastUpdateTime string `yaml:"lastUpdateTime,omitempty"`
}

// deploymentList is the normalized k8s/deployments.yaml layout.
type deploymentList struct {
	APIVersion string       `yaml:"apiVersion"`
	Kind       string       `yaml:"kind"`
	Items      []deployment `yaml:"items"`
}

func trimDeployment(d deployment) deployment {
	d.APIVersion = "apps/v1"
	d.Kind = "Deployment"
	d.Spec.Template.Spec.Containers = trimContainers(d.Spec.Template.Spec.Containers)
	return d
}

func importDeployments(doc []byte) (*importers.Output, error) {
	var wrapper struct {
		Items []deployment `yaml:"items"`
	}
	if err := yamlUnmarshal(doc, &wrapper); err != nil {
		return nil, fmt.Errorf("kubectl: parse deployments: %w", err)
	}
	if wrapper.Items == nil {
		return nil, fmt.Errorf("kubectl: DeploymentList has no items")
	}
	for i := range wrapper.Items {
		wrapper.Items[i] = trimDeployment(wrapper.Items[i])
	}
	return writeDeployments(wrapper.Items)
}

func writeDeployments(items []deployment) (*importers.Output, error) {
	if items == nil {
		items = []deployment{}
	}
	data, err := importers.MarshalYAML(deploymentList{APIVersion: "v1", Kind: "List", Items: items})
	if err != nil {
		return nil, fmt.Errorf("kubectl: encode %s: %w", deploymentsFile, err)
	}
	return importers.Single(deploymentsFile, data), nil
}
