package row

import (
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/ingest/identity"
)

type sourceFileOwnerRowBuilder struct {
	delegate RowBuilder
	owner    string
}

func WithSourceFileOwner(delegate RowBuilder, owner string) RowBuilder {
	return sourceFileOwnerRowBuilder{delegate: delegate, owner: owner}
}

func (b sourceFileOwnerRowBuilder) Build(resourceType string, line []byte, timings map[string]float64) (RowBuildResult, RowErrorType, error) {
	if builder, ok := b.delegate.(interface {
		BuildWithSourceFile(string, []byte, map[string]float64, string) (RowBuildResult, RowErrorType, error)
	}); ok {
		return builder.BuildWithSourceFile(resourceType, line, timings, b.owner)
	}
	result, kind, err := b.delegate.Build(resourceType, line, timings)
	if err != nil {
		return RowBuildResult{}, kind, err
	}
	result.edges, err = sourceFileOwnerEdges(result.edges, b.owner)
	if err != nil {
		return RowBuildResult{}, RowErrorEdge, err
	}
	return result, "", nil
}

func sourceFileOwnerEdges(edges []json.RawMessage, owner string) ([]json.RawMessage, error) {
	owned := make([]json.RawMessage, len(edges))
	for i, raw := range edges {
		document, err := identity.DecodeTopLevelDocument(raw)
		if err != nil {
			return nil, err
		}
		document["source_file"] = json.RawMessage(identity.QuoteDocumentString(owner))
		encoded, err := json.Marshal(document)
		if err != nil {
			return nil, fmt.Errorf("marshal source-owned edge %d: %w", i, err)
		}
		owned[i] = encoded
	}
	return owned, nil
}
