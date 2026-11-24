package lifecycle

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	"github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
)

func TestReplacementCompilesOnlyTargetAndLeavesBrokenConsumerForRepair(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true, Populated: true},
		{ID: "research-study", ResourceType: "ResearchStudy", RowRootEligible: true, Populated: true},
		{ID: "diagnostic-report", ResourceType: "DiagnosticReport", RowRootEligible: true, Populated: true},
	}
	snapshot.Edges = []capability.Edge{
		{
			ID: "study-result-report", FromNodeID: "research-study", ToNodeID: "diagnostic-report",
			Label: "result_DiagnosticReport", SourceResourceType: "ResearchStudy", TargetResourceType: "DiagnosticReport",
			StorageDirection: "OUTBOUND", AllowsRepeatedTarget: true,
		},
		{
			ID: "report-study-result", FromNodeID: "diagnostic-report", ToNodeID: "research-study",
			Label: "result_DiagnosticReport", SourceResourceType: "DiagnosticReport", TargetResourceType: "ResearchStudy",
			StorageDirection: "INBOUND",
		},
	}
	for _, item := range []struct{ nodeID, resourceType string }{
		{"patient", "Patient"}, {"research-study", "ResearchStudy"}, {"diagnostic-report", "DiagnosticReport"},
	} {
		_, err := schemaIndexResolve(item.resourceType, "id")
		if err != nil {
			t.Fatal(err)
		}
		candidateID := item.nodeID + "-id"
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: candidateID, NodeID: item.nodeID, ResourceType: item.resourceType, FieldPath: "id", Label: item.resourceType + " ID",
			LogicalType: "string", Cardinality: "required_one", Populated: true,
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		})
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{
			{
				Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "consumer", Title: "Consumer"},
				RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
				Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
					ID: "copy_source", Inputs: []ws.ConstructionInputRef{
						{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "patients"},
						{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "other"},
					},
					Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
						Kind: ws.ConstructionCombineAppend,
						Projections: []ws.ConstructionCombineProjection{
							{OutputColumnID: "copied", InputIndex: 0, InputColumnID: "patient-id-column"},
							{OutputColumnID: "copied", InputIndex: 1, InputColumnID: "other-id-column"},
						},
					}}, Outputs: []ws.StageColumn{{ID: "copied", Name: "copied", Label: "Copied patient ID", Type: "string", Nullable: true}},
				}}},
			},
			{
				Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
				Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []ws.Column{{ColumnID: "patient-id-column", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
					Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
			{
				Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "other", Title: "Other"}, RootResourceType: "Patient",
				Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []ws.Column{{ColumnID: "other-id-column", Column: "other_id", Label: "Other ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
					Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
		},
		Tabs: []ws.Tab{{ID: "consumer-tab", Title: "Consumer", OutputID: "consumer", Order: 0, Visible: true}, {ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 1, Visible: true}, {ID: "other-tab", Title: "Other", OutputID: "other", Order: 2, Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	compile := func(ws ws.Workspace) (explorercompilation.WorkspaceResult, error) {
		compiled, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "patients", ws, snapshot, explorercompilation.ResolvedInputs{})
		if err != nil {
			return explorercompilation.WorkspaceResult{}, err
		}
		plan, err := recipeplan.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
		if err != nil {
			return explorercompilation.WorkspaceResult{}, err
		}
		resolved, err := recipeplan.ResolveRecipePlan(plan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
		if err != nil {
			return explorercompilation.WorkspaceResult{}, err
		}
		if _, err := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy()); err != nil {
			return explorercompilation.WorkspaceResult{}, err
		}
		return compiled, nil
	}
	if _, err := compile(workspace); err != nil {
		t.Fatalf("full preimage with valid consumer did not compile: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, err := ws.DecodeWorkspace(draft)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 4, DraftDigest: digest}}
	config := testConfig(snapshot)
	resolveForCompilation := config.Capability.ForCompilation
	compilationCapabilityCalls := 0
	rejectNextCompilationCapability := false
	config.Capability.ForCompilation = func(ctx context.Context, project, token string) (ports.AuthorizedCapability, error) {
		compilationCapabilityCalls++
		if rejectNextCompilationCapability {
			rejectNextCompilationCapability = false
			return ports.AuthorizedCapability{}, capability.ErrStaleSnapshot
		}
		return resolveForCompilation(ctx, project, token)
	}
	config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Columns: []string{"id"}, RowCount: 1, Complete: true}, nil
	}
	config.Capability.Catalog = func(got capability.Snapshot, explorerID string) ws.CatalogSnapshot {
		catalog := ws.CatalogSnapshot{
			APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: explorerID,
			SourceGeneration: got.Identity.Generation, AuthorizationScopeDigest: got.Identity.AuthorizationScopeDigest, ResolvedSchemaDigest: got.Identity.SchemaDigest,
			SnapshotToken: got.Token, Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
			Nodes: []ws.CatalogNode{
				{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
				{ID: "research-study", ResourceType: "ResearchStudy", RowRootEligible: true},
				{ID: "diagnostic-report", ResourceType: "DiagnosticReport", RowRootEligible: true},
			},
			Edges: []ws.CatalogEdge{
				{ID: "study-result-report", FromNodeID: "research-study", ToNodeID: "diagnostic-report", Label: "result_DiagnosticReport", StorageDirection: "OUTBOUND", Populated: true},
				{ID: "report-study-result", FromNodeID: "diagnostic-report", ToNodeID: "research-study", Label: "result_DiagnosticReport", StorageDirection: "INBOUND", Populated: true},
			},
		}
		for _, candidate := range got.Candidates {
			choice, choiceErr := capability.NewFieldConstructionChoice(got.Token, candidate)
			if choiceErr != nil {
				t.Fatal(choiceErr)
			}
			catalog.Candidates = append(catalog.Candidates, ws.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
			})
		}
		return catalog
	}
	var compiledWorkspaces []ws.Workspace
	config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compiledWorkspaces = append(compiledWorkspaces, request.Workspace)
		compiled, compileErr := compile(request.Workspace)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt := nativeReceipt(snapshot)
		receipt.IntentDigest, compileErr = request.Workspace.Digest()
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.NormalizedBundle, compileErr = request.Workspace.CanonicalJSON()
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.Bundle = compiled.Bundle
		receipt.RecipeDigest = compiled.RecipeDigest
		receipt.ResolvedRecipeDigest = compiled.RecipeDigest
		receipt.EmittedColumns = compiled.EmittedColumns
		receipt.CompiledOutputSchemas = testCompiledOutputSchemas(request.Workspace)
		receipt.OutputColumnProvenance = testOutputColumnProvenance(request.Workspace)
		receipt.ConstructionStages = map[string][]receiptmodel.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		contracts, marshalErr := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: compiled.OutputContracts})
		if marshalErr != nil {
			return nil, marshalErr
		}
		receipt.PublicOutputContract = contracts
		receipt.OutputContractDigest, compileErr = receiptmodel.CompilationArtifactDigest(contracts)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.CompilationKey, compileErr = receiptmodel.CompilationKey(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.ID, compileErr = receiptmodel.ReceiptID(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		if validateErr := receipt.Validate(); validateErr != nil {
			return nil, validateErr
		}
		store.receipt = receipt
		return receipt, nil
	}
	service := newTestService(t, store, config)
	replacement, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 4, DraftDigest: digest,
		Mode: authoringv2.RowChangeModeReplace, OutputID: "patients", RootNodeID: "research-study",
	})
	if err != nil || replacement.Assessment.Status != authoringv2.RowChangeReady || replacement.CandidateReceiptID == "" {
		t.Fatalf("replacement assessment with broken consumer=%#v err=%v", replacement, err)
	}
	if len(replacement.Assessment.WorkspaceOutputConsumers) != 1 || replacement.Assessment.WorkspaceOutputConsumers[0].OutputID != "consumer" {
		t.Fatalf("replacement did not report affected consumer: %#v", replacement.Assessment.WorkspaceOutputConsumers)
	}
	if len(store.receipt.Bundle.Outputs) != 1 || store.receipt.Bundle.Outputs[0].Name != "patients" || store.receipt.Bundle.Outputs[0].RootResourceType != "ResearchStudy" {
		t.Fatalf("replacement candidate receipt was not target-only: %#v", store.receipt.Bundle.Outputs)
	}

	result, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "replace-root-with-consumer", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyTableRootRebase, OutputID: "patients", Payload: &authoringv2.RowPayload{RowChange: replacement.Assessment.Proposal}}},
	}, "alice")
	if err != nil {
		t.Fatalf("replacement Apply with invalidated consumer: %v", err)
	}
	if len(result.Workspace.Documents) != 3 || !reflect.DeepEqual(result.Workspace.Documents[0], canonicalWorkspace.Documents[0]) {
		t.Fatalf("replacement mutated consumer document:\n got %#v\nwant %#v", result.Workspace.Documents[0], canonicalWorkspace.Documents[0])
	}
	if result.Workspace.Tabs[1].Order != 1 || result.Workspace.Tabs[1].OutputID != "patients" {
		t.Fatalf("replacement changed persisted tab order/identity: %#v", result.Workspace.Tabs)
	}
	if _, err := compile(result.Workspace); err == nil {
		t.Fatal("full workspace compile succeeded although consumer still references removed patient_id")
	}

	preserved, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		DraftVersion: result.DraftVersion, DraftDigest: result.DraftDigest, Mode: authoringv2.RowChangeModePreserve,
		OutputID: "patients", RootNodeID: "diagnostic-report",
	})
	if err != nil {
		t.Fatalf("PRESERVE assessment with unchanged downstream consumer: %v", err)
	}
	if preserved.Assessment.Status != authoringv2.RowChangeReady || preserved.CandidateReceiptID == "" {
		t.Fatalf("connected DiagnosticReport PRESERVE assessment=%#v", preserved)
	}
	if got := compiledOutputIDs(compiledWorkspaces[len(compiledWorkspaces)-1]); !reflect.DeepEqual(got, []string{"patients", "other"}) {
		t.Fatalf("PRESERVE receipt compile scope = %#v, want selected output and independent sibling only", got)
	}
	if len(store.receipt.Bundle.Outputs) != 2 {
		t.Fatalf("PRESERVE receipt contains the downstream consumer: %#v", store.receipt.Bundle.Outputs)
	}
	storedAfterPreserve, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedAfterPreserve.Documents[0], result.Workspace.Documents[0]) {
		t.Fatalf("PRESERVE assessment mutated the saved consumer:\n got %#v\nwant %#v", storedAfterPreserve.Documents[0], result.Workspace.Documents[0])
	}

	capabilities, err := service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: result.DraftVersion, ExpectedDraftDigest: result.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatalf("selected replacement capabilities with stale consumer: %v", err)
	}
	if capabilities.OutputID != "patients" || capabilities.SelectedStage.ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("selected replacement capabilities identity=%#v", capabilities)
	}
	if len(capabilities.WorkspaceInputs) != 1 || capabilities.WorkspaceInputs[0].OutputID != "other" {
		t.Fatalf("independent workspace input was not retained: %#v", capabilities.WorkspaceInputs)
	}
	if got := compiledOutputIDs(compiledWorkspaces[len(compiledWorkspaces)-1]); !reflect.DeepEqual(got, []string{"patients", "other"}) {
		t.Fatalf("capability compilation scope = %#v, want replacement plus independent sibling", got)
	}
	capabilityCallsBeforeReconcile := compilationCapabilityCalls
	selectedReconcile, err := service.ReconcileWithWorkspace(context.Background(), ReconcileRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		DraftVersion: result.DraftVersion, DraftDigest: result.DraftDigest, OutputID: "patients",
	})
	if err != nil {
		t.Fatalf("selected replacement reconcile with stale consumer: %v", err)
	}
	if calls := compilationCapabilityCalls - capabilityCallsBeforeReconcile; calls != 1 {
		t.Fatalf("selected reconcile resolved compilation capability %d times, want once", calls)
	}
	if got := compiledOutputIDs(compiledWorkspaces[len(compiledWorkspaces)-1]); !reflect.DeepEqual(got, []string{"patients"}) {
		t.Fatalf("selected replacement reconcile scope = %#v, want target only", got)
	}
	if _, err := service.Preview(context.Background(), authoringcore.PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: selectedReconcile.Receipt.ID, OutputID: "patients",
		SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	}); err != nil {
		t.Fatalf("preview selected replacement with stale consumer: %v", err)
	}
	rejectNextCompilationCapability = true
	capabilityCallsBeforeReconcile = compilationCapabilityCalls
	_, err = service.ReconcileWithWorkspace(context.Background(), ReconcileRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		DraftVersion: result.DraftVersion, DraftDigest: result.DraftDigest, OutputID: "patients",
	})
	if lifecycleErrorCode(err) != "STALE_CATALOG_SNAPSHOT" {
		t.Fatalf("next selected reconcile error = %v, want fresh stale-capability rejection", err)
	}
	if calls := compilationCapabilityCalls - capabilityCallsBeforeReconcile; calls != 1 {
		t.Fatalf("next selected reconcile resolved compilation capability %d times, want a fresh resolution", calls)
	}
	if _, err := service.ReconcileWithWorkspace(context.Background(), ReconcileRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		DraftVersion: result.DraftVersion, DraftDigest: result.DraftDigest,
	}); err == nil {
		t.Fatal("unscoped reconciliation accepted the full workspace with a stale consumer")
	}
	if got := compiledOutputIDs(compiledWorkspaces[len(compiledWorkspaces)-1]); !reflect.DeepEqual(got, []string{"consumer", "patients", "other"}) {
		t.Fatalf("unscoped reconciliation did not compile the full workspace: %#v", got)
	}

	repaired := result.Workspace
	repaired.Documents = append([]ws.Document(nil), result.Workspace.Documents...)
	repaired.Documents[0] = canonicalWorkspace.Documents[0]
	step := repaired.Documents[0].Construction.Steps[0]
	selectedCompiled, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "patients", result.Workspace, snapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatalf("compile full replacement recipe before target-only preview selection: %v", err)
	}
	targetPlan, err := recipeplan.BuildRecipePlan(selectedCompiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
	if err != nil {
		t.Fatal(err)
	}
	targetResolved, err := recipeplan.ResolveRecipePlan(targetPlan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
	if err != nil {
		t.Fatal(err)
	}
	targetResolved.SemanticPlan.Bindings.OutputNames = []string{"patients"}
	targetLowered, err := facade.CompileResolvedRecipePlan(targetResolved, facade.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var replacementID string
	for _, output := range targetLowered.Outputs {
		if output.Name != "patients" {
			continue
		}
		for _, column := range output.OutputSchema {
			if column.Name == "id" {
				replacementID = column.ID
			}
		}
	}
	if replacementID == "" {
		t.Fatalf("replacement compiler schema did not expose its id column: %#v", targetLowered.Outputs)
	}
	step.Operation.Combine.Projections[0].InputColumnID = replacementID
	repaired.Documents[0].Construction.Steps[0] = step
	if _, err := compile(repaired); err != nil {
		t.Fatalf("full workspace did not compile after consumer repair: %v", err)
	}
}

func schemaIndexResolve(resourceType, path string) (schema.RowPathFacts, error) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		return schema.RowPathFacts{}, err
	}
	return index.ResolveRowPath(schema.DefinitionName(resourceType), path)
}

func compiledOutputIDs(workspace ws.Workspace) []string {
	outputIDs := make([]string, 0, len(workspace.Documents))
	for _, document := range workspace.Documents {
		outputIDs = append(outputIDs, document.Output.ID)
	}
	return outputIDs
}

func testCompiledOutputSchemas(workspace ws.Workspace) map[string][]receiptmodel.ReceiptCompiledOutputColumn {
	schemas := make(map[string][]receiptmodel.ReceiptCompiledOutputColumn, len(workspace.Documents))
	for _, document := range workspace.Documents {
		columns := make([]receiptmodel.ReceiptCompiledOutputColumn, 0, len(document.Columns))
		for _, column := range document.Columns {
			columns = append(columns, receiptmodel.ReceiptCompiledOutputColumn{
				ID: column.ColumnID, Name: column.Column, Label: column.Label,
				LogicalType: column.LogicalType, Cardinality: "optional_one",
			})
		}
		schemas[document.Output.ID] = columns
	}
	return schemas
}

func testOutputColumnProvenance(workspace ws.Workspace) map[string]map[string]string {
	provenance := make(map[string]map[string]string, len(workspace.Documents))
	for _, document := range workspace.Documents {
		columns := make(map[string]string, len(document.Columns))
		for _, column := range document.Columns {
			columns[column.Column] = "EXPLICIT"
		}
		provenance[document.Output.ID] = columns
	}
	return provenance
}

func TestWorkspaceOutputClosureIncludesTransitiveInputsButExcludesConsumers(t *testing.T) {
	document := func(outputID string, inputs ...string) ws.Document {
		value := ws.Document{Output: ws.Output{ID: outputID, Title: outputID}, Rows: ws.RecordsRowDefinition()}
		if len(inputs) == 0 {
			return value
		}
		refs := make([]ws.ConstructionInputRef, 0, len(inputs))
		for _, input := range inputs {
			refs = append(refs, ws.ConstructionInputRef{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: input})
		}
		value.Construction = &ws.Construction{Steps: []ws.ConstructionStep{{Inputs: refs}}}
		return value
	}
	workspace := ws.Workspace{
		Documents: []ws.Document{
			document("target", "upstream"), document("consumer", "target"), document("source"), document("upstream", "source"),
		},
		Tabs: []ws.Tab{
			{ID: "target", OutputID: "target", Order: 0}, {ID: "consumer", OutputID: "consumer", Order: 1},
			{ID: "source", OutputID: "source", Order: 2}, {ID: "upstream", OutputID: "upstream", Order: 3},
		},
		SharedFilters: map[string][]ws.SharedFilterBinding{
			"status": {{OutputID: "target", Column: "status"}, {OutputID: "consumer", Column: "status"}, {OutputID: "source", Column: "status"}},
		}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	projected, err := workspaceOutputClosure(workspace, "target")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, item := range projected.Documents {
		got = append(got, item.Output.ID)
	}
	if !reflect.DeepEqual(got, []string{"source", "upstream", "target"}) {
		t.Fatalf("dependency closure order=%#v", got)
	}
	if len(projected.Tabs) != 3 || projected.Tabs[0].Order != 0 || projected.Tabs[1].Order != 1 || projected.Tabs[2].Order != 2 {
		t.Fatalf("scoped tabs were not renumbered contiguously: %#v", projected.Tabs)
	}
	if len(projected.SharedFilters["status"]) != 2 || projected.SharedFilters["status"][0].OutputID != "target" || projected.SharedFilters["status"][1].OutputID != "source" {
		t.Fatalf("scoped shared-filter bindings=%#v", projected.SharedFilters)
	}
}

func TestSelectedReconcileValidatesFullWorkspaceCycleBeforeScoping(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	base := func(id string) ws.Document {
		return ws.Document{Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: id, Title: id}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}}
	}
	combine := func(dependency string) *ws.Construction {
		return &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "append", Inputs: []ws.ConstructionInputRef{
				{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: dependency},
				{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: "source"},
			},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine, Combine: &ws.ConstructionCombine{
				Kind:        ws.ConstructionCombineAppend,
				Projections: []ws.ConstructionCombineProjection{{OutputColumnID: "id", InputIndex: 0, InputColumnID: "id"}, {OutputColumnID: "id", InputIndex: 1, InputColumnID: "id"}},
			}}, Outputs: []ws.StageColumn{{ID: "id", Name: "id", Label: "ID", Type: "string", Nullable: true}},
		}}}
	}
	a, b, source := base("a"), base("b"), base("source")
	a.Construction, b.Construction = combine("b"), combine("a")
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Cycle"},
		Documents: []ws.Document{a, b, source},
		Tabs:      []ws.Tab{{ID: "a", Title: "a", OutputID: "a", Order: 0}, {ID: "b", Title: "b", OutputID: "b", Order: 1}, {ID: "source", Title: "source", OutputID: "source", Order: 2}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	if err := workspace.Validate(); err == nil || !strings.Contains(err.Error(), "WORKSPACE_OUTPUT_CYCLE") {
		t.Fatalf("full workspace validation error = %v, want WORKSPACE_OUTPUT_CYCLE", err)
	}
	if _, err := workspace.Digest(); err == nil || !strings.Contains(err.Error(), "WORKSPACE_OUTPUT_CYCLE") {
		t.Fatalf("cyclic workspace digest error = %v, want WORKSPACE_OUTPUT_CYCLE", err)
	}
	draft, err := json.Marshal(workspace)
	if err != nil {
		t.Fatal(err)
	}
	digest := "sha256:cyclic-workspace"
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 2, DraftDigest: digest}}
	config := testConfig(snapshot)
	compileCalls := 0
	config.CompileReceipt = func(context.Context, ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compileCalls++
		return nil, errors.New("compile must not run for invalid full state")
	}
	service := newTestService(t, store, config)
	_, err = service.ReconcileWithWorkspace(context.Background(), ReconcileRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 2, DraftDigest: digest, OutputID: "source"})
	if lifecycleErrorCode(err) != "AUTHORING_STATE_MISSING" {
		t.Fatalf("selected Reconcile error = %v, want invalid saved state rejection", err)
	}
	if compileCalls != 0 {
		t.Fatalf("compiled invalid full workspace %d times", compileCalls)
	}
	_, err = service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 2, ExpectedDraftDigest: digest,
		OutputID: "source", StageID: recipe.ConstructionSourceProjectionID,
	})
	if lifecycleErrorCode(err) != "AUTHORING_STATE_MISSING" {
		t.Fatalf("cycle construction capabilities error = %v, want invalid-workspace rejection", err)
	}
	if compileCalls != 0 {
		t.Fatalf("construction capabilities compiled cyclic workspace %d times", compileCalls)
	}
}

func TestConstructionCapabilitiesValidateFullStateBeforeScopedCompile(t *testing.T) {
	setup := constructionProposalSetup(t)
	store, snapshot := setup.Store, setup.Snapshot
	workspace, err := ws.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != store.created.DraftDigest {
		t.Fatalf("stored draft digest = %q, workspace digest = %q", store.created.DraftDigest, digest)
	}
	setup.Config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot {
		return ws.CatalogSnapshot{}
	}
	compileCalls := 0
	compile := setup.Config.CompileReceipt
	setup.Config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compileCalls++
		return compile(ctx, request)
	}
	service := newTestServiceFromSetup(t, setup)
	_, err = service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: digest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if lifecycleErrorCode(err) != "INVALID_AUTHORING_INTENT" {
		t.Fatalf("invalid full builder state error = %v, want INVALID_AUTHORING_INTENT", err)
	}
	if compileCalls != 0 {
		t.Fatalf("compiled invalid full builder state %d times", compileCalls)
	}
}

func TestAssessRowChangeCompilesExactCandidateWithoutSavingDraft(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion,
		Kind:       ws.WorkspaceKind,
		Explorer:   ws.ExplorerMetadata{Title: "Patients"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:   ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient", Children: []ws.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}, Rows: ws.RecordsRowDefinition(),
		}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}}, SemanticsVersion: ws.CurrentSemanticsVersion,
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 7, DraftDigest: digest}}
	config := testConfig(snapshot)
	var compileCalls int
	var previewCalls int
	var previewReceiptID string
	var compiledWorkspace ws.Workspace
	config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if receipt.ID != previewReceiptID {
			t.Fatalf("preview receipt ID=%q, want %q", receipt.ID, previewReceiptID)
		}
		return dataframeexecution.PreviewSummary{Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compileCalls++
		compiledWorkspace = request.Workspace
		receipt := nativeReceipt(snapshot)
		var receiptErr error
		receipt.IntentDigest, receiptErr = request.Workspace.Digest()
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipt.NormalizedBundle, receiptErr = request.Workspace.CanonicalJSON()
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipt.CompilationKey, receiptErr = receiptmodel.CompilationKey(*receipt)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipt.ID, receiptErr = receiptmodel.ReceiptID(*receipt)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		if err := receipt.Validate(); err != nil {
			t.Fatal(err)
		}
		store.receipt = receipt
		return receipt, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot {
		return ws.CatalogSnapshot{
			APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "patients",
			SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
			RoutePolicy: ws.RoutePolicy{Unbounded: true},
			Nodes: []ws.CatalogNode{
				{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
				{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
				{ID: "report", ResourceType: "DiagnosticReport", RowRootEligible: true},
			},
			Candidates: []ws.CatalogCandidate{{
				ID: "report-id", NodeID: "report", FieldPath: "id", Cardinality: "optional_one", Label: "Report ID", LogicalType: "string",
				ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE",
				ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "report-id", "report", "DiagnosticReport", "id", "optional_one", capability.ProjectionScalar),
			}},
			Edges: []ws.CatalogEdge{
				{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
				{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"},
			},
		}
	}
	service := newTestService(t, store, config)
	result, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DraftVersion != 7 || result.DraftDigest != digest || result.Assessment.Status != authoringv2.RowChangeReady || result.Assessment.Proposal == nil || result.CandidateReceiptID == "" {
		t.Fatalf("row change result=%#v", result)
	}
	if result.CandidateDocument == nil || result.CandidateNetwork == nil || result.CandidateRowIdentity == nil || result.CandidateDocument.RootResourceType != "Encounter" || result.CandidateDocument.Columns[0].OccurrenceID != "encounter" || result.CandidateNetwork.OutputID != "patients" || result.CandidateNetwork.Owner != "DOCUMENT" || len(result.CandidateNetwork.Bindings) != 0 || result.CandidateDocument.Columns[0].ColumnID != "" || result.CandidateRowIdentity.Stages[0].Identity.ResourceType != "Encounter" {
		t.Fatalf("READY root change did not expose its exact receipt candidate: %#v", result)
	}
	verifiedRoot, decodeErr := ws.DecodeWorkspace(store.receipt.NormalizedBundle)
	if decodeErr != nil || !reflect.DeepEqual(*result.CandidateDocument, verifiedRoot.Documents[0]) {
		t.Fatal("root projection differs from the preview receipt document")
	}
	previewReceiptID = result.CandidateReceiptID
	if compileCalls != 1 || compiledWorkspace.Documents[0].RootResourceType != "Encounter" || compiledWorkspace.Documents[0].Route.ResourceType != "Encounter" {
		t.Fatalf("candidate compile calls=%d workspace=%#v", compileCalls, compiledWorkspace)
	}
	preview, err := service.Preview(context.Background(), authoringcore.PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: result.CandidateReceiptID, OutputID: "patients",
		SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	if err != nil {
		t.Fatalf("preview compiled row-change candidate: %v", err)
	}
	if previewCalls != 1 || preview.Summary.RowCount != 1 {
		t.Fatalf("preview calls=%d summary=%#v", previewCalls, preview.Summary)
	}
	if store.created.DraftVersion != 7 || store.created.DraftDigest != digest || string(store.created.DraftConfig) != string(draft) {
		t.Fatalf("assessment wrote draft=%#v", store.created)
	}
	blocked, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter", RouteRebase: []authoringv2.RouteRebaseChoice{{OccurrenceID: ws.RootOccurrenceID, EdgeID: "missing-edge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Assessment.Status != authoringv2.RowChangeBlocked || blocked.CandidateReceiptID != "" || blocked.CandidateDocument != nil || blocked.CandidateNetwork != nil || blocked.CandidateRowIdentity != nil || compileCalls != 1 {
		t.Fatalf("blocked assessment compiled a receipt: result=%#v calls=%d", blocked, compileCalls)
	}
	noChange, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	if noChange.Assessment.Status != authoringv2.RowChangeNoChange || noChange.CandidateReceiptID != "" || noChange.CandidateDocument != nil || noChange.CandidateNetwork != nil || noChange.CandidateRowIdentity != nil || compileCalls != 1 {
		t.Fatalf("no-change assessment compiled a receipt: result=%#v calls=%d", noChange, compileCalls)
	}

	_, err = service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 6, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft error=%v", err)
	}

	replacement, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", Mode: authoringv2.RowChangeModeReplace, SnapshotToken: snapshot.Token,
		DraftVersion: 7, DraftDigest: digest, OutputID: "patients", RootNodeID: "report",
	})
	if err != nil || replacement.Assessment.Status != authoringv2.RowChangeReady || replacement.CandidateReceiptID == "" || replacement.Assessment.Proposal == nil {
		t.Fatalf("disconnected replacement assessment=%#v err=%v", replacement, err)
	}
	if replacement.Assessment.Mode != authoringv2.RowChangeModeReplace || len(replacement.Assessment.RemovedFeatureKeys) == 0 {
		t.Fatalf("replacement effect preview=%#v", replacement.Assessment)
	}
	if replacement.CandidateDocument == nil || replacement.CandidateNetwork == nil || replacement.CandidateRowIdentity == nil || replacement.CandidateDocument.RootResourceType != "DiagnosticReport" || replacement.CandidateDocument.ImportedRecipe != nil || replacement.CandidateDocument.ImportedSource != nil || len(replacement.CandidateDocument.Columns) != 1 || replacement.CandidateDocument.Columns[0].Column != "id" || replacement.CandidateNetwork.Owner != "DOCUMENT" || replacement.CandidateRowIdentity.Stages[0].Identity.ResourceType != "DiagnosticReport" {
		t.Fatalf("replacement projection retained the old source instead of its new document: %#v", replacement)
	}
	previewReceiptID = replacement.CandidateReceiptID
	if _, err := service.Preview(context.Background(), authoringcore.PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: replacement.CandidateReceiptID, OutputID: "patients",
		SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	}); err != nil {
		t.Fatalf("preview replacement candidate: %v", err)
	}
	if store.created.DraftVersion != 7 || store.created.DraftDigest != digest || string(store.created.DraftConfig) != string(draft) {
		t.Fatalf("assess/preview applied replacement before confirmation: %#v", store.created)
	}
	apply, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-replacement", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 7, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyTableRootRebase, OutputID: "patients", Payload: &authoringv2.RowPayload{RowChange: replacement.Assessment.Proposal}}},
	}, "alice")
	if err != nil {
		t.Fatalf("apply replacement: %v", err)
	}
	replaced := apply.Workspace.Documents[0]
	if replaced.Output.ID != "patients" || replaced.Output.Title != "Patients" || replaced.RootResourceType != "DiagnosticReport" ||
		len(replaced.Columns) != 1 || replaced.Columns[0].Column != "id" || apply.Workspace.Tabs[0].ID != "patients" {
		t.Fatalf("replacement changed table identity or retained old setup: %#v", apply.Workspace)
	}
	seed := replaced.Columns[0]
	if seed.OccurrenceID != ws.RootOccurrenceID || seed.Source.Kind != ws.SourceField || seed.Source.Field == nil ||
		seed.Source.Field.Path != "id" || seed.Source.Field.ProjectionMode != "VALUE" || seed.Table == nil ||
		seed.Table.Visible == nil || !*seed.Table.Visible || seed.Table.Order == nil || *seed.Table.Order != 0 {
		t.Fatalf("replacement ID seed is not a visible root VALUE column at order zero: %#v", seed)
	}
}

func TestAssessRowChangeRebasesRelatedSourceForSelectedCompilerRootRoute(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "row-change-related-source", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	nodeSet := []capability.Node{
		{ID: "condition", ResourceType: "Condition", RowRootEligible: true, Populated: true},
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true, Populated: true},
		{ID: "patient-alt", ResourceType: "Patient", RowRootEligible: false, Populated: true},
		{ID: "specimen", ResourceType: "Specimen", RowRootEligible: true, Populated: true},
	}
	edgeSet := []capability.Edge{
		{ID: "condition-patient", FromNodeID: "condition", ToNodeID: "patient", Label: "subject_Patient", StorageDirection: "OUTBOUND", SourceResourceType: "Condition", TargetResourceType: "Patient"},
		{ID: "patient-condition", FromNodeID: "patient", ToNodeID: "condition", Label: "subject_Patient", StorageDirection: "INBOUND", SourceResourceType: "Patient", TargetResourceType: "Condition"},
		{ID: "patient-specimen", FromNodeID: "patient", ToNodeID: "specimen", Label: "subject_Patient", StorageDirection: "INBOUND", SourceResourceType: "Patient", TargetResourceType: "Specimen"},
		{ID: "specimen-patient", FromNodeID: "specimen", ToNodeID: "patient", Label: "subject_Patient", StorageDirection: "OUTBOUND", SourceResourceType: "Specimen", TargetResourceType: "Patient"},
		{ID: "condition-patient-alt", FromNodeID: "condition", ToNodeID: "patient-alt", Label: "subject_Patient", StorageDirection: "OUTBOUND", SourceResourceType: "Condition", TargetResourceType: "Patient"},
		{ID: "patient-alt-condition", FromNodeID: "patient-alt", ToNodeID: "condition", Label: "subject_Patient", StorageDirection: "INBOUND", SourceResourceType: "Patient", TargetResourceType: "Condition"},
		{ID: "patient-alt-specimen", FromNodeID: "patient-alt", ToNodeID: "specimen", Label: "subject_Patient", StorageDirection: "INBOUND", SourceResourceType: "Patient", TargetResourceType: "Specimen"},
		{ID: "specimen-patient-alt", FromNodeID: "specimen", ToNodeID: "patient-alt", Label: "subject_Patient", StorageDirection: "OUTBOUND", SourceResourceType: "Specimen", TargetResourceType: "Patient"},
	}
	candidateSet := []capability.Candidate{
		{ID: "condition-id", NodeID: "condition", ResourceType: "Condition", FieldPath: "id", Label: "Condition ID", LogicalType: "string", Cardinality: "required_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true},
		{ID: "patient-id", NodeID: "patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true},
	}
	snapshot.Nodes = nodeSet
	snapshot.Edges = edgeSet
	snapshot.Candidates = candidateSet
	snapshot.Policy.Route = capability.RoutePolicy{}

	relatedRoute := []capability.ConstructionRouteStep{{
		EdgeID: "condition-patient", FromNodeID: "condition", ToNodeID: "patient",
		FromResourceType: "Condition", ToResourceType: "Patient", Relationship: "subject_Patient",
		StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
	}}
	choice, err := capability.NewFieldConstructionChoiceForRoute(snapshot.Token, relatedRoute, candidateSet[1])
	if err != nil {
		t.Fatal(err)
	}
	rootColumn := ws.Column{
		ColumnID: "condition-id-column", Column: "condition_id", Label: "Condition ID", LogicalType: "string",
		OccurrenceID: ws.RootOccurrenceID,
		Source:       ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}
	related := ws.ConstructionRelatedSource{
		AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, SourceOccurrenceID: "patient",
		Source: ws.ConstructionRelatedFieldSource{
			Kind: capability.ConstructionChoiceSourceField, CandidateID: candidateSet[1].ID,
			NodeID: candidateSet[1].NodeID, ResourceType: candidateSet[1].ResourceType,
			Path: candidateSet[1].FieldPath, Cardinality: candidateSet[1].Cardinality,
			LogicalType: candidateSet[1].LogicalType,
		},
		Route: relatedRoute, ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
		Form: capability.ConstructionChoiceAll, RowValuePolicy: ws.ConstructionRowValueOne,
		OutputColumnID: "related-patient-id",
	}
	rootStageColumn := ws.StageColumn{ID: rootColumn.ColumnID, Name: rootColumn.Column, Label: rootColumn.Label, Type: rootColumn.LogicalType}
	relatedStageColumn := ws.StageColumn{ID: related.OutputColumnID, Name: "related_patient_id", Label: "Related Patient ID", Type: "string"}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, SemanticsVersion: ws.CurrentSemanticsVersion,
		Kind: ws.WorkspaceKind, Explorer: ws.ExplorerMetadata{Title: "Conditions"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Conditions"},
			RootResourceType: "Condition", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Condition"},
			Rows: ws.RecordsRowDefinition(), Columns: []ws.Column{rootColumn},
			Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
				ID: "related-patient-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedSource, RelatedSource: &related},
				Outputs:   []ws.StageColumn{rootStageColumn, relatedStageColumn},
			}}},
		}},
		Tabs: []ws.Tab{{ID: "patients", Title: "Conditions", OutputID: "patients", Order: 0, Visible: true}},
	}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate related-source source workspace: %v", err)
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
		Project: "project-a", ExplorerID: "conditions", DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}, copyGet: true}
	config := testConfig(snapshot)
	config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Columns: []string{"condition_id", "related_patient_id"}, RowCount: 1, Complete: true}, nil
	}
	config.Capability.Catalog = func(got capability.Snapshot, explorerID string) ws.CatalogSnapshot {
		catalog := ws.CatalogSnapshot{
			APIVersion: ws.APIVersion, Kind: ws.CatalogKind,
			Project: "project-a", ExplorerID: explorerID, SourceGeneration: got.Identity.Generation,
			AuthorizationScopeDigest: got.Identity.AuthorizationScopeDigest, SnapshotToken: got.Token,
			Complete: true, RoutePolicy: ws.RoutePolicy{Unbounded: true},
		}
		for _, node := range got.Nodes {
			catalog.Nodes = append(catalog.Nodes, ws.CatalogNode{
				ID: node.ID, ResourceType: node.ResourceType, RowRootEligible: node.RowRootEligible, Populated: node.Populated,
			})
		}
		for _, edge := range got.Edges {
			catalog.Edges = append(catalog.Edges, ws.CatalogEdge{
				ID: edge.ID, FromNodeID: edge.FromNodeID, ToNodeID: edge.ToNodeID,
				Label: edge.Label, StorageDirection: edge.StorageDirection,
			})
		}
		for _, candidate := range got.Candidates {
			candidateChoice, choiceErr := capability.NewFieldConstructionChoice(got.Token, candidate)
			if choiceErr != nil {
				t.Fatal(choiceErr)
			}
			projectionModes := make([]string, 0, len(candidate.ProjectionModes))
			for _, mode := range candidate.ProjectionModes {
				switch mode {
				case capability.ProjectionScalar:
					projectionModes = append(projectionModes, "VALUE")
				case capability.ProjectionIndexed:
					projectionModes = append(projectionModes, "INDEXED")
				case capability.ProjectionFirst:
					projectionModes = append(projectionModes, "FIRST")
				case capability.ProjectionArray:
					projectionModes = append(projectionModes, "ALL")
				case capability.ProjectionDistinctArray:
					projectionModes = append(projectionModes, "DISTINCT")
				default:
					projectionModes = append(projectionModes, string(mode))
				}
			}
			catalog.Candidates = append(catalog.Candidates, ws.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath,
				Label: candidate.Label, LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				ProjectionModes: projectionModes, DefaultProjectionMode: "VALUE", ConstructionChoice: &candidateChoice,
			})
		}
		return catalog
	}
	compileCandidate := func(candidateWorkspace ws.Workspace) (explorercompilation.WorkspaceResult, error) {
		compiled, compileErr := explorercompilation.CompileWorkspace(context.Background(), "project-a", "conditions", candidateWorkspace, snapshot, explorercompilation.ResolvedInputs{})
		if compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		plan, compileErr := recipeplan.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
		if compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		resolved, compileErr := recipeplan.ResolveRecipePlan(plan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
		if compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		if _, compileErr := facade.CompileResolvedRecipePlan(resolved, facade.DefaultPhysicalOptimizationPolicy()); compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		return compiled, nil
	}
	var compiledWorkspace ws.Workspace
	config.CompileReceipt = func(_ context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		compiledWorkspace = request.Workspace
		compiled, compileErr := compileCandidate(request.Workspace)
		if compileErr != nil {
			t.Logf("candidate route before compile failure: %#v", request.Workspace.Documents[0].Route)
			t.Logf("candidate related source before compile failure: %#v", request.Workspace.Documents[0].Construction.Steps[0].Operation.RelatedSource)
			return nil, compileErr
		}
		receipt := nativeReceipt(snapshot)
		receipt.ExplorerID = "conditions"
		receipt.IntentDigest, compileErr = request.Workspace.Digest()
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.NormalizedBundle, compileErr = request.Workspace.CanonicalJSON()
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.Bundle = compiled.Bundle
		receipt.RecipeDigest = compiled.RecipeDigest
		receipt.ResolvedRecipeDigest = compiled.RecipeDigest
		receipt.EmittedColumns = compiled.EmittedColumns
		receipt.CompiledOutputSchemas = testCompiledOutputSchemas(request.Workspace)
		receipt.OutputColumnProvenance = testOutputColumnProvenance(request.Workspace)
		contracts, marshalErr := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: compiled.OutputContracts})
		if marshalErr != nil {
			return nil, marshalErr
		}
		receipt.PublicOutputContract = contracts
		receipt.OutputContractDigest, compileErr = receiptmodel.CompilationArtifactDigest(contracts)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.CompilationKey, compileErr = receiptmodel.CompilationKey(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.ID, compileErr = receiptmodel.ReceiptID(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		if compileErr = receipt.Validate(); compileErr != nil {
			return nil, compileErr
		}
		store.receipt = receipt
		return receipt, nil
	}
	service := newTestService(t, store, config)
	initial, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "conditions", SnapshotToken: snapshot.Token,
		DraftVersion: 1, DraftDigest: digest, OutputID: "patients", RootNodeID: "specimen",
	})
	if err != nil || initial.Assessment.Status != authoringv2.RowChangeBlocked || len(initial.Assessment.Unresolved) != 1 ||
		initial.Assessment.Unresolved[0].Code != "AMBIGUOUS_ROW_ROOT_OCCURRENCE" || len(initial.Assessment.Unresolved[0].Alternatives) != 2 {
		t.Fatalf("initial compiler-root assessment=%#v err=%v", initial, err)
	}
	var selectedRoute string
	for _, alternative := range initial.Assessment.Unresolved[0].Alternatives {
		const prefix = "row-change-route:v1:"
		if !strings.HasPrefix(alternative, prefix) {
			continue
		}
		payload, decodeErr := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(alternative, prefix))
		if decodeErr != nil {
			t.Fatalf("decode compiler route %q: %v", alternative, decodeErr)
		}
		var route struct {
			EdgeIDs []string `json:"edgeIds"`
		}
		if decodeErr := json.Unmarshal(payload, &route); decodeErr != nil {
			t.Fatalf("unmarshal compiler route %q: %v", alternative, decodeErr)
		}
		if reflect.DeepEqual(route.EdgeIDs, []string{"condition-patient", "patient-specimen"}) {
			selectedRoute = alternative
		}
	}
	if selectedRoute == "" {
		t.Fatalf("assessment did not offer the exact route through the saved Patient source occurrence: %#v", initial.Assessment.Unresolved[0].Alternatives)
	}
	rootPatient, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "conditions", SnapshotToken: snapshot.Token,
		DraftVersion: 1, DraftDigest: digest, OutputID: "patients", RootNodeID: "patient",
	})
	if err != nil || rootPatient.Assessment.Status != authoringv2.RowChangeReady || rootPatient.Assessment.Proposal == nil || rootPatient.CandidateReceiptID == "" {
		t.Fatalf("root change to saved related-source terminal=%#v err=%v", rootPatient, err)
	}
	rootPatientRoute := compiledWorkspace.Documents[0].Construction.Steps[0].Operation.RelatedSource.Route
	if len(rootPatientRoute) != 2 || rootPatientRoute[0].EdgeID != "patient-condition" || rootPatientRoute[1].EdgeID != "condition-patient" {
		t.Fatalf("root-at-source candidate route=%#v, want Patient→Condition→Patient", rootPatientRoute)
	}

	selected, err := service.AssessRowChange(context.Background(), rowdef.AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "conditions", SnapshotToken: snapshot.Token,
		DraftVersion: 1, DraftDigest: digest, OutputID: "patients", RootNodeID: "specimen",
		RootOccurrenceID: selectedRoute,
	})
	if err != nil || selected.Assessment.Status != authoringv2.RowChangeReady || selected.Assessment.Proposal == nil || selected.CandidateReceiptID == "" {
		t.Fatalf("selected compiler-root assessment=%#v err=%v", selected, err)
	}
	if compiledWorkspace.Documents[0].RootResourceType != "Specimen" {
		t.Fatalf("candidate receipt compiled root %q, want Specimen", compiledWorkspace.Documents[0].RootResourceType)
	}
	gotRelated := compiledWorkspace.Documents[0].Construction.Steps[0].Operation.RelatedSource
	if gotRelated == nil || len(gotRelated.Route) != 3 ||
		gotRelated.Route[0].FromNodeID != "specimen" || gotRelated.Route[0].ToNodeID != "patient" || gotRelated.Route[0].EdgeID != "specimen-patient" ||
		gotRelated.Route[1].FromNodeID != "patient" || gotRelated.Route[1].ToNodeID != "condition" || gotRelated.Route[1].EdgeID != "patient-condition" ||
		gotRelated.Route[2].FromNodeID != "condition" || gotRelated.Route[2].ToNodeID != "patient" || gotRelated.Route[2].EdgeID != "condition-patient" ||
		gotRelated.SourceOccurrenceID != "patient" {
		t.Fatalf("candidate related-source route=%#v, want Specimen→Patient→Condition→Patient to preserve the Condition source anchor", gotRelated)
	}
}

func TestAssessRowReplacementCandidateProjectsExplicitImportedDetachment(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "root-projection", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, Populated: true}, {ID: "study", ResourceType: "ResearchStudy", RowRootEligible: true, Populated: true}}
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{ID: "study-id", NodeID: "study", ResourceType: "ResearchStudy", FieldPath: "id", Label: "Study ID", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true})

	pin := ws.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Pinned patients"}, Documents: []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Pinned patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: "base", ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition(), ImportedRecipe: &pin}}, Tabs: []ws.Tab{{ID: "patients-tab", Title: "Patients", OutputID: "patients", Visible: true}}}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := workspace.Digest()
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: raw, DraftVersion: 1, DraftDigest: digest}, copyGet: true}
	config := testConfig(snapshot)
	config.Capability.Catalog = func(snapshot capability.Snapshot, id string) ws.CatalogSnapshot {
		c := lifecycleInterpretationCatalog(snapshot, id)
		c.Nodes = append(c.Nodes, ws.CatalogNode{ID: "study", ResourceType: "ResearchStudy", RowRootEligible: true})
		c.Candidates = append(c.Candidates, ws.CatalogCandidate{ID: "study-id", NodeID: "study", FieldPath: "id", Cardinality: "optional_one", Label: "Study ID", LogicalType: "string", ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "study-id", "study", "ResearchStudy", "id", "optional_one", capability.ProjectionScalar)})
		return c
	}
	config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt, err := compileTraceReceipt(ctx, request, snapshot, config.Capability.Catalog(snapshot, request.ExplorerID))
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}
	config.PreviewReceipt = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Complete: true}, nil
	}
	service := newTestService(t, store, config)
	request := rowdef.AssessRowChangeRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 1, DraftDigest: digest, OutputID: "patients", RootNodeID: "study"}
	blocked, err := service.AssessRowChange(context.Background(), request)
	if err != nil || blocked.Assessment.Status != authoringv2.RowChangeBlocked || blocked.CandidateDocument != nil || blocked.CandidateNetwork != nil || blocked.CandidateRowIdentity != nil {
		t.Fatalf("imported preserving root exposed a candidate: %#v error=%v", blocked, err)
	}
	request.Mode = authoringv2.RowChangeModeReplace
	result, err := service.AssessRowChange(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Assessment.Status != authoringv2.RowChangeReady || result.CandidateDocument == nil || result.CandidateNetwork == nil || result.CandidateRowIdentity == nil || result.CandidateDocument.ImportedRecipe != nil || result.CandidateDocument.ImportedSource != nil || result.CandidateDocument.RootResourceType != "ResearchStudy" || result.CandidateNetwork.Owner != "DOCUMENT" || result.CandidateRowIdentity.Stages[0].Identity.ResourceType != "ResearchStudy" {
		t.Fatalf("explicit detachment projection retained pin or old root: %#v", result)
	}
	if store.saveDraftCalls != 0 || string(store.created.DraftConfig) != string(raw) || *workspace.Documents[0].ImportedRecipe != pin {
		t.Fatal("assessing replacement detached or changed the saved pin")
	}
}

func TestLegacyCodeableConceptFrameCategoryEditKeepsPrimaryOnlyBinding(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"}}
	snapshot := readySnapshot("project-a", "generation-a", "legacy-frame-snapshot", scope)
	snapshot.Nodes = []capability.Node{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	observation := semanticFrameCompatibilityObservation("codeable concept")
	entry := semantic.Entry{ConceptID: "stage-concept", BindingID: "current-binding", Observation: observation}
	currentFamily := semanticFrameCompatibilityFamily(t, entry)
	priorFamily := currentFamily
	priorFamily.BindingID = semantic.LegacySemanticBindingIDV1(observation)
	priorFamily.SchemaVersion = semantic.ObservationSchemaVersion - 1
	priorFamily.RuleVersion = strconv.Itoa(semantic.SemanticObservationRuleVersion - 1)
	priorFamily.ValueFallbacks = nil
	priorFamily.FallbackOnBlankString = false
	frame, err := ws.NewFrameDefinition(
		"Stage values", "Stages selected from the existing primary text binding.", priorFamily, nil, capability.ConstructionChoiceValue,
	)
	if err != nil {
		t.Fatal(err)
	}

	store := legacyFrameCompatibilityStore(t, frame)
	primary := capability.Candidate{
		ID: "observation-stage-text", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.text", Label: "Stage text", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	fallback := capability.Candidate{
		ID: "observation-stage-display", NodeID: "observation", ResourceType: "Observation",
		FieldPath: "valueCodeableConcept.coding[].display", Label: "Stage display", LogicalType: "string", Cardinality: "many",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst},
	}
	snapshot.Candidates = []capability.Candidate{primary, fallback}
	fieldChoice, err := capability.NewFieldConstructionChoice(snapshot.Token, primary)
	if err != nil {
		t.Fatal(err)
	}
	catalogSnapshot := ws.CatalogSnapshot{
		APIVersion: ws.APIVersion, Kind: ws.CatalogKind, Project: "project-a", ExplorerID: "explorer",
		SourceGeneration: "generation-a", AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		SnapshotToken: snapshot.Token, Complete: true, Nodes: []ws.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}},
		Candidates: []ws.CatalogCandidate{{
			ID: primary.ID, NodeID: primary.NodeID, FieldPath: primary.FieldPath, Label: primary.Label, LogicalType: primary.LogicalType,
			Cardinality: primary.Cardinality, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &fieldChoice,
		}}, RoutePolicy: ws.RoutePolicy{Unbounded: true},
	}
	buildID := semantic.BuildID("project-a", "generation-a")
	unrelated := entry
	unrelated.ConceptID = "unrelated-concept"
	unrelated.BindingID = "unrelated-current-binding"
	unrelated.Observation.Source.Path = "anotherValue"
	unrelated.Observation.Source.Canonical = "Observation.anotherValue"
	resolver := func(_ context.Context, options semantic.ResolveOptions) (semantic.ResolveResult, error) {
		if options.Project != "project-a" || options.DatasetGeneration != "generation-a" ||
			options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
			!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) ||
			!reflect.DeepEqual(options.References, []semantic.Reference{{ConceptID: entry.ConceptID, BindingID: entry.BindingID}}) {
			t.Fatalf("category reauthorization changed project/generation/auth/selection scope: %#v", options)
		}
		return semanticAuthoringInventory(snapshot, []semantic.Entry{entry}), nil
	}
	service := newTestService(t, store, Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
			},
			Catalog: func(capability.Snapshot, string) ws.CatalogSnapshot { return catalogSnapshot },
		},
		SemanticInventory: func(_ context.Context, options semantic.PageOptions) (semantic.Page, error) {
			if options.Project != "project-a" || options.DatasetGeneration != "generation-a" ||
				options.AuthResourcePathsUnrestricted == nil || *options.AuthResourcePathsUnrestricted ||
				!reflect.DeepEqual(options.AuthResourcePaths, scope.AuthResourcePaths) || options.ResourceType != "Observation" || options.BindingID != "" {
				t.Fatalf("frame browse lost its exact project/generation/auth/binding scope: %#v", options)
			}
			return semantic.Page{
				State:   semantic.Complete,
				Build:   semantic.Build{BuildID: buildID, State: semantic.Complete},
				Entries: []semantic.Entry{entry, unrelated},
			}, nil
		},
		ResolveSemanticInventorySelections: resolver,
	})

	browsed, err := service.BrowseSemanticInventory(context.Background(), semanticbrowse.BrowseSemanticInventoryRequest{
		Project: "project-a", ExplorerID: "explorer", SnapshotToken: snapshot.Token, RowRoot: "Observation",
		OutputID: "out", FrameID: frame.ID,
	})
	if err != nil || browsed.FrameID != frame.ID || len(browsed.Entries) != 1 || browsed.Entries[0].ConstructionChoice == nil {
		t.Fatalf("legacy category browse = %#v, err=%v", browsed, err)
	}
	choiceSource, ok := browsed.Entries[0].ConstructionChoice.Source.(capability.SemanticBindingChoiceSource)
	if !ok || browsed.Entries[0].ValueSelector != observation.Value.Selector || choiceSource.ValueSelector != observation.Value.Selector ||
		choiceSource.SchemaVersion != semantic.ObservationSchemaVersion || choiceSource.RuleVersion != strconv.Itoa(semantic.SemanticObservationRuleVersion) ||
		len(choiceSource.ValueFallbacks) != 0 || choiceSource.FallbackOnBlankString {
		t.Fatalf("legacy category choice acquired current fallback behavior: item=%#v source=%#v", browsed.Entries[0], browsed.Entries[0].ConstructionChoice.Source)
	}

	request := authoringv2.ApplyCommandsRequest{
		CommandID: "legacy-frame-category", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionChoice, OutputID: "out", InitialPresentation: authoringv2.InitialPresentationTable, Payload: &authoringv2.ConstructionChoicePayload{ConstructionChoice: &authoringv2.ConstructionChoiceSelection{ChoiceID: browsed.Entries[0].ConstructionChoice.ChoiceID, Form: capability.ConstructionChoiceValue, FrameID: frame.ID}}}},
	}
	response, err := service.ApplyCommands(context.Background(), "project-a", "explorer", request, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Workspace.Documents) != 1 || len(response.Workspace.Documents[0].Columns) != 1 {
		t.Fatalf("legacy category edit produced workspace=%#v", response.Workspace)
	}
	document := response.Workspace.Documents[0]
	column := document.Columns[0]
	if column.FrameID != frame.ID || column.Source.Kind != ws.SourceCodedValue || column.Source.Lookup == nil || column.Source.Lookup.Binding == nil ||
		column.Source.Lookup.Binding.ValuePath != priorFamily.ValuePath || len(column.Source.Lookup.Binding.ValueFallback) != 0 || column.Source.Lookup.Binding.FallbackOnBlankString ||
		len(document.Frames) != 1 || document.Frames[0].ID != frame.ID || !reflect.DeepEqual(document.Frames[0].Source, priorFamily) {
		t.Fatalf("saved category changed the primary-only frame identity: frame=%#v column=%#v", document.Frames, column)
	}
}

func TestLegacyPrimaryOnlyFrameCompatibilityDoesNotChangeExtensionBindings(t *testing.T) {
	lookup := &ws.LookupSource{ProjectionMode: "FIRST", Extension: &fhirbinding.ExtensionBinding{
		OwnerPath: "extension[]", URLPath: []string{"urn:study:stage"}, ValuePath: "valueString", LogicalType: "string", ChoiceArms: []string{"valueString"},
	}}
	document := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "out", Title: "Output"}, RootResourceType: "Observation",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"}, Rows: ws.RecordsRowDefinition(),
		Columns: []ws.Column{{Column: "stage", Label: "Stage", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{
			Kind: ws.SourceExtensionByURL, Lookup: lookup,
		}}},
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Builder"}, Documents: []ws.Document{document},
		Tabs: []ws.Tab{{ID: "tab-out", Title: "Output", OutputID: "out", Visible: true}},
	}
	beforeSource, err := json.Marshal(document.Columns[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ws.DecodeWorkspace(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Documents) != 1 || len(reloaded.Documents[0].Columns) != 1 {
		t.Fatalf("existing Extension binding did not reload: %#v", reloaded.Documents)
	}
	afterSource, err := json.Marshal(reloaded.Documents[0].Columns[0].Source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterSource, beforeSource) {
		t.Fatalf("existing Extension binding wire identity changed across canonical reload: before=%s after=%s", beforeSource, afterSource)
	}
}

func semanticFrameCompatibilityObservation(kind string) semantic.Observation {
	return constructionfixtures.SemanticFrameCompatibilityObservation(kind)
}

func semanticFrameCompatibilityFamily(t *testing.T, entry semantic.Entry) capability.SemanticFrameFamily {
	return constructionfixtures.SemanticFrameCompatibilityFamily(t, entry)
}

func legacyFrameCompatibilityStore(t *testing.T, frame ws.FrameDefinition) *fakeStore {
	t.Helper()
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Builder"},
		Documents: []ws.Document{{
			Kind: ws.Kind, Output: ws.Output{ID: "out", Title: "Observations"}, RootResourceType: "Observation",
			Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Observation"},
			Rows:  ws.RecordsRowDefinition(), Columns: []ws.Column{}, Frames: []ws.FrameDefinition{frame},
		}},
		Tabs: []ws.Tab{{ID: "tab-out", Title: "Observations", OutputID: "out", Visible: true}},
	}
	config, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return &fakeStore{created: &explorer.Explorer{
		Project: "project-a", ExplorerID: "explorer", Title: "Builder", ManagementMode: viewer.ManagementInteractive,
		DraftConfig: config, DraftVersion: 1, DraftDigest: digest,
	}}
}

func TestCellTraceValidatesReceiptAndReturnsBoundEvidence(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	store := &fakeStore{receipt: receipt}
	config := testConfig(snapshot)
	config.CellTrace = func(_ context.Context, gotReceipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		if gotReceipt.ID != receipt.ID || bindings.DatasetGeneration != receipt.SourceGeneration || request.Output != "patients" || request.Column != "patient_id" || request.RowID != "row-7" {
			t.Fatalf("unexpected trace invocation: receipt=%#v bindings=%#v request=%#v", gotReceipt, bindings, request)
		}
		return dataframeexecution.CellTraceResult{RowID: request.RowID, Column: request.Column, Value: "Patient/7", Status: dataframeexecution.CellTraceValue, Complete: true}, nil
	}
	service := newTestService(t, store, config)
	result, err := service.CellTrace(context.Background(), CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7", Column: "patient_id", Limit: 25})
	if err != nil {
		t.Fatal(err)
	}
	if result.Binding.ReceiptID != receipt.ID || result.Binding.ScopeDigest != receipt.AuthorizationScopeDigest || result.Trace.Value != "Patient/7" || !result.Trace.Complete {
		t.Fatalf("unexpected result: %#v", result)
	}
	if result.Feature.OutputID != "patients" || result.Feature.Column != "patient_id" || result.Feature.AuthoredColumn != "patient_id" || result.Feature.OccurrenceID != "base" || result.Feature.Label != "Patient ID" {
		t.Fatalf("unexpected feature descriptor: %#v", result.Feature)
	}
}

func TestCellTraceMissingRowBecomesTypedNotFound(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.CellTrace = func(_ context.Context, _ *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		return dataframeexecution.CellTraceResult{}, dataframeerrors.Wrap(
			dataframeexecution.ErrCellTraceRowNotFound, dataframeerrors.CodeInvalidRequest,
			fmt.Sprintf("published row %q was not found in output %q", request.RowID, request.Output),
			dataframeerrors.WithDetails(map[string]any{"rowId": request.RowID, "output": request.Output}),
		)
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	_, err := service.CellTrace(context.Background(), CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-9", Column: "patient_id"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "PREVIEW_ROW_NOT_FOUND" || lifecycleErr.Class != ports.ClassNotFound {
		t.Fatalf("a mistyped rowId must present as a non-server-fault not-found, got %v (%T)", err, err)
	}
}

func TestCellTraceRejectsUnknownColumnBeforeExecution(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", unrestrictedScope())
	receipt := nativeReceipt(snapshot)
	config := testConfig(snapshot)
	config.CellTrace = func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
		t.Fatal("trace executor must not run")
		return dataframeexecution.CellTraceResult{}, nil
	}
	service := newTestService(t, &fakeStore{receipt: receipt}, config)
	_, err := service.CellTrace(context.Background(), CellTraceRequest{Project: "project-a", ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", RowID: "row-7", Column: "not_a_column"})
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "UNKNOWN_OUTPUT_COLUMN" {
		t.Fatalf("error = %v", err)
	}
}
