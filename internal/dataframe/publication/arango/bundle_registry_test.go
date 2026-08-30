package arango

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

type publishCaptureClient struct {
	testing *testing.T
}

func (c publishCaptureClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c publishCaptureClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	for _, fragment := range []string{"LET lease = DOCUMENT(@@leases, @leaseKey)", "lease.ownerId == @owner", "lease.expiresAt >= @now"} {
		if !strings.Contains(query, fragment) {
			c.testing.Fatalf("publication query lacks lease guard %q", fragment)
		}
	}
	if bindVars["@leases"] != BundleLeasesCollection || bindVars["leaseKey"] != "bundle-key" || bindVars["owner"] != "publisher-a" {
		c.testing.Fatalf("publication lease bindings = %#v", bindVars)
	}
	return visit(map[string]any{"updated": true})
}

func TestPointerDocumentKeyAcceptsLogicalNamesWithNULSeparators(t *testing.T) {
	name := "HTAN_INT-BForePC\x00\x00aced-meta-default"
	first := pointerDocumentKey(name)
	second := pointerDocumentKey(name)
	if first != second {
		t.Fatalf("pointer key is not deterministic: %q != %q", first, second)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(first) {
		t.Fatalf("pointer key is not an Arango-safe SHA256: %q", first)
	}
}

func TestPublishExecutionRequiresCurrentLeaseOwner(t *testing.T) {
	registry, err := New(publishCaptureClient{testing: t})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	execution := publication.BundleExecution{
		ID: "execution-a", Key: "bundle-key", OwnerID: "publisher-a",
		State: publication.BundlePublished, UpdatedAt: now,
	}
	if err := registry.PublishExecution(context.Background(), "project\x00generation\x00recipe", "", execution); err != nil {
		t.Fatal(err)
	}
}
