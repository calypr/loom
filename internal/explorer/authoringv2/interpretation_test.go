package authoringv2

import (
	"reflect"
	"testing"
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
	workspace.Documents[0].Columns[0].Interpretation = &FeatureInterpretation{Kind: "INLINE"}
	if err := workspace.Validate(); err == nil {
		t.Fatal("explicit INLINE interpretation duplicated the nil inline state")
	}
}

func TestApplyInterpretationCandidatePinsAndClearsContributorWithoutChangingSource(t *testing.T) {
	workspace := interpretationWorkspace("revision-old")
	workspace.Documents[0].Columns[0].Interpretation = nil
	workspace.Documents[0].Columns[0].Source = ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{Operation: "COUNT"}}
	predicate := &ContributorPredicate{CandidateID: "patient-id", Operator: ContributorExists}
	workspace.Documents[0].Columns[0].Contributor = predicate
	originalSource := workspace.Documents[0].Columns[0].Source

	updated, results, err := ApplyCommands(workspace, testCatalog(), "candidate", []Command{{
		Type: CommandApplyInterpretationCandidate, OutputID: "patients", Column: "patient_id",
		InterpretationCandidate: &ApplyInterpretationCandidate{CandidateReceiptID: "receipt-candidate", RevisionID: "revision-a"},
	}})
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
	second, _, err := ApplyCommands(updated, testCatalog(), "candidate-retry", []Command{{
		Type: CommandApplyInterpretationCandidate, OutputID: "patients", Column: "patient_id",
		InterpretationCandidate: &ApplyInterpretationCandidate{CandidateReceiptID: "receipt-candidate", RevisionID: "revision-a"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(second, updated) {
		t.Fatalf("replaying candidate changed workspace:\nfirst=%#v\nsecond=%#v", updated, second)
	}
}

func interpretationWorkspace(revisionID string) Workspace {
	return Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Interpretations"},
		Documents: []Document{{
			Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"},
			Columns: []Column{{
				Column: "patient_id", Label: "Patient ID", OccurrenceID: RootOccurrenceID,
				Source:         ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}},
				Interpretation: &FeatureInterpretation{Kind: FeatureInterpretationPinned, Pinned: &PinnedInterpretation{RevisionID: revisionID}},
			}},
		}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}
