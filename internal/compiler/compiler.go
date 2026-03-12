package compiler

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/ssadeghi/tkn-dsl/internal/expr"
	"github.com/ssadeghi/tkn-dsl/internal/resolver"
	"github.com/ssadeghi/tkn-dsl/internal/tekton"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

const sharedWorkspaceName = "shared-workspace"

// CompileResult holds the output of a compilation.
type CompileResult struct {
	PipelineRuns []*tekton.PipelineRun
}

// Compile translates a parsed DSL Pipeline into one or more Tekton PipelineRun CRs.
func Compile(p *dsl.Pipeline, opts Options) (*CompileResult, error) {
	if p.On != nil {
		return compilePaC(p, opts)
	}
	result, err := compileStandalone(p, opts)
	if err != nil {
		return nil, err
	}
	return &CompileResult{PipelineRuns: result}, nil
}

// Options holds compilation options.
type Options struct {
	RepoOwner    string
	RepoName     string
	NoCache      bool
	TaskResolver resolver.TaskResolver
}

// compileStandalone generates a PipelineRun for standalone (non-PaC) mode.
func compileStandalone(p *dsl.Pipeline, opts Options) ([]*tekton.PipelineRun, error) {
	pr, err := buildPipelineRun(p, opts)
	if err != nil {
		return nil, err
	}
	return []*tekton.PipelineRun{pr}, nil
}

// buildPipelineRun creates a single PipelineRun from the DSL Pipeline.
func buildPipelineRun(p *dsl.Pipeline, opts Options) (*tekton.PipelineRun, error) {
	pr := tekton.NewPipelineRun(p.Name)

	// Compile params.
	pr.Spec.PipelineSpec.Params = compileParams(p.Params)

	// Declare shared workspace.
	pr.Spec.PipelineSpec.Workspaces = []tekton.WorkspaceDecl{
		{Name: sharedWorkspaceName},
	}

	// Compile tasks.
	tasks, err := compileTasks(p.Tasks, p, false, opts)
	if err != nil {
		return nil, err
	}

	// Inject approval gates.
	tasks = injectApprovalGates(tasks, p.Tasks)

	pr.Spec.PipelineSpec.Tasks = tasks

	// Compile finally tasks.
	if len(p.Finally) > 0 {
		finallyTasks, err := compileTasks(p.Finally, p, true, opts)
		if err != nil {
			return nil, err
		}
		pr.Spec.PipelineSpec.Finally = finallyTasks
	}

	// Inject cache steps.
	if !opts.NoCache {
		injectCacheSteps(pr, p, opts)
	}

	// Workspace binding with VolumeClaimTemplate.
	pr.Spec.Workspaces = []tekton.WorkspaceBinding{
		buildSharedWorkspaceBinding(p.Storage),
	}

	// Secret workspace bindings.
	for secretAlias, secretName := range p.Secrets {
		wsName := "secret-" + secretAlias
		pr.Spec.PipelineSpec.Workspaces = append(pr.Spec.PipelineSpec.Workspaces,
			tekton.WorkspaceDecl{Name: wsName})
		pr.Spec.Workspaces = append(pr.Spec.Workspaces, tekton.WorkspaceBinding{
			Name:   wsName,
			Secret: &tekton.SecretWorkspace{SecretName: secretName},
		})
	}

	// Cache credential workspace — if cache.credentials references a secret
	// not already declared in secrets:, create a workspace for it.
	if p.Cache != nil && p.Cache.Credentials != "" {
		credAlias := resolveCacheCredentialAlias(p.Cache.Credentials, p.Secrets)
		wsName := "secret-" + credAlias
		// Check if workspace already exists (from secrets: block).
		found := false
		for _, ws := range pr.Spec.PipelineSpec.Workspaces {
			if ws.Name == wsName {
				found = true
				break
			}
		}
		if !found {
			pr.Spec.PipelineSpec.Workspaces = append(pr.Spec.PipelineSpec.Workspaces,
				tekton.WorkspaceDecl{Name: wsName})
			pr.Spec.Workspaces = append(pr.Spec.Workspaces, tekton.WorkspaceBinding{
				Name:   wsName,
				Secret: &tekton.SecretWorkspace{SecretName: p.Cache.Credentials},
			})
		}
	}

	// Merge pipeline-level tekton pass-through.
	mergePipelineTekton(pr, p.Tekton)

	// Merge pipelineRun-level tekton pass-through.
	mergePipelineRunTekton(pr, p.Tekton)

	return pr, nil
}

func compileParams(params map[string]*dsl.Param) []tekton.ParamSpec {
	if len(params) == 0 {
		return nil
	}

	// Sort for deterministic output.
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)

	var specs []tekton.ParamSpec
	for _, name := range names {
		p := params[name]
		spec := tekton.ParamSpec{
			Name:        name,
			Type:        inferParamType(p),
			Description: p.Description,
		}
		if p.Default != nil {
			spec.Default = p.Default
		}
		specs = append(specs, spec)
	}
	return specs
}

func inferParamType(p *dsl.Param) string {
	if p.Type != "" {
		return p.Type
	}
	if p.Default == nil {
		return "string"
	}
	switch p.Default.(type) {
	case []any, []string:
		return "array"
	case map[string]any, map[any]any:
		return "object"
	default:
		return "string"
	}
}

func compileTasks(tasks map[string]*dsl.Task, p *dsl.Pipeline, isFinally bool, opts Options) ([]tekton.PipelineTask, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	// Use declaration order (preserves YAML ordering).
	var names []string
	if isFinally {
		names = p.FinallyOrder
	} else {
		names = p.TaskOrder
	}
	// Fallback to sorted if order not available (e.g., programmatic construction).
	if len(names) == 0 {
		names = sortedTaskNames(tasks)
	}

	// Resolve dependencies using the three-layer model:
	// 1. Declaration order (sequential by default)
	// 2. Result-ref auto-detection
	// 3. Explicit needs: (overrides declaration order)
	resolvedDeps := resolveDependencies(names, tasks)

	var pts []tekton.PipelineTask
	for _, name := range names {
		task := tasks[name]
		if task == nil {
			continue
		}
		pt, err := compileTask(name, task, p, opts)
		if err != nil {
			return nil, fmt.Errorf("task %q: %w", name, err)
		}
		// Override RunAfter with resolved dependencies.
		pt.RunAfter = resolvedDeps[name]
		pts = append(pts, pt)
	}
	return pts, nil
}

// resultRefPattern matches $(tasks.X.results.Y) references in task scripts/expressions.
var resultRefPattern = regexp.MustCompile(`\$\(tasks\.([a-zA-Z0-9_-]+)\.results\.[a-zA-Z0-9_-]+\)`)

// resolveDependencies implements the three-layer dependency model:
//  1. Declaration order: tasks without explicit needs: depend on the previous task
//  2. Result refs: $(tasks.X.results.Y) adds an implicit dependency on task X
//  3. Explicit needs: overrides declaration-order dependency entirely
func resolveDependencies(orderedNames []string, tasks map[string]*dsl.Task) map[string][]string {
	deps := make(map[string][]string)

	for i, name := range orderedNames {
		task := tasks[name]
		if task == nil {
			continue
		}

		var taskDeps []string

		if len(task.Needs) > 0 {
			// Layer 3: Explicit needs — use exactly what the user specified.
			taskDeps = append(taskDeps, task.Needs...)
		} else if i > 0 {
			// Layer 1: Declaration order — depend on the previous task.
			taskDeps = append(taskDeps, orderedNames[i-1])
		}

		// Layer 2: Result refs — add only if not already reachable through existing deps.
		resultDeps := scanResultRefs(task)
		for _, rd := range resultDeps {
			if rd != name && !contains(taskDeps, rd) && !isReachable(rd, taskDeps, deps) {
				taskDeps = append(taskDeps, rd)
			}
		}

		if len(taskDeps) > 0 {
			deps[name] = taskDeps
		}
	}

	return deps
}

// scanResultRefs extracts task names from $(tasks.X.results.Y) references
// in a task's run scripts, steps, if expressions, and params.
func scanResultRefs(task *dsl.Task) []string {
	seen := make(map[string]bool)
	var refs []string

	scan := func(text string) {
		for _, match := range resultRefPattern.FindAllStringSubmatch(text, -1) {
			taskName := match[1]
			if !seen[taskName] {
				seen[taskName] = true
				refs = append(refs, taskName)
			}
		}
	}

	scan(task.Run)
	scan(task.If)
	scan(task.BeforeRun.Script())
	scan(task.AfterRun.Script())
	for _, s := range task.Steps {
		scan(s.Run)
	}
	for _, v := range task.Params {
		if s, ok := v.(string); ok {
			scan(s)
		}
	}

	return refs
}

// isReachable checks if target is transitively reachable from any of the given
// roots through the dependency graph. This avoids adding redundant runAfter entries.
func isReachable(target string, roots []string, deps map[string][]string) bool {
	visited := make(map[string]bool)
	var walk func(node string) bool
	walk = func(node string) bool {
		if node == target {
			return true
		}
		if visited[node] {
			return false
		}
		visited[node] = true
		for _, dep := range deps[node] {
			if walk(dep) {
				return true
			}
		}
		return false
	}
	for _, root := range roots {
		if walk(root) {
			return true
		}
	}
	return false
}

// isCredentialWorkspace returns true if the workspace name suggests it holds
// credentials or secrets that should not be auto-bound to the shared workspace.
func isCredentialWorkspace(name string) bool {
	n := strings.ToLower(strings.ReplaceAll(name, "_", "-"))
	for _, pattern := range []string{"dockerconfig", "docker-config", "kubeconfig", "kube-config", "ssh", "basic-auth", "ssl-ca", "registry-auth"} {
		if strings.Contains(n, pattern) {
			return true
		}
	}
	return false
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func compileTask(name string, task *dsl.Task, p *dsl.Pipeline, opts Options) (tekton.PipelineTask, error) {
	pt := tekton.PipelineTask{
		Name: name,
	}

	// Dependencies are resolved by resolveDependencies() and set by compileTasks().
	// RunAfter is intentionally not set here.

	// When expressions (conditionals).
	if task.If != "" {
		when, err := compileWhen(task.If)
		if err != nil {
			return pt, err
		}
		pt.When = when
	}

	// Workspace binding.
	pt.Workspaces = []tekton.WorkspaceRef{
		{Name: sharedWorkspaceName, Workspace: sharedWorkspaceName},
	}

	// Timeout and retries.
	if task.Timeout != "" {
		pt.Timeout = task.Timeout
	}
	if task.Retries > 0 {
		pt.Retries = task.Retries
	}

	if task.Uses != "" {
		if opts.TaskResolver != nil {
			// Resolve and inline the external task.
			spec, err := opts.TaskResolver.Resolve(task.Uses)
			if err != nil {
				return pt, fmt.Errorf("resolving task %q: %w", task.Uses, err)
			}
			pt.TaskSpec = &tekton.TaskSpec{Raw: spec}
			pt.Params = compileTaskParamsWithWorkspace(task.Params, resolveRawWorkspacePath(spec, task))

			// Map the task's workspaces to pipeline workspaces.
			// Priority order for each task workspace:
			//   1. Explicit mapping via task.Workspaces (e.g., workspaces: {dockerconfig: secret-quay})
			//   2. Auto-bind if workspace name matches a secrets: alias (e.g., workspace "dockerconfig" + secrets: {dockerconfig: ...} → secret-dockerconfig)
			//   3. Skip credential-like optional workspaces with no match
			//   4. Bind everything else to shared-workspace
			taskWorkspaces := resolver.WorkspacesFromSpec(spec)
			if len(taskWorkspaces) > 0 {
				pt.Workspaces = nil
				for _, ws := range taskWorkspaces {
					if mapping, ok := task.Workspaces[ws.Name]; ok {
						// Explicit mapping from DSL.
						pt.Workspaces = append(pt.Workspaces, tekton.WorkspaceRef{
							Name:      ws.Name,
							Workspace: mapping,
						})
					} else if _, ok := p.Secrets[ws.Name]; ok {
						// Task workspace name matches a secrets: alias → auto-bind.
						pt.Workspaces = append(pt.Workspaces, tekton.WorkspaceRef{
							Name:      ws.Name,
							Workspace: "secret-" + ws.Name,
						})
					} else if ws.Optional && isCredentialWorkspace(ws.Name) {
						// Credential-like optional workspaces with no match → skip.
					} else {
						// Required and non-credential optional workspaces → shared-workspace.
						pt.Workspaces = append(pt.Workspaces, tekton.WorkspaceRef{
							Name:      ws.Name,
							Workspace: sharedWorkspaceName,
						})
					}
				}
			}
			injectHooksIntoRawTask(spec, task, p)
			mergeRawTaskTekton(spec, task.Tekton)
			return pt, nil
		}

		// Fallback: external task reference (no resolver available).
		refName := task.Uses
		if idx := strings.Index(refName, ":"); idx > 0 && !strings.Contains(refName, "/") {
			refName = refName[:idx]
		}
		pt.TaskRef = &tekton.TaskRef{Name: refName}
		pt.Params = compileTaskParams(task.Params)
		return pt, nil
	}

	// Inline task.
	image := resolveImage(task, p)

	var steps []tekton.Step

	// before_run hook.
	beforeHook := resolveHook(task.BeforeRun, p, true)
	if beforeHook.script != "" {
		hookImage := beforeHook.image
		if hookImage == "" {
			hookImage = image
		}
		steps = append(steps, tekton.Step{
			Name:   "before-run",
			Image:  hookImage,
			Script: wrapScript(beforeHook.script),
		})
	}

	// User-defined steps.
	if task.Run != "" {
		// Single-step task.
		script := translateWorkspaceVars(task.Run)
		steps = append(steps, tekton.Step{
			Name:   name,
			Image:  image,
			Script: wrapScript(script),
			Env:    compileEnv(task.Env),
		})
	} else if len(task.Steps) > 0 {
		// Multi-step task.
		for _, s := range task.Steps {
			stepImage := s.Image
			if stepImage == "" {
				stepImage = image
			}
			script := translateWorkspaceVars(s.Run)
			step := tekton.Step{
				Name:   s.Name,
				Image:  stepImage,
				Script: wrapScript(script),
				Env:    compileEnv(s.Env),
			}
			if s.Resources != nil {
				step.Resources = compileStepResources(s.Resources)
			}
			if s.Tekton != nil {
				step.TektonRaw = s.Tekton
			}
			steps = append(steps, step)
		}
	}

	// after_run hook.
	afterHook := resolveHook(task.AfterRun, p, false)
	if afterHook.script != "" {
		hookImage := afterHook.image
		if hookImage == "" {
			hookImage = image
		}
		steps = append(steps, tekton.Step{
			Name:    "after-run",
			Image:   hookImage,
			Script:  wrapScript(afterHook.script),
			OnError: "continue",
		})
	}

	taskSpec := &tekton.TaskSpec{
		Workspaces: []tekton.WorkspaceDecl{{Name: sharedWorkspaceName}},
		Steps:      steps,
	}

	// Results.
	if len(task.Results) > 0 {
		for rname, r := range task.Results {
			taskSpec.Results = append(taskSpec.Results, tekton.TaskResult{
				Name:        rname,
				Description: r.Description,
			})
		}
	}

	// Sidecars.
	if len(task.Sidecars) > 0 {
		for _, sc := range task.Sidecars {
			sidecar := tekton.Sidecar{
				Name:  sc.Name,
				Image: sc.Image,
				Env:   compileEnv(sc.Env),
			}
			for _, port := range sc.Ports {
				sidecar.Ports = append(sidecar.Ports, tekton.Port{ContainerPort: port})
			}
			taskSpec.Sidecars = append(taskSpec.Sidecars, sidecar)
		}
	}

	// Merge task-level tekton pass-through into the taskSpec.
	mergeTaskTekton(taskSpec, task.Tekton)

	pt.TaskSpec = taskSpec
	return pt, nil
}

// conflictingTaskSpecFields are fields the compiler generates that could conflict
// with tekton: pass-through.
var conflictingTaskSpecFields = map[string]bool{
	"workspaces": true,
	"steps":      true,
	"results":    true,
	"sidecars":   true,
}

func mergeTaskTekton(ts *tekton.TaskSpec, t map[string]any) {
	if t == nil || ts == nil {
		return
	}
	// Warn on conflicting fields (to stderr).
	for key := range t {
		if conflictingTaskSpecFields[key] {
			fmt.Fprintf(os.Stderr, "warning: tekton pass-through field %q conflicts with compiler-generated field (pass-through value takes precedence)\n", key)
		}
	}
	ts.TektonRaw = t
}

// mergeRawTaskTekton merges tekton: pass-through fields into a resolved (Raw) task spec.
// Fields are merged directly into the Raw map, with deep merge for maps like stepTemplate.
func mergeRawTaskTekton(spec map[string]any, t map[string]any) {
	if t == nil || spec == nil {
		return
	}
	for key, value := range t {
		existing, hasExisting := spec[key]
		if hasExisting {
			// Deep merge maps (e.g., stepTemplate).
			if existingMap, ok := existing.(map[string]any); ok {
				if newMap, ok := value.(map[string]any); ok {
					for k, v := range newMap {
						existingMap[k] = v
					}
					continue
				}
			}
		}
		spec[key] = value
	}
}

func compileWhen(ifExpr string) ([]tekton.WhenExpression, error) {
	parsed, err := expr.Parse(ifExpr)
	if err != nil {
		return nil, err
	}
	return []tekton.WhenExpression{{
		Input:    parsed.Operand,
		Operator: string(parsed.Operator),
		Values:   parsed.Values,
	}}, nil
}

// compileTaskParamsWithWorkspace compiles params and translates $(workspace) references
// to the resolved task's workspace path.
func compileTaskParamsWithWorkspace(params map[string]any, wsPath string) []tekton.TaskParam {
	tps := compileTaskParams(params)
	if wsPath == "" {
		return tps
	}
	for i, tp := range tps {
		if s, ok := tp.Value.(string); ok {
			tps[i].Value = strings.ReplaceAll(s, "$(workspace)", wsPath)
		}
	}
	return tps
}

func compileTaskParams(params map[string]any) []tekton.TaskParam {
	if len(params) == 0 {
		return nil
	}
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)

	var tps []tekton.TaskParam
	for _, name := range names {
		tps = append(tps, tekton.TaskParam{
			Name:  name,
			Value: params[name],
		})
	}
	return tps
}

func compileEnv(env map[string]string) []tekton.EnvVar {
	if len(env) == 0 {
		return nil
	}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)

	var vars []tekton.EnvVar
	for _, name := range names {
		vars = append(vars, tekton.EnvVar{Name: name, Value: env[name]})
	}
	return vars
}

func compileStepResources(r *dsl.Resources) *tekton.StepResources {
	if r == nil {
		return nil
	}
	sr := &tekton.StepResources{}
	reqs := make(map[string]string)
	if r.CPU != "" {
		reqs["cpu"] = r.CPU
	}
	if r.Memory != "" {
		reqs["memory"] = r.Memory
	}
	if len(reqs) > 0 {
		sr.Requests = reqs
		sr.Limits = reqs // set limits = requests
	}
	return sr
}

func resolveImage(task *dsl.Task, p *dsl.Pipeline) string {
	if task.Image != "" {
		return task.Image
	}
	if p.Defaults != nil && p.Defaults.Image != "" {
		return p.Defaults.Image
	}
	return ""
}

// resolvedHook holds the resolved script and image for a hook step.
type resolvedHook struct {
	script string
	image  string
}

func resolveHook(taskHook *dsl.Hook, p *dsl.Pipeline, isBefore bool) resolvedHook {
	if taskHook != nil && taskHook.Run != "" {
		return resolvedHook{
			script: translateWorkspaceVars(taskHook.Run),
			image:  taskHook.Image,
		}
	}
	if p.Defaults != nil {
		var defHook *dsl.Hook
		if isBefore {
			defHook = p.Defaults.BeforeRun
		} else {
			defHook = p.Defaults.AfterRun
		}
		if defHook != nil && defHook.Run != "" {
			return resolvedHook{
				script: translateWorkspaceVars(defHook.Run),
				image:  defHook.Image,
			}
		}
	}
	return resolvedHook{}
}

// injectHooksIntoRawTask injects before_run/after_run steps into a resolved (Raw) task spec.
// Hook image priority: hook.image > task.image > defaults.image > first step's image.
func injectHooksIntoRawTask(spec map[string]any, task *dsl.Task, p *dsl.Pipeline) {
	beforeHook := resolveHook(task.BeforeRun, p, true)
	afterHook := resolveHook(task.AfterRun, p, false)
	if beforeHook.script == "" && afterHook.script == "" {
		return
	}

	// Translate $(workspace) to the correct workspace path for this resolved task.
	wsPath := resolveRawWorkspacePath(spec, task)
	if wsPath != "" {
		beforeHook.script = strings.ReplaceAll(beforeHook.script, "$(workspaces.shared-workspace.path)", wsPath)
		afterHook.script = strings.ReplaceAll(afterHook.script, "$(workspaces.shared-workspace.path)", wsPath)
	}

	// Determine fallback image: task.image > defaults.image > first step's image.
	fallbackImage := task.Image
	if fallbackImage == "" && p.Defaults != nil && p.Defaults.Image != "" {
		fallbackImage = p.Defaults.Image
	}
	if fallbackImage == "" {
		if steps, ok := spec["steps"].([]any); ok && len(steps) > 0 {
			if step, ok := steps[0].(map[string]any); ok {
				if img, ok := step["image"].(string); ok {
					fallbackImage = img
				}
			}
		}
	}
	if fallbackImage == "" {
		fallbackImage = "registry.access.redhat.com/ubi9-minimal"
	}

	existingSteps, _ := spec["steps"].([]any)
	newSteps := make([]any, 0, len(existingSteps)+2)

	if beforeHook.script != "" {
		img := beforeHook.image
		if img == "" {
			img = fallbackImage
		}
		newSteps = append(newSteps, map[string]any{
			"name":   "before-run",
			"image":  img,
			"script": wrapScript(beforeHook.script),
		})
	}

	newSteps = append(newSteps, existingSteps...)

	if afterHook.script != "" {
		img := afterHook.image
		if img == "" {
			img = fallbackImage
		}
		newSteps = append(newSteps, map[string]any{
			"name":    "after-run",
			"image":   img,
			"script":  wrapScript(afterHook.script),
			"onError": "continue",
		})
	}

	spec["steps"] = newSteps
}

// resolveRawWorkspacePath returns the Tekton workspace path expression for the first
// mapped workspace in a resolved task spec. This is used to translate $(workspace)
// references in hook scripts to the correct workspace name for the resolved task.
func resolveRawWorkspacePath(spec map[string]any, task *dsl.Task) string {
	// If the task has explicit workspace mappings, use the first one.
	for wsName := range task.Workspaces {
		return fmt.Sprintf("$(workspaces.%s.path)", wsName)
	}
	// Fall back to the first workspace declared in the resolved task spec.
	if wsList, ok := spec["workspaces"].([]any); ok {
		for _, ws := range wsList {
			if wsMap, ok := ws.(map[string]any); ok {
				if name, ok := wsMap["name"].(string); ok {
					return fmt.Sprintf("$(workspaces.%s.path)", name)
				}
			}
		}
	}
	return ""
}

// translateWorkspaceVars replaces $(workspace) with the Tekton workspace path expression.
func translateWorkspaceVars(s string) string {
	return strings.ReplaceAll(s, "$(workspace)", "$(workspaces.shared-workspace.path)")
}

func wrapScript(script string) string {
	if strings.HasPrefix(script, "#!/") {
		return script
	}
	return "#!/bin/sh\n" + script
}

func buildSharedWorkspaceBinding(storage *dsl.Storage) tekton.WorkspaceBinding {
	size := "1Gi"
	if storage != nil && storage.Size != "" {
		size = storage.Size
	}

	binding := tekton.WorkspaceBinding{
		Name: sharedWorkspaceName,
		VolumeClaimTemplate: &tekton.VolumeClaimTemplate{
			Spec: tekton.VCTSpec{
				AccessModes: []string{"ReadWriteOnce"},
				Resources: tekton.VCTResources{
					Requests: map[string]string{"storage": size},
				},
			},
		},
	}

	if storage != nil && storage.StorageClass != "" {
		binding.VolumeClaimTemplate.Spec.StorageClassName = &storage.StorageClass
	}

	return binding
}

func mergePipelineTekton(pr *tekton.PipelineRun, t map[string]any) {
	if t == nil {
		return
	}
	if md, ok := t["metadata"].(map[string]any); ok {
		if labels, ok := md["labels"].(map[string]any); ok {
			if pr.Metadata.Labels == nil {
				pr.Metadata.Labels = make(map[string]string)
			}
			for k, v := range labels {
				pr.Metadata.Labels[k] = fmt.Sprintf("%v", v)
			}
		}
		if anns, ok := md["annotations"].(map[string]any); ok {
			if pr.Metadata.Annotations == nil {
				pr.Metadata.Annotations = make(map[string]string)
			}
			for k, v := range anns {
				pr.Metadata.Annotations[k] = fmt.Sprintf("%v", v)
			}
		}
	}
}

func mergePipelineRunTekton(pr *tekton.PipelineRun, t map[string]any) {
	if t == nil {
		return
	}
	prRaw, ok := t["pipelineRun"].(map[string]any)
	if !ok {
		return
	}
	pr.Spec.TektonRaw = prRaw
}

func sortedTaskNames(tasks map[string]*dsl.Task) []string {
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
