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
	Name                  string
	Root                  SemanticNode
	RootResourceType      string
	RowGrain              spec.RowGrain
	RootColumnNaming      recipe.RootColumnNaming
	TraversalColumnNaming recipe.TraversalColumnNaming
	Identity              *SemanticExpression
	// RowExpansion is the sole semantic row-producing operation for an output.
	RowExpansion       *SemanticRowExpansion
	ExpansionIdentity  bool
	DynamicMaps        []SemanticDynamicMap
	CatalogProjections []string
	Collision          string
	Population         *SemanticPopulation
}

type SemanticPopulation struct {
	SelectionRevisionID string
	MembershipDigest    string
	MemberCount         int64
	ResourceType        string
	Route               []SemanticPopulationRouteStep
}

type SemanticPopulationRouteStep struct {
	ResourceType string
	Relationship string
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

// ExpansionEmptyPolicy defines what an existing owner contributes when its
// selected collection is null or empty.
type ExpansionEmptyPolicy string

const (
	ExpansionError          ExpansionEmptyPolicy = "ERROR"
	ExpansionExclude        ExpansionEmptyPolicy = "EXCLUDE"
	ExpansionPreserveParent ExpansionEmptyPolicy = "PRESERVE_PARENT"
)

type SemanticOccurrence struct {
	OccurrenceID string
	Alias        string
	ResourceType string
}

// SemanticRowExpansion is the single checked cardinality boundary for an
// output. Its owner is an exact authored route occurrence, not merely a
// resource type or selector prefix.
type SemanticRowExpansion struct {
	Owner       SemanticOccurrence
	Source      SemanticExpression
	ItemBinding string
	Ordinality  string
	EmptyPolicy ExpansionEmptyPolicy
}

func (u SemanticRowExpansion) Validate() error {
	if u.Owner.Alias == "" || u.Owner.ResourceType == "" {
		return fmt.Errorf("row expansion owner occurrence is incomplete")
	}
	if u.Source.Type.Cardinality != expression.Many {
		return fmt.Errorf("row expansion source must be repeated, got %s", u.Source.Type)
	}
	if strings.TrimSpace(u.ItemBinding) == "" {
		return fmt.Errorf("row expansion item binding is required")
	}
	if !semanticBindingNamePattern.MatchString(u.ItemBinding) {
		return fmt.Errorf("row expansion item binding %q is not a safe logical name", u.ItemBinding)
	}
	if strings.TrimSpace(u.Ordinality) != "" {
		if !semanticBindingNamePattern.MatchString(u.Ordinality) {
			return fmt.Errorf("row expansion ordinality binding %q is not a safe logical name", u.Ordinality)
		}
		if u.Ordinality == u.ItemBinding {
			return fmt.Errorf("row expansion ordinality binding must differ from item binding %q", u.ItemBinding)
		}
	}
	switch u.EmptyPolicy {
	case ExpansionError, ExpansionExclude, ExpansionPreserveParent:
		return nil
	case "":
		return fmt.Errorf("row expansion empty policy is required")
	default:
		return fmt.Errorf("unsupported row expansion empty policy %q", u.EmptyPolicy)
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
	Discovered       bool
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
	SourcePath        string
	OwnerOccurrenceID string
	As                string
	Ordinality        string
	EmptyPolicy       ExpansionEmptyPolicy
}

// Explain returns a backend-neutral summary suitable for API diagnostics.
func (p RecipePlan) Explain() RecipePlanExplanation {
	out := RecipePlanExplanation{Version: p.Version, RecipeDigest: p.RecipeDigest, TranslationVersion: p.TranslationVersion, Outputs: make([]OutputPlanExplanation, 0, len(p.Outputs))}
	for _, output := range p.Outputs {
		e := OutputPlanExplanation{Name: output.Name, Root: output.RootResourceType, RowGrain: output.RowGrain, DynamicMap: make([]string, 0, len(output.DynamicMaps)), CatalogProjections: append([]string(nil), output.CatalogProjections...)}
		for _, field := range output.Root.Fields {
			e.Fields = append(e.Fields, explainExpression(field.Expr))
		}
		if output.Identity != nil {
			x := explainExpression(*output.Identity)
			e.Identity = &x
		}
		if output.RowExpansion != nil {
			e.Expansion = &ExpansionExplanation{SourcePath: output.RowExpansion.Source.SourcePath, OwnerOccurrenceID: output.RowExpansion.Owner.OccurrenceID, As: output.RowExpansion.ItemBinding, Ordinality: output.RowExpansion.Ordinality, EmptyPolicy: output.RowExpansion.EmptyPolicy}
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
