package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCodedGroupPreviewWithSourceIdentityKeepsStageInputScoped(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "coded-group-project", DatasetGeneration: "coded-group-generation",
		IncludeAuthResourcePath: true, IncludeSourceIdentity: true,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"BodyStructure/allowed"},
	}
	compiled := lowerConstructionOutput(t, bodyStructureCodedGroupOutput(), bindings)
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"LET __loom_construction_stage_1 = (",
		"FOR root IN @@root_collection",
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"root.auth_resource_path IN @auth_resource_paths",
		"LET __loom_physical_construction_construction_coded_group_raw = (",
		"root.payload[\"includedStructure\"]",
		"AGGREGATE __loom_construction_coded_group_count_1 = SUM(1)",
	} {
		if !strings.Contains(query.Query, expected) {
			t.Errorf("inlined coded-group query is missing %q:\n%s", expected, query.Query)
		}
	}
	for _, unexpected := range []string{"__loom_construction_source_projection", "DOCUMENT(@@root_collection", "IN __loom_construction_input_1"} {
		if strings.Contains(query.Query, unexpected) {
			t.Fatalf("direct-root CODED_GROUP query retained fallback fragment %q:\n%s", unexpected, query.Query)
		}
	}
	if strings.Contains(query.Query, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("composite CODED_GROUP preview gained a single-source identity projection:\n%s", query.Query)
	}
	if got, want := query.PublicColumns, []string{"code_system", "code_version", "code", "source_records"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CODED_GROUP preview public columns = %#v, want %#v", got, want)
	}
	if query.BindVars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("inlined source bypassed restricted authorization: %#v", query.BindVars)
	}
	if paths, ok := query.BindVars["auth_resource_paths"].([]string); !ok || !reflect.DeepEqual(paths, []string{"BodyStructure/allowed"}) {
		t.Fatalf("inlined source changed the authorized paths: %#v", query.BindVars["auth_resource_paths"])
	}
}

func TestCodedGroupTerminalProjectionRetainsProjectionFallback(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "coded-group-project", DatasetGeneration: "coded-group-generation"}
	compiled := lowerConstructionOutput(t, bodyStructureCodedGroupOutput(), bindings)
	query, err := aql.RenderPhysicalPlanWithTerminalProjection(compiled.Plan, "source_records")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Query, "LET __loom_construction_source_projection = (") ||
		!strings.Contains(query.Query, "FOR __loom_construction_input_1 IN __loom_construction_source_projection") ||
		!strings.Contains(query.Query, "DOCUMENT(@@root_collection,") {
		t.Fatalf("terminal projection should retain source projection and document-lookup fallback:\n%s", query.Query)
	}
}

func TestCodedGroupInlinesPopulationSelectedRootScan(t *testing.T) {
	output := bodyStructureCodedGroupOutput()
	output.Population = &recipe.PopulationConstraint{
		SelectionRevisionID: "selection-coded-group",
		MembershipDigest:    "sha256:coded-group-members",
		MemberCount:         2,
		ResourceType:        "BodyStructure",
	}
	bindings := recipe.RuntimeBindings{
		Project: "coded-group-project", DatasetGeneration: "coded-group-generation",
		SelectionProject: "coded-group-project", SelectionMembersCollection: "loom_explorer_selection_members",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"BodyStructure/allowed"},
	}
	compiled := lowerConstructionOutput(t, output, bindings)
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"FOR population_member IN @@population_members_collection",
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"LET root = DOCUMENT(@@root_collection, __loom_physical_population_root_key)",
		"root.payload[\"includedStructure\"]",
	} {
		if !strings.Contains(query.Query, expected) {
			t.Errorf("population-selected coded-group query is missing %q:\n%s", expected, query.Query)
		}
	}
	if strings.Contains(query.Query, "__loom_construction_source_projection") || strings.Contains(query.Query, "__loom_construction_coded_group_source_id") {
		t.Fatalf("population-selected first-stage CODED_GROUP did not inline its physical root scan:\n%s", query.Query)
	}
}

func bodyStructureCodedGroupOutput() recipe.Output {
	const sourceID = "body_structure_id"
	columns := []recipe.StageColumn{
		{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
		{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
		{ID: "code_id", Name: "code", Type: "string", Nullable: true},
		{ID: "count_id", Name: "source_records", Type: "integer"},
	}
	return recipe.Output{
		Name: "body_structures", RootResourceType: "BodyStructure", RowGrain: "resource",
		Fields: []recipe.Field{{Name: sourceID, ColumnID: sourceID, Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: sourceID, Name: sourceID}}, Steps: []recipe.ConstructionStep{{
			ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionCodedGroupOp, CodedGroup: &recipe.ConstructionCodedGroup{
				ConstructionID: "group_codes",
				Source: recipe.ConstructionCodedGroupSource{
					OccurrenceID: "base", ResourceType: "BodyStructure", CodingPath: "includedStructure[].structure.coding[]",
					FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{},
				},
				MissingKeyPolicy:     recipe.ConstructionGroupMissingKeyGroup,
				SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
				CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
			}}, Outputs: columns,
		}}},
	}
}
