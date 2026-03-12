package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ssadeghi/tkn-dsl/internal/validate"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <file>",
		Short: "Validate a DSL YAML file without generating output",
		Args:  cobra.ExactArgs(1),
		RunE:  runValidate,
	}
}

func runValidate(cmd *cobra.Command, args []string) error {
	data, err := os.ReadFile(args[0])
	if err != nil {
		return fmt.Errorf("reading file: %w", err)
	}

	// Schema validation (structural).
	schemaErrs := validate.Schema(data)

	// Parse + semantic validation.
	p, err := dsl.Parse(data)
	if err != nil {
		return err
	}

	semanticErrs := validate.Semantic(p)

	allErrs := append(schemaErrs, semanticErrs...)
	if len(allErrs) > 0 {
		for _, e := range allErrs {
			fmt.Fprintf(os.Stderr, "error: %s\n", e.Message)
		}
		return fmt.Errorf("validation failed with %d error(s)", len(allErrs))
	}

	// Print warnings.
	warnings := validate.Warnings(p)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "%s\n", w.Message)
	}
	if strictMode && len(warnings) > 0 {
		return fmt.Errorf("strict mode: %d warning(s) treated as errors", len(warnings))
	}

	fmt.Println("OK")
	return nil
}
