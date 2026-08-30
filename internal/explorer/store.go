package explorer

import (
	"context"
	"errors"

	"github.com/calypr/loom/internal/dataset"
)

var (
	ErrNotFound                 = errors.New("explorer not found")
	ErrDraftConflict            = errors.New("explorer draft conflict")
	ErrAuthoringCommandConflict = errors.New("Explorer authoring command ID was reused with different intent")
	ErrImmutableRevision        = errors.New("explorer revision content is immutable")
	ErrStaleCatalog             = errors.New("stale Explorer authoring catalog")
	ErrIncompleteCatalog        = errors.New("Explorer catalog discovery is incomplete")
	// ErrCorruptReceipt means that a persisted receipt does not match its
	// content-addressed identity, or that an immutable key was reused for a
	// different receipt. Callers must fail closed rather than execute it.
	ErrCorruptReceipt = errors.New("corrupt compilation receipt")
)

// Store deliberately exposes only lifecycle mutations for revisions. An
// adapter must never replace definition, recipe, digests, source generation,
// or frozen materialization mappings after InsertRevision succeeds.
type Store interface {
	List(context.Context, string) ([]Explorer, error)
	Get(context.Context, string, string) (*Explorer, error)
	Create(context.Context, Explorer) (*Explorer, error)
	// SaveDraft is an optimistic compare-and-swap. Adapters must reject a
	// mismatched expected version/digest and increment the persisted version.
	SaveDraft(context.Context, Explorer, int64, ...string) (*Explorer, error)
	InsertCompilationReceipt(context.Context, CompilationReceipt) (*CompilationReceipt, error)
	GetCompilationReceiptForExplorer(context.Context, string, string, string) (*CompilationReceipt, error)
	PublishAuthoring(context.Context, CompilationReceipt, Revision, dataset.ProjectRelease, int64) (*Revision, error)
	InsertRevision(context.Context, Revision) (*Revision, error)
	GetRevision(context.Context, string) (*Revision, error)
	FailRevision(context.Context, string, []Diagnostic) (*Revision, error)
	ActivateRepositoryGeneration(context.Context, string, string, string) error
}
