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

// TestLookupSourceBinderPairingMatchesCanonicalAcceptSet pins the
// strict_json.go LookupSource discriminator against the single authority on
// writable source semantics, authoringv2 ColumnSource.validate: the binder's
// early XOR checks must neither accept a payload the domain layer would
// reject nor narrow a payload the domain accepts (a rejected round-86 variant
// blanket-blocked the identifier arm and broke the UPDATE_COLUMN round-trip
// of resolver-produced identifierBySystem columns). The identifier binding is
// accepted alone, mirroring the writable branch in semantic_types.go, with
// the kind==identifierBySystem gate and ValidateIdentifierBinding left to the
// domain authority the lookup object alone cannot see; the binder is
// deliberately one notch narrower (an explicit empty/blank match or path
// pointer next to identifier is rejected here although TrimSpace would make
// it empty at the domain).
func TestLookupSourceBinderPairingMatchesCanonicalAcceptSet(t *testing.T) {
	accept := []string{
		`{"match":"category","path":"category.coding[]","projectionMode":"VALUE"}`,
		`{"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"},"key":{"system":"http://loinc.org","code":"718-7"}}`,
		`{"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal","selectionScope":"SYSTEM_VERSION","versionPath":"version","valueScope":"CODING"},"key":{"system":"http://loinc.org","code":""}}`,
		`{"extension":{"ownerPath":"extension[]","urlPath":["http://x/y"],"valuePath":"valueString","logicalType":"string"}}`,
		`{"identifier":{"ownerPath":"identifier[]","systemPath":"system","valuePath":"value","systemURI":"urn:acct","logicalType":"string"}}`,
	}
	for _, raw := range accept {
		var value LookupSource
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			t.Fatalf("contract-legal lookup rejected: %s -> %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"}}`,
		`{"key":{"system":"urn:x","code":"c"}}`,
		`{"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"},"key":{"system":"urn:x","code":"c"},"match":"category"}`,
		`{"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"},"key":{"system":"urn:x","code":"c"},"path":"component[]"}`,
		`{"extension":{"ownerPath":"extension[]","urlPath":["http://x/y"],"valuePath":"valueString","logicalType":"string"},"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"}}`,
		`{"extension":{"ownerPath":"extension[]","urlPath":["http://x/y"],"valuePath":"valueString","logicalType":"string"},"key":{"system":"urn:x","code":"c"}}`,
		`{"extension":{"ownerPath":"extension[]","urlPath":["http://x/y"],"valuePath":"valueString","logicalType":"string"},"match":"ext"}`,
		`{"identifier":{"ownerPath":"identifier[]","systemPath":"system","valuePath":"value","systemURI":"urn:acct","logicalType":"string"},"match":"identifier"}`,
		`{"identifier":{"ownerPath":"identifier[]","systemPath":"system","valuePath":"value","systemURI":"urn:acct","logicalType":"string"},"path":"identifier[]","match":"identifier"}`,
		`{"identifier":{"ownerPath":"identifier[]","systemPath":"system","valuePath":"value","systemURI":"urn:acct","logicalType":"string"},"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"},"key":{"system":"urn:x","code":"c"}}`,
		`{"identifier":{"ownerPath":"identifier[]","systemPath":"system","valuePath":"value","systemURI":"urn:acct","logicalType":"string"},"binding":{"keyPath":"component[].code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"}}`,
		`{"identifier":{"ownerPath":"identifier[]","systemPath":"system","valuePath":"value","systemURI":"urn:acct","logicalType":"string"},"key":{"system":"urn:x","code":"c"}}`,
		`{"identifier":{"ownerPath":"identifier[]","systemPath":"system","valuePath":"value","systemURI":"urn:acct","logicalType":"string"},"extension":{"ownerPath":"extension[]","urlPath":["http://x/y"],"valuePath":"valueString","logicalType":"string"}}`,
	} {
		var value LookupSource
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			t.Fatalf("non-canonical lookup accepted at generated boundary (domain layer would reject or silently drop): %s", raw)
		}
	}
}
