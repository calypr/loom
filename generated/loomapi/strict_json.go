package loomapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

// strictDecodeGenerated keeps documented request bodies closed at the generated
// HTTP boundary (mutation requests plus read/search POST bodies whose schemas
// declare additionalProperties:false). The generated Fiber binder otherwise
// silently drops unknown JSON members before the authoring domain can enforce
// its semantics-version guard.
func strictDecodeGenerated(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func (value *ApplyCommandsRequest) UnmarshalJSON(raw []byte) error {
	type wire ApplyCommandsRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ApplyCommandsRequest(decoded)
	return nil
}

func (value *AuthoringCommand) UnmarshalJSON(raw []byte) error {
	type wire AuthoringCommand
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = AuthoringCommand(decoded)
	return nil
}

func (value *PopulationMemberRemovalProposalRequest) UnmarshalJSON(raw []byte) error {
	type wire PopulationMemberRemovalProposalRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = PopulationMemberRemovalProposalRequest(decoded)
	return nil
}

func (value *SelectionResourceRef) UnmarshalJSON(raw []byte) error {
	type wire SelectionResourceRef
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = SelectionResourceRef(decoded)
	return nil
}

func (value *Column) UnmarshalJSON(raw []byte) error {
	type wire Column
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = Column(decoded)
	return nil
}

func (value *ColumnSource) UnmarshalJSON(raw []byte) error {
	type wire ColumnSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ColumnSource(decoded)
	return nil
}

func (value *FieldSource) UnmarshalJSON(raw []byte) error {
	type wire FieldSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = FieldSource(decoded)
	return nil
}

func (value *AggregateSource) UnmarshalJSON(raw []byte) error {
	type wire AggregateSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = AggregateSource(decoded)
	return nil
}

func (value *ContributorPredicate) UnmarshalJSON(raw []byte) error {
	type wire ContributorPredicate
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ContributorPredicate(decoded)
	return nil
}

func (value *LookupSource) UnmarshalJSON(raw []byte) error {
	type wire LookupSource
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	if decoded.Extension != nil {
		if decoded.Binding != nil || decoded.Key != nil || decoded.Match != nil || decoded.Path != nil {
			return errors.New("extension lookup requires only extension binding")
		}
	} else if decoded.Binding != nil || decoded.Key != nil {
		if decoded.Binding == nil || decoded.Key == nil || decoded.Match != nil || decoded.Path != nil {
			return errors.New("correlated lookup requires only binding and key")
		}
	} else if decoded.Match == nil {
		return errors.New("legacy lookup requires match")
	}
	*value = LookupSource(decoded)
	return nil
}

func (value *RelatedSelection) UnmarshalJSON(raw []byte) error {
	type wire RelatedSelection
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = RelatedSelection(decoded)
	return nil
}

func (value *SelectionCreateRequest) UnmarshalJSON(raw []byte) error {
	type wire SelectionCreateRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	switch decoded.Source.Kind {
	case "resources":
		if decoded.Source.Resources == nil || decoded.Source.PublishedOutput != nil || decoded.Source.SelectionRevision != nil {
			return errors.New("resources selection requires only the resources payload")
		}
	case "publishedOutput":
		if decoded.Source.PublishedOutput == nil || decoded.Source.Resources != nil || decoded.Source.SelectionRevision != nil {
			return errors.New("publishedOutput selection requires only the publishedOutput payload")
		}
	case "selectionRevision":
		if decoded.Source.SelectionRevision == nil || decoded.Source.Resources != nil || decoded.Source.PublishedOutput != nil {
			return errors.New("selectionRevision selection requires only the selectionRevision payload")
		}
	default:
		return errors.New("unsupported selection source kind")
	}
	*value = SelectionCreateRequest(decoded)
	return nil
}

func (value *TableShapeCapabilitiesRequest) UnmarshalJSON(raw []byte) error {
	type wire TableShapeCapabilitiesRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = TableShapeCapabilitiesRequest(decoded)
	return nil
}

func (value *TableShapeCategoryDiscoveryRequest) UnmarshalJSON(raw []byte) error {
	type wire TableShapeCategoryDiscoveryRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = TableShapeCategoryDiscoveryRequest(decoded)
	return nil
}

func (value *TableShapeResolutionRequest) UnmarshalJSON(raw []byte) error {
	type wire TableShapeResolutionRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	payloads := 0
	if decoded.Pivot != nil {
		payloads++
	}
	if decoded.Unpivot != nil {
		payloads++
	}
	if decoded.Derived != nil {
		payloads++
	}
	if payloads != 1 {
		return errors.New("table-shape resolution requires exactly one selection payload")
	}
	switch decoded.Kind {
	case TableShapeResolutionRequestKindPIVOT:
		if decoded.Pivot == nil || decoded.Unpivot != nil || decoded.Derived != nil {
			return errors.New("PIVOT resolution requires only the pivot payload")
		}
	case TableShapeResolutionRequestKindUNPIVOT:
		if decoded.Unpivot == nil || decoded.Pivot != nil || decoded.Derived != nil {
			return errors.New("UNPIVOT resolution requires only the unpivot payload")
		}
	case TableShapeResolutionRequestKindDERIVED:
		if decoded.Derived == nil || decoded.Pivot != nil || decoded.Unpivot != nil {
			return errors.New("DERIVED resolution requires only the derived payload")
		}
	default:
		return errors.New("unsupported table-shape resolution kind")
	}
	*value = TableShapeResolutionRequest(decoded)
	return nil
}

func (value *TableShapeProposalRequest) UnmarshalJSON(raw []byte) error {
	type wire TableShapeProposalRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = TableShapeProposalRequest(decoded)
	return nil
}

func (value *TableShapeOperandSelection) UnmarshalJSON(raw []byte) error {
	type wire TableShapeOperandSelection
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	choice := decoded.ChoiceId != nil
	resolution := decoded.ResolutionId != nil
	literal := decoded.Literal != nil
	switch decoded.Kind {
	case TableShapeOperandSelectionKindCATALOGCHOICE:
		if !choice || resolution || literal {
			return errors.New("CATALOG_CHOICE operand requires only choiceId")
		}
	case TableShapeOperandSelectionKindRESOLUTIONOUTPUT:
		if !resolution || choice || literal {
			return errors.New("RESOLUTION_OUTPUT operand requires only resolutionId")
		}
	case TableShapeOperandSelectionKindLITERAL:
		if !literal || choice || resolution {
			return errors.New("LITERAL operand requires only literal")
		}
	default:
		return errors.New("unsupported table-shape operand kind")
	}
	*value = TableShapeOperandSelection(decoded)
	return nil
}

func (value *ArtifactPrepareRequest) UnmarshalJSON(raw []byte) error {
	type wire ArtifactPrepareRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ArtifactPrepareRequest(decoded)
	return nil
}

func (value *ColumnSourceRequest) UnmarshalJSON(raw []byte) error {
	type wire ColumnSourceRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ColumnSourceRequest(decoded)
	return nil
}

func (value *ConfiguredColumnContextRequest) UnmarshalJSON(raw []byte) error {
	type wire ConfiguredColumnContextRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ConfiguredColumnContextRequest(decoded)
	return nil
}

func (value *ConstructionCapabilitiesRequest) UnmarshalJSON(raw []byte) error {
	type wire ConstructionCapabilitiesRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ConstructionCapabilitiesRequest(decoded)
	return nil
}

func (value *ConstructionCategoryDiscoveryRequest) UnmarshalJSON(raw []byte) error {
	type wire ConstructionCategoryDiscoveryRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ConstructionCategoryDiscoveryRequest(decoded)
	return nil
}

func (value *ConstructionChoiceProposalRequest) UnmarshalJSON(raw []byte) error {
	type wire ConstructionChoiceProposalRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ConstructionChoiceProposalRequest(decoded)
	return nil
}

func (value *ConstructionChoiceSearchRequest) UnmarshalJSON(raw []byte) error {
	type wire ConstructionChoiceSearchRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ConstructionChoiceSearchRequest(decoded)
	return nil
}

func (value *ConstructionInputsRequest) UnmarshalJSON(raw []byte) error {
	type wire ConstructionInputsRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ConstructionInputsRequest(decoded)
	return nil
}

func (value *ConstructionProposalRequest) UnmarshalJSON(raw []byte) error {
	type wire ConstructionProposalRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ConstructionProposalRequest(decoded)
	return nil
}

func (value *ExplicitGroupCreateRequest) UnmarshalJSON(raw []byte) error {
	type wire ExplicitGroupCreateRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ExplicitGroupCreateRequest(decoded)
	return nil
}

func (value *FrameSourceOptionsRequest) UnmarshalJSON(raw []byte) error {
	type wire FrameSourceOptionsRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = FrameSourceOptionsRequest(decoded)
	return nil
}

func (value *InterpretationPreviewRequest) UnmarshalJSON(raw []byte) error {
	type wire InterpretationPreviewRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = InterpretationPreviewRequest(decoded)
	return nil
}

func (value *InterpretationRevisionCreateRequest) UnmarshalJSON(raw []byte) error {
	type wire InterpretationRevisionCreateRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = InterpretationRevisionCreateRequest(decoded)
	return nil
}

func (value *InterpretationRevisionFromColumnRequest) UnmarshalJSON(raw []byte) error {
	type wire InterpretationRevisionFromColumnRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = InterpretationRevisionFromColumnRequest(decoded)
	return nil
}

func (value *PopulationRoutesRequest) UnmarshalJSON(raw []byte) error {
	type wire PopulationRoutesRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = PopulationRoutesRequest(decoded)
	return nil
}

func (value *RelatedExpandChoiceSearchRequest) UnmarshalJSON(raw []byte) error {
	type wire RelatedExpandChoiceSearchRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = RelatedExpandChoiceSearchRequest(decoded)
	return nil
}

func (value *RelatedExpandContributorChoiceSearchRequest) UnmarshalJSON(raw []byte) error {
	type wire RelatedExpandContributorChoiceSearchRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = RelatedExpandContributorChoiceSearchRequest(decoded)
	return nil
}

func (value *RelatedFieldChoiceSearchRequest) UnmarshalJSON(raw []byte) error {
	type wire RelatedFieldChoiceSearchRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = RelatedFieldChoiceSearchRequest(decoded)
	return nil
}

func (value *RowDefinitionProposalRequest) UnmarshalJSON(raw []byte) error {
	type wire RowDefinitionProposalRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = RowDefinitionProposalRequest(decoded)
	return nil
}

func (value *SchemaFieldSearchRequest) UnmarshalJSON(raw []byte) error {
	type wire SchemaFieldSearchRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = SchemaFieldSearchRequest(decoded)
	return nil
}

func (value *SemanticInventoryBrowseRequest) UnmarshalJSON(raw []byte) error {
	type wire SemanticInventoryBrowseRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = SemanticInventoryBrowseRequest(decoded)
	return nil
}

// Review follow-up (round 83 adversarial review F1): the remaining plain
// request-body schemas bound by the generated strict adapter. The 9th gap
// reported there, the update-explorer-config body, is an x-go-type alias of
// authoringv2.Workspace whose strictness would also affect persisted-draft
// decoding; closing that boundary needs a type-level decision and is waived
// here with a backlog entry (docs/EXPLORER_BACKLOG.md).

// AssessRowChangeRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *AssessRowChangeRequest) UnmarshalJSON(raw []byte) error {
	type wire AssessRowChangeRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = AssessRowChangeRequest(decoded)
	return nil
}

// CandidateSearchRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *CandidateSearchRequest) UnmarshalJSON(raw []byte) error {
	type wire CandidateSearchRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = CandidateSearchRequest(decoded)
	return nil
}

// CellTraceRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *CellTraceRequest) UnmarshalJSON(raw []byte) error {
	type wire CellTraceRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = CellTraceRequest(decoded)
	return nil
}

// PopulationMappingRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *PopulationMappingRequest) UnmarshalJSON(raw []byte) error {
	type wire PopulationMappingRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = PopulationMappingRequest(decoded)
	return nil
}

// PreviewRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *PreviewRequest) UnmarshalJSON(raw []byte) error {
	type wire PreviewRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = PreviewRequest(decoded)
	return nil
}

// PublishRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *PublishRequest) UnmarshalJSON(raw []byte) error {
	type wire PublishRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = PublishRequest(decoded)
	return nil
}

// ReconcileRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *ReconcileRequest) UnmarshalJSON(raw []byte) error {
	type wire ReconcileRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = ReconcileRequest(decoded)
	return nil
}

// RowLineageRequest keeps the documented additionalProperties:false contract at the
// generated HTTP boundary; required-field validation stays in the handlers.
func (value *RowLineageRequest) UnmarshalJSON(raw []byte) error {
	type wire RowLineageRequest
	var decoded wire
	if err := strictDecodeGenerated(raw, &decoded); err != nil {
		return err
	}
	*value = RowLineageRequest(decoded)
	return nil
}
