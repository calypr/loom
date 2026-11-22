package codedconcept

const (
	ObservationSchemaVersion         = 4
	StatusUnsupportedValueProjection = "UNSUPPORTED_VALUE_PROJECTION"
)

type Completeness string

const (
	Complete   Completeness = "complete"
	Partial    Completeness = "partial"
	Incomplete Completeness = "incomplete"
)

type Observation struct {
	SchemaVersion          int          `json:"schema_version"`
	Source                 Source       `json:"source"`
	Key                    Key          `json:"key"`
	Value                  Value        `json:"value"`
	OwningScope            string       `json:"owning_scope,omitempty"`
	ExtensionURLPath       []string     `json:"extension_url_path,omitempty"`
	ChoiceArm              string       `json:"choice_arm,omitempty"`
	LogicalType            string       `json:"logical_type,omitempty"`
	ObservedUnits          []string     `json:"observed_units,omitempty"`
	ObservedUnitsTruncated bool         `json:"observed_units_truncated,omitempty"`
	Completeness           Completeness `json:"completeness"`
	Status                 string       `json:"status,omitempty"`
	Population             int64        `json:"population"`
	Examples               []string     `json:"examples,omitempty"`
	ExamplesTruncated      bool         `json:"examples_truncated"`
	RuleHint               string       `json:"rule_hint,omitempty"`
	RuleVersion            string       `json:"rule_version,omitempty"`
}

type Source struct {
	Canonical string `json:"canonical,omitempty"`
	Type      string `json:"type,omitempty"`
	Profile   string `json:"profile,omitempty"`
	Path      string `json:"path,omitempty"`
}

type Key struct {
	Selector string `json:"selector,omitempty"`
	System   string `json:"system,omitempty"`
	Version  string `json:"version,omitempty"`
	Code     string `json:"code,omitempty"`
	Display  string `json:"display,omitempty"`
}

type Value struct {
	Selector              string   `json:"selector,omitempty"`
	Type                  string   `json:"type,omitempty"`
	FallbackSelectors     []string `json:"fallback_selectors,omitempty"`
	FallbackOnBlankString bool     `json:"fallback_on_blank_string,omitempty"`
}
