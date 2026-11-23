package construction

import (
	"context"
	"errors"
	"fmt"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/projectid"
)

type Base struct {
	Owner           *explorer.Explorer
	ExplorerID      string
	Workspace       authoringv2.Workspace
	Document        authoringv2.Document
	Construction    authoringv2.Construction
	Snapshot        capability.Snapshot
	Authorized      ports.AuthorizedCapability
	Catalog         authoringv2.CatalogSnapshot
	Receipt         *explorer.CompilationReceipt
	Stages          []explorer.ReceiptConstructionStage
	BaseDocumentSHA string
}

type constructionBaseLoader struct {
	cache                      *constructionBaseCache
	capability                 ports.CapabilityResolver
	sourceStage                ports.ConstructionSourceStageCompiler
	selectionMembersCollection string
	canCompile                 bool
	compile                    func(context.Context, CompileRequest) (*explorer.CompilationReceipt, error)
	verifyProposalReceipt      func(context.Context, string, *explorer.CompilationReceipt, string, string, string, capability.Snapshot, *authoringv2.Workspace) (authoringv2.Workspace, error)
	getOwner                   func(context.Context, string, string) (*explorer.Explorer, error)
}

func (s *Service) loadConstructionBase(ctx context.Context, project, explorerID, snapshotToken string, draftVersion int64, draftDigest, outputID string) (Base, error) {
	loader := constructionBaseLoader{
		cache: s.constructionBaseCache, capability: s.config.Capability,
		sourceStage: s.config.ConstructionSourceStage, selectionMembersCollection: s.config.SelectionMembersCollection,
		canCompile: s.config.CompileReceipt != nil, compile: s.compile,
		verifyProposalReceipt: s.verifyProposalReceipt, getOwner: s.getOwner,
	}
	return loader.load(ctx, project, explorerID, snapshotToken, draftVersion, draftDigest, outputID)
}

func (s constructionBaseLoader) load(ctx context.Context, project, explorerID, snapshotToken string, draftVersion int64, draftDigest, outputID string) (Base, error) {
	validation, err := s.validateConstructionBase(ctx, project, explorerID, snapshotToken, draftVersion, draftDigest, outputID)
	if err != nil {
		return Base{}, err
	}
	key := constructionBaseCacheKey{
		Format:                   constructionBaseCacheFormatVersion,
		Project:                  projectid.Canonical(validation.project),
		ExplorerID:               validation.explorerID,
		DraftVersion:             validation.owner.DraftVersion,
		DraftDigest:              validation.owner.DraftDigest,
		SnapshotToken:            validation.snapshot.Token,
		Generation:               validation.snapshot.Identity.Generation,
		SchemaDigest:             validation.snapshot.Identity.SchemaDigest,
		ShapeDigest:              validation.snapshot.Identity.ShapeDigest,
		ResourceInventoryDigest:  validation.snapshot.Identity.ResourceInventoryDigest,
		RelationshipDigest:       validation.snapshot.Identity.RelationshipDigest,
		FieldDigest:              validation.snapshot.Identity.FieldDigest,
		ProtocolVersion:          validation.snapshot.Identity.ProtocolVersion,
		CompilerVersion:          validation.snapshot.Identity.CompilerVersion,
		TraversalPolicyVersion:   validation.snapshot.Identity.TraversalPolicyVersion,
		ProjectionPolicyVersion:  validation.snapshot.Identity.ProjectionPolicyVersion,
		CacheVersion:             validation.snapshot.Identity.CacheVersion,
		OutputID:                 validation.outputID,
		AuthorizationScopeDigest: validation.snapshot.Identity.AuthorizationScopeDigest,
	}
	if s.cache != nil {
		if cached, ok := s.cache.Get(key); ok {
			return validation.base(cached.receipt, cached.stages), nil
		}
	}
	compiled, err := s.compileConstructionBase(ctx, validation)
	if err != nil {
		return Base{}, err
	}
	if s.cache != nil {
		s.cache.Put(key, constructionBaseCacheValue{receipt: compiled.receipt, stages: compiled.stages})
	}
	return validation.base(compiled.receipt, compiled.stages), nil
}

type constructionBaseValidation struct {
	project         string
	explorerID      string
	snapshotToken   string
	outputID        string
	authorized      ports.AuthorizedCapability
	snapshot        capability.Snapshot
	catalog         authoringv2.CatalogSnapshot
	owner           *explorer.Explorer
	workspace       authoringv2.Workspace
	document        authoringv2.Document
	upgraded        authoringv2.Document
	baseDocumentSHA string
}

type constructionBaseCompilation struct {
	receipt *explorer.CompilationReceipt
	stages  []explorer.ReceiptConstructionStage
}

func (v constructionBaseValidation) base(receipt *explorer.CompilationReceipt, stages []explorer.ReceiptConstructionStage) Base {
	return Base{
		Owner: v.owner, ExplorerID: v.explorerID, Workspace: v.workspace, Document: v.document,
		Construction: *v.upgraded.Construction, Snapshot: v.snapshot, Authorized: v.authorized.Clone(), Catalog: v.catalog,
		Receipt: receipt, Stages: stages, BaseDocumentSHA: v.baseDocumentSHA,
	}
}

func (s constructionBaseLoader) validateConstructionBase(ctx context.Context, project, explorerID, snapshotToken string, draftVersion int64, draftDigest, outputID string) (constructionBaseValidation, error) {
	if s.capability.ForCompilation == nil {
		return constructionBaseValidation{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "authorized construction compilation is not configured", nil)
	}
	if s.capability.Catalog == nil {
		return constructionBaseValidation{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "construction authoring catalog is not configured", nil)
	}
	authorized, err := s.capability.ForCompilation(ctx, project, snapshotToken)
	if err != nil {
		return constructionBaseValidation{}, conflict("construction-capabilities", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, err)
	}
	snapshot := authorized.Snapshot.Clone()
	if snapshot.ValidateToken(snapshotToken) != nil || projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(project) {
		return constructionBaseValidation{}, conflict("construction-capabilities", "STALE_CATALOG_SNAPSHOT", "the catalog snapshot is stale or unavailable", nil, nil)
	}
	if err := validateAuthorizedReadScope(authorized.Scope, snapshot.Identity.AuthorizationScopeDigest); err != nil {
		return constructionBaseValidation{}, conflict("construction-capabilities", "STALE_AUTHORIZATION_SCOPE", "the authorized catalog scope changed", nil, err)
	}
	authorized.Snapshot = snapshot.Clone()
	catalogSnapshot := s.capability.Catalog(snapshot, explorerID)
	owner, err := s.getOwner(ctx, project, explorerID)
	if err != nil {
		return constructionBaseValidation{}, err
	}
	if owner == nil || owner.ExplorerID != explorerID || projectid.Canonical(owner.Project) != projectid.Canonical(project) {
		return constructionBaseValidation{}, notFound("construction-capabilities", "EXPLORER_NOT_FOUND", "the Explorer was not found", explorer.ErrNotFound)
	}
	if owner.DraftVersion != draftVersion || owner.DraftDigest != draftDigest {
		return constructionBaseValidation{}, conflict("construction-capabilities", "DRAFT_CONFLICT", "the Explorer draft changed; reload before requesting construction capabilities", nil, explorer.ErrDraftConflict)
	}
	workspace, err := authoringv2.DecodeWorkspace(owner.DraftConfig)
	if err != nil {
		return constructionBaseValidation{}, conflict("construction-capabilities", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be compiled", nil, err)
	}
	actualDigest, err := workspace.Digest()
	if err != nil || actualDigest != owner.DraftDigest {
		return constructionBaseValidation{}, conflict("construction-capabilities", "DRAFT_CONFLICT", "the saved workspace does not match its draft digest", nil, err)
	}
	documentIndex := constructionDocumentIndex(workspace, outputID)
	if documentIndex < 0 {
		return constructionBaseValidation{}, unprocessable("construction-capabilities", "OUTPUT_NOT_FOUND", "outputId does not identify a saved table", nil)
	}
	document := workspace.Documents[documentIndex]
	baseDocumentSHA, err := documentDigest(document)
	if err != nil {
		return constructionBaseValidation{}, fmt.Errorf("digest construction base document: %w", err)
	}
	upgraded, err := authoringv2.StartDocumentConstruction(document)
	if err != nil {
		return constructionBaseValidation{}, unprocessable("construction-capabilities", "INVALID_CONSTRUCTION_BASE", err.Error(), err)
	}
	if upgraded.Construction == nil {
		return constructionBaseValidation{}, conflict("construction-capabilities", "INVALID_CONSTRUCTION_BASE", "the source projection has no construction contract", nil, nil)
	}
	if stepID := constructionUnresolvedPivotDiscovery(upgraded.Construction); stepID != "" {
		return constructionBaseValidation{}, unprocessable("construction-capabilities", "PIVOT_DISCOVERY_UNRESOLVED",
			fmt.Sprintf("Pivot %s declares category discovery but has no resolved categories. Propose the Pivot against the current rows to freeze its observed categories before previewing or loading this output.", stepID), nil)
	}
	workspace.Documents[documentIndex] = upgraded
	workspace = workspace.NormalizePresentationOrders()
	if err := (authoringv2.BuilderState{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.StateKind,
		Workspace: &workspace, Catalog: catalogSnapshot,
	}).Validate(); err != nil {
		return constructionBaseValidation{}, unprocessable("intent", workspaceValidationCode(err), err.Error(), err)
	}
	return constructionBaseValidation{
		project: project, explorerID: explorerID, snapshotToken: snapshotToken, outputID: outputID,
		authorized: authorized, snapshot: snapshot, catalog: catalogSnapshot, owner: owner,
		workspace: workspace, document: document, upgraded: upgraded, baseDocumentSHA: baseDocumentSHA,
	}, nil
}

func (s constructionBaseLoader) compileConstructionBase(ctx context.Context, validation constructionBaseValidation) (constructionBaseCompilation, error) {
	project, explorerID, snapshotToken, outputID := validation.project, validation.explorerID, validation.snapshotToken, validation.outputID
	authorized, snapshot := validation.authorized, validation.snapshot
	workspace, document, upgraded := validation.workspace, validation.document, validation.upgraded
	if len(upgraded.Columns) == 0 && len(upgraded.Construction.Steps) == 0 {
		if s.sourceStage == nil {
			return constructionBaseCompilation{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "compiler-resolved source-stage discovery is not configured", nil)
		}
		stage, err := s.sourceStage(ctx, ports.ConstructionSourceStageRequest{
			Project: project, ExplorerID: explorerID, OutputID: outputID, Document: document,
			Authorized: authorized.Clone(), SelectionMembersCollection: s.selectionMembersCollection,
		})
		if err != nil {
			var typed *ports.Error
			if errors.As(err, &typed) {
				return constructionBaseCompilation{}, typed
			}
			return constructionBaseCompilation{}, unprocessable("construction-capabilities", "SOURCE_STAGE_DISCOVERY_FAILED", "the compiler could not resolve the empty source projection", err)
		}
		if stage.ID != recipe.ConstructionSourceProjectionID || stage.RowIdentityColumn == "" || len(stage.Columns) != 0 {
			return constructionBaseCompilation{}, conflict("construction-capabilities", "INVALID_SOURCE_STAGE_DESCRIPTOR", "the compiler returned an invalid zero-column source-stage descriptor", nil, nil)
		}
		stages := []explorer.ReceiptConstructionStage{stage}
		if len(workspace.Documents) > 1 {
			if !s.canCompile {
				return constructionBaseCompilation{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "authorized construction compilation is not configured", nil)
			}
			compileWorkspace, err := constructionCapabilitiesWorkspace(workspace, outputID)
			if err != nil {
				return constructionBaseCompilation{}, conflict("construction-capabilities", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be projected for selected-output capabilities", nil, err)
			}
			receipt, err := s.compile(ctx, CompileRequest{
				Project: project, ExplorerID: explorerID, Workspace: compileWorkspace, SnapshotToken: snapshotToken,
				RequestID: "construction-capabilities",
			})
			if err != nil {
				return constructionBaseCompilation{}, err
			}
			if _, err := s.verifyProposalReceipt(ctx, "construction-capabilities", receipt, project, explorerID, snapshotToken, snapshot, &compileWorkspace); err != nil {
				return constructionBaseCompilation{}, err
			}
			if err := validateReceiptOutputContract(receipt, outputID); err != nil {
				return constructionBaseCompilation{}, unprocessable("construction-capabilities", "OUTPUT_NOT_FOUND", "outputId is not in the compiler output contract", nil)
			}
			return constructionBaseCompilation{receipt: receipt, stages: stages}, nil
		}
		return constructionBaseCompilation{stages: stages}, nil
	}
	if !s.canCompile {
		return constructionBaseCompilation{}, unavailable("construction-capabilities", "CAPABILITY_UNAVAILABLE", "authorized construction compilation is not configured", nil)
	}
	compileWorkspace, err := constructionCapabilitiesWorkspace(workspace, outputID)
	if err != nil {
		return constructionBaseCompilation{}, conflict("construction-capabilities", "AUTHORING_STATE_MISSING", "the saved Explorer draft cannot be projected for selected-output capabilities", nil, err)
	}
	receipt, err := s.compile(ctx, CompileRequest{
		Project: project, ExplorerID: explorerID, Workspace: compileWorkspace, SnapshotToken: snapshotToken,
		RequestID: "construction-capabilities",
	})
	if err != nil {
		return constructionBaseCompilation{}, err
	}
	if _, err := s.verifyProposalReceipt(ctx, "construction-capabilities", receipt, project, explorerID, snapshotToken, snapshot, &compileWorkspace); err != nil {
		return constructionBaseCompilation{}, err
	}
	if err := validateReceiptOutputContract(receipt, outputID); err != nil {
		return constructionBaseCompilation{}, unprocessable("construction-capabilities", "OUTPUT_NOT_FOUND", "outputId is not in the compiler output contract", nil)
	}
	if len(receipt.ConstructionStages[outputID]) == 0 {
		return constructionBaseCompilation{}, conflict("construction-capabilities", "STAGE_CAPABILITIES_UNAVAILABLE", "the compiler receipt has no stage descriptors for this output", nil, nil)
	}
	return constructionBaseCompilation{receipt: receipt, stages: receipt.ConstructionStages[outputID]}, nil
}
