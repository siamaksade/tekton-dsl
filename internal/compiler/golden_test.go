package compiler

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestGoldenFiles(t *testing.T) {
	examples, err := filepath.Glob("../../testdata/examples/*.dsl.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, examples, "no example files found")

	for _, exPath := range examples {
		base := filepath.Base(exPath)
		name := strings.TrimSuffix(base, ".dsl.yaml")

		t.Run(name, func(t *testing.T) {
			goldenPath := filepath.Join("../../testdata/golden", name+".yaml")

			input, err := os.ReadFile(exPath)
			require.NoError(t, err)

			p, err := dsl.Parse(input)
			require.NoError(t, err)

			result, err := Compile(p, Options{})
			require.NoError(t, err)
			require.NotEmpty(t, result.PipelineRuns)

			// Serialize output.
			var parts []string
			for _, pr := range result.PipelineRuns {
				b, err := yaml.Marshal(pr)
				require.NoError(t, err)
				parts = append(parts, string(b))
			}
			actual := strings.Join(parts, "---\n")

			// Read golden file.
			golden, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Logf("Golden file %s not found; writing it", goldenPath)
				err := os.WriteFile(goldenPath, []byte(actual), 0644)
				require.NoError(t, err)
				return
			}

			// Compare as parsed YAML structures (order-insensitive).
			var expectedObj, actualObj any
			require.NoError(t, yaml.Unmarshal(golden, &expectedObj))
			require.NoError(t, yaml.Unmarshal([]byte(actual), &actualObj))

			assert.Equal(t, expectedObj, actualObj, "output doesn't match golden file %s", goldenPath)
		})
	}
}
