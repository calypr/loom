package dataframe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

const (
	SetKindTraverse                  = "TRAVERSE"
	SetKindFilter                    = "FILTER"
	SetKindUnion                     = "UNION"
	SetKindClassifyDocumentReference = "CLASSIFY_DOCUMENT_REFERENCE"
	SetKindLookupStudy               = "LOOKUP_STUDY"

	DerivedOpConst         = "CONST"
	DerivedOpRootField     = "ROOT_FIELD"
	DerivedOpFirstNonNull  = "FIRST_NON_NULL"
	DerivedOpAll           = "ALL"
	DerivedOpCount         = "COUNT"
	DerivedOpCountDistinct = "COUNT_DISTINCT"
	DerivedOpCountWhere    = "COUNT_WHERE"
	DerivedOpAny           = "ANY"
	DerivedOpMin           = "MIN"
	DerivedOpMax           = "MAX"
	DerivedOpUnique        = "UNIQUE"
	DerivedOpPivot         = "PIVOT"
)

type NamedSet struct {
	Name    string
	Kind    string
	Source  string
	Sources []string
	// Direction is the physical Arango traversal direction for a TRAVERSE set.
	// Empty preserves the current optimized-plan default (INBOUND). Generic
	// lowering uses the proven INBOUND catalog contract until a direction-
	// selection optimization has both catalog and edge-layout evidence.
	Direction      string
	Label          string
	ToResourceType string
	// AllTargetTypes is only used by generic sibling-prefix sharing. The
	// ToResourceType above remains a generated-route validation anchor, while
	// the physical traversal deliberately collects every target type for the
	// shared edge label. Typed FILTER subsets consume that base set.
	AllTargetTypes    bool
	MatchResourceType string
	Filters           []TypedFilter
	Unique            bool
	SortField         string
}

type DerivedField struct {
	Name             string
	Source           string
	Operation        string
	Select           string
	FallbackSelects  []string
	Predicate        string
	PredicatePath    string
	PredicateEquals  string
	PivotColumns     []string
	PivotFamily      string
	PivotKeySelect   string
	PivotValueSelect string
	ConstValue       any
}

type RepresentativeSlice struct {
	Name              string
	SourceSet         string
	Predicate         string
	PredicateFieldRef string
	PredicatePath     string
	PredicateEquals   string
	Limit             int
	Fields            []FieldSelect
}

func usesLoweredBuilder(builder Builder) bool {
	if builder.PlanHint != nil && strings.TrimSpace(builder.PlanHint.Mode) != "" {
		return true
	}
	if len(builder.Sets) > 0 || len(builder.DerivedFields) > 0 || len(builder.RepresentativeSlices) > 0 {
		return true
	}
	for _, step := range builder.Traversals {
		if len(step.Sets) > 0 || len(step.DerivedFields) > 0 || len(step.RepresentativeSlices) > 0 {
			return true
		}
	}
	return false
}

func validateLoweredBuilder(builder Builder) error {
	if !fhirschema.HasResource(builder.RootResourceType) {
		return fmt.Errorf("root resource type %q is not represented by the active generated FHIR schema", builder.RootResourceType)
	}
	if builder.RowGrain != "" {
		if err := ValidateRootGrain(builder.RootResourceType, builder.RowGrain); err != nil {
			return err
		}
	}
	if builder.PlanHint != nil && builder.PlanHint.RowIdentity != nil {
		if err := builder.PlanHint.RowIdentity.Validate(); err != nil {
			return fmt.Errorf("plan row identity: %w", err)
		}
		if builder.RowGrain != "" && builder.PlanHint.RowIdentity.Grain != builder.RowGrain {
			return fmt.Errorf("plan row identity grain %q does not match builder row grain %q", builder.PlanHint.RowIdentity.Grain, builder.RowGrain)
		}
	}
	for _, filter := range builder.Filters {
		if err := ValidateTypedFilterForResource(builder.RootResourceType, filter); err != nil {
			return fmt.Errorf("root filter %q: %w", filter.FieldRef, err)
		}
	}
	if err := validateTraversalFilterSemantics(builder.RootResourceType, builder.Traversals); err != nil {
		return err
	}
	if err := validateRequiredTraversalMatches(builder.RootResourceType, builder.RequiredTraversalMatches); err != nil {
		return err
	}
	seenSets := map[string]struct{}{}
	for _, set := range builder.Sets {
		if strings.TrimSpace(set.Name) == "" {
			return fmt.Errorf("set name is required")
		}
		if _, ok := seenSets[set.Name]; ok {
			return fmt.Errorf("set %q is duplicated", set.Name)
		}
		seenSets[set.Name] = struct{}{}
		if set.SortField != "" && !isSafeAQLFieldIdentifier(set.SortField) {
			return fmt.Errorf("set %q uses unsafe sort field %q", set.Name, set.SortField)
		}
		switch strings.ToUpper(strings.TrimSpace(set.Kind)) {
		case SetKindTraverse:
			if set.Label == "" {
				return fmt.Errorf("set %q requires label", set.Name)
			}
			if set.AllTargetTypes {
				if builder.PlanHint == nil || builder.PlanHint.Profile != "generic_fhir_graph" {
					return fmt.Errorf("set %q may collect all target types only in the generic FHIR graph profile", set.Name)
				}
				if strings.TrimSpace(set.ToResourceType) == "" {
					return fmt.Errorf("set %q collecting all target types requires a generated-route validation anchor", set.Name)
				}
				if len(set.Filters) != 0 {
					return fmt.Errorf("set %q collecting all target types must apply filters in typed subsets", set.Name)
				}
			}
			switch strings.ToUpper(strings.TrimSpace(set.Direction)) {
			case "", "INBOUND", "OUTBOUND", "ANY":
			default:
				return fmt.Errorf("set %q uses unsupported traversal direction %q", set.Name, set.Direction)
			}
			for _, filter := range set.Filters {
				if err := ValidateTypedFilterForResource(set.ToResourceType, filter); err != nil {
					return fmt.Errorf("set %q filter %q: %w", set.Name, filter.FieldRef, err)
				}
			}
		case SetKindFilter:
			if set.Source == "" {
				return fmt.Errorf("set %q requires source", set.Name)
			}
			if strings.TrimSpace(set.MatchResourceType) == "" && len(set.Filters) > 0 {
				return fmt.Errorf("set %q requires match resource type for typed filters", set.Name)
			}
			if set.MatchResourceType != "" {
				if !fhirschema.HasResource(set.MatchResourceType) {
					return fmt.Errorf("set %q match resource type %q is not represented by the active generated FHIR schema", set.Name, set.MatchResourceType)
				}
				for _, filter := range set.Filters {
					if err := ValidateTypedFilterForResource(set.MatchResourceType, filter); err != nil {
						return fmt.Errorf("set %q filter %q: %w", set.Name, filter.FieldRef, err)
					}
				}
			}
		case SetKindUnion:
			if len(set.Sources) == 0 {
				return fmt.Errorf("set %q requires sources", set.Name)
			}
		case SetKindClassifyDocumentReference:
			if set.Source == "" {
				return fmt.Errorf("set %q requires source", set.Name)
			}
		case SetKindLookupStudy:
			if set.Source == "" {
				return fmt.Errorf("set %q requires source research subject set", set.Name)
			}
		default:
			return fmt.Errorf("set %q uses unsupported kind %q", set.Name, set.Kind)
		}
	}
	if err := validateGenericLoweredStorageRoutes(builder); err != nil {
		return err
	}

	seenDerived := map[string]struct{}{}
	for _, field := range builder.DerivedFields {
		if field.Name == "" {
			return fmt.Errorf("derived field name is required")
		}
		if _, ok := seenDerived[field.Name]; ok {
			return fmt.Errorf("derived field %q is duplicated", field.Name)
		}
		seenDerived[field.Name] = struct{}{}
		switch strings.ToUpper(strings.TrimSpace(field.Operation)) {
		case DerivedOpConst:
			if field.ConstValue == nil {
				return fmt.Errorf("derived field %q requires const value", field.Name)
			}
		case DerivedOpRootField:
			if field.Select == "" {
				return fmt.Errorf("derived field %q requires select", field.Name)
			}
		case DerivedOpFirstNonNull, DerivedOpAll, DerivedOpUnique:
			if field.Source == "" {
				return fmt.Errorf("derived field %q requires source", field.Name)
			}
			if field.Select == "" {
				return fmt.Errorf("derived field %q requires select", field.Name)
			}
			if _, err := ParseSelector(field.Select); err != nil {
				return fmt.Errorf("derived field %q invalid select: %w", field.Name, err)
			}
			for _, sel := range field.FallbackSelects {
				if _, err := ParseSelector(sel); err != nil {
					return fmt.Errorf("derived field %q invalid fallback select: %w", field.Name, err)
				}
			}
		case DerivedOpPivot:
			if field.Source == "" {
				return fmt.Errorf("derived field %q requires source", field.Name)
			}
			if strings.TrimSpace(field.PivotKeySelect) == "" || strings.TrimSpace(field.PivotValueSelect) == "" {
				return fmt.Errorf("derived field %q requires pivot key/value selectors", field.Name)
			}
			if _, err := ParseSelector(field.PivotKeySelect); err != nil {
				return fmt.Errorf("derived field %q invalid pivot key selector: %w", field.Name, err)
			}
			if _, err := ParseSelector(field.PivotValueSelect); err != nil {
				return fmt.Errorf("derived field %q invalid pivot value selector: %w", field.Name, err)
			}
			if len(field.PivotColumns) == 0 {
				return fmt.Errorf("derived field %q requires bounded pivot columns", field.Name)
			}
		case DerivedOpCount:
			if field.Source == "" {
				return fmt.Errorf("derived field %q requires source", field.Name)
			}
		case DerivedOpCountDistinct, DerivedOpMin, DerivedOpMax:
			if field.Source == "" || strings.TrimSpace(field.Select) == "" {
				return fmt.Errorf("derived field %q requires source and select", field.Name)
			}
			if _, err := ParseSelector(field.Select); err != nil {
				return fmt.Errorf("derived field %q invalid select: %w", field.Name, err)
			}
		case DerivedOpCountWhere:
			if field.Source == "" {
				return fmt.Errorf("derived field %q requires source", field.Name)
			}
			if strings.TrimSpace(field.Predicate) == "" && strings.TrimSpace(field.PredicatePath) == "" {
				return fmt.Errorf("derived field %q requires predicate or predicatePath", field.Name)
			}
			if strings.TrimSpace(field.PredicatePath) != "" {
				if _, err := ParseSelector(field.PredicatePath); err != nil {
					return fmt.Errorf("derived field %q invalid predicatePath: %w", field.Name, err)
				}
			}
		case DerivedOpAny:
			if field.Source == "" {
				return fmt.Errorf("derived field %q requires source", field.Name)
			}
			if strings.TrimSpace(field.Predicate) != "" || strings.TrimSpace(field.PredicatePath) != "" {
				if strings.TrimSpace(field.PredicatePath) != "" {
					if _, err := ParseSelector(field.PredicatePath); err != nil {
						return fmt.Errorf("derived field %q invalid predicatePath: %w", field.Name, err)
					}
				}
			}
		default:
			return fmt.Errorf("derived field %q uses unsupported operation %q", field.Name, field.Operation)
		}
	}

	seenSlices := map[string]struct{}{}
	for _, slice := range builder.RepresentativeSlices {
		if slice.Name == "" {
			return fmt.Errorf("representative slice name is required")
		}
		if _, ok := seenSlices[slice.Name]; ok {
			return fmt.Errorf("representative slice %q is duplicated", slice.Name)
		}
		seenSlices[slice.Name] = struct{}{}
		if slice.SourceSet == "" {
			return fmt.Errorf("representative slice %q requires sourceSet", slice.Name)
		}
		if slice.Limit <= 0 {
			return fmt.Errorf("representative slice %q requires positive limit", slice.Name)
		}
		for _, field := range slice.Fields {
			if strings.TrimSpace(field.Name) == "" || strings.TrimSpace(field.Select) == "" {
				return fmt.Errorf("representative slice %q requires fields with name and select", slice.Name)
			}
			if _, err := ParseSelector(field.Select); err != nil {
				return fmt.Errorf("representative slice %q invalid field %q: %w", slice.Name, field.Name, err)
			}
		}
	}
	return nil
}

func validateTraversalFilterSemantics(sourceResourceType string, traversals []TraversalStep) error {
	for _, step := range traversals {
		if err := step.MatchMode.Validate(); err != nil {
			return fmt.Errorf("traversal %s -> %s (%s): %w", sourceResourceType, step.ToResourceType, step.Label, err)
		}
		if !fhirschema.HasResource(step.ToResourceType) {
			return fmt.Errorf("traversal target resource type %q is not represented by the active generated FHIR schema", step.ToResourceType)
		}
		if _, err := resolveStorageRoute(sourceResourceType, step.Label, step.ToResourceType); err != nil {
			return fmt.Errorf("traversal %s -> %s (%s): %w", sourceResourceType, step.ToResourceType, step.Label, err)
		}
		for _, filter := range step.Filters {
			if err := ValidateTypedFilterForResource(step.ToResourceType, filter); err != nil {
				return fmt.Errorf("traversal %s -> %s filter %q: %w", sourceResourceType, step.ToResourceType, filter.FieldRef, err)
			}
		}
		if err := validateTraversalFilterSemantics(step.ToResourceType, step.Traversals); err != nil {
			return err
		}
	}
	return nil
}
