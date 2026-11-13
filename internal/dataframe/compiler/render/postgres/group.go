package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const groupRowIDColumn = "__loom_row_id"

const (
	groupMissingKeyTag       = "CONSTRUCTION_GROUP_MISSING_KEY"
	groupKeyTypeTag          = "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH"
	groupValueTypeTag        = "CONSTRUCTION_GROUP_VALUE_TYPE_MISMATCH"
	groupRowValueMultipleTag = "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES"
)

const groupTrimCharacters = " "

func groupPlanMentionsStage(plan ir.PhysicalPlan) bool {
	if plan.StageSequence == nil {
		return false
	}
	for _, stage := range plan.StageSequence.Stages {
		if stage.Kind == ir.PhysicalStageGroupOp {
			return true
		}
	}
	return false
}

func renderGroupPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateGroupPlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
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
	sql, err := renderer.renderGroupStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

func validateGroupPlanSurface(plan ir.PhysicalPlan) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("%w: group dispatch requires a stage sequence", ErrUnsupportedPostgresPlan)
	}
	sequence := plan.StageSequence
	if len(sequence.Stages) != 1 {
		return fmt.Errorf("%w: the registered group slice admits exactly one GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap (L-13.1)",
			ErrUnsupportedPostgresPlan)
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageGroupOp || stage.Group == nil {
		return fmt.Errorf("%w: operation %q is not part of the registered group slice",
			ErrUnsupportedPostgresPlan, string(stage.Kind))
	}
	if stage.CodedGroup != nil || stage.CohortGroup != nil || stage.Filter != nil || stage.Expand != nil ||
		stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil ||
		stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%w: GROUP requires only a group payload (ir/physical_stage_validation.go:187 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	if stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID {
		return fmt.Errorf("%w: the registered slice admits only the inline first-stage GROUP (InputStageID must equal %q); composed sequences are a pg-sql-16 capability gap (L-13.1)",
			ErrUnsupportedPostgresPlan, sequence.SourceStageID)
	}
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return fmt.Errorf("%w: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38)",
			ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		return fmt.Errorf("%w: preview source windowing and the terminal Pivot window require Pivot terminals and are not registered for GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: group preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	returnNames, err := validateGroupOperations(plan)
	if err != nil {
		return err
	}
	if err := validateGroupPayload(stage, plan.BindVars, returnNames); err != nil {
		return err
	}
	return validateGroupSemanticKey(plan, stage)
}

func validateGroupPayload(stage ir.PhysicalConstructionStage, bindVars map[string]any, returnNames map[string]bool) error {
	group := stage.Group
	if value, ok := bindVars[group.ConstructionIDBindKey].(string); !ok || strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: group construction identity bind %q must be a non-empty string",
			ErrUnsupportedPostgresPlan, group.ConstructionIDBindKey)
	}
	switch group.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup, ir.PhysicalStageGroupMissingKeyExclude, ir.PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("%w: unsupported missing-key policy %q (construction_group.go:131 parity)",
			ErrUnsupportedPostgresPlan, string(group.MissingKeyPolicy))
	}
	if group.SemanticKey != nil && group.CodingKey != nil {
		return fmt.Errorf("%w: Group supports at most one coded identity key (ir/physical_stage_validation.go:1475 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	if group.RelatedOccurrence != nil {
		return fmt.Errorf("%w: the related-occurrence Group adapter is a pg-sql-16 capability gap (L-13.5; correlated-hop child-set contracts) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.RootContributorInputColumn != "" {
		return fmt.Errorf("%w: the root-contributor set is production-unreachable through the PostgreSQL compile target (L-13.6) and is refused, never partially rendered",
			ErrUnsupportedPostgresPlan)
	}
	if group.GroupRowsVariable == "" || group.IdentityVariable == "" {
		return fmt.Errorf("%w: group plan variables must be compiled and non-empty", ErrUnsupportedPostgresPlan)
	}
	for index, key := range group.Keys {
		switch key.Kind {
		case "STRING", "INTEGER", "DECIMAL", "BOOLEAN":
		default:
			return fmt.Errorf("%w: group key %d has unsupported scalar kind %q",
				ErrUnsupportedPostgresPlan, index, key.Kind)
		}
		if key.InputColumn == "" || key.OutputColumn == "" || key.Variable == "" {
			return fmt.Errorf("%w: group key %d must be fully compiled (input, output, variable)",
				ErrUnsupportedPostgresPlan, index)
		}
		if key.InputInternal || key.FieldSource != nil {
			return fmt.Errorf("%w: group key %d uses a hidden or field-sourced input surface, which the public-scalar lowering gate never emits (L-13.11)",
				ErrUnsupportedPostgresPlan, index)
		}
		if !returnNames[key.InputColumn] {
			return fmt.Errorf("%w: group key %d: group input column %q is not among the source projection names (L-13.11)",
				ErrUnsupportedPostgresPlan, index, key.InputColumn)
		}
	}
	keyless := len(group.Keys) == 0 && group.SemanticKey == nil
	for index, aggregate := range group.Aggregates {
		switch aggregate.Operation {
		case "COUNT_ROWS", "COUNT_NON_NULL", "COUNT_DISTINCT", "MIN", "MAX", "SUM", "MEAN":
		default:
			return fmt.Errorf("%w: aggregate %d: unsupported aggregate operation %q (construction_group.go:807-808 parity)",
				ErrUnsupportedPostgresPlan, index, string(aggregate.Operation))
		}
		if aggregate.Operation == "COUNT_ROWS" {
			if aggregate.InputColumn != "" {
				return fmt.Errorf("%w: aggregate %d COUNT_ROWS must not have an input column", ErrUnsupportedPostgresPlan, index)
			}
		} else if aggregate.InputColumn == "" {
			return fmt.Errorf("%w: aggregate %d %s requires an input column",
				ErrUnsupportedPostgresPlan, index, string(aggregate.Operation))
		}
		if aggregate.Output == "" || aggregate.Variable == "" {
			return fmt.Errorf("%w: aggregate %d must be fully compiled (output, variable)", ErrUnsupportedPostgresPlan, index)
		}
		if aggregate.InputColumn != "" && !returnNames[aggregate.InputColumn] {
			return fmt.Errorf("%w: aggregate %d: group input column %q is not among the source projection names (L-13.11)",
				ErrUnsupportedPostgresPlan, index, aggregate.InputColumn)
		}
	}
	if keyless {
		if len(group.Aggregates) != 1 || group.Aggregates[0].Operation != "COUNT_ROWS" {
			return fmt.Errorf("%w: only keyless COUNT_ROWS groups can consume the source scope (construction_group.go:44-52 re-read at the door)",
				ErrUnsupportedPostgresPlan)
		}
	}
	for index, rowValue := range group.RowValues {
		switch rowValue.Policy {
		case "ALL", "ONE":
		default:
			return fmt.Errorf("%w: row value %q has unsupported policy %q",
				ErrUnsupportedPostgresPlan, rowValue.Output, rowValue.Policy)
		}
		if rowValue.InputColumn == "" || rowValue.Output == "" || rowValue.Variable == "" {
			return fmt.Errorf("%w: row value %d must be fully compiled (input, output, variable)",
				ErrUnsupportedPostgresPlan, index)
		}
		if rowValue.FieldSource != nil {
			return fmt.Errorf("%w: row value %d uses a field-sourced input surface, which the public-scalar lowering gate never emits (L-13.11)",
				ErrUnsupportedPostgresPlan, index)
		}
		if !returnNames[rowValue.InputColumn] {
			return fmt.Errorf("%w: row value %d: group input column %q is not among the source projection names (L-13.11)",
				ErrUnsupportedPostgresPlan, index, rowValue.InputColumn)
		}
	}
	if group.CodingKey != nil {
		return fmt.Errorf("%w: the CODING_IDENTITY Group key is refused, never partially rendered (L-13.4: route-sourced keys name pg-sql-16; the local-array-expansion key is wave 2 of this ticket)",
			ErrUnsupportedPostgresPlan)
	}
	return nil
}

func validateGroupOperations(plan ir.PhysicalPlan) (map[string]bool, error) {
	if len(plan.Operations) == 0 {
		return nil, fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return nil, fmt.Errorf("%w: the group source requires exactly one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	scan := first.RootScan
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return nil, fmt.Errorf("%w: population, cohort, and candidate-seed roots are bound only by RenderPopulationRootScan (population.go); the plain spine refuses them",
			ErrUnsupportedPostgresPlan)
	}
	sawSort, sawLimit, sawReturn := false, false, false
	var returnNames map[string]bool
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return nil, fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp:
			if sawSort || sawLimit || sawReturn {
				return nil, fmt.Errorf("%w: source operations must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Kind == ir.PhysicalFilterOp && operation.Filter == nil {
				return nil, fmt.Errorf("%w: operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet == nil {
				return nil, fmt.Errorf("%w: operation %d: derived-let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalUnnestOp:
			return nil, fmt.Errorf("%w: operation %q is not part of the registered group source surface (L-13.9)",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		case ir.PhysicalSetOp:
			return nil, fmt.Errorf("%w: group source-region SET is a pg-sql-16 capability gap (no PostgreSQL contract for lowering-emitted child-set subplans; L-13.2) and is refused, never partially rendered",
				ErrUnsupportedPostgresPlan)
		case ir.PhysicalSortOp:
			if sawSort || sawLimit || sawReturn {
				return nil, fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Sort == nil {
				return nil, fmt.Errorf("%w: operation %d: sort payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if sawLimit || sawReturn {
				return nil, fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Limit == nil {
				return nil, fmt.Errorf("%w: operation %d: limit payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			if !last || operation.Return == nil || len(operation.Return.Projections) == 0 {
				return nil, fmt.Errorf("%w: the group source requires a terminal RETURN with its projection list",
					ErrUnsupportedPostgresPlan)
			}
			sawReturn = true
			returnNames = map[string]bool{}
			for _, projection := range operation.Return.Projections {
				if projection.Name == groupSemanticCarrierName {
					return nil, fmt.Errorf("%w: source projection name %q is reserved for the renderer's semantic carrier column",
						ErrUnsupportedPostgresPlan, projection.Name)
				}
				returnNames[projection.Name] = true
			}
		default:
			return nil, fmt.Errorf("%w: operation %q is not part of the registered group source surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawReturn {
		return nil, fmt.Errorf("%w: the group source requires a terminal RETURN", ErrUnsupportedPostgresPlan)
	}
	return returnNames, nil
}

type groupColumn struct {
	name  string
	alias string
	expr  string
	ref   string
	jsonb bool
}

func (r *planRenderer) renderGroupStatement(plan ir.PhysicalPlan) (string, error) {
	stage := plan.StageSequence.Stages[0]

	if err := r.renderRootScan(plan, *plan.Operations[0].RootScan); err != nil {
		return "", err
	}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalFilterOp:
			canonical, err := r.acceptCanonicalScopeFilter(operation.Filter)
			if err != nil {
				return "", err
			}
			if canonical {
				continue
			}
			clause, err := r.renderFilter(plan, *operation.Filter)
			if err != nil {
				return "", err
			}
			r.where = append(r.where, clause)
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if !canonical {
				return "", fmt.Errorf("%w: operation %q is not part of the registered group source surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
		}
	}
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalSortOp:
			if err := r.renderWindowSort(plan, *operation.Sort); err != nil {
				return "", err
			}
		case ir.PhysicalLimitOp:
			if err := r.renderWindowLimit(plan, *operation.Limit); err != nil {
				return "", err
			}
		}
	}

	var returnOp *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalReturnOp {
			returnOp = operation.Return
		}
	}
	columns, err := r.materializeGroupSource(plan, returnOp)
	if err != nil {
		return "", err
	}
	if stage.Group.SemanticKey != nil {
		carrier, err := r.groupSemanticCarrierColumn(stage.Group.SemanticKey)
		if err != nil {
			return "", err
		}
		columns = append(columns, *carrier)
	}
	resource := r.storage.Resource
	selectList := make([]string, 0, len(columns))
	for i := range columns {
		selectList = append(selectList, columns[i].expr+` AS "`+columns[i].alias+`"`)
	}
	source := "SELECT " + strings.Join(selectList, ", ") +
		" FROM " + resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		source += " " + join
	}
	for _, lateral := range r.laterals {
		source += " " + lateral
	}
	if len(r.where) > 0 {
		source += " WHERE " + strings.Join(r.where, " AND ")
	}
	source += " ORDER BY " + strings.Join(r.windowOrder(), ", ") + r.window.limitClause

	region, err := r.renderGroupAggregateRegion(plan, stage, columns, source, "g_",
		plan.StageSequence.FinalColumns, groupRowIDColumn)
	if err != nil {
		return "", err
	}
	r.outputColumns = region.names
	final := region.final + " ORDER BY " + strings.Join(region.order, ", ")
	if plan.StageSequence.PreviewLimitBindKey != "" {
		limit := plan.BindVars[plan.StageSequence.PreviewLimitBindKey].(int)
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		final += " LIMIT $" + strconv.Itoa(index)
	}
	return "WITH " + strings.Join(region.ctes, ", ") + " " + final, nil
}

type groupRegionResult struct {
	ctes        []string
	final       string
	order       []string
	names       []string
	keyCollates []string
}

func (r *planRenderer) renderGroupAggregateRegion(
	plan ir.PhysicalPlan,
	stage ir.PhysicalConstructionStage,
	columns []groupColumn,
	sourceText string,
	prefix string,
	outputColumns []ir.PhysicalStageColumn,
	identityColumn string,
) (groupRegionResult, error) {
	group := stage.Group
	nameToColumn := map[string]*groupColumn{}
	var semanticCarrier *groupColumn
	for i := range columns {
		if columns[i].name == groupSemanticCarrierName {
			if semanticCarrier != nil {
				return groupRegionResult{}, fmt.Errorf("%w: the semantic carrier column is declared twice",
					ErrUnsupportedPostgresPlan)
			}
			semanticCarrier = &columns[i]
			continue
		}
		nameToColumn[columns[i].name] = &columns[i]
	}

	keyColumns := make([]*groupColumn, len(group.Keys))
	for i, key := range group.Keys {
		column, err := groupResolveColumn(nameToColumn, key.InputColumn)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group key %q: %w", key.OutputColumn, err)
		}
		keyColumns[i] = column
	}
	type groupedAggregate struct {
		aggregate ir.PhysicalStageGroupAggregate
		column    *groupColumn
		alias     string
	}
	aggregates := make([]groupedAggregate, 0, len(group.Aggregates))
	for i, aggregate := range group.Aggregates {
		var column *groupColumn
		if aggregate.InputColumn != "" {
			resolved, err := groupResolveColumn(nameToColumn, aggregate.InputColumn)
			if err != nil {
				return groupRegionResult{}, fmt.Errorf("group aggregate %q: %w", aggregate.Output, err)
			}
			column = resolved
		}
		aggregates = append(aggregates, groupedAggregate{
			aggregate: aggregate, column: column, alias: prefix + "a" + strconv.Itoa(i),
		})
	}
	type groupedRowValue struct {
		rowValue ir.PhysicalStageRowValue
		column   *groupColumn
		alias    string
	}
	rowValues := make([]groupedRowValue, 0, len(group.RowValues))
	for i, rowValue := range group.RowValues {
		column, err := groupResolveColumn(nameToColumn, rowValue.InputColumn)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group row value %q: %w", rowValue.Output, err)
		}
		rowValues = append(rowValues, groupedRowValue{
			rowValue: rowValue, column: column, alias: prefix + "rv" + strconv.Itoa(i),
		})
	}

	var contributorColumn *groupColumn
	contributorAlias := ""
	if group.RootContributorInputColumn != "" {
		if r.mapping == nil || !r.mapping.witness {
			return groupRegionResult{}, fmt.Errorf("%w: the root-contributor set is production-unreachable through the PostgreSQL compile target (L-13.6) and is refused, never partially rendered",
				ErrUnsupportedPostgresPlan)
		}
		if group.RootContributorOutputColumn != populationMappingContributorColumn || group.RootContributorVariable == "" {
			return groupRegionResult{}, fmt.Errorf("%w: the composed root-contributor set must materialize the compiler-owned mapping contributor column %q with its compiled variable",
				ErrUnsupportedPostgresPlan, populationMappingContributorColumn)
		}
		resolved, err := groupResolveColumn(nameToColumn, group.RootContributorInputColumn)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group root contributors: %w", err)
		}
		contributorColumn = resolved
		contributorAlias = prefix + "rc"
	}

	dollar := func(index int) string { return "$" + strconv.Itoa(index) }

	var semantic *groupSemanticRegion
	if group.SemanticKey != nil {
		region, err := r.renderGroupSemanticRegion(group.SemanticKey, semanticCarrier, stage, prefix, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group semantic key %q: %w", group.SemanticKey.OutputColumn, err)
		}
		semantic = &region
	} else if semanticCarrier != nil {
		return groupRegionResult{}, fmt.Errorf("%w: the semantic carrier column is minted only beside a semantic family key",
			ErrUnsupportedPostgresPlan)
	}

	var conjuncts []string
	keyReads := make([]string, len(group.Keys))
	keyCollates := make([]string, len(group.Keys))
	for i, key := range group.Keys {
		typedRead, witness, err := r.groupColumnRead(keyColumns[i], key.Kind, groupKeyTypeTag, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group key %q: %w", key.OutputColumn, err)
		}
		keyReads[i] = typedRead
		if key.Kind == "STRING" {
			keyCollates[i] = ` COLLATE "C"`
		}
		if witness != "" {
			conjuncts = append(conjuncts, witness)
		}
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyExclude:
			conjuncts = append(conjuncts, typedRead+" IS NOT NULL")
		case ir.PhysicalStageGroupMissingKeyError:
			conjuncts = append(conjuncts,
				"(CASE WHEN "+typedRead+" IS NOT NULL THEN 1 ELSE "+guardCast(groupMissingKeyTag)+" END) > 0")
		}
	}
	baseKeyReads := append([]string(nil), keyReads...)
	for _, item := range aggregates {
		if item.column == nil {
			continue
		}
		_, witness, err := r.groupColumnRead(item.column, item.aggregate.InputKind, groupValueTypeTag, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group aggregate %q: %w", item.aggregate.Output, err)
		}
		if witness != "" {
			conjuncts = append(conjuncts, witness)
		}
	}
	for _, item := range rowValues {
		if !item.rowValue.InputMany {
			_, witness, err := r.groupColumnRead(item.column, item.rowValue.InputKind, groupValueTypeTag, dollar)
			if err != nil {
				return groupRegionResult{}, fmt.Errorf("group row value %q: %w", item.rowValue.Output, err)
			}
			if witness != "" {
				conjuncts = append(conjuncts, witness)
			}
			continue
		}
		nullRead := groupNullRead(item.column)
		conjuncts = append(conjuncts, "(CASE WHEN COALESCE("+nullRead+
			" IS NULL OR jsonb_typeof("+nullRead+") = 'array', false) THEN 1 ELSE "+
			guardCast(groupValueTypeTag)+" END) > 0")
	}

	if semantic != nil {
		switch group.MissingKeyPolicy {
		case ir.PhysicalStageGroupMissingKeyExclude:
			conjuncts = append(conjuncts, semantic.codeRead+" IS NOT NULL")
		case ir.PhysicalStageGroupMissingKeyError:
			conjuncts = append(conjuncts,
				"(CASE WHEN "+semantic.codeRead+" IS NOT NULL THEN 1 ELSE "+guardCast(groupMissingKeyTag)+" END) > 0")
		}
	}

	deduped := make([]string, 0, len(conjuncts))
	seenConjunct := map[string]bool{}
	for _, conjunct := range conjuncts {
		if seenConjunct[conjunct] {
			continue
		}
		seenConjunct[conjunct] = true
		deduped = append(deduped, conjunct)
	}
	rows := "SELECT * FROM " + prefix + "src"
	if semantic != nil {
		rows += semantic.lateral
	}
	if len(deduped) > 0 {
		rows += " WHERE " + strings.Join(deduped, " AND ")
	}

	grpSelects := make([]string, 0, len(keyReads)+1+len(aggregates)+len(rowValues))
	keyAliases := make([]string, 0, len(keyReads))
	for i := range keyReads {
		alias := prefix + "k" + strconv.Itoa(i)
		grpSelects = append(grpSelects, keyReads[i]+" AS "+alias)
		keyAliases = append(keyAliases, alias)
	}
	if semantic != nil {
		for i, read := range semantic.tupleReads {
			alias := semantic.tupleAliases[i]
			keyReads = append(keyReads, read)
			keyCollates = append(keyCollates, ` COLLATE "C"`)
			grpSelects = append(grpSelects, read+" AS "+alias)
			keyAliases = append(keyAliases, alias)
		}
		grpSelects = append(grpSelects, semantic.label+" AS "+prefix+"slb")
	}
	for _, item := range aggregates {
		expression, err := r.groupAggregateExpression(item.aggregate, item.column, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group aggregate %q: %w", item.aggregate.Output, err)
		}
		grpSelects = append(grpSelects, expression+" AS "+item.alias)
	}
	var manyCTEs, manyJoins []string
	for i, item := range rowValues {
		if item.rowValue.InputMany {
			name := prefix + "m" + strconv.Itoa(i)
			selectText, singleValueRef, err := r.groupManyRowValuePoolFor(item.rowValue.Policy, item.rowValue.InputKind,
				item.column, keyReads, name, prefix+"rows", dollar)
			if err != nil {
				return groupRegionResult{}, fmt.Errorf("group row value %q: %w", item.rowValue.Output, err)
			}
			manyCTEs = append(manyCTEs, name+" AS ("+selectText+")")
			grpSelects = append(grpSelects, singleValueRef+" AS "+item.alias)
			conditions := make([]string, 0, len(keyReads))
			for j := range keyReads {
				conditions = append(conditions, keyReads[j]+" IS NOT DISTINCT FROM "+name+".m_k"+strconv.Itoa(j))
			}
			on := "TRUE"
			if len(conditions) > 0 {
				on = strings.Join(conditions, " AND ")
			}
			manyJoins = append(manyJoins, "LEFT JOIN "+name+" ON "+on)
			continue
		}
		expression, err := groupRowValueExpressionFor(item.rowValue.Policy, item.rowValue.InputKind,
			item.rowValue.InputMany, item.column, dollar)
		if err != nil {
			return groupRegionResult{}, fmt.Errorf("group row value %q: %w", item.rowValue.Output, err)
		}
		grpSelects = append(grpSelects, expression+" AS "+item.alias)
	}
	if contributorColumn != nil {
		pool := prefix + "rcpool"
		from := prefix + "rows"
		memberRead := ""
		if group.RootContributorInputMany {
			if !contributorColumn.jsonb {
				return groupRegionResult{}, fmt.Errorf("%w: a many-cardinality root-contributor input requires a jsonb source column", ErrUnsupportedPostgresPlan)
			}
			nullRead := groupNullRead(contributorColumn)
			guard := "CASE WHEN " + nullRead + " IS NULL OR jsonb_typeof(" + nullRead + ") <> 'array' THEN '[]'::jsonb ELSE " + nullRead + " END"
			from = prefix + "rows LEFT JOIN LATERAL jsonb_array_elements_text(" + guard + ") AS rcx(elem) ON true"
			memberRead = "rcx.elem"
		} else {
			typedRead, _, err := r.groupColumnRead(contributorColumn, "STRING", groupValueTypeTag, dollar)
			if err != nil {
				return groupRegionResult{}, fmt.Errorf("group root contributors: %w", err)
			}
			memberRead = typedRead
		}
		poolSelects := make([]string, 0, len(baseKeyReads)+1)
		for i, keyRead := range baseKeyReads {
			poolSelects = append(poolSelects, keyRead+" AS m_k"+strconv.Itoa(i))
		}
		distinct := memberRead + ` COLLATE "C"`
		poolSelects = append(poolSelects, "to_jsonb(COALESCE(ARRAY_AGG(DISTINCT "+distinct+" ORDER BY "+distinct+"), '{}'::text[])) AS m_pool")
		poolSelect := "SELECT " + strings.Join(poolSelects, ", ") + " FROM " + from
		if len(baseKeyReads) > 0 {
			positions := make([]string, len(baseKeyReads))
			for i := range positions {
				positions[i] = strconv.Itoa(i + 1)
			}
			poolSelect += " GROUP BY " + strings.Join(positions, ", ")
		}
		manyCTEs = append(manyCTEs, pool+" AS ("+poolSelect+")")
		conditions := make([]string, 0, len(baseKeyReads))
		for j := range baseKeyReads {
			conditions = append(conditions, baseKeyReads[j]+" IS NOT DISTINCT FROM "+pool+".m_k"+strconv.Itoa(j))
		}
		on := "TRUE"
		if len(conditions) > 0 {
			on = strings.Join(conditions, " AND ")
		}
		manyJoins = append(manyJoins, "LEFT JOIN "+pool+" ON "+on)
		grpSelects = append(grpSelects, "COALESCE(MAX("+pool+".m_pool::text)::jsonb, '[]'::jsonb) AS "+contributorAlias)
	}

	grouped := "SELECT " + strings.Join(grpSelects, ", ") + " FROM " + prefix + "rows"
	if len(manyJoins) > 0 {
		grouped += " " + strings.Join(manyJoins, " ")
	}
	if len(keyAliases) > 0 {
		positions := make([]string, len(keyAliases))
		for i := range positions {
			positions[i] = strconv.Itoa(i + 1)
		}
		grouped += " GROUP BY " + strings.Join(positions, ", ")
	}

	constructionID := r.textParameter(bindStringVar(plan.BindVars, group.ConstructionIDBindKey), "")
	outputIDs := map[string]string{}
	for _, column := range stage.OutputColumns {
		if _, seen := outputIDs[column.Name]; !seen {
			outputIDs[column.Name] = column.ID
		}
	}
	identityParts := []string{
		`'["construction",' || to_jsonb(` + dollar(constructionID) + "::text) || ']'",
		`'["operation","GROUP"]'`,
	}
	appendKeyPart := func(columnName string, alias string) error {
		columnID, ok := outputIDs[columnName]
		if !ok {
			return fmt.Errorf("%w: group key output %q has no stable column ID (construction_group.go:136-142 parity)",
				ErrUnsupportedPostgresPlan, columnName)
		}
		idBind := r.textParameter(columnID, "")
		identityParts = append(identityParts,
			"'[' || to_jsonb("+dollar(idBind)+"::text) || ',' || COALESCE(to_jsonb(g."+alias+")::text, 'null') || ']'")
		return nil
	}
	for i, key := range group.Keys {
		if err := appendKeyPart(key.OutputColumn, prefix+"k"+strconv.Itoa(i)); err != nil {
			return groupRegionResult{}, err
		}
	}
	if semantic != nil {
		identityParts = append(identityParts, semantic.identityPart)
	}
	identity := "'[' || " + strings.Join(identityParts, " || ',' || ") + " || ']'"

	expressions := map[string]string{}
	for i, key := range group.Keys {
		expressions[key.OutputColumn] = "g." + prefix + "k" + strconv.Itoa(i)
	}
	for _, item := range aggregates {
		expressions[item.aggregate.Output] = "g." + item.alias
	}
	for _, item := range rowValues {
		expressions[item.rowValue.Output] = "g." + item.alias
	}
	if semantic != nil {
		expressions[group.SemanticKey.OutputColumn] = "g." + prefix + "slb"
	}
	if contributorColumn != nil {
		expressions[group.RootContributorOutputColumn] = "g." + contributorAlias
	}
	expressions[identityColumn] = identity
	selects := make([]string, 0, len(outputColumns))
	names := make([]string, 0, len(outputColumns))
	for _, column := range outputColumns {
		expression, ok := expressions[column.Name]
		if !ok {
			return groupRegionResult{}, fmt.Errorf("%w: group final column %q is not a registered key, reducer, row value, or the stable identity",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		for _, seen := range names {
			if seen == column.Name {
				return groupRegionResult{}, fmt.Errorf("%w: group final column %q is declared twice", ErrUnsupportedPostgresPlan, column.Name)
			}
		}
		selects = append(selects, expression+` AS "c`+strconv.Itoa(len(selects))+`"`)
		names = append(names, column.Name)
	}
	emitted := len(group.Keys) + len(aggregates) + len(rowValues) + 1
	if semantic != nil {
		emitted++
	}
	if contributorColumn != nil {
		emitted++
	}
	if len(names) != emitted {
		return groupRegionResult{}, fmt.Errorf("%w: the registered slice emits every key, reducer, and row value plus the hidden stable identity",
			ErrUnsupportedPostgresPlan)
	}

	final := "SELECT " + strings.Join(selects, ", ") + " FROM " + prefix + "grp AS g"
	order := make([]string, 0, len(keyAliases)+1)
	for i, alias := range keyAliases {
		order = append(order, "g."+alias+keyCollates[i]+" ASC NULLS FIRST")
	}
	order = append(order, identity+` COLLATE "C" ASC`)

	ctes := []string{
		prefix + "src AS (" + sourceText + ")",
		prefix + "rows AS (" + rows + ")",
	}
	ctes = append(ctes, manyCTEs...)
	ctes = append(ctes, prefix+"grp AS ("+grouped+")")
	return groupRegionResult{ctes: ctes, final: final, order: order, names: names, keyCollates: keyCollates}, nil
}

func (r *planRenderer) materializeGroupSource(plan ir.PhysicalPlan, ret *ir.PhysicalReturn) ([]groupColumn, error) {
	classified, err := classifyExtractionProjections(ret.Projections, func(variable string) (string, bool) {
		scope, ok := r.rows[variable]
		return scope.resourceType, ok
	})
	if err != nil {
		return nil, err
	}
	columns := make([]groupColumn, 0, len(classified))
	seen := map[string]bool{}
	for i, projection := range classified {
		if seen[projection.projection.Name] {
			return nil, fmt.Errorf("%w: group source projection name %q is declared twice (L-13.11)",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		}
		seen[projection.projection.Name] = true
		var expression string
		isJSONB := false
		switch projection.kind {
		case extractionDirectValue, extractionValueExpression:
			value := projection.projection.Value
			if projection.kind == extractionValueExpression {
				value = *projection.projection.Expression.Value
			}
			if len(value.Path) == 0 {
				return nil, fmt.Errorf("%w: group source projection %q is not a registered scalar projection (whole-document read)",
					ErrUnsupportedPostgresPlan, projection.projection.Name)
			}
			resolved, err := r.resolveValue(value)
			if err != nil {
				return nil, fmt.Errorf("group source projection %q: %w", projection.projection.Name, err)
			}
			expression = resolved
			if len(value.Path) > 0 {
				isJSONB = true
			}
			if isPayloadExpression(expression) {
				expression = "(" + expression + ")"
			}
		case extractionSelector:
			scope := r.rows[projection.expression.Extract.Source.Variable]
			value, err := r.scalarExtractValueExpression(scope.alias, projection.path, projection.expression.NullBehavior)
			if err != nil {
				return nil, fmt.Errorf("group source projection %q: %w", projection.projection.Name, err)
			}
			expression = value
			isJSONB = true
		case extractionPresenceSidecar:
			return nil, fmt.Errorf("%w: group source projection %q is a presence sidecar and cannot be addressed by key, reducer, or row value",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		case extractionCallExpression:
			value, err := r.renderCallProjection(plan, projection.projection)
			if err != nil {
				return nil, err
			}
			expression = value
			isJSONB = true
		case extractionObject, extractionSlice:
			return nil, fmt.Errorf("%w: group source projection %q is a composite and cannot be addressed by key, reducer, or row value (lowering admits public scalars only)",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		case extractionUnitNormalized:
			return nil, fmt.Errorf("%w: group source projection %q is a unit-normalized extraction; unit normalization belongs to the read spine registration (unit_normalization.go), not the closed GROUP slice",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		default:
			return nil, fmt.Errorf("%w: group source projection %q is not a registered projection arm",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		}
		alias := "c" + strconv.Itoa(i)
		columns = append(columns, groupColumn{
			name: projection.projection.Name, alias: alias, expr: expression,
			ref: `"` + alias + `"`, jsonb: isJSONB,
		})
	}
	return columns, nil
}

func groupResolveColumn(nameToColumn map[string]*groupColumn, name string) (*groupColumn, error) {
	column, ok := nameToColumn[name]
	if !ok {
		return nil, fmt.Errorf("%w: group input column %q is not among the source projection names (L-13.11)",
			ErrUnsupportedPostgresPlan, name)
	}
	return column, nil
}

func groupNullRead(column *groupColumn) string {
	if !column.jsonb {
		return column.ref
	}
	return "NULLIF(" + column.ref + ", 'null'::jsonb)"
}

func (r *planRenderer) groupColumnRead(column *groupColumn, kind string, tag string, dollar func(int) string) (string, string, error) {
	nullRead := groupNullRead(column)
	var typeofTag string
	switch kind {
	case "STRING":
		typeofTag = "string"
	case "INTEGER", "DECIMAL":
		typeofTag = "number"
	case "BOOLEAN":
		typeofTag = "boolean"
	default:
		return "", "", fmt.Errorf("%w: group column kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
	witness := ""
	if column.jsonb {
		typeBind := r.textParameter(typeofTag, "text:typeof:"+typeofTag)
		witness = "(CASE WHEN COALESCE(" + nullRead + " IS NULL OR jsonb_typeof(" + nullRead + ") = " +
			dollar(typeBind) + "::text, false) THEN 1 ELSE " + guardCast(tag) + " END) > 0"
	}
	switch kind {
	case "STRING":
		if !column.jsonb {
			return column.ref, "", nil
		}
		return "((" + nullRead + ") #>> '{}')", witness, nil
	case "INTEGER", "DECIMAL":
		if !column.jsonb {
			return "(" + column.ref + ")::numeric", "", nil
		}
		return "((" + nullRead + ") #>> '{}')::numeric", witness, nil
	case "BOOLEAN":
		if !column.jsonb {
			return "(" + column.ref + ")::boolean", "", nil
		}
		return "((" + nullRead + ") #>> '{}')::boolean", witness, nil
	}
	return "", "", fmt.Errorf("%w: group column kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
}

func (r *planRenderer) groupAggregateExpression(aggregate ir.PhysicalStageGroupAggregate, column *groupColumn, dollar func(int) string) (string, error) {
	if aggregate.Operation == "COUNT_ROWS" {
		return "COUNT(*)", nil
	}
	typedRead, _, err := r.groupColumnRead(column, aggregate.InputKind, groupValueTypeTag, dollar)
	if err != nil {
		return "", err
	}
	collate := ""
	if aggregate.InputKind == "STRING" {
		collate = ` COLLATE "C"`
	}
	switch aggregate.Operation {
	case "COUNT_NON_NULL":
		return "COUNT(" + typedRead + ")", nil
	case "COUNT_DISTINCT":
		return "COUNT(DISTINCT " + typedRead + collate + ")", nil
	case "MIN":
		return "MIN(" + typedRead + collate + ")", nil
	case "MAX":
		return "MAX(" + typedRead + collate + ")", nil
	case "SUM":
		return groupBoundedDoubleSum(column, aggregate.InputKind), nil
	case "MEAN":
		sum := groupBoundedDoubleSum(column, aggregate.InputKind)
		poolCount := "COUNT(" + groupNumericRead(column, aggregate.InputKind) + ")"
		return "CASE WHEN " + poolCount + " = 0 THEN NULL ELSE " +
			sum + " / (" + poolCount + ")::double precision END", nil
	}
	return "", fmt.Errorf("%w: aggregate %q is not a registered reducer", ErrUnsupportedPostgresPlan, string(aggregate.Operation))
}

func groupDoubleBandCast(numericFragment string) string {
	return "(CASE WHEN " + numericFragment + " IS NULL THEN NULL" +
		" WHEN abs(" + numericFragment + ") < '" + callDoubleOverflowBound + "'::numeric" +
		" THEN CASE WHEN abs(" + numericFragment + ") >= '" + callDoubleUnderflowFloor + "'::numeric" +
		" THEN (" + numericFragment + ")::double precision ELSE 0::double precision END" +
		" ELSE 0::double precision END)"
}

func groupBoundedDoubleSum(column *groupColumn, kind string) string {
	return groupDoubleBandCast("SUM(" + callBandOperandClamp(groupNumericRead(column, kind)) + ")")
}

func groupNumericRead(column *groupColumn, kind string) string {
	if kind == "INTEGER" || kind == "DECIMAL" {
		if !column.jsonb {
			return "(" + column.ref + ")::numeric"
		}
		return "((" + groupNullRead(column) + ") #>> '{}')::numeric"
	}
	return ""
}

func groupRowValueMemberRead(column *groupColumn, kind string) func(source string) string {
	return func(source string) string {
		nullRead := source
		if column.jsonb {
			nullRead = "NULLIF(" + source + ", 'null'::jsonb)"
		}
		switch kind {
		case "STRING":
			if column.jsonb {
				return "((" + nullRead + ") #>> '{}')"
			}
			return "(" + nullRead + ")"
		case "INTEGER", "DECIMAL":
			if column.jsonb {
				return "((" + nullRead + ") #>> '{}')::numeric"
			}
			return "(" + nullRead + ")::numeric"
		default:
			if column.jsonb {
				return "((" + nullRead + ") #>> '{}')::boolean"
			}
			return "(" + nullRead + ")::boolean"
		}
	}
}

func groupRowValueCollateAndArrayType(kind string) (collate, arrayType string, ok bool) {
	switch kind {
	case "STRING":
		return ` COLLATE "C"`, "text[]", true
	case "INTEGER", "DECIMAL":
		return "", "numeric[]", true
	case "BOOLEAN":
		return "", "boolean[]", true
	}
	return "", "", false
}

func (r *planRenderer) groupManyRowValuePoolFor(policy, kind string, column *groupColumn, keyReads []string, cteName string, rowsRelation string, dollar func(int) string) (selectText, singleValueRef string, err error) {
	switch policy {
	case "ALL", "ONE":
	default:
		return "", "", fmt.Errorf("%w: row value policy %q is unsupported", ErrUnsupportedPostgresPlan, policy)
	}
	collate, arrayType, ok := groupRowValueCollateAndArrayType(kind)
	if !ok {
		return "", "", fmt.Errorf("%w: row value input kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
	if !column.jsonb {
		return "", "", fmt.Errorf("%w: a many-cardinality row value requires a jsonb source column", ErrUnsupportedPostgresPlan)
	}
	member := groupRowValueMemberRead(column, kind)
	memberRead := member("(_rv.item)")
	selects := make([]string, 0, len(keyReads)+1)
	for i, keyRead := range keyReads {
		selects = append(selects, keyRead+" AS m_k"+strconv.Itoa(i))
	}
	if policy == "ALL" {
		distinct := memberRead + collate
		selects = append(selects, "to_jsonb(COALESCE(ARRAY_AGG(DISTINCT "+distinct+" ORDER BY "+distinct+
			"), '{}'::"+arrayType+")) AS m_pool")
	} else {
		selects = append(selects, "(CASE WHEN COUNT(DISTINCT "+memberRead+collate+") <= 1 THEN MIN("+memberRead+collate+
			") ELSE "+groupRowValueRaiseArm(kind)+" END) AS m_pool")
	}
	typeofTag := map[string]string{"STRING": "string", "INTEGER": "number", "DECIMAL": "number", "BOOLEAN": "boolean"}[kind]
	itemNull := "NULLIF((_rv.item), 'null'::jsonb)"
	typeBind := r.textParameter(typeofTag, "text:typeof:"+typeofTag)
	selectText = "SELECT " + strings.Join(selects, ", ") +
		" FROM " + rowsRelation + ", LATERAL jsonb_array_elements(" + groupNullRead(column) + ") _rv(item)" +
		" WHERE " + itemNull + " IS NOT NULL AND (CASE WHEN COALESCE(" + itemNull +
		" IS NULL OR jsonb_typeof(" + itemNull + ") = " + dollar(typeBind) + "::text, false) THEN 1 ELSE " +
		guardCast(groupValueTypeTag) + " END) > 0"
	if len(keyReads) > 0 {
		positions := make([]string, len(keyReads))
		for i := range positions {
			positions[i] = strconv.Itoa(i + 1)
		}
		selectText += " GROUP BY " + strings.Join(positions, ", ")
	}
	if policy == "ALL" {
		return selectText, "MAX(" + cteName + ".m_pool::text)::jsonb", nil
	}
	return selectText, "MAX(" + cteName + ".m_pool)", nil
}

func groupRowValueExpressionFor(policy, kind string, many bool, column *groupColumn, dollar func(int) string) (string, error) {
	switch policy {
	case "ALL", "ONE":
	default:
		return "", fmt.Errorf("%w: row value policy %q is unsupported", ErrUnsupportedPostgresPlan, policy)
	}
	if many {
		return "", fmt.Errorf("%w: many-cardinality row values render through groupManyRowValuePoolFor", ErrUnsupportedPostgresPlan)
	}
	collate, arrayType, ok := groupRowValueCollateAndArrayType(kind)
	if !ok {
		return "", fmt.Errorf("%w: row value input kind %q is unsupported", ErrUnsupportedPostgresPlan, kind)
	}
	member := groupRowValueMemberRead(column, kind)
	read := member(column.ref)
	if policy == "ALL" {
		distinct := read + collate
		return "to_jsonb(COALESCE(ARRAY_AGG(DISTINCT " + distinct + " ORDER BY " + distinct +
			") FILTER (WHERE " + read + " IS NOT NULL), '{}'" + "::" + arrayType + "))", nil
	}
	return "(CASE WHEN COUNT(DISTINCT " + read + collate + ") <= 1 THEN MIN(" + read + collate +
		") ELSE " + groupRowValueRaiseArm(kind) + " END)", nil
}

func groupRowValueRaiseArm(kind string) string {
	guard := guardCast(groupRowValueMultipleTag)
	switch kind {
	case "STRING":
		return "CAST(" + guard + " AS text)"
	case "BOOLEAN":
		return "CAST(" + guard + " AS boolean)"
	}
	return guard
}
