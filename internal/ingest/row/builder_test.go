package row

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/identity"
	"github.com/calypr/loom/internal/ingest/source"
	"github.com/calypr/loom/internal/testkit"
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
	if got, want := documentString(t, vertex, identity.LogicalKeyField), "patient-1"; got != want {
		t.Fatalf("vertex logical key = %q, want %q", got, want)
	}
	if got, want := documentString(t, vertex, identity.GenerationIdentityField), "generation-1"; got != want {
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
	if got, want := documentString(t, edge, identity.LogicalKeyField), "subject-edge"; got != want {
		t.Fatalf("edge logical key = %q, want %q", got, want)
	}
	if got, want := documentString(t, edge, identity.GenerationIdentityField), "generation-1"; got != want {
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

	if got, want := identity.GenerationDocumentKeyWithFormat(project, generation, resource, logicalKey, publication.IdentityFormatLegacy), "g_297a2d590f1d1a2b5f2380ad8e3d8bf21b7aed2e3270e25559ae489097b810a6"; got != want {
		t.Fatalf("legacy key = %q, want %q", got, want)
	}
	if got, want := identity.GenerationDocumentKeyWithFormat(project, generation, resource, logicalKey, publication.IdentityFormatCompact), "g2297a2d590f1d1a2b5f2380ad8e3d8bf2"; got != want {
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
			want := identity.GenerationDocumentKeyWithFormat(project, generation, resourceType, logicalKey, format)
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
	if got := edge[identity.GenerationIdentityField]; got != "generation-1" {
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
	value, err := identity.GenerationDocumentID(project, generation, documentID)
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

type parityRowBuilder struct {
	result RowBuildResult
}

func (b parityRowBuilder) Build(string, []byte, map[string]float64) (RowBuildResult, RowErrorType, error) {
	return b.result, "", nil
}

func TestGeneratedEdgeRowsMatchRawEdgePath(t *testing.T) {
	files := fixtureNDJSONFiles(t)
	if meta := os.Getenv("LOOM_INGEST_EDGE_PARITY_META"); meta != "" {
		metaFiles, err := source.DiscoverNDJSON(meta)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, metaFiles...)
	}
	for _, file := range files {
		t.Run(filepath.Base(filepath.Dir(file))+"/"+filepath.Base(file), func(t *testing.T) {
			input, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			scanner := bufio.NewScanner(input)
			scanner.Buffer(make([]byte, 1024), 64*1024*1024)
			for lineNumber := 1; scanner.Scan(); lineNumber++ {
				line := append([]byte(nil), scanner.Bytes()...)
				resourceType := source.ResourceTypeFromPath(file)
				if err := compareEdgePaths(resourceType, line); err != nil {
					t.Fatalf("line %d: %v", lineNumber, err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func compareEdgePaths(resourceType string, line []byte) error {
	_, documents, _, gotKind, gotErr := loadRowGeneratedDocumentsWithPayload(resourceType, line, "edge-parity", map[string]float64{})
	_, rawEdges, _, wantKind, wantErr := loadRowGeneratedWithPayload(resourceType, line, "edge-parity", map[string]float64{})
	if (gotErr != nil) != (wantErr != nil) || gotKind != wantKind {
		return fmt.Errorf("decode error differs: current %q %v, raw %q %v", gotKind, gotErr, wantKind, wantErr)
	}
	if gotErr != nil {
		return nil
	}
	for _, auth := range []string{"", "/programs/p/projects/q"} {
		for _, generation := range []string{"", "generation-1"} {
			for _, sourceFile := range []string{"", resourceType} {
				got, gotRelationships, err := marshalEdgeRows(documents, "edge-parity", auth, generation, sourceFile)
				if err != nil {
					return err
				}
				want := append([]json.RawMessage(nil), rawEdges...)
				if auth != "" {
					for index, edge := range want {
						want[index], err = edgeWithAuthResourcePath(edge, auth)
						if err != nil {
							return err
						}
					}
				}
				if generation != "" {
					want, err = namespaceRowEdges(want, "edge-parity", generation)
					if err != nil {
						return err
					}
				}
				if sourceFile != "" {
					result, _, sourceErr := (sourceFileOwnerRowBuilder{
						delegate: parityRowBuilder{result: RowBuildResult{edges: want}},
						owner:    sourceFile,
					}).Build(resourceType, line, map[string]float64{})
					if sourceErr != nil {
						return sourceErr
					}
					want = result.edges
				}
				gotMaps, err := decodeEdgeMapsRaw(got)
				if err != nil {
					return err
				}
				wantMaps, err := decodeEdgeMapsRaw(want)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(gotMaps, wantMaps) {
					return fmt.Errorf("edge documents differ for auth=%q generation=%q source=%q", auth, generation, sourceFile)
				}
				wantCounts, err := catalog.RelationshipCountsFromRawEdges(want)
				if err != nil {
					return err
				}
				gotCounts := make(map[catalog.RelationshipKey]int64)
				for _, relationship := range gotRelationships {
					gotCounts[relationship]++
				}
				if !reflect.DeepEqual(gotCounts, wantCounts) {
					return fmt.Errorf("relationship counts differ for auth=%q generation=%q source=%q", auth, generation, sourceFile)
				}
			}
		}
	}
	return nil
}

func fixtureNDJSONFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.Walk("../../../tests/fixtures", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(info.Name(), ".ndjson") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func decodeEdgeMapsRaw(edges []json.RawMessage) ([]map[string]any, error) {
	result := make([]map[string]any, len(edges))
	for index, edge := range edges {
		if err := json.Unmarshal(edge, &result[index]); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func TestEdgeWithAuthResourcePathFailsClosed(t *testing.T) {
	if _, err := edgeWithAuthResourcePath(json.RawMessage(`{`), "projects/p"); err == nil {
		t.Fatal("malformed edge was returned without its authorization path")
	}
}

func TestGeneratedRowBuilderPreservesPayloadIdentityAndEdges(t *testing.T) {
	const project = "project-1"
	const authResourcePath = "projects/p/specimens"
	builder := NewGeneratedRowBuilder(project, authResourcePath)
	line := []byte(`{
		"resourceType": "ResearchSubject",
		"id": "research-subject-1",
		"status": "active",
		"study": {"reference": "ResearchStudy/study-1"},
		"subject": {"reference": "Patient/patient-1"},
		"project_id": "source-value",
		"unknown_payload_field": {"nested": ["kept", 42]}
	}`)

	result, errorType, err := builder.Build("ResearchSubject", line, map[string]float64{})
	if err != nil {
		t.Fatalf("GeneratedRowBuilder.Build(): kind=%q err=%v", errorType, err)
	}

	var vertex VertexDocument
	if err := json.Unmarshal(result.vertex, &vertex); err != nil {
		t.Fatalf("decode vertex: %v", err)
	}
	if vertex.Key != "research-subject-1" || vertex.ID != "research-subject-1" || vertex.ResourceType != "ResearchSubject" {
		t.Fatalf("vertex identity = (%q, %q, %q), want research-subject-1 / research-subject-1 / ResearchSubject", vertex.Key, vertex.ID, vertex.ResourceType)
	}
	if vertex.Project != project || vertex.ProjectID != project {
		t.Fatalf("vertex project identity = (%q, %q), want %q", vertex.Project, vertex.ProjectID, project)
	}
	if vertex.AuthResourcePath != authResourcePath {
		t.Fatalf("vertex auth_resource_path = %q, want %q", vertex.AuthResourcePath, authResourcePath)
	}

	vertexPayload, ok := vertex.Payload.(map[string]any)
	if !ok {
		t.Fatalf("vertex payload type = %T, want map[string]any", vertex.Payload)
	}
	if !reflect.DeepEqual(vertexPayload, result.payload) {
		t.Fatalf("vertex payload differs from profiler payload\nvertex: %#v\nprofiler: %#v", vertexPayload, result.payload)
	}
	for name, payload := range map[string]map[string]any{
		"profiler payload": result.payload,
		"vertex payload":   vertexPayload,
	} {
		if got := payload["resourceType"]; got != "ResearchSubject" {
			t.Errorf("%s resourceType = %#v, want ResearchSubject", name, got)
		}
		if got := payload["id"]; got != "research-subject-1" {
			t.Errorf("%s id = %#v, want research-subject-1", name, got)
		}
		if got := payload["project_id"]; got != project {
			t.Errorf("%s project_id = %#v, want %q", name, got, project)
		}
		if got := payload["status"]; got != "active" {
			t.Errorf("%s status = %#v, want active", name, got)
		}
		unknown, ok := payload["unknown_payload_field"].(map[string]any)
		if !ok {
			t.Errorf("%s lost unknown field: %#v", name, payload["unknown_payload_field"])
			continue
		}
		nested, ok := unknown["nested"].([]any)
		if !ok || len(nested) != 2 || nested[0] != "kept" || nested[1] != float64(42) {
			t.Errorf("%s unknown field value = %#v", name, unknown)
		}
	}

	var foundStudyEdge bool
	for _, raw := range result.edges {
		var edge map[string]any
		if err := json.Unmarshal(raw, &edge); err != nil {
			t.Fatalf("decode generated edge: %v", err)
		}
		if edge["label"] == "study" {
			foundStudyEdge = true
			if key, ok := edge["_key"].(string); !ok || key == "" {
				t.Errorf("study edge _key = %#v, want a generated key", edge["_key"])
			}
			if edge["auth_resource_path"] != authResourcePath {
				t.Errorf("study edge auth_resource_path = %#v, want %q", edge["auth_resource_path"], authResourcePath)
			}
			if edge["_from"] != "ResearchSubject/research-subject-1" || edge["_to"] != "ResearchStudy/study-1" {
				t.Errorf("study edge endpoints = (%#v, %#v)", edge["_from"], edge["_to"])
			}
			if edge["from_type"] != "ResearchSubject" || edge["to_type"] != "ResearchStudy" {
				t.Errorf("study edge types = (%#v, %#v)", edge["from_type"], edge["to_type"])
			}
		}
	}
	if !foundStudyEdge {
		t.Fatalf("generated ResearchSubject edges missing study edge: %#v", result.edges)
	}

	generationBuilder, err := NewGenerationRowBuilder(builder, project, "generation-1")
	if err != nil {
		t.Fatalf("NewGenerationRowBuilder(): %v", err)
	}
	namespaced, errorType, err := generationBuilder.Build("ResearchSubject", line, map[string]float64{})
	if err != nil {
		t.Fatalf("generationRowBuilder.Build(): kind=%q err=%v", errorType, err)
	}
	var namespacedVertex map[string]json.RawMessage
	if err := json.Unmarshal(namespaced.vertex, &namespacedVertex); err != nil {
		t.Fatalf("decode namespaced vertex: %v", err)
	}
	if got := documentString(t, namespacedVertex, identity.LogicalKeyField); got != "research-subject-1" {
		t.Fatalf("namespaced vertex logical key = %q, want research-subject-1", got)
	}
	if got := documentString(t, namespacedVertex, identity.GenerationIdentityField); got != "generation-1" {
		t.Fatalf("namespaced vertex generation = %q, want generation-1", got)
	}
	if got := documentString(t, namespacedVertex, "auth_resource_path"); got != authResourcePath {
		t.Fatalf("namespaced vertex auth_resource_path = %q, want %q", got, authResourcePath)
	}
	if got := documentString(t, namespacedVertex, "_key"); got != identity.GenerationDocumentKey(project, "generation-1", "ResearchSubject", vertex.Key) {
		t.Fatalf("namespaced vertex key = %q, want generation-qualified key", got)
	}
	var storedPayload map[string]any
	if err := json.Unmarshal(namespacedVertex["payload"], &storedPayload); err != nil {
		t.Fatalf("decode namespaced payload: %v", err)
	}
	if !reflect.DeepEqual(storedPayload, vertexPayload) {
		t.Fatalf("generation changed the stored payload: %#v", storedPayload)
	}
	if len(namespaced.edges) != len(result.edges) {
		t.Fatalf("namespaced edge count = %d, want %d", len(namespaced.edges), len(result.edges))
	}
	if namespaced.payload["project_id"] != project || namespaced.payload["unknown_payload_field"] == nil {
		t.Fatalf("namespaced profiler payload changed: %#v", namespaced.payload)
	}
}

func TestGraphRowsFixturePassesProductionIngestionValidation(t *testing.T) {
	directory := testkit.FixturePath(t, "graph-rows")
	raw, err := os.ReadFile(filepath.Join(directory, "oracle.json"))
	if err != nil {
		t.Fatal(err)
	}
	var oracle struct {
		RecordCounts map[string]int `json:"recordCounts"`
	}
	if err := json.Unmarshal(raw, &oracle); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(directory, "*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != len(oracle.RecordCounts) {
		t.Fatalf("seed resource files=%d, expected %d", len(paths), len(oracle.RecordCounts))
	}
	builder := NewGeneratedRowBuilder("loom_dev_graph_rows_validation", "")
	for _, path := range paths {
		resourceType := strings.TrimSuffix(filepath.Base(path), ".ndjson")
		t.Run(resourceType, func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			scanner := bufio.NewScanner(file)
			count := 0
			for scanner.Scan() {
				count++
				if _, category, err := builder.Build(resourceType, scanner.Bytes(), map[string]float64{}); err != nil {
					t.Errorf("row %d production %s validation: %v", count, category, err)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			expected, known := oracle.RecordCounts[resourceType]
			if !known || count != expected {
				t.Errorf("%s source rows=%d, expected %d (known=%v)", resourceType, count, expected, known)
			}
		})
	}
}
