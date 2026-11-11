// Control-plane catalog-read port witnesses (pg-runtime-first-query-route,
// Ask C). The runtime is opened for real against LOOM_TEST_POSTGRES_URL on
// a freshly minted isolated database with two owned series schemas
// (pgctl_it_* namespace, registered names dropped by enumerated name and
// audited to zero). The field profiles arrive through the runtime's own
// catalog write port -- the production write path -- never by hand-SQL.
package control

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	pgstore "github.com/calypr/loom/internal/backends/postgres/internal/store"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	crProject = "ctl-p1"
	crGenA    = "generation-a"
)

type ctlService struct {
	ctx     context.Context
	runtime *Runtime
	scrub   func(any) string
	fail    func(string, error)
}

func ctlTestPostgresURL(t *testing.T) string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("LOOM_TEST_POSTGRES_URL"))
	if raw == "" {
		t.Skip("set LOOM_TEST_POSTGRES_URL to run the PostgreSQL control catalog read tests")
	}
	return raw
}

func ctlRandomHex(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("generate isolation suffix: %v", err)
	}
	return fmt.Sprintf("%x", b[:])
}

// newCTLService mints the isolated database and both owned series schemas,
// then opens the control runtime with the migrate policy so every table is
// the runtime's own migration output. The audit binds the exact owned
// database name: it must be gone after cleanup (schemas live inside it).
func newCTLService(t *testing.T, assignSource bool) *ctlService {
	t.Helper()
	raw := ctlTestPostgresURL(t)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatal("LOOM_TEST_POSTGRES_URL must be a postgres:// or postgresql:// URL")
	}
	secrets := []string{raw}
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
	fail := func(step string, err error) { t.Fatalf("%s: %s", step, scrub(err)) }

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	suffix := fmt.Sprintf("%d_%s", time.Now().UnixNano(), ctlRandomHex(t))
	dbName := "pgctl_it_" + suffix
	srcSchema := "pgctl_it_" + suffix + "_src"
	relSchema := "pgctl_it_" + suffix + "_rel"

	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
	// Drop first, then audit: the assertion witnesses that the drop of the
	// registered name converged -- the mint is gone by enumerated name.
	t.Cleanup(func() {
		if _, err := admin.Exec(context.WithoutCancel(ctx),
			`DROP DATABASE IF EXISTS "`+dbName+`" WITH (FORCE)`); err != nil {
			t.Errorf("drop isolated database: %s", scrub(err))
		}
		var count int
		if err := admin.QueryRow(context.WithoutCancel(ctx),
			"SELECT COUNT(*) FROM pg_database WHERE datname = $1", dbName).Scan(&count); err != nil {
			t.Errorf("audit isolated database: %s", scrub(err))
		} else if count != 0 {
			t.Errorf("leftover isolated database %s after cleanup", dbName)
		}
	})

	if q := u.Query(); q.Get("dbname") != "" {
		q.Del("dbname")
	}
	u.Path = "/" + dbName
	dsn := u.String()
	secrets = append(secrets, dsn)

	iso, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fail("connect to isolated database", err)
	}
	defer iso.Close()
	for _, schema := range []string{srcSchema, relSchema} {
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			fail("create owned schema "+schema, err)
		}
	}

	schemas := SeriesSchemas{SeriesDatasetRelease: relSchema}
	if assignSource {
		schemas[SeriesSource] = srcSchema
	}
	runtime, err := Open(ctx, Config{
		DSN: dsn, MaxConns: 4, MinConns: 0, AcquireTimeout: 30 * time.Second,
		Schemas: schemas, Setup: SetupMigrate,
		ApplicationName: ApplicationNamePrefix + "verify-qroute-ctl",
	})
	if err != nil {
		fail("open control runtime", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return &ctlService{ctx: ctx, runtime: runtime, scrub: scrub, fail: fail}
}

func ctlFieldDoc(path, authPath, resourceType string, docCount int64) catalog.FieldCatalogDocument {
	complete := true
	return catalog.FieldCatalogDocument{
		Key:     resourceType + "|" + path,
		Project: crProject, DatasetGeneration: crGenA, AuthResourcePath: authPath,
		ResourceType: resourceType, Path: path, Kind: "scalar",
		DocCount: docCount, EnrichmentComplete: &complete,
	}
}

// writeProfiles stages the field evidence through the runtime's own
// catalog write port -- the same entry a generation load drives.
func (s *ctlService) writeProfiles(t *testing.T) {
	t.Helper()
	if s.runtime.FieldProfiles == nil {
		t.Fatal("runtime assembled without the field-profile write port")
	}
	batches := []struct {
		scope pgstore.FieldProfileScope
		docs  []catalog.FieldCatalogDocument
	}{
		{pgstore.FieldProfileScope{Project: crProject, DatasetGeneration: crGenA,
			AuthResourcePath: "/fhir/clinical", ResourceType: "Patient"},
			[]catalog.FieldCatalogDocument{
				ctlFieldDoc("gender", "/fhir/clinical", "Patient", 3),
				ctlFieldDoc("birthDate", "/fhir/clinical", "Patient", 3),
			}},
		{pgstore.FieldProfileScope{Project: crProject, DatasetGeneration: crGenA,
			AuthResourcePath: "/fhir/finance", ResourceType: "Observation"},
			[]catalog.FieldCatalogDocument{
				ctlFieldDoc("status", "/fhir/finance", "Observation", 9),
			}},
	}
	for _, batch := range batches {
		if err := s.runtime.FieldProfiles.WriteFieldProfile(s.ctx, batch.scope, batch.docs, 100); err != nil {
			s.fail("write field profile "+batch.scope.ResourceType, err)
		}
	}
	// Truncated-empty marker scope: only a profile_status row; its path
	// must still surface in the auth-path listing (reference COLLECT law).
	completeFalse, truncatedFalse := false, false
	markerScope := pgstore.FieldProfileScope{
		Project: crProject, DatasetGeneration: crGenA,
		AuthResourcePath: "/fhir/research", ResourceType: "Patient",
	}
	err := s.runtime.FieldProfiles.WriteFieldProfile(s.ctx, markerScope, []catalog.FieldCatalogDocument{{
		Key: "Patient|status", RecordType: catalog.FieldProfileStatusRecordType,
		Project: crProject, DatasetGeneration: crGenA, AuthResourcePath: "/fhir/research",
		ResourceType: "Patient", EnrichmentComplete: &completeFalse, EnrichmentTruncated: truncatedFalse,
	}}, 100)
	if err != nil {
		s.fail("write marker field profile", err)
	}
}

func TestRuntimeCatalogReadsOverSourceAssignment(t *testing.T) {
	s := newCTLService(t, true)
	s.writeProfiles(t)

	fields, err := s.runtime.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: crProject, DatasetGeneration: crGenA, ResourceType: "Patient",
		AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if err != nil {
		s.fail("runtime discover fields", err)
	}
	if len(fields) != 2 || fields[0].Path != "birthDate" || fields[1].Path != "gender" {
		t.Fatalf("runtime discovery = %#v, want the two Patient fields in declared order", fields)
	}
	for _, f := range fields {
		if f.Project != crProject || f.DatasetGeneration != crGenA || f.ResourceType != "Patient" || f.Kind != "scalar" {
			t.Fatalf("runtime discovery lost identity on %s: %+v", f.Path, f)
		}
	}

	restricted, err := s.runtime.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: crProject, DatasetGeneration: crGenA, ResourceType: "Patient",
		AuthResourcePaths: []string{"/fhir/finance"},
	})
	if err != nil {
		s.fail("restricted runtime discovery", err)
	}
	if len(restricted) != 0 {
		t.Fatalf("restricted runtime discovery leaked rows: %#v", restricted)
	}

	paths, err := s.runtime.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: crProject, DatasetGeneration: crGenA,
	})
	if err != nil {
		s.fail("runtime auth-path listing", err)
	}
	if want := []string{"/fhir/clinical", "/fhir/finance", "/fhir/research"}; !reflect.DeepEqual(paths, want) {
		t.Fatalf("runtime auth paths = %v, want %v", paths, want)
	}

	_, err = s.runtime.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: crProject, AllGenerations: true,
	})
	if !errors.Is(err, catalogstore.ErrAllGenerationsUnsupported) {
		t.Fatalf("all-generations runtime refusal = %v, want %v", err, catalogstore.ErrAllGenerationsUnsupported)
	}
}

func TestRuntimeCatalogReadsRefusedWithoutSourceSeries(t *testing.T) {
	s := newCTLService(t, false)
	_, err := s.runtime.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: crProject, AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	})
	if !errors.Is(err, ErrSourceSeriesUnassigned) {
		t.Fatalf("unassigned-source DiscoverFields error = %v, want %v", err, ErrSourceSeriesUnassigned)
	}
	if text := err.Error(); strings.Contains(text, "postgres://") || strings.Contains(text, "postgresql://") || strings.Contains(text, "@") {
		t.Fatalf("configuration refusal carries connection material: %q", text)
	}
	if _, err := s.runtime.DiscoverExistingAuthResourcePaths(s.ctx, catalog.AuthResourcePathOptions{
		Project: crProject,
	}); !errors.Is(err, ErrSourceSeriesUnassigned) {
		t.Fatalf("unassigned-source listing error = %v, want %v", err, ErrSourceSeriesUnassigned)
	}
}

func TestRuntimeCatalogReadsAfterCloseTyped(t *testing.T) {
	s := newCTLService(t, true)
	if err := s.runtime.Close(); err != nil {
		s.fail("close runtime", err)
	}
	if _, err := s.runtime.DiscoverFields(s.ctx, catalog.PopulatedFieldOptions{
		Project: crProject, AuthResourcePathsUnrestricted: catalog.ExplicitAuthResourcePathsUnrestricted(true),
	}); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("closed-runtime DiscoverFields error = %v, want %v", err, ErrRuntimeClosed)
	}
}
