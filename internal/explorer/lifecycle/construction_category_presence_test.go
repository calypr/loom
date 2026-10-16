package lifecycle

import (
	"context"
	"fmt"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/fhir/schema"
)

func TestDiscoverConstructionCategoriesCompilesPresenceForFilteredOrdinaryProbe(t *testing.T) {
	service, store, snapshot := constructionProposalService(t)
	workspace, err := authoringv2.DecodeWorkspace(store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.Columns = []authoringv2.Column{{
		ColumnID: "group_id", Column: "patient_id", Label: "Patient ID", LogicalType: "string",
		OccurrenceID: authoringv2.RootOccurrenceID,
		Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}}
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "keep_positive", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: "group_id", Operator: authoringv2.ConstructionFilterExists,
		}},
		Outputs: []authoringv2.StageColumn{{ID: "group_id", Name: "patient_id", Label: "Patient ID", Type: "string"}},
	}}}
	workspace.Documents[0] = document
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.created.DraftConfig, store.created.DraftDigest = encoded, digest

	snapshot = categoryPresenceSnapshot(t, snapshot)
	service.config.Capability.Token = func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) {
		return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
	}
	service.config.RowChoiceResolver = categoryPresenceRowChoiceResolver{}
	categoryChoice := categoryPresenceRowChoice(t, snapshot, "gender")
	valueChoice := categoryPresenceRowChoice(t, snapshot, "multipleBirthInteger")

	compileReceipt := service.config.CompileReceipt
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt, err := compileReceipt(ctx, request)
		if err != nil {
			return nil, err
		}
		compiledWorkspace, err := explorercompilation.CompileWorkspace(ctx, request.Project, request.ExplorerID, request.Workspace, snapshot, explorercompilation.ResolvedInputs{})
		if err != nil {
			return nil, err
		}
		bundle := compiledWorkspace.Bundle
		receipt.Bundle = bundle
		receipt.RecipeDigest, err = bundle.Digest()
		if err != nil {
			return nil, err
		}
		receipt.ResolvedRecipeDigest = receipt.RecipeDigest
		receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
		if err != nil {
			return nil, err
		}
		receipt.ID, err = explorer.ReceiptID(*receipt)
		if err != nil {
			return nil, err
		}
		store.receipt = receipt
		return receipt, nil
	}

	queries := 0
	service.config.ScanCategories = func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, scan dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		if receipt == nil || receipt.Bundle.Outputs[0].Construction == nil || scan.StageID != "keep_positive" ||
			scan.ColumnID != "pivot_category_id" || scan.ValueColumnID != "pivot_value_id" {
			t.Fatalf("lifecycle did not pass the exact filtered discovery pair: receipt=%#v scan=%#v", receipt, scan)
		}
		plan, err := semantic.BuildRecipePlan(receipt.Bundle, bindings)
		if err != nil {
			t.Fatalf("build lifecycle receipt recipe: %v", err)
		}
		resolved, err := semantic.ResolveRecipePlan(plan, receipt.AuthorizationScopeDigest, bindings.DatasetGeneration)
		if err != nil {
			t.Fatalf("resolve lifecycle receipt recipe: %v", err)
		}
		compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("lower lifecycle receipt recipe: %v", err)
		}
		query, err := compiler.CompileCategoryScanStageWithPolicy(compiled.Outputs[0], scan.StageID, scan.ColumnID, scan.ValueColumnID, scan.MaxValues, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatalf("compile lifecycle category stage: %v", err)
		}
		queries++
		if !query.Proof.PresenceTracked || query.PresentColumn == "" {
			t.Fatalf("actual ordinary-probe compiler output did not prove source presence: %#v", query.Proof)
		}
		rows := []map[string]any{
			{query.PresentColumn: true, query.ValueColumn: "final"},
			{query.PresentColumn: true, query.ValueColumn: nil},
			{query.PresentColumn: false, query.ValueColumn: nil},
		}
		queryRows := func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			for _, row := range rows {
				if err := visit(row); err != nil {
					return err
				}
			}
			return nil
		}
		engine, err := dataframeexecution.New(dataframeexecution.Config{
			Registry: categoryDiscoveryEmptyRecipeReader{}, QueryRows: queryRows, PreviewQueryRows: queryRows,
		})
		if err != nil {
			t.Fatalf("make lifecycle category engine: %v", err)
		}
		return engine.ScanCategoriesCompiled(ctx, query)
	}

	request := ConstructionCategoryDiscoveryRequest{
		Project: store.created.Project, ExplorerID: store.created.ExplorerID, SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: store.created.DraftVersion, ExpectedDraftDigest: store.created.DraftDigest,
		OutputID: "patients", StageID: "keep_positive", PivotStepID: "pivot_probe",
		CategoryColumnID: "pivot_category_id", ValueColumnID: "pivot_value_id", GroupKeyIDs: []string{"group_id"},
		PivotSources: []ConstructionPivotSourceSelection{
			{ChoiceID: categoryChoice.ChoiceID, ColumnID: "pivot_category_id"},
			{ChoiceID: valueChoice.ChoiceID, ColumnID: "pivot_value_id"},
		},
		CandidateConstruction: document.Construction,
	}
	response, err := service.DiscoverConstructionCategories(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if queries != 1 || !response.Complete || response.Outcome != constructionCategoryDiscoveryComplete || len(response.Categories) != 3 {
		t.Fatalf("filtered ordinary STRING probe discovery = %#v; actual compiler scans=%d", response, queries)
	}
	wantKinds := []authoringv2.TableScalarKind{authoringv2.TableScalarString, authoringv2.TableScalarNull, authoringv2.TableScalarMissing}
	for index, want := range wantKinds {
		if response.Categories[index].Key.Kind != want {
			t.Fatalf("category %d kind = %q, want %q: %#v", index, response.Categories[index].Key.Kind, want, response.Categories)
		}
	}
}

func categoryPresenceSnapshot(t *testing.T, snapshot capability.Snapshot) capability.Snapshot {
	t.Helper()
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Nodes = []capability.Node{{ID: "patient", ResourceType: "Patient", RowRootEligible: true, Populated: true}}
	snapshot.Candidates = nil
	for _, field := range []struct{ path, logical string }{{"id", "string"}, {"gender", "string"}, {"multipleBirthInteger", "integer"}} {
		facts, err := index.ResolveRowPath("Patient", field.path)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
			ID: field.path, NodeID: "patient", ResourceType: "Patient", FieldPath: field.path,
			Label: field.path, LogicalType: field.logical, Cardinality: string(facts.Cardinality),
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Populated: true,
		})
	}
	return snapshot
}

func categoryPresenceRowChoice(t *testing.T, snapshot capability.Snapshot, path string) capability.RowChoice {
	t.Helper()
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	facts, err := index.ResolveRowPath("Patient", path)
	if err != nil {
		t.Fatal(err)
	}
	choice, err := capability.NewRowChoice(snapshot, []capability.RowChoiceOccurrence{{
		OccurrenceID: authoringv2.RootOccurrenceID, NodeID: "patient", ResourceType: "Patient",
	}}, authoringv2.RootOccurrenceID, capability.RowChoiceFieldGroupKey, capability.RowChoiceFacts{
		ResourceType: "Patient", CanonicalPath: facts.CanonicalPath, FHIRType: facts.FHIRType,
		Cardinality: capability.RowChoiceCardinality(facts.Cardinality), Shape: capability.RowChoiceShape(facts.Shape), Reference: facts.Reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	return choice
}

type categoryPresenceRowChoiceResolver struct{}

func (categoryPresenceRowChoiceResolver) ResolveRowChoiceID(_ context.Context, request RowChoiceResolveRequest) (ResolvedRowChoice, error) {
	identity, err := capability.DecodeRowChoiceID(request.RowChoiceID)
	if err != nil {
		return ResolvedRowChoice{}, err
	}
	return ResolvedRowChoice{Kind: request.ExpectedKind, OccurrenceID: identity.Occurrence.OccurrenceID, FieldPath: identity.Path}, nil
}

type categoryDiscoveryEmptyRecipeReader struct{}

func (categoryDiscoveryEmptyRecipeReader) LoadRecipe(context.Context, string) (exec.Entry, error) {
	return exec.Entry{}, fmt.Errorf("not used by compiled category scan")
}

func (categoryDiscoveryEmptyRecipeReader) LoadRecipeVersion(context.Context, string, string) (exec.Entry, error) {
	return exec.Entry{}, fmt.Errorf("not used by compiled category scan")
}
