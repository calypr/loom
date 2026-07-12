package discovery

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/fhirschema"
)

// Validate proves that a Snapshot remains a normalized, compiler-safe product
// contract. It is useful at the boundary where a later command handler accepts
// IDs chosen from a prior discovery response.
func (snapshot Snapshot) Validate() error {
	if strings.TrimSpace(snapshot.Dataset.Project) == "" {
		return fmt.Errorf("dataset project is required")
	}
	roots, err := validateRoots(snapshot.Dataset.Roots)
	if err != nil {
		return err
	}

	columnCounts := make(map[string]int, len(roots))
	columns, err := validateColumns(snapshot.Columns, roots, columnCounts)
	if err != nil {
		return err
	}
	relationshipCounts := make(map[string]int, len(roots))
	if err := validateRelationships(snapshot.Relationships.Entries, roots, relationshipCounts); err != nil {
		return err
	}
	if err := validateFilters(snapshot.Filters, columns); err != nil {
		return err
	}

	for _, root := range snapshot.Dataset.Roots {
		if root.CandidateColumnCount != columnCounts[root.ResourceType] {
			return fmt.Errorf("root candidate column count does not match columns")
		}
		if root.RelationshipCount != relationshipCounts[root.ResourceType] {
			return fmt.Errorf("root relationship count does not match relationships")
		}
		if root.Available != (root.CandidateColumnCount > 0 || root.RelationshipCount > 0 || hasIncomingRelationship(snapshot.Relationships.Entries, root.ResourceType)) {
			return fmt.Errorf("root availability does not match supplied catalog facts")
		}
	}
	return nil
}

func validateRoots(roots []RootResourceSummary) (map[string]RootResourceSummary, error) {
	expected := fhirschema.ResourceTypes()
	if len(roots) != len(expected) {
		return nil, fmt.Errorf("dataset roots do not match active generated schema")
	}
	byType := make(map[string]RootResourceSummary, len(roots))
	for i, root := range roots {
		if root.ResourceType != expected[i] {
			return nil, fmt.Errorf("dataset roots are not the sorted active generated schema roots")
		}
		if !root.Supported {
			return nil, fmt.Errorf("generated schema root must be marked supported")
		}
		if !root.SupportReason.Valid() {
			return nil, fmt.Errorf("root support reason is invalid")
		}
		if root.Available && root.SupportReason != RootSupportObservedInCatalog {
			return nil, fmt.Errorf("available root must have observed catalog support reason")
		}
		if !root.Available && root.SupportReason != RootSupportNotObservedInCatalog {
			return nil, fmt.Errorf("unavailable root must have not observed support reason")
		}
		if root.CandidateColumnCount < 0 || root.RelationshipCount < 0 {
			return nil, fmt.Errorf("root counts must not be negative")
		}
		byType[root.ResourceType] = root
	}
	return byType, nil
}

func validateColumns(columns []CandidateColumn, roots map[string]RootResourceSummary, counts map[string]int) (map[ColumnID]CandidateColumn, error) {
	byID := make(map[ColumnID]CandidateColumn, len(columns))
	var previous CandidateColumn
	for index, column := range columns {
		if !validOpaqueID(string(column.ID), "col_") {
			return nil, fmt.Errorf("candidate column ID is not a valid opaque identifier")
		}
		if _, ok := roots[column.ResourceType]; !ok {
			return nil, fmt.Errorf("candidate column resource type is not an active generated root")
		}
		if strings.TrimSpace(column.Label) == "" {
			return nil, fmt.Errorf("candidate column label is required")
		}
		if !column.ValueKind.Valid() {
			return nil, fmt.Errorf("candidate column has an unsupported value kind")
		}
		if column.PopulatedDocumentCount < 0 {
			return nil, fmt.Errorf("candidate column population count must not be negative")
		}
		if err := validateColumnCapabilities(column); err != nil {
			return nil, err
		}
		if err := validateSuggestedValues(column.SuggestedValues); err != nil {
			return nil, err
		}
		if _, exists := byID[column.ID]; exists {
			return nil, fmt.Errorf("candidate column IDs must be unique")
		}
		if index > 0 && compareColumns(previous, column) >= 0 {
			return nil, fmt.Errorf("candidate columns are not sorted deterministically")
		}
		previous = column
		byID[column.ID] = column
		counts[column.ResourceType]++
	}
	return byID, nil
}

func validateColumnCapabilities(column CandidateColumn) error {
	switch column.ValueKind {
	case ValueKindComposite:
		if column.Repeated || column.CanSelect || column.CanFilter || !column.CanPivot {
			return fmt.Errorf("composite candidate must be pivot-only")
		}
	default:
		if !column.CanSelect || !column.CanFilter {
			return fmt.Errorf("scalar candidate must support selection and filtering")
		}
	}
	return nil
}

func validateRelationships(relationships []Relationship, roots map[string]RootResourceSummary, counts map[string]int) error {
	seen := make(map[RelationshipID]struct{}, len(relationships))
	var previous Relationship
	for index, relationship := range relationships {
		if !validOpaqueID(string(relationship.ID), "rel_") {
			return fmt.Errorf("relationship ID is not a valid opaque identifier")
		}
		if _, ok := roots[relationship.FromResourceType]; !ok {
			return fmt.Errorf("relationship source is not an active generated root")
		}
		if _, ok := roots[relationship.ToResourceType]; !ok {
			return fmt.Errorf("relationship target is not an active generated root")
		}
		if strings.TrimSpace(relationship.Label) == "" {
			return fmt.Errorf("relationship label is required")
		}
		if !relationship.Multiplicity.Valid() {
			return fmt.Errorf("relationship has unsupported multiplicity")
		}
		if relationship.ObservedEdgeCount < 0 {
			return fmt.Errorf("relationship edge count must not be negative")
		}
		if _, exists := seen[relationship.ID]; exists {
			return fmt.Errorf("relationship IDs must be unique")
		}
		if index > 0 && compareRelationships(previous, relationship) >= 0 {
			return fmt.Errorf("relationships are not sorted deterministically")
		}
		previous = relationship
		seen[relationship.ID] = struct{}{}
		counts[relationship.FromResourceType]++
	}
	return nil
}

func validateFilters(filters []GuidedFilterSuggestion, columns map[ColumnID]CandidateColumn) error {
	seen := make(map[FilterSuggestionID]struct{}, len(filters))
	var previous GuidedFilterSuggestion
	filterableColumns := 0
	for _, column := range columns {
		if column.CanFilter {
			filterableColumns++
		}
	}
	if len(filters) != filterableColumns {
		return fmt.Errorf("guided filters must cover every filterable candidate exactly once")
	}
	for index, filter := range filters {
		if !validOpaqueID(string(filter.ID), "flt_") {
			return fmt.Errorf("guided filter ID is not a valid opaque identifier")
		}
		column, ok := columns[filter.ColumnID]
		if !ok || !column.CanFilter {
			return fmt.Errorf("guided filter does not resolve to a filterable candidate")
		}
		if filter.ID != opaqueFilterSuggestionID(filter.ColumnID) ||
			filter.ResourceType != column.ResourceType ||
			filter.Label != column.Label ||
			filter.ValueKind != column.ValueKind ||
			filter.Repeated != column.Repeated ||
			!equalFilterOperators(filter.Operators, filterOperators(column.ValueKind)) ||
			!equalFilterQuantifiers(filter.Quantifiers, filterQuantifiers(column.Repeated)) ||
			!equalStrings(filter.SuggestedValues, column.SuggestedValues) ||
			filter.ValuesTruncated != column.ValuesTruncated {
			return fmt.Errorf("guided filter does not match its candidate")
		}
		if _, exists := seen[filter.ID]; exists {
			return fmt.Errorf("guided filter IDs must be unique")
		}
		if index > 0 && compareFilters(previous, filter) >= 0 {
			return fmt.Errorf("guided filters are not sorted deterministically")
		}
		previous = filter
		seen[filter.ID] = struct{}{}
	}
	return nil
}

func hasIncomingRelationship(relationships []Relationship, resourceType string) bool {
	for _, relationship := range relationships {
		if relationship.ToResourceType == resourceType {
			return true
		}
	}
	return false
}

func validOpaqueID(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) || len(value) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))
	return err == nil
}

func validateSuggestedValues(values []string) error {
	if len(values) > maxSuggestedValues {
		return fmt.Errorf("suggested values exceed the bounded limit")
	}
	for index, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("suggested values must not be empty")
		}
		if index > 0 && values[index-1] >= value {
			return fmt.Errorf("suggested values are not sorted and unique")
		}
	}
	return nil
}

func compareColumns(left, right CandidateColumn) int {
	if left.ResourceType != right.ResourceType {
		return strings.Compare(left.ResourceType, right.ResourceType)
	}
	if left.Label != right.Label {
		return strings.Compare(left.Label, right.Label)
	}
	return strings.Compare(string(left.ID), string(right.ID))
}

func compareRelationships(left, right Relationship) int {
	if left.FromResourceType != right.FromResourceType {
		return strings.Compare(left.FromResourceType, right.FromResourceType)
	}
	if left.ToResourceType != right.ToResourceType {
		return strings.Compare(left.ToResourceType, right.ToResourceType)
	}
	if left.Label != right.Label {
		return strings.Compare(left.Label, right.Label)
	}
	return strings.Compare(string(left.ID), string(right.ID))
}

func compareFilters(left, right GuidedFilterSuggestion) int {
	if left.ResourceType != right.ResourceType {
		return strings.Compare(left.ResourceType, right.ResourceType)
	}
	if left.Label != right.Label {
		return strings.Compare(left.Label, right.Label)
	}
	return strings.Compare(string(left.ID), string(right.ID))
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalFilterOperators(left, right []FilterOperator) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalFilterQuantifiers(left, right []FilterQuantifier) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (kind ValueKind) Valid() bool {
	switch kind {
	case ValueKindString, ValueKindBoolean, ValueKindInteger, ValueKindDecimal, ValueKindDate, ValueKindDateTime, ValueKindComposite:
		return true
	default:
		return false
	}
}

func (multiplicity RelationshipMultiplicity) Valid() bool {
	return multiplicity == RelationshipOne || multiplicity == RelationshipMany
}

func (reason RootSupportReason) Valid() bool {
	return reason == RootSupportObservedInCatalog || reason == RootSupportNotObservedInCatalog
}
