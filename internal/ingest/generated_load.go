package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	fhir "github.com/calypr/loom/generated/fhir"
)

var generatedLoadTypes = map[string]struct{}{
	"BodyStructure": {}, "Condition": {}, "DocumentReference": {}, "Group": {},
	"ImagingStudy": {}, "Medication": {}, "MedicationAdministration": {},
	"Observation": {}, "Organization": {}, "Patient": {}, "Practitioner": {},
	"ResearchStudy": {}, "ResearchSubject": {}, "Specimen": {},
}

type generatedLoadResource interface {
	fhir.ConcreteResource
	Validate() error
	ExtractEdges(string) ([]json.RawMessage, error)
}

func supportsGeneratedLoad(resourceType string) bool {
	_, ok := generatedLoadTypes[resourceType]
	return ok
}

func loadRowGenerated(resourceType string, line []byte, project string, stageSeconds map[string]float64) (VertexDocument, []json.RawMessage, rowErrorType, error) {
	if !supportsGeneratedLoad(resourceType) {
		return VertexDocument{}, nil, rowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}
	resource, ok := fhir.NewConcreteResource(resourceType)
	if !ok {
		return VertexDocument{}, nil, rowErrorGeneration, fmt.Errorf("unknown FHIR resource %q", resourceType)
	}
	value, ok := resource.(generatedLoadResource)
	if !ok {
		return VertexDocument{}, nil, rowErrorGeneration, fmt.Errorf("FHIR resource %q lacks generated validation or edge extraction", resourceType)
	}

	start := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, value); err != nil {
		stageSeconds["decode"] += time.Since(start).Seconds()
		return VertexDocument{}, nil, rowErrorValidation, err
	}
	stageSeconds["decode"] += time.Since(start).Seconds()

	start = time.Now()
	if err := value.Validate(); err != nil {
		stageSeconds["validate"] += time.Since(start).Seconds()
		return VertexDocument{}, nil, rowErrorValidation, err
	}
	stageSeconds["validate"] += time.Since(start).Seconds()

	start = time.Now()
	objectID := strings.TrimSpace(value.GetID())
	stageSeconds["object_id"] += time.Since(start).Seconds()
	if objectID == "" {
		return VertexDocument{}, nil, rowErrorValidation, fmt.Errorf("%s payload missing string id", resourceType)
	}

	start = time.Now()
	edges, err := value.ExtractEdges(project)
	stageSeconds["edge_generation"] += time.Since(start).Seconds()
	if err != nil {
		return VertexDocument{}, nil, rowErrorGeneration, err
	}

	return VertexDocument{
		Key:          SanitizeKey(objectID),
		ID:           objectID,
		Project:      project,
		ResourceType: resourceType,
		Payload:      json.RawMessage(line),
	}, edges, "", nil
}
