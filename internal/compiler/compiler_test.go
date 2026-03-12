package compiler

import (
	"fmt"
	"testing"

	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func compile(t *testing.T, input string, opts ...Options) *CompileResult {
	t.Helper()
	p, err := dsl.Parse([]byte(input))
	require.NoError(t, err)
	o := Options{}
	if len(opts) > 0 {
		o = opts[0]
	}
	result, err := Compile(p, o)
	require.NoError(t, err)
	return result
}

func TestCompileSimple(t *testing.T) {
	r := compile(t, `
name: test-simple
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello from $(workspace)
`)
	require.Len(t, r.PipelineRuns, 1)
	pr := r.PipelineRuns[0]
	assert.Equal(t, "tekton.dev/v1", pr.APIVersion)
	assert.Equal(t, "PipelineRun", pr.Kind)
	assert.Equal(t, "test-simple", pr.Metadata.Name)
	assert.Equal(t, "tkn-dsl", pr.Metadata.Labels["app.kubernetes.io/managed-by"])

	require.Len(t, pr.Spec.PipelineSpec.Workspaces, 1)
	assert.Equal(t, "shared-workspace", pr.Spec.PipelineSpec.Workspaces[0].Name)

	require.Len(t, pr.Spec.PipelineSpec.Tasks, 1)
	task := pr.Spec.PipelineSpec.Tasks[0]
	assert.Equal(t, "hello", task.Name)
	require.NotNil(t, task.TaskSpec)
	require.Len(t, task.TaskSpec.Steps, 1)
	assert.Equal(t, "redhat/ubi9-minimal", task.TaskSpec.Steps[0].Image)
	assert.Contains(t, task.TaskSpec.Steps[0].Script, "$(workspaces.shared-workspace.path)")
	assert.NotContains(t, task.TaskSpec.Steps[0].Script, "$(workspace)")

	require.Len(t, pr.Spec.Workspaces, 1)
	assert.NotNil(t, pr.Spec.Workspaces[0].VolumeClaimTemplate)
	assert.Equal(t, "1Gi", pr.Spec.Workspaces[0].VolumeClaimTemplate.Spec.Resources.Requests["storage"])
}

func TestCompileWithParams(t *testing.T) {
	r := compile(t, `
name: test-params
params:
  name: "Who to greet"
  greeting:
    description: "The greeting"
    default: "Hello"
  targets:
    description: "Deploy targets"
    default: ["staging", "production"]
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo $(params.greeting) $(params.name)
`)
	params := r.PipelineRuns[0].Spec.PipelineSpec.Params
	require.Len(t, params, 3)
	assert.Equal(t, "greeting", params[0].Name)
	assert.Equal(t, "string", params[0].Type)
	assert.Equal(t, "Hello", params[0].Default)
	assert.Equal(t, "name", params[1].Name)
	assert.Equal(t, "string", params[1].Type)
	assert.Equal(t, "targets", params[2].Name)
	assert.Equal(t, "array", params[2].Type)
}

func TestCompileWithNeeds(t *testing.T) {
	r := compile(t, `
name: test-needs
tasks:
  clone:
    image: redhat/ubi9-minimal
    run: echo clone
  build:
    needs: [clone]
    image: redhat/ubi9-minimal
    run: echo build
  deploy:
    needs: [build]
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	require.Len(t, tasks, 3)
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}
	assert.Empty(t, tasks[taskMap["clone"]].RunAfter)
	assert.Equal(t, []string{"clone"}, tasks[taskMap["build"]].RunAfter)
	assert.Equal(t, []string{"build"}, tasks[taskMap["deploy"]].RunAfter)
}

func TestCompileWithConditional(t *testing.T) {
	r := compile(t, `
name: test-when
tasks:
  deploy:
    if: params.env == 'production'
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	task := r.PipelineRuns[0].Spec.PipelineSpec.Tasks[0]
	require.Len(t, task.When, 1)
	assert.Equal(t, "$(params.env)", task.When[0].Input)
	assert.Equal(t, "in", task.When[0].Operator)
	assert.Equal(t, []string{"production"}, task.When[0].Values)
}

func TestCompileWithFinally(t *testing.T) {
	r := compile(t, `
name: test-finally
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
finally:
  cleanup:
    image: redhat/ubi9-minimal
    run: echo cleanup
`)
	assert.Len(t, r.PipelineRuns[0].Spec.PipelineSpec.Finally, 1)
	assert.Equal(t, "cleanup", r.PipelineRuns[0].Spec.PipelineSpec.Finally[0].Name)
}

func TestCompileMultiStep(t *testing.T) {
	r := compile(t, `
name: test-multistep
tasks:
  build:
    image: golang:1.22
    steps:
      - name: compile
        run: go build .
      - name: test
        image: golang:1.22-alpine
        run: go test ./...
`)
	steps := r.PipelineRuns[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps
	require.Len(t, steps, 2)
	assert.Equal(t, "compile", steps[0].Name)
	assert.Equal(t, "golang:1.22", steps[0].Image)
	assert.Equal(t, "test", steps[1].Name)
	assert.Equal(t, "golang:1.22-alpine", steps[1].Image)
}

func TestCompileDefaults(t *testing.T) {
	r := compile(t, `
name: test-defaults
defaults:
  image: golang:1.22
  before_run: echo starting
  after_run: echo done
tasks:
  build:
    run: go build .
`)
	steps := r.PipelineRuns[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps
	require.Len(t, steps, 3)
	assert.Equal(t, "before-run", steps[0].Name)
	assert.Equal(t, "golang:1.22", steps[0].Image)
	assert.Equal(t, "build", steps[1].Name)
	assert.Equal(t, "golang:1.22", steps[1].Image)
	assert.Equal(t, "after-run", steps[2].Name)
	assert.Equal(t, "continue", steps[2].OnError)
}

func TestCompileStorage(t *testing.T) {
	r := compile(t, `
name: test-storage
storage:
  size: 5Gi
  storageClass: fast-ssd
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
`)
	ws := r.PipelineRuns[0].Spec.Workspaces[0]
	assert.Equal(t, "5Gi", ws.VolumeClaimTemplate.Spec.Resources.Requests["storage"])
	assert.Equal(t, "fast-ssd", *ws.VolumeClaimTemplate.Spec.StorageClassName)
}

func TestCompilePaC(t *testing.T) {
	r := compile(t, `
name: test-pac
on:
  pull_request:
    branches: [main, "release-*"]
    paths: ["src/**"]
  push:
    branches: [main]
concurrency:
  cancel-in-progress: true
cleanup:
  max-keep-runs: 5
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
`)
	require.Len(t, r.PipelineRuns, 1)
	pr := r.PipelineRuns[0]
	assert.Equal(t, "test-pac", pr.Metadata.Name)
	assert.Equal(t, "[pull_request, push]", pr.Metadata.Annotations["pipelinesascode.tekton.dev/on-event"])
	assert.Equal(t, "[main]", pr.Metadata.Annotations["pipelinesascode.tekton.dev/on-target-branch"])
	assert.Equal(t, "true", pr.Metadata.Annotations["pipelinesascode.tekton.dev/cancel-in-progress"])
	assert.Equal(t, "5", pr.Metadata.Annotations["pipelinesascode.tekton.dev/max-keep-runs"])
}

func TestCompilePaCTemplateVars(t *testing.T) {
	r := compile(t, `
name: test-pac-vars
on:
  pull_request:
    branches: [main]
tasks:
  clone:
    image: alpine/git:latest
    run: git clone $(repo_url) $(workspace)/repo
`)
	script := r.PipelineRuns[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Script
	assert.Contains(t, script, "{{ repo_url }}")
	assert.Contains(t, script, "$(workspaces.shared-workspace.path)")
	assert.NotContains(t, script, "$(repo_url)")
	assert.NotContains(t, script, "$(workspace)")
}

func TestCompilePaCParamPassThrough(t *testing.T) {
	// $(params.X) Tekton syntax passes through unchanged.
	// Only PaC built-ins like $(repo_url) are translated to {{ var }}.
	r := compile(t, `
name: test-param-passthrough
on:
  push:
    branches: [main]
params:
  image_url: "quay.io/myorg/myapp:$(source_branch)"
tasks:
  build:
    image: buildah:latest
    run: echo $(params.image_url) $(repo_url)
`)
	pr := r.PipelineRuns[0]

	// Param default: $(source_branch) is a PaC built-in → {{ source_branch }}.
	paramDefault := pr.Spec.PipelineSpec.Params[0].Default.(string)
	assert.Contains(t, paramDefault, "{{ source_branch }}")

	// In script: $(params.image_url) passes through as-is (Tekton resolves it).
	// $(repo_url) is a PaC built-in → {{ repo_url }}.
	script := pr.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps[0].Script
	assert.Contains(t, script, "$(params.image_url)")
	assert.Contains(t, script, "{{ repo_url }}")
}

func TestCompileSecrets(t *testing.T) {
	r := compile(t, `
name: test-secrets
secrets:
  git-credentials: git-creds
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
`)
	wsDecls := r.PipelineRuns[0].Spec.PipelineSpec.Workspaces
	assert.Len(t, wsDecls, 2)
	wsBindings := r.PipelineRuns[0].Spec.Workspaces
	assert.Len(t, wsBindings, 2)
	var secretBinding *int
	for i := range wsBindings {
		if wsBindings[i].Secret != nil {
			secretBinding = &i
			break
		}
	}
	require.NotNil(t, secretBinding)
	assert.Equal(t, "git-creds", wsBindings[*secretBinding].Secret.SecretName)
}

func TestCompileTektonPassThrough(t *testing.T) {
	r := compile(t, `
name: test-passthrough
tekton:
  metadata:
    labels:
      team: backend
    annotations:
      custom.io/priority: high
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
`)
	assert.Equal(t, "backend", r.PipelineRuns[0].Metadata.Labels["team"])
	assert.Equal(t, "high", r.PipelineRuns[0].Metadata.Annotations["custom.io/priority"])
	assert.Equal(t, "tkn-dsl", r.PipelineRuns[0].Metadata.Labels["app.kubernetes.io/managed-by"])
}

func TestCompileApprovalGate(t *testing.T) {
	r := compile(t, `
name: test-approval
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
  deploy:
    needs: [build]
    approval:
      approvers: [alice, bob]
      required: 2
      description: "Approve deploy"
      timeout: 120m
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}

	// Should have approve-deploy inserted.
	_, hasApproval := taskMap["approve-deploy"]
	require.True(t, hasApproval, "should have approve-deploy task")

	approvalTask := tasks[taskMap["approve-deploy"]]
	assert.NotNil(t, approvalTask.TaskRef)
	assert.Equal(t, "ApprovalTask", approvalTask.TaskRef.Kind)
	assert.Equal(t, []string{"build"}, approvalTask.RunAfter)

	deployTask := tasks[taskMap["deploy"]]
	assert.Equal(t, []string{"approve-deploy"}, deployTask.RunAfter)
}

func TestCompileApprovalShorthand(t *testing.T) {
	r := compile(t, `
name: test-approval-short
tasks:
  deploy:
    approval: [alice, bob]
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}
	_, hasApproval := taskMap["approve-deploy"]
	require.True(t, hasApproval)
}

func TestCompileCacheInjection(t *testing.T) {
	r := compile(t, `
name: test-cache
cache:
  image: oci://registry.example.com/my-cache
tasks:
  build:
    image: golang:1.22
    run: go build .
    cache:
      path: /go/pkg/mod
      key: ["**/go.sum"]
`)

	pr := r.PipelineRuns[0]

	steps := pr.Spec.PipelineSpec.Tasks[0].TaskSpec.Steps
	// Should have: cache-fetch, user step, cache-upload.
	require.Len(t, steps, 3)
	assert.Equal(t, "cache-fetch-mod", steps[0].Name)
	assert.Nil(t, steps[0].Ref, "cache steps should be inlined, not use ref")
	assert.NotEmpty(t, steps[0].Image)
	assert.Contains(t, steps[0].Script, "/ko-app/cache fetch")
	assert.Equal(t, "build", steps[1].Name)
	assert.Equal(t, "cache-upload-mod", steps[2].Name)
	assert.Contains(t, steps[2].Script, "/ko-app/cache upload")
}

func TestCompileCacheNoCache(t *testing.T) {
	r := compile(t, `
name: test-cache-nocache
cache:
  image: oci://registry.example.com/my-cache
tasks:
  build:
    image: golang:1.22
    run: go build .
    cache:
      path: /go/pkg/mod
      key: ["**/go.sum"]
`, Options{NoCache: true})

	steps := r.PipelineRuns[0].Spec.PipelineSpec.Tasks[0].TaskSpec.Steps
	require.Len(t, steps, 1) // no cache steps
}

func TestCompileCacheOnlyOnTargetedTask(t *testing.T) {
	r := compile(t, `
name: test-cache-targeted
cache:
  image: oci://registry.example.com/my-cache
tasks:
  build:
    image: golang:1.22
    run: go build .
    cache:
      path: /go/pkg/mod
      key: ["**/go.sum"]
  deploy:
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	// build: cache-fetch + build + cache-upload = 3 steps.
	require.Len(t, tasks[0].TaskSpec.Steps, 3)
	// deploy: just the user step (no cache).
	require.Len(t, tasks[1].TaskSpec.Steps, 1)
}

func TestCompileTaskTektonPassThrough(t *testing.T) {
	r := compile(t, `
name: test-task-passthrough
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
    tekton:
      podTemplate:
        nodeSelector:
          gpu-type: a100
`)
	taskSpec := r.PipelineRuns[0].Spec.PipelineSpec.Tasks[0].TaskSpec
	require.NotNil(t, taskSpec.TektonRaw)
	pt, ok := taskSpec.TektonRaw["podTemplate"]
	require.True(t, ok)
	ptMap := pt.(map[string]any)
	ns := ptMap["nodeSelector"].(map[string]any)
	assert.Equal(t, "a100", ns["gpu-type"])
}

// mockResolver returns a fixed task spec for testing.
type mockResolver struct {
	specs map[string]map[string]any
}

func (m *mockResolver) Resolve(uses string) (map[string]any, error) {
	spec, ok := m.specs[uses]
	if !ok {
		return nil, fmt.Errorf("unknown task: %s", uses)
	}
	return spec, nil
}

func TestCompileUsesInlined(t *testing.T) {
	// Mock resolver that returns a git-clone task spec.
	mock := &mockResolver{
		specs: map[string]map[string]any{
			"git-clone": {
				"params": []any{
					map[string]any{"name": "url", "type": "string"},
					map[string]any{"name": "revision", "type": "string", "default": ""},
				},
				"steps": []any{
					map[string]any{
						"name":   "clone",
						"image":  "gcr.io/tekton-releases/git-init:v0.40.2",
						"script": "git clone $(params.url) $(workspaces.output.path)",
					},
				},
				"workspaces": []any{
					map[string]any{"name": "output", "description": "The git repo"},
					map[string]any{"name": "ssh-directory", "optional": true},
				},
			},
		},
	}

	r := compile(t, `
name: test-inline
tasks:
  clone:
    uses: git-clone
    params:
      url: https://github.com/example/repo
      revision: main
  build:
    needs: [clone]
    image: golang:1.22
    run: go build .
`, Options{TaskResolver: mock})

	require.Len(t, r.PipelineRuns, 1)
	pr := r.PipelineRuns[0]
	tasks := pr.Spec.PipelineSpec.Tasks

	// Clone task should be inlined (taskSpec, not taskRef).
	cloneTask := tasks[0]
	assert.Equal(t, "clone", cloneTask.Name)
	assert.Nil(t, cloneTask.TaskRef, "should not have taskRef when resolved")
	require.NotNil(t, cloneTask.TaskSpec, "should have inline taskSpec")
	assert.NotNil(t, cloneTask.TaskSpec.Raw, "should use Raw spec for resolved tasks")

	// Params should be forwarded.
	assert.Len(t, cloneTask.Params, 2)

	// Workspace refs should only map required (non-optional) workspaces.
	// "output" is required, "ssh-directory" is optional → only "output" mapped.
	assert.Len(t, cloneTask.Workspaces, 1)
	assert.Equal(t, "output", cloneTask.Workspaces[0].Name)
	assert.Equal(t, "shared-workspace", cloneTask.Workspaces[0].Workspace)

	// Build task should still be a regular inline task.
	buildTask := tasks[1]
	assert.Equal(t, "build", buildTask.Name)
	assert.Nil(t, buildTask.TaskRef)
	require.NotNil(t, buildTask.TaskSpec)
	assert.Nil(t, buildTask.TaskSpec.Raw, "non-uses task should not use Raw")
	require.Len(t, buildTask.TaskSpec.Steps, 1)
}

func TestCompileUsesNoResolver(t *testing.T) {
	// Without a resolver, uses: should still generate taskRef (backward compat).
	r := compile(t, `
name: test-taskref
tasks:
  clone:
    uses: git-clone
    params:
      url: https://github.com/example/repo
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	assert.NotNil(t, tasks[0].TaskRef, "should have taskRef without resolver")
	assert.Equal(t, "git-clone", tasks[0].TaskRef.Name)
	assert.Nil(t, tasks[0].TaskSpec, "should not have taskSpec without resolver")
}

func TestCompilePaCUsesAnnotationsSkippedWithResolver(t *testing.T) {
	mock := &mockResolver{
		specs: map[string]map[string]any{
			"git-clone": {
				"steps": []any{
					map[string]any{"name": "clone", "image": "git-init:latest", "script": "echo clone"},
				},
			},
		},
	}

	r := compile(t, `
name: test-pac-no-annotations
on:
  pull_request:
    branches: [main]
tasks:
  clone:
    uses: git-clone
    params:
      url: $(repo_url)
`, Options{TaskResolver: mock})

	pr := r.PipelineRuns[0]
	// Should NOT have pipelinesascode.tekton.dev/task annotation.
	for key := range pr.Metadata.Annotations {
		assert.NotContains(t, key, "pipelinesascode.tekton.dev/task",
			"should not have task annotations when resolver is used")
	}
	// But should still have event annotations.
	assert.Equal(t, "[pull_request]", pr.Metadata.Annotations["pipelinesascode.tekton.dev/on-event"])
}

func TestCompileClusterTaskInlined(t *testing.T) {
	mock := &mockResolver{
		specs: map[string]map[string]any{
			"cluster://openshift-pipelines/buildah": {
				"params": []any{
					map[string]any{"name": "IMAGE", "type": "string"},
				},
				"steps": []any{
					map[string]any{
						"name":   "build",
						"image":  "quay.io/buildah/stable",
						"script": "buildah bud -t $(params.IMAGE) .",
					},
				},
				"workspaces": []any{
					map[string]any{"name": "source"},
					map[string]any{"name": "dockerconfig", "optional": true},
				},
			},
		},
	}

	r := compile(t, `
name: test-cluster-task
tasks:
  build:
    uses: cluster://openshift-pipelines/buildah
    params:
      IMAGE: quay.io/myorg/myapp:latest
`, Options{TaskResolver: mock})

	task := r.PipelineRuns[0].Spec.PipelineSpec.Tasks[0]
	assert.Equal(t, "build", task.Name)
	assert.Nil(t, task.TaskRef)
	require.NotNil(t, task.TaskSpec)
	assert.NotNil(t, task.TaskSpec.Raw)

	// Params forwarded.
	require.Len(t, task.Params, 1)
	assert.Equal(t, "IMAGE", task.Params[0].Name)

	// Only required workspace mapped, optional "dockerconfig" skipped.
	require.Len(t, task.Workspaces, 1)
	assert.Equal(t, "source", task.Workspaces[0].Name)
	assert.Equal(t, "shared-workspace", task.Workspaces[0].Workspace)
}

// --- Dependency resolution tests ---

func TestImplicitDeclarationOrder(t *testing.T) {
	// No needs: anywhere → sequential in declaration order.
	r := compile(t, `
name: test-implicit-order
tasks:
  clone:
    image: redhat/ubi9-minimal
    run: echo clone
  build:
    image: redhat/ubi9-minimal
    run: echo build
  test:
    image: redhat/ubi9-minimal
    run: echo test
  deploy:
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}

	assert.Empty(t, tasks[taskMap["clone"]].RunAfter)
	assert.Equal(t, []string{"clone"}, tasks[taskMap["build"]].RunAfter)
	assert.Equal(t, []string{"build"}, tasks[taskMap["test"]].RunAfter)
	assert.Equal(t, []string{"test"}, tasks[taskMap["deploy"]].RunAfter)
}

func TestExplicitNeedsEnablesParallelism(t *testing.T) {
	// lint and test both need clone → parallel. deploy follows test (declaration order).
	r := compile(t, `
name: test-parallel
tasks:
  clone:
    image: redhat/ubi9-minimal
    run: echo clone
  lint:
    needs: [clone]
    image: redhat/ubi9-minimal
    run: echo lint
  test:
    needs: [clone]
    image: redhat/ubi9-minimal
    run: echo test
  deploy:
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}

	assert.Empty(t, tasks[taskMap["clone"]].RunAfter)
	assert.Equal(t, []string{"clone"}, tasks[taskMap["lint"]].RunAfter)
	assert.Equal(t, []string{"clone"}, tasks[taskMap["test"]].RunAfter)
	// deploy has no needs → depends on previous task (test) in declaration order.
	assert.Equal(t, []string{"test"}, tasks[taskMap["deploy"]].RunAfter)
}

func TestResultRefAutoDetection(t *testing.T) {
	// deploy references tasks.check.results.status → auto-dependency on check.
	r := compile(t, `
name: test-result-ref
tasks:
  check:
    image: redhat/ubi9-minimal
    run: echo ready
    results:
      status:
        description: "readiness status"
  deploy:
    needs: [check]
    if: tasks.check.results.status == 'ready'
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}
	// check has result ref from deploy's if: expression — and deploy has explicit needs: [check].
	assert.Equal(t, []string{"check"}, tasks[taskMap["deploy"]].RunAfter)
}

func TestResultRefWithoutExplicitNeeds(t *testing.T) {
	// No explicit needs, but result ref should add dependency in addition to declaration order.
	r := compile(t, `
name: test-result-ref-implicit
tasks:
  check:
    image: redhat/ubi9-minimal
    run: echo ready
    results:
      status:
        description: "readiness status"
  middle:
    image: redhat/ubi9-minimal
    run: echo middle
  deploy:
    image: redhat/ubi9-minimal
    run: echo $(tasks.check.results.status)
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}

	// deploy: declaration order gives dep on "middle". Result ref on "check" is
	// not added because "check" is already transitively reachable via middle → check.
	deployDeps := tasks[taskMap["deploy"]].RunAfter
	assert.Contains(t, deployDeps, "middle")    // declaration order
	assert.NotContains(t, deployDeps, "check")  // transitively reachable, not redundantly added
}

func TestMixedImplicitAndExplicit(t *testing.T) {
	// Real-world pattern: clone → lint+test (parallel) → build → deploy.
	r := compile(t, `
name: test-mixed
tasks:
  clone:
    image: redhat/ubi9-minimal
    run: echo clone
  lint:
    needs: [clone]
    image: redhat/ubi9-minimal
    run: echo lint
  test:
    needs: [clone]
    image: redhat/ubi9-minimal
    run: echo test
  build:
    needs: [lint, test]
    image: redhat/ubi9-minimal
    run: echo build
  deploy:
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	tasks := r.PipelineRuns[0].Spec.PipelineSpec.Tasks
	taskMap := make(map[string]int)
	for i, t := range tasks {
		taskMap[t.Name] = i
	}

	assert.Empty(t, tasks[taskMap["clone"]].RunAfter)
	assert.Equal(t, []string{"clone"}, tasks[taskMap["lint"]].RunAfter)
	assert.Equal(t, []string{"clone"}, tasks[taskMap["test"]].RunAfter)
	assert.ElementsMatch(t, []string{"lint", "test"}, tasks[taskMap["build"]].RunAfter)
	// deploy has no needs → depends on build (previous in declaration order).
	assert.Equal(t, []string{"build"}, tasks[taskMap["deploy"]].RunAfter)
}

func TestBuildCacheURI(t *testing.T) {
	// OCI: cache entries are tags on the image.
	uri := buildCacheURI("oci://quay.io/siamaksade/my-cache", "m2")
	assert.Equal(t, "oci://quay.io/siamaksade/my-cache:m2-{{hash}}", uri)

	uri = buildCacheURI("oci://quay.io/siamaksade/my-cache", "go-build")
	assert.Equal(t, "oci://quay.io/siamaksade/my-cache:go-build-{{hash}}", uri)

	// S3: nested paths.
	uri = buildCacheURI("s3://my-bucket/tekton-cache", "go-pkg-mod")
	assert.Equal(t, "s3://my-bucket/tekton-cache/go-pkg-mod/{{hash}}", uri)
}

func TestSanitizePathShort(t *testing.T) {
	// sanitizePath takes the last segment for short tag names.
	assert.Equal(t, "m2", sanitizePath("/workspace/maven-local-repo/.m2"))
	assert.Equal(t, "go-build", sanitizePath("/root/.cache/go-build"))
	assert.Equal(t, "mod", sanitizePath("/go/pkg/mod"))
	assert.Equal(t, "node-modules", sanitizePath("node_modules"))
}
