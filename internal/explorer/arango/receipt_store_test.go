package arango

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
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
	receipt := testCompilationReceipt(t, "intent")
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
}

func TestInsertCompilationReceiptReturnsWinnerFromConcurrentIdenticalWriters(t *testing.T) {
	receipt := testCompilationReceipt(t, "same-intent")
	client := &receiptWriteClient{conflictOnExisting: true, barrier: newReceiptInsertBarrier(2)}
	adapter, err := New(client)
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		receipt *explorer.CompilationReceipt
		err     error
	}
	results := make(chan result, 2)
	var writers sync.WaitGroup
	for range 2 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			stored, err := adapter.InsertCompilationReceipt(context.Background(), receipt)
			results <- result{receipt: stored, err: err}
		}()
	}
	writers.Wait()
	close(results)

	for result := range results {
		if result.err != nil {
			t.Fatalf("InsertCompilationReceipt() error = %v", result.err)
		}
		if result.receipt == nil || !sameReceipt(*result.receipt, receipt) {
			t.Fatalf("InsertCompilationReceipt() = %#v, want receipt %q", result.receipt, receipt.ID)
		}
	}
	if calls := client.insertCallCount(); calls != 2 {
		t.Fatalf("insert calls = %d, want two concurrent attempts", calls)
	}
}

func TestInsertCompilationReceiptRetriesOnlyArangoConflict(t *testing.T) {
	t.Run("winner not visible yet", func(t *testing.T) {
		receipt := testCompilationReceipt(t, "retry-intent")
		client := &receiptWriteClient{insertErrors: []error{receiptConflictError(), receiptConflictError()}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}

		stored, err := adapter.InsertCompilationReceipt(context.Background(), receipt)
		if err != nil {
			t.Fatalf("InsertCompilationReceipt() error = %v", err)
		}
		if stored == nil || !sameReceipt(*stored, receipt) {
			t.Fatalf("InsertCompilationReceipt() = %#v, want receipt %q", stored, receipt.ID)
		}
		if calls := client.insertCallCount(); calls != 3 {
			t.Fatalf("insert calls = %d, want two conflicts and one successful retry", calls)
		}
	})

	t.Run("retry budget is bounded", func(t *testing.T) {
		receipt := testCompilationReceipt(t, "bounded-intent")
		client := &receiptWriteClient{insertErrors: []error{
			receiptConflictError(), receiptConflictError(), receiptConflictError(), receiptConflictError(),
		}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}

		_, err = adapter.InsertCompilationReceipt(context.Background(), receipt)
		if !shared.IsArangoErrorWithErrorNum(err, shared.ErrArangoConflict) {
			t.Fatalf("InsertCompilationReceipt() error = %v, want Arango conflict", err)
		}
		if calls := client.insertCallCount(); calls != compilationReceiptInsertMaxAttempts {
			t.Fatalf("insert calls = %d, want bounded maximum %d", calls, compilationReceiptInsertMaxAttempts)
		}
	})
}

func TestInsertCompilationReceiptPreservesNonConflictAndCancellationErrors(t *testing.T) {
	t.Run("non-conflict error is returned without recovery read", func(t *testing.T) {
		receipt := testCompilationReceipt(t, "non-conflict-intent")
		want := errors.New("connection reset")
		client := &receiptWriteClient{insertErrors: []error{want}}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}

		_, err = adapter.InsertCompilationReceipt(context.Background(), receipt)
		if !errors.Is(err, want) {
			t.Fatalf("InsertCompilationReceipt() error = %v, want wrapped %v", err, want)
		}
		if calls := client.insertCallCount(); calls != 1 || client.readCallCount() != 0 {
			t.Fatalf("insert/read calls = %d/%d, want 1/0", calls, client.readCallCount())
		}
	})

	t.Run("cancellation during conflict recovery is preserved", func(t *testing.T) {
		receipt := testCompilationReceipt(t, "cancel-intent")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client := &receiptWriteClient{insertErrors: []error{receiptConflictError()}, cancelAfterFirstInsert: cancel}
		adapter, err := New(client)
		if err != nil {
			t.Fatal(err)
		}

		_, err = adapter.InsertCompilationReceipt(ctx, receipt)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("InsertCompilationReceipt() error = %v, want context cancellation", err)
		}
		if calls := client.insertCallCount(); calls != 1 {
			t.Fatalf("insert calls = %d, want no retry after cancellation", calls)
		}
	})
}

func TestInsertCompilationReceiptRejectsDifferentReceiptAtExistingKey(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		name := "ignored duplicate"
		if conflict {
			name = "write conflict"
		}
		t.Run(name, func(t *testing.T) {
			receipt := testCompilationReceipt(t, "requested-intent")
			other := testCompilationReceipt(t, "stored-different-intent")
			storedDoc, err := document(other, receipt.ID)
			if err != nil {
				t.Fatal(err)
			}
			client := &receiptWriteClient{doc: storedDoc, conflictOnExisting: conflict}
			adapter, err := New(client)
			if err != nil {
				t.Fatal(err)
			}

			_, err = adapter.InsertCompilationReceipt(context.Background(), receipt)
			if !errors.Is(err, explorer.ErrCorruptReceipt) {
				t.Fatalf("InsertCompilationReceipt() error = %v, want ErrCorruptReceipt", err)
			}
			if calls := client.insertCallCount(); calls != 1 {
				t.Fatalf("insert calls = %d, want one attempt", calls)
			}
		})
	}
}

type receiptWriteClient struct {
	mu                     sync.Mutex
	doc                    map[string]any
	insertErrors           []error
	insertCalls            int
	readCalls              int
	conflictOnExisting     bool
	barrier                *receiptInsertBarrier
	cancelAfterFirstInsert context.CancelFunc
}

func (c *receiptWriteClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *receiptWriteClient) QueryRows(ctx context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimSpace(query), "INSERT") {
		c.mu.Lock()
		call := c.insertCalls
		c.insertCalls++
		var insertErr error
		if call < len(c.insertErrors) {
			insertErr = c.insertErrors[call]
		}
		c.mu.Unlock()

		if c.barrier != nil && call < c.barrier.participants {
			if err := c.barrier.wait(ctx); err != nil {
				return err
			}
		}
		if call == 0 && insertErr != nil && c.cancelAfterFirstInsert != nil {
			c.cancelAfterFirstInsert()
		}

		c.mu.Lock()
		if insertErr == nil && c.conflictOnExisting && c.doc != nil {
			insertErr = receiptConflictError()
		}
		if insertErr == nil && c.doc == nil {
			c.doc = cloneReceiptDocument(binds["doc"].(map[string]any))
		}
		doc := cloneReceiptDocument(c.doc)
		c.mu.Unlock()
		if insertErr != nil {
			return insertErr
		}
		return visit(doc)
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.readCalls++
	doc := cloneReceiptDocument(c.doc)
	c.mu.Unlock()
	if doc == nil {
		return nil
	}
	return visit(doc)
}

func (c *receiptWriteClient) insertCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.insertCalls
}

func (c *receiptWriteClient) readCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readCalls
}

type receiptInsertBarrier struct {
	mu           sync.Mutex
	arrivals     int
	participants int
	ready        chan struct{}
}

func newReceiptInsertBarrier(participants int) *receiptInsertBarrier {
	return &receiptInsertBarrier{participants: participants, ready: make(chan struct{})}
}

func (b *receiptInsertBarrier) wait(ctx context.Context) error {
	b.mu.Lock()
	b.arrivals++
	if b.arrivals == b.participants {
		close(b.ready)
	}
	ready := b.ready
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ready:
		return nil
	}
}

func cloneReceiptDocument(doc map[string]any) map[string]any {
	if doc == nil {
		return nil
	}
	clone := make(map[string]any, len(doc))
	for key, value := range doc {
		clone[key] = value
	}
	return clone
}

func receiptConflictError() error {
	return shared.ArangoError{HasError: true, Code: 409, ErrorNum: shared.ErrArangoConflict, ErrorMessage: "write-write conflict"}
}

func testCompilationReceipt(t *testing.T, intent string) explorer.CompilationReceipt {
	t.Helper()
	bundle := testReceiptBundle()
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion:    explorer.CurrentReceiptFormatVersion,
		CompilerContractVersion: explorer.CurrentCompilerContractVersion,
		Project:                 "project-a", ExplorerID: "explorer-a", IntentDigest: intent, SnapshotToken: "snapshot",
		AuthorizationScopeDigest: "scope", CapabilitySchemaDigest: "schema", ShapeDigest: "shape",
		SourceGeneration: "generation", RecipeDigest: "recipe", ResolvedSchemaDigest: "resolved-schema",
		Bundle: bundle, PublicOutputContract: json.RawMessage(`{"outputs":[{"outputId":"output","columns":[],"lossless":true,"mlReady":true}]}`),
		OutputColumnProvenance: map[string]map[string]string{"output": {"__loom_row_id": "EXPLICIT"}},
	}
	var err error
	receipt.ResolvedRecipeDigest, err = bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.OutputContractDigest, err = explorer.CompilationArtifactDigest(receipt.PublicOutputContract)
	if err != nil {
		t.Fatal(err)
	}
	receipt.CompilationKey, err = explorer.CompilationKey(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func testReceiptBundle() recipe.Bundle {
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "receipt-test", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "output", RootResourceType: "Patient", RowGrain: "resource"}}}
}
