package postgres_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	storepostgres "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var pgcat05NamePattern = regexp.MustCompile(`^verify_pgcat05_it_[0-9]+_[0-9a-f]+$`)

var (
	pgcat05OwnedMu     sync.Mutex
	pgcat05OwnedByName = map[string]string{}
)

func pgcat05Register(db, schema string) {
	pgcat05OwnedMu.Lock()
	defer pgcat05OwnedMu.Unlock()
	pgcat05OwnedByName[db] = schema
}

type pgcat05Service struct {
	ctx    context.Context
	admin  *pgxpool.Pool
	iso    *pgxpool.Pool
	dsn    string
	schema string
	scrub  func(any) string
	fail   func(step string, err error)
}

func pgcat05TestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL 18 integration tests")
	}
	return raw
}

func pgcat05RandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

func newPgcat05Service(t *testing.T) *pgcat05Service {
	t.Helper()
	raw := pgcat05TestPostgresURL(t)

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

	dbName = fmt.Sprintf("verify_pgcat05_it_%d_%s", time.Now().UnixNano(), pgcat05RandomHex(t))
	if !pgcat05NamePattern.MatchString(dbName) {
		t.Fatalf("isolated database name %q does not match the required pattern", dbName)
	}
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	pgcat05Register(dbName, "")
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

	schema = fmt.Sprintf("verify_pgcat05_it_%d_%s", time.Now().UnixNano(), pgcat05RandomHex(t))
	if !pgcat05NamePattern.MatchString(schema) {
		t.Fatalf("owned schema name %q does not match the required pattern", schema)
	}
	if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		fail("create owned schema", err)
	}
	pgcat05Register(dbName, schema)
	t.Cleanup(func() {
		if _, err := iso.Exec(context.WithoutCancel(ctx),
			`DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Errorf("drop owned schema: %s", scrub(err))
		}
	})

	service := &pgcat05Service{ctx: ctx, admin: admin, iso: iso, dsn: dsn, schema: schema, scrub: scrub, fail: fail}
	if _, err := storepostgres.MigrateSchema(ctx, service.openClient(t), schema); err != nil {
		fail("MigrateSchema", err)
	}
	return service
}

func (s *pgcat05Service) openClient(t *testing.T) *storepostgres.Client {
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

func (s *pgcat05Service) openDedicatedClient(t *testing.T, label string) (*storepostgres.Client, int32) {
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
	conn, err := client.Acquire(s.ctx)
	if err != nil {
		s.fail("acquire dedicated connection "+label, err)
	}
	defer conn.Release()
	var pid int32
	if err := conn.QueryRow(s.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		s.fail("witness pg_backend_pid "+label, err)
	}
	if pid <= 0 {
		t.Fatalf("pg_backend_pid() for %s returned %d, want a live backend", label, pid)
	}
	return client, pid
}

func (s *pgcat05Service) contributionRepository(t *testing.T) *catalogstore.SemanticContributionRepository {
	t.Helper()
	repo, err := catalogstore.NewSemanticContributionRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new semantic contribution repository", err)
	}
	return repo
}

func (s *pgcat05Service) buildRepository(t *testing.T) *catalogstore.SemanticBuildRepository {
	t.Helper()
	repo, err := catalogstore.NewSemanticBuildRepository(s.openClient(t), s.schema)
	if err != nil {
		s.fail("new semantic build repository", err)
	}
	return repo
}

func (s *pgcat05Service) backfillStore(t *testing.T) *catalogstore.SemanticInventoryBackfillStore {
	t.Helper()
	store, err := catalogstore.NewSemanticInventoryBackfillStore(s.contributionRepository(t), s.buildRepository(t))
	if err != nil {
		s.fail("new semantic inventory backfill store", err)
	}
	return store
}

func (s *pgcat05Service) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := s.iso.Exec(s.ctx, sql, args...); err != nil {
		s.fail("fixture exec", err)
	}
}

func (s *pgcat05Service) contributionFQN() string {
	return pgx.Identifier{s.schema, "fhir_semantic_inventory_contribution"}.Sanitize()
}

func (s *pgcat05Service) resourceFQN() string {
	return pgx.Identifier{s.schema, "fhir_resource"}.Sanitize()
}

func (s *pgcat05Service) buildFQN() string {
	return pgx.Identifier{s.schema, "fhir_semantic_inventory_build"}.Sanitize()
}

func (s *pgcat05Service) fieldProfileFQN() string {
	return pgx.Identifier{s.schema, "fhir_field_profile"}.Sanitize()
}

func (s *pgcat05Service) coverageFQN() string {
	return pgx.Identifier{s.schema, "fhir_field_profile_coverage"}.Sanitize()
}

func (s *pgcat05Service) ledgerFQN() string {
	return pgx.Identifier{s.schema, storepostgres.LedgerTable}.Sanitize()
}

type contributionLiteral struct {
	Key               string
	Project           string
	DatasetGeneration string
	BuildID           string
	SourceKind        string
	AuthResourcePath  string
	ResourceType      string
	SourceID          string
	Ordinal           int
	ConceptID         string
	BindingID         string
	Example           string
	PayloadDigest     string
	Observation       string
}

type contributionWitness struct {
	contributionLiteral
	RecordedAt time.Time
}

func pgcat05CanonicalJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}

func (l contributionLiteral) normalized() contributionLiteral {
	l.Observation = pgcat05CanonicalJSON(l.Observation)
	return l
}

func (w contributionWitness) normalized() contributionWitness {
	w.Observation = pgcat05CanonicalJSON(w.Observation)
	return w
}

func sameContributionLiteral(a, b contributionLiteral) bool {
	return a.normalized() == b.normalized()
}

func (w contributionWitness) sameAs(o contributionWitness) bool {
	return fmt.Sprint(w.normalized()) == fmt.Sprint(o.normalized())
}

const contributionWitnessColumns = "contribution_key, project, dataset_generation, build_id, source_kind," +
	" auth_resource_path, resource_type, source_id, ordinal, concept_id, binding_id, example," +
	" payload_digest, observation::text, recorded_at"

func (s *pgcat05Service) witnessContribution(t *testing.T, key string) (contributionWitness, bool) {
	t.Helper()
	var w contributionWitness
	err := s.iso.QueryRow(s.ctx, "SELECT "+contributionWitnessColumns+" FROM "+s.contributionFQN()+" WHERE contribution_key = $1", key).Scan(
		&w.Key, &w.Project, &w.DatasetGeneration, &w.BuildID, &w.SourceKind,
		&w.AuthResourcePath, &w.ResourceType, &w.SourceID, &w.Ordinal, &w.ConceptID,
		&w.BindingID, &w.Example, &w.PayloadDigest, &w.Observation, &w.RecordedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return contributionWitness{}, false
	}
	if err != nil {
		s.fail("witness contribution row", err)
	}
	return w, true
}

func (s *pgcat05Service) requireNoContribution(t *testing.T, key, what string) {
	t.Helper()
	if row, found := s.witnessContribution(t, key); found {
		t.Fatalf("%s: unexpected row %+v", what, row)
	}
}

func (s *pgcat05Service) countContributions(t *testing.T) int64 {
	t.Helper()
	var count int64
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.contributionFQN()).Scan(&count); err != nil {
		s.fail("count contributions", err)
	}
	return count
}

func (s *pgcat05Service) contributionSeedSQL() string {
	return "INSERT INTO " + s.contributionFQN() +
		" (contribution_key, project, dataset_generation, build_id, source_kind," +
		" auth_resource_path, resource_type, source_id, ordinal, concept_id, binding_id," +
		" example, payload_digest, observation)" +
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::jsonb)"
}

func (s *pgcat05Service) seedContributionLiteral(t *testing.T, l contributionLiteral) {
	t.Helper()
	s.exec(t, s.contributionSeedSQL(), l.Key, l.Project, l.DatasetGeneration, l.BuildID, l.SourceKind,
		l.AuthResourcePath, l.ResourceType, l.SourceID, l.Ordinal, l.ConceptID, l.BindingID,
		l.Example, l.PayloadDigest, l.Observation)
}

func (s *pgcat05Service) trySeedContributionLiteral(t *testing.T, l contributionLiteral) error {
	t.Helper()
	_, err := s.iso.Exec(s.ctx, s.contributionSeedSQL(), l.Key, l.Project, l.DatasetGeneration, l.BuildID,
		l.SourceKind, l.AuthResourcePath, l.ResourceType, l.SourceID, l.Ordinal, l.ConceptID,
		l.BindingID, l.Example, l.PayloadDigest, l.Observation)
	return err
}

func (l contributionLiteral) toWitness(recordedAt time.Time) contributionWitness {
	return contributionWitness{contributionLiteral: l, RecordedAt: recordedAt}
}

func (s *pgcat05Service) seedRetainedResource(t *testing.T, project, generation, resourceType, logicalID, authPath string, payload []byte) {
	t.Helper()
	var payloadValue any
	if payload != nil {
		payloadValue = []byte(payload)
	}
	s.exec(t, "INSERT INTO "+s.resourceFQN()+
		" (project, dataset_generation, resource_type, logical_id, auth_resource_path, payload)"+
		" VALUES ($1, $2, $3, $4, $5, $6::jsonb)",
		project, generation, resourceType, logicalID, authPath, payloadValue)
}

func (s *pgcat05Service) seedFieldProfileDoc(t *testing.T, project, generation, authPath, resourceType, path string, docCount int64) {
	t.Helper()
	s.exec(t, "INSERT INTO "+s.fieldProfileFQN()+
		" (project, dataset_generation, auth_resource_path, resource_type, path, profile_key,"+
		" record_type, kind, doc_count, max_items, sample_count, distinct_truncated,"+
		" enrichment_truncated, pivot_candidate, pivot_kind, pivot_family,"+
		" pivot_column_selector, pivot_value_selector, pivot_item_source, pivot_item_resource_type)"+
		" VALUES ($1, $2, $3, $4, $5, $6, '', $7, $8, 0, 0, false, false, false, '', '', '', '', '', '')",
		project, generation, authPath, resourceType, path, "fpk_"+resourceType+"_"+path, "string", docCount)
}

func (s *pgcat05Service) seedCoverageMarker(t *testing.T, project, generation, authPath, resourceType string) {
	t.Helper()
	s.exec(t, "INSERT INTO "+s.coverageFQN()+
		" (project, dataset_generation, auth_resource_path, resource_type, enrichment_truncated)"+
		" VALUES ($1, $2, $3, $4, false)",
		project, generation, authPath, resourceType)
}

func (s *pgcat05Service) witnessBuildRow(t *testing.T, project, generation, buildID string) (catalog.SemanticInventoryBuild, bool) {
	t.Helper()
	repo := s.buildRepository(t)
	build, found, err := repo.ReadSemanticInventoryBuild(s.ctx, project, generation, buildID)
	if err != nil {
		t.Fatalf("witness build row %s/%s/%s: %v", project, generation, buildID, err)
	}
	return build, found
}

const (
	pgcat05GoldenProject    = "pgcat05-golden"
	pgcat05GoldenGeneration = "gen-a"
)

type goldenContribution struct {
	key          string
	conceptID    string
	bindingID    string
	example      string
	digest       string
	sourceID     string
	authPath     string
	resourceType string
	observation  string
	payload      string
}

var pgcat05ObservationCodeObs = `{"schema_version":4,"source":{"canonical":"Observation.code.coding[]","type":"Observation","path":"code.coding[]"},"key":{"selector":"code.coding[]","system":"urn:obs-05","version":"v1","code":"loinc-42"},"value":{"selector":"code.coding[].display","type":"string","fallback_selectors":["code.coding[].code"],"fallback_on_blank_string":true},"logical_type":"string","completeness":"complete","status":"SUPPORTED","population":1,"examples":["loinc-42"],"examples_truncated":false,"rule_hint":"CODED_CONCEPT_V1","rule_version":"5"}`

var pgcat05Goldens = map[string]goldenContribution{
	"obs-a": {
		key:          "800ae5168dd3f11d3ef7dc957d4f89cd572e225812e699a0feb088d8de0c480c",
		conceptID:    "ec572479ea77eabd0c8014f33810f458e9f009a27adb8fd77fb9fab3121ad62f",
		bindingID:    "b498c06c58e286f4cc9ceb74942c8a7872ad49b0220e65f0ea754fd936f4a53d",
		example:      "loinc-42",
		digest:       "e95374423254d82ba79ee0cc3305e952f34539b36af2a159feccf2281653ac17",
		sourceID:     "retained:Observation/obs-a",
		authPath:     "fhir/Observation?code=urn:obs-05|loinc-42",
		resourceType: "Observation",
		observation:  pgcat05ObservationCodeObs,
		payload:      `{"resourceType":"Observation","code":{"coding":[{"system":"urn:obs-05","version":"v1","code":"loinc-42"}]}}`,
	},
	"obs-b": {
		key:          "52536e2cb03cb6b0ffbd00fd2b519bc540d185317d400dfc9ed033c1991f3327",
		conceptID:    "ec572479ea77eabd0c8014f33810f458e9f009a27adb8fd77fb9fab3121ad62f",
		bindingID:    "b498c06c58e286f4cc9ceb74942c8a7872ad49b0220e65f0ea754fd936f4a53d",
		example:      "loinc-42",
		digest:       "11f404e19e6be133fc6212b99b2717f43c01a835eab01ca67580ed39aace094e",
		sourceID:     "retained:Observation/obs-b",
		authPath:     "",
		resourceType: "Observation",
		observation:  pgcat05ObservationCodeObs,
		payload:      `{"resourceType":"Observation","code":{"coding":[{"system":"urn:obs-05","version":"v1","code":"loinc-42"}]}}`,
	},
	"obs-c": {
		key:          "443978452a52b2b02b0d11dd9910114eae49143defaaf522312bc8c95a4c0626",
		conceptID:    "974b766eafbd1eb88624d676ed271537f253d149dc4afb1ae4dbc0c9e2a8f5d7",
		bindingID:    "699570acfab3e420beae8c57bd32d81f1fca5c4b62482f28a64a62f3ff8457af",
		example:      "3.5",
		digest:       "6e5581a2e45fa58099a69662429eb8ac618d515c8b583d0b141815d5b32d0f96",
		sourceID:     "retained:Observation/obs-c",
		authPath:     "",
		resourceType: "Observation",
		observation:  `{"schema_version":4,"source":{"canonical":"Observation.code","type":"Observation","path":"code"},"key":{"selector":"code.coding[]","system":"urn:obs-05","version":"v2","code":"loinc-99"},"value":{"selector":"valueQuantity.value","type":"decimal"},"choice_arm":"valueQuantity","logical_type":"decimal","observed_units":["kg"],"completeness":"complete","status":"SUPPORTED","population":1,"examples":["3.5"],"examples_truncated":false,"rule_hint":"CODED_VALUE_V1","rule_version":"5"}`,
		payload:      `{"resourceType":"Observation","code":{"coding":[{"system":"urn:obs-05","version":"v2","code":"loinc-99"}]},"valueQuantity":{"value":3.5,"unit":"kg"}}`,
	},
	"obs-d": {
		key:          "d75c5c4b985d9f6a8c01fe007901cca521c088cb8869a313e16e098e6acd74b8",
		conceptID:    "a0987c1620992c6d9df2adba885c019209707cbcd73145837b732ae4b4c884ac",
		bindingID:    "b498c06c58e286f4cc9ceb74942c8a7872ad49b0220e65f0ea754fd936f4a53d",
		example:      "loinc-77",
		digest:       "caf01406cc621e22e085d7cb7795e0e63a10b0a867fc834b5302c26d992b4a28",
		sourceID:     "retained:Observation/obs-d",
		authPath:     "fhir/Encounter?type=visit",
		resourceType: "Observation",
		observation:  `{"schema_version":4,"source":{"canonical":"Observation.code.coding[]","type":"Observation","path":"code.coding[]"},"key":{"selector":"code.coding[]","system":"urn:obs-05","version":"v3","code":"loinc-77"},"value":{"selector":"code.coding[].display","type":"string","fallback_selectors":["code.coding[].code"],"fallback_on_blank_string":true},"logical_type":"string","completeness":"complete","status":"SUPPORTED","population":1,"examples":["loinc-77"],"examples_truncated":false,"rule_hint":"CODED_CONCEPT_V1","rule_version":"5"}`,
		payload:      `{"resourceType":"Observation","code":{"coding":[{"system":"urn:obs-05","version":"v3","code":"loinc-77"}]}}`,
	},
	"obs-e": {
		key:          "7f44d967925c2ff0929492f44522c90119de8532470a8c2336ab5aade9333ef6",
		conceptID:    "ec572479ea77eabd0c8014f33810f458e9f009a27adb8fd77fb9fab3121ad62f",
		bindingID:    "b498c06c58e286f4cc9ceb74942c8a7872ad49b0220e65f0ea754fd936f4a53d",
		example:      "loinc-42",
		digest:       "3e3d2c7e97641a0224a6dcf7032aaf32e94cdd1d5a8b207d264ddefcc810366f",
		sourceID:     "retained:Observation/obs-e",
		authPath:     "",
		resourceType: "Observation",
		observation:  pgcat05ObservationCodeObs,
		payload:      `{"resourceType":"Observation","code":{"coding":[{"system":"urn:obs-05","version":"v1","code":"loinc-42"}]}}`,
	},
	"obs-f": {
		key:          "33b6804e9386d95119b04eea4767ad781e92106f5307b48600c376775b4975a2",
		conceptID:    "8c5fa05f46d6a6a401ac7ce93c06dcdb2a209234d181213ec78156e0455bbd37",
		bindingID:    "699570acfab3e420beae8c57bd32d81f1fca5c4b62482f28a64a62f3ff8457af",
		example:      "7",
		digest:       "a4b9c92c27b80c4ac1982436e9448f4171e080ed047b64cacebc9959c8f09b85",
		sourceID:     "retained:Observation/obs-f",
		authPath:     "",
		resourceType: "Observation",
		observation:  `{"schema_version":4,"source":{"canonical":"Observation.code","type":"Observation","path":"code"},"key":{"selector":"code.coding[]","system":"urn:obs-05","version":"v9","code":"loinc-12345"},"value":{"selector":"valueQuantity.value","type":"decimal"},"choice_arm":"valueQuantity","logical_type":"decimal","observed_units":["mg/dL"],"completeness":"complete","status":"SUPPORTED","population":1,"examples":["7"],"examples_truncated":false,"rule_hint":"CODED_VALUE_V1","rule_version":"5"}`,
		payload:      `{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"urn:obs-05","version":"v9","code":"loinc-12345"}]},"valueQuantity":{"value":7,"unit":"mg/dL"}}`,
	},
}

func pgcat05GoldenIDs() []string {
	ids := make([]string, 0, len(pgcat05Goldens))
	for id := range pgcat05Goldens {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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

func pgcat05GoldensBatch(t *testing.T, ids []string) []catalog.SemanticInventoryContribution {
	t.Helper()
	var batch []catalog.SemanticInventoryContribution
	for _, id := range ids {
		g := pgcat05Goldens[id]
		emitted := pgcat05EmitContribution(t, pgcat05GoldenProject, pgcat05GoldenGeneration,
			g.authPath, g.resourceType, g.sourceID, g.payload)
		if len(emitted) != 1 {
			t.Fatalf("golden %s: emitter produced %d contributions, want exactly 1", id, len(emitted))
		}
		batch = append(batch, emitted[0])
	}
	return batch
}

func pgcat05ExpectedLiteral(g goldenContribution) contributionLiteral {
	return contributionLiteral{
		Key: g.key, Project: pgcat05GoldenProject, DatasetGeneration: pgcat05GoldenGeneration,
		BuildID:          catalog.SemanticInventoryBuildID(pgcat05GoldenProject, pgcat05GoldenGeneration),
		SourceKind:       catalog.SemanticInventorySourceRetained,
		AuthResourcePath: g.authPath, ResourceType: g.resourceType, SourceID: g.sourceID,
		Ordinal: 0, ConceptID: g.conceptID, BindingID: g.bindingID, Example: g.example,
		PayloadDigest: g.digest, Observation: g.observation,
	}
}

func TestSemanticContributionEmitterGoldenAnchor(t *testing.T) {
	for _, id := range pgcat05GoldenIDs() {
		g := pgcat05Goldens[id]
		emitted := pgcat05EmitContribution(t, pgcat05GoldenProject, pgcat05GoldenGeneration,
			g.authPath, g.resourceType, g.sourceID, g.payload)
		if len(emitted) != 1 {
			t.Fatalf("%s: emitter produced %d contributions, want exactly 1", id, len(emitted))
		}
		c := emitted[0]
		if c.Key != g.key {
			t.Fatalf("%s: contribution key = %s, golden %s", id, c.Key, g.key)
		}
		if c.ConceptID != g.conceptID || c.BindingID != g.bindingID || c.Example != g.example {
			t.Fatalf("%s: concept/binding/example = %s/%s/%q, golden %s/%s/%q",
				id, c.ConceptID, c.BindingID, c.Example, g.conceptID, g.bindingID, g.example)
		}
		observation, err := json.Marshal(c.Observation)
		if err != nil {
			t.Fatalf("%s: encode observation: %v", id, err)
		}
		var goldenObs catalog.SemanticObservation
		if err := json.Unmarshal([]byte(g.observation), &goldenObs); err != nil {
			t.Fatalf("%s: decode golden observation: %v", id, err)
		}
		if !reflect.DeepEqual(c.Observation, goldenObs) {
			t.Fatalf("%s: observation %#v differs from golden %#v", id, c.Observation, goldenObs)
		}
		if digest := semanticContributionPayloadDigest(c, observation); digest != g.digest {
			t.Fatalf("%s: payload digest = %s, golden %s", id, digest, g.digest)
		}
	}
}

func TestSemanticContributionWriteRoundTripsEveryColumn(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	ids := pgcat05GoldenIDs()
	if err := repo.WriteSemanticInventoryContributions(s.ctx, pgcat05GoldensBatch(t, ids), 0); err != nil {
		s.fail("write golden batch", err)
	}
	if got := s.countContributions(t); got != int64(len(ids)) {
		t.Fatalf("contribution count = %d, want %d", got, len(ids))
	}
	for _, id := range ids {
		g := pgcat05Goldens[id]
		w, found := s.witnessContribution(t, g.key)
		if !found {
			t.Fatalf("%s: contribution %s missing after write", id, g.key)
		}
		want := pgcat05ExpectedLiteral(g)
		if !sameContributionLiteral(w.contributionLiteral, want) {
			t.Fatalf("%s: stored row = %+v, want %+v", id, w.contributionLiteral, want)
		}
		var equal bool
		if err := s.iso.QueryRow(s.ctx, "SELECT observation = $1::jsonb FROM "+s.contributionFQN()+
			" WHERE contribution_key = $2", g.observation, g.key).Scan(&equal); err != nil {
			s.fail("witness observation jsonb equality", err)
		}
		if !equal {
			t.Fatalf("%s: stored observation is not jsonb-equal to the golden literal: %s", id, w.Observation)
		}
		if w.RecordedAt.IsZero() {
			t.Fatalf("%s: recorded_at is zero", id)
		}
	}
}

func TestSemanticContributionReplayIsIdempotentByteIdentical(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	ids := pgcat05GoldenIDs()
	batch := pgcat05GoldensBatch(t, ids)
	if err := repo.WriteSemanticInventoryContributions(s.ctx, batch, 2); err != nil {
		s.fail("initial write", err)
	}
	before := map[string]contributionWitness{}
	for _, id := range ids {
		w, found := s.witnessContribution(t, pgcat05Goldens[id].key)
		if !found {
			t.Fatalf("missing row after initial write: %s", id)
		}
		before[id] = w
	}

	if err := repo.WriteSemanticInventoryContributions(s.ctx, pgcat05GoldensBatch(t, ids), 0); err != nil {
		s.fail("replay write", err)
	}
	if got := s.countContributions(t); got != int64(len(ids)) {
		t.Fatalf("count after replay = %d, want %d (replay must not duplicate)", got, len(ids))
	}
	for _, id := range ids {
		after, found := s.witnessContribution(t, pgcat05Goldens[id].key)
		if !found {
			t.Fatalf("row vanished after replay: %s", id)
		}
		if !after.sameAs(before[id]) {
			t.Fatalf("%s: replay mutated the row:\n before %+v\n after  %+v", id, before[id], after)
		}
	}
}

func TestSemanticContributionConflictFailsWithoutOverwrite(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	if err := repo.WriteSemanticInventoryContributions(s.ctx, pgcat05GoldensBatch(t, []string{"obs-a"}), 0); err != nil {
		s.fail("initial write", err)
	}
	before, found := s.witnessContribution(t, pgcat05Goldens["obs-a"].key)
	if !found {
		t.Fatal("seed row missing")
	}

	tampered := pgcat05GoldensBatch(t, []string{"obs-a"})[0]
	tampered.Example = "tampered-example"
	tampered.Observation.Examples = []string{"tampered-example"}

	err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{tampered}, 0)
	var conflict *catalogstore.SemanticInventoryContributionConflictError
	if !errors.As(err, &conflict) || !errors.Is(err, catalogstore.ErrSemanticInventoryContributionConflict) {
		t.Fatalf("conflicting write error = %v, want *SemanticInventoryContributionConflictError", err)
	}
	if conflict.ContributionKey != pgcat05Goldens["obs-a"].key || conflict.Slot {
		t.Fatalf("conflict misattributed: %+v", conflict)
	}
	if conflict.StoredDigest != pgcat05Goldens["obs-a"].digest {
		t.Fatalf("conflict names stored digest %s, want golden %s", conflict.StoredDigest, pgcat05Goldens["obs-a"].digest)
	}
	if conflict.ProvidedDigest == conflict.StoredDigest || !isLowerHex64(conflict.ProvidedDigest) {
		t.Fatalf("conflict provided digest = %q, want a differing 64-hex digest", conflict.ProvidedDigest)
	}
	for _, other := range []error{catalogstore.ErrInvalidSemanticInventoryContribution, catalogstore.ErrSemanticInventoryEntriesUnsupported, catalogstore.ErrRetainedSemanticSourceMissing} {
		if errors.Is(err, other) {
			t.Fatalf("conflict error misclassifies as %v", other)
		}
	}

	after, found := s.witnessContribution(t, pgcat05Goldens["obs-a"].key)
	if !found {
		t.Fatal("conflicting write removed the stored row")
	}
	if !after.sameAs(before) {
		t.Fatalf("conflicting payload overwrote the stored row:\n before %+v\n after  %+v", before, after)
	}
	if got := s.countContributions(t); got != 1 {
		t.Fatalf("count after refused conflict = %d, want 1", got)
	}
}

func TestSemanticContributionSlotConflictRefusedWithoutOverwrite(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	golden := pgcat05Goldens["obs-a"]
	s.seedContributionLiteral(t, pgcat05ExpectedLiteral(golden))

	claimed := pgcat05GoldensBatch(t, []string{"obs-a"})[0]
	claimed.AuthResourcePath = "fhir/Observation?code=urn:other"
	claimed.Key = strings.Repeat("7", 64)
	claimed.Example = golden.example

	err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{claimed}, 0)
	var conflict *catalogstore.SemanticInventoryContributionConflictError
	if !errors.As(err, &conflict) || !conflict.Slot {
		t.Fatalf("slot conflict error = %v, want a slot-classified conflict", err)
	}
	if conflict.SourceID != golden.sourceID || conflict.Ordinal != 0 {
		t.Fatalf("slot conflict misattributed: %+v", conflict)
	}
	s.requireNoContribution(t, claimed.Key, "slot-conflicting row")
	after, found := s.witnessContribution(t, golden.key)
	if !found || !after.sameAs(pgcat05ExpectedLiteral(golden).toWitness(after.RecordedAt)) {
		t.Fatalf("slot conflict disturbed the stored row: %+v found=%t", after, found)
	}
	if !sameContributionLiteral(after.contributionLiteral, pgcat05ExpectedLiteral(golden)) {
		t.Fatalf("slot conflict mutated stored columns: %+v", after)
	}
}

func TestSemanticContributionConstraintNegatives(t *testing.T) {
	s := newPgcat05Service(t)

	base := func(suffix string) contributionLiteral {
		g := pgcat05Goldens["obs-b"]
		l := pgcat05ExpectedLiteral(g)
		l.Key = fmt.Sprintf("%x", sha256.Sum256([]byte("pgcat05-base/"+suffix)))
		l.SourceID = "retained:Observation/neg-" + suffix
		l.PayloadDigest = fmt.Sprintf("%064x", len(suffix)+0xbeef0000)
		return l
	}
	const (
		checkViolation   = "23514"
		uniqueViolation  = "23505"
		notNullViolation = "23502"
	)
	cases := []struct {
		name           string
		mutate         func(*contributionLiteral)
		wantCode       string
		wantConstraint string
	}{
		{"malformed contribution_key", func(l *contributionLiteral) { l.Key = "key_short" }, checkViolation, "fhir_semantic_inventory_contribution_key_shape"},
		{"uppercase contribution_key", func(l *contributionLiteral) { l.Key = strings.ToUpper(l.Key[:32]) + l.Key[32:] }, checkViolation, "fhir_semantic_inventory_contribution_key_shape"},
		{"malformed payload_digest", func(l *contributionLiteral) { l.PayloadDigest = strings.Repeat("z", 64) }, checkViolation, "fhir_semantic_inventory_contribution_digest_shape"},
		{"malformed build_id", func(l *contributionLiteral) { l.BuildID = strings.Repeat("g", 64) }, checkViolation, "fhir_semantic_inventory_contribution_build_shape"},
		{"blank build_id", func(l *contributionLiteral) { l.BuildID = "" }, checkViolation, "fhir_semantic_inventory_contribution_build_present"},
		{"blank project", func(l *contributionLiteral) { l.Project = "" }, checkViolation, "fhir_semantic_inventory_contribution_project_present"},
		{"blank resource_type", func(l *contributionLiteral) { l.ResourceType = "" }, checkViolation, "fhir_semantic_inventory_contribution_resource_type_present"},
		{"blank source_id", func(l *contributionLiteral) { l.SourceID = "" }, checkViolation, "fhir_semantic_inventory_contribution_source_present"},
		{"blank concept_id", func(l *contributionLiteral) { l.ConceptID = "" }, checkViolation, "fhir_semantic_inventory_contribution_concept_present"},
		{"blank binding_id", func(l *contributionLiteral) { l.BindingID = "" }, checkViolation, "fhir_semantic_inventory_contribution_binding_present"},
		{"negative ordinal", func(l *contributionLiteral) { l.Ordinal = -1 }, checkViolation, "fhir_semantic_inventory_contribution_ordinal_nonnegative"},
		{"unknown source kind", func(l *contributionLiteral) { l.SourceKind = "stream" }, checkViolation, "fhir_semantic_inventory_contribution_source_kind_known"},
		{"observation is a json array", func(l *contributionLiteral) { l.Observation = `[{"a":1}]` }, checkViolation, "fhir_semantic_inventory_contribution_observation_object"},
		{"observation is a json scalar", func(l *contributionLiteral) { l.Observation = `"observation"` }, checkViolation, "fhir_semantic_inventory_contribution_observation_object"},
	}
	for i, tc := range cases {
		bad := base(fmt.Sprintf("neg%d", i))
		tc.mutate(&bad)
		control := base(fmt.Sprintf("ctrl%d", i))
		s.seedContributionLiteral(t, control)
		if _, found := s.witnessContribution(t, control.Key); !found {
			t.Fatalf("%s: control row did not insert — the harness is not proving anything", tc.name)
		}
		err := s.trySeedContributionLiteral(t, bad)
		var pgErr *pgconn.PgError
		if err == nil {
			t.Fatalf("%s: violating row was ACCEPTED — constraint missing or renamed", tc.name)
		}
		if !errors.As(err, &pgErr) {
			t.Fatalf("%s: violating row failed with a non-database error: %v", tc.name, err)
		}
		if pgErr.Code != tc.wantCode || pgErr.ConstraintName != tc.wantConstraint {
			t.Fatalf("%s: rejected by code=%s constraint=%s, want code=%s constraint=%s",
				tc.name, pgErr.Code, pgErr.ConstraintName, tc.wantCode, tc.wantConstraint)
		}
		s.requireNoContribution(t, bad.Key, tc.name)
	}

	notNull := base("null-obs")
	_, err := s.iso.Exec(s.ctx, "INSERT INTO "+s.contributionFQN()+
		" (contribution_key, project, dataset_generation, build_id, source_kind,"+
		" auth_resource_path, resource_type, source_id, ordinal, concept_id, binding_id,"+
		" example, payload_digest, observation)"+
		" VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, NULL)",
		notNull.Key, notNull.Project, notNull.DatasetGeneration, notNull.BuildID, notNull.SourceKind,
		notNull.AuthResourcePath, notNull.ResourceType, notNull.SourceID, notNull.Ordinal,
		notNull.ConceptID, notNull.BindingID, notNull.Example, notNull.PayloadDigest)
	var pgErr *pgconn.PgError
	if err == nil || !errors.As(err, &pgErr) || pgErr.Code != notNullViolation ||
		pgErr.ColumnName != "observation" {
		t.Fatalf("NULL observation accepted or misattributed: %v", err)
	}
	s.requireNoContribution(t, notNull.Key, "NULL observation")

	dupKey := base("dup-key")
	s.seedContributionLiteral(t, dupKey)
	stolen := dupKey
	stolen.SourceID = "retained:Observation/stolen-slot"
	stolen.PayloadDigest = fmt.Sprintf("%064x", 0x1234)
	err = s.trySeedContributionLiteral(t, stolen)
	if err == nil || !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation ||
		pgErr.ConstraintName != "fhir_semantic_inventory_contribution_key_ux" {
		t.Fatalf("reused contribution_key accepted or misattributed: %v", err)
	}
	if row, found := s.witnessContribution(t, dupKey.Key); !found || row.PayloadDigest != dupKey.PayloadDigest {
		t.Fatalf("reused key disturbed the stored row: %+v found=%t", row, found)
	}

	dupSlot := base("dup-slot")
	s.seedContributionLiteral(t, dupSlot)
	rewritten := dupSlot
	rewritten.Key = fmt.Sprintf("%064x", 0x9999)
	rewritten.PayloadDigest = fmt.Sprintf("%064x", 0x7777)
	err = s.trySeedContributionLiteral(t, rewritten)
	if err == nil || !errors.As(err, &pgErr) || pgErr.Code != uniqueViolation ||
		pgErr.ConstraintName != "fhir_semantic_inventory_contribution_pkey" {
		t.Fatalf("second key at one slot accepted or misattributed: %v", err)
	}
	if row, found := s.witnessContribution(t, dupSlot.Key); !found || row.PayloadDigest != dupSlot.PayloadDigest {
		t.Fatalf("duplicate slot replaced the stored row: %+v found=%t", row, found)
	}
}

func TestSemanticContributionMigrationContract(t *testing.T) {
	s := newPgcat05Service(t)

	const goldenChecksum = "41579b3c86d17c5912195ccaa6ff8655e6a864c01a01f2a23fa1cb4aa6883b02"

	var version int64
	var name, checksum string
	var rowCount int
	err := s.iso.QueryRow(s.ctx,
		"SELECT COUNT(*) FROM "+s.ledgerFQN()+" WHERE version = 9 AND name = 'semantic_inventory_contribution'").Scan(&rowCount)
	if err != nil {
		s.fail("read ledger", err)
	}
	if rowCount != 1 {
		t.Fatalf("ledger rows for version 9 = %d, want exactly 1", rowCount)
	}
	if err := s.iso.QueryRow(s.ctx,
		"SELECT version, name, checksum FROM "+s.ledgerFQN()+" WHERE version = 9").Scan(&version, &name, &checksum); err != nil {
		s.fail("read ledger row", err)
	}
	if version != 9 || name != "semantic_inventory_contribution" {
		t.Fatalf("ledger row = (%d, %q), want (9, %q)", version, name, "semantic_inventory_contribution")
	}
	if checksum != goldenChecksum {
		t.Fatalf("000009 checksum = %s, golden %s", checksum, goldenChecksum)
	}

	before := s.witnessLedger(t)
	if _, err := storepostgres.MigrateSchema(s.ctx, s.openClient(t), s.schema); err != nil {
		s.fail("second MigrateSchema", err)
	}
	after := s.witnessLedger(t)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("double apply mutated the ledger:\n before %v\n after  %v", before, after)
	}
}

func (s *pgcat05Service) witnessLedger(t *testing.T) []storepostgres.LedgerRecord {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "SELECT version, name, checksum FROM "+s.ledgerFQN()+" ORDER BY version")
	if err != nil {
		s.fail("witness ledger", err)
	}
	defer rows.Close()
	var out []storepostgres.LedgerRecord
	for rows.Next() {
		var rec storepostgres.LedgerRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.Checksum); err != nil {
			s.fail("scan ledger", err)
		}
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness ledger", err)
	}
	return out
}

func TestSemanticContributionWriteValidationRefusals(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	base := pgcat05GoldensBatch(t, []string{"obs-a"})[0]
	mutate := func(fn func(*catalog.SemanticInventoryContribution)) catalog.SemanticInventoryContribution {
		c := base
		fn(&c)
		return c
	}
	cases := []struct {
		name  string
		bad   catalog.SemanticInventoryContribution
		match func(*testing.T, error)
	}{
		{"blank key", mutate(func(c *catalog.SemanticInventoryContribution) { c.Key = "" }), wantInvalidContribution},
		{"short key", mutate(func(c *catalog.SemanticInventoryContribution) { c.Key = c.Key[:63] }), wantInvalidContribution},
		{"uppercase key", mutate(func(c *catalog.SemanticInventoryContribution) { c.Key = strings.ToUpper(c.Key) }), wantInvalidContribution},
		{"blank project", mutate(func(c *catalog.SemanticInventoryContribution) { c.Project = "" }), wantInvalidContribution},
		{"blank generation", mutate(func(c *catalog.SemanticInventoryContribution) { c.DatasetGeneration = "   " }), wantInvalidContribution},
		{"blank source id", mutate(func(c *catalog.SemanticInventoryContribution) { c.SourceID = "" }), wantInvalidContribution},
		{"blank concept", mutate(func(c *catalog.SemanticInventoryContribution) { c.ConceptID = "" }), wantInvalidContribution},
		{"blank binding", mutate(func(c *catalog.SemanticInventoryContribution) { c.BindingID = "" }), wantInvalidContribution},
		{"blank resource type", mutate(func(c *catalog.SemanticInventoryContribution) { c.ResourceType = "" }), wantInvalidContribution},
		{"negative ordinal", mutate(func(c *catalog.SemanticInventoryContribution) { c.Ordinal = -1 }), wantInvalidContribution},
		{"unknown source kind", mutate(func(c *catalog.SemanticInventoryContribution) { c.SourceKind = "stream" }), wantInvalidContribution},
		{"build/generation mismatch", mutate(func(c *catalog.SemanticInventoryContribution) { c.BuildID = strings.Repeat("a", 64) }), wantInvalidContribution},
	}
	for _, tc := range cases {
		err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{tc.bad}, 0)
		tc.match(t, err)
		if errors.Is(err, catalogstore.ErrSemanticInventoryContributionConflict) ||
			errors.Is(err, catalogstore.ErrSemanticInventoryEntriesUnsupported) ||
			errors.Is(err, catalogstore.ErrRetainedSemanticSourceMissing) {
			t.Fatalf("%s: validation refusal misclassifies: %v", tc.name, err)
		}
	}
	if got := s.countContributions(t); got != 0 {
		t.Fatalf("validation refusals wrote %d rows, want 0 (refusal must precede any statement)", got)
	}

	dup := pgcat05GoldensBatch(t, []string{"obs-a"})
	second := dup[0]
	second.Key = strings.Repeat("e", 64)
	err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{dup[0], second}, 0)
	var conflict *catalogstore.SemanticInventoryContributionConflictError
	if !errors.As(err, &conflict) || !conflict.Slot {
		t.Fatalf("in-batch duplicate slot error = %v, want slot conflict", err)
	}
	if got := s.countContributions(t); got != 0 {
		t.Fatalf("in-batch duplicate wrote %d rows, want 0", got)
	}

	if err := repo.WriteSemanticInventoryContributions(s.ctx, pgcat05GoldensBatch(t, pgcat05GoldenIDs()), 0); err != nil {
		s.fail("control write", err)
	}
	if got := s.countContributions(t); got != int64(len(pgcat05Goldens)) {
		t.Fatalf("control count = %d, want %d", got, len(pgcat05Goldens))
	}
}

func wantInvalidContribution(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, catalogstore.ErrInvalidSemanticInventoryContribution) {
		t.Fatalf("validation error = %v, want ErrInvalidSemanticInventoryContribution", err)
	}
}

var pgcat05AdversarialIDs = []string{
	"Zebra", "apple", "Apple", "10", "2", "-x", "é",
	"obs-1", "obs-10", "obs-2", "obs.a", "obs~t", "obscure", "obs_c",
	"z-null", "z-jsonnull",
}

func TestSemanticContributionRetainedPageStableByteOrderKeyset(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	const project, generation, resourceType = "pgcat05-pages", "gen-p", "Observation"
	payload := []byte(pgcat05Goldens["obs-b"].payload)
	for _, id := range pgcat05AdversarialIDs {
		var rowPayload []byte
		switch id {
		case "z-null":
			rowPayload = nil
		case "z-jsonnull":
			rowPayload = []byte("null")
		default:
			rowPayload = payload
		}
		s.seedRetainedResource(t, project, generation, resourceType, id, "path/"+id, rowPayload)
	}
	for _, id := range pgcat05AdversarialIDs {
		s.seedRetainedResource(t, project+"-other", generation, resourceType, id, "decoy", payload)
		s.seedRetainedResource(t, project, generation+"-other", resourceType, id, "decoy", payload)
		s.seedRetainedResource(t, project, generation, "Patient", id, "decoy", payload)
	}

	var defaultOrder []string
	{
		rows, err := s.iso.Query(s.ctx, "SELECT logical_id FROM "+s.resourceFQN()+
			" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 ORDER BY logical_id",
			project, generation, resourceType)
		if err != nil {
			s.fail("witness default collation order", err)
		}
		for rows.Next() {
			var v string
			if err := rows.Scan(&v); err != nil {
				rows.Close()
				s.fail("scan default collation order", err)
			}
			defaultOrder = append(defaultOrder, v)
		}
		rows.Close()
	}
	want := append([]string(nil), pgcat05AdversarialIDs...)
	sort.Strings(want)
	if reflect.DeepEqual(defaultOrder, want) {
		t.Fatalf("server default collation order matches Go byte order on the adversarial set — the probe is vacuous on this service; expand the id set")
	}
	boundary := want[len(want)/2]
	var icuCount, byteCount int64
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.resourceFQN()+
		" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id > $4",
		project, generation, resourceType, boundary).Scan(&icuCount); err != nil {
		s.fail("icu keyset probe", err)
	}
	if err := s.iso.QueryRow(s.ctx, `SELECT COUNT(*) FROM `+s.resourceFQN()+
		` WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3 AND logical_id COLLATE "C" > $4 COLLATE "C"`,
		project, generation, resourceType, boundary).Scan(&byteCount); err != nil {
		s.fail("byte keyset probe", err)
	}
	goAbove := 0
	for _, id := range want {
		if id > boundary {
			goAbove++
		}
	}
	if byteCount != int64(goAbove) {
		t.Fatalf("byte-order predicate returned %d rows, Go byte order above %q is %d", byteCount, boundary, goAbove)
	}
	if icuCount == int64(goAbove) {
		t.Fatalf("default-collation predicate agrees with byte order on this set — predicate probe is vacuous; expand the id set")
	}

	var walked []string
	afterKey := ""
	pages := 0
	for {
		page, err := repo.ReadRetainedSemanticInventoryPage(s.ctx, project, generation, resourceType, afterKey, 3)
		if err != nil {
			s.fail("read page", err)
		}
		pages++
		if !page.SourceExists {
			t.Fatalf("page %d: SourceExists=false for a populated scope", pages)
		}
		if len(page.Rows) > 3 {
			t.Fatalf("page %d: %d rows, want at most 3", pages, len(page.Rows))
		}
		for _, row := range page.Rows {
			walked = append(walked, row.Key)
			if row.AuthResourcePath == nil {
				t.Fatalf("row %s: auth path is nil; this store never writes SQL NULL auth paths", row.Key)
			}
			if *row.AuthResourcePath != "path/"+row.Key {
				t.Fatalf("row %s: auth path = %q, want %q", row.Key, *row.AuthResourcePath, "path/"+row.Key)
			}
			switch row.Key {
			case "z-null":
				if row.Payload != nil {
					t.Fatalf("z-null: payload = %s, want SQL NULL (nil RawMessage)", row.Payload)
				}
			case "z-jsonnull":
				if string(row.Payload) != "null" {
					t.Fatalf("z-jsonnull: payload = %q, want the explicit JSON null document", row.Payload)
				}
			default:
				var got, expected map[string]any
				if err := json.Unmarshal(row.Payload, &got); err != nil {
					t.Fatalf("row %s: payload %s does not decode: %v", row.Key, row.Payload, err)
				}
				if err := json.Unmarshal([]byte(pgcat05Goldens["obs-b"].payload), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, expected) {
					t.Fatalf("row %s: payload %s, want the seeded JSONB %s", row.Key, row.Payload, pgcat05Goldens["obs-b"].payload)
				}
			}
		}
		if !page.HasMore {
			break
		}
		if len(page.Rows) == 0 {
			t.Fatalf("page %d: HasMore=true with no rows — cursor would stall", pages)
		}
		nextKey := page.Rows[len(page.Rows)-1].Key
		if nextKey <= afterKey {
			t.Fatalf("page %d: cursor did not advance (%q -> %q)", pages, afterKey, nextKey)
		}
		afterKey = nextKey
		if pages > len(pgcat05AdversarialIDs) {
			t.Fatalf("page walk did not terminate")
		}
	}
	if !reflect.DeepEqual(walked, want) {
		t.Fatalf("full walk =\n %v\nwant Go byte order\n %v", walked, want)
	}
}

func TestSemanticContributionRetainedPageEmptySourceDistinctions(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)
	const generation = "gen-e"

	page, err := repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-empty-none", generation, "Observation", "", 10)
	if err != nil {
		s.fail("read unproven scope", err)
	}
	if page.SourceExists || len(page.Rows) != 0 {
		t.Fatalf("never-profiled scope page = %+v, want SourceExists=false with no rows", page)
	}

	page, err = repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-empty-coverage", generation, "Observation", "", 10)
	if err != nil {
		s.fail("read coverage scope", err)
	}
	if page.SourceExists {
		t.Fatal("scope without any marker must be unproven before seeding (harness is not proving the distinction)")
	}
	s.seedCoverageMarker(t, "pgcat05-empty-coverage", generation, "", "Observation")
	page, err = repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-empty-coverage", generation, "Observation", "", 10)
	if err != nil {
		s.fail("read seeded coverage scope", err)
	}
	if !page.SourceExists || len(page.Rows) != 0 || page.HasMore {
		t.Fatalf("known-empty scope page = %+v, want SourceExists=true, no rows, no more", page)
	}
	s.exec(t, "DELETE FROM "+s.coverageFQN()+" WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3",
		"pgcat05-empty-coverage", generation, "Observation")
	page, err = repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-empty-coverage", generation, "Observation", "", 10)
	if err != nil {
		s.fail("read after marker drop", err)
	}
	if page.SourceExists {
		t.Fatal("known-empty claim survived dropping the coverage marker — the distinction is not marker-driven")
	}

	s.seedFieldProfileDoc(t, "pgcat05-empty-docs", generation, "", "Observation", "code.coding", 3)
	_, err = repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-empty-docs", generation, "Observation", "", 10)
	var missing *catalogstore.RetainedSemanticSourceMissingError
	if !errors.As(err, &missing) || !errors.Is(err, catalogstore.ErrRetainedSemanticSourceMissing) {
		t.Fatalf("missing-source read error = %v, want *RetainedSemanticSourceMissingError", err)
	}
	if missing.ResourceType != "Observation" || missing.Project != "pgcat05-empty-docs" {
		t.Fatalf("missing-source error names the wrong scope: %+v", missing)
	}
	for _, other := range []error{catalogstore.ErrSemanticInventoryStorageMissing, catalogstore.ErrInvalidRetainedSemanticPageRequest, catalogstore.ErrSemanticInventoryContributionConflict} {
		if errors.Is(err, other) {
			t.Fatalf("missing-source error misclassifies as %v", other)
		}
	}
	s.exec(t, "UPDATE "+s.fieldProfileFQN()+" SET doc_count = 0 WHERE project = $1 AND dataset_generation = $2 AND resource_type = $3",
		"pgcat05-empty-docs", generation, "Observation")
	page, err = repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-empty-docs", generation, "Observation", "", 10)
	if err != nil {
		s.fail("read after doc_count zeroed", err)
	}
	if page.SourceExists {
		t.Fatal("doc_count = 0 was treated as document/coverage evidence")
	}
	s.exec(t, "INSERT INTO "+s.fieldProfileFQN()+
		" (project, dataset_generation, auth_resource_path, resource_type, path, profile_key,"+
		" record_type, kind, doc_count, max_items, sample_count, distinct_truncated,"+
		" enrichment_truncated, pivot_candidate, pivot_kind, pivot_family,"+
		" pivot_column_selector, pivot_value_selector, pivot_item_source, pivot_item_resource_type)"+
		" VALUES ($1, $2, '', $3, '', 'fpk-status', 'profile_status', '', 9, 0, 0, false, true, false, '', '', '', '', '', '')",
		"pgcat05-empty-status", generation, "Observation")
	page, err = repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-empty-status", generation, "Observation", "", 10)
	if err != nil {
		s.fail("read profile_status-shaped scope", err)
	}
	if page.SourceExists {
		t.Fatal("a profile_status marker row (no fields) must not count as document evidence")
	}
}

func TestSemanticContributionRetainedPageMidWalkEndsCollection(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)
	const project, generation = "pgcat05-midwalk", "gen-m"
	s.seedRetainedResource(t, project, generation, "Observation", "m-a", "", []byte(pgcat05Goldens["obs-b"].payload))
	s.seedRetainedResource(t, project, generation, "Observation", "m-b", "", []byte(pgcat05Goldens["obs-b"].payload))
	s.seedFieldProfileDoc(t, project, generation, "", "Observation", "code.coding", 2)

	first, err := repo.ReadRetainedSemanticInventoryPage(s.ctx, project, generation, "Observation", "", 10)
	if err != nil {
		s.fail("read first page", err)
	}
	if !first.SourceExists || len(first.Rows) != 2 {
		t.Fatalf("first page = %+v, want two rows", first)
	}
	end, err := repo.ReadRetainedSemanticInventoryPage(s.ctx, project, generation, "Observation", "m-z", 10)
	if err != nil {
		t.Fatalf("mid-walk empty read error = %v, want plain end of collection", err)
	}
	if !end.SourceExists || len(end.Rows) != 0 || end.HasMore {
		t.Fatalf("mid-walk empty page = %+v, want SourceExists=true, no rows, no more", end)
	}
}

func TestSemanticContributionRetainedPageRequestValidation(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	bad := []struct {
		name                            string
		project, generation, collection string
		limit                           int
	}{
		{"non-canonical collection case", "p", "g", "observation", 10},
		{"unknown collection", "p", "g", "Bogus99", 10},
		{"trailing-space collection", "p", "g", "Observation ", 10},
		{"empty collection", "p", "g", "", 10},
		{"blank project", "", "g", "Observation", 10},
		{"whitespace project", "   ", "g", "Observation", 10},
		{"zero limit", "p", "g", "Observation", 0},
		{"negative limit", "p", "g", "Observation", -1},
		{"oversized limit", "p", "g", "Observation", 1001},
	}
	for _, tc := range bad {
		_, err := repo.ReadRetainedSemanticInventoryPage(s.ctx, tc.project, tc.generation, tc.collection, "", tc.limit)
		if !errors.Is(err, catalogstore.ErrInvalidRetainedSemanticPageRequest) {
			t.Fatalf("%s: error = %v, want ErrInvalidRetainedSemanticPageRequest", tc.name, err)
		}
		if errors.Is(err, catalogstore.ErrRetainedSemanticSourceMissing) || errors.Is(err, catalogstore.ErrSemanticInventoryStorageMissing) {
			t.Fatalf("%s: validation refusal misclassifies: %v", tc.name, err)
		}
	}
	if _, err := repo.ReadRetainedSemanticInventoryPage(s.ctx, "pgcat05-validate", "g", "Observation", "", 1000); err != nil {
		s.fail("control page read", err)
	}
}

func TestSemanticContributionRetainedPageGenerationNormalization(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)
	const project = "pgcat05-normalize"
	payload := []byte(pgcat05Goldens["obs-b"].payload)
	s.seedRetainedResource(t, project, "gen-n", "Observation", "clean-1", "", payload)
	s.seedRetainedResource(t, project, "gen-n", "Observation", "clean-2", "", payload)
	s.seedRetainedResource(t, project, "  gen-n  ", "Observation", "padded-only", "", payload)

	for _, request := range []string{"gen-n", "  gen-n  ", "\tgen-n\n"} {
		page, err := repo.ReadRetainedSemanticInventoryPage(s.ctx, project, request, "Observation", "", 10)
		if err != nil {
			s.fail("normalized read", err)
		}
		keys := []string{}
		for _, row := range page.Rows {
			keys = append(keys, row.Key)
		}
		if !reflect.DeepEqual(keys, []string{"clean-1", "clean-2"}) {
			t.Fatalf("request %q returned %v, want only the normalized spelling's rows", request, keys)
		}
	}
	var padded string
	if err := s.iso.QueryRow(s.ctx, "SELECT logical_id FROM "+s.resourceFQN()+
		" WHERE project = $1 AND dataset_generation = $2", project, "  gen-n  ").Scan(&padded); err != nil {
		s.fail("witness padded row", err)
	}
	if padded != "padded-only" {
		t.Fatalf("padded row missing from the table — the normalization probe was vacuous")
	}
}

func TestSemanticContributionPrepareRefusesMissingTables(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	rerun := func(name string) {
		migrations, err := storepostgres.EmbeddedMigrations()
		if err != nil {
			s.fail("load migrations", err)
		}
		for _, m := range migrations {
			if m.Name == name {
				conn, err := s.iso.Acquire(s.ctx)
				if err != nil {
					s.fail("acquire rerun connection", err)
				}
				defer conn.Release()
				if _, err := conn.Exec(s.ctx, "SET search_path TO "+pgx.Identifier{s.schema}.Sanitize()+", public"); err != nil {
					s.fail("set rerun search_path", err)
				}
				if _, err := conn.Exec(s.ctx, m.SQL); err != nil {
					s.fail("rerun migration "+m.Name, err)
				}
				return
			}
		}
		t.Fatalf("migration %q is not embedded — the harness lost its restore path", name)
	}

	drops := []struct {
		table     string
		migration string
		dropSQL   string
	}{
		{"fhir_semantic_inventory_contribution", "semantic_inventory_contribution",
			"DROP TABLE " + s.contributionFQN() + " CASCADE"},
		{"fhir_semantic_inventory_build", "semantic_inventory_build",
			"DROP TABLE " + s.buildFQN() + " CASCADE"},
		{"fhir_resource", "fhir_resource",
			"DROP TABLE " + s.resourceFQN() + " CASCADE"},
	}
	for _, d := range drops {
		s.exec(t, d.dropSQL)
		err := repo.PrepareSemanticInventoryBackfill(s.ctx)
		var missing *catalogstore.SemanticInventoryStorageMissingError
		if !errors.As(err, &missing) || !errors.Is(err, catalogstore.ErrSemanticInventoryStorageMissing) {
			t.Fatalf("dropped %s: error = %v, want named storage-missing refusal", d.table, err)
		}
		if missing.Table != d.table {
			t.Fatalf("dropped %s: refusal names %q", d.table, missing.Table)
		}
		if errors.Is(err, catalogstore.ErrSemanticInventoryEntriesUnsupported) || errors.Is(err, catalogstore.ErrRetainedSemanticSourceMissing) {
			t.Fatalf("dropped %s: refusal misclassifies: %v", d.table, err)
		}
		rerun(d.migration)
		if err := repo.PrepareSemanticInventoryBackfill(s.ctx); err != nil {
			t.Fatalf("restored %s: Prepare error = %v", d.table, err)
		}
	}
}

func TestSemanticContributionEntriesRefusalIsNamedNoOp(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	build := catalog.NewSemanticInventoryBuild(pgcat05GoldenProject, pgcat05GoldenGeneration, "")
	build.SourceKind = catalog.SemanticInventorySourceRetained
	got, err := repo.EnsureSemanticInventoryEntries(s.ctx, build)
	var unsupported *catalogstore.SemanticInventoryEntriesUnsupportedError
	if !errors.As(err, &unsupported) || !errors.Is(err, catalogstore.ErrSemanticInventoryEntriesUnsupported) {
		t.Fatalf("Ensure error = %v, want named entries-unsupported refusal", err)
	}
	if got.Key != "" || got.EntryIndexVersion != 0 {
		t.Fatalf("refused Ensure returned a mutated build: %+v", got)
	}
	if unsupported.EntryIndexVersion != 0 || unsupported.BuildID != build.BuildID {
		t.Fatalf("refusal does not name the build: %+v", unsupported)
	}
	if errors.Is(err, catalogstore.ErrSemanticInventoryClaimLost) || errors.Is(err, catalogstore.ErrSemanticInventoryStorageMissing) {
		t.Fatalf("entries refusal misclassifies: %v", err)
	}
	if got := s.countContributions(t); got != 0 {
		t.Fatalf("Ensure refusal wrote %d contribution rows, want 0", got)
	}

	corrupted := build
	corrupted.Key = "sib_not_a_digest"
	_, err = repo.EnsureSemanticInventoryEntries(s.ctx, corrupted)
	if !errors.Is(err, catalogstore.ErrInvalidSemanticInventoryBuild) {
		t.Fatalf("corrupted build Ensure error = %v, want ErrInvalidSemanticInventoryBuild", err)
	}
	if errors.Is(err, catalogstore.ErrSemanticInventoryEntriesUnsupported) {
		t.Fatal("corrupted build must fail as invalid, not merely unsupported")
	}
}

const pgcat05RaceRounds = 10

func pgcat05RaceContribution(t *testing.T, project, generation, sourceID, example string) (catalog.SemanticInventoryContribution, contributionLiteral) {
	t.Helper()
	g := pgcat05Goldens["obs-b"]
	emitted := pgcat05EmitContribution(t, project, generation, g.authPath, g.resourceType, sourceID, g.payload)
	if len(emitted) != 1 {
		t.Fatalf("emitter produced %d contributions for %s, want 1", len(emitted), sourceID)
	}
	c := emitted[0]
	if example != "" {
		c.Example = example
		c.Observation.Examples = []string{example}
	}
	digest, observation, err := prepareContributionTest(c)
	if err != nil {
		t.Fatalf("prepare race contribution: %v", err)
	}
	literal := contributionLiteral{
		Key: c.Key, Project: c.Project, DatasetGeneration: c.DatasetGeneration, BuildID: c.BuildID,
		SourceKind: c.SourceKind, AuthResourcePath: c.AuthResourcePath, ResourceType: c.ResourceType,
		SourceID: c.SourceID, Ordinal: c.Ordinal, ConceptID: c.ConceptID, BindingID: c.BindingID,
		Example: c.Example, PayloadDigest: digest, Observation: string(observation),
	}
	return c, literal
}

func pgcat05RequireDistinctBackends(t *testing.T, pidA, pidB int32) {
	t.Helper()
	if pidA <= 0 || pidB <= 0 || pidA == pidB {
		t.Fatalf("race connections must be distinct live backends, got %d and %d", pidA, pidB)
	}
}

func TestSemanticContributionIdenticalWriteRaceTwoConnections(t *testing.T) {
	s := newPgcat05Service(t)
	clientA, pidA := s.openDedicatedClient(t, "identical-race A")
	clientB, pidB := s.openDedicatedClient(t, "identical-race B")

	repoA, err := catalogstore.NewSemanticContributionRepository(clientA, s.schema)
	if err != nil {
		s.fail("repository A", err)
	}
	repoB, err := catalogstore.NewSemanticContributionRepository(clientB, s.schema)
	if err != nil {
		s.fail("repository B", err)
	}

	for round := 0; round < pgcat05RaceRounds; round++ {
		pgcat05RequireDistinctBackends(t, pidA, pidB)
		var batch []catalog.SemanticInventoryContribution
		literals := map[string]contributionLiteral{}
		for i := 0; i < 3; i++ {
			c, literal := pgcat05RaceContribution(t, "pgcat05-race-ident", "gen",
				fmt.Sprintf("retained:Observation/race-%d-%d", round, i), "")
			batch = append(batch, c)
			literals[c.Key] = literal
		}
		start := make(chan struct{})
		var writers sync.WaitGroup
		errs := make([]error, 2)
		for i, repo := range []*catalogstore.SemanticContributionRepository{repoA, repoB} {
			writers.Add(1)
			go func(i int, repo *catalogstore.SemanticContributionRepository) {
				defer writers.Done()
				<-start
				errs[i] = repo.WriteSemanticInventoryContributions(s.ctx, batch, 0)
			}(i, repo)
		}
		close(start)
		writers.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d writer %d: %v", round, i, err)
			}
		}
		for _, literal := range literals {
			w, found := s.witnessContribution(t, literal.Key)
			if !found {
				t.Fatalf("round %d: row %s missing after race", round, literal.Key)
			}
			if !sameContributionLiteral(w.contributionLiteral, literal) {
				t.Fatalf("round %d: stored row = %+v, want %+v", round, w.contributionLiteral, literal)
			}
		}
	}
}

func TestSemanticContributionUncommittedWinnerRaceTwoConnections(t *testing.T) {
	s := newPgcat05Service(t)
	clientA, pidA := s.openDedicatedClient(t, "uncommitted A")
	clientB, pidB := s.openDedicatedClient(t, "uncommitted B")

	repoB, err := catalogstore.NewSemanticContributionRepository(clientB, s.schema)
	if err != nil {
		s.fail("repository B", err)
	}

	for round := 0; round < pgcat05RaceRounds; round++ {
		pgcat05RequireDistinctBackends(t, pidA, pidB)
		commitWinner := round%2 == 0
		sourceID := fmt.Sprintf("retained:Observation/uncommitted-%d", round)
		_, literalA := pgcat05RaceContribution(t, "pgcat05-race-uncommitted", "gen", sourceID, "payload-from-A")
		contributionB, literalB := pgcat05RaceContribution(t, "pgcat05-race-uncommitted", "gen", sourceID, "payload-from-B")
		if contributionB.Key != literalA.Key {
			t.Fatalf("round %d: conflicting payloads produced different keys %s vs %s", round, contributionB.Key, literalA.Key)
		}

		connA, err := clientA.Acquire(s.ctx)
		if err != nil {
			s.fail("acquire A", err)
		}
		tx, err := connA.Begin(s.ctx)
		if err != nil {
			connA.Release()
			s.fail("begin A", err)
		}
		if _, err := tx.Exec(s.ctx, s.contributionSeedSQL(),
			literalA.Key, literalA.Project, literalA.DatasetGeneration, literalA.BuildID, literalA.SourceKind,
			literalA.AuthResourcePath, literalA.ResourceType, literalA.SourceID, literalA.Ordinal,
			literalA.ConceptID, literalA.BindingID, literalA.Example, literalA.PayloadDigest, literalA.Observation); err != nil {
			_ = tx.Rollback(s.ctx)
			connA.Release()
			s.fail("uncommitted insert A", err)
		}

		bDone := make(chan error, 1)
		go func() {
			bDone <- repoB.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{contributionB}, 0)
		}()

		if !s.waitForBackendLockWait(t, pidB, 15*time.Second) {
			_ = tx.Rollback(s.ctx)
			connA.Release()
			t.Fatalf("round %d: writer B never waited on A's uncommitted insert", round)
		}
		if commitWinner {
			if err := tx.Commit(s.ctx); err != nil {
				connA.Release()
				s.fail("commit A", err)
			}
		} else {
			if err := tx.Rollback(s.ctx); err != nil {
				connA.Release()
				s.fail("rollback A", err)
			}
		}
		connA.Release()
		bErr := <-bDone

		w, found := s.witnessContribution(t, literalA.Key)
		if !found {
			t.Fatalf("round %d: no row after race", round)
		}
		if commitWinner {
			var conflict *catalogstore.SemanticInventoryContributionConflictError
			if !errors.As(bErr, &conflict) {
				t.Fatalf("round %d (commit): B error = %v, want typed conflict", round, bErr)
			}
			if conflict.StoredDigest != literalA.PayloadDigest || conflict.ProvidedDigest != literalB.PayloadDigest {
				t.Fatalf("round %d (commit): conflict digests = %s/%s, want %s/%s",
					round, conflict.StoredDigest, conflict.ProvidedDigest, literalA.PayloadDigest, literalB.PayloadDigest)
			}
			if !sameContributionLiteral(w.contributionLiteral, literalA) {
				t.Fatalf("round %d (commit): committed winner's row disturbed:\n stored %+v\n want  %+v", round, w.contributionLiteral, literalA)
			}
		} else {
			if bErr != nil {
				t.Fatalf("round %d (abort): B error = %v, want post-abort convergence to B's row", round, bErr)
			}
			if !sameContributionLiteral(w.contributionLiteral, literalB) {
				t.Fatalf("round %d (abort): row after aborted winner:\n stored %+v\n want  %+v", round, w.contributionLiteral, literalB)
			}
		}
	}
}

func (s *pgcat05Service) waitForBackendLockWait(t *testing.T, pid int32, budget time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := s.iso.QueryRow(s.ctx,
			"SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid = $1 AND state = 'active' AND wait_event_type = 'Lock')",
			pid).Scan(&waiting); err != nil {
			s.fail("poll pg_stat_activity", err)
		}
		if waiting {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

type pgcat05BackfillProbe struct {
	*catalogstore.SemanticInventoryBackfillStore
	mu                       sync.Mutex
	writeCalls, advanceCalls int
	failWriteAt              int
}

func (p *pgcat05BackfillProbe) WriteSemanticInventoryContributions(ctx context.Context, contributions []catalog.SemanticInventoryContribution, batchSize int) error {
	p.mu.Lock()
	p.writeCalls++
	call := p.writeCalls
	fail := p.failWriteAt != 0 && call == p.failWriteAt
	p.mu.Unlock()
	if fail {
		return errors.New("injected contribution write failure")
	}
	return p.SemanticInventoryBackfillStore.WriteSemanticInventoryContributions(ctx, contributions, batchSize)
}

func (p *pgcat05BackfillProbe) AdvanceSemanticInventoryBackfill(ctx context.Context, build catalog.SemanticInventoryBuild, token string, expected, next catalog.SemanticInventoryCheckpoint, scanned int64, lease time.Duration) (catalog.SemanticInventoryBuild, error) {
	p.mu.Lock()
	p.advanceCalls++
	p.mu.Unlock()
	return p.SemanticInventoryBackfillStore.AdvanceSemanticInventoryBackfill(ctx, build, token, expected, next, scanned, lease)
}

func (p *pgcat05BackfillProbe) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeCalls, p.advanceCalls
}

func (p *pgcat05BackfillProbe) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writeCalls, p.advanceCalls = 0, 0
}

func pgcat05Manifest(t *testing.T, project, generation string, resourceTypes []string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("manifest ref: %v", err)
	}
	schema, err := publication.NewSchemaSnapshot("pgcat05-schema", "4.0.1", strings.Repeat("a", 64), resourceTypes)
	if err != nil {
		t.Fatalf("schema snapshot: %v", err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	manifest.State = publication.StateStaged
	return manifest
}

func pgcat05BackfillOptions() ingest.SemanticInventoryBackfillOptions {
	return ingest.SemanticInventoryBackfillOptions{
		Project: pgcat05GoldenProject, DatasetGeneration: pgcat05GoldenGeneration,
		PageSize: 2, BatchSize: 1,
	}
}

func seedPgcat05GoldenSources(t *testing.T, s *pgcat05Service) {
	t.Helper()
	for _, id := range pgcat05GoldenIDs() {
		g := pgcat05Goldens[id]
		s.seedRetainedResource(t, pgcat05GoldenProject, pgcat05GoldenGeneration, g.resourceType, id, g.authPath, []byte(g.payload))
	}
	s.seedRetainedResource(t, pgcat05GoldenProject, pgcat05GoldenGeneration, "Patient", "pat-a", "",
		[]byte(`{"resourceType":"Patient","gender":"female"}`))
	s.seedFieldProfileDoc(t, pgcat05GoldenProject, pgcat05GoldenGeneration, "", "Observation", "code.coding", 6)
	s.seedFieldProfileDoc(t, pgcat05GoldenProject, pgcat05GoldenGeneration, "", "Patient", "gender", 1)
}

func pgcat05FullTableWitness(t *testing.T, s *pgcat05Service) []contributionWitness {
	t.Helper()
	rows, err := s.iso.Query(s.ctx, "SELECT "+contributionWitnessColumns+" FROM "+s.contributionFQN()+" ORDER BY contribution_key")
	if err != nil {
		s.fail("witness full table", err)
	}
	defer rows.Close()
	var out []contributionWitness
	for rows.Next() {
		var w contributionWitness
		if err := rows.Scan(&w.Key, &w.Project, &w.DatasetGeneration, &w.BuildID, &w.SourceKind,
			&w.AuthResourcePath, &w.ResourceType, &w.SourceID, &w.Ordinal, &w.ConceptID,
			&w.BindingID, &w.Example, &w.PayloadDigest, &w.Observation, &w.RecordedAt); err != nil {
			s.fail("scan full table witness", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		s.fail("witness full table", err)
	}
	return out
}

func TestSemanticContributionBackfillRoundTripEndToEnd(t *testing.T) {
	s := newPgcat05Service(t)
	seedPgcat05GoldenSources(t, s)
	probe := &pgcat05BackfillProbe{SemanticInventoryBackfillStore: s.backfillStore(t)}

	manifest := pgcat05Manifest(t, pgcat05GoldenProject, pgcat05GoldenGeneration, []string{"Observation", "Patient"})
	report, err := ingest.BackfillSemanticInventory(s.ctx, probe, manifest, pgcat05BackfillOptions())
	if err != nil {
		s.fail("backfill", err)
	}
	if report.State != catalog.SemanticInventoryComplete || report.ScannedTotal != 7 || report.Checkpoint != (catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "pat-a"}) {
		t.Fatalf("report = %+v, want complete with 7 scanned and Patient/pat-a checkpoint", report)
	}
	if report.SourceAvailability != catalog.SemanticInventorySourceAvailabilityVerified || len(report.MissingCollections) != 0 {
		t.Fatalf("availability = %q missing=%v, want verified with none", report.SourceAvailability, report.MissingCollections)
	}
	if writes, advances := probe.counts(); writes != 6 || advances != 4 {
		t.Fatalf("probe writes=%d advances=%d, want 6 page chunks over 4 pages", writes, advances)
	}

	ids := pgcat05GoldenIDs()
	if got := s.countContributions(t); got != int64(len(ids)) {
		t.Fatalf("contribution count = %d, want %d", got, len(ids))
	}
	for _, id := range ids {
		want := pgcat05ExpectedLiteral(pgcat05Goldens[id])
		w, found := s.witnessContribution(t, want.Key)
		if !found {
			t.Fatalf("%s: golden contribution %s missing after backfill", id, want.Key)
		}
		if !sameContributionLiteral(w.contributionLiteral, want) {
			t.Fatalf("%s: stored %+v, want golden %+v", id, w.contributionLiteral, want)
		}
	}
	build, found := s.witnessBuildRow(t, pgcat05GoldenProject, pgcat05GoldenGeneration, catalog.SemanticInventoryBuildID(pgcat05GoldenProject, pgcat05GoldenGeneration))
	if !found {
		t.Fatal("build row missing")
	}
	if build.State != catalog.SemanticInventoryComplete || build.ScannedResources != 7 ||
		build.SourceCheckpoint != (catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "pat-a"}) ||
		build.LeaseToken != "" || build.LeaseExpiresAt != 0 || len(build.Diagnostics) != 0 ||
		build.SourceAvailability != catalog.SemanticInventorySourceAvailabilityVerified {
		t.Fatalf("build row = %+v, want complete/7/Patient-pat-a/released/no-diagnostics/verified", build)
	}

	before := pgcat05FullTableWitness(t, s)
	_, err = ingest.BackfillSemanticInventory(s.ctx, probe, manifest, pgcat05BackfillOptions())
	if !errors.Is(err, catalogstore.ErrSemanticInventoryEntriesUnsupported) {
		t.Fatalf("completed version-0 replay error = %v, want named entries refusal", err)
	}
	after := pgcat05FullTableWitness(t, s)
	if !sameContributionWitnesses(before, after) {
		t.Fatalf("entries refusal mutated contribution rows:\n before %v\n after  %v", before, after)
	}

	s.exec(t, "UPDATE "+s.buildFQN()+" SET entry_index_version = $1 WHERE project = $2 AND dataset_generation = $3 AND build_id = $4",
		catalog.SemanticInventoryEntryIndexVersion, pgcat05GoldenProject, pgcat05GoldenGeneration, catalog.SemanticInventoryBuildID(pgcat05GoldenProject, pgcat05GoldenGeneration))
	probe.reset()
	replay, err := ingest.BackfillSemanticInventory(s.ctx, probe, manifest, pgcat05BackfillOptions())
	if err != nil {
		t.Fatalf("version-2 replay: %v", err)
	}
	if !replay.NoOp || replay.State != catalog.SemanticInventoryComplete {
		t.Fatalf("version-2 replay report = %+v, want clean no-op", replay)
	}
	if writes, advances := probe.counts(); writes != 0 || advances != 0 {
		t.Fatalf("no-op replay did work: writes=%d advances=%d", writes, advances)
	}
	final := pgcat05FullTableWitness(t, s)
	if !sameContributionWitnesses(before, final) {
		t.Fatalf("replay sequence disturbed rows:\n before %v\n after  %v", before, final)
	}
}

func sameContributionWitnesses(a, b []contributionWitness) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !a[i].sameAs(b[i]) {
			return false
		}
	}
	return true
}

func TestSemanticContributionBackfillFailedBatchKeepsCheckpointAndReplaysIdempotently(t *testing.T) {
	s := newPgcat05Service(t)
	seedPgcat05GoldenSources(t, s)
	probe := &pgcat05BackfillProbe{SemanticInventoryBackfillStore: s.backfillStore(t)}
	manifest := pgcat05Manifest(t, pgcat05GoldenProject, pgcat05GoldenGeneration, []string{"Observation", "Patient"})
	options := pgcat05BackfillOptions()

	probe.failWriteAt = 4
	_, err := ingest.BackfillSemanticInventory(s.ctx, probe, manifest, options)
	if err == nil || !strings.Contains(err.Error(), "injected contribution write failure") {
		t.Fatalf("injected failure error = %v", err)
	}
	buildID := catalog.SemanticInventoryBuildID(pgcat05GoldenProject, pgcat05GoldenGeneration)
	build, found := s.witnessBuildRow(t, pgcat05GoldenProject, pgcat05GoldenGeneration, buildID)
	if !found {
		t.Fatal("build row missing after failed run")
	}
	if build.State != catalog.SemanticInventoryFailed ||
		build.SourceCheckpoint != (catalog.SemanticInventoryCheckpoint{Collection: "Observation", Key: "obs-b"}) ||
		build.ScannedResources != 2 || build.LeaseToken != "" || build.LeaseExpiresAt != 0 {
		t.Fatalf("failed-run build = %+v, want failed at Observation/obs-b with 2 scanned and released lease", build)
	}
	if len(build.Diagnostics) != 1 || build.Diagnostics[0] != "injected contribution write failure" {
		t.Fatalf("failed-run diagnostics = %v, want exactly the injected cause", build.Diagnostics)
	}
	if writes, advances := probe.counts(); writes != 4 || advances != 1 {
		t.Fatalf("failed-run probe writes=%d advances=%d, want 4 attempts with 1 checkpoint advance", writes, advances)
	}
	for _, id := range []string{"obs-a", "obs-b", "obs-c"} {
		if _, found := s.witnessContribution(t, pgcat05Goldens[id].key); !found {
			t.Fatalf("%s: committed chunk missing after the failed run", id)
		}
	}
	s.requireNoContribution(t, pgcat05Goldens["obs-d"].key, "failed chunk obs-d")

	crashed, found := s.witnessContribution(t, pgcat05Goldens["obs-c"].key)
	if !found {
		t.Fatal("crash-window row obs-c missing")
	}
	crashedWant := pgcat05ExpectedLiteral(pgcat05Goldens["obs-c"])
	if !sameContributionLiteral(crashed.contributionLiteral, crashedWant) {
		t.Fatalf("crash-window row = %+v, want %+v", crashed.contributionLiteral, crashedWant)
	}

	probe.failWriteAt = 0
	probe.reset()
	report, err := ingest.BackfillSemanticInventory(s.ctx, probe, manifest, options)
	if err != nil {
		s.fail("resumed backfill", err)
	}
	if report.State != catalog.SemanticInventoryComplete || report.ScannedTotal != 7 {
		t.Fatalf("resumed report = %+v", report)
	}
	resumed, found := s.witnessContribution(t, pgcat05Goldens["obs-c"].key)
	if !found || !resumed.sameAs(crashed) {
		t.Fatalf("resume disturbed the replayed crash-window row:\n before %+v\n after  %+v", crashed, resumed)
	}
	if writes, advances := probe.counts(); writes != 4 || advances != 3 {
		t.Fatalf("resumed probe writes=%d advances=%d, want 4 chunk writes (one a replay) over 3 page advances", writes, advances)
	}
	for _, id := range pgcat05GoldenIDs() {
		want := pgcat05ExpectedLiteral(pgcat05Goldens[id])
		w, found := s.witnessContribution(t, want.Key)
		if !found || !sameContributionLiteral(w.contributionLiteral, want) {
			t.Fatalf("%s: after resume stored %+v found=%t, want %+v", id, w.contributionLiteral, found, want)
		}
	}
	build, _ = s.witnessBuildRow(t, pgcat05GoldenProject, pgcat05GoldenGeneration, buildID)
	if build.State != catalog.SemanticInventoryComplete ||
		build.SourceCheckpoint != (catalog.SemanticInventoryCheckpoint{Collection: "Patient", Key: "pat-a"}) ||
		len(build.Diagnostics) != 0 {
		t.Fatalf("resumed build = %+v, want complete at Patient/pat-a with diagnostics cleared", build)
	}
}

func TestSemanticContributionBackfillSourceAvailability(t *testing.T) {
	s := newPgcat05Service(t)
	const generation = "gen-av"
	optionsFor := func(project string) ingest.SemanticInventoryBackfillOptions {
		return ingest.SemanticInventoryBackfillOptions{Project: project, DatasetGeneration: generation, PageSize: 2, BatchSize: 50}
	}
	manifestFor := func(project string) publication.Manifest {
		return pgcat05Manifest(t, project, generation, []string{"Observation", "Patient"})
	}
	seedObservation := func(project string) {
		s.seedRetainedResource(t, project, generation, "Observation", "av-a", "", []byte(pgcat05Goldens["obs-b"].payload))
		s.seedFieldProfileDoc(t, project, generation, "", "Observation", "code.coding", 1)
	}

	project := "pgcat05-av-unproven"
	seedObservation(project)
	probe := &pgcat05BackfillProbe{SemanticInventoryBackfillStore: s.backfillStore(t)}
	report, err := ingest.BackfillSemanticInventory(s.ctx, probe, manifestFor(project), optionsFor(project))
	if err != nil {
		s.fail("unproven backfill", err)
	}
	if report.State != catalog.SemanticInventoryComplete ||
		report.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven ||
		len(report.MissingCollections) != 1 || report.MissingCollections[0] != "Patient" {
		t.Fatalf("unproven report = %+v, want complete/unproven/[Patient]", report)
	}
	build, _ := s.witnessBuildRow(t, project, generation, catalog.SemanticInventoryBuildID(project, generation))
	if build.SourceAvailability != catalog.SemanticInventorySourceAvailabilityUnproven {
		t.Fatalf("unproven build availability = %q", build.SourceAvailability)
	}

	project = "pgcat05-av-knownempty"
	seedObservation(project)
	s.seedCoverageMarker(t, project, generation, "", "Patient")
	probe = &pgcat05BackfillProbe{SemanticInventoryBackfillStore: s.backfillStore(t)}
	report, err = ingest.BackfillSemanticInventory(s.ctx, probe, manifestFor(project), optionsFor(project))
	if err != nil {
		s.fail("known-empty backfill", err)
	}
	if report.State != catalog.SemanticInventoryComplete ||
		report.SourceAvailability != catalog.SemanticInventorySourceAvailabilityVerified ||
		len(report.MissingCollections) != 0 {
		t.Fatalf("known-empty report = %+v, want complete/verified/no-missing", report)
	}

	project = "pgcat05-av-contradict"
	seedObservation(project)
	s.seedFieldProfileDoc(t, project, generation, "", "Patient", "gender", 4)
	probe = &pgcat05BackfillProbe{SemanticInventoryBackfillStore: s.backfillStore(t)}
	_, err = ingest.BackfillSemanticInventory(s.ctx, probe, manifestFor(project), optionsFor(project))
	if !errors.Is(err, catalogstore.ErrRetainedSemanticSourceMissing) {
		t.Fatalf("contradiction backfill error = %v, want ErrRetainedSemanticSourceMissing", err)
	}
	if !strings.Contains(err.Error(), "Patient") {
		t.Fatalf("contradiction error does not name the collection: %v", err)
	}
	build, _ = s.witnessBuildRow(t, project, generation, catalog.SemanticInventoryBuildID(project, generation))
	if build.State != catalog.SemanticInventoryFailed || len(build.Diagnostics) != 1 || !strings.Contains(build.Diagnostics[0], "Patient") {
		t.Fatalf("contradiction build = %+v, want failed with a Patient diagnostic", build)
	}
}

func TestSemanticContributionBackfillClaimRaceTwoConnections(t *testing.T) {
	s := newPgcat05Service(t)
	clientA, pidA := s.openDedicatedClient(t, "claim-race A")
	clientB, pidB := s.openDedicatedClient(t, "claim-race B")

	const generation = "gen-cr"
	for round := 0; round < pgcat05RaceRounds; round++ {
		pgcat05RequireDistinctBackends(t, pidA, pidB)
		project := fmt.Sprintf("pgcat05-claim-race-%d", round)
		for _, id := range []string{"obs-b", "obs-e"} {
			g := pgcat05Goldens[id]
			s.seedRetainedResource(t, project, generation, g.resourceType, id, g.authPath, []byte(g.payload))
		}
		manifest := pgcat05Manifest(t, project, generation, []string{"Observation"})
		options := ingest.SemanticInventoryBackfillOptions{Project: project, DatasetGeneration: generation, PageSize: 1, BatchSize: 1}

		storeA, err := catalogstore.NewSemanticInventoryBackfillStore(
			pgcat05Must(catalogstore.NewSemanticContributionRepository(clientA, s.schema)),
			pgcat05Must(catalogstore.NewSemanticBuildRepository(clientA, s.schema)))
		if err != nil {
			s.fail("composite A", err)
		}
		storeB, err := catalogstore.NewSemanticInventoryBackfillStore(
			pgcat05Must(catalogstore.NewSemanticContributionRepository(clientB, s.schema)),
			pgcat05Must(catalogstore.NewSemanticBuildRepository(clientB, s.schema)))
		if err != nil {
			s.fail("composite B", err)
		}

		start := make(chan struct{})
		var racers sync.WaitGroup
		errs := make([]error, 2)
		for i, store := range []*catalogstore.SemanticInventoryBackfillStore{storeA, storeB} {
			racers.Add(1)
			go func(i int, store *catalogstore.SemanticInventoryBackfillStore) {
				defer racers.Done()
				<-start
				_, errs[i] = ingest.BackfillSemanticInventory(s.ctx, store, manifest, options)
			}(i, store)
		}
		close(start)
		racers.Wait()

		completed := 0
		for _, err := range errs {
			if err == nil {
				completed++
				continue
			}
			if !errors.Is(err, catalogstore.ErrSemanticInventoryClaimed) &&
				!errors.Is(err, catalogstore.ErrSemanticInventoryClaimLost) &&
				!errors.Is(err, catalogstore.ErrSemanticInventoryEntriesUnsupported) {
				t.Fatalf("round %d: loser error = %v, want a named refusal", round, err)
			}
		}
		if completed != 1 {
			t.Fatalf("round %d: %d backfills completed, want exactly 1 (errors %v)", round, completed, errs)
		}
		build, found := s.witnessBuildRow(t, project, generation, catalog.SemanticInventoryBuildID(project, generation))
		if !found || build.State != catalog.SemanticInventoryComplete {
			t.Fatalf("round %d: build not completed: %+v found=%t", round, build, found)
		}
		for _, id := range []string{"obs-b", "obs-e"} {
			g := pgcat05Goldens[id]
			want := pgcat05ExpectedLiteral(g)
			want.Project = project
			want.DatasetGeneration = catalog.NormalizeDatasetGeneration(generation)
			want.BuildID = catalog.SemanticInventoryBuildID(project, generation)
			emitted := pgcat05EmitContribution(t, project, generation, g.authPath, g.resourceType, g.sourceID, g.payload)
			if len(emitted) != 1 {
				t.Fatalf("round %d: emitter drift for %s", round, id)
			}
			want.Key = emitted[0].Key
			preparedDigest, _, prepErr := prepareContributionTest(emitted[0])
			if prepErr != nil {
				t.Fatalf("round %d: prepare: %v", round, prepErr)
			}
			want.PayloadDigest = preparedDigest
			w, found := s.witnessContribution(t, want.Key)
			if !found || !sameContributionLiteral(w.contributionLiteral, want) {
				t.Fatalf("round %d: %s converged to %+v found=%t, want %+v", round, id, w.contributionLiteral, found, want)
			}
		}
	}
}

func pgcat05Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestZZZeroLeftoverSemanticContributionFixtures(t *testing.T) {
	raw := pgcat05TestPostgresURL(t)
	pgcat05OwnedMu.Lock()
	owned := make(map[string]string, len(pgcat05OwnedByName))
	for db, schema := range pgcat05OwnedByName {
		owned[db] = schema
	}
	pgcat05OwnedMu.Unlock()
	if len(owned) == 0 {
		t.Skip("this process created no verify_pgcat05_it_* fixtures")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect to test service for audit: %v", err)
	}
	defer admin.Close()

	for db, schema := range owned {
		if !strings.HasPrefix(db, "verify_pgcat05_it_") {
			t.Fatalf("registry holds foreign database %q", db)
		}
		var count int
		if err := admin.QueryRow(ctx, "SELECT COUNT(*) FROM pg_database WHERE datname = $1", db).Scan(&count); err != nil {
			t.Errorf("audit owned database %s: %v", db, err)
			continue
		}
		if count != 0 {
			t.Errorf("leftover owned database %s after all cleanups", db)
			if schema != "" {
				u, err := url.Parse(raw)
				if err == nil {
					if q := u.Query(); q.Get("dbname") != "" {
						q.Del("dbname")
						u.RawQuery = q.Encode()
					}
					u.Path = "/" + db
					if probe, err := pgxpool.New(ctx, u.String()); err == nil {
						var schemaCount int
						if err := probe.QueryRow(ctx, "SELECT COUNT(*) FROM pg_namespace WHERE nspname = $1", schema).Scan(&schemaCount); err != nil {
							t.Errorf("audit owned schema %s in %s: %v", schema, db, err)
						} else if schemaCount != 0 {
							t.Errorf("leftover owned schema %s in leftover database %s", schema, db)
						}
						probe.Close()
					}
				}
			}
		}
	}

	var prefixLeftovers int
	if err := admin.QueryRow(ctx,
		"SELECT COUNT(*) FROM pg_database WHERE datname LIKE 'verify_pgcat05_it_%'").Scan(&prefixLeftovers); err != nil {
		t.Fatalf("prefix sweep: %v", err)
	}
	if prefixLeftovers != 0 {
		t.Errorf("%d verify_pgcat05_it_* databases survive all cleanups", prefixLeftovers)
	}
}

func TestSemanticContributionLargeBatchClampsChunkCeiling(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	const rows = 5000
	var goldenObs catalog.SemanticObservation
	if err := json.Unmarshal([]byte(pgcat05ObservationCodeObs), &goldenObs); err != nil {
		t.Fatalf("decode shared observation template: %v", err)
	}
	const (
		project    = "pgcat05-large-batch"
		generation = "gen-large"
	)
	buildID := catalog.SemanticInventoryBuildID(project, generation)

	batch := make([]catalog.SemanticInventoryContribution, 0, rows)
	for i := 0; i < rows; i++ {
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("pgcat05-large/%d", i))))
		batch = append(batch, catalog.SemanticInventoryContribution{
			Key:               key,
			Project:           project,
			DatasetGeneration: generation,
			BuildID:           buildID,
			SourceKind:        catalog.SemanticInventorySourceRetained,
			ResourceType:      "Observation",
			SourceID:          fmt.Sprintf("retained:Observation/large-%05d", i),
			Ordinal:           0,
			ConceptID:         fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("concept/%d", i)))),
			BindingID:         fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("binding/%d", i)))),
			Example:           fmt.Sprintf("example-%d", i),
			Observation:       goldenObs,
		})
	}

	if err := repo.WriteSemanticInventoryContributions(s.ctx, batch, 5000); err != nil {
		t.Fatalf("contract-maximum batch of %d rows: %v", rows, err)
	}
	var count int64
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.contributionFQN()+" WHERE project = $1", project).Scan(&count); err != nil {
		s.fail("count large-batch rows", err)
	}
	if count != rows {
		t.Fatalf("large-batch rows = %d, want %d", count, rows)
	}

	if err := repo.WriteSemanticInventoryContributions(s.ctx, batch, 5000); err != nil {
		t.Fatalf("large-batch replay: %v", err)
	}
	if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.contributionFQN()+" WHERE project = $1", project).Scan(&count); err != nil {
		s.fail("recount large-batch rows", err)
	}
	if count != rows {
		t.Fatalf("large-batch rows after replay = %d, want %d (replay must not add rows)", count, rows)
	}
	for _, i := range []int{0, 1, rows / 2, rows - 2, rows - 1} {
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("pgcat05-large/%d", i))))
		if _, found := s.witnessContribution(t, key); !found {
			t.Fatalf("row %d (chunk-crossing sample) missing after clamped write", i)
		}
	}
}

func TestSemanticContributionForgedKeyCrossSlotSkipIsClassified(t *testing.T) {
	s := newPgcat05Service(t)
	repo := s.contributionRepository(t)

	forgedKey := fmt.Sprintf("%x", sha256.Sum256([]byte("pgcat05-forged-key")))
	g := pgcat05Goldens["obs-a"]

	emitInProject := func(project, sourceID string) catalog.SemanticInventoryContribution {
		emitted := pgcat05EmitContribution(t, project, "g1", g.authPath, g.resourceType, sourceID, g.payload)
		if len(emitted) != 1 {
			t.Fatalf("%s: emitter produced %d contributions, want exactly 1", project, len(emitted))
		}
		c := emitted[0]
		c.Key = forgedKey
		return c
	}
	countIn := func(project string) int64 {
		var n int64
		if err := s.iso.QueryRow(s.ctx, "SELECT COUNT(*) FROM "+s.contributionFQN()+" WHERE project = $1", project).Scan(&n); err != nil {
			s.fail("count rows for "+project, err)
		}
		return n
	}

	t.Run("in-batch-forged-key-across-slots-refuses-with-typed-conflict", func(t *testing.T) {
		a := emitInProject("pgcat05-fk-inbatch-a", "retained:Observation/fk")
		b := emitInProject("pgcat05-fk-inbatch-b", "retained:Observation/fk")
		err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{a, b}, 0)
		var conflict *catalogstore.SemanticInventoryContributionConflictError
		if err == nil {
			w, _ := s.witnessContribution(t, forgedKey)
			t.Fatalf("SILENT DROP: two distinct-slot contributions under one forged key returned nil; "+
				"only this row stored (count-a=%d count-b=%d row=%+v) — the second payload vanished without a typed conflict",
				countIn("pgcat05-fk-inbatch-a"), countIn("pgcat05-fk-inbatch-b"), w)
		}
		if !errors.As(err, &conflict) && !errors.Is(err, catalogstore.ErrSemanticInventoryContributionConflict) {
			t.Fatalf("forged-key in-batch write returned unclassified error: %v", err)
		}
		if n := countIn("pgcat05-fk-inbatch-b"); n != 0 {
			t.Fatalf("forged-key batch wrote into the refused slot: %d rows", n)
		}
		w, found := s.witnessContribution(t, forgedKey)
		if !found {
			t.Fatal("forged-key row missing after refused in-batch write")
		}
		first := a
		if w.Project != first.Project {
			first = b
		}
		firstObs, err := json.Marshal(first.Observation)
		if err != nil {
			t.Fatalf("encode winning observation: %v", err)
		}
		if w.PayloadDigest != semanticContributionPayloadDigest(first, firstObs) {
			t.Fatalf("winning stored row digest = %s, want the winning contribution's digest", w.PayloadDigest)
		}
	})

	t.Run("cross-batch-forged-key-positive-control", func(t *testing.T) {
		otherForged := fmt.Sprintf("%x", sha256.Sum256([]byte("pgcat05-forged-key-cross-batch")))
		a := emitInProject("pgcat05-fkx-1", "retained:Observation/kx")
		a.Key = otherForged
		b := emitInProject("pgcat05-fkx-2", "retained:Observation/kx")
		b.Key = otherForged
		if err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{a}, 0); err != nil {
			t.Fatalf("first forged write: %v", err)
		}
		err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{b}, 0)
		if !errors.Is(err, catalogstore.ErrSemanticInventoryContributionConflict) {
			t.Fatalf("cross-batch forged key with differing payload = %v, want typed conflict", err)
		}
		if _, ok := s.witnessContribution(t, otherForged); !ok {
			t.Fatal("stored forged row vanished")
		}
		if n := countIn("pgcat05-fkx-2"); n != 0 {
			t.Fatalf("conflicting forged row was written: %d", n)
		}
	})

	t.Run("honest-replay-still-nil-no-false-conflict", func(t *testing.T) {
		c := pgcat05EmitContribution(t, "pgcat05-fk-replay", "g1", g.authPath, g.resourceType,
			"retained:Observation/replay", g.payload)[0]
		if err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{c}, 0); err != nil {
			t.Fatalf("first honest write: %v", err)
		}
		before, ok := s.witnessContribution(t, c.Key)
		if !ok {
			t.Fatal("honest row missing after first write")
		}
		if err := repo.WriteSemanticInventoryContributions(s.ctx, []catalog.SemanticInventoryContribution{c}, 0); err != nil {
			t.Fatalf("honest replay returned %v, want nil (offered slot is held by exactly its own key)", err)
		}
		after, ok := s.witnessContribution(t, c.Key)
		if !ok {
			t.Fatal("honest row missing after replay")
		}
		before.RecordedAt, after.RecordedAt = time.Time{}, time.Time{}
		if before != after {
			t.Fatalf("replay moved the stored row:\n before %+v\n after  %+v", before, after)
		}
	})
}

func prepareContributionTest(contribution catalog.SemanticInventoryContribution) (digest string, observation []byte, err error) {
	contribution.DatasetGeneration = catalog.NormalizeDatasetGeneration(contribution.DatasetGeneration)
	if !isLowerHex64(contribution.Key) {
		return "", nil, fmt.Errorf("contribution key %q is not 64 lowercase hex characters", contribution.Key)
	}
	if contribution.Project == "" || contribution.DatasetGeneration == "" || contribution.BuildID == "" ||
		contribution.SourceID == "" || contribution.ConceptID == "" || contribution.BindingID == "" ||
		contribution.ResourceType == "" {
		return "", nil, errors.New("contribution is missing stable identity")
	}
	if contribution.Ordinal < 0 {
		return "", nil, fmt.Errorf("contribution ordinal is %d, want >= 0", contribution.Ordinal)
	}
	switch contribution.SourceKind {
	case catalog.SemanticInventorySourceFile, catalog.SemanticInventorySourceRetained:
	default:
		return "", nil, fmt.Errorf("contribution source kind %q is unknown", contribution.SourceKind)
	}
	if contribution.BuildID != catalog.SemanticInventoryBuildID(contribution.Project, contribution.DatasetGeneration) {
		return "", nil, errors.New("contribution build does not match its generation")
	}
	observation, err = json.Marshal(contribution.Observation)
	if err != nil {
		return "", nil, fmt.Errorf("encode contribution observation: %w", err)
	}
	if len(observation) == 0 || observation[0] != '{' {
		return "", nil, errors.New("contribution observation does not encode as a JSON object")
	}
	return semanticContributionPayloadDigest(contribution, observation), observation, nil
}
func isLowerHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func semanticContributionPayloadDigest(c catalog.SemanticInventoryContribution, observation []byte) string {
	h := sha256.New()
	put := func(s string) {
		h.Write([]byte(strconv.Itoa(len(s))))
		h.Write([]byte{':'})
		h.Write([]byte(s))
	}
	put("loom.semantic-contribution/v1\n")
	put(c.Project)
	put(c.DatasetGeneration)
	put(c.BuildID)
	put(c.SourceKind)
	put(c.AuthResourcePath)
	put(c.ResourceType)
	put(c.SourceID)
	put("o" + strconv.Itoa(c.Ordinal))
	put(c.ConceptID)
	put(c.BindingID)
	put(c.Example)
	put(string(observation))
	return hex.EncodeToString(h.Sum(nil))
}
