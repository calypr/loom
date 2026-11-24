package row

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/ingest/source"

	"github.com/bytedance/sonic"
	fhir "github.com/calypr/loom/fhir/model"
)

func loadRowGeneratedDocumentsWithPayload(resourceType string, line []byte, project string, stageSeconds map[string]float64) (VertexDocument, []fhir.EdgeDocument, map[string]any, RowErrorType, error) {
	resource, ok := fhir.NewConcreteResource(resourceType)
	if !ok {
		return VertexDocument{}, nil, nil, RowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}
	value, ok := resource.(source.GeneratedLoadResource)
	if !ok {
		return VertexDocument{}, nil, nil, RowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}

	var payload map[string]any
	var vertexPayload json.RawMessage
	start := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, value); err != nil {
		normalized, normalizedPayload := normalizeFHIRInteger64(line, resourceType, project)
		if normalized == nil {
			stageSeconds["decode"] += time.Since(start).Seconds()
			return VertexDocument{}, nil, nil, RowErrorValidation, err
		}
		resource, _ := fhir.NewConcreteResource(resourceType)
		value = resource.(source.GeneratedLoadResource)
		if err := sonic.ConfigFastest.Unmarshal(normalized, value); err != nil {
			stageSeconds["decode"] += time.Since(start).Seconds()
			return VertexDocument{}, nil, nil, RowErrorValidation, err
		}
		vertexPayload = normalized
		payload = normalizedPayload
	}
	stageSeconds["decode"] += time.Since(start).Seconds()

	start = time.Now()
	err := value.Validate()
	stageSeconds["validate"] += time.Since(start).Seconds()
	if err != nil {
		return VertexDocument{}, nil, nil, RowErrorValidation, err
	}

	start = time.Now()
	objectID := strings.TrimSpace(value.GetID())
	stageSeconds["object_id"] += time.Since(start).Seconds()
	if objectID == "" {
		return VertexDocument{}, nil, nil, RowErrorValidation, fmt.Errorf("%s payload missing string id", resourceType)
	}

	start = time.Now()
	edges, err := value.ExtractEdgeDocuments(project)
	stageSeconds["edge_generation"] += time.Since(start).Seconds()
	if err != nil {
		return VertexDocument{}, nil, nil, RowErrorGeneration, err
	}

	if vertexPayload == nil {
		start = time.Now()
		payload, err = decodePayloadWithProjectID(line, project)
		stageSeconds["payload_map_decode"] += time.Since(start).Seconds()
		if err != nil {
			return VertexDocument{}, nil, nil, RowErrorValidation, err
		}
		vertexPayload = marshalPayloadWithFallback(payload, line)
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

func SupportsGeneratedLoad(resourceType string) bool {
	return source.SupportsGeneratedLoad(resourceType)
}
