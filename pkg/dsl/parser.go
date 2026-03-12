package dsl

import (
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// pacBracePattern matches {{ var_name }} and {{var_name}} — the PaC template
// syntax that is invalid unquoted YAML. We normalize it to $(var_name) before
// parsing so users can write either syntax.
var pacBracePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_.]*)\s*\}\}`)

// Parse parses DSL YAML bytes into a Pipeline IR.
// Accepts both $(var) and {{ var }} syntax for PaC template variables.
func Parse(data []byte) (*Pipeline, error) {
	// Pre-process: normalize {{ var }} to $(var) before YAML parsing.
	// {{ }} is never valid unquoted YAML, so this can't break valid files.
	data = pacBracePattern.ReplaceAll(data, []byte("$($1)"))

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("YAML parse error: %w", err)
	}

	if root.Kind == 0 {
		return nil, fmt.Errorf("empty YAML document")
	}

	var p Pipeline
	if err := root.Decode(&p); err != nil {
		return nil, fmt.Errorf("decode error: %w", err)
	}

	// Extract declaration order from YAML nodes (maps don't preserve order).
	p.TaskOrder = extractKeyOrder(&root, "tasks")
	p.FinallyOrder = extractKeyOrder(&root, "finally")

	// Set task names from map keys.
	setTaskNames(p.Tasks)
	setTaskNames(p.Finally)

	return &p, nil
}

// extractKeyOrder walks the YAML node tree to find a top-level mapping key
// and returns its child keys in declaration order.
func extractKeyOrder(root *yaml.Node, key string) []string {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}

	for i := 0; i < len(doc.Content)-1; i += 2 {
		if doc.Content[i].Value == key {
			mapNode := doc.Content[i+1]
			if mapNode.Kind != yaml.MappingNode {
				return nil
			}
			var order []string
			for j := 0; j < len(mapNode.Content)-1; j += 2 {
				order = append(order, mapNode.Content[j].Value)
			}
			return order
		}
	}
	return nil
}

// setTaskNames sets the Name field on each Task from its map key.
func setTaskNames(tasks map[string]*Task) {
	for name, task := range tasks {
		if task != nil {
			task.Name = name
		}
	}
}
