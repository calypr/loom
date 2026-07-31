package ingest

import (
	"encoding/json"
	"maps"
	"time"

	"github.com/bmeg/jsonschema/v6"
	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/bytedance/sonic"
)

type rowErrorType string

const (
	rowErrorValidation rowErrorType = "validation"
	rowErrorGeneration rowErrorType = "generation"
	rowErrorEdge       rowErrorType = "edge"
)

type rowBuildResult struct {
	vertex  json.RawMessage
	edges   []json.RawMessage
	payload map[string]any
}

type RowBuilder interface {
	Build(resourceType string, line []byte, stageSeconds map[string]float64) (rowBuildResult, rowErrorType, error)
}

type GeneratedRowBuilder struct {
	project          string
	authResourcePath string
}

func NewGeneratedRowBuilder(project, authResourcePath string) *GeneratedRowBuilder {
	return &GeneratedRowBuilder{project: project, authResourcePath: authResourcePath}
}

func (b *GeneratedRowBuilder) Build(resourceType string, line []byte, stageSeconds map[string]float64) (rowBuildResult, rowErrorType, error) {
	vDoc, eDocs, errorType, err := loadRowGenerated(resourceType, line, b.project, stageSeconds)
	if err != nil {
		return rowBuildResult{}, errorType, err
	}
	if b.authResourcePath != "" {
		vDoc.AuthResourcePath = b.authResourcePath
		for i := range eDocs {
			eDocs[i], err = edgeWithAuthResourcePath(eDocs[i], b.authResourcePath)
			if err != nil {
				return rowBuildResult{}, rowErrorEdge, err
			}
		}
	}
	marshalStart := time.Now()
	vBytes, err := sonic.ConfigFastest.Marshal(&vDoc)
	stageSeconds["vertex_marshal"] += time.Since(marshalStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorValidation, err
	}
	var payload map[string]any
	decodeStart := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, &payload); err != nil {
		stageSeconds["payload_map_decode"] += time.Since(decodeStart).Seconds()
		return rowBuildResult{}, rowErrorValidation, err
	}
	stageSeconds["payload_map_decode"] += time.Since(decodeStart).Seconds()
	return rowBuildResult{
		vertex:  json.RawMessage(vBytes),
		edges:   eDocs,
		payload: payload,
	}, "", nil
}

func edgeWithAuthResourcePath(edge json.RawMessage, authResourcePath string) (json.RawMessage, error) {
	if authResourcePath == "" {
		return edge, nil
	}
	var doc map[string]any
	if err := sonic.ConfigFastest.Unmarshal(edge, &doc); err != nil {
		return nil, err
	}
	doc["auth_resource_path"] = authResourcePath
	out, err := sonic.ConfigFastest.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(out), nil
}

type GenericRowBuilder struct {
	project   string
	class     *jsonschema.Schema
	schema    *graph.GraphSchema
	extraArgs map[string]any
}

func NewGenericRowBuilder(project string, class *jsonschema.Schema, schema *graph.GraphSchema, extraArgs map[string]any) *GenericRowBuilder {
	return &GenericRowBuilder{
		project:   project,
		class:     class,
		schema:    schema,
		extraArgs: extraArgs,
	}
}

func (b *GenericRowBuilder) Build(resourceType string, line []byte, stageSeconds map[string]float64) (rowBuildResult, rowErrorType, error) {
	// jsonschemagraph currently deletes its optional namespace entry from the
	// extra-arguments map. Generic builders are shared by parallel ingest
	// workers, so each row must give that dependency an isolated mutable map.
	extraArgs := maps.Clone(b.extraArgs)

	var payload map[string]any
	decodeStart := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, &payload); err != nil {
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()
		return rowBuildResult{}, rowErrorValidation, err
	}
	stageSeconds["decode"] += time.Since(decodeStart).Seconds()

	validateStart := time.Now()
	if err := b.class.ValidateFast(payload); err != nil {
		stageSeconds["validate"] += time.Since(validateStart).Seconds()
		return rowBuildResult{}, rowErrorValidation, err
	}
	stageSeconds["validate"] += time.Since(validateStart).Seconds()

	vertexStart := time.Now()
	vDoc, err := VertexFromFHIRWithExtra(b.project, resourceType, payload, extraArgs)
	stageSeconds["vertex_build"] += time.Since(vertexStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorValidation, err
	}

	edgeStart := time.Now()
	objectIDStart := time.Now()
	objectID, err := graphObjectID(payload, b.class)
	stageSeconds["object_id"] += time.Since(objectIDStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorGeneration, err
	}
	gripEdges, err := b.schema.BuildEdgesWithID(resourceType, objectID, payload, extraArgs, true)
	stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorGeneration, err
	}

	convertedEdges := make([]json.RawMessage, 0, len(gripEdges))
	authResourcePath, _ := b.extraArgs["auth_resource_path"].(string)
	for _, generatedEdge := range gripEdges {
		edge, err := EdgeFromGrip(b.project, resourceType, generatedEdge)
		if err != nil {
			return rowBuildResult{}, rowErrorEdge, err
		}
		marshalStart := time.Now()
		eBytes, err := sonic.ConfigFastest.Marshal(&edge)
		stageSeconds["edge_marshal"] += time.Since(marshalStart).Seconds()
		if err != nil {
			return rowBuildResult{}, rowErrorEdge, err
		}
		if authResourcePath != "" {
			eBytes, err = edgeWithAuthResourcePath(eBytes, authResourcePath)
			if err != nil {
				return rowBuildResult{}, rowErrorEdge, err
			}
		}
		convertedEdges = append(convertedEdges, json.RawMessage(eBytes))
	}

	marshalStart := time.Now()
	vBytes, err := sonic.ConfigFastest.Marshal(&vDoc)
	stageSeconds["vertex_marshal"] += time.Since(marshalStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorValidation, err
	}

	return rowBuildResult{
		vertex:  json.RawMessage(vBytes),
		edges:   convertedEdges,
		payload: payload,
	}, "", nil
}
