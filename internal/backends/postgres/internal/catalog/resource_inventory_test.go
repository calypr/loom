package postgres

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgcat02CatalogNamePattern = regexp.MustCompile(`^pgcat02_it_[0-9]+_[0-9a-f]+$`)

type catalogInventoryService struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func catalogTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func catalogRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func newCatalogInventoryService(t *testing.T) *catalogInventoryService {
	t.Helper()
	raw := catalogTestPostgresURL(t)

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

	dbName = fmt.Sprintf("pgcat02_it_%d_%s", time.Now().UnixNano(), catalogRandomHex(t))
	if !pgcat02CatalogNamePattern.MatchString(dbName) {
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

	schema = fmt.Sprintf("pgcat02_it_%d_%s", time.Now().UnixNano(), catalogRandomHex(t))
	if !pgcat02CatalogNamePattern.MatchString(schema) {
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

	service := &catalogInventoryService{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
	if _, err := storepostgres.MigrateSchema(ctx, service.openClient(t), schema); err != nil {
		fail("MigrateSchema", err)
	}
	return service
}

func (s *catalogInventoryService) openClient(t *testing.T) *storepostgres.Client {
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

func (s *catalogInventoryService) realRepository(t *testing.T) *storepostgres.InventoryRepository {
	t.Helper()
	repo, err := storepostgres.NewInventoryRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new inventory repository", err)
	}
	return repo
}

func (s *catalogInventoryService) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *catalogInventoryService) seedResource(t *testing.T, project, generation, resourceType, logicalID, authPath string) {
	t.Helper()
	s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, "fhir_resource"}.Sanitize()+
		" (project, dataset_generation, resource_type, logical_id, auth_resource_path)"+
		" VALUES ($1, $2, $3, $4, $5)",
		project, generation, resourceType, logicalID, authPath)
}

type catalogCountWitness struct {
	CountKey         string
	BuildID          string
	ResourceType     string
	AuthResourcePath string
	DocumentCount    int64
}

func (s *catalogInventoryService) witnessCountRows(t *testing.T, buildID string) []catalogCountWitness {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "SELECT count_key, build_id, resource_type, auth_resource_path, document_count"+
		" FROM "+pgx.Identifier{s.schema, "fhir_resource_inventory_count"}.Sanitize()+
		" WHERE build_id = $1 ORDER BY resource_type, auth_resource_path", buildID)
	if err != nil {
		s.fail("witness count rows", err)
	}
	defer rows.Close()
	var out []catalogCountWitness
	for rows.Next() {
		var w catalogCountWitness
		if err := rows.Scan(&w.CountKey, &w.BuildID, &w.ResourceType, &w.AuthResourcePath, &w.DocumentCount); err != nil {
			s.fail("witness count rows", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness count rows", err)
	}
	return out
}

type catalogMarkerWitness struct {
	MarkerKey     string
	BuildID       string
	Complete      bool
	CountRowCount int
}

func (s *catalogInventoryService) witnessMarkers(t *testing.T, project, generation string) []catalogMarkerWitness {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "SELECT marker_key, build_id, complete, count_row_count"+
		" FROM "+pgx.Identifier{s.schema, "fhir_resource_inventory_build"}.Sanitize()+
		" WHERE project = $1 AND dataset_generation = $2", project, generation)
	if err != nil {
		s.fail("witness markers", err)
	}
	defer rows.Close()
	var out []catalogMarkerWitness
	for rows.Next() {
		var w catalogMarkerWitness
		if err := rows.Scan(&w.MarkerKey, &w.BuildID, &w.Complete, &w.CountRowCount); err != nil {
			s.fail("witness markers", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness markers", err)
	}
	return out
}

func (s *catalogInventoryService) totals(result catalog.ResourceInventoryResult) map[string]int64 {
	got := map[string]int64{}
	for _, value := range result.Values {
		got[value.ResourceType] = value.DocumentCount
	}
	return got
}

type faultedInventory struct {
	*storepostgres.InventoryRepository
	calls catalogInventoryCalls

	failOnInsert    int
	splitBeforeFail bool
	injected        error

	failOnPublish int
}

type catalogInventoryCalls struct {
	scan      int
	insert    int
	publish   int
	readBuild int
	readSums  int
}

func (f *faultedInventory) ScanSourceCounts(ctx context.Context, project, datasetGeneration, resourceType string) ([]storepostgres.InventorySourceGroup, error) {
	f.calls.scan++
	return f.InventoryRepository.ScanSourceCounts(ctx, project, datasetGeneration, resourceType)
}

func (f *faultedInventory) InsertCounts(ctx context.Context, rows []catalog.ResourceInventoryCount, batchSize int) error {
	f.calls.insert++
	if f.failOnInsert > 0 && f.calls.insert == f.failOnInsert {
		if f.splitBeforeFail && len(rows) > 1 {
			if err := f.InventoryRepository.InsertCounts(ctx, rows[:1], batchSize); err != nil {
				return err
			}
		}
		return f.injected
	}
	return f.InventoryRepository.InsertCounts(ctx, rows, batchSize)
}

func (f *faultedInventory) PublishBuild(ctx context.Context, build catalog.ResourceInventoryBuild) error {
	f.calls.publish++
	if f.failOnPublish > 0 && f.calls.publish == f.failOnPublish {
		return f.injected
	}
	return f.InventoryRepository.PublishBuild(ctx, build)
}

func (f *faultedInventory) ReadBuild(ctx context.Context, project, datasetGeneration string) (catalog.ResourceInventoryBuild, bool, error) {
	f.calls.readBuild++
	return f.InventoryRepository.ReadBuild(ctx, project, datasetGeneration)
}

func (f *faultedInventory) ReadCountSums(ctx context.Context, scope storepostgres.InventoryCountScope) (map[string]int64, error) {
	f.calls.readSums++
	return f.InventoryRepository.ReadCountSums(ctx, scope)
}

func requireUnavailable(t *testing.T, result catalog.ResourceInventoryResult, err error, wantReasonPart string) {
	t.Helper()
	if err == nil || !errors.Is(err, ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("error = %v, want errors.Is ErrResourceInventoryBuildUnavailable", err)
	}
	var typed *ResourceInventoryUnavailableError
	if !errors.As(err, &typed) {
		t.Fatalf("error %v is not a typed *ResourceInventoryUnavailableError", err)
	}
	if !strings.Contains(typed.Reason, wantReasonPart) {
		t.Fatalf("unavailable reason = %q, want it to contain %q", typed.Reason, wantReasonPart)
	}
	if result.Available || result.Complete || result.Status != catalog.EvidenceUnavailable || len(result.Values) != 0 {
		t.Fatalf("unavailable result = %#v", result)
	}
}

func (s *catalogInventoryService) literalFixture(t *testing.T, project, generation string) {
	t.Helper()
	s.seedResource(t, project, generation, "Patient", "p1", "a")
	s.seedResource(t, project, generation, "Patient", "p2", "a")
	s.seedResource(t, project, generation, "Patient", "p3", "b")
	s.seedResource(t, project, generation, "Patient", "p4", "b")
	s.seedResource(t, project, generation, "Patient", "p5", "b")
	s.seedResource(t, project, generation, "Patient", "p6", "b")
	s.seedResource(t, project, generation, "Patient", "p7", "b")
	s.seedResource(t, project, generation, "Practitioner", "r1", "")
}

func inventoryRebuildOptions(project, generation string, types ...string) catalog.ResourceInventoryRebuildOptions {
	return catalog.ResourceInventoryRebuildOptions{
		Project: project, DatasetGeneration: generation, ResourceTypes: types, BatchSize: 2,
	}
}

func inventoryReadOptions(project, generation string, unrestricted bool, paths []string, types ...string) catalog.ResourceInventoryOptions {
	flag := unrestricted
	return catalog.ResourceInventoryOptions{
		Project: project, DatasetGeneration: generation, ResourceTypes: types,
		AuthResourcePathsUnrestricted: &flag, AuthResourcePaths: paths,
	}
}

func TestResourceInventoryRebuildGroupsSourceRowsAndPublishesMarkerLast(t *testing.T) {
	s := newCatalogInventoryService(t)
	faulted := &faultedInventory{InventoryRepository: s.realRepository(t)}
	adapter, err := New(faulted)
	if err != nil {
		t.Fatal(err)
	}
	s.literalFixture(t, "p", "g")

	summary, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	if err != nil {
		t.Fatalf("RebuildResourceInventory: %v", err)
	}
	if summary.BuildID == "" || summary.CoverageDigest == "" ||
		summary.ResourceTypeCount != 3 || summary.CountRowCount != 4 || summary.DocumentCount != 8 {
		t.Fatalf("summary = %#v", summary)
	}

	if faulted.calls.scan != 3 {
		t.Fatalf("source scans = %d, want one per declared type", faulted.calls.scan)
	}
	rows := s.witnessCountRows(t, summary.BuildID)
	want := []catalogCountWitness{
		{BuildID: summary.BuildID, ResourceType: "Observation", AuthResourcePath: "", DocumentCount: 0},
		{BuildID: summary.BuildID, ResourceType: "Patient", AuthResourcePath: "a", DocumentCount: 2},
		{BuildID: summary.BuildID, ResourceType: "Patient", AuthResourcePath: "b", DocumentCount: 5},
		{BuildID: summary.BuildID, ResourceType: "Practitioner", AuthResourcePath: "", DocumentCount: 1},
	}
	if len(rows) != len(want) {
		t.Fatalf("committed rows = %v, want %d rows", rows, len(want))
	}
	for i := range want {
		if rows[i].BuildID != want[i].BuildID || rows[i].ResourceType != want[i].ResourceType ||
			rows[i].AuthResourcePath != want[i].AuthResourcePath || rows[i].DocumentCount != want[i].DocumentCount {
			t.Fatalf("row %d = %v, want %v", i, rows[i], want[i])
		}
		if !strings.HasPrefix(rows[i].CountKey, "ric_") {
			t.Fatalf("row %d count key %q is not a ric_ identity", i, rows[i].CountKey)
		}
	}

	markers := s.witnessMarkers(t, "p", "g")
	if len(markers) != 1 {
		t.Fatalf("markers = %v, want one", markers)
	}
	if markers[0].BuildID != summary.BuildID || !markers[0].Complete || markers[0].CountRowCount != 4 {
		t.Fatalf("marker = %+v", markers[0])
	}
	if markers[0].MarkerKey != catalog.ResourceInventoryBuildKey("p", "g") {
		t.Fatalf("marker key = %q, want %q", markers[0].MarkerKey, catalog.ResourceInventoryBuildKey("p", "g"))
	}

	result, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Observation", "Patient", "Practitioner"))
	if err != nil || result.Status != catalog.EvidenceAvailable {
		t.Fatalf("inventory = %#v err=%v", result, err)
	}
	if fmt.Sprint(s.totals(result)) != fmt.Sprint(map[string]int64{"Practitioner": 1, "Observation": 0, "Patient": 7}) {
		t.Fatalf("totals = %v", s.totals(result))
	}
}

func TestResourceInventoryCountWriteFailureKeepsPriorReadAndExposesTypedErrors(t *testing.T) {
	s := newCatalogInventoryService(t)
	faulted := &faultedInventory{InventoryRepository: s.realRepository(t)}
	adapter, err := New(faulted)
	if err != nil {
		t.Fatal(err)
	}
	s.literalFixture(t, "p", "g")

	first, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	if err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	before, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Observation", "Patient", "Practitioner"))
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	beforeDigest := before.Digest

	s.seedResource(t, "p", "g", "Patient", "p8", "a")
	injected := errors.New("injected count-write failure")
	faulted.failOnInsert = 2
	faulted.splitBeforeFail = true
	faulted.injected = injected

	_, err = adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	var writeErr *ResourceInventoryWriteError
	if err == nil || !errors.As(err, &writeErr) || !errors.Is(err, ErrResourceInventoryWrite) {
		t.Fatalf("failed rebuild error = %v, want *ResourceInventoryWriteError", err)
	}
	if writeErr.Op != "counts" || !errors.Is(err, injected) {
		t.Fatalf("write error op = %q or injected cause missing: %v", writeErr.Op, err)
	}
	if !strings.Contains(err.Error(), "not published") {
		t.Fatalf("write error is not actionable: %v", err)
	}

	failedBuild, _, err := catalog.NewResourceInventoryBuild("p", "g",
		[]string{"Observation", "Patient", "Practitioner"},
		[]catalog.ResourceInventoryCount{
			{Project: "p", DatasetGeneration: "g", AuthResourcePath: "", ResourceType: "Observation", DocumentCount: 0},
			{Project: "p", DatasetGeneration: "g", AuthResourcePath: "a", ResourceType: "Patient", DocumentCount: 3},
			{Project: "p", DatasetGeneration: "g", AuthResourcePath: "b", ResourceType: "Patient", DocumentCount: 5},
			{Project: "p", DatasetGeneration: "g", AuthResourcePath: "", ResourceType: "Practitioner", DocumentCount: 1},
		})
	if err != nil {
		t.Fatalf("derive failed build identity: %v", err)
	}
	if rows := s.witnessCountRows(t, failedBuild.BuildID); len(rows) != 1 || rows[0].ResourceType != "Observation" || rows[0].DocumentCount != 0 {
		t.Fatalf("partial rows of the failed build = %v, want the committed first row", rows)
	}

	after := s.witnessMarkers(t, "p", "g")
	if len(after) != 1 || after[0].BuildID != first.BuildID || !after[0].Complete {
		t.Fatalf("marker after failure = %+v, want the first build intact", after)
	}
	intact, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Observation", "Patient", "Practitioner"))
	if err != nil || intact.Status != catalog.EvidenceAvailable || intact.Digest != beforeDigest {
		t.Fatalf("read after failure = %#v err=%v, want the prior result", intact, err)
	}
	if fmt.Sprint(s.totals(intact)) != fmt.Sprint(map[string]int64{"Practitioner": 1, "Observation": 0, "Patient": 7}) {
		t.Fatalf("totals after failure = %v, want the prior totals", s.totals(intact))
	}
	faulted.calls = catalogInventoryCalls{}
	if _, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Patient")); err != nil {
		t.Fatalf("read: %v", err)
	}
	if faulted.calls.readSums != 1 {
		t.Fatalf("count reads = %d, want exactly one (marker-bound)", faulted.calls.readSums)
	}

	s.seedResource(t, "p", "g2", "Patient", "q1", "z")
	faulted.failOnInsert = faulted.calls.insert + 1
	faulted.splitBeforeFail = false
	_, err = adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g2", "Patient"))
	if err == nil || !errors.Is(err, ErrResourceInventoryWrite) || !errors.Is(err, injected) {
		t.Fatalf("fresh rebuild error = %v, want injected write failure", err)
	}
	result, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g2", true, nil, "Patient"))
	requireUnavailable(t, result, err, "marker is missing")
	if markers := s.witnessMarkers(t, "p", "g2"); len(markers) != 0 {
		t.Fatalf("markers after failed first rebuild = %+v, want none", markers)
	}
	expected, _, err := catalog.NewResourceInventoryBuild("p", "g2", []string{"Patient"},
		[]catalog.ResourceInventoryCount{{Project: "p", DatasetGeneration: "g2", AuthResourcePath: "z", ResourceType: "Patient", DocumentCount: 1}})
	if err != nil {
		t.Fatalf("derive expected build: %v", err)
	}
	if rows := s.witnessCountRows(t, expected.BuildID); len(rows) != 0 {
		t.Fatalf("count rows committed despite a fully failed count write: %v", rows)
	}
}

func TestResourceInventoryMarkerPublishFailureKeepsPriorMarkerReadable(t *testing.T) {
	s := newCatalogInventoryService(t)
	faulted := &faultedInventory{InventoryRepository: s.realRepository(t)}
	adapter, err := New(faulted)
	if err != nil {
		t.Fatal(err)
	}
	s.literalFixture(t, "p", "g")

	first, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	if err != nil {
		t.Fatalf("first rebuild: %v", err)
	}

	s.seedResource(t, "p", "g", "Observation", "o1", "a")
	injected := errors.New("injected marker-publish failure")
	faulted.failOnPublish = 2
	faulted.injected = injected
	_, err = adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	var writeErr *ResourceInventoryWriteError
	if err == nil || !errors.As(err, &writeErr) || writeErr.Op != "marker" || !errors.Is(err, injected) {
		t.Fatalf("publish-failure error = %v, want marker write failure", err)
	}

	if markers := s.witnessMarkers(t, "p", "g"); len(markers) != 1 || markers[0].BuildID != first.BuildID {
		t.Fatalf("markers after publish failure = %+v, want only the first marker", markers)
	}
	result, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Observation", "Patient", "Practitioner"))
	if err != nil || fmt.Sprint(s.totals(result)) != fmt.Sprint(map[string]int64{"Practitioner": 1, "Observation": 0, "Patient": 7}) {
		t.Fatalf("read after publish failure = %v err=%v, want prior totals", s.totals(result), err)
	}

	faulted.failOnPublish = 0
	third, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	if err != nil {
		t.Fatalf("retry rebuild: %v", err)
	}
	markers := s.witnessMarkers(t, "p", "g")
	if len(markers) != 1 || markers[0].BuildID != third.BuildID {
		t.Fatalf("markers after retry = %+v, want only the third build", markers)
	}
	result, err = adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Observation", "Patient", "Practitioner"))
	if err != nil || fmt.Sprint(s.totals(result)) != fmt.Sprint(map[string]int64{"Practitioner": 1, "Observation": 1, "Patient": 7}) {
		t.Fatalf("read after retry = %v err=%v", s.totals(result), err)
	}
}

func TestResourceInventoryRepeatedBuildIsStableAndIsolatesOtherScopes(t *testing.T) {
	s := newCatalogInventoryService(t)
	faulted := &faultedInventory{InventoryRepository: s.realRepository(t)}
	adapter, err := New(faulted)
	if err != nil {
		t.Fatal(err)
	}
	s.literalFixture(t, "p", "g")

	types := []string{"Observation", "Patient", "Practitioner"}
	first, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", types...))
	if err != nil {
		t.Fatalf("first rebuild: %v", err)
	}
	second, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", types...))
	if err != nil {
		t.Fatalf("second rebuild: %v", err)
	}
	if first.BuildID != second.BuildID || first.CoverageDigest != second.CoverageDigest ||
		first.CountRowCount != second.CountRowCount || first.DocumentCount != second.DocumentCount {
		t.Fatalf("repeated build identity changed: %#v vs %#v", first, second)
	}
	if rows := s.witnessCountRows(t, first.BuildID); len(rows) != 4 {
		t.Fatalf("rows after repeat = %d, want 4", len(rows))
	}
	if markers := s.witnessMarkers(t, "p", "g"); len(markers) != 1 {
		t.Fatalf("markers after repeat = %+v, want one", markers)
	}

	s.seedResource(t, "p", "g2", "Patient", "q1", "z")
	s.seedResource(t, "p", "g2", "Patient", "q2", "z")
	s.seedResource(t, "p", "g2", "Patient", "q3", "y")
	other, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g2", "Patient"))
	if err != nil {
		t.Fatalf("g2 rebuild: %v", err)
	}
	if other.BuildID == first.BuildID {
		t.Fatal("different generation produced the same build id")
	}

	g, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, types...))
	if err != nil || fmt.Sprint(s.totals(g)) != fmt.Sprint(map[string]int64{"Practitioner": 1, "Observation": 0, "Patient": 7}) {
		t.Fatalf("g read after sibling rebuild = %v err=%v", s.totals(g), err)
	}
	gPathZ, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", false, []string{"z"}, "Patient"))
	if err != nil || fmt.Sprint(s.totals(gPathZ)) != fmt.Sprint(map[string]int64{"Patient": 0}) {
		t.Fatalf("g restricted [z] = %v err=%v, want Patient=0", s.totals(gPathZ), err)
	}
	g2PathA, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g2", false, []string{"a"}, "Patient"))
	if err != nil || fmt.Sprint(s.totals(g2PathA)) != fmt.Sprint(map[string]int64{"Patient": 0}) {
		t.Fatalf("g2 restricted [a] = %v err=%v, want Patient=0", s.totals(g2PathA), err)
	}
	g2, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g2", true, nil, "Patient"))
	if err != nil || fmt.Sprint(s.totals(g2)) != fmt.Sprint(map[string]int64{"Patient": 3}) {
		t.Fatalf("g2 read = %v err=%v", s.totals(g2), err)
	}
}

func TestResourceInventoryReadSumsOnlyAuthorizedPaths(t *testing.T) {
	s := newCatalogInventoryService(t)
	faulted := &faultedInventory{InventoryRepository: s.realRepository(t)}
	adapter, err := New(faulted)
	if err != nil {
		t.Fatal(err)
	}
	s.literalFixture(t, "p", "g")
	summary, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	s.exec(t, "INSERT INTO "+pgx.Identifier{s.schema, "fhir_resource_inventory_count"}.Sanitize()+
		" (count_key, build_id, project, dataset_generation, resource_type, auth_resource_path, document_count)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7)",
		"ric_forged_unmarked", "sha256:"+strings.Repeat("a", 64), "p", "g", "Patient", "a", int64(999999))

	restrictedA, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", false, []string{"a"}, "Observation", "Patient", "Practitioner"))
	if err != nil || restrictedA.Status != catalog.EvidenceAvailable {
		t.Fatalf("restricted [a] = %#v err=%v", restrictedA, err)
	}
	if fmt.Sprint(s.totals(restrictedA)) != fmt.Sprint(map[string]int64{"Practitioner": 0, "Observation": 0, "Patient": 2}) {
		t.Fatalf("restricted [a] totals = %v, want only path-a evidence (Patient 2, unscoped rows excluded)", s.totals(restrictedA))
	}
	restrictedB, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", false, []string{"b"}, "Patient"))
	if err != nil || fmt.Sprint(s.totals(restrictedB)) != fmt.Sprint(map[string]int64{"Patient": 5}) {
		t.Fatalf("restricted [b] totals = %v err=%v, want Patient 5", s.totals(restrictedB), err)
	}
	restrictedBoth, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", false, []string{"a", "b"}, "Patient"))
	if err != nil || fmt.Sprint(s.totals(restrictedBoth)) != fmt.Sprint(map[string]int64{"Patient": 7}) {
		t.Fatalf("restricted [a b] totals = %v err=%v, want Patient 7", s.totals(restrictedBoth), err)
	}
	unrestricted, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Observation", "Patient", "Practitioner"))
	if err != nil || fmt.Sprint(s.totals(unrestricted)) != fmt.Sprint(map[string]int64{"Practitioner": 1, "Observation": 0, "Patient": 7}) {
		t.Fatalf("unrestricted totals = %v err=%v", s.totals(unrestricted), err)
	}
	unrestrictedAgain, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Observation", "Patient", "Practitioner"))
	if err != nil || unrestrictedAgain.Digest != unrestricted.Digest || unrestricted.Digest == "" {
		t.Fatalf("digest unstable: %q vs %q err=%v", unrestricted.Digest, unrestrictedAgain.Digest, err)
	}

	retry, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient", "Practitioner"))
	if err != nil {
		t.Fatalf("rebuild over forged scope: %v", err)
	}
	if retry.BuildID != summary.BuildID {
		t.Fatalf("rebuild identity drifted: %s vs %s", retry.BuildID, summary.BuildID)
	}
	after, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Patient"))
	if err != nil || fmt.Sprint(s.totals(after)) != fmt.Sprint(map[string]int64{"Patient": 7}) {
		t.Fatalf("read after forged-scope rebuild = %v err=%v", s.totals(after), err)
	}
}

func TestResourceInventoryRestrictedEmptyScopeReturnsNoRows(t *testing.T) {
	s := newCatalogInventoryService(t)
	faulted := &faultedInventory{InventoryRepository: s.realRepository(t)}
	adapter, err := New(faulted)
	if err != nil {
		t.Fatal(err)
	}
	s.literalFixture(t, "p", "g")
	if _, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Patient")); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	faulted.calls = catalogInventoryCalls{}
	result, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", false, []string{}, "Patient"))
	if err != nil {
		t.Fatalf("restricted empty scope: %v", err)
	}
	if !result.Available || !result.Complete || result.Status != catalog.EvidenceEmpty || len(result.Values) != 0 {
		t.Fatalf("restricted empty result = %#v", result)
	}
	if faulted.calls.readSums != 0 || faulted.calls.readBuild != 1 {
		t.Fatalf("restricted empty scope queried counts: %+v", faulted.calls)
	}
}

func TestResourceInventoryStaleMarkerIsUnavailable(t *testing.T) {
	s := newCatalogInventoryService(t)
	faulted := &faultedInventory{InventoryRepository: s.realRepository(t)}
	adapter, err := New(faulted)
	if err != nil {
		t.Fatal(err)
	}

	faulted.calls = catalogInventoryCalls{}
	result, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Patient"))
	requireUnavailable(t, result, err, "marker is missing")
	if faulted.calls.readSums != 0 {
		t.Fatalf("missing marker queried counts: %+v", faulted.calls)
	}

	s.literalFixture(t, "p", "g")
	summary, err := adapter.RebuildResourceInventory(s.ctx, inventoryRebuildOptions("p", "g", "Observation", "Patient"))
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	s.exec(t, "UPDATE "+pgx.Identifier{s.schema, "fhir_resource_inventory_build"}.Sanitize()+
		" SET complete = false WHERE project = $1 AND dataset_generation = $2", "p", "g")
	faulted.calls = catalogInventoryCalls{}
	result, err = adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Patient"))
	requireUnavailable(t, result, err, "stale or incomplete marker")
	if faulted.calls.readSums != 0 {
		t.Fatalf("incomplete marker queried counts: %+v", faulted.calls)
	}
	if rows := s.witnessCountRows(t, summary.BuildID); len(rows) != 3 {
		t.Fatalf("count rows vanished under an incomplete marker: %v", rows)
	}

	s.exec(t, "UPDATE "+pgx.Identifier{s.schema, "fhir_resource_inventory_build"}.Sanitize()+
		" SET complete = true, coverage_digest = $3 WHERE project = $1 AND dataset_generation = $2",
		"p", "g", "sha256:"+strings.Repeat("0", 64))
	result, err = adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Patient"))
	requireUnavailable(t, result, err, "stale or incomplete marker")

	s.exec(t, "UPDATE "+pgx.Identifier{s.schema, "fhir_resource_inventory_build"}.Sanitize()+
		" SET complete = true, coverage_digest = $3 WHERE project = $1 AND dataset_generation = $2",
		"p", "g", summary.CoverageDigest)
	result, err = adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Practitioner"))
	requireUnavailable(t, result, err, "does not cover Practitioner")
	ok, err := adapter.DiscoverResourceInventory(s.ctx, inventoryReadOptions("p", "g", true, nil, "Patient"))
	if err != nil || ok.Status != catalog.EvidenceAvailable {
		t.Fatalf("covered-type read after restore = %#v err=%v", ok, err)
	}
}
