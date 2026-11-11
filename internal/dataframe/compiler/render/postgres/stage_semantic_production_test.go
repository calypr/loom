package postgres

// pg-sql-16 G2 (L-13.3 closure) live legs — plan §10 L4 "semantic matrix".
// Hand-carried IR over the production-lowered base (production authoring of
// the semantic family stays refused at recipe.Bundle.Validate — L-13.3's own
// probe receipt). Every expected row is literal, typed columns are checked
// through the driver, and every group multiset is compared under the
// registered order. Namespace: the ticket-owned verify_sql16v1_* (already
// registered with the TestMain delta audit in stage_sequence_test.go).

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func newSQL16SemanticService(t *testing.T) *pgir02Service {
	t.Helper()
	return newOwnedService(t, 0, "verify_sql16v1")
}

func sql16SemanticTrackFixtures(t *testing.T, svc *pgir02Service) {
	t.Helper()
	var database string
	if err := svc.iso.QueryRow(svc.ctx, "SELECT current_database()").Scan(&database); err != nil {
		svc.fail("track owned database", err)
	}
	sql16FixturesMu.Lock()
	defer sql16FixturesMu.Unlock()
	sql16TrackedFixtures[database] = true
	sql16TrackedFixtures[svc.schema] = true
}

// sql16Coding is one stored Coding object; absent keys are MISSING members
// (the payload simply lacks them), which the matcher reads as non-matching
// or null exactly as Arango does.
func sql16Coding(system, version, code, display any) map[string]any {
	coding := map[string]any{}
	if system != nil {
		coding["system"] = system
	}
	if version != nil {
		coding["version"] = version
	}
	if code != nil {
		coding["code"] = code
	}
	if display != nil {
		coding["display"] = display
	}
	return coding
}

// sql16SemanticRow plants an Observation with the status key and the raw
// code.coding[] array (the carrier's walk root).
func sql16SemanticRow(t *testing.T, svc *pgir02Service, logicalID, project, generation, authPath, status string, codings ...map[string]any) {
	t.Helper()
	payload := map[string]any{"status": status}
	if codings != nil {
		payload["code"] = map[string]any{"coding": codings}
	}
	svc.insertResource(t, svc.schema, project, generation, "Observation", logicalID, authPath, payload)
}

// sql16SemanticFixture plants the L4 acceptance table. With the versioned
// family (system sql16SemanticSystem, version sql16SemanticIdentity) the
// expected membership rows are:
//
//	s-1: (S,V,"a","alpha"), (S,V,"b","bravo")   — per-row dedup + MIN collapse
//	s-2: (S,V,"a","mike")
//	s-3: (S,V,"c",null)      — blank display gates to null (label → code)
//	s-4: zero-match fallback (foreign system)
//	s-5: (null,null,null,"ghost") — null code keeps its membership row
//	s-6: zero-match fallback (family system, foreign version)
//	s-7: (S,V,"a",null)      — missing display member
//	s-9: zero-match fallback (system stored as the NUMBER 123)
//	s-10: zero-match fallback (no code key at all)
//	w-out: out of scope entirely (denied auth path)
func sql16SemanticFixture(t *testing.T, svc *pgir02Service) {
	t.Helper()
	sql16SemanticRow(t, svc, "s-1", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "a", "zulu"),
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "a", "alpha"),
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "b", "bravo"),
	)
	sql16SemanticRow(t, svc, "s-2", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "a", "mike"))
	sql16SemanticRow(t, svc, "s-3", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "c", ""))
	sql16SemanticRow(t, svc, "s-4", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding("http://loom.test/fhir/CodeSystem/foreign", sql16SemanticIdentity, "x", "xray"))
	sql16SemanticRow(t, svc, "s-5", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, nil, "ghost"))
	sql16SemanticRow(t, svc, "s-6", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, "9.9.9", "a", "nope"))
	sql16SemanticRow(t, svc, "s-7", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "a", nil))
	sql16SemanticRow(t, svc, "s-9", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(json.Number("123"), sql16SemanticIdentity, "a", "num"))
	sql16SemanticRow(t, svc, "s-10", group13Project, group13Generation, group13AuthPath, "final",
		map[string]any{"display": "keyless"})
	sql16SemanticRow(t, svc, "w-out", group13Project, group13Generation, "FHIR/Hidden", "final",
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "secret", "out"))
	if rows := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`"."fhir_resource" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = $3`,
		group13Project, group13Generation, group13AuthPath); rows[0][0] != "9" {
		t.Fatalf("scope witness: acceptance row count = %v, want 9", rows)
	}
}

// sql16SemanticIdentityTag renders the expected identity of one group: the
// ordinary status_group member plus the semantic member
// (construction_group.go:170-171), tuple = [system, version, code].
func sql16SemanticIdentityTag(t *testing.T, plan ir.PhysicalPlan, version *string, tuple [3]*string) string {
	t.Helper()
	_ = plan
	quote := func(value *string) string {
		if value == nil {
			return "null"
		}
		raw, err := json.Marshal(*value)
		if err != nil {
			t.Fatalf("marshal identity member: %v", err)
		}
		return string(raw)
	}
	str := func(value string) *string { return &value }
	parts := []string{
		`["construction","` + group13StepID + `"]`, `["operation","GROUP"]`,
		`["status_group","final"]`,
		`["category","semantic_family",` + quote(str(sql16SemanticBinding)) + `,` +
			quote(str(sql16SemanticRule)) + `,` + quote(str(sql16SemanticFamily)) + `,` +
			quote(str(sql16SemanticSystem)) + `,` + quote(version) + `,` +
			quote(str(sql16SemanticPath)) + `,[` + quote(tuple[0]) + `,` + quote(tuple[1]) + `,` + quote(tuple[2]) + `]]`,
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func sql16Str(value string) *string { return &value }

// sql16SemanticColumnTypes describes the rendered statement's result
// columns through a prepared statement, so the label column's type is the
// server's, not the renderer's claim. Returned as type OIDs: text is 25,
// bigint (int8) is 20.
func sql16SemanticColumnTypes(t *testing.T, svc *pgir02Service, rendered command.RenderedPostgresPhysicalPlan) []string {
	t.Helper()
	conn, err := svc.iso.Acquire(svc.ctx)
	if err != nil {
		svc.fail("acquire for column types", err)
	}
	defer conn.Release()
	pgxConn := conn.Conn()
	sd, err := pgxConn.Prepare(svc.ctx, "sql16_semantic_describe", rendered.SQL)
	if err != nil {
		svc.fail("prepare for column types", err)
	}
	defer pgxConn.Deallocate(svc.ctx, "sql16_semantic_describe")
	var oids []string
	for _, field := range sd.Fields {
		oids = append(oids, strconv.FormatUint(uint64(field.DataTypeOID), 10))
	}
	return oids
}

// sql16SemanticRows are the L4 acceptance multiset under GROUP: registered
// order is the ordinary status key, then the tuple (system, version, code)
// each COLLATE "C" NULLS FIRST, then the identity refinement.
//
// String-comparator honesty (D-13b law): every display here is ASCII
// lowercase, the class the banked o2b-compare.wire/o2b-sort.wire receipts
// pin Arango's comparator to on COLLATE "C" ("a"<"b", "A"<"a", case-strict
// ==). Codes, systems, versions, and displays with symbols or mixed case
// are an out-of-parity class (Arango sorts "_" and "~" BEFORE letters,
// COLLATE "C" sorts them between "Z" and "a"): no cross-engine order is
// asserted for them, and no fixture depends on one.
func sql16SemanticRows(t *testing.T, plan ir.PhysicalPlan) [][]string {
	t.Helper()
	familySystem, familyVersion := sql16Str(sql16SemanticSystem), sql16Str(sql16SemanticIdentity)
	return [][]string{
		// The zero-match / null-code group: s-4, s-5, s-6, s-9, s-10. The
		// AQL membership walk lives in LETs inside the row FOR
		// (construction_group.go:663-700), so the fallback tuple row keeps
		// its source-row key columns — "final", not null. s-5 carries
		// display "ghost", so MIN(non-null displays) wins over the code
		// fallback.
		{"final", "5", "ghost", sql16SemanticIdentityTag(t, plan, sql16Str(sql16SemanticIdentity), [3]*string{nil, nil, nil})},
		{"final", "3", "alpha", sql16SemanticIdentityTag(t, plan, sql16Str(sql16SemanticIdentity), [3]*string{familySystem, familyVersion, sql16Str("a")})},
		{"final", "1", "bravo", sql16SemanticIdentityTag(t, plan, sql16Str(sql16SemanticIdentity), [3]*string{familySystem, familyVersion, sql16Str("b")})},
		// s-3's only display is blank → gated null → the label is the code.
		{"final", "1", "c", sql16SemanticIdentityTag(t, plan, sql16Str(sql16SemanticIdentity), [3]*string{familySystem, familyVersion, sql16Str("c")})},
	}
}

func TestSQL16SemanticLiveMatrix(t *testing.T) {
	svc := newSQL16SemanticService(t)
	sql16SemanticTrackFixtures(t, svc)
	sql16SemanticFixture(t, svc)

	plan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, sql16SemanticIdentity)
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// No family or fixture string reaches statement text.
	for _, leaked := range []string{sql16SemanticSystem, sql16SemanticBinding, sql16SemanticRule,
		sql16SemanticFamily, sql16SemanticIdentity, group13Project, group13Generation, "final", "alpha"} {
		if strings.Contains(rendered.SQL, leaked) {
			t.Fatalf("value %q leaked into statement text", leaked)
		}
	}

	rows := svc.runRendered(t, rendered)
	if !reflect.DeepEqual(rows, sql16SemanticRows(t, plan)) {
		t.Fatalf("semantic groups mismatch:\n got %q\nwant %q", rows, sql16SemanticRows(t, plan))
	}
	if repeated := svc.runRendered(t, rendered); !reflect.DeepEqual(repeated, rows) {
		t.Fatalf("grouping changed between executions:\n%v\n%v", rows, repeated)
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row[3]] {
			t.Fatalf("duplicate row identity %q", row[3])
		}
		seen[row[3]] = true
	}

	// Typed columns: the label is text, the reducer bigint, the ordinary
	// key text, the identity text (driver-side types via a prepared
	// statement description).
	columnTypes := sql16SemanticColumnTypes(t, svc, rendered)
	for index, want := range []string{"25", "20", "25", "25"} {
		if columnTypes[index] != want {
			t.Fatalf("column %d type = %q, want %q", index, columnTypes[index], want)
		}
	}

	// Mutation probe: planting a lower display for code "a" lifts the
	// group's MIN(display) to "aardvark" — the collapse is live, not a
	// first-row artifact.
	sql16SemanticRow(t, svc, "s-11", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, sql16SemanticIdentity, "a", "aardvark"))
	mutated := svc.runRendered(t, rendered)
	if mutated[1][2] != "aardvark" {
		t.Fatalf("mutation probe: (S,V,a) label never became aardvark: %v", mutated)
	}
	if mutated[1][1] != "4" {
		t.Fatalf("mutation probe: (S,V,a) rows never became 4: %v", mutated)
	}
}

func TestSQL16SemanticLiveVersionlessFamily(t *testing.T) {
	svc := newSQL16SemanticService(t)
	sql16SemanticTrackFixtures(t, svc)
	sql16SemanticFixture(t, svc)
	sql16SemanticRow(t, svc, "s-12", group13Project, group13Generation, group13AuthPath, "final",
		sql16Coding(sql16SemanticSystem, nil, "d", "delta"))

	plan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyGroup, "")
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	rows := svc.runRendered(t, rendered)
	// With the blank-version twin only s-12 matches (every other row
	// stores a version string — including s-5, whose "ghost" display
	// therefore never enters the collapse, so the all-fallback group
	// labels to its null code). s-12's tuple system member is the family
	// system and its version member is NULL: the AQL twin renders the
	// matched version as literal null for a blank-version family
	// (construction_group.go:734-737), not the bound empty string — the
	// bound blank rides only the identity's separate @version part, which
	// AQL binds unconditionally (:170-171).
	null := "<null>"
	familySystem := sql16Str(sql16SemanticSystem)
	want := [][]string{
		{"final", "9", null, sql16SemanticIdentityTag(t, plan, sql16Str(""), [3]*string{nil, nil, nil})},
		{"final", "1", "delta", sql16SemanticIdentityTag(t, plan, sql16Str(""), [3]*string{familySystem, nil, sql16Str("d")})},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("versionless family mismatch:\n got %q\nwant %q", rows, want)
	}
}

func TestSQL16SemanticLiveMissingKeyPolicy(t *testing.T) {
	svc := newSQL16SemanticService(t)
	sql16SemanticTrackFixtures(t, svc)
	sql16SemanticFixture(t, svc)

	// EXCLUDE: every zero-match/null-code group disappears; the three
	// family-coded groups survive untouched.
	excludePlan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyExclude, sql16SemanticIdentity)
	excludeRendered, err := RenderPhysicalPlan(excludePlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("render exclude: %v", err)
	}
	excluded := svc.runRendered(t, excludeRendered)
	if len(excluded) != 3 {
		t.Fatalf("EXCLUDE rows = %v, want the three family-coded groups", excluded)
	}
	for _, row := range excluded {
		if row[2] == "<null>" {
			t.Fatalf("EXCLUDE kept a null-code group: %v", row)
		}
	}

	// ERROR: the null-code membership rows raise the canonical tag
	// through the guard barrier (22P02), while the same data under GROUP
	// is non-empty (the alive control above).
	errorPlan := sql16SemanticPlan(t, recipe.ConstructionGroupMissingKeyError, sql16SemanticIdentity)
	errorRendered, err := RenderPhysicalPlan(errorPlan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("render error: %v", err)
	}
	group13ExpectRaise(t, svc, errorRendered, groupMissingKeyTag)
}
