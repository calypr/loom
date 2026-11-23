package arango

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

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
		header := explorerselection.SelectionRevision{
			ID: selectionID, Project: project, Generation: generation, ResourceType: resourceType,
			Rule:        explorerselection.SelectionRule{Kind: explorerselection.SelectionRuleExplicit},
			Source:      explorerselection.SelectionSource{Kind: explorerselection.SelectionSourceExplicit},
			ScopeDigest: "scale-scope", RuleDigest: "scale-rule-" + fmt.Sprint(size),
			IdempotencyKey: "scale-" + runID + "-" + fmt.Sprint(size), CreatedAt: time.Now().UTC(),
		}
		started := time.Now()
		if _, err := persistence.BeginSelection(ctx, header, writerToken); err != nil {
			t.Fatalf("rows=%d begin: %v", size, err)
		}
		refs := make([]explorerselection.SelectionMember, size)
		for i := range refs {
			refs[i] = explorerselection.SelectionMember{Ref: explorerselection.ResourceRef{
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
		wantDigest := explorerselection.MembershipDigest(refs)
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
