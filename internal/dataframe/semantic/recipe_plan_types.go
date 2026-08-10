package semantic

// This file is the single semantic boundary for persisted recipes and the
// existing GraphQL dataframe request. It deliberately stops before physical
// lowering: no collection, AQL, SQL, or backend implementation detail belongs
// in these types.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

var semanticBindingNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	RowGrain         spec.RowGrain
	Identity         *SemanticExpression
	// Unnest is the canonical semantic row-producing operation for an output.
	// Persisted recipe expand syntax is lowered into this operation; consumers
	// must not infer cardinality-changing behavior from a transport-facing
	// explanation field; the typed UNNEST operation is authoritative.
	Unnest             *SemanticUnnest
	Fields             []SemanticProjection
	DynamicMaps        []SemanticDynamicMap
	CatalogProjections []string
	Collision          string
	DeclaredOrder      []string
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

// UnnestJoinMode makes null/empty collection behavior explicit at the
// semantic boundary. The renderer must not infer this from the AQL context.
type UnnestJoinMode string

const (
	UnnestInner UnnestJoinMode = "INNER"
	UnnestOuter UnnestJoinMode = "OUTER"
)

// SemanticUnnest is a backend-neutral row-producing operation. It is the
// semantic representation of recipe expand syntax and is available to any
// frontend that needs one output row per element of a repeated value.
//
// Source is evaluated once per parent row. As introduces the item binding;
// Ordinality, when non-empty, introduces a deterministic zero-based position
// binding. The operation changes row cardinality and therefore is not a
// projection or ordinary expression.
type SemanticUnnest struct {
	Source     SemanticExpression
	As         string
	Ordinality string
	JoinMode   UnnestJoinMode
}

// Validate checks the semantic invariants that are independent of a backend
// renderer. Lexical scope validation belongs to the caller because only the
// surrounding output scope knows which bindings are visible.
func (u SemanticUnnest) Validate() error {
	if u.Source.Type.Cardinality != expression.Many {
		return fmt.Errorf("unnest source must be a repeated expression, got %s", u.Source.Type)
	}
	if strings.TrimSpace(u.As) == "" {
		return fmt.Errorf("unnest item binding is required")
	}
	if !semanticBindingNamePattern.MatchString(u.As) {
		return fmt.Errorf("unnest item binding %q is not a safe logical name", u.As)
	}
	if strings.TrimSpace(u.Ordinality) != "" {
		if !semanticBindingNamePattern.MatchString(u.Ordinality) {
			return fmt.Errorf("unnest ordinality binding %q is not a safe logical name", u.Ordinality)
		}
		if u.Ordinality == u.As {
			return fmt.Errorf("unnest ordinality binding must differ from item binding %q", u.As)
		}
	}
	switch u.JoinMode {
	case UnnestInner, UnnestOuter:
		return nil
	case "":
		return fmt.Errorf("unnest join mode is required")
	default:
		return fmt.Errorf("unsupported unnest join mode %q", u.JoinMode)
	}
}

type SemanticDynamicMap struct {
	Name             string
	ColumnPrefix     *string
	ScopeAlias       string
	ResourceType     string
	Source           SemanticExpression
	Key              *SemanticExpression
	Value            *SemanticExpression
	Columns          []string
	ColumnTypes      map[string]string
	ColumnSourceKeys map[string]string
	// AllowUnknownKeys is used by schema-aware projections that intentionally
	// select one key from a shared runtime map. Other keys in that map are
	// siblings, not schema drift; frozen matching keys still receive type checks.
	AllowUnknownKeys bool
	MaxColumns       int
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
	Name               string
	Root               string
	RowGrain           spec.RowGrain
	Fields             []ExpressionExplanation
	Identity           *ExpressionExplanation
	Unnest             *UnnestExplanation
	Expansion          *ExpansionExplanation
	DynamicMap         []string
	CatalogProjections []string
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

type UnnestExplanation struct {
	SourcePath string
	As         string
	Ordinality string
	JoinMode   UnnestJoinMode
}

// Explain returns a backend-neutral summary suitable for API diagnostics.
func (p RecipePlan) Explain() RecipePlanExplanation {
	out := RecipePlanExplanation{Version: p.Version, RecipeDigest: p.RecipeDigest, TranslationVersion: p.TranslationVersion, Outputs: make([]OutputPlanExplanation, 0, len(p.Outputs))}
	for _, output := range p.Outputs {
		e := OutputPlanExplanation{Name: output.Name, Root: output.RootResourceType, RowGrain: output.RowGrain, DynamicMap: make([]string, 0, len(output.DynamicMaps)), CatalogProjections: append([]string(nil), output.CatalogProjections...)}
		for _, field := range output.Fields {
			e.Fields = append(e.Fields, explainExpression(field.Expr))
		}
		if output.Identity != nil {
			x := explainExpression(*output.Identity)
			e.Identity = &x
		}
		if output.Unnest != nil {
			e.Unnest = &UnnestExplanation{SourcePath: output.Unnest.Source.SourcePath, As: output.Unnest.As, Ordinality: output.Unnest.Ordinality, JoinMode: output.Unnest.JoinMode}
		}
		if output.Unnest != nil {
			e.Expansion = &ExpansionExplanation{SourcePath: output.Unnest.Source.SourcePath, As: output.Unnest.As}
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
