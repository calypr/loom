package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestKeylessGroupFallbackRetainsEmptyInputSentinel(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}
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
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}
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
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}
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
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}
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

func TestKeyedGroupOneRowValueStreamsDistinctValues(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}
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
				renderer := &physicalPlanRenderer{
					bindVars:       map[string]any{"construction_id": "group"},
					reservedVars:   map[string]struct{}{},
					internalPrefix: "test_",
				}
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
			renderer := &physicalPlanRenderer{
				bindVars:       map[string]any{"construction_id": "group"},
				reservedVars:   map[string]struct{}{},
				internalPrefix: "test_",
			}
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
						OutputColumn: "coding_label", TupleVariable: "coding_tuple", LabelVariable: "coding_label_value",
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
				"LET coding_tuple = [__loom_physical_test_construction_group_coding_membership.system, (TRIM(__loom_physical_test_construction_group_coding_membership.version) != \"\" ? __loom_physical_test_construction_group_coding_membership.version : null)",
				"TRIM(__loom_physical_test_construction_group_coding_membership.code) != \"\" ? __loom_physical_test_construction_group_coding_membership.code : null",
				"COLLECT coding_tuple = coding_tuple AGGREGATE __loom_physical_test_construction_group_count_rows = SUM(1), __loom_physical_test_construction_group_coding_min_display = MIN(__loom_physical_test_construction_group_coding_display)",
				"coding_label_value = (__loom_physical_test_construction_group_coding_min_display != null ? __loom_physical_test_construction_group_coding_min_display : coding_tuple[2])",
			} {
				if !strings.Contains(query, want) {
					t.Errorf("Coding identity Group query missing %q:\n%s", want, query)
				}
			}
			switch policy {
			case ir.PhysicalStageGroupMissingKeyGroup:
				if strings.Contains(query, "FILTER ASSERT(coding_tuple[2]") || strings.Contains(query, "FILTER coding_tuple[2]") {
					t.Fatalf("GROUP missing-key policy unexpectedly filters missing code:\n%s", query)
				}
			case ir.PhysicalStageGroupMissingKeyExclude:
				if !strings.Contains(query, "FILTER coding_tuple[2] != null") {
					t.Fatalf("EXCLUDE policy did not filter missing code:\n%s", query)
				}
			case ir.PhysicalStageGroupMissingKeyError:
				if !strings.Contains(query, "FILTER ASSERT(coding_tuple[2] != null, \"CONSTRUCTION_GROUP_MISSING_KEY\")") {
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
