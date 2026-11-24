package arango

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer/arango/db"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/projectid"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestExplicitGroupStorageAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	persistence, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	project := projectid.Canonical("loom_explicit_group_test_" + uuid.NewString())
	source, sourceWriter, err := persistExplicitGroupTestSelection(ctx, t, persistence, project)
	if err != nil {
		t.Fatal(err)
	}
	if source.MemberCount != 3 {
		t.Fatalf("source selection has %d members, want 3", source.MemberCount)
	}
	defer func() { _ = persistence.AbortSelection(context.Background(), source.ID, sourceWriter) }()
	header := explicitGroupTestRevision(source, "primary")
	writer := uuid.NewString()
	defer func() { _ = persistence.AbortExplicitGroupRevision(context.Background(), header.ID, writer) }()

	started, err := persistence.BeginExplicitGroupRevision(ctx, header, source, writer)
	if err != nil || started.ID != header.ID || started.State != explorerselection.ExplicitGroupRevisionStaging {
		t.Fatalf("begin = %#v err=%v", started, err)
	}
	stagingChoices, err := persistence.ListExplicitGroupRevisions(ctx, project, source.Generation, source.ScopeDigest, source.ResourceType, 10)
	if err != nil || len(stagingChoices) != 0 {
		t.Fatalf("staging row-root choices = %#v err=%v, want no choices", stagingChoices, err)
	}
	if _, err := persistence.BeginExplicitGroupRevision(ctx, header, source, writer); err != nil {
		t.Fatalf("same-owner begin retry = %v", err)
	}
	if _, err := persistence.BeginExplicitGroupRevision(ctx, header, source, uuid.NewString()); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("foreign-owner begin = %v", err)
	}
	wrongSource := source
	wrongSource.ScopeDigest = "another-scope"
	if _, err := persistence.BeginExplicitGroupRevision(ctx, header, wrongSource, writer); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionStaleSource) {
		t.Fatalf("mismatched source begin = %v", err)
	}
	missingSource := source
	missingSource.ID = "selection-" + uuid.NewString()
	missingHeader := explicitGroupTestRevision(missingSource, "missing-source")
	if _, err := persistence.BeginExplicitGroupRevision(ctx, missingHeader, missingSource, uuid.NewString()); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionStaleSource) {
		t.Fatalf("unavailable source begin = %v", err)
	}

	groups := []explorerselection.ExplicitGroupDefinition{
		{ID: "group-a", Label: "Alpha", Ordinal: 0},
		{ID: "group-b", Label: "Beta", Ordinal: 1},
		{ID: "group-empty", Label: "No members", Ordinal: 2},
	}
	if err := persistence.PutExplicitGroupDefinitions(ctx, header.ID, writer, groups); err != nil {
		t.Fatalf("put definitions = %v", err)
	}
	if err := persistence.PutExplicitGroupDefinitions(ctx, header.ID, writer, groups); err != nil {
		t.Fatalf("definition retry = %v", err)
	}
	changedDefinition := append([]explorerselection.ExplicitGroupDefinition(nil), groups...)
	changedDefinition[0].Label = "Changed intent"
	if err := persistence.PutExplicitGroupDefinitions(ctx, header.ID, writer, changedDefinition); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("changed definition retry = %v", err)
	}

	sharedRef := explorerselection.ResourceRef{Project: project, Generation: source.Generation, ResourceType: source.ResourceType, ID: "patient-1"}
	onlyARef := explorerselection.ResourceRef{Project: project, Generation: source.Generation, ResourceType: source.ResourceType, ID: "patient-2"}
	memberships := []explorerselection.ExplicitGroupMembership{
		{GroupID: "group-a", Ref: sharedRef},
		{GroupID: "group-a", Ref: sharedRef},
		{GroupID: "group-b", Ref: sharedRef},
		{GroupID: "group-a", Ref: onlyARef},
	}
	wrongScopeBatch := append([]explorerselection.ExplicitGroupMembership(nil), memberships[:1]...)
	wrongScopeBatch = append(wrongScopeBatch, explorerselection.ExplicitGroupMembership{GroupID: "group-b", Ref: explorerselection.ResourceRef{Project: "another-project", Generation: source.Generation, ResourceType: source.ResourceType, ID: "patient-3"}})
	if _, err := persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, wrongScopeBatch); !errors.Is(err, explorerselection.ErrResourceRefScopeMismatch) {
		t.Fatalf("cross-project append = %v", err)
	}
	foreignBatch := []explorerselection.ExplicitGroupMembership{
		{GroupID: "group-a", Ref: sharedRef},
		{GroupID: "group-b", Ref: explorerselection.ResourceRef{Project: project, Generation: source.Generation, ResourceType: source.ResourceType, ID: "not-in-source-selection"}},
	}
	if _, err := persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, foreignBatch); !errors.Is(err, explorerselection.ErrExplicitGroupMemberNotInSelection) {
		t.Fatalf("foreign source member append = %v", err)
	}
	_, _, _, partialCount, err := persistence.DigestExplicitGroupRevision(ctx, project, header.ID)
	if err != nil || partialCount != 0 {
		t.Fatalf("foreign batch wrote a partial membership set: count=%d err=%v", partialCount, err)
	}
	inserted, err := persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, memberships)
	if err != nil || !reflect.DeepEqual(inserted, []explorerselection.ExplicitGroupMembership{{GroupID: "group-a", Ref: sharedRef.Canonical()}, {GroupID: "group-a", Ref: onlyARef.Canonical()}, {GroupID: "group-b", Ref: sharedRef.Canonical()}}) {
		t.Fatalf("first append = %#v err=%v", inserted, err)
	}
	inserted, err = persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, memberships)
	if err != nil || len(inserted) != 0 {
		t.Fatalf("membership retry = %#v err=%v", inserted, err)
	}
	definitionDigest, err := explorerselection.ExplicitGroupDefinitionDigest(groups)
	if err != nil {
		t.Fatal(err)
	}
	membershipDigest, err := explorerselection.ExplicitGroupMembershipDigest(header, groups, memberships)
	if err != nil {
		t.Fatal(err)
	}
	actualDefinitionDigest, actualMembershipDigest, groupCount, memberCount, err := persistence.DigestExplicitGroupRevision(ctx, project, header.ID)
	if err != nil || actualDefinitionDigest != definitionDigest || actualMembershipDigest != membershipDigest || groupCount != 3 || memberCount != 3 {
		t.Fatalf("staged digest = %s/%s groups=%d members=%d err=%v, want %s/%s groups=3 members=3", actualDefinitionDigest, actualMembershipDigest, groupCount, memberCount, err, definitionDigest, membershipDigest)
	}
	if _, _, _, _, err := persistence.DigestExplicitGroupRevision(ctx, "another-project", header.ID); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionNotFound) {
		t.Fatalf("cross-project digest lookup = %v", err)
	}
	if _, err := persistence.CompleteExplicitGroupRevision(ctx, header.ID, writer, definitionDigest, membershipDigest, groupCount, memberCount+1, time.Now().UTC()); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("wrong completion count = %v", err)
	}
	completed, err := persistence.CompleteExplicitGroupRevision(ctx, header.ID, writer, definitionDigest, membershipDigest, groupCount, memberCount, time.Now().UTC())
	if err != nil || completed.State != explorerselection.ExplicitGroupRevisionComplete || completed.GroupCount != 3 || completed.MemberCount != 3 {
		t.Fatalf("complete = %#v err=%v", completed, err)
	}
	choices, err := persistence.ListExplicitGroupRevisions(ctx, project, source.Generation, source.ScopeDigest, source.ResourceType, 10)
	if err != nil || len(choices) != 1 || choices[0].ID != header.ID || choices[0].GroupCount != 3 || choices[0].MemberCount != 3 {
		t.Fatalf("complete row-root choices = %#v err=%v", choices, err)
	}
	if otherRoot, err := persistence.ListExplicitGroupRevisions(ctx, project, source.Generation, source.ScopeDigest, "Observation", 10); err != nil || len(otherRoot) != 0 {
		t.Fatalf("other row-root choices = %#v err=%v, want no choices", otherRoot, err)
	}
	if _, err := persistence.CompleteExplicitGroupRevision(ctx, header.ID, uuid.NewString(), definitionDigest, membershipDigest, groupCount, memberCount, time.Now().UTC()); err != nil {
		t.Fatalf("completed retry = %v", err)
	}
	if _, err := persistence.BeginExplicitGroupRevision(ctx, header, source, uuid.NewString()); err != nil {
		t.Fatalf("completed begin retry = %v", err)
	}
	if _, err := persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, memberships[:1]); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("append after completion = %v", err)
	}
	if err := persistence.PutExplicitGroupDefinitions(ctx, header.ID, writer, groups); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("definition write after completion = %v", err)
	}
	if err := completed.ValidateSource(source); err != nil {
		t.Fatalf("matching source selection rejected = %v", err)
	}
	wrongSource.ID = "another-selection"
	if err := completed.ValidateSource(wrongSource); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionStaleSource) {
		t.Fatalf("mismatched source selection accepted = %v", err)
	}
	if err := persistence.AbortExplicitGroupRevision(ctx, header.ID, writer); !errors.Is(err, explorerselection.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("abort after completion = %v", err)
	}

	gotGroups, err := persistence.ListExplicitGroupDefinitions(ctx, project, header.ID)
	if err != nil || !reflect.DeepEqual(gotGroups, groups) {
		t.Fatalf("read definitions = %#v err=%v, want %#v", gotGroups, err, groups)
	}
	if gotGroups[2] != (explorerselection.ExplicitGroupDefinition{ID: "group-empty", Label: "No members", Ordinal: 2}) {
		t.Fatalf("empty declared group was lost: %#v", gotGroups)
	}
	gotMemberships := make([]explorerselection.ExplicitGroupMembership, 0, 3)
	cursor := explorerselection.ExplicitGroupMembershipCursor{}
	for {
		next, err := persistence.VisitExplicitGroupMemberships(ctx, project, header.ID, cursor, 1, func(membership explorerselection.ExplicitGroupMembership) error {
			gotMemberships = append(gotMemberships, membership)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if next == cursor {
			break
		}
		cursor = next
	}
	wantMemberships := []explorerselection.ExplicitGroupMembership{{GroupID: "group-a", Ref: sharedRef.Canonical()}, {GroupID: "group-a", Ref: onlyARef.Canonical()}, {GroupID: "group-b", Ref: sharedRef.Canonical()}}
	if !reflect.DeepEqual(gotMemberships, wantMemberships) {
		t.Fatalf("read memberships = %#v, want %#v", gotMemberships, wantMemberships)
	}

	testExplicitGroupConcurrentBegin(ctx, t, persistence, explicitGroupTestRevision(source, "concurrent"), source)
	testExplicitGroupCleanup(ctx, t, client, persistence, source)
}

func testExplicitGroupConcurrentBegin(ctx context.Context, t *testing.T, repository explorerselection.ExplicitGroupRepository, revision explorerselection.ExplicitGroupRevision, source explorerselection.SelectionRevision) {
	t.Helper()
	tokens := []string{uuid.NewString(), uuid.NewString()}
	errs := make([]error, len(tokens))
	var wg sync.WaitGroup
	for i, token := range tokens {
		wg.Add(1)
		go func(i int, token string) {
			defer wg.Done()
			_, errs[i] = repository.BeginExplicitGroupRevision(ctx, revision, source, token)
		}(i, token)
	}
	wg.Wait()
	successes, conflicts := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, explorerselection.ErrExplicitGroupRevisionConflict) {
			conflicts++
		} else {
			t.Fatalf("concurrent begin error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent begin results = %#v, want one owner and one conflict", errs)
	}
	for i, err := range errs {
		if err == nil {
			if abortErr := repository.AbortExplicitGroupRevision(ctx, revision.ID, tokens[i]); abortErr != nil {
				t.Fatalf("abort concurrent staging revision = %v", abortErr)
			}
			return
		}
	}
}

func testExplicitGroupCleanup(ctx context.Context, t *testing.T, client *store.Client, repository explorerselection.ExplicitGroupRepository, source explorerselection.SelectionRevision) {
	t.Helper()
	revision := explicitGroupTestRevision(source, "cleanup")
	revision.CreatedAt = time.Now().UTC().Add(-time.Hour)
	writer := uuid.NewString()
	if _, err := repository.BeginExplicitGroupRevision(ctx, revision, source, writer); err != nil {
		t.Fatal(err)
	}
	groups := []explorerselection.ExplicitGroupDefinition{{ID: "group-a", Label: "Alpha", Ordinal: 0}}
	if err := repository.PutExplicitGroupDefinitions(ctx, revision.ID, writer, groups); err != nil {
		t.Fatal(err)
	}
	membership := explorerselection.ExplicitGroupMembership{GroupID: "group-a", Ref: explorerselection.ResourceRef{Project: revision.Project, Generation: revision.Generation, ResourceType: revision.ResourceType, ID: "patient-3"}}
	if _, err := repository.AppendExplicitGroupMemberships(ctx, revision.ID, writer, []explorerselection.ExplicitGroupMembership{membership}); err != nil {
		t.Fatal(err)
	}
	if err := client.ExecuteAQL(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH {writerExpiresAt: 0} IN @@c`, map[string]any{"@c": db.ExplicitGroupRevisionsCollection, "key": string(revision.ID)}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CleanupExplicitGroupStaging(ctx, time.Now().UTC(), 1); err != nil {
		t.Fatal(err)
	}
	recoveredWriter := uuid.NewString()
	if _, err := repository.BeginExplicitGroupRevision(ctx, revision, source, recoveredWriter); err != nil {
		t.Fatalf("begin after staging cleanup = %v", err)
	}
	if _, err := repository.AppendExplicitGroupMemberships(ctx, revision.ID, recoveredWriter, []explorerselection.ExplicitGroupMembership{membership}); err == nil {
		t.Fatal("membership append succeeded before declared groups were restored")
	}
	if err := repository.PutExplicitGroupDefinitions(ctx, revision.ID, recoveredWriter, groups); err != nil {
		t.Fatal(err)
	}
	_, _, groupCount, memberCount, err := repository.DigestExplicitGroupRevision(ctx, revision.Project, revision.ID)
	if err != nil || groupCount != 1 || memberCount != 0 {
		t.Fatalf("recovered staging counts = %d/%d err=%v", groupCount, memberCount, err)
	}
	if err := repository.AbortExplicitGroupRevision(ctx, revision.ID, recoveredWriter); err != nil {
		t.Fatal(err)
	}
}

func explicitGroupTestSelection(project string) explorerselection.SelectionRevision {
	return explorerselection.SelectionRevision{
		ID: "selection-" + uuid.NewString(), Project: project, Generation: "generation-a", ResourceType: "Patient",
		Rule: explorerselection.SelectionRule{Kind: explorerselection.SelectionRuleExplicit}, Source: explorerselection.SelectionSource{Kind: explorerselection.SelectionSourceExplicit},
		ScopeDigest: "scope-a", RuleDigest: "selection-rule", IdempotencyKey: uuid.NewString(), CreatedAt: time.Now().UTC(),
	}
}

func persistExplicitGroupTestSelection(ctx context.Context, t *testing.T, persistence *Store, project string) (explorerselection.SelectionRevision, string, error) {
	t.Helper()
	selection := explicitGroupTestSelection(project)
	writer := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selection, writer); err != nil {
		return explorerselection.SelectionRevision{}, writer, err
	}
	members := []explorerselection.SelectionMember{
		{Ref: explorerselection.ResourceRef{Project: project, Generation: selection.Generation, ResourceType: selection.ResourceType, ID: "patient-1"}},
		{Ref: explorerselection.ResourceRef{Project: project, Generation: selection.Generation, ResourceType: selection.ResourceType, ID: "patient-2"}},
		{Ref: explorerselection.ResourceRef{Project: project, Generation: selection.Generation, ResourceType: selection.ResourceType, ID: "patient-3"}},
	}
	if _, err := persistence.AppendSelectionMembers(ctx, selection.ID, writer, members); err != nil {
		return explorerselection.SelectionRevision{}, writer, err
	}
	digest, count, bytes, err := persistence.DigestSelectionMembers(ctx, project, selection.ID)
	if err != nil {
		return explorerselection.SelectionRevision{}, writer, err
	}
	completed, err := persistence.CompleteSelection(ctx, selection.ID, writer, digest, count, bytes, time.Now().UTC())
	if err != nil {
		return explorerselection.SelectionRevision{}, writer, err
	}
	return *completed, writer, nil
}

func explicitGroupTestRevision(source explorerselection.SelectionRevision, key string) explorerselection.ExplicitGroupRevision {
	createdAt := time.Now().UTC()
	idempotencyKey := fmt.Sprintf("%s-%s", key, uuid.NewString())
	return explorerselection.ExplicitGroupRevision{
		ID: explorerselection.ExplicitGroupRevisionIDFor(source.Project, idempotencyKey), Project: source.Project, Generation: source.Generation,
		ScopeDigest: source.ScopeDigest, ResourceType: source.ResourceType, SourceSelectionRevisionID: source.ID,
		SourceMembershipDigest: source.MembershipDigest, State: explorerselection.ExplicitGroupRevisionStaging,
		IdempotencyKey: idempotencyKey, CreatedAt: createdAt,
	}
}

func TestPutExplicitGroupDefinitionsUsesObjectRows(t *testing.T) {
	ctx := context.Background()
	revision := explorerselection.ExplicitGroupRevision{
		ID: explorerselection.ExplicitGroupRevisionIDFor("project-a", "groups-a"), Project: "project-a",
		Generation: "generation-a", ScopeDigest: "scope-a", ResourceType: "Patient",
		SourceSelectionRevisionID: "selection-a", SourceMembershipDigest: "source-membership-a",
		State: explorerselection.ExplicitGroupRevisionStaging, IdempotencyKey: "groups-a", CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	header, err := db.Document(revision, string(revision.ID))
	if err != nil {
		t.Fatal(err)
	}
	header["writerToken"] = "writer-a"
	client := &explicitGroupDefinitionClient{header: header}
	repository, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	want := []explorerselection.ExplicitGroupDefinition{{ID: "group-empty", Label: "Empty group", Ordinal: 0}}
	if err := repository.PutExplicitGroupDefinitions(ctx, revision.ID, "writer-a", want); err != nil {
		t.Fatalf("put definitions: %v", err)
	}
	if !client.definitionWriteReturnedObject {
		t.Fatal("definition UPSERT did not return a map-shaped row")
	}
	definitionDigest, err := explorerselection.ExplicitGroupDefinitionDigest(want)
	if err != nil {
		t.Fatal(err)
	}
	membershipDigest, err := explorerselection.ExplicitGroupMembershipDigest(revision, want, nil)
	if err != nil {
		t.Fatal(err)
	}
	completed := revision
	completed.State = explorerselection.ExplicitGroupRevisionComplete
	completed.DefinitionDigest = definitionDigest
	completed.MembershipDigest = membershipDigest
	completed.GroupCount = 1
	completedAt := time.Date(2026, time.January, 1, 0, 1, 0, 0, time.UTC)
	completed.CompletedAt = &completedAt
	client.header, err = db.Document(completed, string(completed.ID))
	if err != nil {
		t.Fatal(err)
	}
	got, err := repository.ListExplicitGroupDefinitions(ctx, revision.Project, revision.ID)
	if err != nil {
		t.Fatalf("list definitions: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("definitions = %#v, want %#v", got, want)
	}
}

type explicitGroupDefinitionClient struct {
	header                        map[string]any
	definitions                   []map[string]any
	definitionWriteReturnedObject bool
}

func (c *explicitGroupDefinitionClient) WithTransaction(ctx context.Context, _ store.TransactionCollections, fn store.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *explicitGroupDefinitionClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit store.RowVisitor) error {
	switch {
	case strings.Contains(query, "UPDATE d WITH {writerExpiresAt: @expiresAt}"):
		return visit(map[string]any{"renewed": true})
	case strings.Contains(query, "SORT g.ordinal ASC, g.groupId ASC"):
		for _, definition := range c.definitions {
			if err := visit(definition); err != nil {
				return err
			}
		}
		return nil
	case strings.Contains(query, "FOR doc IN @docs"):
		if !strings.Contains(query, "RETURN {key: NEW._key}") {
			return fmt.Errorf("definition UPSERT must return an object row for QueryRows")
		}
		c.definitionWriteReturnedObject = true
		docs, ok := binds["docs"].([]map[string]any)
		if !ok {
			return fmt.Errorf("definition UPSERT docs have type %T", binds["docs"])
		}
		for _, definition := range docs {
			c.definitions = append(c.definitions, definition)
			if err := visit(map[string]any{"key": definition["_key"]}); err != nil {
				return err
			}
		}
		return nil
	case strings.Contains(query, "RETURN d"):
		return visit(c.header)
	default:
		return fmt.Errorf("unexpected explicit-group query: %s", query)
	}
}

func TestExplicitGroupDefinitionKeysPreservePersistedFraming(t *testing.T) {
	for _, tc := range []struct {
		revision explorerselection.ExplicitGroupRevisionID
		group    explorerselection.ExplicitGroupID
		want     string
	}{
		{"", "", "groupdef_374708fff7719dd5979ec875d56cd2286f6d3cf7ec317a3b25632aab28ec37bb"},
		{"a", "bc", "groupdef_3fafa1cf2f19a7c1129beb20cf0983f73a489a221fc0dd2f16d1be292d089205"},
		{"ab", "c", "groupdef_601d5476e2ccfe2c87a2bba7a322659734a05749d5b5aa781f513e4912db0d5f"},
		{"rév", "x\x00y", "groupdef_f43a1e971adc0b430b4e31e5a8791e8d000703b511202cd45cbf9a111bbc128f"},
	} {
		if got := explicitGroupDefinitionKey(tc.revision, tc.group); got != tc.want {
			t.Fatalf("definition key for %q/%q = %q, want %q", tc.revision, tc.group, got, tc.want)
		}
	}
}
