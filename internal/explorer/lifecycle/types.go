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
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
)

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
	Project                    string
	ExplorerID                 string
	Workspace                  authoringv2.Workspace
	SnapshotToken              string
	RequestID                  string
	Authorized                 AuthorizedCapability
	ResolvedInputs             explorercompilation.ResolvedInputs
	SelectionMembersCollection string
}

type ReceiptCompiler func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error)
type ReceiptReader func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
type ReceiptPreviewer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error)

// PopulationMappingExecutor is the narrow lifecycle-to-execution adapter.
// Lifecycle supplies the validated receipt, scope bindings, and immutable
// selected IDs; execution owns compiler witness interpretation.
type PopulationMappingExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, string, []string, string, int) (dataframeexecution.PopulationMappingResult, error)

// SelectionReferenceValidator resolves explicit references against the
// authorized active generation. It must verify both resource existence and
// auth_resource_path before lifecycle persists any member or exclusion.
type SelectionReferenceValidator func(context.Context, string, string, authscope.ReadScope, []explorer.ResourceRef) error

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
	// SelectionMembersCollection is a deployment-owned runtime binding. It is
	// never copied into authoring intent, recipes, or receipt identity.
	SelectionMembersCollection string
	Capability                 CapabilityResolver
	// InterpretationRepository resolves exact immutable revision IDs. It is
	// intentionally narrow so lifecycle cannot accidentally depend on heads or
	// unrelated Explorer persistence methods.
	InterpretationRepository explorer.InterpretationRepository
	// SelectionSourceResolver resolves an exact immutable published revision
	// after Capability.ForExecution has established project and scope.
	SelectionSourceResolver     SelectionSourceResolver
	SelectionReferenceValidator SelectionReferenceValidator

	CompileReceipt               ReceiptCompiler
	PreviewReceipt               ReceiptPreviewer
	PopulationMapping            PopulationMappingExecutor
	PopulationMappingCursorCodec PopulationMappingCursorCodec
	MaterializeReceipt           ReceiptMaterializer
	ReceiptLookup                ReceiptReader

	ValidateReleaseGeneration GenerationValidator
	ActivateRelease           ReleaseActivator
	PrepareRelease            ReleasePreparer
	PersistPublishedWorkspace func(context.Context, string, string, []byte) error
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

type compileRequest struct {
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

type AssessRowChangeRequest struct {
	Project          string
	ExplorerID       string
	SnapshotToken    string
	DraftVersion     int64
	DraftDigest      string
	OutputID         string
	RootNodeID       string
	RootOccurrenceID string
	RouteRebase      []authoringv2.RouteRebaseChoice
}

type AssessRowChangeResult struct {
	SnapshotToken string
	DraftVersion  int64
	DraftDigest   string
	Assessment    authoringv2.RowChangeAssessment
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

// PreviewInterpretationCandidateRequest identifies an immutable candidate
// revision and the exact draft against which it was proposed. The draft CAS
// is checked before either receipt is compiled, but this operation never
// saves the draft.
type PreviewInterpretationCandidateRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	Column               string
	RevisionID           string
	Limit                int
}

type CandidatePreviewCompleteness string

const (
	CandidatePreviewComplete   CandidatePreviewCompleteness = "COMPLETE"
	CandidatePreviewIncomplete CandidatePreviewCompleteness = "INCOMPLETE"
)

type CandidatePreviewState string

const (
	CandidatePreviewUnchanged  CandidatePreviewState = "UNCHANGED"
	CandidatePreviewChanged    CandidatePreviewState = "CHANGED"
	CandidatePreviewResolved   CandidatePreviewState = "RESOLVED"
	CandidatePreviewUnresolved CandidatePreviewState = "UNRESOLVED"
)

// InterpretationCandidatePreviewSample contains one stable output row. The
// Before and After maps are keyed by the physical public column names present
// in the preview, allowing one authored column to retain every indexed or
// repeated physical emission.
type InterpretationCandidatePreviewSample struct {
	RowID  string                `json:"rowId"`
	Before map[string]any        `json:"before"`
	After  map[string]any        `json:"after"`
	State  CandidatePreviewState `json:"state"`
}

// CandidatePreviewCounts describe only the bounded sample, never an
// inferred whole-population count.
type CandidatePreviewCounts struct {
	Compared   int `json:"compared"`
	Changed    int `json:"changed"`
	Resolved   int `json:"resolved"`
	Unresolved int `json:"unresolved"`
}

type PreviewInterpretationCandidateResult struct {
	BaseReceiptID      string                                 `json:"baseReceiptId"`
	CandidateReceiptID string                                 `json:"candidateReceiptId"`
	OutputID           string                                 `json:"outputId"`
	Column             string                                 `json:"column"`
	RevisionID         string                                 `json:"revisionId"`
	Completeness       CandidatePreviewCompleteness           `json:"completeness"`
	Samples            []InterpretationCandidatePreviewSample `json:"samples"`
	Counts             CandidatePreviewCounts                 `json:"counts"`
}

type PopulationMappingRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	Cursor     string
	Limit      int
}

type PopulationMappingBinding struct {
	ReceiptID           string
	OutputID            string
	Project             string
	ExplorerID          string
	Generation          string
	ScopeDigest         string
	SelectionRevisionID string
	MembershipDigest    string
	ResourceType        string
}

type PopulationMappingCounts struct {
	Selected    int64
	Mapped      int64
	Unmapped    int64
	EmittedRows int64
}

type PopulationMappingDiagnostic struct {
	Code    string
	Message string
}

type PopulationMappingReport struct {
	Binding     PopulationMappingBinding
	Status      dataframeexecution.PopulationMappingStatus
	Counts      *PopulationMappingCounts
	Unmapped    []explorer.ResourceRef
	NextCursor  string
	Diagnostics []PopulationMappingDiagnostic
}

type PopulationMappingResult struct {
	Report PopulationMappingReport
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
