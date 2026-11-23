package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestTerminalGroupAllRowValuePreviewWindowsAfterCompleteGroupKeys(t *testing.T) {
	plan := groupRowValuePreviewTestPlan("ALL")
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	if _, _, ok := terminalGroupRowValuePreviewWindowFor(plan, sourcePlan, plan.StageSequence, physicalRenderOptions{}, false); !ok {
		t.Fatal("test Group source did not satisfy the terminal row-value preview shape")
	}
	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	query := rendered.Query
	frontier := strings.Index(query, "COLLECT __loom_physical_construction_group_preview_group_key")
	if frontier < 0 {
		t.Fatalf("query has no selected-group frontier:\n%s", query)
	}
	frontierLimit := strings.Index(query[frontier:], "LIMIT @preview_limit")
	frontierReturn := strings.Index(query[frontier:], "RETURN __loom_physical_construction_group_preview_root_key")
	if frontierLimit < 0 || frontierReturn < 0 || frontierLimit > frontierReturn {
		t.Fatalf("frontier must sort/group then limit complete groups and return all member root keys:\n%s", query)
	}
	if !strings.Contains(query[frontier:], "INTO __loom_physical_construction_group_preview_root_key = __loom_physical_construction_group_preview_source_row.__loom_preview_root_key") {
		t.Fatalf("frontier does not retain every contributor root key:\n%s", query)
	}
	if !strings.Contains(query, "FILTER ASSERT(__loom_physical_construction_group_preview_group_key == null OR TYPENAME(__loom_physical_construction_group_preview_group_key) == @__loom_physical_construction_group_preview_key_type") {
		t.Fatalf("frontier omitted the canonical nullable typed-key assertion:\n%s", query)
	}
	if !strings.Contains(query, "SORT __loom_physical_construction_group_preview_group_key ASC") {
		t.Fatalf("frontier changed canonical Group-key ordering:\n%s", query)
	}
	if !strings.Contains(query, "LET __loom_physical_construction_group_preview_selected_root_keys = FLATTEN(") {
		t.Fatalf("frontier did not flatten complete selected-group root-key sets:\n%s", query)
	}
	if !strings.Contains(query, "FOR __loom_physical_preview_root_key IN __loom_physical_construction_group_preview_selected_root_keys") ||
		!strings.Contains(query, "LET root = DOCUMENT(@@root_collection, __loom_physical_preview_root_key)") {
		t.Fatalf("canonical source did not use the existing root-key execution window:\n%s", query)
	}
	keyWindow := strings.Index(query, "LET root = DOCUMENT(@@root_collection, __loom_physical_preview_root_key)")
	rowValueWork := strings.Index(query, "root.payload.values")
	canonicalGroup := strings.LastIndex(query, "COLLECT group_key_value = group_input[@")
	finalLimit := strings.LastIndex(query, "LIMIT @preview_limit")
	if keyWindow < 0 || rowValueWork < keyWindow || canonicalGroup < rowValueWork || finalLimit < canonicalGroup {
		t.Fatalf("row-value work must follow group-key selection and the final complete GROUP must still precede its preview LIMIT:\n%s", query)
	}
	if strings.Count(query, "FILTER root.project == @project") != 2 ||
		strings.Count(query, "FILTER root.dataset_generation == @dataset_generation") != 2 ||
		strings.Count(query, "root.auth_resource_path IN @auth_resource_paths") != 2 {
		t.Fatalf("key frontier or canonical source dropped source/auth predicates:\n%s", query)
	}
}

func TestTerminalGroupRowValuePreviewPreservesDirectScalarExtractKeyProjection(t *testing.T) {
	plan := groupRowValuePreviewTestPlan("ALL")
	plan.Source.ResourceType = "Condition"
	plan.BindVars["root_collection"] = "Condition"
	setGroupRowValuePreviewExtractKey(plan.Operations[len(plan.Operations)-1].Return, "Condition", "onsetString")
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	frontierPlan, _, ok := terminalGroupRowValuePreviewWindowFor(plan, sourcePlan, plan.StageSequence, physicalRenderOptions{}, false)
	if !ok {
		t.Fatal("compiler-shaped direct scalar EXTRACT key projection was rejected")
	}
	frontierProjection := frontierPlan.Operations[len(frontierPlan.Operations)-1].Return.Projections[0]
	if frontierProjection.Name != groupRowValuePreviewKeyColumn || frontierProjection.Expression == nil || frontierProjection.Presence == nil ||
		frontierProjection.Expression.Kind != ir.PhysicalExtractExpression ||
		frontierProjection.Expression.Extract == nil || frontierProjection.Expression.Extract.Selector.Steps[0].Field != "onsetString" ||
		len(frontierProjection.Presence.Paths) != 1 || frontierProjection.Presence.Paths[0][0] != "onsetString" {
		t.Fatalf("frontier did not preserve the exact compiler key expression and presence proof: %#v", frontierProjection)
	}
	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	if !strings.Contains(rendered.Query, "root.payload.onsetString") || !strings.Contains(rendered.Query, "group_preview_selected_root_keys = FLATTEN(") {
		t.Fatalf("frontier did not render the preserved direct-scalar key expression before the selected-root window:\n%s", rendered.Query)
	}
}

func TestTerminalGroupRowValuePreviewRejectsNonDirectScalarExtractKey(t *testing.T) {
	for _, name := range []string{"generic mode", "mismatched presence"} {
		t.Run(name, func(t *testing.T) {
			plan := groupRowValuePreviewTestPlan("ALL")
			plan.Source.ResourceType = "Condition"
			plan.BindVars["root_collection"] = "Condition"
			setGroupRowValuePreviewExtractKey(plan.Operations[len(plan.Operations)-1].Return, "Condition", "onsetString")
			projection := &plan.Operations[len(plan.Operations)-1].Return.Projections[0]
			if name == "generic mode" {
				projection.Expression.Extract.ExecutionMode = ir.PhysicalSelectorGeneric
			} else {
				projection.Presence.Paths[0][0] = "different_field"
			}
			rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{})
			if err != nil {
				t.Fatalf("renderPhysicalStageSequence() error = %v", err)
			}
			if strings.Contains(rendered.Query, "group_preview_selected_root_keys") {
				t.Fatalf("unsupported key projection entered the preview frontier:\n%s", rendered.Query)
			}
		})
	}
}

func setGroupRowValuePreviewExtractKey(returnOp *ir.PhysicalReturn, resourceType, field string) {
	if returnOp == nil || len(returnOp.Projections) == 0 {
		return
	}
	source := ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}
	returnOp.Projections[0] = ir.PhysicalProjection{
		Name: "group_key",
		Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{
				Source: source, ResourceType: resourceType,
				Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: field}}},
				ExecutionMode: ir.PhysicalSelectorDirectScalar,
			},
		},
		Presence: &ir.PhysicalProjectionPresence{Source: source, Paths: [][]string{{field}}},
	}
}

func TestTerminalGroupRowValuePreviewLeavesNonAllPolicyOnGenericPath(t *testing.T) {
	plan := groupRowValuePreviewTestPlan("ONE")
	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	if strings.Contains(rendered.Query, "group_preview_root_keys_by_group") || strings.Contains(rendered.Query, "group_preview_selected_root_keys") {
		t.Fatalf("unsupported ONE row-value shape entered the ALL-only preview window:\n%s", rendered.Query)
	}
	rowValueWork := strings.Index(rendered.Query, "root.payload.values")
	group := strings.Index(rendered.Query, "COLLECT group_key_value = group_input[@")
	if rowValueWork < 0 || group < rowValueWork {
		t.Fatalf("generic fallback did not retain the original source-before-GROUP order:\n%s", rendered.Query)
	}
}

func TestTerminalGroupRowValuePreviewRequiresDeferredProjectionWork(t *testing.T) {
	plan := groupRowValuePreviewTestPlan("ALL")
	last := len(plan.Operations) - 1
	plan.Operations[last].Return.Projections[1] = ir.PhysicalProjection{
		Name: "row_tags", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "values"}},
	}
	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	if strings.Contains(rendered.Query, "group_preview_root_keys_by_group") || strings.Contains(rendered.Query, "group_preview_selected_root_keys") {
		t.Fatalf("direct root-only row values should remain on the canonical renderer path:\n%s", rendered.Query)
	}
}

func TestTerminalGroupRowValuePreviewKeepsHintedOrdinaryGroupOnGenericPath(t *testing.T) {
	plan := groupRowValuePreviewTestPlan("ALL")
	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{rootIndexHint: "loom_pivot_preview_test"})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	if strings.Contains(rendered.Query, "group_preview_selected_root_keys") {
		t.Fatalf("ordinary row-value Group changed its preexisting hinted render path:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, `OPTIONS { indexHint: "loom_pivot_preview_test", forceIndexHint: false }`) {
		t.Fatalf("ordinary Group lost its existing non-forcing root-index hint:\n%s", rendered.Query)
	}
}

func TestTerminalGroupRowValuePreviewRejectsPostScopeSourceFilter(t *testing.T) {
	plan := groupRowValuePreviewTestPlan("ALL")
	last := len(plan.Operations) - 1
	plan.BindVars["active"] = true
	plan.Operations = append(plan.Operations[:last], append([]ir.PhysicalOperation{{
		Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "active"}},
			Right: &ir.PhysicalValue{BindKey: "active"},
		}},
	}}, plan.Operations[last:]...)...)
	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	if strings.Contains(rendered.Query, "group_preview_root_keys_by_group") || strings.Contains(rendered.Query, "group_preview_selected_root_keys") {
		t.Fatalf("post-scope source FILTER must stay on the canonical renderer path:\n%s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "FILTER root.payload.active == @active") {
		t.Fatalf("canonical source FILTER was dropped from fallback:\n%s", rendered.Query)
	}
}

func TestTerminalSemanticGroupPreviewWindowsByLeadingScalarKey(t *testing.T) {
	plan := semanticGroupPreviewTestPlan(ir.PhysicalStageGroupMissingKeyGroup)
	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{rootIndexHint: "loom_pivot_preview_test"})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	query := rendered.Query
	frontier := strings.Index(query, "LET __loom_physical_construction_group_preview_source_rows = (")
	keyAssertion := strings.Index(query, "FILTER ASSERT(__loom_physical_construction_group_preview_group_key == null OR TYPENAME(__loom_physical_construction_group_preview_group_key)")
	frontierLimit := strings.Index(query, "LIMIT @preview_limit")
	selectedRoots := strings.Index(query, "LET __loom_physical_construction_group_preview_selected_root_keys = FLATTEN(")
	rootWindow := strings.Index(query, "LET root = DOCUMENT(@@root_collection, __loom_physical_preview_root_key)")
	semanticMembership := strings.Index(query, "[{system: null, version: null, code: null, display: null}]")
	groupStage := strings.Index(query, "LET __loom_construction_stage_1 = (")
	groupCollect := strings.LastIndex(query, "COLLECT ")
	finalLimit := strings.LastIndex(query, "LIMIT @preview_limit")
	if frontier < 0 || keyAssertion < frontier || keyAssertion > frontierLimit || selectedRoots < frontierLimit ||
		rootWindow < selectedRoots || groupStage < rootWindow || semanticMembership < groupStage ||
		groupCollect < semanticMembership || finalLimit < groupCollect || finalLimit == frontierLimit {
		t.Fatalf("semantic membership and final Group must run only after the complete scalar-key frontier, with key errors checked before its limit (frontier=%d key-assert=%d frontier-limit=%d selected-roots=%d root-window=%d semantic=%d stage=%d collect=%d final-limit=%d):\n%s",
			frontier, keyAssertion, frontierLimit, selectedRoots, rootWindow, semanticMembership, groupStage, groupCollect, finalLimit, query)
	}
	if !strings.Contains(query, "SORT __loom_physical_construction_group_preview_group_key ASC") {
		t.Fatalf("frontier changed the leading ordinary Group-key ordering:\n%s", query)
	}
	if strings.Count(query, "FILTER root.project == @project") != 2 ||
		strings.Count(query, "FILTER root.dataset_generation == @dataset_generation") != 2 ||
		strings.Count(query, "root.auth_resource_path IN @auth_resource_paths") != 2 {
		t.Fatalf("frontier or canonical source dropped a scope predicate:\n%s", query)
	}
	hint := `OPTIONS { indexHint: "loom_pivot_preview_test", forceIndexHint: false }`
	if strings.Count(query, hint) != 1 || strings.Index(query, hint) > rootWindow {
		t.Fatalf("the non-forcing hint must remain on the frontier scan and must not alter the DOCUMENT root window:\n%s", query)
	}
}

func TestTerminalSemanticGroupPreviewKeepsUnsupportedShapesOnGenericPath(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ir.PhysicalPlan)
	}{
		{
			name: "exclude missing policy",
			mutate: func(plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].Group.MissingKeyPolicy = ir.PhysicalStageGroupMissingKeyExclude
			},
		},
		{
			name: "error missing policy",
			mutate: func(plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].Group.MissingKeyPolicy = ir.PhysicalStageGroupMissingKeyError
			},
		},
		{
			name: "semantic key is not bound to the direct root",
			mutate: func(plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].Group.SemanticKey.SourceRootVariable = "other_root"
			},
		},
		{
			name: "semantic root identity is not projected",
			mutate: func(plan *ir.PhysicalPlan) {
				last := len(plan.Operations) - 1
				projections := plan.Operations[last].Return.Projections[:0]
				for _, projection := range plan.Operations[last].Return.Projections {
					if projection.Name != "_key" {
						projections = append(projections, projection)
					}
				}
				plan.Operations[last].Return.Projections = projections
			},
		},
		{
			name: "semantic collection does not match root scan",
			mutate: func(plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].Group.SemanticKey.RootCollectionBindKey = "other_collection"
			},
		},
		{
			name: "no leading scalar key",
			mutate: func(plan *ir.PhysicalPlan) {
				plan.StageSequence.Stages[0].Group.Keys = nil
			},
		},
		{
			name: "population root source",
			mutate: func(plan *ir.PhysicalPlan) {
				plan.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := semanticGroupPreviewTestPlan(ir.PhysicalStageGroupMissingKeyGroup)
			test.mutate(&plan)
			sourcePlan := ir.ClonePhysicalPlan(plan)
			sourcePlan.StageSequence = nil
			if _, _, ok := terminalGroupRowValuePreviewWindowFor(plan, sourcePlan, plan.StageSequence, physicalRenderOptions{}, false); ok {
				t.Fatal("unsupported semantic Group entered the direct-root key frontier")
			}
		})
	}
}

func semanticGroupPreviewTestPlan(policy ir.PhysicalStageGroupMissingKeyPolicy) ir.PhysicalPlan {
	plan := groupRowValuePreviewTestPlan("ALL")
	stage := &plan.StageSequence.Stages[0]
	stage.Group.RowValues = nil
	stage.Group.MissingKeyPolicy = policy
	stage.Group.SemanticKey = &ir.PhysicalStageGroupSemanticKey{
		OutputColumn: "category", TupleVariable: "semantic_tuple", LabelVariable: "semantic_label",
		SourceRootVariable: "root", SourceIdentityColumn: "_key", RootCollectionBindKey: "root_collection",
		ResourceType: "Specimen", CodingPath: "type.coding[]",
		PathSegments: []ir.PhysicalCodedGroupPathSegment{{Name: "type"}, {Name: "coding", Repeated: true}},
		BindingID:    "binding", RuleVersion: "1", FamilyID: "family", System: "urn:test", Version: "v1",
	}
	stage.OutputColumns = []ir.PhysicalStageColumn{
		{ID: "group-key-output", Name: "group_key", Kind: "string", Cardinality: "optional_one"},
		{ID: "category-output", Name: "category", Kind: "string", Cardinality: "optional_one"},
		{ID: "row-count", Name: "source_records", Kind: "integer", Cardinality: "required_one"},
		{ID: "group-identity", Name: "row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
	}
	stage.OutputProjections = []ir.PhysicalProjection{
		{Name: "group_key", Value: ir.PhysicalValue{Variable: "group_key_value"}},
		{Name: "category", Value: ir.PhysicalValue{Variable: "semantic_label"}},
		{Name: "source_records", Value: ir.PhysicalValue{Variable: "source_count"}},
		{Name: "row_id", Hidden: true, Value: ir.PhysicalValue{Variable: "group_identity"}},
	}
	plan.StageSequence.FinalColumns = append([]ir.PhysicalStageColumn(nil), stage.OutputColumns...)
	return plan
}

func groupRowValuePreviewTestPlan(policy string) ir.PhysicalPlan {
	rootValue := func(path ...string) ir.PhysicalValue { return ir.PhysicalValue{Variable: "root", Path: path} }
	inputValue := func(path ...string) ir.PhysicalValue { return ir.PhysicalValue{Variable: "group_input", Path: path} }
	sourceColumns := []ir.PhysicalStageColumn{
		{ID: "group-key", Name: "group_key", Kind: "string", Cardinality: "optional_one"},
		{ID: "row-tags", Name: "row_tags", Kind: "string", Cardinality: "many"},
		{ID: "root-key", Name: "_key", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
	}
	finalColumns := []ir.PhysicalStageColumn{
		{ID: "group-key-output", Name: "group_key", Kind: "string", Cardinality: "optional_one"},
		{ID: "row-count", Name: "source_records", Kind: "integer", Cardinality: "required_one"},
		{ID: "tags-output", Name: "tags", Kind: "string", Cardinality: "many"},
		{ID: "group-identity", Name: "row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
	}
	return ir.PhysicalPlan{
		Version: 1, Engine: ir.PhysicalEngineAQL, Source: ir.PhysicalSource{ResourceType: "Specimen"},
		BindVars: map[string]any{
			"root_collection": "Specimen", "project": "loom", "dataset_generation": "gen-1",
			"scope_allowed": true, "auth_resource_paths": []string{"root/*"}, "auth_resource_paths_unrestricted": false,
			"construction_id": "construction", "preview_limit": 2,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: rootValue("project"), Right: &ir.PhysicalValue{BindKey: "project"},
			}}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: rootValue("dataset_generation"), Right: &ir.PhysicalValue{BindKey: "dataset_generation"},
			}}},
			{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
				Variable: "root_scope_allowed", Operator: "AUTH_RESOURCE_PATH_ALLOWED",
				Inputs: []ir.PhysicalValue{rootValue("auth_resource_path"), {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"}},
			}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root_scope_allowed"}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"},
			}}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
				{Name: "group_key", Value: rootValue("payload", "group_key")},
				{Name: "row_tags", Expression: &ir.PhysicalExpression{
					Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Value: ptrPhysicalValue(rootValue("payload", "values")),
				}},
				{Name: "_key", Hidden: true, Value: rootValue("_key")},
			}}},
		},
		StageSequence: &ir.PhysicalStageSequence{
			SourceStageID: "source", SourceRowIdentity: "_key", FinalStageID: "group", FinalRowIdentity: "row_id",
			SourceColumns: sourceColumns, FinalColumns: finalColumns, PreviewLimitBindKey: "preview_limit",
			Stages: []ir.PhysicalConstructionStage{{
				ID: "group", InputStageID: "source", Kind: ir.PhysicalStageGroupOp,
				InputRowVariable: "group_input", OutputRowVariable: "group_output",
				InputColumns: sourceColumns, OutputColumns: finalColumns, RowIdentityColumn: "row_id",
				InputProjections: []ir.PhysicalProjection{
					{Name: "group_key", Value: inputValue("group_key")},
					{Name: "row_tags", Value: inputValue("row_tags")},
					{Name: "_key", Hidden: true, Value: inputValue("_key")},
				},
				OutputProjections: []ir.PhysicalProjection{
					{Name: "group_key", Value: ir.PhysicalValue{Variable: "group_key_value"}},
					{Name: "source_records", Value: ir.PhysicalValue{Variable: "source_count"}},
					{Name: "tags", Value: ir.PhysicalValue{Variable: "tag_values"}},
					{Name: "row_id", Hidden: true, Value: ir.PhysicalValue{Variable: "group_identity"}},
				},
				Group: &ir.PhysicalStageGroup{
					GroupRowsVariable: "group_rows", IdentityVariable: "group_identity", ConstructionIDBindKey: "construction_id",
					MissingKeyPolicy: ir.PhysicalStageGroupMissingKeyGroup,
					Keys:             []ir.PhysicalStageGroupKey{{InputColumn: "group_key", OutputColumn: "group_key", Variable: "group_key_value", Kind: "STRING"}},
					Aggregates:       []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "source_records", Variable: "source_count", OutputKind: "integer"}},
					RowValues:        []ir.PhysicalStageRowValue{{InputColumn: "row_tags", InputKind: "STRING", InputMany: true, Output: "tags", Policy: policy, Variable: "tag_values"}},
				},
			}},
		},
	}
}

func ptrPhysicalValue(value ir.PhysicalValue) *ir.PhysicalValue { return &value }
