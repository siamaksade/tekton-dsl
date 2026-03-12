package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseClusterRef(t *testing.T) {
	tests := []struct {
		input     string
		namespace string
		name      string
		wantErr   bool
	}{
		{"my-namespace/git-clone", "my-namespace", "git-clone", false},
		{"openshift-pipelines/buildah", "openshift-pipelines", "buildah", false},
		{"cluster://dsl-test/maven", "dsl-test", "maven", false},
		{"git-clone", "", "", true},              // no namespace
		{"/git-clone", "", "", true},              // empty namespace
		{"my-namespace/", "", "", true},            // empty name
		{"", "", "", true},                         // empty string
	}
	for _, tt := range tests {
		ns, name, err := parseClusterRef(tt.input)
		if tt.wantErr {
			assert.Error(t, err, "parseClusterRef(%q)", tt.input)
		} else {
			require.NoError(t, err, "parseClusterRef(%q)", tt.input)
			assert.Equal(t, tt.namespace, ns, "namespace for %q", tt.input)
			assert.Equal(t, tt.name, name, "name for %q", tt.input)
		}
	}
}
