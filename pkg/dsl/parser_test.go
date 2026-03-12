package dsl

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSimple(t *testing.T) {
	input := `
name: test-pipeline
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, "test-pipeline", p.Name)
	assert.Len(t, p.Tasks, 1)
	assert.Equal(t, "hello", p.Tasks["hello"].Name)
	assert.Equal(t, "redhat/ubi9-minimal", p.Tasks["hello"].Image)
	assert.Equal(t, "echo hello", p.Tasks["hello"].Run)
}

func TestParseParamShorthand(t *testing.T) {
	input := `
name: test
params:
  image-tag: "latest"
  branch:
    description: "Branch to build"
    default: "main"
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)

	// Shorthand param: bare value = default.
	imageTag := p.Params["image-tag"]
	require.NotNil(t, imageTag)
	assert.Equal(t, "latest", imageTag.Default)
	assert.Empty(t, imageTag.Description)

	// Full param.
	branch := p.Params["branch"]
	require.NotNil(t, branch)
	assert.Equal(t, "Branch to build", branch.Description)
	assert.Equal(t, "main", branch.Default)
}

func TestParseParamShorthandArray(t *testing.T) {
	input := `
name: test
params:
  targets: ["staging", "production"]
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)

	targets := p.Params["targets"]
	require.NotNil(t, targets)
	assert.NotNil(t, targets.Default)
	assert.Empty(t, targets.Type) // inferred as array from default
}

func TestParseParamTypeInference(t *testing.T) {
	input := `
name: test
params:
  simple: "hello"
  with-default:
    description: "String with default"
    default: "hello"
  array-param:
    description: "Array param"
    default: ["a", "b"]
  explicit:
    type: array
    description: "Explicit array"
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)

	assert.Equal(t, "", p.Params["simple"].Type)
	assert.Equal(t, "hello", p.Params["simple"].Default)
	assert.Equal(t, "", p.Params["with-default"].Type)
	assert.Equal(t, "", p.Params["array-param"].Type)
	assert.Equal(t, "array", p.Params["explicit"].Type)
}

func TestParseMultiStep(t *testing.T) {
	input := `
name: test
tasks:
  build:
    image: golang:1.22
    steps:
      - name: compile
        run: go build .
      - name: test
        run: go test ./...
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	assert.Len(t, p.Tasks["build"].Steps, 2)
	assert.Equal(t, "compile", p.Tasks["build"].Steps[0].Name)
	assert.Equal(t, "test", p.Tasks["build"].Steps[1].Name)
}

func TestParseApprovalShorthand(t *testing.T) {
	input := `
name: test
tasks:
  deploy:
    approval: [alice, bob]
    image: redhat/ubi9-minimal
    run: echo deploy
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	require.NotNil(t, p.Tasks["deploy"].Approval)
	assert.Equal(t, []string{"alice", "bob"}, p.Tasks["deploy"].Approval.Approvers)
	assert.Equal(t, 1, p.Tasks["deploy"].Approval.Required)
	assert.Equal(t, "60m", p.Tasks["deploy"].Approval.Timeout)
}

func TestParseApprovalFull(t *testing.T) {
	input := `
name: test
tasks:
  deploy:
    approval:
      approvers: [alice, bob, "group:platform"]
      required: 2
      description: "Approve deploy"
      timeout: 120m
    image: redhat/ubi9-minimal
    run: echo deploy
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	a := p.Tasks["deploy"].Approval
	require.NotNil(t, a)
	assert.Equal(t, []string{"alice", "bob", "group:platform"}, a.Approvers)
	assert.Equal(t, 2, a.Required)
	assert.Equal(t, "Approve deploy", a.Description)
	assert.Equal(t, "120m", a.Timeout)
}

func TestParseOnTrigger(t *testing.T) {
	input := `
name: test
on:
  pull_request:
    branches: [main, "release-*"]
    paths: ["src/**"]
    paths_ignore: ["docs/**"]
  push:
    branches: [main]
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	require.NotNil(t, p.On)
	require.NotNil(t, p.On.PullRequest)
	assert.Equal(t, []string{"main", "release-*"}, p.On.PullRequest.Branches)
	assert.Equal(t, []string{"src/**"}, p.On.PullRequest.Paths)
	assert.Equal(t, []string{"docs/**"}, p.On.PullRequest.PathsIgnore)
	require.NotNil(t, p.On.Push)
	assert.Equal(t, []string{"main"}, p.On.Push.Branches)
}

func TestParseFinally(t *testing.T) {
	input := `
name: test
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
finally:
  cleanup:
    image: redhat/ubi9-minimal
    run: echo cleanup
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	assert.Len(t, p.Finally, 1)
	assert.Equal(t, "cleanup", p.Finally["cleanup"].Name)
}

func TestParseCacheShorthand(t *testing.T) {
	input := `
name: test
cache:
  backend: oci://registry.example.com/cache
  path: /go/pkg/mod
  key: ["**/go.sum"]
tasks:
  build:
    image: golang:1.22
    run: go build .
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	require.NotNil(t, p.Cache)
	paths := p.Cache.EffectiveCachePaths()
	assert.Len(t, paths, 1)
	assert.Equal(t, "/go/pkg/mod", paths[0].Path)
}

func TestParseCacheFull(t *testing.T) {
	input := `
name: test
cache:
  backend: oci://registry.example.com/cache
  paths:
    - path: /go/pkg/mod
      key: ["**/go.sum"]
    - path: /root/.cache/go-build
      key: ["**/go.sum", "**/go.mod"]
tasks:
  build:
    image: golang:1.22
    run: go build .
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	paths := p.Cache.EffectiveCachePaths()
	assert.Len(t, paths, 2)
}

func TestParseDefaults(t *testing.T) {
	input := `
name: test
defaults:
  image: golang:1.22
  before_run: echo starting
  after_run: echo done
tasks:
  build:
    run: go build .
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	require.NotNil(t, p.Defaults)
	assert.Equal(t, "golang:1.22", p.Defaults.Image)
	assert.Equal(t, "echo starting", p.Defaults.BeforeRun.Script())
	assert.Equal(t, "echo done", p.Defaults.AfterRun.Script())
}

func TestParseSecrets(t *testing.T) {
	input := `
name: test
secrets:
  git-credentials: git-creds
  docker-config: regcred
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)
	assert.Equal(t, "git-creds", p.Secrets["git-credentials"])
	assert.Equal(t, "regcred", p.Secrets["docker-config"])
}

func TestParsePaCBraceSyntax(t *testing.T) {
	input := `
name: test
tasks:
  clone:
    uses: git-clone
    params:
      url: {{ repo_url }}
      revision: {{ revision }}
  build:
    image: redhat/ubi9-minimal
    run: echo {{source_branch}}
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)

	// {{ var }} should be normalized to $(var).
	clone := p.Tasks["clone"]
	require.NotNil(t, clone)
	assert.Equal(t, "$(repo_url)", clone.Params["url"])
	assert.Equal(t, "$(revision)", clone.Params["revision"])

	build := p.Tasks["build"]
	require.NotNil(t, build)
	assert.Contains(t, build.Run, "$(source_branch)")
	assert.NotContains(t, build.Run, "{{")
}

func TestParsePaCBraceSyntaxInParams(t *testing.T) {
	input := `
name: test
params:
  image_url: "quay.io/myorg/myapp:{{source_branch}}"
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo $(params.image_url)
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)

	// {{ }} inside quoted strings should also be normalized.
	assert.Equal(t, "quay.io/myorg/myapp:$(source_branch)", p.Params["image_url"].Default)
}

func TestParsePaCBraceSyntaxWithDots(t *testing.T) {
	input := `
name: test
tasks:
  info:
    image: redhat/ubi9-minimal
    run: echo "{{ body.pull_request.title }}"
`
	p, err := Parse([]byte(input))
	require.NoError(t, err)

	// Dotted PaC body references should also be normalized.
	assert.Contains(t, p.Tasks["info"].Run, "$(body.pull_request.title)")
}
