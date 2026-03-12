# tkn-dsl

A CLI that compiles a simplified YAML DSL into Tekton PipelineRun Custom Resources, compatible with [Pipelines-as-Code](https://pipelinesascode.com).

## Quickstart

```bash
# Install
go install github.com/ssadeghi/tkn-dsl/cmd/tkn-dsl@latest

# Scaffold a new pipeline
tkn-dsl init pipeline.dsl.yaml

# Compile to Tekton YAML
tkn-dsl generate pipeline.dsl.yaml > .tekton/pipeline.yaml

# Validate without generating
tkn-dsl validate pipeline.dsl.yaml
```

## Example

A simple "clone + build + test" pipeline in ~20 lines instead of ~80:

```yaml
name: ci

on:
  pull_request:
    branches: [main]
    paths: ["src/**", "pkg/**"]

params:
  image-tag: "latest"

tasks:
  clone:
    uses: git-clone
    params:
      url: $(repo_url)
      revision: $(revision)

  test:
    needs: [clone]
    image: golang:1.22
    run: |
      cd $(workspace)/repo
      go test -v ./...
```

## Parameters

Parameters use a compact `param-name: value` shorthand where the value is the default:

```yaml
params:
  # Shorthand: bare value = default (type inferred)
  branch: "main"
  image-tag: "latest"
  targets: ["staging", "production"]

  # Full form when description or explicit type is needed
  config:
    description: "Build configuration"
    default:
      optimize: true
  extra-args:
    type: array
```

**Inference rules:**
- Bare string → `type: string`, value is the default
- Bare list → `type: array`, list is the default
- Map with `description:`/`default:`/`type:` → full form
- No default, no type → `type: string` with no default

## Features

| Feature | DSL Syntax | What It Generates |
|---|---|---|
| Git triggers | `on: pull_request:` | PaC annotations |
| Parameters | `params: {branch: "main"}` | pipelineSpec.params with type inference |
| Single-step tasks | `run:` directly on task | Inline taskSpec |
| Multi-step tasks | `steps:` array | Inline taskSpec with steps |
| Dependencies | `needs: [task-a]` | `runAfter` |
| Conditionals | `if: params.env == 'prod'` | WhenExpressions |
| External tasks | `uses: git-clone:0.9` | PaC task annotations + taskRef |
| Shared filesystem | `$(workspace)` | Auto-provisioned VolumeClaimTemplate |
| Secrets | `secrets:` block | Secret workspaces |
| Persistent cache | `cache:` block | tekton-caches StepAction injection |
| Manual approval | `approval:` block | ApprovalTask custom task |
| Cleanup tasks | `finally:` block | pipelineSpec.finally |
| Hook scripts | `before_run:` / `after_run:` | Injected steps |
| Pass-through | `tekton:` block | Raw fields merged into generated CR |

## CLI Reference

```
tkn-dsl generate <file>        # Compile DSL -> Tekton YAML
tkn-dsl validate <file>        # Validate without generating
tkn-dsl schema                 # Print JSON Schema for IDE integration
tkn-dsl init [file]            # Scaffold an example DSL file
tkn-dsl version                # Print version
```

### Flags

```
-o, --output yaml|json      Output format (default: yaml)
--output-dir <dir>           Write each CR to a separate file
--repo owner/name            Repository identity for cache namespacing
--set key=value              Override param defaults
--no-cache                   Skip cache step injection
--strict                     Treat warnings as errors
```

## IDE Support

Use the `tkn-dsl schema` output with the [YAML Language Server](https://github.com/redhat-developer/yaml-language-server) for autocomplete and validation:

```yaml
# yaml-language-server: $schema=./schema.json
name: my-pipeline
# ...
```

## Library API

The compiler is available as an importable Go package for integration into other tools (e.g., Pipelines-as-Code):

```go
import "github.com/ssadeghi/tkn-dsl/pkg/tkndsl"

result, err := tkndsl.Compile(dslYAML, tkndsl.CompileOptions{
    RepoOwner: "acme",
    RepoName:  "backend",
})
// result.PipelineRuns contains []*tekton.PipelineRun
```

## Design

See [DESIGN.md](DESIGN.md) for the full design document covering syntax, translation architecture, and technical decisions.
