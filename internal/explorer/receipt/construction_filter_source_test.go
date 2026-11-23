package receipt

import (
	"context"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	"github.com/calypr/loom/internal/authscope"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/recipetest"
)

const filterSourceF1Column = "col_reconcile_integer"
const filterSourceF1HonestField = "multipleBirthInteger"
const filterSourceF1OwnerStep = "only_step"

func filterSourceF1Snapshot() capability.Snapshot {
	snapshot := capabilityfixtures.AuthoringV2CapabilitySnapshot(capabilityresolver.ScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}))
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "c_patient_active", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "active", Label: "Active", LogicalType: "boolean", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
		capability.Candidate{
			ID: "c_patient_multiple_birth_integer", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: filterSourceF1HonestField, Label: "Multiple birth integer", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
	)
	return snapshot
}

func filterSourceF1HonestProjection() ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: filterSourceF1Column, OwnerStepID: filterSourceF1OwnerStep,
		OccurrenceID: ws.RootOccurrenceID, FieldPath: filterSourceF1HonestField,
		FHIRType: "integer", LogicalType: "integer", Label: "Multiple birth integer",
	}
}

func filterSourceF1Workspace(t *testing.T, snapshot capability.Snapshot, projection ws.ConstructionSourceProjection) ws.Workspace {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	columns = append(columns, ws.StageColumn{
		ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID),
		Label: projection.Label, Type: projection.LogicalType,
	})
	document.Construction.Steps = []ws.ConstructionStep{{
		ID:     filterSourceF1OwnerStep,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind: ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{
				ColumnID: projection.ColumnID, Operator: ws.ConstructionFilterEquals,
				Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: filterSourceHTTPBool(true)}},
			},
		},
		Outputs: columns,
	}}
	document.Construction.SourceProjections = []ws.ConstructionSourceProjection{projection}
	workspace.Documents[0] = document
	catalog := ws.CatalogFromCapability(snapshot, "custom")
	workspace, err = ws.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate durable filter-source workspace: %v", err)
	}
	return workspace
}

func filterSourceHTTPBool(value bool) *bool { return &value }

func filterSourceF1Compile(t *testing.T, snapshot capability.Snapshot) (ws.Workspace, dataframeexecution.Resolved) {
	t.Helper()
	workspace := filterSourceF1Workspace(t, snapshot, filterSourceF1HonestProjection())
	translated, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "custom", workspace, snapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatalf("compile honest filter-source plan: %v", err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), translated.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
	if err != nil {
		t.Fatalf("compile honest filter-source recipe: %v", err)
	}
	return workspace, resolved
}

func filterSourceF1Refuse(t *testing.T, tampered ws.ConstructionSourceProjection, want string) {
	t.Helper()
	snapshot := filterSourceF1Snapshot()
	_, resolved := filterSourceF1Compile(t, snapshot)
	workspace := filterSourceF1Workspace(t, snapshot, tampered)
	honest := filterSourceF1HonestProjection()
	if tampered.ColumnID != honest.ColumnID || tampered.OwnerStepID != honest.OwnerStepID {
		t.Fatal("tampered probe must keep honest owner and column identity")
	}
	compiledByOutput := map[string]facade.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	_, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, snapshot)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("reconcile FILTER branch wrongly accepted tampered projection %s: err=%v want=%q", tampered.FieldPath, err, want)
	}
}

func TestConstructionFilterSourceReconcileRefusesFieldPathSwap(t *testing.T) {
	tampered := filterSourceF1HonestProjection()
	tampered.FieldPath = "active"
	tampered.FHIRType = "boolean"
	tampered.LogicalType = "boolean"
	tampered.Label = "Patient.active"
	filterSourceF1Refuse(t, tampered, "compiled source column identity")
}

func TestConstructionFilterSourceReconcileRefusesLogicalTypeFlip(t *testing.T) {
	tampered := filterSourceF1HonestProjection()
	tampered.LogicalType = "boolean"
	filterSourceF1Refuse(t, tampered, "compiled source column identity")
}

func TestConstructionFilterSourceReconcileClaimsHonestProjection(t *testing.T) {
	snapshot := filterSourceF1Snapshot()
	workspace, resolved := filterSourceF1Compile(t, snapshot)
	compiledByOutput := map[string]facade.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	if _, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, snapshot); err != nil {
		t.Fatalf("reconcile FILTER branch refused the honest projection: %v", err)
	}
}

func TestConstructionFilterSourceReconcileClaimsOnlyWithAuthorizedSnapshot(t *testing.T) {
	armedSnapshot := filterSourceF1Snapshot()
	workspace, resolved := filterSourceF1Compile(t, armedSnapshot)
	compiledByOutput := map[string]facade.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	stale := filterSourceF1Snapshot()
	for index := range stale.Candidates {
		if stale.Candidates[index].ID == "c_patient_multiple_birth_integer" {
			stale.Candidates[index].Populated = false
		}
	}
	blankToken := stale
	blankToken.Token = ""

	_, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, blankToken)
	if err == nil || !strings.Contains(err.Error(), "without an authorized capability snapshot") {
		t.Fatalf("unarmed reconcile wrongly published an unpopulated filter-owned claim: err=%v", err)
	}
	if _, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, stale); err == nil || !strings.Contains(err.Error(), "authorized filter source derivation") {
		t.Fatalf("armed reconcile wrongly published the same unpopulated claim: err=%v", err)
	}
}
