package arango

import (
	"context"
	"errors"
	"fmt"

	publication "github.com/calypr/loom/internal/dataset"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	metadataBatchSize = 32
	documentKeyDomain = "loom.datasetstore.v1"
)

var (
	ErrNilQueryClient             = errors.New("generation store query client is required")
	ErrManifestAlreadyExists      = errors.New("generation manifest already exists")
	ErrManifestNotFound           = publication.ErrManifestNotFound
	ErrManifestTransitionConflict = errors.New("generation manifest transition conflict")
	ErrActivationConflict         = errors.New("generation activation conflict")
	ErrUnexpectedStoreResult      = errors.New("unexpected generation store result")
)

type QueryRowsClient interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arangostore.RowVisitor) error
}

var _ QueryRowsClient = (*arangostore.Client)(nil)
var _ publication.ActiveResolver = (*Store)(nil)

type Store struct{ client QueryRowsClient }

func New(client QueryRowsClient) (*Store, error) {
	if client == nil {
		return nil, ErrNilQueryClient
	}
	return &Store{client: client}, nil
}

func (s *Store) CreateManifest(ctx context.Context, manifest publication.Manifest) (publication.Manifest, error) {
	if err := manifest.Validate(); err != nil {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w", err)
	}
	if manifest.State != publication.StateLoading {
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w: state must be %s, got %s", publication.ErrInvalidTransition, publication.StateLoading, manifest.State)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	document, err := manifestDocument(manifest)
	if err != nil {
		return publication.Manifest{}, err
	}
	bindVars := lifecycleBindVars(manifest.Dataset.Project)
	// Arango rejects undeclared bind parameters. The create query derives the
	// project and active record from the immutable documents themselves.
	delete(bindVars, "project")
	delete(bindVars, "active_record_type")
	bindVars["manifest"] = document
	bindVars["active_placeholder"] = activePlaceholderDocument(manifest.Dataset.Project)
	bindVars["active_release_placeholder"] = activeReleasePlaceholderDocument(manifest.Dataset.Project)
	bindVars["manifest_key"] = manifestDocumentKey(manifest.Dataset)
	bindVars["active_key"] = activeDocumentKey(manifest.Dataset.Project)
	bindVars["active_release_key"] = activeReleaseDocumentKey(manifest.Dataset.Project)

	var created *publication.Manifest
	var unexpected error
	err = s.client.QueryRows(ctx, createManifestAQL, metadataBatchSize, bindVars, func(row map[string]any) error {
		recordType, _ := row["recordType"].(string)
		switch recordType {
		case manifestRecordType:
			if created != nil {
				unexpected = fmt.Errorf("%w: create returned more than one manifest", ErrUnexpectedStoreResult)
				return unexpected
			}
			decoded, err := manifestFromValue(row["manifest"])
			if err != nil {
				unexpected = err
				return err
			}
			if !manifestIdentityEqual(decoded, manifest) {
				unexpected = fmt.Errorf("%w: created manifest does not match request", ErrUnexpectedStoreResult)
				return unexpected
			}
			created = &decoded
		case activeRecordType:
		default:
			unexpected = fmt.Errorf("%w: create returned record type %q", ErrUnexpectedStoreResult, recordType)
			return unexpected
		}
		return nil
	})
	if err != nil {
		if unexpected != nil {
			return publication.Manifest{}, unexpected
		}
		return publication.Manifest{}, fmt.Errorf("create generation manifest: %w", err)
	}
	if created == nil {
		return publication.Manifest{}, fmt.Errorf("%w: %s/%s", ErrManifestAlreadyExists, manifest.Dataset.Project, manifest.Dataset.Generation)
	}
	return *created, nil
}

func (s *Store) ReadManifest(ctx context.Context, ref publication.Ref) (publication.Manifest, error) {
	if err := ref.Validate(); err != nil {
		return publication.Manifest{}, fmt.Errorf("read generation manifest: %w", err)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	bindVars := lifecycleBindVars(ref.Project)
	delete(bindVars, "active_record_type")
	bindVars["manifest_key"] = manifestDocumentKey(ref)
	bindVars["generation"] = ref.Generation
	rows, err := s.manifestRows(ctx, readManifestAQL, bindVars)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("read generation manifest: %w", err)
	}
	if len(rows) == 0 {
		return publication.Manifest{}, fmt.Errorf("%w: %s/%s", ErrManifestNotFound, ref.Project, ref.Generation)
	}
	if rows[0].Dataset != ref {
		return publication.Manifest{}, fmt.Errorf("%w: read manifest reference does not match request", ErrUnexpectedStoreResult)
	}
	return rows[0], nil
}

func (s *Store) TransitionManifest(ctx context.Context, manifest publication.Manifest, next publication.State) (publication.Manifest, error) {
	nextManifest, err := manifest.Transition(next)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("transition generation manifest: %w", err)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	schemaIdentity, err := schemaIdentityBindValue(manifest.SchemaIdentity)
	if err != nil {
		return publication.Manifest{}, err
	}
	bindVars := lifecycleBindVars(manifest.Dataset.Project)
	delete(bindVars, "active_record_type")
	bindVars["manifest_key"] = manifestDocumentKey(manifest.Dataset)
	bindVars["generation"] = manifest.Dataset.Generation
	bindVars["expected_state"] = string(manifest.State)
	bindVars["next_state"] = string(next)
	bindVars["schema_identity"] = schemaIdentity
	rows, err := s.manifestRows(ctx, transitionManifestAQL, bindVars)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("transition generation manifest: %w", err)
	}
	if len(rows) == 0 {
		return publication.Manifest{}, fmt.Errorf("%w: %s/%s was not %s with the expected immutable metadata", ErrManifestTransitionConflict, manifest.Dataset.Project, manifest.Dataset.Generation, manifest.State)
	}
	if !manifestIdentityEqual(rows[0], nextManifest) {
		return publication.Manifest{}, fmt.Errorf("%w: transition result does not match requested state", ErrUnexpectedStoreResult)
	}
	return rows[0], nil
}

func (s *Store) ResolveActiveManifest(ctx context.Context, project string) (publication.Manifest, error) {
	if err := validateProject(project); err != nil {
		return publication.Manifest{}, fmt.Errorf("resolve active generation: %w", err)
	}
	if err := s.validate(); err != nil {
		return publication.Manifest{}, err
	}
	bindVars := lifecycleBindVars(project)
	bindVars["active_key"] = activeDocumentKey(project)
	bindVars["staged_state"] = string(publication.StateStaged)
	bindVars["ready_state"] = string(publication.StateReady)
	rows, err := s.manifestRows(ctx, readActiveAQL, bindVars)
	if err != nil {
		return publication.Manifest{}, fmt.Errorf("resolve active generation: %w", err)
	}
	if len(rows) == 0 {
		return publication.Manifest{}, fmt.Errorf("%w: %s", publication.ErrNoActiveGeneration, project)
	}
	if rows[0].Dataset.Project != project || !rows[0].IsReady() {
		return publication.Manifest{}, fmt.Errorf("%w: active pointer and staged manifest are inconsistent", ErrUnexpectedStoreResult)
	}
	return rows[0], nil
}

func (s *Store) Activate(ctx context.Context, candidate publication.Manifest) error {
	if !candidate.IsReady() {
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("activate generation: %w", err)
		}
		return fmt.Errorf("activate generation: %w: %s is %s", publication.ErrGenerationNotReady, candidate.Dataset.Generation, candidate.State)
	}
	if err := s.validate(); err != nil {
		return err
	}
	schemaIdentity, err := schemaIdentityBindValue(candidate.SchemaIdentity)
	if err != nil {
		return err
	}
	bindVars := lifecycleBindVars(candidate.Dataset.Project)
	bindVars["candidate_key"] = manifestDocumentKey(candidate.Dataset)
	bindVars["active_key"] = activeDocumentKey(candidate.Dataset.Project)
	bindVars["generation"] = candidate.Dataset.Generation
	bindVars["schema_identity"] = schemaIdentity
	bindVars["staged_state"] = string(publication.StateStaged)
	bindVars["ready_state"] = string(publication.StateReady)

	var selected bool
	var unexpected error
	err = s.client.QueryRows(ctx, activateAQL, metadataBatchSize, bindVars, func(row map[string]any) error {
		if selected {
			unexpected = fmt.Errorf("%w: activation returned multiple active pointers", ErrUnexpectedStoreResult)
			return unexpected
		}
		ref, err := refFromValue(row["dataset"])
		if err != nil {
			unexpected = err
			return err
		}
		if ref != candidate.Dataset {
			unexpected = fmt.Errorf("%w: activation selected a different generation", ErrUnexpectedStoreResult)
			return unexpected
		}
		selected = true
		return nil
	})
	if err != nil {
		if unexpected != nil {
			return unexpected
		}
		return fmt.Errorf("activate generation: %w", err)
	}
	if !selected {
		return fmt.Errorf("%w: candidate %s/%s was not a persisted staged manifest with a valid active pointer", ErrActivationConflict, candidate.Dataset.Project, candidate.Dataset.Generation)
	}
	return nil
}

const createManifestAQL = `
LET existing = DOCUMENT(@@lifecycle_collection, @manifest_key)
FILTER existing == null
LET active = DOCUMENT(@@lifecycle_collection, @active_key)
LET activeRelease = DOCUMENT(@@lifecycle_collection, @active_release_key)
LET documents = APPEND([@manifest], APPEND(active == null ? [@active_placeholder] : [], activeRelease == null ? [@active_release_placeholder] : []))
FOR document IN documents
  INSERT document INTO @@lifecycle_collection
  FILTER NEW.recordType == @manifest_record_type
  RETURN {
    recordType: NEW.recordType,
    manifest: NEW.recordType == @manifest_record_type ? {
      dataset: NEW.dataset,
      state: NEW.state,
      schemaIdentity: NEW.schemaIdentity
    } : null
  }
`

const readManifestAQL = `
FOR manifest IN @@lifecycle_collection
  FILTER manifest._key == @manifest_key
  FILTER manifest.recordType == @manifest_record_type
  FILTER manifest.dataset.project == @project
  FILTER manifest.dataset.generation == @generation
  LIMIT 2
  RETURN {
    dataset: manifest.dataset,
    state: manifest.state,
    schemaIdentity: manifest.schemaIdentity,
    analysisVersion: manifest.analysisVersion
  }
`

const transitionManifestAQL = `
FOR manifest IN @@lifecycle_collection
  FILTER manifest._key == @manifest_key
  FILTER manifest.recordType == @manifest_record_type
  FILTER manifest.dataset.project == @project
  FILTER manifest.dataset.generation == @generation
  FILTER manifest.state == @expected_state
  FILTER manifest.schemaIdentity == @schema_identity
  UPDATE manifest WITH { state: @next_state } IN @@lifecycle_collection
  RETURN {
    dataset: NEW.dataset,
    state: NEW.state,
    schemaIdentity: NEW.schemaIdentity
  }
`

const readActiveAQL = `
FOR active IN @@lifecycle_collection
  FILTER active._key == @active_key
  FILTER active.recordType == @active_record_type
  FILTER active.project == @project
  FILTER active.manifestKey != null
  FOR manifest IN @@lifecycle_collection
    FILTER manifest._key == active.manifestKey
    FILTER manifest.recordType == @manifest_record_type
    FILTER manifest.dataset == active.dataset
    FILTER manifest.state == @ready_state OR manifest.state == @staged_state
    LIMIT 2
    RETURN {
      dataset: manifest.dataset,
      state: manifest.state,
      schemaIdentity: manifest.schemaIdentity,
      analysisVersion: manifest.analysisVersion
    }
`

const activateAQL = `
LET candidate = FIRST(
  FOR manifest IN @@lifecycle_collection
    FILTER manifest._key == @candidate_key
    FILTER manifest.recordType == @manifest_record_type
    FILTER manifest.dataset.project == @project
    FILTER manifest.dataset.generation == @generation
    FILTER manifest.state == @ready_state OR manifest.state == @staged_state
    FILTER manifest.schemaIdentity == @schema_identity
    RETURN manifest
)
LET active = DOCUMENT(@@lifecycle_collection, @active_key)
FILTER candidate != null
FILTER active != null
FILTER active.recordType == @active_record_type
FILTER active.project == @project
UPDATE active WITH { dataset: candidate.dataset, manifestKey: candidate._key } IN @@lifecycle_collection
  OPTIONS { ignoreRevs: false, mergeObjects: false }
RETURN { dataset: NEW.dataset }
`
