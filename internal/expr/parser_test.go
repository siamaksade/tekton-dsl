package expr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEqual(t *testing.T) {
	e, err := Parse("params.branch == 'main'")
	require.NoError(t, err)
	assert.Equal(t, "$(params.branch)", e.Operand)
	assert.Equal(t, OpIn, e.Operator)
	assert.Equal(t, []string{"main"}, e.Values)
}

func TestParseNotEqual(t *testing.T) {
	e, err := Parse("params.tag != 'dev'")
	require.NoError(t, err)
	assert.Equal(t, "$(params.tag)", e.Operand)
	assert.Equal(t, OpNotIn, e.Operator)
	assert.Equal(t, []string{"dev"}, e.Values)
}

func TestParseIn(t *testing.T) {
	e, err := Parse(`params.environment in ("staging", "production")`)
	require.NoError(t, err)
	assert.Equal(t, "$(params.environment)", e.Operand)
	assert.Equal(t, OpIn, e.Operator)
	assert.Equal(t, []string{"staging", "production"}, e.Values)
}

func TestParseNotIn(t *testing.T) {
	e, err := Parse(`params.tag not in ("dev", "test")`)
	require.NoError(t, err)
	assert.Equal(t, "$(params.tag)", e.Operand)
	assert.Equal(t, OpNotIn, e.Operator)
	assert.Equal(t, []string{"dev", "test"}, e.Values)
}

func TestParseResultRef(t *testing.T) {
	e, err := Parse("tasks.check.results.status == 'ready'")
	require.NoError(t, err)
	assert.Equal(t, "$(tasks.check.results.status)", e.Operand)
	assert.Equal(t, OpIn, e.Operator)
	assert.Equal(t, []string{"ready"}, e.Values)
}

func TestParseDoubleQuotes(t *testing.T) {
	e, err := Parse(`params.branch == "main"`)
	require.NoError(t, err)
	assert.Equal(t, []string{"main"}, e.Values)
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		input string
		match string
	}{
		{"", "expected operand"},
		{"foo.bar == 'x'", "must start with 'params.' or 'tasks.'"},
		{"params.x && params.y", "unsupported operator"},
		{"params.x || params.y", "unsupported operator"},
		{"params.x == ", "expected quoted string"},
		{"params.x in ()", "empty value list"},
		{"params.x in ('a',)", "expected quoted string"},
		{"params.x == 'a' extra", "unexpected trailing content"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			_, err := Parse(tt.input)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.match)
		})
	}
}
