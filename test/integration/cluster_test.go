// Package integration contains integration tests that run against a real
// Kubernetes cluster with Tekton Pipelines installed.
//
// These tests are skipped by default. Run with:
//
//	go test ./test/integration/ -tags=integration -v -timeout=15m
//
// Prerequisites:
//   - kubectl configured with access to a cluster with Tekton
//   - A namespace "dsl-test" must exist with permissions to create PipelineRuns
package integration

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ssadeghi/tkn-dsl/internal/compiler"
	"github.com/ssadeghi/tkn-dsl/internal/validate"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

const (
	testNamespace   = "dsl-test"
	pipelineTimeout = 3 * time.Minute
)

// compileDSL compiles a DSL string and returns the YAML output.
func compileDSL(t *testing.T, input string) string {
	t.Helper()
	p, err := dsl.Parse([]byte(input))
	require.NoError(t, err)
	errs := validate.Semantic(p)
	require.Empty(t, errs, "validation errors: %v", errs)
	result, err := compiler.Compile(p, compiler.Options{})
	require.NoError(t, err)
	var parts []string
	for _, pr := range result.PipelineRuns {
		b, err := yaml.Marshal(pr)
		require.NoError(t, err)
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "---\n")
}

// kubectlApply applies YAML to the cluster via kubectl in the test namespace.
func kubectlApply(t *testing.T, yamlContent string) {
	t.Helper()
	cmd := exec.Command("kubectl", "apply", "-n", testNamespace, "-f", "-")
	cmd.Stdin = strings.NewReader(yamlContent)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "kubectl apply failed: %s", string(out))
	t.Logf("kubectl apply: %s", strings.TrimSpace(string(out)))
}

// kubectlDelete deletes a PipelineRun in the test namespace.
func kubectlDelete(name string) {
	cmd := exec.Command("kubectl", "delete", "pipelinerun", name, "-n", testNamespace, "--ignore-not-found")
	_ = cmd.Run()
}

// runCapture runs a command and returns stdout.
func runCapture(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), nil
}

// waitForPipelineRun waits for a PipelineRun to reach a terminal condition.
func waitForPipelineRun(t *testing.T, name string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, err := runCapture("kubectl", "get", "pipelinerun", name, "-n", testNamespace,
			"-o", "jsonpath={.status.conditions[0].type}:{.status.conditions[0].status}:{.status.conditions[0].reason}")
		if err == nil && out != "" {
			parts := strings.SplitN(out, ":", 3)
			if len(parts) == 3 {
				condType := parts[0]
				condStatus := parts[1]
				reason := parts[2]
				if condType == "Succeeded" {
					if condStatus == "True" {
						return "Succeeded"
					}
					if condStatus == "False" {
						msg, _ := runCapture("kubectl", "get", "pipelinerun", name, "-n", testNamespace,
							"-o", "jsonpath={.status.conditions[0].message}")
						t.Logf("PipelineRun %s failed (reason=%s): %s", name, reason, msg)
						return "Failed:" + reason
					}
				}
			}
		}
		time.Sleep(5 * time.Second)
	}
	// Timeout — dump status for debugging.
	status, _ := runCapture("kubectl", "get", "pipelinerun", name, "-n", testNamespace, "-o", "yaml")
	t.Logf("PipelineRun %s timed out. Status:\n%s", name, status)
	return "Timeout"
}

// getPipelineRunLabel returns a label value from a PipelineRun.
func getPipelineRunLabel(t *testing.T, name, label string) string {
	t.Helper()
	out, err := runCapture("kubectl", "get", "pipelinerun", name, "-n", testNamespace,
		"-o", fmt.Sprintf("jsonpath={.metadata.labels.%s}", label))
	require.NoError(t, err)
	return out
}

// getPipelineRunAnnotation returns an annotation value from a PipelineRun.
func getPipelineRunAnnotation(t *testing.T, name, annotation string) string {
	t.Helper()
	// jsonpath doesn't handle dots in keys well, use go-template.
	out, err := runCapture("kubectl", "get", "pipelinerun", name, "-n", testNamespace,
		"-o", fmt.Sprintf(`go-template={{index .metadata.annotations "%s"}}`, annotation))
	if err != nil {
		return ""
	}
	return out
}

// --- Integration Tests ---

func TestIntegSimplePipeline(t *testing.T) {
	const name = "integ-simple"
	kubectlDelete(name)
	defer kubectlDelete(name)

	generated := compileDSL(t, `
name: `+name+`
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: |
      echo "Hello from integration test!"
      echo "Workspace: $(workspace)"
      ls -la $(workspace)
`)
	t.Logf("Generated YAML:\n%s", generated)
	kubectlApply(t, generated)
	status := waitForPipelineRun(t, name, pipelineTimeout)
	assert.Equal(t, "Succeeded", status)
}

func TestIntegMultiTaskWithDeps(t *testing.T) {
	const name = "integ-multi"
	kubectlDelete(name)
	defer kubectlDelete(name)

	generated := compileDSL(t, `
name: `+name+`
params:
  greeting:
    description: "Greeting message"
    default: "Hello"

tasks:
  generate:
    image: redhat/ubi9-minimal
    run: |
      echo -n "$(params.greeting), world!" > $(workspace)/message.txt

  display:
    needs: [generate]
    image: redhat/ubi9-minimal
    run: |
      echo "Message:"
      cat $(workspace)/message.txt
      echo ""

  skip-me:
    needs: [generate]
    if: params.greeting == 'Goodbye'
    image: redhat/ubi9-minimal
    run: echo "This should NOT run"

finally:
  cleanup:
    image: redhat/ubi9-minimal
    run: |
      echo "Cleaning up..."
      rm -f $(workspace)/message.txt
      echo "Done."
`)
	kubectlApply(t, generated)
	status := waitForPipelineRun(t, name, pipelineTimeout)
	assert.Equal(t, "Succeeded", status)
}

func TestIntegDefaultsAndHooks(t *testing.T) {
	const name = "integ-hooks"
	kubectlDelete(name)
	defer kubectlDelete(name)

	generated := compileDSL(t, `
name: `+name+`
defaults:
  image: redhat/ubi9-minimal
  before_run: echo "=== BEFORE ==="
  after_run: echo "=== AFTER ==="

tasks:
  build:
    run: |
      echo "Main task running"
      echo "build-output" > $(workspace)/build.txt

  verify:
    needs: [build]
    before_run: echo "Custom before for verify"
    run: |
      echo "Verifying..."
      cat $(workspace)/build.txt
`)
	kubectlApply(t, generated)
	status := waitForPipelineRun(t, name, pipelineTimeout)
	assert.Equal(t, "Succeeded", status)
}

func TestIntegMultiStep(t *testing.T) {
	const name = "integ-multistep"
	kubectlDelete(name)
	defer kubectlDelete(name)

	generated := compileDSL(t, `
name: `+name+`
tasks:
  build:
    image: redhat/ubi9-minimal
    steps:
      - name: step-one
        run: |
          echo "Step 1"
          echo "step1-done" > $(workspace)/step1.txt
      - name: step-two
        run: |
          echo "Step 2: reading step1 output"
          cat $(workspace)/step1.txt
`)
	kubectlApply(t, generated)
	status := waitForPipelineRun(t, name, pipelineTimeout)
	assert.Equal(t, "Succeeded", status)
}

func TestIntegTektonPassThrough(t *testing.T) {
	const name = "integ-passthrough"
	kubectlDelete(name)
	defer kubectlDelete(name)

	generated := compileDSL(t, `
name: `+name+`
tekton:
  metadata:
    labels:
      test-label: integration
    annotations:
      test.io/annotation: "true"

tasks:
  check:
    image: redhat/ubi9-minimal
    run: echo "pass-through test"
`)
	kubectlApply(t, generated)
	status := waitForPipelineRun(t, name, pipelineTimeout)
	assert.Equal(t, "Succeeded", status)

	// Verify labels/annotations were applied.
	label := getPipelineRunLabel(t, name, "test-label")
	assert.Equal(t, "integration", label)

	ann := getPipelineRunAnnotation(t, name, "test.io/annotation")
	assert.Equal(t, "true", ann)
}

func TestIntegStorageSize(t *testing.T) {
	const name = "integ-storage"
	kubectlDelete(name)
	defer kubectlDelete(name)

	generated := compileDSL(t, `
name: `+name+`
storage:
  size: 2Gi

tasks:
  write:
    image: redhat/ubi9-minimal
    run: |
      dd if=/dev/zero of=$(workspace)/bigfile bs=1M count=10
      ls -lh $(workspace)/bigfile
      echo "Wrote 10MB file to workspace"
`)
	kubectlApply(t, generated)
	status := waitForPipelineRun(t, name, pipelineTimeout)
	assert.Equal(t, "Succeeded", status)
}

func TestMain(m *testing.M) {
	// Verify cluster access before running tests.
	if _, err := runCapture("kubectl", "get", "namespace", testNamespace); err != nil {
		fmt.Fprintf(os.Stderr, "SKIP: cannot access namespace %q: %v\n", testNamespace, err)
		fmt.Fprintf(os.Stderr, "Integration tests require a cluster with Tekton and a %q namespace.\n", testNamespace)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
