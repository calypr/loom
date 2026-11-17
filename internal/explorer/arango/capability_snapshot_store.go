package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
	store "github.com/calypr/loom/internal/store/arango"
)

const CapabilitySnapshotCollection = "loom_explorer_capability_snapshots"

func CapabilitySnapshotCollectionSpec() store.CollectionSpec {
	return store.CollectionSpec{
		Name: CapabilitySnapshotCollection,
		Indexes: [][]string{
			{"identity.project", "identity.generation", "identity.authorizationScopeDigest", "identity.protocolVersion", "identity.compilerVersion"},
			{"identity.project", "identity.generation", "identity.authorizationScopeDigest", "status"},
		},
	}
}

type CapabilitySnapshotStore struct {
	client    client
	snapshots *snapshotRevisionCache
}

func NewCapabilitySnapshotStore(c client) (*CapabilitySnapshotStore, error) {
	if c == nil {
		return nil, fmt.Errorf("Explorer capability snapshot Arango client is required")
	}
	return &CapabilitySnapshotStore{client: c, snapshots: newSnapshotRevisionCache(capabilitySnapshotCacheMaxEntries, capabilitySnapshotCacheMaxBytes)}, nil
}

func (s *CapabilitySnapshotStore) Put(ctx context.Context, snapshot capability.Snapshot) (*capability.Snapshot, error) {
	if err := capability.ValidateSnapshot(snapshot); err != nil {
		return nil, err
	}
	doc, err := canonicalSnapshotDocument(snapshot, capabilitySnapshotKey(snapshot.Token))
	if err != nil {
		return nil, err
	}
	var out *capability.Snapshot
	err = s.client.QueryRows(ctx, capabilitySnapshotPutAQL, 1, map[string]any{
		"@c":  CapabilitySnapshotCollection,
		"doc": doc,
	}, func(row map[string]any) error {
		value, decodeErr := decode[capability.Snapshot](row)
		if decodeErr != nil {
			return decodeErr
		}
		if value.Token != snapshot.Token {
			return fmt.Errorf("%w: %s", capability.ErrTokenCollision, snapshot.Token)
		}
		if string(value.CanonicalPayload()) != string(snapshot.CanonicalPayload()) {
			return fmt.Errorf("%w: %s", capability.ErrTokenCollision, snapshot.Token)
		}
		value = value.Clone()
		out = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		existing, getErr := s.GetByToken(ctx, snapshot.Token)
		if getErr != nil {
			return nil, getErr
		}
		if string(existing.CanonicalPayload()) != string(snapshot.CanonicalPayload()) {
			return nil, fmt.Errorf("%w: %s", capability.ErrTokenCollision, snapshot.Token)
		}
		return existing, nil
	}
	return out, nil
}

func (s *CapabilitySnapshotStore) GetByToken(ctx context.Context, token string) (*capability.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := capabilitySnapshotKey(token)
	cached := s.snapshots.get(token)
	revision := ""
	if cached != nil {
		revision = cached.revision
	}
	var resolved *snapshotRevisionEntry
	err := s.client.QueryRows(ctx, capabilitySnapshotGetByTokenAQL, 1, map[string]any{
		"@c": CapabilitySnapshotCollection, "key": key, "token": token, "revision": revision,
	}, func(row map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if resolved != nil {
			return fmt.Errorf("%w: multiple snapshot rows", capability.ErrInvalidSnapshot)
		}
		if row["_key"] != key || row["token"] != token {
			return fmt.Errorf("%w: snapshot identity mismatch", capability.ErrInvalidSnapshot)
		}
		if marker, exists := row["_loomCapabilityUnchanged"]; exists {
			if marker != true || len(row) != 4 || cached == nil || revision == "" || row["_rev"] != revision {
				return fmt.Errorf("%w: invalid unchanged snapshot marker", capability.ErrInvalidSnapshot)
			}
			resolved = cached
			return nil
		}
		raw, err := json.Marshal(row)
		if err != nil {
			return err
		}
		var value capability.Snapshot
		if err = json.Unmarshal(raw, &value); err != nil {
			return err
		}
		if err = capability.ValidateSnapshot(value); err != nil {
			return err
		}
		storedRevision, _ := row["_rev"].(string)
		resolved = &snapshotRevisionEntry{token: token, revision: storedRevision, snapshot: value, bytes: len(raw)}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if resolved == nil {
		return nil, capability.ErrNotFound
	}
	if resolved != cached {
		s.snapshots.put(resolved)
	}
	out := resolved.snapshot.Clone()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *CapabilitySnapshotStore) GetByIdentity(ctx context.Context, identity capability.SnapshotIdentity) (*capability.Snapshot, error) {
	var out *capability.Snapshot
	err := s.client.QueryRows(ctx, capabilitySnapshotGetByIdentityAQL, 1, map[string]any{
		"@c":                       CapabilitySnapshotCollection,
		"project":                  identity.Project,
		"generation":               identity.Generation,
		"authorizationScopeDigest": identity.AuthorizationScopeDigest,
		"cacheVersion":             identity.CacheVersion,
		"schemaDigest":             identity.SchemaDigest,
		"resourceInventoryDigest":  identity.ResourceInventoryDigest,
		"relationshipDigest":       identity.RelationshipDigest,
		"fieldDigest":              identity.FieldDigest,
		"protocolVersion":          identity.ProtocolVersion,
		"compilerVersion":          identity.CompilerVersion,
		"traversalPolicyVersion":   identity.TraversalPolicyVersion,
		"projectionPolicyVersion":  identity.ProjectionPolicyVersion,
		"statuses":                 []string{string(capability.StatusReady), string(capability.StatusBuilding), string(capability.StatusFailed)},
	}, func(row map[string]any) error {
		value, err := decode[capability.Snapshot](row)
		if err == nil {
			err = capability.ValidateSnapshot(value)
		}
		if err == nil {
			value = value.Clone()
			out = &value
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, capability.ErrNotFound
	}
	return out, nil
}

func capabilitySnapshotKey(token string) string {
	return "capability_snapshot_" + strings.NewReplacer(":", "_", "/", "_").Replace(token)
}

func canonicalSnapshotDocument(snapshot capability.Snapshot, key string) (map[string]any, error) {

	var doc map[string]any
	if err := json.Unmarshal(snapshot.CanonicalPayload(), &doc); err != nil {
		return nil, err
	}
	doc["token"] = snapshot.Token
	doc["_key"] = key
	return doc, nil
}

const capabilitySnapshotPutAQL = `
INSERT @doc INTO @@c
  OPTIONS { overwriteMode: "ignore" }
  RETURN NEW
`

const capabilitySnapshotGetByTokenAQL = `
FOR d IN @@c
  FILTER d._key == @key AND d.token == @token
  RETURN @revision != "" AND d._rev == @revision
    ? { _key: d._key, token: d.token, _rev: d._rev, _loomCapabilityUnchanged: true }
    : d
`

const capabilitySnapshotGetByIdentityAQL = `
FOR d IN @@c
  FILTER d.identity.project == @project
    AND d.identity.generation == @generation
    AND d.identity.authorizationScopeDigest == @authorizationScopeDigest
    AND NOT_NULL(d.identity.cacheVersion, "") == @cacheVersion
		AND d.identity.schemaDigest == @schemaDigest
		AND d.identity.resourceInventoryDigest == @resourceInventoryDigest
		AND d.identity.relationshipDigest == @relationshipDigest
    AND d.identity.fieldDigest == @fieldDigest
    AND d.identity.protocolVersion == @protocolVersion
    AND d.identity.compilerVersion == @compilerVersion
    AND d.identity.traversalPolicyVersion == @traversalPolicyVersion
    AND d.identity.projectionPolicyVersion == @projectionPolicyVersion
    AND d.status IN @statuses
SORT d.status == "READY" DESC, d.token
LIMIT 1
RETURN d
`

var _ capability.Repository = (*CapabilitySnapshotStore)(nil)
