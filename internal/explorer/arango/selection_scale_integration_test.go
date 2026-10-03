package arango

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// TestSelectionStorageScaleAgainstArango measures the production selection
// path at the plan's probe sizes. It deliberately uses the same Begin,
// Append, Digest, and Complete methods as lifecycle requests; no fake store
// or in-memory digest substitute is involved.
func TestSelectionStorageScaleAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
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

	const (
		batchSize    = 256
		project      = "loom_selection_scale"
		generation   = "selection-scale-generation"
		resourceType = "DocumentReference"
	)
	runID := uuid.NewString()
	for _, size := range []int{100, 10_000, 100_000} {
		selectionID := "selection-scale-" + runID + "-" + fmt.Sprint(size)
		writerToken := uuid.NewString()
		header := explorer.SelectionRevision{
			ID: selectionID, Project: project, Generation: generation, ResourceType: resourceType,
			Rule:        explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit},
			Source:      explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
			ScopeDigest: "scale-scope", RuleDigest: "scale-rule-" + fmt.Sprint(size),
			IdempotencyKey: "scale-" + runID + "-" + fmt.Sprint(size), CreatedAt: time.Now().UTC(),
		}
		started := time.Now()
		if _, err := persistence.BeginSelection(ctx, header, writerToken); err != nil {
			t.Fatalf("rows=%d begin: %v", size, err)
		}
		refs := make([]explorer.SelectionMember, size)
		for i := range refs {
			refs[i] = explorer.SelectionMember{Ref: explorer.ResourceRef{
				Project: project, Generation: generation, ResourceType: resourceType,
				ID: fmt.Sprintf("scale-%s-%06d", runID, i),
			}}
		}
		batches := 0
		for offset := 0; offset < len(refs); offset += batchSize {
			end := offset + batchSize
			if end > len(refs) {
				end = len(refs)
			}
			if _, err := persistence.AppendSelectionMembers(ctx, selectionID, writerToken, refs[offset:end]); err != nil {
				t.Fatalf("rows=%d append offset=%d: %v", size, offset, err)
			}
			batches++
		}
		digest, count, bytes, err := persistence.DigestSelectionMembers(ctx, project, selectionID)
		if err != nil {
			t.Fatalf("rows=%d digest: %v", size, err)
		}
		wantDigest := explorer.MembershipDigest(refs)
		if count != int64(size) || digest != wantDigest {
			t.Fatalf("rows=%d digest/count = %s/%d, want %s/%d", size, digest, count, wantDigest, size)
		}
		completed, err := persistence.CompleteSelection(ctx, selectionID, writerToken, digest, count, bytes, time.Now().UTC())
		if err != nil {
			t.Fatalf("rows=%d complete: %v", size, err)
		}
		if !completed.Complete || completed.MemberCount != int64(size) || completed.MembershipDigest != wantDigest {
			t.Fatalf("rows=%d completed header = %#v", size, completed)
		}
		t.Logf("selection scale rows=%d batches=%d elapsed=%s", size, batches, time.Since(started).Round(time.Millisecond))
	}
}
