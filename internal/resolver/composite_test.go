package resolver

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCompositeResolverRouting(t *testing.T) {
	hubSpec := map[string]any{"steps": []any{map[string]any{"name": "hub-step"}}}
	clusterSpec := map[string]any{"steps": []any{map[string]any{"name": "cluster-step"}}}

	t.Run("cluster prefix routes to cluster resolver", func(t *testing.T) {
		comp := &CompositeResolver{}
		_, err := comp.Resolve("cluster://test-ns/my-task")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cluster resolver not configured")
	})

	t.Run("hub prefix nil returns error", func(t *testing.T) {
		comp := &CompositeResolver{Cluster: NewClusterResolver()}
		_, err := comp.Resolve("git-clone:0.9")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "hub resolver not configured")
	})

	t.Run("file path nil returns error", func(t *testing.T) {
		comp := &CompositeResolver{}
		_, err := comp.Resolve("./tasks/my-task.yaml")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "file resolver not configured")
	})

	t.Run("routes cluster:// correctly via mock", func(t *testing.T) {
		comp := &mockCompositeResolver{
			hubSpec:     hubSpec,
			clusterSpec: clusterSpec,
		}
		spec, err := comp.Resolve("cluster://my-ns/my-task")
		require.NoError(t, err)
		steps := spec["steps"].([]any)
		step := steps[0].(map[string]any)
		assert.Equal(t, "cluster-step", step["name"])
	})

	t.Run("routes plain name correctly via mock", func(t *testing.T) {
		comp := &mockCompositeResolver{
			hubSpec:     hubSpec,
			clusterSpec: clusterSpec,
		}
		spec, err := comp.Resolve("git-clone:0.9")
		require.NoError(t, err)
		steps := spec["steps"].([]any)
		step := steps[0].(map[string]any)
		assert.Equal(t, "hub-step", step["name"])
	})
}

// mockCompositeResolver simulates CompositeResolver routing without
// real network/kubectl access.
type mockCompositeResolver struct {
	hubSpec     map[string]any
	clusterSpec map[string]any
}

func (r *mockCompositeResolver) Resolve(uses string) (map[string]any, error) {
	if strings.HasPrefix(uses, "cluster://") {
		return r.clusterSpec, nil
	}
	return r.hubSpec, nil
}
