package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgcat06FixturePrefix = "verify_cat06_"

var pgcat06NamePattern = regexp.MustCompile(`^verify_cat06_[0-9]+_[0-9a-f]+$`)

var (
	pgcat06OwnedMu     sync.Mutex
	pgcat06OwnedByName = map[string]string{}
)

func pgcat06Register(db, schema string) {
	pgcat06OwnedMu.Lock()
	defer pgcat06OwnedMu.Unlock()
	pgcat06OwnedByName[db] = schema
}

type pgcat06Service struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func pgcat06TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func pgcat06RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func newPgcat06Service(t *testing.T) *pgcat06Service {
	t.Helper()
	raw := pgcat06TestPostgresURL(t)

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

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	t.Cleanup(cancel)

	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	var dbName, schema string

	dbName = fmt.Sprintf("verify_cat06_%d_%s", time.Now().UnixNano(), pgcat06RandomHex(t))
	if !pgcat06NamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	pgcat06Register(dbName, "")
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

	schema = fmt.Sprintf("verify_cat06_%d_%s", time.Now().UnixNano(), pgcat06RandomHex(t))
	if !pgcat06NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	pgcat06Register(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	service := &pgcat06Service{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
	if _, err := storepostgres.MigrateSchema(ctx, service.openClient(t), schema); err != nil {
		fail("MigrateSchema", err)
	}
	return service
}

func (s *pgcat06Service) openClient(t *testing.T) *storepostgres.Client {
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

func (s *pgcat06Service) openDedicatedClient(t *testing.T, label string) (*storepostgres.Client, int32) {
	t.Helper()
	client, err := storepostgres.Open(s.ctx, storepostgres.Config{
		DSN:            s.dsn,
		MaxConns:       1,
		MinConns:       1,
		AcquireTimeout: 30 * time.Second,
	})
	if err != nil {
		s.fail("open dedicated client "+label, err)
	}
	t.Cleanup(client.Close)
	pid := s.witnessClientPid(t, client, label)
	return client, pid
}

func (s *pgcat06Service) witnessClientPid(t *testing.T, client *storepostgres.Client, label string) int32 {
	t.Helper()
	conn, err := client.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire connection "+label, err)
	}
	defer conn.Release()
	var pid int32
	if err := conn.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		s.fail("witness pg_backend_pid "+label, err)
	}
	if pid <= 0 {
		t.Fatalf("pg_backend_pid() for %s returned %d, want a live backend", label, pid)
	}
	return pid
}

func (s *pgcat06Service) readRepository(t *testing.T) *SemanticReadRepository {
	t.Helper()
	repo, err := NewSemanticReadRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new semantic read repository", err)
	}
	return repo
}

func (s *pgcat06Service) contributionRepository(t *testing.T) *SemanticContributionRepository {
	t.Helper()
	repo, err := NewSemanticContributionRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new semantic contribution repository", err)
	}
	return repo
}

func (s *pgcat06Service) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *pgcat06Service) contributionFQN() string {
	return pgx.Identifier{s.schema, semanticInventoryContributionTableName}.Sanitize()
}

func (s *pgcat06Service) buildFQN() string {
	return pgx.Identifier{s.schema, semanticInventoryBuildTableName}.Sanitize()
}

func (s *pgcat06Service) ledgerFQN() string {
	return pgx.Identifier{s.schema, storepostgres.LedgerTable}.Sanitize()
}

func pgcat06Hex64(label string) string {
	sum := sha256.Sum256([]byte("pgcat06/" + label))
	return hex.EncodeToString(sum[:])
}

type buildSeed struct {
	Project            string
	Generation         string
	State              catalog.SemanticInventoryState
	ObservationSchema  int
	InventorySchema    int
	EntryIndexVersion  int
	RuleVersion        int
	SourceKind         string
	SourceAvailability string
	AuthResourcePath   string
	ScannedResources   int64
	LeaseToken         string
	LeaseExpiresAt     int64
	Diagnostics        []string
}

func pgcat06DefaultBuildSeed(project, generation string) buildSeed {
	return buildSeed{
		Project: project, Generation: generation,
		State:              catalog.SemanticInventoryComplete,
		ObservationSchema:  catalog.SemanticObservationSchemaVersion,
		InventorySchema:    catalog.SemanticInventorySchemaVersion,
		EntryIndexVersion:  0,
		RuleVersion:        catalog.SemanticObservationRuleVersion,
		SourceKind:         catalog.SemanticInventorySourceFile,
		SourceAvailability: catalog.SemanticInventorySourceAvailabilityVerified,
		ScannedResources:   7,
	}
}

func (s *pgcat06Service) seedBuild(t *testing.T, seed buildSeed) catalog.SemanticInventoryBuild {
	t.Helper()
	build := catalog.NewSemanticInventoryBuild(seed.Project, seed.Generation, seed.AuthResourcePath)
	build.State = seed.State
	build.ObservationSchema = seed.ObservationSchema
	build.InventorySchema = seed.InventorySchema
	build.EntryIndexVersion = seed.EntryIndexVersion
	build.RuleVersion = seed.RuleVersion
	build.SourceKind = seed.SourceKind
	build.SourceAvailability = seed.SourceAvailability
	build.ScannedResources = seed.ScannedResources
	build.LeaseToken = seed.LeaseToken
	build.LeaseExpiresAt = seed.LeaseExpiresAt
	diagnostics := seed.Diagnostics
	if diagnostics == nil {
		diagnostics = []string{}
	}
	s.exec(t, "INSERT INTO "+s.buildFQN()+
		" (build_key, project, dataset_generation, build_id, state, observation_schema,"+
		" inventory_schema, entry_index_version, rule_version, source_kind, source_availability,"+
		" auth_resource_path, scanned_resources, checkpoint, checkpoint_collection, checkpoint_key,"+
		" lease_token, lease_expires_at, diagnostics)"+
		" VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'','','',$14,$15,$16::text[])",
		build.Key, seed.Project, catalog.NormalizeDatasetGeneration(seed.Generation), build.BuildID,
		string(seed.State), seed.ObservationSchema, seed.InventorySchema, seed.EntryIndexVersion,
		seed.RuleVersion, seed.SourceKind, seed.SourceAvailability, seed.AuthResourcePath,
		seed.ScannedResources, seed.LeaseToken, seed.LeaseExpiresAt, diagnostics)
	return build
}

type contributionSeed struct {
	Key              string
	Project          string
	Generation       string
	BuildID          string
	SourceKind       string
	AuthResourcePath string
	ResourceType     string
	SourceID         string
	Ordinal          int
	ConceptID        string
	BindingID        string
	Example          string
	Observation      string
}

func (s *pgcat06Service) seedContribution(t *testing.T, seed contributionSeed) {
	t.Helper()
	if seed.Key == "" {
		seed.Key = pgcat06Hex64(seed.Project + "/" + seed.Generation + "/" + seed.BuildID + "/" +
			seed.SourceKind + "/" + seed.SourceID + "/" + fmt.Sprint(seed.Ordinal) + "/" + seed.ConceptID)
	}
	if strings.TrimSpace(seed.Observation) == "" || seed.Observation[0] != '{' {
		t.Fatalf("contribution seed for %s requires an observation object", seed.ConceptID)
	}
	s.exec(t, "INSERT INTO "+s.contributionFQN()+
		" (contribution_key, project, dataset_generation, build_id, source_kind, auth_resource_path,"+
		" resource_type, source_id, ordinal, concept_id, binding_id, example, payload_digest, observation)"+
		" VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb)",
		seed.Key, seed.Project, catalog.NormalizeDatasetGeneration(seed.Generation), seed.BuildID,
		seed.SourceKind, seed.AuthResourcePath, seed.ResourceType, seed.SourceID, seed.Ordinal,
		seed.ConceptID, seed.BindingID, seed.Example, pgcat06Hex64("digest/"+seed.Key), seed.Observation)
}

func pageWitness(t *testing.T, page catalog.SemanticInventoryPage) string {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("canonicalize page witness: %v", err)
	}
	return string(raw)
}

func resolveWitness(t *testing.T, result catalog.SemanticInventoryResolveResult) string {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("canonicalize resolve witness: %v", err)
	}
	return string(raw)
}

type pgcat06CursorPayload struct {
	Version       int    `json:"v"`
	RequestDigest string `json:"r"`
	BindingID     string `json:"b"`
	ConceptID     string `json:"c"`
}

func pgcat06DecodeCursor(t *testing.T, cursor string) pgcat06CursorPayload {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("decode cursor base64: %v", err)
	}
	var payload pgcat06CursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode cursor payload: %v", err)
	}
	return payload
}

func pgcat06ObsJSON(t *testing.T, observation catalog.SemanticObservation) string {
	t.Helper()
	raw, err := json.Marshal(observation)
	if err != nil {
		t.Fatalf("encode fixture observation: %v", err)
	}
	return string(raw)
}

func pgcat06Observation(population int64) catalog.SemanticObservation {
	return catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source: catalog.SemanticObservationSource{
			Canonical: "Observation.code.coding[]",
			Type:      "Observation",
			Profile:   "http://profiles.test/Observation",
			Path:      "code.coding[]",
		},
		Key: catalog.SemanticObservationKey{
			Selector: "code.coding[]",
			System:   "urn:sys-a",
		},
		Value: catalog.SemanticObservationValue{
			Selector: "value.value",
			Type:     "string",
		},
		Completeness: catalog.SemanticComplete,
		Status:       "SUPPORTED",
		Population:   population,
	}
}

var errPgcat06DeadSource = errors.New("pgcat06 offline test: database must not be touched")

type pgcat06DeadSource struct{ hits *int }

func (d *pgcat06DeadSource) Acquire(context.Context) (*pgxpool.Conn, error) {
	if d.hits != nil {
		*d.hits++
	}
	return nil, errPgcat06DeadSource
}

func pgcat06OfflineRepo(t *testing.T) (*SemanticReadRepository, *int) {
	t.Helper()
	hits := 0
	repo, err := NewSemanticReadRepository(&pgcat06DeadSource{hits: &hits}, "verify_cat06_offline")
	if err != nil {
		t.Fatalf("new offline semantic read repository: %v", err)
	}
	return repo, &hits
}

func TestSemanticInventoryPageRequestRefusalsBeforeAnyStatement(t *testing.T) {
	repo, hits := pgcat06OfflineRepo(t)
	base := func() catalog.SemanticInventoryPageOptions {
		return catalog.SemanticInventoryPageOptions{
			Project: "cat06-proj", DatasetGeneration: "gen-1",
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
		}
	}
	cases := []struct {
		name   string
		mutate func(*catalog.SemanticInventoryPageOptions)
	}{
		{"blank project", func(o *catalog.SemanticInventoryPageOptions) { o.Project = "   " }},
		{"empty project", func(o *catalog.SemanticInventoryPageOptions) { o.Project = "" }},
		{"blank generation", func(o *catalog.SemanticInventoryPageOptions) { o.DatasetGeneration = " \t " }},
		{"empty generation", func(o *catalog.SemanticInventoryPageOptions) { o.DatasetGeneration = "" }},
		{"unknown selection mode", func(o *catalog.SemanticInventoryPageOptions) { o.SelectionMode = "SOMETHING_ELSE" }},
	}
	for _, tc := range cases {
		opts := base()
		tc.mutate(&opts)
		_, err := repo.PageSemanticInventory(context.Background(), opts)
		if !errors.Is(err, ErrInvalidSemanticInventoryPage) {
			t.Errorf("%s: PageSemanticInventory error = %v, want ErrInvalidSemanticInventoryPage", tc.name, err)
		}
	}
	for _, mode := range []catalog.SemanticInventorySelectionMode{"", catalog.SemanticInventoryExactConcept, catalog.SemanticInventorySystemVersionCategory} {
		opts := base()
		opts.SelectionMode = mode
		if _, err := repo.PageSemanticInventory(context.Background(), opts); !errors.Is(err, errPgcat06DeadSource) {
			t.Errorf("mode %q: error = %v, want the dead-source sentinel (validation accepted, storage reached)", mode, err)
		}
	}
	if *hits == 0 {
		t.Fatalf("accepted shapes never reached the connection source")
	}
}

func TestSemanticInventoryPageConstructorRefusals(t *testing.T) {
	if _, err := NewSemanticReadRepository(nil, "verify_cat06_ok"); err == nil {
		t.Fatal("nil connection source accepted")
	}
	if _, err := NewSemanticReadRepository(&pgcat06DeadSource{}, "Bad-Schema"); !errors.Is(err, storepostgres.ErrInvalidSchemaName) {
		t.Fatalf("invalid schema error = %v, want ErrInvalidSchemaName", err)
	}
	if _, err := NewSemanticReadRepository(&pgcat06DeadSource{}, strings.Repeat("s", 64)); !errors.Is(err, storepostgres.ErrInvalidSchemaName) {
		t.Fatalf("over-long schema error = %v, want ErrInvalidSchemaName", err)
	}
}

func TestSemanticInventoryResolveRequestRefusalsBeforeAnyStatement(t *testing.T) {
	repo, hits := pgcat06OfflineRepo(t)
	family := catalog.SemanticInventoryFamilyReference{
		BindingID: "bnd-a", SourceKind: catalog.SemanticInventorySourceFile, ResourceType: "Observation",
		SourcePath: "code.coding[]", System: "urn:sys-a", Version: "v1",
	}
	family.FamilyID = catalog.SemanticInventoryFamilyID(family)
	base := func() catalog.SemanticInventoryResolveOptions {
		return catalog.SemanticInventoryResolveOptions{
			Project: "cat06-proj", DatasetGeneration: "gen-1",
			AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(false),
			AuthResourcePaths:             []string{"fhir/Observation?type=x"},
			References:                    []catalog.SemanticInventoryReference{{ConceptID: "cpt-a", BindingID: "bnd-a"}},
		}
	}
	cases := []struct {
		name   string
		mutate func(*catalog.SemanticInventoryResolveOptions)
	}{
		{"nil authorization decision", func(o *catalog.SemanticInventoryResolveOptions) { o.AuthResourcePathsUnrestricted = nil }},
		{"blank project", func(o *catalog.SemanticInventoryResolveOptions) { o.Project = " " }},
		{"blank generation", func(o *catalog.SemanticInventoryResolveOptions) { o.DatasetGeneration = "  " }},
		{"no references", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = nil
			o.FamilyReferences = nil
		}},
		{"too many references", func(o *catalog.SemanticInventoryResolveOptions) {
			refs := make([]catalog.SemanticInventoryReference, 0, maxSemanticInventoryResolveSelections+1)
			for i := 0; i <= maxSemanticInventoryResolveSelections; i++ {
				refs = append(refs, catalog.SemanticInventoryReference{
					ConceptID: fmt.Sprintf("cpt-%03d", i), BindingID: "bnd-a"})
			}
			o.References = refs
		}},
		{"blank concept", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = []catalog.SemanticInventoryReference{{ConceptID: " ", BindingID: "bnd-a"}}
		}},
		{"blank binding", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = []catalog.SemanticInventoryReference{{ConceptID: "cpt-a", BindingID: " "}}
		}},
		{"duplicate reference", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = []catalog.SemanticInventoryReference{
				{ConceptID: "cpt-a", BindingID: "bnd-a"},
				{ConceptID: "cpt-a", BindingID: "bnd-a"},
			}
		}},
		{"family id does not re-derive", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = nil
			bad := family
			bad.FamilyID = strings.Repeat("0", 64)
			o.FamilyReferences = []catalog.SemanticInventoryFamilyReference{bad}
		}},
		{"family blank system", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = nil
			bad := family
			bad.System = " "
			bad.FamilyID = catalog.SemanticInventoryFamilyID(bad)
			o.FamilyReferences = []catalog.SemanticInventoryFamilyReference{bad}
		}},
		{"family blank resource type", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = nil
			bad := family
			bad.ResourceType = ""
			bad.FamilyID = catalog.SemanticInventoryFamilyID(bad)
			o.FamilyReferences = []catalog.SemanticInventoryFamilyReference{bad}
		}},
		{"family blank source path", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = nil
			bad := family
			bad.SourcePath = "  "
			bad.FamilyID = catalog.SemanticInventoryFamilyID(bad)
			o.FamilyReferences = []catalog.SemanticInventoryFamilyReference{bad}
		}},
		{"family unknown source kind", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = nil
			bad := family
			bad.SourceKind = "stream_of_consciousness"
			bad.FamilyID = catalog.SemanticInventoryFamilyID(bad)
			o.FamilyReferences = []catalog.SemanticInventoryFamilyReference{bad}
		}},
		{"duplicate family reference", func(o *catalog.SemanticInventoryResolveOptions) {
			o.References = nil
			o.FamilyReferences = []catalog.SemanticInventoryFamilyReference{family, family}
		}},
	}
	for _, tc := range cases {
		opts := base()
		tc.mutate(&opts)
		_, err := repo.ResolveSemanticInventorySelections(context.Background(), opts)
		if !errors.Is(err, ErrInvalidSemanticInventorySelection) {
			t.Errorf("%s: ResolveSemanticInventorySelections error = %v, want ErrInvalidSemanticInventorySelection", tc.name, err)
		}
	}
	if _, err := repo.ResolveSemanticInventorySelections(context.Background(), base()); !errors.Is(err, errPgcat06DeadSource) {
		t.Fatalf("accepted resolve error = %v, want the dead-source sentinel", err)
	}
	if *hits == 0 {
		t.Fatalf("accepted shapes never reached the connection source")
	}
}

func TestSemanticInventoryCursorMismatchUnderScopeChanges(t *testing.T) {
	const buildID = "0000000000000000000000000000000000000000000000000000000000000001"
	unrestricted := true
	base := catalog.SemanticInventoryPageOptions{
		Project: "cat06-proj", DatasetGeneration: "gen-1",
		AuthResourcePathsUnrestricted: &unrestricted,
		ResourceType:                  "Observation",
		BindingID:                     "bnd-a",
		Query:                         "abc",
		Limit:                         10,
	}
	cursor := catalog.EncodeSemanticInventoryCursor(base, buildID, "bnd-a", "cpt-z")
	if binding, concept, err := catalog.DecodeSemanticInventoryCursor(cursor, base, buildID); err != nil || binding != "bnd-a" || concept != "cpt-z" {
		t.Fatalf("unchanged cursor round-trip = (%q, %q, %v)", binding, concept, err)
	}
	changes := []struct {
		name   string
		mutate func(*catalog.SemanticInventoryPageOptions)
	}{
		{"project", func(o *catalog.SemanticInventoryPageOptions) { o.Project = "cat06-other" }},
		{"generation", func(o *catalog.SemanticInventoryPageOptions) { o.DatasetGeneration = "gen-2" }},
		{"resource type", func(o *catalog.SemanticInventoryPageOptions) { o.ResourceType = "Encounter" }},
		{"binding filter", func(o *catalog.SemanticInventoryPageOptions) { o.BindingID = "bnd-b" }},
		{"query", func(o *catalog.SemanticInventoryPageOptions) { o.Query = "abd" }},
		{"limit", func(o *catalog.SemanticInventoryPageOptions) { o.Limit = 11 }},
		{"auth paths added", func(o *catalog.SemanticInventoryPageOptions) {
			o.AuthResourcePaths = []string{"fhir/Observation?type=x"}
		}},
		{"authorization decision", func(o *catalog.SemanticInventoryPageOptions) {
			value := false
			o.AuthResourcePathsUnrestricted = &value
		}},
		{"selection mode", func(o *catalog.SemanticInventoryPageOptions) {
			o.SelectionMode = catalog.SemanticInventorySystemVersionCategory
		}},
	}
	for _, tc := range changes {
		opts := base
		tc.mutate(&opts)
		if _, _, err := catalog.DecodeSemanticInventoryCursor(cursor, opts, buildID); !errors.Is(err, catalog.ErrSemanticInventoryCursorMismatch) {
			t.Errorf("%s: changed-scope cursor error = %v, want ErrSemanticInventoryCursorMismatch", tc.name, err)
		}
	}
	padded := base
	padded.DatasetGeneration = "  gen-1\t"
	if _, _, err := catalog.DecodeSemanticInventoryCursor(cursor, padded, buildID); err != nil {
		t.Fatalf("padded generation must normalize onto the same cursor: %v", err)
	}
	for _, bad := range []string{"!!!not base64!!!", base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"r":"x","b":"y","c":"z"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"r":"x","b":"","c":"z"}`)), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"r":"x","b":"y","c":""}`))} {
		if _, _, err := catalog.DecodeSemanticInventoryCursor(bad, base, buildID); !errors.Is(err, catalog.ErrSemanticInventoryCursorMismatch) {
			t.Errorf("tampered cursor %q error = %v, want typed mismatch", bad, err)
		}
	}
}

func TestSemanticInventoryPageSQLCarriesTheParityContract(t *testing.T) {
	scope := semanticPageParams{
		project: "cat06-proj", generation: "gen-1",
		buildID:      "0000000000000000000000000000000000000000000000000000000000000001",
		sourceKind:   catalog.SemanticInventorySourceFile,
		unrestricted: false, paths: []string{"fhir/Observation?type=x"},
		resourceType: "Observation", bindingID: "bnd-a", query: "abc",
		afterBindingID: "bnd-a", afterSelection: "cpt-a", limit: 50,
	}
	exactSQL, exactArgs := semanticPageQuery(`"s"."fhir_semantic_inventory_contribution"`, scope)
	familyScope := scope
	familyScope.familyMode = true
	familySQL, familyArgs := semanticPageQuery(`"s"."fhir_semantic_inventory_contribution"`, familyScope)

	for name, pair := range map[string]struct {
		sql  string
		args []any
	}{
		"exact":  {exactSQL, exactArgs},
		"family": {familySQL, familyArgs},
	} {
		sql, args := pair.sql, pair.args
		if len(args) != 12 {
			t.Fatalf("%s: statement binds %d parameters, want 12 (scope $1..$6, filters $7..$9, cursor $10..$11, budget $12)", name, len(args))
		}
		if args[11] != 51 {
			t.Errorf("%s: budget parameter = %v, want limit+1 = 51", name, args[11])
		}
		wants := []string{
			`c.project = $1 AND c.dataset_generation = $2 AND c.build_id = $3 AND c.source_kind = $4`,
			`($5 OR c.auth_resource_path = ANY($6::text[]))`,
			`($7 = '' OR c.resource_type = $7)`,
			`candidates.binding_id COLLATE "C" > $10 COLLATE "C"`,
			`candidates.selection_id COLLATE "C" > $11 COLLATE "C"`,
			`ORDER BY i.binding_id COLLATE "C", i.selection_id COLLATE "C", i.is_category`,
			`LIMIT $12`,
			`strpos(lower(concat_ws(' ', c.resource_type,`,
			`COLLATE "C"), lower($9 COLLATE "C")) > 0`,
			`min(NULLIF(c.example, '') COLLATE "C") AS agg_example`,
			`~ '^-?[0-9]+$'`,
		}
		for _, want := range wants {
			if !strings.Contains(sql, want) {
				t.Errorf("%s statement missing load-bearing fragment %q", name, want)
			}
		}
		if got := strings.Count(sql, `($7 = '' OR c.resource_type = $7)`); got != 2 {
			t.Errorf("%s statement applies the resource-type filter %d times, want 2 (identity + aggregation stages)", name, got)
		}
		if !strings.Contains(sql, `concat_ws(' ', c.resource_type, (c.observation #>> '{source,path}')`) {
			t.Errorf("%s statement must feed raw leaves into concat_ws: %s", name, sql)
		}
		if strings.Contains(sql, `concat_ws(' ', c.resource_type, COALESCE(`) {
			t.Errorf("%s statement keeps empty search slots where the reference skips them", name)
		}
	}
	if strings.Contains(exactSQL, "CODED_CONCEPT_V1") || strings.Contains(exactSQL, "family:[") {
		t.Errorf("exact page must not compute family identities at all: %s", exactSQL)
	}
	if !strings.Contains(exactSQL, `false AS is_category, c.concept_id AS selection_id`) {
		t.Errorf("exact page must derive the shared ordering key from concept_id: %s", exactSQL)
	}
	for _, want := range []string{
		`'family:['`,
		`'CODED_CONCEPT_V1'`,
	} {
		if !strings.Contains(familySQL, want) {
			t.Errorf("family statement missing %q", want)
		}
	}
	if strings.Contains(familySQL, "jsonb_build_array") {
		t.Errorf("family selection must not use jsonb_build_array (its text form pads commas with spaces)")
	}
}

func TestSemanticInventoryResolveSQLCarriesTheParityContract(t *testing.T) {
	scope := resolveScope{
		project: "cat06-proj", generation: "gen-1",
		buildID:    "0000000000000000000000000000000000000000000000000000000000000001",
		sourceKind: catalog.SemanticInventorySourceRetained, unrestricted: false,
		paths: []string{"fhir/Observation?type=x"}, observationSchema: catalog.SemanticObservationSchemaVersion,
	}
	exactSQL, exactArgs := semanticResolveReferencesQuery(`"s"."t"`, scope, []catalog.SemanticInventoryReference{
		{ConceptID: "cpt-a", BindingID: "bnd-a"}, {ConceptID: "cpt-b", BindingID: "bnd-b"},
	})
	if len(exactArgs) != 6+4 {
		t.Fatalf("exact resolve bound %d args, want 10", len(exactArgs))
	}
	if exactArgs[6] != "bnd-a" || exactArgs[7] != "cpt-a" || exactArgs[8] != "bnd-b" || exactArgs[9] != "cpt-b" {
		t.Fatalf("exact resolve args out of request order: %v", exactArgs)
	}
	for _, want := range []string{
		`(VALUES ($7::text, $8::text, 0), ($9::text, $10::text, 1)) AS requested(binding_id, concept_id, ord)`,
		`ORDER BY c.contribution_key COLLATE "C" LIMIT 1) c ORDER BY requested.ord`,
		`c.project = $1`,
	} {
		if !strings.Contains(exactSQL, want) {
			t.Errorf("exact resolve missing %q", want)
		}
	}

	family := catalog.SemanticInventoryFamilyReference{
		BindingID: "bnd-a", SourceKind: catalog.SemanticInventorySourceRetained, ResourceType: "Observation",
		SourcePath: "code.coding[]", System: "urn:sys-a", Version: "v1",
	}
	family.FamilyID = catalog.SemanticInventoryFamilyID(family)
	familySQL, familyArgs := semanticResolveFamiliesQuery(`"s"."t"`, scope, []catalog.SemanticInventoryFamilyReference{family})
	if len(familyArgs) != 6+6 {
		t.Fatalf("family resolve bound %d args, want 12", len(familyArgs))
	}
	for _, want := range []string{
		`LEFT JOIN "s"."t" c ON c.project = $1`,
		`CASE WHEN (c.observation #> '{key,version}') IS NULL THEN '' WHEN (c.observation #> '{key,version}') = 'null'::jsonb THEN NULL ELSE (c.observation #>> '{key,version}') END) = requested.version`,
		`c.observation #>> '{rule_hint}' = 'CODED_CONCEPT_V1'`,
		`GROUP BY requested.ord HAVING COUNT(c.contribution_key) > 0`,
		`ORDER BY requested.ord`,
		`WHERE requested.source_kind = $4`,
	} {
		if !strings.Contains(familySQL, want) {
			t.Errorf("family resolve missing %q", want)
		}
	}
}

const (
	pgcat06Project     = "cat06-proj"
	pgcat06Generation  = "gen-1"
	pgcat06PathGlucose = "fhir/Observation?type=glucose"
	pgcat06PathVisit   = "fhir/Encounter?type=visit"
	pgcat06PathSecret  = "fhir/Secret?type=x"
	pgcat06PathNone    = "fhir/Nothing?type=none"
)

func pgcat06Unrestricted(v bool) *bool { return catalog.ExplicitAuthResourcePathsUnrestricted(v) }

func TestSemanticInventoryEmitterWriterReaderGoldenAnchor(t *testing.T) {
	s := newPgcat06Service(t)
	anchorSeed := pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation)
	anchorSeed.SourceKind = catalog.SemanticInventorySourceRetained
	s.seedBuild(t, anchorSeed)
	contributions := pgcat05EmitContribution(t, pgcat06Project, pgcat06Generation,
		pgcat06PathGlucose, "Observation", "retained:Observation/cat06-anchor",
		`{"resourceType":"Observation","code":{"coding":[{"system":"urn:obs-05","version":"v1","code":"loinc-42"}]}}`)
	if len(contributions) != 1 {
		t.Fatalf("emitter produced %d contributions, want exactly 1", len(contributions))
	}
	if contributions[0].ConceptID != "ec572479ea77eabd0c8014f33810f458e9f009a27adb8fd77fb9fab3121ad62f" ||
		contributions[0].BindingID != "b498c06c58e286f4cc9ceb74942c8a7872ad49b0220e65f0ea754fd936f4a53d" {
		t.Fatalf("emitter concept/binding drifted from the pinned anchor: %s/%s",
			contributions[0].ConceptID, contributions[0].BindingID)
	}
	if err := s.contributionRepository(t).WriteSemanticInventoryContributions(s.ctx, contributions, 0); err != nil {
		s.fail("write emitter contribution", err)
	}

	repo := s.readRepository(t)
	opts := catalog.SemanticInventoryPageOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
	}
	page, err := repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("page semantic inventory", err)
	}
	if len(page.Entries) != 1 || page.NextCursor != "" {
		t.Fatalf("anchor page = %+v, want exactly one entry and no cursor", page)
	}
	want := catalog.SemanticInventoryEntry{
		ConceptID: "ec572479ea77eabd0c8014f33810f458e9f009a27adb8fd77fb9fab3121ad62f",
		BindingID: "b498c06c58e286f4cc9ceb74942c8a7872ad49b0220e65f0ea754fd936f4a53d",
		Observation: catalog.SemanticObservation{
			SchemaVersion: catalog.SemanticObservationSchemaVersion,
			Source: catalog.SemanticObservationSource{
				Canonical: "Observation.code.coding[]",
				Type:      "Observation",
				Path:      "code.coding[]",
			},
			Key: catalog.SemanticObservationKey{
				Selector: "code.coding[]", System: "urn:obs-05", Version: "v1", Code: "loinc-42",
			},
			Value: catalog.SemanticObservationValue{
				Selector: "code.coding[].display", Type: "string",
				FallbackSelectors:     []string{"code.coding[].code"},
				FallbackOnBlankString: true,
			},
			LogicalType:       "string",
			ObservedUnits:     []string{},
			Completeness:      catalog.SemanticComplete,
			Status:            "SUPPORTED",
			Population:        1,
			Examples:          []string{"loinc-42"},
			ExamplesTruncated: false,
			RuleHint:          "CODED_CONCEPT_V1",
			RuleVersion:       "5",
		},
	}
	if !reflect.DeepEqual(page.Entries[0], want) {
		t.Fatalf("anchor page entry =\n%#v\nwant\n%#v", page.Entries[0], want)
	}
	perturbed := want
	perturbed.Observation.Population = 2
	if reflect.DeepEqual(page.Entries[0], perturbed) {
		t.Fatal("the golden comparison accepted a perturbed population")
	}
	perturbed2 := want
	perturbed2.Observation.Examples = []string{"other"}
	if reflect.DeepEqual(page.Entries[0], perturbed2) {
		t.Fatal("the golden comparison accepted perturbed examples")
	}
}

func TestSemanticInventoryPageExactGolden(t *testing.T) {
	s := newPgcat06Service(t)
	build := s.seedBuild(t, func() buildSeed {
		seed := pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation)
		seed.LeaseToken = "lease-live"
		seed.LeaseExpiresAt = 1_700_000_000_000
		seed.AuthResourcePath = "fhir/secret/internal"
		seed.Diagnostics = []string{"stale-diagnostic"}
		return seed
	}())
	_ = build
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	put := func(sourceID string, ordinal int, binding, concept, path, example string, observation catalog.SemanticObservation) {
		s.seedContribution(t, contributionSeed{
			Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
			SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: path,
			ResourceType: "Observation", SourceID: sourceID, Ordinal: ordinal,
			ConceptID: concept, BindingID: binding, Example: example,
			Observation: pgcat06ObsJSON(t, observation),
		})
	}

	alpha := func(pop int64) catalog.SemanticObservation {
		o := pgcat06Observation(pop)
		o.Key = catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:sys-a", Version: "v1", Code: "code-a", Display: "Alpha Display"}
		return o
	}
	beta := func() catalog.SemanticObservation {
		o := pgcat06Observation(1)
		o.Key = catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:sys-b", Version: "v1", Code: "code-b", Display: "Beta"}
		return o
	}
	gammaUnits := func(units []string) catalog.SemanticObservation {
		o := pgcat06Observation(1)
		o.Key = catalog.SemanticObservationKey{Selector: "code.coding[]", System: "urn:sys-c", Code: "code-c"}
		o.ObservedUnits = units
		return o
	}

	put("src-1", 0, "bnd-alpha", "cpt-alpha", pgcat06PathGlucose, "ex-alpha-1", alpha(3))
	put("src-2", 0, "bnd-alpha", "cpt-alpha", pgcat06PathGlucose, "", alpha(2))
	put("src-3", 0, "bnd-alpha", "cpt-beta", pgcat06PathVisit, "beta-ex", beta())
	put("src-4", 0, "bnd-beta", "cpt-gamma", pgcat06PathGlucose, "g1", gammaUnits([]string{"kg"}))
	put("src-4", 1, "bnd-beta", "cpt-gamma", pgcat06PathGlucose, "g2", gammaUnits([]string{"g"}))

	foreign := contributionSeed{
		Project: "cat06-other", Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "src-f1", Ordinal: 0,
		ConceptID: "cpt-alpha", BindingID: "bnd-alpha", Example: "foreign",
		Observation: pgcat06ObsJSON(t, alpha(99)),
	}
	s.seedContribution(t, foreign)
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: "gen-2", BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "src-f2", Ordinal: 0,
		ConceptID: "cpt-alpha", BindingID: "bnd-alpha", Example: "foreign",
		Observation: pgcat06ObsJSON(t, alpha(99)),
	})
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: pgcat06Hex64("foreign-build"),
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "src-f3", Ordinal: 0,
		ConceptID: "cpt-alpha", BindingID: "bnd-alpha", Example: "foreign",
		Observation: pgcat06ObsJSON(t, alpha(99)),
	})
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceRetained, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "src-f4", Ordinal: 0,
		ConceptID: "cpt-alpha", BindingID: "bnd-alpha", Example: "foreign",
		Observation: pgcat06ObsJSON(t, alpha(99)),
	})
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathSecret,
		ResourceType: "Observation", SourceID: "src-f5", Ordinal: 0,
		ConceptID: "cpt-alpha", BindingID: "bnd-alpha", Example: "secret-ex",
		Observation: pgcat06ObsJSON(t, alpha(4)),
	})

	repo := s.readRepository(t)
	baseOpts := func() catalog.SemanticInventoryPageOptions {
		return catalog.SemanticInventoryPageOptions{
			Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		}
	}

	opts := baseOpts()
	opts.AuthResourcePathsUnrestricted = pgcat06Unrestricted(true)
	page, err := repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("unrestricted page", err)
	}
	if len(page.Entries) != 3 {
		t.Fatalf("unrestricted page returned %d entries: %+v", len(page.Entries), page.Entries)
	}
	wantPopulation := int64(3 + 2 + 4)
	got := page.Entries[0]
	if got.ConceptID != "cpt-alpha" || got.BindingID != "bnd-alpha" {
		t.Fatalf("first identity = %s/%s, want bnd-alpha/cpt-alpha", got.BindingID, got.ConceptID)
	}
	if got.Observation.Population != wantPopulation || !reflect.DeepEqual(got.Observation.Examples, []string{"ex-alpha-1"}) ||
		!got.Observation.ExamplesTruncated {
		t.Fatalf("cpt-alpha aggregate = population %d examples %#v truncated %v, want population %d [ex-alpha-1] true",
			got.Observation.Population, got.Observation.Examples, got.Observation.ExamplesTruncated,
			wantPopulation)
	}
	if page.Entries[1].ConceptID != "cpt-beta" || page.Entries[2].ConceptID != "cpt-gamma" {
		t.Fatalf("identity order = %s, %s; want cpt-beta then cpt-gamma", page.Entries[1].ConceptID, page.Entries[2].ConceptID)
	}
	if page.Entries[1].Observation.Population != 1 || page.Entries[1].Observation.ExamplesTruncated ||
		!reflect.DeepEqual(page.Entries[1].Observation.Examples, []string{"beta-ex"}) {
		t.Fatalf("cpt-beta aggregate = %+v, want population 1, no truncation, examples [beta-ex]", page.Entries[1].Observation)
	}
	gamma := page.Entries[2].Observation
	if !reflect.DeepEqual(gamma.ObservedUnits, []string{"g"}) || !gamma.ObservedUnitsTruncated ||
		gamma.Population != 2 || !gamma.ExamplesTruncated || !reflect.DeepEqual(gamma.Examples, []string{"g1"}) {
		t.Fatalf("cpt-gamma aggregate = %+v, want units [g] truncated, population 2, examples [g1] truncated", gamma)
	}
	if page.NextCursor != "" {
		t.Fatalf("short page must not carry a cursor, got %q", page.NextCursor)
	}
	if page.Build.LeaseToken != "lease-live" || page.Build.LeaseExpiresAt != 1_700_000_000_000 ||
		page.Build.ScannedResources != 7 || page.Build.AuthResourcePath != "fhir/secret/internal" ||
		!reflect.DeepEqual(page.Build.Diagnostics, []string{"stale-diagnostic"}) {
		t.Fatalf("unrestricted build internals = %+v", page.Build)
	}

	opts = baseOpts()
	opts.AuthResourcePathsUnrestricted = pgcat06Unrestricted(false)
	opts.AuthResourcePaths = []string{pgcat06PathGlucose}
	page, err = repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("restricted page", err)
	}
	if len(page.Entries) != 2 || page.Entries[0].ConceptID != "cpt-alpha" || page.Entries[1].ConceptID != "cpt-gamma" {
		t.Fatalf("restricted page identities = %+v", page.Entries)
	}
	if page.Entries[0].Observation.Population != 5 {
		t.Fatalf("restricted cpt-alpha population = %d, want 5 (secret path excluded)", page.Entries[0].Observation.Population)
	}
	if page.Build.ScannedResources != 0 || page.Build.Checkpoint != "" || page.Build.SourceCheckpoint != (catalog.SemanticInventoryCheckpoint{}) ||
		page.Build.LeaseToken != "" || page.Build.LeaseExpiresAt != 0 || page.Build.Diagnostics != nil || page.Build.AuthResourcePath != "" {
		t.Fatalf("restricted build internals were not scrubbed: %+v", page.Build)
	}
	if page.Build.State != catalog.SemanticInventoryComplete || page.Build.BuildID == "" {
		t.Fatalf("restricted page lost the build identity/state: %+v", page.Build)
	}

	opts = baseOpts()
	opts.AuthResourcePathsUnrestricted = pgcat06Unrestricted(false)
	opts.AuthResourcePaths = []string{pgcat06PathNone}
	page, err = repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("restricted-empty page", err)
	}
	if page.Entries == nil || len(page.Entries) != 0 || page.Families != nil || page.State != catalog.SemanticInventoryComplete ||
		page.NextCursor != "" || page.Build.BuildID == "" {
		t.Fatalf("restricted-empty page = %+v, want explicit non-nil empty page with the completed build", page)
	}

	put("src-5", 0, "bnd-beta", "cpt-gamma", pgcat06PathGlucose, "g3", gammaUnits([]string{"g"}))
	opts = baseOpts()
	opts.AuthResourcePathsUnrestricted = pgcat06Unrestricted(true)
	page, err = repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("page after mutation probe", err)
	}
	if page.Entries[2].Observation.Population != 3 || page.Entries[0].Observation.Population != 9 {
		t.Fatalf("mutation probe changed the wrong aggregates: %+v", page.Entries)
	}
}

const (
	pgcat06EscapedPath      = "a\"b\\c\t\u001f"
	pgcat06EscapedSystem    = "urn:fam\"x"
	pgcat06EscapedSelection = `family:["Observation","a\"b\\c\t\u001f","urn:fam\"x","v1"]`
)

func pgcat06FamilyObs(path, system, version string, population int64) catalog.SemanticObservation {
	o := pgcat06Observation(population)
	o.Source.Type = "Observation"
	o.Source.Path = path
	o.Key.Selector = "code.coding[]"
	o.Key.System = system
	o.Key.Version = version
	o.RuleHint = "CODED_CONCEPT_V1"
	o.RuleVersion = "5"
	return o
}

func pgcat06PlainObs(population int64) catalog.SemanticObservation {
	o := pgcat06Observation(population)
	o.Source.Type = "Observation"
	o.Source.Path = "value.coding[]"
	o.RuleHint = "CODED_VALUE_V1"
	o.RuleVersion = "5"
	return o
}

func TestSemanticInventoryPageFamilyGolden(t *testing.T) {
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	put := func(sourceID string, ordinal int, concept, path, system, version, example string, observation catalog.SemanticObservation) {
		s.seedContribution(t, contributionSeed{
			Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
			SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
			ResourceType: "Observation", SourceID: sourceID, Ordinal: ordinal,
			ConceptID: concept, BindingID: "bnd-family", Example: example,
			Observation: pgcat06ObsJSON(t, observation),
		})
	}

	put("f-1", 0, "cpt-fam-v1-a", "code.coding[]", "urn:family-a", "v1", "f-ex-2", pgcat06FamilyObs("code.coding[]", "urn:family-a", "v1", 2))
	put("f-1", 1, "cpt-fam-v1-b", "code.coding[]", "urn:family-a", "v1", "f-ex-1", pgcat06FamilyObs("code.coding[]", "urn:family-a", "v1", 3))
	put("f-2", 0, "cpt-fam-nv-a", "code.coding[]", "urn:family-a", "", "", pgcat06FamilyObs("code.coding[]", "urn:family-a", "", 1))
	put("f-2", 1, "cpt-fam-nv-b", "code.coding[]", "urn:family-a", "", "", pgcat06FamilyObs("code.coding[]", "urn:family-a", "", 1))
	put("f-3", 0, "cpt-fam-esc", pgcat06EscapedPath, pgcat06EscapedSystem, "v1", "esc-ex", pgcat06FamilyObs(pgcat06EscapedPath, pgcat06EscapedSystem, "v1", 1))
	put("p-1", 0, "0-numeric", "value.coding[]", "urn:sys-a", "", "", pgcat06PlainObs(1))
	put("p-2", 0, "cpt-fam-ext", "code.coding[]", "urn:family-a", "v1", "ext-ex", func() catalog.SemanticObservation {
		o := pgcat06FamilyObs("code.coding[]", "urn:family-a", "v1", 4)
		o.ExtensionURLPath = []string{"http://ext"}
		return o
	}())
	put("p-3", 0, "cpt-fam-nosys", "code.coding[]", "", "", "", func() catalog.SemanticObservation {
		o := pgcat06FamilyObs("code.coding[]", "", "", 1)
		return o
	}())
	put("p-4", 0, "cpt-plain", "value.coding[]", "urn:sys-a", "v1", "plain-ex", pgcat06PlainObs(1))
	put("p-5", 0, "family:zz-plain", "value.coding[]", "urn:sys-a", "", "", pgcat06PlainObs(1))

	repo := s.readRepository(t)
	opts := catalog.SemanticInventoryPageOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
		SelectionMode:                 catalog.SemanticInventorySystemVersionCategory,
		BindingID:                     "bnd-family",
	}
	page, err := repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("family page", err)
	}
	if len(page.Entries) != 5 {
		t.Fatalf("family page returned %d plain entries: %+v", len(page.Entries), page.Entries)
	}
	if len(page.Families) != 3 {
		t.Fatalf("family page returned %d families: %+v", len(page.Families), page.Families)
	}
	gotOrder := []string{
		page.Entries[0].ConceptID, page.Entries[1].ConceptID,
		page.Entries[2].ConceptID, page.Entries[3].ConceptID,
		"F:" + page.Families[0].FamilyID[:8], "F:" + page.Families[1].FamilyID[:8], "F:" + page.Families[2].FamilyID[:8],
		page.Entries[4].ConceptID,
	}
	wantOrder := []string{
		"0-numeric", "cpt-fam-ext", "cpt-fam-nosys", "cpt-plain",
		"F:f32acc4b", "F:0a459082", "F:2827c31b", "family:zz-plain",
	}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("interleaved identity order = %v, want %v", gotOrder, wantOrder)
	}

	escaped := catalog.SemanticInventoryFamily{
		SemanticInventoryFamilyReference: catalog.SemanticInventoryFamilyReference{
			FamilyID:  "f32acc4bd611d5349f64539c4280579b644d414660bb37c6d0a42a239801dd58",
			BindingID: "bnd-family", SourceKind: catalog.SemanticInventorySourceFile,
			ResourceType: "Observation", SourcePath: pgcat06EscapedPath, System: pgcat06EscapedSystem, Version: "v1",
		},
		Source: catalog.SemanticObservationSource{
			Canonical: "Observation.code.coding[]", Type: "Observation",
			Profile: "http://profiles.test/Observation", Path: pgcat06EscapedPath,
		},
		KeySelector: "code.coding[]",
		Value: catalog.SemanticObservationValue{
			Selector: "value.value", Type: "string",
		},
		SchemaVersion:     catalog.SemanticObservationSchemaVersion,
		RuleVersion:       "5",
		Completeness:      catalog.SemanticComplete,
		Status:            "SUPPORTED",
		Population:        1,
		Examples:          []string{"esc-ex"},
		ExamplesTruncated: false,
	}
	if !reflect.DeepEqual(page.Families[0], escaped) {
		t.Fatalf("escaped family =\n%#v\nwant\n%#v", page.Families[0], escaped)
	}
	versionless := escaped
	versionless.FamilyID = "0a4590824e29ff0d2f81579035f294e561e7c250ecf10ec956a1d31c3d996c00"
	versionless.SourcePath = "code.coding[]"
	versionless.System = "urn:family-a"
	versionless.Version = ""
	versionless.Source.Path = "code.coding[]"
	versionless.Population = 2
	versionless.Examples = []string{}
	versionless.ExamplesTruncated = true
	if !reflect.DeepEqual(page.Families[1], versionless) {
		t.Fatalf("versionless family =\n%#v\nwant\n%#v", page.Families[1], versionless)
	}
	versioned := versionless
	versioned.FamilyID = "2827c31b6970c91d02bf2fecde13a646dd8f007f1b4ce66eb67bafaed7becbc8"
	versioned.Version = "v1"
	versioned.Population = 5
	versioned.Examples = []string{"f-ex-1"}
	if !reflect.DeepEqual(page.Families[2], versioned) {
		t.Fatalf("versioned family =\n%#v\nwant\n%#v", page.Families[2], versioned)
	}
	if !reflect.DeepEqual(page.Entries[1].Observation.ExtensionURLPath, []string{"http://ext"}) ||
		page.Entries[1].Observation.Population != 4 {
		t.Fatalf("decoy entry = %+v", page.Entries[1])
	}

	var walkedEntries []catalog.SemanticInventoryEntry
	var walkedFamilies []catalog.SemanticInventoryFamily
	cursor := ""
	identitySequence := []string{}
	for rounds := 0; ; rounds++ {
		if rounds > 10 {
			t.Fatalf("identity walk did not terminate: %v", identitySequence)
		}
		step := opts
		step.Limit = 1
		step.Cursor = cursor
		small, err := repo.PageSemanticInventory(s.ctx, step)
		if err != nil {
			s.fail("family page step", err)
		}
		if len(small.Entries) > 1 || len(small.Families) > 1 || len(small.Entries)+len(small.Families) != 1 {
			t.Fatalf("limit-1 page returned %d entries %d families", len(small.Entries), len(small.Families))
		}
		walkedEntries = append(walkedEntries, small.Entries...)
		walkedFamilies = append(walkedFamilies, small.Families...)
		if small.NextCursor == "" {
			if len(small.Entries) == 1 {
				identitySequence = append(identitySequence, small.Entries[0].ConceptID)
			} else {
				identitySequence = append(identitySequence, escapedSelectionOf(small.Families[0]))
			}
			break
		}
		payload := pgcat06DecodeCursor(t, small.NextCursor)
		if payload.Version != 1 || payload.BindingID != "bnd-family" {
			t.Fatalf("cursor payload = %+v, want version 1 and binding bnd-family", payload)
		}
		if len(small.Families) == 1 && payload.ConceptID != small.Families[0].SourcePath {
			if payload.ConceptID != escapedSelectionOf(small.Families[0]) {
				t.Fatalf("family cursor selection %q does not re-derive from the family row", payload.ConceptID)
			}
		}
		identitySequence = append(identitySequence, payload.ConceptID)
		cursor = small.NextCursor
	}
	if len(identitySequence) != 8 {
		t.Fatalf("identity walk produced %d identities: %v", len(identitySequence), identitySequence)
	}
	if identitySequence[4] != pgcat06EscapedSelection {
		t.Fatalf("cursor-carried family selection bytes = %q, want %q", identitySequence[4], pgcat06EscapedSelection)
	}
	walked := catalog.SemanticInventoryPage{Entries: walkedEntries, Families: walkedFamilies, State: catalog.SemanticInventoryComplete}
	full := catalog.SemanticInventoryPage{Entries: page.Entries, Families: page.Families, State: catalog.SemanticInventoryComplete}
	if pageWitness(t, walked) != pageWitness(t, full) {
		t.Fatalf("paged walk diverged from the unpaged read:\n%s\nvs\n%s", pageWitness(t, walked), pageWitness(t, full))
	}

	exactOpts := opts
	exactOpts.SelectionMode = catalog.SemanticInventoryExactConcept
	exact, err := repo.PageSemanticInventory(s.ctx, exactOpts)
	if err != nil {
		s.fail("exact page over family fixtures", err)
	}
	if len(exact.Entries) != 10 || exact.Families != nil {
		t.Fatalf("exact mode over family fixtures returned %d entries, %d families", len(exact.Entries), len(exact.Families))
	}

	origWitness := pageWitness(t, page)
	s.exec(t, "UPDATE "+s.contributionFQN()+
		" SET observation = observation - 'extension_url_path' WHERE concept_id = 'cpt-fam-ext'")
	merged, err := repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("family page after guard drop", err)
	}
	if len(merged.Families) != 3 || merged.Families[2].Population != 9 ||
		!reflect.DeepEqual(merged.Families[2].Examples, []string{"ext-ex"}) {
		t.Fatalf("decoy did not merge into its twin family: %+v", merged.Families)
	}
	if len(merged.Entries) != 4 {
		t.Fatalf("guard drop left %d plain entries, want 4", len(merged.Entries))
	}
	s.exec(t, "UPDATE "+s.contributionFQN()+
		" SET observation = jsonb_set(observation, '{extension_url_path}', '[\"http://ext\"]'::jsonb) WHERE concept_id = 'cpt-fam-ext'")
	restored, err := repo.PageSemanticInventory(s.ctx, opts)
	if err != nil {
		s.fail("family page after guard restore", err)
	}
	if pageWitness(t, restored) != origWitness {
		t.Fatalf("restored page is not byte-identical to the original witness")
	}
}

func escapedSelectionOf(family catalog.SemanticInventoryFamily) string {
	return `family:[` + pgcat06JSONString(family.Source.Type) + "," + pgcat06JSONString(family.SourcePath) + "," +
		pgcat06JSONString(family.System) + "," + pgcat06JSONString(family.Version) + `]`
}

func pgcat06JSONString(value string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\t':
			sb.WriteString(`\t`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

func TestSemanticInventoryResolveExactGolden(t *testing.T) {
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)

	low, high := pgcat06Hex64("resolve-a"), pgcat06Hex64("resolve-b")
	if low > high {
		low, high = high, low
	}
	obsAt := func(code, display string, population int64) catalog.SemanticObservation {
		o := pgcat06Observation(population)
		o.Source.Type = "Observation"
		o.Key.Selector = "code.coding[]"
		o.Key.System = "urn:resolve"
		o.Key.Code = code
		o.Key.Display = display
		o.RuleHint = "CODED_CONCEPT_V1"
		o.RuleVersion = "5"
		return o
	}
	lowObs := obsAt("low", "SecretLow", 1)
	highObs := obsAt("high", "GlucoseHigh", 2)
	foreignObs := obsAt("foreign", "ForeignWinsIfLeaked", 99)
	seed := func(key, path, example string, observation catalog.SemanticObservation, sourceID string) {
		s.seedContribution(t, contributionSeed{
			Key: key, Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
			SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: path,
			ResourceType: "Observation", SourceID: sourceID, Ordinal: 0,
			ConceptID: "cpt-resolve", BindingID: "bnd-resolve", Example: example,
			Observation: pgcat06ObsJSON(t, observation),
		})
	}
	seed(low, pgcat06PathSecret, "low-ex", lowObs, "res-low")
	seed(high, pgcat06PathGlucose, "high-ex", highObs, "res-high")
	s.seedContribution(t, contributionSeed{
		Key: pgcat06Hex64("resolve-foreign"), Project: pgcat06Project, Generation: "gen-2", BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "res-f", Ordinal: 0,
		ConceptID: "cpt-resolve", BindingID: "bnd-resolve", Example: "f",
		Observation: pgcat06ObsJSON(t, foreignObs),
	})

	repo := s.readRepository(t)
	request := func(unrestricted bool, paths []string, refs ...catalog.SemanticInventoryReference) catalog.SemanticInventoryResolveResult {
		t.Helper()
		result, err := repo.ResolveSemanticInventorySelections(s.ctx, catalog.SemanticInventoryResolveOptions{
			Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
			AuthResourcePathsUnrestricted: pgcat06Unrestricted(unrestricted),
			AuthResourcePaths:             paths,
			References:                    refs,
		})
		if err != nil {
			s.fail("resolve selections", err)
		}
		return result
	}
	refs := func() []catalog.SemanticInventoryReference {
		return []catalog.SemanticInventoryReference{
			{ConceptID: "cpt-missing", BindingID: "bnd-resolve"},
			{ConceptID: "cpt-resolve", BindingID: "bnd-resolve"},
		}
	}

	restricted := request(false, []string{pgcat06PathGlucose}, refs()...)
	if len(restricted.Entries) != 1 || restricted.Entries[0].ConceptID != "cpt-resolve" ||
		!reflect.DeepEqual(restricted.Entries[0].Observation, highObs) {
		t.Fatalf("restricted resolve = entries %+v state %v build %+v, want the authorized glucose row verbatim",
			restricted.Entries, restricted.State, restricted.Build)
	}
	if restricted.State != catalog.SemanticInventoryComplete || restricted.Build.ScannedResources != 0 ||
		restricted.Build.LeaseToken != "" || restricted.Build.Diagnostics != nil {
		t.Fatalf("restricted resolve build = %+v state %v", restricted.Build, restricted.State)
	}

	unrestricted := request(true, nil, refs()...)
	if len(unrestricted.Entries) != 1 || !reflect.DeepEqual(unrestricted.Entries[0].Observation, lowObs) {
		t.Fatalf("unrestricted resolve = %+v, want the least-key secret row verbatim", unrestricted.Entries)
	}
	if unrestricted.Build.ScannedResources != 7 {
		t.Fatalf("unrestricted resolve must expose build internals verbatim: %+v", unrestricted.Build)
	}
	if unrestricted.Build.Project != pgcat06Project || unrestricted.Build.DatasetGeneration != pgcat06Generation ||
		unrestricted.Build.BuildID != catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation) {
		t.Fatalf("resolve must return the fully identified build: %+v", unrestricted.Build)
	}

	empty := request(true, nil, catalog.SemanticInventoryReference{ConceptID: "cpt-none", BindingID: "bnd-none"})
	if empty.Entries == nil || len(empty.Entries) != 0 || empty.State != catalog.SemanticInventoryComplete {
		t.Fatalf("all-unmatched resolve = %+v, want non-nil empty entries with the completed build", empty)
	}
}

func TestSemanticInventoryResolveFamilyGolden(t *testing.T) {
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)

	fam := func(path, system, version string, population int64) catalog.SemanticObservation {
		o := pgcat06Observation(population)
		o.Source.Type = "Observation"
		o.Source.Path = path
		o.Key.Selector = "code.coding[]"
		o.Key.System = system
		o.Key.Version = version
		o.RuleHint = "CODED_CONCEPT_V1"
		o.RuleVersion = "5"
		return o
	}
	put := func(sourceID string, concept, path string, observation catalog.SemanticObservation, authPath string) {
		s.seedContribution(t, contributionSeed{
			Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
			SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: authPath,
			ResourceType: "Observation", SourceID: sourceID, Ordinal: 0,
			ConceptID: concept, BindingID: "bnd-resolve-family",
			Observation: pgcat06ObsJSON(t, observation),
		})
	}
	put("r-1", "cpt-rv2-a", "code.coding", fam("code.coding", "urn:sys-f", "v2", 1), pgcat06PathGlucose)
	put("r-2", "cpt-rv2-b", "code.coding", fam("code.coding", "urn:sys-f", "v2", 2), pgcat06PathVisit)
	put("r-3", "cpt-rv2-c", "code.coding", fam("code.coding", "urn:sys-f", "v2", 100), pgcat06PathSecret)
	put("r-4", "cpt-rnv-a", "code.coding", fam("code.coding", "urn:sys-f", "", 5), pgcat06PathGlucose)
	put("r-5", "cpt-rv2-ext", "code.coding", func() catalog.SemanticObservation {
		o := fam("code.coding", "urn:sys-f", "v2", 77)
		o.ExtensionURLPath = []string{"http://ext"}
		return o
	}(), pgcat06PathGlucose)
	put("r-6", "cpt-rv2-rule", "code.coding", func() catalog.SemanticObservation {
		o := fam("code.coding", "urn:sys-f", "v2", 77)
		o.RuleHint = "CODED_VALUE_V1"
		return o
	}(), pgcat06PathGlucose)
	put("r-7", "cpt-rv2-type", "code.coding", func() catalog.SemanticObservation {
		o := fam("code.coding", "urn:sys-f", "v2", 77)
		o.Source.Type = "Encounter"
		return o
	}(), pgcat06PathGlucose)
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "r-8", Ordinal: 0,
		ConceptID: "cpt-rnull", BindingID: "bnd-resolve-family",
		Observation: `{"source":{"type":"Observation","path":"code.coding"},"key":{"selector":"code.coding[]","system":"urn:sys-f","version":null},"rule_hint":"CODED_CONCEPT_V1","population":40}`,
	})
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: "gen-2", BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "r-9", Ordinal: 0,
		ConceptID: "cpt-rv2-gen2", BindingID: "bnd-resolve-family",
		Observation: pgcat06ObsJSON(t, fam("code.coding", "urn:sys-f", "v2", 999)),
	})

	v2Ref := catalog.SemanticInventoryFamilyReference{
		BindingID: "bnd-resolve-family", SourceKind: catalog.SemanticInventorySourceFile,
		ResourceType: "Observation", SourcePath: "code.coding", System: "urn:sys-f", Version: "v2",
	}
	v2Ref.FamilyID = catalog.SemanticInventoryFamilyID(v2Ref)
	if v2Ref.FamilyID != "1e2abae9e0d5d0fd601edc065d704e5e8e447d7076ba67cfacd1c3b7b088ca43" {
		t.Fatalf("v2 family digest drifted from the pinned literal: %s", v2Ref.FamilyID)
	}
	nvRef := v2Ref
	nvRef.Version = ""
	nvRef.FamilyID = catalog.SemanticInventoryFamilyID(nvRef)
	if nvRef.FamilyID != "5a9a79e24891c171724106b7b46f237e68abd0e603243bbde139f243e6e0ae1d" {
		t.Fatalf("versionless family digest drifted from the pinned literal: %s", nvRef.FamilyID)
	}
	wrongKind := v2Ref
	wrongKind.SourceKind = catalog.SemanticInventorySourceRetained
	wrongKind.FamilyID = catalog.SemanticInventoryFamilyID(wrongKind)
	missing := v2Ref
	missing.System = "urn:sys-nowhere"
	missing.FamilyID = catalog.SemanticInventoryFamilyID(missing)

	repo := s.readRepository(t)
	result, err := repo.ResolveSemanticInventorySelections(s.ctx, catalog.SemanticInventoryResolveOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(false),
		AuthResourcePaths:             []string{pgcat06PathGlucose},
		FamilyReferences:              []catalog.SemanticInventoryFamilyReference{nvRef, missing, v2Ref, wrongKind},
	})
	if err != nil {
		s.fail("resolve families", err)
	}
	if len(result.Families) != 2 ||
		result.Families[0].FamilyID != nvRef.FamilyID || result.Families[1].FamilyID != v2Ref.FamilyID {
		t.Fatalf("family resolve = %+v, want [versionless, v2] in request order", result.Families)
	}
	if result.Families[0].Population != 5 || result.Families[1].Population != 1 {
		t.Fatalf("restricted family populations = %d, %d; want 5 and 1 (visit/secret rows excluded)",
			result.Families[0].Population, result.Families[1].Population)
	}
	want := catalog.SemanticInventoryFamily{
		SemanticInventoryFamilyReference: v2Ref,
		Source: catalog.SemanticObservationSource{
			Canonical: "Observation.code.coding[]", Type: "Observation",
			Profile: "http://profiles.test/Observation", Path: "code.coding",
		},
		KeySelector: "code.coding[]",
		Value: catalog.SemanticObservationValue{
			Selector: "value.value", Type: "string",
		},
		SchemaVersion:     catalog.SemanticObservationSchemaVersion,
		RuleVersion:       "5",
		Completeness:      catalog.SemanticComplete,
		Status:            "SUPPORTED",
		Population:        1,
		Examples:          []string{},
		ExamplesTruncated: false,
	}
	if !reflect.DeepEqual(result.Families[1], want) {
		t.Fatalf("restricted v2 family =\n%#v\nwant\n%#v", result.Families[1], want)
	}

	unrestrictedResult, err := repo.ResolveSemanticInventorySelections(s.ctx, catalog.SemanticInventoryResolveOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
		FamilyReferences:              []catalog.SemanticInventoryFamilyReference{v2Ref},
	})
	if err != nil {
		s.fail("resolve families unrestricted", err)
	}
	if len(unrestrictedResult.Families) != 1 || unrestrictedResult.Families[0].Population != 103 {
		t.Fatalf("unrestricted v2 family population = %+v, want 103 (1+2+100 across all paths)", unrestrictedResult.Families)
	}

	s.exec(t, "UPDATE "+s.contributionFQN()+" SET observation = observation #- '{key,version}' WHERE source_id = 'r-8'")
	nullProbe, err := repo.ResolveSemanticInventorySelections(s.ctx, catalog.SemanticInventoryResolveOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(false),
		AuthResourcePaths:             []string{pgcat06PathGlucose},
		FamilyReferences:              []catalog.SemanticInventoryFamilyReference{nvRef},
	})
	if err != nil {
		s.fail("resolve families after null-leaf mutation", err)
	}
	if len(nullProbe.Families) != 1 || nullProbe.Families[0].Population != 45 {
		t.Fatalf("absent-version row did not join the versionless family: %+v", nullProbe.Families)
	}
	s.exec(t, "UPDATE "+s.contributionFQN()+
		" SET observation = jsonb_set(observation, '{key,version}', 'null'::jsonb, true) WHERE source_id = 'r-8'")
	reNull, err := repo.ResolveSemanticInventorySelections(s.ctx, catalog.SemanticInventoryResolveOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(false),
		AuthResourcePaths:             []string{pgcat06PathGlucose},
		FamilyReferences:              []catalog.SemanticInventoryFamilyReference{nvRef},
	})
	if err != nil {
		s.fail("resolve families after null-leaf restore", err)
	}
	if len(reNull.Families) != 1 || reNull.Families[0].Population != 5 {
		t.Fatalf("JSON-null-version row leaked into the versionless family: %+v", reNull.Families)
	}
}

func TestSemanticInventoryStorageStates(t *testing.T) {
	s := newPgcat06Service(t)
	repo := s.readRepository(t)
	pageOpts := catalog.SemanticInventoryPageOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
	}
	resolveOpts := func() catalog.SemanticInventoryResolveOptions {
		return catalog.SemanticInventoryResolveOptions{
			Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
			AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
			References:                    []catalog.SemanticInventoryReference{{ConceptID: "cpt-x", BindingID: "bnd-x"}},
		}
	}

	if _, err := s.iso.Exec(s.ctx, "DROP TABLE "+s.buildFQN()+" CASCADE"); err != nil {
		s.fail("drop build table", err)
	}
	page, err := repo.PageSemanticInventory(s.ctx, pageOpts)
	if err != nil {
		s.fail("page without build table", err)
	}
	if page.Entries == nil || len(page.Entries) != 0 || page.State != catalog.SemanticInventoryUnknown ||
		!reflect.DeepEqual(page.Build, catalog.SemanticInventoryBuild{}) {
		t.Fatalf("page without build table = %+v, want unknown-state empty", page)
	}
	result, err := repo.ResolveSemanticInventorySelections(s.ctx, resolveOpts())
	if err != nil {
		s.fail("resolve without build table", err)
	}
	if result.Entries == nil || len(result.Entries) != 0 || result.State != catalog.SemanticInventoryUnknown {
		t.Fatalf("resolve without build table = %+v, want unknown-state empty", result)
	}

	if _, err := s.iso.Exec(s.ctx, "DELETE FROM "+s.ledgerFQN()+" WHERE version >= 6"); err != nil {
		s.fail("reset ledger below the build migration", err)
	}
	if _, err := storepostgres.MigrateSchema(s.ctx, s.openClient(t), s.schema); err != nil {
		s.fail("re-migrate schema", err)
	}
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	if _, err := s.iso.Exec(s.ctx, "DROP TABLE "+s.contributionFQN()+" CASCADE"); err != nil {
		s.fail("drop contribution table", err)
	}
	page, err = repo.PageSemanticInventory(s.ctx, pageOpts)
	if err != nil {
		s.fail("page without contribution table", err)
	}
	if page.State != catalog.SemanticInventoryUnknown || page.Entries == nil || len(page.Entries) != 0 {
		t.Fatalf("page without contribution table = %+v, want the Arango-matching unknown-empty", page)
	}
	_, err = repo.ResolveSemanticInventorySelections(s.ctx, resolveOpts())
	var missing *SemanticInventoryStorageMissingError
	if !errors.As(err, &missing) || !strings.Contains(err.Error(), semanticInventoryContributionTableName) {
		t.Fatalf("resolve without contribution table error = %v, want named SemanticInventoryStorageMissingError", err)
	}
	if _, err := s.iso.Exec(s.ctx, "DELETE FROM "+s.ledgerFQN()+" WHERE version >= 9"); err != nil {
		s.fail("reset ledger below the contribution migration", err)
	}
	if _, err := storepostgres.MigrateSchema(s.ctx, s.openClient(t), s.schema); err != nil {
		s.fail("restore contribution table", err)
	}
	page, err = repo.PageSemanticInventory(s.ctx, pageOpts)
	if err != nil {
		s.fail("page after table restore", err)
	}
	if page.State != catalog.SemanticInventoryComplete || page.Entries == nil || len(page.Entries) != 0 {
		t.Fatalf("page over restored-but-empty table = %+v, want complete-state empty (distinct from the dropped-table unknown)", page)
	}
}

func TestSemanticInventoryEntryIndexBoundaryRefusesByName(t *testing.T) {
	s := newPgcat06Service(t)
	seed := pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation)
	seed.EntryIndexVersion = catalog.SemanticInventoryEntryIndexVersion
	s.seedBuild(t, seed)
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "idx-1", Ordinal: 0,
		ConceptID: "cpt-idx", BindingID: "bnd-idx", Example: "e",
		Observation: pgcat06ObsJSON(t, pgcat06Observation(1)),
	})
	repo := s.readRepository(t)
	pageOpts := catalog.SemanticInventoryPageOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
	}
	resolveOpts := catalog.SemanticInventoryResolveOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
		References:                    []catalog.SemanticInventoryReference{{ConceptID: "cpt-idx", BindingID: "bnd-idx"}},
	}

	_, err := repo.PageSemanticInventory(s.ctx, pageOpts)
	var unsupported *SemanticInventoryEntriesUnsupportedError
	if !errors.As(err, &unsupported) || !errors.Is(err, ErrSemanticInventoryEntriesUnsupported) {
		t.Fatalf("page at the entry-index boundary error = %v, want named SemanticInventoryEntriesUnsupportedError", err)
	}
	if unsupported.Project != pgcat06Project || unsupported.DatasetGeneration != pgcat06Generation ||
		unsupported.BuildID != buildID || unsupported.EntryIndexVersion != catalog.SemanticInventoryEntryIndexVersion {
		t.Fatalf("refusal fields = %+v", unsupported)
	}
	if _, err := repo.ResolveSemanticInventorySelections(s.ctx, resolveOpts); !errors.Is(err, ErrSemanticInventoryEntriesUnsupported) {
		t.Fatalf("resolve at the entry-index boundary error = %v, want the named boundary error", err)
	}

	s.exec(t, "UPDATE "+s.buildFQN()+" SET entry_index_version = $1", catalog.SemanticInventoryEntryIndexVersion-1)
	page, err := repo.PageSemanticInventory(s.ctx, pageOpts)
	if err != nil || len(page.Entries) != 1 || page.Entries[0].ConceptID != "cpt-idx" {
		t.Fatalf("below the boundary the page must serve: %+v err %v", page, err)
	}
	if _, err := repo.ResolveSemanticInventorySelections(s.ctx, resolveOpts); err != nil {
		t.Fatalf("below the boundary resolve must serve: %v", err)
	}
	s.exec(t, "UPDATE "+s.buildFQN()+" SET entry_index_version = $1", catalog.SemanticInventoryEntryIndexVersion)
	if _, err := repo.PageSemanticInventory(s.ctx, pageOpts); !errors.Is(err, ErrSemanticInventoryEntriesUnsupported) {
		t.Fatalf("restored boundary must refuse by name again: %v", err)
	}
}

func pgcat06ContributionInsert(fqn string, seed contributionSeed) (string, []any) {
	if seed.Key == "" {
		seed.Key = pgcat06Hex64(seed.Project + "/" + seed.Generation + "/" + seed.BuildID + "/" +
			seed.SourceKind + "/" + seed.SourceID + "/" + fmt.Sprint(seed.Ordinal) + "/" + seed.ConceptID)
	}
	return "INSERT INTO " + fqn +
			" (contribution_key, project, dataset_generation, build_id, source_kind, auth_resource_path," +
			" resource_type, source_id, ordinal, concept_id, binding_id, example, payload_digest, observation)" +
			" VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::jsonb)",
		[]any{seed.Key, seed.Project, catalog.NormalizeDatasetGeneration(seed.Generation), seed.BuildID,
			seed.SourceKind, seed.AuthResourcePath, seed.ResourceType, seed.SourceID, seed.Ordinal,
			seed.ConceptID, seed.BindingID, seed.Example, pgcat06Hex64("digest/" + seed.Key), seed.Observation}
}

func TestSemanticInventoryTwoConnectionReadConsistency(t *testing.T) {
	const rounds = 10
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "stable-1", Ordinal: 0,
		ConceptID: "cpt-stable", BindingID: "bnd-stable", Example: "s",
		Observation: pgcat06ObsJSON(t, pgcat06Observation(1)),
	})

	writer, writerPid := s.openDedicatedClient(t, "writer")
	readerA, pidA := s.openDedicatedClient(t, "reader-a")
	readerB, pidB := s.openDedicatedClient(t, "reader-b")
	if writerPid == pidA || writerPid == pidB || pidA == pidB {
		t.Fatalf("racing backends share pg_backend_pid: %d %d %d", writerPid, pidA, pidB)
	}
	repoA, err := NewSemanticReadRepository(readerA, s.schema)
	if err != nil {
		s.fail("reader A repository", err)
	}
	repoB, err := NewSemanticReadRepository(readerB, s.schema)
	if err != nil {
		s.fail("reader B repository", err)
	}
	pageOpts := catalog.SemanticInventoryPageOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
	}
	resolveOpts := catalog.SemanticInventoryResolveOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
		References:                    []catalog.SemanticInventoryReference{{ConceptID: "cpt-stable", BindingID: "bnd-stable"}},
	}
	baselinePage, err := repoA.PageSemanticInventory(s.ctx, pageOpts)
	if err != nil {
		s.fail("baseline page", err)
	}
	baselineResolve, err := repoA.ResolveSemanticInventorySelections(s.ctx, resolveOpts)
	if err != nil {
		s.fail("baseline resolve", err)
	}
	expectedPageWitness, expectedResolveWitness := pageWitness(t, baselinePage), resolveWitness(t, baselineResolve)

	for round := 0; round < rounds; round++ {
		commit := round >= rounds/2
		for _, pair := range []struct {
			label  string
			client *storepostgres.Client
		}{{"writer", writer}, {"reader-a", readerA}, {"reader-b", readerB}} {
			if pid := s.witnessClientPid(t, pair.client, pair.label); pid <= 0 {
				t.Fatalf("round %d: %s backend is not live", round, pair.label)
			}
		}
		conn, err := writer.Acquire(s.ctx)
		if err != nil {
			s.fail("acquire writer", err)
		}
		concept := fmt.Sprintf("cpt-race-%d", round)
		sql, args := pgcat06ContributionInsert(s.contributionFQN(), contributionSeed{
			Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
			SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
			ResourceType: "Observation", SourceID: fmt.Sprintf("race-%d", round), Ordinal: 0,
			ConceptID: concept, BindingID: "bnd-stable", Example: "r",
			Observation: pgcat06ObsJSON(t, pgcat06Observation(1)),
		})
		if _, err := conn.Exec(s.ctx, "BEGIN"); err != nil {
			conn.Release()
			s.fail("begin writer transaction", err)
		}
		if _, err := conn.Exec(s.ctx, sql, args...); err != nil {
			conn.Release()
			s.fail("insert inside writer transaction", err)
		}
		for name, repo := range map[string]*SemanticReadRepository{"A": repoA, "B": repoB} {
			page, err := repo.PageSemanticInventory(s.ctx, pageOpts)
			if err != nil {
				conn.Exec(s.ctx, "ROLLBACK")
				conn.Release()
				s.fail("reader "+name+" page during open transaction", err)
			}
			if w := pageWitness(t, page); w != expectedPageWitness {
				conn.Exec(s.ctx, "ROLLBACK")
				conn.Release()
				t.Fatalf("round %d: reader %s saw the in-flight row: %s", round, name, w)
			}
			result, err := repo.ResolveSemanticInventorySelections(s.ctx, resolveOpts)
			if err != nil {
				conn.Exec(s.ctx, "ROLLBACK")
				conn.Release()
				s.fail("reader "+name+" resolve during open transaction", err)
			}
			if w := resolveWitness(t, result); w != expectedResolveWitness {
				conn.Exec(s.ctx, "ROLLBACK")
				conn.Release()
				t.Fatalf("round %d: reader %s resolve witness drifted: %s", round, name, w)
			}
		}
		if commit {
			if _, err := conn.Exec(s.ctx, "COMMIT"); err != nil {
				conn.Release()
				s.fail("commit writer transaction", err)
			}
		} else {
			if _, err := conn.Exec(s.ctx, "ROLLBACK"); err != nil {
				conn.Release()
				s.fail("rollback writer transaction", err)
			}
		}
		conn.Release()

		pageA, err := repoA.PageSemanticInventory(s.ctx, pageOpts)
		if err != nil {
			s.fail("post-transaction page A", err)
		}
		pageB, err := repoB.PageSemanticInventory(s.ctx, pageOpts)
		if err != nil {
			s.fail("post-transaction page B", err)
		}
		if wa, wb := pageWitness(t, pageA), pageWitness(t, pageB); wa != wb {
			t.Fatalf("round %d: two backends disagree after the transaction:\n%s\nvs\n%s", round, wa, wb)
		}
		sawCommitted := false
		for _, entry := range pageA.Entries {
			if entry.ConceptID == concept {
				sawCommitted = true
			}
		}
		if sawCommitted != commit {
			t.Fatalf("round %d: committed=%v but readers saw the row: %v", round, commit, sawCommitted)
		}
		if !commit {
			if w := pageWitness(t, pageA); w != expectedPageWitness {
				t.Fatalf("round %d: aborted write leaked into the readers' witness: %s", round, w)
			}
		} else {
			expectedPageWitness = pageWitness(t, pageA)
		}
	}
}

type pgcat06SeamSource struct {
	inner  storepostgres.ConnSource
	mu     sync.Mutex
	calls  int
	seamAt int
	fired  bool
	seam   func()
}

func (z *pgcat06SeamSource) Acquire(ctx context.Context) (*pgxpool.Conn, error) {
	z.mu.Lock()
	z.calls++
	if z.calls == z.seamAt && !z.fired {
		z.fired = true
		z.seam()
	}
	z.mu.Unlock()
	return z.inner.Acquire(ctx)
}

func pgcat06PgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestSemanticInventoryCrashSeams(t *testing.T) {
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "seam-1", Ordinal: 0,
		ConceptID: "cpt-seam", BindingID: "bnd-seam", Example: "s",
		Observation: pgcat06ObsJSON(t, pgcat06Observation(1)),
	})
	pageOpts := catalog.SemanticInventoryPageOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
	}
	baseline, err := s.readRepository(t).PageSemanticInventory(s.ctx, pageOpts)
	if err != nil {
		s.fail("baseline page", err)
	}
	baselineWitness := pageWitness(t, baseline)

	type pageOutcome struct {
		page catalog.SemanticInventoryPage
		err  error
	}
	waitForLockWait := func(t *testing.T, pid int32) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			var waiting bool
			if err := s.admin.QueryRow(s.ctx,
				"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND wait_event_type = 'Lock')",
				pid).Scan(&waiting); err != nil {
				s.fail("poll pg_stat_activity", err)
			}
			if waiting {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("backend %d never entered a lock wait", pid)
			}
			select {
			case <-s.ctx.Done():
				t.Fatalf("context expired waiting for the lock wait")
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	terminate := func(t *testing.T, pid int32) {
		t.Helper()
		var killed bool
		if err := s.admin.QueryRow(s.ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&killed); err != nil {
			s.fail("terminate backend", err)
		}
		if !killed {
			t.Fatalf("pg_terminate_backend(%d) reported no live backend", pid)
		}
	}

	locker, lockerPid := s.openDedicatedClient(t, "lock-holder")
	lockConn, err := locker.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire lock holder", err)
	}
	if _, err := lockConn.Exec(s.ctx, "BEGIN; LOCK TABLE "+s.contributionFQN()+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		lockConn.Release()
		s.fail("take table lock", err)
	}
	reader, readerPid := s.openDedicatedClient(t, "locked-reader")
	readerRepo, err := NewSemanticReadRepository(reader, s.schema)
	if err != nil {
		s.fail("locked reader repository", err)
	}
	outcome := make(chan pageOutcome, 1)
	go func() {
		page, err := readerRepo.PageSemanticInventory(s.ctx, pageOpts)
		outcome <- pageOutcome{page, err}
	}()
	waitForLockWait(t, readerPid)
	terminate(t, lockerPid)
	select {
	case res := <-outcome:
		if res.err != nil {
			t.Fatalf("reader failed after the lock holder was terminated: %s", s.scrub(res.err))
		}
		if w := pageWitness(t, res.page); w != baselineWitness {
			t.Fatalf("reader after lock-holder crash returned the wrong page: %s", w)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("reader never completed after the lock holder was terminated")
	}
	lockConn.Release()
	if healedPid := s.witnessClientPid(t, locker, "lock-holder (healed)"); healedPid == lockerPid {
		t.Fatalf("crashed lock-holder pool reused backend pid %d", lockerPid)
	}

	lockConn, err = locker.Acquire(s.ctx)
	if err != nil {
		s.fail("re-acquire lock holder", err)
	}
	if _, err := lockConn.Exec(s.ctx, "BEGIN; LOCK TABLE "+s.contributionFQN()+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		lockConn.Release()
		s.fail("take table lock (second seam)", err)
	}
	reader2, reader2Pid := s.openDedicatedClient(t, "blocked-reader")
	reader2Repo, err := NewSemanticReadRepository(reader2, s.schema)
	if err != nil {
		s.fail("blocked reader repository", err)
	}
	outcome2 := make(chan pageOutcome, 1)
	go func() {
		page, err := reader2Repo.PageSemanticInventory(s.ctx, pageOpts)
		outcome2 <- pageOutcome{page, err}
	}()
	waitForLockWait(t, reader2Pid)
	terminate(t, reader2Pid)
	select {
	case res := <-outcome2:
		if res.err == nil {
			t.Fatalf("terminated reader reported success")
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("terminated reader never returned an error")
	}
	if _, err := lockConn.Exec(s.ctx, "COMMIT"); err != nil {
		lockConn.Release()
		s.fail("release second lock", err)
	}
	lockConn.Release()
	if healedPid := s.witnessClientPid(t, reader2, "blocked-reader (healed)"); healedPid == reader2Pid {
		t.Fatalf("terminated reader pool reused backend pid %d", reader2Pid)
	}
	page, err := reader2Repo.PageSemanticInventory(s.ctx, pageOpts)
	if err != nil {
		s.fail("recovered reader page", err)
	}
	if w := pageWitness(t, page); w != baselineWitness {
		t.Fatalf("recovered reader returned the wrong page: %s", w)
	}

	dropSeam := func() {
		if _, err := s.iso.Exec(s.ctx, "DROP TABLE "+s.contributionFQN()+" CASCADE"); err != nil {
			s.fail("drop contribution table at seam", err)
		}
	}
	for _, tc := range []struct {
		name string
		run  func(repo *SemanticReadRepository) error
		seam int
	}{
		{"page", func(repo *SemanticReadRepository) error {
			_, err := repo.PageSemanticInventory(s.ctx, pageOpts)
			return err
		}, 4},
		{"resolve", func(repo *SemanticReadRepository) error {
			_, err := repo.ResolveSemanticInventorySelections(s.ctx, catalog.SemanticInventoryResolveOptions{
				Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
				AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
				References:                    []catalog.SemanticInventoryReference{{ConceptID: "cpt-seam", BindingID: "bnd-seam"}},
			})
			return err
		}, 4},
	} {
		client := s.openClient(t)
		seam := &pgcat06SeamSource{inner: client, seamAt: tc.seam, seam: dropSeam}
		repo, err := NewSemanticReadRepository(seam, s.schema)
		if err != nil {
			s.fail("seam repository "+tc.name, err)
		}
		err = tc.run(repo)
		if err == nil {
			t.Fatalf("%s: dropped table between check and statement returned no error (disguised empty page)", tc.name)
		}
		if code := pgcat06PgErrorCode(err); code != "42P01" {
			t.Fatalf("%s: dropped-table error code = %q (%s), want 42P01", tc.name, code, s.scrub(err))
		}
		if !seam.fired {
			t.Fatalf("%s: the seam never fired (statement order changed under the test)", tc.name)
		}
		if _, err := s.iso.Exec(s.ctx, "DELETE FROM "+s.ledgerFQN()+" WHERE version >= 9"); err != nil {
			s.fail("reset ledger after seam", err)
		}
		if _, err := storepostgres.MigrateSchema(s.ctx, s.openClient(t), s.schema); err != nil {
			s.fail("restore contribution table after seam", err)
		}
	}
}

func TestSemanticInventoryPlanShapeEvidence(t *testing.T) {
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	seedBulk := func(project, generation, sourceIDPrefix string, count int) {
		if _, err := s.iso.Exec(s.ctx, "INSERT INTO "+s.contributionFQN()+
			" (contribution_key, project, dataset_generation, build_id, source_kind, auth_resource_path,"+
			" resource_type, source_id, ordinal, concept_id, binding_id, example, payload_digest, observation)"+
			" SELECT md5($5 || gs::text || 'ka') || md5($5 || gs::text || 'kb'), $1, $2, $3, 'file', $4,"+
			" 'Observation', $5 || gs, 0, 'cpt-' || gs, 'bnd-' || (gs % 25), 'ex-' || gs,"+
			" md5(gs::text || 'da') || md5(gs::text || 'db'), '{\"population\":1,\"rule_hint\":\"CODED_VALUE_V1\",\"source\":{\"type\":\"Observation\",\"path\":\"code.coding[]\"},\"key\":{\"system\":\"urn:bulk\"}}'::jsonb"+
			" FROM generate_series(1, $6) gs",
			project, catalog.NormalizeDatasetGeneration(generation), buildID, pgcat06PathGlucose, sourceIDPrefix, count); err != nil {
			s.fail("seed bulk contributions", err)
		}
	}
	seedBulk(pgcat06Project, pgcat06Generation, "bulk-", 4000)
	seedBulk("cat06-noise", pgcat06Generation, "noise-", 4000)
	if _, err := s.iso.Exec(s.ctx, "ANALYZE "+s.contributionFQN()); err != nil {
		s.fail("analyze contribution table", err)
	}

	explain := func(label, sql string, args ...any) string {
		t.Helper()
		rows, err := s.iso.Query(s.ctx, "EXPLAIN (FORMAT TEXT) "+sql, args...)
		if err != nil {
			s.fail("explain "+label, err)
		}
		defer rows.Close()
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				s.fail("scan "+label+" plan", err)
			}
			plan.WriteString(line)
			plan.WriteString("\n")
		}
		if err := rows.Err(); err != nil {
			s.fail("read "+label+" plan", err)
		}
		t.Logf("%s plan:\n%s", label, plan.String())
		return plan.String()
	}

	pageSQL, pageArgs := semanticPageQuery(s.contributionFQN(), semanticPageParams{
		project: pgcat06Project, generation: pgcat06Generation, buildID: buildID,
		sourceKind: catalog.SemanticInventorySourceFile, unrestricted: true, paths: []string{},
		limit: 50,
	})
	if plan := explain("page", pageSQL, pageArgs...); !strings.Contains(plan, "fhir_semantic_inventory_contribution_scope_ix") {
		t.Fatalf("page plan does not touch the target-contract scope index")
	}

	scope := resolveScope{
		project: pgcat06Project, generation: pgcat06Generation, buildID: buildID,
		sourceKind: catalog.SemanticInventorySourceFile, unrestricted: true, paths: []string{},
		observationSchema: catalog.SemanticObservationSchemaVersion,
	}
	exactSQL, exactArgs := semanticResolveReferencesQuery(s.contributionFQN(), scope, []catalog.SemanticInventoryReference{
		{ConceptID: "cpt-42", BindingID: "bnd-42"},
	})
	if plan := explain("resolve-exact", exactSQL, exactArgs...); !strings.Contains(plan, "fhir_semantic_inventory_contribution") {
		t.Fatalf("resolve-exact plan does not scan the contribution table at all")
	}
	family := catalog.SemanticInventoryFamilyReference{
		BindingID: "bnd-42", SourceKind: catalog.SemanticInventorySourceFile,
		ResourceType: "Observation", SourcePath: "code.coding[]", System: "urn:bulk", Version: "",
	}
	familySQL, familyArgs := semanticResolveFamiliesQuery(s.contributionFQN(), scope, []catalog.SemanticInventoryFamilyReference{family})
	if plan := explain("resolve-family", familySQL, familyArgs...); !strings.Contains(plan, "fhir_semantic_inventory_contribution") {
		t.Fatalf("resolve-family plan does not scan the contribution table at all")
	}
}

func TestSemanticInventoryStorageContract(t *testing.T) {
	s := newPgcat06Service(t)
	var indexDef string
	if err := s.iso.QueryRow(s.ctx,
		"SELECT indexdef FROM pg_indexes WHERE schemaname = $1 AND tablename = $2"+
			" AND indexname = 'fhir_semantic_inventory_contribution_scope_ix'",
		s.schema, semanticInventoryContributionTableName).Scan(&indexDef); err != nil {
		s.fail("read scope index definition", err)
	}
	for _, want := range []string{
		"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path",
	} {
		if !strings.Contains(indexDef, want) {
			t.Fatalf("scope index definition %q missing column %q", indexDef, want)
		}
	}
	if !strings.Contains(indexDef, `(project, dataset_generation, build_id, source_kind, binding_id, concept_id, auth_resource_path)`) {
		t.Fatalf("scope index definition %q does not carry the contracted column order", indexDef)
	}

	var ledgerVersions []int64
	rows, err := s.iso.Query(s.ctx, "SELECT version FROM "+s.ledgerFQN()+" ORDER BY version")
	if err != nil {
		s.fail("read migration ledger", err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			s.fail("scan migration ledger", err)
		}
		ledgerVersions = append(ledgerVersions, v)
	}
	if err := rows.Err(); err != nil {
		s.fail("read migration ledger", err)
	}
	if len(ledgerVersions) == 0 {
		t.Fatal("empty migration ledger after MigrateSchema")
	}
	for i, v := range ledgerVersions {
		if v != int64(i+1) {
			t.Fatalf("migration ledger is not contiguous from 1: %v", ledgerVersions)
		}
	}
	if ledgerVersions[len(ledgerVersions)-1] < 9 {
		t.Fatalf("the pg-catalog-05 contribution migration (version 9) is not applied: %v", ledgerVersions)
	}
}

func TestZZZeroLeftoverSemanticInventoryPageFixtures(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for the fixture audit: %s", err)
	}
	defer admin.Close()

	pgcat06OwnedMu.Lock()
	owned := make([]string, 0, len(pgcat06OwnedByName))
	for db := range pgcat06OwnedByName {
		owned = append(owned, db)
	}
	pgcat06OwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("no fixture databases were created by this run")
	}
	for _, db := range owned {
		var count int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Fatalf("audit owned database %q: %v", db, err)
		}
		if count != 0 {
			t.Errorf("owned database %q survived the run", db)
		}
	}
	var leftoverDBs int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_database WHERE datname LIKE 'verify\_cat06\_%'`).Scan(&leftoverDBs); err != nil {
		t.Fatalf("audit fixture prefix: %v", err)
	}
	if leftoverDBs != 0 {
		t.Errorf("%d databases still match the private verify_cat06_ fixture prefix", leftoverDBs)
	}
	var leftoverSchemas int
	if err := admin.QueryRow(ctx,
		`SELECT count(*) FROM pg_namespace WHERE nspname LIKE 'verify\_cat06\_%'`).Scan(&leftoverSchemas); err != nil {
		t.Fatalf("audit fixture schemas: %v", err)
	}
	if leftoverSchemas != 0 {
		t.Errorf("%d schemas still match the private verify_cat06_ fixture prefix", leftoverSchemas)
	}
}

func TestSemanticInventoryPageAggregationHonorsResourceTypeFilter(t *testing.T) {
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	put := func(sourceID, resourceType, binding, concept string, observation catalog.SemanticObservation) {
		s.seedContribution(t, contributionSeed{
			Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
			SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
			ResourceType: resourceType, SourceID: sourceID, Ordinal: 0,
			ConceptID: concept, BindingID: binding,
			Observation: pgcat06ObsJSON(t, observation),
		})
	}
	put("rt-1", "Observation", "bnd-rt", "cpt-rt", pgcat06Observation(1))
	put("rt-2", "Encounter", "bnd-rt", "cpt-rt", pgcat06Observation(99))
	fam := func(population int64) catalog.SemanticObservation {
		o := pgcat06FamilyObs("code.coding[]", "urn:rt-family", "v1", population)
		return o
	}
	put("rt-3", "Observation", "bnd-frt", "cpt-frt-a", fam(1))
	put("rt-4", "Encounter", "bnd-frt", "cpt-frt-b", fam(99))

	repo := s.readRepository(t)
	base := catalog.SemanticInventoryPageOptions{
		Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
		AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
		ResourceType:                  "Observation",
	}
	for _, tc := range []struct {
		label   string
		binding string
		family  bool
	}{{"exact", "bnd-rt", false}, {"family", "bnd-frt", true}} {
		filtered := base
		filtered.BindingID = tc.binding
		if tc.family {
			filtered.SelectionMode = catalog.SemanticInventorySystemVersionCategory
		}
		page, err := repo.PageSemanticInventory(s.ctx, filtered)
		if err != nil {
			s.fail("filtered page "+tc.label, err)
		}
		if tc.family {
			if len(page.Families) != 1 || page.Families[0].Population != 1 || len(page.Entries) != 0 {
				t.Fatalf("filtered %s page = entries %+v families %+v, want one family with population 1",
					tc.label, page.Entries, page.Families)
			}
		} else if len(page.Entries) != 1 || page.Entries[0].ConceptID != "cpt-rt" ||
			page.Entries[0].Observation.Population != 1 || len(page.Families) != 0 {
			t.Fatalf("filtered %s page = %+v, want cpt-rt alone with population 1 (Encounter-column sibling excluded)",
				tc.label, page.Entries)
		}
		unfiltered := filtered
		unfiltered.ResourceType = ""
		page, err = repo.PageSemanticInventory(s.ctx, unfiltered)
		if err != nil {
			s.fail("unfiltered page "+tc.label, err)
		}
		if tc.family {
			if len(page.Families) != 1 || page.Families[0].Population != 100 {
				t.Fatalf("unfiltered %s page = %+v, want one family with population 100", tc.label, page.Families)
			}
		} else if len(page.Entries) != 1 || page.Entries[0].Observation.Population != 100 {
			t.Fatalf("unfiltered %s page = %+v, want population 100 (sibling folds in)", tc.label, page.Entries)
		}
	}
}

func TestSemanticInventoryPageQueryFoldingGolden(t *testing.T) {
	s := newPgcat06Service(t)
	s.seedBuild(t, pgcat06DefaultBuildSeed(pgcat06Project, pgcat06Generation))
	buildID := catalog.SemanticInventoryBuildID(pgcat06Project, pgcat06Generation)
	observation := catalog.SemanticObservation{
		SchemaVersion: catalog.SemanticObservationSchemaVersion,
		Source: catalog.SemanticObservationSource{
			Type: "Observation", Path: "alpha beta",
		},
		Key:          catalog.SemanticObservationKey{System: "gamma delta", Code: "code-a"},
		Value:        catalog.SemanticObservationValue{Type: "string"},
		Completeness: catalog.SemanticComplete,
		Status:       "SUPPORTED",
		Population:   1,
	}
	s.seedContribution(t, contributionSeed{
		Project: pgcat06Project, Generation: pgcat06Generation, BuildID: buildID,
		SourceKind: catalog.SemanticInventorySourceFile, AuthResourcePath: pgcat06PathGlucose,
		ResourceType: "Observation", SourceID: "fold-1", Ordinal: 0,
		ConceptID: "cpt-fold", BindingID: "bnd-fold",
		Observation: pgcat06ObsJSON(t, observation),
	})
	repo := s.readRepository(t)
	search := func(query string) int {
		t.Helper()
		page, err := repo.PageSemanticInventory(s.ctx, catalog.SemanticInventoryPageOptions{
			Project: pgcat06Project, DatasetGeneration: pgcat06Generation,
			AuthResourcePathsUnrestricted: pgcat06Unrestricted(true),
			Query:                         query,
		})
		if err != nil {
			s.fail("search page", err)
		}
		return len(page.Entries)
	}
	if got := search("beta gamma"); got != 1 {
		t.Fatalf("phrase spanning absent leaves matched %d entries, want 1", got)
	}
	if got := search("beta zeta"); got != 0 {
		t.Fatalf("non-matching phrase matched %d entries, want 0 (negative control)", got)
	}
	if got := search("alpha beta gamma delta code-a"); got != 1 {
		t.Fatalf("full-span phrase matched %d entries, want 1", got)
	}
}

func pgcat05EmitContribution(t *testing.T, project, generation, authPath, resourceType, sourceID, payload string) []catalog.SemanticInventoryContribution {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("decode golden payload: %v", err)
	}
	emitter := catalog.NewSemanticInventoryEmitter(project, generation)
	var out []catalog.SemanticInventoryContribution
	if err := emitter.ObservePayload(decoded, resourceType, authPath, sourceID, func(c catalog.SemanticInventoryContribution) {
		out = append(out, c)
	}); err != nil {
		t.Fatalf("emit contributions: %v", err)
	}
	return out
}
