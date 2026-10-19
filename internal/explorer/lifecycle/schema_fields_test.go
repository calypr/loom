package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestSearchSchemaFieldsIncludesMissingNullableGeneratedField(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := schemaFieldsTestSnapshot(scope, nil)
	service := schemaFieldsTestService(snapshot, scope)

	result, err := service.SearchSchemaFields(context.Background(), SearchSchemaFieldsRequest{
		Project: snapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: snapshot.Token,
		NodeID: "patient-node", Query: "gender", Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchSchemaFields returned error: %v", err)
	}
	if result.SchemaDigest != snapshot.Identity.SchemaDigest || result.ResourceType != "Patient" || !result.Complete || result.Truncated || result.NextCursor != "" {
		t.Fatalf("page context/completeness = %#v", result)
	}
	var field *SchemaFieldOption
	for index := range result.Fields {
		if result.Fields[index].Path == "gender" {
			field = &result.Fields[index]
			break
		}
	}
	if field == nil {
		t.Fatalf("generated Patient.gender option was absent from substring results: %#v", result.Fields)
	}
	if field.Origin != "GENERATED_SCHEMA" || field.NodeID != "patient-node" || field.ResourceType != "Patient" ||
		field.Path != "gender" || field.PrimitiveType != "string" || field.Cardinality != "optional_one" || len(field.RepeatedPaths) != 0 {
		t.Fatalf("generated Patient.gender option = %#v", field)
	}
}

func TestSearchSchemaFieldsRemovesObservedPathDuplicate(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	candidate := capability.Candidate{
		ID: "observed-gender", NodeID: "patient-node", ResourceType: "Patient", FieldPath: "gender",
		Observed: true, ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	snapshot := schemaFieldsTestSnapshot(scope, []capability.Candidate{candidate})
	service := schemaFieldsTestService(snapshot, scope)

	result, err := service.SearchSchemaFields(context.Background(), SearchSchemaFieldsRequest{
		Project: snapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: snapshot.Token,
		NodeID: "patient-node", Query: "gender", Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchSchemaFields returned error: %v", err)
	}
	for _, field := range result.Fields {
		if field.Path == "gender" {
			t.Fatalf("observed path was duplicated by generated option: %#v", result)
		}
	}
	if !result.Complete || result.Truncated {
		t.Fatalf("observed-path filter returned incomplete results: %#v", result)
	}
}

func TestSearchSchemaFieldsPagesStablePathsAndBindsQuery(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := schemaFieldsTestSnapshot(scope, nil)
	service := schemaFieldsTestService(snapshot, scope)
	request := SearchSchemaFieldsRequest{
		Project: snapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: snapshot.Token,
		NodeID: "observation-node", Limit: 1,
	}
	first, err := service.SearchSchemaFields(context.Background(), request)
	if err != nil || len(first.Fields) != 1 || !first.Truncated || first.Complete || first.NextCursor == "" {
		t.Fatalf("first page = %#v, error=%v", first, err)
	}
	request.Cursor = first.NextCursor
	second, err := service.SearchSchemaFields(context.Background(), request)
	if err != nil || len(second.Fields) != 1 || second.Fields[0].Path <= first.Fields[0].Path {
		t.Fatalf("second page = %#v, error=%v", second, err)
	}
	request.Query = "status"
	if _, err := service.SearchSchemaFields(context.Background(), request); !schemaFieldsErrorCode(err, "STALE_SCHEMA_FIELD_CURSOR") {
		t.Fatalf("query-mismatched cursor error = %v", err)
	}
}

func TestSearchSchemaFieldsRejectsCursorAfterAuthorizationScopeChange(t *testing.T) {
	initialScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := schemaFieldsTestSnapshot(initialScope, nil)
	currentScope := initialScope
	service := &Service{config: Config{Capability: CapabilityResolver{
		ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: currentScope}, nil
		},
	}}}
	first, err := service.SearchSchemaFields(context.Background(), SearchSchemaFieldsRequest{
		Project: snapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: snapshot.Token,
		NodeID: "observation-node", Limit: 1,
	})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %#v, error=%v", first, err)
	}

	currentScope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"Patient"}}
	_, err = service.SearchSchemaFields(context.Background(), SearchSchemaFieldsRequest{
		Project: snapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: snapshot.Token,
		NodeID: "observation-node", Limit: 1, Cursor: first.NextCursor,
	})
	if !schemaFieldsErrorCode(err, "STALE_AUTHORIZATION_SCOPE") {
		t.Fatalf("scope-changed page error = %v", err)
	}
}

func TestSearchSchemaFieldsRequiresActiveGeneratedSchemaDigest(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	base := schemaFieldsTestSnapshot(scope, nil)
	identity := base.Identity
	identity.SchemaDigest = "schema-v1"
	snapshot := capability.NewSnapshot(identity, base.Policy, base.Status, base.Complete, base.Truncated, base.Nodes, base.Edges, base.Candidates, base.Diagnostics)
	service := schemaFieldsTestService(snapshot, scope)
	_, err := service.SearchSchemaFields(context.Background(), SearchSchemaFieldsRequest{
		Project: snapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: snapshot.Token,
		NodeID: "patient-node", Query: "gender", Limit: 10,
	})
	if !schemaFieldsErrorCode(err, "STALE_CATALOG_SNAPSHOT") {
		t.Fatalf("invalid schema identity error = %v", err)
	}
}

func TestSchemaFieldCursorRetainsActiveGeneratedSchemaDigest(t *testing.T) {
	cursor := schemaFieldCursor{
		Version: 1, Project: "project-a", ExplorerID: "builder", SnapshotToken: "snapshot-a", Generation: "generation-a",
		AuthorizationScopeDigest: "scope-a", SchemaDigest: strings.Repeat("b", 64), NodeID: "node-a", ResourceType: "Patient",
		Query: "gender", Limit: 20, LastPath: "gender",
	}
	encoded, err := encodeSchemaFieldCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeSchemaFieldCursor(encoded)
	if err != nil || decoded.SchemaDigest != cursor.SchemaDigest || decoded.LastPath != cursor.LastPath {
		t.Fatalf("cursor round trip = %#v, error=%v", decoded, err)
	}
}

func TestSearchSchemaFieldsRejectsCursorAfterActiveSchemaDigestChanges(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := schemaFieldsTestSnapshot(scope, nil)
	firstService := schemaFieldsTestService(snapshot, scope)
	first, err := firstService.SearchSchemaFields(context.Background(), SearchSchemaFieldsRequest{
		Project: snapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: snapshot.Token,
		NodeID: "observation-node", Limit: 1,
	})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first page = %#v, error=%v", first, err)
	}

	identity := snapshot.Identity
	identity.SchemaDigest = strings.Repeat("b", 64)
	changedSnapshot := capability.NewSnapshot(identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
	changedService := schemaFieldsTestService(changedSnapshot, scope)
	_, err = changedService.SearchSchemaFields(context.Background(), SearchSchemaFieldsRequest{
		Project: changedSnapshot.Identity.Project, ExplorerID: "builder", SnapshotToken: changedSnapshot.Token,
		NodeID: "observation-node", Limit: 1, Cursor: first.NextCursor,
	})
	if !schemaFieldsErrorCode(err, "STALE_SCHEMA_FIELD_CURSOR") {
		t.Fatalf("schema-changed cursor error = %v", err)
	}
}

func schemaFieldsTestSnapshot(scope authscope.ReadScope, candidates []capability.Candidate) capability.Snapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	scopeHash := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	identity := capability.SnapshotIdentity{
		Project: "loom_dev_cda_fhir", Generation: "cda-fhir-v1",
		AuthorizationScopeDigest: hex.EncodeToString(scopeHash[:]), SchemaDigest: strings.Repeat("a", 64),
		ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields",
		ProtocolVersion: "protocol", CompilerVersion: "compiler", TraversalPolicyVersion: "route", ProjectionPolicyVersion: "projection",
	}
	return capability.NewSnapshot(identity, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{
		{ID: "patient-node", ResourceType: "Patient", Populated: true},
		{ID: "observation-node", ResourceType: "Observation", Populated: true},
	}, nil, candidates, nil)
}

func schemaFieldsTestService(snapshot capability.Snapshot, scope authscope.ReadScope) *Service {
	return &Service{config: Config{Capability: CapabilityResolver{
		ForCompilation: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
		},
	}}}
}

func schemaFieldsErrorCode(err error, code string) bool {
	var lifecycleErr *Error
	return errors.As(err, &lifecycleErr) && lifecycleErr.Code == code
}
