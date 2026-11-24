package lifecycle

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/publication"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/population"

	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/rowdef"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/semantic"
	semanticarango "github.com/calypr/loom/internal/semantic/arango"
)

type ReceiptReader func(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error)
type ReceiptPreviewer func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error)
type ReceiptStreamValidator func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings) error

type CategoryScanner func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error)

type ColumnPopulationScanner func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.ColumnPopulationRequest) (dataframeexecution.ColumnPopulationResult, error)

type CellTraceExecutor func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error)

type RowLineageExecutor func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error)

type Config struct {
	DefaultDataframeWorkspace func(context.Context, string, capability.Snapshot, ws.CatalogSnapshot) (ws.Workspace, error)
	AuthoringNetwork          func(context.Context, string, ports.AuthorizedCapability, ws.Document) (authoringnetwork.AuthoringNetwork, error)
	EditImportedRelationship  func(context.Context, string, ports.AuthorizedCapability, ws.Document, authoringv2.ImportedRelationshipChange) (ws.Document, error)
	CreateImportedSourceRule  func(context.Context, string, ports.AuthorizedCapability, ws.Document, ws.SourceRuleCreate) (ws.Document, error)
	EditImportedSource        func(context.Context, string, ports.AuthorizedCapability, ws.Document, *authoringv2.ImportedSourceStepChange) (ws.Document, error)
	EditImportedSources       func(context.Context, string, ports.AuthorizedCapability, ws.Document, []authoringv2.ImportedSourceStepChange) (ws.Document, error)
	RemoveImportedField       func(context.Context, string, ports.AuthorizedCapability, ws.Document, string, string) (ws.Document, error)
	RemoveImportedOutput      func(context.Context, string, ports.AuthorizedCapability, ws.Document, string, string) (ws.Document, error)

	SelectionMembersCollection         string
	Capability                         ports.CapabilityResolver
	SemanticInventory                  func(context.Context, semantic.PageOptions) (semantic.Page, error)
	SemanticCoverage                   func(context.Context, semanticarango.SemanticCoverageLookupOptions) (semanticarango.SemanticCoverageLookupResult, error)
	ResolveSemanticInventorySelections func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)

	InterpretationRepository interpretation.Repository
	ExplorerOwners           explorer.ExplorerOwnerStore
	DraftStore               explorer.DraftStore
	DraftRevisions           explorer.DraftRevisionReader
	Revisions                explorer.RevisionReader
	RevisionWriter           explorer.RevisionWriter
	Receipts                 explorer.CompilationReceiptStore
	AuthoringPublisher       explorer.AuthoringPublisher
	RepositoryActivator      explorer.RepositoryGenerationActivator
	SelectionStore           explorerselection.Store

	SelectionSourceResolver     explorerselection.SelectionSourceResolver
	SelectionReferenceValidator explorerselection.SelectionReferenceValidator
	RowChoiceResolver           rowdef.RowChoiceResolver
	RowChoicePlanner            rowdef.RowChoicePlanner
	ExplicitGroupResolver       explicitgroup.ExplicitGroupRevisionResolver
	ExplicitGroupRepository     explorerselection.ExplicitGroupRepository

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

	ValidateReleaseGeneration publication.GenerationValidator
	ActivateRelease           publication.ReleaseActivator
	PrepareRelease            publication.ReleasePreparer
	PersistPublishedWorkspace func(context.Context, string, string, []byte) error
	Now                       func() time.Time
}

type ListResult struct {
	Project   string
	Summaries []viewer.ExplorerSummaryV1
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
	Candidates    []ws.CatalogCandidate
}

type BuilderRequest struct {
	Project    string
	ExplorerID string
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
	Receipt   *receiptmodel.CompilationReceipt
	Workspace ws.Workspace
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
