// Package recipeengine is Loom's production recipe execution seam. It owns
// recipe resolution, scoped discovery, physical lowering, and streaming row
// execution; transport adapters must depend on this package rather than
// interpreting recipe expressions themselves.
package recipeengine

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeexec"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

type Registry interface {
	LoadRecipe(context.Context, string) (recipeexec.Entry, error)
}

// LocalRegistry adapts the process-local registry for tests and local runs.
// Production deployments should provide the durable Arango adapter.
type LocalRegistry struct{ Registry *recipeexec.Registry }

func (r LocalRegistry) LoadRecipe(_ context.Context, name string) (recipeexec.Entry, error) {
	if r.Registry == nil {
		return recipeexec.Entry{}, fmt.Errorf("recipe registry is required")
	}
	entry, ok := r.Registry.Get(name)
	if !ok {
		return recipeexec.Entry{}, fmt.Errorf("%w: %s", recipeexec.ErrRecipeNotFound, name)
	}
	return entry, nil
}

type QueryRows func(context.Context, string, int, map[string]any, func(map[string]any) error) error

type Config struct {
	Registry    Registry
	Discover    semantic.DiscoverFunc
	QueryRows   QueryRows
	ScopeDigest func(recipe.RuntimeBindings) string
	BatchSize   int
}

type Engine struct {
	registry    Registry
	discover    semantic.DiscoverFunc
	queryRows   QueryRows
	scopeDigest func(recipe.RuntimeBindings) string
	batchSize   int
}

type Resolved struct {
	Semantic semantic.ResolvedRecipePlan
	Physical lower.RecipePhysicalPlan
}

type OutputStream struct {
	Name          string
	Columns       []string
	Query         string
	BindVars      map[string]any
	DynamicChecks map[string]map[string]DynamicColumnCheck
	stream        QueryRows
	batchSize     int
}

type DynamicColumnCheck struct {
	ColumnName string
	ValueType  string
}

type StreamResult struct {
	Output   string
	Columns  []string
	RowCount int
}

func New(cfg Config) (*Engine, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("recipe registry is required")
	}
	if cfg.QueryRows == nil {
		return nil, fmt.Errorf("recipe query executor is required")
	}
	batch := cfg.BatchSize
	if batch <= 0 {
		batch = 1000
	}
	return &Engine{registry: cfg.Registry, discover: cfg.Discover, queryRows: cfg.QueryRows, scopeDigest: cfg.ScopeDigest, batchSize: batch}, nil
}

func (e *Engine) Resolve(ctx context.Context, name string, bindings recipe.RuntimeBindings) (Resolved, error) {
	if strings.TrimSpace(bindings.Project) == "" {
		return Resolved{}, fmt.Errorf("recipe project is required")
	}
	if strings.TrimSpace(bindings.DatasetGeneration) == "" {
		return Resolved{}, fmt.Errorf("recipe dataset generation is required")
	}
	entry, err := e.registry.LoadRecipe(ctx, name)
	if err != nil {
		return Resolved{}, err
	}
	semanticPlan, err := semantic.BuildRecipePlan(entry.Bundle, bindings)
	if err != nil {
		return Resolved{}, err
	}
	scope := ""
	if e.scopeDigest != nil {
		scope = e.scopeDigest(bindings)
	}
	resolved, err := semantic.ResolveRecipePlan(ctx, semanticPlan, scope, bindings.DatasetGeneration, e.discover)
	if err != nil {
		return Resolved{}, err
	}
	physical, err := lower.LowerResolvedRecipePlan(resolved)
	if err != nil {
		return Resolved{}, err
	}
	return Resolved{Semantic: resolved, Physical: physical}, nil
}

func (e *Engine) Streams(ctx context.Context, resolved Resolved) ([]OutputStream, error) {
	streams := make([]OutputStream, 0, len(resolved.Physical.Outputs))
	for _, output := range resolved.Physical.Outputs {
		query, bindVars, columns, err := renderOutput(resolved, output)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", output.Name, err)
		}
		streams = append(streams, OutputStream{Name: output.Name, Columns: columns, Query: query, BindVars: bindVars, DynamicChecks: dynamicChecks(output), stream: e.queryRows, batchSize: e.batchSize})
	}
	return streams, nil
}

func (e *Engine) Preview(ctx context.Context, resolved Resolved, limit int) (map[string][]map[string]any, error) {
	if limit <= 0 {
		limit = 25
	}
	streams, err := e.Streams(ctx, resolved)
	if err != nil {
		return nil, err
	}
	result := make(map[string][]map[string]any, len(streams))
	for _, stream := range streams {
		rows := make([]map[string]any, 0, limit)
		count := 0
		err := stream.stream(ctx, stream.Query, stream.batchSize, stream.BindVars, func(row map[string]any) error {
			if count >= limit {
				return errPreviewLimit
			}
			resolved, err := materializePostQueryRowWithChecks(row, stream.DynamicChecks)
			if err != nil {
				return err
			}
			rows = append(rows, resolved)
			count++
			return nil
		})
		if err != nil && err != errPreviewLimit {
			return nil, fmt.Errorf("output %q: %w", stream.Name, err)
		}
		result[stream.Name] = rows
	}
	return result, nil
}

var errPreviewLimit = fmt.Errorf("preview limit reached")

func (s OutputStream) Stream(ctx context.Context, visit func(map[string]any) error) (StreamResult, error) {
	if visit == nil {
		return StreamResult{}, fmt.Errorf("row visitor is required")
	}
	count := 0
	err := s.stream(ctx, s.Query, s.batchSize, s.BindVars, func(row map[string]any) error {
		resolved, err := materializePostQueryRowWithChecks(row, s.DynamicChecks)
		if err != nil {
			return err
		}
		count++
		return visit(resolved)
	})
	return StreamResult{Output: s.Name, Columns: append([]string(nil), s.Columns...), RowCount: count}, err
}

func dynamicChecks(output lower.RecipePhysicalOutput) map[string]map[string]DynamicColumnCheck {
	checks := make(map[string]map[string]DynamicColumnCheck)
	for _, dynamic := range output.DynamicMaps {
		columns := make(map[string]DynamicColumnCheck, len(dynamic.ResolvedColumns))
		for _, column := range dynamic.ResolvedColumns {
			columns[column.SourceKey] = DynamicColumnCheck{ColumnName: column.Name, ValueType: column.ValueType}
		}
		if len(columns) > 0 {
			checks[dynamic.Name] = columns
		}
	}
	return checks
}

func cloneRow(row map[string]any) map[string]any {
	copy := make(map[string]any, len(row))
	for key, value := range row {
		copy[key] = value
	}
	return copy
}

var safeFieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func renderOutput(resolved Resolved, output lower.RecipePhysicalOutput) (string, map[string]any, []string, error) {
	binds := make(map[string]any, len(resolved.Physical.BindVars)+8)
	for key, value := range resolved.Physical.BindVars {
		binds[key] = value
	}
	for key, value := range map[string]any{
		"project":                          resolved.Semantic.SemanticPlan.Bindings.Project,
		"dataset_generation":               resolved.Semantic.SemanticPlan.Bindings.DatasetGeneration,
		"auth_resource_paths":              append([]string(nil), resolved.Semantic.SemanticPlan.Bindings.AuthResourcePaths...),
		"auth_resource_paths_unrestricted": len(resolved.Semantic.SemanticPlan.Bindings.AuthResourcePaths) == 0,
		"recipe_root_collection":           output.RootResourceType,
	} {
		binds[key] = value
	}
	lines := []string{"FOR root IN @@recipe_root_collection", "  FILTER root.project == @project", "  FILTER root.dataset_generation == @dataset_generation", "  FILTER @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths"}
	for index, traversal := range output.Root {
		if err := renderTraversal(&lines, &binds, traversal, "root", index+1); err != nil {
			return "", nil, nil, err
		}
	}
	if output.Expansion != nil {
		from, next, err := aql.RenderRecipeExpression(output.Expansion.From, binds)
		if err != nil {
			return "", nil, nil, fmt.Errorf("expand source: %w", err)
		}
		binds = next
		lines = append(lines, "  FOR "+safeVariable(output.Expansion.As)+" IN "+from)
	}
	columns := make([]string, 0, len(output.Fields))
	parts := make([]string, 0, len(output.Fields))
	dynamicObserved := make([]string, 0, len(output.DynamicMaps))
	for _, field := range output.Fields {
		if !safeFieldName.MatchString(field.Name) {
			return "", nil, nil, fmt.Errorf("unsafe output field %q", field.Name)
		}
		expr := rewriteExpressionVariable(field.Expr, output.Expansion)
		value, next, err := aql.RenderRecipeExpression(expr, binds)
		if err != nil {
			return "", nil, nil, fmt.Errorf("field %q: %w", field.Name, err)
		}
		binds = next
		columns = append(columns, field.Name)
		parts = append(parts, fmt.Sprintf("%q: %s", field.Name, value))
	}
	for _, traversal := range output.Root {
		if err := renderTraversalFields(&parts, &columns, &binds, traversal); err != nil {
			return "", nil, nil, err
		}
	}
	if output.Identity != nil {
		identity, next, err := aql.RenderRecipeExpression(rewriteExpressionVariable(*output.Identity, output.Expansion), binds)
		if err != nil {
			return "", nil, nil, fmt.Errorf("identity: %w", err)
		}
		binds = next
		parts = append(parts, fmt.Sprintf("%q: %s", "__loom_row_id", identity))
	}
	for _, dynamic := range output.DynamicMaps {
		dynamicParts, dynamicColumns, observed, next, err := renderDynamicMap(dynamic, binds)
		if err != nil {
			return "", nil, nil, fmt.Errorf("dynamic map %q: %w", dynamic.Name, err)
		}
		binds = next
		for _, column := range dynamicColumns {
			for _, existing := range columns {
				if existing == column {
					return "", nil, nil, fmt.Errorf("dynamic column %q collides with another output column", column)
				}
			}
			columns = append(columns, column)
		}
		parts = append(parts, dynamicParts...)
		if observed != "" {
			dynamicObserved = append(dynamicObserved, fmt.Sprintf("%q: %s", dynamic.Name, observed))
		}
	}
	if len(parts) == 0 {
		return "", nil, nil, fmt.Errorf("output %q has no projected fields", output.Name)
	}
	if len(dynamicObserved) > 0 {
		parts = append(parts, fmt.Sprintf("%q: {%s}", "__loom_dynamic_runtime_keys", strings.Join(dynamicObserved, ", ")))
	}
	lines = append(lines, "  RETURN {"+strings.Join(parts, ", ")+"}")
	return strings.Join(lines, "\n") + "\n", binds, columns, nil
}

func renderDynamicMap(dynamic lower.RecipePhysicalDynamicMap, binds map[string]any) ([]string, []string, string, map[string]any, error) {
	if len(dynamic.ResolvedColumns) == 0 {
		return nil, nil, "", binds, nil
	}
	source, next, err := aql.RenderRecipeExpression(dynamic.Source, binds)
	if err != nil {
		return nil, nil, "", nil, fmt.Errorf("source: %w", err)
	}
	parts := make([]string, 0, len(dynamic.ResolvedColumns))
	columns := make([]string, 0, len(dynamic.ResolvedColumns))
	item := "recipe_dynamic_item"
	keyExpr := item
	if dynamic.Key != nil {
		keyExpression := rewritePhysicalVariable(*dynamic.Key, "item", item)
		keyExpr, next, err = aql.RenderRecipeExpression(keyExpression, next)
		if err != nil {
			return nil, nil, "", nil, fmt.Errorf("key: %w", err)
		}
	}
	valueExpr := item
	if dynamic.Value != nil {
		valueExpression := rewritePhysicalVariable(*dynamic.Value, "item", item)
		valueExpr, next, err = aql.RenderRecipeExpression(valueExpression, next)
		if err != nil {
			return nil, nil, "", nil, fmt.Errorf("value: %w", err)
		}
	}
	for index, column := range dynamic.ResolvedColumns {
		if !safeFieldName.MatchString(column.Name) {
			return nil, nil, "", nil, fmt.Errorf("unsafe resolved dynamic output field %q", column.Name)
		}
		key := column.SourceKey
		if key == "" {
			key = strings.TrimPrefix(column.Name, dynamic.Name+"_")
		}
		keyBind := fmt.Sprintf("recipe_dynamic_key_%d", index)
		next[keyBind] = key
		projection := fmt.Sprintf("FIRST(FOR %s IN %s FILTER %s == @%s RETURN %s)", item, source, keyExpr, keyBind, valueExpr)
		parts = append(parts, fmt.Sprintf("%q: %s", column.Name, projection))
		columns = append(columns, column.Name)
	}
	observed := fmt.Sprintf("SORTED_UNIQUE(FOR %s IN %s RETURN TO_STRING(%s))", "recipe_dynamic_observed_item", source, keyExpr)
	return parts, columns, observed, next, nil
}

func rewritePhysicalVariable(expression lower.PhysicalExpression, from, to string) lower.PhysicalExpression {
	if expression.Extract != nil && expression.Extract.Source.Variable == from {
		expression.Extract.Source.Variable = to
	}
	if expression.Call != nil {
		for index := range expression.Call.Args {
			expression.Call.Args[index] = rewritePhysicalVariable(expression.Call.Args[index], from, to)
		}
	}
	return expression
}

func renderTraversal(lines *[]string, binds *map[string]any, traversal lower.RecipePhysicalTraversal, parent string, ordinal int) error {
	alias := safeVariable(traversal.Alias)
	edge := fmt.Sprintf("recipe_edge_%d", ordinal)
	target := fmt.Sprintf("recipe_target_%d", ordinal)
	edgeCollection := fmt.Sprintf("recipe_edge_collection_%d", ordinal)
	targetCollection := fmt.Sprintf("recipe_target_collection_%d", ordinal)
	(*binds)[edgeCollection] = "fhir_edge"
	(*binds)[targetCollection] = traversal.ToResourceType
	(*binds)["recipe_edge_label_"+fmt.Sprint(ordinal)] = traversal.Name
	parentEndpoint, targetEndpoint := "_to", "_from"
	switch strings.ToUpper(traversal.Direction) {
	case "OUTBOUND":
		parentEndpoint, targetEndpoint = "_from", "_to"
	case "INBOUND":
		parentEndpoint, targetEndpoint = "_to", "_from"
	case "ANY":
		parentEndpoint, targetEndpoint = "", ""
	default:
		return fmt.Errorf("traversal %q has unsupported physical direction %q", traversal.Name, traversal.Direction)
	}
	query := []string{
		fmt.Sprintf("  LET %s = FIRST(FOR %s IN @@%s", alias, edge, edgeCollection),
		fmt.Sprintf("      FILTER %s.project == @project", edge),
		fmt.Sprintf("      FILTER %s.dataset_generation == @dataset_generation", edge),
		fmt.Sprintf("      FILTER %s.label == @recipe_edge_label_%d", edge, ordinal),
	}
	if parentEndpoint == "" {
		query = append(query, fmt.Sprintf("      FILTER %s._from == %s._id OR %s._to == %s._id", edge, parent, edge, parent))
	} else {
		query = append(query, fmt.Sprintf("      FILTER %s.%s == %s._id", edge, parentEndpoint, parent))
	}
	query = append(query,
		fmt.Sprintf("      FOR %s IN @@%s", target, targetCollection),
		fmt.Sprintf("        FILTER %s._id == %s.%s", target, edge, targetEndpoint),
		fmt.Sprintf("        FILTER %s.project == @project", target),
		fmt.Sprintf("        FILTER %s.dataset_generation == @dataset_generation", target),
		fmt.Sprintf("        FILTER @auth_resource_paths_unrestricted == true OR (%s.auth_resource_path IN @auth_resource_paths AND %s.auth_resource_path IN @auth_resource_paths)", edge, target),
		fmt.Sprintf("        RETURN %s", target),
		"  )")
	*lines = append(*lines, query...)
	for childIndex, child := range traversal.Children {
		if err := renderTraversal(lines, binds, child, alias, ordinal+childIndex+1); err != nil {
			return err
		}
	}
	return nil
}

func renderTraversalFields(parts *[]string, columns *[]string, binds *map[string]any, traversal lower.RecipePhysicalTraversal) error {
	for _, field := range traversal.Fields {
		if !safeFieldName.MatchString(field.Name) {
			return fmt.Errorf("unsafe traversal output field %q", field.Name)
		}
		value, next, err := aql.RenderRecipeExpression(field.Expr, *binds)
		if err != nil {
			return fmt.Errorf("traversal field %q: %w", field.Name, err)
		}
		*binds = next
		*columns = append(*columns, field.Name)
		*parts = append(*parts, fmt.Sprintf("%q: %s", field.Name, value))
	}
	for _, child := range traversal.Children {
		if err := renderTraversalFields(parts, columns, binds, child); err != nil {
			return err
		}
	}
	return nil
}

func safeVariable(value string) string {
	value = strings.TrimSpace(value)
	if safeFieldName.MatchString(value) {
		return value
	}
	return "recipe_alias"
}

func rewriteExpressionVariable(expr lower.PhysicalExpression, expansion *lower.RecipePhysicalExpansion) lower.PhysicalExpression {
	if expansion == nil || expansion.As == "" {
		return expr
	}
	if expr.Extract != nil && expr.Extract.Source.Variable == expansion.As {
		expr.Extract.Source.Variable = expansion.As
		expr.Extract.Source.Path = nil
	}
	if expr.Call != nil {
		for i := range expr.Call.Args {
			expr.Call.Args[i] = rewriteExpressionVariable(expr.Call.Args[i], expansion)
		}
	}
	return expr
}
