package authoringv2

import (
	"fmt"
	"strings"
)

const maxImportedSourcePlanSteps = 4096

// Positional identities grow with traversal depth. Allow the longest path
// representable by a bounded source plan rather than imposing a hop limit.
const maxImportedSourcePlanIDBytes = maxImportedSourcePlanSteps * 32

// ImportedSourcePlan describes editable or inspectable stages of one pinned
// native recipe output. Recipe expressions stay on the server; clients persist
// only stable stage identities and bounded overrides.
type ImportedSourcePlan struct {
	Steps []ImportedSourceStep `json:"steps"`
}

// ImportedSourceStep identifies one source-plan stage. ParentID preserves the
// owning traversal without flattening its fields into unrelated operations.
type ImportedSourceStep struct {
	ID         string `json:"id"`
	ParentID   string `json:"parentId,omitempty"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Summary    string `json:"summary"`
	Enabled    bool   `json:"enabled"`
	Editable   bool   `json:"editable"`
	MaxColumns *int   `json:"maxColumns,omitempty"`
	ValueMode  string `json:"valueMode,omitempty"`
}

// Validate checks the durable shape of the source plan. ApplySourcePlan also
// checks every descriptor and override against the exact pinned recipe output.
func (p ImportedSourcePlan) Validate() error {
	if len(p.Steps) > maxImportedSourcePlanSteps {
		return fmt.Errorf("imported source plan has more than %d steps", maxImportedSourcePlanSteps)
	}

	seen := make(map[string]struct{}, len(p.Steps))
	for index, step := range p.Steps {
		path := fmt.Sprintf("steps[%d]", index)
		if err := validateImportedSourcePlanText(path+".id", step.ID, maxImportedSourcePlanIDBytes); err != nil {
			return err
		}
		if _, duplicate := seen[step.ID]; duplicate {
			return fmt.Errorf("%s.id duplicates %q", path, step.ID)
		}
		if step.ParentID != "" {
			if err := validateImportedSourcePlanText(path+".parentId", step.ParentID, maxImportedSourcePlanIDBytes); err != nil {
				return err
			}
			if _, exists := seen[step.ParentID]; !exists {
				return fmt.Errorf("%s.parentId %q must identify an earlier source-plan step", path, step.ParentID)
			}
		}
		if err := validateImportedSourcePlanText(path+".kind", step.Kind, 64); err != nil {
			return err
		}
		if err := validateImportedSourcePlanText(path+".title", step.Title, 256); err != nil {
			return err
		}
		if err := validateImportedSourcePlanText(path+".summary", step.Summary, 2048); err != nil {
			return err
		}
		if !step.Editable && !step.Enabled {
			return fmt.Errorf("%s.enabled must remain true for a read-only step", path)
		}
		if step.MaxColumns != nil && *step.MaxColumns < 1 {
			return fmt.Errorf("%s.maxColumns must be positive", path)
		}
		if step.ValueMode != "" && !validImportedSourceValueMode(step.ValueMode) {
			return fmt.Errorf("%s.valueMode %q is unsupported", path, step.ValueMode)
		}
		if !step.Editable && (step.MaxColumns != nil || step.ValueMode != "") {
			return fmt.Errorf("%s cannot override a read-only step", path)
		}
		seen[step.ID] = struct{}{}
	}
	return nil
}

func validateImportedSourcePlanText(path, value string, maxLength int) error {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be non-empty and trimmed", path)
	}
	if len(value) > maxLength {
		return fmt.Errorf("%s exceeds %d bytes", path, maxLength)
	}
	return nil
}

func validImportedSourceValueMode(value string) bool {
	switch value {
	case "AUTO", "FIRST", "ALL", "DISTINCT", "VALUE":
		return true
	default:
		return false
	}
}
