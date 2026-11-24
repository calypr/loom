package authoringv2

import (
	"reflect"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestWorkspaceDigestIncludesExactInterpretationReference(t *testing.T) {
	workspace := interpretationWorkspace("revision-a")
	first, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	workspace = interpretationWorkspace("revision-b")
	second, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatalf("interpretation reference did not change workspace digest: %q", first)
	}
}

func TestInterpretationReferenceHasOneCanonicalInlineState(t *testing.T) {
	workspace := interpretationWorkspace("revision-a")
	workspace.Documents[0].Columns[0].Interpretation = &ws.FeatureInterpretation{Kind: "INLINE"}
	if err := workspace.Validate(); err == nil {
		t.Fatal("explicit INLINE interpretation duplicated the nil inline state")
	}
}

func TestApplyInterpretationCandidatePinsAndClearsContributorWithoutChangingSource(t *testing.T) {
	workspace := interpretationWorkspace("revision-old")
	workspace.Documents[0].Columns[0].Interpretation = nil
	workspace.Documents[0].Columns[0].Source = ws.ColumnSource{Kind: ws.SourceAggregate, Aggregate: &ws.AggregateSource{Operation: "COUNT"}}
	predicate := &ws.ContributorPredicate{CandidateID: "patient-id", Operator: ws.ContributorExists}
	workspace.Documents[0].Columns[0].Contributor = predicate
	originalSource := workspace.Documents[0].Columns[0].Source

	updated, results, err := ApplyCommands(workspace, testCatalog(), "candidate", []Command{{Type: CommandApplyInterpretationCandidate, OutputID: "patients", Payload: &ColumnPayload{Column: "patient_id", InterpretationCandidate: &ApplyInterpretationCandidate{CandidateReceiptID: "receipt-candidate", RevisionID: "revision-a"}}}})
	if err != nil {
		t.Fatal(err)
	}
	column := updated.Documents[0].Columns[0]
	if len(results) != 1 || results[0].Type != CommandResultTableChanged || column.Interpretation == nil || column.Interpretation.Pinned == nil || column.Interpretation.Pinned.RevisionID != "revision-a" {
		t.Fatalf("candidate result=%#v column=%#v", results, column)
	}
	if column.Contributor != nil || !reflect.DeepEqual(column.Source, originalSource) {
		t.Fatalf("candidate changed unrelated feature state: contributor=%#v source=%#v", column.Contributor, column.Source)
	}
	second, _, err := ApplyCommands(updated, testCatalog(), "candidate-retry", []Command{{Type: CommandApplyInterpretationCandidate, OutputID: "patients", Payload: &ColumnPayload{Column: "patient_id", InterpretationCandidate: &ApplyInterpretationCandidate{CandidateReceiptID: "receipt-candidate", RevisionID: "revision-a"}}}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, updated) {
		t.Fatalf("replaying candidate changed workspace:\nfirst=%#v\nsecond=%#v", updated, second)
	}
}

func interpretationWorkspace(revisionID string) ws.Workspace {
	return ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Interpretations"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, Rows: ws.RecordsRowDefinition(), RootResourceType: "Patient",
			Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []ws.Column{{
				Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID,
				Source:         ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
				Interpretation: &ws.FeatureInterpretation{Kind: ws.FeatureInterpretationPinned, Pinned: &ws.PinnedInterpretation{RevisionID: revisionID}},
			}},
		}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}
