package ir

import (
	"fmt"
	"strings"
)

// PhysicalCategoryScanReturn describes distinct category discovery over an exact
// source relation. It is a renderer terminal request, not an executable source
// operation; source scopes remain in the validated PhysicalPlan.
type PhysicalCategoryScanReturn struct {
	CategoryColumn          string
	PresenceMarkerColumn    string
	PresenceCompanionColumn string
	ValueColumn             string
	MaxValues               int
	StreamRoot              bool
	RootIndexHint           string
	IndexedRoot             *PhysicalIndexedCategoryScan
}
type PhysicalIndexedCategoryScan struct {
	RootVariable     string
	ValuePath        []string
	ResourceType     string
	StoredParentPath []string
}

func (s PhysicalCategoryScanReturn) Validate() error {
	if strings.TrimSpace(s.CategoryColumn) == "" || s.MaxValues < 1 || s.MaxValues > 256 {
		return fmt.Errorf("invalid category terminal column or limit")
	}
	if s.PresenceCompanionColumn == "" && s.PresenceMarkerColumn == "" && s.IndexedRoot == nil {
		return fmt.Errorf("category terminal requires a presence contract")
	}
	if s.RootIndexHint != "" && (!physicalPathPartPattern.MatchString(s.RootIndexHint) || s.RootIndexHint[0] == '_') {
		return fmt.Errorf("unsafe category index hint")
	}
	if s.IndexedRoot != nil {
		if s.StreamRoot || s.ValueColumn != "" || s.PresenceCompanionColumn != "" {
			return fmt.Errorf("indexed category terminal cannot combine row-stream or value-bearing modes")
		}
		r := s.IndexedRoot
		if r.RootVariable == "" || len(r.ValuePath) == 0 {
			return fmt.Errorf("indexed category terminal lacks direct root presence")
		}
		for _, path := range [][]string{r.ValuePath, r.StoredParentPath} {
			for _, part := range path {
				if !physicalPathPartPattern.MatchString(part) {
					return fmt.Errorf("unsafe indexed category path")
				}
			}
		}
	}
	return nil
}

// DirectScalarRootProjectionValue proves an unchanged direct scalar source slot.
func DirectScalarRootProjectionValue(projection PhysicalProjection, rootVariable string) (PhysicalValue, bool) {
	if projection.Expression == nil {
		value := projection.Value
		return value, value.Variable == rootVariable && value.BindKey == "" && len(value.Path) > 0
	}
	expression := projection.Expression
	if expression.Kind != PhysicalExtractExpression || expression.Cardinality != PhysicalScalarCardinality ||
		expression.Extract == nil {
		return PhysicalValue{}, false
	}
	extract := expression.Extract
	if extract.ExecutionMode != PhysicalSelectorDirectScalar || extract.Source.Variable != rootVariable ||
		extract.Source.BindKey != "" || len(extract.Source.Path) == 0 || len(extract.Fallbacks) != 0 ||
		extract.Prepared != nil || extract.Distinct || extract.UnitNormalization != nil || extract.Selector.Filter != nil ||
		len(extract.Selector.Steps) == 0 {
		return PhysicalValue{}, false
	}
	path := append([]string(nil), extract.Source.Path...)
	for _, step := range extract.Selector.Steps {
		if step.Field == "" || step.Iterate || step.Index != nil {
			return PhysicalValue{}, false
		}
		path = append(path, step.Field)
	}
	return PhysicalValue{Variable: rootVariable, Path: path}, true
}
