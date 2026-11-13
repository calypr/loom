package postgres

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

var ErrUnsupportedPostgresPlan = errors.New("postgres renderer: unsupported physical plan")

var ErrInvalidPostgresPlanScope = errors.New("postgres renderer: invalid plan scope")

func canonicalKeyColumn(path string) string {
	switch path {
	case "_key", "id":
		return "logical_id"
	default:
		return path
	}
}

var resourceFilterColumns = map[string]bool{
	"project":            true,
	"dataset_generation": true,
	"resource_type":      true,
	"logical_id":         true,
	"auth_resource_path": true,
	"recorded_at":        true,
}

func RenderPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if cellTraceTerminalMentions(plan) {
		return renderCellTracePhysicalPlan(plan, storage)
	}
	if graphPlanMentionsPathOperations(plan) {
		return renderGraphPhysicalPlan(plan, storage)
	}
	if rowLineageTerminalMentions(plan) {
		return renderRowLineagePhysicalPlan(plan, storage)
	}
	if stageSequenceCompositionMentions(plan) {
		return renderStageSequencePhysicalPlan(plan, storage)
	}
	if codedGroupPlanMentionsStage(plan) {
		return renderCodedGroupPhysicalPlan(plan, storage)
	}
	if stageSequenceRelatedOccurrenceMentions(plan) {
		return renderStageSequencePhysicalPlan(plan, storage)
	}
	if groupPlanMentionsStage(plan) {
		return renderGroupPhysicalPlan(plan, storage)
	}
	if groupedPivotStagePlanMentionsSurface(plan) {
		return renderGroupedCodedPivotPhysicalPlan(plan, storage)
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnnestOp {
			if err := validateUnnestPlanSurface(plan); err != nil {
				return command.RenderedPostgresPhysicalPlan{}, err
			}
			break
		}
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalUnpivotOp {
			if err := validateUnpivotPlanSurface(plan); err != nil {
				return command.RenderedPostgresPhysicalPlan{}, err
			}
			break
		}
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalGroupedPivotOp {
			return renderGroupedPivotPhysicalPlan(plan, storage)
		}
	}
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalTableShapeExclusionReturnOp {
			return renderTableShapeExclusionPhysicalPlan(plan, storage)
		}
	}
	if plan.StageSequence != nil {
		return renderStageSequencePhysicalPlan(plan, storage)
	}
	if err := plan.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf("validate physical plan: %w", err)
	}
	if plan.Engine != "" && plan.Engine != ir.PhysicalEngineAQL {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: engine %q has no PostgreSQL source; the source graph IR is backend-neutral and ClickHouse plans are not PostgreSQL-rendered",
			ErrUnsupportedPostgresPlan, string(plan.Engine))
	}
	if plan.ClickHouseCombine != nil || plan.ClickHousePrefix != nil {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: ClickHouse combine/prefix payloads have no PostgreSQL rendering", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: preview source windowing is not yet implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(plan.DeferredExpressionLets) != 0 {
		return command.RenderedPostgresPhysicalPlan{}, fmt.Errorf(
			"%w: completed plans must not carry deferred expression lets", ErrUnsupportedPostgresPlan)
	}

	renderer := &planRenderer{
		storage:           storage,
		rows:              map[string]rowScope{},
		parameters:        map[string]int{},
		canonicalAuthLets: map[string]string{},
	}
	if err := renderer.loadScope(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := renderer.renderOperations(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	sql := renderer.assemble()
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

type rowScope struct {
	alias        string
	resourceType string
	derivedValue string
	derivedJSON  bool
	optional     bool
}

type planRenderer struct {
	storage           ir.PostgresStorageBinding
	args              []command.PostgreSQLParameter
	parameters        map[string]int
	rows              map[string]rowScope
	canonicalAuthLets map[string]string

	stageRows map[string]stageRowScope

	scope         planScope
	rootAlias     string
	edgeAliases   []string
	traversalSeq  int
	where         []string
	joins         []string
	selects       []string
	outputColumns []string

	window executionWindow

	laterals       []string
	lateralAliases []string
	unnestSeq      int

	unpivotSeq     int
	unpivotOutputs map[string]map[string]string

	membership *PostgresPopulationRootBinding

	mapping *populationMappingRender

	rootVariable   string
	sliceSeq       int
	setBodies      map[string]correlatedSetBody
	setProjections map[string][]ir.PhysicalSetProjectionField
}

func (r *planRenderer) addParameter(parameter command.PostgreSQLParameter, cacheKey string) int {
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
	return r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterText, Value: value}, cacheKey)
}

func (r *planRenderer) jsonbParameter(raw json.RawMessage) int {
	return r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterJSONB, Value: append(json.RawMessage(nil), raw...)}, "")
}

func (r *planRenderer) renderOperations(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	phase := 0
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
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return err
			}
			if canonical {
				continue
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalDerivedLetOp:
			if phase != 0 {
				return fmt.Errorf("%w: derived lets must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return err
			}
			if canonical {
				continue
			}
			return fmt.Errorf("%w: operation %q is not part of the v1 PostgreSQL render surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		case ir.PhysicalTraversalOp:
			if phase != 0 {
				return fmt.Errorf("%w: traversals must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderTraversal(plan, *operation.Traversal, false); err != nil {
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
		case ir.PhysicalSetOp:
			if phase != 0 {
				return fmt.Errorf("%w: sets must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if operation.Set == nil {
				return fmt.Errorf("%w: operation %d: set payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderSet(plan, *operation.Set); err != nil {
				return err
			}
		case ir.PhysicalUnpivotOp:
			if phase != 0 {
				return fmt.Errorf("%w: unpivot must precede the execution window", ErrUnsupportedPostgresPlan)
			}
			if operation.Unpivot == nil {
				return fmt.Errorf("%w: operation %d: unpivot payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderUnpivot(plan, *operation.Unpivot); err != nil {
				return err
			}
		case ir.PhysicalSortOp:
			if sawSort || phase > 1 || (phase == 1 && sawLimit) {
				return fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return err
			}
			sawSort, phase = true, 1
		case ir.PhysicalLimitOp:
			if sawLimit || phase > 1 {
				return fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return err
			}
			sawLimit, phase = true, 1
		case ir.PhysicalReturnOp:
			if !last || phase > 1 {
				return fmt.Errorf("%w: RETURN must be the final operation", ErrUnsupportedPostgresPlan)
			}
			if err := r.renderReturn(plan, *operation.Return); err != nil {
				return err
			}
			phase = 2
		case ir.PhysicalPopulationMappingReturnOp:
			if r.mapping == nil {
				return fmt.Errorf("%w: operation %q is not part of the v1 PostgreSQL render surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
			if !last || phase > 1 {
				return fmt.Errorf("%w: POPULATION_MAPPING_RETURN must be the single final operation", ErrUnsupportedPostgresPlan)
			}
			if operation.PopulationMappingReturn == nil {
				return fmt.Errorf("%w: operation %d: population-mapping payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := r.renderPopulationMappingTerminal(plan, *operation.PopulationMappingReturn); err != nil {
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
	if scan.Population != nil || scan.CohortSource != nil {
		return r.renderRootSources(plan, scan)
	}
	if scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not yet implemented for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	}
	resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string)
	if !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	r.rootAlias = "r0"
	r.rootVariable = scan.Variable
	r.rows[scan.Variable] = rowScope{alias: r.rootAlias, resourceType: resourceType}
	r.where = append(r.where, r.scopedResourcePredicate(r.rootAlias,
		r.textParameter(resourceType, "text:"+resourceType)))
	return nil
}

func (r *planRenderer) resolveValue(value ir.PhysicalValue) (string, error) {
	if scope, ok := r.stageRows[value.Variable]; ok {
		return r.resolveStageRowValue(scope, value)
	}
	if columns, ok := r.unpivotOutputs[value.Variable]; ok {
		return r.resolveUnpivotOutputValue(columns, value)
	}
	if scope, ok := r.rows[value.Variable]; ok && scope.derivedValue != "" {
		return r.resolveDerivedValue(value, scope)
	}
	return r.resolveValueInRows(r.rows, value)
}

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
			return nil
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
	default:
		return nil
	}
}

func optionalRowRefError(variable string) error {
	return fmt.Errorf("%w: FILTER references %q, a row materialized by an OPTIONAL traversal LEFT JOIN LATERAL; a WHERE conjunct over null-extended child rows would re-add the inner-join exclusion the AQL LET-set never applies (render/aql/navigation_render.go renderTraversalSet). Express required matching as the typed EXISTS subplan FILTER form",
		ErrUnsupportedPostgresPlan, variable)
}

var payloadPathSegmentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (r *planRenderer) renderReturn(plan ir.PhysicalPlan, ret ir.PhysicalReturn) error {
	if r.unpivotOutputs != nil {
		return r.renderUnpivotReturn(ret)
	}
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
		case extractionCallExpression:
			value, err := r.renderCallProjection(plan, projection.projection)
			if err != nil {
				return err
			}
			expression = value
		case extractionObject:
			value, err := r.renderObjectValue(plan, r.rows, *projection.projection.Expression, 1)
			if err != nil {
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionSlice:
			value, err := r.renderSliceValue(plan, r.rows, *projection.projection.Expression, 1)
			if err != nil {
				return fmt.Errorf("projection %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionUnitNormalized:
			value, err := r.renderUnitNormalizedProjection(projection)
			if err != nil {
				return err
			}
			expression = value
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
	for _, lateral := range r.laterals {
		builder.WriteString(" " + lateral)
	}
	if len(r.where) > 0 {
		builder.WriteString(" WHERE " + strings.Join(r.where, " AND "))
	}
	builder.WriteString(" ORDER BY " + strings.Join(r.windowOrder(), ", "))
	builder.WriteString(r.window.limitClause)
	return builder.String()
}
