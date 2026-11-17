package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/source"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgstore04ResourceNamePattern = regexp.MustCompile(`^pgstore04_it_[0-9]+_[0-9a-f]+$`)

type resourceService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newResourceService(t *testing.T) *resourceService {
	t.Helper()
	raw := testPostgresURL(t)

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

	dbName := fmt.Sprintf("pgstore04_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore04ResourceNamePattern.MatchString(dbName) {
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

	schema := fmt.Sprintf("pgstore04_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgstore04ResourceNamePattern.MatchString(schema) {
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

	return &resourceService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

func (s *resourceService) openClient(t *testing.T) *Client {
	t.Helper()
	client, err := Open(s.ctx, Config{
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

func (s *resourceService) newRepository(t *testing.T) *ResourceRepository {
	t.Helper()
	repo, err := NewResourceRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new resource repository", err)
	}
	return repo
}

func (s *resourceService) mustMigrateEmbedded(t *testing.T) MigrateResult {
	t.Helper()
	result, err := MigrateSchema(s.ctx, s.openClient(t), s.schema)
	if err != nil {
		s.fail("MigrateSchema", err)
	}
	return result
}

func (s *resourceService) readLedger(t *testing.T) []LedgerRecord {
	t.Helper()
	rows, err := s.iso.Query(s.ctx,
		`SELECT version, name, checksum, applied_at FROM "`+s.schema+`".`+LedgerTable+` ORDER BY version`)
	if err != nil {
		s.fail("witness ledger", err)
	}
	defer rows.Close()
	var out []LedgerRecord
	for rows.Next() {
		var rec LedgerRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.Checksum, &rec.AppliedAt); err != nil {
			s.fail("witness ledger scan", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness ledger rows", err)
	}
	return out
}

func (s *resourceService) mustInsert(t *testing.T, repo *ResourceRepository, rec source.Resource) int64 {
	t.Helper()
	seq, err := repo.InsertResource(s.ctx, rec)
	if err != nil {
		s.fail(fmt.Sprintf("insert resource %s/%s/%s/%s",
			rec.Project, rec.DatasetGeneration, rec.ResourceType, rec.LogicalID), err)
	}
	if seq < 1 {
		t.Fatalf("insert returned storage identity %d, want >= 1", seq)
	}
	return seq
}

func (s *resourceService) mustGet(t *testing.T, repo *ResourceRepository,
	scope source.ResourceScope, resourceType, logicalID string) ResourceRow {
	t.Helper()
	rec, err := repo.GetResource(s.ctx, scope, resourceType, logicalID)
	if err != nil {
		s.fail(fmt.Sprintf("get resource %s/%s/%s/%s",
			scope.Project, scope.DatasetGeneration, resourceType, logicalID), err)
	}
	return rec
}

func (s *resourceService) mustRead(t *testing.T, repo *ResourceRepository,
	scope source.ResourceScope, afterSeq int64, limit int) ([]ResourceRow, bool) {
	t.Helper()
	rows, hasMore, err := repo.ReadResources(s.ctx, scope, afterSeq, limit)
	if err != nil {
		s.fail(fmt.Sprintf("keyset read resources %s/%s after %d",
			scope.Project, scope.DatasetGeneration, afterSeq), err)
	}
	return rows, hasMore
}

func TestPhysicalResourceRowRefusesOfferedStorageIdentity(t *testing.T) {
	base := source.Resource{
		Project: "proj", DatasetGeneration: "gen", ResourceType: "Patient",
		LogicalID: "p-1", AuthResourcePath: "practitioner/pr-1",
		Payload: json.RawMessage(`{"id":"p-1"}`),
	}
	if err := validateResourceForInsert(ResourceRow{Resource: base}); err != nil {
		t.Errorf("validateResourceForInsert with zero storage identity = %v, want nil", err)
	}
	err := validateResourceForInsert(ResourceRow{Resource: base, ResourceSeq: 7})
	if !errors.Is(err, ErrInvalidResource) {
		t.Fatalf("validateResourceForInsert with offered storage identity = %v, want ErrInvalidResource", err)
	}
	if !strings.Contains(err.Error(), "server-assigned") {
		t.Errorf("refusal %v does not name the server-assigned contract", err)
	}
}

func restrictedScope(project, generation string, paths ...string) source.ResourceScope {
	restricted := false
	return source.ResourceScope{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &restricted,
		AuthResourcePaths:             paths,
	}
}

func unrestrictedScope(project, generation string) source.ResourceScope {
	unrestricted := true
	return source.ResourceScope{
		Project:                       project,
		DatasetGeneration:             generation,
		AuthResourcePathsUnrestricted: &unrestricted,
	}
}

func payloadObject(t *testing.T, payload json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	if payload == nil {
		t.Fatal("payload is nil (SQL NULL), want a JSON object")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(payload, &obj); err != nil {
		t.Fatalf("payload %s is not a JSON object: %v", payload, err)
	}
	return obj
}

func assertPayloadEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("stored payload %s does not parse: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want payload %s does not parse: %v", want, err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("stored payload %s, want %s", got, want)
	}
}

func assertRowScope(t *testing.T, got ResourceRow, want source.Resource) {
	t.Helper()
	if got.Project != want.Project || got.DatasetGeneration != want.DatasetGeneration ||
		got.ResourceType != want.ResourceType || got.LogicalID != want.LogicalID ||
		got.AuthResourcePath != want.AuthResourcePath {
		t.Fatalf("row scope = %+v, want scope %+v", got, want)
	}
}

func TestResourceValidationRejectsInvalidInserts(t *testing.T) {
	repo, err := NewResourceRepository(stubConnSource{}, "pgstore04_stub")
	if err != nil {
		t.Fatalf("NewResourceRepository: %v", err)
	}
	base := source.Resource{
		Project: "proj", DatasetGeneration: "gen", ResourceType: "Patient",
		LogicalID: "p-1", AuthResourcePath: "practitioner/pr-1",
		Payload: json.RawMessage(`{"id":"p-1"}`),
	}
	for _, tc := range []struct {
		name   string
		mutate func(*source.Resource)
	}{
		{"blank project", func(r *source.Resource) { r.Project = "  " }},
		{"blank resource type", func(r *source.Resource) { r.ResourceType = "" }},
		{"blank logical id", func(r *source.Resource) { r.LogicalID = "\t" }},
		{"malformed json payload", func(r *source.Resource) { r.Payload = json.RawMessage(`{"id":`) }},
		{"whitespace payload", func(r *source.Resource) { r.Payload = json.RawMessage(`  `) }},
	} {
		rec := base
		tc.mutate(&rec)
		_, err := repo.InsertResource(context.Background(), rec)
		if !errors.Is(err, ErrInvalidResource) {
			t.Errorf("%s: InsertResource = %v, want ErrInvalidResource", tc.name, err)
		}
	}
}

func TestResourceScopeAndQueryValidation(t *testing.T) {
	repo, err := NewResourceRepository(stubConnSource{}, "pgstore04_stub")
	if err != nil {
		t.Fatalf("NewResourceRepository: %v", err)
	}
	ctx := context.Background()

	if _, err := NewResourceRepository(nil, "pgstore04_stub"); err == nil {
		t.Error("NewResourceRepository with nil source succeeded, want error")
	}
	if _, err := NewResourceRepository(stubConnSource{}, "Bad-Schema"); !errors.Is(err, ErrInvalidSchemaName) {
		t.Errorf("NewResourceRepository with invalid schema = %v, want ErrInvalidSchemaName", err)
	}

	scope := source.ResourceScope{Project: "proj", AuthResourcePaths: []string{"a"}}
	if _, err := repo.GetResource(ctx, scope, "Patient", "p-1"); !errors.Is(err, ErrInvalidResourceScope) {
		t.Errorf("GetResource with nil unrestricted flag = %v, want ErrInvalidResourceScope", err)
	}
	if _, _, err := repo.ReadResources(ctx, scope, 0, 10); !errors.Is(err, ErrInvalidResourceScope) {
		t.Errorf("ReadResources with nil unrestricted flag = %v, want ErrInvalidResourceScope", err)
	}

	unrestrictedWithPaths := unrestrictedScope("proj", "gen")
	unrestrictedWithPaths.AuthResourcePaths = []string{"practitioner/pr-1"}
	if _, err := repo.GetResource(ctx, unrestrictedWithPaths, "Patient", "p-1"); !errors.Is(err, ErrInvalidResourceScope) {
		t.Errorf("unrestricted scope carrying paths = %v, want ErrInvalidResourceScope", err)
	}

	blank := restrictedScope("  ", "gen", "a")
	if _, _, err := repo.ReadResources(ctx, blank, 0, 10); !errors.Is(err, ErrInvalidResourceScope) {
		t.Errorf("blank project = %v, want ErrInvalidResourceScope", err)
	}

	for _, limit := range []int{0, -1, MaxResourceKeysetLimit + 1} {
		if _, _, err := repo.ReadResources(ctx, unrestrictedScope("proj", "gen"), 0, limit); !errors.Is(err, ErrInvalidResourceScope) {
			t.Errorf("limit %d = %v, want ErrInvalidResourceScope", limit, err)
		}
	}
	if _, _, err := repo.ReadResources(ctx, unrestrictedScope("proj", "gen"), -1, 10); !errors.Is(err, ErrInvalidResourceScope) {
		t.Errorf("negative cursor = %v, want ErrInvalidResourceScope", err)
	}

	if _, err := repo.GetResource(ctx, unrestrictedScope("proj", "gen"), " ", "p-1"); !errors.Is(err, ErrInvalidResource) {
		t.Errorf("blank resource type = %v, want ErrInvalidResource", err)
	}
	if _, err := repo.GetResource(ctx, unrestrictedScope("proj", "gen"), "Patient", ""); !errors.Is(err, ErrInvalidResource) {
		t.Errorf("blank logical id = %v, want ErrInvalidResource", err)
	}
}

func TestResourceMigrationCreatesTypedResourceTable(t *testing.T) {
	svc := newResourceService(t)
	first := svc.mustMigrateEmbedded(t)
	second := svc.mustMigrateEmbedded(t)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("re-apply changed the result:\n1st: %+v\n2nd: %+v", first, second)
	}
	assertLedgerEquals(t, svc.readLedger(t), mustEmbeddedMigrations(t))

	var recordedName string
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT name FROM "`+svc.schema+`".`+LedgerTable+` WHERE version = 2`).Scan(&recordedName); err != nil {
		svc.fail("witness version 2 ledger row", err)
	}
	if recordedName != "fhir_resource" {
		t.Fatalf("version 2 ledger name = %q, want fhir_resource", recordedName)
	}

	type columnWitness struct {
		Name     string
		Type     string
		NotNull  bool
		Identity string
	}
	rows, err := svc.iso.Query(svc.ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod), a.attnotnull,
		       CASE WHEN a.attidentity = 'a' THEN 'a'
		            WHEN a.attidentity = 'd' THEN 'd'
		            ELSE '' END
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = 'fhir_resource'
		  AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, svc.schema)
	if err != nil {
		svc.fail("witness columns", err)
	}
	defer rows.Close()
	var columns []columnWitness
	for rows.Next() {
		var col columnWitness
		if err := rows.Scan(&col.Name, &col.Type, &col.NotNull, &col.Identity); err != nil {
			svc.fail("witness columns scan", err)
		}
		columns = append(columns, col)
	}
	if err := rows.Err(); err != nil {
		svc.fail("witness columns rows", err)
	}
	wantColumns := []columnWitness{
		{"resource_seq", "bigint", true, "a"},
		{"project", "text", true, ""},
		{"dataset_generation", "text", true, ""},
		{"resource_type", "text", true, ""},
		{"logical_id", "text", true, ""},
		{"auth_resource_path", "text", true, ""},
		{"payload", "jsonb", false, ""},
		{"recorded_at", "timestamp with time zone", true, ""},
	}
	if !reflect.DeepEqual(columns, wantColumns) {
		t.Fatalf("fhir_resource columns = %+v, want %+v", columns, wantColumns)
	}

	constraintRows, err := svc.iso.Query(svc.ctx, `
		SELECT con.conname, pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		JOIN pg_class c ON c.oid = con.conrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1 AND c.relname = 'fhir_resource'
		  AND con.contype IN ('p', 'u', 'c')
		ORDER BY con.conname`, svc.schema)
	if err != nil {
		svc.fail("witness constraints", err)
	}
	defer constraintRows.Close()
	var constraints [][2]string
	for constraintRows.Next() {
		var name, def string
		if err := constraintRows.Scan(&name, &def); err != nil {
			svc.fail("witness constraints scan", err)
		}
		constraints = append(constraints, [2]string{name, def})
	}
	if err := constraintRows.Err(); err != nil {
		svc.fail("witness constraints rows", err)
	}
	wantConstraints := [][2]string{
		{"fhir_resource_logical_id_present", `CHECK ((logical_id <> ''::text))`},
		{"fhir_resource_natural_identity_ux", `UNIQUE (project, dataset_generation, resource_type, logical_id)`},
		{"fhir_resource_pkey", `PRIMARY KEY (resource_seq)`},
		{"fhir_resource_project_present", `CHECK ((project <> ''::text))`},
		{"fhir_resource_resource_type_present", `CHECK ((resource_type <> ''::text))`},
	}
	if !reflect.DeepEqual(constraints, wantConstraints) {
		t.Fatalf("fhir_resource constraints = %+v, want %+v", constraints, wantConstraints)
	}

	indexRows, err := svc.iso.Query(svc.ctx, `
		SELECT indexname, indexdef FROM pg_indexes
		WHERE schemaname = $1 AND tablename = 'fhir_resource'
		ORDER BY indexname`, svc.schema)
	if err != nil {
		svc.fail("witness indexes", err)
	}
	defer indexRows.Close()
	var indexes [][2]string
	for indexRows.Next() {
		var name, def string
		if err := indexRows.Scan(&name, &def); err != nil {
			svc.fail("witness indexes scan", err)
		}
		indexes = append(indexes, [2]string{name, def})
	}
	if err := indexRows.Err(); err != nil {
		svc.fail("witness indexes rows", err)
	}
	wantIndexes := [][2]string{
		{"fhir_resource_natural_identity_ux",
			`CREATE UNIQUE INDEX fhir_resource_natural_identity_ux ON ` + svc.schema + `.fhir_resource USING btree (project, dataset_generation, resource_type, logical_id)`},
		{"fhir_resource_pkey",
			`CREATE UNIQUE INDEX fhir_resource_pkey ON ` + svc.schema + `.fhir_resource USING btree (resource_seq)`},
		{"fhir_resource_scope_seq_ix",
			`CREATE INDEX fhir_resource_scope_seq_ix ON ` + svc.schema + `.fhir_resource USING btree (project, dataset_generation, auth_resource_path, resource_seq)`},
		{"fhir_resource_semantic_keyset_ix",
			`CREATE INDEX fhir_resource_semantic_keyset_ix ON ` + svc.schema + `.fhir_resource USING btree (project, dataset_generation, resource_type, logical_id COLLATE "C")`},
	}
	if !reflect.DeepEqual(indexes, wantIndexes) {
		t.Fatalf("fhir_resource indexes = %+v, want %+v", indexes, wantIndexes)
	}

	var rowCount int64
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT count(*) FROM "`+svc.schema+`".fhir_resource`).Scan(&rowCount); err != nil {
		svc.fail("count resources", err)
	}
	if rowCount != 0 {
		t.Fatalf("fresh fhir_resource has %d rows, want 0", rowCount)
	}
}

func TestResourceInsertAndExactReadPreserveFHIRJSON(t *testing.T) {
	svc := newResourceService(t)
	svc.mustMigrateEmbedded(t)
	repo := svc.newRepository(t)

	const payload = `{"resourceType":"Observation","id":"obs-7","status":"final",` +
		`"deceased":null,"category":[],"meta":{},"note":{"text":"keep"},"value":42}`
	want := source.Resource{
		Project: "proj-f", DatasetGeneration: "gen-1", ResourceType: "Observation",
		LogicalID: "obs-7", AuthResourcePath: "practitioner/pr-1",
		Payload: json.RawMessage(payload),
	}
	seq := svc.mustInsert(t, repo, want)

	got := svc.mustGet(t, repo,
		restrictedScope("proj-f", "gen-1", "practitioner/pr-1"), "Observation", "obs-7")
	if got.ResourceSeq != seq {
		t.Errorf("storage identity = %d, want %d assigned at insert", got.ResourceSeq, seq)
	}
	assertRowScope(t, got, want)
	assertPayloadEqual(t, got.Payload, payload)

	obj := payloadObject(t, got.Payload)
	raw, ok := obj["deceased"]
	if !ok || string(raw) != "null" {
		t.Errorf(`key "deceased" = %q (present=%t), want literal JSON null`, raw, ok)
	}
	raw, ok = obj["category"]
	if !ok || string(raw) != "[]" {
		t.Errorf(`key "category" = %q (present=%t), want literal []`, raw, ok)
	}
	raw, ok = obj["meta"]
	if !ok || string(raw) != "{}" {
		t.Errorf(`key "meta" = %q (present=%t), want literal {}`, raw, ok)
	}
	raw, ok = obj["value"]
	if !ok || string(raw) != "42" {
		t.Errorf(`key "value" = %q (present=%t), want literal 42`, raw, ok)
	}
	if raw, ok := obj["subject"]; ok {
		t.Errorf(`absent key "subject" came back as %q; missing key must stay absent`, raw)
	}

	var typeof string
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT jsonb_typeof(payload) FROM "`+svc.schema+`".fhir_resource WHERE resource_seq = $1`,
		seq).Scan(&typeof); err != nil {
		svc.fail("witness jsonb_typeof", err)
	}
	if typeof != "object" {
		t.Errorf("jsonb_typeof(payload) = %q, want object", typeof)
	}
}

func TestResourcePayloadDistinguishesSQLNullJSONNullAndMissingKey(t *testing.T) {
	svc := newResourceService(t)
	svc.mustMigrateEmbedded(t)
	repo := svc.newRepository(t)
	scope := func() source.ResourceScope { return restrictedScope("proj-n", "gen-1", "") }

	absentSeq := svc.mustInsert(t, repo, source.Resource{
		Project: "proj-n", DatasetGeneration: "gen-1", ResourceType: "Patient",
		LogicalID: "n-1", AuthResourcePath: "",
		Payload: nil,
	})
	jsonNullSeq := svc.mustInsert(t, repo, source.Resource{
		Project: "proj-n", DatasetGeneration: "gen-1", ResourceType: "Patient",
		LogicalID: "n-2", AuthResourcePath: "",
		Payload: json.RawMessage(`null`),
	})
	objectSeq := svc.mustInsert(t, repo, source.Resource{
		Project: "proj-n", DatasetGeneration: "gen-1", ResourceType: "Patient",
		LogicalID: "n-3", AuthResourcePath: "",
		Payload: json.RawMessage(`{"id":"n-3","a":null}`),
	})

	witness := func(seq int64, wantIsNull bool, wantTypeof *string) {
		t.Helper()
		var isNull bool
		var typeof *string
		if err := svc.iso.QueryRow(svc.ctx,
			`SELECT payload IS NULL, jsonb_typeof(payload) FROM "`+svc.schema+`".fhir_resource WHERE resource_seq = $1`,
			seq).Scan(&isNull, &typeof); err != nil {
			svc.fail("witness payload nulls", err)
		}
		if isNull != wantIsNull {
			t.Errorf("seq %d payload IS NULL = %t, want %t", seq, isNull, wantIsNull)
		}
		if (typeof == nil) != (wantTypeof == nil) {
			t.Errorf("seq %d jsonb_typeof = %v, want %v", seq, typeof, wantTypeof)
			return
		}
		if wantTypeof != nil && *typeof != *wantTypeof {
			t.Errorf("seq %d jsonb_typeof = %q, want %q", seq, *typeof, *wantTypeof)
		}
	}
	nullType := "null"
	objectType := "object"
	witness(absentSeq, true, nil)
	witness(jsonNullSeq, false, &nullType)
	witness(objectSeq, false, &objectType)

	absent := svc.mustGet(t, repo, scope(), "Patient", "n-1")
	if absent.Payload != nil {
		t.Errorf("SQL NULL payload read back as %q, want nil", absent.Payload)
	}
	jsonNull := svc.mustGet(t, repo, scope(), "Patient", "n-2")
	if string(jsonNull.Payload) != "null" || jsonNull.Payload == nil {
		t.Errorf("JSON null payload read back as %q, want the bytes null", jsonNull.Payload)
	}
	object := svc.mustGet(t, repo, scope(), "Patient", "n-3")
	obj := payloadObject(t, object.Payload)
	if raw, ok := obj["a"]; !ok || string(raw) != "null" {
		t.Errorf(`explicit null key "a" = %q (present=%t), want literal null`, raw, ok)
	}
	if raw, ok := obj["b"]; ok {
		t.Errorf(`missing key "b" came back as %q; missing must stay distinct from null`, raw)
	}
}

func TestResourceDuplicateNaturalIdentityConflict(t *testing.T) {
	svc := newResourceService(t)
	svc.mustMigrateEmbedded(t)
	repo := svc.newRepository(t)

	const original = `{"resourceType":"Patient","id":"dup-1","gender":"female"}`
	const different = `{"resourceType":"Patient","id":"dup-1","gender":"male"}`
	originalSeq := svc.mustInsert(t, repo, source.Resource{
		Project: "proj-c", DatasetGeneration: "gen-1", ResourceType: "Patient",
		LogicalID: "dup-1", AuthResourcePath: "practitioner/pr-1",
		Payload: json.RawMessage(original),
	})

	_, err := repo.InsertResource(svc.ctx, source.Resource{
		Project: "proj-c", DatasetGeneration: "gen-1", ResourceType: "Patient",
		LogicalID: "dup-1", AuthResourcePath: "practitioner/pr-2",
		Payload: json.RawMessage(different),
	})
	if !errors.Is(err, ErrResourceConflict) {
		t.Fatalf("duplicate insert = %v, want ErrResourceConflict", err)
	}
	var conflict *ResourceConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error type = %T, want *ResourceConflictError", err)
	}
	wantConflict := &ResourceConflictError{
		Project: "proj-c", DatasetGeneration: "gen-1", ResourceType: "Patient",
		LogicalID: "dup-1", AuthResourcePath: "practitioner/pr-2",
	}
	if !reflect.DeepEqual(conflict, wantConflict) {
		t.Errorf("conflict = %+v, want %+v", conflict, wantConflict)
	}

	got := svc.mustGet(t, repo,
		restrictedScope("proj-c", "gen-1", "practitioner/pr-1"), "Patient", "dup-1")
	if got.ResourceSeq != originalSeq {
		t.Errorf("storage identity = %d after conflict, want the original %d: the row was replaced",
			got.ResourceSeq, originalSeq)
	}
	assertPayloadEqual(t, got.Payload, original)
	if got.AuthResourcePath != "practitioner/pr-1" {
		t.Errorf("auth path = %q after conflict, want practitioner/pr-1", got.AuthResourcePath)
	}

	if _, err := repo.InsertResource(svc.ctx, source.Resource{
		Project: "proj-c", DatasetGeneration: "gen-1", ResourceType: "Patient",
		LogicalID: "dup-1", AuthResourcePath: "practitioner/pr-1",
		Payload: json.RawMessage(original),
	}); !errors.Is(err, ErrResourceConflict) {
		t.Errorf("identical duplicate insert = %v, want ErrResourceConflict", err)
	}

	var count int64
	if err := svc.iso.QueryRow(svc.ctx,
		`SELECT count(*) FROM "`+svc.schema+`".fhir_resource
		 WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id = $4`,
		"proj-c", "gen-1", "Patient", "dup-1").Scan(&count); err != nil {
		svc.fail("count natural identity", err)
	}
	if count != 1 {
		t.Errorf("natural identity has %d rows, want 1", count)
	}
}

func TestResourceExactReadBindsScope(t *testing.T) {
	svc := newResourceService(t)
	svc.mustMigrateEmbedded(t)
	repo := svc.newRepository(t)

	seed := []source.Resource{
		{Project: "proj-a", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "p1", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"p1"}`)},
		{Project: "proj-a", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "p2", AuthResourcePath: "practitioner/pr-2", Payload: json.RawMessage(`{"id":"p2"}`)},
		{Project: "proj-a", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "p3", AuthResourcePath: "", Payload: json.RawMessage(`{"id":"p3"}`)},
		{Project: "proj-a", DatasetGeneration: "gen-2", ResourceType: "Patient", LogicalID: "p4", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"p4"}`)},
		{Project: "proj-b", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "p5", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"p5"}`)},
		{Project: "proj-a", DatasetGeneration: "gen-1", ResourceType: "Encounter", LogicalID: "p1", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"enc-p1"}`)},
	}
	for _, rec := range seed {
		svc.mustInsert(t, repo, rec)
	}

	got := svc.mustGet(t, repo, restrictedScope("proj-a", "gen-1", "practitioner/pr-1"), "Patient", "p1")
	assertRowScope(t, got, seed[0])
	assertPayloadEqual(t, got.Payload, `{"id":"p1"}`)

	got = svc.mustGet(t, repo, restrictedScope("proj-a", "gen-1", ""), "Patient", "p3")
	assertRowScope(t, got, seed[2])
	assertPayloadEqual(t, got.Payload, `{"id":"p3"}`)

	notFoundCases := []struct {
		name         string
		scope        source.ResourceScope
		resourceType string
		logicalID    string
	}{
		{"wrong path", restrictedScope("proj-a", "gen-1", "practitioner/pr-2"), "Patient", "p1"},
		{"wrong generation", restrictedScope("proj-a", "gen-1", "practitioner/pr-1"), "Patient", "p4"},
		{"other project", restrictedScope("proj-a", "gen-1", "practitioner/pr-1"), "Patient", "p5"},
		{"other resource type", restrictedScope("proj-a", "gen-1", "practitioner/pr-1"), "Encounter", "nope"},
		{"restricted with no paths", restrictedScope("proj-a", "gen-1"), "Patient", "p1"},
	}
	for _, tc := range notFoundCases {
		_, err := repo.GetResource(svc.ctx, tc.scope, tc.resourceType, tc.logicalID)
		if !errors.Is(err, ErrResourceNotFound) {
			t.Errorf("%s: GetResource = %v, want ErrResourceNotFound", tc.name, err)
			continue
		}
		var nf *ResourceNotFoundError
		if errors.As(err, &nf) && !nf.Restricted {
			t.Errorf("%s: not-found error %+v does not report restricted mode", tc.name, nf)
		}
	}

	got = svc.mustGet(t, repo, unrestrictedScope("proj-a", "gen-1"), "Patient", "p2")
	assertRowScope(t, got, seed[1])
	if _, err := repo.GetResource(svc.ctx, unrestrictedScope("proj-a", "gen-1"), "Patient", "p4"); !errors.Is(err, ErrResourceNotFound) {
		t.Errorf("unrestricted read across generations = %v, want ErrResourceNotFound", err)
	}
	if _, err := repo.GetResource(svc.ctx, unrestrictedScope("proj-a", "gen-1"), "Patient", "p5"); !errors.Is(err, ErrResourceNotFound) {
		t.Errorf("unrestricted read across projects = %v, want ErrResourceNotFound", err)
	}

	got = svc.mustGet(t, repo, restrictedScope("proj-b", "gen-1", "practitioner/pr-1"), "Patient", "p5")
	assertRowScope(t, got, seed[4])
}

func TestResourceKeysetReadStableOrderNeverCrossesScope(t *testing.T) {
	svc := newResourceService(t)
	svc.mustMigrateEmbedded(t)
	repo := svc.newRepository(t)

	inScope := []source.Resource{
		{Project: "proj-k", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "k1", AuthResourcePath: "practitioner/pr-2", Payload: json.RawMessage(`{"id":"k1"}`)},
		{Project: "proj-k", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "k2", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"k2"}`)},
		{Project: "proj-k", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "k3", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"k3"}`)},
		{Project: "proj-k", DatasetGeneration: "gen-1", ResourceType: "Observation", LogicalID: "k4", AuthResourcePath: "practitioner/pr-2", Payload: json.RawMessage(`{"id":"k4"}`)},
		{Project: "proj-k", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "k5", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"k5"}`)},
	}
	decoys := []source.Resource{
		{Project: "proj-x", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "x1", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"x1"}`)},
		{Project: "proj-k", DatasetGeneration: "gen-2", ResourceType: "Patient", LogicalID: "k6", AuthResourcePath: "practitioner/pr-1", Payload: json.RawMessage(`{"id":"k6"}`)},
		{Project: "proj-k", DatasetGeneration: "gen-1", ResourceType: "Patient", LogicalID: "k7", AuthResourcePath: "practitioner/pr-forbidden", Payload: json.RawMessage(`{"id":"k7"}`)},
		{Project: "proj-x", DatasetGeneration: "gen-2", ResourceType: "Patient", LogicalID: "x2", AuthResourcePath: "practitioner/pr-2", Payload: json.RawMessage(`{"id":"x2"}`)},
	}
	insertOrder := []source.Resource{
		inScope[2], decoys[0], inScope[0], decoys[1], inScope[4], decoys[2], inScope[1], decoys[3], inScope[3],
	}
	for _, rec := range insertOrder {
		svc.mustInsert(t, repo, rec)
	}
	wantSeqs := make([]int64, 0, len(inScope))
	for _, rec := range inScope {
		got := svc.mustGet(t, repo, restrictedScope("proj-k", "gen-1", "practitioner/pr-1", "practitioner/pr-2"), rec.ResourceType, rec.LogicalID)
		wantSeqs = append(wantSeqs, got.ResourceSeq)
	}
	if len(wantSeqs) != len(inScope) {
		t.Fatalf("seeded %d in-scope rows, witnessed %d", len(inScope), len(inScope))
	}
	if !(wantSeqs[2] < wantSeqs[0] && wantSeqs[0] < wantSeqs[4] && wantSeqs[4] < wantSeqs[1] && wantSeqs[1] < wantSeqs[3]) {
		t.Fatalf("seed storage order %+v is not k3<k1<k5<k2<k4; the contradiction this test relies on is gone", wantSeqs)
	}

	wantPages := [][]string{{"k3", "k1"}, {"k5", "k2"}, {"k4"}}
	scope := restrictedScope("proj-k", "gen-1", "practitioner/pr-1", "practitioner/pr-2")
	var walked []ResourceRow
	cursor := int64(0)
	pages := 0
	for {
		rows, hasMore := svc.mustRead(t, repo, scope, cursor, 2)
		if pages < len(wantPages) {
			if len(rows) != len(wantPages[pages]) {
				t.Fatalf("page %d returned %d rows, want %d", pages, len(rows), len(wantPages[pages]))
			}
			for i, rec := range rows {
				if rec.LogicalID != wantPages[pages][i] {
					t.Fatalf("page %d position %d returned %q, want %q: the read is not ordered by storage identity (lexical/page-array order leaked in)",
						pages, i, rec.LogicalID, wantPages[pages][i])
				}
			}
		}
		pages++
		if pages > 10 {
			t.Fatal("keyset walk did not terminate within 10 pages")
		}
		for i, rec := range rows {
			if i > 0 && rec.ResourceSeq <= rows[i-1].ResourceSeq {
				t.Fatalf("page not ascending by storage identity: %+v", rows)
			}
			if rec.Project != "proj-k" || rec.DatasetGeneration != "gen-1" {
				t.Fatalf("page crossed project/generation: %+v", rec)
			}
			if rec.AuthResourcePath != "practitioner/pr-1" && rec.AuthResourcePath != "practitioner/pr-2" {
				t.Fatalf("page crossed auth path: %+v", rec)
			}
			if rec.ResourceSeq <= cursor {
				t.Fatalf("row %+v did not advance past cursor %d", rec, cursor)
			}
		}
		walked = append(walked, rows...)
		if len(rows) > 0 {
			cursor = rows[len(rows)-1].ResourceSeq
		}
		if !hasMore {
			break
		}
	}
	if pages != len(wantPages) {
		t.Errorf("walked %d pages, want %d (five in-scope rows at limit 2)", pages, len(wantPages))
	}
	if len(walked) != len(inScope) {
		t.Fatalf("walk returned %d rows, want %d: %+v", len(walked), len(inScope), walked)
	}

	seenID := make(map[string]int, len(walked))
	for i, rec := range walked {
		seenID[rec.ResourceType+"/"+rec.LogicalID]++
		witness := svc.mustGet(t, repo, restrictedScope("proj-k", "gen-1", "practitioner/pr-1", "practitioner/pr-2"), rec.ResourceType, rec.LogicalID)
		if rec.ResourceSeq != witness.ResourceSeq {
			t.Fatalf("walk position %d has storage identity %d, want the witnessed %d for %s/%s",
				i, rec.ResourceSeq, witness.ResourceSeq, rec.ResourceType, rec.LogicalID)
		}
		if i > 0 && walked[i-1].ResourceSeq >= rec.ResourceSeq {
			t.Fatalf("walk is not strictly ascending by storage identity at position %d: %d then %d",
				i, walked[i-1].ResourceSeq, rec.ResourceSeq)
		}
		var want source.Resource
		for _, cand := range inScope {
			if cand.ResourceType == rec.ResourceType && cand.LogicalID == rec.LogicalID {
				want = cand
				break
			}
		}
		if want.LogicalID == "" {
			t.Fatalf("walk returned an unexpected row %+v", rec)
		}
		assertRowScope(t, rec, want)
		assertPayloadEqual(t, rec.Payload, string(want.Payload))
	}
	for _, rec := range inScope {
		if seenID[rec.ResourceType+"/"+rec.LogicalID] != 1 {
			t.Errorf("in-scope row %s/%s walked %d times, want exactly once",
				rec.ResourceType, rec.LogicalID, seenID[rec.ResourceType+"/"+rec.LogicalID])
		}
	}

	rows, hasMore := svc.mustRead(t, repo, scope, cursor, 2)
	if len(rows) != 0 || hasMore {
		t.Errorf("read past the end returned %d rows (hasMore=%t), want empty", len(rows), hasMore)
	}

	rows, hasMore, err := repo.ReadResources(svc.ctx, restrictedScope("proj-k", "gen-1"), 0, 10)
	if err != nil || len(rows) != 0 || hasMore {
		t.Errorf("restricted-empty read = (%d rows, hasMore=%t, err=%v), want empty with no error",
			len(rows), hasMore, err)
	}

	rows, hasMore = svc.mustRead(t, repo, unrestrictedScope("proj-k", "gen-1"), 0, MaxResourceKeysetLimit)
	if hasMore {
		t.Fatalf("unrestricted read reported more rows past a full page")
	}
	if len(rows) != len(inScope)+1 {
		t.Fatalf("unrestricted read returned %d rows, want %d (in-scope plus the forbidden-path row)",
			len(rows), len(inScope)+1)
	}
	for _, rec := range rows {
		if rec.Project != "proj-k" || rec.DatasetGeneration != "gen-1" {
			t.Fatalf("unrestricted read crossed project/generation: %+v", rec)
		}
	}
}

func TestResourceKeysetWalkStableUnderInserts(t *testing.T) {
	svc := newResourceService(t)
	svc.mustMigrateEmbedded(t)
	repo := svc.newRepository(t)
	scope := restrictedScope("proj-w", "gen-1", "practitioner/pr-1")

	mk := func(id string) source.Resource {
		return source.Resource{
			Project: "proj-w", DatasetGeneration: "gen-1", ResourceType: "Patient",
			LogicalID: id, AuthResourcePath: "practitioner/pr-1",
			Payload: json.RawMessage(`{"id":"` + id + `"}`),
		}
	}
	firstFour := []source.Resource{mk("w3"), mk("w1"), mk("w4"), mk("w2")}
	for _, rec := range firstFour {
		svc.mustInsert(t, repo, rec)
	}

	page1, hasMore := svc.mustRead(t, repo, scope, 0, 2)
	if len(page1) != 2 || !hasMore {
		t.Fatalf("page1 = %d rows (hasMore=%t), want 2 rows more=true", len(page1), hasMore)
	}
	if page1[0].LogicalID != "w3" || page1[1].LogicalID != "w1" {
		t.Fatalf("page1 returned %s,%s, want w3,w1: the read is not ordered by storage identity (lexical order leaked in)",
			page1[0].LogicalID, page1[1].LogicalID)
	}
	cursor := page1[1].ResourceSeq

	lastTwo := []source.Resource{mk("w6"), mk("w5")}
	for _, rec := range lastTwo {
		svc.mustInsert(t, repo, rec)
	}

	page2, hasMore := svc.mustRead(t, repo, scope, cursor, 2)
	if len(page2) != 2 || !hasMore {
		t.Fatalf("page2 = %d rows (hasMore=%t), want 2 rows more=true", len(page2), hasMore)
	}
	if page2[0].LogicalID != "w4" || page2[1].LogicalID != "w2" {
		t.Fatalf("page2 returned %s,%s, want w4,w2: the page boundary shifted under concurrent inserts",
			page2[0].LogicalID, page2[1].LogicalID)
	}
	cursor = page2[1].ResourceSeq

	page3, hasMore := svc.mustRead(t, repo, scope, cursor, 2)
	if len(page3) != 2 || hasMore {
		t.Fatalf("page3 = %d rows (hasMore=%t), want 2 rows more=false", len(page3), hasMore)
	}
	if page3[0].LogicalID != "w6" || page3[1].LogicalID != "w5" {
		t.Fatalf("page3 returned %s,%s, want w6,w5 (new rows must land at the end in storage order)",
			page3[0].LogicalID, page3[1].LogicalID)
	}

	seen := map[string]int{}
	for _, rec := range append(append(append([]ResourceRow(nil), page1...), page2...), page3...) {
		seen[rec.LogicalID]++
	}
	if len(seen) != 6 {
		t.Fatalf("walk saw %d distinct rows, want 6: %v", len(seen), seen)
	}
	for id, count := range seen {
		if count != 1 {
			t.Errorf("row %s returned %d times, want exactly once", id, count)
		}
	}
}

func TestResourceNoLeftovers(t *testing.T) {
	raw := testPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service: %v", err)
	}
	defer admin.Close()

	var leftDatabases []string
	rows, err := admin.Query(ctx,
		`SELECT datname FROM pg_database WHERE datname LIKE 'pgstore04_it_%' ORDER BY datname`)
	if err != nil {
		t.Fatalf("probe leftover databases: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("probe leftover databases scan: %v", err)
		}
		leftDatabases = append(leftDatabases, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("probe leftover databases rows: %v", err)
	}
	rows.Close()
	for _, name := range leftDatabases {
		if !pgstore04ResourceNamePattern.MatchString(name) {
			t.Fatalf("leftover database %q does not carry this ticket's prefix; refusing to drop it", name)
		}
		if _, err := admin.Exec(ctx, `DROP DATABASE IF EXISTS "`+name+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop leftover database %s: %v", name, err)
		} else {
			t.Errorf("dropped leftover database %s; an earlier resource test leaked it", name)
		}
	}

	var leftSchemas []string
	rows, err = admin.Query(ctx,
		`SELECT nspname FROM pg_namespace WHERE nspname LIKE 'pgstore04_it_%' ORDER BY nspname`)
	if err != nil {
		t.Fatalf("probe leftover schemas: %v", err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("probe leftover schemas scan: %v", err)
		}
		leftSchemas = append(leftSchemas, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("probe leftover schemas rows: %v", err)
	}
	rows.Close()
	for _, name := range leftSchemas {
		if !pgstore04ResourceNamePattern.MatchString(name) {
			t.Fatalf("leftover schema %q does not carry this ticket's prefix; refusing to drop it", name)
		}
		if _, err := admin.Exec(ctx, `DROP SCHEMA IF EXISTS "`+name+`" CASCADE`); err != nil {
			t.Errorf("drop leftover schema %s: %v", name, err)
		} else {
			t.Errorf("dropped leftover schema %s; an earlier resource test leaked it", name)
		}
	}
}
