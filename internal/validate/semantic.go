package validate

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ssadeghi/tkn-dsl/internal/expr"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

// ValidationError is a semantic validation error with optional location.
type ValidationError struct {
	Message string
}

func (e ValidationError) Error() string {
	return e.Message
}

// Semantic validates the parsed Pipeline IR for semantic correctness.
func Semantic(p *dsl.Pipeline) []ValidationError {
	var errs []ValidationError

	if p.Name == "" {
		errs = append(errs, ValidationError{Message: "pipeline 'name' is required"})
	}

	if len(p.Tasks) == 0 {
		errs = append(errs, ValidationError{Message: "pipeline must have at least one task"})
	}

	allTasks := collectTaskNames(p)

	errs = append(errs, validateNeeds(p.Tasks, allTasks)...)
	errs = append(errs, validateNeeds(p.Finally, allTasks)...)
	errs = append(errs, validateCycles(p.Tasks)...)
	errs = append(errs, validateIfExpressions(p.Tasks)...)
	errs = append(errs, validateIfExpressions(p.Finally)...)
	errs = append(errs, validateTasks(p.Tasks)...)
	errs = append(errs, validateTasks(p.Finally)...)
	errs = append(errs, validateOnBlock(p.On)...)
	errs = append(errs, validateCacheBackend(p.Cache)...)
	errs = append(errs, validateSecretNames(p.Secrets)...)
	errs = append(errs, validateUsesRefs(p.Tasks)...)
	errs = append(errs, validateUsesRefs(p.Finally)...)
	errs = append(errs, validateParamRefs(p)...)
	errs = append(errs, validateResultRefs(p)...)

	return errs
}

// Warnings returns non-fatal warnings (unused params, etc.).
func Warnings(p *dsl.Pipeline) []ValidationError {
	return warnUnusedParams(p)
}

func collectTaskNames(p *dsl.Pipeline) map[string]bool {
	names := make(map[string]bool)
	for name := range p.Tasks {
		names[name] = true
	}
	for name := range p.Finally {
		names[name] = true
	}
	return names
}

func validateNeeds(tasks map[string]*dsl.Task, allTasks map[string]bool) []ValidationError {
	var errs []ValidationError
	for name, task := range tasks {
		if task == nil {
			continue
		}
		for _, dep := range task.Needs {
			if !allTasks[dep] {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("task %q: needs reference %q does not exist", name, dep),
				})
			}
		}
	}
	return errs
}

func validateCycles(tasks map[string]*dsl.Task) []ValidationError {
	// Build adjacency list.
	graph := make(map[string][]string)
	for name, task := range tasks {
		if task == nil {
			continue
		}
		graph[name] = task.Needs
	}

	// Topological sort using DFS with cycle detection.
	const (
		white = 0 // unvisited
		gray  = 1 // in progress
		black = 2 // done
	)
	colors := make(map[string]int)
	var path []string

	var visit func(node string) []ValidationError
	visit = func(node string) []ValidationError {
		colors[node] = gray
		path = append(path, node)
		for _, dep := range graph[node] {
			if colors[dep] == gray {
				// Found cycle — find it in path.
				cycleStart := -1
				for i, n := range path {
					if n == dep {
						cycleStart = i
						break
					}
				}
				cycle := append(path[cycleStart:], dep)
				return []ValidationError{{
					Message: fmt.Sprintf("circular dependency detected: %s", strings.Join(cycle, " -> ")),
				}}
			}
			if colors[dep] == white {
				if errs := visit(dep); len(errs) > 0 {
					return errs
				}
			}
		}
		path = path[:len(path)-1]
		colors[node] = black
		return nil
	}

	for name := range tasks {
		if colors[name] == white {
			if errs := visit(name); len(errs) > 0 {
				return errs
			}
		}
	}
	return nil
}

func validateIfExpressions(tasks map[string]*dsl.Task) []ValidationError {
	var errs []ValidationError
	for name, task := range tasks {
		if task == nil || task.If == "" {
			continue
		}
		if _, err := expr.Parse(task.If); err != nil {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("task %q: invalid if expression: %s", name, err),
			})
		}
	}
	return errs
}

func validateTasks(tasks map[string]*dsl.Task) []ValidationError {
	var errs []ValidationError
	for name, task := range tasks {
		if task == nil {
			continue
		}
		// A task must have exactly one of: uses, run, steps.
		count := 0
		if task.Uses != "" {
			count++
		}
		if task.Run != "" {
			count++
		}
		if len(task.Steps) > 0 {
			count++
		}
		if count == 0 {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("task %q: must have one of 'uses', 'run', or 'steps'", name),
			})
		}
		if count > 1 {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("task %q: cannot have more than one of 'uses', 'run', 'steps'", name),
			})
		}

		// Inline tasks (run/steps) require an image (unless defaults provide one).
		if task.Uses == "" && task.Image == "" && len(task.Steps) == 0 && task.Run != "" {
			// Image might come from defaults — compiler will check. Skip here.
		}
	}
	return errs
}

var validEventTypes = map[string]bool{
	"pull_request": true,
	"push":         true,
}

func validateOnBlock(on *dsl.OnTrigger) []ValidationError {
	if on == nil {
		return nil
	}
	return nil
}

// k8sNamePattern validates Kubernetes resource names (RFC 1123 DNS label).
var k8sNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`)

func validateSecretNames(secrets map[string]string) []ValidationError {
	var errs []ValidationError
	for alias, name := range secrets {
		if !k8sNamePattern.MatchString(name) {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("secrets: %q references invalid Kubernetes Secret name %q", alias, name),
			})
		}
	}
	return errs
}

func validateUsesRefs(tasks map[string]*dsl.Task) []ValidationError {
	var errs []ValidationError
	for name, task := range tasks {
		if task == nil || task.Uses == "" {
			continue
		}
		ref := task.Uses
		// Valid forms: "name", "name:version", "https://...", "path/to/file.yaml"
		if strings.Contains(ref, " ") {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("task %q: invalid uses reference %q (must not contain spaces)", name, ref),
			})
		}
	}
	return errs
}

// paramRefPattern matches $(params.X) references in scripts.
var paramRefPattern = regexp.MustCompile(`\$\(params\.([a-zA-Z0-9_-]+)\)`)

// bareRefPattern matches $(X) where X is a simple identifier — used to detect
// PaC-style param references like $(image_url) which are equivalent to $(params.image_url).
var bareRefPattern = regexp.MustCompile(`\$\(([a-zA-Z_][a-zA-Z0-9_-]*)\)`)

// resultRefPattern matches $(tasks.X.results.Y) references.
var resultRefPattern = regexp.MustCompile(`\$\(tasks\.([a-zA-Z0-9_-]+)\.results\.([a-zA-Z0-9_-]+)\)`)

func validateParamRefs(p *dsl.Pipeline) []ValidationError {
	var errs []ValidationError
	declaredParams := make(map[string]bool)
	for name := range p.Params {
		declaredParams[name] = true
	}

	checkRefs := func(taskName, text string) {
		for _, match := range paramRefPattern.FindAllStringSubmatch(text, -1) {
			paramName := match[1]
			if !declaredParams[paramName] {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("task %q: references undeclared param %q", taskName, paramName),
				})
			}
		}
	}

	scanTask := func(name string, t *dsl.Task) {
		if t == nil {
			return
		}
		checkRefs(name, t.Run)
		for _, s := range t.Steps {
			checkRefs(name, s.Run)
		}
		checkRefs(name, t.BeforeRun.Script())
		checkRefs(name, t.AfterRun.Script())
	}

	for name, task := range p.Tasks {
		scanTask(name, task)
	}
	for name, task := range p.Finally {
		scanTask(name, task)
	}
	return errs
}

func validateResultRefs(p *dsl.Pipeline) []ValidationError {
	// Collect declared results per task.
	declaredResults := make(map[string]map[string]bool) // task -> result -> true
	for name, task := range p.Tasks {
		if task == nil || len(task.Results) == 0 {
			continue
		}
		declaredResults[name] = make(map[string]bool)
		for rname := range task.Results {
			declaredResults[name][rname] = true
		}
	}

	var errs []ValidationError

	checkRefs := func(taskName, text string) {
		for _, match := range resultRefPattern.FindAllStringSubmatch(text, -1) {
			refTask := match[1]
			refResult := match[2]
			taskResults, taskExists := declaredResults[refTask]
			if !taskExists {
				if _, exists := p.Tasks[refTask]; !exists {
					errs = append(errs, ValidationError{
						Message: fmt.Sprintf("task %q: references results of unknown task %q", taskName, refTask),
					})
				} else {
					// Task exists but has no declared results.
					errs = append(errs, ValidationError{
						Message: fmt.Sprintf("task %q: references undeclared result %q on task %q (task declares no results)", taskName, refResult, refTask),
					})
				}
				continue
			}
			if !taskResults[refResult] {
				errs = append(errs, ValidationError{
					Message: fmt.Sprintf("task %q: references undeclared result %q on task %q", taskName, refResult, refTask),
				})
			}
		}
	}

	scanTask := func(name string, t *dsl.Task) {
		if t == nil {
			return
		}
		checkRefs(name, t.Run)
		checkRefs(name, t.If)
		for _, s := range t.Steps {
			checkRefs(name, s.Run)
		}
	}

	for name, task := range p.Tasks {
		scanTask(name, task)
	}
	for name, task := range p.Finally {
		scanTask(name, task)
	}
	return errs
}

func warnUnusedParams(p *dsl.Pipeline) []ValidationError {
	if len(p.Params) == 0 {
		return nil
	}

	used := make(map[string]bool)

	scanText := func(text string) {
		// Match $(params.X) references.
		for _, match := range paramRefPattern.FindAllStringSubmatch(text, -1) {
			used[match[1]] = true
		}
		// Match bare $(X) references (PaC-style param syntax).
		for _, match := range bareRefPattern.FindAllStringSubmatch(text, -1) {
			name := match[1]
			if _, isParam := p.Params[name]; isParam {
				used[name] = true
			}
		}
	}

	// ifParamPattern matches bare params.X in if: expressions (no $() wrapping).
	ifParamPattern := regexp.MustCompile(`params\.([a-zA-Z0-9_-]+)`)

	scanTask := func(t *dsl.Task) {
		if t == nil {
			return
		}
		scanText(t.Run)
		scanText(t.BeforeRun.Script())
		scanText(t.AfterRun.Script())
		scanText(t.If)
		// if: expressions use bare params.X (without $()).
		for _, match := range ifParamPattern.FindAllStringSubmatch(t.If, -1) {
			used[match[1]] = true
		}
		for _, s := range t.Steps {
			scanText(s.Run)
		}
		// Params passed to uses: tasks.
		for _, v := range t.Params {
			if s, ok := v.(string); ok {
				scanText(s)
			}
		}
	}

	for _, task := range p.Tasks {
		scanTask(task)
	}
	for _, task := range p.Finally {
		scanTask(task)
	}

	var errs []ValidationError
	for name := range p.Params {
		if !used[name] {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("warning: param %q declared but never referenced", name),
			})
		}
	}
	return errs
}

var cacheBackendPattern = regexp.MustCompile(`^(oci|s3|gs)://`)

func validateCacheBackend(cache *dsl.Cache) []ValidationError {
	if cache == nil {
		return nil
	}
	var errs []ValidationError
	image := cache.EffectiveImage()
	if image == "" {
		errs = append(errs, ValidationError{Message: "cache: 'image' is required"})
	} else if !cacheBackendPattern.MatchString(image) {
		errs = append(errs, ValidationError{
			Message: fmt.Sprintf("cache: image %q must use oci://, s3://, or gs:// scheme", image),
		})
	}

	paths := cache.EffectiveCachePaths()
	for i, cp := range paths {
		if cp.Path == "" {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("cache.paths[%d]: 'path' is required", i),
			})
		}
		if len(cp.Key) == 0 {
			errs = append(errs, ValidationError{
				Message: fmt.Sprintf("cache.paths[%d]: 'key' is required", i),
			})
		}
	}

	return errs
}
