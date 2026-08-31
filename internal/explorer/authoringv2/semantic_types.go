package authoringv2

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	LifecycleNew   = "NEW"
	LifecycleReady = "READY"
)

type ExplorerMetadata struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// RouteNode is one semantic occurrence in a document route tree. Relationship
// is empty only for the root; every child names the relationship from its
// parent. Resource identities are stable FHIR resource types, not catalog IDs.
type RouteNode struct {
	OccurrenceID string      `json:"occurrenceId"`
	ResourceType string      `json:"resourceType"`
	Relationship string      `json:"relationship,omitempty"`
	Children     []RouteNode `json:"children,omitempty"`
}

type ColumnSource struct {
	Kind           string `json:"kind"`
	FieldPath      string `json:"fieldPath,omitempty"`
	Match          string `json:"match,omitempty"`
	ProjectionMode string `json:"projectionMode,omitempty"`
	Operation      string   `json:"operation,omitempty"`
	WherePath      string   `json:"wherePath,omitempty"`
	WhereEquals    string   `json:"whereEquals,omitempty"`
	RequiredValues []string `json:"requiredValues,omitempty"`
}

const (
	SourceField                      = "field"
	SourceIdentifierBySystem         = "identifierBySystem"
	SourceExtensionByURL             = "extensionByUrl"
	SourceCodingBySystem             = "codingBySystem"
	SourceObservationComponentByCode = "observationComponentByCode"
	SourceProjectID                  = "projectId"
	SourceAggregate                  = "aggregate"
)

type Column struct {
	Column       string              `json:"column"`
	Label        string              `json:"label"`
	LogicalType  string              `json:"logicalType,omitempty"`
	OccurrenceID string              `json:"occurrenceId"`
	Source       ColumnSource        `json:"source"`
	Table        *TablePresentation  `json:"table,omitempty"`
	Filter       *FilterPresentation `json:"filter,omitempty"`
	Chart        *ChartPresentation  `json:"chart,omitempty"`
}

type FixedFilter struct {
	Column string   `json:"column"`
	Values []string `json:"values"`
}

type ActionColumn struct {
	Column       string `json:"column"`
	ExportHeader string `json:"exportHeader,omitempty"`
}

type Action struct {
	Type     string         `json:"type"`
	Title    string         `json:"title"`
	FileName string         `json:"fileName,omitempty"`
	Columns  []ActionColumn `json:"columns,omitempty"`
}

type SharedFilterBinding struct {
	OutputID string `json:"outputId"`
	Column   string `json:"column"`
}

type FileActions struct {
	Extensions map[string][]string `json:"extensions"`
	Actions    map[string]string   `json:"actions"`
}

var physicalColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (d Document) semanticOccurrences() (map[string]RouteNode, error) {
	if strings.TrimSpace(d.RootResourceType) == "" {
		return nil, fmt.Errorf("rootResourceType is required")
	}
	if d.Route.OccurrenceID != RootOccurrenceID || d.Route.ResourceType != d.RootResourceType || strings.TrimSpace(d.Route.Relationship) != "" {
		return nil, fmt.Errorf("route root must be occurrence %q with resourceType %q and no relationship", RootOccurrenceID, d.RootResourceType)
	}
	occurrences := map[string]RouteNode{}
	var walk func(RouteNode, string) error
	walk = func(node RouteNode, path string) error {
		if emptyID(node.OccurrenceID) || strings.TrimSpace(node.ResourceType) == "" {
			return fmt.Errorf("%s occurrenceId and resourceType are required", path)
		}
		if _, duplicate := occurrences[node.OccurrenceID]; duplicate {
			return fmt.Errorf("duplicate route occurrence id %q", node.OccurrenceID)
		}
		occurrences[node.OccurrenceID] = node
		for i, child := range node.Children {
			if strings.TrimSpace(child.Relationship) == "" {
				return fmt.Errorf("%s.children[%d].relationship is required", path, i)
			}
			if err := walk(child, fmt.Sprintf("%s.children[%d]", path, i)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(d.Route, "route"); err != nil {
		return nil, err
	}
	return occurrences, nil
}

func (s ColumnSource) validate(path string) error {
	mode := strings.ToUpper(strings.TrimSpace(s.ProjectionMode))
	if mode == "" {
		mode = "FIRST"
	}
	if mode != "VALUE" && mode != "FIRST" && mode != "ALL" && mode != "DISTINCT" {
		return fmt.Errorf("%s.projectionMode %q is unsupported", path, s.ProjectionMode)
	}
	switch s.Kind {
	case SourceField:
		if strings.TrimSpace(s.FieldPath) == "" || strings.TrimSpace(s.Match) != "" {
			return fmt.Errorf("%s field source requires fieldPath and forbids match", path)
		}
	case SourceIdentifierBySystem, SourceExtensionByURL, SourceCodingBySystem, SourceObservationComponentByCode:
		if strings.TrimSpace(s.Match) == "" {
			return fmt.Errorf("%s %s source requires match", path, s.Kind)
		}
	case SourceProjectID:
		if strings.TrimSpace(s.FieldPath) != "" || strings.TrimSpace(s.Match) != "" || strings.TrimSpace(s.Operation) != "" || strings.TrimSpace(s.WherePath) != "" || strings.TrimSpace(s.WhereEquals) != "" || len(s.RequiredValues) != 0 || strings.TrimSpace(s.ProjectionMode) != "" {
			return fmt.Errorf("%s projectId source has no parameters", path)
		}
	case SourceAggregate:
		if strings.TrimSpace(s.Match) != "" || strings.TrimSpace(s.ProjectionMode) != "" {
			return fmt.Errorf("%s aggregate source forbids match and projectionMode", path)
		}
		op := strings.ToUpper(strings.TrimSpace(s.Operation))
		switch op {
		case "COUNT", "COUNT_DISTINCT", "DISTINCT_VALUES", "MIN", "MAX", "EXISTS", "CONTAINS_ALL":
		default:
			return fmt.Errorf("%s aggregate source operation %q is unsupported", path, s.Operation)
		}
		requiresField := op == "COUNT_DISTINCT" || op == "DISTINCT_VALUES" || op == "MIN" || op == "MAX" || op == "CONTAINS_ALL"
		if requiresField && strings.TrimSpace(s.FieldPath) == "" {
			return fmt.Errorf("%s aggregate operation %s requires fieldPath", path, op)
		}
		if !requiresField && strings.TrimSpace(s.FieldPath) != "" {
			return fmt.Errorf("%s aggregate operation %s forbids fieldPath", path, op)
		}
		if strings.TrimSpace(s.WhereEquals) != "" && strings.TrimSpace(s.WherePath) == "" {
			return fmt.Errorf("%s.whereEquals requires wherePath", path)
		}
		if op == "CONTAINS_ALL" {
			if len(s.RequiredValues) == 0 {
				return fmt.Errorf("%s.requiredValues is required for CONTAINS_ALL", path)
			}
			seen := map[string]bool{}
			for i, value := range s.RequiredValues {
				if strings.TrimSpace(value) == "" {
					return fmt.Errorf("%s.requiredValues[%d] must be non-empty", path, i)
				}
				if seen[value] {
					return fmt.Errorf("%s.requiredValues[%d] is duplicated", path, i)
				}
				seen[value] = true
			}
		} else if len(s.RequiredValues) != 0 {
			return fmt.Errorf("%s.requiredValues is only valid for CONTAINS_ALL", path)
		}
	default:
		return fmt.Errorf("%s source kind %q is unsupported", path, s.Kind)
	}
	return nil
}

func (d Document) validateSemantic() error {
	occurrences, err := d.semanticOccurrences()
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, column := range d.Columns {
		path := fmt.Sprintf("columns[%d]", i)
		if !physicalColumnPattern.MatchString(column.Column) {
			return fmt.Errorf("%s.column %q is not a valid physical column", path, column.Column)
		}
		if seen[column.Column] {
			return fmt.Errorf("duplicate column %q", column.Column)
		}
		seen[column.Column] = true
		if strings.TrimSpace(column.Label) == "" {
			return fmt.Errorf("%s.label is required", path)
		}
		if _, ok := occurrences[column.OccurrenceID]; !ok {
			return fmt.Errorf("%s references unknown occurrence %q", path, column.OccurrenceID)
		}
		if err := column.Source.validate(path + ".source"); err != nil {
			return err
		}
		if column.Table != nil && column.Table.Order != nil && *column.Table.Order < 0 {
			return fmt.Errorf("%s.table.order must not be negative", path)
		}
		if column.Table != nil && column.Table.CellRenderer != "" && column.Table.CellRenderer != "fileActions" {
			return fmt.Errorf("%s.table.cellRenderer is unsupported", path)
		}
		if column.Filter != nil && column.Filter.Order != nil && *column.Filter.Order < 0 {
			return fmt.Errorf("%s.filter.order must not be negative", path)
		}
		if column.Chart != nil {
			if strings.TrimSpace(column.Chart.Type) == "" {
				return fmt.Errorf("%s.chart.type is required", path)
			}
			if column.Chart.Order != nil && *column.Chart.Order < 0 {
				return fmt.Errorf("%s.chart.order must not be negative", path)
			}
		}
	}
	for i, fixed := range d.FixedFilters {
		if !seen[fixed.Column] || len(fixed.Values) == 0 {
			return fmt.Errorf("fixedFilters[%d] must reference a declared column and contain values", i)
		}
	}
	for i, action := range d.Actions {
		if strings.TrimSpace(action.Type) == "" || strings.TrimSpace(action.Title) == "" {
			return fmt.Errorf("actions[%d].type and title are required", i)
		}
		for j, binding := range action.Columns {
			if !seen[binding.Column] {
				return fmt.Errorf("actions[%d].columns[%d] references unknown column %q", i, j, binding.Column)
			}
		}
	}
	return nil
}

func (w Workspace) validateSemanticBindings() error {
	columns := map[string]map[string]bool{}
	for _, document := range w.Documents {
		columns[document.Output.ID] = map[string]bool{}
		for _, column := range document.Columns {
			columns[document.Output.ID][column.Column] = true
		}
	}
	for name, bindings := range w.SharedFilters {
		if strings.TrimSpace(name) == "" || len(bindings) == 0 {
			return fmt.Errorf("sharedFilters names and bindings must be non-empty")
		}
		for i, binding := range bindings {
			if !columns[binding.OutputID][binding.Column] {
				return fmt.Errorf("sharedFilters[%q][%d] references unknown output column", name, i)
			}
		}
	}
	if w.FileActions != nil {
		for extension, actions := range w.FileActions.Extensions {
			if strings.TrimSpace(extension) == "" || len(actions) == 0 {
				return fmt.Errorf("fileActions.extensions entries must be non-empty")
			}
			for _, action := range actions {
				if _, ok := w.FileActions.Actions[action]; !ok {
					return fmt.Errorf("fileActions extension %q references unknown action %q", extension, action)
				}
			}
		}
	}
	return nil
}
