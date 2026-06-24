package dataframe

import "fmt"

type CompiledQuery struct {
	Project           string
	RootResourceType  string
	AuthResourcePaths []string
	PlanMode          string
	PlanProfile       string
	NamedSetCount     int
	FileSummaries     bool
	StudyLookup       bool
	Query             string
	BindVars          map[string]any
	Columns           []string
	PivotFields       []string
	Limit             int
}

func Compile(builder Builder, limit int) (CompiledQuery, error) {
	if usesLoweredBuilder(builder) {
		return compileLowered(builder, limit)
	}
	return CompiledQuery{}, fmt.Errorf("unsupported dataframe query shape: request was not lowered into the optimized lowered plan")
}

func planMode(hint *PlanHint) string {
	if hint == nil || hint.Mode == "" {
		return "unsupported"
	}
	return hint.Mode
}

func planProfile(hint *PlanHint) string {
	if hint == nil {
		return ""
	}
	return hint.Profile
}

func planNamedSetCount(hint *PlanHint) int {
	if hint == nil {
		return 0
	}
	return hint.NamedSetCount
}

func planFileSummaries(hint *PlanHint) bool {
	if hint == nil {
		return false
	}
	return hint.ClassifiedFileSummaries
}

func planStudyLookup(hint *PlanHint) bool {
	if hint == nil {
		return false
	}
	return hint.StudyLookup
}

type compiler struct {
	builder     Builder
	bindVars    map[string]any
	columns     []string
	pivotFields []string
	bindCount   int
	pivotExprs  map[string]string
}
