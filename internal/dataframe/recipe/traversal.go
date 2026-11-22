package recipe

import (
	"encoding/json"
	"strings"
)

type TraversalMatchMode string

const (
	MatchOptional TraversalMatchMode = "OPTIONAL"
	MatchRequired TraversalMatchMode = "REQUIRED"
)

func (m TraversalMatchMode) Valid() bool {
	return m == "" || m == MatchOptional || m == MatchRequired || strings.EqualFold(string(m), string(MatchOptional)) || strings.EqualFold(string(m), string(MatchRequired))
}

func (m TraversalMatchMode) Normalized() TraversalMatchMode {
	switch strings.ToUpper(string(m)) {
	case string(MatchRequired):
		return MatchRequired
	default:
		return MatchOptional
	}
}

type Traversal struct {
	Name               string                  `json:"name"`
	OccurrenceID       string                  `json:"occurrenceId,omitempty"`
	ToResourceType     string                  `json:"toResourceType"`
	Alias              string                  `json:"alias,omitempty"`
	From               *Expression             `json:"from,omitempty"`
	MatchMode          TraversalMatchMode      `json:"matchMode,omitempty"`
	Fields             []Field                 `json:"fields,omitempty"`
	Filters            []Filter                `json:"filters,omitempty"`
	Pivots             []Pivot                 `json:"pivots,omitempty"`
	OwnerRecords       []OwnerRecordProjection `json:"ownerRecords,omitempty"`
	Aggregates         []Aggregate             `json:"aggregates,omitempty"`
	Slices             []RepresentativeSlice   `json:"slices,omitempty"`
	Traversals         []Traversal             `json:"traversals,omitempty"`
	DynamicColumns     []DynamicColumn         `json:"dynamicColumns,omitempty"`
	ExtensionColumns   []ExtensionColumn       `json:"extensionColumns,omitempty"`
	CatalogProjections []CatalogProjection     `json:"catalogProjections,omitempty"`
}

type Expansion struct {
	OwnerOccurrenceID string               `json:"ownerOccurrenceId,omitempty"`
	From              Expression           `json:"from"`
	As                string               `json:"as"`
	Ordinality        string               `json:"ordinality,omitempty"`
	EmptyPolicy       ExpansionEmptyPolicy `json:"emptyPolicy,omitempty"`
}

type ExpansionEmptyPolicy string

const (
	ExpansionError          ExpansionEmptyPolicy = "ERROR"
	ExpansionExclude        ExpansionEmptyPolicy = "EXCLUDE"
	ExpansionPreserveParent ExpansionEmptyPolicy = "PRESERVE_PARENT"
)

func (p ExpansionEmptyPolicy) Valid() bool {
	return p == "" || p == ExpansionError || p == ExpansionExclude || p == ExpansionPreserveParent
}

func (p ExpansionEmptyPolicy) Normalized() ExpansionEmptyPolicy {
	if p == "" {
		return ExpansionExclude
	}
	return p
}

type Identity struct {
	Name      string             `json:"name"`
	Expr      Expression         `json:"expr,omitempty"`
	Expansion *ExpansionIdentity `json:"expansion,omitempty"`
}

func (i Identity) MarshalJSON() ([]byte, error) {
	if i.Expansion != nil && !expressionPresent(i.Expr) {
		return json.Marshal(struct {
			Name      string             `json:"name"`
			Expansion *ExpansionIdentity `json:"expansion"`
		}{Name: i.Name, Expansion: i.Expansion})
	}
	type identityWire Identity
	return json.Marshal(identityWire(i))
}

type ExpansionIdentity struct{}
