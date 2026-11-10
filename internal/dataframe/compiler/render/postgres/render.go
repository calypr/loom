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
// generation, and an explicit auth mode), scalar filter comparisons —
// direct value comparisons over typed columns or JSONB payload paths with
// typed (spec value-kind-checked) bind values, and the closed
// selector-extraction comparisons whose ANY/NONE/ALL quantifier and
// null/missing reading preserve the physical-predicate contract (all
// predicate rendering and its capability registration live in
// predicates.go) — boolean ALL/ANY/NOT predicate trees, depth-one typed
// edge-occurrence joins, one column sort with deterministic identity
// tie-breaks, one bound limit, and one positional return projection.
// Population/cohort roots, endpoint-lookup traversals, repeated/indexed
// selector predicates, correlated predicates, CONTAINS_TEXT, sets, unnest,
// subplans, graph returns, group rows, pivots, unpivots, stage sequences,
// and ClickHouse payloads are capability gaps that return typed errors.
package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

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
// never reaches SQL text.
type rowScope struct {
	alias        string
	resourceType string
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
	scope         planScope
	rootAlias     string
	edgeAliases   []string
	where         []string
	joins         []string
	selects       []string
	outputColumns []string
	orderBy       []string
	limitClause   string
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

func (r *planRenderer) renderTraversal(plan ir.PhysicalPlan, traversal ir.PhysicalTraversal) error {
	source, ok := r.rows[traversal.SourceVariable]
	if !ok {
		return fmt.Errorf("%w: traversal source variable %q does not bind a readable row (edge variables and set variables are not readable)",
			ErrUnsupportedPostgresPlan, traversal.SourceVariable)
	}
	switch traversal.Direction {
	case ir.PhysicalOutbound, ir.PhysicalInbound:
	case ir.PhysicalAny:
		return fmt.Errorf("%w: ANY-direction traversal has no proven PostgreSQL contract", ErrUnsupportedPostgresPlan)
	default:
		return fmt.Errorf("%w: unknown traversal direction %q", ErrUnsupportedPostgresPlan, string(traversal.Direction))
	}
	if traversal.Strategy == ir.PhysicalTraversalEndpointLookup {
		return fmt.Errorf("%w: endpoint-lookup strategy has no proven PostgreSQL contract yet; native typed-identity joins are the only registered shape",
			ErrUnsupportedPostgresPlan)
	}
	if traversal.Strategy != "" && traversal.Strategy != ir.PhysicalTraversalNative {
		return fmt.Errorf("%w: unknown traversal strategy %q", ErrUnsupportedPostgresPlan, string(traversal.Strategy))
	}
	// The same direction-aware discriminator proof lower/storage_route.go
	// requires of Arango routes: without it the typed-identity join cannot be
	// attributed to a proven storage direction.
	expectedField := "to_type"
	if traversal.Direction == ir.PhysicalInbound {
		expectedField = "from_type"
	}
	if traversal.EdgeTargetTypeField != expectedField {
		return fmt.Errorf("%w: %s traversal must carry EdgeTargetTypeField %q, got %q",
			ErrUnsupportedPostgresPlan, string(traversal.Direction), expectedField, traversal.EdgeTargetTypeField)
	}
	label, ok := plan.BindVars[traversal.EdgeLabelBindKey].(string)
	if !ok || strings.TrimSpace(label) == "" {
		return fmt.Errorf("%w: edge label bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, traversal.EdgeLabelBindKey)
	}
	targetType, ok := plan.BindVars[traversal.TargetTypeBindKey].(string)
	if !ok || strings.TrimSpace(targetType) == "" {
		return fmt.Errorf("%w: traversal target type bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, traversal.TargetTypeBindKey)
	}
	if traversal.SourceTypeBindKey != "" {
		if bound, ok := plan.BindVars[traversal.SourceTypeBindKey].(string); !ok || bound != source.resourceType {
			return fmt.Errorf("%w: traversal source type bind does not match the current row resource type %q",
				ErrUnsupportedPostgresPlan, source.resourceType)
		}
	}

	edgeAlias := "e" + strconv.Itoa(len(r.edgeAliases)+1)
	targetAlias := "r" + strconv.Itoa(len(r.edgeAliases)+1)
	edgeTable := r.storage.Edge.Identifier().SQLText()
	resourceTable := r.storage.Resource.Identifier().SQLText()

	edgeColumns := r.storage.Edge
	// The edge row's canonical project/generation/auth scope comes from the
	// single mapping in scope.go; the label and endpoint identity clauses
	// appended below are traversal payload, not canonical scope.
	edgeScope := r.scopedEdgePredicate(edgeAlias)
	edgeScope += " AND " + edgeAlias + "." + edgeColumns.MustColumn("label").SQLText() +
		" = $" + strconv.Itoa(r.textParameter(label, "text:"+label))

	// The reached endpoint is the one whose typed identity the join carries
	// toward the resource table; the anchored endpoint equality preserves the
	// exact source row identity on the other side.
	var anchoredTypeColumn, anchoredIDColumn, reachedTypeColumn, reachedIDColumn string
	if traversal.Direction == ir.PhysicalOutbound {
		anchoredTypeColumn, anchoredIDColumn = "source_resource_type", "source_logical_id"
		reachedTypeColumn, reachedIDColumn = "target_resource_type", "target_logical_id"
	} else {
		anchoredTypeColumn, anchoredIDColumn = "target_resource_type", "target_logical_id"
		reachedTypeColumn, reachedIDColumn = "source_resource_type", "source_logical_id"
	}
	sourceTypeIndex := r.textParameter(source.resourceType, "text:"+source.resourceType)
	targetTypeIndex := r.textParameter(targetType, "text:"+targetType)
	edgeScope += " AND " + edgeAlias + "." + edgeColumns.MustColumn(anchoredTypeColumn).SQLText() + " = $" + strconv.Itoa(sourceTypeIndex)
	edgeScope += " AND " + edgeAlias + "." + edgeColumns.MustColumn(anchoredIDColumn).SQLText() +
		" = " + source.alias + "." + r.storage.Resource.MustColumn("logical_id").SQLText()
	edgeScope += " AND " + edgeAlias + "." + edgeColumns.MustColumn(reachedTypeColumn).SQLText() + " = $" + strconv.Itoa(targetTypeIndex)

	join := "INNER JOIN " + edgeTable + " AS " + edgeAlias + " ON " + edgeScope
	join += " INNER JOIN " + resourceTable + " AS " + targetAlias + " ON " +
		r.scopedResourcePredicate(targetAlias, targetTypeIndex) +
		" AND " + targetAlias + "." + r.storage.Resource.MustColumn("logical_id").SQLText() +
		" = " + edgeAlias + "." + edgeColumns.MustColumn(reachedIDColumn).SQLText()
	r.joins = append(r.joins, join)
	r.edgeAliases = append(r.edgeAliases, edgeAlias)
	r.rows[traversal.TargetVariable] = rowScope{alias: targetAlias, resourceType: targetType}
	return nil
}

// resolveValue maps one plan value onto either a trusted column expression
// or a parameterized JSONB payload path over its row alias. Payload paths
// are bound through documentPathExpression (null_semantics.go), the
// package's single path-binding authority, so extraction and comparison
// paths can never drift apart.
func (r *planRenderer) resolveValue(value ir.PhysicalValue) (string, error) {
	scope, ok := r.rows[value.Variable]
	if !ok {
		return "", fmt.Errorf("%w: variable %q does not bind a readable row", ErrUnsupportedPostgresPlan, value.Variable)
	}
	if len(value.Path) == 1 && resourceFilterColumns[value.Path[0]] {
		return scope.alias + "." + r.storage.Resource.MustColumn(value.Path[0]).SQLText(), nil
	}
	return r.documentPathExpression(scope.alias, value.Path)
}

// payloadPathSegmentPattern re-checks the frozen ir path contract at the
// renderer boundary.
var payloadPathSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (r *planRenderer) renderSort(sort ir.PhysicalSort) error {
	for _, key := range sort.Keys {
		scope, ok := r.rows[key.Variable]
		if !ok {
			return fmt.Errorf("%w: sort variable %q does not bind a readable row", ErrUnsupportedPostgresPlan, key.Variable)
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
	if len(r.where) > 0 {
		builder.WriteString(" WHERE " + strings.Join(r.where, " AND "))
	}
	// Deterministic total order: plan sort keys first, then the storage
	// identities so repeated rows from parallel edge occurrences order the
	// same way on every execution.
	order := append(append([]string{}, r.orderBy...), r.rootAlias+"."+r.storage.Resource.MustColumn("resource_seq").SQLText()+" ASC")
	for _, edgeAlias := range r.edgeAliases {
		order = append(order, edgeAlias+"."+r.storage.Edge.MustColumn("edge_seq").SQLText()+" ASC")
	}
	builder.WriteString(" ORDER BY " + strings.Join(order, ", "))
	builder.WriteString(r.limitClause)
	return builder.String()
}
