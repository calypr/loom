package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
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

func TestCompileStandaloneExplicitGroupRowLineageUsesPinnedBoundedMembers(t *testing.T) {
	output := lowerConstructionOutput(t, recipe.Output{
		Name: "NamedCohort", RootResourceType: "Observation", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_row_lineage", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED"},
	}, recipe.RuntimeBindings{Project: "row-lineage-project", SelectionProject: "row-lineage-project", DatasetGeneration: "row-lineage-generation"})
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("standalone explicit GROUP_ROWS lineage capability = %#v", capability)
	}
	rowID, err := json.Marshal(map[string]string{"group_revision_id": "grouprev_row_lineage", "group_id": "cohort-a"})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileRowLineageOutput(output, string(rowID), 3, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"revision.sourceMembershipDigest == selection.membershipDigest",
		"revision.scopeDigest == selection.scopeDigest",
		"member.ref.project == member.project AND member.ref.generation == member.generation",
		"member.groupId == definition.groupId",
		"SORT member.project ASC, member.generation ASC, member.resourceType ASC, member.id ASC",
		"source == null OR @auth_resource_paths_unrestricted == true OR source.auth_resource_path IN @auth_resource_paths",
		"COLLECT WITH COUNT INTO count",
		"LIMIT @__loom_physical_row_lineage_offset, @__loom_physical_row_lineage_fetch_limit",
		"contributors: SLICE(page, 0, @__loom_physical_row_lineage_limit)",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("standalone explicit group row lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "COLLECT member") || strings.Contains(compiled.Query, "COLLECT selected") || strings.Contains(compiled.Query, "COLLECT resource") {
		t.Fatalf("explicit group lineage materialized an unbounded contributor collection:\n%s", compiled.Query)
	}
	if compiled.BindVars["__loom_physical_row_lineage_group_revision_id"] != "grouprev_row_lineage" ||
		compiled.BindVars["__loom_physical_row_lineage_group_id"] != "cohort-a" ||
		compiled.BindVars["__loom_physical_row_lineage_offset"] != 3 ||
		compiled.BindVars["__loom_physical_row_lineage_limit"] != 7 ||
		compiled.BindVars["__loom_physical_row_lineage_fetch_limit"] != 8 {
		t.Fatalf("explicit group row lineage bindings = %#v", compiled.BindVars)
	}

	for _, invalidID := range []string{
		`{"group_revision_id":"grouprev_other","group_id":"cohort-a"}`,
		`{"group_id":"cohort-a","group_revision_id":"grouprev_row_lineage","extra":"forged"}`,
		`{"group_revision_id":"grouprev_row_lineage", "group_id":"cohort-a"}`,
	} {
		invalid, compileErr := CompileRowLineageOutput(output, invalidID, 0, 7, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile malformed/cross-revision identity %q: %v", invalidID, compileErr)
		}
		if invalid.BindVars["__loom_physical_row_lineage_group_revision_id"] != "" || invalid.BindVars["__loom_physical_row_lineage_group_id"] != "" {
			t.Errorf("invalid identity %q retained requested group binds %#v", invalidID, invalid.BindVars)
		}
	}
}

func TestCompileCohortGroupLineageAppliesFilterBeforePaging(t *testing.T) {
	filterValue := "Cohort"
	output := recipe.Output{
		Name: "NamedCohort", RootResourceType: "Observation", RowGrain: "groups",
		Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "filter_group_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: recipe.ConstructionCohortGroupStageID}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "group_label", Operator: recipe.FilterEquals,
					Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterValue}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group_id", Type: "string"},
					{ID: "group_label", Name: "group_label", Type: "string"},
					{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
					{ID: "members", Name: "members", Type: "array"},
					{ID: "specimen_id", Name: "specimen_id", Type: "array"},
				},
			}},
		},
		GroupRows: &recipe.GroupRows{
			RevisionID: "grouprev_cohort_row_lineage", UnassignedMemberPolicy: "EXCLUDE",
			RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "specimen_id", Policy: recipe.ConstructionRowValueAll}},
		},
	}
	bindings := recipe.RuntimeBindings{
		Project: "row-lineage-project", SelectionProject: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/p1"},
	}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
		t.Fatalf("cohort Group with trailing filter has no lineage capability: %#v", capability)
	}
	rowID, err := json.Marshal(map[string]string{"group_revision_id": "grouprev_cohort_row_lineage", "group_id": "cohort-a"})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileRowLineageOutput(compiledOutput, string(rowID), 2, 4, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"revision.sourceMembershipDigest == selection.membershipDigest",
		"revision.scopeDigest == selection.scopeDigest",
		"candidate.groupId == @__loom_physical_row_lineage_group_id",
		"group_label == @",
		"found:",
		"FILTER found",
		"occurrenceKey: member._key",
		"source.auth_resource_path IN @",
		"LIMIT @__loom_physical_row_lineage_offset, @__loom_physical_row_lineage_fetch_limit",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("cohort Group lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if compiled.BindVars["__loom_physical_row_lineage_group_revision_id"] != "grouprev_cohort_row_lineage" ||
		compiled.BindVars["__loom_physical_row_lineage_group_id"] != "cohort-a" ||
		compiled.BindVars["__loom_physical_row_lineage_offset"] != 2 ||
		compiled.BindVars["__loom_physical_row_lineage_fetch_limit"] != 5 {
		t.Fatalf("cohort Group lineage bindings = %#v", compiled.BindVars)
	}
	for _, invalidID := range []string{
		`{"group_revision_id":"grouprev_other","group_id":"cohort-a"}`,
		`{"group_revision_id":"grouprev_cohort_row_lineage","group_id":"forged"}`,
	} {
		invalid, compileErr := CompileRowLineageOutput(compiledOutput, invalidID, 0, 4, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile invalid cohort row ID %q: %v", invalidID, compileErr)
		}
		if invalid.BindVars["__loom_physical_row_lineage_group_revision_id"] != "" ||
			invalid.BindVars["__loom_physical_row_lineage_group_id"] != "" {
			t.Errorf("invalid cohort row ID %q retained binds %#v", invalidID, invalid.BindVars)
		}
	}
	forged, err := CompileRowLineageOutput(compiledOutput,
		`{"group_id":"forged","group_revision_id":"grouprev_cohort_row_lineage"}`, 0, 4, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile canonical but nonexistent cohort row ID: %v", err)
	}
	if forged.BindVars["__loom_physical_row_lineage_group_id"] != "forged" ||
		!strings.Contains(forged.Query, "FILTER found AND") || !strings.Contains(forged.Query, "LET found =") {
		t.Fatalf("nonexistent cohort row is not gated by final row membership: binds=%#v query=%s", forged.BindVars, forged.Query)
	}
	memberFilterOutput := compiledOutput
	memberFilterOutput.Plan = ir.ClonePhysicalPlan(compiledOutput.Plan)
	memberFilterOutput.Plan.StageSequence.Stages[1].Filter.Predicate.Left.Path = []string{"specimen_id"}
	if capability := RowLineageCapabilityForOutput(memberFilterOutput); capability.Available || capability.Operation != string(ir.PhysicalStageFilterOp) {
		t.Fatalf("array-valued member filter unexpectedly claimed scalar cohort lineage support: %#v", capability)
	}
}

func TestCompileGroupRowLineageAppliesEveryTrailingFilter(t *testing.T) {
	output := lowerConstructionOutput(t, constructionGroupFilterLineageOutput(104, 103), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("Group with trailing filters has no lineage capability: %#v", capability)
	}
	compiled, err := CompileRowLineageOutput(output, "opaque-filtered-group-row", 25, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(compiled.Query, ".rows > @") != 2 {
		t.Fatalf("lineage query did not evaluate both typed filters against the candidate Group row:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, "WITH COUNT INTO") || strings.Contains(compiled.Query, "row_lineage_filter_group_inputs") || strings.Contains(compiled.Query, "__loom_construction_group_rows") {
		t.Fatalf("COUNT_ROWS lineage buffered Group contributors instead of streaming its candidate count:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, "found: ") || !strings.Contains(compiled.Query, " AND ") || !strings.Contains(compiled.Query, "contributors: SLICE(") {
		t.Fatalf("lineage result does not bind contributor lookup to final filter membership:\n%s", compiled.Query)
	}
	thresholds := 0
	for _, value := range compiled.BindVars {
		if value == int64(104) || value == int64(103) {
			thresholds++
		}
	}
	if thresholds != 2 {
		t.Fatalf("filter chain lost typed thresholds; bind vars = %#v", compiled.BindVars)
	}

	changedSchema := output
	changedSchema.Plan = ir.ClonePhysicalPlan(output.Plan)
	changedSchema.Plan.StageSequence.Stages[1].OutputColumns[0].Name = "renamed"
	if got := RowLineageCapabilityForOutput(changedSchema); got.Available || got.ReasonCode != "ROW_LINEAGE_OPERATION_UNSUPPORTED" {
		t.Fatalf("filter changing its schema retained lineage capability: %#v", got)
	}
}

func TestCompileNonCountGroupFilterLineageProjectsSourceRowsInScope(t *testing.T) {
	output := lowerConstructionOutput(t, constructionGroupFilteredSummaryOutput(), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("non-count Group with a trailing filter has no lineage capability: %#v", capability)
	}
	compiled, err := CompileRowLineageOutput(output, "opaque-filtered-summary-row", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, "construction_group_projected_input") {
		t.Fatalf("non-count candidate did not project source rows in scope:\n%s", compiled.Query)
	}
	if strings.Contains(compiled.Query, "row_lineage_filter_group_inputs") {
		t.Fatalf("non-count candidate materialized an extra selected-group input array:\n%s", compiled.Query)
	}
	if !strings.Contains(compiled.Query, ".amount_sum > @") {
		t.Fatalf("typed filter was not evaluated against the candidate aggregate:\n%s", compiled.Query)
	}
}

func TestGroupRowLineageCapabilityRequiresPhysicalFinalIdentityBinding(t *testing.T) {
	output := lowerConstructionOutput(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	stage := output.Plan.StageSequence.Stages[0]
	if stage.RowIdentityColumn == "" || stage.RowIdentityColumn != output.Plan.StageSequence.FinalRowIdentity {
		t.Fatalf("compiled Group physical identity binding = %q / %q", stage.RowIdentityColumn, output.Plan.StageSequence.FinalRowIdentity)
	}
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("valid physical Group identity rejected: %#v", capability)
	}

	withoutSemanticIdentity := output
	withoutSemanticIdentity.RowIdentity = nil
	if capability := RowLineageCapabilityForOutput(withoutSemanticIdentity); capability.Available || capability.ReasonCode != "ROW_LINEAGE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("Group without declared publication identity capability = %#v, want identity unavailable", capability)
	}

	output.Plan.StageSequence.FinalRowIdentity = "forged_identity"
	capability := RowLineageCapabilityForOutput(output)
	if capability.Available || capability.ReasonCode != "ROW_LINEAGE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("mismatched physical Group identity binding capability = %#v, want identity unavailable", capability)
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
		"contributors: SLICE(__loom_physical_construction_row_lineage_coded_group_page, 0, @row_lineage_limit)",
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

func TestCompileConstructionPivotRowLineageUsesTypedScopedPreimageAndPagination(t *testing.T) {
	output := constructionPivotLineageOutput()
	output.Population = &recipe.PopulationConstraint{
		SelectionRevisionID: "selection-lineage-revision", MembershipDigest: "sha256:lineage-members",
		MemberCount: 2, ResourceType: "Observation",
	}
	bindings := recipe.RuntimeBindings{
		Project: "lineage-project", SelectionProject: "lineage-selection-project",
		SelectionMembersCollection: "loom_explorer_selection_members", DatasetGeneration: "lineage-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/lineage"},
	}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
		t.Fatalf("direct construction Pivot lineage capability = %#v", capability)
	}
	rowID, err := json.Marshal([]any{"GROUPED_PIVOT", "row_lineage_pivot", []any{"STRING", nil}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileRowLineageOutput(compiledOutput, string(rowID), 4, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"population_member.resourceType == @population_resource_type",
		"auth_resource_path IN @auth_resource_paths",
		"FILTER __loom_physical_construction_row_lineage_pivot_identity == @row_lineage_row_id",
		"GROUPED_PIVOT", "FILTER ASSERT(", "TABLE_PIVOT_UNLISTED_CATEGORY",
		"COLLECT AGGREGATE", "TABLE_PIVOT_CELL_CARDINALITY",
		"SORT __loom_physical_construction_row_lineage_pivot_source",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit", "contributors: SLICE(", "hasMore:",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("construction Pivot lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	pageAt := strings.Index(compiled.Query, "LET __loom_physical_construction_row_lineage_pivot_page = (")
	if pageAt < 0 {
		t.Fatalf("Pivot contributor page subquery is missing:\n%s", compiled.Query)
	}
	validationQuery := compiled.Query[:pageAt]
	if !strings.Contains(validationQuery, "TABLE_PIVOT_UNLISTED_CATEGORY") ||
		!strings.Contains(validationQuery, "TABLE_PIVOT_CELL_CARDINALITY") ||
		!strings.Contains(validationQuery, "== @row_lineage_row_id ? 1 : 0") {
		t.Fatalf("Pivot found check does not globally validate policies and aggregate the selected identity:\n%s", validationQuery)
	}
	if strings.Contains(validationQuery, "== @row_lineage_pivot_group_key_0") ||
		strings.Contains(validationQuery, "FILTER __loom_physical_construction_row_lineage_pivot_identity == @row_lineage_row_id") {
		t.Fatalf("Pivot policy validation was narrowed to the requested key before global checks:\n%s", validationQuery)
	}
	if strings.Contains(compiled.Query, "COLLECT INTO") || strings.Contains(compiled.Query, "sourceRows") || strings.Contains(compiled.Query, "fullsourceRows") {
		t.Fatalf("construction Pivot lineage retained an unbounded source/member array:\n%s", compiled.Query)
	}
	if strings.Contains(compiled.Query, "TABLE_PIVOT_VALUE_TYPE_MISMATCH") {
		t.Fatalf("ERROR duplicate policy gained a value type check that ordinary Pivot does not apply:\n%s", compiled.Query)
	}
	keyValue, keyFound := compiled.BindVars["row_lineage_pivot_group_key_0"]
	if !keyFound || keyValue != nil {
		t.Fatalf("decoded null Pivot group key = (%#v, %t), want a present null binding; binds=%#v", keyValue, keyFound, compiled.BindVars)
	}
	if compiled.BindVars[rowLineageRowIDBind] != string(rowID) || compiled.BindVars[rowLineageOffsetBind] != 4 ||
		compiled.BindVars[rowLineageLimitBind] != 7 || compiled.BindVars[rowLineageFetchLimitBind] != 8 {
		t.Fatalf("construction Pivot row lineage identity/page bindings = %#v", compiled.BindVars)
	}
	if compiled.BindVars["population_selection_id"] != "selection-lineage-revision" ||
		compiled.BindVars["population_project"] != "lineage-selection-project" ||
		compiled.BindVars["project"] != "lineage-project" ||
		compiled.BindVars["dataset_generation"] != "lineage-generation" ||
		!reflect.DeepEqual(compiled.BindVars["auth_resource_paths"], []string{"/programs/lineage"}) {
		t.Fatalf("Pivot lineage lost population or read-scope bindings: %#v", compiled.BindVars)
	}

	forgedIDs := []string{
		`["GROUPED_PIVOT","different_construction",["STRING",null]]`,
		`["GROUPED_PIVOT","row_lineage_pivot",["INTEGER",1]]`,
		`not-a-pivot-row-id`,
	}
	for _, forgedID := range forgedIDs {
		forged, compileErr := CompileRowLineageOutput(compiledOutput, forgedID, 0, 7, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile forged Pivot row ID %q: %v", forgedID, compileErr)
		}
		if !strings.Contains(forged.Query, "FILTER __loom_physical_construction_row_lineage_pivot_identity == @row_lineage_row_id") ||
			forged.BindVars[rowLineageRowIDBind] != forgedID {
			t.Errorf("forged Pivot row ID %q did not remain an exact owner-identity comparison: %#v", forgedID, forged.BindVars)
		}
		if key, ok := forged.BindVars["row_lineage_pivot_group_key_0"]; !ok || key != nil {
			t.Errorf("forged Pivot row ID %q did not fail closed with a null key bind: %#v", forgedID, forged.BindVars)
		}
	}

	// Reducers consume only non-null values of the declared type, and an
	// ERROR-on-missing Pivot validates that each requested cell has one.
	compiledOutput.Plan.StageSequence.Stages[0].GroupedPivot.DuplicatePolicy = "SUM"
	compiledOutput.Plan.StageSequence.Stages[0].GroupedPivot.MissingCellPolicy = "ERROR"
	summed, err := CompileRowLineageOutput(compiledOutput, string(rowID), 0, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile SUM Pivot lineage: %v", err)
	}
	for _, want := range []string{"TABLE_PIVOT_VALUE_TYPE_MISMATCH", "TABLE_PIVOT_CELL_MISSING"} {
		if !strings.Contains(summed.Query, want) {
			t.Errorf("SUM Pivot lineage query is missing %q:\n%s", want, summed.Query)
		}
	}
}

func TestCompileConstructionCountRowsGroupPivotLineageStreamsScopedRootPreimage(t *testing.T) {
	output := constructionCountRowsGroupPivotLineageOutput()
	output.Population = &recipe.PopulationConstraint{
		SelectionRevisionID: "selection-group-pivot-revision", MembershipDigest: "sha256:group-pivot-members",
		MemberCount: 2, ResourceType: "Observation",
	}
	bindings := recipe.RuntimeBindings{
		Project: "group-pivot-project", SelectionProject: "group-pivot-selection-project",
		SelectionMembersCollection: "loom_explorer_selection_members", DatasetGeneration: "group-pivot-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/group-pivot"},
	}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
		t.Fatalf("COUNT_ROWS Group followed by Pivot lineage capability = %#v", capability)
	}
	sequence := compiledOutput.Plan.StageSequence
	if sequence == nil || len(sequence.Stages) != 2 || sequence.Stages[0].Kind != ir.PhysicalStageGroupOp || sequence.Stages[1].Kind != ir.PhysicalStagePivotOp {
		t.Fatalf("compiled Group/Pivot stages = %#v", sequence)
	}
	rowID, err := json.Marshal([]any{"GROUPED_PIVOT", "row_lineage_group_pivot", []any{"STRING", "patient-7"}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileRowLineageOutput(compiledOutput, string(rowID), 6, 9, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"auth_resource_path IN @auth_resource_paths",
		"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH",
		"TABLE_PIVOT_UNLISTED_CATEGORY", "TABLE_PIVOT_CELL_CARDINALITY",
		"GROUPED_PIVOT", "row_lineage_group_pivot_group_key_type",
		"row_lineage_group_pivot_source", "root.payload.id", "root._key",
		"row_lineage_group_pivot_category_projection[@__loom_physical_construction_row_lineage_group_pivot_category_column] == null",
		"SORT __loom_physical_construction_row_lineage_group_pivot_source",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit", "contributors: SLICE(", "hasMore:",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("Group/Pivot lineage query is missing %q:\n%s", want, compiled.Query)
		}
	}
	if strings.Contains(compiled.Query, "COLLECT INTO") || strings.Contains(compiled.Query, "SORTED_UNIQUE") || strings.Contains(compiled.Query, "__loom_root_contributor_keys") {
		t.Fatalf("Group/Pivot row lineage used a row array or RootContributor sidecar:\n%s", compiled.Query)
	}
	pageAt := strings.Index(compiled.Query, "LET __loom_physical_construction_row_lineage_group_pivot_page = (")
	if pageAt < 0 {
		t.Fatalf("Group/Pivot source preimage page is missing:\n%s", compiled.Query)
	}
	validation := compiled.Query[:pageAt]
	if !strings.Contains(validation, "TABLE_PIVOT_UNLISTED_CATEGORY") ||
		!strings.Contains(validation, "TABLE_PIVOT_CELL_CARDINALITY") ||
		!strings.Contains(validation, "== @row_lineage_row_id ? 1 : 0") {
		t.Fatalf("Group/Pivot policies were not checked across the full scoped grouped input before paging:\n%s", validation)
	}
	for _, key := range []string{"row_lineage_pivot_group_key_0", "row_lineage_row_id", "row_lineage_offset", "row_lineage_limit", "row_lineage_fetch_limit"} {
		if _, ok := compiled.BindVars[key]; !ok {
			t.Errorf("Group/Pivot lineage omitted typed/page bind %q: %#v", key, compiled.BindVars)
		}
	}
	if compiled.BindVars["row_lineage_pivot_group_key_0"] != "patient-7" || compiled.BindVars[rowLineageRowIDBind] != string(rowID) ||
		compiled.BindVars[rowLineageOffsetBind] != 6 || compiled.BindVars[rowLineageLimitBind] != 9 || compiled.BindVars[rowLineageFetchLimitBind] != 10 {
		t.Errorf("Group/Pivot typed identity/page bindings = %#v", compiled.BindVars)
	}
	if compiled.BindVars["population_selection_id"] != "selection-group-pivot-revision" ||
		compiled.BindVars["population_project"] != "group-pivot-selection-project" ||
		compiled.BindVars["project"] != "group-pivot-project" ||
		compiled.BindVars["dataset_generation"] != "group-pivot-generation" ||
		!reflect.DeepEqual(compiled.BindVars["auth_resource_paths"], []string{"/programs/group-pivot"}) {
		t.Fatalf("Group/Pivot lineage lost source scope bindings: %#v", compiled.BindVars)
	}
	for policy, want := range map[ir.PhysicalStageGroupMissingKeyPolicy]string{
		ir.PhysicalStageGroupMissingKeyExclude: "FILTER __loom_physical_construction_row_lineage_group_pivot_group_value_0 != null",
		ir.PhysicalStageGroupMissingKeyError:   "CONSTRUCTION_GROUP_MISSING_KEY",
	} {
		policyOutput := compiledOutput
		policyOutput.Plan = ir.ClonePhysicalPlan(compiledOutput.Plan)
		policyOutput.Plan.StageSequence.Stages[0].Group.MissingKeyPolicy = policy
		policyCompiled, policyErr := CompileRowLineageOutput(policyOutput, string(rowID), 0, 9, ir.DefaultPhysicalOptimizationPolicy())
		if policyErr != nil {
			t.Fatalf("compile Group/Pivot lineage with Group missing policy %q: %v", policy, policyErr)
		}
		if !strings.Contains(policyCompiled.Query, want) {
			t.Errorf("Group/Pivot lineage did not preserve Group missing policy %q (%q):\n%s", policy, want, policyCompiled.Query)
		}
	}

	for name, mutate := range map[string]func(*ir.PhysicalStageSequence){
		"row values": func(sequence *ir.PhysicalStageSequence) {
			sequence.Stages[0].Group.RowValues = []ir.PhysicalStageRowValue{{InputColumn: "status", InputKind: "STRING", Output: "statuses", Policy: "ALL"}}
		},
		"unmapped group key": func(sequence *ir.PhysicalStageSequence) {
			sequence.Stages[0].Group.Keys = append(sequence.Stages[0].Group.Keys, ir.PhysicalStageGroupKey{InputColumn: "other", OutputColumn: "other", Variable: "other_key", Kind: "STRING"})
		},
		"nonterminal pivot policy": func(sequence *ir.PhysicalStageSequence) {
			sequence.Stages[1].GroupedPivot.UnlistedCategoryPolicy = "EXCLUDE_WITH_EVIDENCE"
		},
	} {
		mutated := compiledOutput
		mutated.Plan = ir.ClonePhysicalPlan(compiledOutput.Plan)
		mutate(mutated.Plan.StageSequence)
		capability := RowLineageCapabilityForOutput(mutated)
		if capability.Available || capability.ReasonCode == "" {
			t.Errorf("unsupported composed Group/Pivot %s capability = %#v", name, capability)
		}
	}
}

func constructionCountRowsGroupPivotLineageOutput() recipe.Output {
	active := "active"
	return recipe.Output{
		Name: "construction_count_rows_group_pivot_lineage", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "patient_ref", ColumnID: "patient_ref_id", Expr: recipe.Expression{Select: "root.subject.reference"}},
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "patient_ref_id", Name: "patient_ref", Type: "string"},
				{ID: "status_id", Name: "status", Type: "string"},
			},
			Steps: []recipe.ConstructionStep{
				{
					ID: "group", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
						ConstructionID: "group", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
						Keys: []recipe.ConstructionGroupKey{
							{InputColumnID: "patient_ref_id", OutputColumnID: "patient_ref_group_id"},
							{InputColumnID: "status_id", OutputColumnID: "status_group_id"},
						},
						Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_ref_group_id", Name: "patient_ref", Type: "string"},
						{ID: "status_group_id", Name: "status", Type: "string"},
						{ID: "rows_id", Name: "rows", Type: "integer"},
					},
				},
				{
					ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group"}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
						ConstructionID: "row_lineage_group_pivot", GroupKeyIDs: []string{"patient_ref_group_id"},
						CategoryColumnID: "status_group_id", ValueColumnID: "rows_id",
						Categories: []recipe.ConstructionPivotCategory{
							{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &active}, OutputColumnID: "active_rows_id"},
							{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_status_rows_id"},
						},
						DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
						UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "patient_ref_group_id", Name: "patient_ref", Type: "string"},
						{ID: "active_rows_id", Name: "active_rows", Type: "integer"},
						{ID: "null_status_rows_id", Name: "null_status_rows", Type: "integer"},
					},
				},
			},
		},
	}
}

func TestRowLineagePivotCapabilityRejectsUnsupportedPolicies(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "lineage-project", DatasetGeneration: "lineage-generation"}
	compiled := lowerConstructionOutput(t, constructionPivotLineageOutput(), bindings)
	compiled.Plan.StageSequence.Stages[0].GroupedPivot.UnlistedCategoryPolicy = "EXCLUDE_WITH_EVIDENCE"
	if capability := RowLineageCapabilityForOutput(compiled); capability.Available {
		t.Fatalf("Pivot lineage capability admitted unsupported unlisted-category policy: %#v", capability)
	}
}

func constructionPivotLineageOutput() recipe.Output {
	zero := int64(0)
	return recipe.Output{
		Name: "construction_pivot_lineage", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "group_text", ColumnID: "group_text_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.valueInteger"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version: 1,
			SourceColumns: []recipe.StageColumn{
				{ID: "group_text_id", Name: "group_text"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"},
			},
			Steps: []recipe.ConstructionStep{{
				ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{
					ConstructionID: "row_lineage_pivot", GroupKeyIDs: []string{"group_text_id"},
					CategoryColumnID: "category_id", ValueColumnID: "amount_id",
					Categories: []recipe.ConstructionPivotCategory{
						{Key: recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &zero}, OutputColumnID: "zero_id"},
						{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_id"},
					},
					DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
					UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_text_id", Name: "group_text"}, {ID: "zero_id", Name: "zero"}, {ID: "null_id", Name: "null_value"},
				},
			}},
		},
	}
}

func TestRelatedExpandLineageCapabilityAdmitsIdentityPreservingSuffixAndRejectsInvalidRouteMetadata(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	filtered := lowerConstructionOutput(t, relatedExpandOracleOutput(recipe.ExpansionExclude), bindings)
	if capability := RowLineageCapabilityForOutput(filtered); !capability.Available {
		t.Fatalf("RELATED_EXPAND with identity-preserving suffix capability = %#v, want available", capability)
	}

	direct := lowerConstructionOutput(t, directRelatedExpandOutput(recipe.ExpansionExclude), bindings)
	direct.Plan.StageSequence.Stages[0].RelatedExpand.Route = append(
		direct.Plan.StageSequence.Stages[0].RelatedExpand.Route,
		direct.Plan.StageSequence.Stages[0].RelatedExpand.Route[0],
	)
	if capability := RowLineageCapabilityForOutput(direct); capability.Available || capability.ReasonCode != "ROW_LINEAGE_ROUTE_UNSUPPORTED" {
		t.Fatalf("route metadata that disagrees with its physical traversal = %#v, want route unsupported", capability)
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
		compiled.BindVars[rowLineageStage0RowIDBind] != rowID || compiled.BindVars[rowLineageRowIDBind] != rowID {
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
	compiled, err := CompileRowLineageOutput(output, forged, 0, 25, ir.DefaultPhysicalOptimizationPolicy())
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
	compiled, err := CompileRowLineageOutput(output, "not-json", 0, 10, ir.DefaultPhysicalOptimizationPolicy())
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
		if _, _, _, ok := parseRelatedExpandRowID(rowID, "step", false); ok {
			t.Errorf("row ID %q parsed as a row for a different operation", rowID)
		}
	}
	if _, _, _, ok := parseRelatedExpandRowID(`[["input","p"],["construction","step"],["empty"]]`, "step", true); !ok {
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

	rootKey, matches, ok := decodeRelatedRowLineageIdentity(rowID, stages, bindVars)
	if !ok || rootKey != "root-storage-key" || len(matches) != owners {
		t.Fatalf("decoded root=%q matches=%#v valid=%v", rootKey, matches, ok)
	}
	for index, match := range matches {
		if match.StageIndex != index || match.StageID != stages[index].ID || match.RowID != wantRows[index] || match.TerminalID != wantTerminals[index] || match.RowKind != "RELATED" {
			t.Errorf("owner %d decoded as %#v", index, match)
		}
	}

	bindVars["construction_2"] = "wrong-stage"
	if _, _, ok := decodeRelatedRowLineageIdentity(rowID, stages, bindVars); ok {
		t.Fatal("wrong construction owner was accepted in a nested identity")
	}
}

func TestCompileComposedRelatedRowLineageBindsEachOwnerAndPagesWitnesses(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	output := lowerConstructionOutput(t, composedRelatedExpandOutput(), bindings)
	if capability := RowLineageCapabilityForOutput(output); !capability.Available {
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
	compiled, err := CompileRowLineageOutput(output, finalRowID, 2, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if compiled.BindVars[rowLineageRootKeyBind] != "patient-storage-key" ||
		compiled.BindVars[rowLineageStage0RowIDBind] != firstRowID ||
		compiled.BindVars[rowLineageStage0TerminalIDBind] != "Observation/observation-storage-key" ||
		compiled.BindVars["row_lineage_stage_1_row_id"] != finalRowID ||
		compiled.BindVars["row_lineage_stage_1_terminal_id"] != "Patient/related-patient-storage-key" ||
		compiled.BindVars[rowLineageRowIDBind] != finalRowID {
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

const (
	rowLineageRootKeyBind          = "row_lineage_root_key"
	rowLineageStage0RowIDBind      = "row_lineage_stage_0_row_id"
	rowLineageStage0TerminalIDBind = "row_lineage_stage_0_terminal_id"
)

func directRelatedExpandOutput(emptyPolicy recipe.ExpansionEmptyPolicy) recipe.Output {
	output := relatedExpandOracleOutput(emptyPolicy)
	step := output.Construction.Steps[1]
	step.Inputs = []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}
	output.Construction.Steps = []recipe.ConstructionStep{step}
	return output
}

func composedRelatedExpandOutput() recipe.Output {
	output := directRelatedExpandOutput(recipe.ExpansionExclude)
	first := output.Construction.Steps[0]
	digest := sha256.Sum256([]byte(first.ID))
	activeObservationID := "__loom_related_terminal_id_" + hex.EncodeToString(digest[:])
	outputs := append(append([]recipe.StageColumn(nil), first.Outputs...), recipe.StageColumn{
		ID: "related-patient-id", Name: "related_patient_id", Type: "string",
	})
	output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{
		ID: "expand_related_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: first.ID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
			AnchorColumnID: activeObservationID, ChoiceID: "observation-patient-via-specimen",
			TargetNodeID: "patient-node", TargetResourceType: "Patient",
			Route: []recipe.ConstructionRelatedRouteStep{
				{EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node", FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"},
				{EdgeID: "specimen-patient", FromNodeID: "specimen-node", ToNodeID: "patient-node", FromResourceType: "Specimen", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"},
			},
			ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionExclude, RelatedRecordColumnID: "related-patient-id",
		}},
		Outputs: outputs,
	})
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

func constructionGroupFilterLineageOutput(thresholds ...int64) recipe.Output {
	output := constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup)
	previous := "group_status"
	columns := append([]recipe.StageColumn(nil), output.Construction.Steps[0].Outputs...)
	for index, threshold := range thresholds {
		value := threshold
		stepID := fmt.Sprintf("filter_group_count_%d", index+1)
		output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{
			ID: stepID, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: previous}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
				ColumnID: "row_count_id", Operator: recipe.FilterGreaterThan,
				Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &value}},
			}},
			Outputs: append([]recipe.StageColumn(nil), columns...),
		})
		previous = stepID
	}
	return output
}

func constructionGroupFilteredSummaryOutput() recipe.Output {
	output := constructionSummaryOutput()
	step := &output.Construction.Steps[0]
	group := step.Operation.Group
	group.Keys = []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "group_status_id"}}
	step.Outputs = append(step.Outputs, recipe.StageColumn{ID: "group_status_id", Name: "status", Type: "string"})
	threshold := 10.0
	stepID := "filter_summary_amount"
	filterOutputs := append([]recipe.StageColumn(nil), step.Outputs...)
	output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{
		ID: stepID, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: step.ID}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
			ColumnID: "amount_sum_id", Operator: recipe.FilterGreaterThan,
			Values: []recipe.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &threshold}},
		}},
		Outputs: filterOutputs,
	})
	return output
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
