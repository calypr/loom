package authoringv2

import (
	"fmt"
	"strings"
)

// AuthoringNetwork is a read model. It is regenerated from the authoritative
// Route/Rows for ordinary documents and the immutable pin plus source overlay
// for imported documents. It must never be saved as a second canvas model.
type AuthoringNetwork struct {
	Owner       string                 `json:"owner"`
	OutputID    string                 `json:"outputId"`
	SourceRows  RowDefinition          `json:"sourceRows"`
	Occurrences []NetworkOccurrence    `json:"occurrences"`
	Bindings    []NetworkColumnBinding `json:"bindings"`
	Operations  []NetworkOperation     `json:"operations"`
}

type NetworkOccurrence struct {
	OccurrenceID       string         `json:"occurrenceId"`
	ParentOccurrenceID string         `json:"parentOccurrenceId,omitempty"`
	SourceStepID       string         `json:"sourceStepId,omitempty"`
	ResourceType       string         `json:"resourceType"`
	Label              string         `json:"label,omitempty"`
	LabelMissing       bool           `json:"labelMissing"`
	CatalogEdgeID      string         `json:"catalogEdgeId,omitempty"`
	Relationship       string         `json:"relationship,omitempty"`
	StorageDirection   string         `json:"storageDirection,omitempty"`
	MatchMode          RouteMatchMode `json:"matchMode,omitempty"`
	Enabled            bool           `json:"enabled"`
	// Optional relationship values preserve identity; REQUIRED can exclude rows.
	PreservesRowIdentity bool `json:"preservesRowIdentity"`
	CanExcludeRows       bool `json:"canExcludeRows"`
}

type NetworkColumnBinding struct {
	// Only persisted stable identities are emitted. Legacy ordinary columns
	// without ColumnID remain in the authoritative document and are omitted
	// from this binding list rather than assigned a fabricated identity.
	ColumnID     string `json:"columnId"`
	OccurrenceID string `json:"occurrenceId,omitempty"`
	SourceStepID string `json:"sourceStepId,omitempty"`
	ValueMode    string `json:"valueMode,omitempty"`
	// UNKNOWN is explicit when an older resolved schema lacks provenance. The
	// base occurrence on imported columns is transport history, not provenance.
	Status string `json:"status"`
}

type NetworkOperation struct {
	OperationID    string                    `json:"operationId"`
	Kind           ConstructionOperationKind `json:"kind"`
	InputStepIDs   []string                  `json:"inputStepIds"`
	AnchorColumnID string                    `json:"anchorColumnId,omitempty"`
}

// ImportedRelationshipOverlay holds only project-owned source edits. Original
// recipe-index step IDs remain in Steps, even after removal. Addition IDs live
// in a separate namespace and never depend on array positions or display text.
type ImportedRelationshipOverlay struct {
	Additions      []ImportedRelationship      `json:"additions,omitempty"`
	Removals       []string                    `json:"removals,omitempty"`
	MatchOverrides []ImportedRelationshipMatch `json:"matchOverrides,omitempty"`
}

type ImportedRelationship struct {
	OccurrenceID       string                      `json:"occurrenceId"`
	ParentOccurrenceID string                      `json:"parentOccurrenceId"`
	CatalogEdgeID      string                      `json:"catalogEdgeId"`
	Relationship       string                      `json:"relationship"`
	ResourceType       string                      `json:"resourceType"`
	StorageDirection   string                      `json:"storageDirection,omitempty"`
	MatchMode          RouteMatchMode              `json:"matchMode"`
	Fields             []ImportedRelationshipField `json:"fields,omitempty"`
}

type ImportedRelationshipField struct {
	CandidateID string `json:"candidateId"`
	ColumnID    string `json:"columnId"`
	Column      string `json:"column"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	ValueMode   string `json:"valueMode"`
}

type ImportedRelationshipMatch struct {
	OccurrenceID string         `json:"occurrenceId"`
	MatchMode    RouteMatchMode `json:"matchMode"`
}

func (o ImportedRelationshipOverlay) Validate() error {
	if len(o.Additions)+len(o.Removals)+len(o.MatchOverrides) > maxImportedSourcePlanSteps {
		return fmt.Errorf("relationship overlay exceeds %d edits", maxImportedSourcePlanSteps)
	}
	seen := map[string]bool{}
	for i, a := range o.Additions {
		if !strings.HasPrefix(a.OccurrenceID, "project_") || len(a.OccurrenceID) <= len("project_") {
			return fmt.Errorf("relationship additions[%d] requires a project_ occurrence identity", i)
		}
		for _, ch := range a.OccurrenceID {
			if !(ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_') {
				return fmt.Errorf("relationship occurrenceId must be an identifier")
			}
		}
		for name, value := range map[string]string{"occurrenceId": a.OccurrenceID, "parentOccurrenceId": a.ParentOccurrenceID, "catalogEdgeId": a.CatalogEdgeID, "relationship": a.Relationship, "resourceType": a.ResourceType} {
			if err := validateImportedSourcePlanText(name, value, 256); err != nil {
				return err
			}
		}
		if seen[a.OccurrenceID] {
			return fmt.Errorf("duplicate relationship addition %q", a.OccurrenceID)
		}
		seen[a.OccurrenceID] = true
		if a.MatchMode != RouteMatchOptional && a.MatchMode != RouteMatchRequired {
			return fmt.Errorf("relationship %q requires OPTIONAL or REQUIRED matchMode", a.OccurrenceID)
		}
		if a.StorageDirection != "" && a.StorageDirection != "INBOUND" && a.StorageDirection != "OUTBOUND" {
			return fmt.Errorf("relationship %q has unsupported storageDirection", a.OccurrenceID)
		}
		fields := map[string]bool{}
		for _, f := range a.Fields {
			for name, value := range map[string]string{"candidateId": f.CandidateID, "columnId": f.ColumnID, "column": f.Column, "fieldPath": f.FieldPath, "label": f.Label} {
				if err := validateImportedSourcePlanText(name, value, 2048); err != nil {
					return err
				}
			}
			if fields[f.ColumnID] {
				return fmt.Errorf("relationship %q duplicates field %q", a.OccurrenceID, f.ColumnID)
			}
			fields[f.ColumnID] = true
			if f.ValueMode != "ALL" && f.ValueMode != "FIRST" && f.ValueMode != "DISTINCT" {
				return fmt.Errorf("relationship field %q requires explicit ALL, FIRST, or DISTINCT value policy", f.ColumnID)
			}
		}
	}
	removed := map[string]bool{}
	for _, id := range o.Removals {
		if err := validateImportedSourcePlanText("removal occurrenceId", id, 256); err != nil {
			return err
		}
		if id == RootOccurrenceID || removed[id] {
			return fmt.Errorf("invalid or duplicate removal %q", id)
		}
		removed[id] = true
	}
	matches := map[string]bool{}
	for _, m := range o.MatchOverrides {
		if err := validateImportedSourcePlanText("match occurrenceId", m.OccurrenceID, 256); err != nil {
			return err
		}
		if matches[m.OccurrenceID] || removed[m.OccurrenceID] || m.OccurrenceID == RootOccurrenceID {
			return fmt.Errorf("invalid or duplicate match override %q", m.OccurrenceID)
		}
		if m.MatchMode != RouteMatchOptional && m.MatchMode != RouteMatchRequired {
			return fmt.Errorf("match override requires OPTIONAL or REQUIRED")
		}
		matches[m.OccurrenceID] = true
	}
	return nil
}

// NetworkForDocument adapts the ordinary route without guessing imported
// provenance. Imported callers use recipeimport.HydrateNetwork with the pin.
func NetworkForDocument(d Document, catalog CatalogSnapshot) (AuthoringNetwork, error) {
	if d.ImportedRecipe != nil {
		return AuthoringNetwork{}, fmt.Errorf("imported network requires the pinned recipe and its source overlay")
	}
	n := NewAuthoringNetwork(d, "DOCUMENT")
	edges := map[string]CatalogEdge{}
	for _, e := range catalog.Edges {
		edges[e.ID] = e
	}
	root, rootFound := catalogNodeForConstructionRoot(catalog, d.Route.ResourceType)
	if !rootFound {
		return AuthoringNetwork{}, fmt.Errorf("route root %q is unavailable or ambiguous", d.Route.ResourceType)
	}
	seen := map[string]bool{}
	var walk func(RouteNode, string, string, bool) error
	walk = func(r RouteNode, parent, nodeID string, requiredPrefix bool) error {
		if seen[r.OccurrenceID] {
			return fmt.Errorf("ambiguous route occurrence %q", r.OccurrenceID)
		}
		seen[r.OccurrenceID] = true
		e := edges[r.CatalogEdgeID]
		n.Occurrences = append(n.Occurrences, NetworkOccurrence{OccurrenceID: r.OccurrenceID, ParentOccurrenceID: parent, ResourceType: r.ResourceType, Label: r.ResourceType, LabelMissing: false, CatalogEdgeID: r.CatalogEdgeID, Relationship: r.Relationship, StorageDirection: e.StorageDirection, MatchMode: r.MatchMode.Normalized(), Enabled: true, PreservesRowIdentity: true, CanExcludeRows: parent != "" && requiredPrefix && r.MatchMode == RouteMatchRequired})
		for _, c := range r.Children {
			edge, err := resolveCatalogRouteEdge(catalog, nodeID, r, c)
			if err != nil {
				return err
			}
			c.CatalogEdgeID = edge.ID
			if err := walk(c, r.OccurrenceID, edge.ToNodeID, requiredPrefix && (parent == "" || r.MatchMode == RouteMatchRequired)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(d.Route, "", root.ID, true); err != nil {
		return AuthoringNetwork{}, err
	}
	for _, c := range d.Columns {
		if c.ColumnID == "" {
			continue
		}
		binding := NetworkColumnBinding{ColumnID: c.ColumnID, OccurrenceID: c.OccurrenceID, Status: "EXACT"}
		if c.Source.Field != nil {
			binding.ValueMode = c.Source.Field.ProjectionMode
		}
		n.Bindings = append(n.Bindings, binding)
	}
	return n, nil
}

// NewAuthoringNetwork initializes ordered operation ownership. Anchor columns
// stay explicit and are never inferred from the currently selected graph node.
func NewAuthoringNetwork(d Document, owner string) AuthoringNetwork {
	n := AuthoringNetwork{Owner: owner, OutputID: d.Output.ID, SourceRows: d.Rows, Occurrences: []NetworkOccurrence{}, Bindings: []NetworkColumnBinding{}, Operations: []NetworkOperation{}}
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
