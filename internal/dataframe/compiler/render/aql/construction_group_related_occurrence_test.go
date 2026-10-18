package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestRelatedOccurrenceGroupStreamsCodingAndPreservesRouteSentinel(t *testing.T) {
	for _, policy := range []ir.PhysicalUnnestEmptyPolicy{
		ir.PhysicalUnnestPreserveParent,
		ir.PhysicalUnnestExclude,
		ir.PhysicalUnnestError,
	} {
		t.Run(string(policy), func(t *testing.T) {
			renderer := &physicalPlanRenderer{
				bindVars:       map[string]any{"construction_id": "group"},
				reservedVars:   map[string]struct{}{},
				internalPrefix: "test_",
			}
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

func TestRelatedScalarGroupOmitsCodingExpansionAndRetainsNullOrdinalIdentity(t *testing.T) {
	renderer := &physicalPlanRenderer{
		bindVars:       map[string]any{"construction_id": "group"},
		reservedVars:   map[string]struct{}{},
		internalPrefix: "test_",
	}
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
