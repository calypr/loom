package workspace

import (
	"fmt"
	"strings"
)

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
