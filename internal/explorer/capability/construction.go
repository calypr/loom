package capability

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

const ConstructionChoiceIDMaxLength = 16384

type ConstructionChoiceForm string

const (
	ConstructionChoiceValue        ConstructionChoiceForm = "VALUE"
	ConstructionChoiceFirst        ConstructionChoiceForm = "FIRST"
	ConstructionChoiceAll          ConstructionChoiceForm = "ALL"
	ConstructionChoiceCount        ConstructionChoiceForm = "COUNT"
	ConstructionChoicePresence     ConstructionChoiceForm = "PRESENCE"
	ConstructionChoiceDistinct     ConstructionChoiceForm = "DISTINCT"
	ConstructionChoiceOwnerRecords ConstructionChoiceForm = "OWNER_RECORDS"
)

type ConstructionChoiceDecision string

const (
	ConstructionChoiceDefault          ConstructionChoiceDecision = "DEFAULT"
	ConstructionChoiceRequiresDecision ConstructionChoiceDecision = "REQUIRES_DECISION"
)

type ConstructionChoicePreservation string

const (
	ConstructionChoicePreserving ConstructionChoicePreservation = "PRESERVING"
	ConstructionChoiceReducing   ConstructionChoicePreservation = "REDUCING"
)

type ConstructionChoiceShape string

const (
	ConstructionChoiceScalar ConstructionChoiceShape = "SCALAR"
	ConstructionChoiceList   ConstructionChoiceShape = "LIST"
)

type ConstructionChoiceRowEffect string

const ConstructionChoicePreservesRows ConstructionChoiceRowEffect = "PRESERVES_ROW_GRAIN"

type ConstructionChoiceSupport string

const ConstructionChoiceSupported ConstructionChoiceSupport = "SUPPORTED"

type ConstructionChoicePredicateOperator string

const (
	ConstructionChoicePredicateExists ConstructionChoicePredicateOperator = "EXISTS"
	ConstructionChoicePredicateEquals ConstructionChoicePredicateOperator = "EQUALS"
)

type ConstructionChoiceSourceKind string

const (
	ConstructionChoiceSourceField           ConstructionChoiceSourceKind = "FIELD"
	ConstructionChoiceSourceSemantic        ConstructionChoiceSourceKind = "SEMANTIC"
	ConstructionChoiceSourceSemanticFrame   ConstructionChoiceSourceKind = "SEMANTIC_FRAME"
	ConstructionChoiceSourceRelatedResource ConstructionChoiceSourceKind = "RELATED_RESOURCE"
	ConstructionChoiceSourceRelatedField    ConstructionChoiceSourceKind = "RELATED_FIELD"
	ConstructionChoiceSourceCodedGroup      ConstructionChoiceSourceKind = "CODED_GROUP"
	ConstructionChoiceSourceGroupField      ConstructionChoiceSourceKind = "GROUP_FIELD"
)

// ConstructionChoiceSource is closed to the variants defined by this package.
type ConstructionChoiceSource interface {
	constructionChoiceSource()
}

// FieldChoiceSource identifies one exact, compiler-proved capability field.
// Labels stay outside the identity so presentation changes do not select data.
type FieldChoiceSource struct {
	Kind               ConstructionChoiceSourceKind `json:"kind"`
	CandidateID        string                       `json:"candidateId"`
	NodeID             string                       `json:"nodeId"`
	ResourceType       string                       `json:"resourceType"`
	Path               string                       `json:"path"`
	Cardinality        string                       `json:"cardinality"`
	RepeatedBoundaries []RepeatedBoundary           `json:"repeatedBoundaries,omitempty"`
}

func (FieldChoiceSource) constructionChoiceSource() {}

// GroupFieldChoiceSource is an ephemeral, compiler-context-bound Group field
// capability. The choice token contains one input variant; flat fields are
// accepted only to decode pre-sum CURRENT_STAGE tokens.
type GroupFieldChoiceSource struct {
	Kind                 ConstructionChoiceSourceKind `json:"kind"`
	TargetGroupStepID    string                       `json:"targetGroupStepId,omitempty"`
	SourceStageID        string                       `json:"sourceStageId"`
	SourceShapeID        string                       `json:"sourceShapeId"`
	SourceIdentityColumn string                       `json:"sourceIdentityColumn"`
	Input                *GroupFieldChoiceInput       `json:"input,omitempty"`
	RowChoiceID          string                       `json:"rowChoiceId,omitempty"`
	ValueKind            string                       `json:"valueKind"`
	NodeID               string                       `json:"nodeId"`
	FHIRType             string                       `json:"fhirType"`
	Cardinality          string                       `json:"cardinality"`
	Shape                string                       `json:"shape"`
	Reference            bool                         `json:"reference"`

	// Flat CURRENT_STAGE capability fields retained for existing builders and
	// old choice-token decoding. New tokens carry these fields under Input.
	SourceRole         string   `json:"-"`
	OwnerOccurrenceID  string   `json:"-"`
	OwnerResourceType  string   `json:"-"`
	OwnerSelector      string   `json:"-"`
	ConsumedBoundaries []string `json:"-"`
	OccurrenceID       string   `json:"-"`
	ResourceType       string   `json:"-"`
	BoundarySelector   string   `json:"-"`
	CandidateID        string   `json:"-"`
	CanonicalPath      string   `json:"-"`
}

type GroupFieldChoiceInput struct {
	Kind              string                            `json:"kind"`
	CurrentStage      *GroupFieldCurrentStageInput      `json:"currentStage,omitempty"`
	RelatedOccurrence *GroupFieldRelatedOccurrenceInput `json:"relatedOccurrence,omitempty"`
}

func (input *GroupFieldChoiceInput) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Kind              string          `json:"kind"`
		CurrentStage      json.RawMessage `json:"currentStage,omitempty"`
		RelatedOccurrence json.RawMessage `json:"relatedOccurrence,omitempty"`
	}
	if err := decodeChoiceJSON(raw, &wire); err != nil {
		return err
	}
	if (len(wire.CurrentStage) == 0) == (len(wire.RelatedOccurrence) == 0) {
		return fmt.Errorf("Group field choice input requires exactly one provenance payload")
	}
	decoded := GroupFieldChoiceInput{Kind: wire.Kind}
	switch wire.Kind {
	case "CURRENT_STAGE":
		if len(wire.CurrentStage) == 0 {
			return fmt.Errorf("CURRENT_STAGE Group field choice requires currentStage")
		}
		var current GroupFieldCurrentStageInput
		if err := decodeChoiceJSON(wire.CurrentStage, &current); err != nil {
			return err
		}
		decoded.CurrentStage = &current
	case "RELATED_OCCURRENCE":
		if len(wire.RelatedOccurrence) == 0 {
			return fmt.Errorf("RELATED_OCCURRENCE Group field choice requires relatedOccurrence")
		}
		var related GroupFieldRelatedOccurrenceInput
		if err := decodeChoiceJSON(wire.RelatedOccurrence, &related); err != nil {
			return err
		}
		decoded.RelatedOccurrence = &related
	default:
		return fmt.Errorf("unsupported Group field choice input kind %q", wire.Kind)
	}
	*input = decoded
	return nil
}

type GroupFieldCurrentStageInput struct {
	SourceRole         string   `json:"sourceRole"`
	OccurrenceID       string   `json:"occurrenceId"`
	OwnerOccurrenceID  string   `json:"ownerOccurrenceId,omitempty"`
	OwnerResourceType  string   `json:"ownerResourceType"`
	OwnerSelector      string   `json:"ownerSelector,omitempty"`
	ConsumedBoundaries []string `json:"consumedBoundaries,omitempty"`
	ResourceType       string   `json:"resourceType"`
	BoundarySelector   string   `json:"boundarySelector,omitempty"`
	CandidateID        string   `json:"candidateId,omitempty"`
	CanonicalPath      string   `json:"canonicalPath,omitempty"`
}

type GroupFieldRelatedRouteStep struct {
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

type GroupFieldRelatedOccurrenceInput struct {
	RouteID            string                       `json:"-"`
	AnchorOccurrenceID string                       `json:"anchorOccurrenceId"`
	AnchorNodeID       string                       `json:"anchorNodeId"`
	AnchorResourceType string                       `json:"anchorResourceType"`
	Route              []GroupFieldRelatedRouteStep `json:"route"`
	TargetOccurrenceID string                       `json:"targetOccurrenceId"`
	TargetNodeID       string                       `json:"targetNodeId"`
	TargetResourceType string                       `json:"targetResourceType"`
	ResourceType       string                       `json:"resourceType"`
	CandidateID        string                       `json:"candidateId,omitempty"`
	CanonicalPath      string                       `json:"canonicalPath,omitempty"`
	EmptyPolicy        string                       `json:"emptyPolicy"`
	Multiplicity       string                       `json:"multiplicity"`
	CodingSelector     string                       `json:"codingSelector,omitempty"`
	CodingEmptyPolicy  string                       `json:"codingEmptyPolicy,omitempty"`
	OrdinalSemantics   string                       `json:"ordinalSemantics,omitempty"`
}

func (source *GroupFieldChoiceSource) UnmarshalJSON(raw []byte) error {
	var wire struct {
		Kind                 ConstructionChoiceSourceKind `json:"kind"`
		TargetGroupStepID    string                       `json:"targetGroupStepId,omitempty"`
		SourceStageID        string                       `json:"sourceStageId"`
		SourceShapeID        string                       `json:"sourceShapeId"`
		SourceIdentityColumn string                       `json:"sourceIdentityColumn"`
		Input                *GroupFieldChoiceInput       `json:"input,omitempty"`
		RowChoiceID          string                       `json:"rowChoiceId,omitempty"`
		ValueKind            string                       `json:"valueKind"`
		NodeID               string                       `json:"nodeId"`
		FHIRType             string                       `json:"fhirType"`
		Cardinality          string                       `json:"cardinality"`
		Shape                string                       `json:"shape"`
		Reference            bool                         `json:"reference"`
		SourceRole           *string                      `json:"sourceRole,omitempty"`
		OwnerOccurrenceID    *string                      `json:"ownerOccurrenceId,omitempty"`
		OwnerResourceType    *string                      `json:"ownerResourceType,omitempty"`
		OwnerSelector        *string                      `json:"ownerSelector,omitempty"`
		ConsumedBoundaries   []string                     `json:"consumedBoundaries,omitempty"`
		OccurrenceID         *string                      `json:"occurrenceId,omitempty"`
		ResourceType         *string                      `json:"resourceType,omitempty"`
		BoundarySelector     *string                      `json:"boundarySelector,omitempty"`
		CandidateID          *string                      `json:"candidateId,omitempty"`
		CanonicalPath        *string                      `json:"canonicalPath,omitempty"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return err
	}
	decoded := GroupFieldChoiceSource{
		Kind: wire.Kind, TargetGroupStepID: wire.TargetGroupStepID,
		SourceStageID: wire.SourceStageID, SourceShapeID: wire.SourceShapeID,
		SourceIdentityColumn: wire.SourceIdentityColumn, Input: wire.Input,
		RowChoiceID: wire.RowChoiceID, ValueKind: wire.ValueKind, NodeID: wire.NodeID,
		FHIRType: wire.FHIRType, Cardinality: wire.Cardinality, Shape: wire.Shape, Reference: wire.Reference,
		ConsumedBoundaries: append([]string(nil), wire.ConsumedBoundaries...),
	}
	flat := wire.SourceRole != nil || wire.OwnerOccurrenceID != nil || wire.OwnerResourceType != nil ||
		wire.OwnerSelector != nil || wire.ConsumedBoundaries != nil || wire.OccurrenceID != nil ||
		wire.ResourceType != nil || wire.BoundarySelector != nil || wire.CandidateID != nil || wire.CanonicalPath != nil
	if wire.Input != nil && flat {
		return fmt.Errorf("Group field choice input cannot be combined with legacy flat fields")
	}
	if wire.Input == nil {
		if !flat {
			return fmt.Errorf("Group field choice requires an input provenance")
		}
		current := &GroupFieldCurrentStageInput{}
		if wire.SourceRole != nil {
			current.SourceRole = *wire.SourceRole
		}
		if wire.OccurrenceID != nil {
			current.OccurrenceID = *wire.OccurrenceID
		}
		if wire.OwnerOccurrenceID != nil {
			current.OwnerOccurrenceID = *wire.OwnerOccurrenceID
		}
		if wire.OwnerResourceType != nil {
			current.OwnerResourceType = *wire.OwnerResourceType
		}
		if wire.OwnerSelector != nil {
			current.OwnerSelector = *wire.OwnerSelector
		}
		if wire.ResourceType != nil {
			current.ResourceType = *wire.ResourceType
		}
		if wire.BoundarySelector != nil {
			current.BoundarySelector = *wire.BoundarySelector
		}
		if wire.CandidateID != nil {
			current.CandidateID = *wire.CandidateID
		}
		if wire.CanonicalPath != nil {
			current.CanonicalPath = *wire.CanonicalPath
		}
		decoded.Input = &GroupFieldChoiceInput{Kind: "CURRENT_STAGE", CurrentStage: current}
		decoded.setCurrentStageAliases(current)
	}
	if err := decoded.normalizeAndValidateInput(); err != nil {
		return err
	}
	*source = decoded
	return nil
}

func (source *GroupFieldChoiceSource) setCurrentStageAliases(current *GroupFieldCurrentStageInput) {
	if source == nil || current == nil {
		return
	}
	source.SourceRole = current.SourceRole
	source.OccurrenceID = current.OccurrenceID
	source.OwnerOccurrenceID = current.OwnerOccurrenceID
	if source.OwnerOccurrenceID == "" {
		source.OwnerOccurrenceID = current.OccurrenceID
	}
	source.OwnerResourceType = current.OwnerResourceType
	source.OwnerSelector = current.OwnerSelector
	source.ConsumedBoundaries = append([]string(nil), current.ConsumedBoundaries...)
	source.ResourceType = current.ResourceType
	source.BoundarySelector = current.BoundarySelector
	source.CandidateID = current.CandidateID
	source.CanonicalPath = current.CanonicalPath
}

func (source *GroupFieldChoiceSource) normalizeAndValidateInput() error {
	if source == nil || source.Kind != ConstructionChoiceSourceGroupField || source.SourceStageID == "" || source.SourceShapeID == "" ||
		source.SourceIdentityColumn == "" || source.NodeID == "" || source.ValueKind != "SCALAR" && source.ValueKind != "CODING_IDENTITY" {
		return fmt.Errorf("Group field choice requires exact stage, shape, row identity, node, and value kind")
	}
	if source.Input == nil {
		if source.SourceRole == "" && source.OccurrenceID == "" && source.OwnerResourceType == "" && source.ResourceType == "" &&
			source.CandidateID == "" && source.CanonicalPath == "" && source.RowChoiceID == "" {
			return fmt.Errorf("Group field choice requires one input provenance")
		}
		current := &GroupFieldCurrentStageInput{
			SourceRole: source.SourceRole, OccurrenceID: source.OccurrenceID, OwnerOccurrenceID: source.OwnerOccurrenceID,
			OwnerResourceType: source.OwnerResourceType, OwnerSelector: source.OwnerSelector,
			ConsumedBoundaries: append([]string(nil), source.ConsumedBoundaries...), ResourceType: source.ResourceType,
			BoundarySelector: source.BoundarySelector, CandidateID: source.CandidateID, CanonicalPath: source.CanonicalPath,
		}
		source.Input = &GroupFieldChoiceInput{Kind: "CURRENT_STAGE", CurrentStage: current}
	}
	switch source.Input.Kind {
	case "CURRENT_STAGE":
		current := source.Input.CurrentStage
		if current == nil || source.Input.RelatedOccurrence != nil || current.SourceRole == "" || current.OccurrenceID == "" ||
			current.OwnerResourceType == "" || current.ResourceType == "" || !validGroupFieldSourceRole(current.SourceRole) {
			return fmt.Errorf("CURRENT_STAGE Group field choice requires exact current field ownership")
		}
		if current.OwnerOccurrenceID != "" && current.OwnerOccurrenceID != current.OccurrenceID {
			return fmt.Errorf("CURRENT_STAGE Group field choice owner occurrence must match its field occurrence")
		}
		if source.ValueKind == "SCALAR" && (current.CandidateID == "" || current.CanonicalPath == "") {
			return fmt.Errorf("scalar CURRENT_STAGE Group field choice requires candidate and canonical path")
		}
		if source.ValueKind == "CODING_IDENTITY" && (current.SourceRole != "CURRENT_ITEM" || current.CandidateID != "" ||
			current.CanonicalPath == "" || source.RowChoiceID == "") {
			return fmt.Errorf("Coding identity CURRENT_STAGE Group choice requires an exact observed current Coding item")
		}
		source.setCurrentStageAliases(current)
	case "RELATED_OCCURRENCE":
		related := source.Input.RelatedOccurrence
		if related == nil || source.Input.CurrentStage != nil || related.AnchorOccurrenceID == "" || related.AnchorNodeID == "" ||
			related.AnchorResourceType == "" || related.TargetOccurrenceID == "" || related.TargetNodeID == "" ||
			related.TargetResourceType == "" || related.ResourceType == "" || len(related.Route) == 0 ||
			related.EmptyPolicy == "" || related.Multiplicity != "DISTINCT_TARGET_PER_INPUT" {
			return fmt.Errorf("RELATED_OCCURRENCE Group field choice requires exact anchor, route, target, and multiplicity")
		}
		if related.Route[0].FromOccurrenceID != related.AnchorOccurrenceID || related.Route[0].FromNodeID != related.AnchorNodeID ||
			related.Route[0].FromResourceType != related.AnchorResourceType ||
			related.Route[len(related.Route)-1].ToOccurrenceID != related.TargetOccurrenceID ||
			related.Route[len(related.Route)-1].ToNodeID != related.TargetNodeID ||
			related.Route[len(related.Route)-1].ToResourceType != related.TargetResourceType {
			return fmt.Errorf("RELATED_OCCURRENCE route endpoints do not match its anchor and target")
		}
		for index, step := range related.Route {
			if step.StepIndex != index || step.FromOccurrenceID == "" || step.FromNodeID == "" || step.FromResourceType == "" ||
				step.EdgeID == "" || step.Relationship == "" || step.ToOccurrenceID == "" || step.ToNodeID == "" || step.ToResourceType == "" ||
				(step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND") ||
				(step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED") ||
				(index > 0 && (related.Route[index-1].ToOccurrenceID != step.FromOccurrenceID || related.Route[index-1].ToNodeID != step.FromNodeID || related.Route[index-1].ToResourceType != step.FromResourceType)) {
				return fmt.Errorf("RELATED_OCCURRENCE route step %d is invalid or disconnected", index)
			}
		}
		if related.EmptyPolicy != "PRESERVE_PARENT" && related.EmptyPolicy != "EXCLUDE" && related.EmptyPolicy != "ERROR" {
			return fmt.Errorf("RELATED_OCCURRENCE emptyPolicy is unsupported")
		}
		if source.ValueKind == "SCALAR" {
			if related.CandidateID == "" || related.CanonicalPath == "" || related.CodingSelector != "" || related.CodingEmptyPolicy != "" || related.OrdinalSemantics != "" {
				return fmt.Errorf("scalar RELATED_OCCURRENCE choice requires one observed candidate path")
			}
		} else if related.CandidateID != "" || related.CanonicalPath != "" || related.CodingSelector == "" ||
			related.CodingEmptyPolicy == "" || related.OrdinalSemantics == "" || source.RowChoiceID == "" {
			return fmt.Errorf("Coding identity RELATED_OCCURRENCE choice requires exact observed Coding selector facts")
		}
		if related.CodingEmptyPolicy != "" && related.CodingEmptyPolicy != "PRESERVE_PARENT" && related.CodingEmptyPolicy != "EXCLUDE" && related.CodingEmptyPolicy != "ERROR" {
			return fmt.Errorf("RELATED_OCCURRENCE Coding empty policy is unsupported")
		}
		source.SourceRole = "RELATED_OCCURRENCE"
		source.OccurrenceID = related.TargetOccurrenceID
		source.OwnerOccurrenceID = related.TargetOccurrenceID
		source.OwnerResourceType = related.TargetResourceType
		source.ResourceType = related.ResourceType
		source.CandidateID = related.CandidateID
		source.CanonicalPath = related.CanonicalPath
		source.NodeID = related.TargetNodeID
	default:
		return fmt.Errorf("unsupported Group field choice input kind %q", source.Input.Kind)
	}
	return nil
}

func (GroupFieldChoiceSource) constructionChoiceSource() {}

// SemanticBindingChoiceSource preserves the observed FHIR owner and terminology
// identity. Display text is deliberately excluded from computational identity.
type SemanticBindingChoiceSource struct {
	Kind                  ConstructionChoiceSourceKind `json:"kind"`
	GroupOutputID         string                       `json:"groupOutputId,omitempty"`
	GroupStageID          string                       `json:"groupStageId,omitempty"`
	ConceptID             string                       `json:"conceptId"`
	BindingID             string                       `json:"bindingId"`
	CandidateID           string                       `json:"candidateId"`
	NodeID                string                       `json:"nodeId"`
	ResourceType          string                       `json:"resourceType"`
	SourcePath            string                       `json:"sourcePath"`
	SourceCanonical       string                       `json:"sourceCanonical,omitempty"`
	SourceProfile         string                       `json:"sourceProfile,omitempty"`
	FieldPath             string                       `json:"fieldPath"`
	OwningScope           string                       `json:"owningScope,omitempty"`
	ExtensionURLPath      []string                     `json:"extensionUrlPath,omitempty"`
	KeySelector           string                       `json:"keySelector,omitempty"`
	System                string                       `json:"system,omitempty"`
	SourceKind            string                       `json:"sourceKind,omitempty"`
	Version               string                       `json:"version,omitempty"`
	Code                  string                       `json:"code,omitempty"`
	ValueSelector         string                       `json:"valueSelector"`
	ValueFallbacks        []string                     `json:"valueFallbacks,omitempty"`
	FallbackOnBlankString bool                         `json:"fallbackOnBlankString,omitempty"`
	ChoiceArm             string                       `json:"choiceArm,omitempty"`
	LogicalType           string                       `json:"logicalType"`
	RuleHint              string                       `json:"ruleHint"`
	RuleVersion           string                       `json:"ruleVersion"`
	SchemaVersion         int                          `json:"schemaVersion"`
	Cardinality           string                       `json:"cardinality"`
	RepeatedBoundaries    []RepeatedBoundary           `json:"repeatedBoundaries,omitempty"`
}

func (SemanticBindingChoiceSource) constructionChoiceSource() {}

func validateSemanticCategoryChoiceSource(source SemanticBindingChoiceSource) error {
	if source.RuleHint != "CODED_CATEGORY_V1" {
		if source.SourceKind != "" {
			return fmt.Errorf("sourceKind is reserved for an explicit coded category selection")
		}
		return nil
	}
	if strings.TrimSpace(source.System) == "" || strings.TrimSpace(source.Code) != "" ||
		(source.SourceKind != "file" && source.SourceKind != "retained_vertex") {
		return fmt.Errorf("coded category identity requires system, source kind, and no exact code")
	}
	return nil
}

// SemanticFrameFamily is the metadata identity of one correlated code/value
// binding, independent of any one observed code. It is persisted with a frame
// so later category choices can be checked against the same binding.
type SemanticFrameFamily struct {
	BindingID             string   `json:"bindingId"`
	ResourceType          string   `json:"resourceType"`
	SourcePath            string   `json:"sourcePath"`
	SourceCanonical       string   `json:"sourceCanonical,omitempty"`
	SourceProfile         string   `json:"sourceProfile,omitempty"`
	OwningScope           string   `json:"owningScope"`
	KeyPath               string   `json:"keyPath"`
	ValuePath             string   `json:"valuePath"`
	ValueFallbacks        []string `json:"valueFallbacks,omitempty"`
	FallbackOnBlankString bool     `json:"fallbackOnBlankString,omitempty"`
	ChoiceArms            []string `json:"choiceArms,omitempty"`
	LogicalType           string   `json:"logicalType"`
	RuleVersion           string   `json:"ruleVersion"`
	SchemaVersion         int      `json:"schemaVersion"`
}

// SemanticFrameChoiceSource is a server-issued frame-family choice. The
// anchor concept proves the family against the current inventory; its selected
// code is intentionally absent so the frame represents the whole family.
type SemanticFrameChoiceSource struct {
	Kind               ConstructionChoiceSourceKind `json:"kind"`
	AnchorConceptID    string                       `json:"anchorConceptId"`
	Family             SemanticFrameFamily          `json:"family"`
	CandidateID        string                       `json:"candidateId"`
	NodeID             string                       `json:"nodeId"`
	FieldPath          string                       `json:"fieldPath"`
	Cardinality        string                       `json:"cardinality"`
	RepeatedBoundaries []RepeatedBoundary           `json:"repeatedBoundaries,omitempty"`
}

func (SemanticFrameChoiceSource) constructionChoiceSource() {}

// RelatedResourceChoiceSource identifies one exact terminal resource node.
// It lets lifecycle authorize a route without requiring a field selection.
type RelatedResourceChoiceSource struct {
	Kind               ConstructionChoiceSourceKind `json:"kind"`
	StageID            string                       `json:"stageId"`
	AnchorColumnID     string                       `json:"anchorColumnId"`
	AnchorKind         string                       `json:"anchorKind"`
	AnchorNodeID       string                       `json:"anchorNodeId"`
	AnchorResourceType string                       `json:"anchorResourceType"`
	NodeID             string                       `json:"nodeId"`
	ResourceType       string                       `json:"resourceType"`
}

func (RelatedResourceChoiceSource) constructionChoiceSource() {}

// RelatedFieldChoiceSource binds one scalar field to a specific stage that
// carries an exact terminal resource identity.
type RelatedFieldChoiceSource struct {
	Kind         ConstructionChoiceSourceKind `json:"kind"`
	StageID      string                       `json:"stageId"`
	CandidateID  string                       `json:"candidateId"`
	NodeID       string                       `json:"nodeId"`
	ResourceType string                       `json:"resourceType"`
	Path         string                       `json:"path"`
	Cardinality  string                       `json:"cardinality"`
	LogicalType  string                       `json:"logicalType"`
}

func (RelatedFieldChoiceSource) constructionChoiceSource() {}

// CodedGroupChoiceSource binds a root Coding[] path to one output and one
// exact construction input stage. The persisted construction stores the path
// facts separately so it remains compilable after this snapshot expires.
type CodedGroupChoiceSource struct {
	Kind         ConstructionChoiceSourceKind `json:"kind"`
	OutputID     string                       `json:"outputId"`
	StageID      string                       `json:"stageId"`
	OccurrenceID string                       `json:"occurrenceId"`
	NodeID       string                       `json:"nodeId"`
	ResourceType string                       `json:"resourceType"`
	Path         string                       `json:"path"`
	SchemaDigest string                       `json:"schemaDigest"`
}

func (CodedGroupChoiceSource) constructionChoiceSource() {}

type ConstructionCodedGroupChoice struct {
	ChoiceID     string `json:"choiceId"`
	OccurrenceID string `json:"occurrenceId"`
	ResourceType string `json:"resourceType"`
	CodingPath   string `json:"codingPath"`
	Label        string `json:"label"`
}

type ConstructionChoiceOption struct {
	Form                          ConstructionChoiceForm                `json:"form"`
	Shape                         ConstructionChoiceShape               `json:"shape"`
	Decision                      ConstructionChoiceDecision            `json:"decision"`
	Preservation                  ConstructionChoicePreservation        `json:"preservation"`
	RowEffect                     ConstructionChoiceRowEffect           `json:"rowEffect"`
	Support                       ConstructionChoiceSupport             `json:"support"`
	Reason                        string                                `json:"reason"`
	ContributorPredicateOperators []ConstructionChoicePredicateOperator `json:"contributorPredicateOperators,omitempty"`
}

type ConstructionChoice struct {
	ChoiceID     string                         `json:"choiceId"`
	Source       ConstructionChoiceSource       `json:"source"`
	Route        []ConstructionRouteStep        `json:"route"`
	Presentation ConstructionChoicePresentation `json:"presentation"`
	Options      []ConstructionChoiceOption     `json:"options"`
}

type ConstructionChoicePresentation struct {
	Summary string                   `json:"summary"`
	Facts   []ConstructionChoiceFact `json:"facts"`
}

type ConstructionChoiceFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ConstructionRouteStep pins one directed traversal in a construction choice.
// Capability edge identity, endpoint identity, generated relationship label,
// and physical storage direction are all retained so apply cannot substitute
// a different route that happens to reach the same resource type.
type ConstructionRouteStep struct {
	EdgeID           string `json:"edgeId"`
	FromNodeID       string `json:"fromNodeId"`
	ToNodeID         string `json:"toNodeId"`
	FromResourceType string `json:"fromResourceType"`
	ToResourceType   string `json:"toResourceType"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection"`
	MatchMode        string `json:"matchMode"`
}

// ConstructionChoiceIdentity is the decoded, pinned identity used to
// re-resolve and reconstruct a server-issued choice during apply.
type ConstructionChoiceIdentity struct {
	Version              string
	Kind                 ConstructionChoiceSourceKind
	SnapshotToken        string
	SemanticContextToken string
	BuildID              string
	Source               ConstructionChoiceSource
	Route                []ConstructionRouteStep
}

type ConstructionRelatedResourceRouteChoice struct {
	ChoiceID           string
	TargetNodeID       string
	TargetResource     string
	AnchorColumnID     string
	AnchorKind         string
	AnchorNodeID       string
	AnchorResourceType string
	Route              []ConstructionRouteStep
}

type ConstructionRelatedFieldChoice struct {
	ChoiceID string
	Source   FieldChoiceSource
	Label    string
}

type constructionChoiceToken struct {
	Version              string                       `json:"version"`
	Kind                 ConstructionChoiceSourceKind `json:"kind"`
	SnapshotToken        string                       `json:"snapshotToken"`
	SemanticContextToken string                       `json:"semanticContextToken,omitempty"`
	BuildID              string                       `json:"buildId,omitempty"`
	Source               json.RawMessage              `json:"source"`
	Route                []ConstructionRouteStep      `json:"route"`
}

// NewFieldConstructionChoice exposes only projection modes already proven by
// the compiler. Indexed is a Builder annotation, not a compiler projection.
func NewFieldConstructionChoice(snapshotToken string, candidate Candidate) (ConstructionChoice, error) {
	return NewFieldConstructionChoiceForRoute(snapshotToken, nil, candidate)
}

// IsSupportedConstructionSourceType reports whether the recipe compiler
// accepts a declared logical type for a source projection.
func IsSupportedConstructionSourceType(logicalType string) bool {
	return recipe.IsValidConstructionLogicalType(logicalType)
}

// ValidateConstructionSourceType rejects values that the recipe compiler
// cannot emit and, when available, points to the nearest typed child field.
func ValidateConstructionSourceType(candidate Candidate, candidates []Candidate) error {
	if IsSupportedConstructionSourceType(candidate.LogicalType) {
		return nil
	}

	fieldPath := strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.")
	field := strings.TrimSpace(candidate.ResourceType + "." + fieldPath)
	message := fmt.Sprintf("source field %s has unsupported logical type %q", field, strings.TrimSpace(candidate.LogicalType))
	childPath := ""
	for _, child := range candidates {
		childFieldPath := strings.TrimPrefix(strings.TrimSpace(child.FieldPath), "root.")
		if child.NodeID != candidate.NodeID || child.ResourceType != candidate.ResourceType ||
			!strings.HasPrefix(childFieldPath, fieldPath+".") || !IsSupportedConstructionSourceType(child.LogicalType) {
			continue
		}
		if childPath == "" || len(childFieldPath) < len(childPath) || (len(childFieldPath) == len(childPath) && childFieldPath < childPath) {
			childPath = childFieldPath
		}
	}
	if childPath != "" {
		message += fmt.Sprintf("; choose a supported scalar child field such as %s.%s", candidate.ResourceType, childPath)
	} else {
		message += "; construction requires a supported scalar field"
	}
	return fmt.Errorf("%s", message)
}

// NewFieldConstructionChoiceForRoute exposes only projection modes already
// proven by the compiler for this exact route. A nil or empty route represents
// the zero-hop row-root choice.
func NewFieldConstructionChoiceForRoute(snapshotToken string, route []ConstructionRouteStep, candidate Candidate) (ConstructionChoice, error) {
	if strings.TrimSpace(snapshotToken) == "" || strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.NodeID) == "" || strings.TrimSpace(candidate.ResourceType) == "" || strings.TrimSpace(candidate.FieldPath) == "" {
		return ConstructionChoice{}, fmt.Errorf("snapshot token and exact candidate identity are required")
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionChoice{}, err
	}
	if err := ValidateConstructionSourceType(candidate, nil); err != nil {
		return ConstructionChoice{}, err
	}
	source := FieldChoiceSource{
		Kind: ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
		ResourceType: candidate.ResourceType, Path: candidate.FieldPath,
		Cardinality:        candidate.Cardinality,
		RepeatedBoundaries: append([]RepeatedBoundary(nil), candidate.RepeatedBoundaries...),
	}
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceField,
		SnapshotToken: snapshotToken, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionChoice{}, err
	}
	choice := ConstructionChoice{ChoiceID: choiceID, Source: source, Route: cloneConstructionRoute(route), Presentation: fieldChoicePresentation(source, candidate), Options: constructionOptionsForRoute(candidate, route)}
	if len(choice.Options) == 0 {
		return ConstructionChoice{}, fmt.Errorf("candidate %q has no compiler-proved construction output", candidate.ID)
	}
	return choice, nil
}

// NewGroupFieldConstructionChoice creates an ephemeral Group-key authority
// from compiler-issued source context and one authorized scalar candidate.
func NewGroupFieldConstructionChoice(snapshotToken string, route []ConstructionRouteStep, source GroupFieldChoiceSource, candidate Candidate) (ConstructionChoice, error) {
	source.Kind = ConstructionChoiceSourceGroupField
	if strings.TrimSpace(snapshotToken) == "" {
		return ConstructionChoice{}, fmt.Errorf("Group field choice requires a snapshot token")
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionChoice{}, err
	}
	if err := source.normalizeAndValidateInput(); err != nil {
		return ConstructionChoice{}, err
	}
	if err := validateGroupFieldChoiceSource(source, route); err != nil {
		return ConstructionChoice{}, err
	}
	if err := ValidateConstructionSourceType(candidate, nil); err != nil {
		return ConstructionChoice{}, err
	}
	if source.ValueKind != "SCALAR" {
		return ConstructionChoice{}, fmt.Errorf("scalar Group field choice must use SCALAR valueKind")
	}
	if err := validateGroupFieldCandidate(source, candidate); err != nil {
		return ConstructionChoice{}, err
	}
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceGroupField,
		SnapshotToken: snapshotToken, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionChoice{}, err
	}
	choice := ConstructionChoice{
		ChoiceID: choiceID, Source: source, Route: cloneConstructionRoute(route),
		Presentation: fieldChoicePresentation(FieldChoiceSource{
			Kind: ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
			ResourceType: candidate.ResourceType, Path: candidate.FieldPath, Cardinality: candidate.Cardinality,
			RepeatedBoundaries: append([]RepeatedBoundary(nil), candidate.RepeatedBoundaries...),
		}, candidate),
		Options: constructionOptionsForRoute(candidate, route),
	}
	if len(choice.Options) == 0 {
		return ConstructionChoice{}, fmt.Errorf("candidate %q has no compiler-proved construction output", candidate.ID)
	}
	return choice, nil
}

// NewGroupCodingIdentityChoice exposes a compiler-proved current Coding item
// as its normalized terminology identity without inventing an inventory
// family binding.
func NewGroupCodingIdentityChoice(snapshotToken string, route []ConstructionRouteStep, source GroupFieldChoiceSource) (ConstructionChoice, error) {
	source.Kind = ConstructionChoiceSourceGroupField
	source.ValueKind = "CODING_IDENTITY"
	if strings.TrimSpace(snapshotToken) == "" {
		return ConstructionChoice{}, fmt.Errorf("Coding identity choice requires a snapshot token")
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionChoice{}, err
	}
	if err := source.normalizeAndValidateInput(); err != nil {
		return ConstructionChoice{}, err
	}
	if err := validateGroupFieldChoiceSource(source, route); err != nil {
		return ConstructionChoice{}, err
	}
	rowChoice, err := DecodeRowChoiceID(source.RowChoiceID)
	if err != nil || rowChoice.Version != "row-choice/v1" || rowChoice.SnapshotToken != snapshotToken || rowChoice.Kind != RowChoiceExpandedScope ||
		rowChoice.FHIRType != "Coding" || rowChoice.Cardinality != RowChoiceMany || rowChoice.Shape != RowChoiceArray || rowChoice.Reference {
		return ConstructionChoice{}, fmt.Errorf("Coding identity choice requires one exact observed Coding row choice")
	}
	if source.Input != nil && source.Input.Kind == "RELATED_OCCURRENCE" {
		related := source.Input.RelatedOccurrence
		if related == nil || source.Shape != "CODING_IDENTITY" || rowChoice.Occurrence.OccurrenceID != related.TargetOccurrenceID ||
			rowChoice.Occurrence.NodeID != related.TargetNodeID || rowChoice.Occurrence.ResourceType != related.TargetResourceType ||
			!constructionRouteHasSuffix(rowChoice.Occurrence.Route, route) || rowChoice.Path != related.CodingSelector {
			return ConstructionChoice{}, fmt.Errorf("Coding identity choice must pin the exact related Coding selector")
		}
	} else if source.SourceRole != "CURRENT_ITEM" || source.Shape != "CODING_IDENTITY" || source.CanonicalPath != source.OwnerSelector ||
		rowChoice.Occurrence.OccurrenceID != source.OwnerOccurrenceID || rowChoice.Occurrence.NodeID != source.NodeID ||
		rowChoice.Occurrence.ResourceType != source.OwnerResourceType || !equalConstructionRouteSteps(rowChoice.Occurrence.Route, route) ||
		rowChoice.Path != source.OwnerSelector || source.CandidateID != "" {
		return ConstructionChoice{}, fmt.Errorf("Coding identity choice must pin the exact current Coding item selector")
	}
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceGroupField,
		SnapshotToken: snapshotToken, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionChoice{}, err
	}
	fieldLabel := source.CanonicalPath
	if source.Input != nil && source.Input.Kind == "RELATED_OCCURRENCE" && source.Input.RelatedOccurrence != nil {
		fieldLabel = source.Input.RelatedOccurrence.CodingSelector
	}
	return ConstructionChoice{
		ChoiceID: choiceID, Source: source, Route: cloneConstructionRoute(route),
		Presentation: ConstructionChoicePresentation{Summary: "Coding identity", Facts: []ConstructionChoiceFact{
			{Label: "Field", Value: fieldLabel},
			{Label: "Scope", Value: source.SourceRole},
		}},
	}, nil
}

func equalConstructionRouteSteps(left, right []ConstructionRouteStep) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func constructionRouteHasSuffix(full, suffix []ConstructionRouteStep) bool {
	if len(suffix) == 0 || len(suffix) > len(full) {
		return false
	}
	return equalConstructionRouteSteps(full[len(full)-len(suffix):], suffix)
}

func validGroupFieldSourceRole(role string) bool {
	switch role {
	case "ROOT", "OWNER", "ANCESTOR", "CURRENT_ITEM":
		return true
	default:
		return false
	}
}

func validateGroupFieldChoiceSource(source GroupFieldChoiceSource, route []ConstructionRouteStep) error {
	if err := source.normalizeAndValidateInput(); err != nil {
		return err
	}
	if source.Kind != ConstructionChoiceSourceGroupField || strings.TrimSpace(source.FHIRType) == "" || source.Reference {
		return fmt.Errorf("Group field choice requires an exact compiler-proved source identity")
	}
	if source.Input != nil && source.Input.Kind == "RELATED_OCCURRENCE" {
		related := source.Input.RelatedOccurrence
		if related == nil || source.Cardinality != "ONE" && source.ValueKind == "SCALAR" || source.ValueKind == "CODING_IDENTITY" && (source.Cardinality != "MANY" || source.FHIRType != "Coding") {
			return fmt.Errorf("related Group field choice has an invalid field cardinality or type")
		}
		if len(route) != len(related.Route) {
			return fmt.Errorf("related Group field choice route differs from its signed route")
		}
		for index, step := range related.Route {
			routeStep := route[index]
			if step.EdgeID != routeStep.EdgeID || step.FromNodeID != routeStep.FromNodeID || step.ToNodeID != routeStep.ToNodeID ||
				step.FromResourceType != routeStep.FromResourceType || step.ToResourceType != routeStep.ToResourceType ||
				step.Relationship != routeStep.Relationship || step.StorageDirection != routeStep.StorageDirection || step.MatchMode != routeStep.MatchMode {
				return fmt.Errorf("related Group field choice route step %d differs from its signed route", index)
			}
		}
		if source.NodeID != related.TargetNodeID || source.OwnerResourceType != related.TargetResourceType || source.ResourceType != related.ResourceType {
			return fmt.Errorf("related Group field choice terminal occurrence differs from its field owner")
		}
		return nil
	}
	if !validGroupFieldSourceRole(source.SourceRole) || strings.TrimSpace(source.SourceStageID) == "" ||
		strings.TrimSpace(source.SourceShapeID) == "" || strings.TrimSpace(source.SourceIdentityColumn) == "" ||
		strings.TrimSpace(source.OccurrenceID) == "" || strings.TrimSpace(source.ResourceType) == "" ||
		strings.TrimSpace(source.OwnerOccurrenceID) == "" || strings.TrimSpace(source.OwnerResourceType) == "" ||
		strings.TrimSpace(source.NodeID) == "" || strings.TrimSpace(source.CanonicalPath) == "" || source.Cardinality != "ONE" {
		return fmt.Errorf("Group field choice requires exact stage, shape, role, owner, and scalar field identity")
	}
	if (source.OwnerOccurrenceID == "") != (source.OwnerResourceType == "") {
		return fmt.Errorf("Group field choice owner occurrence and resource must be supplied together")
	}
	if source.SourceRole == "ANCESTOR" && strings.TrimSpace(source.BoundarySelector) == "" {
		return fmt.Errorf("ancestor Group field choice requires its exact repeated boundary selector")
	}
	if source.ValueKind == "SCALAR" {
		if strings.TrimSpace(source.CandidateID) == "" || source.Shape != "SCALAR" {
			return fmt.Errorf("scalar Group field choice requires a compiler-proved scalar candidate")
		}
	} else if source.ValueKind == "CODING_IDENTITY" {
		if source.CandidateID != "" || source.RowChoiceID == "" || source.Shape != "CODING_IDENTITY" || source.FHIRType != "Coding" ||
			source.SourceRole != "CURRENT_ITEM" || source.CanonicalPath != source.OwnerSelector {
			return fmt.Errorf("Coding identity Group field choice requires one exact current Coding item")
		}
	} else {
		return fmt.Errorf("Group field choice valueKind is unsupported")
	}
	if len(route) > 0 {
		last := route[len(route)-1]
		if last.ToNodeID != source.NodeID || last.ToResourceType != source.OwnerResourceType {
			return fmt.Errorf("Group field choice route does not end at the exact field owner")
		}
	}
	return nil
}

func validateGroupFieldCandidate(source GroupFieldChoiceSource, candidate Candidate) error {
	if source.Input != nil && source.Input.Kind == "RELATED_OCCURRENCE" {
		related := source.Input.RelatedOccurrence
		if related == nil || candidatePathForGroupFieldSource(source) != canonicalPath(candidate.FieldPath) ||
			candidate.ID != related.CandidateID || candidate.NodeID != related.TargetNodeID || candidate.ResourceType != related.TargetResourceType ||
			!candidate.Observed || !candidate.Populated || !IsSupportedConstructionSourceType(candidate.LogicalType) ||
			!containsProjectionMode(candidate.ProjectionModes, ProjectionScalar) || IsRepeatedCardinality(candidate.Cardinality) {
			return fmt.Errorf("related Group field choice does not match one populated scalar candidate")
		}
		return nil
	}
	if candidatePathForGroupFieldSource(source) != canonicalPath(candidate.FieldPath) ||
		candidate.ID != source.CandidateID || candidate.NodeID != source.NodeID || candidate.ResourceType != source.OwnerResourceType ||
		!candidate.Populated || !IsSupportedConstructionSourceType(candidate.LogicalType) ||
		source.ValueKind != "SCALAR" {
		return fmt.Errorf("Group field choice does not match one populated compiler-proved scalar candidate")
	}
	if source.SourceRole == "ROOT" {
		if len(candidate.RepeatedBoundaries) != 0 || IsRepeatedCardinality(candidate.Cardinality) ||
			!containsProjectionMode(candidate.ProjectionModes, ProjectionScalar) {
			return fmt.Errorf("root Group field choice must be a one-valued scalar source")
		}
		return nil
	}
	consumed := make(map[string]struct{}, len(source.ConsumedBoundaries)+2)
	orderedConsumed := make([]string, 0, len(source.ConsumedBoundaries)+2)
	if len(source.ConsumedBoundaries) == 0 {
		for _, boundary := range []string{source.OwnerSelector, source.BoundarySelector} {
			if selector := canonicalPath(boundary); selector != "" {
				if _, duplicate := consumed[selector]; !duplicate {
					consumed[selector] = struct{}{}
					orderedConsumed = append(orderedConsumed, selector)
				}
			}
		}
	} else {
		for _, boundary := range source.ConsumedBoundaries {
			selector := canonicalPath(boundary)
			if selector == "" || selector != boundary {
				return fmt.Errorf("Group field choice has a non-canonical consumed repeated boundary")
			}
			if _, duplicate := consumed[selector]; duplicate {
				return fmt.Errorf("Group field choice repeats a consumed boundary")
			}
			consumed[selector] = struct{}{}
			orderedConsumed = append(orderedConsumed, selector)
		}
		if source.SourceRole == "CURRENT_ITEM" {
			if _, ok := consumed[canonicalPath(source.OwnerSelector)]; !ok {
				return fmt.Errorf("current-item Group field choice lacks its exact compiler item selector")
			}
		}
		if source.SourceRole == "ANCESTOR" {
			if _, ok := consumed[canonicalPath(source.BoundarySelector)]; !ok {
				return fmt.Errorf("ancestor Group field choice lacks its exact compiler boundary selector")
			}
		}
	}
	matchedIndex := -1
	for _, boundary := range candidate.RepeatedBoundaries {
		selector := canonicalPath(boundary.Path)
		if _, ok := consumed[selector]; !ok {
			return fmt.Errorf("Group field choice has an unconsumed repeated boundary %q", boundary.Path)
		}
		foundIndex := -1
		for index := matchedIndex + 1; index < len(orderedConsumed); index++ {
			if orderedConsumed[index] == selector {
				foundIndex = index
				break
			}
		}
		if foundIndex < 0 {
			return fmt.Errorf("Group field choice repeated boundaries do not follow the compiled consumed prefix")
		}
		matchedIndex = foundIndex
	}
	if IsRepeatedCardinality(candidate.Cardinality) && len(candidate.RepeatedBoundaries) == 0 {
		return fmt.Errorf("repeated Group field choice has no exact consumed item or ancestor boundary")
	}
	if source.SourceRole == "CURRENT_ITEM" && source.OwnerSelector == "" {
		return fmt.Errorf("current-item Group field choice requires the exact active item selector")
	}
	if source.SourceRole == "ANCESTOR" && source.BoundarySelector == "" {
		return fmt.Errorf("ancestor Group field choice requires the exact repeated boundary selector")
	}
	return nil
}

func candidatePathForGroupFieldSource(source GroupFieldChoiceSource) string {
	if source.Input != nil && source.Input.Kind == "RELATED_OCCURRENCE" {
		if source.Input.RelatedOccurrence == nil {
			return ""
		}
		return canonicalPath(source.Input.RelatedOccurrence.CanonicalPath)
	}
	path := canonicalPath(source.CanonicalPath)
	switch source.SourceRole {
	case "ROOT", "OWNER":
		return path
	case "ANCESTOR":
		return joinGroupFieldPath(source.BoundarySelector, path)
	case "CURRENT_ITEM":
		return joinGroupFieldPath(source.OwnerSelector, path)
	default:
		return ""
	}
}

func joinGroupFieldPath(prefix, path string) string {
	prefix, path = canonicalPath(prefix), canonicalPath(path)
	if prefix == "" || path == "" {
		return ""
	}
	return prefix + "." + path
}

func containsProjectionMode(modes []ProjectionMode, want ProjectionMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}

// NewConstructionRelatedResourceRouteChoiceFromAnchor pins a route to one
// exact compiler-proven row anchor as well as its terminal resource.
func NewConstructionRelatedResourceRouteChoiceFromAnchor(
	snapshotToken, stageID, anchorColumnID, anchorKind, anchorNodeID, anchorResource,
	targetNodeID, targetResource string, route []ConstructionRouteStep,
) (ConstructionRelatedResourceRouteChoice, error) {
	if strings.TrimSpace(snapshotToken) == "" || strings.TrimSpace(stageID) == "" || strings.TrimSpace(targetNodeID) == "" || strings.TrimSpace(targetResource) == "" || len(route) == 0 {
		return ConstructionRelatedResourceRouteChoice{}, fmt.Errorf("snapshot, input stage, exact row anchor, terminal node, resource type, and non-empty route are required")
	}
	if strings.TrimSpace(anchorColumnID) == "" || strings.TrimSpace(anchorKind) == "" || strings.TrimSpace(anchorNodeID) == "" || strings.TrimSpace(anchorResource) == "" {
		return ConstructionRelatedResourceRouteChoice{}, fmt.Errorf("related-resource route anchor identity is incomplete")
	}
	if anchorKind != "root" && anchorKind != "rootContributors" && anchorKind != "activeRelatedRecord" {
		return ConstructionRelatedResourceRouteChoice{}, fmt.Errorf("related-resource route anchor kind %q is unsupported", anchorKind)
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionRelatedResourceRouteChoice{}, err
	}
	terminal := route[len(route)-1]
	if terminal.ToNodeID != targetNodeID || terminal.ToResourceType != targetResource {
		return ConstructionRelatedResourceRouteChoice{}, fmt.Errorf("route does not end at the exact terminal resource")
	}
	if route[0].FromNodeID != anchorNodeID || route[0].FromResourceType != anchorResource {
		return ConstructionRelatedResourceRouteChoice{}, fmt.Errorf("route does not start at the exact selected row anchor")
	}
	source := RelatedResourceChoiceSource{
		Kind: ConstructionChoiceSourceRelatedResource, StageID: stageID,
		AnchorColumnID: anchorColumnID, AnchorKind: anchorKind, AnchorNodeID: anchorNodeID,
		AnchorResourceType: anchorResource, NodeID: targetNodeID, ResourceType: targetResource,
	}
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceRelatedResource,
		SnapshotToken: snapshotToken, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionRelatedResourceRouteChoice{}, err
	}
	return ConstructionRelatedResourceRouteChoice{
		ChoiceID: choiceID, TargetNodeID: targetNodeID, TargetResource: targetResource,
		AnchorColumnID: anchorColumnID, AnchorKind: anchorKind, AnchorNodeID: anchorNodeID,
		AnchorResourceType: anchorResource, Route: cloneConstructionRoute(route),
	}, nil
}

// NewConstructionRelatedFieldChoice issues a stage-bound choice for one
// compiler catalog scalar field on the active exact terminal resource.
func NewConstructionRelatedFieldChoice(snapshotToken, stageID string, candidate Candidate) (ConstructionRelatedFieldChoice, error) {
	if strings.TrimSpace(snapshotToken) == "" || strings.TrimSpace(stageID) == "" ||
		strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.NodeID) == "" ||
		strings.TrimSpace(candidate.ResourceType) == "" || strings.TrimSpace(candidate.FieldPath) == "" ||
		strings.TrimSpace(candidate.LogicalType) == "" ||
		(candidate.Cardinality != "optional_one" && candidate.Cardinality != "required_one") {
		return ConstructionRelatedFieldChoice{}, fmt.Errorf("snapshot, stage, and exact scalar field identity are required")
	}
	source := FieldChoiceSource{
		Kind: ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
		ResourceType: candidate.ResourceType, Path: candidate.FieldPath, Cardinality: candidate.Cardinality,
	}
	identitySource := RelatedFieldChoiceSource{
		Kind: ConstructionChoiceSourceRelatedField, StageID: stageID, CandidateID: candidate.ID,
		NodeID: candidate.NodeID, ResourceType: candidate.ResourceType, Path: candidate.FieldPath,
		Cardinality: candidate.Cardinality, LogicalType: candidate.LogicalType,
	}
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceRelatedField,
		SnapshotToken: snapshotToken, Source: identitySource,
	})
	if err != nil {
		return ConstructionRelatedFieldChoice{}, err
	}
	label := strings.TrimSpace(candidate.Label)
	if label == "" {
		label = strings.TrimSpace(candidate.ResourceType + "." + candidate.FieldPath)
	}
	return ConstructionRelatedFieldChoice{ChoiceID: choiceID, Source: source, Label: label}, nil
}

// NewConstructionCodedGroupChoice issues a choice for one generated root
// Coding[] path on one exact source-projection stage.
func NewConstructionCodedGroupChoice(
	snapshot Snapshot,
	outputID, stageID string,
	occurrence RowChoiceOccurrence,
	facts RowChoiceFacts,
) (ConstructionCodedGroupChoice, error) {
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return ConstructionCodedGroupChoice{}, err
	}
	if strings.TrimSpace(snapshot.Identity.SchemaDigest) == "" || strings.TrimSpace(outputID) == "" ||
		strings.TrimSpace(stageID) == "" || strings.TrimSpace(occurrence.OccurrenceID) == "" ||
		strings.TrimSpace(occurrence.NodeID) == "" || occurrence.ResourceType == "" {
		return ConstructionCodedGroupChoice{}, fmt.Errorf("coded group choice requires an exact snapshot, output, stage, and root occurrence")
	}
	if len(occurrence.Route) != 0 {
		return ConstructionCodedGroupChoice{}, fmt.Errorf("coded group choice must use a direct root occurrence")
	}
	if facts.ResourceType != occurrence.ResourceType {
		return ConstructionCodedGroupChoice{}, fmt.Errorf("coded group path resource %q differs from root resource %q", facts.ResourceType, occurrence.ResourceType)
	}
	if facts.Shape != RowChoiceArray || facts.Cardinality != RowChoiceMany || facts.FHIRType != "Coding" || facts.Reference ||
		strings.TrimSpace(facts.CanonicalPath) == "" {
		return ConstructionCodedGroupChoice{}, fmt.Errorf("coded group path %q must be a non-reference repeated Coding array", facts.CanonicalPath)
	}
	source := CodedGroupChoiceSource{
		Kind: ConstructionChoiceSourceCodedGroup, OutputID: outputID, StageID: stageID,
		OccurrenceID: occurrence.OccurrenceID, NodeID: occurrence.NodeID,
		ResourceType: occurrence.ResourceType, Path: facts.CanonicalPath,
		SchemaDigest: snapshot.Identity.SchemaDigest,
	}
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceCodedGroup,
		SnapshotToken: snapshot.Token, Source: source,
	})
	if err != nil {
		return ConstructionCodedGroupChoice{}, err
	}
	label := occurrence.ResourceType + " · " + strings.ReplaceAll(facts.CanonicalPath, ".", " › ")
	return ConstructionCodedGroupChoice{
		ChoiceID: choiceID, OccurrenceID: occurrence.OccurrenceID,
		ResourceType: occurrence.ResourceType, CodingPath: facts.CanonicalPath, Label: label,
	}, nil
}

// NewSemanticConstructionChoice binds an inventory observation to the exact
// compiler candidate for its value path. Snapshot, semantic context, and build
// identities pin the choice without adding separate mutable context fields.
func NewSemanticConstructionChoice(snapshotToken, semanticContextToken, buildID string, source SemanticBindingChoiceSource, candidate Candidate, ownerRecordsProved ...bool) (ConstructionChoice, error) {
	return NewSemanticConstructionChoiceForRoute(snapshotToken, semanticContextToken, buildID, nil, source, candidate, ownerRecordsProved...)
}

// NewSemanticConstructionChoiceForRoute pins an observed semantic binding to
// the exact compiler-proved route and terminal candidate.
func NewSemanticConstructionChoiceForRoute(snapshotToken, semanticContextToken, buildID string, route []ConstructionRouteStep, source SemanticBindingChoiceSource, candidate Candidate, ownerRecordsProved ...bool) (ConstructionChoice, error) {
	if (source.GroupOutputID == "") != (source.GroupStageID == "") ||
		(source.GroupOutputID != "" && (strings.TrimSpace(source.GroupOutputID) != source.GroupOutputID || strings.TrimSpace(source.GroupStageID) != source.GroupStageID)) {
		return ConstructionChoice{}, fmt.Errorf("semantic group choice must pin an exact output and stage together")
	}
	if strings.TrimSpace(snapshotToken) == "" || strings.TrimSpace(semanticContextToken) == "" || strings.TrimSpace(buildID) == "" ||
		strings.TrimSpace(source.ConceptID) == "" || strings.TrimSpace(source.BindingID) == "" ||
		strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.SourcePath) == "" ||
		strings.TrimSpace(source.FieldPath) == "" || strings.TrimSpace(source.ValueSelector) == "" || strings.TrimSpace(source.LogicalType) == "" ||
		strings.TrimSpace(source.RuleVersion) == "" || source.SchemaVersion <= 0 {
		return ConstructionChoice{}, fmt.Errorf("snapshot, semantic context, build, and exact semantic source identity are required")
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionChoice{}, err
	}
	if err := ValidateConstructionSourceType(candidate, nil); err != nil {
		return ConstructionChoice{}, err
	}
	if !IsSupportedConstructionSourceType(source.LogicalType) {
		return ConstructionChoice{}, fmt.Errorf("semantic source field %s.%s has unsupported logical type %q", source.ResourceType, source.FieldPath, strings.TrimSpace(source.LogicalType))
	}
	if err := validateSemanticFallback(source.LogicalType, source.ValueFallbacks, source.FallbackOnBlankString); err != nil {
		return ConstructionChoice{}, err
	}
	if err := validateSemanticCategoryChoiceSource(source); err != nil {
		return ConstructionChoice{}, err
	}
	resolvedValuePath := canonicalPath(source.FieldPath)
	selectorPath := canonicalOwnedSemanticPath(source.OwningScope, source.ValueSelector)
	candidatePath := canonicalPath(candidate.FieldPath)
	if candidate.ID == "" || candidate.NodeID == "" || candidate.ResourceType != source.ResourceType ||
		resolvedValuePath == "" || selectorPath == "" || candidatePath == "" ||
		candidatePath != candidate.FieldPath || candidatePath != resolvedValuePath || resolvedValuePath != selectorPath {
		return ConstructionChoice{}, fmt.Errorf("semantic source has no matching compiler-proved capability candidate")
	}
	source.Kind = ConstructionChoiceSourceSemantic
	source.CandidateID = candidate.ID
	source.NodeID = candidate.NodeID
	source.FieldPath = resolvedValuePath
	source.ValueSelector = selectorPath
	source.ValueFallbacks = append([]string(nil), source.ValueFallbacks...)
	source.ExtensionURLPath = append([]string(nil), source.ExtensionURLPath...)
	source.Cardinality = candidate.Cardinality
	source.RepeatedBoundaries = append([]RepeatedBoundary(nil), candidate.RepeatedBoundaries...)
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceSemantic,
		SnapshotToken: snapshotToken, SemanticContextToken: semanticContextToken,
		BuildID: buildID, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionChoice{}, err
	}
	choice := ConstructionChoice{ChoiceID: choiceID, Source: source, Route: cloneConstructionRoute(route), Presentation: semanticChoicePresentation(source, candidate), Options: constructionOptionsForRoute(candidate, route)}
	if len(ownerRecordsProved) > 0 && ownerRecordsProved[0] && ownerRecordsSourceSupported(source, candidate) {
		choice.Options = append(choice.Options, ConstructionChoiceOption{
			Form: ConstructionChoiceOwnerRecords, Shape: ConstructionChoiceList,
			Decision: ConstructionChoiceRequiresDecision, Preservation: ConstructionChoicePreserving,
			RowEffect: ConstructionChoicePreservesRows, Support: ConstructionChoiceSupported,
			Reason: "Keep each repeated FHIR owner as an ordered record with its value and source evidence.",
		})
	}
	if len(choice.Options) == 0 {
		return ConstructionChoice{}, fmt.Errorf("semantic source %q has no compiler-proved construction output", source.SourcePath)
	}
	return choice, nil
}

// NewSemanticFrameConstructionChoice issues an exact family-and-route token
// for a correlated code/value source, without binding it to one category code.
func NewSemanticFrameConstructionChoice(
	snapshotToken, semanticContextToken, buildID string,
	source SemanticFrameChoiceSource,
	route []ConstructionRouteStep,
	candidate Candidate,
) (ConstructionChoice, error) {
	if strings.TrimSpace(snapshotToken) == "" || strings.TrimSpace(semanticContextToken) == "" || strings.TrimSpace(buildID) == "" ||
		strings.TrimSpace(source.AnchorConceptID) == "" || strings.TrimSpace(source.Family.BindingID) == "" ||
		strings.TrimSpace(source.Family.ResourceType) == "" || strings.TrimSpace(source.Family.SourcePath) == "" ||
		source.Family.OwningScope != strings.TrimSpace(source.Family.OwningScope) || strings.TrimSpace(source.Family.KeyPath) == "" ||
		strings.TrimSpace(source.Family.ValuePath) == "" || strings.TrimSpace(source.Family.LogicalType) == "" ||
		strings.TrimSpace(source.Family.RuleVersion) == "" || source.Family.SchemaVersion <= 0 ||
		strings.TrimSpace(source.CandidateID) == "" || strings.TrimSpace(source.NodeID) == "" ||
		strings.TrimSpace(source.FieldPath) == "" || strings.TrimSpace(candidate.ID) == "" {
		return ConstructionChoice{}, fmt.Errorf("snapshot, semantic context, build, frame family, and exact candidate identity are required")
	}
	if err := validateSemanticFallback(source.Family.LogicalType, source.Family.ValueFallbacks, source.Family.FallbackOnBlankString); err != nil {
		return ConstructionChoice{}, err
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionChoice{}, err
	}
	if err := ValidateConstructionSourceType(candidate, nil); err != nil {
		return ConstructionChoice{}, err
	}
	if source.Kind != ConstructionChoiceSourceSemanticFrame || source.Family.ResourceType != candidate.ResourceType ||
		source.CandidateID != candidate.ID || source.NodeID != candidate.NodeID ||
		canonicalPath(source.FieldPath) != canonicalPath(candidate.FieldPath) ||
		canonicalPath(frameFamilyValuePath(source.Family)) != canonicalPath(candidate.FieldPath) {
		return ConstructionChoice{}, fmt.Errorf("semantic frame source does not match its compiler candidate")
	}
	source.RepeatedBoundaries = append([]RepeatedBoundary(nil), candidate.RepeatedBoundaries...)
	source.Cardinality = candidate.Cardinality
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceSemanticFrame,
		SnapshotToken: snapshotToken, SemanticContextToken: semanticContextToken,
		BuildID: buildID, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionChoice{}, err
	}
	return ConstructionChoice{
		ChoiceID: choiceID, Source: source, Route: cloneConstructionRoute(route),
		Presentation: ConstructionChoicePresentation{
			Summary: source.Family.ResourceType + " coded values",
			Facts: nonEmptyConstructionFacts(append([]ConstructionChoiceFact{
				{Label: "Value type", Value: source.Family.LogicalType},
				{Label: "Value cardinality", Value: cardinalityPresentation(candidate.Cardinality)},
				{Label: "Paired source", Value: source.Family.SourcePath},
				{Label: "Route", Value: semanticFrameRouteSummary(route, source.Family.ResourceType)},
			}, semanticFallbackFacts(source.Family.ValueFallbacks, source.Family.FallbackOnBlankString)...)),
		},
		Options: constructionOptionsForRoute(candidate, route),
	}, nil
}

func frameFamilyValuePath(family SemanticFrameFamily) string {
	owner, value := canonicalPath(family.OwningScope), canonicalPath(family.ValuePath)
	if owner == "" {
		return value
	}
	if value == "" {
		return owner
	}
	return owner + "." + value
}

func semanticFrameRouteSummary(route []ConstructionRouteStep, targetResource string) string {
	if len(route) == 0 {
		return "same resource"
	}
	parts := make([]string, 0, len(route)+1)
	parts = append(parts, route[0].FromResourceType)
	for _, step := range route {
		parts = append(parts, step.Relationship, step.ToResourceType)
	}
	if strings.TrimSpace(targetResource) != "" && route[len(route)-1].ToResourceType != targetResource {
		parts = append(parts, targetResource)
	}
	return strings.Join(parts, " → ")
}

func ownerRecordsSourceSupported(source SemanticBindingChoiceSource, candidate Candidate) bool {
	ownerPath := canonicalSemanticPath(source.OwningScope)
	if ownerPath == "" || strings.TrimSpace(source.System) == "" || strings.TrimSpace(source.Code) == "" ||
		strings.TrimSpace(source.KeySelector) == "" || strings.TrimSpace(source.ValueSelector) == "" {
		return false
	}
	for _, boundary := range candidate.RepeatedBoundaries {
		if canonicalSemanticPath(boundary.Path) == ownerPath {
			return true
		}
	}
	return false
}

// DecodeConstructionChoiceID validates the token envelope and returns the
// typed pinned identity. The caller must still re-authorize and re-resolve it.
func DecodeConstructionChoiceID(choiceID string) (ConstructionChoiceIdentity, error) {
	if len(choiceID) == 0 || len(choiceID) > ConstructionChoiceIDMaxLength {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id has an invalid length")
	}
	parts := strings.Split(choiceID, ".")
	if len(parts) != 3 || (parts[0] != "cc1" && parts[0] != "cc2") {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id has an unsupported version or format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id payload is invalid")
	}
	providedDigest, err := hex.DecodeString(parts[2])
	if err != nil || len(providedDigest) != sha256.Size {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id digest is invalid")
	}
	expectedDigest := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(providedDigest, expectedDigest[:]) != 1 {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id digest does not match its payload")
	}
	var token constructionChoiceToken
	if err := decodeChoiceJSON(payload, &token); err != nil {
		return ConstructionChoiceIdentity{}, fmt.Errorf("decode construction choice id payload: %w", err)
	}
	if (parts[0] == "cc1" && token.Version != "construction-choice/v1") ||
		(parts[0] == "cc2" && token.Version != "construction-choice/v2") || strings.TrimSpace(token.SnapshotToken) == "" {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id is missing its pinned version or snapshot")
	}
	if parts[0] == "cc1" && len(token.Route) != 0 {
		return ConstructionChoiceIdentity{}, fmt.Errorf("legacy construction choice cannot carry a route")
	}
	if err := validateConstructionRoute(token.Route); err != nil {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice route is invalid: %w", err)
	}
	identity := ConstructionChoiceIdentity{
		Version: token.Version, Kind: token.Kind, SnapshotToken: token.SnapshotToken,
		SemanticContextToken: token.SemanticContextToken, BuildID: token.BuildID,
		Route: cloneConstructionRoute(token.Route),
	}
	switch token.Kind {
	case ConstructionChoiceSourceField:
		var source FieldChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode field choice source: %w", err)
		}
		if source.Kind != token.Kind || strings.TrimSpace(source.CandidateID) == "" || strings.TrimSpace(source.NodeID) == "" || strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.Path) == "" {
			return ConstructionChoiceIdentity{}, fmt.Errorf("field choice source identity is incomplete")
		}
		if token.SemanticContextToken != "" || token.BuildID != "" {
			return ConstructionChoiceIdentity{}, fmt.Errorf("field choice id cannot carry semantic inventory context")
		}
		identity.Source = source
	case ConstructionChoiceSourceSemantic:
		var source SemanticBindingChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode semantic choice source: %w", err)
		}
		if token.SemanticContextToken == "" || token.BuildID == "" || source.Kind != token.Kind ||
			strings.TrimSpace(source.ConceptID) == "" || strings.TrimSpace(source.BindingID) == "" ||
			strings.TrimSpace(source.CandidateID) == "" || strings.TrimSpace(source.NodeID) == "" ||
			strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.SourcePath) == "" ||
			strings.TrimSpace(source.FieldPath) == "" || strings.TrimSpace(source.ValueSelector) == "" ||
			strings.TrimSpace(source.RuleVersion) == "" || source.SchemaVersion <= 0 {
			return ConstructionChoiceIdentity{}, fmt.Errorf("semantic choice source identity is incomplete")
		}
		if (source.GroupOutputID == "") != (source.GroupStageID == "") ||
			(source.GroupOutputID != "" && (strings.TrimSpace(source.GroupOutputID) != source.GroupOutputID || strings.TrimSpace(source.GroupStageID) != source.GroupStageID)) {
			return ConstructionChoiceIdentity{}, fmt.Errorf("semantic group choice output/stage identity is incomplete")
		}
		if err := validateSemanticCategoryChoiceSource(source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("semantic choice source identity is invalid: %w", err)
		}
		identity.Source = source
	case ConstructionChoiceSourceGroupField:
		var source GroupFieldChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode Group field choice source: %w", err)
		}
		if source.Kind != token.Kind || token.SemanticContextToken != "" || token.BuildID != "" {
			return ConstructionChoiceIdentity{}, fmt.Errorf("Group field choice identity is inconsistent")
		}
		if err := validateGroupFieldChoiceSource(source, identity.Route); err != nil {
			return ConstructionChoiceIdentity{}, err
		}
		identity.Source = source
	case ConstructionChoiceSourceSemanticFrame:
		var source SemanticFrameChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode semantic frame choice source: %w", err)
		}
		if token.SemanticContextToken == "" || token.BuildID == "" || source.Kind != token.Kind ||
			strings.TrimSpace(source.AnchorConceptID) == "" || strings.TrimSpace(source.Family.BindingID) == "" ||
			strings.TrimSpace(source.Family.ResourceType) == "" || strings.TrimSpace(source.Family.SourcePath) == "" ||
			source.Family.OwningScope != strings.TrimSpace(source.Family.OwningScope) || strings.TrimSpace(source.Family.KeyPath) == "" ||
			strings.TrimSpace(source.Family.ValuePath) == "" || strings.TrimSpace(source.Family.LogicalType) == "" ||
			strings.TrimSpace(source.Family.RuleVersion) == "" || source.Family.SchemaVersion <= 0 ||
			strings.TrimSpace(source.CandidateID) == "" || strings.TrimSpace(source.NodeID) == "" ||
			strings.TrimSpace(source.FieldPath) == "" || len(token.Route) > 0 && token.Route[len(token.Route)-1].ToNodeID != source.NodeID {
			return ConstructionChoiceIdentity{}, fmt.Errorf("semantic frame choice source identity is incomplete or inconsistent")
		}
		identity.Source = source
	case ConstructionChoiceSourceRelatedResource:
		var source RelatedResourceChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode related resource choice source: %w", err)
		}
		if source.Kind != token.Kind || strings.TrimSpace(source.StageID) == "" || strings.TrimSpace(source.NodeID) == "" || strings.TrimSpace(source.ResourceType) == "" ||
			token.SemanticContextToken != "" || token.BuildID != "" || len(token.Route) == 0 ||
			token.Route[len(token.Route)-1].ToNodeID != source.NodeID || token.Route[len(token.Route)-1].ToResourceType != source.ResourceType {
			return ConstructionChoiceIdentity{}, fmt.Errorf("related resource choice identity is incomplete or inconsistent")
		}
		identity.Source = source
	case ConstructionChoiceSourceRelatedField:
		var source RelatedFieldChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode related field choice source: %w", err)
		}
		if source.Kind != token.Kind || strings.TrimSpace(source.StageID) == "" ||
			strings.TrimSpace(source.CandidateID) == "" || strings.TrimSpace(source.NodeID) == "" ||
			strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.Path) == "" ||
			strings.TrimSpace(source.LogicalType) == "" ||
			(source.Cardinality != "optional_one" && source.Cardinality != "required_one") ||
			token.SemanticContextToken != "" || token.BuildID != "" || len(token.Route) != 0 {
			return ConstructionChoiceIdentity{}, fmt.Errorf("related field choice identity is incomplete or inconsistent")
		}
		identity.Source = source
	case ConstructionChoiceSourceCodedGroup:
		var source CodedGroupChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode coded group choice source: %w", err)
		}
		if source.Kind != token.Kind || strings.TrimSpace(source.OutputID) == "" ||
			strings.TrimSpace(source.StageID) == "" || strings.TrimSpace(source.OccurrenceID) == "" ||
			strings.TrimSpace(source.NodeID) == "" || strings.TrimSpace(source.ResourceType) == "" ||
			strings.TrimSpace(source.Path) == "" || strings.TrimSpace(source.SchemaDigest) == "" ||
			!strings.HasSuffix(source.Path, "coding[]") || token.SemanticContextToken != "" ||
			token.BuildID != "" || len(token.Route) != 0 {
			return ConstructionChoiceIdentity{}, fmt.Errorf("coded group choice identity is incomplete or inconsistent")
		}
		identity.Source = source
	default:
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id has an unsupported source kind")
	}
	return identity, nil
}

func encodeConstructionChoiceID(identity ConstructionChoiceIdentity) (string, error) {
	if identity.Version != "construction-choice/v2" || strings.TrimSpace(identity.SnapshotToken) == "" {
		return "", fmt.Errorf("construction choice identity has no supported version or snapshot")
	}
	if err := validateConstructionRoute(identity.Route); err != nil {
		return "", fmt.Errorf("construction choice route is invalid: %w", err)
	}
	var kind ConstructionChoiceSourceKind
	switch source := identity.Source.(type) {
	case FieldChoiceSource:
		kind = source.Kind
	case GroupFieldChoiceSource:
		kind = source.Kind
	case SemanticBindingChoiceSource:
		kind = source.Kind
	case SemanticFrameChoiceSource:
		kind = source.Kind
	case RelatedResourceChoiceSource:
		kind = source.Kind
	case RelatedFieldChoiceSource:
		kind = source.Kind
	case CodedGroupChoiceSource:
		kind = source.Kind
	default:
		return "", fmt.Errorf("construction choice identity has an unsupported source")
	}
	if kind != identity.Kind {
		return "", fmt.Errorf("construction choice identity kind does not match its source")
	}
	if (kind == ConstructionChoiceSourceSemantic || kind == ConstructionChoiceSourceSemanticFrame) && (identity.SemanticContextToken == "" || identity.BuildID == "") {
		return "", fmt.Errorf("semantic choice identity requires inventory context and build")
	}
	if kind == ConstructionChoiceSourceField && (identity.SemanticContextToken != "" || identity.BuildID != "") {
		return "", fmt.Errorf("field choice identity cannot include semantic inventory context")
	}
	if kind == ConstructionChoiceSourceGroupField && (identity.SemanticContextToken != "" || identity.BuildID != "") {
		return "", fmt.Errorf("Group field choice identity cannot include semantic inventory context")
	}
	if kind == ConstructionChoiceSourceRelatedResource && (identity.SemanticContextToken != "" || identity.BuildID != "" || len(identity.Route) == 0) {
		return "", fmt.Errorf("related resource choice identity requires a route and cannot include semantic inventory context")
	}
	if (kind == ConstructionChoiceSourceRelatedField || kind == ConstructionChoiceSourceCodedGroup) &&
		(identity.SemanticContextToken != "" || identity.BuildID != "" || len(identity.Route) != 0) {
		return "", fmt.Errorf("related field choice identity is stage-bound and cannot include route or semantic inventory context")
	}
	source, err := json.Marshal(identity.Source)
	if err != nil {
		return "", fmt.Errorf("marshal construction choice source: %w", err)
	}
	payload, err := json.Marshal(constructionChoiceToken{
		Version: identity.Version, Kind: identity.Kind, SnapshotToken: identity.SnapshotToken,
		SemanticContextToken: identity.SemanticContextToken, BuildID: identity.BuildID, Source: source,
		Route: cloneConstructionRoute(identity.Route),
	})
	if err != nil {
		return "", fmt.Errorf("marshal construction choice identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	choiceID := "cc2." + base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(digest[:])
	if len(choiceID) > ConstructionChoiceIDMaxLength {
		return "", fmt.Errorf("construction choice identity exceeds the maximum token length")
	}
	return choiceID, nil
}

func validateConstructionRoute(route []ConstructionRouteStep) error {
	for index, step := range route {
		if strings.TrimSpace(step.EdgeID) == "" || strings.TrimSpace(step.FromNodeID) == "" || strings.TrimSpace(step.ToNodeID) == "" ||
			strings.TrimSpace(step.FromResourceType) == "" || strings.TrimSpace(step.ToResourceType) == "" ||
			strings.TrimSpace(step.Relationship) == "" || strings.TrimSpace(step.StorageDirection) == "" {
			return fmt.Errorf("route step %d is missing exact edge identity", index)
		}
		if step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND" {
			return fmt.Errorf("route step %d has an unsupported storage direction", index)
		}
		if step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED" {
			return fmt.Errorf("route step %d has an unsupported match mode", index)
		}
		if index > 0 && (route[index-1].ToNodeID != step.FromNodeID || route[index-1].ToResourceType != step.FromResourceType) {
			return fmt.Errorf("route is discontinuous at step %d", index)
		}
	}
	return nil
}

func cloneConstructionRoute(route []ConstructionRouteStep) []ConstructionRouteStep {
	if route == nil {
		return []ConstructionRouteStep{}
	}
	return append([]ConstructionRouteStep{}, route...)
}

func fieldChoicePresentation(source FieldChoiceSource, candidate Candidate) ConstructionChoicePresentation {
	summary := strings.TrimSpace(candidate.Label)
	if summary == "" {
		summary = strings.TrimSpace(source.ResourceType + "." + source.Path)
	}
	facts := []ConstructionChoiceFact{
		{Label: "FHIR field", Value: strings.TrimSpace(source.ResourceType + "." + source.Path)},
		{Label: "Value type", Value: strings.TrimSpace(candidate.LogicalType)},
		{Label: "Repetition", Value: cardinalityPresentation(source.Cardinality)},
	}
	return ConstructionChoicePresentation{Summary: summary, Facts: nonEmptyConstructionFacts(facts)}
}

func semanticChoicePresentation(source SemanticBindingChoiceSource, candidate Candidate) ConstructionChoicePresentation {
	summary := strings.TrimSpace(candidate.Label)
	if summary == "" {
		summary = strings.TrimSpace(source.Code)
	}
	facts := []ConstructionChoiceFact{
		{Label: "Code system", Value: strings.TrimSpace(source.System)},
		{Label: "Code", Value: strings.TrimSpace(source.Code)},
		{Label: "Value field", Value: strings.TrimSpace(source.ResourceType + "." + source.FieldPath)},
		{Label: "Value type", Value: strings.TrimSpace(source.LogicalType)},
		{Label: "Source", Value: strings.TrimSpace(source.SourcePath)},
		{Label: "Owner", Value: strings.TrimSpace(source.OwningScope)},
		{Label: "Choice arm", Value: strings.TrimSpace(source.ChoiceArm)},
		{Label: "Value cardinality", Value: cardinalityPresentation(source.Cardinality)},
	}
	facts = append(facts, semanticFallbackFacts(source.ValueFallbacks, source.FallbackOnBlankString)...)
	return ConstructionChoicePresentation{Summary: summary, Facts: nonEmptyConstructionFacts(facts)}
}

func semanticFallbackFacts(fallbacks []string, fallbackOnBlankString bool) []ConstructionChoiceFact {
	if len(fallbacks) == 0 {
		return nil
	}
	rule := "Use the fallback when the primary value is absent"
	if fallbackOnBlankString {
		rule = "Use the fallback when the primary string is blank"
	}
	return []ConstructionChoiceFact{
		{Label: "Fallback value", Value: strings.Join(fallbacks, ", ")},
		{Label: "Fallback rule", Value: rule},
	}
}

func validateSemanticFallback(logicalType string, fallbacks []string, fallbackOnBlankString bool) error {
	for index, fallback := range fallbacks {
		if strings.TrimSpace(fallback) == "" {
			return fmt.Errorf("semantic fallback selector %d is empty", index)
		}
	}
	if fallbackOnBlankString && (logicalType != "string" || len(fallbacks) == 0) {
		return fmt.Errorf("blank-string semantic fallback requires string values and at least one fallback selector")
	}
	return nil
}

func cardinalityPresentation(cardinality string) string {
	if IsRepeatedCardinality(cardinality) {
		return "Repeated values"
	}
	return "Single value"
}

func nonEmptyConstructionFacts(facts []ConstructionChoiceFact) []ConstructionChoiceFact {
	result := make([]ConstructionChoiceFact, 0, len(facts))
	for _, fact := range facts {
		if fact.Label != "" && fact.Value != "" {
			result = append(result, fact)
		}
	}
	return result
}

func decodeChoiceJSON(raw []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func constructionOptions(candidate Candidate) []ConstructionChoiceOption {
	if !IsSupportedConstructionSourceType(candidate.LogicalType) {
		return nil
	}
	options := make([]ConstructionChoiceOption, 0, 4)
	for _, projection := range []ProjectionMode{ProjectionScalar, ProjectionFirst, ProjectionArray, ProjectionDistinctArray} {
		form, found := constructionForm(projection)
		if !found || !containsProjection(candidate.ProjectionModes, projection) {
			continue
		}
		repeated := IsRepeatedCardinality(candidate.Cardinality)
		if form == ConstructionChoiceFirst && !repeated {
			continue
		}
		option := ConstructionChoiceOption{
			Form: form, Decision: ConstructionChoiceRequiresDecision,
			Shape: ConstructionChoiceScalar, Preservation: ConstructionChoiceReducing,
			RowEffect: ConstructionChoicePreservesRows,
			Support:   ConstructionChoiceSupported, Reason: "The compiler proved this output form for the exact field path.",
		}
		switch form {
		case ConstructionChoiceValue:
			option.Preservation = ConstructionChoicePreserving
			if !repeated {
				option.Decision = ConstructionChoiceDefault
			}
		case ConstructionChoiceAll:
			option.Shape = ConstructionChoiceList
			option.Preservation = ConstructionChoicePreserving
			if repeated {
				option.Decision = ConstructionChoiceDefault
			}
		case ConstructionChoiceDistinct:
			option.Shape = ConstructionChoiceList
		}
		options = append(options, option)
	}
	return options
}

func constructionOptionsForRoute(candidate Candidate, route []ConstructionRouteStep) []ConstructionChoiceOption {
	if !IsSupportedConstructionSourceType(candidate.LogicalType) {
		return nil
	}
	options := constructionOptions(candidate)
	if len(route) == 0 || IsRepeatedCardinality(candidate.Cardinality) || !containsProjection(candidate.ProjectionModes, ProjectionScalar) {
		return options
	}
	hasAll := false
	for _, option := range options {
		hasAll = hasAll || option.Form == ConstructionChoiceAll
	}
	if !hasAll {
		options = append(options, ConstructionChoiceOption{
			Form: ConstructionChoiceAll, Shape: ConstructionChoiceList,
			Decision: ConstructionChoiceRequiresDecision, Preservation: ConstructionChoicePreserving,
			RowEffect: ConstructionChoicePreservesRows, Support: ConstructionChoiceSupported,
			Reason: "Collect the scalar field from every matching resource on this exact route.",
		})
	}
	options = append(options,
		ConstructionChoiceOption{
			Form: ConstructionChoiceCount, Shape: ConstructionChoiceScalar,
			Decision: ConstructionChoiceRequiresDecision, Preservation: ConstructionChoiceReducing,
			RowEffect: ConstructionChoicePreservesRows, Support: ConstructionChoiceSupported,
			Reason: "Count distinct matching source records; a row with no match has zero.",
		},
		ConstructionChoiceOption{
			Form: ConstructionChoicePresence, Shape: ConstructionChoiceScalar,
			Decision: ConstructionChoiceRequiresDecision, Preservation: ConstructionChoiceReducing,
			RowEffect: ConstructionChoicePreservesRows, Support: ConstructionChoiceSupported,
			Reason: "Show whether at least one matching source record exists.",
		},
	)
	operators := []ConstructionChoicePredicateOperator{ConstructionChoicePredicateExists}
	switch strings.ToLower(strings.TrimSpace(candidate.LogicalType)) {
	case "string", "code":
		operators = append(operators, ConstructionChoicePredicateEquals)
	}
	for index := range options {
		switch options[index].Form {
		case ConstructionChoiceAll, ConstructionChoiceCount, ConstructionChoicePresence:
			options[index].ContributorPredicateOperators = append([]ConstructionChoicePredicateOperator(nil), operators...)
		}
	}
	return options
}

func constructionForm(mode ProjectionMode) (ConstructionChoiceForm, bool) {
	switch mode {
	case ProjectionScalar:
		return ConstructionChoiceValue, true
	case ProjectionFirst:
		return ConstructionChoiceFirst, true
	case ProjectionArray:
		return ConstructionChoiceAll, true
	case ProjectionDistinctArray:
		return ConstructionChoiceDistinct, true
	default:
		return "", false
	}
}

func canonicalSemanticPath(path string) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "root.")
	return canonicalPath(path)
}

func canonicalOwnedSemanticPath(ownerPath, valuePath string) string {
	ownerPath = canonicalSemanticPath(ownerPath)
	valuePath = canonicalSemanticPath(valuePath)
	if ownerPath == "" || valuePath == "" || valuePath == ownerPath || strings.HasPrefix(valuePath, ownerPath+".") {
		return valuePath
	}
	return ownerPath + "." + valuePath
}

func containsProjection(modes []ProjectionMode, want ProjectionMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}
