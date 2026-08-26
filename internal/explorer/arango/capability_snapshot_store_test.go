package arango

import (
	"context"
	"regexp"
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
	assertOnlyDeclaredCapabilityBinds(t, call)
	if !strings.Contains(call.query, "INSERT @doc") || !strings.Contains(call.query, `overwriteMode: "ignore"`) || strings.Contains(call.query, "UPDATE") {
		t.Fatalf("Put query is not immutable insert-if-absent:\n%s", call.query)
	}
	if _, exists := call.binds["key"]; exists {
		t.Fatalf("Put supplied undeclared key bind: %#v", call.binds)
	}
	if len(call.binds) != 2 || call.binds["@c"] != CapabilitySnapshotCollection {
		t.Fatalf("Put binds = %#v", call.binds)
	}
	doc := call.binds["doc"].(map[string]any)
	if doc["token"] != snapshot.Token || doc["identity"] == nil || doc["status"] != string(snapshot.Status) {
		t.Fatalf("canonical full document=%#v", doc)
	}

	if _, err := adapter.GetByToken(context.Background(), snapshot.Token); err != nil {
		t.Fatal(err)
	}
	assertOnlyDeclaredCapabilityBinds(t, client.calls[len(client.calls)-1])

	client.row, err = canonicalSnapshotDocument(snapshot, capabilitySnapshotKey(snapshot.Token))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.GetByIdentity(context.Background(), snapshot.Identity); err != nil {
		t.Fatal(err)
	}
	identityCall := client.calls[len(client.calls)-1]
	assertOnlyDeclaredCapabilityBinds(t, identityCall)
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

func assertOnlyDeclaredCapabilityBinds(t *testing.T, call queryCall) {
	t.Helper()
	for name := range call.binds {
		pattern := "@" + regexp.QuoteMeta(name) + `\b`
		if !regexp.MustCompile(pattern).MatchString(call.query) {
			t.Errorf("bind variable %q is not declared in query", name)
		}
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
