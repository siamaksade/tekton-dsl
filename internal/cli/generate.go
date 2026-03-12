package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/ssadeghi/tkn-dsl/internal/compiler"
	"github.com/ssadeghi/tkn-dsl/internal/resolver"
	"github.com/ssadeghi/tkn-dsl/internal/validate"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

var (
	noCache   bool
	noResolve bool
	setFlags  []string
)

func newGenerateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "generate <file>",
		Aliases: []string{"dsl-to-tekton"},
		Short:   "Compile DSL YAML to Tekton PipelineRun YAML",
		Args:    cobra.ExactArgs(1),
		RunE:    runGenerate,
	}
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "Skip cache step injection")
	cmd.Flags().BoolVar(&noResolve, "no-resolve", false, "Skip resolving external tasks (keep taskRef instead of inlining)")
	cmd.Flags().StringArrayVar(&setFlags, "set", nil, "Override param defaults (key=value)")
	return cmd
}

func runGenerate(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	p, err := dsl.Parse(data)
	if err != nil {
		return err
	}

	// Apply --set overrides.
	for _, s := range setFlags {
		parts := strings.SplitN(s, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid --set value %q: expected key=value", s)
		}
		if p.Params == nil {
			p.Params = make(map[string]*dsl.Param)
		}
		param, ok := p.Params[parts[0]]
		if !ok {
			param = &dsl.Param{}
			p.Params[parts[0]] = param
		}
		param.Default = parts[1]
	}

	errs := validate.Semantic(p)
	if len(errs) > 0 {
		for _, e := range errs {
			fmt.Fprintf(os.Stderr, "error: %s\n", e.Message)
		}
		return fmt.Errorf("validation failed with %d error(s)", len(errs))
	}

	// Print warnings (non-fatal unless --strict).
	warnings := validate.Warnings(p)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "%s\n", w.Message)
	}
	if strictMode && len(warnings) > 0 {
		return fmt.Errorf("strict mode: %d warning(s) treated as errors", len(warnings))
	}

	opts := compiler.Options{NoCache: noCache}
	if !noResolve {
		baseDir := filepath.Dir(args[0])
		opts.TaskResolver = resolver.NewCompositeResolver(baseDir)
	}
	if repoFlag != "" {
		parts := strings.SplitN(repoFlag, "/", 2)
		if len(parts) == 2 {
			opts.RepoOwner = parts[0]
			opts.RepoName = parts[1]
		}
	}

	result, err := compiler.Compile(p, opts)
	if err != nil {
		return err
	}

	// Write to --output-dir if specified.
	if outputDir != "" {
		return writeToDir(result)
	}

	// Write to stdout.
	if err := outputResource(result.PipelineRuns[0]); err != nil {
		return err
	}

	return nil
}

func writeToDir(result *compiler.CompileResult) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return err
	}

	pr := result.PipelineRuns[0]
	filename := pr.Metadata.Name + ".yaml"
	if outputFormat == "json" {
		filename = pr.Metadata.Name + ".json"
	}
	path := filepath.Join(outputDir, filename)
	if err := writeResourceToFile(path, pr); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", path)
	return nil
}

func writeResourceToFile(path string, v any) error {
	var data []byte
	var err error

	switch outputFormat {
	case "json":
		data, err = json.MarshalIndent(v, "", "  ")
	default:
		data, err = yaml.Marshal(v)
	}
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func outputResource(v any) error {
	switch outputFormat {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	default:
		out, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		fmt.Print(string(out))
		return nil
	}
}
