package dsl

import "gopkg.in/yaml.v3"

// Pipeline is the top-level DSL intermediate representation.
type Pipeline struct {
	Name        string              `yaml:"name"`
	On          *OnTrigger          `yaml:"on,omitempty"`
	Params      map[string]*Param   `yaml:"params,omitempty"`
	Secrets     map[string]string   `yaml:"secrets,omitempty"`
	Tasks       map[string]*Task    `yaml:"tasks,omitempty"`
	Finally     map[string]*Task    `yaml:"finally,omitempty"`
	Defaults    *Defaults           `yaml:"defaults,omitempty"`
	Storage     *Storage            `yaml:"storage,omitempty"`
	Cache       *Cache              `yaml:"cache,omitempty"`
	Concurrency *Concurrency        `yaml:"concurrency,omitempty"`
	Cleanup     *Cleanup            `yaml:"cleanup,omitempty"`
	Tekton      map[string]any      `yaml:"tekton,omitempty"`

	// Declaration order (set by parser, not from YAML).
	TaskOrder    []string `yaml:"-"`
	FinallyOrder []string `yaml:"-"`

	// Source location tracking (not from YAML)
	SourceFile string `yaml:"-"`
}

// OnTrigger represents the `on:` block for Git event triggers.
type OnTrigger struct {
	PullRequest *EventFilter `yaml:"pull_request,omitempty"`
	Push        *EventFilter `yaml:"push,omitempty"`
	Comment     string       `yaml:"comment,omitempty"`
	CEL         string       `yaml:"cel,omitempty"`
}

// EventFilter represents branch/path filters for an event type.
type EventFilter struct {
	Branches    []string `yaml:"branches,omitempty"`
	Paths       []string `yaml:"paths,omitempty"`
	PathsIgnore []string `yaml:"paths_ignore,omitempty"`
	Labels      []string `yaml:"labels,omitempty"`
}

// Param represents a pipeline parameter (supports shorthand and full form).
type Param struct {
	Description string `yaml:"description,omitempty"`
	Type        string `yaml:"type,omitempty"`
	Default     any    `yaml:"default,omitempty"`
}

// UnmarshalYAML handles param shorthand: bare value = default.
// Examples:
//
//	param-name: "latest"        → Param{Default: "latest"}
//	param-name: 42              → Param{Default: 42}
//	param-name: [a, b]          → Param{Default: [a, b]}  (array, type inferred)
//	param-name:
//	  description: "..."
//	  default: "value"          → full form
func (p *Param) UnmarshalYAML(node *yaml.Node) error {
	// Bare scalar → default value.
	if node.Kind == yaml.ScalarNode {
		p.Default = node.Value
		return nil
	}
	// Bare sequence → default array value.
	if node.Kind == yaml.SequenceNode {
		var list []any
		if err := node.Decode(&list); err != nil {
			return err
		}
		p.Default = list
		return nil
	}
	type paramAlias Param
	var alias paramAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*p = Param(alias)
	return nil
}

// Task represents a task in the pipeline.
type Task struct {
	Uses       string               `yaml:"uses,omitempty"`
	Image      string               `yaml:"image,omitempty"`
	Run        string               `yaml:"run,omitempty"`
	Steps      []Step               `yaml:"steps,omitempty"`
	Needs      []string             `yaml:"needs,omitempty"`
	If         string               `yaml:"if,omitempty"`
	Params     map[string]any       `yaml:"params,omitempty"`
	Workspaces map[string]string    `yaml:"workspaces,omitempty"`
	Cache      *TaskCache           `yaml:"cache,omitempty"`
	Results    map[string]*Result   `yaml:"results,omitempty"`
	Env        map[string]string    `yaml:"env,omitempty"`
	Timeout    string               `yaml:"timeout,omitempty"`
	Retries    int                  `yaml:"retries,omitempty"`
	Sidecars   []Sidecar            `yaml:"sidecars,omitempty"`
	Approval   *Approval            `yaml:"approval,omitempty"`
	Tekton     map[string]any       `yaml:"tekton,omitempty"`

	BeforeRun *Hook `yaml:"before_run,omitempty"`
	AfterRun  *Hook `yaml:"after_run,omitempty"`
	Resources *Resources `yaml:"resources,omitempty"`

	// Set by parser, not from YAML
	Name string `yaml:"-"`
}

// TaskCache defines cache paths for a specific task.
// Cache is restored before the task runs and uploaded after it completes.
type TaskCache struct {
	Paths []CachePath `yaml:"paths,omitempty"`

	// Shorthand fields (single cache entry).
	Path string   `yaml:"path,omitempty"`
	Key  []string `yaml:"key,omitempty"`
}

// EffectivePaths returns the normalized list of cache paths for a task.
func (tc *TaskCache) EffectivePaths() []CachePath {
	if tc == nil {
		return nil
	}
	if len(tc.Paths) > 0 {
		return tc.Paths
	}
	if tc.Path != "" {
		return []CachePath{{Path: tc.Path, Key: tc.Key}}
	}
	return nil
}

// Step represents a single step within a multi-step task.
type Step struct {
	Name      string         `yaml:"name"`
	Image     string         `yaml:"image,omitempty"`
	Run       string         `yaml:"run"`
	Env       map[string]string `yaml:"env,omitempty"`
	Resources *Resources     `yaml:"resources,omitempty"`
	Tekton    map[string]any `yaml:"tekton,omitempty"`
}

// Result represents a task result declaration.
type Result struct {
	Description string `yaml:"description,omitempty"`
}

// Sidecar represents a sidecar container for a task.
type Sidecar struct {
	Name  string            `yaml:"name"`
	Image string            `yaml:"image"`
	Ports []int             `yaml:"ports,omitempty"`
	Env   map[string]string `yaml:"env,omitempty"`
}

// Approval represents a manual approval gate on a task.
type Approval struct {
	Approvers   []string `yaml:"approvers"`
	Required    int      `yaml:"required,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Timeout     string   `yaml:"timeout,omitempty"`
}

// UnmarshalYAML handles approval shorthand: sequence of strings = approvers list.
func (a *Approval) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var approvers []string
		if err := node.Decode(&approvers); err != nil {
			return err
		}
		a.Approvers = approvers
		a.Required = 1
		a.Timeout = "60m"
		return nil
	}
	type approvalAlias Approval
	var alias approvalAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*a = Approval(alias)
	return nil
}

// Hook represents a before_run or after_run hook script.
// Supports both string shorthand (just the script) and struct form (image + run).
//
//	before_run: echo "hello"                    → Hook{Run: "echo \"hello\""}
//	before_run:
//	  image: redhat/ubi9-minimal
//	  run: echo "hello"                         → Hook{Image: "redhat/ubi9-minimal", Run: "echo \"hello\""}
type Hook struct {
	Image string `yaml:"image,omitempty"`
	Run   string `yaml:"run,omitempty"`
}

// UnmarshalYAML handles hook shorthand: bare string = script only.
func (h *Hook) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		h.Run = node.Value
		return nil
	}
	type hookAlias Hook
	var alias hookAlias
	if err := node.Decode(&alias); err != nil {
		return err
	}
	*h = Hook(alias)
	return nil
}

// Script returns the hook's script text (empty if no hook).
func (h *Hook) Script() string {
	if h == nil {
		return ""
	}
	return h.Run
}

// Defaults represents pipeline-wide defaults for inline tasks.
type Defaults struct {
	Image     string `yaml:"image,omitempty"`
	BeforeRun *Hook  `yaml:"before_run,omitempty"`
	AfterRun  *Hook  `yaml:"after_run,omitempty"`
}

// Storage customizes the shared workspace volume.
type Storage struct {
	Size         string `yaml:"size,omitempty"`
	StorageClass string `yaml:"storageClass,omitempty"`
}

// Cache represents the top-level cache configuration.
// Cache paths are defined at the task level via task.cache.
type Cache struct {
	// Image is the OCI image URL used for caching. Cache entries are stored
	// as tags on this image (e.g. oci://quay.io/org/my-cache:m2-{{hash}}).
	Image       string       `yaml:"image"`
	Credentials string       `yaml:"credentials,omitempty"`
	Insecure    bool         `yaml:"insecure,omitempty"`

	// Deprecated: use Image instead. Kept for backward compat.
	Backend     string       `yaml:"backend,omitempty"`
	// Deprecated: use task-level cache instead.
	Paths       []CachePath  `yaml:"paths,omitempty"`
	Path        string       `yaml:"path,omitempty"`
	Key         []string     `yaml:"key,omitempty"`
}

// EffectiveImage returns the cache image URL, falling back to Backend for compat.
func (c *Cache) EffectiveImage() string {
	if c.Image != "" {
		return c.Image
	}
	return c.Backend
}

// CachePath represents a single cache path entry.
type CachePath struct {
	Path  string   `yaml:"path"`
	Key   []string `yaml:"key"`
}

// Concurrency controls parallel pipeline runs.
type Concurrency struct {
	CancelInProgress bool `yaml:"cancel-in-progress,omitempty"`
}

// Cleanup controls PipelineRun retention.
type Cleanup struct {
	MaxKeepRuns int `yaml:"max-keep-runs,omitempty"`
}

// Resources specifies compute resources for a step.
type Resources struct {
	CPU    string `yaml:"cpu,omitempty"`
	Memory string `yaml:"memory,omitempty"`
}

// EffectiveCachePaths returns the normalized list of cache paths,
// handling both shorthand (single path/key) and full form (paths array).
func (c *Cache) EffectiveCachePaths() []CachePath {
	if c == nil {
		return nil
	}
	if len(c.Paths) > 0 {
		return c.Paths
	}
	if c.Path != "" {
		return []CachePath{{Path: c.Path, Key: c.Key}}
	}
	return nil
}
