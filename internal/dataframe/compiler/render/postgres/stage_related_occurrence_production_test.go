package postgres

// L5 live leg for the G3(c) RelatedOccurrence group adapter (plan §5 G3(c),
// §8 S9, §9): the route-sourced Coding key executed on real PostgreSQL 18
// over a real fhir_edge_occurrence hop.
//
// Scope of this file, stated plainly: the composition door DOES dispatch an
// adapter-bearing GROUP stage (the renderStageGroup dispatch arm, integrated
// 2026-11-11), and this leg additionally drives the registered adapter
// region directly against real storage so the row-level laws are witnessed
// independent of the chain. The plan/stage shape is the one
// lower/recipe_construction_related_expand.go and
// lower/group_related_occurrence_lower.go mint, carried by the shared offline
// fixture (stage_related_occurrence_test.go), with the live binding and the
// prior stage relation standing in as an owned view. The production-compiled
// P4 leg (BuildRecipePlan → ResolveRecipePlan → CompileResolvedRecipePlan with
// zero hand-shaping) is unblocked by the arm and stays a separate, explicit
// claim — it is NOT claimed here.
//
// What this leg proves on real rows:
//   - the Coding array is read AT the iterated selector step
//     ({valueCodeableConcept,coding}), not the container above it: a wrong
//     array path would silently yield an empty Coding array on real data;
//   - the occurrence key (root_id, terminal_id, item_present, ordinal) and
//     the two has-item witnesses are the observable columns, in the total order;
//   - the CodingUnnest empty policy (EXCLUDE vs PRESERVE_PARENT) changes the
//     row set the way construction_group.go:544-554 does;
//   - the route ERROR policy raises through the registered guard tag;
//   - auth scope drops a denied target and a denied anchor, and two edge
//     occurrences to one target dedup to one reached target;
//   - two prior relations each render their own expansion (the S9
//     share-violation killer, live).
//
// Expected rows are literals written from the seeded fixture, never
// recomputed from the renderer (plan §9).

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	pgstore "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	sql16roLivePriorRelation = "__loom_stage_0"
	sql16roDeniedAuth        = "FHIR/Denied"
	// sql16roLiveLabel is the fixture's edge label; the lockstep check in
	// the first cell keeps it equal to the plan's own ro_label bind.
	sql16roLiveLabel = "subject_Reference"
)

// sql16roLiveFixture owns one seeded schema plus the prior-stage stand-in.
type sql16roLiveFixture struct {
	svc *pgir02Service
}

func newSQL16roLiveFixture(t *testing.T) sql16roLiveFixture {
	t.Helper()
	svc := newOwnedService(t, 0, "verify_sql16v1")
	insert := func(resourceType, logicalID, authPath string, payload map[string]any) {
		svc.insertResource(t, svc.schema, sql16roProject, sql16roGeneration, resourceType, logicalID, authPath, payload)
	}
	edge := func(observation, patient, authPath string, ordinal int64, sourcePath string) {
		svc.insertEdge(t, pgstore.EdgeOccurrenceRow{
			Project: sql16roProject, DatasetGeneration: sql16roGeneration, AuthResourcePath: authPath,
			Label: sql16roLiveLabel, SourceResourceType: "Observation", SourceLogicalID: observation,
			TargetResourceType: "Patient", TargetLogicalID: patient, SourcePath: sourcePath, SourceOrdinal: ordinal,
		})
	}

	// p-1 carries the route: o-1 (two Coding items), o-4 (one item), o-3
	// (no valueCodeableConcept at all), and o-denied behind a denied auth
	// path. The two o-1 edge rows reach the SAME target twice.
	insert("Patient", "p-1", "FHIR/Patient", map[string]any{"gender": "female"})
	insert("Patient", "p-2", "FHIR/Patient", map[string]any{"gender": "male"})
	insert("Patient", "p-denied", sql16roDeniedAuth, map[string]any{"gender": "denied"})
	insert("Observation", "o-1", "FHIR/Observation", map[string]any{
		"status": "final",
		"valueCodeableConcept": map[string]any{
			"coding": []any{
				map[string]any{"system": "urn:sys", "code": "c1", "display": "One"},
				map[string]any{"system": "urn:sys", "code": "c2"},
			},
		},
	})
	insert("Observation", "o-3", "FHIR/Observation", map[string]any{"status": "amended"})
	insert("Observation", "o-4", "FHIR/Observation", map[string]any{
		"status": "final",
		"valueCodeableConcept": map[string]any{
			"coding": []any{map[string]any{"system": "urn:other", "code": "solo", "display": "Solo"}},
		},
	})
	insert("Observation", "o-denied", sql16roDeniedAuth, map[string]any{
		"status": "final",
		"valueCodeableConcept": map[string]any{
			"coding": []any{map[string]any{"system": "urn:denied", "code": "d"}},
		},
	})
	edge("o-1", "p-1", "FHIR/Observation", 0, "subject[0]")
	edge("o-1", "p-1", "FHIR/Observation", 1, "subject[1]") // duplicate occurrence: one item
	edge("o-3", "p-1", "FHIR/Observation", 0, "subject[0]")
	edge("o-4", "p-1", "FHIR/Observation", 0, "subject[0]")
	edge("o-denied", "p-1", sql16roDeniedAuth, 0, "subject[0]")
	// o-guard is fully authorized (target and edge alike) and hangs off the
	// DENIED anchor: only the route's own anchor scope legs can stop it, so
	// deleting those legs shows up here instead of passing silently.
	insert("Observation", "o-guard", "FHIR/Observation", map[string]any{
		"status": "final",
		"valueCodeableConcept": map[string]any{
			"coding": []any{map[string]any{"system": "urn:guard", "code": "g"}},
		},
	})
	edge("o-guard", "p-denied", "FHIR/Observation", 0, "subject[0]")
	return sql16roLiveFixture{svc: svc}
}

// priorView stands in for the prior composition relation: the materialized
// root-key column the adapter correlates through (facts["_key"].alias). The
// anchor's own auth scope is deliberately NOT applied here — the region's
// anchor scope block is what must drop the denied anchor.
func (f sql16roLiveFixture) priorView(t *testing.T, anchors ...string) {
	t.Helper()
	quoted := make([]string, 0, len(anchors))
	for _, anchor := range anchors {
		quoted = append(quoted, "'"+anchor+"'")
	}
	svc := f.svc
	// Views cannot carry parameters, so the fixture's own scope literals are
	// inlined; the anchor grain is what the adapter must correlate on.
	create := fmt.Sprintf(`CREATE VIEW "%s"."%s" AS SELECT logical_id AS "c0" FROM "%s"."fhir_resource"
		WHERE project = '%s' AND dataset_generation = '%s' AND resource_type = 'Patient'
		  AND logical_id IN (%s)`,
		svc.schema, sql16roLivePriorRelation, svc.schema, sql16roProject, sql16roGeneration,
		strings.Join(quoted, ","))
	if _, err := svc.iso.Exec(svc.ctx, create); err != nil {
		svc.fail("create prior relation stand-in", err)
	}
	t.Cleanup(func() {
		if _, err := svc.iso.Exec(context.WithoutCancel(svc.ctx),
			`DROP VIEW IF EXISTS "`+svc.schema+`"."`+sql16roLivePriorRelation+`"`); err != nil {
			t.Errorf("drop prior relation stand-in: %v", err)
		}
	})
}

// renderLive renders the registered adapter region against the owned schema.
func (f sql16roLiveFixture) renderLive(t *testing.T, plan ir.PhysicalPlan, stage ir.PhysicalConstructionStage,
	facts map[string]stageColumnFact,
) (stageRelatedOccurrenceRegion, *planRenderer) {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding(f.svc.schema)
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	r := &planRenderer{
		storage: binding, rows: map[string]rowScope{},
		parameters: map[string]int{}, canonicalAuthLets: map[string]string{},
	}
	if err := r.loadScope(plan); err != nil {
		t.Fatalf("loadScope: %v", err)
	}
	region, err := r.renderStageGroupRelatedOccurrence(plan, stage, 1, sql16roLivePriorRelation, facts)
	if err != nil {
		t.Fatalf("renderStageGroupRelatedOccurrence: %v", err)
	}
	return region, r
}

// qualifiedRegionText qualifies the prior-relation reference with the owned
// schema: the composition CTE name is a WITH-chain local, so a standalone
// live execution must address the same grain as an owned relation.
func (f sql16roLiveFixture) qualifiedRegionText(t *testing.T, region stageRelatedOccurrenceRegion) string {
	t.Helper()
	from := "FROM " + sql16roLivePriorRelation + " AS " + stageTerminalAlias
	if !strings.Contains(region.sourceText, from) {
		t.Fatalf("region does not read the prior relation as %q:\n%s", from, region.sourceText)
	}
	return strings.Replace(region.sourceText, from,
		`FROM "`+f.svc.schema+`"."`+sql16roLivePriorRelation+`" AS `+stageTerminalAlias, 1)
}

func (f sql16roLiveFixture) regionArgs(r *planRenderer) []any {
	args := make([]any, len(r.args))
	for index, parameter := range r.args {
		args[index] = parameter.Value
	}
	return args
}

// queryRegion executes the region and returns the outer projection's
// stringified rows, re-applying the region's own order terms the way the
// consuming aggregate would (the terminal-alias read re-qualified onto the
// materialized rows).
func (f sql16roLiveFixture) queryRegion(t *testing.T, region stageRelatedOccurrenceRegion,
	r *planRenderer, selectList string,
) [][]string {
	t.Helper()
	sql := "SELECT " + selectList + " FROM (" + f.qualifiedRegionText(t, region) + ") AS q" +
		sql16roOrder(t, region)
	return f.svc.witness(t, sql, f.regionArgs(r)...)
}

// sql16roOrder re-qualifies the region's own order terms onto the
// materialized rows, so every live cell reads the expansion back in exactly
// the order the consuming aggregate would.
func sql16roOrder(t *testing.T, region stageRelatedOccurrenceRegion) string {
	t.Helper()
	order := make([]string, 0, len(region.orderTerms))
	for _, term := range region.orderTerms {
		qualified := strings.Replace(term, stageTerminalAlias+".", "q.", 1)
		if qualified == term {
			t.Fatalf("order term %q is not a terminal-alias read", term)
		}
		order = append(order, qualified)
	}
	return " ORDER BY " + strings.Join(order, ", ")
}

// sql16roKey is the parsed occurrence key of one adapter row.
type sql16roKey struct {
	RootID      *string `json:"root_id"`
	TerminalID  *string `json:"terminal_id"`
	ItemPresent bool    `json:"item_present"`
	Ordinal     *int    `json:"ordinal"`
}

// sql16roMembership is the parsed Coding membership object.
type sql16roMembership struct {
	System  *string `json:"system"`
	Version *string `json:"version"`
	Code    *string `json:"code"`
	Display *string `json:"display"`
}

func sql16roParse[T any](t *testing.T, cell string) T {
	t.Helper()
	var out T
	if cell == "<null>" {
		return out
	}
	if err := json.Unmarshal([]byte(cell), &out); err != nil {
		t.Fatalf("parse jsonb cell %q: %v", cell, err)
	}
	return out
}

func sql16roText(v any) *string {
	text, _ := v.(string)
	return &text
}

func sql16roInt(value int) *int { return &value }

// sql16roShow renders an optional field by value, so a failing cell prints
// the values that differ instead of pointer addresses.
func sql16roShow[T any](value *T) string {
	if value == nil {
		return "<nil>"
	}
	return fmt.Sprint(*value)
}

func (k sql16roKey) String() string {
	return fmt.Sprintf("root=%s terminal=%s item_present=%v ordinal=%s",
		sql16roShow(k.RootID), sql16roShow(k.TerminalID), k.ItemPresent, sql16roShow(k.Ordinal))
}

func (m sql16roMembership) String() string {
	return fmt.Sprintf("system=%s version=%s code=%s display=%s",
		sql16roShow(m.System), sql16roShow(m.Version), sql16roShow(m.Code), sql16roShow(m.Display))
}

// wantRow is one literal expected adapter row: the occurrence key, the
// Coding membership, the two has-item witnesses, the rank, and the selected
// scalar as the driver renders it.
type wantRow struct {
	key        sql16roKey
	membership sql16roMembership
	hasItem    string
	routeItem  string
	rank       string
	status     string
}

func (w wantRow) String() string {
	return fmt.Sprintf("{%s | %s | has=%s route=%s rank=%s status=%s}",
		w.key, w.membership, w.hasItem, w.routeItem, w.rank, w.status)
}

// TestSQL16RelatedOccurrenceLiveRouteSourcedCodingKey is the L5 leg: the
// route-sourced Coding key, its witnesses, its total order, its policies, and
// the share law, on real rows.
func TestSQL16RelatedOccurrenceLiveRouteSourcedCodingKey(t *testing.T) {
	fixture := newSQL16roLiveFixture(t)
	fixture.priorView(t, "p-1", "p-2", "p-denied")
	plan, stage, facts := sql16roFixture(t)
	if got := plan.BindVars["ro_label"]; got != sql16roLiveLabel {
		t.Fatalf("the seeded edge label %q drifted from the plan's ro_label bind %v", sql16roLiveLabel, got)
	}

	region, r := fixture.renderLive(t, plan, stage, facts)

	// --- the observable rows, in the total order. ---
	// The window is ROW_NUMBER() OVER (ORDER BY root COLLATE "C" NULLS
	// FIRST, term_id COLLATE "C" NULLS FIRST, rn): the AQL FOR nesting, so
	// each parent's expansion runs together in parent order, the reached
	// targets in logical_id order, and the Coding items densely numbered.
	const selectList = `q."__loom_r1_k"::text, q."__loom_r1_m"::text, q."__loom_r1_h"::text, ` +
		`q."__loom_r1_r"::text, q."__loom_r1_n", q."__loom_r1_s0"::text`
	rows := fixture.queryRegion(t, region, r, selectList)

	want := []wantRow{
		{
			key:        sql16roKey{RootID: sql16roText("p-1"), TerminalID: sql16roText("o-1"), ItemPresent: true, Ordinal: sql16roInt(0)},
			membership: sql16roMembership{System: sql16roText("urn:sys"), Code: sql16roText("c1"), Display: sql16roText("One")},
			hasItem:    "true", routeItem: "true", rank: "0", status: `"final"`,
		},
		{ // The second Coding item: an absent display reads as null.
			key:        sql16roKey{RootID: sql16roText("p-1"), TerminalID: sql16roText("o-1"), ItemPresent: true, Ordinal: sql16roInt(1)},
			membership: sql16roMembership{System: sql16roText("urn:sys"), Code: sql16roText("c2")},
			hasItem:    "true", routeItem: "true", rank: "1", status: `"final"`,
		},
		{
			key:        sql16roKey{RootID: sql16roText("p-1"), TerminalID: sql16roText("o-4"), ItemPresent: true, Ordinal: sql16roInt(0)},
			membership: sql16roMembership{System: sql16roText("urn:other"), Code: sql16roText("solo"), Display: sql16roText("Solo")},
			hasItem:    "true", routeItem: "true", rank: "2", status: `"final"`,
		},
		{ // The unmatched anchor keeps exactly one pad row.
			key:        sql16roKey{RootID: sql16roText("p-2"), ItemPresent: false},
			membership: sql16roMembership{},
			hasItem:    "false", routeItem: "false", rank: "0", status: "<null>",
		},
		{ // The denied anchor reaches nothing, even though o-guard and its
			// edge are fully authorized: the route's anchor scope legs are what
			// stop it, leaving the parent its pad row.
			key:        sql16roKey{RootID: sql16roText("p-denied"), ItemPresent: false},
			membership: sql16roMembership{},
			hasItem:    "false", routeItem: "false", rank: "0", status: "<null>",
		},
	}
	if len(rows) != len(want) {
		t.Fatalf("row count = %d, want %d:\n%v", len(rows), len(want), rows)
	}
	for index, row := range rows {
		got := wantRow{
			key:        sql16roParse[sql16roKey](t, row[0]),
			membership: sql16roParse[sql16roMembership](t, row[1]),
			hasItem:    row[2], routeItem: row[3], rank: row[4], status: row[5],
		}
		if got.String() != want[index].String() {
			t.Fatalf("row %d = %v, want %v", index, got, want[index])
		}
	}

	// --- the denied target never appears, and the denied anchor never
	// reaches its (fully authorized) child. ---
	for index, row := range rows {
		key := sql16roParse[sql16roKey](t, row[0])
		if key.TerminalID != nil && (*key.TerminalID == "o-denied" || *key.TerminalID == "o-guard") {
			t.Fatalf("row %d reached a scope-excluded target %q:\n%s", index, *key.TerminalID, row[0])
		}
		if strings.Contains(row[1], "urn:denied") || strings.Contains(row[1], "urn:guard") {
			t.Fatalf("row %d carries Coding from a scope-excluded target:\n%s", index, row[1])
		}
	}

	// --- the duplicate occurrence deduped: one reached target, two items. ---
	o1Items := 0
	for _, row := range rows {
		if strings.Contains(row[0], `"terminal_id": "o-1"`) {
			o1Items++
		}
	}
	if o1Items != 2 {
		t.Fatalf("two edge occurrences to one target must dedup to one reached target with two Coding items, got %d rows", o1Items)
	}

	// --- typed columns: the SQL types (the scalar reads are jsonb: the
	// registered document-path read yields jsonb, not text). ---
	typed := fixture.queryRegion(t, region, r,
		`pg_typeof(q."__loom_r1_k"), pg_typeof(q."__loom_r1_m"), pg_typeof(q."__loom_r1_h"), `+
			`pg_typeof(q."__loom_r1_n"), pg_typeof(q."__loom_r1_s0")`)
	const wantTypes = "jsonb jsonb boolean bigint jsonb"
	if got := strings.Join(typed[0], " "); got != wantTypes {
		t.Fatalf("adapter column types = %q, want %q", got, wantTypes)
	}
	sql16roCheckDriverTypes(t, fixture, region, r)

	// --- the independent identity witness (plan §9): the same ordered
	// (root, terminal, ordinal) triples from a hand-written query over the
	// base tables, never from the renderer. ---
	witness := fixture.svc.witness(t, `
		WITH anchors AS (
		  SELECT logical_id AS root, auth_resource_path AS root_auth FROM "`+fixture.svc.schema+`"."fhir_resource"
		  WHERE project = $1 AND dataset_generation = $2 AND resource_type = 'Patient'
		    AND logical_id IN ('p-1','p-2','p-denied')),
		reached AS (
		  SELECT DISTINCT ON (x.logical_id COLLATE "C") a.root AS root, x.logical_id AS term, x.payload AS doc
		  FROM anchors a
		  INNER JOIN "`+fixture.svc.schema+`"."fhir_edge_occurrence" e
		    ON e.project = $1 AND e.dataset_generation = $2
		       AND e.auth_resource_path = ANY($3::text[])
		       AND e.label = $4 AND e.target_resource_type = 'Patient'
		       AND e.target_logical_id = a.root AND e.source_resource_type = 'Observation'
		  INNER JOIN "`+fixture.svc.schema+`"."fhir_resource" x
		    ON x.project = $1 AND x.dataset_generation = $2
		       AND x.auth_resource_path = ANY($3::text[])
		       AND x.resource_type = 'Observation' AND x.logical_id = e.source_logical_id
		-- the anchor's own auth legs: a denied anchor reaches nothing, but
		-- it is a prior-grain row, so the LEFT JOIN keeps its pad row: the
		-- pad branch must NOT re-filter auth, or the guard cell would be
		-- comparing different grains.
		WHERE a.root_auth = ANY($3::text[])
		ORDER BY x.logical_id COLLATE "C"),
		items AS (
		  SELECT reached.root AS root, reached.term AS term, (c.ordinal_row - 1) AS ordinal
		  FROM reached CROSS JOIN LATERAL jsonb_array_elements(
		    CASE WHEN jsonb_typeof(reached.doc #> '{valueCodeableConcept,coding}'::text[]) = 'array'
		         THEN reached.doc #> '{valueCodeableConcept,coding}'::text[] ELSE '[]'::jsonb END
		  ) WITH ORDINALITY AS c(item, ordinal_row)
		  WHERE jsonb_typeof(c.item) <> 'null')
		SELECT coalesce(root,'<none>') || '/' || coalesce(term,'<none>') || '/' || coalesce(ordinal::text,'-')
		FROM (SELECT root, term, ordinal FROM items
		      UNION ALL
		      SELECT a.root, NULL, NULL FROM anchors a
		      WHERE NOT EXISTS (SELECT 1 FROM reached WHERE reached.root = a.root)) u
		ORDER BY root NULLS FIRST, term NULLS FIRST, ordinal NULLS FIRST`,
		sql16roProject, sql16roGeneration, []string{"FHIR/Patient", "FHIR/Observation"}, sql16roLiveLabel)
	var gotTriples []string
	for _, row := range rows {
		key := sql16roParse[sql16roKey](t, row[0])
		root, term, ordinal := "<none>", "<none>", "-"
		if key.RootID != nil {
			root = *key.RootID
		}
		if key.TerminalID != nil {
			term = *key.TerminalID
		}
		if key.Ordinal != nil {
			ordinal = fmt.Sprint(*key.Ordinal)
		}
		gotTriples = append(gotTriples, root+"/"+term+"/"+ordinal)
	}
	var wantTriples []string
	for _, row := range witness {
		wantTriples = append(wantTriples, row[0])
	}
	if fmt.Sprint(gotTriples) != fmt.Sprint(wantTriples) {
		t.Fatalf("row identity drifted from the independent witness:\n got %v\nwant %v", gotTriples, wantTriples)
	}

	// --- the empty-policy arms on real rows. ---
	t.Run("preserve-parent-coding-keeps-the-route-with-no-items", func(t *testing.T) {
		policyPlan, policyStage, policyFacts := sql16roFixture(t)
		adapter := policyStage.Group.RelatedOccurrence
		adapter.CodingEmptyPolicy = ir.PhysicalUnnestPreserveParent
		adapter.CodingUnnest.EmptyPolicy = ir.PhysicalUnnestPreserveParent
		policyRegion, policyRenderer := fixture.renderLive(t, policyPlan, policyStage, policyFacts)
		policyRows := fixture.queryRegion(t, policyRegion, policyRenderer, selectList)
		// o-3 (matched, no Coding items) now contributes one row with
		// item_present false and a null ordinal; everything else is unchanged.
		if len(policyRows) != len(want)+1 {
			t.Fatalf("PRESERVE_PARENT must add the itemless route row, got %d rows:\n%v", len(policyRows), policyRows)
		}
		var itemless []string
		for _, row := range policyRows {
			if strings.Contains(row[0], `"terminal_id": "o-3"`) {
				itemless = row
			}
		}
		if itemless == nil {
			t.Fatalf("the itemless route row is missing:\n%v", policyRows)
		}
		key := sql16roParse[sql16roKey](t, itemless[0])
		if key.RootID == nil || *key.RootID != "p-1" || key.TerminalID == nil || *key.TerminalID != "o-3" ||
			key.ItemPresent || key.Ordinal != nil {
			t.Fatalf("itemless route key = %#v, want root p-1, terminal o-3, item_present false, null ordinal", key)
		}
		if itemless[2] != "false" || itemless[3] != "true" {
			t.Fatalf("itemless route witnesses = %q/%q, want false/true", itemless[2], itemless[3])
		}
	})

	t.Run("route-error-policy-raises-the-registered-guard-tag", func(t *testing.T) {
		errorPlan, errorStage, errorFacts := sql16roFixture(t)
		errorStage.Group.RelatedOccurrence.RelatedRecords.EmptyPolicy = ir.PhysicalUnnestError
		errorRegion, errorRenderer := fixture.renderLive(t, errorPlan, errorStage, errorFacts)
		// The guard raises during fetch, not at Query time (pgx surfaces the
		// extended-protocol error from the rows). Drain and close before
		// judging: an unclosed result blocks the pool's Close in cleanup.
		rows, err := fixture.svc.iso.Query(fixture.svc.ctx,
			fixture.qualifiedRegionText(t, errorRegion), fixture.regionArgs(errorRenderer)...)
		if err == nil {
			for rows.Next() {
			}
			err = rows.Err()
			rows.Close()
		}
		if err == nil {
			t.Fatalf("the route ERROR policy must raise on the unmatched anchor")
		}
		if !strings.Contains(err.Error(), stageExpandEmptyTag) {
			t.Fatalf("the guard must carry the registered tag %q, got: %v", stageExpandEmptyTag, err)
		}
	})

	// --- the S9 share-violation killer, live: two prior relations, each
	// expansion bound to its own anchor grain. ---
	t.Run("two-relations-never-share-an-expansion", func(t *testing.T) {
		other := newSQL16roLiveFixture(t)
		other.priorView(t, "p-2")
		regionA, rendererA := fixture.renderLive(t, plan, stage, facts)
		regionB, rendererB := other.renderLive(t, plan, stage, facts)
		rowsA := fixture.queryRegion(t, regionA, rendererA, `q."__loom_r1_k"::text`)
		rowsB := other.queryRegion(t, regionB, rendererB, `q."__loom_r1_k"::text`)
		if !strings.Contains(strings.Join(flattenSQL16ro(rowsA), " "), "p-1") {
			t.Fatalf("relation A must carry its own reachable anchor p-1: %v", rowsA)
		}
		for _, cell := range flattenSQL16ro(rowsB) {
			if strings.Contains(cell, "p-1") {
				t.Fatalf("relation B borrowed relation A's expansion (share violation): %s", cell)
			}
		}
		if len(rowsB) != 1 {
			t.Fatalf("relation B is its own grain: one pad row for p-2, got %v", rowsB)
		}
	})
}

// sql16roCheckDriverTypes reads the adapter columns without stringifying, so
// the driver-side Go types are the evidence.
func sql16roCheckDriverTypes(t *testing.T, fixture sql16roLiveFixture,
	region stageRelatedOccurrenceRegion, r *planRenderer,
) {
	t.Helper()
	sql := `SELECT q."__loom_r1_n", q."__loom_r1_h" FROM (` + fixture.qualifiedRegionText(t, region) + ") AS q" +
		sql16roOrder(t, region) + " LIMIT 1"
	rows, err := fixture.svc.iso.Query(fixture.svc.ctx, sql, fixture.regionArgs(r)...)
	if err != nil {
		fixture.svc.fail("execute driver-type probe", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("driver-type probe returned no rows")
	}
	values, err := rows.Values()
	if err != nil {
		fixture.svc.fail("read driver-type probe", err)
	}
	// The rank is ROW_NUMBER() - 1, which PostgreSQL types as bigint (the
	// sibling EXPAND ordinal, stage_sequence.go:1288, is bigint alike); the
	// occurrence-key ordinal inside the jsonb is the registered ::int read.
	if _, ok := values[0].(int64); !ok {
		t.Fatalf("the rank must arrive as a driver-side bigint, got %T", values[0])
	}
	if _, ok := values[1].(bool); !ok {
		t.Fatalf("the has-item witness must arrive as a driver-side bool, got %T", values[1])
	}
	if err := rows.Err(); err != nil {
		fixture.svc.fail("finish driver-type probe", err)
	}
}

func flattenSQL16ro(rows [][]string) []string {
	var out []string
	for _, row := range rows {
		out = append(out, row...)
	}
	return out
}
