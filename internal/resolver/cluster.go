package resolver

import (
	"fmt"
	"os/exec"
	"strings"
)

// ClusterResolver resolves tasks from a Kubernetes cluster by fetching
// Tekton Task CRs via kubectl.
//
// Uses format: "cluster://namespace/task-name"
type ClusterResolver struct {
	// KubectlPath is the path to the kubectl binary. Defaults to "kubectl".
	KubectlPath string
	// Kubeconfig is an optional path to a kubeconfig file.
	Kubeconfig string
	// Context is an optional kubectl context to use.
	Context string
}

// NewClusterResolver creates a resolver that fetches tasks from the cluster.
func NewClusterResolver() *ClusterResolver {
	return &ClusterResolver{
		KubectlPath: "kubectl",
	}
}

// Resolve fetches a Tekton Task from the cluster and returns its spec.
// The uses string must be in the format "namespace/task-name" (without the
// "cluster://" prefix, which is stripped by the CompositeResolver).
func (r *ClusterResolver) Resolve(uses string) (map[string]any, error) {
	namespace, taskName, err := parseClusterRef(uses)
	if err != nil {
		return nil, err
	}

	data, err := r.kubectlGetTask(namespace, taskName)
	if err != nil {
		return nil, err
	}

	return ParseTaskSpec(data)
}

func parseClusterRef(ref string) (namespace, name string, err error) {
	// Strip "cluster://" prefix if still present.
	ref = strings.TrimPrefix(ref, "cluster://")

	parts := strings.SplitN(ref, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid cluster task reference %q: expected \"namespace/task-name\"", ref)
	}
	return parts[0], parts[1], nil
}

func (r *ClusterResolver) kubectlGetTask(namespace, name string) ([]byte, error) {
	args := []string{"get", "task", name, "-n", namespace, "-o", "yaml"}
	if r.Kubeconfig != "" {
		args = append(args, "--kubeconfig", r.Kubeconfig)
	}
	if r.Context != "" {
		args = append(args, "--context", r.Context)
	}

	cmd := exec.Command(r.KubectlPath, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("kubectl get task %s -n %s failed: %s", name, namespace, string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("running kubectl: %w", err)
	}
	return out, nil
}
