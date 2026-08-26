package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/capabilitystore"
	store "github.com/calypr/loom/internal/store/arango"
)

// CapabilitySnapshotCollection is intentionally separate from the legacy
// Explorer collections. Registration in the server bootstrap should include
// this persistent, non-truncating collection.
const CapabilitySnapshotCollection = "loom_explorer_capability_snapshots"

// CapabilitySnapshotCollectionSpec is exported so the server's shared
// bootstrap registry can append it without coupling this adapter to the
// legacy collection list.
func CapabilitySnapshotCollectionSpec() store.CollectionSpec {
	return store.CollectionSpec{
		Name: CapabilitySnapshotCollection,
		Indexes: [][]string{
			{"identity.project", "identity.generation", "identity.authorizationScopeDigest", "identity.protocolVersion", "identity.compilerVersion"},
			{"identity.project", "identity.generation", "identity.authorizationScopeDigest", "status"},
		},
	}
}

// CapabilitySnapshotStore persists the complete canonical snapshot under its
// content address. Insert-ignore is deliberate: retries never execute an
// UPDATE, so an immutable document's payload and revision remain untouched.
type CapabilitySnapshotStore struct{ client client }

func NewCapabilitySnapshotStore(c client) (*CapabilitySnapshotStore, error) {
	if c == nil {
		return nil, fmt.Errorf("Explorer capability snapshot Arango client is required")
	}
	return &CapabilitySnapshotStore{client: c}, nil
}

// NewSnapshotStore is a concise compatibility constructor.
func NewSnapshotStore(c client) (*CapabilitySnapshotStore, error) {
	return NewCapabilitySnapshotStore(c)
}

func (s *CapabilitySnapshotStore) Put(ctx context.Context, snapshot capability.Snapshot) (*capability.Snapshot, error) {
	if err := capabilitystore.ValidateSnapshot(snapshot); err != nil {
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
			return fmt.Errorf("%w: %s", capabilitystore.ErrTokenCollision, snapshot.Token)
		}
		if string(value.CanonicalPayload()) != string(snapshot.CanonicalPayload()) {
			return fmt.Errorf("%w: %s", capabilitystore.ErrTokenCollision, snapshot.Token)
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
			return nil, fmt.Errorf("%w: %s", capabilitystore.ErrTokenCollision, snapshot.Token)
		}
		return existing, nil
	}
	return out, nil
}

func (s *CapabilitySnapshotStore) GetByToken(ctx context.Context, token string) (*capability.Snapshot, error) {
	var out *capability.Snapshot
	err := s.client.QueryRows(ctx, capabilitySnapshotGetByTokenAQL, 1, map[string]any{
		"@c":    CapabilitySnapshotCollection,
		"key":   capabilitySnapshotKey(token),
		"token": token,
	}, func(row map[string]any) error {
		value, err := decode[capability.Snapshot](row)
		if err == nil {
			err = capabilitystore.ValidateSnapshot(value)
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
		return nil, capabilitystore.ErrNotFound
	}
	return out, nil
}

func (s *CapabilitySnapshotStore) GetByIdentity(ctx context.Context, identity capability.SnapshotIdentity) (*capability.Snapshot, error) {
	var out *capability.Snapshot
	err := s.client.QueryRows(ctx, capabilitySnapshotGetByIdentityAQL, 1, map[string]any{
		"@c":                       CapabilitySnapshotCollection,
		"project":                  identity.Project,
		"generation":               identity.Generation,
		"authorizationScopeDigest": identity.AuthorizationScopeDigest,
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
			err = capabilitystore.ValidateSnapshot(value)
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
		return nil, capabilitystore.ErrNotFound
	}
	return out, nil
}

func capabilitySnapshotKey(token string) string {
	return "capability_snapshot_" + strings.NewReplacer(":", "_", "/", "_").Replace(token)
}

func canonicalSnapshotDocument(snapshot capability.Snapshot, key string) (map[string]any, error) {
	// CanonicalPayload is the exact payload covered by Token. Add Token back to
	// that normalized object so Arango stores a canonical *full* snapshot.
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
  RETURN d
`

const capabilitySnapshotGetByIdentityAQL = `
FOR d IN @@c
  FILTER d.identity.project == @project
    AND d.identity.generation == @generation
    AND d.identity.authorizationScopeDigest == @authorizationScopeDigest
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

var _ capabilitystore.Repository = (*CapabilitySnapshotStore)(nil)
