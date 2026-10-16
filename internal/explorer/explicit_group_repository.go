package explorer

import (
	"context"
	"time"
)

type ExplicitGroupMembershipCursor struct {
	GroupID    ExplicitGroupID
	ResourceID string
}

// ExplicitGroupRepository keeps explicit group revisions separate from the
// general Explorer lifecycle store. Definition rows preserve empty groups;
// membership rows preserve overlaps between groups.
type ExplicitGroupRepository interface {
	BeginExplicitGroupRevision(context.Context, ExplicitGroupRevision, SelectionRevision, string) (*ExplicitGroupRevision, error)
	PutExplicitGroupDefinitions(context.Context, ExplicitGroupRevisionID, string, []ExplicitGroupDefinition) error
	AppendExplicitGroupMemberships(context.Context, ExplicitGroupRevisionID, string, []ExplicitGroupMembership) ([]ExplicitGroupMembership, error)
	DigestExplicitGroupRevision(context.Context, string, ExplicitGroupRevisionID) (string, string, int64, int64, error)
	CompleteExplicitGroupRevision(context.Context, ExplicitGroupRevisionID, string, string, string, int64, int64, time.Time) (*ExplicitGroupRevision, error)
	AbortExplicitGroupRevision(context.Context, ExplicitGroupRevisionID, string) error
	CleanupExplicitGroupStaging(context.Context, time.Time, int) error
	ListExplicitGroupRevisions(context.Context, string, string, string, string, int) ([]ExplicitGroupRevision, error)
	GetExplicitGroupRevision(context.Context, string, ExplicitGroupRevisionID) (*ExplicitGroupRevision, error)
	ListExplicitGroupDefinitions(context.Context, string, ExplicitGroupRevisionID) ([]ExplicitGroupDefinition, error)
	VisitExplicitGroupMemberships(context.Context, string, ExplicitGroupRevisionID, ExplicitGroupMembershipCursor, int, func(ExplicitGroupMembership) error) (ExplicitGroupMembershipCursor, error)
}
