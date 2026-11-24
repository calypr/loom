package rowlineage_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestExpandedCodingGroupLineagePagesExactInputOccurrences(t *testing.T) {
	output := expandedCodingRowLineageOutput(t)
	if !rowlineage.RowLineageExpandedCodingGroup(output) {
		t.Fatal("compiler did not recognize the exact expanded Coding Group lineage shape")
	}
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("expanded Coding Group row lineage is unavailable: %#v", capability)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "opaque-expanded-coded-group-row", 4, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile expanded Coding Group lineage: %v", err)
	}
	for _, want := range []string{
		"row_lineage_coding_selected_group",
		"coding_identity",
		"row_lineage_coding_contributor",
		"row_lineage_coding_contributor_input_tuple",
		"FILTER __loom_physical_construction_row_lineage_coding_contributor_input_tuple == __loom_physical_construction_row_lineage_coding_selected_group.codingTuple",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
		"occurrenceKey: __loom_physical_construction_row_lineage_coding_contributor",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("expanded Coding Group lineage query missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Count(compiled.Query, "FOR root IN @@root_collection") != 2 ||
		strings.Count(compiled.Query, "FOR __loom_physical_unnest_index_0") != 2 {
		t.Fatalf("lineage must requery the exact scoped root/UNNEST preimage twice:\n%s", compiled.Query)
	}
	if strings.Contains(compiled.Query, "UNIQUE(") || strings.Contains(compiled.Query, "SORT __loom_physical_construction_row_lineage_coding_contributor[@__loom_physical_construction_row_lineage_coding_occurrence_key_column] ASC") == false {
		t.Fatalf("expanded occurrence lineage must page exact occurrence identities without owner dedup:\n%s", compiled.Query)
	}
	foundExactOccurrence := false
	for _, value := range compiled.BindVars {
		if value == "__loom_group_source_occurrence_key" {
			foundExactOccurrence = true
		}
	}
	if !foundExactOccurrence {
		t.Fatalf("lineage binds did not pin the full expanded occurrence column: %#v", compiled.BindVars)
	}
}

func TestRelatedCodingGroupLineageReplaysExactRouteAndCodingOrdinal(t *testing.T) {
	output := relatedCodingGroupLineageOutput(t)
	if !rowlineage.RowLineageRelatedCodingGroup(output) {
		t.Fatal("compiler did not recognize the terminal related Coding Group lineage shape")
	}
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("related Coding Group row lineage is unavailable: %#v", capability)
	}
	stage := output.Plan.StageSequence.Stages[0]
	if stage.Group == nil || stage.Group.RelatedOccurrence == nil || stage.Group.CodingKey == nil {
		t.Fatalf("related Coding Group lost its shared route adapter: %#v", stage.Group)
	}
	adapter := stage.Group.RelatedOccurrence
	rowID := `[["construction","group_related_id"],["operation","GROUP"],["status_group_id","final"],["coding_group_id","coding_identity",["urn:example:codes","1","same-code"]]]`
	compiled, err := rowlineage.CompileRowLineageOutput(output, rowID, 2, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile related Coding Group row lineage: %v", err)
	}
	terminal := output.Plan.StageSequence.RowLineageReturn
	if terminal != nil {
		t.Fatal("compilation mutated its input output plan")
	}
	if compiled.BindVars[rowlineage.RowLineageRowIDBind] != rowID || compiled.BindVars[rowlineage.RowLineageOffsetBind] != 2 ||
		compiled.BindVars[rowlineage.RowLineageFetchLimitBind] != 6 || compiled.BindVars[rowlineage.RowLineageLimitBind] != 5 {
		t.Fatalf("lineage query has wrong identity/page bindings: %#v", compiled.BindVars)
	}
	if !rowLineageHasBindValue(compiled.BindVars, adapter.OccurrenceKeyColumn) ||
		!rowLineageHasBindValue(compiled.BindVars, rowlineage.RowLineageResourceID) {
		t.Fatalf("lineage output does not expose the exact Group occurrence and root resource ID columns: %#v", compiled.BindVars)
	}
	if _, ok := compiled.BindVars["@related_expand_0_hop_1_edge_collection"]; !ok {
		t.Fatalf("related Group route collection bind is missing: %#v", compiled.BindVars)
	}
	for _, want := range []string{
		"row_lineage_related_selected",
		"coding_identity",
		"codingTuple",
		"TRIM(__loom_physical_construction_row_lineage_related_coding_membership.version)",
		"TYPENAME(__loom_physical_construction_row_lineage_related_coding_membership.code) == \"string\"",
		"COLLECT ",
		"AGGREGATE __loom_physical_construction_row_lineage_related_count = SUM(1)",
		"FILTER __loom_physical_construction_row_lineage_related_identity == @row_lineage_row_id",
		"FILTER __loom_physical_construction_row_lineage_related_contributor_tuple == __loom_physical_construction_row_lineage_related_selected.codingTuple",
		"SORT __loom_physical_construction_row_lineage_related_contributor[@__loom_physical_construction_row_lineage_related_occurrence_key_column] ASC",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
		"occurrenceKey: __loom_physical_construction_row_lineage_related_contributor[@__loom_physical_construction_row_lineage_related_occurrence_key_column]",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("related Coding Group lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	for _, field := range []string{"root_id", "terminal_id", "item_present", "ordinal"} {
		if !rowLineageHasBindValue(compiled.BindVars, field) {
			t.Errorf("lineage occurrence projection does not bind %q", field)
		}
	}
	if !strings.Contains(compiled.Query, " == 0 ? [null] : RANGE(0, LENGTH(") ||
		!strings.Contains(compiled.Query, " AND "+adapter.CodingUnnest.HasItemVariable) {
		t.Fatalf("lineage replay lost the route/Coding empty sentinels or route-gated Coding presence:\n%s", compiled.Query)
	}
	if strings.Count(compiled.Query, "FOR root IN @@root_collection") != 2 ||
		strings.Count(compiled.Query, "FOR __loom_construction_related_expand_0_hop_1_edge_1 IN @@related_expand_0_hop_1_edge_collection") != 2 ||
		strings.Count(compiled.Query, "LET "+adapter.CodingUnnest.OutputVariable+" =") != 2 {
		t.Fatalf("lineage must replay the same scoped root, route, and Coding expansion for lookup and paging:\n%s", compiled.Query)
	}
	if strings.Contains(compiled.Query, " INTO ") {
		t.Fatalf("related Coding Group lineage must preserve Coding ordinals without materializing or deduplicating contributors:\n%s", compiled.Query)
	}

	unsupported := output
	unsupported.Plan = ir.ClonePhysicalPlan(output.Plan)
	unsupported.Plan.StageSequence.Stages[0].Group.RelatedOccurrence.RouteID += "_other"
	if capability := rowlineage.RowLineageCapabilityForOutput(unsupported); capability.Available {
		t.Fatalf("mismatched route unexpectedly enabled related Group lineage: %#v", capability)
	}
}

func TestExpandedCodingGroupLineageReusesRoutedAncestorOccurrence(t *testing.T) {
	output := routedAncestorCodingRowLineageOutput(t)
	stage := output.Plan.StageSequence.Stages[0]
	if stage.Group == nil || stage.Group.CodingKey == nil || len(stage.Group.RowValues) != 1 || stage.Group.RowValues[0].FieldSource == nil {
		t.Fatalf("routed Group lacks its bound ancestor RowValue: %#v", stage.Group)
	}
	rowValueSource := stage.Group.RowValues[0].FieldSource
	if rowValueSource.Role != "ANCESTOR" || rowValueSource.OwnerResourceType != "Observation" ||
		rowValueSource.ResourceType != "ObservationComponent" || rowValueSource.BoundarySelector != "component[]" {
		t.Fatalf("ancestor RowValue lost its repeated boundary binding: %#v", rowValueSource)
	}
	if len(stage.Group.CodingKey.SourceUnnest.Owner.Route) != 1 ||
		stage.Group.CodingKey.SourceUnnest.Owner.Route[0].OccurrenceID != "observation-owner" {
		t.Fatalf("Coding Group did not retain the exact Patient-to-Observation owner route: %#v", stage.Group.CodingKey.SourceUnnest.Owner.Route)
	}
	var operationKinds []string
	for _, operation := range output.Plan.Operations {
		operationKinds = append(operationKinds, string(operation.Kind))
	}
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("routed expanded Coding Group row lineage is unavailable: %#v", capability)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "routed-expanded-row", 0, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile routed expanded Coding Group lineage: %v", err)
	}
	if strings.Count(compiled.Query, "FOR population_member IN @@population_members_collection") != 2 ||
		strings.Count(compiled.Query, "FOR __loom_physical_unnest_index_0") != 2 {
		t.Fatalf("routed lineage must requery the same root/route/UNNEST preimage twice:\n%s", compiled.Query)
	}
	for _, want := range []string{
		"row_lineage_coding_contributor", "occurrenceKey:", "component", "coding", "ordinal",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("routed expanded Coding Group lineage lacks %q:\n%s", want, compiled.Query)
		}
	}
	bindText := fmt.Sprint(compiled.BindVars)
	for _, want := range []string{"subject_Patient", "edge_00_id", "owner_id", "item_present"} {
		if !strings.Contains(bindText, want) {
			t.Errorf("lineage binds omitted routed occurrence fact %q: %#v", want, compiled.BindVars)
		}
	}
}

func TestExpandedCodingGroupLineageCapabilityAcceptsExactPopulationRouteFixture(t *testing.T) {
	output := expandedCodingGroupFixtureLineageOutput(t)
	stage := output.Plan.StageSequence.Stages[0]
	if stage.Group == nil || stage.Group.CodingKey == nil || stage.Group.RootContributorInputColumn != "" {
		t.Fatalf("exact occurrence lineage should replace the terminal Group root contributor array: %#v", stage.Group)
	}
	rootKeyRetained := false
	for _, column := range output.Plan.StageSequence.SourceColumns {
		if column.Name == "_key" && column.Internal && column.RootContributorResourceType == "Patient" {
			rootKeyRetained = true
		}
	}
	if !rootKeyRetained {
		t.Fatal("source preimage lost the typed Patient root key needed for exact occurrence lineage")
	}
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("exact population/route/UNNEST Coding Group lineage capability = %#v", capability)
	}
	groupQuery, err := CompileRecipeOutputWithPolicy(output, recipe.RuntimeBindings{
		Project: "row-lineage-project", SelectionProject: "row-lineage-project",
		SelectionMembersCollection: "loom_explorer_selection_members", DatasetGeneration: "row-lineage-generation",
	}, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile exact fixture Group query: %v", err)
	}
	if !strings.HasPrefix(groupQuery.Query, "LET __loom_construction_stage_1 = (\n  FOR population_member IN @@population_members_collection") ||
		strings.Count(groupQuery.Query, "FOR population_member IN @@population_members_collection") != 1 ||
		strings.Count(groupQuery.Query, "FOR __loom_expansion_edge_1 IN @@expansion_route_1_edge_collection") != 1 ||
		strings.Count(groupQuery.Query, "FOR __loom_physical_unnest_index_0") != 1 ||
		strings.Contains(groupQuery.Query, "LET __loom_construction_source_projection = (") ||
		strings.Contains(groupQuery.Query, " INTO __loom_construction_group_rows") ||
		strings.Contains(groupQuery.Query, "__loom_root_contributor_keys") ||
		strings.Contains(groupQuery.Query, "__loom_construction_root_contributors") {
		t.Fatalf("exact fixture Group did not stream its scoped population/route/UNNEST into COLLECT:\n%s", groupQuery.Query)
	}
	if !strings.Contains(groupQuery.Query, "COLLECT __loom_construction_group_key_1 =") ||
		!strings.Contains(groupQuery.Query, " AGGREGATE __loom_physical_construction_construction_group_count_rows = SUM(") ||
		strings.Contains(groupQuery.Query, " INTO ") {
		t.Fatalf("exact fixture Group did not aggregate rows directly without materializing Group rows:\n%s", groupQuery.Query)
	}
	inputStart := strings.Index(groupQuery.Query, "LET __loom_construction_input_1 = {")
	if inputStart < 0 {
		t.Fatalf("exact fixture Group has no narrow per-row source projection:\n%s", groupQuery.Query)
	}
	inputEnd := strings.Index(groupQuery.Query[inputStart:], "LET __loom_physical_construction_construction_group_coding_membership =")
	if inputEnd < 0 {
		t.Fatalf("exact fixture Group has no narrow per-row source projection:\n%s", groupQuery.Query)
	}
	inputProjection := groupQuery.Query[inputStart : inputStart+inputEnd]
	for _, want := range []string{
		"__loom_expansion_node_1.payload.id", "__loom_expansion_node_1.payload.status",
		"coding_item.code", "coding_item.display", "coding_item.system", "coding_item.version",
		"__loom_expansion_edge_1._id", "__loom_has_expanded_item", "coding_ordinal", "root._id",
	} {
		if !strings.Contains(inputProjection, want) {
			t.Errorf("exact fixture Group projection omitted selected scalar/occurrence fact %q:\n%s", want, inputProjection)
		}
	}
	if strings.Contains(inputProjection, "coding_item,") || !strings.Contains(groupQuery.Query, "construction_group_row_value_arrays_0 = UNIQUE(") {
		t.Fatalf("Group input must project compact Coding fields and only the requested ALL RowValue, not whole Coding payloads:\nprojection=%s\nquery=%s", inputProjection, groupQuery.Query)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "fixture-expanded-coded-group-row", 0, 20, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile exact fixture Coding Group lineage: %v", err)
	}
	if strings.Count(compiled.Query, "FOR population_member IN @@population_members_collection") != 2 ||
		strings.Count(compiled.Query, "FOR __loom_physical_unnest_index_0") != 2 {
		t.Fatalf("fixture lineage must requery the exact population/route/UNNEST preimage twice:\n%s", compiled.Query)
	}
	for _, want := range []string{"population_selection_id", "subject_Patient", "ordinal", "occurrenceKey:"} {
		if !strings.Contains(fmt.Sprint(compiled.BindVars), want) && !strings.Contains(compiled.Query, want) {
			t.Errorf("fixture lineage omitted exact source fact %q from AQL and binds", want)
		}
	}
}

func TestExpandedCodingGroupPopulationNonCountRowsRetainsRootContributors(t *testing.T) {
	output := expandedCodingGroupFixtureLineageOutputWithAggregate(t, recipe.ConstructionGroupAggregate{
		Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "owner_id", OutputColumnID: "occurrences_id",
	})
	group := output.Plan.StageSequence.Stages[0].Group
	if group == nil || group.RootContributorInputColumn != "_key" || group.RootContributorOutputColumn != "__loom_root_contributor_keys" {
		t.Fatalf("non-COUNT_ROWS expanded Group must keep the ordinary population contributor path: %#v", group)
	}
}

func TestRelatedExpandLineageCapabilityAdmitsIdentityPreservingSuffixAndRejectsInvalidRouteMetadata(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	filtered := lowerConstructionOutput(t, relatedExpandOracleOutput(recipe.ExpansionExclude), bindings)
	if capability := rowlineage.RowLineageCapabilityForOutput(filtered); !capability.Available {
		t.Fatalf("RELATED_EXPAND with identity-preserving suffix capability = %#v, want available", capability)
	}

	direct := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	direct.Plan.StageSequence.Stages[0].RelatedExpand.Route = append(
		direct.Plan.StageSequence.Stages[0].RelatedExpand.Route,
		direct.Plan.StageSequence.Stages[0].RelatedExpand.Route[0],
	)
	if capability := rowlineage.RowLineageCapabilityForOutput(direct); capability.Available || capability.ReasonCode != "ROW_LINEAGE_ROUTE_UNSUPPORTED" {
		t.Fatalf("route metadata that disagrees with its physical traversal = %#v, want route unsupported", capability)
	}
}

func TestRelatedExpandLineageCapabilityRejectsIndirectSourceOperations(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	direct := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	direct.Plan.Operations[0].Kind = ir.PhysicalGroupRowsOp
	if capability := rowlineage.RowLineageCapabilityForOutput(direct); capability.Available || capability.ReasonCode != "ROW_LINEAGE_SOURCE_NOT_DIRECT" {
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
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("selected root source lineage capability = %#v, want available", capability)
	}
}

func TestCompileRelatedExpandRowLineageUsesAnchoredCanonicalAuthorizedRoute(t *testing.T) {
	bindings := recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"Patient/visible"},
	}
	output := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	capability := rowlineage.RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("direct one-hop RELATED_EXPAND capability = %#v", capability)
	}
	stage := output.Plan.StageSequence.Stages[0]
	constructionID := output.Plan.BindVars[stage.RelatedExpand.ConstructionIDBindKey].(string)
	rowID := relatedExpandRowID(t, "patient-storage-key", constructionID, "Observation/observation-storage-key")
	compiled, err := rowlineage.CompileRowLineageOutput(output, rowID, 0, 1, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"root._key == @row_lineage_root_key",
		"_id == @row_lineage_stage_0_terminal_id",
		"== @row_lineage_stage_0_row_id",
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
	if compiled.BindVars[rowLineageRootKeyBind] != "patient-storage-key" ||
		compiled.BindVars[rowLineageStage0TerminalIDBind] != "Observation/observation-storage-key" ||
		compiled.BindVars[rowLineageStage0RowIDBind] != rowID || compiled.BindVars[rowlineage.RowLineageRowIDBind] != rowID {
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
	compiled, err := rowlineage.CompileRowLineageOutput(output, rowID, 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if compiled.BindVars[rowLineageRootKeyBind] != "patient-storage-key" ||
		compiled.BindVars[rowLineageStage0RowIDBind] != rowID {
		t.Fatalf("empty related row identity bindings = %#v", compiled.BindVars)
	}
	if _, exists := compiled.BindVars[rowLineageStage0TerminalIDBind]; exists {
		t.Fatalf("empty row retained an unused terminal bind: %#v", compiled.BindVars)
	}
	for _, want := range []string{"FILTER __loom_physical_construction_row_lineage", "[\"empty\"]", "contributors: SLICE", "root:"} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("PRESERVE_PARENT row lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	for key := range compiled.BindVars {
		if strings.Contains(key, "row_lineage_related_resource_type") {
			t.Fatalf("PRESERVE_PARENT empty row retained a related contributor bind %q", key)
		}
	}
}

func TestCompileRelatedExpandRowLineageTreatsForgedIdentityAsUnmatchable(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	output := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	stage := output.Plan.StageSequence.Stages[0]
	constructionID := output.Plan.BindVars[stage.RelatedExpand.ConstructionIDBindKey].(string)
	forged := relatedExpandRowID(t, "patient-storage-key", constructionID, "Observation/forged-key")
	compiled, err := rowlineage.CompileRowLineageOutput(output, forged, 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if compiled.BindVars[rowLineageRootKeyBind] != "patient-storage-key" ||
		compiled.BindVars[rowLineageStage0TerminalIDBind] != "Observation/forged-key" ||
		compiled.BindVars[rowLineageStage0RowIDBind] != forged {
		t.Fatalf("forged row ID was not bound as an exact candidate: %#v", compiled.BindVars)
	}
	if !strings.Contains(compiled.Query, "_id == @row_lineage_stage_0_terminal_id") || !strings.Contains(compiled.Query, "== @row_lineage_stage_0_row_id") {
		t.Fatalf("forged row ID is not checked against the authorized terminal and canonical identity:\n%s", compiled.Query)
	}
}

func TestCompileRelatedExpandRowLineageBindsMalformedIdentityToNullRoot(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	output := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	compiled, err := rowlineage.CompileRowLineageOutput(output, "not-json", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if compiled.BindVars[rowLineageRootKeyBind] != nil || compiled.BindVars[rowLineageStage0TerminalIDBind] != "" ||
		compiled.BindVars[rowLineageStage0RowIDBind] != "not-json" {
		t.Fatalf("malformed identity was not made unmatchable at the root: %#v", compiled.BindVars)
	}
}

func TestParseRelatedExpandRowIDRejectsMalformedAndCrossStageIdentity(t *testing.T) {
	for _, rowID := range []string{"not-json", `[["input","p"],["construction","other"],["related","Observation/o"]]`, `[["input","p"],["construction","step"],["empty"]]`} {
		if _, _, _, ok := rowlineage.ParseRelatedExpandRowID(rowID, "step", false); ok {
			t.Errorf("row ID %q parsed as a row for a different operation", rowID)
		}
	}
	if _, _, _, ok := rowlineage.ParseRelatedExpandRowID(`[["input","p"],["construction","step"],["empty"]]`, "step", true); !ok {
		t.Fatal("PRESERVE_PARENT empty row identity did not parse")
	}
}

func TestDecodeRelatedRowLineageIdentityWalksEveryAuthoredOwner(t *testing.T) {
	const owners = 4
	stages := make([]ir.PhysicalConstructionStage, owners)
	bindVars := make(map[string]any, owners)
	rowID := "root-storage-key"
	wantRows := make([]string, owners)
	wantTerminals := make([]string, owners)
	for index := 0; index < owners; index++ {
		constructionBind := fmt.Sprintf("construction_%d", index)
		constructionID := fmt.Sprintf("step_%d", index)
		bindVars[constructionBind] = constructionID
		terminalID := fmt.Sprintf("Resource%d/document-%d", index, index)
		rowID = relatedExpandRowID(t, rowID, constructionID, terminalID)
		wantRows[index], wantTerminals[index] = rowID, terminalID
		stages[index] = ir.PhysicalConstructionStage{
			ID: fmt.Sprintf("stage_%d", index), Kind: ir.PhysicalStageRelatedExpandOp,
			RelatedExpand: &ir.PhysicalStageRelatedExpand{
				ConstructionIDBindKey: constructionBind,
				EmptyPolicy:           ir.PhysicalUnnestExclude,
			},
		}
	}

	rootKey, matches, ok := rowlineage.DecodeRelatedRowLineageIdentity(rowID, stages, bindVars)
	if !ok || rootKey != "root-storage-key" || len(matches) != owners {
		t.Fatalf("decoded root=%q matches=%#v valid=%v", rootKey, matches, ok)
	}
	for index, match := range matches {
		if match.StageIndex != index || match.StageID != stages[index].ID || match.RowID != wantRows[index] || match.TerminalID != wantTerminals[index] || match.RowKind != "RELATED" {
			t.Errorf("owner %d decoded as %#v", index, match)
		}
	}

	bindVars["construction_2"] = "wrong-stage"
	if _, _, ok := rowlineage.DecodeRelatedRowLineageIdentity(rowID, stages, bindVars); ok {
		t.Fatal("wrong construction owner was accepted in a nested identity")
	}
}

func TestCompileComposedRelatedRowLineageBindsEachOwnerAndPagesWitnesses(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	output := lowerConstructionOutput(t, composedRelatedExpandOutput(), bindings)
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("multi-stage and multi-hop RELATED_EXPAND capability = %#v", capability)
	}
	stages := output.Plan.StageSequence.Stages
	if len(stages) != 2 || stages[0].RelatedExpand == nil || stages[1].RelatedExpand == nil || len(stages[1].RelatedExpand.Route) != 2 {
		t.Fatalf("lowered related chain = %#v", stages)
	}
	firstConstructionID := output.Plan.BindVars[stages[0].RelatedExpand.ConstructionIDBindKey]
	secondConstructionID := output.Plan.BindVars[stages[1].RelatedExpand.ConstructionIDBindKey]
	firstRowID := relatedExpandRowID(t, "patient-storage-key", firstConstructionID.(string), "Observation/observation-storage-key")
	finalRowID := relatedExpandRowID(t, firstRowID, secondConstructionID.(string), "Patient/related-patient-storage-key")
	compiled, err := rowlineage.CompileRowLineageOutput(output, finalRowID, 2, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if compiled.BindVars[rowLineageRootKeyBind] != "patient-storage-key" ||
		compiled.BindVars[rowLineageStage0RowIDBind] != firstRowID ||
		compiled.BindVars[rowLineageStage0TerminalIDBind] != "Observation/observation-storage-key" ||
		compiled.BindVars["row_lineage_stage_1_row_id"] != finalRowID ||
		compiled.BindVars["row_lineage_stage_1_terminal_id"] != "Patient/related-patient-storage-key" ||
		compiled.BindVars[rowlineage.RowLineageRowIDBind] != finalRowID {
		t.Fatalf("composed owner bindings do not retain exact stage identities: %#v", compiled.BindVars)
	}
	for _, want := range []string{
		"root._key == @row_lineage_root_key",
		"@row_lineage_stage_0_row_id", "@row_lineage_stage_1_row_id",
		"@row_lineage_stage_0_terminal_id", "@row_lineage_stage_1_terminal_id",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
		"contributors: SLICE", "hasMore:",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("composed row lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
}
