package arango

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	storepkg "github.com/calypr/loom/internal/store/arango"
)

type draftHistoryClient struct {
	owner       map[string]any
	revisions   map[string]map[string]any
	calls       []queryCall
	transaction storepkg.TransactionCollections
	loseCAS     bool
}

func (c *draftHistoryClient) WithTransaction(ctx context.Context, collections storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	previousOwner := cloneDocument(c.owner)
	previousRevisions := make(map[string]map[string]any, len(c.revisions))
	for id, revision := range c.revisions {
		previousRevisions[id] = cloneDocument(revision)
	}
	c.transaction = collections
	err := fn(ctx, c)
	if err != nil {
		c.owner = previousOwner
		c.revisions = previousRevisions
	}
	return err
}

func (c *draftHistoryClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	c.calls = append(c.calls, queryCall{query: query, binds: binds})
	collection, _ := binds["@c"].(string)
	switch {
	case strings.HasPrefix(strings.TrimSpace(query), "INSERT") && collection == DraftRevisionsCollection:
		doc := cloneDocument(binds["doc"].(map[string]any))
		id := doc["_key"].(string)
		if c.revisions == nil {
			c.revisions = map[string]map[string]any{}
		}
		if c.revisions[id] != nil {
			return errors.New("duplicate immutable draft revision")
		}
		c.revisions[id] = doc
		return visit(cloneDocument(doc))
	case strings.Contains(query, "d.id == @revisionId") && collection == DraftRevisionsCollection:
		id, _ := binds["key"].(string)
		revision := c.revisions[id]
		if revision == nil || revision["id"] != binds["revisionId"] || revision["project"] != binds["project"] || revision["explorerId"] != binds["explorerId"] {
			return nil
		}
		return visit(cloneDocument(revision))
	case strings.Contains(query, "UPDATE d WITH MERGE(base, @doc"):
		if c.loseCAS || !draftOwnerMatches(c.owner, binds) {
			return nil
		}
		updated := cloneDocument(c.owner)
		if binds["clearDraftConfig"] == true {
			delete(updated, "draftConfig")
		}
		for key, value := range binds["doc"].(map[string]any) {
			updated[key] = value
		}
		updated["draftVersion"] = binds["nextVersion"]
		updated["previousDraftRevisionId"] = binds["revisionId"]
		c.owner = updated
		return visit(cloneDocument(updated))
	case strings.Contains(query, "RETURN d") && collection == ExplorersCollection && binds["expected"] != nil:
		if !draftOwnerMatches(c.owner, binds) {
			return nil
		}
		return visit(cloneDocument(c.owner))
	default:
		return nil
	}
}

func draftOwnerMatches(owner map[string]any, binds map[string]any) bool {
	if owner == nil || owner["_key"] != binds["key"] || owner["project"] != binds["project"] || owner["explorerId"] != binds["explorerId"] {
		return false
	}
	expected, ok := binds["expected"].(int64)
	if !ok {
		return false
	}
	version, _ := owner["draftVersion"].(float64)
	if int64(version) != expected {
		return false
	}
	expectedDigest, _ := binds["expectedDigest"].(string)
	return expectedDigest == "" || owner["draftDigest"] == expectedDigest
}

func cloneDocument(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	cloned := make(map[string]any, len(value))
	for key, item := range value {
		cloned[key] = item
	}
	return cloned
}

func TestSaveDraftPersistsExactPreimageAtomicallyAndPreservesActiveRevision(t *testing.T) {
	previous := explorer.Explorer{
		Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig:  json.RawMessage(`{"documents":[{"outputId":"patients","construction":{"steps":[{"id":"pivot","outputs":[{"id":"col_age","name":"age"}]}]}}]}`),
		DraftVersion: 4, DraftDigest: "sha256:old-draft", DraftSnapshotToken: "snapshot-old",
		DraftSourceGeneration: "generation-old", DraftAuthorizationScopeDigest: "scope-old",
		ActiveRevisionID: "published-revision-9", UpdatedBy: "alice", UpdatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
	}
	owner, err := document(previous, explorerKey(previous.Project, previous.ExplorerID))
	if err != nil {
		t.Fatal(err)
	}
	client := &draftHistoryClient{owner: owner}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	updated := previous
	updated.DraftConfig = json.RawMessage(`{"documents":[{"outputId":"patients","construction":{"steps":[{"id":"pivot","outputs":[{"id":"col_age","name":"age"}]},{"id":"derive","outputs":[{"id":"col_ratio","name":"ratio"}]}]}}]}`)
	updated.DraftVersion = 999 // The adapter owns the monotonic CAS version.
	updated.DraftDigest = "sha256:new-draft"
	updated.DraftSnapshotToken = "snapshot-new"
	updated.DraftSourceGeneration = "generation-new"
	updated.DraftAuthorizationScopeDigest = "scope-new"
	updated.ActiveRevisionID = "stale-revision-must-not-overwrite"
	updated.UpdatedBy = "bob"
	stored, err := adapter.SaveDraft(context.Background(), updated, previous.DraftVersion, previous.DraftDigest)
	if err != nil {
		t.Fatal(err)
	}
	if stored.DraftVersion != previous.DraftVersion+1 {
		t.Fatalf("draftVersion=%d, want %d", stored.DraftVersion, previous.DraftVersion+1)
	}
	if stored.ActiveRevisionID != previous.ActiveRevisionID {
		t.Fatalf("SaveDraft changed active published revision to %q", stored.ActiveRevisionID)
	}
	if stored.PreviousDraftRevisionID == "" {
		t.Fatal("SaveDraft did not return the prior revision ID")
	}
	if len(client.revisions) != 1 {
		t.Fatalf("persisted draft revisions=%d, want 1", len(client.revisions))
	}
	if !reflect.DeepEqual(client.transaction.Write, []string{ExplorersCollection, DraftRevisionsCollection}) {
		t.Fatalf("transaction write set=%v", client.transaction.Write)
	}
	var snapshot *explorer.DraftRevision
	for _, id := range []string{stored.PreviousDraftRevisionID} {
		snapshot, err = adapter.GetDraftRevision(context.Background(), previous.Project, previous.ExplorerID, id)
		if err != nil {
			t.Fatal(err)
		}
	}
	if snapshot.DraftVersion != previous.DraftVersion || snapshot.DraftDigest != previous.DraftDigest || snapshot.Title != previous.Title {
		t.Fatalf("snapshot identity/state = %#v", snapshot)
	}
	if !json.Valid(snapshot.DraftConfig) || !strings.Contains(string(snapshot.DraftConfig), `"id":"col_age"`) || strings.Contains(string(snapshot.DraftConfig), `"id":"col_ratio"`) {
		t.Fatalf("snapshot did not preserve exact prior construction and output IDs: %s", snapshot.DraftConfig)
	}
	if snapshot.SnapshotToken != "snapshot-old" || snapshot.SourceGeneration != "generation-old" || snapshot.AuthorizationScopeDigest != "scope-old" {
		t.Fatalf("snapshot source context = %#v", snapshot)
	}
	if !strings.Contains(client.calls[0].query, "@expectedDigest") || !strings.Contains(client.calls[2].query, "previousDraftRevisionId: @revisionId") {
		t.Fatalf("history write/CAS query sequence is incomplete: %#v", client.calls)
	}
}

func TestSaveDraftCASConflictDoesNotPersistHistory(t *testing.T) {
	previous := explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftVersion: 4, DraftDigest: "sha256:current"}
	owner, err := document(previous, explorerKey(previous.Project, previous.ExplorerID))
	if err != nil {
		t.Fatal(err)
	}
	client := &draftHistoryClient{owner: owner}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.SaveDraft(context.Background(), previous, 3, "sha256:stale")
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("SaveDraft error=%v, want ErrDraftConflict", err)
	}
	if len(client.revisions) != 0 {
		t.Fatalf("CAS conflict persisted %d history snapshots", len(client.revisions))
	}
}

func TestSaveDraftRollsBackHistoryIfOwnerCASFailsAfterSnapshotInsert(t *testing.T) {
	previous := explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftVersion: 4, DraftDigest: "sha256:current"}
	owner, err := document(previous, explorerKey(previous.Project, previous.ExplorerID))
	if err != nil {
		t.Fatal(err)
	}
	client := &draftHistoryClient{owner: owner, loseCAS: true}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.SaveDraft(context.Background(), previous, previous.DraftVersion, previous.DraftDigest)
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("SaveDraft error=%v, want ErrDraftConflict", err)
	}
	if len(client.revisions) != 0 {
		t.Fatalf("failed owner CAS left %d history snapshots", len(client.revisions))
	}
}

func TestGetDraftRevisionScopesOpaqueIDsToExplorer(t *testing.T) {
	revision := explorer.DraftRevision{
		ID: "draft_revision_one", Project: "project-a", ExplorerID: "patients", DraftVersion: 3,
		DraftDigest: "sha256:draft", DraftConfig: json.RawMessage(`{"documents":[{"outputId":"patients"}]}`),
	}
	doc, err := document(revision, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	client := &draftHistoryClient{revisions: map[string]map[string]any{revision.ID: doc}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.GetDraftRevision(context.Background(), "project-b", revision.ExplorerID, revision.ID); !errors.Is(err, explorer.ErrNotFound) {
		t.Fatalf("cross-project lookup error=%v, want ErrNotFound", err)
	}
	got, err := adapter.GetDraftRevision(context.Background(), revision.Project, revision.ExplorerID, revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != revision.ID || !json.Valid(got.DraftConfig) || !strings.Contains(string(got.DraftConfig), `"outputId":"patients"`) {
		t.Fatalf("draft revision lookup = %#v", got)
	}
	query := client.calls[len(client.calls)-1].query
	for _, predicate := range []string{"d.project == @project", "d.explorerId == @explorerId", "d.id == @revisionId"} {
		if !strings.Contains(query, predicate) {
			t.Fatalf("revision lookup missing scope %q: %s", predicate, query)
		}
	}
}
