package recipe

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/spec"
)

type ConstructionRelatedSource struct {
	AnchorColumnID     string                         `json:"anchorColumnId"`
	ChoiceID           string                         `json:"choiceId"`
	SourceOccurrenceID string                         `json:"sourceOccurrenceId"`
	Source             ConstructionRelatedFieldSource `json:"source"`
	Route              []ConstructionRelatedRouteStep `json:"route"`
	ContributorPolicy  string                         `json:"contributorPolicy"`
	Predicate          *ConstructionRelatedPredicate  `json:"predicate,omitempty"`
	Form               string                         `json:"form"`
	RowValuePolicy     ConstructionRowValuePolicy     `json:"rowValuePolicy,omitempty"`
	OutputColumnID     string                         `json:"outputColumnId"`
}

type ConstructionRelatedExpand struct {
	AnchorColumnID        string
	ChoiceID              string
	TargetNodeID          string
	TargetResourceType    string
	Route                 []ConstructionRelatedRouteStep
	ContributorPolicy     string
	ContributorPredicate  *ConstructionRelatedPredicate
	ContributorSource     *ConstructionRelatedFieldSource
	ContributorChoiceID   string
	EmptyPolicy           ExpansionEmptyPolicy
	RelatedRecordColumnID string
}

type ConstructionRelatedEligibility struct {
	AnchorColumnID       string
	ChoiceID             string
	TargetNodeID         string
	TargetResourceType   string
	Route                []ConstructionRelatedRouteStep
	ContributorPolicy    string
	ContributorPredicate *ConstructionRelatedPredicate
	ContributorSource    *ConstructionRelatedFieldSource
	ContributorChoiceID  string
	MatchKind            string
	Threshold            *int
}

const (
	RelatedEligibilityExists       = "EXISTS"
	RelatedEligibilityAbsent       = "ABSENT"
	RelatedEligibilityCountAtLeast = "COUNT_AT_LEAST"
)

type ConstructionRelatedField struct {
	ChoiceID       string                         `json:"choiceId"`
	Source         ConstructionRelatedFieldSource `json:"source"`
	OutputColumnID string                         `json:"outputColumnId"`
}

type ConstructionRelatedPredicate struct {
	CandidateID string              `json:"candidateId"`
	Quantifier  ArrayQuantifier     `json:"quantifier,omitempty"`
	Operator    spec.FilterOperator `json:"operator"`
	Value       *spec.FilterValue   `json:"value,omitempty"`
}

type ConstructionRelatedRepeatedBoundary struct {
	Path     string `json:"path"`
	MaxItems int    `json:"maxItems"`
}

type ConstructionRelatedFieldSource struct {
	CandidateID        string                                `json:"candidateId"`
	NodeID             string                                `json:"nodeId"`
	ResourceType       string                                `json:"resourceType"`
	Path               string                                `json:"path"`
	Cardinality        string                                `json:"cardinality"`
	LogicalType        string                                `json:"logicalType"`
	RepeatedBoundaries []ConstructionRelatedRepeatedBoundary `json:"repeatedBoundaries,omitempty"`
}

type ConstructionRelatedRouteStep struct {
	EdgeID           string `json:"edgeId"`
	FromNodeID       string `json:"fromNodeId"`
	ToNodeID         string `json:"toNodeId"`
	FromResourceType string `json:"fromResourceType"`
	ToResourceType   string `json:"toResourceType"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection"`
	MatchMode        string `json:"matchMode"`
}

func validateConstructionRelatedEligibility(related ConstructionRelatedEligibility, input, output map[string]StageColumn, path string) error {
	if !validConstructionColumnID(related.AnchorColumnID) || !validConstructionColumnID(related.ChoiceID) ||
		!validConstructionColumnID(related.TargetNodeID) || !validConstructionColumnID(related.TargetResourceType) ||
		len(related.Route) == 0 {
		return fmt.Errorf("%s.relatedEligibility requires an exact input anchor, choice, target, and route", path)
	}
	priorNode, priorResource := related.Route[0].FromNodeID, related.Route[0].FromResourceType
	for index, hop := range related.Route {
		if !validConstructionColumnID(hop.EdgeID) || !validConstructionColumnID(hop.FromNodeID) ||
			!validConstructionColumnID(hop.ToNodeID) || !validConstructionColumnID(hop.FromResourceType) ||
			!validConstructionColumnID(hop.ToResourceType) || !validConstructionColumnID(hop.Relationship) ||
			hop.FromNodeID != priorNode || hop.FromResourceType != priorResource ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("%s.relatedEligibility.route[%d] is invalid or discontinuous", path, index)
		}
		priorNode, priorResource = hop.ToNodeID, hop.ToResourceType
	}
	if priorNode != related.TargetNodeID || priorResource != related.TargetResourceType {
		return fmt.Errorf("%s.relatedEligibility route terminal differs from its exact target", path)
	}
	if related.ContributorPolicy != "ALL_MATCHES" {
		return fmt.Errorf("%s.relatedEligibility contributor policy must be ALL_MATCHES", path)
	}
	if related.ContributorPredicate == nil {
		if related.ContributorSource != nil || related.ContributorChoiceID != "" {
			return fmt.Errorf("%s.relatedEligibility contributor source and choice require a predicate", path)
		}
	} else {
		if related.ContributorSource == nil || !validConstructionColumnID(related.ContributorChoiceID) {
			return fmt.Errorf("%s.relatedEligibility predicate requires an exact contributor source and choice", path)
		}
		source := *related.ContributorSource
		if source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
			!validConstructionColumnID(source.CandidateID) || !validConstructionColumnID(source.Path) || !validConstructionColumnID(source.LogicalType) ||
			(source.Cardinality != "optional_one" && source.Cardinality != "required_one") {
			return fmt.Errorf("%s.relatedEligibility contributor source must be an exact target scalar", path)
		}
		if err := validateConstructionRelatedPredicate(source, related.ContributorPredicate, path+".relatedEligibility.contributorPredicate"); err != nil {
			return err
		}
	}
	switch related.MatchKind {
	case RelatedEligibilityExists, RelatedEligibilityAbsent:
		if related.Threshold != nil {
			return fmt.Errorf("%s.relatedEligibility threshold is only valid for COUNT_AT_LEAST", path)
		}
	case RelatedEligibilityCountAtLeast:
		if related.Threshold == nil || *related.Threshold <= 0 {
			return fmt.Errorf("%s.relatedEligibility COUNT_AT_LEAST requires a positive threshold", path)
		}
	default:
		return fmt.Errorf("%s.relatedEligibility match kind is unsupported", path)
	}
	expected := make(map[string]bool, len(input))
	for id := range input {
		expected[id] = true
	}
	return requireExactStageOutputIDs(expected, output, path)
}

func validateConstructionRelatedField(related ConstructionRelatedField, input, output map[string]StageColumn, path string) error {
	source := related.Source
	if !validConstructionColumnID(related.ChoiceID) || !validConstructionColumnID(source.CandidateID) ||
		!validConstructionColumnID(source.NodeID) || !validConstructionColumnID(source.ResourceType) ||
		!validConstructionColumnID(source.Path) || !validConstructionColumnID(source.LogicalType) ||
		(source.Cardinality != "optional_one" && source.Cardinality != "required_one") {
		return fmt.Errorf("%s.relatedField requires an exact scalar field choice and output", path)
	}
	if !validConstructionColumnID(related.OutputColumnID) || input[related.OutputColumnID].ID != "" {
		return fmt.Errorf("%s.relatedField.outputColumnId is empty or already exists", path)
	}
	want := make(map[string]bool, len(input)+1)
	for id := range input {
		want[id] = true
	}
	want[related.OutputColumnID] = true
	if err := requireExactStageOutputIDs(want, output, path); err != nil {
		return err
	}
	column := output[related.OutputColumnID]
	if column.Type != "" && column.Type != "INFER" && column.Type != source.LogicalType {
		return fmt.Errorf("%s.relatedField output type must match the selected field logical type", path)
	}
	if !column.Nullable {
		return fmt.Errorf("%s.relatedField output must be nullable because the active terminal record may be absent", path)
	}
	return nil
}

func validateConstructionRelatedExpand(related ConstructionRelatedExpand, input, output map[string]StageColumn, path string) error {
	if !validConstructionColumnID(related.AnchorColumnID) || !validConstructionColumnID(related.ChoiceID) ||
		!validConstructionColumnID(related.TargetNodeID) || !validConstructionColumnID(related.TargetResourceType) ||
		!validConstructionColumnID(related.RelatedRecordColumnID) || len(related.Route) == 0 {
		return fmt.Errorf("%s.relatedExpand requires an exact row anchor, choice and target, route, and output", path)
	}
	priorNode, priorResource := related.Route[0].FromNodeID, related.Route[0].FromResourceType
	for index, hop := range related.Route {
		if !validConstructionColumnID(hop.EdgeID) || !validConstructionColumnID(hop.FromNodeID) ||
			!validConstructionColumnID(hop.ToNodeID) || !validConstructionColumnID(hop.FromResourceType) ||
			!validConstructionColumnID(hop.ToResourceType) || !validConstructionColumnID(hop.Relationship) ||
			hop.FromNodeID != priorNode || hop.FromResourceType != priorResource ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("%s.relatedExpand.route[%d] is not a contiguous exact hop", path, index)
		}
		priorNode, priorResource = hop.ToNodeID, hop.ToResourceType
	}
	if priorNode != related.TargetNodeID || priorResource != related.TargetResourceType {
		return fmt.Errorf("%s.relatedExpand route terminal differs from its exact target", path)
	}
	if related.ContributorPolicy != "ALL_MATCHES" {
		return fmt.Errorf("%s.relatedExpand contributor policy must be ALL_MATCHES", path)
	}
	if related.ContributorPredicate == nil {
		if related.ContributorSource != nil || related.ContributorChoiceID != "" {
			return fmt.Errorf("%s.relatedExpand contributor source and choice require a predicate", path)
		}
	} else {
		if related.ContributorSource == nil || !validConstructionColumnID(related.ContributorChoiceID) {
			return fmt.Errorf("%s.relatedExpand predicate requires a contributor source and choice", path)
		}
		source := *related.ContributorSource
		if !validConstructionColumnID(source.CandidateID) || source.NodeID != related.TargetNodeID || source.ResourceType != related.TargetResourceType ||
			!validConstructionColumnID(source.Path) || !validConstructionColumnID(source.LogicalType) ||
			(source.Cardinality != "optional_one" && source.Cardinality != "required_one" && source.Cardinality != "many") {
			return fmt.Errorf("%s.relatedExpand contributor source must be a scalar or repeated scalar field on the target resource", path)
		}
		if err := validateConstructionRelatedFieldSourceShape(source, path+".relatedExpand.contributorSource"); err != nil {
			return err
		}
		if err := validateConstructionRelatedPredicate(source, related.ContributorPredicate, path+".relatedExpand.contributorPredicate"); err != nil {
			return err
		}
	}
	if related.EmptyPolicy != ExpansionError && related.EmptyPolicy != ExpansionExclude && related.EmptyPolicy != ExpansionPreserveParent {
		return fmt.Errorf("%s.relatedExpand empty policy is unsupported", path)
	}
	if input[related.RelatedRecordColumnID].ID != "" {
		return fmt.Errorf("%s.relatedExpand outputColumnId collides with an input column", path)
	}
	want := make(map[string]bool, len(input)+1)
	for id := range input {
		want[id] = true
	}
	want[related.RelatedRecordColumnID] = true
	if err := requireExactStageOutputIDs(want, output, path); err != nil {
		return err
	}
	wantNullable := related.EmptyPolicy == ExpansionPreserveParent
	if output[related.RelatedRecordColumnID].Nullable != wantNullable {
		return fmt.Errorf("%s.relatedExpand output nullable must be %t for its empty policy", path, wantNullable)
	}
	return nil
}

func validateConstructionRelatedSource(related ConstructionRelatedSource, input, output map[string]StageColumn, path string) error {
	if !validConstructionRelatedSourceAnchorID(related.AnchorColumnID) {
		return fmt.Errorf("%s.relatedSource.anchorColumnId must identify the hidden root row identity", path)
	}
	if !validConstructionColumnID(related.ChoiceID) || !validConstructionColumnID(related.SourceOccurrenceID) ||
		related.SourceOccurrenceID != related.Source.NodeID {
		return fmt.Errorf("%s.relatedSource requires a durable choice and terminal source occurrence", path)
	}
	source := related.Source
	if !validConstructionColumnID(source.CandidateID) || !validConstructionColumnID(source.NodeID) ||
		!validConstructionColumnID(source.ResourceType) || !validConstructionColumnID(source.Path) ||
		!validConstructionColumnID(source.LogicalType) ||
		(source.Cardinality != "optional_one" && source.Cardinality != "required_one") {
		return fmt.Errorf("%s.relatedSource.source must identify one scalar field candidate", path)
	}
	if (related.Form != "ALL" && related.Form != "COUNT" && related.Form != "PRESENCE") || related.ContributorPolicy != "ALL_MATCHES" {
		return fmt.Errorf("%s.relatedSource supports ALL, COUNT, or PRESENCE with ALL_MATCHES contributor policy", path)
	}
	if related.RowValuePolicy != "" && related.RowValuePolicy != ConstructionRowValueAll && related.RowValuePolicy != ConstructionRowValueOne {
		return fmt.Errorf("%s.relatedSource.rowValuePolicy must be ALL or ONE", path)
	}
	if related.RowValuePolicy == ConstructionRowValueOne && related.Form != "ALL" {
		return fmt.Errorf("%s.relatedSource.rowValuePolicy ONE is supported only when form is ALL", path)
	}
	if err := validateConstructionRelatedPredicate(source, related.Predicate, path+".relatedSource.predicate"); err != nil {
		return err
	}
	if len(related.Route) == 0 {
		return fmt.Errorf("%s.relatedSource.route must contain at least one hop", path)
	}
	priorNode, priorResource := related.Route[0].FromNodeID, related.Route[0].FromResourceType
	for index, hop := range related.Route {
		if !validConstructionColumnID(hop.EdgeID) || !validConstructionColumnID(hop.Relationship) ||
			!validConstructionColumnID(hop.ToNodeID) || !validConstructionColumnID(hop.ToResourceType) ||
			hop.FromNodeID != priorNode || hop.FromResourceType != priorResource ||
			(hop.StorageDirection != "INBOUND" && hop.StorageDirection != "OUTBOUND") ||
			(hop.MatchMode != "OPTIONAL" && hop.MatchMode != "REQUIRED") {
			return fmt.Errorf("%s.relatedSource.route[%d] is not a contiguous authorized hop", path, index)
		}
		priorNode, priorResource = hop.ToNodeID, hop.ToResourceType
	}
	if priorNode != source.NodeID || priorResource != source.ResourceType {
		return fmt.Errorf("%s.relatedSource route terminal does not match source identity", path)
	}
	if !validConstructionColumnID(related.OutputColumnID) || input[related.OutputColumnID].ID != "" {
		return fmt.Errorf("%s.relatedSource.outputColumnId is empty or already exists", path)
	}
	want := make(map[string]bool, len(input)+1)
	for id := range input {
		want[id] = true
	}
	want[related.OutputColumnID] = true
	return requireExactStageOutputIDs(want, output, path)
}

func validConstructionRelatedSourceAnchorID(id string) bool {
	return id == "_key" || id == "__loom_row_id"
}

func validateConstructionRelatedPredicate(source ConstructionRelatedFieldSource, predicate *ConstructionRelatedPredicate, path string) error {
	if predicate == nil {
		return nil
	}
	if source.Cardinality == "many" {
		if predicate.Quantifier != QuantifierAny {
			return fmt.Errorf("%s repeated contributor predicates require ANY quantifier", path)
		}
	} else if predicate.Quantifier != "" {
		return fmt.Errorf("%s scalar contributor predicates must omit quantifier", path)
	}
	if !validConstructionColumnID(predicate.CandidateID) || predicate.CandidateID != source.CandidateID {
		return fmt.Errorf("%s candidateId must match the selected related source field", path)
	}
	if predicate.Operator != FilterExists && predicate.Operator != FilterEquals {
		return fmt.Errorf("%s operator must be EXISTS or EQUALS", path)
	}
	if predicate.Operator == FilterExists {
		if predicate.Value != nil {
			return fmt.Errorf("%s EXISTS does not accept a value", path)
		}
		return nil
	}
	if predicate.Value == nil {
		return fmt.Errorf("%s EQUALS requires a value", path)
	}
	if err := predicate.Value.Validate(); err != nil {
		return fmt.Errorf("%s value: %w", path, err)
	}
	wantKind := FilterString
	switch strings.ToLower(strings.TrimSpace(source.LogicalType)) {
	case "string":
	case "code":
		wantKind = FilterCode
	default:
		return fmt.Errorf("%s EQUALS is supported only for string or code source fields", path)
	}
	if predicate.Value.Kind != wantKind {
		return fmt.Errorf("%s EQUALS requires a %s value for the selected source field", path, wantKind)
	}
	return nil
}

func validateConstructionRelatedFieldSourceShape(source ConstructionRelatedFieldSource, path string) error {
	selectorPath := strings.TrimPrefix(source.Path, source.ResourceType+".")
	selector, err := spec.ParseSelector(selectorPath)
	if err != nil || selector.Filter != nil || len(selector.Steps) == 0 {
		return fmt.Errorf("%s path must be an exact scalar selector", path)
	}

	pathParts := make([]string, 0, len(selector.Steps))
	expectedBoundaries := make([]string, 0, len(source.RepeatedBoundaries))
	for _, step := range selector.Steps {
		if step.Index != nil {
			return fmt.Errorf("%s path must not contain array indexes", path)
		}
		segment := step.Field
		if step.Iterate {
			segment += "[]"
		}
		pathParts = append(pathParts, segment)
		if step.Iterate {
			expectedBoundaries = append(expectedBoundaries, strings.Join(pathParts, "."))
		}
	}

	if len(expectedBoundaries) == 0 {
		if source.Cardinality != "optional_one" && source.Cardinality != "required_one" {
			return fmt.Errorf("%s scalar path must have scalar cardinality", path)
		}
		if len(source.RepeatedBoundaries) != 0 {
			return fmt.Errorf("%s scalar path cannot carry repeated boundaries", path)
		}
		return nil
	}
	if source.Cardinality != "many" {
		return fmt.Errorf("%s repeated path requires many cardinality", path)
	}
	if len(source.RepeatedBoundaries) != len(expectedBoundaries) {
		return fmt.Errorf("%s repeated boundaries do not match the selector path", path)
	}
	for index, boundary := range source.RepeatedBoundaries {
		if boundary.Path != expectedBoundaries[index] || boundary.MaxItems < 0 {
			return fmt.Errorf("%s repeated boundary %d does not match the selector path", path, index)
		}
	}
	return nil
}
