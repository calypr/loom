package main

// Schema is the JSON input contract consumed by all generators.
type Schema struct {
	Defs map[string]*Definition `json:"$defs"`
}

type Definition struct {
	ID                   string               `json:"$id"`
	Type                 string               `json:"type"`
	Title                string               `json:"title"`
	Description          string               `json:"description"`
	Properties           map[string]*Property `json:"properties"`
	Required             []string             `json:"required"`
	AdditionalProperties bool                 `json:"additionalProperties"`
	Links                []Link               `json:"links"`
}

type BindingStrength string

type Property struct {
	Ref                  string               `json:"$ref"`
	Type                 any                  `json:"type"`
	Items                *Property            `json:"items"`
	Properties           map[string]*Property `json:"properties"`
	Required             []string             `json:"required"`
	Pattern              string               `json:"pattern"`
	MinLength            *int                 `json:"minLength"`
	MaxLength            *int                 `json:"maxLength"`
	Minimum              *float64             `json:"minimum"`
	ExclusiveMinimum     *float64             `json:"exclusiveMinimum"`
	Const                any                  `json:"const"`
	Format               string               `json:"format"`
	ChoiceGroup          string               `json:"one_of_many"`
	ChoiceGroupRequired  bool                 `json:"one_of_many_required"`
	ElementRequired      bool                 `json:"element_required"`
	BindingStrength      BindingStrength      `json:"binding_strength"`
	BindingURI           string               `json:"binding_uri"`
	BindingVersion       string               `json:"binding_version"`
	BindingDescription   string               `json:"binding_description"`
	ReferenceTargetTypes []string             `json:"enum_reference_types"`
	Title                string               `json:"title"`
	Description          string               `json:"description"`
}

type Link struct {
	Rel              string           `json:"rel"`
	TargetHints      TargetHints      `json:"targetHints"`
	TargetSchema     TargetSchema     `json:"targetSchema"`
	TemplatePointers TemplatePointers `json:"templatePointers"`
}

type TargetHints struct {
	Backref      []string `json:"backref"`
	Direction    []string `json:"direction"`
	Multiplicity []string `json:"multiplicity"`
	RegexMatch   []string `json:"regex_match"`
}

type TargetSchema struct {
	Ref string `json:"$ref"`
}
type TemplatePointers struct {
	ID string `json:"id"`
}
