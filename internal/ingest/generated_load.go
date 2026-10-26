package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	fhir "github.com/calypr/loom/generated/fhir"
)

type generatedLoadResource interface {
	fhir.ConcreteResource
	Validate() error
	ExtractEdges(string) ([]json.RawMessage, error)
}

func supportsGeneratedLoad(resourceType string) bool {
	resource, ok := fhir.NewConcreteResource(resourceType)
	if !ok {
		return false
	}
	_, ok = resource.(generatedLoadResource)
	return ok
}

func loadRowGenerated(resourceType string, line []byte, project string, stageSeconds map[string]float64) (VertexDocument, []json.RawMessage, rowErrorType, error) {
	vertex, edges, _, errorType, err := loadRowGeneratedInternal(resourceType, line, project, stageSeconds, false)
	return vertex, edges, errorType, err
}

func loadRowGeneratedWithPayload(resourceType string, line []byte, project string, stageSeconds map[string]float64) (VertexDocument, []json.RawMessage, map[string]any, rowErrorType, error) {
	return loadRowGeneratedInternal(resourceType, line, project, stageSeconds, true)
}

func loadRowGeneratedInternal(resourceType string, line []byte, project string, stageSeconds map[string]float64, includePayloadMap bool) (VertexDocument, []json.RawMessage, map[string]any, rowErrorType, error) {
	resource, ok := fhir.NewConcreteResource(resourceType)
	if !ok {
		return VertexDocument{}, nil, nil, rowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}
	value, ok := resource.(generatedLoadResource)
	if !ok {
		return VertexDocument{}, nil, nil, rowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}

	start := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, value); err != nil {
		stageSeconds["decode"] += time.Since(start).Seconds()
		return VertexDocument{}, nil, nil, rowErrorValidation, err
	}
	stageSeconds["decode"] += time.Since(start).Seconds()

	start = time.Now()
	if err := value.Validate(); err != nil {
		stageSeconds["validate"] += time.Since(start).Seconds()
		return VertexDocument{}, nil, nil, rowErrorValidation, err
	}
	stageSeconds["validate"] += time.Since(start).Seconds()

	start = time.Now()
	objectID := strings.TrimSpace(value.GetID())
	stageSeconds["object_id"] += time.Since(start).Seconds()
	if objectID == "" {
		return VertexDocument{}, nil, nil, rowErrorValidation, fmt.Errorf("%s payload missing string id", resourceType)
	}

	start = time.Now()
	edges, err := value.ExtractEdges(project)
	stageSeconds["edge_generation"] += time.Since(start).Seconds()
	if err != nil {
		return VertexDocument{}, nil, nil, rowErrorGeneration, err
	}

	var payload map[string]any
	var vertexPayload json.RawMessage
	if includePayloadMap {
		start = time.Now()
		payload, err = decodePayloadWithProjectID(line, project)
		stageSeconds["payload_map_decode"] += time.Since(start).Seconds()
		if err != nil {
			return VertexDocument{}, nil, nil, rowErrorValidation, err
		}
		vertexPayload = marshalPayloadWithFallback(payload, line)
	} else {
		vertexPayload = withProjectID(line, project)
	}

	return VertexDocument{
		Key:          SanitizeKey(objectID),
		ID:           objectID,
		Project:      project,
		ProjectID:    project,
		ResourceType: resourceType,
		Payload:      vertexPayload,
	}, edges, payload, "", nil
}

func decodePayloadWithProjectID(line []byte, project string) (map[string]any, error) {
	var payload map[string]any
	if err := sonic.ConfigFastest.Unmarshal(line, &payload); err != nil {
		return nil, err
	}
	payload["project_id"] = project
	return payload, nil
}

func marshalPayloadWithFallback(payload map[string]any, fallback []byte) json.RawMessage {
	encoded, err := sonic.ConfigFastest.Marshal(payload)
	if err != nil {
		return json.RawMessage(fallback)
	}
	return json.RawMessage(encoded)
}

func withProjectID(line []byte, project string) json.RawMessage {
	payload, err := decodePayloadWithProjectID(line, project)
	if err != nil {
		return json.RawMessage(line)
	}
	return marshalPayloadWithFallback(payload, line)
}
