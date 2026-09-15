package arango

import (
	"context"
	"encoding/json"
	"errors"
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

type saveExecutionClient struct {
	allowSave bool
}

func (c saveExecutionClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c saveExecutionClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	for _, fragment := range []string{"LET lease = DOCUMENT(@@leases, @leaseKey)", "lease.ownerId == @owner", "lease.expiresAt >= @now", "UPSERT {_key: @executionKey}"} {
		if !strings.Contains(query, fragment) {
			return errors.New("fenced execution query is missing " + fragment)
		}
	}
	if bindVars["leaseKey"] != "bundle-key" || bindVars["owner"] != "publisher-a" || bindVars["executionKey"] != "execution-a" {
		return errors.New("fenced execution bindings are incorrect")
	}
	if c.allowSave {
		return visit(map[string]any{"saved": true})
	}
	return nil
}

func TestSaveExecutionRejectsCheckpointAfterLeaseTakeover(t *testing.T) {
	registry, err := New(saveExecutionClient{allowSave: true})
	if err != nil {
		t.Fatal(err)
	}
	execution := publication.BundleExecution{ID: "execution-a", Key: "bundle-key", OwnerID: "publisher-a"}
	if err := registry.SaveExecution(context.Background(), execution, execution.OwnerID); err != nil {
		t.Fatal(err)
	}

	registry, err = New(saveExecutionClient{allowSave: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.SaveExecution(context.Background(), execution, execution.OwnerID); !errors.Is(err, publication.ErrBundleLeaseLost) {
		t.Fatalf("SaveExecution() error = %v, want lease loss", err)
	}
}

type legacyExecutionClient struct {
	savedOwner string
}

func (c *legacyExecutionClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c *legacyExecutionClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	if strings.Contains(query, "UPSERT {_key: @executionKey}") {
		if bindVars["owner"] != "publisher-a" {
			return errors.New("legacy recovery did not use the active lease owner")
		}
		c.savedOwner = bindVars["owner"].(string)
		return visit(map[string]any{"saved": true})
	}
	if !strings.Contains(query, "FILTER doc._key == @key") {
		return errors.New("unexpected legacy execution query")
	}
	return visit(map[string]any{
		"id":        "legacy-execution",
		"key":       "legacy-bundle",
		"name":      "legacy-recipe",
		"state":     "RUNNING",
		"updatedAt": time.Now().UTC(),
	})
}

func TestLegacyExecutionWithoutOwnerCanBeReadAndFencedForRecovery(t *testing.T) {
	client := &legacyExecutionClient{}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	execution, err := registry.GetExecution(context.Background(), "legacy-execution")
	if err != nil {
		t.Fatal(err)
	}
	if execution.OwnerID != "" {
		t.Fatalf("legacy execution owner = %q, want empty owner", execution.OwnerID)
	}
	if err := registry.SaveExecution(context.Background(), execution, "publisher-a"); err != nil {
		t.Fatal(err)
	}
	if client.savedOwner != "publisher-a" {
		t.Fatalf("recovery fence owner = %q, want publisher-a", client.savedOwner)
	}
}

type executionPageClient struct {
	rows  [][]map[string]any
	binds []map[string]interface{}
}

func (c *executionPageClient) InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error {
	return nil
}

func (c *executionPageClient) QueryRows(_ context.Context, query string, _ int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	if !strings.Contains(query, "SORT doc.updatedAt ASC, doc._key ASC") || !strings.Contains(query, "LIMIT @limit") {
		return errors.New("execution page query is not stably ordered and bounded")
	}
	copied := make(map[string]interface{}, len(bindVars))
	for key, value := range bindVars {
		copied[key] = value
	}
	c.binds = append(c.binds, copied)
	page := c.rows[0]
	c.rows = c.rows[1:]
	for _, row := range page {
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func TestVisitExecutionPagesUsesStableContinuation(t *testing.T) {
	firstUpdated := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	secondUpdated := firstUpdated.Add(time.Second)
	thirdUpdated := secondUpdated.Add(time.Second)
	client := &executionPageClient{rows: [][]map[string]any{
		{
			{"_key": "execution-a", "id": "execution-a", "key": "bundle-a", "state": "RUNNING", "updatedAt": firstUpdated},
			{"_key": "execution-b", "id": "execution-b", "key": "bundle-b", "state": "RUNNING", "updatedAt": secondUpdated},
		},
		{{"_key": "execution-c", "id": "execution-c", "key": "bundle-c", "state": "RUNNING", "updatedAt": thirdUpdated}},
	}}
	registry, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	var executions []publication.BundleExecution
	if err := registry.VisitExecutionPages(context.Background(), publication.BundleRunning, thirdUpdated.Add(time.Minute), 2, func(page []publication.BundleExecution) error {
		if len(page) > 2 {
			t.Fatalf("execution page length = %d, want at most 2", len(page))
		}
		executions = append(executions, page...)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(executions) != 3 || executions[0].ID != "execution-a" || executions[1].ID != "execution-b" || executions[2].ID != "execution-c" {
		t.Fatalf("paged executions = %#v", executions)
	}
	if len(client.binds) != 2 {
		t.Fatalf("execution page query count = %d, want 2", len(client.binds))
	}
	if got := client.binds[0]["afterKey"]; got != "" {
		t.Fatalf("first continuation key = %v, want empty", got)
	}
	if got := client.binds[1]["afterKey"]; got != "execution-b" {
		t.Fatalf("second continuation key = %v, want execution-b", got)
	}
}
