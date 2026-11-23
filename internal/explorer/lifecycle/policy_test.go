package lifecycle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestUpdateColumnRowValuePolicyFullStreamFailureLeavesDraftUnchanged(t *testing.T) {
	config, store, request := rowValuePolicyApplySetup(t)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeDigest := store.created.DraftDigest
	lateViolation := dataframeerrors.NewError(dataframeerrors.CodeConstructionRowValueMultipleValues, "AQL private detail must never reach the authoring response")
	rowsVisited := 0
	config.ValidateReceiptStream = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings) error {
		if receipt.IntentDigest == "" || bindings.PreviewLimit != 0 || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
			t.Fatalf("full-stream candidate bindings/receipt = %#v / %#v", bindings, receipt)
		}
		for rowsVisited < 1001 {
			rowsVisited++
		}
		return lateViolation
	}

	service := newTestService(t, store, config)
	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if !errors.Is(err, lateViolation) {
		t.Fatalf("ApplyCommands error = %v, want late full-stream violation", err)
	}
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ports.ClassUnprocessable || lifecycleErr.Code != string(dataframeerrors.CodeConstructionRowValueMultipleValues) || lifecycleErr.Message != "This cohort has multiple distinct values for this field. Keep All unique values." {
		t.Fatalf("ApplyCommands error = %#v, want the stable actionable row-value diagnostic", err)
	}
	if strings.Contains(err.Error(), "AQL private detail") {
		t.Fatalf("raw execution details leaked through ApplyCommands: %v", err)
	}
	if rowsVisited <= 1000 {
		t.Fatalf("validator visited %d rows, want a row beyond the Preview cap", rowsVisited)
	}
	if store.saveDraftCalls != 0 || store.created.DraftVersion != 4 || store.created.DraftDigest != beforeDigest || !bytes.Equal(store.created.DraftConfig, beforeConfig) {
		t.Fatalf("failed full-stream validation changed draft: save calls=%d owner=%#v", store.saveDraftCalls, store.created)
	}
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := workspace.Documents[0].Rows.Groups.RowValues[0].Policy; got != ws.ConstructionRowValueAll {
		t.Fatalf("saved policy = %q, want original ALL", got)
	}
}

func TestUpdateColumnRowValuePolicyConflictAfterValidationKeepsConcurrentDraft(t *testing.T) {
	config, store, request := rowValuePolicyApplySetup(t)
	config.ValidateReceiptStream = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) error { return nil }
	store.copyGet = true
	store.saveDraftHook = func(explorer.Explorer) error {
		concurrent := *store.created
		concurrent.DraftVersion = 5
		concurrent.Title = "Concurrent edit"
		store.created = &concurrent
		return explorer.ErrDraftConflict
	}

	service := newTestService(t, store, config)
	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("ApplyCommands error = %v, want draft conflict", err)
	}
	if store.saveDraftCalls != 1 || store.created.DraftVersion != 5 || store.created.Title != "Concurrent edit" {
		t.Fatalf("concurrent owner was overwritten after preflight: calls=%d owner=%#v", store.saveDraftCalls, store.created)
	}
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := workspace.Documents[0].Rows.Groups.RowValues[0].Policy; got != ws.ConstructionRowValueAll {
		t.Fatalf("concurrent saved policy = %q, want original ALL", got)
	}
}

func TestUpdateColumnRowValuePolicyMustBeValidatedBeforeDraftSave(t *testing.T) {
	config, store, request := rowValuePolicyApplySetup(t)
	config.ValidateReceiptStream = nil
	service := newTestService(t, store, config)
	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "ROW_VALUE_POLICY_VALIDATION_UNAVAILABLE" {
		t.Fatalf("ApplyCommands error = %v, want validation-unavailable error", err)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("draft saved without a full-stream validator: calls=%d", store.saveDraftCalls)
	}
}

func rowValuePolicyApplySetup(t *testing.T) (Config, *fakeStore, authoringv2.ApplyCommandsRequest) {
	t.Helper()
	snapshot := readySnapshot("project-a", "generation-a", "snapshot-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []ws.Column{{ColumnID: "patient-active", Column: "patient_active", Label: "Patient active", LogicalType: "boolean", OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "active", ProjectionMode: "VALUE"}}}},
			Rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{
				Source:    ws.GroupSource{Kind: ws.GroupSourceExplicit, Explicit: &ws.ExplicitGroupSource{RevisionID: "cohort-rev-1", UnassignedMemberPolicy: ws.UnassignedMemberExclude}},
				RowValues: []ws.ExplicitGroupRowValue{{ColumnID: "patient-active", Policy: ws.ConstructionRowValueAll}},
			}},
		}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{
		Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: viewer.ManagementInteractive,
		DraftConfig: draft, DraftVersion: 4, DraftDigest: digest,
	}}
	config := testConfig(snapshot)
	var receipt *receiptmodel.CompilationReceipt
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt = lifecycleConstructionReceipt(snapshot, request.Workspace)
		return receipt, nil
	}
	config.ReceiptLookup = func(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error) {
		return receipt, nil
	}
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "row-policy-one", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandUpdateColumnRowValuePolicy, OutputID: "patients", Payload: &authoringv2.ColumnPayload{Column: "patient_active", RowValuePolicy: ws.ConstructionRowValueOne}}},
	}
	return config, store, request
}

func TestUpdateColumnRowValuePolicyCheckerRunsBeforeSave(t *testing.T) {
	config, store, request := rowValuePolicyApplySetup(t)
	var order []string
	config.ValidateReceiptStream = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) error {
		order = append(order, "validate")
		return nil
	}
	store.saveDraftHook = func(explorer.Explorer) error {
		order = append(order, "save")
		return nil
	}
	service := newTestService(t, store, config)
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(order) != "[validate save]" {
		t.Fatalf("candidate validation/save order = %v", order)
	}
}

func TestPreviousDynamicInventoryCompilerReceiptCannotExecute(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	receipt.CompilerContractVersion = "loom.explorer.compiler/v20"
	var err error
	receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = receiptmodel.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Validate(); err != nil {
		t.Fatalf("historical generic receipt must remain readable: %v", err)
	}
	service := &Service{config: Config{CompileReceipt: func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		t.Fatal("must reject without recompiling")
		return nil, nil
	}}}
	err = service.validateReceiptRoute(receipt, "project-a", "patients")
	var typed *ports.Error
	if !errors.As(err, &typed) || typed.Code != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("historical compiler receipt was executable: %v", err)
	}
}
