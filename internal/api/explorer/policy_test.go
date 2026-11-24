package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/proposal"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"

	"github.com/calypr/loom/internal/testkit/explorerfixtures"

	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestColumnPolicyHTTPContractPreviewApplyAndClosedIntent(t *testing.T) {
	ctx := context.Background()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(capability.SnapshotIdentity{Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(scope), SchemaDigest: "schema", ShapeDigest: "shape"}, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}, nil, []capability.Candidate{{ID: "candidate-id", NodeID: "patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst}, SupportedOperations: []capability.Operation{capability.OperationSelect}, Populated: true}}, nil)
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Patients"}, Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: "base", ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition(), Columns: []ws.Column{{ColumnID: "patient-id", Column: "c_patient", Label: "Patient ID", LogicalType: "string", OccurrenceID: "base", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}}, Tabs: []ws.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Visible: true}}}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := workspace.Digest()
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "custom", Title: "Patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	previews := 0
	config := lifecycle.Config{Capability: ports.CapabilityResolver{Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }, ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}, ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}, Catalog: ws.CatalogFromCapability}, CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt, err := explorerfixtures.PersistTestNativeReceipt(ctx, t, service, request, snapshot)
		if err != nil {
			return nil, err
		}
		clone := *receipt
		clone.ColumnPolicyProposal = receiptapi.CloneColumnPolicyBinding(request.ColumnPolicyProposal)
		clone.CompilationKey, err = receiptmodel.CompilationKey(clone)
		if err != nil {
			return nil, err
		}
		clone.ID, err = receiptmodel.ReceiptID(clone)
		if err != nil {
			return nil, err
		}
		return service.StoreCompilationReceipt(ctx, clone)
	}, PreviewReceipt: func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previews++
		if receipt.ColumnPolicyProposal == nil || !bindings.IncludeRowIdentity || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
			t.Fatal("preview did not use exact policy receipt/output")
		}
		if err := visit(map[string]any{"__loom_row_id": "patient-1", "c_patient": "p1"}); err != nil {
			return dataframeexecution.PreviewSummary{}, err
		}
		return dataframeexecution.PreviewSummary{RowCount: 1, Complete: true}, nil
	}}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	path := "/api/v1/projects/project-a/explorers/custom/authoring/v2"
	intent := fmt.Sprintf(`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","columnId":"patient-id","projectionMode":"FIRST"}`, snapshot.Token, digest)
	for _, extra := range []string{`,"project":"other"`, `,"candidateWorkspace":{}`, `,"candidateDocument":{}`, `,"candidateNetwork":{}`, `,"source":{"kind":"field","field":{"path":"secret"}}`, `,"limit":0`, `,"limit":null`} {
		response := requestJSON(t, app, http.MethodPost, path+"/column-policy-proposals", strings.TrimSuffix(intent, "}")+extra+"}")
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("injected policy intent HTTP %d: %s", response.StatusCode, response.Body)
		}
	}
	response := requestJSON(t, app, http.MethodPost, path+"/column-policy-proposals", intent)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("policy proposal HTTP %d: %s", response.StatusCode, response.Body)
	}
	var proposal proposal.ColumnPolicyProposal
	if err := json.Unmarshal([]byte(response.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" || proposal.CandidateReceiptID != proposal.ProposalID || proposal.CandidateDocument.Output.ID != "patients" || len(proposal.CandidateDocument.Columns) != 1 || proposal.CandidateDocument.Columns[0].ColumnID != "patient-id" || proposal.CandidateDocument.Columns[0].Source.Field.ProjectionMode != "FIRST" || proposal.CandidateNetwork.Bindings[0].ValueMode != "FIRST" || proposal.CandidateRowIdentity.Stages[0].Identity.Kind != "RECORD" || previews != 1 {
		t.Fatalf("policy candidate public fields lost: %s", response.Body)
	}
	before, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if before.DraftVersion != 1 || before.DraftDigest != digest || !bytes.Equal(raw, before.DraftConfig) {
		t.Fatal("Preview/Cancel saved policy")
	}
	applyBody := func(commandID string) string {
		return fmt.Sprintf(`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_COLUMN_POLICY_PROPOSAL","outputId":"patients","proposalId":%q}]}`, commandID, ws.CurrentSemanticsVersion, snapshot.Token, digest, proposal.ProposalID)
	}
	applied := requestJSON(t, app, http.MethodPost, path+"/commands", applyBody("policy-apply"))
	if applied.StatusCode != http.StatusOK {
		t.Fatalf("policy Apply HTTP %d: %s", applied.StatusCode, applied.Body)
	}
	saved, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if saved.DraftVersion != 2 || previews != 2 {
		t.Fatal("policy Apply did not validate runtime/save once")
	}
	reloaded, err := ws.DecodeWorkspace(saved.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Documents[0].Columns) != 1 || reloaded.Documents[0].Columns[0].ColumnID != "patient-id" || reloaded.Documents[0].Columns[0].Source.Field.ProjectionMode != "FIRST" {
		t.Fatal("policy reload duplicated or lost exact column")
	}
	stale := requestJSON(t, app, http.MethodPost, path+"/commands", applyBody("stale-policy"))
	if stale.StatusCode != http.StatusConflict {
		t.Fatalf("stale policy Apply HTTP %d: %s", stale.StatusCode, stale.Body)
	}
	final, err := service.Get(ctx, "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if final.DraftVersion != saved.DraftVersion || !bytes.Equal(final.DraftConfig, saved.DraftConfig) {
		t.Fatal("stale policy changed saved plan")
	}
}
