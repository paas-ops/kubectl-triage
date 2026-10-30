package kubectl

import (
	"fmt"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

// event is the diagnostic subset of a core/v1 Event.
type event struct {
	Type           string    `yaml:"type" json:"type,omitempty"`
	Reason         string    `yaml:"reason" json:"reason,omitempty"`
	Message        string    `yaml:"message" json:"message,omitempty"`
	InvolvedObject objectRef `yaml:"involvedObject" json:"involvedObject"`
	Count          int32     `yaml:"count" json:"count,omitempty"`
	FirstTimestamp string    `yaml:"firstTimestamp" json:"firstTimestamp,omitempty"`
	LastTimestamp  string    `yaml:"lastTimestamp" json:"lastTimestamp,omitempty"`
}

// objectRef identifies the object an Event refers to.
type objectRef struct {
	Kind      string `yaml:"kind" json:"kind,omitempty"`
	Namespace string `yaml:"namespace" json:"namespace,omitempty"`
	Name      string `yaml:"name" json:"name,omitempty"`
}

// eventList is the normalized k8s/events.json layout.
type eventList struct {
	APIVersion string  `json:"apiVersion"`
	Kind       string  `json:"kind"`
	Items      []event `json:"items"`
}

func importEvents(doc []byte) (*importers.Output, error) {
	var wrapper struct {
		Items []event `yaml:"items"`
	}
	if err := yamlUnmarshal(doc, &wrapper); err != nil {
		return nil, fmt.Errorf("kubectl: parse events: %w", err)
	}
	if wrapper.Items == nil {
		return nil, fmt.Errorf("kubectl: EventList has no items")
	}
	return writeEvents(wrapper.Items)
}

func writeEvents(items []event) (*importers.Output, error) {
	if items == nil {
		items = []event{}
	}
	data, err := importers.MarshalJSON(eventList{APIVersion: "v1", Kind: "EventList", Items: items})
	if err != nil {
		return nil, fmt.Errorf("kubectl: encode %s: %w", eventsFile, err)
	}
	return importers.Single(eventsFile, data), nil
}
