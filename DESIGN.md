# Design Document: Tekton Pipeline DSL

## Context

Tekton's Kubernetes-native API requires verbose Custom Resources to author pipelines, creating a steep learning curve compared to GitHub Actions and GitLab CI. This design document explores a simplified YAML DSL that translates into Tekton Pipeline CRs, with a Go CLI performing the translation. The goal is to validate whether a simpler authoring experience can be layered on top of Tekton without sacrificing its capabilities.

**Deliverable**: A design document covering DSL syntax, translation architecture, CLI structure, and key technical decisions. No code in this phase.

**Two-phase rollout**:

- **Phase 1 (Experiment)**: A standalone Go CLI (`tkn-dsl`) that compiles DSL files into PaC-compatible PipelineRun YAML. Users run `tkn-dsl generate pipeline.yaml > .tekton/pipeline.yaml` and commit the generated output. PaC processes the generated PipelineRun YAML as usual.

- **Phase 2 (Native integration)**: The DSL compiler is integrated directly into [Pipelines-as-Code](https://github.com/openshift-pipelines/pipelines-as-code). Users commit `.tekton/*.dsl.yaml` files to their repositories, and PaC natively understands the DSL format — no generation step needed. PaC's `ReadTektonTypes()` function in `pkg/resolve/resolve.go` is extended to detect DSL files, compile them to PipelineRun objects in-memory, and feed them into the existing matching and resolution pipeline. The DSL becomes the primary authoring interface for Tekton pipelines, replacing hand-written PipelineRun YAML for most use cases.

This two-phase approach means the compiler must be designed as an **importable Go library**, not just a CLI wrapper. The CLI is a thin layer on top of the library for phase 1; PaC imports the same library in phase 2.

---

## 1. DSL Syntax Design

### 1.1 Example Pipeline

```yaml
name: build-and-deploy

on:
  pull_request:
    branches: [main, "release-*"]
    paths: ["src/**", "go.mod", "go.sum"]
  push:
    branches: [main]

params:
  repo-url: "https://github.com/example/repo"
  image-tag: "latest"

secrets:
  git-credentials: git-creds

tasks:
  clone:
    image: alpine/git:latest
    run: git clone $(params.repo-url) $(workspace)/repo
    results:
      commit-sha:
        description: "The git commit SHA"

  build:
    needs: [clone]
    image: golang:1.21
    steps:
      - name: compile
        run: |
          cd $(workspace)/repo
          go build -o app .
      - name: test
        run: |
          cd $(workspace)/repo
          go test ./...

  deploy:
    needs: [build]
    if: params.image-tag not in ("dev", "test")
    image: bitnami/kubectl:latest
    run: kubectl apply -f $(workspace)/repo/k8s/

finally:
  notify:
    image: curlimages/curl:latest
    run: |
      curl -X POST https://hooks.slack.com/... \
        -d '{"text": "Pipeline completed"}'
```

### 1.2 Syntax Conventions

| Feature | DSL Syntax | Notes |
|---|---|---|
| **Git triggers** | `on:` top-level block | GHA-style; compiles to PaC annotations |
| **Single-step task** | `run:` directly on the task | No `steps:` needed |
| **Multi-step task** | `steps:` array with `name:`, `run:`, optional `image:` | Step `image:` overrides task-level `image:` |
| **Dependencies** | `needs: [task-a, task-b]` | Three-layer: declaration order (sequential by default), result refs (auto-detected), explicit `needs:` (overrides) |
| **Variables** | `$(params.foo)`, `$(tasks.X.results.Y)` | Tekton-native syntax, passed through unchanged |
| **Built-in variables** | `$(repo_url)`, `$(revision)`, `$(source_branch)`, etc. | PaC template variables, passed through |
| **Shared filesystem** | `$(workspace)` | Auto-provisioned, mounted to all tasks at `/workspace` |
| **Conditionals** | `if: params.branch == 'main'` | GHA-style expressions, compiled to Tekton WhenExpressions |
| **Cleanup tasks** | `finally:` top-level block | Same task syntax, always runs |
| **External tasks** | `uses: git-clone` or `uses: git-clone:0.9` | Resolved via PaC resolver (Artifact Hub, HTTP, local) |
| **Secrets** | `secrets:` top-level block | Mounted as volumes or env vars, no workspace boilerplate |
| **Concurrency** | `concurrency:` top-level | Controls parallel runs; compiles to PaC annotation |
| **Cache** | `cache:` top-level block | Persistent cross-run caching via tekton-caches StepActions |
| **Manual approval** | `approval:` block on a task | Compiles to ApprovalTask custom task before gated task |
| **Hook scripts** | `before_run:` / `after_run:` on task or in `defaults:` | String or struct form (`image:` + `run:`); injected into all tasks (including `uses:`); `after_run` runs even on failure |
| **Retries/timeout** | `retries: 3`, `timeout: 30m` | Per-task |
| **Resources** | `resources: {cpu: 500m, memory: 256Mi}` | Per-step requests/limits |
| **Sidecars** | `sidecars:` array on a task | `image:`, `ports:`, `env:` |
| **Tekton pass-through** | `tekton:` block on pipeline/task/step | Arbitrary Tekton fields merged into generated CR |

### 1.3 Defaults That Reduce Verbosity

- No `apiVersion` required — CLI version determines schema
- Param shorthand: `param-name: value` sets the default (type inferred from value). Full form (`description:`/`default:`/`type:`) available when needed.
- Shared filesystem auto-provisioned — no workspace declarations needed
- Task/step names auto-generated from YAML keys
- Tasks without `needs:` run sequentially by declaration order (each task depends on the previous one)
- Timeouts, retries, service accounts omitted = cluster defaults apply

### 1.4 What This Replaces

A simple "clone + build" pipeline that takes ~80 lines of Tekton YAML (separate Task CRs + Pipeline CR + PipelineRun CR) reduces to ~20 lines of DSL.

---

## 2. Key Design Decisions

### 2.1 Implicit Shared Filesystem

**Problem**: Tekton workspaces are the #1 source of verbosity and confusion. Users must declare workspaces at the pipeline level, bind them in each task, configure volume sources in the PipelineRun, and manage mount paths — all to share files between tasks.

**Solution**: The DSL auto-provisions a shared workspace. Every task gets a PVC-backed volume mounted at `/workspace` with no user configuration.

- `$(workspace)` resolves to the shared mount path (`/workspace`)
- The compiler generates a single `VolumeClaimTemplate` in the PipelineRun and binds it to every task automatically
- All tasks read/write to the same PVC — files written by `clone` are visible to `build`

**What the compiler generates behind the scenes**:
```yaml
# Pipeline spec (auto-generated workspace declaration)
spec:
  workspaces:
    - name: shared-workspace
  tasks:
    - name: clone
      workspaces:
        - name: shared-workspace
          workspace: shared-workspace
      taskSpec:
        workspaces:
          - name: shared-workspace
        steps:
          - ...

# PipelineRun spec (auto-generated volume)
spec:
  workspaces:
    - name: shared-workspace
      volumeClaimTemplate:
        spec:
          accessModes: [ReadWriteOnce]
          resources:
            requests:
              storage: 1Gi   # default, configurable via top-level `storage:`
```

**Customization** (optional, for advanced users):
```yaml
# Override the default shared storage size
storage:
  size: 5Gi
  storageClass: fast-ssd
```

**Secrets** are handled via a dedicated `secrets:` block. Each entry maps an **alias** to a Kubernetes Secret name:

```yaml
secrets:
  dockerconfig: quay-credentials   # alias: dockerconfig → K8s Secret: quay-credentials
  git: git-creds                    # alias: git → K8s Secret: git-creds
```

The compiler creates a **pipeline-level** workspace named `secret-<alias>` for each entry, backed by the referenced Kubernetes Secret.

**Automatic binding**: When a resolved task (`uses:`) has a workspace whose name matches a secret alias, the compiler automatically binds it — no explicit `workspaces:` mapping needed. Name the alias to match the task's workspace:

```yaml
secrets:
  dockerconfig: quay-credentials   # alias matches buildah's "dockerconfig" workspace

tasks:
  build-image:
    uses: cluster://openshift-pipelines/buildah
    params:
      IMAGE: quay.io/myorg/myapp:latest
    # No workspaces: needed! The compiler sees that buildah has a workspace
    # called "dockerconfig" which matches the secrets alias "dockerconfig",
    # and auto-binds it to the "secret-dockerconfig" pipeline workspace.
```

**Explicit override**: If the alias doesn't match the task workspace name, use the `workspaces:` block to map manually:

```yaml
secrets:
  quay: quay-credentials           # alias is "quay", not "dockerconfig"

tasks:
  build-image:
    uses: cluster://openshift-pipelines/buildah
    params:
      IMAGE: quay.io/myorg/myapp:latest
    workspaces:
      dockerconfig: secret-quay     # manually bind: task ws "dockerconfig" → pipeline ws "secret-quay"
```

**Cache credentials**: `cache.credentials` is also auto-wired — reference the secret alias and the compiler handles the rest:

```yaml
secrets:
  dockerconfig: quay-credentials

cache:
  image: oci://quay.io/myorg/cache
  credentials: dockerconfig         # auto-wired to secret-dockerconfig
```

### 2.2 GitHub-Style Conditionals

**Problem**: Tekton's `WhenExpressions` use a verbose `input/operator/values` structure that is hard to read.

**DSL syntax** — use `if:` with human-readable expressions:
```yaml
tasks:
  deploy:
    if: params.branch == 'main'
    # ...

  notify:
    if: params.environment in ("staging", "production")
    # ...

  skip-dev:
    if: params.tag != 'dev'
    # ...
```

**Supported operators**: `==`, `!=`, `in`, `not in`

**Compilation**: The expression parser translates to Tekton WhenExpressions:

| DSL `if:` | Tekton WhenExpression |
|---|---|
| `params.branch == 'main'` | `input: $(params.branch)`, `operator: in`, `values: ["main"]` |
| `params.branch != 'dev'` | `input: $(params.branch)`, `operator: notin`, `values: ["dev"]` |
| `params.env in ("stg", "prod")` | `input: $(params.env)`, `operator: in`, `values: ["stg", "prod"]` |
| `params.tag not in ("dev", "test")` | `input: $(params.tag)`, `operator: notin`, `values: ["dev", "test"]` |

The expression parser is intentionally limited to what Tekton WhenExpressions can represent. Compound expressions (`&&`, `||`) are not supported because Tekton does not support them natively. This avoids creating an abstraction that suggests more power than the underlying system provides.

**Result references** work too:
```yaml
  deploy:
    if: tasks.check.results.status == 'ready'
```

### 2.3 Compact Parameters

**Problem**: Requiring `type: string`, `description:`, and `default:` on every parameter is boilerplate. Most params are strings with a default value.

**Solution**: Parameters use a compact `param-name: value` shorthand where the value is the default. Types are inferred from the value. The full form is available when description or explicit type is needed.

```yaml
params:
  # Shorthand: bare value = default (type inferred as string)
  branch: "main"
  image-tag: "latest"

  # Shorthand: bare list = default (type inferred as array)
  targets: ["staging", "production"]

  # Full form: when description is needed
  config:
    description: "Build configuration"
    default:
      optimize: true
      debug: false

  # Full form: explicit type with no default
  extra-args:
    type: array
```

**Inference rules**:
1. Bare scalar value → `type: string`, value is the default
2. Bare list value → `type: array`, list is the default
3. Map with `default:` that is a scalar → `string`
4. Map with `default:` that is a list → `array`
5. Map with `default:` that is a map → `object`
6. No `type:`, no `default:` → `string` with no default
7. Explicit `type:` always wins

### 2.4 Tekton Pass-Through (Escape Hatch)

**Problem**: Tekton continuously adds new fields to Pipeline, Task, PipelineRun, and TaskRun APIs (e.g., `podTemplate`, `stepTemplate`, `securityContext`, affinity, tolerations). The DSL cannot anticipate every field. Without an escape hatch, users hit walls and abandon the DSL.

**Solution**: A `tekton:` block at pipeline, task, and step levels. Its contents are merged directly into the corresponding generated Tekton CR with no interpretation or validation by the DSL compiler.

```yaml
name: build-with-gpu

tasks:
  train:
    image: nvidia/cuda:12.0-runtime
    run: python train.py
    tekton:
      # Merged into the TaskSpec
      stepTemplate:
        resources:
          requests:
            nvidia.com/gpu: 1
      podTemplate:
        nodeSelector:
          gpu-type: a100
        tolerations:
          - key: nvidia.com/gpu
            operator: Exists
            effect: NoSchedule
        securityContext:
          runAsNonRoot: true
          runAsUser: 1000

# Pipeline-level pass-through
tekton:
  # Merged into the Pipeline metadata or spec
  metadata:
    labels:
      team: ml-platform
    annotations:
      custom.io/priority: high
```

**How merging works**:
- `tekton:` on a task → merged into the generated `taskSpec` (fields like `podTemplate`, `stepTemplate`, `sidecars`, etc.)
- `tekton:` on a step → merged into the generated `step` (fields like `securityContext`, `resources`, `volumeMounts`, etc.)
- `tekton:` at pipeline level → merged into the generated `Pipeline` CR (fields like `metadata.labels`, `metadata.annotations`, etc.)
- A separate `tekton.pipelineRun:` block can target the generated PipelineRun CR

**Design principles**:
- The compiler does NOT validate `tekton:` contents against the Tekton API — it passes them through as-is. Validation happens at apply time by the Kubernetes API server.
- If a `tekton:` field conflicts with a compiler-generated field (e.g., user sets `tekton.workspaces:` which the compiler also generates), the `tekton:` value takes precedence with a warning.
- This ensures **forward compatibility**: when Tekton adds a new field to TaskSpec in a future release, users can use it immediately via `tekton:` without waiting for a DSL update.

### 2.5 Pipelines-as-Code Integration

[Pipelines-as-Code](https://pipelinesascode.com) (PaC) is the standard way to run Tekton pipelines from Git repositories. It handles event triggering, task resolution, status reporting, and GitOps commands. The DSL should generate PaC-compatible PipelineRuns rather than duplicating PaC's capabilities.

#### 2.5.1 Git Event Triggers (`on:`)

PaC uses annotations on PipelineRuns to match Git events. The DSL introduces a GHA-style `on:` block that compiles to these annotations.

**DSL syntax:**
```yaml
# Simple: trigger on PR to main
on:
  pull_request:
    branches: [main]

# Multiple events with path filtering
on:
  pull_request:
    branches: [main, "release-*"]
    paths: ["src/**", "pkg/**", "go.mod"]
    paths_ignore: ["docs/**", "*.md"]
  push:
    branches: [main]

# Comment-triggered pipeline
on:
  comment: "^/deploy"

# Label-triggered pipeline
on:
  pull_request:
    branches: [main]
    labels: [approved, ready-to-merge]

# Advanced: CEL expression for full control
on:
  cel: |
    event == "pull_request" && target_branch == "main"
    && source_branch.matches("feat/.*")
    && !files.all.all(x, x.matches("^docs/"))

```

**Compilation to PaC annotations:**

| DSL `on:` | PaC Annotation |
|---|---|
| `pull_request:` | `pipelinesascode.tekton.dev/on-event: "[pull_request]"` |
| `push:` | `pipelinesascode.tekton.dev/on-event: "[push]"` |
| `pull_request:` + `push:` | `pipelinesascode.tekton.dev/on-event: "[pull_request, push]"` |
| `branches: [main, "release-*"]` | `pipelinesascode.tekton.dev/on-target-branch: "[main, release-*]"` |
| `paths: ["src/**"]` | `pipelinesascode.tekton.dev/on-path-change: "[src/**]"` |
| `paths_ignore: ["docs/**"]` | `pipelinesascode.tekton.dev/on-path-change-ignore: "[docs/**]"` |
| `comment: "^/deploy"` | `pipelinesascode.tekton.dev/on-comment: "^/deploy"` |
| `labels: [approved]` | `pipelinesascode.tekton.dev/on-label: "[approved]"` |
| `cel: <expression>` | `pipelinesascode.tekton.dev/on-cel-expression: "<expression>"` |

The compiler generates a **single PipelineRun** from the `on:` block. Multiple event types are combined into a single `on-event` annotation.

#### 2.5.2 Built-in Variables (PaC Template Variables)

PaC provides dynamic template variables that are expanded at runtime. The DSL passes these through using `$(...)` syntax:

| Variable | Description |
|---|---|
| `$(repo_url)` | Complete repository URL |
| `$(revision)` | Full commit SHA |
| `$(source_branch)` | Source branch name |
| `$(target_branch)` | Target branch name |
| `$(repo_name)` | Repository name |
| `$(repo_owner)` | Repository owner/org (full slug for hierarchical VCS) |
| `$(source_url)` | Source repository URL (for forks) |
| `$(event)` | Normalized event type: `push`, `pull_request`, or `incoming` |
| `$(event_type)` | Provider-specific event type from webhook header |
| `$(pull_request_number)` | PR/MR number (pull_request events only) |
| `$(pull_request_labels)` | PR labels (newline-separated) |
| `$(sender)` | Commit author username or account ID |
| `$(trigger_comment)` | GitOps command that triggered the run (e.g., `/test`) |
| `$(git_tag)` | Git tag pushed (tag push events only; empty otherwise) |
| `$(target_namespace)` | Namespace where PipelineRun executes |
| `$(git_auth_secret)` | Auto-generated secret with provider token (for private repos) |

These replace the need to manually define `repo-url` as a param in most cases:

```yaml
tasks:
  clone:
    image: alpine/git:latest
    run: git clone $(repo_url) $(workspace)/repo
```

The compiler translates `$(repo_url)` to PaC's `{{ repo_url }}` template syntax in the generated PipelineRun.

#### 2.5.3 Task Resolution via PaC Resolver

PaC already resolves remote tasks from multiple sources. The DSL's `uses:` keyword compiles to PaC task annotations rather than embedding task resolution logic in the CLI.

**DSL syntax:**
```yaml
tasks:
  clone:
    uses: git-clone            # Artifact Hub (latest)
  lint:
    uses: git-clone:0.9        # Artifact Hub (pinned version)
  custom:
    uses: https://raw.githubusercontent.com/org/repo/main/tasks/my-task.yaml  # HTTP URL
  local:
    uses: tasks/my-task.yaml   # Relative path in repo
```

**Compilation**: Each `uses:` generates a PaC annotation on the PipelineRun:
```yaml
metadata:
  annotations:
    pipelinesascode.tekton.dev/task: "git-clone"
    pipelinesascode.tekton.dev/task-1: "git-clone:0.9"
    pipelinesascode.tekton.dev/task-2: "https://raw.githubusercontent.com/org/repo/main/tasks/my-task.yaml"
    pipelinesascode.tekton.dev/task-3: "tasks/my-task.yaml"
```

The PaC resolver handles the actual fetching, authentication, and inlining at runtime. The DSL compiler does NOT need to resolve tasks itself.

#### 2.5.4 Concurrency Control

PaC supports cancelling in-progress runs when new commits arrive. The DSL exposes this simply:

```yaml
concurrency:
  cancel-in-progress: true    # Cancel previous runs on same PR/branch
```

Compiles to:
```yaml
metadata:
  annotations:
    pipelinesascode.tekton.dev/cancel-in-progress: "true"
```

#### 2.5.5 Max PipelineRun Cleanup

PaC can auto-clean old PipelineRuns. The DSL exposes this:

```yaml
cleanup:
  max-keep-runs: 5
```

Compiles to:
```yaml
metadata:
  annotations:
    pipelinesascode.tekton.dev/max-keep-runs: "5"
```

#### 2.5.6 What PaC Already Handles (Do NOT Duplicate in DSL)

| Capability | PaC Feature | DSL Approach |
|---|---|---|
| Git event webhook handling | EventListener + Triggers | Not in DSL — PaC handles |
| Status reporting to Git provider | Automatic check/commit status | Not in DSL — PaC handles |
| GitOps commands (`/test`, `/retest`, `/cancel`) | Comment-triggered re-runs | Not in DSL — PaC handles |
| Access control (`/ok-to-test`) | ACL + OWNERS file | Not in DSL — PaC handles |
| Task resolution from Hub/HTTP/repo | PaC resolver | DSL generates annotations; PaC resolves |
| Git auth for private repos | Auto-generated `git_auth_secret` | DSL references `$(git_auth_secret)` |
| Log streaming | `tkn pac logs` | Not in DSL — use existing tooling |

#### 2.5.7 Generated Output Structure for PaC

When `on:` is present, the compiler generates a PipelineRun (not Pipeline + PipelineRun) with an embedded `pipelineSpec` — this is PaC's preferred format:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: build-and-deploy
  annotations:
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main, release-*]"
    pipelinesascode.tekton.dev/on-path-change: "[src/**, go.mod, go.sum]"
    pipelinesascode.tekton.dev/task: "[git-clone]"
    pipelinesascode.tekton.dev/cancel-in-progress: "true"
    pipelinesascode.tekton.dev/max-keep-runs: "5"
spec:
  pipelineSpec:
    # ... inline pipeline spec with tasks, params, workspaces
  workspaces:
    - name: shared-workspace
      volumeClaimTemplate:
        spec:
          accessModes: [ReadWriteOnce]
          resources:
            requests:
              storage: 1Gi
```

The user places this generated file in `.tekton/` and PaC takes over from there.

#### 2.5.8 Full Example with PaC Features

```yaml
name: ci

on:
  pull_request:
    branches: [main]
    paths: ["src/**", "pkg/**"]
    paths_ignore: ["docs/**"]
  push:
    branches: [main]

concurrency:
  cancel-in-progress: true

cleanup:
  max-keep-runs: 5

tasks:
  clone:
    uses: git-clone
    params:
      url: $(repo_url)
      revision: $(revision)

  lint:
    needs: [clone]
    image: golangci/golangci-lint:latest
    run: |
      cd $(workspace)/repo
      golangci-lint run ./...

  test:
    needs: [clone]
    image: golang:1.21
    run: |
      cd $(workspace)/repo
      go test -v ./...

  build:
    needs: [lint, test]
    image: golang:1.21
    run: |
      cd $(workspace)/repo
      go build -o app .

finally:
  notify:
    if: tasks.build.results.status == 'failed'
    image: curlimages/curl:latest
    run: |
      curl -X POST $(params.slack-webhook) \
        -d '{"text": "CI failed for $(repo_name)#$(pull_request_number)"}'
```

### 2.6 Persistent Cache Across Pipeline Runs

**Problem**: The implicit shared filesystem (`$(workspace)`) only lives for a single pipeline run. Build caches (Go module cache, `node_modules/`, Maven `.m2/`, Python `.venv/`) must be re-fetched every run, wasting time. GitLab CI solves this with a built-in `cache:` keyword. Tekton has no native caching, but the [tekton-caches](https://github.com/openshift-pipelines/tekton-caches) project provides `cache-fetch` and `cache-upload` StepActions that store/retrieve caches from OCI registries, S3, GCS, or Azure Blob Storage.

**Solution**: A top-level `cache:` block in the DSL. The compiler injects `cache-fetch` and `cache-upload` StepActions into the appropriate tasks automatically — users never write StepAction references or manage cache parameters directly.

#### 2.6.1 DSL Syntax

```yaml
cache:
  backend: oci://registry.example.com/cache   # shared base — one place to configure
  paths:
    - path: /go/pkg/mod
      key: ["**/go.sum"]
    - path: /root/.cache/go-build
      key: ["**/go.sum", "**/go.mod"]
```

That's it. The compiler auto-generates unique, conflict-free cache URIs by combining:
- The `backend` base URL
- The **repository identity** (`{repo-owner}/{repo-name}`, from PaC context or `--repo` flag)
- The pipeline `name:` (from the top of the DSL file)
- A sanitized version of the `path`
- The `{{hash}}` computed from `key` files

For a pipeline named `myapp-ci-cd` in the `acme/backend-service` repository, the above generates:
```
oci://registry.example.com/cache/acme/backend-service/myapp-ci-cd/go-pkg-mod:{{hash}}
oci://registry.example.com/cache/acme/backend-service/myapp-ci-cd/go-build:{{hash}}
```

The repo identity ensures caches never collide across repositories, even if two repos have pipelines with the same name. In **phase 2** (native PaC integration), the repo owner and name are available automatically from the Repository CR and Git event context. In **phase 1** (CLI), they are derived from the Git remote URL of the current working directory, or specified explicitly via `--repo owner/name`.

**All supported backends:**
```yaml
# OCI registry (recommended — works with any container registry)
cache:
  backend: oci://registry.example.com/cache

# S3
cache:
  backend: s3://my-bucket/tekton-cache

# GCS
cache:
  backend: gs://my-bucket/tekton-cache
```

**Fields:**

| Field | Required | Description |
|---|---|---|
| `cache.backend` | Yes | Base storage URI. Supports `oci://`, `s3://`, `gs://` schemes. |
| `cache.credentials` | No | References a `secrets:` entry for backend auth (applies to all entries) |
| `cache.insecure` | No | Use insecure mode for OCI registries (default: `false`) |
| `cache.paths[].path` | Yes | Directory to cache (absolute or relative to `$(workspace)`) |
| `cache.paths[].key` | Yes | Glob patterns for files whose content determines the cache hash |

**Generated URI format:**

| Backend | Generated URI |
|---|---|
| `oci://registry.example.com/cache` | `oci://registry.example.com/cache/{repo-owner}/{repo-name}/{pipeline-name}/{sanitized-path}:{{hash}}` |
| `s3://my-bucket/tekton-cache` | `s3://my-bucket/tekton-cache/{repo-owner}/{repo-name}/{pipeline-name}/{sanitized-path}/{{hash}}` |
| `gs://my-bucket/tekton-cache` | `gs://my-bucket/tekton-cache/{repo-owner}/{repo-name}/{pipeline-name}/{sanitized-path}/{{hash}}` |

**Path sanitization**: `/go/pkg/mod` → `go-pkg-mod`, `/root/.cache/go-build` → `go-build`, `node_modules` → `node-modules`

**Shorthand** for single-cache pipelines:
```yaml
cache:
  backend: oci://registry.example.com/cache
  path: /go/pkg/mod
  key: ["**/go.sum"]
```

#### 2.6.2 How It Compiles

The compiler transforms each `cache:` entry into two injected steps per task that uses the cached path:

1. **`cache-fetch`** — injected as the **first step** of the task, before any user-defined steps
2. **`cache-upload`** — injected as the **last step** of the task, after all user-defined steps

The compiler also generates the PaC StepAction annotation to resolve the `cache-fetch` and `cache-upload` StepActions from the tekton-caches repository.

**Which tasks get cache steps?** The compiler injects cache steps into tasks whose `run:` or `steps:` reference the cached `path`. If no specific task references the path, the cache is applied to **all inline tasks** (tasks with `run:` or `steps:`, not `uses:`).

**Example — DSL input:**
```yaml
name: my-pipeline

cache:
  backend: oci://registry.example.com/cache
  paths:
    - path: /go/pkg/mod
      key: ["**/go.sum"]

tasks:
  clone:
    uses: git-clone
    params:
      url: $(repo_url)
      revision: $(revision)

  build:
    needs: [clone]
    image: golang:1.22
    run: |
      cd $(workspace)/repo
      go build -o app ./cmd/server
```

**Generated output (abbreviated):**
```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: my-pipeline
  annotations:
    pipelinesascode.tekton.dev/task: "[git-clone]"
    # StepAction references for PaC resolver
    pipelinesascode.tekton.dev/step-action: >-
      [https://raw.githubusercontent.com/openshift-pipelines/tekton-caches/main/tekton/cache-fetch.yaml,
       https://raw.githubusercontent.com/openshift-pipelines/tekton-caches/main/tekton/cache-upload.yaml]
spec:
  pipelineSpec:
    tasks:
      - name: build
        runAfter: [clone]
        taskSpec:
          steps:
            # ── Injected: cache-fetch (before user steps) ──
            - name: cache-fetch-go-pkg-mod
              ref:
                name: cache-fetch
              params:
                - name: PATTERNS
                  value: ["**/go.sum"]
                - name: SOURCE
                  # Auto-generated: {backend}/{repo-owner}/{repo-name}/{pipeline-name}/{sanitized-path}:{{hash}}
                  value: "oci://registry.example.com/cache/acme/backend-service/my-pipeline/go-pkg-mod:{{hash}}"
                - name: CACHE_PATH
                  value: "/go/pkg/mod"
                - name: WORKING_DIR
                  value: "$(workspaces.shared-workspace.path)"

            # ── User-defined step ──
            - name: build
              image: golang:1.22
              script: |
                cd $(workspaces.shared-workspace.path)/repo
                go build -o app ./cmd/server

            # ── Injected: cache-upload (after user steps) ──
            - name: cache-upload-go-pkg-mod
              ref:
                name: cache-upload
              params:
                - name: PATTERNS
                  value: ["**/go.sum"]
                - name: TARGET
                  # Same auto-generated path as cache-fetch
                  value: "oci://registry.example.com/cache/acme/backend-service/my-pipeline/go-pkg-mod:{{hash}}"
                - name: CACHE_PATH
                  value: "/go/pkg/mod"
                - name: WORKING_DIR
                  value: "$(workspaces.shared-workspace.path)"
                - name: FETCHED
                  value: "$(steps.cache-fetch-go-pkg-mod.results.fetched)"
```

#### 2.6.3 Cache Credentials

Cache backends often require authentication. A single `credentials:` field on the `cache:` block applies to all entries:

```yaml
secrets:
  registry-auth: regcred          # for OCI backend

cache:
  backend: oci://registry.example.com/cache
  credentials: registry-auth      # references a secrets: entry, applies to all paths
  paths:
    - path: /go/pkg/mod
      key: ["**/go.sum"]
    - path: /root/.cache/go-build
      key: ["**/go.sum", "**/go.mod"]
```

The compiler maps `credentials:` to the StepAction's `DOCKER_CONFIG`, `AWS_CONFIG_FILE` / `AWS_SHARED_CREDENTIALS_FILE`, or `GOOGLE_APPLICATION_CREDENTIALS` param depending on the backend scheme (`oci://`, `s3://`, `gs://`).

#### 2.6.4 Multiple Caches

Pipelines often have multiple independent caches. Just list them under `paths:` — the backend and credentials are shared:

```yaml
name: fullstack-app

cache:
  backend: oci://registry.example.com/cache
  credentials: registry-auth
  paths:
    - path: /go/pkg/mod
      key: ["**/go.sum"]
    - path: /root/.cache/go-build
      key: ["**/go.sum", "**/go.mod"]
    - path: frontend/node_modules
      key: ["frontend/package-lock.json"]
```

Generated URIs (all unique, no conflicts possible — even across repos):
```
oci://registry.example.com/cache/acme/fullstack/fullstack-app/go-pkg-mod:{{hash}}
oci://registry.example.com/cache/acme/fullstack/fullstack-app/go-build:{{hash}}
oci://registry.example.com/cache/acme/fullstack/fullstack-app/node-modules:{{hash}}
```

Each entry generates its own `cache-fetch` / `cache-upload` step pair. Multiple fetch steps run sequentially at the start of the task; multiple upload steps run sequentially at the end.

#### 2.6.5 Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Backend scope | Top-level `cache.backend:` shared across all entries | One place to configure; avoids repeating the same registry/bucket per cache entry |
| Cache URI uniqueness | Auto-generated as `{backend}/{repo-owner}/{repo-name}/{pipeline-name}/{sanitized-path}:{{hash}}` | Repo identity + pipeline name = globally unique. In phase 2 (PaC native), repo identity comes from Repository CR automatically. In phase 1 (CLI), derived from Git remote or `--repo` flag. |
| Path sanitization | `/go/pkg/mod` → `go-pkg-mod` (strip leading `/`, replace `/` and `.` with `-`) | Produces valid OCI tags and S3 keys |
| Where to inject steps | First/last steps of the task | Matches GitLab's model: cache is restored before work, saved after |
| Which tasks get cache steps | Tasks that reference the cached path, or all inline tasks | Avoids caching in tasks that don't need it (e.g., `uses: git-clone`) |
| Skip upload if cache hit | Yes — `FETCHED` result from `cache-fetch` passed to `cache-upload` | Avoids redundant uploads, saves time and bandwidth |
| StepAction source | Raw GitHub URL from `openshift-pipelines/tekton-caches` | PaC resolver fetches at runtime; user doesn't install StepActions manually |

### 2.7 Manual Approval Gates

**Problem**: Production deployments often require human approval before proceeding. GitLab CI supports `when: manual` for this. Tekton has no built-in approval mechanism, but the [manual-approval-gate](https://github.com/openshift-pipelines/manual-approval-gate) project provides an `ApprovalTask` custom task that pauses a pipeline until designated users approve or reject.

**Solution**: An `approval:` block on any task in the DSL. The compiler generates a custom task reference to `ApprovalTask` that runs before the gated task.

#### 2.7.1 DSL Syntax

```yaml
tasks:
  deploy-staging:
    needs: [build]
    image: bitnami/kubectl:latest
    run: kubectl apply -f k8s/staging/

  deploy-production:
    needs: [deploy-staging]
    approval:
      approvers:
        - alice
        - bob
        - group:platform-team
      required: 2                           # number of approvals needed
      description: "Approve production deployment of $(repo_name)"
      timeout: 60m                          # reject automatically after timeout
    image: bitnami/kubectl:latest
    run: kubectl apply -f k8s/production/
```

**Shorthand** when only approvers are needed (defaults: required=1, timeout=60m):
```yaml
  deploy-production:
    needs: [deploy-staging]
    approval: [alice, bob, group:platform-team]
    image: bitnami/kubectl:latest
    run: kubectl apply -f k8s/production/
```

**Fields:**

| Field | Required | Default | Description |
|---|---|---|---|
| `approval.approvers` | Yes | — | Users and/or groups who can approve. Groups use `group:name` syntax. |
| `approval.required` | No | `1` | Number of approvals needed to proceed |
| `approval.description` | No | `"Approve task: {task-name}"` | Message shown to approvers |
| `approval.timeout` | No | `60m` | Time before auto-rejection |

#### 2.7.2 How It Compiles

The compiler inserts an `ApprovalTask` custom task reference **before** the gated task, linked via `runAfter`:

**DSL input:**
```yaml
tasks:
  deploy-production:
    needs: [deploy-staging]
    approval:
      approvers: [alice, bob, group:platform-team]
      required: 2
      description: "Approve production deployment"
      timeout: 60m
    image: bitnami/kubectl:latest
    run: kubectl apply -f k8s/production/
```

**Generated output (within pipelineSpec):**
```yaml
tasks:
  # ── Injected: approval gate (before gated task) ──
  - name: approve-deploy-production
    runAfter: [deploy-staging]
    taskRef:
      apiVersion: openshift-pipelines.org/v1alpha1
      kind: ApprovalTask
    params:
      - name: approvers
        value:
          - alice
          - bob
          - group:platform-team
      - name: numberOfApprovalsRequired
        value: "2"
      - name: description
        value: "Approve production deployment"
      - name: timeout
        value: "60m"

  # ── Original task (now depends on approval) ──
  - name: deploy-production
    runAfter: [approve-deploy-production]    # rewired to depend on approval
    taskSpec:
      steps:
        - name: deploy-production
          image: bitnami/kubectl:latest
          script: kubectl apply -f k8s/production/
```

The compiler:
1. Creates a new task named `approve-{task-name}` with the original task's `needs:` dependencies
2. Rewires the original task's `runAfter` to depend on the approval task instead
3. The approval task uses `taskRef` (not `taskSpec`) since it's a custom task CRD

#### 2.7.3 How Approvals Work at Runtime

- When the pipeline reaches the approval task, it **pauses** — no further tasks run until approval
- Designated approvers approve or reject via the OpenShift Console UI, or using the `tkn-approvaltask` CLI
- If the required number of approvals is reached, the pipeline continues
- If **any** approver rejects, the pipeline **fails immediately**
- If the timeout expires without sufficient approvals, the task is auto-rejected and the pipeline fails
- Approvers can change their decision (approve → reject) before the threshold is met

#### 2.7.4 Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Approval as task modifier, not separate task type | `approval:` block on an existing task | More intuitive — "deploy-production needs approval" vs defining a separate approval task |
| Injected task naming | `approve-{task-name}` | Clear relationship between approval gate and gated task |
| Dependency rewiring | Automatic | User writes `needs: [deploy-staging]` on the gated task; compiler ensures approval runs after staging and before production |
| Shorthand syntax | `approval: [alice, bob]` | Common case: just list approvers, use defaults for everything else |
| Group syntax | `group:team-name` | Matches the upstream `manual-approval-gate` convention |

### 2.8 Hook Scripts (`before_run` / `after_run`)

**Problem**: Many tasks share common setup (installing dependencies, configuring auth, printing debug info) and teardown (cleaning temp files, uploading logs). Repeating these in every task is tedious. GitLab CI solves this with `before_script` / `after_script` at both the global `default:` and job level.

**Solution**: `before_run:` and `after_run:` fields available at two levels:
1. **`defaults:`** (top-level) — applies to **all** tasks in the pipeline (both inline and `uses:` tasks)
2. **Per-task** — applies only to that task, and overrides `defaults:`

Each hook supports two forms:
- **String shorthand**: `before_run: echo "hello"` — just the script
- **Struct form**: `before_run: { image: alpine:latest, run: echo "hello" }` — script with a custom image

`after_run:` has a critical difference from regular steps: it **runs even if the task fails**, making it suitable for cleanup, log collection, and notification.

#### 2.8.1 DSL Syntax

**String shorthand** (hook uses the task's or defaults' image):
```yaml
defaults:
  image: golang:1.22
  before_run: |
    echo "Pipeline: $(repo_name) @ $(revision)"
    echo "Task starting at $(date)"
  after_run: |
    echo "Task completed, cleaning temp files"
    rm -rf /tmp/build-*
```

**Struct form with per-hook image:**
```yaml
defaults:
  image: golang:1.22
  before_run:
    image: alpine:latest
    run: echo "before with alpine"
  after_run:
    image: busybox:latest
    run: echo "after with busybox"
```

**Per-task override:**
```yaml
tasks:
  test:
    image: golang:1.22
    before_run: |
      go mod download
      go install gotest.tools/gotestsum@latest
    run: gotestsum --junitfile report.xml ./...
    after_run: |
      cp report.xml $(workspace)/test-results/

  deploy:
    uses: cluster://openshift-pipelines/openshift-client
    before_run:
      image: alpine:latest
      run: |
        cd $(workspace)/environments/dev
        sed -i "s|myapp:.*|myapp:latest@$(tasks.build.results.IMAGE_DIGEST)|g" kustomization.yaml
    params:
      SCRIPT: oc apply -k $(workspace)/environments/dev
```

**Combining defaults and per-task:**
```yaml
defaults:
  before_run: echo "Starting task"
  after_run: echo "Task done"

tasks:
  build:
    image: golang:1.22
    # Uses defaults: before_run and after_run
    run: go build -o app .

  test:
    image: golang:1.22
    before_run: go mod download        # overrides defaults: before_run
    run: go test ./...
    # Uses defaults: after_run (not overridden)
```

#### 2.8.2 How It Compiles

The compiler transforms `before_run` and `after_run` into additional steps in the generated `taskSpec`. This works for **both inline tasks** (`run:`/`steps:`) **and resolved tasks** (`uses:`).

- **`before_run`** → injected as a step **before** all user-defined steps (but after any `cache-fetch` steps)
- **`after_run`** → injected as a step **after** all user-defined steps (but before any `cache-upload` steps), using Tekton's `onError: continue` to ensure it runs even on failure

**Step ordering in the generated taskSpec:**
```
1. cache-fetch steps (if cache: is configured)
2. before_run step (from defaults: or task-level)
3. user-defined steps (run:, steps:, or resolved task steps)
4. after_run step (from defaults: or task-level, with onError: continue)
5. cache-upload steps (if cache: is configured)
```

**Hook image resolution priority:**
```
1. hook.image (from struct form: before_run: { image: ..., run: ... })
2. task.image (task-level image)
3. defaults.image (pipeline-level default image)
4. First step's image (for resolved tasks)
5. registry.access.redhat.com/ubi9-minimal (final fallback)
```

**Example — inline task:**
```yaml
defaults:
  before_run: echo "Starting"

tasks:
  build:
    image: golang:1.22
    run: go build -o app .
    after_run: echo "Build done"
```

**Generated output (within taskSpec):**
```yaml
steps:
  - name: before-run
    image: golang:1.22
    script: |
      echo "Starting"
  - name: build
    image: golang:1.22
    script: |
      go build -o app .
  - name: after-run
    image: golang:1.22
    onError: continue                # runs even if previous steps fail
    script: |
      echo "Build done"
```

**Example — resolved task with per-hook image:**
```yaml
defaults:
  before_run:
    image: alpine:latest
    run: echo "setup"

tasks:
  build:
    uses: maven:0.4.0
    params:
      GOALS: ["clean", "package"]
```

The `before-run` and `after-run` steps are injected directly into the resolved task's step list, using the hook's own image (`alpine:latest`) rather than the resolved task's images.

**`$(workspace)` in resolved tasks:** The compiler automatically translates `$(workspace)` references in hook scripts and task params to the correct workspace path for the resolved task. For example, if a resolved task has a workspace named `manifest_dir`, `$(workspace)` in its hooks becomes `$(workspaces.manifest_dir.path)`.

#### 2.8.3 `defaults:` Block

The `defaults:` block sets pipeline-wide defaults for **all tasks** — both inline (`run:`/`steps:`) and resolved (`uses:`):

```yaml
defaults:
  image: golang:1.22                # default image for inline tasks and hook steps
  before_run: |                     # runs before every task
    echo "Task: $TASK_NAME"
  after_run: |                      # runs after every task (even on failure)
    echo "Cleanup"
```

With per-hook images:
```yaml
defaults:
  image: golang:1.22
  before_run:
    image: alpine:latest            # hook-specific image
    run: echo "setup"
  after_run:
    image: busybox:latest           # different image for after hook
    run: echo "teardown"
```

**Override rules:**
- A task-level `before_run:` **replaces** (not appends to) the `defaults: before_run:`
- A task-level `after_run:` **replaces** the `defaults: after_run:`
- A task-level `image:` **replaces** the `defaults: image:` for inline tasks
- For `uses:` tasks, `defaults: before_run`/`after_run` are injected as extra steps into the resolved task spec

#### 2.8.4 Design Decisions

| Decision | Choice | Rationale |
|---|---|---|
| `after_run` runs on failure | Yes, via `onError: continue` | Primary use case is cleanup/log collection, which must run regardless of task outcome |
| Override, not append | Task-level replaces defaults, not appends | Simpler mental model; avoids confusion about execution order. If a task needs both, the task-level script can call the shared logic explicitly. |
| Hook image | Configurable per-hook via struct form, falls back to task/defaults image | Hooks often need different tools (e.g., `kustomize` in a deploy hook vs `golang` in the task) |
| String vs struct form | Both supported via `UnmarshalYAML` | String shorthand for simple hooks; struct form when a custom image is needed |
| `defaults:` scope | All tasks (inline and `uses:`) | Hooks are injected into resolved task specs too, enabling consistent setup/teardown across the pipeline |
| `$(workspace)` in resolved tasks | Auto-translated to the correct workspace name | The compiler maps `$(workspace)` to the first mapped workspace in the resolved task (e.g., `$(workspaces.manifest_dir.path)`) |
| Interaction with cache steps | Hooks run inside the cache bracket | `before_run` runs after `cache-fetch` (cache is available), `after_run` runs before `cache-upload` (modified cache is saved) |

### 2.9 Complete Feature Example

The following example uses every DSL feature in a single pipeline. It models a realistic Go microservice CI/CD workflow: clone, lint, unit test with a database sidecar, build a container image, security scan, deploy to staging, and notify on completion.

```yaml
# ─── Pipeline identity ───────────────────────────────────────────────
name: myapp-ci-cd

# ─── Git event triggers (compiles to PaC annotations) ────────────────
on:
  pull_request:
    branches: [main, "release-*"]           # glob patterns for target branch
    paths: ["cmd/**", "internal/**", "go.mod", "go.sum"]
    paths_ignore: ["docs/**", "*.md", "LICENSE"]
    labels: [ready-for-ci]                  # only trigger when label is present
  push:
    branches: [main]
  comment: "^/deploy-staging"               # trigger via PR comment

# ─── Concurrency & cleanup (PaC features) ────────────────────────────
concurrency:
  cancel-in-progress: true                  # cancel previous runs on same PR

cleanup:
  max-keep-runs: 10                         # auto-delete old PipelineRuns

# ─── Pipeline-wide defaults (applied to all tasks) ───────────────────
defaults:
  image: golang:1.22                          # default image for inline tasks and hook steps
  before_run: |                               # runs before every task (inline and uses:)
    echo "Starting task in $(repo_name)@$(revision)"
  after_run: |                                # runs after every task (even on failure)
    rm -rf /tmp/build-* /tmp/test-*

# ─── Shared storage customization (optional — defaults to 1Gi emptyDir) ─
storage:
  size: 5Gi
  storageClass: fast-ssd

# ─── Persistent cache across pipeline runs (via tekton-caches) ────────
#     Backend is shared; paths are auto-namespaced by pipeline name
cache:
  backend: oci://registry.example.com/cache   # base URI for all caches
  credentials: registry-auth                  # references secrets: entry
  paths:
    - path: /go/pkg/mod                       # → .../myapp-ci-cd/go-pkg-mod:{{hash}}
      key: ["**/go.sum"]
    - path: /root/.cache/go-build             # → .../myapp-ci-cd/go-build:{{hash}}
      key: ["**/go.sum", "**/go.mod"]

# ─── Parameters (types inferred from defaults) ───────────────────────
params:
  # Shorthand: bare value = default (type inferred as string)
  slack-webhook: "https://hooks.slack.com/services/..."
  environment: "staging"

  # Shorthand: bare list = default (type inferred as array)
  deploy-regions: ["us-east-1", "eu-west-1"]

  # Full form: when description or map default is needed
  build-config:
    description: "Build-time configuration"
    default:
      optimize: true
      debug: false

  # Full form: explicit type with no default
  extra-build-args:
    type: array

# ─── Secrets (mounted as volumes, no workspace boilerplate) ───────────
secrets:
  git-credentials: git-creds                # K8s Secret name
  docker-config: regcred                    # for image push auth
  registry-auth: regcred                    # for cache OCI backend
  sonar-token: sonarqube-secret             # for security scanning

# ─── Tasks ────────────────────────────────────────────────────────────
tasks:

  # ── External task from Artifact Hub (resolved by PaC) ──
  clone:
    uses: git-clone                         # latest from Artifact Hub
    params:
      url: $(repo_url)                      # PaC built-in variable
      revision: $(revision)                 # PaC built-in variable

  # ── External task pinned to version ──
  lint:
    uses: golangci-lint:0.3                 # pinned Artifact Hub version
    needs: [clone]
    params:
      package: ./...

  # ── External task from HTTP URL ──
  security-scan:
    uses: https://raw.githubusercontent.com/my-org/tekton-tasks/main/tasks/trivy-scan.yaml
    needs: [build-image]
    params:
      image: $(tasks.build-image.results.image-url)

  # ── External task from local repo path with manual approval gate ──
  deploy:
    uses: tasks/deploy-to-k8s.yaml          # relative to repo root
    needs: [security-scan]
    if: params.environment in ("staging", "production")
    approval:                                 # pipeline pauses here until approved
      approvers: [alice, bob, group:platform-team]
      required: 2
      description: "Approve $(params.environment) deployment of $(repo_name)"
      timeout: 120m
    params:
      image: $(tasks.build-image.results.image-url)
      regions: $(params.deploy-regions)

  # ── Single-step task with per-task before_run (overrides defaults:) ──
  unit-test:
    needs: [clone]
    image: golang:1.22
    before_run: |                               # overrides defaults: before_run
      cd $(workspace)/repo
      go mod download
    run: go test -v -race -coverprofile=coverage.out ./...
    timeout: 15m
    retries: 2
    # Sidecar: database for integration tests
    sidecars:
      - name: postgres
        image: postgres:16
        ports: [5432]
        env:
          POSTGRES_DB: testdb
          POSTGRES_USER: test
          POSTGRES_PASSWORD: test

  # ── Multi-step task with per-step image override ──
  build-image:
    needs: [lint, unit-test]
    image: golang:1.22                      # default image for steps
    env:
      CGO_ENABLED: "0"
      GOFLAGS: "-trimpath"
    steps:
      - name: compile
        run: |
          cd $(workspace)/repo
          go build -o app ./cmd/server
      - name: build-push
        image: gcr.io/kaniko-project/executor:latest    # per-step image override
        run: |
          /kaniko/executor \
            --dockerfile=$(workspace)/repo/Dockerfile \
            --destination=registry.example.com/myapp:$(revision) \
            --context=$(workspace)/repo
    results:
      image-url:
        description: "Full image reference that was pushed"
    # Tekton pass-through on task: merged into generated taskSpec
    tekton:
      stepTemplate:
        resources:
          requests:
            cpu: "1"
            memory: 2Gi
      podTemplate:
        nodeSelector:
          workload-type: ci-build
        securityContext:
          runAsNonRoot: true
          runAsUser: 1000

  # ── Task with result-based conditional ──
  tag-release:
    needs: [deploy]
    if: tasks.deploy.results.status == 'success'
    image: alpine/git:latest
    run: |
      cd $(workspace)/repo
      git tag v$(revision) && git push --tags
    timeout: 5m

# ─── Finally block (always runs, regardless of task success/failure) ──
finally:
  upload-coverage:
    image: curlimages/curl:latest
    run: |
      curl -X POST https://codecov.example.com/upload \
        -F "file=@$(workspace)/repo/coverage.out" \
        -F "commit=$(revision)" \
        -F "branch=$(source_branch)"

  notify-slack:
    image: curlimages/curl:latest
    steps:
      - name: send-result
        run: |
          curl -X POST $(params.slack-webhook) \
            -H 'Content-Type: application/json' \
            -d '{
              "text": "Pipeline for $(repo_owner)/$(repo_name) #$(pull_request_number) completed",
              "channel": "#ci-notifications"
            }'

# ─── Pipeline-level Tekton pass-through ──────────────────────────────
tekton:
  metadata:
    labels:
      team: backend
      cost-center: engineering
    annotations:
      quality-gate.example.com/required: "true"
```

**Features demonstrated:**
| # | Feature | Where in example |
|---|---|---|
| 1 | `on:` with `pull_request` (branches, paths, paths_ignore, labels) | `on: pull_request:` block |
| 2 | `on:` with `push` | `on: push:` block |
| 3 | `on:` with `comment` trigger | `on: comment:` |
| 4 | `concurrency: cancel-in-progress` | `concurrency:` block |
| 6 | `cleanup: max-keep-runs` | `cleanup:` block |
| 7 | `defaults:` with image, `before_run`, `after_run` | `defaults:` block (global image + hooks) |
| 8 | Per-task `before_run` override | `unit-test: before_run:` (overrides defaults) |
| 9 | `storage:` customization (size, storageClass) | `storage:` block |
| 10 | `cache:` with multiple backends and credentials | `cache:` block (Go mod + Go build caches via OCI) |
| 11 | Param shorthand (bare value = default) | `slack-webhook: "https://..."`, `environment: "staging"` |
| 12 | Param shorthand (bare list = array default) | `deploy-regions: ["us-east-1", "eu-west-1"]` |
| 13 | Param full form (description + map default) | `build-config:` |
| 14 | Param full form (explicit type, no default) | `extra-build-args:` |
| 16 | `secrets:` block with cache credentials | `secrets:` block (includes `registry-auth` for cache) |
| 17 | `uses:` — Artifact Hub (latest) | `clone: uses: git-clone` |
| 18 | `uses:` — Artifact Hub (pinned version) | `lint: uses: golangci-lint:0.3` |
| 19 | `uses:` — HTTP URL | `security-scan: uses: https://...` |
| 20 | `uses:` — local repo path | `deploy: uses: tasks/deploy-to-k8s.yaml` |
| 21 | PaC built-in variables | `$(repo_url)`, `$(revision)`, `$(source_branch)`, etc. |
| 22 | Single-step `run:` shorthand | `unit-test:` task |
| 23 | Multi-step `steps:` with per-step `image:` override | `build-image:` task (compile + build-push) |
| 24 | `needs:` dependencies (single and multiple) | `needs: [clone]`, `needs: [lint, unit-test]`, etc. |
| 25 | `if:` with `in` operator | `deploy: if: params.environment in (...)` |
| 26 | `approval:` manual approval gate with approvers, groups, threshold | `deploy: approval:` block |
| 27 | `if:` with `==` and result reference | `tag-release: if: tasks.deploy.results.status == 'success'` |
| 28 | `results:` declaration | `build-image: results: image-url:` |
| 29 | `retries:` and `timeout:` | `unit-test: timeout: 15m, retries: 2` |
| 30 | `sidecars:` with ports and env | `unit-test: sidecars: postgres` |
| 31 | `env:` on a task | `build-image: env: CGO_ENABLED, GOFLAGS` |
| 32 | `resources:` via `tekton:` stepTemplate | `build-image: tekton: stepTemplate: resources:` |
| 33 | `tekton:` pass-through on task (podTemplate, securityContext) | `build-image: tekton:` block |
| 34 | `finally:` block with multiple cleanup tasks | `finally: upload-coverage:, notify-slack:` |
| 35 | `$(workspace)` implicit shared filesystem | Throughout |
| 36 | Pipeline-level `tekton:` pass-through (labels, annotations) | Top-level `tekton:` block |

### 2.10 Tekton Concept Mapping

| DSL | Generated Output | Mapping |
|---|---|---|
| Top-level `name:` | `PipelineRun.metadata.name` | Direct |
| `on:` | PaC `on-event`, `on-target-branch`, `on-path-change`, etc. annotations | GHA-style to PaC annotations |
| `tasks:` entries | `pipelineSpec.tasks[].taskSpec` | Inline in PipelineRun |
| `uses:` | PaC `task` annotations + `taskRef` | Resolved by PaC resolver |
| `params:` | `pipelineSpec.params` | Compact `param: value` shorthand; types inferred from value |
| `results:` | `taskSpec.results` | Direct |
| `needs:` | `tasks[].runAfter` | Three-layer model: (1) declaration order — tasks without `needs:` depend on the previous task, (2) result refs — `$(tasks.X.results.Y)` adds implicit dep on X if not already transitively reachable, (3) explicit `needs:` overrides declaration order |
| `if:` | `tasks[].when` | Expression compiled to WhenExpressions |
| `finally:` | `pipelineSpec.finally` | Direct |
| `$(workspace)` | Auto-generated workspace + VolumeClaimTemplate | Fully implicit |
| `$(repo_url)`, `$(revision)`, etc. | PaC `{{ repo_url }}`, `{{ revision }}` template vars | Syntax translation |
| `secrets:` | Workspace of type `Secret` named `secret-<alias>` | Auto-binds when task workspace name matches alias; explicit `workspaces:` override available; cache credentials auto-wired |
| `cache:` | Injected `cache-fetch` / `cache-upload` StepAction steps | Auto-injected from tekton-caches |
| `approval:` | Injected `ApprovalTask` custom task before gated task | From manual-approval-gate |
| `defaults:` | Applied to all tasks — inline and `uses:` (image, before_run, after_run) | Compiler merges into each task |
| `before_run:` | Injected step before user steps (string or `{image, run}`) | Prepended to taskSpec steps; works with both inline and resolved tasks |
| `after_run:` | Injected step after user steps with `onError: continue` (string or `{image, run}`) | Runs even on failure; works with both inline and resolved tasks |
| `concurrency:` | PaC `cancel-in-progress` annotation | Direct |
| `cleanup:` | PaC `max-keep-runs` annotation | Direct |
| `tekton:` | Merged into generated CR | Raw pass-through |

### 2.11 Deferred

| Feature | Reason to Defer |
|---|---|
| Matrix / fan-out | Still in alpha in Tekton |
| Tekton Bundles | Advanced distribution concern |
| Custom Tasks | Extension mechanism, rare in basic pipelines |
| Step Actions | Relatively new Tekton feature |
| Resolver references | Complex resolution strategies |

---

## 3. Translation Architecture

### 3.1 Compilation Pipeline

```
DSL YAML file
  --> Parse (yaml.v3 with position tracking via yaml.Node)
  --> Structural Validation (JSON Schema)
  --> Build IR (internal Go structs)
  --> Infer param types from defaults
  --> Parse `if:` expressions into AST
  --> Semantic Validation (reference resolution, cycle detection)
  --> Lower to Tekton/PaC model:
      - Compile `on:` block to PaC annotations
      - Collect `uses:` references into PaC task annotations
      - Translate `$(repo_url)` etc. to PaC `{{ repo_url }}` template vars
      - Generate shared workspace declaration + bindings
      - Generate VolumeClaimTemplate for shared storage
      - Mount secrets as Tekton Secret workspaces
      - Compile `if:` AST to WhenExpressions
      - Inject cache-fetch / cache-upload StepAction steps into tasks
      - Add PaC step-action annotations for tekton-caches
      - Add concurrency/cleanup annotations
      - Merge `tekton:` pass-through blocks
  --> Serialize to PipelineRun YAML (PaC-compatible, with embedded pipelineSpec)
  --> If multiple events in `on:`, generate separate PipelineRun per event
  --> Generate single PipelineRun output
```

### 3.2 Output

When `on:` is present (PaC mode), the output is a PipelineRun with embedded `pipelineSpec` — PaC's preferred format. This is a single resource that goes into `.tekton/`:

```yaml
apiVersion: tekton.dev/v1
kind: PipelineRun
metadata:
  name: build-and-deploy
  labels:
    app.kubernetes.io/managed-by: tkn-dsl
  annotations:
    # Compiled from on: block
    pipelinesascode.tekton.dev/on-event: "[pull_request]"
    pipelinesascode.tekton.dev/on-target-branch: "[main, release-*]"
    pipelinesascode.tekton.dev/on-path-change: "[src/**, go.mod, go.sum]"
    # Compiled from uses: references
    pipelinesascode.tekton.dev/task: "[git-clone]"
    # Compiled from concurrency/cleanup
    pipelinesascode.tekton.dev/cancel-in-progress: "true"
    pipelinesascode.tekton.dev/max-keep-runs: "5"
spec:
  pipelineSpec:
    workspaces:
      - name: shared-workspace          # auto-generated
      - name: secret-git-credentials    # from secrets: block (alias "git-credentials" → workspace "secret-git-credentials")
    params:
      - name: repo-url
        type: string
        default: "https://github.com/example/repo"
      - name: image-tag
        type: string                    # inferred from bare value
        default: "latest"
    tasks:
      - name: clone
        workspaces:
          - name: shared-workspace
            workspace: shared-workspace
        taskSpec:
          workspaces:
            - name: shared-workspace
          results:
            - name: commit-sha
              description: "The git commit SHA"
          steps:
            - name: clone
              image: alpine/git:latest
              script: git clone {{ repo_url }} $(workspaces.shared-workspace.path)/repo
      - name: deploy
        runAfter: [build]
        when:                           # compiled from: if: params.image-tag not in ("dev", "test")
          - input: $(params.image-tag)
            operator: notin
            values: ["dev", "test"]
        # ...
  workspaces:
    - name: shared-workspace           # auto-provisioned
      volumeClaimTemplate:
        spec:
          accessModes: [ReadWriteOnce]
          resources:
            requests:
              storage: 1Gi
    - name: secret-git-credentials     # alias "git-credentials" → workspace "secret-git-credentials" backed by K8s Secret "git-creds"
      secret:
        secretName: git-creds
```

When `on:` is absent (standalone mode), the output is a separate Pipeline + PipelineRun pair for direct `kubectl apply` usage without PaC.

### 3.3 Key Translation Decisions

**Embedded pipelineSpec in PipelineRun**: When `on:` is present, everything is embedded in a single PipelineRun with `pipelineSpec` — this is what PaC expects. No separate Pipeline CR is generated.

**Inline taskSpec**: Tasks defined in the DSL become inline `taskSpec` within the pipeline. This:
- Produces fewer resources
- Avoids naming collisions
- Is simpler to reason about
- Matches the "single file = single pipeline" model

**External tasks via PaC resolver**: Tasks referenced via `uses:` generate a `taskRef` in the pipeline and a PaC `task` annotation. PaC handles the actual resolution — the CLI does NOT fetch remote tasks.

### 3.4 Compilation of `if:` Expressions

The compiler includes a small expression parser that handles the `if:` syntax:

```
expression  := operand operator value_list
operand     := "params." name | "tasks." name ".results." name
operator    := "==" | "!=" | "in" | "not in"
value_list  := quoted_string | "(" quoted_string ("," quoted_string)* ")"
```

This is deliberately minimal — it only supports what Tekton WhenExpressions can represent. The parser:
1. Extracts the operand and wraps it in `$(...)` for Tekton substitution
2. Maps `==` to `in` with a single value, `!=` to `notin` with a single value
3. Passes `in`/`not in` value lists directly
4. Reports parse errors with line/column numbers

### 3.5 Validation Strategy

**Layer 1 — Structural**: JSON Schema validates required fields, types, allowed values. Embedded in the Go binary and also published for IDE consumption.

**Layer 2 — Semantic**:
- `needs:` references point to existing tasks
- No circular dependencies (topological sort of task DAG)
- Result references (`$(tasks.X.results.Y)`) resolve to declared results
- Param references resolve
- `if:` expressions parse correctly and reference valid params/results
- `secrets:` entries are valid Kubernetes Secret name references
- `on:` event types are valid (`pull_request`, `push`, `comment`)
- `on:` branch patterns are valid globs
- `uses:` references are syntactically valid (name, name:version, URL, or relative path)

**Layer 3 — `tekton:` pass-through**: Not validated by the DSL compiler. Warnings emitted if `tekton:` fields conflict with compiler-generated fields.

**Layer 4 — Cluster (deferred)**: Verify secrets, SAs, storage classes exist.

### 3.6 Error Reporting

Use `yaml.Node` line/column tracking throughout the IR:

```
pipeline.tkn.yaml:12:3: circular dependency detected: build -> test -> build
pipeline.tkn.yaml:17:9: invalid expression: expected operator, got 'AND' (only ==, !=, in, not in are supported)
pipeline.tkn.yaml:8:3: param "repo-url" declared but never referenced
```

---

## 4. CLI Design

### 4.1 Command Structure

```
tkn-dsl generate <file>     # DSL -> Tekton YAML to stdout
tkn-dsl validate <file>     # Validate without generating
tkn-dsl schema              # Print JSON Schema for IDE integration
tkn-dsl init                # Scaffold an annotated example
tkn-dsl version             # Print version
```

**Deferred commands**: `apply`, `run`, `delete`, `status`, `logs`, `fmt`

### 4.2 Key Flags

```
--output / -o        # yaml (default) or json
--output-dir         # Write each CR to a separate file
--set key=value      # Override params at generation time
--repo owner/name    # Repository identity for cache namespacing
--strict             # Treat warnings as errors
--no-cache           # Skip cache step injection even if `cache:` is defined
--no-resolve         # Skip resolving external tasks (keep taskRef instead of inlining)
```

### 4.3 tkn Plugin Convention

Name the binary `tkn-dsl`. When on `$PATH`, the `tkn` CLI auto-discovers it as `tkn dsl generate ...`.

---

## 5. Go Architecture

### 5.1 Library-First Design

The compiler is structured as an **importable Go package** (`pkg/dsl`), not an internal CLI tool. This is critical for the phase 2 goal of native PaC integration.

**Phase 1 (CLI)**: The `tkn-dsl` binary is a thin CLI wrapper around the library.
**Phase 2 (PaC native)**: PaC imports the same library and calls it from `pkg/resolve/resolve.go` when it detects `.dsl.yaml` files.

```go
// pkg/dsl — the public API surface
// This is what PaC will import in phase 2

// Compile takes a DSL YAML byte slice and repo context, returns PipelineRun objects
func Compile(dslYAML []byte, opts CompileOptions) ([]*tektonv1.PipelineRun, error)

// Validate checks a DSL file without generating output
func Validate(dslYAML []byte) []ValidationError

// CompileOptions provides context that the DSL compiler needs
type CompileOptions struct {
    RepoOwner   string   // e.g., "acme" — from Repository CR in phase 2
    RepoName    string   // e.g., "backend-service" — from Repository CR in phase 2
    // Phase 1: derived from Git remote or --repo flag
    // Phase 2: provided by PaC from the Repository CR and event context
}
```

### 5.2 PaC Integration Point

In phase 2, PaC's file processing pipeline gains a DSL detection stage:

```
.tekton/ files fetched from Git repo
  --> For each file:
      --> If file ends in .dsl.yaml:
          --> dsl.Compile(fileBytes, CompileOptions{...})
          --> Returns []*tektonv1.PipelineRun (in-memory, never written to disk)
      --> Else:
          --> Existing ReadTektonTypes() path (unchanged)
  --> All PipelineRuns (from DSL + native YAML) feed into MatchPipelinerunByAnnotation()
  --> Existing matching, resolution, and execution pipeline (unchanged)
```

**Key architectural decisions for PaC integration:**

| Decision | Choice | Rationale |
|---|---|---|
| File extension | `.dsl.yaml` | Distinguishes from native PipelineRun YAML; still valid YAML for editor support |
| Detection point | Before `ReadTektonTypes()` in `pkg/resolve` | DSL files compile to the same `*tektonv1.PipelineRun` objects; rest of PaC is unaware |
| Output type | `*tektonv1.PipelineRun` (Tekton Go types) | Phase 2 must use Tekton types since PaC already depends on them. Phase 1 uses lightweight structs for YAML serialization, but the library API returns Tekton types when imported by PaC. |
| Annotation generation | Still needed | PaC's `MatchPipelinerunByAnnotation()` reads annotations to match events. The compiled PipelineRun must have `on-event`, `on-target-branch`, etc. annotations set. |
| Template variables | Pass-through as `{{ }}` | PaC's existing template expansion in `startPR()` handles `{{ repo_url }}`, `{{ revision }}`, etc. The DSL compiler just emits them. |
| Task resolution | PaC annotations | The compiled PipelineRun includes `pipelinesascode.tekton.dev/task` annotations. PaC's existing resolver handles the rest. |
| Repo context | `CompileOptions` struct | In phase 1, the CLI populates this from Git remote / `--repo` flag. In phase 2, PaC populates it from the Repository CR (`spec.url`) and Git event. |

**What PaC does NOT need to change** (beyond adding DSL detection):
- Event matching (`pkg/matcher`) — works on annotations, which the DSL compiler generates
- Task resolution (`pkg/resolve`) — works on annotations, which the DSL compiler generates
- Template expansion (`pkg/templates`) — works on `{{ }}` variables, which the DSL compiler emits
- Status reporting (`pkg/provider`) — unchanged
- GitOps commands (`pkg/opscomments`) — `/test`, `/retest`, `/cancel` work on PipelineRun names, unchanged
- ACL (`pkg/acl`) — unchanged
- Custom params (`pkg/customparams`) — unchanged

### 5.3 Dependency Strategy

**Phase 1 (CLI)**: Avoid importing `github.com/tektoncd/pipeline` (which pulls in Knative, K8s controller-runtime, etc.). Use lightweight custom structs that serialize to Tekton-compatible YAML. Validate correctness via golden file tests.

**Phase 2 (PaC import)**: When integrated into PaC, the compiler outputs `*tektonv1.PipelineRun` directly using Tekton's Go types (which PaC already depends on). The library provides both paths:

```go
// Phase 1: CLI serialization
func CompileToYAML(dslYAML []byte, opts CompileOptions) ([]byte, error)

// Phase 2: PaC in-memory compilation
func Compile(dslYAML []byte, opts CompileOptions) ([]*tektonv1.PipelineRun, error)
```

### 5.4 Key Libraries

| Purpose | Library |
|---|---|
| CLI framework | `github.com/spf13/cobra` |
| YAML parsing | `gopkg.in/yaml.v3` |
| JSON Schema validation | `github.com/santhosh-tekuri/jsonschema/v5` |
| DAG cycle detection | Hand-rolled topological sort (small enough) |
| Testing | `github.com/stretchr/testify` |
| Golden file tests | Manual or `github.com/sebdah/goldie/v2` |

### 5.5 Package Layout

```
cmd/tkn-dsl/main.go           # CLI entry point (thin wrapper, phase 1 only)
pkg/
  dsl/                         # PUBLIC API — this is what PaC imports in phase 2
    compile.go                 # Compile() and CompileToYAML() entry points
    compile_options.go         # CompileOptions struct (repo context)
    model.go                   # DSL Go structs (IR)
    parser.go                  # YAML -> IR with position tracking
    validate.go                # Validate() entry point
internal/
  cli/                         # Cobra commands (generate, validate, init, schema)
  validate/
    schema.go                  # JSON Schema validation
    semantic.go                # Reference/cycle checks
  expr/
    parser.go                  # `if:` expression parser
    parser_test.go             # Expression parsing tests
  compiler/
    compiler.go                # IR -> Tekton YAML structs
    pipeline.go                # Pipeline CR generation (standalone mode)
    pipelinerun.go             # PipelineRun CR generation
    pac.go                     # PaC annotation generation from `on:`, `uses:`, concurrency, cleanup
    cache.go                   # cache-fetch / cache-upload StepAction injection
    variables.go               # $(repo_url) -> {{ repo_url }} translation
  tekton/
    types.go                   # Lightweight Tekton-compatible structs (phase 1)
  schema/
    v1alpha1.json              # Embedded JSON Schema
testdata/
  examples/                    # Example DSL files
  golden/                      # Expected Tekton YAML output
```

Key difference from a typical CLI: the core logic lives in `pkg/dsl/` (public, importable) rather than `internal/` (private). The `internal/` packages handle implementation details that PaC does not need to import directly.

---

## 6. Risks and Mitigations

| Risk | Impact | Mitigation |
|---|---|---|
| Implicit workspace doesn't fit all access patterns | `ReadWriteOnce` PVC can't be mounted by parallel tasks on different nodes | Default to `ReadWriteOnce`; document that parallel tasks sharing storage must land on the same node. Allow `storage.accessMode` override. |
| `tekton:` pass-through causes confusing errors | Errors surface at apply time with K8s API messages, not DSL context | Emit a warning: "tekton: block is not validated by the DSL compiler — errors will surface at apply time" |
| `if:` expression parser scope creep | Users expect full expression language (`&&`, `||`, functions) | Document the 4 supported operators clearly. Error messages should say "only ==, !=, in, not in are supported" |
| Tekton API version drift | Generated YAML becomes invalid | Pin to Tekton `v1` stable API; `tekton:` pass-through covers new fields |
| Lightweight structs diverge from Tekton | Subtle YAML differences cause runtime failures | Comprehensive golden file tests; integration test with `kind` + Tekton in CI |
| Param type inference surprises | `param: 123` — is this string or number? | Tekton only supports string/array/object — YAML scalars always become `type: string`. Lists become `type: array`. |
| PaC annotation format changes | PaC updates annotation names or semantics | Pin to PaC stable annotations; `tekton:` pass-through covers new PaC annotations too |
| Multi-event `on:` generates multiple PipelineRuns | Users expect one file = one pipeline, but PaC needs separate PipelineRuns per event | Document clearly; use naming convention `<name>-pull-request`, `<name>-push` |
| Cache backend requires external setup | OCI registry, S3 bucket, or GCS bucket must exist before caching works | Document setup steps; validate backend URI scheme at compile time; clear error if no `backend:` specified |
| Two pipelines with same `name:` share cache | Name collision causes cross-pipeline cache corruption | Cache URI includes `{repo-owner}/{repo-name}/{pipeline-name}`, so cross-repo collisions are impossible. Within a repo, validate `name:` uniqueness across `.tekton/` directory. |
| Cache step injection increases task complexity | Generated YAML is harder to debug with injected cache steps | Add `# injected by tkn-dsl: cache-fetch` comments in generated YAML; `--no-cache` flag to skip injection |

---

## 7. Verification Plan

1. **Unit tests**: Parser, validator, and compiler each tested independently
2. **Golden file tests**: Each example DSL file has a corresponding expected Tekton YAML output; tests compare generated vs expected
3. **Manual verification**: Apply generated YAML to a `kind` cluster with Tekton installed, run the pipeline, verify it succeeds
4. **Schema verification**: Validate generated YAML against Tekton's own OpenAPI schema
5. **Example pipelines to build and test with**:
   - Simple single-task pipeline (clone a repo) — validates implicit workspace
   - Multi-task pipeline with dependencies (clone -> build -> test) — validates shared filesystem
   - Pipeline with params (all inference modes), `if:` conditionals, `finally:`, and `secrets:`
   - Pipeline using `tekton:` pass-through for podTemplate/nodeSelector
   - PaC pipeline with `on:` triggers, path filtering, `uses:` for remote tasks, and PaC template variables
   - PaC pipeline with CEL expression trigger and concurrency control
   - Pipeline with `cache:` (single and multiple entries, OCI and S3 backends) — validates step injection order and credential mapping
6. **PaC integration verification**: Generated PipelineRun YAML should be accepted by `tkn-pac resolve` for local validation before committing to `.tekton/`
7. **Library API verification**: `pkg/dsl.Compile()` returns valid `*tektonv1.PipelineRun` objects that pass PaC's `MatchPipelinerunByAnnotation()` matching — validates phase 2 integration path
