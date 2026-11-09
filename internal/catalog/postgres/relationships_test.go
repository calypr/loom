package postgres

// Offline unit tests for the pg-catalog-03 relationship-evidence adapter:
// scope validation, the exact rendered aggregation SQL (golden literals),
// storage-versus-Builder direction mapping, diagnostics, status, and digest
// paths. These exercise the pure Go layer with recorded scan results; every
// live-PostgreSQL claim (aggregation over real rows, authorization against
// real data, races) is established by relationships_pg_test.go against real
// PostgreSQL 18, never by these stubs.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	storepostgres "github.com/calypr/loom/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// stubRelationshipStore records every scope it is asked to scan and replays
// recorded groups. It exists to exercise the pure classification path; it
// is not evidence about PostgreSQL behavior.
type stubRelationshipStore struct {
	calls  int
	scopes []RelationshipScope
	groups []RelationshipOccurrenceGroup
	err    error
}

func (s *stubRelationshipStore) ScanRelationshipOccurrences(_ context.Context, scope RelationshipScope) ([]RelationshipOccurrenceGroup, error) {
	s.calls++
	s.scopes = append(s.scopes, scope)
	if s.err != nil {
		return nil, s.err
	}
	return s.groups, nil
}

func stubStore(t *testing.T, stub *stubRelationshipStore) *Store {
	t.Helper()
	store, err := NewRelationshipEvidenceStore(stub)
	if err != nil {
		t.Fatalf("NewRelationshipEvidenceStore: %v", err)
	}
	return store
}

func TestRelationshipEvidenceStoreRequiresBoundary(t *testing.T) {
	if _, err := NewRelationshipEvidenceStore(nil); err == nil {
		t.Fatal("nil relationship store accepted")
	}
	if _, err := NewRelationshipEvidenceRepository(nil, "rel_it"); err == nil {
		t.Fatal("nil connection source accepted")
	}
	if _, err := NewRelationshipEvidenceRepository(discardConnSource{}, "Bad-Name"); err == nil {
		t.Fatal("invalid schema name accepted")
	} else if !errors.Is(err, storepostgres.ErrInvalidSchemaName) {
		t.Fatalf("invalid schema name error = %v, want ErrInvalidSchemaName", err)
	}
}

// discardConnSource is a non-nil, never-live source: validation must reject
// bad inputs before any acquire, so any call here is itself a failure.
type discardConnSource struct{}

func (discardConnSource) Acquire(context.Context) (*pgxpool.Conn, error) {
	return nil, errors.New("validation must reject before acquiring")
}

func TestRelationshipEvidenceScopeValidationRejectsBeforeQuery(t *testing.T) {
	repo, err := NewRelationshipEvidenceRepository(discardConnSource{}, "rel_it")
	if err != nil {
		t.Fatalf("NewRelationshipEvidenceRepository: %v", err)
	}
	trueValue, falseValue := true, false
	cases := []struct {
		name  string
		scope RelationshipScope
	}{
		{"blank project", RelationshipScope{Project: "  ", DatasetGeneration: "g", AuthResourcePathsUnrestricted: &trueValue}},
		{"nil unrestricted flag", RelationshipScope{Project: "p", DatasetGeneration: "g"}},
		{"unrestricted with paths", RelationshipScope{Project: "p", DatasetGeneration: "g", AuthResourcePathsUnrestricted: &trueValue, AuthResourcePaths: []string{"a"}}},
		{"blank requested type", RelationshipScope{Project: "p", DatasetGeneration: "g", AuthResourcePathsUnrestricted: &falseValue, ResourceTypes: []string{"Patient", " "}}},
	}
	for _, tc := range cases {
		_, err := repo.ScanRelationshipOccurrences(context.Background(), tc.scope)
		if !errors.Is(err, ErrInvalidRelationshipScope) {
			t.Errorf("%s: ScanRelationshipOccurrences error = %v, want ErrInvalidRelationshipScope", tc.name, err)
		}
	}
}

// TestRelationshipEvidenceAggregateSQLGoldens pins the exact statement for
// each authorization mode. A GROUP BY that silently drops the auth path, a
// COUNT(DISTINCT ...) that silently deduplicates occurrences, or a widened
// WHERE would each break these literals.
func TestRelationshipEvidenceAggregateSQLGoldens(t *testing.T) {
	const tableFQN = `"rel_it"."fhir_edge_occurrence"`
	const groupedTail = " GROUP BY auth_resource_path, source_resource_type, label, target_resource_type" +
		" ORDER BY auth_resource_path, source_resource_type, label, target_resource_type"
	trueValue, falseValue := true, false

	cases := []struct {
		name     string
		scope    RelationshipScope
		wantSQL  string
		wantArgs []any
	}{
		{
			name:     "unrestricted without type filter",
			scope:    RelationshipScope{Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: &trueValue},
			wantSQL:  `SELECT auth_resource_path, source_resource_type, label, target_resource_type, COUNT(*) AS occurrence_count FROM "rel_it"."fhir_edge_occurrence" WHERE project = $1 AND dataset_generation = $2` + groupedTail,
			wantArgs: []any{"proj", "g1"},
		},
		{
			name:     "restricted one path",
			scope:    RelationshipScope{Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: &falseValue, AuthResourcePaths: []string{"clinic/a"}},
			wantSQL:  `SELECT auth_resource_path, source_resource_type, label, target_resource_type, COUNT(*) AS occurrence_count FROM "rel_it"."fhir_edge_occurrence" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = ANY($3::text[])` + groupedTail,
			wantArgs: []any{"proj", "g1", []string{"clinic/a"}},
		},
		{
			name:     "restricted multiple paths with type filter",
			scope:    RelationshipScope{Project: "proj", DatasetGeneration: "", AuthResourcePathsUnrestricted: &falseValue, AuthResourcePaths: []string{"clinic/a", "clinic/b"}, ResourceTypes: []string{"Condition", "Patient"}},
			wantSQL:  `SELECT auth_resource_path, source_resource_type, label, target_resource_type, COUNT(*) AS occurrence_count FROM "rel_it"."fhir_edge_occurrence" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = ANY($3::text[]) AND source_resource_type = ANY($4::text[]) AND target_resource_type = ANY($4::text[])` + groupedTail,
			wantArgs: []any{"proj", "", []string{"clinic/a", "clinic/b"}, []string{"Condition", "Patient"}},
		},
		{
			name:     "unrestricted with type filter",
			scope:    RelationshipScope{Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: &trueValue, ResourceTypes: []string{"Patient"}},
			wantSQL:  `SELECT auth_resource_path, source_resource_type, label, target_resource_type, COUNT(*) AS occurrence_count FROM "rel_it"."fhir_edge_occurrence" WHERE project = $1 AND dataset_generation = $2 AND source_resource_type = ANY($3::text[]) AND target_resource_type = ANY($3::text[])` + groupedTail,
			wantArgs: []any{"proj", "g1", []string{"Patient"}},
		},
	}
	for _, tc := range cases {
		bound, err := bindRelationshipAggregate(tableFQN, tc.scope)
		if err != nil {
			t.Errorf("%s: bindRelationshipAggregate: %v", tc.name, err)
			continue
		}
		if bound.SQL != tc.wantSQL {
			t.Errorf("%s: SQL mismatch\n got: %s\nwant: %s", tc.name, bound.SQL, tc.wantSQL)
		}
		if len(bound.Args) != len(tc.wantArgs) {
			t.Errorf("%s: %d args, want %d", tc.name, len(bound.Args), len(tc.wantArgs))
			continue
		}
		for i := range bound.Args {
			if !argsEqual(bound.Args[i], tc.wantArgs[i]) {
				t.Errorf("%s: arg %d = %#v, want %#v", tc.name, i, bound.Args[i], tc.wantArgs[i])
			}
		}
	}
}

func argsEqual(a, b any) bool {
	as, aOK := a.([]string)
	bs, bOK := b.([]string)
	if aOK || bOK {
		if !aOK || !bOK || len(as) != len(bs) {
			return false
		}
		for i := range as {
			if as[i] != bs[i] {
				return false
			}
		}
		return true
	}
	return a == b
}

// TestRelationshipEvidenceScopeBindingNeverFlattensAuthPaths pins that a
// restricted multi-path read reaches the boundary with exactly the
// authorized paths and no silent fallback to unrestricted.
func TestRelationshipEvidenceScopeBindingNeverFlattensAuthPaths(t *testing.T) {
	stub := &stubRelationshipStore{}
	store := stubStore(t, stub)
	_, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project:                       "proj",
		DatasetGeneration:             " g1 ",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
		AuthResourcePaths:             []string{"clinic/a", "clinic/b"},
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	if stub.calls != 1 || len(stub.scopes) != 1 {
		t.Fatalf("scans = %d, want exactly 1", stub.calls)
	}
	got := stub.scopes[0]
	if got.Project != "proj" || got.DatasetGeneration != "g1" {
		t.Fatalf("scope = %+v, want project proj generation g1 (normalized)", got)
	}
	if got.AuthResourcePathsUnrestricted == nil || *got.AuthResourcePathsUnrestricted {
		t.Fatalf("restricted scope arrived unrestricted: %+v", got)
	}
	if strings.Join(got.AuthResourcePaths, "|") != "clinic/a|clinic/b" {
		t.Fatalf("auth paths = %#v, want exactly the two authorized paths", got.AuthResourcePaths)
	}
}

func TestRelationshipEvidenceRestrictedEmptyReadsNothing(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 3},
	}}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project:                       "proj",
		DatasetGeneration:             "g1",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	if stub.calls != 0 {
		t.Fatalf("restricted-empty read ran %d scans, want no query at all", stub.calls)
	}
	if result.Status != catalog.EvidenceEmpty || !result.Available || !result.Complete || len(result.Values) != 0 {
		t.Fatalf("restricted-empty result = %+v, want available complete empty with no values", result)
	}
	// Golden digest of the empty value set.
	if result.Digest != "sha256:74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b" {
		t.Fatalf("empty digest = %q", result.Digest)
	}
}

// TestRelationshipEvidenceLegacyCompatRuleIsShared pins the shared
// catalog.EffectiveAuthResourcePathsUnrestricted contract this adapter must
// keep with the Arango adapter: an absent flag with no paths is the legacy
// unrestricted read; an absent flag with paths is restricted to exactly
// those paths; an explicit true flag wins over stray paths and binds none.
func TestRelationshipEvidenceLegacyCompatRuleIsShared(t *testing.T) {
	// Absent flag, no paths: legacy unrestricted.
	stub := &stubRelationshipStore{}
	store := stubStore(t, stub)
	if _, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1",
	}); err != nil {
		t.Fatalf("legacy unrestricted read: %v", err)
	}
	got := stub.scopes[0]
	if got.AuthResourcePathsUnrestricted == nil || !*got.AuthResourcePathsUnrestricted || len(got.AuthResourcePaths) != 0 {
		t.Fatalf("absent flag with no paths must be unrestricted: %+v", got)
	}

	// Absent flag with paths: restricted to exactly those paths.
	stub2 := &stubRelationshipStore{}
	store2 := stubStore(t, stub2)
	if _, err := store2.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePaths: []string{"clinic/a"},
	}); err != nil {
		t.Fatalf("legacy restricted read: %v", err)
	}
	got2 := stub2.scopes[0]
	if got2.AuthResourcePathsUnrestricted == nil || *got2.AuthResourcePathsUnrestricted {
		t.Fatalf("absent flag with paths must stay restricted: %+v", got2)
	}
	if strings.Join(got2.AuthResourcePaths, "|") != "clinic/a" {
		t.Fatalf("legacy restricted paths = %#v", got2.AuthResourcePaths)
	}

	// Explicit unrestricted wins over stray paths and binds none.
	stub3 := &stubRelationshipStore{}
	store3 := stubStore(t, stub3)
	if _, err := store3.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		AuthResourcePaths:             []string{"clinic/a"},
	}); err != nil {
		t.Fatalf("explicit unrestricted read: %v", err)
	}
	got3 := stub3.scopes[0]
	if got3.AuthResourcePathsUnrestricted == nil || !*got3.AuthResourcePathsUnrestricted || len(got3.AuthResourcePaths) != 0 {
		t.Fatalf("explicit unrestricted must win over stray paths: %+v", got3)
	}
}

// TestRelationshipEvidencePreservesStorageAndBuilderDirection pins the core
// contract: the physical storage direction and the registry-derived Builder
// direction are returned separately and never overwrite each other.
func TestRelationshipEvidencePreservesStorageAndBuilderDirection(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		// Registry pair Condition|subject_Patient|Patient (and its inverse)
		// exists in the generated traversal registry, so the Builder view
		// is the inverse, physically INBOUND.
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 3},
		// Registry pair BodyStructure|patient|Patient.
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "BodyStructure", Label: "patient", StorageTargetResourceType: "Patient", OccurrenceCount: 1},
	}}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project:                       "proj",
		DatasetGeneration:             "g1",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	if len(result.Values) != 2 {
		t.Fatalf("values = %+v, want 2", result.Values)
	}
	for _, value := range result.Values {
		if value.StorageDirection != "OUTBOUND" {
			t.Errorf("storage direction = %q, want OUTBOUND", value.StorageDirection)
		}
		if value.BuilderDirection != "INBOUND" {
			t.Errorf("builder direction = %q, want INBOUND", value.BuilderDirection)
		}
		if value.StorageFromType == value.StorageToType {
			t.Errorf("endpoints collapsed: %+v", value)
		}
		if value.FromType != value.BuilderFromType || value.ToType != value.BuilderToType {
			t.Errorf("Builder-facing aliases diverge: %+v", value)
		}
		switch value.Label {
		case "subject_Patient":
			if value.StorageFromType != "Condition" || value.StorageToType != "Patient" ||
				value.BuilderFromType != "Patient" || value.BuilderToType != "Condition" || value.EdgeCount != 3 {
				t.Errorf("subject_Patient observation = %+v", value)
			}
		case "patient":
			if value.StorageFromType != "BodyStructure" || value.StorageToType != "Patient" ||
				value.BuilderFromType != "Patient" || value.BuilderToType != "BodyStructure" || value.EdgeCount != 1 {
				t.Errorf("patient observation = %+v", value)
			}
		default:
			t.Errorf("unexpected label %q", value.Label)
		}
	}
	// Golden digest over the two-observation fixture above.
	if result.Digest != "sha256:3a90f45976b58876882254602a6d9bbcc884f021a8f62cdbc468982915737083" {
		t.Fatalf("direction fixture digest = %q", result.Digest)
	}
	if result.Status != catalog.EvidenceAvailable || !result.Available || !result.Complete {
		t.Fatalf("status = %+v, want available complete", result)
	}
}

// TestRelationshipEvidenceCasingIsCanonicalizedNotDiagnosed pins the
// deliberate divergence from raw-string registry lookup: a lowercase
// concrete endpoint canonicalizes into evidence instead of being
// misdiagnosed as invalid.
func TestRelationshipEvidenceCasingIsCanonicalizedNotDiagnosed(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "condition", Label: "subject_Patient", StorageTargetResourceType: "patient", OccurrenceCount: 2},
	}}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	if len(result.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want none", result.Diagnostics)
	}
	if len(result.Values) != 1 || result.Values[0].StorageFromType != "Condition" || result.Values[0].StorageToType != "Patient" {
		t.Fatalf("values = %+v, want canonicalized Condition/Patient", result.Values)
	}
}

// TestRelationshipEvidenceDiagnosesInvalidEndpointsAndTraversals pins the
// diagnostic path: invalid endpoints and unsupported labels are excluded
// from Builder capability evidence with exact codes and messages, while the
// valid subset still makes the result available. The stored rows themselves
// are untouched — the live table witness is in the PostgreSQL tests.
func TestRelationshipEvidenceDiagnosesInvalidEndpointsAndTraversals(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "NotAThing", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 4},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Resource", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Patient", Label: "not_a_generated_label", StorageTargetResourceType: "Practitioner", OccurrenceCount: 7},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 2},
	}}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	if len(result.Values) != 1 || result.Values[0].StorageFromType != "Condition" {
		t.Fatalf("invalid evidence leaked into values: %+v", result.Values)
	}
	if result.Status != catalog.EvidenceAvailable || !result.Available {
		t.Fatalf("result with a valid subset must stay available: %+v", result)
	}
	if len(result.Diagnostics) != 3 {
		t.Fatalf("diagnostics = %+v, want three", result.Diagnostics)
	}
	for _, d := range result.Diagnostics {
		switch d.StorageFromType {
		case "NotAThing", "Resource":
			if d.Code != "INVALID_RELATIONSHIP_ENDPOINT" ||
				d.Message != "relationship endpoint is not a concrete generated FHIR resource" ||
				d.Label != "subject_Patient" || d.StorageToType != "Patient" ||
				(d.StorageFromType == "NotAThing" && d.Count != 4) || (d.StorageFromType == "Resource" && d.Count != 1) {
				t.Errorf("endpoint diagnostic = %+v", d)
			}
		case "Patient":
			if d.Code != "UNSUPPORTED_GENERATED_TRAVERSAL" ||
				d.Message != "relationship endpoints and label are not a supported generated traversal" ||
				d.Label != "not_a_generated_label" || d.StorageToType != "Practitioner" || d.Count != 7 {
				t.Errorf("traversal diagnostic = %+v", d)
			}
		default:
			t.Errorf("unexpected diagnostic %q", d.StorageFromType)
		}
	}
}

// TestRelationshipEvidenceNonPositiveCountIsDiagnosed pins the defensive
// parity branch of relationshipObservation. A real COUNT(*) is never
// non-positive (the repository refuses such groups); the stub reaches the
// branch a corrupted catalog document would take on the Arango path.
func TestRelationshipEvidenceNonPositiveCountIsDiagnosed(t *testing.T) {
	observation, ok, diagnostic := relationshipObservation("proj", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 0)
	if ok || observation.Label != "" {
		t.Fatalf("zero count accepted: %+v", observation)
	}
	if diagnostic.Code != "INVALID_RELATIONSHIP_COUNT" ||
		diagnostic.Message != "relationship evidence count must be positive" ||
		diagnostic.StorageFromType != "Condition" || diagnostic.StorageToType != "Patient" || diagnostic.Label != "subject_Patient" {
		t.Fatalf("count diagnostic = %+v", diagnostic)
	}
}

// TestRelationshipEvidenceRepeatedOccurrencesAndDirectionsStaySeparate pins
// two non-merging guarantees of the observation key: aggregates for
// different auth paths never sum together, and opposite storage directions
// under one label stay two observations.
func TestRelationshipEvidenceRepeatedOccurrencesAndDirectionsStaySeparate(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 2},
		{AuthResourcePath: "clinic/b", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 5},
		// Same label, opposite storage direction: its Builder projection is
		// the inverse — a separate key, never summed with the first.
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Patient", Label: "subject_Patient", StorageTargetResourceType: "Condition", OccurrenceCount: 3},
	}}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	if len(result.Values) != 3 {
		t.Fatalf("values = %+v, want three distinct observations", result.Values)
	}
	seen := map[string]int64{}
	for _, v := range result.Values {
		seen[v.AuthResourcePath+"|"+v.StorageFromType+"->"+v.StorageToType] = v.EdgeCount
	}
	if seen["clinic/a|Condition->Patient"] != 2 || seen["clinic/b|Condition->Patient"] != 5 || seen["clinic/a|Patient->Condition"] != 3 {
		t.Fatalf("cross-scope or cross-direction sum detected: %#v", seen)
	}
	// Values arrive ordered by the shared comparator.
	for i := 1; i < len(result.Values); i++ {
		if relationshipEvidenceLess(result.Values[i], result.Values[i-1]) {
			t.Fatalf("values not sorted at %d: %+v", i, result.Values)
		}
	}
}

func TestRelationshipEvidenceUnavailablePropagatesError(t *testing.T) {
	cause := errors.New("synthetic scan failure")
	stub := &stubRelationshipStore{err: cause}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err == nil || !errors.Is(err, cause) {
		t.Fatalf("error = %v, want wrapped scan failure", err)
	}
	if result.Available || result.Complete || result.Status != catalog.EvidenceUnavailable || len(result.Values) != 0 {
		t.Fatalf("unavailable result = %+v", result)
	}
	found := false
	for _, d := range result.Diagnostics {
		if d.Code == "RELATIONSHIP_EVIDENCE_UNAVAILABLE" && strings.Contains(d.Message, "synthetic scan failure") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing unavailable diagnostic: %+v", result.Diagnostics)
	}
}

func TestRelationshipEvidenceTypeRequestCanonicalizesAndDiagnoses(t *testing.T) {
	stub := &stubRelationshipStore{}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		ResourceTypes: []string{"condition", "NotAThing", "Condition"},
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	got := stub.scopes[0]
	if got.ResourceTypes == nil || len(got.ResourceTypes) != 1 || got.ResourceTypes[0] != "Condition" {
		t.Fatalf("bound types = %#v, want exactly [Condition]", got.ResourceTypes)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "INVALID_RESOURCE_TYPE" || result.Diagnostics[0].ResourceType != "NotAThing" {
		t.Fatalf("diagnostics = %+v, want one INVALID_RESOURCE_TYPE for NotAThing", result.Diagnostics)
	}
	// An all-invalid request binds an empty (non-nil) list: nothing matches,
	// matching the Arango adapter's IN [] behavior.
	stub2 := &stubRelationshipStore{}
	store2 := stubStore(t, stub2)
	if _, err := store2.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		ResourceTypes: []string{"NotAThing"},
	}); err != nil {
		t.Fatalf("all-invalid request: %v", err)
	}
	if stub2.scopes[0].ResourceTypes == nil || len(stub2.scopes[0].ResourceTypes) != 0 {
		t.Fatalf("all-invalid request must bind an empty non-nil list, got %#v", stub2.scopes[0].ResourceTypes)
	}
}
