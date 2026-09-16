package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
)

type selectionLifecycleStore struct {
	explorer.Store
	header   explorer.SelectionRevision
	members  map[string]explorer.SelectionMember
	revision *explorer.Revision
}

func (s *selectionLifecycleStore) BeginSelection(_ context.Context, value explorer.SelectionRevision, _ string) (*explorer.SelectionRevision, error) {
	if s.members == nil {
		s.members = map[string]explorer.SelectionMember{}
	}
	if s.header.ID != "" {
		if s.header.RuleDigest != value.RuleDigest || s.header.ScopeDigest != value.ScopeDigest {
			return nil, explorer.ErrSelectionConflict
		}
		return &s.header, nil
	}
	s.header = value
	return &s.header, nil
}

func TestCreateSelectionRejectsChangedExplicitRefsForSameIdempotencyKey(t *testing.T) {
	store := &selectionLifecycleStore{}
	persistence, _ := explorer.NewService(store)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	var resolvedToken string
	service, _ := New(persistence, Config{Capability: CapabilityResolver{ForCompilation: func(_ context.Context, project, token string) (AuthorizedCapability, error) {
		if project != "project" {
			t.Fatalf("execution project = %q", project)
		}
		resolvedToken = token
		return AuthorizedCapability{Snapshot: capabilitySnapshot("token", "generation-a", scopeDigest(scope)), Scope: scope}, nil
	}}, SelectionReferenceValidator: func(context.Context, string, string, authscope.ReadScope, []explorer.ResourceRef) error { return nil }})
	first := SelectionIntentCreateRequest{Project: "project", ExplorerID: "explorer", SnapshotToken: "token", IdempotencyKey: "same-key", Source: SelectionSourceIntent{Kind: SelectionSourceResources, Resources: []explorer.ResourceRef{{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files001"}}}}
	if _, err := service.CreateSelection(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if resolvedToken != "token" {
		t.Fatalf("execution resolver token = %q, want submitted snapshot token", resolvedToken)
	}
	first.Source.Resources[0].ID = "files002"
	_, err := service.CreateSelection(context.Background(), first)
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "SELECTION_IDEMPOTENCY_CONFLICT" {
		t.Fatalf("changed explicit refs error = %v", err)
	}
}

func TestCreateSelectionRejectsRetainedOldGenerationSnapshot(t *testing.T) {
	persistence, _ := explorer.NewService(&selectionLifecycleStore{})
	service, _ := New(persistence, Config{
		Capability: CapabilityResolver{ForCompilation: func(_ context.Context, project, token string) (AuthorizedCapability, error) {
			if project != "project" || token != "old-token" {
				t.Fatalf("compilation resolver args = %q/%q", project, token)
			}
			return AuthorizedCapability{}, capability.ErrStaleSnapshot
		}},
		SelectionReferenceValidator: func(context.Context, string, string, authscope.ReadScope, []explorer.ResourceRef) error { return nil },
	})
	_, err := service.CreateSelection(context.Background(), SelectionIntentCreateRequest{
		Project: "project", ExplorerID: "explorer", SnapshotToken: "old-token", IdempotencyKey: "old-generation",
		Source: SelectionSourceIntent{Kind: SelectionSourceResources, Resources: []explorer.ResourceRef{{Project: "project", Generation: "old-generation", ResourceType: "DocumentReference", ID: "files001"}}},
	})
	if !errors.Is(err, capability.ErrStaleSnapshot) {
		t.Fatalf("old-generation selection error = %v", err)
	}
}

func TestReadSelectionResolvesCurrentSnapshotThenExecutionToken(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	digest := scopeDigest(scope)
	completedAt := time.Now().UTC()
	store := &selectionLifecycleStore{header: explorer.SelectionRevision{
		ID: "selection-current-token", Project: "project", Generation: "generation-a", ResourceType: "DocumentReference",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: digest, RuleDigest: "rule", MembershipDigest: explorer.MembershipDigest(nil), Complete: true, CompletedAt: &completedAt,
	}}
	persistence, _ := explorer.NewService(store)
	var currentProject, currentExplorer, currentGeneration, executionProject, executionToken string
	service, err := New(persistence, Config{Capability: CapabilityResolver{
		Current: func(_ context.Context, project, explorerID, generation string) (capability.Snapshot, error) {
			currentProject, currentExplorer, currentGeneration = project, explorerID, generation
			return capabilitySnapshot("current-token", "generation-a", digest), nil
		},
		ForExecution: func(_ context.Context, project, token string) (AuthorizedCapability, error) {
			executionProject, executionToken = project, token
			return AuthorizedCapability{Snapshot: capabilitySnapshot(token, "generation-a", digest), Scope: scope}, nil
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReadSelection(context.Background(), SelectionReadIntentRequest{Project: "project", ExplorerID: "explorer", RevisionID: store.header.ID, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if currentProject != "project" || currentExplorer != "explorer" || currentGeneration != "" {
		t.Fatalf("current capability args = %q/%q/%q", currentProject, currentExplorer, currentGeneration)
	}
	if executionProject != "project" || executionToken != "current-token" {
		t.Fatalf("execution capability args = %q/%q", executionProject, executionToken)
	}
}

func (s *selectionLifecycleStore) AppendSelectionMembers(_ context.Context, _ string, _ string, values []explorer.SelectionMember) ([]explorer.SelectionMember, error) {
	inserted := make([]explorer.SelectionMember, 0, len(values))
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

func (s *selectionLifecycleStore) DigestSelectionMembers(_ context.Context, _, _ string) (string, int64, int64, error) {
	values := make([]explorer.SelectionMember, 0, len(s.members))
	for _, value := range s.members {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].Ref.ID < values[j].Ref.ID })
	digest := explorer.MembershipDigest(values)
	return digest, int64(len(values)), int64(len(values)), nil
}

func (s *selectionLifecycleStore) CompleteSelection(_ context.Context, _ string, _ string, digest string, count, bytes int64, completedAt time.Time) (*explorer.SelectionRevision, error) {
	s.header.Complete, s.header.MembershipDigest, s.header.MemberCount, s.header.MemberBytes = true, digest, count, bytes
	s.header.CompletedAt = &completedAt
	return &s.header, nil
}

func (s *selectionLifecycleStore) AbortSelection(context.Context, string, string) error { return nil }

func (s *selectionLifecycleStore) GetSelection(context.Context, string, string) (*explorer.SelectionRevision, error) {
	return &s.header, nil
}

func (s *selectionLifecycleStore) GetRevision(context.Context, string) (*explorer.Revision, error) {
	if s.revision == nil {
		return nil, explorer.ErrNotFound
	}
	return s.revision, nil
}

func (s *selectionLifecycleStore) VisitSelectionMembers(_ context.Context, _, _, after string, limit int, visit func(explorer.SelectionMember) error) (string, error) {
	values := make([]explorer.SelectionMember, 0, len(s.members))
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
	store := &selectionLifecycleStore{}
	persistence, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(persistence, Config{})
	if err != nil {
		t.Fatal(err)
	}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	scopeDigest := scopeDigest(scope)
	request := selectionCreateRequest{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", IdempotencyKey: "retry-1", Scope: scope, ScopeDigest: scopeDigest, Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit}, Members: []explorer.ResourceRef{{Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files001"}, {Project: "project", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files002"}}}
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

type selectionResolverFixture struct {
	materialization published.Materialization
	executionID     string
}

func (f *selectionResolverFixture) ResolveSelectionSource(_ context.Context, _, _, executionID, _ string) (published.Materialization, error) {
	f.executionID = executionID
	return f.materialization, nil
}

func (f *selectionResolverFixture) StreamSelectionSource(_ context.Context, _ published.Materialization, _ published.StreamRequest, _ func(map[string]any) error) error {
	return nil
}

func TestCreateSelectionRejectsForeignReferencesWithoutRewritingThem(t *testing.T) {
	store := &selectionLifecycleStore{}
	persistence, _ := explorer.NewService(store)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service, _ := New(persistence, Config{Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: capabilitySnapshot("token", "generation-a", scopeDigest(scope)), Scope: scope}, nil
	}}})
	_, err := service.CreateSelection(context.Background(), SelectionIntentCreateRequest{Project: "project", ExplorerID: "explorer", SnapshotToken: "token", IdempotencyKey: "foreign", Source: SelectionSourceIntent{Kind: SelectionSourceResources, Resources: []explorer.ResourceRef{{Project: "other", Generation: "generation-a", ResourceType: "DocumentReference", ID: "files001"}}}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "SELECTION_STALE_SCOPE" {
		t.Fatalf("error = %v, want stale-scope lifecycle error", err)
	}
}

func TestCreateSelectionRejectsBothSourceVariants(t *testing.T) {
	store := &selectionLifecycleStore{}
	persistence, _ := explorer.NewService(store)
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	service, _ := New(persistence, Config{Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: capabilitySnapshot("token", "generation-a", scopeDigest(scope)), Scope: scope}, nil
	}}})
	_, err := service.CreateSelection(context.Background(), SelectionIntentCreateRequest{Project: "project", ExplorerID: "explorer", SnapshotToken: "token", IdempotencyKey: "both", Source: SelectionSourceIntent{Kind: SelectionSourceResources, Resources: []explorer.ResourceRef{}, PublishedOutput: &PublishedOutputIntent{RevisionID: "revision", OutputID: "files"}}})
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "MALFORMED_REQUEST" {
		t.Fatalf("error = %v, want malformed lifecycle error", err)
	}
}

func TestCreateSelectionBindsRequestedRevisionSeparatelyFromExecution(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	store := &selectionLifecycleStore{revision: &explorer.Revision{Project: "project", ExplorerID: "explorer", CompilationReceiptID: "receipt", ResolvedSchemaDigest: "schema", Publication: explorer.PublicationMetadata{ExecutionID: "execution-a"}, Dataset: explorer.DatasetMetadata{Generation: "generation-a", Outputs: []explorer.DatasetOutput{{Name: "files"}}}}}
	persistence, _ := explorer.NewService(store)
	resolver := &selectionResolverFixture{materialization: published.Materialization{Revision: "execution-a", ReceiptID: "receipt", SchemaDigest: "final-physical-schema", Project: "project", DatasetGeneration: "generation-a", SourceRow: &publication.SourceRowMetadata{ResourceType: "DocumentReference", IDColumn: "id"}, Selector: published.DataframeSelector{Recipe: "recipe", Output: "files"}}}
	service, _ := New(persistence, Config{Capability: CapabilityResolver{ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: capabilitySnapshot("token", "generation-a", scopeDigest(scope)), Scope: scope}, nil
	}}, SelectionSourceResolver: resolver})
	created, err := service.CreateSelection(context.Background(), SelectionIntentCreateRequest{Project: "project", ExplorerID: "explorer", SnapshotToken: "token", IdempotencyKey: "revision-binding", Source: SelectionSourceIntent{Kind: SelectionSourcePublishedOutput, PublishedOutput: &PublishedOutputIntent{RevisionID: "revision-request", OutputID: "files"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.executionID != "execution-a" || created.Header.Source.RevisionID != "revision-request" || created.Header.Source.ExecutionID != "execution-a" {
		t.Fatalf("source binding = %#v, resolver execution=%q", created.Header.Source, resolver.executionID)
	}
	if created.Header.Source.SchemaDigest != "final-physical-schema" {
		t.Fatalf("source schema = %q, want final physical schema, not compilation schema", created.Header.Source.SchemaDigest)
	}
}

func capabilitySnapshot(token, generation, digest string) capability.Snapshot {
	return capability.Snapshot{Token: token, Identity: capability.SnapshotIdentity{Generation: generation, AuthorizationScopeDigest: digest}}
}

func scopeDigest(scope authscope.ReadScope) string {
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00"))
	return hex.EncodeToString(sum[:])
}
