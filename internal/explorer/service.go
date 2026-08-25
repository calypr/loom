package explorer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/projectid"
)

// Service is the persistence-neutral lifecycle coordinator. Compiler and
// publication adapters are intentionally injected by server wiring.
type Service struct {
	store Store
	now   func() time.Time
}

func NewService(store Store) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("Explorer store is required")
	}
	return &Service{store: store, now: func() time.Time { return time.Now().UTC() }}, nil
}
func (s *Service) List(ctx context.Context, project string) ([]Explorer, error) {
	return s.store.List(ctx, projectid.Legacy(project))
}
func (s *Service) RepositoryConfig(ctx context.Context, project string) (*RepositoryConfig, error) {
	return s.store.GetRepositoryConfig(ctx, projectid.Legacy(project))
}
func (s *Service) SaveRepositoryConfig(ctx context.Context, value RepositoryConfig) (*RepositoryConfig, error) {
	value.Project = projectid.Legacy(value.Project)
	return s.store.SaveRepositoryConfig(ctx, value)
}
func (s *Service) ListConfigs(ctx context.Context, project string) ([]RepositoryConfig, error) {
	return s.store.ListConfigs(ctx, projectid.Legacy(project))
}
func (s *Service) Config(ctx context.Context, project, id string) (*RepositoryConfig, error) {
	return s.store.GetConfig(ctx, projectid.Legacy(project), id)
}
func (s *Service) SaveConfig(ctx context.Context, value RepositoryConfig) (*RepositoryConfig, error) {
	value.Project = projectid.Legacy(value.Project)
	return s.store.SaveConfig(ctx, value)
}
func (s *Service) Get(ctx context.Context, project, id string) (*Explorer, error) {
	return s.store.Get(ctx, projectid.Legacy(project), id)
}

// EnsureRepositoryExplorer creates the repository-owned default identity when
// a migration is importing a configuration that has not previously been
// deployed through Loom. Existing identities are returned unchanged so the
// subsequent immutable revision publication can switch the active pointer in
// its normal transaction.
func (s *Service) EnsureRepositoryExplorer(ctx context.Context, project, id, title, actor string) (*Explorer, error) {
	project = projectid.Canonical(project)
	if id == "" {
		id = "default"
	}
	if id != "default" {
		return nil, fmt.Errorf("repository Explorer identity must be default")
	}
	owner, err := s.store.Get(ctx, projectid.Legacy(project), id)
	if err == nil {
		return owner, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if strings.TrimSpace(title) == "" {
		title = "Explorer"
	}
	return s.store.CreateRepository(ctx, Explorer{
		Project:        projectid.Legacy(project),
		ExplorerID:     id,
		Title:          title,
		ManagementMode: ManagementRepository,
		UpdatedBy:      actor,
		UpdatedAt:      s.now(),
	})
}

// CreateEmptyInteractive creates only the Explorer identity. Unpublished
// Explorers have no persisted authoring content and hydrate as an empty valid
// Builder model.
func (s *Service) CreateEmptyInteractive(ctx context.Context, project, id, title, actor string) (*Explorer, error) {
	if id == "" || id == "default" {
		return nil, fmt.Errorf("invalid interactive Explorer identity")
	}
	return s.store.CreateInteractive(ctx, Explorer{Project: projectid.Legacy(project), ExplorerID: id, Title: title, ManagementMode: ManagementInteractive, UpdatedBy: actor, UpdatedAt: s.now()})
}
func (s *Service) Revision(ctx context.Context, id string) (*Revision, error) {
	return s.store.GetRevision(ctx, id)
}
func (s *Service) ActiveRevision(ctx context.Context, project, id string) (*Revision, error) {
	e, err := s.store.Get(ctx, projectid.Legacy(project), id)
	if err != nil {
		return nil, err
	}
	if e.ActiveRevisionID == "" {
		return nil, ErrNotFound
	}
	return s.store.GetRevision(ctx, e.ActiveRevisionID)
}
func (s *Service) FailRevision(ctx context.Context, id string, diagnostics []Diagnostic) (*Revision, error) {
	return s.store.TransitionRevision(ctx, id, RevisionFailed, diagnostics)
}

func (s *Service) CompilationReceipt(ctx context.Context, id string) (*CompilationReceipt, error) {
	return s.store.GetCompilationReceipt(ctx, id)
}

// CompilationReceiptForExplorer performs the tenant-scoped lookup used by
// execution and publication. The ID-only method above remains for legacy
// callers during the receipt migration.
func (s *Service) CompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*CompilationReceipt, error) {
	return s.store.GetCompilationReceiptForExplorer(ctx, projectid.Canonical(project), explorerID, id)
}

func (s *Service) CompilationReceiptByCompilationKey(ctx context.Context, project, explorerID, compilationKey string) (*CompilationReceipt, error) {
	return s.store.GetCompilationReceiptByCompilationKey(ctx, projectid.Canonical(project), explorerID, compilationKey)
}

func (s *Service) CompilationReceiptStats(ctx context.Context, project string) (ReceiptStoreStats, error) {
	return s.store.CompilationReceiptStats(ctx, projectid.Canonical(project))
}

func (s *Service) StoreCompilationReceipt(ctx context.Context, receipt CompilationReceipt) (*CompilationReceipt, error) {
	return s.store.InsertCompilationReceipt(ctx, receipt)
}

func (s *Service) PurgeDraftAuthoring(ctx context.Context) error {
	return s.store.PurgeDraftAuthoring(ctx)
}

func (s *Service) InsertAuthoringRevision(ctx context.Context, revision Revision) (*Revision, error) {
	if revision.ID == "" || revision.CompilationReceiptID == "" || revision.IntentDigest == "" {
		return nil, fmt.Errorf("authoring revision identity is incomplete")
	}
	revision.Project = projectid.Legacy(revision.Project)
	return s.store.InsertRevision(ctx, revision)
}

// PublishAuthoring atomically stores the server receipt and immutable revision
// and switches the Explorer's active pointer.
func (s *Service) PublishAuthoring(ctx context.Context, receipt CompilationReceipt, revision Revision) (*Revision, error) {
	if receipt.ID == "" || revision.ID == "" || revision.CompilationReceiptID != receipt.ID {
		return nil, fmt.Errorf("authoring publication identity is incomplete")
	}
	receipt.Project = projectid.Canonical(receipt.Project)
	revision.Project = projectid.Legacy(revision.Project)
	return s.store.PublishAuthoring(ctx, receipt, revision)
}

// NewInteractiveRevisionID is opaque and collision-resistant; repository
// revisions intentionally use the deterministic tuple identity instead.
func NewInteractiveRevisionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "interactive_" + hex.EncodeToString(bytes), nil
}

// UpsertRepositoryV2 records the repository-owned default and its immutable
// ready revision after the external dataframe publication has succeeded.
// Activation is intentionally a separate call so callers can compose it with
// the dataset release switch where the durable adapter supports that atomic
// transaction.
func (s *Service) UpsertRepositoryV2(ctx context.Context, raw []byte, sourceCommit, sourceGeneration, actor string, compiled Compilation, resolvedSchemaDigest string, materializations []Materialization, dataset DatasetMetadata, publication PublicationMetadata) (*Explorer, *Revision, error) {
	cfg, _, canonical, configDigest, err := CanonicalConfigV2(raw, "", "default", "repository")
	if err != nil {
		// The project is carried by the packet; decode once to obtain it while
		// preserving the strict validator above.
		var envelope struct {
			Project string `json:"project"`
		}
		if decodeErr := json.Unmarshal(raw, &envelope); decodeErr != nil {
			return nil, nil, err
		}
		cfg, _, canonical, configDigest, err = CanonicalConfigV2(raw, envelope.Project, "default", "repository")
		if err != nil {
			return nil, nil, err
		}
	}
	project := projectid.Canonical(cfg.Project)
	storageProject := projectid.Legacy(project)
	owner, err := s.store.Get(ctx, storageProject, "default")
	if errors.Is(err, ErrNotFound) {
		owner, err = s.store.CreateRepository(ctx, Explorer{Project: storageProject, ExplorerID: "default", Title: cfg.Explorer.Title, ManagementMode: ManagementRepository, DraftConfig: canonical, DraftVersion: 1, DraftDigest: configDigest, SourceGeneration: sourceGeneration, Dataset: dataset, Publication: publication, UpdatedBy: actor, UpdatedAt: s.now()})
	} else if err == nil {
		owner.Title, owner.DraftConfig, owner.DraftDigest, owner.SourceGeneration, owner.Dataset, owner.Publication, owner.UpdatedBy = cfg.Explorer.Title, canonical, configDigest, sourceGeneration, dataset, publication, actor
		owner, err = s.store.SaveDraft(ctx, *owner, owner.DraftVersion)
	}
	if err != nil {
		return nil, nil, err
	}
	revisionID := RepositoryRevisionID(project, sourceCommit, configDigest, sourceGeneration)
	now := s.now()
	revision, err := s.store.InsertRevision(ctx, Revision{ID: revisionID, Project: storageProject, ExplorerID: "default", Config: canonical, ConfigDigest: configDigest, Recipe: compiled.Bundle, RecipeDigest: compiled.RecipeDigest, ResolvedSchemaDigest: resolvedSchemaDigest, SourceGeneration: sourceGeneration, SourceCommit: sourceCommit, Dataset: dataset, Publication: publication, Materializations: append([]Materialization(nil), materializations...), EmittedColumns: append([]EmittedColumn(nil), compiled.EmittedColumns...), Status: RevisionReady, CreatedBy: actor, CreatedAt: now, ReadyAt: &now})
	if err != nil {
		return nil, nil, err
	}
	return owner, revision, nil
}
func (s *Service) ActivateInteractive(ctx context.Context, project, explorerID, revisionID string) error {
	return s.store.ActivateInteractive(ctx, projectid.Legacy(project), explorerID, revisionID)
}
func (s *Service) ActivateRepository(ctx context.Context, project, revisionID string) error {
	return s.store.ActivateRepository(ctx, projectid.Legacy(project), revisionID)
}
func (s *Service) ActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) error {
	return s.store.ActivateRepositoryGeneration(ctx, projectid.Legacy(project), generation, revisionID)
}

// RepositoryRevisionID is stable for retrying the same checked-out repository
// content against the same Loom generation. It intentionally has no Gecko
// organization/project identity component.
func RepositoryRevisionID(project, sourceCommit, definitionDigest, sourceGeneration string) string {
	sum := sha256.Sum256([]byte(project + "\x00default\x00" + sourceCommit + "\x00" + definitionDigest + "\x00" + sourceGeneration))
	return "repository_" + hex.EncodeToString(sum[:])
}

// CreateInteractiveV2 persists a complete custom V2 draft without compiling or
// materializing it. The repository default is created by repository deploys;
// it uses SaveDraftV2 for subsequent Builder edits.
func (s *Service) CreateInteractiveV2(ctx context.Context, project, id string, raw []byte, actor string) (*Explorer, error) {
	if id == "default" {
		return nil, fmt.Errorf("default Explorer already exists; edit its draft")
	}
	cfg, _, canonical, digest, err := CanonicalConfigV2(raw, project, id, "interactive")
	if err != nil {
		return nil, err
	}
	canonicalProject := projectid.Canonical(project)
	return s.store.CreateInteractive(ctx, Explorer{
		Project: projectid.Legacy(canonicalProject), ExplorerID: id, Title: cfg.Explorer.Title,
		ManagementMode: ManagementInteractive, DraftConfig: canonical,
		DraftVersion: 1, DraftDigest: digest, UpdatedBy: actor, UpdatedAt: s.now(),
	})
}

// SaveDraftV2 is the only V2 draft write path. It performs no compilation or
// materialization. Draft writes are intentionally last-write-wins: the
// expected version and digest are retained for API compatibility and
// observability, but a stale Builder cannot block the latest valid draft from
// being saved.
// The repository default is editable; its repository identity is retained in
// the packet so a later ETL deployment can still recognize it.
func (s *Service) SaveDraftV2(ctx context.Context, project, id string, raw []byte, expected int64, expectedDigest, actor string) (*Explorer, error) {
	management := ConfigManagementForID(id)
	cfg, _, canonical, digest, err := CanonicalConfigV2(raw, project, id, management)
	if err != nil {
		return nil, err
	}
	canonicalProject := projectid.Canonical(project)
	storageProject := projectid.Legacy(canonicalProject)
	current, err := s.store.Get(ctx, storageProject, id)
	if errors.Is(err, ErrNotFound) && id == "default" {
		repository, repositoryErr := s.store.GetRepositoryConfig(ctx, storageProject)
		if repositoryErr != nil {
			return nil, repositoryErr
		}
		baseVersion := repository.DraftVersion
		if baseVersion == 0 {
			baseVersion = 1
		}
		baseDigest := repository.ConfigDigest
		if baseDigest == "" {
			_, _, _, baseDigest, _ = CanonicalConfigV2(repository.Config, project, "default", "repository")
		}
		updatedAt := repository.UpdatedAt
		if updatedAt.IsZero() {
			updatedAt = s.now()
		}
		current, err = s.store.CreateRepository(ctx, Explorer{
			Project: storageProject, ExplorerID: "default", Title: cfg.Explorer.Title,
			ManagementMode: ManagementRepository, DraftConfig: append([]byte(nil), repository.Config...),
			DraftVersion: baseVersion, DraftDigest: baseDigest,
			ActiveRevisionID: repository.ActiveRevisionID, ActiveConfig: append([]byte(nil), repository.Config...),
			SourceGeneration: repository.SourceGeneration, Materializations: append([]Materialization(nil), repository.Materializations...),
			Dataset: repository.Dataset, Publication: repository.Publication, Diagnostics: append([]Diagnostic(nil), repository.Diagnostics...),
			UpdatedBy: "repository", UpdatedAt: updatedAt,
		})
		if errors.Is(err, ErrDraftConflict) {
			current, err = s.store.Get(ctx, storageProject, id)
		}
	}
	if err != nil {
		return nil, err
	}
	expectedManagement := ManagementInteractive
	if id == "default" {
		expectedManagement = ManagementRepository
	}
	if current.ManagementMode != expectedManagement {
		return nil, fmt.Errorf("Explorer management mode does not match its identity")
	}
	current.Title = cfg.Explorer.Title
	current.DraftConfig = canonical
	current.DraftDigest = digest
	current.UpdatedBy = actor
	return s.store.SaveDraft(ctx, *current, expected, expectedDigest)
}

func (s *Service) InsertReadyRevisionV2(ctx context.Context, owner *Explorer, config []byte, configDigest string, compiled Compilation, resolvedSchemaDigest, sourceGeneration, actor string, materializations []Materialization) (*Revision, error) {
	return s.InsertReadyRevisionV2WithMetadata(ctx, owner, config, configDigest, compiled, resolvedSchemaDigest, sourceGeneration, actor, materializations, datasetMetadata(sourceGeneration, resolvedSchemaDigest, materializations), PublicationMetadata{})
}

// InsertReadyRevisionV2WithMetadata records a configuration revision against
// an already materialized dataset. Callers that publish an Explorer
// configuration without rebuilding data pass the active release's frozen
// dataset/materialization metadata here so the revision remains queryable.
func (s *Service) InsertReadyRevisionV2WithMetadata(ctx context.Context, owner *Explorer, config []byte, configDigest string, compiled Compilation, resolvedSchemaDigest, sourceGeneration, actor string, materializations []Materialization, dataset DatasetMetadata, publication PublicationMetadata) (*Revision, error) {
	if owner == nil || (owner.ManagementMode != ManagementInteractive && !(owner.ExplorerID == "default" && owner.ManagementMode == ManagementRepository)) {
		return nil, fmt.Errorf("Explorer is not editable")
	}
	id, err := NewInteractiveRevisionID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	if dataset.Generation == "" {
		dataset.Generation = sourceGeneration
	}
	if dataset.SchemaDigest == "" {
		dataset.SchemaDigest = resolvedSchemaDigest
	}
	if publication.State == "" {
		publication.State = string(RevisionReady)
	}
	if publication.Generation == "" {
		publication.Generation = sourceGeneration
	}
	if publication.UpdatedAt.IsZero() {
		publication.UpdatedAt = now
	}
	return s.store.InsertRevision(ctx, Revision{
		ID: id, Project: owner.Project, ExplorerID: owner.ExplorerID,
		Config: append([]byte(nil), config...), ConfigDigest: configDigest,
		Recipe: compiled.Bundle, RecipeDigest: compiled.RecipeDigest,
		ResolvedSchemaDigest: resolvedSchemaDigest, SourceGeneration: sourceGeneration,
		Dataset:          dataset,
		Publication:      publication,
		EmittedColumns:   append([]EmittedColumn(nil), compiled.EmittedColumns...),
		Materializations: append([]Materialization(nil), materializations...),
		Status:           RevisionReady, CreatedBy: actor, CreatedAt: now, ReadyAt: &now,
	})
}

func datasetMetadata(generation, schemaDigest string, materializations []Materialization) DatasetMetadata {
	outputs := make([]DatasetOutput, 0, len(materializations))
	for _, materialization := range materializations {
		outputs = append(outputs, DatasetOutput{
			Name:  materialization.Output,
			State: string(RevisionReady), Queryable: true,
			Selector: materialization.Selector,
			Columns:  append([]publication.PhysicalColumn(nil), materialization.Columns...),
		})
	}
	return DatasetMetadata{Generation: generation, SchemaDigest: schemaDigest, Outputs: outputs}
}

func (s *Service) InsertFailedRevisionV2(ctx context.Context, owner *Explorer, config []byte, configDigest, sourceGeneration, actor string, diagnostics []Diagnostic) (*Revision, error) {
	if owner == nil || (owner.ManagementMode != ManagementInteractive && !(owner.ExplorerID == "default" && owner.ManagementMode == ManagementRepository)) {
		return nil, fmt.Errorf("Explorer is not editable")
	}
	id, err := NewInteractiveRevisionID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	return s.store.InsertRevision(ctx, Revision{ID: id, Project: owner.Project, ExplorerID: owner.ExplorerID, Config: append([]byte(nil), config...), ConfigDigest: configDigest, SourceGeneration: sourceGeneration, Diagnostics: append([]Diagnostic(nil), diagnostics...), Status: RevisionFailed, CreatedBy: actor, CreatedAt: now, FailedAt: &now})
}
