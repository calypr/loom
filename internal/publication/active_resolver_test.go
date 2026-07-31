package publication

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type staticResolver struct {
	manifest Manifest
	err      error
}

func (r staticResolver) ResolveActiveManifest(context.Context, string) (Manifest, error) {
	return r.manifest, r.err
}

func TestResolveActiveValidatesBoundary(t *testing.T) {
	ready, err := testManifest(t).Transition(StateReady)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveActive(context.Background(), staticResolver{manifest: ready}, "project-a"); err != nil || !reflect.DeepEqual(got, ready) {
		t.Fatalf("ResolveActive = %#v, %v", got, err)
	}
	tests := []struct {
		name     string
		resolver ActiveResolver
		project  string
		want     error
	}{
		{"nil", nil, "project-a", ErrInvalidActiveGeneration},
		{"missing", staticResolver{err: ErrNoActiveGeneration}, "project-a", ErrNoActiveGeneration},
		{"wrong project", staticResolver{manifest: ready}, "project-b", ErrInvalidActiveGeneration},
		{"invalid", staticResolver{manifest: Manifest{}}, "project-a", ErrInvalidManifest},
		{"loading", staticResolver{manifest: testManifest(t)}, "project-a", ErrGenerationNotReady},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ResolveActive(context.Background(), tt.resolver, tt.project); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}
