# Getting Started with tkn-dsl

`tkn-dsl` compiles a simplified YAML DSL into Tekton PipelineRun resources compatible with [Pipelines-as-Code](https://pipelinesascode.com). Write ~20 lines of DSL instead of ~80 lines of Tekton YAML.

## Installation

```bash
go install github.com/ssadeghi/tkn-dsl/cmd/tkn-dsl@latest
```

Or build from source:

```bash
git clone https://github.com/ssadeghi/tkn-dsl.git
cd tkn-dsl
make build
# binary is at ./bin/tkn-dsl
```

## Your First Pipeline

### 1. Scaffold a pipeline

```bash
tkn-dsl init pipeline.dsl.yaml
```

Or create one manually:

```yaml
# pipeline.dsl.yaml
name: hello-world

tasks:
  greet:
    image: redhat/ubi9-minimal
    run: echo "Hello from Tekton DSL!"
```

### 2. Generate the Tekton PipelineRun

```bash
tkn-dsl generate pipeline.dsl.yaml
```

This outputs the full Tekton PipelineRun YAML to stdout. To write it to a file:

```bash
tkn-dsl generate pipeline.dsl.yaml > .tekton/pipeline.yaml
```

### 3. Validate without generating

```bash
tkn-dsl validate pipeline.dsl.yaml
```

## DSL Syntax Guide

### Tasks

Tasks are the building blocks of a pipeline. Each task runs in its own pod.

**Single-step task** — use `run:` for a quick script:

```yaml
tasks:
  build:
    image: golang:1.22
    run: go build -o app .
```

**Multi-step task** — use `steps:` when you need multiple steps:

```yaml
tasks:
  build:
    image: golang:1.22
    steps:
      - name: compile
        run: go build -o app .
      - name: test
        run: go test ./...
```

**External task** — use `uses:` to reference a task from Tekton Hub or the cluster:

```yaml
tasks:
  clone:
    uses: git-clone:0.10.0        # from Tekton Hub (version optional)
    params:
      url: $(repo_url)
      revision: $(revision)

  build:
    uses: cluster://openshift-pipelines/buildah   # from the cluster
    params:
      IMAGE: quay.io/myorg/myapp:latest
```

### Task Dependencies

Tasks run **sequentially by declaration order** by default — each task depends on the one before it. Use `needs:` to override:

```yaml
tasks:
  clone:
    uses: git-clone
    params: { url: $(repo_url) }

  # runs after clone (declaration order)
  lint:
    image: golangci/golangci-lint
    run: golangci-lint run ./...

  # runs after lint (declaration order)
  test:
    image: golang:1.22
    run: go test ./...

  # runs after BOTH lint and test (explicit)
  build:
    needs: [lint, test]
    image: golang:1.22
    run: go build -o app .
```

Result references (`$(tasks.X.results.Y)`) also create implicit dependencies automatically.

### Shared Workspace

All tasks share a filesystem. Use `$(workspace)` to reference it:

```yaml
tasks:
  clone:
    image: alpine/git
    run: git clone https://github.com/example/repo $(workspace)/repo

  build:
    image: golang:1.22
    run: |
      cd $(workspace)/repo
      go build -o app .
```

No workspace declarations needed — a `VolumeClaimTemplate` is auto-provisioned.

### Parameters

Parameters use a compact shorthand where bare values are defaults:

```yaml
params:
  branch: "main"                    # string, default "main"
  image-tag: "latest"               # string, default "latest"
  targets: ["staging", "prod"]      # array, default ["staging", "prod"]

  # full form when you need a description
  config:
    description: "Build configuration"
    default: "release"

tasks:
  build:
    image: golang:1.22
    run: echo "Building branch $(params.branch)"
```

### Conditionals

Use `if:` to skip tasks based on parameter values or task results:

```yaml
tasks:
  deploy:
    needs: [build]
    if: params.branch == 'main'
    image: bitnami/kubectl
    run: kubectl apply -f deploy.yaml

  skip-dev:
    needs: [build]
    if: params.env not in ("dev", "test")
    image: alpine
    run: echo "Not a dev environment"
```

### Git Triggers (Pipelines-as-Code)

The `on:` block configures PaC annotations for automatic triggering:

```yaml
on:
  pull_request:
    branches: [main, "release-*"]
    paths: ["src/**", "pkg/**"]
    paths_ignore: ["docs/**", "*.md"]
  push:
    branches: [main]
```

Use PaC template variables for dynamic values:

```yaml
tasks:
  clone:
    uses: git-clone
    params:
      url: {{ repo_url }}
      revision: {{ revision }}
```

Common variables: `{{ repo_url }}`, `{{ revision }}`, `{{ source_branch }}`, `{{ repo_owner }}`, `{{ repo_name }}`.

### Hook Scripts

`before_run` and `after_run` inject setup/teardown steps into every task:

```yaml
defaults:
  image: golang:1.22
  before_run: echo "Starting task"
  after_run: echo "Task done"

tasks:
  build:
    run: go build -o app .    # gets before_run + after_run steps

  test:
    before_run: go mod download    # overrides defaults for this task
    run: go test ./...
```

Hooks can specify their own image using the struct form:

```yaml
defaults:
  before_run:
    image: alpine:latest
    run: echo "setup with alpine"
  after_run:
    image: busybox:latest
    run: echo "cleanup with busybox"
```

Hooks are injected into **all tasks**, including `uses:` tasks. `after_run` always runs, even if the task fails.

### Secrets

Mount Kubernetes secrets as workspace volumes:

```yaml
secrets:
  registry-auth: my-registry-secret
  git-credentials: git-creds
```

These are available as workspace volumes to tasks that need them.

### Persistent Cache

Cache dependencies across pipeline runs using [tekton-caches](https://github.com/openshift-pipelines/tekton-caches). Define the cache backend at the top level, then add a `cache:` block to any task that needs caching:

```yaml
cache:
  image: oci://quay.io/myorg/my-cache
  credentials: registry-auth      # references a secrets: entry

tasks:
  build:
    image: golang:1.22
    run: go build -o app .
    cache:
      path: /go/pkg/mod
      key: ["**/go.sum"]

  build-jar:
    uses: maven:0.4.0
    params:
      GOALS: ["clean", "package"]
    cache:                          # works on uses: tasks too
      path: /workspace/maven-local-repo/.m2
      key: ["**/pom.xml"]
```

Any task — both inline (`run:`) and resolved (`uses:`) — can have a `cache:` block. The compiler auto-injects `cache-fetch` and `cache-upload` steps around the task's own steps. Multiple cache paths per task are also supported:

```yaml
  build:
    image: golang:1.22
    run: go build -o app .
    cache:
      paths:
        - path: /go/pkg/mod
          key: ["**/go.sum"]
        - path: /root/.cache/go-build
          key: ["**/go.sum", "**/go.mod"]
```

### Finally (Cleanup Tasks)

Tasks in `finally:` always run, regardless of whether the pipeline succeeded or failed:

```yaml
finally:
  notify:
    image: curlimages/curl:latest
    run: |
      curl -X POST https://hooks.slack.com/webhook \
        -d '{"text": "Pipeline completed"}'

  cleanup:
    image: alpine:latest
    run: rm -rf $(workspace)/tmp
```

### Manual Approval Gates

Pause the pipeline for human approval before a task runs:

```yaml
tasks:
  build:
    image: alpine
    run: echo building

  deploy:
    needs: [build]
    approval:
      approvers: [alice, bob, "group:platform"]
      required: 2
      description: "Approve production deployment"
      timeout: 120m
    image: alpine
    run: echo deploying
```

### Tekton Pass-Through

For advanced Tekton features not covered by the DSL, use the `tekton:` escape hatch to inject raw fields. Works on both inline and `uses:` tasks:

```yaml
tasks:
  build:
    image: golang:1.22
    run: go build .
    tekton:
      stepTemplate:
        resources:
          requests:
            cpu: "500m"
            memory: "256Mi"

  build-jar:
    uses: maven:0.4.0
    params:
      GOALS: ["clean", "package"]
    tekton:                          # merged into the resolved task spec
      stepTemplate:
        resources:
          requests:
            memory: 2Gi
```

## Real-World Example

A complete Java CI/CD pipeline that clones, builds, scans, and deploys:

```yaml
name: spring-petclinic

on:
  push:
    branches: [main]

params:
  image_url: "quay.io/myorg/spring-petclinic:{{ source_branch }}"

cache:
  image: oci://quay.io/myorg/m2-cache
  credentials: quay-credentials

defaults:
  before_run:
    image: registry.access.redhat.com/ubi9-minimal
    run: echo "#### BEFORE RUN ####"
  after_run:
    image: registry.access.redhat.com/ubi9-minimal
    run: echo "#### AFTER RUN ####"

tasks:
  git-source:
    uses: git-clone:0.10.0
    params:
      url: {{ repo_url }}
      revision: {{ revision }}

  build-jar:
    uses: maven:0.4.0
    params:
      GOALS: ["clean", "package", "-Dcheckstyle.skip=true"]
    cache:
      path: /workspace/maven-local-repo/.m2
      key: ["**/pom.xml"]

  build-image:
    uses: cluster://openshift-pipelines/buildah
    params:
      IMAGE: $(params.image_url)
      TLS_VERIFY: false

  security-scan:
    uses: trivy-scanner
    needs: ["build-image"]
    params:
      ARGS: ["image", "--severity", "HIGH,CRITICAL", "--exit-code", "0"]
      IMAGE_PATH: "$(tasks.build-image.results.IMAGE_URL)@$(tasks.build-image.results.IMAGE_DIGEST)"

  git-manifests:
    uses: git-clone:0.10.0
    needs: ["build-image"]
    params:
      url: https://github.com/myorg/app-config

  deploy:
    uses: cluster://openshift-pipelines/openshift-client
    needs: [security-scan, git-manifests]
    before_run: |
      cd $(workspace)/environments/dev
      sed -i "s|spring-petclinic:.*|spring-petclinic:latest@$(tasks.build-image.results.IMAGE_DIGEST)|g" kustomization.yaml
    params:
      SCRIPT: oc apply -k $(workspace)/environments/dev

finally:
  notify:
    image: curlimages/curl:latest
    run: |
      curl -X POST https://hooks.slack.com/ -d '{"text": "Pipeline completed"}'
```

## CLI Reference

```
tkn-dsl generate <file>        Compile DSL to Tekton PipelineRun YAML
tkn-dsl validate <file>        Validate without generating output
tkn-dsl schema                 Print JSON Schema for IDE integration
tkn-dsl init [file]            Scaffold an annotated example DSL file
tkn-dsl version                Print version information
```

### Flags

| Flag | Description |
|---|---|
| `-o, --output yaml\|json` | Output format (default: yaml) |
| `--output-dir <dir>` | Write each CR to a separate file in a directory |
| `--repo owner/name` | Repository identity for cache namespacing |
| `--set key=value` | Override param defaults |
| `--no-cache` | Skip cache step injection |
| `--no-resolve` | Skip resolving external tasks (keep taskRef) |
| `--strict` | Treat warnings as errors |

## IDE Support

Generate a JSON Schema for autocomplete and validation in your editor:

```bash
tkn-dsl schema > schema.json
```

Then add this comment to the top of your DSL files:

```yaml
# yaml-language-server: $schema=./schema.json
name: my-pipeline
# ... autocomplete and validation now work
```

## Next Steps

- See [DESIGN.md](../DESIGN.md) for the full design document
- Browse [testdata/examples/](../testdata/examples/) for more DSL examples
- Run `tkn-dsl init` to scaffold an annotated pipeline with inline documentation
