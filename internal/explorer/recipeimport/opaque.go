package recipeimport

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

var editableSourceExpressionFunctions = map[string]struct{}{
	"first": {}, "all": {}, "reference_id": {}, "last_segment": {},
	"coalesce": {}, "coalesce_string": {}, "join": {}, "concat": {}, "uuid5": {},
}

func validateSourceParameterExpressionEdits(original, edited *ws.ImportedSourceParameters) error {
	if original == nil || edited == nil {
		return nil
	}
	check := func(before, after *recipe.Expression, path string) error {
		return validateSourceExpressionEdit(before, after, path)
	}
	checkList := func(before, after []recipe.Expression, path string) error {
		for index := 0; index < max(len(before), len(after)); index++ {
			var oldExpression, newExpression *recipe.Expression
			if index < len(before) {
				oldExpression = &before[index]
			}
			if index < len(after) {
				newExpression = &after[index]
			}
			if err := check(oldExpression, newExpression, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		return nil
	}
	checkPair := func(before, after *ws.ImportedSourceParameters) error {
		switch {
		case before.FieldGroup != nil && after.FieldGroup != nil:
			for index := 0; index < min(len(before.FieldGroup.Fields), len(after.FieldGroup.Fields)); index++ {
				oldField, newField := before.FieldGroup.Fields[index], after.FieldGroup.Fields[index]
				path := fmt.Sprintf("fieldGroup.fields[%d]", index)
				if err := check(&oldField.Expr, &newField.Expr, path+".expr"); err != nil {
					return err
				}
				if err := checkList(oldField.Fallbacks, newField.Fallbacks, path+".fallbacks"); err != nil {
					return err
				}
			}
		case before.Dynamic != nil && after.Dynamic != nil:
			if err := check(&before.Dynamic.Source, &after.Dynamic.Source, "dynamic.source"); err != nil {
				return err
			}
			if err := check(before.Dynamic.Key, after.Dynamic.Key, "dynamic.key"); err != nil {
				return err
			}
			return check(before.Dynamic.Value, after.Dynamic.Value, "dynamic.value")
		case before.Extension != nil && after.Extension != nil:
			return check(&before.Extension.Source, &after.Extension.Source, "extension.source")
		case before.Pivot != nil && after.Pivot != nil:
			if err := check(before.Pivot.ColumnExpr, after.Pivot.ColumnExpr, "pivot.columnExpr"); err != nil {
				return err
			}
			if err := check(before.Pivot.ValueExpr, after.Pivot.ValueExpr, "pivot.valueExpr"); err != nil {
				return err
			}
			if err := checkList(before.Pivot.ValueFallbacks, after.Pivot.ValueFallbacks, "pivot.valueFallbacks"); err != nil {
				return err
			}
			return check(before.Pivot.ItemSource, after.Pivot.ItemSource, "pivot.itemSource")
		case before.Expansion != nil && after.Expansion != nil:
			return check(&before.Expansion.From, &after.Expansion.From, "expansion.from")
		case before.Identity != nil && after.Identity != nil:
			return check(&before.Identity.Expr, &after.Identity.Expr, "identity.expr")
		case before.Traversal != nil && after.Traversal != nil:
			return check(before.Traversal.From, after.Traversal.From, "traversal.from")
		}
		return nil
	}
	return checkPair(original, edited)
}

func validateSourceExpressionEdit(original, edited *recipe.Expression, path string) error {
	if original != nil && isOpaqueSourceExpression(*original) {
		if edited == nil || !sameSourceExpression(*original, *edited) {
			return fmt.Errorf("%s contains a saved expression that cannot be changed by the structured editor", path)
		}
		return nil
	}
	if edited == nil {
		if original != nil && containsOpaqueSourceExpression(*original) {
			return fmt.Errorf("%s cannot remove a saved expression that the structured editor cannot edit", path)
		}
		return nil
	}
	if isOpaqueSourceExpression(*edited) {
		return fmt.Errorf("%s cannot introduce an expression that the structured editor cannot edit", path)
	}
	if edited.Call != "" {
		if _, ok := editableSourceExpressionFunctions[edited.Call]; !ok {
			return fmt.Errorf("%s uses unsupported source expression function %q", path, edited.Call)
		}
	}
	if edited.Literal != nil && !primitiveSourceLiteral(edited.Literal) {
		return fmt.Errorf("%s must use a primitive source literal", path)
	}
	var originalArgs []recipe.Expression
	if original != nil {
		originalArgs = original.Args
	}
	for index := 0; index < max(len(originalArgs), len(edited.Args)); index++ {
		var oldExpression, newExpression *recipe.Expression
		if index < len(originalArgs) {
			oldExpression = &originalArgs[index]
		}
		if index < len(edited.Args) {
			newExpression = &edited.Args[index]
		}
		if err := validateSourceExpressionEdit(oldExpression, newExpression, fmt.Sprintf("%s.args[%d]", path, index)); err != nil {
			return err
		}
	}
	return nil
}

func containsOpaqueSourceExpression(expression recipe.Expression) bool {
	if isOpaqueSourceExpression(expression) {
		return true
	}
	for _, argument := range expression.Args {
		if containsOpaqueSourceExpression(argument) {
			return true
		}
	}
	return false
}

func isOpaqueSourceExpression(expression recipe.Expression) bool {
	if expression.Document != nil {
		return true
	}
	if expression.Call != "" {
		if _, ok := editableSourceExpressionFunctions[expression.Call]; !ok {
			return true
		}
	}
	return expression.Literal != nil && !primitiveSourceLiteral(expression.Literal)
}

func primitiveSourceLiteral(raw json.RawMessage) bool {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch value.(type) {
	case nil, bool, string, float64:
		return true
	default:
		return false
	}
}

func sameSourceExpression(left, right recipe.Expression) bool {
	if left.Select != right.Select || left.Call != right.Call || !reflect.DeepEqual(left.Document, right.Document) || len(left.Args) != len(right.Args) {
		return false
	}
	if (left.Literal == nil) != (right.Literal == nil) {
		return false
	}
	if left.Literal != nil {
		var leftValue, rightValue any
		if decodeSourceLiteral(left.Literal, &leftValue) != nil || decodeSourceLiteral(right.Literal, &rightValue) != nil || !reflect.DeepEqual(leftValue, rightValue) {
			return false
		}
	}
	for index := range left.Args {
		if !sameSourceExpression(left.Args[index], right.Args[index]) {
			return false
		}
	}
	return true
}

func decodeSourceLiteral(raw json.RawMessage, value *any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(value)
}
