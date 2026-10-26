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
)

func TestUpdateColumnRowValuePolicyFullStreamFailureLeavesDraftUnchanged(t *testing.T) {
	service, store, _, request := rowValuePolicyApplyFixture(t)
	beforeConfig := append([]byte(nil), store.created.DraftConfig...)
	beforeDigest := store.created.DraftDigest
	lateViolation := dataframeerrors.NewError(dataframeerrors.CodeConstructionRowValueMultipleValues, "AQL private detail must never reach the authoring response")
	rowsVisited := 0
	service.config.ValidateReceiptStream = func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) error {
		if receipt.IntentDigest == "" || bindings.PreviewLimit != 0 || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
			t.Fatalf("full-stream candidate bindings/receipt = %#v / %#v", bindings, receipt)
		}
		for rowsVisited < 1001 {
			rowsVisited++
		}
		return lateViolation
	}

	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if !errors.Is(err, lateViolation) {
		t.Fatalf("ApplyCommands error = %v, want late full-stream violation", err)
	}
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != ClassUnprocessable || lifecycleErr.Code != string(dataframeerrors.CodeConstructionRowValueMultipleValues) || lifecycleErr.Message != "This cohort has multiple distinct values for this field. Keep All unique values." {
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
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := workspace.Documents[0].Rows.Groups.RowValues[0].Policy; got != authoringv2.ConstructionRowValueAll {
		t.Fatalf("saved policy = %q, want original ALL", got)
	}
}

func TestUpdateColumnRowValuePolicyConflictAfterValidationKeepsConcurrentDraft(t *testing.T) {
	service, store, _, request := rowValuePolicyApplyFixture(t)
	service.config.ValidateReceiptStream = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) error { return nil }
	store.copyGet = true
	store.saveDraftHook = func(explorer.Explorer) error {
		concurrent := *store.created
		concurrent.DraftVersion = 5
		concurrent.Title = "Concurrent edit"
		store.created = &concurrent
		return explorer.ErrDraftConflict
	}

	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	if !errors.Is(err, explorer.ErrDraftConflict) {
		t.Fatalf("ApplyCommands error = %v, want draft conflict", err)
	}
	if store.saveDraftCalls != 1 || store.created.DraftVersion != 5 || store.created.Title != "Concurrent edit" {
		t.Fatalf("concurrent owner was overwritten after preflight: calls=%d owner=%#v", store.saveDraftCalls, store.created)
	}
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if got := workspace.Documents[0].Rows.Groups.RowValues[0].Policy; got != authoringv2.ConstructionRowValueAll {
		t.Fatalf("concurrent saved policy = %q, want original ALL", got)
	}
}

func TestUpdateColumnRowValuePolicyMustBeValidatedBeforeDraftSave(t *testing.T) {
	service, store, _, request := rowValuePolicyApplyFixture(t)
	service.config.ValidateReceiptStream = nil
	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice")
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "ROW_VALUE_POLICY_VALIDATION_UNAVAILABLE" {
		t.Fatalf("ApplyCommands error = %v, want validation-unavailable error", err)
	}
	if store.saveDraftCalls != 0 {
		t.Fatalf("draft saved without a full-stream validator: calls=%d", store.saveDraftCalls)
	}
}

func rowValuePolicyApplyFixture(t *testing.T) (*Service, *fakeStore, capability.Snapshot, authoringv2.ApplyCommandsRequest) {
	t.Helper()
	snapshot := readySnapshot("project-a", "generation-a", "snapshot-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
			Columns: []authoringv2.Column{{ColumnID: "patient-active", Column: "patient_active", Label: "Patient active", LogicalType: "boolean", OccurrenceID: authoringv2.RootOccurrenceID,
				Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "active", ProjectionMode: "VALUE"}}}},
			Rows: authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{
				Source:    authoringv2.GroupSource{Kind: authoringv2.GroupSourceExplicit, Explicit: &authoringv2.ExplicitGroupSource{RevisionID: "cohort-rev-1", UnassignedMemberPolicy: authoringv2.UnassignedMemberExclude}},
				RowValues: []authoringv2.ExplicitGroupRowValue{{ColumnID: "patient-active", Policy: authoringv2.ConstructionRowValueAll}},
			}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
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
		Project: "project-a", ExplorerID: "patients", Title: "Patients", ManagementMode: explorer.ManagementInteractive,
		DraftConfig: draft, DraftVersion: 4, DraftDigest: digest,
	}}
	config := testConfig(snapshot)
	var receipt *explorer.CompilationReceipt
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt = lifecycleTableShapeReceipt(snapshot, request.Workspace)
		return receipt, nil
	}
	config.ReceiptLookup = func(context.Context, string, string, string) (*explorer.CompilationReceipt, error) {
		return receipt, nil
	}
	service := newTestService(t, store, config)
	request := authoringv2.ApplyCommandsRequest{
		CommandID: "row-policy-one", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandUpdateColumnRowValuePolicy, OutputID: "patients", Column: "patient_active", RowValuePolicy: authoringv2.ConstructionRowValueOne}},
	}
	return service, store, snapshot, request
}

func TestUpdateColumnRowValuePolicyCheckerRunsBeforeSave(t *testing.T) {
	service, store, _, request := rowValuePolicyApplyFixture(t)
	var order []string
	service.config.ValidateReceiptStream = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) error {
		order = append(order, "validate")
		return nil
	}
	store.saveDraftHook = func(explorer.Explorer) error {
		order = append(order, "save")
		return nil
	}
	if _, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "alice"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(order) != "[validate save]" {
		t.Fatalf("candidate validation/save order = %v", order)
	}
}
