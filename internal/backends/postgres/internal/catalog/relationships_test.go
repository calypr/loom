package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

func stubStore(t *testing.T, stub *stubRelationshipStore) *RelationshipAdapter {
	t.Helper()
	store, err := NewRelationshipAdapter(stub)
	if err != nil {
		t.Fatalf("NewRelationshipAdapter: %v", err)
	}
	return store
}

func TestRelationshipEvidenceStoreRequiresBoundary(t *testing.T) {
	if _, err := NewRelationshipAdapter(nil); err == nil {
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

type discardConnSource struct{}

func (discardConnSource) Acquire(context.Context) (*pgxpool.Conn, error) {
	return nil, errors.New("validation must reject before acquiring")
}

func TestRelationshipEvidenceScopeValidationRejectsBeforeQuery(t *testing.T) {
	repo, err := NewRelationshipEvidenceRepository(discardConnSource{}, "rel_it")
	if err != nil {
		t.Fatalf("NewRelationshipEvidenceRepository: %v", err)
	}
	trueValue := true
	cases := []struct {
		name  string
		scope RelationshipScope
	}{
		{"blank project", RelationshipScope{Project: "  ", DatasetGeneration: "g", AuthResourcePathsUnrestricted: &trueValue}},
		{"nil unrestricted flag", RelationshipScope{Project: "p", DatasetGeneration: "g"}},
		{"unrestricted with paths", RelationshipScope{Project: "p", DatasetGeneration: "g", AuthResourcePathsUnrestricted: &trueValue, AuthResourcePaths: []string{"a"}}},
	}
	for _, tc := range cases {
		_, err := repo.ScanRelationshipOccurrences(context.Background(), tc.scope)
		if !errors.Is(err, ErrInvalidRelationshipScope) {
			t.Errorf("%s: ScanRelationshipOccurrences error = %v, want ErrInvalidRelationshipScope", tc.name, err)
		}
	}
}

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
			name:     "restricted multiple paths",
			scope:    RelationshipScope{Project: "proj", DatasetGeneration: "", AuthResourcePathsUnrestricted: &falseValue, AuthResourcePaths: []string{"clinic/a", "clinic/b"}},
			wantSQL:  `SELECT auth_resource_path, source_resource_type, label, target_resource_type, COUNT(*) AS occurrence_count FROM "rel_it"."fhir_edge_occurrence" WHERE project = $1 AND dataset_generation = $2 AND auth_resource_path = ANY($3::text[])` + groupedTail,
			wantArgs: []any{"proj", "", []string{"clinic/a", "clinic/b"}},
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
	if result.Digest != "sha256:74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b" {
		t.Fatalf("empty digest = %q", result.Digest)
	}
}

func TestRelationshipEvidenceLegacyCompatRuleIsShared(t *testing.T) {
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

func TestRelationshipEvidencePreservesStorageAndBuilderDirection(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 3},
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
	if result.Digest != "sha256:3a90f45976b58876882254602a6d9bbcc884f021a8f62cdbc468982915737083" {
		t.Fatalf("direction fixture digest = %q", result.Digest)
	}
	if result.Status != catalog.EvidenceAvailable || !result.Available || !result.Complete {
		t.Fatalf("status = %+v, want available complete", result)
	}
}

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

func TestRelationshipEvidenceNonPositiveCountIsDiagnosed(t *testing.T) {
	observation, ok, diagnostic := catalog.ProjectRelationshipObservation("proj", "g1", "clinic/a", "Condition", "subject_Patient", "Patient", 0)
	if ok || observation.Label != "" {
		t.Fatalf("zero count accepted: %+v", observation)
	}
	if diagnostic.Code != "INVALID_RELATIONSHIP_COUNT" ||
		diagnostic.Message != "relationship evidence count must be positive" ||
		diagnostic.StorageFromType != "Condition" || diagnostic.StorageToType != "Patient" || diagnostic.Label != "subject_Patient" {
		t.Fatalf("count diagnostic = %+v", diagnostic)
	}
}

func TestRelationshipEvidenceRepeatedOccurrencesAndDirectionsStaySeparate(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 2},
		{AuthResourcePath: "clinic/b", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 5},
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
	for i := 1; i < len(result.Values); i++ {
		if catalog.RelationshipEvidenceLess(result.Values[i], result.Values[i-1]) {
			t.Fatalf("values not sorted at %d: %+v", i, result.Values)
		}
	}
}

func TestRelationshipEvidenceReadAliasDelegates(t *testing.T) {
	stub := &stubRelationshipStore{groups: []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 1},
	}}
	store := stubStore(t, stub)
	opts := catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	}
	direct, err := store.DiscoverRelationshipObservations(context.Background(), opts)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	aliased, err := store.ReadRelationshipObservations(context.Background(), opts)
	if err != nil {
		t.Fatalf("alias: %v", err)
	}
	if stub.calls != 2 {
		t.Fatalf("alias must delegate a real read, calls = %d", stub.calls)
	}
	if direct.Digest != aliased.Digest || len(aliased.Values) != 1 || aliased.Values[0].EdgeCount != 1 {
		t.Fatalf("alias result diverges: %q vs %q", direct.Digest, aliased.Digest)
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

func variantGroups() []RelationshipOccurrenceGroup {
	return []RelationshipOccurrenceGroup{
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "condition", Label: "subject_Patient", StorageTargetResourceType: "PatIent", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition\t", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "\rCondition", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "Condition\u00a0", Label: "subject_Patient", StorageTargetResourceType: "\u00a0Patient", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: " \tCondition\u00a0", Label: "subject_Patient", StorageTargetResourceType: "\nPatient", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "BodyStructure", Label: "patient", StorageTargetResourceType: "Patient", OccurrenceCount: 1},
		{AuthResourcePath: "clinic/a", StorageSourceResourceType: "NotAThing", Label: "subject_Patient", StorageTargetResourceType: "Patient", OccurrenceCount: 9},
	}
}

func TestRelationshipEvidenceTypeRequestCanonicalizesAndDiagnoses(t *testing.T) {
	stub := &stubRelationshipStore{groups: variantGroups()}
	store := stubStore(t, stub)
	result, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		ResourceTypes: []string{"condition", "Patient", "NotAThing"},
	})
	if err != nil {
		t.Fatalf("DiscoverRelationshipObservations: %v", err)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "INVALID_RESOURCE_TYPE" || result.Diagnostics[0].ResourceType != "NotAThing" {
		t.Fatalf("diagnostics = %+v, want one INVALID_RESOURCE_TYPE for NotAThing", result.Diagnostics)
	}
	if len(result.Values) != 1 || result.Values[0].StorageFromType != "Condition" || result.Values[0].EdgeCount != 6 {
		t.Fatalf("filtered values = %+v, want the six canonical Condition->Patient variants summed, with the BodyStructure and NotAThing groups silently out", result.Values)
	}

	unfiltered, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err != nil {
		t.Fatalf("unfiltered: %v", err)
	}
	full, err := store.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		ResourceTypes: []string{"Condition", "Patient", "BodyStructure"},
	})
	if err != nil {
		t.Fatalf("full-set filtered: %v", err)
	}
	if full.Digest != unfiltered.Digest || len(full.Values) != 2 {
		t.Fatalf("filtered full-set digest %q != unfiltered %q (values %v)", full.Digest, unfiltered.Digest, full.Values)
	}

	stub2 := &stubRelationshipStore{groups: variantGroups()}
	store2 := stubStore(t, stub2)
	emptyResult, err := store2.DiscoverRelationshipObservations(context.Background(), catalog.RelationshipObservationOptions{
		Project: "proj", DatasetGeneration: "g1", AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		ResourceTypes: []string{"NotAThing"},
	})
	if err != nil {
		t.Fatalf("all-invalid request: %v", err)
	}
	if stub2.calls != 1 {
		t.Fatalf("all-invalid request must still scan once, calls = %d", stub2.calls)
	}
	if len(emptyResult.Values) != 0 || emptyResult.Status != catalog.EvidenceEmpty {
		t.Fatalf("all-invalid result = %+v, want empty", emptyResult)
	}
	for _, d := range emptyResult.Diagnostics {
		if d.Code == "INVALID_RELATIONSHIP_ENDPOINT" {
			t.Fatalf("requested read must not diagnose rows outside the set: %+v", d)
		}
	}
}
