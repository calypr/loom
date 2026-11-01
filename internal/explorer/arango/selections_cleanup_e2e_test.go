package arango

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// These tests drive the real selection staging lifecycle against a private
// ArangoDB database and only ever call production Store methods. They exist to
// test two claims about CleanupSelectionStaging (selections.go:384) instead of
// trusting its comment at selections.go:37-40:
//
//	H3-a: a writer that is legitimately mid-operation but whose writer lease has
//	      lapsed is never touched by another session's BeginSelection.
//	H3-b: cleanup cannot destroy a completed selection, because it omits the
//	      complete != true guard that AbortSelection (selections.go:290) and
//	      CompleteSelection (selections.go:242) both carry.
//
// Databases use the loom_exv_ prefix so this suite's residue is checkable
// independently of other verification workers.

const selectionCleanupDatabasePrefix = "loom_exv_"

type selectionCleanupDocument struct {
	exists                bool
	state                 string
	complete              bool
	hasWriterExpiresAt    bool
	writerExpiresAtMillis int64
	createdAt             string
	memberCount           int64
}

func selectionCleanupOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *Store) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := selectionCleanupDatabasePrefix + "cleanup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, selectionCleanupDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, selectionCleanupDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("SELECTION_CLEANUP_DATABASE=%s", name)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", name, err)
			return
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("cleanup %s: %v", name, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("cleanup %s status=%d", name, response.StatusCode)
			return
		}
		t.Logf("SELECTION_CLEANUP_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap %s: %v", name, err)
	}
	persistence, err := New(client)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return ctx, client, persistence
}

func selectionCleanupInspect(t *testing.T, ctx context.Context, client *store.Client, selectionID string) selectionCleanupDocument {
	t.Helper()
	info := selectionCleanupDocument{}
	if err := client.QueryRows(ctx, `LET d = DOCUMENT(@@headers, @key)
RETURN {missing: d == null, state: d == null ? null : d.state, complete: d != null AND d.complete == true, hasWriterExpiresAt: d != null && HAS(d, "writerExpiresAt"), writerExpiresAt: d == null ? null : d.writerExpiresAt, createdAt: d == null ? null : d.createdAt}`, 1, map[string]any{"@headers": SelectionsCollection, "key": selectionID}, func(row map[string]any) error {
		missing, _ := row["missing"].(bool)
		info.exists = !missing
		info.state, _ = row["state"].(string)
		info.complete, _ = row["complete"].(bool)
		info.hasWriterExpiresAt, _ = row["hasWriterExpiresAt"].(bool)
		info.createdAt, _ = row["createdAt"].(string)
		switch number := row["writerExpiresAt"].(type) {
		case float64:
			info.writerExpiresAtMillis = int64(number)
		case int64:
			info.writerExpiresAtMillis = number
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect header %s: %v", selectionID, err)
	}
	if !info.exists {
		return info
	}
	if err := client.QueryRows(ctx, `RETURN {count: LENGTH(FOR m IN @@members FILTER m.selectionId == @key RETURN 1)}`, 1, map[string]any{"@members": SelectionMembersCollection, "key": selectionID}, func(row map[string]any) error {
		switch number := row["count"].(type) {
		case float64:
			info.memberCount = int64(number)
		case int64:
			info.memberCount = number
		}
		return nil
	}); err != nil {
		t.Fatalf("inspect members %s: %v", selectionID, err)
	}
	return info
}

// selectionCleanupExpireLease rewinds writerExpiresAt into the past. Production
// renews the lease only inside AppendSelectionMembers (selections.go:168), so an
// idle gap past the 10m lease has exactly this end state. The existing harness
// uses the same direct patch with the literal 0
// (selection_storage_integration_test.go:29-34); this variant keeps the value
// explicit about how far past the lease the document is.
func selectionCleanupExpireLease(t *testing.T, ctx context.Context, client *store.Client, selectionID string, lapsedFor time.Duration) int64 {
	t.Helper()
	expired := time.Now().UTC().Add(-selectionWriterLease - lapsedFor).UnixMilli()
	if err := client.ExecuteAQL(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH {writerExpiresAt: @writerExpiresAt} IN @@c RETURN NEW._key`, map[string]any{"@c": SelectionsCollection, "key": selectionID, "writerExpiresAt": expired}); err != nil {
		t.Fatalf("rewind writerExpiresAt for %s: %v", selectionID, err)
	}
	t.Logf("SELECTION_CLEANUP_LEASE_REWOUND selection=%s writerExpiresAtMillis=%d (= now - %s - %s) lease=%s", selectionID, expired, selectionWriterLease, lapsedFor, selectionWriterLease)
	return expired
}

// selectionCleanupAssertNoOrphans fails when staged members exist whose header
// is gone. Cleanup removes members and the header in one AQL query, so an orphan
// set is the observable signature of a torn reap.
func selectionCleanupAssertNoOrphans(t *testing.T, ctx context.Context, client *store.Client, label string) {
	t.Helper()
	var orphans int64
	if err := client.QueryRows(ctx, `LET ids = (FOR m IN @@members RETURN DISTINCT m.selectionId)
RETURN {count: LENGTH(FOR id IN ids FILTER DOCUMENT(@@headers, id) == null RETURN id)}`, 1, map[string]any{"@members": SelectionMembersCollection, "@headers": SelectionsCollection}, func(row map[string]any) error {
		switch number := row["count"].(type) {
		case float64:
			orphans = int64(number)
		case int64:
			orphans = number
		}
		return nil
	}); err != nil {
		t.Fatalf("orphan scan after %s: %v", label, err)
	}
	t.Logf("SELECTION_CLEANUP_ORPHAN_MEMBERS=%d after=%s", orphans, label)
	if orphans != 0 {
		t.Errorf("torn or non-transactional reap after %s: %d member documents without a header", label, orphans)
	}
}

func selectionCleanupHeader(project, selectionID, idempotencyKey string, createdAt time.Time) explorer.SelectionRevision {
	return explorer.SelectionRevision{
		ID: selectionID, Project: project, Generation: "generation-a", ResourceType: "DocumentReference",
		Rule:           explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
		Source:         explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		ScopeDigest:    "cleanup-scope",
		RuleDigest:     "cleanup-rule",
		IdempotencyKey: idempotencyKey,
		CreatedAt:      createdAt,
	}
}

func selectionCleanupMembers(project, generation, resourceType, prefix string, count int) []explorer.SelectionMember {
	members := make([]explorer.SelectionMember, count)
	for i := range members {
		members[i] = explorer.SelectionMember{Ref: explorer.ResourceRef{
			Project: project, Generation: generation, ResourceType: resourceType,
			ID: fmt.Sprintf("%s-%03d", prefix, i),
		}}
	}
	return members
}

// TestSelectionCleanupStagingReapsLeaseLapsedWriterMidDigest reproduces, in
// production order, the un-renewed window that lifecycle/selection.go:393-419
// creates between the last AppendSelectionMembers and CompleteSelection, then
// lets a second session call BeginSelection (which unconditionally runs
// CleanupSelectionStaging at selections.go:41).
func TestSelectionCleanupStagingReapsLeaseLapsedWriterMidDigest(t *testing.T) {
	ctx, client, persistence := selectionCleanupOpen(t, 300*time.Second)
	const project = "loom_exv_cleanup_mid_digest"

	// Case 0: harness proof. A genuinely crashed creator (STAGING header, lease
	// lapsed) must be reaped, or the H3-a result below would be vacuous.
	crashedID := "selection-cleanup-crashed-" + uuid.NewString()
	crashedToken := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, crashedID, uuid.NewString(), time.Now().UTC()), crashedToken); err != nil {
		t.Fatalf("crashed creator begin: %v", err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, crashedID, crashedToken, selectionCleanupMembers(project, "generation-a", "DocumentReference", "crashed", 3)); err != nil {
		t.Fatalf("crashed creator append: %v", err)
	}
	selectionCleanupExpireLease(t, ctx, client, crashedID, time.Minute)
	foreignID := "selection-cleanup-foreign-" + uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, foreignID, uuid.NewString(), time.Now().UTC()), uuid.NewString()); err != nil {
		t.Fatalf("foreign begin after crashed creator: %v", err)
	}
	crashed := selectionCleanupInspect(t, ctx, client, crashedID)
	t.Logf("SELECTION_CLEANUP_CRASHED exists=%t state=%q memberCount=%d", crashed.exists, crashed.state, crashed.memberCount)
	if crashed.exists || crashed.memberCount != 0 {
		t.Fatalf("crashed staging creator was not reaped: %#v", crashed)
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "crashed-reap")

	// Case 1: control. A writer inside the lease that is mid-digest must survive
	// another session's BeginSelection and must still be able to complete.
	liveID := "selection-cleanup-live-" + uuid.NewString()
	liveToken := uuid.NewString()
	liveMembers := selectionCleanupMembers(project, "generation-a", "DocumentReference", "live", 3)
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, liveID, uuid.NewString(), time.Now().UTC()), liveToken); err != nil {
		t.Fatalf("live writer begin: %v", err)
	}
	if inserted, err := persistence.AppendSelectionMembers(ctx, liveID, liveToken, liveMembers); err != nil || len(inserted) != 3 {
		t.Fatalf("live writer append inserted=%d err=%v", len(inserted), err)
	}
	// The un-renewed window: digest runs, no lease refresh happens, then a second
	// session calls BeginSelection before this writer completes.
	liveDigest, liveCount, _, err := persistence.DigestSelectionMembers(ctx, project, liveID)
	if err != nil || liveCount != 3 {
		t.Fatalf("live writer digest count=%d err=%v", liveCount, err)
	}
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, foreignID+"-b", uuid.NewString(), time.Now().UTC()), uuid.NewString()); err != nil {
		t.Fatalf("foreign begin while live writer mid-digest: %v", err)
	}
	afterForeign := selectionCleanupInspect(t, ctx, client, liveID)
	t.Logf("SELECTION_CLEANUP_LIVE_WRITER afterForeign exists=%t state=%q complete=%t hasWriterExpiresAt=%t writerExpiresAtMillis=%d memberCount=%d",
		afterForeign.exists, afterForeign.state, afterForeign.complete, afterForeign.hasWriterExpiresAt, afterForeign.writerExpiresAtMillis, afterForeign.memberCount)
	if !afterForeign.exists || afterForeign.state != selectionStagingState || afterForeign.memberCount != 3 {
		t.Fatalf("active writer inside the lease was touched: %#v", afterForeign)
	}
	liveCompleted, err := persistence.CompleteSelection(ctx, liveID, liveToken, liveDigest, liveCount, 0, time.Now().UTC())
	if err != nil || liveCompleted == nil || !liveCompleted.Complete {
		t.Fatalf("live writer complete after foreign begin = %#v err=%v", liveCompleted, err)
	}
	t.Logf("SELECTION_CLEANUP_LIVE_WRITER_COMPLETED digest=%s memberCount=%d", liveCompleted.MembershipDigest, liveCompleted.MemberCount)
	selectionCleanupAssertNoOrphans(t, ctx, client, "live-writer")

	// Case 2: the hypothesis. Identical to case 1, except the lease lapsed while
	// the writer sat in the un-renewed digest phase with no timeout violation.
	idleID := "selection-cleanup-idle-" + uuid.NewString()
	idleToken := uuid.NewString()
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, idleID, uuid.NewString(), time.Now().UTC()), idleToken); err != nil {
		t.Fatalf("idle writer begin: %v", err)
	}
	if inserted, err := persistence.AppendSelectionMembers(ctx, idleID, idleToken, selectionCleanupMembers(project, "generation-a", "DocumentReference", "idle", 3)); err != nil || len(inserted) != 3 {
		t.Fatalf("idle writer append inserted=%d err=%v", len(inserted), err)
	}
	rewound := selectionCleanupExpireLease(t, ctx, client, idleID, time.Minute)
	idleDigest, idleCount, _, err := persistence.DigestSelectionMembers(ctx, project, idleID)
	if err != nil || idleCount != 3 {
		t.Fatalf("idle writer digest count=%d err=%v", idleCount, err)
	}
	secondSessionID := "selection-cleanup-second-" + uuid.NewString()
	secondStarted, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, secondSessionID, uuid.NewString(), time.Now().UTC()), uuid.NewString())
	if err != nil {
		t.Fatalf("second session begin: %v", err)
	}
	victim := selectionCleanupInspect(t, ctx, client, idleID)
	t.Logf("SELECTION_CLEANUP_H3A victimHeader exists=%t state=%q complete=%t hasWriterExpiresAt=%t writerExpiresAtMillis=%d(rewoundTo=%d nowMillis=%d) memberResidue=%d secondSessionComplete=%t",
		victim.exists, victim.state, victim.complete, victim.hasWriterExpiresAt, victim.writerExpiresAtMillis, rewound, time.Now().UTC().UnixMilli(), victim.memberCount, secondStarted.Complete)
	completeOutcome, completeErr := persistence.CompleteSelection(ctx, idleID, idleToken, idleDigest, idleCount, 0, time.Now().UTC())
	if completeErr != nil {
		t.Logf("SELECTION_CLEANUP_H3A_COMPLETE_OUTCOME error=%v notFound=%t incomplete=%t conflict=%t",
			completeErr, errors.Is(completeErr, explorer.ErrSelectionNotFound), errors.Is(completeErr, explorer.ErrSelectionIncomplete), errors.Is(completeErr, explorer.ErrSelectionConflict))
	} else {
		t.Logf("SELECTION_CLEANUP_H3A_COMPLETE_OUTCOME ok complete=%t memberCount=%d digest=%s", completeOutcome.Complete, completeOutcome.MemberCount, completeOutcome.MembershipDigest)
	}
	afterComplete := selectionCleanupInspect(t, ctx, client, idleID)
	t.Logf("SELECTION_CLEANUP_H3A_AFTER_COMPLETE exists=%t state=%q complete=%t memberResidue=%d", afterComplete.exists, afterComplete.state, afterComplete.complete, afterComplete.memberCount)
	if victim.exists != (afterComplete.memberCount > 0) && !victim.exists && afterComplete.memberCount > 0 {
		t.Errorf("header removed but staged members survived: header=%#v members=%d", victim, afterComplete.memberCount)
	}
	if victim.exists && afterComplete.memberCount == 0 {
		t.Errorf("header kept but staged members removed: header=%#v", victim)
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "lease-lapsed-mid-digest")
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, "selection-cleanup-tail-"+uuid.NewString(), uuid.NewString(), time.Now().UTC()), uuid.NewString()); err != nil {
		t.Fatalf("tail begin: %v", err)
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "tail-begin")
}

// TestSelectionCleanupStagingDestroysCompletedStagingHeader probes the missing
// complete != true guard at selections.go:388-400 against the sibling guards in
// AbortSelection (selections.go:290) and CompleteSelection (selections.go:242),
// and separately measures whether production code can reach that state at all.
func TestSelectionCleanupStagingDestroysCompletedStagingHeader(t *testing.T) {
	ctx, client, persistence := selectionCleanupOpen(t, 240*time.Second)
	const project = "loom_exv_cleanup_completed"

	// Reachability probe A: BeginSelection rejects a complete revision, so the
	// immutable staging insert cannot carry complete=true.
	rejected := selectionCleanupHeader(project, "selection-cleanup-reject-"+uuid.NewString(), uuid.NewString(), time.Now().UTC())
	rejected.Complete = true
	rejected.MembershipDigest = strings.Repeat("a", 64)
	rejected.MemberCount = 1
	now := time.Now().UTC()
	rejected.CompletedAt = &now
	_, err := persistence.BeginSelection(ctx, rejected, uuid.NewString())
	t.Logf("SELECTION_CLEANUP_H3B_BEGIN_COMPLETE_ERROR=%v", err)
	if err == nil {
		t.Errorf("BeginSelection accepted a complete revision; state=STAGING with complete=true is directly reachable")
	}

	// Reachability probe B: run the whole production lifecycle and confirm no
	// document ever lands in {state:"STAGING", complete:true}.
	fullID := "selection-cleanup-full-" + uuid.NewString()
	fullToken := uuid.NewString()
	fullMembers := selectionCleanupMembers(project, "generation-a", "DocumentReference", "full", 4)
	if _, err := persistence.BeginSelection(ctx, selectionCleanupHeader(project, fullID, uuid.NewString(), time.Now().UTC()), fullToken); err != nil {
		t.Fatalf("full lifecycle begin: %v", err)
	}
	if _, err := persistence.AppendSelectionMembers(ctx, fullID, fullToken, fullMembers); err != nil {
		t.Fatalf("full lifecycle append: %v", err)
	}
	fullDigest, fullCount, fullBytes, err := persistence.DigestSelectionMembers(ctx, project, fullID)
	if err != nil || fullCount != 4 {
		t.Fatalf("full lifecycle digest count=%d err=%v", fullCount, err)
	}
	if _, err := persistence.CompleteSelection(ctx, fullID, fullToken, fullDigest, fullCount, fullBytes, time.Now().UTC()); err != nil {
		t.Fatalf("full lifecycle complete: %v", err)
	}
	var stagingComplete int64
	if err := client.QueryRows(ctx, `RETURN {count: LENGTH(FOR d IN @@c FILTER d.state == @state AND d.complete == true RETURN d._key)}`, 1, map[string]any{"@c": SelectionsCollection, "state": selectionStagingState}, func(row map[string]any) error {
		switch number := row["count"].(type) {
		case float64:
			stagingComplete = int64(number)
		case int64:
			stagingComplete = number
		}
		return nil
	}); err != nil {
		t.Fatalf("scan staging+complete: %v", err)
	}
	t.Logf("SELECTION_CLEANUP_H3B_PRODUCTION_STAGING_COMPLETE_DOCUMENTS=%d", stagingComplete)
	if stagingComplete != 0 {
		t.Errorf("production lifecycle produced state=STAGING with complete=true")
	}
	tamperedHeader := selectionCleanupInspect(t, ctx, client, fullID)
	t.Logf("SELECTION_CLEANUP_H3B_PRODUCTION_COMPLETED_HEADER exists=%t state=%q complete=%t memberCount=%d", tamperedHeader.exists, tamperedHeader.state, tamperedHeader.complete, tamperedHeader.memberCount)
	if err := persistence.CleanupSelectionStaging(ctx, time.Now().UTC().Add(time.Hour), 100); err != nil {
		t.Fatalf("cleanup with a future horizon: %v", err)
	}
	survivor := selectionCleanupInspect(t, ctx, client, fullID)
	t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_WITH_FUTURE_HORIZON exists=%t memberCount=%d", survivor.exists, survivor.memberCount)
	if !survivor.exists || survivor.memberCount != 4 {
		t.Errorf("completed selection destroyed by cleanup: %#v", survivor)
	}

	// Guard comparison: a document forced into {STAGING, complete:true} by raw
	// AQL (no production path does this) is refused by Abort and Complete but not
	// by Cleanup.
	const guardedID = "selection-cleanup-guarded"
	guardedToken := "guarded-writer-token"
	completedAt := time.Now().UTC().Add(-3 * time.Hour)
	guardedDoc := map[string]any{
		"_key":             guardedID,
		"id":               guardedID,
		"project":          project,
		"generation":       "generation-a",
		"resourceType":     "DocumentReference",
		"rule":             map[string]any{"kind": string(explorer.SelectionRuleExplicit)},
		"source":           map[string]any{"kind": string(explorer.SelectionSourceExplicit)},
		"scopeDigest":      "cleanup-scope",
		"ruleDigest":       "cleanup-rule",
		"membershipDigest": strings.Repeat("b", 64),
		"memberCount":      2,
		"memberBytes":      96,
		"complete":         true,
		"idempotencyKey":   "cleanup-guarded",
		"createdAt":        time.Now().UTC().Add(-4 * time.Hour).Format(time.RFC3339Nano),
		"completedAt":      completedAt.Format(time.RFC3339Nano),
		"state":            selectionStagingState,
		"writerToken":      guardedToken,
		"writerExpiresAt":  time.Now().UTC().Add(-2 * time.Hour).UnixMilli(),
	}
	memberDocs := make([]map[string]any, 0, 2)
	for _, member := range selectionCleanupMembers(project, "generation-a", "DocumentReference", "guarded", 2) {
		memberKey := selectionMemberKey(guardedID, member.Ref)
		memberDocs = append(memberDocs, map[string]any{
			"_key": memberKey, "selectionId": guardedID,
			"ref":       map[string]any{"project": member.Ref.Project, "generation": member.Ref.Generation, "resourceType": member.Ref.ResourceType, "id": member.Ref.ID},
			"memberKey": memberKey,
			"project":   member.Ref.Project, "generation": member.Ref.Generation, "resourceType": member.Ref.ResourceType, "id": member.Ref.ID,
		})
	}
	if err := client.ExecuteAQL(ctx, `INSERT @doc INTO @@headers`, map[string]any{"@headers": SelectionsCollection, "doc": guardedDoc}); err != nil {
		t.Fatalf("seed guarded header: %v", err)
	}
	if err := client.ExecuteAQL(ctx, `FOR doc IN @docs INSERT doc INTO @@members`, map[string]any{"@members": SelectionMembersCollection, "docs": memberDocs}); err != nil {
		t.Fatalf("seed guarded members: %v", err)
	}
	seeded := selectionCleanupInspect(t, ctx, client, guardedID)
	t.Logf("SELECTION_CLEANUP_H3B_SEEDED exists=%t state=%q complete=%t memberCount=%d", seeded.exists, seeded.state, seeded.complete, seeded.memberCount)
	if !seeded.exists || !seeded.complete || seeded.state != selectionStagingState || seeded.memberCount != 2 {
		t.Fatalf("guarded fixture not seeded: %#v", seeded)
	}

	abortErr := persistence.AbortSelection(ctx, guardedID, guardedToken)
	t.Logf("SELECTION_CLEANUP_H3B_ABORT error=%v", abortErr)
	afterAbort := selectionCleanupInspect(t, ctx, client, guardedID)
	t.Logf("SELECTION_CLEANUP_H3B_ABORT_THEN exists=%t memberCount=%d", afterAbort.exists, afterAbort.memberCount)
	if !afterAbort.exists || afterAbort.memberCount != 2 {
		t.Errorf("AbortSelection destroyed a complete=true document despite its guard: %#v", afterAbort)
	}
	_, completeErr := persistence.CompleteSelection(ctx, guardedID, guardedToken, strings.Repeat("b", 64), 2, 96, time.Now().UTC())
	t.Logf("SELECTION_CLEANUP_H3B_COMPLETE error=%v incomplete=%t conflict=%t", completeErr, errors.Is(completeErr, explorer.ErrSelectionIncomplete), errors.Is(completeErr, explorer.ErrSelectionConflict))
	afterComplete := selectionCleanupInspect(t, ctx, client, guardedID)
	if !afterComplete.exists || afterComplete.memberCount != 2 {
		t.Errorf("CompleteSelection destroyed a complete=true document despite its guard: %#v", afterComplete)
	}

	if err := persistence.CleanupSelectionStaging(ctx, time.Now().UTC(), 100); err != nil {
		t.Fatalf("cleanup guarded: %v", err)
	}
	afterCleanup := selectionCleanupInspect(t, ctx, client, guardedID)
	t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_THEN exists=%t state=%q complete=%t memberResidue=%d", afterCleanup.exists, afterCleanup.state, afterCleanup.complete, afterCleanup.memberCount)
	if afterCleanup.exists || afterCleanup.memberCount != 0 {
		t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_PRESERVED_COMPLETED_STAGING exists=%t memberResidue=%d", afterCleanup.exists, afterCleanup.memberCount)
	} else {
		t.Logf("SELECTION_CLEANUP_H3B_CLEANUP_DESTROYED_COMPLETED_STAGING=true")
	}
	selectionCleanupAssertNoOrphans(t, ctx, client, "guarded-cleanup")
}

// TestSelectionCleanupStagingLeaseGapAtScale quantifies the un-renewed gap that
// DigestSelectionMembers (selections.go:181-227) can create at the production
// row bound, so the 10m lease claim is judged against measured work rather than
// an assumed constant.
func TestSelectionCleanupStagingLeaseGapAtScale(t *testing.T) {
	ctx, _, persistence := selectionCleanupOpen(t, 25*time.Minute)
	size := int(lifecycle.DefaultSelectionMaxRows)
	if override := strings.TrimSpace(os.Getenv("LOOM_EXV_SCALE_ROWS")); override != "" {
		parsed, err := strconv.Atoi(override)
		if err != nil || parsed <= 0 {
			t.Fatalf("LOOM_EXV_SCALE_ROWS=%q: %v", override, err)
		}
		size = parsed
	}
	selectionCleanupScaleRun(t, ctx, persistence, "loom_exv_cleanup_scale", "generation-a", "DocumentReference", size)
}

func selectionCleanupScaleRun(t *testing.T, ctx context.Context, persistence *Store, project, generation, resourceType string, size int) {
	t.Helper()
	selectionID := "selection-cleanup-scale-" + uuid.NewString()
	writerToken := uuid.NewString()
	header := selectionCleanupHeader(project, selectionID, uuid.NewString(), time.Now().UTC())
	header.Generation = generation
	header.ResourceType = resourceType
	if _, err := persistence.BeginSelection(ctx, header, writerToken); err != nil {
		t.Fatalf("scale begin: %v", err)
	}
	members := make([]explorer.SelectionMember, size)
	for i := range members {
		members[i] = explorer.SelectionMember{Ref: explorer.ResourceRef{Project: project, Generation: generation, ResourceType: resourceType, ID: fmt.Sprintf("scale-%07d", i)}}
	}
	appendStart := time.Now()
	batches := 0
	for offset := 0; offset < len(members); offset += 256 {
		end := offset + 256
		if end > len(members) {
			end = len(members)
		}
		if _, err := persistence.AppendSelectionMembers(ctx, selectionID, writerToken, members[offset:end]); err != nil {
			t.Fatalf("scale append offset=%d: %v", offset, err)
		}
		batches++
	}
	appendElapsed := time.Since(appendStart)
	// The lease clock last ticked inside the final AppendSelectionMembers
	// (selections.go:168). Everything recorded after this line is un-renewed work.
	unrenewedStart := time.Now()
	digest, count, bytes, err := persistence.DigestSelectionMembers(ctx, project, selectionID)
	if err != nil {
		t.Fatalf("scale digest: %v", err)
	}
	digestElapsed := time.Since(unrenewedStart)
	if count != int64(size) {
		t.Fatalf("scale digest count=%d want=%d", count, size)
	}
	if want := explorer.MembershipDigest(members); digest != want {
		t.Fatalf("scale digest = %s want %s", digest, want)
	}
	if _, err := persistence.CompleteSelection(ctx, selectionID, writerToken, digest, count, bytes, time.Now().UTC()); err != nil {
		t.Fatalf("scale complete: %v", err)
	}
	unrenewedElapsed := time.Since(unrenewedStart)
	t.Logf("SELECTION_CLEANUP_SCALE rows=%d batches=%d appendElapsed=%s digestElapsed=%s unRenewedGapToComplete=%s lease=%s lifecycleSelectionBudget=%s lifecycleMaxRows=%d",
		size, batches, appendElapsed.Round(time.Millisecond), digestElapsed.Round(time.Millisecond), unrenewedElapsed.Round(time.Millisecond), selectionWriterLease, lifecycle.DefaultSelectionMaxDuration, lifecycle.DefaultSelectionMaxRows)
	if unrenewedElapsed >= selectionWriterLease {
		t.Errorf("un-renewed digest+complete gap %s reached the %s writer lease", unrenewedElapsed, selectionWriterLease)
	}
}
