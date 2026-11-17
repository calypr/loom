package postgres

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	pgsq01Project  = "pgsq01-proj"
	pgsq01Other    = "pgsq01-other"
	pgsq01Gen      = "gen-1"
	pgsq01GenTwo   = "gen-2"
	pgsq01Path     = "FHIR/Patient"
	pgsq01ConfPath = "FHIR/Confidential"
)

var pgsq01ResourceNamePattern = regexp.MustCompile(`^pgsq01_it_[0-9]+_[0-9a-f]+$`)

func pgsq01Plan(project, generation string, paths []string, unrestricted bool, resourceType string, columns ...string) ir.PhysicalPlan {
	projections := make([]ir.PhysicalProjection, 0, len(columns))
	for _, column := range columns {
		projections = append(projections, ir.PhysicalProjection{
			Name:  column,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{column}},
		})
	}
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  resourceType,
			"project":                          project,
			"dataset_generation":               generation,
			"auth_resource_paths":              paths,
			"auth_resource_paths_unrestricted": unrestricted,
			"scope_allowed":                    true,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
		},
	}
}

func TestRootScanGoldenRestrictedSQLAndArguments(t *testing.T) {
	plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path, "FHIR/Encounter"}, false, "Patient", "logical_id", "resource_type")
	binding := pgsq01UnitBinding(t)

	rendered, err := RenderRootScan(plan, binding)
	if err != nil {
		t.Fatalf("RenderRootScan: %v", err)
	}
	const wantSQL = `SELECT r0."logical_id" AS "c0", r0."resource_type" AS "c1"` +
		` FROM "pgsq01_it_unit"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1` +
		` AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4` +
		` ORDER BY r0."resource_seq" ASC`
	if rendered.SQL != wantSQL {
		t.Fatalf("restricted SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq01Project},
		{Type: command.PostgreSQLParameterText, Value: pgsq01Gen},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient","FHIR/Encounter"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("restricted args = %#v, want %#v", rendered.Args, wantArgs)
	}
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"logical_id", "resource_type"}) {
		t.Fatalf("output columns = %v", rendered.OutputColumns)
	}

	for _, forbidden := range []string{"@", "DISTINCT", "GROUP BY", "JOIN", "LIMIT", "OFFSET",
		pgsq01Project, pgsq01Gen, pgsq01Path, "Patient", "FHIR/Encounter"} {
		if strings.Contains(rendered.SQL, forbidden) {
			t.Fatalf("restricted SQL leaked %q: %s", forbidden, rendered.SQL)
		}
	}

	second, err := RenderRootScan(plan, binding)
	if err != nil || second.SQL != rendered.SQL || !reflect.DeepEqual(second.Args, rendered.Args) {
		t.Fatalf("second render drifted: %v %#v", err, second)
	}

	mutable := plan.BindVars["auth_resource_paths"].([]string)
	mutable[0] = "FHIR/Stolen"
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("earlier rendered args were aliased to plan data: %#v", rendered.Args)
	}
	third, err := RenderRootScan(plan, binding)
	if err != nil {
		t.Fatalf("third render after mutation: %v", err)
	}
	if !reflect.DeepEqual(third.Args[2].Value, json.RawMessage(`["FHIR/Stolen","FHIR/Encounter"]`)) {
		t.Fatalf("plan-mutated args = %#v", third.Args[2].Value)
	}
}

func TestRootScanGoldenUnrestrictedSQLAndArguments(t *testing.T) {
	plan := pgsq01Plan(pgsq01Project, "", nil, true, "Observation", "logical_id", "auth_resource_path")
	rendered, err := RenderRootScan(plan, pgsq01UnitBinding(t))
	if err != nil {
		t.Fatalf("RenderRootScan: %v", err)
	}
	const wantSQL = `SELECT r0."logical_id" AS "c0", r0."auth_resource_path" AS "c1"` +
		` FROM "pgsq01_it_unit"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1` +
		` AND r0."dataset_generation" = $2` +
		` AND r0."resource_type" = $3` +
		` ORDER BY r0."resource_seq" ASC`
	if rendered.SQL != wantSQL {
		t.Fatalf("unrestricted SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq01Project},
		{Type: command.PostgreSQLParameterText, Value: ""},
		{Type: command.PostgreSQLParameterText, Value: "Observation"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("unrestricted args = %#v, want %#v", rendered.Args, wantArgs)
	}
	if strings.Contains(rendered.SQL, "auth_resource_path =") {
		t.Fatalf("explicit unrestricted mode must add no auth predicate: %s", rendered.SQL)
	}
}

func TestRootScanEmptyRestrictedPathSetRendersEmptyArrayParameter(t *testing.T) {
	for _, paths := range [][]string{{}, nil} {
		plan := pgsq01Plan(pgsq01Project, pgsq01Gen, paths, false, "Patient", "logical_id")
		rendered, err := RenderRootScan(plan, pgsq01UnitBinding(t))
		if err != nil {
			t.Fatalf("RenderRootScan: %v", err)
		}
		if got, ok := rendered.Args[2].Value.(json.RawMessage); !ok || string(got) != "[]" {
			t.Fatalf("empty restricted set arg = %#v, want JSON empty array", rendered.Args[2].Value)
		}
		if rendered.Args[2].Type != command.PostgreSQLParameterJSONB {
			t.Fatalf("empty restricted set type = %q", rendered.Args[2].Type)
		}
	}
}

func pgsq01UnitBinding(t *testing.T) ir.PostgresStorageBinding {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("pgsq01_it_unit")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	return binding
}

func pgsq01PlanWithOperations(operations ...ir.PhysicalOperation) ir.PhysicalPlan {
	plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
	plan.Operations = operations
	return plan
}

func pgsq01ReturnOp(columns ...string) ir.PhysicalOperation {
	projections := make([]ir.PhysicalProjection, 0, len(columns))
	for _, column := range columns {
		projections = append(projections, ir.PhysicalProjection{
			Name:  column,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{column}},
		})
	}
	return ir.PhysicalOperation{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}}
}

func pgsq01RootScanOp() ir.PhysicalOperation {
	return ir.PhysicalOperation{Kind: ir.PhysicalRootScanOp,
		RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}}
}

func pgsq01Projections(projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient")
	plan.Operations[1].Return.Projections = projections
	return plan
}

func TestRootScanRejectsEveryShapeOutsideTheClosedSlice(t *testing.T) {
	binding := pgsq01UnitBinding(t)
	cases := []struct {
		name   string
		plan   ir.PhysicalPlan
		wantIn string
	}{
		{"no operations", pgsq01PlanWithOperations(), "exactly one ROOT_SCAN"},
		{"scan without return", pgsq01PlanWithOperations(pgsq01RootScanOp()), "exactly one ROOT_SCAN"},
		{"filter inserted", pgsq01PlanWithOperations(pgsq01RootScanOp(),
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}},
				Right: &ir.PhysicalValue{BindKey: "value"}}}},
			pgsq01ReturnOp("logical_id")), "exactly one ROOT_SCAN"},
		{"traversal inserted", pgsq01PlanWithOperations(pgsq01RootScanOp(),
			ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
				SourceVariable: "root", TargetVariable: "t", Direction: ir.PhysicalOutbound,
				EdgeLabelBindKey: "edge_label", TargetTypeBindKey: "target_type",
				EdgeTargetTypeField: "to_type", Strategy: ir.PhysicalTraversalNative}},
			pgsq01ReturnOp("logical_id")), "exactly one ROOT_SCAN"},
		{"sort inserted", pgsq01PlanWithOperations(pgsq01RootScanOp(),
			ir.PhysicalOperation{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{
				Keys: []ir.PhysicalValue{{Variable: "root", Path: []string{"logical_id"}}}}},
			pgsq01ReturnOp("logical_id")), "exactly one ROOT_SCAN"},
		{"limit inserted", pgsq01PlanWithOperations(pgsq01RootScanOp(),
			ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: "limit"}},
			pgsq01ReturnOp("logical_id")), "exactly one ROOT_SCAN"},
		{"return before scan", pgsq01PlanWithOperations(pgsq01ReturnOp("logical_id"), pgsq01RootScanOp()),
			"leading operation must be ROOT_SCAN"},
		{"second scan instead of return", pgsq01PlanWithOperations(pgsq01RootScanOp(), pgsq01RootScanOp()),
			"terminal operation must be RETURN"},
		{"root scan without payload", pgsq01PlanWithOperations(
			ir.PhysicalOperation{Kind: ir.PhysicalRootScanOp}, pgsq01ReturnOp("logical_id")),
			"payload set"},
		{"return without payload", pgsq01PlanWithOperations(pgsq01RootScanOp(),
			ir.PhysicalOperation{Kind: ir.PhysicalReturnOp}), "RETURN with its payload set"},
		{"unknown operation", pgsq01PlanWithOperations(pgsq01RootScanOp(),
			ir.PhysicalOperation{Kind: ir.PhysicalOperationKind("NOPE")}), "terminal operation must be RETURN"},
		{"population root", func() ir.PhysicalPlan {
			plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
			plan.Operations[0].RootScan.Population = &ir.PhysicalPopulationRootSource{}
			return plan
		}(), "not registered for PostgreSQL"},
		{"cohort root", func() ir.PhysicalPlan {
			plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
			plan.Operations[0].RootScan.CohortSource = &ir.PhysicalCohortRootSource{}
			return plan
		}(), "not registered for PostgreSQL"},
		{"candidate seed root", func() ir.PhysicalPlan {
			plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
			plan.Operations[0].RootScan.PageCandidateSeed = &ir.PhysicalRootPageCandidateSeed{}
			return plan
		}(), "not registered for PostgreSQL"},
		{"unnamed root variable", func() ir.PhysicalPlan {
			plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
			plan.Operations[0].RootScan.Variable = ""
			return plan
		}(), "must name its row variable"},
		{"missing collection bind key", func() ir.PhysicalPlan {
			plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
			plan.Operations[0].RootScan.CollectionBindKey = ""
			return plan
		}(), "bind carrying the single resource type"},
		{"collection bind not a string", func() ir.PhysicalPlan {
			plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
			plan.BindVars["root_collection"] = 7
			return plan
		}(), "root collection bind"},
		{"empty return", func() ir.PhysicalPlan {
			return pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient")
		}(), "at least one typed scope column"},
		{"blank projection name", pgsq01Projections(ir.PhysicalProjection{
			Name: " ", Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}),
			"projection names are required"},
		{"expression projection", pgsq01Projections(ir.PhysicalProjection{
			Name: "logical_id",
			Expression: &ir.PhysicalExpression{Kind: ir.PhysicalValueExpression,
				Value: &ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}}),
			"expression form"},
		{"hidden projection", pgsq01Projections(ir.PhysicalProjection{
			Name: "logical_id", Hidden: true,
			Value: ir.PhysicalValue{Variable: "root", Path: []string{"logical_id"}}}),
			"presence or hidden"},
		{"bind-value projection", pgsq01Projections(ir.PhysicalProjection{
			Name: "v", Value: ir.PhysicalValue{BindKey: "value"}}),
			"not a bind value"},
		{"foreign variable projection", pgsq01Projections(ir.PhysicalProjection{
			Name: "v", Value: ir.PhysicalValue{Variable: "other", Path: []string{"logical_id"}}}),
			"root row variable"},
		{"payload projection", pgsq01Projections(ir.PhysicalProjection{
			Name: "status", Value: ir.PhysicalValue{Variable: "root", Path: []string{"status"}}}),
			"typed scope column"},
		{"multi-segment projection", pgsq01Projections(ir.PhysicalProjection{
			Name: "nested", Value: ir.PhysicalValue{Variable: "root", Path: []string{"meta", "tag"}}}),
			"typed scope column"},
		{"storage sequence projection", pgsq01Projections(ir.PhysicalProjection{
			Name: "seq", Value: ir.PhysicalValue{Variable: "root", Path: []string{"resource_seq"}}}),
			"typed scope column"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rendered, err := RenderRootScan(testCase.plan, binding)
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

func TestRootScanRejectsMissingOrContradictoryScope(t *testing.T) {
	binding := pgsq01UnitBinding(t)
	cases := []struct {
		name   string
		mutate func(plan ir.PhysicalPlan)
		wantIn string
	}{
		{"project missing", func(p ir.PhysicalPlan) { delete(p.BindVars, "project") }, `"project"`},
		{"project blank", func(p ir.PhysicalPlan) { p.BindVars["project"] = "  " }, `"project"`},
		{"project wrong type", func(p ir.PhysicalPlan) { p.BindVars["project"] = 9 }, `"project"`},
		{"generation missing", func(p ir.PhysicalPlan) { delete(p.BindVars, "dataset_generation") }, `"dataset_generation"`},
		{"generation wrong type", func(p ir.PhysicalPlan) { p.BindVars["dataset_generation"] = 1 }, `"dataset_generation"`},
		{"auth mode missing", func(p ir.PhysicalPlan) { delete(p.BindVars, "auth_resource_paths_unrestricted") },
			`"auth_resource_paths_unrestricted"`},
		{"auth mode wrong type", func(p ir.PhysicalPlan) { p.BindVars["auth_resource_paths_unrestricted"] = "yes" },
			`"auth_resource_paths_unrestricted"`},
		{"unrestricted with paths", func(p ir.PhysicalPlan) {
			p.BindVars["auth_resource_paths_unrestricted"] = true
			p.BindVars["auth_resource_paths"] = []string{pgsq01Path}
		}, "unrestricted mode must not carry auth resource paths"},
		{"paths wrong type", func(p ir.PhysicalPlan) { p.BindVars["auth_resource_paths"] = "FHIR/Patient" },
			`"auth_resource_paths"`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
			testCase.mutate(plan)
			rendered, err := RenderRootScan(plan, binding)
			if !errors.Is(err, ErrInvalidPostgresPlanScope) {
				t.Fatalf("error = %v, want ErrInvalidPostgresPlanScope", err)
			}
			if rendered.SQL != "" || rendered.Args != nil || rendered.OutputColumns != nil {
				t.Fatalf("rejected scope produced output: %#v", rendered)
			}
			if !strings.Contains(err.Error(), testCase.wantIn) {
				t.Fatalf("error %q must name %q", err.Error(), testCase.wantIn)
			}
		})
	}
}

func TestRootScanRejectsUntrustedStorageBinding(t *testing.T) {
	plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id")
	if _, err := RenderRootScan(plan, ir.PostgresStorageBinding{}); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("zero binding error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
	if _, err := ir.NewPostgresStorageBinding("PGSQ01"); !errors.Is(err, ir.ErrUnregisteredPostgresIdentifier) {
		t.Fatalf("uppercase schema error = %v, want ErrUnregisteredPostgresIdentifier", err)
	}
}

type pgsq01Service struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	client *control.Client
	schema string
	scrub  func(string) string
	fail   func(step string, err error)
}

func newPGSQ01Service(t *testing.T) *pgsq01Service {
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

	dbName := pgsq01ResourceName(t)
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

	schema := pgsq01ResourceName(t)
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
	return &pgsq01Service{ctx: ctx, admin: admin, iso: iso, client: client, schema: schema, scrub: scrub, fail: fail}
}

func pgsq01ResourceName(t *testing.T) string {
	t.Helper()
	name := "pgsq01_it_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
	if !pgsq01ResourceNamePattern.MatchString(name) {
		t.Fatalf("owned resource name %q does not match the required pattern", name)
	}
	return name
}

func (s *pgsq01Service) insertResource(t *testing.T, project, generation, resourceType, logicalID, authPath string, payload map[string]any) int64 {
	t.Helper()
	repo, err := control.NewResourceRepository(s.client, s.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	var raw json.RawMessage
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		raw = encoded
	}
	seq, err := repo.InsertResource(s.ctx, source.Resource{
		Project:           project,
		DatasetGeneration: generation,
		ResourceType:      resourceType,
		LogicalID:         logicalID,
		AuthResourcePath:  authPath,
		Payload:           raw,
	})
	if err != nil {
		s.fail("insert resource "+resourceType+"/"+logicalID, err)
	}
	return seq
}

func (s *pgsq01Service) run(t *testing.T, sql string, args ...any) [][]string {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, sql, args...)
	if err != nil {
		s.fail("execute statement "+sql, err)
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

func (s *pgsq01Service) runRendered(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) [][]string {
	t.Helper()
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	return s.run(t, rendered.SQL, args...)
}

func (s *pgsq01Service) expectRefused(t *testing.T, sql string, wantCode, wantConstraint string, args ...any) {
	t.Helper()
	_, err := s.iso.Exec(s.ctx, sql, args...)
	if err == nil {
		t.Fatalf("statement was accepted, want refusal by %s: %s", wantConstraint, sql)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		s.fail("expect pg error for "+sql, err)
	}
	if pgErr.Code != wantCode || pgErr.ConstraintName != wantConstraint {
		s.fail("statement refused by ("+pgErr.Code+","+pgErr.ConstraintName+
			") want ("+wantCode+","+wantConstraint+")", err)
	}
}

func TestPGSQ01RootScanReturnsOnlySelectedProjectGenerationType(t *testing.T) {
	svc := newPGSQ01Service(t)
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-1", pgsq01Path, map[string]any{"code": "shared"})
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-dup-a", pgsq01Path, map[string]any{"code": "shared"})
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-dup-b", pgsq01Path, map[string]any{"code": "shared"})
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-plain", pgsq01Path, map[string]any{"code": "other"})
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Observation", "p-1", pgsq01Path, nil)
	svc.insertResource(t, pgsq01Project, pgsq01GenTwo, "Patient", "p-1", pgsq01Path, nil)
	svc.insertResource(t, pgsq01Other, pgsq01Gen, "Patient", "p-1", pgsq01Path, nil)
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-hidden", pgsq01ConfPath, nil)

	plan := pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id", "resource_type")
	rendered, err := RenderRootScan(plan, bindingFor(t, svc.schema))
	if err != nil {
		t.Fatalf("RenderRootScan: %v", err)
	}

	wantArgs := []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: pgsq01Project},
		{Type: command.PostgreSQLParameterText, Value: pgsq01Gen},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
	}
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", rendered.Args, wantArgs)
	}

	want := [][]string{
		{"p-1", "Patient"},
		{"p-dup-a", "Patient"},
		{"p-dup-b", "Patient"},
		{"p-plain", "Patient"},
	}
	got := svc.runRendered(t, rendered)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("root scan rows = %v, want %v", got, want)
	}

	witness := svc.run(t,
		`SELECT logical_id, resource_type FROM "`+svc.schema+`"."fhir_resource"`+
			` WHERE project = 'pgsq01-proj' AND dataset_generation = 'gen-1'`+
			` AND auth_resource_path = 'FHIR/Patient' AND resource_type = 'Patient'`+
			` ORDER BY resource_seq`)
	if !reflect.DeepEqual(witness, want) {
		t.Fatalf("witness rows = %v, want %v", witness, want)
	}

	for _, probe := range []struct {
		desc, sql string
		args      []any
	}{
		{"observation decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Observation' AND logical_id='p-1'`, []any{pgsq01Project, pgsq01Gen}},
		{"generation decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='p-1'`, []any{pgsq01Project, pgsq01GenTwo}},
		{"project decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='p-1'`, []any{pgsq01Other, pgsq01Gen}},
		{"auth decoy", `SELECT count(*) FROM "` + svc.schema + `".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id='p-hidden'`, []any{pgsq01Project, pgsq01Gen}},
	} {
		rows := svc.run(t, probe.sql, probe.args...)
		if len(rows) != 1 || rows[0][0] != "1" {
			t.Fatalf("%s must exist: %v", probe.desc, rows)
		}
	}

	type probe struct {
		desc     string
		sql      string
		args     []any
		wantRows [][]string
	}
	base := `SELECT logical_id FROM "` + svc.schema + `".fhir_resource WHERE `
	const orderTail = ` ORDER BY resource_seq`
	for _, probe := range []probe{
		{"full chain (control)", base + `project=$1 AND dataset_generation=$2 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb)) AND resource_type=$4` + orderTail,
			[]any{pgsq01Project, pgsq01Gen, json.RawMessage(`["FHIR/Patient"]`), "Patient"},
			[][]string{{"p-1"}, {"p-dup-a"}, {"p-dup-b"}, {"p-plain"}}},
		{"without project", base + `dataset_generation=$1 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($2::jsonb)) AND resource_type=$3` + orderTail,
			[]any{pgsq01Gen, json.RawMessage(`["FHIR/Patient"]`), "Patient"},
			[][]string{{"p-1"}, {"p-dup-a"}, {"p-dup-b"}, {"p-plain"}, {"p-1"}}},
		{"without generation", base + `project=$1 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($2::jsonb)) AND resource_type=$3` + orderTail,
			[]any{pgsq01Project, json.RawMessage(`["FHIR/Patient"]`), "Patient"},
			[][]string{{"p-1"}, {"p-dup-a"}, {"p-dup-b"}, {"p-plain"}, {"p-1"}}},
		{"without auth", base + `project=$1 AND dataset_generation=$2 AND resource_type=$3` + orderTail,
			[]any{pgsq01Project, pgsq01Gen, "Patient"},
			[][]string{{"p-1"}, {"p-dup-a"}, {"p-dup-b"}, {"p-plain"}, {"p-hidden"}}},
		{"without resource type", base + `project=$1 AND dataset_generation=$2 AND auth_resource_path = ANY (SELECT jsonb_array_elements_text($3::jsonb))` + orderTail,
			[]any{pgsq01Project, pgsq01Gen, json.RawMessage(`["FHIR/Patient"]`)},
			[][]string{{"p-1"}, {"p-dup-a"}, {"p-dup-b"}, {"p-plain"}, {"p-1"}}},
	} {
		rows := svc.run(t, probe.sql, probe.args...)
		if len(rows) != len(probe.wantRows) {
			t.Fatalf("guard-drop %s rows = %v, want %v", probe.desc, rows, probe.wantRows)
		}
		for index := range rows {
			if rows[index][0] != probe.wantRows[index][0] {
				t.Fatalf("guard-drop %s rows = %v, want %v", probe.desc, rows, probe.wantRows)
			}
		}
	}
}

func TestPGSQ01AuthModeIsLoadBearingAndStableIdentityIsPreserved(t *testing.T) {
	svc := newPGSQ01Service(t)
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-visible", pgsq01Path, nil)
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-conf", pgsq01ConfPath, nil)
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "p-legacy", "", nil)

	binding := bindingFor(t, svc.schema)
	restricted, err := RenderRootScan(
		pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id"), binding)
	if err != nil {
		t.Fatalf("restricted render: %v", err)
	}
	if got := svc.runRendered(t, restricted); !reflect.DeepEqual(got, [][]string{{"p-visible"}}) {
		t.Fatalf("restricted rows = %v, want only the authorized path", got)
	}
	unrestricted, err := RenderRootScan(
		pgsq01Plan(pgsq01Project, pgsq01Gen, nil, true, "Patient", "logical_id", "auth_resource_path"), binding)
	if err != nil {
		t.Fatalf("unrestricted render: %v", err)
	}
	want := [][]string{{"p-visible", "FHIR/Patient"}, {"p-conf", "FHIR/Confidential"}, {"p-legacy", ""}}
	if got := svc.runRendered(t, unrestricted); !reflect.DeepEqual(got, want) {
		t.Fatalf("unrestricted rows = %v, want %v", got, want)
	}

	orderSQL := `SELECT logical_id, resource_seq FROM "` + svc.schema + `".fhir_resource` +
		` WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' ORDER BY resource_seq`
	first := svc.run(t, orderSQL, pgsq01Project, pgsq01Gen)
	second := svc.run(t, orderSQL, pgsq01Project, pgsq01Gen)
	if !reflect.DeepEqual(first, second) || len(first) != 3 {
		t.Fatalf("storage identity order unstable: %v vs %v", first, second)
	}
	if first[0][0] != "p-visible" || first[1][0] != "p-conf" || first[2][0] != "p-legacy" {
		t.Fatalf("unexpected insertion order: %v", first)
	}
	if !(first[0][1] < first[1][1] && first[1][1] < first[2][1]) {
		t.Fatalf("resource_seq must be strictly increasing: %v", first)
	}
}

func TestPGSQ01DuplicatePhysicalDocumentsAreNotCollapsed(t *testing.T) {
	svc := newPGSQ01Service(t)
	duplicate := map[string]any{"code": "same", "meta": map[string]any{"tag": "dup"}}
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "d-1", pgsq01Path, duplicate)
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "d-2", pgsq01Path, duplicate)
	svc.insertResource(t, pgsq01Project, pgsq01Gen, "Patient", "d-3", pgsq01Path, duplicate)

	binding := bindingFor(t, svc.schema)

	constantRendered, err := RenderRootScan(
		pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "resource_type"), binding)
	if err != nil {
		t.Fatalf("constant-projection render: %v", err)
	}
	for _, forbidden := range []string{"DISTINCT", "GROUP BY"} {
		if strings.Contains(strings.ToUpper(constantRendered.SQL), forbidden) {
			t.Fatalf("root scan collapses rows with %s: %s", forbidden, constantRendered.SQL)
		}
	}
	if got := svc.runRendered(t, constantRendered); !reflect.DeepEqual(got, [][]string{{"Patient"}, {"Patient"}, {"Patient"}}) {
		t.Fatalf("duplicate physical documents collapsed to %v, want three rows", got)
	}

	identityRendered, err := RenderRootScan(
		pgsq01Plan(pgsq01Project, pgsq01Gen, []string{pgsq01Path}, false, "Patient", "logical_id"), binding)
	if err != nil {
		t.Fatalf("identity-projection render: %v", err)
	}
	if got := svc.runRendered(t, identityRendered); !reflect.DeepEqual(got, [][]string{{"d-1"}, {"d-2"}, {"d-3"}}) {
		t.Fatalf("stable logical ids = %v", got)
	}

	for _, logicalID := range []string{"d-1", "d-2", "d-3"} {
		rows := svc.run(t, `SELECT count(*) FROM "`+svc.schema+`".fhir_resource WHERE project=$1 AND dataset_generation=$2 AND resource_type='Patient' AND logical_id=$3`,
			pgsq01Project, pgsq01Gen, logicalID)
		if len(rows) != 1 || rows[0][0] != "1" {
			t.Fatalf("physical row count for %s = %v, want exactly 1", logicalID, rows)
		}
	}
	duplicateInsert := `INSERT INTO "` + svc.schema + `".fhir_resource (project, dataset_generation, resource_type, logical_id, auth_resource_path) VALUES ($1,$2,$3,$4,$5)`
	svc.expectRefused(t, duplicateInsert, "23505", "fhir_resource_natural_identity_ux",
		pgsq01Project, pgsq01Gen, "Patient", "d-1", pgsq01Path)

	svc.expectRefused(t, duplicateInsert, "23514", "fhir_resource_project_present",
		"", pgsq01Gen, "Patient", "ck-project", pgsq01Path)
	svc.expectRefused(t, duplicateInsert, "23514", "fhir_resource_resource_type_present",
		pgsq01Project, pgsq01Gen, "", "ck-type", pgsq01Path)
	svc.expectRefused(t, duplicateInsert, "23514", "fhir_resource_logical_id_present",
		pgsq01Project, pgsq01Gen, "Patient", "", pgsq01Path)
	if _, err := svc.iso.Exec(svc.ctx, duplicateInsert, pgsq01Project, pgsq01Gen, "Patient", "ck-emptypath", ""); err != nil {
		t.Fatalf("empty auth path is the legacy namespace and must insert: %v", err)
	}

	repo, err := control.NewResourceRepository(svc.client, svc.schema)
	if err != nil {
		t.Fatalf("resource repository: %v", err)
	}
	if _, err := repo.InsertResource(svc.ctx, source.Resource{
		Project: pgsq01Project, DatasetGeneration: pgsq01Gen, ResourceType: "Patient",
		LogicalID: "d-1", AuthResourcePath: pgsq01Path,
	}); !errors.Is(err, control.ErrResourceConflict) {
		t.Fatalf("duplicate repository insert = %v, want ErrResourceConflict", err)
	}
}

func TestZZNoLeftoverPgSQ01Resources(t *testing.T) {
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
		"SELECT count(*) FROM pg_database WHERE datname LIKE 'pgsq01_it_%'").Scan(&databases); err != nil {
		t.Fatalf("count databases: %v", err)
	}
	if databases != 0 {
		t.Fatalf("%d pgsq01_it_* databases still exist", databases)
	}
	var schemas int
	if err := admin.QueryRow(ctx,
		"SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'pgsq01_it_%'").Scan(&schemas); err != nil {
		t.Fatalf("count schemas: %v", err)
	}
	if schemas != 0 {
		t.Fatalf("%d pgsq01_it_* schemas still exist in the service database", schemas)
	}
}
