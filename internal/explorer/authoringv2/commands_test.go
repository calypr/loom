package authoringv2

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func emptyCommandWorkspace() Workspace {
	return Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Builder"}, Documents: []Document{}, Tabs: []Tab{}}
}

func commandCatalog() CatalogSnapshot {
	catalog := testCatalog()
	for index := range catalog.Candidates {
		catalog.Candidates[index].ProjectionModes = []string{"VALUE"}
		catalog.Candidates[index].DefaultProjectionMode = "VALUE"
		catalog.Candidates[index].FieldPath = "id"
	}
	return catalog
}

func TestApplyCommandsCreatesRecipeSafeBackendIdentities(t *testing.T) {
	workspace, results, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "browser-request-with-hyphens", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(workspace.Documents) != 1 || len(workspace.Tabs) != 1 {
		t.Fatalf("workspace=%#v results=%#v", workspace, results)
	}
	safe := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	if !safe.MatchString(results[0].OutputID) || results[0].OutputID != workspace.Documents[0].Output.ID {
		t.Fatalf("backend generated invalid output identity %q", results[0].OutputID)
	}
	if results[0].TabID == "" || results[0].OccurrenceID != RootOccurrenceID {
		t.Fatalf("result=%#v", results[0])
	}
	wire, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"columns":[]`) {
		t.Fatalf("empty columns must remain an array on the wire: %s", wire)
	}
}

func TestApplyCommandsSetsAndClearsPopulationUsingSemanticRoute(t *testing.T) {
	catalog := commandCatalog()
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, _, err = ApplyCommands(workspace, catalog, "set-population", []Command{{
		Type: CommandSetTablePopulation, OutputID: outputID, SelectionRevisionID: "selection-1", EdgeIDs: []string{"patient-encounter"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	population := workspace.Documents[0].Population
	if population == nil || population.SelectionRevisionID != "selection-1" || len(population.Route) != 1 || population.Route[0] != (PopulationRouteStep{ResourceType: "Encounter", Relationship: "encounters"}) {
		t.Fatalf("population = %#v", population)
	}
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "patient-encounter") {
		t.Fatalf("catalog edge ID was persisted in population route: %s", encoded)
	}
	workspace, _, err = ApplyCommands(workspace, catalog, "clear-population", []Command{{Type: CommandClearTablePopulation, OutputID: outputID}})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Documents[0].Population != nil {
		t.Fatalf("population was not cleared: %#v", workspace.Documents[0].Population)
	}
}

func TestApplyCommandsSetsAndClearsContributorExplicitly(t *testing.T) {
	catalog := commandCatalog()
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, added, err := ApplyCommands(workspace, catalog, "aggregate", []Command{{
		Type: CommandAddColumnSource, OutputID: outputID, OccurrenceID: RootOccurrenceID,
		Source: &ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{Operation: "COUNT"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	columnID := added[0].Column
	predicate := &ContributorPredicate{CandidateID: "patient-id", Operator: ContributorEquals, Value: &ContributorValue{Kind: ContributorString, String: stringPtr("active")}}
	set := Command{Type: CommandSetColumnContributor, OutputID: outputID, Column: columnID, Contributor: predicate}
	workspace, _, err = ApplyCommands(workspace, catalog, "set-contributor", []Command{set})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Documents[0].Columns[0].Contributor == nil || workspace.Documents[0].Columns[0].Contributor.CandidateID != "patient-id" {
		t.Fatalf("contributor was not set: %#v", workspace.Documents[0].Columns[0].Contributor)
	}
	workspace, _, err = ApplyCommands(workspace, catalog, "set-contributor-retry", []Command{set})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, err = ApplyCommands(workspace, catalog, "clear-contributor", []Command{{Type: CommandClearColumnContributor, OutputID: outputID, Column: columnID}})
	if err != nil {
		t.Fatal(err)
	}
	if workspace.Documents[0].Columns[0].Contributor != nil {
		t.Fatalf("contributor was not cleared: %#v", workspace.Documents[0].Columns[0].Contributor)
	}
	workspace, _, err = ApplyCommands(workspace, catalog, "clear-contributor-retry", []Command{{Type: CommandClearColumnContributor, OutputID: outputID, Column: columnID}})
	if err != nil {
		t.Fatal(err)
	}
}

func stringPtr(value string) *string { return &value }

func TestApplyCommandsAllowsIndependentOccurrencesThroughOneRelationship(t *testing.T) {
	catalog := commandCatalog()
	catalog.RoutePolicy.AllowRepeatedEdges = false
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, results, err := ApplyCommands(workspace, catalog, "contributors", []Command{
		{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"},
		{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"},
	})
	if err != nil {
		t.Fatal(err)
	}
	children := workspace.Documents[0].Route.Children
	if len(results) != 2 || len(children) != 2 {
		t.Fatalf("route results=%#v children=%#v", results, children)
	}
	if children[0].OccurrenceID == children[1].OccurrenceID {
		t.Fatalf("independent contributors share occurrence identity: %#v", children)
	}
	for index, child := range children {
		if child.ResourceType != "Encounter" || child.Relationship != "encounters" || child.OccurrenceID != results[index].OccurrenceID {
			t.Fatalf("child[%d]=%#v result=%#v", index, child, results[index])
		}
	}
}

func TestApplyCommandsSetTableRootPreservesSameRootAndRejectsDestructiveRebase(t *testing.T) {
	catalog := commandCatalog()
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{{Kind: Kind, Output: Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Patient", Children: []RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}}, Columns: []Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: RootOccurrenceID, Source: ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}},
		Tabs:      []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	preserved, _, err := ApplyCommands(workspace, catalog, "same-root", []Command{{Type: CommandSetTableRoot, OutputID: "patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(preserved.Documents[0].Route.Children) != 1 || len(preserved.Documents[0].Columns) != 1 {
		t.Fatalf("same-root command discarded configured meaning: %#v", preserved.Documents[0])
	}
	if _, _, err := ApplyCommands(workspace, catalog, "change-root", []Command{{Type: CommandSetTableRoot, OutputID: "patients", RootNodeID: "encounter"}}); err == nil || !strings.Contains(err.Error(), "ROOT_REBASE_REQUIRED") {
		t.Fatalf("destructive root rebase error = %v", err)
	}
}

func TestApplyCommandsRejectsPopulationRouteBeyondCatalogMaxHops(t *testing.T) {
	catalog := commandCatalog()
	maxHops := 0
	catalog.RoutePolicy.MaxHops = &maxHops
	catalog.RoutePolicy.Unbounded = false
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, catalog, "set-population", []Command{{Type: CommandSetTablePopulation, OutputID: created[0].OutputID, SelectionRevisionID: "selection-1", EdgeIDs: []string{"patient-encounter"}}})
	if err == nil || !strings.Contains(err.Error(), "ROUTE_TOO_LONG") {
		t.Fatalf("population over-depth error = %v, want ROUTE_TOO_LONG", err)
	}
}

func TestApplyCommandsAcceptsCorrelatedLookupWithoutLegacyPath(t *testing.T) {
	catalog := CatalogSnapshot{
		APIVersion: APIVersion, Kind: CatalogKind, Project: "project", ExplorerID: "explorer",
		SourceGeneration: "generation", AuthorizationScopeDigest: "scope", SnapshotToken: "sha256:snapshot", Complete: true,
		Nodes:       []CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		RoutePolicy: RoutePolicy{Unbounded: true},
	}
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Observations"},
		Documents: []Document{{Kind: Kind, Output: Output{ID: "observations", Title: "Observations"}, RootResourceType: "Observation", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"}}},
		Tabs:      []Tab{{ID: "observations", Title: "Observations", OutputID: "observations", Order: 0, Visible: true}},
	}
	source := &ColumnSource{Kind: SourceObservationComponentByCode, Lookup: &LookupSource{
		Binding: &fhirschema.CorrelatedBinding{OwnerPath: "component[]", KeyPath: "component[].code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
		Key:     &fhirschema.CorrelatedKey{System: "urn:study:A", Code: "shared"},
	}}
	updated, results, err := ApplyCommands(workspace, catalog, "add-correlated", []Command{{Type: CommandAddColumnSource, OutputID: "observations", OccurrenceID: RootOccurrenceID, Source: source}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(updated.Documents[0].Columns) != 1 {
		t.Fatalf("results=%#v workspace=%#v", results, updated)
	}
	column := updated.Documents[0].Columns[0]
	if column.Source.Lookup == nil || column.Source.Lookup.Binding == nil || column.Source.Lookup.Path != "" || column.LogicalType != "decimal" {
		t.Fatalf("correlated source was not preserved as typed input: %#v", column)
	}
}

func TestApplyCommandsAcceptsAncestorAwareExtensionAndRejectsLegacyWrite(t *testing.T) {
	catalog := CatalogSnapshot{
		APIVersion: APIVersion, Kind: CatalogKind, Project: "project", ExplorerID: "explorer",
		SourceGeneration: "generation", AuthorizationScopeDigest: "scope", SnapshotToken: "sha256:snapshot", Complete: true,
		Nodes: []CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}, RoutePolicy: RoutePolicy{Unbounded: true},
	}
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Observations"},
		Documents: []Document{{Kind: Kind, Output: Output{ID: "observations", Title: "Observations"}, RootResourceType: "Observation", Route: RouteNode{OccurrenceID: RootOccurrenceID, ResourceType: "Observation"}}},
		Tabs:      []Tab{{ID: "observations", Title: "Observations", OutputID: "observations", Order: 0, Visible: true}},
	}
	typed := &ColumnSource{Kind: SourceExtensionByURL, Lookup: &LookupSource{Extension: &fhirschema.ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueString", LogicalType: "string",
	}}}
	updated, results, err := ApplyCommands(workspace, catalog, "add-extension", []Command{{Type: CommandAddColumnSource, OutputID: "observations", OccurrenceID: RootOccurrenceID, Source: typed}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(updated.Documents[0].Columns) != 1 || updated.Documents[0].Columns[0].Source.Lookup.Extension == nil {
		t.Fatalf("typed extension result = %#v", updated)
	}
	legacy := &ColumnSource{Kind: SourceExtensionByURL, Lookup: &LookupSource{Match: "leaf", Path: "extension[].valueString"}}
	if _, _, err := ApplyCommands(workspace, catalog, "legacy-extension", []Command{{Type: CommandAddColumnSource, OutputID: "observations", OccurrenceID: RootOccurrenceID, Source: legacy}}); err == nil || !strings.Contains(err.Error(), "explicit typed binding") {
		t.Fatalf("legacy extension write error = %v", err)
	}
	wrongArm := &ColumnSource{Kind: SourceExtensionByURL, Lookup: &LookupSource{Extension: &fhirschema.ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:parent:left", "urn:leaf"}, ValuePath: "valueInteger", LogicalType: "string",
	}}}
	if _, _, err := ApplyCommands(workspace, catalog, "wrong-extension-arm", []Command{{Type: CommandAddColumnSource, OutputID: "observations", OccurrenceID: RootOccurrenceID, Source: wrongArm}}); err == nil || !strings.Contains(err.Error(), "incompatible") {
		t.Fatalf("wrong extension arm error = %v", err)
	}
	missingParent := &ColumnSource{Kind: SourceExtensionByURL, Lookup: &LookupSource{Extension: &fhirschema.ExtensionBinding{
		OwnerPath: "extension[].extension[]", URLPath: []string{"urn:leaf"}, ValuePath: "valueString", LogicalType: "string",
	}}}
	if _, _, err := ApplyCommands(workspace, catalog, "missing-extension-parent", []Command{{Type: CommandAddColumnSource, OutputID: "observations", OccurrenceID: RootOccurrenceID, Source: missingParent}}); err == nil || !strings.Contains(err.Error(), "one URL per extension boundary") {
		t.Fatalf("missing extension parent error = %v", err)
	}
}

func TestApplyCommandsRequestRequiresSemanticsV3(t *testing.T) {
	request := ApplyCommandsRequest{CommandID: "cmd", SnapshotToken: "token", Commands: []Command{{Type: CommandDeleteTable, OutputID: "out"}}}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "UNSUPPORTED_SEMANTICS_VERSION") {
		t.Fatalf("version zero error=%v", err)
	}
	request.SemanticsVersion = CurrentSemanticsVersion
	if err := request.Validate(); err != nil {
		t.Fatalf("current semantics version rejected: %v", err)
	}
}

func TestApplyCommandsOwnsNestedRouteAndColumnIdentities(t *testing.T) {
	workspace, create, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := create[0].OutputID
	workspace, route, err := ApplyCommands(workspace, commandCatalog(), "route", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, columns, err := ApplyCommands(workspace, commandCatalog(), "column", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: route[0].OccurrenceID, CandidateID: "encounter-id"}})
	if err != nil {
		t.Fatal(err)
	}
	if route[0].OccurrenceID == "" || columns[0].Column == "" || workspace.Documents[0].Columns[0].Column != columns[0].Column {
		t.Fatalf("route=%#v columns=%#v workspace=%#v", route, columns, workspace)
	}
}

func TestApplyCommandsUpdatesRouteEdgeWithoutReplacingOccurrenceState(t *testing.T) {
	catalog := commandCatalog()
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "patient-encounter-secondary", FromNodeID: "patient", ToNodeID: "encounter", Label: "researchEncounters"})
	workspace, create, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := create[0].OutputID
	workspace, route, err := ApplyCommands(workspace, catalog, "route", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	occurrenceID := route[0].OccurrenceID
	workspace, _, err = ApplyCommands(workspace, catalog, "column", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: occurrenceID, CandidateID: "encounter-id"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Route.Children[0].Children = []RouteNode{{OccurrenceID: "nested", ResourceType: "Encounter", Relationship: "revisits"}}

	updated, _, err := ApplyCommands(workspace, catalog, "update-edge", []Command{{Type: CommandUpdateRouteEdge, OutputID: outputID, OccurrenceID: occurrenceID, EdgeID: "patient-encounter-secondary"}})
	if err != nil {
		t.Fatal(err)
	}
	child := updated.Documents[0].Route.Children[0]
	if child.OccurrenceID != occurrenceID || child.Relationship != "researchEncounters" || len(child.Children) != 1 || child.Children[0].OccurrenceID != "nested" {
		t.Fatalf("updated route=%#v", child)
	}
	if len(updated.Documents[0].Columns) != 1 || updated.Documents[0].Columns[0].OccurrenceID != occurrenceID {
		t.Fatalf("columns were not preserved: %#v", updated.Documents[0].Columns)
	}
}

func TestApplyCommandsSetsRouteMatchModeWithoutChangingFeatureState(t *testing.T) {
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, routes, err := ApplyCommands(workspace, commandCatalog(), "route", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	occurrenceID := routes[0].OccurrenceID

	updated, _, err := ApplyCommands(workspace, commandCatalog(), "required", []Command{{Type: CommandSetRouteMatchMode, OutputID: outputID, OccurrenceID: occurrenceID, MatchMode: RouteMatchRequired}})
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Documents[0].Route.Children[0].MatchMode; got != RouteMatchRequired {
		t.Fatalf("matchMode=%q, want REQUIRED", got)
	}
	updated, _, err = ApplyCommands(updated, commandCatalog(), "optional", []Command{{Type: CommandSetRouteMatchMode, OutputID: outputID, OccurrenceID: occurrenceID, MatchMode: RouteMatchOptional}})
	if err != nil {
		t.Fatal(err)
	}
	if got := updated.Documents[0].Route.Children[0].MatchMode; got != RouteMatchOptional {
		t.Fatalf("matchMode=%q, want OPTIONAL", got)
	}
	_, _, err = ApplyCommands(updated, commandCatalog(), "root", []Command{{Type: CommandSetRouteMatchMode, OutputID: outputID, OccurrenceID: RootOccurrenceID, MatchMode: RouteMatchRequired}})
	if err == nil || !strings.Contains(err.Error(), "non-root") {
		t.Fatalf("root match-mode error=%v", err)
	}
}

func TestApplyCommandsRejectsRouteUpdateThatRepeatsADescendantEdge(t *testing.T) {
	catalog := commandCatalog()
	catalog.RoutePolicy.AllowRepeatedEdges = false
	catalog.Edges = append(catalog.Edges,
		CatalogEdge{ID: "patient-encounter-secondary", FromNodeID: "patient", ToNodeID: "encounter", Label: "researchEncounters"},
		CatalogEdge{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"},
	)
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, encounter, err := ApplyCommands(workspace, catalog, "encounter", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter-secondary"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, patient, err := ApplyCommands(workspace, catalog, "patient", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: encounter[0].OccurrenceID, EdgeID: "encounter-patient"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, err = ApplyCommands(workspace, catalog, "descendant", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: patient[0].OccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, catalog, "update", []Command{{Type: CommandUpdateRouteEdge, OutputID: outputID, OccurrenceID: encounter[0].OccurrenceID, EdgeID: "patient-encounter"}})
	if err == nil || !strings.Contains(err.Error(), "already used in this route") {
		t.Fatalf("route update error=%v", err)
	}
}

func TestApplyCommandsRejectsReusingRelationshipFromAnotherOccurrence(t *testing.T) {
	catalog := commandCatalog()
	catalog.RoutePolicy.AllowRepeatedEdges = false
	catalog.Edges = append(catalog.Edges, CatalogEdge{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"})
	workspace, create, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := create[0].OutputID
	workspace, encounter, err := ApplyCommands(workspace, catalog, "encounter", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, patient, err := ApplyCommands(workspace, catalog, "patient", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: encounter[0].OccurrenceID, EdgeID: "encounter-patient"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, catalog, "repeat", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: patient[0].OccurrenceID, EdgeID: "patient-encounter"}})
	if err == nil || !strings.Contains(err.Error(), "already used in this route") {
		t.Fatalf("error=%v", err)
	}
}

func TestApplyCommandsRejectsRouteBeyondCatalogMaxHopsWithoutMutation(t *testing.T) {
	catalog := commandCatalog()
	maxHops := 0
	catalog.RoutePolicy.MaxHops = &maxHops
	catalog.RoutePolicy.Unbounded = false
	workspace, create, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := create[0].OutputID
	_, _, err = ApplyCommands(workspace, catalog, "route", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err == nil || !strings.Contains(err.Error(), "ROUTE_TOO_LONG") {
		t.Fatalf("over-depth route error = %v, want ROUTE_TOO_LONG", err)
	}
	if len(workspace.Documents[0].Route.Children) != 0 {
		t.Fatalf("rejected route mutated input = %#v", workspace.Documents[0].Route)
	}

	maxHops = 1
	allowed, _, err := ApplyCommands(workspace, catalog, "route-allowed", []Command{{Type: CommandAddRoute, OutputID: outputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil || len(allowed.Documents[0].Route.Children) != 1 {
		t.Fatalf("bounded route = %#v, err=%v", allowed.Documents[0].Route, err)
	}
}

func TestApplyCommandsAddsFilterAndChartWithoutMakingColumnTableVisible(t *testing.T) {
	catalog := commandCatalog()
	catalog.Candidates[0].Filterable = true
	catalog.Candidates[0].Chartable = true
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, added, err := ApplyCommands(workspace, catalog, "filter", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: RootOccurrenceID, CandidateID: "patient-id", InitialPresentation: InitialPresentationFilter}})
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents[0].Columns) != 1 {
		t.Fatalf("columns=%#v", workspace.Documents[0].Columns)
	}
	column := workspace.Documents[0].Columns[0]
	if column.Column != added[0].Column || column.Table != nil || column.Filter == nil || column.Chart != nil {
		t.Fatalf("filter-only column=%#v", column)
	}

	workspace, _, err = ApplyCommands(workspace, catalog, "chart", []Command{{Type: CommandAddColumn, OutputID: outputID, OccurrenceID: RootOccurrenceID, CandidateID: "patient-id", InitialPresentation: InitialPresentationChart}})
	if err != nil {
		t.Fatal(err)
	}
	column = workspace.Documents[0].Columns[0]
	if len(workspace.Documents[0].Columns) != 1 || column.Table != nil || column.Filter == nil || column.Chart == nil || column.Chart.Type != "bar" {
		t.Fatalf("filter-and-chart column=%#v", column)
	}
}

func TestApplyCommandsRejectsUnsupportedInitialPresentation(t *testing.T) {
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, commandCatalog(), "filter", []Command{{Type: CommandAddColumn, OutputID: created[0].OutputID, OccurrenceID: RootOccurrenceID, CandidateID: "patient-id", InitialPresentation: InitialPresentationFilter}})
	if err == nil || !strings.Contains(err.Error(), "does not support filters") {
		t.Fatalf("error=%v", err)
	}
}

func TestApplyCommandsRejectsBatchAtomically(t *testing.T) {
	original := emptyCommandWorkspace()
	_, _, err := ApplyCommands(original, commandCatalog(), "batch", []Command{
		{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"},
		{Type: CommandDeleteTable, OutputID: "missing"},
	})
	if err == nil {
		t.Fatal("invalid second command unexpectedly succeeded")
	}
	if len(original.Documents) != 0 || len(original.Tabs) != 0 {
		t.Fatalf("input mutated after rejected batch: %#v", original)
	}
}

func TestApplyCommandsSourceEditsPreserveColumnIdentityAndAreIdempotent(t *testing.T) {
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	count := &ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{Operation: "COUNT"}}
	workspace, added, err := ApplyCommands(workspace, commandCatalog(), "count", []Command{{Type: CommandAddColumnSource, OutputID: outputID, OccurrenceID: RootOccurrenceID, Source: count, Title: "Patient count"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 1 || len(workspace.Documents[0].Columns) != 1 {
		t.Fatalf("added=%#v columns=%#v", added, workspace.Documents[0].Columns)
	}
	column := workspace.Documents[0].Columns[0]
	if column.Source.Aggregate == nil || column.Source.Aggregate.Operation != "COUNT" || column.Label != "Patient count" {
		t.Fatalf("column=%#v", column)
	}
	workspace, replayed, err := ApplyCommands(workspace, commandCatalog(), "count-retry", []Command{{Type: CommandAddColumnSource, OutputID: outputID, OccurrenceID: RootOccurrenceID, Source: count, Title: "Different title"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Documents[0].Columns) != 1 || len(replayed) != 1 || replayed[0].Column != column.Column {
		t.Fatalf("idempotent replay columns=%#v result=%#v", workspace.Documents[0].Columns, replayed)
	}
	presentation := workspace.Documents[0].Columns[0].Table
	updated, results, err := ApplyCommands(workspace, commandCatalog(), "exists", []Command{{Type: CommandUpdateColumnSource, OutputID: outputID, Column: column.Column, Source: &ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{Operation: "EXISTS"}}}})
	if err != nil {
		t.Fatal(err)
	}
	updatedColumn := updated.Documents[0].Columns[0]
	if len(results) != 1 || results[0].Column != column.Column || updatedColumn.Column != column.Column || updatedColumn.Label != column.Label || updatedColumn.Table == nil || presentation == nil || *updatedColumn.Table.Visible != *presentation.Visible {
		t.Fatalf("source update changed identity/presentation: before=%#v after=%#v result=%#v", column, updatedColumn, results)
	}
	if updatedColumn.Source.Aggregate == nil || updatedColumn.Source.Aggregate.Operation != "EXISTS" {
		t.Fatalf("updated source=%#v", updatedColumn.Source)
	}
	if updatedColumn.LogicalType != "boolean" {
		t.Fatalf("source update did not rederive logical type: %q", updatedColumn.LogicalType)
	}
	workspaceA, createdA, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "same-create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	workspaceB, createdB, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "same-create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	rawSource, err := json.Marshal(ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{Operation: "COUNT"}})
	if err != nil {
		t.Fatal(err)
	}
	var decodedSource ColumnSource
	if err := json.Unmarshal(rawSource, &decodedSource); err != nil {
		t.Fatal(err)
	}
	workspaceA, addedA, err := ApplyCommands(workspaceA, commandCatalog(), "same-source", []Command{{Type: CommandAddColumnSource, OutputID: createdA[0].OutputID, OccurrenceID: RootOccurrenceID, Source: &ColumnSource{Kind: SourceAggregate, Aggregate: &AggregateSource{Operation: "COUNT"}}, Title: "Count"}})
	if err != nil {
		t.Fatal(err)
	}
	_, addedB, err := ApplyCommands(workspaceB, commandCatalog(), "same-source", []Command{{Type: CommandAddColumnSource, OutputID: createdB[0].OutputID, OccurrenceID: RootOccurrenceID, Source: &decodedSource, Title: "Count"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(addedA) != 1 || len(addedB) != 1 || addedA[0].Column != addedB[0].Column {
		t.Fatalf("semantic source identity changed with pointer allocation: A=%#v B=%#v", addedA, addedB)
	}
}

func TestApplyCommandsSourceValidationChecksCatalogProjectionMode(t *testing.T) {
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), commandCatalog(), "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = ApplyCommands(workspace, commandCatalog(), "bad-mode", []Command{{Type: CommandAddColumnSource, OutputID: created[0].OutputID, OccurrenceID: RootOccurrenceID, Source: &ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "FIRST"}}}})
	if err == nil || !strings.Contains(err.Error(), "projection mode") {
		t.Fatalf("unsupported source projection accepted: %v", err)
	}
}

func TestApplyCommandsAddsUnacknowledgedRelatedSelectionForChildScalar(t *testing.T) {
	catalog := commandCatalog()
	workspace, created, err := ApplyCommands(emptyCommandWorkspace(), catalog, "create", []Command{{Type: CommandCreateTable, Title: "Patients", RootNodeID: "patient"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, route, err := ApplyCommands(workspace, catalog, "route", []Command{{Type: CommandAddRoute, OutputID: created[0].OutputID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "patient-encounter"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, err = ApplyCommands(workspace, catalog, "child", []Command{{Type: CommandAddColumn, OutputID: created[0].OutputID, OccurrenceID: route[0].OccurrenceID, CandidateID: "encounter-id"}})
	if err != nil {
		t.Fatal(err)
	}
	selection := workspace.Documents[0].Columns[0].Source.Field.RelatedSelection
	if selection == nil || selection.Kind != "first-by-resource-key" || selection.Acknowledged {
		t.Fatalf("child source related selection=%#v", selection)
	}
	if err := workspace.ValidateForPublication(); err == nil || !strings.Contains(err.Error(), "UNACKNOWLEDGED_RELATED_FIRST") {
		t.Fatalf("publication error=%v", err)
	}
	selection.Acknowledged = true
	if err := workspace.ValidateForPublication(); err != nil {
		t.Fatalf("acknowledged child source rejected: %v", err)
	}
}
