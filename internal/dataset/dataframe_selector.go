package dataset

import (
	"fmt"
	"strings"
)

// DataframeSelector is the exact, versioned identity used by publication,
// releases, federation, GraphQL, and ETL.
type DataframeSelector struct {
	Recipe             string `json:"recipe"`
	TranslationVersion string `json:"translationVersion"`
	Output             string `json:"output"`
}

func (s DataframeSelector) Validate() error {
	for name, value := range map[string]string{
		"recipe": s.Recipe, "translationVersion": s.TranslationVersion, "output": s.Output,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return fmt.Errorf("%s is required and must not have surrounding whitespace", name)
		}
	}
	return nil
}

func (s DataframeSelector) Valid() bool { return s.Validate() == nil }

// Key is a collision-safe stable key for maps and persisted documents.
func (s DataframeSelector) Key() string {
	return fmt.Sprintf("%d:%s%d:%s%d:%s", len(s.Recipe), s.Recipe, len(s.TranslationVersion), s.TranslationVersion, len(s.Output), s.Output)
}
