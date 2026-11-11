package postgres

// Compilation receipts on PostgreSQL 18.
//
// Parity contract (internal/explorer/arango/receipts.go): insert-once,
// retry conflicts by scoped reread, compare the validated canonical
// ReceiptID digest -- jsonb object order never replaces the digest check --
// and report corrupt-ID reuse as explorer.ErrCorruptReceipt. The relational
// claim is INSERT ... ON CONFLICT (project, explorer_id, id) DO NOTHING;
// when the claim produced no row, the reread runs FOR UPDATE so it waits for
// a concurrent in-flight winner to commit instead of racing past it, and the
// insert is retried a bounded number of times if that winner aborted -- the
// same bounded conflict retry the Arango adapter applies to write conflicts.
//
// Every read is tenant-scoped by (project, explorer_id): a receipt that
// exists under another Explorer or project is indistinguishable from one
// that does not exist. There is no fallback to any other backend: every
// failure is returned typed for the caller to surface.

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

// ReceiptsTable is the compilation receipt table created by migration
// 000005_explorer_compilation_receipt.sql inside the store's schema.
const ReceiptsTable = "compilation_receipts"

const (
	// receiptInsertMaxAttempts and receiptConflictRetryDelay mirror the
	// Arango adapter's bounded conflict-retry budget.
	receiptInsertMaxAttempts  = 4
	receiptConflictRetryDelay = 5 * time.Millisecond
)

// ErrReceiptClaimLost reports that every insert attempt lost the claim race
// to a winner whose transaction aborted, so no row ever became visible.
// It is a genuine race outcome, never a silent success: the caller must
// surface it.
var ErrReceiptClaimLost = errors.New("postgres explorer store: receipt insert lost every claim race to an aborted winner")

// compilationReceiptColumns is the single source of truth for the compilation
// receipts column order: the INSERT
// column list, the RETURNING/SELECT column list, the argument order, and the
// scan order all derive from it, so ordered SQL parameters ($1..$N) match
// the Go argument slice by construction.
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

// receiptStatements are the backend-tagged statements of the receipt store.
type receiptStatements struct {
	insert string // INSERT ... ON CONFLICT (project, explorer_id, id) DO NOTHING RETURNING
	claim  string // tenant-scoped SELECT ... FOR UPDATE (waits for an in-flight winner)
	get    string // tenant-scoped SELECT
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

// storedReceipt is the physically stored form of one receipt row: identity
// and guard-visible scope columns plus the full serialized document.
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

// scanReceipt decodes one row into a storedReceipt. Targets follow
// compilationReceiptColumns order.
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

// compilationReceiptArgs maps a receipt and its pre-encoded document to column values
// in compilationReceiptColumns order.
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

// validateReceiptForStore mirrors the Arango adapter's insert validation
// (internal/explorer/arango/receipts.go validateReceipt): an empty ID is
// corrupt, a receipt carrying its contract fields must pass Validate, and a
// content-addressed "receipt_<sha256hex>" ID must match its canonical
// ReceiptID digest.
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

// decodeStoredReceipt reconstructs the receipt from its stored document and
// fails closed when the row is internally inconsistent: an undecodable or
// invalid document, or scope/digest columns that disagree with the document,
// are explorer.ErrCorruptReceipt -- callers must never execute a receipt the
// store cannot vouch for.
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

// InsertCompilationReceipt stores a compilation receipt exactly once.
//
//   - A first insert stores the row and returns what PostgreSQL persisted.
//   - Re-inserting the same (project, explorer_id, id) with the same
//     canonical content (same ReceiptID; RequestID and CreatedAt are
//     excluded from the content identity by design) is idempotent: nothing
//     is written and the stored row is returned.
//   - Reusing the same ID with different content is a typed
//     explorer.ErrCorruptReceipt refusal -- the corrupt-ID-reuse report the
//     Arango adapter gives -- and the stored row is never overwritten.
//   - Concurrent identical writers all succeed: the claim loser rereads the
//     winner's row (waiting on its in-flight insert through FOR UPDATE) and
//     returns it. If a winner aborts, the insert is retried a bounded number
//     of times; losing every race surfaces ErrReceiptClaimLost.
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

// claimedReceipt performs one insert attempt on conn. It returns the
// resolved stored receipt on success, or (nil, err) describing why the
// attempt should be retried or surfaced.
func (s *Store) claimedReceipt(ctx context.Context, conn *pgxpool.Conn, receipt explorer.CompilationReceipt, doc []byte, wantKey string) (*explorer.CompilationReceipt, error) {
	var row storedReceipt
	err := scanReceipt(conn.QueryRow(ctx, s.receipt.insert, compilationReceiptArgs(receipt, doc)...), &row)
	if err == nil {
		// This writer won the claim: verify what PostgreSQL persisted and
		// return it, never the in-memory input.
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

	// ON CONFLICT DO NOTHING produced no row: the identity already exists or
	// is being claimed right now. Reread FOR UPDATE so the claim waits for
	// an in-flight winner instead of racing past it.
	existing, err := s.readReceiptRow(ctx, conn, s.receipt.claim, receipt.Project, receipt.ExplorerID, receipt.ID)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		// The winner aborted before committing: retry the insert.
		return nil, fmt.Errorf("%w: receipt claim %q/%q/%q vanished before it became visible",
			explorer.ErrNotFound, receipt.Project, receipt.ExplorerID, receipt.ID)
	}
	stored, decodeErr := decodeStoredReceipt(*existing)
	if decodeErr != nil {
		return nil, decodeErr
	}
	storedKey, keyErr := canonicalReceiptKey(*stored)
	if keyErr != nil || storedKey != wantKey {
		// Same ID, different canonical content: corrupt-ID reuse, refused
		// with the stored row left exactly as it is.
		return nil, fmt.Errorf("%w: receipt id %q was reused with different content", explorer.ErrCorruptReceipt, receipt.ID)
	}
	return stored, nil
}

// canonicalReceiptKey is the canonical content identity used for collision
// checks. It is the exported ReceiptID digest, so jsonb normalization can
// never change it.
func canonicalReceiptKey(receipt explorer.CompilationReceipt) (string, error) {
	return explorer.ReceiptID(receipt)
}

// GetCompilationReceiptForExplorer returns the receipt for one Explorer.
// The read is tenant-scoped: a receipt that exists only under another
// project or another Explorer returns explorer.ErrNotFound with no
// information leak. The returned receipt is verified against its stored
// document; a row that no longer matches its content identity is
// explorer.ErrCorruptReceipt, never a silent pass.
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

// readReceiptRow executes one tenant-scoped receipt statement. A missing
// row yields (nil, nil); callers choose the missing-row meaning.
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

// waitForStoreRetry sleeps d unless ctx is cancelled first.
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
