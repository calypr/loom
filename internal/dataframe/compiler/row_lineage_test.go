package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileRowLineageUsesBoundedCanonicalGroupTerminal(t *testing.T) {
	output := lowerConstructionOutput(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("unsupported source operation: %s", capability.Operation)
	}
	compiled, err := CompileRowLineageOutput(output, "opaque-group-row", 25, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"root.payload.id", "root._key", "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH",
		"TO_STRING([[\"construction\"", "SORT __loom_physical_construction_row_lineage_contributor",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit", "hasMore:", "found:",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("row lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "COLLECT ") && strings.Contains(compiled.Query, " INTO ") {
		t.Fatalf("row lineage query materialized unbounded Group contributors:\n%s", compiled.Query)
	}
	if compiled.BindVars[rowLineageRowIDBind] != "opaque-group-row" || compiled.BindVars[rowLineageOffsetBind] != 25 ||
		compiled.BindVars[rowLineageLimitBind] != 10 || compiled.BindVars[rowLineageFetchLimitBind] != 11 {
		t.Fatalf("row lineage page bindings = %#v", compiled.BindVars)
	}
}

func TestCompileKeylessEmptyGroupHasExplicitEmptyContributorPage(t *testing.T) {
	output := lowerConstructionOutput(t, constructionCountRowsOnlyOutput(), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("keyless Group capability = %#v, want available", capability)
	}
	compiled, err := CompileRowLineageOutput(output, "opaque-empty-group-row", 0, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, "COLLECT WITH COUNT INTO") || !strings.Contains(compiled.Query, "contributors: SLICE") || strings.Contains(compiled.Query, "COLLECT "+"__loom_physical_row_lineage_group_key") {
		t.Fatalf("keyless Group lineage terminal does not preserve the empty Group row and bounded empty page:\n%s", compiled.Query)
	}
	if compiled.Limit != DefaultRowLineageLimit || compiled.BindVars[rowLineageFetchLimitBind] != DefaultRowLineageLimit+1 {
		t.Fatalf("default page = limit %d, bindings %#v", compiled.Limit, compiled.BindVars)
	}
}

func TestGroupRowLineageCapabilityKeepsPopulationSourcesEligible(t *testing.T) {
	output := lowerConstructionOutput(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	for index := range output.Plan.Operations {
		if output.Plan.Operations[index].Kind == ir.PhysicalRootScanOp {
			output.Plan.BindVars["population_members"] = "loom_explorer_selection_members"
			output.Plan.Operations[index].RootScan.Population = &ir.PhysicalPopulationRootSource{
				MemberScan: ir.PhysicalCollectionScan{Variable: "population_member", CollectionBindKey: "population_members"},
				ResourceOperations: []ir.PhysicalOperation{
					{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "population_source", CollectionBindKey: "root_collection"}},
					{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
						Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "population_source", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"},
					}}},
					{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
						Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "population_source", Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"},
					}}},
				},
				RootKey:  ir.PhysicalValue{Variable: "population_source", Path: []string{"_key"}},
				MemberID: ir.PhysicalValue{Variable: "population_member", Path: []string{"id"}},
			}
			break
		}
	}
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("legacy Group population-source capability narrowed to %#v", capability)
	}
}

func TestCompileCodedGroupRowLineageResolvesExactTupleAndPagesRootSources(t *testing.T) {
	lineageOutput := codedGroupLineageOutput()
	lineageOutput.RootResourceType = "BodyStructure"
	lineageOutput.Construction.Steps[0].Operation.CodedGroup.Source.ResourceType = "BodyStructure"
	lineageOutput.Construction.Steps[0].Operation.CodedGroup.Source.CodingPath = "includedStructure[].structure.coding[]"
	output := lowerConstructionOutput(t, lineageOutput, recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("CODED_GROUP lineage capability = %#v", capability)
	}
	compiled, err := CompileRowLineageOutput(output, "opaque-coded-group-row", 4, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"CODED_GROUP", "@project", "@dataset_generation", "DOCUMENT(@@root_collection",
		".payload[\"includedStructure\"]",
		`TYPENAME(__loom_physical_construction_row_lineage_coded_group_candidate.code) == "string"`,
		"COLLECT __loom_physical_construction_row_lineage_coded_group_source_id",
		`TO_STRING(["construction", @construction_coded_group_id`,
		"FILTER __loom_physical_construction_row_lineage_coded_group_identity == @row_lineage_row_id",
		"SORT __loom_physical_construction_row_lineage_coded_group_contributor_key ASC",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
		"resourceId:", "occurrenceKey:", "hasMore:",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("CODED_GROUP lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "COLLECT "+"__loom_physical_row_lineage_coded_group_source_id = ") &&
		strings.Contains(compiled.Query, " INTO ") {
		t.Fatalf("CODED_GROUP lineage materialized an unbounded contributor list:\n%s", compiled.Query)
	}
	if compiled.BindVars[rowLineageRowIDBind] != "opaque-coded-group-row" || compiled.BindVars[rowLineageOffsetBind] != 4 ||
		compiled.BindVars[rowLineageLimitBind] != 10 || compiled.BindVars[rowLineageFetchLimitBind] != 11 {
		t.Fatalf("CODED_GROUP lineage page bindings = %#v", compiled.BindVars)
	}
}

func TestCodedGroupRowLineageCapabilityKeepsPopulationSourcesEligible(t *testing.T) {
	output := lowerConstructionOutput(t, codedGroupLineageOutput(), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	for index := range output.Plan.Operations {
		if output.Plan.Operations[index].Kind == ir.PhysicalRootScanOp {
			output.Plan.Operations[index].RootScan.Population = &ir.PhysicalPopulationRootSource{}
			break
		}
	}
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("population-selected CODED_GROUP lineage capability = %#v", capability)
	}
}

func TestRowLineageCapabilityRejectsEarlierConstructionOperations(t *testing.T) {
	output := constructionOracleOutput()
	compiled := lowerConstructionOutput(t, output, recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := RowLineageCapabilityForOutput(compiled)
	if capability.Available || capability.ReasonCode == "" || capability.Operation == "" {
		t.Fatalf("multi-stage construction capability = %#v, want a specific unsupported state", capability)
	}
}

func TestRelatedExpandLineageCapabilityRejectsMultiStageAndMultiHopSources(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	filtered := lowerConstructionOutput(t, relatedExpandOracleOutput(recipe.ExpansionExclude), bindings)
	if capability := RowLineageCapabilityForOutput(filtered); capability.Available || capability.ReasonCode != "ROW_LINEAGE_OPERATION_UNSUPPORTED" {
		t.Fatalf("multi-stage RELATED_EXPAND capability = %#v, want operation unsupported", capability)
	}

	direct := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	direct.Plan.StageSequence.Stages[0].RelatedExpand.Route = append(
		direct.Plan.StageSequence.Stages[0].RelatedExpand.Route,
		direct.Plan.StageSequence.Stages[0].RelatedExpand.Route[0],
	)
	if capability := RowLineageCapabilityForOutput(direct); capability.Available || capability.ReasonCode != "ROW_LINEAGE_ROUTE_UNSUPPORTED" {
		t.Fatalf("multi-hop RELATED_EXPAND capability = %#v, want route unsupported", capability)
	}
}

func TestRelatedExpandLineageCapabilityRejectsIndirectSourceOperations(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	direct := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	direct.Plan.Operations[0].Kind = ir.PhysicalGroupRowsOp
	if capability := RowLineageCapabilityForOutput(direct); capability.Available || capability.ReasonCode != "ROW_LINEAGE_SOURCE_NOT_DIRECT" {
		t.Fatalf("transformed source capability = %#v, want direct-source refusal", capability)
	}
}

func TestRelatedExpandLineageCapabilityKeepsSelectedRootSourcesEligible(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	output := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionPreserveParent), bindings)
	for index := range output.Plan.Operations {
		if output.Plan.Operations[index].Kind == ir.PhysicalRootScanOp {
			output.Plan.Operations[index].RootScan.Population = &ir.PhysicalPopulationRootSource{}
			break
		}
	}
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("selected root source lineage capability = %#v, want available", capability)
	}
}

func TestCompileRelatedExpandRowLineageUsesAnchoredCanonicalAuthorizedRoute(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"Patient/visible"},
	}
	output := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	capability := RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("direct one-hop RELATED_EXPAND capability = %#v", capability)
	}
	stage := output.Plan.StageSequence.Stages[0]
	constructionID := output.Plan.BindVars[stage.RelatedExpand.ConstructionIDBindKey].(string)
	rowID := relatedExpandRowID(t, "patient-storage-key", constructionID, "Observation/observation-storage-key")
	compiled, err := CompileRowLineageOutput(output, rowID, 0, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"root._key == @row_lineage_parent_key",
		"_id == @row_lineage_terminal_id",
		"TO_STRING([[\"input\", ",
		"[\"related\", ",
		"@auth_resource_paths",
		"@dataset_generation",
		"@project",
		"LIMIT 1",
		"contributors: SLICE",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("related row lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if compiled.BindVars[rowLineageParentKeyBind] != "patient-storage-key" || compiled.BindVars[rowLineageTerminalIDBind] != "Observation/observation-storage-key" {
		t.Fatalf("row identity bindings = %#v", compiled.BindVars)
	}
	if !strings.Contains(compiled.Query, "auth_resource_path IN @auth_resource_paths") {
		t.Fatalf("related row lineage omitted the restricted route scope:\n%s", compiled.Query)
	}
}

func TestCompileRelatedExpandPreserveParentLineageReturnsRootOnlyForEmptyIdentity(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	output := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionPreserveParent), bindings)
	stage := output.Plan.StageSequence.Stages[0]
	constructionID := output.Plan.BindVars[stage.RelatedExpand.ConstructionIDBindKey].(string)
	rowID := relatedExpandEmptyRowID(t, "patient-storage-key", constructionID)
	compiled, err := CompileRowLineageOutput(output, rowID, 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if compiled.BindVars[rowLineageParentKeyBind] != "patient-storage-key" {
		t.Fatalf("empty related row identity bindings = %#v", compiled.BindVars)
	}
	for _, want := range []string{"FILTER __loom_physical_construction_row_lineage_related_item", "[\"empty\"]", "rootID:", "rootKey:"} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("PRESERVE_PARENT row lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "relatedID:") || strings.Contains(compiled.Query, "target_resource_type") {
		t.Fatalf("PRESERVE_PARENT empty row lineage projected a terminal contributor:\n%s", compiled.Query)
	}
}

func TestCompileRelatedExpandRowLineageTreatsForgedIdentityAsUnmatchable(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	output := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	stage := output.Plan.StageSequence.Stages[0]
	constructionID := output.Plan.BindVars[stage.RelatedExpand.ConstructionIDBindKey].(string)
	forged := relatedExpandRowID(t, "patient-storage-key", constructionID, "Observation/forged-key")
	compiled, err := CompileRowLineageOutput(output, forged, 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if compiled.BindVars[rowLineageParentKeyBind] != "patient-storage-key" || compiled.BindVars[rowLineageTerminalIDBind] != "Observation/forged-key" {
		t.Fatalf("forged row ID was not bound as an exact candidate: %#v", compiled.BindVars)
	}
	if !strings.Contains(compiled.Query, "_id == @row_lineage_terminal_id") || !strings.Contains(compiled.Query, "FILTER __loom_physical_construction_row_lineage_related_identity == @row_lineage_row_id") {
		t.Fatalf("forged row ID is not checked against the authorized terminal and canonical identity:\n%s", compiled.Query)
	}
}

func TestParseRelatedExpandRowIDRejectsMalformedAndCrossStageIdentity(t *testing.T) {
	for _, rowID := range []string{"not-json", `[["input","p"],["construction","other"],["related","Observation/o"]]`, `[["input","p"],["construction","step"],["empty"]]`} {
		if _, _, _, ok := parseRelatedExpandRowID(rowID, "step", false); ok {
			t.Errorf("row ID %q parsed as a row for a different operation", rowID)
		}
	}
	if _, _, _, ok := parseRelatedExpandRowID(`[["input","p"],["construction","step"],["empty"]]`, "step", true); !ok {
		t.Fatal("PRESERVE_PARENT empty row identity did not parse")
	}
}

const (
	rowLineageParentKeyBind  = "row_lineage_parent_key"
	rowLineageTerminalIDBind = "row_lineage_terminal_id"
)

func directRelatedExpandOutput(emptyPolicy recipe.ExpansionEmptyPolicy) recipe.Output {
	output := relatedExpandOracleOutput(emptyPolicy)
	step := output.Construction.Steps[1]
	step.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}
	output.Construction.Steps = []recipe.ConstructionStep{step}
	return output
}

func codedGroupLineageOutput() recipe.Output {
	return recipe.Output{
		Name: "coded_group_lineage", RootResourceType: "Specimen", RowGrain: "resource",
		Fields: []recipe.Field{{Name: "resource_id", ColumnID: "resource_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "resource_id", Name: "resource_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_codes", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{
					Kind: recipe.ConstructionCodedGroupOp,
					CodedGroup: &recipe.ConstructionCodedGroup{
						ConstructionID: "group_codes",
						Source: recipe.ConstructionCodedGroupSource{
							OccurrenceID: "base", ResourceType: "Specimen", CodingPath: "type.coding[]",
							FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY", Route: []recipe.ConstructionRelatedRouteStep{},
						},
						MissingKeyPolicy:     recipe.ConstructionGroupMissingKeyGroup,
						SystemOutputColumnID: "system_id", VersionOutputColumnID: "version_id",
						CodeOutputColumnID: "code_id", DistinctSourceCountOutputColumnID: "count_id",
					},
				},
				Outputs: []recipe.StageColumn{
					{ID: "system_id", Name: "code_system", Type: "string", Nullable: true},
					{ID: "version_id", Name: "code_version", Type: "string", Nullable: true},
					{ID: "code_id", Name: "code", Type: "string", Nullable: true},
					{ID: "count_id", Name: "source_records", Type: "integer"},
				},
			}},
		},
	}
}

func relatedExpandRowID(t *testing.T, parentKey, constructionID, terminalID string) string {
	t.Helper()
	encoded, err := json.Marshal([][]string{{"input", parentKey}, {"construction", constructionID}, {"related", terminalID}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func relatedExpandEmptyRowID(t *testing.T, parentKey, constructionID string) string {
	t.Helper()
	encoded, err := json.Marshal([][]string{{"input", parentKey}, {"construction", constructionID}, {"empty"}})
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
