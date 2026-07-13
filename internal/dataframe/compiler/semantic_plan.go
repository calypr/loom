package compiler

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/fhirschema"
	"github.com/calypr/loom/internal/authscope"
)

// SemanticPlan is the validated, backend-independent meaning of a dataframe
// request. It deliberately contains no AQL variable names, named-set choices,
// or optimizer decisions. Physical lowering is free to choose those details
// while preserving this plan's row and selection semantics.
//
// It is currently constructed from Builder so the existing GraphQL contract
// can remain stable while the compiler moves away from string-oriented lowering.
type SemanticPlan struct {
	Version           int
	Project           string
	DatasetGeneration string
	AuthResourcePaths []string
	// AuthScopeMode is copied from Builder so physical plans retain the
	// restricted-empty distinction after semantic lowering.
	AuthScopeMode authscope.ReadScopeMode
	RowIdentity   *RowIdentity
	Root          SemanticNode
}

// SemanticNode represents one FHIR resource reached at a stable alias. The
// root alias is always "root". Child nodes are relationship traversals from
// this node; a child selection is represented as an array, aggregate, pivot,
// or representative slice unless a later row-grain plan explicitly explodes
// it into rows.
type SemanticNode struct {
	Alias        string
	ResourceType string
	EdgeLabel    string
	MatchMode    TraversalMatchMode
	Fields       []SemanticField
	Filters      []TypedFilter
	Pivots       []SemanticPivot
	Aggregates   []SemanticAggregate
	Slices       []SemanticSlice
	Children     []SemanticNode
}

type SemanticField struct {
	Name      string
	FieldRef  string
	Selector  Selector
	Fallbacks []Selector
	ValueMode string
}

type SemanticPivot struct {
	Name           string
	FieldRef       string
	ColumnSelector Selector
	ValueSelector  Selector
	Columns        []string
	Family         string
}

type SemanticAggregate struct {
	Name            string
	Operation       string
	FieldRef        string
	Selector        *Selector
	PredicateField  string
	Predicate       *Selector
	PredicateEquals string
	ValueMode       string
}

type SemanticSlice struct {
	Name            string
	Limit           int
	PredicateField  string
	Predicate       *Selector
	PredicateEquals string
	Fields          []SemanticField
}

// BuildSemanticPlan parses the selector-bearing portions of Builder exactly
// once and preserves the user's requested traversal shape. Catalog and
// authorization validation remain the responsibility of Service.validateBuilder
// because they require a request context and an observed dataset.
func BuildSemanticPlan(builder Builder) (SemanticPlan, error) {
	if strings.TrimSpace(builder.Project) == "" {
		return SemanticPlan{}, fmt.Errorf("project is required")
	}
	if strings.TrimSpace(builder.RootResourceType) == "" {
		return SemanticPlan{}, fmt.Errorf("rootResourceType is required")
	}
	if !fhirschema.HasResource(builder.RootResourceType) {
		return SemanticPlan{}, fmt.Errorf("root resource type %q is not represented by the active generated FHIR schema", builder.RootResourceType)
	}

	root, err := semanticNodeFromBuilder("root", builder.RootResourceType, "", "", builder.Fields, builder.Filters, builder.Pivots, builder.Aggregates, builder.Slices, builder.Traversals)
	if err != nil {
		return SemanticPlan{}, err
	}
	plan := SemanticPlan{
		Version:           1,
		Project:           builder.Project,
		DatasetGeneration: normalizeDatasetGeneration(builder.DatasetGeneration),
		AuthResourcePaths: cloneStrings(builder.AuthResourcePaths),
		AuthScopeMode:     builder.AuthScopeMode,
		Root:              root,
	}
	grain := builder.RowGrain
	if grain == "" {
		var known bool
		grain, known = InferRowGrain(builder.RootResourceType)
		if !known {
			return SemanticPlan{}, fmt.Errorf("no row grain is available for root resource type %q", builder.RootResourceType)
		}
	}
	if err := ValidateRootGrain(builder.RootResourceType, grain); err != nil {
		return SemanticPlan{}, err
	}
	identity, ok := DefaultRowIdentity(grain)
	if !ok {
		return SemanticPlan{}, fmt.Errorf("invalid row grain %q", grain)
	}
	plan.RowIdentity = &identity
	if err := ValidateSemanticGraph(plan); err != nil {
		return SemanticPlan{}, err
	}
	if _, err := NormalizeSelectionPlan(plan); err != nil {
		return SemanticPlan{}, err
	}
	return plan, nil
}

func semanticNodeFromBuilder(alias, resourceType, edgeLabel string, matchMode TraversalMatchMode, fields []FieldSelect, filters []TypedFilter, pivots []PivotSelect, aggregates []AggregateSelect, slices []RepresentativeSlice, traversals []TraversalStep) (SemanticNode, error) {
	node := SemanticNode{
		Alias:        alias,
		ResourceType: resourceType,
		EdgeLabel:    edgeLabel,
		MatchMode:    matchMode,
		Fields:       make([]SemanticField, 0, len(fields)),
		Filters:      append([]TypedFilter(nil), filters...),
		Pivots:       make([]SemanticPivot, 0, len(pivots)),
		Aggregates:   make([]SemanticAggregate, 0, len(aggregates)),
		Slices:       make([]SemanticSlice, 0, len(slices)),
		Children:     make([]SemanticNode, 0, len(traversals)),
	}
	for _, filter := range node.Filters {
		if err := ValidateTypedFilterForResource(resourceType, filter); err != nil {
			return SemanticNode{}, fmt.Errorf("filter %q: %w", filter.FieldRef, err)
		}
	}

	seenFields := map[string]struct{}{}
	for _, field := range fields {
		if strings.TrimSpace(field.Name) == "" {
			return SemanticNode{}, fmt.Errorf("field selections require name and select")
		}
		if _, exists := seenFields[field.Name]; exists {
			return SemanticNode{}, fmt.Errorf("field name %q is duplicated", field.Name)
		}
		seenFields[field.Name] = struct{}{}
		selector, err := ParseSelector(field.Select)
		if err != nil {
			return SemanticNode{}, fmt.Errorf("field %q: %w", field.Name, err)
		}
		fallbacks := make([]Selector, 0, len(field.FallbackSelects))
		for _, fallback := range field.FallbackSelects {
			parsed, err := ParseSelector(fallback)
			if err != nil {
				return SemanticNode{}, fmt.Errorf("field %q fallback: %w", field.Name, err)
			}
			fallbacks = append(fallbacks, parsed)
		}
		node.Fields = append(node.Fields, SemanticField{
			Name:      field.Name,
			FieldRef:  field.FieldRef,
			Selector:  selector,
			Fallbacks: fallbacks,
			ValueMode: field.ValueMode,
		})
	}

	seenPivots := map[string]struct{}{}
	for _, pivot := range pivots {
		if strings.TrimSpace(pivot.Name) == "" {
			return SemanticNode{}, fmt.Errorf("pivot selections require name")
		}
		if _, exists := seenPivots[pivot.Name]; exists {
			return SemanticNode{}, fmt.Errorf("pivot name %q is duplicated", pivot.Name)
		}
		seenPivots[pivot.Name] = struct{}{}
		if len(pivot.Columns) == 0 {
			return SemanticNode{}, fmt.Errorf("pivot %q requires bounded columns resolved from the field catalog before compilation", pivot.Name)
		}
		column, err := ParseSelector(pivot.ColumnSelect)
		if err != nil {
			return SemanticNode{}, fmt.Errorf("pivot %q column selector: %w", pivot.Name, err)
		}
		value, err := ParseSelector(pivot.ValueSelect)
		if err != nil {
			return SemanticNode{}, fmt.Errorf("pivot %q value selector: %w", pivot.Name, err)
		}
		if err := validateSemanticSelector(resourceType, column); err != nil {
			return SemanticNode{}, fmt.Errorf("pivot %q column selector: %w", pivot.Name, err)
		}
		if err := validateSemanticSelector(resourceType, value); err != nil {
			return SemanticNode{}, fmt.Errorf("pivot %q value selector: %w", pivot.Name, err)
		}
		pivotSpec, err := fhirschema.ValidatePivotSelectors(resourceType, selectorSpecFromSelector(column), selectorSpecFromSelector(value))
		if err != nil {
			return SemanticNode{}, fmt.Errorf("pivot %q: %w", pivot.Name, err)
		}
		if pivot.PivotFamily != "" && pivot.PivotFamily != pivotSpec.Family {
			return SemanticNode{}, fmt.Errorf("pivot %q family %q conflicts with generated family %q", pivot.Name, pivot.PivotFamily, pivotSpec.Family)
		}
		node.Pivots = append(node.Pivots, SemanticPivot{
			Name:           pivot.Name,
			FieldRef:       pivot.FieldRef,
			ColumnSelector: column,
			ValueSelector:  value,
			Columns:        cloneStrings(pivot.Columns),
			Family:         pivotSpec.Family,
		})
	}

	seenAggregates := map[string]struct{}{}
	for _, aggregate := range aggregates {
		if strings.TrimSpace(aggregate.Name) == "" {
			return SemanticNode{}, fmt.Errorf("aggregate selections require name")
		}
		if _, exists := seenAggregates[aggregate.Name]; exists {
			return SemanticNode{}, fmt.Errorf("aggregate name %q is duplicated", aggregate.Name)
		}
		seenAggregates[aggregate.Name] = struct{}{}
		operation := strings.ToUpper(strings.TrimSpace(aggregate.Operation))
		if !isKnownAggregateOperation(operation) {
			return SemanticNode{}, fmt.Errorf("aggregate %q uses unsupported operation %q", aggregate.Name, aggregate.Operation)
		}
		if !isKnownValueMode(aggregate.ValueMode) {
			return SemanticNode{}, fmt.Errorf("aggregate %q uses unsupported value mode %q", aggregate.Name, aggregate.ValueMode)
		}
		semanticAggregate := SemanticAggregate{
			Name:            aggregate.Name,
			Operation:       operation,
			FieldRef:        aggregate.FieldRef,
			PredicateField:  aggregate.PredicateFieldRef,
			PredicateEquals: aggregate.PredicateEquals,
			ValueMode:       aggregate.ValueMode,
		}
		if strings.TrimSpace(aggregate.Select) != "" {
			selector, err := ParseSelector(aggregate.Select)
			if err != nil {
				return SemanticNode{}, fmt.Errorf("aggregate %q selector: %w", aggregate.Name, err)
			}
			if err := validateSemanticSelector(resourceType, selector); err != nil {
				return SemanticNode{}, fmt.Errorf("aggregate %q selector: %w", aggregate.Name, err)
			}
			semanticAggregate.Selector = &selector
		}
		if strings.TrimSpace(aggregate.PredicatePath) != "" {
			predicate, err := ParseSelector(aggregate.PredicatePath)
			if err != nil {
				return SemanticNode{}, fmt.Errorf("aggregate %q predicate: %w", aggregate.Name, err)
			}
			if err := validateSemanticSelector(resourceType, predicate); err != nil {
				return SemanticNode{}, fmt.Errorf("aggregate %q predicate: %w", aggregate.Name, err)
			}
			semanticAggregate.Predicate = &predicate
		}
		if aggregateOperationRequiresSelector(operation) && semanticAggregate.Selector == nil {
			return SemanticNode{}, fmt.Errorf("aggregate %q operation %s requires a selector", aggregate.Name, operation)
		}
		node.Aggregates = append(node.Aggregates, semanticAggregate)
	}

	seenSlices := map[string]struct{}{}
	for _, slice := range slices {
		if strings.TrimSpace(slice.Name) == "" {
			return SemanticNode{}, fmt.Errorf("representative slices require name")
		}
		if _, exists := seenSlices[slice.Name]; exists {
			return SemanticNode{}, fmt.Errorf("representative slice name %q is duplicated", slice.Name)
		}
		seenSlices[slice.Name] = struct{}{}
		if slice.Limit <= 0 {
			return SemanticNode{}, fmt.Errorf("representative slice %q requires positive limit", slice.Name)
		}
		semanticSlice := SemanticSlice{
			Name:            slice.Name,
			Limit:           slice.Limit,
			PredicateField:  slice.PredicateFieldRef,
			PredicateEquals: slice.PredicateEquals,
			Fields:          make([]SemanticField, 0, len(slice.Fields)),
		}
		if strings.TrimSpace(slice.PredicatePath) != "" {
			predicate, err := ParseSelector(slice.PredicatePath)
			if err != nil {
				return SemanticNode{}, fmt.Errorf("representative slice %q predicate: %w", slice.Name, err)
			}
			if err := validateSemanticSelector(resourceType, predicate); err != nil {
				return SemanticNode{}, fmt.Errorf("representative slice %q predicate: %w", slice.Name, err)
			}
			semanticSlice.Predicate = &predicate
		}
		seenSliceFields := map[string]struct{}{}
		for _, field := range slice.Fields {
			if strings.TrimSpace(field.Name) == "" {
				return SemanticNode{}, fmt.Errorf("representative slice %q requires field name", slice.Name)
			}
			if _, exists := seenSliceFields[field.Name]; exists {
				return SemanticNode{}, fmt.Errorf("representative slice %q field name %q is duplicated", slice.Name, field.Name)
			}
			seenSliceFields[field.Name] = struct{}{}
			selector, err := ParseSelector(field.Select)
			if err != nil {
				return SemanticNode{}, fmt.Errorf("representative slice %q field %q: %w", slice.Name, field.Name, err)
			}
			if err := validateSemanticSelector(resourceType, selector); err != nil {
				return SemanticNode{}, fmt.Errorf("representative slice %q field %q: %w", slice.Name, field.Name, err)
			}
			fallbacks := make([]Selector, 0, len(field.FallbackSelects))
			for _, fallback := range field.FallbackSelects {
				parsed, err := ParseSelector(fallback)
				if err != nil {
					return SemanticNode{}, fmt.Errorf("representative slice %q field %q fallback: %w", slice.Name, field.Name, err)
				}
				if err := validateSemanticSelector(resourceType, parsed); err != nil {
					return SemanticNode{}, fmt.Errorf("representative slice %q field %q fallback: %w", slice.Name, field.Name, err)
				}
				fallbacks = append(fallbacks, parsed)
			}
			if !isKnownValueMode(field.ValueMode) {
				return SemanticNode{}, fmt.Errorf("representative slice %q field %q uses unsupported value mode %q", slice.Name, field.Name, field.ValueMode)
			}
			semanticSlice.Fields = append(semanticSlice.Fields, SemanticField{
				Name:      field.Name,
				FieldRef:  field.FieldRef,
				Selector:  selector,
				Fallbacks: fallbacks,
				ValueMode: field.ValueMode,
			})
		}
		node.Slices = append(node.Slices, semanticSlice)
	}

	for _, traversal := range traversals {
		child, err := semanticNodeFromBuilder(traversal.Alias, traversal.ToResourceType, traversal.Label, traversal.MatchMode, traversal.Fields, traversal.Filters, traversal.Pivots, traversal.Aggregates, traversal.Slices, traversal.Traversals)
		if err != nil {
			return SemanticNode{}, err
		}
		node.Children = append(node.Children, child)
	}
	return node, nil
}

func validateSemanticSelector(resourceType string, selector Selector) error {
	_, _, err := selectorCardinality(resourceType, selector)
	return err
}

func aggregateOperationRequiresSelector(operation string) bool {
	switch operation {
	case "COUNT_DISTINCT", "DISTINCT_VALUES", "MIN", "MAX":
		return true
	default:
		return false
	}
}

func isKnownAggregateOperation(operation string) bool {
	switch strings.ToUpper(strings.TrimSpace(operation)) {
	case "COUNT", "COUNT_DISTINCT", "EXISTS", "DISTINCT_VALUES", "MIN", "MAX":
		return true
	default:
		return false
	}
}

// Explain returns a compact stable summary suitable for diagnostics and tests.
// It intentionally does not expose physical AQL decisions.
func (p SemanticPlan) Explain() SemanticPlanExplanation {
	explanation := SemanticPlanExplanation{
		Version:           p.Version,
		RootResourceType:  p.Root.ResourceType,
		DatasetGeneration: p.DatasetGeneration,
		RowIdentity:       cloneRowIdentity(p.RowIdentity),
		Nodes:             make([]SemanticNodeExplanation, 0),
	}
	var walk func(SemanticNode, string)
	walk = func(node SemanticNode, parentAlias string) {
		explanation.Nodes = append(explanation.Nodes, SemanticNodeExplanation{
			Alias:          node.Alias,
			ParentAlias:    parentAlias,
			ResourceType:   node.ResourceType,
			EdgeLabel:      node.EdgeLabel,
			MatchMode:      node.MatchMode,
			FieldCount:     len(node.Fields),
			PivotCount:     len(node.Pivots),
			AggregateCount: len(node.Aggregates),
			SliceCount:     len(node.Slices),
		})
		for _, child := range node.Children {
			walk(child, node.Alias)
		}
	}
	walk(p.Root, "")
	return explanation
}

type SemanticPlanExplanation struct {
	Version           int
	RootResourceType  string
	DatasetGeneration string
	RowIdentity       *RowIdentity
	Nodes             []SemanticNodeExplanation
}

func cloneRowIdentity(identity *RowIdentity) *RowIdentity {
	if identity == nil {
		return nil
	}
	copy := *identity
	copy.Fields = cloneStrings(identity.Fields)
	return &copy
}

type SemanticNodeExplanation struct {
	Alias          string
	ParentAlias    string
	ResourceType   string
	EdgeLabel      string
	MatchMode      TraversalMatchMode
	FieldCount     int
	PivotCount     int
	AggregateCount int
	SliceCount     int
}
