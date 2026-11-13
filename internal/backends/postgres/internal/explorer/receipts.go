package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/calypr/loom/internal/explorer"
)

const ReceiptsTable = "compilation_receipts"

const (
	receiptInsertMaxAttempts  = 4
	receiptConflictRetryDelay = 5 * time.Millisecond
)

var ErrReceiptClaimLost = errors.New("postgres explorer store: receipt insert lost every claim race to an aborted winner")

var compilationReceiptColumns = []string{
	"project",
	"explorer_id",
	"id",
	"receipt_format_version",
	"compiler_contract_version",
	"compilation_key",
	"intent_digest",
	"snapshot_token",
	"source_generation",
	"recipe_digest",
	"resolved_recipe_digest",
	"doc",
}

type receiptStatements struct {
	insert string
	claim  string
	get    string
}

func newReceiptStatements(schema string) receiptStatements {
	table := sanitizeTableRef(schema, ReceiptsTable)
	cols := strings.Join(compilationReceiptColumns, ", ")
	placeholders := orderedPlaceholders(len(compilationReceiptColumns))
	scope := " WHERE project = $1 AND explorer_id = $2 AND id = $3"
	return receiptStatements{
		insert: "INSERT INTO " + table + " (" + cols + ") VALUES (" + placeholders + ") " +
			"ON CONFLICT (project, explorer_id, id) DO NOTHING RETURNING " + cols,
		claim: "SELECT " + cols + " FROM " + table + scope + " FOR UPDATE",
		get:   "SELECT " + cols + " FROM " + table + scope,
	}
}

type storedReceipt struct {
	Project                 string
	ExplorerID              string
	ID                      string
	ReceiptFormatVersion    int64
	CompilerContractVersion string
	CompilationKey          string
	IntentDigest            string
	SnapshotToken           string
	SourceGeneration        string
	RecipeDigest            string
	ResolvedRecipeDigest    string
	Doc                     json.RawMessage
}

func scanReceipt(row rowScanner, out *storedReceipt) error {
	return row.Scan(
		&out.Project,
		&out.ExplorerID,
		&out.ID,
		&out.ReceiptFormatVersion,
		&out.CompilerContractVersion,
		&out.CompilationKey,
		&out.IntentDigest,
		&out.SnapshotToken,
		&out.SourceGeneration,
		&out.RecipeDigest,
		&out.ResolvedRecipeDigest,
		&out.Doc,
	)
}

func compilationReceiptArgs(receipt explorer.CompilationReceipt, doc []byte) []any {
	return []any{
		receipt.Project,
		receipt.ExplorerID,
		receipt.ID,
		int64(receipt.ReceiptFormatVersion),
		receipt.CompilerContractVersion,
		receipt.CompilationKey,
		receipt.IntentDigest,
		receipt.SnapshotToken,
		receipt.SourceGeneration,
		receipt.RecipeDigest,
		receipt.ResolvedRecipeDigest,
		doc,
	}
}

func validateReceiptForStore(receipt explorer.CompilationReceipt) error {
	if strings.TrimSpace(receipt.ID) == "" {
		return explorer.ErrCorruptReceipt
	}
	if receipt.ReceiptFormatVersion != 0 || receipt.CompilerContractVersion != "" || receipt.CompilationKey != "" {
		if err := receipt.Validate(); err != nil {
			return err
		}
	}
	if strings.HasPrefix(receipt.ID, "receipt_") && len(receipt.ID) == len("receipt_")+sha256.Size*2 {
		if _, err := hex.DecodeString(strings.TrimPrefix(receipt.ID, "receipt_")); err != nil {
			return explorer.ErrCorruptReceipt
		}
		if err := receipt.ValidateID(); err != nil {
			return explorer.ErrCorruptReceipt
		}
	}
	return nil
}

func decodeStoredReceipt(row storedReceipt) (*explorer.CompilationReceipt, error) {
	var receipt explorer.CompilationReceipt
	if err := json.Unmarshal(row.Doc, &receipt); err != nil {
		return nil, fmt.Errorf("%w: stored receipt %q document is undecodable: %v", explorer.ErrCorruptReceipt, row.ID, err)
	}
	if err := validateReceiptForStore(receipt); err != nil {
		return nil, fmt.Errorf("%w: stored receipt %q failed validation: %v", explorer.ErrCorruptReceipt, row.ID, err)
	}
	if receipt.ID != row.ID || receipt.Project != row.Project || receipt.ExplorerID != row.ExplorerID {
		return nil, fmt.Errorf("%w: stored receipt %q scope columns disagree with its document", explorer.ErrCorruptReceipt, row.ID)
	}
	if int64(receipt.ReceiptFormatVersion) != row.ReceiptFormatVersion ||
		receipt.CompilerContractVersion != row.CompilerContractVersion ||
		receipt.CompilationKey != row.CompilationKey ||
		receipt.IntentDigest != row.IntentDigest ||
		receipt.SnapshotToken != row.SnapshotToken ||
		receipt.SourceGeneration != row.SourceGeneration ||
		receipt.RecipeDigest != row.RecipeDigest ||
		receipt.ResolvedRecipeDigest != row.ResolvedRecipeDigest {
		return nil, fmt.Errorf("%w: stored receipt %q digest columns disagree with its document", explorer.ErrCorruptReceipt, row.ID)
	}
	return &receipt, nil
}

func (s *Store) InsertCompilationReceipt(ctx context.Context, receipt explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	if err := validateReceiptForStore(receipt); err != nil {
		return nil, fmt.Errorf("postgres explorer store: insert compilation receipt: %w", err)
	}
	doc, err := json.Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: encode compilation receipt %q: %w", receipt.ID, err)
	}
	wantKey, err := canonicalReceiptKey(receipt)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: content identity of compilation receipt %q: %w", receipt.ID, err)
	}

	var lastErr error
	for attempt := 1; attempt <= receiptInsertMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("postgres explorer store: insert compilation receipt %q: %w", receipt.ID, err)
		}
		conn, err := s.client.Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("postgres explorer store: acquire receipt insert connection: %w", err)
		}
		stored, claimErr := s.claimedReceipt(ctx, conn, receipt, doc, wantKey)
		conn.Release()
		if stored != nil {
			return stored, nil
		}
		lastErr = claimErr
		if attempt == receiptInsertMaxAttempts {
			break
		}
		if waitErr := waitForStoreRetry(ctx, time.Duration(attempt)*receiptConflictRetryDelay); waitErr != nil {
			return nil, fmt.Errorf("postgres explorer store: retry compilation receipt insert %q: %w",
				receipt.ID, errors.Join(lastErr, waitErr))
		}
	}
	if lastErr == nil {
		lastErr = ErrReceiptClaimLost
	}
	return nil, fmt.Errorf("postgres explorer store: insert compilation receipt %q: %w", receipt.ID, lastErr)
}

func (s *Store) claimedReceipt(ctx context.Context, conn *pgxpool.Conn, receipt explorer.CompilationReceipt, doc []byte, wantKey string) (*explorer.CompilationReceipt, error) {
	var row storedReceipt
	err := scanReceipt(conn.QueryRow(ctx, s.receipt.insert, compilationReceiptArgs(receipt, doc)...), &row)
	if err == nil {
		stored, decodeErr := decodeStoredReceipt(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		storedKey, keyErr := canonicalReceiptKey(*stored)
		if keyErr != nil || storedKey != wantKey {
			return nil, fmt.Errorf("%w: stored receipt %q content identity disagrees with the claim", explorer.ErrCorruptReceipt, row.ID)
		}
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("postgres explorer store: claim compilation receipt %q: %w", receipt.ID, err)
	}

	existing, err := s.readReceiptRow(ctx, conn, s.receipt.claim, receipt.Project, receipt.ExplorerID, receipt.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, fmt.Errorf("%w: receipt claim %q/%q/%q vanished before it became visible",
			explorer.ErrNotFound, receipt.Project, receipt.ExplorerID, receipt.ID)
	}
	stored, decodeErr := decodeStoredReceipt(*existing)
	if decodeErr != nil {
		return nil, decodeErr
	}
	storedKey, keyErr := canonicalReceiptKey(*stored)
	if keyErr != nil || storedKey != wantKey {
		return nil, fmt.Errorf("%w: receipt id %q was reused with different content", explorer.ErrCorruptReceipt, receipt.ID)
	}
	return stored, nil
}

func canonicalReceiptKey(receipt explorer.CompilationReceipt) (string, error) {
	return explorer.ReceiptID(receipt)
}

func (s *Store) GetCompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*explorer.CompilationReceipt, error) {
	conn, err := s.client.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("postgres explorer store: acquire receipt read connection: %w", err)
	}
	defer conn.Release()

	row, err := s.readReceiptRow(ctx, conn, s.receipt.get, project, explorerID, id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, explorer.ErrNotFound
	}
	return decodeStoredReceipt(*row)
}

func (s *Store) readReceiptRow(ctx context.Context, conn *pgxpool.Conn, statement, project, explorerID, id string) (*storedReceipt, error) {
	var row storedReceipt
	err := scanReceipt(conn.QueryRow(ctx, statement, project, explorerID, id), &row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("postgres explorer store: read compilation receipt %q/%q/%q: %w", project, explorerID, id, err)
	}
	return &row, nil
}

func waitForStoreRetry(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
