package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const codedGroupRowIDColumn = "__loom_row_id"

const codedGroupMissingKeyTag = "CONSTRUCTION_CODED_GROUP_MISSING_KEY"

const codedGroupTrimCharacters = " "

func (r *planRenderer) codedGroupTrimmedNonBlank(expr string) string {
	trimChars := r.textParameter(codedGroupTrimCharacters, "text:"+codedGroupTrimCharacters)
	dollar := func(index int) string { return "$" + strconv.Itoa(index) }
	return "btrim(" + expr + ", " + dollar(trimChars) + "::text) <> ''"
}

func codedGroupPlanMentionsStage(plan ir.PhysicalPlan) bool {
	if plan.StageSequence == nil {
		return false
	}
	for _, stage := range plan.StageSequence.Stages {
		if stage.Kind == ir.PhysicalStageCodedGroupOp {
			return true
		}
	}
	return false
}

func renderCodedGroupPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateCodedGroupPlanSurface(plan); err != nil {
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
	sql, err := renderer.renderCodedGroupStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

func validateCodedGroupPlanSurface(plan ir.PhysicalPlan) error {
	if plan.StageSequence == nil {
		return fmt.Errorf("%w: coded-group dispatch requires a stage sequence", ErrUnsupportedPostgresPlan)
	}
	sequence := plan.StageSequence
	if len(sequence.Stages) != 1 {
		return fmt.Errorf("%w: the registered coded-group slice admits exactly one CODED_GROUP construction stage; multi-stage composition is a pg-sql-16 capability gap",
			ErrUnsupportedPostgresPlan)
	}
	stage := sequence.Stages[0]
	if stage.Kind != ir.PhysicalStageCodedGroupOp || stage.CodedGroup == nil {
		return fmt.Errorf("%w: operation %q is not part of the registered coded-group slice",
			ErrUnsupportedPostgresPlan, string(stage.Kind))
	}
	if stage.Group != nil || stage.CohortGroup != nil || stage.Filter != nil || stage.Expand != nil ||
		stage.GroupedPivot != nil || stage.Unpivot != nil || stage.RelatedSource != nil ||
		stage.RelatedExpand != nil || stage.RelatedField != nil || len(stage.DerivedLets) != 0 {
		return fmt.Errorf("%w: CODED_GROUP requires only a coded-group payload (ir/physical_stage_validation.go:187 re-read at the door)",
			ErrUnsupportedPostgresPlan)
	}
	if stage.InputStageID != sequence.SourceStageID || stage.ID != sequence.FinalStageID {
		return fmt.Errorf("%w: the registered slice admits only the inline first-stage CODED_GROUP (InputStageID must equal %q); the DOCUMENT re-query shape is production-unreachable (recipe/construction.go:665-668)",
			ErrUnsupportedPostgresPlan, sequence.SourceStageID)
	}
	if sequence.CellTraceReturn != nil || sequence.RowLineageReturn != nil || sequence.PopulationMappingReturn != nil {
		return fmt.Errorf("%w: row-lineage, cell-trace, and population-mapping returns have no PostgreSQL compiled route (compiler/row_lineage.go:655, cell_trace.go:54, population_mapping.go:38)",
			ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID || sequence.PreviewSourceWindowByRootID || sequence.PreviewTerminalPivotWindow {
		return fmt.Errorf("%w: preview source windowing and the terminal Pivot window require Pivot terminals and are not registered for CODED_GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.OutputAuthResourcePathBindKey != "" {
		return fmt.Errorf("%w: the composite Combine prefix authorization path is not registered for CODED_GROUP", ErrUnsupportedPostgresPlan)
	}
	if sequence.PreviewLimitBindKey != "" {
		if value, ok := plan.BindVars[sequence.PreviewLimitBindKey].(int); !ok || value <= 0 {
			return fmt.Errorf("%w: coded-group preview limit bind %q must be a positive int",
				ErrUnsupportedPostgresPlan, sequence.PreviewLimitBindKey)
		}
	}
	if err := validateCodedGroupPayload(stage, plan.BindVars); err != nil {
		return err
	}
	return validateCodedGroupOperations(plan)
}

func validateCodedGroupPayload(stage ir.PhysicalConstructionStage, bindVars map[string]any) error {
	coded := stage.CodedGroup
	for _, key := range []string{coded.RootCollectionBindKey, coded.ConstructionIDBindKey, coded.OccurrenceIDBindKey, coded.CodingPathBindKey} {
		if value, ok := bindVars[key].(string); !ok || strings.TrimSpace(value) == "" {
			return fmt.Errorf("%w: coded-group bind %q must be a non-empty string", ErrUnsupportedPostgresPlan, key)
		}
	}
	if collection, _ := bindVars[coded.RootCollectionBindKey].(string); collection != coded.ResourceType || strings.TrimSpace(coded.ResourceType) == "" {
		return fmt.Errorf("%w: root collection bind does not match the exact root resource type", ErrUnsupportedPostgresPlan)
	}
	if occurrence, _ := bindVars[coded.OccurrenceIDBindKey].(string); occurrence != "base" {
		return fmt.Errorf("%w: the registered inline slice admits only the base root occurrence (ir/physical_stage_validation.go:2274)",
			ErrUnsupportedPostgresPlan)
	}
	if codingPath, _ := bindVars[coded.CodingPathBindKey].(string); codingPath != coded.CodingPath {
		return fmt.Errorf("%w: the Coding path bind differs from the compiled path", ErrUnsupportedPostgresPlan)
	}
	if !coded.SourceRowsUnique || coded.SourceIdentityColumn != "_key" {
		return fmt.Errorf("%w: CODED_GROUP requires the compiler proof of one source row per root _key (ir/physical_stage_validation.go:2270-2274)",
			ErrUnsupportedPostgresPlan)
	}
	if err := validateCodedGroupRowValues(coded); err != nil {
		return err
	}
	switch coded.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyGroup, ir.PhysicalStageGroupMissingKeyExclude, ir.PhysicalStageGroupMissingKeyError:
	default:
		return fmt.Errorf("%w: missing-key policy %q is unsupported", ErrUnsupportedPostgresPlan, string(coded.MissingKeyPolicy))
	}
	if len(coded.PathSegments) == 0 || !coded.PathSegments[len(coded.PathSegments)-1].Repeated {
		return fmt.Errorf("%w: the Coding path must end in a repeated Coding array", ErrUnsupportedPostgresPlan)
	}
	var canonical strings.Builder
	for index, segment := range coded.PathSegments {
		if !payloadPathSegmentPattern.MatchString(segment.Name) {
			return fmt.Errorf("%w: coded-group path segment %q is not renderer-safe", ErrUnsupportedPostgresPlan, segment.Name)
		}
		if index > 0 {
			canonical.WriteByte('.')
		}
		canonical.WriteString(segment.Name)
		if segment.Repeated {
			canonical.WriteString("[]")
		}
	}
	if canonical.String() != coded.CodingPath {
		return fmt.Errorf("%w: the Coding path must be the canonical join of its segments", ErrUnsupportedPostgresPlan)
	}
	for _, variable := range []string{coded.SystemVariable, coded.VersionVariable, coded.CodeVariable, coded.CountVariable, coded.IdentityVariable, coded.GroupRowsVariable} {
		if variable == "" {
			return fmt.Errorf("%w: coded-group plan variables must be compiled and non-empty", ErrUnsupportedPostgresPlan)
		}
	}
	return nil
}

func codedGroupSanitizeRowValueProjections(projections []ir.PhysicalProjection) []ir.PhysicalProjection {
	out := make([]ir.PhysicalProjection, len(projections))
	for i, projection := range projections {
		projection.Hidden = false
		out[i] = projection
	}
	return out
}

func validateCodedGroupRowValues(coded *ir.PhysicalStageCodedGroup) error {
	if len(coded.RowValues) == 0 && len(coded.RowValueProjections) == 0 {
		return nil
	}
	inputs := make(map[string]bool, len(coded.RowValues))
	for _, rowValue := range coded.RowValues {
		if rowValue.InputColumn == "" || rowValue.Output == "" || rowValue.Variable == "" {
			return fmt.Errorf("%w: coded-group row value columns and variables must be compiled and non-empty",
				ErrUnsupportedPostgresPlan)
		}
		if inputs[rowValue.InputColumn] {
			return fmt.Errorf("%w: coded-group row value input column %q is declared twice (construction_coded_group.go:92-94 parity)",
				ErrUnsupportedPostgresPlan, rowValue.InputColumn)
		}
		inputs[rowValue.InputColumn] = true
		switch rowValue.Policy {
		case "ALL", "ONE":
		default:
			return fmt.Errorf("%w: row value %q has unsupported policy %q (row_values.go:53 parity)",
				ErrUnsupportedPostgresPlan, rowValue.Output, rowValue.Policy)
		}
		switch rowValue.InputKind {
		case "STRING", "INTEGER", "DECIMAL", "BOOLEAN":
		default:
			return fmt.Errorf("%w: row value %q has unsupported input kind %q",
				ErrUnsupportedPostgresPlan, rowValue.Output, rowValue.InputKind)
		}
		if rowValue.InputMany {
			return fmt.Errorf("%w: coded-group many-cardinality (local-array-expansion) row values are a wave-2 pg-sql-13 gap (the shared pool emitter groupManyRowValuePoolFor renders from g_rows; a CODED_GROUP pool over cg_tuples is wave-2) and are refused, never partially rendered",
				ErrUnsupportedPostgresPlan)
		}
	}
	provided := make(map[string]int, len(coded.RowValueProjections))
	classified, err := classifyExtractionProjections(codedGroupSanitizeRowValueProjections(coded.RowValueProjections), func(string) (string, bool) {
		return coded.ResourceType, true
	})
	if err != nil {
		return err
	}
	for _, projection := range classified {
		provided[projection.projection.Name]++
		switch projection.kind {
		case extractionDirectValue, extractionValueExpression, extractionSelector:
			value := projection.projection.Value
			if projection.kind == extractionValueExpression {
				value = *projection.projection.Expression.Value
			}
			if projection.kind != extractionSelector && len(value.Path) == 0 {
				return fmt.Errorf("%w: coded-group row value projection %q is not a registered scalar projection (whole-document read)",
					ErrUnsupportedPostgresPlan, projection.projection.Name)
			}
		default:
			return fmt.Errorf("%w: coded-group row value projection %q is not a direct payload extraction (only the source-clone scalar shapes — direct value, value expression, selector — are registered, group-arm parity)",
				ErrUnsupportedPostgresPlan, projection.projection.Name)
		}
	}
	for name := range inputs {
		if provided[name] != 1 {
			return fmt.Errorf("%w: coded-group row value input %q needs exactly one source projection (found %d)",
				ErrUnsupportedPostgresPlan, name, provided[name])
		}
	}
	for name := range provided {
		if !inputs[name] {
			return fmt.Errorf("%w: coded-group row value projection %q addresses no row value input column",
				ErrUnsupportedPostgresPlan, name)
		}
	}
	return nil
}

func validateCodedGroupOperations(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	first := plan.Operations[0]
	if first.Kind != ir.PhysicalRootScanOp || first.RootScan == nil {
		return fmt.Errorf("%w: the coded-group source requires exactly one leading ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	scan := first.RootScan
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are bound only by RenderPopulationRootScan (population.go); the plain spine refuses them",
			ErrUnsupportedPostgresPlan)
	}
	sawSort, sawLimit, sawReturn := false, false, false
	for index, operation := range plan.Operations {
		last := index == len(plan.Operations)-1
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: exactly one leading ROOT_SCAN is required", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalFilterOp, ir.PhysicalDerivedLetOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: source operations must precede the execution window and the terminal RETURN",
					ErrUnsupportedPostgresPlan)
			}
			if operation.Kind == ir.PhysicalFilterOp && operation.Filter == nil {
				return fmt.Errorf("%w: operation %d: filter payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Kind == ir.PhysicalDerivedLetOp && operation.DerivedLet == nil {
				return fmt.Errorf("%w: operation %d: derived-let payload is missing", ErrUnsupportedPostgresPlan, index)
			}
		case ir.PhysicalSetOp:
			return fmt.Errorf("%w: coded-group source-region SET is a pg-sql-16 capability gap (no PostgreSQL contract for lowering-emitted child-set subplans; probe-attested L-19.4) and is refused, never partially rendered",
				ErrUnsupportedPostgresPlan)
		case ir.PhysicalSortOp:
			if sawSort || sawLimit || sawReturn {
				return fmt.Errorf("%w: at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Sort == nil {
				return fmt.Errorf("%w: operation %d: sort payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if sawLimit || sawReturn {
				return fmt.Errorf("%w: at most one LIMIT", ErrUnsupportedPostgresPlan)
			}
			if operation.Limit == nil {
				return fmt.Errorf("%w: operation %d: limit payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			if !last || operation.Return == nil || len(operation.Return.Projections) == 0 {
				return fmt.Errorf("%w: the coded-group source requires a terminal RETURN with its projection list (lower/recipe_construction_coded_group.go:180-184)",
					ErrUnsupportedPostgresPlan)
			}
			sawReturn = true
		default:
			return fmt.Errorf("%w: operation %q is not part of the registered coded-group source surface",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawReturn {
		return fmt.Errorf("%w: the coded-group source requires a terminal RETURN", ErrUnsupportedPostgresPlan)
	}
	return nil
}

func (r *planRenderer) renderCodedGroupStatement(plan ir.PhysicalPlan) (string, error) {
	coded := plan.StageSequence.Stages[0].CodedGroup

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
				return "", fmt.Errorf("%w: operation %q is not part of the registered coded-group source surface",
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

	type codedGroupRowValue struct {
		rowValue ir.PhysicalStageRowValue
		column   *groupColumn
	}
	var codedRowValues []codedGroupRowValue
	var rvSuffix, rvCarryS, rvCarryC string
	if len(coded.RowValues) > 0 {
		classified, err := classifyExtractionProjections(codedGroupSanitizeRowValueProjections(coded.RowValueProjections), func(variable string) (string, bool) {
			scope, ok := r.rows[variable]
			if !ok {
				return "", false
			}
			if scope.resourceType != "" {
				return scope.resourceType, true
			}
			return coded.ResourceType, true
		})
		if err != nil {
			return "", err
		}
		byName := map[string]classifiedExtractionProjection{}
		for _, projection := range classified {
			byName[projection.projection.Name] = projection
		}
		for i := range coded.RowValues {
			rowValue := coded.RowValues[i]
			projection, ok := byName[rowValue.InputColumn]
			if !ok {
				return "", fmt.Errorf("%w: coded-group row value input %q has no source projection",
					ErrUnsupportedPostgresPlan, rowValue.InputColumn)
			}
			var resolved string
			switch projection.kind {
			case extractionSelector:
				extracted, err := r.scalarExtractValueExpression(r.rootAlias, projection.path, projection.expression.NullBehavior)
				if err != nil {
					return "", fmt.Errorf("coded-group row value %q: %w", rowValue.Output, err)
				}
				resolved = extracted
			case extractionDirectValue, extractionValueExpression:
				value := projection.projection.Value
				if projection.kind == extractionValueExpression {
					value = *projection.projection.Expression.Value
				}
				if len(value.Path) == 0 {
					return "", fmt.Errorf("%w: coded-group row value projection %q is not a registered scalar projection (whole-document read)",
						ErrUnsupportedPostgresPlan, rowValue.InputColumn)
				}
				renderedValue, err := r.resolveValue(value)
				if err != nil {
					return "", fmt.Errorf("coded-group row value %q: %w", rowValue.Output, err)
				}
				resolved = renderedValue
			default:
				return "", fmt.Errorf("%w: coded-group row value projection %q is not a registered scalar arm",
					ErrUnsupportedPostgresPlan, rowValue.InputColumn)
			}
			if isPayloadExpression(resolved) {
				resolved = "(" + resolved + ")"
			}
			alias := "cg_rv" + strconv.Itoa(i)
			codedRowValues = append(codedRowValues, codedGroupRowValue{
				rowValue: rowValue,
				column:   &groupColumn{name: rowValue.InputColumn, alias: alias, expr: resolved, ref: `"` + alias + `"`, jsonb: true},
			})
			rvSuffix += ", " + resolved + ` AS ` + alias
			rvCarryS += ", s." + alias
			rvCarryC += ", c." + alias
		}
	}

	resource := r.storage.Resource
	source := "SELECT " + r.rootAlias + "." + resource.MustColumn("logical_id").SQLText() +
		` AS cg_root_key, ` + r.rootAlias + "." + resource.MustColumn("payload").SQLText() + ` AS cg_payload` + rvSuffix +
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

	lateralSQL, itemExpression, err := r.codedGroupExpansion(coded.PathSegments)
	if err != nil {
		return "", err
	}

	systemKey := r.textParameter("system", "text:system")
	versionKey := r.textParameter("version", "text:version")
	codeKey := r.textParameter("code", "text:code")
	trimChars := r.textParameter(codedGroupTrimCharacters, "text:"+codedGroupTrimCharacters)
	dollar := func(index int) string { return "$" + strconv.Itoa(index) }

	itemSystemText := itemExpression + " ->> " + dollar(systemKey) + "::text"
	itemCodeText := itemExpression + " ->> " + dollar(codeKey) + "::text"

	valid := "COALESCE(jsonb_typeof(" + itemExpression + ") = 'object'" +
		" AND jsonb_typeof(" + itemExpression + " -> " + dollar(systemKey) + "::text) = 'string'" +
		" AND btrim(" + itemSystemText + ", " + dollar(trimChars) + "::text) <> ''" +
		" AND jsonb_typeof(" + itemExpression + " -> " + dollar(codeKey) + "::text) = 'string'" +
		" AND btrim(" + itemCodeText + ", " + dollar(trimChars) + "::text) <> '', false)"

	candidates := "SELECT s.cg_root_key" + rvCarryS + ", " + itemExpression + " AS cg_item, " + valid + " AS cg_valid" +
		" FROM cg_src AS s" + lateralSQL

	tupleSystemText := "c.cg_item ->> " + dollar(systemKey) + "::text"
	tupleCodeText := "c.cg_item ->> " + dollar(codeKey) + "::text"
	tupleVersionText := "c.cg_item ->> " + dollar(versionKey) + "::text"

	var tuplesConjuncts []string
	seenTuplesConjunct := map[string]bool{}
	for i := range codedRowValues {
		_, witness, err := r.groupColumnRead(codedRowValues[i].column, codedRowValues[i].rowValue.InputKind,
			groupValueTypeTag, dollar)
		if err != nil {
			return "", fmt.Errorf("coded-group row value %q: %w", codedRowValues[i].rowValue.Output, err)
		}
		if witness != "" && !seenTuplesConjunct[witness] {
			seenTuplesConjunct[witness] = true
			tuplesConjuncts = append(tuplesConjuncts, witness)
		}
	}
	switch coded.MissingKeyPolicy {
	case ir.PhysicalStageGroupMissingKeyExclude:
		tuplesConjuncts = append(tuplesConjuncts, "c.cg_valid")
	case ir.PhysicalStageGroupMissingKeyError:
		tuplesConjuncts = append(tuplesConjuncts,
			"(CASE WHEN c.cg_valid THEN 1 ELSE "+guardCast(codedGroupMissingKeyTag)+" END) > 0")
	}
	tuplesWhere := ""
	if len(tuplesConjuncts) > 0 {
		tuplesWhere = " WHERE " + strings.Join(tuplesConjuncts, " AND ")
	}
	tuples := "SELECT DISTINCT c.cg_root_key" + rvCarryC + "," +
		" CASE WHEN c.cg_valid THEN " + tupleSystemText + " END AS cg_system," +
		" CASE WHEN c.cg_valid AND jsonb_typeof(c.cg_item -> " + dollar(versionKey) + "::text) = 'string'" +
		" AND btrim(" + tupleVersionText + ", " + dollar(trimChars) + "::text) <> '' THEN " + tupleVersionText + " END AS cg_version," +
		" CASE WHEN c.cg_valid THEN " + tupleCodeText + " END AS cg_code" +
		" FROM cg_candidates AS c" + tuplesWhere

	groupedSelects := "SELECT cg_system, cg_version, cg_code, COUNT(*) AS cg_count"
	for i := range codedRowValues {
		expression, err := groupRowValueExpressionFor(codedRowValues[i].rowValue.Policy,
			codedRowValues[i].rowValue.InputKind, false, codedRowValues[i].column, dollar)
		if err != nil {
			return "", fmt.Errorf("coded-group row value %q: %w", codedRowValues[i].rowValue.Output, err)
		}
		groupedSelects += ", " + expression + " AS cg_g" + strconv.Itoa(i)
	}
	grouped := groupedSelects + " FROM cg_tuples GROUP BY 1, 2, 3"

	constructionID := r.textParameter(bindStringVar(plan.BindVars, coded.ConstructionIDBindKey), "")
	occurrenceID := r.textParameter(bindStringVar(plan.BindVars, coded.OccurrenceIDBindKey), "")
	codingPath := r.textParameter(bindStringVar(plan.BindVars, coded.CodingPathBindKey), "")
	identity := "'[' || " +
		quoteSQLLiteral(`"construction"`) + " || ',' || to_jsonb(" + dollar(constructionID) + "::text)" +
		" || ',' || " + quoteSQLLiteral(`"operation"`) + " || ',' || " + quoteSQLLiteral(`"CODED_GROUP"`) +
		" || ',' || " + quoteSQLLiteral(`"occurrence"`) + " || ',' || to_jsonb(" + dollar(occurrenceID) + "::text)" +
		" || ',' || " + quoteSQLLiteral(`"path"`) + " || ',' || to_jsonb(" + dollar(codingPath) + "::text)" +
		" || ',' || " + quoteSQLLiteral(`"system"`) + " || ',' || COALESCE(to_jsonb(g.cg_system)::text, 'null')" +
		" || ',' || " + quoteSQLLiteral(`"version"`) + " || ',' || COALESCE(to_jsonb(g.cg_version)::text, 'null')" +
		" || ',' || " + quoteSQLLiteral(`"code"`) + " || ',' || COALESCE(to_jsonb(g.cg_code)::text, 'null')" +
		" || ']'"

	expressions := map[string]string{
		coded.SystemOutputColumn:  "g.cg_system",
		coded.VersionOutputColumn: "g.cg_version",
		coded.CodeOutputColumn:    "g.cg_code",
		coded.CountOutputColumn:   "g.cg_count",
		codedGroupRowIDColumn:     identity,
	}
	for i := range codedRowValues {
		expressions[codedRowValues[i].rowValue.Output] = "g.cg_g" + strconv.Itoa(i)
	}
	selects := make([]string, 0, len(plan.StageSequence.FinalColumns))
	names := make([]string, 0, len(plan.StageSequence.FinalColumns))
	for _, column := range plan.StageSequence.FinalColumns {
		expression, ok := expressions[column.Name]
		if !ok {
			return "", fmt.Errorf("%w: coded-group final column %q is not a registered tuple member",
				ErrUnsupportedPostgresPlan, column.Name)
		}
		for _, seen := range names {
			if seen == column.Name {
				return "", fmt.Errorf("%w: coded-group final column %q is declared twice",
					ErrUnsupportedPostgresPlan, column.Name)
			}
		}
		selects = append(selects, expression+` AS "c`+strconv.Itoa(len(selects))+`"`)
		names = append(names, column.Name)
	}
	if len(names) != 5+len(codedRowValues) {
		return "", fmt.Errorf("%w: the registered slice emits the three tuple keys, the root count, the hidden stable identity, and one column per registered row value",
			ErrUnsupportedPostgresPlan)
	}
	r.outputColumns = names

	final := "SELECT " + strings.Join(selects, ", ") + " FROM cg_grouped AS g" +
		" ORDER BY " + identity + ` COLLATE "C" ASC`
	if plan.StageSequence.PreviewLimitBindKey != "" {
		limit := plan.BindVars[plan.StageSequence.PreviewLimitBindKey].(int)
		index := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(limit)}, "")
		final += " LIMIT $" + strconv.Itoa(index)
	}

	sql := "WITH cg_src AS (" + source +
		"), cg_candidates AS (" + candidates +
		"), cg_tuples AS (" + tuples +
		"), cg_grouped AS (" + grouped + ") " + final
	return sql, nil
}

func (r *planRenderer) codedGroupExpansion(segments []ir.PhysicalCodedGroupPathSegment) (string, string, error) {
	current := "s.cg_payload"
	var expansions []string
	var lastAlias string
	var pending []string
	sequence := 0
	for _, segment := range segments {
		pending = append(pending, segment.Name)
		if !segment.Repeated {
			continue
		}
		extraction, err := r.documentPathFromExpression(current, pending)
		if err != nil {
			return "", "", err
		}
		current = extraction
		pending = nil
		sequence++
		alias := "cgx" + strconv.Itoa(sequence)
		guard := "CASE WHEN jsonb_typeof(" + current + ") = 'array' THEN " + current + " ELSE '[]'::jsonb END"
		expansions = append(expansions, "jsonb_array_elements("+guard+") AS "+alias+"(elem)")
		lastAlias = alias
		current = alias + ".elem"
	}
	if len(pending) != 0 || sequence == 0 {
		return "", "", fmt.Errorf("%w: the Coding path must end in a repeated Coding array", ErrUnsupportedPostgresPlan)
	}
	if sequence == 1 {
		return " LEFT JOIN LATERAL " + expansions[0] + " ON true", "cgx1.elem", nil
	}
	chain := " LEFT JOIN LATERAL (SELECT " + lastAlias + ".elem FROM " +
		strings.Join(expansions, " CROSS JOIN LATERAL ") + ") AS cgexp(elem) ON true"
	return chain, "cgexp.elem", nil
}

func bindStringVar(bindVars map[string]any, key string) string {
	value, _ := bindVars[key].(string)
	return value
}
