package authoringnetwork

import (
	"fmt"

	"github.com/calypr/loom/internal/explorer/workspace"
)

type AuthoringNetwork struct {
	Owner           string                  `json:"owner"`
	OutputID        string                  `json:"outputId"`
	SourceRows      workspace.RowDefinition `json:"sourceRows"`
	Occurrences     []NetworkOccurrence     `json:"occurrences"`
	Bindings        []NetworkColumnBinding  `json:"bindings"`
	ProjectedFields []ProjectedField        `json:"projectedFields"`
	Operations      []NetworkOperation      `json:"operations"`
}

type ProjectedField struct {
	ColumnID        string `json:"columnId,omitempty"`
	Column          string `json:"column"`
	Label           string `json:"label"`
	OccurrenceID    string `json:"occurrenceId,omitempty"`
	Owner           string `json:"owner"`
	Removal         string `json:"removal"`
	Status          string `json:"status"`
	SourceStepID    string `json:"sourceStepId,omitempty"`
	SourceFieldID   string `json:"sourceFieldId,omitempty"`
	SourceOutputKey string `json:"sourceOutputKey,omitempty"`
}

const (
	ProjectedFieldOwnerPinned   = "PINNED_SOURCE"
	ProjectedFieldOwnerAuthored = "AUTHORED"
	ProjectedFieldOwnerUnknown  = "UNKNOWN"

	ProjectedFieldRemovalDisablePinned       = "DISABLE_PINNED_FIELD"
	ProjectedFieldRemovalDisablePinnedOutput = "DISABLE_PINNED_OUTPUT"
	ProjectedFieldRemovalColumn              = "REMOVE_COLUMN"
	ProjectedFieldRemovalUnavailable         = "UNAVAILABLE"

	ProjectedFieldStatusExact   = "EXACT"
	ProjectedFieldStatusUnknown = "UNKNOWN"
)

type NetworkOccurrence struct {
	OccurrenceID       string                   `json:"occurrenceId"`
	ParentOccurrenceID string                   `json:"parentOccurrenceId,omitempty"`
	SourceStepID       string                   `json:"sourceStepId,omitempty"`
	ResourceType       string                   `json:"resourceType"`
	Label              string                   `json:"label,omitempty"`
	LabelMissing       bool                     `json:"labelMissing"`
	CatalogEdgeID      string                   `json:"catalogEdgeId,omitempty"`
	Relationship       string                   `json:"relationship,omitempty"`
	StorageDirection   string                   `json:"storageDirection,omitempty"`
	MatchMode          workspace.RouteMatchMode `json:"matchMode,omitempty"`
	Enabled            bool                     `json:"enabled"`

	PreservesRowIdentity bool `json:"preservesRowIdentity"`
	CanExcludeRows       bool `json:"canExcludeRows"`
}

type NetworkColumnBinding struct {
	ColumnID     string `json:"columnId"`
	OccurrenceID string `json:"occurrenceId,omitempty"`
	SourceStepID string `json:"sourceStepId,omitempty"`

	OutputKey string `json:"outputKey,omitempty"`
	ValueMode string `json:"valueMode,omitempty"`

	Status string `json:"status"`
}

type NetworkOperation struct {
	OperationID    string                              `json:"operationId"`
	Kind           workspace.ConstructionOperationKind `json:"kind"`
	InputStepIDs   []string                            `json:"inputStepIds"`
	AnchorColumnID string                              `json:"anchorColumnId,omitempty"`
}

func NetworkForDocument(d workspace.Document, catalog workspace.CatalogSnapshot) (AuthoringNetwork, error) {
	if d.ImportedRecipe != nil {
		return AuthoringNetwork{}, fmt.Errorf("imported network requires the pinned recipe and its source overlay")
	}
	n := NewAuthoringNetwork(d, "DOCUMENT")
	edges := map[string]workspace.CatalogEdge{}
	for _, e := range catalog.Edges {
		edges[e.ID] = e
	}
	root, rootFound := workspace.CatalogNodeForConstructionRoot(catalog, d.Route.ResourceType)
	if !rootFound {
		return AuthoringNetwork{}, fmt.Errorf("route root %q is unavailable or ambiguous", d.Route.ResourceType)
	}
	seen := map[string]bool{}
	var walk func(workspace.RouteNode, string, string, bool) error
	walk = func(r workspace.RouteNode, parent, nodeID string, requiredPrefix bool) error {
		if seen[r.OccurrenceID] {
			return fmt.Errorf("ambiguous route occurrence %q", r.OccurrenceID)
		}
		seen[r.OccurrenceID] = true
		e := edges[r.CatalogEdgeID]
		n.Occurrences = append(n.Occurrences, NetworkOccurrence{OccurrenceID: r.OccurrenceID, ParentOccurrenceID: parent, ResourceType: r.ResourceType, Label: r.ResourceType, LabelMissing: false, CatalogEdgeID: r.CatalogEdgeID, Relationship: r.Relationship, StorageDirection: e.StorageDirection, MatchMode: r.MatchMode.Normalized(), Enabled: true, PreservesRowIdentity: true, CanExcludeRows: parent != "" && requiredPrefix && r.MatchMode == workspace.RouteMatchRequired})
		for _, c := range r.Children {
			edge, err := workspace.ResolveCatalogRouteEdge(catalog, nodeID, r, c)
			if err != nil {
				return err
			}
			c.CatalogEdgeID = edge.ID
			if err := walk(c, r.OccurrenceID, edge.ToNodeID, requiredPrefix && (parent == "" || r.MatchMode == workspace.RouteMatchRequired)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(d.Route, "", root.ID, true); err != nil {
		return AuthoringNetwork{}, err
	}
	for _, c := range d.Columns {
		if c.ColumnID != "" {
			binding := NetworkColumnBinding{ColumnID: c.ColumnID, OccurrenceID: c.OccurrenceID, Status: "EXACT"}
			if c.Source.Field != nil {
				binding.ValueMode = c.Source.Field.ProjectionMode
			}
			n.Bindings = append(n.Bindings, binding)
		}
	}
	n.ProjectedFields = ProjectedFieldsForDocument(d, n)
	return n, nil
}

func ProjectedFieldsForDocument(d workspace.Document, network AuthoringNetwork) []ProjectedField {
	bindings := make(map[string]NetworkColumnBinding, len(network.Bindings))
	for _, binding := range network.Bindings {
		if binding.ColumnID != "" {
			bindings[binding.ColumnID] = binding
		}
	}
	columnIDCounts := make(map[string]int, len(d.Columns))
	for _, column := range d.Columns {
		if column.ColumnID != "" {
			columnIDCounts[column.ColumnID]++
		}
	}
	fields := make([]ProjectedField, 0, len(d.Columns))
	for _, column := range d.Columns {
		field := ProjectedField{
			ColumnID: column.ColumnID, Column: column.Column, Label: column.Label,
			OccurrenceID: column.OccurrenceID, Owner: ProjectedFieldOwnerUnknown,
			Removal: ProjectedFieldRemovalUnavailable, Status: ProjectedFieldStatusUnknown,
		}
		if field.Label == "" {
			field.Label = column.Column
		}
		if column.Source.Kind == workspace.SourceImported {
			field.Owner = ProjectedFieldOwnerPinned
			field.OccurrenceID = ""
		} else if column.Source.Kind != "" {
			field.Owner = ProjectedFieldOwnerAuthored
			if column.ColumnID != "" && column.Column != "" {
				field.Status = ProjectedFieldStatusExact
				field.Removal = ProjectedFieldRemovalColumn
			}
		}
		if column.ColumnID != "" {
			if binding, ok := bindings[column.ColumnID]; ok && binding.Status == "EXACT" {
				field.Status = ProjectedFieldStatusExact
				if binding.OccurrenceID != "" {
					field.OccurrenceID = binding.OccurrenceID
				}
				field.SourceStepID = binding.SourceStepID
			}
		}
		if column.ColumnID == "" || column.Column == "" || columnIDCounts[column.ColumnID] != 1 {
			field.Status = ProjectedFieldStatusUnknown
			field.Removal = ProjectedFieldRemovalUnavailable
		}
		fields = append(fields, field)
	}
	return fields
}

func NewAuthoringNetwork(d workspace.Document, owner string) AuthoringNetwork {
	n := AuthoringNetwork{Owner: owner, OutputID: d.Output.ID, SourceRows: d.Rows, Occurrences: []NetworkOccurrence{}, Bindings: []NetworkColumnBinding{}, ProjectedFields: []ProjectedField{}, Operations: []NetworkOperation{}}
	if d.Construction != nil {
		for _, s := range d.Construction.Steps {
			op := NetworkOperation{OperationID: s.ID, Kind: s.Operation.Kind, InputStepIDs: []string{}}
			for _, input := range s.Inputs {
				if input.StepID != "" {
					op.InputStepIDs = append(op.InputStepIDs, input.StepID)
				}
			}
			if s.Operation.RelatedSource != nil {
				op.AnchorColumnID = s.Operation.RelatedSource.AnchorColumnID
			}
			if s.Operation.RelatedExpand != nil {
				op.AnchorColumnID = s.Operation.RelatedExpand.AnchorColumnID
			}
			if s.Operation.RelatedEligibility != nil {
				op.AnchorColumnID = s.Operation.RelatedEligibility.AnchorColumnID
			}
			n.Operations = append(n.Operations, op)
		}
	}
	return n
}
