package tekton

// PipelineRun is a lightweight representation of a Tekton PipelineRun CR.
type PipelineRun struct {
	APIVersion string            `yaml:"apiVersion" json:"apiVersion"`
	Kind       string            `yaml:"kind" json:"kind"`
	Metadata   Metadata          `yaml:"metadata" json:"metadata"`
	Spec       PipelineRunSpec   `yaml:"spec" json:"spec"`
}

// Metadata holds standard Kubernetes object metadata fields.
type Metadata struct {
	Name        string            `yaml:"name" json:"name"`
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
}

// PipelineRunSpec is the spec of a PipelineRun.
type PipelineRunSpec struct {
	PipelineSpec *PipelineSpec       `yaml:"pipelineSpec,omitempty" json:"pipelineSpec,omitempty"`
	Params       []RunParam          `yaml:"params,omitempty" json:"params,omitempty"`
	Workspaces   []WorkspaceBinding  `yaml:"workspaces,omitempty" json:"workspaces,omitempty"`

	// TektonRaw holds pass-through fields from tekton.pipelineRun: block.
	TektonRaw    map[string]any      `yaml:",inline" json:"-"`
}

// RunParam is a parameter value in a PipelineRun.
type RunParam struct {
	Name  string `yaml:"name" json:"name"`
	Value any    `yaml:"value" json:"value"`
}

// PipelineSpec is the inline pipeline specification.
type PipelineSpec struct {
	Params     []ParamSpec         `yaml:"params,omitempty" json:"params,omitempty"`
	Workspaces []WorkspaceDecl     `yaml:"workspaces,omitempty" json:"workspaces,omitempty"`
	Tasks      []PipelineTask      `yaml:"tasks,omitempty" json:"tasks,omitempty"`
	Finally    []PipelineTask      `yaml:"finally,omitempty" json:"finally,omitempty"`
}

// ParamSpec defines a pipeline parameter.
type ParamSpec struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Default     any    `yaml:"default,omitempty" json:"default,omitempty"`
}

// WorkspaceDecl declares a workspace in a pipeline.
type WorkspaceDecl struct {
	Name string `yaml:"name" json:"name"`
}

// PipelineTask is a task within a pipeline.
type PipelineTask struct {
	Name       string              `yaml:"name" json:"name"`
	TaskRef    *TaskRef            `yaml:"taskRef,omitempty" json:"taskRef,omitempty"`
	TaskSpec   *TaskSpec           `yaml:"taskSpec,omitempty" json:"taskSpec,omitempty"`
	RunAfter   []string            `yaml:"runAfter,omitempty" json:"runAfter,omitempty"`
	Params     []TaskParam         `yaml:"params,omitempty" json:"params,omitempty"`
	When       []WhenExpression    `yaml:"when,omitempty" json:"when,omitempty"`
	Workspaces []WorkspaceRef      `yaml:"workspaces,omitempty" json:"workspaces,omitempty"`
	Timeout    string              `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retries    int                 `yaml:"retries,omitempty" json:"retries,omitempty"`
}

// TaskRef references an external task.
type TaskRef struct {
	Name       string `yaml:"name,omitempty" json:"name,omitempty"`
	Kind       string `yaml:"kind,omitempty" json:"kind,omitempty"`
	APIVersion string `yaml:"apiVersion,omitempty" json:"apiVersion,omitempty"`
}

// TaskSpec is an inline task specification.
type TaskSpec struct {
	// Raw holds the complete task spec when inlining a resolved external task.
	// When set, this is marshaled directly; all other fields are ignored.
	Raw map[string]any `yaml:"-" json:"-"`

	Workspaces []WorkspaceDecl `yaml:"workspaces,omitempty" json:"workspaces,omitempty"`
	Params     []ParamSpec     `yaml:"params,omitempty" json:"params,omitempty"`
	Results    []TaskResult    `yaml:"results,omitempty" json:"results,omitempty"`
	Steps      []Step          `yaml:"steps" json:"steps"`
	Sidecars   []Sidecar       `yaml:"sidecars,omitempty" json:"sidecars,omitempty"`

	// TektonRaw holds pass-through fields merged directly into the generated taskSpec.
	TektonRaw map[string]any `yaml:",inline" json:"-"`
}

// MarshalYAML implements custom marshaling for TaskSpec.
// If Raw is set (resolved external task), it is marshaled directly.
// Otherwise, the struct fields are marshaled normally.
func (ts TaskSpec) MarshalYAML() (interface{}, error) {
	if ts.Raw != nil {
		return ts.Raw, nil
	}
	type plain TaskSpec
	return plain(ts), nil
}

// TaskResult declares a result produced by a task.
type TaskResult struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Step is a single step within a task.
type Step struct {
	Name       string            `yaml:"name" json:"name"`
	Image      string            `yaml:"image,omitempty" json:"image,omitempty"`
	Script     string            `yaml:"script,omitempty" json:"script,omitempty"`
	Ref        *StepRef          `yaml:"ref,omitempty" json:"ref,omitempty"`
	StepParams []StepParam       `yaml:"params,omitempty" json:"params,omitempty"`
	Env        []EnvVar          `yaml:"env,omitempty" json:"env,omitempty"`
	OnError    string            `yaml:"onError,omitempty" json:"onError,omitempty"`
	Resources  *StepResources    `yaml:"resources,omitempty" json:"resources,omitempty"`

	// TektonRaw holds pass-through fields merged directly into the generated step.
	TektonRaw  map[string]any    `yaml:",inline" json:"-"`
}

// StepRef references a StepAction by name.
type StepRef struct {
	Name string `yaml:"name" json:"name"`
}

// StepParam is a parameter passed to a StepAction.
type StepParam struct {
	Name  string `yaml:"name" json:"name"`
	Value any    `yaml:"value" json:"value"`
}

// EnvVar is an environment variable.
type EnvVar struct {
	Name  string `yaml:"name" json:"name"`
	Value string `yaml:"value" json:"value"`
}

// StepResources defines resource requests/limits for a step.
type StepResources struct {
	Requests map[string]string `yaml:"requests,omitempty" json:"requests,omitempty"`
	Limits   map[string]string `yaml:"limits,omitempty" json:"limits,omitempty"`
}

// Sidecar is a sidecar container for a task.
type Sidecar struct {
	Name  string   `yaml:"name" json:"name"`
	Image string   `yaml:"image" json:"image"`
	Ports []Port   `yaml:"ports,omitempty" json:"ports,omitempty"`
	Env   []EnvVar `yaml:"env,omitempty" json:"env,omitempty"`
}

// Port is a container port.
type Port struct {
	ContainerPort int `yaml:"containerPort" json:"containerPort"`
}

// TaskParam is a parameter passed to a task.
type TaskParam struct {
	Name  string `yaml:"name" json:"name"`
	Value any    `yaml:"value" json:"value"`
}

// WhenExpression is a Tekton WhenExpression.
type WhenExpression struct {
	Input    string   `yaml:"input" json:"input"`
	Operator string   `yaml:"operator" json:"operator"`
	Values   []string `yaml:"values" json:"values"`
}

// WorkspaceRef binds a workspace in a pipeline task.
type WorkspaceRef struct {
	Name      string `yaml:"name" json:"name"`
	Workspace string `yaml:"workspace" json:"workspace"`
}

// WorkspaceBinding binds a workspace in a PipelineRun.
type WorkspaceBinding struct {
	Name                string               `yaml:"name" json:"name"`
	VolumeClaimTemplate *VolumeClaimTemplate `yaml:"volumeClaimTemplate,omitempty" json:"volumeClaimTemplate,omitempty"`
	Secret              *SecretWorkspace     `yaml:"secret,omitempty" json:"secret,omitempty"`
}

// VolumeClaimTemplate defines a PVC template for workspace storage.
type VolumeClaimTemplate struct {
	Spec VCTSpec `yaml:"spec" json:"spec"`
}

// VCTSpec is the spec within a VolumeClaimTemplate.
type VCTSpec struct {
	AccessModes []string    `yaml:"accessModes" json:"accessModes"`
	Resources   VCTResources `yaml:"resources" json:"resources"`
	StorageClassName *string `yaml:"storageClassName,omitempty" json:"storageClassName,omitempty"`
}

// VCTResources contains resource requests for a VCT.
type VCTResources struct {
	Requests map[string]string `yaml:"requests" json:"requests"`
}

// SecretWorkspace binds a Kubernetes Secret as a workspace.
type SecretWorkspace struct {
	SecretName string `yaml:"secretName" json:"secretName"`
}

// NewPipelineRun creates a new PipelineRun with standard fields set.
func NewPipelineRun(name string) *PipelineRun {
	return &PipelineRun{
		APIVersion: "tekton.dev/v1",
		Kind:       "PipelineRun",
		Metadata: Metadata{
			Name: name,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "tkn-dsl",
			},
		},
		Spec: PipelineRunSpec{
			PipelineSpec: &PipelineSpec{},
		},
	}
}
