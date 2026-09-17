package authoringv2

import (
	"reflect"
	"strings"
	"testing"
)

func rowChangeWorkspace() Workspace {
	visible := true
	order := 0
	return Workspace{
		APIVersion: APIVersion,
		Kind:       WorkspaceKind,
		Explorer:   ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{
			Kind:             Kind,
			Output:           Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient",
			Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient", Children: []RouteNode{{
				OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters",
			}}},
			Population: &Population{SelectionRevisionID: "selection-patients", Route: []PopulationRouteStep{}},
			Columns: []Column{
				{Column: "patient_id", Label: "Patient ID", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &TablePresentation{Visible: &visible, Order: &order}},
				{Column: "encounter_id", Label: "Encounter ID", OccurrenceID: "encounter", Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE", RelatedSelection: &RelatedSelection{Kind: "first-by-resource-key", Acknowledged: true}}}},
			},
			FixedFilters: []FixedFilter{{Column: "patient_id", Values: []string{"patient-1"}}},
			Actions:      []Action{{Type: "download", Title: "Download", Columns: []ActionColumn{{Column: "encounter_id"}}}},
		}},
		Tabs: []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
}

func rowChangeCatalog() CatalogSnapshot {
	catalog := commandCatalog()
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"})
	return catalog
}

func TestAssessAndApplyRowChangePreservesAuthoredTable(t *testing.T) {
	workspace := rowChangeWorkspace()
	catalog := rowChangeCatalog()
	before, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "encounter"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeReady || assessment.Proposal == nil || len(assessment.Unresolved) != 0 {
		t.Fatalf("assessment=%#v", assessment)
	}
	if !reflect.DeepEqual(assessment.PreservedFeatureKeys, []string{"encounter_id", "patient_id"}) {
		t.Fatalf("preserved feature keys=%#v", assessment.PreservedFeatureKeys)
	}
	if after, _ := workspace.CanonicalJSON(); string(after) != string(before) {
		t.Fatalf("assessment mutated workspace\nbefore=%s\nafter=%s", before, after)
	}

	rebased, _, err := ApplyCommands(workspace, catalog, "apply-rebase", []Command{{Type: CommandApplyTableRootRebase, RowChange: assessment.Proposal}})
	if err != nil {
		t.Fatal(err)
	}
	document := rebased.Documents[0]
	if document.RootResourceType != "Encounter" || document.Route.OccurrenceID != RootOccurrenceID || document.Route.ResourceType != "Encounter" {
		t.Fatalf("rebased root=%#v", document.Route)
	}
	if len(document.Route.Children) != 1 || document.Route.Children[0].OccurrenceID != "encounter" || document.Route.Children[0].ResourceType != "Patient" || document.Route.Children[0].Relationship != "patient" {
		t.Fatalf("rebased route=%#v", document.Route)
	}
	occurrences := map[string]string{}
	for _, column := range document.Columns {
		occurrences[column.Column] = column.OccurrenceID
	}
	if !reflect.DeepEqual(occurrences, map[string]string{"encounter_id": RootOccurrenceID, "patient_id": "encounter"}) {
		t.Fatalf("rebased column occurrences=%#v", occurrences)
	}
	if document.Population == nil || document.Population.SelectionRevisionID != "selection-patients" || !reflect.DeepEqual(document.Population.Route, []PopulationRouteStep{{ResourceType: "Patient", Relationship: "patient"}}) {
		t.Fatalf("rebased population=%#v", document.Population)
	}
	if !reflect.DeepEqual(document.FixedFilters, workspace.Documents[0].FixedFilters) || !reflect.DeepEqual(document.Actions, workspace.Documents[0].Actions) {
		t.Fatalf("filters/actions changed: %#v %#v", document.FixedFilters, document.Actions)
	}
}

func TestAssessRowChangeReturnsStructuredRelationshipChoices(t *testing.T) {
	workspace := rowChangeWorkspace()
	catalog := commandCatalog()
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "encounter"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeBlocked || assessment.Proposal != nil || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "MISSING_ROUTE_REBASE_EDGE" {
		t.Fatalf("missing reverse assessment=%#v", assessment)
	}

	catalog = rowChangeCatalog()
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "encounter-subject", FromNodeID: "encounter", ToNodeID: "patient", Label: "subject"})
	assessment, err = AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "encounter"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeBlocked || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "AMBIGUOUS_ROUTE_REBASE_EDGE" || !reflect.DeepEqual(assessment.Unresolved[0].Alternatives, []string{"encounter-patient", "encounter-subject"}) {
		t.Fatalf("ambiguous reverse assessment=%#v", assessment)
	}
	assessment, err = AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "encounter", RouteRebase: []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: "encounter-patient"}},
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("resolved assessment=%#v err=%v", assessment, err)
	}
}

func TestAssessRowChangeRequiresAnExplicitOccurrenceWhenTheRouteHasDuplicates(t *testing.T) {
	workspace := rowChangeWorkspace()
	workspace.Documents[0].Route.Children = append(
		workspace.Documents[0].Route.Children,
		RouteNode{OccurrenceID: "encounter-followup", ResourceType: "Encounter", Relationship: "encounters"},
	)
	catalog := rowChangeCatalog()

	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "encounter",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeBlocked || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "AMBIGUOUS_ROW_ROOT_OCCURRENCE" || !reflect.DeepEqual(assessment.Unresolved[0].Alternatives, []string{"encounter", "encounter-followup"}) {
		t.Fatalf("ambiguous occurrence assessment=%#v", assessment)
	}

	assessment, err = AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "encounter", RootOccurrenceID: "encounter-followup",
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil || assessment.Proposal.RootOccurrenceID != "encounter-followup" {
		t.Fatalf("resolved occurrence assessment=%#v err=%v", assessment, err)
	}
}

func TestApplyRowChangeRejectsStaleProposalWithoutPartialMutation(t *testing.T) {
	workspace := rowChangeWorkspace()
	catalog := rowChangeCatalog()
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "encounter"})
	if err != nil || assessment.Proposal == nil {
		t.Fatalf("assessment=%#v err=%v", assessment, err)
	}
	before, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, catalog, "atomic-rebase", []Command{
		{Type: CommandRenameTable, OutputID: "patients", Title: "Renamed"},
		{Type: CommandApplyTableRootRebase, RowChange: assessment.Proposal},
	})
	if err == nil || !strings.Contains(err.Error(), "ROW_REBASE_PROPOSAL_STALE") {
		t.Fatalf("stale proposal error=%v", err)
	}
	after, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("failed batch mutated input\nbefore=%s\nafter=%s", before, after)
	}
}
