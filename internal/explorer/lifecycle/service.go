package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/explicitgroup"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/lifecycle/support"
	"github.com/calypr/loom/internal/explorer/population"
	"github.com/calypr/loom/internal/explorer/proposal"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/selection"
)

type Service struct {
	store                 *explorer.Service
	config                Config
	now                   func() time.Time
	constructionService   *construction.Service
	interpretationService *interpretation.Service
	selectionService      *selection.Service
	explicitGroupService  *explicitgroup.Service
	rowDefService         *rowdef.Service
	populationService     *population.Service
	proposalService       *proposal.Service
}

func New(store *explorer.Service, config Config) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("Explorer lifecycle store is required")
	}
	now := config.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	config.Now = now
	interpretationService, err := interpretation.NewService(interpretation.Config{
		Repository: config.InterpretationRepository,
		Errors: interpretation.ErrorFactory{
			Malformed:          support.Malformed,
			Unavailable:        support.Unavailable,
			Conflict:           support.Conflict,
			NotFound:           support.NotFound,
			Unprocessable:      support.Unprocessable,
			Internal:           support.Internal,
			PersistenceFailure: support.PersistenceFailure,
		},
		IsNotFound: func(err error) bool { return errors.Is(err, explorer.ErrNotFound) },
	})
	if err != nil {
		return nil, err
	}
	service := &Service{
		store:                 store,
		config:                config,
		now:                   now,
		interpretationService: interpretationService,
	}
	selectionService, err := selection.NewService(selection.Config{
		Store: store,
		RevisionReader: selection.RevisionReaderFunc(func(ctx context.Context, id string) (*selection.PublishedRevision, error) {
			revision, err := store.GetRevision(ctx, id)
			if err != nil {
				return nil, err
			}
			outputs := make([]string, len(revision.Dataset.Outputs))
			for i, output := range revision.Dataset.Outputs {
				outputs[i] = output.Name
			}
			return &selection.PublishedRevision{
				Project: revision.Project, ExplorerID: revision.ExplorerID,
				CompilationReceiptID: revision.CompilationReceiptID,
				ExecutionID:          revision.Publication.ExecutionID, Outputs: outputs,
			}, nil
		}),
		CapabilityProvider: func() selection.CapabilityResolver {
			return selectionCapabilityResolver(service.config.Capability)
		},
		SelectionSourceResolver: config.SelectionSourceResolver,
		SelectionReferenceValidatorProvider: func() selection.SelectionReferenceValidator {
			return service.config.SelectionReferenceValidator
		},
		Errors: selection.ErrorFactory{
			Malformed: support.Malformed, Unavailable: support.Unavailable, Conflict: support.Conflict,
			Forbidden: support.Forbidden, NotFound: support.NotFound, Unprocessable: support.Unprocessable,
			PersistenceFailure: support.PersistenceFailure,
		},
		IsNotFound: func(err error) bool { return errors.Is(err, explorer.ErrNotFound) },
	})
	if err != nil {
		return nil, err
	}
	service.selectionService = selectionService
	explicitGroupService, err := explicitgroup.NewService(explicitgroup.Config{
		Store: store, Repository: config.ExplicitGroupRepository, Capability: config.Capability, Now: now,
	})
	if err != nil {
		return nil, err
	}
	service.explicitGroupService = explicitGroupService
	rowDefService, err := service.buildRowDefService()
	if err != nil {
		return nil, err
	}
	service.rowDefService = rowDefService
	constructionService, err := service.buildConstructionService()
	if err != nil {
		return nil, err
	}
	service.constructionService = constructionService
	populationService, err := population.NewService(service.populationConfig())
	if err != nil {
		return nil, err
	}
	service.populationService = populationService
	proposalService, err := proposal.NewService(service.proposalConfig())
	if err != nil {
		return nil, err
	}
	service.proposalService = proposalService
	return service, nil
}

func (s *Service) populationConfig() population.Config {
	return population.Config{
		Capability:                     func() ports.CapabilityResolver { return s.config.Capability },
		Store:                          s.store,
		SelectionMembersCollection:     s.config.SelectionMembersCollection,
		LookupReceipt:                  s.lookupReceipt,
		ValidateExecutableReceiptRoute: s.validateExecutableReceiptRoute,
		Compile: func(ctx context.Context, request population.CompileRequest) (*explorer.CompilationReceipt, error) {
			return s.compile(ctx, compileRequest{
				Project: request.Project, ExplorerID: request.ExplorerID, Workspace: request.Workspace,
				SnapshotToken: request.SnapshotToken, RequestID: request.RequestID,
				PopulationMemberRemovalProposal: request.PopulationMemberRemovalProposal,
			})
		},
		VerifyProposalReceipt: s.verifyProposalReceipt,
		GetOwner:              s.getOwner,
		CurrentWorkspace:      s.currentWorkspace,
		CreateSelection:       s.CreateSelection,
		Catalog:               s.catalog,
		PreviewMemberRemoval: func(ctx context.Context, receipt *explorer.CompilationReceipt, project, explorerID string) error {
			binding := receipt.PopulationMemberRemovalProposal
			_, err := s.Preview(ctx, PreviewRequest{
				Project: project, ExplorerID: explorerID, ReceiptID: receipt.ID, OutputID: binding.OutputID,
				Limit: binding.PreviewLimit,
				SinkFactory: func(_ *explorer.CompilationReceipt, _ []explorer.EmittedColumn) (func(map[string]any) error, error) {
					return func(map[string]any) error { return nil }, nil
				},
			})
			return err
		},
		DocumentDigest:               documentDigest,
		ConstructionDocumentIndex:    constructionDocumentIndex,
		ConstructionCompilerScope:    constructionCompilerScope,
		ClassifyMaterializationError: classifyMaterializationError,
		PopulationMapping:            s.config.PopulationMapping,
		PopulationMappingCursorCodec: s.config.PopulationMappingCursorCodec,
	}
}

func (s *Service) constructionConfig() construction.Config {
	return construction.Config{
		Store:                              s.store,
		Capability:                         s.config.Capability,
		ConstructionSourceStage:            s.config.ConstructionSourceStage,
		SelectionMembersCollection:         s.config.SelectionMembersCollection,
		SemanticInventory:                  s.config.SemanticInventory,
		ResolveSemanticInventorySelections: s.config.ResolveSemanticInventorySelections,
		RowChoiceResolver:                  s.config.RowChoiceResolver,
		RowChoicePlanner:                   s.config.RowChoicePlanner,
		CompileReceipt:                     s.config.CompileReceipt,
		Compile: func(ctx context.Context, request construction.CompileRequest) (*explorer.CompilationReceipt, error) {
			return s.compile(ctx, compileRequest{
				Project: request.Project, ExplorerID: request.ExplorerID, Workspace: request.Workspace,
				SnapshotToken: request.SnapshotToken, RequestID: request.RequestID,
				ConstructionProposal: request.ConstructionProposal, RowDefinitionProposal: request.RowDefinitionProposal,
			})
		},
		VerifyProposalReceipt: s.verifyProposalReceipt,
		GetOwner:              s.getOwner,
		LookupReceipt:         s.lookupReceipt,
		Preview: func(ctx context.Context, request construction.PreviewRequest) (proposal.PreviewResult, error) {
			return s.Preview(ctx, PreviewRequest{
				Project: request.Project, ExplorerID: request.ExplorerID, ReceiptID: request.ReceiptID,
				OutputID: request.OutputID, Limit: request.Limit,
				AllowSampledAggregatePreview: request.AllowSampledAggregatePreview,
				SinkFactory:                  request.SinkFactory,
			})
		},
		ScanCategories:               construction.CategoryScanner(s.config.ScanCategories),
		ScanColumnPopulation:         construction.ColumnPopulationScanner(s.config.ScanColumnPopulation),
		ConstructionBaseCacheTTL:     s.config.ConstructionBaseCacheTTL,
		ConstructionBaseCacheEntries: s.config.ConstructionBaseCacheEntries,
		Now:                          s.now,
		SourceLanes:                  constructionSourceLanes{service: s},
		ReauthorizeFrameSourceChoice: func(ctx context.Context, base construction.Base, project, explorerID, rowRoot, outputID, choiceID string, authorized ports.AuthorizedCapability, identity capability.ConstructionChoiceIdentity) (construction.FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
			family, route, err := s.reauthorizeFrameSourceChoice(ctx, project, explorerID, rowRoot, outputID, choiceID, authorized, identity)
			return construction.FrameSourceFamilyCandidate{Family: family.Family, Entry: family.Entry, Candidate: family.Candidate}, route, err
		},
	}
}

func (s *Service) buildConstructionService() (*construction.Service, error) {
	return construction.NewService(s.constructionConfig())
}

func (s *Service) syncConstructionService() *construction.Service {
	if s.constructionService == nil {
		service, err := s.buildConstructionService()
		if err != nil {
			return nil
		}
		s.constructionService = service
	} else {
		s.constructionService.SetConfig(s.constructionConfig())
	}
	return s.constructionService
}

func (s *Service) buildRowDefService() (*rowdef.Service, error) {
	return rowdef.NewService(rowdef.Config{
		Store:                      s.store,
		SelectionMembersCollection: s.config.SelectionMembersCollection,
		Capability:                 s.config.Capability,
		GetOwner:                   s.getOwner,
		Compile: func(ctx context.Context, request rowdef.CompileRequest) (*explorer.CompilationReceipt, error) {
			return s.compile(ctx, compileRequest{
				Project: request.Project, ExplorerID: request.ExplorerID, Workspace: request.Workspace,
				SnapshotToken: request.SnapshotToken, RequestID: request.RequestID, RowDefinitionProposal: request.RowDefinitionProposal,
			})
		},
		LookupReceipt:                  s.lookupReceipt,
		ValidateExecutableReceiptRoute: s.validateExecutableReceiptRoute,
		VerifyProposalReceipt:          s.verifyProposalReceipt,
		PrepareCandidateWorkspace: func(workspace authoringv2.Workspace, outputID string, replace bool) (authoringv2.Workspace, error) {
			if replace {
				return rowdef.RowChangeReceiptWorkspace(workspace, outputID)
			}
			return constructionCapabilitiesWorkspace(workspace, outputID)
		},
		PreviewReceipt:        s.config.PreviewReceipt,
		ValidateReceiptStream: s.config.ValidateReceiptStream,
		RowLineage:            s.config.RowLineage,
		RowChoiceResolver:     s.config.RowChoiceResolver,
		RowChoicePlanner:      s.config.RowChoicePlanner,
		ExplicitGroupResolver: s.config.ExplicitGroupResolver,
	})
}

func (s *Service) syncRowDefService() *rowdef.Service {
	service, err := s.buildRowDefService()
	if err != nil {
		return s.rowDefService
	}
	s.rowDefService = service
	return s.rowDefService
}

func selectionCapabilityResolver(capabilities ports.CapabilityResolver) selection.CapabilityResolver {
	return selection.CapabilityResolver{
		Current: capabilities.Current,
		ForCompilation: func(ctx context.Context, project, token string) (selection.AuthorizedCapability, error) {
			value, err := capabilities.ForCompilation(ctx, project, token)
			return selection.AuthorizedCapability{Snapshot: value.Snapshot, Scope: value.Scope}, err
		},
		ForExecution: func(ctx context.Context, project, token string) (selection.AuthorizedCapability, error) {
			value, err := capabilities.ForExecution(ctx, project, token)
			return selection.AuthorizedCapability{Snapshot: value.Snapshot, Scope: value.Scope}, err
		},
	}
}

func (s *Service) InterpretationService() *interpretation.Service {
	if s == nil {
		return nil
	}
	return s.interpretationService
}

func (s *Service) SelectionService() *selection.Service {
	if s == nil {
		return nil
	}
	return s.selectionService
}
