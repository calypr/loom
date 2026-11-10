package postgres

// window_test.go witnesses the pg-sql-12 execution-window contract owned by
// window.go: the W1 spine window kept byte-identical to the merged
// pg-sql-02 behavior, the W2 final window over expanded rows keyed by the
// complete typed row identity (never the root ID alone), the full refusal
// surface for every shape outside the closed slice, and — in the
// service-gated section — literal expected pages that repeat identically
// across executions on a real PostgreSQL 18 server. A skipped required live
// case is a failure for this ticket's acceptance; the gate exists only to
// keep unrelated CI lanes green. TestZZNoLeftoverSQL12Resources audits that
// nothing under the ticket-owned verify_sql12_ prefix survives; it is the
// final function in this file, and window_test.go is the last-sorted test
// file in the package, so every live resource this file mints predates the
// audit in execution order (the Go file-order gotcha is a known vacuous-
// audit failure mode and is deliberately avoided).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/source"
	pgstore "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// Contract helpers
// ---------------------------------------------------------------------------

// windowTuple builds the plan SORT keys from the compiler-owned stable row
// identity tuple, exactly as lowering emits them for a row expansion. The
// renderer must accept this tuple verbatim and reject every deviation.
func windowTuple(unnest ir.PhysicalUnnest) []ir.PhysicalValue {
	return ir.PhysicalUnnestSortKeys(unnest)
}

func windowSortOp(keys ...ir.PhysicalValue) ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: keys}}
}

func windowLimitOp(bind string) ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: bind}}
}

func windowReturnOp(projections ...ir.PhysicalProjection) ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}}
}

// windowedUnnestPlan builds [ROOT_SCAN, UNNEST, SORT(tuple), LIMIT, RETURN]
// over one registered expansion with the limit bound to "limit".
func windowedUnnestPlan(unnest ir.PhysicalUnnest, limit int, projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	plan := basePlan()
	plan.BindVars["limit"] = limit
	plan.Operations = []ir.PhysicalOperation{
		plan.Operations[0],
		unnestOp(unnest),
		windowSortOp(windowTuple(unnest)...),
		windowLimitOp("limit"),
		windowReturnOp(projections...),
	}
	return plan
}

// windowOrderByClause extracts the exact ORDER BY term list, with any trailing
// bound LIMIT cut off, so goldens pin the total order itself.
func windowOrderByClause(t *testing.T, sql string) string {
	t.Helper()
	const marker = " ORDER BY "
	start := strings.Index(sql, marker)
	if start < 0 {
		t.Fatalf("statement carries no ORDER BY:\n%s", sql)
	}
	rest := sql[start+len(marker):]
	if limit := strings.Index(rest, " LIMIT $"); limit >= 0 {
		rest = rest[:limit]
	}
	return rest
}

func requireWindowRefusal(t *testing.T, plan ir.PhysicalPlan, context string) {
	t.Helper()
	_, entryErr := RenderUnnest(plan, mustBinding(t))
	if !errors.Is(entryErr, ErrUnsupportedPostgresPlan) {
		t.Fatalf("%s: RenderUnnest error = %v, want ErrUnsupportedPostgresPlan", context, entryErr)
	}
	sawUnnest := false
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			sawUnnest = true
		}
	}
	if !sawUnnest {
		// Without UNNEST the plan belongs to the spine dispatch, which
		// keeps its own merged gates; the entry/identity pair covers only
		// plans this slice registers.
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
			t.Fatalf("%s: spine dispatch accepted a shape that must fail closed", context)
		}
		return
	}
	_, dispatchErr := RenderPhysicalPlan(plan, mustBinding(t))
	if !errors.Is(dispatchErr, ErrUnsupportedPostgresPlan) {
		t.Fatalf("%s: core dispatch error = %v, want ErrUnsupportedPostgresPlan", context, dispatchErr)
	}
	// The full-backstop invariant from pg-sql-09 carries over to the window
	// arms: both entries must refuse with a byte-identical message.
	if entryErr.Error() != dispatchErr.Error() {
		t.Fatalf("%s: dispatch error %q diverged from entry error %q", context, dispatchErr, entryErr)
	}
}

// ---------------------------------------------------------------------------
// W1: the spine window stays byte-identical to the merged pg-sql-02 contract
// ---------------------------------------------------------------------------

func TestWindowSpineSortLimitSQLIsByteIdentical(t *testing.T) {
	plan := basePlan()
	plan.Operations = []ir.PhysicalOperation{
		plan.Operations[0],
		windowSortOp(ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}),
		windowLimitOp("limit"),
		windowReturnOp(
			valueProjection("logical_id", "root", "logical_id"),
			valueProjection("status", "root", "status"),
		),
	}
	rendered := renderOK(t, plan)
	wantSQL := `SELECT r0."logical_id" AS "c0", (r0."payload" #> $6::text[]) AS "c1"` +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4` +
		` ORDER BY r0."logical_id" ASC, r0."resource_seq" ASC LIMIT $5`
	if rendered.SQL != wantSQL {
		t.Fatalf("window.go drifted the spine golden:\n got %s\nwant %s", rendered.SQL, wantSQL)
	}
	if len(rendered.Args) != 6 || rendered.Args[4].Type != command.PostgreSQLParameterBigint || rendered.Args[4].Value != int64(10) {
		t.Fatalf("window args drifted: %#v", rendered.Args)
	}
}

func TestWindowSpineLimitWithoutSortStaysDeterministic(t *testing.T) {
	// The merged spine contract (window.go D1) keeps accepting a bare LIMIT:
	// the renderer-appended resource_seq witness makes the page a strict
	// total order even when the plan sorts nothing.
	plan := basePlan()
	plan.Operations = []ir.PhysicalOperation{
		plan.Operations[0],
		windowLimitOp("limit"),
		windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
	}
	rendered := renderOK(t, plan)
	if got := windowOrderByClause(t, rendered.SQL); got != `r0."resource_seq" ASC` {
		t.Fatalf("bare-LIMIT ORDER BY = %q, want the resource_seq witness", got)
	}
	if !strings.HasSuffix(rendered.SQL, " LIMIT $5") {
		t.Fatalf("bare-LIMIT statement lost its bound limit:\n%s", rendered.SQL)
	}
}

func TestWindowSpineDuplicateSortKeysAreEmittedVerbatim(t *testing.T) {
	// Review-pinned W1 invariant: ir.Validate accepts duplicate SORT keys
	// (each key is validated independently), and the merged pg-sql-02
	// renderer emitted duplicate ORDER BY terms. The spine window must keep
	// that byte-identity: dedup is an expansion-only (W2) behavior, because
	// the renderer witnesses resource_seq/edge_seq sit outside
	// resourceFilterColumns and can never collide with a typed-column plan
	// key when no expansion is present.
	plan := basePlan()
	plan.Operations = []ir.PhysicalOperation{
		plan.Operations[0],
		windowSortOp(
			ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
		),
		windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
	}
	rendered := renderOK(t, plan)
	if got, want := windowOrderByClause(t, rendered.SQL),
		`r0."logical_id" ASC, r0."logical_id" ASC, r0."resource_seq" ASC`; got != want {
		t.Fatalf("duplicate-key spine ORDER BY =\n got %q\nwant %q", got, want)
	}
}

func TestWindowEdgeVariableRebindIsRefusedAtRegistration(t *testing.T) {
	// The window's edge-join registration refuses re-binding one variable
	// to a second join: the occurrence witness must name exactly one join
	// per edge variable, or it would silently point at the later duplicate
	// join. At the plan surface, ir.Validate already rejects duplicate
	// variable definitions (verified against the merged pg-sql-02/09
	// renderer: "validate physical plan: operation N: variable %q is
	// already defined"), so both merged contexts below were refused before
	// this PR as well — the arm narrows nothing merged; it pins the
	// registration invariant directly inside the renderer.
	t.Run("plan surface refuses duplicate edge variable in both contexts", func(t *testing.T) {
		spine := basePlan()
		spine.BindVars["edge_collection"] = "fhir_edge"
		spine.BindVars["edge_label"] = "has-specimen"
		spine.BindVars["target_type"] = "Specimen"
		traversal := func(target string) ir.PhysicalOperation {
			return ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
				SourceVariable: "root", TargetVariable: target, EdgeVariable: "edge",
				Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
				EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
				EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
			}}
		}
		spine.Operations = []ir.PhysicalOperation{
			spine.Operations[0], traversal("target_a"), traversal("target_b"),
			windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
		}
		requireRebindRefusal(t, spine, `variable "edge" is already defined`)

		routeTraversal := func(occurrence, target string) ir.PhysicalUnnestRouteStep {
			return ir.PhysicalUnnestRouteStep{OccurrenceID: occurrence, Traversal: ir.PhysicalTraversal{
				SourceVariable: "root", TargetVariable: target, EdgeVariable: "obs_edge",
				Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
				EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
				EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
			}}
		}
		routed := func(occurrence, owner, output, ordinality, hasItem, ancestor string) ir.PhysicalUnnest {
			unnest := codingExpansion(ir.PhysicalUnnestExclude)
			unnest.Owner = ir.PhysicalUnnestOwner{OccurrenceID: occurrence, ResourceType: "Observation",
				RootVariable: "root", OwnerVariable: owner, Route: []ir.PhysicalUnnestRouteStep{routeTraversal(occurrence, owner)}}
			unnest.Expression = arrayExtract(owner, "Observation",
				spec.SelectorStep{Field: "component", Iterate: true},
				spec.SelectorStep{Field: "code"},
				spec.SelectorStep{Field: "coding", Iterate: true})
			unnest.OutputVariable, unnest.Ordinality, unnest.HasItemVariable = output, ordinality, hasItem
			unnest.Ancestors = []ir.PhysicalUnnestAncestor{{StepIndex: 0, Variable: ancestor}}
			return unnest
		}
		windowless := basePlan()
		windowless.BindVars["edge_collection"] = "fhir_edge"
		windowless.BindVars["edge_label"] = "has-observation"
		windowless.BindVars["target_type"] = "Observation"
		windowless.Operations = []ir.PhysicalOperation{
			windowless.Operations[0],
			unnestOp(routed("obs-a-owner", "obs_a", "item_a", "position_a", "has_item_a", "component_item_a")),
			unnestOp(routed("obs-b-owner", "obs_b", "item_b", "position_b", "has_item_b", "component_item_b")),
			windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
		}
		requireRebindRefusal(t, windowless, `variable "obs_edge" is already defined`)
	})

	t.Run("registration arm refuses a rebind and names the first join", func(t *testing.T) {
		for _, context := range []struct{ name, variable, first, second string }{
			{"spine join rebind", "edge", "e1", "e3"},
			{"unnest route rebind", "obs_edge", "e2", "e4"},
		} {
			window := &executionWindow{}
			if err := window.rememberEdge(context.variable, context.first); err != nil {
				t.Fatalf("%s: first registration: %v", context.name, err)
			}
			err := window.rememberEdge(context.variable, context.second)
			if !errors.Is(err, ErrUnsupportedPostgresPlan) {
				t.Fatalf("%s: rebind error = %v, want ErrUnsupportedPostgresPlan", context.name, err)
			}
			if want := `edge variable "` + context.variable + `"`; !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: rebind error %q must name the variable", context.name, err)
			}
			if want := `already joined as "` + context.first + `"`; !strings.Contains(err.Error(), want) {
				t.Fatalf("%s: rebind error %q must name the first join alias", context.name, err)
			}
		}
		window := &executionWindow{}
		if err := window.rememberEdge("", "e9"); err != nil {
			t.Fatalf("untyped (edgeless) traversal must be a no-op: %v", err)
		}
		if len(window.edgeVars) != 0 {
			t.Fatalf("edgeless traversal registered %v", window.edgeVars)
		}
	})
}

func requireRebindRefusal(t *testing.T, plan ir.PhysicalPlan, wantFragment string) {
	t.Helper()
	sawUnnest := false
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			sawUnnest = true
		}
	}
	check := func(name string, err error) {
		if err == nil || !strings.Contains(err.Error(), wantFragment) {
			t.Fatalf("%s: %s error = %v, want plan-surface refusal containing %q", t.Name(), name, err, wantFragment)
		}
	}
	if sawUnnest {
		_, entryErr := RenderUnnest(plan, mustBinding(t))
		check("entry", entryErr)
	}
	_, dispatchErr := RenderPhysicalPlan(plan, mustBinding(t))
	check("dispatch", dispatchErr)
}

func TestWindowSpineSortsRootKeyByLogicalIdAndRejectsPayloadSorts(t *testing.T) {
	// The _key spelling keeps its own tuple-vocabulary checks on W2 below.
	// On the spine, the generic execution window's root _key sort is the
	// canonical identity spelling: since the row-execution path became the
	// compiled renderer path (arch-backend-11), every PostgreSQL stream
	// carries a root _key spine sort, and the renderer maps it onto the
	// logical_id storage column — exactly the term W2's identity segments
	// emit. The mapping is live-verified by the engine stream witnesses
	// (ORDER BY logical_id ASC, resource_seq ASC). Payload paths stay refused.
	t.Run("RootKeySortRendersLogicalIdOrdering", func(t *testing.T) {
		plan := basePlan()
		plan.Operations = []ir.PhysicalOperation{
			plan.Operations[0], windowSortOp(ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}), windowLimitOp("limit"),
			windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
		}
		rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
		if err != nil {
			t.Fatalf("root _key spine sort must render through the canonical identity mapping: %v", err)
		}
		if got := windowOrderByClause(t, rendered.SQL); got != `r0."logical_id" ASC, r0."resource_seq" ASC` {
			t.Fatalf("root _key spine sort must order by the logical_id storage column, got %q", got)
		}
	})
	for _, tc := range []struct {
		name string
		key  ir.PhysicalValue
	}{
		{"payload path sort", ir.PhysicalValue{Variable: "root", Path: []string{"status"}}},
		{"multi-segment path", ir.PhysicalValue{Variable: "root", Path: []string{"payload", "status"}}},
	} {
		plan := basePlan()
		plan.Operations = []ir.PhysicalOperation{
			plan.Operations[0], windowSortOp(tc.key), windowLimitOp("limit"),
			windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
		}
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Errorf("%s: error = %v, want ErrUnsupportedPostgresPlan", tc.name, err)
		}
	}
}

// ---------------------------------------------------------------------------
// W2: the final window after UNNEST over the complete typed row identity
// ---------------------------------------------------------------------------

func TestWindowFinalSortLimitOverExpansionIsExact(t *testing.T) {
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	plan := windowedUnnestPlan(unnest, 3,
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("position", "position"),
		valueProjection("has_item", "has_item"),
	)
	rendered := renderOK(t, plan)

	if got, want := windowOrderByClause(t, rendered.SQL),
		`r0."logical_id" ASC, (u1.rn IS NOT NULL) ASC, u1.rn ASC, r0."resource_seq" ASC`; got != want {
		t.Fatalf("final-window ORDER BY =\n got %q\nwant %q", got, want)
	}
	if !strings.HasSuffix(rendered.SQL, " LIMIT $6") {
		t.Fatalf("final window must end in the bound LIMIT:\n%s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "@") {
		t.Fatalf("windowed SQL carries AQL bind syntax:\n%s", rendered.SQL)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: "proj-a"},
		{Type: command.PostgreSQLParameterText, Value: "gen-1"},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient","FHIR/Observation"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "{identifier}"},
		{Type: command.PostgreSQLParameterBigint, Value: int64(3)},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("window args =\n got %#v\nwant %#v", rendered.Args, wantArgs)
	}
}

func TestWindowFinalSortLimitOverOuterExpansionOrdersHasItemWitness(t *testing.T) {
	// The has-item witness renders immediately before the rn, the ORDER BY
	// counterpart of the AQL tuple sorting has-item before ordinality
	// (ir.PhysicalUnnestSortKeys; render/aql/render.go sorts every key ASC).
	unnest := identifierExpansion(ir.PhysicalUnnestPreserveParent)
	plan := windowedUnnestPlan(unnest, 2,
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("item", "item"),
		valueProjection("position", "position"),
		valueProjection("has_item", "has_item"),
	)
	rendered := renderOK(t, plan)
	if got, want := windowOrderByClause(t, rendered.SQL),
		`r0."logical_id" ASC, (u1.rn IS NOT NULL) ASC, u1.rn ASC, r0."resource_seq" ASC`; got != want {
		t.Fatalf("OUTER final-window ORDER BY =\n got %q\nwant %q", got, want)
	}
	if !strings.Contains(rendered.SQL, "LEFT JOIN LATERAL") {
		t.Fatalf("OUTER expansion lost its pad join:\n%s", rendered.SQL)
	}
}

func TestWindowFinalSortLimitOverOwnerRouteIncludesOccurrenceWitness(t *testing.T) {
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	plan := basePlan()
	plan.BindVars["limit"] = 2
	planRouteTraversal(&plan, &unnest, ir.PhysicalOutbound, "")
	plan.Operations = []ir.PhysicalOperation{
		plan.Operations[0],
		unnestOp(unnest),
		windowSortOp(windowTuple(unnest)...),
		windowLimitOp("limit"),
		windowReturnOp(
			valueProjection("logical_id", "root", "logical_id"),
			valueProjection("owner_logical_id", "obs", "logical_id"),
			valueProjection("position", "position"),
		),
	}
	rendered := renderOK(t, plan)
	// Root identity, route occurrence witness, distinct-owner identity, then
	// the has-item and rn witnesses, then the renderer completeness chain.
	// Base-carried golden re-pinned at the hop: the owner route is the INNER
	// renderTraversalScan scan (render/aql/navigation_render.go, review round
	// at 061cd197), so the owner row is the top-level x{n}, not the old flat
	// r{n} alias; e1."edge_seq" and the term order are unchanged.
	if got, want := windowOrderByClause(t, rendered.SQL),
		`r0."logical_id" ASC, e1."edge_seq" ASC, x1."logical_id" ASC, (u1.rn IS NOT NULL) ASC, u1.rn ASC, r0."resource_seq" ASC`; got != want {
		t.Fatalf("owner-route final-window ORDER BY =\n got %q\nwant %q", got, want)
	}
}

func TestWindowFinalSortLimitOverTwoExpansionsOrdersEveryWitness(t *testing.T) {
	first := identifierExpansion(ir.PhysicalUnnestExclude)
	second := ir.PhysicalUnnest{
		Owner: ir.PhysicalUnnestOwner{OccurrenceID: "root", ResourceType: "Patient",
			RootVariable: "root", OwnerVariable: "root"},
		OutputVariable: "telecom_item", Ordinality: "telecom_position", HasItemVariable: "telecom_has_item",
		Expression:  arrayExtract("root", "Patient", spec.SelectorStep{Field: "telecom", Iterate: true}),
		EmptyPolicy: ir.PhysicalUnnestExclude,
	}
	plan := basePlan()
	plan.BindVars["limit"] = 4
	keys := append(windowTuple(first), windowTuple(second)...)
	plan.Operations = []ir.PhysicalOperation{
		plan.Operations[0], unnestOp(first), unnestOp(second),
		windowSortOp(keys...), windowLimitOp("limit"),
		windowReturnOp(
			valueProjection("logical_id", "root", "logical_id"),
			valueProjection("position", "position"),
			valueProjection("telecom_position", "telecom_position"),
		),
	}
	rendered := renderOK(t, plan)
	if got, want := windowOrderByClause(t, rendered.SQL),
		`r0."logical_id" ASC, (u1.rn IS NOT NULL) ASC, u1.rn ASC, (u2.rn IS NOT NULL) ASC, u2.rn ASC, r0."resource_seq" ASC`; got != want {
		t.Fatalf("two-expansion final-window ORDER BY =\n got %q\nwant %q", got, want)
	}
}

// mergedNoWindowUnnestGolden is the MERGED pg-sql-09 (commit 00676b9d,
// PR #112) rendering of unnestPlan(expansionProjections()...) under
// mustBinding, captured verbatim from the merged renderer in a detached
// base checkout — ratification condition C2: asserted byte-for-byte, never
// re-imagined.
const mergedNoWindowUnnestGolden = `SELECT r0."logical_id" AS "c0", (u1.item) AS "c1", (u1.rn - 1) AS "c2", (u1.rn IS NOT NULL) AS "c3" FROM "pgir02_it_unit"."fhir_resource" AS r0 CROSS JOIN LATERAL (SELECT u1_e0.item AS item, ROW_NUMBER() OVER (ORDER BY u1_e0.ord) AS rn FROM jsonb_array_elements(CASE WHEN (r0."payload" #> $5::text[]) IS NULL OR jsonb_typeof((r0."payload" #> $5::text[])) = 'null' THEN '[]'::jsonb WHEN jsonb_typeof((r0."payload" #> $5::text[])) = 'array' THEN (r0."payload" #> $5::text[]) WHEN (r0."payload" #> $5::text[]) = 'false'::jsonb OR (r0."payload" #> $5::text[]) = '0'::jsonb OR (r0."payload" #> $5::text[]) = '""'::jsonb THEN '[]'::jsonb ELSE jsonb_build_array((r0."payload" #> $5::text[])) END) WITH ORDINALITY AS u1_e0(item, ord) WHERE jsonb_typeof(u1_e0.item) <> 'null') AS u1 WHERE r0."project" = $1 AND r0."dataset_generation" = $2 AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND r0."resource_type" = $4 ORDER BY r0."resource_seq" ASC, u1.rn ASC`

func TestWindowWithoutWindowKeepsMergedUnnestSQLByteIdentical(t *testing.T) {
	// Ratification condition C2: the no-window pg-sql-09 statement is
	// untouched, asserted against the merged golden text verbatim. The
	// has-item ORDER BY witness appears only while a window is open.
	rendered := renderOK(t, unnestPlan(expansionProjections()...))
	if rendered.SQL != mergedNoWindowUnnestGolden {
		t.Fatalf("no-window statement drifted from the merged pg-sql-09 golden:\n got %q\nwant %q", rendered.SQL, mergedNoWindowUnnestGolden)
	}
	if got, want := windowOrderByClause(t, rendered.SQL), `r0."resource_seq" ASC, u1.rn ASC`; got != want {
		t.Fatalf("no-window ORDER BY drifted:\n got %q\nwant %q", got, want)
	}
	if strings.Contains(rendered.SQL, "IS NOT NULL) ASC") {
		t.Fatalf("no-window statement grew a has-item witness:\n%s", rendered.SQL)
	}
}

func TestWindowRenderIsDeterministicAndDoesNotAliasPlanData(t *testing.T) {
	unnest := identifierExpansion(ir.PhysicalUnnestPreserveParent)
	plan := windowedUnnestPlan(unnest, 3,
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("item", "item"),
		valueProjection("position", "position"),
	)
	first := renderOK(t, plan)
	second := renderOK(t, plan)
	if first.SQL != second.SQL || !reflect.DeepEqual(first.Args, second.Args) {
		t.Fatalf("window render is not deterministic")
	}
	// Mutating the plan binds after rendering must not move the bound value.
	plan.BindVars["limit"] = 99
	third := renderOK(t, plan)
	if third.SQL != first.SQL {
		t.Fatalf("statement text followed a mutated plan bind:\n got %s\nwant %s", third.SQL, first.SQL)
	}
	for _, parameter := range first.Args {
		if bigint, ok := parameter.Value.(int64); ok && bigint == 99 {
			t.Fatalf("rendered arguments aliased a later plan mutation: %#v", first.Args)
		}
	}
}

// ---------------------------------------------------------------------------
// W2 refusal surface
// ---------------------------------------------------------------------------

func TestWindowFinalSortRejectsEveryTupleDeviation(t *testing.T) {
	type mutation func(unnest *ir.PhysicalUnnest, keys *[]ir.PhysicalValue)
	cases := []struct {
		name        string
		mutate      mutation
		projections []ir.PhysicalProjection
	}{
		{"drop the ordinality key", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) { *keys = (*keys)[:2] }, nil},
		{"drop the has-item key", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			*keys = []ir.PhysicalValue{(*keys)[0], (*keys)[2]}
		}, nil},
		{"drop the root key", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) { *keys = (*keys)[1:] }, nil},
		{"root key uses logical_id instead of _key", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			(*keys)[0] = ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}
		}, nil},
		{"root key uses a payload path", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			(*keys)[0] = ir.PhysicalValue{Variable: "root", Path: []string{"payload", "name"}}
		}, nil},
		{"swap has-item and ordinality", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			(*keys)[1], (*keys)[2] = (*keys)[2], (*keys)[1]
		}, nil},
		{"append a duplicate root key", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			*keys = append(*keys, ir.PhysicalValue{Variable: "root", Path: []string{"_key"}})
		}, nil},
		{"sort on the expansion item", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			(*keys)[1] = ir.PhysicalValue{Variable: "item"}
		}, nil},
		{"sort on the item with a path", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			(*keys)[1] = ir.PhysicalValue{Variable: "item", Path: []string{"value"}}
		}, nil},
		{"has-item key carries a path", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			(*keys)[1] = ir.PhysicalValue{Variable: "has_item", Path: []string{"_key"}}
		}, nil},
		{"key is a bind value", func(_ *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			(*keys)[1] = ir.PhysicalValue{BindKey: "limit"}
		}, nil},
		{"ordinality removed from the expansion while a key remains", func(unnest *ir.PhysicalUnnest, keys *[]ir.PhysicalValue) {
			unnest.Ordinality = ""
			*keys = []ir.PhysicalValue{
				ir.PhysicalValue{Variable: "root", Path: []string{"_key"}},
				ir.PhysicalValue{Variable: "has_item"},
				ir.PhysicalValue{Variable: "root", Path: []string{"_key"}},
			}
		}, []ir.PhysicalProjection{
			valueProjection("logical_id", "root", "logical_id"),
			valueProjection("item", "item"),
		}},
	}
	for _, tc := range cases {
		unnest := identifierExpansion(ir.PhysicalUnnestExclude)
		keys := windowTuple(unnest)
		tc.mutate(&unnest, &keys)
		projections := tc.projections
		if projections == nil {
			projections = expansionProjections()
		}
		plan := basePlan()
		plan.BindVars["limit"] = 3
		plan.Operations = []ir.PhysicalOperation{
			plan.Operations[0], unnestOp(unnest), windowSortOp(keys...), windowLimitOp("limit"),
			windowReturnOp(projections...),
		}
		requireWindowRefusal(t, plan, tc.name)
	}
}

func TestWindowFinalSortKeepsThePlanRouteOccurrenceProofExact(t *testing.T) {
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	plan := basePlan()
	plan.BindVars["limit"] = 2
	planRouteTraversal(&plan, &unnest, ir.PhysicalOutbound, "")
	keys := windowTuple(unnest)
	// The tuple must include the route edge occurrence witness: dropping it
	// collapses two distinct join paths and is refused positionally.
	for _, drop := range []int{1} {
		broken := append([]ir.PhysicalValue(nil), keys[:drop]...)
		broken = append(broken, keys[drop+1:]...)
		bad := basePlan()
		bad.BindVars["limit"] = 2
		planRouteTraversal(&bad, &unnest, ir.PhysicalOutbound, "")
		bad.Operations = []ir.PhysicalOperation{
			bad.Operations[0], unnestOp(unnest), windowSortOp(broken...), windowLimitOp("limit"),
			windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
		}
		requireWindowRefusal(t, bad, "drop route edge witness")
	}
}

func TestWindowRejectsWindowPositionViolations(t *testing.T) {
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	validTuple := windowTuple(unnest)
	type arms = []ir.PhysicalOperation
	cases := []struct {
		name string
		ops  func(root, ret ir.PhysicalOperation, u ir.PhysicalOperation) arms
	}{
		{"SORT before the expansion", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, windowSortOp(validTuple...), u, ret}
		}},
		{"LIMIT before the expansion", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, windowLimitOp("limit"), u, ret}
		}},
		{"LIMIT before the SORT", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, u, windowLimitOp("limit"), windowSortOp(validTuple...), ret}
		}},
		{"second SORT after the LIMIT", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, u, windowSortOp(validTuple...), windowLimitOp("limit"), windowSortOp(validTuple...), ret}
		}},
		{"second SORT before the LIMIT", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, u, windowSortOp(validTuple...), windowSortOp(validTuple...), windowLimitOp("limit"), ret}
		}},
		{"second LIMIT", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, u, windowSortOp(validTuple...), windowLimitOp("limit"), windowLimitOp("limit"), ret}
		}},
		{"expansion after the window opens", func(root, ret, u ir.PhysicalOperation) arms {
			late := ir.PhysicalUnnest{
				Owner: ir.PhysicalUnnestOwner{OccurrenceID: "root", ResourceType: "Patient",
					RootVariable: "root", OwnerVariable: "root"},
				OutputVariable: "telecom_item", Ordinality: "telecom_position", HasItemVariable: "telecom_has_item",
				Expression:  arrayExtract("root", "Patient", spec.SelectorStep{Field: "telecom", Iterate: true}),
				EmptyPolicy: ir.PhysicalUnnestExclude,
			}
			return arms{root, u, windowSortOp(validTuple...), unnestOp(late), ret}
		}},
		{"nil SORT payload", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, {Kind: ir.PhysicalSortOp}, u, ret}
		}},
		{"nil LIMIT payload", func(root, ret, u ir.PhysicalOperation) arms {
			return arms{root, u, {Kind: ir.PhysicalLimitOp}, ret}
		}},
	}
	for _, tc := range cases {
		plan := basePlan()
		plan.BindVars["limit"] = 3
		root := plan.Operations[0]
		u := unnestOp(identifierExpansion(ir.PhysicalUnnestExclude))
		ret := windowReturnOp(expansionProjections()...)
		plan.Operations = tc.ops(root, ret, u)
		requireWindowRefusal(t, plan, tc.name)
	}
}

func TestWindowRejectsLimitBindDefects(t *testing.T) {
	// Fail-closed controls: the frozen ir.Validate positive-int contract is
	// the first line and the window's own bind check is the second. Either
	// line refusing is acceptable; rendering must never succeed.
	for _, tc := range []struct {
		name  string
		value any
	}{
		{"zero limit", 0},
		{"negative limit", -4},
		{"string limit", "10"},
	} {
		unnest := identifierExpansion(ir.PhysicalUnnestExclude)
		plan := windowedUnnestPlan(unnest, 3, expansionProjections()...)
		plan.BindVars["limit"] = tc.value
		if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
			t.Errorf("%s: render succeeded, want fail-closed rejection", tc.name)
		}
	}
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	plan := windowedUnnestPlan(unnest, 3, expansionProjections()...)
	delete(plan.BindVars, "limit")
	if _, err := RenderPhysicalPlan(plan, mustBinding(t)); err == nil {
		t.Errorf("missing limit bind: render succeeded, want fail-closed rejection")
	}
}

func TestWindowPreviewSourceWindowByRootIDStaysRefused(t *testing.T) {
	// The window is never taken at the source by root ID: the plan flag keeps
	// its typed refusal (render.go), which is the point of this ticket.
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	plan := windowedUnnestPlan(unnest, 3, expansionProjections()...)
	plan.PreviewSourceWindowByRootID = true
	requireWindowRefusal(t, plan, "preview source window by root id")

	spine := basePlan()
	spine.Operations = []ir.PhysicalOperation{
		spine.Operations[0],
		windowSortOp(ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}),
		windowLimitOp("limit"),
		windowReturnOp(valueProjection("logical_id", "root", "logical_id")),
	}
	spine.PreviewSourceWindowByRootID = true
	if _, err := RenderPhysicalPlan(spine, mustBinding(t)); !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("spine preview-source-window error = %v, want ErrUnsupportedPostgresPlan", err)
	}
}

// ---------------------------------------------------------------------------
// Live PostgreSQL 18: pages over duplicate root rows and expanded children
// repeat identically across executions against literal expected rows.
// ---------------------------------------------------------------------------

const (
	sql12Project    = "sql12-proj"
	sql12Generation = "gen-1"
)

var sql12ResourceNamePattern = regexp.MustCompile(`^verify_sql12_[0-9]+_[0-9a-f]+$`)

type sql12Service struct {
	ctx       context.Context
	admin     *pgxpool.Pool
	iso       *pgxpool.Pool
	client    *pgstore.Client
	schema    string
	resources *pgstore.ResourceRepository
	fail      func(step string, err error)
}

// newSQL12Service mints one owned verify_sql12_* database and schema per
// case, migrates through the merged pg-store-02 runner, and stores literal
// rows through the merged pg-store-04/05 repositories. MUTATING live runs
// are coordinated: the verify_sql12_* prefix is registered with the
// coordinator and the first mutating run awaits its window signal.
func newSQL12Service(t *testing.T) *sql12Service {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the pg-sql-12 window acceptance tests")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	t.Cleanup(admin.Close)

	dbName := fmt.Sprintf("verify_sql12_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !sql12ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		t.Fatalf("create isolated database: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to isolated database: %v", err)
	}
	t.Cleanup(iso.Close)

	schema := fmt.Sprintf("verify_sql12_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !sql12ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create owned schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %v", err)
		}
	})

	client, err := pgstore.Open(ctx, pgstore.Config{DSN: dsn, MaxConns: 4, MinConns: 1, AcquireTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("open pg-store client (requires a PostgreSQL 18 service): %v", err)
	}
	t.Cleanup(client.Close)
	if _, err := pgstore.MigrateSchema(ctx, client, schema); err != nil {
		t.Fatalf("migrate owned schema: %v", err)
	}
	resources, err := pgstore.NewResourceRepository(client, schema)
	if err != nil {
		t.Fatalf("build resource repository: %v", err)
	}
	return &sql12Service{ctx: ctx, admin: admin, iso: iso, client: client, schema: schema, resources: resources,
		fail: func(step string, err error) { t.Fatalf("%s: %v", step, err) }}
}

func (s *sql12Service) insertResource(t *testing.T, resourceType, logicalID, authPath string, payload map[string]any) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode payload: %v", err)
	}
	if _, err := s.resources.InsertResource(s.ctx, source.Resource{
		Project: sql12Project, DatasetGeneration: sql12Generation, ResourceType: resourceType,
		LogicalID: logicalID, AuthResourcePath: authPath, Payload: raw,
	}); err != nil {
		s.fail("insert resource "+resourceType+"/"+logicalID, err)
	}
}

func (s *sql12Service) insertEdge(t *testing.T, rec pgstore.EdgeOccurrenceRow) {
	t.Helper()
	edges, err := pgstore.NewEdgeRepository(s.client, s.schema)
	if err != nil {
		s.fail("build edge repository", err)
	}
	if _, err := edges.InsertEdge(s.ctx, rec); err != nil {
		s.fail("insert edge "+rec.Label+" "+rec.SourceLogicalID+"->"+rec.TargetLogicalID, err)
	}
}

func (s *sql12Service) runRendered(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) [][]string {
	t.Helper()
	return s.queryRows(t, rendered.SQL, windowParameterValues(rendered.Args)...)
}

func (s *sql12Service) queryRows(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("execute statement: "+sql, err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			t.Fatalf("read row: %v", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = sql09StringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("finish statement: %v", err)
	}
	return out
}

func (s *sql12Service) witness(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	return s.queryRows(t, sql, args...)
}

func windowParameterValues(args []command.PostgreSQLParameter) []any {
	values := make([]any, len(args))
	for index, parameter := range args {
		values[index] = parameter.Value
	}
	return values
}

// windowRepeat executes the rendered statement the given number of times and
// requires every execution to return the identical page. Expected pages are
// additionally compared against literal rows supplied by the test, never
// derived from renderer output.
func windowRepeat(t *testing.T, svc *sql12Service, rendered command.RenderedPostgresPhysicalPlan, executions int, want [][]string) {
	t.Helper()
	for run := 1; run <= executions; run++ {
		got := svc.runRendered(t, rendered)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("execution %d page =\n got %v\nwant %v", run, got, want)
		}
	}
}

func sql12Binding(t *testing.T, schema string) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(schema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	return binding
}

func (s *sql12Service) render(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, sql12Binding(t, s.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	return rendered
}

// L1: five root rows share every plan-visible sort column (same resource
// type); the page is cut by the bound LIMIT and must repeat identically,
// ordered by the storage occurrence witness, with an independently written
// literal witness.
func TestPG18WindowDuplicateRootRowsPageStably(t *testing.T) {
	svc := newSQL12Service(t)
	for _, id := range []string{"dup-1", "dup-2", "dup-3", "dup-4", "dup-5"} {
		svc.insertResource(t, "Patient", id, "FHIR/Patient", map[string]any{"status": "final"})
	}
	plan := scopedPlan(sql12Project, sql12Generation, []string{"FHIR/Patient"}, false)
	plan.BindVars["limit"] = 3
	plan.Operations = append(plan.Operations,
		windowSortOp(ir.PhysicalValue{Variable: "root", Path: []string{"resource_type"}}),
		windowLimitOp("limit"))
	plan = withReturn(plan, valueProjection("logical_id", "root", "logical_id"))
	rendered := svc.render(t, plan)

	want := [][]string{{"dup-1"}, {"dup-2"}, {"dup-3"}}
	windowRepeat(t, svc, rendered, 5, want)

	witness := svc.witness(t, `SELECT logical_id FROM "`+svc.schema+`"."fhir_resource"`+
		` WHERE project = 'sql12-proj' AND dataset_generation = 'gen-1'`+
		` AND resource_type = 'Patient' AND auth_resource_path = 'FHIR/Patient'`+
		` ORDER BY resource_type, resource_seq LIMIT 3`)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness page = %v, want %v", witness, want)
	}
}

// L2: a LIMIT that cuts mid-expansion pages duplicate items in stored-array
// order; the page repeats identically across executions and matches an
// independent hand-written expansion witness.
func TestPG18WindowExpandedChildrenPageStablyWithDuplicateItems(t *testing.T) {
	svc := newSQL12Service(t)
	svc.insertResource(t, "Patient", "p-array", "FHIR/Patient", map[string]any{
		"identifier": []any{
			map[string]any{"value": "a"}, map[string]any{"value": "a"},
			map[string]any{"value": "b"}, map[string]any{"value": "c"},
		},
	})
	unnest := identifierExpansion(ir.PhysicalUnnestExclude)
	plan := scopedPlan(sql12Project, sql12Generation, []string{"FHIR/Patient"}, false)
	plan.Operations = append(plan.Operations,
		unnestOp(unnest),
		windowSortOp(windowTuple(unnest)...),
		windowLimitOp("limit"))
	plan.BindVars["limit"] = 3
	plan = withReturn(plan,
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("item_value", "item", "value"),
		valueProjection("position", "position"))
	rendered := svc.render(t, plan)

	// sql09StringifyCell decodes jsonb scalars to their Go-native form:
	// `#> '{value}'` on a JSON string cell stringifies unquoted.
	want := [][]string{
		{"p-array", "a", "0"},
		{"p-array", "a", "1"},
		{"p-array", "b", "2"},
	}
	windowRepeat(t, svc, rendered, 5, want)

	witness := svc.witness(t, `SELECT r.logical_id, elem.value #> '{value}', elem.ord - 1`+
		` FROM "`+svc.schema+`"."fhir_resource" AS r`+
		` CROSS JOIN LATERAL jsonb_array_elements(r.payload #> '{identifier}'::text[]) WITH ORDINALITY AS elem(value, ord)`+
		` WHERE r.project = 'sql12-proj' AND r.dataset_generation = 'gen-1'`+
		` AND r.resource_type = 'Patient' AND r.auth_resource_path = 'FHIR/Patient'`+
		` ORDER BY r.logical_id, r.resource_seq, elem.ord LIMIT 3`)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness page = %v, want %v", witness, want)
	}
}

// L3: two edge occurrences of one owner are distinguished by the occurrence
// witness; a LIMIT cutting between them pages in stable insertion order.
func TestPG18WindowOwnerOccurrencesPageStably(t *testing.T) {
	svc := newSQL12Service(t)
	svc.insertResource(t, "Patient", "p-own", "FHIR/Patient", map[string]any{})
	svc.insertResource(t, "Patient", "p-ta", "FHIR/Patient", map[string]any{})
	svc.insertResource(t, "Patient", "p-tb", "FHIR/Patient", map[string]any{})
	svc.insertResource(t, "Patient", "q-own", "FHIR/Patient", map[string]any{})
	svc.insertResource(t, "Patient", "q-ta", "FHIR/Patient", map[string]any{})
	for index, target := range []string{"p-ta", "p-tb"} {
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: sql12Project, DatasetGeneration: sql12Generation, AuthResourcePath: "FHIR/Patient",
			Label: "has-item-owner", SourceResourceType: "Patient", SourceLogicalID: "p-own",
			TargetResourceType: "Patient", TargetLogicalID: target,
			SourcePath: fmt.Sprintf("item[%d]", index), SourceOrdinal: int64(index),
		})
	}
	svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
		Project: sql12Project, DatasetGeneration: sql12Generation, AuthResourcePath: "FHIR/Patient",
		Label: "has-item-owner", SourceResourceType: "Patient", SourceLogicalID: "q-own",
		TargetResourceType: "Patient", TargetLogicalID: "q-ta",
		SourcePath: "item[0]", SourceOrdinal: 0,
	})

	unnest := ir.PhysicalUnnest{
		Owner: ir.PhysicalUnnestOwner{OccurrenceID: "owner-route", ResourceType: "Patient",
			RootVariable: "root", OwnerVariable: "own",
			Route: []ir.PhysicalUnnestRouteStep{{OccurrenceID: "owner-route", Traversal: ir.PhysicalTraversal{
				SourceVariable: "root", TargetVariable: "own", EdgeVariable: "owner_edge",
				Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
				EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
				EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
			}}}},
		OutputVariable: "item", Ordinality: "position", HasItemVariable: "has_item",
		Expression:  arrayExtract("own", "Patient", spec.SelectorStep{Field: "identifier", Iterate: true}),
		EmptyPolicy: ir.PhysicalUnnestPreserveParent,
	}
	plan := scopedPlan(sql12Project, sql12Generation, []string{"FHIR/Patient"}, false)
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = "has-item-owner"
	plan.BindVars["target_type"] = "Patient"
	plan.BindVars["limit"] = 3
	plan.Operations = append(plan.Operations,
		unnestOp(unnest),
		windowSortOp(windowTuple(unnest)...),
		windowLimitOp("limit"))
	plan = withReturn(plan,
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("owner_logical_id", "own", "logical_id"))
	rendered := svc.render(t, plan)

	// p-own sorts first and its two occurrences both fit; the third row is
	// q-own's single occurrence. The page is the full result here, and it
	// must repeat identically on every execution.
	want := [][]string{
		{"p-own", "p-ta"},
		{"p-own", "p-tb"},
		{"q-own", "q-ta"},
	}
	windowRepeat(t, svc, rendered, 5, want)

	witness := svc.witness(t, `SELECT r.logical_id, r1.logical_id`+
		` FROM "`+svc.schema+`"."fhir_resource" AS r`+
		` INNER JOIN "`+svc.schema+`"."fhir_edge_occurrence" AS e`+
		` ON e.project = 'sql12-proj' AND e.dataset_generation = 'gen-1'`+
		` AND e.auth_resource_path = 'FHIR/Patient' AND e.label = 'has-item-owner'`+
		` AND e.source_resource_type = 'Patient' AND e.source_logical_id = r.logical_id`+
		` AND e.target_resource_type = 'Patient'`+
		` INNER JOIN "`+svc.schema+`"."fhir_resource" AS r1`+
		` ON r1.project = 'sql12-proj' AND r1.dataset_generation = 'gen-1'`+
		` AND r1.auth_resource_path = 'FHIR/Patient' AND r1.resource_type = 'Patient'`+
		` AND r1.logical_id = e.target_logical_id`+
		` WHERE r.project = 'sql12-proj' AND r.dataset_generation = 'gen-1'`+
		` AND r.resource_type = 'Patient' AND r.auth_resource_path = 'FHIR/Patient'`+
		` ORDER BY r.logical_id, e.edge_seq, r1.logical_id LIMIT 3`)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness page = %v, want %v", witness, want)
	}
}

// L4: the OUTER pad row inside the cut page is asserted by CONTENTS — the
// item and ordinality cells are SQL NULL and has-item is false on exactly
// the padded row — never by positional trivia.
func TestPG18WindowOuterPadRowPageContents(t *testing.T) {
	svc := newSQL12Service(t)
	svc.insertResource(t, "Patient", "a-empty", "FHIR/Patient", map[string]any{})
	svc.insertResource(t, "Patient", "b-full", "FHIR/Patient", map[string]any{
		"identifier": []any{map[string]any{"value": "x"}, map[string]any{"value": "y"}},
	})
	unnest := identifierExpansion(ir.PhysicalUnnestPreserveParent)
	plan := scopedPlan(sql12Project, sql12Generation, []string{"FHIR/Patient"}, false)
	plan.BindVars["limit"] = 3
	plan.Operations = append(plan.Operations,
		unnestOp(unnest),
		windowSortOp(windowTuple(unnest)...),
		windowLimitOp("limit"))
	plan = withReturn(plan,
		valueProjection("logical_id", "root", "logical_id"),
		valueProjection("item", "item"),
		valueProjection("position", "position"),
		valueProjection("has_item", "has_item"))
	rendered := svc.render(t, plan)

	// The item projection carries the whole element, so it stringifies as
	// compact JSON; scalars inside it are quoted by JSON itself.
	want := [][]string{
		{"a-empty", "<null>", "<null>", "false"},
		{"b-full", `{"value":"x"}`, "0", "true"},
		{"b-full", `{"value":"y"}`, "1", "true"},
	}
	windowRepeat(t, svc, rendered, 5, want)

	// Independent literal witness for the non-pad rows: a hand-written
	// OUTER expansion over just b-full must reproduce its item rows, while
	// the same statement without padding over a-empty yields zero rows —
	// pinning that the pad row exists exactly once in the rendered page and
	// carries no item content (its <null>/<null>/false cells above are the
	// content proof).
	// The witness projects the whole element, matching the page's item
	// projection cell-for-cell.
	witness := svc.witness(t, `SELECT r.logical_id, elem.value, elem.ord - 1`+
		` FROM "`+svc.schema+`"."fhir_resource" AS r`+
		` CROSS JOIN LATERAL jsonb_array_elements(r.payload #> '{identifier}'::text[])`+
		` WITH ORDINALITY AS elem(value, ord)`+
		` WHERE r.project = 'sql12-proj' AND r.dataset_generation = 'gen-1'`+
		` AND r.resource_type = 'Patient' AND r.auth_resource_path = 'FHIR/Patient'`+
		` AND r.logical_id = 'b-full'`+
		` ORDER BY r.logical_id, r.resource_seq, elem.ord`)
	wantNoPad := [][]string{{"b-full", `{"value":"x"}`, "0"}, {"b-full", `{"value":"y"}`, "1"}}
	if !reflect.DeepEqual(witness, wantNoPad) {
		t.Fatalf("non-pad witness page = %v, want %v", witness, wantNoPad)
	}
}

// L5: the filter executes before the final window as the plan specifies:
// rows the filter removes never appear on any page, under repetition, with
// an independent literal witness.
func TestPG18WindowFilterExecutesBeforeFinalWindow(t *testing.T) {
	svc := newSQL12Service(t)
	svc.insertResource(t, "Patient", "x-final-1", "FHIR/Patient", map[string]any{"status": "final"})
	svc.insertResource(t, "Patient", "x-final-2", "FHIR/Patient", map[string]any{"status": "final"})
	svc.insertResource(t, "Patient", "x-prelim-0", "FHIR/Patient", map[string]any{"status": "prelim"})
	svc.insertResource(t, "Patient", "x-zz-prelim", "FHIR/Patient", map[string]any{"status": "prelim"})

	plan := scopedPlan(sql12Project, sql12Generation, []string{"FHIR/Patient"}, false)
	plan.BindVars["limit"] = 3
	plan.BindVars["status_value"] = "final"
	plan.Operations = append(plan.Operations,
		ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"status"}},
			Right: &ir.PhysicalValue{BindKey: "status_value"},
		}}},
		windowSortOp(ir.PhysicalValue{Variable: "root", Path: []string{"resource_type"}}),
		windowLimitOp("limit"))
	plan = withReturn(plan, valueProjection("logical_id", "root", "logical_id"))
	rendered := svc.render(t, plan)

	// The LIMIT is larger than the filtered set: the page proves the filter
	// ran before the window (prelim rows never surface), and the ORDER BY
	// clause text proves the window is the final clause before LIMIT.
	want := [][]string{{"x-final-1"}, {"x-final-2"}}
	windowRepeat(t, svc, rendered, 5, want)
	order := windowOrderByClause(t, rendered.SQL)
	if !strings.HasPrefix(order, `r0."resource_type" ASC`) || !strings.HasSuffix(order, `r0."resource_seq" ASC`) {
		t.Fatalf("window did not stay the final clause before LIMIT: %q", order)
	}

	witness := svc.witness(t, `SELECT logical_id FROM "`+svc.schema+`"."fhir_resource"`+
		` WHERE project = 'sql12-proj' AND dataset_generation = 'gen-1'`+
		` AND resource_type = 'Patient' AND auth_resource_path = 'FHIR/Patient'`+
		` AND payload #> '{status}'::text[] = '"final"'::jsonb`+
		` ORDER BY resource_type, resource_seq LIMIT 3`)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness page = %v, want %v", witness, want)
	}
}

// ---------------------------------------------------------------------------
// Zero-survivor audit (delta-scoped): nothing this file mints may survive.
// It is the final function in the last-sorted test file of the package, so
// every live case above has already run when it executes.
// ---------------------------------------------------------------------------

func TestZZNoLeftoverSQL12Resources(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer admin.Close()
	var databases int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'verify_sql12_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d verify_sql12_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify_sql12_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d verify_sql12_* schemas still exist in the service database", schemas)
	}
}
