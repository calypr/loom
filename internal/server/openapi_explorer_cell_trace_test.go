package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestCellTraceRouteReturnsReceiptBoundTypedEvidence(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "custom", "Custom", "", "test"); err != nil {
		t.Fatal(err)
	}
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
		},
		ReceiptLookup: service.CompilationReceiptForExplorer,
		CellTrace: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
			if receipt == nil || receipt.ID == "" || bindings.DatasetGeneration != snapshot.Identity.Generation {
				t.Fatalf("trace was not receipt/generation bound: receipt=%#v bindings=%#v", receipt, bindings)
			}
			if request.Output != "patients" || request.RowID != "row-7" || request.Column != "c_patient" || request.Offset != 2 || request.Limit != 3 {
				t.Fatalf("unexpected trace request: %#v", request)
			}
			return dataframeexecution.CellTraceResult{
				RowID: request.RowID, Column: request.Column, Value: "patient-7", Status: dataframeexecution.CellTraceValue,
				Contributions: []dataframeexecution.CellTraceContribution{{ResourceType: "Patient", ResourceID: "patient-7", Value: "patient-7"}},
				HasMore:       true, NextOffset: 3, Complete: true,
			}, nil
		},
	}
	receipt, err := persistTestNativeReceipt(context.Background(), t, service, lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/cell-trace", `{"receiptId":"`+receipt.ID+`","outputId":"patients","rowId":"row-7","column":"c_patient","offset":2,"limit":3}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("trace status=%d body=%s", response.StatusCode, response.Body)
	}
	var body loomapi.CellTraceResponse
	if err := json.Unmarshal([]byte(response.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body.Binding.ReceiptId != receipt.ID || body.Binding.OutputId != "patients" || body.Binding.Project != "project-a" || body.Binding.ExplorerId != "custom" || body.Binding.Generation != "generation-a" || body.Binding.ScopeDigest == "" {
		t.Fatalf("trace binding=%#v", body.Binding)
	}
	if body.Trace.Status != loomapi.CellTraceTraceStatusVALUE || body.Trace.Value != "patient-7" || len(body.Trace.Contributions) != 1 || body.Trace.Contributions[0].ResourceId == nil || *body.Trace.Contributions[0].ResourceId != "patient-7" || !body.Trace.HasMore || body.Trace.NextOffset != 3 || !body.Trace.Complete {
		t.Fatalf("trace evidence=%#v", body.Trace)
	}
}

func TestCellTraceRouteMapsLifecycleValidationErrors(t *testing.T) {
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	response := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/cell-trace", `{"receiptId":"receipt","outputId":"out","rowId":"row","column":"column"}`)
	if response.StatusCode != http.StatusServiceUnavailable || !strings.Contains(response.Body, `"code":"CELL_TRACE_UNAVAILABLE"`) {
		t.Fatalf("unavailable status=%d body=%s", response.StatusCode, response.Body)
	}
}
