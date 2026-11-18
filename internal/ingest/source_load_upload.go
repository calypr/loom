package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func (p *RepositorySourceLoader) UploadFile(ctx context.Context, project, operationID, resourceType, localPath string) (SourceFileReceipt, error) {
	project, operationID = strings.TrimSpace(project), strings.TrimSpace(operationID)
	record, found, err := p.readSourceLoad(ctx, sourceLoadDocumentKey(project, operationID))
	if err != nil {
		return SourceFileReceipt{}, err
	}
	if !found || record.Project != project || record.OperationID != operationID {
		return SourceFileReceipt{}, ErrSourceLoadNotFound
	}
	resourceType = strings.TrimSpace(resourceType)
	planned, ok := sourceLoadFileForType(record.Plan.Files, resourceType)
	if !ok {
		return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "resource type is not selected by this source-load plan"}
	}
	if record.State != SourceLoadUploading || !record.CopyComplete {
		if receipt, completed := record.Receipts[planned.Path]; completed {
			digest, err := hashSourceLoadFile(localPath)
			if err != nil {
				return SourceFileReceipt{}, err
			}
			if digest != receipt.SHA256 {
				return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "completed file identity was retried with different bytes"}
			}
			return receipt, nil
		}
		return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "source load no longer accepts file uploads"}
	}
	if receipt, completed := record.Receipts[planned.Path]; completed {
		digest, err := hashSourceLoadFile(localPath)
		if err != nil {
			return SourceFileReceipt{}, err
		}
		if digest != receipt.SHA256 {
			return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "completed file identity was retried with different bytes"}
		}
		return receipt, nil
	}
	digest, rows, validation, err := validateSourceLoadFile(ctx, localPath, planned.Path, project, record.Plan.AuthResourcePath, record.Generation, p.schemaPath)
	if err != nil {
		return SourceFileReceipt{}, fmt.Errorf("validate source file %q: %w", planned.Path, err)
	}
	if validation != nil {
		return SourceFileReceipt{}, validation
	}
	if planned.SHA256 != "" && planned.SHA256 != digest {
		return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "uploaded file digest does not match the immutable source plan"}
	}
	owner, err := newSourceLeaseOwner()
	if err != nil {
		return SourceFileReceipt{}, err
	}
	claimed, err := p.claimSourceLoadFile(ctx, record.Key, planned.Path, owner, digest)
	if err != nil {
		return SourceFileReceipt{}, err
	}
	if !claimed {
		latest, found, err := p.readSourceLoad(ctx, record.Key)
		if err != nil {
			return SourceFileReceipt{}, err
		}
		if found {
			if pinned := latest.FileDigests[planned.Path]; pinned != "" && pinned != digest {
				return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "an incomplete file upload was retried with different bytes"}
			}
			if receipt, completed := latest.Receipts[planned.Path]; completed {
				if receipt.SHA256 != digest {
					return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "completed file identity was retried with different bytes"}
				}
				return receipt, nil
			}
		}
		return SourceFileReceipt{}, ErrSourceLoadInProgress
	}
	defer p.releaseSourceLoadFile(record.Key, planned.Path, owner)
	workCtx, stopLease := p.renewSourceLoadFileLease(ctx, record.Key, planned.Path, owner)
	defer stopLease()

	if err := p.clearCandidateSourceFile(workCtx, record, planned.Path, owner, resourceType); err != nil {
		return SourceFileReceipt{}, err
	}
	schema, err := graph.Load(p.schemaPath)
	if err != nil {
		return SourceFileReceipt{}, err
	}
	stat, err := os.Stat(localPath)
	if err != nil || !stat.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("source file is not a regular file")
		}
		return SourceFileReceipt{}, fmt.Errorf("inspect source file %q: %w", planned.Path, err)
	}
	opts := normalizeLoadOptions(LoadOptions{
		Schema: p.schemaPath, MetaDir: filepath.Dir(localPath), Project: project,
		AuthResourcePath: record.Plan.AuthResourcePath, BatchSize: p.batchSize,
		WriterCount: 1, WorkerCount: 1, FailFast: true, SourceFileOwner: resourceType,
		WriteAPI: p.writeAPI,
	})
	loaded, loadErr := loadFile(workCtx, opts, p.client, schema, localPath, record.Generation, false, time.Now(), 0, 0, insertRawDocuments)
	if loadErr != nil {
		cleanupErr := p.clearCandidateSourceFile(context.WithoutCancel(ctx), record, planned.Path, owner, resourceType)
		return SourceFileReceipt{}, errors.Join(fmt.Errorf("load source file %q into inactive generation: %w", planned.Path, loadErr), cleanupErr)
	}
	if loaded.ValidationErrors != 0 || loaded.GenerationErrors != 0 || loaded.EdgeErrors != 0 {
		cleanupErr := p.clearCandidateSourceFile(context.WithoutCancel(ctx), record, planned.Path, owner, resourceType)
		return SourceFileReceipt{}, errors.Join(fmt.Errorf("source file changed or failed during candidate import"), cleanupErr)
	}
	if loaded.Catalog != nil {
		catalogStore, err := catalogarango.New(p.client)
		if err != nil {
			return SourceFileReceipt{}, err
		}
		if err := catalogStore.WriteFieldCatalog(workCtx, catalog.FieldCatalogCollection, loaded.Catalog.Documents(), p.batchSize, false, p.writeAPI, nil); err != nil {
			cleanupErr := p.clearCandidateSourceFile(context.WithoutCancel(ctx), record, planned.Path, owner, resourceType)
			return SourceFileReceipt{}, errors.Join(fmt.Errorf("write candidate field catalog for %s: %w", resourceType, err), cleanupErr)
		}
	}
	postDigest, err := hashSourceLoadFile(localPath)
	if err != nil || postDigest != digest {
		cleanupErr := p.clearCandidateSourceFile(context.WithoutCancel(ctx), record, planned.Path, owner, resourceType)
		if err == nil {
			err = &SourcePublishConflictError{Reason: "source file changed while it was being imported"}
		}
		return SourceFileReceipt{}, errors.Join(err, cleanupErr)
	}
	if err := context.Cause(workCtx); err != nil {
		cleanupErr := p.clearCandidateSourceFile(context.WithoutCancel(ctx), record, planned.Path, owner, resourceType)
		return SourceFileReceipt{}, errors.Join(err, cleanupErr)
	}
	receipt := SourceFileReceipt{Path: planned.Path, ResourceType: resourceType, SHA256: digest, Rows: rows, State: "READY"}
	if err := p.completeSourceLoadFile(ctx, record.Key, planned.Path, owner, receipt); err != nil {
		latest, found, readErr := p.readSourceLoad(context.WithoutCancel(ctx), record.Key)
		if readErr != nil {
			return SourceFileReceipt{}, errors.Join(err, readErr)
		}
		if found {
			if accepted, ok := latest.Receipts[planned.Path]; ok {
				if accepted.SHA256 == digest {
					return accepted, nil
				}
				return SourceFileReceipt{}, &SourcePublishConflictError{Reason: "durable file receipt does not match the uploaded bytes"}
			}
		}
		cleanupErr := p.clearCandidateSourceFile(context.WithoutCancel(ctx), record, planned.Path, owner, resourceType)
		return SourceFileReceipt{}, errors.Join(err, cleanupErr)
	}
	return receipt, nil
}

func sourceLoadFileForType(files []SourceManifestFile, resourceType string) (SourceManifestFile, bool) {
	for _, file := range files {
		typeName, err := canonicalSourceResourceType(file.Path)
		if err == nil && typeName == resourceType {
			return file, true
		}
	}
	return SourceManifestFile{}, false
}

func hashSourceLoadFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open source file: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash source file: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (p *RepositorySourceLoader) claimSourceLoadFile(ctx context.Context, key, path, owner, digest string) (bool, error) {
	claimed := false
	now := time.Now().UTC()
	expires := time.Now().Add(time.Duration(sourceLoadClaimMillis) * time.Millisecond).UnixMilli()
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
LET claim = record == null ? null : NOT_NULL(record.file_claims[@path], null)
LET pinned_digest = record == null ? null : NOT_NULL(record.file_digests[@path], null)
FILTER record != null AND record.state == @uploading AND record.copy_complete == true
FILTER POSITION(record.plan.files[*].path, @path, true) >= 0
FILTER NOT HAS(record.receipts, @path)
FILTER pinned_digest == null OR pinned_digest == @digest
FILTER claim == null OR TO_NUMBER(NOT_NULL(claim.expires_at, 0)) < DATE_NOW()
UPDATE record WITH {
  file_digests: MERGE(NOT_NULL(record.file_digests, {}), ZIP([@path], [@digest])),
  file_claims: MERGE(NOT_NULL(record.file_claims, {}), ZIP([@path], [{owner: @owner, expires_at: @expires}])),
  updated_at: @now
} IN @@loads
RETURN {claimed: true}`, 1, map[string]any{
		"@loads": sourceLoadCollection, "key": key, "path": path, "owner": owner,
		"digest": digest, "expires": expires, "uploading": SourceLoadUploading, "now": now,
	}, func(row map[string]any) error { claimed, _ = row["claimed"].(bool); return nil })
	return claimed, err
}

func (p *RepositorySourceLoader) renewSourceLoadFileLease(parent context.Context, key, path, owner string) (context.Context, func()) {
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
				ok, err := p.renewSourceLoadFileOnce(ctx, key, path, owner)
				if err != nil {
					cancel(fmt.Errorf("renew source file upload lease: %w", err))
					return
				}
				if !ok {
					cancel(&SourcePublishConflictError{Reason: "source file upload lease was lost"})
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

func (p *RepositorySourceLoader) renewSourceLoadFileOnce(ctx context.Context, key, path, owner string) (bool, error) {
	updated := false
	expires := time.Now().Add(time.Duration(sourceLoadClaimMillis) * time.Millisecond).UnixMilli()
	err := p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
LET claim = record == null ? null : NOT_NULL(record.file_claims[@path], null)
FILTER record != null AND record.state == @uploading AND claim.owner == @owner AND claim.expires_at >= DATE_NOW()
UPDATE record WITH {file_claims: MERGE(record.file_claims, ZIP([@path], [{owner: @owner, expires_at: @expires}]))} IN @@loads
RETURN {updated: true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "path": path, "owner": owner, "expires": expires, "uploading": SourceLoadUploading}, func(row map[string]any) error {
		updated, _ = row["updated"].(bool)
		return nil
	})
	return updated, err
}

func (p *RepositorySourceLoader) completeSourceLoadFile(ctx context.Context, key, path, owner string, receipt SourceFileReceipt) error {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	updated := false
	err = p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
LET claim = record == null ? null : NOT_NULL(record.file_claims[@path], null)
LET receipts = MERGE(NOT_NULL(record.receipts, {}), ZIP([@path], [@receipt]))
FILTER record != null AND record.state == @uploading AND claim.owner == @owner AND claim.expires_at >= DATE_NOW()
FILTER NOT HAS(record.receipts, @path)
UPDATE record WITH {
  receipts: receipts,
  file_claims: UNSET(NOT_NULL(record.file_claims, {}), @path),
  state: LENGTH(ATTRIBUTES(receipts)) == LENGTH(record.plan.files) ? @ready : @uploading,
  updated_at: @now
} IN @@loads
RETURN {updated: true}`, 1, map[string]any{
		"@loads": sourceLoadCollection, "key": key, "path": path, "owner": owner,
		"receipt": json.RawMessage(encoded), "ready": SourceLoadReady, "uploading": SourceLoadUploading,
		"now": time.Now().UTC(),
	}, func(row map[string]any) error { updated, _ = row["updated"].(bool); return nil })
	if err != nil {
		return err
	}
	if !updated {
		return &SourcePublishConflictError{Reason: "source file receipt lost its upload lease or completion slot"}
	}
	return nil
}

func (p *RepositorySourceLoader) releaseSourceLoadFile(key, path, owner string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
LET claim = record == null ? null : NOT_NULL(record.file_claims[@path], null)
FILTER record != null AND claim.owner == @owner
UPDATE record WITH {file_claims: UNSET(record.file_claims, @path)} IN @@loads
RETURN {released: true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "path": path, "owner": owner}, func(map[string]any) error { return nil })
}

func (p *RepositorySourceLoader) clearCandidateSourceFile(ctx context.Context, record sourceLoadRecord, path, owner, resourceType string) error {
	targets := []struct{ collection, condition string }{
		{resourceType, "doc.dataset_generation == @generation"},
		{EdgeCollection, "doc.dataset_generation == @generation AND doc.source_file == @resource_type"},
		{catalog.FieldCatalogCollection, "doc.dataset_generation == @generation AND doc.resource_type == @resource_type"},
		{catalog.SemanticInventoryCollection, "doc.dataset_generation == @generation AND doc.resource_type == @resource_type"},
	}
	for _, target := range targets {
		if exists, err := p.client.CollectionExists(ctx, target.collection); err != nil {
			return err
		} else if !exists {
			continue
		}
		for {
			removed, fenced, err := p.clearCandidateSourceFileBatch(ctx, record, path, owner, resourceType, target.collection, target.condition)
			if err != nil {
				return fmt.Errorf("clear prior candidate %s rows for %s: %w", target.collection, resourceType, err)
			}
			if !fenced {
				return &SourcePublishConflictError{Reason: "candidate cleanup lost the file-upload fence"}
			}
			if removed < p.batchSize {
				break
			}
		}
	}
	return nil
}

func (p *RepositorySourceLoader) clearCandidateSourceFileBatch(ctx context.Context, record sourceLoadRecord, path, owner, resourceType, collection, condition string) (int, bool, error) {
	txClient, ok := any(p.client).(arangostore.TransactionalRowClient)
	if !ok {
		return 0, false, fmt.Errorf("fenced candidate cleanup requires a transactional Arango client")
	}
	removed := 0
	fenced := false
	err := txClient.WithTransaction(ctx, arangostore.TransactionCollections{
		Read: []string{sourceLoadCollection}, Write: []string{collection, sourceLoadCollection},
	}, func(txCtx context.Context, tx arangostore.RowQueryer) error {
		query := `LET updated = FIRST(
  FOR record IN @@loads
    FILTER record._key == @key
    FILTER record.state == @uploading
    FILTER NOT HAS(record.receipts, @path)
    FILTER record.file_claims[@path].owner == @owner
    FILTER record.file_claims[@path].expires_at >= DATE_NOW()
    UPDATE record WITH {cleanup_fence: @owner} IN @@loads
    RETURN NEW
)
FILTER updated != null
LET deleted = (
  FOR doc IN @@collection
    FILTER doc.project == @project
    FILTER ` + condition + `
    LIMIT @batch_size
    REMOVE doc IN @@collection
    RETURN 1
)
RETURN {fenced: true, removed: LENGTH(deleted)}`
		binds := map[string]any{
			"@loads": sourceLoadCollection, "@collection": collection,
			"key": record.Key, "path": path, "owner": owner,
			"uploading": SourceLoadUploading, "project": record.Project,
			"generation": record.Generation,
			"batch_size": p.batchSize,
		}
		if collection != resourceType {
			binds["resource_type"] = resourceType
		}
		return tx.QueryRows(txCtx, query, 1, binds, func(row map[string]any) error {
			fenced, _ = row["fenced"].(bool)
			removed = int(numericInt64(row["removed"]))
			return nil
		})
	})
	return removed, fenced, err
}
