package arango

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	storepkg "github.com/calypr/loom/internal/store/arango"
)

type receiptClient struct {
	calls []queryCall
	doc   map[string]any
}

func (c *receiptClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *receiptClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	c.calls = append(c.calls, queryCall{query: query, binds: binds})
	if strings.HasPrefix(strings.TrimSpace(query), "INSERT") {
		if c.doc == nil {
			c.doc = binds["doc"].(map[string]any)
		}
		return visit(c.doc)
	}
	if strings.Contains(query, "oldestCreatedAt") {
		return visit(map[string]any{"count": 1, "approxBytes": 128, "oldestCreatedAt": "2026-01-01T00:00:00Z", "unreferencedCount": 1})
	}
	if c.doc != nil {
		return visit(c.doc)
	}
	return nil
}

func TestReceiptArangoStoreUsesImmutableTenantScopedQueries(t *testing.T) {
	client := &receiptClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	bundle := testReceiptBundle()
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion:    explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion: explorer.CurrentCompilerContractVersion,
		Project:                 "project-a", ExplorerID: "explorer-a", IntentDigest: "intent", SnapshotToken: "snapshot",
		AuthorizationScopeDigest: "scope", CapabilitySchemaDigest: "schema",
		SourceGeneration: "generation", RecipeDigest: "recipe", ResolvedSchemaDigest: "resolved-schema",
		Bundle: bundle, PublicOutputContract: json.RawMessage(`{"outputId":"output","columns":[]}`),
	}
	receipt.ResolvedRecipeDigest, _ = bundle.Digest()
	receipt.OutputContractDigest, _ = explorer.CompilationArtifactDigest(receipt.PublicOutputContract)
	receipt.CompilationKey, _ = explorer.CompilationKey(receipt)
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.InsertCompilationReceipt(context.Background(), receipt); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(client.calls[0].query, "UPSERT") || strings.Contains(client.calls[0].query, "UPDATE") || !strings.Contains(client.calls[0].query, "overwriteMode: \"ignore\"") {
		t.Fatalf("receipt insert is not immutable: %s", client.calls[0].query)
	}
	if _, err := adapter.GetCompilationReceiptForExplorer(context.Background(), "project-a", "explorer-a", receipt.ID); err != nil {
		t.Fatal(err)
	}
	call := client.calls[len(client.calls)-1]
	if !strings.Contains(call.query, "d.project == @project") || !strings.Contains(call.query, "d.explorerId == @explorerId") {
		t.Fatalf("lookup is not tenant scoped: %s", call.query)
	}
	if _, err := adapter.GetCompilationReceiptByCompilationKey(context.Background(), "project-a", "explorer-a", receipt.CompilationKey); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(client.calls[len(client.calls)-1].query, "d.compilationKey == @compilationKey") {
		t.Fatalf("compilation key query missing: %s", client.calls[len(client.calls)-1].query)
	}
}

func TestReceiptArangoStats(t *testing.T) {
	client := &receiptClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	stats, err := adapter.CompilationReceiptStats(context.Background(), "project-a")
	if err != nil {
		t.Fatal(err)
	}
	if stats.Count != 1 || stats.ApproxBytes != 128 || stats.UnreferencedCount != 1 {
		t.Fatalf("stats=%+v", stats)
	}
}

func testReceiptBundle() recipe.Bundle {
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "receipt-test", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "output", RootResourceType: "Patient", RowGrain: "resource"}}}
}
