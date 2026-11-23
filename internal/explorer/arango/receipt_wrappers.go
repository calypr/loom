package arango

import (
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/arango/publicationdb"
)

func validateReceipt(receipt explorer.CompilationReceipt) error {
	return publicationdb.ValidateReceipt(receipt)
}

func sameReceipt(a, b explorer.CompilationReceipt) bool {
	return publicationdb.SameReceipt(a, b)
}

const (
	compilationReceiptInsertMaxAttempts  = publicationdb.CompilationReceiptInsertMaxAttempts
	compilationReceiptConflictRetryDelay = publicationdb.CompilationReceiptConflictRetryDelay
)
