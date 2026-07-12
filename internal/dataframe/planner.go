package dataframe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/authscope"
)

type PlanHint struct {
	Mode                    string
	Profile                 string
	NamedSetCount           int
	ClassifiedFileSummaries bool
	StudyLookup             bool
	// RowIdentity is copied from the semantic plan so the physical renderer,
	// compiler explain output, and downstream exporters agree on what one row
	// represents. It is not an optimizer hint despite living here temporarily
	// during the Builder-to-physical-plan migration.
	RowIdentity *RowIdentity
	// AppliedRules records stable optimizer rule identifiers that changed the
	// physical shape. It is carried into CompiledQuery for explain, golden, and
	// performance comparisons.
	AppliedRules []string
}

type logicalRequest struct {
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	AuthScopeMode     authscope.ReadScopeMode
	Root              logicalNode
}

type logicalNode struct {
	ResourceType string
	Alias        string
	Label        string
	MatchMode    TraversalMatchMode
	Fields       []FieldSelect
	Filters      []TypedFilter
	Pivots       []PivotSelect
	Aggregates   []AggregateSelect
	Slices       []RepresentativeSlice
	Children     []logicalNode
}

type loweringContext struct {
	request                    logicalRequest
	builder                    Builder
	setsByName                 map[string]struct{}
	modes                      map[string]string
	genericSetsBySignature     map[string]string
	genericFilterSetsBySig     map[string]string
	genericAliasesBySetName    map[string]string
	genericTraversalShareCount int
}

func buildLogicalRequest(builder Builder) logicalRequest {
	return logicalRequest{
		Project:           builder.Project,
		DatasetGeneration: normalizeDatasetGeneration(builder.DatasetGeneration),
		AuthResourcePaths: cloneStrings(builder.AuthResourcePaths),
		AuthScopeMode:     builder.AuthScopeMode,
		Root: logicalNode{
			ResourceType: builder.RootResourceType,
			Alias:        "root",
			Fields:       append([]FieldSelect(nil), builder.Fields...),
			Filters:      append([]TypedFilter(nil), builder.Filters...),
			Pivots:       append([]PivotSelect(nil), builder.Pivots...),
			Aggregates:   append([]AggregateSelect(nil), builder.Aggregates...),
			Slices:       append([]RepresentativeSlice(nil), builder.Slices...),
			Children:     logicalNodesFromTraversal(builder.Traversals),
		},
	}
}

func logicalNodesFromTraversal(in []TraversalStep) []logicalNode {
	if len(in) == 0 {
		return []logicalNode{}
	}
	out := make([]logicalNode, 0, len(in))
	for _, step := range in {
		out = append(out, logicalNode{
			ResourceType: step.ToResourceType,
			Alias:        step.Alias,
			Label:        step.Label,
			MatchMode:    step.MatchMode,
			Fields:       append([]FieldSelect(nil), step.Fields...),
			Filters:      append([]TypedFilter(nil), step.Filters...),
			Pivots:       append([]PivotSelect(nil), step.Pivots...),
			Aggregates:   append([]AggregateSelect(nil), step.Aggregates...),
			Slices:       append([]RepresentativeSlice(nil), step.Slices...),
			Children:     logicalNodesFromTraversal(step.Traversals),
		})
	}
	return out
}

func lowerGraphQLBuilder(builder Builder) (Builder, error) {
	return lowerGenericGraphQLBuilder(builder, buildLogicalRequest(builder))
}

func requestHasTypedFilters(node logicalNode) bool {
	if len(node.Filters) > 0 {
		return true
	}
	for _, child := range node.Children {
		if requestHasTypedFilters(child) {
			return true
		}
	}
	return false
}

func requestHasRequiredTraversalMatch(node logicalNode) bool {
	for _, child := range node.Children {
		if child.MatchMode.required() || requestHasRequiredTraversalMatch(child) {
			return true
		}
	}
	return false
}

// Lower converts the public dataframe request into a validated physical-plan
// builder. It is the compiler boundary used by conformance tooling and by the
// service layer; callers should not construct named sets directly.
func Lower(builder Builder) (Builder, error) {
	semantic, err := BuildSemanticPlan(builder)
	if err != nil {
		return Builder{}, err
	}
	return lowerSemanticBuilder(builder, semantic)
}

// lowerSemanticBuilder performs the representation-specific half of Lower
// after its caller has already built the semantic request. Keeping that seam
// explicit lets CompileRequest, the service path, and compiler explain all
// choose the typed physical renderer without parsing selectors twice.
func lowerSemanticBuilder(builder Builder, semantic SemanticPlan) (Builder, error) {
	planned, err := lowerGraphQLBuilder(builder)
	if err != nil {
		return Builder{}, err
	}
	planned.RowGrain = semantic.RowIdentity.Grain
	if planned.PlanHint == nil {
		return Builder{}, fmt.Errorf("compiler lowering produced no physical plan hint")
	}
	planned.PlanHint.RowIdentity = cloneRowIdentity(semantic.RowIdentity)
	return planned, nil
}

func unsupportedLoweringError(msg string) error {
	return fmt.Errorf("unsupported dataframe query shape: %s", msg)
}

// lowerNodeSelections implements the canonical selection contract for every
// FHIR root and traversal. AUTO on a repeated relationship is deterministic
// FIRST; callers that need arrays must request ALL or DISTINCT explicitly.
func (ctx *loweringContext) lowerNodeSelections(node logicalNode, sourceSet string) {
	for _, field := range node.Fields {
		selectExpr := field.Select
		fallbacks := append([]string(nil), field.FallbackSelects...)
		operation := DerivedOpUnique
		switch normalizeValueMode(field.ValueMode) {
		case "FIRST":
			operation = DerivedOpFirstNonNull
		case "ALL":
			operation = DerivedOpAll
		case "DISTINCT":
			operation = DerivedOpUnique
		case "AUTO":
			operation = DerivedOpFirstNonNull
		}
		ctx.builder.DerivedFields = append(ctx.builder.DerivedFields, DerivedField{
			Name:            sanitizeColumnName(node.Alias + "__" + field.Name),
			Source:          sourceSet,
			Operation:       operation,
			Select:          selectExpr,
			FallbackSelects: fallbacks,
		})
	}
	for _, pivot := range node.Pivots {
		keySelect := pivot.ColumnSelect
		valueSelect := pivot.ValueSelect
		ctx.builder.DerivedFields = append(ctx.builder.DerivedFields, DerivedField{
			Name:             sanitizeColumnName(node.Alias + "__" + pivot.Name),
			Source:           sourceSet,
			Operation:        DerivedOpPivot,
			PivotFamily:      pivot.PivotFamily,
			PivotKeySelect:   keySelect,
			PivotValueSelect: valueSelect,
			PivotColumns:     cloneStrings(pivot.Columns),
		})
	}
	for _, agg := range node.Aggregates {
		field := DerivedField{
			Name:            sanitizeColumnName(node.Alias + "__" + agg.Name),
			Source:          sourceSet,
			Select:          agg.Select,
			PredicatePath:   agg.PredicatePath,
			PredicateEquals: agg.PredicateEquals,
		}
		switch strings.ToUpper(strings.TrimSpace(agg.Operation)) {
		case "COUNT":
			if strings.TrimSpace(field.PredicatePath) != "" || strings.TrimSpace(field.Predicate) != "" {
				field.Operation = DerivedOpCountWhere
			} else {
				field.Operation = DerivedOpCount
			}
		case "COUNT_DISTINCT":
			field.Operation = DerivedOpCountDistinct
		case "EXISTS":
			field.Operation = DerivedOpAny
		case "DISTINCT_VALUES":
			field.Operation = DerivedOpUnique
		case "MIN":
			field.Operation = DerivedOpMin
		case "MAX":
			field.Operation = DerivedOpMax
		default:
			continue
		}
		ctx.builder.DerivedFields = append(ctx.builder.DerivedFields, field)
	}
	for _, slice := range node.Slices {
		projected := RepresentativeSlice{
			Name:            sanitizeColumnName(node.Alias + "__" + slice.Name),
			SourceSet:       sourceSet,
			PredicatePath:   slice.PredicatePath,
			PredicateEquals: slice.PredicateEquals,
			Limit:           slice.Limit,
			Fields:          append([]FieldSelect(nil), slice.Fields...),
		}
		ctx.builder.RepresentativeSlices = append(ctx.builder.RepresentativeSlices, projected)
	}
}

func normalizeValueMode(mode string) string {
	switch strings.ToUpper(strings.TrimSpace(mode)) {
	case "FIRST", "ALL", "DISTINCT":
		return strings.ToUpper(strings.TrimSpace(mode))
	default:
		return "AUTO"
	}
}

func (ctx *loweringContext) ensureSet(set NamedSet, mode string) string {
	if _, ok := ctx.setsByName[set.Name]; ok {
		return set.Name
	}
	ctx.builder.Sets = append(ctx.builder.Sets, set)
	ctx.setsByName[set.Name] = struct{}{}
	ctx.modes[set.Name] = mode
	return set.Name
}
