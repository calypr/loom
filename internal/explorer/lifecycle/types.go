package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/population"

	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/rowdef"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
)

type ReceiptReader func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
type ReceiptPreviewer func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error)
type ReceiptStreamValidator func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings) error

type CategoryScanner func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error)

type ColumnPopulationScanner func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.ColumnPopulationRequest) (dataframeexecution.ColumnPopulationResult, error)

type CellTraceExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error)

type RowLineageExecutor func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error)

type GenerationValidator func(context.Context, string, string) error
type ReleaseActivator func(context.Context, string, string, []dataset.DataframeSelector) error
type ReleasePreparer func(context.Context, string, string, []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error)

type Config struct {
	DefaultDataframeWorkspace func(context.Context, string, capability.Snapshot, authoringv2.CatalogSnapshot) (authoringv2.Workspace, error)
	AuthoringNetwork          func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document) (authoringv2.AuthoringNetwork, error)
	EditImportedRelationship  func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, authoringv2.ImportedRelationshipChange) (authoringv2.Document, error)
	CreateImportedSourceRule  func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, authoringv2.SourceRuleCreate) (authoringv2.Document, error)
	EditImportedSource        func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, *authoringv2.ImportedSourceStepChange) (authoringv2.Document, error)
	EditImportedSources       func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, []authoringv2.ImportedSourceStepChange) (authoringv2.Document, error)
	RemoveImportedField       func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, string, string) (authoringv2.Document, error)
	RemoveImportedOutput      func(context.Context, string, ports.AuthorizedCapability, authoringv2.Document, string, string) (authoringv2.Document, error)

	SelectionMembersCollection         string
	Capability                         ports.CapabilityResolver
	SemanticInventory                  func(context.Context, semantic.PageOptions) (semantic.Page, error)
	SemanticCoverage                   func(context.Context, semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error)
	ResolveSemanticInventorySelections func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)

	InterpretationRepository interpretation.Repository

	SelectionSourceResolver     SelectionSourceResolver
	SelectionReferenceValidator SelectionReferenceValidator
	RowChoiceResolver           rowdef.RowChoiceResolver
	RowChoicePlanner            rowdef.RowChoicePlanner
	ExplicitGroupResolver       explicitgroup.ExplicitGroupRevisionResolver
	ExplicitGroupRepository     explorer.ExplicitGroupRepository

	CompileReceipt               ports.ReceiptCompiler
	ConstructionSourceStage      ports.ConstructionSourceStageCompiler
	PreviewReceipt               ReceiptPreviewer
	ValidateReceiptStream        ReceiptStreamValidator
	ScanCategories               CategoryScanner
	ScanColumnPopulation         ColumnPopulationScanner
	PopulationMapping            population.PopulationMappingExecutor
	PopulationMappingCursorCodec *population.HMACPopulationMappingCursorCodec
	CellTrace                    CellTraceExecutor
	RowLineage                   RowLineageExecutor
	MaterializeReceipt           ports.ReceiptMaterializer
	ReceiptLookup                ReceiptReader
	ArtifactStore                explorer.ArtifactStore
	PublishedReader              ports.ArtifactPublishedReader

	ArtifactTTL      time.Duration
	ArtifactMaxRows  int64
	ArtifactMaxBytes int64

	ConstructionBaseCacheTTL     time.Duration
	ConstructionBaseCacheEntries int

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
	ColumnPolicyProposal            *explorer.ColumnPolicyProposalBinding
	SourceRelationshipProposal      *explorer.SourceRelationshipProposalBinding
	RowDefinitionProposal           *explorer.RowDefinitionProposalBinding
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

type PreviewRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	Limit      int

	AllowSampledAggregatePreview bool

	SinkFactory func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error)
}

type ApplyCommandsWithConstructionPreviewResponse struct {
	Commands *authoringv2.ApplyCommandsResponse
	Accepted *construction.AcceptedConstruction
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
	Execution ports.Execution
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
	Execution        ports.Execution
	Materializations []explorer.Materialization
	Dataset          explorer.DatasetMetadata
	Publication      explorer.PublicationMetadata
}
