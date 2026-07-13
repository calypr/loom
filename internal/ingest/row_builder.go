package ingest

import (
	"encoding/json"
	"maps"
	"time"

	"github.com/bmeg/grip/gripql"
	"github.com/bmeg/jsonschema/v6"
	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/bytedance/sonic"
	jsgarango "github.com/calypr/loom/internal/graphstore"
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
	vDoc, eDocs, err := loadRowGenerated(resourceType, line, b.project, stageSeconds)
	if err != nil {
		return rowBuildResult{}, rowErrorValidation, err
	}
	if b.authResourcePath != "" {
		vDoc.AuthResourcePath = b.authResourcePath
		for i := range eDocs {
			eDocs[i] = edgeWithAuthResourcePath(eDocs[i], b.authResourcePath)
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

func edgeWithAuthResourcePath(edge json.RawMessage, authResourcePath string) json.RawMessage {
	if authResourcePath == "" {
		return edge
	}
	var doc map[string]any
	if err := sonic.ConfigFastest.Unmarshal(edge, &doc); err != nil {
		return edge
	}
	doc["auth_resource_path"] = authResourcePath
	out, err := sonic.ConfigFastest.Marshal(doc)
	if err != nil {
		return edge
	}
	return json.RawMessage(out)
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
	var payload map[string]any
	decodeStart := time.Now()
	if err := sonic.ConfigFastest.Unmarshal(line, &payload); err != nil {
		stageSeconds["decode"] += time.Since(decodeStart).Seconds()
		return rowBuildResult{}, rowErrorValidation, err
	}
	stageSeconds["decode"] += time.Since(decodeStart).Seconds()

	validateStart := time.Now()
	if err := b.class.Validate(payload); err != nil {
		stageSeconds["validate"] += time.Since(validateStart).Seconds()
		return rowBuildResult{}, rowErrorValidation, err
	}
	stageSeconds["validate"] += time.Since(validateStart).Seconds()

	vertexStart := time.Now()
	vDoc, err := jsgarango.VertexFromFHIRWithExtra(b.project, resourceType, payload, b.extraArgs)
	stageSeconds["vertex_build"] += time.Since(vertexStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorValidation, err
	}

	edgeStart := time.Now()
	// Generate is the public jsonschemagraph API. The repository's historical
	// BuildEdgesWithID helper is not part of the published module, so select the
	// edge elements from Generate while preserving the same edge conversion and
	// authorization propagation below.
	elements, err := b.schema.Generate(resourceType, payload, maps.Clone(b.extraArgs))
	stageSeconds["edge_generation"] += time.Since(edgeStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorGeneration, err
	}

	gripEdges := make([]*gripql.Edge, 0, len(elements))
	for _, element := range elements {
		if element != nil && element.Edge != nil {
			gripEdges = append(gripEdges, element.Edge)
		}
	}
	convertedEdges := make([]json.RawMessage, 0, len(gripEdges))
	authResourcePath, _ := b.extraArgs["auth_resource_path"].(string)
	for _, generatedEdge := range gripEdges {
		edge, err := jsgarango.EdgeFromGrip(b.project, resourceType, generatedEdge)
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
			eBytes = edgeWithAuthResourcePath(eBytes, authResourcePath)
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
