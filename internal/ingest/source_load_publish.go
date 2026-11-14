package ingest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	dataset "github.com/calypr/loom/internal/dataset"
)

func (p *RepositorySourceLoader) Publish(ctx context.Context, project, operationID string) (SourcePublishReceipt, error) {
	project, operationID = strings.TrimSpace(project), strings.TrimSpace(operationID)
	record, found, err := p.readSourceLoad(ctx, sourceLoadDocumentKey(project, operationID))
	if err != nil {
		return SourcePublishReceipt{}, err
	}
	if !found || record.Project != project || record.OperationID != operationID {
		return SourcePublishReceipt{}, ErrSourceLoadNotFound
	}
	if record.State == SourceLoadPublished {
		return sourceLoadPublicationReceipt(record, true), nil
	}
	if record.State == SourceLoadSuperseded {
		return SourcePublishReceipt{}, &SourcePublishConflictError{Reason: "a newer project release superseded this source load"}
	}
	if !record.CopyComplete || len(record.Receipts) != len(record.Plan.Files) {
		return SourcePublishReceipt{}, ErrSourceLoadIncomplete
	}
	manifest := SourceManifest{Repository: record.Plan.Repository, Commit: record.Plan.Commit}
	for _, planned := range record.Plan.Files {
		accepted, ok := record.Receipts[planned.Path]
		if !ok || accepted.State != "READY" || accepted.Path != planned.Path {
			return SourcePublishReceipt{}, ErrSourceLoadIncomplete
		}
		manifest.Files = append(manifest.Files, SourceFile{Path: planned.Path, SHA256: accepted.SHA256})
	}
	manifestHash, err := SourceManifestHash(manifest)
	if err != nil {
		return SourcePublishReceipt{}, err
	}
	owner, err := newSourceLeaseOwner()
	if err != nil {
		return SourcePublishReceipt{}, err
	}
	claimed, err := p.claimSourceLoadPublication(ctx, record.Key, owner, manifestHash)
	if err != nil {
		return SourcePublishReceipt{}, err
	}
	if !claimed {
		latest, found, readErr := p.readSourceLoad(ctx, record.Key)
		if readErr != nil {
			return SourcePublishReceipt{}, readErr
		}
		if found && latest.State == SourceLoadPublished {
			return sourceLoadPublicationReceipt(latest, true), nil
		}
		return SourcePublishReceipt{}, ErrSourceLoadInProgress
	}
	defer p.releaseSourceLoadPublication(record.Key, owner)
	workCtx, stopLease := p.renewSourceLoadPublication(ctx, record.Key, owner)
	defer stopLease()
	ref, err := dataset.NewRef(project, record.Generation)
	if err != nil {
		return SourcePublishReceipt{}, err
	}
	candidate, err := p.datasets.ReadManifest(workCtx, ref)
	if err != nil {
		return SourcePublishReceipt{}, err
	}
	if candidate.State == dataset.StateLoading {
		totalRows, err := p.finalizeSourceLoadCandidate(workCtx, record)
		if err != nil {
			return SourcePublishReceipt{}, err
		}
		inventory, err := catalogarango.New(p.client)
		if err != nil {
			return SourcePublishReceipt{}, err
		}
		build := catalog.NewSemanticInventoryBuild(project, record.Generation, record.Plan.AuthResourcePath)
		if err := inventory.CompleteSemanticInventoryBuild(workCtx, build, totalRows, ""); err != nil {
			return SourcePublishReceipt{}, fmt.Errorf("complete candidate semantic inventory: %w", err)
		}
		if _, err := p.datasets.TransitionManifest(workCtx, candidate, dataset.StateStaged); err != nil {
			return SourcePublishReceipt{}, err
		}
	} else if !candidate.IsStaged() {
		return SourcePublishReceipt{}, fmt.Errorf("candidate generation is not loadable or staged")
	}
	if err := p.prepareLegacySourceLoadActivation(workCtx, record); err != nil {
		return SourcePublishReceipt{}, err
	}
	executionID, revision, err := p.materialize(workCtx, project, record.Generation, operationID, record.Plan.Commit, record.BaseRevision)
	if err != nil {
		if errors.Is(err, dataset.ErrReleaseActivationConflict) {
			_ = p.supersedeSourceLoad(workCtx, record.Key, owner)
			return SourcePublishReceipt{}, &SourcePublishConflictError{Reason: "project release changed while this source load was publishing"}
		}
		return SourcePublishReceipt{}, err
	}
	if executionID == "" || revision <= record.BaseRevision {
		return SourcePublishReceipt{}, fmt.Errorf("source load publication returned an invalid release receipt")
	}
	record.ExecutionID, record.SourceRevision, record.ManifestHash, record.State = executionID, revision, manifestHash, SourceLoadPublished
	if err := p.completeSourceLoadPublication(workCtx, record, owner); err != nil {
		// An unknown acknowledgement must never undo the release. A retry runs the
		// same generation/operation and reconciles through release activation CAS.
		latest, found, readErr := p.readSourceLoad(ctx, record.Key)
		if readErr == nil && found && latest.State == SourceLoadPublished {
			return sourceLoadPublicationReceipt(latest, true), nil
		}
		return SourcePublishReceipt{}, err
	}
	return sourceLoadPublicationReceipt(record, false), nil
}

func sourceLoadPublicationReceipt(record sourceLoadRecord, reused bool) SourcePublishReceipt {
	return SourcePublishReceipt{OperationID: record.OperationID, SourceRevision: record.SourceRevision, ManifestHash: record.ManifestHash, ExecutionID: record.ExecutionID, State: "PUBLISHED", Reused: reused}
}

func (p *RepositorySourceLoader) claimSourceLoadPublication(ctx context.Context, key, owner, manifestHash string) (bool, error) {
	claimed := false
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
FILTER record != null AND record.state IN [@ready,@pending]
FILTER record.copy_complete == true AND LENGTH(ATTRIBUTES(record.receipts)) == LENGTH(record.plan.files)
FILTER NOT_NULL(record.publish_expires_at,0) < DATE_NOW()
UPDATE record WITH {state:@pending,publish_owner:@owner,publish_expires_at:DATE_NOW()+@ttl,manifest_hash:@hash,updated_at:@now} IN @@loads
RETURN {claimed:true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "owner": owner, "hash": manifestHash, "ttl": sourceLoadClaimMillis, "ready": SourceLoadReady, "pending": SourceLoadPublishPending, "now": time.Now().UTC()}, func(row map[string]any) error { claimed, _ = row["claimed"].(bool); return nil })
	return claimed, err
}

func (p *RepositorySourceLoader) renewSourceLoadPublication(parent context.Context, key, owner string) (context.Context, func()) {
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
				updated := false
				err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads,@key)
FILTER record != null AND record.state == @pending AND record.publish_owner == @owner AND record.publish_expires_at >= DATE_NOW()
UPDATE record WITH {publish_expires_at:DATE_NOW()+@ttl} IN @@loads RETURN {updated:true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "owner": owner, "ttl": sourceLoadClaimMillis, "pending": SourceLoadPublishPending}, func(row map[string]any) error { updated, _ = row["updated"].(bool); return nil })
				if err != nil {
					cancel(err)
					return
				}
				if !updated {
					cancel(&SourcePublishConflictError{Reason: "source load publication lease was lost"})
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(nil); <-done }
}

func (p *RepositorySourceLoader) completeSourceLoadPublication(ctx context.Context, record sourceLoadRecord, owner string) error {
	updated := false
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads,@key)
FILTER record != null AND record.state == @pending AND record.publish_owner == @owner AND record.publish_expires_at >= DATE_NOW()
UPDATE record WITH {state:@published,execution_id:@execution,source_revision:@revision,manifest_hash:@hash,publish_owner:null,publish_expires_at:0,updated_at:@now} IN @@loads
RETURN {updated:true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": record.Key, "owner": owner, "pending": SourceLoadPublishPending, "published": SourceLoadPublished, "execution": record.ExecutionID, "revision": record.SourceRevision, "hash": record.ManifestHash, "now": time.Now().UTC()}, func(row map[string]any) error { updated, _ = row["updated"].(bool); return nil })
	if err != nil {
		return err
	}
	if !updated {
		return &SourcePublishConflictError{Reason: "source load publication receipt lost its lease"}
	}
	return nil
}

func (p *RepositorySourceLoader) releaseSourceLoadPublication(key, owner string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads,@key) FILTER record != null AND record.publish_owner == @owner
UPDATE record WITH {publish_owner:null,publish_expires_at:0} IN @@loads RETURN {released:true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "owner": owner}, func(map[string]any) error { return nil })
}

func (p *RepositorySourceLoader) supersedeSourceLoad(ctx context.Context, key, owner string) error {
	return p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads,@key) FILTER record != null AND record.publish_owner == @owner
UPDATE record WITH {state:@superseded,publish_owner:null,publish_expires_at:0} IN @@loads RETURN {updated:true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "owner": owner, "superseded": SourceLoadSuperseded}, func(map[string]any) error { return nil })
}
