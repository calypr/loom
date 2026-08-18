package explorer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
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
	return s.store.List(ctx, project)
}
func (s *Service) RepositoryConfig(ctx context.Context, project string) (*RepositoryConfig, error) {
	return s.store.GetRepositoryConfig(ctx, project)
}
func (s *Service) SaveRepositoryConfig(ctx context.Context, value RepositoryConfig) (*RepositoryConfig, error) {
	return s.store.SaveRepositoryConfig(ctx, value)
}
func (s *Service) ListConfigs(ctx context.Context, project string) ([]RepositoryConfig, error) {
	return s.store.ListConfigs(ctx, project)
}
func (s *Service) Config(ctx context.Context, project, id string) (*RepositoryConfig, error) {
	return s.store.GetConfig(ctx, project, id)
}
func (s *Service) SaveConfig(ctx context.Context, value RepositoryConfig) (*RepositoryConfig, error) {
	return s.store.SaveConfig(ctx, value)
}
func (s *Service) Get(ctx context.Context, project, id string) (*Explorer, error) {
	return s.store.Get(ctx, project, id)
}
func (s *Service) Revision(ctx context.Context, id string) (*Revision, error) {
	return s.store.GetRevision(ctx, id)
}
func (s *Service) ActiveRevision(ctx context.Context, project, id string) (*Revision, error) {
	e, err := s.store.Get(ctx, project, id)
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
	project := cfg.Project
	owner, err := s.store.Get(ctx, project, "default")
	if errors.Is(err, ErrNotFound) {
		owner, err = s.store.CreateRepository(ctx, Explorer{Project: project, ExplorerID: "default", Title: cfg.Explorer.Title, ManagementMode: ManagementRepository, DraftConfig: canonical, DraftVersion: 1, DraftDigest: configDigest, SourceGeneration: sourceGeneration, Dataset: dataset, Publication: publication, UpdatedBy: actor, UpdatedAt: s.now()})
	} else if err == nil {
		owner.Title, owner.DraftConfig, owner.DraftDigest, owner.SourceGeneration, owner.Dataset, owner.Publication, owner.UpdatedBy = cfg.Explorer.Title, canonical, configDigest, sourceGeneration, dataset, publication, actor
		owner, err = s.store.SaveDraft(ctx, *owner, owner.DraftVersion)
	}
	if err != nil {
		return nil, nil, err
	}
	revisionID := RepositoryRevisionID(project, sourceCommit, configDigest, sourceGeneration)
	now := s.now()
	revision, err := s.store.InsertRevision(ctx, Revision{ID: revisionID, Project: project, ExplorerID: "default", Config: canonical, ConfigDigest: configDigest, Recipe: compiled.Bundle, RecipeDigest: compiled.RecipeDigest, ResolvedSchemaDigest: resolvedSchemaDigest, SourceGeneration: sourceGeneration, SourceCommit: sourceCommit, Dataset: dataset, Publication: publication, Materializations: append([]Materialization(nil), materializations...), EmittedColumns: append([]EmittedColumn(nil), compiled.EmittedColumns...), Status: RevisionReady, CreatedBy: actor, CreatedAt: now, ReadyAt: &now})
	if err != nil {
		return nil, nil, err
	}
	return owner, revision, nil
}
func (s *Service) ActivateInteractive(ctx context.Context, project, explorerID, revisionID string) error {
	return s.store.ActivateInteractive(ctx, project, explorerID, revisionID)
}
func (s *Service) ActivateRepository(ctx context.Context, project, revisionID string) error {
	return s.store.ActivateRepository(ctx, project, revisionID)
}
func (s *Service) ActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) error {
	return s.store.ActivateRepositoryGeneration(ctx, project, generation, revisionID)
}

// RepositoryRevisionID is stable for retrying the same checked-out repository
// content against the same Loom generation. It intentionally has no Gecko
// organization/project identity component.
func RepositoryRevisionID(project, sourceCommit, definitionDigest, sourceGeneration string) string {
	sum := sha256.Sum256([]byte(project + "\x00default\x00" + sourceCommit + "\x00" + definitionDigest + "\x00" + sourceGeneration))
	return "repository_" + hex.EncodeToString(sum[:])
}

// CreateInteractiveV2 persists a complete V2 draft without compiling or
// materializing it. The raw canonical packet is retained alongside the digest
// so presentation-only fields survive a later read unchanged.
func (s *Service) CreateInteractiveV2(ctx context.Context, project, id string, raw []byte, actor string) (*Explorer, error) {
	if id == "default" {
		return nil, fmt.Errorf("default Explorer is repository-managed")
	}
	cfg, _, canonical, digest, err := CanonicalConfigV2(raw, project, id, "interactive")
	if err != nil {
		return nil, err
	}
	return s.store.CreateInteractive(ctx, Explorer{
		Project: project, ExplorerID: id, Title: cfg.Explorer.Title,
		ManagementMode: ManagementInteractive, DraftConfig: canonical,
		DraftVersion: 1, DraftDigest: digest, UpdatedBy: actor, UpdatedAt: s.now(),
	})
}

// SaveInteractiveDraftV2 is the only V2 draft write path. It performs no
// compilation or materialization and supplies both CAS components to the
// durable adapter.
func (s *Service) SaveInteractiveDraftV2(ctx context.Context, project, id string, raw []byte, expected int64, expectedDigest, actor string) (*Explorer, error) {
	if id == "default" {
		return nil, fmt.Errorf("default Explorer is repository-managed")
	}
	cfg, _, canonical, digest, err := CanonicalConfigV2(raw, project, id, "interactive")
	if err != nil {
		return nil, err
	}
	current, err := s.store.Get(ctx, project, id)
	if err != nil {
		return nil, err
	}
	if current.ManagementMode != ManagementInteractive {
		return nil, fmt.Errorf("Explorer is not interactive")
	}
	current.Title = cfg.Explorer.Title
	current.DraftConfig = canonical
	current.DraftDigest = digest
	current.UpdatedBy = actor
	return s.store.SaveDraft(ctx, *current, expected, expectedDigest)
}

func (s *Service) InsertInteractiveReadyRevisionV2(ctx context.Context, owner *Explorer, config []byte, configDigest string, compiled Compilation, resolvedSchemaDigest, sourceGeneration, actor string, materializations []Materialization) (*Revision, error) {
	if owner == nil || owner.ManagementMode != ManagementInteractive {
		return nil, fmt.Errorf("Explorer is not interactive")
	}
	id, err := NewInteractiveRevisionID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	return s.store.InsertRevision(ctx, Revision{
		ID: id, Project: owner.Project, ExplorerID: owner.ExplorerID,
		Config: append([]byte(nil), config...), ConfigDigest: configDigest,
		Recipe: compiled.Bundle, RecipeDigest: compiled.RecipeDigest,
		ResolvedSchemaDigest: resolvedSchemaDigest, SourceGeneration: sourceGeneration,
		Dataset:          datasetMetadata(sourceGeneration, resolvedSchemaDigest, materializations),
		Publication:      PublicationMetadata{State: string(RevisionReady), Generation: sourceGeneration, UpdatedAt: now},
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

func (s *Service) InsertInteractiveFailedRevisionV2(ctx context.Context, owner *Explorer, config []byte, configDigest, sourceGeneration, actor string, diagnostics []Diagnostic) (*Revision, error) {
	if owner == nil || owner.ManagementMode != ManagementInteractive {
		return nil, fmt.Errorf("Explorer is not interactive")
	}
	id, err := NewInteractiveRevisionID()
	if err != nil {
		return nil, err
	}
	now := s.now()
	return s.store.InsertRevision(ctx, Revision{ID: id, Project: owner.Project, ExplorerID: owner.ExplorerID, Config: append([]byte(nil), config...), ConfigDigest: configDigest, SourceGeneration: sourceGeneration, Diagnostics: append([]Diagnostic(nil), diagnostics...), Status: RevisionFailed, CreatedBy: actor, CreatedAt: now, FailedAt: &now})
}
