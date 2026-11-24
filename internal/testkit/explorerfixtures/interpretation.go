package explorerfixtures

import (
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer/interpretation"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func InterpretationRevisionInput() interpretation.InterpretationRevision {
	return interpretation.InterpretationRevision{
		Project: "project-a", LibraryID: "library-a", Author: "tester", Explanation: "lifecycle",
		CreatedAt: time.Unix(1, 0).UTC(), Rules: []interpretation.InterpretationRule{{ID: "rule", Match: interpretation.InterpretationStructuralMatch{ResourceType: "Patient", LogicalType: "string"}, Definition: interpretation.InterpretationFeatureDefinition{Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}},
	}
}

func PrepareInterpretationRevision(t testing.TB, input interpretation.InterpretationRevision) interpretation.InterpretationRevision {
	t.Helper()
	revision, err := interpretation.PrepareInterpretationRevision(input)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
