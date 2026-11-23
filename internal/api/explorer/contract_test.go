package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	loomapi "github.com/calypr/loom/api/loomapi"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	executionarango "github.com/calypr/loom/internal/dataframe/execution/arango"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/testkit"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

func TestNativeV2RouteUsesAuthorizedPersistedReceipt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	executionScope := scope
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "custom", "Custom", "", "test"); err != nil {
		t.Fatal(err)
	}
	previewCalls := 0
	config := lifecycle.Config{
		Capability: ports.CapabilityResolver{
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: executionScope}, nil
			},
		},
		ReceiptLookup: service.CompilationReceiptForExplorer,
		PreviewReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCalls++
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("builder preview context has no deadline")
			}
			if remaining := time.Until(deadline); remaining > executionarango.ExplorerPreviewTimeout || remaining < executionarango.ExplorerPreviewTimeout-time.Second {
				t.Fatalf("direct preview deadline remaining = %s, want approximately %s", remaining, executionarango.ExplorerPreviewTimeout)
			}
			if got := executionarango.PreviewQueryMaxRuntime(ctx); got != executionarango.ExplorerPreviewTimeout {
				t.Fatalf("direct preview query max runtime = %s, want %s", got, executionarango.ExplorerPreviewTimeout)
			}
			if receipt == nil || bindings.AuthScopeMode != authscope.ReadScopeUnrestricted || bindings.IncludeAuthResourcePath || !bindings.IncludeRowIdentity || !bindings.IncludeSourceIdentity {
				t.Fatalf("preview bindings changed authorization or omitted preview-only row metadata: receipt=%#v bindings=%#v", receipt, bindings)
			}
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, PlanMode: "physical", PlanProfile: "generic_fhir_graph_recipe", TraversalCount: 0}, nil
		},
	}
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := explorerfixtures.PersistTestNativeReceipt(context.Background(), t, service, ports.CompileReceiptRequest{Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token, Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	preview := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+receipt.ID+`","outputId":"patients","limit":5}`)
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.StatusCode, preview.Body)
	}
	ownerAfterPreview, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if ownerAfterPreview.ActiveRevisionID != "" || len(ownerAfterPreview.DraftConfig) != 0 {
		t.Fatalf("preview mutated active or draft state: %#v", ownerAfterPreview)
	}
	if previewCalls != 1 {
		t.Fatalf("preview calls=%d", previewCalls)
	}
	unknown := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+receipt.ID+`","outputId":"missing","limit":5}`)
	if unknown.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(unknown.Body, `"code":"UNKNOWN_AUTHORING_OUTPUT"`) || previewCalls != 1 {
		t.Fatalf("unknown output status=%d calls=%d body=%s", unknown.StatusCode, previewCalls, unknown.Body)
	}
	for _, path := range []string{
		"/api/v1/projects/project-b/explorers/custom/authoring/v2/preview",
		"/api/v1/projects/project-a/explorers/other/authoring/v2/preview",
	} {
		foreign := requestJSON(t, app, http.MethodPost, path, `{"receiptId":"`+receipt.ID+`","outputId":"patients","limit":5}`)
		if foreign.StatusCode != http.StatusNotFound || !strings.Contains(foreign.Body, `"code":"COMPILE_RECEIPT_NOT_FOUND"`) || previewCalls != 1 {
			t.Fatalf("foreign receipt path=%s status=%d calls=%d body=%s", path, foreign.StatusCode, previewCalls, foreign.Body)
		}
	}
	executionScope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	widened := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+receipt.ID+`","outputId":"patients","limit":5}`)
	if widened.StatusCode != http.StatusConflict || !strings.Contains(widened.Body, `"code":"RECEIPT_STALE"`) || previewCalls != 1 {
		t.Fatalf("scope mismatch status=%d calls=%d body=%s", widened.StatusCode, previewCalls, widened.Body)
	}
}

func TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "custom", "Custom", "", "test"); err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		Capability: ports.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return explorerfixtures.PersistTestNativeReceipt(ctx, t, service, request, snapshot)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)

	commandBody := fmt.Sprintf(`{"commandId":"browser-command-1","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":0,"commands":[{"type":"CREATE_TABLE","title":"Patients","rootNodeId":"n_patient"}]}`, ws.CurrentSemanticsVersion, snapshot.Token)
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if created.StatusCode != http.StatusOK {
		t.Fatalf("command status=%d body=%s", created.StatusCode, created.Body)
	}
	if !strings.Contains(created.Body, `"columns":[]`) {
		t.Fatalf("command response omitted required empty columns array: %s", created.Body)
	}
	var response authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(created.Body), &response); err != nil {
		t.Fatal(err)
	}
	if response.DraftVersion != 1 || len(response.Workspace.Documents) != 1 || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(response.Workspace.Documents[0].Output.ID) {
		t.Fatalf("command response=%#v", response)
	}
	replayed := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if replayed.StatusCode != http.StatusOK || !strings.Contains(replayed.Body, `"draftVersion":1`) {
		t.Fatalf("replay status=%d body=%s", replayed.StatusCode, replayed.Body)
	}
	conflicting := strings.Replace(commandBody, `"Patients"`, `"Different"`, 1)
	conflict := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", conflicting)
	if conflict.StatusCode != http.StatusConflict || !strings.Contains(conflict.Body, `"code":"COMMAND_ID_CONFLICT"`) {
		t.Fatalf("command ID conflict status=%d body=%s", conflict.StatusCode, conflict.Body)
	}
	secondBody := fmt.Sprintf(`{"commandId":"browser-command-2","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"commands":[{"type":"CREATE_TABLE","title":"Visits","rootNodeId":"n_patient"}]}`, ws.CurrentSemanticsVersion, snapshot.Token)
	second := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", secondBody)
	if second.StatusCode != http.StatusOK || !strings.Contains(second.Body, `"draftVersion":2`) {
		t.Fatalf("second command status=%d body=%s", second.StatusCode, second.Body)
	}
	var secondResponse authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(second.Body), &secondResponse); err != nil {
		t.Fatal(err)
	}
	oldRetry := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if oldRetry.StatusCode != http.StatusConflict || !strings.Contains(oldRetry.Body, `"code":"DRAFT_CONFLICT"`) {
		t.Fatalf("older command retry status=%d body=%s", oldRetry.StatusCode, oldRetry.Body)
	}
	reconcileBody := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q}`, snapshot.Token, secondResponse.DraftVersion, secondResponse.DraftDigest)
	reconciled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/reconcile", reconcileBody)
	if reconciled.StatusCode != http.StatusOK || !strings.Contains(reconciled.Body, `"kind":"ExplorerBuilderReceipt"`) {
		t.Fatalf("reconcile status=%d body=%s", reconciled.StatusCode, reconciled.Body)
	}
	staleBody := strings.Replace(reconcileBody, secondResponse.DraftDigest, "stale-digest", 1)
	stale := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/reconcile", staleBody)
	if stale.StatusCode != http.StatusConflict || !strings.Contains(stale.Body, `"code":"DRAFT_CONFLICT"`) {
		t.Fatalf("stale reconcile status=%d body=%s", stale.StatusCode, stale.Body)
	}
	paddedOutputID := secondResponse.Workspace.Documents[0].Output.ID
	paddedBody := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q,"outputId":%q}`, snapshot.Token, secondResponse.DraftVersion, secondResponse.DraftDigest, " "+paddedOutputID+" ")
	padded := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/reconcile", paddedBody)
	if padded.StatusCode != http.StatusBadRequest || !strings.Contains(padded.Body, `"code":"MALFORMED_REQUEST"`) {
		t.Fatalf("padded output status=%d body=%s", padded.StatusCode, padded.Body)
	}
}

func TestCreateExplorerFromCurrentClonesWorkspaceOnServer(t *testing.T) {
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "source", Title: "Source", ManagementMode: viewer.ManagementInteractive, DraftConfig: canonical, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers", `{"name":"Cloned Explorer","title":"Cloned Explorer","sourceExplorerId":"source"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("clone status=%d body=%s", created.StatusCode, created.Body)
	}
	clone, err := service.Get(context.Background(), "project-a", explorer.StableExplorerID("Cloned Explorer"))
	if err != nil {
		t.Fatal(err)
	}
	clonedWorkspace, err := ws.DecodeWorkspace(clone.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if clone.DraftVersion != 1 || clonedWorkspace.Explorer.Title != "Cloned Explorer" || len(clonedWorkspace.Documents) != len(workspace.Documents) || clonedWorkspace.Documents[0].Output.ID != workspace.Documents[0].Output.ID {
		t.Fatalf("cloned Explorer=%#v workspace=%#v", clone, clonedWorkspace)
	}
}

func TestAcceptedConstructionSharedContractFixture(t *testing.T) {
	fixture, err := os.ReadFile(testkit.FixturePath(t, "openapi/accepted-construction-response.json"))
	if err != nil {
		t.Fatal(err)
	}
	var response loomapi.ApplyCommandsResponse
	if err := httpapi.DecodeStrict(fixture, &response); err != nil {
		t.Fatalf("strictly decode accepted construction fixture: %v", err)
	}
	accepted := response.AcceptedConstruction
	if accepted == nil {
		t.Fatal("fixture omitted acceptedConstruction")
	}
	if accepted.Receipt.IntentDigest != response.DraftDigest || accepted.Preview.ReceiptId != accepted.Receipt.ReceiptId {
		t.Fatalf("accepted identities do not match the saved draft: accepted=%#v draftDigest=%q", accepted, response.DraftDigest)
	}
	if accepted.PreviewLimit < 1 || accepted.PreviewLimit > 1000 || len(accepted.Preview.Rows) != 1 || accepted.Preview.Rows[0]["patient_id"] != "p1" {
		t.Fatalf("accepted preview shape is invalid: %#v", accepted)
	}
	if len(accepted.Preview.RowSources) != len(accepted.Preview.Rows) || accepted.Preview.RowLineageCapability.Status != loomapi.RowLineageCapabilityStatusAVAILABLE {
		t.Fatalf("accepted preview row metadata is incomplete: %#v", accepted.Preview)
	}
	receiptJSON, err := json.Marshal(accepted.Receipt)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(receiptJSON, []byte(`"builder"`)) || bytes.Contains(receiptJSON, []byte(`"constructionProposal"`)) {
		t.Fatalf("public accepted receipt exposed duplicated workspace or private binding: %s", receiptJSON)
	}

	var raw map[string]any
	if err := json.Unmarshal(fixture, &raw); err != nil {
		t.Fatal(err)
	}
	acceptedObject := raw["acceptedConstruction"].(map[string]any)
	receiptObject := acceptedObject["receipt"].(map[string]any)
	receiptObject["constructionProposal"] = map[string]any{"baseDraftDigest": "private"}
	withPrivateBinding, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := httpapi.DecodeStrict(withPrivateBinding, &loomapi.ApplyCommandsResponse{}); err == nil {
		t.Fatal("strict accepted-construction contract accepted a private proposal binding")
	}
}
