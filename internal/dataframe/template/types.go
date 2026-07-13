// Package template contains product-level dataframe template metadata.
//
// Templates describe semantic preferences only. They do not contain AQL,
// Arango collection names, or compiler selectors. Availability is resolved
// against a caller-scoped capability snapshot before a starter request is
// returned to a client.
package template

import "strings"

// AvailabilityStatus is the support level for a template in one dataset and
// authorization scope.
type AvailabilityStatus string

const (
	StatusAvailable   AvailabilityStatus = "AVAILABLE"
	StatusPartial     AvailabilityStatus = "PARTIAL"
	StatusUnavailable AvailabilityStatus = "UNAVAILABLE"
)

// Definition is immutable product metadata. Definitions are validated when a
// Registry is constructed and are returned defensively from the registry.
type Definition struct {
	ID                  string
	Version             int
	Label               string
	Description         string
	RootCandidates      []string
	RowGrain            string
	SuggestedColumns    []ColumnSuggestion
	SuggestedTraversals []TraversalSuggestion
	SuggestedPivots     []PivotSuggestion
}

// ColumnSuggestion is a user-facing column preference. Alternatives are
// stable fieldRef values in priority order; selector parsing remains owned by
// the existing dataframe input resolver.
type ColumnSuggestion struct {
	ID                   string
	Label                string
	FieldRefAlternatives []string
	DefaultSelected      bool
	Advanced             bool
	Required             bool
}

// TraversalSuggestion describes a semantic relationship preference. The
// observed edge label is deliberately not part of the definition: availability
// resolution obtains a proven label from the current catalog and generated
// FHIR schema.
type TraversalSuggestion struct {
	ID                string
	Label             string
	SemanticRole      string
	FromResourceTypes []string
	ToResourceTypes   []string
	DefaultSelected   bool
	Advanced          bool
	Required          bool
}

// PivotSuggestion identifies a pivot-capable field by fieldRef alternatives.
// Bounded columns are taken from the current catalog, never from product
// metadata or a hard-coded fixture.
type PivotSuggestion struct {
	ID                   string
	Label                string
	FieldRefAlternatives []string
	DefaultSelected      bool
	Advanced             bool
	Required             bool
}

// FieldCapability is a catalog-backed field visible to the current caller.
type FieldCapability struct {
	ResourceType      string
	FieldRef          string
	PivotCandidate    bool
	PivotColumns      []string
	PivotFamily       string
	PivotColumnSelect string
	PivotValueSelect  string
}

// ResourceCapability is a visible resource and its populated fields. Present
// distinguishes a visible resource with no currently populated fields from an
// absent resource.
type ResourceCapability struct {
	ResourceType string
	Present      bool
	Fields       []FieldCapability
}

// RelationshipCapability is a catalog-backed relationship. Label is observed
// data, not a product-template assumption.
type RelationshipCapability struct {
	FromType  string
	Label     string
	ToType    string
	EdgeCount int64
}

// CapabilitySnapshot is the persistence-neutral input to availability
// resolution. Adapters may build it from catalog reads, fake fixtures, or a
// future analysis cache.
type CapabilitySnapshot struct {
	Resources     []ResourceCapability
	Relationships []RelationshipCapability
}

// MissingCapability explains why a suggestion was not included in the
// starter request. Codes are stable machine-readable values.
type MissingCapability struct {
	SuggestionID string
	Kind         string
	Label        string
	Code         string
}

// SelectedColumn is a fieldRef chosen from a suggestion's alternatives.
type SelectedColumn struct {
	ID       string
	Label    string
	FieldRef string
	Advanced bool
}

// SelectedTraversal is a data-backed traversal choice. EdgeLabel is returned
// only after matching a catalog relationship and generated schema metadata.
type SelectedTraversal struct {
	ID           string
	Label        string
	SemanticRole string
	FromType     string
	EdgeLabel    string
	ToType       string
	Advanced     bool
}

// SelectedPivot is a bounded pivot starter intent. The input resolver fills in
// the physical selector pair from the selected fieldRef's catalog metadata.
type SelectedPivot struct {
	ID       string
	Label    string
	FieldRef string
	Columns  []string
	Advanced bool
}

// StarterRequest is intentionally equivalent to the semantic portion of a
// dataframe request, without GraphQL-generated types or raw selectors.
type StarterRequest struct {
	RootResourceType string
	RowGrain         string
	Fields           []SelectedColumn
	Traversals       []SelectedTraversal
	Pivots           []SelectedPivot
}

// Availability is the frontend-facing result of resolving one definition
// against one capability snapshot.
type Availability struct {
	ID               string
	Version          int
	Label            string
	Description      string
	Status           AvailabilityStatus
	RootResourceType string
	CommonColumns    []SelectedColumn
	AdvancedColumns  []SelectedColumn
	Traversals       []SelectedTraversal
	Pivots           []SelectedPivot
	Missing          []MissingCapability
	Reasons          []string
	Starter          StarterRequest
}

func (d Definition) clone() Definition {
	d.RootCandidates = cloneStrings(d.RootCandidates)
	d.SuggestedColumns = append([]ColumnSuggestion(nil), d.SuggestedColumns...)
	for i := range d.SuggestedColumns {
		d.SuggestedColumns[i].FieldRefAlternatives = cloneStrings(d.SuggestedColumns[i].FieldRefAlternatives)
	}
	d.SuggestedTraversals = append([]TraversalSuggestion(nil), d.SuggestedTraversals...)
	for i := range d.SuggestedTraversals {
		d.SuggestedTraversals[i].FromResourceTypes = cloneStrings(d.SuggestedTraversals[i].FromResourceTypes)
		d.SuggestedTraversals[i].ToResourceTypes = cloneStrings(d.SuggestedTraversals[i].ToResourceTypes)
	}
	d.SuggestedPivots = append([]PivotSuggestion(nil), d.SuggestedPivots...)
	for i := range d.SuggestedPivots {
		d.SuggestedPivots[i].FieldRefAlternatives = cloneStrings(d.SuggestedPivots[i].FieldRefAlternatives)
	}
	return d
}

func (a Availability) clone() Availability {
	a.CommonColumns = append([]SelectedColumn(nil), a.CommonColumns...)
	a.AdvancedColumns = append([]SelectedColumn(nil), a.AdvancedColumns...)
	a.Traversals = append([]SelectedTraversal(nil), a.Traversals...)
	a.Pivots = append([]SelectedPivot(nil), a.Pivots...)
	for i := range a.Pivots {
		a.Pivots[i].Columns = cloneStrings(a.Pivots[i].Columns)
	}
	a.Missing = append([]MissingCapability(nil), a.Missing...)
	a.Reasons = cloneStrings(a.Reasons)
	a.Starter = StarterRequest{
		RootResourceType: a.Starter.RootResourceType,
		RowGrain:         a.Starter.RowGrain,
		Fields:           append([]SelectedColumn(nil), a.Starter.Fields...),
		Traversals:       append([]SelectedTraversal(nil), a.Starter.Traversals...),
		Pivots:           append([]SelectedPivot(nil), a.Starter.Pivots...),
	}
	for i := range a.Starter.Pivots {
		a.Starter.Pivots[i].Columns = cloneStrings(a.Starter.Pivots[i].Columns)
	}
	return a
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func nonEmpty(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}
