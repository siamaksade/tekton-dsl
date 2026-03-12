package resolver

import (
	"fmt"
	"strings"
)

// CompositeResolver routes task references to the appropriate resolver
// based on the uses: format.
//
// Supported formats:
//   - "./path/to/task.yaml"               → FileResolver (local file in repo)
//   - "cluster://namespace/task-name"     → ClusterResolver (fetch from k8s cluster)
//   - "https://example.com/task.yaml"     → HubResolver (direct URL fetch)
//   - "task-name" or "task-name:version"  → HubResolver (Artifact Hub)
type CompositeResolver struct {
	Hub     *HubResolver
	Cluster *ClusterResolver
	File    *FileResolver
}

// NewCompositeResolver creates a resolver that routes to Hub, Cluster, or File
// based on the uses: format.
func NewCompositeResolver(baseDir string) *CompositeResolver {
	return &CompositeResolver{
		Hub:     NewHubResolver(),
		Cluster: NewClusterResolver(),
		File:    NewFileResolver(baseDir),
	}
}

// isFilePath returns true if the uses reference looks like a local file path.
func isFilePath(uses string) bool {
	if strings.HasPrefix(uses, "https://") || strings.HasPrefix(uses, "http://") ||
		strings.HasPrefix(uses, "cluster://") {
		return false
	}
	return strings.HasPrefix(uses, "./") ||
		strings.HasPrefix(uses, "../") ||
		strings.HasPrefix(uses, "/") ||
		strings.HasSuffix(uses, ".yaml") ||
		strings.HasSuffix(uses, ".yml")
}

func (r *CompositeResolver) Resolve(uses string) (map[string]any, error) {
	if isFilePath(uses) {
		if r.File == nil {
			return nil, fmt.Errorf("file resolver not configured for %q", uses)
		}
		return r.File.Resolve(uses)
	}

	if strings.HasPrefix(uses, "cluster://") {
		if r.Cluster == nil {
			return nil, fmt.Errorf("cluster resolver not configured for %q", uses)
		}
		ref := strings.TrimPrefix(uses, "cluster://")
		return r.Cluster.Resolve(ref)
	}

	if r.Hub == nil {
		return nil, fmt.Errorf("hub resolver not configured for %q", uses)
	}
	return r.Hub.Resolve(uses)
}
