package dataset

import (
	"fmt"
	"strings"
)

// DataframeSelector is the exact, versioned identity of one recipe output.
// It is shared by publication, release activation, GraphQL, and ETL.
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

// Valid reports whether the selector contains three non-blank components.
// It is retained as a convenience for callers that only need a boolean.
func (s DataframeSelector) Valid() bool {
	return s.Validate() == nil
}

// Key is a collision-safe stable key for maps and persisted documents.
func (s DataframeSelector) Key() string {
	return fmt.Sprintf("%d:%s%d:%s%d:%s", len(s.Recipe), s.Recipe, len(s.TranslationVersion), s.TranslationVersion, len(s.Output), s.Output)
}
