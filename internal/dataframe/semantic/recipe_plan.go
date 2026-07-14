package semantic

// This file is the single semantic boundary for persisted recipes and the
// existing GraphQL dataframe request. It deliberately stops before physical
// lowering: no collection, AQL, SQL, or backend implementation detail belongs
// in these types.

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

// RecipePlan is an immutable, checked semantic representation of a recipe
// bundle. Runtime bindings are request-scoped and are intentionally excluded
// from the persisted recipe digest.
type RecipePlan struct {
	Version            int
	RecipeDigest       string
	TranslationVersion string
	Bindings           recipe.RuntimeBindings
	Outputs            []OutputPlan
}

type OutputPlan struct {
	Name             string
	Root             SemanticNode
	RootResourceType string
	RowGrain         RowGrain
	Identity         *SemanticExpression
	Expansion        *SemanticExpansion
	Fields           []SemanticProjection
	DynamicMaps      []SemanticDynamicMap
	Collision        string
	DeclaredOrder    []string
}

// SemanticExpression keeps the checked typed AST together with the logical
// source location and lexical context that produced it. SourcePath is a
// recipe JSON path for diagnostics, not a physical query fragment.
type SemanticExpression struct {
	Expression expression.Expression
	Type       expression.Type
	SourcePath string
	Context    string
}

type SemanticProjection struct {
	Name      string
	FieldRef  string
	ValueMode string
	Expr      SemanticExpression
}

type SemanticExpansion struct {
	From SemanticExpression
	As   string
}

type SemanticDynamicMap struct {
	Name       string
	Source     SemanticExpression
	Key        *SemanticExpression
	Value      *SemanticExpression
	Columns    []string
	MaxColumns int
}

// RecipePlanExplanation is stable diagnostic output and contains only logical
// types and source paths. It never exposes a backend query or storage name.
type RecipePlanExplanation struct {
	Version            int
	RecipeDigest       string
	TranslationVersion string
	Outputs            []OutputPlanExplanation
}

type OutputPlanExplanation struct {
	Name       string
	Root       string
	RowGrain   RowGrain
	Fields     []ExpressionExplanation
	Identity   *ExpressionExplanation
	Expansion  *ExpansionExplanation
	DynamicMap []string
}

type ExpressionExplanation struct {
	SourcePath string
	Context    string
	Type       expression.Type
	Kind       expression.NodeKind
}

type ExpansionExplanation struct {
	SourcePath string
	As         string
}

// Explain returns a backend-neutral summary suitable for API diagnostics.
func (p RecipePlan) Explain() RecipePlanExplanation {
	out := RecipePlanExplanation{Version: p.Version, RecipeDigest: p.RecipeDigest, TranslationVersion: p.TranslationVersion, Outputs: make([]OutputPlanExplanation, 0, len(p.Outputs))}
	for _, output := range p.Outputs {
		e := OutputPlanExplanation{Name: output.Name, Root: output.RootResourceType, RowGrain: output.RowGrain, DynamicMap: make([]string, 0, len(output.DynamicMaps))}
		for _, field := range output.Fields {
			e.Fields = append(e.Fields, explainExpression(field.Expr))
		}
		if output.Identity != nil {
			x := explainExpression(*output.Identity)
			e.Identity = &x
		}
		if output.Expansion != nil {
			e.Expansion = &ExpansionExplanation{SourcePath: output.Expansion.From.SourcePath, As: output.Expansion.As}
		}
		for _, dynamic := range output.DynamicMaps {
			e.DynamicMap = append(e.DynamicMap, dynamic.Name)
		}
		out.Outputs = append(out.Outputs, e)
	}
	return out
}

func explainExpression(e SemanticExpression) ExpressionExplanation {
	return ExpressionExplanation{SourcePath: e.SourcePath, Context: e.Context, Type: e.Type, Kind: e.Expression.Kind}
}

// BuildRecipePlan lowers and type-checks every expression in a stored bundle.
// The recipe remains data; no output name or resource-specific branch is used
// here. Type resolution is lexical and schema-backed.
func BuildRecipePlan(bundle recipe.Bundle, bindings recipe.RuntimeBindings) (RecipePlan, error) {
	if bundle.Fragments != nil {
		expanded, err := bundle.ExpandFragments()
		if err != nil {
			return RecipePlan{}, err
		}
		bundle = expanded
	}
	if err := bundle.Validate(); err != nil {
		return RecipePlan{}, err
	}
	digest, err := bundle.Digest()
	if err != nil {
		return RecipePlan{}, err
	}
	plan := RecipePlan{Version: 1, RecipeDigest: digest, TranslationVersion: bundle.TranslationVersion, Bindings: cloneBindings(bindings), Outputs: make([]OutputPlan, 0, len(bundle.Outputs))}
	for index, output := range bundle.Outputs {
		compiled, err := buildRecipeOutput(output, bindings)
		if err != nil {
			return RecipePlan{}, fmt.Errorf("outputs[%d] %s: %w", index, output.Name, err)
		}
		plan.Outputs = append(plan.Outputs, compiled)
	}
	return plan, nil
}

// BuildRecipePlanFromBuilder adapts the existing GraphQL Builder into the
// same typed output representation. This keeps GraphQL and stored recipes on
// one semantic path while preserving the public request contract.
func BuildRecipePlanFromBuilder(builder Builder) (RecipePlan, error) {
	semanticPlan, err := BuildSemanticPlan(builder)
	if err != nil {
		return RecipePlan{}, err
	}
	output := OutputPlan{
		Name: builder.RootResourceType, Root: semanticPlan.Root,
		RootResourceType: builder.RootResourceType, RowGrain: semanticPlan.RowIdentity.Grain,
		Collision: "error", DeclaredOrder: make([]string, 0, len(builder.Fields)),
		Fields: make([]SemanticProjection, 0, len(builder.Fields)),
	}
	rootScope := newRootScope(builder.RootResourceType)
	root, fields, err := adaptBuilderNode(builder.RootResourceType, "root", builder.Fields, builder.Traversals, rootScope, "$.fields")
	if err != nil {
		return RecipePlan{}, err
	}
	overlayBuilderSemantics(&root, semanticPlan.Root)
	output.Root = root
	output.Fields = fields
	for _, field := range fields {
		output.DeclaredOrder = append(output.DeclaredOrder, field.Name)
	}
	return RecipePlan{Version: 1, TranslationVersion: "graphql-request", Bindings: recipe.RuntimeBindings{Project: builder.Project, DatasetGeneration: builder.DatasetGeneration, AuthResourcePaths: append([]string(nil), builder.AuthResourcePaths...)}, Outputs: []OutputPlan{output}}, nil
}

// overlayBuilderSemantics retains the existing GraphQL semantic selections
// that do not have a recipe wire equivalent yet (filters, pivots, aggregates,
// and representative slices) while replacing field expressions with the
// unified typed AST.
func overlayBuilderSemantics(dst *SemanticNode, src SemanticNode) {
	dst.Filters = append([]TypedFilter(nil), src.Filters...)
	dst.Pivots = append([]SemanticPivot(nil), src.Pivots...)
	dst.Aggregates = append([]SemanticAggregate(nil), src.Aggregates...)
	dst.Slices = append([]SemanticSlice(nil), src.Slices...)
	for index := range dst.Fields {
		for _, source := range src.Fields {
			if source.Name == dst.Fields[index].Name {
				dst.Fields[index].Selector = source.Selector
				dst.Fields[index].Fallbacks = append([]Selector(nil), source.Fallbacks...)
				break
			}
		}
	}
	for index := range dst.Children {
		if index < len(src.Children) {
			overlayBuilderSemantics(&dst.Children[index], src.Children[index])
		}
	}
}

type scopeBinding struct {
	ResourceType string
	Prefix       string
	// ExpandedItem means this alias denotes one element of a repeated
	// collection. The prefix retains the array path for schema resolution,
	// but that collection's cardinality must not leak into expressions scoped
	// to the item.
	ExpandedItem bool
}

type scopeFrame struct {
	aliases map[string]scopeBinding
}

func newRootScope(resourceType string) scopeFrame {
	return scopeFrame{aliases: map[string]scopeBinding{"root": {ResourceType: resourceType}}}
}

func (s scopeFrame) child(alias string, binding scopeBinding) (scopeFrame, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return scopeFrame{}, fmt.Errorf("alias is required")
	}
	if _, exists := s.aliases[alias]; exists {
		return scopeFrame{}, fmt.Errorf("alias %q shadows an existing lexical binding", alias)
	}
	child := scopeFrame{aliases: make(map[string]scopeBinding, len(s.aliases)+1)}
	for name, value := range s.aliases {
		child.aliases[name] = value
	}
	child.aliases[alias] = binding
	return child, nil
}

func (s scopeFrame) expression(input recipe.Expression, path string) (SemanticExpression, error) {
	expr, err := expression.FromRecipeInContexts(input, keys(s.aliases))
	if err != nil {
		return SemanticExpression{}, fmt.Errorf("%s: %w", path, err)
	}
	checked, err := expr.Check(expression.TypeContext{Resolve: s.resolve})
	if err != nil {
		return SemanticExpression{}, fmt.Errorf("%s: %w", path, err)
	}
	context := ""
	if expr.Selector != nil {
		context = expr.Selector.Context
	}
	return SemanticExpression{Expression: checked.Expression, Type: checked.Type, SourcePath: path, Context: context}, nil
}

func (s scopeFrame) resolve(ref expression.SelectorRef) (expression.Type, error) {
	alias := ref.Context
	if alias == "" {
		alias = "root"
	}
	binding, ok := s.aliases[alias]
	if !ok {
		return expression.Type{}, fmt.Errorf("selector context %q is not in scope", alias)
	}
	path := strings.TrimPrefix(strings.TrimSpace(ref.Path), ".")
	if path == "" {
		return expression.Type{}, fmt.Errorf("selector path is empty")
	}
	// An unqualified dotted selector may be either a nested root field or an
	// alias-qualified selector. If the generated schema cannot resolve it as a
	// root path, report the lexical alias error before the lower-level schema
	// diagnostic so recipe authors get an actionable scope failure.
	if ref.Context == "" {
		if parts := strings.SplitN(path, ".", 2); len(parts) == 2 {
			if _, visible := s.aliases[parts[0]]; !visible {
				if _, resolved := fhirschema.ResolveTerminalScalarMetadata(binding.ResourceType, path); !resolved {
					return expression.Type{}, fmt.Errorf("selector context %q is not in scope", parts[0])
				}
			}
		}
	}
	fullPath := path
	if binding.Prefix != "" {
		fullPath = binding.Prefix + "." + path
	}
	selector, err := spec.ParseSelector(fullPath)
	if err != nil {
		return expression.Type{}, err
	}
	repeated, _, err := spec.SelectorCardinality(binding.ResourceType, selector)
	if err != nil {
		if ref.Context == "" && strings.Contains(path, ".") {
			return expression.Type{}, fmt.Errorf("selector context or path %q is undefined: %w", path, err)
		}
		return expression.Type{}, err
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(binding.ResourceType, fullPath)
	if !ok {
		return expression.Type{}, fmt.Errorf("selector path %q is not in the active FHIR schema", fullPath)
	}
	kind := primitiveKind(metadata.Primitive)
	if kind == expression.KindObject {
		// A repeated object is a valid expansion source even though it has no
		// terminal scalar metadata.
		repeated = repeated || metadata.Repeated
	}
	if binding.ExpandedItem {
		// The expansion itself is the row-shaping operation. Any [] markers in
		// the item-relative selector still represent a many-valued expression,
		// while the repeated prefix used to reach the item does not.
		repeated = strings.Contains(path, "[]")
	}
	cardinality := expression.OptionalOne
	if repeated {
		cardinality = expression.Many
	}
	return expression.Type{Kind: kind, Cardinality: cardinality}, nil
}

func primitiveKind(kind fhirschema.PrimitiveKind) expression.ValueKind {
	switch kind {
	case fhirschema.PrimitiveBoolean:
		return expression.KindBoolean
	case fhirschema.PrimitiveInteger:
		return expression.KindInteger
	case fhirschema.PrimitiveDecimal:
		return expression.KindDecimal
	case fhirschema.PrimitiveDate:
		return expression.KindDate
	case fhirschema.PrimitiveDateTime:
		return expression.KindDateTime
	case fhirschema.PrimitiveString:
		return expression.KindString
	default:
		return expression.KindObject
	}
}

func buildRecipeOutput(output recipe.Output, bindings recipe.RuntimeBindings) (OutputPlan, error) {
	if !fhirschema.HasResource(output.RootResourceType) {
		return OutputPlan{}, fmt.Errorf("root resource type %q is not represented by the active generated FHIR schema", output.RootResourceType)
	}
	grain := RowGrain(output.RowGrain)
	if err := ValidateRootGrain(output.RootResourceType, grain); err != nil {
		// Persisted recipes may introduce a product-specific grain when they
		// also declare the row-shaping operation and an explicit identity. The
		// GraphQL request contract remains strict and continues to use
		// ValidateRootGrain above.
		if output.Expand == nil || output.Identity == nil || !validCustomGrain(string(grain)) {
			return OutputPlan{}, err
		}
	}
	scope := newRootScope(output.RootResourceType)
	if output.Expand != nil {
		// The source is checked in the parent lexical scope first, then its
		// selector path becomes the prefix for the expansion item alias.
		from, err := scope.expression(output.Expand.From, "expand.from")
		if err != nil {
			return OutputPlan{}, err
		}
		if from.Expression.Selector == nil || from.Type.Cardinality != expression.Many {
			return OutputPlan{}, fmt.Errorf("expand.from must be a repeated selector")
		}
		ref := from.Expression.Selector
		binding, err := scopeBindingForSelector(scope, *ref)
		if err != nil {
			return OutputPlan{}, err
		}
		prefix := binding.Prefix
		if prefix != "" {
			prefix += "."
		}
		// The expansion alias denotes one item, not the repeated collection.
		// An explicit index keeps schema cardinality scalar while retaining the
		// canonical array path for generated metadata.
		prefix += strings.TrimSuffix(strings.TrimPrefix(ref.Path, "."), "[]") + "[0]"
		scope, err = scope.child(output.Expand.As, scopeBinding{ResourceType: binding.ResourceType, Prefix: prefix, ExpandedItem: true})
		if err != nil {
			return OutputPlan{}, err
		}
		plan := OutputPlan{Name: output.Name, RootResourceType: output.RootResourceType, RowGrain: grain, Collision: output.CollisionPolicy, Expansion: &SemanticExpansion{From: from, As: output.Expand.As}}
		return finishRecipeOutput(plan, output, scope, bindings)
	}
	plan := OutputPlan{Name: output.Name, RootResourceType: output.RootResourceType, RowGrain: grain, Collision: output.CollisionPolicy}
	return finishRecipeOutput(plan, output, scope, bindings)
}

func validCustomGrain(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for index, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (index > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func finishRecipeOutput(plan OutputPlan, output recipe.Output, scope scopeFrame, bindings recipe.RuntimeBindings) (OutputPlan, error) {
	if plan.Collision == "" {
		plan.Collision = "error"
	}
	plan.Fields = make([]SemanticProjection, 0, len(output.Fields))
	plan.DeclaredOrder = make([]string, 0, len(output.Fields))
	plan.Root = SemanticNode{Alias: "root", ResourceType: output.RootResourceType, Fields: make([]SemanticField, 0, len(output.Fields))}
	for index, field := range output.Fields {
		x, err := scope.expression(field.Expr, fmt.Sprintf("fields[%d].expr", index))
		if err != nil {
			return OutputPlan{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
		projection := SemanticProjection{Name: field.Name, Expr: x}
		plan.Fields = append(plan.Fields, projection)
		plan.DeclaredOrder = append(plan.DeclaredOrder, field.Name)
		plan.Root.Fields = append(plan.Root.Fields, semanticFieldFromProjection(projection))
	}
	for index, traversal := range output.Traversals {
		child, err := buildRecipeTraversal(traversal, scope, fmt.Sprintf("traversals[%d]", index))
		if err != nil {
			return OutputPlan{}, err
		}
		plan.Root.Children = append(plan.Root.Children, child)
	}
	if output.Identity != nil {
		x, err := scope.expression(output.Identity.Expr, "identity.expr")
		if err != nil {
			return OutputPlan{}, err
		}
		if x.Type.Cardinality == expression.Many || x.Type.Kind == expression.KindObject || x.Type.Kind == expression.KindNull {
			return OutputPlan{}, fmt.Errorf("identity expression must resolve to one scalar value")
		}
		plan.Identity = &x
	}
	for index, dynamic := range output.DynamicColumns {
		item := SemanticDynamicMap{Name: dynamic.Name, Columns: append([]string(nil), dynamic.Columns...), MaxColumns: dynamic.MaxColumns}
		var err error
		item.Source, err = scope.expression(dynamic.Source, fmt.Sprintf("dynamicColumns[%d].source", index))
		if err != nil {
			return OutputPlan{}, err
		}
		dynamicScope := scope
		if selector := item.Source.Expression.Selector; selector != nil && strings.Contains(selector.Path, "[]") {
			binding, scopeErr := scopeBindingForSelector(scope, *selector)
			if scopeErr != nil {
				return OutputPlan{}, scopeErr
			}
			path := strings.TrimPrefix(strings.TrimSpace(selector.Path), ".")
			prefix := strings.TrimPrefix(binding.Prefix+"."+path, ".")
			dynamicScope, err = scope.child("item", scopeBinding{ResourceType: binding.ResourceType, Prefix: prefix, ExpandedItem: true})
			if err != nil {
				return OutputPlan{}, fmt.Errorf("dynamicColumns[%d] item scope: %w", index, err)
			}
		}
		if dynamic.Key != nil {
			x, err := dynamicScope.expression(*dynamic.Key, fmt.Sprintf("dynamicColumns[%d].key", index))
			if err != nil {
				return OutputPlan{}, err
			}
			item.Key = &x
		}
		if dynamic.Value != nil {
			x, err := dynamicScope.expression(*dynamic.Value, fmt.Sprintf("dynamicColumns[%d].value", index))
			if err != nil {
				return OutputPlan{}, err
			}
			item.Value = &x
		}
		plan.DynamicMaps = append(plan.DynamicMaps, item)
	}
	_ = bindings // retained in RecipePlan; output compilation is scope-only
	return plan, nil
}

func buildRecipeTraversal(input recipe.Traversal, parent scopeFrame, path string) (SemanticNode, error) {
	if !fhirschema.HasResource(input.ToResourceType) {
		return SemanticNode{}, fmt.Errorf("%s: target resource type %q is not represented by the active generated FHIR schema", path, input.ToResourceType)
	}
	alias := input.Alias
	if strings.TrimSpace(alias) == "" {
		alias = input.Name
	}
	scope, err := parent.child(alias, scopeBinding{ResourceType: input.ToResourceType})
	if err != nil {
		return SemanticNode{}, fmt.Errorf("%s: %w", path, err)
	}
	node := SemanticNode{Alias: alias, ResourceType: input.ToResourceType, EdgeLabel: input.Name, MatchMode: TraversalMatchMode(strings.ToUpper(input.MatchMode))}
	if node.MatchMode == "" {
		node.MatchMode = spec.TraversalMatchOptional
	}
	if input.From != nil {
		x, err := parent.expression(*input.From, path+".from")
		if err != nil {
			return SemanticNode{}, err
		}
		node.From = &x
	}
	for index, field := range input.Fields {
		x, err := scope.expression(field.Expr, fmt.Sprintf("%s.fields[%d].expr", path, index))
		if err != nil {
			return SemanticNode{}, err
		}
		node.Fields = append(node.Fields, semanticFieldFromProjection(SemanticProjection{Name: field.Name, Expr: x}))
	}
	for index, child := range input.Traversals {
		nested, err := buildRecipeTraversal(child, scope, fmt.Sprintf("%s.traversals[%d]", path, index))
		if err != nil {
			return SemanticNode{}, err
		}
		node.Children = append(node.Children, nested)
	}
	return node, nil
}

func semanticFieldFromProjection(p SemanticProjection) SemanticField {
	field := SemanticField{Name: p.Name, FieldRef: p.FieldRef, ValueMode: p.ValueMode, Expr: &p.Expr.Expression, ExprType: p.Expr.Type, SourcePath: p.Expr.SourcePath}
	if p.Expr.Expression.Selector != nil {
		selector, err := spec.ParseSelector(p.Expr.Expression.Selector.Path)
		if err == nil {
			field.Selector = selector
		}
	}
	return field
}

func scopeBindingForSelector(scope scopeFrame, ref expression.SelectorRef) (scopeBinding, error) {
	alias := ref.Context
	if alias == "" {
		alias = "root"
	}
	binding, ok := scope.aliases[alias]
	if !ok {
		return scopeBinding{}, fmt.Errorf("selector context %q is not in scope", alias)
	}
	return binding, nil
}

func adaptBuilderNode(resourceType, alias string, fields []FieldSelect, traversals []TraversalStep, parent scopeFrame, path string) (SemanticNode, []SemanticProjection, error) {
	node := SemanticNode{Alias: alias, ResourceType: resourceType, Fields: make([]SemanticField, 0, len(fields))}
	projections := make([]SemanticProjection, 0, len(fields))
	for index, field := range fields {
		input := recipe.Expression{Select: qualifySelect(alias, field.Select)}
		if len(field.FallbackSelects) > 0 {
			args := []recipe.Expression{input}
			for _, fallback := range field.FallbackSelects {
				args = append(args, recipe.Expression{Select: qualifySelect(alias, fallback)})
			}
			input = recipe.Expression{Call: "coalesce", Args: args}
		}
		x, err := parent.expression(input, fmt.Sprintf("%s[%d].expr", path, index))
		if err != nil {
			return SemanticNode{}, nil, err
		}
		projection := SemanticProjection{Name: field.Name, FieldRef: field.FieldRef, ValueMode: field.ValueMode, Expr: x}
		projections = append(projections, projection)
		node.Fields = append(node.Fields, semanticFieldFromProjection(projection))
	}
	for index, traversal := range traversals {
		childAlias := traversal.Alias
		if childAlias == "" {
			childAlias = traversal.Label
		}
		childScope, err := parent.child(childAlias, scopeBinding{ResourceType: traversal.ToResourceType})
		if err != nil {
			return SemanticNode{}, nil, fmt.Errorf("traversals[%d]: %w", index, err)
		}
		child, childFields, err := adaptBuilderNode(traversal.ToResourceType, childAlias, traversal.Fields, traversal.Traversals, childScope, fmt.Sprintf("%s.traversals[%d].fields", path, index))
		if err != nil {
			return SemanticNode{}, nil, err
		}
		child.EdgeLabel, child.MatchMode = traversal.Label, traversal.MatchMode
		if child.MatchMode == "" {
			child.MatchMode = spec.TraversalMatchOptional
		}
		node.Children = append(node.Children, child)
		_ = childFields
	}
	return node, projections, nil
}

func qualifySelect(alias, selectText string) string {
	selectText = strings.TrimSpace(selectText)
	if selectText == "" {
		return alias + ".__missing__"
	}
	if strings.Contains(selectText, ".") && strings.HasPrefix(selectText, alias+".") {
		return selectText
	}
	return alias + "." + selectText
}

func cloneBindings(in recipe.RuntimeBindings) recipe.RuntimeBindings {
	in.AuthResourcePaths = append([]string(nil), in.AuthResourcePaths...)
	return in
}

func keys(values map[string]scopeBinding) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for key := range values {
		result[key] = struct{}{}
	}
	return result
}
