package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	control "github.com/calypr/loom/internal/backends/postgres/control"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sql21UCUM = "http://unitsofmeasure.org"
	sql21Gen  = "gen-1"
	sql21Path = "FHIR/Observation"
	sql21Prod = "proj-a"
)

var sql21LiveNamePattern = regexp.MustCompile(`^verify_sql21v1_[0-9]+_[0-9a-f]+$`)

type sql21Service struct {
	*pgsq01Service
}

func newSQL21Service(t *testing.T) *sql21Service {
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
		t.Fatalf("%s: %s", step, scrub(err.Error()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	dbName := sql21ResourceName(t)
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	sql21TrackResource(t, dbName)
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

	schema := sql21ResourceName(t)
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	sql21TrackResource(t, schema)
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
	return &sql21Service{pgsq01Service: &pgsq01Service{
		ctx: ctx, iso: iso, client: client, schema: schema, scrub: scrub, fail: fail,
	}}
}

func sql21ResourceName(t *testing.T) string {
	t.Helper()
	name := "verify_sql21v1_" + strconv.FormatInt(time.Now().UnixNano(), 10) + "_" + randomHex(t)
	if !sql21LiveNamePattern.MatchString(name) {
		t.Fatalf("owned resource name %q does not match the required pattern", name)
	}
	return name
}

func sql21TrackResource(t *testing.T, name string) {
	t.Helper()
	sql21FixturesMu.Lock()
	defer sql21FixturesMu.Unlock()
	sql21TrackedFixtures[name] = true
}

func sql21DriverArgs(rendered command.RenderedPostgresPhysicalPlan) []any {
	args := make([]any, len(rendered.Args))
	for index, parameter := range rendered.Args {
		args[index] = parameter.Value
	}
	return args
}

func (s *sql21Service) renderLive(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	rendered, err := RenderPhysicalPlan(plan, bindingFor(t, s.schema))
	if err != nil {
		s.fail("render plan", err)
	}
	return rendered
}

func (s *sql21Service) explain(t *testing.T, rendered command.RenderedPostgresPhysicalPlan) string {
	t.Helper()
	var text strings.Builder
	for _, row := range s.run(t, "EXPLAIN (FORMAT TEXT) "+rendered.SQL, sql21DriverArgs(rendered)...) {
		text.WriteString(strings.Join(row, "\n"))
		text.WriteString("\n")
	}
	if text.Len() == 0 {
		s.fail("explain produced no plan", errors.New(rendered.SQL))
	}
	return text.String()
}

func (s *sql21Service) expectRaise(t *testing.T, rendered command.RenderedPostgresPhysicalPlan, wantCode string, wantSubstrings ...string) {
	t.Helper()
	_, err := s.iso.Exec(s.ctx, rendered.SQL, sql21DriverArgs(rendered)...)
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

func (s *sql21Service) runOne(t *testing.T, rendered command.RenderedPostgresPhysicalPlan, want ...string) {
	t.Helper()
	rows := s.runRendered(t, rendered)
	if len(rows) != 1 {
		t.Fatalf("rows = %v, want exactly one row %v", rows, want)
	}
	if strings.Join(rows[0], "|") != strings.Join(want, "|") {
		t.Fatalf("row = %v, want %v\nSQL: %s", rows[0], want, rendered.SQL)
	}
}

func sql21Observation(value any, system any, code any) map[string]any {
	quantity := map[string]any{}
	if value != nil {
		quantity["value"] = value
	}
	if system != nil {
		quantity["system"] = system
	}
	if code != nil {
		quantity["code"] = code
	}
	return map[string]any{"status": "final", "valueQuantity": quantity}
}

var sql21JSONNull = json.RawMessage("null")

func sql21LivePlan(project string, scanVariable string, projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	return ir.PhysicalPlan{
		Version: 1,
		Engine:  ir.PhysicalEngineAQL,
		BindVars: map[string]any{
			"root_collection":                  "Observation",
			"project":                          project,
			"dataset_generation":               sql21Gen,
			"auth_resource_paths":              []string{sql21Path},
			"auth_resource_paths_unrestricted": false,
			"scope_allowed":                    true,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: scanVariable, CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
		},
	}
}

func sql21FahrenheitProjection(t *testing.T) ir.PhysicalProjection {
	t.Helper()
	target, dimension, rules := sql21FahrenheitFacts(t)
	valueSelector := sql21Steps("valueQuantity", "value")
	extract := &ir.PhysicalExtract{
		Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		ResourceType:  "Observation",
		Selector:      valueSelector,
		ExecutionMode: ir.PhysicalSelectorGeneric,
		UnitNormalization: &ir.PhysicalUnitNormalization{
			OriginalValue: valueSelector,
			SourceSystem:  sql21Steps("valueQuantity", "system"),
			SourceCode:    sql21Steps("valueQuantity", "code"),
			Target:        target,
			Dimension:     dimension,
			Rules:         rules,
		},
	}
	return ir.PhysicalProjection{Name: "temp_f", Expression: &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull, Extract: extract,
	}}
}

func sql21IndexedComponentProjection(t *testing.T, index int) ir.PhysicalProjection {
	t.Helper()
	target, dimension, rules := sql21CentimeterFacts(t)
	valueSelector := spec.Selector{Steps: []spec.SelectorStep{
		{Field: "component", Index: &index}, {Field: "valueQuantity"}, {Field: "value"},
	}}
	systemSelector := spec.Selector{Steps: []spec.SelectorStep{
		{Field: "component", Index: &index}, {Field: "valueQuantity"}, {Field: "system"},
	}}
	codeSelector := spec.Selector{Steps: []spec.SelectorStep{
		{Field: "component", Index: &index}, {Field: "valueQuantity"}, {Field: "code"},
	}}
	extract := &ir.PhysicalExtract{
		Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
		ResourceType:  "Observation",
		Selector:      valueSelector,
		ExecutionMode: ir.PhysicalSelectorGeneric,
		UnitNormalization: &ir.PhysicalUnitNormalization{
			OriginalValue: valueSelector,
			SourceSystem:  systemSelector,
			SourceCode:    codeSelector,
			Target:        target,
			Dimension:     dimension,
			Rules:         rules,
		},
	}
	return ir.PhysicalProjection{Name: "component_cm", Expression: &ir.PhysicalExpression{
		Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalEmptyOnNull, Extract: extract,
	}}
}

type sql21Cell struct {
	project string
	payload map[string]any
	want    string
	wantTag string
}

func sql21RunCells(t *testing.T, svc *sql21Service, projection ir.PhysicalProjection, cells []sql21Cell) {
	t.Helper()
	for _, cell := range cells {
		svc.insertResource(t, cell.project, sql21Gen, "Observation", cell.project, sql21Path, cell.payload)
		rendered := svc.renderLive(t, sql21LivePlan(cell.project, "root", projection))
		if cell.wantTag == "" {
			svc.runOne(t, rendered, cell.want)
			continue
		}
		planText := svc.explain(t, rendered)
		if !strings.Contains(planText, "SubPlan") && !strings.Contains(planText, "Function Scan") {
			t.Fatalf("%s: guard subplan missing from plan:\n%s", cell.project, planText)
		}
		svc.expectRaise(t, rendered, "22P02", cell.wantTag)
	}
}

func TestSQL21LiveLinearAndIdentityConversionsReadExact(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-exact-cm", sql21Observation(5, sql21UCUM, "cm"), "[5]", ""},
		{"sql21-exact-m", sql21Observation(2, sql21UCUM, "m"), "[200]", ""},
	})
	fahrenheit := sql21FahrenheitProjection(t)
	sql21RunCells(t, svc, fahrenheit, []sql21Cell{
		{"sql21-cel", sql21Observation(20, sql21UCUM, "Cel"), "[68]", ""},
		{"sql21-degf-identity", sql21Observation(98.6, sql21UCUM, "[degF]"), "[98.6]", ""},
	})
	svc.insertResource(t, "sql21-exact-cm", sql21Gen, "Patient", "sql21-exact-cm", sql21Path,
		map[string]any{"gender": "female"})
	svc.runOne(t, svc.renderLive(t, sql21LivePlan("sql21-exact-cm", "root", cm)), "[5]")
}

func TestSQL21LiveNullValuesSkipBeforeIdentityAssert(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-null-value", sql21Observation(sql21JSONNull, sql21UCUM, "cm"), "[]", ""},
		{"sql21-null-value-bad-identity", sql21Observation(sql21JSONNull, "urn:example:bad", "furlong"), "[]", ""},
		{"sql21-array-nulls", sql21Observation([]any{2, nil, 4}, sql21UCUM, "cm"), "[2 4]", ""},
		{"sql21-null-quantity", map[string]any{"status": "final", "valueQuantity": sql21JSONNull}, "[]", ""},
	})
}

func TestSQL21LiveEmptyValueListReadsEmptyArray(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-missing-quantity", map[string]any{"status": "final"}, "[]", ""},
		{"sql21-empty-array", sql21Observation([]any{}, sql21UCUM, "cm"), "[]", ""},
		{"sql21-quantity-no-value", map[string]any{"status": "final",
			"valueQuantity": map[string]any{"system": sql21UCUM, "code": "cm"}}, "[]", ""},
	})
}

func TestSQL21LiveDisplayLabelDoesNotSelectRule(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	displayLabel := map[string]any{"status": "final", "valueQuantity": map[string]any{
		"value": 5, "system": sql21UCUM, "code": "centimeter", "unit": "cm",
	}}
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-display-label", displayLabel, "", unitGuardIdentityUnknown},
		{"sql21-missing-system", map[string]any{"status": "final",
			"valueQuantity": map[string]any{"value": 5, "code": "cm"}}, "", unitGuardIdentityUnknown},
	})
}

func TestSQL21LiveExactIdentityPair(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-code-leading-space", sql21Observation(5, sql21UCUM, " cm"), "", unitGuardIdentityUnknown},
		{"sql21-code-case", sql21Observation(5, sql21UCUM, "CM"), "", unitGuardIdentityUnknown},
		{"sql21-system-space", sql21Observation(5, sql21UCUM+" ", "cm"), "", unitGuardIdentityUnknown},
		{"sql21-unknown-code", sql21Observation(5, sql21UCUM, "furlong"), "", unitGuardIdentityUnknown},
		{"sql21-code-number", sql21Observation(5, sql21UCUM, 5), "", unitGuardIdentityUnknown},
	})
}

func TestSQL21LiveRepeatedSourceSiblingIdentity(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	firstWins := map[string]any{"status": "final", "valueQuantity": map[string]any{
		"value": 4, "system": []any{sql21UCUM, sql21UCUM}, "code": []any{"cm", "m"},
	}}
	nullFirst := map[string]any{"status": "final", "valueQuantity": map[string]any{
		"value": 4, "system": []any{nil, sql21UCUM}, "code": []any{nil, "cm"},
	}}
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-first-sibling", firstWins, "[4]", ""},
		{"sql21-single-item-array", map[string]any{"status": "final", "valueQuantity": map[string]any{
			"value": 6, "system": []any{sql21UCUM}, "code": []any{"cm"},
		}}, "[6]", ""},
		{"sql21-null-first-sibling", nullFirst, "", unitGuardIdentityUnknown},
	})
}

func TestSQL21LiveIndexedQuantityReadsInRangeAndOutOfRange(t *testing.T) {
	svc := newSQL21Service(t)
	payload := map[string]any{"status": "final", "component": []any{
		map[string]any{"valueQuantity": map[string]any{"value": 7, "system": sql21UCUM, "code": "cm"}},
	}}
	sql21RunCells(t, svc, sql21IndexedComponentProjection(t, 0), []sql21Cell{
		{"sql21-indexed-in-range", payload, "[7]", ""},
	})
	sql21RunCells(t, svc, sql21IndexedComponentProjection(t, 3), []sql21Cell{
		{"sql21-indexed-out-of-range", payload, "[]", ""},
	})
}

func TestSQL21LiveDecimalStringAndNonNumericReadBack(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-decimal-string", sql21Observation("2.50", sql21UCUM, "cm"), "[2.5]", ""},
		{"sql21-boolean-value", sql21Observation(true, sql21UCUM, "cm"), "[<nil>]", ""},
		{"sql21-object-value", sql21Observation(map[string]any{"a": 1}, sql21UCUM, "cm"), "[<nil>]", ""},
		{"sql21-word-value", sql21Observation("abc", sql21UCUM, "cm"), "[<nil>]", ""},
		{"sql21-huge-exponent", sql21Observation("1e999999999999", sql21UCUM, "cm"), "[<nil>]", ""},
		{"sql21-null-then-value", sql21Observation([]any{nil, "3"}, sql21UCUM, "cm"), "[3]", ""},
	})
}

func TestSQL21LiveOverflowReadsNull(t *testing.T) {
	svc := newSQL21Service(t)
	cm := sql21UnitProjection(t, nil)
	sql21RunCells(t, svc, cm, []sql21Cell{
		{"sql21-multiply-overflow", sql21Observation("1e307", sql21UCUM, "m"), "[<nil>]", ""},
		{"sql21-operand-overflow", sql21Observation("1.8e308", sql21UCUM, "cm"), "[<nil>]", ""},
	})
}

func TestSQL21LiveAdditionNearTieReadsNull(t *testing.T) {
	svc := newSQL21Service(t)
	upper := new(big.Int).Mul(
		new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 53), big.NewInt(1)),
		new(big.Int).Lsh(big.NewInt(1), 971))
	upper.Sub(upper, new(big.Int).Lsh(big.NewInt(1), 975))
	valueInt := new(big.Int).Quo(new(big.Int).Mul(upper, big.NewInt(10)), big.NewInt(18))
	productTenths := new(big.Int).Mul(valueInt, big.NewInt(18))
	lowerTenths := new(big.Int).Sub(new(big.Int).Mul(upper, big.NewInt(10)), big.NewInt(18))
	if productTenths.Cmp(lowerTenths) < 0 || productTenths.Cmp(new(big.Int).Mul(upper, big.NewInt(10))) >= 0 {
		t.Fatalf("constructed near-tie product left [upper-1.8, upper)")
	}
	if new(big.Int).Add(productTenths, big.NewInt(320)).Cmp(new(big.Int).Mul(upper, big.NewInt(10))) <= 0 {
		t.Fatalf("near-tie sum must sit above the addition pass boundary")
	}
	mulDiv := new(big.Int)
	if _, ok := mulDiv.SetString(callDoubleMulDivPassUpper, 10); !ok {
		t.Fatalf("callDoubleMulDivPassUpper is not integral decimal text")
	}
	if mulDiv.Cmp(upper) <= 0 {
		t.Fatalf("multiply bound must sit above the addition bound")
	}
	operand := new(big.Int)
	if _, ok := operand.SetString(callDoubleOverflowBound, 10); !ok {
		t.Fatalf("callDoubleOverflowBound is not integral decimal text")
	}
	if valueInt.Cmp(operand) >= 0 {
		t.Fatalf("near-tie operand must stay below the operand overflow band")
	}

	fahrenheit := sql21FahrenheitProjection(t)
	sql21RunCells(t, svc, fahrenheit, []sql21Cell{
		{"sql21-addition-near-tie", sql21Observation(valueInt.String(), sql21UCUM, "Cel"), "[<nil>]", ""},
	})
}

func sql21UnitNormalizationBundle(name string, target unit.UnitIdentity, rules []unit.UnitRuleReference) recipe.Bundle {
	return recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                name,
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "observations", RootResourceType: "Observation", RowGrain: "resource",
			Fields: []recipe.Field{{Name: "status", Expr: recipe.Expression{Select: "root.status"}}},
			Aggregates: []recipe.Aggregate{{
				Name: "height_cm", Operation: recipe.AggregateMax,
				Expr: &recipe.Expression{Select: "root.valueQuantity.value"},
				UnitNormalization: &recipe.UnitNormalizationPolicy{
					SystemPath: "valueQuantity.system", CodePath: "valueQuantity.code",
					Target: target,
					Rules:  rules,
				},
			}},
		}},
	}
}

func sql21CompileProduction(t *testing.T, bundle recipe.Bundle) ir.PhysicalPlan {
	t.Helper()
	bindings := recipe.RuntimeBindings{
		Project: sql21Prod, DatasetGeneration: sql21Gen,
		AuthResourcePaths: []string{sql21Path},
	}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("BuildRecipePlan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatalf("ResolveRecipePlan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("lower.CompileResolvedRecipePlan: %v", err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	output := compiled.Outputs[0]
	if output.OptimizedPlan != nil {
		return *output.OptimizedPlan
	}
	return output.Plan
}

func sql21CentimeterPolicyRefs(t *testing.T) (unit.UnitIdentity, []unit.UnitRuleReference) {
	t.Helper()
	policy, err := unit.ResolveApprovedUnitPolicy("to-centimeters", "1")
	if err != nil {
		t.Fatalf("to-centimeters policy must resolve: %v", err)
	}
	references := make([]unit.UnitRuleReference, 0, len(policy.Rules))
	for _, rule := range policy.Rules {
		references = append(references, unit.UnitRuleReference{ID: rule.ID, Version: rule.Version})
	}
	return policy.Target, references
}

func sql21FindAggregateNormalization(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalAggregate {
	t.Helper()
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			expression := projection.Expression
			if expression == nil || expression.Kind != ir.PhysicalAggregateExpression || expression.Aggregate == nil {
				continue
			}
			value := expression.Aggregate.Value
			if value != nil && value.Extract != nil && value.Extract.UnitNormalization != nil {
				return expression.Aggregate
			}
		}
	}
	t.Fatalf("production plan carries no unit-normalized aggregate projection")
	return nil
}

func TestSQL21ProductionRecipeRefusedAtAggregateGate(t *testing.T) {
	target, references := sql21CentimeterPolicyRefs(t)
	plan := sql21CompileProduction(t, sql21UnitNormalizationBundle("sql21_p1_aggregate_gate", target, references))
	aggregate := sql21FindAggregateNormalization(t, plan)
	if aggregate.Value.Extract.UnitNormalization == nil {
		t.Fatal("witness lost its normalization")
	}
	binding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	_, err = RenderPhysicalPlan(plan, binding)
	if err == nil {
		t.Fatal("production aggregate plan rendered; the aggregate gate must refuse it (pg-sql-13 owns that surface)")
	}
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("aggregate gate error = %v, want ErrUnsupportedPostgresPlan", err)
	}
	if !strings.Contains(err.Error(), string(ir.PhysicalAggregateExpression)) ||
		!strings.Contains(err.Error(), "no registered PostgreSQL rendering") {
		t.Fatalf("aggregate gate refusal text drifted: %v", err)
	}
	t.Logf("P-1 verbatim aggregate-gate refusal (banked): %v", err)
}

func TestSQL21ProductionLoweredExtractHoistsByteIdentically(t *testing.T) {
	target, references := sql21CentimeterPolicyRefs(t)
	plan := sql21CompileProduction(t, sql21UnitNormalizationBundle("sql21_p2_hoist_identity", target, references))
	aggregate := sql21FindAggregateNormalization(t, plan)
	production := aggregate.Value

	productionRaw, err := json.Marshal(production)
	if err != nil {
		t.Fatalf("marshal production expression: %v", err)
	}
	var hoisted ir.PhysicalExpression
	if err := json.Unmarshal(productionRaw, &hoisted); err != nil {
		t.Fatalf("unmarshal hoisted expression: %v", err)
	}
	hoistedRaw, err := json.Marshal(&hoisted)
	if err != nil {
		t.Fatalf("marshal hoisted expression: %v", err)
	}
	t.Logf("P-2 lowering output  : %s", productionRaw)
	t.Logf("P-2 hoisted EXTRACT  : %s", hoistedRaw)
	if string(productionRaw) != string(hoistedRaw) {
		t.Fatalf("hoisted expression is not byte-identical to the lowering output")
	}
	if hoisted.Extract == nil || hoisted.Extract.UnitNormalization == nil ||
		hoisted.Kind != ir.PhysicalExtractExpression ||
		hoisted.Cardinality != ir.PhysicalArrayCardinality ||
		hoisted.NullBehavior != ir.PhysicalEmptyOnNull {
		t.Fatalf("hoisted expression left the registered posture: %+v", hoisted)
	}

	projection := ir.PhysicalProjection{Name: "length_cm", Expression: &hoisted}
	offlineBinding, err := ir.NewPostgresStorageBinding("loom_offline")
	if err != nil {
		t.Fatalf("NewPostgresStorageBinding: %v", err)
	}
	livePlan := func(project string) ir.PhysicalPlan {
		return sql21LivePlan(project, hoisted.Extract.Source.Variable, projection)
	}
	if _, err := RenderPhysicalPlan(livePlan("sql21_p2_offline"), offlineBinding); err != nil {
		t.Fatalf("renderer refused the hoisted production EXTRACT: %v", err)
	}

	svc := newSQL21Service(t)
	sql21RunCells(t, svc, projection, []sql21Cell{
		{"sql21_p2_cm", sql21Observation(5, sql21UCUM, "cm"), "[5]", ""},
		{"sql21_p2_m", sql21Observation(2, sql21UCUM, "m"), "[200]", ""},
		{"sql21_p2_null", sql21Observation(nil, sql21UCUM, "cm"), "[]", ""},
	})
}

func TestSQL21TamperedRuleSetFailsClosed(t *testing.T) {
	tamperedScale := sql21UnitProjection(t, func(x *ir.PhysicalExtract) {
		x.UnitNormalization.Rules[0].Scale = 101
	})
	err := sql21ContractError(t, tamperedScale, nil)
	if !strings.Contains(err.Error(), "does not match the pinned registry entry") {
		t.Fatalf("tampered scale refused with drifted text: %v", err)
	}

	unknown := sql21UnitProjection(t, func(x *ir.PhysicalExtract) {
		x.UnitNormalization.Rules = []unit.UnitConversionRule{{
			ID: "invented", Version: "1", Kind: unit.UnitConversionLinear,
			Source:    unit.UnitIdentity{System: sql21UCUM, Code: "furlong"},
			Target:    unit.UnitIdentity{System: sql21UCUM, Code: "cm"},
			Dimension: unit.UnitDimension{Length: 1}, Scale: 20116.8,
		}}
	})
	err = sql21ContractError(t, unknown, nil)
	if !errors.Is(err, unit.ErrUnitIdentityUnknown) {
		t.Fatalf("unapproved rule material = %v, want unit.ErrUnitIdentityUnknown sentinel", err)
	}

	driftedDimension := sql21UnitProjection(t, func(x *ir.PhysicalExtract) {
		x.UnitNormalization.Dimension = unit.UnitDimension{Time: 1}
	})
	err = sql21ContractError(t, driftedDimension, nil)
	if !errors.Is(err, unit.ErrUnitDimensionIncompatible) {
		t.Fatalf("drifted dimension = %v, want unit.ErrUnitDimensionIncompatible sentinel", err)
	}
}
