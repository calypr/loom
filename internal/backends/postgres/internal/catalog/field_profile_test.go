package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgcat01NamePattern = regexp.MustCompile(`^pgcat01_it_[0-9]+_[0-9a-f]+$`)

type fieldProfileService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func fieldProfileTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func fieldProfileRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func fieldProfileScrubber(raw string) func(any) string {
	secrets := []string{raw}
	if u, err := url.Parse(raw); err == nil && u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	return func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
}

func newFieldProfileService(t *testing.T) *fieldProfileService {
	t.Helper()
	raw := fieldProfileTestPostgresURL(t)

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}

	var secrets []string
	secrets = append(secrets, raw)
	if u.User != nil {
		secrets = append(secrets, u.User.String())
		if pw, ok := u.User.Password(); ok && pw != "" {
			secrets = append(secrets, pw)
		}
	}
	scrub := func(v any) string {
		s := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				s = strings.ReplaceAll(s, secret, "<redacted>")
			}
		}
		return s
	}
	fail := func(step string, err error) {
		t.Fatalf("%s: %s", step, scrub(err))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	var dbName, schema string

	t.Cleanup(func() {
		var dbCount int
		if err := admin.QueryRow(context.WithoutCancel(ctx),
			"SELECT COUNT(*) FROM pg_database WHERE datname = $1", dbName).Scan(&dbCount); err != nil {
			t.Errorf("audit isolated database %s: %s", dbName, scrub(err))
		} else if dbCount != 0 {
			t.Errorf("leftover isolated database %s after cleanup", dbName)
		}
		var schemaCount int
		if err := admin.QueryRow(context.WithoutCancel(ctx),
			"SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1", schema).Scan(&schemaCount); err != nil {
			t.Errorf("audit owned schema %s: %s", schema, scrub(err))
		} else if schemaCount != 0 {
			t.Errorf("leftover owned schema %s in the service database after cleanup", schema)
		}
	})

	dbName = fmt.Sprintf("pgcat01_it_%d_%s", time.Now().UnixNano(), fieldProfileRandomHex(t))
	if !pgcat01NamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
		u.RawQuery = q.Encode()
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	t.Cleanup(iso.Close)

	schema = fmt.Sprintf("pgcat01_it_%d_%s", time.Now().UnixNano(), fieldProfileRandomHex(t))
	if !pgcat01NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	service := &fieldProfileService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
	if _, err := storepostgres.MigrateSchema(ctx, service.openClient(t), schema); err != nil {
		fail("MigrateSchema", err)
	}
	return service
}

func (s *fieldProfileService) openClient(t *testing.T) *storepostgres.Client {
	t.Helper()
	client, err := storepostgres.Open(s.ctx, storepostgres.Config{
		DSN:            s.dsn,
		MaxConns:       4,
		MinConns:       0,
		AcquireTimeout: 10 * time.Second,
	})
	if err != nil {
		s.fail("open client pool", err)
	}
	t.Cleanup(client.Close)
	return client
}

func (s *fieldProfileService) realRepository(t *testing.T) *storepostgres.FieldProfileRepository {
	t.Helper()
	repo, err := storepostgres.NewFieldProfileRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new field profile repository", err)
	}
	return repo
}

func (s *fieldProfileService) adapter(t *testing.T) *Store {
	t.Helper()
	store, err := NewFieldProfileStore(s.realRepository(t))
	if err != nil {
		s.fail("new field profile store", err)
	}
	return store
}

func (s *fieldProfileService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *fieldProfileService) profileFQN() string {
	return pgx.Identifier{s.schema, "fhir_field_profile"}.Sanitize()
}

func (s *fieldProfileService) coverageFQN() string {
	return pgx.Identifier{s.schema, "fhir_field_profile_coverage"}.Sanitize()
}

type profileRowWitness struct {
	Path                string
	RecordType          string
	Kind                string
	ProfileKey          string
	DocCount            int64
	MaxItems            int64
	SampleCount         int64
	DistinctTruncated   bool
	EnrichmentTruncated bool
	EnrichmentComplete  *bool
	DistinctLength      int
	ExtensionLength     int
	SemanticLength      int
}

func (s *fieldProfileService) witnessRows(t *testing.T, project, generation string, args ...any) []profileRowWitness {
	t.Helper()
	all := append([]any{project, generation}, args...)
	rows, err := s.iso.Query(s.ctx, "SELECT path, record_type, kind, profile_key, doc_count, max_items, sample_count,"+
		" distinct_truncated, enrichment_truncated, enrichment_complete,"+
		" COALESCE(array_length(distinct_values, 1), 0),"+
		" COALESCE(jsonb_array_length(extension_values), 0),"+
		" COALESCE(jsonb_array_length(semantic_observations), 0)"+
		" FROM "+s.profileFQN()+" WHERE project = $1 AND dataset_generation = $2"+argsSQL(args), all...)
	if err != nil {
		s.fail("witness rows", err)
	}
	defer rows.Close()
	var out []profileRowWitness
	for rows.Next() {
		var w profileRowWitness
		if err := rows.Scan(&w.Path, &w.RecordType, &w.Kind, &w.ProfileKey, &w.DocCount, &w.MaxItems, &w.SampleCount,
			&w.DistinctTruncated, &w.EnrichmentTruncated, &w.EnrichmentComplete,
			&w.DistinctLength, &w.ExtensionLength, &w.SemanticLength); err != nil {
			s.fail("witness rows", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness rows", err)
	}
	return out
}

func argsSQL(args []any) string {
	var b strings.Builder
	next := 3
	for range args {
		switch next {
		case 3:
			b.WriteString(" AND auth_resource_path = $" + fmt.Sprint(next))
		case 4:
			b.WriteString(" AND resource_type = $" + fmt.Sprint(next))
		}
		next++
	}
	return b.String()
}

type coverageWitness struct {
	AuthResourcePath    string
	ResourceType        string
	EnrichmentIsNull    bool
	EnrichmentComplete  *bool
	EnrichmentTruncated bool
}

func (s *fieldProfileService) witnessCoverage(t *testing.T, project, generation string) []coverageWitness {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "SELECT auth_resource_path, resource_type, enrichment_complete IS NULL,"+
		" enrichment_complete, enrichment_truncated FROM "+s.coverageFQN()+
		" WHERE project = $1 AND dataset_generation = $2 ORDER BY auth_resource_path, resource_type", project, generation)
	if err != nil {
		s.fail("witness coverage", err)
	}
	defer rows.Close()
	var out []coverageWitness
	for rows.Next() {
		var w coverageWitness
		if err := rows.Scan(&w.AuthResourcePath, &w.ResourceType, &w.EnrichmentIsNull, &w.EnrichmentComplete, &w.EnrichmentTruncated); err != nil {
			s.fail("witness coverage", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness coverage", err)
	}
	return out
}

func (s *fieldProfileService) witnessRowCount(t *testing.T, project string) int {
	t.Helper()
	var count int
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.profileFQN()+" WHERE project = $1", project).Scan(&count); err != nil {
		s.fail("witness row count", err)
	}
	return count
}

func (s *fieldProfileService) countJSONB(t *testing.T, project, resourceType, path, column, predicate string, args ...any) int {
	t.Helper()
	sql := "SELECT COUNT(*) FROM " + s.profileFQN() + ", jsonb_array_elements(" + column + ") e" +
		" WHERE project = $1 AND resource_type = $2"
	all := []any{project, resourceType}
	if path != "*" {
		sql += " AND path = $3"
		all = append(all, path)
	}
	sql += " AND " + predicate
	all = append(all, args...)
	var count int
	if err := s.iso.QueryRow(s.ctx, sql, all...).Scan(&count); err != nil {
		s.fail("witness jsonb elements", err)
	}
	return count
}

func fieldProfileScope(project, generation, authPath, resourceType string) storepostgres.FieldProfileScope {
	return storepostgres.FieldProfileScope{Project: project, DatasetGeneration: generation, AuthResourcePath: authPath, ResourceType: resourceType}
}

func patientPayload(gender, birthDate string) map[string]any {
	return map[string]any{
		"resourceType": "Patient",
		"gender":       gender,
		"active":       true,
		"birthDate":    birthDate,
		"name": []any{
			map[string]any{"family": "Smith", "given": []any{"Ann", "Q"}},
			map[string]any{"family": "Ö’brien‑X"},
		},
		"extension": []any{
			map[string]any{"url": "urn:ext:a", "valueString": "alpha"},
			map[string]any{"url": "urn:ext:b", "valueInteger": 7},
		},
	}
}

func observationPayload() map[string]any {
	return map[string]any{
		"resourceType": "Observation",
		"status":       "final",
		"code": map[string]any{
			"coding": []any{map[string]any{"system": "urn:root", "code": "root-code", "display": "Root label"}},
			"text":   "Root text",
		},
		"valueQuantity": map[string]any{"value": 42.5, "unit": "kg"},
		"component": []any{
			map[string]any{
				"code": map[string]any{"coding": []any{
					map[string]any{"system": "urn:study:A", "code": "shared"},
					map[string]any{"system": "urn:decoy", "code": "decoy"},
				}},
				"valueQuantity": map[string]any{"value": 111.0, "unit": "cm"},
			},
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:study:B", "code": "shared"}}},
				"valueQuantity": map[string]any{"value": 222.0, "unit": "cm"},
			},
		},
		"extension": []any{map[string]any{
			"url":       "parent",
			"extension": []any{map[string]any{"url": "leaf", "valueString": "left-only"}},
		}},
	}
}

func conditionPayload(label string) map[string]any {
	return map[string]any{
		"resourceType": "Condition",
		"code":         map[string]any{"text": label},
		"subject":      map[string]any{"reference": "Patient/p1"},
	}
}

func profilerDocuments(t *testing.T, project, generation, authPath, resourceType string, limits catalog.ProfileLimits, payloads ...map[string]any) []catalog.FieldCatalogDocument {
	t.Helper()
	profiler := catalog.NewProfilerForGenerationWithLimits(project, generation, authPath, resourceType, nil, limits)
	for _, payload := range payloads {
		if err := profiler.ObservePayload(payload, map[string]float64{}); err != nil {
			t.Fatalf("observe payload: %v", err)
		}
	}
	return profiler.Documents()
}

func boolPointer(v bool) *bool { return &v }

func TestFieldProfileProfilerRoundTripPreservesAllPublicFields(t *testing.T) {
	s := newFieldProfileService(t)
	store := s.adapter(t)

	patientScope := fieldProfileScope("pgcat01-round", "g1", "tenant/a", "Patient")
	patientDocs := profilerDocuments(t, "pgcat01-round", "g1", "tenant/a", "Patient", catalog.DefaultProfileLimits(),
		patientPayload("male", "2001-02-03"), patientPayload("female", "1999-12-31"))
	if len(patientDocs) == 0 {
		t.Fatal("profiler emitted no documents")
	}
	if err := store.WriteFieldProfile(s.ctx, patientScope, patientDocs, 2); err != nil {
		t.Fatalf("write patient profile: %v", err)
	}
	got, err := store.ReadFieldProfile(s.ctx, patientScope)
	if err != nil {
		t.Fatalf("read patient profile: %v", err)
	}
	if !reflect.DeepEqual(got, patientDocs) {
		t.Fatalf("patient profile round-trip drifted\n got=%#v\nwant=%#v", got, patientDocs)
	}

	obsScope := fieldProfileScope("pgcat01-round", "g1", "tenant/a", "Observation")
	obsDocs := profilerDocuments(t, "pgcat01-round", "g1", "tenant/a", "Observation", catalog.DefaultProfileLimits(), observationPayload())
	if err := store.WriteFieldProfile(s.ctx, obsScope, obsDocs, 0); err != nil {
		t.Fatalf("write observation profile: %v", err)
	}
	gotObs, err := store.ReadFieldProfile(s.ctx, obsScope)
	if err != nil {
		t.Fatalf("read observation profile: %v", err)
	}
	if !reflect.DeepEqual(gotObs, obsDocs) {
		t.Fatalf("observation profile round-trip drifted\n got=%#v\nwant=%#v", gotObs, obsDocs)
	}

	if n := s.countJSONB(t, "pgcat01-round", "Observation", "component[]", "semantic_observations",
		"e->'key'->>'system' = $4 AND e->'examples' @> $5::jsonb", "urn:study:A", `["111"]`); n != 1 {
		t.Fatalf("A↔111 correlated elements = %d, want exactly 1", n)
	}
	if n := s.countJSONB(t, "pgcat01-round", "Observation", "component[]", "semantic_observations",
		"e->'key'->>'system' = $4 AND e->'examples' @> $5::jsonb", "urn:study:B", `["222"]`); n != 1 {
		t.Fatalf("B↔222 correlated elements = %d, want exactly 1", n)
	}
	if n := s.countJSONB(t, "pgcat01-round", "Observation", "component[]", "semantic_observations",
		"(e->'key'->>'system' = $4 AND e->'examples' @> $5::jsonb) OR (e->'key'->>'system' = $6 AND e->'examples' @> $7::jsonb)",
		"urn:study:A", `["222"]`, "urn:study:B", `["111"]`); n != 0 {
		t.Fatalf("cross-combined semantic elements = %d, want 0 (key and value must stay correlated)", n)
	}
	if n := s.countJSONB(t, "pgcat01-round", "Patient", "extension[].url", "extension_values",
		"e->>'url' = $4 AND e->>'value_path' = $5 AND e->>'value_type' = $6", "urn:ext:a", "valueString", "string"); n != 1 {
		t.Fatalf("urn:ext:a→valueString elements = %d, want exactly 1", n)
	}
	if n := s.countJSONB(t, "pgcat01-round", "Patient", "extension[].url", "extension_values",
		"e->>'url' = $4 AND e->>'value_path' = $5 AND e->>'value_type' = $6", "urn:ext:b", "valueInteger", "integer"); n != 1 {
		t.Fatalf("urn:ext:b→valueInteger elements = %d, want exactly 1", n)
	}
	if n := s.countJSONB(t, "pgcat01-round", "Patient", "extension[].url", "extension_values",
		"(e->>'url' = $4 AND e->>'value_path' = $5) OR (e->>'url' = $6 AND e->>'value_path' = $7)",
		"urn:ext:a", "valueInteger", "urn:ext:b", "valueString"); n != 0 {
		t.Fatalf("cross-combined extension elements = %d, want 0 (URL and value[x] must stay correlated)", n)
	}
	if n := s.countJSONB(t, "pgcat01-round", "Observation", "*", "semantic_observations",
		"e->'extension_url_path' @> $3::jsonb AND e->'examples' @> $4::jsonb", `["parent","leaf"]`, `["left-only"]`); n == 0 {
		t.Fatalf("nested extension ancestry observation not persisted as one correlated object")
	}

	truncLimits := catalog.ProfileLimits{MaxDistinctValuesPerField: 2, MaxDistinctValueBytes: 64, MaxFields: 3, MaxPivotColumnsPerField: 8, MaxExtensionValuesPerField: 8, MaxShapePlans: 8, MaxRetainedBytes: 4096}
	truncScope := fieldProfileScope("pgcat01-round", "g1", "tenant/a", "Condition")
	truncDocs := profilerDocuments(t, "pgcat01-round", "g1", "tenant/a", "Condition", truncLimits,
		conditionPayload("Angina"), conditionPayload("Sepsis"), conditionPayload("Stroke"))
	sawDistinctTruncated, sawEnrichmentTruncated := false, false
	for _, doc := range truncDocs {
		sawDistinctTruncated = sawDistinctTruncated || doc.DistinctTruncated
		sawEnrichmentTruncated = sawEnrichmentTruncated || doc.EnrichmentTruncated
	}
	if !sawDistinctTruncated || !sawEnrichmentTruncated {
		t.Fatalf("truncated fixture lost its truncation signal before persisting: distinct=%v enrichment=%v docs=%#v",
			sawDistinctTruncated, sawEnrichmentTruncated, truncDocs)
	}
	if err := store.WriteFieldProfile(s.ctx, truncScope, truncDocs, 0); err != nil {
		t.Fatalf("write truncated profile: %v", err)
	}
	gotTrunc, err := store.ReadFieldProfile(s.ctx, truncScope)
	if err != nil {
		t.Fatalf("read truncated profile: %v", err)
	}
	if !reflect.DeepEqual(gotTrunc, truncDocs) {
		t.Fatalf("truncated profile round-trip drifted\n got=%#v\nwant=%#v", gotTrunc, truncDocs)
	}
	for _, w := range s.witnessRows(t, "pgcat01-round", "g1", "tenant/a", "Condition") {
		if !w.EnrichmentTruncated || w.EnrichmentComplete == nil || *w.EnrichmentComplete {
			t.Fatalf("witness truncated row %q kept clean flags: %+v", w.Path, w)
		}
	}

	handScope := fieldProfileScope("pgcat01-round", "g1", "tenant/a", "Practitioner")
	handKey := "handcraft-practitioner-active"
	handDocs := []catalog.FieldCatalogDocument{{
		Key:                 handKey,
		Project:             "pgcat01-round",
		DatasetGeneration:   "g1",
		AuthResourcePath:    "tenant/a",
		ResourceType:        "Practitioner",
		Path:                "active",
		Kind:                "scalar",
		DocCount:            0,
		MaxItems:            0,
		SampleCount:         0,
		DistinctTruncated:   true,
		EnrichmentTruncated: false,
		PivotCandidate:      false,
		ExtensionValues: []catalog.ExtensionValueObservation{{
			URL: "urn:hand", SourcePath: "extension[]", ValuePath: "valueBoolean", ValueType: "boolean",
			URLPath: []string{"parent-url"},
		}},
		SemanticObservations: []catalog.SemanticObservation{{
			SchemaVersion: 4,
			Source:        catalog.SemanticObservationSource{Canonical: "urn:hand#active"},
			Key:           catalog.SemanticObservationKey{Selector: "active", System: "urn:sys"},
			Value: catalog.SemanticObservationValue{
				Selector: "active", Type: "boolean",
				FallbackSelectors:     []string{"active.extension"},
				FallbackOnBlankString: true,
			},
			ObservedUnits:          []string{"unit-1"},
			ObservedUnitsTruncated: true,
			Completeness:           catalog.SemanticIncomplete,
			Status:                 "SUPPORTED",
			Population:             3,
			Examples:               []string{"true", "false"},
			ExamplesTruncated:      true,
			RuleHint:               catalog.SemanticRuleHintCodedValueV1,
			RuleVersion:            "9",
		}},
	}}
	if err := store.WriteFieldProfile(s.ctx, handScope, handDocs, 0); err != nil {
		t.Fatalf("write hand-built profile: %v", err)
	}
	gotHand, err := store.ReadFieldProfile(s.ctx, handScope)
	if err != nil {
		t.Fatalf("read hand-built profile: %v", err)
	}
	if !reflect.DeepEqual(gotHand, handDocs) {
		t.Fatalf("hand-built profile round-trip drifted\n got=%#v\nwant=%#v", gotHand, handDocs)
	}
	handWitnesses := s.witnessRows(t, "pgcat01-round", "g1", "tenant/a", "Practitioner")
	if len(handWitnesses) != 1 {
		t.Fatalf("hand-built witnesses = %v, want one row", handWitnesses)
	}
	if handWitnesses[0].EnrichmentComplete != nil {
		t.Fatalf("missing completeness claim was coerced: %+v", handWitnesses[0])
	}
	if !handWitnesses[0].DistinctTruncated || handWitnesses[0].EnrichmentTruncated {
		t.Fatalf("false/empty booleans did not round-trip literally: %+v", handWitnesses[0])
	}
	if handWitnesses[0].SampleCount != 0 || handWitnesses[0].DistinctLength != 0 {
		t.Fatalf("zero counts did not round-trip literally: %+v", handWitnesses[0])
	}
	if gotHand[0].SemanticObservations[0].ExamplesTruncated != true || gotHand[0].SemanticObservations[0].ObservedUnitsTruncated != true {
		t.Fatalf("bounded-example truncation metadata lost: %#v", gotHand[0].SemanticObservations[0])
	}

	for _, w := range s.witnessRows(t, "pgcat01-round", "g1", "tenant/a", "Patient") {
		if w.Path != "gender" {
			continue
		}
		if w.Kind != "scalar" || w.DocCount != 2 || w.SampleCount != 2 || w.DistinctLength != 2 ||
			w.DistinctTruncated || w.EnrichmentTruncated || w.EnrichmentComplete == nil || !*w.EnrichmentComplete {
			t.Fatalf("gender witness = %+v, want exact literal row", w)
		}
		if !strings.HasPrefix(w.ProfileKey, "gfc_") {
			t.Fatalf("generation-qualified profile key %q lost its gfc_ identity", w.ProfileKey)
		}
	}
}

func TestFieldProfileUpsertReplacesExactIdentityOnly(t *testing.T) {
	s := newFieldProfileService(t)
	store := s.adapter(t)

	type scopeSnapshot struct {
		scope storepostgres.FieldProfileScope
		docs  []catalog.FieldCatalogDocument
	}
	scopes := []storepostgres.FieldProfileScope{
		fieldProfileScope("pgcat01-up", "g1", "a", "Patient"),
		fieldProfileScope("pgcat01-up", "g1", "b", "Patient"),
		fieldProfileScope("pgcat01-up", "g2", "a", "Patient"),
		fieldProfileScope("pgcat01-up", "g1", "a", "Observation"),
		fieldProfileScope("pgcat02-neighbor", "g1", "a", "Patient"),
	}
	var snapshots []scopeSnapshot
	for _, scope := range scopes {
		docs := profilerDocuments(t, scope.Project, scope.DatasetGeneration, scope.AuthResourcePath, scope.ResourceType,
			catalog.DefaultProfileLimits(), patientPayload("male", "2001-02-03"))
		if err := store.WriteFieldProfile(s.ctx, scope, docs, 0); err != nil {
			t.Fatalf("seed %v: %v", scope, err)
		}
		got, err := store.ReadFieldProfile(s.ctx, scope)
		if err != nil {
			t.Fatalf("seed read %v: %v", scope, err)
		}
		if !reflect.DeepEqual(got, docs) {
			t.Fatalf("seed read drifted for %v", scope)
		}
		snapshots = append(snapshots, scopeSnapshot{scope: scope, docs: got})
	}
	totalBefore := 0
	for _, project := range []string{"pgcat01-up", "pgcat02-neighbor"} {
		totalBefore += s.witnessRowCount(t, project)
	}

	replacement := []catalog.FieldCatalogDocument{{
		Key:                 "gfc_replaced_gender",
		Project:             "pgcat01-up",
		DatasetGeneration:   "g1",
		AuthResourcePath:    "a",
		ResourceType:        "Patient",
		Path:                "gender",
		Kind:                "codeable_concept",
		DocCount:            42,
		MaxItems:            7,
		SampleCount:         1,
		DistinctValues:      []string{"zzz-replaced"},
		DistinctTruncated:   true,
		EnrichmentComplete:  boolPointer(false),
		EnrichmentTruncated: true,
		PivotCandidate:      true,
		PivotKind:           "codeable_concept_display_value",
		PivotColumns:        []string{"replaced-column"},
	}}
	if err := store.WriteFieldProfile(s.ctx, scopes[0], replacement, 0); err != nil {
		t.Fatalf("replace identity: %v", err)
	}
	got, err := store.ReadFieldProfile(s.ctx, scopes[0])
	if err != nil {
		t.Fatalf("read replaced scope: %v", err)
	}
	var gender *catalog.FieldCatalogDocument
	for i := range got {
		if got[i].Path == "gender" {
			gender = &got[i]
		}
	}
	if gender == nil {
		t.Fatalf("replaced identity vanished: %#v", got)
	}
	if gender.Kind != "codeable_concept" || gender.DocCount != 42 || gender.MaxItems != 7 ||
		gender.SampleCount != 1 || !reflect.DeepEqual(gender.DistinctValues, []string{"zzz-replaced"}) ||
		!gender.DistinctTruncated || gender.EnrichmentComplete == nil || *gender.EnrichmentComplete ||
		!gender.EnrichmentTruncated || !gender.PivotCandidate ||
		!reflect.DeepEqual(gender.PivotColumns, []string{"replaced-column"}) || gender.Key != "gfc_replaced_gender" {
		t.Fatalf("replacement did not fully overwrite the identity: %#v", gender)
	}
	witnesses := s.witnessRows(t, "pgcat01-up", "g1", "a", "Patient")
	genderRows := 0
	for _, w := range witnesses {
		if w.Path == "gender" {
			genderRows++
		}
	}
	if genderRows != 1 {
		t.Fatalf("upsert left %d rows for the replaced identity, want exactly 1", genderRows)
	}

	for i := 1; i < len(snapshots); i++ {
		again, err := store.ReadFieldProfile(s.ctx, snapshots[i].scope)
		if err != nil {
			t.Fatalf("re-read %v: %v", snapshots[i].scope, err)
		}
		if !reflect.DeepEqual(again, snapshots[i].docs) {
			t.Fatalf("sibling identity %v was disturbed by the upsert\n got=%#v\nwant=%#v", snapshots[i].scope, again, snapshots[i].docs)
		}
	}
	totalAfter := 0
	for _, project := range []string{"pgcat01-up", "pgcat02-neighbor"} {
		totalAfter += s.witnessRowCount(t, project)
	}
	if totalAfter != totalBefore {
		t.Fatalf("row count moved %d → %d; upsert must only replace, never append or drop", totalBefore, totalAfter)
	}

	if err := store.WriteFieldProfile(s.ctx, scopes[0], replacement, 0); err != nil {
		t.Fatalf("re-apply replacement batch: %v", err)
	}
	again, err := store.ReadFieldProfile(s.ctx, scopes[0])
	if err != nil {
		t.Fatalf("read after re-apply: %v", err)
	}
	if !reflect.DeepEqual(again, got) {
		t.Fatalf("re-applying the identical batch changed the row\n got=%#v\nwant=%#v", again, got)
	}
}

func TestFieldProfileProfileStatesRemainDistinguishable(t *testing.T) {
	s := newFieldProfileService(t)
	store := s.adapter(t)

	completeScope := fieldProfileScope("pgcat01-complete", "g1", "a", "Observation")
	completeDocs := profilerDocuments(t, "pgcat01-complete", "g1", "a", "Observation", catalog.DefaultProfileLimits(), observationPayload())
	if err := store.WriteFieldProfile(s.ctx, completeScope, completeDocs, 0); err != nil {
		t.Fatalf("write complete: %v", err)
	}
	truncatedScope := fieldProfileScope("pgcat01-truncated", "g1", "a", "Condition")
	truncDocs := profilerDocuments(t, "pgcat01-truncated", "g1", "a", "Condition",
		catalog.ProfileLimits{MaxFields: 1, MaxDistinctValuesPerField: 64, MaxDistinctValueBytes: 64, MaxPivotColumnsPerField: 8, MaxExtensionValuesPerField: 8, MaxShapePlans: 8, MaxRetainedBytes: 4096},
		conditionPayload("Angina"))
	if err := store.WriteFieldProfile(s.ctx, truncatedScope, truncDocs, 0); err != nil {
		t.Fatalf("write truncated: %v", err)
	}
	emptyTruncatedScope := fieldProfileScope("pgcat01-empty-truncated", "g1", "a", "Condition")
	statusDocs := profilerDocuments(t, "pgcat01-empty-truncated", "g1", "a", "Condition",
		catalog.ProfileLimits{MaxFields: 64, MaxDistinctValuesPerField: 64, MaxDistinctValueBytes: 64, MaxPivotColumnsPerField: 8, MaxExtensionValuesPerField: 8, MaxShapePlans: 1, MaxRetainedBytes: 1},
		conditionPayload("Angina"))
	if len(statusDocs) != 1 || statusDocs[0].RecordType != catalog.FieldProfileStatusRecordType {
		t.Fatalf("expected exactly one profile_status record, got %#v", statusDocs)
	}
	if err := store.WriteFieldProfile(s.ctx, emptyTruncatedScope, statusDocs, 0); err != nil {
		t.Fatalf("write status: %v", err)
	}
	emptyScope := fieldProfileScope("pgcat01-empty", "g1", "a", "Observation")
	if err := store.WriteFieldProfile(s.ctx, emptyScope, nil, 0); err != nil {
		t.Fatalf("write known-empty: %v", err)
	}

	type expectation struct {
		project         string
		resourceType    string
		wantStatus      catalog.EvidenceStatus
		wantComplete    bool
		wantCoverage    bool
		wantValues      bool
		wantUnavailable bool
		unavailablePart string
	}
	cases := []expectation{
		{project: "pgcat01-complete", resourceType: "Observation", wantStatus: catalog.EvidenceAvailable, wantComplete: true, wantCoverage: true, wantValues: true},
		{project: "pgcat01-truncated", resourceType: "Condition", wantStatus: catalog.EvidenceTruncated, wantComplete: false, wantCoverage: true, wantValues: true},
		{project: "pgcat01-empty-truncated", resourceType: "Condition", wantStatus: catalog.EvidenceTruncated, wantComplete: false, wantCoverage: true, wantValues: false},
		{project: "pgcat01-empty", resourceType: "Observation", wantStatus: catalog.EvidenceEmpty, wantComplete: true, wantCoverage: true, wantValues: false},
		{project: "pgcat01-never", resourceType: "Observation", wantStatus: catalog.EvidenceUnavailable, wantComplete: false, wantCoverage: false, wantValues: false, wantUnavailable: true, unavailablePart: "no persisted profile or coverage marker"},
	}
	for _, tc := range cases {
		result, err := store.DiscoverFieldEnrichment(s.ctx, catalog.FieldEnrichmentOptions{
			Project: tc.project, DatasetGeneration: "g1", ResourceTypes: []string{tc.resourceType},
			AuthResourcePathsUnrestricted: boolPointer(true),
		})
		if tc.wantUnavailable {
			if err == nil || !errors.Is(err, ErrFieldProfileUnavailable) {
				t.Fatalf("%s: error = %v, want errors.Is ErrFieldProfileUnavailable", tc.project, err)
			}
			var typed *FieldProfileUnavailableError
			if !errors.As(err, &typed) {
				t.Fatalf("%s: error %v is not a typed *FieldProfileUnavailableError", tc.project, err)
			}
			if !strings.Contains(typed.Reason, tc.unavailablePart) {
				t.Fatalf("%s: unavailable reason %q lacks %q", tc.project, typed.Reason, tc.unavailablePart)
			}
		} else if err != nil {
			t.Fatalf("%s: discover: %v", tc.project, err)
		}
		if result.Status != tc.wantStatus || result.Complete != tc.wantComplete ||
			result.FieldCoverageKnown != tc.wantCoverage || (len(result.Values) > 0) != tc.wantValues {
			t.Fatalf("%s: result = status %v complete %v coverage %v values %d, want %v/%v/%v/%v",
				tc.project, result.Status, result.Complete, result.FieldCoverageKnown, len(result.Values),
				tc.wantStatus, tc.wantComplete, tc.wantCoverage, tc.wantValues)
		}
		if tc.wantStatus == catalog.EvidenceTruncated && !result.Truncated {
			t.Fatalf("%s: truncated status without the truncation flag", tc.project)
		}
	}

	if rows := s.witnessRows(t, "pgcat01-empty", "g1"); len(rows) != 0 {
		t.Fatalf("known-empty scope persisted field rows: %v", rows)
	}
	coverage := s.witnessCoverage(t, "pgcat01-empty", "g1")
	if len(coverage) != 1 || coverage[0].EnrichmentIsNull || *coverage[0].EnrichmentComplete != true || coverage[0].EnrichmentTruncated {
		t.Fatalf("known-empty coverage marker = %v, want exactly one complete/untruncated marker", coverage)
	}
	if len(s.witnessCoverage(t, "pgcat01-never", "g1")) != 0 {
		t.Fatal("never-written scope has a coverage marker")
	}
	statusWitness := s.witnessRows(t, "pgcat01-empty-truncated", "g1", "a", "Condition")
	if len(statusWitness) != 1 || statusWitness[0].RecordType != catalog.FieldProfileStatusRecordType ||
		statusWitness[0].Path != "" || statusWitness[0].EnrichmentComplete == nil || *statusWitness[0].EnrichmentComplete ||
		!statusWitness[0].EnrichmentTruncated {
		t.Fatalf("profile_status witness = %v, want one pathless complete=false truncated=true marker", statusWitness)
	}
	if !strings.HasPrefix(statusWitness[0].ProfileKey, "fpe_") {
		t.Fatalf("profile_status key %q lost its fpe_ identity", statusWitness[0].ProfileKey)
	}

	restrictedEmpty, err := store.DiscoverFieldEnrichment(s.ctx, catalog.FieldEnrichmentOptions{
		Project: "pgcat01-complete", DatasetGeneration: "g1", ResourceTypes: []string{"Observation"},
		AuthResourcePathsUnrestricted: boolPointer(false), AuthResourcePaths: []string{},
	})
	if err != nil {
		t.Fatalf("restricted-empty discover: %v", err)
	}
	if restrictedEmpty.Status != catalog.EvidenceEmpty || !restrictedEmpty.Available || !restrictedEmpty.Complete {
		t.Fatalf("restricted-empty result = %#v", restrictedEmpty)
	}

	otherAuth, err := store.DiscoverFieldEnrichment(s.ctx, catalog.FieldEnrichmentOptions{
		Project: "pgcat01-empty", DatasetGeneration: "g1", ResourceTypes: []string{"Observation"},
		AuthResourcePathsUnrestricted: boolPointer(false), AuthResourcePaths: []string{"other-path"},
	})
	if err == nil || !errors.Is(err, ErrFieldProfileUnavailable) {
		t.Fatalf("foreign auth path read = %#v err=%v, want typed unavailable", otherAuth, err)
	}
	if otherAuth.Status != catalog.EvidenceUnavailable || otherAuth.Available || otherAuth.Complete {
		t.Fatalf("foreign auth path result = %#v", otherAuth)
	}
}

func TestFieldProfileDiscoveryScopesAuthPathsAndDigests(t *testing.T) {
	s := newFieldProfileService(t)
	store := s.adapter(t)

	for _, scope := range []storepostgres.FieldProfileScope{
		fieldProfileScope("pgcat01-disc", "g1", "a", "Patient"),
		fieldProfileScope("pgcat01-disc", "g1", "b", "Patient"),
		fieldProfileScope("pgcat01-disc", "g1", "a", "Observation"),
	} {
		var payloads []map[string]any
		if scope.ResourceType == "Patient" {
			payloads = []map[string]any{patientPayload("male", "2001-02-03")}
			if scope.AuthResourcePath == "b" {
				payloads = []map[string]any{patientPayload("female", "1988-08-08")}
			}
		} else {
			payloads = []map[string]any{observationPayload()}
		}
		docs := profilerDocuments(t, scope.Project, scope.DatasetGeneration, scope.AuthResourcePath, scope.ResourceType, catalog.DefaultProfileLimits(), payloads...)
		if err := store.WriteFieldProfile(s.ctx, scope, docs, 0); err != nil {
			t.Fatalf("seed %v: %v", scope, err)
		}
	}

	discover := func(restricted bool, paths []string, types ...string) (catalog.FieldEnrichmentResult, error) {
		return store.DiscoverFieldEnrichment(s.ctx, catalog.FieldEnrichmentOptions{
			Project: "pgcat01-disc", DatasetGeneration: "g1", ResourceTypes: types,
			AuthResourcePathsUnrestricted: boolPointer(!restricted), AuthResourcePaths: paths,
		})
	}

	aOnly, err := discover(true, []string{"a"}, "Patient")
	if err != nil || aOnly.Status != catalog.EvidenceAvailable {
		t.Fatalf("restricted [a] = %#v err=%v", aOnly, err)
	}
	for _, value := range aOnly.Values {
		if value.AuthResourcePath != "a" || value.ResourceType != "Patient" {
			t.Fatalf("restricted [a] leaked scope: %+v", value)
		}
	}
	bOnly, err := discover(true, []string{"b"}, "Patient")
	if err != nil {
		t.Fatalf("restricted [b]: %v", err)
	}
	if aOnly.Digest == bOnly.Digest || aOnly.Digest == "" || bOnly.Digest == "" {
		t.Fatalf("different authorization scopes share a digest: %q vs %q", aOnly.Digest, bOnly.Digest)
	}
	both, err := discover(true, []string{"a", "b"}, "Patient")
	if err != nil {
		t.Fatalf("restricted [a b]: %v", err)
	}
	if len(both.Values) != len(aOnly.Values)+len(bOnly.Values) {
		t.Fatalf("restricted [a b] values %d != %d + %d", len(both.Values), len(aOnly.Values), len(bOnly.Values))
	}
	unrestricted, err := discover(false, nil, "Observation", "Patient")
	if err != nil || unrestricted.Status != catalog.EvidenceAvailable {
		t.Fatalf("unrestricted = %#v err=%v", unrestricted, err)
	}
	if len(unrestricted.Values) <= len(both.Values) {
		t.Fatalf("unrestricted read did not widen beyond the restricted union: %d vs %d", len(unrestricted.Values), len(both.Values))
	}
	for i := 1; i < len(unrestricted.Values); i++ {
		if !fieldEnrichmentLess(unrestricted.Values[i-1], unrestricted.Values[i]) {
			t.Fatalf("values not ordered at %d: %+v then %+v", i, unrestricted.Values[i-1], unrestricted.Values[i])
		}
	}
	again, err := store.DiscoverFieldEnrichment(s.ctx, catalog.FieldEnrichmentOptions{
		Project: "pgcat01-disc", DatasetGeneration: "g1", ResourceTypes: []string{"Patient", "Observation"},
	})
	if err != nil {
		t.Fatalf("legacy-default read: %v", err)
	}
	if again.Digest != unrestricted.Digest {
		t.Fatalf("digest unstable across equivalent reads: %q vs %q", again.Digest, unrestricted.Digest)
	}
}

func TestFieldProfileWriteRejectionsLeaveNothingBehind(t *testing.T) {
	s := newFieldProfileService(t)
	store := s.adapter(t)

	baseDoc := func(mutate func(*catalog.FieldCatalogDocument)) catalog.FieldCatalogDocument {
		doc := catalog.FieldCatalogDocument{
			Key: "reject-key", Project: "pgcat01-reject", DatasetGeneration: "g1",
			AuthResourcePath: "a", ResourceType: "Patient", Path: "gender", Kind: "scalar",
			DocCount: 1, SampleCount: 1, DistinctValues: []string{"male"},
			EnrichmentComplete: boolPointer(true),
		}
		mutate(&doc)
		return doc
	}
	cases := []struct {
		name     string
		docs     []catalog.FieldCatalogDocument
		sentinel error
	}{
		{"scope divergence", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.AuthResourcePath = "other" })}, ErrInvalidFieldProfile},
		{"project divergence", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.Project = "pgcat01-other" })}, ErrInvalidFieldProfile},
		{"generation divergence", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.DatasetGeneration = "g2" })}, ErrInvalidFieldProfile},
		{"duplicate identity", []catalog.FieldCatalogDocument{baseDoc(func(*catalog.FieldCatalogDocument) {}), baseDoc(func(d *catalog.FieldCatalogDocument) { d.DocCount = 2 })}, ErrInvalidFieldProfile},
		{"inconsistent completeness", []catalog.FieldCatalogDocument{
			baseDoc(func(*catalog.FieldCatalogDocument) {}),
			baseDoc(func(d *catalog.FieldCatalogDocument) {
				d.Path = "active"
				d.Key = "reject-key-2"
				d.EnrichmentComplete = boolPointer(false)
			}),
		}, ErrInvalidFieldProfile},
		{"sample count disagreement", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.SampleCount = 5 })}, ErrInvalidFieldProfile},
		{"blank key", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.Key = "" })}, storepostgres.ErrInvalidFieldProfileRow},
		{"field without kind", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.Kind = "" })}, storepostgres.ErrInvalidFieldProfileRow},
		{"status with path", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.RecordType = catalog.FieldProfileStatusRecordType })}, storepostgres.ErrInvalidFieldProfileRow},
		{"unknown record type", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.RecordType = "wat" })}, storepostgres.ErrInvalidFieldProfileRow},
		{"negative doc count", []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.DocCount = -1 })}, storepostgres.ErrInvalidFieldProfileRow},
	}
	for _, tc := range cases {
		err := store.WriteFieldProfile(s.ctx, fieldProfileScope("pgcat01-reject", "g1", "a", "Patient"), tc.docs, 0)
		if err == nil || !errors.Is(err, tc.sentinel) {
			t.Fatalf("%s: error = %v, want errors.Is %v", tc.name, err, tc.sentinel)
		}
		if s.witnessRowCount(t, "pgcat01-reject") != 0 {
			t.Fatalf("%s: rejected batch left rows behind", tc.name)
		}
		if len(s.witnessCoverage(t, "pgcat01-reject", "g1")) != 0 {
			t.Fatalf("%s: rejected batch left a coverage marker behind", tc.name)
		}
	}
	mixed := []catalog.FieldCatalogDocument{
		baseDoc(func(*catalog.FieldCatalogDocument) {}),
		baseDoc(func(d *catalog.FieldCatalogDocument) { d.Path = "active"; d.Key = "reject-key-2"; d.SampleCount = 9 }),
	}
	if err := store.WriteFieldProfile(s.ctx, fieldProfileScope("pgcat01-reject", "g1", "a", "Patient"), mixed, 0); !errors.Is(err, ErrInvalidFieldProfile) {
		t.Fatalf("mixed batch error = %v, want ErrInvalidFieldProfile", err)
	}
	if s.witnessRowCount(t, "pgcat01-reject") != 0 {
		t.Fatal("mixed batch persisted the valid half")
	}

	validDocs := []catalog.FieldCatalogDocument{baseDoc(func(d *catalog.FieldCatalogDocument) { d.Key = "keep-me-key" })}
	if err := store.WriteFieldProfile(s.ctx, fieldProfileScope("pgcat01-reject", "g1", "a", "Patient"), validDocs, 0); err != nil {
		t.Fatalf("valid write: %v", err)
	}
	badBatch := []catalog.FieldCatalogDocument{
		baseDoc(func(d *catalog.FieldCatalogDocument) { d.Key = "keep-me-key"; d.DocCount = 99 }),
		baseDoc(func(d *catalog.FieldCatalogDocument) { d.Path = "active"; d.Key = "doomed-key"; d.Kind = "" }),
	}
	if err := store.WriteFieldProfile(s.ctx, fieldProfileScope("pgcat01-reject", "g1", "a", "Patient"), badBatch, 0); !errors.Is(err, storepostgres.ErrInvalidFieldProfileRow) {
		t.Fatalf("atomic rejection error = %v, want ErrInvalidFieldProfileRow", err)
	}
	after, err := store.ReadFieldProfile(s.ctx, fieldProfileScope("pgcat01-reject", "g1", "a", "Patient"))
	if err != nil {
		t.Fatalf("read after atomic rejection: %v", err)
	}
	if len(after) != 1 || after[0].DocCount != 1 || after[0].Key != "keep-me-key" {
		t.Fatalf("atomic rejection disturbed the stored state: %#v", after)
	}

	mustCheckViolation := func(step, sql string) {
		t.Helper()
		_, err := s.iso.Exec(s.ctx, sql)
		var pgErr *pgconn.PgError
		if err == nil || !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("%s: error = %v, want CHECK violation 23514", step, err)
		}
	}
	mustCheckViolation("status row with a path", "INSERT INTO "+s.profileFQN()+
		" (project, dataset_generation, auth_resource_path, resource_type, path, profile_key, record_type, kind,"+
		" doc_count, max_items, sample_count, distinct_truncated, enrichment_truncated, pivot_candidate, pivot_kind,"+
		" pivot_family, pivot_column_selector, pivot_value_selector, pivot_item_source, pivot_item_resource_type)"+
		" VALUES ('pgcat01-dbcontract','','','Patient','oops','k','profile_status','scalar',0,0,0,false,false,false,'','','','','','')")
	mustCheckViolation("negative count", "INSERT INTO "+s.profileFQN()+
		" (project, dataset_generation, auth_resource_path, resource_type, path, profile_key, record_type, kind,"+
		" doc_count, max_items, sample_count, distinct_truncated, enrichment_truncated, pivot_candidate, pivot_kind,"+
		" pivot_family, pivot_column_selector, pivot_value_selector, pivot_item_source, pivot_item_resource_type)"+
		" VALUES ('pgcat01-dbcontract','','','Patient','gender','k','','scalar',-1,0,0,false,false,false,'','','','','','')")
	mustCheckViolation("jsonb scalar subrecord", "INSERT INTO "+s.profileFQN()+
		" (project, dataset_generation, auth_resource_path, resource_type, path, profile_key, record_type, kind,"+
		" doc_count, max_items, sample_count, distinct_truncated, enrichment_truncated, pivot_candidate, pivot_kind,"+
		" pivot_family, pivot_column_selector, pivot_value_selector, pivot_item_source, pivot_item_resource_type, extension_values)"+
		" VALUES ('pgcat01-dbcontract','','','Patient','gender','k','','scalar',0,0,0,false,false,false,'','','','','','','42'::jsonb)")

	if _, err := NewFieldProfileStore(nil); err == nil {
		t.Fatal("NewFieldProfileStore(nil) accepted a nil persistence boundary")
	}
	if _, err := storepostgres.NewFieldProfileRepository(s.openClient(t), "Bad-Name"); !errors.Is(err, storepostgres.ErrInvalidSchemaName) {
		t.Fatalf("NewFieldProfileRepository invalid schema = %v, want ErrInvalidSchemaName", err)
	}
	repo := s.realRepository(t)
	if _, err := repo.ReadFieldProfileRowsFiltered(s.ctx, storepostgres.FieldProfileFilter{
		Project: "x", ResourceTypes: []string{"Patient"},
	}); !errors.Is(err, storepostgres.ErrInvalidFieldProfileScope) {
		t.Fatalf("nil unrestricted flag = %v, want ErrInvalidFieldProfileScope", err)
	}
	if _, err := repo.ReadFieldProfileRowsFiltered(s.ctx, storepostgres.FieldProfileFilter{
		Project: "x", ResourceTypes: []string{"Patient"},
		AuthResourcePathsUnrestricted: boolPointer(true), AuthResourcePaths: []string{"a"},
	}); !errors.Is(err, storepostgres.ErrInvalidFieldProfileScope) {
		t.Fatalf("unrestricted with paths = %v, want ErrInvalidFieldProfileScope", err)
	}
}

func TestZZFieldProfileZeroLeftovers(t *testing.T) {
	raw := fieldProfileTestPostgresURL(t)
	scrub := fieldProfileScrubber(raw)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %s", scrub(err))
	}
	defer admin.Close()

	var dbLeftovers []string
	rows, err := admin.Query(ctx, "SELECT datname FROM pg_database WHERE datname LIKE 'pgcat01\\_it\\_%' ESCAPE '\\' ORDER BY datname")
	if err != nil {
		t.Fatalf("audit isolated databases: %s", scrub(err))
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("audit isolated databases: %s", scrub(err))
		}
		dbLeftovers = append(dbLeftovers, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("audit isolated databases: %s", scrub(err))
	}
	if len(dbLeftovers) != 0 {
		t.Errorf("leftover isolated databases owned by this package: %v", dbLeftovers)
	}

	var schemaLeftovers []string
	rows, err = admin.Query(ctx, "SELECT nspname FROM pg_namespace WHERE nspname LIKE 'pgcat01\\_it\\_%' ESCAPE '\\' ORDER BY nspname")
	if err != nil {
		t.Fatalf("audit owned schemas: %s", scrub(err))
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("audit owned schemas: %s", scrub(err))
		}
		schemaLeftovers = append(schemaLeftovers, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("audit owned schemas: %s", scrub(err))
	}
	if len(schemaLeftovers) != 0 {
		t.Errorf("leftover owned schemas owned by this package: %v", schemaLeftovers)
	}
}
