package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const initTemplate = `# yaml-language-server: $schema=https://github.com/ssadeghi/tkn-dsl/schema/v1alpha1
# tkn-dsl pipeline definition
# Run: tkn-dsl generate pipeline.dsl.yaml > .tekton/pipeline.yaml

name: my-pipeline

# Git event triggers (compiles to Pipelines-as-Code annotations)
# on:
#   pull_request:
#     branches: [main]
#     paths: ["src/**", "pkg/**"]
#   push:
#     branches: [main]

# Pipeline parameters (types inferred from defaults)
# params:
#   repo-url: "Git repository URL"
#   image-tag:
#     description: "Docker image tag"
#     default: "latest"

# Kubernetes secrets (mounted automatically)
# secrets:
#   git-credentials: git-creds

tasks:
  hello:
    image: redhat/ubi9-minimal
    run: |
      echo "Hello from tkn-dsl!"
      echo "Workspace: $(workspace)"
      ls -la $(workspace)

  # Multi-step task example
  # build:
  #   needs: [hello]
  #   image: golang:1.22
  #   steps:
  #     - name: compile
  #       run: go build -o app .
  #     - name: test
  #       run: go test ./...

  # External task (resolved by Pipelines-as-Code)
  # clone:
  #   uses: git-clone
  #   params:
  #     url: $(repo_url)
  #     revision: $(revision)

# Cleanup tasks (always run)
# finally:
#   notify:
#     image: curlimages/curl:latest
#     run: |
#       curl -X POST https://hooks.slack.com/... \
#         -d '{"text": "Pipeline completed"}'
`

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init [file]",
		Short: "Scaffold an annotated example DSL file",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filename := "pipeline.dsl.yaml"
			if len(args) > 0 {
				filename = args[0]
			}

			if _, err := os.Stat(filename); err == nil {
				return fmt.Errorf("file %q already exists", filename)
			}

			if err := os.WriteFile(filename, []byte(initTemplate), 0644); err != nil {
				return err
			}

			fmt.Printf("Created %s\n", filename)
			return nil
		},
	}
}
