package selection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
)

type literalSelectionStore struct {
	header  SelectionRevision
	members map[string]SelectionMember
}

func (s *literalSelectionStore) BeginSelection(_ context.Context, value SelectionRevision, _ string) (*SelectionRevision, error) {
	if s.members == nil {
		s.members = map[string]SelectionMember{}
	}
	if s.header.ID != "" {
		if s.header.RuleDigest != value.RuleDigest || s.header.ScopeDigest != value.ScopeDigest {
			return nil, ErrSelectionConflict
		}
		return &s.header, nil
	}
	s.header = value
	return &s.header, nil
}

func (s *literalSelectionStore) AppendSelectionMembers(_ context.Context, _, _ string, values []SelectionMember) ([]SelectionMember, error) {
	inserted := make([]SelectionMember, 0, len(values))
	for _, value := range values {
		key := value.Ref.Canonical().ID
		if _, ok := s.members[key]; ok {
			continue
		}
		s.members[key] = value
		inserted = append(inserted, value)
	}
	return inserted, nil
}

func (s *literalSelectionStore) DigestSelectionMembers(context.Context, string, string) (string, int64, int64, error) {
	values := make([]SelectionMember, 0, len(s.members))
	for _, value := range s.members {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Ref.ID < values[j].Ref.ID })
	digest := MembershipDigest(values)
	return digest, int64(len(values)), int64(len(values)), nil
}

func (s *literalSelectionStore) CompleteSelection(_ context.Context, _ string, _ string, digest string, count, bytes int64, completedAt time.Time) (*SelectionRevision, error) {
	s.header.Complete, s.header.MembershipDigest, s.header.MemberCount, s.header.MemberBytes = true, digest, count, bytes
	s.header.CompletedAt = &completedAt
	return &s.header, nil
}

func (s *literalSelectionStore) AbortSelection(context.Context, string, string) error { return nil }

func (s *literalSelectionStore) GetSelection(context.Context, string, string) (*SelectionRevision, error) {
	return &s.header, nil
}

func (s *literalSelectionStore) VisitSelectionMembers(_ context.Context, _, _, after string, limit int, visit func(SelectionMember) error) (string, error) {
	values := make([]SelectionMember, 0, len(s.members))
	for _, value := range s.members {
		if value.Ref.ID > after {
			values = append(values, value)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Ref.ID < values[j].Ref.ID })
	if len(values) > limit {
		values = values[:limit]
	}
	for _, value := range values {
		if err := visit(value); err != nil {
			return after, err
		}
		after = value.Ref.ID
	}
	return after, nil
}

func TestCreateAndReadSelectionKeepsLiteralMembershipAndIdempotence(t *testing.T) {
	store := &literalSelectionStore{}
	service, err := NewService(Config{
		Store: store,
		Errors: ErrorFactory{
			Malformed: func(_, _ string, cause error) error { return cause },
			Unavailable: func(_, _, _ string, cause error) error {
				return cause
			},
			Conflict: func(_, _, _ string, _ map[string]any, cause error) error {
				return cause
			},
			Forbidden:     func(_, _ string, cause error) error { return cause },
			NotFound:      func(_, _, _ string, cause error) error { return cause },
			Unprocessable: func(_, _, _ string, cause error) error { return cause },
			PersistenceFailure: func(_, _ string, err error) error {
				return err
			},
		},
		IsNotFound: func(error) bool { return false },
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	scopeDigest := scopeDigestForTest(scope)
	request := selectionCreateRequest{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", IdempotencyKey: "retry-1", Scope: scope, ScopeDigest: scopeDigest, Rule: SelectionRule{Kind: SelectionRuleExplicit}, Source: SelectionSource{Kind: SelectionSourceExplicit}, Members: []ResourceRef{{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files001"}, {Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files002"}}}
	created, err := service.createSelectionResolved(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if created.Header.MemberCount != 2 || !created.Header.Complete {
		t.Fatalf("selection header = %#v", created.Header)
	}
	retried, err := service.createSelectionResolved(context.Background(), request)
	if err != nil || retried.Header.ID != created.Header.ID || retried.Header.MembershipDigest != created.Header.MembershipDigest {
		t.Fatalf("retry result = %#v, err=%v", retried.Header, err)
	}
	page, err := service.readSelectionResolved(context.Background(), selectionReadRequest{Project: "project", RevisionID: created.Header.ID, Scope: scope, ScopeDigest: scopeDigest, Limit: 10})
	if err != nil || len(page.Members) != 2 {
		t.Fatalf("read page = %#v, err=%v", page, err)
	}
}

func scopeDigestForTest(scope authscope.ReadScope) string {
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00"))
	return hex.EncodeToString(sum[:])
}
