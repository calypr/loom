package authoringv2

import (
	"encoding/json"
	"fmt"
	"strings"
)

type ImportedSourceStepChange struct {
	Parameters *ImportedSourceParameters `json:"parameters,omitempty"`
	StepID     string                    `json:"stepId"`
	Remove     bool                      `json:"remove,omitempty"`
	MaxColumns *int                      `json:"maxColumns,omitempty"`
	ValueMode  string                    `json:"valueMode,omitempty"`
}

func (c *ImportedSourceStepChange) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Parameters *ImportedSourceParameters `json:"parameters,omitempty"`
		StepID     string                    `json:"stepId"`
		Remove     *bool                     `json:"remove,omitempty"`
		MaxColumns *int                      `json:"maxColumns,omitempty"`
		ValueMode  string                    `json:"valueMode,omitempty"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	if wire.Remove != nil && !*wire.Remove {
		return fmt.Errorf("sourceStep.remove must be true when provided")
	}
	*c = ImportedSourceStepChange{Parameters: wire.Parameters, StepID: wire.StepID, Remove: wire.Remove != nil, MaxColumns: wire.MaxColumns, ValueMode: wire.ValueMode}
	return c.Validate()
}

func (c ImportedSourceStepChange) Validate() error {
	if c.Remove && (c.Parameters != nil || c.MaxColumns != nil || c.ValueMode != "") {
		return fmt.Errorf("sourceStep.remove cannot be combined with source step settings")
	}
	if err := c.Parameters.Validate(""); err != nil {
		return err
	}
	if c.StepID == "" || c.StepID != strings.TrimSpace(c.StepID) {
		return fmt.Errorf("sourceStep.stepId is required and must be trimmed")
	}
	if c.MaxColumns != nil && *c.MaxColumns < 1 {
		return fmt.Errorf("sourceStep.maxColumns must be positive")
	}
	return nil
}

func IsImportedSourceCommand(kind string) bool {
	return kind == CommandUpdateImportedSourceStep || kind == CommandRefreshImportedSource || kind == CommandApplyImportedRelationshipProposal
}

type ImportedRelationshipChange struct {
	Action             string         `json:"action"`
	OccurrenceID       string         `json:"occurrenceId,omitempty"`
	ParentOccurrenceID string         `json:"parentOccurrenceId,omitempty"`
	EdgeID             string         `json:"edgeId,omitempty"`
	MatchMode          RouteMatchMode `json:"matchMode,omitempty"`
}

func (c ImportedRelationshipChange) Validate() error {
	for _, value := range []string{c.OccurrenceID, c.ParentOccurrenceID, c.EdgeID} {
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("relationship identities must be trimmed")
		}
	}
	switch c.Action {
	case "ADD":
		if c.OccurrenceID != "" || c.ParentOccurrenceID == "" || c.EdgeID == "" {
			return fmt.Errorf("ADD requires parentOccurrenceId and edgeId, without occurrenceId")
		}
	case "MATCH":
		if c.OccurrenceID == "" || c.ParentOccurrenceID != "" || c.EdgeID != "" {
			return fmt.Errorf("MATCH requires only occurrenceId and matchMode")
		}
	case "REMOVE":
		if c.OccurrenceID == "" || c.ParentOccurrenceID != "" || c.EdgeID != "" || c.MatchMode != "" {
			return fmt.Errorf("REMOVE requires only occurrenceId")
		}
		return nil
	default:
		return fmt.Errorf("relationship action must be ADD, MATCH, or REMOVE")
	}
	if c.MatchMode != RouteMatchOptional && c.MatchMode != RouteMatchRequired {
		return fmt.Errorf("matchMode must be OPTIONAL or REQUIRED")
	}
	for _, value := range []string{c.OccurrenceID, c.ParentOccurrenceID, c.EdgeID} {
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("relationship identities must be trimmed")
		}
	}
	return nil
}

func (c *ImportedRelationshipChange) UnmarshalJSON(raw []byte) error {
	type wire ImportedRelationshipChange
	var decoded wire
	if err := strictDecode(raw, &decoded); err != nil {
		return err
	}
	*c = ImportedRelationshipChange(decoded)
	return c.Validate()
}

func ValidateImportedSourceStepBatch(changes []ImportedSourceStepChange) error {
	if len(changes) < 1 || len(changes) > 128 {
		return fmt.Errorf("sourceSteps requires between 1 and 128 existing source steps")
	}
	seen := map[string]bool{}
	for _, change := range changes {
		if err := change.Validate(); err != nil {
			return err
		}
		if seen[change.StepID] {
			return fmt.Errorf("sourceSteps repeats stepId %q", change.StepID)
		}
		seen[change.StepID] = true
	}
	return nil
}

func (c *Command) ResolveImportedDocument(document Document) error {
	if c == nil || !IsImportedSourceCommand(c.Type) || document.Output.ID != c.OutputID || document.ImportedRecipe == nil {
		return fmt.Errorf("source change requires a matching imported document")
	}
	if err := document.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return err
	}
	var cloned Document
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return err
	}
	c.resolvedImportedDocument = &cloned
	return nil
}

func (c *Command) ResolveProjectedFieldRemovalDocument(document Document) error {
	if c == nil || c.Type != CommandRemoveProjectedField || document.Output.ID != c.OutputID || document.ImportedRecipe == nil {
		return fmt.Errorf("projected field removal requires a matching imported document")
	}
	if err := document.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return err
	}
	var cloned Document
	if err := json.Unmarshal(raw, &cloned); err != nil {
		return err
	}
	c.resolvedImportedDocument = &cloned
	return nil
}
