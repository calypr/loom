package authoringv2

import (
	"bytes"
	"testing"
)

func TestPrepareWorkspaceForCompilationUpgradesV9WithoutMutatingInput(t *testing.T) {
	workspace := compilationPreparationWorkspace(tableShapeSemanticsVersion)
	inputTableOrder := 7
	workspace.Documents[0].Columns[0].Table.Order = &inputTableOrder

	beforeCanonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	beforeDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}

	prepared, err := PrepareWorkspaceForCompilation(workspace, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SemanticsVersion != CurrentSemanticsVersion {
		t.Fatalf("prepared semanticsVersion = %d, want %d", prepared.SemanticsVersion, CurrentSemanticsVersion)
	}
	if got := *prepared.Documents[0].Columns[0].Table.Order; got != 0 {
		t.Fatalf("prepared table order = %d, want normalized order 0", got)
	}

	afterCanonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	afterDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterCanonical, beforeCanonical) {
		t.Fatalf("preparation changed input canonical bytes:\nbefore=%s\nafter=%s", beforeCanonical, afterCanonical)
	}
	if afterDigest != beforeDigest {
		t.Fatalf("preparation changed input digest: before=%q after=%q", beforeDigest, afterDigest)
	}
	if got := *workspace.Documents[0].Columns[0].Table.Order; got != inputTableOrder {
		t.Fatalf("preparation mutated input table order to %d, want %d", got, inputTableOrder)
	}
}

func TestPrepareWorkspaceForCompilationIsDeterministicAndIdempotent(t *testing.T) {
	workspace := compilationPreparationWorkspace(tableShapeSemanticsVersion)
	order := 9
	workspace.Documents[0].Columns[0].Table.Order = &order

	first, err := PrepareWorkspaceForCompilation(workspace, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := PrepareWorkspaceForCompilation(workspace, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	firstCanonical, err := first.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	secondCanonical, err := second.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstCanonical, secondCanonical) {
		t.Fatalf("same input produced different prepared canonical bytes:\nfirst=%s\nsecond=%s", firstCanonical, secondCanonical)
	}

	again, err := PrepareWorkspaceForCompilation(first, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	againCanonical, err := again.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(againCanonical, firstCanonical) {
		t.Fatalf("preparing an already prepared workspace changed canonical bytes:\nfirst=%s\nagain=%s", firstCanonical, againCanonical)
	}
}

func TestPrepareWorkspaceForCompilationDigestIncludesUnrelatedFieldEdits(t *testing.T) {
	original := compilationPreparationWorkspace(tableShapeSemanticsVersion)
	changed := compilationPreparationWorkspace(tableShapeSemanticsVersion)
	changed.Explorer.Description = "Updated workspace description"

	preparedOriginal, err := PrepareWorkspaceForCompilation(original, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	preparedChanged, err := PrepareWorkspaceForCompilation(changed, CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	originalDigest, err := preparedOriginal.Digest()
	if err != nil {
		t.Fatal(err)
	}
	changedDigest, err := preparedChanged.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if originalDigest == changedDigest {
		t.Fatalf("unrelated workspace description edit did not change prepared digest %q", originalDigest)
	}
}

func TestPrepareWorkspaceForCompilationPreservesV3ContributorDefaultsWithPinnedCatalog(t *testing.T) {
	legacy := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: 3,
		Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{
			Kind: Kind, Output: Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient",
			Route:            RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"},
			Columns: []Column{{
				Column: "patient_count", Label: "Patients", OccurrenceID: RootOccurrenceID,
				Source: ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{
					Operation: "COUNT", Where: &SourceWhere{Path: "id", Equals: ""},
				}},
			}},
		}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	catalog := commandCatalog()

	prepared, err := PrepareWorkspaceForCompilation(legacy, catalog)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SemanticsVersion != CurrentSemanticsVersion {
		t.Fatalf("prepared semanticsVersion = %d, want %d", prepared.SemanticsVersion, CurrentSemanticsVersion)
	}
	document := prepared.Documents[0]
	if document.Rows.Kind != RowDefinitionRecords {
		t.Fatalf("prepared v3 default rows = %#v, want root-record rows", document.Rows)
	}
	column := document.Columns[0]
	if column.Source.Aggregate.Where != nil {
		t.Fatalf("legacy aggregate where remains after preparation: %#v", column.Source.Aggregate.Where)
	}
	if column.Contributor == nil || column.Contributor.CandidateID != "patient-id" || column.Contributor.Operator != ContributorExists || column.Contributor.Value != nil {
		t.Fatalf("v3 empty-equals contributor did not retain catalog-bound EXISTS default: %#v", column.Contributor)
	}
	if len(prepared.MigrationDecisions) != 1 || prepared.MigrationDecisions[0] != "semantics-v4:aggregate-where-to-contributor:patients:patient_count:patient-id" {
		t.Fatalf("prepared migration decisions = %#v", prepared.MigrationDecisions)
	}
	if legacy.SemanticsVersion != 3 || legacy.Documents[0].Rows.Kind != "" || legacy.Documents[0].Columns[0].Source.Aggregate.Where == nil {
		t.Fatalf("preparation mutated the v3 caller input: %#v", legacy)
	}
}

func compilationPreparationWorkspace(version int) Workspace {
	visible := true
	return Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: version,
		Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{
			Kind: Kind, Output: Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient",
			Route:            RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient"},
			Rows:             RecordsRowDefinition(),
			Columns: []Column{{
				Column: "patient_id", Label: "Patient ID", OccurrenceID: RootOccurrenceID,
				Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}},
				Table:  &TablePresentation{Visible: &visible},
			}},
		}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
}
