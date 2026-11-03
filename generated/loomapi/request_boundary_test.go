package loomapi

import (
	"encoding/json"
	"testing"
)

// TestRequestBodySchemasRejectUnknownMembersAtGeneratedBoundary pins the OpenAPI
// promise that every request body schema (additionalProperties: false) stays
// closed at the generated HTTP boundary, mirroring strict_json.go's contract:
// "keep mutation requests closed at the generated HTTP boundary". An ignored
// unknown member turns a client typo (e.g. a mistyped filter or outputId) into
// a silently dropped field instead of the documented 400 (the shared error
// handler renders binder rejections as INVALID_REQUEST). The
// update-explorer-config body is an x-go-type alias of authoringv2.Workspace
// and is waived in strict_json.go pending that type-boundary decision.
func TestRequestBodySchemasRejectUnknownMembersAtGeneratedBoundary(t *testing.T) {
	requests := map[string]func() any{
		"ArtifactPrepareRequest":                      func() any { return &ArtifactPrepareRequest{} },
		"ApplyCommandsRequest":                        func() any { return &ApplyCommandsRequest{} },
		"AssessRowChangeRequest":                      func() any { return &AssessRowChangeRequest{} },
		"CandidateSearchRequest":                      func() any { return &CandidateSearchRequest{} },
		"CellTraceRequest":                            func() any { return &CellTraceRequest{} },
		"ColumnSourceRequest":                         func() any { return &ColumnSourceRequest{} },
		"ConfiguredColumnContextRequest":              func() any { return &ConfiguredColumnContextRequest{} },
		"ConstructionCapabilitiesRequest":             func() any { return &ConstructionCapabilitiesRequest{} },
		"ConstructionCategoryDiscoveryRequest":        func() any { return &ConstructionCategoryDiscoveryRequest{} },
		"ConstructionChoiceProposalRequest":           func() any { return &ConstructionChoiceProposalRequest{} },
		"ConstructionChoiceSearchRequest":             func() any { return &ConstructionChoiceSearchRequest{} },
		"ConstructionInputsRequest":                   func() any { return &ConstructionInputsRequest{} },
		"ConstructionProposalRequest":                 func() any { return &ConstructionProposalRequest{} },
		"ExplicitGroupCreateRequest":                  func() any { return &ExplicitGroupCreateRequest{} },
		"FrameSourceOptionsRequest":                   func() any { return &FrameSourceOptionsRequest{} },
		"InterpretationPreviewRequest":                func() any { return &InterpretationPreviewRequest{} },
		"InterpretationRevisionCreateRequest":         func() any { return &InterpretationRevisionCreateRequest{} },
		"InterpretationRevisionFromColumnRequest":     func() any { return &InterpretationRevisionFromColumnRequest{} },
		"PopulationMemberRemovalProposalRequest":      func() any { return &PopulationMemberRemovalProposalRequest{} },
		"PopulationMappingRequest":                    func() any { return &PopulationMappingRequest{} },
		"PopulationRoutesRequest":                     func() any { return &PopulationRoutesRequest{} },
		"PreviewRequest":                              func() any { return &PreviewRequest{} },
		"PublishRequest":                              func() any { return &PublishRequest{} },
		"RelatedExpandChoiceSearchRequest":            func() any { return &RelatedExpandChoiceSearchRequest{} },
		"RelatedExpandContributorChoiceSearchRequest": func() any { return &RelatedExpandContributorChoiceSearchRequest{} },
		"RelatedFieldChoiceSearchRequest":             func() any { return &RelatedFieldChoiceSearchRequest{} },
		"RowDefinitionProposalRequest":                func() any { return &RowDefinitionProposalRequest{} },
		"RowLineageRequest":                           func() any { return &RowLineageRequest{} },
		"ReconcileRequest":                            func() any { return &ReconcileRequest{} },
		"SchemaFieldSearchRequest":                    func() any { return &SchemaFieldSearchRequest{} },
		"SelectionCreateRequest":                      func() any { return &SelectionCreateRequest{} },
		"SemanticInventoryBrowseRequest":              func() any { return &SemanticInventoryBrowseRequest{} },
		"TableShapeCapabilitiesRequest":               func() any { return &TableShapeCapabilitiesRequest{} },
		"TableShapeCategoryDiscoveryRequest":          func() any { return &TableShapeCategoryDiscoveryRequest{} },
		"TableShapeProposalRequest":                   func() any { return &TableShapeProposalRequest{} },
		"TableShapeResolutionRequest":                 func() any { return &TableShapeResolutionRequest{} },
	}
	for name, factory := range requests {
		t.Run(name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(`{"loomBoundaryProbe":true}`), factory()); err == nil {
				t.Fatalf("%s accepted an unknown member; OpenAPI declares additionalProperties:false", name)
			}
		})
	}
}

// TestNewlyClosedRequestBodiesStillDecodeMinimalPayloads guards against
// over-strictness: the request types that only gained strictness (no semantic
// rules) must still decode their empty envelope; required-field validation
// stays with the documented handler validators.
func TestNewlyClosedRequestBodiesStillDecodeMinimalPayloads(t *testing.T) {
	minimalAllowed := []func() any{
		func() any { return &ArtifactPrepareRequest{} },
		func() any { return &ColumnSourceRequest{} },
		func() any { return &ConfiguredColumnContextRequest{} },
		func() any { return &ConstructionCapabilitiesRequest{} },
		func() any { return &ConstructionCategoryDiscoveryRequest{} },
		func() any { return &ConstructionChoiceProposalRequest{} },
		func() any { return &ConstructionChoiceSearchRequest{} },
		func() any { return &ConstructionInputsRequest{} },
		func() any { return &ConstructionProposalRequest{} },
		func() any { return &ExplicitGroupCreateRequest{} },
		func() any { return &FrameSourceOptionsRequest{} },
		func() any { return &InterpretationPreviewRequest{} },
		func() any { return &InterpretationRevisionCreateRequest{} },
		func() any { return &InterpretationRevisionFromColumnRequest{} },
		func() any { return &PopulationRoutesRequest{} },
		func() any { return &RelatedExpandChoiceSearchRequest{} },
		func() any { return &RelatedExpandContributorChoiceSearchRequest{} },
		func() any { return &RelatedFieldChoiceSearchRequest{} },
		func() any { return &RowDefinitionProposalRequest{} },
		func() any { return &SchemaFieldSearchRequest{} },
		func() any { return &SemanticInventoryBrowseRequest{} },
	}
	for _, factory := range minimalAllowed {
		if err := json.Unmarshal([]byte(`{}`), factory()); err != nil {
			t.Fatalf("minimal envelope rejected: %v", err)
		}
	}
}
