package lifecycle

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/fhir/schema"
)

func TestReplacementCompilesOnlyTargetAndLeavesBrokenConsumerForRepair(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	snapshot.Nodes = []capability.Node{
		{ID: "patient", ResourceType: "Patient", RowRootEligible: true, Populated: true},
		{ID: "research-study", ResourceType: "ResearchStudy", RowRootEligible: true, Populated: true},
	}
	for _, item := range []struct{ nodeID, resourceType string }{{"patient", "Patient"}, {"research-study", "ResearchStudy"}} {
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
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{
			{
				Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "consumer", Title: "Consumer"},
				RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
				Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
					ID: "copy_source", Inputs: []authoringv2.ConstructionInputRef{
						{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: "patients"},
						{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: "other"},
					},
					Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
						Kind: authoringv2.ConstructionCombineAppend,
						Projections: []authoringv2.ConstructionCombineProjection{
							{OutputColumnID: "copied", InputIndex: 0, InputColumnID: "patient-id-column"},
							{OutputColumnID: "copied", InputIndex: 1, InputColumnID: "other-id-column"},
						},
					}}, Outputs: []authoringv2.StageColumn{{ID: "copied", Name: "copied", Label: "Copied patient ID", Type: "string", Nullable: true}},
				}}},
			},
			{
				Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
				Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []authoringv2.Column{{ColumnID: "patient-id-column", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
					Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
			{
				Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: "other", Title: "Other"}, RootResourceType: "Patient",
				Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
				Columns: []authoringv2.Column{{ColumnID: "other-id-column", Column: "other_id", Label: "Other ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
					Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
			},
		},
		Tabs: []authoringv2.Tab{{ID: "consumer-tab", Title: "Consumer", OutputID: "consumer", Order: 0, Visible: true}, {ID: "patients-tab", Title: "Patients", OutputID: "patients", Order: 1, Visible: true}, {ID: "other-tab", Title: "Other", OutputID: "other", Order: 2, Visible: true}},
	}
	compile := func(ws authoringv2.Workspace) (explorercompilation.WorkspaceResult, error) {
		compiled, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "patients", ws, snapshot, explorercompilation.ResolvedInputs{})
		if err != nil {
			return explorercompilation.WorkspaceResult{}, err
		}
		plan, err := semantic.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
		if err != nil {
			return explorercompilation.WorkspaceResult{}, err
		}
		resolved, err := semantic.ResolveRecipePlan(plan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
		if err != nil {
			return explorercompilation.WorkspaceResult{}, err
		}
		if _, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
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
	canonicalWorkspace, err := authoringv2.DecodeWorkspace(draft)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &fakeStore{created: &explorer.Explorer{Project: "project-a", ExplorerID: "patients", DraftConfig: draft, DraftVersion: 4, DraftDigest: digest}}
	config := testConfig(snapshot)
	config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Columns: []string{"id"}, RowCount: 1, Complete: true}, nil
	}
	config.Capability.Catalog = func(got capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		catalog := authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: explorerID,
			SourceGeneration: got.Identity.Generation, AuthorizationScopeDigest: got.Identity.AuthorizationScopeDigest, ResolvedSchemaDigest: got.Identity.SchemaDigest,
			SnapshotToken: got.Token, Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
			Nodes: []authoringv2.CatalogNode{{ID: "patient", ResourceType: "Patient", RowRootEligible: true}, {ID: "research-study", ResourceType: "ResearchStudy", RowRootEligible: true}},
		}
		for _, candidate := range got.Candidates {
			choice, choiceErr := capability.NewFieldConstructionChoice(got.Token, candidate)
			if choiceErr != nil {
				t.Fatal(choiceErr)
			}
			catalog.Candidates = append(catalog.Candidates, authoringv2.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality, ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
			})
		}
		return catalog
	}
	var compiledWorkspaces []authoringv2.Workspace
	config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
		receipt.ConstructionStages = map[string][]explorer.ReceiptConstructionStage{
			"patients": testConstructionStageDescriptors(request.Workspace, "patients"),
		}
		contracts, marshalErr := json.Marshal(explorer.PublicOutputContracts{Outputs: compiled.OutputContracts})
		if marshalErr != nil {
			return nil, marshalErr
		}
		receipt.PublicOutputContract = contracts
		receipt.OutputContractDigest, compileErr = explorer.CompilationArtifactDigest(contracts)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.CompilationKey, compileErr = explorer.CompilationKey(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.ID, compileErr = explorer.ReceiptID(*receipt)
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
	replacement, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
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
		CommandID: "replace-root-with-consumer", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 4, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyTableRootRebase, OutputID: "patients", RowChange: replacement.Assessment.Proposal}},
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

	capabilities, err := service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
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
	selectedReconcile, err := service.ReconcileWithWorkspace(context.Background(), ReconcileRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token,
		DraftVersion: result.DraftVersion, DraftDigest: result.DraftDigest, OutputID: "patients",
	})
	if err != nil {
		t.Fatalf("selected replacement reconcile with stale consumer: %v", err)
	}
	if got := compiledOutputIDs(compiledWorkspaces[len(compiledWorkspaces)-1]); !reflect.DeepEqual(got, []string{"patients"}) {
		t.Fatalf("selected replacement reconcile scope = %#v, want target only", got)
	}
	if _, err := service.Preview(context.Background(), PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: selectedReconcile.Receipt.ID, OutputID: "patients",
		SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	}); err != nil {
		t.Fatalf("preview selected replacement with stale consumer: %v", err)
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
	repaired.Documents = append([]authoringv2.Document(nil), result.Workspace.Documents...)
	repaired.Documents[0] = canonicalWorkspace.Documents[0]
	step := repaired.Documents[0].Construction.Steps[0]
	selectedCompiled, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "patients", result.Workspace, snapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatalf("compile full replacement recipe before target-only preview selection: %v", err)
	}
	targetPlan, err := semantic.BuildRecipePlan(selectedCompiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
	if err != nil {
		t.Fatal(err)
	}
	targetResolved, err := semantic.ResolveRecipePlan(targetPlan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
	if err != nil {
		t.Fatal(err)
	}
	targetResolved.SemanticPlan.Bindings.OutputNames = []string{"patients"}
	targetLowered, err := lower.CompileResolvedRecipePlan(targetResolved, ir.DefaultPhysicalOptimizationPolicy())
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

func compiledOutputIDs(workspace authoringv2.Workspace) []string {
	outputIDs := make([]string, 0, len(workspace.Documents))
	for _, document := range workspace.Documents {
		outputIDs = append(outputIDs, document.Output.ID)
	}
	return outputIDs
}

func testCompiledOutputSchemas(workspace authoringv2.Workspace) map[string][]explorer.ReceiptCompiledOutputColumn {
	schemas := make(map[string][]explorer.ReceiptCompiledOutputColumn, len(workspace.Documents))
	for _, document := range workspace.Documents {
		columns := make([]explorer.ReceiptCompiledOutputColumn, 0, len(document.Columns))
		for _, column := range document.Columns {
			columns = append(columns, explorer.ReceiptCompiledOutputColumn{
				ID: column.ColumnID, Name: column.Column, Label: column.Label,
				LogicalType: column.LogicalType, Cardinality: "optional_one",
			})
		}
		schemas[document.Output.ID] = columns
	}
	return schemas
}

func testOutputColumnProvenance(workspace authoringv2.Workspace) map[string]map[string]string {
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
	document := func(outputID string, inputs ...string) authoringv2.Document {
		value := authoringv2.Document{Output: authoringv2.Output{ID: outputID, Title: outputID}}
		if len(inputs) == 0 {
			return value
		}
		refs := make([]authoringv2.ConstructionInputRef, 0, len(inputs))
		for _, input := range inputs {
			refs = append(refs, authoringv2.ConstructionInputRef{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: input})
		}
		value.Construction = &authoringv2.Construction{Steps: []authoringv2.ConstructionStep{{Inputs: refs}}}
		return value
	}
	workspace := authoringv2.Workspace{
		Documents: []authoringv2.Document{
			document("target", "upstream"), document("consumer", "target"), document("source"), document("upstream", "source"),
		},
		Tabs: []authoringv2.Tab{
			{ID: "target", OutputID: "target", Order: 0}, {ID: "consumer", OutputID: "consumer", Order: 1},
			{ID: "source", OutputID: "source", Order: 2}, {ID: "upstream", OutputID: "upstream", Order: 3},
		},
		SharedFilters: map[string][]authoringv2.SharedFilterBinding{
			"status": {{OutputID: "target", Column: "status"}, {OutputID: "consumer", Column: "status"}, {OutputID: "source", Column: "status"}},
		},
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
	base := func(id string) authoringv2.Document {
		return authoringv2.Document{Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(), Output: authoringv2.Output{ID: id, Title: id}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}}
	}
	combine := func(dependency string) *authoringv2.Construction {
		return &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: "append", Inputs: []authoringv2.ConstructionInputRef{
				{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: dependency},
				{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: "source"},
			},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
				Kind:        authoringv2.ConstructionCombineAppend,
				Projections: []authoringv2.ConstructionCombineProjection{{OutputColumnID: "id", InputIndex: 0, InputColumnID: "id"}, {OutputColumnID: "id", InputIndex: 1, InputColumnID: "id"}},
			}}, Outputs: []authoringv2.StageColumn{{ID: "id", Name: "id", Label: "ID", Type: "string", Nullable: true}},
		}}}
	}
	a, b, source := base("a"), base("b"), base("source")
	a.Construction, b.Construction = combine("b"), combine("a")
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Cycle"},
		Documents: []authoringv2.Document{a, b, source},
		Tabs:      []authoringv2.Tab{{ID: "a", Title: "a", OutputID: "a", Order: 0}, {ID: "b", Title: "b", OutputID: "b", Order: 1}, {ID: "source", Title: "source", OutputID: "source", Order: 2}},
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
	config.CompileReceipt = func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
	_, err = service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
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
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
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
	service.config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
		return authoringv2.CatalogSnapshot{}
	}
	compileCalls := 0
	compile := service.config.CompileReceipt
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		compileCalls++
		return compile(ctx, request)
	}
	_, err = service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
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
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion,
		Kind:       authoringv2.WorkspaceKind,
		Explorer:   authoringv2.ExplorerMetadata{Title: "Patients"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
			Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", Children: []authoringv2.RouteNode{{OccurrenceID: "encounter", ResourceType: "Encounter", Relationship: "encounters"}}},
			Columns: []authoringv2.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Visible: true}},
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
	var compiledWorkspace authoringv2.Workspace
	config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		previewCalls++
		if receipt.ID != previewReceiptID {
			t.Fatalf("preview receipt ID=%q, want %q", receipt.ID, previewReceiptID)
		}
		return dataframeexecution.PreviewSummary{Columns: []string{"patient_id"}, RowCount: 1, Complete: true}, nil
	}
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
		receipt.CompilationKey, receiptErr = explorer.CompilationKey(*receipt)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		receipt.ID, receiptErr = explorer.ReceiptID(*receipt)
		if receiptErr != nil {
			t.Fatal(receiptErr)
		}
		if err := receipt.Validate(); err != nil {
			t.Fatal(err)
		}
		store.receipt = receipt
		return receipt, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot {
		return authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind, Project: "project-a", ExplorerID: "patients",
			SourceGeneration: snapshot.Identity.Generation, AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SnapshotToken: snapshot.Token, Complete: true,
			RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
			Nodes: []authoringv2.CatalogNode{
				{ID: "patient", ResourceType: "Patient", RowRootEligible: true},
				{ID: "encounter", ResourceType: "Encounter", RowRootEligible: true},
				{ID: "report", ResourceType: "DiagnosticReport", RowRootEligible: true},
			},
			Candidates: []authoringv2.CatalogCandidate{{
				ID: "report-id", NodeID: "report", FieldPath: "id", Cardinality: "optional_one", Label: "Report ID", LogicalType: "string",
				ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE",
				ConstructionChoice: lifecycleTestFieldChoice(snapshot.Token, "report-id", "report", "DiagnosticReport", "id", "optional_one", capability.ProjectionScalar),
			}},
			Edges: []authoringv2.CatalogEdge{
				{ID: "patient-encounter", FromNodeID: "patient", ToNodeID: "encounter", Label: "encounters"},
				{ID: "encounter-patient", FromNodeID: "encounter", ToNodeID: "patient", Label: "patient"},
			},
		}
	}
	service := newTestService(t, store, config)
	result, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.DraftVersion != 7 || result.DraftDigest != digest || result.Assessment.Status != authoringv2.RowChangeReady || result.Assessment.Proposal == nil || result.CandidateReceiptID == "" {
		t.Fatalf("row change result=%#v", result)
	}
	previewReceiptID = result.CandidateReceiptID
	if compileCalls != 1 || compiledWorkspace.Documents[0].RootResourceType != "Encounter" || compiledWorkspace.Documents[0].Route.ResourceType != "Encounter" {
		t.Fatalf("candidate compile calls=%d workspace=%#v", compileCalls, compiledWorkspace)
	}
	preview, err := service.Preview(context.Background(), PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: result.CandidateReceiptID, OutputID: "patients",
		SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
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
	blocked, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter", RouteRebase: []authoringv2.RouteRebaseChoice{{OccurrenceID: authoringv2.RootOccurrenceID, EdgeID: "missing-edge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if blocked.Assessment.Status != authoringv2.RowChangeBlocked || blocked.CandidateReceiptID != "" || compileCalls != 1 {
		t.Fatalf("blocked assessment compiled a receipt: result=%#v calls=%d", blocked, compileCalls)
	}
	noChange, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 7, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "patient",
	})
	if err != nil {
		t.Fatal(err)
	}
	if noChange.Assessment.Status != authoringv2.RowChangeNoChange || noChange.CandidateReceiptID != "" || compileCalls != 1 {
		t.Fatalf("no-change assessment compiled a receipt: result=%#v calls=%d", noChange, compileCalls)
	}

	_, err = service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, DraftVersion: 6, DraftDigest: digest,
		OutputID: "patients", RootNodeID: "encounter",
	})
	var lifecycleErr *Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Code != "DRAFT_CONFLICT" {
		t.Fatalf("stale draft error=%v", err)
	}

	replacement, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "patients", Mode: authoringv2.RowChangeModeReplace, SnapshotToken: snapshot.Token,
		DraftVersion: 7, DraftDigest: digest, OutputID: "patients", RootNodeID: "report",
	})
	if err != nil || replacement.Assessment.Status != authoringv2.RowChangeReady || replacement.CandidateReceiptID == "" || replacement.Assessment.Proposal == nil {
		t.Fatalf("disconnected replacement assessment=%#v err=%v", replacement, err)
	}
	if replacement.Assessment.Mode != authoringv2.RowChangeModeReplace || len(replacement.Assessment.RemovedFeatureKeys) == 0 {
		t.Fatalf("replacement effect preview=%#v", replacement.Assessment)
	}
	previewReceiptID = replacement.CandidateReceiptID
	if _, err := service.Preview(context.Background(), PreviewRequest{
		Project: "project-a", ExplorerID: "patients", ReceiptID: replacement.CandidateReceiptID, OutputID: "patients",
		SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	}); err != nil {
		t.Fatalf("preview replacement candidate: %v", err)
	}
	if store.created.DraftVersion != 7 || store.created.DraftDigest != digest || string(store.created.DraftConfig) != string(draft) {
		t.Fatalf("assess/preview applied replacement before confirmation: %#v", store.created)
	}
	apply, err := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-replacement", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 7, ExpectedDraftDigest: digest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyTableRootRebase, OutputID: "patients", RowChange: replacement.Assessment.Proposal}},
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
	if seed.OccurrenceID != authoringv2.RootOccurrenceID || seed.Source.Kind != authoringv2.SourceField || seed.Source.Field == nil ||
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
		{ID: "patient-alt", ResourceType: "Patient", RowRootEligible: true, Populated: true},
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
	rootColumn := authoringv2.Column{
		ColumnID: "condition-id-column", Column: "condition_id", Label: "Condition ID", LogicalType: "string",
		OccurrenceID: authoringv2.RootOccurrenceID,
		Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}
	related := authoringv2.ConstructionRelatedSource{
		AnchorColumnID: "_key", ChoiceID: choice.ChoiceID, SourceOccurrenceID: "patient",
		Source: authoringv2.ConstructionRelatedFieldSource{
			Kind: capability.ConstructionChoiceSourceField, CandidateID: candidateSet[1].ID,
			NodeID: candidateSet[1].NodeID, ResourceType: candidateSet[1].ResourceType,
			Path: candidateSet[1].FieldPath, Cardinality: candidateSet[1].Cardinality,
			LogicalType: candidateSet[1].LogicalType,
		},
		Route: relatedRoute, ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
		Form: capability.ConstructionChoiceAll, RowValuePolicy: authoringv2.ConstructionRowValueOne,
		OutputColumnID: "related-patient-id",
	}
	rootStageColumn := authoringv2.StageColumn{ID: rootColumn.ColumnID, Name: rootColumn.Column, Label: rootColumn.Label, Type: rootColumn.LogicalType}
	relatedStageColumn := authoringv2.StageColumn{ID: related.OutputColumnID, Name: "related_patient_id", Label: "Related Patient ID", Type: "string"}
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Conditions"},
		Documents: []authoringv2.Document{{
			Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Conditions"},
			RootResourceType: "Condition", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Condition"},
			Rows: authoringv2.RecordsRowDefinition(), Columns: []authoringv2.Column{rootColumn},
			Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
				ID: "related-patient-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedSource, RelatedSource: &related},
				Outputs:   []authoringv2.StageColumn{rootStageColumn, relatedStageColumn},
			}}},
		}},
		Tabs: []authoringv2.Tab{{ID: "patients", Title: "Conditions", OutputID: "patients", Order: 0, Visible: true}},
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
	config.PreviewReceipt = func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		return dataframeexecution.PreviewSummary{Columns: []string{"condition_id", "related_patient_id"}, RowCount: 1, Complete: true}, nil
	}
	config.Capability.Catalog = func(got capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		catalog := authoringv2.CatalogSnapshot{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.CatalogKind,
			Project: "project-a", ExplorerID: explorerID, SourceGeneration: got.Identity.Generation,
			AuthorizationScopeDigest: got.Identity.AuthorizationScopeDigest, SnapshotToken: got.Token,
			Complete: true, RoutePolicy: authoringv2.RoutePolicy{Unbounded: true},
		}
		for _, node := range got.Nodes {
			catalog.Nodes = append(catalog.Nodes, authoringv2.CatalogNode{
				ID: node.ID, ResourceType: node.ResourceType, RowRootEligible: node.RowRootEligible, Populated: node.Populated,
			})
		}
		for _, edge := range got.Edges {
			catalog.Edges = append(catalog.Edges, authoringv2.CatalogEdge{
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
			catalog.Candidates = append(catalog.Candidates, authoringv2.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath,
				Label: candidate.Label, LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				ProjectionModes: projectionModes, DefaultProjectionMode: "VALUE", ConstructionChoice: &candidateChoice,
			})
		}
		return catalog
	}
	compileCandidate := func(candidateWorkspace authoringv2.Workspace) (explorercompilation.WorkspaceResult, error) {
		compiled, compileErr := explorercompilation.CompileWorkspace(context.Background(), "project-a", "conditions", candidateWorkspace, snapshot, explorercompilation.ResolvedInputs{})
		if compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		plan, compileErr := semantic.BuildRecipePlan(compiled.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
		if compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		resolved, compileErr := semantic.ResolveRecipePlan(plan, snapshot.Identity.AuthorizationScopeDigest, snapshot.Identity.Generation)
		if compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		if _, compileErr := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy()); compileErr != nil {
			return explorercompilation.WorkspaceResult{}, compileErr
		}
		return compiled, nil
	}
	var compiledWorkspace authoringv2.Workspace
	config.CompileReceipt = func(_ context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
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
		contracts, marshalErr := json.Marshal(explorer.PublicOutputContracts{Outputs: compiled.OutputContracts})
		if marshalErr != nil {
			return nil, marshalErr
		}
		receipt.PublicOutputContract = contracts
		receipt.OutputContractDigest, compileErr = explorer.CompilationArtifactDigest(contracts)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.CompilationKey, compileErr = explorer.CompilationKey(*receipt)
		if compileErr != nil {
			return nil, compileErr
		}
		receipt.ID, compileErr = explorer.ReceiptID(*receipt)
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
	initial, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
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
	terminalSource, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
		Project: "project-a", ExplorerID: "conditions", SnapshotToken: snapshot.Token,
		DraftVersion: 1, DraftDigest: digest, OutputID: "patients", RootNodeID: "patient",
	})
	if err != nil || terminalSource.Assessment.Status != authoringv2.RowChangeBlocked ||
		terminalSource.Assessment.Proposal != nil || terminalSource.CandidateReceiptID != "" ||
		len(terminalSource.Assessment.Unresolved) != 1 || terminalSource.Assessment.Unresolved[0].Code != "ROW_CHANGE_RELATED_SOURCE_AT_ROOT" {
		t.Fatalf("root change to saved related-source terminal=%#v err=%v, want an actionable blocked assessment", terminalSource, err)
	}

	selected, err := service.AssessRowChange(context.Background(), AssessRowChangeRequest{
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
	if gotRelated == nil || len(gotRelated.Route) != 1 || gotRelated.Route[0].FromNodeID != "specimen" ||
		gotRelated.Route[0].ToNodeID != "patient" || gotRelated.Route[0].EdgeID != "specimen-patient" ||
		gotRelated.SourceOccurrenceID != "patient" {
		t.Fatalf("candidate related-source route=%#v, want the selected Specimen→saved Patient occurrence route", gotRelated)
	}
}
