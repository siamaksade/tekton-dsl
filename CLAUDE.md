# CLAUDE.md

## Project

`tkn-dsl` is a Go CLI that compiles a simplified YAML DSL into Tekton PipelineRun CRs compatible with Pipelines-as-Code (PaC). See DESIGN.md for the full spec and docs/getting-started.md for usage.

## Build & Test

```bash
make build          # builds bin/tkn-dsl
make test           # runs unit tests (same as make test-unit)
make test-integration  # runs cluster integration tests (requires OpenShift)
make lint           # runs golangci-lint
```

Go is at `/opt/homebrew/bin/go` (v1.26.1). The binary is `bin/tkn-dsl`.

## Project Structure

```
cmd/tkn-dsl/main.go           Entry point
internal/cli/                  Cobra CLI commands (generate, validate, init, schema, version)
internal/compiler/             DSL -> Tekton compiler (compiler.go, pac.go, variables.go, cache.go, approval.go)
internal/expr/                 if: expression parser
internal/resolver/             Task resolver (Tekton Hub, cluster, local file)
internal/tekton/types.go       Lightweight Tekton-compatible structs (no k8s deps)
internal/validate/             JSON Schema + semantic validation
internal/schema/v1alpha1.json  Embedded JSON Schema for DSL
pkg/dsl/                       DSL types and parser (model.go, parser.go)
pkg/tkndsl/                    Public API: Compile(), CompileToYAML(), Validate()
samples/                       Sample DSL files (java-build.dsl.yaml)
testdata/examples/             Example DSL files (used by golden tests)
testdata/golden/               Expected compiler output (golden files)
test/integration/              Real cluster integration tests
docs/                          Getting started guide
spring-dsl-test/               Separate git repo for PaC integration testing
```

## Key Conventions

- **Import cycle**: `pkg/dsl` cannot import `internal/*`. Public API lives in `pkg/tkndsl`.
- **No k8s dependencies**: Use lightweight structs in `internal/tekton/types.go` that serialize to Tekton-compatible YAML.
- **Golden tests**: Example DSL files in `testdata/examples/` are compiled and compared against `testdata/golden/`. Run `make golden-update` to regenerate.
- **Single PipelineRun output**: The compiler always produces exactly one PipelineRun. Multiple event types (push + pull_request) are combined into a single `on-event` annotation.
- **Task dependencies**: Sequential by declaration order (each task depends on the previous). `needs:` overrides this. Result refs (`$(tasks.X.results.Y)`) add implicit deps only if not already transitively reachable.

## DSL Features

- **Hooks** (`before_run`/`after_run`): Work on both inline and `uses:` tasks. Support string shorthand or struct form (`{image, run}`). Applied via `defaults:` block or per-task.
- **Secrets**: `secrets: { alias: secret-name }`. Auto-binds when alias matches a task workspace name. Pipeline workspace is named `secret-<alias>`.
- **Workspaces**: Transparent — optional non-credential workspaces auto-bind to `shared-workspace`. Credential workspaces (dockerconfig, kubeconfig, ssh, etc.) skip unless matched by secrets alias or explicit mapping.
- **`$(workspace)`**: Translates to `$(workspaces.shared-workspace.path)` for inline tasks, or the correct workspace path for resolved tasks.
- **`tekton:` pass-through**: Works on both inline and `uses:` tasks. For resolved tasks, fields are deep-merged into the Raw spec.
- **Cache**: Injected as `cache-fetch`/`cache-upload` steps. `cache.credentials` auto-wires to the matching `secrets:` alias.

## Test Cluster

- OpenShift cluster with Tekton/PaC in `openshift-pipelines` namespace
- Test namespace: `dsl`
- `spring-dsl-test/` is a clone of https://github.com/siamaksade/spring-dsl-test with PaC integration
- Use `kubectl` directly (MCP server contexts may be unreachable)

## CLI Commands

```
tkn-dsl generate <file>    # Compile DSL -> single Tekton PipelineRun YAML
tkn-dsl validate <file>    # Validate without generating
tkn-dsl schema             # Print JSON Schema
tkn-dsl init [file]        # Scaffold example DSL file
tkn-dsl version            # Print version
```

Key flags: `--repo owner/name`, `--no-cache`, `--no-resolve`, `--set key=value`, `--output-dir`, `--strict`

## Workflow for Testing on Cluster

```bash
# 1. Build
make build

# 2. Generate PipelineRun
./bin/tkn-dsl generate samples/java-build.dsl.yaml > spring-dsl-test/.tekton/build.yaml

# 3. Commit and push to trigger PaC
git -C spring-dsl-test add .tekton/ && git -C spring-dsl-test commit -m "pipeline updated" && git -C spring-dsl-test push origin main

# 4. Monitor
kubectl get pipelinerun -n dsl --sort-by=.metadata.creationTimestamp | tail -3
```
