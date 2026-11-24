package publicationdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer/arango/db"
	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/arangodb/go-driver/v2/arangodb/shared"
	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

const (
	compilationReceiptInsertMaxAttempts  = 4
	compilationReceiptConflictRetryDelay = 5 * time.Millisecond
)

func (s *ReceiptStore) InsertCompilationReceipt(ctx context.Context, receipt receiptmodel.CompilationReceipt) (*receiptmodel.CompilationReceipt, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	doc, err := db.Document(receipt, receipt.ID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < compilationReceiptInsertMaxAttempts; attempt++ {
		err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": db.CompilationReceiptsCollection, "doc": doc}, func(map[string]any) error { return nil })
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
		if waitErr := store.WaitForConflictRetry(ctx, time.Duration(attempt+1)*compilationReceiptConflictRetryDelay); waitErr != nil {
			return nil, fmt.Errorf("retry compilation receipt insert: %w", errors.Join(err, waitErr))
		}
	}
	return nil, errors.New("unreachable compilation receipt insert state")
}

func (s *ReceiptStore) getVerifiedCompilationReceipt(ctx context.Context, receipt receiptmodel.CompilationReceipt) (*receiptmodel.CompilationReceipt, error) {
	stored, err := s.GetCompilationReceiptForExplorer(ctx, receipt.Project, receipt.ExplorerID, receipt.ID)
	if err != nil {
		return nil, err
	}

	same := false
	if isContentAddressedReceiptID(stored.ID) {
		expectedID, identityErr := receiptmodel.ReceiptID(receipt)
		same = identityErr == nil && expectedID == stored.ID
	} else {
		same = sameReceipt(*stored, receipt)
	}
	if !same {
		return nil, explorer.ErrCorruptReceipt
	}
	return stored, nil
}

func (s *ReceiptStore) GetCompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*receiptmodel.CompilationReceipt, error) {
	return s.readCompilationReceipt(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId RETURN d`, map[string]any{"@c": db.CompilationReceiptsCollection, "key": id, "project": project, "explorerId": explorerID})
}

func (s *ReceiptStore) readCompilationReceipt(ctx context.Context, query string, binds map[string]any) (*receiptmodel.CompilationReceipt, error) {
	var out *receiptmodel.CompilationReceipt
	err := s.client.QueryRows(ctx, db.MigrationJSONQuery(query, true), 1, binds, func(row map[string]any) error {
		value, decodeErr := db.DecodeMigrationJSON[receiptmodel.CompilationReceipt](row["migrationJSON"])
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

func validateReceipt(receipt receiptmodel.CompilationReceipt) error {
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

		if !hasContract {
			if err := receipt.ValidateID(); err != nil {
				return explorer.ErrCorruptReceipt
			}
		}
	}
	return nil
}

func sameReceipt(a, b receiptmodel.CompilationReceipt) bool {
	left, leftErr := receiptmodel.ReceiptID(a)
	right, rightErr := receiptmodel.ReceiptID(b)
	return leftErr == nil && rightErr == nil && left == right
}

func isContentAddressedReceiptID(id string) bool {
	if !strings.HasPrefix(id, "receipt_") || len(id) != len("receipt_")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(id, "receipt_"))
	return err == nil
}

func ValidateReceipt(receipt receiptmodel.CompilationReceipt) error {
	return validateReceipt(receipt)
}

const (
	CompilationReceiptInsertMaxAttempts  = compilationReceiptInsertMaxAttempts
	CompilationReceiptConflictRetryDelay = compilationReceiptConflictRetryDelay
)
