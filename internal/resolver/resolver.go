package resolver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// TaskResolver resolves external task references into inline task specs.
type TaskResolver interface {
	// Resolve fetches a Tekton Task by its uses reference and returns the task spec
	// as a raw map, suitable for inlining as taskSpec in a PipelineTask.
	Resolve(uses string) (map[string]any, error)
}

// HubResolver resolves tasks from the Artifact Hub API (same source PaC uses).
// API pattern (matches PaC's artifactHubClient):
//   - Latest:  GET {baseURL}/packages/tekton-task/{catalog}/{name}
//   - Version: GET {baseURL}/packages/tekton-task/{catalog}/{name}/{version}
//
// Response: { "data": { "manifestRaw": "<Task YAML>" } }
type HubResolver struct {
	BaseURL    string
	Catalog    string
	HTTPClient *http.Client
}

// NewHubResolver creates a resolver that fetches tasks from the Artifact Hub,
// using the same API and catalog that Pipelines-as-Code uses by default.
func NewHubResolver() *HubResolver {
	return &HubResolver{
		BaseURL:    "https://artifacthub.io/api/v1",
		Catalog:    "tekton-catalog-tasks",
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Resolve fetches a task definition and returns its spec as a map.
// Supported uses formats:
//   - "task-name"          → latest version from Artifact Hub
//   - "task-name:0.9"      → specific version from Artifact Hub
//   - "https://example.com/task.yaml" → direct URL fetch (raw Task YAML)
func (r *HubResolver) Resolve(uses string) (map[string]any, error) {
	var taskYAML []byte
	var err error

	if strings.HasPrefix(uses, "https://") || strings.HasPrefix(uses, "http://") {
		taskYAML, err = r.fetchURL(uses)
	} else {
		taskYAML, err = r.fetchFromHub(uses)
	}
	if err != nil {
		return nil, err
	}

	return ParseTaskSpec(taskYAML)
}

func (r *HubResolver) fetchFromHub(uses string) ([]byte, error) {
	name, version := ParseUses(uses)

	var url string
	if version != "" {
		url = fmt.Sprintf("%s/packages/tekton-task/%s/%s/%s", r.BaseURL, r.Catalog, name, version)
	} else {
		url = fmt.Sprintf("%s/packages/tekton-task/%s/%s", r.BaseURL, r.Catalog, name)
	}

	body, err := r.fetchURL(url)
	if err != nil {
		return nil, fmt.Errorf("fetching task %q from hub: %w", uses, err)
	}

	// Artifact Hub wraps the manifest in a JSON response.
	var resp struct {
		Data struct {
			ManifestRaw string `json:"manifestRaw"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing hub response for %q: %w", uses, err)
	}
	if resp.Data.ManifestRaw == "" {
		return nil, fmt.Errorf("empty manifest in hub response for task %q", uses)
	}

	return []byte(resp.Data.ManifestRaw), nil
}

func (r *HubResolver) fetchURL(url string) ([]byte, error) {
	resp, err := r.HTTPClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, url, string(body))
	}

	return io.ReadAll(resp.Body)
}

// ParseUses splits a uses reference into name and version.
// "git-clone:0.9" → ("git-clone", "0.9")
// "git-clone"     → ("git-clone", "")
// "https://..."   → ("https://...", "")
func ParseUses(uses string) (string, string) {
	if strings.HasPrefix(uses, "https://") || strings.HasPrefix(uses, "http://") {
		return uses, ""
	}
	if idx := strings.Index(uses, ":"); idx > 0 && !strings.Contains(uses[:idx], "/") {
		return uses[:idx], uses[idx+1:]
	}
	return uses, ""
}

// ParseTaskSpec extracts the spec from a Tekton Task YAML document.
func ParseTaskSpec(data []byte) (map[string]any, error) {
	var task map[string]any
	if err := yaml.Unmarshal(data, &task); err != nil {
		return nil, fmt.Errorf("parsing task YAML: %w", err)
	}

	spec, ok := task["spec"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("task YAML has no spec field")
	}

	return spec, nil
}

// TaskWorkspace holds the name and optional flag of a task workspace.
type TaskWorkspace struct {
	Name     string
	Optional bool
}

// WorkspacesFromSpec extracts workspace declarations from a parsed task spec.
func WorkspacesFromSpec(spec map[string]any) []TaskWorkspace {
	wsList, ok := spec["workspaces"].([]any)
	if !ok {
		return nil
	}
	var workspaces []TaskWorkspace
	for _, ws := range wsList {
		wsMap, ok := ws.(map[string]any)
		if !ok {
			continue
		}
		name, ok := wsMap["name"].(string)
		if !ok {
			continue
		}
		optional, _ := wsMap["optional"].(bool)
		workspaces = append(workspaces, TaskWorkspace{Name: name, Optional: optional})
	}
	return workspaces
}
