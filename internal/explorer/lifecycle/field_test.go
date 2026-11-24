package lifecycle

import (
	"context"
	"errors"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/recipeimport"
)

func TestRemoveProjectedFieldResolvesAuthoredOwnerAndEnforcesDraftAndAuthorization(t *testing.T) {
	ctx := context.Background()
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace, _, config := projectedFieldRemovalFixture(t, snapshot)
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := workspace.CanonicalJSON()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.AuthoringNetwork = projectedFieldNetwork
	removeImportedCalls := 0
	config.RemoveImportedField = func(context.Context, string, ports.AuthorizedCapability, ws.Document, string, string) (ws.Document, error) {
		removeImportedCalls++
		return ws.Document{}, errors.New("authored field must not be routed to pinned source editing")
	}
	compileCalls := 0
	var candidate ws.Workspace
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compileCalls++
		candidate = request.Workspace
		receipt := nativeReceipt(snapshot)
		store.receipt = receipt
		return receipt, nil
	}
	service := newTestService(t, store, config)
	request := projectedFieldRemovalRequest("remove-authored", "project-id", digest, 1, snapshot.Token)
	response, err := service.ApplyCommands(ctx, "project-a", "patients", request, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if compileCalls != 1 || store.saveDraftCalls != 1 || response.DraftVersion != 2 || removeImportedCalls != 0 {
		t.Fatalf("authored removal did not compile and save once through its owner: compile=%d saves=%d removeImported=%d response=%+v", compileCalls, store.saveDraftCalls, removeImportedCalls, response)
	}
	if findLifecycleColumn(response.Workspace.Documents[0], "project-id") || !findLifecycleColumn(response.Workspace.Documents[0], "patient-birthdate") || !findLifecycleColumn(response.Workspace.Documents[0], "coded-value") || !findLifecycleColumn(response.Workspace.Documents[0], "patient-id") || !findLifecycleColumn(response.Workspace.Documents[0], "patient-status") {
		t.Fatal("authored removal changed pinned/direct/codedValue columns, or retained the authored target")
	}
	if len(candidate.Documents) != 2 || candidate.Documents[1].Output.ID != "other" || !findLifecycleColumn(candidate.Documents[1], "other-project") {
		t.Fatal("candidate compilation omitted the unrelated output")
	}

	stale := projectedFieldRemovalRequest("remove-stale", "patient-id", digest, 1, snapshot.Token)
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", stale, "tester"); err == nil || store.saveDraftCalls != 1 || compileCalls != 1 {
		t.Fatalf("stale draft was accepted or compiled: err=%v saves=%d compiles=%d", err, store.saveDraftCalls, compileCalls)
	}
	unknown := projectedFieldRemovalRequest("remove-forged-id", "not-a-saved-column", response.DraftDigest, response.DraftVersion, snapshot.Token)
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", unknown, "tester"); err == nil || store.saveDraftCalls != 1 || compileCalls != 1 {
		t.Fatalf("unknown column identity was accepted: err=%v saves=%d compiles=%d", err, store.saveDraftCalls, compileCalls)
	}
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		crossTenant := snapshot
		crossTenant.Identity.Project = "project-b"
		return ports.AuthorizedCapability{Snapshot: crossTenant, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service = newTestService(t, store, config)
	crossTenant := projectedFieldRemovalRequest("remove-cross-tenant", "patient-id", response.DraftDigest, response.DraftVersion, snapshot.Token)
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", crossTenant, "tester"); err == nil || store.saveDraftCalls != 1 || compileCalls != 1 {
		t.Fatalf("cross-tenant authorization was accepted: err=%v saves=%d compiles=%d", err, store.saveDraftCalls, compileCalls)
	}
}

func TestRemoveProjectedFieldDisablesExactLegacyPinnedSlotAndPreservesAuthoredColumns(t *testing.T) {
	ctx := context.Background()
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace, bundle, config := projectedFieldRemovalFixture(t, snapshot)
	document := workspace.Documents[0]
	if document.ImportedSource == nil || len(document.ImportedSource.FieldBindings) != 0 {
		t.Fatal("fixture must represent a legacy imported draft with no FieldBindings")
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := workspace.CanonicalJSON()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.AuthoringNetwork = projectedFieldNetwork
	var removedStep, removedField string
	config.RemoveImportedField = func(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document, stepID, fieldID string) (ws.Document, error) {
		removedStep, removedField = stepID, fieldID
		updated := cloneLifecycleDocument(t, document)
		if updated.ImportedSource == nil {
			return ws.Document{}, errors.New("source plan is missing")
		}
		for stepIndex := range updated.ImportedSource.Steps {
			step := &updated.ImportedSource.Steps[stepIndex]
			if step.ID != stepID || step.Parameters == nil || step.Parameters.FieldGroup == nil {
				continue
			}
			for fieldIndex := range step.Parameters.FieldGroup.Fields {
				field := &step.Parameters.FieldGroup.Fields[fieldIndex]
				if field.FieldID == fieldID {
					field.Enabled = false
				}
			}
		}
		columnIDs := map[string]string{"fields[0]": "patient-id", "fields[1]": "patient-status"}
		targetColumnID := columnIDs[fieldID]
		if targetColumnID == "" {
			return ws.Document{}, errors.New("unexpected pinned field identity")
		}
		columns := updated.Columns[:0]
		for _, column := range updated.Columns {
			if column.ColumnID != targetColumnID {
				columns = append(columns, column)
			}
		}
		updated.Columns = columns
		return updated, nil
	}
	compileCalls := 0
	failCompile := false
	var compiledCandidate ws.Workspace
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compileCalls++
		compiledCandidate = request.Workspace
		if failCompile {
			return nil, errors.New("candidate downstream compilation failed")
		}
		receipt := nativeReceipt(snapshot)
		store.receipt = receipt
		return receipt, nil
	}
	service := newTestService(t, store, config)
	request := projectedFieldRemovalRequest("remove-pinned-status", "patient-status", digest, 1, snapshot.Token)
	response, err := service.ApplyCommands(ctx, "project-a", "patients", request, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if removedStep != "fields" || removedField != "fields[1]" {
		t.Fatalf("server-selected pinned owner = %s/%s, want fields/fields[1]", removedStep, removedField)
	}
	if response.DraftVersion != 2 || store.saveDraftCalls != 1 || compileCalls != 1 {
		t.Fatalf("pinned removal was not one compiled CAS write: response=%+v saves=%d compiles=%d", response, store.saveDraftCalls, compileCalls)
	}
	updated := response.Workspace.Documents[0]
	if findLifecycleColumn(updated, "patient-status") || !findLifecycleColumn(updated, "patient-id") || !findLifecycleColumn(updated, "project-id") || !findLifecycleColumn(updated, "patient-birthdate") || !findLifecycleColumn(updated, "coded-value") {
		t.Fatal("pinned removal dropped a sibling pinned field, direct field, or codedValue addition")
	}
	if updated.ImportedSource.Steps[1].Parameters.FieldGroup.Fields[1].Enabled {
		t.Fatalf("test stub did not disable the exact source field %q: %+v", removedField, updated.ImportedSource.Steps[1].Parameters.FieldGroup.Fields)
	}
	if len(compiledCandidate.Documents) != 2 || compiledCandidate.Documents[1].Output.ID != "other" || !findLifecycleColumn(compiledCandidate.Documents[1], "other-project") {
		t.Fatal("candidate compilation omitted the unrelated output")
	}
	if got := response.Workspace.Documents[0].ImportedRecipe; got == nil || *got != *document.ImportedRecipe {
		t.Fatal("pinned removal changed the immutable recipe identity")
	}
	if len(bundle.Outputs) != 1 || bundle.Outputs[0].Name != "patients" {
		t.Fatal("fixture bundle changed unexpectedly")
	}
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", request, "tester"); err != nil || compileCalls != 1 || store.saveDraftCalls != 1 {
		t.Fatalf("replay must return stored result without compile or save: err=%v compiles=%d saves=%d", err, compileCalls, store.saveDraftCalls)
	}

	before := append([]byte(nil), store.created.DraftConfig...)
	failCompile = true
	failed := projectedFieldRemovalRequest("remove-pinned-id-failure", "patient-id", response.DraftDigest, response.DraftVersion, snapshot.Token)
	if _, err := service.ApplyCommands(ctx, "project-a", "patients", failed, "tester"); err == nil || compileCalls != 2 || store.saveDraftCalls != 1 || string(store.created.DraftConfig) != string(before) {
		t.Fatalf("compile failure did not reach candidate compile or changed the persisted draft: err=%v compiles=%d saves=%d", err, compileCalls, store.saveDraftCalls)
	}
	if removedField != "fields[0]" || findLifecycleColumn(compiledCandidate.Documents[0], "patient-id") || findLifecycleColumn(compiledCandidate.Documents[0], "patient-status") || !findLifecycleColumn(compiledCandidate.Documents[0], "patient-birthdate") || !findLifecycleColumn(compiledCandidate.Documents[0], "coded-value") {
		t.Fatalf("failed candidate did not disable exactly the requested stable field: sourceFieldId=%q columns=%#v", removedField, compiledCandidate.Documents[0].Columns)
	}
}

func TestRemoveProjectedFieldDispatchesServerResolvedPinnedOutputKey(t *testing.T) {
	ctx := context.Background()
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace, _, config := projectedFieldRemovalFixture(t, snapshot)
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := workspace.CanonicalJSON()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.AuthoringNetwork = func(ctx context.Context, project string, authorized ports.AuthorizedCapability, document ws.Document) (authoringnetwork.AuthoringNetwork, error) {
		network, err := projectedFieldNetwork(ctx, project, authorized, document)
		if err != nil {
			return authoringnetwork.AuthoringNetwork{}, err
		}
		for index := range network.ProjectedFields {
			field := &network.ProjectedFields[index]
			if field.ColumnID == "patient-status" {
				field.Removal = authoringnetwork.ProjectedFieldRemovalDisablePinnedOutput
				field.SourceOutputKey = "urn:loinc|8480-6"
				field.SourceFieldID = ""
			}
		}
		return network, nil
	}
	var removedStep, removedKey string
	config.RemoveImportedOutput = func(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document, stepID, outputKey string) (ws.Document, error) {
		removedStep, removedKey = stepID, outputKey
		updated := cloneLifecycleDocument(t, document)
		columns := updated.Columns[:0]
		for _, column := range updated.Columns {
			if column.ColumnID != "patient-status" {
				columns = append(columns, column)
			}
		}
		updated.Columns = columns
		return updated, nil
	}
	config.CompileReceipt = func(_ context.Context, _ ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt := nativeReceipt(snapshot)
		store.receipt = receipt
		return receipt, nil
	}
	service := newTestService(t, store, config)
	request := projectedFieldRemovalRequest("remove-pivot-output", "patient-status", digest, 1, snapshot.Token)
	response, err := service.ApplyCommands(ctx, "project-a", "patients", request, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if removedStep != "fields" || removedKey != "urn:loinc|8480-6" {
		t.Fatalf("server-selected output owner = %s/%s", removedStep, removedKey)
	}
	if findLifecycleColumn(response.Workspace.Documents[0], "patient-status") || !findLifecycleColumn(response.Workspace.Documents[0], "patient-id") || !findLifecycleColumn(response.Workspace.Documents[0], "coded-value") {
		t.Fatal("output removal changed sibling or authored columns")
	}
}

func projectedFieldRemovalFixture(t *testing.T, snapshot capability.Snapshot) (ws.Workspace, recipe.Bundle, Config) {
	t.Helper()
	config := testConfig(snapshot)
	output := recipe.Output{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{
		{Name: "id", Expr: recipe.Expression{Select: "root.id"}},
		{Name: "status", Expr: recipe.Expression{Select: "root.status"}},
	}}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "default", TranslationVersion: "v1", Outputs: []recipe.Output{output}}
	catalog := config.Capability.Catalog(snapshot, "patients")
	workspace, err := recipeimport.ImportWorkspace(bundle, catalog, map[string][]recipe.StageColumn{"patients": {
		{ID: "patient-id", Name: "id", Label: "ID", Type: "string"},
		{ID: "patient-status", Name: "status", Label: "Status", Type: "string"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, ws.Column{
		ColumnID: "project-id", Column: "project_id", Label: "Project", OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceProjectID},
	})
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, ws.Column{
		ColumnID: "patient-birthdate", Column: "birth_date", Label: "Birth date", OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}},
	})
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, ws.Column{
		ColumnID: "coded-value", Column: "identifier_type", Label: "Identifier type", OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceCodedValue, Lookup: &ws.LookupSource{
			Binding: &fhirbinding.CorrelatedBinding{OwnerPath: "identifier[]", KeyPath: "identifier[].type.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "value", LogicalType: "string"},
			Key:     &fhirbinding.CorrelatedKey{System: "urn:study:A", Code: "MRN"},
		}},
	})
	other := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "other", Title: "Other"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition(),
		Columns: []ws.Column{{ColumnID: "other-project", Column: "other_project", Label: "Other project", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceProjectID}}},
	}
	workspace.Documents = append(workspace.Documents, other)
	workspace.Tabs = append(workspace.Tabs, ws.Tab{ID: "other-tab", Title: "Other", OutputID: "other", Order: 1, Visible: true})
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate projected-field fixture: %v", err)
	}
	return workspace, bundle, config
}

func projectedFieldNetwork(_ context.Context, _ string, _ ports.AuthorizedCapability, document ws.Document) (authoringnetwork.AuthoringNetwork, error) {
	network := authoringnetwork.NewAuthoringNetwork(document, "IMPORTED_SOURCE")
	network.Bindings = []authoringnetwork.NetworkColumnBinding{
		{ColumnID: "patient-id", OccurrenceID: ws.RootOccurrenceID, SourceStepID: "fields", Status: "EXACT"},
		{ColumnID: "patient-status", OccurrenceID: ws.RootOccurrenceID, SourceStepID: "fields", Status: "EXACT"},
	}
	network.ProjectedFields = authoringnetwork.ProjectedFieldsForDocument(document, network)
	for index := range network.ProjectedFields {
		field := &network.ProjectedFields[index]
		if field.ColumnID == "patient-id" {
			field.SourceFieldID = "fields[0]"
			field.Removal = authoringnetwork.ProjectedFieldRemovalDisablePinned
		}
		if field.ColumnID == "patient-status" {
			field.SourceFieldID = "fields[1]"
			field.Removal = authoringnetwork.ProjectedFieldRemovalDisablePinned
		}
	}
	return network, nil
}

func projectedFieldRemovalRequest(commandID, columnID, digest string, version int64, snapshotToken string) authoringv2.ApplyCommandsRequest {
	return authoringv2.ApplyCommandsRequest{
		CommandID: commandID, SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshotToken,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandRemoveProjectedField, OutputID: "patients", Payload: &authoringv2.ColumnPayload{ColumnID: columnID}}},
	}
}

func findLifecycleColumn(document ws.Document, columnID string) bool {
	for _, column := range document.Columns {
		if column.ColumnID == columnID {
			return true
		}
	}
	return false
}

func cloneLifecycleDocument(t *testing.T, document ws.Document) ws.Document {
	return constructionfixtures.CloneDocument(t, document)
}

func TestReviewProjectedRemovalCandidateCASFailure(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace, _, config := projectedFieldRemovalFixture(t, snapshot)
	digest, _ := workspace.Digest()
	raw, _ := workspace.CanonicalJSON()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config.AuthoringNetwork = projectedFieldNetwork
	compileCalls := 0
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compileCalls++
		if len(request.Workspace.Documents) != 2 || findLifecycleColumn(request.Workspace.Documents[0], "project-id") {
			t.Fatal("incorrect candidate workspace compiled")
		}
		receipt := nativeReceipt(snapshot)
		store.receipt = receipt
		return receipt, nil
	}
	store.saveDraftHook = func(explorer.Explorer) error { return explorer.ErrDraftConflict }
	service := newTestService(t, store, config)
	request := projectedFieldRemovalRequest("projected-removal-cas-failure", "project-id", digest, 1, snapshot.Token)
	_, err := service.ApplyCommands(context.Background(), "project-a", "patients", request, "tester")
	if err == nil || compileCalls != 1 || store.saveDraftCalls != 1 || string(store.created.DraftConfig) != string(raw) || store.created.DraftVersion != 1 {
		t.Fatalf("CAS failure changed owner or did not compile candidate: err=%v compiles=%d saves=%d version=%d", err, compileCalls, store.saveDraftCalls, store.created.DraftVersion)
	}
}
