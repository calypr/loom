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
	"github.com/calypr/loom/internal/explorer/rowdef"
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

type RowChoiceResolver = rowdef.RowChoiceResolver
type RowChoicePlanner = rowdef.RowChoicePlanner
type RowChoiceResolveRequest = rowdef.RowChoiceResolveRequest

const (
	RowChoiceFieldGroup = rowdef.RowChoiceFieldGroup
	RowChoiceExpanded   = rowdef.RowChoiceExpanded
)

type FrameSourceFamilyCandidate struct {
	Family    capability.SemanticFrameFamily
	Entry     semantic.Entry
	Candidate capability.Candidate
}

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
	RowChoiceResolver                  rowdef.RowChoiceResolver
	RowChoicePlanner                   rowdef.RowChoicePlanner

	CompileReceipt ports.ReceiptCompiler

	ScanCategories       CategoryScanner
	ScanColumnPopulation ColumnPopulationScanner

	ConstructionBaseCacheTTL     time.Duration
	ConstructionBaseCacheEntries int
	Now                          func() time.Time

	SourceLanes SourceLanes
}

type SourceLanes interface {
	ConstructionFilterSourceCapability(context.Context, Base, string, string) (ConstructionFilterSourceCapability, error)
	ResolveConstructionFilterSource(context.Context, Base, rowsource.ProposalRequest, string, ConstructionFilterSourceSelection) (ws.ConstructionSourceProjection, error)
	ConstructionCandidateWithFilterSources(context.Context, Base, rowsource.ProposalRequest, ws.Construction) (ws.Construction, error)
	ReauthorizeConstructionFilterSources(context.Context, Base, string, ws.Construction) error
	ConstructionHasFilterSourceInput(ws.Construction) bool

	ConstructionDeriveSourceCapability(context.Context, Base, string, string) (ConstructionDeriveSourceCapability, error)
	ResolveConstructionDeriveSource(context.Context, Base, rowsource.ProposalRequest, string, ConstructionDeriveSourceSelection) (ws.ConstructionSourceProjection, error)
	ConstructionCandidateWithDeriveSources(context.Context, Base, rowsource.ProposalRequest, ws.Construction) (ws.Construction, error)
	ReauthorizeConstructionDeriveSources(context.Context, Base, string, ws.Construction) error
	ConstructionHasDeriveSourceInput(ws.Construction) bool

	ConstructionExpandSourceCapability(context.Context, Base, string, string) (ConstructionExpandSourceCapability, error)
	ResolveConstructionExpandSource(context.Context, Base, rowsource.ProposalRequest, string, ConstructionExpandSourceSelection) (ws.ConstructionSourceProjection, error)
	ConstructionCandidateWithExpandSources(context.Context, Base, rowsource.ProposalRequest, ws.Construction) (ws.Construction, error)
	ReauthorizeConstructionExpandSources(context.Context, Base, string, ws.Construction) error
	ConstructionHasExpandSourceInput(ws.Construction) bool

	ConstructionUnpivotSourceCapability(context.Context, Base, string, string) (ConstructionUnpivotSourceCapability, error)
	ResolveConstructionUnpivotSource(context.Context, Base, rowsource.ProposalRequest, string, ConstructionUnpivotSourceSelection) (ws.ConstructionSourceProjection, error)
	ConstructionCandidateWithUnpivotSources(context.Context, Base, rowsource.ProposalRequest, ws.Construction) (ws.Construction, error)
	ReauthorizeConstructionUnpivotSources(context.Context, Base, string, ws.Construction) error
	ConstructionHasUnpivotSourceInput(ws.Construction) bool
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

func (s *Service) getOwner(ctx context.Context, project, explorerID string) (*explorer.Explorer, error) {
	return s.config.Core.GetOwner(ctx, project, explorerID)
}

func (s *Service) lookupReceipt(ctx context.Context, project, explorerID, receiptID string) (*receiptmodel.CompilationReceipt, error) {
	return s.config.Core.LookupReceipt(ctx, project, explorerID, receiptID)
}

func (s *Service) verifyProposalReceipt(ctx context.Context, stage string, receipt *receiptmodel.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *ws.Workspace) (ws.Workspace, error) {
	return s.config.Core.VerifyProposalReceipt(ctx, stage, receipt, project, explorerID, snapshotToken, snapshot, expected)
}

func (s *Service) compile(ctx context.Context, request authoringcore.CompileRequest) (*receiptmodel.CompilationReceipt, error) {
	return s.config.Core.Compile(ctx, request)
}

func (s *Service) Preview(ctx context.Context, request authoringcore.PreviewRequest) (authoringcore.PreviewResult, error) {
	return s.config.Core.Preview(ctx, request)
}
