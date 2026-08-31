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
	var err error
	output.Fields, err = l.expandFields(output.Fields)
	if err != nil {
		return Output{}, err
	}
	output.Pivots, err = l.expandPivots(output.Pivots)
	if err != nil {
		return Output{}, err
	}
	output.Aggregates, err = l.expandAggregates(output.Aggregates)
	if err != nil {
		return Output{}, err
	}
	output.Slices, err = l.expandSlices(output.Slices)
	if err != nil {
		return Output{}, err
	}
	output.Traversals = append([]Traversal(nil), output.Traversals...)
	for i := range output.Traversals {
		resolved, err := l.expandTraversal(output.Traversals[i])
		if err != nil {
			return Output{}, err
		}
		output.Traversals[i] = resolved
	}
	if output.Expand != nil {
		expansion := *output.Expand
		expr, err := l.ExpandExpression(expansion.From)
		if err != nil {
			return Output{}, err
		}
		expansion.From = expr
		output.Expand = &expansion
	}
	if output.Identity != nil {
		identity := *output.Identity
		expr, err := l.ExpandExpression(identity.Expr)
		if err != nil {
			return Output{}, err
		}
		identity.Expr = expr
		output.Identity = &identity
	}
	output.DynamicColumns, err = l.expandDynamicColumns(output.DynamicColumns)
	if err != nil {
		return Output{}, err
	}
	output.ExtensionColumns, err = l.expandExtensionColumns(output.ExtensionColumns)
	if err != nil {
		return Output{}, err
	}
	return output, nil
}

func (l FragmentLibrary) expandTraversal(traversal Traversal) (Traversal, error) {
	var err error
	traversal.Fields, err = l.expandFields(traversal.Fields)
	if err != nil {
		return Traversal{}, err
	}
	traversal.Pivots, err = l.expandPivots(traversal.Pivots)
	if err != nil {
		return Traversal{}, err
	}
	traversal.Aggregates, err = l.expandAggregates(traversal.Aggregates)
	if err != nil {
		return Traversal{}, err
	}
	traversal.Slices, err = l.expandSlices(traversal.Slices)
	if err != nil {
		return Traversal{}, err
	}
	traversal.Traversals = append([]Traversal(nil), traversal.Traversals...)
	for i := range traversal.Traversals {
		resolved, err := l.expandTraversal(traversal.Traversals[i])
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
	traversal.DynamicColumns, err = l.expandDynamicColumns(traversal.DynamicColumns)
	if err != nil {
		return Traversal{}, err
	}
	traversal.ExtensionColumns, err = l.expandExtensionColumns(traversal.ExtensionColumns)
	if err != nil {
		return Traversal{}, err
	}
	return traversal, nil
}

func (l FragmentLibrary) expandFields(fields []Field) ([]Field, error) {
	fields = append([]Field(nil), fields...)
	for i := range fields {
		expr, err := l.ExpandExpression(fields[i].Expr)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", fields[i].Name, err)
		}
		fields[i].Expr = expr
	}
	return fields, nil
}

func (l FragmentLibrary) expandPivots(pivots []Pivot) ([]Pivot, error) {
	pivots = append([]Pivot(nil), pivots...)
	for i := range pivots {
		pivot := &pivots[i]
		pivot.ValueFallbacks = append([]Expression(nil), pivot.ValueFallbacks...)
		columnExpr, err := l.ExpandExpression(pivot.ColumnExpr)
		if err != nil {
			return nil, fmt.Errorf("pivot %q column expression: %w", pivot.Name, err)
		}
		pivot.ColumnExpr = columnExpr
		valueExpr, err := l.ExpandExpression(pivot.ValueExpr)
		if err != nil {
			return nil, fmt.Errorf("pivot %q value expression: %w", pivot.Name, err)
		}
		pivot.ValueExpr = valueExpr
		for j := range pivot.ValueFallbacks {
			fallback, err := l.ExpandExpression(pivot.ValueFallbacks[j])
			if err != nil {
				return nil, fmt.Errorf("pivot %q fallback: %w", pivot.Name, err)
			}
			pivot.ValueFallbacks[j] = fallback
		}
		if !pivot.ItemSource.zero() {
			itemSource, err := l.ExpandExpression(pivot.ItemSource)
			if err != nil {
				return nil, fmt.Errorf("pivot %q item source: %w", pivot.Name, err)
			}
			pivot.ItemSource = itemSource
		}
	}
	return pivots, nil
}

func (l FragmentLibrary) expandAggregates(aggregates []Aggregate) ([]Aggregate, error) {
	aggregates = append([]Aggregate(nil), aggregates...)
	for i := range aggregates {
		if aggregates[i].Expr == nil {
			continue
		}
		expr, err := l.ExpandExpression(*aggregates[i].Expr)
		if err != nil {
			return nil, fmt.Errorf("aggregate %q: %w", aggregates[i].Name, err)
		}
		aggregates[i].Expr = &expr
	}
	return aggregates, nil
}

func (l FragmentLibrary) expandSlices(slices []RepresentativeSlice) ([]RepresentativeSlice, error) {
	slices = append([]RepresentativeSlice(nil), slices...)
	for i := range slices {
		fields, err := l.expandFields(slices[i].Fields)
		if err != nil {
			return nil, fmt.Errorf("slice %q: %w", slices[i].Name, err)
		}
		slices[i].Fields = fields
	}
	return slices, nil
}

func (l FragmentLibrary) expandDynamicColumns(columns []DynamicColumn) ([]DynamicColumn, error) {
	columns = append([]DynamicColumn(nil), columns...)
	for i := range columns {
		column := &columns[i]
		source, err := l.ExpandExpression(column.Source)
		if err != nil {
			return nil, fmt.Errorf("dynamic column %q source: %w", column.Name, err)
		}
		column.Source = source
		if column.Key != nil {
			key, err := l.ExpandExpression(*column.Key)
			if err != nil {
				return nil, fmt.Errorf("dynamic column %q key: %w", column.Name, err)
			}
			column.Key = &key
		}
		if column.Value != nil {
			value, err := l.ExpandExpression(*column.Value)
			if err != nil {
				return nil, fmt.Errorf("dynamic column %q value: %w", column.Name, err)
			}
			column.Value = &value
		}
	}
	return columns, nil
}

func (l FragmentLibrary) expandExtensionColumns(columns []ExtensionColumn) ([]ExtensionColumn, error) {
	columns = append([]ExtensionColumn(nil), columns...)
	for i := range columns {
		source, err := l.ExpandExpression(columns[i].Source)
		if err != nil {
			return nil, fmt.Errorf("extension column %q source: %w", columns[i].Name, err)
		}
		columns[i].Source = source
	}
	return columns, nil
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
	input.Args = append([]Expression(nil), input.Args...)
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
