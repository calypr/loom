package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	dataframepublished "github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/tableshapecap"
)

type CapabilityResolver struct {
	Current           func(context.Context, string, string, string) (capability.Snapshot, error)
	Token             func(context.Context, string, string) (capability.Snapshot, error)
	ForCompilation    func(context.Context, string, string) (AuthorizedCapability, error)
	ForExecution      func(context.Context, string, string) (AuthorizedCapability, error)
	Catalog           func(capability.Snapshot, string) authoringv2.CatalogSnapshot
	ValidateReadScope func(authscope.ReadScope, string) error
}

type AuthorizedCapability struct {
	Snapshot capability.Snapshot
	Scope    authscope.ReadScope
}

func (a AuthorizedCapability) Clone() AuthorizedCapability {
	return AuthorizedCapability{Snapshot: a.Snapshot.Clone(), Scope: a.Scope.Clone()}
}

type CompileReceiptRequest struct {
	Project                         string
	ExplorerID                      string
	Workspace                       authoringv2.Workspace
	SnapshotToken                   string
	RequestID                       string
	Authorized                      AuthorizedCapability
	ResolvedInputs                  explorercompilation.ResolvedInputs
	SelectionMembersCollection      string
	RowDefinitionProposal           *explorer.RowDefinitionProposalBinding
	TableShapeProposal              *explorer.TableShapeProposalBinding
	ConstructionProposal            *explorer.ConstructionProposalBinding
	PopulationMemberRemovalProposal *explorer.PopulationMemberRemovalProposalBinding
}

type ReceiptCompiler func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error)
type ReceiptReader func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
type ReceiptPreviewer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error)
type ReceiptStreamValidator func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) error

type ConstructionSourceStageCompiler func(context.Context, ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error)

type ConstructionSourceStageRequest struct {
	Project                    string
	ExplorerID                 string
	OutputID                   string
	Document                   authoringv2.Document
	Authorized                 AuthorizedCapability
	SelectionMembersCollection string
}

type CategoryScanner func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error)

type ArtifactPublishedReader interface {
	ExactExecutionMaterialization(context.Context, string, string) (dataframepublished.Materialization, error)
	StreamExactExport(context.Context, dataframepublished.ExactExportRequest, dataframepublished.ExactExportVisitor) (dataframepublished.ExactExportResult, error)
}

type PopulationMappingExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, string, []string, string, int) (dataframeexecution.PopulationMappingResult, error)

type CellTraceExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error)

type RowLineageExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error)

type TableShapeExclusionExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.TableShapeExclusionRequest) (dataframeexecution.TableShapeExclusionResult, error)

type SelectionReferenceValidator func(context.Context, string, string, authscope.ReadScope, []explorer.ResourceRef) error

type Execution struct {
	ID                   string
	Name                 string
	Project              string
	RecipeDigest         string
	ResolvedSchemaDigest string
	SourceGeneration     string
	ScopeDigest          string
	State                string
	Outputs              []ExecutionOutput
	QualityReports       []publication.QualityReport
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

type Config struct {
	SelectionMembersCollection         string
	Capability                         CapabilityResolver
	SemanticInventory                  func(context.Context, catalog.SemanticInventoryPageOptions) (catalog.SemanticInventoryPage, error)
	SemanticCoverage                   func(context.Context, catalog.SemanticCoverageLookupOptions) (catalog.SemanticCoverageLookupResult, error)
	ResolveSemanticInventorySelections func(context.Context, catalog.SemanticInventoryResolveOptions) (catalog.SemanticInventoryResolveResult, error)
	InterpretationRepository           explorer.InterpretationRepository
	SelectionSourceResolver            SelectionSourceResolver
	SelectionReferenceValidator        SelectionReferenceValidator
	RowChoiceResolver                  RowChoiceResolver
	RowChoicePlanner                   RowChoicePlanner
	ExplicitGroupResolver              ExplicitGroupRevisionResolver
	ExplicitGroupRepository            explorer.ExplicitGroupRepository

	CompileReceipt               ReceiptCompiler
	ConstructionSourceStage      ConstructionSourceStageCompiler
	PreviewReceipt               ReceiptPreviewer
	ValidateReceiptStream        ReceiptStreamValidator
	TableShapeCapabilities       tableshapecap.Repository
	ScanCategories               CategoryScanner
	PopulationMapping            PopulationMappingExecutor
	PopulationMappingCursorCodec PopulationMappingCursorCodec
	CellTrace                    CellTraceExecutor
	RowLineage                   RowLineageExecutor
	TableShapeExclusions         TableShapeExclusionExecutor
	MaterializeReceipt           ReceiptMaterializer
	ReceiptLookup                ReceiptReader
	ArtifactStore                explorer.ArtifactStore
	PublishedReader              ArtifactPublishedReader
	ArtifactTTL                  time.Duration
	ArtifactMaxRows              int64
	ArtifactMaxBytes             int64

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
	Project                         string
	ExplorerID                      string
	Workspace                       authoringv2.Workspace
	SnapshotToken                   string
	RequestID                       string
	RowDefinitionProposal           *explorer.RowDefinitionProposalBinding
	TableShapeProposal              *explorer.TableShapeProposalBinding
	ConstructionProposal            *explorer.ConstructionProposalBinding
	PopulationMemberRemovalProposal *explorer.PopulationMemberRemovalProposalBinding
}

type ReconcileRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	DraftVersion  int64
	DraftDigest   string
	OutputID      string
}

type ReconcileResult struct {
	Receipt   *explorer.CompilationReceipt
	Workspace authoringv2.Workspace
}

type AssessRowChangeRequest struct {
	Mode             string
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
	SnapshotToken      string
	DraftVersion       int64
	DraftDigest        string
	CandidateReceiptID string
	Assessment         authoringv2.RowChangeAssessment
}

type PreviewRequest struct {
	Project     string
	ExplorerID  string
	ReceiptID   string
	OutputID    string
	Limit       int
	SinkFactory func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error)
}

type PreviewResult struct {
	Receipt *explorer.CompilationReceipt
	Columns []explorer.EmittedColumn
	Summary dataframeexecution.PreviewSummary
}

type PreviewSinkFactory func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error)

type AcceptedConstruction struct {
	Preview      PreviewResult
	PreviewLimit int
}

type ApplyCommandsWithConstructionPreviewResponse struct {
	Commands *authoringv2.ApplyCommandsResponse
	Accepted *AcceptedConstruction
}

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

type InterpretationCandidatePreviewSample struct {
	RowID  string                `json:"rowId"`
	Before map[string]any        `json:"before"`
	After  map[string]any        `json:"after"`
	State  CandidatePreviewState `json:"state"`
}

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

type InterpretationLibraryView struct {
	Library explorer.InterpretationLibrary
	Head    *explorer.InterpretationRevision
}

type ListInterpretationLibrariesResult struct {
	Project   string
	Libraries []InterpretationLibraryView
}

type GetInterpretationRevisionRequest struct {
	Project    string
	RevisionID string
}

type CreateInterpretationRevisionRequest struct {
	Project          string
	LibraryID        string
	ParentRevisionID string
	Applicability    explorer.InterpretationApplicability
	Rules            []explorer.InterpretationRule
	Explanation      string
	Author           string
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

type CellTraceRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	RowID      string
	Column     string
	Offset     int
	Limit      int
}

type RowLineageRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	RowID      string
	Offset     int
	Limit      int
}

type RowLineageResult struct {
	ReceiptID    string
	OutputID     string
	RowID        string
	Status       string
	Contributors []dataframeexecution.RowLineageContributor
	HasMore      bool
	NextOffset   *int
	ReasonCode   string
	Operation    string
}

type CellTraceBinding struct {
	ReceiptID   string `json:"receiptId"`
	OutputID    string `json:"outputId"`
	Project     string `json:"project"`
	ExplorerID  string `json:"explorerId"`
	Generation  string `json:"generation"`
	ScopeDigest string `json:"scopeDigest"`
}

type CellTraceResult struct {
	Binding CellTraceBinding                   `json:"binding"`
	Feature CellTraceFeature                   `json:"feature"`
	Trace   dataframeexecution.CellTraceResult `json:"trace"`
}

type CellTraceFeature struct {
	OutputID           string   `json:"outputId"`
	Column             string   `json:"column"`
	AuthoredColumn     string   `json:"authoredColumn"`
	OccurrenceID       string   `json:"occurrenceId"`
	Label              string   `json:"label"`
	LogicalType        string   `json:"logicalType"`
	SourceResourceType string   `json:"sourceResourceType,omitempty"`
	SourcePath         string   `json:"sourcePath,omitempty"`
	ProjectionMode     string   `json:"projectionMode,omitempty"`
	Lossless           bool     `json:"lossless"`
	LossReasons        []string `json:"lossReasons"`
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
