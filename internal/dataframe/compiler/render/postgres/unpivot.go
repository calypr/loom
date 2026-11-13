package postgres

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const unpivotValueTypeTag = "TABLE_UNPIVOT_VALUE_TYPE_MISMATCH"

const unpivotRowIDColumn = "__loom_row_id"

var unpivotValueTypeOf = map[string]string{
	"STRING":  "string",
	"INTEGER": "number",
	"DECIMAL": "number",
	"BOOLEAN": "boolean",
}

func RenderUnpivot(plan ir.PhysicalPlan, storage ir.PostgresStorageBinding) (command.RenderedPostgresPhysicalPlan, error) {
	if err := validateUnpivotPlanSurface(plan); err != nil {
		return command.RenderedPostgresPhysicalPlan{}, err
	}
	return RenderPhysicalPlan(plan, storage)
}

func validateUnpivotPlanSurface(plan ir.PhysicalPlan) error {
	if len(plan.Operations) == 0 {
		return fmt.Errorf("%w: plan has no operations", ErrUnsupportedPostgresPlan)
	}
	sawUnpivot, sawReturn := false, false
	sawSort, sawLimit := false, false
	var melt *ir.PhysicalUnpivot
	for index, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalRootScanOp:
			if index != 0 {
				return fmt.Errorf("%w: the unpivot slice leads with exactly one ROOT_SCAN at position 0, got one at %d", ErrUnsupportedPostgresPlan, index)
			}
			if operation.RootScan == nil {
				return fmt.Errorf("%w: operation %d: ROOT_SCAN payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotRootScan(plan, *operation.RootScan); err != nil {
				return err
			}
		case ir.PhysicalUnpivotOp:
			if sawUnpivot {
				return fmt.Errorf("%w: operation %d: the unpivot slice registers exactly one melt; a second table reshape has no PostgreSQL rendering (multiple melts are construction stage sequences, which stay refused)", ErrUnsupportedPostgresPlan, index)
			}
			sawUnpivot = true
			if index == 0 {
				return fmt.Errorf("%w: operation %d: UNPIVOT cannot run before the rows it melts", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: UNPIVOT must precede the execution window; melting after SORT/LIMIT would window the pre-melt rows the window contract orders", ErrUnsupportedPostgresPlan, index)
			}
			if operation.Unpivot == nil {
				return fmt.Errorf("%w: operation %d: UNPIVOT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotOperationShape(plan, *operation.Unpivot); err != nil {
				return fmt.Errorf("operation %d: %w", index, err)
			}
			melt = operation.Unpivot
		case ir.PhysicalSortOp:
			if operation.Sort == nil {
				return fmt.Errorf("%w: operation %d: SORT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnpivot {
				return fmt.Errorf("%w: operation %d: SORT must follow the melt; the reshaped window orders melted rows, not source rows", ErrUnsupportedPostgresPlan, index)
			}
			if sawSort || sawLimit {
				return fmt.Errorf("%w: operation %d: the unpivot window admits at most one SORT, before LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotSortKeys(*operation.Sort, *melt); err != nil {
				return err
			}
			sawSort = true
		case ir.PhysicalLimitOp:
			if operation.Limit == nil {
				return fmt.Errorf("%w: operation %d: LIMIT payload is missing", ErrUnsupportedPostgresPlan, index)
			}
			if !sawUnpivot {
				return fmt.Errorf("%w: operation %d: LIMIT must follow the melt; the window bounds melted rows, not source rows", ErrUnsupportedPostgresPlan, index)
			}
			if sawLimit {
				return fmt.Errorf("%w: operation %d: the unpivot window admits at most one LIMIT", ErrUnsupportedPostgresPlan, index)
			}
			if !sawSort {
				return fmt.Errorf("%w: operation %d: a LIMIT after UNPIVOT requires the stable row identity SORT first (the ir reshaped-window guard's contract, re-read at the renderer)", ErrUnsupportedPostgresPlan, index)
			}
			sawLimit = true
		case ir.PhysicalReturnOp:
			sawReturn = true
			if index != len(plan.Operations)-1 {
				return fmt.Errorf("%w: RETURN must be the final operation of the unpivot slice", ErrUnsupportedPostgresPlan)
			}
			if operation.Return == nil {
				return fmt.Errorf("%w: the terminal operation must be RETURN with its payload set", ErrUnsupportedPostgresPlan)
			}
			if melt == nil {
				return fmt.Errorf("%w: RETURN at %d precedes the melt; the registered slice reads melt outputs after UNPIVOT", ErrUnsupportedPostgresPlan, index)
			}
			if err := validateUnpivotReturn(*operation.Return, *melt); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: operation %q cannot coexist with UNPIVOT in the registered slice; filters, traversals, unnests, sets, subplans, expression lets, cell trace, and every other shape belong to the read spine or a later capability",
				ErrUnsupportedPostgresPlan, string(operation.Kind))
		}
	}
	if !sawUnpivot {
		return fmt.Errorf("%w: the unpivot slice requires exactly one UNPIVOT between ROOT_SCAN and RETURN", ErrUnsupportedPostgresPlan)
	}
	if !sawReturn {
		return fmt.Errorf("%w: the unpivot slice requires a terminal RETURN after its melt", ErrUnsupportedPostgresPlan)
	}
	return nil
}

func validateUnpivotRootScan(plan ir.PhysicalPlan, scan ir.PhysicalRootScan) error {
	if scan.Population != nil || scan.CohortSource != nil || scan.PageCandidateSeed != nil {
		return fmt.Errorf("%w: population, cohort, and candidate-seed roots are not registered for PostgreSQL; only the direct root collection scan has a rendered contract",
			ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(scan.Variable) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name its row variable", ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(scan.CollectionBindKey) == "" {
		return fmt.Errorf("%w: ROOT_SCAN must name the bind carrying the single resource type", ErrUnsupportedPostgresPlan)
	}
	if resourceType, ok := plan.BindVars[scan.CollectionBindKey].(string); !ok || strings.TrimSpace(resourceType) == "" {
		return fmt.Errorf("%w: root collection bind %q must name one resource type as a non-empty string",
			ErrUnsupportedPostgresPlan, scan.CollectionBindKey)
	}
	return nil
}

func validateUnpivotOperationShape(plan ir.PhysicalPlan, unpivot ir.PhysicalUnpivot) error {
	if unpivot.InputRowVariable == "" || unpivot.SlotVariable == "" || unpivot.OutputRowVariable == "" {
		return fmt.Errorf("%w: unpivot requires input-row, slot, and output-row variables", ErrUnsupportedPostgresPlan)
	}
	if unpivot.InputRowVariable == unpivot.OutputRowVariable || unpivot.SlotVariable == unpivot.OutputRowVariable || unpivot.SlotVariable == unpivot.InputRowVariable {
		return fmt.Errorf("%w: unpivot variables must be distinct; they name distinct SQL placements", ErrUnsupportedPostgresPlan)
	}
	if len(unpivot.InputProjections) == 0 || len(unpivot.Inputs) == 0 || len(unpivot.IdentityParts) == 0 {
		return fmt.Errorf("%w: unpivot requires input projections, at least one selected input, and identity parts", ErrUnsupportedPostgresPlan)
	}
	if strings.TrimSpace(unpivot.ConstructionID) == "" {
		return fmt.Errorf("%w: unpivot construction ID is required", ErrUnsupportedPostgresPlan)
	}
	if _, ok := plan.BindVars[unpivot.ConstructionIDBindKey].(string); !ok {
		return fmt.Errorf("%w: unpivot construction ID bind %q must carry a string", ErrUnsupportedPostgresPlan, unpivot.ConstructionIDBindKey)
	}
	if plan.BindVars[unpivot.ConstructionIDBindKey].(string) != unpivot.ConstructionID {
		return fmt.Errorf("%w: unpivot construction ID bind does not match construction metadata", ErrUnsupportedPostgresPlan)
	}
	if unpivot.KeyOutput == "" || unpivot.ValueOutput == "" || unpivot.KeyOutput == unpivot.ValueOutput {
		return fmt.Errorf("%w: unpivot key and value outputs must be non-empty and distinct", ErrUnsupportedPostgresPlan)
	}
	if _, ok := unpivotValueTypeOf[unpivot.KeyType]; !ok {
		return fmt.Errorf("%w: unpivot key type %q is outside the closed scalar registry", ErrUnsupportedPostgresPlan, unpivot.KeyType)
	}
	if _, ok := unpivotValueTypeOf[unpivot.ValueType]; !ok {
		return fmt.Errorf("%w: unpivot value type %q is outside the closed scalar registry", ErrUnsupportedPostgresPlan, unpivot.ValueType)
	}
	if unpivot.NullRowPolicy != "DROP" && unpivot.NullRowPolicy != "PRESERVE" {
		return fmt.Errorf("%w: unsupported null-row policy %q", ErrUnsupportedPostgresPlan, unpivot.NullRowPolicy)
	}
	projectionNames := make(map[string]bool, len(unpivot.InputProjections))
	for _, projection := range unpivot.InputProjections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: unpivot input projection names are required", ErrUnsupportedPostgresPlan)
		}
		if projectionNames[projection.Name] {
			return fmt.Errorf("%w: unpivot input projection %q is duplicated", ErrUnsupportedPostgresPlan, projection.Name)
		}
		projectionNames[projection.Name] = true
	}
	if projectionNames[unpivot.KeyOutput] || projectionNames[unpivot.ValueOutput] {
		return fmt.Errorf("%w: unpivot key or value output collides with an input projection", ErrUnsupportedPostgresPlan)
	}
	selected := make(map[string]bool, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		if input.Column == "" || !projectionNames[input.Column] {
			return fmt.Errorf("%w: unpivot input column %q is not an input projection", ErrUnsupportedPostgresPlan, input.Column)
		}
		if selected[input.Column] {
			return fmt.Errorf("%w: unpivot input column %q is duplicated", ErrUnsupportedPostgresPlan, input.Column)
		}
		selected[input.Column] = true
		if input.KeyBindKey == "" {
			return fmt.Errorf("%w: unpivot input %q requires a key bind", ErrUnsupportedPostgresPlan, input.Column)
		}
		if input.KeyKind != unpivot.KeyType {
			return fmt.Errorf("%w: unpivot input %q key kind %q does not match the canonical key type %q",
				ErrUnsupportedPostgresPlan, input.Column, input.KeyKind, unpivot.KeyType)
		}
		if !unpivotValueKindCompatible(unpivot.ValueType, input.ValueKind) {
			return fmt.Errorf("%w: unpivot input %q value kind %q is incompatible with the output type %q",
				ErrUnsupportedPostgresPlan, input.Column, input.ValueKind, unpivot.ValueType)
		}
	}
	preservedInputs := make(map[string]bool, len(unpivot.PreservedOutputs))
	preservedOutputs := make(map[string]bool, len(unpivot.PreservedOutputs))
	for _, output := range unpivot.PreservedOutputs {
		if output.InputColumn == "" || output.OutputColumn == "" {
			return fmt.Errorf("%w: unpivot preserved output mapping is incomplete", ErrUnsupportedPostgresPlan)
		}
		if !selected[output.InputColumn] && !projectionNames[output.InputColumn] {
			return fmt.Errorf("%w: unpivot preserved input %q is not an input projection", ErrUnsupportedPostgresPlan, output.InputColumn)
		}
		if preservedInputs[output.InputColumn] || preservedOutputs[output.OutputColumn] {
			return fmt.Errorf("%w: unpivot preserved output mapping duplicates %q", ErrUnsupportedPostgresPlan, output.InputColumn)
		}
		if output.OutputColumn == unpivot.KeyOutput || output.OutputColumn == unpivot.ValueOutput ||
			output.OutputColumn == unpivotRowIDColumn {
			return fmt.Errorf("%w: unpivot preserved output %q collides with the key, value, or stable row identity output",
				ErrUnsupportedPostgresPlan, output.OutputColumn)
		}
		preservedInputs[output.InputColumn], preservedOutputs[output.OutputColumn] = true, true
	}
	identityNames := make(map[string]bool, len(unpivot.IdentityParts))
	for _, part := range unpivot.IdentityParts {
		if part.Name == "" || identityNames[part.Name] {
			return fmt.Errorf("%w: unpivot identity part name is empty or duplicated", ErrUnsupportedPostgresPlan)
		}
		identityNames[part.Name] = true
		if part.Value.Variable == unpivot.InputRowVariable {
			if len(part.Value.Path) != 1 || !projectionNames[part.Value.Path[0]] {
				return fmt.Errorf("%w: unpivot identity part %q references an input column that is not an input projection",
					ErrUnsupportedPostgresPlan, part.Name)
			}
			continue
		}
		if part.Value.Variable != "" || len(part.Value.Path) != 0 || part.Value.BindKey == "" {
			return fmt.Errorf("%w: unpivot identity part %q must reference the input row by one column or a scalar bind",
				ErrUnsupportedPostgresPlan, part.Name)
		}
	}
	return nil
}

func unpivotValueKindCompatible(output, input string) bool {
	return output == input || output == "DECIMAL" && input == "INTEGER"
}

func validateUnpivotSortKeys(sort ir.PhysicalSort, unpivot ir.PhysicalUnpivot) error {
	if len(sort.Keys) != 1 {
		return fmt.Errorf("%w: the reshaped execution SORT must carry exactly the stable row identity key", ErrUnsupportedPostgresPlan)
	}
	key := sort.Keys[0]
	if key.Variable != unpivot.OutputRowVariable || key.BindKey != "" || len(key.Path) != 1 || key.Path[0] != unpivotRowIDColumn {
		return fmt.Errorf("%w: the reshaped execution SORT must sort on %s.%s exactly",
			ErrUnsupportedPostgresPlan, unpivot.OutputRowVariable, unpivotRowIDColumn)
	}
	return nil
}

func validateUnpivotReturn(ret ir.PhysicalReturn, unpivot ir.PhysicalUnpivot) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: the unpivot slice RETURN must project at least one value", ErrUnsupportedPostgresPlan)
	}
	legal := unpivotLegalOutputNames(unpivot)
	for _, projection := range ret.Projections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set", ErrUnsupportedPostgresPlan)
		}
		if projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput {
			return fmt.Errorf("%w: melt projection %q uses %s; the melt RETURN reads only registered output columns by direct value",
				ErrUnsupportedPostgresPlan, projection.Name, unpivotProjectionForm(projection))
		}
		if projection.Value.BindKey != "" {
			return fmt.Errorf("%w: melt projection %q must read an output column, not a bind value", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if projection.Value.Variable != unpivot.OutputRowVariable {
			return fmt.Errorf("%w: melt projection %q reads variable %q; the melt RETURN reads only the melt output variable %q",
				ErrUnsupportedPostgresPlan, projection.Name, projection.Value.Variable, unpivot.OutputRowVariable)
		}
		if len(projection.Value.Path) != 1 {
			return fmt.Errorf("%w: melt projection %q must read the output column by exactly one name", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if !legal[projection.Value.Path[0]] {
			return fmt.Errorf("%w: melt projection %q reads %q, which is not a registered melt output",
				ErrUnsupportedPostgresPlan, projection.Name, projection.Value.Path[0])
		}
	}
	return nil
}

func unpivotProjectionForm(projection ir.PhysicalProjection) string {
	switch {
	case projection.PresenceOutput:
		return "presence-sidecar metadata"
	case projection.Presence != nil:
		return "presence metadata"
	default:
		return fmt.Sprintf("expression kind %q", string(projection.Expression.Kind))
	}
}

func unpivotLegalOutputNames(unpivot ir.PhysicalUnpivot) map[string]bool {
	legal := map[string]bool{unpivot.KeyOutput: true, unpivot.ValueOutput: true, unpivotRowIDColumn: true}
	for _, output := range unpivot.PreservedOutputs {
		legal[output.OutputColumn] = true
	}
	return legal
}

func (r *planRenderer) renderUnpivot(plan ir.PhysicalPlan, unpivot ir.PhysicalUnpivot) error {
	if r.unpivotSeq == 0 {
		if err := validateUnpivotPlanSurface(plan); err != nil {
			return err
		}
	}
	if err := validateUnpivotOperationShape(plan, unpivot); err != nil {
		return err
	}
	if _, taken := r.unpivotOutputs[unpivot.OutputRowVariable]; taken {
		return fmt.Errorf("%w: melt output variable %q is already bound; melt outputs name distinct SQL placements",
			ErrUnsupportedPostgresPlan, unpivot.OutputRowVariable)
	}
	if _, taken := r.rows[unpivot.OutputRowVariable]; taken {
		return fmt.Errorf("%w: melt output variable %q collides with a stored row variable",
			ErrUnsupportedPostgresPlan, unpivot.OutputRowVariable)
	}

	classified, err := classifyExtractionProjections(unpivot.InputProjections, func(variable string) (string, bool) {
		scope, ok := r.rows[variable]
		return scope.resourceType, ok
	})
	if err != nil {
		return fmt.Errorf("unpivot input projections: %w", err)
	}
	inputExprs := make(map[string]string, len(classified))
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
				return fmt.Errorf("unpivot input %q: %w", projection.projection.Name, err)
			}
			expression = resolved
			if isPayloadExpression(expression) {
				expression = "(" + expression + ")"
			}
		case extractionSelector:
			scope := r.rows[projection.expression.Extract.Source.Variable]
			value, err := r.scalarExtractValueExpression(scope.alias, projection.path, projection.expression.NullBehavior)
			if err != nil {
				return fmt.Errorf("unpivot input %q: %w", projection.projection.Name, err)
			}
			expression = value
		case extractionPresenceSidecar:
			scope := r.rows[projection.presence.Source.Variable]
			presence, err := r.propertyPresenceExpression(scope.alias, projection.presencePath)
			if err != nil {
				return fmt.Errorf("unpivot input %q: %w", projection.projection.Name, err)
			}
			expression = presence
		case extractionObject, extractionSlice:
			return fmt.Errorf("%w: unpivot input %q uses a %s expression; melt inputs are the spine's scalar/selector projections, composites belong to the read spine registration (objects.go)",
				ErrUnsupportedPostgresPlan, projection.projection.Name, string(projection.projection.Expression.Kind))
		}
		inputExprs[projection.projection.Name] = expression
	}

	r.unpivotSeq++
	alias := "x" + strconv.Itoa(r.unpivotSeq)

	region, err := r.renderMeltRegion(plan, unpivot, inputExprs, alias)
	if err != nil {
		return err
	}
	r.laterals = append(r.laterals, region.lateral)
	r.where = append(r.where, region.conjuncts...)
	if r.unpivotOutputs == nil {
		r.unpivotOutputs = map[string]map[string]string{}
	}
	r.unpivotOutputs[unpivot.OutputRowVariable] = region.columns

	r.window.hasUnpivot = true
	r.window.unpivotOrdinals = append(r.window.unpivotOrdinals, region.ordinal)
	return nil
}

type meltRegionResult struct {
	lateral   string
	conjuncts []string
	columns   map[string]string
	ordinal   string
}

func (r *planRenderer) renderMeltRegion(plan ir.PhysicalPlan, unpivot ir.PhysicalUnpivot, inputExprs map[string]string, alias string) (meltRegionResult, error) {
	rows := make([]string, 0, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		raw, err := unpivotKeyJSON(plan.BindVars[input.KeyBindKey], input.KeyKind)
		if err != nil {
			return meltRegionResult{}, fmt.Errorf("unpivot input %q: %w", input.Column, err)
		}
		keyIndex := r.jsonbParameter(raw)
		expression, ok := inputExprs[input.Column]
		if !ok {
			return meltRegionResult{}, fmt.Errorf("%w: unpivot input %q has no rendered projection expression", ErrUnsupportedPostgresPlan, input.Column)
		}
		rows = append(rows, fmt.Sprintf("($%d::jsonb, to_jsonb(%s), %d)", keyIndex, expression, len(rows)+1))
	}
	lateral := "CROSS JOIN LATERAL (VALUES " + strings.Join(rows, ", ") + ") AS " +
		alias + "(slot_key, slot_value, slot_ord)"

	slotValue := alias + ".slot_value"
	valueTypeOf := unpivotValueTypeOf[unpivot.ValueType]
	bad := "(" + slotValue + " IS NOT NULL AND " + slotValue + " <> 'null'::jsonb AND jsonb_typeof(" + slotValue + ") <> " + quoteSQLLiteral(valueTypeOf) + ")"
	conjuncts := []string{"(CASE WHEN " + bad + " THEN " + guardCast(unpivotValueTypeTag) + " ELSE 1 END) = 1"}
	if unpivot.NullRowPolicy == "DROP" {
		conjuncts = append(conjuncts, "("+slotValue+" IS NOT NULL AND "+slotValue+" <> 'null'::jsonb)")
	}

	columns := map[string]string{
		unpivot.KeyOutput:   alias + ".slot_key",
		unpivot.ValueOutput: slotValue,
	}
	preservedNames := make(map[string]string, len(unpivot.PreservedOutputs))
	for _, output := range unpivot.PreservedOutputs {
		preservedNames[output.InputColumn] = output.OutputColumn
	}
	selectedColumns := make(map[string]bool, len(unpivot.Inputs))
	for _, input := range unpivot.Inputs {
		selectedColumns[input.Column] = true
	}
	for _, projection := range unpivot.InputProjections {
		if selectedColumns[projection.Name] {
			continue
		}
		if projection.Name == unpivotRowIDColumn {
			continue
		}
		outputName := projection.Name
		if renamed, ok := preservedNames[projection.Name]; ok {
			outputName = renamed
		}
		expression, ok := inputExprs[projection.Name]
		if !ok {
			return meltRegionResult{}, fmt.Errorf("%w: unpivot preserved column %q has no rendered projection expression", ErrUnsupportedPostgresPlan, projection.Name)
		}
		if _, taken := columns[outputName]; taken {
			return meltRegionResult{}, fmt.Errorf("%w: melt output column %q is emitted twice", ErrUnsupportedPostgresPlan, outputName)
		}
		columns[outputName] = expression
	}
	columns[unpivotRowIDColumn] = r.unpivotIdentityExpression(plan, unpivot, inputExprs, alias)
	if columns[unpivotRowIDColumn] == "" {
		return meltRegionResult{}, fmt.Errorf("%w: unpivot identity expression failed", ErrUnsupportedPostgresPlan)
	}
	return meltRegionResult{lateral: lateral, conjuncts: conjuncts, columns: columns, ordinal: alias + ".slot_ord"}, nil
}

func unpivotKeyJSON(value any, kind string) (json.RawMessage, error) {
	switch kind {
	case "STRING":
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under STRING", ErrUnsupportedPostgresPlan, value)
		}
		return marshalUnpivotKey(text)
	case "INTEGER":
		var integer int64
		switch typed := value.(type) {
		case int:
			integer = int64(typed)
		case int8:
			integer = int64(typed)
		case int16:
			integer = int64(typed)
		case int32:
			integer = int64(typed)
		case int64:
			integer = typed
		case uint:
			integer = int64(typed)
		case uint8:
			integer = int64(typed)
		case uint16:
			integer = int64(typed)
		case uint32:
			integer = int64(typed)
		case uint64:
			if typed > 1<<53 {
				return nil, fmt.Errorf("%w: unpivot INTEGER key %d exceeds the 2^53 double round-trip bound", ErrUnsupportedPostgresPlan, typed)
			}
			integer = int64(typed)
		default:
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under INTEGER", ErrUnsupportedPostgresPlan, value)
		}
		if integer > 1<<53 || integer < -(1<<53) {
			return nil, fmt.Errorf("%w: unpivot INTEGER key %d exceeds the 2^53 double round-trip bound", ErrUnsupportedPostgresPlan, integer)
		}
		return marshalUnpivotKey(integer)
	case "DECIMAL":
		var number float64
		switch typed := value.(type) {
		case float32:
			number = float64(typed)
		case float64:
			number = typed
		default:
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under DECIMAL", ErrUnsupportedPostgresPlan, value)
		}
		if !(number == number) || number > 1.7976931348623157e308 || number < -1.7976931348623157e308 {
			return nil, fmt.Errorf("%w: unpivot DECIMAL key bind must be finite", ErrUnsupportedPostgresPlan)
		}
		return marshalUnpivotKey(number)
	case "BOOLEAN":
		boolean, ok := value.(bool)
		if !ok {
			return nil, fmt.Errorf("%w: unpivot key bind carries %T under BOOLEAN", ErrUnsupportedPostgresPlan, value)
		}
		return marshalUnpivotKey(boolean)
	default:
		return nil, fmt.Errorf("%w: unpivot key kind %q is outside the closed registry", ErrUnsupportedPostgresPlan, kind)
	}
}

func marshalUnpivotKey(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil || string(raw) == "null" {
		return nil, fmt.Errorf("%w: unpivot key is not JSON-encodable", ErrUnsupportedPostgresPlan)
	}
	return raw, nil
}

func (r *planRenderer) unpivotIdentityExpression(plan ir.PhysicalPlan, unpivot ir.PhysicalUnpivot, inputExprs map[string]string, alias string) string {
	pairs := make([]string, 0, len(unpivot.IdentityParts)+2)
	for _, part := range unpivot.IdentityParts {
		nameIndex := r.textParameter(part.Name, "unpivot-identity-name:"+part.Name)
		var valueText string
		if part.Value.Variable == unpivot.InputRowVariable {
			expression, ok := inputExprs[part.Value.Path[0]]
			if !ok {
				return ""
			}
			valueText = "COALESCE(to_jsonb(" + expression + "), 'null'::jsonb)::text"
		} else {
			bound, ok := plan.BindVars[part.Value.BindKey]
			if !ok {
				return ""
			}
			text, ok := bound.(string)
			if !ok {
				return ""
			}
			bindIndex := r.textParameter(text, "unpivot-identity-bind:"+part.Value.BindKey+":"+text)
			valueText = "to_jsonb($" + strconv.Itoa(bindIndex) + "::text)::text"
		}
		pairs = append(pairs, "'[' || to_jsonb($"+strconv.Itoa(nameIndex)+"::text) || ',' || "+valueText+" || ']'")
	}
	constructionIndex := r.textParameter(unpivot.ConstructionID, "unpivot-construction-id:"+unpivot.ConstructionID)
	pairs = append(pairs, "'[' || to_jsonb($"+strconv.Itoa(constructionIndex)+"::text) || ',' || "+quoteSQLLiteral(`"TABLE_UNPIVOT"`)+" || ']'")
	keyKind := unpivot.Inputs[0].KeyKind
	pairs = append(pairs, "'[' || "+quoteSQLLiteral(`"`+keyKind+`"`)+" || ',' || "+alias+".slot_key::text || ']'")
	return "'[' || " + strings.Join(pairs, " || ',' || ") + " || ']'"
}

func (r *planRenderer) resolveUnpivotOutputValue(columns map[string]string, value ir.PhysicalValue) (string, error) {
	if value.BindKey != "" {
		return "", fmt.Errorf("%w: melt reads must name an output column, not a bind value", ErrUnsupportedPostgresPlan)
	}
	if len(value.Path) != 1 {
		return "", fmt.Errorf("%w: melt reads must name exactly one output column, got %v", ErrUnsupportedPostgresPlan, value.Path)
	}
	expression, ok := columns[value.Path[0]]
	if !ok {
		return "", fmt.Errorf("%w: %q is not a registered melt output", ErrUnsupportedPostgresPlan, value.Path[0])
	}
	return "(" + expression + ")", nil
}

func (r *planRenderer) renderUnpivotReturn(ret ir.PhysicalReturn) error {
	if len(ret.Projections) == 0 {
		return fmt.Errorf("%w: the unpivot slice RETURN must project at least one value", ErrUnsupportedPostgresPlan)
	}
	for _, projection := range ret.Projections {
		if strings.TrimSpace(projection.Name) == "" {
			return fmt.Errorf("%w: projection names are required output metadata (never SQL text) and must be set", ErrUnsupportedPostgresPlan)
		}
		if projection.Expression != nil || projection.Presence != nil || projection.PresenceOutput {
			return fmt.Errorf("%w: melt projection %q carries %s", ErrUnsupportedPostgresPlan, projection.Name, unpivotProjectionForm(projection))
		}
		columns, ok := r.unpivotOutputs[projection.Value.Variable]
		if !ok {
			return fmt.Errorf("%w: melt projection %q reads variable %q, which never bound a melt",
				ErrUnsupportedPostgresPlan, projection.Name, projection.Value.Variable)
		}
		expression, err := r.resolveUnpivotOutputValue(columns, projection.Value)
		if err != nil {
			return fmt.Errorf("projection %q: %w", projection.Name, err)
		}
		r.selects = append(r.selects, expression+` AS "c`+strconv.Itoa(len(r.selects))+`"`)
		r.outputColumns = append(r.outputColumns, projection.Name)
	}
	return nil
}

func (r *planRenderer) renderFinalWindowSortUnpivot(sort ir.PhysicalSort) error {
	if r.window.sortSeen {
		return fmt.Errorf("%w: at most one SORT", ErrUnsupportedPostgresPlan)
	}
	if len(r.window.unpivotOrdinals) == 0 {
		return fmt.Errorf("%w: the melt window orders rows before the melt bound", ErrUnsupportedPostgresPlan)
	}
	if len(sort.Keys) != 1 {
		return fmt.Errorf("%w: the reshaped execution SORT must carry exactly the stable row identity key", ErrUnsupportedPostgresPlan)
	}
	key := sort.Keys[0]
	columns, ok := r.unpivotOutputs[key.Variable]
	if !ok || key.BindKey != "" || len(key.Path) != 1 || key.Path[0] != unpivotRowIDColumn {
		return fmt.Errorf("%w: the reshaped execution SORT must sort on the melt stable row identity column", ErrUnsupportedPostgresPlan)
	}
	identity, ok := columns[unpivotRowIDColumn]
	if !ok {
		return fmt.Errorf("%w: the melt stable row identity column is not registered", ErrUnsupportedPostgresPlan)
	}
	r.window.keys = append(r.window.keys, "("+identity+") ASC")
	return nil
}
