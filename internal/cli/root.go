package cli

import (
	"github.com/spf13/cobra"
)

var (
	outputFormat string
	outputDir    string
	repoFlag     string
	strictMode   bool
)

// NewRootCmd creates the root cobra command for tkn-dsl.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "tkn-dsl",
		Short: "Compile Tekton DSL YAML to Tekton PipelineRun CRs",
		Long: `tkn-dsl is a CLI that compiles a simplified YAML DSL into
Tekton Pipeline Custom Resources compatible with Pipelines-as-Code.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVarP(&outputFormat, "output", "o", "yaml", "Output format: yaml or json")
	root.PersistentFlags().StringVar(&outputDir, "output-dir", "", "Write each CR to a separate file in this directory")
	root.PersistentFlags().StringVar(&repoFlag, "repo", "", "Repository identity (owner/name) for cache namespacing")
	root.PersistentFlags().BoolVar(&strictMode, "strict", false, "Treat warnings as errors")

	root.AddCommand(newGenerateCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newSchemaCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newVersionCmd(version))

	return root
}
