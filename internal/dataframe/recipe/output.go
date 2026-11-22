package recipe

import (
	"github.com/calypr/loom/internal/dataframe/columntransform"
)

type Output struct {
	Name                  string                  `json:"name"`
	RootResourceType      string                  `json:"rootResourceType"`
	RootOccurrenceID      string                  `json:"rootOccurrenceId,omitempty"`
	RowGrain              string                  `json:"rowGrain"`
	RootColumnNaming      RootColumnNaming        `json:"rootColumnNaming,omitempty"`
	TraversalColumnNaming TraversalColumnNaming   `json:"traversalColumnNaming,omitempty"`
	Fields                []Field                 `json:"fields,omitempty"`
	Filters               []Filter                `json:"filters,omitempty"`
	Pivots                []Pivot                 `json:"pivots,omitempty"`
	OwnerRecords          []OwnerRecordProjection `json:"ownerRecords,omitempty"`
	Aggregates            []Aggregate             `json:"aggregates,omitempty"`
	Slices                []RepresentativeSlice   `json:"slices,omitempty"`
	Traversals            []Traversal             `json:"traversals,omitempty"`
	ArchiveRetiredReshape *archivedTableReshape   `json:"tableReshape,omitempty"`
	Expand                *Expansion              `json:"expand,omitempty"`
	GroupRows             *GroupRows              `json:"groupRows,omitempty"`
	Identity              *Identity               `json:"identity,omitempty"`
	DynamicColumns        []DynamicColumn         `json:"dynamicColumns,omitempty"`
	ExtensionColumns      []ExtensionColumn       `json:"extensionColumns,omitempty"`
	CatalogProjections    []CatalogProjection     `json:"catalogProjections,omitempty"`
	ColumnTransformations []ColumnTransformation  `json:"columnTransformations,omitempty"`
	DerivedColumns        []DerivedColumn         `json:"derivedColumns,omitempty"`

	Construction    *Construction         `json:"construction,omitempty"`
	Population      *PopulationConstraint `json:"population,omitempty"`
	CollisionPolicy string                `json:"collisionPolicy,omitempty"`
}

type ColumnTransformation struct {
	Column         string                              `json:"column"`
	Transformation columntransform.ValueTransformation `json:"transformation"`
}

type GroupRows struct {
	RevisionID             string                `json:"revisionId"`
	UnassignedMemberPolicy string                `json:"unassignedMemberPolicy"`
	AfterStepID            string                `json:"afterStepId,omitempty"`
	RowValues              []GroupRowValuePolicy `json:"rowValues,omitempty"`
}

type GroupRowValuePolicy struct {
	ColumnID string                     `json:"columnId"`
	Policy   ConstructionRowValuePolicy `json:"policy"`
}

type PopulationConstraint struct {
	SelectionRevisionID string                `json:"selectionRevisionId"`
	MembershipDigest    string                `json:"membershipDigest"`
	MemberCount         int64                 `json:"memberCount"`
	ResourceType        string                `json:"resourceType"`
	Route               []PopulationRouteStep `json:"route,omitempty"`
}

type PopulationRouteStep struct {
	ResourceType     string `json:"resourceType"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection,omitempty"`
}
