package resolver

import (
	"fmt"
	"os"
	"path/filepath"
)

// FileResolver resolves tasks from local YAML files relative to the DSL file.
type FileResolver struct {
	// BaseDir is the directory to resolve relative paths from (typically
	// the directory containing the DSL file).
	BaseDir string
}

// NewFileResolver creates a resolver that reads Task YAML from local files.
func NewFileResolver(baseDir string) *FileResolver {
	return &FileResolver{BaseDir: baseDir}
}

// Resolve reads a Tekton Task YAML file and returns its spec.
func (r *FileResolver) Resolve(uses string) (map[string]any, error) {
	path := uses
	if !filepath.IsAbs(path) {
		path = filepath.Join(r.BaseDir, path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading task file %q: %w", uses, err)
	}

	return ParseTaskSpec(data)
}
