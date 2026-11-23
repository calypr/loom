package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
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

func TestMergeRenderedBindVarsRejectsConflictingSourceScanBindings(t *testing.T) {
	merged := map[string]any{"__loom_physical_projection_0_name": "_key"}
	err := mergeRenderedBindVars(merged, map[string]any{"__loom_physical_projection_0_name": "resource_id"})
	if err == nil {
		t.Fatal("conflicting source-scan projection bindings were merged")
	}
}
