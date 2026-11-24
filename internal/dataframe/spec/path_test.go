package spec_test

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestFieldPathSegmentValidation(t *testing.T) {
	for _, segment := range []string{"a", "A", "value_2", "Observation"} {
		if !spec.ValidFieldPathSegment(segment) {
			t.Errorf("ValidFieldPathSegment(%q) = false", segment)
		}
	}
	for _, segment := range []string{"", "_key", "2value", "value-name", "é"} {
		if spec.ValidFieldPathSegment(segment) {
			t.Errorf("ValidFieldPathSegment(%q) = true", segment)
		}
	}
}

func TestPathHasPrefix(t *testing.T) {
	tests := []struct {
		name   string
		path   []string
		prefix []string
		want   bool
	}{
		{name: "same path", path: []string{"payload", "code"}, prefix: []string{"payload", "code"}, want: true},
		{name: "child path", path: []string{"payload", "code", "system"}, prefix: []string{"payload", "code"}, want: true},
		{name: "shorter path", path: []string{"payload"}, prefix: []string{"payload", "code"}},
		{name: "sibling prefix", path: []string{"payload", "coding"}, prefix: []string{"payload", "code"}},
		{name: "empty prefix", path: []string{"payload"}, prefix: []string{}, want: true},
		{name: "empty path and prefix", path: []string{}, prefix: []string{}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := spec.PathHasPrefix(test.path, test.prefix); got != test.want {
				t.Fatalf("PathHasPrefix(%v, %v) = %v, want %v", test.path, test.prefix, got, test.want)
			}
		})
	}
}
