package rowlineage_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
	explorerdb "github.com/calypr/loom/internal/explorer/arango/db"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestSemanticGroupUsesExactFamilyTupleAndReconstructsContributors(t *testing.T) {
	output := lowerConstructionOutput(t, semanticGroupOutputForLineageTest(), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := rowlineage.RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("direct semantic Group should retain contributor lineage: %#v", capability)
	}
	stage := output.Plan.StageSequence.Stages[0]
	if stage.Group == nil || stage.Group.SemanticKey == nil || stage.Group.SemanticKey.SourceIdentityColumn != "_key" {
		t.Fatalf("lowered semantic Group has no exact root-key family binding: %#v", stage.Group)
	}
	groupPlan, err := aql.Render(output.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"construction_group_semantic_raw_codings",
		"construction_group_semantic_coding.system == @",
		"construction_group_semantic_coding.version",
		"COLLECT __loom_physical_construction_construction_group_semantic_code =",
		"MIN((TYPENAME(__loom_physical_construction_construction_group_semantic_matched_coding.display)",
		"construction_group_semantic_label",
		"__loom_construction_group_semantic_tuple_1[2]",
	} {
		if !strings.Contains(groupPlan.Query, want) {
			t.Errorf("semantic Group AQL is missing %q:\n%s", want, groupPlan.Query)
		}
	}
	if strings.Contains(groupPlan.Query, "group_semantic_tuple.code") {
		t.Fatalf("display fallback must read the tuple's code slot, not an object attribute:\n%s", groupPlan.Query)
	}

	lineage, err := rowlineage.CompileRowLineageOutput(output, "semantic-row-id", 0, 5, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"row_lineage_semantic_selected_group",
		"row_lineage_semantic_group_family",
		"row_lineage_semantic_group_system",
		"semanticTuple:",
		"row_lineage_semantic_contributor",
		"FILTER [__loom_physical_construction_row_lineage_semantic_membership_1.system, __loom_physical_construction_row_lineage_semantic_membership_1.version, __loom_physical_construction_row_lineage_semantic_membership_1.code] == __loom_physical_construction_row_lineage_semantic_selected_group.semanticTuple",
		"LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
	} {
		if !strings.Contains(lineage.Query, want) {
			t.Errorf("semantic Group lineage query is missing %q:\n%s", want, lineage.Query)
		}
	}
}

func rowLineageHasBindValue(bindVars map[string]any, value string) bool {
	for _, candidate := range bindVars {
		if candidate == value {
			return true
		}
	}
	return false
}

func relatedCodingGroupLineageOutput(t *testing.T) lower.CompiledRecipeOutput {
	t.Helper()
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	baseOutput := recipe.Output{
		Name: "patient_related_group_source", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
	}
	base := lowerConstructionOutput(t, baseOutput, bindings)
	descriptor, err := lower.DescribeConstructionSourceStage(base.OutputSchema, baseOutput.RootResourceType, &base.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil {
		t.Fatalf("describe direct Patient source context: descriptor=%#v err=%v", descriptor, err)
	}
	context := descriptor.GroupFieldSourceContext
	makeSource := func(valueKind, resourceType, candidateID, canonicalPath string) recipe.ConstructionGroupFieldSource {
		related := &recipe.ConstructionGroupRelatedOccurrenceFieldSource{
			AnchorNodeID: "catalog-patient-node", AnchorResourceType: "Patient",
			Route: []recipe.ConstructionGroupRelatedRouteStep{{
				StepIndex: 0, FromNodeID: "catalog-patient-node", FromResourceType: "Patient",
				EdgeID: "patient-observation-edge", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
				ToNodeID: "catalog-observation-node", ToResourceType: "Observation",
			}},
			TargetNodeID: "catalog-observation-node", TargetResourceType: "Observation",
			ResourceType: resourceType, CandidateID: candidateID, CanonicalPath: canonicalPath,
			EmptyPolicy:  recipe.ExpansionPreserveParent,
			Multiplicity: recipe.ConstructionGroupRelatedMultiplicityDistinctTargetPerInput,
		}
		if valueKind == "CODING_IDENTITY" {
			related.ResourceType = "Coding"
			related.CodingSelector = "valueCodeableConcept.coding[]"
			related.CodingEmptyPolicy = recipe.ConstructionGroupCodingEmptyPolicyGroup
			related.OrdinalSemantics = recipe.ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition
		}
		source := recipe.ConstructionGroupFieldSource{
			Kind: "BINDING", ValueKind: valueKind,
			Input: &recipe.ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: related},
		}
		bound, err := lower.BindGroupRelatedOccurrenceSource(context, source)
		if err != nil {
			t.Fatalf("bind %s related Group source: %v", valueKind, err)
		}
		return bound
	}
	status := makeSource("SCALAR", "string", "observation-status", "status")
	coding := makeSource("CODING_IDENTITY", "Coding", "", "")
	quantity := makeSource("SCALAR", "decimal", "observation-value-quantity-value", "valueQuantity.value")
	output := recipe.Output{
		Name: "patient_related_coding_group", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "patient_id", Name: "patient_id"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_related", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_related_id", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys: []recipe.ConstructionGroupKey{
						{OutputColumnID: "status_group_id", FieldSource: &status},
						{OutputColumnID: "coding_group_id", FieldSource: &coding},
					},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "status_group_id", Name: "status_group", Type: "string"},
					{ID: "coding_group_id", Name: "coding_group", Type: "string"},
					{ID: "row_count_id", Name: "row_count", Type: "integer"},
					{ID: "quantity_values_id", Name: "quantity_values", Type: "array"},
				},
				RowValues: []recipe.ConstructionRowValue{{
					FieldSource: &quantity, OutputColumnID: "quantity_values_id", Policy: recipe.ConstructionRowValueAll,
				}},
			}},
		},
	}
	return lowerConstructionOutput(t, output, bindings)
}

func expandedCodingRowLineageOutput(t *testing.T) lower.CompiledRecipeOutput {
	t.Helper()
	output := recipe.Output{
		Name: "expanded_coding_lineage", RootResourceType: "Observation", RootOccurrenceID: "root-observation", RowGrain: "expanded",
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "root-observation", From: recipe.Expression{Select: "root.valueCodeableConcept.coding[]"},
			As: "coding", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionExclude,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	bindings := recipe.RuntimeBindings{Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation"}
	source := lowerConstructionOutput(t, output, bindings)
	descriptor, err := lower.DescribeConstructionSourceStage(source.OutputSchema, output.RootResourceType, &source.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil || descriptor.GroupFieldSourceContext.SourceExpansion == nil {
		t.Fatalf("describe expanded source context: descriptor=%#v err=%v", descriptor, err)
	}
	context := descriptor.GroupFieldSourceContext
	output.Construction = &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{{
		ID: "group_codings", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "coding-lineage-group", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
			Keys: []recipe.ConstructionGroupKey{{
				OutputColumnID: "coding_label_id",
				FieldSource: &recipe.ConstructionGroupFieldSource{
					Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: context.SourceStageID,
					SourceShapeID: context.SourceShapeID, SourceRole: "CURRENT_ITEM",
					OccurrenceID: context.SourceExpansion.OwnerOccurrenceID, OwnerResourceType: context.SourceExpansion.OwnerResourceType,
					ResourceType: "Coding", CanonicalPath: context.SourceExpansion.Selector,
				},
			}},
			Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		}},
		RowValues: []recipe.ConstructionRowValue{{
			FieldSource: &recipe.ConstructionGroupFieldSource{
				Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: context.SourceStageID,
				SourceShapeID: context.SourceShapeID, SourceRole: "ROOT", OccurrenceID: "root-observation",
				OwnerResourceType: "Observation", ResourceType: "Observation", CandidateID: "observation-issued", CanonicalPath: "issued",
			},
			OutputColumnID: "issued_values_id", Policy: recipe.ConstructionRowValueAll,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "coding_label_id", Name: "coding_label", Type: "string"},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "issued_values_id", Name: "issued_values", Type: "array"},
		},
	}}}
	return lowerConstructionOutput(t, output, bindings)
}

func expandedCodingGroupFixtureLineageOutput(t *testing.T) lower.CompiledRecipeOutput {
	return expandedCodingGroupFixtureLineageOutputWithAggregate(t, recipe.ConstructionGroupAggregate{
		Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "occurrences_id",
	})
}

func expandedCodingGroupFixtureLineageOutputWithAggregate(t *testing.T, aggregate recipe.ConstructionGroupAggregate) lower.CompiledRecipeOutput {
	t.Helper()
	selectionID := "row-lineage-selection"
	output := recipe.Output{
		Name: "expanded_coding_field_source", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		TraversalColumnNaming: recipe.TraversalColumnNamingExact,
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner", From: recipe.Expression{Select: "observation.valueCodeableConcept.coding[]"},
			As: "coding_item", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionPreserveParent,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation", MatchMode: recipe.MatchRequired,
			Fields: []recipe.Field{
				{Name: "owner_id", ColumnID: "owner_id", Expr: recipe.Expression{Select: "observation.id"}},
				{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "observation.status"}},
			},
		}},
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: selectionID, MembershipDigest: selectionID + "-membership", MemberCount: 1, ResourceType: "Patient",
		},
	}
	bindings := recipe.RuntimeBindings{
		Project: "row-lineage-project", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "row-lineage-project",
		SelectionMembersCollection: "loom_explorer_selection_members", DatasetGeneration: "row-lineage-generation",
	}
	source := lowerConstructionOutput(t, output, bindings)
	descriptor, err := lower.DescribeConstructionSourceStage(source.OutputSchema, output.RootResourceType, &source.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil || descriptor.GroupFieldSourceContext.SourceExpansion == nil {
		t.Fatalf("describe exact fixture Group source context: descriptor=%#v err=%v", descriptor, err)
	}
	context := descriptor.GroupFieldSourceContext
	output.Construction = &recipe.Construction{
		Version:       1,
		SourceColumns: []recipe.StageColumn{{ID: "owner_id", Name: "owner_id", Type: "string"}, {ID: "status_id", Name: "status", Type: "string"}},
		Steps: []recipe.ConstructionStep{{
			ID: "group_expanded_coding", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
			Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
				ConstructionID: "group_expanded_coding", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
				Keys: []recipe.ConstructionGroupKey{
					{InputColumnID: "status_id", OutputColumnID: "group_status_id"},
					{OutputColumnID: "coding_id", FieldSource: &recipe.ConstructionGroupFieldSource{
						Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: context.SourceStageID,
						SourceShapeID: context.SourceShapeID, SourceRole: "CURRENT_ITEM",
						OccurrenceID: context.SourceExpansion.OwnerOccurrenceID, OwnerResourceType: context.SourceExpansion.OwnerResourceType,
						ResourceType: "Coding", CanonicalPath: context.SourceExpansion.Selector,
					}},
				},
				Aggregates: []recipe.ConstructionGroupAggregate{aggregate},
			}},
			RowValues: []recipe.ConstructionRowValue{{InputColumnID: "owner_id", OutputColumnID: "owners_id", Policy: recipe.ConstructionRowValueAll}},
			Outputs: []recipe.StageColumn{
				{ID: "group_status_id", Name: "status", Type: "string"},
				{ID: "coding_id", Name: "coding", Type: "string", Nullable: true},
				{ID: "occurrences_id", Name: "occurrences", Type: "integer"},
				{ID: "owners_id", Name: "owners", Type: "array"},
			},
		}},
	}
	return lowerConstructionOutput(t, output, bindings)
}

func routedAncestorCodingRowLineageOutput(t *testing.T) lower.CompiledRecipeOutput {
	t.Helper()
	output := recipe.Output{
		Name: "routed_ancestor_coding_lineage", RootResourceType: "Patient", RootOccurrenceID: "patient-root", RowGrain: "expanded",
		Population: &recipe.PopulationConstraint{
			SelectionRevisionID: "lineage-selection", MembershipDigest: "sha256:lineage-members", MemberCount: 1, ResourceType: "Patient",
		},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation",
		}},
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner", From: recipe.Expression{Select: "observation.component[].code.coding[]"},
			As: "coding", Ordinality: "coding_ordinal", EmptyPolicy: recipe.ExpansionExclude,
		},
		Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
	}
	bindings := recipe.RuntimeBindings{
		Project: "row-lineage-project", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "row-lineage-project",
		SelectionMembersCollection: "loom_explorer_selection_members", DatasetGeneration: "row-lineage-generation",
	}
	source := lowerConstructionOutput(t, output, bindings)
	descriptor, err := lower.DescribeConstructionSourceStage(source.OutputSchema, output.RootResourceType, &source.Plan)
	if err != nil || descriptor.GroupFieldSourceContext == nil || descriptor.GroupFieldSourceContext.SourceExpansion == nil {
		t.Fatalf("describe routed expanded source context: descriptor=%#v err=%v", descriptor, err)
	}
	context := descriptor.GroupFieldSourceContext
	output.Construction = &recipe.Construction{Version: 1, Steps: []recipe.ConstructionStep{{
		ID: "group_routed_codings", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_routed_codings", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
			Keys: []recipe.ConstructionGroupKey{
				{OutputColumnID: "gender_id", FieldSource: &recipe.ConstructionGroupFieldSource{
					Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: context.SourceStageID,
					SourceShapeID: context.SourceShapeID, SourceRole: "ROOT", OccurrenceID: "patient-root",
					OwnerResourceType: "Patient", ResourceType: "Patient", CandidateID: "patient-gender", CanonicalPath: "gender",
				}},
				{OutputColumnID: "coding_label_id", FieldSource: &recipe.ConstructionGroupFieldSource{
					Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: context.SourceStageID,
					SourceShapeID: context.SourceShapeID, SourceRole: "CURRENT_ITEM",
					OccurrenceID: context.SourceExpansion.OwnerOccurrenceID, OwnerResourceType: context.SourceExpansion.OwnerResourceType,
					ResourceType: "Coding", CanonicalPath: context.SourceExpansion.Selector,
				}},
			},
			Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
		}},
		RowValues: []recipe.ConstructionRowValue{{
			FieldSource: &recipe.ConstructionGroupFieldSource{
				Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: context.SourceStageID,
				SourceShapeID: context.SourceShapeID, SourceRole: "ANCESTOR", OccurrenceID: context.SourceExpansion.OwnerOccurrenceID,
				OwnerResourceType: "Observation", ResourceType: "ObservationComponent", CandidateID: "component-value-quantity",
				CanonicalPath: "valueQuantity.value", BoundarySelector: "component[]",
			},
			OutputColumnID: "component_values_id", Policy: recipe.ConstructionRowValueAll,
		}},
		Outputs: []recipe.StageColumn{
			{ID: "gender_id", Name: "gender", Type: "string", Nullable: true},
			{ID: "coding_label_id", Name: "coding_label", Type: "string"},
			{ID: "rows_id", Name: "rows", Type: "integer"},
			{ID: "component_values_id", Name: "component_values", Type: "array"},
		},
	}}}
	return lowerConstructionOutput(t, output, bindings)
}

func semanticGroupOutputForLineageTest() recipe.Output {
	return recipe.Output{
		Name: "specimen_semantic_group", RootResourceType: "Specimen", RowGrain: "specimen",
		Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_type", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_type", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys: []recipe.ConstructionGroupKey{
						{InputColumnID: "specimen_id", OutputColumnID: "specimen_id_group"},
						{SemanticCategory: &recipe.ConstructionGroupSemanticCategory{
							Kind: "BINDING", BindingID: "binding-specimen-type", RuleVersion: "5",
							FamilyID: "family-specimen-type", SourceKind: "file", System: "urn:specimen-type",
							Version: "", ResourceType: "Specimen", CodingPath: "type.coding[]",
						}, OutputColumnID: "specimen_type"},
					},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows"}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "specimen_id_group", Name: "specimen_id", Type: "string"},
					{ID: "specimen_type", Name: "specimen_type", Label: "Specimen type", Type: "string"},
					{ID: "rows", Name: "rows", Type: "integer"},
				},
			}},
		},
	}
}

func TestCompileRowLineageUsesBoundedCanonicalGroupTerminal(t *testing.T) {
	output := lowerConstructionOutput(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := rowlineage.RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("unsupported source operation: %s", capability.Operation)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "opaque-group-row", 25, 10, ir.DefaultPhysicalOptimizationPolicy())
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
	if compiled.BindVars[rowlineage.RowLineageRowIDBind] != "opaque-group-row" || compiled.BindVars[rowlineage.RowLineageOffsetBind] != 25 ||
		compiled.BindVars[rowlineage.RowLineageLimitBind] != 10 || compiled.BindVars[rowlineage.RowLineageFetchLimitBind] != 11 {
		t.Fatalf("row lineage page bindings = %#v", compiled.BindVars)
	}
}

func TestCompileStandaloneExplicitGroupRowLineageUsesPinnedBoundedMembers(t *testing.T) {
	output := lowerConstructionOutput(t, recipe.Output{
		Name: "NamedCohort", RootResourceType: "Observation", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_row_lineage", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED"},
	}, recipe.RuntimeBindings{Project: "row-lineage-project", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "row-lineage-project", DatasetGeneration: "row-lineage-generation"})
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("standalone explicit GROUP_ROWS lineage capability = %#v", capability)
	}
	rowID, err := json.Marshal(map[string]string{"group_revision_id": "grouprev_row_lineage", "group_id": "cohort-a"})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, string(rowID), 3, 7, ir.DefaultPhysicalOptimizationPolicy())
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
		invalid, compileErr := rowlineage.CompileRowLineageOutput(output, invalidID, 0, 7, ir.DefaultPhysicalOptimizationPolicy())
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
					Values: []spec.FilterValue{{Kind: recipe.FilterString, String: &filterValue}},
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
		Project: "row-lineage-project", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/p1"},
	}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := rowlineage.RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
		t.Fatalf("cohort Group with trailing filter has no lineage capability: %#v", capability)
	}
	rowID, err := json.Marshal(map[string]string{"group_revision_id": "grouprev_cohort_row_lineage", "group_id": "cohort-a"})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(compiledOutput, string(rowID), 2, 4, ir.DefaultPhysicalOptimizationPolicy())
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
		invalid, compileErr := rowlineage.CompileRowLineageOutput(compiledOutput, invalidID, 0, 4, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile invalid cohort row ID %q: %v", invalidID, compileErr)
		}
		if invalid.BindVars["__loom_physical_row_lineage_group_revision_id"] != "" ||
			invalid.BindVars["__loom_physical_row_lineage_group_id"] != "" {
			t.Errorf("invalid cohort row ID %q retained binds %#v", invalidID, invalid.BindVars)
		}
	}
	forged, err := rowlineage.CompileRowLineageOutput(compiledOutput,
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
	if capability := rowlineage.RowLineageCapabilityForOutput(memberFilterOutput); capability.Available || capability.Operation != string(ir.PhysicalStageFilterOp) {
		t.Fatalf("array-valued member filter unexpectedly claimed scalar cohort lineage support: %#v", capability)
	}
}

func TestCompileGroupRowLineageAppliesEveryTrailingFilter(t *testing.T) {
	output := lowerConstructionOutput(t, constructionGroupFilterLineageOutput(104, 103), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := rowlineage.RowLineageCapabilityForOutput(output)
	if !capability.Available {
		t.Fatalf("Group with trailing filters has no lineage capability: %#v", capability)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "opaque-filtered-group-row", 25, 10, ir.DefaultPhysicalOptimizationPolicy())
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
	if got := rowlineage.RowLineageCapabilityForOutput(changedSchema); got.Available || got.ReasonCode != "ROW_LINEAGE_OPERATION_UNSUPPORTED" {
		t.Fatalf("filter changing its schema retained lineage capability: %#v", got)
	}
}

func TestCompileNonCountGroupFilterLineageProjectsSourceRowsInScope(t *testing.T) {
	output := lowerConstructionOutput(t, constructionGroupFilteredSummaryOutput(), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("non-count Group with a trailing filter has no lineage capability: %#v", capability)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "opaque-filtered-summary-row", 0, 25, ir.DefaultPhysicalOptimizationPolicy())
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
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("valid physical Group identity rejected: %#v", capability)
	}

	withoutSemanticIdentity := output
	withoutSemanticIdentity.RowIdentity = nil
	if capability := rowlineage.RowLineageCapabilityForOutput(withoutSemanticIdentity); capability.Available || capability.ReasonCode != "ROW_LINEAGE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("Group without declared publication identity capability = %#v, want identity unavailable", capability)
	}

	output.Plan.StageSequence.FinalRowIdentity = "forged_identity"
	capability := rowlineage.RowLineageCapabilityForOutput(output)
	if capability.Available || capability.ReasonCode != "ROW_LINEAGE_IDENTITY_UNAVAILABLE" {
		t.Fatalf("mismatched physical Group identity binding capability = %#v, want identity unavailable", capability)
	}
}

func TestCompileKeylessEmptyGroupHasExplicitEmptyContributorPage(t *testing.T) {
	output := lowerConstructionOutput(t, constructionCountRowsOnlyOutput(), recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("keyless Group capability = %#v, want available", capability)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "opaque-empty-group-row", 0, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compiled.Query, "COLLECT WITH COUNT INTO") || !strings.Contains(compiled.Query, "contributors: SLICE") || strings.Contains(compiled.Query, "COLLECT "+"__loom_physical_row_lineage_group_key") {
		t.Fatalf("keyless Group lineage terminal does not preserve the empty Group row and bounded empty page:\n%s", compiled.Query)
	}
	if compiled.Limit != rowlineage.DefaultRowLineageLimit || compiled.BindVars[rowlineage.RowLineageFetchLimitBind] != rowlineage.DefaultRowLineageLimit+1 {
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
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
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
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("CODED_GROUP lineage capability = %#v", capability)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(output, "opaque-coded-group-row", 4, 10, ir.DefaultPhysicalOptimizationPolicy())
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
	if compiled.BindVars[rowlineage.RowLineageRowIDBind] != "opaque-coded-group-row" || compiled.BindVars[rowlineage.RowLineageOffsetBind] != 4 ||
		compiled.BindVars[rowlineage.RowLineageLimitBind] != 10 || compiled.BindVars[rowlineage.RowLineageFetchLimitBind] != 11 {
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
	if capability := rowlineage.RowLineageCapabilityForOutput(output); !capability.Available {
		t.Fatalf("population-selected CODED_GROUP lineage capability = %#v", capability)
	}
}

func TestRowLineageCapabilityRejectsEarlierConstructionOperations(t *testing.T) {
	output := constructionOracleOutput()
	compiled := lowerConstructionOutput(t, output, recipe.RuntimeBindings{
		Project: "row-lineage-project", DatasetGeneration: "row-lineage-generation",
	})
	capability := rowlineage.RowLineageCapabilityForOutput(compiled)
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
		Project: "lineage-project", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "lineage-selection-project",
		SelectionMembersCollection: "loom_explorer_selection_members", DatasetGeneration: "lineage-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/lineage"},
	}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := rowlineage.RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
		t.Fatalf("direct construction Pivot lineage capability = %#v", capability)
	}
	rowID, err := json.Marshal([]any{"GROUPED_PIVOT", "row_lineage_pivot", []any{"STRING", nil}})
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := rowlineage.CompileRowLineageOutput(compiledOutput, string(rowID), 4, 7, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"population_member.selectionId == @population_selection_id",
		"population_member.project == @population_project",
		"population_member.generation == @dataset_generation",
		"population_member.resourceType == @population_resource_type",
		"auth_resource_path IN @auth_resource_paths",
		"COLLECT AGGREGATE", "LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
	} {
		if !strings.Contains(compiled.Query, want) {
			t.Errorf("construction Pivot lineage query is missing %q:\n%s", want, compiled.Query)
		}
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
	if compiled.BindVars[rowlineage.RowLineageRowIDBind] != string(rowID) || compiled.BindVars[rowlineage.RowLineageOffsetBind] != 4 ||
		compiled.BindVars[rowlineage.RowLineageLimitBind] != 7 || compiled.BindVars[rowlineage.RowLineageFetchLimitBind] != 8 {
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
		forged, compileErr := rowlineage.CompileRowLineageOutput(compiledOutput, forgedID, 0, 7, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile forged Pivot row ID %q: %v", forgedID, compileErr)
		}
		if got := forged.BindVars[rowlineage.RowLineageRowIDBind]; got != forgedID {
			t.Errorf("requested Pivot row ID binding = %q, want %q", got, forgedID)
		}
		if key, ok := forged.BindVars["row_lineage_pivot_group_key_0"]; !ok || key != nil {
			t.Errorf("forged Pivot row ID %q did not fail closed with a null key bind: %#v", forgedID, forged.BindVars)
		}
	}

	compiledOutput.Plan.StageSequence.Stages[0].GroupedPivot.DuplicatePolicy = "SUM"
	compiledOutput.Plan.StageSequence.Stages[0].GroupedPivot.MissingCellPolicy = "ERROR"
	summed, err := rowlineage.CompileRowLineageOutput(compiledOutput, string(rowID), 0, 7, ir.DefaultPhysicalOptimizationPolicy())
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
		Project: "group-pivot-project", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "group-pivot-selection-project",
		SelectionMembersCollection: "loom_explorer_selection_members", DatasetGeneration: "group-pivot-generation",
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/programs/group-pivot"},
	}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	if capability := rowlineage.RowLineageCapabilityForOutput(compiledOutput); !capability.Available {
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
	compiled, err := rowlineage.CompileRowLineageOutput(compiledOutput, string(rowID), 6, 9, ir.DefaultPhysicalOptimizationPolicy())
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
	if compiled.BindVars["row_lineage_pivot_group_key_0"] != "patient-7" || compiled.BindVars[rowlineage.RowLineageRowIDBind] != string(rowID) ||
		compiled.BindVars[rowlineage.RowLineageOffsetBind] != 6 || compiled.BindVars[rowlineage.RowLineageLimitBind] != 9 || compiled.BindVars[rowlineage.RowLineageFetchLimitBind] != 10 {
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
		policyCompiled, policyErr := rowlineage.CompileRowLineageOutput(policyOutput, string(rowID), 0, 9, ir.DefaultPhysicalOptimizationPolicy())
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
		capability := rowlineage.RowLineageCapabilityForOutput(mutated)
		if capability.Available || capability.ReasonCode == "" {
			t.Errorf("unsupported composed Group/Pivot %s capability = %#v", name, capability)
		}
	}
}

func TestRowLineagePivotCapabilityRejectsUnsupportedPolicies(t *testing.T) {
	bindings := recipe.RuntimeBindings{Project: "lineage-project", DatasetGeneration: "lineage-generation"}
	compiled := lowerConstructionOutput(t, constructionPivotLineageOutput(), bindings)
	compiled.Plan.StageSequence.Stages[0].GroupedPivot.UnlistedCategoryPolicy = "EXCLUDE_WITH_EVIDENCE"
	if capability := rowlineage.RowLineageCapabilityForOutput(compiled); capability.Available {
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

const (
	rowLineageRootKeyBind          = "row_lineage_root_key"
	rowLineageStage0RowIDBind      = "row_lineage_stage_0_row_id"
	rowLineageStage0TerminalIDBind = "row_lineage_stage_0_terminal_id"
)

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
				Values: []spec.FilterValue{{Kind: recipe.FilterInteger, Integer: &value}},
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
			Values: []spec.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &threshold}},
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
