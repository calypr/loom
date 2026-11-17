package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const exclusionSlotPrefix = "px_"

func tableShapeExclusionPlanMentionsSurface(plan ir.PhysicalPlan) bool {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalTableShapeExclusionReturnOp {
			return true
		}
	}
	return false
}

func RenderTableShapeExclusions(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	return renderTableShapeExclusionPhysicalPlan(plan, storage)
}

func renderTableShapeExclusionPhysicalPlan(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := storage.Validate(); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	if err := validateTableShapeExclusionPlanSurface(plan); err != nil {
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
	sql, err := renderer.renderTableShapeExclusionStatement(plan)
	if err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return command.NewRenderedPostgresPhysicalPlan(sql, renderer.args, renderer.outputColumns)
}

func validateTableShapeExclusionPlanSurface(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	if plan.Operations[0].Kind != ir.PhysicalRootScanOp || plan.Operations[0].RootScan == nil {
		return fmt.Errorf("%w: the exclusion slice leads with exactly one ROOT_SCAN at position 0", ErrUnsupportedPostgresPlan)
	}
	if plan.StageSequence != nil {
		return fmt.Errorf("%w: an exclusion terminal combined with a stage sequence has no PostgreSQL rendering (staged compositions are pg-sql-16)", ErrUnsupportedPostgresPlan)
	}
	if plan.PreviewSourceWindowByRootID {
		return fmt.Errorf("%w: the plan-level preview source window requires terminal windowing contracts that are not registered for the exclusion terminal (pg-sql-16)", ErrUnsupportedPostgresPlan)
	}
	var terminal *ir.PhysicalTableShapeExclusionReturn
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: the exclusion slice leads with exactly one ROOT_SCAN at position 0, got one at %d",
					ErrUnsupportedPostgresPlan, index)
			}
			if operation.RootScan == nil {
				return fmt.Errorf("%w: ROOT_SCAN is missing its payload", ErrUnsupportedPostgresPlan)
			}
			if err := validatePivotRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalFilterOp:
			if operation.Filter == nil {
				return fmt.Errorf("%w: FILTER is missing its payload", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalDerivedLetOp:
			if operation.DerivedLet == nil {
				return fmt.Errorf("%w: DERIVED_LET is missing its payload", ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalTableShapeExclusionReturnOp:
			if index != len(plan.Operations)-1 {
				return fmt.Errorf("%w: the evidence terminal must be the final operation, got position %d of %d",
					ErrUnsupportedPostgresPlan, index, len(plan.Operations))
			}
			if operation.TableShapeExclusionReturn == nil {
				return fmt.Errorf("%w: the evidence terminal is missing its payload", ErrUnsupportedPostgresPlan)
			}
			terminal = operation.TableShapeExclusionReturn
		default:
			return fmt.Errorf("%w: operation %q cannot coexist with the evidence terminal in the registered slice; the terminal owns its own SORT and OFFSET/LIMIT window, and every other shape belongs to the read spine or the pg-sql-16 composition capability",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if terminal == nil {
		return fmt.Errorf("%w: the exclusion slice requires a terminal evidence operation after ROOT_SCAN", ErrUnsupportedPostgresPlan)
	}
	rootVariable := plan.Operations[0].RootScan.Variable
	if err := validateTableShapeExclusionBinds(plan, *terminal); err != nil {
		return err
	}
	pivot := terminal.Pivot
	if pivot.UnlistedCategoryPolicy != string(ir.PhysicalPivotUnlistedCategoryExcludeWithEvidence) {
		return fmt.Errorf("%w: the evidence terminal exists exactly under the EXCLUDE_WITH_EVIDENCE policy, got %q (ir/physical_validation.go re-read; the compiler entry refuses any other policy with TABLE_SHAPE_EXCLUSION_NO_POLICY)",
			ErrUnsupportedPostgresPlan, pivot.UnlistedCategoryPolicy)
	}
	if err := validateGroupedPivotPayload(plan, pivot, rootVariable); err != nil {
		return err
	}
	if _, err := exclusionStableRowColumn(pivot); err != nil {
		return err
	}
	return nil
}

func validateTableShapeExclusionBinds(plan ir.PhysicalPlan, terminal ir.PhysicalTableShapeExclusionReturn) error {
	bind := func(key, field string) (int, error) {
		if strings.TrimSpace(key) == "" {
			return 0, fmt.Errorf("%w: the evidence terminal has no %s bind key", ErrUnsupportedPostgresPlan, field)
		}
		value, ok := plan.BindVars[key].(int)
		if !ok {
			return 0, fmt.Errorf("%w: evidence %s bind %q must be an int", ErrUnsupportedPostgresPlan, field, key)
		}
		return value, nil
	}
	offset, err := bind(terminal.OffsetBindKey, "offset")
	if err != nil {
		return err
	}
	limit, err := bind(terminal.LimitBindKey, "limit")
	if err != nil {
		return err
	}
	fetchLimit, err := bind(terminal.FetchLimitBindKey, "fetch limit")
	if err != nil {
		return err
	}
	if offset < 0 {
		return fmt.Errorf("%w: evidence offset must be non-negative, got %d", ErrUnsupportedPostgresPlan, offset)
	}
	if limit <= 0 {
		return fmt.Errorf("%w: evidence limit must be positive, got %d", ErrUnsupportedPostgresPlan, limit)
	}
	if fetchLimit != limit+1 {
		return fmt.Errorf("%w: evidence fetch limit must be limit+1 (%d), got %d",
			ErrUnsupportedPostgresPlan, limit+1, fetchLimit)
	}
	return nil
}

func exclusionStableRowColumn(pivot ir.PhysicalGroupedPivot) (string, error) {
	stable := ""
	for _, projection := range pivot.InputProjections {
		if projection.Name == pivotRowIDColumn {
			return pivotRowIDColumn, nil
		}
		if projection.Name == "_key" {
			stable = projection.Name
		}
	}
	if stable == "" {
		return "", fmt.Errorf("%w: table-shape exclusion input has no stable row tie-breaker (%s or _key)",
			ErrUnsupportedPostgresPlan, pivotRowIDColumn)
	}
	return stable, nil
}

func exclusionConsumedInputNames(pivot ir.PhysicalGroupedPivot, stableRowColumn string) map[string]bool {
	consumed := map[string]bool{pivot.CategoryColumn: true, stableRowColumn: true}
	for _, key := range pivot.GroupKeys {
		consumed[key.Column] = true
	}
	if pivot.CategoryPresenceFromInput {
		consumed[pivot.CategoryPresenceColumn] = true
	}
	return consumed
}

func (r *planRenderer) renderTableShapeExclusionStatement(plan ir.PhysicalPlan) (string, error) {
	var terminal *ir.PhysicalTableShapeExclusionReturn
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalTableShapeExclusionReturnOp {
			terminal = plan.Operations[index].TableShapeExclusionReturn
		}
	}
	if terminal == nil {
		return "", fmt.Errorf("%w: the exclusion slice requires a terminal evidence operation", ErrUnsupportedPostgresPlan)
	}
	pivot := terminal.Pivot
	rootVariable := plan.Operations[0].RootScan.Variable

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
			if !canonical {
				return "", fmt.Errorf("%w: a non-canonical FILTER cannot coexist with the registered evidence terminal (probe-attested renders-at-AQL divergence, pg-sql-16)",
					ErrUnsupportedPostgresPlan)
			}
		case ir.PhysicalDerivedLetOp:
			canonical, err := r.acceptCanonicalAuthLet(operation.DerivedLet)
			if err != nil {
				return "", err
			}
			if !canonical {
				return "", fmt.Errorf("%w: operation %q is not part of the registered evidence terminal source surface",
					ErrUnsupportedPostgresPlan, string(operation.Kind))
			}
		}
	}
	fromText := r.storage.Resource.Identifier().SQLText() + " AS " + r.rootAlias
	for _, join := range r.joins {
		fromText += " " + join
	}
	for _, lateral := range r.laterals {
		fromText += " " + lateral
	}
	if len(r.where) > 0 {
		fromText += " WHERE " + strings.Join(r.where, " AND ")
	}

	stableRowColumn, err := exclusionStableRowColumn(pivot)
	if err != nil {
		return "", err
	}
	consumed := exclusionConsumedInputNames(pivot, stableRowColumn)
	reads := map[string]string{}
	slotExpressions := make([]string, 0, len(consumed))
	addSlot := func(expression string) string {
		slot := exclusionSlotPrefix + strconv.Itoa(len(slotExpressions))
		slotExpressions = append(slotExpressions, expression+" AS "+slot)
		return "p." + slot
	}
	bareReads := map[string]string{}
	categoryBehavior := ir.PhysicalPreserveNull
	for _, projection := range pivot.InputProjections {
		if !consumed[projection.Name] {
			continue
		}
		if _, rendered := reads[projection.Name]; rendered {
			continue
		}
		expression, behavior, err := r.exclusionInputRead(projection, rootVariable)
		if err != nil {
			return "", err
		}
		slot := exclusionSlotPrefix + strconv.Itoa(len(slotExpressions))
		reads[projection.Name] = addSlot(expression)
		bareReads[projection.Name] = slot
		if projection.Name == pivot.CategoryColumn {
			categoryBehavior = behavior
		}
	}
	pvCat, err := pivotSlotRead(reads, pivot.CategoryColumn)
	if err != nil {
		return "", err
	}
	stableRead, err := pivotSlotRead(reads, stableRowColumn)
	if err != nil {
		return "", err
	}

	present := ""
	switch {
	case pivot.CategoryPresence != nil:
		proofs := make([]string, 0, len(pivot.CategoryPresence.Paths))
		for _, path := range pivot.CategoryPresence.Paths {
			proof, err := r.propertyPresenceExpression(r.rootAlias, path)
			if err != nil {
				return "", err
			}
			proofs = append(proofs, proof)
		}
		present = addSlot("CASE WHEN " + strings.Join(proofs, " OR ") + " THEN true ELSE false END")
	case pivot.CategoryPresenceFromInput:
		if _, err := pivotSlotRead(reads, pivot.CategoryPresenceColumn); err != nil {
			return "", err
		}
		present = addSlot("COALESCE(" + bareReads[pivot.CategoryPresenceColumn] + " = 'true'::jsonb, false)")
	default:
		switch categoryBehavior {
		case ir.PhysicalOmitNulls:
			present = addSlot(bareReads[pivot.CategoryColumn] + " IS NOT NULL")
		default:
			present = addSlot("true")
		}
	}

	matchPredicates := make([]string, 0, len(pivot.Categories))
	for _, category := range pivot.Categories {
		switch category.MatchKind {
		case ir.PhysicalPivotCategoryValueMatch:
			raw, err := pivotCategoryRaw(plan.BindVars, category)
			if err != nil {
				return "", err
			}
			valueParameter := pivotDollar(r.jsonbParameter(raw))
			matchPredicates = append(matchPredicates,
				"("+present+" AND jsonb_typeof("+pvCat+") = "+quoteSQLLiteral(pivotValueTypeOf[category.ValueKind])+
					" AND "+pvCat+" = "+valueParameter+"::jsonb)")
		case ir.PhysicalPivotCategoryNullMatch:
			nullRead := "(" + pvCat + " IS NULL OR " + pvCat + " = 'null'::jsonb)"
			if categoryBehavior == ir.PhysicalOmitNulls {
				nullRead = "(" + pvCat + " IS NOT NULL AND " + pvCat + " = 'null'::jsonb)"
			}
			matchPredicates = append(matchPredicates, "("+present+" AND "+nullRead+")")
		case ir.PhysicalPivotCategoryMissingMatch:
			matchPredicates = append(matchPredicates, "NOT ("+present+")")
		default:
			return "", fmt.Errorf("%w: pivot category match kind %q is not registered",
				ErrUnsupportedPostgresPlan, string(category.MatchKind))
		}
	}
	listed := "(" + strings.Join(matchPredicates, " OR ") + ")"

	keySlots := make([]string, 0, len(pivot.GroupKeys))
	kinds := make([]string, 0, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		expr, err := pivotSlotRead(reads, key.Column)
		if err != nil {
			return "", err
		}
		keySlots = append(keySlots, expr)
		kinds = append(kinds, key.Kind)
	}
	identity := r.pivotIdentitySQL(bindStringVar(plan.BindVars, pivot.ConstructionIDBindKey), kinds, keySlots)

	document := addSlot(r.documentColumn(r.rootAlias))
	sourceDocument := r.documentColumn(r.rootAlias)
	identityAvailable := addSlot("COALESCE(jsonb_typeof(" + sourceDocument + ") = 'object' AND " +
		"jsonb_typeof(" + sourceDocument + " -> 'resourceType') = 'string' AND " +
		exclusionTextMember(sourceDocument, "resourceType") + " <> '' AND " +
		"jsonb_typeof(" + sourceDocument + " -> 'id') = 'string' AND " +
		exclusionTextMember(sourceDocument, "id") + " <> '', false)")

	evidenceValue := "CASE WHEN " + present + " THEN " + pvCat + " END"
	textParameter := func(value, key string) string {
		return pivotDollar(r.textParameter(value, "exclusion:"+key))
	}
	selects := []string{
		"(CASE WHEN " + identityAvailable + " THEN " + document + " -> 'resourceType' END)" +
			` AS "c0"`,
		"(CASE WHEN " + identityAvailable + " THEN " + document + " -> 'id' END)" +
			` AS "c1"`,
		"(CASE WHEN " + identityAvailable + " THEN " + textParameter(ir.PhysicalTableShapeExclusionIdentityExact, "exact") +
			" ELSE " + textParameter(ir.PhysicalTableShapeExclusionIdentityUnavailable, "unavailable") + " END)" +
			` AS "c2"`,
		evidenceValue + ` AS "c3"`,
		"to_jsonb(" + present + ")" + ` AS "c4"`,
		textParameter(pivot.CategoryType, "category-type") + ` AS "c5"`,
		identity + ` AS "c6"`,
		textParameter(ir.PhysicalTableShapeExclusionReasonUnlistedCategory, "reason") + ` AS "c7"`,
		"(CASE WHEN " + identityAvailable + " THEN NULL ELSE " +
			textParameter(ir.PhysicalTableShapeExclusionSourceIdentityUnavailable, "omission") + " END)" + ` AS "c8"`,
	}
	r.outputColumns = []string{
		ir.PhysicalTableShapeExclusionResourceTypeField,
		ir.PhysicalTableShapeExclusionResourceIDField,
		ir.PhysicalTableShapeExclusionIdentityStatusField,
		ir.PhysicalTableShapeExclusionCategoryValueField,
		ir.PhysicalTableShapeExclusionCategoryPresentField,
		ir.PhysicalTableShapeExclusionCategoryTypeField,
		ir.PhysicalTableShapeExclusionOutputRowIDField,
		ir.PhysicalTableShapeExclusionReasonField,
		ir.PhysicalTableShapeExclusionOmissionField,
	}

	order := []string{identity + ` COLLATE "C" ASC`, identityAvailable + " DESC"}
	order = append(order, exclusionTypeRankTerms(document+" -> 'resourceType'")...)
	order = append(order, exclusionTypeRankTerms(document+" -> 'id'")...)
	order = append(order, present+" ASC")
	order = append(order, exclusionTypeRankTerms("CASE WHEN "+present+" THEN "+pvCat+" END")...)
	order = append(order, exclusionTypeRankTerms(stableRead)...)

	statement := "WITH " + exclusionSlotPrefix + "src AS (SELECT " +
		strings.Join(slotExpressions, ", ") + " FROM " + fromText + ") " +
		"SELECT " + strings.Join(selects, ", ") + " FROM " + exclusionSlotPrefix + "src AS p " +
		"WHERE NOT COALESCE(" + listed + ", false) " +
		"ORDER BY " + strings.Join(order, ", ")
	offset := plan.BindVars[terminal.OffsetBindKey].(int)
	fetchLimit := plan.BindVars[terminal.FetchLimitBindKey].(int)
	offsetIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(offset)}, "")
	limitIndex := r.addParameter(command.PostgreSQLParameter{Type: command.PostgreSQLParameterBigint, Value: int64(fetchLimit)}, "")
	statement += " OFFSET " + pivotDollar(offsetIndex) + " LIMIT " + pivotDollar(limitIndex)
	return statement, nil
}

func (r *planRenderer) exclusionInputRead(projection ir.PhysicalProjection, rootVariable string) (string, ir.PhysicalNullBehavior, error) {
	path, behavior, err := pivotInputReadShape(projection, rootVariable)
	if err != nil {
		return "", "", err
	}
	if projection.Expression != nil {
		extraction, err := r.documentPathExpression(r.rootAlias, path)
		if err != nil {
			return "", "", err
		}
		switch behavior {
		case ir.PhysicalOmitNulls:
			return "NULLIF((" + extraction + "), 'null'::jsonb)", behavior, nil
		case ir.PhysicalEmptyOnNull:
			return "COALESCE(NULLIF((" + extraction + "), 'null'::jsonb), '\"\"'::jsonb)", behavior, nil
		default:
			return "(" + extraction + ")", behavior, nil
		}
	}
	if len(path) == 1 {
		if column := canonicalKeyColumn(path[0]); resourceFilterColumns[column] {
			return "to_jsonb(" + r.rootAlias + "." + r.storage.Resource.MustColumn(column).SQLText() + "::text)", behavior, nil
		}
	}
	extraction, err := r.documentPathFromExpression(r.documentColumn(r.rootAlias), path)
	if err != nil {
		return "", "", err
	}
	return "(" + extraction + ")", behavior, nil
}
func exclusionTextMember(document, member string) string {
	return "(" + document + " -> '" + member + "' #>> '{}')"
}

func exclusionTypeRankTerms(expr string) []string {
	return []string{
		"(CASE WHEN " + expr + " IS NULL THEN 0 WHEN jsonb_typeof(" + expr + ") = 'boolean' THEN 1" +
			" WHEN jsonb_typeof(" + expr + ") = 'number' THEN 2 WHEN jsonb_typeof(" + expr + ") = 'string' THEN 3 ELSE 4 END) ASC",
		"(CASE WHEN jsonb_typeof(" + expr + ") = 'boolean' THEN " + expr + "::text END) ASC",
		"((CASE WHEN jsonb_typeof(" + expr + ") = 'number' THEN " + expr + " #>> '{}' END)::double precision) ASC",
		"((CASE WHEN jsonb_typeof(" + expr + ") = 'string' THEN " + expr + " #>> '{}' END) COLLATE \"C\") ASC",
	}
}
