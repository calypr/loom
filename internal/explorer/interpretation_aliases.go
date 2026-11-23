package explorer

import "github.com/calypr/loom/internal/explorer/interpretation"

type InterpretationLibraryID = interpretation.InterpretationLibraryID
type InterpretationRevisionID = interpretation.InterpretationRevisionID
type InterpretationRuleID = interpretation.InterpretationRuleID
type InterpretationContentDigest = interpretation.InterpretationContentDigest
type InterpretationPriority = interpretation.InterpretationPriority
type InterpretationLibrary = interpretation.InterpretationLibrary
type InterpretationRevision = interpretation.InterpretationRevision
type InterpretationApplicability = interpretation.InterpretationApplicability
type InterpretationStructuralMatch = interpretation.InterpretationStructuralMatch
type InterpretationStructuralCandidate = interpretation.InterpretationStructuralCandidate
type InterpretationRule = interpretation.InterpretationRule
type InterpretationFeatureDefinition = interpretation.InterpretationFeatureDefinition
type ResolvedInterpretation = interpretation.ResolvedInterpretation

var (
	ErrInvalidInterpretationID        = interpretation.ErrInvalidInterpretationID
	ErrInvalidInterpretationDigest    = interpretation.ErrInvalidInterpretationDigest
	ErrInvalidInterpretationProject   = interpretation.ErrInvalidInterpretationProject
	ErrAmbiguousMapping               = interpretation.ErrAmbiguousMapping
	ErrNoInterpretationMatch          = interpretation.ErrNoInterpretationMatch
	ErrInterpretationParentConflict   = interpretation.ErrInterpretationParentConflict
	ErrImmutableInterpretation        = interpretation.ErrImmutableInterpretation
	ErrInterpretationIdentityMismatch = interpretation.ErrInterpretationIdentityMismatch
)

var (
	NewInterpretationLibraryID     = interpretation.NewInterpretationLibraryID
	NewInterpretationRevisionID    = interpretation.NewInterpretationRevisionID
	NewInterpretationContentDigest = interpretation.NewInterpretationContentDigest
	CanonicalInterpretationProject = interpretation.CanonicalInterpretationProject
	PrepareInterpretationRevision  = interpretation.PrepareInterpretationRevision
)
