package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestRestoreDraftRevisionPublicAPIIsScopedAndReloadVisible(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	previous, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	previous.Explorer.Title = "Previous draft"
	previousRaw, err := previous.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	previousDigest, err := previous.Digest()
	if err != nil {
		t.Fatal(err)
	}
	current := previous
	current.Explorer.Title = "Current draft"
	currentRaw, err := current.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	currentDigest, err := current.Digest()
	if err != nil {
		t.Fatal(err)
	}

	store := newTestExplorerStore()
	owner := explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: current.Explorer.Title,
		ManagementMode: explorer.ManagementInteractive, DraftConfig: currentRaw, DraftVersion: 4, DraftDigest: currentDigest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		PreviousDraftRevisionID:       "draft_revision_custom_3", ActiveRevisionID: "active_published",
	}
	if _, err := store.create(owner); err != nil {
		t.Fatal(err)
	}
	store.draftRevisions[testExplorerKey(owner.Project, owner.ExplorerID)+"\x00"+owner.PreviousDraftRevisionID] = explorer.DraftRevision{
		ID: owner.PreviousDraftRevisionID, Project: owner.Project, ExplorerID: owner.ExplorerID, DraftVersion: 3,
		DraftDigest: previousDigest, DraftConfig: previousRaw, Title: previous.Explorer.Title,
		SnapshotToken: snapshot.Token, SourceGeneration: snapshot.Identity.Generation,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}
	store.revisions[owner.ActiveRevisionID] = explorer.Revision{
		ID: owner.ActiveRevisionID, Project: owner.Project, ExplorerID: owner.ExplorerID,
		SourceGeneration: snapshot.Identity.Generation, AuthoringBundle: previousRaw,
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := lifecycle.Config{Capability: lifecycle.CapabilityResolver{
		Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
		Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
		ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
			return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
		},
		Catalog: authoringV2Catalog,
	}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	commandBody := fmt.Sprintf(`{"commandId":"restore-draft-3","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":4,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, currentDigest, owner.PreviousDraftRevisionID)
	response := requestJSON(t, app, http.MethodPost, basePath+"/commands", commandBody)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restore status=%d body=%s", response.StatusCode, response.Body)
	}
	var restored authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(response.Body), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.DraftVersion != 5 || restored.Workspace.Explorer.Title != "Previous draft" || len(restored.Results) != 1 || restored.Results[0].Type != authoringv2.CommandResultDraftRestored {
		t.Fatalf("restore response = %#v", restored)
	}
	if restored.PreviousDraftRevisionID == "" || restored.PreviousDraftRevisionID == owner.PreviousDraftRevisionID {
		t.Fatalf("restore response did not expose the new Undo target: %#v", restored)
	}
	updated, err := domain.Get(context.Background(), owner.Project, owner.ExplorerID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveRevisionID != owner.ActiveRevisionID || updated.DraftVersion != 5 {
		t.Fatalf("restore changed publication or missed CAS version: active=%q draftVersion=%d", updated.ActiveRevisionID, updated.DraftVersion)
	}

	builder := requestJSON(t, app, http.MethodGet, basePath+"/builder", "")
	if builder.StatusCode != http.StatusOK || !containsJSONField(builder.Body, "previousDraftRevisionId", restored.PreviousDraftRevisionID) {
		t.Fatalf("reload Builder state did not expose Undo target: status=%d body=%s", builder.StatusCode, builder.Body)
	}

	replayed := requestJSON(t, app, http.MethodPost, basePath+"/commands", commandBody)
	if replayed.StatusCode != http.StatusOK || !containsJSONField(replayed.Body, "draftVersion", "5") {
		t.Fatalf("restore idempotency status=%d body=%s", replayed.StatusCode, replayed.Body)
	}

	staleCASBody := fmt.Sprintf(`{"commandId":"stale-restore-cas","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":4,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, currentDigest, restored.PreviousDraftRevisionID)
	staleCAS := requestJSON(t, app, http.MethodPost, basePath+"/commands", staleCASBody)
	if staleCAS.StatusCode != http.StatusConflict || !strings.Contains(staleCAS.Body, `"code":"DRAFT_CONFLICT"`) {
		t.Fatalf("stale restore CAS status=%d body=%s", staleCAS.StatusCode, staleCAS.Body)
	}

	store.mu.Lock()
	key := testExplorerKey(owner.Project, owner.ExplorerID) + "\x00" + restored.PreviousDraftRevisionID
	priorRevision := store.draftRevisions[key]
	priorRevision.AuthorizationScopeDigest = "different-scope"
	store.draftRevisions[key] = priorRevision
	store.mu.Unlock()
	staleScopeBody := fmt.Sprintf(`{"commandId":"stale-restore-scope","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":5,"expectedDraftDigest":%q,"commands":[{"type":"RESTORE_DRAFT_REVISION","draftRevisionId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, restored.DraftDigest, restored.PreviousDraftRevisionID)
	staleScope := requestJSON(t, app, http.MethodPost, basePath+"/commands", staleScopeBody)
	if staleScope.StatusCode != http.StatusConflict || !strings.Contains(staleScope.Body, `"code":"STALE_DRAFT_SOURCE_CONTEXT"`) {
		t.Fatalf("stale restore source scope status=%d body=%s", staleScope.StatusCode, staleScope.Body)
	}
}

func containsJSONField(raw, key, value string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &fields) != nil {
		return false
	}
	var decoded string
	if err := json.Unmarshal(fields[key], &decoded); err == nil {
		return decoded == value
	}
	return string(fields[key]) == value
}
