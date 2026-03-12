package dsl

// CompileOptions provides context for DSL compilation.
// Kept here for backward compat; prefer pkg/tkndsl.CompileOptions.
type CompileOptions struct {
	RepoOwner string
	RepoName  string
}
