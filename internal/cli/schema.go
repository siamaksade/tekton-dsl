package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ssadeghi/tkn-dsl/internal/schema"
)

func newSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print JSON Schema for DSL validation and IDE integration",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println(string(schema.V1Alpha1))
		},
	}
}
