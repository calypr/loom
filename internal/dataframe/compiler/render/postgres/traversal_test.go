package postgres

// pg-sql-06: depth-one typed edge-occurrence traversals in the PostgreSQL
// renderer. This file is the acceptance surface for traversal.go: the
// OPTIONAL parent-preserving LEFT JOIN LATERAL child join and the REQUIRED
// match EXISTS semi-join.
//
// Offline: golden statement and argument literals for both registered
// shapes (including the two traversal pins relocated from render_test.go,
// semantically identical modulo the lateral SQL shape), the closed refusal
// matrix for every shape outside the registered slice, the main-sequence
// guards that keep OPTIONAL child rows out of FILTER and SORT, and
// determinism/inertness probes.
//
// Live (LOOM_TEST_POSTGRES_URL, real PostgreSQL 18 through the pgir02_it_*
// harness in render_pg18_test.go): OPTIONAL multiplicity with parent-row
// preservation across every defect class (duplicate occurrences, dangling
// edges mixed with resolved ones, all-dangling parents, edge-free parents,
// wrong-label decoys), REQUIRED match exclusion including dangling and
// wrong-label witnesses, a REQUIRED match with a typed payload selector
// filter, and INBOUND matrices for both modes — each compared to literal
// rows plus a hand-written direct-SQL witness. TestMain audits, delta-
// scoped to fixtures created during this run and tracked by the tests
// themselves, that nothing under the pgir02_it_* prefix survives.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	pgstore "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgstoreEdgeRow builds one stored Patient→Specimen has-* occurrence for
// the live fixtures, authenticated on the Patient path (the anchored side).
func pgstoreEdgeRow(project, generation, label, patient, specimen, path string) pgstore.EdgeOccurrenceRow {
	return pgstore.EdgeOccurrenceRow{
		Project: project, DatasetGeneration: generation, AuthResourcePath: "FHIR/Patient",
		Label: label, SourceResourceType: "Patient", SourceLogicalID: patient,
		TargetResourceType: "Specimen", TargetLogicalID: specimen,
		SourcePath: path, SourceOrdinal: 0,
	}
}

// trvInsertDanglingSourceEdge writes one edge row whose source resource is
// deliberately not stored. The schema carries no source FK (migration
// 000004): an unstored source is a legitimate storage state (a source
// removed after publication), and excluding such rows at query time is
// exactly what the traversal's INNER JOIN pair must prove. The repository's
// insert gate models publication-time discipline and refuses the state, so
// the fixture writes the literal row through the service pool, mirroring
// the repository's own column list.
func trvInsertDanglingSourceEdge(t *testing.T, svc *pgir02Service, label, sourceType, sourceID, targetType, targetID, authPath, path string) {
	t.Helper()
	_, err := svc.iso.Exec(svc.ctx,
		`INSERT INTO "`+svc.schema+`"."fhir_edge_occurrence"`+
			` (project, dataset_generation, auth_resource_path, label,`+
			` source_resource_type, source_logical_id, target_resource_type, target_logical_id,`+
			` source_path, source_ordinal)`+
			` VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,0)`,
		itProject, itGeneration, authPath, label, sourceType, sourceID, targetType, targetID, path)
	if err != nil {
		svc.fail("insert dangling-source edge", err)
	}
}

// ---------------------------------------------------------------------------
// Offline: OPTIONAL traversal — LEFT JOIN LATERAL golden statements.
// ---------------------------------------------------------------------------

// trvTraversalPlan builds the registered OPTIONAL plan shape: one scoped
// root scan, one depth-one typed traversal, one positional return over the
// parent and child logical ids. Bind keys follow the shared lowering
// contract (edge_collection bound to fhir_edge, edge_label, target_type).
func trvTraversalPlan(direction ir.PhysicalTraversalDirection, edgeTargetTypeField, label, targetType string) ir.PhysicalPlan {
	plan := basePlan()
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = label
	plan.BindVars["target_type"] = targetType
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "target", EdgeVariable: "edge",
			Direction: direction, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: edgeTargetTypeField, Strategy: ir.PhysicalTraversalNative,
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			{Name: "specimen_id", Value: ir.PhysicalValue{Variable: "target", Path: []string{"logical_id"}}},
		}}},
	}
	return plan
}

// TestRenderOutboundTraversalSQLIsExact is the relocated pg-ir-02 golden,
// re-pinned for the lateral shape: the reached target and the renderer-owned
// edge witness live inside LEFT JOIN LATERAL (edge INNER JOIN target) ON
// TRUE, so a parent with zero surviving occurrences is preserved rather than
// inner-join excluded (render/aql navigation_render.go renderTraversalSet).
// The assertions are identical to the pre-lateral pin modulo the join shape:
// full statement literal, six ordered arguments, and plan-data inertness.
func TestRenderOutboundTraversalSQLIsExact(t *testing.T) {
	plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "has-specimen", "Specimen")
	rendered := renderOK(t, plan)
	wantSQL := `SELECT r0."logical_id" AS "c0", r1."logical_id" AS "c1"` +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` LEFT JOIN LATERAL (SELECT x1."auth_resource_path" AS "auth_resource_path",` +
		` x1."dataset_generation" AS "dataset_generation",` +
		` x1."logical_id" AS "logical_id", x1."payload" AS "payload",` +
		` x1."project" AS "project", x1."recorded_at" AS "recorded_at",` +
		` x1."resource_seq" AS "resource_seq", x1."resource_type" AS "resource_type",` +
		` e1."edge_seq" AS "edge_seq"` +
		` FROM "pgir02_it_unit"."fhir_edge_occurrence" AS e1` +
		` INNER JOIN "pgir02_it_unit"."fhir_resource" AS x1` +
		` ON x1."project" = $1 AND x1."dataset_generation" = $2` +
		` AND x1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND x1."resource_type" = $6 AND x1."logical_id" = e1."target_logical_id"` +
		` WHERE e1."project" = $1 AND e1."dataset_generation" = $2` +
		` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND e1."label" = $5 AND e1."source_resource_type" = $4` +
		` AND e1."source_logical_id" = r0."logical_id" AND e1."target_resource_type" = $6)` +
		` AS r1 ON TRUE` +
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4` +
		` ORDER BY r0."resource_seq" ASC, r1."edge_seq" ASC`
	if rendered.SQL != wantSQL {
		t.Fatalf("SQL mismatch:\n got %s\nwant %s", rendered.SQL, wantSQL)
	}
	if len(rendered.Args) != 6 {
		t.Fatalf("args = %d, want 6 (project, generation, auth paths, root type, label, target type)", len(rendered.Args))
	}
	for index, want := range []struct {
		typ, value string
	}{
		{"text", "proj-a"}, {"text", "gen-1"}, {"jsonb", ""},
		{"text", "Patient"}, {"text", "has-specimen"}, {"text", "Specimen"},
	} {
		got := rendered.Args[index]
		if string(got.Type) != want.typ {
			t.Fatalf("arg %d type = %q, want %q", index, string(got.Type), want.typ)
		}
		if want.value != "" && got.Value != want.value {
			t.Fatalf("arg %d value = %#v, want %q", index, got.Value, want.value)
		}
	}
	if raw, ok := rendered.Args[2].Value.(json.RawMessage); !ok ||
		string(raw) != `["FHIR/Patient","FHIR/Observation"]` {
		t.Fatalf("auth argument = %#v", rendered.Args[2].Value)
	}
	// The plan's own Arango metadata never reaches SQL text: the edge
	// "collection" bind value and plan variable names are not SQL text.
	for _, leaked := range []string{"fhir_edge\"", "\"edge\"", "\"target\"", "\"root\""} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("plan data %q leaked into SQL: %s", leaked, rendered.SQL)
		}
	}
}

// TestRenderInboundTraversalBindsReverseEndpointColumns is the relocated
// pg-ir-02 pin, re-pinned for the lateral shape. INBOUND anchors the stored
// source side's opposite: the anchored endpoint is target_* and the reached
// endpoint is source_*. Inside the child subquery the reached resource row
// is x1 (r1 names the lateral relation itself); the semantic content of the
// pre-lateral pins is unchanged.
func TestRenderInboundTraversalBindsReverseEndpointColumns(t *testing.T) {
	plan := trvTraversalPlan(ir.PhysicalInbound, "from_type", "has-specimen", "Observation")
	rendered := renderOK(t, plan)
	for _, want := range []string{
		`e1."target_resource_type" = $4`,
		`e1."target_logical_id" = r0."logical_id"`,
		`e1."source_resource_type" = $6`,
		`x1."resource_type" = $6`,
		`x1."logical_id" = e1."source_logical_id"`,
		`x1."resource_type" AS "resource_type"`,
	} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("inbound SQL missing %q: %s", want, rendered.SQL)
		}
	}
	if strings.Contains(rendered.SQL, `e1."source_logical_id" = r0."logical_id"`) {
		t.Fatalf("inbound traversal must not anchor on the stored source side: %s", rendered.SQL)
	}
}

// TestRenderTraversalOrderByUsesEdgeWitness proves the deterministic row
// order over expanded occurrences: root resource_seq then each lateral's
// renderer-owned edge_seq, with distinct aliases per traversal.
func TestRenderTraversalOrderByUsesEdgeWitness(t *testing.T) {
	plan := basePlan()
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = "has-specimen"
	plan.BindVars["target_type"] = "Specimen"
	plan.BindVars["edge_label_2"] = "has-observation"
	plan.BindVars["target_type_2"] = "Observation"
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "specimen", EdgeVariable: "edge",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
		}},
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "specimen", TargetVariable: "observation", EdgeVariable: "edge2",
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label_2", TargetTypeBindKey: "target_type_2",
			EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "patient_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
			{Name: "observation_id", Value: ir.PhysicalValue{Variable: "observation", Path: []string{"logical_id"}}},
		}}},
	}
	rendered := renderOK(t, plan)
	for _, want := range []string{
		`ORDER BY r0."resource_seq" ASC, r1."edge_seq" ASC, r2."edge_seq" ASC`,
		`"fhir_edge_occurrence" AS e1`, `"fhir_edge_occurrence" AS e2`,
		`AS r1 ON TRUE`, `AS r2 ON TRUE`,
		`e2."source_logical_id" = r1."logical_id"`,
		`x2."logical_id" = e2."target_logical_id"`,
	} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("chained traversal SQL missing %q: %s", want, rendered.SQL)
		}
	}
}

// ---------------------------------------------------------------------------
// Offline: REQUIRED match — the pre-window EXISTS semi-join.
// ---------------------------------------------------------------------------

func trvRequiredSubplan(operations ...ir.PhysicalOperation) ir.PhysicalSubplan {
	return ir.PhysicalSubplan{
		Captures:   []string{"root"},
		Operations: operations,
		Return: ir.PhysicalExpression{
			Kind:         ir.PhysicalValueExpression,
			Cardinality:  ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{BindKey: "required_match_result"},
		},
	}
}

func trvSubplanTraversal(sourceVariable, targetVariable, labelKey, targetTypeKey string) ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
		SourceVariable: sourceVariable, TargetVariable: targetVariable,
		EdgeVariable: targetVariable + "_edge",
		Direction:    ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection",
		EdgeLabelBindKey: labelKey, TargetTypeBindKey: targetTypeKey,
		EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative,
	}}
}

// trvPayloadMatchPredicate builds the canonical REQUIRED-match selector
// filter lower/required_match.go:131-137 emits: an ARRAY-cardinality,
// EMPTY_ON_NULL EXTRACT over the row's ["payload"] document with a
// validated value kind. The value bind is "status_value" for the tests
// here; EXISTS/MISSING carry no right value.
func trvPayloadMatchPredicate(variable, resourceType, operator string, valueKind spec.FilterValueKind, steps ...string) *ir.PhysicalPredicateExpression {
	selectorSteps := make([]spec.SelectorStep, 0, len(steps))
	for _, field := range steps {
		selectorSteps = append(selectorSteps, spec.SelectorStep{Field: field})
	}
	predicate := &ir.PhysicalPredicate{
		Operator:  operator,
		ValueKind: valueKind,
		LeftExpression: &ir.PhysicalExpression{
			Kind:         ir.PhysicalExtractExpression,
			Cardinality:  ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull,
			Extract: &ir.PhysicalExtract{
				Source:       ir.PhysicalValue{Variable: variable, Path: []string{"payload"}},
				ResourceType: resourceType,
				Selector:     spec.Selector{Steps: selectorSteps},
			},
		},
	}
	if operator != "EXISTS" && operator != "MISSING" {
		predicate.Right = &ir.PhysicalValue{BindKey: "status_value"}
	}
	return &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: predicate}
}

// trvRequiredPlan builds the REQUIRED match plan shape lower/required_match
// .go emits for a REQUIRED semantic route: the root scan plus a FILTER whose
// expression is one EXISTS predicate over a bounded correlated subplan
// anchored on the root row, with the canonical constant-1 return.
func trvRequiredPlan(subplan ir.PhysicalSubplan) ir.PhysicalPlan {
	plan := basePlan()
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = "has-specimen"
	plan.BindVars["target_type"] = "Specimen"
	plan.BindVars["required_match_result"] = 1
	plan.Operations = []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
			Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &subplan},
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		}}},
	}
	return plan
}

// TestRenderRequiredMatchExistsIsExactSemiJoin pins the REQUIRED match
// rendering: WHERE EXISTS (SELECT 1 FROM edge INNER JOIN target …), the
// root semi-join form the AQL pipeline runs pre-window
// (lower/required_match.go:12-15). The no-match parent is excluded (no
// LEFT JOIN, no lateral), the EXISTS is total so no COALESCE guard appears,
// and the LIMIT 1 witness limit the AQL LENGTH(FOR … LIMIT 1) form needs is
// unnecessary and absent. Scope arguments dedupe with the root scan.
func TestRenderRequiredMatchExistsIsExactSemiJoin(t *testing.T) {
	subplan := trvRequiredSubplan(trvSubplanTraversal("root", "child", "edge_label", "target_type"))
	rendered := renderOK(t, trvRequiredPlan(subplan))
	wantSQL := `SELECT r0."logical_id" AS "c0"` +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1 AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4` +
		` AND EXISTS (SELECT 1 FROM "pgir02_it_unit"."fhir_edge_occurrence" AS e1` +
		` INNER JOIN "pgir02_it_unit"."fhir_resource" AS x1` +
		` ON x1."project" = $1 AND x1."dataset_generation" = $2` +
		` AND x1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND x1."resource_type" = $6 AND x1."logical_id" = e1."target_logical_id"` +
		` WHERE e1."project" = $1 AND e1."dataset_generation" = $2` +
		` AND e1."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND e1."label" = $5 AND e1."source_resource_type" = $4` +
		` AND e1."source_logical_id" = r0."logical_id" AND e1."target_resource_type" = $6)` +
		` ORDER BY r0."resource_seq" ASC`
	if rendered.SQL != wantSQL {
		t.Fatalf("SQL mismatch:\n got %s\nwant %s", rendered.SQL, wantSQL)
	}
	if len(rendered.Args) != 6 {
		t.Fatalf("args = %d, want 6 (scope and traversal parameters deduped with the root scan)", len(rendered.Args))
	}
	if strings.Contains(rendered.SQL, "LIMIT") {
		t.Fatalf("EXISTS makes the witness LIMIT 1 unnecessary; none may appear: %s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "LEFT JOIN") {
		t.Fatalf("a required match must exclude no-match parents, never null-extend: %s", rendered.SQL)
	}
	if strings.Contains(rendered.SQL, "COALESCE") {
		t.Fatalf("EXISTS is total and needs no null guard: %s", rendered.SQL)
	}
	// Pre-window position: the semi-join filters root rows, so it lands in
	// WHERE ahead of the deterministic ordering, never after it.
	if strings.Index(rendered.SQL, "EXISTS") > strings.Index(rendered.SQL, "ORDER BY") {
		t.Fatalf("required match must precede the ordering window: %s", rendered.SQL)
	}
}

// TestRenderRequiredMatchInboundCorrelatesReversed pins that an INBOUND
// subplan step anchors the stored target side and correlates through it.
func TestRenderRequiredMatchInboundCorrelatesReversed(t *testing.T) {
	step := trvSubplanTraversal("root", "child", "edge_label", "target_type")
	step.Traversal.Direction = ir.PhysicalInbound
	step.Traversal.EdgeTargetTypeField = "from_type"
	rendered := renderOK(t, trvRequiredPlan(trvRequiredSubplan(step)))
	for _, want := range []string{
		`e1."target_resource_type" = $4`,
		`e1."target_logical_id" = r0."logical_id"`,
		`e1."source_resource_type" = $6`,
		`x1."logical_id" = e1."source_logical_id"`,
	} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("inbound required-match SQL missing %q: %s", want, rendered.SQL)
		}
	}
}

// TestRenderRequiredMatchTypedSubplanFilters registers the closed
// subplan-local filter forms: the selector-EXTRACT payload comparison
// lower/required_match.go emits (rendered through the package's single
// extraction authority, agreeing expression-for-expression with pg-sql-03
// projections) and a typed column IN over the reached row.
func TestRenderRequiredMatchTypedSubplanFilters(t *testing.T) {
	t.Run("payload selector comparison", func(t *testing.T) {
		plan := trvRequiredPlan(trvRequiredSubplan(
			trvSubplanTraversal("root", "child", "edge_label", "target_type"),
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Expression: trvPayloadMatchPredicate("child", "Specimen", "EQUALS", spec.FilterString, "status"),
			}},
		))
		plan.BindVars["status_value"] = "final"
		rendered := renderOK(t, plan)
		// The value list mirrors the AQL selector FOR loop exactly
		// (render/aql/predicates.go renderSelectorPredicate + the value-list
		// normalization of renderSelectorArrayFromSource): missing/JSON-null
		// yields [], a stored array yields its non-null elements, and any
		// other stored value yields the single-element list. ANY is the
		// default quantifier (predicates.go:256-258).
		document := `(x1."payload" #> $7::text[])`
		values := "(CASE WHEN " + document + " IS NULL OR jsonb_typeof(" + document + ") = 'null' THEN '[]'::jsonb" +
			" WHEN jsonb_typeof(" + document + ") = 'array' THEN COALESCE((SELECT jsonb_agg(__elem.e) FROM jsonb_array_elements(" + document + ") AS __elem(e) WHERE __elem.e <> 'null'::jsonb), '[]'::jsonb)" +
			" ELSE jsonb_build_array(" + document + ") END)"
		want := `AND (EXISTS (SELECT 1 FROM jsonb_array_elements(` + values + `) AS __sv(v) WHERE (__sv.v) = ($8::jsonb)))`
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("typed payload filter missing %q:\n%s", want, rendered.SQL)
		}
		if len(rendered.Args) != 8 {
			t.Fatalf("args = %d, want 8", len(rendered.Args))
		}
		if got := rendered.Args[6]; string(got.Type) != "text" || got.Value != "{status}" {
			t.Fatalf("path argument = %#v, want text {status}", got)
		}
		if raw, ok := rendered.Args[7].Value.(json.RawMessage); !ok || string(raw) != `"final"` {
			t.Fatalf("value argument = %#v, want jsonb \"final\"", rendered.Args[7].Value)
		}
	})
	t.Run("column IN over reached row", func(t *testing.T) {
		plan := trvRequiredPlan(trvRequiredSubplan(
			trvSubplanTraversal("root", "child", "edge_label", "target_type"),
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Expression: &ir.PhysicalPredicateExpression{
					Kind: ir.PhysicalComparisonPredicate,
					Comparison: &ir.PhysicalPredicate{
						Operator: "IN",
						Left:     ir.PhysicalValue{Variable: "child", Path: []string{"resource_type"}},
						Right:    &ir.PhysicalValue{BindKey: "type_values"},
					},
				},
			}},
		))
		plan.BindVars["type_values"] = []string{"Specimen"}
		rendered := renderOK(t, plan)
		if !strings.Contains(rendered.SQL,
			`AND x1."resource_type" = ANY (SELECT jsonb_array_elements_text($7::jsonb))`) {
			t.Fatalf("typed column IN filter missing its canonical form: %s", rendered.SQL)
		}
	})
}

// TestRenderRequiredMatchChainedSteps registers multi-step matching: step
// k+1 anchors on step k's reached row, every step joins its own edge and
// target with full canonical scope, and all parameters dedupe globally.
func TestRenderRequiredMatchChainedSteps(t *testing.T) {
	plan := trvRequiredPlan(ir.PhysicalSubplan{
		Captures: []string{"root"},
		Operations: []ir.PhysicalOperation{
			trvSubplanTraversal("root", "specimen", "edge_label", "target_type"),
			trvSubplanTraversal("specimen", "observation", "edge_label_2", "target_type_2"),
		},
		Return: ir.PhysicalExpression{
			Kind:         ir.PhysicalValueExpression,
			Cardinality:  ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{BindKey: "required_match_result"},
		},
	})
	plan.BindVars["edge_label_2"] = "has-observation"
	plan.BindVars["target_type_2"] = "Observation"
	rendered := renderOK(t, plan)
	for _, want := range []string{
		`EXISTS (SELECT 1 FROM "pgir02_it_unit"."fhir_edge_occurrence" AS e1`,
		`INNER JOIN "pgir02_it_unit"."fhir_resource" AS x1 ON x1."project" = $1`,
		`INNER JOIN "pgir02_it_unit"."fhir_edge_occurrence" AS e2 ON e2."project" = $1` +
			` AND e2."dataset_generation" = $2` +
			` AND e2."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
			` AND e2."label" = $7 AND e2."source_resource_type" = $6` +
			` AND e2."source_logical_id" = x1."logical_id" AND e2."target_resource_type" = $8`,
		`INNER JOIN "pgir02_it_unit"."fhir_resource" AS x2 ON x2."project" = $1`,
		`AND x2."resource_type" = $8 AND x2."logical_id" = e2."target_logical_id"`,
		`AND e1."source_logical_id" = r0."logical_id"`,
	} {
		if !strings.Contains(rendered.SQL, want) {
			t.Fatalf("chained required-match SQL missing %q: %s", want, rendered.SQL)
		}
	}
	if len(rendered.Args) != 8 {
		t.Fatalf("args = %d, want 8 (project, generation, auth, Patient, label1, Specimen, label2, Observation)", len(rendered.Args))
	}
}

// ---------------------------------------------------------------------------
// Offline: the main-sequence guards over OPTIONAL rows (ticket §4).
// ---------------------------------------------------------------------------

func trvOptionalWithFilter(filter ir.PhysicalFilter) ir.PhysicalPlan {
	plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "has-specimen", "Specimen")
	returnOp := plan.Operations[len(plan.Operations)-1]
	plan.Operations = append(plan.Operations[:2], ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &filter}, returnOp)
	return plan
}

// TestMainSequenceRejectsOptionalRowReferences enforces the OPTIONAL
// boundary: a WHERE conjunct over null-extended child rows would re-add the
// inner-join exclusion the AQL LET-set never applies, so no FILTER leaf at
// any depth of a NOT/ALL composition may read an OPTIONAL traversal row.
// Required matching must use the typed EXISTS form, which stays accepted
// (it renders over INNER joins where WHERE placement is exact).
func TestMainSequenceRejectsOptionalRowReferences(t *testing.T) {
	optional := func(variable string) ir.PhysicalValue {
		return ir.PhysicalValue{Variable: variable, Path: []string{"logical_id"}}
	}
	comparison := func(variable string) ir.PhysicalPredicateExpression {
		return ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalComparisonPredicate,
			Comparison: &ir.PhysicalPredicate{
				Operator: "EQUALS", Left: optional(variable),
				Right: &ir.PhysicalValue{BindKey: "status_value"},
			},
		}
	}
	cases := []struct {
		name   string
		filter ir.PhysicalFilter
	}{
		{"predicate form over optional row",
			ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: optional("target"),
				Right: &ir.PhysicalValue{BindKey: "status_value"},
			}}},
		{"comparison leaf over optional row",
			ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
				Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
					Operator: "EQUALS", Left: optional("target"),
					Right: &ir.PhysicalValue{BindKey: "status_value"},
				},
			}}},
		{"NOT-composed leaf over optional row",
			ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
				Kind:     ir.PhysicalNotPredicate,
				Children: []ir.PhysicalPredicateExpression{comparison("target")},
			}}},
		{"ALL-composed leaf over optional row",
			ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
				Kind:     ir.PhysicalAllPredicate,
				Children: []ir.PhysicalPredicateExpression{comparison("root"), comparison("target")},
			}}},
		{"selector expression over optional row",
			*func() *ir.PhysicalFilter {
				filter := ir.PhysicalFilter{Expression: trvPayloadMatchPredicate("target", "Specimen", "EQUALS", spec.FilterString, "status")}
				return &filter
			}()},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := trvOptionalWithFilter(testCase.filter)
			plan.BindVars["status_value"] = "final"
			trvWantRefusal(t, plan, "FILTER references \"target\"")
		})
	}

	// Positive control: the same NOT-composed shape over the root row is
	// untouched by the guard (pre-existing FILTER surface unchanged).
	plan := trvOptionalWithFilter(ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
		Kind:     ir.PhysicalNotPredicate,
		Children: []ir.PhysicalPredicateExpression{comparison("root")},
	}})
	plan.BindVars["status_value"] = "final"
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `NOT (r0."logical_id" = $7)`) {
		t.Fatalf("root-row NOT filter degraded: %s", rendered.SQL)
	}
}

// SORT guard: ordering an expanded child row is not registered.
func TestMainSequenceRejectsSortOnOptionalRow(t *testing.T) {
	plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "has-specimen", "Specimen")
	returnOp := plan.Operations[len(plan.Operations)-1]
	plan.Operations = append(plan.Operations[:2], ir.PhysicalOperation{
		Kind: ir.PhysicalSortOp,
		Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "target", Path: []string{"logical_id"}}}},
	}, returnOp)
	trvWantRefusal(t, plan, "OPTIONAL traversal LEFT JOIN LATERAL")

	// Root-row sorting still renders (pre-existing surface unchanged).
	plan.Operations[2].Sort = &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}}
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, `ORDER BY r0."logical_id" ASC, r0."resource_seq" ASC, r1."edge_seq" ASC`) {
		t.Fatalf("root sort degraded: %s", rendered.SQL)
	}
}

// The typed EXISTS form stays accepted after an OPTIONAL traversal: the
// required match is a root-level semi-join and its own contract is enforced
// by renderRequiredMatchExists, so the FILTER guard does not walk into it.
func TestRequiredMatchAcceptedAlongsideOptionalTraversal(t *testing.T) {
	plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "has-specimen", "Specimen")
	subplan := trvRequiredSubplan(trvSubplanTraversal("root", "child", "edge_label", "target_type"))
	returnOp := plan.Operations[len(plan.Operations)-1]
	plan.Operations = append(plan.Operations[:2], ir.PhysicalOperation{
		Kind: ir.PhysicalFilterOp,
		Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalExistsPredicate, Exists: &subplan,
		}},
	}, returnOp)
	plan.BindVars["required_match_result"] = 1
	rendered := renderOK(t, plan)
	if !strings.Contains(rendered.SQL, "EXISTS (SELECT 1 FROM") ||
		!strings.Contains(rendered.SQL, "LEFT JOIN LATERAL") {
		t.Fatalf("combined OPTIONAL+REQUIRED plan missing a shape: %s", rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Offline: the closed refusal matrix (ticket §4, one row per refusal).
// ---------------------------------------------------------------------------

func trvWantRefusal(t *testing.T, plan ir.PhysicalPlan, wantIn string) {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	wantUnsupported(t, err, "traversal refusal")
	if err == nil || !strings.Contains(err.Error(), wantIn) {
		t.Fatalf("error = %v, want substring %q", err, wantIn)
	}
	if rendered.SQL != "" || len(rendered.Args) != 0 {
		t.Fatalf("refusal produced SQL or args: %s %#v", rendered.SQL, rendered.Args)
	}
}

func TestTraversalRefusalMatrix(t *testing.T) {
	mutate := func(plan ir.PhysicalPlan, edit func(*ir.PhysicalTraversal)) ir.PhysicalPlan {
		tr := *plan.Operations[1].Traversal
		edit(&tr)
		plan.Operations[1] = ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &tr}
		return plan
	}
	// badSubplan applies an edit to the canonical REQUIRED plan.
	badSubplan := func(edit func(*ir.PhysicalSubplan)) ir.PhysicalPlan {
		subplan := trvRequiredSubplan(trvSubplanTraversal("root", "child", "edge_label", "target_type"))
		edit(&subplan)
		return trvRequiredPlan(subplan)
	}
	badFirstStep := func(edit func(*ir.PhysicalOperation)) ir.PhysicalPlan {
		step := trvSubplanTraversal("root", "child", "edge_label", "target_type")
		edit(&step)
		return trvRequiredPlan(trvRequiredSubplan(step))
	}

	// rowsClosedByRenderer: every row is an ErrUnsupportedPostgresPlan
	// refusal from traversal.go with no SQL. RenderPhysicalPlan first runs
	// ir-plan validation (see the ir-gate rows below), so these plans are
	// IR-valid by construction.
	cases := []struct {
		name   string
		plan   ir.PhysicalPlan
		wantIn string
	}{
		// Contract: direction, strategy, discriminator, binds. The ir gate
		// accepts Arango-legal directions (including ANY) and native-or-""
		// strategies; the PostgreSQL contract is the renderer's to close.
		{"any direction", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.Direction = ir.PhysicalAny }),
			"ANY-direction traversal has no proven PostgreSQL contract"},
		{"outbound with from_type", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.EdgeTargetTypeField = "from_type" }),
			`must carry EdgeTargetTypeField "to_type"`},
		{"inbound with to_type", mutate(trvTraversalPlan(ir.PhysicalInbound, "from_type", "l", "Observation"),
			func(tr *ir.PhysicalTraversal) { tr.EdgeTargetTypeField = "to_type" }),
			`must carry EdgeTargetTypeField "from_type"`},
		{"missing edge variable", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.EdgeVariable = "" }),
			"must name its edge variable"},
		{"edge collection not fhir_edge", func() ir.PhysicalPlan {
			plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen")
			plan.BindVars["edge_collection"] = "fhir_edge_old"
			return plan
		}(), `must be bound to "fhir_edge"`},
		{"empty label bind", func() ir.PhysicalPlan {
			plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen")
			plan.BindVars["edge_label"] = "  "
			return plan
		}(), "edge label bind \"edge_label\" must be a non-empty string"},
		{"non-string label bind", func() ir.PhysicalPlan {
			plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen")
			plan.BindVars["edge_label"] = 42
			return plan
		}(), "edge label bind \"edge_label\" must be a non-empty string"},
		{"mismatched source type bind", func() ir.PhysicalPlan {
			plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen")
			plan.Operations[1].Traversal.SourceTypeBindKey = "source_type"
			plan.BindVars["source_type"] = "Observation"
			return plan
		}(), "traversal source type bind does not match the current row resource type"},

		// EXISTS subplan contract (renderer-closed rows).
		{"exists return reads a row", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Return.Value = &ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}
		}), "canonical scalar VALUE expression"},
		{"exists return array cardinality", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Return.Cardinality = ir.PhysicalArrayCardinality
		}), "SCALAR cardinality with PRESERVE_NULL"},
		{"exists return omit-nulls", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Return.NullBehavior = ir.PhysicalOmitNulls
		}), "SCALAR cardinality with PRESERVE_NULL"},
		{"exists return constant not one", func() ir.PhysicalPlan {
			plan := trvRequiredPlan(trvRequiredSubplan(trvSubplanTraversal("root", "child", "edge_label", "target_type")))
			plan.BindVars["required_match_result"] = 2
			return plan
		}(), "must be bound to the integer constant 1"},
		{"exists filter before traversal", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Operations = []ir.PhysicalOperation{{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
				Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
					Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
					Right: &ir.PhysicalValue{BindKey: "required_match_result"},
				}}},
			}}
		}), "a FILTER may only follow a traversal step"},
		{"exists derived-let scope block", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Operations = append(sp.Operations, ir.PhysicalOperation{
				Kind:       ir.PhysicalDerivedLetOp,
				DerivedLet: &ir.PhysicalDerivedLet{Variable: "scope", Operator: "UNIQUE"},
			})
		}), "plan-authored scope operations"},
		{"exists filter predicate form", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Operations = append(sp.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp,
				Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
					Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "child", Path: []string{"logical_id"}},
					Right: &ir.PhysicalValue{BindKey: "required_match_result"},
				}}})
		}), "must use the Expression form"},
		{"nested exists in subplan filter", badSubplan(func(sp *ir.PhysicalSubplan) {
			nested := ir.PhysicalSubplan{
				Captures: []string{"child"},
				Operations: []ir.PhysicalOperation{
					trvSubplanTraversal("child", "grand", "edge_label", "target_type"),
				},
				Return: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: "required_match_result"}},
			}
			sp.Operations = append(sp.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp,
				Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate, Exists: &nested}}})
		}), "not part of the registered traversal predicate surface"},
		{"any predicate", trvRequiredPlan(ir.PhysicalSubplan{
			Captures: []string{"root"},
			Operations: []ir.PhysicalOperation{
				trvSubplanTraversal("root", "child", "edge_label", "target_type"),
				{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
					Kind: ir.PhysicalAnyPredicate,
					Children: []ir.PhysicalPredicateExpression{{Kind: ir.PhysicalComparisonPredicate, Comparison: &ir.PhysicalPredicate{
						Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
						Right: &ir.PhysicalValue{BindKey: "required_match_result"},
					}}},
				}}},
			},
			Return: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{BindKey: "required_match_result"}},
		}), "not part of the registered traversal predicate surface"},
		{"subplan traversal discriminator drift", badFirstStep(func(op *ir.PhysicalOperation) {
			op.Traversal.EdgeTargetTypeField = "from_type"
		}), `must carry EdgeTargetTypeField "to_type"`},
		{"subplan traversal bad edge bind", func() ir.PhysicalPlan {
			plan := trvRequiredPlan(trvRequiredSubplan(trvSubplanTraversal("root", "child", "edge_label", "target_type")))
			plan.BindVars["edge_collection"] = "fhir_edge_old"
			return plan
		}(), `must be bound to "fhir_edge"`},
		{"subplan step 2 not anchored on step 1", func() ir.PhysicalPlan {
			plan := trvRequiredPlan(trvRequiredSubplan(
				trvSubplanTraversal("root", "specimen", "edge_label", "target_type"),
				trvSubplanTraversal("specimen", "observation", "edge_label_2", "target_type_2"),
			))
			plan.BindVars["edge_label_2"] = "has-observation"
			plan.BindVars["target_type_2"] = "Observation"
			// Step 2 must chain through step 1's reached row; anchoring on
			// the root instead fabricates a second independent hop.
			plan.Operations[1].Filter.Expression.Exists.Operations[1].Traversal.SourceVariable = "root"
			return plan
		}(), "must anchor on"},
	}

	// Rows the ir-plan gate closes ahead of the renderer
	// (physical_validation.go). These are equally closed typed rejections
	// that yield no SQL; the renderer-layer equivalents are pinned against
	// the unexported entry points in the probe tests below.
	irCases := []struct {
		name   string
		plan   ir.PhysicalPlan
		wantIn string
	}{
		{"unknown direction", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.Direction = ir.PhysicalTraversalDirection("SIDEWAYS") }),
			`invalid traversal direction "SIDEWAYS"`},
		{"endpoint lookup strategy", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.Strategy = ir.PhysicalTraversalEndpointLookup }),
			"endpoint lookup requires safe endpoint and join fields"},
		{"unknown strategy", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.Strategy = ir.PhysicalTraversalStrategy("HASH_JOIN") }),
			`unsupported traversal strategy "HASH_JOIN"`},
		{"missing edge collection bind key", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.EdgeCollectionBindKey = "" }),
			"traversal edge collection bind key is required"},
		{"edge collection bind missing", func() ir.PhysicalPlan {
			plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen")
			delete(plan.BindVars, "edge_collection")
			return plan
		}(), `bind key "edge_collection" is not defined`},
		{"missing target type bind", func() ir.PhysicalPlan {
			plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen")
			delete(plan.BindVars, "target_type")
			return plan
		}(), `bind key "target_type" is not defined`},
		{"source variable unbound", mutate(trvTraversalPlan(ir.PhysicalOutbound, "to_type", "l", "Specimen"),
			func(tr *ir.PhysicalTraversal) { tr.SourceVariable = "ghost" }),
			`traversal source variable "ghost" is out of scope`},
		{"exists without subplan payload", func() ir.PhysicalPlan {
			plan := basePlan()
			plan.Operations = []ir.PhysicalOperation{
				{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
				{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{
					Expression: &ir.PhysicalPredicateExpression{Kind: ir.PhysicalExistsPredicate},
				}},
				{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
					{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
				}}},
			}
			return plan
		}(), "EXISTS predicate requires exactly one subplan"},
		{"exists no captures", badSubplan(func(sp *ir.PhysicalSubplan) { sp.Captures = nil }),
			"at least one explicit capture"},
		{"exists two captures", badSubplan(func(sp *ir.PhysicalSubplan) { sp.Captures = []string{"root", "child"} }),
			`capture "child" is out of scope`},
		{"exists unknown capture", badSubplan(func(sp *ir.PhysicalSubplan) { sp.Captures = []string{"ghost"} }),
			`capture "ghost" is out of scope`},
		{"exists return is not a VALUE expression", trvRequiredPlan(ir.PhysicalSubplan{
			Captures: []string{"root"},
			Operations: []ir.PhysicalOperation{
				trvSubplanTraversal("root", "child", "edge_label", "target_type"),
			},
			Return: ir.PhysicalExpression{Kind: ir.PhysicalCallExpression},
		}), "expression has invalid cardinality"},
		{"exists return sort modifier", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Sort = &ir.PhysicalValue{Variable: "child", Path: []string{"logical_id"}}
		}), "projection-only sort or unique modifiers"},
		{"exists return bind undefined", func() ir.PhysicalPlan {
			plan := trvRequiredPlan(trvRequiredSubplan(trvSubplanTraversal("root", "child", "edge_label", "target_type")))
			delete(plan.BindVars, "required_match_result")
			return plan
		}(), `bind key "required_match_result" is not defined`},
		{"exists no operations", badSubplan(func(sp *ir.PhysicalSubplan) { sp.Operations = nil }),
			"subplan requires at least one operation"},
		{"exists expression-let", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Operations = append(sp.Operations, ir.PhysicalOperation{
				Kind: ir.PhysicalExpressionLetOp,
				ExpressionLet: &ir.PhysicalExpressionLet{Variable: "x", Expression: ir.PhysicalExpression{
					Kind:         ir.PhysicalValueExpression,
					Cardinality:  ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull,
					Value:        &ir.PhysicalValue{BindKey: "required_match_result"},
				}},
			})
		}), `subplan operation 1 has unsupported kind "EXPRESSION_LET"`},
		{"exists sort op", badSubplan(func(sp *ir.PhysicalSubplan) {
			sp.Operations = append(sp.Operations, ir.PhysicalOperation{
				Kind: ir.PhysicalSortOp,
				Sort: &ir.PhysicalSort{Keys: []ir.PhysicalValue{{Variable: "child", Path: []string{"logical_id"}}}},
			})
		}), "subplan operation 1 cannot be SORT"},
		{"exists missing traversal payload", badFirstStep(func(op *ir.PhysicalOperation) { op.Traversal = nil }),
			"operation must contain exactly one payload"},
		{"exists traversal not anchored on capture", badFirstStep(func(op *ir.PhysicalOperation) {
			op.Traversal.SourceVariable = "someone-else"
		}), `traversal source variable "someone-else" is out of scope`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) { trvWantRefusal(t, testCase.plan, testCase.wantIn) })
	}
	for _, testCase := range irCases {
		t.Run("ir-gate/"+testCase.name, func(t *testing.T) { trvWantClosedRejection(t, testCase.plan, testCase.wantIn) })
	}
}

// trvWantClosedRejection asserts the same closed-surface properties as
// trvWantRefusal (typed non-nil error naming the boundary, zero SQL, zero
// arguments) for rows where the closing layer is the ir-plan gate rather
// than the renderer.
func trvWantClosedRejection(t *testing.T, plan ir.PhysicalPlan, wantIn string) {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil || !strings.Contains(err.Error(), wantIn) {
		t.Fatalf("error = %v, want closed rejection %q", err, wantIn)
	}
	if rendered.SQL != "" || len(rendered.Args) != 0 {
		t.Fatalf("rejection produced SQL or args: %s %#v", rendered.SQL, rendered.Args)
	}
}

// ---------------------------------------------------------------------------
// Offline: the REQUIRED-match selector filter — renderer-layer surface.
//
// RenderPhysicalPlan runs ir-plan validation before the renderer, so a
// malformed selector can be closed at either layer; both are closed
// surfaces. These probes drive renderSelectorMatchFragment directly so
// every renderer-layer refusal and every registered form is pinned
// independently of the plan gate.
// ---------------------------------------------------------------------------

func trvSelectorProbe(t *testing.T, edit func(*ir.PhysicalExtract, *ir.PhysicalExpression, *ir.PhysicalPredicate)) (string, error) {
	t.Helper()
	extract := &ir.PhysicalExtract{
		Source:       ir.PhysicalValue{Variable: "child", Path: []string{"payload"}},
		ResourceType: "Specimen",
		Selector:     spec.Selector{Steps: []spec.SelectorStep{{Field: "status"}}},
	}
	expression := &ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull,
		Extract:      extract,
	}
	predicate := ir.PhysicalPredicate{
		Operator: "EQUALS", ValueKind: spec.FilterString,
		LeftExpression: expression,
		Right:          &ir.PhysicalValue{BindKey: "status_value"},
	}
	if edit != nil {
		edit(extract, expression, &predicate)
	}
	plan := basePlan()
	plan.BindVars["status_value"] = "final"
	plan.BindVars["array_value"] = []any{"final", "prelim"}
	plan.BindVars["date_value"] = "2024-01-01T00:00:00Z"
	plan.BindVars["count_value"] = int64(7)
	r := &planRenderer{
		storage:    mustBinding(t),
		parameters: map[string]int{},
		rows: map[string]rowScope{
			"root":  {alias: "r0", resourceType: "Patient"},
			"child": {alias: "x1", resourceType: "Specimen"},
		},
	}
	fragment, err := r.renderSelectorMatchFragment(plan, predicate, r.rows)
	if err != nil {
		return "", err
	}
	return fragment.sql, nil
}

func trvSelectorRefusal(t *testing.T, name string, edit func(*ir.PhysicalExtract, *ir.PhysicalExpression, *ir.PhysicalPredicate), wantIn string) {
	t.Helper()
	t.Run(name, func(t *testing.T) {
		sql, err := trvSelectorProbe(t, edit)
		if !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Fatalf("error = %v (%T), want ErrUnsupportedPostgresPlan", err, err)
		}
		if err == nil || !strings.Contains(err.Error(), wantIn) {
			t.Fatalf("error = %v, want substring %q", err, wantIn)
		}
		if sql != "" {
			t.Fatalf("rejection emitted SQL: %s", sql)
		}
	})
}

func TestRequiredMatchSelectorFilterRefusals(t *testing.T) {
	indexZero := 0
	// Selector-shape refusals (defense-in-depth beside the ir plan gate).
	trvSelectorRefusal(t, "wrong expression kind", func(_ *ir.PhysicalExtract, e *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		e.Kind = ir.PhysicalCallExpression
	}, "must be an EXTRACT selector expression")
	trvSelectorRefusal(t, "missing extract payload", func(x *ir.PhysicalExtract, e *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		e.Extract = nil
		_ = x
	}, "missing its EXTRACT payload")
	trvSelectorRefusal(t, "scalar cardinality", func(_ *ir.PhysicalExtract, e *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		e.Cardinality = ir.PhysicalScalarCardinality
	}, "must be the ARRAY-cardinality form")
	trvSelectorRefusal(t, "preserve-null behavior", func(_ *ir.PhysicalExtract, e *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		e.NullBehavior = ir.PhysicalPreserveNull
	}, "must carry the EMPTY_ON_NULL null behavior")
	trvSelectorRefusal(t, "unknown null behavior", func(_ *ir.PhysicalExtract, e *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		e.NullBehavior = ir.PhysicalNullBehavior("SILENT")
	}, "must carry the EMPTY_ON_NULL null behavior")
	trvSelectorRefusal(t, "conditional array mode", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.ExecutionMode = ir.PhysicalSelectorConditionalArray
	}, "CONDITIONAL_ARRAY")
	trvSelectorRefusal(t, "unknown execution mode", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.ExecutionMode = ir.PhysicalSelectorExecutionMode("RAW_SQL")
	}, "execution mode")
	trvSelectorRefusal(t, "distinct", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Distinct = true
	}, "DISTINCT changes element multiplicity")
	trvSelectorRefusal(t, "fallbacks", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Fallbacks = []ir.PhysicalSelectorFallback{{Source: x.Source, Selector: x.Selector}}
	}, "fallbacks are a separate capability")
	trvSelectorRefusal(t, "prepared reference", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Prepared = &ir.PhysicalPreparedReference{SetVariable: "s", Field: "f"}
	}, "prepared-set references")
	trvSelectorRefusal(t, "unit normalization", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.UnitNormalization = &ir.PhysicalUnitNormalization{}
	}, "unit normalization is not implemented")
	trvSelectorRefusal(t, "selector CONTAINS filter", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Selector.Filter = &spec.ContainsFilter{}
	}, "CONTAINS filters are not implemented")
	trvSelectorRefusal(t, "empty selector", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Selector = spec.Selector{}
	}, "selector is empty")
	trvSelectorRefusal(t, "step without property", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: ""}}}
	}, "has no property")
	trvSelectorRefusal(t, "iterate step", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "status", Iterate: true}}}
	}, "repeated ([]) step")
	trvSelectorRefusal(t, "indexed step", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "status", Index: &indexZero}}}
	}, "indexed step")
	trvSelectorRefusal(t, "unsafe field", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Selector = spec.Selector{Steps: []spec.SelectorStep{{Field: "bad field"}}}
	}, "not renderer-safe")
	trvSelectorRefusal(t, "source bind", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Source = ir.PhysicalValue{BindKey: "status_value"}
	}, "source must be a row document, not a bind value")
	trvSelectorRefusal(t, "source path not payload", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Source = ir.PhysicalValue{Variable: "child", Path: []string{"payload", "status"}}
	}, `source must be the row document at ["payload"]`)
	trvSelectorRefusal(t, "unknown variable", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.Source = ir.PhysicalValue{Variable: "ghost", Path: []string{"payload"}}
	}, "does not bind a readable subplan row")
	trvSelectorRefusal(t, "missing resource type", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.ResourceType = "  "
	}, "must name the schema resource type")
	trvSelectorRefusal(t, "resource type mismatch", func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression, _ *ir.PhysicalPredicate) {
		x.ResourceType = "Observation"
	}, "but the row is joined as")

	// Operator and value refusals.
	trvSelectorRefusal(t, "contains_text gap", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Operator = "CONTAINS_TEXT"
	}, "CONTAINS_TEXT")
	// DATE/DATE_TIME are registered through the shared selector match
	// (predicates.go selectorMatchExpression): equality compares the jsonb
	// item, ordered comparisons take the temporal timestamptz form. They
	// are covered as registered forms below, not as gaps.
	trvSelectorRefusal(t, "unknown operator", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Operator = "REGEX"
	}, "not part of the v1 PostgreSQL surface")
	trvSelectorRefusal(t, "LTE against STRING is not spec-orderable", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Operator = "LTE"
	}, "not compatible with value kind")
	trvSelectorRefusal(t, "invalid value kind", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.ValueKind = spec.FilterValueKind("JSON")
	}, "value kind")
	trvSelectorRefusal(t, "unknown quantifier", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Quantifier = spec.ArrayQuantifier("MANY")
	}, "array quantifier")
	trvSelectorRefusal(t, "scalar bind for IN", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Operator = "IN"
	}, "requires a []any or []string bind value")
	trvSelectorRefusal(t, "array bind for EQUALS", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Right = &ir.PhysicalValue{BindKey: "array_value"}
	}, "requires a string bind value")
	trvSelectorRefusal(t, "missing right value", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Right = nil
	}, "requires a bind-backed right value")
	trvSelectorRefusal(t, "undefined right bind", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Right = &ir.PhysicalValue{BindKey: "not-defined"}
	}, "is not defined")
	trvSelectorRefusal(t, "exists with right value", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Operator = "EXISTS"
	}, "EXISTS must not carry a right value")
	trvSelectorRefusal(t, "correlated left expression", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
		p.Correlation = &ir.PhysicalCorrelation{ResourceType: "Specimen"}
	}, "correlated predicates")
}

func TestRequiredMatchSelectorFilterRegisteredForms(t *testing.T) {
	// The probe's parameter counter starts empty: path binds $1, value $2.
	const valuesPin = `(CASE WHEN (x1."payload" #> $1::text[]) IS NULL OR jsonb_typeof((x1."payload" #> $1::text[])) = 'null' THEN '[]'::jsonb WHEN jsonb_typeof((x1."payload" #> $1::text[])) = 'array' THEN COALESCE((SELECT jsonb_agg(__elem.e) FROM jsonb_array_elements((x1."payload" #> $1::text[])) AS __elem(e) WHERE __elem.e <> 'null'::jsonb), '[]'::jsonb) ELSE jsonb_build_array((x1."payload" #> $1::text[])) END)`
	cases := []struct {
		name string
		edit func(*ir.PhysicalExtract, *ir.PhysicalExpression, *ir.PhysicalPredicate)
		want string
	}{
		{"any default quantifier", nil, "(EXISTS (SELECT 1 FROM jsonb_array_elements(" + valuesPin + ") AS __sv(v) WHERE (__sv.v) = ($2::jsonb)))"},
		{"explicit ANY", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Quantifier = spec.QuantifierAny
		}, "(EXISTS (SELECT 1 FROM jsonb_array_elements(" + valuesPin + ") AS __sv(v) WHERE (__sv.v) = ($2::jsonb)))"},
		{"NONE quantifier", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Quantifier = spec.QuantifierNone
		}, "(NOT EXISTS (SELECT 1 FROM jsonb_array_elements(" + valuesPin + ") AS __sv(v) WHERE (__sv.v) = ($2::jsonb)))"},
		{"ALL quantifier", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Quantifier = spec.QuantifierAll
		}, "(jsonb_array_length(" + valuesPin + ") > 0 AND NOT EXISTS (SELECT 1 FROM jsonb_array_elements(" + valuesPin + ") AS __sv(v) WHERE NOT ((__sv.v) = ($2::jsonb))))"},
		{"NOT_EQUALS", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Operator = "NOT_EQUALS"
		}, "(__sv.v) <> ($2::jsonb)"},
		{"IN membership", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Operator = "IN"
			p.Right = &ir.PhysicalValue{BindKey: "array_value"}
		}, "EXISTS (SELECT 1 FROM jsonb_array_elements($2::jsonb) AS loom_selector_item WHERE loom_selector_item = (__sv.v))"},
		{"LTE ordering (INTEGER, numeric guard)", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Operator = "LTE"
			p.ValueKind = spec.FilterInteger
			p.Right = &ir.PhysicalValue{BindKey: "count_value"}
		}, "COALESCE(jsonb_typeof(__sv.v) = 'number' AND (__sv.v) <= ($2::jsonb), false)"},
		{"DATE equality", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.ValueKind = spec.FilterDate
			p.Right = &ir.PhysicalValue{BindKey: "date_value"}
		}, "(__sv.v) = ($2::jsonb)"},
		{"DATE_TIME ordering takes the temporal form", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Operator = "LTE"
			p.ValueKind = spec.FilterDateTime
			p.Right = &ir.PhysicalValue{BindKey: "date_value"}
		}, ") <= $2, false)"},
		{"EXISTS emptiness", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Operator = "EXISTS"
			p.Right = nil
		}, "(jsonb_array_length(" + valuesPin + ") > 0)"},
		{"MISSING emptiness", func(_ *ir.PhysicalExtract, _ *ir.PhysicalExpression, p *ir.PhysicalPredicate) {
			p.Operator = "MISSING"
			p.Right = nil
		}, "(jsonb_array_length(" + valuesPin + ") = 0)"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			sql, err := trvSelectorProbe(t, testCase.edit)
			if err != nil {
				t.Fatalf("registered form refused: %v", err)
			}
			if !strings.Contains(sql, testCase.want) {
				t.Fatalf("fragment missing %q:\n%s", testCase.want, sql)
			}
		})
	}
}

// TestTraversalRenderingIsDeterministicAndPlanDataInert renders the same
// plan twice and pushes hostile plan data through every bind position.
func TestTraversalRenderingIsDeterministicAndPlanDataInert(t *testing.T) {
	plan := trvTraversalPlan(ir.PhysicalOutbound, "to_type", `x'; DROP TABLE fhir_resource; --`, `"injected";$3`)
	first := renderOK(t, plan)
	second := renderOK(t, plan)
	if first.SQL != second.SQL || !reflect.DeepEqual(first.Args, second.Args) {
		t.Fatalf("renders drifted:\n%s\n%s", first.SQL, second.SQL)
	}
	for _, hostile := range []string{"DROP TABLE", `injected`, "\"injected\""} {
		if strings.Contains(first.SQL, hostile) {
			t.Fatalf("hostile plan data %q reached SQL text: %s", hostile, first.SQL)
		}
	}
	foundLabel := false
	for _, parameter := range first.Args {
		if text, ok := parameter.Value.(string); ok && strings.Contains(text, "DROP TABLE") {
			foundLabel = true
		}
	}
	if !foundLabel {
		t.Fatal("hostile label never reached the parameter list; it was dropped, not bound")
	}
}

// ---------------------------------------------------------------------------
// Live: PostgreSQL 18 behavior — OPTIONAL parent preservation.
// ---------------------------------------------------------------------------

// trvRunDeltaAudit wraps m.Run with this ticket's fixture-hygiene audit,
// delta-scoped to this run (ticket condition C3): it snapshots pgir02_it_*
// databases and schemas before the run, and after the run fails only on
// fixtures this run's own tests created and tracked (so concurrent sibling
// lanes can neither frame this run nor hide leaks from it). Resources born
// during the run but untracked are reported to stderr without failing,
// because they may belong to a sibling still mid-run. Nothing is ever
// deleted here. The package TestMain (main_test.go) composes this with the
// package-wide zero-leftover audits.
func trvRunDeltaAudit(m *testing.M) int {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		return m.Run()
	}
	beforeList, err := trvSnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-06 delta audit: snapshot before run: %v\n", err)
		return 1
	}
	before := make(map[string]bool, len(beforeList))
	for _, name := range beforeList {
		before[name] = true
	}
	code := m.Run()
	after, err := trvSnapshotOwnedResources(context.Background(), raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "pg-sql-06 delta audit: snapshot after run: %v\n", err)
		code = 1
	}
	trvFixturesMu.Lock()
	defer trvFixturesMu.Unlock()
	for _, name := range after {
		if before[name] {
			continue
		}
		if trvTrackedFixtures[name] {
			fmt.Fprintf(os.Stderr, "pg-sql-06 delta audit: %q was created and tracked by this run and still survives\n", name)
			code = 1
			continue
		}
		fmt.Fprintf(os.Stderr, "pg-sql-06 delta audit: note — %q was born during this run but is not owned by it (sibling lane?)\n", name)
	}
	return code
}

var (
	trvFixturesMu      sync.Mutex
	trvTrackedFixtures = map[string]bool{}
)

// trvTrackFixtures records the database and schema one live test owns, so
// the TestMain delta audit can attribute survivors to this run exactly.
func trvTrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	trvFixturesMu.Lock()
	defer trvFixturesMu.Unlock()
	trvTrackedFixtures[database] = true
	trvTrackedFixtures[svc.schema] = true
}

func trvSnapshotOwnedResources(ctx context.Context, raw string) ([]string, error) {
	pool, err := pgxpool.New(ctx, raw)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var names []string
	for _, query := range []string{
		`SELECT datname FROM pg_database WHERE datname LIKE 'pgir02\_it\_%' ESCAPE '\'`,
		`SELECT nspname FROM pg_namespace WHERE nspname LIKE 'pgir02\_it\_%' ESCAPE '\'`,
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

func trvLiveTraversalPlan(rootType string, direction ir.PhysicalTraversalDirection, edgeTargetTypeField, label, targetType string, paths []string) ir.PhysicalPlan {
	plan := scopedPlan(itProject, itGeneration, paths, false)
	plan.BindVars["root_collection"] = rootType
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = label
	plan.BindVars["target_type"] = targetType
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{
		Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: "target", EdgeVariable: "edge",
			Direction: direction, EdgeCollectionBindKey: "edge_collection",
			EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
			EdgeTargetTypeField: edgeTargetTypeField, Strategy: ir.PhysicalTraversalNative,
		},
	})
	return withReturn(plan,
		ir.PhysicalProjection{Name: "parent_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		ir.PhysicalProjection{Name: "child_id", Value: ir.PhysicalValue{Variable: "target", Path: []string{"logical_id"}}},
	)
}

// TestPG18OptionalTraversalPreservesParentRows exercises every OPTIONAL
// defect class against the live server: duplicate occurrences must survive
// (no multiplicity collapse), resolved-plus-dangling parents keep only the
// resolved rows, and parents with only-dangling, no, or wrong-label edges
// collapse to exactly one row with NULL child columns — never an
// inner-join-style exclusion.
func TestPG18OptionalTraversalPreservesParentRows(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	trvTrackFixtures(t, svc)
	for _, id := range []string{"pa", "pb", "pc", "pd", "pe"} {
		svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", id, "FHIR/Patient", nil)
	}
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-1", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-2", "FHIR/Specimen", nil)
	edge := func(label, source, target, path string) {
		svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, label, source, target, path))
	}
	// pa: two occurrences of st-1, one st-2, plus a dangling ghost — three
	// surviving rows and the ghost must leave no row of its own.
	edge("has-specimen", "pa", "st-1", "specimen[0]")
	edge("has-specimen", "pa", "st-1", "specimen[1]")
	edge("has-specimen", "pa", "st-2", "contained[0].specimen")
	edge("has-specimen", "pa", "st-ghost", "specimen[9]")
	// pb: one resolved occurrence.
	edge("has-specimen", "pb", "st-2", "specimen[0]")
	// pc: only a dangling occurrence — exactly one NULL-extended row.
	edge("has-specimen", "pc", "st-ghost", "specimen[0]")
	// pd: no edges at all — exactly one NULL-extended row.
	// pe: only a wrong-label occurrence — excluded by label, one NULL row.
	edge("other-label", "pe", "st-1", "specimen[0]")

	plan := trvLiveTraversalPlan("Patient", ir.PhysicalOutbound, "to_type", "has-specimen", "Specimen",
		[]string{"FHIR/Patient", "FHIR/Specimen"})
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	got := svc.runRendered(t, rendered)
	want := [][]string{
		{"pa", "st-1"}, {"pa", "st-1"}, {"pa", "st-2"},
		{"pb", "st-2"},
		{"pc", "<null>"},
		{"pd", "<null>"},
		{"pe", "<null>"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OPTIONAL traversal rows = %v, want parent-preserving matrix %v", got, want)
	}

	stored := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2 AND label='has-specimen'`,
		itProject, itGeneration)
	if len(stored) != 1 || stored[0][0] != "6" {
		t.Fatalf("witness must show all six stored occurrences (two ghosts included): %v", stored)
	}
	nullParents := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" r0`+
			` WHERE r0.project=$1 AND r0.dataset_generation=$2 AND r0.resource_type='Patient' AND r0.auth_resource_path='FHIR/Patient'`+
			` AND NOT EXISTS (SELECT 1 FROM "`+svc.schema+`"."fhir_edge_occurrence" e1`+
			` INNER JOIN "`+svc.schema+`"."fhir_resource" x1 ON x1.project=e1.project AND x1.dataset_generation=e1.dataset_generation`+
			` AND x1.auth_resource_path='FHIR/Specimen' AND x1.resource_type='Specimen' AND x1.logical_id=e1.target_logical_id`+
			` WHERE e1.project=$1 AND e1.dataset_generation=$2 AND e1.auth_resource_path='FHIR/Patient'`+
			` AND e1.label='has-specimen' AND e1.source_resource_type='Patient' AND e1.source_logical_id=r0.logical_id`+
			` AND e1.target_resource_type='Specimen')`,
		itProject, itGeneration)
	if len(nullParents) != 1 || nullParents[0][0] != "3" {
		t.Fatalf("witness parent count with zero surviving occurrences = %v, want 3 (pc, pd, pe)", nullParents)
	}
}

// ---------------------------------------------------------------------------
// Live: PostgreSQL 18 behavior — REQUIRED match exclusion.
// ---------------------------------------------------------------------------

func trvLiveRequiredPlan(rootType, label, targetType string, paths []string, direction ir.PhysicalTraversalDirection, edgeTargetTypeField string, subplanFilter *ir.PhysicalPredicateExpression) ir.PhysicalPlan {
	plan := scopedPlan(itProject, itGeneration, paths, false)
	plan.BindVars["root_collection"] = rootType
	plan.BindVars["edge_collection"] = "fhir_edge"
	plan.BindVars["edge_label"] = label
	plan.BindVars["target_type"] = targetType
	plan.BindVars["required_match_result"] = 1
	step := trvSubplanTraversal("root", "child", "edge_label", "target_type")
	step.Traversal.Direction = direction
	step.Traversal.EdgeTargetTypeField = edgeTargetTypeField
	operations := []ir.PhysicalOperation{step}
	if subplanFilter != nil {
		operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp,
			Filter: &ir.PhysicalFilter{Expression: subplanFilter}})
	}
	subplan := trvRequiredSubplan(operations...)
	plan.Operations = append(plan.Operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp,
		Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalExistsPredicate, Exists: &subplan,
		}}})
	return withReturn(plan,
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
}

// TestPG18RequiredMatchExcludesUnmatchedParents proves the REQUIRED
// exclusion side the OPTIONAL join never performs: a parent matches only
// through a stored occurrence whose target row exists under the right type
// and label. Wrong-label and dangling witnesses are proven stored first, so
// their exclusion is the join's doing, not missing data.
func TestPG18RequiredMatchExcludesUnmatchedParents(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	trvTrackFixtures(t, svc)
	for _, id := range []string{"p-a", "p-b", "p-c", "p-d"} {
		svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", id, "FHIR/Patient", nil)
	}
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-1", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-9", "FHIR/Specimen", nil)
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "has-specimen", "p-a", "st-1", "specimen[0]"))
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "wrong-label", "p-c", "st-9", "specimen[0]"))
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "has-specimen", "p-d", "st-ghost", "specimen[0]"))

	plan := trvLiveRequiredPlan("Patient", "has-specimen", "Specimen",
		[]string{"FHIR/Patient", "FHIR/Specimen"}, ir.PhysicalOutbound, "to_type", nil)
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	got := svc.runRendered(t, rendered)
	if want := [][]string{{"p-a"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("REQUIRED rows = %v, want %v", got, want)
	}

	// Witnesses: the excluded parents' occurrences are stored; only the
	// join excludes them.
	stored := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2 AND label='wrong-label' AND source_logical_id='p-c'`,
		itProject, itGeneration)
	if len(stored) != 1 || stored[0][0] != "1" {
		t.Fatalf("wrong-label witness occurrence must be stored: %v", stored)
	}
	ghost := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2 AND label='has-specimen' AND source_logical_id='p-d' AND target_logical_id='st-ghost'`,
		itProject, itGeneration)
	if len(ghost) != 1 || ghost[0][0] != "1" {
		t.Fatalf("dangling witness occurrence must be stored: %v", ghost)
	}

	// Combined OPTIONAL + REQUIRED: the semi-join filters parents; the
	// lateral still expands every surviving occurrence of kept parents.
	optionalPlan := trvLiveTraversalPlan("Patient", ir.PhysicalOutbound, "to_type", "has-specimen", "Specimen",
		[]string{"FHIR/Patient", "FHIR/Specimen"})
	subplan := trvRequiredSubplan(trvSubplanTraversal("root", "child", "edge_label", "target_type"))
	returnOp := optionalPlan.Operations[len(optionalPlan.Operations)-1]
	optionalPlan.BindVars["required_match_result"] = 1
	optionalPlan.Operations = append(optionalPlan.Operations[:2], ir.PhysicalOperation{
		Kind: ir.PhysicalFilterOp,
		Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalExistsPredicate, Exists: &subplan,
		}},
	}, returnOp)
	combined, err := RenderPhysicalPlan(optionalPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("combined RenderPhysicalPlan: %v", err)
	}
	got = svc.runRendered(t, combined)
	if want := [][]string{{"p-a", "st-1"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("combined OPTIONAL+REQUIRED rows = %v, want %v", got, want)
	}
}

// TestPG18RequiredMatchWithTypedPayloadFilter runs the REQUIRED match with
// the subplan-local typed payload selector filter (status EQUALS final) and
// proves the filter prunes inside the semi-join only: a patient whose only
// specimen is preliminary is excluded, a patient with both survives, and in
// the combined OPTIONAL+REQUIRED plan the lateral still expands every
// occurrence (the match is at parent level, not row level).
func TestPG18RequiredMatchWithTypedPayloadFilter(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	trvTrackFixtures(t, svc)
	for _, id := range []string{"p-a", "p-b", "p-c", "p-d"} {
		svc.insertResource(t, svc.schema, itProject, itGeneration, "Patient", id, "FHIR/Patient", nil)
	}
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-f", "FHIR/Specimen", map[string]any{"status": "final"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-p", "FHIR/Specimen", map[string]any{"status": "prelim"})
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-m", "FHIR/Specimen", nil)
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "has-specimen", "p-a", "st-f", "specimen[0]"))
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "has-specimen", "p-b", "st-p", "specimen[0]"))
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "has-specimen", "p-c", "st-m", "specimen[0]"))
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "has-specimen", "p-d", "st-f", "specimen[0]"))
	svc.insertEdge(t, pgstoreEdgeRow(itProject, itGeneration, "has-specimen", "p-d", "st-p", "specimen[1]"))

	payloadFilter := trvPayloadMatchPredicate("child", "Specimen", "EQUALS", spec.FilterString, "status")
	plan := trvLiveRequiredPlan("Patient", "has-specimen", "Specimen",
		[]string{"FHIR/Patient", "FHIR/Specimen"}, ir.PhysicalOutbound, "to_type", payloadFilter)
	plan.BindVars["status_value"] = "final"
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	got := svc.runRendered(t, rendered)
	if want := [][]string{{"p-a"}, {"p-d"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("typed payload REQUIRED rows = %v, want %v", got, want)
	}

	// Direct-SQL witness over the same stored state.
	witness := svc.witness(t,
		`SELECT r0.logical_id FROM "`+svc.schema+`"."fhir_resource" r0`+
			` WHERE r0.project=$1 AND r0.dataset_generation=$2 AND r0.resource_type='Patient' AND r0.auth_resource_path='FHIR/Patient'`+
			` AND EXISTS (SELECT 1 FROM "`+svc.schema+`"."fhir_edge_occurrence" e1`+
			` INNER JOIN "`+svc.schema+`"."fhir_resource" x1 ON x1.project=e1.project AND x1.dataset_generation=e1.dataset_generation`+
			` AND x1.auth_resource_path='FHIR/Specimen' AND x1.resource_type='Specimen' AND x1.logical_id=e1.target_logical_id`+
			` WHERE e1.project=$1 AND e1.dataset_generation=$2 AND e1.auth_resource_path='FHIR/Patient'`+
			` AND e1.label='has-specimen' AND e1.source_resource_type='Patient' AND e1.source_logical_id=r0.logical_id`+
			` AND e1.target_resource_type='Specimen' AND x1.payload->>'status'='final')`+
			` ORDER BY r0.resource_seq`,
		itProject, itGeneration)
	if !reflect.DeepEqual(witness, [][]string{{"p-a"}, {"p-d"}}) {
		t.Fatalf("typed payload witness = %v", witness)
	}
}

// ---------------------------------------------------------------------------
// Live: PostgreSQL 18 behavior — INBOUND matrices.
// ---------------------------------------------------------------------------

// TestPG18InboundTraversalOptionalAndRequired runs both modes on the
// stored-source side: INBOUND OPTIONAL from a Specimen reaches the
// Observations that reference it (with parent preservation for unreferenced
// and dangling-only specimens), and INBOUND REQUIRED keeps only specimens
// that carry at least one resolved, correctly labeled incoming occurrence.
func TestPG18InboundTraversalOptionalAndRequired(t *testing.T) {
	svc := newPGIR02Service(t, 0)
	trvTrackFixtures(t, svc)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-1", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-2", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-3", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Specimen", "st-4", "FHIR/Specimen", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Observation", "o-1", "FHIR/Observation", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Observation", "o-2", "FHIR/Observation", nil)
	svc.insertResource(t, svc.schema, itProject, itGeneration, "Observation", "o-3", "FHIR/Observation", nil)
	inbound := func(label, observation, specimen, path string) {
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: itProject, DatasetGeneration: itGeneration, AuthResourcePath: "FHIR/Observation",
			Label: label, SourceResourceType: "Observation", SourceLogicalID: observation,
			TargetResourceType: "Specimen", TargetLogicalID: specimen, SourcePath: path, SourceOrdinal: 0,
		})
	}
	inbound("focus", "o-1", "st-1", "focus[0]")
	inbound("focus", "o-2", "st-1", "focus[0]")
	inbound("focus", "o-3", "st-2", "focus[0]")
	// st-4's only incoming occurrence names a source that was never
	// stored: the reached-side INNER JOIN must exclude it, leaving st-4
	// parent-preserved with a NULL child.
	trvInsertDanglingSourceEdge(t, svc, "focus", "Observation", "o-ghost", "Specimen", "st-4", "FHIR/Observation", "focus[0]")
	// st-3: no incoming edges. A wrong-label decoy on st-3 proves exclusion.
	inbound("other", "o-1", "st-3", "focus[1]")

	paths := []string{"FHIR/Specimen", "FHIR/Observation"}
	optionalPlan := trvLiveTraversalPlan("Specimen", ir.PhysicalInbound, "from_type", "focus", "Observation", paths)
	rendered, err := RenderPhysicalPlan(optionalPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("inbound OPTIONAL render: %v", err)
	}
	got := svc.runRendered(t, rendered)
	want := [][]string{
		{"st-1", "o-1"}, {"st-1", "o-2"},
		{"st-2", "o-3"},
		{"st-3", "<null>"},
		{"st-4", "<null>"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inbound OPTIONAL rows = %v, want %v", got, want)
	}

	requiredPlan := trvLiveRequiredPlan("Specimen", "focus", "Observation", paths, ir.PhysicalInbound, "from_type", nil)
	rendered, err = RenderPhysicalPlan(requiredPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("inbound REQUIRED render: %v", err)
	}
	got = svc.runRendered(t, rendered)
	if want := [][]string{{"st-1"}, {"st-2"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inbound REQUIRED rows = %v, want %v", got, want)
	}

	stored := svc.witness(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_edge_occurrence" WHERE project=$1 AND dataset_generation=$2 AND label='focus' AND target_logical_id='st-4'`,
		itProject, itGeneration)
	if len(stored) != 1 || stored[0][0] != "1" {
		t.Fatalf("dangling inbound witness occurrence must be stored: %v", stored)
	}
}
