package ingest

import (
	"encoding/json"
	"time"

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
	return b.build(resourceType, line, stageSeconds, "")
}

func (b *GeneratedRowBuilder) build(resourceType string, line []byte, stageSeconds map[string]float64, datasetGeneration string) (rowBuildResult, rowErrorType, error) {
	vDoc, eDocs, payload, errorType, err := loadRowGeneratedWithPayload(resourceType, line, b.project, stageSeconds)
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
	var document any = &vDoc
	if datasetGeneration != "" {
		logicalKey := vDoc.Key
		vDoc.Key = generationDocumentKey(b.project, datasetGeneration, resourceType, logicalKey)
		document = &struct {
			VertexDocument
			LogicalKey        string `json:"logical_key"`
			DatasetGeneration string `json:"dataset_generation"`
		}{vDoc, logicalKey, datasetGeneration}
	}
	vBytes, err := sonic.ConfigFastest.Marshal(document)
	stageSeconds["vertex_marshal"] += time.Since(marshalStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorValidation, err
	}
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
