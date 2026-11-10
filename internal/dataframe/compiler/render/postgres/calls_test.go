package postgres

// pg-sql-10 offline suite: golden CALL projections with full ordered
// arguments, the registered-capability refusal cells, placeholder
// discipline, and determinism. Expected values are hand-derived from the
// AQL producer text (render/aql/calls.go) and the official ArangoDB 3.9
// documentation cited in calls.go's contract comment — never from
// PostgreSQL execution, which belongs to the live-gated cells in
// calls_live_test.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

const (
	sql10Project = "sql10-proj"
	sql10Gen     = "gen-1"
	sql10Path    = "FHIR/Patient"
)

// sql10Plan builds the closed read-spine plan: one direct ROOT_SCAN over
// resourceType with the canonical restricted scope binds, plus any extra
// bind entries the CALL payloads need, then a RETURN of the given
// projections.
func sql10Plan(resourceType string, extraBinds map[string]any, projections ...ir.PhysicalProjection) ir.PhysicalPlan {
	binds := map[string]any{
		"root_collection":                  resourceType,
		"project":                          sql10Project,
		"dataset_generation":               sql10Gen,
		"auth_resource_paths":              []string{sql10Path},
		"auth_resource_paths_unrestricted": false,
		"scope_allowed":                    true,
	}
	for key, value := range extraBinds {
		binds[key] = value
	}
	return ir.PhysicalPlan{
		Version:  1,
		Engine:   ir.PhysicalEngineAQL,
		BindVars: binds,
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: projections}},
		},
	}
}

func sql10CallProjection(name string, expression *ir.PhysicalExpression) ir.PhysicalProjection {
	return ir.PhysicalProjection{Name: name, Expression: expression}
}

func sql10Call(name, targetKind string, args ...ir.PhysicalExpression) *ir.PhysicalExpression {
	return &ir.PhysicalExpression{
		Kind:         ir.PhysicalCallExpression,
		Cardinality:  ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Call:         &ir.PhysicalCall{Name: name, TargetKind: targetKind, Args: args},
	}
}

// sql10Nested embeds one registered call as an operand expression.
func sql10Nested(call *ir.PhysicalExpression) ir.PhysicalExpression { return *call }

func sql10Literal(bindKey string) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind:         ir.PhysicalLiteralExpression,
		Cardinality:  ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Literal:      &ir.PhysicalLiteral{BindKey: bindKey},
	}
}

// sql10ArrayLiteral is the distinct_present_one collection form: an
// ARRAY-cardinality literal bind (the exact shape
// ir/physical_validation_expr.go:296 requires for that call).
func sql10ArrayLiteral(bindKey string) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind:         ir.PhysicalLiteralExpression,
		Cardinality:  ir.PhysicalArrayCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Literal:      &ir.PhysicalLiteral{BindKey: bindKey},
	}
}

func sql10Value(path ...string) ir.PhysicalExpression {
	return ir.PhysicalExpression{
		Kind:         ir.PhysicalValueExpression,
		Cardinality:  ir.PhysicalScalarCardinality,
		NullBehavior: ir.PhysicalPreserveNull,
		Value:        &ir.PhysicalValue{Variable: "root", Path: path},
	}
}

// sql10Aggregate builds an ir-valid COUNT aggregate expression — the
// registered render walk must refuse every operand class outside
// LITERAL/EXTRACT/VALUE/nested-CALL by name.
func sql10Aggregate(cardinality ir.PhysicalCardinality) ir.PhysicalExpression {
	behavior := ir.PhysicalPreserveNull
	if cardinality == ir.PhysicalArrayCardinality {
		behavior = ir.PhysicalEmptyOnNull
	}
	return ir.PhysicalExpression{
		Kind:         ir.PhysicalAggregateExpression,
		Cardinality:  cardinality,
		NullBehavior: behavior,
		Aggregate: &ir.PhysicalAggregate{
			Source:    ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			Operation: ir.PhysicalCountAggregate,
		},
	}
}

func sql10Selector(t *testing.T, path string) spec.Selector {
	t.Helper()
	selector, err := spec.ParseSelector(path)
	if err != nil {
		t.Fatalf("ParseSelector(%q): %v", path, err)
	}
	return selector
}

// sql10Extract builds the closed EXTRACT operand: payload-anchored source,
// schema resource type, direct property selector.
func sql10Extract(t *testing.T, resourceType, path string,
	cardinality ir.PhysicalCardinality, behavior ir.PhysicalNullBehavior) ir.PhysicalExpression {
	t.Helper()
	return ir.PhysicalExpression{
		Kind:         ir.PhysicalExtractExpression,
		Cardinality:  cardinality,
		NullBehavior: behavior,
		Extract: &ir.PhysicalExtract{
			Source:        ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			ResourceType:  resourceType,
			Selector:      sql10Selector(t, path),
			ExecutionMode: ir.PhysicalSelectorDirectScalar,
		},
	}
}

func sql10RenderOK(t *testing.T, plan ir.PhysicalPlan) command.RenderedPostgresPhysicalPlan {
	t.Helper()
	// The plan is validated first through the public entry (render.go
	// runs plan.Validate before any assembly): a fixture that ir itself
	// rejects can never pass a golden vacuously.
	rendered, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("RenderPhysicalPlan: %v", err)
	}
	// Placeholder discipline belt-and-braces: every $1..$n of a
	// successful render is referenced in the statement (the command
	// constructor enforces this; the scan makes the contract visible
	// here as well).
	for index := 1; index <= len(rendered.Args); index++ {
		if !strings.Contains(rendered.SQL, fmt.Sprintf("$%d", index)) {
			t.Fatalf("parameter $%d is never referenced: %s", index, rendered.SQL)
		}
	}
	return rendered
}

func sql10ScopeArgs() []command.PostgreSQLParameter {
	return []command.PostgreSQLParameter{
		{Type: command.PostgreSQLParameterText, Value: sql10Project},
		{Type: command.PostgreSQLParameterText, Value: sql10Gen},
		{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["FHIR/Patient"]`)},
		{Type: command.PostgreSQLParameterText, Value: "Patient"},
	}
}

// sql10Statement wraps a golden projection list in the canonical scope
// frame the read spine assembles (same shape the pg-sql-03 goldens pin).
func sql10Statement(selects string) string {
	return `SELECT ` + selects +
		` FROM "pgir02_it_unit"."fhir_resource" AS r0` +
		` WHERE r0."project" = $1` +
		` AND r0."dataset_generation" = $2` +
		` AND r0."auth_resource_path" = ANY (SELECT jsonb_array_elements_text($3::jsonb))` +
		` AND r0."resource_type" = $4` +
		` ORDER BY r0."resource_seq" ASC`
}

func sql10Reject(t *testing.T, plan ir.PhysicalPlan, context string, want ...string) {
	t.Helper()
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("%s: error = %v (%T), want ErrUnsupportedPostgresPlan", context, err, err)
	}
	for _, fragment := range want {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("%s: error %q lacks %q", context, err.Error(), fragment)
		}
	}
}

// sql10RejectAny asserts only that rendering fails: cells whose refusal
// legitimately comes from ir.Validate upstream carry ir's own error type.
func sql10RejectAny(t *testing.T, plan ir.PhysicalPlan, context string, want ...string) {
	t.Helper()
	_, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err == nil {
		t.Fatalf("%s: render succeeded, want refusal", context)
	}
	for _, fragment := range want {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("%s: error %q lacks %q", context, err.Error(), fragment)
		}
	}
}

// ---------------------------------------------------------------------------
// Golden registered surface
// ---------------------------------------------------------------------------

// sql10NumericText renders the operand text the arithmetic goldens pin:
// one jsonb bind read through its canonical text as a numeric — the
// registered band-test domain; the executed + - * / run in double (IEEE
// parity) only inside arms whose numeric-domain guard has already proven
// the operation cannot reach PG18's 22003 raise points.
func sql10NumericText(parameter string) string {
	return "((" + parameter + ") #>> '{}')::numeric"
}

// The registered IEEE-754 double boundaries, pinned from the measured
// facts (PG18 probe battery): the overflow bound is DBL_MAX + 2^970, the
// round-half-even tie above the largest finite double — the exact point
// where numeric->double casts raise and where IEEE overflow begins (reads
// NULL, the documented invalid-value reading); the add pass boundary is
// DBL_MAX − 2^970 (two operand half-ulps below the tie); the multiply/
// divide pass boundary is the tie − 2^973 (relative operand error 2^-52
// ≈ 2^972 at the top, with margin); the cast pass floor is 2.5e-324
// (above the measured 17-digit round-to-zero tie where the cast raises,
// below the minimum subnormal — magnitudes below read the registered 0);
// and the operation subnormal floor is 4.9406564584124655e-324 (above the
// exact minimum subnormal — products/quotients below it read the
// registered 0 instead of reaching the raising round-to-zero); the divisor
// normal floor is DBL_MIN 2^-1022 in its exact binary decimal expansion
// (divisors below it read the registered NULL — F-10,
// sql10WantNormalFloor).
const (
	sql10WantOverflowBound   = "179769313486231580793728971405303415079934132710037826936173778980444968292764750946649017977587207096330286416692887910946555547851940402630657488671505820681908902000708383676273854845817711531764475730270069855571366959622842914819860834936475292719074168444365510704342711559699508093042880177904174497792"
	sql10WantAddPassUpper    = "179769313486231560835325876058105298516207002341652166261661174625869553267292326574530099287946549246750631490335877017522087105926987962906277604735569213290190919152394180476217125334960946356387261286640198029037799514183602981511756283727771403830521483963923935633133642802139091669457927874464075218944"
	sql10WantMulDivPassUpper = "179769313486231500960116590016510948825025611236495184238123361562143308190875053458173343219024575698011666711264844337248681780152130643733137952927759391115036970607451570876046936802390650830255617955750582549437097177865883181587442630101659737164863430522599210419506436529457842398703070964143777382400"
	sql10WantUnderflowFloor  = "2.5E-324"
	sql10WantSubnormalFloor  = "4.9406564584124655E-324"
	// sql10WantNormalFloor is DBL_MIN, the divisor gate (F-10): a
	// subnormal-band divisor's numeric text can sit a relative ~9e-16 off
	// the double it executes as — the 15-digit canonical spelling of the
	// minimum subnormal passes any round-trip conjunct tautologically
	// (float8->numeric rounds to 15 significant digits, probed) — so the
	// whole band below DBL_MIN reads the registered NULL and every cast
	// executes only on a normal double (half-ulp parse, inside the
	// mul/div pass margin). TestCallsDivisorNormalBand pins this.
	sql10WantNormalFloor = "2.225073858507201383090232717332404064219215980462331830553327416887204434813918195854283159012511020564067339731035811005152434161553460108856012385377718821130777993532002330479610147442583636071921565046942503734208375250806650616658158948720491179968591639648500635908770118304874799780887753749949451580451605050915399856582470818645113537935804992115981085766051992433352114352390148795699609591288891602992641511063466313393663477586513029371762047325631781485664350872122828637642044846811407613911477062801689853244110024161447421618567166150540154285084716752901903161322778896729707373123334086988983175067838846926092773977972858659654941091369095406136467568702398678315290680984617210924625396728515625E-308"
	// Band-test operand cap (F-8): comparison-only CASE that caps
	// band-test operands' magnitudes at 1e400 so the band tests' own
	// numeric +,-,*,/ can never leave the storable numeric domain
	// (PG18 raises 22003 on unclamped 1e5e9-class products, quotients,
	// and near-max sums; tiny operands are safe because division flushes
	// and tiny products read the registered 0 arm — probes in the F-8
	// note). TestCallsBandTestClamps pins this against the emitter
	// constant and against the band boundaries: every pass verdict needs
	// both operands far inside the cap, so real-valued verdicts are
	// byte-identical with or without the clamp.
	sql10WantBandClampCap = "1e400"
)

// sql10WantNumericToJSONB mirrors the registered numeric->jsonb cast
// (TO_NUMBER path): range-guarded in numeric, cast back exactly once,
// overflow NULL, IEEE underflow 0, NULL propagated.
func sql10WantNumericToJSONB(expression string) string {
	return `to_jsonb(CASE WHEN abs(` + expression + `) < '` + sql10WantOverflowBound + `'::numeric` +
		` THEN CASE WHEN abs(` + expression + `) >= '` + sql10WantUnderflowFloor + `'::numeric` +
		` THEN (` + expression + `)::double precision ELSE 0::double precision END END)`
}

// sql10WantDoubleOperand mirrors the guarded numeric->double parse:
// overflow band NULL, subnormal-floor band 0, exact cast otherwise.
func sql10WantDoubleOperand(expression string) string {
	return `(CASE WHEN abs(` + expression + `) < '` + sql10WantOverflowBound + `'::numeric` +
		` THEN CASE WHEN abs(` + expression + `) >= '` + sql10WantUnderflowFloor + `'::numeric` +
		` THEN (` + expression + `)::double precision ELSE 0::double precision END` +
		` ELSE NULL END)`
}

// sql10WantDoubleDivisor mirrors the divisor parse (F-10): zero and the
// whole subnormal band below sql10WantNormalFloor read NULL — never a
// zero-valued double reaches the division, and never a divisor whose
// parse drifted more than half an ulp (the round-trip conjunct of F-9
// was tautological for the 15-digit canonical spelling that float8->
// numeric itself prints, so the band is refused outright) — overflow
// reads NULL, and magnitudes at or above the normal floor cast exactly.
func sql10WantDoubleDivisor(expression string) string {
	return `(CASE WHEN abs(` + expression + `) < '` + sql10WantOverflowBound + `'::numeric` +
		` THEN CASE WHEN abs(` + expression + `) >= '` + sql10WantNormalFloor + `'::numeric` +
		` THEN (` + expression + `)::double precision ELSE NULL END` +
		` ELSE NULL END)`
}

// sql10WantBandOperandClamp mirrors the band-test operand clamp (F-8):
// sign-preserving magnitude cap by comparison only; zero and NULL pass
// through the ELSE (the divisor's NULLIF fold keeps its meaning).
func sql10WantBandOperandClamp(operand string) string {
	return "(CASE" +
		" WHEN " + operand + " > '" + sql10WantBandClampCap + "'::numeric THEN '" + sql10WantBandClampCap + "'::numeric" +
		" WHEN " + operand + " < '-" + sql10WantBandClampCap + "'::numeric THEN '-" + sql10WantBandClampCap + "'::numeric" +
		" ELSE " + operand + " END)"
}

// sql10WantArithmetic mirrors the registered IEEE-domain arithmetic
// fragment: an exact numeric-domain band test — on clamp-windowed
// operands (F-8), so the test's own numeric operation cannot raise —
// selects the arm, the double operation runs inside the pass arm on the
// RAW operands (their own guards do the parse), underflow reads 0 and
// overflow (plus every NULL contribution) reads NULL.
func sql10WantArithmetic(operator, left, right string) string {
	clampedLeft, clampedRight := sql10WantBandOperandClamp(left), sql10WantBandOperandClamp(right)
	switch operator {
	case "+", "-":
		return `to_jsonb(CASE WHEN abs((` + clampedLeft + ` ` + operator + ` ` + clampedRight + `)) < '` + sql10WantAddPassUpper + `'::numeric` +
			` THEN ` + sql10WantDoubleOperand(left) + ` ` + operator + ` ` + sql10WantDoubleOperand(right) +
			` ELSE NULL END)`
	case "*":
		magnitude := `abs((` + clampedLeft + ` * ` + clampedRight + `))`
		return `to_jsonb(CASE WHEN ` + magnitude + ` < '` + sql10WantMulDivPassUpper + `'::numeric` +
			` AND ` + magnitude + ` >= '` + sql10WantSubnormalFloor + `'::numeric` +
			` THEN ` + sql10WantDoubleOperand(left) + ` * ` + sql10WantDoubleOperand(right) +
			` WHEN ` + magnitude + ` < '` + sql10WantSubnormalFloor + `'::numeric` +
			` THEN 0::double precision ELSE NULL END)`
	default:
		quotient := `((` + clampedLeft + ` + 0E-400) / NULLIF(` + clampedRight + `, 0))`
		magnitude := `abs(` + quotient + `)`
		return `to_jsonb(CASE WHEN ` + magnitude + ` < '` + sql10WantMulDivPassUpper + `'::numeric` +
			` AND ` + magnitude + ` >= '` + sql10WantSubnormalFloor + `'::numeric` +
			` THEN ` + sql10WantDoubleOperand(left) + ` / ` + sql10WantDoubleDivisor(right) +
			` WHEN ` + magnitude + ` < '` + sql10WantSubnormalFloor + `'::numeric` +
			` THEN 0::double precision ELSE NULL END)`
	}
}

// TestCallsWhitespaceSingleSource structurally enforces the ratified F-5
// disposition condition: ONE Go constant (callDecimalWhitespace) feeds
// both SQL guard sites — the grammar's surrounding class and btrim's
// erasure argument are derived from it, and the locale-dependent POSIX
// class appears nowhere. Any duplicated whitespace literal — including a
// re-widened [[:space:]], the shape PG18 measured admits U+2009 while
// numeric_in raises 22P02 on the cast — dies on these assertions.
func TestCallsWhitespaceSingleSource(t *testing.T) {
	if callDecimalWhitespace != " \t\n\v\f\r" {
		t.Fatalf("whitespace source = %q, want the explicit ASCII isspace set", callDecimalWhitespace)
	}
	wantClass := `[ ` + "\t\n\v\f\r" + `]`
	if got := callWhitespaceRegexClass(); got != wantClass {
		t.Fatalf("regex class = %q, want %q", got, wantClass)
	}
	if got := strings.Count(callDecimalStringPattern, wantClass); got != 2 {
		t.Fatalf("grammar admits the derived class %d times, want 2: %q", got, callDecimalStringPattern)
	}
	for _, forbidden := range []string{"[[:space:]]", "[[:blank:]]", `\s`, `\S`} {
		if strings.Contains(callDecimalStringPattern, forbidden) {
			t.Fatalf("grammar carries %q instead of the derived class: %q", forbidden, callDecimalStringPattern)
		}
	}
	if got, want := callDecimalTrimCharacters, quoteSQLLiteral(callDecimalWhitespace); got != want {
		t.Fatalf("btrim argument = %q, want the derived %q", got, want)
	}
	for _, pattern := range []string{
		callDecimalZeroPattern, callDecimalUnderflowPattern,
	} {
		for _, forbidden := range []string{"[[:space:]]", "[[:blank:]]", `\s`, `\S`} {
			if strings.Contains(pattern, forbidden) {
				t.Fatalf("tier grammar carries %q instead of the derived class: %q", forbidden, pattern)
			}
		}
	}
	// Both guard sites consume the shared construction verbatim: the
	// arithmetic coercion arm and the TO_NUMBER string arm.
	for name, fragment := range map[string]string{
		"coercion":  callNumericCoercion("o"),
		"to_number": callToNumberExpression("o"),
	} {
		if !strings.Contains(fragment, quoteSQLLiteral(callDecimalStringPattern)) ||
			!strings.Contains(fragment, callDecimalTrimCharacters) {
			t.Fatalf("%s guard arm does not embed the shared whitespace construction: %s", name, fragment)
		}
	}
}

// TestCallsDecimalTierSingleSource structurally enforces the ratified F-7
// disposition: the cast is gated by PostgreSQL's own input oracle
// (pg_input_is_valid) rather than digit-count bounds — the guard site
// must embed the oracle before the textual cast, the two provable-
// underflow tiers derive from the callDecimalUnderflow* constants and
// their provability arithmetic, and no quantifier anywhere exceeds the
// measured PG18 repetition limit (255). Loosening an underflow constant
// breaks the provability inequality asserted below or the live reading
// cells; removing the oracle gate lets "1e999999999999" reach the cast
// and the live battery cells die with a 22003 raise.
func TestCallsDecimalTierSingleSource(t *testing.T) {
	// Provable-IEEE-zero arithmetic: mantissa < 10^UD, exponent
	// ≤ -10^(MD-1) — the bound holds because the exponent's FIRST digit
	// is nonzero (a leading-zero exponent spells a small value and must
	// never claim provability: "e-00001" is -1, verifier 136's
	// provability spoof) — so the value magnitude is 10^(UD - 10^(MD-1)).
	// Below the gradual-subnormal floor (≈10^-323.4) means the exponent
	// difference must beat 324.
	tenTo := func(n int) int {
		v := 1
		for range n {
			v *= 10
		}
		return v
	}
	if callDecimalUnderflowIntegerDigits != 99 || callDecimalUnderflowMinExponentDigits != 5 {
		t.Fatalf("underflow constants drifted from the registered values: %d %d",
			callDecimalUnderflowIntegerDigits, callDecimalUnderflowMinExponentDigits)
	}
	if margin := tenTo(callDecimalUnderflowMinExponentDigits-1) - callDecimalUnderflowIntegerDigits; margin <= 324 {
		t.Fatalf("underflow tiers are not provably IEEE-zero: 10^(%d-%d) = 10^-%d is not below the subnormal floor",
			callDecimalUnderflowMinExponentDigits-1, callDecimalUnderflowIntegerDigits, margin)
	}
	// The only bounded digit quantifiers the grammar may contain,
	// derived from the two underflow constants — the exponent arm's
	// count is MD-1 because its first digit is [1-9] — and every
	// repetition count anywhere stays within PG18's measured regex limit
	// (a larger count is a compile error server-side, SQLSTATE 2201B).
	derived := map[string]bool{
		"[0-9]{1," + strconv.Itoa(callDecimalUnderflowIntegerDigits) + "}":      true,
		"[0-9]{" + strconv.Itoa(callDecimalUnderflowMinExponentDigits-1) + ",}": true,
	}
	if !strings.Contains(callDecimalUnderflowPattern, `[eE]-[1-9]`) {
		t.Fatalf("underflow exponent lost its nonzero leading digit: %q", callDecimalUnderflowPattern)
	}
	quantifier := regexp.MustCompile(`\[(?:0-9|0)\]\{(\d+)(?:,(\d*))?\}`)
	tiers := map[string]string{
		"underflow-zero": callDecimalZeroPattern,
		"underflow-tiny": callDecimalUnderflowPattern,
		"unbounded":      callDecimalStringPattern,
	}
	for name, pattern := range tiers {
		for _, run := range quantifier.FindAllStringSubmatch(pattern, -1) {
			if !derived[run[0]] {
				t.Fatalf("%s tier carries undervived digit run %q: %q", name, run[0], pattern)
			}
			for _, digits := range run[1:] {
				if digits == "" {
					continue
				}
				count, err := strconv.Atoi(digits)
				if err != nil || count > 255 {
					t.Fatalf("%s tier repetition count %q exceeds the PG18-measured 255 limit: %q", name, digits, pattern)
				}
			}
		}
	}
	// The zero and tiny tiers are proper subsets of the registered
	// shape: a text taking the registered 0 must also be a registered
	// valid representation (Go RE2, matching PG18's probed acceptance).
	wide := regexp.MustCompile(callDecimalStringPattern)
	zero := regexp.MustCompile(callDecimalZeroPattern)
	tiny := regexp.MustCompile(callDecimalUnderflowPattern)
	for _, text := range []string{
		"0", "-0", "0.", "0.0", ".0", "00", " 0.000\t", "0e999999999999",
		"1e-999999999999", "-99.9e-10000", "1" + strings.Repeat("0", 98) + "e-99999",
	} {
		if (zero.MatchString(text) || tiny.MatchString(text)) && !wide.MatchString(text) {
			t.Fatalf("registered-0 text %q falls outside the registered shape", text)
		}
	}
	if !tiny.MatchString("1" + strings.Repeat("0", callDecimalUnderflowIntegerDigits-1) + "e-99999") {
		t.Fatal("tiny tier must admit a mantissa of exactly the registered digit bound")
	}
	if tiny.MatchString("1" + strings.Repeat("0", callDecimalUnderflowIntegerDigits) + "e-99999") {
		t.Fatal("tiny tier admits a mantissa beyond the registered digit bound")
	}
	if tiny.MatchString("1e-9999") {
		t.Fatal("tiny tier admits an exponent below the registered digit bound")
	}
	// The provability spoof (verifier 136): leading-zero exponents spell
	// SMALL values and must never claim the provable-underflow reading —
	// a wrong 0 is worse than a raise. Both red probes (scale-overflow
	// texts whose values are ~0.1 and ~1e98) and a normal-magnitude
	// mantissa must fall through to the oracle-gated cast (which answers
	// "1e-00001" with 0.1) or the fail-closed NULL arm.
	for _, spoof := range []string{
		"1e-00001",
		"0." + strings.Repeat("9", 18000) + "e-00001",
		"1." + strings.Repeat("9", 20000) + "e-00001",
		strings.Repeat("9", 99) + "." + strings.Repeat("9", 20000) + "e-00001",
		"9.9e-09999",
	} {
		if tiny.MatchString(spoof) {
			t.Fatalf("provable-underflow arm admits leading-zero exponent %q", spoof)
		}
	}
	if !wide.MatchString("1e-00001") {
		t.Fatal(`"1e-00001" must stay inside the registered shape (the cast arm answers it)`)
	}
	// Both guard sites embed the registered arm shape: the shape test,
	// then the oracle-gated cast (the textual cast strictly after its
	// pg_input_is_valid gate — CASE ordering is the never-raise
	// guarantee), then the provable-0 pair, then the fail-closed NULL.
	// The oracle appears once per emitted tier arm: once in the
	// coercion, and nine times in TO_NUMBER (the bounded recursion
	// replicates the arm three ways, and callNumericToJSONB repeats the
	// banded expression three ways — both registered shapes).
	for name, cell := range map[string]struct {
		fragment   string
		wantOracle int
	}{
		"coercion":  {callNumericCoercion("o"), 1},
		"to_number": {callToNumberExpression("o"), 9},
	} {
		fragment := cell.fragment
		btrim := "btrim((o #>> '{}'), " + callDecimalTrimCharacters + ")"
		ordered := []string{
			quoteSQLLiteral(callDecimalStringPattern) + " THEN CASE WHEN ",
			"pg_input_is_valid(" + btrim + ", 'numeric') THEN " + btrim + "::numeric",
			quoteSQLLiteral(callDecimalZeroPattern),
			quoteSQLLiteral(callDecimalUnderflowPattern) + ") THEN 0",
			" ELSE NULL END",
		}
		last := -1
		for _, marker := range ordered {
			at := strings.Index(fragment, marker)
			if at <= last {
				t.Fatalf("%s arms missing or out of registered order at %q: %s", name, marker, fragment)
			}
			last = at
		}
		if got := strings.Count(fragment, "pg_input_is_valid("); got != cell.wantOracle {
			t.Fatalf("%s embeds the input oracle %d times, want %d: %s", name, got, cell.wantOracle, fragment)
		}
	}
}

// TestCallsBandTestClamps pins the F-8 disposition: the band tests' own
// numeric-domain operations (which raised 22003 on storable extreme
// operands before the clamp — PG18 measured 1e98303 × 1e98303,
// 9.9e131071 + 9.9e131071, and 1e131071 / 1e-16383) run on sign-
// preserved comparison-capped operands. The cap must sit above every
// registered band boundary (so no pass-band verdict ever sees it and
// real-valued verdicts are byte-identical), and every capped operation
// result — the worst product cap² and the worst quotient cap ÷ the
// smallest storable nonzero magnitude 1e-16383 — must stay inside the
// measured storable ceiling (1e131071) so the tests cannot raise.
// Tiny operands need no floor: PG18 division flushes and tiny products
// read the registered 0 arm (probed).
func TestCallsBandTestClamps(t *testing.T) {
	if callBandClampCap != sql10WantBandClampCap {
		t.Fatalf("emitter cap %s diverged from the mirror %s", callBandClampCap, sql10WantBandClampCap)
	}
	if callBandClampCap != "1e400" {
		t.Fatalf("clamp cap drifted from the registered value: %s", callBandClampCap)
	}
	parse := func(s string) *big.Float {
		v, _, err := big.ParseFloat(s, 10, 256, big.ToNearestEven)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}
	capV := parse(callBandClampCap)
	// The cap clears every registered band boundary on the upper side:
	// no pass verdict's operand magnitudes are ever capped.
	for _, upper := range []string{callDoubleAddPassUpper, callDoubleMulDivPassUpper, callDoubleOverflowBound} {
		if capV.Cmp(parse(upper)) <= 0 {
			t.Fatalf("clamp cap %s does not exceed the boundary %s", callBandClampCap, upper)
		}
	}
	// Capped operation results stay far inside numeric's storable domain
	// (measured ceiling 1e131071 on PG18), so the band tests cannot
	// raise: worst product cap², worst quotient cap ÷ 1e-16383 (the
	// smallest storable nonzero magnitude), worst sum 2·cap.
	ceiling := parse("1e131071")
	if product := new(big.Float).Mul(capV, capV); product.Cmp(ceiling) >= 0 {
		t.Fatalf("worst capped product %s reaches the measured numeric ceiling", product.Text('g', 6))
	}
	if quotient := new(big.Float).Quo(capV, parse("1e-16383")); quotient.Cmp(ceiling) >= 0 {
		t.Fatalf("worst capped quotient %s reaches the measured numeric ceiling", quotient.Text('g', 6))
	}
	if sum := new(big.Float).Mul(capV, big.NewFloat(2)); sum.Cmp(ceiling) >= 0 {
		t.Fatalf("worst capped sum %s reaches the measured numeric ceiling", sum.Text('g', 6))
	}
	// The clamp is comparison-only: no arithmetic operator may appear
	// (only registered literals and the operand), zero and NULL pass
	// through the ELSE, and the cap literal appears exactly as its
	// constant derived it (comparison + THEN on each sign side).
	clamped := callBandOperandClamp("o")
	for _, forbidden := range []string{" + ", " - ", " * ", " / ", "::double", "::float"} {
		if strings.Contains(clamped, forbidden) {
			t.Fatalf("clamp performs %q: %s", forbidden, clamped)
		}
	}
	if !strings.HasSuffix(clamped, " ELSE o END)") {
		t.Fatalf("clamp does not pass zero and NULL through unchanged: %s", clamped)
	}
	if got := strings.Count(clamped, "'"+callBandClampCap+"'::numeric"); got != 2 {
		t.Fatalf("clamp embeds the cap literal %d times, want 2: %s", got, clamped)
	}
	if got := strings.Count(clamped, "'-"+callBandClampCap+"'::numeric"); got != 2 {
		t.Fatalf("clamp embeds the negated cap literal %d times, want 2: %s", got, clamped)
	}
}

// TestCallsDivisorNormalBand pins the F-10 divisor gate. A round-trip
// conjunct ((x)::double precision)::numeric = x cannot guard the
// division: float8->numeric rounds to 15 significant digits (PG18
// probe: ((4.9406564584124654e-324)::double precision)::numeric prints
// 0.000…0494065645841247), so the 15-digit canonical spelling of the
// minimum subnormal satisfies the conjunct TAUTOLOGICALLY while sitting
// a relative 9.23e-16 off the double it executes as — above what the
// 2^973 pass margin leaves after the dividend's own half ulp, and PG18
// raised 22003 inside the pass arm on the measured witness
// 8.88178419700125430e-16 ÷ 4.94065645841247e-324. The gate is instead
// the DBL_MIN band refusal: the cast executes only at or above the
// normal floor, where every parse is within half an ulp of its text.
func TestCallsDivisorNormalBand(t *testing.T) {
	if callDoubleNormalFloor != sql10WantNormalFloor {
		t.Fatalf("emitter normal floor %s diverged from the mirror %s", callDoubleNormalFloor, sql10WantNormalFloor)
	}
	// The registered literal must be the exact double DBL_MIN = 2^1022,
	// not a nearby spelling.
	bits := math.Float64bits(func() float64 {
		v, err := strconv.ParseFloat(callDoubleNormalFloor, 64)
		if err != nil {
			t.Fatalf("parse normal floor %q: %v", callDoubleNormalFloor, err)
		}
		return v
	}())
	if bits != 0x0010000000000000 {
		t.Fatalf("normal floor %s is not DBL_MIN (bits %#x)", callDoubleNormalFloor, bits)
	}
	parse := func(s string) *big.Float {
		v, _, err := big.ParseFloat(s, 10, 256, big.ToNearestEven)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return v
	}
	// Above the subnormal-floor region it replaces for the divisor, far
	// below the pass upper: the gate refuses exactly the band whose
	// double parse can drift more than half an ulp.
	if parse(callDoubleSubnormalFloor).Cmp(parse(callDoubleNormalFloor)) >= 0 {
		t.Fatal("divisor normal floor must sit above the operation subnormal floor")
	}
	if parse(callDoubleNormalFloor).Cmp(parse(callDoubleMulDivPassUpper)) >= 0 {
		t.Fatal("divisor normal floor must sit below the pass upper")
	}
	// The divisor arm is two comparison gates plus one cast: the refused
	// tautological conjunct must not return, the cast appears exactly
	// once, and each registered bound embeds exactly once.
	divisor := callDoubleDivisor("o")
	if strings.Contains(divisor, ")::double precision)::numeric = ") {
		t.Fatalf("divisor arm resurrected the tautological round-trip conjunct: %s", divisor)
	}
	if got := strings.Count(divisor, "(o)::double precision"); got != 1 {
		t.Fatalf("divisor arm casts %d times, want exactly 1: %s", got, divisor)
	}
	if got := strings.Count(divisor, "'"+callDoubleNormalFloor+"'::numeric"); got != 1 {
		t.Fatalf("divisor arm embeds the normal floor %d times, want 1: %s", got, divisor)
	}
	if got := strings.Count(divisor, "'"+callDoubleOverflowBound+"'::numeric"); got != 1 {
		t.Fatalf("divisor arm embeds the overflow bound %d times, want 1: %s", got, divisor)
	}
	if !strings.Contains(divisor, "abs(o) >= '"+callDoubleNormalFloor+"'::numeric") {
		t.Fatalf("divisor gate does not refuse the whole subnormal band by magnitude: %s", divisor)
	}
}

func TestCallsGoldenArithmeticAddDivide(t *testing.T) {
	left, right := sql10NumericText("$5::jsonb"), sql10NumericText("$6::jsonb")
	plan := sql10Plan("Patient", map[string]any{"a": int64(2), "b": float64(1.5)},
		sql10CallProjection("sum", sql10Call("add", "", sql10Literal("a"), sql10Literal("b"))),
	)
	rendered := sql10RenderOK(t, plan)
	wantSQL := sql10Statement(`(` + sql10WantArithmetic("+", left, right) + `) AS "c0"`)
	if rendered.SQL != wantSQL {
		t.Fatalf("add SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := append(sql10ScopeArgs(),
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`2`)},
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`1.5`)},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("add args = %#v, want %#v", rendered.Args, wantArgs)
	}
	if !reflect.DeepEqual(rendered.OutputColumns, []string{"sum"}) {
		t.Fatalf("add output columns = %v", rendered.OutputColumns)
	}

	// divide: the divisor is NULLIF-guarded so PostgreSQL's 22012 is
	// unreachable; the documented 1/0 cell reads SQL NULL. The band test
	// divides in numeric with a scale bias; the executed division is a
	// double operation with a divisor arm that can never be zero.
	dividePlan := sql10Plan("Patient", map[string]any{"a": int64(2), "b": float64(1.5)},
		sql10CallProjection("ratio", sql10Call("divide", "", sql10Literal("a"), sql10Literal("b"))),
	)
	divideRendered := sql10RenderOK(t, dividePlan)
	wantDivide := sql10Statement(`(` + sql10WantArithmetic("/", left, right) + `) AS "c0"`)
	if divideRendered.SQL != wantDivide {
		t.Fatalf("divide SQL =\n%s\nwant\n%s", divideRendered.SQL, wantDivide)
	}
}

func TestCallsGoldenArithmeticDynamicOperandCoercion(t *testing.T) {
	// extract + literal: the dynamic operand carries the registered
	// coercion table (numbers, decimal-form strings; everything else
	// NULL) while the literal must be a number bind.
	number := sql10NumericText("$6::jsonb")
	o := `(r0."payload" #> $5::text[])`
	coercion := `(CASE WHEN ` + o + ` IS NULL OR jsonb_typeof(` + o + `) = 'null' THEN NULL` +
		` WHEN jsonb_typeof(` + o + `) = 'number' THEN (` + o + ` #>> '{}')::numeric` +
		sql10WantDecimalTierArms(o) +
		` ELSE NULL END)`
	plan := sql10Plan("Patient", map[string]any{"a": int64(3)},
		sql10CallProjection("scaled", sql10Call("multiply", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("a"))),
	)
	rendered := sql10RenderOK(t, plan)
	wantSQL := sql10Statement(`(` + sql10WantArithmetic("*", coercion, number) + `) AS "c0"`)
	if rendered.SQL != wantSQL {
		t.Fatalf("multiply SQL =\n%s\nwant\n%s", rendered.SQL, wantSQL)
	}
	wantArgs := append(sql10ScopeArgs(),
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "{gender}"},
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`3`)},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("multiply args = %#v, want %#v", rendered.Args, wantArgs)
	}
}

// sql10WantToNumberDouble mirrors the documented TO_NUMBER table
// (functions-type-cast.md 47-77: null/false→0, true→1, number→identity,
// decimal-form string→parse, other string→0, []→0, [x]→TO_NUMBER(x),
// ≥2→0, object→0) independently of calls.go's emitter, with the same
// bounded recursion the contract comment registers.
// sql10WantDecimalTierArms mirrors the registered decimal-string tiers
// (F-7) independently of calls.go's emitter: inside the registered
// shape, the PostgreSQL input oracle gates the btrim'd cast
// (pg_input_is_valid is false exactly where numeric_in would raise
// 22003), the provable-underflow pair reads the registered 0, and every
// other oracle-rejected representation reads the registered conservative
// NULL.
func sql10WantDecimalTierArms(operand string) string {
	text := "(" + operand + ` #>> '{}')`
	btrim := "btrim(" + text + ", " + callDecimalTrimCharacters + ")"
	return " WHEN jsonb_typeof(" + operand + ") = 'string' AND " + text + " ~ '" + callDecimalStringPattern +
		"' THEN CASE WHEN pg_input_is_valid(" + btrim + ", 'numeric') THEN " + btrim + "::numeric" +
		" WHEN (" + text + " ~ '" + callDecimalZeroPattern + "' OR " + text + " ~ '" + callDecimalUnderflowPattern + "') THEN 0" +
		" ELSE NULL END"
}

func sql10WantToNumberDouble(operand string, depth int) string {
	if depth >= callToNumberMaxRecursion {
		return "0"
	}
	element := sql10WantToNumberDouble("(("+operand+") -> 0)", depth+1)
	return "CASE" +
		" WHEN " + operand + " IS NULL OR jsonb_typeof(" + operand + ") = 'null' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'boolean' THEN CASE WHEN " + operand + " = 'true'::jsonb THEN 1 ELSE 0 END" +
		" WHEN jsonb_typeof(" + operand + ") = 'number' THEN (" + operand + " #>> '{}')::numeric" +
		sql10WantDecimalTierArms(operand) +
		" WHEN jsonb_typeof(" + operand + ") = 'string' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'array' THEN CASE" +
		" WHEN jsonb_array_length(" + operand + ") = 0 THEN 0" +
		" WHEN jsonb_array_length(" + operand + ") = 1 THEN " + element +
		" ELSE 0 END" +
		" ELSE 0 END"
}

func TestCallsGoldenCastTables(t *testing.T) {
	// cast string (TO_STRING scalar arms).
	stringPlan := sql10Plan("Patient", nil,
		sql10CallProjection("label", sql10Call("cast", "string",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
	)
	o := `(r0."payload" #> $5::text[])`
	stringRendered := sql10RenderOK(t, stringPlan)
	wantString := sql10Statement(`(CASE` +
		` WHEN ` + o + ` IS NULL OR jsonb_typeof(` + o + `) = 'null' THEN '""'::jsonb` +
		` WHEN jsonb_typeof(` + o + `) IN ('string', 'boolean', 'number')` +
		` THEN to_jsonb((` + o + `) #>> '{}')` +
		` ELSE NULL END) AS "c0"`)
	if stringRendered.SQL != wantString {
		t.Fatalf("cast string SQL =\n%s\nwant\n%s", stringRendered.SQL, wantString)
	}

	// cast integer (TO_NUMBER table) over a literal bind, range-guarded
	// in numeric before the single cast back to double.
	numberPlan := sql10Plan("Patient", map[string]any{"v": "42"},
		sql10CallProjection("count", sql10Call("cast", "integer", sql10Literal("v"))),
	)
	numberRendered := sql10RenderOK(t, numberPlan)
	numberDouble := sql10WantToNumberDouble("$5::jsonb", 0)
	wantNumber := sql10Statement(`(` + sql10WantNumericToJSONB(numberDouble) + `) AS "c0"`)
	if numberRendered.SQL != wantNumber {
		t.Fatalf("cast integer SQL =\n%s\nwant\n%s", numberRendered.SQL, wantNumber)
	}

	// cast boolean (full TO_BOOL table).
	booleanPlan := sql10Plan("Patient", nil,
		sql10CallProjection("flag", sql10Call("cast", "boolean",
			sql10Extract(t, "Patient", "active", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
	)
	booleanRendered := sql10RenderOK(t, booleanPlan)
	a := `(r0."payload" #> $5::text[])`
	wantBoolean := sql10Statement(`(CASE` +
		` WHEN ` + a + ` IS NULL OR jsonb_typeof(` + a + `) = 'null' THEN 'false'::jsonb` +
		` WHEN jsonb_typeof(` + a + `) = 'boolean' THEN ` + a +
		` WHEN jsonb_typeof(` + a + `) = 'number' THEN CASE WHEN ` + a + ` = '0'::jsonb THEN 'false'::jsonb ELSE 'true'::jsonb END` +
		` WHEN jsonb_typeof(` + a + `) = 'string' THEN CASE WHEN ` + a + ` = '""'::jsonb THEN 'false'::jsonb ELSE 'true'::jsonb END` +
		` ELSE 'true'::jsonb END) AS "c0"`)
	if booleanRendered.SQL != wantBoolean {
		t.Fatalf("cast boolean SQL =\n%s\nwant\n%s", booleanRendered.SQL, wantBoolean)
	}
}

func TestCallsGoldenCoalesceConditionalsLogical(t *testing.T) {
	// coalesce drops every null-reading arm; all-null yields SQL NULL.
	coalescePlan := sql10Plan("Patient", map[string]any{"s": "x", "n": nil},
		sql10CallProjection("pick", sql10Call("coalesce", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("s"), sql10Literal("n"))),
	)
	coalesceRendered := sql10RenderOK(t, coalescePlan)
	wantCoalesce := sql10Statement(`(COALESCE(NULLIF((r0."payload" #> $5::text[]), 'null'::jsonb), NULLIF($6::jsonb, 'null'::jsonb), NULLIF($7::jsonb, 'null'::jsonb))) AS "c0"`)
	if coalesceRendered.SQL != wantCoalesce {
		t.Fatalf("coalesce SQL =\n%s\nwant\n%s", coalesceRendered.SQL, wantCoalesce)
	}
	if len(coalesceRendered.Args) != 7 {
		t.Fatalf("coalesce args = %d, want 7", len(coalesceRendered.Args))
	}

	// if under the documented falsy set.
	ifPlan := sql10Plan("Patient", map[string]any{"t": true, "f": false},
		sql10CallProjection("choice", sql10Call("if", "",
			sql10Extract(t, "Patient", "active", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("t"), sql10Literal("f"))),
	)
	ifRendered := sql10RenderOK(t, ifPlan)
	a := `(r0."payload" #> $5::text[])`
	truthy := `NOT (` + a + ` IS NULL OR jsonb_typeof(` + a + `) = 'null' OR ` + a +
		` = 'false'::jsonb OR (jsonb_typeof(` + a + `) = 'number' AND ` + a + ` = '0'::jsonb) OR ` + a + ` = '""'::jsonb)`
	wantIf := sql10Statement(`(CASE WHEN ` + truthy + ` THEN $6::jsonb ELSE $7::jsonb END) AS "c0"`)
	if ifRendered.SQL != wantIf {
		t.Fatalf("if SQL =\n%s\nwant\n%s", ifRendered.SQL, wantIf)
	}

	// case without else: the SQL NULL keyword, no stranded parameter.
	casePlan := sql10Plan("Patient", map[string]any{"t": true},
		sql10CallProjection("recoded", sql10Call("case", "",
			sql10Literal("t"), sql10Literal("t"))),
	)
	caseRendered := sql10RenderOK(t, casePlan)
	caseTruthy := `NOT ($5::jsonb IS NULL OR jsonb_typeof($5::jsonb) = 'null' OR $5::jsonb = 'false'::jsonb` +
		` OR (jsonb_typeof($5::jsonb) = 'number' AND $5::jsonb = '0'::jsonb) OR $5::jsonb = '""'::jsonb)`
	wantCase := sql10Statement(`(CASE WHEN ` + caseTruthy + ` THEN $6::jsonb ELSE NULL END) AS "c0"`)
	if caseRendered.SQL != wantCase {
		t.Fatalf("case SQL =\n%s\nwant\n%s", caseRendered.SQL, wantCase)
	}
	// Condition and result take $5 and $6; the missing else bound
	// nothing — a sixth call parameter would be a stranded scalarNull.
	if len(caseRendered.Args) != 6 {
		t.Fatalf("case args = %d, want 6 (no stranded else parameter)", len(caseRendered.Args))
	}

	// and returns an operand value, not a boolean.
	andPlan := sql10Plan("Patient", map[string]any{"a": int64(0), "b": "x"},
		sql10CallProjection("fallback_pair", sql10Call("and", "", sql10Literal("a"), sql10Literal("b"))),
	)
	andRendered := sql10RenderOK(t, andPlan)
	andTruthy := `NOT ($5::jsonb IS NULL OR jsonb_typeof($5::jsonb) = 'null' OR $5::jsonb = 'false'::jsonb` +
		` OR (jsonb_typeof($5::jsonb) = 'number' AND $5::jsonb = '0'::jsonb) OR $5::jsonb = '""'::jsonb)`
	wantAnd := sql10Statement(`(CASE WHEN ` + andTruthy + ` THEN $6::jsonb ELSE $5::jsonb END) AS "c0"`)
	if andRendered.SQL != wantAnd {
		t.Fatalf("and SQL =\n%s\nwant\n%s", andRendered.SQL, wantAnd)
	}

	// not negates the two-valued truthiness test.
	notPlan := sql10Plan("Patient", nil,
		sql10CallProjection("inverted", sql10Call("not", "",
			sql10Extract(t, "Patient", "active", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))),
	)
	notRendered := sql10RenderOK(t, notPlan)
	wantNot := sql10Statement(`(to_jsonb(NOT ` + truthy + `)) AS "c0"`)
	if notRendered.SQL != wantNot {
		t.Fatalf("not SQL =\n%s\nwant\n%s", notRendered.SQL, wantNot)
	}
}

func TestCallsGoldenComparisons(t *testing.T) {
	// eq is total: SQL NULL and missing normalize to the jsonb null.
	eqPlan := sql10Plan("Patient", map[string]any{"s": "female"},
		sql10CallProjection("is_female", sql10Call("eq", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("s"))),
	)
	eqRendered := sql10RenderOK(t, eqPlan)
	wantEq := sql10Statement(`(to_jsonb(COALESCE((r0."payload" #> $5::text[]), 'null'::jsonb) = COALESCE($6::jsonb, 'null'::jsonb))) AS "c0"`)
	if eqRendered.SQL != wantEq {
		t.Fatalf("eq SQL =\n%s\nwant\n%s", eqRendered.SQL, wantEq)
	}

	// neq negates the total equality.
	neqPlan := sql10Plan("Patient", map[string]any{"s": "female"},
		sql10CallProjection("other", sql10Call("neq", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("s"))),
	)
	neqRendered := sql10RenderOK(t, neqPlan)
	wantNeq := sql10Statement(`(to_jsonb(NOT (COALESCE((r0."payload" #> $5::text[]), 'null'::jsonb) = COALESCE($6::jsonb, 'null'::jsonb)))) AS "c0"`)
	if neqRendered.SQL != wantNeq {
		t.Fatalf("neq SQL =\n%s\nwant\n%s", neqRendered.SQL, wantNeq)
	}

	// gt uses the AQL rank comparator: rank-4 (array/object) operands
	// read NULL before any rank or value compare (L15).
	gtPlan := sql10Plan("Patient", map[string]any{"n": float64(1)},
		sql10CallProjection("over_one", sql10Call("gt", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("n"))),
	)
	gtRendered := sql10RenderOK(t, gtPlan)
	lhs := `(r0."payload" #> $5::text[])`
	rank := func(operand string) string {
		return `(CASE WHEN ` + operand + ` IS NULL OR jsonb_typeof(` + operand + `) = 'null' THEN 0` +
			` WHEN jsonb_typeof(` + operand + `) = 'boolean' THEN 1` +
			` WHEN jsonb_typeof(` + operand + `) = 'number' THEN 2` +
			` WHEN jsonb_typeof(` + operand + `) = 'string' THEN 3` +
			` ELSE 4 END)`
	}
	rankLeft, rankRight := rank(lhs), rank("$6::jsonb")
	wantGt := sql10Statement(`(to_jsonb(CASE` +
		` WHEN ` + rankLeft + ` >= 4 OR ` + rankRight + ` >= 4 THEN NULL` +
		` WHEN ` + rankLeft + ` <> ` + rankRight + ` THEN ` + rankLeft + ` > ` + rankRight +
		` ELSE (COALESCE(` + lhs + `, 'null'::jsonb)) > (COALESCE($6::jsonb, 'null'::jsonb)) END)) AS "c0"`)
	if gtRendered.SQL != wantGt {
		t.Fatalf("gt SQL =\n%s\nwant\n%s", gtRendered.SQL, wantGt)
	}
}

func TestCallsGoldenAssertBarrier(t *testing.T) {
	// The raising arm keeps the population.go constant-folding barrier
	// and carries the plan-data message on a $n text parameter inside
	// the cast text (never interpolated).
	plan := sql10Plan("Patient", map[string]any{"msg": "MISSING_GENDER"},
		sql10CallProjection("checked", sql10Call("assert", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("msg"))),
	)
	rendered := sql10RenderOK(t, plan)
	a := `(r0."payload" #> $5::text[])`
	truthy := `NOT (` + a + ` IS NULL OR jsonb_typeof(` + a + `) = 'null' OR ` + a +
		` = 'false'::jsonb OR (jsonb_typeof(` + a + `) = 'number' AND ` + a + ` = '0'::jsonb) OR ` + a + ` = '""'::jsonb)`
	want := sql10Statement(`(CASE WHEN ` + truthy + ` THEN 'true'::jsonb ELSE ` +
		`to_jsonb(CAST('ASSERT:' || $6 || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer)) END) AS "c0"`)
	if rendered.SQL != want {
		t.Fatalf("assert SQL =\n%s\nwant\n%s", rendered.SQL, want)
	}
	wantArgs := append(sql10ScopeArgs(),
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "{gender}"},
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "MISSING_GENDER"},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("assert args = %#v, want %#v", rendered.Args, wantArgs)
	}
}

func TestCallsGoldenDistinctPresentOne(t *testing.T) {
	// One correlated scalar subquery: one-level FLATTEN reproduction,
	// null items dropped, type guard, distinct-count guard, MIN.
	// The registered collection operand is the closed pg-sql-05 direct-
	// property form: a schema-non-repeated path whose stored JSON value
	// is the array (the same form predicates.go registers for selector
	// comparisons; [] steps are unregistered).
	plan := sql10Plan("Patient", nil,
		sql10CallProjection("one_system", sql10Call("distinct_present_one", "STRING",
			sql10Extract(t, "Patient", "gender", ir.PhysicalArrayCardinality, ir.PhysicalEmptyOnNull))),
	)
	rendered := sql10RenderOK(t, plan)
	// EMPTY_ON_NULL arms the array collection through the registered
	// scalar extractor (null_semantics.go behavior table).
	base := `COALESCE(NULLIF((r0."payload" #> $5::text[]), 'null'::jsonb), '""'::jsonb)`
	want := sql10Statement(`((SELECT CASE` +
		` WHEN bool_or(jsonb_typeof(loom_dp_item.item) <> 'string') THEN to_jsonb(CAST('CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH' || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer))` +
		` WHEN COUNT(*) = 0 THEN NULL` +
		` WHEN COUNT(DISTINCT loom_dp_item.item) > 1 THEN to_jsonb(CAST('CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES' || COALESCE(current_setting('loom.explorer_guard_barrier', true), '') AS integer))` +
		` ELSE min(loom_dp_item.item::text)::jsonb` +
		` END FROM jsonb_array_elements(` +
		`CASE WHEN ` + base + ` IS NULL OR jsonb_typeof(` + base + `) <> 'array' THEN NULL::jsonb ELSE ` + base + ` END` +
		`) AS loom_dp_outer(value)` +
		` LEFT JOIN LATERAL jsonb_array_elements(` +
		`CASE WHEN jsonb_typeof(loom_dp_outer.value) = 'array' THEN loom_dp_outer.value ELSE NULL::jsonb END` +
		`) AS loom_dp_flattened(value) ON TRUE` +
		` LEFT JOIN LATERAL (SELECT CASE` +
		` WHEN jsonb_typeof(loom_dp_outer.value) = 'array' THEN loom_dp_flattened.value` +
		` ELSE loom_dp_outer.value END AS item) AS loom_dp_item ON TRUE` +
		` WHERE loom_dp_item.item IS NOT NULL AND loom_dp_item.item <> 'null'::jsonb)) AS "c0"`)
	if rendered.SQL != want {
		t.Fatalf("distinct_present_one SQL =\n%s\nwant\n%s", rendered.SQL, want)
	}
	wantArgs := append(sql10ScopeArgs(),
		command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: "{gender}"},
	)
	if !reflect.DeepEqual(rendered.Args, wantArgs) {
		t.Fatalf("distinct_present_one args = %#v, want %#v", rendered.Args, wantArgs)
	}
}

func TestCallsGoldenDistinctPresentOneLiteralArray(t *testing.T) {
	plan := sql10Plan("Patient", map[string]any{"items": json.RawMessage(`["a","b"]`)},
		sql10CallProjection("one", sql10Call("distinct_present_one", "STRING", sql10ArrayLiteral("items"))),
	)
	rendered := sql10RenderOK(t, plan)
	if !strings.Contains(rendered.SQL, `jsonb_array_elements(CASE WHEN $5::jsonb IS NULL OR jsonb_typeof($5::jsonb) <> 'array' THEN NULL::jsonb ELSE $5::jsonb END)`) {
		t.Fatalf("literal-array distinct form missing: %s", rendered.SQL)
	}
	if !reflect.DeepEqual(rendered.Args[4], command.PostgreSQLParameter{
		Type: command.PostgreSQLParameterJSONB, Value: json.RawMessage(`["a","b"]`)}) {
		t.Fatalf("literal-array arg = %#v", rendered.Args[4])
	}
}

func TestCallsGoldenNestedCall(t *testing.T) {
	// A nested registered call renders through one fragment (the
	// derived-column wrapper shape): eq over two literals inside an
	// outer coalesce.
	plan := sql10Plan("Patient", map[string]any{"a": int64(1), "b": float64(1.0), "c": "z"},
		sql10CallProjection("pick", sql10Call("coalesce", "",
			sql10Nested(sql10Call("eq", "", sql10Literal("a"), sql10Literal("b"))),
			sql10Literal("c"))),
	)
	rendered := sql10RenderOK(t, plan)
	if !strings.Contains(rendered.SQL,
		`(COALESCE(NULLIF((to_jsonb(COALESCE($5::jsonb, 'null'::jsonb) = COALESCE($6::jsonb, 'null'::jsonb))), 'null'::jsonb), NULLIF($7::jsonb, 'null'::jsonb)))`) {
		t.Fatalf("nested call fragment missing: %s", rendered.SQL)
	}
	if len(rendered.Args) != 7 {
		t.Fatalf("nested args = %d, want 7", len(rendered.Args))
	}
}

// ---------------------------------------------------------------------------
// Refusal cells
// ---------------------------------------------------------------------------

func TestCallsRejectUnregisteredNames(t *testing.T) {
	// Every G-row name from the parity table is ir-known and must be
	// refused by this renderer with the registered-surface message.
	for _, name := range []string{
		"first", "all", "distinct", "length", "coalesce_string", "concat", "join",
		"contains", "canonical_json", "reference_id", "path_segment", "basename",
		"last_segment", "sanitize_name", "sanitize_graphql_name", "uuid3", "uuid5",
	} {
		plan := sql10Plan("Patient", map[string]any{"a": int64(1)},
			sql10CallProjection("x", sql10Call(name, "", sql10Literal("a"))))
		sql10Reject(t, plan, "call "+name, "not part of the v1 PostgreSQL registered call surface")
	}
	// Names ir itself rejects fail even earlier (defense in depth, both
	// layers refuse).
	for _, name := range []string{"drop_table", ""} {
		plan := sql10Plan("Patient", map[string]any{"a": int64(1)},
			sql10CallProjection("x", sql10Call(name, "", sql10Literal("a"))))
		sql10RejectAny(t, plan, "ir-known refusal "+name, "call")
	}
}

func TestCallsRejectUnknownLiteralTypes(t *testing.T) {
	// Unknown call/type pairs are refused before execution: a literal
	// bind whose Go type is not one of int64/float64/string/bool/nil/
	// array-RawMessage never reaches the driver.
	for name, bind := range map[string]any{
		"int":        int(1),
		"uint64":     uint64(1),
		"time":       time.Now(),
		"map":        map[string]any{"k": 1},
		"slice":      []string{"a"},
		"object":     json.RawMessage(`{"k":1}`),
		"null-raw":   json.RawMessage(`null`),
		"number-raw": json.RawMessage(`1`),
	} {
		plan := sql10Plan("Patient", map[string]any{"a": bind},
			sql10CallProjection("x", sql10Call("coalesce", "", sql10Literal("a"))))
		sql10Reject(t, plan, "literal "+name, "unknown call/type pair")
	}
	// Infinity has no JSON and no jsonb representation.
	plan := sql10Plan("Patient", map[string]any{"a": math.Inf(1)},
		sql10CallProjection("x", sql10Call("coalesce", "", sql10Literal("a"))))
	sql10Reject(t, plan, "infinite float", "not JSON-encodable")
	// Arithmetic demands number binds.
	stringArith := sql10Plan("Patient", map[string]any{"a": "x", "b": int64(1)},
		sql10CallProjection("x", sql10Call("add", "", sql10Literal("a"), sql10Literal("b"))))
	sql10Reject(t, stringArith, "string arithmetic literal", "arithmetic accepts number binds")
	// A missing bind is refused, never rendered as a guess.
	missing := sql10Plan("Patient", nil,
		sql10CallProjection("x", sql10Call("coalesce", "", sql10Literal("ghost"))))
	sql10RejectAny(t, missing, "missing bind", "ghost")
}

func TestCallsRejectPlanLevelShapes(t *testing.T) {
	literal := func(key string) ir.PhysicalExpression { return sql10Literal(key) }
	binds := map[string]any{"a": int64(1), "b": int64(2), "m": int64(1), "t": true,
		"items": json.RawMessage(`["a"]`), "words": "not-an-array"}

	cases := []struct {
		context string
		call    *ir.PhysicalExpression
		want    string
	}{
		{"not arity", sql10Call("not", "", literal("a"), literal("b")), "requires 1 argument"},
		{"and arity", sql10Call("and", "", literal("a")), "requires at least 2 arguments"},
		{"or arity", sql10Call("or", "", literal("a")), "requires at least 2 arguments"},
		{"if arity", sql10Call("if", "", literal("a"), literal("b")), "requires 3 arguments"},
		{"case arity", sql10Call("case", "", literal("a")), "requires at least one condition/result pair"},
		{"coalesce arity", sql10Call("coalesce", ""), "requires at least one argument"},
		{"cast date gap", sql10Call("cast", "date", literal("a")), "date/date_time coercions are gaps"},
		{"cast date_time gap", sql10Call("cast", "date_time", literal("a")), "date/date_time coercions are gaps"},
		{"assert number message", sql10Call("assert", "", literal("t"), literal("m")), "must be a string"},
		{"assert row-path message", sql10Call("assert", "", literal("t"), sql10Value("payload", "gender")),
			"bind-backed string literal"},
		{"aggregate operand", sql10Call("coalesce", "", sql10Aggregate(ir.PhysicalScalarCardinality)), "capability gaps"},
		{"typed column operand", sql10Call("coalesce", "", sql10Value("logical_id")), "not storage columns"},
		{"distinct VALUE argument", sql10Call("distinct_present_one", "STRING", func() ir.PhysicalExpression {
			value := sql10Value("payload", "identifier")
			value.Cardinality = ir.PhysicalArrayCardinality
			return value
		}()), "is not registered"},
		{"distinct aggregate argument", sql10Call("distinct_present_one", "STRING",
			sql10Aggregate(ir.PhysicalArrayCardinality)), "is not registered"},
		{"distinct string literal", sql10Call("distinct_present_one", "STRING", sql10ArrayLiteral("words")),
			"must bind a JSON array"},
	}
	for _, cell := range cases {
		plan := sql10Plan("Patient", binds, sql10CallProjection("x", cell.call))
		sql10Reject(t, plan, cell.context, cell.want)
	}

	// ir-side refusals for the same classes (defense in depth upstream).
	irCells := []struct {
		context string
		call    *ir.PhysicalExpression
		want    string
	}{
		{"add arity", sql10Call("add", "", literal("a")), "two arguments"},
		{"assert arity", sql10Call("assert", "", literal("a")), "condition and message"},
		{"cast arity", sql10Call("cast", "string", literal("a"), literal("b")), "one argument"},
		{"distinct arity", sql10Call("distinct_present_one", "STRING", literal("items"), literal("a")), "array argument"},
		{"distinct bad kind", sql10Call("distinct_present_one", "OBSERVATION", sql10ArrayLiteral("items")), "target kind"},
		{"add with target kind", sql10Call("add", "STRING", literal("a"), literal("b")), "target kind"},
		{"cast unknown target", sql10Call("cast", "observation", literal("a")), "unsupported"},
		{"cast missing target", sql10Call("cast", "", literal("a")), "requires one argument"},
	}
	for _, cell := range irCells {
		plan := sql10Plan("Patient", binds, sql10CallProjection("x", cell.call))
		sql10RejectAny(t, plan, cell.context, cell.want)
	}
}

// TestCallsValueOperandRefusesNonJSONDerivedScope (review thread
// r4529631198) proves a CALL VALUE operand may not read an ordinality or
// has-item derived row: those scopes carry raw integer/boolean SQL
// (unnest.go:683,687) which resolveDerivedValue hands back verbatim for a
// bare path (unnest.go:716-718), so accepting them would put
// (lateral.rn - 1) into jsonb operand positions and trade this file's
// typed pre-SQL refusal for a raw 42804 at prepare time — the posture
// renderCallExtractOperand already takes against derived rows. Driven
// directly against the renderer's row bindings because the read-spine
// contract refuses CALL projections on the unnest slice upstream; this arm
// is the defense-in-depth layer beneath that gate. The registered JSON
// derived rows (unnest item values, derivedJSON=true) must still be
// accepted.
func TestCallsValueOperandRefusesNonJSONDerivedScope(t *testing.T) {
	r := &planRenderer{rows: map[string]rowScope{
		"position": {derivedValue: "(lateral.rn - 1)"},
		"has_item": {derivedValue: "(lateral.rn IS NOT NULL)"},
		"item":     {derivedValue: "(lateral.item.value)", derivedJSON: true},
	}}
	value := func(variable string) ir.PhysicalExpression {
		return ir.PhysicalExpression{
			Kind:         ir.PhysicalValueExpression,
			Cardinality:  ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Value:        &ir.PhysicalValue{Variable: variable},
		}
	}
	for _, variable := range []string{"position", "has_item"} {
		_, err := r.renderCallValueOperand(value(variable))
		if err == nil || !strings.Contains(err.Error(), "ordinality and has-item") {
			t.Fatalf("derived non-JSON operand %q: error %v lacks the typed refusal", variable, err)
		}
	}
	operand, err := r.renderCallValueOperand(value("item"))
	if err != nil {
		t.Fatalf("JSON-derived row must stay accepted: %v", err)
	}
	// The registered JSON derived value resolves through the derived arm
	// (parenthesizing is the renderers'; only the source identity is the
	// assertion here — never a raw column or storage-alias substitution).
	if !strings.Contains(operand.sql, "lateral.item.value") {
		t.Fatalf("JSON-derived operand fragment = %q", operand.sql)
	}
}

// TestCallsRejectFragmentByteBudget (review thread r4529631959) proves the
// registered fragment budget bounds emitted SQL size on its own: operand
// replication (the arithmetic band test plus the callDoubleOperand guards
// embed each operand around eight times per level, callTruthyTest/
// callNumericCoercion ×5-7) multiplies per nesting level, so a left-nested
// chain far inside the 64-level depth cap would otherwise expand beyond
// memory before the depth guard fires. The budget must refuse with the
// typed error first, while the depth cap still governs chains below the
// byte bound's reach.
func TestCallsRejectFragmentByteBudget(t *testing.T) {
	nest := func(levels int) *ir.PhysicalExpression {
		expression := sql10Literal("one")
		for range levels {
			expression = sql10Nested(sql10Call("add", "", expression, sql10Literal("one")))
		}
		return &expression
	}
	// 40 levels: replication alone exceeds the 1 MiB bound long before
	// the depth guard's 64.
	sql10Reject(t, sql10Plan("Patient", map[string]any{"one": int64(1)},
		sql10CallProjection("x", nest(40))), "fragment budget", "exceeds the")
	// A legitimate depth (lowering nests at most three) stays far inside
	// the budget and renders: the registered bound never touches a real
	// plan shape.
	shallow := sql10Plan("Patient", map[string]any{"one": int64(1)},
		sql10CallProjection("x", nest(4)))
	rendered := sql10RenderOK(t, shallow)
	if len(rendered.SQL) >= maxCallFragmentBytes {
		t.Fatalf("legitimate 8-level chain produced %d bytes", len(rendered.SQL))
	}
}

func TestCallsRejectOperandContractViolations(t *testing.T) {
	// EXTRACT operands keep the closed-selector contract: fallbacks…
	fallback := sql10Plan("Patient", map[string]any{"a": int64(1)},
		sql10CallProjection("x", sql10Call("coalesce", "", func() ir.PhysicalExpression {
			expression := sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
			expression.Extract.ExecutionMode = ir.PhysicalSelectorGeneric
			expression.Extract.Fallbacks = []ir.PhysicalSelectorFallback{{
				Source:       ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
				ResourceType: "Patient",
				Selector:     sql10Selector(t, "active"),
			}}
			return expression
		}())))
	sql10Reject(t, fallback, "extract fallbacks", "fallback selectors are a separate capability")

	// …repeated steps (GENERIC mode keeps ir happy; the renderer owns
	// the closed-path rule)…
	repeated := sql10Plan("Patient", map[string]any{"a": int64(1)},
		sql10CallProjection("x", sql10Call("coalesce", "", func() ir.PhysicalExpression {
			expression := sql10Extract(t, "Patient", "identifier.system", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull)
			expression.Extract.ExecutionMode = ir.PhysicalSelectorGeneric
			expression.Extract.Selector.Steps[0].Iterate = true
			return expression
		}())))
	sql10Reject(t, repeated, "repeated step", "repeated")

	// …and the resource-type identity against the scanned row.
	wrongType := sql10Plan("Patient", map[string]any{"a": int64(1)},
		sql10CallProjection("x", sql10Call("coalesce", "",
			sql10Extract(t, "Observation", "status", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull))))
	sql10Reject(t, wrongType, "resource-type contradiction", "scanned as")

	// Posture violations on the CALL projection itself.
	arrayCall := sql10Plan("Patient", map[string]any{"a": int64(1)},
		ir.PhysicalProjection{Name: "x", Expression: func() *ir.PhysicalExpression {
			call := sql10Call("coalesce", "", sql10Literal("a"))
			call.Cardinality = ir.PhysicalArrayCardinality
			return call
		}()})
	sql10Reject(t, arrayCall, "array-cardinality CALL", "SCALAR-cardinality")
	omitCall := sql10Plan("Patient", map[string]any{"a": int64(1)},
		ir.PhysicalProjection{Name: "x", Expression: func() *ir.PhysicalExpression {
			call := sql10Call("coalesce", "", sql10Literal("a"))
			call.NullBehavior = ir.PhysicalOmitNulls
			return call
		}()})
	sql10Reject(t, omitCall, "omit-nulls CALL", "SCALAR-cardinality")

	// Presence metadata and hidden posture keep their refusals.
	presenceCell := sql10Plan("Patient", map[string]any{"a": int64(1)},
		ir.PhysicalProjection{Name: "x", Presence: &ir.PhysicalProjectionPresence{
			Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
			Paths:  [][]string{{"gender"}},
		}, Expression: sql10Call("coalesce", "", sql10Literal("a"))})
	sql10Reject(t, presenceCell, "presence on CALL", "presence proofs pair with selector extractions only")
	hiddenCell := sql10Plan("Patient", map[string]any{"a": int64(1)},
		func() ir.PhysicalProjection {
			projection := sql10CallProjection("x", sql10Call("coalesce", "", sql10Literal("a")))
			projection.Hidden = true
			return projection
		}())
	sql10Reject(t, hiddenCell, "hidden CALL", "hidden executor-side metadata")

	// Nesting beyond the registered bound: 66 coalesce nodes put the
	// innermost call at depth 65, one past the 64-level guard.
	binds := map[string]any{"a": int64(1)}
	var inner ir.PhysicalExpression = sql10Literal("a")
	for index := 0; index <= maxCallExpressionDepth+1; index++ {
		inner = sql10Nested(sql10Call("coalesce", "", inner))
	}
	deep := sql10Plan("Patient", binds, sql10CallProjection("x", &inner))
	sql10Reject(t, deep, "deep nesting", "registered bound")
}

func TestCallsDefenseInDepthShapeWalk(t *testing.T) {
	// The render-walk re-read is callable directly: arity/TargetKind
	// shapes ir also refuses must be refused here too, with the typed
	// renderer error, independent of the classifier path.
	literal := func(key string) ir.PhysicalExpression { return sql10Literal(key) }
	shape := func(name, target string, args ...ir.PhysicalExpression) *ir.PhysicalCall {
		return &ir.PhysicalCall{Name: name, TargetKind: target, Args: args}
	}
	cells := []struct {
		context string
		call    *ir.PhysicalCall
		want    string
	}{
		{"add 3-arg", shape("add", "", literal("a"), literal("a"), literal("a")), "requires 2 arguments"},
		{"eq 1-arg", shape("eq", "", literal("a")), "requires 2 arguments"},
		{"gt 3-arg", shape("gt", "", literal("a"), literal("a"), literal("a")), "requires 2 arguments"},
		{"if 2-arg", shape("if", "", literal("a"), literal("a")), "requires 3 arguments"},
		{"case 1-arg", shape("case", "", literal("a")), "at least one condition/result pair"},
		{"assert 1-arg", shape("assert", "", literal("a")), "condition and a message"},
		{"distinct 2-arg", shape("distinct_present_one", "STRING", literal("a"), literal("a")), "one array argument"},
		{"cast unknown", shape("cast", "instant", literal("a")), "not registered for PostgreSQL"},
		{"add target kind", shape("add", "STRING", literal("a"), literal("a")), "must not carry target kind"},
		{"unknown name", shape("drop_table", "", literal("a")), "not part of the v1 PostgreSQL registered call surface"},
	}
	for _, cell := range cells {
		if err := validateCallShape("probe", cell.call, 0); !errors.Is(err, ErrUnsupportedPostgresPlan) {
			t.Fatalf("%s: validateCallShape error = %v, want ErrUnsupportedPostgresPlan", cell.context, err)
		} else if !strings.Contains(err.Error(), cell.want) {
			t.Fatalf("%s: error %q lacks %q", cell.context, err.Error(), cell.want)
		}
	}
}

func TestCallsSelectorSliceStaysClosed(t *testing.T) {
	// Claim D0-b.4: the closed selector-extraction entry keeps refusing
	// CALL projections; the call surface registers on the full spine only.
	plan := sql10Plan("Patient", map[string]any{"a": int64(1)},
		sql10CallProjection("x", sql10Call("coalesce", "", sql10Literal("a"))))
	_, err := RenderSelectorExtraction(plan, mustBinding(t))
	if !errors.Is(err, ErrUnsupportedPostgresPlan) {
		t.Fatalf("selector-slice CALL error = %v, want ErrUnsupportedPostgresPlan", err)
	}
	if !strings.Contains(err.Error(), "full read spine") {
		t.Fatalf("selector-slice CALL error names no spine: %q", err.Error())
	}
}

func TestCallsDeterministic(t *testing.T) {
	plan := sql10Plan("Patient", map[string]any{"a": int64(2), "b": "x"},
		sql10CallProjection("sum", sql10Call("add", "", sql10Literal("a"), sql10Literal("a"))),
		sql10CallProjection("pick", sql10Call("fallback", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("b"))),
	)
	first := sql10RenderOK(t, plan)
	second, err := RenderPhysicalPlan(plan, mustBinding(t))
	if err != nil {
		t.Fatalf("second render: %v", err)
	}
	if first.SQL != second.SQL {
		t.Fatalf("SQL drifted between renders:\n%s\n%s", first.SQL, second.SQL)
	}
	if !reflect.DeepEqual(first.Args, second.Args) {
		t.Fatalf("args drifted between renders: %#v vs %#v", first.Args, second.Args)
	}
	// No AQL syntax and no raw plan text anywhere in the statement.
	for _, forbidden := range []string{"@", "FOR ", "RETURN ", "FILTER ", sql10Project, sql10Gen} {
		if strings.Contains(first.SQL, forbidden) {
			t.Fatalf("SQL leaked %q: %s", forbidden, first.SQL)
		}
	}
}

func TestCallsRowVariableWalkVisitsOperands(t *testing.T) {
	// The classifier walk covers VALUE, EXTRACT, and nested CALL
	// operands; unknown operand kinds are left to the render refusal.
	visited := 0
	err := forEachCallOperandRowVariable(*sql10Call("coalesce", "",
		sql10Value("payload", "gender"),
		sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
		sql10Nested(sql10Call("not", "", sql10Value("payload", "active")))),
		func(variable string, extract *ir.PhysicalExtract) error {
			visited++
			if variable != "root" {
				t.Fatalf("visited variable %q, want root", variable)
			}
			if extract != nil && extract.ResourceType != "Patient" {
				t.Fatalf("visited extract resource type %q", extract.ResourceType)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if visited != 3 {
		t.Fatalf("visited %d operands, want 3", visited)
	}
}

func TestCallsRankComparatorIsTotalOverNulls(t *testing.T) {
	// L1/D5: the comparator never propagates SQL NULL across ranks — a
	// missing key still compares. Golden pins the rank CASE arms for
	// both operands (null-rank 0 first) so the total structure is
	// visible; the live suite pins the values.
	plan := sql10Plan("Patient", map[string]any{"s": "female"},
		sql10CallProjection("ordered", sql10Call("lte", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("s"))),
	)
	rendered := sql10RenderOK(t, plan)
	rankArms := []string{"THEN 0", "THEN 1", "THEN 2", "THEN 3", "ELSE 4 END"}
	for _, arm := range rankArms {
		if !strings.Contains(rendered.SQL, arm) {
			t.Fatalf("rank arm %q missing: %s", arm, rendered.SQL)
		}
	}
	if !strings.Contains(rendered.SQL, `>= 4 OR `) || !strings.Contains(rendered.SQL, `>= 4 THEN NULL`) {
		t.Fatalf("rank-4 fail-closed arm missing: %s", rendered.SQL)
	}
}

// sql10ParameterPattern finds every $n reference in a statement.
var sql10ParameterPattern = regexp.MustCompile(`\$(\d+)`)

func TestCallsEveryParameterReferencedExactlyInOrder(t *testing.T) {
	plan := sql10Plan("Patient", map[string]any{"a": int64(1), "msg": "TAG"},
		sql10CallProjection("checked", sql10Call("assert", "",
			sql10Extract(t, "Patient", "gender", ir.PhysicalScalarCardinality, ir.PhysicalPreserveNull),
			sql10Literal("msg"))),
		sql10CallProjection("flag", sql10Call("cast", "boolean", sql10Literal("a"))),
	)
	rendered := sql10RenderOK(t, plan)
	if len(rendered.Args) != 7 {
		t.Fatalf("args = %d, want 7", len(rendered.Args))
	}
	seen := map[int]bool{}
	for _, match := range sql10ParameterPattern.FindAllStringSubmatch(rendered.SQL, -1) {
		var index int
		fmt.Sscanf(match[0], "$%d", &index)
		seen[index] = true
	}
	for index := 1; index <= len(rendered.Args); index++ {
		if !seen[index] {
			t.Fatalf("$%d unreferenced (constructor should have refused): %s", index, rendered.SQL)
		}
	}
}
