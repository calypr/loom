package explicitgroup

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

type ExplicitGroupRevisionResolveRequest struct {
	Project          string
	ExplorerID       string
	OutputID         string
	Snapshot         capability.Snapshot
	RevisionID       string
	RootResourceType string
}

type ExplicitGroupRevisionListRequest struct {
	Project          string
	Snapshot         capability.Snapshot
	RootResourceType string
	PinnedRevisionID string
}

type ExplicitGroupRevisionChoice struct {
	RevisionID                string                               `json:"revisionId"`
	SourceSelectionRevisionID string                               `json:"sourceSelectionRevisionId,omitempty"`
	GroupCount                int64                                `json:"groupCount"`
	MemberCount               int64                                `json:"memberCount"`
	CreatedAt                 time.Time                            `json:"createdAt"`
	UnassignedMemberPolicies  []authoringv2.UnassignedMemberPolicy `json:"unassignedMemberPolicies"`
}

type ExplicitGroupRevisionProof struct {
	RevisionID                string
	Project                   string
	SourceGeneration          string
	AuthorizationScopeDigest  string
	RootResourceType          string
	SelectionRevisionID       string
	SelectionMembershipDigest string
	DefinitionDigest          string
	MembershipDigest          string
	Complete                  bool
}

type ExplicitGroupRevisionResolver interface {
	ListExplicitGroupRevisions(context.Context, ExplicitGroupRevisionListRequest) ([]ExplicitGroupRevisionChoice, error)
	ResolveExplicitGroupRevision(context.Context, ExplicitGroupRevisionResolveRequest) (ExplicitGroupRevisionProof, error)
	ValidateCompilationReceipt(context.Context, ExplicitGroupRevisionProof, *explorer.CompilationReceipt) error
}
