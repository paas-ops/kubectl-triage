package kubectl

// metadata is the diagnostic subset of ObjectMeta.
type metadata struct {
	Name              string            `yaml:"name,omitempty"`
	Namespace         string            `yaml:"namespace,omitempty"`
	Labels            map[string]string `yaml:"labels,omitempty"`
	Annotations       map[string]string `yaml:"annotations,omitempty"`
	CreationTimestamp string            `yaml:"creationTimestamp,omitempty"`
	OwnerReferences   []ownerReference  `yaml:"ownerReferences,omitempty"`
}

// ownerReference is the diagnostic subset of ObjectMeta.ownerReferences; the
// controller reference tells which ReplicaSet/Deployment owns the object.
type ownerReference struct {
	APIVersion string `yaml:"apiVersion,omitempty"`
	Kind       string `yaml:"kind,omitempty"`
	Name       string `yaml:"name,omitempty"`
	UID        string `yaml:"uid,omitempty"`
	Controller *bool  `yaml:"controller,omitempty"`
}

// container is the diagnostic subset of a container spec.
type container struct {
	Name      string     `yaml:"name"`
	Image     string     `yaml:"image,omitempty"`
	Resources *resources `yaml:"resources,omitempty"`
}

// resources keeps requests/limits only; other resource fields are not diagnostic.
type resources struct {
	Limits   map[string]string `yaml:"limits,omitempty"`
	Requests map[string]string `yaml:"requests,omitempty"`
}

func (r *resources) empty() bool {
	return r == nil || (len(r.Limits) == 0 && len(r.Requests) == 0)
}

func trimContainers(in []container) []container {
	for i := range in {
		if in[i].Resources.empty() {
			in[i].Resources = nil
		}
	}
	return in
}

// containerState is the diagnostic subset of ContainerState.
type containerState struct {
	Waiting    *stateWaiting    `yaml:"waiting,omitempty"`
	Running    *stateRunning    `yaml:"running,omitempty"`
	Terminated *stateTerminated `yaml:"terminated,omitempty"`
}

type stateWaiting struct {
	Reason  string `yaml:"reason,omitempty"`
	Message string `yaml:"message,omitempty"`
}

type stateRunning struct {
	StartedAt string `yaml:"startedAt,omitempty"`
}

type stateTerminated struct {
	Reason     string `yaml:"reason,omitempty"`
	Message    string `yaml:"message,omitempty"`
	ExitCode   int32  `yaml:"exitCode"`
	StartedAt  string `yaml:"startedAt,omitempty"`
	FinishedAt string `yaml:"finishedAt,omitempty"`
}

func (s *containerState) empty() bool {
	return s == nil || (s.Waiting == nil && s.Running == nil && s.Terminated == nil)
}
