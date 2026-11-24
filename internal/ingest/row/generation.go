package row

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/identity"
)

type generationRowBuilder struct {
	delegate          RowBuilder
	generated         *GeneratedRowBuilder
	project           string
	datasetGeneration string
	identityFormat    string
}

func NewGenerationRowBuilder(delegate RowBuilder, project, datasetGeneration string) (*generationRowBuilder, error) {
	return NewGenerationRowBuilderWithFormat(delegate, project, datasetGeneration, publication.IdentityFormatCompact)
}

func NewGenerationRowBuilderWithFormat(delegate RowBuilder, project, datasetGeneration, identityFormat string) (*generationRowBuilder, error) {
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
	identityFormat = identity.NormalizeIdentityFormat(identityFormat)
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

func (b *generationRowBuilder) Build(resourceType string, line []byte, stageSeconds map[string]float64) (RowBuildResult, RowErrorType, error) {
	return b.BuildWithSourceFile(resourceType, line, stageSeconds, "")
}

func (b *generationRowBuilder) BuildWithSourceFile(resourceType string, line []byte, stageSeconds map[string]float64, sourceFile string) (RowBuildResult, RowErrorType, error) {
	if b.generated != nil {
		result, errType, err := b.generated.build(resourceType, line, stageSeconds, b.datasetGeneration, sourceFile, b.identityFormat)
		if err != nil {
			return RowBuildResult{}, errType, err
		}
		return result, "", nil
	}
	result, errType, err := b.delegate.Build(resourceType, line, stageSeconds)
	if err != nil {
		return RowBuildResult{}, errType, err
	}
	namespaced, err := namespaceRowBuildResultWithFormat(result, b.project, b.datasetGeneration, resourceType, b.identityFormat)
	if err != nil {
		return RowBuildResult{}, RowErrorGeneration, err
	}
	if sourceFile != "" {
		namespaced.edges, err = sourceFileOwnerEdges(namespaced.edges, sourceFile)
		if err != nil {
			return RowBuildResult{}, RowErrorEdge, err
		}
	}
	return namespaced, "", nil
}

func namespaceRowBuildResult(result RowBuildResult, project, datasetGeneration, resourceType string) (RowBuildResult, error) {
	return namespaceRowBuildResultWithFormat(result, project, datasetGeneration, resourceType, publication.IdentityFormatCompact)
}

func namespaceRowBuildResultWithFormat(result RowBuildResult, project, datasetGeneration, resourceType, identityFormat string) (RowBuildResult, error) {
	project = strings.TrimSpace(project)
	datasetGeneration = strings.TrimSpace(datasetGeneration)
	resourceType = strings.TrimSpace(resourceType)
	if project == "" || datasetGeneration == "" || resourceType == "" {
		return RowBuildResult{}, fmt.Errorf("namespace row result requires project, dataset generation, and resource type")
	}

	vertex, logicalVertexKey, err := identity.NamespaceVertexDocumentWithFormat(result.vertex, project, datasetGeneration, resourceType, identityFormat)
	if err != nil {
		return RowBuildResult{}, err
	}
	edges, err := namespaceRowEdgesWithFormat(result.edges, project, datasetGeneration, identityFormat)
	if err != nil {
		return RowBuildResult{}, fmt.Errorf("namespace %s/%s: %w", resourceType, logicalVertexKey, err)
	}
	return RowBuildResult{vertex: vertex, edges: edges, payload: result.payload}, nil
}

func namespaceRowEdges(edges []json.RawMessage, project, datasetGeneration string) ([]json.RawMessage, error) {
	return namespaceRowEdgesWithFormat(edges, project, datasetGeneration, publication.IdentityFormatCompact)
}

func namespaceRowEdgesWithFormat(edges []json.RawMessage, project, datasetGeneration, identityFormat string) ([]json.RawMessage, error) {
	namespaced := make([]json.RawMessage, len(edges))
	for index, edge := range edges {
		document, err := identity.NamespaceEdgeDocumentWithFormat(edge, project, datasetGeneration, identityFormat)
		if err != nil {
			return nil, fmt.Errorf("edge %d: %w", index, err)
		}
		namespaced[index] = document
	}
	return namespaced, nil
}
