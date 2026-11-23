package aql

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestKeylessGroupFallbackRetainsEmptyInputSentinel(t *testing.T) {
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	stage := ir.PhysicalConstructionStage{
		InputRowVariable:  "input_row",
		OutputRowVariable: "output_row",
		InputProjections: []ir.PhysicalProjection{{
			Name: "tags", Value: ir.PhysicalValue{Variable: "input_row", Path: []string{"tags"}},
		}, {
			Name: "amount", Value: ir.PhysicalValue{Variable: "input_row", Path: []string{"amount"}},
		}},
		OutputProjections: []ir.PhysicalProjection{
			{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
			{Name: "tag", Value: ir.PhysicalValue{Variable: "tag_value"}},
		},
		Group: &ir.PhysicalStageGroup{
			GroupRowsVariable:     "group_rows",
			IdentityVariable:      "row_id",
			ConstructionIDBindKey: "construction_id",
			MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyGroup,
			Aggregates: []ir.PhysicalStageGroupAggregate{{
				Operation: "SUM", InputColumn: "amount", InputKind: "DECIMAL", Output: "rows", Variable: "row_count",
			}},
			RowValues: []ir.PhysicalStageRowValue{{
				InputColumn: "tags", InputKind: "STRING", InputMany: true,
				Output: "tag", Policy: "ONE", Variable: "tag_value",
			}},
		},
	}
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{RowsVariable: "input_rows"})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	for _, want := range []string{
		"LENGTH(__loom_physical_test_construction_group_input_rows) == 0 ? [null]",
		"COLLECT __loom_physical_test_construction_group_all_rows = null INTO group_rows",
		"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("keyless fallback query missing %q:\n%s", want, query)
		}
	}
}

func TestKeylessGroupOneValuesAggregatesZeroRowsThroughSentinel(t *testing.T) {
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	stage := ir.PhysicalConstructionStage{
		InputRowVariable:  "input_row",
		OutputRowVariable: "output_row",
		OutputProjections: []ir.PhysicalProjection{
			{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
			{Name: "tag_value", Value: ir.PhysicalValue{Variable: "tag_value"}},
		},
		Group: &ir.PhysicalStageGroup{
			IdentityVariable:      "row_id",
			ConstructionIDBindKey: "construction_id",
			MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyGroup,
			Aggregates: []ir.PhysicalStageGroupAggregate{{
				Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count",
			}},
			RowValues: []ir.PhysicalStageRowValue{{
				InputColumn: "tags", InputKind: "STRING", InputMany: true,
				Output: "tag_value", Policy: "ONE", Variable: "tag_value",
			}},
		},
	}
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{RowsVariable: "source_rows"})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	for _, want := range []string{
		"FOR input_row IN (LENGTH(source_rows) == 0 ? [null] : source_rows)",
		"AGGREGATE __loom_physical_test_construction_group_count_rows = SUM(input_row != null ? 1 : 0)",
		"ASSERT(LENGTH(__loom_physical_test_construction_group_row_value_unique_0) <= 1, \"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES\")",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("keyless ONE Group query missing %q:\n%s", want, query)
		}
	}
	if strings.Contains(query, " INTO ") {
		t.Fatalf("keyless ONE Group query retained contributor rows:\n%s", query)
	}
}

func TestKeylessGroupAllValuesAggregatesZeroRowsThroughSentinel(t *testing.T) {
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	stage := ir.PhysicalConstructionStage{
		InputRowVariable:  "input_row",
		OutputRowVariable: "output_row",
		OutputProjections: []ir.PhysicalProjection{
			{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
			{Name: "tags", Value: ir.PhysicalValue{Variable: "tag_values"}},
		},
		Group: &ir.PhysicalStageGroup{
			GroupRowsVariable:     "group_rows",
			IdentityVariable:      "row_id",
			ConstructionIDBindKey: "construction_id",
			MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyGroup,
			Aggregates: []ir.PhysicalStageGroupAggregate{{
				Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count",
			}},
			RowValues: []ir.PhysicalStageRowValue{{
				InputColumn: "tags", InputKind: "STRING", InputMany: true,
				Output: "tags", Policy: "ALL", Variable: "tag_values",
			}},
		},
	}
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{RowsVariable: "source_rows"})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	for _, want := range []string{
		"FOR input_row IN (LENGTH(source_rows) == 0 ? [null] : source_rows)",
		"AGGREGATE __loom_physical_test_construction_group_count_rows = SUM(input_row != null ? 1 : 0)",
		"UNIQUE((input_row[@",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("keyless ALL aggregate query missing %q:\n%s", want, query)
		}
	}
	if strings.Contains(query, " INTO ") {
		t.Fatalf("keyless ALL aggregate query retained contributor rows:\n%s", query)
	}
}

func TestKeylessGroupOneValuesAggregatesCurrentSourceScope(t *testing.T) {
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	stage := ir.PhysicalConstructionStage{
		InputRowVariable:  "input_row",
		OutputRowVariable: "output_row",
		OutputProjections: []ir.PhysicalProjection{
			{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
			{Name: "patient_id", Value: ir.PhysicalValue{Variable: "patient_id_value"}},
		},
		Group: &ir.PhysicalStageGroup{
			IdentityVariable:      "row_id",
			ConstructionIDBindKey: "construction_id",
			MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyGroup,
			Aggregates:            []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count"}},
			RowValues: []ir.PhysicalStageRowValue{{
				InputColumn: "patient_id", InputKind: "STRING", Output: "patient_id", Policy: "ONE", Variable: "patient_id_value",
			}},
		},
	}
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{SourceRowInScope: true})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	for _, want := range []string{
		"COLLECT AGGREGATE __loom_physical_test_construction_group_count_rows = SUM(input_row != null ? 1 : 0)",
		"= UNIQUE((input_row[@",
		"ASSERT(LENGTH(__loom_physical_test_construction_group_row_value_unique_0) <= 1, \"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES\")",
		"LENGTH(__loom_physical_test_construction_group_row_value_unique_0) == 0 ? null : FIRST(__loom_physical_test_construction_group_row_value_unique_0)",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("keyless source-scope ONE Group query missing %q:\n%s", want, query)
		}
	}
	if strings.Contains(query, "only keyless COUNT_ROWS groups") || strings.Contains(query, " INTO ") {
		t.Fatalf("keyless source-scope ONE Group query did not aggregate directly:\n%s", query)
	}
}

func TestKeyedGroupWithoutSummariesCollectsKeysWithoutRetainingRows(t *testing.T) {
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	stage := ir.PhysicalConstructionStage{
		InputRowVariable:  "input_row",
		OutputRowVariable: "output_row",
		InputProjections: []ir.PhysicalProjection{{
			Name: "status", Value: ir.PhysicalValue{Variable: "input_row", Path: []string{"status"}},
		}},
		OutputColumns: []ir.PhysicalStageColumn{{ID: "status_group_id", Name: "status_group"}},
		OutputProjections: []ir.PhysicalProjection{
			{Name: "status", Value: ir.PhysicalValue{Variable: "group_key"}},
		},
		Group: &ir.PhysicalStageGroup{
			GroupRowsVariable:          "group_rows",
			DynamicRuntimeKeysVariable: "dynamic_keys",
			IdentityVariable:           "row_id",
			ConstructionIDBindKey:      "construction_id",
			MissingKeyPolicy:           ir.PhysicalStageGroupMissingKeyGroup,
			Keys: []ir.PhysicalStageGroupKey{{
				InputColumn: "status", OutputColumn: "status_group", Kind: "STRING", Variable: "group_key",
			}},
		},
	}
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{RowsVariable: "input_rows"})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	if !strings.Contains(query, "COLLECT group_key = input_row[@") {
		t.Fatalf("keys-only Group did not collect the group key:\n%s", query)
	}
	if !strings.Contains(query, "AGGREGATE dynamic_keys = UNIQUE(input_row != null ? (input_row[\"__loom_dynamic_runtime_keys\"] != null ? input_row[\"__loom_dynamic_runtime_keys\"] : []) : [])") {
		t.Fatalf("keys-only Group did not aggregate the dynamic-key inventory:\n%s", query)
	}
	if strings.Contains(query, " INTO ") || strings.Contains(query, "group_rows") {
		t.Fatalf("keys-only Group retained contributing rows:\n%s", query)
	}
}

func TestKeyedGroupOneRowValueStreamsDistinctValues(t *testing.T) {
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	stage := ir.PhysicalConstructionStage{
		InputRowVariable:  "input_row",
		OutputRowVariable: "output_row",
		OutputColumns:     []ir.PhysicalStageColumn{{ID: "status_group_id", Name: "status_group"}},
		OutputProjections: []ir.PhysicalProjection{
			{Name: "status", Value: ir.PhysicalValue{Variable: "group_key"}},
			{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
			{Name: "tag_value", Value: ir.PhysicalValue{Variable: "tag_value"}},
		},
		Group: &ir.PhysicalStageGroup{
			IdentityVariable:      "row_id",
			ConstructionIDBindKey: "construction_id",
			MissingKeyPolicy:      ir.PhysicalStageGroupMissingKeyGroup,
			Keys: []ir.PhysicalStageGroupKey{{
				InputColumn: "status", OutputColumn: "status_group", Variable: "group_key", Kind: "STRING",
			}},
			Aggregates: []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count"}},
			RowValues: []ir.PhysicalStageRowValue{{
				InputColumn: "tags", InputKind: "STRING", InputMany: true,
				Output: "tag_value", Policy: "ONE", Variable: "tag_value",
			}},
		},
	}
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{RowsVariable: "source_rows"})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	for _, want := range []string{
		"AGGREGATE __loom_physical_test_construction_group_count_rows = SUM(input_row != null ? 1 : 0)",
		"UNIQUE((input_row[@__loom_physical_test_construction_row_value_column]",
		"SORTED_UNIQUE((FOR __loom_physical_test_construction_group_row_value_item_0 IN FLATTEN(",
		"ASSERT(LENGTH(__loom_physical_test_construction_group_row_value_unique_0) <= 1, \"CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES\")",
		"LENGTH(__loom_physical_test_construction_group_row_value_unique_0) == 0 ? null : FIRST(__loom_physical_test_construction_group_row_value_unique_0)",
	} {
		if !strings.Contains(query, want) {
			t.Errorf("streaming ONE Group query missing %q:\n%s", want, query)
		}
	}
	for _, buffered := range []string{"construction_group_input_rows", " INTO __loom_physical_test_construction_group_rows"} {
		if strings.Contains(query, buffered) {
			t.Errorf("streaming ONE Group query retained contributor materialization %q:\n%s", buffered, query)
		}
	}
}

func TestGroupKeyTypeAssertionFollowsCollectBeforeSort(t *testing.T) {
	paths := []struct {
		name string
		set  func(*ir.PhysicalStageGroup, *ir.PhysicalConstructionStage)
	}{
		{
			name: "count-only",
			set: func(group *ir.PhysicalStageGroup, stage *ir.PhysicalConstructionStage) {
				group.Aggregates = []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count"}}
				stage.OutputProjections = append(stage.OutputProjections, ir.PhysicalProjection{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}})
			},
		},
		{
			name: "count-and-all-row-values",
			set: func(group *ir.PhysicalStageGroup, stage *ir.PhysicalConstructionStage) {
				group.Aggregates = []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count"}}
				group.RowValues = []ir.PhysicalStageRowValue{{InputColumn: "tags", InputKind: "STRING", InputMany: true, Output: "tags", Policy: "ALL", Variable: "tag_values"}}
				stage.OutputProjections = append(stage.OutputProjections,
					ir.PhysicalProjection{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
					ir.PhysicalProjection{Name: "tags", Value: ir.PhysicalValue{Variable: "tag_values"}},
				)
			},
		},
		{
			name: "buffered-aggregate",
			set: func(group *ir.PhysicalStageGroup, stage *ir.PhysicalConstructionStage) {
				group.Aggregates = []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_NON_NULL", InputColumn: "amount", InputKind: "STRING", Output: "amount_count", Variable: "amount_count_value"}}
				stage.InputProjections = []ir.PhysicalProjection{
					{Name: "status", Value: ir.PhysicalValue{Variable: "input_row", Path: []string{"status"}}},
					{Name: "amount", Value: ir.PhysicalValue{Variable: "input_row", Path: []string{"amount"}}},
				}
				stage.OutputProjections = append(stage.OutputProjections, ir.PhysicalProjection{Name: "amount_count", Value: ir.PhysicalValue{Variable: "amount_count_value"}})
			},
		},
	}
	policies := []ir.PhysicalStageGroupMissingKeyPolicy{
		ir.PhysicalStageGroupMissingKeyExclude,
		ir.PhysicalStageGroupMissingKeyError,
	}
	for _, path := range paths {
		for _, policy := range policies {
			t.Run(path.name+"/"+string(policy), func(t *testing.T) {
				renderer := &physicalPlanRenderer{renderContext: renderContext{
					bindVars:       map[string]any{"construction_id": "group"},
					reservedVars:   map[string]struct{}{},
					internalPrefix: "test_",
				}}
				stage := ir.PhysicalConstructionStage{
					InputRowVariable:  "input_row",
					OutputRowVariable: "output_row",
					OutputColumns:     []ir.PhysicalStageColumn{{ID: "status_group_id", Name: "status_group"}},
					OutputProjections: []ir.PhysicalProjection{{Name: "status", Value: ir.PhysicalValue{Variable: "group_key"}}},
					Group: &ir.PhysicalStageGroup{
						GroupRowsVariable:     "group_rows",
						IdentityVariable:      "row_id",
						ConstructionIDBindKey: "construction_id",
						MissingKeyPolicy:      policy,
						Keys:                  []ir.PhysicalStageGroupKey{{InputColumn: "status", OutputColumn: "status_group", Variable: "group_key", Kind: "STRING"}},
					},
				}
				path.set(stage.Group, &stage)
				lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{RowsVariable: "source_rows"})
				if err != nil {
					t.Fatal(err)
				}
				query := strings.Join(lines, "\n")
				collectAt := strings.Index(query, "COLLECT group_key = input_row[@")
				assertionAt := strings.Index(query, "FILTER ASSERT(group_key == null OR TYPENAME(group_key) == @__loom_physical_test_construction_group_key_type_0")
				sortAt := strings.Index(query, "SORT group_key ASC")
				if collectAt < 0 || assertionAt <= collectAt || sortAt <= assertionAt {
					t.Fatalf("key type assertion must run after grouping and before sorting:\n%s", query)
				}
				missingKeyFilter := "FILTER input_row[@__loom_physical_test_construction_group_key_column_0] != null"
				if policy == ir.PhysicalStageGroupMissingKeyError {
					missingKeyFilter = "FILTER ASSERT(input_row[@__loom_physical_test_construction_group_key_column_0] != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")"
				}
				missingAt := strings.Index(query, missingKeyFilter)
				if missingAt < 0 || missingAt >= collectAt {
					t.Fatalf("%s missing-key filter must remain before grouping:\n%s", policy, query)
				}
			})
		}
	}
}

func TestCodingIdentityGroupStreamsTupleCountAndNullableDisplayMin(t *testing.T) {
	for _, policy := range []ir.PhysicalStageGroupMissingKeyPolicy{
		ir.PhysicalStageGroupMissingKeyGroup,
		ir.PhysicalStageGroupMissingKeyExclude,
		ir.PhysicalStageGroupMissingKeyError,
	} {
		t.Run(string(policy), func(t *testing.T) {
			renderer := &physicalPlanRenderer{renderContext: renderContext{
				bindVars:       map[string]any{"construction_id": "group"},
				reservedVars:   map[string]struct{}{},
				internalPrefix: "test_",
			}}
			stage := ir.PhysicalConstructionStage{
				InputRowVariable:  "input_row",
				OutputRowVariable: "output_row",
				OutputColumns: []ir.PhysicalStageColumn{
					{ID: "coding_label_id", Name: "coding_label", Kind: "string", Cardinality: "required_one"},
					{ID: "rows_id", Name: "rows", Kind: "integer", Cardinality: "required_one"},
				},
				OutputProjections: []ir.PhysicalProjection{
					{Name: "coding_label", Value: ir.PhysicalValue{Variable: "coding_label_value"}},
					{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
				},
				Group: &ir.PhysicalStageGroup{
					GroupRowsVariable: "group_rows", IdentityVariable: "row_id", ConstructionIDBindKey: "construction_id",
					MissingKeyPolicy: policy,
					CodingKey: &ir.PhysicalStageGroupCodingKey{
						OutputColumn: "coding_label", InputTupleVariable: "coding_input_tuple", TupleVariable: "coding_tuple", LabelVariable: "coding_label_value",
						MembershipColumn: "coding_membership", SourceOccurrenceKeyColumn: "coding_occurrence",
						SourceStageID: "source_projection", SourceShapeID: "group-source-shape-v1:test",
						OwnerOccurrenceID: "root-observation", OwnerResourceType: "Observation", CodingPath: "valueCodeableConcept.coding[]",
					},
					Aggregates: []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count"}},
				},
			}
			lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{SourceRowInScope: true})
			if err != nil {
				t.Fatal(err)
			}
			query := strings.Join(lines, "\n")
			for _, want := range []string{
				"FILTER ASSERT(__loom_physical_test_construction_group_coding_membership.system == null OR TYPENAME(__loom_physical_test_construction_group_coding_membership.system) == \"string\", \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")",
				"FILTER ASSERT(__loom_physical_test_construction_group_coding_membership.version == null OR TYPENAME(__loom_physical_test_construction_group_coding_membership.version) == \"string\", \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")",
				"FILTER ASSERT(__loom_physical_test_construction_group_coding_membership.display == null OR TYPENAME(__loom_physical_test_construction_group_coding_membership.display) == \"string\", \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")",
				"LET coding_input_tuple = [__loom_physical_test_construction_group_coding_membership.system, (TRIM(__loom_physical_test_construction_group_coding_membership.version) != \"\" ? __loom_physical_test_construction_group_coding_membership.version : null)",
				"TRIM(__loom_physical_test_construction_group_coding_membership.code) != \"\" ? __loom_physical_test_construction_group_coding_membership.code : null",
				"COLLECT coding_tuple = coding_input_tuple AGGREGATE __loom_physical_test_construction_group_count_rows = SUM(1), __loom_physical_test_construction_group_coding_min_display = MIN(__loom_physical_test_construction_group_coding_display)",
				"coding_label_value = (__loom_physical_test_construction_group_coding_min_display != null ? __loom_physical_test_construction_group_coding_min_display : coding_tuple[2])",
			} {
				if !strings.Contains(query, want) {
					t.Errorf("Coding identity Group query missing %q:\n%s", want, query)
				}
			}
			switch policy {
			case ir.PhysicalStageGroupMissingKeyGroup:
				if strings.Contains(query, "FILTER ASSERT(coding_input_tuple[2]") || strings.Contains(query, "FILTER coding_input_tuple[2]") {
					t.Fatalf("GROUP missing-key policy unexpectedly filters missing code:\n%s", query)
				}
			case ir.PhysicalStageGroupMissingKeyExclude:
				if !strings.Contains(query, "FILTER coding_input_tuple[2] != null") {
					t.Fatalf("EXCLUDE policy did not filter missing code:\n%s", query)
				}
			case ir.PhysicalStageGroupMissingKeyError:
				if !strings.Contains(query, "FILTER ASSERT(coding_input_tuple[2] != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")") {
					t.Fatalf("ERROR policy did not assert missing code:\n%s", query)
				}
			}
			for _, forbidden := range []string{"DOCUMENT(", "UNNEST", "codingPath", "semantic_family", "UNIQUE(__loom_physical_test_construction_group_coding_display)"} {
				if strings.Contains(query, forbidden) {
					t.Errorf("item-mode Group unexpectedly rescanned source or collected display arrays (%q):\n%s", forbidden, query)
				}
			}
			if !strings.Contains(query, "SUM(1)") || strings.Contains(query, "SORTED_UNIQUE(") {
				t.Fatalf("COUNT_ROWS must count each existing source ordinal without tuple deduplication:\n%s", query)
			}
		})
	}
}

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

func TestStreamingMissingCategoryPivotUsesDirectScalarInputs(t *testing.T) {
	render := func(duplicatePolicy, missingCellPolicy, unlistedPolicy string) (string, physicalPlanRenderer) {
		t.Helper()
		renderer := physicalPlanRenderer{renderContext: renderContext{
			bindVars: map[string]any{
				"construction":   "pivot",
				"limit":          10,
				"category_false": false,
			},
			collectionKeys:                       map[string]struct{}{},
			reservedVars:                         map[string]struct{}{},
			rootVariable:                         "root",
			storedPresenceParentPath:             []string{"payload", "valueQuantity"},
			streamingMissingCategoryPivotPreview: true,
		}}
		pivot := ir.PhysicalGroupedPivot{
			ConstructionID: "pivot", ConstructionIDBindKey: "construction",
			InputRowVariable: "input", GroupRowsVariable: "group_rows", OutputRowVariable: "output_row",
			InputProjections: []ir.PhysicalProjection{
				{Name: "group_key", Expression: directScalarProjection("root", "payload", "valueQuantity", "group_key")},
				{Name: "category_code", Expression: directScalarProjection("root", "payload", "valueQuantity", "category_code")},
				{Name: "measurement", Expression: directScalarProjection("root", "payload", "valueQuantity", "measurement")},
				{
					Name: "category_present", PresenceOutput: true,
					Presence: &ir.PhysicalProjectionPresence{
						Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
						Paths:  [][]string{{"valueQuantity", "category_code"}},
					},
				},
			},
			GroupKeys:      []ir.PhysicalGroupedPivotKey{{Column: "group_key", Variable: "group_value", Kind: "STRING"}},
			CategoryColumn: "category_code", CategoryPresenceColumn: "category_present",
			CategoryPresenceFromInput: true,
			ValueColumn:               "measurement", CategoryType: "BOOLEAN", ValueType: "INTEGER",
			Categories: []ir.PhysicalGroupedPivotCategory{
				{Output: "missing_category", MatchKind: ir.PhysicalPivotCategoryMissingMatch},
				{Output: "null_category", MatchKind: ir.PhysicalPivotCategoryNullMatch},
				{Output: "false_category", MatchKind: ir.PhysicalPivotCategoryValueMatch, ValueBindKey: "category_false", ValueKind: "BOOLEAN"},
			},
			DuplicatePolicy: duplicatePolicy, MissingCellPolicy: missingCellPolicy,
			UnlistedCategoryPolicy: unlistedPolicy, UnlistedEvidenceColumn: "unlisted_count",
		}
		lines, err := renderer.tableShape().renderGroupedTablePivot(pivot, true, "limit")
		if err != nil {
			t.Fatalf("render %s/%s/%s Pivot: %v", duplicatePolicy, missingCellPolicy, unlistedPolicy, err)
		}
		return strings.Join(lines, "\n"), renderer
	}

	query, renderer := render("SUM", "ERROR", "EXCLUDE_WITH_EVIDENCE")

	for _, fragment := range []string{
		"LET __loom_physical_reshape_stream_source_parent = FIRST([root.payload.valueQuantity])",
		"LET __loom_physical_reshape_stream_input_0 = __loom_physical_reshape_stream_source_parent.group_key",
		"LET __loom_physical_reshape_stream_input_1 = __loom_physical_reshape_stream_source_parent.category_code",
		"LET __loom_physical_reshape_stream_input_2 = __loom_physical_reshape_stream_source_parent.measurement",
		"IS_OBJECT(__loom_physical_reshape_stream_source_parent)",
		"HAS(__loom_physical_reshape_stream_source_parent, @__loom_physical_reshape_presence_path)",
		"COLLECT group_value = __loom_physical_reshape_stream_input_0 AGGREGATE",
		"NOT (__loom_physical_reshape_stream_input_3 == true)",
		"(__loom_physical_reshape_stream_input_3 == true AND __loom_physical_reshape_stream_input_1 == null)",
		"TYPENAME(__loom_physical_reshape_stream_input_1)",
		"__loom_physical_reshape_stream_input_1 == @category_false",
		"__loom_physical_reshape_stream_input_2 != null",
		"TYPENAME(__loom_physical_reshape_stream_input_2)",
		"TABLE_PIVOT_VALUE_TYPE_MISMATCH",
		"TABLE_PIVOT_CELL_MISSING",
		"__loom_physical_reshape_stream_category_value_count_0 == 0 ? null : __loom_physical_reshape_stream_category_reduced_0",
		"SORT __loom_physical_reshape_stream_identity ASC",
		"LIMIT @limit",
		"__loom_physical_reshape_stream_unlisted_count",
	} {
		if !strings.Contains(query, fragment) {
			t.Errorf("direct scalar Pivot query is missing %q:\n%s", fragment, query)
		}
	}
	if strings.Contains(query, "LET input = {") || strings.Contains(query, "input[@") {
		t.Fatalf("direct scalar Pivot retained its per-row input object or bound reads:\n%s", query)
	}
	if count := strings.Count(query, "FIRST([root.payload.valueQuantity])"); count != 1 {
		t.Errorf("direct scalar Pivot rendered the shared stored parent %d times, want 1:\n%s", count, query)
	}
	for _, key := range []string{"reshape_category_column", "reshape_value_column", "reshape_category_presence_column", "reshape_group_column_0"} {
		for bindKey := range renderer.bindVars {
			if strings.Contains(bindKey, key) {
				t.Errorf("direct scalar Pivot retained unnecessary column bind %q", bindKey)
			}
		}
	}
	if category, ok := renderer.bindVars["category_false"].(bool); !ok || category {
		t.Errorf("falsey category bind = %#v, want bool(false)", renderer.bindVars["category_false"])
	}
	for _, outputName := range []string{"unlisted_count", "__loom_row_id"} {
		found := false
		for _, value := range renderer.bindVars {
			if value == outputName {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("direct scalar Pivot output projection %q is missing", outputName)
		}
	}

	for _, duplicatePolicy := range []string{"ERROR", "SUM", "MIN", "MAX"} {
		t.Run(duplicatePolicy, func(t *testing.T) {
			policyQuery, _ := render(duplicatePolicy, "NULL", "ERROR")
			reducer := duplicatePolicy
			if reducer == "ERROR" {
				reducer = "MAX"
				if !strings.Contains(policyQuery, "TABLE_PIVOT_CELL_CARDINALITY") {
					t.Errorf("ERROR policy omitted null-inclusive cell cardinality assertion:\n%s", policyQuery)
				}
			}
			if !strings.Contains(policyQuery, fmt.Sprintf("= %s(__loom_physical_reshape_stream_category_value_0)", reducer)) {
				t.Errorf("%s policy did not reduce direct scalar values with %s:\n%s", duplicatePolicy, reducer, policyQuery)
			}
			if !strings.Contains(policyQuery, "TABLE_PIVOT_UNLISTED_CATEGORY") {
				t.Errorf("ERROR unlisted policy omitted its assertion:\n%s", policyQuery)
			}
		})
	}
}

func TestStreamingMissingCategoryPivotFallsBackWithoutDirectScalarProof(t *testing.T) {
	filteredCategory := &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			Selector: spec.Selector{
				Steps:  []spec.SelectorStep{{Field: "category_code"}},
				Filter: &spec.ContainsFilter{Field: "usage", Needle: "pivot"},
			},
			ExecutionMode: ir.PhysicalSelectorGeneric,
		},
	}
	tests := []struct {
		name               string
		rootVariable       string
		groupProjection    ir.PhysicalProjection
		categoryProjection ir.PhysicalProjection
		wantFragment       string
	}{
		{
			name: "missing source-root proof", rootVariable: "",
			groupProjection: ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "group_key"}}},
			wantFragment:    "root.payload.group_key",
		},
		{
			name: "derived group projection", rootVariable: "root",
			groupProjection: ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "derived_group"}},
			wantFragment:    "derived_group",
		},
		{
			name: "related group projection", rootVariable: "root",
			groupProjection: ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "related_group"}},
			wantFragment:    "related_group",
		},
		{
			name: "filtered selector projection", rootVariable: "root",
			groupProjection:    ir.PhysicalProjection{Name: "group_key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "group_key"}}},
			categoryProjection: ir.PhysicalProjection{Name: "category_code", Expression: filteredCategory},
			wantFragment:       "FILTER CONTAINS(__root.usage ? __root.usage : \"\", @__loom_physical_selector_contains)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			categoryProjection := test.categoryProjection
			if categoryProjection.Name == "" {
				categoryProjection = ir.PhysicalProjection{Name: "category_code", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "category_code"}}}
			}
			renderer := physicalPlanRenderer{renderContext: renderContext{
				bindVars:       map[string]any{"construction": "pivot", "limit": 10},
				collectionKeys: map[string]struct{}{}, reservedVars: map[string]struct{}{},
				rootVariable: test.rootVariable, streamingMissingCategoryPivotPreview: true,
			}}
			pivot := ir.PhysicalGroupedPivot{
				ConstructionID: "pivot", ConstructionIDBindKey: "construction",
				InputRowVariable: "input", GroupRowsVariable: "group_rows", OutputRowVariable: "output_row",
				InputProjections: []ir.PhysicalProjection{
					test.groupProjection,
					categoryProjection,
					{Name: "measurement", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "measurement"}}},
					{
						Name: "category_present", PresenceOutput: true,
						Presence: &ir.PhysicalProjectionPresence{
							Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
							Paths:  [][]string{{"category_code"}},
						},
					},
				},
				GroupKeys:      []ir.PhysicalGroupedPivotKey{{Column: "group_key", Variable: "group_value", Kind: "STRING"}},
				CategoryColumn: "category_code", CategoryPresenceColumn: "category_present",
				CategoryPresenceFromInput: true,
				ValueColumn:               "measurement", CategoryType: "STRING", ValueType: "INTEGER",
				Categories:      []ir.PhysicalGroupedPivotCategory{{Output: "missing_category", MatchKind: ir.PhysicalPivotCategoryMissingMatch}},
				DuplicatePolicy: "SUM", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
			}
			scalarLines, scalarInputs, err := renderer.tableShape().renderStreamingPivotDirectScalarInputs(pivot)
			if err != nil {
				t.Fatalf("check direct scalar eligibility: %v", err)
			}
			if len(scalarLines) != 0 || scalarInputs != nil {
				t.Fatalf("ineligible projections selected the direct scalar path: lines=%v inputs=%v", scalarLines, scalarInputs)
			}
			lines, err := renderer.tableShape().renderGroupedTablePivot(pivot, true, "limit")
			if err != nil {
				t.Fatalf("render generic streaming fallback: %v", err)
			}
			query := strings.Join(lines, "\n")
			if !strings.Contains(query, "LET input = {") || !strings.Contains(query, "input[@") {
				t.Fatalf("ineligible projections did not retain generic input-object rendering:\n%s", query)
			}
			if !strings.Contains(query, test.wantFragment) {
				t.Fatalf("generic input rendering lost %q:\n%s", test.wantFragment, query)
			}
		})
	}
}

func directScalarProjection(rootVariable, sourceField string, selectorFields ...string) *ir.PhysicalExpression {
	steps := make([]spec.SelectorStep, 0, len(selectorFields))
	for _, field := range selectorFields {
		steps = append(steps, spec.SelectorStep{Field: field})
	}
	return &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: rootVariable, Path: []string{sourceField}},
			Selector:      spec.Selector{Steps: steps},
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
}

func TestRebaseStreamingPivotExtractToStoredParentRequiresSourcePrefix(t *testing.T) {
	parentPath := []string{"payload", "valueQuantity"}
	valid := &ir.PhysicalExtract{
		Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "code"}}},
		ExecutionMode: ir.PhysicalSelectorDirectScalar,
	}
	if !rebaseStreamingPivotExtractToStoredParent(valid, "root", parentPath, "source_parent") {
		t.Fatal("valid root.payload + valueQuantity.code selector did not rebase to the stored parent")
	}
	if valid.Source.Variable != "source_parent" || len(valid.Source.Path) != 0 ||
		!reflect.DeepEqual(valid.Selector.Steps, []spec.SelectorStep{{Field: "code"}}) {
		t.Fatalf("valid shared-parent selector rebased incorrectly: %+v", valid)
	}

	invalid := &ir.PhysicalExtract{
		Source:        ir.PhysicalValue{Variable: "root", Path: []string{"other"}},
		Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "valueQuantity"}, {Field: "code"}}},
		ExecutionMode: ir.PhysicalSelectorDirectScalar,
	}
	before := *invalid
	before.Source.Path = append([]string(nil), invalid.Source.Path...)
	before.Selector.Steps = append([]spec.SelectorStep(nil), invalid.Selector.Steps...)
	if rebaseStreamingPivotExtractToStoredParent(invalid, "root", parentPath, "source_parent") {
		t.Fatal("mismatched root.other + valueQuantity.code selector was rebased to the stored parent")
	}
	if !reflect.DeepEqual(invalid, &before) {
		t.Fatalf("failed source-prefix proof mutated the selector: got %+v, want unchanged %+v", invalid, before)
	}
}

func TestRelatedOccurrenceGroupStreamsCodingAndPreservesRouteSentinel(t *testing.T) {
	for _, policy := range []ir.PhysicalUnnestEmptyPolicy{
		ir.PhysicalUnnestPreserveParent,
		ir.PhysicalUnnestExclude,
		ir.PhysicalUnnestError,
	} {
		t.Run(string(policy), func(t *testing.T) {
			renderer := &physicalPlanRenderer{renderContext: renderContext{
				bindVars:       map[string]any{"construction_id": "group"},
				reservedVars:   map[string]struct{}{},
				internalPrefix: "test_",
			}}
			stage := relatedOccurrenceGroupStage(policy)
			lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{SourceRowInScope: true})
			if err != nil {
				t.Fatal(err)
			}
			query := strings.Join(lines, "\n")
			for _, want := range []string{
				"FOR __loom_physical_test_construction_group_related_route_result IN (",
				"LENGTH(related_records) == 0 ? [null] : RANGE(0, LENGTH(related_records) - 1)",
				"LET " + stage.Group.RelatedOccurrence.RouteHasItemVariable + " = __loom_physical_test_construction_group_related_index != null",
				"MERGE(input_row,",
				"LET coding_ordinal = __loom_physical_test_unnest_index_0",
				"COLLECT status_key = __loom_physical_test_construction_group_related_input[@",
				"coding_tuple = coding_input_tuple AGGREGATE __loom_physical_test_construction_group_count_rows = SUM(__loom_physical_test_construction_group_related_input != null ? 1 : 0)",
				"UNIQUE((__loom_physical_test_construction_group_related_input[@",
			} {
				if !strings.Contains(query, want) {
					t.Errorf("related Group query missing %q:\n%s", want, query)
				}
			}
			for _, field := range []string{"terminal_id", "item_present", "ordinal", "code", "system", "version", "display"} {
				if !strings.Contains(query, bindReferenceForValue(renderer.bindVars, field)) {
					t.Errorf("related Group query does not emit the %q field bind:\n%s", field, query)
				}
			}
			for _, forbidden := range []string{"construction_group_input_rows", " INTO group_rows"} {
				if strings.Contains(query, forbidden) {
					t.Errorf("streaming related Group query retained %q:\n%s", forbidden, query)
				}
			}
			switch policy {
			case ir.PhysicalUnnestPreserveParent:
				if strings.Contains(query, "FILTER !"+stage.Group.RelatedOccurrence.RouteHasItemVariable) ||
					strings.Contains(query, "FILTER ASSERT(!"+stage.Group.RelatedOccurrence.RouteHasItemVariable) {
					t.Fatalf("GROUP Coding policy unexpectedly filters the preserved route row:\n%s", query)
				}
			case ir.PhysicalUnnestExclude:
				if !strings.Contains(query, "FILTER !"+stage.Group.RelatedOccurrence.RouteHasItemVariable+" OR coding_has_item") {
					t.Fatalf("EXCLUDE did not preserve a missing route while filtering matched empty Coding arrays:\n%s", query)
				}
			case ir.PhysicalUnnestError:
				if !strings.Contains(query, "FILTER ASSERT(!"+stage.Group.RelatedOccurrence.RouteHasItemVariable+" OR coding_has_item,") {
					t.Fatalf("ERROR did not bypass a missing route while rejecting matched empty Coding arrays:\n%s", query)
				}
				if strings.Contains(query, "FILTER ASSERT(LENGTH(__loom_physical_test_unnest_source_0) > 0") {
					t.Fatalf("Coding ERROR policy was applied unconditionally by renderUnnest:\n%s", query)
				}
			}
		})
	}
}

func bindReferenceForValue(bindVars map[string]any, want string) string {
	for key, value := range bindVars {
		if actual, ok := value.(string); ok && actual == want {
			return "@" + key
		}
	}
	return "@<missing-bind-for-" + want + ">"
}

func TestRelatedOccurrenceGroupRequiresExactCodingArrayProjection(t *testing.T) {
	adapter := relatedOccurrenceGroupStage(ir.PhysicalUnnestPreserveParent).Group.RelatedOccurrence
	adapter.CodingUnnest.Expression.Value.Path[0] = "other_codings"
	if err := validateConstructionGroupRelatedOccurrenceRendererShape(adapter); err == nil {
		t.Fatal("renderer accepted a Coding unnest that does not consume the adapter's exact target projection")
	}
}

func TestRelatedGroupAdapterRouteCollectionBindSurvivesRenderedBindPruning(t *testing.T) {
	stage := relatedOccurrenceGroupStage(ir.PhysicalUnnestPreserveParent)
	adapter := stage.Group.RelatedOccurrence
	if err := validateConstructionGroupRelatedOccurrenceRendererShape(adapter); err != nil {
		t.Fatalf("validate related Group adapter: %v", err)
	}
	const edgeCollectionBindKey = "related_expand_0_hop_1_edge_collection"
	const edgeCollectionValue = "subject_Patient_edge_collection"
	adapter.RelatedRecords.RelatedRecords.Operations = []ir.PhysicalOperation{{
		Kind: ir.PhysicalCollectionScanOp,
		CollectionScan: &ir.PhysicalCollectionScan{
			Variable: "related_target", CollectionBindKey: edgeCollectionBindKey,
		},
	}}
	bindVars := map[string]any{edgeCollectionBindKey: edgeCollectionValue}
	plan := ir.PhysicalPlan{
		BindVars:      bindVars,
		StageSequence: &ir.PhysicalStageSequence{Stages: []ir.PhysicalConstructionStage{stage}},
	}
	collectionKeys, err := collectionBindKeys(plan)
	if err != nil {
		t.Fatalf("collect Group adapter collection binds: %v", err)
	}
	if _, ok := collectionKeys[edgeCollectionBindKey]; !ok {
		t.Fatalf("Group adapter route bind %q was omitted from collection keys: %#v", edgeCollectionBindKey, collectionKeys)
	}
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       runtimePhysicalBindVars(bindVars, collectionKeys),
		collectionKeys: collectionKeys,
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{SourceRowInScope: true})
	if err != nil {
		t.Fatalf("render related Group adapter: %v", err)
	}
	query := strings.Join(lines, "\n")
	if !strings.Contains(query, "@@"+edgeCollectionBindKey) {
		t.Fatalf("related Group query omitted its adapter edge collection bind:\n%s", query)
	}
	rendered := RenderedPhysicalPlan{Query: query, BindVars: pruneUnusedRuntimeBindVars(renderer.bindVars, query)}
	collection, ok := rendered.BindVars["@"+edgeCollectionBindKey]
	if !ok || collection != edgeCollectionValue {
		t.Fatalf("related Group rendered bind %q = %#v, present=%v; want adapter collection value %#v; binds=%#v",
			"@"+edgeCollectionBindKey, collection, ok, edgeCollectionValue, rendered.BindVars)
	}
}

func TestRelatedScalarGroupOmitsCodingExpansionAndRetainsNullOrdinalIdentity(t *testing.T) {
	renderer := &physicalPlanRenderer{renderContext: renderContext{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}}
	stage := relatedOccurrenceGroupStage(ir.PhysicalUnnestPreserveParent)
	stage.Group.CodingKey = nil
	stage.Group.RelatedOccurrence.CodingSelector = ""
	stage.Group.RelatedOccurrence.CodingArrayColumn = ""
	stage.Group.RelatedOccurrence.CodingEmptyPolicy = ""
	stage.Group.RelatedOccurrence.OrdinalSemantics = ""
	stage.Group.RelatedOccurrence.CodingUnnest = nil
	stage.Group.RelatedOccurrence.CodingMembershipColumn = ""
	stage.Group.RowValues = nil
	stage.OutputColumns = stage.OutputColumns[:1]
	stage.OutputProjections = stage.OutputProjections[:1]
	lines, err := renderer.renderConstructionGroupStage(stage, constructionGroupInput{SourceRowInScope: true})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Join(lines, "\n")
	if strings.Contains(query, "unnest_") || strings.Contains(query, "coding_membership") {
		t.Fatalf("scalar related Group unexpectedly emitted Coding expansion:\n%s", query)
	}
	for _, name := range []string{"root_id", "terminal_id", "item_present", "ordinal"} {
		if !strings.Contains(query, bindReferenceForValue(renderer.bindVars, name)) {
			t.Errorf("scalar related Group occurrence key omitted %q:\n%s", name, query)
		}
	}
	if !hasBindValue(renderer.bindVars, false) || !hasBindValue(renderer.bindVars, nil) {
		t.Fatalf("scalar related Group occurrence identity did not bind item_present=false and ordinal=null: %#v", renderer.bindVars)
	}
	if strings.Contains(query, "INTO group_rows") {
		t.Fatalf("scalar COUNT_ROWS Group materialized contributor rows:\n%s", query)
	}
}

func hasBindValue(bindVars map[string]any, want any) bool {
	for _, value := range bindVars {
		switch expected := want.(type) {
		case nil:
			if value == nil {
				return true
			}
		case bool:
			if actual, ok := value.(bool); ok && actual == expected {
				return true
			}
		}
	}
	return false
}

func relatedOccurrenceGroupStage(policy ir.PhysicalUnnestEmptyPolicy) ir.PhysicalConstructionStage {
	value := func(variable string, path ...string) ir.PhysicalExpression {
		return ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable, Path: path},
		}
	}
	target := ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
			{Name: "terminal_id", Expression: value("target", "_id")},
			{Name: "status", Expression: value("target", "payload", "status")},
			{Name: "coding_array", Expression: value("target", "payload", "code", "coding")},
		}},
	}
	adapter := &ir.PhysicalStageGroupRelatedOccurrenceAdapter{
		RouteID: "route-1",
		RelatedRecords: ir.PhysicalStageRelatedExpand{
			RouteID: "route-1", Multiplicity: "DISTINCT_TARGET_PER_INPUT",
			AnchorColumnID: "_key", AnchorOccurrenceID: "root", AnchorResourceType: "Observation",
			TargetNodeID: "specimen", TargetResourceType: "Specimen", ParentIdentityColumn: "_key",
			RelatedRecordsVariable: "related_records", IndexVariable: "related_index", ItemVariable: "related_item",
			IdentityVariable: "related_identity", ConstructionIDBindKey: "construction_id",
			EmptyPolicy:    ir.PhysicalUnnestPreserveParent,
			RelatedRecords: ir.PhysicalSubplan{Return: target},
			Route:          []ir.PhysicalStageRelatedRouteStep{{ToOccurrenceID: "specimen-occurrence"}},
		},
		ScalarProjections: []ir.PhysicalStageGroupRelatedProjection{{
			InputColumn: "related_status", ResultField: "status", OwnerResourceType: "Specimen",
			ValueResourceType: "Observation", CandidateID: "status-candidate", CanonicalPath: "status",
		}},
		CodingSelector: "code.coding[]", CodingArrayColumn: "coding_array",
		CodingEmptyPolicy: policy, OrdinalSemantics: constructionGroupRelatedCodingOrdinalSemantics,
		CodingUnnest: &ir.PhysicalUnnest{
			OutputVariable: "coding_item", HasItemVariable: "coding_has_item", Ordinality: "coding_ordinal",
			EmptyPolicy: policy,
			Expression: ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Value:        &ir.PhysicalValue{Variable: "related_item", Path: []string{"coding_array"}},
			},
		},
		CodingMembershipColumn: "coding_membership", OccurrenceKeyColumn: "occurrence_key",
		RouteHasItemVariable: "__loom_group_related_route_has_item",
	}
	return ir.PhysicalConstructionStage{
		ID: "group-1", InputRowVariable: "input_row", OutputRowVariable: "output_row",
		InputColumns: []ir.PhysicalStageColumn{{
			ID: "_key", Name: "_key", Kind: "string", Cardinality: "required_one", Internal: true,
			RootContributorResourceType: "Observation",
		}},
		InputProjections: []ir.PhysicalProjection{
			{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: "input_row", Path: []string{"_key"}}},
			{Name: "related_status", Hidden: true, Value: ir.PhysicalValue{Variable: "related_item", Path: []string{"status"}}},
		},
		OutputColumns: []ir.PhysicalStageColumn{
			{ID: "status-id", Name: "status"}, {ID: "coding-id", Name: "coding"},
		},
		OutputProjections: []ir.PhysicalProjection{
			{Name: "status", Value: ir.PhysicalValue{Variable: "status_key"}},
			{Name: "coding", Value: ir.PhysicalValue{Variable: "coding_label"}},
			{Name: "rows", Value: ir.PhysicalValue{Variable: "row_count"}},
			{Name: "ids", Value: ir.PhysicalValue{Variable: "ids"}},
		},
		Group: &ir.PhysicalStageGroup{
			GroupRowsVariable: "group_rows", IdentityVariable: "row_identity", ConstructionIDBindKey: "construction_id",
			MissingKeyPolicy: ir.PhysicalStageGroupMissingKeyGroup,
			Keys: []ir.PhysicalStageGroupKey{{
				InputColumn: "related_status", OutputColumn: "status", Variable: "status_key", Kind: "STRING",
			}},
			CodingKey: &ir.PhysicalStageGroupCodingKey{
				OutputColumn: "coding", InputTupleVariable: "coding_input_tuple", TupleVariable: "coding_tuple",
				LabelVariable: "coding_label", MembershipColumn: "coding_membership",
				SourceOccurrenceKeyColumn: "occurrence_key",
			},
			RelatedOccurrence: adapter,
			Aggregates:        []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "rows", Variable: "row_count"}},
			RowValues: []ir.PhysicalStageRowValue{{
				InputColumn: "id", InputKind: "STRING", Output: "ids", Policy: "ALL", Variable: "ids",
			}},
		},
	}
}

func TestTwoScanPivotPreviewBindsNonIdentifierGroupColumns(t *testing.T) {
	const groupColumn = "group.key value"
	pivot := ir.PhysicalGroupedPivot{
		ConstructionIDBindKey: "construction_id",
		GroupKeys: []ir.PhysicalGroupedPivotKey{
			{Column: groupColumn, Kind: "STRING"},
			{Column: "another key", Kind: "BOOLEAN"},
		},
	}

	selectionBinds := map[string]any{"construction_id": "pivot-1"}
	selection, err := renderTerminalPivotGroupIdentitySelection(
		"RETURN { value: 1 }\n", pivot, "selected_groups", "key_row", []string{"group_value", "second_value"}, "group_identity",
		selectionBinds, map[string]struct{}{}, "limit",
	)
	if err != nil {
		t.Fatal(err)
	}
	selectionQuery := strings.Join(selection, "\n")
	if !strings.Contains(selectionQuery, "key_row[@__loom_physical_construction_reshape_preview_group_column]") ||
		selectionBinds["__loom_physical_construction_reshape_preview_group_column"] != groupColumn {
		t.Fatalf("first-pass group selection did not bind its arbitrary group column: %s, binds=%#v", selectionQuery, selectionBinds)
	}
	if !strings.Contains(selectionQuery, "RETURN [group_value, second_value]") || !strings.Contains(selectionQuery, "SORT group_identity ASC") {
		t.Fatalf("first pass must sort by canonical identity and return the complete raw multi-key tuple:\n%s", selectionQuery)
	}
}

func TestTwoScanPivotPreviewFiltersDirectSourceGroupTuples(t *testing.T) {
	renderer := physicalPlanRenderer{renderContext: renderContext{bindVars: map[string]any{}, internalPrefix: "preview_"}}
	filter := &pivotGroupTupleFilter{
		SelectedGroupKeysVariable: "selected_groups",
		SourcePaths: [][]string{
			{"payload", "subject", "reference"},
			{"payload", "active"},
		},
	}
	query, err := renderer.renderPivotGroupTupleFilter(filter, "root")
	if err != nil {
		t.Fatal(err)
	}
	if query != "FILTER POSITION(selected_groups, [root.payload.subject.reference, root.payload.active])" || len(renderer.bindVars) != 0 {
		t.Fatalf("source group tuple filter did not render the complete direct tuple: %s, binds=%#v", query, renderer.bindVars)
	}
}

func TestTwoScanPivotPreviewUsesScalarMembershipForOneGroupKey(t *testing.T) {
	renderer := physicalPlanRenderer{renderContext: renderContext{bindVars: map[string]any{}}}
	query, err := renderer.renderPivotGroupTupleFilter(&pivotGroupTupleFilter{
		SelectedGroupKeysVariable: "selected_groups",
		SourcePaths:               [][]string{{"payload", "subject", "reference"}},
	}, "root")
	if err != nil {
		t.Fatal(err)
	}
	if query != "FILTER root.payload.subject.reference IN selected_groups" {
		t.Fatalf("single-key filter = %q, want direct scalar membership", query)
	}
	if _, err := renderer.renderPivotGroupTupleFilter(&pivotGroupTupleFilter{
		SelectedGroupKeysVariable: "selected_groups",
		SourcePaths:               [][]string{{"payload", "bad path"}},
	}, "root"); err == nil {
		t.Fatal("unsafe direct source path was accepted")
	}
}

func TestMergeRenderedBindVarsRejectsConflictingSourceScanBindings(t *testing.T) {
	merged := map[string]any{"__loom_physical_projection_0_name": "_key"}
	err := mergeRenderedBindVars(merged, map[string]any{"__loom_physical_projection_0_name": "resource_id"})
	if err == nil {
		t.Fatal("conflicting source-scan projection bindings were merged")
	}
}
