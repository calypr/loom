package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	pgstore "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// pg-sql-10 live proofs (plan section 6: P1-P3; P4 determinism is offline).
//
// Gated on LOOM_TEST_POSTGRES_URL and real PostgreSQL 18 (pgstore.Open
// refuses any other major, and every fixture acquires its pool through
// it). Each test owns one verify_sql10_* database and one owned schema,
// migrates through the real pg-store-02 runner, and stores literal rows
// through the real pg-store-04 repository. TestMain's zero-leftover audit
// proves nothing under the prefix survives the run.
//
// P1 no-plan-time-raise: the raising arms (D9 assert, both distinct
// guards) are barrier-protected CASTs; a bare dead-arm CAST raises at
// plan time here (population.go:1059-1065, measured live), so plain
// EXPLAIN completing on violating rows is the barrier proof, followed by
// successful execution on healthy rows.
//
// P2 execution-time raise: the same statement shapes over violating rows
// fail at execution with SQLSTATE 22P02 naming the tag, inside the
// constant-folding-barrier message text.
//
// P3 no-pruning/no-short-circuit: EXPLAIN still shows the correlated
// guard SubPlan (never folded away), and value-returning AND leaves a
// falsy first operand's second — raising — operand unevaluated.
// ---------------------------------------------------------------------------

var sql10LiveNamePattern = regexp.MustCompile(`^verify_sql10_[0-9]+_[0-9a-f]+$`)

// sql10Service owns one isolated database/schema pair. The row-insertion
// and execution helpers are the pg-sql-01 ones (same package, same
// pgstore repository path), embedded so the fixture cannot drift from the
// already-audited storage behavior.
type sql10Service struct {
	*pgsq01Service
}

func newSQL10Service(t *testing.T) *sql10Service {
	t.Helper()
	raw := testPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v string) string {
		s := v
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		text := err.Error()
		for _, secret := range secrets {
			if secret != "" {
				text = strings.ReplaceAll(text, secret, "<redacted>")
			}
		}
		t.Fatalf("%s: %s", step, text)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := sql10ResourceName(t)
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err.Error()))
		}
	})

	// pgconn lets a dbname query parameter outrank the URL path; drop any
	// dbname the service URL carries so isolation is provable.
	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := sql10ResourceName(t)
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err.Error()))
		}
	})

	client, err := pgstore.Open(ctx, pgstore.Config{
		DSN:            dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 2 * time.Second,
	})
	if err != nil {
		fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	t.Cleanup(client.Close)
	if _, err := pgstore.MigrateSchema(ctx, client, schema); err != nil {
		fail("migrate owned schema", err)
	}
	return &sql10Service{pgsq01Service: &pgsq01Service{
		ctx: ctx, iso: iso, client: client, schema: schema, scrub: scrub, fail: fail,
	}}
}

func sql10ResourceName(t *testing.T) string {
	t.Helper()
	name := "verify_sql10_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
	if !sql10LiveNamePattern.MatchString(name) {
		t.Fatalf("owned resource name %q does not match the required pattern", name)
	}
	return name
}

func sql10DriverArgs(rendered command.RenderedPostgresPhysicalPlan) []any {
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	return args
}

// renderLive renders through the public entry against the owned schema.
func (s *sql10Service) renderLive(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, s.schema))
	if err != nil {
		s.fail("render plan", err)
	}
	return rendered
}

// explain returns the executor's plan text. Completion is itself an
// assertion: the violating-row statements must PLAIN without raising,
// which is what distinguishes the barrier-protected guard cast from the
// bare dead-arm CAST that PostgreSQL folds and raises at plan time.
func (s *sql10Service) explain(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) string {
	t.Helper()
	var text strings.Builder
	for _, row := range s.run(t, "EXPLAIN (FORMAT TEXT) "+rendered.SQL, sql10DriverArgs(rendered)...) {
		text.WriteString(strings.Join(row, "\n"))
		text.WriteString("\n")
	}
	if text.Len() == 0 {
		s.fail("explain produced no plan", errors.New(rendered.SQL))
	}
	return text.String()
}

// expectRaise executes the rendered statement to completion and demands
// the named SQLSTATE with every tag substring inside the message text.
func (s *sql10Service) expectRaise(t *testing.T, rendered command.RenderedPostgresPhysicalPlan, wantCode string, wantSubstrings ...string) {
	t.Helper()
	_, err := s.iso.Exec(s.ctx, rendered.SQL, sql10DriverArgs(rendered)...)
	if err == nil {
		t.Fatalf("statement executed clean, want %s raising %v: %s", wantCode, wantSubstrings, rendered.SQL)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		s.fail("expect pg error", err)
	}
	if pgErr.Code != wantCode {
		s.fail("statement raised ("+pgErr.Code+") want ("+wantCode+")", err)
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(pgErr.Message, want) {
			t.Fatalf("error message %q does not name %q\nSQL: %s", pgErr.Message, want, rendered.SQL)
		}
	}
}

// runOne executes the rendered statement and demands exactly one row of
// the expected stringified cells. jsonb cells arrive through the pgx
// codec as decoded Go values (bool/string/float64), so expectations read
// as decoded text: a jsonb string "female" is cell "female", not
// `"female"`.
func (s *sql10Service) runOne(t *testing.T, rendered command.RenderedPostgresPhysicalPlan, want ...string) {
	t.Helper()
	rows := s.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly one row %v", rows, want)
	}
	if strings.Join(rows[0], "|") != strings.Join(want, "|") {
		t.Fatalf("row = %v, want %v\nSQL: %s", rows[0], want, rendered.SQL)
	}
}

// ---------------------------------------------------------------------------
// Read-back: registered arithmetic, cast-table, and coalesce semantics on
// stored payloads — the live half of the parity table, against the ArangoDB
// 3.9 documentation citations in the plan.
// ---------------------------------------------------------------------------

func TestSQL10LiveArithmeticCastCoalesceReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-calc", sql10Path,
		map[string]any{"multipleBirthInteger": 5, "active": true,
			// Whitespace-padded decimal spellings: the guard grammar and
			// the btrim set are derived from one explicit ASCII class
			// (review thread r4529631602 — version-varying numeric_in
			// tolerance), so ASCII-padded spellings read the number.
			"birthDate":        " \t42\n",
			"deceasedDateTime": " 42 ",
			"text":             map[string]any{"status": "\n42.5\r", "div": "\u300042"},
			// Unicode-space-padded numerics: PG18's ICU [[:space:]]
			// admits U+2009/U+3000 and the ASCII set refuses NBSP/NEL,
			// so all four sit outside the single-source ASCII grammar
			// and take the documented non-matching arms — cast reads 0
			// (functions-type-cast.md 65-66), arithmetic coercion reads
			// the registered NULL (L2). Never a 22P02 raise, never a
			// locale dependency (F-5 disposition).
			"deceasedBoolean":      "\u2009 42",
			"communication":        map[string]any{"language": "\u300042"},
			"maritalStatus":        "\u00a042",
			"managingOrganization": "\u008542",
			"meta":                 map[string]any{"versionId": "9007199254740993", "source": "5e-324", "lastUpdated": "2.4703282292062327e-324"},
		})
	// Type decoy with the same logical id: excluded by the scope chain,
	// proven stored below.
	svc.insertResource(t, sql10Project, sql10Gen, "Observation", "p-calc", sql10Path, nil)

	binds := map[string]any{
		"ten":       int64(10),
		"thirtytwo": int64(32),
		"two":       int64(2),
		"one":       int64(1),
		"zero":      int64(0),
		"three":     int64(3),
		"huge":      float64(1e308),
		"tiny":      float64(1e-308),
		"tenth":     float64(0.1),
		"tenth3":    float64(0.3),
		"d02":       float64(0.2),
		"half":      float64(0.5),
		"eleven":    int64(11),
		"lakhth":    int64(100000),
		"numstr":    "42",
		"flagstr":   "true",
		"fb":        "fallback",
	}
	plan := sql10Plan("Patient", binds,
		// 10 + 32 = 42 in double precision (all arithmetic is double).
		sql10CallProjection("sum", sql10Call("add", "", sql10Literal("ten"), sql10Literal("thirtytwo"))),
		// 5 / 2 = 2.5 over a stored payload number.
		sql10CallProjection("quot", sql10Call("divide", "",
			sql10Extract(t, "Patient", "multipleBirthInteger", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("two"))),
		// 1 / 0 reads NULL (operators.md 259-277: 1/0 -> null), never a
		// division-by-zero error.
		sql10CallProjection("zero", sql10Call("divide", "", sql10Literal("one"), sql10Literal("zero"))),
		// 1e308 * 10 overflows: the IEEE result is Infinity — the
		// documented invalid value (operators.md 259-262) — and the band
		// guard reads NULL instead of reaching PG18's 22003 raise.
		sql10CallProjection("huge", sql10Call("multiply", "", sql10Literal("huge"), sql10Literal("ten"))),
		// 1e-308 / 1e308 = 1e-616 underflows: IEEE round-to-even's own
		// answer is 0, and the band guard reads 0 rather than letting
		// PG18's raise on the way (L20-adjacent, IEEE-exact side).
		sql10CallProjection("small", sql10Call("divide", "", sql10Literal("tiny"), sql10Literal("huge"))),
		// IEEE parity pins (F-2/F-4 fix evidence, plan §4c): the results
		// are hardware-IEEE double roundings, which no decimal-domain
		// chain reproduces. 0.1+0.2 and 0.1/0.3 misround to the decimal
		// answers in numeric; 0.30000000000000004 and
		// 0.33333333333333337 are the doubles AQL stores.
		sql10CallProjection("ieeeAdd", sql10Call("add", "", sql10Literal("tenth"), sql10Literal("d02"))),
		sql10CallProjection("ieeeDiv", sql10Call("divide", "", sql10Literal("tenth"), sql10Literal("tenth3"))),
		sql10CallProjection("ieeeDiv11", sql10Call("divide", "", sql10Literal("eleven"), sql10Literal("lakhth"))),
		sql10CallProjection("ieeeDiv3", sql10Call("divide", "", sql10Literal("one"), sql10Literal("three"))),
		// 2^53+1 is exactly representable as decimal text but not as a
		// double: IEEE parsing (strtod's round-to-nearest) reads it as
		// 9007199254740992, which is what AQL stores.
		sql10CallProjection("ieeeParse", sql10Call("add", "",
			sql10Extract(t, "Patient", "meta.versionId", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("zero"))),
		// Band cells: overflow and the divisor at the subnormal floor
		// read the registered NULL; a gradual product below the floor
		// reads the registered 0; the subnormal tie text (where PG18
		// measured the cast itself raising 22003) reads the registered 0
		// — the self-caused leak fixed and pinned (L20).
		sql10CallProjection("addOver", sql10Call("add", "", sql10Literal("huge"), sql10Literal("huge"))),
		sql10CallProjection("divOver", sql10Call("divide", "", sql10Literal("huge"), sql10Literal("half"))),
		sql10CallProjection("mulUnder", sql10Call("multiply", "", sql10Literal("tiny"), sql10Literal("tiny"))),
		sql10CallProjection("divMinSub", sql10Call("divide", "", sql10Literal("one"),
			sql10Extract(t, "Patient", "meta.source", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("tieAdd", sql10Call("add", "",
			sql10Extract(t, "Patient", "meta.lastUpdated", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("zero"))),
		// TO_NUMBER table: "42" -> 42; TO_BOOL table: "true" -> true.
		sql10CallProjection("num", sql10Call("cast", "INTEGER", sql10Literal("numstr"))),
		sql10CallProjection("flag", sql10Call("cast", "BOOLEAN", sql10Literal("flagstr"))),
		// COALESCE over a present-but-JSON-null read: "active" is stored
		// true here, so a missing field proves the null-normalized arm.
		sql10CallProjection("pick", sql10Call("coalesce", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("fb"))),
		// Tab/newline-padded decimal strings through every whitespace
		// alphabet: the arithmetic coercion arm, and the cast INTEGER /
		// DECIMAL TO_NUMBER arms — all must read the number, never raise
		// (the explicit btrim set matches the [[:space:]] grammar).
		sql10CallProjection("padmul", sql10Call("multiply", "",
			sql10Extract(t, "Patient", "birthDate", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("one"))),
		sql10CallProjection("padint", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "deceasedDateTime", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("paddec", sql10Call("cast", "DECIMAL",
			sql10Extract(t, "Patient", "text.status", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		// Unicode-space-padded numerics never raise and take the
		// documented arms: cast → 0 (functions-type-cast.md 65-66),
		// arithmetic coercion → registered NULL (L2). These die on the
		// F-5 mutant: re-widening the grammar to the ICU [[:space:]]
		// class would pass U+2009/U+3000 through the guard and raise
		// 22P02 on the cast, failing this read instead of reading 0.
		sql10CallProjection("thinCast", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "deceasedBoolean", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("thinAdd", sql10Call("add", "",
			sql10Extract(t, "Patient", "deceasedBoolean", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("zero"))),
		sql10CallProjection("ideoCast", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "text.div", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("nbspCast", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "maritalStatus", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("nelCast", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "managingOrganization", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
	)
	svc.runOne(t, svc.renderLive(t, plan),
		"42", "2.5", "<null>", "<null>", "0",
		"0.30000000000000004", "0.33333333333333337", "0.00011", "0.3333333333333333",
		"9.007199254740992e+15",
		"<null>", "<null>", "0", "<null>", "0",
		"42", "true", "fallback",
		"42", "42", "42.5",
		"0", "<null>", "0", "0", "0")

	// Non-vacuity: the decoy really exists, so its exclusion is the
	// scope chain, not an empty table.
	witness := svc.run(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project=$1 AND dataset_generation=$2 AND resource_type='Observation' AND logical_id='p-calc'`,
		sql10Project, sql10Gen)
	if len(witness) != 1 || witness[0][0] != "1" {
		t.Fatalf("decoy must exist: %v", witness)
	}
}

// TestSQL10LiveDecimalTierReadBack pins the F-7 registered tiers on PG18:
// valid representations whose numeric_in cast raises 22003 (the battery
// cells below all raised before the oracle gate — "1e999999999999"
// through both the TO_NUMBER cast table and the arithmetic coercion)
// read the registered conservative arms instead of raising: the
// pg_input_is_valid oracle gates the cast exactly where numeric_in
// raises, provably-IEEE-zero spellings read 0, and every oracle-rejected
// remainder reads the fail-closed NULL. The 10000-digit e-9999 spelling (a legitimate 1),
// the 101-digit-mantissa e-10000 and the 7000-digit fraction e-9000
// (both castable in numeric, band-reading 0), and the 32768-digit
// integer (band NULL) pin that the gate re-reads nothing currently safe.
// Every cell runs on STORED strings, the F-7 reach path.
func TestSQL10LiveDecimalTierReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-tier", sql10Path,
		map[string]any{
			// The verifier battery cells: exponent-overflow texts.
			"meta": map[string]any{
				"versionId":   "1e999999999999",
				"source":      "1e999999999998",
				"lastUpdated": "1e-999999999999",
			},
			"deceasedBoolean": "-1e999999999999",
			"birthDate":       " 1e2147483648 ",
			// Oracle-rejected zero spelling: the all-zero fraction beyond
			// numeric's display scale — reads the registered 0.
			"deceasedDateTime": "0." + strings.Repeat("0", 20000),
			"text": map[string]any{
				// Legitimate arbitrary-precision spelling: 10^9999 ·
				// 10^-9999 = 1 — the gate must still cast and read it.
				"status": "1" + strings.Repeat("0", 9999) + "e-9999",
				// Castable and band-zero — reads 0 through the oracle
				// cast, the same reading the provable-underflow arm
				// gives its rejected neighbours.
				"div": "1" + strings.Repeat("0", 99) + "e-10000",
			},
			// Mantissa-digit-versus-exponent magnitudes: both castable in
			// numeric (scale 10000 / 16000) and band-zero — the validity
			// gate keeps their real readings, no registered sliver.
			"maritalStatus":        "1" + strings.Repeat("0", 100) + "e-10000",
			"managingOrganization": "0." + strings.Repeat("9", 7000) + "e-9000",
			// Measured-storable huge integer: casts, then the band reads
			// NULL (the same reading the unbounded grammar gave).
			"gender": strings.Repeat("9", 32768),
		})
	binds := map[string]any{"zero": int64(0)}
	str := func(path ...string) ir.PhysicalExpression {
		return sql10Extract(t, "Patient", strings.Join(path, "."), ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	}
	castOf := func(name string, path ...string) ir.PhysicalProjection {
		return sql10CallProjection(name, sql10Call("cast", "INTEGER", str(path...)))
	}
	addOf := func(name string, path ...string) ir.PhysicalProjection {
		return sql10CallProjection(name, sql10Call("add", "", str(path...), sql10Literal("zero")))
	}
	plan := sql10Plan("Patient", binds,
		// Battery cells through TO_NUMBER: registered NULL for the
		// overflow side, registered 0 for the underflow side — never
		// the 22003 raise the pre-F-7 unguarded cast reached.
		castOf("b1", "meta", "versionId"),
		castOf("b2", "meta", "source"),
		castOf("b3", "meta", "lastUpdated"),
		castOf("b4", "deceasedBoolean"),
		castOf("b5", "birthDate"),
		// The same cells through arithmetic coercion: NULL propagates,
		// the provable zero coerces to the 0 operand AQL parses.
		addOf("b1a", "meta", "versionId"),
		addOf("b3a", "meta", "lastUpdated"),
		addOf("b5a", "birthDate"),
		castOf("u1zero", "deceasedDateTime"),
		castOf("safeLegit", "text", "status"),
		castOf("u2tiny", "text", "div"),
		addOf("u2add", "text", "div"),
		castOf("mantissaS1", "maritalStatus"),
		castOf("mantissaS2", "managingOrganization"),
		castOf("safeCap", "gender"),
	)
	svc.runOne(t, svc.renderLive(t, plan),
		"<null>", "<null>", "0", "<null>", "<null>",
		"<null>", "0", "<null>",
		"0", "1", "0", "0",
		"0", "0", "<null>")
}

// TestSQL10LiveDecimalTierRejectedReads pins the remainder of the F-7
// registered reading table: an oracle-rejected representation that is
// neither zero-valued nor provably underflow reads the fail-closed NULL
// through TO_NUMBER and through arithmetic coercion (a 18000-digit
// fraction overflows numeric's display scale; its IEEE value would be a
// real 0.777… decimal, which the register refuses to approximate from
// stored text — fail-closed beats a wrong number, and the pre-F-7 path
// here was a 22003 raise), while an oracle-rejected all-zero fraction
// with a huge exponent still reads the registered 0 (zero times any
// power of ten is zero, no cast needed).
func TestSQL10LiveDecimalTierRejectedReads(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-oracle", sql10Path,
		map[string]any{
			"meta": map[string]any{
				"versionId": "0." + strings.Repeat("9", 18000),
				// F-8 provability spoof (verifier 136's intended-red
				// cells s1-s3): scale-overflow texts with leading-zero
				// exponents — values ~0.1, ~0.2, and ~1e98 that the
				// digit-count arm used to spoof into the registered 0.
				"source":      "0." + strings.Repeat("9", 18000) + "e-00001",
				"lastUpdated": strings.Repeat("9", 99) + "." + strings.Repeat("9", 20000) + "e-00001",
			},
			"gender": "1." + strings.Repeat("9", 20000) + "e-00001",
			"text": map[string]any{
				// Zero-valued text whose POSITIVE exponent rescues the
				// scale: 136's grid overturned the earlier assumption
				// and pinned this oracle-TRUE, so the cast arm answers
				// it — the registered 0 here is the exact cast reading,
				// the same value the zero tier would answer.
				"div": "0." + strings.Repeat("0", 18000) + "e99999",
				// The oracle-accepted leading-zero exponent: the cast
				// arm answers it exactly (never the underflow arm).
				"status": "1e-00001",
			},
		})
	frac := sql10Extract(t, "Patient", "meta.versionId", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	zeroScaled := sql10Extract(t, "Patient", "text.div", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	spoofTiny := sql10Extract(t, "Patient", "meta.source", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	spoofHuge := sql10Extract(t, "Patient", "meta.lastUpdated", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	spoofMid := sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	plan := sql10Plan("Patient", map[string]any{"zero": int64(0)},
		sql10CallProjection("fracCast", sql10Call("cast", "INTEGER", frac)),
		sql10CallProjection("fracAdd", sql10Call("add", "", frac, sql10Literal("zero"))),
		sql10CallProjection("zeroCast", sql10Call("cast", "INTEGER", zeroScaled)),
		// Fail-closed NULL, never the spoofed 0.
		sql10CallProjection("spoofTiny", sql10Call("cast", "INTEGER", spoofTiny)),
		sql10CallProjection("spoofMid", sql10Call("cast", "INTEGER", spoofMid)),
		sql10CallProjection("spoofHuge", sql10Call("cast", "INTEGER", spoofHuge)),
	)
	svc.runOne(t, svc.renderLive(t, plan), "<null>", "<null>", "0", "<null>", "<null>", "<null>")
}

// TestSQL10LiveDecimalTierLeadingZeroExponent pins the cast-arm side of
// the F-8 disposition: a leading-zero exponent is a valid representation
// the grammar keeps (JSON's exponent grammar and PostgreSQL's numeric
// input accept it), so the oracle-gated cast answers it exactly —
// "1e-00001" reads 0.1 through TO_NUMBER, never the provable-underflow
// 0 and never the fail-closed NULL.
func TestSQL10LiveDecimalTierLeadingZeroExponent(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-lzero", sql10Path,
		map[string]any{"text": map[string]any{"status": "1e-00001"}})
	status := sql10Extract(t, "Patient", "text.status", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	plan := sql10Plan("Patient", nil,
		sql10CallProjection("toNum", sql10Call("cast", "DECIMAL", status)),
	)
	svc.runOne(t, svc.renderLive(t, plan), "0.1")
}

// TestSQL10LiveDivisorRoundTripReadBack pins the F-9 disposition: a
// pass-band division whose divisor drifts on the double cast (the
// subnormal band [callDoubleSubnormalFloor, ~7.41e-324) rounds to the
// minimum subnormal, shrinking the divisor and inflating the executed
// quotient past the tie) takes the registered NULL instead of PG18's
// 22003 "value out of range: overflow" inside the pass arm (the exact
// reviewer reproduction: 1.25e-15 ÷ 7.4e-324 — band quotient 1.689e308
// passes, the drifted double quotient would be 2.53e308). The
// minimum-subnormal spelling pair is the registered fail-closed
// divergence (IEEE's quotient 1e-16 ÷ 4.9406564584124654e-324 is a
// finite ~2.02e307; only drift-band divisors can reach this arm, and
// every subnormal-band spelling refuses the round-trip guard, probed).
// The sanity pair proves normal-range registered divisors are byte-
// identical with the guard in place.
func TestSQL10LiveDivisorRoundTripReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-drift", sql10Path,
		map[string]any{
			"multipleBirthInteger": json.RawMessage(`1.25e-15`),
			"deceasedBoolean":      json.RawMessage(`7.4e-324`),
			"deceasedDateTime":     json.RawMessage(`1e-16`),
			"gender":               json.RawMessage(`4.9406564584124654e-324`),
			"maritalStatus":        json.RawMessage(`10`),
			"managingOrganization": json.RawMessage(`4`),
		})
	num := func(path string) ir.PhysicalExpression {
		return sql10Extract(t, "Patient", path, ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	}
	plan := sql10Plan("Patient", nil,
		sql10CallProjection("driftDiv", sql10Call("divide", "",
			num("multipleBirthInteger"), num("deceasedBoolean"))),
		sql10CallProjection("minSubDiv", sql10Call("divide", "",
			num("deceasedDateTime"), num("gender"))),
		sql10CallProjection("sanityDiv", sql10Call("divide", "",
			num("maritalStatus"), num("managingOrganization"))),
	)
	svc.runOne(t, svc.renderLive(t, plan), "<null>", "<null>", "2.5")
}

// TestSQL10LiveDecimalTierStoredNumbers pins the number side of F-7:
// jsonb_in itself rejects numbers numeric_in cannot represent
// (PG18 measured: INSERT of '{"a": 1e999999999999}'::jsonb raises 22003
// at write time), so every storable number text casts safely and the
// unguarded number-arm cast needs no tier guard — the band arms do the
// magnitude work. Both cells below store numbers at the measured
// storable extremes (1e100000 and a 65536-digit integer) and read the
// registered band NULL without a raise.
func TestSQL10LiveDecimalTierStoredNumbers(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-num", sql10Path,
		map[string]any{
			"multipleBirthInteger": json.RawMessage(`1e100000`),
			"deceasedBoolean":      json.RawMessage(`1` + strings.Repeat("0", 65535)),
		})
	plan := sql10Plan("Patient", nil,
		sql10CallProjection("bigNum", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "multipleBirthInteger", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("longNum", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "deceasedBoolean", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
	)
	svc.runOne(t, svc.renderLive(t, plan), "<null>", "<null>")
}

// TestSQL10LiveArithmeticBandClampReadBack pins the F-8 disposition on
// the reach path the band design exists for: STORABLE extreme-number
// payloads whose unclamped band-test operations raised 22003 inside the
// numeric domain before any arm could answer (PG18 probed: 1e98303 ×
// 1e98303 — each storable under the measured 1e131071 exponent cap,
// 1e131071 ÷ 1e-16383, and 9.9e131071 + 9.9e131071). With the
// comparison clamp the tests' products, quotients, and sums stay inside
// the domain, and the registered arms answer: overflow sides read NULL,
// the reciprocal division reads the registered 0 (matching the probed
// numeric division behavior — an underflowing quotient flushes to
// 0.000… rather than raising), and the sanity multiply still reads its
// IEEE result.
func TestSQL10LiveArithmeticBandClampReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-band", sql10Path,
		map[string]any{
			"multipleBirthInteger": json.RawMessage(`2`),
			"active":               json.RawMessage(`3`),
			"deceasedBoolean":      json.RawMessage(`1e98303`),
			"deceasedDateTime":     json.RawMessage(`1e98303`),
			"text": map[string]any{
				"div": json.RawMessage(`1e131071`),
			},
			"maritalStatus": json.RawMessage(`1e-16383`),
			"birthDate":     json.RawMessage(`9.9e131071`),
			"gender":        json.RawMessage(`9.9e131071`),
		})
	num := func(path string) ir.PhysicalExpression {
		return sql10Extract(t, "Patient", path, ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	}
	plan := sql10Plan("Patient", nil,
		// Sanity: a real multiply still reads its IEEE result — the
		// clamp is invisible to round-tripping magnitudes.
		sql10CallProjection("mulSanity", sql10Call("multiply", "",
			num("multipleBirthInteger"), num("active"))),
		// Overflow sides: registered NULL, never the raise.
		sql10CallProjection("mulExt", sql10Call("multiply", "",
			num("deceasedBoolean"), num("deceasedDateTime"))),
		sql10CallProjection("divExt", sql10Call("divide", "",
			num("text.div"), num("maritalStatus"))),
		sql10CallProjection("addExt", sql10Call("add", "",
			num("birthDate"), num("gender"))),
		// Underflow side: registered 0 (probed flush, not a raise).
		sql10CallProjection("divRecip", sql10Call("divide", "",
			num("maritalStatus"), num("text.div"))),
	)
	svc.runOne(t, svc.renderLive(t, plan), "6", "<null>", "<null>", "<null>", "0")
}

// readings that prove the registered strict-type basis — AQL comparisons
// "do not perform implicit type casts if the compared operands have
// different types" (operators.md 39-43) and rank data types before values
// (fundamentals-type-value-order.md 15-18) — plus the two registered
// null-padding divergences L19 (fundamentals-type-value-order.md 97-136)
// and the L15 array-rank NULL. Every expectation names the documented AQL
// reading of the same pair; pg-sql-05's predicates.go live-pins its
// ordering divergences the same way.
// ---------------------------------------------------------------------------

func TestSQL10LiveComparisonCrossTypeReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-cmp", sql10Path,
		map[string]any{
			// Schema-valid Patient slots carrying deliberately cross-type
			// stored values — the exact posture pg-sql-05's live matrix
			// uses ("stores a string under a numeric kind to pin it"):
			// the selector must name a schema field, the payload pins
			// the stored JSON type.
			"multipleBirthInteger": 65, "gender": "65", "active": false,
			"deceasedBoolean":      1,
			"text":                 map[string]any{"status": "", "div": []any{float64(1)}},
			"managingOrganization": map[string]any{},
			"maritalStatus":        map[string]any{"a": nil},
		})
	// Type decoy with the same logical id: the row this plan must NOT read.
	svc.insertResource(t, sql10Project, sql10Gen, "Observation", "p-cmp", sql10Path, nil)

	binds := map[string]any{
		"str65": "65", "sixtyfive": int64(65), "zero": int64(0),
		"onepointzero": float64(1), "nul": nil,
		"arrpad": json.RawMessage(`[1,null]`), "arrplain": json.RawMessage(`[1]`),
	}
	num := sql10Extract(t, "Patient", "multipleBirthInteger", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	numstr := sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	flag := sql10Extract(t, "Patient", "active", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	empty := sql10Extract(t, "Patient", "text.status", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	one := sql10Extract(t, "Patient", "deceasedBoolean", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	arr := sql10Extract(t, "Patient", "text.div", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	obj1 := sql10Extract(t, "Patient", "managingOrganization", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	obj2 := sql10Extract(t, "Patient", "maritalStatus", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	// "deceasedDateTime" is never stored: the SQL NULL arm, which AQL
	// reads as its null scalar (0 == null is false, null == null is true).
	absent := sql10Extract(t, "Patient", "deceasedDateTime", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)

	plan := sql10Plan("Patient", binds,
		// operators.md 47-58: 65 != "65", 0 == null false — no coercion.
		sql10CallProjection("eq65str", sql10Call("eq", "", num, sql10Literal("str65"))),
		sql10CallProjection("eq65num", sql10Call("eq", "", num, sql10Literal("sixtyfive"))),
		sql10CallProjection("neq65str", sql10Call("neq", "", num, sql10Literal("str65"))),
		sql10CallProjection("eqFalse0", sql10Call("eq", "", flag, sql10Literal("zero"))),
		sql10CallProjection("eqEmpty0", sql10Call("eq", "", empty, sql10Literal("zero"))),
		sql10CallProjection("eqAbsent0", sql10Call("eq", "", absent, sql10Literal("zero"))),
		sql10CallProjection("eqAbsentNull", sql10Call("eq", "", absent, sql10Literal("nul"))),
		// Cardinal-value numeric equality: 1 == 1.0 true on both engines.
		sql10CallProjection("eq1eq10", sql10Call("eq", "", one, sql10Literal("onepointzero"))),
		// Type-rank ordering (fundamentals-type-value-order.md 31-63):
		// false < 0, null < 0, 0 < '' — and a numeric-form string ranks as
		// a string, never converted to rank with numbers.
		sql10CallProjection("gtStrNum", sql10Call("gt", "", numstr, num)),
		sql10CallProjection("ltNumStr", sql10Call("lt", "", num, numstr)),
		sql10CallProjection("ltFalseNum", sql10Call("lt", "", flag, num)),
		sql10CallProjection("ltNullNum", sql10Call("lt", "", absent, num)),
		// L19 registered divergence: AQL's null-padded traversal reads
		// [1] == [1, null] and { } == { "a": null } TRUE; jsonb = reads
		// them false — pinned here as the registered PG reading.
		sql10CallProjection("eqArrPad", sql10Call("eq", "", arr, sql10Literal("arrpad"))),
		sql10CallProjection("eqArrSame", sql10Call("eq", "", arr, sql10Literal("arrplain"))),
		sql10CallProjection("eqObjNullAttr", sql10Call("eq", "", obj1, obj2)),
		// L15: array/object operands in an ordering comparison read NULL.
		sql10CallProjection("gtArrNum", sql10Call("gt", "", arr, num)),
	)
	svc.runOne(t, svc.renderLive(t, plan),
		"false", "true", "true", "false", "false", "false", "true", "true",
		"true", "true", "true", "true",
		"false", "true", "false", "<null>")

	witness := svc.run(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project=$1 AND dataset_generation=$2 AND resource_type='Observation' AND logical_id='p-cmp'`,
		sql10Project, sql10Gen)
	if len(witness) != 1 || witness[0][0] != "1" {
		t.Fatalf("decoy must exist: %v", witness)
	}
}

// ---------------------------------------------------------------------------
// D9 ASSERT: barrier against plan-time constant folding (P1), execution-
// time 22P02 naming the message (P2), and value-returning AND never
// evaluating the raising arm of a falsy-first operand (P3).
// ---------------------------------------------------------------------------

func TestSQL10LiveAssertBarrier(t *testing.T) {
	svc := newSQL10Service(t)
	const message = "gender must be a non-empty string"
	svc.insertResource(t, "sql10-ok", sql10Gen, "Patient", "p-ok", sql10Path,
		map[string]any{"gender": "female"})
	svc.insertResource(t, "sql10-bad", sql10Gen, "Patient", "p-bad", sql10Path,
		map[string]any{"gender": false})
	svc.insertResource(t, "sql10-lazy", sql10Gen, "Patient", "p-lazy", sql10Path,
		map[string]any{"gender": false})

	falsyCond := func() ir.PhysicalExpression {
		return sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
	}

	// Truthy arm: EXPLAIN completes and execution reads jsonb 'true'
	// (functions-miscellaneous.md: ASSERT returns true).
	okPlan := sql10Plan("Patient", map[string]any{
		"project": "sql10-ok", "amsg": message,
	}, sql10CallProjection("checked", sql10Call("assert", "", falsyCond(), sql10Literal("amsg"))))
	okRendered := svc.renderLive(t, okPlan)
	svc.explain(t, okRendered)
	svc.runOne(t, okRendered, "true")

	// Falsy arm: EXPLAIN still completes — the current_setting barrier
	// keeps the raising CAST out of plan-time folding (population.go
	// measures a bare dead-arm CAST raising here) — and execution raises
	// 22P02 carrying the message through its $n text parameter.
	badPlan := sql10Plan("Patient", map[string]any{
		"project": "sql10-bad", "amsg": message,
	}, sql10CallProjection("checked", sql10Call("assert", "", falsyCond(), sql10Literal("amsg"))))
	badRendered := svc.renderLive(t, badPlan)
	svc.explain(t, badRendered)
	svc.expectRaise(t, badRendered, "22P02", "ASSERT:", message)

	// Value-returning AND short-circuit (operators.md 146-148): a falsy
	// first operand returns its own value and the second, raising,
	// operand is never evaluated. If the CASE were eager this row would
	// abort with 22P02 instead of reading false.
	lazyPlan := sql10Plan("Patient", map[string]any{
		"project": "sql10-lazy", "amsg": message, "fl": false,
	}, sql10CallProjection("guarded", sql10Call("and", "",
		sql10Literal("fl"),
		sql10Nested(sql10Call("assert", "", falsyCond(), sql10Literal("amsg"))))))
	svc.runOne(t, svc.renderLive(t, lazyPlan), "false")
}

// ---------------------------------------------------------------------------
// D10 distinct_present_one: FLATTEN one level, drop JSON nulls, MIN over
// the unique present value; guards raise their canonical tags at execution
// only (P1/P2), and the correlated guard subplan survives EXPLAIN (P3).
// ---------------------------------------------------------------------------

func TestSQL10LiveDistinctPresentOneGuards(t *testing.T) {
	svc := newSQL10Service(t)
	scenarios := []struct {
		project   string
		logicalID string
		gender    any
		want      string // expected single cell; ignored when wantTag is set
		wantTag   string // 22P02 message substring; empty => expect want
	}{
		{"sql10-dp-single", "d-1", []string{"female"}, "female", ""},
		{"sql10-dp-nested", "d-1", [][]string{{"male"}}, "male", ""}, // FLATTEN one level
		{"sql10-dp-empty", "d-1", []string{}, "<null>", ""},          // FLATTEN([]) = [] -> NULL
		{"sql10-dp-nulls", "d-1", []any{nil, "x"}, "x", ""},          // nulls drop first
		{"sql10-dp-scalar", "d-1", "scalar", "<null>", ""},           // L14: non-array reads NULL
		{"sql10-dp-multi", "d-1", []string{"female", "male"}, "", distinctPresentGuardMultipleValues},
		// Two distinct values of the wrong type too: the type guard is
		// checked first, pinning the CASE arm order.
		{"sql10-dp-typemis", "d-1", []any{"a", 1}, "", distinctPresentGuardTypeMismatch},
	}
	for _, scenario := range scenarios {
		svc.insertResource(t, scenario.project, sql10Gen, "Patient", scenario.logicalID, sql10Path,
			map[string]any{"gender": scenario.gender})
		plan := sql10Plan("Patient", map[string]any{"project": scenario.project},
			sql10CallProjection("one", sql10Call("distinct_present_one", "STRING",
				sql10Extract(t, "Patient", "gender", ir.PhysicalArrayCardinality, ir.PhysicalEmptyOnNull))))
		rendered := svc.renderLive(t, plan)
		if scenario.wantTag == "" {
			svc.runOne(t, rendered, scenario.want)
			continue
		}
		// P1: the guard CASTs must not fold at plan time even though the
		// violating row is in scope; then P2: execution raises the tag.
		planText := svc.explain(t, rendered)
		// P3: the guard is a per-row correlated subplan, never pruned.
		// The array-collection scenarios must show the SubPlan spelling;
		// the scalar-collection L14 read keeps its guard behind a
		// typeof gate the planner may fold to a per-row NULL test.
		if !strings.Contains(planText, "SubPlan") && !strings.Contains(planText, "Function Scan") {
			t.Fatalf("%s: guard subplan missing from plan:\n%s", scenario.project, planText)
		}
		svc.expectRaise(t, rendered, "22P02", scenario.wantTag)
	}
}
