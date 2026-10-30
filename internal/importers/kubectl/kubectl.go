// Package kubectl imports kubectl get -o json|yaml snapshots into the incident
// bundle standard k8s/ files.
//
// Supported input kinds: EventList/Event, PodList/Pod, DeploymentList/Deployment,
// plus the generic List that kubectl emits for multi-object output. Each form is
// normalized so the same objects always produce byte-identical files:
//
//	EventList/Event                  -> k8s/events.json     (JSON EventList)
//	PodList/Pod/List of Pod          -> k8s/pods.yaml       (YAML List, one item per Pod)
//	DeploymentList/Deployment/List   -> k8s/deployments.yaml (YAML List, one item per Deployment)
//
// Only diagnostic fields are kept (see the type definitions in this package).
// Output layout is fixed: struct fields are emitted in declaration order and maps
// are encoded with sorted keys, so identical input produces identical output.
package kubectl

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/paas-ops/kubectl-triage/internal/importers"
)

const (
	sourceName      = "kubectl"
	eventsFile      = "k8s/events.json"
	podsFile        = "k8s/pods.yaml"
	deploymentsFile = "k8s/deployments.yaml"
)

func init() { importers.Register(importer{}) }

type importer struct{}

func (importer) Name() string        { return sourceName }
func (importer) DefaultName() string { return "" }

func (importer) Import(in importers.Input) (*importers.Output, error) { return Import(in) }

// Import converts one kubectl snapshot into the matching bundle file.
func Import(in importers.Input) (*importers.Output, error) {
	doc := bytes.TrimSpace(in.Data)
	if len(doc) == 0 {
		return nil, fmt.Errorf("kubectl: empty input")
	}
	kind, err := peekKind(doc)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "EventList":
		return importEvents(doc)
	case "Event":
		item, err := decodeOne[event](doc, "Event")
		if err != nil {
			return nil, err
		}
		return writeEvents([]event{item})
	case "PodList":
		return importPods(doc)
	case "Pod":
		item, err := decodeOne[pod](doc, "Pod")
		if err != nil {
			return nil, err
		}
		return writePods([]pod{trimPod(item)})
	case "DeploymentList":
		return importDeployments(doc)
	case "Deployment":
		item, err := decodeOne[deployment](doc, "Deployment")
		if err != nil {
			return nil, err
		}
		return writeDeployments([]deployment{trimDeployment(item)})
	case "List":
		return importGenericList(doc)
	default:
		return nil, fmt.Errorf("kubectl: unsupported kind %q (want EventList|Event|PodList|Pod|DeploymentList|Deployment)", kind)
	}
}

func peekKind(doc []byte) (string, error) {
	var hdr struct {
		Kind string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(doc, &hdr); err != nil {
		return "", fmt.Errorf("kubectl: parse input: %w", err)
	}
	if hdr.Kind == "" {
		return "", fmt.Errorf("kubectl: input has no kind; export with kubectl get RESOURCE -o json|yaml")
	}
	return hdr.Kind, nil
}

// yamlUnmarshal decodes JSON or YAML into out. kubectl may emit either format and
// YAML is a superset of JSON, so a single decoder with yaml tags matching the
// Kubernetes API keys handles both deterministically.
func yamlUnmarshal(doc []byte, out any) error {
	return yaml.Unmarshal(doc, out)
}

func decodeOne[T any](doc []byte, kind string) (T, error) {
	var out T
	if err := yaml.Unmarshal(doc, &out); err != nil {
		return out, fmt.Errorf("kubectl: parse %s: %w", kind, err)
	}
	return out, nil
}

// importGenericList handles the kind: List wrapper kubectl emits for
// multi-object output. The resource kind is inferred from the items; mixed or
// empty lists are rejected because the target bundle file would be ambiguous.
func importGenericList(doc []byte) (*importers.Output, error) {
	var probe struct {
		Items []struct {
			Kind string `yaml:"kind"`
		} `yaml:"items"`
	}
	if err := yaml.Unmarshal(doc, &probe); err != nil {
		return nil, fmt.Errorf("kubectl: parse List: %w", err)
	}
	if len(probe.Items) == 0 {
		return nil, fmt.Errorf("kubectl: List has no items; cannot infer the resource kind, export one resource type instead")
	}
	kind := probe.Items[0].Kind
	if kind == "" {
		return nil, fmt.Errorf("kubectl: List item 0 has no kind; export a typed list (PodList/EventList/DeploymentList) or a single object")
	}
	for i, item := range probe.Items {
		if item.Kind != kind {
			return nil, fmt.Errorf("kubectl: List mixes kinds %q and %q (item %d); import one resource type at a time", kind, item.Kind, i)
		}
	}
	switch kind {
	case "Event":
		return importEvents(doc)
	case "Pod":
		return importPods(doc)
	case "Deployment":
		return importDeployments(doc)
	default:
		return nil, fmt.Errorf("kubectl: unsupported kind %q in List (want Event|Pod|Deployment)", kind)
	}
}
