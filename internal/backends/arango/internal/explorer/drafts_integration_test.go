package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	store "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
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
	w := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion, Explorer: authoringv2.ExplorerMetadata{Title: title}, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{}}
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
	e := explorer.Explorer{Project: "project-cas", ExplorerID: "patients", Title: "Initial", ManagementMode: explorer.ManagementInteractive, DraftConfig: raw, DraftVersion: 1, DraftDigest: digest, DraftSnapshotToken: "snapshot-one", DraftSourceGeneration: "generation-one", DraftAuthorizationScopeDigest: "scope-one", ActiveRevisionID: "published-stable", UpdatedBy: "initial", UpdatedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
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
		v, err := decode[explorer.DraftRevision](row)
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
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{
		Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		Catalog: func(got capability.Snapshot, id string) authoringv2.CatalogSnapshot {
			return authoringv2.CatalogSnapshot{APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: got.Identity.Project, ExplorerID: id, SourceGeneration: got.Identity.Generation, AuthorizationScopeDigest: got.Identity.AuthorizationScopeDigest, SnapshotToken: got.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true}, Nodes: []authoringv2.CatalogNode{{ID: "node", ResourceType: "Patient", RowRootEligible: true}}}
		},
	}}
	service, err := lifecycle.New(domain, config)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for i := range 2 {
		go func(i int) {
			_, err := service.ApplyCommands(ctx, before.Project, before.ExplorerID, authoringv2.ApplyCommandsRequest{CommandID: fmt.Sprintf("race-command-%d", i), SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: before.DraftDigest, Commands: []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: fmt.Sprintf("Patients-%d", i), RootNodeID: "node"}}}, "actor")
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
	var app *lifecycle.Error
	if !errors.As(failed, &app) {
		t.Fatalf("public Apply error lacks lifecycle classification: %v", failed)
	}
	t.Logf("CAS_REAL_PUBLIC_APPLY class=%s code=%s cause=%v", app.Class, app.Code, app.Cause)
	if app.Class != lifecycle.ClassConflict || app.Code != "DRAFT_CONFLICT" {
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
