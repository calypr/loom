package sourceload

import (
	"context"
	"fmt"
	"time"

	"github.com/calypr/loom/internal/lease"
)

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
	renewal := lease.StartRenewal(parent, 30*time.Second, func(ctx context.Context) error {
		updated, err := p.renewCandidateCopyLeaseOnce(ctx, key, owner)
		if err != nil {
			return fmt.Errorf("renew candidate initialization lease: %w", err)
		}
		if !updated {
			return &SourcePublishConflictError{Reason: "candidate initialization lease was lost"}
		}
		return nil
	})
	return renewal.Context(), func() { renewal.Stop(context.Canceled) }
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
	renewal := lease.StartRenewal(parent, 30*time.Second, func(ctx context.Context) error {
		updated, err := p.renewSourceLoadFileOnce(ctx, key, path, owner)
		if err != nil {
			return fmt.Errorf("renew source file upload lease: %w", err)
		}
		if !updated {
			return &SourcePublishConflictError{Reason: "source file upload lease was lost"}
		}
		return nil
	})
	return renewal.Context(), func() { renewal.Stop(context.Canceled) }
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

func (p *RepositorySourceLoader) releaseSourceLoadFile(key, path, owner string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.client.QueryRows(ctx, `LET record = DOCUMENT(@@loads, @key)
LET claim = record == null ? null : NOT_NULL(record.file_claims[@path], null)
FILTER record != null AND claim.owner == @owner
UPDATE record WITH {file_claims: UNSET(record.file_claims, @path)} IN @@loads
RETURN {released: true}`, 1, map[string]any{"@loads": sourceLoadCollection, "key": key, "path": path, "owner": owner}, func(map[string]any) error { return nil })
}
