package capabilitystore

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func testSnapshot(status capability.Status, complete bool) capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope-a",
			SchemaDigest: "schema-a", RelationshipDigest: "relationship-a", FieldDigest: "field-a",
			ProtocolVersion: "protocol-a", CompilerVersion: "compiler-a", TraversalPolicyVersion: "traversal-a", ProjectionPolicyVersion: "projection-a",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "route-a"}, Projection: capability.ProjectionPolicy{Version: "projection-a", Modes: []capability.ProjectionMode{capability.ProjectionFirst}}},
		status, complete, false,
		[]capability.Node{{ID: "node-a", ResourceType: "Patient", RowRootEligible: true, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil, nil,
		[]capability.Diagnostic{{Code: "notice", Message: "kept", Details: map[string]any{"nested": map[string]any{"value": "original"}}}},
	)
}

func TestMemoryStoreIsIdempotentAndDefensive(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	want := testSnapshot(capability.StatusReady, true)
	got, err := store.Put(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	got.Nodes[0].SupportedOperations[0] = capability.OperationFilter
	got.Diagnostics[0].Details["nested"].(map[string]any)["value"] = "changed"
	gotAgain, err := store.Put(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if gotAgain.Nodes[0].SupportedOperations[0] != capability.OperationSelect {
		t.Fatal("Put returned an aliased node slice")
	}
	read, err := store.GetByToken(ctx, want.Token)
	if err != nil {
		t.Fatal(err)
	}
	if read.Diagnostics[0].Details["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("nested diagnostic data was aliased")
	}
	if read.Token != want.Token {
		t.Fatalf("token=%q, want %q", read.Token, want.Token)
	}
}

func TestMemoryStoreIdentityPrefersReadyAndRetainsFailedDiagnostics(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	failed := testSnapshot(capability.StatusFailed, false)
	if _, err := store.Put(ctx, failed); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetByIdentity(ctx, failed.Identity)
	if err != nil || got.Status != capability.StatusFailed {
		t.Fatalf("failed lookup=(%v, %v)", got, err)
	}
	ready := testSnapshot(capability.StatusReady, true)
	if _, err := store.Put(ctx, ready); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetByIdentity(ctx, ready.Identity)
	if err != nil || got.Status != capability.StatusReady {
		t.Fatalf("ready lookup=(%v, %v)", got, err)
	}
}

func TestMemoryStoreRejectsUnusableReadyAndTokenCollision(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	unusable := testSnapshot(capability.StatusReady, false)
	if _, err := store.Put(ctx, unusable); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("Put unusable READY error=%v", err)
	}
	first := testSnapshot(capability.StatusFailed, false)
	if _, err := store.Put(ctx, first); err != nil {
		t.Fatal(err)
	}
	collision := testSnapshot(capability.StatusReady, true)
	collision.Token = first.Token
	if _, err := store.Put(ctx, collision); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("Put token collision error=%v", err)
	}
}
