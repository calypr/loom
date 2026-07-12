package recipe

import (
	"fmt"
	"strings"
	"unicode"
)

const (
	maxColumns      = 512
	maxFilters      = 128
	maxFilterValues = 1024
)

type ValidationError struct {
	Code    string
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func invalid(code, field, message string) error {
	return &ValidationError{Code: code, Field: field, Message: message}
}

func (r Recipe) Validate() error {
	_, err := r.Normalize()
	return err
}

// Normalize trims user-owned labels, canonicalizes operator spelling, removes
// duplicate column selections, and returns a defensive copy.
func (r Recipe) Normalize() (Recipe, error) {
	r.Project = strings.TrimSpace(r.Project)
	r.Generation = strings.TrimSpace(r.Generation)
	if r.Version != VersionV1 {
		return Recipe{}, invalid("unsupported_version", "version", fmt.Sprintf("must be %q", VersionV1))
	}
	template, ok := templateByID[r.Template]
	if !ok {
		return Recipe{}, invalid("unknown_template", "template", fmt.Sprintf("unknown template %q", r.Template))
	}
	if r.TemplateVersion <= 0 {
		return Recipe{}, invalid("invalid_template_version", "templateVersion", "must be positive")
	}
	if r.Project == "" || hasControl(r.Project) {
		return Recipe{}, invalid("invalid_project", "project", "must be a non-empty printable identifier")
	}
	if !templateAllowsGrain(template, r.Grain) {
		return Recipe{}, invalid("incompatible_grain", "grain", fmt.Sprintf("template %q does not support grain %q", r.Template, r.Grain))
	}
	switch r.GenerationPolicy {
	case GenerationLatest:
		if r.Generation != "" {
			return Recipe{}, invalid("unexpected_generation", "generation", "must be empty when policy is latest")
		}
	case GenerationPinned:
		if r.Generation == "" || hasControl(r.Generation) {
			return Recipe{}, invalid("missing_generation", "generation", "is required when policy is pinned")
		}
	default:
		return Recipe{}, invalid("invalid_generation_policy", "generationPolicy", "must be latest or pinned")
	}
	if len(r.Columns) == 0 {
		return Recipe{}, invalid("missing_columns", "columns", "at least one column is required")
	}
	if len(r.Columns) > maxColumns {
		return Recipe{}, invalid("too_many_columns", "columns", fmt.Sprintf("cannot exceed %d", maxColumns))
	}
	columns := make([]ColumnSelection, 0, len(r.Columns))
	knownColumns := make(map[string]struct{}, len(r.Columns))
	outputNames := make(map[string]struct{}, len(r.Columns))
	for index, column := range r.Columns {
		column.ID = strings.TrimSpace(column.ID)
		column.OutputName = strings.TrimSpace(column.OutputName)
		field := fmt.Sprintf("columns[%d]", index)
		if !validSemanticID(column.ID) {
			return Recipe{}, invalid("invalid_semantic_id", field+".id", "must be a stable semantic identifier")
		}
		if column.OutputName != "" && hasControl(column.OutputName) {
			return Recipe{}, invalid("invalid_output_name", field+".outputName", "contains control characters")
		}
		if _, exists := knownColumns[column.ID]; exists {
			continue
		}
		if column.OutputName != "" {
			if _, exists := outputNames[column.OutputName]; exists {
				return Recipe{}, invalid("duplicate_output_name", field+".outputName", fmt.Sprintf("%q is duplicated", column.OutputName))
			}
			outputNames[column.OutputName] = struct{}{}
		}
		knownColumns[column.ID] = struct{}{}
		columns = append(columns, column)
	}
	r.Columns = columns
	if len(r.Filters) > maxFilters {
		return Recipe{}, invalid("too_many_filters", "filters", fmt.Sprintf("cannot exceed %d", maxFilters))
	}
	r.Filters = append([]Filter(nil), r.Filters...)
	for index := range r.Filters {
		filter := &r.Filters[index]
		filter.ColumnID = strings.TrimSpace(filter.ColumnID)
		filter.Operator = FilterOperator(strings.ToLower(strings.TrimSpace(string(filter.Operator))))
		filter.Values = append([]string(nil), filter.Values...)
		field := fmt.Sprintf("filters[%d]", index)
		if !validSemanticID(filter.ColumnID) {
			return Recipe{}, invalid("invalid_semantic_id", field+".columnId", "must be a stable semantic identifier")
		}
		if _, exists := knownColumns[filter.ColumnID]; !exists {
			return Recipe{}, invalid("unknown_filter_column", field+".columnId", "must reference a selected column")
		}
		if err := validateFilter(filter, field); err != nil {
			return Recipe{}, err
		}
	}
	if !validDestination(r.Destination.Type) {
		return Recipe{}, invalid("invalid_destination", "destination.type", fmt.Sprintf("unknown destination %q", r.Destination.Type))
	}
	if !templateAllowsDestination(template, r.Destination.Type) {
		return Recipe{}, invalid("unsupported_destination", "destination.type", fmt.Sprintf("template %q does not support destination %q", r.Template, r.Destination.Type))
	}
	return r, nil
}

func validateFilter(filter *Filter, field string) error {
	if len(filter.Values) > maxFilterValues {
		return invalid("too_many_filter_values", field+".values", fmt.Sprintf("cannot exceed %d", maxFilterValues))
	}
	for index := range filter.Values {
		filter.Values[index] = strings.TrimSpace(filter.Values[index])
		if hasControl(filter.Values[index]) {
			return invalid("invalid_filter_value", fmt.Sprintf("%s.values[%d]", field, index), "contains control characters")
		}
	}
	want := -1
	switch filter.Operator {
	case FilterExists, FilterMissing:
		want = 0
	case FilterEquals, FilterNotEquals, FilterContains, FilterGreaterThan, FilterLessThan:
		want = 1
	case FilterBetween:
		want = 2
	case FilterIn, FilterNotIn:
		if len(filter.Values) == 0 {
			return invalid("missing_filter_values", field+".values", "at least one value is required")
		}
		return nil
	default:
		return invalid("invalid_filter_operator", field+".operator", fmt.Sprintf("unknown operator %q", filter.Operator))
	}
	if len(filter.Values) != want {
		return invalid("invalid_filter_values", field+".values", fmt.Sprintf("operator %q requires %d values", filter.Operator, want))
	}
	return nil
}

func validSemanticID(value string) bool {
	if value == "" || len(value) > 256 {
		return false
	}
	for _, r := range value {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("._:-", r)) {
			return false
		}
	}
	return true
}

func hasControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
