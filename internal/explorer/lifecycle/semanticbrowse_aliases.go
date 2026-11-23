package lifecycle

import "github.com/calypr/loom/internal/explorer/semanticbrowse"

type BrowseSemanticInventoryRequest = semanticbrowse.BrowseSemanticInventoryRequest
type FieldPresentationRequest = semanticbrowse.FieldPresentationRequest
type FieldPresentationReason = semanticbrowse.FieldPresentationReason
type FieldPresentationResultKind = semanticbrowse.FieldPresentationResultKind
type FieldPresentationDecisionKind = semanticbrowse.FieldPresentationDecisionKind
type SemanticInventoryItemIdentityKind = semanticbrowse.SemanticInventoryItemIdentityKind
type FieldPresentationAccess = semanticbrowse.FieldPresentationAccess
type SemanticInventoryItem = semanticbrowse.SemanticInventoryItem

const (
	FieldPresentationResultUnavailable  = semanticbrowse.FieldPresentationResultUnavailable
	FieldPresentationResultReady        = semanticbrowse.FieldPresentationResultReady
	FieldPresentationDecisionKeepRaw    = semanticbrowse.FieldPresentationDecisionKeepRaw
	FieldPresentationDecisionGroup      = semanticbrowse.FieldPresentationDecisionGroup
	SemanticInventoryIdentityExact      = semanticbrowse.SemanticInventoryIdentityExact
	SemanticInventoryIdentityFamily     = semanticbrowse.SemanticInventoryIdentityFamily
	FieldPresentationAccessSourceFields = semanticbrowse.FieldPresentationAccessSourceFields

	FieldPresentationMissingSystem              = semanticbrowse.FieldPresentationMissingSystem
	FieldPresentationMissingCode                = semanticbrowse.FieldPresentationMissingCode
	FieldPresentationTextOnly                   = semanticbrowse.FieldPresentationTextOnly
	FieldPresentationAmbiguousAnchor            = semanticbrowse.FieldPresentationAmbiguousAnchor
	FieldPresentationMixedChoice                = semanticbrowse.FieldPresentationMixedChoice
	FieldPresentationUnsupportedValueProjection = semanticbrowse.FieldPresentationUnsupportedValueProjection
	FieldPresentationUnsupportedVersion         = semanticbrowse.FieldPresentationUnsupportedVersion
	FieldPresentationUnknownSchemaMember        = semanticbrowse.FieldPresentationUnknownSchemaMember
	FieldPresentationUnknownCodeClassification  = semanticbrowse.FieldPresentationUnknownCodeClassification
	FieldPresentationUnsupportedSelector        = semanticbrowse.FieldPresentationUnsupportedSelector
	FieldPresentationIncompleteSource           = semanticbrowse.FieldPresentationIncompleteSource
	FieldPresentationWalkFailed                 = semanticbrowse.FieldPresentationWalkFailed
	FieldPresentationTruncatedEvidence          = semanticbrowse.FieldPresentationTruncatedEvidence
	FieldPresentationUnrepresentedMetadata      = semanticbrowse.FieldPresentationUnrepresentedMetadata
	FieldPresentationCoverageUnavailable        = semanticbrowse.FieldPresentationCoverageUnavailable
	FieldPresentationIncompleteCoverage         = semanticbrowse.FieldPresentationIncompleteCoverage
	FieldPresentationNoExactRoute               = semanticbrowse.FieldPresentationNoExactRoute
	FieldPresentationUnsupportedCurrentStage    = semanticbrowse.FieldPresentationUnsupportedCurrentStage
	FieldPresentationStaleContext               = semanticbrowse.FieldPresentationStaleContext
	FieldPresentationReplacementUnavailable     = semanticbrowse.FieldPresentationReplacementUnavailable
	FieldPresentationSourceFieldsInaccessible   = semanticbrowse.FieldPresentationSourceFieldsInaccessible
	FieldPresentationCandidateNotPopulated      = semanticbrowse.FieldPresentationCandidateNotPopulated
	FieldPresentationCandidateNotAddable        = semanticbrowse.FieldPresentationCandidateNotAddable
)
