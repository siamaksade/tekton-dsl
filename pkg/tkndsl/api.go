// Package tkndsl provides the public API for compiling Tekton DSL files.
// This is the entry point for PaC integration in phase 2.
package tkndsl

import (
	"github.com/ssadeghi/tkn-dsl/internal/compiler"
	"github.com/ssadeghi/tkn-dsl/internal/resolver"
	"github.com/ssadeghi/tkn-dsl/internal/validate"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
	"gopkg.in/yaml.v3"
)

// TaskResolver resolves external task references into inline task specs.
type TaskResolver = resolver.TaskResolver

// CompileOptions provides context for DSL compilation.
type CompileOptions struct {
	RepoOwner    string
	RepoName     string
	NoCache      bool
	TaskResolver TaskResolver
}

// CompileResult holds the compiled output.
type CompileResult = compiler.CompileResult

// ValidationError represents a validation error.
type ValidationError = validate.ValidationError

// Compile parses and compiles DSL YAML into a Tekton PipelineRun.
func Compile(dslYAML []byte, opts CompileOptions) (*CompileResult, error) {
	p, err := dsl.Parse(dslYAML)
	if err != nil {
		return nil, err
	}

	errs := validate.Semantic(p)
	if len(errs) > 0 {
		return nil, errs[0]
	}

	return compiler.Compile(p, compiler.Options{
		RepoOwner:    opts.RepoOwner,
		RepoName:     opts.RepoName,
		NoCache:      opts.NoCache,
		TaskResolver: opts.TaskResolver,
	})
}

// CompileToYAML parses and compiles DSL YAML, returning serialized Tekton YAML.
func CompileToYAML(dslYAML []byte, opts CompileOptions) ([]byte, error) {
	result, err := Compile(dslYAML, opts)
	if err != nil {
		return nil, err
	}

	return yaml.Marshal(result.PipelineRuns[0])
}

// Validate checks a DSL YAML file for errors without generating output.
func Validate(dslYAML []byte) []ValidationError {
	p, err := dsl.Parse(dslYAML)
	if err != nil {
		return []ValidationError{{Message: err.Error()}}
	}
	return validate.Semantic(p)
}
