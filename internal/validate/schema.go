package validate

import (
	"bytes"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v5"
	"gopkg.in/yaml.v3"

	"github.com/ssadeghi/tkn-dsl/internal/schema"
)

// Schema validates the raw YAML bytes against the embedded JSON Schema.
func Schema(data []byte) []ValidationError {
	// Parse YAML into a generic structure.
	var doc any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return []ValidationError{{Message: fmt.Sprintf("YAML parse error: %s", err)}}
	}

	// Convert to JSON-compatible types.
	doc = convertYAMLToJSON(doc)

	// Compile the schema.
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", bytes.NewReader(schema.V1Alpha1)); err != nil {
		return []ValidationError{{Message: fmt.Sprintf("internal: schema compile error: %s", err)}}
	}

	sch, err := compiler.Compile("schema.json")
	if err != nil {
		return []ValidationError{{Message: fmt.Sprintf("internal: schema compile error: %s", err)}}
	}

	if err := sch.Validate(doc); err != nil {
		ve, ok := err.(*jsonschema.ValidationError)
		if !ok {
			return []ValidationError{{Message: err.Error()}}
		}
		return flattenSchemaErrors(ve)
	}

	return nil
}

func flattenSchemaErrors(ve *jsonschema.ValidationError) []ValidationError {
	var errs []ValidationError
	if ve.Message != "" && len(ve.Causes) == 0 {
		loc := ve.InstanceLocation
		if loc == "" {
			loc = "/"
		}
		errs = append(errs, ValidationError{
			Message: fmt.Sprintf("schema: %s: %s", loc, ve.Message),
		})
	}
	for _, cause := range ve.Causes {
		errs = append(errs, flattenSchemaErrors(cause)...)
	}
	return errs
}

// convertYAMLToJSON converts YAML-specific types to JSON-compatible ones.
func convertYAMLToJSON(v any) any {
	switch val := v.(type) {
	case map[string]any:
		result := make(map[string]any, len(val))
		for k, v := range val {
			result[k] = convertYAMLToJSON(v)
		}
		return result
	case []any:
		result := make([]any, len(val))
		for i, v := range val {
			result[i] = convertYAMLToJSON(v)
		}
		return result
	case int:
		return float64(val)
	case int64:
		return float64(val)
	default:
		return val
	}
}
