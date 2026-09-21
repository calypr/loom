package ir

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

func validatePhysicalGroupedPivot(pivot PhysicalGroupedPivot, defined map[string]bool, bindVars map[string]any) error {
	for _, variable := range []string{pivot.InputRowVariable, pivot.GroupRowsVariable, pivot.OutputRowVariable} {
		if !physicalVariablePattern.MatchString(variable) || defined[variable] {
			return fmt.Errorf("row variable %q is empty, unsafe, or already defined", variable)
		}
	}
	if len(pivot.InputProjections) == 0 || len(pivot.GroupKeys) == 0 || len(pivot.Categories) == 0 {
		return fmt.Errorf("input projections, group keys, and categories are required")
	}
	if strings.TrimSpace(pivot.ConstructionID) == "" {
		return fmt.Errorf("construction ID is required")
	}
	if id, ok := bindVars[pivot.ConstructionIDBindKey].(string); !ok || id != pivot.ConstructionID {
		return fmt.Errorf("construction ID bind does not match construction metadata")
	}
	if strings.TrimSpace(pivot.CategoryColumn) == "" || strings.TrimSpace(pivot.ValueColumn) == "" || pivot.CategoryColumn == pivot.ValueColumn {
		return fmt.Errorf("category and value columns must be non-empty and distinct")
	}
	if !validTableScalarKind(pivot.CategoryType) || !validTableScalarKind(pivot.ValueType) {
		return fmt.Errorf("category and value types must be scalar table types")
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
	case "ERROR", "EXCLUDE_WITH_EVIDENCE":
	default:
		return fmt.Errorf("unsupported unlisted-category policy %q", pivot.UnlistedCategoryPolicy)
	}
	if pivot.UnlistedCategoryPolicy == "EXCLUDE_WITH_EVIDENCE" {
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
		groupColumns[key.Column] = true
		groupVariables[key.Variable] = true
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
	for column := range groupColumns {
		if outputs[column] {
			return fmt.Errorf("category output %q collides with a group key", column)
		}
	}
	if pivot.UnlistedEvidenceColumn != "" {
		if outputs[pivot.UnlistedEvidenceColumn] || groupColumns[pivot.UnlistedEvidenceColumn] || projectionNames[pivot.UnlistedEvidenceColumn] {
			return fmt.Errorf("unlisted-category evidence column collides with an output")
		}
	}
	return nil
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
