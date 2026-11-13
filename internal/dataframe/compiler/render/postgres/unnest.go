package postgres

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func RenderUnnest(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateUnnestPlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return RenderPhysicalPlan(plan, storage)
}

func validateUnnestPlanSurface(plan ir.PhysicalPlan) error {
	sawUnnest, sawReturn := false, false
	sawSort, sawLimit := false, false
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: the unnest slice leads with exactly one ROOT_SCAN at position 0, got one at %d", ErrUnsupportedPostgresPlan, index)
			}
			if operation.RootScan == nil {
				return fmt.Errorf("%w: operation %d: ROOT_SCAN payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnnestRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalUnnestOp:
			sawUnnest = true
			if index == 0 {
				return fmt.Errorf("%w: operation %d: UNNEST cannot run before the rows it expands", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: UNNEST must precede the execution window; expansion after SORT/LIMIT would window the pre-expansion rows the window contract orders", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Unnest == nil {
				return fmt.Errorf("%w: operation %d: UNNEST payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnnestOperationShape(plan, *operation.Unnest); err != nil {
				return fmt.Errorf("operation %d: %w", index, err)
			}
		case ir.PhysicalSortOp:
			if operation.Sort == nil {
				return fmt.Errorf("%w: operation %d: SORT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnnest {
				return fmt.Errorf("%w: operation %d: SORT must follow every UNNEST; the window orders final expanded rows, not pre-expansion sources", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: the unnest window admits at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if operation.Limit == nil {
				return fmt.Errorf("%w: operation %d: LIMIT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnnest {
				return fmt.Errorf("%w: operation %d: LIMIT must follow every UNNEST; the window bounds final expanded rows, not pre-expansion sources", ErrUnsupportedPostgresPlan, index)
			}
			if sawLimit {
				return fmt.Errorf("%w: operation %d: the unnest window admits at most one LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			if !sawSort {
				return fmt.Errorf("%w: operation %d: a LIMIT after UNNEST requires the complete stable row identity SORT first", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			sawReturn = true
			if index != len(plan.Operations)-1 {
				return fmt.Errorf("%w: RETURN must be the final operation of the unnest slice", ErrUnsupportedPostgresPlan)
			}
			if operation.Return == nil {
				return fmt.Errorf("%w: the terminal operation must be RETURN with its payload set", ErrUnsupportedPostgresPlan)
			}
			if err := validateUnnestReturn(*operation.Return); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: operation %q cannot coexist with UNNEST in the registered slice; filters, traversal operations, and every other shape beyond the SORT/LIMIT window belong to the read spine or a later capability",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawUnnest {
		return fmt.Errorf("%w: the unnest slice requires at least one UNNEST operation between ROOT_SCAN and RETURN", ErrUnsupportedPostgresPlan)
	}
	if !sawReturn {
		return fmt.Errorf("%w: the unnest slice requires a terminal RETURN after its expansions", ErrUnsupportedPostgresPlan)
	}
	bound := map[string]string{plan.Operations[0].RootScan.Variable: "root scan row"}
	for index, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalUnnestOp {
			continue
		}
		unnest := operation.Unnest
		outputs := []struct {
			role, variable string
		}{
			{"item", unnest.OutputVariable},
			{"item-discriminator", unnest.HasItemVariable},
			{"ordinality", unnest.Ordinality},
		}
		for _, ancestor := range unnest.Ancestors {
			outputs = append(outputs, struct {
				role, variable string
			}{"ancestor", ancestor.Variable})
		}
		for _, output := range outputs {
			if output.variable == "" {
				continue
			}
			if ownerRole, taken := bound[output.variable]; taken {
				return fmt.Errorf("%w: operation %d binds %s variable %q, already bound as the %s; expansion outputs must be unique",
					ErrUnsupportedPostgresPlan, index, output.role, output.variable, ownerRole)
			}
			bound[output.variable] = "operation " + strconv.Itoa(index) + " " + output.role
		}
	}
	return nil
}

func validateUnnestRootScan(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not registered for PostgreSQL; only the direct root collection scan has a rendered contract",
			ErrUnsupportedPostgresPlan)
	}
	if scan.Variable == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if scan.CollectionBindKey == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	if resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	return nil
}

func validateUnnestReturn(ret ir.PhysicalReturn) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: the unnest slice RETURN must project at least one value", ErrUnsupportedPostgresPlan)
	}
	for _, projection := range ret.Projections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set", ErrUnsupportedPostgresPlan)
		}
		if projection.Hidden {
			return fmt.Errorf("%w: projection %q is hidden executor-side metadata with no registered PostgreSQL rendering",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Presence != nil || projection.PresenceOutput {
			return fmt.Errorf("%w: projection %q carries presence metadata; presence proofs pair with selector extractions, which are outside the unnest slice",
				ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Expression != nil {
			if projection.Expression.Kind != ir.PhysicalValueExpression || projection.Expression.Value == nil {
				return fmt.Errorf("%w: projection %q uses expression kind %q; the unnest slice emits direct values and VALUE expressions only, and selector projections over expanded items belong to a later capability",
					ErrUnsupportedPostgresPlan, projection.Name, string(projection.Expression.Kind))
			}
			if projection.Expression.Value.BindKey != "" {
				return fmt.Errorf("%w: projection %q must read a row or derived value, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
			}
			continue
		}
		if projection.Value.BindKey != "" {
			return fmt.Errorf("%w: projection %q must read a row or derived value, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Value.Variable == "" {
			return fmt.Errorf("%w: projection %q must read exactly one typed row or derived value", ErrUnsupportedPostgresPlan, projection.Name)
		}
	}
	return nil
}

func validateUnnestOperationShape(plan ir.PhysicalPlan, unnest ir.PhysicalUnnest) error {
	owner := unnest.Owner
	if owner.RootVariable == "" || owner.OwnerVariable == "" || unnest.OutputVariable == "" || unnest.HasItemVariable == "" {
		return fmt.Errorf("%w: unnest requires root, owner, output, and item-discriminator variables", ErrUnsupportedPostgresPlan)
	}
	switch unnest.EmptyPolicy {
	case ir.PhysicalUnnestExclude, ir.PhysicalUnnestPreserveParent:
	case ir.PhysicalUnnestError:
		return fmt.Errorf("%w: ERROR unnest policy has no proven PostgreSQL contract yet (AQL's ASSERT-style typed raise has no pure-SQL primitive); it is registered as a capability gap rather than approximated",
			ErrUnsupportedPostgresPlan)
	default:
		return fmt.Errorf("%w: unknown unnest empty policy %q", ErrUnsupportedPostgresPlan, string(unnest.EmptyPolicy))
	}

	previous := owner.RootVariable
	for stepIndex, step := range owner.Route {
		if strings.TrimSpace(step.OccurrenceID) == "" {
			return fmt.Errorf("%w: unnest owner route step %d is missing an occurrence ID", ErrUnsupportedPostgresPlan, stepIndex)
		}
		if len(step.Scope) != 0 {
			return fmt.Errorf("%w: unnest owner route step %d carries %d scope operations, which have no registered PostgreSQL rendering",
				ErrUnsupportedPostgresPlan, stepIndex, len(step.Scope))
		}
		switch step.Traversal.Direction {
		case ir.PhysicalOutbound, ir.PhysicalInbound:
		default:
			return fmt.Errorf("%w: unnest owner route step %d has direction %q, which has no proven PostgreSQL contract",
				ErrUnsupportedPostgresPlan, stepIndex, string(step.Traversal.Direction))
		}
		if step.Traversal.Strategy != "" && step.Traversal.Strategy != ir.PhysicalTraversalNative {
			return fmt.Errorf("%w: unnest owner route step %d has strategy %q; native typed-identity joins are the only registered shape",
				ErrUnsupportedPostgresPlan, stepIndex, string(step.Traversal.Strategy))
		}
		if step.Traversal.SourceVariable != previous {
			return fmt.Errorf("%w: unnest owner route step %d starts at %q, want %q",
				ErrUnsupportedPostgresPlan, stepIndex, step.Traversal.SourceVariable, previous)
		}
		previous = step.Traversal.TargetVariable
	}
	if previous != owner.OwnerVariable {
		return fmt.Errorf("%w: unnest route terminates at %q, not owner %q", ErrUnsupportedPostgresPlan, previous, owner.OwnerVariable)
	}

	expression := unnest.Expression
	if expression.Kind != ir.PhysicalExtractExpression || expression.Extract == nil {
		return fmt.Errorf("%w: unnest source must be an EXTRACT selector over the owner row document; value and other expression sources belong to a later capability",
			ErrUnsupportedPostgresPlan)
	}
	if expression.Cardinality != ir.PhysicalArrayCardinality {
		return fmt.Errorf("%w: unnest source expression must be array-valued, got %q", ErrUnsupportedPostgresPlan, string(expression.Cardinality))
	}
	if expression.NullBehavior != ir.PhysicalPreserveNull {
		return fmt.Errorf("%w: unnest source null behavior %q has no registered PostgreSQL form; the expansion normalizes null-or-missing sources per the AQL contract and scalar null behaviors belong to value projections",
			ErrUnsupportedPostgresPlan, string(expression.NullBehavior))
	}
	extract := expression.Extract
	switch extract.ExecutionMode {
	case "", ir.PhysicalSelectorGeneric:
	case ir.PhysicalSelectorDirectScalar:
		return fmt.Errorf("%w: DIRECT_SCALAR selector mode contradicts a repeated terminal step", ErrUnsupportedPostgresPlan)
	case ir.PhysicalSelectorConditionalArray:
		return fmt.Errorf("%w: CONDITIONAL_ARRAY selector mode requires conditional-array rendering, which is not registered for PostgreSQL",
			ErrUnsupportedPostgresPlan)
	default:
		return fmt.Errorf("%w: unknown selector execution mode %q", ErrUnsupportedPostgresPlan, string(extract.ExecutionMode))
	}
	if extract.Prepared != nil {
		return fmt.Errorf("%w: prepared-set selector references are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Fallbacks) != 0 {
		return fmt.Errorf("%w: ordered fallback selectors are a separate capability", ErrUnsupportedPostgresPlan)
	}
	if extract.UnitNormalization != nil {
		return fmt.Errorf("%w: unit-normalized extraction is not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Distinct {
		return fmt.Errorf("%w: DISTINCT expansion is a separate capability: deduplication would erase the duplicate-item preservation this operation exists to provide",
			ErrUnsupportedPostgresPlan)
	}
	if extract.Selector.Filter != nil {
		return fmt.Errorf("%w: selector CONTAINS filters are not implemented for PostgreSQL", ErrUnsupportedPostgresPlan)
	}
	if extract.Source.BindKey != "" {
		return fmt.Errorf("%w: the unnest source must be a row document, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(extract.Source.Path) != 1 || extract.Source.Path[0] != "payload" {
		return fmt.Errorf("%w: the unnest source must be the owner row document at [\"payload\"], the exact source shape lowering emits (lower/row_expansion.go); got %v",
			ErrUnsupportedPostgresPlan, extract.Source.Path)
	}
	if extract.Source.Variable != owner.OwnerVariable {
		return fmt.Errorf("%w: unnest source reads variable %q, want the exact owner occurrence %q",
			ErrUnsupportedPostgresPlan, extract.Source.Variable, owner.OwnerVariable)
	}
	if strings.TrimSpace(extract.ResourceType) == "" || extract.ResourceType != owner.ResourceType {
		return fmt.Errorf("%w: unnest selector schema type %q must name the owner resource type %q",
			ErrUnsupportedPostgresPlan, extract.ResourceType, owner.ResourceType)
	}

	steps := extract.Selector.Steps
	if len(steps) == 0 {
		return fmt.Errorf("%w: the unnest selector is empty", ErrUnsupportedPostgresPlan)
	}
	for stepIndex, step := range steps {
		if step.Field == "" {
			return fmt.Errorf("%w: unnest selector step %d has no property", ErrUnsupportedPostgresPlan, stepIndex)
		}
		if step.Index != nil {
			return fmt.Errorf("%w: unnest selector step %d is indexed; positional selection belongs to a later capability",
				ErrUnsupportedPostgresPlan, stepIndex)
		}
		if !payloadPathSegmentPattern.MatchString(step.Field) {
			return fmt.Errorf("%w: unnest selector step %q is not renderer-safe", ErrUnsupportedPostgresPlan, step.Field)
		}
	}
	if !steps[len(steps)-1].Iterate {
		return fmt.Errorf("%w: the unnest selector must end with a repeated ([]) step, so expansion has a defined terminal cardinality",
			ErrUnsupportedPostgresPlan)
	}
	seenAncestorSteps := make(map[int]bool, len(unnest.Ancestors))
	for ancestorIndex, ancestor := range unnest.Ancestors {
		if ancestor.StepIndex < 0 || ancestor.StepIndex >= len(steps)-1 || !steps[ancestor.StepIndex].Iterate {
			return fmt.Errorf("%w: unnest ancestor %d does not identify a repeated prefix selector step",
				ErrUnsupportedPostgresPlan, ancestorIndex)
		}
		if seenAncestorSteps[ancestor.StepIndex] {
			return fmt.Errorf("%w: unnest repeats ancestor selector step %d", ErrUnsupportedPostgresPlan, ancestor.StepIndex)
		}
		seenAncestorSteps[ancestor.StepIndex] = true
	}
	return nil
}

func (r *planRenderer) renderUnnest(plan ir.PhysicalPlan, unnest ir.PhysicalUnnest) error {
	if r.unnestSeq == 0 {
		if err := validateUnnestPlanSurface(plan); err != nil {
			return err
		}
	}
	if err := validateUnnestOperationShape(plan, unnest); err != nil {
		return err
	}

	for stepIndex, step := range unnest.Owner.Route {
		if err := r.renderTraversal(plan, step.Traversal, true); err != nil {
			return fmt.Errorf("%w: unnest owner route step %d: %v", ErrUnsupportedPostgresPlan, stepIndex, err)
		}
	}
	ownerScope, ok := r.rows[unnest.Owner.OwnerVariable]
	if !ok || ownerScope.derivedValue != "" {
		return fmt.Errorf("%w: unnest owner %q does not bind a readable stored row", ErrUnsupportedPostgresPlan, unnest.Owner.OwnerVariable)
	}
	if ownerScope.resourceType != unnest.Owner.ResourceType {
		return fmt.Errorf("%w: unnest owner %q is scanned as %q, not the declared owner type %q",
			ErrUnsupportedPostgresPlan, unnest.Owner.OwnerVariable, ownerScope.resourceType, unnest.Owner.ResourceType)
	}

	type expansion struct {
		path           []string
		iterateStep    int
		expansionAlias string
		valueColumn    string
	}
	var expansions []expansion
	current := make([]string, 0, 4)
	for stepIndex, step := range unnest.Expression.Extract.Selector.Steps {
		current = append(current, step.Field)
		if step.Iterate {
			expansions = append(expansions, expansion{
				path:        append([]string(nil), current...),
				iterateStep: stepIndex,
			})
			current = nil
		}
	}

	r.unnestSeq++
	lateralAlias := "u" + strconv.Itoa(r.unnestSeq)

	var from strings.Builder
	orderByOrds := make([]string, 0, len(expansions))
	ancestorColumns := make([]string, len(unnest.Ancestors))
	base := r.documentColumn(ownerScope.alias)
	terminal := len(expansions) - 1
	for index, stepExpansion := range expansions {
		extraction, err := r.documentPathFromExpression(base, stepExpansion.path)
		if err != nil {
			return fmt.Errorf("unnest expansion %d: %w", index, err)
		}
		expansionAlias := lateralAlias + "_e" + strconv.Itoa(index)
		valueColumn := "value"
		var sourceForm string
		if index == terminal && len(unnest.Ancestors) == 0 {
			valueColumn = "item"
			sourceForm = unnestSingleLayerForm(extraction)
		} else {
			if index == terminal {
				valueColumn = "item"
			}
			sourceForm = unnestNullToEmptyForm(extraction)
		}
		fragment := "jsonb_array_elements(" + sourceForm + ") WITH ORDINALITY AS " +
			expansionAlias + "(" + valueColumn + ", ord)"
		if index == 0 {
			from.WriteString("FROM " + fragment)
		} else {
			from.WriteString(" CROSS JOIN LATERAL " + fragment)
		}
		expansions[index].expansionAlias = expansionAlias
		expansions[index].valueColumn = valueColumn
		orderByOrds = append(orderByOrds, expansionAlias+".ord")
		base = expansionAlias + ".value"
	}
	for ancestorIndex, ancestor := range unnest.Ancestors {
		var captured string
		for _, stepExpansion := range expansions {
			if stepExpansion.iterateStep == ancestor.StepIndex {
				captured = stepExpansion.expansionAlias + "." + stepExpansion.valueColumn
				break
			}
		}
		if captured == "" {
			return fmt.Errorf("%w: unnest ancestor %d was not captured by an expansion", ErrUnsupportedPostgresPlan, ancestorIndex)
		}
		ancestorColumns[ancestorIndex] = captured + " AS ancestor_" + strconv.Itoa(ancestorIndex)
	}

	rowNumber := "ROW_NUMBER() OVER (ORDER BY " + strings.Join(orderByOrds, ", ") + ") AS rn"
	selectList := expansions[terminal].expansionAlias + "." + expansions[terminal].valueColumn + " AS item"
	for _, column := range ancestorColumns {
		if column != "" {
			selectList += ", " + column
		}
	}
	selectList += ", " + rowNumber
	terminalItem := expansions[terminal].expansionAlias + "." + expansions[terminal].valueColumn
	subquery := "SELECT " + selectList + " " + from.String() +
		" WHERE jsonb_typeof(" + terminalItem + ") <> 'null'"

	join := "CROSS JOIN LATERAL (" + subquery + ") AS " + lateralAlias
	if unnest.EmptyPolicy == ir.PhysicalUnnestPreserveParent {
		join = "LEFT JOIN LATERAL (" + subquery + ") AS " + lateralAlias + " ON true"
	}
	r.laterals = append(r.laterals, join)
	r.lateralAliases = append(r.lateralAliases, lateralAlias)
	r.window.hasUnnest = true

	if err := r.registerDerivedValue(unnest.OutputVariable, lateralAlias+".item", true); err != nil {
		return err
	}
	if unnest.Ordinality != "" {
		if err := r.registerDerivedValue(unnest.Ordinality, lateralAlias+".rn - 1", false); err != nil {
			return err
		}
	}
	if err := r.registerDerivedValue(unnest.HasItemVariable, lateralAlias+".rn IS NOT NULL", false); err != nil {
		return err
	}
	for ancestorIndex, ancestor := range unnest.Ancestors {
		if err := r.registerDerivedValue(ancestor.Variable, lateralAlias+".ancestor_"+strconv.Itoa(ancestorIndex), true); err != nil {
			return err
		}
	}
	return nil
}

func (r *planRenderer) registerDerivedValue(variable, expression string, jsonValue bool) error {
	if _, taken := r.rows[variable]; taken {
		return fmt.Errorf("%w: unnest output variable %q is already bound; expansion output variables must be unique because they name distinct SQL outputs",
			ErrUnsupportedPostgresPlan, variable)
	}
	r.rows[variable] = rowScope{derivedValue: expression, derivedJSON: jsonValue}
	return nil
}

func (r *planRenderer) resolveDerivedValue(value ir.PhysicalValue, scope rowScope) (string, error) {
	if len(value.Path) == 0 {
		return "(" + scope.derivedValue + ")", nil
	}
	if !scope.derivedJSON {
		return "", fmt.Errorf("%w: value %q reads path %v of computed variable %q, which is not a JSON document",
			ErrUnsupportedPostgresPlan, value.Variable, value.Path, value.Variable)
	}
	expression, err := r.documentPathFromExpression(scope.derivedValue, value.Path)
	if err != nil {
		return "", err
	}
	return "(" + expression + ")", nil
}

const unnestFalsyArms = " WHEN (%s) = 'false'::jsonb OR (%s) = '0'::jsonb OR (%s) = '\"\"'::jsonb THEN '[]'::jsonb"

func unnestNullToEmptyForm(extraction string) string {
	return "CASE" +
		" WHEN (" + extraction + ") IS NULL OR jsonb_typeof((" + extraction + ")) = 'null' THEN '[]'::jsonb" +
		" WHEN jsonb_typeof((" + extraction + ")) = 'array' THEN (" + extraction + ")" +
		unnestFalsyArmsWith(extraction) +
		" ELSE (" + extraction + ") END"
}

func unnestSingleLayerForm(extraction string) string {
	return "CASE" +
		" WHEN (" + extraction + ") IS NULL OR jsonb_typeof((" + extraction + ")) = 'null' THEN '[]'::jsonb" +
		" WHEN jsonb_typeof((" + extraction + ")) = 'array' THEN (" + extraction + ")" +
		unnestFalsyArmsWith(extraction) +
		" ELSE jsonb_build_array((" + extraction + ")) END"
}

func unnestFalsyArmsWith(extraction string) string {
	return fmt.Sprintf(unnestFalsyArms, extraction, extraction, extraction)
}
