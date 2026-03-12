package compiler

import (
	"fmt"
	"strings"

	"github.com/ssadeghi/tkn-dsl/internal/tekton"
	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
)

const cacheImage = "registry.redhat.io/openshift-pipelines/pipelines-cache-rhel9@sha256:ce548401247c025aa5dee6ed641ede514a8ce19cbfb6fbe41edeabcdb1d493f2"

const cacheFetchScript = `#!/bin/sh
PATTERN_FLAGS=""
echo "Patterns: $@"
for p in "$@"; do
  PATTERN_FLAGS="${PATTERN_FLAGS} --pattern ${p}"
done

set -x
/ko-app/cache fetch ${PATTERN_FLAGS} \
                    --source ${PARAM_SOURCE} \
                    --folder ${PARAM_CACHE_PATH} \
                    --insecure ${PARAM_INSECURE} \
                    --workingdir ${PARAM_WORKING_DIR}
if [ $? -eq 0 ]; then
  echo -n true > $(step.results.fetched.path)
else
  echo -n false > $(step.results.fetched.path)
fi
`

const cacheUploadScript = `#!/bin/sh
if [ "${PARAM_FORCE_CACHE_UPLOAD}" = "false" ] && [ "${RESULT_CACHE_FETCHED}" = "true" ]; then
  echo "no need to upload cache"
  exit 0
fi

PATTERN_FLAGS=""
echo "Patterns: $@"
for p in "$@"; do
  PATTERN_FLAGS="${PATTERN_FLAGS} --pattern ${p}"
done

set -ex
/ko-app/cache upload ${PATTERN_FLAGS} \
                    --target ${PARAM_TARGET} \
                    --folder ${PARAM_CACHE_PATH} \
                    --insecure ${PARAM_INSECURE} \
                    --workingdir ${PARAM_WORKING_DIR}
`

// injectCacheSteps injects fully-inlined cache-fetch and cache-upload steps
// into tasks that declare cache paths. Steps are inlined (no StepAction ref)
// so they work without installing StepActions on the cluster.
func injectCacheSteps(pr *tekton.PipelineRun, p *dsl.Pipeline, opts Options) {
	if p.Cache == nil {
		return
	}

	// Collect cache paths from task-level cache declarations.
	type taskCacheEntry struct {
		taskName   string
		cachePaths []dsl.CachePath
	}
	var entries []taskCacheEntry
	for _, name := range p.TaskOrder {
		task := p.Tasks[name]
		if task == nil {
			continue
		}
		paths := task.Cache.EffectivePaths()
		if len(paths) > 0 {
			entries = append(entries, taskCacheEntry{taskName: name, cachePaths: paths})
		}
	}

	// Backward compat: if no task-level cache, fall back to pipeline-level paths.
	if len(entries) == 0 {
		pipelinePaths := p.Cache.EffectiveCachePaths()
		if len(pipelinePaths) == 0 {
			return
		}
		for i := range pr.Spec.PipelineSpec.Tasks {
			task := &pr.Spec.PipelineSpec.Tasks[i]
			if task.TaskSpec == nil || task.TaskRef != nil {
				continue
			}
			entries = append(entries, taskCacheEntry{taskName: task.Name, cachePaths: pipelinePaths})
		}
	}

	if len(entries) == 0 {
		return
	}

	for _, entry := range entries {
		for i := range pr.Spec.PipelineSpec.Tasks {
			task := &pr.Spec.PipelineSpec.Tasks[i]
			if task.Name != entry.taskName || task.TaskSpec == nil {
				continue
			}

			workDir := resolveWorkDir(task)
			insecure := "false"
			if p.Cache.Insecure {
				insecure = "true"
			}
			var credEnvs []tekton.EnvVar
			if p.Cache.Credentials != "" {
				credAlias := resolveCacheCredentialAlias(p.Cache.Credentials, p.Secrets)
				credEnvs = cacheCredentialEnvs(p.Cache.EffectiveImage(), credAlias)
			}

			if task.TaskSpec.Raw != nil {
				injectCacheStepsIntoRawTask(task, entry.cachePaths, p, workDir, insecure, credEnvs)
			} else {
				injectCacheStepsIntoTask(task, entry.cachePaths, p, workDir, insecure, credEnvs)
			}
		}
	}
}

// resolveWorkDir finds the workspace path expression for the task.
func resolveWorkDir(task *tekton.PipelineTask) string {
	for _, ws := range task.Workspaces {
		if ws.Workspace == sharedWorkspaceName {
			return fmt.Sprintf("$(workspaces.%s.path)", ws.Name)
		}
	}
	return "$(workspaces.shared-workspace.path)"
}

func buildFetchEnvs(source, cachePath, workDir, insecure string, credEnvs []tekton.EnvVar) []tekton.EnvVar {
	envs := []tekton.EnvVar{
		{Name: "PARAM_SOURCE", Value: source},
		{Name: "PARAM_CACHE_PATH", Value: cachePath},
		{Name: "PARAM_WORKING_DIR", Value: workDir},
		{Name: "PARAM_INSECURE", Value: insecure},
	}
	return append(envs, credEnvs...)
}

func buildUploadEnvs(target, cachePath, workDir, insecure, fetchedRef string, credEnvs []tekton.EnvVar) []tekton.EnvVar {
	envs := []tekton.EnvVar{
		{Name: "PARAM_TARGET", Value: target},
		{Name: "PARAM_CACHE_PATH", Value: cachePath},
		{Name: "PARAM_WORKING_DIR", Value: workDir},
		{Name: "PARAM_INSECURE", Value: insecure},
		{Name: "RESULT_CACHE_FETCHED", Value: fetchedRef},
		{Name: "PARAM_FORCE_CACHE_UPLOAD", Value: "false"},
	}
	return append(envs, credEnvs...)
}

func injectCacheStepsIntoTask(task *tekton.PipelineTask, cachePaths []dsl.CachePath, p *dsl.Pipeline, workDir, insecure string, credEnvs []tekton.EnvVar) {
	var fetchSteps []tekton.Step
	var uploadSteps []tekton.Step

	for _, cp := range cachePaths {
		sanitized := sanitizePath(cp.Path)
		cacheURI := buildCacheURI(p.Cache.EffectiveImage(), sanitized)
		fetchName := "cache-fetch-" + sanitized
		uploadName := "cache-upload-" + sanitized

		fetchSteps = append(fetchSteps, tekton.Step{
			Name:   fetchName,
			Image:  cacheImage,
			Env:    buildFetchEnvs(cacheURI, cp.Path, workDir, insecure, credEnvs),
			Script: cacheFetchScript,
			TektonRaw: map[string]any{
				"args":    cp.Key,
				"results": []any{map[string]any{"name": "fetched", "type": "string"}},
			},
		})

		uploadSteps = append(uploadSteps, tekton.Step{
			Name:   uploadName,
			Image:  cacheImage,
			Env:    buildUploadEnvs(cacheURI, cp.Path, workDir, insecure, fmt.Sprintf("$(steps.%s.results.fetched)", fetchName), credEnvs),
			Script: cacheUploadScript,
			TektonRaw: map[string]any{
				"args": cp.Key,
			},
		})
	}

	newSteps := make([]tekton.Step, 0, len(fetchSteps)+len(task.TaskSpec.Steps)+len(uploadSteps))
	newSteps = append(newSteps, fetchSteps...)
	newSteps = append(newSteps, task.TaskSpec.Steps...)
	newSteps = append(newSteps, uploadSteps...)
	task.TaskSpec.Steps = newSteps
}

func injectCacheStepsIntoRawTask(task *tekton.PipelineTask, cachePaths []dsl.CachePath, p *dsl.Pipeline, workDir, insecure string, credEnvs []tekton.EnvVar) {
	var fetchSteps []any
	var uploadSteps []any

	for _, cp := range cachePaths {
		sanitized := sanitizePath(cp.Path)
		cacheURI := buildCacheURI(p.Cache.EffectiveImage(), sanitized)
		fetchName := "cache-fetch-" + sanitized
		uploadName := "cache-upload-" + sanitized

		fetchEnv := toRawEnvs(buildFetchEnvs(cacheURI, cp.Path, workDir, insecure, credEnvs))
		uploadEnv := toRawEnvs(buildUploadEnvs(cacheURI, cp.Path, workDir, insecure, fmt.Sprintf("$(steps.%s.results.fetched)", fetchName), credEnvs))

		fetchSteps = append(fetchSteps, map[string]any{
			"name":   fetchName,
			"image":  cacheImage,
			"env":    fetchEnv,
			"script": cacheFetchScript,
			"args":   cp.Key,
			"results": []any{
				map[string]any{"name": "fetched", "type": "string"},
			},
		})

		uploadSteps = append(uploadSteps, map[string]any{
			"name":   uploadName,
			"image":  cacheImage,
			"env":    uploadEnv,
			"script": cacheUploadScript,
			"args":   cp.Key,
		})
	}

	existingSteps, _ := task.TaskSpec.Raw["steps"].([]any)
	newSteps := make([]any, 0, len(fetchSteps)+len(existingSteps)+len(uploadSteps))
	newSteps = append(newSteps, fetchSteps...)
	newSteps = append(newSteps, existingSteps...)
	newSteps = append(newSteps, uploadSteps...)
	task.TaskSpec.Raw["steps"] = newSteps
}

func toRawEnvs(envs []tekton.EnvVar) []any {
	raw := make([]any, len(envs))
	for i, e := range envs {
		raw[i] = map[string]any{"name": e.Name, "value": e.Value}
	}
	return raw
}

// resolveCacheCredentialAlias resolves the credentials value to a secret alias.
// Accepts either the alias ("quay") or the secret name ("quay-credentials").
func resolveCacheCredentialAlias(credentials string, secrets map[string]string) string {
	// Direct alias match.
	if _, ok := secrets[credentials]; ok {
		return credentials
	}
	// Match by secret name (value) — find the alias.
	for alias, secretName := range secrets {
		if secretName == credentials {
			return alias
		}
	}
	// Fallback: use as-is.
	return credentials
}

// cacheCredentialEnvs returns env vars for cache backend credentials.
func cacheCredentialEnvs(image, credentials string) []tekton.EnvVar {
	credPath := fmt.Sprintf("$(workspaces.secret-%s.path)", credentials)
	switch {
	case strings.HasPrefix(image, "oci://"):
		return []tekton.EnvVar{{Name: "DOCKER_CONFIG", Value: credPath}}
	case strings.HasPrefix(image, "s3://"):
		return []tekton.EnvVar{{Name: "AWS_SHARED_CREDENTIALS_FILE", Value: credPath}}
	case strings.HasPrefix(image, "gs://"):
		return []tekton.EnvVar{{Name: "GOOGLE_APPLICATION_CREDENTIALS", Value: credPath}}
	default:
		return nil
	}
}

// sanitizePath converts a cache path to a short, safe tag component.
// /workspace/maven-local-repo/.m2 → m2
// /go/pkg/mod → go-pkg-mod
// /root/.cache/go-build → go-build
// It takes only the last path segment to keep tags short.
func sanitizePath(path string) string {
	path = strings.TrimPrefix(path, "/")
	// Use the last meaningful segment for a short tag.
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if last == "" && len(parts) > 1 {
		last = parts[len(parts)-2]
	}
	last = strings.TrimPrefix(last, ".")
	last = strings.ReplaceAll(last, ".", "-")
	last = strings.ReplaceAll(last, "_", "-")
	return last
}

// buildCacheURI generates the cache URI.
// For OCI: cache entries are tags on the image.
//
//	image: oci://quay.io/org/my-cache, path "m2"
//	→ oci://quay.io/org/my-cache:m2-{{hash}}
//
// For S3/GCS: nested paths under the base URL.
func buildCacheURI(image, sanitizedPath string) string {
	image = strings.TrimRight(image, "/")

	if strings.HasPrefix(image, "oci://") {
		return image + ":" + sanitizedPath + "-{{hash}}"
	}

	return image + "/" + sanitizedPath + "/{{hash}}"
}
