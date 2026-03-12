package validate

import (
	"testing"

	"github.com/ssadeghi/tkn-dsl/pkg/dsl"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parse(t *testing.T, input string) *dsl.Pipeline {
	t.Helper()
	p, err := dsl.Parse([]byte(input))
	require.NoError(t, err)
	return p
}

func TestValidMissingName(t *testing.T) {
	p := parse(t, `
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`)
	p.Name = ""
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "name")
}

func TestValidNoTasks(t *testing.T) {
	p := &dsl.Pipeline{Name: "test"}
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "at least one task")
}

func TestValidNeedsUnresolved(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  build:
    needs: [nonexistent]
    image: redhat/ubi9-minimal
    run: echo build
`)
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "nonexistent")
}

func TestValidCycleDetection(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  a:
    needs: [b]
    image: redhat/ubi9-minimal
    run: echo a
  b:
    needs: [a]
    image: redhat/ubi9-minimal
    run: echo b
`)
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "circular dependency")
}

func TestValidIfExpression(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  deploy:
    if: params.x && params.y
    image: redhat/ubi9-minimal
    run: echo deploy
`)
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "invalid if expression")
}

func TestValidTaskMustHaveRunOrStepsOrUses(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  empty:
    image: redhat/ubi9-minimal
`)
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "must have one of")
}

func TestValidTaskCannotHaveRunAndSteps(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  both:
    image: redhat/ubi9-minimal
    run: echo hello
    steps:
      - name: step1
        run: echo step
`)
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "cannot have more than one")
}

func TestValidCacheImage(t *testing.T) {
	p := parse(t, `
name: test
cache:
  image: http://invalid.com
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
    cache:
      path: /go/pkg/mod
      key: ["**/go.sum"]
`)
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "oci://, s3://, or gs://")
}

func TestValidSecretInvalidName(t *testing.T) {
	p := parse(t, `
name: test
secrets:
  my-secret: "INVALID_NAME_WITH_CAPS"
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`)
	errs := Semantic(p)
	require.Len(t, errs, 1)
	assert.Contains(t, errs[0].Message, "invalid Kubernetes Secret name")
}

func TestValidSecretValidName(t *testing.T) {
	p := parse(t, `
name: test
secrets:
  my-secret: valid-secret-name
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`)
	errs := Semantic(p)
	assert.Empty(t, errs)
}

func TestValidUsesRefWithSpaces(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  bad:
    uses: "invalid ref with spaces"
`)
	errs := Semantic(p)
	var found bool
	for _, e := range errs {
		if contains(e.Message, "must not contain spaces") {
			found = true
		}
	}
	assert.True(t, found, "expected error about spaces in uses ref")
}

func TestValidUndeclaredParamRef(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo $(params.missing-param)
`)
	errs := Semantic(p)
	var found bool
	for _, e := range errs {
		if contains(e.Message, "undeclared param") && contains(e.Message, "missing-param") {
			found = true
		}
	}
	assert.True(t, found, "expected undeclared param error, got: %v", errs)
}

func TestValidDeclaredParamRefOK(t *testing.T) {
	p := parse(t, `
name: test
params:
  branch: "Branch name"
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo $(params.branch)
`)
	errs := Semantic(p)
	// Should have unused param warning but no undeclared param error.
	for _, e := range errs {
		assert.NotContains(t, e.Message, "undeclared param")
	}
}

func TestValidUndeclaredResultRef(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  a:
    image: redhat/ubi9-minimal
    run: echo hello
  b:
    needs: [a]
    image: redhat/ubi9-minimal
    run: echo $(tasks.a.results.nonexistent)
`)
	errs := Semantic(p)
	var found bool
	for _, e := range errs {
		if contains(e.Message, "undeclared result") {
			found = true
		}
	}
	assert.True(t, found, "expected undeclared result error, got: %v", errs)
}

func TestValidResultRefToUnknownTask(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  b:
    image: redhat/ubi9-minimal
    run: echo $(tasks.nonexistent.results.foo)
`)
	errs := Semantic(p)
	var found bool
	for _, e := range errs {
		if contains(e.Message, "unknown task") && contains(e.Message, "nonexistent") {
			found = true
		}
	}
	assert.True(t, found, "expected unknown task error, got: %v", errs)
}

func TestValidUnusedParamWarning(t *testing.T) {
	p := parse(t, `
name: test
params:
  unused-param: "never-used-value"
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`)
	// Warnings are separate from errors.
	errs := Semantic(p)
	assert.Empty(t, errs, "unused params should not cause validation errors")

	warnings := Warnings(p)
	var found bool
	for _, w := range warnings {
		if contains(w.Message, "unused-param") {
			found = true
		}
	}
	assert.True(t, found, "expected unused param warning, got: %v", warnings)
}

func TestValidOK(t *testing.T) {
	p := parse(t, `
name: test
tasks:
  hello:
    image: redhat/ubi9-minimal
    run: echo hello
`)
	errs := Semantic(p)
	assert.Empty(t, errs)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
