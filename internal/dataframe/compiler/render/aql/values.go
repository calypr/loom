package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderExpression(expression ir.PhysicalExpression) (string, error) {
	switch expression.Kind {
	case ir.PhysicalValueExpression:
		return r.renderValue(*expression.Value)
	case ir.PhysicalLiteralExpression:
		if expression.Literal == nil {
			return "", fmt.Errorf("LITERAL expression is missing payload")
		}
		return r.renderLiteral(*expression.Literal)
	case ir.PhysicalExtractExpression:
		return r.renderExtract(expression)
	case ir.PhysicalAggregateExpression:
		return r.renderAggregate(expression)
	case ir.PhysicalPivotExpression:
		return r.renderPivot(expression)
	case ir.PhysicalSliceExpression:
		return r.renderSlice(expression)
	case ir.PhysicalLookupExpression:
		return r.renderLookup(expression)
	case ir.PhysicalObjectLookupExpression:
		if expression.ObjectLookup == nil {
			return "", fmt.Errorf("OBJECT_LOOKUP expression is missing payload")
		}
		if _, ok := r.bindVars[expression.ObjectLookup.KeyBindKey]; !ok {
			return "", fmt.Errorf("object lookup bind %q is not defined", expression.ObjectLookup.KeyBindKey)
		}
		return fmt.Sprintf("%s[@%s]", expression.ObjectLookup.ObjectVariable, expression.ObjectLookup.KeyBindKey), nil
	case ir.PhysicalKeyedMapExpression:
		return r.renderKeyedMap(expression)
	case ir.PhysicalObjectKeysExpression:
		if expression.ObjectKeys == nil {
			return "", fmt.Errorf("OBJECT_KEYS expression is missing payload")
		}
		return fmt.Sprintf("SORTED_UNIQUE(ATTRIBUTES(%s, true))", expression.ObjectKeys.ObjectVariable), nil
	case ir.PhysicalKeySetExpression:
		return r.renderKeySet(expression)
	case ir.PhysicalObjectExpression:
		return r.renderObject(expression)
	case ir.PhysicalCallExpression:
		return r.renderCall(expression)
	default:
		return "", fmt.Errorf("physical renderer does not yet support expression kind %q", expression.Kind)
	}
}

func (r *physicalPlanRenderer) renderKeyedMap(expression ir.PhysicalExpression) (string, error) {
	keyed := expression.KeyedMap
	if keyed == nil {
		return "", fmt.Errorf("KEYED_MAP expression is missing payload")
	}
	source, err := r.renderExpression(keyed.Source)
	if err != nil {
		return "", fmt.Errorf("keyed map source: %w", err)
	}
	item := keyed.ItemVariable
	previous := r.preparedItem
	r.preparedItem = item
	key, err := r.renderExpression(keyed.ItemKey)
	if err != nil {
		r.preparedItem = previous
		return "", fmt.Errorf("keyed map key: %w", err)
	}
	valueExpressions := make([]string, 0, 1+len(keyed.ValueFallbacks))
	value, err := r.renderExpression(keyed.ItemValue)
	if err != nil {
		r.preparedItem = previous
		return "", fmt.Errorf("keyed map value: %w", err)
	}
	valueExpressions = append(valueExpressions, value)
	for _, fallback := range keyed.ValueFallbacks {
		fallbackValue, fallbackErr := r.renderExpression(fallback)
		if fallbackErr != nil {
			r.preparedItem = previous
			return "", fmt.Errorf("keyed map fallback: %w", fallbackErr)
		}
		valueExpressions = append(valueExpressions, fallbackValue)
	}
	r.preparedItem = previous
	valueExpression := valueExpressions[0]
	if len(valueExpressions) > 1 {
		valueExpression = "FIRST(FOR __loom_keyed_candidate IN [" + strings.Join(valueExpressions, ", ") + "] FILTER __loom_keyed_candidate != null RETURN __loom_keyed_candidate)"
	}
	// Selector extraction returns a subquery whose rows can themselves be
	// arrays. Flatten that result directly so a keyed family iterates source
	// items. Wrapping the subquery in another array leaves one array layer
	// behind and makes item key/value selectors read from the array itself.
	sourceLoop := source
	if keyed.FlattenSource {
		sourceLoop = "FLATTEN(" + source + ")"
	}
	values := "FIRST(__loom_keyed_group[*].__loom_keyed_value)"
	if keyed.Reduction == ir.PhysicalMapFirstSorted {
		values = "FIRST(SORTED_UNIQUE(__loom_keyed_group[*].__loom_keyed_value))"
	}
	return fmt.Sprintf(`MERGE(
	  FOR %s IN %s
    LET __loom_keyed_key = %s
    LET __loom_keyed_value = %s
    FILTER __loom_keyed_key != null
    FILTER __loom_keyed_value != null
    COLLECT __loom_keyed_group_key = __loom_keyed_key INTO __loom_keyed_group
    LET __loom_keyed_values = %s
    RETURN { [__loom_keyed_group_key]: __loom_keyed_values }
)`, item, sourceLoop, key, valueExpression, values), nil
}

// renderLookup emits the sole canonical AQL lowering for a bounded dynamic
// key/value projection. AQL's FOR-over-null behavior supplies the empty
// source semantics; FIRST preserves the historical scalar-column contract
// when a key is absent or appears more than once.
func (r *physicalPlanRenderer) renderLookup(expression ir.PhysicalExpression) (string, error) {
	lookup := expression.Lookup
	if lookup == nil {
		return "", fmt.Errorf("LOOKUP expression is missing payload")
	}
	source, err := r.renderExpression(lookup.Source)
	if err != nil {
		return "", fmt.Errorf("lookup source: %w", err)
	}
	key, err := r.renderExpression(lookup.ItemKey)
	if err != nil {
		return "", fmt.Errorf("lookup item key: %w", err)
	}
	value, err := r.renderExpression(lookup.ItemValue)
	if err != nil {
		return "", fmt.Errorf("lookup item value: %w", err)
	}
	if lookup.MatchBindKey == "" {
		return "", fmt.Errorf("lookup match bind key is required")
	}
	if _, ok := r.bindVars[lookup.MatchBindKey]; !ok {
		return "", fmt.Errorf("lookup match bind key %q is not defined", lookup.MatchBindKey)
	}
	// AQL FOR over a null array produces no rows, which is the canonical
	// missing-key/null projection behavior. Keep the source expression single
	// evaluated per lookup instead of duplicating it in a null ternary.
	return fmt.Sprintf("FIRST(FOR %s IN %s FILTER %s == @%s RETURN %s)", lookup.ItemVariable, source, key, lookup.MatchBindKey, value), nil
}

func (r *physicalPlanRenderer) renderKeySet(expression ir.PhysicalExpression) (string, error) {
	keySet := expression.KeySet
	if keySet == nil {
		return "", fmt.Errorf("KEY_SET expression is missing payload")
	}
	source, err := r.renderExpression(keySet.Source)
	if err != nil {
		return "", fmt.Errorf("key set source: %w", err)
	}
	previousPreparedItem := r.preparedItem
	r.preparedItem = keySet.ItemVariable
	key, err := r.renderExpression(keySet.ItemKey)
	r.preparedItem = previousPreparedItem
	if err != nil {
		return "", fmt.Errorf("key set item key: %w", err)
	}
	return fmt.Sprintf("SORTED_UNIQUE(FLATTEN(FOR %s IN %s RETURN %s))", keySet.ItemVariable, source, key), nil
}

func (r *physicalPlanRenderer) renderLiteral(literal ir.PhysicalLiteral) (string, error) {
	if literal.BindKey == "" {
		return "", fmt.Errorf("literal bind key is required")
	}
	if _, collection := r.collectionKeys[literal.BindKey]; collection {
		return "", fmt.Errorf("literal bind key %q cannot be a collection bind", literal.BindKey)
	}
	if _, ok := r.bindVars[literal.BindKey]; !ok {
		return "", fmt.Errorf("literal bind key %q is not defined", literal.BindKey)
	}
	return "@" + literal.BindKey, nil
}
