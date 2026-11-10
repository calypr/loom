package postgres

// This file registers scalar filter comparisons and boolean predicates as
// implemented, closed PostgreSQL operations (pg-sql-05). It is the
// renderer-facing capability registration point for the operations named by
// the physical IR's PhysicalFilter: every FILTER clause the read spine
// renders is built here, and shape validation — not assembly — lives in
// render.go, which remains the package's single statement builder. No second
// SQL builder exists: this file emits predicate fragments and binds their
// parameters; render.go composes them into the WHERE list.
//
// Two comparison shapes are registered.
//
// (1) Direct value comparisons (established by pg-sql-01/02): one row-path
// PhysicalValue on the left (a trusted typed column or a JSONB document
// path) against one bind-backed right value. The bind's Go type must match
// the column's storage type; when PhysicalPredicate.ValueKind is set it
// additionally drives parameter typing and is validated against the closed
// operator/kind compatibility in internal/dataframe/spec
// (OperatorSupportsKind), so an operator the typed filter AST does not
// support for that kind — and a bind value whose Go type contradicts the
// declared kind — is rejected with a typed error before any SQL text is
// produced. ValueKind "" keeps the storage-shape-driven dispatch of
// pg-sql-02 unchanged.
//
// (2) Selector-expression comparisons (this ticket): the exact closed
// construction lowering emits in internal/dataframe/compiler/lower
// (typed_predicate.go, graph_plan.go, required_match.go):
// PhysicalPredicate{LeftExpression: PhysicalExpression{Kind: EXTRACT,
// Cardinality: ARRAY, NullBehavior: EMPTY_ON_NULL, Extract: PhysicalExtract
// {Source: {Variable: <readable row>, Path: ["payload"]}, ResourceType:
// <scanned row type>, Selector: direct property path (no repeated, indexed,
// or filtered steps), no fallbacks, no prepared reference, no unit
// normalization, no DISTINCT, ExecutionMode generic or direct-scalar}},
// bind-backed Right, Quantifier ""/ANY/NONE/ALL from a TypedFilter}.
//
// Null/missing quantifier preservation is the contract this shape must keep.
// The AQL reference (render/aql/predicates.go renderSelectorPredicate over
// the null-filtered loop of render/aql/selectors.go
// renderSelectorArrayFromSource) iterates a value array from which every
// null — a missing key or a stored JSON null alike, because AQL reads both
// as null and the loop applies FILTER value != null — has been dropped. A
// direct property path therefore contributes a 0..1-item multiset: absent,
// SQL-NULL payload, or stored JSON null give the empty multiset; every other
// stored value (including arrays and arrays containing null, which the
// non-iterated terminal never unpacks) gives exactly one item, the whole
// stored value. PostgreSQL reproduces that multiset with the two-valued
// presence proof present := COALESCE(jsonb_typeof(extract), 'null') <>
// 'null' (the same reading the direct EXISTS/MISSING comparisons and the
// presence proofs in null_semantics.go already use — SQL NULL, JSON null,
// and missing stay distinct in storage and are never flattened here):
//
//	quantifier   AQL                                     registered SQL
//	""/ANY       LENGTH(FOR ... FILTER m LIMIT 1) > 0    present AND m
//	NONE         LENGTH(FOR ... FILTER m LIMIT 1) == 0   NOT (present AND m)
//	ALL          LENGTH(values) > 0 AND no non-matching  present AND m
//	EXISTS       LENGTH(values) > 0                      present
//	MISSING      LENGTH(values) == 0                     NOT present
//
// Because the quantifier result is total two-valued SQL (false AND NULL is
// false, and a present item never compares NULL except under the temporal
// guard, which COALESCEs), NOT over these predicates needs no null guard —
// the AQL side also evaluates them two-valued. The direct-form payload
// comparisons remain three-valued (a missing key compared against a value
// is SQL NULL where AQL reads false), so predicateFragment.mayYieldNull
// keeps the existing COALESCE(NOT (...), true) discipline for them, and
// AND/OR combine it conservatively: a disjunction is mayYieldNull when any
// child is, because SQL and AQL AND/OR agree in WHERE position (only true
// survives; NULL and false are both dropped).
//
// Value parameterization never changes: comparison values are ordered,
// typed $n parameters (jsonb for document paths; text/bigint/boolean/
// timestamptz for typed columns), document paths bind as one whole text[]
// parameter through documentPathExpression (null_semantics.go remains the
// package's single path-binding authority, so extraction and comparison
// paths cannot drift), and no plan string — path segment, operator, or
// value — is ever interpolated into SQL text. The temporal comparison
// guards are renderer-owned contract constants fixed in this file, never
// plan data and never parameters.
//
// Everything outside the registered shapes — correlated predicates,
// CONTAINS_TEXT (its AQL TO_STRING reading for non-string stored values has
// no registered PostgreSQL equivalence and keeps its pg-sql-02 fail-closed
// refusal), selector expressions with fallbacks, prepared sets, unit
// normalization, DISTINCT, selector filters, repeated/indexed steps, a
// non-EMPTY_ON_NULL array extraction, object/scalar cardinality, a
// quantifier on the direct value form, a payload-path IN or ordering
// comparison the closed kind table does not admit, an operator/kind pair
// outside spec.OperatorSupportsKind when ValueKind is set, a bind type that
// contradicts the declared kind, an unparsable temporal literal, or a
// null-literal right value on the selector form (every mapping would be a
// constant stranding a required parameter, matching the direct-form
// LT/GTE-against-null precedent) — is a capability gap: a typed
// ErrUnsupportedPostgresPlan refusal that yields no SQL. There is no
// fallback to the AQL renderer and no AQL-to-SQL conversion here.
//
// Planner-facing command registration (compiler.CompiledCommandForBackend)
// stays fail-closed for PostgreSQL exactly as in pg-sql-01/02/03: this file
// — not command dispatch — is where the scalar filter comparison and
// boolean predicate operations are implemented and registered.
//
// One documented divergence: a stored string under a DATE/DATE_TIME ordering
// comparison that is not a full-precision instant (for example the partial
// FHIR date "2024", or an invalid-calendar shape such as 2024-02-30,
// 2024-13-01, 2024-01-01T25:70:99Z, an offset beyond ±15:59 such as
// +16:00, or a comma decimal such as 10:20:30,5) is never-comparable
// here — the guard yields SQL NULL and the row is excluded — where AQL's
// DATE_TIMESTAMP would fold such values and possibly match or raise. The
// guard's regexes are calendar-aware precisely so its THEN branches never
// hand PostgreSQL a string it would reject: the renderer never includes a
// row it cannot order and never raises on stored data; exclusion is the
// fail-closed outcome, and the live fixture pins it. On the literal side a
// comma decimal such as 10:20:30,5 is a typed refusal at render time (the
// spec-bound Go parse rejects it), where AQL would execute the comparison
// with DATE_TIMESTAMP(literal) folding to NaN and simply match nothing:
// same visible outcome, zero rows, but refused loudly rather than silently.
// A second ordering divergence is closed the same way: INTEGER/DECIMAL ordering comparisons
// over a document path require jsonb_typeof of the stored item to be
// 'number' before any ordering operator reaches it. PostgreSQL orders jsonb
// across types as Null, String, Number, Boolean, Array, Object while
// ArangoDB orders null, false, true, numbers, strings, arrays, objects —
// exactly the swap this ticket's typed ordering registers touch — so a raw
// jsonb ordering comparison would over-select under LTE/LT and under-select
// under GTE/GT against AQL. A non-numeric stored value reads
// never-comparable and is excluded; the live matrix stores a string under a
// numeric kind to pin it.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func (r *planRenderer) renderFilter(plan ir.PhysicalPlan, filter ir.PhysicalFilter) (string, error) {
	if filter.Expression != nil {
		return r.renderPredicateExpression(plan, *filter.Expression)
	}
	return r.renderComparison(plan, filter.Predicate)
}

func (r *planRenderer) renderPredicateExpression(plan ir.PhysicalPlan, predicate ir.PhysicalPredicateExpression) (string, error) {
	fragment, err := r.renderPredicateFragment(plan, predicate)
	if err != nil {
		return "", err
	}
	return fragment.sql, nil
}

// predicateFragment carries rendered SQL plus whether the predicate can
// evaluate to SQL NULL. A three-valued predicate under NOT drops rows that
// AQL keeps (AQL coerces NOT(null) to true), so negation must know.
type predicateFragment struct {
	sql          string
	mayYieldNull bool
}

func (r *planRenderer) renderPredicateFragment(plan ir.PhysicalPlan, predicate ir.PhysicalPredicateExpression) (predicateFragment, error) {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return predicateFragment{}, fmt.Errorf("%w: COMPARISON predicate has no payload", ErrUnsupportedPostgresPlan)
		}
		return r.renderComparisonFragment(plan, *predicate.Comparison)
	case ir.PhysicalNotPredicate:
		if len(predicate.Children) != 1 {
			return predicateFragment{}, fmt.Errorf("%w: NOT requires exactly one child", ErrUnsupportedPostgresPlan)
		}
		child, err := r.renderPredicateFragment(plan, predicate.Children[0])
		if err != nil {
			return predicateFragment{}, err
		}
		if child.mayYieldNull {
			return predicateFragment{sql: "COALESCE(NOT (" + child.sql + "), true)"}, nil
		}
		return predicateFragment{sql: "NOT (" + child.sql + ")"}, nil
	case ir.PhysicalAllPredicate:
		if len(predicate.Children) == 0 {
			return predicateFragment{}, fmt.Errorf("%w: ALL requires at least one child", ErrUnsupportedPostgresPlan)
		}
		parts := make([]string, 0, len(predicate.Children))
		mayYieldNull := false
		for _, child := range predicate.Children {
			part, err := r.renderPredicateFragment(plan, child)
			if err != nil {
				return predicateFragment{}, err
			}
			mayYieldNull = mayYieldNull || part.mayYieldNull
			parts = append(parts, "("+part.sql+")")
		}
		return predicateFragment{sql: strings.Join(parts, " AND "), mayYieldNull: mayYieldNull}, nil
	case ir.PhysicalAnyPredicate:
		// WHERE keeps only true and AQL FILTER likewise keeps only true, so
		// SQL and AQL disjunctions coincide row-for-row even when operands
		// are three-valued. A disjunction may yield NULL exactly when some
		// child may: with no child true, the SQL side is NULL-or-false while
		// the AQL side is false-equivalent, which the NOT guard absorbs.
		if len(predicate.Children) == 0 {
			return predicateFragment{}, fmt.Errorf("%w: ANY requires at least one child", ErrUnsupportedPostgresPlan)
		}
		parts := make([]string, 0, len(predicate.Children))
		mayYieldNull := false
		for _, child := range predicate.Children {
			part, err := r.renderPredicateFragment(plan, child)
			if err != nil {
				return predicateFragment{}, err
			}
			mayYieldNull = mayYieldNull || part.mayYieldNull
			parts = append(parts, "("+part.sql+")")
		}
		return predicateFragment{sql: strings.Join(parts, " OR "), mayYieldNull: mayYieldNull}, nil
	default:
		return predicateFragment{}, fmt.Errorf("%w: predicate kind %q is not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, string(predicate.Kind))
	}
}

func (r *planRenderer) renderComparison(plan ir.PhysicalPlan, predicate ir.PhysicalPredicate) (string, error) {
	fragment, err := r.renderComparisonFragment(plan, predicate)
	if err != nil {
		return "", err
	}
	return fragment.sql, nil
}

func (r *planRenderer) renderComparisonFragment(plan ir.PhysicalPlan, predicate ir.PhysicalPredicate) (predicateFragment, error) {
	if predicate.Correlation != nil {
		return predicateFragment{}, fmt.Errorf("%w: correlated predicates are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.Left.BindKey != "" {
		return predicateFragment{}, fmt.Errorf("%w: filter left side must be a row path, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if predicate.LeftExpression != nil {
		return r.renderSelectorComparisonFragment(plan, predicate)
	}
	if predicate.Quantifier != "" {
		return predicateFragment{}, fmt.Errorf("%w: array quantifier %q is not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
	left, err := r.resolveValue(predicate.Left)
	if err != nil {
		return predicateFragment{}, err
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}
	switch operator {
	case "EXISTS", "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: %s must not carry a right value", ErrUnsupportedPostgresPlan, operator)
		}
		if isPayloadExpression(left) {
			presence := "COALESCE(jsonb_typeof(" + left + "), 'null')"
			if operator == "EXISTS" {
				return predicateFragment{sql: presence + " <> 'null'"}, nil
			}
			return predicateFragment{sql: presence + " = 'null'"}, nil
		}
		// Registered storage columns are stored NOT NULL and jsonb_typeof has
		// no text/timestamptz input, so column presence is nullability itself;
		// emitting jsonb_typeof over a column would only fail at execution.
		if operator == "EXISTS" {
			return predicateFragment{sql: left + " IS NOT NULL"}, nil
		}
		return predicateFragment{sql: left + " IS NULL"}, nil
	case "EQUALS", "NOT_EQUALS", "GT", "GTE", "LT", "LTE", "IN":
	default:
		// CONTAINS_TEXT keeps its pg-sql-02 fail-closed refusal: AQL's
		// TO_STRING reading for non-string stored values has no registered
		// PostgreSQL equivalence yet.
		return predicateFragment{}, fmt.Errorf("%w: filter operator %q is not part of the v1 PostgreSQL surface",
			ErrUnsupportedPostgresPlan, predicate.Operator)
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	if !isPayloadExpression(left) {
		return r.renderColumnComparison(left, operator, predicate.ValueKind, value)
	}
	return r.renderPayloadComparison(left, operator, predicate.ValueKind, value)
}

// validateDirectOperatorKind is the boundary check the typed filter AST
// already enforced before lowering; re-reading it here means a hand-built
// plan cannot pair an operator with a value kind the closed spec rejects.
// ValueKind "" is the untyped direct form and keeps the operator set the
// renderer itself admits.
func validateDirectOperatorKind(operator string, kind spec.FilterValueKind) error {
	switch operator {
	case "GT", "GTE", "LT", "LTE":
		if kind != "" && !spec.OperatorSupportsKind(spec.FilterOperator(operator), kind) {
			return fmt.Errorf("%w: operator %s is not compatible with value kind %q",
				ErrUnsupportedPostgresPlan, operator, string(kind))
		}
	}
	return nil
}

func isPayloadExpression(expression string) bool { return strings.Contains(expression, " #> $") }

// renderColumnComparison binds comparisons against typed columns. Values
// arrive in their canonical Go types; anything else fails closed rather
// than being stringified into the statement or silently coerced. When a
// value kind is declared it must agree with the column's storage type:
// text columns accept STRING/CODE, the recorded-at column accepts an
// INTEGER-free temporal literal (time.Time, or a full-precision DATE /
// RFC3339 DATE_TIME string, which becomes a timestamptz parameter), and
// every other pairing is a typed refusal before SQL.
func (r *planRenderer) renderColumnComparison(left, operator string, kind spec.FilterValueKind, value any) (predicateFragment, error) {
	isRecordedAt := strings.HasSuffix(left, "."+r.storage.Resource.MustColumn("recorded_at").SQLText())
	if operator == "IN" {
		paths, ok := value.([]string)
		if !ok {
			return predicateFragment{}, fmt.Errorf("%w: column IN requires a []string bind value", ErrUnsupportedPostgresPlan)
		}
		raw, err := json.Marshal(append([]string(nil), paths...))
		if err != nil {
			return predicateFragment{}, fmt.Errorf("%w: column IN values are not JSON-encodable: %v", ErrUnsupportedPostgresPlan, err)
		}
		if string(raw) == "null" {
			// An empty (or nil) slice marshals to JSON null, and
			// jsonb_array_elements_text over a jsonb null scalar raises
			// "cannot extract elements from a scalar". Binding an empty array
			// makes ANY read zero rows, so the predicate is false, matching
			// the AQL reading of x IN [].
			raw = []byte("[]")
		}
		return predicateFragment{sql: left + " = ANY (SELECT jsonb_array_elements_text($" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb))"}, nil
	}
	if isRecordedAt {
		if kind != "" && kind != spec.FilterDate && kind != spec.FilterDateTime {
			return predicateFragment{}, fmt.Errorf("%w: value kind %q has no registered recorded-at comparison; that column admits only temporal comparisons",
				ErrUnsupportedPostgresPlan, string(kind))
		}
		timestamp, err := temporalComparisonValue(kind, value)
		if err != nil {
			return predicateFragment{}, err
		}
		index := r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterTimestamptz, Value: timestamp}, "")
		return predicateFragment{sql: left + " " + sqlComparison(operator) + " $" + strconv.Itoa(index)}, nil
	}
	if kind != "" && kind != spec.FilterString && kind != spec.FilterCode {
		return predicateFragment{}, fmt.Errorf("%w: value kind %q has no registered typed-column comparison; text columns accept STRING/CODE",
			ErrUnsupportedPostgresPlan, string(kind))
	}
	text, ok := value.(string)
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: typed column comparisons require a string bind value, got %T",
			ErrUnsupportedPostgresPlan, value)
	}
	return predicateFragment{sql: left + " " + sqlComparison(operator) + " $" + strconv.Itoa(r.textParameter(text, "text:"+text))}, nil
}

// temporalComparisonValue converts a declared DATE/DATE_TIME filter literal
// into an instant. The spec already requires full precision before an
// ordered comparison is lowered, so a partial or unparsable literal here is
// a rejected plan, never a lexical text comparison that silently misorders.
// Date-only literals read as UTC midnight, the same instant AQL's
// DATE_TIMESTAMP assigns them.
func temporalComparisonValue(kind spec.FilterValueKind, value any) (time.Time, error) {
	switch typed := value.(type) {
	case time.Time:
		if kind == "" || kind == spec.FilterDate || kind == spec.FilterDateTime {
			// Normalize to UTC: the same instant, but the bind text
			// PostgreSQL encodes always carries +00:00, so no time.Time
			// carrying a local zone can smuggle a displacement past the
			// ±15:59 range PostgreSQL accepts in a timestamptz cast.
			return typed.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%w: value kind %q cannot bind a timestamp", ErrUnsupportedPostgresPlan, string(kind))
	case string:
		if kind == spec.FilterDate {
			parsed, err := time.Parse("2006-01-02", typed)
			if err != nil {
				return time.Time{}, fmt.Errorf("%w: DATE comparison literal %q is not a full-precision YYYY-MM-DD date",
					ErrUnsupportedPostgresPlan, typed)
			}
			return parsed.UTC(), nil
		}
		if kind == spec.FilterDateTime || kind == "" {
			parsed, err := time.Parse(time.RFC3339Nano, typed)
			if err != nil {
				return time.Time{}, fmt.Errorf("%w: DATE_TIME comparison literal %q is not a full RFC3339 timestamp",
					ErrUnsupportedPostgresPlan, typed)
			}
			return parsed.UTC(), nil
		}
		return time.Time{}, fmt.Errorf("%w: value kind %q cannot bind a timestamp", ErrUnsupportedPostgresPlan, string(kind))
	default:
		return time.Time{}, fmt.Errorf("%w: recorded_at comparisons require a time.Time or full-precision temporal string bind value, got %T",
			ErrUnsupportedPostgresPlan, value)
	}
}

// renderPayloadComparison binds JSONB path comparisons. The whole document
// path is one bound parameter and the value is one bound JSONB parameter,
// so paths and values never become SQL text. MISSING/EXISTS semantics keep
// present-but-JSON-null and missing indistinguishable, matching the AQL
// null-or-missing reading; SQL NULL, JSON null, and missing remain distinct
// in storage (migration 000002) and in this renderer they are only ever
// bound, never flattened.
func (r *planRenderer) renderPayloadComparison(left, operator string, kind spec.FilterValueKind, value any) (predicateFragment, error) {
	if operator == "IN" {
		return predicateFragment{}, fmt.Errorf("%w: payload-path IN is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if err := validatePayloadBindType(kind, value); err != nil {
		return predicateFragment{}, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return predicateFragment{}, fmt.Errorf("%w: filter value %T is not JSON-encodable: %v", ErrUnsupportedPostgresPlan, value, err)
	}
	if string(raw) == "null" {
		// AQL reads a missing key and a stored JSON null alike as null, and
		// AQL sorts null below every other value. Rendering the presence
		// test for every operator would invert LT/LTE and over-select for
		// GTE, so the null literal maps operator by operator; the two pure
		// constants fail closed as dead-parameter shapes.
		presence := "COALESCE(jsonb_typeof(" + left + "), 'null')"
		switch operator {
		case "EQUALS", "LTE":
			return predicateFragment{sql: presence + " = 'null'"}, nil
		case "NOT_EQUALS", "GT":
			// GT coincides with the presence test: x > null holds exactly
			// when x is present and not null.
			return predicateFragment{sql: presence + " <> 'null'"}, nil
		case "LT", "GTE":
			// AQL constants these to false and true, but a bare constant
			// would strand the resolved path parameter, and the plan
			// contract requires every bound parameter to be referenced.
			// The v1 surface fails these closed instead.
			return predicateFragment{}, fmt.Errorf("%w: payload ordering %s against a JSON null literal is not part of the v1 PostgreSQL surface",
				ErrUnsupportedPostgresPlan, operator)
		default:
			return predicateFragment{}, fmt.Errorf("%w: payload operator %q against a JSON null literal is not part of the v1 PostgreSQL surface",
				ErrUnsupportedPostgresPlan, operator)
		}
	}
	if kind == spec.FilterDate || kind == spec.FilterDateTime {
		switch operator {
		case "GT", "GTE", "LT", "LTE":
			return r.renderTemporalPayloadComparison(left, operator, kind, value)
		}
	}
	if isNumericOrdering(operator, kind) {
		// PostgreSQL orders jsonb across types as Null, String, Number,
		// Boolean, Array, Object — the opposite cross-type position for
		// strings and numbers from ArangoDB's null < false < true <
		// numbers < strings < arrays < objects. A raw jsonb ordering
		// comparison would over-select under LTE/LT and under-select
		// under GTE/GT relative to AQL. Require the stored value to be a
		// JSON number; a non-numeric stored value (or a missing key)
		// reads never-comparable and is excluded, mirroring the temporal
		// guard. The result can still be SQL NULL (missing key), so NOT
		// needs the truth-table guard.
		index := r.jsonbParameter(raw)
		return predicateFragment{sql: "(jsonb_typeof((" + left + ")) = 'number' AND (" + left + ") " +
			sqlComparison(operator) + " ($" + strconv.Itoa(index) + "::jsonb))", mayYieldNull: true}, nil
	}
	index := r.jsonbParameter(raw)
	if operator == "NOT_EQUALS" {
		return predicateFragment{sql: "(" + left + ") IS DISTINCT FROM ($" + strconv.Itoa(index) + "::jsonb)"}, nil
	}
	// The path extraction is SQL NULL for a missing key, so these comparisons
	// are three-valued; NOT over them must be guarded for AQL parity.
	return predicateFragment{sql: "(" + left + ") " + sqlComparison(operator) + " ($" + strconv.Itoa(index) + "::jsonb)", mayYieldNull: true}, nil
}

// validatePayloadBindType pins the canonical Go type of each declared
// filter value kind — the exact types lower/filter_literal.go produces —
// so a bind that contradicts its declared kind is refused before SQL
// instead of being JSON-encoded into a different jsonb type. ValueKind ""
// keeps the pg-sql-02 accepted set.
func validatePayloadBindType(kind spec.FilterValueKind, value any) error {
	if kind == "" {
		switch value.(type) {
		case string, bool, int, int64, float64, json.RawMessage:
			return nil
		default:
			return fmt.Errorf("%w: payload comparisons accept string, bool, number, or raw JSON values, got %T",
				ErrUnsupportedPostgresPlan, value)
		}
	}
	switch kind {
	case spec.FilterString, spec.FilterCode, spec.FilterDate, spec.FilterDateTime:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%w: value kind %q requires a string bind value, got %T", ErrUnsupportedPostgresPlan, string(kind), value)
		}
		return nil
	case spec.FilterBoolean:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%w: value kind BOOLEAN requires a bool bind value, got %T", ErrUnsupportedPostgresPlan, value)
		}
		return nil
	case spec.FilterInteger:
		if _, ok := value.(int64); !ok {
			return fmt.Errorf("%w: value kind INTEGER requires an int64 bind value, got %T", ErrUnsupportedPostgresPlan, value)
		}
		return nil
	case spec.FilterDecimal:
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("%w: value kind DECIMAL requires a float64 bind value, got %T", ErrUnsupportedPostgresPlan, value)
		}
		return nil
	default:
		return fmt.Errorf("%w: value kind %q is not registered for PostgreSQL", ErrUnsupportedPostgresPlan, string(kind))
	}
}

// Temporal stored-value guards. A stored instant is compared as a real
// timestamptz; anything the guards reject reads never-comparable (SQL NULL
// → excluded) rather than raising or misordering. Full-precision stored
// dates and RFC3339 instants order identically to AQL's DATE_TIMESTAMP.
//
// Cast-safety is the load-bearing property: the THEN branches apply
// ::timestamptz, which raises "invalid input syntax" and aborts the whole
// statement on any string it cannot parse. Digit-shaped regexes are not
// enough — day validity depends on month and year — so the date core is an
// explicit alternation of cast-safe month/day branches: 31-day months,
// 30-day months, February through the 28th, and February 29 only under a
// proleptic-Gregorian leap-year test ((div-by-4 and not div-by-100) or
// div-by-400, century form written out because POSIX ERE has no
// lookahead). Time hours/minutes/seconds are range-bounded (leap seconds
// excluded, as before); fractional seconds are '.'-only and UTC offsets
// stop at ±15:59, because PostgreSQL raises SQLSTATE 22007 on a comma
// decimal ("10:20:30,5") and SQLSTATE 22009 ("time zone displacement out
// of range") on ±16:00 through ±23:59 — the patterns end exactly where the
// casts start raising. Year 0000 and any shape outside these branches are
// never-comparable, never cast; that also matches the AQL reading, where
// DATE_TIMESTAMP yields NaN and every NaN comparison is false. The live
// matrix and the boundary sweep store invalid-calendar, over-range offset,
// and comma-decimal rows (2024-02-30, 2024-13-01, 2024-01-01T25:70:99Z,
// +16:00, 10:20:30,5) to pin that the statement never raises and the rows
// are excluded.
const (
	// Two-digit pairs whose value is divisible by 4 and nonzero: 04/08
	// (even tens 0), [2468]x0/4/8 (even tens), x2/x6 with odd tens.
	temporalLeapTensOnesPattern = `(0[48]|[2468][048]|[13579][26])`
	// The date core is the cast-safe month/day alternation described
	// above; the leap-day branches are the century form (this pattern
	// followed by 00) and the non-century form (any two leading digits
	// followed by this pattern), so year 0000 is never cast.
	temporalStoredYearPattern      = `[1-9][0-9]{3}`
	temporalStoredDateCorePattern  = `([1-9][0-9]{3}-(0[13578]|1[02])-(0[1-9]|[12][0-9]|3[01])|` + temporalStoredYearPattern + `-(0[469]|11)-(0[1-9]|[12][0-9]|30)|` + temporalStoredYearPattern + `-02-(0[1-9]|1[0-9]|2[0-8])|` + temporalLeapTensOnesPattern + `00-02-29|[0-9]{2}` + temporalLeapTensOnesPattern + `-02-29)`
	temporalStoredTimePattern      = `(0[0-9]|1[0-9]|2[0-3]):[0-5][0-9](:[0-5][0-9]([.][0-9]+)?)?`
	temporalStoredOffsetPattern    = `(Z|[+-]((0[0-9]|1[0-4]):[0-5][0-9]|15:[0-5][0-9]))`
	temporalStoredDatePattern      = `^` + temporalStoredDateCorePattern + `$`
	temporalStoredInstantPattern   = `^` + temporalStoredDateCorePattern + `T` + temporalStoredTimePattern + temporalStoredOffsetPattern + `$`
	temporalStoredNoOffsetPattern  = `^` + temporalStoredDateCorePattern + `T` + temporalStoredTimePattern + `$`
	temporalStoredComparisonSuffix = `T00:00:00+00:00`
)

func temporalStoredExpression(extraction string) string {
	// The extracted text is parenthesized because :: binds tighter than
	// any operator: without the parens `'{}'::timestamptz` would parse as
	// a cast of the right operand of #>>.
	text := "((" + extraction + ") #>> '{}')"
	return "CASE" +
		" WHEN jsonb_typeof(" + extraction + ") = 'string' AND " + text + " ~ '" + temporalStoredDatePattern + "'" +
		" THEN (" + text + " || '" + temporalStoredComparisonSuffix + "')::timestamptz" +
		" WHEN jsonb_typeof(" + extraction + ") = 'string' AND " + text + " ~ '" + temporalStoredInstantPattern + "'" +
		" THEN " + text + "::timestamptz" +
		" WHEN jsonb_typeof(" + extraction + ") = 'string' AND " + text + " ~ '" + temporalStoredNoOffsetPattern + "'" +
		" THEN (" + text + " || '+00:00')::timestamptz" +
		" END"
}

// renderTemporalPayloadComparison binds an ordered DATE/DATE_TIME
// comparison over a document path. The right literal must parse as a
// full-precision instant (the spec admits only full-precision ordered
// literals); it becomes one timestamptz parameter, never text.
func (r *planRenderer) renderTemporalPayloadComparison(left, operator string, kind spec.FilterValueKind, value any) (predicateFragment, error) {
	timestamp, err := temporalComparisonValue(kind, value)
	if err != nil {
		return predicateFragment{}, err
	}
	index := r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterTimestamptz, Value: timestamp}, "")
	// The stored side is SQL NULL for missing keys and for never-comparable
	// stored strings, so the comparison is three-valued; NOT must be guarded.
	return predicateFragment{
		sql:          "(" + temporalStoredExpression(left) + ") " + sqlComparison(operator) + " $" + strconv.Itoa(index),
		mayYieldNull: true,
	}, nil
}

func sqlComparison(operator string) string {
	switch operator {
	case "EQUALS":
		return "="
	case "NOT_EQUALS":
		// Identical to != over the NOT NULL storage columns and never NULL
		// for nullable ones, mirroring the payload-path reading.
		return "IS DISTINCT FROM"
	case "GT":
		return ">"
	case "GTE":
		return ">="
	case "LT":
		return "<"
	case "LTE":
		return "<="
	default:
		return ""
	}
}

// ---------------------------------------------------------------------------
// Registered selector-expression comparison (pg-sql-05)
// ---------------------------------------------------------------------------

// renderSelectorComparisonFragment renders the closed selector-expression
// comparison shape described in this file's contract comment: one direct
// property path extracted from one readable row's document column, compared
// as the null-filtered 0..1-item multiset the AQL selector loop produces,
// under the predicate's ANY/NONE/ALL quantifier (empty means ANY, matching
// the AQL renderer's default).
func (r *planRenderer) renderSelectorComparisonFragment(plan ir.PhysicalPlan, predicate ir.PhysicalPredicate) (predicateFragment, error) {
	expression := predicate.LeftExpression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Cardinality != ir.PhysicalArrayCardinality {
		return predicateFragment{}, fmt.Errorf("%w: selector filter left side must be an ARRAY-cardinality EXTRACT expression",
			ErrUnsupportedPostgresPlan)
	}
	if expression.NullBehavior != ir.PhysicalEmptyOnNull {
		return predicateFragment{}, fmt.Errorf("%w: selector filter extraction null behavior %q has no registered comparison form",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	extract := expression.Extract
	if extract == nil || len(extract.Fallbacks) != 0 || extract.Prepared != nil ||
		extract.UnitNormalization != nil || extract.Distinct || extract.Selector.Filter != nil {
		return predicateFragment{}, fmt.Errorf("%w: selector filter extraction carries unregistered payload (fallbacks, prepared sets, unit normalization, DISTINCT, or selector filters are gaps)",
			ErrUnsupportedPostgresPlan)
	}
	switch extract.ExecutionMode {
	case ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar, "":
	default:
		return predicateFragment{}, fmt.Errorf("%w: selector filter execution mode %q has no registered comparison form",
			ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	source := extract.Source
	if source.BindKey != "" || source.Variable == "" || len(source.Path) != 1 || source.Path[0] != "payload" {
		return predicateFragment{}, fmt.Errorf("%w: selector filter source must be one readable row's document payload",
			ErrUnsupportedPostgresPlan)
	}
	scope, ok := r.rows[source.Variable]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: selector filter source variable %q does not bind a readable row",
			ErrUnsupportedPostgresPlan, source.Variable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != scope.resourceType {
		return predicateFragment{}, fmt.Errorf("%w: selector filter resource type %q contradicts the scanned row type %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
	}
	if predicate.Left.Variable != "" || len(predicate.Left.Path) != 0 {
		return predicateFragment{}, fmt.Errorf("%w: selector filter predicate cannot also declare an ordinary left value",
			ErrUnsupportedPostgresPlan)
	}
	if predicate.Right != nil && predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: selector filter right value must be bind-backed", ErrUnsupportedPostgresPlan)
	}
	if predicate.Right != nil && (predicate.Right.Variable != "" || len(predicate.Right.Path) != 0) {
		return predicateFragment{}, fmt.Errorf("%w: selector filter right value must be a bind, not a row path", ErrUnsupportedPostgresPlan)
	}

	path := make([]string, 0, len(extract.Selector.Steps))
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return predicateFragment{}, fmt.Errorf("%w: selector filter accepts only direct property paths; repeated, indexed, and filtered steps are gaps",
				ErrUnsupportedPostgresPlan)
		}
		path = append(path, step.Field)
	}
	if len(path) == 0 {
		return predicateFragment{}, fmt.Errorf("%w: selector filter selector must have at least one property", ErrUnsupportedPostgresPlan)
	}
	operator := strings.ToUpper(strings.TrimSpace(predicate.Operator))
	if err := validateDirectOperatorKind(operator, predicate.ValueKind); err != nil {
		return predicateFragment{}, err
	}

	extraction, err := r.documentPathExpression(scope.alias, path)
	if err != nil {
		return predicateFragment{}, err
	}
	presence := "COALESCE(jsonb_typeof(" + extraction + "), 'null') <> 'null'"

	switch operator {
	case "EXISTS":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: EXISTS must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: presence}, nil
	case "MISSING":
		if predicate.Right != nil {
			return predicateFragment{}, fmt.Errorf("%w: MISSING must not carry a right value", ErrUnsupportedPostgresPlan)
		}
		return predicateFragment{sql: "NOT (" + presence + ")"}, nil
	}
	if predicate.Right == nil || predicate.Right.BindKey == "" {
		return predicateFragment{}, fmt.Errorf("%w: %s requires a bind-backed right value", ErrUnsupportedPostgresPlan, operator)
	}
	if !predicate.ValueKind.Valid() {
		return predicateFragment{}, fmt.Errorf("%w: selector filter value kind %q is not registered",
			ErrUnsupportedPostgresPlan, string(predicate.ValueKind))
	}
	value, ok := plan.BindVars[predicate.Right.BindKey]
	if !ok {
		return predicateFragment{}, fmt.Errorf("%w: filter value bind %q is not defined", ErrUnsupportedPostgresPlan, predicate.Right.BindKey)
	}
	match, err := r.selectorMatchExpression(extraction, operator, predicate.ValueKind, value)
	if err != nil {
		return predicateFragment{}, err
	}
	// The 0..1-item multiset makes any_ the whole quantifier story: ANY is
	// present-and-matching, NONE its negation, and ALL additionally
	// requires the item to exist — all total, so NOT needs no guard.
	any_ := "(" + presence + " AND " + match + ")"
	switch predicate.Quantifier {
	case "", spec.QuantifierAny:
		return predicateFragment{sql: any_}, nil
	case spec.QuantifierNone:
		return predicateFragment{sql: "NOT (" + any_ + ")"}, nil
	case spec.QuantifierAll:
		return predicateFragment{sql: any_}, nil
	default:
		return predicateFragment{}, fmt.Errorf("%w: selector filter quantifier %q is not registered",
			ErrUnsupportedPostgresPlan, string(predicate.Quantifier))
	}
}

// selectorMatchExpression renders the per-item comparison the AQL loop's
// FILTER applies. The item is present (never SQL NULL and never the JSON
// null scalar) whenever this expression is evaluated under the AND with the
// presence proof, so jsonb comparisons are two-valued there; the temporal
// match is COALESCEd because its stored-side guard can still read NULL.
func (r *planRenderer) selectorMatchExpression(extraction, operator string, kind spec.FilterValueKind, value any) (string, error) {
	if operator == "CONTAINS_TEXT" {
		// Same registered gap as the direct form: AQL's TO_STRING reading
		// for non-string stored values has no registered equivalence.
		return "", fmt.Errorf("%w: selector CONTAINS_TEXT is not part of the registered PostgreSQL surface",
			ErrUnsupportedPostgresPlan)
	}
	if operator == "IN" {
		if err := validateSelectorInList(kind, value); err != nil {
			return "", err
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return "", fmt.Errorf("%w: selector IN values are not JSON-encodable: %v", ErrUnsupportedPostgresPlan, err)
		}
		if string(raw) == "null" {
			raw = []byte("[]")
		}
		index := r.jsonbParameter(raw)
		// EXISTS is total even when the bound list is empty, matching the
		// AQL reading of POSITION(...) == -1 for x IN [].
		return "EXISTS (SELECT 1 FROM jsonb_array_elements($" + strconv.Itoa(index) + "::jsonb) AS loom_selector_item WHERE loom_selector_item = (" + extraction + "))", nil
	}
	if err := validatePayloadBindType(kind, value); err != nil {
		return "", err
	}
	if kind == spec.FilterDate || kind == spec.FilterDateTime {
		switch operator {
		case "GT", "GTE", "LT", "LTE":
			timestamp, err := temporalComparisonValue(kind, value)
			if err != nil {
				return "", err
			}
			index := r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterTimestamptz, Value: timestamp}, "")
			// Never-comparable stored strings read NULL; under the presence
			// AND they mean "no item matched", the fail-closed AQL-parity
			// reading for values AQL would fold only with DATE_TIMESTAMP.
			return "COALESCE((" + temporalStoredExpression(extraction) + ") " + sqlComparison(operator) +
				" $" + strconv.Itoa(index) + ", false)", nil
		}
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("%w: filter value %T is not JSON-encodable: %v", ErrUnsupportedPostgresPlan, value, err)
	}
	if string(raw) == "null" {
		// The null-filtered AQL multiset never contains null, so a
		// null-literal right value constants every selector comparison and
		// would strand the path or value parameter. Fail closed.
		return "", fmt.Errorf("%w: selector comparison against a null literal is not part of the registered PostgreSQL surface",
			ErrUnsupportedPostgresPlan)
	}
	comparison := sqlComparison(operator)
	if operator == "NOT_EQUALS" {
		// The item is present, so plain <> is already total; IS DISTINCT
		// FROM would collapse nothing here but reads as three-valued logic
		// the fragment contract does not declare.
		comparison = "<>"
	}
	if isNumericOrdering(operator, kind) {
		// See renderPayloadComparison: jsonb cross-type order disagrees
		// with AQL where strings and numbers swap. Requiring a JSON
		// number makes a non-numeric stored item never-comparable; under
		// the presence conjunct the COALESCE keeps the quantifier
		// fragment total, so NOT needs no guard.
		return "COALESCE(jsonb_typeof(" + extraction + ") = 'number' AND (" + extraction + ") " +
			comparison + " ($" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb), false)", nil
	}
	return "(" + extraction + ") " + comparison + " ($" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb)", nil
}

func isNumericOrdering(operator string, kind spec.FilterValueKind) bool {
	switch operator {
	case "GT", "GTE", "LT", "LTE":
		return kind == spec.FilterInteger || kind == spec.FilterDecimal
	}
	return false
}

// validateSelectorInList pins each IN list element to the canonical Go
// type lower/filter_literal.go produces for the declared kind, so a bind
// whose element types contradict the kind is a typed refusal before any
// SQL text exists — the same boundary contract validatePayloadBindType
// enforces for scalar filter values. The []string shorthand is admitted
// only for the string-carrying kinds.
func validateSelectorInList(kind spec.FilterValueKind, value any) error {
	var elements []any
	switch list := value.(type) {
	case []string:
		if kind != spec.FilterString && kind != spec.FilterCode {
			return fmt.Errorf("%w: selector IN with a []string bind is admitted only for STRING and CODE kinds, got kind %q",
				ErrUnsupportedPostgresPlan, string(kind))
		}
		return nil
	case []any:
		elements = list
	default:
		return fmt.Errorf("%w: selector IN requires a []any or []string bind value, got %T", ErrUnsupportedPostgresPlan, value)
	}
	for _, element := range elements {
		var ok bool
		switch kind {
		case spec.FilterString, spec.FilterCode, spec.FilterDate, spec.FilterDateTime:
			_, ok = element.(string)
		case spec.FilterBoolean:
			_, ok = element.(bool)
		case spec.FilterInteger:
			_, ok = element.(int64)
		case spec.FilterDecimal:
			_, ok = element.(float64)
		default:
			return fmt.Errorf("%w: selector IN value kind %q is not registered", ErrUnsupportedPostgresPlan, string(kind))
		}
		if !ok {
			return fmt.Errorf("%w: selector IN element %T contradicts the declared value kind %q",
				ErrUnsupportedPostgresPlan, element, string(kind))
		}
	}
	return nil
}
