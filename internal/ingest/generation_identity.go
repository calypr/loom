package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	publication "github.com/calypr/loom/internal/dataset"
)

const generationIdentityField = "dataset_generation"

const logicalKeyField = "logical_key"

type generationRowBuilder struct {
	delegate          RowBuilder
	generated         *GeneratedRowBuilder
	project           string
	datasetGeneration string
	identityFormat    string
}

func newGenerationRowBuilder(delegate RowBuilder, project, datasetGeneration string) (*generationRowBuilder, error) {
	return newGenerationRowBuilderWithFormat(delegate, project, datasetGeneration, publication.IdentityFormatCompact)
}

func newGenerationRowBuilderWithFormat(delegate RowBuilder, project, datasetGeneration, identityFormat string) (*generationRowBuilder, error) {
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
	identityFormat = normalizeIdentityFormat(identityFormat)
	generated, _ := delegate.(*GeneratedRowBuilder)
	if generated != nil && (generated.project != project || !utf8.ValidString(project)) {
		generated = nil
	}
	return &generationRowBuilder{
		generated:         generated,
		delegate:          delegate,
		project:           project,
		datasetGeneration: datasetGeneration,
		identityFormat:    identityFormat,
	}, nil
}

func (b *generationRowBuilder) Build(resourceType string, line []byte, stageSeconds map[string]float64) (rowBuildResult, rowErrorType, error) {
	return b.BuildWithSourceFile(resourceType, line, stageSeconds, "")
}

func (b *generationRowBuilder) BuildWithSourceFile(resourceType string, line []byte, stageSeconds map[string]float64, sourceFile string) (rowBuildResult, rowErrorType, error) {
	if b.generated != nil {
		result, errType, err := b.generated.build(resourceType, line, stageSeconds, b.datasetGeneration, sourceFile, b.identityFormat)
		if err != nil {
			return rowBuildResult{}, errType, err
		}
		return result, "", nil
	}
	result, errType, err := b.delegate.Build(resourceType, line, stageSeconds)
	if err != nil {
		return rowBuildResult{}, errType, err
	}
	namespaced, err := namespaceRowBuildResultWithFormat(result, b.project, b.datasetGeneration, resourceType, b.identityFormat)
	if err != nil {
		return rowBuildResult{}, rowErrorGeneration, err
	}
	if sourceFile != "" {
		namespaced.edges, err = sourceFileOwnerEdges(namespaced.edges, sourceFile)
		if err != nil {
			return rowBuildResult{}, rowErrorEdge, err
		}
	}
	return namespaced, "", nil
}

func namespaceRowBuildResult(result rowBuildResult, project, datasetGeneration, resourceType string) (rowBuildResult, error) {
	return namespaceRowBuildResultWithFormat(result, project, datasetGeneration, resourceType, publication.IdentityFormatCompact)
}

func namespaceRowBuildResultWithFormat(result rowBuildResult, project, datasetGeneration, resourceType, identityFormat string) (rowBuildResult, error) {
	project = strings.TrimSpace(project)
	datasetGeneration = strings.TrimSpace(datasetGeneration)
	resourceType = strings.TrimSpace(resourceType)
	if project == "" || datasetGeneration == "" || resourceType == "" {
		return rowBuildResult{}, fmt.Errorf("namespace row result requires project, dataset generation, and resource type")
	}

	vertex, logicalVertexKey, err := namespaceVertexDocumentWithFormat(result.vertex, project, datasetGeneration, resourceType, identityFormat)
	if err != nil {
		return rowBuildResult{}, err
	}
	edges, err := namespaceRowEdgesWithFormat(result.edges, project, datasetGeneration, identityFormat)
	if err != nil {
		return rowBuildResult{}, fmt.Errorf("namespace %s/%s: %w", resourceType, logicalVertexKey, err)
	}
	return rowBuildResult{vertex: vertex, edges: edges, payload: result.payload}, nil
}

func namespaceRowEdges(edges []json.RawMessage, project, datasetGeneration string) ([]json.RawMessage, error) {
	return namespaceRowEdgesWithFormat(edges, project, datasetGeneration, publication.IdentityFormatCompact)
}

func namespaceRowEdgesWithFormat(edges []json.RawMessage, project, datasetGeneration, identityFormat string) ([]json.RawMessage, error) {
	namespaced := make([]json.RawMessage, len(edges))
	for index, edge := range edges {
		document, err := namespaceEdgeDocumentWithFormat(edge, project, datasetGeneration, identityFormat)
		if err != nil {
			return nil, fmt.Errorf("edge %d: %w", index, err)
		}
		namespaced[index] = document
	}
	return namespaced, nil
}

func namespaceVertexDocument(raw json.RawMessage, project, datasetGeneration, resourceType string) (json.RawMessage, string, error) {
	return namespaceVertexDocumentWithFormat(raw, project, datasetGeneration, resourceType, publication.IdentityFormatCompact)
}

func namespaceVertexDocumentWithFormat(raw json.RawMessage, project, datasetGeneration, resourceType, identityFormat string) (json.RawMessage, string, error) {
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
	document["_key"] = json.RawMessage(strconvQuote(generationDocumentKeyWithFormat(project, datasetGeneration, resourceType, logicalKey, identityFormat)))
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, "", fmt.Errorf("marshal namespaced vertex: %w", err)
	}
	return json.RawMessage(encoded), logicalKey, nil
}

func namespaceEdgeDocument(raw json.RawMessage, project, datasetGeneration string) (json.RawMessage, error) {
	return namespaceEdgeDocumentWithFormat(raw, project, datasetGeneration, publication.IdentityFormatCompact)
}

func namespaceEdgeDocumentWithFormat(raw json.RawMessage, project, datasetGeneration, identityFormat string) (json.RawMessage, error) {
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
	namespacedFrom, err := generationDocumentIDWithFormat(project, datasetGeneration, from, identityFormat)
	if err != nil {
		return nil, fmt.Errorf("edge _from: %w", err)
	}
	namespacedTo, err := generationDocumentIDWithFormat(project, datasetGeneration, to, identityFormat)
	if err != nil {
		return nil, fmt.Errorf("edge _to: %w", err)
	}
	document[logicalKeyField] = json.RawMessage(strconvQuote(logicalKey))

	document["logical_from"] = json.RawMessage(strconvQuote(from))
	document["logical_to"] = json.RawMessage(strconvQuote(to))
	document[generationIdentityField] = json.RawMessage(strconvQuote(datasetGeneration))
	document["_key"] = json.RawMessage(strconvQuote(generationEdgeKeyWithFormat(project, datasetGeneration, logicalKey, from, to, identityFormat)))
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
	return generationDocumentIDWithFormat(project, datasetGeneration, documentID, publication.IdentityFormatCompact)
}

func generationDocumentIDWithFormat(project, datasetGeneration, documentID, identityFormat string) (string, error) {
	collection, logicalKey, ok := strings.Cut(documentID, "/")
	if !ok || strings.TrimSpace(collection) == "" || strings.TrimSpace(logicalKey) == "" || strings.Contains(logicalKey, "/") {
		return "", fmt.Errorf("document ID %q must be collection/key", documentID)
	}
	return collection + "/" + generationDocumentKeyWithFormat(project, datasetGeneration, collection, logicalKey, identityFormat), nil
}

func generationDocumentKey(project, datasetGeneration, collection, logicalKey string) string {
	return generationDocumentKeyWithFormat(project, datasetGeneration, collection, logicalKey, publication.IdentityFormatCompact)
}

func generationEdgeKey(project, datasetGeneration, logicalKey, from, to string) string {
	return generationEdgeKeyWithFormat(project, datasetGeneration, logicalKey, from, to, publication.IdentityFormatCompact)
}

func generationDocumentKeyWithFormat(project, datasetGeneration, collection, logicalKey, format string) string {
	format = normalizeIdentityFormat(format)
	if format == publication.IdentityFormatLegacy {
		return "g_" + generationHash("vertex", project, datasetGeneration, collection, logicalKey, "", 5, 64)
	}
	return "g2" + generationHash("vertex", project, datasetGeneration, collection, logicalKey, "", 5, 32)
}

func generationEdgeKeyWithFormat(project, datasetGeneration, logicalKey, from, to, format string) string {
	format = normalizeIdentityFormat(format)
	if format == publication.IdentityFormatLegacy {
		return "g_" + generationHash("edge", project, datasetGeneration, logicalKey, from, to, 6, 64)
	}
	return "g2" + generationHash("edge", project, datasetGeneration, logicalKey, from, to, 6, 32)
}

func normalizeIdentityFormat(format string) string {
	if format == "" {
		return publication.IdentityFormatLegacy
	}
	return format
}

func generationHash(first, second, third, fourth, fifth, sixth string, partCount, hexLength int) string {
	hash := sha256.New()
	parts := [...]string{first, second, third, fourth, fifth, sixth}
	for index := 0; index < partCount; index++ {
		_, _ = hash.Write([]byte(parts[index]))
		_, _ = hash.Write([]byte{0})
	}
	var digest [sha256.Size]byte
	var encoded [sha256.Size * 2]byte
	encodedHash := hash.Sum(digest[:0])
	hex.Encode(encoded[:], encodedHash)
	return string(encoded[:hexLength])
}

func strconvQuote(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
