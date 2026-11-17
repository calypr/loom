package control

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	catalogstore "github.com/calypr/loom/internal/backends/postgres/internal/catalog"
	"github.com/calypr/loom/internal/catalog"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRuntimeCatalogSatisfiesCapabilityEvidenceReader(t *testing.T) {
	s := newCTLService(t, true)
	runtime := s.runtime

	full := reflect.ValueOf(runtime.Catalog).Pointer()
	for name, port := range map[string]any{
		"Inventory":     runtime.Inventory,
		"FieldProfiles": runtime.FieldProfiles,
		"Relationships": runtime.Relationships,
	} {
		if port == nil || reflect.ValueOf(port).IsNil() {
			t.Fatalf("runtime.%s is nil with the source series assigned", name)
		}
		if reflect.ValueOf(port).Pointer() != full {
			t.Fatalf("runtime.%s is not the same Store as runtime.Catalog", name)
		}
	}

	unrestricted := catalog.ExplicitAuthResourcePathsUnrestricted(false)
	opts := catalog.CapabilityEvidenceOptions{
		Project: crProject, DatasetGeneration: crGenA,
		AuthResourcePaths:             []string{"/fhir/clinical"},
		AuthResourcePathsUnrestricted: unrestricted,
	}

	evidence, err := func() (evidence catalog.CapabilityEvidence, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ReadCapabilityEvidence(runtime.Catalog) panicked on the migrated schema: %v", rec)
			}
		}()
		return catalog.ReadCapabilityEvidence(s.ctx, runtime.Catalog, opts)
	}()
	if err == nil {
		t.Fatal("a migrated-but-unloaded generation must not report a complete aggregate")
	}
	if !errors.Is(err, catalogstore.ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("inventory leg must refuse with the typed build-unavailable error, got %v", err)
	}
	if evidence.ResourceInventory.Available || evidence.ResourceInventory.Status != catalog.EvidenceUnavailable {
		t.Fatalf("inventory leg posture = available=%v status=%v, want typed unavailable",
			evidence.ResourceInventory.Available, evidence.ResourceInventory.Status)
	}
	if !evidence.Relationships.Available || len(evidence.Relationships.Values) != 0 {
		t.Fatalf("relationship leg on an empty schema = available=%v values=%d, want available/empty",
			evidence.Relationships.Available, len(evidence.Relationships.Values))
	}
	if evidence.FieldEnrichment.Available || evidence.FieldEnrichment.Status != catalog.EvidenceUnavailable {
		t.Fatalf("enrichment leg posture = available=%v status=%v, want typed unavailable",
			evidence.FieldEnrichment.Available, evidence.FieldEnrichment.Status)
	}

	s.writeProfiles(t)
	evidence, err = func() (evidence catalog.CapabilityEvidence, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ReadCapabilityEvidence(runtime.Catalog) panicked after seeding: %v", rec)
			}
		}()
		return catalog.ReadCapabilityEvidence(s.ctx, runtime.Catalog, opts)
	}()
	if err == nil || !errors.Is(err, catalogstore.ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("only the inventory leg may still refuse after seeding, got %v", err)
	}
	if !evidence.FieldEnrichment.Available || len(evidence.FieldEnrichment.Values) == 0 {
		t.Fatalf("enrichment leg must answer through the composed store: available=%v values=%d",
			evidence.FieldEnrichment.Available, len(evidence.FieldEnrichment.Values))
	}
}

func TestRuntimeCatalogSatisfiesCapabilityEvidenceReaderSixSeries(t *testing.T) {
	s := newCTLServiceSixSeries(t)
	runtime := s.runtime
	if runtime.Catalog == nil {
		t.Fatal("six-series runtime must assemble Catalog")
	}
	unrestricted := catalog.ExplicitAuthResourcePathsUnrestricted(false)
	opts := catalog.CapabilityEvidenceOptions{
		Project: crProject, DatasetGeneration: crGenA,
		AuthResourcePaths:             []string{"/fhir/clinical"},
		AuthResourcePathsUnrestricted: unrestricted,
	}
	evidence, err := func() (evidence catalog.CapabilityEvidence, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ReadCapabilityEvidence(runtime.Catalog) panicked on the six-series runtime: %v", rec)
			}
		}()
		return catalog.ReadCapabilityEvidence(s.ctx, runtime.Catalog, opts)
	}()
	if !errors.Is(err, catalogstore.ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("six-series aggregate must refuse with the typed build-unavailable error, got %v", err)
	}
	if evidence.ResourceInventory.Available || evidence.ResourceInventory.Status != catalog.EvidenceUnavailable {
		t.Fatalf("inventory leg posture = available=%v status=%v, want typed unavailable",
			evidence.ResourceInventory.Available, evidence.ResourceInventory.Status)
	}
	if !evidence.Relationships.Available || len(evidence.Relationships.Values) != 0 {
		t.Fatalf("relationship leg on an empty six-series schema = available=%v values=%d, want available/empty",
			evidence.Relationships.Available, len(evidence.Relationships.Values))
	}
	if evidence.FieldEnrichment.Available || evidence.FieldEnrichment.Status != catalog.EvidenceUnavailable {
		t.Fatalf("enrichment leg posture = available=%v status=%v, want typed unavailable",
			evidence.FieldEnrichment.Available, evidence.FieldEnrichment.Status)
	}
	s.writeProfiles(t)
	evidence, err = func() (evidence catalog.CapabilityEvidence, err error) {
		defer func() {
			if rec := recover(); rec != nil {
				t.Fatalf("ReadCapabilityEvidence(runtime.Catalog) panicked after seeding: %v", rec)
			}
		}()
		return catalog.ReadCapabilityEvidence(s.ctx, runtime.Catalog, opts)
	}()
	if err == nil || !errors.Is(err, catalogstore.ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("only the inventory leg may still refuse after seeding, got %v", err)
	}
	if !evidence.FieldEnrichment.Available || len(evidence.FieldEnrichment.Values) == 0 {
		t.Fatalf("six-series enrichment leg must answer through the composed store: available=%v values=%d",
			evidence.FieldEnrichment.Available, len(evidence.FieldEnrichment.Values))
	}
}

func TestRuntimeWithoutSourceSeriesKeepsNilPorts(t *testing.T) {
	s := newCTLService(t, false)
	runtime := s.runtime
	if runtime.Catalog != nil {
		t.Fatal("Catalog must stay nil without the source series")
	}
	if runtime.Inventory != nil || runtime.FieldProfiles != nil || runtime.Relationships != nil {
		t.Fatal("narrow catalog ports must stay nil without the source series")
	}
}

func newCTLServiceSixSeries(t *testing.T) *ctlService {
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
		str := fmt.Sprintf("%v", v)
		for _, secret := range secrets {
			if secret != "" {
				str = strings.ReplaceAll(str, secret, "<redacted>")
			}
		}
		return str
	}
	fail := func(step string, err error) { t.Fatalf("%s: %s", step, scrub(err)) }

	ctx, cancel := context.WithTimeout(context.Background(), 240*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		fail("connect to test service", err)
	}
	t.Cleanup(admin.Close)

	suffix := fmt.Sprintf("%x_%s", time.Now().UnixNano(), ctlRandomHex(t)[:8])
	dbName := "pgctl6_it_" + suffix
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+dbName+`"`); err != nil {
		fail("create isolated database", err)
	}
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
	t.Cleanup(iso.Close)
	schemas := SeriesSchemas{}
	for _, series := range AllSeries() {
		schema := "pgctl6_it_" + suffix + "_" + string(series)
		if _, err := iso.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
			fail("create owned schema "+string(series), err)
		}
		schemas[series] = schema
	}

	runtime, err := Open(ctx, Config{
		DSN: dsn, MaxConns: 4, MinConns: 0, AcquireTimeout: 30 * time.Second,
		Schemas: schemas, Setup: SetupMigrate,
		ApplicationName: ApplicationNamePrefix + "verify-capcompose-ctl6",
	})
	if err != nil {
		fail("open six-series control runtime", err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return &ctlService{ctx: ctx, runtime: runtime, scrub: scrub, fail: fail}
}
