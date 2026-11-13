package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	pgstore "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	objLimitBind   = "obj11_limit"
	objPredicate   = "obj11_predicate_value"
	objSlicePrefix = "child_set_0"
)

func objValueExpr(variable string, path []string, cardinality ir.PhysicalCardinality, behavior ir.PhysicalNullBehavior) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalValueExpression, Cardinality: cardinality, NullBehavior: behavior,
		Value: &ir.PhysicalValue{Variable: variable, Path: path},
	}
}

func objExtractExpr(variable, resourceType string, steps []spec.SelectorStep, cardinality ir.PhysicalCardinality, behavior ir.PhysicalNullBehavior) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: cardinality, NullBehavior: behavior,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: variable, Path: []string{"payload"}},
			ResourceType:  resourceType,
			Selector:      spec.Selector{Steps: steps},
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
}

func objGenderExtract(variable, resourceType string) ir.PhysicalExpression {
	return objExtractExpr(variable, resourceType, []spec.SelectorStep{{Field: "gender"}},
		ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
}

func objObjectExpr(fields ...ir.PhysicalExpressionProjection) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Object:       &ir.PhysicalObject{Fields: fields},
	}
}

func objField(name string, expression ir.PhysicalExpression) ir.PhysicalExpressionProjection {
	return ir.PhysicalExpressionProjection{Name: name, Expression: expression}
}

func objSliceExpr(sourceVariable, limitBind string, projections ...ir.PhysicalExpressionProjection) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind: ir.PhysicalSliceExpression, Cardinality: ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Slice: &ir.PhysicalSlice{
			Source:       ir.PhysicalValue{Variable: sourceVariable},
			Sort:         &ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: sourceVariable, Path: []string{"_key"}}},
			LimitBindKey: limitBind,
			Projections:  projections,
		},
	}
}

func objExpressionProjection(name string, expression ir.PhysicalExpression) ir.PhysicalProjection {
	return ir.PhysicalProjection{Name: name, Expression: &expression}
}

func objPlan() ir.PhysicalPlan {
	plan := setsPlan()
	plan.BindVars[objLimitBind] = 2
	plan.Operations[len(plan.Operations)-1].Return.Projections = []ir.PhysicalProjection{
		{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		objExpressionProjection("envelope", objObjectExpr(
			objField("key", objValueExpr("root", []string{"_key"}, ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)),
			objField("status", objValueExpr("root", []string{"status"}, ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)),
			objField("gender", objGenderExtract("root", "Patient")),
		)),
		objExpressionProjection("items", objSliceExpr("root", objLimitBind,
			objField("value", objExtractExpr("root", "Patient", []spec.SelectorStep{{Field: "active"}},
				ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)),
		)),
	}
	return plan
}

func TestObjectProjectionBindsFieldNamesAsParameters(t *testing.T) {
	rendered := renderOK(t, objPlan())
	sql := rendered.SQL
	if got := strings.Count(sql, "jsonb_build_object($"); got != 4 {
		t.Fatalf("jsonb_build_object pairs = %d, want 4 (three envelope fields + one slice item field): %s", got, sql)
	}
	for _, want := range []string{
		`::text, to_jsonb(r0."logical_id"))`,
		`::text, (r0."payload" #> $`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("object rendering missing %q:\n%s", want, sql)
		}
	}
	bound := map[string]bool{}
	for _, arg := range rendered.Args {
		if text, ok := arg.Value.(string); ok {
			bound[text] = true
		}
	}
	for _, want := range []string{"key", "status", "gender", "value", "{status}", "{gender}", "{active}"} {
		if !bound[want] {
			t.Fatalf("field/path %q never bound: %v", want, rendered.Args)
		}
	}
	hostile := objPlan()
	hostile.Operations[len(hostile.Operations)-1].Return.Projections[1].
		Expression.Object.Fields[0].Name = `evil"; DROP TABLE fhir_resource; --`
	hostileRendered := renderOK(t, hostile)
	if strings.Contains(hostileRendered.SQL, "DROP TABLE") {
		t.Fatalf("hostile field name leaked into SQL text: %s", hostileRendered.SQL)
	}
	hostileBound := false
	for _, arg := range hostileRendered.Args {
		if text, ok := arg.Value.(string); ok && text == `evil"; DROP TABLE fhir_resource; --` {
			hostileBound = true
		}
	}
	if !hostileBound {
		t.Fatalf("hostile field name must cross the $n boundary: %v", hostileRendered.Args)
	}
	again := renderOK(t, objPlan())
	if again.SQL != sql || !reflect.DeepEqual(again.Args, rendered.Args) {
		t.Fatalf("object rendering is not deterministic")
	}
}

func TestObjectOmitFieldDropsBothNullStates(t *testing.T) {
	plan := objPlan()
	envelope := plan.Operations[len(plan.Operations)-1].Return.Projections[1].Expression.Object
	envelope.Fields[1].Expression.NullBehavior = ir.PhysicalOmitNulls
	rendered := renderOK(t, plan)
	sql := rendered.SQL
	if !strings.Contains(sql, `) IS NULL OR (`) ||
		!strings.Contains(sql, `= 'null'::jsonb THEN '{}'::jsonb ELSE jsonb_build_object(`) {
		t.Fatalf("omission CASE missing the both-null-states reading:\n%s", sql)
	}
	if !strings.Contains(sql, `CASE WHEN (r0."payload" #> $`) {
		t.Fatalf("omission CASE must guard the rendered value expression:\n%s", sql)
	}
	if got := strings.Count(sql, "CASE WHEN"); got != 1 {
		t.Fatalf("CASE-wrapped fields = %d, want exactly the one omit field: %s", got, sql)
	}
	if !strings.Contains(sql, "(jsonb_build_object($") {
		t.Fatalf("mixed omit/preserve chain must be parenthesized: %s", sql)
	}
}

func TestSliceSingletonGolden(t *testing.T) {
	rendered := renderOK(t, objPlan())
	sql := rendered.SQL
	if !strings.Contains(sql, `COALESCE(jsonb_agg(sl1."item" ORDER BY sl1."item_sort" ASC NULLS FIRST, sl1."item_key"), '[]'::jsonb) FROM (SELECT`) {
		t.Fatalf("slice sublink spelling wrong:\n%s", sql)
	}
	if !strings.Contains(sql, `ORDER BY r0."logical_id" ASC NULLS FIRST, item_key ASC LIMIT $`) {
		t.Fatalf("slice total-order SORT missing:\n%s", sql)
	}
	limitFound := false
	for _, arg := range rendered.Args {
		if arg.Type == command.PostgreSQLParameterBigint && reflect.DeepEqual(arg.Value, int64(2)) {
			limitFound = true
		}
	}
	if !limitFound {
		t.Fatalf("slice limit must cross as a Bigint int64 parameter: %v", rendered.Args)
	}
	if strings.Contains(sql, "LIMIT 2") {
		t.Fatalf("limit must never be interpolated: %s", sql)
	}
}

func TestSlicePredicateSortAndLimitBind(t *testing.T) {
	plan := objPlan()
	slice := plan.Operations[len(plan.Operations)-1].Return.Projections[2].Expression.Slice
	slice.Predicate = &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
		Operator: "EQUALS", ValueKind: spec.FilterString,
		LeftExpression: &ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{
				Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				ResourceType:  "Patient",
				Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}},
				ExecutionMode: ir.PhysicalSelectorDirectScalar,
			},
		},
		Right: &ir.PhysicalValue{BindKey: objPredicate},
	}}
	slice.Sort = &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			ResourceType:  "Patient",
			Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}},
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
	plan.BindVars[objPredicate] = "female"
	rendered := renderOK(t, plan)
	sql := rendered.SQL
	if !strings.Contains(sql, " WHERE ") || !strings.Contains(sql, `#> $`) {
		t.Fatalf("slice predicate missing:\n%s", sql)
	}
	if strings.Contains(sql, "female") {
		t.Fatalf("predicate value must bind, not inline: %s", sql)
	}
	if !strings.Contains(sql, `ORDER BY (r0."payload" #> $`) {
		t.Fatalf("EXTRACT sort must render over the item row scope:\n%s", sql)
	}
	if got := strings.Count(sql, `r0."payload" #> $`); got < 3 {
		t.Fatalf("predicate + item projection + sort must all read the item document (count %d): %s", got, sql)
	}
}

func objChildSetSlicePlan() ir.PhysicalPlan {
	set := setsChildSet(ir.PhysicalSetReductionAll)
	set.Reduction = nil
	plan := setsPlan()
	plan.BindVars[objLimitBind] = 2
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &set},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			objExpressionProjection("items", objSliceExpr(setsPrefix, objLimitBind,
				objField("value", objExtractExpr(setsPrefix, "Observation", []spec.SelectorStep{{Field: "status"}},
					ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)),
			)),
		}}})
	return plan
}

func TestChildSetSliceSharesTheReductionItemRelation(t *testing.T) {
	reductionRendered := renderOK(t, setsPlanWithSet(setsChildSet(ir.PhysicalSetReductionAll), "__loom_reduced_0"))
	reductionSQL := reductionRendered.SQL
	start := strings.Index(reductionSQL, `(SELECT DISTINCT `)
	if start < 0 {
		t.Fatalf("reduction item relation missing from %s", reductionSQL)
	}
	end := strings.Index(reductionSQL[start:], `) AS i`)
	if end < 0 {
		t.Fatalf("reduction item relation does not close: %s", reductionSQL)
	}
	items := reductionSQL[start : start+end]

	sliceRendered := renderOK(t, objChildSetSlicePlan())
	sql := sliceRendered.SQL
	if !strings.Contains(sql, items+`) AS slx`) {
		t.Fatalf("slice item relation must be byte-identical to the reduction relation\n want substring: %s\n got: %s", items, sql)
	}
	if !strings.Contains(sql, `COALESCE(jsonb_agg(sl`) {
		t.Fatalf("child-set slice must still be a COALESCE'd jsonb_agg: %s", sql)
	}
	for _, forbidden := range []string{"LEFT JOIN", "LATERAL (SELECT"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("slice must not add outer joins (%q present): %s", forbidden, sql)
		}
	}
	if !strings.Contains(sql, `= r0."logical_id"`) {
		t.Fatalf("child-set slice must correlate on the outer row identity: %s", sql)
	}
}

func TestNilReductionWithoutSliceConsumerKeepsTheSetsRefusal(t *testing.T) {
	set := setsChildSet(ir.PhysicalSetReductionAll)
	set.Reduction = nil
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &set},
		setsReturn())
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "nil-Reduction set without a slice consumer")
	want := `postgres renderer: unsupported physical plan: set "child_set_0" has no reduction contract; the registered surface renders sets only through their FIRST/ALL/DISTINCT selector reductions, because no other consumer of the item array is registered`
	if err.Error() != want {
		t.Fatalf("no-consumer refusal drifted:\n got: %s\nwant: %s", err.Error(), want)
	}
}

func TestPreparedBearingSliceStaysRefusedVerbatim(t *testing.T) {
	plan := objPlan()
	r := &planRenderer{
		storage:      mustBinding(t),
		parameters:   map[string]int{},
		rows:         map[string]rowScope{"root": {alias: "r0", resourceType: "Patient"}},
		rootVariable: "root",
	}
	sliceExpression := objSliceExpr("root", objLimitBind,
		objField("value", objExtractExpr("root", "Patient", []spec.SelectorStep{{Field: "active"}},
			ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)))
	sliceExpression.Slice.Projections[0].Expression.Extract.Prepared = &ir.PhysicalPreparedReference{
		SetVariable: "child_set_0_prepared", Field: "__loom_projection_0",
	}
	_, err := r.renderSliceValue(plan, r.rows, sliceExpression, 1)
	wantUnsupported(t, err, "Prepared-bearing slice projection")
	want := `postgres renderer: unsupported physical plan: composite EXTRACT operand: prepared-set selector references are not implemented for PostgreSQL`
	if !strings.HasSuffix(err.Error(), want) {
		t.Fatalf("Prepared refusal drifted from the D3 cell:\n got: %s\nwant suffix: %s", err.Error(), want)
	}
}

func objPreparedReference() *ir.PhysicalPreparedReference {
	return &ir.PhysicalPreparedReference{SetVariable: setsPrefix, Field: "__loom_projection_0"}
}

func objChildSliceOf(plan ir.PhysicalPlan) *ir.PhysicalSlice {
	return plan.Operations[len(plan.Operations)-1].Return.Projections[1].Expression.Slice
}

func obj11SlicePredicate(plan ir.PhysicalPlan) {
	plan.BindVars[objPredicate] = "same"
	objChildSliceOf(plan).Predicate = &ir.PhysicalPredicateExpression{
		Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
			Operator: "EQUALS", ValueKind: spec.FilterString,
			LeftExpression: &ir.PhysicalExpression{
				Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Extract: &ir.PhysicalExtract{
					Source:        ir.PhysicalValue{Variable: setsPrefix, Path: []string{"payload"}},
					ResourceType:  "Observation",
					Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
					ExecutionMode: ir.PhysicalSelectorDirectScalar,
				},
			},
			Right: &ir.PhysicalValue{BindKey: objPredicate},
		}}
}

func TestPreparedResolutionRendersByteIdenticallyToUnannotated(t *testing.T) {
	arms := []struct {
		name     string
		build    func(ir.PhysicalPlan)
		annotate func(ir.PhysicalPlan)
	}{
		{"projection", nil, func(plan ir.PhysicalPlan) {
			objChildSliceOf(plan).Projections[0].Expression.Extract.Prepared = objPreparedReference()
		}},
		{"sort", obj11StatusSort, func(plan ir.PhysicalPlan) {
			objChildSliceOf(plan).Sort.Extract.Prepared = objPreparedReference()
		}},
		{"predicate", obj11SlicePredicate, func(plan ir.PhysicalPlan) {
			objChildSliceOf(plan).Predicate.Comparison.LeftExpression.Extract.Prepared = objPreparedReference()
		}},
		{"bare-row-source", func(plan ir.PhysicalPlan) {
			objChildSliceOf(plan).Projections[0].Expression.Extract.Source.Path = nil
		}, func(plan ir.PhysicalPlan) {
			objChildSliceOf(plan).Projections[0].Expression.Extract.Prepared = objPreparedReference()
		}},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			plain, annotated := objChildSetSlicePlan(), objChildSetSlicePlan()
			if arm.build != nil {
				arm.build(plain)
				arm.build(annotated)
			}
			arm.annotate(annotated)
			plainRendered, annotatedRendered := renderOK(t, plain), renderOK(t, annotated)
			probe := objChildSliceOf(annotated)
			carries := func() bool {
				switch arm.name {
				case "sort":
					return probe.Sort.Extract != nil && probe.Sort.Extract.Prepared != nil
				case "predicate":
					return probe.Predicate != nil && probe.Predicate.Comparison != nil &&
						probe.Predicate.Comparison.LeftExpression != nil &&
						probe.Predicate.Comparison.LeftExpression.Extract != nil &&
						probe.Predicate.Comparison.LeftExpression.Extract.Prepared != nil
				default:
					return probe.Projections[0].Expression.Extract != nil &&
						probe.Projections[0].Expression.Extract.Prepared != nil
				}
			}
			if !carries() {
				t.Fatalf("rendering mutated the plan: %s-arm Extract.Prepared was stripped from the IR", arm.name)
			}
			if annotatedRendered.SQL != plainRendered.SQL {
				t.Fatalf("annotated SQL drifted from the unannotated slot text:\nplain:     %s\nannotated: %s", plainRendered.SQL, annotatedRendered.SQL)
			}
			if !reflect.DeepEqual(annotatedRendered.Args, plainRendered.Args) {
				t.Fatalf("annotated Args drifted from the unannotated slot text:\nplain:     %v\nannotated: %v", plainRendered.Args, annotatedRendered.Args)
			}
			for _, leak := range []string{"__loom_projection", "prepared", "child_set"} {
				if strings.Contains(annotatedRendered.SQL, leak) {
					t.Fatalf("%q leaked into resolved statement text: %s", leak, annotatedRendered.SQL)
				}
			}
		})
	}
}

func TestPreparedRegisterSlotTableRefusals(t *testing.T) {
	const verbatim = `postgres renderer: unsupported physical plan: composite EXTRACT operand: prepared-set selector references are not implemented for PostgreSQL`
	directStatus := spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}}
	iteratingCategory := spec.Selector{Steps: []spec.SelectorStep{{Field: "category", Iterate: true}}}
	operand := func(selector spec.Selector) ir.PhysicalExpression {
		return objExtractExpr(setsPrefix, "Observation", selector.Steps,
			ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	}
	for _, testCase := range []struct {
		name    string
		slot    *ir.PhysicalSetProjectionField
		extract ir.PhysicalExpression
		want    string
	}{
		{
			name:    "referenceNamesAnotherSet",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Observation", Selector: directStatus, ExecutionMode: ir.PhysicalSelectorDirectScalar},
			extract: operand(directStatus),
			want:    verbatim,
		},
		{
			name:    "referenceNamesAnUnregisteredSlot",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Observation", Selector: directStatus, ExecutionMode: ir.PhysicalSelectorDirectScalar},
			extract: operand(directStatus),
			want:    verbatim,
		},
		{
			name:    "slotNamesADifferentSelector",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Observation", Selector: directStatus, ExecutionMode: ir.PhysicalSelectorDirectScalar},
			extract: operand(spec.Selector{Steps: []spec.SelectorStep{{Field: "note"}}}),
			want:    "names a different selector than the operand carries",
		},
		{
			name:    "slotResourceTypeDisagrees",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Patient", Selector: directStatus, ExecutionMode: ir.PhysicalSelectorDirectScalar},
			extract: operand(directStatus),
			want:    "projects resource type \"Patient\" but the operand validates against \"Observation\"",
		},
		{
			name:    "slotSelectorIsNotADirectPath",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Observation", Selector: iteratingCategory, ExecutionMode: ir.PhysicalSelectorDirectScalar},
			extract: operand(iteratingCategory),
			want:    "not a direct property step",
		},
		{
			name:    "slotIsConditionalArray",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Observation", Selector: directStatus, ExecutionMode: ir.PhysicalSelectorConditionalArray},
			extract: operand(directStatus),
			want:    "is CONDITIONAL_ARRAY, whose repeated-step rendering is not registered",
		},
		{
			name:    "slotDemandIsUnregistered",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Observation", Selector: directStatus, ExecutionMode: ir.PhysicalSelectorDirectScalar, Demand: ir.PhysicalSelectorValueDemand("BOGUS")},
			extract: operand(directStatus),
			want:    "carries unsupported value demand \"BOGUS\"",
		},
		{
			name:    "slotModeIsUnknown",
			slot:    &ir.PhysicalSetProjectionField{Name: "__loom_projection_0", ResourceType: "Observation", Selector: directStatus, ExecutionMode: ir.PhysicalSelectorExecutionMode("MYSTERY")},
			extract: operand(directStatus),
			want:    "unknown selector execution mode \"MYSTERY\"",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			extract := testCase.extract
			reference := objPreparedReference()
			switch testCase.name {
			case "referenceNamesAnotherSet":
				reference.SetVariable = "child_set_9"
			case "referenceNamesAnUnregisteredSlot":
				reference.Field = "__loom_projection_7"
			}
			extract.Extract.Prepared = reference
			r := &planRenderer{
				storage:        mustBinding(t),
				parameters:     map[string]int{},
				rows:           map[string]rowScope{"root": {alias: "r0", resourceType: "Patient"}},
				rootVariable:   "root",
				setBodies:      map[string]correlatedSetBody{setsPrefix: {from: "FROM fhir_resource x1", where: `x1.project = $1`, targetAlias: "x1", targetType: "Observation", targetVar: "x1"}},
				setProjections: map[string][]ir.PhysicalSetProjectionField{setsPrefix: {*testCase.slot}},
			}
			plan := setsPlan()
			plan.BindVars[objLimitBind] = 2
			sliceExpression := objSliceExpr(setsPrefix, objLimitBind, objField("value", extract))
			_, err := r.renderSliceValue(plan, r.rows, sliceExpression, 1)
			wantUnsupported(t, err, "Prepared register: "+testCase.name)
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("refusal %q drifted:\n got: %s\nwant substring: %s", testCase.name, err.Error(), testCase.want)
			}
		})
	}
}

func TestPreparedInsideSliceItemObjectKeepsVerbatimRefusal(t *testing.T) {
	plan := objChildSetSlicePlan()
	field := objField("v", objExtractExpr(setsPrefix, "Observation", []spec.SelectorStep{{Field: "status"}},
		ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))
	field.Expression.Extract.Prepared = objPreparedReference()
	objChildSliceOf(plan).Projections[0].Expression = objObjectExpr(field)
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "Prepared inside a slice item OBJECT")
	const verbatim = `postgres renderer: unsupported physical plan: composite EXTRACT operand: prepared-set selector references are not implemented for PostgreSQL`
	if !strings.HasSuffix(err.Error(), verbatim) {
		t.Fatalf("OBJECT-arm Prepared refusal drifted:\n got: %s\nwant suffix: %s", err.Error(), verbatim)
	}
}

func objNestedObjects(depth int) ir.PhysicalExpression {
	expression := objGenderExtract("root", "Patient")
	for level := 0; level < depth; level++ {
		expression = objObjectExpr(objField("level", expression))
	}
	return expression
}

func TestCompositeDepthCapIsDefensive(t *testing.T) {
	plan := objPlan()
	plan.Operations[len(plan.Operations)-1].Return.Projections[1].
		Expression = &ir.PhysicalExpression{}
	*plan.Operations[len(plan.Operations)-1].Return.Projections[1].Expression = objNestedObjects(4)
	renderOK(t, plan)

	deep := objPlan()
	deepExpression := objNestedObjects(5)
	deep.Operations[len(deep.Operations)-1].Return.Projections[1].
		Expression = &deepExpression
	_, err := RenderPhysicalPlan(deep, mustBinding(t))
	wantUnsupported(t, err, "depth-5 composite")
	if !strings.Contains(err.Error(), "nesting exceeds the registered maximum depth 4") {
		t.Fatalf("depth refusal missing the cap sentence: %v", err)
	}
	if !strings.Contains(err.Error(), "unbounded") {
		t.Fatalf("depth refusal must cite the AQL-unbounded contrast: %v", err)
	}
}

func objBatteryReturn(plan *ir.PhysicalPlan) []ir.PhysicalProjection {
	return plan.Operations[len(plan.Operations)-1].Return.Projections
}

func TestCompositeSurfaceRefusals(t *testing.T) {
	tests := []struct {
		name          string
		want          string
		rendererLevel bool
		mutate        func(*ir.PhysicalPlan)
	}{
		{
			name:          "object cardinality dropped",
			want:          "must carry OBJECT cardinality",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Cardinality = ir.PhysicalScalarCardinality
			},
		},
		{
			name:          "slice cardinality dropped",
			want:          "must carry ARRAY cardinality",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[2].Expression.Cardinality = ir.PhysicalScalarCardinality
			},
		},
		{
			name:          "slice null behavior dropped",
			want:          "EMPTY_ON_NULL",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[2].Expression.NullBehavior = ir.PhysicalPreserveNull
			},
		},
		{
			name:          "slice source is a bind value",
			want:          "not a bind value",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				plan.BindVars["obj11_bind_source"] = "root"
				objBatteryReturn(plan)[2].Expression.Slice.Source = ir.PhysicalValue{BindKey: "obj11_bind_source"}
			},
		},
		{
			name:          "slice source carries a document path",
			want:          "must be the row itself",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[2].Expression.Slice.Source.Path = []string{"payload"}
			},
		},
		{
			name:          "slice source is an unregistered variable",
			want:          "not a registered item source",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				traversal := setsTraversal()
				traversal.TargetVariable = "spine_node"
				plan.Operations = append(plan.Operations[:len(plan.Operations)-1],
					ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &traversal},
					plan.Operations[len(plan.Operations)-1])
				objBatteryReturn(plan)[2].Expression.Slice.Source.Variable = "spine_node"
				sliceSort := objBatteryReturn(plan)[2].Expression.Slice.Sort
				sliceSort.Value.Variable = "spine_node"
			},
		},
		{
			name:          "slice without a projection",
			want:          "at least one projection",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[2].Expression.Slice.Projections = nil
			},
		},
		{
			name:          "slice without sort",
			want:          "stable sort",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[2].Expression.Slice.Sort = nil
			},
		},
		{
			name:          "slice VALUE sort off the identity key",
			want:          "identity key",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[2].Expression.Slice.Sort.Value.Path = []string{"gender"}
			},
		},
		{
			name:          "slice sort of a foreign kind",
			want:          "outside the registered forms",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				literal := ir.PhysicalExpression{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: objLimitBind}}
				objBatteryReturn(plan)[2].Expression.Slice.Sort = &literal
			},
		},
		{
			name:          "slice predicate extraction on another row",
			want:          "must read the slice source row",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				traversal := setsTraversal()
				traversal.TargetVariable = "spine_node"
				plan.Operations = append(plan.Operations[:len(plan.Operations)-1],
					ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &traversal},
					plan.Operations[len(plan.Operations)-1])
				predicate := objBatteryReturn(plan)[2].Expression.Slice.Predicate
				predicate.Comparison.LeftExpression.Extract.Source.Variable = "spine_node"
			},
		},
		{
			name:          "slice predicate of a foreign kind",
			want:          "must be a comparison",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				slice := objBatteryReturn(plan)[2].Expression.Slice
				comparison := slice.Predicate.Comparison
				slice.Predicate = &ir.PhysicalPredicateExpression{
					Kind:     ir.PhysicalAllPredicate,
					Children: []ir.PhysicalPredicateExpression{{Kind: ir.PhysicalComparisonPredicate, Comparison: comparison}},
				}
			},
		},
		{
			name:          "slice limit bind zero",
			want:          "positive int",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				plan.BindVars[objLimitBind] = 0
			},
		},
		{
			name:          "composite literal operand",
			want:          "no registered PostgreSQL rendering",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				literal := ir.PhysicalExpression{Kind: ir.PhysicalLiteralExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Literal: &ir.PhysicalLiteral{BindKey: "project"}}
				objBatteryReturn(plan)[1].Expression.Object.Fields[0].Expression = literal
			},
		},
		{
			name:          "composite VALUE operand reads a bind",
			want:          "not a bind value",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields[1].Expression.Value = &ir.PhysicalValue{BindKey: "project"}
			},
		},
		{
			name:          "composite VALUE operand without a path",
			want:          "must name a document path",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields[1].Expression.Value.Path = nil
			},
		},
		{
			name:          "composite extraction over a foreign source path",
			want:          `spelled ["payload"] or as the bare row`,
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields[2].Expression.Extract.Source.Path = []string{"gender"}
			},
		},
		{
			name:          "composite extraction DISTINCT",
			want:          "DISTINCT projection is meaningful only",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields[2].Expression.Extract.Distinct = true
			},
		},
		{
			name:          "composite extraction iterate step",
			want:          "direct scalar selector mode requires",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields[2].Expression.Extract.Selector.Steps[0].Iterate = true
			},
		},
		{
			name:          "composite extraction conditional array mode",
			want:          "conditional array selector mode requires",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields[2].Expression.Extract.ExecutionMode = ir.PhysicalSelectorConditionalArray
			},
		},
		{
			name:          "composite extraction resource-type mismatch",
			want:          "scanned as",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				plan.BindVars["root_collection"] = "Observation"
			},
		},
		{
			name:          "empty object",
			want:          "at least one projection",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields = nil
			},
		},
		{
			name:          "duplicate object field name",
			want:          "empty or duplicated",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object.Fields[1].Name = "key"
			},
		},
		{
			name:          "duplicate slice projection name",
			want:          "empty or duplicated",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				slice := objBatteryReturn(plan)[2].Expression.Slice
				slice.Projections = append(slice.Projections, slice.Projections[0])
			},
		},
		{
			name:          "presence metadata on an OBJECT projection",
			want:          "presence proofs pair with selector extractions only",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Presence = &ir.PhysicalProjectionPresence{
					Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
					Paths:  [][]string{{"gender"}},
				}
			},
		},
		{
			name:          "object expression without payload",
			want:          "exactly one payload",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[1].Expression.Object = nil
			},
		},
		{
			name:          "slice expression without payload",
			want:          "exactly one payload",
			rendererLevel: false,
			mutate: func(plan *ir.PhysicalPlan) {
				objBatteryReturn(plan)[2].Expression.Slice = nil
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := objPlan()
			if test.want == "must read the slice source row" || test.want == "must be a comparison" {
				slice := plan.Operations[len(plan.Operations)-1].Return.Projections[2].Expression.Slice
				slice.Predicate = &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
					Operator: "EQUALS", ValueKind: spec.FilterString,
					LeftExpression: &ir.PhysicalExpression{
						Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
						NullBehavior: ir.PhysicalEmptyOnNull,
						Extract: &ir.PhysicalExtract{
							Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
							ResourceType:  "Patient",
							Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}},
							ExecutionMode: ir.PhysicalSelectorDirectScalar,
						},
					},
					Right: &ir.PhysicalValue{BindKey: objPredicate},
				}}
				plan.BindVars[objPredicate] = "female"
			}
			test.mutate(&plan)
			_, err := RenderPhysicalPlan(plan, mustBinding(t))
			if err == nil {
				t.Fatalf("%s: expected refusal, render succeeded", test.name)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s: error = %v, want substring %q", test.name, err, test.want)
			}
			if test.rendererLevel {
				wantUnsupported(t, err, test.name)
			}
		})
	}
}

func TestClosedSurfacesKeepTheirCompositeReceipts(t *testing.T) {
	plan := basePlan()
	plan.BindVars[objLimitBind] = 2
	plan.Operations[len(plan.Operations)-1].Return.Projections = []ir.PhysicalProjection{
		objExpressionProjection("envelope", objObjectExpr(
			objField("gender", objGenderExtract("root", "Patient")),
		)),
	}
	_, err := RenderSelectorExtraction(plan, mustBinding(t))
	wantUnsupported(t, err, "selector-extraction composite receipt")
	if !strings.Contains(err.Error(), "composites belong to the read spine registration (objects.go)") {
		t.Fatalf("selector-extraction receipt drifted: %v", err)
	}
	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err != nil {
		t.Fatalf("spine must render the composite projection: %v", err)
	}
}

func TestMeltInputKeepsItsCompositeReceipt(t *testing.T) {
	plan := unp15Plan(t, unp15Fixture{Mutate: func(plan *ir.PhysicalPlan) {
		for index := range plan.Operations {
			if plan.Operations[index].Kind == ir.PhysicalUnpivotOp {
				plan.Operations[index].Unpivot.InputProjections[1].Expression = &ir.PhysicalExpression{}
				*plan.Operations[index].Unpivot.InputProjections[1].Expression = objObjectExpr(
					objField("gender", objGenderExtract("root", "Patient")))
				plan.Operations[index].Unpivot.InputProjections[1].Value = ir.PhysicalValue{}
			}
		}
	}})
	_, err := RenderUnpivot(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("melt input composite must be refused")
	}
	if !strings.Contains(err.Error(), "melt inputs are the spine's scalar/selector projections") {
		t.Fatalf("melt-input receipt drifted: %v", err)
	}
	wantUnsupported(t, err, "melt-input composite receipt")
}

func TestMembershipRootRefusesCompositeProjections(t *testing.T) {
	plan := objPlan()
	plan.BindVars["obj11_membership_probe"] = true
	r := &planRenderer{
		storage:    mustBinding(t),
		parameters: map[string]int{},
		rows:       map[string]rowScope{"root": {alias: "r0", resourceType: "Patient"}},
		membership: &PostgresPopulationRootBinding{},
	}
	_, err := r.renderObjectValue(plan, r.rows, *objBatteryReturn(&plan)[1].Expression, 1)
	wantUnsupported(t, err, "OBJECT on a membership root")
	if !strings.Contains(err.Error(), "membership-root routes") {
		t.Fatalf("membership gate drifted: %v", err)
	}
	_, err = r.renderSliceValue(plan, r.rows, *objBatteryReturn(&plan)[2].Expression, 1)
	wantUnsupported(t, err, "SLICE on a membership root")
	if !strings.Contains(err.Error(), "membership-root routes") {
		t.Fatalf("membership gate drifted: %v", err)
	}
}

func TestSpineStatementsStayByteIdentical(t *testing.T) {
	const witnessSQL = `SELECT r0."logical_id" AS "c0", r0."project" AS "c1", (r0."payload" #> $5::text[]) AS "c2" FROM "pgir02_it_unit"."fhir_resource" AS r0 WHERE r0."project" = $1 AND r0."dataset_generation" = $2 AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND r0."resource_type" = $4 ORDER BY r0."resource_seq" ASC`
	plan := basePlan()
	plan.Operations[len(plan.Operations)-1].Return.Projections = []ir.PhysicalProjection{
		{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		{Name: "project", Value: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}},
		objExpressionProjection("gender", objGenderExtract("root", "Patient")),
	}
	rendered := renderOK(t, plan)
	if rendered.SQL != witnessSQL {
		t.Fatalf("spine statement drifted from the f1389b45 witness:\n got: %s\nwant: %s", rendered.SQL, witnessSQL)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "proj-a"},
		{Type: command.PostgreSQLParameterText, Value: "gen-1"},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient","FHIR/Observation"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "{gender}"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("spine arguments drifted from the f1389b45 witness:\n got: %v\nwant: %v", rendered.Args, wantArgs)
	}
}

func obj11RenderOK(t *testing.T, svc *pgir02Service, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	return rendered
}

func obj11ObjectPlan(order []string) ir.PhysicalPlan {
	plan := basePlan()
	fields := make([]ir.PhysicalExpressionProjection, 0, len(order))
	for _, name := range order {
		switch name {
		case "gender":
			fields = append(fields, objField("gender", objGenderExtract("root", "Patient")))
		case "active":
			fields = append(fields, objField("active", objExtractExpr("root", "Patient",
				[]spec.SelectorStep{{Field: "active"}}, ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)))
		case "dropped":
			fields = append(fields, objField("dropped", objExtractExpr("root", "Patient",
				[]spec.SelectorStep{{Field: "gender"}}, ir.PhysicalScalarCardinality, ir.PhysicalOmitNulls)))
		case "kept":
			fields = append(fields, objField("kept", objGenderExtract("root", "Patient")))
		default:
			panic("unknown object field " + name)
		}
	}
	projections := []ir.PhysicalProjection{
		{Name: "pid", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		{Name: "kept", Expression: &ir.PhysicalExpression{}},
	}
	*projections[1].Expression = objGenderExtract("root", "Patient")
	if len(order) > 0 {
		projections = append([]ir.PhysicalProjection{
			objExpressionProjection("envelope", objObjectExpr(fields...)),
		}, projections...)
	}
	plan.Operations[len(plan.Operations)-1].Return.Projections = projections
	return plan
}

func TestObject11LiveObjectNullStatesAndOmission(t *testing.T) {
	svc := newOwnedService(t, 0, "obj11_it")
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-absent", "FHIR/Patient", map[string]any{"note": "x"})
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-null", "FHIR/Patient", map[string]any{"gender": nil})
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-present", "FHIR/Patient", map[string]any{"gender": "female"})

	rendered := obj11RenderOK(t, svc, obj11ObjectPlan([]string{"kept", "dropped"}))
	if strings.Contains(rendered.SQL, "female") || strings.Contains(rendered.SQL, "dropped") {
		t.Fatalf("field names and values must stay below the $n boundary: %s", rendered.SQL)
	}
	rows := svc.runRendered(t, rendered)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3 (parent grain): %v", len(rows), rows)
	}
	byID := map[string][]string{}
	for _, row := range rows {
		byID[row[1]] = row
	}
	if got := byID["p-present"][0]; got != "map[dropped:female kept:female]" {
		t.Fatalf("present envelope = %q", got)
	}
	for _, id := range []string{"p-null", "p-absent"} {
		if got := byID[id][0]; got != "map[kept:<nil>]" {
			t.Fatalf("%s envelope = %q, want the kept key alone with a JSON null", id, got)
		}
	}
	witness := svc.witness(t, `SELECT logical_id,
		((payload #> '{gender}') IS NULL)::text,
		jsonb_typeof(payload #> '{gender}')::text
		FROM "`+svc.schema+`"."fhir_resource"
		WHERE project = 'proj-a' AND dataset_generation = 'gen-1' AND resource_type = 'Patient'
		ORDER BY logical_id`)
	if len(witness) != 3 {
		t.Fatalf("witness rows = %d: %v", len(witness), witness)
	}
	states := map[string][2]string{
		"p-absent":  {"true", "<null>"},
		"p-null":    {"false", "null"},
		"p-present": {"false", "string"},
	}
	for _, row := range witness {
		wantState, ok := states[row[0]]
		if !ok || row[1] != wantState[0] || row[2] != wantState[1] {
			t.Fatalf("witness state for %s = (%s, %s), want %v — the store must keep the two null states distinct", row[0], row[1], row[2], wantState)
		}
		omitted := row[1] == "true" || row[2] == "null"
		hasDropped := strings.Contains(byID[row[0]][0], "dropped:")
		if hasDropped == omitted {
			t.Fatalf("rendered omission disagrees with the SQL witness for %s (dropped key=%v, witness omitted=%v)", row[0], hasDropped, omitted)
		}
	}
	counts := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource"
		WHERE project='proj-a' AND dataset_generation='gen-1' AND logical_id='p-absent'
		  AND jsonb_exists(payload, 'gender')`)
	if len(counts) != 1 || counts[0][0] != "0" {
		t.Fatalf("p-absent must store no gender key: %v", counts)
	}
}

func TestObject11LiveKeyOrderIrrelevance(t *testing.T) {
	svc := newOwnedService(t, 0, "obj11_it")
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-1", "FHIR/Patient",
		map[string]any{"gender": "male", "active": true})

	first := obj11RenderOK(t, svc, obj11ObjectPlan([]string{"gender", "active"}))
	second := obj11RenderOK(t, svc, obj11ObjectPlan([]string{"active", "gender"}))
	if first.SQL == second.SQL {
		t.Fatalf("declaration orders must reach different statement shapes (pair order): %s", first.SQL)
	}
	rowsFirst := svc.runRendered(t, first)
	rowsSecond := svc.runRendered(t, second)
	if len(rowsFirst) != 1 || len(rowsSecond) != 1 {
		t.Fatalf("one row expected: %v / %v", rowsFirst, rowsSecond)
	}
	wantCell := "map[active:true gender:male]"
	if rowsFirst[0][0] != wantCell || rowsSecond[0][0] != wantCell {
		t.Fatalf("decoded object drifted by declaration order:\n first:  %q\n second: %q\n want:   %q",
			rowsFirst[0][0], rowsSecond[0][0], wantCell)
	}
	witness := svc.witness(t, `SELECT jsonb_build_object('active', true, 'gender', 'male'::text)
		UNION ALL SELECT jsonb_build_object('gender', 'male'::text, 'active', true)`)
	if len(witness) != 2 || witness[0][0] != wantCell || witness[1][0] != wantCell {
		t.Fatalf("witness key-order irrelevance failed: %v", witness)
	}
}

func obj11StatusSort(plan ir.PhysicalPlan) {
	slice := plan.Operations[len(plan.Operations)-1].Return.Projections[1].Expression.Slice
	slice.Sort = &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: setsPrefix, Path: []string{"payload"}},
			ResourceType:  "Observation",
			Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
}

func TestObject11LiveChildSetSliceDedupTieBreakLimitAndPredicate(t *testing.T) {
	svc := newOwnedService(t, 0, "obj11_it")
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-1", "FHIR/Patient", map[string]any{})
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-2", "FHIR/Patient", map[string]any{})
	for _, pair := range [][2]string{{"obs-a", "same"}, {"obs-b", "same"}, {"obs-c", "early"}} {
		svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Observation", pair[0], "FHIR/Observation", map[string]any{"status": pair[1]})
	}
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Observation", "obs-blocked", "FHIR/Blocked", map[string]any{"status": "sneaky"})
	insert := func(from, to, sourcePath string) {
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: "proj-a", DatasetGeneration: "gen-1", AuthResourcePath: "FHIR/Patient",
			Label: "has-observation", SourceResourceType: "Patient", SourceLogicalID: from,
			TargetResourceType: "Observation", TargetLogicalID: to, SourcePath: sourcePath, SourceOrdinal: 0,
		})
	}
	insert("p-1", "obs-a", "observation[0]")
	insert("p-1", "obs-b", "observation[0]")
	insert("p-1", "obs-b", "observation[1]")
	insert("p-1", "obs-c", "observation[2]")
	insert("p-1", "obs-blocked", "observation[9]")

	plan := objChildSetSlicePlan()
	plan.BindVars[objLimitBind] = 10
	obj11StatusSort(plan)
	full := svc.runRendered(t, obj11RenderOK(t, svc, plan))
	if len(full) != 2 {
		t.Fatalf("both parents must survive (empty-array grain): %v", full)
	}
	if full[0][0] != "p-1" || full[1][0] != "p-2" {
		t.Fatalf("unexpected row order: %v", full)
	}
	const fullItems = "[map[value:early] map[value:same] map[value:same]]"
	if full[0][1] != fullItems {
		t.Fatalf("items = %q, want %q (two occurrences of obs-b dedup to one; status ASC, logical_id tie-break)", full[0][1], fullItems)
	}
	if full[1][1] != "[]" {
		t.Fatalf("zero-child parent must decode to the empty array, got %q", full[1][1])
	}
	if strings.Contains(full[0][1], "sneaky") {
		t.Fatalf("denied-auth child leaked into the slice: %s", full[0][1])
	}
	witness := svc.witness(t, `SELECT DISTINCT x.logical_id, x.payload #> '{status}'
		FROM "`+svc.schema+`"."fhir_edge_occurrence" e
		INNER JOIN "`+svc.schema+`"."fhir_resource" x
		  ON x.project = 'proj-a' AND x.dataset_generation = 'gen-1'
		     AND x.auth_resource_path IN ('FHIR/Patient','FHIR/Observation')
		     AND x.resource_type = 'Observation' AND x.logical_id = e.target_logical_id
		WHERE e.project = 'proj-a' AND e.dataset_generation = 'gen-1'
		  AND e.auth_resource_path IN ('FHIR/Patient','FHIR/Observation')
		  AND e.label = 'has-observation' AND e.source_resource_type = 'Patient'
		  AND e.source_logical_id = 'p-1' AND e.target_resource_type = 'Observation'
		ORDER BY (x.payload #> '{status}') ASC NULLS FIRST, x.logical_id ASC`)
	if len(witness) != 3 || witness[0][0] != "obs-c" || witness[1][0] != "obs-a" || witness[2][0] != "obs-b" {
		t.Fatalf("witness order drifted: %v", witness)
	}
	if witness[0][1] != "early" || witness[1][1] != "same" || witness[2][1] != "same" {
		t.Fatalf("witness values drifted: %v", witness)
	}

	limited := objChildSetSlicePlan()
	limited.BindVars[objLimitBind] = 2
	obj11StatusSort(limited)
	limitedRows := svc.runRendered(t, obj11RenderOK(t, svc, limited))
	if limitedRows[0][1] != "[map[value:early] map[value:same]]" {
		t.Fatalf("LIMIT 2 must render the witness prefix, got %q", limitedRows[0][1])
	}

	predicated := objChildSetSlicePlan()
	predicated.BindVars[objLimitBind] = 10
	predicated.BindVars[objPredicate] = "same"
	predicated.Operations[len(predicated.Operations)-1].Return.Projections[1].Expression.Slice.Predicate =
		&ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
			Operator: "EQUALS", ValueKind: spec.FilterString,
			LeftExpression: &ir.PhysicalExpression{
				Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
				NullBehavior: ir.PhysicalEmptyOnNull,
				Extract: &ir.PhysicalExtract{
					Source:        ir.PhysicalValue{Variable: setsPrefix, Path: []string{"payload"}},
					ResourceType:  "Observation",
					Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
					ExecutionMode: ir.PhysicalSelectorDirectScalar,
				},
			},
			Right: &ir.PhysicalValue{BindKey: objPredicate},
		}}
	predicatedRows := svc.runRendered(t, obj11RenderOK(t, svc, predicated))
	if predicatedRows[0][1] != "[map[value:same] map[value:same]]" {
		t.Fatalf("predicate must keep exactly the two \"same\" children, got %q", predicatedRows[0][1])
	}
}

func TestObject11LiveSortHomogeneousTypeClasses(t *testing.T) {
	svc := newOwnedService(t, 0, "obj11_it")
	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-1", "FHIR/Patient", map[string]any{})
	for _, fixture := range []struct {
		logicalID string
		payload   map[string]any
	}{
		{"obs-1", map[string]any{"valueInteger": 10}},
		{"obs-2", map[string]any{"valueInteger": 2}},
		{"obs-3", map[string]any{"valueInteger": 1}},
		{"obs-4", map[string]any{}},
	} {
		svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Observation", fixture.logicalID, "FHIR/Observation", fixture.payload)
	}
	for ordinal, target := range []string{"obs-1", "obs-2", "obs-3", "obs-4"} {
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: "proj-a", DatasetGeneration: "gen-1", AuthResourcePath: "FHIR/Patient",
			Label: "has-observation", SourceResourceType: "Patient", SourceLogicalID: "p-1",
			TargetResourceType: "Observation", TargetLogicalID: target, SourcePath: "observation[0]", SourceOrdinal: int64(ordinal),
		})
	}

	sortOn := func(plan ir.PhysicalPlan, property string, steps []spec.SelectorStep) {
		slice := objChildSliceOf(plan)
		slice.Projections[0].Expression = objExtractExpr(setsPrefix, "Observation", steps,
			ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
		slice.Sort = &ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{
				Source:        ir.PhysicalValue{Variable: setsPrefix},
				ResourceType:  "Observation",
				Selector:      spec.Selector{Steps: steps},
				ExecutionMode: ir.PhysicalSelectorDirectScalar,
			},
		}
	}

	numeric := objChildSetSlicePlan()
	numeric.BindVars[objLimitBind] = 10
	sortOn(numeric, "valueInteger", []spec.SelectorStep{{Field: "valueInteger"}})
	byParent := map[string]string{}
	for _, row := range svc.runRendered(t, obj11RenderOK(t, svc, numeric)) {
		byParent[row[0]] = row[1]
	}
	const wantNumeric = "[map[value:<nil>] map[value:1] map[value:2] map[value:10]]"
	if got := byParent["p-1"]; got != wantNumeric {
		t.Fatalf("numeric sort = %q, want %q (numeric order, null first — AQL total order within the class)", got, wantNumeric)
	}

	svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Patient", "p-2", "FHIR/Patient", map[string]any{})
	for _, fixture := range []struct {
		logicalID string
		payload   map[string]any
	}{
		{"bool-1", map[string]any{"valueBoolean": true}},
		{"bool-2", map[string]any{"valueBoolean": false}},
		{"bool-3", map[string]any{}},
	} {
		svc.insertResource(t, svc.schema, "proj-a", "gen-1", "Observation", fixture.logicalID, "FHIR/Observation", fixture.payload)
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: "proj-a", DatasetGeneration: "gen-1", AuthResourcePath: "FHIR/Patient",
			Label: "has-observation", SourceResourceType: "Patient", SourceLogicalID: "p-2",
			TargetResourceType: "Observation", TargetLogicalID: fixture.logicalID, SourcePath: "observation[9]", SourceOrdinal: 9,
		})
	}
	boolean := objChildSetSlicePlan()
	boolean.BindVars[objLimitBind] = 10
	sortOn(boolean, "valueBoolean", []spec.SelectorStep{{Field: "valueBoolean"}})
	byParent = map[string]string{}
	for _, row := range svc.runRendered(t, obj11RenderOK(t, svc, boolean)) {
		byParent[row[0]] = row[1]
	}
	const wantBoolean = "[map[value:<nil>] map[value:false] map[value:true]]"
	if got := byParent["p-2"]; got != wantBoolean {
		t.Fatalf("boolean sort = %q, want %q (missing < false < true — AQL total order within the class)", got, wantBoolean)
	}
}

func TestCanonicalKeyReferencesResolveToTheIdentityColumnOnBothLegs(t *testing.T) {
	plan := basePlan()
	plan.BindVars["id_value"] = "p-1"
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS",
			Left:     ir.PhysicalValue{Variable: "root", Path: []string{"id"}},
			Right:    &ir.PhysicalValue{BindKey: "id_value"},
		}}},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "key", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}})
	rendered := renderOK(t, plan)
	for _, want := range []string{`r0."logical_id" AS "c0"`, `r0."logical_id" AS "c1"`, `r0."logical_id" = $`} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("canonical-key leg lost %q: %s", want, rendered.SQL)
		}
	}
	for _, forbidden := range []string{"_key", "id", "_id"} {
		for _, arg := range rendered.Args {
			if text, ok := arg.Value.(string); ok && text == forbidden {
				t.Fatalf("identity spelling %q reached Args: %v", forbidden, rendered.Args)
			}
		}
	}
}

func obj11SnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var names []string
	for _, query := range []string{
		`SELECT datname FROM pg_database WHERE datname LIKE 'obj11\_it\_%' ESCAPE '\'`,
		`SELECT nspname FROM pg_namespace WHERE nspname LIKE 'obj11\_it\_%' ESCAPE '\'`,
	} {
		rows, err := pool.Query(queryCtx, query)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			names = append(names, name)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return names, nil
}

func obj11RunDeltaAudit(inner func() int) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return inner()
	}
	code := 0
	before, err := obj11SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-11 delta audit: snapshot before run: %v\n", err)
		code = 1
	} else if len(before) != 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-11 delta audit: %d obj11_it_* resources exist before the run: %v\n", len(before), before)
		code = 1
	}
	if innerCode := inner(); innerCode != 0 {
		code = innerCode
	}
	survivors, err := obj11SnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-11 delta audit: survivor scan: %v\n", err)
		code = 1
	} else if len(survivors) != 0 {
		fmt.Fprintf(os.Stderr, "pg-sql-11 delta audit: %d obj11_it_* resources survive: %v\n", len(survivors), survivors)
		code = 1
	}
	return code
}
