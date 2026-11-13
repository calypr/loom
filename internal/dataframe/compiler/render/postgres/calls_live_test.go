package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	control "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var sql10LiveNamePattern = regexp.MustCompile(`^verify_sql10v7_[0-9]+_[0-9a-f]+$`)

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

	client, err := control.OpenPool(ctx, control.ClientConfig{
		DSN:            dsn,
		MaxConns:       4,
		MinConns:       1,
		AcquireTimeout: 2 * time.Second,
	})
	if err != nil {
		fail("open pg-store client (requires a live PostgreSQL 18 service)", err)
	}
	t.Cleanup(client.Close)
	if _, err := control.MigrateSchema(ctx, client, schema); err != nil {
		fail("migrate owned schema", err)
	}
	return &sql10Service{pgsq01Service: &pgsq01Service{
		ctx: ctx, iso: iso, client: client, schema: schema, scrub: scrub, fail: fail,
	}}
}

func sql10ResourceName(t *testing.T) string {
	t.Helper()
	name := "verify_sql10v7_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
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

func (s *sql10Service) renderLive(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, s.schema))
	if err != nil {
		s.fail("render plan", err)
	}
	return rendered
}

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

func TestSQL10LiveArithmeticCastCoalesceReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-calc", sql10Path,
		map[string]any{"multipleBirthInteger": 5, "active": true,
			"birthDate":            " \t42\n",
			"deceasedDateTime":     " 42 ",
			"text":                 map[string]any{"status": "\n42.5\r", "div": "\u300042"},
			"deceasedBoolean":      "\u2009 42",
			"communication":        map[string]any{"language": "\u300042"},
			"maritalStatus":        "\u00a042",
			"managingOrganization": "\u008542",
			"meta":                 map[string]any{"versionId": "9007199254740993", "source": "5e-324", "lastUpdated": "2.4703282292062327e-324"},
		})
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
		sql10CallProjection("sum", sql10Call("add", "", sql10Literal("ten"), sql10Literal("thirtytwo"))),
		sql10CallProjection("quot", sql10Call("divide", "",
			sql10Extract(t, "Patient", "multipleBirthInteger", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("two"))),
		sql10CallProjection("zero", sql10Call("divide", "", sql10Literal("one"), sql10Literal("zero"))),
		sql10CallProjection("huge", sql10Call("multiply", "", sql10Literal("huge"), sql10Literal("ten"))),
		sql10CallProjection("small", sql10Call("divide", "", sql10Literal("tiny"), sql10Literal("huge"))),
		sql10CallProjection("ieeeAdd", sql10Call("add", "", sql10Literal("tenth"), sql10Literal("d02"))),
		sql10CallProjection("ieeeDiv", sql10Call("divide", "", sql10Literal("tenth"), sql10Literal("tenth3"))),
		sql10CallProjection("ieeeDiv11", sql10Call("divide", "", sql10Literal("eleven"), sql10Literal("lakhth"))),
		sql10CallProjection("ieeeDiv3", sql10Call("divide", "", sql10Literal("one"), sql10Literal("three"))),
		sql10CallProjection("ieeeParse", sql10Call("add", "",
			sql10Extract(t, "Patient", "meta.versionId", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("zero"))),
		sql10CallProjection("addOver", sql10Call("add", "", sql10Literal("huge"), sql10Literal("huge"))),
		sql10CallProjection("divOver", sql10Call("divide", "", sql10Literal("huge"), sql10Literal("half"))),
		sql10CallProjection("mulUnder", sql10Call("multiply", "", sql10Literal("tiny"), sql10Literal("tiny"))),
		sql10CallProjection("divMinSub", sql10Call("divide", "", sql10Literal("one"),
			sql10Extract(t, "Patient", "meta.source", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("tieAdd", sql10Call("add", "",
			sql10Extract(t, "Patient", "meta.lastUpdated", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("zero"))),
		sql10CallProjection("num", sql10Call("cast", "INTEGER", sql10Literal("numstr"))),
		sql10CallProjection("flag", sql10Call("cast", "BOOLEAN", sql10Literal("flagstr"))),
		sql10CallProjection("pick", sql10Call("coalesce", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("fb"))),
		sql10CallProjection("padmul", sql10Call("multiply", "",
			sql10Extract(t, "Patient", "birthDate", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("one"))),
		sql10CallProjection("padint", sql10Call("cast", "INTEGER",
			sql10Extract(t, "Patient", "deceasedDateTime", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
		sql10CallProjection("paddec", sql10Call("cast", "DECIMAL",
			sql10Extract(t, "Patient", "text.status", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
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

	witness := svc.run(t,
		`SELECT count(*) FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project=$1 AND dataset_generation=$2 AND resource_type='Observation' AND logical_id='p-calc'`,
		sql10Project, sql10Gen)
	if len(witness) != 1 || witness[0][0] != "1" {
		t.Fatalf("decoy must exist: %v", witness)
	}
}

func TestSQL10LiveDecimalTierReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-tier", sql10Path,
		map[string]any{
			"meta": map[string]any{
				"versionId":   "1e999999999999",
				"source":      "1e999999999998",
				"lastUpdated": "1e-999999999999",
			},
			"deceasedBoolean":  "-1e999999999999",
			"birthDate":        " 1e2147483648 ",
			"deceasedDateTime": "0." + strings.Repeat("0", 20000),
			"text": map[string]any{
				"status": "1" + strings.Repeat("0", 9999) + "e-9999",
				"div":    "1" + strings.Repeat("0", 99) + "e-10000",
			},
			"maritalStatus":        "1" + strings.Repeat("0", 100) + "e-10000",
			"managingOrganization": "0." + strings.Repeat("9", 7000) + "e-9000",
			"gender":               strings.Repeat("9", 32768),
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
		castOf("b1", "meta", "versionId"),
		castOf("b2", "meta", "source"),
		castOf("b3", "meta", "lastUpdated"),
		castOf("b4", "deceasedBoolean"),
		castOf("b5", "birthDate"),
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

func TestSQL10LiveDecimalTierRejectedReads(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-oracle", sql10Path,
		map[string]any{
			"meta": map[string]any{
				"versionId":   "0." + strings.Repeat("9", 18000),
				"source":      "0." + strings.Repeat("9", 18000) + "e-00001",
				"lastUpdated": strings.Repeat("9", 99) + "." + strings.Repeat("9", 20000) + "e-00001",
			},
			"gender": "1." + strings.Repeat("9", 20000) + "e-00001",
			"text": map[string]any{
				"div":    "0." + strings.Repeat("0", 18000) + "e99999",
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
		sql10CallProjection("spoofTiny", sql10Call("cast", "INTEGER", spoofTiny)),
		sql10CallProjection("spoofMid", sql10Call("cast", "INTEGER", spoofMid)),
		sql10CallProjection("spoofHuge", sql10Call("cast", "INTEGER", spoofHuge)),
	)
	svc.runOne(t, svc.renderLive(t, plan), "<null>", "<null>", "0", "<null>", "<null>", "<null>")
}

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

func TestSQL10LiveDivisorBandReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-drift", sql10Path,
		map[string]any{
			"multipleBirthInteger": json.RawMessage(`1.25e-15`),
			"deceasedBoolean":      json.RawMessage(`7.4e-324`),
			"deceasedDateTime":     json.RawMessage(`1e-16`),
			"gender":               json.RawMessage(`4.9406564584124654e-324`),
			"maritalStatus":        json.RawMessage(`10`),
			"managingOrganization": json.RawMessage(`4`),
			"active":               json.RawMessage(`8.9e-16`),
			"birthDate":            json.RawMessage(`5e-324`),
			"meta.source":          json.RawMessage(`8.88178419700125430e-16`),
			"meta.versionId":       json.RawMessage(`4.94065645841247e-324`),
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
		sql10CallProjection("canonDiv", sql10Call("divide", "",
			num("active"), num("birthDate"))),
		sql10CallProjection("witnessDiv", sql10Call("divide", "",
			num("meta.source"), num("meta.versionId"))),
	)
	svc.runOne(t, svc.renderLive(t, plan), "<null>", "<null>", "2.5", "<null>", "<null>")
}

func TestSQL10LiveDecimalTierLiteralBind(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-bind", sql10Path,
		map[string]any{"multipleBirthInteger": 1})
	cells := []struct {
		name string
		bind string
		kind string
		want string
	}{
		{"intPlain", "42", "INTEGER", "42"},
		{"intRejected", "1e999999999999", "INTEGER", "<null>"},
		{"intZeroScale", "0." + strings.Repeat("0", 20000), "INTEGER", "0"},
		{"decLeadingZero", "1e-00001", "DECIMAL", "0.1"},
		{"decNaN", "NaN", "DECIMAL", "0"},
	}
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		conn, err := svc.iso.Acquire(svc.ctx)
		if err != nil {
			svc.fail("acquire pinned connection", err)
		}
		if _, err := conn.Exec(svc.ctx, "SET plan_cache_mode = "+mode); err != nil {
			conn.Release()
			svc.fail("pin plan_cache_mode "+mode, err)
		}
		for _, cell := range cells {
			plan := sql10Plan("Patient", map[string]any{"operand": cell.bind},
				sql10CallProjection(cell.name, sql10Call("cast", cell.kind, sql10Literal("operand"))),
			)
			rendered := svc.renderLive(t, plan)
			args := sql10DriverArgs(rendered)
			if _, err := conn.Exec(svc.ctx, "EXPLAIN "+rendered.SQL, args...); err != nil {
				conn.Release()
				svc.fail("plan-time EXPLAIN raised under "+mode+" on "+cell.name, err)
			}
			rows, err := conn.Query(svc.ctx, rendered.SQL, args...)
			if err != nil {
				conn.Release()
				svc.fail("execute under "+mode+" on "+cell.name, err)
			}
			var got []string
			for rows.Next() {
				values, err := rows.Values()
				if err != nil {
					rows.Close()
					conn.Release()
					svc.fail("read row under "+mode+" on "+cell.name, err)
				}
				for _, value := range values {
					got = append(got, stringifyCell(value))
				}
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				conn.Release()
				svc.fail("finish statement under "+mode+" on "+cell.name, err)
			}
			if strings.Join(got, "|") != cell.want {
				conn.Release()
				svc.fail("cell "+cell.name+" under "+mode,
					fmt.Errorf("row = %v, want %s\nSQL: %s", got, cell.want, rendered.SQL))
			}
		}
		conn.Release()
	}
}

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
		sql10CallProjection("mulSanity", sql10Call("multiply", "",
			num("multipleBirthInteger"), num("active"))),
		sql10CallProjection("mulExt", sql10Call("multiply", "",
			num("deceasedBoolean"), num("deceasedDateTime"))),
		sql10CallProjection("divExt", sql10Call("divide", "",
			num("text.div"), num("maritalStatus"))),
		sql10CallProjection("addExt", sql10Call("add", "",
			num("birthDate"), num("gender"))),
		sql10CallProjection("divRecip", sql10Call("divide", "",
			num("maritalStatus"), num("text.div"))),
	)
	svc.runOne(t, svc.renderLive(t, plan), "6", "<null>", "<null>", "<null>", "0")
}

func TestSQL10LiveComparisonCrossTypeReadBack(t *testing.T) {
	svc := newSQL10Service(t)
	svc.insertResource(t, sql10Project, sql10Gen, "Patient", "p-cmp", sql10Path,
		map[string]any{
			"multipleBirthInteger": 65, "gender": "65", "active": false,
			"deceasedBoolean":      1,
			"text":                 map[string]any{"status": "", "div": []any{float64(1)}},
			"managingOrganization": map[string]any{},
			"maritalStatus":        map[string]any{"a": nil},
		})
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
	absent := sql10Extract(t, "Patient", "deceasedDateTime", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)

	plan := sql10Plan("Patient", binds,
		sql10CallProjection("eq65str", sql10Call("eq", "", num, sql10Literal("str65"))),
		sql10CallProjection("eq65num", sql10Call("eq", "", num, sql10Literal("sixtyfive"))),
		sql10CallProjection("neq65str", sql10Call("neq", "", num, sql10Literal("str65"))),
		sql10CallProjection("eqFalse0", sql10Call("eq", "", flag, sql10Literal("zero"))),
		sql10CallProjection("eqEmpty0", sql10Call("eq", "", empty, sql10Literal("zero"))),
		sql10CallProjection("eqAbsent0", sql10Call("eq", "", absent, sql10Literal("zero"))),
		sql10CallProjection("eqAbsentNull", sql10Call("eq", "", absent, sql10Literal("nul"))),
		sql10CallProjection("eq1eq10", sql10Call("eq", "", one, sql10Literal("onepointzero"))),
		sql10CallProjection("gtStrNum", sql10Call("gt", "", numstr, num)),
		sql10CallProjection("ltNumStr", sql10Call("lt", "", num, numstr)),
		sql10CallProjection("ltFalseNum", sql10Call("lt", "", flag, num)),
		sql10CallProjection("ltNullNum", sql10Call("lt", "", absent, num)),
		sql10CallProjection("eqArrPad", sql10Call("eq", "", arr, sql10Literal("arrpad"))),
		sql10CallProjection("eqArrSame", sql10Call("eq", "", arr, sql10Literal("arrplain"))),
		sql10CallProjection("eqObjNullAttr", sql10Call("eq", "", obj1, obj2)),
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

	okPlan := sql10Plan("Patient", map[string]any{
		"project": "sql10-ok", "amsg": message,
	}, sql10CallProjection("checked", sql10Call("assert", "", falsyCond(), sql10Literal("amsg"))))
	okRendered := svc.renderLive(t, okPlan)
	svc.explain(t, okRendered)
	svc.runOne(t, okRendered, "true")

	badPlan := sql10Plan("Patient", map[string]any{
		"project": "sql10-bad", "amsg": message,
	}, sql10CallProjection("checked", sql10Call("assert", "", falsyCond(), sql10Literal("amsg"))))
	badRendered := svc.renderLive(t, badPlan)
	svc.explain(t, badRendered)
	svc.expectRaise(t, badRendered, "22P02", "ASSERT:", message)

	lazyPlan := sql10Plan("Patient", map[string]any{
		"project": "sql10-lazy", "amsg": message, "fl": false,
	}, sql10CallProjection("guarded", sql10Call("and", "",
		sql10Literal("fl"),
		sql10Nested(sql10Call("assert", "", falsyCond(), sql10Literal("amsg"))))))
	svc.runOne(t, svc.renderLive(t, lazyPlan), "false")
}

func TestSQL10LiveDistinctPresentOneGuards(t *testing.T) {
	svc := newSQL10Service(t)
	scenarios := []struct {
		project   string
		logicalID string
		gender    any
		want      string
		wantTag   string
	}{
		{"sql10-dp-single", "d-1", []string{"female"}, "female", ""},
		{"sql10-dp-nested", "d-1", [][]string{{"male"}}, "male", ""},
		{"sql10-dp-empty", "d-1", []string{}, "<null>", ""},
		{"sql10-dp-nulls", "d-1", []any{nil, "x"}, "x", ""},
		{"sql10-dp-scalar", "d-1", "scalar", "<null>", ""},
		{"sql10-dp-multi", "d-1", []string{"female", "male"}, "", distinctPresentGuardMultipleValues},
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
		planText := svc.explain(t, rendered)
		if !strings.Contains(planText, "SubPlan") && !strings.Contains(planText, "Function Scan") {
			t.Fatalf("%s: guard subplan missing from plan:\n%s", scenario.project, planText)
		}
		svc.expectRaise(t, rendered, "22P02", scenario.wantTag)
	}
}
