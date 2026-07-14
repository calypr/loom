// Package recipeeval evaluates the storage-neutral recipe language over a
// caller-provided row and relationship resolver. It is deliberately generic:
// the evaluator knows only logical contexts and expressions, never FHIR types,
// graph collections, or ClickHouse tables.
package recipeeval

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/uuidcompat"
)

// Related resolves one logical traversal. Implementations may use Loom's
// generated relationship catalog, a fixture graph, or another backend.
type Related func(parent map[string]any, traversal recipe.Traversal) ([]map[string]any, error)

// EvaluateOutput evaluates one output for a root resource. One root can yield
// multiple rows when the recipe declares an expansion.
func EvaluateOutput(output recipe.Output, root map[string]any, related Related) ([]map[string]any, error) {
	if related == nil {
		related = func(map[string]any, recipe.Traversal) ([]map[string]any, error) { return nil, nil }
	}
	base := context{"root": root}
	rows := []context{base}
	if output.Expand != nil {
		value, err := eval(output.Expand.From, base)
		if err != nil {
			return nil, err
		}
		items := many(value)
		rows = make([]context, 0, len(items))
		for _, item := range items {
			row := cloneContext(base)
			row[output.Expand.As] = item
			rows = append(rows, row)
		}
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		values, err := project(output, row, related)
		if err != nil {
			return nil, err
		}
		out = append(out, values)
	}
	return out, nil
}

// DiscoverColumns evaluates dynamic column sources without emitting rows.
// Callers use the result to freeze a schema before materialization.
func DiscoverColumns(output recipe.Output, roots []map[string]any, related Related) ([]string, error) {
	seen := map[string]struct{}{}
	for _, root := range roots {
		rows, err := EvaluateOutput(output, root, related)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			for _, dynamic := range output.DynamicColumns {
				prefix := dynamic.Name
				for name := range row {
					if strings.HasPrefix(name, prefix+"_") {
						seen[name] = struct{}{}
					}
				}
			}
		}
	}
	columns := make([]string, 0, len(seen))
	for name := range seen {
		columns = append(columns, name)
	}
	sort.Strings(columns)
	return columns, nil
}

type context map[string]any

func project(output recipe.Output, row context, related Related) (map[string]any, error) {
	result := map[string]any{}
	policy := output.CollisionPolicy
	if policy == "" {
		policy = "error"
	}
	put := func(name string, value any) error {
		if _, exists := result[name]; !exists {
			result[name] = value
			return nil
		}
		switch policy {
		case "overwrite":
			result[name] = value
		case "coalesce":
			if isNull(result[name]) {
				result[name] = value
			}
		default:
			return fmt.Errorf("output column %q emitted more than once", name)
		}
		return nil
	}
	if output.Identity != nil {
		value, err := eval(output.Identity.Expr, row)
		if err != nil {
			return nil, err
		}
		if err := put(output.Identity.Name, value); err != nil {
			return nil, err
		}
	}
	for _, field := range output.Fields {
		value, err := eval(field.Expr, row)
		if err != nil {
			return nil, fmt.Errorf("field %q: %w", field.Name, err)
		}
		if err := put(field.Name, value); err != nil {
			return nil, err
		}
	}
	if err := projectTraversals(output.Traversals, row, row["root"].(map[string]any), result, put, related); err != nil {
		return nil, err
	}
	for _, dynamic := range output.DynamicColumns {
		value, err := eval(dynamic.Source, row)
		if err != nil {
			return nil, fmt.Errorf("dynamic column %q: %w", dynamic.Name, err)
		}
		items := many(value)
		allowed := map[string]struct{}{}
		for _, name := range dynamic.Columns {
			allowed[name] = struct{}{}
		}
		for _, item := range items {
			itemCtx := cloneContext(row)
			itemCtx["item"] = item
			key := item
			if dynamic.Key != nil {
				key, err = eval(*dynamic.Key, itemCtx)
				if err != nil {
					return nil, err
				}
			}
			keyText := sanitize(fmt.Sprint(key))
			if keyText == "" {
				continue
			}
			if len(allowed) > 0 {
				if _, ok := allowed[fmt.Sprint(key)]; !ok {
					continue
				}
			}
			if dynamic.MaxColumns > 0 && len(allowed) == 0 && countPrefix(result, dynamic.Name+"_") >= dynamic.MaxColumns {
				return nil, fmt.Errorf("dynamic column %q exceeds maxColumns", dynamic.Name)
			}
			column := dynamic.Name + "_" + keyText
			val := item
			if dynamic.Value != nil {
				val, err = eval(*dynamic.Value, itemCtx)
				if err != nil {
					return nil, err
				}
			}
			if err := put(column, val); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func projectTraversals(items []recipe.Traversal, parent context, current map[string]any, result map[string]any, put func(string, any) error, related Related) error {
	for _, traversal := range items {
		parentDoc := current
		if traversal.From != nil {
			value, err := eval(*traversal.From, parent)
			if err != nil {
				return err
			}
			if doc, ok := value.(map[string]any); ok {
				parentDoc = doc
			}
		}
		children, err := related(parentDoc, traversal)
		if err != nil {
			return fmt.Errorf("traversal %q: %w", traversal.Name, err)
		}
		if len(children) == 0 && traversal.MatchMode == "required" {
			return fmt.Errorf("required traversal %q has no related resource", traversal.Name)
		}
		for _, child := range children {
			ctx := cloneContext(parent)
			alias := traversal.Alias
			if alias == "" {
				alias = traversal.Name
			}
			ctx[alias] = child
			for _, field := range traversal.Fields {
				value, err := eval(field.Expr, ctx)
				if err != nil {
					return err
				}
				if err := put(field.Name, value); err != nil {
					return err
				}
			}
			if err := projectTraversals(traversal.Traversals, ctx, child, result, put, related); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneContext(in context) context {
	out := context{}
	for k, v := range in {
		out[k] = v
	}
	return out
}

func eval(e recipe.Expression, ctx context) (any, error) {
	if e.Select != "" {
		return selectPath(e.Select, ctx), nil
	}
	if e.Literal != nil {
		var value any
		dec := json.NewDecoder(strings.NewReader(string(e.Literal)))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	args := make([]any, len(e.Args))
	for i, arg := range e.Args {
		value, err := eval(arg, ctx)
		if err != nil {
			return nil, err
		}
		args[i] = value
	}
	switch strings.ToLower(e.Call) {
	case "coalesce", "fallback":
		for _, value := range args {
			if !isNull(value) {
				return value, nil
			}
		}
		return nil, nil
	case "first":
		values := many(args[0])
		if len(values) == 0 {
			return nil, nil
		}
		return values[0], nil
	case "all":
		return many(args[0]), nil
	case "distinct":
		return distinct(many(args[0])), nil
	case "concat":
		var b strings.Builder
		for _, value := range args {
			if !isNull(value) {
				b.WriteString(fmt.Sprint(value))
			}
		}
		return b.String(), nil
	case "join":
		values := many(args[0])
		parts := make([]string, len(values))
		for i, value := range values {
			parts[i] = fmt.Sprint(value)
		}
		return strings.Join(parts, fmt.Sprint(args[1])), nil
	case "cast":
		return cast(args[0], fmt.Sprint(args[1]))
	case "reference_id", "path_segment":
		return pathSegment(fmt.Sprint(args[0])), nil
	case "sanitize_name":
		return sanitize(fmt.Sprint(args[0])), nil
	case "uuid3", "uuid5":
		// uuid3/uuid5 are optional-producing functions when one of their
		// scalar arguments is optional.  Do not coerce a missing namespace or
		// name to the empty string: that would silently mint a different ID.
		// This mirrors the typed expression contract (optional in, optional
		// out) and keeps the evaluator aligned with the legacy Python
		// dataframer's UUID semantics.
		for _, value := range args {
			if isNull(value) {
				return nil, nil
			}
		}
		return namedUUID(strings.ToLower(e.Call), args)
	case "if":
		if truthy(args[0]) {
			return args[1], nil
		}
		return args[2], nil
	case "case":
		for i := 0; i+1 < len(args); i += 2 {
			if truthy(args[i]) {
				return args[i+1], nil
			}
		}
		if len(args)%2 == 1 {
			return args[len(args)-1], nil
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported call %q", e.Call)
	}
}

func selectPath(path string, ctx context) any {
	parts := strings.Split(strings.TrimSpace(path), ".")
	if len(parts) == 0 {
		return nil
	}
	current := any(nil)
	start := 0
	if value, ok := ctx[parts[0]]; ok {
		current = value
		start = 1
	} else if value, ok := ctx["root"]; ok {
		current = value
	}
	for _, part := range parts[start:] {
		current = selectSegment(current, part)
	}
	return current
}

func selectSegment(value any, segment string) any {
	if value == nil {
		return nil
	}
	iterate := strings.HasSuffix(segment, "[]")
	segment = strings.TrimSuffix(segment, "[]")
	if idx := strings.Index(segment, "["); idx >= 0 && strings.HasSuffix(segment, "]") {
		name := segment[:idx]
		n, _ := strconv.Atoi(segment[idx+1 : len(segment)-1])
		value = mapGet(value, name)
		if list := many(value); n >= 0 && n < len(list) {
			return list[n]
		}
		return nil
	}
	if segment != "" {
		value = mapGet(value, segment)
	}
	if iterate {
		return many(value)
	}
	return value
}

func mapGet(value any, key string) any {
	if obj, ok := value.(map[string]any); ok {
		return obj[key]
	}
	return nil
}
func many(value any) []any {
	if value == nil {
		return nil
	}
	if list, ok := value.([]any); ok {
		return list
	}
	return []any{value}
}
func distinct(values []any) []any {
	seen := map[string]struct{}{}
	out := []any{}
	for _, v := range values {
		b, _ := json.Marshal(v)
		k := string(b)
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}
func isNull(v any) bool { return v == nil }
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case nil:
		return false
	case string:
		return x != ""
	default:
		return true
	}
}
func cast(v any, target string) (any, error) {
	switch strings.ToLower(target) {
	case "string":
		return fmt.Sprint(v), nil
	case "integer", "int":
		i, err := strconv.ParseInt(fmt.Sprint(v), 10, 64)
		return i, err
	case "decimal", "float":
		f, err := strconv.ParseFloat(fmt.Sprint(v), 64)
		return f, err
	case "boolean", "bool":
		b, err := strconv.ParseBool(fmt.Sprint(v))
		return b, err
	default:
		return nil, fmt.Errorf("unsupported cast target %q", target)
	}
}
func pathSegment(v string) string {
	v = strings.TrimRight(v, "/")
	if idx := strings.LastIndexAny(v, "/#"); idx >= 0 {
		return v[idx+1:]
	}
	return v
}

var invalidName = regexp.MustCompile(`[^A-Za-z0-9_]`)

func sanitize(v string) string {
	v = invalidName.ReplaceAllString(v, "_")
	if v == "" {
		return "_"
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "_" + v
	}
	if strings.HasPrefix(v, "__") {
		return "_" + strings.TrimPrefix(v, "__")
	}
	return v
}
func namedUUID(name string, args []any) (string, error) {
	value, err := uuidcompat.Compute(name, args)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return value.(string), nil
}
func countPrefix(row map[string]any, prefix string) int {
	n := 0
	for name := range row {
		if strings.HasPrefix(name, prefix) {
			n++
		}
	}
	return n
}
