package arango

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
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

	project := "loom_explicit_group_test_" + uuid.NewString()
	source, sourceWriter, err := persistExplicitGroupTestSelection(ctx, t, persistence, project)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = persistence.AbortSelection(context.Background(), source.ID, sourceWriter) }()
	header := explicitGroupTestRevision(source, "primary")
	writer := uuid.NewString()
	defer func() { _ = persistence.AbortExplicitGroupRevision(context.Background(), header.ID, writer) }()

	started, err := persistence.BeginExplicitGroupRevision(ctx, header, source, writer)
	if err != nil || started.ID != header.ID || started.State != explorer.ExplicitGroupRevisionStaging {
		t.Fatalf("begin = %#v err=%v", started, err)
	}
	stagingChoices, err := persistence.ListExplicitGroupRevisions(ctx, project, source.Generation, source.ScopeDigest, source.ResourceType, 10)
	if err != nil || len(stagingChoices) != 0 {
		t.Fatalf("staging row-root choices = %#v err=%v, want no choices", stagingChoices, err)
	}
	if _, err := persistence.BeginExplicitGroupRevision(ctx, header, source, writer); err != nil {
		t.Fatalf("same-owner begin retry = %v", err)
	}
	if _, err := persistence.BeginExplicitGroupRevision(ctx, header, source, uuid.NewString()); !errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("foreign-owner begin = %v", err)
	}
	wrongSource := source
	wrongSource.ScopeDigest = "another-scope"
	if _, err := persistence.BeginExplicitGroupRevision(ctx, header, wrongSource, writer); !errors.Is(err, explorer.ErrExplicitGroupRevisionStaleSource) {
		t.Fatalf("mismatched source begin = %v", err)
	}
	missingSource := source
	missingSource.ID = "selection-" + uuid.NewString()
	missingHeader := explicitGroupTestRevision(missingSource, "missing-source")
	if _, err := persistence.BeginExplicitGroupRevision(ctx, missingHeader, missingSource, uuid.NewString()); !errors.Is(err, explorer.ErrExplicitGroupRevisionStaleSource) {
		t.Fatalf("unavailable source begin = %v", err)
	}

	groups := []explorer.ExplicitGroupDefinition{
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
	changedDefinition := append([]explorer.ExplicitGroupDefinition(nil), groups...)
	changedDefinition[0].Label = "Changed intent"
	if err := persistence.PutExplicitGroupDefinitions(ctx, header.ID, writer, changedDefinition); !errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("changed definition retry = %v", err)
	}

	sharedRef := explorer.ResourceRef{Project: project, Generation: source.Generation, ResourceType: source.ResourceType, ID: "patient-1"}
	onlyARef := explorer.ResourceRef{Project: project, Generation: source.Generation, ResourceType: source.ResourceType, ID: "patient-2"}
	memberships := []explorer.ExplicitGroupMembership{
		{GroupID: "group-a", Ref: sharedRef},
		{GroupID: "group-a", Ref: sharedRef},
		{GroupID: "group-b", Ref: sharedRef},
		{GroupID: "group-a", Ref: onlyARef},
	}
	wrongScopeBatch := append([]explorer.ExplicitGroupMembership(nil), memberships[:1]...)
	wrongScopeBatch = append(wrongScopeBatch, explorer.ExplicitGroupMembership{GroupID: "group-b", Ref: explorer.ResourceRef{Project: "another-project", Generation: source.Generation, ResourceType: source.ResourceType, ID: "patient-3"}})
	if _, err := persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, wrongScopeBatch); !errors.Is(err, explorer.ErrResourceRefScopeMismatch) {
		t.Fatalf("cross-project append = %v", err)
	}
	inserted, err := persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, memberships)
	if err != nil || !reflect.DeepEqual(inserted, []explorer.ExplicitGroupMembership{{GroupID: "group-a", Ref: sharedRef.Canonical()}, {GroupID: "group-a", Ref: onlyARef.Canonical()}, {GroupID: "group-b", Ref: sharedRef.Canonical()}}) {
		t.Fatalf("first append = %#v err=%v", inserted, err)
	}
	inserted, err = persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, memberships)
	if err != nil || len(inserted) != 0 {
		t.Fatalf("membership retry = %#v err=%v", inserted, err)
	}
	definitionDigest, err := explorer.ExplicitGroupDefinitionDigest(groups)
	if err != nil {
		t.Fatal(err)
	}
	membershipDigest, err := explorer.ExplicitGroupMembershipDigest(header, groups, memberships)
	if err != nil {
		t.Fatal(err)
	}
	actualDefinitionDigest, actualMembershipDigest, groupCount, memberCount, err := persistence.DigestExplicitGroupRevision(ctx, project, header.ID)
	if err != nil || actualDefinitionDigest != definitionDigest || actualMembershipDigest != membershipDigest || groupCount != 3 || memberCount != 3 {
		t.Fatalf("staged digest = %s/%s groups=%d members=%d err=%v, want %s/%s groups=3 members=3", actualDefinitionDigest, actualMembershipDigest, groupCount, memberCount, err, definitionDigest, membershipDigest)
	}
	if _, _, _, _, err := persistence.DigestExplicitGroupRevision(ctx, "another-project", header.ID); !errors.Is(err, explorer.ErrExplicitGroupRevisionNotFound) {
		t.Fatalf("cross-project digest lookup = %v", err)
	}
	if _, err := persistence.CompleteExplicitGroupRevision(ctx, header.ID, writer, definitionDigest, membershipDigest, groupCount, memberCount+1, time.Now().UTC()); !errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("wrong completion count = %v", err)
	}
	completed, err := persistence.CompleteExplicitGroupRevision(ctx, header.ID, writer, definitionDigest, membershipDigest, groupCount, memberCount, time.Now().UTC())
	if err != nil || completed.State != explorer.ExplicitGroupRevisionComplete || completed.GroupCount != 3 || completed.MemberCount != 3 {
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
	if _, err := persistence.AppendExplicitGroupMemberships(ctx, header.ID, writer, memberships[:1]); !errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("append after completion = %v", err)
	}
	if err := persistence.PutExplicitGroupDefinitions(ctx, header.ID, writer, groups); !errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("definition write after completion = %v", err)
	}
	if err := completed.ValidateSource(source); err != nil {
		t.Fatalf("matching source selection rejected = %v", err)
	}
	wrongSource.ID = "another-selection"
	if err := completed.ValidateSource(wrongSource); !errors.Is(err, explorer.ErrExplicitGroupRevisionStaleSource) {
		t.Fatalf("mismatched source selection accepted = %v", err)
	}
	if err := persistence.AbortExplicitGroupRevision(ctx, header.ID, writer); !errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) {
		t.Fatalf("abort after completion = %v", err)
	}

	gotGroups, err := persistence.ListExplicitGroupDefinitions(ctx, project, header.ID)
	if err != nil || !reflect.DeepEqual(gotGroups, groups) {
		t.Fatalf("read definitions = %#v err=%v, want %#v", gotGroups, err, groups)
	}
	if gotGroups[2] != (explorer.ExplicitGroupDefinition{ID: "group-empty", Label: "No members", Ordinal: 2}) {
		t.Fatalf("empty declared group was lost: %#v", gotGroups)
	}
	gotMemberships := make([]explorer.ExplicitGroupMembership, 0, 3)
	cursor := explorer.ExplicitGroupMembershipCursor{}
	for {
		next, err := persistence.VisitExplicitGroupMemberships(ctx, project, header.ID, cursor, 1, func(membership explorer.ExplicitGroupMembership) error {
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
	wantMemberships := []explorer.ExplicitGroupMembership{{GroupID: "group-a", Ref: sharedRef.Canonical()}, {GroupID: "group-a", Ref: onlyARef.Canonical()}, {GroupID: "group-b", Ref: sharedRef.Canonical()}}
	if !reflect.DeepEqual(gotMemberships, wantMemberships) {
		t.Fatalf("read memberships = %#v, want %#v", gotMemberships, wantMemberships)
	}

	testExplicitGroupConcurrentBegin(ctx, t, persistence, explicitGroupTestRevision(source, "concurrent"), source)
	testExplicitGroupCleanup(ctx, t, client, persistence, source)
}

func testExplicitGroupConcurrentBegin(ctx context.Context, t *testing.T, repository explorer.ExplicitGroupRepository, revision explorer.ExplicitGroupRevision, source explorer.SelectionRevision) {
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
		} else if errors.Is(err, explorer.ErrExplicitGroupRevisionConflict) {
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

func testExplicitGroupCleanup(ctx context.Context, t *testing.T, client *store.Client, repository explorer.ExplicitGroupRepository, source explorer.SelectionRevision) {
	t.Helper()
	revision := explicitGroupTestRevision(source, "cleanup")
	revision.CreatedAt = time.Now().UTC().Add(-time.Hour)
	writer := uuid.NewString()
	if _, err := repository.BeginExplicitGroupRevision(ctx, revision, source, writer); err != nil {
		t.Fatal(err)
	}
	groups := []explorer.ExplicitGroupDefinition{{ID: "group-a", Label: "Alpha", Ordinal: 0}}
	if err := repository.PutExplicitGroupDefinitions(ctx, revision.ID, writer, groups); err != nil {
		t.Fatal(err)
	}
	membership := explorer.ExplicitGroupMembership{GroupID: "group-a", Ref: explorer.ResourceRef{Project: revision.Project, Generation: revision.Generation, ResourceType: revision.ResourceType, ID: "patient-expired"}}
	if _, err := repository.AppendExplicitGroupMemberships(ctx, revision.ID, writer, []explorer.ExplicitGroupMembership{membership}); err != nil {
		t.Fatal(err)
	}
	if err := client.ExecuteAQL(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH {writerExpiresAt: 0} IN @@c`, map[string]any{"@c": ExplicitGroupRevisionsCollection, "key": string(revision.ID)}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CleanupExplicitGroupStaging(ctx, time.Now().UTC(), 1); err != nil {
		t.Fatal(err)
	}
	recoveredWriter := uuid.NewString()
	if _, err := repository.BeginExplicitGroupRevision(ctx, revision, source, recoveredWriter); err != nil {
		t.Fatalf("begin after staging cleanup = %v", err)
	}
	if _, err := repository.AppendExplicitGroupMemberships(ctx, revision.ID, recoveredWriter, []explorer.ExplicitGroupMembership{membership}); err == nil {
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

func explicitGroupTestSelection(project string) explorer.SelectionRevision {
	return explorer.SelectionRevision{
		ID: "selection-" + uuid.NewString(), Project: project, Generation: "generation-a", ResourceType: "Patient",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: "scope-a", RuleDigest: "selection-rule", IdempotencyKey: uuid.NewString(), CreatedAt: time.Now().UTC(),
	}
}

func persistExplicitGroupTestSelection(ctx context.Context, t *testing.T, persistence *Store, project string) (explorer.SelectionRevision, string, error) {
	t.Helper()
	selection := explicitGroupTestSelection(project)
	writer := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selection, writer); err != nil {
		return explorer.SelectionRevision{}, writer, err
	}
	member := explorer.SelectionMember{Ref: explorer.ResourceRef{Project: project, Generation: selection.Generation, ResourceType: selection.ResourceType, ID: "source-patient"}}
	if _, err := persistence.AppendSelectionMembers(ctx, selection.ID, writer, []explorer.SelectionMember{member}); err != nil {
		return explorer.SelectionRevision{}, writer, err
	}
	digest, count, bytes, err := persistence.DigestSelectionMembers(ctx, project, selection.ID)
	if err != nil {
		return explorer.SelectionRevision{}, writer, err
	}
	completed, err := persistence.CompleteSelection(ctx, selection.ID, writer, digest, count, bytes, time.Now().UTC())
	if err != nil {
		return explorer.SelectionRevision{}, writer, err
	}
	return *completed, writer, nil
}

func explicitGroupTestRevision(source explorer.SelectionRevision, key string) explorer.ExplicitGroupRevision {
	createdAt := time.Now().UTC()
	idempotencyKey := fmt.Sprintf("%s-%s", key, uuid.NewString())
	return explorer.ExplicitGroupRevision{
		ID: explorer.ExplicitGroupRevisionIDFor(source.Project, idempotencyKey), Project: source.Project, Generation: source.Generation,
		ScopeDigest: source.ScopeDigest, ResourceType: source.ResourceType, SourceSelectionRevisionID: source.ID,
		SourceMembershipDigest: source.MembershipDigest, State: explorer.ExplicitGroupRevisionStaging,
		IdempotencyKey: idempotencyKey, CreatedAt: createdAt,
	}
}
