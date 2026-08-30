// Package lifecycle contains the transport-neutral Explorer application
// workflows. It owns orchestration and policy while transport packages remain
// responsible for request decoding, authorization context extraction, and
// response encoding.
package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

// Store is the persistence surface required by Explorer workflows. The
// concrete Explorer service implements this interface; tests can provide a
// small fake without constructing a database-backed service.
type Store interface {
	List(context.Context, string) ([]explorer.Explorer, error)
	Get(context.Context, string, string) (*explorer.Explorer, error)
	LoadExplorerState(context.Context, string, string) (explorer.ExplorerStateV1, error)
	CreateInteractiveFrom(context.Context, string, string, string, string, string) (*explorer.Explorer, error)
	ApplyWorkspaceCommands(context.Context, string, string, authoringv2.CatalogSnapshot, authoringv2.ApplyCommandsRequest, string) (*authoringv2.ApplyCommandsResponse, error)
	ActiveRevision(context.Context, string, string) (*explorer.Revision, error)
	CompilationReceiptForExplorer(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
	PublishAuthoring(context.Context, explorer.CompilationReceipt, explorer.Revision, dataset.ProjectRelease, int64) (*explorer.Revision, error)
	UpsertRepositoryV2(context.Context, explorer.CompilationReceipt, string, string, []explorer.Materialization, explorer.DatasetMetadata, explorer.PublicationMetadata) (*explorer.Explorer, *explorer.Revision, error)
	FailRevision(context.Context, string, []explorer.Diagnostic) (*explorer.Revision, error)
	ActivateRepositoryGeneration(context.Context, string, string, string) error
	SaveRepositoryConfig(context.Context, explorer.RepositoryConfig) (*explorer.RepositoryConfig, error)
}

// CapabilityResolver resolves the current or a retained immutable capability
// snapshot. The callbacks deliberately return domain values and do not expose
// HTTP, Fiber, GraphQL, or generated API types.
type CapabilityResolver struct {
	Current           func(context.Context, string, string, string) (capability.Snapshot, error)
	Token             func(context.Context, string, string) (capability.Snapshot, error)
	ForCompilation    func(context.Context, string, string) (AuthorizedCapability, error)
	ForExecution      func(context.Context, string, string) (AuthorizedCapability, error)
	Catalog           func(capability.Snapshot, string) authoringv2.CatalogSnapshot
	ValidateReadScope func(authscope.ReadScope, string) error
}

// AuthorizedCapability is the exact snapshot and authorization scope frozen
// into a compilation receipt or used for receipt-backed execution.
type AuthorizedCapability struct {
	Snapshot capability.Snapshot
	Scope    authscope.ReadScope
}

func (a AuthorizedCapability) Clone() AuthorizedCapability {
	return AuthorizedCapability{Snapshot: a.Snapshot.Clone(), Scope: a.Scope.Clone()}
}

type CompileReceiptRequest struct {
	Project       string
	ExplorerID    string
	Workspace     authoringv2.Workspace
	SnapshotToken string
	RequestID     string
	Authorized    AuthorizedCapability
}

type ReceiptCompiler func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error)
type ReceiptReader func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
type ReceiptPreviewer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error)

// Execution is the small logical publication result needed by Explorer. It
// intentionally avoids the GraphQL resolver's execution type.
type Execution struct {
	ID                   string
	Name                 string
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	State                string
	Outputs              []ExecutionOutput
}

type ExecutionOutput struct {
	Name     string
	State    string
	RowCount *int
	Columns  []publication.PhysicalColumn
}

type ReceiptMaterializer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) (Execution, error)
type GenerationValidator func(context.Context, string, string) error
type ReleaseActivator func(context.Context, string, string, []dataset.DataframeSelector) error
type ReleasePreparer func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error)

// Config contains deployment adapters. Lifecycle policy calls these narrow
// callbacks, but never imports the transport packages that construct them.
type Config struct {
	Capability CapabilityResolver

	CompileReceipt     ReceiptCompiler
	PreviewReceipt     ReceiptPreviewer
	MaterializeReceipt ReceiptMaterializer
	ReceiptLookup      ReceiptReader

	ValidateReleaseGeneration GenerationValidator
	ActivateRelease           ReleaseActivator
	PrepareRelease            ReleasePreparer
	Now                       func() time.Time
}

type ListResult struct {
	Project   string
	Summaries []explorer.ExplorerSummaryV1
}

type CreateRequest struct {
	Project          string
	Name             string
	Title            string
	SourceExplorerID string
	Actor            string
}

type SuggestionsRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	NodeID        string
	Query         string
}

type SuggestionsResult struct {
	SnapshotToken string
	NodeID        string
	Candidates    []authoringv2.CatalogCandidate
}

type BuilderRequest struct {
	Project    string
	ExplorerID string
}

type CompileRequest struct {
	Project       string
	ExplorerID    string
	Workspace     authoringv2.Workspace
	SnapshotToken string
	RequestID     string
}

type ReconcileRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	DraftVersion  int64
	DraftDigest   string
}

type PreviewRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	Limit      int
	// SinkFactory is supplied by the transport adapter. It receives the
	// validated receipt and emitted columns and returns a row sink for native
	// streaming previews. Lifecycle never sees the response encoder itself.
	SinkFactory func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error)
}

type PreviewResult struct {
	Receipt *explorer.CompilationReceipt
	Columns []explorer.EmittedColumn
	Summary dataframeexecution.PreviewSummary
}

type PublishRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	Actor      string
}

type PublishResult struct {
	Receipt   *explorer.CompilationReceipt
	Revision  *explorer.Revision
	Execution Execution
}

type RepositoryPublishRequest struct {
	Project    string
	Generation string
	Workspace  authoringv2.Workspace
	Commit     string
	Actor      string
}

type RepositoryPublishResult struct {
	Receipt          *explorer.CompilationReceipt
	Owner            *explorer.Explorer
	Revision         *explorer.Revision
	Execution        Execution
	Materializations []explorer.Materialization
	Dataset          explorer.DatasetMetadata
	Publication      explorer.PublicationMetadata
}
