package compilation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func fixtureSnapshot() capability.Snapshot {
	return fixtureSnapshotForProject("project-a")
}

func fixtureSnapshotForProject(project string) capability.Snapshot {
	identity := capability.SnapshotIdentity{Project: project, Generation: "generation-a", AuthorizationScopeDigest: "scope-a", SchemaDigest: "schema-a", CompilerVersion: "compiler-a"}
	policy := capability.Policy{Route: capability.RoutePolicy{Version: "route-a", AllowsRepeatedEdges: true, AllowsSelfLoops: true}, Projection: capability.ProjectionPolicy{Version: "projection-a", Modes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionIndexed, capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}}}
	nodes := []capability.Node{{ID: "n_patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient"}, {ID: "n_encounter", ResourceType: "Encounter", RowRootEligible: true, RowGrain: "resource"}}
	edges := []capability.Edge{{ID: "e_encounter", FromNodeID: "n_patient", ToNodeID: "n_encounter", Label: "encounters"}, {ID: "e_self", FromNodeID: "n_encounter", ToNodeID: "n_encounter", Label: "revisits"}}
	ops := []capability.Operation{capability.OperationSelect, capability.OperationFilter, capability.OperationChart}
	candidates := []capability.Candidate{
		{ID: "c_patient_id", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient.id", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, SupportedOperations: ops},
		{ID: "c_patient_given", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[].given[]", Label: "Patient.name.given", LogicalType: "string", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}, {Path: "name[].given[]", MaxItems: 3}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionIndexed, capability.ProjectionFirst, capability.ProjectionArray}, SupportedOperations: ops},
		{ID: "c_patient_family", NodeID: "n_patient", ResourceType: "Patient", FieldPath: "name[].family", Label: "Patient.name.family", LogicalType: "string", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}}, ProjectionModes: []capability.ProjectionMode{capability.ProjectionIndexed, capability.ProjectionFirst, capability.ProjectionArray}, SupportedOperations: ops},
		{ID: "c_encounter_code", NodeID: "n_encounter", ResourceType: "Encounter", FieldPath: "code.coding[].code", Label: "Encounter.code.coding[].code", LogicalType: "string", ProjectionModes: []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray, capability.ProjectionDistinctArray}, SupportedOperations: ops},
	}
	return capability.NewSnapshot(identity, policy, capability.StatusReady, true, false, nodes, edges, candidates, nil)
}

func TestProjectionWireModesPreserveDistinctArray(t *testing.T) {
	if got := wireProjectionMode(capability.ProjectionArray); got != "ALL" {
		t.Fatalf("array wire mode = %q, want ALL", got)
	}
	if got := wireProjectionMode(capability.ProjectionDistinctArray); got != "DISTINCT" {
		t.Fatalf("distinct array wire mode = %q, want DISTINCT", got)
	}
}

func TestRouteDepthUsesStableErrorAtCommandAndCompileBoundaries(t *testing.T) {
	snapshot := fixtureSnapshot()
	snapshot.Policy.Route.MaxHops = 1
	catalog := catalogFromCapability(snapshot, "explorer-a")
	workspace, created, err := authoringv2.ApplyCommands(
		authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Builder"}},
		catalog,
		"create",
		[]authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: "Patients", RootNodeID: "n_patient"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	outputID := created[0].OutputID
	workspace, _, err = authoringv2.ApplyCommands(workspace, catalog, "first-hop", []authoringv2.Command{{
		Type: authoringv2.CommandAddRoute, OutputID: outputID, ParentOccurrenceID: authoringv2.RootOccurrenceID, EdgeID: "e_encounter",
	}})
	if err != nil {
		t.Fatal(err)
	}
	parentOccurrenceID := workspace.Documents[0].Route.Children[0].OccurrenceID
	_, _, err = authoringv2.ApplyCommands(workspace, catalog, "over-depth", []authoringv2.Command{{
		Type: authoringv2.CommandAddRoute, OutputID: outputID, ParentOccurrenceID: parentOccurrenceID, EdgeID: "e_self",
	}})
	if err == nil || !strings.Contains(err.Error(), "ROUTE_TOO_LONG") {
		t.Fatalf("command over-depth route error = %v, want ROUTE_TOO_LONG", err)
	}

	_, err = Compile(context.Background(), "project-a", "explorer-a", authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{{
			OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters", Children: []authoringv2.RouteNode{{
				OccurrenceID: "repeat-encounter", ResourceType: "Encounter", Relationship: "revisits",
			}},
		}}},
	}, snapshot)
	var compileErr *Error
	if !errors.As(err, &compileErr) || compileErr.Code != "ROUTE_TOO_LONG" {
		t.Fatalf("compile over-depth route error = %v, want ROUTE_TOO_LONG", err)
	}
}

func TestCompileRejectsCapabilityMismatchedLogicalTypeAndProjection(t *testing.T) {
	base := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
	}
	for _, test := range []struct {
		name   string
		mutate func(*authoringv2.Document)
		code   string
	}{
		{name: "logical type", mutate: func(document *authoringv2.Document) { document.Columns[0].LogicalType = "integer" }, code: "CAPABILITY_LOGICAL_TYPE_MISMATCH"},
		{name: "projection mode", mutate: func(document *authoringv2.Document) { document.Columns[0].Source.Field.ProjectionMode = "FIRST" }, code: "UNSUPPORTED_PROJECTION_MODE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := base
			test.mutate(&document)
			_, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
			var compileErr *Error
			if !errors.As(err, &compileErr) || compileErr.Code != test.code {
				t.Fatalf("compile error = %v, want %s", err, test.code)
			}
		})
	}
}

func TestCompileWorkspaceAcceptsCommandGeneratedCandidateSelection(t *testing.T) {
	snapshot := fixtureSnapshot()
	catalog := catalogFromCapability(snapshot, "explorer-a")
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Builder"}}
	var created []authoringv2.CommandResult
	var err error
	workspace, created, err = authoringv2.ApplyCommands(workspace, catalog, "create", []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: "Patients", RootNodeID: "n_patient"}})
	if err != nil {
		t.Fatal(err)
	}
	workspace, _, err = authoringv2.ApplyCommands(workspace, catalog, "column", []authoringv2.Command{{Type: authoringv2.CommandAddColumn, OutputID: created[0].OutputID, OccurrenceID: authoringv2.RootOccurrenceID, CandidateID: "c_patient_id"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, ResolvedInputs{}); err != nil {
		t.Fatalf("command-generated workspace rejected: %v", err)
	}
}

func TestCompileWorkspaceSetsCanonicalResolvedInputsIdentity(t *testing.T) {
	snapshot := fixtureSnapshot()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer: authoringv2.ExplorerMetadata{Title: "Builder"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	first, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, ResolvedInputs{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, ResolvedInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolvedInputsDigest == "" || first.ResolvedInputsDigest != second.ResolvedInputsDigest || !strings.HasPrefix(first.ResolvedInputsDigest, "sha256:") {
		t.Fatalf("resolved input identity first=%q second=%q", first.ResolvedInputsDigest, second.ResolvedInputsDigest)
	}
	snapshot.Identity.ShapeDigest = "different-shape"
	third, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, ResolvedInputs{})
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolvedInputsDigest == third.ResolvedInputsDigest {
		t.Fatal("capability shape change did not change resolved input identity")
	}
}

func TestCompileWorkspaceCarriesPopulationIntoRecipeAndReceiptIdentity(t *testing.T) {
	snapshot := fixtureSnapshot()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:      authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
			Population: &authoringv2.Population{SelectionRevisionID: "selection-1", Route: []authoringv2.PopulationRouteStep{}},
			Columns:    []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
	}
	resolved := ResolvedInputs{Populations: []ResolvedPopulation{{
		OutputID: "patients", SelectionRevisionID: "selection-1", MembershipDigest: "sha256:members", MemberCount: 3,
		ResourceType: "Patient", Route: []authoringv2.PopulationRouteStep{},
	}}}
	first, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, resolved)
	if err != nil {
		t.Fatal(err)
	}
	population := first.Bundle.Outputs[0].Population
	if population == nil || population.SelectionRevisionID != "selection-1" || population.MembershipDigest != "sha256:members" || population.MemberCount != 3 || population.ResourceType != "Patient" {
		t.Fatalf("compiled population = %#v", population)
	}
	if first.ResolvedInputsDigest == "" || !strings.HasPrefix(first.ResolvedInputsDigest, "sha256:") {
		t.Fatalf("resolved input digest = %q", first.ResolvedInputsDigest)
	}
	resolved.Populations[0].SelectionRevisionID = "selection-2"
	workspace.Documents[0].Population.SelectionRevisionID = "selection-2"
	second, err := CompileWorkspace(context.Background(), "project-a", "explorer-a", workspace, snapshot, resolved)
	if err != nil {
		t.Fatal(err)
	}
	if first.ResolvedInputsDigest == second.ResolvedInputsDigest || first.RecipeDigest == second.RecipeDigest {
		t.Fatalf("population identity did not change compilation identity: first=%q/%q second=%q/%q", first.ResolvedInputsDigest, first.RecipeDigest, second.ResolvedInputsDigest, second.RecipeDigest)
	}
}

func TestCompileIndexedProjectionEmitsLosslessScalarContract(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{{Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}}, Table: &authoringv2.TablePresentation{Visible: &visible}}},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	hasReason := func(want string) bool {
		for _, reason := range result.OutputContract.LossReasons {
			if reason == want {
				return true
			}
		}
		return false
	}
	if !result.OutputContract.Lossless || result.OutputContract.MLReady || result.OutputContract.RowMultiplication != "none" || hasReason("INDEXED_PROJECTION_TRUNCATION") || len(result.OutputContract.LossReasons) != 0 {
		t.Fatalf("output contract = %#v", result.OutputContract)
	}
	for _, emission := range result.EmittedColumns {
		if emission.Shape == "repeated_count" && (!emission.Lossless || len(emission.LossReasons) != 0) {
			t.Fatalf("complete root indexed count emission = %#v", emission)
		}
	}
	wantFields := []string{"given__0__0", "given__0__1", "given__0__2", "given__1__0", "given__1__1", "given__1__2"}
	fields := result.Bundle.Outputs[0].Fields
	if len(fields) != len(wantFields)+3 {
		t.Fatalf("fields = %d, want %d values plus three counts: %#v", len(fields), len(wantFields), fields)
	}
	for index, want := range wantFields {
		if fields[index].Name != want {
			t.Fatalf("field[%d] = %q, want %q", index, fields[index].Name, want)
		}
	}
	if len(result.Bundle.Outputs[0].Fields) != 9 {
		t.Fatalf("fields including counts = %#v, want six values plus three counts", result.Bundle.Outputs[0].Fields)
	}
	if got := result.EmittedColumns[0].Coordinates; len(got) != 2 || got[0].Index != 0 || got[1].Index != 0 {
		t.Fatalf("first coordinates = %#v", got)
	}
	if len(result.IdentityMappings) != 1 || len(result.IdentityMappings[0].EmissionIDs) != 9 {
		t.Fatalf("identity mappings = %#v", result.IdentityMappings)
	}
}

func TestCompileRootRepeatedFirstClaimsLoss(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{{Column: "given", Label: "Given", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name[].given[]", ProjectionMode: "FIRST"}}, Table: &authoringv2.TablePresentation{Visible: &visible}}},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	column := result.OutputContract.Columns[0]
	if result.OutputContract.Lossless || column.Lossless || column.StructuralSuitability != "requires-review" || len(column.LossReasons) != 1 || column.LossReasons[0] != "FIELD_FIRST_REDUCTION" {
		t.Fatalf("root repeated FIRST contract=%#v", result.OutputContract)
	}
}

func TestCompileIndexedProjectionTracksEveryOwnerOfSharedBoundaryCount(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Columns: []authoringv2.Column{
			{Column: "given", Label: "Given", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name[].given[]", ProjectionMode: "INDEXED"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			{Column: "family", Label: "Family", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "name[].family", ProjectionMode: "INDEXED"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
		},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	for index, column := range result.EmittedColumns {
		if column.PublicColumn != "name__count" {
			continue
		}
		if len(column.AuthoredColumns) != 2 || column.AuthoredColumns[0] != "given" || column.AuthoredColumns[1] != "family" {
			t.Fatalf("shared count owners = %#v", column.AuthoredColumns)
		}
		if got := result.OutputContract.Columns[index].AuthoredColumns; len(got) != 2 || got[0] != "given" || got[1] != "family" {
			t.Fatalf("shared contract owners = %#v", got)
		}
		return
	}
	t.Fatal("shared name__count emission was not produced")
}

func TestCompileAcceptsEquivalentProjectIdentities(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient"},
		Columns: []authoringv2.Column{{
			Column: "project_id", Label: "Project", LogicalType: "string", OccurrenceID: "base",
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID},
			Table:  &authoringv2.TablePresentation{Visible: &visible},
		}},
	}
	snapshot := fixtureSnapshotForProject("HTAN_INT/BForePC")

	for _, project := range []string{"HTAN_INT/BForePC", "HTAN_INT%2FBForePC", "HTAN_INT-BForePC"} {
		t.Run(project, func(t *testing.T) {
			result, err := Compile(context.Background(), project, "explorer-a", document, snapshot)
			if err != nil {
				t.Fatalf("Compile(%q) rejected an equivalent project identity: %v", project, err)
			}
			if got := string(result.Bundle.Outputs[0].Fields[0].Expr.Literal); got != `"HTAN_INT/BForePC"` {
				t.Fatalf("project binding = %s, want canonical project identity", got)
			}
		})
	}

	_, err := Compile(context.Background(), "HTAN_INT/OtherProject", "explorer-a", document, snapshot)
	var compileErr *Error
	if !errors.As(err, &compileErr) || compileErr.Code != "PROJECT_MISMATCH" {
		t.Fatalf("different project error = %v, want PROJECT_MISMATCH", err)
	}
}

func TestCompileSemanticWorkspacePreservesAuthoredColumnsAndTypedSources(t *testing.T) {
	visible, order := true, 0
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients", RowLabel: "People"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
		Columns: []authoringv2.Column{
			{Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: &visible, Order: &order}},
			{Column: "encounter__code", Label: "Encounter code", LogicalType: "string", OccurrenceID: "encounter", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "code.coding[].code", ProjectionMode: "FIRST"}}},
			{Column: "project_id", Label: "Project", LogicalType: "string", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceProjectID}},
		},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"patient_id", "encounter__code", "project_id"}
	for index, column := range result.OutputContract.Columns {
		if column.Column != want[index] || len(column.AuthoredColumns) != 0 || result.EmittedColumns[index].PublicColumn != want[index] || len(result.EmittedColumns[index].AuthoredColumns) != 0 {
			t.Fatalf("column %d = %#v emission=%#v", index, column, result.EmittedColumns[index])
		}
	}
	if got := result.Bundle.Outputs[0].Traversals[0].Alias; got != "encounter" {
		t.Fatalf("traversal alias = %q", got)
	}
	if got := result.Bundle.Outputs[0].TraversalColumnNaming; got != recipe.TraversalColumnNamingAlias {
		t.Fatalf("traversal column naming = %q", got)
	}
	if got := result.Bundle.Outputs[0].RootColumnNaming; got != recipe.RootColumnNamingExact {
		t.Fatalf("root column naming = %q", got)
	}
	if got := string(result.Bundle.Outputs[0].Fields[1].Expr.Literal); got != `"project-a"` {
		t.Fatalf("project binding = %s", got)
	}
}

func TestCompileSemanticWorkspacePreservesSiblingBranchesDeterministically(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients"},
		RootResourceType: "Patient",
		Route: authoringv2.RouteNode{
			OccurrenceID: "base", ResourceType: "Patient",
			Children: []authoringv2.RouteNode{
				{OccurrenceID: "encounter_b", ResourceType: "Encounter", Relationship: "encounters"},
				{OccurrenceID: "encounter_a", ResourceType: "Encounter", Relationship: "encounters"},
			},
		},
		Columns: []authoringv2.Column{
			{Column: "patient_id", Label: "Patient ID", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			{Column: "encounter_a__code", Label: "Encounter A", OccurrenceID: "encounter_a", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "code.coding[].code", ProjectionMode: "FIRST"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			{Column: "encounter_b__code", Label: "Encounter B", OccurrenceID: "encounter_b", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "code.coding[].code", ProjectionMode: "FIRST"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
		},
	}

	first, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	traversals := first.Bundle.Outputs[0].Traversals
	if len(traversals) != 2 || traversals[0].Alias != "encounter_a" || traversals[1].Alias != "encounter_b" {
		t.Fatalf("sibling traversals = %#v", traversals)
	}
	if traversals[0].Fields[0].Name != "code" || traversals[1].Fields[0].Name != "code" {
		t.Fatalf("sibling fields = %#v", traversals)
	}

	document.Route.Children[0], document.Route.Children[1] = document.Route.Children[1], document.Route.Children[0]
	second, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if first.RecipeDigest != second.RecipeDigest {
		t.Fatalf("sibling order changed recipe identity: %q != %q", first.RecipeDigest, second.RecipeDigest)
	}
}

func TestSemanticObservationPivotFreezesOnlyAuthoredComponentColumns(t *testing.T) {
	column := authoringv2.Column{
		Column:       "observation__observation_component_values__GENE_SYMBOL",
		OccurrenceID: "observation",
		Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceObservationComponentByCode, Lookup: &authoringv2.LookupSource{Path: "component[]", Match: "GENE_SYMBOL", ProjectionMode: "FIRST"}},
	}
	pivot, err := semanticObservationPivot(column, "observation", "observation_component_values__GENE_SYMBOL")
	if err != nil {
		t.Fatal(err)
	}
	if pivot.Name != "observation_component_values" || len(pivot.Columns) != 1 || pivot.Columns[0] != "GENE_SYMBOL" || pivot.ItemResourceType != "ObservationComponent" {
		t.Fatalf("pivot = %#v", pivot)
	}
	merged := appendSemanticPivot([]recipe.Pivot{pivot}, recipe.Pivot{Name: pivot.Name, Columns: []string{"TIMEPOINT_LABEL"}})
	if len(merged) != 1 || len(merged[0].Columns) != 2 {
		t.Fatalf("merged pivots = %#v", merged)
	}
}

func TestSemanticNestedOccurrenceUsesGloballyScopedAlias(t *testing.T) {
	if got := semanticAlias("patient__condition"); got != "patient__condition" {
		t.Fatalf("nested alias = %q", got)
	}
	leaf, err := semanticColumnLeaf("patient__condition__code_coding_code", "patient__condition")
	if err != nil || leaf != "code_coding_code" {
		t.Fatalf("nested physical leaf = %q, %v", leaf, err)
	}
}

func TestCompileSemanticAggregateUsesExactPublicName(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "patient_output", Title: "Patients"},
		RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: []authoringv2.RouteNode{{
			OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters",
		}}},
		Columns: []authoringv2.Column{
			{Column: "patient_id", Label: "Patient ID", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
			{Column: "encounter_count", Label: "Encounter count", OccurrenceID: "encounter", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceAggregate, Aggregate: &authoringv2.AggregateSource{Operation: "COUNT"}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
		},
	}

	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	aggregate := result.Bundle.Outputs[0].Traversals[0].Aggregates[0]
	if aggregate.OutputName != "encounter_count" || aggregate.Operation != recipe.AggregateCount {
		t.Fatalf("aggregate = %#v", aggregate)
	}
}

func TestCompileRelatedFirstClaimsLossAndRequiresReview(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patient_output", Title: "Patients"}, RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
		Columns: []authoringv2.Column{
			{Column: "encounter__code", Label: "Encounter code", OccurrenceID: "encounter", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "code.coding[].code", ProjectionMode: "FIRST", RelatedSelection: &authoringv2.RelatedSelection{Kind: "first-by-resource-key"}}}, Table: &authoringv2.TablePresentation{Visible: &visible}},
		},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	column := result.OutputContract.Columns[0]
	if column.Lossless || column.StructuralSuitability != "requires-review" || column.Shape != "scalar" || len(column.LossReasons) != 1 || column.LossReasons[0] != "RELATED_RESOURCE_FIRST_LOSSY" {
		t.Fatalf("related FIRST contract=%#v", column)
	}
}

func TestCompileRelatedIndexedCountClaimsAssociationLoss(t *testing.T) {
	visible := true
	snapshot := fixtureSnapshot()
	for index := range snapshot.Candidates {
		if snapshot.Candidates[index].ID == "c_encounter_code" {
			snapshot.Candidates[index].ProjectionModes = append(snapshot.Candidates[index].ProjectionModes, capability.ProjectionIndexed)
			snapshot.Candidates[index].RepeatedBoundaries = []capability.RepeatedBoundary{{Path: "code.coding[]", MaxItems: 2}}
		}
	}
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patient_output", Title: "Patients"}, RootResourceType: "Patient",
		Route:   authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
		Columns: []authoringv2.Column{{Column: "encounter__code", Label: "Encounter code", OccurrenceID: "encounter", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "code.coding[].code", ProjectionMode: "INDEXED"}}, Table: &authoringv2.TablePresentation{Visible: &visible}}},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if result.OutputContract.Lossless || len(result.OutputContract.LossReasons) != 1 || result.OutputContract.LossReasons[0] != "RELATED_RESOURCE_FIRST_LOSSY" {
		t.Fatalf("related INDEXED contract=%#v", result.OutputContract)
	}
	for _, emission := range result.EmittedColumns {
		if emission.Shape == "repeated_count" && (emission.Lossless || len(emission.LossReasons) != 1 || emission.LossReasons[0] != "RELATED_RESOURCE_FIRST_LOSSY") {
			t.Fatalf("related indexed count emission=%#v", emission)
		}
	}
}

func TestCompileRelatedAllUsesAssociationLossReasonNotFirstSelection(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patient_output", Title: "Patients"}, RootResourceType: "Patient",
		Route:   authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
		Columns: []authoringv2.Column{{Column: "encounter__code", Label: "Encounter code", OccurrenceID: "encounter", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "code.coding[].code", ProjectionMode: "ALL"}}, Table: &authoringv2.TablePresentation{Visible: &visible}}},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	column := result.OutputContract.Columns[0]
	if column.Shape != "array" || column.Lossless || column.StructuralSuitability != "requires-review" || len(column.LossReasons) != 1 || column.LossReasons[0] != "RELATED_RESOURCE_ALL_LOSSY" {
		t.Fatalf("related ALL contract=%#v", column)
	}
}

func TestCompileDistinctValuesAggregateIsArrayAndLossy(t *testing.T) {
	visible := true
	document := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patient_output", Title: "Patients"}, RootResourceType: "Patient",
		Route:   authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Patient"},
		Columns: []authoringv2.Column{{Column: "distinct_names", Label: "Distinct names", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceAggregate, Aggregate: &authoringv2.AggregateSource{Operation: "DISTINCT_VALUES", Path: "name[].family"}}, Table: &authoringv2.TablePresentation{Visible: &visible}}},
	}
	result, err := Compile(context.Background(), "project-a", "explorer-a", document, fixtureSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	column := result.OutputContract.Columns[0]
	if column.Shape != "array" || column.StructuralSuitability != "array" || column.Lossless || len(column.LossReasons) != 2 || column.LossReasons[0] != "AGGREGATE_REDUCTION" || column.LossReasons[1] != "DISTINCT_VALUES_REDUCTION" {
		t.Fatalf("distinct values contract=%#v", column)
	}
}
