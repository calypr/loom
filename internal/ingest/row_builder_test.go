package ingest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/bmeg/jsonschemagraph/graph"
)

func TestEdgeWithAuthResourcePathFailsClosed(t *testing.T) {
	if _, err := edgeWithAuthResourcePath(json.RawMessage(`{`), "projects/p"); err == nil {
		t.Fatal("malformed edge was returned without its authorization path")
	}
}

func TestGenericRowBuilderIsSafeForParallelWorkers(t *testing.T) {
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	const resourceType = "SubstanceDefinition"
	class := schema.GetClass(resourceType)
	if class == nil {
		t.Fatalf("class %q not found", resourceType)
	}
	extraArgs := map[string]any{"auth_resource_path": "HTAN_INT-BForePC"}
	builder := NewGenericRowBuilder("HTAN_INT-BForePC", class, schema, extraArgs)
	line := []byte(`{"resourceType":"SubstanceDefinition","id":"substance-1","name":[{"name":"Gemcitabine"}]}`)

	const workers = 32
	errs := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, _, buildErr := builder.Build(resourceType, line, map[string]float64{})
			if buildErr == nil {
				if got := result.payload["project_id"]; got != "HTAN_INT-BForePC" {
					buildErr = fmt.Errorf("payload project_id = %#v", got)
				}
			}
			errs <- buildErr
		}()
	}
	wait.Wait()
	close(errs)
	for buildErr := range errs {
		if buildErr != nil {
			t.Fatal(buildErr)
		}
	}
	if got := extraArgs["auth_resource_path"]; got != "HTAN_INT-BForePC" {
		t.Fatalf("shared extra arguments were mutated: %#v", extraArgs)
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

	generationBuilder, err := newGenerationRowBuilder(builder, project, "generation-1")
	if err != nil {
		t.Fatalf("newGenerationRowBuilder(): %v", err)
	}
	namespaced, errorType, err := generationBuilder.Build("ResearchSubject", line, map[string]float64{})
	if err != nil {
		t.Fatalf("generationRowBuilder.Build(): kind=%q err=%v", errorType, err)
	}
	var namespacedVertex map[string]json.RawMessage
	if err := json.Unmarshal(namespaced.vertex, &namespacedVertex); err != nil {
		t.Fatalf("decode namespaced vertex: %v", err)
	}
	if got := documentString(t, namespacedVertex, logicalKeyField); got != "research-subject-1" {
		t.Fatalf("namespaced vertex logical key = %q, want research-subject-1", got)
	}
	if got := documentString(t, namespacedVertex, generationIdentityField); got != "generation-1" {
		t.Fatalf("namespaced vertex generation = %q, want generation-1", got)
	}
	if got := documentString(t, namespacedVertex, "auth_resource_path"); got != authResourcePath {
		t.Fatalf("namespaced vertex auth_resource_path = %q, want %q", got, authResourcePath)
	}
	if len(namespaced.edges) != len(result.edges) {
		t.Fatalf("namespaced edge count = %d, want %d", len(namespaced.edges), len(result.edges))
	}
	if namespaced.payload["project_id"] != project || namespaced.payload["unknown_payload_field"] == nil {
		t.Fatalf("namespaced profiler payload changed: %#v", namespaced.payload)
	}
}
