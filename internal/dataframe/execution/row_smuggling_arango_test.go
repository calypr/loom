package execution

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestPublicationRowStreamRejectsPayloadMetadataSmuggling(t *testing.T) {
	arangoURL := os.Getenv("LOOM_TEST_ARANGO_URL")
	if arangoURL == "" {
		t.Skip("LOOM_TEST_ARANGO_URL is not set")
	}
	database := os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if database == "" {
		t.Fatal("LOOM_TEST_ARANGO_DATABASE is required when LOOM_TEST_ARANGO_URL is set")
	}
	parsedURL, err := url.Parse(arangoURL)
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Hostname() == "" || parsedURL.User != nil || parsedURL.Path != "" && parsedURL.Path != "/" || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		t.Fatalf("LOOM_TEST_ARANGO_URL must be a bare unauthenticated HTTP(S) origin, got %q", arangoURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	project := "loom_row_smuggling_" + uuid.NewString()
	generation := "generation-" + uuid.NewString()
	key := project + "_patient"
	client, err := store.Open(ctx, arangoURL, database)
	if err != nil {
		t.Fatalf("open test Arango database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx, "FOR d IN Patient FILTER d._key == @key REMOVE d IN Patient", map[string]any{"key": key}); err != nil {
			t.Errorf("remove owned Patient fixture: %v", err)
		}
		if err := client.Close(cleanupCtx); err != nil {
			t.Errorf("close test Arango client: %v", err)
		}
	})
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Patient"}}}); err != nil {
		t.Fatalf("bootstrap Patient collection: %v", err)
	}

	trustedPath := "/trusted"
	document, err := json.Marshal(map[string]any{
		"_key": key, "id": "patient-1", "project": project, "project_id": project,
		"dataset_generation": generation, "resourceType": "Patient",
		"auth_resource_path": trustedPath,
		"payload": map[string]any{
			"id": "patient-1", "__loom_row_id": "forged-row-id", "auth_resource_path": "/forged",
		},
	})
	if err != nil {
		t.Fatalf("encode Patient fixture: %v", err)
	}
	if err := client.InsertBatchRaw(ctx, "Patient", []json.RawMessage{document}, false, "document"); err != nil {
		t.Fatalf("insert Patient fixture: %v", err)
	}

	engine, err := New(Config{
		Registry: invalidRecipeRegistry{},
		QueryRows: func(queryCtx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			return client.QueryRows(queryCtx, query, batchSize, bindVars, visit)
		},
	})
	if err != nil {
		t.Fatalf("create recipe engine: %v", err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "row metadata smuggling",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		}},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{trustedPath},
		IncludeAuthResourcePath: true,
	}
	resolved, err := engine.CompileResolvedBundle(ctx, bundle, bindings)
	if err != nil {
		t.Fatalf("compile resolved bundle: %v", err)
	}
	streams, err := engine.Streams(ctx, resolved)
	if err != nil {
		t.Fatalf("create output streams: %v", err)
	}
	if len(streams) != 1 {
		t.Fatalf("compiled streams = %d, want 1", len(streams))
	}
	var rows []map[string]any
	result, err := streams[0].Stream(ctx, func(row map[string]any) error {
		rows = append(rows, row)
		return nil
	})
	if err != nil {
		t.Fatalf("stream compiled Patient output: %v", err)
	}
	if result.RowCount != 1 || len(rows) != 1 {
		t.Fatalf("stream returned %d rows (result count %d), want exactly one: %#v", len(rows), result.RowCount, rows)
	}
	row := rows[0]
	if row["id"] != "patient-1" {
		t.Fatalf("streamed patient id = %#v, want patient-1", row["id"])
	}
	if row["auth_resource_path"] != trustedPath {
		t.Fatalf("streamed auth_resource_path = %#v, want stored trusted path %q", row["auth_resource_path"], trustedPath)
	}
	if got := fmt.Sprint(row["__loom_row_id"]); got == "" || got == "forged-row-id" {
		t.Fatalf("streamed __loom_row_id = %#v, want compiler-derived identity rather than payload value", row["__loom_row_id"])
	}
	wantIdentity := map[string]any{"project": project, "_key": key}
	if err := ensureStableRowIdentity(wantIdentity, streams[0].RowIdentity, streams[0].bindVars); err != nil {
		t.Fatalf("derive expected compiler row identity: %v", err)
	}
	if row["__loom_row_id"] != wantIdentity["__loom_row_id"] {
		t.Fatalf("streamed __loom_row_id = %#v, want identity derived from project/key %#v", row["__loom_row_id"], wantIdentity["__loom_row_id"])
	}
	if len(row) != 3 {
		t.Fatalf("public streamed row contains payload metadata or other unselected columns: %#v", row)
	}
}
