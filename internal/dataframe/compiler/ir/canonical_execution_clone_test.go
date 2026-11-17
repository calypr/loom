package ir

import (
	"strings"
	"testing"
)

// This serialized counterexample freezes the old canonical bytes in paths the
// operation/stage canonicalizers deliberately do not descend into. The GROUP_ROWS
// Subplan member value is invalid by the current exact member-payload validator;
// the companion payload test documents that boundary.
func TestCanonicalExecutionPhysicalPlanPreservesLegacyOmittedSubplans(t *testing.T) {
	groupSource := PhysicalSource{SemanticNode: "group-member-subplan", ResourceType: "Condition"}
	constructionSource := PhysicalSource{SemanticNode: "cell-trace-construction-subplan", ResourceType: "Observation"}
	stageTraceSource := PhysicalSource{SemanticNode: "stage-cell-trace-subplan", ResourceType: "Specimen"}
	groupSubplan := PhysicalSubplan{Operations: []PhysicalOperation{{Kind: PhysicalCollectionScanOp, Source: groupSource}}}
	constructionSubplan := PhysicalSubplan{Operations: []PhysicalOperation{{Kind: PhysicalCollectionScanOp, Source: constructionSource}}}
	stageTraceSubplan := PhysicalSubplan{Operations: []PhysicalOperation{{Kind: PhysicalCollectionScanOp, Source: stageTraceSource}}}
	plan := PhysicalPlan{
		Version: 1,
		Operations: []PhysicalOperation{
			{Kind: PhysicalGroupRowsOp, GroupRows: &PhysicalGroupRows{MemberValues: []PhysicalGroupMemberValue{{
				Output: "member_value", Expression: PhysicalExpression{Kind: PhysicalSubplanExpression, Subplan: &groupSubplan},
			}}}},
			{Kind: PhysicalCellTraceReturnOp, CellTraceReturn: &PhysicalCellTraceReturn{Construction: &PhysicalCellTraceConstruction{
				RelatedSource: &PhysicalCellTraceRelatedSource{Subplan: constructionSubplan},
			}}},
		},
		StageSequence: &PhysicalStageSequence{CellTraceReturn: &PhysicalCellTraceReturn{
			Value: PhysicalExpression{Kind: PhysicalSubplanExpression, Subplan: &stageTraceSubplan},
		}},
	}

	canonical := CanonicalExecutionPhysicalPlan(plan)
	if got := physicalPlanFingerprint(canonical); got != "4aee7b08c0ed70fc45dc2fc36caa7896cbb858f46c6f397f4d4ff9dd479740be" {
		t.Fatalf("canonical fixture fingerprint = %s; want unchanged legacy fingerprint", got)
	}
	if canonical.Operations[0].GroupRows.MemberValues[0].Expression.Subplan.Operations[0].Source != groupSource ||
		canonical.Operations[1].CellTraceReturn.Construction.RelatedSource.Subplan.Operations[0].Source != constructionSource ||
		canonical.StageSequence.CellTraceReturn.Value.Subplan.Operations[0].Source != stageTraceSource {
		t.Fatal("canonicalization cleared operation Source metadata from a legacy omitted path")
	}
	if plan.Operations[0].GroupRows.MemberValues[0].Expression.Subplan.Operations[0].Source != groupSource ||
		plan.Operations[1].CellTraceReturn.Construction.RelatedSource.Subplan.Operations[0].Source != constructionSource ||
		plan.StageSequence.CellTraceReturn.Value.Subplan.Operations[0].Source != stageTraceSource {
		t.Fatal("canonicalization mutated the input plan")
	}
}

func TestPhysicalExpressionChildrenRequireAValidatedActivePayload(t *testing.T) {
	value := PhysicalValue{Variable: "member"}
	subplan := PhysicalSubplan{Operations: []PhysicalOperation{{Kind: PhysicalCollectionScanOp, Source: PhysicalSource{SemanticNode: "inactive-payload"}}}}
	expression := PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull, Value: &value, Subplan: &subplan}
	if err := validatePhysicalExpression(expression, map[string]bool{"member": true}, nil); err == nil || !strings.Contains(err.Error(), "exactly one payload") {
		t.Fatalf("expression with an inactive Subplan payload validates: %v", err)
	}
	if validCohortMemberValueExpression(PhysicalExpression{Kind: PhysicalSubplanExpression, Subplan: &subplan}, nil) {
		t.Fatal("GROUP_ROWS accepted a member-value Subplan outside its exact member-payload/case contract")
	}
}
