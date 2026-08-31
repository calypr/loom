// Package recipe defines the persistence-neutral recipe document used by the
// dataframe compiler. A recipe describes semantic row shaping only; it never
// carries database collection, table, AQL, or SQL details.
package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (b Bundle) Validate() error {
	if b.RecipeSchemaVersion != CurrentSchemaVersion {
		return validationError("unsupported_schema_version", "$.recipeSchemaVersion", fmt.Sprintf("must be %d", CurrentSchemaVersion))
	}
	if strings.TrimSpace(b.Name) == "" {
		return validationError("required", "$.name", "name is required")
	}
	if strings.TrimSpace(b.TranslationVersion) == "" {
		return validationError("required", "$.translationVersion", "translationVersion is required")
	}
	if len(b.Outputs) == 0 {
		return validationError("required", "$.outputs", "at least one output is required")
	}
	if b.Fragments != nil {
		if err := b.Fragments.Validate(); err != nil {
			return validationError("invalid_fragments", "$.fragments", err.Error())
		}
	}
	seen := map[string]bool{}
	for i, output := range b.Outputs {
		path := fmt.Sprintf("$.outputs[%d]", i)
		if err := validateRecipeName(output.Name, path+".name"); err != nil {
			return err
		}
		if seen[output.Name] {
			return validationError("duplicate_name", path+".name", "duplicate output name")
		}
		seen[output.Name] = true
		if strings.TrimSpace(output.RootResourceType) == "" {
			return validationError("required", path+".rootResourceType", "rootResourceType is required")
		}
		if strings.TrimSpace(output.RowGrain) == "" {
			return validationError("required", path+".rowGrain", "rowGrain is required")
		}
		if !output.TraversalColumnNaming.Valid() {
			return validationError("invalid_traversal_column_naming", path+".traversalColumnNaming", "must be PATH or ALIAS")
		}
		if !output.RootColumnNaming.Valid() {
			return validationError("invalid_root_column_naming", path+".rootColumnNaming", "must be PREFIXED or EXACT")
		}
		if output.CollisionPolicy != "" && output.CollisionPolicy != "error" && output.CollisionPolicy != "overwrite" && output.CollisionPolicy != "coalesce" {
			return validationError("invalid_collision_policy", path+".collisionPolicy", "must be error, overwrite, or coalesce")
		}
		budget := 0
		if err := validateNodeShape(output.Fields, output.Filters, output.Pivots, output.Aggregates, output.Slices, path, &budget); err != nil {
			return err
		}
		if err := validateTraversals(output.Traversals, path+".traversals", 0); err != nil {
			return err
		}
		if output.TraversalColumnNaming.Normalized() == TraversalColumnNamingAlias {
			if err := validateGloballyUniqueTraversalAliases(output.Traversals, path+".traversals", map[string]string{}); err != nil {
				return err
			}
		}
		if output.Expand != nil {
			if err := validateRecipeName(output.Expand.As, path+".expand.as"); err != nil {
				return err
			}
			if err := validateExpressionBudget(output.Expand.From, path+".expand.from", &budget); err != nil {
				return err
			}
		}
		if output.Identity != nil {
			if err := validateRecipeName(output.Identity.Name, path+".identity.name"); err != nil {
				return err
			}
			if err := validateExpressionBudget(output.Identity.Expr, path+".identity.expr", &budget); err != nil {
				return err
			}
		}
		if err := validateDynamicColumns(output.DynamicColumns, path+".dynamicColumns", &budget); err != nil {
			return err
		}
		if err := validateExtensionColumns(output.ExtensionColumns, path+".extensionColumns", &budget); err != nil {
			return err
		}
		for index, projection := range output.CatalogProjections {
			if err := projection.validateAt(fmt.Sprintf("%s.catalogProjections[%d]", path, index)); err != nil {
				return err
			}
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
		// The relationship label is not an output namespace: two routes may
		// legitimately use the same edge label while targeting different FHIR
		// resources (for example Patient -> Condition and Patient -> Specimen).
		// Alias is the lexical identity when supplied; otherwise the edge name
		// remains the backwards-compatible uniqueness key.
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
		if err := validateNodeShape(t.Fields, t.Filters, t.Pivots, t.Aggregates, t.Slices, p, &budget); err != nil {
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
	"coalesce": {1, -1}, "coalesce_string": {1, -1}, "first": {1, 1}, "all": {1, 1}, "distinct": {1, 1},
	"canonical_json": {1, 1},
	"concat":         {1, -1}, "join": {2, 2}, "cast": {2, 2}, "reference_id": {1, 1},
	"path_segment": {1, 1}, "basename": {1, 1}, "last_segment": {1, 1},
	"sanitize_name": {1, 1}, "sanitize_graphql_name": {1, 1}, "uuid3": {3, 3}, "uuid5": {3, 3},
	"if": {3, 3}, "case": {2, -1},
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

// CanonicalJSON returns stable compact JSON for a validated document.
