package explorer

import (
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
)

type ExplicitGroupRevision = explorerselection.ExplicitGroupRevision
type ExplicitGroupRevisionID = explorerselection.ExplicitGroupRevisionID
type ExplicitGroupID = explorerselection.ExplicitGroupID
type ExplicitGroupRevisionState = explorerselection.ExplicitGroupRevisionState
type ExplicitGroupDefinition = explorerselection.ExplicitGroupDefinition
type ExplicitGroupMembership = explorerselection.ExplicitGroupMembership
type ExplicitGroupMembershipCursor = explorerselection.ExplicitGroupMembershipCursor
type ExplicitGroupRepository = explorerselection.ExplicitGroupRepository

const (
	ExplicitGroupRevisionStaging  = explorerselection.ExplicitGroupRevisionStaging
	ExplicitGroupRevisionComplete = explorerselection.ExplicitGroupRevisionComplete
)

var (
	ErrExplicitGroupRevisionNotFound     = explorerselection.ErrExplicitGroupRevisionNotFound
	ErrExplicitGroupRevisionConflict     = explorerselection.ErrExplicitGroupRevisionConflict
	ErrExplicitGroupRevisionIncomplete   = explorerselection.ErrExplicitGroupRevisionIncomplete
	ErrExplicitGroupRevisionStaleSource  = explorerselection.ErrExplicitGroupRevisionStaleSource
	ErrExplicitGroupMemberNotInSelection = explorerselection.ErrExplicitGroupMemberNotInSelection
	ErrCorruptExplicitGroupRevision      = explorerselection.ErrCorruptExplicitGroupRevision
)

var (
	ExplicitGroupRevisionIDFor         = explorerselection.ExplicitGroupRevisionIDFor
	CanonicalExplicitGroupDefinitions  = explorerselection.CanonicalExplicitGroupDefinitions
	ExplicitGroupDefinitionDigest      = explorerselection.ExplicitGroupDefinitionDigest
	CanonicalExplicitGroupMemberships  = explorerselection.CanonicalExplicitGroupMemberships
	ExplicitGroupMembershipDigest      = explorerselection.ExplicitGroupMembershipDigest
	StartExplicitGroupMembershipDigest = explorerselection.StartExplicitGroupMembershipDigest
	WriteExplicitGroupMembershipFrame  = explorerselection.WriteExplicitGroupMembershipFrame
)
