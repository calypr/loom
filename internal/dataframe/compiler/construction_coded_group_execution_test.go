package compiler

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCodedGroupPreviewWithSourceIdentityKeepsStageInputScoped(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "coded-group-project", DatasetGeneration: "coded-group-generation",
		IncludeAuthResourcePath: true, IncludeSourceIdentity: true,
	}
	compiled := lowerConstructionOutput(t, bodyStructureCodedGroupOutput(), bindings)
	query, err := CompileRecipeOutputWithPolicy(compiled, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Query, "FOR __loom_construction_input_1 IN __loom_construction_source_projection") {
		t.Fatalf("coded-group input row is not scoped by the source projection:\n%s", query.Query)
	}
	if strings.Contains(query.Query, " IN __loom_construction_input_1") {
		t.Fatalf("CODED_GROUP preview leaked its input row variable outside the row scope:\n%s", query.Query)
	}
	if strings.Contains(query.Query, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("composite CODED_GROUP preview gained a single-source identity projection:\n%s", query.Query)
	}
	if got, want := query.PublicColumns, []string{"code_system", "code_version", "code", "source_records"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CODED_GROUP preview public columns = %#v, want %#v", got, want)
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
