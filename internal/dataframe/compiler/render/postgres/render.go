// Package postgres renders validated physical IR plans into parameterized
// PostgreSQL 18 statements for the source store (pg-ir-02).
//
// The renderer input is a physical plan plus an ir.PostgresStorageBinding.
// The binding is the only source of SQL identifiers: table and column names
// come from the compiler-owned allowlist in internal/dataframe/compiler/ir
// (postgres_storage.go), and an unregistered or hand-assembled identifier is
// rejected with ErrUnregisteredPostgresIdentifier. Everything else the plan
// carries — project, dataset generation, auth resource paths, resource
// types, edge labels, filter values, payload paths, limit values, and output
// column names — is runtime data and is passed to the driver as ordered,
// typed $n parameters. No plan string is ever interpolated into SQL text.
//
// Unsupported plans fail closed with ErrUnsupportedPostgresPlan; there is no
// fallback to the AQL renderer and no AQL-to-SQL conversion anywhere in this
// package. The rendered result is a compiler.RenderedPostgresPhysicalPlan,
// which additionally refuses AQL bind syntax and placeholder/argument
// mismatch at construction.
//
// The v1 supported surface is the scoped read spine: one direct root
// resource scan, typed scope predicates (explicit project, dataset
// generation, and an explicit auth mode), direct comparison filters over
// typed columns or JSONB payload paths, depth-one typed edge-occurrence
// traversals rendered by traversal.go — the OPTIONAL parent-preserving
// LEFT JOIN LATERAL child join and the REQUIRED match EXISTS semi-join —
// and the JSON-array cardinality boundary (UNNEST, pg-sql-09) registered in
// unnest.go as its own closed operation slice: one direct root scan, one or
// more JSON-array expansions with typed-identity owner routes, and a
// positional return over the stored rows plus the renderer-derived item,
// ancestor, ordinality, and has-item values — with one column sort and
// deterministic identity tie-breaks, one bound limit, and one positional
// return projection. Population/cohort roots, endpoint-lookup traversals,
// sets, subplans outside the registered REQUIRED shape, graph returns,
// group rows, pivots, unpivots, stage sequences, and ClickHouse payloads
// are capability gaps that return typed errors.
package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

// ErrUnsupportedPostgresPlan reports a physical plan outside the closed v1
// PostgreSQL render surface. Callers must surface it as a capability gap;
// the renderer never falls back to AQL and never converts AQL to SQL.
var ErrUnsupportedPostgresPlan = errors.New("postgres renderer: unsupported physical plan")

// ErrInvalidPostgresPlanScope reports a plan whose explicit scope binds are
// missing or contradictory (blank project, absent auth mode flags, or an
// unrestricted mode carrying paths). Scope is never inferred and never
// widened; the canonical mapping itself lives in scope.go.
var ErrInvalidPostgresPlanScope = errors.New("postgres renderer: invalid plan scope")

// renderer-owned filterable column policy: single-segment document paths
// that map onto typed columns of the resource table. Any other path is a
// JSONB payload path and is bound as one whole text-path parameter. Storage
// identity sequences (resource_seq, edge_seq) are reserved for
// renderer-generated ordering and are never user-addressable columns.
var resourceFilterColumns = map[string]bool{
	"project":            true,
	"dataset_generation": true,
	"resource_type":      true,
	"logical_id":         true,
	"auth_resource_path": true,
	"recorded_at":        true,
}

// RenderPhysicalPlan renders a validated physical plan onto the trusted
// PostgreSQL storage binding. The result is deterministic: the same plan and
// binding always yield byte-identical SQL and value-identical ordered
// arguments, and it never aliases plan data (every bound value is copied or
// re-encoded).
func RenderPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (compiler.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, err
	}
	// Full-backstop defense in depth (unnest.go contract): a plan that
	// mentions UNNEST is pinned to the registered slice as the first
	// plan-facing act here, before plan validation, scope loading, or any
	// rendering can reject with an off-type error, so the dispatch entry
	// enforces one contract with RenderUnnest, not a partial backstop.
	// The re-check at the first UNNEST in renderUnnest stays as the local
	// invariant; the check is total over any plan, so a malformed non-unnest
	// plan reaches plan.Validate untouched.
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			if err := validateUnnestPlanSurface(plan); err != nil {
				return compiler.RenderedPostgresPhysicalPlan{}, err
			}
			break
		}
	}
	if err := plan.Validate(); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if plan.StageSequence != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: staged row-operator sequences are not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: preview source windowing is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return compiler.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}

	renderer := &planRenderer{
		storage:    storage,
		rows:       map[string]rowScope{},
		parameters: map[string]int{},
	}
	if err := renderer.loadScope(plan); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, err
	}
	if err := renderer.renderOperations(plan); err != nil {
		return compiler.RenderedPostgresPhysicalPlan{}, err
	}
	sql := renderer.assemble()
	return compiler.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

// rowScope records the renderer-owned SQL placement of one plan variable.
// The plan-authored variable name is used only as an internal map key; it
// never reaches SQL text. optional marks a row materialized by an OPTIONAL
// traversal join (traversal.go): it is NULL-extensible, so main-sequence
// FILTER and SORT may not reference it, while RETURN projections read its
// columns with SQL NULL for parent-preserving rows.
//
// A derived scope (unnest.go) carries a renderer-computed SQL expression
// instead of a storage alias: derivedValue is the only reading, there is no
// table alias or resource type, and only resolveValue may read it, so a
// derived variable can never be used as a join, filter column, or sort
// column. derivedJSON marks a jsonb-valued derived row whose non-empty
// paths are document paths over the derived value.
type rowScope struct {
	alias        string
	resourceType string
	derivedValue string
	derivedJSON  bool
	optional     bool
}

type planRenderer struct {
	storage ir.PostgresStorageBinding
	args    []compiler.PostgreSQLParameter
	// parameters deduplicates identical scalar scope binds (project,
	// generation, resource types, label, auth array) so repeated predicates
	// reference one placeholder.
	parameters map[string]int
	rows       map[string]rowScope

	// scope is the re-read canonical scope established by loadScope (its
	// definition and predicate mapping live in scope.go).
	scope     planScope
	rootAlias string
	// edgeAliases records the lateral aliases of OPTIONAL traversal joins in
	// plan order; the assembler orders every one by its exposed edge_seq so
	// repeated occurrences are deterministic on every execution.
	edgeAliases []string
	// traversalSeq is the single alias counter shared by traversal.go: every
	// registered traversal join (lateral child subquery or EXISTS step)
	// consumes one sequence number, so edge/target/lateral aliases are
	// unique and deterministic for a plan.
	traversalSeq  int
	where         []string
	joins         []string
	selects       []string
	outputColumns []string
	orderBy       []string
	limitClause   string

	// Unnest state (unnest.go): lateral clauses in plan order, the rn
	// tie-break keys they contribute to the total order, and the expansion
	// counter that mints renderer-owned lateral aliases. All are
	// renderer-owned; no plan string reaches SQL text through them.
	laterals    []string
	unnestOrder []string
	unnestSeq   int
}

// addParameter appends one ordered argument and returns its $n index. A
// non-empty cacheKey deduplicates equal scalar bindings.
func (r *planRenderer) addParameter(parameter compiler.PostgreSQLParameter, cacheKey string) int {
	if cacheKey != "" {
		if index, ok := r.parameters[cacheKey]; ok {
			return index
		}
	}
	r.args = append(r.args, parameter)
	index := len(r.args)
	if cacheKey != "" {
		r.parameters[cacheKey] = index
	}
	return index
}

func (r *planRenderer) textParameter(value, cacheKey string) int {
	return r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterText, Value: value}, cacheKey)
}

func (r *planRenderer) jsonbParameter(raw json.RawMessage) int {
	return r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterJSONB, Value: append(json.RawMessage(nil), raw...)}, "")
}

func (r *planRenderer) renderOperations(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	phase := 0 // 0 = source ops, 1 = window ops, 2 = return terminal
	sawSort, sawLimit := false, false
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if phase != 0 || index != 0 {
				return fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalFilterOp:
			if phase != 0 {
				return fmt.Errorf("%w: filters must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalTraversalOp:
			if phase != 0 {
				return fmt.Errorf("%w: traversals must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderTraversal(plan, *operation.Traversal); err != nil {
				return err
			}
		case ir.PhysicalUnnestOp:
			if phase != 0 {
				return fmt.Errorf("%w: unnest must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if operation.Unnest == nil {
				return fmt.Errorf("%w: operation %d: unnest payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderUnnest(plan, *operation.Unnest); err != nil {
				return err
			}
		case ir.PhysicalSortOp:
			if sawSort || phase > 1 || (phase == 1 && sawLimit) {
				return fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderSort(*operation.Sort); err != nil {
				return err
			}
			sawSort, phase = true, 1
		case ir.PhysicalLimitOp:
			if sawLimit || phase > 1 {
				return fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderLimit(plan, *operation.Limit); err != nil {
				return err
			}
			sawLimit, phase = true, 1
		case ir.PhysicalReturnOp:
			if !last || phase > 1 {
				return fmt.Errorf("%w: RETURN must be the final operation", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderReturn(*operation.Return); err != nil {
				return err
			}
			phase = 2
		default:
			return fmt.Errorf("%w: operation %q is not part of the v1 PostgreSQL render surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if phase != 2 {
		return fmt.Errorf("%w: plan requires a RETURN terminal", ErrUnsupportedPostgresPlan)
	}
	return nil
}

func (r *planRenderer) renderRootScan(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	r.rootAlias = "r0"
	r.rows[scan.Variable] = rowScope{alias: r.rootAlias, resourceType: resourceType}
	r.where = append(r.where, r.scopedResourcePredicate(r.rootAlias,
		r.textParameter(resourceType, "text:"+resourceType)))
	return nil
}

// resolveValue maps one plan value onto either a trusted column expression
// or a parameterized JSONB payload path over its row alias. Payload paths
// are bound through documentPathExpression (null_semantics.go), the
// package's single path-binding authority, so extraction and comparison
// paths can never drift apart. Derived scopes (unnest.go) resolve through
// resolveDerivedValue against their renderer-computed expression instead;
// everything else resolves through the shared rows-parameterized resolver
// in traversal.go, which main sequence and EXISTS subplans share.
func (r *planRenderer) resolveValue(value ir.PhysicalValue) (string, error) {
	if scope, ok := r.rows[value.Variable]; ok && scope.derivedValue != "" {
		return r.resolveDerivedValue(value, scope)
	}
	return r.resolveValueInRows(r.rows, value)
}

// rejectOptionalRowReferences enforces the OPTIONAL-traversal boundary for
// main-sequence FILTER operations: no comparison leaf, at any depth of an
// ALL/NOT composition, may read a row materialized by an OPTIONAL
// traversal join. A WHERE conjunct over null-extended child rows re-adds
// the inner-join exclusion the AQL LET-set never applies (render/aql
// navigation_render.go:11-37), and NOT/IS-DISTINCT-FROM forms would keep
// parents an AQL required match excludes (lower/required_match.go).
// Required matching must use the typed EXISTS subplan form, which renders
// over INNER joins where WHERE placement is exact. EXISTS predicate
// subplans are self-scoped and walk-free: their own contract is enforced by
// renderRequiredMatchExists.
func (r *planRenderer) rejectOptionalRowReferences(filter ir.PhysicalFilter) error {
	if filter.Expression != nil {
		return rejectOptionalInPredicate(*filter.Expression, r.rows)
	}
	return rejectOptionalInPredicate(ir.PhysicalPredicateExpression{
		Kind:       ir.PhysicalComparisonPredicate,
		Comparison: &filter.Predicate,
	}, r.rows)
}

func rejectOptionalInPredicate(predicate ir.PhysicalPredicateExpression, rows map[string]rowScope) error {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate:
		if predicate.Comparison == nil {
			return nil // the comparison renderer reports the missing payload.
		}
		if predicate.Comparison.LeftExpression != nil &&
			predicate.Comparison.LeftExpression.Extract != nil {
			if scope, ok := rows[predicate.Comparison.LeftExpression.Extract.Source.Variable]; ok && scope.optional {
				return optionalRowRefError(predicate.Comparison.LeftExpression.Extract.Source.Variable)
			}
		}
		if predicate.Comparison.Left.BindKey == "" && predicate.Comparison.Left.Variable != "" {
			if scope, ok := rows[predicate.Comparison.Left.Variable]; ok && scope.optional {
				return optionalRowRefError(predicate.Comparison.Left.Variable)
			}
		}
		return nil
	case ir.PhysicalAllPredicate, ir.PhysicalAnyPredicate:
		for _, child := range predicate.Children {
			if err := rejectOptionalInPredicate(child, rows); err != nil {
				return err
			}
		}
		return nil
	case ir.PhysicalNotPredicate:
		for _, child := range predicate.Children {
			if err := rejectOptionalInPredicate(child, rows); err != nil {
				return err
			}
		}
		return nil
	default: // EXISTS and unknown kinds: no main-sequence row references.
		return nil
	}
}

func optionalRowRefError(variable string) error {
	return fmt.Errorf("%w: FILTER references %q, a row materialized by an OPTIONAL traversal LEFT JOIN LATERAL; a WHERE conjunct over null-extended child rows would re-add the inner-join exclusion the AQL LET-set never applies (render/aql/navigation_render.go renderTraversalSet). Express required matching as the typed EXISTS subplan FILTER form",
		ErrUnsupportedPostgresPlan, variable)
}

// payloadPathSegmentPattern re-checks the frozen ir path contract at the
// renderer boundary.
var payloadPathSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (r *planRenderer) renderFilter(plan ir.PhysicalPlan, filter ir.PhysicalFilter) (string, error) {
	if err := r.rejectOptionalRowReferences(filter); err != nil {
		return "", err
	}
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

// renderPredicateFragment renders one main-sequence predicate expression.
// COMPARISON/ALL/NOT delegate to the scoped renderer in traversal.go with
// the renderer's own row bindings; EXISTS is the REQUIRED match filter and
// is owned by the traversal slice (renderRequiredMatchExists). Nested
// EXISTS composition beyond one required match is refused there, keeping
// the required-semi-join surface depth-one closed.
func (r *planRenderer) renderPredicateFragment(plan ir.PhysicalPlan, predicate ir.PhysicalPredicateExpression) (predicateFragment, error) {
	switch predicate.Kind {
	case ir.PhysicalComparisonPredicate, ir.PhysicalNotPredicate, ir.PhysicalAllPredicate:
		return r.renderPredicateFragmentScoped(plan, predicate, r.rows, false)
	case ir.PhysicalExistsPredicate:
		return r.renderRequiredMatchExists(plan, predicate)
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
	// The main sequence never registers the subplan selector left side
	// (allowSelectorExtract=false), so the pre-pg-sql-06 refusals for
	// selector-expression and correlated predicates are unchanged.
	return r.renderComparisonFragmentScoped(plan, predicate, r.rows, false)
}

func isPayloadExpression(expression string) bool { return strings.Contains(expression, " #> $") }

// renderColumnComparison binds comparisons against typed columns. Values
// arrive in their canonical Go types; anything else fails closed rather
// than being stringified into the statement or silently coerced.
func (r *planRenderer) renderColumnComparison(left, operator string, value any) (predicateFragment, error) {
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
	switch {
	case strings.HasSuffix(left, "."+r.storage.Resource.MustColumn("recorded_at").SQLText()):
		timestamp, ok := value.(time.Time)
		if !ok {
			return predicateFragment{}, fmt.Errorf("%w: recorded_at comparisons require a time.Time bind value", ErrUnsupportedPostgresPlan)
		}
		index := r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterTimestamptz, Value: timestamp}, "")
		return predicateFragment{sql: left + " " + sqlComparison(operator) + " $" + strconv.Itoa(index)}, nil
	default:
		text, ok := value.(string)
		if !ok {
			return predicateFragment{}, fmt.Errorf("%w: typed column comparisons require a string bind value, got %T",
				ErrUnsupportedPostgresPlan, value)
		}
		return predicateFragment{sql: left + " " + sqlComparison(operator) + " $" + strconv.Itoa(r.textParameter(text, "text:"+text))}, nil
	}
}

// renderPayloadComparison binds JSONB path comparisons. The whole document
// path is one bound parameter and the value is one bound JSONB parameter,
// so paths and values never become SQL text. MISSING/EXISTS semantics keep
// present-but-JSON-null and missing indistinguishable, matching the AQL
// null-or-missing reading; SQL NULL, JSON null, and missing remain distinct
// in storage (migration 000002) and in this renderer they are only ever
// bound, never flattened.
func (r *planRenderer) renderPayloadComparison(left, operator string, value any) (predicateFragment, error) {
	if operator == "IN" {
		return predicateFragment{}, fmt.Errorf("%w: payload-path IN is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return predicateFragment{}, fmt.Errorf("%w: filter value %T is not JSON-encodable: %v", ErrUnsupportedPostgresPlan, value, err)
	}
	switch value.(type) {
	case string, bool, int, int64, float64, json.RawMessage:
	default:
		return predicateFragment{}, fmt.Errorf("%w: payload comparisons accept string, bool, number, or raw JSON values, got %T",
			ErrUnsupportedPostgresPlan, value)
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
	index := r.jsonbParameter(raw)
	if operator == "NOT_EQUALS" {
		return predicateFragment{sql: "(" + left + ") IS DISTINCT FROM ($" + strconv.Itoa(index) + "::jsonb)"}, nil
	}
	// The path extraction is SQL NULL for a missing key, so these comparisons
	// are three-valued; NOT over them must be guarded for AQL parity.
	return predicateFragment{sql: "(" + left + ") " + sqlComparison(operator) + " ($" + strconv.Itoa(index) + "::jsonb)", mayYieldNull: true}, nil
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

func (r *planRenderer) renderSort(sort ir.PhysicalSort) error {
	for _, key := range sort.Keys {
		scope, ok := r.rows[key.Variable]
		if !ok {
			return fmt.Errorf("%w: sort variable %q does not bind a readable row", ErrUnsupportedPostgresPlan, key.Variable)
		}
		if scope.optional {
			// NULL-extended child columns sort as a distinct witness a plan
			// never declared; deterministic row order belongs to the root
			// identity plus the renderer-owned edge witnesses.
			return fmt.Errorf("%w: sort key on %q, a row materialized by an OPTIONAL traversal LEFT JOIN LATERAL, is not registered; sorting expanded child rows is a later capability",
				ErrUnsupportedPostgresPlan, key.Variable)
		}
		if key.BindKey != "" || len(key.Path) != 1 || !resourceFilterColumns[key.Path[0]] {
			return fmt.Errorf("%w: sort keys must be single typed columns; payload-path sorting is not yet implemented",
				ErrUnsupportedPostgresPlan)
		}
		r.orderBy = append(r.orderBy, scope.alias+"."+r.storage.Resource.MustColumn(key.Path[0]).SQLText()+" ASC")
	}
	return nil
}

func (r *planRenderer) renderLimit(plan ir.PhysicalPlan, limit ir.PhysicalLimit) error {
	value, ok := plan.BindVars[limit.BindKey].(int)
	if !ok || value <= 0 {
		return fmt.Errorf("%w: limit bind %q must be a positive int", ErrUnsupportedPostgresPlan, limit.BindKey)
	}
	index := r.addParameter(compiler.PostgreSQLParameter{Type: compiler.PostgreSQLParameterBigint, Value: int64(value)}, "")
	r.limitClause = " LIMIT $" + strconv.Itoa(index)
	return nil
}

// renderReturn assembles the positional projection list. Every projection
// first passes classifyExtractionProjections (selectors.go) — the single
// projection-contract definition, applied with the renderer's own row
// bindings — and is then emitted from its classification: direct values and
// VALUE expressions through resolveValue, selector extractions and presence
// sidecars through the canonical mappings in null_semantics.go.
func (r *planRenderer) renderReturn(ret ir.PhysicalReturn) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: RETURN requires at least one projection", ErrUnsupportedPostgresPlan)
	}
	classified, err := classifyExtractionProjections(ret.Projections, func(variable string) (string, bool) {
		scope, ok := r.rows[variable]
		return scope.resourceType, ok
	})
	if err != nil {
		return err
	}
	for _, projection := range classified {
		var expression string
		switch projection.kind {
		case extractionDirectValue, extractionValueExpression:
			value := projection.projection.Value
			if projection.kind == extractionValueExpression {
				value = *projection.projection.Expression.Value
			}
			resolved, err := r.resolveValue(value)
			if err != nil {
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = resolved
			if isPayloadExpression(expression) {
				expression = "(" + expression + ")"
			}
		case extractionSelector:
			scope := r.rows[projection.expression.Extract.Source.Variable]
			value, err := r.scalarExtractValueExpression(scope.alias, projection.path, projection.expression.NullBehavior)
			if err != nil {
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionPresenceSidecar:
			scope := r.rows[projection.presence.Source.Variable]
			presence, err := r.propertyPresenceExpression(scope.alias, projection.presencePath)
			if err != nil {
				return fmt.Errorf("presence projection %q: %w", projection.projection.Name, err)
			}
			expression = presence
		}
		r.selects = append(r.selects, expression+` AS "c`+strconv.Itoa(len(r.selects))+`"`)
		r.outputColumns = append(r.outputColumns, projection.projection.Name)
	}
	return nil
}

func (r *planRenderer) assemble() string {
	var builder strings.Builder
	builder.WriteString("SELECT ")
	builder.WriteString(strings.Join(r.selects, ", "))
	builder.WriteString(" FROM " + r.storage.Resource.Identifier().SQLText() + " AS " + r.rootAlias)
	for _, join := range r.joins {
		builder.WriteString(" " + join)
	}
	// Lateral expansions trail the storage joins so each references only
	// rows that already bound (unnest.go mints them in plan order).
	for _, lateral := range r.laterals {
		builder.WriteString(" " + lateral)
	}
	if len(r.where) > 0 {
		builder.WriteString(" WHERE " + strings.Join(r.where, " AND "))
	}
	// Deterministic total order: plan sort keys first, then the storage
	// identities so repeated rows from parallel edge occurrences order the
	// same way on every execution, then each unnest expansion's rn so
	// duplicate items keep their source-array position (the ordering
	// counterpart of ir.PhysicalUnnestSortKeys' has-item/ordinality
	// tie-break).
	order := append(append([]string{}, r.orderBy...), r.rootAlias+"."+r.storage.Resource.MustColumn("resource_seq").SQLText()+" ASC")
	for _, edgeAlias := range r.edgeAliases {
		order = append(order, edgeAlias+"."+r.storage.Edge.MustColumn("edge_seq").SQLText()+" ASC")
	}
	order = append(order, r.unnestOrder...)
	builder.WriteString(" ORDER BY " + strings.Join(order, ", "))
	builder.WriteString(r.limitClause)
	return builder.String()
}
