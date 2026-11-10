package postgres

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// ---------------------------------------------------------------------------
// pg-sql-08 offline registration tests: the closed child-set slice, its
// FIRST/ALL/DISTINCT reductions, the hop-only canonical RELATED existence
// shape, and the refusal battery. The set fixtures are plan.Validate-aware
// structural mirrors of lower/child_sets.go buildOptionalChildPhysicalSet.
// The RELATED existence fixtures are the hop-only canonical shape only:
// production related-eligibility lowering prefixes its subplan with an
// anchor operation and attaches the filter at stage level, so no lowered
// plan reaches the hop-only renderer — that production shape's fail-closed
// refusal is pinned here too (TestRelatedExistsAnchorPrefixedProductionShapeStaysRefused),
// and every assertion exercises the renderer gate, never an IR escape.
// ---------------------------------------------------------------------------

const (
	setsPrefix          = "child_set_0"
	setsEdgeVariable    = "child_set_0_edge"
	setsTargetVariable  = "child_set_0_node"
	setsReducedVariable = "child_set_0_reduced"
)

// setsTraversal is the depth-one OUTBOUND hop child_sets.go:17-27 lowers
// for a Patient→Observation has-observation route.
func setsTraversal() ir.PhysicalTraversal {
	return ir.PhysicalTraversal{
		SourceVariable: "root", TargetVariable: setsTargetVariable, EdgeVariable: setsEdgeVariable,
		Direction:             ir.PhysicalOutbound,
		EdgeCollectionBindKey: setsPrefix + "_edge_collection",
		EdgeLabelBindKey:      setsPrefix + "_edge_label",
		TargetTypeBindKey:     setsPrefix + "_target_type",
		EdgeTargetTypeField:   "to_type", Strategy: ir.PhysicalTraversalNative,
	}
}

// setsPlan lays the spine and the child-set bind vars exactly as the
// lowering emits them (@fhir_edge collection, label, target type).
func setsPlan() ir.PhysicalPlan {
	plan := basePlan()
	plan.BindVars[setsPrefix+"_edge_collection"] = "fhir_edge"
	plan.BindVars[setsPrefix+"_edge_label"] = "has-observation"
	plan.BindVars[setsPrefix+"_target_type"] = "Observation"
	return plan
}

// setsProjectionField is one traversal-time selector slot over the child
// document, the form projectPhysicalChildSet declares.
func setsProjectionField(name, property string) ir.PhysicalSetProjectionField {
	return ir.PhysicalSetProjectionField{
		Name: name, ResourceType: "Observation",
		Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: property}}},
		ExecutionMode: ir.PhysicalSelectorDirectScalar,
	}
}

// setsChildSet builds the closed set payload: one capture, one hop, the
// canonical scope block for [edge, target], the child_sets.go:88
// whole-document return, Unique/SortByKey pinned, and a reduction over the
// named slots with the given modes.
func setsChildSet(modes ...ir.PhysicalSetReductionMode) ir.PhysicalSet {
	traversal := setsTraversal()
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}}
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	fields := make([]ir.PhysicalSetProjectionField, 0, len(modes))
	reductionFields := make([]ir.PhysicalSetReductionField, 0, len(modes))
	for index, mode := range modes {
		slot := "__loom_projection_" + strconv.Itoa(index)
		fields = append(fields, setsProjectionField(slot, "status"))
		reductionFields = append(reductionFields, ir.PhysicalSetReductionField{
			Name: "__loom_reduced_" + strconv.Itoa(index), SourceField: slot, Mode: mode,
		})
	}
	return ir.PhysicalSet{
		Variable: setsPrefix,
		Subplan: ir.PhysicalSubplan{
			Captures:   []string{"root"},
			Operations: operations,
			Return: ir.PhysicalExpression{
				Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Value:        &ir.PhysicalValue{Variable: setsTargetVariable},
			},
		},
		Unique:    true,
		SortByKey: true,
		Projection: &ir.PhysicalSetProjection{
			Fields: fields,
		},
		Reduction: &ir.PhysicalSetReduction{
			Variable: setsReducedVariable, SourceSetVariable: setsPrefix, Fields: reductionFields,
		},
	}
}

// setsReturn projects the reduced slots positionally, mirroring the
// projectPhysicalChildSet rewrite (projection.Value = {variable:
// <set>_reduced, path: [name]}).
func setsReturn(names ...string) ir.PhysicalOperation {
	projections := make([]ir.PhysicalProjection, 0, len(names)+1)
	projections = append(projections, ir.PhysicalProjection{
		Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
	for _, name := range names {
		projections = append(projections, ir.PhysicalProjection{
			Name: name, Value: ir.PhysicalValue{Variable: setsReducedVariable, Path: []string{name}}})
	}
	return ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}}
}

func setsPlanWithSet(set ir.PhysicalSet, names ...string) ir.PhysicalPlan {
	plan := setsPlan()
	// Drop the RETURN that basePlan carries, then splice SET before it.
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &set},
		setsReturn(names...))
	return plan
}

func TestSetChildSetReductionsRenderAsCorrelatedScalarSublinks(t *testing.T) {
	set := setsChildSet(ir.PhysicalSetReductionFirst, ir.PhysicalSetReductionAll, ir.PhysicalSetReductionDistinct)
	rendered := renderOK(t, setsPlanWithSet(set,
		"__loom_reduced_0", "__loom_reduced_1", "__loom_reduced_2"))

	sql := rendered.SQL
	// E2 (parent-grain invariance): the set materializes no join; the
	// outer statement is the bare spine plus SELECT-list sublinks.
	for _, forbidden := range []string{"LEFT JOIN", "LATERAL (SELECT"} {
		if strings.Contains(sql, forbidden) {
			t.Fatalf("set must not add outer joins (%q present): %s", forbidden, sql)
		}
	}
	// Each of the three reduction modes appears once per RETURN projection
	// that reads the reduced variable (derived values inline their text;
	// PostgreSQL's planner shares one SubPlan across the equal sublinks,
	// so the repetition is textual, not per-row execution).
	for _, want := range []string{
		`ORDER BY i2."logical_id", s2.ord LIMIT 1`,
		`jsonb_agg(s3.v ORDER BY i3."logical_id", s3.ord)`,
		`DISTINCT ON (i4."logical_id", s4.v)`,
	} {
		if got := strings.Count(sql, want); got != 3 {
			t.Fatalf("occurrences of %q = %d, want 3 (one per reduced-variable projection): %s", want, got, sql)
		}
	}
	// E1 (per-row execution, not a one-shot InitPlan): every item scan is
	// correlated to the outer row identity through the edge anchor.
	if got := strings.Count(sql, `= r0."logical_id"`); got != 9 {
		t.Fatalf("correlated item scans = %d, want 9 (3 fields x 3 readers): %s", got, sql)
	}
	// AQL UNIQUE(set) parity: items are the deduped child documents.
	if got := strings.Count(sql, `SELECT DISTINCT x1."logical_id"`); got != 9 {
		t.Fatalf("item dedup count = %d, want 9: %s", got, sql)
	}
	// The reduced variable is one jsonb_build_object over bound field
	// names; nothing reads the raw set variable. The keys must carry an
	// explicit ::text cast (jsonb_build_object is variadic "any"; untyped
	// parameters fail planning with SQLSTATE 42P18).
	if !strings.Contains(sql, "jsonb_build_object($") || !strings.Contains(sql, "::text, (SELECT") {
		t.Fatalf("reduced object must bind text-cast keys over sublink values: %s", sql)
	}
	// Parameter discipline: labels/types/names bind, no plan data in text.
	for _, leaked := range []string{"has-observation", "Observation", "proj-a", "gen-1", "FHIR/Patient", "__loom_reduced_0"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, sql)
		}
	}
	if !strings.Contains(sql, "$") {
		t.Fatalf("expected bound parameters: %s", sql)
	}
	// Canonical edge-scope conjuncts live inside the sublinks (structural
	// scope.go predicates, D5: the consumed scope block must not widen
	// them away — E4 deny-never-narrowed, offline leg).
	for _, want := range []string{"e1.", "auth_resource_path"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("sublinks must carry the structural edge scope (%q missing): %s", want, sql)
		}
	}

	// Determinism: same plan, byte-identical statement and arguments.
	again := renderOK(t, setsPlanWithSet(set,
		"__loom_reduced_0", "__loom_reduced_1", "__loom_reduced_2"))
	if again.SQL != sql || !reflect.DeepEqual(again.Args, rendered.Args) {
		t.Fatalf("rendering is not deterministic")
	}
	if got, want := len(rendered.OutputColumns), 4; got != want {
		t.Fatalf("output columns = %d, want %d", got, want)
	}
}

// ---------------------------------------------------------------------------
// RELATED existence: the hop-only canonical shape (the canonical {_id}
// return of lower/recipe_construction_related_eligibility.go:208-213 over
// hops that close with their canonical scope blocks) and the refusal of
// the anchor-prefixed shape production actually emits.
// ---------------------------------------------------------------------------

func setsRelatedExistsExpression(negate bool) ir.PhysicalPredicateExpression {
	traversal := setsTraversal()
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}}
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"_id"}},
		},
	}
	exists := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	if !negate {
		return exists
	}
	return ir.PhysicalPredicateExpression{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{exists}}
}

func setsRelatedExistsPlan(negate bool) ir.PhysicalPlan {
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	expression := setsRelatedExistsExpression(negate)
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	return plan
}

func TestRelatedExistsFilterRendersBoundedSemiJoin(t *testing.T) {
	rendered := renderOK(t, setsRelatedExistsPlan(false))
	sql := rendered.SQL
	if !strings.Contains(sql, "AND EXISTS (SELECT 1 FROM ") {
		t.Fatalf("related existence filter must render the bounded semi-join:\n%s", sql)
	}
	if strings.Contains(sql, "LIMIT") {
		t.Fatalf("EXISTS makes the AQL LIMIT 1 witness unnecessary (no LIMIT allowed): %s", sql)
	}
	if !strings.Contains(sql, `= r0."logical_id"`) {
		t.Fatalf("semi-join must correlate on the outer row identity: %s", sql)
	}
	// E4 (deny-never-narrowed, offline leg): the canonical auth predicate
	// survives structurally inside the semi-join for both edge and target.
	for _, want := range []string{`e1."auth_resource_path"`, `x1."auth_resource_path"`} {
		if !strings.Contains(sql, want) {
			t.Fatalf("semi-join must carry the canonical auth scope (%q missing): %s", want, sql)
		}
	}
	// Semi-join never multiplies rows: no lateral child join was added.
	if strings.Contains(sql, "LEFT JOIN") {
		t.Fatalf("semi-join must not add a row-multiplying join: %s", sql)
	}
}

func TestRelatedExistsNotFormIsUnguardedTotalNot(t *testing.T) {
	rendered := renderOK(t, setsRelatedExistsPlan(true))
	sql := rendered.SQL
	if !strings.Contains(sql, "AND NOT (EXISTS (SELECT 1 FROM ") {
		t.Fatalf("registered top-level NOT(EXISTS) form missing:\n%s", sql)
	}
	// E3 (semi-join totality): EXISTS is never SQL NULL, so NOT needs no
	// COALESCE guard — and none may appear (a guard would mask a NULL-
	// producing rewrite of the semi-join).
	if strings.Contains(sql, "COALESCE") {
		t.Fatalf("NOT over the total semi-join must stay unguarded: %s", sql)
	}
}

func TestRequiredShapeAndUnknownReturnsKeepTheTraversalPath(t *testing.T) {
	// The canonical REQUIRED constant-1 return inside NOT stays refused by
	// the legacy composition rule, byte-identically to pre-pg-sql-08.
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.BindVars["required_0_result"] = 1
	traversal := setsTraversal()
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}}
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{BindKey: "required_0_result"},
		},
	}
	exists := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	notExpression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalNotPredicate, Children: []ir.PhysicalPredicateExpression{exists}}
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &notExpression}},
		setsReturn())
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "NOT over the REQUIRED constant-1 EXISTS")

	// An EXISTS return outside both canonical shapes (whole document with
	// no scalar {_id} read) keeps the legacy traversal.go refusal text.
	plan = setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	traversal = setsTraversal()
	bad := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}},
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsTargetVariable},
		},
	}
	expression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &bad}
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	_, err = RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "EXISTS with a non-canonical return")
	if !strings.Contains(err.Error(), "required-match subplan return") {
		t.Fatalf("unknown EXISTS shapes must keep the traversal.go refusal, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Typed subplan FILTER parity: child filters render through the shared
// selector-match contract; values cross only the $n boundary.
// ---------------------------------------------------------------------------

func TestSetSubplanFilterRendersSharedSelectorMatch(t *testing.T) {
	set := setsChildSet(ir.PhysicalSetReductionAll)
	filterValue := "final"
	set.Subplan.Operations = append(set.Subplan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
			Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
				Operator: "EQUALS", ValueKind: spec.FilterString,
				LeftExpression: &ir.PhysicalExpression{
					Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
					NullBehavior: ir.PhysicalEmptyOnNull,
					Extract: &ir.PhysicalExtract{
						Source:        ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"payload"}},
						ResourceType:  "Observation",
						Selector:      spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
						ExecutionMode: ir.PhysicalSelectorDirectScalar,
					},
				},
				Right: &ir.PhysicalValue{BindKey: "child_filter_value"},
			}}}})
	plan := setsPlanWithSet(set, "__loom_reduced_0")
	plan.BindVars["child_filter_value"] = filterValue

	rendered := renderOK(t, plan)
	sql := rendered.SQL
	if !strings.Contains(sql, "x1.") || strings.Contains(sql, filterValue) {
		t.Fatalf("child filter value must bind, not inline: %s", sql)
	}
	// The filter lands inside every item scan (alive control: without it
	// the reduction would aggregate over unfiltered children).
	if got := strings.Count(sql, `x1."payload" #> $`); got < 2 {
		t.Fatalf("child filter must participate in the correlated scan (payload path count %d): %s", got, sql)
	}
	// Alive control: the value crossed the $n boundary (payload
	// comparisons bind JSONB-encoded).
	boundFilter := false
	for _, arg := range rendered.Args {
		if strings.Contains(fmt.Sprintf("%s", arg.Value), strconv.Quote(filterValue)) {
			boundFilter = true
		}
	}
	if !boundFilter {
		t.Fatalf("filter value never reached the bind list: %v", rendered.Args)
	}
}

// ---------------------------------------------------------------------------
// Anchor rules: SET captures may be OPTIONAL rows (parent-preserving empty
// aggregates); EXISTS captures may not (traversal.go:350-354 rule).
// ---------------------------------------------------------------------------

func setsSpineTraversal() ir.PhysicalOperation {
	traversal := setsTraversal()
	return ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}
}

func TestSetCaptureAllowsOptionalAnchorExistanceCaptureRefuses(t *testing.T) {
	// SET anchored on the OPTIONAL spine row: correlation rides the lateral
	// alias, and a NULL-extended parent aggregates over zero child rows.
	optional := setsTraversal()
	optional.TargetVariable = "spine_node"
	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	set := setsChildSet(ir.PhysicalSetReductionFirst)
	set.Subplan.Captures = []string{"spine_node"}
	set.Subplan.Operations[0].Traversal.SourceVariable = "spine_node"
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &optional},
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &set},
		setsReturn("__loom_reduced_0"))
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `= r1."logical_id"`) {
		t.Fatalf("SET sublink must correlate on the OPTIONAL lateral row: %s", rendered.SQL)
	}

	// The same anchor under EXISTS is refused: NULL-extended anchor columns
	// would let the semi-join compare absent rows.
	expression := setsRelatedExistsExpression(false)
	expression.Exists.Captures = []string{"spine_node"}
	expression.Exists.Operations[0].Traversal.SourceVariable = "spine_node"
	plan = setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &optional},
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "EXISTS anchored on an OPTIONAL row")
	if !strings.Contains(err.Error(), "NULL-extended anchor") {
		t.Fatalf("refusal must name the anchor rule, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Refusal battery: every shape outside the registered slice must fail
// closed with a typed capability-gap error (renderer-owned refusals are
// checked as ErrUnsupportedPostgresPlan; shapes the IR gate refuses first
// are accepted at either level, since the plan never reaches rendering).
// ---------------------------------------------------------------------------

func setsBatteryCase(t *testing.T, name, want string, rendererLevel bool, mutate func(*ir.PhysicalPlan)) {
	t.Helper()
	set := setsChildSet(ir.PhysicalSetReductionFirst)
	plan := setsPlanWithSet(set, "__loom_reduced_0")
	mutate(&plan)
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("%s: expected refusal, render succeeded", name)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: error = %v, want substring %q", name, err, want)
	}
	if rendererLevel {
		wantUnsupported(t, err, name)
	}
}

func setsBatterySet(plan *ir.PhysicalPlan) *ir.PhysicalSet {
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalSetOp {
			return plan.Operations[index].Set
		}
	}
	panic("battery plan has no SET operation")
}

func TestSetSurfaceRefusals(t *testing.T) {
	tests := []struct {
		name          string
		want          string
		rendererLevel bool
		mutate        func(*ir.PhysicalPlan)
	}{
		{
			name:          "missing reduction",
			want:          "no reduction contract",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				set.Reduction = nil
				// The reduced variable must leave the RETURN first, or the
				// IR gate refuses the undefined variable before rendering.
				plan.Operations[len(plan.Operations)-1] = setsReturn()
			},
		},
		{
			name: "missing projection",
			want: "reduction requires a selector projection",
			mutate: func(plan *ir.PhysicalPlan) {
				setsBatterySet(plan).Projection = nil
			},
		},
		{
			name:          "unique flag dropped",
			want:          "Unique and SortByKey",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).Unique = false },
		},
		{
			name:          "sort-by-key flag dropped",
			want:          "Unique and SortByKey",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).SortByKey = false },
		},
		{
			name:          "node-path kind",
			want:          "NODE_PATH",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).Kind = ir.PhysicalNodePathSetKind },
		},
		{
			name:          "unknown kind",
			want:          "unknown set kind",
			rendererLevel: true,
			mutate:        func(plan *ir.PhysicalPlan) { setsBatterySet(plan).Kind = "GRAPH_WALK" },
		},
		{
			name:          "shared subset",
			want:          "shared subsets",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				set.SourceSetVariable = "root"
				set.ItemVariable = "item"
				// The IR's shared-subset shape forbids a TRAVERSAL start
				// (it iterates the materialized source set), so rewrite the
				// subplan to that shape and let the renderer own the gap:
				// renderSharedSubset has no registered PostgreSQL contract.
				filter := ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EQUALS",
					Left:     ir.PhysicalValue{Variable: "root", Path: []string{"project"}},
					Right:    &ir.PhysicalValue{BindKey: "project"},
				}}
				set.Subplan.Operations = []ir.PhysicalOperation{
					{Kind: ir.PhysicalFilterOp, Filter: &filter}}
				set.Subplan.Return.Value = &ir.PhysicalValue{Variable: "item"}
			},
		},
		{
			name:          "prepared set",
			want:          "prepared sets are not implemented",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				setsBatterySet(plan).Prepared = &ir.PhysicalPreparedSet{
					Variable: "child_set_0_prepared", SourceSetVariable: setsPrefix,
					Fields: []ir.PhysicalPreparedField{{
						Name: "__loom_prepared_0", ResourceType: "Observation",
						Selector: spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
					}},
				}
			},
		},
		{
			name:          "subplan sort modifier",
			want:          "projection-only modifiers",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				setsBatterySet(plan).Subplan.Sort = &ir.PhysicalValue{
					Variable: setsTargetVariable, Path: []string{"logical_id"}}
			},
		},
		{
			name:          "chained hops in a set",
			want:          "exactly one depth-one traversal hop",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				second := setsTraversal()
				second.SourceVariable = setsTargetVariable
				second.TargetVariable = setsPrefix + "_node2"
				second.EdgeVariable = setsPrefix + "_edge2"
				set.Subplan.Operations = append(set.Subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &second})
			},
		},
		{
			name:          "nested set in a subplan",
			want:          "has kind \"SET\"",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				traversal := setsTraversal()
				set.Subplan.Operations = append(set.Subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{
						Variable: "inner_set",
						Subplan: ir.PhysicalSubplan{
							Captures:   []string{"root"},
							Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}},
							Return: ir.PhysicalExpression{
								Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalObjectCardinality,
								NullBehavior: ir.PhysicalPreserveNull,
								Value:        &ir.PhysicalValue{Variable: setsTargetVariable},
							},
						},
					}})
			},
		},
		{
			name:          "collection scan in a subplan",
			want:          "has kind \"COLLECTION_SCAN\"",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				plan.BindVars["sets_cs_collection"] = "Observation"
				set := setsBatterySet(plan)
				set.Subplan.Operations = append(set.Subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{
						Variable: "cs", CollectionBindKey: "sets_cs_collection",
					}})
			},
		},
		{
			name:          "filter before the hop",
			want:          "may only follow a traversal step",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				filter := ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EQUALS",
					Left:     ir.PhysicalValue{Variable: "root", Path: []string{"project"}},
					Right:    &ir.PhysicalValue{BindKey: "project"},
				}}
				set.Subplan.Operations = append([]ir.PhysicalOperation{
					{Kind: ir.PhysicalFilterOp, Filter: &filter}}, set.Subplan.Operations...)
			},
		},
		{
			name:          "non-canonical scope block",
			want:          "canonical subplan scope block",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				set := setsBatterySet(plan)
				// The first scope leg compares the wrong property.
				set.Subplan.Operations[1].Filter.Predicate.Left.Path = []string{"projekt"}
			},
		},
		{
			name:          "direct read of the set variable",
			want:          "does not bind a readable row",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				ret := plan.Operations[len(plan.Operations)-1].Return
				ret.Projections[1].Value = ir.PhysicalValue{Variable: setsPrefix, Path: []string{"payload"}}
			},
		},
		{
			name:          "set after the execution window",
			want:          "sets must precede the execution window",
			rendererLevel: true,
			mutate: func(plan *ir.PhysicalPlan) {
				sortOp := ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
					Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}},
				}}
				rootScan, setOp, ret := plan.Operations[0], plan.Operations[1], plan.Operations[2]
				plan.Operations = []ir.PhysicalOperation{rootScan, sortOp, setOp, ret}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			setsBatteryCase(t, test.name, test.want, test.rendererLevel, test.mutate)
		})
	}
}

func TestRelatedExistsSurfaceRefusals(t *testing.T) {
	tests := []struct {
		name          string
		want          string
		rendererLevel bool
		mutate        func(*ir.PhysicalSubplan, *ir.PhysicalPlan)
	}{
		{
			// The IR gate already refuses bounded-EXISTS modifiers
			// (physical_validation_shapes.go), matching the AQL renderer's
			// refusal (render/aql/predicates.go:315-317); the renderer's
			// own pin is the second line of defense.
			name: "projection modifiers on an EXISTS subplan",
			want: "projection-only sort or unique modifiers",
			mutate: func(subplan *ir.PhysicalSubplan, _ *ir.PhysicalPlan) {
				subplan.Sort = &ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"_id"}}
				subplan.Unique = true
			},
		},
		{
			name:          "missing scope block on a hop",
			want:          "canonical subplan scope block",
			rendererLevel: true,
			mutate: func(subplan *ir.PhysicalSubplan, _ *ir.PhysicalPlan) {
				// Drop the closing auth equality of the block.
				subplan.Operations = subplan.Operations[:len(subplan.Operations)-1]
			},
		},
		{
			name:          "chained hop without its own block",
			want:          "canonical subplan scope block",
			rendererLevel: true,
			mutate: func(subplan *ir.PhysicalSubplan, _ *ir.PhysicalPlan) {
				second := setsTraversal()
				second.SourceVariable = setsTargetVariable
				second.TargetVariable = setsPrefix + "_node2"
				second.EdgeVariable = setsPrefix + "_edge2"
				subplan.Operations = append(subplan.Operations,
					ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &second})
				// The canonical {_id} return follows the LAST hop's target.
				subplan.Return.Value.Variable = setsPrefix + "_node2"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expression := setsRelatedExistsExpression(false)
			plan := setsPlan()
			plan.Operations = plan.Operations[:len(plan.Operations)-1]
			test.mutate(expression.Exists, &plan)
			plan.Operations = append(plan.Operations,
				ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
				setsReturn())
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

func TestTwoSetsRenderIndependentReducedObjects(t *testing.T) {
	first := setsChildSet(ir.PhysicalSetReductionFirst)
	second := setsChildSet(ir.PhysicalSetReductionDistinct)
	second.Variable = "child_set_1"
	second.Subplan.Captures = []string{"root"}
	second.Reduction.Variable = "child_set_1_reduced"
	second.Reduction.SourceSetVariable = "child_set_1"
	second.Reduction.Fields[0].Name = "__loom_reduced_0"
	second.Projection.Fields[0].Name = "__loom_projection_0"
	second.Projection.Fields[0].Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "code"}}}

	plan := setsPlan()
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &first},
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: &second},
		ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			{Name: "first_status", Value: ir.PhysicalValue{Variable: "child_set_0_reduced", Path: []string{"__loom_reduced_0"}}},
			{Name: "distinct_codes", Value: ir.PhysicalValue{Variable: "child_set_1_reduced", Path: []string{"__loom_reduced_0"}}},
		}}})
	rendered := renderOK(t, plan)
	if got := strings.Count(rendered.SQL, "jsonb_build_object("); got != 2 {
		t.Fatalf("reduced objects = %d, want one per set: %s", got, rendered.SQL)
	}
	// Independent selectors bind their own paths: {status} and {code}.
	bound := map[string]bool{}
	for _, arg := range rendered.Args {
		if text, ok := arg.Value.(string); ok {
			bound[text] = true
		}
	}
	for _, want := range []string{"{status}", "{code}"} {
		if !bound[want] {
			t.Fatalf("selector path %q never bound: %v", want, rendered.Args)
		}
	}
}

// ---------------------------------------------------------------------------
// Production-shape honesty: the eligibility subplans
// lowerConstructionRelatedEligibility actually emits carry an anchor
// operation (COLLECTION_SCAN + variable-right Predicate FILTER, or
// DOCUMENT_LOOKUP) plus the anchor's own canonical scope block before the
// first hop, and their filter attaches only as stage.Filter on a
// PhysicalStageRelatedEligibilityOp (rejected upstream with StageSequence).
// The anchor operations are subplan-argument calls, unregistered this wave,
// so the production shape must fail closed here.
// ---------------------------------------------------------------------------

func setsAnchorPrefixedPlan(t *testing.T, anchorKind string) ir.PhysicalPlan {
	t.Helper()
	traversal := setsTraversal()
	anchorVariable := "related_eligibility_0_anchor"
	operations := make([]ir.PhysicalOperation, 0, 10)
	switch anchorKind {
	case "scan":
		// recipe_construction_related_eligibility.go:95-115: the
		// root/rootContributors anchor is a COLLECTION_SCAN plus a
		// Predicate-form FILTER whose right side is a VARIABLE.
		operations = append(operations,
			ir.PhysicalOperation{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{
				Variable: anchorVariable, CollectionBindKey: "root_collection",
			}},
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS",
				Left:     ir.PhysicalValue{Variable: anchorVariable, Path: []string{"_key"}},
				Right:    &ir.PhysicalValue{Variable: "root", Path: []string{"_key"}},
			}}})
	case "lookup":
		// :116-127: the activeRelatedRecord anchor is a DOCUMENT_LOOKUP.
		operations = append(operations,
			ir.PhysicalOperation{Kind: ir.PhysicalDocumentLookupOp, DocumentLookup: &ir.PhysicalDocumentLookup{
				Variable: anchorVariable, CollectionBindKey: "related_eligibility_anchor_collection",
				ExactID: ir.PhysicalValue{Variable: "root", Path: []string{"focus_id"}},
			}})
	default:
		t.Fatalf("unknown anchor kind %q", anchorKind)
	}
	// :128-130: the anchor variable's own canonical scope block.
	operations = appendScopeBlock(operations, []string{anchorVariable}, "related_eligibility_0_anchor_scope_allowed")
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &traversal})
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsTargetVariable, Path: []string{"_id"}},
		},
	}
	expression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	plan := setsPlan()
	plan.BindVars["related_eligibility_anchor_collection"] = "Observation"
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	return plan
}

func TestRelatedExistsAnchorPrefixedProductionShapeStaysRefused(t *testing.T) {
	for _, anchorKind := range []string{"scan", "lookup"} {
		plan := setsAnchorPrefixedPlan(t, anchorKind)
		_, err := RenderPhysicalPlan(plan, mustBinding(t))
		wantUnsupported(t, err, "anchor-prefixed ("+anchorKind+") eligibility subplan")
		// The refusal must be the subplan-argument gap, not an accidental
		// success further along: the anchor operation is the first thing
		// the loop sees.
		if !strings.Contains(err.Error(), "has kind") {
			t.Fatalf("%s anchor refusal must name the anchor operation kind: %v", anchorKind, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Chained-hop happy path: the multipleTraversals surface (the hop-only
// RELATED existence shape's multi-hop route) has no production caller yet,
// so its second-hop assembly — the edge INNER JOIN carrying the full
// predicate chain in ON, the chained anchor identity equality, the target
// INNER JOIN, and the chained scope-block consumption — is pinned here and
// witnessed live (sets_live_test.go) rather than left provably-refused-only.
// ---------------------------------------------------------------------------

func setsRelatedExistsTwoHopPlan() ir.PhysicalPlan {
	hopOne := setsTraversal()
	hopTwo := setsTraversal()
	hopTwo.SourceVariable = setsTargetVariable
	hopTwo.TargetVariable = setsPrefix + "_node2"
	hopTwo.EdgeVariable = setsPrefix + "_edge2"
	hopTwo.EdgeCollectionBindKey = setsPrefix + "_edge_collection2"
	hopTwo.EdgeLabelBindKey = setsPrefix + "_edge_label2"
	hopTwo.TargetTypeBindKey = setsPrefix + "_target_type2"
	operations := []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &hopOne}}
	operations = appendScopeBlock(operations, []string{setsEdgeVariable, setsTargetVariable}, setsPrefix+"_scope_auth")
	operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &hopTwo})
	operations = appendScopeBlock(operations,
		[]string{setsPrefix + "_edge2", setsPrefix + "_node2"}, setsPrefix+"_scope_auth2")
	subplan := ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: setsPrefix + "_node2", Path: []string{"_id"}},
		},
	}
	expression := ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan}
	plan := setsPlan()
	plan.BindVars[setsPrefix+"_edge_collection2"] = "fhir_edge"
	plan.BindVars[setsPrefix+"_edge_label2"] = "has-specimen"
	plan.BindVars[setsPrefix+"_target_type2"] = "Specimen"
	plan.Operations = plan.Operations[:len(plan.Operations)-1]
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &expression}},
		setsReturn())
	return plan
}

func TestRelatedExistsChainedRouteGoldenAssemblesBothHops(t *testing.T) {
	rendered := renderOK(t, setsRelatedExistsTwoHopPlan())
	sql := rendered.SQL
	// FROM list: the first hop's edge relation carries the correlation in
	// WHERE; every later hop joins its edge in ON with the full predicate
	// chain, mirroring traversal.go's chained assembly.
	for _, want := range []string{
		`FROM "pgir02_it_unit"."fhir_edge_occurrence" AS e1`,
		`INNER JOIN "pgir02_it_unit"."fhir_resource" AS x1 ON`,
		`INNER JOIN "pgir02_it_unit"."fhir_edge_occurrence" AS e2 ON`,
		`INNER JOIN "pgir02_it_unit"."fhir_resource" AS x2 ON`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("chained FROM missing %q:\n%s", want, sql)
		}
	}
	// WHERE conjunction: only the first hop correlates to the outer row.
	if !strings.Contains(sql, `e1."source_logical_id" = r0."logical_id"`) {
		t.Fatalf("first hop must correlate on the outer row: %s", sql)
	}
	// The chained hop anchors on the first hop's reached row, carried in
	// the second edge join's ON clause.
	if !strings.Contains(sql, `e2."source_logical_id" = x1."logical_id"`) {
		t.Fatalf("second hop must anchor on x1 with its full predicate chain: %s", sql)
	}
	// Second-hop discriminators bind their own label/type values.
	if !strings.Contains(sql, `e2."label" = $`) || !strings.Contains(sql, `e2."target_resource_type" = $`) {
		t.Fatalf("second hop must bind its label and reached type: %s", sql)
	}
	// Canonical auth survives structurally on every hop's rows (E4).
	for _, want := range []string{`e1."auth_resource_path"`, `x1."auth_resource_path"`, `e2."auth_resource_path"`, `x2."auth_resource_path"`} {
		if !strings.Contains(sql, want) {
			t.Fatalf("chained semi-join must carry the canonical auth scope (%q missing): %s", want, sql)
		}
	}
	if strings.Contains(sql, "LIMIT") {
		t.Fatalf("EXISTS stays limit-free: %s", sql)
	}
	// Bind-parameter discipline: exactly project, generation, auth array,
	// Patient, label1, Observation, label2, Specimen — with the repeated
	// Patient/Observation discriminators deduplicated by the shared cache.
	if got := len(rendered.Args); got != 8 {
		t.Fatalf("chained parameter count = %d, want 8 (deduped discriminators): %v", got, rendered.Args)
	}
	for _, leaked := range []string{"has-specimen", "Specimen", "has-observation"} {
		if strings.Contains(sql, leaked) {
			t.Fatalf("value %q leaked into statement text: %s", leaked, sql)
		}
	}
	// Determinism: the same plan re-renders byte-identically.
	again := renderOK(t, setsRelatedExistsTwoHopPlan())
	if again.SQL != sql || !reflect.DeepEqual(again.Args, rendered.Args) {
		t.Fatalf("chained rendering is not deterministic")
	}
}
