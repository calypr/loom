package postgres

// This file registers derived scalar expressions and typed calls as
// implemented, closed PostgreSQL operations (pg-sql-10). It is the
// renderer-facing capability registration point for the operation named by
// the physical IR's PhysicalCallExpression (ir/physical_values.go:110-117:
// a backend-neutral expression operator whose TargetKind names a logical or
// table scalar type, never a backend type or query fragment). Every CALL
// projection the RETURN clause renders is built here; shape validation — not
// assembly — lives here plus the single projection contract in
// selectors.go, and render.go remains the package's single statement
// builder. No second SQL builder exists: this file emits jsonb-valued
// expression fragments and binds their parameters; render.go composes them
// into the SELECT list.
//
// AQL parity anchors. The AQL producer of every shape registered here is
// the CALL case of render/aql/values.go renderExpression (values.go:57-58)
// dispatching into render/aql/calls.go renderCall (calls.go:10-229); the
// plan producers are the compiler lowerings that build ir.PhysicalCall
// nodes: lower/recipe_expression_lowering.go:422-444,
// lower/recipe_expression.go:96-121, lower/recipe_derived_columns.go:280-324
// (arithmetic plus the case{eq,or,assert} MissingInput/DivisionByZero
// wrappers), lower/recipe_column_transformations.go:60-94 (exact-category
// recode), lower/recipe_construction.go:1787-1814 (the distinct_present_one
// / length / gt forms — whose SUBPLAN arguments stay unregistered and
// fail-closed in this wave (pg-sql-08's ratified plan refuses nested
// subplan scans/SetOps too, so no sibling owns them; registration is a
// coordinator decision), lower/recipe_construction_related_eligibility.
// go:226, and lower/recipe_dynamic_columns.go:260. Every producer of the
// comparison-operator shape is cited, not just the dispatching one:
// ==/!=/>/>=/</<= reach ArangoDB from renderCall's eq case group
// (render/aql/calls.go:209-213), the direct physical filters
// EQUALS/NOT_EQUALS/GT/GTE/LT/LTE (render/aql/predicates.go:38-52), the
// correlated value-filter arm (predicates.go:238-250), the group-row value
// match (render/aql/group_rows.go:195), and the row-identity/key matching
// FILTERs (row_lineage.go:92,128,163,310,338,479,579,597,
// row_lineage_coded_group.go:39, row_lineage_related_coding.go:156,185 —
// compiler-minted identifiers only, same operators, same doc governance).
// Every registered
// conversion is decided against the official ArangoDB documentation source
// (github.com/arangodb/docs, 3.9/aql manual set): operators.md:259-277
// (invalid arithmetic such as 1/0 yields null), operators.md:160-183
// (logical operators return operands under the falsy set
// {null,false,0,""}; ! returns the negated boolean), functions-type-cast.md
// 24-107 (TO_BOOL/TO_NUMBER/TO_STRING tables), fundamentals-type-value-order
// .md (type order null < bool < number < string < array < object; null is
// comparable to null without SQL null-propagation), and
// functions-miscellaneous.md 504-517 (ASSERT returns true, else raises with
// the message). The falsy reading is corroborated in-repo by the live-
// measured falsy arms in unnest.go, and the jsonb cross-type order that
// makes the registered rank comparator load-bearing is the one predicates.go
// measured live (Null, String, Number, Boolean, Array, Object — the
// swap against ArangoDB's order for strings and numbers).
//
// Registered surface (scalar RETURN projections only; the selector-extraction
// slice keeps refusing CALL projections in selectors.go):
//
//	add, subtract, multiply, divide       ArangoDB stores IEEE-754 doubles
//	                                      and its double arithmetic is
//	                                      hardware IEEE (fundamentals-
//	                                      data-types.md 72-73), so every
//	                                      result is computed by a real
//	                                      double-precision + - * / — the
//	                                      only domain where PG18 rounds
//	                                      like the AQL engine (numeric-
//	                                      domain emulation misrounds:
//	                                      float8->numeric is a shortest-
//	                                      decimal conversion and numeric
//	                                      division truncates). Because
//	                                      PG18 raises 22003 at the IEEE
//	                                      overflow/underflow boundaries,
//	                                      each float8 operation runs only
//	                                      inside an arm whose magnitude
//	                                      was pre-proven safe by an exact
//	                                      numeric-domain band test (never
//	                                      raising itself); the pass bands
//	                                      admit every round-tripping
//	                                      double text, and the boundary
//	                                      gray bands read registered
//	                                      conservative values (L20).
//	                                      Invalid values (null, missing,
//	                                      non-numeric except decimal-form
//	                                      strings, which coerce) yield SQL
//	                                      NULL, mirroring the documented
//	                                      "1/0 → null" non-raising policy —
//	                                      PostgreSQL division by zero
//	                                      (SQLSTATE 22012) is unreachable
//	                                      because the divisor is
//	                                      NULLIF-guarded; IEEE overflow
//	                                      (Infinity, which jsonb cannot
//	                                      hold, and which operators.md
//	                                      259-262 reads back as invalid →
//	                                      null) reads NULL and IEEE
//	                                      underflow reads 0.
//	cast string|code|uuid                 TO_STRING table: null/missing →
//	                                      "", booleans → "true"/"false",
//	                                      numbers → their spelling, strings
//	                                      identity; arrays/objects read
//	                                      NULL (the no-extra-whitespace JSON
//	                                      conversion is unregistered).
//	cast integer|decimal                  TO_NUMBER table: null/false → 0,
//	                                      true → 1, numbers identity,
//	                                      decimal-form strings (whitespace
//	                                      allowed) parse, other strings →
//	                                      0, [] → 0, [x] → TO_NUMBER(x)
//	                                      (bounded recursion, deeper
//	                                      nesting reads 0), two-or-more
//	                                      arrays and objects → 0; a parsed
//	                                      double that is not finite reads
//	                                      NULL (jsonb cannot hold it).
//	cast boolean                          TO_BOOL table: null → false,
//	                                      0 → false, other numbers → true,
//	                                      "" → false, non-empty strings →
//	                                      true, arrays/objects → true.
//	cast date|date_time                   unregistered gap: AQL DATE_ISO8601
//	                                      normalization has no registered
//	                                      PostgreSQL equivalence yet.
//	coalesce, fallback                    FIRST(FOR v IN [..] FILTER v !=
//	                                      null RETURN ...) of
//	                                      render/aql/calls.go:61-65 becomes
//	                                      COALESCE(NULLIF(..,'null'::jsonb))
//	                                      per argument: JSON null and
//	                                      missing drop, all-null yields
//	                                      SQL NULL (AQL's FIRST of []).
//	if, case                              conditions are evaluated under
//	                                      the documented falsy set; a
//	                                      missing case else is the SQL NULL
//	                                      keyword (no stranded parameter —
//	                                      the AQL scalarNull bind of
//	                                      render/aql/calls.go:41-45 has no
//	                                      PostgreSQL parameter form).
//	and, or                               the documented value-returning
//	                                      semantics (lhs falsy → lhs,
//	                                      else rhs) under a lazily
//	                                      evaluated CASE, which reproduces
//	                                      AQL short-circuit evaluation.
//	not                                   ! semantics: NOT over the
//	                                      two-valued truthiness test.
//	eq, neq                               total AQL equality: SQL NULL and
//	                                      missing normalize to the jsonb
//	                                      'null' scalar first (AQL null is
//	                                      comparable to null without
//	                                      null-propagation), then jsonb =
//	                                      decides. Both engines compare
//	                                      strictly by type: AQL comparisons
//	                                      "do not perform implicit type
//	                                      casts if the compared operands
//	                                      have different types, i.e. they
//	                                      test for strict equality"
//	                                      (operators.md 39-43; documented
//	                                      examples 0 == null false, 65 !=
//	                                      "65" true), so number vs numeric
//	                                      string, false vs 0 and "" vs 0
//	                                      read false under == and under
//	                                      jsonb = alike — no coercion is
//	                                      registered because none exists
//	                                      to reproduce — and jsonb numeric
//	                                      equality treats 1 = 1.0 exactly
//	                                      like AQL. Array/object pairs
//	                                      carry one registered divergence:
//	                                      AQL's null-padded traversal reads
//	                                      [1] == [1, null] and { } ==
//	                                      { "a": null } true (fundamentals-
//	                                      type-value-order.md 97-136)
//	                                      while jsonb = reads them false
//	                                      (L19, pinned live).
//	gt, gte, lt, lte                      the documented type order
//	                                      null < bool < number < string
//	                                      ranks first — AQL compares data
//	                                      types before values and converts
//	                                      nothing across them (fundamentals-
//	                                      type-value-order.md 15-18; e.g.
//	                                      45 <= "yikes!" is true because
//	                                      number ranks below string,
//	                                      operators.md 47) — values then
//	                                      compare only within a rank, so
//	                                      numeric-form strings never rank
//	                                      with numbers; PostgreSQL's jsonb
//	                                      cross-type order (predicates.go:
//	                                      122-124) would misorder strings
//	                                      against numbers; array/object
//	                                      operands read NULL (the
//	                                      element-wise comparator is a
//	                                      follow-up, L15), and comparisons
//	                                      never raise.
//	assert                                ASSERT(cond, message): true
//	                                      returns the jsonb 'true' scalar;
//	                                      falsy raises SQLSTATE 22P02
//	                                      carrying the bound message,
//	                                      behind the population.go
//	                                      constant-folding barrier — a bare
//	                                      dead-arm CAST folds and raises at
//	                                      plan time there (measured live),
//	                                      so every raising arm here keeps
//	                                      the same current_setting barrier.
//	distinct_present_one                  render/aql/calls.go:84-99: FLATTEN
//	                                      promotes exactly one nesting
//	                                      level (reproduced), JSON-null
//	                                      items drop first, every remaining
//	                                      item must carry the target
//	                                      scalar's jsonb type or the query
//	                                      raises CONSTRUCTION_ROW_VALUE_
//	                                      TYPE_MISMATCH, more than one
//	                                      DISTINCT value raises
//	                                      CONSTRUCTION_ROW_VALUE_MULTIPLE_
//	                                      VALUES, zero values read NULL,
//	                                      one value reads that value
//	                                      (MIN over the asserted same-type
//	                                      items equals FIRST(SORTED_UNIQUE)).
//
// Everything else — first, all, distinct, length, coalesce_string, concat,
// join, contains, canonical_json, reference_id, path_segment, basename,
// last_segment, sanitize_name, sanitize_graphql_name, uuid3, uuid5 (the AQL
// post-query marker forms), unregistered TargetKinds, ARRAY/OBJECT-cardinality
// CALL expressions, subplan/aggregate/pivot/object operands, typed-column
// operands, unregistered literal Go types, presence metadata on a CALL
// projection, hidden CALL projections, and any arity/TargetKind violation
// ir/physical_validation_expr.go:264-310 also rejects — is a capability gap:
// a typed ErrUnsupportedPostgresPlan refusal that yields no SQL, naming the
// boundary it enforces. Unknown call/type pairs are refused BEFORE execution:
// literal bind types and TargetKind matrices are checked while the fragment
// is assembled, so an unregistered pair never reaches the driver. There is
// no fallback to the AQL renderer and no AQL-to-SQL conversion here.
//
// Planner-facing command registration (the selected compiler target) stays
// fail-closed for PostgreSQL exactly as in pg-sql-01/02/03/05/12/18: this
// file — not command dispatch — is where these expression operations are
// implemented and registered.
//
// Parameter discipline never changes: every value is one ordered $n
// parameter (jsonb for literals; the message of assert is a text parameter
// embedded inside the barrier-cast concatenation, never SQL-interpolated),
// document paths bind through documentPathExpression (null_semantics.go
// remains the package's single path-binding authority), renderer-owned
// constants (rank integers, typeof literals, the decimal string grammar,
// guard tags) are structural text fixed by this file, and no plan string
// ever becomes SQL text.

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// maxCallExpressionDepth bounds the recursive walk of one hand-built CALL
// tree. Lowering emits shallow trees (the derived-column wrappers nest at
// most three levels); the cap keeps a hostile plan from unbounded recursion
// while every lowering shape stays far inside it.
//
// The depth cap alone does NOT bound emitted SQL size: the registered
// fragments replicate their operands (callNumericToJSONB repeats the
// numeric expression four times, callTruthyTest and callNumericCoercion
// five to seven), so each nesting level multiplies the fragment and a
// left-nested chain near the depth cap would expand beyond any memory
// before the depth guard fires. maxCallFragmentBytes therefore bounds the
// assembled fragment itself — a typed refusal when replication outgrows
// any sane plan, checked at both fragment choke points below so expansion
// is refused within a constant factor of the bound.
const maxCallExpressionDepth = 64

// maxCallFragmentBytes bounds one assembled CALL fragment: registered
// lowering nests at most three levels (fragment sizes in the low kilobytes
// even for wide argument lists), so the bound is orders of magnitude above
// every legitimate plan and far below anything a replication chain can
// force.
const maxCallFragmentBytes = 1 << 20

// checkCallFragment enforces the registered fragment budget.
func checkCallFragment(fragment string) error {
	if len(fragment) > maxCallFragmentBytes {
		return fmt.Errorf("%w: call fragment of %d bytes exceeds the %d-byte registered bound (operand replication under nesting)",
			ErrUnsupportedPostgresPlan, len(fragment), maxCallFragmentBytes)
	}
	return nil
}

// registeredCallNames is the closed v1 PostgreSQL call surface. It is a
// strict subset of the ir-validate vocabulary
// (ir/physical_validation_expr.go:272-282): admitted names outside it stay
// typed refusals naming the registered gap.
var registeredCallNames = map[string]bool{
	"add": true, "subtract": true, "multiply": true, "divide": true,
	"cast": true, "coalesce": true, "fallback": true,
	"if": true, "case": true, "not": true, "and": true, "or": true,
	"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"assert": true, "distinct_present_one": true,
}

// callLiteralKind is the static type of one bind-backed literal operand,
// derived from its canonical Go type (the same canonical-type discipline
// validatePayloadBindType pins for filter values). Only int64, float64,
// string, bool, nil, and a json.RawMessage holding a JSON array are literal
// operands at all; anything else is an unknown call/type pair refused
// before execution.
type callLiteralKind uint8

const (
	callLiteralNot     callLiteralKind = iota // operand is not a literal
	callLiteralNull                           // JSON null scalar (Go nil)
	callLiteralNumber                         // int64 or float64
	callLiteralString                         // string
	callLiteralBoolean                        // bool
	callLiteralArray                          // json.RawMessage holding a JSON array
)

// callOperand is one validated operand fragment. sql is always a
// jsonb-valued expression; kind names the static type for literal operands
// and callLiteralNot for dynamic ones (EXTRACT/VALUE/nested calls, whose
// stored type is decided at execution through the registered guards).
type callOperand struct {
	sql  string
	kind callLiteralKind
}

// callResultFacts reports the operand's provable stored kind. A literal
// reports its bind type. add/subtract/multiply/divide and the registered
// casts guarantee their result kind and report it through
// renderCallExpression's result facts; everything else is dynamic.
type callResultFacts struct {
	// staticKind is callLiteralNot unless the fragment provably yields one
	// stored jsonb kind (arithmetic → number, cast → its target class,
	// comparisons/not → boolean).
	staticKind callLiteralKind
}

// renderCallProjection renders one classified CALL RETURN projection onto
// its positional SELECT entry. Classification (selectors.go) already pinned
// the projection shape; this is the plan-aware deep walk that needs bind
// values and row bindings. The returned fragment is parenthesized so the
// assembly in render.go can place it verbatim.
func (r *planRenderer) renderCallProjection(plan ir.PhysicalPlan, projection ir.PhysicalProjection) (string, error) {
	fragment, _, err := r.renderCallExpression(plan, *projection.Expression, 0)
	if err != nil {
		return "", fmt.Errorf("projection %q: %w", projection.Name, err)
	}
	if err := checkCallFragment(fragment); err != nil {
		return "", fmt.Errorf("projection %q: %w", projection.Name, err)
	}
	return "(" + fragment + ")", nil
}

// validateCallProjection is the plan-independent half of the CALL contract,
// run by the selectors.go classifier: presence posture, SCALAR/PRESERVE_NULL
// posture, registered name, arity, and the TargetKind matrix. It never
// reads bind values or row bindings — those belong to the render walk.
func validateCallProjection(projection ir.PhysicalProjection) error {
	expression := projection.Expression
	if expression == nil {
		return fmt.Errorf("%w: projection %q: CALL expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	call := expression.Call
	if call == nil {
		return fmt.Errorf("%w: projection %q: CALL expression is missing its payload",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if expression.Cardinality != ir.PhysicalScalarCardinality || expression.NullBehavior != ir.PhysicalPreserveNull {
		return fmt.Errorf("%w: projection %q: CALL is registered for SCALAR-cardinality PRESERVE_NULL expressions; array and object call projections belong to a later capability",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	if projection.Presence != nil {
		return fmt.Errorf("%w: projection %q carries presence metadata on a CALL expression; presence proofs pair with selector extractions only",
			ErrUnsupportedPostgresPlan, projection.Name)
	}
	return validateCallShape(projection.Name, call, 0)
}

// validateCallShape re-reads the closed per-name arity and TargetKind
// contract, mirroring ir/physical_validation_expr.go:286-306 (defense in
// depth: ir.Validate pins the vocabulary upstream; the renderer never
// trusts it).
func validateCallShape(projectionName string, call *ir.PhysicalCall, depth int) error {
	if depth > maxCallExpressionDepth {
		return fmt.Errorf("%w: projection %q: call nesting exceeds the %d-level registered bound",
			ErrUnsupportedPostgresPlan, projectionName, maxCallExpressionDepth)
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	if !registeredCallNames[name] {
		return fmt.Errorf("%w: call %q is not part of the v1 PostgreSQL registered call surface (unregistered: array reducers, string pipelines, path utilities, uuid markers, and subplan-valued construction calls belong to later capabilities)",
			ErrUnsupportedPostgresPlan, call.Name)
	}
	switch name {
	case "add", "subtract", "multiply", "divide", "eq", "neq", "gt", "gte", "lt", "lte":
		if len(call.Args) != 2 {
			return fmt.Errorf("%w: call %q requires 2 arguments, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "not":
		if len(call.Args) != 1 {
			return fmt.Errorf("%w: call %q requires 1 argument, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "and", "or":
		if len(call.Args) < 2 {
			return fmt.Errorf("%w: call %q requires at least 2 arguments, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "if":
		if len(call.Args) != 3 {
			return fmt.Errorf("%w: call %q requires 3 arguments, got %d", ErrUnsupportedPostgresPlan, name, len(call.Args))
		}
	case "case":
		// render/aql/calls.go:177-194: an odd argument count carries the
		// trailing else, an even count has none; both shapes are legal.
		if len(call.Args) < 2 {
			return fmt.Errorf("%w: call %q requires at least one condition/result pair", ErrUnsupportedPostgresPlan, name)
		}
	case "coalesce", "fallback":
		if len(call.Args) == 0 {
			return fmt.Errorf("%w: call %q requires at least one argument", ErrUnsupportedPostgresPlan, name)
		}
	case "assert":
		if len(call.Args) != 2 {
			return fmt.Errorf("%w: call %q requires a condition and a message", ErrUnsupportedPostgresPlan, name)
		}
	case "cast":
		if len(call.Args) != 1 {
			return fmt.Errorf("%w: cast requires 1 argument, got %d", ErrUnsupportedPostgresPlan, len(call.Args))
		}
		if !validCallCastTarget(call.TargetKind) {
			return fmt.Errorf("%w: cast target kind %q is not registered for PostgreSQL (date/date_time coercions are gaps)",
				ErrUnsupportedPostgresPlan, call.TargetKind)
		}
	case "distinct_present_one":
		if len(call.Args) != 1 ||
			(call.TargetKind != "STRING" && call.TargetKind != "INTEGER" && call.TargetKind != "DECIMAL" && call.TargetKind != "BOOLEAN") {
			return fmt.Errorf("%w: distinct_present_one requires one array argument and a STRING/INTEGER/DECIMAL/BOOLEAN target kind (the aqlTableScalarType domain, render/aql/table_reshape.go:693-704)",
				ErrUnsupportedPostgresPlan)
		}
	default:
		return fmt.Errorf("%w: call %q is not part of the v1 PostgreSQL registered call surface",
			ErrUnsupportedPostgresPlan, call.Name)
	}
	// TargetKind is payload only for cast and distinct_present_one, exactly
	// the ir/physical_validation_expr.go:304-306 rule re-read here.
	if name != "cast" && name != "distinct_present_one" && strings.TrimSpace(call.TargetKind) != "" {
		return fmt.Errorf("%w: call %q must not carry target kind %q (only cast and distinct_present_one name a target kind)",
			ErrUnsupportedPostgresPlan, name, call.TargetKind)
	}
	for index := range call.Args {
		arg := &call.Args[index]
		if arg.Call != nil && arg.Kind == ir.PhysicalCallExpression {
			if err := validateCallShape(projectionName, arg.Call, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// validCallCastTarget is the registered cast matrix. ir.Validate admits the
// same domain plus date/date_time; those two keep a typed refusal here.
func validCallCastTarget(target string) bool {
	switch strings.ToLower(strings.TrimSpace(target)) {
	case "string", "code", "uuid", "integer", "decimal", "boolean":
		return true
	default:
		return false
	}
}

// forEachCallOperandRowVariable visits every row variable one CALL
// expression's operands read: each VALUE operand's variable (extract is
// nil) and each EXTRACT operand's source variable (with its payload for the
// resource-type cross-check), descending into nested CALL operands. The
// selectors.go classifier uses it to prove every operand binds a readable
// row before assembly; unknown operand kinds are left to the render-walk
// refusal so the classifier stays a row-visibility check, not a second
// operand contract.
func forEachCallOperandRowVariable(expression ir.PhysicalExpression, visit func(variable string, extract *ir.PhysicalExtract) error) error {
	call := expression.Call
	if call == nil {
		return fmt.Errorf("%w: CALL expression is missing its payload", ErrUnsupportedPostgresPlan)
	}
	for index := range call.Args {
		arg := &call.Args[index]
		switch arg.Kind {
		case ir.PhysicalValueExpression:
			if arg.Value == nil {
				return fmt.Errorf("%w: VALUE call operand is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := visit(arg.Value.Variable, nil); err != nil {
				return fmt.Errorf("argument %d: %w", index, err)
			}
		case ir.PhysicalExtractExpression:
			if arg.Extract == nil {
				return fmt.Errorf("%w: EXTRACT call operand is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := visit(arg.Extract.Source.Variable, arg.Extract); err != nil {
				return fmt.Errorf("argument %d: %w", index, err)
			}
		case ir.PhysicalCallExpression:
			if err := forEachCallOperandRowVariable(*arg, visit); err != nil {
				return fmt.Errorf("argument %d: %w", index, err)
			}
		}
	}
	return nil
}

// renderCallExpression renders one registered CALL subtree to a
// jsonb-valued fragment and reports its result facts. Every literal bind
// type and TargetKind pair is checked here, before any SQL exists.
func (r *planRenderer) renderCallExpression(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (string, callResultFacts, error) {
	if depth > maxCallExpressionDepth {
		return "", callResultFacts{}, fmt.Errorf("%w: call nesting exceeds the %d-level registered bound",
			ErrUnsupportedPostgresPlan, maxCallExpressionDepth)
	}
	if expression.Kind != ir.PhysicalCallExpression || expression.Call == nil {
		return "", callResultFacts{}, fmt.Errorf("%w: CALL expression payload is missing", ErrUnsupportedPostgresPlan)
	}
	call := expression.Call
	if expression.Cardinality != ir.PhysicalScalarCardinality || expression.NullBehavior != ir.PhysicalPreserveNull {
		return "", callResultFacts{}, fmt.Errorf("%w: CALL is registered for SCALAR-cardinality PRESERVE_NULL expressions",
			ErrUnsupportedPostgresPlan)
	}
	// Defense in depth: the classifier ran this shape walk over the whole
	// tree already; the render walk re-reads it node by node so no render
	// path can reach a fragment builder with an unvalidated shape.
	if err := validateCallShape("", call, depth); err != nil {
		return "", callResultFacts{}, err
	}
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "add", "subtract", "multiply", "divide":
		sql, err := r.renderCallArithmetic(plan, name, call, depth)
		return sql, callResultFacts{staticKind: callLiteralNumber}, err
	case "cast":
		sql, kind, err := r.renderCallCast(plan, call, depth)
		return sql, callResultFacts{staticKind: kind}, err
	case "coalesce", "fallback":
		sql, err := r.renderCallCoalesce(plan, call, depth)
		return sql, callResultFacts{}, err
	case "if":
		sql, err := r.renderCallIf(plan, call, depth)
		return sql, callResultFacts{}, err
	case "case":
		sql, err := r.renderCallCase(plan, call, depth)
		return sql, callResultFacts{}, err
	case "and", "or":
		sql, err := r.renderCallLogical(plan, name, call, depth)
		return sql, callResultFacts{}, err
	case "not":
		operand, err := r.renderCallOperand(plan, call.Args[0], depth)
		if err != nil {
			return "", callResultFacts{}, err
		}
		return "to_jsonb(NOT " + callTruthyTest(operand.sql) + ")", callResultFacts{staticKind: callLiteralBoolean}, nil
	case "eq", "neq":
		// Strict-type equality on both sides of the parity line: AQL's
		// non-strict operator name conceals a type-first comparison that
		// "do[es] not perform implicit type casts" across types
		// (operators.md 39-43), so jsonb = reproduces it without coercion
		// arms; the null-padding array/object divergence is L19 (header
		// table cell + live pin).
		left, right, err := r.renderCallPair(plan, call, depth)
		if err != nil {
			return "", callResultFacts{}, err
		}
		equal := "COALESCE(" + left.sql + ", 'null'::jsonb) = COALESCE(" + right.sql + ", 'null'::jsonb)"
		if name == "neq" {
			equal = "NOT (" + equal + ")"
		}
		return "to_jsonb(" + equal + ")", callResultFacts{staticKind: callLiteralBoolean}, nil
	case "gt", "gte", "lt", "lte":
		// The comparator below is the registered type-first ranking; AQL
		// converts nothing across type ranks in ordering either
		// (fundamentals-type-value-order.md 15-18, operators.md 47), so
		// numeric-form strings rank as strings, exactly as ArangoDB reads
		// them.
		left, right, err := r.renderCallPair(plan, call, depth)
		if err != nil {
			return "", callResultFacts{}, err
		}
		return "to_jsonb(" + callOrderTest(left.sql, sqlCallComparison(name), right.sql) + ")",
			callResultFacts{staticKind: callLiteralBoolean}, nil
	case "assert":
		sql, err := r.renderCallAssert(plan, call, depth)
		return sql, callResultFacts{staticKind: callLiteralBoolean}, err
	case "distinct_present_one":
		sql, err := r.renderCallDistinctPresentOne(plan, call, depth)
		return sql, callResultFacts{}, err
	default:
		return "", callResultFacts{}, fmt.Errorf("%w: call %q is not part of the v1 PostgreSQL registered call surface",
			ErrUnsupportedPostgresPlan, call.Name)
	}
}

// ---------------------------------------------------------------------------
// Operands
// ---------------------------------------------------------------------------

// renderCallOperand renders one operand to a jsonb-valued fragment through
// the registered operand classes. Literal bind types are pinned here; an
// unregistered Go type is an unknown call/type pair refused before SQL.
func (r *planRenderer) renderCallOperand(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (callOperand, error) {
	switch expression.Kind {
	case ir.PhysicalLiteralExpression:
		return r.renderCallLiteral(plan, expression)
	case ir.PhysicalExtractExpression:
		return r.renderCallExtractOperand(expression)
	case ir.PhysicalValueExpression:
		return r.renderCallValueOperand(expression)
	case ir.PhysicalCallExpression:
		sql, _, err := r.renderCallExpression(plan, expression, depth+1)
		if err != nil {
			return callOperand{}, err
		}
		sql = "(" + sql + ")"
		// The budget is enforced at every operand BEFORE the parent
		// replicates it: bottom-up refusal keeps the largest ever-allocated
		// fragment a constant factor of the bound (a root-only check would
		// build the full exponential expansion first — measured: the
		// process OOMs at 40 add levels).
		if err := checkCallFragment(sql); err != nil {
			return callOperand{}, err
		}
		return callOperand{sql: sql}, nil
	default:
		return callOperand{}, fmt.Errorf("%w: call operand kind %q has no registered PostgreSQL rendering (subplan, aggregate, pivot, object, and related-field operands are capability gaps)",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
}

// renderCallLiteral binds one canonical-typed plan bind as a jsonb
// parameter. The canonical Go bind types are exactly the set §D2 registers:
// int64, float64, string, bool, nil (the JSON null scalar, bound as
// json.RawMessage("null") — never a SQL-null parameter, per
// command/rendered_postgres.go's JSON-null rule), and a json.RawMessage
// that unmarshals to a JSON array (the distinct_present_one collection
// only). Any other Go type or JSON shape is the unknown call/type pair,
// refused before any SQL exists.
func (r *planRenderer) renderCallLiteral(plan ir.PhysicalPlan, expression ir.PhysicalExpression) (callOperand, error) {
	if expression.Literal == nil || expression.Literal.BindKey == "" {
		return callOperand{}, fmt.Errorf("%w: LITERAL call operand is missing its bind key", ErrUnsupportedPostgresPlan)
	}
	value, ok := plan.BindVars[expression.Literal.BindKey]
	if !ok {
		return callOperand{}, fmt.Errorf("%w: call literal bind %q is not defined", ErrUnsupportedPostgresPlan, expression.Literal.BindKey)
	}
	switch typed := value.(type) {
	case nil:
		// jsonb bind parameters ride the wire untyped (the predicates.go
		// precedent annotates them), so every reference carries ::jsonb:
		// an unannotated parameter is "unknown" to the parser and makes
		// jsonb operators like #>> uniquely-unresolvable (42725).
		return callOperand{sql: "$" + strconv.Itoa(r.jsonbParameter(json.RawMessage("null"))) + "::jsonb", kind: callLiteralNull}, nil
	case bool:
		return r.bindCallNumberOrScalar(typed, callLiteralBoolean)
	case int64:
		return r.bindCallNumberOrScalar(typed, callLiteralNumber)
	case float64:
		return r.bindCallNumberOrScalar(typed, callLiteralNumber)
	case string:
		return r.bindCallNumberOrScalar(typed, callLiteralString)
	case json.RawMessage:
		var probe any
		if err := json.Unmarshal(typed, &probe); err != nil {
			return callOperand{}, fmt.Errorf("%w: call literal bind %q is not valid JSON: %v",
				ErrUnsupportedPostgresPlan, expression.Literal.BindKey, err)
		}
		if _, isArray := probe.([]any); !isArray {
			return callOperand{}, fmt.Errorf("%w: call literal bind %q has stored kind %s; only a JSON array binds raw (the distinct_present_one collection form) — the unknown call/type pair gate",
				ErrUnsupportedPostgresPlan, expression.Literal.BindKey, jsonProbeKind(probe))
		}
		raw := append(json.RawMessage(nil), typed...)
		return callOperand{sql: "$" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb", kind: callLiteralArray}, nil
	default:
		return callOperand{}, fmt.Errorf("%w: call literal bind %q has Go type %T; the registered literal bind types are int64, float64, string, bool, nil, and a json.RawMessage array — the unknown call/type pair gate",
			ErrUnsupportedPostgresPlan, expression.Literal.BindKey, value)
	}
}

// bindCallNumberOrScalar JSON-encodes one canonical scalar bind and binds
// it as one jsonb parameter. A float64 NaN or Infinity has no JSON
// representation (json.Marshal fails) and no jsonb representation, so it
// fails closed here, before execution.
func (r *planRenderer) bindCallNumberOrScalar(value any, kind callLiteralKind) (callOperand, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return callOperand{}, fmt.Errorf("%w: call literal bind value %T is not JSON-encodable: %v",
			ErrUnsupportedPostgresPlan, value, err)
	}
	return callOperand{sql: "$" + strconv.Itoa(r.jsonbParameter(raw)) + "::jsonb", kind: kind}, nil
}

// jsonProbeKind names a decoded JSON shape for the refusal text.
func jsonProbeKind(probe any) string {
	switch probe.(type) {
	case nil:
		return "JSON null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case map[string]any:
		return "object"
	}
	return "unknown"
}

// renderCallExtractOperand renders the closed scalar-selector operand form
// through the selectors.go contract and null_semantics.go emitters. The
// operand must read a stored row (a derived LET row has no payload column
// to extract through) whose scanned type agrees with the selector's schema
// provenance — the same identity rule classifyExtractionProjections applies
// to selector projections.
func (r *planRenderer) renderCallExtractOperand(expression ir.PhysicalExpression) (callOperand, error) {
	if expression.Cardinality != ir.PhysicalScalarCardinality {
		return callOperand{}, fmt.Errorf("%w: CALL scalar operands accept SCALAR-cardinality EXTRACT expressions only",
			ErrUnsupportedPostgresPlan)
	}
	path, err := validateSelectorExtraction(ir.PhysicalProjection{Name: "call operand", Expression: &expression})
	if err != nil {
		return callOperand{}, err
	}
	scope, ok := r.rows[expression.Extract.Source.Variable]
	if !ok || scope.alias == "" || scope.derivedValue != "" || scope.derivedJSON {
		return callOperand{}, fmt.Errorf("%w: call operand extracts from variable %q, which does not bind a readable stored row",
			ErrUnsupportedPostgresPlan, expression.Extract.Source.Variable)
	}
	if expression.Extract.ResourceType != scope.resourceType {
		return callOperand{}, fmt.Errorf("%w: call operand validates selector against resource type %q, but the row is scanned as %q",
			ErrUnsupportedPostgresPlan, expression.Extract.ResourceType, scope.resourceType)
	}
	sql, err := r.scalarExtractValueExpression(scope.alias, path, expression.NullBehavior)
	if err != nil {
		return callOperand{}, err
	}
	return callOperand{sql: sql}, nil
}

// renderCallValueOperand renders a payload-path VALUE (stored rows through
// resolveValueInRows, derived JSON rows through resolveDerivedValue).
// Typed-column paths are refused: storage columns are not call operands.
func (r *planRenderer) renderCallValueOperand(expression ir.PhysicalExpression) (callOperand, error) {
	value := expression.Value
	if value == nil || value.BindKey != "" {
		return callOperand{}, fmt.Errorf("%w: VALUE call operands must read a row path, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if _, ok := r.rows[value.Variable]; !ok {
		return callOperand{}, fmt.Errorf("%w: call operand reads variable %q, which does not bind a readable row",
			ErrUnsupportedPostgresPlan, value.Variable)
	}
	// A derived row is a call operand only when it is a registered JSON
	// document. The ordinality and has-item derivations carry raw integer
	// and boolean SQL (unnest.go:683,687) which resolveDerivedValue hands
	// back verbatim for a bare path (unnest.go:716-718); letting that
	// reach a jsonb operand position would trade this file's typed
	// pre-SQL refusal for a raw 42804 at prepare time — the exact
	// posture renderCallExtractOperand takes against derived rows.
	if scope := r.rows[value.Variable]; scope.derivedValue != "" && !scope.derivedJSON {
		return callOperand{}, fmt.Errorf("%w: call operand reads derived variable %q, which carries a computed non-JSON value (ordinality and has-item columns are not registered call operands)",
			ErrUnsupportedPostgresPlan, value.Variable)
	}
	if len(value.Path) == 1 && resourceFilterColumns[canonicalKeyColumn(value.Path[0])] {
		return callOperand{}, fmt.Errorf("%w: call operand %v addresses the typed storage column %q; registered call operands read JSON documents, not storage columns",
			ErrUnsupportedPostgresPlan, value.Path, value.Path[0])
	}
	sql, err := r.resolveValue(*value)
	if err != nil {
		return callOperand{}, err
	}
	return callOperand{sql: "(" + sql + ")"}, nil
}

// renderCallPair renders two operands, rejecting nothing beyond the operand
// contract itself (comparisons are total over every jsonb kind).
func (r *planRenderer) renderCallPair(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (callOperand, callOperand, error) {
	left, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return callOperand{}, callOperand{}, fmt.Errorf("argument 0: %w", err)
	}
	right, err := r.renderCallOperand(plan, call.Args[1], depth)
	if err != nil {
		return callOperand{}, callOperand{}, fmt.Errorf("argument 1: %w", err)
	}
	return left, right, nil
}

// ---------------------------------------------------------------------------
// Arithmetic
// ---------------------------------------------------------------------------

// renderCallArithmetic renders add/subtract/multiply/divide as real
// double-precision operations: ArangoDB stores IEEE-754 doubles and its
// arithmetic is hardware IEEE (fundamentals-data-types.md 72-73), PG18
// double + - * / rounds identically, and no numeric-domain emulation can
// match that rounding (PG18 measured live: float8->numeric is a
// shortest-decimal conversion, so a numeric-domain chain cannot recover an
// operand's binary value; and numeric division truncates near the
// 17-significant-digit mark, misrounding 1-ulp cases such as 11/100000).
// PG18 also raises 22003 when a double operation overflows or rounds a
// nonzero product/quotient to zero (measured live: 1e308+1e308,
// 1.7976931348623155e308+2.6e307, 1e-310*1e-20 and 1e-310/1e20 all raise;
// gradual underflow and cancellation never raise), so each double
// operation runs only in an arm whose operand and result magnitudes were
// pre-proven safe by exact numeric-domain band tests that themselves never
// raise, and no arm depends on CASE evaluation order: every arm is
// individually raise-free. The pass bands admit every text that
// round-trips through float64 — every spelling loom ingests — so registered
// outputs equal IEEE's; the sub-ulp gray bands between a pass boundary and
// the true raise point (reachable only by hand-authored non-round-tripping
// text) read the registered conservative value of their outer side (L20,
// pinned live). IEEE overflow — an Infinity result, which operators.md
// 259-262 reads back as invalid → null and jsonb could not hold anyway —
// reads NULL; IEEE underflow reads 0. The divisor is NULLIF-guarded so
// SQLSTATE 22012 is unreachable; a divisor whose magnitude parses below the
// subnormal floor reads NULL like division by zero (its IEEE quotient, if
// the text is above the IEEE tie, would overflow to Infinity — null again;
// below it, IEEE divides by zero — null again). Division biases the
// dividend's numeric scale (callDividendScaleBias) so the truncating band-
// test quotient carries 400 fractional digits — far inside both the
// overflow margin and the subnormal-boundary spacing.
func (r *planRenderer) renderCallArithmetic(plan ir.PhysicalPlan, name string, call *ir.PhysicalCall, depth int) (string, error) {
	operator, ok := map[string]string{"add": "+", "subtract": "-", "multiply": "*", "divide": "/"}[name]
	if !ok {
		return "", fmt.Errorf("%w: arithmetic call %q is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan, name)
	}
	left, err := r.renderCallNumericOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", fmt.Errorf("argument 0: %w", err)
	}
	right, err := r.renderCallNumericOperand(plan, call.Args[1], depth)
	if err != nil {
		return "", fmt.Errorf("argument 1: %w", err)
	}
	switch operator {
	case "+", "-":
		// Addition and subtraction never raise except by rounding the sum
		// to Infinity: cancellation and gradual results flush to zero
		// without an exception (PG18 measured), so only the overflow band
		// is needed. Each operand magnitude is < the round-to-Infinity tie
		// (callDoubleOperand), so |a±b| exceeds the test value by at most
		// two half-ulps of DBL_MAX (2 * 2^970); passing strictly below
		// callDoubleAddPassUpper (DBL_MAX − 2^970) keeps |a±b| strictly
		// below the tie.
		magnitude := "abs((" + left + " " + operator + " " + right + "))"
		return "to_jsonb(CASE WHEN " + magnitude + " < '" + callDoubleAddPassUpper + "'::numeric" +
			" THEN " + callDoubleOperand(left) + " " + operator + " " + callDoubleOperand(right) +
			" ELSE NULL END)", nil
	case "*":
		// Multiplication additionally raises when a nonzero product rounds
		// to zero: passing requires |a*b| strictly below the overflow tie
		// with the operand-parsing error margin (each double is within
		// half an ulp of its exact operand value, so |a*b| deviates from
		// the exact numeric product by at most product·2^-52 ≈ 2^972 at
		// the top; the margin below is 2^973) AND at or above
		// callDoubleSubnormalFloor (17 significant digits above the exact
		// minimum subnormal, so the product rounds to at least one
		// subnormal and cannot raise; PG18 measured gradual products safe
		// but a round-to-zero product raising). Below the floor the
		// registered 0 arm answers, above the pass upper the NULL arm.
		magnitude := "abs((" + left + " * " + right + "))"
		return "to_jsonb(CASE WHEN " + magnitude + " < '" + callDoubleMulDivPassUpper + "'::numeric" +
			" AND " + magnitude + " >= '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN " + callDoubleOperand(left) + " * " + callDoubleOperand(right) +
			" WHEN " + magnitude + " < '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN 0::double precision ELSE NULL END)", nil
	default:
		// The quotient magnitude test runs in numeric against a scale-
		// biased NULLIF-guarded division (never raising: numeric division
		// truncates, and the biased zero plus NULLIF fold the zero divisor
		// to NULL, whose comparisons select the ELSE NULL arm — the
		// registered reading of division by zero, operators.md 260-262).
		// The divisor double arm maps the zero and subnormal-floor regions
		// to NULL rather than 0.0 so the executed division can never see a
		// zero divisor (callDoubleDivisor).
		quotient := "((" + left + " + " + callDividendScaleBias + ") / NULLIF(" + right + ", 0))"
		magnitude := "abs(" + quotient + ")"
		return "to_jsonb(CASE WHEN " + magnitude + " < '" + callDoubleMulDivPassUpper + "'::numeric" +
			" AND " + magnitude + " >= '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN " + callDoubleOperand(left) + " / " + callDoubleDivisor(right) +
			" WHEN " + magnitude + " < '" + callDoubleSubnormalFloor + "'::numeric" +
			" THEN 0::double precision ELSE NULL END)", nil
	}
}

// Registered IEEE-754 double boundaries, as exact numeric literals:
//   - callDoubleOverflowBound is DBL_MAX + 2^970, the round-half-even tie
//     above the largest finite double: a numeric magnitude at or beyond it
//     is the IEEE overflow case (Infinity, read back as invalid → null per
//     operators.md 259-262), and PG18 measured numeric->double casts raise
//     22003 exactly there and above.
//   - callDoubleAddPassUpper is DBL_MAX − 2^970: the strict pass boundary
//     for an add/subtract band test (two operand half-ulps below the tie).
//   - callDoubleMulDivPassUpper is the round-half-even tie − 2^973: the
//     strict pass boundary for a multiply/divide band test (operand
//     relative error 2^-52 ≈ 2^972 at the top, with margin).
//   - callDoubleUnderflowFloor is 2.5e-324, above the round-to-zero tie
//     (2^-1075 ≈ 2.4703282292062327e-324, where PG18 measured the cast
//     raising) and below the minimum subnormal: a nonzero magnitude at or
//     above it casts to at least the minimum subnormal without raising;
//     below it the registered reading is 0 (IEEE's own answer below the
//     tie, the L20 gray band above it).
//   - callDoubleSubnormalFloor is 17 significant digits above the exact
//     minimum subnormal (2^-1074 ≈ 4.9406564584124654e-324): a product or
//     quotient magnitude at or above it rounds to at least one subnormal
//     and cannot raise; below it the registered reading is 0.
const (
	callDoubleOverflowBound   = "179769313486231580793728971405303415079934132710037826936173778980444968292764750946649017977587207096330286416692887910946555547851940402630657488671505820681908902000708383676273854845817711531764475730270069855571366959622842914819860834936475292719074168444365510704342711559699508093042880177904174497792"
	callDoubleAddPassUpper    = "179769313486231560835325876058105298516207002341652166261661174625869553267292326574530099287946549246750631490335877017522087105926987962906277604735569213290190919152394180476217125334960946356387261286640198029037799514183602981511756283727771403830521483963923935633133642802139091669457927874464075218944"
	callDoubleMulDivPassUpper = "179769313486231500960116590016510948825025611236495184238123361562143308190875053458173343219024575698011666711264844337248681780152130643733137952927759391115036970607451570876046936802390650830255617955750582549437097177865883181587442630101659737164863430522599210419506436529457842398703070964143777382400"
	callDoubleUnderflowFloor  = "2.5E-324"
	callDoubleSubnormalFloor  = "4.9406564584124655E-324"
	// callDividendScaleBias forces the band-test quotient's numeric scale
	// to at least 400 fractional digits (PG18 measured: the dividend's
	// numeric scale drives the quotient scale; plain integer operands
	// truncate, and the dscale maximum is 1000). The truncation error
	// (5e-401) sits far below both the subnormal-boundary spacing and the
	// overflow margins, so the band decision equals the double result's
	// side of each boundary for every round-tripping operand.
	callDividendScaleBias = "0E-400"
)

// callDoubleOperand converts one never-raising numeric fragment to the
// double that IEEE's parser would produce: magnitudes at or beyond
// callDoubleOverflowBound (IEEE's Infinity, the documented invalid value)
// read NULL; magnitudes at or above callDoubleUnderflowFloor cast exactly
// once (PG18 measured: the cast rounds correctly across the whole finite
// range, including subnormals, and the exact zero casts to 0.0); and
// magnitudes below the floor — where PG18 measured the cast rounding to
// zero and raising — read the registered 0, which is IEEE's own answer
// below the tie and the L20 gray band above it. The CAST is reached only
// where PG18 measured it cannot raise.
func callDoubleOperand(numericOperand string) string {
	return "(CASE WHEN abs(" + numericOperand + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + numericOperand + ") >= '" + callDoubleUnderflowFloor + "'::numeric" +
		" THEN (" + numericOperand + ")::double precision ELSE 0::double precision END" +
		" ELSE NULL END)"
}

// callDoubleDivisor is callDoubleOperand for the executed division's right
// side: the zero divisor and the subnormal-floor gray band both read NULL
// (division by zero is the registered null; a subnormal-floor divisor is
// either IEEE's own zero — division by zero, null — or above the tie,
// where its double quotient overflows to Infinity — invalid, null again).
// This keeps a zero-valued double out of the divisor position, which would
// raise SQLSTATE 22037.
func callDoubleDivisor(numericOperand string) string {
	return "(CASE WHEN abs(" + numericOperand + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + numericOperand + ") >= '" + callDoubleSubnormalFloor + "'::numeric" +
		" THEN (" + numericOperand + ")::double precision ELSE NULL END" +
		" ELSE NULL END)"
}

// callNumericToJSONB casts one exact-numeric expression to a jsonb number
// without ever raising: magnitudes at or beyond callDoubleOverflowBound
// (IEEE overflow) read NULL; nonzero magnitudes below
// callDoubleUnderflowFloor (the region whose PG18-measured cast rounds to
// zero and raises — the tie itself included) read 0; NULL propagates
// through both guards. The numeric->double cast itself is exact-rounding
// inside the guarded range (PG18 measured). The cast tables (TO_NUMBER)
// keep using this numeric-domain path; only the arithmetic operators need
// the IEEE float8 domain of renderCallArithmetic.
func callNumericToJSONB(expression string) string {
	return "to_jsonb(CASE WHEN abs(" + expression + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + expression + ") >= '" + callDoubleUnderflowFloor + "'::numeric" +
		" THEN (" + expression + ")::double precision ELSE 0::double precision END END)"
}

// renderCallNumericOperand renders one operand to a double-precision
// expression that reads NULL exactly when the operand contributes NULL to
// the result (missing, JSON null, non-numeric outside the documented
// coercion arms). Literal operands must be numbers — any other bind type is
// an unknown call/type pair refused before SQL.
func (r *planRenderer) renderCallNumericOperand(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (string, error) {
	if expression.Kind == ir.PhysicalLiteralExpression {
		literal, err := r.renderCallLiteral(plan, expression)
		if err != nil {
			return "", err
		}
		if literal.kind != callLiteralNumber {
			return "", fmt.Errorf("%w: arithmetic operand literal has stored kind %s; arithmetic accepts number binds (dynamic operands carry the registered coercion table)",
				ErrUnsupportedPostgresPlan, callLiteralKindName(literal.kind))
		}
		return "((" + literal.sql + ") #>> '{}')::numeric", nil
	}
	if expression.Kind == ir.PhysicalCallExpression {
		sql, facts, err := r.renderCallExpression(plan, expression, depth)
		if err != nil {
			return "", err
		}
		// Same bottom-up budget discipline as the operand choke point:
		// this direct recursion must refuse a replicated operand before
		// embedding it in the numeric arms.
		if err := checkCallFragment(sql); err != nil {
			return "", err
		}
		if facts.staticKind == callLiteralNumber {
			return "((" + sql + ") #>> '{}')::numeric", nil
		}
		return callNumericCoercion("(" + sql + ")"), nil
	}
	operand, err := r.renderCallOperand(plan, expression, depth)
	if err != nil {
		return "", err
	}
	return callNumericCoercion(operand.sql), nil
}

// callNumericCoercion renders the numeric-domain pre-pass of one dynamic
// jsonb operand (the double conversion itself is callDoubleOperand's):
// numbers convert through their canonical text under a CASE-guarded cast
// (never an unguarded cast — the planner does not promise AND-clause
// ordering), decimal-form strings coerce through the documented arithmetic
// reading (operators.md 275-277 shows 24/"12" // 2), and every other stored
// kind — booleans, arrays, objects, and strings outside the registered
// decimal grammar, Unicode-space-padded numerics included — reads NULL:
// the registered non-numeric-operand arm (L2), the fail-closed reading of
// the documented arithmetic policy, which promises only that invalid
// values do not raise (operators.md 259-262; functions-type-cast.md 65-66
// fixes the cast context's own invalid reading at 0, which the TO_NUMBER
// arm applies).
func callNumericCoercion(operand string) string {
	return "(CASE" +
		" WHEN " + operand + " IS NULL OR jsonb_typeof(" + operand + ") = 'null' THEN NULL" +
		" WHEN jsonb_typeof(" + operand + ") = 'number' THEN (" + operand + " #>> '{}')::numeric" +
		" WHEN jsonb_typeof(" + operand + ") = 'string' AND (" + operand + " #>> '{}') ~ " + quoteSQLLiteral(callDecimalStringPattern) +
		" THEN btrim((" + operand + " #>> '{}'), " + callDecimalTrimCharacters + ")::numeric" +
		" ELSE NULL END)"
}

// callDecimalWhitespace is the single source of truth for the numeric
// guard's whitespace alphabet: the grammar's surrounding arms and btrim's
// erasure set are both derived from this one constant, so the two SQL
// sites cannot drift apart — the F-5 shape (a grammar class wider than the
// trim set, PG18's ICU [[:space:]] admitting U+2000-U+200A/U+2028/U+205F/
// U+3000 while numeric_in raises 22P02 on their cast, PG18 measured) is
// unconstructible by construction. The alphabet is the explicit ASCII
// isspace set — space, tab, newline, vertical tab, form feed, carriage
// return — which is the widest set ArangoDB's "any whitespace at the start
// or the end" can be read as under its own documentation: the docs leave
// "whitespace" undefined (operators.md 252-253, functions-type-cast.md
// 64-65), and the locale-dependent POSIX class would put the never-raise
// invariant at the mercy of the collation. Unicode-space-padded numerics
// therefore take the documented non-matching arms — cast reads 0
// (functions-type-cast.md 65-66), arithmetic coercion reads the
// registered NULL (L2) — both never-raising and locale-independent.
const callDecimalWhitespace = " \t\n\v\f\r"

// callDecimalStringPattern is the conservative registered numeric grammar:
// optional surrounding whitespace (both arms derived from
// callDecimalWhitespace), one sign, decimal digits with an optional
// fraction and exponent — the shapes both ArangoDB's documented "valid
// representation of a number" and PostgreSQL's double text parsing accept
// identically. Wider JavaScript extensions (hexadecimal, "Infinity") stay
// outside the registered grammar.
var callDecimalStringPattern = `^` + callWhitespaceRegexClass() + `*[+-]?([0-9]+(\.[0-9]*)?|\.[0-9]+)([eE][+-]?[0-9]+)?` + callWhitespaceRegexClass() + `*$`

// callDecimalTrimCharacters is btrim's explicit whitespace argument, built
// from callDecimalWhitespace alone so it erases exactly what the grammar
// admits. Bare btrim(text) trims only the space character, which would let
// a tab- or newline-padded spelling pass the guard and then depend on
// numeric_in's version-varying whitespace tolerance (pinned live for tab-
// and newline-padded spellings).
var callDecimalTrimCharacters = quoteSQLLiteral(callDecimalWhitespace)

// callWhitespaceRegexClass renders callDecimalWhitespace as a bracket
// expression: the characters appear raw (the pattern travels through
// quoteSQLLiteral's standard quoting, where control characters survive
// verbatim — no backslash escapes are needed and the ICU interpretation of
// \t-style escapes stays irrelevant) and regex-significant characters are
// backslash-escaped, so the class cannot be reinterpreted.
func callWhitespaceRegexClass() string {
	var class strings.Builder
	class.WriteByte('[')
	for _, r := range callDecimalWhitespace {
		switch r {
		case '\\', ']', '^', '-':
			class.WriteByte('\\')
		}
		class.WriteRune(r)
	}
	class.WriteByte(']')
	return class.String()
}

// ---------------------------------------------------------------------------
// Cast
// ---------------------------------------------------------------------------

// renderCallCast renders the registered TO_STRING/TO_NUMBER/TO_BOOL tables
// (functions-type-cast.md 24-107). It returns the registered stored-kind
// guarantee of the result.
func (r *planRenderer) renderCallCast(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, callLiteralKind, error) {
	operand, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", callLiteralNot, err
	}
	switch strings.ToLower(strings.TrimSpace(call.TargetKind)) {
	case "string", "code", "uuid":
		// TO_STRING: null → "", booleans/numbers/strings → their text,
		// arrays/objects → NULL (the JSON no-extra-whitespace conversion is
		// unregistered; fail-closed NULL never prints misleading text).
		return "CASE" +
			" WHEN " + operand.sql + " IS NULL OR jsonb_typeof(" + operand.sql + ") = 'null' THEN '\"\"'::jsonb" +
			" WHEN jsonb_typeof(" + operand.sql + ") IN ('string', 'boolean', 'number')" +
			" THEN to_jsonb((" + operand.sql + ") #>> '{}')" +
			" ELSE NULL END", callLiteralString, nil
	case "integer", "decimal":
		return callToNumberExpression(operand.sql), callLiteralNumber, nil
	case "boolean":
		// TO_BOOL, fully documented: null → false, 0 (every jsonb spelling
		// through jsonb numeric equality) → false, other numbers → true,
		// "" → false, non-empty strings → true, arrays/objects → true.
		return "CASE" +
			" WHEN " + operand.sql + " IS NULL OR jsonb_typeof(" + operand.sql + ") = 'null' THEN 'false'::jsonb" +
			" WHEN jsonb_typeof(" + operand.sql + ") = 'boolean' THEN " + operand.sql +
			" WHEN jsonb_typeof(" + operand.sql + ") = 'number' THEN CASE WHEN " + operand.sql +
			" = '0'::jsonb THEN 'false'::jsonb ELSE 'true'::jsonb END" +
			" WHEN jsonb_typeof(" + operand.sql + ") = 'string' THEN CASE WHEN " + operand.sql +
			" = '\"\"'::jsonb THEN 'false'::jsonb ELSE 'true'::jsonb END" +
			" ELSE 'true'::jsonb END", callLiteralBoolean, nil
	default:
		return "", callLiteralNot, fmt.Errorf("%w: cast target kind %q is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan, call.TargetKind)
	}
}

// callToNumberMaxRecursion bounds the documented TO_NUMBER([x]) recursion.
// ArangoDB recurses without a documented limit; beyond three nesting levels
// the registered table falls to the documented object/many-arm value 0,
// which every conforming FHIR source never reaches.
const callToNumberMaxRecursion = 3

// callToNumberExpression renders the registered TO_NUMBER table. The whole
// table is computed in the numeric domain and guarded before to_jsonb
// (callNumericToJSONB): a decimal-form string such as "1e400" parses to a
// magnitude beyond the finite-double range, and the registered policy is
// fail-closed NULL, never a raise on stored data (PG18 measured: a direct
// text->double cast raises 22003 there).
func callToNumberExpression(operand string) string {
	return callNumericToJSONB(callToNumberDouble(operand, 0))
}

// callToNumberDouble renders the TO_NUMBER arms as a double-precision
// scalar expression. Every jsonb_array_length call sits inside a CASE
// already narrowed to the array typeof (CASE WHEN clauses evaluate in
// written order; a bare AND would not promise it), and the single-element
// recursion substitutes the element expression textually one level down.
func callToNumberDouble(operand string, depth int) string {
	if depth >= callToNumberMaxRecursion {
		// Deeper nesting reads the documented "not a convertible shape"
		// value 0 rather than recursing without bound.
		return "0"
	}
	element := callToNumberDouble("(("+operand+") -> 0)", depth+1)
	return "CASE" +
		" WHEN " + operand + " IS NULL OR jsonb_typeof(" + operand + ") = 'null' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'boolean' THEN CASE WHEN " + operand +
		" = 'true'::jsonb THEN 1 ELSE 0 END" +
		" WHEN jsonb_typeof(" + operand + ") = 'number' THEN (" + operand + " #>> '{}')::numeric" +
		" WHEN jsonb_typeof(" + operand + ") = 'string' AND (" + operand + " #>> '{}') ~ " + quoteSQLLiteral(callDecimalStringPattern) +
		" THEN btrim((" + operand + " #>> '{}'), " + callDecimalTrimCharacters + ")::numeric" +
		" WHEN jsonb_typeof(" + operand + ") = 'string' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'array' THEN CASE" +
		" WHEN jsonb_array_length(" + operand + ") = 0 THEN 0" +
		" WHEN jsonb_array_length(" + operand + ") = 1 THEN " + element +
		" ELSE 0 END" +
		" ELSE 0 END"
}

// ---------------------------------------------------------------------------
// Null fallback, conditionals, and boolean calls
// ---------------------------------------------------------------------------

func (r *planRenderer) renderCallCoalesce(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	parts := make([]string, 0, len(call.Args))
	for index := range call.Args {
		operand, err := r.renderCallOperand(plan, call.Args[index], depth)
		if err != nil {
			return "", fmt.Errorf("argument %d: %w", index, err)
		}
		// AQL drops every value that reads null (missing key or stored JSON
		// null alike) before FIRST; NULLIF folds the jsonb 'null' scalar to
		// SQL NULL so COALESCE sees exactly the AQL surviving multiset, and
		// all-null COALESCEs to SQL NULL (FIRST of the empty array).
		parts = append(parts, "NULLIF("+operand.sql+", 'null'::jsonb)")
	}
	return "COALESCE(" + strings.Join(parts, ", ") + ")", nil
}

func (r *planRenderer) renderCallIf(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	condition, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", fmt.Errorf("condition: %w", err)
	}
	thenOperand, err := r.renderCallOperand(plan, call.Args[1], depth)
	if err != nil {
		return "", fmt.Errorf("then arm: %w", err)
	}
	elseOperand, err := r.renderCallOperand(plan, call.Args[2], depth)
	if err != nil {
		return "", fmt.Errorf("else arm: %w", err)
	}
	return "CASE WHEN " + callTruthyTest(condition.sql) + " THEN " + thenOperand.sql + " ELSE " + elseOperand.sql + " END", nil
}

// renderCallCase right-nests the condition/result pairs exactly as
// render/aql/calls.go:177-194 does, with the optional trailing else (an odd
// argument count carries it, an even count has none). A missing else
// renders the SQL NULL keyword — the AQL scalarNull bind has no PostgreSQL
// parameter form, and a bound NULL would strand a parameter the statement
// must reference.
func (r *planRenderer) renderCallCase(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	operands := make([]callOperand, len(call.Args))
	for index := range call.Args {
		operand, err := r.renderCallOperand(plan, call.Args[index], depth)
		if err != nil {
			return "", fmt.Errorf("argument %d: %w", index, err)
		}
		operands[index] = operand
	}
	end := len(operands)
	result := "NULL"
	if end%2 == 1 {
		result = operands[end-1].sql
		end--
	}
	for index := end - 2; index >= 0; index -= 2 {
		result = "CASE WHEN " + callTruthyTest(operands[index].sql) + " THEN " + operands[index+1].sql + " ELSE " + result + " END"
	}
	return result, nil
}

// renderCallLogical renders the documented value-returning and/or: lhs
// falsy → lhs, else rhs (and); lhs truthy → lhs, else rhs (or). The CASE
// evaluates only the taken arm, reproducing AQL short-circuit evaluation
// (operators.md 146-148), so an assert in the untaken arm never raises.
func (r *planRenderer) renderCallLogical(plan ir.PhysicalPlan, name string, call *ir.PhysicalCall, depth int) (string, error) {
	operands := make([]callOperand, len(call.Args))
	for index := range call.Args {
		operand, err := r.renderCallOperand(plan, call.Args[index], depth)
		if err != nil {
			return "", fmt.Errorf("argument %d: %w", index, err)
		}
		operands[index] = operand
	}
	sql := operands[len(operands)-1].sql
	for index := len(operands) - 2; index >= 0; index-- {
		test := callTruthyTest(operands[index].sql)
		if name == "and" {
			sql = "CASE WHEN " + test + " THEN " + sql + " ELSE " + operands[index].sql + " END"
		} else {
			sql = "CASE WHEN " + test + " THEN " + operands[index].sql + " ELSE " + sql + " END"
		}
	}
	return sql, nil
}

// callTruthyTest renders the two-valued truthiness test of the documented
// falsy set {null, false, 0, ""} (operators.md 176-183): numbers compare
// against jsonb 0 through jsonb numeric equality (every spelling of zero —
// the exact reading the live-measured unnest.go falsy arms use), and the
// whole test is total because every arm is total once the SQL NULL arm is
// taken first.
func callTruthyTest(operand string) string {
	return "NOT (" + operand + " IS NULL" +
		" OR jsonb_typeof(" + operand + ") = 'null'" +
		" OR " + operand + " = 'false'::jsonb" +
		" OR (jsonb_typeof(" + operand + ") = 'number' AND " + operand + " = '0'::jsonb)" +
		" OR " + operand + " = '\"\"'::jsonb)"
}

func sqlCallComparison(name string) string {
	switch name {
	case "gt":
		return ">"
	case "gte":
		return ">="
	case "lt":
		return "<"
	case "lte":
		return "<="
	default:
		return ""
	}
}

// callOrderTest renders the registered ordering comparison: the documented
// AQL type order ranks each side (null < bool < number < string; array and
// object rank last and read NULL, L15 — checked before any rank comparison
// so a rank-4 operand never leaks a rank ordering), cross-rank scalar pairs
// compare by rank, and same-rank values compare with the jsonb operator
// inside a single type — where the cross-type jsonb order predicates.go
// measured never enters. Nothing coerces across ranks on either engine:
// AQL compares data types before values ("45 <= \"yikes!\"" is true because
// number ranks below string; fundamentals-type-value-order.md 15-18,
// operators.md 39-47), so a numeric-form string ranks as a string here too.
// The result is total over scalar kinds, matching
// AQL, where null compares to every value without becoming null itself.
func callOrderTest(left, operator, right string) string {
	leftRank := callValueRank(left)
	rightRank := callValueRank(right)
	return "CASE" +
		" WHEN " + leftRank + " >= 4 OR " + rightRank + " >= 4 THEN NULL" +
		" WHEN " + leftRank + " <> " + rightRank + " THEN " + leftRank + " " + operator + " " + rightRank +
		" ELSE (" + callNullNormalized(left) + ") " + operator + " (" + callNullNormalized(right) + ") END"
}

func callNullNormalized(operand string) string {
	return "COALESCE(" + operand + ", 'null'::jsonb)"
}

func callValueRank(operand string) string {
	return "(CASE" +
		" WHEN " + operand + " IS NULL OR jsonb_typeof(" + operand + ") = 'null' THEN 0" +
		" WHEN jsonb_typeof(" + operand + ") = 'boolean' THEN 1" +
		" WHEN jsonb_typeof(" + operand + ") = 'number' THEN 2" +
		" WHEN jsonb_typeof(" + operand + ") = 'string' THEN 3" +
		" ELSE 4 END)"
}

// renderCallAssert renders ASSERT(cond, message) (functions-miscellaneous.md
// 504-517): truthy returns the jsonb 'true' scalar; falsy raises SQLSTATE
// 22P02. The message is plan data, so it rides a $n text parameter INSIDE
// the barrier-cast concatenation (never SQL-interpolated); the
// population.go current_setting barrier keeps the raising arm stable
// against the plan-time constant folding that raises bare dead-arm CASTs
// there (measured live, population.go:1059-1065).
func (r *planRenderer) renderCallAssert(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	condition, err := r.renderCallOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", fmt.Errorf("condition: %w", err)
	}
	message := call.Args[1]
	if message.Kind != ir.PhysicalLiteralExpression || message.Literal == nil || message.Literal.BindKey == "" {
		return "", fmt.Errorf("%w: assert message must be one bind-backed string literal", ErrUnsupportedPostgresPlan)
	}
	value, ok := plan.BindVars[message.Literal.BindKey]
	if !ok {
		return "", fmt.Errorf("%w: assert message bind %q is not defined", ErrUnsupportedPostgresPlan, message.Literal.BindKey)
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%w: assert message bind %q has type %T; the ASSERT message must be a string (the unknown call/type pair gate)",
			ErrUnsupportedPostgresPlan, message.Literal.BindKey, value)
	}
	index := r.textParameter(text, "")
	// to_jsonb keeps the CASE arms type-matchable (jsonb/jsonb): the
	// integer CAST inside the barrier still raises 22P02 when evaluated,
	// but an unmatched arm type would refuse the whole statement at
	// parse time (42804, measured live).
	raise := "to_jsonb(CAST('ASSERT:' || $" + strconv.Itoa(index) +
		" || COALESCE(current_setting(" + quoteSQLLiteral(guardBarrierGUC) + ", true), '') AS integer))"
	return "CASE WHEN " + callTruthyTest(condition.sql) + " THEN 'true'::jsonb ELSE " + raise + " END", nil
}

// ---------------------------------------------------------------------------
// distinct-present
// ---------------------------------------------------------------------------

// distinctPresentGuardTags are the canonical construction row-value ASSERT
// tags spelled literally by the AQL producer (render/aql/calls.go:97-98);
// they are renderer-owned structural constants, so they take the plain
// population.go guardCast (tag embedded in the 22P02 message).
const (
	distinctPresentGuardTypeMismatch   = "CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH"
	distinctPresentGuardMultipleValues = "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES"
)

// renderCallDistinctPresentOne renders the AQL reading of
// render/aql/calls.go:84-99 as one correlated scalar subquery:
// FLATTEN promotes exactly one nesting level (a direct child array's items
// take its place; a second-level array survives as an item and fails the
// type assertion, matching FLATTEN's single pass), JSON-null items drop
// before every check, each remaining item must carry the target scalar's
// jsonb type or the query raises the type-mismatch tag, more than one
// DISTINCT value raises the multiple-values tag, zero values read NULL, and
// one value reads that value (MIN equals FIRST(SORTED_UNIQUE) once every
// item is the asserted same type). The expected jsonb type mirrors
// aqlTableScalarType (render/aql/table_reshape.go:693-704). The item
// selection uses a CASE, not a COALESCE, so an empty array contributes zero
// items: an array outer row with no flattened children yields a NULL item
// that the null filter drops, exactly matching FLATTEN([]) = [].
func (r *planRenderer) renderCallDistinctPresentOne(plan ir.PhysicalPlan, call *ir.PhysicalCall, depth int) (string, error) {
	var expected string
	switch call.TargetKind {
	case "STRING":
		expected = "string"
	case "INTEGER", "DECIMAL":
		expected = "number"
	case "BOOLEAN":
		expected = "boolean"
	default:
		return "", fmt.Errorf("%w: distinct_present_one target kind %q is not registered", ErrUnsupportedPostgresPlan, call.TargetKind)
	}
	base, err := r.renderCallArrayOperand(plan, call.Args[0], depth)
	if err != nil {
		return "", err
	}
	// L14: a non-array collection reads zero items (AQL would error
	// iterating it; the package never raises on stored data).
	guarded := "CASE WHEN " + base + " IS NULL OR jsonb_typeof(" + base + ") <> 'array' THEN NULL::jsonb ELSE " + base + " END"
	// to_jsonb on every arm keeps the CASE type-matchable (measured
	// live: integer guard arms against jsonb value arms refuse the
	// statement at parse time, 42804); the barrier casts still raise
	// 22P02 when reached. There is no min(jsonb) aggregate (42883): the
	// unique present value is min'd over its jsonb text and cast back,
	// exact because COUNT(DISTINCT item) = 1 above this arm.
	return "(SELECT CASE" +
		" WHEN bool_or(jsonb_typeof(loom_dp_item.item) <> " + quoteSQLLiteral(expected) + ") THEN to_jsonb(" + guardCast(distinctPresentGuardTypeMismatch) + ")" +
		" WHEN COUNT(*) = 0 THEN NULL" +
		" WHEN COUNT(DISTINCT loom_dp_item.item) > 1 THEN to_jsonb(" + guardCast(distinctPresentGuardMultipleValues) + ")" +
		" ELSE min(loom_dp_item.item::text)::jsonb" +
		" END FROM jsonb_array_elements(" + guarded + ") AS loom_dp_outer(value)" +
		" LEFT JOIN LATERAL jsonb_array_elements(" +
		"CASE WHEN jsonb_typeof(loom_dp_outer.value) = 'array' THEN loom_dp_outer.value ELSE NULL::jsonb END" +
		") AS loom_dp_flattened(value) ON TRUE" +
		" LEFT JOIN LATERAL (SELECT CASE" +
		" WHEN jsonb_typeof(loom_dp_outer.value) = 'array' THEN loom_dp_flattened.value" +
		" ELSE loom_dp_outer.value END AS item) AS loom_dp_item ON TRUE" +
		" WHERE loom_dp_item.item IS NOT NULL AND loom_dp_item.item <> 'null'::jsonb)", nil
}

// renderCallArrayOperand renders the array-argument form of
// distinct_present_one: an ARRAY-cardinality EXTRACT of a direct property
// path over a readable row (the closed selector shape re-read by
// predicates.go for comparisons) or a bind literal that must unmarshal to a
// JSON array (any other bind type is an unknown call/type pair).
func (r *planRenderer) renderCallArrayOperand(plan ir.PhysicalPlan, expression ir.PhysicalExpression, depth int) (string, error) {
	switch expression.Kind {
	case ir.PhysicalLiteralExpression:
		literal, err := r.renderCallLiteral(plan, expression)
		if err != nil {
			return "", err
		}
		if literal.kind != callLiteralArray {
			return "", fmt.Errorf("%w: distinct_present_one literal must bind a JSON array (the unknown call/type pair gate); got stored kind %s",
				ErrUnsupportedPostgresPlan, callLiteralKindName(literal.kind))
		}
		return literal.sql, nil
	case ir.PhysicalExtractExpression:
		if expression.Cardinality != ir.PhysicalArrayCardinality {
			return "", fmt.Errorf("%w: distinct_present_one EXTRACT argument must be ARRAY-cardinality", ErrUnsupportedPostgresPlan)
		}
		extract := expression.Extract
		if extract == nil || len(extract.Fallbacks) != 0 || extract.Prepared != nil ||
			extract.UnitNormalization != nil || extract.Distinct || extract.Selector.Filter != nil {
			return "", fmt.Errorf("%w: distinct_present_one accepts only the closed direct-property EXTRACT; fallbacks, prepared sets, unit normalization, DISTINCT, and selector filters are gaps",
				ErrUnsupportedPostgresPlan)
		}
		switch extract.ExecutionMode {
		case ir.PhysicalSelectorGeneric, ir.PhysicalSelectorDirectScalar, "":
		default:
			return "", fmt.Errorf("%w: distinct_present_one extraction mode %q has no registered form",
				ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
		}
		switch expression.NullBehavior {
		case ir.PhysicalPreserveNull, ir.PhysicalEmptyOnNull:
		default:
			return "", fmt.Errorf("%w: distinct_present_one extraction null behavior %q has no registered form",
				ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
		}
		source := extract.Source
		if source.BindKey != "" || source.Variable == "" || len(source.Path) != 1 || source.Path[0] != "payload" {
			return "", fmt.Errorf("%w: distinct_present_one source must be one readable row's document payload", ErrUnsupportedPostgresPlan)
		}
		scope, ok := r.rows[source.Variable]
		if !ok || scope.alias == "" || scope.derivedValue != "" || scope.derivedJSON {
			return "", fmt.Errorf("%w: distinct_present_one source variable %q does not bind a readable stored row",
				ErrUnsupportedPostgresPlan, source.Variable)
		}
		if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != scope.resourceType {
			return "", fmt.Errorf("%w: distinct_present_one resource type %q contradicts the scanned row type %q",
				ErrUnsupportedPostgresPlan, extract.ResourceType, scope.resourceType)
		}
		path := make([]string, 0, len(extract.Selector.Steps))
		for _, step := range extract.Selector.Steps {
			if step.Field == "" || step.Iterate || step.Index != nil {
				return "", fmt.Errorf("%w: distinct_present_one accepts only direct property paths; repeated, indexed, and filtered steps are gaps",
					ErrUnsupportedPostgresPlan)
			}
			if !payloadPathSegmentPattern.MatchString(step.Field) {
				return "", fmt.Errorf("%w: distinct_present_one path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
			}
			path = append(path, step.Field)
		}
		if len(path) == 0 {
			return "", fmt.Errorf("%w: distinct_present_one selector must have at least one property", ErrUnsupportedPostgresPlan)
		}
		return r.scalarExtractValueExpression(scope.alias, path, expression.NullBehavior)
	default:
		return "", fmt.Errorf("%w: distinct_present_one argument kind %q is not registered (subplan arguments stay unregistered in this wave; their registration is a coordinator decision)",
			ErrUnsupportedPostgresPlan, string(expression.Kind))
	}
}

// ---------------------------------------------------------------------------
// Literal rendering
// ---------------------------------------------------------------------------

// callLiteralKindName names a stored kind for error text.
func callLiteralKindName(kind callLiteralKind) string {
	switch kind {
	case callLiteralNull:
		return "JSON null"
	case callLiteralNumber:
		return "number"
	case callLiteralString:
		return "string"
	case callLiteralBoolean:
		return "boolean"
	case callLiteralArray:
		return "array"
	default:
		return "unknown"
	}
}
