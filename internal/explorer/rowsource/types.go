package rowsource

type ConstructionDeriveSourceCapability struct {
	Supported  bool                             `json:"supported"`
	StageID    string                           `json:"stageId"`
	ReasonCode string                           `json:"reasonCode,omitempty"`
	Reason     string                           `json:"reason,omitempty"`
	Choices    []ConstructionDeriveSourceChoice `json:"choices"`
}

type ConstructionDeriveSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

type ConstructionDeriveSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

type ConstructionExpandSourceCapability struct {
	Supported  bool                             `json:"supported"`
	StageID    string                           `json:"stageId"`
	ReasonCode string                           `json:"reasonCode,omitempty"`
	Reason     string                           `json:"reason,omitempty"`
	Choices    []ConstructionExpandSourceChoice `json:"choices"`
}

type ConstructionExpandSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

type ConstructionExpandSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

type ConstructionFilterSourceCapability struct {
	Supported  bool                             `json:"supported"`
	StageID    string                           `json:"stageId"`
	ReasonCode string                           `json:"reasonCode,omitempty"`
	Reason     string                           `json:"reason,omitempty"`
	Choices    []ConstructionFilterSourceChoice `json:"choices"`
}

type ConstructionFilterSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

type ConstructionFilterSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}

type ConstructionUnpivotSourceCapability struct {
	Supported  bool                              `json:"supported"`
	StageID    string                            `json:"stageId"`
	ReasonCode string                            `json:"reasonCode,omitempty"`
	Reason     string                            `json:"reason,omitempty"`
	Choices    []ConstructionUnpivotSourceChoice `json:"choices"`
}

type ConstructionUnpivotSourceChoice struct {
	ChoiceID    string `json:"choiceId"`
	FieldPath   string `json:"fieldPath"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	IsPopulated bool   `json:"isPopulated"`
	ColumnID    string `json:"columnId"`
}

type ConstructionUnpivotSourceSelection struct {
	ChoiceID string `json:"choiceId"`
	ColumnID string `json:"columnId"`
}
