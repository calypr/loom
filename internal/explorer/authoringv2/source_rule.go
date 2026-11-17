package authoringv2

import "fmt"

type SourceRuleCreate struct {
	Kind         string                    `json:"kind"`
	OccurrenceID string                    `json:"occurrenceId,omitempty"`
	Parameters   *ImportedSourceParameters `json:"parameters"`
}

func (c *SourceRuleCreate) UnmarshalJSON(raw []byte) error {
	type wire SourceRuleCreate
	var decoded wire
	if err := strictDecode(raw, &decoded); err != nil {
		return err
	}
	if err := SourceRuleCreate(decoded).Validate(); err != nil {
		return err
	}
	*c = SourceRuleCreate(decoded)
	return nil
}

func (c SourceRuleCreate) Validate() error {
	switch c.Kind {
	case "DYNAMIC_COLUMN", "EXTENSION_COLUMN", "CATALOG_PROJECTION":
	default:
		return fmt.Errorf("unsupported source rule kind %q", c.Kind)
	}
	if c.OccurrenceID != "" {
		if err := validateImportedSourcePlanText("occurrenceId", c.OccurrenceID, maxImportedSourcePlanIDBytes); err != nil {
			return err
		}
	}
	if c.Parameters == nil {
		return fmt.Errorf("source rule requires parameters")
	}
	return c.Parameters.Validate(c.Kind)
}
