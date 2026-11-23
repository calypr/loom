package explicitgroup

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
	"github.com/google/uuid"
)

type Store interface {
	GetSelection(context.Context, string, string) (*selection.SelectionRevision, error)
	DigestSelectionMembers(context.Context, string, string) (string, int64, int64, error)
	VisitSelectionMembers(context.Context, string, string, string, int, func(selection.SelectionMember) error) (string, error)
}

type Config struct {
	Store      Store
	Repository explorer.ExplicitGroupRepository
	Capability ports.CapabilityResolver
	Now        func() time.Time
}

type Service struct {
	config Config
}

func NewService(config Config) (*Service, error) {
	if config.Store == nil {
		return nil, errors.New("explicit group store is required")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{config: config}, nil
}

func (s *Service) now() time.Time {
	return s.config.Now().UTC()
}

func (s *Service) capabilityResolutionFailure(stage, message string, err error) error {
	switch {
	case errors.Is(err, capability.ErrStaleSnapshot), errors.Is(err, capability.ErrSnapshotUnavailable), errors.Is(err, capability.ErrNotFound):
		return support.Conflict(stage, "STALE_CATALOG_SNAPSHOT", message, nil, err)
	case errors.Is(err, authscope.ErrUnauthenticated), errors.Is(err, authscope.ErrForbidden):
		return support.Forbidden(stage, message, err)
	default:
		return support.Unavailable(stage, "CAPABILITY_UNAVAILABLE", message, err)
	}
}

const (
	explicitGroupCreateBatchSize = 256
	explicitGroupMaxCount        = 1000
	explicitGroupMaxRelations    = 100_000
)

type ExplicitGroupInput struct {
	ID        string
	Label     string
	Ordinal   int64
	MemberIDs []string
}

type ExplicitGroupCreateRequest struct {
	Project        string
	ExplorerID     string
	SnapshotToken  string
	SelectionID    string
	IdempotencyKey string
	Groups         []ExplicitGroupInput
}

type ExplicitGroupSummary struct {
	ID          string
	Label       string
	Ordinal     int64
	MemberCount int64
}

type ExplicitGroupCreateResult struct {
	RevisionID                string
	SourceSelectionRevisionID string
	GroupCount                int64
	MemberCount               int64
	CreatedAt                 time.Time
	Groups                    []ExplicitGroupSummary
}

func (s *Service) CreateExplicitGroupRevision(ctx context.Context, request ExplicitGroupCreateRequest) (result ExplicitGroupCreateResult, err error) {
	if s == nil || s.config.Repository == nil || s.config.Capability.ForCompilation == nil {
		return result, support.Unavailable("explicit-groups", "AUTHORING_UNAVAILABLE", "explicit group authoring is not configured", nil)
	}
	project := projectid.Canonical(request.Project)
	selectionID := strings.TrimSpace(request.SelectionID)
	snapshotToken := strings.TrimSpace(request.SnapshotToken)
	idempotencyKey := strings.TrimSpace(request.IdempotencyKey)
	if project == "" || strings.TrimSpace(request.ExplorerID) == "" || selectionID == "" || snapshotToken == "" || idempotencyKey == "" || len(idempotencyKey) > 256 || len(request.Groups) == 0 || len(request.Groups) > explicitGroupMaxCount {
		return result, support.Malformed("explicit-groups", "a source selection, idempotency key, and one or more groups are required", nil)
	}
	definitions := make([]explorer.ExplicitGroupDefinition, len(request.Groups))
	requestedByKey := make(map[string][]explorer.ExplicitGroupID)
	relationCount := 0
	for i, group := range request.Groups {
		if len(strings.TrimSpace(group.ID)) > 256 || len(strings.TrimSpace(group.Label)) > 256 {
			return result, support.Malformed("explicit-groups", "group identities and names must be at most 256 characters", nil)
		}
		definitions[i] = explorer.ExplicitGroupDefinition{ID: explorer.ExplicitGroupID(group.ID), Label: group.Label, Ordinal: group.Ordinal}
		if group.MemberIDs == nil {
			return result, support.Malformed("explicit-groups", "each group must include a memberIds array (which may be empty)", nil)
		}
		seen := make(map[string]struct{}, len(group.MemberIDs))
		for _, raw := range group.MemberIDs {
			memberID := strings.TrimSpace(raw)
			if memberID == "" || len(memberID) > 512 {
				return result, support.Malformed("explicit-groups", "member identities must be non-empty opaque selection keys", nil)
			}
			if _, duplicate := seen[memberID]; duplicate {
				return result, support.Malformed("explicit-groups", "a group cannot contain the same member more than once", nil)
			}
			seen[memberID] = struct{}{}
			relationCount++
			if relationCount > explicitGroupMaxRelations {
				return result, support.Unprocessable("explicit-groups", "EXPLICIT_GROUP_LIMIT_EXCEEDED", "explicit group membership exceeds the relation limit", nil)
			}
			requestedByKey[memberID] = append(requestedByKey[memberID], explorer.ExplicitGroupID(group.ID))
		}
	}
	canonicalDefinitions, err := explorer.CanonicalExplicitGroupDefinitions(definitions)
	if err != nil {
		return result, support.Malformed("explicit-groups", "group definitions are invalid", err)
	}

	authorized, err := s.config.Capability.ForCompilation(ctx, project, snapshotToken)
	if err != nil {
		return result, s.capabilityResolutionFailure("explicit-groups", "the explicit group capability snapshot is unavailable", err)
	}
	if strings.TrimSpace(authorized.Snapshot.Token) != snapshotToken {
		return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_STALE_SNAPSHOT", "the authorized dataset snapshot changed", nil, nil)
	}
	generation := strings.TrimSpace(authorized.Snapshot.Identity.Generation)
	scopeDigest := strings.TrimSpace(authorized.Snapshot.Identity.AuthorizationScopeDigest)
	if generation == "" || scopeDigest == "" {
		return result, support.Unavailable("explicit-groups", "AUTHORING_UNAVAILABLE", "the authorized snapshot is incomplete", nil)
	}
	source, err := s.config.Store.GetSelection(ctx, project, selectionID)
	if err != nil {
		return result, normalizeExplicitGroupError(err)
	}
	if source == nil || !source.Complete {
		return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_SOURCE_INCOMPLETE", "the source selection is not complete", nil, selection.ErrSelectionIncomplete)
	}
	if err := source.Validate(); err != nil {
		return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_SOURCE_CORRUPT", "the source selection is incomplete or corrupt", nil, err)
	}
	if source.Project != project || source.Generation != generation || source.ScopeDigest != scopeDigest {
		return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_STALE_SOURCE", "the source selection is outside the active project, generation, or authorization scope", nil, explorer.ErrExplicitGroupRevisionStaleSource)
	}
	if digest, count, _, digestErr := s.config.Store.DigestSelectionMembers(ctx, project, selectionID); digestErr != nil || digest != source.MembershipDigest || count != source.MemberCount {
		if digestErr != nil {
			return result, support.PersistenceFailure("explicit-groups", "the source selection membership could not be read", digestErr)
		}
		return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_SOURCE_CORRUPT", "the source selection membership no longer matches its immutable header", nil, explorer.ErrExplicitGroupRevisionStaleSource)
	}

	refsByKey := make(map[string]selection.ResourceRef, len(requestedByKey))
	after := ""
	var visited int64
	for {
		pageCount := 0
		next, visitErr := s.config.Store.VisitSelectionMembers(ctx, project, selectionID, after, 1000, func(member selection.SelectionMember) error {
			pageCount++
			visited++
			member = member.Canonical()
			if err := member.Validate(project, generation, source.ResourceType); err != nil {
				return err
			}
			if member.MemberKey == "" {
				return explorer.ErrCorruptExplicitGroupRevision
			}
			if _, wanted := requestedByKey[member.MemberKey]; wanted {
				if _, duplicate := refsByKey[member.MemberKey]; duplicate {
					return explorer.ErrCorruptExplicitGroupRevision
				}
				refsByKey[member.MemberKey] = member.Ref
			}
			return nil
		})
		if visitErr != nil {
			return result, support.PersistenceFailure("explicit-groups", "the source selection membership could not be verified", visitErr)
		}
		if pageCount == 0 || pageCount < 1000 || next == after {
			break
		}
		after = next
	}
	if visited != source.MemberCount || len(refsByKey) != len(requestedByKey) {
		return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_FOREIGN_MEMBER", "every group member must belong to the exact source selection", nil, nil)
	}

	memberships := make([]explorer.ExplicitGroupMembership, 0, relationCount)
	memberCounts := make(map[explorer.ExplicitGroupID]int64, len(canonicalDefinitions))
	for _, group := range request.Groups {
		for _, memberID := range group.MemberIDs {
			key := strings.TrimSpace(memberID)
			ref, ok := refsByKey[key]
			if !ok {
				return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_FOREIGN_MEMBER", "every group member must belong to the exact source selection", nil, nil)
			}
			groupID := explorer.ExplicitGroupID(strings.TrimSpace(group.ID))
			memberships = append(memberships, explorer.ExplicitGroupMembership{GroupID: groupID, Ref: ref})
			memberCounts[groupID]++
		}
	}

	header := explorer.ExplicitGroupRevision{
		ID: explorer.ExplicitGroupRevisionIDFor(project, idempotencyKey), Project: project,
		Generation: generation, ScopeDigest: scopeDigest, ResourceType: source.ResourceType,
		SourceSelectionRevisionID: source.ID, SourceMembershipDigest: source.MembershipDigest,
		State: explorer.ExplicitGroupRevisionStaging, IdempotencyKey: idempotencyKey, CreatedAt: s.now().UTC(),
	}
	if err := header.Validate(); err != nil {
		return result, support.Malformed("explicit-groups", "explicit group revision identity is invalid", err)
	}
	definitionDigest, err := explorer.ExplicitGroupDefinitionDigest(canonicalDefinitions)
	if err != nil {
		return result, support.Malformed("explicit-groups", "group definitions are invalid", err)
	}
	canonicalMemberships, err := explorer.CanonicalExplicitGroupMemberships(header, canonicalDefinitions, memberships)
	if err != nil {
		return result, support.Malformed("explicit-groups", "group memberships are invalid", err)
	}
	membershipDigest, err := explorer.ExplicitGroupMembershipDigest(header, canonicalDefinitions, canonicalMemberships)
	if err != nil {
		return result, support.Malformed("explicit-groups", "group memberships are invalid", err)
	}
	writerToken := uuid.NewString()
	started, err := s.config.Repository.BeginExplicitGroupRevision(ctx, header, *source, writerToken)
	if err != nil {
		return result, normalizeExplicitGroupError(err)
	}
	if started.State == explorer.ExplicitGroupRevisionComplete {
		if started.DefinitionDigest != definitionDigest || started.MembershipDigest != membershipDigest || started.GroupCount != int64(len(canonicalDefinitions)) || started.MemberCount != int64(len(canonicalMemberships)) {
			return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_IDEMPOTENCY_CONFLICT", "the idempotency key is already bound to different group intent", nil, explorer.ErrExplicitGroupRevisionConflict)
		}
		return explicitGroupCreateResult(*started, canonicalDefinitions, memberCounts), nil
	}
	if started.State != explorer.ExplicitGroupRevisionStaging {
		return result, support.Conflict("explicit-groups", "EXPLICIT_GROUP_CONFLICT", "the explicit group revision is not writable", nil, explorer.ErrExplicitGroupRevisionConflict)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	abort := func(cause error) (ExplicitGroupCreateResult, error) {
		abortCtx, abortCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer abortCancel()
		return result, errors.Join(cause, s.config.Repository.AbortExplicitGroupRevision(abortCtx, header.ID, writerToken))
	}
	if err := s.config.Repository.PutExplicitGroupDefinitions(writeCtx, header.ID, writerToken, canonicalDefinitions); err != nil {
		return abort(normalizeExplicitGroupError(err))
	}
	for start := 0; start < len(canonicalMemberships); start += explicitGroupCreateBatchSize {
		end := start + explicitGroupCreateBatchSize
		if end > len(canonicalMemberships) {
			end = len(canonicalMemberships)
		}
		if _, err := s.config.Repository.AppendExplicitGroupMemberships(writeCtx, header.ID, writerToken, canonicalMemberships[start:end]); err != nil {
			return abort(normalizeExplicitGroupError(err))
		}
	}
	actualDefinition, actualMembership, groupCount, memberCount, err := s.config.Repository.DigestExplicitGroupRevision(writeCtx, project, header.ID)
	if err != nil {
		return abort(normalizeExplicitGroupError(err))
	}
	if actualDefinition != definitionDigest || actualMembership != membershipDigest || groupCount != int64(len(canonicalDefinitions)) || memberCount != int64(len(canonicalMemberships)) {
		return abort(support.Conflict("explicit-groups", "EXPLICIT_GROUP_STAGING_INCOMPLETE", "the staged group set did not match the requested complete membership", nil, explorer.ErrExplicitGroupRevisionIncomplete))
	}
	completed, err := s.config.Repository.CompleteExplicitGroupRevision(writeCtx, header.ID, writerToken, definitionDigest, membershipDigest, groupCount, memberCount, s.now().UTC())
	if err != nil {
		return abort(normalizeExplicitGroupError(err))
	}
	return explicitGroupCreateResult(*completed, canonicalDefinitions, memberCounts), nil
}

func explicitGroupCreateResult(revision explorer.ExplicitGroupRevision, groups []explorer.ExplicitGroupDefinition, memberCounts map[explorer.ExplicitGroupID]int64) ExplicitGroupCreateResult {
	result := ExplicitGroupCreateResult{
		RevisionID: string(revision.ID), SourceSelectionRevisionID: revision.SourceSelectionRevisionID,
		GroupCount: revision.GroupCount, MemberCount: revision.MemberCount, CreatedAt: revision.CreatedAt,
		Groups: make([]ExplicitGroupSummary, 0, len(groups)),
	}
	for _, group := range groups {
		result.Groups = append(result.Groups, ExplicitGroupSummary{ID: string(group.ID), Label: group.Label, Ordinal: group.Ordinal, MemberCount: memberCounts[group.ID]})
	}
	return result
}

func normalizeExplicitGroupError(err error) error {
	if err == nil {
		return nil
	}
	var typed *ports.Error
	if errors.As(err, &typed) {
		return err
	}
	switch {
	case errors.Is(err, explorer.ErrExplicitGroupRevisionConflict):
		return support.Conflict("explicit-groups", "EXPLICIT_GROUP_CONFLICT", "explicit group revision conflicts with existing immutable intent", nil, err)
	case errors.Is(err, explorer.ErrExplicitGroupRevisionIncomplete), errors.Is(err, selection.ErrSelectionIncomplete):
		return support.Conflict("explicit-groups", "EXPLICIT_GROUP_INCOMPLETE", "explicit group or source selection is incomplete", nil, err)
	case errors.Is(err, explorer.ErrExplicitGroupRevisionStaleSource), errors.Is(err, selection.ErrSelectionStaleScope), errors.Is(err, selection.ErrResourceRefScopeMismatch):
		return support.Conflict("explicit-groups", "EXPLICIT_GROUP_STALE_SOURCE", "explicit group source selection is stale or mismatched", nil, err)
	case errors.Is(err, explorer.ErrExplicitGroupMemberNotInSelection):
		return support.Conflict("explicit-groups", "EXPLICIT_GROUP_FOREIGN_MEMBER", "every group member must belong to the exact source selection", nil, err)
	case errors.Is(err, selection.ErrSelectionNotFound):
		return support.NotFound("explicit-groups", "EXPLICIT_GROUP_SOURCE_NOT_FOUND", "source selection was not found", err)
	case errors.Is(err, explorer.ErrExplicitGroupRevisionNotFound):
		return support.NotFound("explicit-groups", "EXPLICIT_GROUP_NOT_FOUND", "explicit group revision was not found", err)
	default:
		return support.PersistenceFailure("explicit-groups", "explicit group authoring failed", err)
	}
}
