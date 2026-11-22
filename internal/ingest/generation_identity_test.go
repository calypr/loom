package ingest

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
)

func TestNamespaceRowBuildResultKeepsLogicalFHIRIdentityAndQualifiesGraphIdentity(t *testing.T) {
	result := RowBuildResult{
		vertex: json.RawMessage(`{"_key":"patient-1","id":"patient-1","project":"project-a","resourceType":"Patient","payload":{"resourceType":"Patient","id":"patient-1"}}`),
		edges: []json.RawMessage{
			json.RawMessage(`{"_key":"subject-edge","_from":"Observation/obs-1","_to":"Patient/patient-1","label":"subject_Patient","project":"project-a","from_type":"Observation","to_type":"Patient"}`),
		},
		payload: map[string]any{"resourceType": "Patient", "id": "patient-1"},
	}

	namespaced, err := namespaceRowBuildResult(result, "project-a", "generation-1", "Patient")
	if err != nil {
		t.Fatalf("namespaceRowBuildResult() error = %v", err)
	}
	vertex := decodeIdentityDocument(t, namespaced.vertex)
	if got, want := documentString(t, vertex, "id"), "patient-1"; got != want {
		t.Fatalf("vertex id = %q, want %q", got, want)
	}
	if got, want := documentString(t, vertex, LogicalKeyField), "patient-1"; got != want {
		t.Fatalf("vertex logical key = %q, want %q", got, want)
	}
	if got, want := documentString(t, vertex, GenerationIdentityField), "generation-1"; got != want {
		t.Fatalf("vertex dataset generation = %q, want %q", got, want)
	}
	vertexKey := documentString(t, vertex, "_key")
	if vertexKey == "patient-1" || len(vertexKey) != 34 || !strings.HasPrefix(vertexKey, "g2") {
		t.Fatalf("vertex physical key = %q, want generation-qualified hash", vertexKey)
	}
	if got, want := string(vertex["payload"]), `{"resourceType":"Patient","id":"patient-1"}`; got != want {
		t.Fatalf("payload changed\ngot:  %s\nwant: %s", got, want)
	}

	if got, want := len(namespaced.edges), 1; got != want {
		t.Fatalf("edge count = %d, want %d", got, want)
	}
	edge := decodeIdentityDocument(t, namespaced.edges[0])
	if got, want := documentString(t, edge, LogicalKeyField), "subject-edge"; got != want {
		t.Fatalf("edge logical key = %q, want %q", got, want)
	}
	if got, want := documentString(t, edge, GenerationIdentityField), "generation-1"; got != want {
		t.Fatalf("edge dataset generation = %q, want %q", got, want)
	}
	if got, want := documentString(t, edge, "_from"), generationDocumentIDMust(t, "project-a", "generation-1", "Observation/obs-1"); got != want {
		t.Fatalf("edge _from = %q, want %q", got, want)
	}
	if got, want := documentString(t, edge, "_to"), "Patient/"+vertexKey; got != want {
		t.Fatalf("edge _to = %q, want %q", got, want)
	}
	if key := documentString(t, edge, "_key"); key == "subject-edge" || len(key) != 34 || !strings.HasPrefix(key, "g2") {
		t.Fatalf("edge physical key = %q, want generation-qualified hash", key)
	}
	if !reflect.DeepEqual(namespaced.payload, result.payload) {
		t.Fatalf("payload map changed\ngot:  %#v\nwant: %#v", namespaced.payload, result.payload)
	}
}

func TestGenerationIdentityFormatKeepsLegacyKeysAndUsesCompactKeys(t *testing.T) {
	const (
		project    = "project-a"
		generation = "generation-a"
		resource   = "Patient"
		logicalKey = "patient-1"
	)

	if got, want := GenerationDocumentKeyWithFormat(project, generation, resource, logicalKey, publication.IdentityFormatLegacy), "g_297a2d590f1d1a2b5f2380ad8e3d8bf21b7aed2e3270e25559ae489097b810a6"; got != want {
		t.Fatalf("legacy key = %q, want %q", got, want)
	}
	if got, want := GenerationDocumentKeyWithFormat(project, generation, resource, logicalKey, publication.IdentityFormatCompact), "g2297a2d590f1d1a2b5f2380ad8e3d8bf2"; got != want {
		t.Fatalf("compact key = %q, want %q", got, want)
	}
}

func TestGenerationRowBuilderUsesManifestIdentityFormat(t *testing.T) {
	const project, generation, resourceType, logicalKey = "project-a", "generation-a", "Patient", "patient-1"
	line := []byte(`{"resourceType":"Patient","id":"patient-1"}`)
	for _, format := range []string{publication.IdentityFormatLegacy, publication.IdentityFormatCompact} {
		t.Run(format, func(t *testing.T) {
			builder, err := NewGenerationRowBuilderWithFormat(NewGeneratedRowBuilder(project, ""), project, generation, format)
			if err != nil {
				t.Fatal(err)
			}
			result, kind, err := builder.Build(resourceType, line, map[string]float64{})
			if err != nil {
				t.Fatalf("Build() kind %q: %v", kind, err)
			}
			vertex := decodeIdentityDocument(t, result.vertex)
			want := GenerationDocumentKeyWithFormat(project, generation, resourceType, logicalKey, format)
			if got := documentString(t, vertex, "_key"); got != want {
				t.Fatalf("vertex key = %q, want %q", got, want)
			}
		})
	}
}

func TestNamespaceRowBuildResultSeparatesProjectsAndGenerationsWithSameFHIRID(t *testing.T) {
	result := RowBuildResult{
		vertex:  json.RawMessage(`{"_key":"same-id","id":"same-id","project":"project-a","resourceType":"Patient"}`),
		payload: map[string]any{"id": "same-id"},
	}
	first, err := namespaceRowBuildResult(result, "project-a", "generation-a", "Patient")
	if err != nil {
		t.Fatal(err)
	}
	second, err := namespaceRowBuildResult(result, "project-a", "generation-b", "Patient")
	if err != nil {
		t.Fatal(err)
	}
	otherProject := result
	otherProject.vertex = json.RawMessage(`{"_key":"same-id","id":"same-id","project":"project-b","resourceType":"Patient"}`)
	third, err := namespaceRowBuildResult(otherProject, "project-b", "generation-a", "Patient")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{
		documentString(t, decodeIdentityDocument(t, first.vertex), "_key"),
		documentString(t, decodeIdentityDocument(t, second.vertex), "_key"),
		documentString(t, decodeIdentityDocument(t, third.vertex), "_key"),
	}
	if keys[0] == keys[1] || keys[0] == keys[2] || keys[1] == keys[2] {
		t.Fatalf("namespaced keys collide: %#v", keys)
	}
}

func TestNamespaceRowBuildResultRejectsMalformedOrCrossProjectDocuments(t *testing.T) {
	tests := []struct {
		name   string
		result RowBuildResult
	}{
		{
			name:   "missing vertex key",
			result: RowBuildResult{vertex: json.RawMessage(`{"project":"project-a"}`)},
		},
		{
			name:   "project mismatch",
			result: RowBuildResult{vertex: json.RawMessage(`{"_key":"one","project":"project-b"}`)},
		},
		{
			name: "malformed edge endpoint",
			result: RowBuildResult{
				vertex: json.RawMessage(`{"_key":"one","project":"project-a"}`),
				edges:  []json.RawMessage{json.RawMessage(`{"_key":"edge","_from":"not-a-document-id","_to":"Patient/one","project":"project-a"}`)},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := namespaceRowBuildResult(test.result, "project-a", "generation-a", "Patient"); err == nil {
				t.Fatal("namespaceRowBuildResult() error = nil, want validation failure")
			}
		})
	}
}

func TestGenerationRowBuilderTurnsIdentityFailureIntoGenerationError(t *testing.T) {
	delegate := rowBuilderFunc(func(resourceType string, line []byte, stageSeconds map[string]float64) (RowBuildResult, RowErrorType, error) {
		return RowBuildResult{vertex: json.RawMessage(`{"project":"project-a"}`)}, "", nil
	})
	builder, err := NewGenerationRowBuilder(delegate, "project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	_, kind, err := builder.Build("Patient", []byte(`{}`), map[string]float64{})
	if err == nil || kind != RowErrorGeneration {
		t.Fatalf("Build() = kind %q err %v, want generation error", kind, err)
	}
}

func TestGenerationRowBuilderRejectsInvalidUTF8Project(t *testing.T) {
	const project = "p\xff"
	builder, err := NewGenerationRowBuilder(NewGeneratedRowBuilder(project, ""), project, "generation")
	if err != nil {
		t.Fatal(err)
	}
	_, kind, err := builder.Build("Patient", []byte(`{"resourceType":"Patient","id":"a"}`), map[string]float64{})
	if err == nil || kind != RowErrorGeneration {
		t.Fatalf("expected generation error, got %q %v", kind, err)
	}
}

func TestGenerationRowBuilderFallbackKeepsSourceOwnerAndRecountsNamespacedEdges(t *testing.T) {
	const project = "project-1"
	result, _, err := NewGeneratedRowBuilder(project, "").Build("ResearchSubject", []byte(`{"resourceType":"ResearchSubject","id":"r","status":"active","study":{"reference":"ResearchStudy/s"},"subject":{"reference":"Patient/p"}}`), map[string]float64{})
	if err != nil {
		t.Fatal(err)
	}
	delegate := rowBuilderFunc(func(string, []byte, map[string]float64) (RowBuildResult, RowErrorType, error) {
		return result, "", nil
	})
	builder, err := NewGenerationRowBuilder(delegate, project, "generation-1")
	if err != nil {
		t.Fatal(err)
	}
	owned, kind, err := (sourceFileOwnerRowBuilder{delegate: builder, owner: "ResearchSubject"}).Build("ResearchSubject", nil, map[string]float64{})
	if err != nil {
		t.Fatalf("Build() kind %q error = %v", kind, err)
	}
	if len(owned.edges) == 0 {
		t.Fatal("Build() returned no edges")
	}
	var edge map[string]any
	if err := json.Unmarshal(owned.edges[0], &edge); err != nil {
		t.Fatal(err)
	}
	if got := edge["source_file"]; got != "ResearchSubject" {
		t.Fatalf("source_file = %v, want ResearchSubject", got)
	}
	if got := edge[GenerationIdentityField]; got != "generation-1" {
		t.Fatalf("dataset_generation = %v, want generation-1", got)
	}
	if owned.relationships != nil {
		t.Fatalf("fallback relationship keys = %#v, want nil so final documents are recounted", owned.relationships)
	}
	counts, err := catalog.RelationshipCountsFromRawEdges(owned.edges)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 2 {
		t.Fatalf("final document relationship counts = %#v, want both ResearchStudy and Patient endpoints", counts)
	}
	for relationship := range counts {
		if relationship.DatasetGeneration != "generation-1" || relationship.FromType != "ResearchSubject" {
			t.Fatalf("final document relationship = %#v, want namespaced ResearchSubject relationship", relationship)
		}
	}
}

type rowBuilderFunc func(string, []byte, map[string]float64) (RowBuildResult, RowErrorType, error)

func (f rowBuilderFunc) Build(resourceType string, line []byte, stageSeconds map[string]float64) (RowBuildResult, RowErrorType, error) {
	return f(resourceType, line, stageSeconds)
}

func decodeIdentityDocument(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode document %s: %v", raw, err)
	}
	return document
}

func documentString(t *testing.T, document map[string]json.RawMessage, field string) string {
	t.Helper()
	var value string
	if raw, ok := document[field]; ok {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

func generationDocumentIDMust(t *testing.T, project, generation, documentID string) string {
	t.Helper()
	value, err := generationDocumentID(project, generation, documentID)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve ingest test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func repoPath(t *testing.T, elems ...string) string {
	t.Helper()
	return filepath.Join(append([]string{repoRoot(t)}, elems...)...)
}
