package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	publication "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	sourceLoadClaimMillis = int64(2 * time.Minute / time.Millisecond)
	sourceLoadCopyMillis  = int64(2 * time.Minute / time.Millisecond)
)

type sourceLoadRecord struct {
	Key                  string                       `json:"_key"`
	Project              string                       `json:"project"`
	OperationID          string                       `json:"operation_id"`
	Plan                 SourceLoadPlan               `json:"plan"`
	PlanHash             string                       `json:"plan_hash"`
	Generation           string                       `json:"generation"`
	State                string                       `json:"state"`
	BaseRevision         int64                        `json:"base_revision"`
	SourceGeneration     string                       `json:"source_generation,omitempty"`
	LegacySourceRevision int64                        `json:"legacy_source_revision"`
	LegacySourceTracked  bool                         `json:"legacy_source_tracked"`
	LegacyRepository     string                       `json:"legacy_repository,omitempty"`
	SourceMode           string                       `json:"source_mode"`
	CopyComplete         bool                         `json:"copy_complete"`
	Receipts             map[string]SourceFileReceipt `json:"receipts"`
	FileDigests          map[string]string            `json:"file_digests,omitempty"`
	FileClaims           map[string]sourceLoadClaim   `json:"file_claims,omitempty"`
	CopyOwner            string                       `json:"copy_owner,omitempty"`
	CopyExpiresAt        int64                        `json:"copy_expires_at,omitempty"`
	PublishOwner         string                       `json:"publish_owner,omitempty"`
	PublishExpiresAt     int64                        `json:"publish_expires_at,omitempty"`
	ManifestHash         string                       `json:"manifest_hash,omitempty"`
	ExecutionID          string                       `json:"execution_id,omitempty"`
	SourceRevision       int64                        `json:"source_revision,omitempty"`
	CreatedAt            time.Time                    `json:"created_at"`
	UpdatedAt            time.Time                    `json:"updated_at"`
}

type sourceLoadClaim struct {
	Owner     string `json:"owner"`
	ExpiresAt int64  `json:"expires_at"`
}

type sourceLoadBase struct {
	Revision             int64
	Generation           string
	Mode                 string
	LegacySourceRevision int64
	LegacySourceTracked  bool
	LegacyRepository     string
	LegacyFileScopes     map[string]string
}

type legacySourceSnapshot struct {
	Exists         bool
	SourceTracked  bool
	SourceRevision int64
	Repository     string
	Pending        string
	PendingPhase   string
	LeaseOwner     string
	LeaseExpiresAt int64
	FileScopes     map[string]string
}

func sourceLoadDocumentKey(project, operationID string) string {
	digest := sha256.Sum256([]byte(project + "\x00" + operationID))
	return hex.EncodeToString(digest[:])
}

func sourceLoadGeneration(project, operationID string) string {
	digest := sha256.Sum256([]byte("loom-source-load-generation-v1\x00" + project + "\x00" + operationID))
	return "source-" + hex.EncodeToString(digest[:])
}

func sourceLoadPlanHash(plan SourceLoadPlan) (string, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode source-load plan identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (p *RepositorySourceLoader) Begin(ctx context.Context, project, operationID string, plan SourceLoadPlan) (SourceLoadStatus, error) {
	project, operationID = strings.TrimSpace(project), strings.TrimSpace(operationID)
	if _, err := publication.NewRef(project, "source-load-project-validation"); err != nil {
		return SourceLoadStatus{}, err
	}
	if operationID == "" {
		return SourceLoadStatus{}, fmt.Errorf("source-load operation ID is required")
	}
	if p.validateProject != nil {
		if err := p.validateProject(ctx, project); err != nil {
			return SourceLoadStatus{}, err
		}
	}
	canonical, manifest, _, err := canonicalizeSourceLoadPlan(plan)
	if err != nil {
		return SourceLoadStatus{}, &SourcePublishConflictError{Reason: "source-load plan is invalid"}
	}
	planHash, err := sourceLoadPlanHash(canonical)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	schemaIdentity, err := loadSchemaSnapshot(p.schemaPath)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	if err := p.client.Bootstrap(ctx, bootstrapSpecWithReporter(schemaIdentity.GeneratedResourceTypes, false, nil)); err != nil {
		return SourceLoadStatus{}, fmt.Errorf("bootstrap source-load graph collections: %w", err)
	}
	if err := p.client.Bootstrap(ctx, datasetarango.BootstrapSpec()); err != nil {
		return SourceLoadStatus{}, fmt.Errorf("bootstrap dataset lifecycle: %w", err)
	}
	if err := p.client.Bootstrap(ctx, sourceLoadBootstrapSpec()); err != nil {
		return SourceLoadStatus{}, fmt.Errorf("bootstrap source-load sessions: %w", err)
	}
	key := sourceLoadDocumentKey(project, operationID)
	if existing, found, err := p.readSourceLoad(ctx, key); err != nil {
		return SourceLoadStatus{}, err
	} else if found {
		if existing.Project != project || existing.OperationID != operationID || existing.PlanHash != planHash {
			return SourceLoadStatus{}, &SourcePublishConflictError{Reason: "operation ID is already bound to another source-load plan"}
		}
		if err := p.ensureSourceLoadCandidate(ctx, &existing, manifest); err != nil {
			return SourceLoadStatus{}, err
		}
		return sourceLoadStatus(existing), nil
	}

	base, err := p.readSourceLoadBase(ctx, project)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	if base.LegacySourceTracked && base.LegacyRepository != canonical.Repository {
		return SourceLoadStatus{}, &SourcePublishConflictError{Reason: "project source is already bound to another repository"}
	}
	selectedTypes := make([]string, 0, len(canonical.Files))
	for _, file := range canonical.Files {
		resourceType, _ := canonicalSourceResourceType(file.Path)
		selectedTypes = append(selectedTypes, resourceType)
	}
	if err := p.validateSourceLoadSelectedScopes(ctx, project, base, selectedTypes, canonical.AuthResourcePath); err != nil {
		return SourceLoadStatus{}, err
	}
	if err := p.requireMutableSourceCopySafe(ctx, project, base, selectedTypes); err != nil {
		return SourceLoadStatus{}, err
	}
	if canonical.ExpectedRevision != nil && *canonical.ExpectedRevision != base.Revision {
		return SourceLoadStatus{}, &SourcePublishConflictError{Reason: "expected release revision does not match the current project revision"}
	}
	ref, err := publication.NewRef(project, sourceLoadGeneration(project, operationID))
	if err != nil {
		return SourceLoadStatus{}, err
	}
	record := sourceLoadRecord{
		Key: key, Project: project, OperationID: operationID, Plan: canonical, PlanHash: planHash,
		Generation: ref.Generation, State: SourceLoadUploading, BaseRevision: base.Revision,
		SourceGeneration: base.Generation, LegacySourceRevision: base.LegacySourceRevision,
		LegacySourceTracked: base.LegacySourceTracked, LegacyRepository: base.LegacyRepository,
		SourceMode: base.Mode, Receipts: map[string]SourceFileReceipt{},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return SourceLoadStatus{}, err
	}
	created := false
	err = p.client.QueryRows(ctx, `LET existing = DOCUMENT(@@loads, @key)
FILTER existing == null
INSERT @record INTO @@loads
RETURN {created: true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "record": json.RawMessage(encoded)}, func(row map[string]any) error {
		created, _ = row["created"].(bool)
		return nil
	})
	if err != nil {
		return SourceLoadStatus{}, fmt.Errorf("persist source-load plan: %w", err)
	}
	if !created {
		existing, found, err := p.readSourceLoad(ctx, key)
		if err != nil {
			return SourceLoadStatus{}, err
		}
		if !found || existing.PlanHash != planHash {
			return SourceLoadStatus{}, &SourcePublishConflictError{Reason: "operation ID is already bound to another source-load plan"}
		}
		record = existing
	}
	if err := p.ensureSourceLoadCandidate(ctx, &record, manifest); err != nil {
		return SourceLoadStatus{}, err
	}
	return sourceLoadStatus(record), nil
}

func sourceLoadBootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{
		Name: sourceLoadCollection, Indexes: [][]string{{"project", "operation_id"}, {"project", "state"}},
	}}}
}

func (p *RepositorySourceLoader) readSourceLoadBase(ctx context.Context, project string) (sourceLoadBase, error) {
	legacy, err := p.readLegacySourceSnapshot(ctx, project)
	if err != nil {
		return sourceLoadBase{}, err
	}
	activeRelease, releaseErr := p.datasets.ReadActiveRelease(ctx, project)
	if releaseErr == nil {
		ref, err := publication.NewRef(project, activeRelease.Release.Generation)
		if err != nil {
			return sourceLoadBase{}, fmt.Errorf("active release has an invalid generation: %w", err)
		}
		manifest, err := p.datasets.ReadManifest(ctx, ref)
		if err != nil {
			return sourceLoadBase{}, fmt.Errorf("read active release generation manifest: %w", err)
		}
		if !manifest.IsStaged() {
			return sourceLoadBase{}, fmt.Errorf("active release generation %q is not staged", ref.Generation)
		}
		return sourceLoadBase{Revision: activeRelease.Revision, Generation: ref.Generation, Mode: "generation", LegacySourceRevision: legacy.SourceRevision, LegacySourceTracked: legacy.SourceTracked, LegacyRepository: legacy.Repository, LegacyFileScopes: legacy.FileScopes}, nil
	}
	if !errors.Is(releaseErr, publication.ErrNoActiveRelease) {
		return sourceLoadBase{}, fmt.Errorf("read active release: %w", releaseErr)
	}
	manifest, manifestErr := p.datasets.ResolveActiveManifest(ctx, project)
	if manifestErr == nil {
		return sourceLoadBase{Revision: 0, Generation: manifest.Dataset.Generation, Mode: "generation", LegacySourceRevision: legacy.SourceRevision, LegacySourceTracked: legacy.SourceTracked, LegacyRepository: legacy.Repository, LegacyFileScopes: legacy.FileScopes}, nil
	}
	if !errors.Is(manifestErr, publication.ErrNoActiveGeneration) {
		return sourceLoadBase{}, fmt.Errorf("resolve active dataset generation: %w", manifestErr)
	}
	if legacy.SourceTracked && (legacy.Pending != "" || legacy.PendingPhase != "" || legacy.LeaseOwner != "" || legacy.LeaseExpiresAt >= time.Now().UnixMilli()) {
		return sourceLoadBase{}, &SourcePublishConflictError{Reason: "a legacy source publication is still pending"}
	}
	return sourceLoadBase{Revision: 0, Mode: "mutable", LegacySourceRevision: legacy.SourceRevision, LegacySourceTracked: legacy.SourceTracked, LegacyRepository: legacy.Repository, LegacyFileScopes: legacy.FileScopes}, nil
}

func (p *RepositorySourceLoader) readLegacySourceSnapshot(ctx context.Context, project string) (legacySourceSnapshot, error) {
	exists, err := p.client.CollectionExists(ctx, "loom_source_file_states")
	if err != nil {
		return legacySourceSnapshot{}, fmt.Errorf("inspect legacy source state collection: %w", err)
	}
	if !exists {
		return legacySourceSnapshot{}, nil
	}
	var snapshot legacySourceSnapshot
	err = p.client.QueryRows(ctx, `LET state = DOCUMENT(@@states, @key)
FILTER state != null AND state.project == @project
RETURN {
  source_tracked: NOT_NULL(state.sourceTracked, false),
  source_revision: NOT_NULL(state.sourceRevision, 0),
  source_repository: NOT_NULL(state.sourceRepository, ""),
  pending_operation_id: NOT_NULL(state.pendingOperationId, ""),
  pending_phase: NOT_NULL(state.pendingPhase, ""),
  lease_owner_id: NOT_NULL(state.leaseOwnerId, ""),
  lease_expires_at: NOT_NULL(state.leaseExpiresAt, 0),
  file_scopes: (
    FOR file IN VALUES(NOT_NULL(state.files, {}))
      RETURN {resource_type: NOT_NULL(file.resourceType, ""), auth_resource_path: NOT_NULL(file.authResourcePath, "")}
  )
}`, 1, map[string]any{
		"@states": "loom_source_file_states", "key": legacySourceStateKey(project), "project": project,
	}, func(row map[string]any) error {
		snapshot.Exists = true
		snapshot.SourceTracked, _ = row["source_tracked"].(bool)
		snapshot.SourceRevision = sourceLoadNumber(row["source_revision"])
		snapshot.Repository, _ = row["source_repository"].(string)
		snapshot.Pending, _ = row["pending_operation_id"].(string)
		snapshot.PendingPhase, _ = row["pending_phase"].(string)
		snapshot.LeaseOwner, _ = row["lease_owner_id"].(string)
		snapshot.LeaseExpiresAt = sourceLoadNumber(row["lease_expires_at"])
		var scopes []struct {
			ResourceType     string `json:"resource_type"`
			AuthResourcePath string `json:"auth_resource_path"`
		}
		encoded, err := json.Marshal(row["file_scopes"])
		if err != nil {
			return err
		}
		if err := json.Unmarshal(encoded, &scopes); err != nil {
			return err
		}
		snapshot.FileScopes = make(map[string]string, len(scopes))
		for _, scope := range scopes {
			if scope.ResourceType != "" {
				snapshot.FileScopes[scope.ResourceType] = scope.AuthResourcePath
			}
		}
		return nil
	})
	if err != nil {
		return legacySourceSnapshot{}, fmt.Errorf("read legacy source revision: %w", err)
	}
	return snapshot, nil
}

func sourceLoadNumber(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	default:
		return 0
	}
}

func legacySourceStateKey(project string) string {
	digest := sha256.Sum256([]byte(project))
	return hex.EncodeToString(digest[:])
}

func (p *RepositorySourceLoader) readSourceLoad(ctx context.Context, key string) (sourceLoadRecord, bool, error) {
	var record *sourceLoadRecord
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
FILTER record != null
RETURN record`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key}, func(row map[string]any) error {
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var decoded sourceLoadRecord
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			return err
		}
		record = &decoded
		return nil
	})
	if err != nil {
		return sourceLoadRecord{}, false, err
	}
	if record == nil {
		return sourceLoadRecord{}, false, nil
	}
	if record.Key != key || record.Project == "" || record.OperationID == "" || record.Generation == "" || record.PlanHash == "" {
		return sourceLoadRecord{}, false, fmt.Errorf("stored source-load identity is malformed")
	}
	if record.Receipts == nil {
		record.Receipts = map[string]SourceFileReceipt{}
	}
	if record.FileClaims == nil {
		record.FileClaims = map[string]sourceLoadClaim{}
	}
	if record.FileDigests == nil {
		record.FileDigests = map[string]string{}
	}
	return *record, true, nil
}

func (p *RepositorySourceLoader) Status(ctx context.Context, project, operationID string) (SourceLoadStatus, error) {
	record, found, err := p.readSourceLoad(ctx, sourceLoadDocumentKey(strings.TrimSpace(project), strings.TrimSpace(operationID)))
	if err != nil {
		return SourceLoadStatus{}, err
	}
	if !found || record.Project != project || record.OperationID != operationID {
		return SourceLoadStatus{}, ErrSourceLoadNotFound
	}
	return sourceLoadStatus(record), nil
}

func (p *RepositorySourceLoader) ensureSourceLoadCandidate(ctx context.Context, record *sourceLoadRecord, manifest SourceManifest) error {
	if record.CopyComplete {
		return nil
	}
	owner, err := newSourceLeaseOwner()
	if err != nil {
		return err
	}
	claimed, err := p.claimCandidateCopy(ctx, record.Key, owner)
	if err != nil {
		return err
	}
	if !claimed {
		latest, found, err := p.readSourceLoad(ctx, record.Key)
		if err != nil {
			return err
		}
		if found && latest.CopyComplete {
			*record = latest
			return nil
		}
		return ErrSourceLoadInProgress
	}
	defer p.releaseCandidateCopy(record.Key, owner)
	workCtx, stopLease := p.renewCandidateCopyLease(ctx, record.Key, owner)
	defer stopLease()

	schemaIdentity, err := loadSchemaSnapshot(p.schemaPath)
	if err != nil {
		return err
	}
	ref, err := publication.NewRef(record.Project, record.Generation)
	if err != nil {
		return err
	}
	candidateManifest, err := publication.NewManifest(ref, schemaIdentity)
	if err != nil {
		return err
	}
	created, err := p.datasets.CreateManifest(workCtx, candidateManifest)
	if err != nil {
		if !errors.Is(err, datasetarango.ErrManifestAlreadyExists) {
			return fmt.Errorf("create source-load candidate manifest: %w", err)
		}
		created, err = p.datasets.ReadManifest(workCtx, ref)
		if err != nil {
			return err
		}
		if created.SchemaIdentity.SchemaSHA256 != schemaIdentity.SchemaSHA256 || created.State != publication.StateLoading {
			return &SourcePublishConflictError{Reason: "source-load candidate generation already exists with incompatible lifecycle state"}
		}
	}
	if err := p.copyGenerationForward(workCtx, *record); err != nil {
		return err
	}
	if err := p.prepareCandidateSemanticInventory(workCtx, *record); err != nil {
		return err
	}
	if err := context.Cause(workCtx); err != nil {
		return err
	}
	if err := p.markCandidateCopyComplete(workCtx, record.Key, owner); err != nil {
		return err
	}
	latest, found, err := p.readSourceLoad(ctx, record.Key)
	if err != nil {
		return err
	}
	if !found || !latest.CopyComplete {
		return fmt.Errorf("source-load candidate initialization did not persist completion")
	}
	*record = latest
	return nil
}

func (p *RepositorySourceLoader) claimCandidateCopy(ctx context.Context, key, owner string) (bool, error) {
	claimed := false
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
FILTER record != null AND record.state == @uploading AND record.copy_complete != true
FILTER TO_NUMBER(NOT_NULL(record.copy_expires_at, 0)) < DATE_NOW()
UPDATE record WITH {copy_owner: @owner, copy_expires_at: DATE_NOW() + @ttl, updated_at: @now} IN @@loads
RETURN {claimed: true}`, 1, map[string]any{
		"@loads": sourceLoadCollection, "key": key, "owner": owner, "ttl": sourceLoadCopyMillis,
		"uploading": SourceLoadUploading, "now": time.Now().UTC(),
	}, func(row map[string]any) error { claimed, _ = row["claimed"].(bool); return nil })
	return claimed, err
}

func (p *RepositorySourceLoader) markCandidateCopyComplete(ctx context.Context, key, owner string) error {
	updated := false
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
FILTER record != null AND record.copy_owner == @owner AND record.copy_expires_at >= DATE_NOW()
UPDATE record WITH {copy_complete: true, copy_owner: null, copy_expires_at: 0, updated_at: @now} IN @@loads
RETURN {updated: true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "owner": owner, "now": time.Now().UTC()}, func(row map[string]any) error {
		updated, _ = row["updated"].(bool)
		return nil
	})
	if err != nil {
		return err
	}
	if !updated {
		return &SourcePublishConflictError{Reason: "source-load candidate initialization lease was lost"}
	}
	return nil
}

func (p *RepositorySourceLoader) releaseCandidateCopy(key, owner string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
FILTER record != null AND record.copy_owner == @owner AND record.copy_complete != true
UPDATE record WITH {copy_owner: null, copy_expires_at: 0} IN @@loads
RETURN {released: true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "owner": owner}, func(map[string]any) error { return nil })
}

func (p *RepositorySourceLoader) renewCandidateCopyLease(parent context.Context, key, owner string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				updated, err := p.renewCandidateCopyLeaseOnce(ctx, key, owner)
				if err != nil {
					cancel(fmt.Errorf("renew candidate initialization lease: %w", err))
					return
				}
				if !updated {
					cancel(&SourcePublishConflictError{Reason: "candidate initialization lease was lost"})
					return
				}
			}
		}
	}()
	stop := func() {
		cancel(context.Canceled)
		<-done
	}
	return ctx, stop
}

func (p *RepositorySourceLoader) renewCandidateCopyLeaseOnce(ctx context.Context, key, owner string) (bool, error) {
	updated := false
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
FILTER record != null AND record.copy_owner == @owner AND record.copy_expires_at >= DATE_NOW() AND record.copy_complete != true
UPDATE record WITH {copy_expires_at: DATE_NOW() + @ttl} IN @@loads
RETURN {updated: true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "owner": owner, "ttl": sourceLoadCopyMillis}, func(row map[string]any) error {
		updated, _ = row["updated"].(bool)
		return nil
	})
	return updated, err
}
