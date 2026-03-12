package validate

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSchemaValidMinimal(t *testing.T) {
	input := `
name: test
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	errs := Schema([]byte(input))
	assert.Empty(t, errs)
}

func TestSchemaRejectsMissingName(t *testing.T) {
	input := `
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	errs := Schema([]byte(input))
	assert.NotEmpty(t, errs)
}

func TestSchemaRejectsMissingTasks(t *testing.T) {
	input := `
name: test
`
	errs := Schema([]byte(input))
	assert.NotEmpty(t, errs)
}

func TestSchemaRejectsUnknownTopLevelField(t *testing.T) {
	input := `
name: test
unknown_field: true
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`
	errs := Schema([]byte(input))
	assert.NotEmpty(t, errs)
}

func TestSchemaAcceptsFullPipeline(t *testing.T) {
	input := `
name: full-test
on:
  pull_request:
    branches: [main]
    paths: ["src/**"]
  push:
    branches: [main]
  schedule:
    - cron: "0 2 * * *"
      branch: main
params:
  greeting: "Hello message"
  target:
    description: "Who to greet"
    default: world
secrets:
  git-creds: my-secret
defaults:
  image: golang:1.22
  before_run: echo start
  after_run: echo done
storage:
  size: 5Gi
  storageClass: fast-ssd
cache:
  image: oci://registry.example.com/my-cache
concurrency:
  cancel-in-progress: true
cleanup:
  max-keep-runs: 5
tasks:
  build:
    image: redhat/ubi9-minimal
    run: echo build
    timeout: 30m
    retries: 2
    env:
      FOO: bar
    results:
      output:
        description: "Build output"
  test:
    needs: [build]
    if: params.target == 'world'
    image: golang:1.22
    steps:
      - name: unit
        run: go test ./...
      - name: lint
        image: golangci/golangci-lint:latest
        run: golangci-lint run
    sidecars:
      - name: db
        image: postgres:16
        ports: [5432]
        env:
          POSTGRES_DB: test
  deploy:
    uses: my-deploy-task
    params:
      env: staging
  gate:
    needs: [build]
    approval:
      approvers: [alice, bob]
      required: 2
      timeout: 60m
    image: redhat/ubi9-minimal
    run: echo approved
finally:
  cleanup:
    image: redhat/ubi9-minimal
    run: echo cleanup
tekton:
  metadata:
    labels:
      team: backend
`
	errs := Schema([]byte(input))
	assert.Empty(t, errs, "full pipeline should pass schema validation: %v", errs)
}
