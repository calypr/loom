package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/calypr/loom/internal/explorer"
)

func (s *Store) InsertCompilationReceipt(ctx context.Context, receipt explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	doc, err := document(receipt, receipt.ID)
	if err != nil {
		return nil, err
	}
	// INSERT with overwriteMode=ignore makes the key immutable under retries
	// and concurrent requests. The read below is authoritative and compares
	// the content-addressed identity, detecting a corrupted key collision.
	err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": CompilationReceiptsCollection, "doc": doc}, func(map[string]any) error { return nil })
	if err != nil {
		return nil, err
	}
	return s.GetCompilationReceiptForExplorer(ctx, receipt.Project, receipt.ExplorerID, receipt.ID)
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

func sameReceipt(a, b explorer.CompilationReceipt) bool {
	left, leftErr := explorer.ReceiptID(a)
	right, rightErr := explorer.ReceiptID(b)
	return leftErr == nil && rightErr == nil && left == right
}
