package ir

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

const PhysicalPivotUnlistedCategoryExcludeWithEvidence = "EXCLUDE_WITH_EVIDENCE"

func validatePhysicalGroupedPivot(pivot PhysicalGroupedPivot, defined map[string]bool, bindVars map[string]any) error {
	coded := pivot.CodedCorrelation != nil || len(pivot.CodedCategories) != 0 || pivot.CodedSourceVariable != ""
	for _, variable := range []string{pivot.InputRowVariable, pivot.GroupRowsVariable, pivot.OutputRowVariable} {
		if !physicalVariablePattern.MatchString(variable) || defined[variable] {
			return fmt.Errorf("row variable %q is empty, unsafe, or already defined", variable)
		}
	}
	if len(pivot.InputProjections) == 0 || len(pivot.GroupKeys) == 0 || (!coded && len(pivot.Categories) == 0) {
		return fmt.Errorf("input projections, group keys, and categories are required")
	}
	if strings.TrimSpace(pivot.ConstructionID) == "" {
		return fmt.Errorf("construction ID is required")
	}
	if id, ok := bindVars[pivot.ConstructionIDBindKey].(string); !ok || id != pivot.ConstructionID {
		return fmt.Errorf("construction ID bind does not match construction metadata")
	}
	if coded {
		if pivot.CodedCorrelation == nil || len(pivot.CodedCategories) == 0 || len(pivot.CodedCategories) > 50 ||
			len(pivot.Categories) != 0 || pivot.CategoryColumn != "" || pivot.ValueColumn != "" || pivot.CategoryType != "" ||
			pivot.CategoryPresenceColumn != "" || pivot.CategoryPresence != nil || pivot.UnlistedEvidenceColumn != "" ||
			!physicalVariablePattern.MatchString(pivot.CodedSourceVariable) || !pivot.OneInputRowPerGroup || !validTableScalarKind(pivot.ValueType) {
			return fmt.Errorf("coded grouped Pivot requires one direct source, scalar values, and 1..50 coded categories")
		}
	} else {
		if pivot.CodedCorrelation != nil || len(pivot.CodedCategories) != 0 || pivot.CodedSourceVariable != "" {
			return fmt.Errorf("ordinary grouped Pivot cannot carry coded source facts")
		}
		if strings.TrimSpace(pivot.CategoryColumn) == "" || strings.TrimSpace(pivot.ValueColumn) == "" || pivot.CategoryColumn == pivot.ValueColumn {
			return fmt.Errorf("category and value columns must be non-empty and distinct")
		}
		if !validTableScalarKind(pivot.CategoryType) || !validTableScalarKind(pivot.ValueType) {
			return fmt.Errorf("category and value types must be scalar table types")
		}
	}
	if err := requireBind(bindVars, pivot.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	if id, ok := bindVars[pivot.ConstructionIDBindKey].(string); !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("construction ID bind must be a non-empty string")
	}
	switch pivot.DuplicatePolicy {
	case "ERROR", "SUM", "MIN", "MAX":
	default:
		return fmt.Errorf("unsupported duplicate policy %q", pivot.DuplicatePolicy)
	}
	if pivot.DuplicatePolicy != "ERROR" && pivot.ValueType != "INTEGER" && pivot.ValueType != "DECIMAL" {
		return fmt.Errorf("duplicate reducer %q requires a numeric value type", pivot.DuplicatePolicy)
	}
	switch pivot.MissingCellPolicy {
	case "NULL", "ERROR":
	default:
		return fmt.Errorf("unsupported missing-cell policy %q", pivot.MissingCellPolicy)
	}
	switch pivot.UnlistedCategoryPolicy {
	case "ERROR", PhysicalPivotUnlistedCategoryExcludeWithEvidence:
	case "IGNORE":
		if !coded {
			return fmt.Errorf("IGNORE unlisted-category policy is only valid for coded Pivot")
		}
	default:
		return fmt.Errorf("unsupported unlisted-category policy %q", pivot.UnlistedCategoryPolicy)
	}
	if pivot.UnlistedCategoryPolicy == PhysicalPivotUnlistedCategoryExcludeWithEvidence {
		if !physicalPathPartPattern.MatchString(pivot.UnlistedEvidenceColumn) {
			return fmt.Errorf("unlisted-category evidence column is required and must be safe")
		}
	} else if pivot.UnlistedEvidenceColumn != "" {
		return fmt.Errorf("unlisted-category evidence column is only valid for EXCLUDE_WITH_EVIDENCE")
	}
	projectionNames, err := validateTableShapeInputProjections(pivot.InputProjections, defined, bindVars)
	if err != nil {
		return err
	}
	if coded {
		codedDefined := make(map[string]bool, len(defined)+1)
		for variable, isDefined := range defined {
			codedDefined[variable] = isDefined
		}
		codedDefined[pivot.InputRowVariable] = true
		return validatePhysicalGroupedCodedPivot(pivot, projectionNames, codedDefined, bindVars)
	}
	for _, column := range []string{pivot.CategoryColumn, pivot.ValueColumn} {
		if !projectionNames[column] {
			return fmt.Errorf("input projection %q is missing", column)
		}
	}
	var categoryProjectionPresence *PhysicalProjectionPresence
	for _, projection := range pivot.InputProjections {
		if projection.Name == pivot.CategoryColumn {
			categoryProjectionPresence = projection.Presence
			break
		}
	}
	if !sameProjectionPresence(categoryProjectionPresence, pivot.CategoryPresence) {
		return fmt.Errorf("grouped pivot category presence does not match its input projection")
	}
	if pivot.CategoryPresence != nil {
		if !physicalPathPartPattern.MatchString(pivot.CategoryPresenceColumn) || projectionNames[pivot.CategoryPresenceColumn] {
			return fmt.Errorf("grouped pivot category presence column is unsafe or collides with an input projection")
		}
	} else if pivot.CategoryPresenceColumn != "" {
		return fmt.Errorf("grouped pivot category presence column requires a presence contract")
	}
	groupColumns := make(map[string]bool, len(pivot.GroupKeys))
	groupVariables := make(map[string]bool, len(pivot.GroupKeys))
	groupOutputs := make(map[string]bool, len(pivot.GroupKeys))
	for _, key := range pivot.GroupKeys {
		if strings.TrimSpace(key.Column) == "" || !projectionNames[key.Column] || groupColumns[key.Column] {
			return fmt.Errorf("group key column %q is missing or duplicated", key.Column)
		}
		if !physicalVariablePattern.MatchString(key.Variable) || defined[key.Variable] || groupVariables[key.Variable] {
			return fmt.Errorf("group key variable %q is empty, unsafe, or duplicated", key.Variable)
		}
		if !validTableScalarKind(key.Kind) {
			return fmt.Errorf("group key %q has unsupported scalar type %q", key.Column, key.Kind)
		}
		output := key.Output
		if output == "" {
			output = key.Column
		}
		if !physicalPathPartPattern.MatchString(output) || groupOutputs[output] {
			return fmt.Errorf("group key output %q is unsafe or duplicated", output)
		}
		groupColumns[key.Column] = true
		groupVariables[key.Variable] = true
		groupOutputs[output] = true
	}
	if groupColumns[pivot.CategoryColumn] || groupColumns[pivot.ValueColumn] {
		return fmt.Errorf("group keys must differ from category and value columns")
	}
	outputs := make(map[string]bool, len(pivot.Categories))
	binds := make(map[string]bool, len(pivot.Categories))
	identities := make(map[string]bool, len(pivot.Categories))
	for _, category := range pivot.Categories {
		if !physicalPathPartPattern.MatchString(category.Output) || outputs[category.Output] || projectionNames[category.Output] {
			return fmt.Errorf("category output %q is empty, unsafe, or duplicated", category.Output)
		}
		identity := string(category.MatchKind)
		switch category.MatchKind {
		case PhysicalPivotCategoryValueMatch:
			if category.ValueBindKey == "" {
				return fmt.Errorf("category %q ordinary match requires a value bind", category.Output)
			}
			if category.ValueKind != pivot.CategoryType {
				return fmt.Errorf("category %q key type %q does not match category column type %q", category.Output, category.ValueKind, pivot.CategoryType)
			}
			if err := requireBind(bindVars, category.ValueBindKey); err != nil {
				return fmt.Errorf("category %q: %w", category.Output, err)
			}
			if err := validateTableScalarBind(bindVars[category.ValueBindKey], category.ValueKind); err != nil {
				return fmt.Errorf("category %q key: %w", category.Output, err)
			}
			if binds[category.ValueBindKey] {
				return fmt.Errorf("category value bind %q is duplicated", category.ValueBindKey)
			}
			canonical, err := canonicalTableScalarBind(bindVars[category.ValueBindKey], category.ValueKind)
			if err != nil {
				return fmt.Errorf("category %q key: %w", category.Output, err)
			}
			identity += "\x00" + category.ValueKind + "\x00" + canonical
			binds[category.ValueBindKey] = true
		case PhysicalPivotCategoryNullMatch, PhysicalPivotCategoryMissingMatch:
			if category.ValueBindKey != "" || category.ValueKind != "" {
				return fmt.Errorf("category %q sentinel match cannot carry a value bind or value kind", category.Output)
			}
			if category.MatchKind == PhysicalPivotCategoryMissingMatch && pivot.CategoryPresence == nil {
				return fmt.Errorf("category %q MISSING match requires a preserved category presence contract", category.Output)
			}
		default:
			return fmt.Errorf("category %q has unsupported match kind %q", category.Output, category.MatchKind)
		}
		if identities[identity] {
			return fmt.Errorf("category %q duplicates a frozen category key", category.Output)
		}
		identities[identity] = true
		outputs[category.Output] = true
	}
	for column := range groupOutputs {
		if outputs[column] {
			return fmt.Errorf("category output %q collides with a group key output", column)
		}
	}
	if pivot.UnlistedEvidenceColumn != "" {
		if outputs[pivot.UnlistedEvidenceColumn] || groupColumns[pivot.UnlistedEvidenceColumn] || projectionNames[pivot.UnlistedEvidenceColumn] {
			return fmt.Errorf("unlisted-category evidence column collides with an output")
		}
	}
	return nil
}

func validatePhysicalGroupedCodedPivot(pivot PhysicalGroupedPivot, projectionNames, defined map[string]bool, bindVars map[string]any) error {
	if len(pivot.GroupKeys) != 1 || pivot.GroupKeys[0].Column != "_key" || !pivot.GroupKeys[0].Hidden ||
		pivot.GroupKeys[0].Kind != "STRING" || pivot.CodedCorrelation == nil {
		return fmt.Errorf("coded Pivot must group by the retained hidden root _key")
	}
	if !projectionNames[PhysicalCodedPivotSourcePayloadColumn] {
		return fmt.Errorf("coded Pivot source payload projection is required")
	}
	rootKeyProjection, payloadProjection := false, false
	for _, projection := range pivot.InputProjections {
		if projection.Name == "_key" {
			rootKeyProjection = projection.Hidden && projection.Value.Variable == pivot.CodedSourceVariable &&
				len(projection.Value.Path) == 1 && projection.Value.Path[0] == "_key"
		}
		if projection.Name == PhysicalCodedPivotSourcePayloadColumn {
			payloadProjection = projection.Hidden && projection.Value.Variable == pivot.CodedSourceVariable &&
				len(projection.Value.Path) == 1 && projection.Value.Path[0] == "payload"
		}
	}
	if !rootKeyProjection || !payloadProjection {
		return fmt.Errorf("coded Pivot source projection must carry only the direct root key and payload")
	}
	correlation := *pivot.CodedCorrelation
	if correlation.Source.Variable != pivot.InputRowVariable || len(correlation.Source.Path) != 1 || correlation.Source.Path[0] != PhysicalCodedPivotSourcePayloadColumn ||
		correlation.SystemBindKey != pivot.CodedCategories[0].SystemBindKey || correlation.CodeBindKey != pivot.CodedCategories[0].CodeBindKey {
		return fmt.Errorf("coded Pivot correlation must read the payload from its staged root row and bind the first category")
	}
	if tableType, ok := physicalCodedPivotTableType(correlation.LogicalType); !ok || tableType != pivot.ValueType {
		return fmt.Errorf("coded Pivot value type %q does not match correlated logical type %q", pivot.ValueType, correlation.LogicalType)
	}
	outputs, identities, bindKeys := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for index, category := range pivot.CodedCategories {
		if !physicalPathPartPattern.MatchString(category.Output) || outputs[category.Output] || projectionNames[category.Output] {
			return fmt.Errorf("coded Pivot category output %q is unsafe, duplicated, or collides with input", category.Output)
		}
		for name, key := range map[string]string{"system": category.SystemBindKey, "code": category.CodeBindKey} {
			if !physicalBindKeyPattern.MatchString(key) || bindKeys[key] {
				return fmt.Errorf("coded Pivot category %d %s bind is unsafe or duplicated", index, name)
			}
			bindKeys[key] = true
			value, ok := bindVars[key].(string)
			if !ok || strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
				return fmt.Errorf("coded Pivot category %d %s bind must be an exact non-empty string", index, name)
			}
		}
		system, _ := bindVars[category.SystemBindKey].(string)
		code, _ := bindVars[category.CodeBindKey].(string)
		identity := system + "\x00" + code
		if identities[identity] {
			return fmt.Errorf("coded Pivot category %d duplicates a bound system/code pair", index)
		}
		identities[identity], outputs[category.Output] = true, true
		candidate := correlation
		candidate.SystemBindKey, candidate.CodeBindKey = category.SystemBindKey, category.CodeBindKey
		if err := validatePhysicalCorrelation(candidate, defined, bindVars); err != nil {
			return fmt.Errorf("coded Pivot category %d correlation: %w", index, err)
		}
	}
	for _, key := range pivot.GroupKeys {
		if outputs[key.Output] {
			return fmt.Errorf("coded Pivot category output %q collides with its root identity", key.Output)
		}
	}
	return nil
}

func physicalCodedPivotTableType(logicalType string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(logicalType)) {
	case "string", "code", "uuid", "date", "datetime":
		return "STRING", true
	case "integer":
		return "INTEGER", true
	case "decimal":
		return "DECIMAL", true
	case "boolean":
		return "BOOLEAN", true
	default:
		return "", false
	}
}

func validatePhysicalUnpivot(unpivot PhysicalUnpivot, defined map[string]bool, bindVars map[string]any) error {
	for _, variable := range []string{unpivot.InputRowVariable, unpivot.SlotVariable, unpivot.OutputRowVariable} {
		if !physicalVariablePattern.MatchString(variable) || defined[variable] {
			return fmt.Errorf("row variable %q is empty, unsafe, or already defined", variable)
		}
	}
	if len(unpivot.InputProjections) == 0 || len(unpivot.Inputs) == 0 || len(unpivot.IdentityParts) == 0 {
		return fmt.Errorf("input projections and unpivot inputs are required")
	}
	if strings.TrimSpace(unpivot.ConstructionID) == "" {
		return fmt.Errorf("construction ID is required")
	}
	if id, ok := bindVars[unpivot.ConstructionIDBindKey].(string); !ok || id != unpivot.ConstructionID {
		return fmt.Errorf("construction ID bind does not match construction metadata")
	}
	if !physicalPathPartPattern.MatchString(unpivot.KeyOutput) || !physicalPathPartPattern.MatchString(unpivot.ValueOutput) || unpivot.KeyOutput == unpivot.ValueOutput {
		return fmt.Errorf("unpivot key and value outputs must be safe and distinct")
	}
	if !validTableScalarKind(unpivot.KeyType) || !validTableScalarKind(unpivot.ValueType) {
		return fmt.Errorf("unpivot key and value types must be scalar table types")
	}
	if err := requireBind(bindVars, unpivot.ConstructionIDBindKey); err != nil {
		return fmt.Errorf("construction ID: %w", err)
	}
	if id, ok := bindVars[unpivot.ConstructionIDBindKey].(string); !ok || strings.TrimSpace(id) == "" {
		return fmt.Errorf("construction ID bind must be a non-empty string")
	}
	if unpivot.NullRowPolicy != "DROP" && unpivot.NullRowPolicy != "PRESERVE" {
		return fmt.Errorf("unsupported null-row policy %q", unpivot.NullRowPolicy)
	}
	projectionNames, err := validateTableShapeInputProjections(unpivot.InputProjections, defined, bindVars)
	if err != nil {
		return err
	}
	selectedColumns := make(map[string]bool, len(unpivot.Inputs))
	keyBinds := make(map[string]bool, len(unpivot.Inputs))
	keyKind := ""
	for _, input := range unpivot.Inputs {
		if !projectionNames[input.Column] || selectedColumns[input.Column] {
			return fmt.Errorf("unpivot input column %q is missing or duplicated", input.Column)
		}
		if err := requireBind(bindVars, input.KeyBindKey); err != nil {
			return fmt.Errorf("unpivot input %q: %w", input.Column, err)
		}
		if err := validateTableScalarBind(bindVars[input.KeyBindKey], input.KeyKind); err != nil {
			return fmt.Errorf("unpivot input %q key: %w", input.Column, err)
		}
		if !validTableScalarKind(input.ValueKind) || !tableScalarKindsCompatible(unpivot.ValueType, input.ValueKind) {
			return fmt.Errorf("unpivot input %q value type %q is incompatible with output type %q", input.Column, input.ValueKind, unpivot.ValueType)
		}
		if keyKind == "" {
			keyKind = input.KeyKind
		} else if keyKind != input.KeyKind {
			return fmt.Errorf("unpivot input keys must have one canonical scalar type")
		}
		if keyBinds[input.KeyBindKey] {
			return fmt.Errorf("unpivot key bind %q is duplicated", input.KeyBindKey)
		}
		selectedColumns[input.Column] = true
		keyBinds[input.KeyBindKey] = true
	}
	if keyKind != unpivot.KeyType {
		return fmt.Errorf("unpivot key type %q does not match input key type %q", unpivot.KeyType, keyKind)
	}
	if projectionNames[unpivot.KeyOutput] || projectionNames[unpivot.ValueOutput] {
		return fmt.Errorf("unpivot outputs collide with preserved input columns")
	}
	if len(unpivot.PreservedOutputs) > 0 {
		preservedInputs := map[string]bool{}
		for _, projection := range unpivot.InputProjections {
			if !selectedColumns[projection.Name] && projection.Name != "__loom_row_id" {
				preservedInputs[projection.Name] = true
			}
		}
		mappedInputs, mappedOutputs := map[string]bool{}, map[string]bool{}
		for _, output := range unpivot.PreservedOutputs {
			if !preservedInputs[output.InputColumn] || mappedInputs[output.InputColumn] || !physicalPathPartPattern.MatchString(output.OutputColumn) {
				return fmt.Errorf("unpivot preserved output %q has an invalid or duplicate input mapping", output.OutputColumn)
			}
			if output.OutputColumn == unpivot.KeyOutput || output.OutputColumn == unpivot.ValueOutput || mappedOutputs[output.OutputColumn] {
				return fmt.Errorf("unpivot preserved output %q collides with another output", output.OutputColumn)
			}
			mappedInputs[output.InputColumn], mappedOutputs[output.OutputColumn] = true, true
		}
		if len(mappedInputs) != len(preservedInputs) {
			return fmt.Errorf("unpivot preserved output mapping does not cover every retained input")
		}
	}
	identityNames := make(map[string]bool, len(unpivot.IdentityParts))
	for _, part := range unpivot.IdentityParts {
		if !physicalPathPartPattern.MatchString(part.Name) || identityNames[part.Name] {
			return fmt.Errorf("identity part name %q is unsafe or duplicated", part.Name)
		}
		identityNames[part.Name] = true
		if part.Value.Variable == unpivot.InputRowVariable {
			if len(part.Value.Path) != 1 || !projectionNames[part.Value.Path[0]] {
				return fmt.Errorf("identity input column for %q is missing", part.Name)
			}
			continue
		}
		if part.Value.Variable != "" || len(part.Value.Path) != 0 || part.Value.BindKey == "" {
			return fmt.Errorf("identity part %q must reference an input column or scalar bind", part.Name)
		}
		if err := requireBind(bindVars, part.Value.BindKey); err != nil {
			return fmt.Errorf("identity part %q: %w", part.Name, err)
		}
	}
	return nil
}

func validTableScalarKind(kind string) bool {
	switch kind {
	case "STRING", "INTEGER", "DECIMAL", "BOOLEAN":
		return true
	default:
		return false
	}
}

func validateTableScalarBind(value any, kind string) error {
	if !validTableScalarKind(kind) {
		return fmt.Errorf("unsupported scalar type %q", kind)
	}
	valid := false
	switch kind {
	case "STRING":
		_, valid = value.(string)
	case "INTEGER":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			valid = true
		}
	case "DECIMAL":
		switch number := value.(type) {
		case float32:
			valid = !math.IsNaN(float64(number)) && !math.IsInf(float64(number), 0)
		case float64:
			valid = !math.IsNaN(number) && !math.IsInf(number, 0)
		}
	case "BOOLEAN":
		_, valid = value.(bool)
	}
	if !valid {
		return fmt.Errorf("bind value type %T does not match %s", value, kind)
	}
	return nil
}

func validatePhysicalProjectionPresence(presence PhysicalProjectionPresence, defined map[string]bool, bindVars map[string]any) error {
	if err := validatePhysicalValue(presence.Source, defined, bindVars); err != nil {
		return fmt.Errorf("presence source: %w", err)
	}
	if len(presence.Paths) == 0 {
		return fmt.Errorf("presence paths are required")
	}
	for pathIndex, path := range presence.Paths {
		if len(path) == 0 {
			return fmt.Errorf("presence path %d is empty", pathIndex)
		}
		for _, part := range path {
			if !physicalPathPartPattern.MatchString(part) {
				return fmt.Errorf("presence path %d contains unsafe part %q", pathIndex, part)
			}
		}
	}
	return nil
}

func sameProjectionPresence(left, right *PhysicalProjectionPresence) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	if left.Source.Variable != right.Source.Variable || left.Source.BindKey != right.Source.BindKey || !equalStrings(left.Source.Path, right.Source.Path) || len(left.Paths) != len(right.Paths) {
		return false
	}
	for index := range left.Paths {
		if len(left.Paths[index]) != len(right.Paths[index]) {
			return false
		}
		for partIndex := range left.Paths[index] {
			if left.Paths[index][partIndex] != right.Paths[index][partIndex] {
				return false
			}
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func canonicalTableScalarBind(value any, kind string) (string, error) {
	switch kind {
	case "STRING":
		return strconv.Quote(value.(string)), nil
	case "INTEGER":
		switch number := value.(type) {
		case int:
			return strconv.FormatInt(int64(number), 10), nil
		case int8:
			return strconv.FormatInt(int64(number), 10), nil
		case int16:
			return strconv.FormatInt(int64(number), 10), nil
		case int32:
			return strconv.FormatInt(int64(number), 10), nil
		case int64:
			return strconv.FormatInt(number, 10), nil
		case uint:
			return strconv.FormatUint(uint64(number), 10), nil
		case uint8:
			return strconv.FormatUint(uint64(number), 10), nil
		case uint16:
			return strconv.FormatUint(uint64(number), 10), nil
		case uint32:
			return strconv.FormatUint(uint64(number), 10), nil
		case uint64:
			return strconv.FormatUint(number, 10), nil
		}
	case "DECIMAL":
		var number float64
		switch typed := value.(type) {
		case float32:
			number = float64(typed)
		case float64:
			number = typed
		}
		if number == 0 {
			number = 0
		}
		return strconv.FormatFloat(number, 'g', -1, 64), nil
	case "BOOLEAN":
		return strconv.FormatBool(value.(bool)), nil
	}
	return "", fmt.Errorf("unsupported scalar type %q", kind)
}

func tableScalarKindsCompatible(output, input string) bool {
	return output == input || output == "DECIMAL" && input == "INTEGER"
}

func validateTableShapeInputProjections(projections []PhysicalProjection, defined map[string]bool, bindVars map[string]any) (map[string]bool, error) {
	names := make(map[string]bool, len(projections))
	for index, projection := range projections {
		if !physicalPathPartPattern.MatchString(projection.Name) || names[projection.Name] {
			return nil, fmt.Errorf("input projection %d has an empty, unsafe, or duplicate name %q", index, projection.Name)
		}
		if err := validatePhysicalProjection(projection, defined, bindVars); err != nil {
			return nil, fmt.Errorf("input projection %q: %w", projection.Name, err)
		}
		names[projection.Name] = true
	}
	return names, nil
}

func groupedPivotVariables(keys []PhysicalGroupedPivotKey) []string {
	variables := make([]string, 0, len(keys))
	for _, key := range keys {
		variables = append(variables, key.Variable)
	}
	return variables
}
