package ingest

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/bytedance/sonic"
	"github.com/calypr/loom/fhirstructs"
	jsgarango "github.com/calypr/loom/internal/graphstore"
)

var generatedLoadTypes = map[string]struct{}{
	"BodyStructure": {}, "Condition": {}, "DocumentReference": {}, "Group": {},
	"ImagingStudy": {}, "Medication": {}, "MedicationAdministration": {},
	"Observation": {}, "Organization": {}, "Patient": {}, "Practitioner": {},
	"ResearchStudy": {}, "ResearchSubject": {}, "Specimen": {},
}

type generatedLoadResource interface {
	fhirstructs.ConcreteResource
	Validate() error
	ExtractEdges(string) ([]json.RawMessage, error)
}

func supportsGeneratedLoad(resourceType string) bool {
	_, ok := generatedLoadTypes[resourceType]
	return ok
}

func loadRowGenerated(resourceType string, line []byte, project string, stageSeconds map[string]float64) (jsgarango.VertexDocument, []json.RawMessage, rowErrorType, error) {
	if !supportsGeneratedLoad(resourceType) {
		return jsgarango.VertexDocument{}, nil, rowErrorGeneration, fmt.Errorf("generated loader does not support %q", resourceType)
	}
	resource, ok := fhirstructs.NewConcreteResource(resourceType)
	if !ok {
		return jsgarango.VertexDocument{}, nil, rowErrorGeneration, fmt.Errorf("unknown FHIR resource %q", resourceType)
	}
	value, ok := resource.(generatedLoadResource)
	if !ok {
		return jsgarango.VertexDocument{}, nil, rowErrorGeneration, fmt.Errorf("FHIR resource %q lacks generated validation or edge extraction", resourceType)
	}

	start := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, value); err != nil {
		stageSeconds["decode"] += time.Since(start).Seconds()
		return jsgarango.VertexDocument{}, nil, rowErrorValidation, err
	}
	stageSeconds["decode"] += time.Since(start).Seconds()

	start = time.Now()
	if err := value.Validate(); err != nil {
		stageSeconds["validate"] += time.Since(start).Seconds()
		return jsgarango.VertexDocument{}, nil, rowErrorValidation, err
	}
	stageSeconds["validate"] += time.Since(start).Seconds()

	start = time.Now()
	objectID := value.GetID()
	stageSeconds["object_id"] += time.Since(start).Seconds()

	start = time.Now()
	edges, err := value.ExtractEdges(project)
	stageSeconds["edge_generation"] += time.Since(start).Seconds()
	if err != nil {
		return jsgarango.VertexDocument{}, nil, rowErrorGeneration, err
	}

	return jsgarango.VertexDocument{
		Key:          jsgarango.SanitizeKey(objectID),
		ID:           objectID,
		Project:      project,
		ResourceType: resourceType,
		Payload:      json.RawMessage(line),
	}, edges, "", nil
}
