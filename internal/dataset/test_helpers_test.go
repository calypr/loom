package dataset

import (
	"strings"
	"testing"
)

const fixtureSchemaSHA256 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func fixtureSchema(t *testing.T) SchemaIdentitySnapshot {
	t.Helper()
	schema, err := NewSchemaIdentitySnapshot(
		"urn:loom:test-schema",
		"",
		fixtureSchemaSHA256,
		[]string{"Patient", "Observation"},
	)
	if err != nil {
		t.Fatalf("NewSchemaIdentitySnapshot: %v", err)
	}
	return schema
}

func fixtureRef(t *testing.T, project, generation string) DatasetRef {
	t.Helper()
	ref, err := NewDatasetRef(project, generation)
	if err != nil {
		t.Fatalf("NewDatasetRef(%q, %q): %v", project, generation, err)
	}
	return ref
}

func fixtureManifest(t *testing.T, project, generation string) Manifest {
	t.Helper()
	manifest, err := NewManifest(fixtureRef(t, project, generation), fixtureSchema(t))
	if err != nil {
		t.Fatalf("NewManifest: %v", err)
	}
	return manifest
}

func transitionManifest(t *testing.T, manifest Manifest, state ManifestState) Manifest {
	t.Helper()
	next, err := manifest.Transition(state)
	if err != nil {
		t.Fatalf("Transition(%s -> %s): %v", manifest.State, state, err)
	}
	return next
}

func readyManifest(t *testing.T, project, generation string) Manifest {
	t.Helper()
	manifest := fixtureManifest(t, project, generation)
	manifest = transitionManifest(t, manifest, ManifestStateLoading)
	manifest = transitionManifest(t, manifest, ManifestStateAnalyzing)
	return transitionManifest(t, manifest, ManifestStateReady)
}

func repeated(char string, count int) string {
	return strings.Repeat(char, count)
}
