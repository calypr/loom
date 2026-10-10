package authoringv2

import (
	"reflect"
	"sort"
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
			Rows:       RecordsRowDefinition(),
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

func rowChangePlainRootScenario() (Workspace, CatalogSnapshot) {
	workspace := rowChangeWorkspace()
	workspace.Documents[0].Route.Children = nil
	workspace.Documents[0].Columns = workspace.Documents[0].Columns[:1]
	workspace.Documents[0].Actions[0].Columns = []ActionColumn{{Column: "patient_id"}}

	catalog := rowChangeCatalog()
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "observation", ResourceType: "Observation", RowRootEligible: true})
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", Label: "observations"},
		CatalogEdge{ID: "observation-patient", FromNodeID: "observation", ToNodeID: "patient", Label: "patient"},
	)
	return workspace, catalog
}

func rowChangePopulationRoundTripScenario() (Workspace, CatalogSnapshot) {
	workspace, catalog := rowChangePlainRootScenario()
	catalog.Edges = removeCatalogEdges(catalog.Edges, "patient-observation", "observation-patient")
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-observation-subject", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient", StorageDirection: "INBOUND"},
		CatalogEdge{ID: "observation-patient-subject", FromNodeID: "observation", ToNodeID: "patient", Label: "subject_Patient", StorageDirection: "OUTBOUND"},
	)
	workspace.Documents[0].Population = &Population{SelectionRevisionID: "selection-patient-root", Route: []PopulationRouteStep{}}
	return workspace, catalog
}

func removeCatalogEdges(edges []CatalogEdge, ids ...string) []CatalogEdge {
	remove := make(map[string]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	filtered := make([]CatalogEdge, 0, len(edges))
	for _, edge := range edges {
		if !remove[edge.ID] {
			filtered = append(filtered, edge)
		}
	}
	return filtered
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
	if document.Population == nil || document.Population.SelectionRevisionID != "selection-patients" || !reflect.DeepEqual(document.Population.Route, []PopulationRouteStep{{ResourceType: "Patient", Relationship: "patient", CatalogEdgeID: "encounter-patient"}}) {
		t.Fatalf("rebased population=%#v", document.Population)
	}
	if !reflect.DeepEqual(document.FixedFilters, workspace.Documents[0].FixedFilters) || !reflect.DeepEqual(document.Actions, workspace.Documents[0].Actions) {
		t.Fatalf("filters/actions changed: %#v %#v", document.FixedFilters, document.Actions)
	}
}

func TestAssessAndApplyRowChangeAddsCatalogRootAlongAnAuthoredPath(t *testing.T) {
	workspace := rowChangeWorkspace()
	workspace.Documents[0].Route.Children[0].CatalogEdgeID = "patient-encounter"
	catalog := rowChangeCatalog()
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "observation", ResourceType: "Observation", RowRootEligible: true})
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "patient-decoy", ResourceType: "Patient", RowRootEligible: true})
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "encounter-observation", FromNodeID: "encounter", ToNodeID: "observation", Label: "observations"},
		CatalogEdge{ID: "observation-encounter", FromNodeID: "observation", ToNodeID: "encounter", Label: "encounter"},
		CatalogEdge{ID: "observation-patient", FromNodeID: "observation", ToNodeID: "patient", Label: "patient"},
		CatalogEdge{ID: "observation-patient-decoy", FromNodeID: "observation", ToNodeID: "patient-decoy", Label: "patient"},
	)
	before, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}

	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "observation"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeReady || assessment.Proposal == nil || len(assessment.Unresolved) != 0 {
		t.Fatalf("assessment=%#v", assessment)
	}
	if after, _ := workspace.CanonicalJSON(); string(after) != string(before) {
		t.Fatalf("assessment mutated workspace\nbefore=%s\nafter=%s", before, after)
	}
	if !strings.HasPrefix(assessment.Proposal.RootOccurrenceID, "row-change-route:v1:") {
		t.Fatalf("derived root occurrence=%q, want a catalog-route choice token", assessment.Proposal.RootOccurrenceID)
	}
	if !reflect.DeepEqual(assessment.Proposal.RouteRebase, []RouteRebaseChoice{
		{OccurrenceID: RootOccurrenceID, EdgeID: "encounter-patient"},
		{OccurrenceID: "encounter", EdgeID: "observation-encounter"},
	}) {
		t.Fatalf("route rebase=%#v", assessment.Proposal.RouteRebase)
	}

	rebased, err := ApplyRowChange(workspace.Documents[0], catalog, *assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if rebased.RootResourceType != "Observation" || rebased.Route.ResourceType != "Observation" || rebased.Route.OccurrenceID != RootOccurrenceID {
		t.Fatalf("rebased root=%#v", rebased.Route)
	}
	if len(rebased.Route.Children) != 1 {
		t.Fatalf("rebased route=%#v", rebased.Route)
	}
	encounter := rebased.Route.Children[0]
	if encounter.OccurrenceID != "encounter" || encounter.ResourceType != "Encounter" || encounter.Relationship != "encounter" || encounter.MatchMode.Normalized() != RouteMatchOptional {
		t.Fatalf("rebased encounter=%#v", encounter)
	}
	if len(encounter.Children) != 1 || encounter.Children[0].ResourceType != "Patient" || encounter.Children[0].Relationship != "patient" || !strings.HasPrefix(encounter.Children[0].OccurrenceID, "row_change_") {
		t.Fatalf("rebased patient=%#v", encounter.Children)
	}
	patientOccurrenceID := encounter.Children[0].OccurrenceID
	if findRoute(&rebased.Route, assessment.Proposal.RootOccurrenceID) != nil {
		t.Fatalf("temporary route token persisted in the document: %q", assessment.Proposal.RootOccurrenceID)
	}
	occurrences := map[string]string{}
	for _, column := range rebased.Columns {
		occurrences[column.Column] = column.OccurrenceID
	}
	if !reflect.DeepEqual(occurrences, map[string]string{"patient_id": patientOccurrenceID, "encounter_id": "encounter"}) {
		t.Fatalf("rebased column occurrences=%#v", occurrences)
	}
	if rebased.Population == nil || rebased.Population.SelectionRevisionID != "selection-patients" || !reflect.DeepEqual(rebased.Population.Route, []PopulationRouteStep{
		{ResourceType: "Encounter", Relationship: "encounter", CatalogEdgeID: "observation-encounter"},
		{ResourceType: "Patient", Relationship: "patient", CatalogEdgeID: "encounter-patient"},
	}) {
		t.Fatalf("rebased population=%#v", rebased.Population)
	}
	if !reflect.DeepEqual(rebased.FixedFilters, workspace.Documents[0].FixedFilters) || !reflect.DeepEqual(rebased.Actions, workspace.Documents[0].Actions) {
		t.Fatalf("filters/actions changed: %#v %#v", rebased.FixedFilters, rebased.Actions)
	}
}

func TestAssessAndApplyRowChangePreservesPlainRootTableForCatalogRoot(t *testing.T) {
	workspace, catalog := rowChangePlainRootScenario()
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "observation"})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("plain-root assessment=%#v err=%v", assessment, err)
	}
	if !reflect.DeepEqual(assessment.PreservedFeatureKeys, []string{"patient_id"}) {
		t.Fatalf("preserved feature keys=%#v", assessment.PreservedFeatureKeys)
	}

	rebased, err := ApplyRowChange(workspace.Documents[0], catalog, *assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if rebased.RootResourceType != "Observation" || rebased.Route.ResourceType != "Observation" {
		t.Fatalf("rebased root=%#v resource type=%q", rebased.Route, rebased.RootResourceType)
	}
	if rebased.Route.OccurrenceID != RootOccurrenceID || len(rebased.Route.Children) != 1 {
		t.Fatalf("rebased route=%#v", rebased.Route)
	}
	patient := rebased.Route.Children[0]
	if patient.ResourceType != "Patient" || patient.Relationship != "patient" || patient.CatalogEdgeID != "observation-patient" {
		t.Fatalf("rebased patient route=%#v", patient)
	}
	if !strings.HasPrefix(patient.OccurrenceID, "row_change_") {
		t.Fatalf("rebased patient occurrence=%q", patient.OccurrenceID)
	}
	wantColumn := workspace.Documents[0].Columns[0]
	wantColumn.OccurrenceID = patient.OccurrenceID
	if !reflect.DeepEqual(rebased.Columns, []Column{wantColumn}) {
		t.Fatalf("retained table columns=%#v, want %#v", rebased.Columns, []Column{wantColumn})
	}
	wantPopulationRoute := []PopulationRouteStep{{ResourceType: "Patient", Relationship: "patient", CatalogEdgeID: "observation-patient"}}
	if rebased.Population == nil || rebased.Population.SelectionRevisionID != "selection-patients" || !reflect.DeepEqual(rebased.Population.Route, wantPopulationRoute) {
		t.Fatalf("retained population=%#v", rebased.Population)
	}
	if !reflect.DeepEqual(rebased.FixedFilters, workspace.Documents[0].FixedFilters) || !reflect.DeepEqual(rebased.Actions, workspace.Documents[0].Actions) {
		t.Fatalf("filters/actions changed: %#v %#v", rebased.FixedFilters, rebased.Actions)
	}
}

func TestApplyRowChangeRoundTripRestoresDirectSelectedPopulationRoute(t *testing.T) {
	workspace, catalog := rowChangePopulationRoundTripScenario()
	observationDocument := rowChangeToObservation(t, workspace, catalog, "observation-patient-subject")
	if observationDocument.Population == nil || observationDocument.Population.SelectionRevisionID != "selection-patient-root" || !reflect.DeepEqual(observationDocument.Population.Route, []PopulationRouteStep{{
		ResourceType: "Patient", Relationship: "subject_Patient", CatalogEdgeID: "observation-patient-subject", StorageDirection: "OUTBOUND",
	}}) {
		t.Fatalf("Observation population route=%#v", observationDocument.Population)
	}
	if len(observationDocument.Route.Children) != 1 || observationDocument.Route.Children[0].ResourceType != "Patient" {
		t.Fatalf("Observation row route=%#v", observationDocument.Route)
	}

	patientOccurrenceID := observationDocument.Route.Children[0].OccurrenceID
	toPatient, err := AssessRowChange(Workspace{Documents: []Document{observationDocument}}, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "patient", RootOccurrenceID: patientOccurrenceID,
	})
	if err != nil || toPatient.Status != RowChangeReady || toPatient.Proposal == nil {
		t.Fatalf("Observation-to-Patient assessment=%#v err=%v", toPatient, err)
	}
	patientDocument, err := ApplyRowChange(observationDocument, catalog, *toPatient.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if patientDocument.RootResourceType != "Patient" || patientDocument.Route.ResourceType != "Patient" {
		t.Fatalf("restored root=%#v resource type=%q", patientDocument.Route, patientDocument.RootResourceType)
	}
	if patientDocument.Population == nil || patientDocument.Population.SelectionRevisionID != "selection-patient-root" || len(patientDocument.Population.Route) != 0 {
		t.Fatalf("round-trip should restore direct selection membership, population=%#v", patientDocument.Population)
	}
	if len(patientDocument.Columns) != 1 || patientDocument.Columns[0].OccurrenceID != RootOccurrenceID || patientDocument.Columns[0].Column != "patient_id" {
		t.Fatalf("restored Patient field=%#v", patientDocument.Columns)
	}
}

func TestApplyRowChangePopulationRebasePreservesDifferentRelationshipAtSeam(t *testing.T) {
	workspace, catalog := rowChangePopulationRoundTripScenario()
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-observation-focus", FromNodeID: "patient", ToNodeID: "observation", Label: "focus", StorageDirection: "INBOUND"},
		CatalogEdge{ID: "observation-patient-focus", FromNodeID: "observation", ToNodeID: "patient", Label: "focus", StorageDirection: "OUTBOUND"},
	)
	observationDocument := rowChangeToObservation(t, workspace, catalog, "observation-patient-subject")
	patientOccurrenceID := observationDocument.Route.Children[0].OccurrenceID

	assessment, err := AssessRowChange(Workspace{Documents: []Document{observationDocument}}, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "patient", RootOccurrenceID: patientOccurrenceID,
		RouteRebase: []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: "patient-observation-focus"}},
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("Observation-to-Patient assessment=%#v err=%v", assessment, err)
	}
	rebased, err := ApplyRowChange(observationDocument, catalog, *assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	want := []PopulationRouteStep{
		{ResourceType: "Observation", Relationship: "focus", CatalogEdgeID: "patient-observation-focus", StorageDirection: "INBOUND"},
		{ResourceType: "Patient", Relationship: "subject_Patient", CatalogEdgeID: "observation-patient-subject", StorageDirection: "OUTBOUND"},
	}
	if rebased.Population == nil || !reflect.DeepEqual(rebased.Population.Route, want) {
		t.Fatalf("different relationship must remain in population route, got %#v want %#v", rebased.Population, want)
	}
}

func TestApplyRowChangePopulationRebasePreservesDistinctSameTypeAnchor(t *testing.T) {
	workspace, catalog := rowChangePopulationRoundTripScenario()
	observationDocument := rowChangeToObservation(t, workspace, catalog, "observation-patient-subject")
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "patient-decoy", ResourceType: "Patient", RowRootEligible: true})
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-decoy-observation-subject", FromNodeID: "patient-decoy", ToNodeID: "observation", Label: "subject_Patient", StorageDirection: "INBOUND"},
		CatalogEdge{ID: "observation-patient-decoy-subject", FromNodeID: "observation", ToNodeID: "patient-decoy", Label: "subject_Patient", StorageDirection: "OUTBOUND"},
	)
	decoyToken, err := encodeRowChangeRouteToken(rowChangeRouteToken{
		EdgeIDs: []string{"observation-patient-decoy-subject"}, AnchorOccurrenceID: RootOccurrenceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := AssessRowChange(Workspace{Documents: []Document{observationDocument}}, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "patient-decoy", RootOccurrenceID: decoyToken,
		RouteRebase: []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: "patient-decoy-observation-subject"}},
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("Observation-to-distinct-Patient assessment=%#v err=%v", assessment, err)
	}
	rebased, err := ApplyRowChange(observationDocument, catalog, *assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	want := []PopulationRouteStep{
		{ResourceType: "Observation", Relationship: "subject_Patient", CatalogEdgeID: "patient-decoy-observation-subject", StorageDirection: "INBOUND"},
		{ResourceType: "Patient", Relationship: "subject_Patient", CatalogEdgeID: "observation-patient-subject", StorageDirection: "OUTBOUND"},
	}
	if rebased.RootResourceType != "Patient" || rebased.Route.ResourceType != "Patient" || assessment.Proposal.RootNodeID != "patient-decoy" {
		t.Fatalf("selected root should be the distinct Patient catalog node: proposal=%#v route=%#v", assessment.Proposal, rebased.Route)
	}
	if rebased.Population == nil || !reflect.DeepEqual(rebased.Population.Route, want) {
		t.Fatalf("distinct same-type anchor path must remain, got %#v want %#v", rebased.Population, want)
	}
}

func rowChangeToObservation(t *testing.T, workspace Workspace, catalog CatalogSnapshot, inverseEdgeID string) Document {
	t.Helper()
	token, err := encodeRowChangeRouteToken(rowChangeRouteToken{
		EdgeIDs: []string{"patient-observation-subject"}, AnchorOccurrenceID: RootOccurrenceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: token,
		RouteRebase: []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: inverseEdgeID}},
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("Patient-to-Observation assessment=%#v err=%v", assessment, err)
	}
	document, err := ApplyRowChange(workspace.Documents[0], catalog, *assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	return document
}

func TestAssessRowChangeRejectsMalformedOrUnknownCatalogRouteTokenWithoutMutation(t *testing.T) {
	workspace, catalog := rowChangePlainRootScenario()
	before, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	unknownToken, err := encodeRowChangeRouteToken(rowChangeRouteToken{
		EdgeIDs: []string{"missing-edge"}, AnchorOccurrenceID: RootOccurrenceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{rowChangeRouteTokenPrefix + "not-base64", unknownToken} {
		t.Run(token, func(t *testing.T) {
			assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{
				OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: token,
			})
			if err != nil {
				t.Fatal(err)
			}
			if assessment.Status != RowChangeBlocked || assessment.Proposal != nil || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "ROW_ROOT_OCCURRENCE_NOT_FOUND" {
				t.Fatalf("invalid catalog route token assessment=%#v", assessment)
			}
			if after, _ := workspace.CanonicalJSON(); string(after) != string(before) {
				t.Fatalf("assessment mutated workspace\nbefore=%s\nafter=%s", before, after)
			}
		})
	}
}

func TestApplyRowChangeRejectsObsoleteCatalogRouteTokenWithoutPartialMutation(t *testing.T) {
	workspace, catalog := rowChangePlainRootScenario()
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "observation"})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("plain-root assessment=%#v err=%v", assessment, err)
	}
	before, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	updatedCatalog := catalog
	updatedCatalog.Edges = make([]CatalogEdge, 0, len(catalog.Edges)-1)
	for _, edge := range catalog.Edges {
		if edge.ID != "observation-patient" {
			updatedCatalog.Edges = append(updatedCatalog.Edges, edge)
		}
	}

	_, err = ApplyRowChange(workspace.Documents[0], updatedCatalog, *assessment.Proposal)
	if err == nil || !strings.Contains(err.Error(), "ROW_REBASE_UNRESOLVED") {
		t.Fatalf("obsolete catalog route token error=%v", err)
	}
	if after, _ := workspace.CanonicalJSON(); string(after) != string(before) {
		t.Fatalf("failed apply mutated input document\nbefore=%s\nafter=%s", before, after)
	}
}

func TestAssessRowChangeRejectsAnAbsentRootWithoutAnInversePath(t *testing.T) {
	workspace := rowChangeWorkspace()
	catalog := rowChangeCatalog()
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "observation", ResourceType: "Observation", RowRootEligible: true})
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "patient-observation", FromNodeID: "patient", ToNodeID: "observation", Label: "observations"})

	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "observation"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeBlocked || assessment.Proposal != nil || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "ROW_ROOT_PATH_NOT_FOUND" || len(assessment.Unresolved[0].Alternatives) != 0 {
		t.Fatalf("unsupported catalog path assessment=%#v", assessment)
	}
}

func TestAssessRowChangeDoesNotTreatSameResourceTypeNodeAsCurrentRoot(t *testing.T) {
	workspace := rowChangeWorkspace()
	catalog := rowChangeCatalog()
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "patient-decoy", ResourceType: "Patient", RowRootEligible: true})
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-decoy", FromNodeID: "patient", ToNodeID: "patient-decoy", Label: "relatedPatients"},
		CatalogEdge{ID: "decoy-patient", FromNodeID: "patient-decoy", ToNodeID: "patient", Label: "patient"},
	)
	unchanged, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "patient"})
	if err != nil || unchanged.Status != RowChangeNoChange || unchanged.Proposal != nil {
		t.Fatalf("exact current catalog root assessment=%#v err=%v", unchanged, err)
	}

	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "patient-decoy"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeReady || assessment.Proposal == nil || assessment.Proposal.RootNodeID != "patient-decoy" {
		t.Fatalf("same-resource-type catalog node assessment=%#v", assessment)
	}

	rebased, err := ApplyRowChange(workspace.Documents[0], catalog, *assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebased.Route.Children) != 1 || rebased.Route.Children[0].ResourceType != "Patient" || rebased.Route.Children[0].Relationship != "patient" {
		t.Fatalf("same-resource-type root choice did not rebase the route: %#v", rebased.Route)
	}
}

func TestAssessRowChangeDisambiguatesDistinctCatalogPathsForAnAbsentRoot(t *testing.T) {
	workspace := rowChangeWorkspace()
	catalog := rowChangeCatalog()
	catalog.Nodes = append(catalog.Nodes,
		CatalogNode{ID: "observation", ResourceType: "Observation", RowRootEligible: true},
		CatalogNode{ID: "condition", ResourceType: "Condition"},
	)
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-observation-subject", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"},
		CatalogEdge{ID: "observation-patient-subject", FromNodeID: "observation", ToNodeID: "patient", Label: "subject_Patient"},
		CatalogEdge{ID: "patient-observation-focus", FromNodeID: "patient", ToNodeID: "observation", Label: "focus_Patient"},
		CatalogEdge{ID: "observation-patient-focus", FromNodeID: "observation", ToNodeID: "patient", Label: "focus_Patient"},
		CatalogEdge{ID: "encounter-observation", FromNodeID: "encounter", ToNodeID: "observation", Label: "observations"},
		CatalogEdge{ID: "observation-encounter", FromNodeID: "observation", ToNodeID: "encounter", Label: "encounter"},
		CatalogEdge{ID: "patient-condition", FromNodeID: "patient", ToNodeID: "condition", Label: "conditions"},
		CatalogEdge{ID: "condition-patient", FromNodeID: "condition", ToNodeID: "patient", Label: "patient"},
		CatalogEdge{ID: "condition-observation", FromNodeID: "condition", ToNodeID: "observation", Label: "observations"},
		CatalogEdge{ID: "observation-condition", FromNodeID: "observation", ToNodeID: "condition", Label: "condition"},
	)

	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{OutputID: "patients", RootNodeID: "observation"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeBlocked || assessment.Proposal != nil || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "AMBIGUOUS_ROW_ROOT_OCCURRENCE" || len(assessment.Unresolved[0].Alternatives) != 2 {
		t.Fatalf("ambiguous catalog path assessment=%#v", assessment)
	}

	selectedEdges := make([]string, 0, len(assessment.Unresolved[0].Alternatives))
	for _, alternative := range assessment.Unresolved[0].Alternatives {
		if !strings.HasPrefix(alternative, "row-change-route:v1:") {
			t.Fatalf("alternative=%q, want a catalog-route choice token", alternative)
		}
		resolved, err := AssessRowChange(workspace, catalog, RowChangeRequest{
			OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: alternative,
		})
		if err != nil || resolved.Status != RowChangeReady || resolved.Proposal == nil || resolved.Proposal.RootOccurrenceID != alternative {
			t.Fatalf("selected alternative=%q assessment=%#v err=%v", alternative, resolved, err)
		}
		if len(resolved.Proposal.RouteRebase) != 1 {
			t.Fatalf("alternative=%q selected a non-shortest catalog route: %#v", alternative, resolved.Proposal.RouteRebase)
		}
		selectedEdges = append(selectedEdges, resolved.Proposal.RouteRebase[0].EdgeID)
		_, err = ApplyRowChange(workspace.Documents[0], catalog, *resolved.Proposal)
		if err != nil {
			t.Fatalf("apply alternative %q: %v", alternative, err)
		}
	}
	sort.Strings(selectedEdges)
	if !reflect.DeepEqual(selectedEdges, []string{"observation-patient-focus", "observation-patient-subject"}) {
		t.Fatalf("selected inverse edges=%#v, want only the two minimum-depth alternatives", selectedEdges)
	}
}

func TestAssessAndApplyRowChangePromotesADeepDescendant(t *testing.T) {
	workspace := rowChangeWorkspace()
	workspace.Documents[0].Route.Children[0].MatchMode = RouteMatchRequired
	workspace.Documents[0].Route.Children[0].Children = []RouteNode{
		{OccurrenceID: "followup", ResourceType: "Encounter", Relationship: "revisits"},
		{OccurrenceID: "observation", ResourceType: "Observation", Relationship: "observations", MatchMode: RouteMatchOptional},
	}
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, Column{
		Column: "observation_id", Label: "Observation ID", OccurrenceID: "observation",
		Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE", RelatedSelection: &RelatedSelection{Kind: "first-by-resource-key", Acknowledged: true}}},
	})
	catalog := rowChangeCatalog()
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "observation", ResourceType: "Observation", RowRootEligible: true})
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "encounter-observation", FromNodeID: "encounter", ToNodeID: "observation", Label: "observations"},
		CatalogEdge{ID: "observation-encounter", FromNodeID: "observation", ToNodeID: "encounter", Label: "encounter"},
	)

	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: "observation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("deep assessment=%#v", assessment)
	}
	if !reflect.DeepEqual(assessment.Proposal.RouteRebase, []RouteRebaseChoice{
		{OccurrenceID: RootOccurrenceID, EdgeID: "encounter-patient"},
		{OccurrenceID: "encounter", EdgeID: "observation-encounter"},
	}) {
		t.Fatalf("deep route choices=%#v", assessment.Proposal.RouteRebase)
	}

	rebased, err := ApplyRowChange(workspace.Documents[0], catalog, *assessment.Proposal)
	if err != nil {
		t.Fatal(err)
	}
	if rebased.RootResourceType != "Observation" || rebased.Route.OccurrenceID != RootOccurrenceID {
		t.Fatalf("deep root=%#v", rebased.Route)
	}
	if len(rebased.Route.Children) != 1 || rebased.Route.Children[0].OccurrenceID != "encounter" || rebased.Route.Children[0].Relationship != "encounter" || rebased.Route.Children[0].MatchMode != RouteMatchOptional {
		t.Fatalf("deep encounter route=%#v", rebased.Route)
	}
	encounterChildren := rebased.Route.Children[0].Children
	if len(encounterChildren) != 2 || encounterChildren[0].OccurrenceID != "followup" {
		t.Fatalf("deep rebase lost the off-path Encounter branch: %#v", encounterChildren)
	}
	patient := encounterChildren[1]
	if patient.OccurrenceID != "observation" || patient.ResourceType != "Patient" || patient.Relationship != "patient" || patient.MatchMode != RouteMatchRequired {
		t.Fatalf("deep patient route=%#v", patient)
	}
	occurrences := map[string]string{}
	for _, column := range rebased.Columns {
		occurrences[column.Column] = column.OccurrenceID
	}
	if !reflect.DeepEqual(occurrences, map[string]string{"patient_id": "observation", "encounter_id": "encounter", "observation_id": RootOccurrenceID}) {
		t.Fatalf("deep column occurrences=%#v", occurrences)
	}
	if rebased.Population == nil || !reflect.DeepEqual(rebased.Population.Route, []PopulationRouteStep{
		{ResourceType: "Encounter", Relationship: "encounter", CatalogEdgeID: "observation-encounter"},
		{ResourceType: "Patient", Relationship: "patient", CatalogEdgeID: "encounter-patient"},
	}) {
		t.Fatalf("deep population route=%#v", rebased.Population)
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

func TestAssessRowChangePrefersTheAuthoredInverseAmongDifferentRelationships(t *testing.T) {
	workspace, catalog := rowChangeSubjectFocusFixture()
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: "observation",
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil {
		t.Fatalf("assessment=%#v err=%v", assessment, err)
	}
	if !reflect.DeepEqual(assessment.Proposal.RouteRebase, []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: "observation-patient-subject"}}) {
		t.Fatalf("authored inverse route choice=%#v, want the subject relationship", assessment.Proposal.RouteRebase)
	}
	rebased, err := ApplyRowChange(workspace.Documents[0], catalog, *assessment.Proposal)
	if err != nil || rebased.RootResourceType != "Observation" || len(rebased.Route.Children) != 1 ||
		rebased.Route.Children[0].ResourceType != "Patient" || rebased.Route.Children[0].Relationship != "subject_Patient" ||
		rebased.Route.Children[0].CatalogEdgeID != "observation-patient-subject" {
		t.Fatalf("rebased route=%#v err=%v, want Observation → Patient on the authored subject relationship", rebased.Route, err)
	}

	// An explicit user choice still takes priority over the inferred inverse.
	assessment, err = AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: "observation",
		RouteRebase: []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: "observation-patient-focus"}},
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil ||
		!reflect.DeepEqual(assessment.Proposal.RouteRebase, []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: "observation-patient-focus"}}) {
		t.Fatalf("explicit inverse override assessment=%#v err=%v", assessment, err)
	}
}

func TestAssessRowChangeKeepsAmbiguityBetweenMatchingInverseRelationships(t *testing.T) {
	workspace, catalog := rowChangeSubjectFocusFixture()
	catalog.Edges = append(catalog.Edges, CatalogEdge{
		ID: "observation-patient-subject-secondary", FromNodeID: "observation", ToNodeID: "patient", Label: "subject_Patient",
	})
	assessment, err := AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: "observation",
	})
	if err != nil || assessment.Status != RowChangeBlocked || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "AMBIGUOUS_ROUTE_REBASE_EDGE" {
		t.Fatalf("duplicate authored inverse assessment=%#v err=%v", assessment, err)
	}
	if !reflect.DeepEqual(assessment.Unresolved[0].Alternatives, []string{
		"observation-patient-focus", "observation-patient-subject", "observation-patient-subject-secondary",
	}) {
		t.Fatalf("duplicate inverse alternatives=%#v", assessment.Unresolved[0].Alternatives)
	}

	assessment, err = AssessRowChange(workspace, catalog, RowChangeRequest{
		OutputID: "patients", RootNodeID: "observation", RootOccurrenceID: "observation",
		RouteRebase: []RouteRebaseChoice{{OccurrenceID: RootOccurrenceID, EdgeID: "observation-patient-subject-secondary"}},
	})
	if err != nil || assessment.Status != RowChangeReady || assessment.Proposal == nil ||
		len(assessment.Proposal.RouteRebase) != 1 || assessment.Proposal.RouteRebase[0].EdgeID != "observation-patient-subject-secondary" {
		t.Fatalf("explicit duplicate inverse selection assessment=%#v err=%v", assessment, err)
	}
}

func rowChangeSubjectFocusFixture() (Workspace, CatalogSnapshot) {
	workspace := rowChangeWorkspace()
	workspace.Documents[0].Route.Children[0] = RouteNode{
		OccurrenceID: "observation", ResourceType: "Observation", Relationship: "subject_Patient", CatalogEdgeID: "patient-observation-subject",
	}
	for index := range workspace.Documents[0].Columns {
		if workspace.Documents[0].Columns[index].OccurrenceID == "encounter" {
			workspace.Documents[0].Columns[index].OccurrenceID = "observation"
		}
	}
	catalog := commandCatalog()
	catalog.Nodes = append(catalog.Nodes, CatalogNode{ID: "observation", ResourceType: "Observation", RowRootEligible: true})
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-observation-subject", FromNodeID: "patient", ToNodeID: "observation", Label: "subject_Patient"},
		CatalogEdge{ID: "observation-patient-subject", FromNodeID: "observation", ToNodeID: "patient", Label: "subject_Patient"},
		CatalogEdge{ID: "observation-patient-focus", FromNodeID: "observation", ToNodeID: "patient", Label: "focus_Patient"},
	)
	return workspace, catalog
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
