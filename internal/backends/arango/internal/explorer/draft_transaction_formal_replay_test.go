package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	store "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/explorer"
)

const draftTransactionRevisionID = "draft_revision_5a5bd747c995d2a79aba154d30be769c0fd16d0329b5eb8ed60efa0f073e02d8"
const draftTransactionExplorerKey = "explorer_197f03a05310264cc602a25263fcec44e53b05736b3c9c4fddf10fe5c1aee09c"

func draftTransactionClone(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for name, item := range value {
		result[name] = item
	}
	return result
}

type draftTransactionOutcomes struct {
	Begin   string `json:"begin"`
	Read    string `json:"read"`
	Archive string `json:"archive"`
	Update  string `json:"update"`
	Commit  string `json:"commit"`
}
type draftTransactionCase struct {
	ID       string                   `json:"id"`
	Outcomes draftTransactionOutcomes `json:"outcomes"`
}
type draftTransactionObservation struct {
	ID              string   `json:"id"`
	Queries         []string `json:"queries"`
	Result          string   `json:"result"`
	BackendConflict bool     `json:"backendConflict"`
	History         bool     `json:"history"`
	Head            bool     `json:"head"`
	ExactHistory    bool     `json:"exactHistory"`
	ExactHead       bool     `json:"exactHead"`
	StablePublished bool     `json:"stablePublished"`
	ExactReturned   bool     `json:"exactReturned"`
	CallbackError   bool     `json:"callbackError"`
	Transactions    int      `json:"transactions"`
}
type draftTransactionPrimitive struct {
	t                *testing.T
	input            draftTransactionCase
	old              explorer.Explorer
	proposed         explorer.Explorer
	persistedHead    map[string]any
	persistedHistory map[string]any
	stagedHead       map[string]any
	stagedHistory    map[string]any
	queries          []string
	callbackError    bool
	transactions     int
}

func draftTransactionError(outcome string) error {
	if outcome == "conflict" {
		return shared.ArangoError{HasError: true, Code: 409, ErrorNum: shared.ErrArangoConflict, ErrorMessage: "controlled write conflict"}
	}
	return fmt.Errorf("controlled transaction %s", outcome)
}
func (c *draftTransactionPrimitive) WithTransaction(ctx context.Context, collections store.TransactionCollections, fn store.TransactionFunc) error {
	c.transactions++
	if !reflect.DeepEqual(collections.Write, []string{ExplorersCollection, DraftRevisionsCollection}) {
		c.t.Fatalf("wrong transaction write set: %#v", collections)
	}
	if c.input.Outcomes.Begin != "ok" {
		return draftTransactionError(c.input.Outcomes.Begin)
	}
	err := fn(ctx, c)
	c.callbackError = err != nil
	if err != nil {
		c.stagedHead = nil
		c.stagedHistory = nil
		return err
	}
	switch c.input.Outcomes.Commit {
	case "ok", "unknown", "none":
		if c.stagedHead != nil {
			c.persistedHead = c.stagedHead
		}
		if c.stagedHistory != nil {
			c.persistedHistory = c.stagedHistory
		}
		if c.input.Outcomes.Commit == "unknown" {
			return draftTransactionError("commit acknowledgement lost")
		}
		return nil
	case "conflict", "reject":
		return draftTransactionError(c.input.Outcomes.Commit)
	default:
		c.t.Fatalf("unexpected commit reached: %q", c.input.Outcomes.Commit)
		return nil
	}
}
func (c *draftTransactionPrimitive) QueryRows(_ context.Context, query string, batch int, binds map[string]any, visit store.RowVisitor) error {
	stage := "read"
	if strings.Contains(query, "INSERT @doc") {
		stage = "archive"
	} else if strings.Contains(query, "UPDATE d WITH MERGE") {
		stage = "update"
	}
	expected := []string{"read", "archive", "update"}
	index := len(c.queries)
	if index >= len(expected) || stage != expected[index] || batch != 1 {
		c.t.Fatalf("unexpected query sequence: %v then %s", c.queries, query)
	}
	c.queries = append(c.queries, stage)
	outcome := map[string]string{"read": c.input.Outcomes.Read, "archive": c.input.Outcomes.Archive, "update": c.input.Outcomes.Update}[stage]
	if stage != "archive" {
		if binds["@c"] != ExplorersCollection || binds["key"] != draftTransactionExplorerKey || binds["project"] != "project-formal" || binds["explorerId"] != "draft-formal" || binds["expected"] != int64(1) || binds["expectedDigest"] != "digest-old" {
			c.t.Fatalf("changed CAS binds: %#v", binds)
		}
		for _, guard := range []string{"d._key == @key", "d.project == @project", "d.explorerId == @explorerId", "d.draftVersion == @expected", "d.draftDigest == @expectedDigest"} {
			if !strings.Contains(query, guard) {
				c.t.Fatalf("missing query guard %s", guard)
			}
		}
	}
	if outcome == "conflict" || outcome == "other" {
		return draftTransactionError(outcome)
	}
	if outcome == "missing" {
		return nil
	}
	if outcome != "ok" {
		c.t.Fatalf("unexpected query reached: %s=%s", stage, outcome)
	}
	switch stage {
	case "read":
		return visit(draftTransactionClone(c.persistedHead))
	case "archive":
		if binds["@c"] != DraftRevisionsCollection {
			c.t.Fatal("wrong history collection")
		}
		doc, ok := binds["doc"].(map[string]any)
		if !ok {
			c.t.Fatal("missing history document")
		}
		c.stagedHistory = draftTransactionClone(doc)
		return visit(draftTransactionClone(doc))
	case "update":
		if binds["nextVersion"] != int64(2) || binds["revisionId"] != draftTransactionRevisionID {
			c.t.Fatalf("changed head identity: %#v", binds)
		}
		next := draftTransactionClone(c.persistedHead)
		if binds["clearDraftConfig"] == true {
			delete(next, "draftConfig")
		}
		patch, ok := binds["doc"].(map[string]any)
		if !ok {
			c.t.Fatal("missing patch")
		}
		for k, v := range patch {
			next[k] = v
		}
		next["draftVersion"] = int64(2)
		next["previousDraftRevisionId"] = binds["revisionId"]
		c.stagedHead = next
		return visit(draftTransactionClone(next))
	}
	return nil
}
func draftTransactionFixture() explorer.Explorer {
	return explorer.Explorer{Project: "project-formal", ExplorerID: "draft-formal", Title: "Old title", DraftVersion: 1, DraftDigest: "digest-old", DraftConfig: json.RawMessage(`{"documents":[{"outputId":"old-output"}]}`), DraftSnapshotToken: "snapshot-old", DraftSourceGeneration: "generation-old", DraftAuthorizationScopeDigest: "scope-old", ActiveRevisionID: "published-stable", UpdatedBy: "old-author", UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}
func draftTransactionJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func runDraftTransactionCase(t *testing.T, input draftTransactionCase) draftTransactionObservation {
	t.Helper()
	old := draftTransactionFixture()
	oldDoc, err := document(old, draftTransactionExplorerKey)
	if err != nil {
		t.Fatal(err)
	}
	proposed := old
	proposed.Title = "New title"
	proposed.DraftVersion = 999
	proposed.DraftDigest = "digest-new"
	proposed.DraftConfig = json.RawMessage(`{"documents":[{"outputId":"new-output"}]}`)
	proposed.DraftSnapshotToken = "snapshot-new"
	proposed.DraftSourceGeneration = "generation-new"
	proposed.DraftAuthorizationScopeDigest = "scope-new"
	proposed.ActiveRevisionID = "must-not-publish"
	proposed.UpdatedBy = "new-author"
	proposed.LastAuthoringCommandID = "command-new"
	proposed.LastAuthoringCommandDigest = "command-digest-new"
	proposed.LastAuthoringCommandResults = json.RawMessage(`[{"commandId":"command-new"}]`)
	client := &draftTransactionPrimitive{t: t, input: input, old: old, proposed: proposed, persistedHead: oldDoc, queries: []string{}}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	out, err := adapter.SaveDraft(context.Background(), proposed, 1, "digest-old")
	result := "other-error"
	if err == nil {
		result = "success"
	} else if errors.Is(err, explorer.ErrDraftConflict) {
		result = "draft-conflict"
	}
	var backend shared.ArangoError
	hasBackend := errors.As(err, &backend) && backend.ErrorNum == shared.ErrArangoConflict
	observation := draftTransactionObservation{ID: input.ID, Queries: client.queries, Result: result, BackendConflict: hasBackend, History: client.persistedHistory != nil, Head: draftTransactionJSON(client.persistedHead) != draftTransactionJSON(oldDoc), ExactHistory: true, ExactHead: true, StablePublished: client.persistedHead["activeRevisionId"] == "published-stable", ExactReturned: out == nil && err != nil, CallbackError: client.callbackError, Transactions: client.transactions}
	if client.persistedHistory != nil {
		var got explorer.DraftRevision
		raw, _ := json.Marshal(client.persistedHistory)
		if json.Unmarshal(raw, &got) != nil {
			t.Fatal("invalid captured history")
		}
		want := explorer.DraftRevision{ID: draftTransactionRevisionID, Project: old.Project, ExplorerID: old.ExplorerID, DraftVersion: 1, DraftDigest: "digest-old", DraftConfig: old.DraftConfig, Title: "Old title", SnapshotToken: "snapshot-old", SourceGeneration: "generation-old", AuthorizationScopeDigest: "scope-old", UpdatedBy: "old-author", UpdatedAt: old.UpdatedAt}
		observation.ExactHistory = reflect.DeepEqual(got, want) && client.persistedHistory["_key"] == want.ID
	}
	if observation.Head {
		var got explorer.Explorer
		raw, _ := json.Marshal(client.persistedHead)
		if json.Unmarshal(raw, &got) != nil {
			t.Fatal("invalid captured head")
		}
		want := proposed
		want.DraftVersion = 2
		want.ActiveRevisionID = "published-stable"
		want.PreviousDraftRevisionID = draftTransactionRevisionID
		want.UpdatedAt = got.UpdatedAt
		observation.ExactHead = draftTransactionJSON(got) == draftTransactionJSON(want) && !got.UpdatedAt.IsZero()
	}
	if out != nil {
		observation.ExactReturned = err == nil && draftTransactionJSON(out) == draftTransactionJSON(client.persistedHeadWithoutKey())
	}
	return observation
}
func (c *draftTransactionPrimitive) persistedHeadWithoutKey() explorer.Explorer {
	var value explorer.Explorer
	raw, _ := json.Marshal(c.persistedHead)
	_ = json.Unmarshal(raw, &value)
	return value
}
func TestDraftTransactionModelReplay(t *testing.T) {
	inputPath, outputPath := os.Getenv("LOOM_FORMAL_DRAFT_TRANSACTION_INPUT"), os.Getenv("LOOM_FORMAL_DRAFT_TRANSACTION_OUTPUT")
	if inputPath == "" && outputPath == "" {
		for _, input := range []draftTransactionCase{{ID: "success", Outcomes: draftTransactionOutcomes{"ok", "ok", "ok", "ok", "ok"}}, {ID: "rollback", Outcomes: draftTransactionOutcomes{"ok", "ok", "ok", "other", "none"}}, {ID: "unknown", Outcomes: draftTransactionOutcomes{"ok", "ok", "ok", "ok", "unknown"}}} {
			got := runDraftTransactionCase(t, input)
			wantResult := map[string]string{"success": "success", "rollback": "other-error", "unknown": "other-error"}[input.ID]
			wantPublished := input.ID != "rollback"
			if got.Result != wantResult || got.History != wantPublished || got.Head != wantPublished || !got.ExactHistory || !got.ExactHead || !got.StablePublished || !got.ExactReturned {
				t.Fatalf("invalid local smoke: %+v", got)
			}
		}
		return
	}
	if inputPath == "" || outputPath == "" {
		t.Fatal("paired formal input/output required")
	}
	file, err := os.Open(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var input struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Cases         []draftTransactionCase `json:"cases"`
	}
	if err = decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		t.Fatal("trailing formal input")
	}
	if input.SchemaVersion != 1 || len(input.Cases) == 0 {
		t.Fatal("invalid formal input")
	}
	seen := map[string]bool{}
	results := []draftTransactionObservation{}
	for _, row := range input.Cases {
		if row.ID == "" || seen[row.ID] {
			t.Fatal("duplicate or missing case id")
		}
		seen[row.ID] = true
		results = append(results, runDraftTransactionCase(t, row))
	}
	raw, err := json.Marshal(struct {
		SchemaVersion int                           `json:"schemaVersion"`
		Results       []draftTransactionObservation `json:"results"`
	}{1, results})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(outputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
