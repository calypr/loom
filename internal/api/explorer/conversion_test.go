package explorerapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/gofiber/fiber/v3"
)

func TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{
			{
				Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "consumer", Title: "Consumer"},
				RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
				Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
					ID: "combine", Inputs: []ws.ConstructionInputRef{
						{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "patients"},
						{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "other"},
					},
					Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
						Kind: ws.ConstructionCombineAppend,
						Projections: []ws.ConstructionCombineProjection{
							{OutputColumnID: "combined", InputIndex: 0, InputColumnID: "old-patient-id"},
							{OutputColumnID: "combined", InputIndex: 1, InputColumnID: "other-id"},
						},
					}},
					Outputs: []ws.StageColumn{{ID: "combined", Name: "combined", Label: "Combined", Type: "string", Nullable: true}},
				}}},
			},
			{
				Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "patients", Title: "Patients"},
				RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []ws.Column{{ColumnID: "id", Column: "id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
					Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
			{
				Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "other", Title: "Other"},
				RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []ws.Column{{ColumnID: "other-id", Column: "other_id", Label: "Other ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
					Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
		},
		Tabs: []ws.Tab{
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
		Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive,
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
		Capability: ports.CapabilityResolver{
			Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*receiptmodel.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, resolveErr := receiptapi.CompileValidatedResolution(ctx, engine, receipt, bindings)
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
	if _, err := lifecycleService.Preview(context.Background(), authoringcore.PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: reconciled.Receipt.ID, OutputID: "patients",
		SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
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
		Builder ws.Workspace            `json:"builder"`
		Outputs []loomapi.ReceiptOutput `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(apiResponse.Body), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Builder.Documents) != 3 || len(decoded.Outputs) != 1 || decoded.Outputs[0].OutputId != "patients" {
		t.Fatalf("selected reconcile API response builder/outputs = %d/%#v", len(decoded.Builder.Documents), decoded.Outputs)
	}
}

func TestV2ReceiptResponsePreservesResultUnitsAndOmitsAbsentUnit(t *testing.T) {
	const system = "http://unitsofmeasure.org"
	aggregateUnit := &unit.UnitIdentity{System: system, Code: "kg"}
	derivedUnit := &unit.UnitIdentity{System: system, Code: "kg/m2"}
	receipt := &receiptmodel.CompilationReceipt{
		ID:     "receipt-a",
		Bundle: recipe.Bundle{Outputs: []recipe.Output{{Name: "patients", RowGrain: "RESOURCE"}}},
		EmittedColumns: []viewer.EmittedColumn{
			{OutputID: "patients", PublicColumn: "mean_weight", Label: "Mean weight", LogicalType: "decimal", ResultUnit: aggregateUnit},
			{OutputID: "patients", PublicColumn: "bmi", Label: "BMI", LogicalType: "decimal", ResultUnit: derivedUnit},
			{OutputID: "patients", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string"},
		},
	}
	workspace := ws.Workspace{Documents: []ws.Document{{Output: ws.Output{ID: "patients", Title: "Patients"}}}}

	response := v2ReceiptResponse(receipt, workspace)
	if len(response.Outputs) != 1 || len(response.Outputs[0].Columns) != 3 {
		t.Fatalf("compile response columns = %#v", response.Outputs)
	}
	if got := response.Outputs[0].Columns[0].ResultUnit; got == nil || got.System != aggregateUnit.System || got.Code != aggregateUnit.Code {
		t.Fatalf("aggregate result unit = %#v, want %#v", got, aggregateUnit)
	}
	if got := response.Outputs[0].Columns[1].ResultUnit; got == nil || got.System != derivedUnit.System || got.Code != derivedUnit.Code {
		t.Fatalf("derived result unit = %#v, want %#v", got, derivedUnit)
	}

	raw, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Outputs []struct {
			Columns []map[string]json.RawMessage `json:"columns"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("decode compile response %s: %v", raw, err)
	}
	assertResultUnit := func(raw json.RawMessage, code string) {
		t.Helper()
		var got map[string]string
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode resultUnit %s: %v", raw, err)
		}
		if len(got) != 2 || got["system"] != "http://unitsofmeasure.org" || got["code"] != code {
			t.Fatalf("resultUnit JSON = %#v, want {system: UCUM, code: %q}", got, code)
		}
	}
	assertResultUnit(wire.Outputs[0].Columns[0]["resultUnit"], "kg")
	assertResultUnit(wire.Outputs[0].Columns[1]["resultUnit"], "kg/m2")
	if _, exists := wire.Outputs[0].Columns[2]["resultUnit"]; exists {
		t.Fatalf("unitless compile column contains resultUnit: %s", wire.Outputs[0].Columns[2]["resultUnit"])
	}
}
