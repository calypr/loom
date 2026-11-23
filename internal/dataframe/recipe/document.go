package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type recipeValidationMode bool

const (
	currentRecipeValidation recipeValidationMode = false
	archiveRecipeValidation recipeValidationMode = true
)

func validateGroupRowValuePolicies(output Output, path string) error {
	if output.GroupRows == nil || len(output.GroupRows.RowValues) == 0 {
		return nil
	}
	fieldsByID := make(map[string][]Field, len(output.Fields))
	for _, field := range output.Fields {
		if field.ColumnID != "" {
			fieldsByID[field.ColumnID] = append(fieldsByID[field.ColumnID], field)
		}
	}
	seen := make(map[string]bool, len(output.GroupRows.RowValues))
	for index, value := range output.GroupRows.RowValues {
		valuePath := fmt.Sprintf("%s[%d]", path, index)
		if strings.TrimSpace(value.ColumnID) == "" || value.ColumnID != strings.TrimSpace(value.ColumnID) || seen[value.ColumnID] {
			return validationError("invalid_group_row_value", valuePath+".columnId", "columnId must be an exact unique source identity")
		}
		seen[value.ColumnID] = true
		field, found := fieldsByID[value.ColumnID]
		if len(field) != 1 || !found {
			return validationError("invalid_group_row_value", valuePath+".columnId", "columnId must identify exactly one selected root field")
		}
		if strings.TrimSpace(field[0].Expr.Select) == "" || len(field[0].Fallbacks) != 0 {
			return validationError("invalid_group_row_value", valuePath, "only direct root FHIR field projections are supported")
		}
		switch value.Policy {
		case ConstructionRowValueAll, ConstructionRowValueOne:
		default:
			return validationError("invalid_group_row_value", valuePath+".policy", "policy must be ALL or ONE")
		}
	}
	return nil
}

func validateGloballyUniqueTraversalAliases(items []Traversal, path string, seen map[string]string) error {
	for index, traversal := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		alias := strings.TrimSpace(traversal.Alias)
		if alias == "" {
			alias = strings.TrimSpace(traversal.Name)
		}
		if firstPath, exists := seen[alias]; exists {
			return validationError("duplicate_global_traversal_alias", itemPath+".alias", fmt.Sprintf("alias %q is already used at %s", alias, firstPath))
		}
		seen[alias] = itemPath + ".alias"
		if err := validateGloballyUniqueTraversalAliases(traversal.Traversals, itemPath+".traversals", seen); err != nil {
			return err
		}
	}
	return nil
}

func validateTraversals(items []Traversal, path string, depth int) error {
	if depth > maxExpressionDepth {
		return validationError("max_depth", path, "traversal depth exceeds limit")
	}
	seen := map[string]bool{}
	for i, t := range items {
		p := fmt.Sprintf("%s[%d]", path, i)
		if err := validateRecipeName(t.Name, p+".name"); err != nil {
			return err
		}
		if err := validateOccurrenceID(t.OccurrenceID, p+".occurrenceId"); err != nil {
			return err
		}

		traversalKey := t.Alias
		if traversalKey == "" {
			traversalKey = t.Name
		}
		if seen[traversalKey] {
			return validationError("duplicate_name", p+".alias", "duplicate traversal alias")
		}
		seen[traversalKey] = true
		if strings.TrimSpace(t.ToResourceType) == "" {
			return validationError("required", p+".toResourceType", "toResourceType is required")
		}
		if !t.MatchMode.Valid() {
			return validationError("invalid_match_mode", p+".matchMode", "must be OPTIONAL or REQUIRED")
		}
		budget := 0
		if t.From != nil {
			if err := validateExpressionBudget(*t.From, p+".from", &budget); err != nil {
				return err
			}
		}
		if err := validateNodeShape(t.Fields, t.Filters, t.Pivots, t.OwnerRecords, t.Aggregates, t.Slices, p, &budget); err != nil {
			return err
		}
		if err := validateDynamicColumns(t.DynamicColumns, p+".dynamicColumns", &budget); err != nil {
			return err
		}
		if err := validateExtensionColumns(t.ExtensionColumns, p+".extensionColumns", &budget); err != nil {
			return err
		}
		for index, projection := range t.CatalogProjections {
			if err := projection.validateAt(fmt.Sprintf("%s.catalogProjections[%d]", p, index)); err != nil {
				return err
			}
		}
		if err := validateTraversals(t.Traversals, p+".traversals", depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateOccurrenceID(value, path string) error {
	if value != "" && strings.TrimSpace(value) != value {
		return validationError("invalid_occurrence_id", path, "must equal its trimmed value")
	}
	return nil
}

func expressionPresent(expression Expression) bool {
	return expression.Select != "" || expression.Call != "" || expression.Literal != nil || expression.Document != nil || len(expression.Args) != 0
}

func validateExpression(e Expression, path string) error {
	nodes := 0
	var walk func(Expression, string, int) error
	walk = func(node Expression, p string, depth int) error {
		nodes++
		if nodes > maxExpressionNodes {
			return validationError("max_nodes", p, "expression node count exceeds limit")
		}
		if depth > maxExpressionDepth {
			return validationError("max_depth", p, "expression depth exceeds limit")
		}
		operators := 0
		if node.Select != "" {
			operators++
		}
		if node.Call != "" {
			operators++
		}
		if node.Literal != nil {
			operators++
		}
		if node.Document != nil {
			operators++
		}
		if operators != 1 {
			return validationError("invalid_expression", p, "expression must contain exactly one operator")
		}
		if node.Literal != nil {
			if err := validateLiteral(node.Literal, p+".literal"); err != nil {
				return err
			}
			return nil
		}
		if node.Select != "" {
			if strings.TrimSpace(node.Select) == "" {
				return validationError("required", p+".select", "select is required")
			}
			return nil
		}
		if node.Document != nil {
			if len(node.Args) != 0 {
				return validationError("invalid_expression", p+".args", "document expressions do not accept args")
			}
			if node.Document.Context != "" && !recipeNamePattern.MatchString(node.Document.Context) {
				return validationError("invalid_context", p+".document.context", "context must be a logical name")
			}
			return nil
		}
		arity, ok := callArities[node.Call]
		if strings.HasPrefix(node.Call, "fragment:") {
			if strings.TrimSpace(strings.TrimPrefix(node.Call, "fragment:")) == "" {
				return validationError("required", p+".call", "fragment name is required")
			}
			for i, arg := range node.Args {
				if err := walk(arg, fmt.Sprintf("%s.args[%d]", p, i), depth+1); err != nil {
					return err
				}
			}
			return nil
		}
		if !ok {
			return validationError("unsupported_operation", p+".call", "unsupported call "+strconv.Quote(node.Call))
		}
		if len(node.Args) < arity.min || (arity.max >= 0 && len(node.Args) > arity.max) {
			return validationError("invalid_arity", p+".args", fmt.Sprintf("call %q expects %d..%s arguments", node.Call, arity.min, maxString(arity.max)))
		}
		for i, arg := range node.Args {
			if err := walk(arg, fmt.Sprintf("%s.args[%d]", p, i), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(e, path, 0)
}

type arity struct{ min, max int }

var callArities = map[string]arity{
	"coalesce": {1, -1}, "fallback": {1, -1}, "coalesce_string": {1, -1}, "first": {1, 1}, "all": {1, 1}, "distinct": {1, 1}, "length": {1, 1},
	"canonical_json": {1, 1},
	"concat":         {1, -1}, "join": {2, 2}, "cast": {2, 2}, "reference_id": {1, 1},
	"path_segment": {1, 1}, "basename": {1, 1}, "last_segment": {1, 1},
	"sanitize_name": {1, 1}, "sanitize_graphql_name": {1, 1}, "uuid3": {3, 3}, "uuid5": {3, 3},
	"if": {3, 3}, "case": {2, -1},
	"not": {1, 1}, "and": {2, -1}, "or": {2, -1},
	"eq": {2, 2}, "neq": {2, 2}, "gt": {2, 2}, "gte": {2, 2}, "lt": {2, 2}, "lte": {2, 2}, "contains": {2, 2},
}

func maxString(max int) string {
	if max < 0 {
		return "many"
	}
	return strconv.Itoa(max)
}

func validateLiteral(raw json.RawMessage, path string) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return validationError("invalid_literal", path, err.Error())
	}
	switch v := value.(type) {
	case nil, string, bool, json.Number:
		return nil
	case []any:
		if len(v) > maxLiteralArray {
			return validationError("literal_limit", path, "literal array is too large")
		}
		for i, item := range v {
			switch item.(type) {
			case nil, string, bool, json.Number:
			default:
				return validationError("invalid_literal", fmt.Sprintf("%s[%d]", path, i), "literal arrays may contain only scalar values")
			}
		}
		return nil
	default:
		return validationError("invalid_literal", path, "literal must be a scalar or bounded scalar array")
	}
}
