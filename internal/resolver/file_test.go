package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileResolver(t *testing.T) {
	dir := t.TempDir()
	taskYAML := `apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: my-task
spec:
  steps:
    - name: hello
      image: alpine
      script: echo hello
  workspaces:
    - name: source
`
	err := os.WriteFile(filepath.Join(dir, "my-task.yaml"), []byte(taskYAML), 0644)
	require.NoError(t, err)

	r := NewFileResolver(dir)
	spec, err := r.Resolve("my-task.yaml")
	require.NoError(t, err)

	steps, ok := spec["steps"].([]any)
	require.True(t, ok)
	assert.Len(t, steps, 1)

	ws := WorkspacesFromSpec(spec)
	assert.Len(t, ws, 1)
	assert.Equal(t, "source", ws[0].Name)
}

func TestFileResolverNotFound(t *testing.T) {
	r := NewFileResolver(t.TempDir())
	_, err := r.Resolve("nonexistent.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "nonexistent.yaml")
}

func TestIsFilePath(t *testing.T) {
	assert.True(t, isFilePath("./tasks/my-task.yaml"))
	assert.True(t, isFilePath("../tasks/my-task.yaml"))
	assert.True(t, isFilePath("/absolute/path/task.yaml"))
	assert.True(t, isFilePath(".tekton/tasks/task.yaml"))
	assert.True(t, isFilePath("tasks/task.yml"))
	assert.False(t, isFilePath("git-clone"))
	assert.False(t, isFilePath("git-clone:0.9"))
	assert.False(t, isFilePath("cluster://ns/task"))
	assert.False(t, isFilePath("https://example.com/task.yaml"))
}
