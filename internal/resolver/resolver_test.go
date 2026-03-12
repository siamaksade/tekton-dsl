package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUses(t *testing.T) {
	tests := []struct {
		input   string
		name    string
		version string
	}{
		{"git-clone", "git-clone", ""},
		{"git-clone:0.9", "git-clone", "0.9"},
		{"maven:0.4.0", "maven", "0.4.0"},
		{"https://example.com/task.yaml", "https://example.com/task.yaml", ""},
		{"http://example.com/task.yaml", "http://example.com/task.yaml", ""},
	}
	for _, tt := range tests {
		name, version := ParseUses(tt.input)
		assert.Equal(t, tt.name, name, "ParseUses(%q) name", tt.input)
		assert.Equal(t, tt.version, version, "ParseUses(%q) version", tt.input)
	}
}

func TestParseTaskSpec(t *testing.T) {
	yaml := `apiVersion: tekton.dev/v1
kind: Task
metadata:
  name: git-clone
spec:
  params:
    - name: url
      type: string
    - name: revision
      type: string
      default: ""
  steps:
    - name: clone
      image: gcr.io/tekton-releases/github.com/tektoncd/pipeline/cmd/git-init:v0.40.2
      script: |
        git clone $(params.url) $(workspaces.output.path)
  workspaces:
    - name: output
      description: The git repo will be cloned onto this workspace
    - name: ssh-directory
      optional: true
`
	spec, err := ParseTaskSpec([]byte(yaml))
	require.NoError(t, err)
	require.NotNil(t, spec)

	// Check params exist.
	params, ok := spec["params"].([]any)
	require.True(t, ok)
	assert.Len(t, params, 2)

	// Check steps exist.
	steps, ok := spec["steps"].([]any)
	require.True(t, ok)
	assert.Len(t, steps, 1)

	// Check workspaces.
	workspaces := WorkspacesFromSpec(spec)
	require.Len(t, workspaces, 2)
	assert.Equal(t, "output", workspaces[0].Name)
	assert.False(t, workspaces[0].Optional)
	assert.Equal(t, "ssh-directory", workspaces[1].Name)
	assert.True(t, workspaces[1].Optional)
}

func TestParseTaskSpecNoSpec(t *testing.T) {
	yaml := `apiVersion: v1
kind: ConfigMap
metadata:
  name: test
`
	_, err := ParseTaskSpec([]byte(yaml))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no spec field")
}

func TestWorkspacesFromSpecEmpty(t *testing.T) {
	spec := map[string]any{
		"steps": []any{},
	}
	workspaces := WorkspacesFromSpec(spec)
	assert.Nil(t, workspaces)
}
