package explorer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

var (
	ErrNotFound          = errors.New("explorer not found")
	ErrDraftConflict     = errors.New("explorer draft conflict")
	ErrImmutableRevision = errors.New("explorer revision content is immutable")
	ErrStaleCatalog      = errors.New("stale Explorer authoring catalog")
	ErrIncompleteCatalog = errors.New("Explorer catalog discovery is incomplete")
	// ErrCorruptReceipt means that a persisted receipt does not match its
	// content-addressed identity, or that an immutable key was reused for a
	// different receipt. Callers must fail closed rather than execute it.
	ErrCorruptReceipt = errors.New("corrupt compilation receipt")
)

// ReceiptStoreStats is intentionally read-only and approximate. ApproxBytes
// is a serialized-document estimate, suitable for capacity observations, not
// billing or quota enforcement.
type ReceiptStoreStats struct {
	Count             int
	ApproxBytes       int64
	OldestCreatedAt   time.Time
	UnreferencedCount int
}

func validateCompilationReceipt(receipt CompilationReceipt) error {
	if strings.TrimSpace(receipt.ID) == "" {
		return ErrCorruptReceipt
	}
	// New-format receipts are validated as executable artifacts. A few pre-v2
	// fixtures used human-readable IDs; keep accepting those
	// during the migration, but every content-addressed receipt is checked on
	// every read and write.
	if receipt.ReceiptFormatVersion != 0 || receipt.CompilerContractVersion != "" || receipt.CompilationKey != "" {
		if err := receipt.Validate(); err != nil {
			return err
		}
	}
	if strings.HasPrefix(receipt.ID, "receipt_") && looksContentAddressedReceiptID(receipt.ID) {
		if err := receipt.ValidateID(); err != nil {
			return ErrCorruptReceipt
		}
	}
	return nil
}

func looksContentAddressedReceiptID(id string) bool {
	const prefix = "receipt_"
	return len(id) == len(prefix)+sha256.Size*2 && hexEncoded(id[len(prefix):])
}

func hexEncoded(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameCompilationReceipt(a, b CompilationReceipt) bool {
	left, leftErr := ReceiptID(a)
	right, rightErr := ReceiptID(b)
	return leftErr == nil && rightErr == nil && left == right
}

// Store deliberately exposes only lifecycle mutations for revisions.  An
// adapter must never replace definition, recipe, digests, source generation,
// or frozen materialization mappings after InsertRevision succeeds.
type Store interface {
	ListConfigs(context.Context, string) ([]RepositoryConfig, error)
	GetConfig(context.Context, string, string) (*RepositoryConfig, error)
	SaveConfig(context.Context, RepositoryConfig) (*RepositoryConfig, error)
	GetRepositoryConfig(context.Context, string) (*RepositoryConfig, error)
	SaveRepositoryConfig(context.Context, RepositoryConfig) (*RepositoryConfig, error)
	List(context.Context, string) ([]Explorer, error)
	Get(context.Context, string, string) (*Explorer, error)
	CreateInteractive(context.Context, Explorer) (*Explorer, error)
	CreateRepository(context.Context, Explorer) (*Explorer, error)
	// expected and expectedDigest are retained for adapter compatibility and
	// observability. Draft writes are last-write-wins; adapters must increment
	// the stored draft version from the current value rather than trusting the
	// caller's expected version.
	SaveDraft(context.Context, Explorer, int64, ...string) (*Explorer, error)
	InsertCompilationReceipt(context.Context, CompilationReceipt) (*CompilationReceipt, error)
	GetCompilationReceipt(context.Context, string) (*CompilationReceipt, error)
	// GetCompilationReceiptForExplorer is the tenant-scoped form. The legacy
	// ID-only method remains temporarily for old publication callers.
	GetCompilationReceiptForExplorer(context.Context, string, string, string) (*CompilationReceipt, error)
	GetCompilationReceiptByCompilationKey(context.Context, string, string, string) (*CompilationReceipt, error)
	CompilationReceiptStats(context.Context, string) (ReceiptStoreStats, error)
	PublishAuthoring(context.Context, CompilationReceipt, Revision) (*Revision, error)
	InsertRevision(context.Context, Revision) (*Revision, error)
	GetRevision(context.Context, string) (*Revision, error)
	TransitionRevision(context.Context, string, RevisionStatus, []Diagnostic) (*Revision, error)
	ActivateInteractive(context.Context, string, string, string) error
	ActivateRepository(context.Context, string, string) error
	ActivateRepositoryGeneration(context.Context, string, string, string) error
	// PurgeDraftAuthoring irreversibly removes every legacy/draft authoring
	// field and compilation receipt not referenced by an immutable revision.
	PurgeDraftAuthoring(context.Context) error
}
