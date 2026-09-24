package catalog

import "fmt"

type RouteCoverageSourceKind string

const (
	RouteCoverageField    RouteCoverageSourceKind = "FIELD"
	RouteCoverageSemantic RouteCoverageSourceKind = "SEMANTIC"
)

type RouteCoverageSource struct {
	Kind      RouteCoverageSourceKind
	FieldPath string
	ConceptID string
	BindingID string
}

func (source RouteCoverageSource) Validate() error {
	switch source.Kind {
	case RouteCoverageField:
		if source.FieldPath == "" || source.ConceptID != "" || source.BindingID != "" {
			return fmt.Errorf("field coverage requires only a field path")
		}
	case RouteCoverageSemantic:
		if source.FieldPath != "" || source.ConceptID == "" || source.BindingID == "" {
			return fmt.Errorf("semantic coverage requires one exact concept and binding")
		}
	default:
		return fmt.Errorf("unsupported route coverage source kind %q", source.Kind)
	}
	return nil
}

type RouteCoverageStep struct {
	FromResourceType string
	ToResourceType   string
	Relationship     string
	StorageDirection string
}

// RouteCoverageOptions describes the default authorized record cohort. Custom
// populations and filters are resolved by the caller before this query runs.
type RouteCoverageOptions struct {
	Project                       string
	DatasetGeneration             string
	BuildID                       string
	AuthResourcePaths             []string
	AuthResourcePathsUnrestricted bool
	RootResourceType              string
	SourceResourceType            string
	Route                         []RouteCoverageStep
	Source                        RouteCoverageSource
}

type RouteCoverage struct {
	RowsWithValue int64 `json:"rowsWithValue"`
}
