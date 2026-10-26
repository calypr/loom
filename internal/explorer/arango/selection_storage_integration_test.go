package arango

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestSelectionStorageAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	expireStaging := func(selectionID string) {
		err := client.ExecuteAQL(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH {writerExpiresAt: 0} IN @@c`, map[string]any{"@c": SelectionsCollection, "key": selectionID})
		if err != nil {
			t.Fatal(err)
		}
	}
	persistence, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	selectionID := "selection-storage-" + uuid.NewString()
	writerToken := uuid.NewString()
	header := explorer.SelectionRevision{
		ID: selectionID, Project: "loom_selection_test", Generation: "generation-a", ResourceType: "DocumentReference",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest: "scope", RuleDigest: "rule", IdempotencyKey: uuid.NewString(), CreatedAt: time.Now().UTC(),
	}
	defer func() { _ = persistence.AbortSelection(context.Background(), selectionID, writerToken) }()
	started, err := persistence.BeginSelection(ctx, header, writerToken)
	if err != nil || started.ID != selectionID {
		t.Fatalf("begin = %#v err=%v", started, err)
	}
	refs := []explorer.SelectionMember{{Ref: explorer.ResourceRef{Project: header.Project, Generation: header.Generation, ResourceType: header.ResourceType, ID: "files001"}}, {Ref: explorer.ResourceRef{Project: header.Project, Generation: header.Generation, ResourceType: header.ResourceType, ID: "files002"}}, {Ref: explorer.ResourceRef{Project: header.Project, Generation: header.Generation, ResourceType: header.ResourceType, ID: "files001"}}}

	// BeginSelection reaps expired staging leases, allowing an idempotent
	// retry to recover both empty and partially populated crashed attempts.
	expiredEmptyID := "selection-expired-empty-" + uuid.NewString()
	expiredEmpty := header
	expiredEmpty.ID, expiredEmpty.IdempotencyKey = expiredEmptyID, uuid.NewString()
	expiredOwner := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, expiredEmpty, expiredOwner); err != nil {
		t.Fatal(err)
	}
	expireStaging(expiredEmptyID)
	recoveredEmptyOwner := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, expiredEmpty, recoveredEmptyOwner); err != nil {
		t.Fatalf("expired empty retry = %v", err)
	}
	if err := persistence.AbortSelection(ctx, expiredEmptyID, recoveredEmptyOwner); err != nil {
		t.Fatal(err)
	}

	expiredMultiID := "selection-expired-multi-" + uuid.NewString()
	expiredMulti := header
	expiredMulti.ID, expiredMulti.IdempotencyKey = expiredMultiID, uuid.NewString()
	expiredMultiOwner := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, expiredMulti, expiredMultiOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, expiredMultiID, expiredMultiOwner, refs[:2]); err != nil {
		t.Fatal(err)
	}
	expireStaging(expiredMultiID)
	recoveredMultiOwner := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, expiredMulti, recoveredMultiOwner); err != nil {
		t.Fatalf("expired multi retry = %v", err)
	}
	if _, recoveredCount, _, err := persistence.DigestSelectionMembers(ctx, expiredMulti.Project, expiredMultiID); err != nil || recoveredCount != 0 {
		t.Fatalf("expired multi rows survived cleanup: count=%d err=%v", recoveredCount, err)
	}
	if err := persistence.AbortSelection(ctx, expiredMultiID, recoveredMultiOwner); err != nil {
		t.Fatal(err)
	}

	activeID := "selection-active-lease-" + uuid.NewString()
	active := header
	active.ID, active.IdempotencyKey = activeID, uuid.NewString()
	activeOwner := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, active, activeOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.BeginSelection(ctx, active, uuid.NewString()); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("active lease was reclaimed: %v", err)
	}
	if err := persistence.AbortSelection(ctx, activeID, activeOwner); err != nil {
		t.Fatal(err)
	}
	inserted, err := persistence.AppendSelectionMembers(ctx, selectionID, writerToken, refs)
	if err != nil || len(inserted) != 2 {
		t.Fatalf("first append = %d err=%v", len(inserted), err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, selectionID, uuid.NewString(), refs[:1]); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("foreign writer append err=%v", err)
	}
	inserted, err = persistence.AppendSelectionMembers(ctx, selectionID, writerToken, refs)
	if err != nil || len(inserted) != 0 {
		t.Fatalf("retry append = %d err=%v", len(inserted), err)
	}
	digest, count, _, err := persistence.DigestSelectionMembers(ctx, header.Project, selectionID)
	if err != nil || count != 2 || digest != explorer.MembershipDigest(refs[:2]) {
		t.Fatalf("digest = %s count=%d err=%v", digest, count, err)
	}
	completed, err := persistence.CompleteSelection(ctx, selectionID, writerToken, digest, count, 0, time.Now().UTC())
	if err != nil || !completed.Complete {
		t.Fatalf("complete = %#v err=%v", completed, err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, selectionID, writerToken, refs[:1]); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("append after complete err=%v", err)
	}
	retry, err := persistence.BeginSelection(ctx, header, uuid.NewString())
	if err != nil || !retry.Complete || retry.MembershipDigest != digest {
		t.Fatalf("idempotent begin = %#v err=%v", retry, err)
	}
	changed := header
	changed.RuleDigest = "different-intent"
	if _, err := persistence.BeginSelection(ctx, changed, uuid.NewString()); !errors.Is(err, explorer.ErrSelectionConflict) {
		t.Fatalf("changed idempotency intent err=%v", err)
	}

	// A stale retry may race a live writer, but its abort must not remove the
	// live writer's staged members. Repeat this against production Arango
	// transactions so a write-skew implementation cannot pass by luck.
	for i := 0; i < 50; i++ {
		raceID := "selection-owner-race-" + uuid.NewString()
		owner := uuid.NewString()
		foreignOwner := uuid.NewString()
		raceHeader := header
		raceHeader.ID, raceHeader.IdempotencyKey = raceID, uuid.NewString()
		if _, err := persistence.BeginSelection(ctx, raceHeader, owner); err != nil {
			t.Fatal(err)
		}
		var appendErr, abortErr error
		done := make(chan struct{}, 2)
		go func() {
			_, appendErr = persistence.AppendSelectionMembers(ctx, raceID, owner, []explorer.SelectionMember{refs[0]})
			done <- struct{}{}
		}()
		go func() {
			abortErr = persistence.AbortSelection(ctx, raceID, foreignOwner)
			done <- struct{}{}
		}()
		<-done
		<-done
		if appendErr != nil || abortErr != nil {
			t.Fatalf("owner race append=%v abort=%v", appendErr, abortErr)
		}
		tracedigest, tracecount, _, err := persistence.DigestSelectionMembers(ctx, raceHeader.Project, raceID)
		if err != nil || tracecount != 1 {
			t.Fatalf("owner race digest=%s count=%d err=%v", tracedigest, tracecount, err)
		}
		if _, err := persistence.CompleteSelection(ctx, raceID, owner, tracedigest, tracecount, 0, time.Now().UTC()); err != nil {
			t.Fatalf("owner race complete err=%v", err)
		}
		if err := persistence.AbortSelection(ctx, raceID, foreignOwner); err != nil {
			t.Fatalf("foreign owner abort after complete err=%v", err)
		}
	}

	partialID := "selection-partial-" + uuid.NewString()
	partial := header
	partial.ID, partial.IdempotencyKey = partialID, uuid.NewString()
	partialWriter := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, partial, partialWriter); err != nil {
		t.Fatal(err)
	}
	_, err = persistence.AppendSelectionMembers(ctx, partialID, partialWriter, []explorer.SelectionMember{refs[0], {Ref: explorer.ResourceRef{Project: "other", Generation: header.Generation, ResourceType: header.ResourceType, ID: "files003"}}})
	if err == nil {
		t.Fatal("mixed-project append unexpectedly succeeded")
	}
	_, count, _, err = persistence.DigestSelectionMembers(ctx, partial.Project, partialID)
	if err != nil || count != 0 {
		t.Fatalf("partial append leaked rows: count=%d err=%v", count, err)
	}
	if err := persistence.AbortSelection(ctx, partialID, partialWriter); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetSelection(ctx, partial.Project, partialID); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("zero-member abort left header: %v", err)
	}

	multiID := "selection-abort-multi-" + uuid.NewString()
	multiWriter := uuid.NewString()
	multi := header
	multi.ID, multi.IdempotencyKey = multiID, uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, multi, multiWriter); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, multiID, multiWriter, refs[:2]); err != nil {
		t.Fatal(err)
	}
	if err := persistence.AbortSelection(ctx, multiID, multiWriter); err != nil {
		t.Fatal(err)
	}
	if _, err := persistence.GetSelection(ctx, multi.Project, multiID); !errors.Is(err, explorer.ErrSelectionNotFound) {
		t.Fatalf("multi-member abort left header: %v", err)
	}
}
