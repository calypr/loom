package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
)

type explicitGroupSelectionFixture struct {
	explorer.Store
	header  explorer.SelectionRevision
	members []explorer.SelectionMember
}

func (s *explicitGroupSelectionFixture) GetSelection(_ context.Context, project, id string) (*explorer.SelectionRevision, error) {
	if id != s.header.ID || project != s.header.Project {
		return nil, explorer.ErrSelectionNotFound
	}
	value := s.header
	return &value, nil
}

func (s *explicitGroupSelectionFixture) DigestSelectionMembers(_ context.Context, project, id string) (string, int64, int64, error) {
	if id != s.header.ID || project != s.header.Project {
		return "", 0, 0, explorer.ErrSelectionNotFound
	}
	return explorer.MembershipDigest(s.members), int64(len(s.members)), int64(len(s.members)), nil
}

func (s *explicitGroupSelectionFixture) VisitSelectionMembers(_ context.Context, project, id, after string, limit int, visit func(explorer.SelectionMember) error) (string, error) {
	if id != s.header.ID || project != s.header.Project {
		return "", explorer.ErrSelectionNotFound
	}
	values := append([]explorer.SelectionMember(nil), s.members...)
	sortSelectionMembers(values)
	if limit <= 0 {
		limit = len(values)
	}
	count := 0
	last := after
	for _, member := range values {
		if member.MemberKey <= after {
			continue
		}
		if count >= limit {
			break
		}
		if err := visit(member); err != nil {
			return last, err
		}
		last = member.MemberKey
		count++
	}
	return last, nil
}

func sortSelectionMembers(members []explorer.SelectionMember) {
	for i := 1; i < len(members); i++ {
		for j := i; j > 0 && members[j].MemberKey < members[j-1].MemberKey; j-- {
			members[j], members[j-1] = members[j-1], members[j]
		}
	}
}

type explicitGroupRepositoryFixture struct {
	explorer.ExplicitGroupRepository
	header        *explorer.ExplicitGroupRevision
	writer        string
	definitions   []explorer.ExplicitGroupDefinition
	memberships   []explorer.ExplicitGroupMembership
	appendErr     error
	beginCalls    int
	abortCalls    int
	completeCalls int
}

func (r *explicitGroupRepositoryFixture) BeginExplicitGroupRevision(_ context.Context, header explorer.ExplicitGroupRevision, source explorer.SelectionRevision, writer string) (*explorer.ExplicitGroupRevision, error) {
	r.beginCalls++
	if err := header.ValidateSourceSelection(source); err != nil {
		return nil, err
	}
	if r.header != nil {
		if r.header.ID != header.ID || r.header.SourceSelectionRevisionID != header.SourceSelectionRevisionID {
			return nil, explorer.ErrExplicitGroupRevisionConflict
		}
		value := *r.header
		return &value, nil
	}
	value := header
	r.header, r.writer = &value, writer
	return &value, nil
}

func (r *explicitGroupRepositoryFixture) PutExplicitGroupDefinitions(_ context.Context, id explorer.ExplicitGroupRevisionID, writer string, groups []explorer.ExplicitGroupDefinition) error {
	if r.header == nil || r.header.ID != id || r.writer != writer || r.header.State != explorer.ExplicitGroupRevisionStaging {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	canonical, err := explorer.CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return err
	}
	r.definitions = canonical
	return nil
}

func (r *explicitGroupRepositoryFixture) AppendExplicitGroupMemberships(_ context.Context, id explorer.ExplicitGroupRevisionID, writer string, memberships []explorer.ExplicitGroupMembership) ([]explorer.ExplicitGroupMembership, error) {
	if r.appendErr != nil {
		return nil, r.appendErr
	}
	if r.header == nil || r.header.ID != id || r.writer != writer || r.header.State != explorer.ExplicitGroupRevisionStaging {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	canonical, err := explorer.CanonicalExplicitGroupMemberships(*r.header, r.definitions, memberships)
	if err != nil {
		return nil, err
	}
	r.memberships = append(r.memberships, canonical...)
	return canonical, nil
}

func (r *explicitGroupRepositoryFixture) DigestExplicitGroupRevision(_ context.Context, _ string, id explorer.ExplicitGroupRevisionID) (string, string, int64, int64, error) {
	if r.header == nil || r.header.ID != id {
		return "", "", 0, 0, explorer.ErrExplicitGroupRevisionNotFound
	}
	definitionDigest, err := explorer.ExplicitGroupDefinitionDigest(r.definitions)
	if err != nil {
		return "", "", 0, 0, err
	}
	membershipDigest, err := explorer.ExplicitGroupMembershipDigest(*r.header, r.definitions, r.memberships)
	if err != nil {
		return "", "", 0, 0, err
	}
	canonical, err := explorer.CanonicalExplicitGroupMemberships(*r.header, r.definitions, r.memberships)
	return definitionDigest, membershipDigest, int64(len(r.definitions)), int64(len(canonical)), err
}

func (r *explicitGroupRepositoryFixture) CompleteExplicitGroupRevision(_ context.Context, id explorer.ExplicitGroupRevisionID, writer, definitionDigest, membershipDigest string, groupCount, memberCount int64, at time.Time) (*explorer.ExplicitGroupRevision, error) {
	if r.header == nil || r.header.ID != id || r.writer != writer {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	actualDefinition, actualMembership, actualGroups, actualMembers, err := r.DigestExplicitGroupRevision(context.Background(), r.header.Project, id)
	if err != nil {
		return nil, err
	}
	if actualDefinition != definitionDigest || actualMembership != membershipDigest || actualGroups != groupCount || actualMembers != memberCount {
		return nil, explorer.ErrExplicitGroupRevisionConflict
	}
	r.completeCalls++
	r.header.DefinitionDigest, r.header.MembershipDigest = definitionDigest, membershipDigest
	r.header.GroupCount, r.header.MemberCount = groupCount, memberCount
	r.header.State = explorer.ExplicitGroupRevisionComplete
	r.header.CompletedAt = &at
	return r.header, nil
}

func (r *explicitGroupRepositoryFixture) AbortExplicitGroupRevision(_ context.Context, id explorer.ExplicitGroupRevisionID, writer string) error {
	r.abortCalls++
	if r.header == nil || r.header.ID != id || r.writer != writer {
		return explorer.ErrExplicitGroupRevisionConflict
	}
	r.header, r.definitions, r.memberships = nil, nil, nil
	return nil
}

func TestCreateExplicitGroupRevisionPreservesEmptyAndOverlappingMemberships(t *testing.T) {
	service, selectionStore, repository := explicitGroupServiceFixture(t)
	request := explicitGroupCreateFixtureRequest(selectionStore.header.ID)
	result, err := service.CreateExplicitGroupRevision(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.SourceSelectionRevisionID != selectionStore.header.ID || result.GroupCount != 3 || result.MemberCount != 4 {
		t.Fatalf("created summary = %#v", result)
	}
	if len(repository.definitions) != 3 || repository.definitions[2].ID != "group-empty" {
		t.Fatalf("empty group definition was lost: %#v", repository.definitions)
	}
	want := []explorer.ExplicitGroupMembership{
		{GroupID: "group-a", Ref: selectionStore.members[0].Ref},
		{GroupID: "group-a", Ref: selectionStore.members[1].Ref},
		{GroupID: "group-b", Ref: selectionStore.members[1].Ref},
		{GroupID: "group-b", Ref: selectionStore.members[2].Ref},
	}
	got, err := explorer.CanonicalExplicitGroupMemberships(*repository.header, repository.definitions, repository.memberships)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("stored memberships = %#v err=%v, want %#v", got, err, want)
	}
	retry, err := service.CreateExplicitGroupRevision(context.Background(), request)
	if err != nil || retry.RevisionID != result.RevisionID || repository.completeCalls != 1 {
		t.Fatalf("idempotent retry = %#v err=%v completeCalls=%d", retry, err, repository.completeCalls)
	}
}

func TestCreateExplicitGroupRevisionRejectsForeignMembersBeforeStaging(t *testing.T) {
	service, selectionStore, repository := explicitGroupServiceFixture(t)
	request := explicitGroupCreateFixtureRequest(selectionStore.header.ID)
	request.Groups[0].MemberIDs = []string{"member-a", "not-a-source-member"}
	_, err := service.CreateExplicitGroupRevision(context.Background(), request)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "EXPLICIT_GROUP_FOREIGN_MEMBER" {
		t.Fatalf("foreign member error = %v", err)
	}
	if repository.beginCalls != 0 || len(repository.definitions) != 0 || len(repository.memberships) != 0 {
		t.Fatalf("foreign member reached staging: begin=%d definitions=%#v memberships=%#v", repository.beginCalls, repository.definitions, repository.memberships)
	}
}

func TestCreateExplicitGroupRevisionAbortsFailedStagingWithoutCompletion(t *testing.T) {
	service, selectionStore, repository := explicitGroupServiceFixture(t)
	repository.appendErr = errors.New("injected membership write error")
	_, err := service.CreateExplicitGroupRevision(context.Background(), explicitGroupCreateFixtureRequest(selectionStore.header.ID))
	if err == nil || repository.abortCalls != 1 || repository.completeCalls != 0 || repository.header != nil {
		t.Fatalf("failed write left partial revision: err=%v abort=%d complete=%d header=%#v", err, repository.abortCalls, repository.completeCalls, repository.header)
	}
}

func explicitGroupServiceFixture(t *testing.T) (*Service, *explicitGroupSelectionFixture, *explicitGroupRepositoryFixture) {
	t.Helper()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	digest := scopeDigest(scope)
	completedAt := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	selection := explorer.SelectionRevision{
		ID: "selection-source", Project: "project", Generation: "generation-a", ResourceType: "OpaqueResource",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: digest, RuleDigest: "selection-rule", MembershipDigest: "", MemberCount: 3, MemberBytes: 3,
		Complete: true, CreatedAt: completedAt, CompletedAt: &completedAt,
	}
	members := []explorer.SelectionMember{
		{Ref: explorer.ResourceRef{Project: "project", Generation: "generation-a", ResourceType: "OpaqueResource", ID: "record-a"}, MemberKey: "member-a"},
		{Ref: explorer.ResourceRef{Project: "project", Generation: "generation-a", ResourceType: "OpaqueResource", ID: "record-b"}, MemberKey: "member-b"},
		{Ref: explorer.ResourceRef{Project: "project", Generation: "generation-a", ResourceType: "OpaqueResource", ID: "record-c"}, MemberKey: "member-c"},
	}
	selection.MembershipDigest = explorer.MembershipDigest(members)
	selectionStore := &explicitGroupSelectionFixture{header: selection, members: members}
	persistence, err := explorer.NewService(selectionStore)
	if err != nil {
		t.Fatal(err)
	}
	repository := &explicitGroupRepositoryFixture{}
	service, err := New(persistence, Config{
		Capability: CapabilityResolver{ForCompilation: func(_ context.Context, project, token string) (AuthorizedCapability, error) {
			if project != "project" || token != "snapshot-token" {
				return AuthorizedCapability{}, capability.ErrStaleSnapshot
			}
			return AuthorizedCapability{Snapshot: capabilitySnapshot(token, "generation-a", digest), Scope: scope}, nil
		}},
		ExplicitGroupRepository: repository,
		Now:                     func() time.Time { return completedAt.Add(time.Minute) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, selectionStore, repository
}

func explicitGroupCreateFixtureRequest(selectionID string) ExplicitGroupCreateRequest {
	return ExplicitGroupCreateRequest{
		Project: "project", ExplorerID: "explorer", SnapshotToken: "snapshot-token", SelectionID: selectionID,
		IdempotencyKey: "group-shape-1",
		Groups: []ExplicitGroupInput{
			{ID: "group-a", Label: "Alpha", Ordinal: 0, MemberIDs: []string{"member-a", "member-b"}},
			{ID: "group-b", Label: "Beta", Ordinal: 1, MemberIDs: []string{"member-b", "member-c"}},
			{ID: "group-empty", Label: "Empty", Ordinal: 2, MemberIDs: []string{}},
		},
	}
}
