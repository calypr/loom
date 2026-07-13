package ingest

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func TestNamespaceRowBuildResultKeepsLogicalFHIRIdentityAndQualifiesGraphIdentity(t *testing.T) {
	result := rowBuildResult{
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
	if got, want := documentString(t, vertex, logicalKeyField), "patient-1"; got != want {
		t.Fatalf("vertex logical key = %q, want %q", got, want)
	}
	if got, want := documentString(t, vertex, generationIdentityField), "generation-1"; got != want {
		t.Fatalf("vertex dataset generation = %q, want %q", got, want)
	}
	vertexKey := documentString(t, vertex, "_key")
	if vertexKey == "patient-1" || len(vertexKey) != len("g_")+64 {
		t.Fatalf("vertex physical key = %q, want generation-qualified hash", vertexKey)
	}
	if got, want := string(vertex["payload"]), `{"resourceType":"Patient","id":"patient-1"}`; got != want {
		t.Fatalf("payload changed\ngot:  %s\nwant: %s", got, want)
	}

	if got, want := len(namespaced.edges), 1; got != want {
		t.Fatalf("edge count = %d, want %d", got, want)
	}
	edge := decodeIdentityDocument(t, namespaced.edges[0])
	if got, want := documentString(t, edge, logicalKeyField), "subject-edge"; got != want {
		t.Fatalf("edge logical key = %q, want %q", got, want)
	}
	if got, want := documentString(t, edge, generationIdentityField), "generation-1"; got != want {
		t.Fatalf("edge dataset generation = %q, want %q", got, want)
	}
	if got, want := documentString(t, edge, "_from"), generationDocumentIDMust(t, "project-a", "generation-1", "Observation/obs-1"); got != want {
		t.Fatalf("edge _from = %q, want %q", got, want)
	}
	if got, want := documentString(t, edge, "_to"), "Patient/"+vertexKey; got != want {
		t.Fatalf("edge _to = %q, want %q", got, want)
	}
	if key := documentString(t, edge, "_key"); key == "subject-edge" || len(key) != len("g_")+64 {
		t.Fatalf("edge physical key = %q, want generation-qualified hash", key)
	}
	if !reflect.DeepEqual(namespaced.payload, result.payload) {
		t.Fatalf("payload map changed\ngot:  %#v\nwant: %#v", namespaced.payload, result.payload)
	}
}

func TestNamespaceRowBuildResultSeparatesProjectsAndGenerationsWithSameFHIRID(t *testing.T) {
	result := rowBuildResult{
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
		result rowBuildResult
	}{
		{
			name:   "missing vertex key",
			result: rowBuildResult{vertex: json.RawMessage(`{"project":"project-a"}`)},
		},
		{
			name:   "project mismatch",
			result: rowBuildResult{vertex: json.RawMessage(`{"_key":"one","project":"project-b"}`)},
		},
		{
			name: "malformed edge endpoint",
			result: rowBuildResult{
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
	delegate := rowBuilderFunc(func(resourceType string, line []byte, stageSeconds map[string]float64) (rowBuildResult, rowErrorType, error) {
		return rowBuildResult{vertex: json.RawMessage(`{"project":"project-a"}`)}, "", nil
	})
	builder, err := newGenerationRowBuilder(delegate, "project-a", "generation-a")
	if err != nil {
		t.Fatal(err)
	}
	_, kind, err := builder.Build("Patient", []byte(`{}`), map[string]float64{})
	if err == nil || kind != rowErrorGeneration {
		t.Fatalf("Build() = kind %q err %v, want generation error", kind, err)
	}
}

type rowBuilderFunc func(string, []byte, map[string]float64) (rowBuildResult, rowErrorType, error)

func (f rowBuilderFunc) Build(resourceType string, line []byte, stageSeconds map[string]float64) (rowBuildResult, rowErrorType, error) {
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

func edgeIdentityTuples(t *testing.T, edges []json.RawMessage) []string {
	t.Helper()
	tuples := make([]string, 0, len(edges))
	for _, raw := range edges {
		document := decodeIdentityDocument(t, raw)
		tuples = append(tuples, strings.Join([]string{
			documentString(t, document, "_key"),
			documentString(t, document, "_from"),
			documentString(t, document, "_to"),
			documentString(t, document, "label"),
			documentString(t, document, generationIdentityField),
		}, "\x00"))
	}
	sort.Strings(tuples)
	return tuples
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
