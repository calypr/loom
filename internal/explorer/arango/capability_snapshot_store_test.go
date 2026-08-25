package arango

import (
	"context"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
	storepkg "github.com/calypr/loom/internal/store/arango"
)

type capabilitySnapshotClient struct {
	calls []queryCall
	row   map[string]any
}

func (c *capabilitySnapshotClient) WithTransaction(ctx context.Context, _ storepkg.TransactionCollections, fn storepkg.TransactionFunc) error {
	return fn(ctx, c)
}

func (c *capabilitySnapshotClient) QueryRows(_ context.Context, query string, _ int, binds map[string]any, visit storepkg.RowVisitor) error {
	c.calls = append(c.calls, queryCall{query: query, binds: binds})
	if c.row != nil {
		return visit(c.row)
	}
	return nil
}

func TestCapabilitySnapshotStoreBindsImmutableIdentity(t *testing.T) {
	client := &capabilitySnapshotClient{}
	adapter, err := NewCapabilitySnapshotStore(client)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := testCapabilitySnapshot()
	client.row, err = canonicalSnapshotDocument(snapshot, capabilitySnapshotKey(snapshot.Token))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Put(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	if len(client.calls) != 1 {
		t.Fatalf("calls=%d", len(client.calls))
	}
	call := client.calls[0]
	if !strings.Contains(call.query, "INSERT @doc") || !strings.Contains(call.query, `overwriteMode: "ignore"`) || strings.Contains(call.query, "UPDATE") {
		t.Fatalf("Put query is not immutable insert-if-absent:\n%s", call.query)
	}
	doc := call.binds["doc"].(map[string]any)
	if doc["token"] != snapshot.Token || doc["identity"] == nil || doc["status"] != string(snapshot.Status) {
		t.Fatalf("canonical full document=%#v", doc)
	}

	client.row, err = canonicalSnapshotDocument(snapshot, capabilitySnapshotKey(snapshot.Token))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.GetByIdentity(context.Background(), snapshot.Identity); err != nil {
		t.Fatal(err)
	}
	identityCall := client.calls[len(client.calls)-1]
	for _, required := range []string{
		"d.identity.project == @project", "d.identity.generation == @generation",
		"d.identity.authorizationScopeDigest == @authorizationScopeDigest",
		"d.identity.protocolVersion == @protocolVersion", "d.identity.compilerVersion == @compilerVersion",
		"d.status IN @statuses",
	} {
		if !strings.Contains(identityCall.query, required) {
			t.Fatalf("identity query missing %q:\n%s", required, identityCall.query)
		}
	}
	if got := identityCall.binds["project"]; got != snapshot.Identity.Project {
		t.Fatalf("project bind=%v", got)
	}
	if got := identityCall.binds["generation"]; got != snapshot.Identity.Generation {
		t.Fatalf("generation bind=%v", got)
	}
}

func testCapabilitySnapshot() capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-a",
			SchemaDigest: "schema-a", ResourceInventoryDigest: "inventory-a", RelationshipDigest: "relationship-a", FieldDigest: "field-a",
			ProtocolVersion: "protocol-a", CompilerVersion: "compiler-a", TraversalPolicyVersion: "traversal-a", ProjectionPolicyVersion: "projection-a",
		},
		capability.Policy{}, capability.StatusFailed, false, false, nil, nil, nil,
		[]capability.Diagnostic{{Code: "failed", Message: "diagnostic"}},
	)
}
