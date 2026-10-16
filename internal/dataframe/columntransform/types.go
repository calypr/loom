// Package columntransform defines closed, backend-neutral transformations
// that operate on one already-selected output column value.
package columntransform

import "fmt"

type Kind string

const (
	KindExactCategoryRecode Kind = "EXACT_CATEGORY_RECODE"
)

type UnknownPolicy string

const (
	UnknownError        UnknownPolicy = "ERROR"
	UnknownKeepOriginal UnknownPolicy = "KEEP_ORIGINAL"
)

// ValueTransformation is a closed union. The selected column remains the
// identity-bearing output; this value only changes its scalar contents.
type ValueTransformation struct {
	Kind                Kind                 `json:"kind"`
	ExactCategoryRecode *ExactCategoryRecode `json:"exactCategoryRecode,omitempty"`
}

type ExactCategoryRecode struct {
	Mappings      []CategoryMapping `json:"mappings"`
	UnknownPolicy UnknownPolicy     `json:"unknownPolicy"`
}

type CategoryMapping struct {
	From string `json:"from"`
	To   string `json:"to"`
}

func (t ValueTransformation) Validate() error {
	switch t.Kind {
	case KindExactCategoryRecode:
		if t.ExactCategoryRecode == nil {
			return fmt.Errorf("EXACT_CATEGORY_RECODE requires exactCategoryRecode")
		}
		if err := t.ExactCategoryRecode.Validate(); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported column transformation kind %q", t.Kind)
	}
	return nil
}

func (r ExactCategoryRecode) Validate() error {
	if len(r.Mappings) == 0 {
		return fmt.Errorf("exactCategoryRecode.mappings must not be empty")
	}
	switch r.UnknownPolicy {
	case UnknownError, UnknownKeepOriginal:
	default:
		return fmt.Errorf("exactCategoryRecode.unknownPolicy must be ERROR or KEEP_ORIGINAL")
	}
	seen := make(map[string]struct{}, len(r.Mappings))
	for index, mapping := range r.Mappings {
		if _, exists := seen[mapping.From]; exists {
			return fmt.Errorf("exactCategoryRecode.mappings[%d].from duplicates an earlier exact source value", index)
		}
		seen[mapping.From] = struct{}{}
	}
	return nil
}

func (t ValueTransformation) Clone() ValueTransformation {
	copy := t
	if t.ExactCategoryRecode != nil {
		recode := *t.ExactCategoryRecode
		recode.Mappings = append([]CategoryMapping(nil), t.ExactCategoryRecode.Mappings...)
		copy.ExactCategoryRecode = &recode
	}
	return copy
}
