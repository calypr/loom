package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	publicationarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	dataset "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	QueryRows(context.Context, string, int, map[string]any, store.RowVisitor) error
	WithTransaction(context.Context, store.TransactionCollections, store.TransactionFunc) error
}
type Store struct{ client client }

func New(client client) (*Store, error) {
	if client == nil {
		return nil, fmt.Errorf("Explorer Arango client is required")
	}
	return &Store{client: client}, nil
}
func key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func explorerKey(project, id string) string { return "explorer_" + key(project, id) }
func repositoryConfigKey(project string) string {
	return "repository_config_" + key(project, "default")
}
func configKey(project, id string) string { return "explorer_config_" + key(project, id) }
func (s *Store) ListConfigs(ctx context.Context, project string) ([]explorer.RepositoryConfig, error) {
	out := []explorer.RepositoryConfig{}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project SORT d.explorerId RETURN d`, 1000, map[string]any{"@c": RepositoryConfigsCollection, "project": project}, func(row map[string]any) error {
		v, err := decode[explorer.RepositoryConfig](row)
		if err == nil {
			out = append(out, v)
		}
		return err
	})
	for i := range out {
		if out[i].ExplorerID == "" {
			out[i].ExplorerID, out[i].Management = "default", explorer.ManagementRepository
		}
	}
	return out, err
}
func (s *Store) GetConfig(ctx context.Context, project, id string) (*explorer.RepositoryConfig, error) {
	var out *explorer.RepositoryConfig
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RepositoryConfigsCollection, "key": configKey(project, id)}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	if out.ExplorerID == "" {
		out.ExplorerID, out.Management = "default", explorer.ManagementRepository
	}
	return out, nil
}
func (s *Store) SaveConfig(ctx context.Context, value explorer.RepositoryConfig) (*explorer.RepositoryConfig, error) {
	value.UpdatedAt = time.Now().UTC()
	doc, err := document(value, configKey(value.Project, value.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.RepositoryConfig
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE @doc IN @@c RETURN NEW`, 1, map[string]any{"@c": RepositoryConfigsCollection, "key": configKey(value.Project, value.ExplorerID), "doc": doc}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Store) GetRepositoryConfig(ctx context.Context, project string) (*explorer.RepositoryConfig, error) {
	var out *explorer.RepositoryConfig
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RepositoryConfigsCollection, "key": repositoryConfigKey(project)}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	if out.ExplorerID == "" {
		out.ExplorerID, out.Management = "default", explorer.ManagementRepository
	}
	return out, nil
}
func (s *Store) SaveRepositoryConfig(ctx context.Context, value explorer.RepositoryConfig) (*explorer.RepositoryConfig, error) {
	value.ExplorerID, value.Management = "default", explorer.ManagementRepository
	value.UpdatedAt = time.Now().UTC()
	doc, err := document(value, repositoryConfigKey(value.Project))
	if err != nil {
		return nil, err
	}
	var out *explorer.RepositoryConfig
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE @doc IN @@c RETURN NEW`, 1, map[string]any{"@c": RepositoryConfigsCollection, "key": repositoryConfigKey(value.Project), "doc": doc}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (s *Store) List(ctx context.Context, project string) ([]explorer.Explorer, error) {
	out := []explorer.Explorer{}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project SORT d.explorerId RETURN d`, 1000, map[string]any{"@c": ExplorersCollection, "project": project}, func(row map[string]any) error {
		v, err := decode[explorer.Explorer](row)
		if err == nil {
			out = append(out, v)
		}
		return err
	})
	return out, err
}
func (s *Store) Get(ctx context.Context, project, id string) (*explorer.Explorer, error) {
	var out *explorer.Explorer
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project RETURN d`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(project, id), "project": project}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}
func (s *Store) CreateInteractive(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	raw, err := document(e, explorerKey(e.Project, e.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.Explorer
	err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": ExplorersCollection, "doc": raw}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrDraftConflict
	}
	return out, nil
}
func (s *Store) CreateRepository(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	return s.CreateInteractive(ctx, e)
}
func (s *Store) SaveDraft(ctx context.Context, e explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	e.UpdatedAt = time.Now().UTC()
	raw, err := document(e, explorerKey(e.Project, e.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.Explorer
	err = s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH MERGE(@doc, { draftVersion: d.draftVersion + 1 }) IN @@c RETURN NEW`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(e.Project, e.ExplorerID), "doc": raw}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrDraftConflict
	}
	return out, nil
}

func (s *Store) InsertCompilationReceipt(ctx context.Context, receipt explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	doc, err := document(receipt, receipt.ID)
	if err != nil {
		return nil, err
	}
	// INSERT with overwriteMode=ignore makes the key immutable under retries
	// and concurrent requests. The read below is authoritative and compares
	// the content-addressed identity, detecting a corrupted key collision.
	err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": CompilationReceiptsCollection, "doc": doc}, func(map[string]any) error { return nil })
	if err != nil {
		return nil, err
	}
	if receipt.Project != "" && receipt.ExplorerID != "" {
		return s.GetCompilationReceiptForExplorer(ctx, receipt.Project, receipt.ExplorerID, receipt.ID)
	}
	return s.GetCompilationReceipt(ctx, receipt.ID)
}

func (s *Store) GetCompilationReceipt(ctx context.Context, id string) (*explorer.CompilationReceipt, error) {
	return s.readCompilationReceipt(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, map[string]any{"@c": CompilationReceiptsCollection, "key": id})
}

func (s *Store) GetCompilationReceiptForExplorer(ctx context.Context, project, explorerID, id string) (*explorer.CompilationReceipt, error) {
	return s.readCompilationReceipt(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId RETURN d`, map[string]any{"@c": CompilationReceiptsCollection, "key": id, "project": project, "explorerId": explorerID})
}

func (s *Store) GetCompilationReceiptByCompilationKey(ctx context.Context, project, explorerID, compilationKey string) (*explorer.CompilationReceipt, error) {
	return s.readCompilationReceipt(ctx, `FOR d IN @@c FILTER d.project == @project AND d.explorerId == @explorerId AND d.compilationKey == @compilationKey SORT d._key LIMIT 1 RETURN d`, map[string]any{"@c": CompilationReceiptsCollection, "project": project, "explorerId": explorerID, "compilationKey": compilationKey})
}

func (s *Store) readCompilationReceipt(ctx context.Context, query string, binds map[string]any) (*explorer.CompilationReceipt, error) {
	var out *explorer.CompilationReceipt
	err := s.client.QueryRows(ctx, query, 1, binds, func(row map[string]any) error {
		value, decodeErr := decode[explorer.CompilationReceipt](row)
		if decodeErr != nil {
			return decodeErr
		}
		if err := validateReceipt(value); err != nil {
			return err
		}
		out = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func validateReceipt(receipt explorer.CompilationReceipt) error {
	if strings.TrimSpace(receipt.ID) == "" {
		return explorer.ErrCorruptReceipt
	}
	if receipt.ReceiptFormatVersion != 0 || receipt.CompilerContractVersion != "" || receipt.CompilationKey != "" {
		if err := receipt.Validate(); err != nil {
			return err
		}
	}
	if strings.HasPrefix(receipt.ID, "receipt_") && len(receipt.ID) == len("receipt_")+sha256.Size*2 {
		if _, err := hex.DecodeString(strings.TrimPrefix(receipt.ID, "receipt_")); err != nil {
			return explorer.ErrCorruptReceipt
		}
		if err := receipt.ValidateID(); err != nil {
			return explorer.ErrCorruptReceipt
		}
	}
	return nil
}

func sameReceipt(a, b explorer.CompilationReceipt) bool {
	left, leftErr := explorer.ReceiptID(a)
	right, rightErr := explorer.ReceiptID(b)
	return leftErr == nil && rightErr == nil && left == right
}

func (s *Store) CompilationReceiptStats(ctx context.Context, project string) (explorer.ReceiptStoreStats, error) {
	var row struct {
		Count             int    `json:"count"`
		ApproxBytes       int64  `json:"approxBytes"`
		OldestCreatedAt   string `json:"oldestCreatedAt"`
		UnreferencedCount int    `json:"unreferencedCount"`
	}
	err := s.client.QueryRows(ctx, `LET referenced = (FOR r IN @@revisions FILTER r.compilationReceiptId != null AND (@project == "" OR r.project == @project) RETURN r.compilationReceiptId) LET receipts = (FOR d IN @@receipts FILTER @project == "" OR d.project == @project RETURN d) RETURN {count: LENGTH(receipts), approxBytes: SUM(FOR d IN receipts RETURN LENGTH(TO_STRING(d))), oldestCreatedAt: MIN(FOR d IN receipts RETURN d.createdAt), unreferencedCount: LENGTH(FOR d IN receipts FILTER d._key NOT IN referenced RETURN 1)}`, 1, map[string]any{"@receipts": CompilationReceiptsCollection, "@revisions": RevisionsCollection, "project": project}, func(value map[string]any) error {
		decoded, err := decode[struct {
			Count             int    `json:"count"`
			ApproxBytes       int64  `json:"approxBytes"`
			OldestCreatedAt   string `json:"oldestCreatedAt"`
			UnreferencedCount int    `json:"unreferencedCount"`
		}](value)
		if err != nil {
			return err
		}
		row.Count, row.ApproxBytes, row.OldestCreatedAt, row.UnreferencedCount = decoded.Count, decoded.ApproxBytes, decoded.OldestCreatedAt, decoded.UnreferencedCount
		return nil
	})
	if err != nil {
		return explorer.ReceiptStoreStats{}, err
	}
	stats := explorer.ReceiptStoreStats{Count: row.Count, ApproxBytes: row.ApproxBytes, UnreferencedCount: row.UnreferencedCount}
	if row.OldestCreatedAt != "" {
		parsed, parseErr := time.Parse(time.RFC3339Nano, row.OldestCreatedAt)
		if parseErr != nil {
			parsed, parseErr = time.Parse(time.RFC3339, row.OldestCreatedAt)
		}
		if parseErr != nil {
			return explorer.ReceiptStoreStats{}, fmt.Errorf("invalid receipt oldestCreatedAt: %w", parseErr)
		}
		stats.OldestCreatedAt = parsed
	}
	return stats, nil
}

func (s *Store) PublishAuthoring(ctx context.Context, receipt explorer.CompilationReceipt, revision explorer.Revision) (*explorer.Revision, error) {
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	receiptDoc, err := document(receipt, receipt.ID)
	if err != nil {
		return nil, err
	}
	revisionDoc, err := document(revision, revision.ID)
	if err != nil {
		return nil, err
	}
	management := explorer.ManagementInteractive
	if revision.ExplorerID == "default" {
		management = explorer.ManagementRepository
	}
	var out *explorer.Revision
	err = s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{ExplorersCollection, RevisionsCollection, CompilationReceiptsCollection}}, func(txCtx context.Context, tx store.Transaction) error {
		if err := tx.QueryRows(txCtx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": CompilationReceiptsCollection, "doc": receiptDoc}, func(map[string]any) error { return nil }); err != nil {
			return err
		}
		var stored *explorer.CompilationReceipt
		if err := tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project AND d.explorerId == @explorerId RETURN d`, 1, map[string]any{"@c": CompilationReceiptsCollection, "key": receipt.ID, "project": receipt.Project, "explorerId": receipt.ExplorerID}, func(row map[string]any) error {
			value, err := decode[explorer.CompilationReceipt](row)
			if err != nil {
				return err
			}
			if err := validateReceipt(value); err != nil {
				return err
			}
			stored = &value
			return nil
		}); err != nil {
			return err
		}
		if stored == nil {
			return explorer.ErrNotFound
		}
		if !sameReceipt(*stored, receipt) {
			return explorer.ErrCorruptReceipt
		}
		if err := tx.QueryRows(txCtx, `UPSERT { _key: @key } INSERT @doc UPDATE {} IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": revision.ID, "doc": revisionDoc}, func(map[string]any) error { return nil }); err != nil {
			return err
		}
		row, found, err := readActivationRow(txCtx, tx, activationReadAQL, map[string]any{"@explorers": ExplorersCollection, "@revisions": RevisionsCollection, "explorerKey": explorerKey(revision.Project, revision.ExplorerID), "revisionKey": revision.ID, "project": revision.Project, "explorerId": revision.ExplorerID, "management": management})
		if err != nil {
			return err
		}
		if !found {
			return explorer.ErrDraftConflict
		}
		state, err := activationStateFromRow(row)
		if err != nil {
			return err
		}
		if err := activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC()); err != nil {
			return err
		}
		return tx.QueryRows(txCtx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RevisionsCollection, "key": revision.ID}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	})
	return out, err
}

func (s *Store) PurgeDraftAuthoring(ctx context.Context) error {
	return s.client.WithTransaction(ctx, store.TransactionCollections{Write: []string{ExplorersCollection, CompilationReceiptsCollection}, Read: []string{RevisionsCollection}}, func(txCtx context.Context, tx store.Transaction) error {
		// UNSET returns the complete cleaned document. Use REPLACE here: UPDATE
		// merges that result back into the original document, which leaves every
		// retired draft field in place and makes the cutover appear to succeed
		// while preserving the legacy state.
		if err := tx.QueryRows(txCtx, `FOR d IN @@explorers REPLACE d WITH UNSET(d, "draftConfig", "draftVersion", "draftDigest", "draftAuthoringBundle", "draftIntentDigest", "draftReceiptId", "draftIdentityMappings", "draftDefinition", "authoringBundle", "intentDigest", "receiptPointer", "identityMappings", "version", "digest") IN @@explorers RETURN {purged: true}`, 100000, map[string]any{"@explorers": ExplorersCollection}, func(map[string]any) error { return nil }); err != nil {
			return err
		}
		return tx.QueryRows(txCtx, `LET referenced = (FOR r IN @@revisions FILTER r.compilationReceiptId != null RETURN DISTINCT r.compilationReceiptId) FOR receipt IN @@receipts FILTER receipt._key NOT IN referenced REMOVE receipt IN @@receipts RETURN {purged: true}`, 100000, map[string]any{"@revisions": RevisionsCollection, "@receipts": CompilationReceiptsCollection}, func(map[string]any) error { return nil })
	})
}

func (s *Store) InsertRevision(ctx context.Context, revision explorer.Revision) (*explorer.Revision, error) {
	if revision.ID == "" {
		return nil, fmt.Errorf("revision ID is required")
	}
	doc, err := document(revision, revision.ID)
	if err != nil {
		return nil, err
	}
	var out *explorer.Revision
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE {} IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": revision.ID, "doc": doc}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func (s *Store) GetRevision(ctx context.Context, id string) (*explorer.Revision, error) {
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RevisionsCollection, "key": id}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

// TransitionRevision updates lifecycle fields only. The AQL never accepts a
// replacement definition/recipe document, preserving revision immutability.
func (s *Store) TransitionRevision(ctx context.Context, id string, status explorer.RevisionStatus, diagnostics []explorer.Diagnostic) (*explorer.Revision, error) {
	now := time.Now().UTC()
	patch := map[string]any{"status": status, "diagnostics": diagnostics}
	if status == explorer.RevisionReady {
		patch["readyAt"] = now
	}
	if status == explorer.RevisionFailed {
		patch["failedAt"] = now
	}
	var out *explorer.Revision
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key UPDATE d WITH @patch IN @@c RETURN NEW`, 1, map[string]any{"@c": RevisionsCollection, "key": id, "patch": patch}, func(row map[string]any) error { value, err := decode[explorer.Revision](row); out = &value; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

const activationReadAQL = `LET owner = DOCUMENT(@@explorers, @explorerKey)
LET candidate = DOCUMENT(@@revisions, @revisionKey)
FILTER owner != null AND candidate != null AND owner.project == @project AND owner.explorerId == @explorerId AND owner.managementMode == @management AND candidate.project == @project AND candidate.explorerId == @explorerId
FILTER candidate.status == "READY" OR (candidate.status == "ACTIVE" AND owner.activeRevisionId == @revisionKey)
LET prior = owner.activeRevisionId == null ? null : DOCUMENT(@@revisions, owner.activeRevisionId)
RETURN {owner: owner, candidate: candidate, prior: prior}`

const compositeActivationReadAQL = `LET manifest = DOCUMENT(@@lifecycle, @manifestKey)
LET active = DOCUMENT(@@lifecycle, @activeKey)
LET owner = DOCUMENT(@@explorers, @explorerKey)
LET candidate = DOCUMENT(@@revisions, @revisionKey)
LET execution = candidate == null OR LENGTH(candidate.materializations) == 0 ? null : DOCUMENT(@@executions, candidate.materializations[0].materializationId)
FILTER manifest != null AND manifest.recordType == "manifest" AND manifest.dataset.project == @project AND manifest.dataset.generation == @generation AND manifest.state IN ["STAGED", "READY"]
FILTER active != null AND active.recordType == "active_generation" AND active.project == @project
FILTER owner != null AND owner.project == @project AND owner.explorerId == "default" AND owner.managementMode == "REPOSITORY"
FILTER candidate != null AND candidate.project == @project AND candidate.explorerId == "default" AND candidate.sourceGeneration == @generation
FILTER candidate.status == "READY" OR (candidate.status == "ACTIVE" AND owner.activeRevisionId == @revisionKey)
FILTER execution != null AND execution.project == @project AND execution.datasetGeneration == @generation AND execution.state == "PUBLISHED"
LET prior = owner.activeRevisionId == null ? null : DOCUMENT(@@revisions, owner.activeRevisionId)
RETURN {manifest: manifest, active: active, owner: owner, candidate: candidate, prior: prior}`

const activationUpdateAQL = `FOR d IN @@c
FILTER d._key == @key
UPDATE d WITH @patch IN @@c
RETURN NEW`

type activationState struct {
	owner     map[string]any
	candidate map[string]any
	prior     map[string]any
}

func (s *Store) ActivateInteractive(ctx context.Context, project, explorerID, revisionID string) error {
	return s.activateOwnerRevision(ctx, project, explorerID, revisionID, explorer.ManagementInteractive, []string{ExplorersCollection, RevisionsCollection})
}

func (s *Store) ActivateRepository(ctx context.Context, project, revisionID string) error {
	return s.activateOwnerRevision(ctx, project, "default", revisionID, explorer.ManagementRepository, []string{ExplorersCollection, RevisionsCollection})
}

func (s *Store) activateOwnerRevision(ctx context.Context, project, explorerID, revisionID string, management explorer.ManagementMode, writeCollections []string) error {
	return s.client.WithTransaction(ctx, store.TransactionCollections{Write: writeCollections}, func(txCtx context.Context, tx store.Transaction) error {
		row, found, err := readActivationRow(txCtx, tx, activationReadAQL, map[string]any{
			"@explorers": ExplorersCollection, "@revisions": RevisionsCollection,
			"explorerKey": explorerKey(project, explorerID), "revisionKey": revisionID,
			"project": project, "explorerId": explorerID, "management": management,
		})
		if err != nil {
			return err
		}
		if !found {
			return explorer.ErrDraftConflict
		}
		state, err := activationStateFromRow(row)
		if err != nil {
			return err
		}
		return activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC())
	})
}

// ActivateRepositoryGeneration is the repository-only composite visibility
// switch. It validates all pointers first, then performs each single-collection
// modification inside one Arango transaction so no partial activation is
// visible if any update fails.
func (s *Store) ActivateRepositoryGeneration(ctx context.Context, project, generation, revisionID string) error {
	return s.client.WithTransaction(ctx, store.TransactionCollections{
		Read:  []string{publicationarango.BundleExecutionsCollection},
		Write: []string{datasetarango.LifecycleCollection, ExplorersCollection, RevisionsCollection},
	}, func(txCtx context.Context, tx store.Transaction) error {
		row, found, err := readActivationRow(txCtx, tx, compositeActivationReadAQL, map[string]any{
			"@lifecycle": datasetarango.LifecycleCollection, "@explorers": ExplorersCollection,
			"@revisions": RevisionsCollection, "@executions": publicationarango.BundleExecutionsCollection,
			"manifestKey": datasetarango.ManifestDocumentKey(dataset.Ref{Project: project, Generation: generation}),
			"activeKey":   datasetarango.ActiveDocumentKey(project), "explorerKey": explorerKey(project, "default"),
			"revisionKey": revisionID, "project": project, "generation": generation,
		})
		if err != nil {
			return err
		}
		if !found {
			return explorer.ErrDraftConflict
		}
		state, err := activationStateFromRow(row)
		if err != nil {
			return err
		}
		manifest, err := requiredActivationDocument(row, "manifest")
		if err != nil {
			return err
		}
		active, err := requiredActivationDocument(row, "active")
		if err != nil {
			return err
		}
		activeKey, err := activationDocumentKey(active)
		if err != nil {
			return err
		}
		manifestKey, err := activationDocumentKey(manifest)
		if err != nil {
			return err
		}
		dataset, ok := manifest["dataset"]
		if !ok {
			return fmt.Errorf("activation manifest has no dataset")
		}
		if err := updateActivationDocument(txCtx, tx, datasetarango.LifecycleCollection, activeKey, map[string]any{"dataset": dataset, "manifestKey": manifestKey}); err != nil {
			return err
		}
		return activateRevisionAndOwner(txCtx, tx, state, time.Now().UTC())
	})
}

func readActivationRow(ctx context.Context, tx store.Transaction, query string, binds map[string]any) (map[string]any, bool, error) {
	var row map[string]any
	err := tx.QueryRows(ctx, query, 1, binds, func(value map[string]any) error {
		if row != nil {
			return fmt.Errorf("activation guard returned more than one row")
		}
		row = value
		return nil
	})
	return row, row != nil, err
}

func activationStateFromRow(row map[string]any) (activationState, error) {
	owner, err := requiredActivationDocument(row, "owner")
	if err != nil {
		return activationState{}, err
	}
	candidate, err := requiredActivationDocument(row, "candidate")
	if err != nil {
		return activationState{}, err
	}
	prior, err := optionalActivationDocument(row, "prior")
	if err != nil {
		return activationState{}, err
	}
	return activationState{owner: owner, candidate: candidate, prior: prior}, nil
}

func requiredActivationDocument(row map[string]any, field string) (map[string]any, error) {
	document, err := optionalActivationDocument(row, field)
	if err != nil {
		return nil, err
	}
	if document == nil {
		return nil, fmt.Errorf("activation guard returned no %s document", field)
	}
	return document, nil
}

func optionalActivationDocument(row map[string]any, field string) (map[string]any, error) {
	value := row[field]
	if value == nil {
		return nil, nil
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("activation guard returned invalid %s document", field)
	}
	return document, nil
}

func activationDocumentKey(document map[string]any) (string, error) {
	key, ok := document["_key"].(string)
	if !ok || key == "" {
		return "", fmt.Errorf("activation document has no _key")
	}
	return key, nil
}

func activateRevisionAndOwner(ctx context.Context, tx store.Transaction, state activationState, now time.Time) error {
	candidateKey, err := activationDocumentKey(state.candidate)
	if err != nil {
		return err
	}
	candidate, err := decode[explorer.Revision](state.candidate)
	if err != nil {
		return fmt.Errorf("decode activation candidate: %w", err)
	}
	if state.prior != nil {
		priorKey, err := activationDocumentKey(state.prior)
		if err != nil {
			return err
		}
		if priorKey != candidateKey {
			if err := updateActivationDocument(ctx, tx, RevisionsCollection, priorKey, map[string]any{"status": explorer.RevisionSuperseded}); err != nil {
				return err
			}
		}
	}
	if err := updateActivationDocument(ctx, tx, RevisionsCollection, candidateKey, map[string]any{"status": explorer.RevisionActive, "activatedAt": now}); err != nil {
		return err
	}
	ownerKey, err := activationDocumentKey(state.owner)
	if err != nil {
		return err
	}
	return updateActivationDocument(ctx, tx, ExplorersCollection, ownerKey, activeExplorerPatch(candidate, candidateKey, now))
}

func activeExplorerPatch(candidate explorer.Revision, revisionKey string, now time.Time) map[string]any {
	publication := candidate.Publication
	materializations, dataset := explorer.WithDataframeSelectors(candidate.Recipe, candidate.Materializations, candidate.Dataset)
	publication.State = string(explorer.RevisionActive)
	publication.RevisionID = revisionKey
	publication.UpdatedAt = now
	return map[string]any{
		"activeRevisionId": revisionKey, "activeConfig": candidate.Config,
		"recipeDigest": candidate.RecipeDigest, "resolvedSchemaDigest": candidate.ResolvedSchemaDigest,
		"sourceGeneration": candidate.SourceGeneration, "dataset": dataset,
		"publication": publication, "emittedColumns": candidate.EmittedColumns,
		"materializations": materializations, "diagnostics": candidate.Diagnostics,
	}
}

func updateActivationDocument(ctx context.Context, tx store.Transaction, collection, key string, patch map[string]any) error {
	var updated bool
	err := tx.QueryRows(ctx, activationUpdateAQL, 1, map[string]any{"@c": collection, "key": key, "patch": patch}, func(map[string]any) error {
		if updated {
			return fmt.Errorf("activation update returned more than one document")
		}
		updated = true
		return nil
	})
	if err != nil {
		return err
	}
	if !updated {
		return explorer.ErrDraftConflict
	}
	return nil
}

var _ explorer.Store = (*Store)(nil)

func decode[T any](value any) (T, error) {
	var out T
	raw, err := json.Marshal(normalizeUpdatedAt(value))
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(raw, &out)
	return out, err
}

// normalizeUpdatedAt keeps reads tolerant of the numeric Arango timestamp
// written by the short-lived last-write-wins regression. New writes use the
// normal JSON time string representation, but existing documents must remain
// readable so they can be repaired by a subsequent draft save.
func normalizeUpdatedAt(value any) any {
	row, ok := value.(map[string]any)
	if !ok {
		return value
	}
	normalized := make(map[string]any, len(row))
	for key, item := range row {
		normalized[key] = item
	}
	if timestamp, ok := numericTimestamp(row["updatedAt"]); ok {
		normalized["updatedAt"] = timestamp
	}
	return normalized
}

func numericTimestamp(value any) (string, bool) {
	var millis int64
	switch number := value.(type) {
	case float64:
		millis = int64(number)
	case float32:
		millis = int64(number)
	case int:
		millis = int64(number)
	case int64:
		millis = number
	case uint64:
		millis = int64(number)
	default:
		return "", false
	}
	return time.UnixMilli(millis).UTC().Format(time.RFC3339Nano), true
}
func document(value any, k string) (map[string]any, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	doc["_key"] = k
	return doc, nil
}
