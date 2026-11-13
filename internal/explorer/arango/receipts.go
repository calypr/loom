package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/explorer"
)

const (
	compilationReceiptInsertMaxAttempts  = 4
	compilationReceiptConflictRetryDelay = 5 * time.Millisecond
)

func (s *Store) InsertCompilationReceipt(ctx context.Context, receipt explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	doc, err := document(receipt, receipt.ID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < compilationReceiptInsertMaxAttempts; attempt++ {
		err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": CompilationReceiptsCollection, "doc": doc}, func(map[string]any) error { return nil })
		if err == nil {
			return s.getVerifiedCompilationReceipt(ctx, receipt)
		}
		if !shared.IsArangoErrorWithErrorNum(err, shared.ErrArangoConflict) {
			return nil, err
		}

		stored, readErr := s.getVerifiedCompilationReceipt(ctx, receipt)
		if readErr == nil {
			return stored, nil
		}
		if !errors.Is(readErr, explorer.ErrNotFound) {
			return nil, fmt.Errorf("read compilation receipt after insert conflict: %w", errors.Join(err, readErr))
		}
		if attempt == compilationReceiptInsertMaxAttempts-1 {
			return nil, err
		}
		if waitErr := waitForCompilationReceiptRetry(ctx, time.Duration(attempt+1)*compilationReceiptConflictRetryDelay); waitErr != nil {
			return nil, fmt.Errorf("retry compilation receipt insert: %w", errors.Join(err, waitErr))
		}
	}
	return nil, errors.New("unreachable compilation receipt insert state")
}

func (s *Store) getVerifiedCompilationReceipt(ctx context.Context, receipt explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	stored, err := s.GetCompilationReceiptForExplorer(ctx, receipt.Project, receipt.ExplorerID, receipt.ID)
	if err != nil {
		return nil, err
	}
	// The scoped read has authenticated the stored content address. Recompute
	// only the expected body: its caller-owned maps may have changed during
	// I/O, whereas rehashing the validated stored value would be redundant.
	// Historical arbitrary IDs still need the complete content comparison.
	same := false
	if isContentAddressedReceiptID(stored.ID) {
		expectedID, identityErr := explorer.ReceiptID(receipt)
		same = identityErr == nil && expectedID == stored.ID
	} else {
		same = sameReceipt(*stored, receipt)
	}
	if !same {
		return nil, explorer.ErrCorruptReceipt
	}
	return stored, nil
}

func waitForCompilationReceiptRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Store) GetCompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*explorer.CompilationReceipt, error) {
	return s.readCompilationReceipt(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId RETURN d`, map[string]any{"@c": CompilationReceiptsCollection, "key": id, "project": project, "explorerId": explorerID})
}

func (s *Store) readCompilationReceipt(ctx context.Context, query string, binds map[string]any) (*explorer.CompilationReceipt, error) {
	var out *explorer.CompilationReceipt
	err := s.client.QueryRows(ctx, query, 1, binds, func(row map[string]any) error {
		value, decodeErr := decode[explorer.CompilationReceipt](row)
		if decodeErr != nil {
			return decodeErr
		}
		if err := validateReceipt(value); err != nil {
			return err
		}
		out = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func validateReceipt(receipt explorer.CompilationReceipt) error {
	if strings.TrimSpace(receipt.ID) == "" {
		return explorer.ErrCorruptReceipt
	}
	hasContract := receipt.ReceiptFormatVersion != 0 || receipt.CompilerContractVersion != "" || receipt.CompilationKey != ""
	if hasContract {
		if err := receipt.Validate(); err != nil {
			return err
		}
	}
	if strings.HasPrefix(receipt.ID, "receipt_") && len(receipt.ID) == len("receipt_")+sha256.Size*2 {
		if _, err := hex.DecodeString(strings.TrimPrefix(receipt.ID, "receipt_")); err != nil {
			return explorer.ErrCorruptReceipt
		}
		// Validate authenticates the ID for contract-bearing receipts. Old
		// address-only artifacts have no full contract, so retain their ID check.
		if !hasContract {
			if err := receipt.ValidateID(); err != nil {
				return explorer.ErrCorruptReceipt
			}
		}
	}
	return nil
}

func sameReceipt(a, b explorer.CompilationReceipt) bool {
	left, leftErr := explorer.ReceiptID(a)
	right, rightErr := explorer.ReceiptID(b)
	return leftErr == nil && rightErr == nil && left == right
}

// isContentAddressedReceiptID is used only after validateReceipt has verified
// the content identity. The spelling check alone never authenticates a receipt.
func isContentAddressedReceiptID(id string) bool {
	if !strings.HasPrefix(id, "receipt_") || len(id) != len("receipt_")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "receipt_"))
	return err == nil
}
