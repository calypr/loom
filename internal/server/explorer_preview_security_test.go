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

func TestValidateAuthorizedReceiptExecutionAcceptsExactRetainedGeneration(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"path-a"}}
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-old", scope)
	receipt := testSecurityReceipt(t, snapshot, "patients", []string{"id"})
	if err := validateAuthorizedReceiptExecution(receipt, AuthorizedCapability{Snapshot: snapshot, Scope: scope}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAuthorizedReceiptExecutionRejectsChangedTokenGenerationSchemaOrScope(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", scope)
	receipt := testSecurityReceipt(t, snapshot, "patients", []string{"id"})
	cases := map[string]func(*explorer.CompilationReceipt, *capability.Snapshot, *authscope.ReadScope){
		"token": func(r *explorer.CompilationReceipt, _ *capability.Snapshot, _ *authscope.ReadScope) {
			r.SnapshotToken = "different-token"
		},
		"generation": func(_ *explorer.CompilationReceipt, s *capability.Snapshot, _ *authscope.ReadScope) {
			s.Identity.Generation = "generation-b"
		},
		"schema": func(_ *explorer.CompilationReceipt, s *capability.Snapshot, _ *authscope.ReadScope) {
			s.Identity.SchemaDigest = "different-schema"
		},
		"scope": func(_ *explorer.CompilationReceipt, _ *capability.Snapshot, s *authscope.ReadScope) {
			s.Mode = authscope.ReadScopeRestricted
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			candidateReceipt := *receipt
			candidateSnapshot := snapshot.Clone()
			candidateScope := scope.Clone()
			mutate(&candidateReceipt, &candidateSnapshot, &candidateScope)
			err := validateAuthorizedReceiptExecution(&candidateReceipt, AuthorizedCapability{Snapshot: candidateSnapshot, Scope: candidateScope})
			if !errors.Is(err, ErrReceiptExecutionContract) {
				t.Fatalf("error=%v, want ErrReceiptExecutionContract", err)
			}
		})
	}
}

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

func TestValidateReceiptOutputContract(t *testing.T) {
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := testSecurityReceipt(t, snapshot, "patients", []string{"id"})
	if err := validateReceiptOutputContract(receipt, "patients"); err != nil {
		t.Fatal(err)
	}
	if err := validateReceiptOutputContract(receipt, "observations"); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("unknown output error=%v, want contract mismatch", err)
	}
	malformed := *receipt
	malformed.PublicOutputContract = json.RawMessage(`{"outputId":"observations"}`)
	if err := validateReceiptOutputContract(&malformed, "patients"); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("forged output contract error=%v, want contract mismatch", err)
	}
}

func TestValidateReceiptEnginePublicColumnsUsesOrderedPublicColumnsOnly(t *testing.T) {
	snapshot := testAuthorizedCapabilitySnapshot(t, "generation-a", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := testSecurityReceipt(t, snapshot, "patients", []string{"id", "name"})
	resolved := engine.Resolved{Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{Name: "patients", OutputSchema: []lower.CompiledOutputColumn{{Name: "id"}, {Name: "__loom_row_id", Internal: true}, {Name: "name"}}}}}}
	if err := validateReceiptEnginePublicColumns(receipt, resolved); err != nil {
		t.Fatal(err)
	}
	wrongOrder := *receipt
	wrongOrder.EmittedColumns = append([]explorer.EmittedColumn(nil), receipt.EmittedColumns...)
	wrongOrder.EmittedColumns[0], wrongOrder.EmittedColumns[1] = wrongOrder.EmittedColumns[1], wrongOrder.EmittedColumns[0]
	if err := validateReceiptEnginePublicColumns(&wrongOrder, resolved); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("wrong column order error=%v, want contract mismatch", err)
	}
	hidden := *receipt
	hidden.EmittedColumns = append([]explorer.EmittedColumn(nil), receipt.EmittedColumns...)
	hidden.EmittedColumns[0].PublicColumn = "__loom_row_id"
	if err := validateReceiptEnginePublicColumns(&hidden, resolved); !errors.Is(err, ErrReceiptExecutionContract) {
		t.Fatalf("hidden-column error=%v, want contract mismatch", err)
	}
}

func testSecurityReceipt(t *testing.T, snapshot capability.Snapshot, output string, columns []string) *explorer.CompilationReceipt {
	t.Helper()
	fields := make([]recipe.Field, 0, len(columns))
	emitted := make([]explorer.EmittedColumn, 0, len(columns))
	contractColumns := make([]map[string]string, 0, len(columns))
	for _, column := range columns {
		fields = append(fields, recipe.Field{Name: column, Expr: recipe.Expression{Select: "root." + column}})
		emitted = append(emitted, explorer.EmittedColumn{EmissionID: "em_" + column, OutputID: output, PublicColumn: column, LogicalType: "string"})
		contractColumns = append(contractColumns, map[string]string{"publicColumn": column})
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "security", TranslationVersion: "test", Outputs: []recipe.Output{{Name: output, RootResourceType: "Patient", RowGrain: "resource", Fields: fields}}}
	contract, err := json.Marshal(map[string]any{"outputId": output, "columns": contractColumns})
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
