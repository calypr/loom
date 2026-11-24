package arango

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

type draftCASClient struct {
	*store.Client
	afterRead  func(context.Context) error
	failUpdate error
}

func (c *draftCASClient) WithTransaction(ctx context.Context, collections store.TransactionCollections, fn store.TransactionFunc) error {
	return c.Client.WithTransaction(ctx, collections, func(ctx context.Context, tx store.RowQueryer) error {
		return fn(ctx, draftCASQueryer{RowQueryer: tx, afterRead: c.afterRead, failUpdate: c.failUpdate})
	})
}

type draftCASQueryer struct {
	store.RowQueryer
	afterRead  func(context.Context) error
	failUpdate error
}

func (q draftCASQueryer) QueryRows(ctx context.Context, query string, batch int, binds map[string]any, visit store.RowVisitor) error {
	if q.failUpdate != nil && strings.Contains(query, "UPDATE d WITH MERGE(base, @doc") {
		return q.failUpdate
	}
	err := q.RowQueryer.QueryRows(ctx, query, batch, binds, visit)
	if err == nil && q.afterRead != nil && strings.Contains(query, "RETURN d") && binds["expected"] != nil && !strings.Contains(query, "UPDATE") {
		return q.afterRead(ctx)
	}
	return err
}

func draftCASDB(t *testing.T) (context.Context, *store.Client, *Store, string) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := "loom_test_draft_cas_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	c, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close(context.Background())
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanup, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
		if err != nil {
			t.Error(err)
			return
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("private database cleanup status=%d", response.StatusCode)
		} else {
			t.Logf("CAS_PRIVATE_DATABASE_REMOVED=%s", name)
		}
	})
	if err := c.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatal(err)
	}
	s, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return ctx, c, s, name
}

func draftCASPayload(t *testing.T, title string) (json.RawMessage, string) {
	t.Helper()
	w := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: title}, Documents: []ws.Document{}, Tabs: []ws.Tab{}}
	raw, err := w.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := w.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return raw, digest
}

func draftCASSeed(t *testing.T, ctx context.Context, s *Store) explorer.Explorer {
	t.Helper()
	raw, digest := draftCASPayload(t, "Initial")
	e := explorer.Explorer{Project: "project-cas", ExplorerID: "patients", Title: "Initial", ManagementMode: viewer.ManagementInteractive, DraftConfig: raw, DraftVersion: 1, DraftDigest: digest, DraftSnapshotToken: "snapshot-one", DraftSourceGeneration: "generation-one", DraftAuthorizationScopeDigest: "scope-one", ActiveRevisionID: "published-stable", UpdatedBy: "initial", UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	if _, err := s.Create(ctx, e); err != nil {
		t.Fatal(err)
	}
	actual, err := s.Get(ctx, e.Project, e.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	return *actual
}

func draftCASHistory(t *testing.T, ctx context.Context, c *store.Client, e explorer.Explorer) []explorer.DraftRevision {
	t.Helper()
	rows := []explorer.DraftRevision{}
	err := c.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project AND d.explorerId == @explorer RETURN d`, 10, map[string]any{"@c": DraftRevisionsCollection, "project": e.Project, "explorer": e.ExplorerID}, func(row map[string]any) error {
		v, err := db.Decode[explorer.DraftRevision](row)
		rows = append(rows, v)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestDraftCASConcurrentWritersAgainstArango(t *testing.T) {
	ctx, c, s, database := draftCASDB(t)
	before := draftCASSeed(t, ctx, s)
	var mu sync.Mutex
	reads := 0
	both := make(chan struct{})
	barrier := func(ctx context.Context) error {
		mu.Lock()
		reads++
		if reads == 2 {
			close(both)
		}
		mu.Unlock()
		select {
		case <-both:
			return nil
		case <-ctx.Done():
			return fmt.Errorf("CAS read barrier: %w", ctx.Err())
		}
	}
	raced, _ := New(&draftCASClient{Client: c, afterRead: barrier})
	type outcome struct {
		candidate explorer.Explorer
		saved     *explorer.Explorer
		err       error
	}
	results := make(chan outcome, 2)
	candidates := []explorer.Explorer{before, before}
	for i := range candidates {
		title := fmt.Sprintf("Writer-%d", i)
		candidates[i].Title = title
		candidates[i].DraftConfig, candidates[i].DraftDigest = draftCASPayload(t, title)
		candidates[i].LastAuthoringCommandID = title
		candidates[i].UpdatedBy = title
		candidates[i].ActiveRevisionID = "must-not-replace-published"
	}
	for _, candidate := range candidates {
		go func(e explorer.Explorer) {
			saved, err := raced.SaveDraft(ctx, e, before.DraftVersion, before.DraftDigest)
			results <- outcome{e, saved, err}
		}(candidate)
	}
	var winner, loser outcome
	wins := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			wins++
			winner = result
		} else {
			loser = result
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly one winner, got %d winner=%#v loser=%v", wins, winner, loser.err)
	}
	freshClient, err := store.Open(ctx, os.Getenv("LOOM_TEST_ARANGO_URL"), database)
	if err != nil {
		t.Fatal(err)
	}
	defer freshClient.Close(context.Background())
	fresh, _ := New(freshClient)
	head, err := fresh.Get(ctx, before.Project, before.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if head.DraftVersion != 2 || head.DraftDigest != winner.candidate.DraftDigest || head.Title != winner.candidate.Title || head.ActiveRevisionID != before.ActiveRevisionID || head.LastAuthoringCommandID != winner.candidate.LastAuthoringCommandID {
		t.Fatalf("wrong winning head: %#v", head)
	}
	var gotPayload, wantPayload any
	if json.Unmarshal(head.DraftConfig, &gotPayload) != nil || json.Unmarshal(winner.candidate.DraftConfig, &wantPayload) != nil || !reflect.DeepEqual(gotPayload, wantPayload) {
		t.Fatal("winning payload differs")
	}
	history := draftCASHistory(t, ctx, freshClient, before)
	want := draftRevisionFromExplorer(before)
	want.ID = draftRevisionKey(before.Project, before.ExplorerID, before.DraftVersion)
	if len(history) != 1 || !reflect.DeepEqual(history[0], want) || head.PreviousDraftRevisionID != want.ID {
		t.Fatalf("history changed/lost original state: %#v expected %#v head=%#v", history, want, head)
	}
	if _, err := fresh.SaveDraft(ctx, loser.candidate, 1, before.DraftDigest); !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("stale retry not domain conflict: %v", err)
	}
	retriedHead, err := fresh.Get(ctx, before.Project, before.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retriedHead, head) || !reflect.DeepEqual(draftCASHistory(t, ctx, freshClient, before), history) {
		t.Fatal("stale retry changed head or immutable history")
	}
	var backend shared.ArangoError
	if !errors.As(loser.err, &backend) || backend.ErrorNum != shared.ErrArangoConflict {
		t.Fatalf("loser lost original Arango write conflict: %v", loser.err)
	}
	t.Logf("CAS_REAL_LOSER domainConflict=%v backendCode=%d backendNumber=%d type=%T error=%v", errors.Is(loser.err, explorer.ErrDraftConflict), backend.Code, backend.ErrorNum, loser.err, loser.err)
	if !errors.Is(loser.err, explorer.ErrDraftConflict) {
		t.Errorf("CAS_LOSER_NOT_DOMAIN_CONFLICT: %v", loser.err)
	}
}

func TestDraftCASUpdateFailureRollsBackHistoryAgainstArango(t *testing.T) {
	ctx, c, s, _ := draftCASDB(t)
	before := draftCASSeed(t, ctx, s)
	injected := errors.New("controlled transport failure before head update")
	failing, _ := New(&draftCASClient{Client: c, failUpdate: injected})
	candidate := before
	candidate.Title = "Must not persist"
	candidate.DraftConfig, candidate.DraftDigest = draftCASPayload(t, candidate.Title)
	saved, saveErr := failing.SaveDraft(ctx, candidate, 1, before.DraftDigest)
	head, err := s.Get(ctx, before.Project, before.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	history := draftCASHistory(t, ctx, c, before)
	if !reflect.DeepEqual(*head, before) || len(history) != 0 {
		t.Fatalf("FAILED_SAVE_PERSISTED_HISTORY: sameHead=%v historyCount=%d returnedNil=%v error=%v", reflect.DeepEqual(*head, before), len(history), saved == nil, saveErr)
	}
	if !errors.Is(saveErr, injected) {
		t.Fatalf("controlled failure not preserved: %v", saveErr)
	}
	t.Log("CAS_REAL_ABORT head and history unchanged after actual history INSERT")
}

func TestDraftCASPublicApplyConflictClassAgainstArango(t *testing.T) {
	ctx, c, s, _ := draftCASDB(t)
	before := draftCASSeed(t, ctx, s)
	var mu sync.Mutex
	reads := 0
	both := make(chan struct{})
	barrier := func(ctx context.Context) error {
		mu.Lock()
		reads++
		if reads == 2 {
			close(both)
		}
		mu.Unlock()
		select {
		case <-both:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	raced, _ := New(&draftCASClient{Client: c, afterRead: barrier})
	domain, err := explorer.NewService(raced)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := capability.Snapshot{Identity: capability.SnapshotIdentity{Project: before.Project, Generation: "generation-one", AuthorizationScopeDigest: "scope-one", SchemaDigest: "schema", ShapeDigest: "shape"}, Token: "snapshot-one", Status: capability.StatusReady, Complete: true}
	config := lifecycle.Config{Capability: ports.CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		Catalog: func(got capability.Snapshot, id string) ws.CatalogSnapshot {
			return ws.CatalogSnapshot{APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: got.Identity.Project, ExplorerID: id, SourceGeneration: got.Identity.Generation, AuthorizationScopeDigest: got.Identity.AuthorizationScopeDigest, SnapshotToken: got.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true}, Nodes: []ws.CatalogNode{{ID: "node", ResourceType: "Patient", RowRootEligible: true}}}
		},
	}}
	service, err := lifecycle.New(domain, config)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := range 2 {
		go func(i int) {
			_, err := service.ApplyCommands(ctx, before.Project, before.ExplorerID, authoringv2.ApplyCommandsRequest{CommandID: fmt.Sprintf("race-command-%d", i), SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: before.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: fmt.Sprintf("Patients-%d", i), Payload: &authoringv2.TablePayload{RootNodeID: "node"}}}}, "actor")
			results <- err
		}(i)
	}
	wins := 0
	var failed error
	for range 2 {
		if err := <-results; err == nil {
			wins++
		} else {
			failed = err
		}
	}
	if wins != 1 {
		t.Fatalf("expected one public Apply success, got=%d failed=%v", wins, failed)
	}
	var app *ports.Error
	if !errors.As(failed, &app) {
		t.Fatalf("public Apply error lacks lifecycle classification: %v", failed)
	}
	t.Logf("CAS_REAL_PUBLIC_APPLY class=%s code=%s cause=%v", app.Class, app.Code, app.Cause)
	if app.Class != ports.ClassConflict || app.Code != "DRAFT_CONFLICT" {
		t.Errorf("CAS_PUBLIC_APPLY_WRONG_CLASS: class=%s code=%s", app.Class, app.Code)
	}
}

func TestDraftCASAfterWinnerCommitAgainstArango(t *testing.T) {
	ctx, c, s, _ := draftCASDB(t)
	before := draftCASSeed(t, ctx, s)
	var mu sync.Mutex
	reads := 0
	both := make(chan struct{})
	committed := make(chan struct{})
	barrier := func(ctx context.Context) error {
		mu.Lock()
		reads++
		if reads == 2 {
			close(both)
		}
		mu.Unlock()
		select {
		case <-both:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	a, _ := New(&draftCASClient{Client: c, afterRead: barrier})
	b, _ := New(&draftCASClient{Client: c, afterRead: func(ctx context.Context) error {
		if err := barrier(ctx); err != nil {
			return err
		}
		select {
		case <-committed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})
	candidateA, candidateB := before, before
	candidateA.Title = "Committed winner"
	candidateB.Title = "Delayed loser"
	candidateA.DraftConfig, candidateA.DraftDigest = draftCASPayload(t, candidateA.Title)
	candidateB.DraftConfig, candidateB.DraftDigest = draftCASPayload(t, candidateB.Title)
	aResult := make(chan error, 1)
	bResult := make(chan error, 1)
	go func() {
		_, err := a.SaveDraft(ctx, candidateA, 1, before.DraftDigest)
		aResult <- err
		close(committed)
	}()
	go func() { _, err := b.SaveDraft(ctx, candidateB, 1, before.DraftDigest); bResult <- err }()
	if err := <-aResult; err != nil {
		t.Fatalf("forced winner failed: %v", err)
	}
	loser := <-bResult
	if loser == nil {
		t.Fatal("delayed stale writer succeeded")
	}
	head, err := s.Get(ctx, before.Project, before.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	history := draftCASHistory(t, ctx, c, before)
	if head.DraftVersion != 2 || head.DraftDigest != candidateA.DraftDigest || len(history) != 1 {
		t.Fatalf("delayed loser changed winning state: version=%d history=%d", head.DraftVersion, len(history))
	}
	var backend shared.ArangoError
	if !errors.As(loser, &backend) || backend.ErrorNum != shared.ErrArangoConflict {
		t.Fatalf("delayed loser lost original Arango write conflict: %v", loser)
	}
	t.Logf("CAS_AFTER_WINNER_COMMIT domainConflict=%v backendCode=%d backendNumber=%d error=%v", errors.Is(loser, explorer.ErrDraftConflict), backend.Code, backend.ErrorNum, loser)
	if !errors.Is(loser, explorer.ErrDraftConflict) {
		t.Errorf("CAS_DELAYED_LOSER_NOT_DOMAIN_CONFLICT: %v", loser)
	}
}

type draftHistoryClient struct {
	owner       map[string]any
	revisions   map[string]map[string]any
	calls       []queryCall
	transaction store.TransactionCollections
	loseCAS     bool
}

func (c *draftHistoryClient) WithTransaction(ctx context.Context, collections store.TransactionCollections, fn store.TransactionFunc) error {
	previousOwner := maps.Clone(c.owner)
	previousRevisions := make(map[string]map[string]any, len(c.revisions))
	for id, revision := range c.revisions {
		previousRevisions[id] = maps.Clone(revision)
	}
	c.transaction = collections
	err := fn(ctx, c)
	if err != nil {
		c.owner = previousOwner
		c.revisions = previousRevisions
	}
	return err
}

func (c *draftHistoryClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit store.RowVisitor) error {
	visit = storedJSONVisitorForTest(query, visit)
	c.calls = append(c.calls, queryCall{query: query, binds: binds})
	collection, _ := binds["@c"].(string)
	switch {
	case strings.HasPrefix(strings.TrimSpace(query), "INSERT") && collection == DraftRevisionsCollection:
		doc := maps.Clone(binds["doc"].(map[string]any))
		id := doc["_key"].(string)
		if c.revisions == nil {
			c.revisions = map[string]map[string]any{}
		}
		if c.revisions[id] != nil {
			return errors.New("duplicate immutable draft revision")
		}
		c.revisions[id] = doc
		return visit(maps.Clone(doc))
	case strings.Contains(query, "d.id == @revisionId") && collection == DraftRevisionsCollection:
		id, _ := binds["key"].(string)
		revision := c.revisions[id]
		if revision == nil || revision["id"] != binds["revisionId"] || revision["project"] != binds["project"] || revision["explorerId"] != binds["explorerId"] {
			return nil
		}
		return visit(maps.Clone(revision))
	case strings.Contains(query, "UPDATE d WITH MERGE(base, @doc"):
		if c.loseCAS || !draftOwnerMatches(c.owner, binds) {
			return nil
		}
		updated := maps.Clone(c.owner)
		if binds["clearDraftConfig"] == true {
			delete(updated, "draftConfig")
		}
		for key, value := range binds["doc"].(map[string]any) {
			updated[key] = value
		}
		updated["draftVersion"] = binds["nextVersion"]
		updated["previousDraftRevisionId"] = binds["revisionId"]
		c.owner = updated
		return visit(maps.Clone(updated))
	case strings.Contains(query, "RETURN d") && collection == ExplorersCollection && binds["expected"] != nil:
		if !draftOwnerMatches(c.owner, binds) {
			return nil
		}
		return visit(maps.Clone(c.owner))
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

func TestSaveDraftPersistsExactPreimageAtomicallyAndPreservesActiveRevision(t *testing.T) {
	previous := explorer.Explorer{
		Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive,
		DraftConfig:  json.RawMessage(`{"documents":[{"outputId":"patients","construction":{"steps":[{"id":"pivot","outputs":[{"id":"col_age","name":"age"}]}]}}]}`),
		DraftVersion: 4, DraftDigest: "sha256:old-draft", DraftSnapshotToken: "snapshot-old",
		DraftSourceGeneration: "generation-old", DraftAuthorizationScopeDigest: "scope-old",
		ActiveRevisionID: "published-revision-9", UpdatedBy: "alice", UpdatedAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
	}
	owner, err := db.Document(previous, explorerKey(previous.Project, previous.ExplorerID))
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
	updated.DraftVersion = 999
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
	owner, err := db.Document(previous, explorerKey(previous.Project, previous.ExplorerID))
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
	owner, err := db.Document(previous, explorerKey(previous.Project, previous.ExplorerID))
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
	doc, err := db.Document(revision, revision.ID)
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
	owner := explorer.Explorer{Project: "project-order", ExplorerID: "patients", Title: "Before", ManagementMode: viewer.ManagementInteractive,
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
			owner := explorer.Explorer{Project: "project-order", ExplorerID: "patients", Title: "Before", ManagementMode: viewer.ManagementInteractive,
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

type reviewReaderRows struct{ raw []byte }

func (c *reviewReaderRows) WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error {
	panic("read-only probe")
}

func (c *reviewReaderRows) QueryRows(_ context.Context, q string, _ int, b map[string]any, v store.RowVisitor) error {
	if strings.Contains(q, "JSON_STRINGIFY") {
		return v(map[string]any{"migrationJSON": string(c.raw)})
	}
	var row map[string]any
	if err := json.Unmarshal(c.raw, &row); err != nil {
		return err
	}
	return v(row)
}

func reviewLargeArchive(t *testing.T) ([]byte, string) {
	t.Helper()
	raw, err := os.ReadFile("../authoringv2/testdata/typed-unpivot.input.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"integer":10`, `"integer":9007199254740993`, 1))
	w, err := ws.DecodeStoredWorkspaceForMigration(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := ws.CanonicalStoredWorkspaceForMigration(w)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, fmt.Sprintf("sha256:%x", sha256.Sum256(canonical))
}

func TestReviewStoredReceiptLargeIntegerRemainsVerifiableOnRead(t *testing.T) {
	raw, digest := reviewLargeArchive(t)
	receipt := testCompilationReceipt(t, digest)
	receipt.NormalizedBundle = raw
	receipt.CompilationKey, _ = receiptmodel.CompilationKey(receipt)
	receipt.ID, _ = receiptmodel.ReceiptID(receipt)
	if err := receipt.Validate(); err != nil {
		t.Fatalf("typed original invalid: %v", err)
	}
	if err := receipt.ValidateID(); err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := New(&reviewReaderRows{doc})
	got, err := adapter.GetCompilationReceiptForExplorer(context.Background(), receipt.Project, receipt.ExplorerID, receipt.ID)
	fmt.Printf("typed_original_valid=true receipt_read_error=%v\n", err)
	if err != nil {
		t.Fatalf("authentic original receipt became unverifiable after row callback codec: %v", err)
	}
	if err := got.ValidateID(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got.NormalizedBundle), "9007199254740993") {
		t.Fatal("raw receipt integer changed")
	}
}

func TestReviewStoredOwnerHistoryRevisionLargeIntegerReadIdentity(t *testing.T) {
	raw, digest := reviewLargeArchive(t)
	for _, kind := range []string{"owner", "history", "revision"} {
		t.Run(kind, func(t *testing.T) {
			var value any
			switch kind {
			case "owner":
				value = explorer.Explorer{Project: "p", ExplorerID: "e", DraftConfig: raw, DraftDigest: digest}
			case "history":
				value = explorer.DraftRevision{ID: "h", Project: "p", ExplorerID: "e", DraftConfig: raw, DraftDigest: digest}
			case "revision":
				value = explorer.Revision{ID: "r", Project: "p", ExplorerID: "e", Config: raw, AuthoringBundle: raw, IntentDigest: digest}
			}
			bytes, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			adapter, _ := New(&reviewReaderRows{bytes})
			var found json.RawMessage
			switch kind {
			case "owner":
				owner, err := adapter.Get(context.Background(), "p", "e")
				if err != nil {
					t.Fatal(err)
				}
				found = owner.DraftConfig
			case "history":
				history, err := adapter.GetDraftRevision(context.Background(), "p", "e", "h")
				if err != nil {
					t.Fatal(err)
				}
				found = history.DraftConfig
			case "revision":
				revision, err := adapter.GetRevision(context.Background(), "r")
				if err != nil {
					t.Fatal(err)
				}
				found = revision.AuthoringWorkspaceBytes()
			}
			fmt.Printf("reader=%s exact_int64=%t rounded_int64=%t\n", kind, strings.Contains(string(found), "9007199254740993"), strings.Contains(string(found), "9007199254740992"))
			if !strings.Contains(string(found), "9007199254740993") {
				t.Fatalf("%s reader changed immutable/source workspace identity", kind)
			}
		})
	}
}

func storedJSONVisitorForTest(query string, visit store.RowVisitor) store.RowVisitor {
	if !strings.Contains(query, "JSON_STRINGIFY") {
		return visit
	}
	return func(row map[string]any) error {
		raw, err := json.Marshal(row)
		if err != nil {
			return err
		}
		return visit(map[string]any{"migrationJSON": string(raw)})
	}
}

func TestStoredJSONReaderRetainsLegacyNumericTimestampAndRawInteger(t *testing.T) {
	value, err := db.DecodeMigrationJSON[explorer.Explorer](`{"updatedAt":1776620000000,"draftConfig":{"integer":9007199254740993}}`)
	if err != nil {
		t.Fatal(err)
	}
	if value.UpdatedAt.UnixMilli() != 1776620000000 || !strings.Contains(string(value.DraftConfig), "9007199254740993") {
		t.Fatalf("legacy timestamp normalization changed packet: %#v", value)
	}
	for _, wrong := range []any{nil, map[string]any{"id": "wrong"}, 17, "{malformed"} {
		if _, err := db.DecodeMigrationJSON[explorer.Explorer](wrong); err == nil {
			t.Fatalf("invalid raw reader envelope accepted: %#v", wrong)
		}
	}
}

func TestReviewCurrentOwnerLargeIntegerReadIdentity(t *testing.T) {
	raw, _ := reviewLargeArchive(t)
	migrated, err := ws.MigrateStoredTableShapeWorkspace(raw, ws.CatalogSnapshot{})
	if err != nil {
		t.Fatal(err)
	}
	owner := explorer.Explorer{Project: "p", ExplorerID: "e", DraftConfig: migrated.CanonicalJSON, DraftDigest: migrated.MigratedCanonicalDigest}
	doc, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	adapter, _ := New(&reviewReaderRows{doc})
	stored, err := adapter.Get(context.Background(), "p", "e")
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := ws.DecodeWorkspace(stored.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("current_owner_int64_preserved=%t digest_matches=%t\n", strings.Contains(string(stored.DraftConfig), "9007199254740993"), digest == stored.DraftDigest)
	if digest != stored.DraftDigest || !strings.Contains(string(stored.DraftConfig), "9007199254740993") {
		t.Fatal("current draft getter changed legal INTEGER and canonical digest")
	}
}

type draftTransactionFailureClient struct {
	*draftHistoryClient
	failure  error
	attempts int
}

func (c *draftTransactionFailureClient) WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error {
	c.attempts++
	return c.failure
}

func TestSaveDraftTransactionErrorClassification(t *testing.T) {
	conflict := shared.ArangoError{HasError: true, Code: 409, ErrorNum: shared.ErrArangoConflict, ErrorMessage: "write conflict"}
	unique := shared.ArangoError{HasError: true, Code: 409, ErrorNum: 1210, ErrorMessage: "duplicate revision"}
	other := shared.ArangoError{HasError: true, Code: 409, ErrorNum: 1234, ErrorMessage: "other conflict"}
	transport := errors.New("transport unavailable")
	for _, tc := range []struct {
		name      string
		failure   error
		normalize bool
	}{
		{"raw write conflict", conflict, true},
		{"wrapped write conflict", fmt.Errorf("insert history: %w", conflict), true},
		{"existing domain conflict", explorer.ErrDraftConflict, false},
		{"unique constraint", unique, false},
		{"unrelated HTTP conflict", other, false},
		{"cancellation", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"transport", transport, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &draftTransactionFailureClient{draftHistoryClient: &draftHistoryClient{}, failure: tc.failure}
			s, _ := New(client)
			saved, err := s.SaveDraft(context.Background(), explorer.Explorer{Project: "p", ExplorerID: "e"}, 1, "digest")
			if saved != nil || client.attempts != 1 {
				t.Fatalf("saved=%v transaction attempts=%d", saved, client.attempts)
			}
			if tc.normalize {
				var cause shared.ArangoError
				if !errors.Is(err, explorer.ErrDraftConflict) || !errors.Is(err, tc.failure) || !errors.As(err, &cause) || cause.ErrorNum != shared.ErrArangoConflict {
					t.Fatalf("lost domain classification or backend cause: %v", err)
				}
			} else if err != tc.failure {
				t.Fatalf("unrelated error changed: got %v want %v", err, tc.failure)
			}
		})
	}
}
