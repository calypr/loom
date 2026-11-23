package construction

import (
	"context"
	"time"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/proposal"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/semantic"
)

type CategoryScanner func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error)

type ColumnPopulationScanner func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, dataframeexecution.ColumnPopulationRequest) (dataframeexecution.ColumnPopulationResult, error)

type AcceptedConstruction struct {
	Preview      proposal.PreviewResult
	PreviewLimit int
}

type PreviewSinkFactory = proposal.PreviewSinkFactory

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

type CompileRequest struct {
	Project               string
	ExplorerID            string
	Workspace             authoringv2.Workspace
	SnapshotToken         string
	RequestID             string
	ConstructionProposal  *explorer.ConstructionProposalBinding
	RowDefinitionProposal *explorer.RowDefinitionProposalBinding
}

type PreviewRequest struct {
	Project    string
	ExplorerID string
	ReceiptID  string
	OutputID   string
	Limit      int

	AllowSampledAggregatePreview bool

	SinkFactory proposal.PreviewSinkFactory
}

type Config struct {
	Store *explorer.Service

	Capability                         ports.CapabilityResolver
	ConstructionSourceStage            ports.ConstructionSourceStageCompiler
	SelectionMembersCollection         string
	SemanticInventory                  func(context.Context, semantic.PageOptions) (semantic.Page, error)
	ResolveSemanticInventorySelections func(context.Context, semantic.ResolveOptions) (semantic.ResolveResult, error)
	RowChoiceResolver                  rowdef.RowChoiceResolver
	RowChoicePlanner                   rowdef.RowChoicePlanner

	CompileReceipt ports.ReceiptCompiler

	Compile               func(context.Context, CompileRequest) (*explorer.CompilationReceipt, error)
	VerifyProposalReceipt func(context.Context, string, *explorer.CompilationReceipt, string, string, string, capability.Snapshot, *authoringv2.Workspace) (authoringv2.Workspace, error)
	GetOwner              func(context.Context, string, string) (*explorer.Explorer, error)
	LookupReceipt         func(context.Context, string, string, string) (*explorer.CompilationReceipt, error)
	Preview               func(context.Context, PreviewRequest) (proposal.PreviewResult, error)

	ScanCategories       CategoryScanner
	ScanColumnPopulation ColumnPopulationScanner

	ConstructionBaseCacheTTL     time.Duration
	ConstructionBaseCacheEntries int
	Now                          func() time.Time

	SourceLanes SourceLanes

	ReauthorizeFrameSourceChoice func(context.Context, Base, string, string, string, string, string, ports.AuthorizedCapability, capability.ConstructionChoiceIdentity) (FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error)
}

type SourceLanes interface {
	ConstructionFilterSourceCapability(context.Context, Base, string, string) (ConstructionFilterSourceCapability, error)
	ResolveConstructionFilterSource(context.Context, Base, ConstructionProposalRequest, string, ConstructionFilterSourceSelection) (authoringv2.ConstructionSourceProjection, error)
	ConstructionCandidateWithFilterSources(context.Context, Base, ConstructionProposalRequest, authoringv2.Construction) (authoringv2.Construction, error)
	ReauthorizeConstructionFilterSources(context.Context, Base, string, authoringv2.Construction) error
	ConstructionHasFilterSourceInput(authoringv2.Construction) bool

	ConstructionDeriveSourceCapability(context.Context, Base, string, string) (ConstructionDeriveSourceCapability, error)
	ResolveConstructionDeriveSource(context.Context, Base, ConstructionProposalRequest, string, ConstructionDeriveSourceSelection) (authoringv2.ConstructionSourceProjection, error)
	ConstructionCandidateWithDeriveSources(context.Context, Base, ConstructionProposalRequest, authoringv2.Construction) (authoringv2.Construction, error)
	ReauthorizeConstructionDeriveSources(context.Context, Base, string, authoringv2.Construction) error
	ConstructionHasDeriveSourceInput(authoringv2.Construction) bool

	ConstructionExpandSourceCapability(context.Context, Base, string, string) (ConstructionExpandSourceCapability, error)
	ResolveConstructionExpandSource(context.Context, Base, ConstructionProposalRequest, string, ConstructionExpandSourceSelection) (authoringv2.ConstructionSourceProjection, error)
	ConstructionCandidateWithExpandSources(context.Context, Base, ConstructionProposalRequest, authoringv2.Construction) (authoringv2.Construction, error)
	ReauthorizeConstructionExpandSources(context.Context, Base, string, authoringv2.Construction) error
	ConstructionHasExpandSourceInput(authoringv2.Construction) bool

	ConstructionUnpivotSourceCapability(context.Context, Base, string, string) (ConstructionUnpivotSourceCapability, error)
	ResolveConstructionUnpivotSource(context.Context, Base, ConstructionProposalRequest, string, ConstructionUnpivotSourceSelection) (authoringv2.ConstructionSourceProjection, error)
	ConstructionCandidateWithUnpivotSources(context.Context, Base, ConstructionProposalRequest, authoringv2.Construction) (authoringv2.Construction, error)
	ReauthorizeConstructionUnpivotSources(context.Context, Base, string, authoringv2.Construction) error
	ConstructionHasUnpivotSourceInput(authoringv2.Construction) bool
}

type Service struct {
	config                Config
	store                 *explorer.Service
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

func (s *Service) SetConfig(config Config) {
	s.config = config
	s.store = config.Store
	if config.Now != nil {
		s.now = config.Now
	}
}

func (s *Service) catalog(snapshot capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
	if s.config.Capability.Catalog == nil {
		return authoringv2.CatalogSnapshot{}
	}
	return s.config.Capability.Catalog(snapshot, explorerID)
}

func (s *Service) getOwner(ctx context.Context, project, explorerID string) (*explorer.Explorer, error) {
	return s.config.GetOwner(ctx, project, explorerID)
}

func (s *Service) lookupReceipt(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
	return s.config.LookupReceipt(ctx, project, explorerID, receiptID)
}

func (s *Service) verifyProposalReceipt(ctx context.Context, stage string, receipt *explorer.CompilationReceipt, project, explorerID, snapshotToken string, snapshot capability.Snapshot, expected *authoringv2.Workspace) (authoringv2.Workspace, error) {
	return s.config.VerifyProposalReceipt(ctx, stage, receipt, project, explorerID, snapshotToken, snapshot, expected)
}

func (s *Service) compile(ctx context.Context, request CompileRequest) (*explorer.CompilationReceipt, error) {
	return s.config.Compile(ctx, request)
}

func (s *Service) Preview(ctx context.Context, request PreviewRequest) (proposal.PreviewResult, error) {
	return s.config.Preview(ctx, request)
}
