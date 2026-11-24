package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/authoringnetwork"
	"github.com/calypr/loom/internal/explorer/receiptmodel"

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
	"github.com/calypr/loom/internal/explorer/publication"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/rowsource"
	"github.com/calypr/loom/internal/explorer/selection"
	"github.com/calypr/loom/internal/explorer/semanticbrowse"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/projectid"
)

type Service struct {
	core                  *authoringcore.Core
	store                 *explorer.Service
	config                Config
	now                   func() time.Time
	constructionService   *construction.Service
	interpretationService *interpretation.Service
	selectionService      *selection.Service
	explicitGroupService  *explicitgroup.Service
	rowDefService         *rowdef.Service
	rowSourceService      *rowsource.Service
	populationService     *population.Service
	proposalService       *proposal.Service
	publicationService    *publication.Service
	semanticBrowse        *semanticbrowse.Service
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
		core: authoringcore.New(store, authoringcore.Config{
			DefaultDataframeWorkspace:  config.DefaultDataframeWorkspace,
			Capability:                 config.Capability,
			CompileReceipt:             config.CompileReceipt,
			PreviewReceipt:             config.PreviewReceipt,
			ReceiptLookup:              config.ReceiptLookup,
			InterpretationRepository:   config.InterpretationRepository,
			SelectionMembersCollection: config.SelectionMembersCollection,
			NativeReceipts:             config.CompileReceipt != nil || config.PreviewReceipt != nil || config.PopulationMapping != nil || config.MaterializeReceipt != nil,
		}),
		store:                 store,
		config:                config,
		now:                   now,
		interpretationService: interpretationService,
	}
	selectionService, err := selection.NewService(selection.Config{
		Store: store,
		ReadPublishedRevision: func(ctx context.Context, id string) (*selection.PublishedRevision, error) {
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
		},
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
	service.rowSourceService = rowsource.NewService(rowsource.Config{
		Capability:        config.Capability,
		SemanticInventory: config.SemanticInventory,
		RowChoiceResolver: config.RowChoiceResolver,
		RowChoicePlanner:  config.RowChoicePlanner,
	})
	constructionService, err := service.buildConstructionService()
	if err != nil {
		return nil, err
	}
	service.constructionService = constructionService
	service.semanticBrowse = semanticbrowse.NewService(semanticbrowse.Config{
		Core:                               service.core,
		Capability:                         config.Capability,
		SemanticInventory:                  config.SemanticInventory,
		SemanticCoverage:                   config.SemanticCoverage,
		ResolveSemanticInventorySelections: config.ResolveSemanticInventorySelections,
		ProvePopulationRoute:               provePopulationRoute,
		ReauthorizeFrameSourceChoice: func(ctx context.Context, project, explorerID, rowRoot, outputID, choiceID string, authorized ports.AuthorizedCapability, identity capability.ConstructionChoiceIdentity) (semanticbrowse.FrameSourceFamilyCandidate, []capability.ConstructionRouteStep, error) {
			family, route, err := service.constructionService.ReauthorizeFrameSourceChoice(ctx, project, explorerID, rowRoot, outputID, choiceID, authorized, identity)
			return semanticbrowse.FrameSourceFamilyCandidate{Family: family.Family, Entry: family.Entry, Candidate: family.Candidate}, route, err
		},
	})
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
	publicationService, err := publication.NewService(publication.Config{
		Core: service.core, Store: store, ExplorerStore: config.ExplorerStore,
		RevisionWriter: config.RevisionWriter, Capability: config.Capability,
		SelectionMembersCollection: config.SelectionMembersCollection,
		CompileReceipt:             config.CompileReceipt, MaterializeReceipt: config.MaterializeReceipt,
		ArtifactStore: config.ArtifactStore, PublishedReader: config.PublishedReader,
		ArtifactTTL: config.ArtifactTTL, ArtifactMaxRows: config.ArtifactMaxRows, ArtifactMaxBytes: config.ArtifactMaxBytes,
		ValidateReleaseGeneration: config.ValidateReleaseGeneration, ActivateRelease: config.ActivateRelease,
		PrepareRelease: config.PrepareRelease, PersistPublishedWorkspace: config.PersistPublishedWorkspace, Now: now,
	})
	if err != nil {
		return nil, err
	}
	service.publicationService = publicationService
	return service, nil
}

func (s *Service) populationConfig() population.Config {
	return population.Config{
		Core:                         s.core,
		Capability:                   s.config.Capability,
		Store:                        s.store,
		SelectionMembersCollection:   s.config.SelectionMembersCollection,
		CreateSelection:              s.selectionService.CreateSelection,
		ClassifyMaterializationError: support.ClassifyMaterializationError,
		PopulationMapping:            s.config.PopulationMapping,
		PopulationMappingCursorCodec: s.config.PopulationMappingCursorCodec,
	}
}

func (s *Service) constructionConfig() construction.Config {
	return construction.Config{
		Core:                               s.core,
		Store:                              s.store,
		Capability:                         s.config.Capability,
		ConstructionSourceStage:            s.config.ConstructionSourceStage,
		SelectionMembersCollection:         s.config.SelectionMembersCollection,
		SemanticInventory:                  s.config.SemanticInventory,
		ResolveSemanticInventorySelections: s.config.ResolveSemanticInventorySelections,
		RowChoiceResolver:                  s.config.RowChoiceResolver,
		RowChoicePlanner:                   s.config.RowChoicePlanner,
		CompileReceipt:                     s.config.CompileReceipt,
		ScanCategories:                     construction.CategoryScanner(s.config.ScanCategories),
		ScanColumnPopulation:               construction.ColumnPopulationScanner(s.config.ScanColumnPopulation),
		ConstructionBaseCacheTTL:           s.config.ConstructionBaseCacheTTL,
		ConstructionBaseCacheEntries:       s.config.ConstructionBaseCacheEntries,
		Now:                                s.now,
		SourceLanes:                        s.rowSourceService,
	}
}

func (s *Service) buildConstructionService() (*construction.Service, error) {
	return construction.NewService(s.constructionConfig())
}

func (s *Service) buildRowDefService() (*rowdef.Service, error) {
	return rowdef.NewService(rowdef.Config{
		Core:                       s.core,
		Store:                      s.store,
		SelectionMembersCollection: s.config.SelectionMembersCollection,
		Capability:                 s.config.Capability,
		PrepareCandidateWorkspace: func(workspace ws.Workspace, outputID string, replace bool) (ws.Workspace, error) {
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

func (s *Service) DescribeAuthoringNetwork(ctx context.Context, request AuthoringNetworkRequest) (AuthoringNetworkResponse, error) {
	empty := AuthoringNetworkResponse{}
	for name, value := range map[string]string{"project": request.Project, "explorerId": request.ExplorerID, "outputId": request.OutputID, "snapshotToken": request.SnapshotToken, "expectedDraftDigest": request.ExpectedDraftDigest} {
		if err := support.RequireExactIdentity(value, name); err != nil {
			return empty, support.Malformed("authoring-network", err.Error(), err)
		}
	}
	if request.ExpectedDraftVersion < 1 {
		return empty, support.Malformed("authoring-network", "expectedDraftVersion must be positive", nil)
	}
	if s.config.Capability.ForCompilation == nil || s.config.Capability.Catalog == nil || s.config.AuthoringNetwork == nil {
		return empty, support.Unavailable("authoring-network", "CAPABILITY_UNAVAILABLE", "authoring network hydration is unavailable", nil)
	}
	authorized, err := s.config.Capability.ForCompilation(ctx, request.Project, request.SnapshotToken)
	if err != nil || authorized.Snapshot.ValidateToken(request.SnapshotToken) != nil || projectid.Canonical(authorized.Snapshot.Identity.Project) != projectid.Canonical(request.Project) || authorized.Snapshot.Identity.Generation == "" {
		return empty, support.Conflict("authoring-network", "STALE_CATALOG_SNAPSHOT", "reload the source catalog", nil, err)
	}
	if err := support.ValidateAuthorizedReadScope(authorized.Scope, authorized.Snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return empty, support.Conflict("authoring-network", "STALE_AUTHORIZATION_SCOPE", "source authorization changed", nil, err)
	}
	owner, err := s.core.GetOwner(ctx, request.Project, request.ExplorerID)
	if err != nil {
		return empty, err
	}
	if owner.DraftVersion != request.ExpectedDraftVersion || owner.DraftDigest != request.ExpectedDraftDigest {
		return empty, support.Conflict("authoring-network", "DRAFT_CONFLICT", "saved draft changed; reload its network", nil, explorer.ErrDraftConflict)
	}
	workspace, err := ws.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return empty, support.Conflict("authoring-network", "AUTHORING_STATE_MISSING", "saved source cannot be read", nil, err)
	}
	digest, err := workspace.Digest()
	if err != nil || digest != owner.DraftDigest {
		return empty, support.Conflict("authoring-network", "DRAFT_CONFLICT", "saved source digest does not match", nil, err)
	}
	document := support.ProposalDocument(workspace, request.OutputID)
	if document == nil {
		return empty, support.Unprocessable("authoring-network", "OUTPUT_NOT_FOUND", "saved output is unavailable", nil)
	}
	network, err := s.config.AuthoringNetwork(ctx, request.Project, authorized, *document)
	if err != nil {
		var typed *ports.Error
		if errors.As(err, &typed) {
			return empty, typed
		}
		return empty, support.Unprocessable("authoring-network", "INVALID_AUTHORING_NETWORK", err.Error(), err)
	}
	if network.ProjectedFields == nil {
		network.ProjectedFields = authoringnetwork.ProjectedFieldsForDocument(*document, network)
	}
	rowIdentity := support.RowIdentityForNetwork(*document, network)
	return AuthoringNetworkResponse{RowIdentity: rowIdentity, SnapshotToken: request.SnapshotToken, DraftVersion: owner.DraftVersion, DraftDigest: owner.DraftDigest, Network: network}, nil
}

func (s *Service) CreateExplicitGroupRevision(ctx context.Context, request explicitgroup.ExplicitGroupCreateRequest) (explicitgroup.ExplicitGroupCreateResult, error) {
	return s.explicitGroupService.CreateExplicitGroupRevision(ctx, request)
}

func NewSchemaRowChoiceResolver(index *fhirschema.Index) (*rowdef.SchemaRowChoiceResolver, error) {
	return rowdef.NewSchemaRowChoiceResolver(index)
}

func (s *Service) AssessRowChange(ctx context.Context, request rowdef.AssessRowChangeRequest) (rowdef.AssessRowChangeResult, error) {
	return s.rowDefService.AssessRowChange(ctx, request)
}

func (s *Service) ListRowDefinitionChoices(ctx context.Context, request rowdef.RowDefinitionChoicesRequest) (rowdef.RowDefinitionChoicesResponse, error) {
	return s.rowDefService.ListRowDefinitionChoices(ctx, request)
}

func (s *Service) RowLineage(ctx context.Context, request rowdef.RowLineageRequest) (rowdef.RowLineageResult, error) {
	return s.rowDefService.RowLineage(ctx, request)
}

func (s *Service) ProposeRowDefinition(ctx context.Context, request rowdef.RowDefinitionProposalRequest) (rowdef.RowDefinitionProposal, error) {
	return s.rowDefService.ProposeRowDefinition(ctx, request)
}

func (s *Service) validateRowValuePolicyCandidate(ctx context.Context, project, explorerID string, request authoringv2.ApplyCommandsRequest, workspace ws.Workspace, snapshot capability.Snapshot, compilationAuth ports.AuthorizedCapability, command authoringv2.Command) error {
	return s.rowDefService.ValidateRowValuePolicyCandidate(ctx, project, explorerID, request, workspace, snapshot, compilationAuth, command)
}

func rowChangeReceiptWorkspace(workspace ws.Workspace, outputID string) (ws.Workspace, error) {
	return rowdef.RowChangeReceiptWorkspace(workspace, outputID)
}

func checkRowDefinitionProposalResult(workspace ws.Workspace, receipt *receiptmodel.CompilationReceipt) error {
	return rowdef.CheckRowDefinitionProposalResult(workspace, receipt)
}
