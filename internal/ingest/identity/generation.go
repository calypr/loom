package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	publication "github.com/calypr/loom/internal/dataset"
)

const GenerationIdentityField = "dataset_generation"

const LogicalKeyField = "logical_key"

func NamespaceVertexDocumentWithFormat(raw json.RawMessage, project, datasetGeneration, resourceType, identityFormat string) (json.RawMessage, string, error) {
	document, err := DecodeTopLevelDocument(raw)
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
	document[LogicalKeyField] = json.RawMessage(QuoteDocumentString(logicalKey))
	document[GenerationIdentityField] = json.RawMessage(QuoteDocumentString(datasetGeneration))
	document["_key"] = json.RawMessage(QuoteDocumentString(GenerationDocumentKeyWithFormat(project, datasetGeneration, resourceType, logicalKey, identityFormat)))
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, "", fmt.Errorf("marshal namespaced vertex: %w", err)
	}
	return json.RawMessage(encoded), logicalKey, nil
}

func NamespaceEdgeDocumentWithFormat(raw json.RawMessage, project, datasetGeneration, identityFormat string) (json.RawMessage, error) {
	document, err := DecodeTopLevelDocument(raw)
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
	namespacedFrom, err := GenerationDocumentIDWithFormat(project, datasetGeneration, from, identityFormat)
	if err != nil {
		return nil, fmt.Errorf("edge _from: %w", err)
	}
	namespacedTo, err := GenerationDocumentIDWithFormat(project, datasetGeneration, to, identityFormat)
	if err != nil {
		return nil, fmt.Errorf("edge _to: %w", err)
	}
	document[LogicalKeyField] = json.RawMessage(QuoteDocumentString(logicalKey))

	document["logical_from"] = json.RawMessage(QuoteDocumentString(from))
	document["logical_to"] = json.RawMessage(QuoteDocumentString(to))
	document[GenerationIdentityField] = json.RawMessage(QuoteDocumentString(datasetGeneration))
	document["_key"] = json.RawMessage(QuoteDocumentString(GenerationEdgeKeyWithFormat(project, datasetGeneration, logicalKey, from, to, identityFormat)))
	document["_from"] = json.RawMessage(QuoteDocumentString(namespacedFrom))
	document["_to"] = json.RawMessage(QuoteDocumentString(namespacedTo))
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("marshal namespaced edge: %w", err)
	}
	return json.RawMessage(encoded), nil
}

func DecodeTopLevelDocument(raw json.RawMessage) (map[string]json.RawMessage, error) {
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
	return GenerationDocumentIDWithFormat(project, datasetGeneration, documentID, publication.IdentityFormatCompact)
}

func GenerationDocumentID(project, datasetGeneration, documentID string) (string, error) {
	return generationDocumentID(project, datasetGeneration, documentID)
}

func GenerationDocumentIDWithFormat(project, datasetGeneration, documentID, identityFormat string) (string, error) {
	collection, logicalKey, ok := strings.Cut(documentID, "/")
	if !ok || strings.TrimSpace(collection) == "" || strings.TrimSpace(logicalKey) == "" || strings.Contains(logicalKey, "/") {
		return "", fmt.Errorf("document ID %q must be collection/key", documentID)
	}
	return collection + "/" + GenerationDocumentKeyWithFormat(project, datasetGeneration, collection, logicalKey, identityFormat), nil
}

func generationDocumentKey(project, datasetGeneration, collection, logicalKey string) string {
	return GenerationDocumentKeyWithFormat(project, datasetGeneration, collection, logicalKey, publication.IdentityFormatCompact)
}

func generationEdgeKey(project, datasetGeneration, logicalKey, from, to string) string {
	return GenerationEdgeKeyWithFormat(project, datasetGeneration, logicalKey, from, to, publication.IdentityFormatCompact)
}

func GenerationDocumentKeyWithFormat(project, datasetGeneration, collection, logicalKey, format string) string {
	format = NormalizeIdentityFormat(format)
	if format == publication.IdentityFormatLegacy {
		return "g_" + generationHash("vertex", project, datasetGeneration, collection, logicalKey, "", 5, 64)
	}
	return "g2" + generationHash("vertex", project, datasetGeneration, collection, logicalKey, "", 5, 32)
}

func GenerationDocumentKey(project, datasetGeneration, collection, logicalKey string) string {
	return generationDocumentKey(project, datasetGeneration, collection, logicalKey)
}

func GenerationEdgeKey(project, datasetGeneration, logicalKey, from, to string) string {
	return generationEdgeKey(project, datasetGeneration, logicalKey, from, to)
}

func GenerationEdgeKeyWithFormat(project, datasetGeneration, logicalKey, from, to, format string) string {
	format = NormalizeIdentityFormat(format)
	if format == publication.IdentityFormatLegacy {
		return "g_" + generationHash("edge", project, datasetGeneration, logicalKey, from, to, 6, 64)
	}
	return "g2" + generationHash("edge", project, datasetGeneration, logicalKey, from, to, 6, 32)
}

func NormalizeIdentityFormat(format string) string {
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

func QuoteDocumentString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
