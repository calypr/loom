package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	fhir "github.com/calypr/loom/fhir/model"
	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type rowErrorType string

const (
	rowErrorValidation rowErrorType = "validation"
	rowErrorGeneration rowErrorType = "generation"
	rowErrorEdge       rowErrorType = "edge"
)

type rowBuildResult struct {
	vertex        json.RawMessage
	edges         []json.RawMessage
	payload       map[string]any
	relationships []catalog.RelationshipKey
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
	return b.BuildWithSourceFile(resourceType, line, stageSeconds, "")
}

func (b *GeneratedRowBuilder) BuildWithSourceFile(resourceType string, line []byte, stageSeconds map[string]float64, sourceFile string) (rowBuildResult, rowErrorType, error) {
	return b.build(resourceType, line, stageSeconds, "", sourceFile)
}

func (b *GeneratedRowBuilder) build(resourceType string, line []byte, stageSeconds map[string]float64, datasetGeneration, sourceFile string) (rowBuildResult, rowErrorType, error) {
	vDoc, eDocs, payload, errorType, err := loadRowGeneratedDocumentsWithPayload(resourceType, line, b.project, stageSeconds)
	if err != nil {
		return rowBuildResult{}, errorType, err
	}
	if b.authResourcePath != "" {
		vDoc.AuthResourcePath = b.authResourcePath
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
	edgeStart := time.Now()
	edgeBytes, relationships, err := marshalEdgeRows(eDocs, b.project, b.authResourcePath, datasetGeneration, sourceFile)
	stageSeconds["edge_marshal"] += time.Since(edgeStart).Seconds()
	if err != nil {
		return rowBuildResult{}, rowErrorEdge, err
	}
	return rowBuildResult{
		vertex:        json.RawMessage(vBytes),
		edges:         edgeBytes,
		payload:       payload,
		relationships: relationships,
	}, "", nil
}

type edgeRow struct {
	fhir.EdgeDocument
	AuthResourcePath  string `json:"auth_resource_path,omitempty"`
	DatasetGeneration string `json:"dataset_generation,omitempty"`
	LogicalKey        string `json:"logical_key,omitempty"`
	LogicalFrom       string `json:"logical_from,omitempty"`
	LogicalTo         string `json:"logical_to,omitempty"`
	SourceFile        string `json:"source_file,omitempty"`
}

func marshalEdgeRows(documents []fhir.EdgeDocument, project, authResourcePath, datasetGeneration, sourceFile string) ([]json.RawMessage, []catalog.RelationshipKey, error) {
	encoded := make([]json.RawMessage, len(documents))
	relationships := make([]catalog.RelationshipKey, 0, len(documents))
	for index, document := range documents {
		row, relationship, err := namespaceEdgeDocumentValue(document, project, authResourcePath, datasetGeneration, sourceFile)
		if err != nil {
			return nil, nil, fmt.Errorf("edge %d: %w", index, err)
		}
		data, err := sonic.ConfigFastest.Marshal(row)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal edge %d: %w", index, err)
		}
		encoded[index] = data
		relationships = append(relationships, relationship)
	}
	return encoded, relationships, nil
}

func namespaceEdgeDocumentValue(document fhir.EdgeDocument, project, authResourcePath, datasetGeneration, sourceFile string) (edgeRow, catalog.RelationshipKey, error) {
	if strings.TrimSpace(document.Key) == "" || strings.TrimSpace(document.From) == "" || strings.TrimSpace(document.To) == "" {
		return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("edge requires non-empty _key, _from, and _to")
	}
	if document.Project != project {
		return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("edge project %q does not match expected project %q", document.Project, project)
	}
	if _, _, ok := strings.Cut(document.From, "/"); !ok || strings.Contains(strings.SplitN(document.From, "/", 2)[1], "/") {
		return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("edge _from %q must be collection/key", document.From)
	}
	if _, _, ok := strings.Cut(document.To, "/"); !ok || strings.Contains(strings.SplitN(document.To, "/", 2)[1], "/") {
		return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("edge _to %q must be collection/key", document.To)
	}
	row := edgeRow{EdgeDocument: document, AuthResourcePath: authResourcePath, SourceFile: sourceFile}
	if datasetGeneration != "" {
		logicalKey, from, to := document.Key, document.From, document.To
		namespacedFrom, err := generationDocumentID(project, datasetGeneration, from)
		if err != nil {
			return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("edge _from: %w", err)
		}
		namespacedTo, err := generationDocumentID(project, datasetGeneration, to)
		if err != nil {
			return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("edge _to: %w", err)
		}
		row.LogicalKey = logicalKey
		row.LogicalFrom = from
		row.LogicalTo = to
		row.DatasetGeneration = datasetGeneration
		row.Key = generationEdgeKey(project, datasetGeneration, logicalKey, from, to)
		row.From = namespacedFrom
		row.To = namespacedTo
	}
	fromType, ok := fhirschema.ConcreteResourceType(document.FromType)
	if !ok {
		return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("unsupported from_type %q", document.FromType)
	}
	toType, ok := fhirschema.ConcreteResourceType(document.ToType)
	if !ok {
		return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("unsupported to_type %q", document.ToType)
	}
	if document.Label == "" {
		return edgeRow{}, catalog.RelationshipKey{}, fmt.Errorf("edge label is empty")
	}
	return row, catalog.RelationshipKey{Project: project, DatasetGeneration: catalog.NormalizeDatasetGeneration(datasetGeneration), AuthResourcePath: authResourcePath, FromType: fromType, Label: document.Label, ToType: toType}, nil
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
