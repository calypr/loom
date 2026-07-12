package dataframe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

// TraversalMatchMode controls whether a relationship contributes values to a
// dataframe row only, or must exist for that root row to be included at all.
//
// OPTIONAL is the legacy behavior: a missing child yields an empty child
// projection but does not remove the root row. REQUIRED is intentionally
// opt-in and lowers to a root-scoped semi-join. The empty value is OPTIONAL so
// existing Builder callers retain their current behavior.
type TraversalMatchMode string

const (
	TraversalMatchOptional TraversalMatchMode = "OPTIONAL"
	TraversalMatchRequired TraversalMatchMode = "REQUIRED"
)

func (m TraversalMatchMode) Validate() error {
	switch m {
	case "", TraversalMatchOptional, TraversalMatchRequired:
		return nil
	default:
		return fmt.Errorf("unsupported traversal match mode %q", m)
	}
}

func (m TraversalMatchMode) required() bool {
	return m == TraversalMatchRequired
}

// RequiredTraversalMatch is the lowered representation of one existential
// relationship route. It is compiler-owned, not a GraphQL input: Lower builds
// it from TraversalStep.MatchMode and Compile renders it before root sorting
// and limiting. Every step in Steps must match for the root row to survive.
type RequiredTraversalMatch struct {
	Steps []TraversalMatchStep
}

// TraversalMatchStep retains only the information necessary to evaluate a
// relationship existence predicate. Alias and selection shape do not affect
// the semi-join, while typed filters do.
type TraversalMatchStep struct {
	Alias          string
	Label          string
	ToResourceType string
	Filters        []TypedFilter
}

func requiredTraversalMatches(root logicalNode) ([]RequiredTraversalMatch, error) {
	matches := make([]RequiredTraversalMatch, 0)
	var walk func(parent logicalNode, route []TraversalMatchStep) error
	walk = func(parent logicalNode, route []TraversalMatchStep) error {
		for _, child := range parent.Children {
			if err := child.MatchMode.Validate(); err != nil {
				return fmt.Errorf("traversal %s -> %s (%s): %w", parent.ResourceType, child.ResourceType, child.Label, err)
			}
			if _, err := resolveStorageRoute(parent.ResourceType, child.Label, child.ResourceType); err != nil {
				return fmt.Errorf("traversal %s -> %s (%s): %w", parent.ResourceType, child.ResourceType, child.Label, err)
			}

			next := appendTraversalMatchStep(route, TraversalMatchStep{
				Alias:          child.Alias,
				Label:          child.Label,
				ToResourceType: child.ResourceType,
				Filters:        append([]TypedFilter(nil), child.Filters...),
			})
			if child.MatchMode.required() {
				matches = append(matches, RequiredTraversalMatch{Steps: cloneTraversalMatchSteps(next)})
			}
			if err := walk(child, next); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, nil); err != nil {
		return nil, err
	}
	return matches, nil
}

func validateRequiredTraversalMatches(rootResourceType string, matches []RequiredTraversalMatch) error {
	for matchIndex, match := range matches {
		if len(match.Steps) == 0 {
			return fmt.Errorf("required traversal match %d has no route steps", matchIndex)
		}
		fromResourceType := rootResourceType
		for stepIndex, step := range match.Steps {
			if strings.TrimSpace(step.Label) == "" {
				return fmt.Errorf("required traversal match %d step %d has no edge label", matchIndex, stepIndex)
			}
			if !fhirschema.HasResource(step.ToResourceType) {
				return fmt.Errorf("required traversal match %d step %d target resource type %q is not represented by the active generated FHIR schema", matchIndex, stepIndex, step.ToResourceType)
			}
			if _, err := resolveStorageRoute(fromResourceType, step.Label, step.ToResourceType); err != nil {
				return fmt.Errorf("required traversal match %d step %d %s -> %s (%s): %w", matchIndex, stepIndex, fromResourceType, step.ToResourceType, step.Label, err)
			}
			for _, filter := range step.Filters {
				if err := ValidateTypedFilterForResource(step.ToResourceType, filter); err != nil {
					return fmt.Errorf("required traversal match %d step %d filter %q: %w", matchIndex, stepIndex, filter.FieldRef, err)
				}
			}
			fromResourceType = step.ToResourceType
		}
	}
	return nil
}

func appendTraversalMatchStep(route []TraversalMatchStep, step TraversalMatchStep) []TraversalMatchStep {
	next := make([]TraversalMatchStep, len(route), len(route)+1)
	copy(next, route)
	next = append(next, step)
	return next
}

func cloneTraversalMatchSteps(in []TraversalMatchStep) []TraversalMatchStep {
	if len(in) == 0 {
		return nil
	}
	out := make([]TraversalMatchStep, len(in))
	for i, step := range in {
		out[i] = step
		out[i].Filters = append([]TypedFilter(nil), step.Filters...)
	}
	return out
}
