package semantic

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

var semanticBindingNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
	RowExpansion          *SemanticRowExpansion
	GroupRows             *SemanticGroupRows
	TableReshape          *SemanticTableReshape
	ExpansionIdentity     bool
	DynamicMaps           []SemanticDynamicMap
	CatalogProjections    []string
	ColumnTransformations []recipe.ColumnTransformation
	DerivedColumns        []recipe.DerivedColumn
	Construction          *recipe.Construction
	Collision             string
	Population            *SemanticPopulation
}

type SemanticGroupRows struct {
	RevisionID             string
	UnassignedMemberPolicy string
	AfterStepID            string
	RowValues              []recipe.GroupRowValuePolicy
}

type SemanticTableReshape struct {
	Kind         recipe.TableReshapeKind
	GroupedPivot *SemanticGroupedPivot
	Unpivot      *SemanticUnpivot
}

type SemanticGroupedPivot struct {
	ConstructionID         string
	GroupKeys              []string
	CategoryColumn         string
	ValueColumn            string
	Categories             []SemanticGroupedPivotCategory
	DuplicatePolicy        recipe.PivotDuplicatePolicy
	MissingCellPolicy      recipe.PivotMissingCellPolicy
	UnlistedCategoryPolicy recipe.PivotUnlistedCategoryPolicy
}

type SemanticGroupedPivotCategory struct {
	Key    recipe.TableScalar
	Output string
	Label  string
}

type SemanticUnpivot struct {
	ConstructionID string
	Inputs         []SemanticUnpivotInput
	KeyOutput      string
	KeyLabel       string
	ValueOutput    string
	ValueLabel     string
	NullRowPolicy  recipe.UnpivotNullRowPolicy
}

type SemanticUnpivotInput struct {
	Column string
	Key    recipe.TableScalar
}

type SemanticPopulation struct {
	SelectionRevisionID string
	MembershipDigest    string
	MemberCount         int64
	ResourceType        string
	Route               []SemanticPopulationRouteStep
}

type SemanticPopulationRouteStep struct {
	ResourceType     string
	Relationship     string
	StorageDirection string
}

type SemanticExpression struct {
	Expression expression.Expression
	Type       expression.Type
	SourcePath string
	Context    string
}

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
	ValueMode        recipe.ValueMode
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
	AllowUnknownKeys bool
	MaxColumns       int
	Discovered       bool
}

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
