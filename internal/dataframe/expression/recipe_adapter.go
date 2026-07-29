package expression

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

// FromRecipe converts the persisted wire expression into the typed AST. Type
// checking remains a separate operation because selector types come from the
// active generated schema/catalog.
func FromRecipe(input recipe.Expression) (Expression, error) {
	if input.Document != nil {
		context := strings.TrimSpace(input.Document.Context)
		if context == "" {
			context = "root"
		}
		return Document(context), nil
	}
	if input.Select != "" {
		parts := strings.SplitN(input.Select, ".", 2)
		ref := SelectorRef{Path: input.Select}
		if len(parts) == 2 && isContextName(parts[0]) {
			ref.Context, ref.Path = parts[0], parts[1]
		}
		return Select(ref), nil
	}
	if input.Literal != nil {
		literal, err := recipeLiteral(input.Literal)
		if err != nil {
			return Expression{}, err
		}
		return Constant(literal.Type, literal.Value), nil
	}
	if input.Call == "" {
		return Expression{}, fmt.Errorf("recipe expression is empty")
	}
	call := Call{Name: input.Call}
	for _, arg := range input.Args {
		converted, err := FromRecipe(arg)
		if err != nil {
			return Expression{}, err
		}
		call.Args = append(call.Args, converted)
	}
	if strings.EqualFold(input.Call, "cast") && len(input.Args) == 2 && input.Args[1].Literal != nil {
		var targetName string
		if err := json.Unmarshal(input.Args[1].Literal, &targetName); err == nil {
			target := typeForName(targetName)
			if target.Kind != "" {
				call.Target = &target
			}
		}
	}
	return Expression{Kind: CallNode, Call: &call, NullBehavior: NullPropagate}, nil
}

// FromRecipeInContexts is the scope-aware variant used by semantic recipe
// lowering. The wire format keeps selectors as strings; this helper turns a
// leading lexical alias into SelectorRef.Context while rejecting aliases that
// are not visible at the current recipe node. An unqualified selector is
// resolved by the caller's root scope.
func FromRecipeInContexts(input recipe.Expression, contexts map[string]struct{}) (Expression, error) {
	expr, err := FromRecipe(input)
	if err != nil {
		return Expression{}, err
	}
	var rewrite func(*Expression) error
	rewrite = func(node *Expression) error {
		if node == nil {
			return nil
		}
		if node.Selector != nil {
			ref := node.Selector
			if ref.Context == "" {
				parts := strings.SplitN(ref.Path, ".", 2)
				if len(parts) == 2 {
					if _, ok := contexts[parts[0]]; ok {
						ref.Context, ref.Path = parts[0], parts[1]
					}
				}
			} else if len(contexts) > 0 {
				if _, ok := contexts[ref.Context]; !ok {
					return fmt.Errorf("selector context %q is not in scope", ref.Context)
				}
			}
			return ref.Validate()
		}
		if node.Call != nil {
			for i := range node.Call.Args {
				if err := rewrite(&node.Call.Args[i]); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := rewrite(&expr); err != nil {
		return Expression{}, err
	}
	return expr, nil
}

func isContextName(value string) bool { return value == "root" || value == "item" }

func recipeLiteral(raw json.RawMessage) (Literal, error) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return Literal{}, err
	}
	switch v := value.(type) {
	case nil:
		return Literal{Type: Type{Kind: KindNull, Cardinality: OptionalOne}}, nil
	case bool:
		return Literal{Type: scalar(KindBoolean), Value: v}, nil
	case string:
		return Literal{Type: scalar(KindString), Value: v}, nil
	case json.Number:
		parsed, err := strconv.ParseFloat(v.String(), 64)
		if err != nil {
			return Literal{}, err
		}
		return Literal{Type: scalar(KindDecimal), Value: parsed}, nil
	case []any:
		kind := KindString
		if len(v) > 0 {
			switch v[0].(type) {
			case bool:
				kind = KindBoolean
			case json.Number:
				kind = KindDecimal
			}
		}
		if kind == KindDecimal {
			converted := make([]any, len(v))
			for i, item := range v {
				number, err := strconv.ParseFloat(item.(json.Number).String(), 64)
				if err != nil {
					return Literal{}, err
				}
				converted[i] = number
			}
			v = converted
		}
		return Literal{Type: Type{Kind: kind, Cardinality: Many}, Value: v}, nil
	default:
		return Literal{}, fmt.Errorf("unsupported recipe literal %T", value)
	}
}

func typeForName(name string) Type {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "string":
		return scalar(KindString)
	case "integer", "int":
		return scalar(KindInteger)
	case "decimal", "float":
		return scalar(KindDecimal)
	case "boolean", "bool":
		return scalar(KindBoolean)
	case "date":
		return scalar(KindDate)
	case "date_time", "datetime":
		return scalar(KindDateTime)
	case "code":
		return scalar(KindCode)
	case "uuid":
		return scalar(KindUUID)
	default:
		return Type{}
	}
}
