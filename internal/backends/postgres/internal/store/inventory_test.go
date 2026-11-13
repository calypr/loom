package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgcat02StoreNamePattern = regexp.MustCompile(`^pgcat02_it_[0-9]+_[0-9a-f]+$`)

type inventoryStoreService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func newInventoryStoreService(t *testing.T) *inventoryStoreService {
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

	dbName = fmt.Sprintf("pgcat02_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgcat02StoreNamePattern.MatchString(dbName) {
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

	schema = fmt.Sprintf("pgcat02_it_%d_%s", time.Now().UnixNano(), randomHex(t))
	if !pgcat02StoreNamePattern.MatchString(schema) {
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

	return &inventoryStoreService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
}

func (s *inventoryStoreService) migrate(t *testing.T) {
	t.Helper()
	if _, err := MigrateSchema(s.ctx, s.openClient(t), s.schema); err != nil {
		s.fail("MigrateSchema", err)
	}
}

func (s *inventoryStoreService) openClient(t *testing.T) *Client {
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

func (s *inventoryStoreService) newRepository(t *testing.T) *InventoryRepository {
	t.Helper()
	repo, err := NewInventoryRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new inventory repository", err)
	}
	return repo
}

func (s *inventoryStoreService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *inventoryStoreService) seedResource(t *testing.T, project, generation, resourceType, logicalID, authPath string) {
	t.Helper()
	s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, resourceTableName}.Sanitize()+
		" (project, dataset_generation, resource_type, logical_id, auth_resource_path)"+
		" VALUES ($1, $2, $3, $4, $5)",
		project, generation, resourceType, logicalID, authPath)
}

type countRowWitness struct {
	CountKey          string
	BuildID           string
	Project           string
	DatasetGeneration string
	ResourceType      string
	AuthResourcePath  string
	DocumentCount     int64
}

func (s *inventoryStoreService) witnessCountRows(t *testing.T, buildID string) []countRowWitness {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "SELECT count_key, build_id, project, dataset_generation, resource_type, auth_resource_path, document_count"+
		" FROM "+pgx.Identifier{s.schema, inventoryCountTableName}.Sanitize()+
		" WHERE build_id = $1 ORDER BY resource_type, auth_resource_path", buildID)
	if err != nil {
		s.fail("witness count rows", err)
	}
	defer rows.Close()
	var out []countRowWitness
	for rows.Next() {
		var w countRowWitness
		if err := rows.Scan(&w.CountKey, &w.BuildID, &w.Project, &w.DatasetGeneration,
			&w.ResourceType, &w.AuthResourcePath, &w.DocumentCount); err != nil {
			s.fail("witness count rows", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness count rows", err)
	}
	return out
}

type markerWitness struct {
	MarkerKey         string
	Project           string
	DatasetGeneration string
	BuildID           string
	CoverageDigest    string
	ResourceTypes     []string
	ResourceTypeCount int
	CountRowCount     int
	Complete          bool
}

func (s *inventoryStoreService) witnessMarkers(t *testing.T, project, generation string) []markerWitness {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "SELECT marker_key, project, dataset_generation, build_id, coverage_digest,"+
		" resource_types, resource_type_count, count_row_count, complete"+
		" FROM "+pgx.Identifier{s.schema, inventoryBuildTableName}.Sanitize()+
		" WHERE project = $1 AND dataset_generation = $2 ORDER BY marker_key", project, generation)
	if err != nil {
		s.fail("witness markers", err)
	}
	defer rows.Close()
	var out []markerWitness
	for rows.Next() {
		var w markerWitness
		if err := rows.Scan(&w.MarkerKey, &w.Project, &w.DatasetGeneration, &w.BuildID, &w.CoverageDigest,
			&w.ResourceTypes, &w.ResourceTypeCount, &w.CountRowCount, &w.Complete); err != nil {
			s.fail("witness markers", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness markers", err)
	}
	return out
}

func mustInventoryBuild(t *testing.T, project, generation string, types []string, counts []catalog.ResourceInventoryCount) (catalog.ResourceInventoryBuild, []catalog.ResourceInventoryCount) {
	t.Helper()
	build, countRows, err := catalog.NewResourceInventoryBuild(project, generation, types, counts)
	if err != nil {
		t.Fatalf("NewResourceInventoryBuild: %v", err)
	}
	return build, countRows
}

func countFor(project, generation, authPath, resourceType string, documentCount int64) catalog.ResourceInventoryCount {
	return catalog.ResourceInventoryCount{
		Project: project, DatasetGeneration: generation, AuthResourcePath: authPath,
		ResourceType: resourceType, DocumentCount: documentCount,
	}
}

func explicitUnrestricted(v bool) *bool { return &v }

func TestInventoryScanSourceCountsGroupsAuthPaths(t *testing.T) {
	s := newInventoryStoreService(t)
	s.migrate(t)
	repo := s.newRepository(t)

	s.seedResource(t, "p", "g", "Patient", "p1", "a")
	s.seedResource(t, "p", "g", "Patient", "p2", "a")
	s.seedResource(t, "p", "g", "Patient", "p3", "b")
	s.seedResource(t, "p", "g", "Patient", "p4", "")
	s.seedResource(t, "p", "g2", "Patient", "q1", "a")
	s.seedResource(t, "p", "g", "Observation", "o1", "z")
	s.seedResource(t, "o", "g", "Patient", "x1", "a")

	groups, err := repo.ScanSourceCounts(s.ctx, "p", "g", "Patient")
	if err != nil {
		t.Fatalf("ScanSourceCounts: %v", err)
	}
	want := []InventorySourceGroup{{AuthResourcePath: "", DocumentCount: 1}, {AuthResourcePath: "a", DocumentCount: 2}, {AuthResourcePath: "b", DocumentCount: 1}}
	if fmt.Sprint(groups) != fmt.Sprint(want) {
		t.Fatalf("Patient groups = %v, want %v", groups, want)
	}

	groups, err = repo.ScanSourceCounts(s.ctx, "p", "g", "Encounter")
	if err != nil {
		t.Fatalf("ScanSourceCounts Encounter: %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("Encounter groups = %v, want none", groups)
	}

	if _, err := repo.ScanSourceCounts(s.ctx, "", "g", "Patient"); !errors.Is(err, ErrInvalidInventoryScope) {
		t.Fatalf("blank project error = %v, want ErrInvalidInventoryScope", err)
	}
	if _, err := repo.ScanSourceCounts(s.ctx, "p", "g", " "); !errors.Is(err, ErrInvalidInventoryScope) {
		t.Fatalf("blank type error = %v, want ErrInvalidInventoryScope", err)
	}
}

func TestInventoryInsertCountsIsRepeatStableAndRejectsDivergence(t *testing.T) {
	s := newInventoryStoreService(t)
	s.migrate(t)
	repo := s.newRepository(t)

	build, counts := mustInventoryBuild(t, "p", "g", []string{"Patient", "Observation"}, []catalog.ResourceInventoryCount{
		countFor("p", "g", "a", "Patient", 2),
		countFor("p", "g", "b", "Patient", 1),
		countFor("p", "g", "", "Observation", 0),
	})

	if err := repo.InsertCounts(s.ctx, counts, 2); err != nil {
		t.Fatalf("first InsertCounts: %v", err)
	}
	rows := s.witnessCountRows(t, build.BuildID)
	if len(rows) != 3 {
		t.Fatalf("committed count rows = %d, want 3", len(rows))
	}
	want := []countRowWitness{
		{CountKey: counts[0].Key, BuildID: build.BuildID, Project: "p", DatasetGeneration: "g", ResourceType: "Observation", AuthResourcePath: "", DocumentCount: 0},
		{CountKey: counts[1].Key, BuildID: build.BuildID, Project: "p", DatasetGeneration: "g", ResourceType: "Patient", AuthResourcePath: "a", DocumentCount: 2},
		{CountKey: counts[2].Key, BuildID: build.BuildID, Project: "p", DatasetGeneration: "g", ResourceType: "Patient", AuthResourcePath: "b", DocumentCount: 1},
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("committed rows = %v, want %v", rows, want)
	}
	for _, row := range rows {
		if !strings.HasPrefix(row.CountKey, "ric_") {
			t.Fatalf("count key %q is not a ric_ identity", row.CountKey)
		}
	}

	if err := repo.InsertCounts(s.ctx, counts, 2); err != nil {
		t.Fatalf("repeat InsertCounts: %v", err)
	}
	if again := s.witnessCountRows(t, build.BuildID); len(again) != 3 {
		t.Fatalf("rows after repeat = %d, want 3", len(again))
	}

	s.exec(t, "UPDATE "+pgx.Identifier{s.schema, inventoryCountTableName}.Sanitize()+
		" SET document_count = 99 WHERE count_key = $1", counts[1].Key)
	err := repo.InsertCounts(s.ctx, counts, 2)
	var conflict *InventoryCountConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, ErrInventoryCountConflict) {
		t.Fatalf("divergent insert error = %v, want *InventoryCountConflictError", err)
	}
	if conflict.Field != "document_count" || conflict.Stored != "99" || conflict.Requested != "2" {
		t.Fatalf("conflict detail = %+v", conflict)
	}
	var stored int64
	if err := s.iso.QueryRow(s.ctx, "SELECT document_count FROM "+pgx.Identifier{s.schema, inventoryCountTableName}.Sanitize()+
		" WHERE count_key = $1", counts[1].Key).Scan(&stored); err != nil {
		s.fail("read tampered row", err)
	}
	if stored != 99 {
		t.Fatalf("stored count = %d, want the tampered 99 (insert must never replace)", stored)
	}

	forged := catalog.ResourceInventoryCount{
		Key: "ric_forged_samecount", RecordType: catalog.ResourceInventoryCountRecordType,
		BuildID: counts[0].BuildID, Project: counts[0].Project, DatasetGeneration: counts[0].DatasetGeneration,
		AuthResourcePath: counts[0].AuthResourcePath, ResourceType: counts[0].ResourceType,
		DocumentCount: counts[0].DocumentCount,
	}
	err = repo.InsertCounts(s.ctx, []catalog.ResourceInventoryCount{forged}, 10)
	var keyConflict *InventoryCountConflictError
	if !errors.As(err, &keyConflict) || keyConflict.Field != "count_key" ||
		keyConflict.Stored != counts[0].Key || keyConflict.Requested != "ric_forged_samecount" {
		t.Fatalf("forged-key reinsert error = %v, want typed count_key conflict", err)
	}
	var storedKey string
	if err := s.iso.QueryRow(s.ctx, "SELECT count_key FROM "+pgx.Identifier{s.schema, inventoryCountTableName}.Sanitize()+
		" WHERE build_id = $1 AND project = $2 AND dataset_generation = $3 AND resource_type = $4 AND auth_resource_path = $5",
		counts[0].BuildID, counts[0].Project, counts[0].DatasetGeneration, counts[0].ResourceType, counts[0].AuthResourcePath,
	).Scan(&storedKey); err != nil {
		s.fail("read stored key", err)
	}
	if storedKey != counts[0].Key {
		t.Fatalf("stored count key = %q, want the authentic %q (never replaced)", storedKey, counts[0].Key)
	}

	if err := repo.InsertCounts(s.ctx, []catalog.ResourceInventoryCount{counts[0]}, 10); err != nil {
		t.Fatalf("identical single-row reinsert: %v", err)
	}

	if err := repo.InsertCounts(s.ctx, nil, 2); !errors.Is(err, ErrInvalidInventoryRow) {
		t.Fatalf("empty insert error = %v, want ErrInvalidInventoryRow", err)
	}
	if err := repo.InsertCounts(s.ctx, counts, MaxInventoryWriteBatch+1); !errors.Is(err, ErrInvalidInventoryScope) {
		t.Fatalf("oversized batch error = %v, want ErrInvalidInventoryScope", err)
	}
	duplicate := append([]catalog.ResourceInventoryCount{}, counts...)
	duplicate = append(duplicate, counts[0])
	if err := repo.InsertCounts(s.ctx, duplicate, 10); !errors.Is(err, ErrInvalidInventoryRow) {
		t.Fatalf("duplicate scope error = %v, want ErrInvalidInventoryRow", err)
	}
	negative := counts
	negative[0].DocumentCount = -1
	if err := repo.InsertCounts(s.ctx, negative, 10); !errors.Is(err, ErrInvalidInventoryRow) {
		t.Fatalf("negative count error = %v, want ErrInvalidInventoryRow", err)
	}
}

func TestInventoryRepositoryRejectsCrossScopeIdentityCollisions(t *testing.T) {
	s := newInventoryStoreService(t)
	s.migrate(t)
	repo := s.newRepository(t)

	_, counts := mustInventoryBuild(t, "p", "g", []string{"Patient"},
		[]catalog.ResourceInventoryCount{countFor("p", "g", "a", "Patient", 2)})
	if err := repo.InsertCounts(s.ctx, counts, 10); err != nil {
		t.Fatalf("baseline InsertCounts: %v", err)
	}
	forgedKeyRow := catalog.ResourceInventoryCount{
		Key: counts[0].Key, RecordType: catalog.ResourceInventoryCountRecordType,
		BuildID: "sha256:" + strings.Repeat("f", 64), Project: "p", DatasetGeneration: "g",
		AuthResourcePath: "z", ResourceType: "Patient", DocumentCount: 1,
	}
	err := repo.InsertCounts(s.ctx, []catalog.ResourceInventoryCount{forgedKeyRow}, 10)
	if !errors.Is(err, ErrInventoryCountConflict) {
		t.Fatalf("cross-scope count_key insert error = %v, want ErrInventoryCountConflict", err)
	}

	build1, counts1 := mustInventoryBuild(t, "p", "g", []string{"Patient"},
		[]catalog.ResourceInventoryCount{countFor("p", "g", "b", "Patient", 1)})
	if err := repo.PublishBuild(s.ctx, build1); !errors.Is(err, ErrInventoryPublishGuard) {
		t.Fatalf("publish before own rows = %v, want guard error", err)
	}
	if err := repo.InsertCounts(s.ctx, counts1, 10); err != nil {
		t.Fatalf("commit build1 rows: %v", err)
	}
	build1.Key = "ribm_forged_cross_scope"
	if err := repo.PublishBuild(s.ctx, build1); err != nil {
		t.Fatalf("publish build1 with forged key: %v", err)
	}
	build2, counts2 := mustInventoryBuild(t, "p", "g2", []string{"Patient"},
		[]catalog.ResourceInventoryCount{countFor("p", "g2", "c", "Patient", 4)})
	if err := repo.InsertCounts(s.ctx, counts2, 10); err != nil {
		t.Fatalf("commit build2 rows: %v", err)
	}
	build2.Key = "ribm_forged_cross_scope"
	err = repo.PublishBuild(s.ctx, build2)
	if err == nil || !strings.Contains(err.Error(), "collides across scopes") {
		t.Fatalf("cross-scope marker_key publish error = %v, want corrupted-derivation classification", err)
	}
	if markers := s.witnessMarkers(t, "p", "g2"); len(markers) != 0 {
		t.Fatalf("markers for g2 after collision = %+v, want none", markers)
	}
	markers := s.witnessMarkers(t, "p", "g")
	if len(markers) != 1 || markers[0].MarkerKey != "ribm_forged_cross_scope" {
		t.Fatalf("first marker damaged by refused publish: %+v", markers)
	}
}

func TestInventoryPublishBuildGuardsUncommittedCounts(t *testing.T) {
	s := newInventoryStoreService(t)
	s.migrate(t)
	repo := s.newRepository(t)

	build, counts := mustInventoryBuild(t, "p", "g", []string{"Patient", "Observation"}, []catalog.ResourceInventoryCount{
		countFor("p", "g", "a", "Patient", 2),
		countFor("p", "g", "b", "Patient", 1),
		countFor("p", "g", "", "Observation", 0),
	})

	if err := repo.InsertCounts(s.ctx, counts[:1], 10); err != nil {
		t.Fatalf("partial InsertCounts: %v", err)
	}
	err := repo.PublishBuild(s.ctx, build)
	var guard *InventoryPublishGuardError
	if !errors.As(err, &guard) || !errors.Is(err, ErrInventoryPublishGuard) {
		t.Fatalf("premature publish error = %v, want *InventoryPublishGuardError", err)
	}
	if guard.CommittedRowCount != 1 || guard.ClaimedRowCount != 3 || fmt.Sprint(guard.MissingResourceTypes) != "[Patient]" {
		t.Fatalf("guard detail = %+v", guard)
	}
	if markers := s.witnessMarkers(t, "p", "g"); len(markers) != 0 {
		t.Fatalf("markers after guard failure = %v, want none", markers)
	}

	if err := repo.InsertCounts(s.ctx, counts[1:2], 10); err != nil {
		t.Fatalf("second InsertCounts: %v", err)
	}
	if err := repo.PublishBuild(s.ctx, build); !errors.Is(err, ErrInventoryPublishGuard) {
		t.Fatalf("gap-only publish = %v, want ErrInventoryPublishGuard", err)
	}

	if err := repo.InsertCounts(s.ctx, counts[2:], 10); err != nil {
		t.Fatalf("final InsertCounts: %v", err)
	}
	if err := repo.PublishBuild(s.ctx, build); err != nil {
		t.Fatalf("PublishBuild: %v", err)
	}
	markers := s.witnessMarkers(t, "p", "g")
	if len(markers) != 1 {
		t.Fatalf("markers = %v, want exactly one", markers)
	}
	wantMarker := markerWitness{
		MarkerKey: build.Key, Project: "p", DatasetGeneration: "g", BuildID: build.BuildID,
		CoverageDigest: build.CoverageDigest, ResourceTypes: []string{"Observation", "Patient"},
		ResourceTypeCount: 2, CountRowCount: 3, Complete: true,
	}
	if fmt.Sprint(markers[0]) != fmt.Sprint(wantMarker) {
		t.Fatalf("marker = %v, want %v", markers[0], wantMarker)
	}

	got, found, err := repo.ReadBuild(s.ctx, "p", "g")
	if err != nil || !found {
		t.Fatalf("ReadBuild = %v, %v, err=%v", got, found, err)
	}
	if got.Key != build.Key || got.BuildID != build.BuildID || got.CoverageDigest != build.CoverageDigest ||
		!got.Complete || got.ResourceTypeCount != 2 || got.CountRowCount != 3 ||
		got.RecordType != catalog.ResourceInventoryBuildRecordType || got.Project != "p" || got.DatasetGeneration != "g" {
		t.Fatalf("ReadBuild marker = %#v", got)
	}
	if _, found, err := repo.ReadBuild(s.ctx, "p", "never"); err != nil || found {
		t.Fatalf("ReadBuild unknown scope = %v, %v, err=%v; want absent", got, found, err)
	}

	if err := repo.PublishBuild(s.ctx, build); err != nil {
		t.Fatalf("republish: %v", err)
	}
	if markers := s.witnessMarkers(t, "p", "g"); len(markers) != 1 {
		t.Fatalf("markers after republish = %v, want one", markers)
	}

	broken := build
	broken.Complete = false
	if err := repo.PublishBuild(s.ctx, broken); !errors.Is(err, ErrInvalidInventoryRow) {
		t.Fatalf("incomplete marker publish = %v, want ErrInvalidInventoryRow", err)
	}
}

func TestInventoryReadCountSumsBindsScopeAndBuild(t *testing.T) {
	s := newInventoryStoreService(t)
	s.migrate(t)
	repo := s.newRepository(t)

	build, counts := mustInventoryBuild(t, "p", "g", []string{"Patient", "Observation"}, []catalog.ResourceInventoryCount{
		countFor("p", "g", "a", "Patient", 2),
		countFor("p", "g", "b", "Patient", 1),
		countFor("p", "g", "", "Observation", 0),
	})
	if err := repo.InsertCounts(s.ctx, counts, 10); err != nil {
		t.Fatalf("InsertCounts: %v", err)
	}
	if err := repo.PublishBuild(s.ctx, build); err != nil {
		t.Fatalf("PublishBuild: %v", err)
	}

	s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, inventoryCountTableName}.Sanitize()+
		" (count_key, build_id, project, dataset_generation, resource_type, auth_resource_path, document_count)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7)",
		"ric_forged_abandoned", "sha256:"+strings.Repeat("a", 64), "p", "g", "Patient", "a", int64(999999))
	s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, inventoryCountTableName}.Sanitize()+
		" (count_key, build_id, project, dataset_generation, resource_type, auth_resource_path, document_count)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7)",
		"ric_forged_other_project", build.BuildID, "other", "g", "Patient", "a", int64(888888))
	s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, inventoryCountTableName}.Sanitize()+
		" (count_key, build_id, project, dataset_generation, resource_type, auth_resource_path, document_count)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7)",
		"ric_forged_other_generation", build.BuildID, "p", "other", "Patient", "a", int64(777777))

	restricted := func(paths []string) map[string]int64 {
		t.Helper()
		sums, err := repo.ReadCountSums(s.ctx, InventoryCountScope{
			Project: "p", DatasetGeneration: "g", BuildID: build.BuildID,
			ResourceTypes:                 []string{"Patient", "Observation"},
			AuthResourcePathsUnrestricted: explicitUnrestricted(false),
			AuthResourcePaths:             paths,
		})
		if err != nil {
			t.Fatalf("restricted ReadCountSums(%v): %v", paths, err)
		}
		return sums
	}
	if got := restricted([]string{"a"}); fmt.Sprint(got) != fmt.Sprint(map[string]int64{"Patient": 2}) {
		t.Fatalf("restricted [a] sums = %v, want Patient=2 only", got)
	}
	if got := restricted([]string{"a", "b"}); fmt.Sprint(got) != fmt.Sprint(map[string]int64{"Patient": 3}) {
		t.Fatalf("restricted [a b] sums = %v, want Patient=3 only", got)
	}
	if got := restricted([]string{}); len(got) != 0 {
		t.Fatalf("restricted empty sums = %v, want empty", got)
	}

	unrestricted, err := repo.ReadCountSums(s.ctx, InventoryCountScope{
		Project: "p", DatasetGeneration: "g", BuildID: build.BuildID,
		ResourceTypes:                 []string{"Patient", "Observation"},
		AuthResourcePathsUnrestricted: explicitUnrestricted(true),
	})
	if err != nil {
		t.Fatalf("unrestricted ReadCountSums: %v", err)
	}
	if fmt.Sprint(unrestricted) != fmt.Sprint(map[string]int64{"Patient": 3, "Observation": 0}) {
		t.Fatalf("unrestricted sums = %v, want Patient=3 Observation=0", unrestricted)
	}

	if _, err := repo.ReadCountSums(s.ctx, InventoryCountScope{
		Project: "p", DatasetGeneration: "g", BuildID: build.BuildID, ResourceTypes: []string{"Patient"},
	}); !errors.Is(err, ErrInvalidInventoryScope) {
		t.Fatalf("nil unrestricted flag error = %v, want ErrInvalidInventoryScope", err)
	}
	if _, err := repo.ReadCountSums(s.ctx, InventoryCountScope{
		Project: "p", DatasetGeneration: "g", BuildID: build.BuildID, ResourceTypes: []string{"Patient"},
		AuthResourcePathsUnrestricted: explicitUnrestricted(true), AuthResourcePaths: []string{"a"},
	}); !errors.Is(err, ErrInvalidInventoryScope) {
		t.Fatalf("unrestricted-with-paths error = %v, want ErrInvalidInventoryScope", err)
	}
	if _, err := repo.ReadCountSums(s.ctx, InventoryCountScope{
		Project: "p", DatasetGeneration: "g", AuthResourcePathsUnrestricted: explicitUnrestricted(true),
	}); !errors.Is(err, ErrInvalidInventoryScope) {
		t.Fatalf("blank build id error = %v, want ErrInvalidInventoryScope", err)
	}
}
