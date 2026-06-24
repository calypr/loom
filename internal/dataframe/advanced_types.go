package dataframe

import (
	"fmt"
	"strings"
)

const (
	SetKindTraverse                  = "TRAVERSE"
	SetKindFilter                    = "FILTER"
	SetKindUnion                     = "UNION"
	SetKindClassifyDocumentReference = "CLASSIFY_DOCUMENT_REFERENCE"
	SetKindLookupStudy               = "LOOKUP_STUDY"

	DerivedOpRawExpr       = "RAW_EXPR"
	DerivedOpConst         = "CONST"
	DerivedOpRootField     = "ROOT_FIELD"
	DerivedOpFirstNonNull  = "FIRST_NON_NULL"
	DerivedOpCount         = "COUNT"
	DerivedOpCountDistinct = "COUNT_DISTINCT"
	DerivedOpCountWhere    = "COUNT_WHERE"
	DerivedOpAny           = "ANY"
	DerivedOpUnique        = "UNIQUE"
	DerivedOpPivot         = "PIVOT"
)

type NamedSet struct {
	Name              string
	Kind              string
	Source            string
	Sources           []string
	Label             string
	ToResourceType    string
	MatchResourceType string
	Unique            bool
	SortField         string
}

type DerivedField struct {
	Name            string
	Source          string
	Operation       string
	Select          string
	FallbackSelects []string
	Predicate       string
	PredicatePath   string
	PredicateEquals string
	PivotColumns    []string
	PivotFamily     string
	PivotKeySelect  string
	PivotValueSelect string
	RawExpr         string
	ConstValue      any
}

type RepresentativeSlice struct {
	Name            string
	SourceSet       string
	Predicate       string
	PredicateFieldRef string
	PredicatePath   string
	PredicateEquals string
	Limit           int
	Fields          []FieldSelect
}

func usesAdvancedBuilder(builder Builder) bool {
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

func validateAdvancedBuilder(builder Builder) error {
	seenSets := map[string]struct{}{}
	for _, set := range builder.Sets {
		if strings.TrimSpace(set.Name) == "" {
			return fmt.Errorf("set name is required")
		}
		if _, ok := seenSets[set.Name]; ok {
			return fmt.Errorf("set %q is duplicated", set.Name)
		}
		seenSets[set.Name] = struct{}{}
		switch strings.ToUpper(strings.TrimSpace(set.Kind)) {
		case SetKindTraverse:
			if set.Label == "" {
				return fmt.Errorf("set %q requires label", set.Name)
			}
		case SetKindFilter:
			if set.Source == "" {
				return fmt.Errorf("set %q requires source", set.Name)
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
		case DerivedOpRawExpr:
			if strings.TrimSpace(field.RawExpr) == "" {
				return fmt.Errorf("derived field %q requires rawExpr", field.Name)
			}
		case DerivedOpConst:
			if field.ConstValue == nil {
				return fmt.Errorf("derived field %q requires const value", field.Name)
			}
		case DerivedOpRootField:
			if field.Select == "" {
				return fmt.Errorf("derived field %q requires select", field.Name)
			}
		case DerivedOpFirstNonNull, DerivedOpUnique:
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
		case DerivedOpCount:
			if field.Source == "" {
				return fmt.Errorf("derived field %q requires source", field.Name)
			}
		case DerivedOpCountDistinct:
			if field.Source == "" || strings.TrimSpace(field.Select) == "" {
				return fmt.Errorf("derived field %q requires source and select", field.Name)
			}
			if _, err := ParseSelector(field.Select); err != nil {
				return fmt.Errorf("derived field %q invalid select: %w", field.Name, err)
			}
		case DerivedOpCountWhere, DerivedOpAny:
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
