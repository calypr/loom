package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/engine"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestValidateAuthorizedReadScopePreservesRestrictedEmpty(t *testing.T) {
	expected := explorerScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeRestricted})
	if err := validateAuthorizedReadScope(authscope.ReadScope{Mode: authscope.ReadScopeRestricted}, expected); err != nil {
		t.Fatalf("restricted-empty scope rejected: %v", err)
	}
	if err := validateAuthorizedReadScope(authscope.ReadScope{}, expected); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("empty scope error=%v, want contract mismatch", err)
	}
	if err := validateAuthorizedReadScope(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, expected); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("widened scope error=%v, want contract mismatch", err)
	}
}

func TestValidateReceiptEnginePublicColumnsUsesExactPublicColumnSet(t *testing.T) {
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := testSecurityReceipt(t, snapshot, "patients", []string{"id", "name"})
	resolved := engine.Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{Name: "patients", OutputSchema: []lower.CompiledOutputColumn{{Name: "id"}, {Name: "__loom_row_id", Internal: true}, {Name: "name"}}}}}}
	if err := validateReceiptEnginePublicColumns(receipt, resolved); err != nil {
		t.Fatal(err)
	}
	wrongOrder := *receipt
	wrongOrder.EmittedColumns = append([]explorer.EmittedColumn(nil), receipt.EmittedColumns...)
	wrongOrder.EmittedColumns[0], wrongOrder.EmittedColumns[1] = wrongOrder.EmittedColumns[1], wrongOrder.EmittedColumns[0]
	if err := validateReceiptEnginePublicColumns(&wrongOrder, resolved); err != nil {
		t.Fatalf("presentation order must not change execution compatibility: %v", err)
	}
	hidden := *receipt
	hidden.EmittedColumns = append([]explorer.EmittedColumn(nil), receipt.EmittedColumns...)
	hidden.EmittedColumns[0].PublicColumn = "__loom_row_id"
	if err := validateReceiptEnginePublicColumns(&hidden, resolved); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("hidden-column error=%v, want contract mismatch", err)
	}
	duplicate := *receipt
	duplicate.EmittedColumns = append([]explorer.EmittedColumn(nil), receipt.EmittedColumns...)
	duplicate.EmittedColumns[1].PublicColumn = "id"
	if err := validateReceiptEnginePublicColumns(&duplicate, resolved); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("duplicate-column error=%v, want contract mismatch", err)
	}
	extra := *receipt
	extra.EmittedColumns = append(append([]explorer.EmittedColumn(nil), receipt.EmittedColumns...), explorer.EmittedColumn{OutputID: "patients", PublicColumn: "extra"})
	if err := validateReceiptEnginePublicColumns(&extra, resolved); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("extra-column error=%v, want contract mismatch", err)
	}
}

func testSecurityReceipt(t *testing.T, snapshot capability.Snapshot, output string, columns []string) *explorer.CompilationReceipt {
	t.Helper()
	fields := make([]recipe.Field, 0, len(columns))
	emitted := make([]explorer.EmittedColumn, 0, len(columns))
	contractColumns := make([]explorer.PublicOutputColumn, 0, len(columns))
	for _, column := range columns {
		fields = append(fields, recipe.Field{Name: column, Expr: recipe.Expression{Select: "root." + column}})
		emitted = append(emitted, explorer.EmittedColumn{EmissionID: "em_" + column, OutputID: output, CandidateID: "c_" + column, OccurrenceID: "base", ProjectionMode: "VALUE", PublicColumn: column, Label: column, LogicalType: "string"})
		contractColumns = append(contractColumns, explorer.PublicOutputColumn{Column: column, Label: column, LogicalType: "string"})
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "security", TranslationVersion: "test", Outputs: []recipe.Output{{Name: output, RootResourceType: "Patient", RowGrain: "resource", Fields: fields}}}
	contract, err := json.Marshal(explorer.PublicOutputContracts{Outputs: []explorer.PublicOutputContract{{OutputID: output, Columns: contractColumns}}})
	if err != nil {
		t.Fatal(err)
	}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := &explorer.CompilationReceipt{
		Project: snapshot.Identity.Project, ExplorerID: "explorer-a", SnapshotToken: snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest,
		SourceGeneration: snapshot.Identity.Generation, Bundle: bundle, PublicOutputContract: contract,
		EmittedColumns: emitted, RecipeDigest: bundleDigest, ResolvedRecipeDigest: bundleDigest,
	}
	return receipt
}

func TestReceiptExecutionContractErrorWrapsCause(t *testing.T) {
	err := receiptExecutionContractError("generation %q changed", "generation-b")
	if !errors.Is(err, ErrReceiptExecutionContract) || !strings.Contains(err.Error(), "generation-b") {
		t.Fatalf("error=%v", err)
	}
}
