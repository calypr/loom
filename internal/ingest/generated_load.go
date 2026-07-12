package ingest

import (
	"encoding/json"
	"fmt"
	"time"

	fhir "github.com/calypr/loom/fhirstructs"

	jsgarango "github.com/bmeg/jsonschemagraph/arango"
	"github.com/bytedance/sonic"
)

// supportsGeneratedLoad reports whether the optimized generated-loader
// dispatcher has a concrete fast-path case for this resource. The generated
// FHIR model can contain more types than this dispatcher; Load uses the
// generic schema-backed builder for active graph-schema roots outside the fast
// switch rather than rejecting an otherwise valid file.
func supportsGeneratedLoad(resourceType string) bool {
	switch resourceType {
	case "BodyStructure", "Condition", "DocumentReference", "Group", "ImagingStudy", "Medication", "MedicationAdministration", "Observation", "Organization", "Patient", "Practitioner", "ResearchStudy", "ResearchSubject", "Specimen":
		return true
	default:
		return false
	}
}

func loadRowGenerated(resourceType string, line []byte, project string, stageSeconds map[string]float64) (jsgarango.VertexDocument, []json.RawMessage, error) {
	switch resourceType {
	case "BodyStructure":
		var val fhir.BodyStructure
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Condition":
		var val fhir.Condition
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "DocumentReference":
		var val fhir.DocumentReference
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Group":
		var val fhir.Group
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "ImagingStudy":
		var val fhir.ImagingStudy
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Medication":
		var val fhir.Medication
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "MedicationAdministration":
		var val fhir.MedicationAdministration
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Observation":
		var val fhir.Observation
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Organization":
		var val fhir.Organization
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Patient":
		var val fhir.Patient
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Practitioner":
		var val fhir.Practitioner
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "ResearchStudy":
		var val fhir.ResearchStudy
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "ResearchSubject":
		var val fhir.ResearchSubject
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	case "Specimen":
		var val fhir.Specimen
		decodeStart := time.Now()
		if err := sonic.ConfigFastest.Unmarshal(line, &val); err != nil {
			stageSeconds["decode"] += time.Since(decodeStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()

		validateStart := time.Now()
		if err := val.Validate(); err != nil {
			stageSeconds["validate"] += time.Since(validateStart).Seconds()
			return jsgarango.VertexDocument{}, nil, err
		}
		stageSeconds["validate"] += time.Since(validateStart).Seconds()

		objectIDStart := time.Now()
		objectID := ""
		if val.ID != nil {
			objectID = *val.ID
		}
		stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()

		edgeStart := time.Now()
		edges, err := val.ExtractEdges(project)
		stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
		if err != nil {
			return jsgarango.VertexDocument{}, nil, err
		}

		vertex := jsgarango.VertexDocument{
			Key:          jsgarango.SanitizeKey(objectID),
			ID:           objectID,
			Project:      project,
			ResourceType: resourceType,
			Payload:      json.RawMessage(line),
		}
		return vertex, edges, nil

	default:
		return jsgarango.VertexDocument{}, nil, fmt.Errorf("unsupported resource type %s in generated path", resourceType)
	}
}
