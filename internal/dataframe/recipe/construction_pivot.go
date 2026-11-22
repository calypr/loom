package recipe

import (
	"strings"
)

type ConstructionPivot struct {
	ConstructionID   string                      `json:"constructionId"`
	GroupKeyIDs      []string                    `json:"groupKeyIds"`
	CategoryColumnID string                      `json:"categoryColumnId"`
	ValueColumnID    string                      `json:"valueColumnId"`
	Categories       []ConstructionPivotCategory `json:"categories"`

	Discovery              *ConstructionPivotDiscovery `json:"discovery,omitempty"`
	DuplicatePolicy        PivotDuplicatePolicy        `json:"duplicatePolicy"`
	MissingCellPolicy      PivotMissingCellPolicy      `json:"missingCellPolicy"`
	UnlistedCategoryPolicy PivotUnlistedCategoryPolicy `json:"unlistedCategoryPolicy"`
}

type ConstructionPivotDiscovery struct {
	MaxCategories int `json:"maxCategories"`
}

type ConstructionPivotCategory struct {
	Key            TableScalar `json:"key"`
	OutputColumnID string      `json:"outputColumnId"`
}

type ConstructionCodedPivot struct {
	ConstructionID    string                           `json:"constructionId"`
	Source            ConstructionCodedPivotSource     `json:"source"`
	Categories        []ConstructionCodedPivotCategory `json:"categories"`
	DuplicatePolicy   PivotDuplicatePolicy             `json:"duplicatePolicy"`
	MissingCellPolicy PivotMissingCellPolicy           `json:"missingCellPolicy"`
}

type ConstructionCodedPivotSource struct {
	BindingID             string                         `json:"bindingId"`
	ResourceType          string                         `json:"resourceType"`
	SourcePath            string                         `json:"sourcePath"`
	SourceCanonical       string                         `json:"sourceCanonical,omitempty"`
	SourceProfile         string                         `json:"sourceProfile,omitempty"`
	OwningScope           string                         `json:"owningScope,omitempty"`
	KeyPath               string                         `json:"keyPath"`
	ValuePath             string                         `json:"valuePath"`
	ValueFallbacks        []string                       `json:"valueFallbacks,omitempty"`
	FallbackOnBlankString bool                           `json:"fallbackOnBlankString,omitempty"`
	ChoiceArms            []string                       `json:"choiceArms,omitempty"`
	LogicalType           string                         `json:"logicalType"`
	RuleVersion           string                         `json:"ruleVersion"`
	SchemaVersion         int                            `json:"schemaVersion"`
	CandidateID           string                         `json:"candidateId"`
	NodeID                string                         `json:"nodeId"`
	FieldPath             string                         `json:"fieldPath"`
	Route                 []ConstructionRelatedRouteStep `json:"route"`
}

type ConstructionCodedPivotCategory struct {
	System         string `json:"system"`
	Code           string `json:"code"`
	OutputColumnID string `json:"outputColumnId"`
}

func constructionPivotScalarKind(typeName string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(typeName)) {
	case "string", "code", "uuid", "date", "date_time", "date-time", "datetime":
		return "STRING", true
	case "integer":
		return "INTEGER", true
	case "decimal":
		return "DECIMAL", true
	case "boolean":
		return "BOOLEAN", true
	default:
		return "", false
	}
}

func constructionPivotTypeIsScalar(typeName string) (scalar, known bool) {
	normalized := strings.ToLower(strings.TrimSpace(typeName))
	if normalized == "" || normalized == "infer" {
		return true, false
	}
	if _, ok := constructionPivotScalarKind(normalized); ok {
		return true, true
	}
	switch normalized {
	case "array", "object", "record", "json", "list", "map":
		return false, true
	default:
		return true, false
	}
}

func validPivotDuplicatePolicy(policy PivotDuplicatePolicy) bool {
	return policy == PivotDuplicateError || policy == PivotDuplicateSum || policy == PivotDuplicateMin || policy == PivotDuplicateMax
}

func validPivotMissingPolicy(policy PivotMissingCellPolicy) bool {
	return policy == PivotMissingCellNull || policy == PivotMissingCellError
}

func validPivotUnlistedPolicy(policy PivotUnlistedCategoryPolicy) bool {
	return policy == PivotUnlistedCategoryError || policy == PivotUnlistedCategoryExcludeWithEvidence
}
