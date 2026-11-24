package receipt

import (
	"context"
	"encoding/json"
	"errors"
	explorerdb "github.com/calypr/loom/internal/explorer/arango/db"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/authscope"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/testkit/recipetest"
)

func TestValidateReceiptEnginePublicColumnsUsesExactPublicColumnSet(t *testing.T) {
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := testSecurityReceipt(t, snapshot, "patients", []string{"id", "name"})
	resolved := dataframeexecution.Resolved{Compiled: facade.CompiledRecipe{Outputs: []facade.CompiledRecipeOutput{{Name: "patients", OutputSchema: []facade.CompiledOutputColumn{{Name: "id"}, {Name: "__loom_row_id", Internal: true}, {Name: "__loom_identity", Identity: true}, {Name: "name"}}}}}}
	if err := ValidateEnginePublicColumns(receipt, resolved); err != nil {
		t.Fatal(err)
	}
	wrongOrder := *receipt
	wrongOrder.EmittedColumns = append([]viewer.EmittedColumn(nil), receipt.EmittedColumns...)
	wrongOrder.EmittedColumns[0], wrongOrder.EmittedColumns[1] = wrongOrder.EmittedColumns[1], wrongOrder.EmittedColumns[0]
	if err := ValidateEnginePublicColumns(&wrongOrder, resolved); err != nil {
		t.Fatalf("presentation order must not change execution compatibility: %v", err)
	}
	hidden := *receipt
	hidden.EmittedColumns = append([]viewer.EmittedColumn(nil), receipt.EmittedColumns...)
	hidden.EmittedColumns[0].PublicColumn = "__loom_row_id"
	if err := ValidateEnginePublicColumns(&hidden, resolved); !errors.Is(err, explorer.ErrReceiptExecutionContract) {
		t.Fatalf("hidden-column error=%v, want contract mismatch", err)
	}
	duplicate := *receipt
	duplicate.EmittedColumns = append([]viewer.EmittedColumn(nil), receipt.EmittedColumns...)
	duplicate.EmittedColumns[1].PublicColumn = "id"
	if err := ValidateEnginePublicColumns(&duplicate, resolved); !errors.Is(err, explorer.ErrReceiptExecutionContract) {
		t.Fatalf("duplicate-column error=%v, want contract mismatch", err)
	}
	extra := *receipt
	extra.EmittedColumns = append(append([]viewer.EmittedColumn(nil), receipt.EmittedColumns...), viewer.EmittedColumn{OutputID: "patients", PublicColumn: "extra"})
	if err := ValidateEnginePublicColumns(&extra, resolved); !errors.Is(err, explorer.ErrReceiptExecutionContract) {
		t.Fatalf("extra-column error=%v, want contract mismatch", err)
	}
}

func TestValidateReceiptEnginePublicColumnsUsesExplicitGroupSchema(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "explicit-group-preview",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "groups",
			GroupRows: &recipe.GroupRows{RevisionID: "grouprev_pinned", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED"},
			Fields:    []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:     recipetest.Registry{},
		RootPageRows: 25,
		QueryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{
				"group_revision_id": "grouprev_pinned", "group_id": "j03-reviewed", "group_label": "J03 reviewed", "group_ordinal": 0,
				"members":       []any{map[string]any{"source_identity": map[string]any{"id": "dev-patient-001"}}},
				"__loom_row_id": map[string]any{"group_revision_id": "grouprev_pinned", "group_id": "j03-reviewed"},
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a", GroupRowsCollections: explorerdb.GroupRowsCollections(), SelectionProject: "project-a"})
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, _, err := ResolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	receipt := &receiptmodel.CompilationReceipt{
		Bundle: bundle, OutputFingerprints: fingerprints,
		EmittedColumns: []viewer.EmittedColumn{{OutputID: "patients", PublicColumn: "id"}},
	}
	if err := ValidateEnginePublicColumns(receipt, resolved); err != nil {
		t.Fatalf("grouped row schema rejected despite stable explicit-group contract: %v", err)
	}
	var previewed []map[string]any
	summary, err := engine.PreviewOutput(context.Background(), resolved, dataframeexecution.PreviewRequest{Output: "patients", Limit: 25, IncludeRowIdentity: true}, func(row map[string]any) error {
		previewed = append(previewed, row)
		return nil
	})
	if err != nil || summary.RowCount != 1 || len(previewed) != 1 {
		t.Fatalf("explicit-group preview summary=%#v rows=%#v err=%v", summary, previewed, err)
	}
	identity, ok := previewed[0]["__loom_row_id"].(map[string]any)
	if !ok || identity["group_revision_id"] != "grouprev_pinned" || identity["group_id"] != "j03-reviewed" || previewed[0]["members"] == nil {
		t.Fatalf("explicit-group preview row lacks exact group identity/members: %#v", previewed[0])
	}

	wrongSchema := resolved
	wrongSchema.Compiled.Outputs = append([]facade.CompiledRecipeOutput(nil), resolved.Compiled.Outputs...)
	wrongSchema.Compiled.Outputs[0].OutputSchema = append([]facade.CompiledOutputColumn(nil), resolved.Compiled.Outputs[0].OutputSchema...)
	wrongSchema.Compiled.Outputs[0].OutputSchema = append(wrongSchema.Compiled.Outputs[0].OutputSchema, facade.CompiledOutputColumn{Name: "unexpected"})
	var mismatch *ContractMismatch
	if err := ValidateEnginePublicColumns(receipt, wrongSchema); !errors.As(err, &mismatch) || mismatch.Component != "output_execution" {
		t.Fatalf("unexpected grouped output fingerprint error=%v, want output execution mismatch", err)
	}
}

func testSecurityReceipt(t *testing.T, snapshot capability.Snapshot, output string, columns []string) *receiptmodel.CompilationReceipt {
	t.Helper()
	fields := make([]recipe.Field, 0, len(columns))
	emitted := make([]viewer.EmittedColumn, 0, len(columns))
	contractColumns := make([]receiptmodel.PublicOutputColumn, 0, len(columns))
	for _, column := range columns {
		fields = append(fields, recipe.Field{Name: column, Expr: recipe.Expression{Select: "root." + column}})
		emitted = append(emitted, viewer.EmittedColumn{EmissionID: "em_" + column, OutputID: output, CandidateID: "c_" + column, OccurrenceID: "base", ProjectionMode: "VALUE", PublicColumn: column, Label: column, LogicalType: "string"})
		contractColumns = append(contractColumns, receiptmodel.PublicOutputColumn{Column: column, Label: column, LogicalType: "string"})
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "security", TranslationVersion: "test", Outputs: []recipe.Output{{Name: output, RootResourceType: "Patient", RowGrain: "resource", Fields: fields}}}
	contract, err := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: []receiptmodel.PublicOutputContract{{OutputID: output, Columns: contractColumns}}})
	if err != nil {
		t.Fatal(err)
	}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := &receiptmodel.CompilationReceipt{
		Project: snapshot.Identity.Project, ExplorerID: "explorer-a", SnapshotToken: snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest,
		SourceGeneration: snapshot.Identity.Generation, Bundle: bundle, PublicOutputContract: contract,
		EmittedColumns: emitted, RecipeDigest: bundleDigest, ResolvedRecipeDigest: bundleDigest,
	}
	return receipt
}

func TestReceiptExecutionContractErrorWrapsCause(t *testing.T) {
	err := ExecutionContractError("generation %q changed", "generation-b")
	if !errors.Is(err, explorer.ErrReceiptExecutionContract) || !strings.Contains(err.Error(), "generation-b") {
		t.Fatalf("error=%v", err)
	}
}
