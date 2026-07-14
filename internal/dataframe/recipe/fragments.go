package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Fragment is a versioned, expression-only recipe macro. Parameters are
// substituted hygienically in selector placeholders `$name` or `${name}`.
// Fragments contain no storage or output-specific behavior.
type Fragment struct {
	Name    string     `json:"name"`
	Version string     `json:"version"`
	Params  []string   `json:"params,omitempty"`
	Expr    Expression `json:"expr"`
}

type FragmentLibrary struct {
	Fragments map[string]Fragment `json:"fragments"`
}

func (l FragmentLibrary) Validate() error {
	if len(l.Fragments) == 0 {
		return fmt.Errorf("fragment library must not be empty")
	}
	for name, fragment := range l.Fragments {
		if strings.TrimSpace(name) == "" || name != fragment.Name {
			return fmt.Errorf("fragment name %q is inconsistent", name)
		}
		if strings.TrimSpace(fragment.Version) == "" {
			return fmt.Errorf("fragment %q version is required", name)
		}
		seen := map[string]struct{}{}
		for _, param := range fragment.Params {
			if strings.TrimSpace(param) == "" {
				return fmt.Errorf("fragment %q has an empty parameter", name)
			}
			if _, ok := seen[param]; ok {
				return fmt.Errorf("fragment %q repeats parameter %q", name, param)
			}
			seen[param] = struct{}{}
		}
		if err := validateExpression(fragment.Expr, "$fragments."+name+".expr"); err != nil {
			return err
		}
	}
	return nil
}

func (l FragmentLibrary) Digest() (string, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(l)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// ExpandExpression resolves fragment calls and returns a normal expression
// tree that contains only built-in recipe operators.
func (l FragmentLibrary) ExpandExpression(input Expression) (Expression, error) {
	if err := l.Validate(); err != nil {
		return Expression{}, err
	}
	return l.expand(input, nil, 0)
}

// ExpandBundle returns a validated bundle with every fragment invocation
// replaced by built-in recipe expressions. The returned canonical document is
// therefore safe to digest and persist as the resolved translation source.
func (l FragmentLibrary) ExpandBundle(bundle Bundle) (Bundle, error) {
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	copy := bundle
	copy.Outputs = make([]Output, len(bundle.Outputs))
	for i, output := range bundle.Outputs {
		resolved, err := l.expandOutput(output)
		if err != nil {
			return Bundle{}, fmt.Errorf("output %q: %w", output.Name, err)
		}
		copy.Outputs[i] = resolved
	}
	if err := copy.Validate(); err != nil {
		return Bundle{}, err
	}
	return copy, nil
}

func (l FragmentLibrary) expandOutput(output Output) (Output, error) {
	for i, field := range output.Fields {
		expr, err := l.ExpandExpression(field.Expr)
		if err != nil {
			return Output{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
		output.Fields[i].Expr = expr
	}
	for i, traversal := range output.Traversals {
		resolved, err := l.expandTraversal(traversal)
		if err != nil {
			return Output{}, err
		}
		output.Traversals[i] = resolved
	}
	if output.Expand != nil {
		expr, err := l.ExpandExpression(output.Expand.From)
		if err != nil {
			return Output{}, err
		}
		output.Expand.From = expr
	}
	if output.Identity != nil {
		expr, err := l.ExpandExpression(output.Identity.Expr)
		if err != nil {
			return Output{}, err
		}
		output.Identity.Expr = expr
	}
	for i, dynamic := range output.DynamicColumns {
		expr, err := l.ExpandExpression(dynamic.Source)
		if err != nil {
			return Output{}, err
		}
		output.DynamicColumns[i].Source = expr
		if dynamic.Key != nil {
			value, err := l.ExpandExpression(*dynamic.Key)
			if err != nil {
				return Output{}, err
			}
			output.DynamicColumns[i].Key = &value
		}
		if dynamic.Value != nil {
			value, err := l.ExpandExpression(*dynamic.Value)
			if err != nil {
				return Output{}, err
			}
			output.DynamicColumns[i].Value = &value
		}
	}
	return output, nil
}

func (l FragmentLibrary) expandTraversal(traversal Traversal) (Traversal, error) {
	for i, field := range traversal.Fields {
		expr, err := l.ExpandExpression(field.Expr)
		if err != nil {
			return Traversal{}, err
		}
		traversal.Fields[i].Expr = expr
	}
	for i, child := range traversal.Traversals {
		resolved, err := l.expandTraversal(child)
		if err != nil {
			return Traversal{}, err
		}
		traversal.Traversals[i] = resolved
	}
	if traversal.From != nil {
		expr, err := l.ExpandExpression(*traversal.From)
		if err != nil {
			return Traversal{}, err
		}
		traversal.From = &expr
	}
	return traversal, nil
}

func (l FragmentLibrary) expand(input Expression, stack []string, depth int) (Expression, error) {
	if depth > maxExpressionDepth {
		return Expression{}, fmt.Errorf("fragment expansion depth exceeds limit")
	}
	if input.Call == "" {
		if input.Select == "" {
			return input, nil
		}
		return input, nil
	}
	args := make([]Expression, len(input.Args))
	for i, arg := range input.Args {
		expanded, err := l.expand(arg, stack, depth+1)
		if err != nil {
			return Expression{}, err
		}
		args[i] = expanded
	}
	if !strings.HasPrefix(input.Call, "fragment:") {
		input.Args = args
		return input, nil
	}
	name := strings.TrimPrefix(input.Call, "fragment:")
	fragment, ok := l.Fragments[name]
	if !ok {
		return Expression{}, fmt.Errorf("unknown fragment %q", name)
	}
	for _, parent := range stack {
		if parent == name {
			return Expression{}, fmt.Errorf("fragment cycle includes %q", name)
		}
	}
	if len(args) != len(fragment.Params) {
		return Expression{}, fmt.Errorf("fragment %q expects %d arguments, got %d", name, len(fragment.Params), len(args))
	}
	bindings := make(map[string]Expression, len(args))
	for i, param := range fragment.Params {
		bindings[param] = args[i]
	}
	body := substitute(fragment.Expr, bindings)
	return l.expand(body, append(stack, name), depth+1)
}

func substitute(input Expression, bindings map[string]Expression) Expression {
	if input.Select != "" {
		key := strings.TrimSpace(input.Select)
		for name, value := range bindings {
			placeholder := "$" + name
			if key == placeholder || key == "${"+name+"}" {
				return value
			}
			if strings.HasPrefix(key, placeholder+".") && value.Select != "" {
				value.Select += strings.TrimPrefix(key, placeholder)
				return value
			}
		}
		return input
	}
	for i := range input.Args {
		input.Args[i] = substitute(input.Args[i], bindings)
	}
	return input
}

// SortedNames makes explanation and registration output deterministic.
func (l FragmentLibrary) SortedNames() []string {
	names := make([]string, 0, len(l.Fragments))
	for name := range l.Fragments {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
