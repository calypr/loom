package ingest

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	fhir "github.com/calypr/loom/fhir/model"
)

type generatedLoadResource interface {
	fhir.ConcreteResource
	Validate() error
	ExtractEdgeDocuments(string) ([]fhir.EdgeDocument, error)
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
	vertex, documents, _, errorType, err := loadRowGeneratedInternal(resourceType, line, project, stageSeconds, false)
	if err != nil {
		return VertexDocument{}, nil, errorType, err
	}
	edges, err := marshalEdgeDocuments(documents)
	return vertex, edges, errorType, err
}

func loadRowGeneratedWithPayload(resourceType string, line []byte, project string, stageSeconds map[string]float64) (VertexDocument, []json.RawMessage, map[string]any, rowErrorType, error) {
	vertex, documents, payload, errorType, err := loadRowGeneratedInternal(resourceType, line, project, stageSeconds, true)
	if err != nil {
		return VertexDocument{}, nil, nil, errorType, err
	}
	edges, err := marshalEdgeDocuments(documents)
	return vertex, edges, payload, errorType, err
}

func loadRowGeneratedDocumentsWithPayload(resourceType string, line []byte, project string, stageSeconds map[string]float64) (VertexDocument, []fhir.EdgeDocument, map[string]any, rowErrorType, error) {
	return loadRowGeneratedInternal(resourceType, line, project, stageSeconds, true)
}

func marshalEdgeDocuments(documents []fhir.EdgeDocument) ([]json.RawMessage, error) {
	edges := make([]json.RawMessage, len(documents))
	for index, document := range documents {
		edges[index] = json.RawMessage(fmt.Sprintf(
			`{"_key":%q,"_from":%q,"_to":%q,"label":%q,"project":%s,"from_type":%q,"to_type":%q}`,
			document.Key,
			document.From,
			document.To,
			document.Label,
			strconv.Quote(document.Project),
			document.FromType,
			document.ToType,
		))
	}
	return edges, nil
}

func loadRowGeneratedInternal(resourceType string, line []byte, project string, stageSeconds map[string]float64, includePayloadMap bool) (VertexDocument, []fhir.EdgeDocument, map[string]any, rowErrorType, error) {
	resource, ok := fhir.NewConcreteResource(resourceType)
	if !ok {
		return VertexDocument{}, nil, nil, rowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}
	value, ok := resource.(generatedLoadResource)
	if !ok {
		return VertexDocument{}, nil, nil, rowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}

	var payload map[string]any
	var vertexPayload json.RawMessage
	start := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, value); err != nil {
		normalized, normalizedPayload := normalizeFHIRInteger64(line, resourceType, project)
		if normalized == nil {
			stageSeconds["decode"] += time.Since(start).Seconds()
			return VertexDocument{}, nil, nil, rowErrorValidation, err
		}
		resource, _ := fhir.NewConcreteResource(resourceType)
		value = resource.(generatedLoadResource)
		if err := sonic.ConfigFastest.Unmarshal(normalized, value); err != nil {
			stageSeconds["decode"] += time.Since(start).Seconds()
			return VertexDocument{}, nil, nil, rowErrorValidation, err
		}
		vertexPayload = normalized
		if includePayloadMap {
			payload = normalizedPayload
		}
	}
	stageSeconds["decode"] += time.Since(start).Seconds()

	start = time.Now()
	err := value.Validate()
	stageSeconds["validate"] += time.Since(start).Seconds()
	if err != nil {
		return VertexDocument{}, nil, nil, rowErrorValidation, err
	}

	start = time.Now()
	objectID := strings.TrimSpace(value.GetID())
	stageSeconds["object_id"] += time.Since(start).Seconds()
	if objectID == "" {
		return VertexDocument{}, nil, nil, rowErrorValidation, fmt.Errorf("%s payload missing string id", resourceType)
	}

	start = time.Now()
	edges, err := value.ExtractEdgeDocuments(project)
	stageSeconds["edge_generation"] += time.Since(start).Seconds()
	if err != nil {
		return VertexDocument{}, nil, nil, rowErrorGeneration, err
	}

	if vertexPayload == nil {
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
