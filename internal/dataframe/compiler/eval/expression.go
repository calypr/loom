package eval

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/spec"
)

type expression interface {
	evaluate(*evaluationScope) (any, error)
	references(map[string]struct{})
}

type evaluationScope struct {
	rootVariable string
	variables    map[string]any
	bindVars     map[string]any
	sets         map[string]struct{}
	uses         map[string]int
}

type missingSentinel struct{}

var missingValue any = missingSentinel{}

type valueExpression struct {
	value ir.PhysicalValue
}

type extractExpression struct {
	cardinality ir.PhysicalCardinality
	extract     ir.PhysicalExtract
}

type keyedMapExpression struct {
	itemVariable string
	source       expression
	key          expression
	value        expression
}

type objectLookupExpression struct {
	objectVariable string
	key            string
	behavior       ir.PhysicalNullBehavior
}

type objectKeysExpression struct {
	objectVariable string
	behavior       ir.PhysicalNullBehavior
}

type compiledObjectField struct {
	name       string
	expression expression
	behavior   ir.PhysicalNullBehavior
}

type objectExpression struct {
	fields []compiledObjectField
}

func compileExpression(value ir.PhysicalExpression, binds map[string]any) (expression, error) {
	switch value.Kind {
	case ir.PhysicalValueExpression:
		return compileValue(value, binds)
	case ir.PhysicalExtractExpression:
		if value.Extract == nil {
			return nil, unsupported("EXTRACT payload is missing")
		}
		extract := *value.Extract
		if len(extract.Fallbacks) != 0 || extract.Selector.Filter != nil || extract.Prepared != nil || extract.UnitNormalization != nil || extract.Distinct {
			return nil, unsupported("EXTRACT flags are outside the captured subset")
		}
		if err := validateSelectorSubset(extract.Selector); err != nil {
			return nil, err
		}
		switch {
		case extract.ExecutionMode == ir.PhysicalSelectorDirectScalar && value.Cardinality == ir.PhysicalScalarCardinality:
		case extract.ExecutionMode == ir.PhysicalSelectorConditionalArray && value.Cardinality == ir.PhysicalArrayCardinality:
		default:
			return nil, unsupported("EXTRACT mode/cardinality %q/%q is unsupported", extract.ExecutionMode, value.Cardinality)
		}
		return extractExpression{cardinality: value.Cardinality, extract: extract}, nil
	case ir.PhysicalPivotExpression:
		if value.Pivot == nil || value.Pivot.Correlation == nil {
			return nil, unsupported("PIVOT_MAP requires a captured correlation")
		}
		pivot, err := compileCorrelatedPivot(*value.Pivot, binds)
		if err != nil {
			return nil, err
		}
		return pivot, nil
	case ir.PhysicalKeyedMapExpression:
		if value.KeyedMap == nil {
			return nil, unsupported("KEYED_MAP payload is missing")
		}
		keyed := value.KeyedMap
		if keyed.Reduction != ir.PhysicalMapFirstSorted || !keyed.FlattenSource || len(keyed.ValueFallbacks) != 0 {
			return nil, unsupported("KEYED_MAP flags are outside the captured subset")
		}
		source, err := compileExpression(keyed.Source, binds)
		if err != nil {
			return nil, fmt.Errorf("KEYED_MAP source: %w", err)
		}
		key, err := compileExpression(keyed.ItemKey, binds)
		if err != nil {
			return nil, fmt.Errorf("KEYED_MAP key: %w", err)
		}
		itemValue, err := compileExpression(keyed.ItemValue, binds)
		if err != nil {
			return nil, fmt.Errorf("KEYED_MAP value: %w", err)
		}
		return keyedMapExpression{itemVariable: keyed.ItemVariable, source: source, key: key, value: itemValue}, nil
	case ir.PhysicalObjectLookupExpression:
		if value.ObjectLookup == nil {
			return nil, unsupported("OBJECT_LOOKUP payload is missing")
		}
		key, ok := binds[value.ObjectLookup.KeyBindKey].(string)
		if !ok {
			return nil, unsupported("OBJECT_LOOKUP bind %q must be a string", value.ObjectLookup.KeyBindKey)
		}
		if value.NullBehavior != ir.PhysicalPreserveNull && value.NullBehavior != ir.PhysicalEmptyOnNull {
			return nil, unsupported("OBJECT_LOOKUP null behavior %q is unsupported", value.NullBehavior)
		}
		return objectLookupExpression{objectVariable: value.ObjectLookup.ObjectVariable, key: key, behavior: value.NullBehavior}, nil
	case ir.PhysicalObjectKeysExpression:
		if value.ObjectKeys == nil {
			return nil, unsupported("OBJECT_KEYS payload is missing")
		}
		if value.NullBehavior != ir.PhysicalPreserveNull && value.NullBehavior != ir.PhysicalEmptyOnNull {
			return nil, unsupported("OBJECT_KEYS null behavior %q is unsupported", value.NullBehavior)
		}
		return objectKeysExpression{objectVariable: value.ObjectKeys.ObjectVariable, behavior: value.NullBehavior}, nil
	case ir.PhysicalObjectExpression:
		if value.Object == nil || len(value.Object.Fields) == 0 {
			return nil, unsupported("OBJECT requires fields")
		}
		object := objectExpression{fields: make([]compiledObjectField, 0, len(value.Object.Fields))}
		for _, field := range value.Object.Fields {
			compiled, err := compileExpression(field.Expression, binds)
			if err != nil {
				return nil, fmt.Errorf("OBJECT field %q: %w", field.Name, err)
			}
			object.fields = append(object.fields, compiledObjectField{name: field.Name, expression: compiled, behavior: field.Expression.NullBehavior})
		}
		return object, nil
	default:
		return nil, unsupported("expression kind %q is unsupported", value.Kind)
	}
}

func compileValue(value ir.PhysicalExpression, binds map[string]any) (expression, error) {
	if value.Value == nil {
		return nil, unsupported("VALUE payload is missing")
	}
	if value.Value.BindKey != "" {
		if _, ok := binds[value.Value.BindKey]; !ok {
			return nil, unsupported("VALUE bind %q is missing", value.Value.BindKey)
		}
	}
	return valueExpression{value: *value.Value}, nil
}

func (value valueExpression) evaluate(scope *evaluationScope) (any, error) {
	return resolveValue(value.value, scope)
}

func (value valueExpression) references(names map[string]struct{}) {
	if value.value.Variable != "" {
		names[value.value.Variable] = struct{}{}
	}
}

func (value extractExpression) evaluate(scope *evaluationScope) (any, error) {
	if _, isSet := scope.sets[value.extract.Source.Variable]; isSet {
		values, err := extractRows(value.extract, scope)
		if err != nil {
			return nil, err
		}
		if value.cardinality == ir.PhysicalScalarCardinality {
			if len(values) == 0 {
				return nil, nil
			}
			return nullable(values[0]), nil
		}
		return values, nil
	}
	if value.cardinality == ir.PhysicalScalarCardinality {
		source, err := resolveValue(value.extract.Source, scope)
		if err != nil {
			return nil, err
		}
		result, err := lookupSelector(source, value.extract.Selector)
		if err != nil {
			return nil, err
		}
		return nullable(result), nil
	}
	return extractRows(value.extract, scope)
}

func (value extractExpression) references(names map[string]struct{}) {
	if value.extract.Source.Variable != "" {
		names[value.extract.Source.Variable] = struct{}{}
	}
}

func (value keyedMapExpression) evaluate(scope *evaluationScope) (any, error) {
	source, err := value.source.evaluate(scope)
	if err != nil {
		return nil, err
	}
	rows, ok := source.([]any)
	if !ok {
		return nil, fmt.Errorf("KEYED_MAP source must be an array, got %T", source)
	}
	items := flattenOne(rows)
	grouped := make(map[string]map[string]struct{})
	for _, item := range items {
		previous, hadPrevious := scope.variables[value.itemVariable]
		scope.variables[value.itemVariable] = item
		keyValue, keyErr := value.key.evaluate(scope)
		itemValue, itemErr := value.value.evaluate(scope)
		if hadPrevious {
			scope.variables[value.itemVariable] = previous
		} else {
			delete(scope.variables, value.itemVariable)
		}
		if keyErr != nil {
			return nil, keyErr
		}
		if itemErr != nil {
			return nil, itemErr
		}
		keyValue = nullable(keyValue)
		itemValue = nullable(itemValue)
		if keyValue == nil || itemValue == nil {
			continue
		}
		key, keyOK := keyValue.(string)
		text, textOK := itemValue.(string)
		if !keyOK || !textOK {
			return nil, fmt.Errorf("KEYED_MAP requires string keys and values, got %T and %T", keyValue, itemValue)
		}
		if grouped[key] == nil {
			grouped[key] = make(map[string]struct{})
		}
		grouped[key][text] = struct{}{}
	}
	result := make(map[string]any, len(grouped))
	for key, values := range grouped {
		ordered := make([]string, 0, len(values))
		for item := range values {
			ordered = append(ordered, item)
		}
		sort.Strings(ordered)
		result[key] = ordered[0]
	}
	return result, nil
}

func (value keyedMapExpression) references(names map[string]struct{}) {
	value.source.references(names)
	local := make(map[string]struct{})
	value.key.references(local)
	value.value.references(local)
	delete(local, value.itemVariable)
	for name := range local {
		names[name] = struct{}{}
	}
}

func (value objectLookupExpression) evaluate(scope *evaluationScope) (any, error) {
	object, ok := scope.variables[value.objectVariable]
	if !ok || object == nil || isMissing(object) {
		return value.nullResult()
	}
	fields, ok := object.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OBJECT_LOOKUP source %q must be an object, got %T", value.objectVariable, object)
	}
	result, ok := fields[value.key]
	if !ok || result == nil || isMissing(result) {
		return value.nullResult()
	}
	return result, nil
}

func (value objectLookupExpression) nullResult() (any, error) {
	if value.behavior == ir.PhysicalEmptyOnNull {
		return []any{}, nil
	}
	return nil, nil
}

func (value objectLookupExpression) references(names map[string]struct{}) {
	names[value.objectVariable] = struct{}{}
}

func (value objectKeysExpression) evaluate(scope *evaluationScope) (any, error) {
	object, ok := scope.variables[value.objectVariable]
	if !ok || object == nil || isMissing(object) {
		if value.behavior == ir.PhysicalEmptyOnNull {
			return []any{}, nil
		}
		return nil, nil
	}
	fields, ok := object.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OBJECT_KEYS source %q must be an object, got %T", value.objectVariable, object)
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys, nil
}

func (value objectKeysExpression) references(names map[string]struct{}) {
	names[value.objectVariable] = struct{}{}
}

func (value objectExpression) evaluate(scope *evaluationScope) (any, error) {
	result := make(map[string]any, len(value.fields))
	for _, field := range value.fields {
		item, err := field.expression.evaluate(scope)
		if err != nil {
			return nil, err
		}
		item = nullable(item)
		if field.behavior == ir.PhysicalOmitNulls && item == nil {
			continue
		}
		result[field.name] = item
	}
	return result, nil
}

func (value objectExpression) references(names map[string]struct{}) {
	for _, field := range value.fields {
		field.expression.references(names)
	}
}

func resolveValue(value ir.PhysicalValue, scope *evaluationScope) (any, error) {
	if value.BindKey != "" {
		result, ok := scope.bindVars[value.BindKey]
		if !ok {
			return missingValue, fmt.Errorf("missing bind %q", value.BindKey)
		}
		return result, nil
	}
	result, ok := scope.variables[value.Variable]
	if !ok {
		return missingValue, fmt.Errorf("missing variable %q", value.Variable)
	}
	for _, part := range value.Path {
		result = lookupField(result, part)
		if isMissing(result) {
			return missingValue, nil
		}
	}
	return result, nil
}

func lookupField(value any, field string) any {
	if object, ok := value.(map[string]any); ok {
		if result, exists := object[field]; exists {
			return result
		}
	}
	return missingValue
}

func extractRows(extract ir.PhysicalExtract, scope *evaluationScope) ([]any, error) {
	if _, isSet := scope.sets[extract.Source.Variable]; isSet {
		rows, ok := scope.variables[extract.Source.Variable].([]any)
		if !ok {
			return nil, fmt.Errorf("source set %q must be an array", extract.Source.Variable)
		}
		values := make([]any, 0, len(rows))
		for index, value := range rows {
			row, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("source set %q row %d must be an object", extract.Source.Variable, index)
			}
			payload, ok := row["payload"]
			if !ok {
				return nil, fmt.Errorf("source set %q row %d is missing payload", extract.Source.Variable, index)
			}
			selected, err := selectRows(payload, extract.Selector)
			if err != nil {
				return nil, err
			}
			values = append(values, selected...)
		}
		return values, nil
	}
	source, err := resolveValue(extract.Source, scope)
	if err != nil {
		return nil, err
	}
	return selectRows(source, extract.Selector)
}

func lookupSelector(source any, selector spec.Selector) (any, error) {
	current := source
	for index, step := range selector.Steps {
		if step.Iterate {
			return missingValue, unsupported("direct scalar selector contains iteration")
		}
		field := lookupField(current, step.Field)
		if step.Index != nil {
			values, err := asArrayOrEmpty(field, step.Field)
			if err != nil {
				return missingValue, err
			}
			if *step.Index < 0 || *step.Index >= len(values) {
				current = missingValue
			} else {
				current = values[*step.Index]
			}
		} else {
			current = field
		}
		if isMissing(current) && index != len(selector.Steps)-1 {
			return missingValue, nil
		}
	}
	return current, nil
}

func selectRows(source any, selector spec.Selector) ([]any, error) {
	if len(selector.Steps) == 0 {
		return nil, unsupported("selector has no steps")
	}
	if selector.Filter != nil {
		return nil, unsupported("selector filters are not supported")
	}
	current := []any{source}
	for _, step := range selector.Steps[:len(selector.Steps)-1] {
		next := make([]any, 0)
		for _, parent := range current {
			field := lookupField(parent, step.Field)
			switch {
			case step.Iterate:
				values, err := asArrayOrEmpty(field, step.Field)
				if err != nil {
					return nil, err
				}
				next = append(next, values...)
			case step.Index != nil:
				values, err := asArrayOrEmpty(field, step.Field)
				if err != nil {
					return nil, err
				}
				if *step.Index >= 0 && *step.Index < len(values) && values[*step.Index] != nil {
					next = append(next, values[*step.Index])
				}
			default:
				if !isMissing(field) && field != nil {
					next = append(next, field)
				}
			}
		}
		current = next
	}
	last := selector.Steps[len(selector.Steps)-1]
	result := make([]any, 0)
	for _, parent := range current {
		field := lookupField(parent, last.Field)
		switch {
		case last.Iterate:
			values, err := asArrayOrEmpty(field, last.Field)
			if err != nil {
				return nil, err
			}
			result = append(result, values)
		case last.Index != nil:
			values, err := asArrayOrEmpty(field, last.Field)
			if err != nil {
				return nil, err
			}
			if *last.Index >= 0 && *last.Index < len(values) && values[*last.Index] != nil {
				result = append(result, values[*last.Index])
			}
		default:
			if !isMissing(field) && field != nil {
				result = append(result, field)
			}
		}
	}
	return result, nil
}

func asArrayOrEmpty(value any, field string) ([]any, error) {
	if isMissing(value) || value == nil {
		return nil, nil
	}
	if boolean, ok := value.(bool); ok {
		if !boolean {
			return nil, nil
		}
		return nil, fmt.Errorf("selector %s expected an array, got bool", field)
	}
	values, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("selector %s expected an array, got %T", field, value)
	}
	return values, nil
}

func flattenOne(values []any) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		if nested, ok := value.([]any); ok {
			result = append(result, nested...)
		} else {
			result = append(result, value)
		}
	}
	return result
}

func validateSelectorSubset(selector spec.Selector) error {
	if len(selector.Steps) == 0 {
		return unsupported("selector has no steps")
	}
	if selector.Filter != nil {
		return unsupported("selector filters are not supported")
	}
	for _, step := range selector.Steps {
		if strings.TrimSpace(step.Field) == "" {
			return unsupported("selector contains an empty field")
		}
		if step.Iterate && step.Index != nil {
			return unsupported("selector step %q combines iteration and indexing", step.Field)
		}
	}
	return nil
}

func nullable(value any) any {
	if isMissing(value) {
		return nil
	}
	return value
}

func isMissing(value any) bool {
	_, ok := value.(missingSentinel)
	return ok
}
