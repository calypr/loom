package recipe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

type ConstructionGroup struct {
	ConstructionID   string                            `json:"constructionId"`
	MissingKeyPolicy ConstructionGroupMissingKeyPolicy `json:"missingKeyPolicy"`
	Keys             []ConstructionGroupKey            `json:"keys,omitempty"`
	Aggregates       []ConstructionGroupAggregate      `json:"aggregates,omitempty"`
}

type ConstructionCodedGroup struct {
	ConstructionID                    string                            `json:"constructionId"`
	Source                            ConstructionCodedGroupSource      `json:"source"`
	MissingKeyPolicy                  ConstructionGroupMissingKeyPolicy `json:"missingKeyPolicy"`
	SystemOutputColumnID              string                            `json:"systemOutputColumnId"`
	VersionOutputColumnID             string                            `json:"versionOutputColumnId"`
	CodeOutputColumnID                string                            `json:"codeOutputColumnId"`
	DistinctSourceCountOutputColumnID string                            `json:"distinctSourceCountOutputColumnId"`
}

type ConstructionCodedGroupSource struct {
	OccurrenceID string                         `json:"occurrenceId"`
	ResourceType string                         `json:"resourceType"`
	CodingPath   string                         `json:"codingPath"`
	FHIRType     string                         `json:"fhirType"`
	Cardinality  string                         `json:"cardinality"`
	Shape        string                         `json:"shape"`
	Route        []ConstructionRelatedRouteStep `json:"route"`
}

type ConstructionGroupMissingKeyPolicy string

const (
	ConstructionGroupMissingKeyGroup   ConstructionGroupMissingKeyPolicy = "GROUP"
	ConstructionGroupMissingKeyExclude ConstructionGroupMissingKeyPolicy = "EXCLUDE"
	ConstructionGroupMissingKeyError   ConstructionGroupMissingKeyPolicy = "ERROR"
)

func (policy ConstructionGroupMissingKeyPolicy) Normalized() ConstructionGroupMissingKeyPolicy {
	if policy == "" {
		return ConstructionGroupMissingKeyGroup
	}
	return policy
}

func (policy ConstructionGroupMissingKeyPolicy) Valid() bool {
	switch policy.Normalized() {
	case ConstructionGroupMissingKeyGroup, ConstructionGroupMissingKeyExclude, ConstructionGroupMissingKeyError:
		return true
	default:
		return false
	}
}

func (group ConstructionGroup) MarshalJSON() ([]byte, error) {
	type wire ConstructionGroup
	group.MissingKeyPolicy = group.MissingKeyPolicy.Normalized()
	return json.Marshal(wire(group))
}

func (group *ConstructionGroup) UnmarshalJSON(raw []byte) error {
	type wire ConstructionGroup
	var value wire
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*group = ConstructionGroup(value)
	group.MissingKeyPolicy = group.MissingKeyPolicy.Normalized()
	return nil
}

type ConstructionGroupKey struct {
	InputColumnID  string                        `json:"inputColumnId,omitempty"`
	OutputColumnID string                        `json:"outputColumnId"`
	FieldSource    *ConstructionGroupFieldSource `json:"fieldSource,omitempty"`

	SemanticCategory *ConstructionGroupSemanticCategory `json:"semanticCategory,omitempty"`
}

type ConstructionGroupFieldSource struct {
	Kind          string                             `json:"kind"`
	ChoiceID      string                             `json:"choiceId,omitempty"`
	ValueKind     string                             `json:"valueKind,omitempty"`
	SourceStageID string                             `json:"sourceStageId,omitempty"`
	SourceShapeID string                             `json:"sourceShapeId,omitempty"`
	Input         *ConstructionGroupFieldSourceInput `json:"input,omitempty"`

	SourceRole        string `json:"sourceRole,omitempty"`
	OccurrenceID      string `json:"occurrenceId,omitempty"`
	OwnerResourceType string `json:"ownerResourceType,omitempty"`
	ResourceType      string `json:"resourceType,omitempty"`
	CandidateID       string `json:"candidateId,omitempty"`
	CanonicalPath     string `json:"canonicalPath,omitempty"`
	BoundarySelector  string `json:"boundarySelector,omitempty"`
}

func (source *ConstructionGroupFieldSource) UnmarshalJSON(raw []byte) error {
	type wire ConstructionGroupFieldSource
	var decoded wire
	if err := decodeStrictGroupFieldSourceJSON(raw, &decoded); err != nil {
		return err
	}
	*source = ConstructionGroupFieldSource(decoded)
	return nil
}

type ConstructionGroupFieldSourceInput struct {
	Kind              string                                         `json:"kind"`
	CurrentStage      *ConstructionGroupCurrentStageFieldSource      `json:"currentStage,omitempty"`
	RelatedOccurrence *ConstructionGroupRelatedOccurrenceFieldSource `json:"relatedOccurrence,omitempty"`
}

func (input *ConstructionGroupFieldSourceInput) UnmarshalJSON(raw []byte) error {
	type wire ConstructionGroupFieldSourceInput
	var decoded wire
	if err := decodeStrictGroupFieldSourceJSON(raw, &decoded); err != nil {
		return err
	}
	*input = ConstructionGroupFieldSourceInput(decoded)
	return nil
}

func decodeStrictGroupFieldSourceJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("fieldSource contains trailing JSON")
		}
		return fmt.Errorf("fieldSource contains invalid trailing JSON: %w", err)
	}
	return nil
}

type ConstructionGroupCurrentStageFieldSource struct {
	SourceRole        string `json:"sourceRole"`
	OccurrenceID      string `json:"occurrenceId"`
	OwnerResourceType string `json:"ownerResourceType"`
	ResourceType      string `json:"resourceType"`
	CandidateID       string `json:"candidateId,omitempty"`
	CanonicalPath     string `json:"canonicalPath"`
	BoundarySelector  string `json:"boundarySelector,omitempty"`
}

type ConstructionGroupRelatedOccurrenceFieldSource struct {
	RouteID            string                              `json:"-"`
	AnchorOccurrenceID string                              `json:"anchorOccurrenceId"`
	AnchorNodeID       string                              `json:"anchorNodeId"`
	AnchorResourceType string                              `json:"anchorResourceType"`
	Route              []ConstructionGroupRelatedRouteStep `json:"route"`
	TargetOccurrenceID string                              `json:"targetOccurrenceId"`
	TargetNodeID       string                              `json:"targetNodeId"`
	TargetResourceType string                              `json:"targetResourceType"`
	ResourceType       string                              `json:"resourceType"`
	CandidateID        string                              `json:"candidateId,omitempty"`
	CanonicalPath      string                              `json:"canonicalPath,omitempty"`
	EmptyPolicy        ExpansionEmptyPolicy                `json:"emptyPolicy"`
	Multiplicity       string                              `json:"multiplicity"`
	CodingSelector     string                              `json:"codingSelector,omitempty"`
	CodingEmptyPolicy  ConstructionGroupCodingEmptyPolicy  `json:"codingEmptyPolicy,omitempty"`
	OrdinalSemantics   string                              `json:"ordinalSemantics,omitempty"`
}

type ConstructionGroupRelatedRouteStep struct {
	StepIndex        int    `json:"stepIndex"`
	FromOccurrenceID string `json:"fromOccurrenceId"`
	FromNodeID       string `json:"fromNodeId"`
	FromResourceType string `json:"fromResourceType"`
	EdgeID           string `json:"edgeId"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection"`
	MatchMode        string `json:"matchMode"`
	ToOccurrenceID   string `json:"toOccurrenceId"`
	ToNodeID         string `json:"toNodeId"`
	ToResourceType   string `json:"toResourceType"`
}

const ConstructionGroupRelatedMultiplicityDistinctTargetPerInput = "DISTINCT_TARGET_PER_INPUT"
const ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition = "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY"

type ConstructionGroupCodingEmptyPolicy string

const (
	ConstructionGroupCodingEmptyPolicyGroup   ConstructionGroupCodingEmptyPolicy = "GROUP"
	ConstructionGroupCodingEmptyPolicyExclude ConstructionGroupCodingEmptyPolicy = "EXCLUDE"
	ConstructionGroupCodingEmptyPolicyError   ConstructionGroupCodingEmptyPolicy = "ERROR"
)

type ConstructionGroupSemanticCategory struct {
	Kind         string `json:"kind"`
	BindingID    string `json:"bindingId,omitempty"`
	RuleVersion  string `json:"ruleVersion,omitempty"`
	FamilyID     string `json:"familyId,omitempty"`
	SourceKind   string `json:"sourceKind,omitempty"`
	System       string `json:"system,omitempty"`
	Version      string `json:"version,omitempty"`
	ResourceType string `json:"resourceType,omitempty"`
	CodingPath   string `json:"codingPath,omitempty"`
}

type ConstructionGroupAggregate struct {
	Operation      ConstructionGroupAggregateOp `json:"operation"`
	InputColumnID  string                       `json:"inputColumnId,omitempty"`
	OutputColumnID string                       `json:"outputColumnId"`
}

type ConstructionGroupAggregateOp string

const (
	ConstructionGroupCountRows     ConstructionGroupAggregateOp = "COUNT_ROWS"
	ConstructionGroupCountNonNull  ConstructionGroupAggregateOp = "COUNT_NON_NULL"
	ConstructionGroupCountDistinct ConstructionGroupAggregateOp = "COUNT_DISTINCT"
	ConstructionGroupSum           ConstructionGroupAggregateOp = "SUM"
	ConstructionGroupMin           ConstructionGroupAggregateOp = "MIN"
	ConstructionGroupMax           ConstructionGroupAggregateOp = "MAX"
	ConstructionGroupMean          ConstructionGroupAggregateOp = "MEAN"
)

func constructionFirstGroupHasResolvedFieldSource(step ConstructionStep) bool {
	if step.Operation.Kind != ConstructionGroupOp || step.Operation.Group == nil {
		return false
	}
	isCompilerBoundSource := func(source *ConstructionGroupFieldSource) bool {
		return source != nil && source.Kind == "BINDING" &&
			source.SourceStageID == ConstructionSourceProjectionID && source.SourceShapeID != "" &&
			validateConstructionGroupFieldSourceBinding(*source, "fieldSource", true) == nil
	}
	for _, key := range step.Operation.Group.Keys {
		if isCompilerBoundSource(key.FieldSource) {
			return true
		}
	}
	for _, rowValue := range step.RowValues {
		if isCompilerBoundSource(rowValue.FieldSource) {
			return true
		}
	}
	return false
}

func validateConstructionGroupScalarFieldSource(source ConstructionGroupFieldSource, path string) error {
	if source.ValueKind != "SCALAR" {
		return fmt.Errorf("%s must bind a scalar fieldSource", path)
	}
	return validateConstructionGroupFieldSourceBinding(source, path, false)
}

func validateConstructionGroupFieldSourceBinding(source ConstructionGroupFieldSource, path string, allowCodingIdentity bool) error {
	if source.Kind != "BINDING" || source.ChoiceID != "" ||
		!validConstructionColumnID(source.SourceStageID) || !validConstructionColumnID(source.SourceShapeID) {
		return fmt.Errorf("%s is not a resolved BINDING", path)
	}
	input, err := normalizedConstructionGroupFieldSourceInput(source, path)
	if err != nil {
		return err
	}
	switch source.ValueKind {
	case "SCALAR":
	case "CODING_IDENTITY":
		if !allowCodingIdentity {
			return fmt.Errorf("%s CODING_IDENTITY is not supported for this source", path)
		}
	default:
		return fmt.Errorf("%s has unsupported valueKind %q", path, source.ValueKind)
	}
	switch input.Kind {
	case "CURRENT_STAGE":
		current := input.CurrentStage
		if current == nil || input.RelatedOccurrence != nil {
			return fmt.Errorf("%s input CURRENT_STAGE requires only currentStage", path)
		}
		if source.ValueKind == "SCALAR" {
			if (current.SourceRole != "ROOT" && current.SourceRole != "OWNER" && current.SourceRole != "ANCESTOR" && current.SourceRole != "CURRENT_ITEM") ||
				!validConstructionColumnID(current.OccurrenceID) || !validConstructionColumnID(current.OwnerResourceType) ||
				!validConstructionColumnID(current.ResourceType) || !validConstructionColumnID(current.CandidateID) ||
				!validConstructionColumnID(current.CanonicalPath) {
				return fmt.Errorf("%s is not a resolved current-stage scalar BINDING", path)
			}
			if current.SourceRole == "ANCESTOR" {
				if !validConstructionColumnID(current.BoundarySelector) {
					return fmt.Errorf("%s ANCESTOR requires an exact boundarySelector", path)
				}
			} else if current.BoundarySelector != "" {
				return fmt.Errorf("%s boundarySelector is only valid for ANCESTOR", path)
			}
			return nil
		}
		if current.SourceRole != "CURRENT_ITEM" || !validConstructionColumnID(current.OccurrenceID) ||
			!validConstructionColumnID(current.OwnerResourceType) || current.ResourceType != "Coding" ||
			current.CandidateID != "" || !validConstructionColumnID(current.CanonicalPath) || current.BoundarySelector != "" {
			return fmt.Errorf("%s CODING_IDENTITY must bind the exact current Coding item without candidateId", path)
		}
		return nil
	case "RELATED_OCCURRENCE":
		related := input.RelatedOccurrence
		if related == nil || input.CurrentStage != nil {
			return fmt.Errorf("%s input RELATED_OCCURRENCE requires only relatedOccurrence", path)
		}
		return validateConstructionGroupRelatedOccurrenceSource(source.ValueKind, *related, path, allowCodingIdentity)
	default:
		return fmt.Errorf("%s input kind %q is unsupported", path, input.Kind)
	}
}

func normalizedConstructionGroupFieldSourceInput(source ConstructionGroupFieldSource, path string) (ConstructionGroupFieldSourceInput, error) {
	legacy := ConstructionGroupCurrentStageFieldSource{
		SourceRole: source.SourceRole, OccurrenceID: source.OccurrenceID,
		OwnerResourceType: source.OwnerResourceType, ResourceType: source.ResourceType,
		CandidateID: source.CandidateID, CanonicalPath: source.CanonicalPath,
		BoundarySelector: source.BoundarySelector,
	}
	hasLegacy := source.SourceRole != "" || source.OccurrenceID != "" || source.OwnerResourceType != "" ||
		source.ResourceType != "" || source.CandidateID != "" || source.CanonicalPath != "" || source.BoundarySelector != ""
	if source.Input != nil {
		if hasLegacy {
			return ConstructionGroupFieldSourceInput{}, fmt.Errorf("%s cannot combine nested input with legacy flat source fields", path)
		}
		if source.Input.Kind == "CURRENT_STAGE" && (source.Input.CurrentStage == nil || source.Input.RelatedOccurrence != nil) ||
			source.Input.Kind == "RELATED_OCCURRENCE" && (source.Input.RelatedOccurrence == nil || source.Input.CurrentStage != nil) ||
			source.Input.Kind != "CURRENT_STAGE" && source.Input.Kind != "RELATED_OCCURRENCE" {
			return ConstructionGroupFieldSourceInput{}, fmt.Errorf("%s input must match exactly one tagged source variant", path)
		}
		return *source.Input, nil
	}
	if !hasLegacy {
		return ConstructionGroupFieldSourceInput{}, fmt.Errorf("%s requires an input source variant", path)
	}
	return ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", CurrentStage: &legacy}, nil
}

func validateConstructionGroupRelatedOccurrenceSource(valueKind string, source ConstructionGroupRelatedOccurrenceFieldSource, path string, allowCodingIdentity bool) error {
	for name, value := range map[string]string{
		"anchorOccurrenceId": source.AnchorOccurrenceID, "anchorNodeId": source.AnchorNodeID,
		"anchorResourceType": source.AnchorResourceType, "targetOccurrenceId": source.TargetOccurrenceID,
		"targetNodeId": source.TargetNodeID, "targetResourceType": source.TargetResourceType,
		"resourceType": source.ResourceType,
	} {
		if !validConstructionColumnID(value) {
			return fmt.Errorf("%s relatedOccurrence.%s is empty or invalid", path, name)
		}
	}
	if len(source.Route) == 0 || source.Multiplicity != ConstructionGroupRelatedMultiplicityDistinctTargetPerInput {
		return fmt.Errorf("%s relatedOccurrence requires an exact route and DISTINCT_TARGET_PER_INPUT multiplicity", path)
	}
	if source.EmptyPolicy != ExpansionPreserveParent && source.EmptyPolicy != ExpansionExclude && source.EmptyPolicy != ExpansionError {
		return fmt.Errorf("%s relatedOccurrence emptyPolicy is required and unsupported", path)
	}
	fromOccurrence, fromNode, fromResource := source.AnchorOccurrenceID, source.AnchorNodeID, source.AnchorResourceType
	for index, hop := range source.Route {
		if hop.StepIndex != index || hop.FromOccurrenceID != fromOccurrence || hop.FromNodeID != fromNode || hop.FromResourceType != fromResource ||
			!validConstructionColumnID(hop.EdgeID) || !validConstructionColumnID(hop.Relationship) ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") ||
			!validConstructionColumnID(hop.ToOccurrenceID) || !validConstructionColumnID(hop.ToNodeID) || !validConstructionColumnID(hop.ToResourceType) {
			return fmt.Errorf("%s relatedOccurrence route[%d] is incomplete or discontinuous", path, index)
		}
		fromOccurrence, fromNode, fromResource = hop.ToOccurrenceID, hop.ToNodeID, hop.ToResourceType
	}
	if fromOccurrence != source.TargetOccurrenceID || fromNode != source.TargetNodeID || fromResource != source.TargetResourceType {
		return fmt.Errorf("%s relatedOccurrence route does not end at its exact target occurrence", path)
	}
	if valueKind == "SCALAR" {
		if !validConstructionColumnID(source.CandidateID) || !validConstructionColumnID(source.CanonicalPath) ||
			source.CodingSelector != "" || source.CodingEmptyPolicy != "" || source.OrdinalSemantics != "" {
			return fmt.Errorf("%s relatedOccurrence SCALAR requires candidateId/path and no Coding ordinal facts", path)
		}
		return nil
	}
	if !allowCodingIdentity || valueKind != "CODING_IDENTITY" || source.CandidateID != "" || source.CanonicalPath != "" ||
		!validConstructionColumnID(source.CodingSelector) || source.OrdinalSemantics != ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition ||
		(source.CodingEmptyPolicy != ConstructionGroupCodingEmptyPolicyGroup && source.CodingEmptyPolicy != ConstructionGroupCodingEmptyPolicyExclude && source.CodingEmptyPolicy != ConstructionGroupCodingEmptyPolicyError) {
		return fmt.Errorf("%s relatedOccurrence CODING_IDENTITY requires exact Coding selector and ordinal policy without candidateId", path)
	}
	return nil
}

func cohortGroupStageColumns(sourceFields []Field, groupRows *GroupRows) ([]StageColumn, error) {
	if groupRows == nil {
		return nil, fmt.Errorf("group rows are required")
	}
	byID := make(map[string]Field, len(sourceFields))
	for _, field := range sourceFields {
		byID[field.ColumnID] = field
	}
	columns := []StageColumn{
		{ID: "group_id", Name: "group_id", Type: "string"},
		{ID: "group_label", Name: "group_label", Type: "string"},
		{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
		{ID: "members", Name: "members", Type: "array"},
	}
	seen := map[string]bool{}
	for _, column := range columns {
		seen[column.ID] = true
	}
	for _, selected := range groupRows.RowValues {
		field, ok := byID[selected.ColumnID]
		if !ok || field.ColumnID == "" {
			return nil, fmt.Errorf("row value column ID %q is missing from the root projection", selected.ColumnID)
		}
		if seen[field.ColumnID] || seen[field.Name] {
			return nil, fmt.Errorf("row value column %q collides with a cohort stage column", selected.ColumnID)
		}
		seen[field.ColumnID], seen[field.Name] = true, true
		typ := "INFER"
		if selected.Policy == ConstructionRowValueAll {
			typ = "array"
		}
		columns = append(columns, StageColumn{ID: field.ColumnID, Name: field.Name, Label: field.Label, Type: typ})
	}
	return columns, nil
}
