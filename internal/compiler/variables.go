package compiler

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ssadeghi/tkn-dsl/internal/tekton"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

// pacTemplateVars are PaC dynamic template variables that need translation
// from DSL $(var) syntax to PaC {{ var }} syntax.
// Full list: https://pipelinesascode.com/docs/guide/authoringprs/
var pacTemplateVars = []string{
	// Core Git context
	"repo_url",
	"revision",
	"source_branch",
	"target_branch",
	"repo_name",
	"repo_owner",
	"source_url",

	// Event context
	"event",
	"event_type",
	"pull_request_number",
	"pull_request_labels",
	"sender",
	"trigger_comment",
	"git_tag",

	// Runtime context
	"target_namespace",
	"git_auth_secret",
}

// bareVarPattern matches $(name) where name is a simple identifier (not dotted
// like params.X or tasks.X.results.Y).
var bareVarPattern = regexp.MustCompile(`\$\(([a-zA-Z_][a-zA-Z0-9_-]*)\)`)

// translatePaCVariables translates only PaC built-in variable references
// from DSL $(var) syntax to PaC {{ var }} syntax throughout the PipelineRun.
// User-defined pipeline params should use $(params.X) directly (Tekton syntax)
// and are passed through unchanged.
func translatePaCVariables(pr *tekton.PipelineRun, p *dsl.Pipeline) {
	if pr.Spec.PipelineSpec == nil {
		return
	}

	pacVars := make(map[string]bool)
	for _, v := range pacTemplateVars {
		pacVars[v] = true
	}

	translate := func(s string) string {
		return bareVarPattern.ReplaceAllStringFunc(s, func(match string) string {
			name := match[2 : len(match)-1]
			if pacVars[name] {
				return fmt.Sprintf("{{ %s }}", name)
			}
			return match
		})
	}

	// Translate in param defaults.
	for i := range pr.Spec.PipelineSpec.Params {
		if s, ok := pr.Spec.PipelineSpec.Params[i].Default.(string); ok {
			pr.Spec.PipelineSpec.Params[i].Default = translate(s)
		}
	}

	translateTasks(pr.Spec.PipelineSpec.Tasks, translate)
	translateTasks(pr.Spec.PipelineSpec.Finally, translate)
}

func translateTasks(tasks []tekton.PipelineTask, translate func(string) string) {
	for i := range tasks {
		// Translate in task params.
		for j := range tasks[i].Params {
			if s, ok := tasks[i].Params[j].Value.(string); ok {
				tasks[i].Params[j].Value = translate(s)
			}
		}

		if tasks[i].TaskSpec == nil {
			continue
		}

		// Translate in step scripts and env vars.
		for j := range tasks[i].TaskSpec.Steps {
			tasks[i].TaskSpec.Steps[j].Script = translate(tasks[i].TaskSpec.Steps[j].Script)
			for k := range tasks[i].TaskSpec.Steps[j].Env {
				tasks[i].TaskSpec.Steps[j].Env[k].Value = translate(tasks[i].TaskSpec.Steps[j].Env[k].Value)
			}
		}
	}
}

// translatePaCVarsInString is kept for backward compat in non-PaC contexts.
func translatePaCVarsInString(s string) string {
	for _, v := range pacTemplateVars {
		s = strings.ReplaceAll(s, "$("+v+")", "{{ "+v+" }}")
	}
	return s
}
