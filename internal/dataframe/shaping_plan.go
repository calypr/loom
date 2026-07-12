package dataframe

import (
	"fmt"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

// ShapingDefaults makes policies absent from the legacy Builder contract
// explicit at the boundary to the semantic compiler.
type ShapingDefaults struct {
	NullPolicy             NullPolicy
	PivotDuplicatePolicy   DuplicatePolicy
	SparsePivotPolicy      SparsePivotPolicy
	MaxDiscoveredPivotCols int
}

func (d ShapingDefaults) Validate() error {
	if err := d.NullPolicy.Validate(); err != nil {
		return err
	}
	if err := d.PivotDuplicatePolicy.Validate(); err != nil {
		return err
	}
	if err := d.SparsePivotPolicy.Validate(); err != nil {
		return err
	}
	if d.MaxDiscoveredPivotCols <= 0 {
		return fmt.Errorf("maximum discovered pivot columns must be positive")
	}
	return nil
}

// ShapingSource records the semantic node and, for non-root nodes, the
// generated FHIR relationship that supplies aggregate or pivot values.
type ShapingSource struct {
	ParentAlias  string
	TargetAlias  string
	ResourceType string
	Relationship *fhirschema.CompilerTraversal
}

type AggregateSemanticSpec struct {
	Alias           string
	Source          ShapingSource
	Config          AggregateConfig
	Selector        *Selector
	Predicate       *Selector
	PredicateEquals string
}

type PivotSemanticSpec struct {
	Alias          string
	Source         ShapingSource
	Config         PivotConfig
	ColumnSelector Selector
	ValueSelector  Selector
	Family         string
}

type ShapingPlan struct {
	Aggregates []AggregateSemanticSpec
	Pivots     []PivotSemanticSpec
}

// NormalizeShapingPlan validates shaping requests and returns deterministic,
// alias-sorted specs. It contains no AQL expressions or physical-plan choices.
func NormalizeShapingPlan(plan SemanticPlan, defaults ShapingDefaults) (ShapingPlan, error) {
	if err := defaults.Validate(); err != nil {
		return ShapingPlan{}, fmt.Errorf("shaping defaults: %w", err)
	}
	out := ShapingPlan{
		Aggregates: make([]AggregateSemanticSpec, 0),
		Pivots:     make([]PivotSemanticSpec, 0),
	}
	aliases := map[string]struct{}{}
	var walk func(SemanticNode, string, string) error
	walk = func(node SemanticNode, parentAlias, parentType string) error {
		if strings.TrimSpace(node.Alias) == "" {
			return fmt.Errorf("semantic node for %s has no alias", node.ResourceType)
		}
		if _, exists := aliases[node.Alias]; exists {
			return fmt.Errorf("semantic node alias %q is duplicated", node.Alias)
		}
		aliases[node.Alias] = struct{}{}
		source := ShapingSource{ParentAlias: parentAlias, TargetAlias: node.Alias, ResourceType: node.ResourceType}
		if parentAlias != "" {
			relationship, found, err := fhirschema.ResolveCompilerTraversal(parentType, node.EdgeLabel, node.ResourceType)
			if err != nil {
				return fmt.Errorf("relationship %s -[%s]-> %s: %w", parentType, node.EdgeLabel, node.ResourceType, err)
			}
			if !found {
				return fmt.Errorf("unknown relationship %s -[%s]-> %s", parentType, node.EdgeLabel, node.ResourceType)
			}
			source.Relationship = &relationship
		}

		for index, aggregate := range node.Aggregates {
			spec, err := normalizeAggregateSpec(node.Alias, index, source, aggregate, defaults.NullPolicy)
			if err != nil {
				return err
			}
			out.Aggregates = append(out.Aggregates, spec)
		}
		for index, pivot := range node.Pivots {
			spec, err := normalizePivotSpec(node.Alias, index, source, pivot, defaults)
			if err != nil {
				return err
			}
			out.Pivots = append(out.Pivots, spec)
		}
		for _, child := range node.Children {
			if err := walk(child, node.Alias, node.ResourceType); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(plan.Root, "", ""); err != nil {
		return ShapingPlan{}, err
	}
	sort.Slice(out.Aggregates, func(i, j int) bool { return out.Aggregates[i].Alias < out.Aggregates[j].Alias })
	sort.Slice(out.Pivots, func(i, j int) bool { return out.Pivots[i].Alias < out.Pivots[j].Alias })
	return out, nil
}

func normalizeAggregateSpec(nodeAlias string, index int, source ShapingSource, in SemanticAggregate, nulls NullPolicy) (AggregateSemanticSpec, error) {
	name := stableShapeName(in.Name, "aggregate", index)
	function, err := normalizeAggregateFunction(in.Operation)
	if err != nil {
		return AggregateSemanticSpec{}, fmt.Errorf("aggregate %q: %w", name, err)
	}
	input := ""
	if in.Selector != nil {
		input = in.Selector.CanonicalPath()
	} else if in.Predicate != nil {
		input = in.Predicate.CanonicalPath()
	} else if function == AggregateExists {
		input = "node:" + source.TargetAlias
	}
	config := AggregateConfig{Function: function, Input: input, NullPolicy: nulls}
	if err := config.Validate(); err != nil {
		return AggregateSemanticSpec{}, fmt.Errorf("aggregate %q: %w", name, err)
	}
	return AggregateSemanticSpec{
		Alias: nodeAlias + "." + name, Source: source, Config: config,
		Selector: in.Selector, Predicate: in.Predicate, PredicateEquals: in.PredicateEquals,
	}, nil
}

func normalizePivotSpec(nodeAlias string, index int, source ShapingSource, in SemanticPivot, defaults ShapingDefaults) (PivotSemanticSpec, error) {
	name := stableShapeName(in.Name, "pivot", index)
	columns := cloneStrings(in.Columns)
	cardinality := PivotRequestedColumns
	maxColumns := len(columns)
	if len(columns) == 0 {
		cardinality = PivotBoundedDiscovery
		maxColumns = defaults.MaxDiscoveredPivotCols
	}
	config := PivotConfig{
		Key: in.ColumnSelector.CanonicalPath(), Value: in.ValueSelector.CanonicalPath(),
		DuplicatePolicy: defaults.PivotDuplicatePolicy, NullPolicy: defaults.NullPolicy,
		CardinalityPolicy: cardinality, SparsePolicy: defaults.SparsePivotPolicy,
		RequestedColumns: columns, MaxColumns: maxColumns,
	}
	if err := config.Validate(); err != nil {
		return PivotSemanticSpec{}, fmt.Errorf("pivot %q: %w", name, err)
	}
	return PivotSemanticSpec{
		Alias: nodeAlias + "." + name, Source: source, Config: config,
		ColumnSelector: in.ColumnSelector, ValueSelector: in.ValueSelector, Family: in.Family,
	}, nil
}

func normalizeAggregateFunction(operation string) (AggregateFunction, error) {
	switch strings.ToUpper(strings.TrimSpace(operation)) {
	case "COUNT":
		return AggregateCount, nil
	case "COUNT_DISTINCT":
		return AggregateCountDistinct, nil
	case "EXISTS":
		return AggregateExists, nil
	case "DISTINCT_VALUES":
		return AggregateDistinctValues, nil
	case "MIN":
		return AggregateMin, nil
	case "MAX":
		return AggregateMax, nil
	default:
		return "", fmt.Errorf("unsupported operation %q", operation)
	}
}

func stableShapeName(name, kind string, index int) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return fmt.Sprintf("%s_%d", kind, index+1)
}
