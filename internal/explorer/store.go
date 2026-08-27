package explorer

import (
	"context"
	"errors"
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

// Store deliberately exposes only lifecycle mutations for revisions.  An
// adapter must never replace definition, recipe, digests, source generation,
// or frozen materialization mappings after InsertRevision succeeds.
type Store interface {
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
	GetCompilationReceiptForExplorer(context.Context, string, string, string) (*CompilationReceipt, error)
	GetCompilationReceiptByCompilationKey(context.Context, string, string, string) (*CompilationReceipt, error)
	PublishAuthoring(context.Context, CompilationReceipt, Revision) (*Revision, error)
	InsertRevision(context.Context, Revision) (*Revision, error)
	GetRevision(context.Context, string) (*Revision, error)
	TransitionRevision(context.Context, string, RevisionStatus, []Diagnostic) (*Revision, error)
	ActivateRepositoryGeneration(context.Context, string, string, string) error
}
