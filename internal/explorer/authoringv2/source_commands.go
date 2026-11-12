package authoringv2

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ImportedSourceStepChange edits only the bounded settings of a registered
// source operation. Its expressions and relationships remain server-owned.
type ImportedSourceStepChange struct {
	StepID     string `json:"stepId"`
	Enabled    bool   `json:"enabled"`
	MaxColumns *int   `json:"maxColumns,omitempty"`
	ValueMode  string `json:"valueMode,omitempty"`
}

func (c *ImportedSourceStepChange) UnmarshalJSON(raw []byte) error {
	var wire struct {
		StepID     string `json:"stepId"`
		Enabled    *bool  `json:"enabled"`
		MaxColumns *int   `json:"maxColumns,omitempty"`
		ValueMode  string `json:"valueMode,omitempty"`
	}
	if err := strictDecode(raw, &wire); err != nil {
		return err
	}
	if wire.Enabled == nil {
		return fmt.Errorf("sourceStep.enabled is required")
	}
	*c = ImportedSourceStepChange{StepID: wire.StepID, Enabled: *wire.Enabled, MaxColumns: wire.MaxColumns, ValueMode: wire.ValueMode}
	return c.Validate()
}

func (c ImportedSourceStepChange) Validate() error {
	if c.StepID == "" || c.StepID != strings.TrimSpace(c.StepID) {
		return fmt.Errorf("sourceStep.stepId is required and must be trimmed")
	}
	if c.MaxColumns != nil && *c.MaxColumns < 1 {
		return fmt.Errorf("sourceStep.maxColumns must be positive")
	}
	return nil
}

func (c *Command) ResolveImportedDocument(document Document) error {
	if c == nil || (c.Type != CommandUpdateImportedSourceStep && c.Type != CommandRefreshImportedSource) || document.Output.ID != c.OutputID || document.ImportedRecipe == nil {
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
