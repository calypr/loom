// Package arango stores private ClickHouse artifact manifests and leases in a
// dedicated Arango collection. It does not register artifacts as published
// outputs or update any publication pointer.
package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

const (
	PrivateArtifactsCollection = "loom_dataframe_private_artifacts"
	queryBatchSize             = 32
	maxExpiredPageSize         = 1000
	manifestTimeLayout         = "2006-01-02T15:04:05.000000000Z"
)

// Catalog implements chartifact.Catalog using single-document conditional
// AQL writes. The Arango document key is the immutable ArtifactID.
type Catalog struct {
	client arangostore.RowQueryer
}

var _ chartifact.Catalog = (*Catalog)(nil)

func NewCatalog(client arangostore.RowQueryer) (*Catalog, error) {
	if client == nil {
		return nil, fmt.Errorf("private artifact Arango query client is required")
	}
	return &Catalog{client: client}, nil
}

func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{
		Name:    PrivateArtifactsCollection,
		Indexes: [][]string{{"leaseUntil"}},
	}}}
}

// Create inserts an immutable manifest only when its ArtifactID is unused.
// Concurrent duplicate creates can produce an Arango write-conflict error;
// neither path replaces an existing manifest.
func (c *Catalog) Create(ctx context.Context, manifest chartifact.Manifest) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(manifest.ArtifactID) == "" || manifest.ArtifactID != strings.TrimSpace(manifest.ArtifactID) {
		return fmt.Errorf("private artifact manifest requires a trimmed artifact ID")
	}
	document, err := manifestDocument(manifest)
	if err != nil {
		return fmt.Errorf("encode private artifact manifest: %w", err)
	}
	created := false
	err = c.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing == null
INSERT @document IN @@collection
RETURN {created: true}`, queryBatchSize, map[string]any{
		"@collection": PrivateArtifactsCollection,
		"key":         manifest.ArtifactID,
		"document":    document,
	}, func(row map[string]any) error {
		created = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("insert private artifact manifest %q: %w", manifest.ArtifactID, err)
	}
	if !created {
		return fmt.Errorf("private artifact manifest %q already exists", manifest.ArtifactID)
	}
	return nil
}

// Update changes only progress fields. Active states cannot shorten a lease;
// cleanup-pending is allowed to make a table immediately reclaimable after the
// owner has stopped using it. An expired or stolen lease cannot be updated.
func (c *Catalog) Update(ctx context.Context, artifactID, owner string, progress chartifact.Progress) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if err := validateUpdate(artifactID, owner, progress); err != nil {
		return err
	}
	updated := false
	err := c.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null
  AND existing.leaseOwner == @owner
  AND existing.leaseUntil >= @now
  AND (existing.state != @cleanupPendingState OR @state == @cleanupPendingState)
UPDATE existing WITH {
  state: @state,
  rowCount: @rowCount,
  byteCount: @byteCount,
  leaseUntil: @cleanupPending ? @leaseUntil : MAX([existing.leaseUntil, @leaseUntil]),
  updatedAt: @updatedAt
} IN @@collection OPTIONS { ignoreRevs: false }
RETURN {updated: true}`, queryBatchSize, map[string]any{
		"@collection":         PrivateArtifactsCollection,
		"key":                 artifactID,
		"owner":               owner,
		"now":                 formatManifestTime(time.Now()),
		"state":               progress.State,
		"rowCount":            progress.RowCount,
		"byteCount":           progress.ByteCount,
		"leaseUntil":          formatManifestTime(progress.LeaseUntil),
		"cleanupPending":      progress.State == chartifact.StateCleanupPending,
		"cleanupPendingState": chartifact.StateCleanupPending,
		"updatedAt":           formatManifestTime(progress.UpdatedAt),
	}, func(map[string]any) error {
		updated = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("update private artifact manifest %q: %w", artifactID, err)
	}
	if !updated {
		return chartifact.ErrLeaseLost
	}
	return nil
}

// Renew extends an active, unexpired lease owned by owner. It never shortens
// an existing lease and never resurrects an expired one.
func (c *Catalog) Renew(ctx context.Context, artifactID, owner string, until time.Time) (bool, error) {
	if err := requireContext(ctx); err != nil {
		return false, err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" || until.IsZero() {
		return false, fmt.Errorf("private artifact renewal requires artifact ID, owner, and lease time")
	}
	renewed := false
	err := c.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null
  AND existing.leaseOwner == @owner
  AND existing.state IN @renewableStates
  AND existing.leaseUntil >= @now
UPDATE existing WITH {leaseUntil: MAX([existing.leaseUntil, @leaseUntil]), updatedAt: @now} IN @@collection OPTIONS { ignoreRevs: false }
RETURN {renewed: true}`, queryBatchSize, map[string]any{
		"@collection":     PrivateArtifactsCollection,
		"key":             artifactID,
		"owner":           owner,
		"renewableStates": []chartifact.State{chartifact.StateCreating, chartifact.StateWriting, chartifact.StateReady},
		"now":             formatManifestTime(time.Now()),
		"leaseUntil":      formatManifestTime(until),
	}, func(map[string]any) error {
		renewed = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("renew private artifact lease %q: %w", artifactID, err)
	}
	return renewed, nil
}

// ListExpired returns a bounded, stable-order page of manifests whose lease is
// expired at or before expiredBefore. Cleanup claims recheck this condition.
func (c *Catalog) ListExpired(ctx context.Context, expiredBefore time.Time, limit int) ([]chartifact.Manifest, error) {
	if err := requireContext(ctx); err != nil {
		return nil, err
	}
	if expiredBefore.IsZero() {
		return nil, fmt.Errorf("private artifact expiry cutoff is required")
	}
	if limit <= 0 {
		return []chartifact.Manifest{}, nil
	}
	if limit > maxExpiredPageSize {
		limit = maxExpiredPageSize
	}
	manifests := make([]chartifact.Manifest, 0, limit)
	err := c.client.QueryRows(ctx, `FOR manifest IN @@collection
FILTER manifest.leaseUntil <= @expiredBefore
SORT manifest.leaseUntil ASC, manifest._key ASC
LIMIT @limit
RETURN manifest`, queryBatchSize, map[string]any{
		"@collection":   PrivateArtifactsCollection,
		"expiredBefore": formatManifestTime(expiredBefore),
		"limit":         limit,
	}, func(row map[string]any) error {
		manifest, err := decodeManifest(row)
		if err != nil {
			return err
		}
		manifests = append(manifests, manifest)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list expired private artifacts: %w", err)
	}
	return manifests, nil
}

// ClaimCleanup atomically changes an expired manifest's owner and state. The
// old lease timestamp is rechecked in the write query so a concurrent renewer
// wins cleanly; only one cleanup owner can claim a given expired manifest.
func (c *Catalog) ClaimCleanup(ctx context.Context, artifactID, owner string, expiredBefore, until time.Time) (chartifact.Manifest, bool, error) {
	if err := requireContext(ctx); err != nil {
		return chartifact.Manifest{}, false, err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" || expiredBefore.IsZero() || !until.After(expiredBefore) {
		return chartifact.Manifest{}, false, fmt.Errorf("private artifact cleanup claim requires ID, owner, and a future cleanup lease")
	}
	var claimed *chartifact.Manifest
	err := c.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null AND existing.leaseUntil <= @expiredBefore
UPDATE existing WITH {
  state: @cleanupPending,
  leaseOwner: @owner,
  leaseUntil: @leaseUntil,
  updatedAt: @updatedAt
} IN @@collection OPTIONS { ignoreRevs: false }
RETURN {manifest: NEW}`, queryBatchSize, map[string]any{
		"@collection":    PrivateArtifactsCollection,
		"key":            artifactID,
		"expiredBefore":  formatManifestTime(expiredBefore),
		"cleanupPending": chartifact.StateCleanupPending,
		"owner":          owner,
		"leaseUntil":     formatManifestTime(until),
		"updatedAt":      formatManifestTime(time.Now()),
	}, func(row map[string]any) error {
		value, ok := row["manifest"].(map[string]any)
		if !ok {
			return fmt.Errorf("private artifact cleanup claim returned no manifest")
		}
		manifest, err := decodeManifest(value)
		if err != nil {
			return err
		}
		claimed = &manifest
		return nil
	})
	if err != nil {
		return chartifact.Manifest{}, false, fmt.Errorf("claim private artifact %q cleanup: %w", artifactID, err)
	}
	if claimed == nil {
		return chartifact.Manifest{}, false, nil
	}
	return *claimed, true, nil
}

// Delete removes a cleanup-pending manifest only for its current owner.
func (c *Catalog) Delete(ctx context.Context, artifactID, owner string) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" {
		return fmt.Errorf("private artifact deletion requires artifact ID and owner")
	}
	deleted := false
	err := c.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null
  AND existing.leaseOwner == @owner
  AND existing.state == @cleanupPending
REMOVE existing IN @@collection OPTIONS { ignoreRevs: false }
RETURN {deleted: true}`, queryBatchSize, map[string]any{
		"@collection":    PrivateArtifactsCollection,
		"key":            artifactID,
		"owner":          owner,
		"cleanupPending": chartifact.StateCleanupPending,
	}, func(map[string]any) error {
		deleted = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete private artifact manifest %q: %w", artifactID, err)
	}
	if !deleted {
		return chartifact.ErrLeaseLost
	}
	return nil
}

// ReleaseCleanup makes a failed cleanup immediately reclaimable, but only
// while the caller still owns the cleanup lease.
func (c *Catalog) ReleaseCleanup(ctx context.Context, artifactID, owner string, until time.Time) error {
	if err := requireContext(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" || until.IsZero() {
		return fmt.Errorf("private artifact cleanup release requires artifact ID, owner, and lease time")
	}
	released := false
	err := c.client.QueryRows(ctx, `LET existing = DOCUMENT(@@collection, @key)
FILTER existing != null
  AND existing.leaseOwner == @owner
  AND existing.state == @cleanupPending
UPDATE existing WITH {leaseUntil: @leaseUntil, updatedAt: @updatedAt} IN @@collection OPTIONS { ignoreRevs: false }
RETURN {released: true}`, queryBatchSize, map[string]any{
		"@collection":    PrivateArtifactsCollection,
		"key":            artifactID,
		"owner":          owner,
		"cleanupPending": chartifact.StateCleanupPending,
		"leaseUntil":     formatManifestTime(until),
		"updatedAt":      formatManifestTime(time.Now()),
	}, func(map[string]any) error {
		released = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("release private artifact cleanup lease %q: %w", artifactID, err)
	}
	if !released {
		return chartifact.ErrLeaseLost
	}
	return nil
}

func validateUpdate(artifactID, owner string, progress chartifact.Progress) error {
	if strings.TrimSpace(artifactID) == "" || strings.TrimSpace(owner) == "" {
		return fmt.Errorf("private artifact update requires artifact ID and owner")
	}
	switch progress.State {
	case chartifact.StateCreating, chartifact.StateWriting, chartifact.StateReady, chartifact.StateCleanupPending:
	default:
		return fmt.Errorf("private artifact update has invalid state %q", progress.State)
	}
	if progress.RowCount < 0 || progress.ByteCount < 0 || progress.LeaseUntil.IsZero() || progress.UpdatedAt.IsZero() {
		return fmt.Errorf("private artifact update requires nonnegative counts and lease timestamps")
	}
	return nil
}

func manifestDocument(manifest chartifact.Manifest) (map[string]any, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		return nil, err
	}
	document["_key"] = manifest.ArtifactID
	// time.Time's default JSON encoding omits trailing fractional zeros. These
	// fields are compared and sorted lexicographically by AQL, so persist a
	// fixed-width UTC representation to keep string order chronological.
	document["leaseUntil"] = formatManifestTime(manifest.LeaseUntil)
	document["createdAt"] = formatManifestTime(manifest.CreatedAt)
	document["updatedAt"] = formatManifestTime(manifest.UpdatedAt)
	return document, nil
}

func formatManifestTime(value time.Time) string {
	return value.UTC().Format(manifestTimeLayout)
}

func decodeManifest(document map[string]any) (chartifact.Manifest, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return chartifact.Manifest{}, fmt.Errorf("encode stored private artifact manifest: %w", err)
	}
	var manifest chartifact.Manifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return chartifact.Manifest{}, fmt.Errorf("decode stored private artifact manifest: %w", err)
	}
	if manifest.ArtifactID == "" {
		if key, ok := document["_key"].(string); ok {
			manifest.ArtifactID = key
		}
	}
	if manifest.ArtifactID == "" {
		return chartifact.Manifest{}, errors.New("stored private artifact manifest has no artifact ID")
	}
	return manifest, nil
}

func requireContext(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("private artifact Arango operation requires a context")
	}
	return ctx.Err()
}
