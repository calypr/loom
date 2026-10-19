package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{
			{
				Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "consumer", Title: "Consumer"},
				RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
				Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
					ID: "combine", Inputs: []authoringv2.ConstructionInputRef{
						{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: "patients"},
						{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: "other"},
					},
					Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
						Kind: authoringv2.ConstructionCombineAppend,
						Projections: []authoringv2.ConstructionCombineProjection{
							{OutputColumnID: "combined", InputIndex: 0, InputColumnID: "old-patient-id"},
							{OutputColumnID: "combined", InputIndex: 1, InputColumnID: "other-id"},
						},
					}},
					Outputs: []authoringv2.StageColumn{{ID: "combined", Name: "combined", Label: "Combined", Type: "string", Nullable: true}},
				}}},
			},
			{
				Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "patients", Title: "Patients"},
				RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []authoringv2.Column{{ColumnID: "id", Column: "id", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
					Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
			{
				Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "other", Title: "Other"},
				RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []authoringv2.Column{{ColumnID: "other-id", Column: "other_id", Label: "Other ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
					Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
		},
		Tabs: []authoringv2.Tab{
			{ID: "consumer-tab", Title: "Consumer", OutputID: "consumer", Order: 0, Visible: true},
			{ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 1, Visible: true},
			{ID: "other-tab", Title: "Other", OutputID: "other", Order: 2, Visible: true},
		},
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 3, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	queryRows := func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: queryRows, PreviewQueryRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	serviceConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, resolveErr := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if resolveErr != nil {
				return dataframeexecution.PreviewSummary{}, resolveErr
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, visit)
		},
	}
	lifecycleService, err := lifecycle.New(domain, serviceConfig)
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycle.ReconcileRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		DraftVersion: 3, DraftDigest: digest, OutputID: "patients",
	}
	reconciled, err := lifecycleService.ReconcileWithWorkspace(context.Background(), request)
	if err != nil {
		t.Fatalf("selected output reconcile: %v", err)
	}
	returnedDigest, err := reconciled.Workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if len(reconciled.Workspace.Documents) != 3 || returnedDigest != digest {
		t.Fatalf("selected reconcile did not return the full persisted workspace: %#v", reconciled.Workspace)
	}
	if len(reconciled.Receipt.Bundle.Outputs) != 1 || reconciled.Receipt.Bundle.Outputs[0].Name != "patients" {
		t.Fatalf("selected reconcile receipt is not scoped to the requested output: %#v", reconciled.Receipt.Bundle.Outputs)
	}
	if _, err := lifecycleService.Preview(context.Background(), lifecycle.PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: reconciled.Receipt.ID, OutputID: "patients",
		SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	}); err != nil {
		t.Fatalf("selected target Preview failed with a stale current-draft consumer: %v", err)
	}
	if _, err := lifecycleService.Reconcile(context.Background(), lifecycle.ReconcileRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, SnapshotToken: request.SnapshotToken,
		DraftVersion: request.DraftVersion, DraftDigest: request.DraftDigest,
	}); err == nil {
		t.Fatal("full-workspace reconcile unexpectedly compiled a stale consumer")
	}

	response := v2ReceiptResponse(reconciled.Receipt, reconciled.Workspace)
	if len(response.Builder.Documents) != 3 || response.IntentDigest != reconciled.Receipt.IntentDigest || len(response.Outputs) != 1 || response.Outputs[0].OutputId != "patients" {
		t.Fatalf("scoped receipt response builder/outputs mismatch: builder=%#v outputs=%#v", response.Builder, response.Outputs)
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, serviceConfig)
	outputID := "patients"
	body, err := json.Marshal(loomapi.ReconcileRequest{SnapshotToken: snapshot.Token, DraftVersion: 3, DraftDigest: digest, OutputId: &outputID})
	if err != nil {
		t.Fatal(err)
	}
	apiResponse := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/patients/authoring/v2/reconcile", string(body))
	if apiResponse.StatusCode != http.StatusOK {
		t.Fatalf("selected output reconcile status=%d body=%s", apiResponse.StatusCode, apiResponse.Body)
	}
	var decoded struct {
		Builder authoringv2.Workspace   `json:"builder"`
		Outputs []loomapi.ReceiptOutput `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(apiResponse.Body), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Builder.Documents) != 3 || len(decoded.Outputs) != 1 || decoded.Outputs[0].OutputId != "patients" {
		t.Fatalf("selected reconcile API response builder/outputs = %d/%#v", len(decoded.Builder.Documents), decoded.Outputs)
	}
}
