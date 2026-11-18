package arango

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

type migrationHistoryOrderClient struct {
	ownerRaw       []byte
	historyRaw     []byte
	updatedOwner   explorer.Explorer
	insertAttempts int
	ownerUpdates   int
}

func (c *migrationHistoryOrderClient) WithTransaction(ctx context.Context, _ store.TransactionCollections, fn store.TransactionFunc) error {
	beforeOwner := append([]byte(nil), c.ownerRaw...)
	beforeHistory := append([]byte(nil), c.historyRaw...)
	err := fn(ctx, c)
	if err != nil {
		c.ownerRaw = beforeOwner
		c.historyRaw = beforeHistory
	}
	return err
}

func (c *migrationHistoryOrderClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit store.RowVisitor) error {
	switch {
	case strings.Contains(query, "UPDATE d WITH MERGE(base, @doc"):
		c.ownerUpdates++
		return visit(migrationHistoryOrderEnvelope(c.updatedOwner, explorerKey(c.updatedOwner.Project, c.updatedOwner.ExplorerID), "owner-rev-after"))
	case strings.Contains(query, `INSERT @doc INTO @@c OPTIONS {overwriteMode:"ignore"} RETURN NEW`):
		c.insertAttempts++
		if c.historyRaw == nil {
			doc, ok := binds["doc"].(map[string]any)
			if !ok {
				return fmt.Errorf("insert bind has type %T", binds["doc"])
			}
			encoded, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			c.historyRaw = encoded
		}
		return nil
	case strings.Contains(query, `FILTER d._key == @key RETURN {migrationJSON:JSON_STRINGIFY(d)}`) && binds["@c"] == DraftRevisionsCollection:
		if c.historyRaw == nil {
			return nil
		}
		return visit(map[string]any{"migrationJSON": string(c.historyRaw)})
	case strings.Contains(query, `d._rev == @expectedRevision`) && strings.Contains(query, `RETURN {migrationJSON:JSON_STRINGIFY(d)}`):
		return visit(map[string]any{"migrationJSON": string(c.ownerRaw)})
	default:
		return fmt.Errorf("unexpected QueryRows in migration saveDraft test: %s", query)
	}
}

func migrationHistoryOrderEnvelope(value any, key, revision string) map[string]any {
	raw, _ := json.Marshal(value)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	fields["_key"], _ = json.Marshal(key)
	fields["_rev"], _ = json.Marshal(revision)
	encoded, _ := json.Marshal(fields)
	return map[string]any{"migrationJSON": string(encoded)}
}

func reviewMigrationOwnerRaw(t *testing.T, value explorer.Explorer) []byte {
	t.Helper()
	row := migrationHistoryOrderEnvelope(value, explorerKey(value.Project, value.ExplorerID), "owner-rev")
	encoded, ok := row["migrationJSON"].(string)
	if !ok {
		t.Fatalf("migration envelope type=%T", row["migrationJSON"])
	}
	return []byte(encoded)
}

func migrationHistoryOrderRaw(t *testing.T, value explorer.DraftRevision, reorderObjects bool) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if reorderObjects {
		from := []byte(`{"workspace":{"document":{"first":1,"second":2}},"array":[1,2],"integer":9007199254740993}`)
		to := []byte(`{"workspace":{"document":{"second":2,"first":1}},"array":[1,2],"integer":9007199254740993}`)
		raw = bytes.Replace(raw, from, to, 1)
		if bytes.Contains(raw, from) || !bytes.Contains(raw, to) {
			t.Fatalf("fixture raw order mutation failed: %s", raw)
		}
	}
	return raw
}

func TestSaveDraftMigrationReusesEquivalentImmutableHistoryWithReorderedWorkspaceObjects(t *testing.T) {
	owner := explorer.Explorer{Project: "project-order", ExplorerID: "patients", Title: "Before", ManagementMode: explorer.ManagementInteractive,
		DraftConfig:  json.RawMessage(`{"workspace":{"document":{"first":1,"second":2}},"array":[1,2],"integer":9007199254740993}`),
		DraftVersion: 7, DraftDigest: "sha256:prior", DraftSnapshotToken: "snapshot-prior", DraftSourceGeneration: "generation-prior",
		DraftAuthorizationScopeDigest: "scope-prior", UpdatedBy: "fixture", UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	prior := draftRevisionFromExplorer(owner)
	prior.ID = draftRevisionKey(owner.Project, owner.ExplorerID, owner.DraftVersion)
	storedHistory := migrationHistoryOrderRaw(t, prior, true)
	candidate := owner
	candidate.Title = "After"
	candidate.DraftVersion = 999
	candidate.DraftDigest = "sha256:next"
	updated := candidate
	updated.DraftVersion = owner.DraftVersion + 1
	updated.PreviousDraftRevisionID = prior.ID
	client := &migrationHistoryOrderClient{ownerRaw: reviewMigrationOwnerRaw(t, owner), historyRaw: storedHistory, updatedOwner: updated}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	got, err := adapter.saveDraft(context.Background(), candidate, owner.DraftVersion, []string{owner.DraftDigest}, &draftMigrationWrite{
		revision:      "owner-rev",
		beforeHistory: func(context.Context, store.RowQueryer, explorer.Explorer, explorer.DraftRevision) error { return nil },
	})
	if err != nil {
		t.Fatalf("saveDraft rejected semantically equal immutable history: %v", err)
	}
	if got == nil || got.DraftVersion != owner.DraftVersion+1 || got.PreviousDraftRevisionID != prior.ID {
		t.Fatalf("saveDraft result=%#v, want incremented owner with prior revision link", got)
	}
	if client.insertAttempts != 1 || client.ownerUpdates != 1 {
		t.Fatalf("insert attempts=%d owner updates=%d, want ignored insert once and owner update once", client.insertAttempts, client.ownerUpdates)
	}
	if !bytes.Equal(client.historyRaw, storedHistory) {
		t.Fatalf("existing immutable history bytes changed:\nbefore=%s\nafter=%s", storedHistory, client.historyRaw)
	}
}

func TestSaveDraftMigrationRejectsSemanticImmutableHistoryChanges(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*explorer.DraftRevision)
	}{
		{"array order", func(r *explorer.DraftRevision) {
			r.DraftConfig = json.RawMessage(`{"workspace":{"document":{"first":1,"second":2}},"array":[2,1],"integer":9007199254740993}`)
		}},
		{"large integer", func(r *explorer.DraftRevision) {
			r.DraftConfig = json.RawMessage(`{"workspace":{"document":{"first":1,"second":2}},"array":[1,2],"integer":9007199254740992}`)
		}},
		{"title", func(r *explorer.DraftRevision) { r.Title = "different" }},
		{"timestamp", func(r *explorer.DraftRevision) { r.UpdatedAt = r.UpdatedAt.Add(time.Second) }},
		{"source generation", func(r *explorer.DraftRevision) { r.SourceGeneration = "different-generation" }},
		{"authorization scope", func(r *explorer.DraftRevision) { r.AuthorizationScopeDigest = "different-scope" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			owner := explorer.Explorer{Project: "project-order", ExplorerID: "patients", Title: "Before", ManagementMode: explorer.ManagementInteractive,
				DraftConfig:  json.RawMessage(`{"workspace":{"document":{"first":1,"second":2}},"array":[1,2],"integer":9007199254740993}`),
				DraftVersion: 7, DraftDigest: "sha256:prior", DraftSnapshotToken: "snapshot-prior", DraftSourceGeneration: "generation-prior",
				DraftAuthorizationScopeDigest: "scope-prior", UpdatedBy: "fixture", UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
			prior := draftRevisionFromExplorer(owner)
			prior.ID = draftRevisionKey(owner.Project, owner.ExplorerID, owner.DraftVersion)
			stored := prior
			tc.mutate(&stored)
			storedHistory := migrationHistoryOrderRaw(t, stored, false)
			candidate := owner
			candidate.Title = "After"
			updated := candidate
			updated.DraftVersion = owner.DraftVersion + 1
			client := &migrationHistoryOrderClient{ownerRaw: reviewMigrationOwnerRaw(t, owner), historyRaw: storedHistory, updatedOwner: updated}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			_, err = adapter.saveDraft(context.Background(), candidate, owner.DraftVersion, []string{owner.DraftDigest}, &draftMigrationWrite{
				revision:      "owner-rev",
				beforeHistory: func(context.Context, store.RowQueryer, explorer.Explorer, explorer.DraftRevision) error { return nil },
			})
			if err == nil || !strings.Contains(err.Error(), "immutable prior Explorer draft revision collision") {
				t.Fatalf("saveDraft error=%v, want immutable history collision", err)
			}
			if client.ownerUpdates != 0 || !bytes.Equal(client.historyRaw, storedHistory) {
				t.Fatalf("semantic collision updated owner=%d times or changed immutable history", client.ownerUpdates)
			}
		})
	}
}
