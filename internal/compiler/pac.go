package compiler

import (
	"fmt"
	"strings"

	"github.com/ssadeghi/tkn-dsl/internal/tekton"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

// compilePaC generates a single PipelineRun with PaC annotations from the `on:` block.
func compilePaC(p *dsl.Pipeline, opts Options) (*CompileResult, error) {
	result := &CompileResult{}
	on := p.On

	pr, err := buildPipelineRun(p, opts)
	if err != nil {
		return nil, err
	}

	if pr.Metadata.Annotations == nil {
		pr.Metadata.Annotations = make(map[string]string)
	}

	// Event types — combined into a single on-event annotation.
	var events []string
	if on.PullRequest != nil {
		events = append(events, "pull_request")
		addEventFilterAnnotations(pr.Metadata.Annotations, on.PullRequest)
	}
	if on.Push != nil {
		events = append(events, "push")
		addEventFilterAnnotations(pr.Metadata.Annotations, on.Push)
	}
	if len(events) > 0 {
		pr.Metadata.Annotations["pipelinesascode.tekton.dev/on-event"] = formatStringList(events)
	}
	if on.Comment != "" {
		pr.Metadata.Annotations["pipelinesascode.tekton.dev/on-comment"] = on.Comment
	}
	if on.CEL != "" {
		pr.Metadata.Annotations["pipelinesascode.tekton.dev/on-cel-expression"] = on.CEL
	}

	// Only add task annotations when tasks are not inlined (no resolver).
	if opts.TaskResolver == nil {
		addUsesAnnotations(pr, p)
	}
	addPaCAnnotations(pr, p)
	translatePaCVariables(pr, p)

	result.PipelineRuns = append(result.PipelineRuns, pr)
	return result, nil
}

func addPaCAnnotations(pr *tekton.PipelineRun, p *dsl.Pipeline) {
	if p.Concurrency != nil && p.Concurrency.CancelInProgress {
		pr.Metadata.Annotations["pipelinesascode.tekton.dev/cancel-in-progress"] = "true"
	}
	if p.Cleanup != nil && p.Cleanup.MaxKeepRuns > 0 {
		pr.Metadata.Annotations["pipelinesascode.tekton.dev/max-keep-runs"] = fmt.Sprintf("%d", p.Cleanup.MaxKeepRuns)
	}
}

func addEventFilterAnnotations(ann map[string]string, ef *dsl.EventFilter) { //nolint:unparam
	if len(ef.Branches) > 0 {
		ann["pipelinesascode.tekton.dev/on-target-branch"] = formatStringList(ef.Branches)
	}
	if len(ef.Paths) > 0 {
		ann["pipelinesascode.tekton.dev/on-path-change"] = formatStringList(ef.Paths)
	}
	if len(ef.PathsIgnore) > 0 {
		ann["pipelinesascode.tekton.dev/on-path-change-ignore"] = formatStringList(ef.PathsIgnore)
	}
	if len(ef.Labels) > 0 {
		ann["pipelinesascode.tekton.dev/on-label"] = formatStringList(ef.Labels)
	}
}

func addUsesAnnotations(pr *tekton.PipelineRun, p *dsl.Pipeline) {
	var refs []string
	collectUses(p.Tasks, &refs)
	collectUses(p.Finally, &refs)

	if len(refs) == 0 {
		return
	}

	if pr.Metadata.Annotations == nil {
		pr.Metadata.Annotations = make(map[string]string)
	}

	for i, ref := range refs {
		key := "pipelinesascode.tekton.dev/task"
		if i > 0 {
			key = fmt.Sprintf("pipelinesascode.tekton.dev/task-%d", i)
		}
		pr.Metadata.Annotations[key] = ref
	}
}

func collectUses(tasks map[string]*dsl.Task, refs *[]string) {
	names := sortedTaskNames(tasks)
	for _, name := range names {
		task := tasks[name]
		if task != nil && task.Uses != "" {
			*refs = append(*refs, task.Uses)
		}
	}
}

func formatStringList(items []string) string {
	return "[" + strings.Join(items, ", ") + "]"
}
