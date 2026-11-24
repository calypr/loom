package construction

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/rowsource"
	"github.com/calypr/loom/internal/semantic"
)

type CategoryScanner func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error)

type ColumnPopulationScanner func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.ColumnPopulationRequest) (dataframeexecution.ColumnPopulationResult, error)

type AcceptedConstruction struct {
	Preview      authoringcore.PreviewResult
	PreviewLimit int
}

type PreviewSinkFactory = authoringcore.PreviewSinkFactory

type RowChoiceResolver = ports.RowChoiceResolver
type RowChoicePlanner = ports.RowChoicePlanner
type RowChoiceResolveRequest = ports.RowChoiceResolveRequest

const (
	RowChoiceFieldGroup = ports.RowChoiceFieldGroup
	RowChoiceExpanded   = ports.RowChoiceExpanded
)

type Store interface {
	ActiveRevision(context.Context, string, string) (*explorer.Revision, error)
	RevisionWorkspace(context.Context, explorer.Revision) (ws.Workspace, error)
}

type authoringOperations interface {
	Compile(context.Context, authoringcore.CompileRequest) (*receiptmodel.CompilationReceipt, error)
	GetOwner(context.Context, string, string) (*explorer.Explorer, error)
	LookupReceipt(context.Context, string, string, string) (*receiptmodel.CompilationReceipt, error)
	VerifyProposalReceipt(context.Context, string, *receiptmodel.CompilationReceipt, string, string, string, capability.Snapshot, *ws.Workspace) (ws.Workspace, error)
	Preview(context.Context, authoringcore.PreviewRequest) (authoringcore.PreviewResult, error)
	CurrentWorkspace(context.Context, string, string, string) (ws.Workspace, error)
}

type Config struct {
	Core  authoringOperations
	Store Store

	Capability                         ports.CapabilityResolver
	ConstructionSourceStage            ports.ConstructionSourceStageCompiler
	SelectionMembersCollection         string
	SemanticInventory                  func(context.Context, semantic.PageOptions) (semantic.Page, error)
	ResolveSemanticInventorySelections func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)
	RowChoiceResolver                  ports.RowChoiceResolver
	RowChoicePlanner                   ports.RowChoicePlanner

	CompileReceipt ports.ReceiptCompiler

	ScanCategories       CategoryScanner
	ScanColumnPopulation ColumnPopulationScanner

	ConstructionBaseCacheTTL     time.Duration
	ConstructionBaseCacheEntries int
	Now                          func() time.Time

	SourceLanes *rowsource.Service
}

type Service struct {
	config                Config
	store                 Store
	now                   func() time.Time
	constructionBaseCache *constructionBaseCache
	columnPopulationCache *columnPopulationCache
}

func NewService(config Config) (*Service, error) {
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	config.Now = now
	return &Service{
		config:                config,
		store:                 config.Store,
		now:                   now,
		constructionBaseCache: newConstructionBaseCache(config.ConstructionBaseCacheTTL, config.ConstructionBaseCacheEntries, now),
		columnPopulationCache: newColumnPopulationCache(columnPopulationCacheTTL, columnPopulationCacheMaxEntries, now),
	}, nil
}

func (s *Service) catalog(snapshot capability.Snapshot, explorerID string) ws.CatalogSnapshot {
	if s.config.Capability.Catalog == nil {
		return ws.CatalogSnapshot{}
	}
	return s.config.Capability.Catalog(snapshot, explorerID)
}
