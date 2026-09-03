package arango

import (
	"context"
	"strings"
	"testing"
	"time"

	storepkg "github.com/calypr/loom/internal/store/arango"
)

func TestDecodeNormalizesNumericUpdatedAt(t *testing.T) {
	timestamp := float64(1776620000000)
	value, err := decode[struct {
		UpdatedAt time.Time `json:"updatedAt"`
	}](map[string]any{"updatedAt": timestamp})
	if err != nil {
		t.Fatal(err)
	}
	if value.UpdatedAt.UnixMilli() != int64(timestamp) {
		t.Fatalf("updatedAt=%v, want %d", value.UpdatedAt, int64(timestamp))
	}
}

type queryCall struct {
	query string
	binds map[string]any
}
type activationClient struct{ calls []queryCall }

func (c *activationClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *activationClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	c.calls = append(c.calls, queryCall{query: query, binds: binds})
	if strings.Contains(query, "RETURN {manifest:") {
		return visit(compositeActivationRow())
	}
	if strings.Contains(query, "RETURN {owner:") {
		return visit(ownerActivationRow())
	}
	return visit(map[string]any{"ok": true})
}

func ownerActivationRow() map[string]any {
	return map[string]any{
		"owner": map[string]any{"_key": "owner-key", "project": "project-a", "explorerId": "default", "managementMode": "REPOSITORY"},
		"candidate": map[string]any{
			"_key": "revision-a", "id": "revision-a", "project": "project-a", "explorerId": "default", "status": "READY",
			"config": map[string]any{}, "sourceGeneration": "generation-a", "publication": map[string]any{},
			"materializations": []any{}, "emittedColumns": []any{}, "diagnostics": []any{},
		},
		"prior": nil,
	}
}

func compositeActivationRow() map[string]any {
	row := ownerActivationRow()
	row["manifest"] = map[string]any{"_key": "manifest-key", "recordType": "manifest", "state": "STAGED", "dataset": map[string]any{"project": "project-a", "generation": "generation-a"}}
	row["active"] = map[string]any{"_key": "active-key", "recordType": "active_generation", "project": "project-a"}
	row["execution"] = map[string]any{"project": "project-a", "datasetGeneration": "generation-a", "state": "PUBLISHED"}
	return row
}

func TestActivateRepositoryGenerationUsesCompositeGuards(t *testing.T) {
	client := &activationClient{}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.ActivateRepositoryGeneration(context.Background(), "project-a", "generation-a", "revision-a"); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 4 {
		t.Fatalf("calls=%d", len(client.calls))
	}
	query := client.calls[0].query
	for _, required := range []string{
		"@@lifecycle", "@@explorers", "@@revisions", "@@executions",
		"manifest.state IN [\"STAGED\", \"READY\"]",
		"candidate.status == \"ACTIVE\" AND owner.activeRevisionId == @revisionKey",
		"execution.project == @project", "execution.datasetGeneration == @generation", "execution.state == \"PUBLISHED\"",
	} {
		if !strings.Contains(query, required) {
			t.Fatalf("query missing %q:\n%s", required, query)
		}
	}
	allQueries := ""
	for _, call := range client.calls {
		allQueries += call.query
	}
	if got := strings.Count(allQueries, "UPDATE d WITH"); got != 2 {
		t.Fatalf("composite activation revision/dataset updates=%d, want 2:\n%s", got, allQueries)
	}
	if !strings.Contains(allQueries, "REPLACE d WITH MERGE(UNSET(d") {
		t.Fatalf("owner activation does not remove legacy generated fields:\n%s", allQueries)
	}
	if got := client.calls[0].binds["generation"]; got != "generation-a" {
		t.Fatalf("generation bind=%v", got)
	}
}

func TestActivationUsesTransactionsAndSingleModificationQueries(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, *Store) error
	}{
		{
			name: "repository generation",
			call: func(ctx context.Context, store *Store) error {
				return store.ActivateRepositoryGeneration(ctx, "project-a", "generation-a", "revision-a")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &activationClient{}
			store, err := New(client)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(context.Background(), store); err != nil {
				t.Fatal(err)
			}
			if len(client.calls) < 3 {
				t.Fatalf("calls=%d, want read plus updates", len(client.calls))
			}
			for _, call := range client.calls {
				for _, invalid := range []string{
					"? null : UPDATE", "LET active = UPDATE", "LET explorer = UPDATE", "LET owner = UPDATE",
					"LET activateDataset = UPDATE", "LET activateRevision = UPDATE", "LET activateExplorer = UPDATE",
					"FOR priorDoc IN [prior]",
				} {
					if strings.Contains(call.query, invalid) {
						t.Fatalf("invalid AQL activation form %q:\n%s", invalid, call.query)
					}
				}
			}
		})
	}
}
