package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// generationIdentityField is deliberately distinct from the resource payload
// and from the loader's error-generation counters. It identifies the immutable
// Loom dataset generation that owns a persisted graph document.
const generationIdentityField = "dataset_generation"

// logicalKeyField preserves the pre-generation Arango key for operational
// diagnostics and future stable external-ID policy. It is not used for graph
// traversal: _key, _from, and _to are all generation-qualified below.
const logicalKeyField = "logical_key"

// generationRowBuilder is the one identity boundary shared by the generated
// and generic FHIR row builders. Keeping it here avoids a second FHIR model or
// a generator-wide change: both builders first produce the established Loom
// vertex/edge representation, then this wrapper qualifies its physical graph
// identity for one immutable dataset generation.
//
// It is intentionally not selected by Load until the dataset lifecycle owns
// manifest creation and active-generation reads. Writing qualified identities
// without generation-qualified readers would be an incomplete migration.
type generationRowBuilder struct {
	delegate          RowBuilder
	project           string
	datasetGeneration string
}

func newGenerationRowBuilder(delegate RowBuilder, project, datasetGeneration string) (*generationRowBuilder, error) {
	if delegate == nil {
		return nil, fmt.Errorf("generation row builder requires a delegate")
	}
	project = strings.TrimSpace(project)
	datasetGeneration = strings.TrimSpace(datasetGeneration)
	if project == "" {
		return nil, fmt.Errorf("generation row builder requires a project")
	}
	if datasetGeneration == "" {
		return nil, fmt.Errorf("generation row builder requires a dataset generation")
	}
	return &generationRowBuilder{
		delegate:          delegate,
		project:           project,
		datasetGeneration: datasetGeneration,
	}, nil
}

func (b *generationRowBuilder) Build(resourceType string, line []byte, stageSeconds map[string]float64) (rowBuildResult, rowErrorType, error) {
	result, errType, err := b.delegate.Build(resourceType, line, stageSeconds)
	if err != nil {
		return rowBuildResult{}, errType, err
	}
	namespaced, err := namespaceRowBuildResult(result, b.project, b.datasetGeneration, resourceType)
	if err != nil {
		return rowBuildResult{}, rowErrorGeneration, err
	}
	return namespaced, "", nil
}

// namespaceRowBuildResult rewrites one generated or generic result into a
// generation-isolated graph identity. The original logical FHIR ID remains in
// the existing top-level id field; logical_key captures the original Arango
// key. Hashing is necessary because keys are global to a collection, while
// both resource IDs and generated edge IDs are otherwise reused by later
// project loads and dataset generations.
func namespaceRowBuildResult(result rowBuildResult, project, datasetGeneration, resourceType string) (rowBuildResult, error) {
	project = strings.TrimSpace(project)
	datasetGeneration = strings.TrimSpace(datasetGeneration)
	resourceType = strings.TrimSpace(resourceType)
	if project == "" || datasetGeneration == "" || resourceType == "" {
		return rowBuildResult{}, fmt.Errorf("namespace row result requires project, dataset generation, and resource type")
	}

	vertex, logicalVertexKey, err := namespaceVertexDocument(result.vertex, project, datasetGeneration, resourceType)
	if err != nil {
		return rowBuildResult{}, err
	}
	edges := make([]json.RawMessage, len(result.edges))
	for index, edge := range result.edges {
		namespaced, err := namespaceEdgeDocument(edge, project, datasetGeneration)
		if err != nil {
			return rowBuildResult{}, fmt.Errorf("namespace edge %d for %s/%s: %w", index, resourceType, logicalVertexKey, err)
		}
		edges[index] = namespaced
	}
	return rowBuildResult{
		vertex:  vertex,
		edges:   edges,
		payload: result.payload,
	}, nil
}

func namespaceVertexDocument(raw json.RawMessage, project, datasetGeneration, resourceType string) (json.RawMessage, string, error) {
	document, err := decodeTopLevelDocument(raw)
	if err != nil {
		return nil, "", err
	}
	logicalKey, err := requiredDocumentString(document, "_key")
	if err != nil {
		return nil, "", fmt.Errorf("vertex: %w", err)
	}
	if err := requireProject(document, project); err != nil {
		return nil, "", fmt.Errorf("vertex: %w", err)
	}
	document[logicalKeyField] = json.RawMessage(strconvQuote(logicalKey))
	document[generationIdentityField] = json.RawMessage(strconvQuote(datasetGeneration))
	document["_key"] = json.RawMessage(strconvQuote(generationDocumentKey(project, datasetGeneration, resourceType, logicalKey)))
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, "", fmt.Errorf("marshal namespaced vertex: %w", err)
	}
	return json.RawMessage(encoded), logicalKey, nil
}

func namespaceEdgeDocument(raw json.RawMessage, project, datasetGeneration string) (json.RawMessage, error) {
	document, err := decodeTopLevelDocument(raw)
	if err != nil {
		return nil, err
	}
	logicalKey, err := requiredDocumentString(document, "_key")
	if err != nil {
		return nil, fmt.Errorf("edge: %w", err)
	}
	if err := requireProject(document, project); err != nil {
		return nil, fmt.Errorf("edge: %w", err)
	}
	from, err := requiredDocumentString(document, "_from")
	if err != nil {
		return nil, fmt.Errorf("edge: %w", err)
	}
	to, err := requiredDocumentString(document, "_to")
	if err != nil {
		return nil, fmt.Errorf("edge: %w", err)
	}
	namespacedFrom, err := generationDocumentID(project, datasetGeneration, from)
	if err != nil {
		return nil, fmt.Errorf("edge _from: %w", err)
	}
	namespacedTo, err := generationDocumentID(project, datasetGeneration, to)
	if err != nil {
		return nil, fmt.Errorf("edge _to: %w", err)
	}
	document[logicalKeyField] = json.RawMessage(strconvQuote(logicalKey))
	document[generationIdentityField] = json.RawMessage(strconvQuote(datasetGeneration))
	document["_key"] = json.RawMessage(strconvQuote(generationEdgeKey(project, datasetGeneration, logicalKey, from, to)))
	document["_from"] = json.RawMessage(strconvQuote(namespacedFrom))
	document["_to"] = json.RawMessage(strconvQuote(namespacedTo))
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("marshal namespaced edge: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func decodeTopLevelDocument(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty document")
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}
	if document == nil {
		return nil, fmt.Errorf("document must be an object")
	}
	return document, nil
}

func requiredDocumentString(document map[string]json.RawMessage, field string) (string, error) {
	raw, ok := document[field]
	if !ok {
		return "", fmt.Errorf("missing %s", field)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s must be a non-empty string", field)
	}
	return value, nil
}

func requireProject(document map[string]json.RawMessage, project string) error {
	documentProject, err := requiredDocumentString(document, "project")
	if err != nil {
		return err
	}
	if documentProject != project {
		return fmt.Errorf("project %q does not match expected project %q", documentProject, project)
	}
	return nil
}

func generationDocumentID(project, datasetGeneration, documentID string) (string, error) {
	collection, logicalKey, ok := strings.Cut(documentID, "/")
	if !ok || strings.TrimSpace(collection) == "" || strings.TrimSpace(logicalKey) == "" || strings.Contains(logicalKey, "/") {
		return "", fmt.Errorf("document ID %q must be collection/key", documentID)
	}
	return collection + "/" + generationDocumentKey(project, datasetGeneration, collection, logicalKey), nil
}

func generationDocumentKey(project, datasetGeneration, collection, logicalKey string) string {
	return "g_" + generationHash("vertex", project, datasetGeneration, collection, logicalKey)
}

func generationEdgeKey(project, datasetGeneration, logicalKey, from, to string) string {
	return "g_" + generationHash("edge", project, datasetGeneration, logicalKey, from, to)
}

func generationHash(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
