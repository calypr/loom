package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	control "github.com/calypr/loom/internal/backends/postgres/control"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgsq03ResourceNamePattern = regexp.MustCompile(`^pgsq03_it_[0-9]+_[0-9a-f]+$`)

func pgsq03RawProjection(name string, expression *ir.PhysicalExpression) ir.PhysicalProjection {
	return ir.PhysicalProjection{Name: name, Expression: expression}
}

func pgsq03ExtractExpression(override func(*ir.PhysicalExtract, *ir.PhysicalExpression)) *ir.PhysicalExpression {
	selector := spec.Selector{Steps: []spec.SelectorStep{{Field: "gender"}}}
	extract := &ir.PhysicalExtract{
		Source:        pgsq03DocumentSource(),
		ResourceType:  "Patient",
		Selector:      selector,
		ExecutionMode: ir.PhysicalSelectorGeneric,
	}
	expression := &ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Extract:      extract,
	}
	if override != nil {
		override(extract, expression)
	}
	return expression
}

func pgsq03ActiveSelector() spec.Selector {
	return spec.Selector{Steps: []spec.SelectorStep{{Field: "active"}}}
}

func TestSelectorExtractionRejectsEveryShapeOutsideTheClosedSlice(t *testing.T) {
	binding := mustBinding(t)
	indexZero := 0
	cases := []struct {
		name   string
		plan   ir.PhysicalPlan
		wantIn string
	}{
		{"no operations", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations = nil
			return plan
		}(), "exactly one ROOT_SCAN"},
		{"scan without return", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations = plan.Operations[:1]
			return plan
		}(), "exactly one ROOT_SCAN"},
		{"return before scan", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations = []ir.PhysicalOperation{plan.Operations[1], plan.Operations[0]}
			return plan
		}(), "leading operation must be ROOT_SCAN"},
		{"scan without payload", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations[0].RootScan = nil
			return plan
		}(), "payload set"},
		{"return without payload", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations[1].Return = nil
			return plan
		}(), "RETURN with its payload set"},
		{"population root", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
			return plan
		}(), "not registered for PostgreSQL"},
		{"cohort root", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations[0].RootScan.CohortSource = &ir.PhysicalCohortRootSource{}
			return plan
		}(), "not registered for PostgreSQL"},
		{"candidate seed root", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations[0].RootScan.PageCandidateSeed = &ir.PhysicalRootPageCandidateSeed{}
			return plan
		}(), "not registered for PostgreSQL"},
		{"unnamed root variable", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations[0].RootScan.Variable = ""
			return plan
		}(), "must name its row variable"},
		{"missing collection bind key", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.Operations[0].RootScan.CollectionBindKey = ""
			return plan
		}(), "bind carrying the single resource type"},
		{"collection bind not a string", func() ir.PhysicalPlan {
			plan := pgsq03SelectorPlan("Patient", pgsq03RawProjection("g", pgsq03ExtractExpression(nil)))
			plan.BindVars["root_collection"] = 7
			return plan
		}(), "root collection bind"},
		{"empty return", pgsq03SelectorPlan("Patient"), "at least one selector extraction"},
		{"blank projection name", pgsq03SelectorPlan("Patient",
			pgsq03RawProjection(" ", pgsq03ExtractExpression(nil))), "projection names are required"},
		{"hidden projection", func() ir.PhysicalPlan {
			projection := pgsq03RawProjection("g", pgsq03ExtractExpression(nil))
			projection.Hidden = true
			return pgsq03SelectorPlan("Patient", projection)
		}(), "hidden executor-side metadata"},
		{"value-expression projection", pgsq03SelectorPlan("Patient", pgsq03RawProjection("v",
			&ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})),
			"VALUE expression"},
		{"direct value with payload path", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "status", Value: ir.PhysicalValue{Variable: "root", Path: []string{"status"}}}),
			"typed scope column"},
		{"direct value multi-segment", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "nested", Value: ir.PhysicalValue{Variable: "root", Path: []string{"meta", "source"}}}),
			"typed scope column"},
		{"direct value foreign variable", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "other", Value: ir.PhysicalValue{Variable: "other", Path: []string{"logical_id"}}}),
			"does not bind a readable row"},
		{"direct value bind-backed", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "const", Value: ir.PhysicalValue{BindKey: "const"}}),
			"not a bind value"},
		{"direct value with presence metadata", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			Presence: &ir.PhysicalProjectionPresence{Source: pgsq03DocumentSource(), Paths: [][]string{{"gender"}}}}),
			"presence proofs pair with selector extractions only"},
		{"extract without payload", pgsq03SelectorPlan("Patient",
			pgsq03RawProjection("g", &ir.PhysicalExpression{Kind: ir.PhysicalExtractExpression,
				Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull})),
			"missing its payload"},
		{"extract array cardinality", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(_ *ir.PhysicalExtract, e *ir.PhysicalExpression) { e.Cardinality = ir.PhysicalArrayCardinality }))),
			"SCALAR cardinality only"},
		{"extract object cardinality", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(_ *ir.PhysicalExtract, e *ir.PhysicalExpression) { e.Cardinality = ir.PhysicalObjectCardinality }))),
			"SCALAR cardinality only"},
		{"extract invalid null behavior", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(_ *ir.PhysicalExtract, e *ir.PhysicalExpression) { e.NullBehavior = "NOPE" }))),
			"null behavior"},
		{"extract conditional array mode", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.ExecutionMode = ir.PhysicalSelectorConditionalArray
			}))),
			"CONDITIONAL_ARRAY"},
		{"extract unknown execution mode", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) { x.ExecutionMode = "TELEPORT" }))),
			"unknown selector execution mode"},
		{"extract distinct", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) { x.Distinct = true }))),
			"DISTINCT"},
		{"extract fallbacks", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Fallbacks = []ir.PhysicalSelectorFallback{{Source: pgsq03DocumentSource(), ResourceType: "Patient", Selector: pgsq03ActiveSelector()}}
			}))),
			"fallback selectors"},
		{"extract prepared reference", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Prepared = &ir.PhysicalPreparedReference{SetVariable: "prepared", Field: "value"}
			}))),
			"prepared-set"},
		{"extract unit normalization", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.UnitNormalization = &ir.PhysicalUnitNormalization{}
			}))),
			"unit-normalized"},
		{"extract selector filter", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Selector.Filter = &spec.ContainsFilter{Field: "gender", Needle: "x"}
			}))),
			"CONTAINS filters"},
		{"extract empty selector", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) { x.Selector = spec.Selector{} }))),
			"selector is empty"},
		{"extract iterate step", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Selector.Steps = []spec.SelectorStep{{Field: "gender", Iterate: true}}
			}))),
			"repeated ([]) selector step"},
		{"extract index step", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Selector.Steps = []spec.SelectorStep{{Field: "gender", Index: &indexZero}}
			}))),
			"indexed selector step"},
		{"extract step without field", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Selector.Steps = []spec.SelectorStep{{Field: ""}}
			}))),
			"has no property"},
		{"extract bind source", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Source = ir.PhysicalValue{BindKey: "doc"}
			}))),
			"not a bind value"},
		{"extract nested document source", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Source = ir.PhysicalValue{Variable: "root", Path: []string{"payload", "contained"}}
			}))),
			`["payload"]`},
		{"extract empty source path", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Source = ir.PhysicalValue{Variable: "root"}
			}))),
			`["payload"]`},
		{"extract foreign source variable", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) {
				x.Source = ir.PhysicalValue{Variable: "other", Path: []string{"payload"}}
			}))),
			"does not bind a readable row"},
		{"extract missing resource type", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) { x.ResourceType = "" }))),
			"schema resource type"},
		{"extract contradicts scanned type", pgsq03SelectorPlan("Patient", pgsq03RawProjection("g",
			pgsq03ExtractExpression(func(x *ir.PhysicalExtract, _ *ir.PhysicalExpression) { x.ResourceType = "Observation" }))),
			"but the row is scanned as"},
		{"extract presence metadata path mismatch", func() ir.PhysicalPlan {
			projection := pgsq03RawProjection("g", pgsq03ExtractExpression(nil))
			projection.Presence = &ir.PhysicalProjectionPresence{Source: pgsq03DocumentSource(), Paths: [][]string{{"active"}}}
			return pgsq03SelectorPlan("Patient", projection)
		}(), "exactly the selector path"},
		{"extract presence metadata foreign source", func() ir.PhysicalPlan {
			projection := pgsq03RawProjection("g", pgsq03ExtractExpression(nil))
			projection.Presence = &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: "other", Path: []string{"payload"}}, Paths: [][]string{{"gender"}}}
			return pgsq03SelectorPlan("Patient", projection)
		}(), "selector's own document source"},
		{"sidecar without proof", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "p", PresenceOutput: true}), "no presence proof"},
		{"sidecar with value", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "p", PresenceOutput: true, Presence: &ir.PhysicalProjectionPresence{Source: pgsq03DocumentSource(), Paths: [][]string{{"gender"}}},
			Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}), "only its typed presence proof"},
		{"sidecar with bind source", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "p", PresenceOutput: true, Presence: &ir.PhysicalProjectionPresence{Source: ir.PhysicalValue{BindKey: "doc"}, Paths: [][]string{{"gender"}}}}),
			"not a bind value"},
		{"sidecar with nested source", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "p", PresenceOutput: true, Presence: &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "meta"}}, Paths: [][]string{{"gender"}}}}),
			"row document at"},
		{"sidecar with multiple candidate paths", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "p", PresenceOutput: true, Presence: &ir.PhysicalProjectionPresence{
				Source: pgsq03DocumentSource(), Paths: [][]string{{"gender"}, {"active"}}}}),
			"candidate paths"},
		{"sidecar proves no rendered selector", pgsq03SelectorPlan("Patient",
			pgsq03RawProjection("g", pgsq03ExtractExpression(nil)),
			pgsq03PresenceSidecar("p", "maritalStatus", "text")), "which no selector in this RETURN extracts"},
		{"sidecar proves unreadable variable", pgsq03SelectorPlan("Patient",
			pgsq03RawProjection("g", pgsq03ExtractExpression(nil)),
			ir.PhysicalProjection{Name: "p", PresenceOutput: true, Presence: &ir.PhysicalProjectionPresence{
				Source: ir.PhysicalValue{Variable: "other", Path: []string{"payload"}}, Paths: [][]string{{"gender"}}}}),
			"does not bind a readable row"},
		{"expression and direct value together", pgsq03SelectorPlan("Patient", ir.PhysicalProjection{
			Name: "g", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
			Expression: pgsq03ExtractExpression(nil)}), "both a direct value and an expression"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rendered, err := RenderSelectorExtraction(testCase.plan, binding)
			if !errors.Is(err, ErrUnsupportedPostgresPlan) {
				t.Fatalf("error = %v, want ErrUnsupportedPostgresPlan", err)
			}
			if rendered.SQL != "" || rendered.Args != nil || rendered.OutputColumns != nil {
				t.Fatalf("rejected plan produced output: %#v", rendered)
			}
			if !strings.Contains(err.Error(), testCase.wantIn) {
				t.Fatalf("error %q must contain %q", err.Error(), testCase.wantIn)
			}
			if strings.Contains(strings.ToUpper(err.Error()), "AQL") {
				t.Fatalf("refusal mentions AQL: %v", err)
			}
		})
	}
}

func TestSelectorExtractionGoldenUnrestrictedMode(t *testing.T) {
	plan := pgsq03SelectorPlan("Patient",
		pgsq03RawProjection("g", pgsq03ExtractExpression(nil)),
		pgsq03PresenceSidecar("g_present", "gender"))
	plan.BindVars["auth_resource_paths_unrestricted"] = true
	plan.BindVars["auth_resource_paths"] = []string(nil)
	rendered, err := RenderSelectorExtraction(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderSelectorExtraction: %v", err)
	}
	wantSQL := `SELECT (r0."payload" #> $4::text[]) AS "c0", ` +
		`COALESCE(jsonb_typeof(r0."payload") = 'object' AND jsonb_exists(r0."payload", $5), false) AS "c1"` +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1` +
		` AND r0."dataset_generation" = $2` +
		` AND r0."resource_type" = $3` +
		` ORDER BY r0."resource_seq" ASC`
	if rendered.SQL != wantSQL {
		t.Fatalf("unrestricted SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	if strings.Contains(rendered.SQL, "auth_resource_path") {
		t.Fatalf("explicit unrestricted mode must add no auth predicate: %s", rendered.SQL)
	}
}

type pgsq03Service struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	client *control.Client
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newPGSQ03Service(t *testing.T) *pgsq03Service {
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
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) { t.Fatalf("%s: %s", step, scrub(err)) }

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := "pgsq03_it_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
	if !pgsq03ResourceNamePattern.MatchString(dbName) {
		t.Fatalf("owned database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema := "pgsq03_it_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
	if !pgsq03ResourceNamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
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
	return &pgsq03Service{ctx: ctx, admin: admin, iso: iso, client: client, schema: schema, scrub: scrub, fail: fail}
}

func (s *pgsq03Service) insertResource(t *testing.T, resourceType, logicalID string, payload any) int64 {
	t.Helper()
	var raw json.RawMessage
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		raw = encoded
	}
	repo, err := control.NewResourceRepository(s.client, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	seq, err := repo.InsertResource(s.ctx, source.Resource{
		Project:           pgsq03Project,
		DatasetGeneration: pgsq03Gen,
		ResourceType:      resourceType,
		LogicalID:         logicalID,
		AuthResourcePath:  pgsq03Path,
		Payload:           raw,
	})
	if err != nil {
		s.fail("insert resource "+resourceType+"/"+logicalID, err)
	}
	return seq
}

func (s *pgsq03Service) runRendered(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) [][]string {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	rows, err := s.iso.Query(s.ctx, rendered.SQL, args...)
	if err != nil {
		s.fail("execute rendered statement "+rendered.SQL, err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = stringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish statement", err)
	}
	return out
}

func (s *pgsq03Service) witness(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("execute direct-SQL witness "+sql, err)
	}
	defer rows.Close()
	var out [][]string
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			s.fail("read witness row", err)
		}
		row := make([]string, len(values))
		for index, value := range values {
			row[index] = stringifyCell(value)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		s.fail("finish witness", err)
	}
	return out
}

func (s *pgsq03Service) expectStored(t *testing.T, logicalID, payloadText string) {
	t.Helper()
	rows := s.witness(t, `SELECT coalesce(payload::text, '<sql-null>') FROM "`+s.schema+`".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND logical_id=$3`,
		pgsq03Project, pgsq03Gen, logicalID)
	if len(rows) != 1 || rows[0][0] != payloadText {
		t.Fatalf("decoy %s must be stored with payload %s, got %v", logicalID, payloadText, rows)
	}
}

func pgsq03LivePlan(paths []string, unrestricted, scopeAllowed bool, projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  "Patient",
			"project":                          pgsq03Project,
			"dataset_generation":               pgsq03Gen,
			"auth_resource_paths":              paths,
			"auth_resource_paths_unrestricted": unrestricted,
			"scope_allowed":                    scopeAllowed,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
		},
	}
}

func pgsq03GenderPlan(t *testing.T, behavior ir.PhysicalNullBehavior, paths []string, unrestricted, scopeAllowed bool) ir.PhysicalPlan {
	return pgsq03LivePlan(paths, unrestricted, scopeAllowed,
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		pgsq03ExtractProjection(t, "gender", "Patient", "gender", behavior, ir.PhysicalSelectorDirectScalar),
		pgsq03PresenceSidecar("gender_present", "gender"))
}

func TestPGSQ03ScalarNullBehaviorMatrixLive(t *testing.T) {
	svc := newPGSQ03Service(t)
	states := []struct {
		logicalID   string
		payload     any
		payloadText string
	}{
		{"m-absent", map[string]any{"active": true}, `{"active": true}`},
		{"m-null", json.RawMessage(`{"gender": null}`), `{"gender": null}`},
		{"m-emptyarray", json.RawMessage(`{"gender": []}`), `{"gender": []}`},
		{"m-arraynull", json.RawMessage(`{"gender": [null]}`), `{"gender": [null]}`},
		{"m-emptystring", json.RawMessage(`{"gender": ""}`), `{"gender": ""}`},
		{"m-false", json.RawMessage(`{"gender": false}`), `{"gender": false}`},
		{"m-zero", json.RawMessage(`{"gender": 0}`), `{"gender": 0}`},
		{"m-nopayload", nil, "<sql-null>"},
		{"m-male", json.RawMessage(`{"gender": "male"}`), `{"gender": "male"}`},
	}
	for _, state := range states {
		svc.insertResource(t, "Patient", state.logicalID, state.payload)
	}
	for _, state := range states {
		svc.expectStored(t, state.logicalID, state.payloadText)
	}

	order := make([]string, 0, len(states))
	for _, state := range states {
		order = append(order, state.logicalID)
	}

	cells := map[ir.PhysicalNullBehavior]map[string]struct {
		value   string
		present string
	}{
		ir.PhysicalPreserveNull: {
			"m-absent":      {"<null>", "false"},
			"m-null":        {"<null>", "true"},
			"m-emptyarray":  {"[]", "true"},
			"m-arraynull":   {"[<nil>]", "true"},
			"m-emptystring": {"", "true"},
			"m-false":       {"false", "true"},
			"m-zero":        {"0", "true"},
			"m-nopayload":   {"<null>", "false"},
			"m-male":        {"male", "true"},
		},
		ir.PhysicalOmitNulls: {
			"m-absent":      {"<null>", "false"},
			"m-null":        {"<null>", "true"},
			"m-emptyarray":  {"[]", "true"},
			"m-arraynull":   {"[<nil>]", "true"},
			"m-emptystring": {"", "true"},
			"m-false":       {"false", "true"},
			"m-zero":        {"0", "true"},
			"m-nopayload":   {"<null>", "false"},
			"m-male":        {"male", "true"},
		},
		ir.PhysicalEmptyOnNull: {
			"m-absent":      {"", "false"},
			"m-null":        {"", "true"},
			"m-emptyarray":  {"[]", "true"},
			"m-arraynull":   {"[<nil>]", "true"},
			"m-emptystring": {"", "true"},
			"m-false":       {"false", "true"},
			"m-zero":        {"0", "true"},
			"m-nopayload":   {"", "false"},
			"m-male":        {"male", "true"},
		},
	}

	for _, behavior := range []ir.PhysicalNullBehavior{
		ir.PhysicalPreserveNull, ir.PhysicalOmitNulls, ir.PhysicalEmptyOnNull,
	} {
		plan := pgsq03GenderPlan(t, behavior, []string{pgsq03Path}, false, true)
		rendered, err := RenderSelectorExtraction(plan, bindingFor(t, svc.schema))
		if err != nil {
			t.Fatalf("%s render: %v", behavior, err)
		}
		wantArgs := []command.PostgreSQLParameter{
			{Type: command.PostgreSQLParameterText, Value: pgsq03Project},
			{Type: command.PostgreSQLParameterText, Value: pgsq03Gen},
			{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
			{Type: command.PostgreSQLParameterText, Value: "Patient"},
			{Type: command.PostgreSQLParameterText, Value: "{gender}"},
			{Type: command.PostgreSQLParameterText, Value: "gender"},
		}
		if !reflect.DeepEqual(rendered.Args, wantArgs) {
			t.Fatalf("%s args = %#v, want %#v", behavior, rendered.Args, wantArgs)
		}
		got := svc.runRendered(t, rendered)
		if len(got) != len(order) {
			t.Fatalf("%s rows = %v, want %d rows", behavior, got, len(order))
		}
		for index, logicalID := range order {
			cell := cells[behavior][logicalID]
			if got[index][0] != logicalID || got[index][1] != cell.value || got[index][2] != cell.present {
				t.Fatalf("%s row %d = %v, want [%s %s %s]", behavior, index, got[index], logicalID, cell.value, cell.present)
			}
		}

		var probe string
		switch behavior {
		case ir.PhysicalPreserveNull:
			probe = `((payload #> '{gender}'::text[]) IS NULL)
                <> NOT (COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false))`
		case ir.PhysicalOmitNulls:
			probe = `(NULLIF((payload #> '{gender}'::text[]), 'null'::jsonb) IS NULL)
                <> (NOT (COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false))
                    OR jsonb_typeof(payload #> '{gender}'::text[]) = 'null')`
		case ir.PhysicalEmptyOnNull:
			probe = `(COALESCE(NULLIF((payload #> '{gender}'::text[]), 'null'::jsonb), '""'::jsonb) = '""'::jsonb)
                <> (NOT (COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false))
                    OR jsonb_typeof(payload #> '{gender}'::text[]) = 'null'
                    OR (payload #> '{gender}'::text[]) = '""'::jsonb)`
		}
		disagree := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND `+probe,
			pgsq03Project, pgsq03Gen)
		if len(disagree) != 1 || disagree[0][0] != "0" {
			t.Fatalf("%s presence/value disagreement rows = %v, want none", behavior, disagree)
		}
	}
}

func TestPGSQ03NestedPresenceDistinguishesParentsAndGuardsAreLoadBearing(t *testing.T) {
	svc := newPGSQ03Service(t)
	states := []struct {
		logicalID   string
		payload     any
		payloadText string
		value       string
		present     string
	}{
		{"n-present", json.RawMessage(`{"meta": {"source": "urn:oid:1"}}`), `{"meta": {"source": "urn:oid:1"}}`, "urn:oid:1", "true"},
		{"n-nullterminal", json.RawMessage(`{"meta": {"source": null}}`), `{"meta": {"source": null}}`, "<null>", "true"},
		{"n-parentnull", json.RawMessage(`{"meta": null}`), `{"meta": null}`, "<null>", "false"},
		{"n-parentscalar", json.RawMessage(`{"meta": 5}`), `{"meta": 5}`, "<null>", "false"},
		{"n-parentarray", json.RawMessage(`{"meta": ["source"]}`), `{"meta": ["source"]}`, "<null>", "false"},
		{"n-absent", map[string]any{"active": true}, `{"active": true}`, "<null>", "false"},
	}
	for _, state := range states {
		svc.insertResource(t, "Patient", state.logicalID, state.payload)
	}
	for _, state := range states {
		svc.expectStored(t, state.logicalID, state.payloadText)
	}

	plan := pgsq03LivePlan([]string{pgsq03Path}, false, true,
		ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}},
		pgsq03ExtractProjection(t, "meta_source", "Patient", "meta.source", ir.PhysicalPreserveNull, ir.PhysicalSelectorDirectScalar),
		pgsq03PresenceSidecar("meta_source_present", "meta", "source"))
	rendered, err := RenderSelectorExtraction(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("nested render: %v", err)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq03Project},
		{Type: command.PostgreSQLParameterText, Value: pgsq03Gen},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
		{Type: command.PostgreSQLParameterText, Value: "{meta,source}"},
		{Type: command.PostgreSQLParameterText, Value: "{meta}"},
		{Type: command.PostgreSQLParameterText, Value: "source"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("nested args = %#v, want %#v", rendered.Args, wantArgs)
	}
	got := svc.runRendered(t, rendered)
	if len(got) != len(states) {
		t.Fatalf("nested rows = %v, want %d rows", got, len(states))
	}
	for index, state := range states {
		if got[index][0] != state.logicalID || got[index][1] != state.value || got[index][2] != state.present {
			t.Fatalf("nested row %d = %v, want [%s %s %s]", index, got[index], state.logicalID, state.value, state.present)
		}
	}

	gated := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_resource
		WHERE project=$1 AND dataset_generation=$2 AND logical_id='n-parentarray'
		AND COALESCE(jsonb_typeof(payload #> '{meta}'::text[]) = 'object' AND jsonb_exists(payload #> '{meta}'::text[], 'source'), false)`,
		pgsq03Project, pgsq03Gen)
	if gated[0][0] != "0" {
		t.Fatalf("gated presence must reject the array-contains decoy, got %v", gated)
	}
	ungated := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_resource
		WHERE project=$1 AND dataset_generation=$2 AND logical_id='n-parentarray'
		AND COALESCE(jsonb_exists(payload #> '{meta}'::text[], 'source'), false)`,
		pgsq03Project, pgsq03Gen)
	if ungated[0][0] != "1" {
		t.Fatalf("the typeof gate is not load-bearing: without it the decoy still reads false (%v)", ungated)
	}

	svc.insertResource(t, "Patient", "g-nodoc", nil)
	totally := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_resource
		WHERE project=$1 AND dataset_generation=$2 AND logical_id='g-nodoc'
		AND (jsonb_typeof(payload #> '{meta}'::text[]) = 'object' AND jsonb_exists(payload #> '{meta}'::text[], 'source')) IS NULL`,
		pgsq03Project, pgsq03Gen)
	if totally[0][0] != "1" {
		t.Fatalf("without COALESCE the presence bit must go NULL for an absent document, got %v", totally)
	}
	withCOALESCE := svc.witness(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_resource
		WHERE project=$1 AND dataset_generation=$2 AND logical_id='g-nodoc'
		AND COALESCE(jsonb_typeof(payload #> '{meta}'::text[]) = 'object' AND jsonb_exists(payload #> '{meta}'::text[], 'source'), false) IS NULL`,
		pgsq03Project, pgsq03Gen)
	if withCOALESCE[0][0] != "0" {
		t.Fatalf("with COALESCE the presence bit must never be NULL, got %v", withCOALESCE)
	}

	collapsed := svc.witness(t, `SELECT payload #>> '{meta,source}'::text[] FROM "`+svc.schema+`".fhir_resource
		WHERE project=$1 AND dataset_generation=$2 AND logical_id='n-nullterminal'`, pgsq03Project, pgsq03Gen)
	if len(collapsed) != 1 || collapsed[0][0] != "<null>" {
		t.Fatalf("baseline: #>> is expected to collapse the JSON null (proving the choice of #> is load-bearing), got %v", collapsed)
	}
	kept := svc.witness(t, `SELECT (payload #> '{meta,source}'::text[])::text FROM "`+svc.schema+`".fhir_resource
		WHERE project=$1 AND dataset_generation=$2 AND logical_id='n-nullterminal'`, pgsq03Project, pgsq03Gen)
	if len(kept) != 1 || kept[0][0] != "null" {
		t.Fatalf("the rendered jsonb form must keep the stored JSON null distinct, got %v", kept)
	}
}

func TestPGSQ03NullKeywordPayloadPathsReadStoredValuesLive(t *testing.T) {
	svc := newPGSQ03Service(t)
	for _, state := range []struct{ logicalID, payload string }{
		{"k-value", `{"null": "stored-value"}`},
		{"k-jsonnull", `{"null": null}`},
		{"k-absent", `{"gender": "female"}`},
		{"k-nested", `{"null": {"x": 7}}`},
		{"k-nestedarray", `{"null": ["x"]}`},
		{"k-upper", `{"NULL": "upper-value"}`},
	} {
		svc.insertResource(t, "Patient", state.logicalID, json.RawMessage(state.payload))
		svc.expectStored(t, state.logicalID, state.payload)
	}

	filterPlan := func(path []string, bindKey string, value any) ir.PhysicalPlan {
		plan := pgsq03LivePlan([]string{pgsq03Path}, false, true,
			ir.PhysicalProjection{Name: "logical_id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}})
		plan.BindVars[bindKey] = value
		predicate := ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: path},
			Right: &ir.PhysicalValue{BindKey: bindKey},
		}}}
		plan.Operations = append(plan.Operations[:1], append([]ir.PhysicalOperation{predicate}, plan.Operations[1:]...)...)
		return plan
	}
	runWithNaivePath := func(t *testing.T, rendered command.RenderedPostgresPhysicalPlan, pathIndex int) [][]string {
		t.Helper()
		naive := append([]command.PostgreSQLParameter(nil), rendered.Args...)
		text, ok := naive[pathIndex].Value.(string)
		if !ok || text[0] != '{' {
			t.Fatalf("argument %d is not an array literal: %#v", pathIndex, naive[pathIndex])
		}
		naive[pathIndex].Value = strings.ReplaceAll(strings.ReplaceAll(text, `"null"`, "null"), `"NULL"`, "NULL")
		return svc.runRendered(t, command.RenderedPostgresPhysicalPlan{SQL: rendered.SQL, Args: naive})
	}

	binding := bindingFor(t, svc.schema)

	rendered, err := RenderPhysicalPlan(filterPlan([]string{"null"}, "null_value", "stored-value"), binding)
	if err != nil {
		t.Fatalf("null-key filter render: %v", err)
	}
	if text, ok := rendered.Args[4].Value.(string); !ok || text != `{"null"}` {
		t.Fatalf("null-key path arg = %#v, want {\"null\"}", rendered.Args[4])
	}
	if got := svc.runRendered(t, rendered); !reflect.DeepEqual(got, [][]string{{"k-value"}}) {
		t.Fatalf("null-key filter rows = %v, want [k-value]", got)
	}
	if got := runWithNaivePath(t, rendered, 4); len(got) != 0 {
		t.Fatalf("the same statement with the naive unquoted {null} element must read nothing, got %v", got)
	}

	nested, err := RenderPhysicalPlan(filterPlan([]string{"null", "x"}, "seven_value", float64(7)), binding)
	if err != nil {
		t.Fatalf("nested null-key filter render: %v", err)
	}
	if text, ok := nested.Args[4].Value.(string); !ok || text != `{"null",x}` {
		t.Fatalf("nested null-key path arg = %#v, want {\"null\",x}", nested.Args[4])
	}
	if got := svc.runRendered(t, nested); !reflect.DeepEqual(got, [][]string{{"k-nested"}}) {
		t.Fatalf("nested null-key filter rows = %v, want [k-nested]", got)
	}
	if got := runWithNaivePath(t, nested, 4); len(got) != 0 {
		t.Fatalf("the same nested statement with a naive unquoted prefix must read nothing, got %v", got)
	}

	upper, err := RenderPhysicalPlan(filterPlan([]string{"NULL"}, "upper_value", "upper-value"), binding)
	if err != nil {
		t.Fatalf("upper null-key filter render: %v", err)
	}
	if text, ok := upper.Args[4].Value.(string); !ok || text != `{"NULL"}` {
		t.Fatalf("upper null-key path arg = %#v, want {\"NULL\"}", upper.Args[4])
	}
	if got := svc.runRendered(t, upper); !reflect.DeepEqual(got, [][]string{{"k-upper"}}) {
		t.Fatalf("upper null-key filter rows = %v, want [k-upper]", got)
	}
	if got := runWithNaivePath(t, upper, 4); len(got) != 0 {
		t.Fatalf("the same upper-case statement with the naive unquoted NULL element must read nothing, got %v", got)
	}
	storedUpper := svc.witness(t, `SELECT payload #> '{"NULL"}'::text[] FROM "`+svc.schema+`".fhir_resource
		WHERE project=$1 AND dataset_generation=$2 AND logical_id='k-upper'`, pgsq03Project, pgsq03Gen)
	if len(storedUpper) != 1 || storedUpper[0][0] != "upper-value" {
		t.Fatalf("the stored upper-case null-named key must read its value, got %v", storedUpper)
	}

	r := &planRenderer{storage: binding, parameters: map[string]int{}, rows: map[string]rowScope{}}
	presence, err := r.propertyPresenceExpression("r0", []string{"null", "x"})
	if err != nil {
		t.Fatalf("null-key presence render: %v", err)
	}
	if len(r.args) != 2 {
		t.Fatalf("presence bound %d args, want quoted prefix + key", len(r.args))
	}
	if text, ok := r.args[0].Value.(string); !ok || text != `{"null"}` {
		t.Fatalf("presence prefix arg = %#v, want {\"null\"}", r.args[0])
	}
	if text, ok := r.args[1].Value.(string); !ok || text != "x" {
		t.Fatalf("presence key arg = %#v, want x", r.args[1])
	}
	presenceSQL := `SELECT logical_id, ` + presence + ` FROM "` + svc.schema + `".fhir_resource AS r0` +
		` WHERE r0."project"=$3 AND r0."dataset_generation"=$4 AND r0."resource_type"='Patient' ORDER BY r0."resource_seq"`
	got := svc.witness(t, presenceSQL, r.args[0].Value, r.args[1].Value, pgsq03Project, pgsq03Gen)
	want := [][]string{
		{"k-value", "false"}, {"k-jsonnull", "false"}, {"k-absent", "false"},
		{"k-nested", "true"}, {"k-nestedarray", "false"}, {"k-upper", "false"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("null-key presence rows = %v, want %v", got, want)
	}
	naive := svc.witness(t, presenceSQL, "{null}", r.args[1].Value, pgsq03Project, pgsq03Gen)
	for _, row := range naive {
		if row[1] != "false" {
			t.Fatalf("with the naive unquoted prefix no row may prove presence, got %v", naive)
		}
	}
}

func TestPGSQ03DenyPathsReadZeroRowsWithStoredDecoys(t *testing.T) {
	svc := newPGSQ03Service(t)
	svc.insertResource(t, "Patient", "d-visible", json.RawMessage(`{"gender": "female"}`))
	svc.insertResource(t, "Patient", "d-null", json.RawMessage(`{"gender": null}`))
	svc.expectStored(t, "d-visible", `{"gender": "female"}`)
	svc.expectStored(t, "d-null", `{"gender": null}`)

	binding := bindingFor(t, svc.schema)
	extractor := func(paths []string, unrestricted, allowed bool) command.RenderedPostgresPhysicalPlan {
		plan := pgsq03GenderPlan(t, ir.PhysicalPreserveNull, paths, unrestricted, allowed)
		rendered, err := RenderSelectorExtraction(plan, binding)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		return rendered
	}

	denied := extractor([]string{pgsq03Path}, false, false)
	if !strings.Contains(denied.SQL, " AND FALSE") {
		t.Fatalf("canonical deny must render the literal zero-row conjunct inherited from scope.go: %s", denied.SQL)
	}
	if got := svc.runRendered(t, denied); len(got) != 0 {
		t.Fatalf("canonical scope_allowed=false deny read %v, want zero rows", got)
	}
	empty := extractor([]string{}, false, true)
	if got := svc.runRendered(t, empty); len(got) != 0 {
		t.Fatalf("restricted empty auth read %v, want zero rows", got)
	}
	visible := extractor(nil, true, true)
	want := [][]string{{"d-visible", "female", "true"}, {"d-null", "<null>", "true"}}
	if got := svc.runRendered(t, visible); !reflect.DeepEqual(got, want) {
		t.Fatalf("unrestricted control rows = %v, want %v", got, want)
	}
}

func TestPGSQ03ExtractionIsConnectionIndependent(t *testing.T) {
	svc := newPGSQ03Service(t)
	svc.insertResource(t, "Patient", "c-female", json.RawMessage(`{"gender": "female"}`))
	svc.insertResource(t, "Patient", "c-missing", map[string]any{"active": true})

	rendered, err := RenderSelectorExtraction(pgsq03GenderPlan(t, ir.PhysicalPreserveNull, []string{pgsq03Path}, false, true), bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}

	connA, err := svc.iso.Acquire(svc.ctx)
	if err != nil {
		svc.fail("acquire connection A", err)
	}
	defer connA.Release()
	connB, err := svc.iso.Acquire(svc.ctx)
	if err != nil {
		svc.fail("acquire connection B", err)
	}
	defer connB.Release()

	var pidA, pidB int
	if err := connA.QueryRow(svc.ctx, `SELECT pg_backend_pid()`).Scan(&pidA); err != nil {
		svc.fail("backend pid A", err)
	}
	if err := connB.QueryRow(svc.ctx, `SELECT pg_backend_pid()`).Scan(&pidB); err != nil {
		svc.fail("backend pid B", err)
	}
	if pidA == pidB || pidA == 0 || pidB == 0 {
		t.Fatalf("the two executions must run on distinct real backends, got %d and %d", pidA, pidB)
	}

	readRows := func(conn *pgxpool.Conn) [][]string {
		rows, err := conn.Query(svc.ctx, rendered.SQL, args...)
		if err != nil {
			svc.fail("execute on backend "+strconv.Itoa(pidA)+"/"+strconv.Itoa(pidB), err)
		}
		defer rows.Close()
		var out [][]string
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				svc.fail("read row", err)
			}
			row := make([]string, len(values))
			for index, value := range values {
				row[index] = stringifyCell(value)
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			svc.fail("finish statement", err)
		}
		return out
	}
	want := [][]string{{"c-female", "female", "true"}, {"c-missing", "<null>", "false"}}
	rowsA := readRows(connA)
	rowsB := readRows(connB)
	if !reflect.DeepEqual(rowsA, want) || !reflect.DeepEqual(rowsB, want) {
		t.Fatalf("connection-dependent rows: A=%v B=%v want %v", rowsA, rowsB, want)
	}
}

func TestPGSQ03LoweredShapeEndToEnd(t *testing.T) {
	svc := newPGSQ03Service(t)
	svc.insertResource(t, "Patient", "l-full", json.RawMessage(`{"gender": "female", "meta": {"source": "urn:x"}}`))
	svc.insertResource(t, "Patient", "l-empty", map[string]any{"active": true})

	documentSource := pgsq03DocumentSource()
	genderSelector, err := spec.ParseSelector("gender")
	if err != nil {
		t.Fatalf("ParseSelector: %v", err)
	}
	metaSelector, err := spec.ParseSelector("meta.source")
	if err != nil {
		t.Fatalf("ParseSelector: %v", err)
	}
	plan := pgsq03LivePlan([]string{pgsq03Path}, false, true,
		ir.PhysicalProjection{
			Name: "gender",
			Expression: &ir.PhysicalExpression{
				Kind:         ir.PhysicalExtractExpression,
				Cardinality:  ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Extract: &ir.PhysicalExtract{
					Source:        documentSource,
					ResourceType:  "Patient",
					Selector:      genderSelector,
					ExecutionMode: ir.PhysicalSelectorDirectScalar,
				},
			},
			Presence: &ir.PhysicalProjectionPresence{Source: documentSource, Paths: [][]string{{"gender"}}},
		},
		ir.PhysicalProjection{Name: "gender_present", Presence: &ir.PhysicalProjectionPresence{Source: documentSource, Paths: [][]string{{"gender"}}}, PresenceOutput: true},
		ir.PhysicalProjection{
			Name: "meta_source",
			Expression: &ir.PhysicalExpression{
				Kind:         ir.PhysicalExtractExpression,
				Cardinality:  ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Extract: &ir.PhysicalExtract{
					Source:        documentSource,
					ResourceType:  "Patient",
					Selector:      metaSelector,
					ExecutionMode: ir.PhysicalSelectorDirectScalar,
				},
			},
			Presence: &ir.PhysicalProjectionPresence{Source: documentSource, Paths: [][]string{{"meta", "source"}}},
		},
		ir.PhysicalProjection{Name: "meta_source_present", Presence: &ir.PhysicalProjectionPresence{Source: documentSource, Paths: [][]string{{"meta", "source"}}}, PresenceOutput: true})

	rendered, err := RenderSelectorExtraction(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("lowered-shape render: %v", err)
	}
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"gender", "gender_present", "meta_source", "meta_source_present"}) {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}
	selectPrefix := rendered.SQL[:strings.Index(rendered.SQL, " FROM ")]
	for _, forbidden := range []string{"@", "gender", "meta", "urn", "Patient", pgsq03Project, pgsq03Gen, pgsq03Path} {
		if strings.Contains(selectPrefix, forbidden) {
			t.Fatalf("lowered-shape SQL leaked %q: %s", forbidden, rendered.SQL)
		}
	}
	got := svc.runRendered(t, rendered)
	wantRows := [][]string{
		{"female", "true", "urn:x", "true"},
		{"<null>", "false", "<null>", "false"},
	}
	if len(got) != len(wantRows) {
		t.Fatalf("lowered-shape rows = %v, want %v", got, wantRows)
	}
	for index, row := range wantRows {
		if got[index][0] != row[0] || got[index][1] != row[1] || got[index][2] != row[2] || got[index][3] != row[3] {
			t.Fatalf("lowered-shape row %d = %v, want %v", index, got[index], row)
		}
	}
	witness := svc.witness(t, `SELECT payload #> '{gender}'::text[],
			COALESCE(jsonb_typeof(payload) = 'object' AND jsonb_exists(payload, 'gender'), false),
			payload #> '{meta,source}'::text[],
			COALESCE(jsonb_typeof(payload #> '{meta}'::text[]) = 'object' AND jsonb_exists(payload #> '{meta}'::text[], 'source'), false)
		FROM "`+svc.schema+`".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' ORDER BY resource_seq`,
		pgsq03Project, pgsq03Gen)
	if len(witness) != len(wantRows) {
		t.Fatalf("witness rows = %v", witness)
	}
	for index, row := range wantRows {
		if witness[index][0] != row[0] || witness[index][1] != row[1] || witness[index][2] != row[2] || witness[index][3] != row[3] {
			t.Fatalf("witness row %d = %v, want %v", index, witness[index], row)
		}
	}
}

func TestZZNoLeftoverPgSQ03Resources(t *testing.T) {
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
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'pgsq03_it_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d pgsq03_it_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgsq03_it_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d pgsq03_it_* schemas still exist in the service database", schemas)
	}
}
