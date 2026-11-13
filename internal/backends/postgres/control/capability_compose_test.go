// Runtime composition regression (defect PG-PG-CAPABILITY-EVIDENCE-
// COMPOSITION-PANIC): the value the server hands the Explorer capability
// resolver (Runtime.Catalog) must complete all three
// catalog.ReadCapabilityEvidence reads without a nil dereference, and must
// be the same assembled Store behind every source-series catalog port.
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

// TestRuntimeCatalogSatisfiesCapabilityEvidenceReader is the regression the
// benchmark session asked for: on a runtime opened with the source series
// assigned (the block that composes the catalog ports),
// ReadCapabilityEvidence(runtime.Catalog, ...) never panics and every leg
// answers with a typed posture. The source-series block is the only place
// these ports are assembled, so the two-series harness (source +
// dataset_release) exercises the full composition law.
func TestRuntimeCatalogSatisfiesCapabilityEvidenceReader(t *testing.T) {
	s := newCTLService(t, true)
	runtime := s.runtime

	// One assembled value behind all four catalog ports.
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

	// Fresh migrated schema: every leg must answer typed, never panic.
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
	// Inventory: no published build yet -> typed unavailable sentinel.
	if !errors.Is(err, catalogstore.ErrResourceInventoryBuildUnavailable) {
		t.Fatalf("inventory leg must refuse with the typed build-unavailable error, got %v", err)
	}
	if evidence.ResourceInventory.Available || evidence.ResourceInventory.Status != catalog.EvidenceUnavailable {
		t.Fatalf("inventory leg posture = available=%v status=%v, want typed unavailable",
			evidence.ResourceInventory.Available, evidence.ResourceInventory.Status)
	}
	// Relationships: the scan runs and legitimately answers empty.
	if !evidence.Relationships.Available || len(evidence.Relationships.Values) != 0 {
		t.Fatalf("relationship leg on an empty schema = available=%v values=%d, want available/empty",
			evidence.Relationships.Available, len(evidence.Relationships.Values))
	}
	// Field enrichment: never persisted -> typed unavailable (this is the
	// leg that panicked before the composition fix).
	if evidence.FieldEnrichment.Available || evidence.FieldEnrichment.Status != catalog.EvidenceUnavailable {
		t.Fatalf("enrichment leg posture = available=%v status=%v, want typed unavailable",
			evidence.FieldEnrichment.Available, evidence.FieldEnrichment.Status)
	}

	// Write profiles through the runtime's own port: the enrichment leg
	// must flip to available on the SAME Catalog value -- proof that the
	// capability reader carries the profile repository, not just refuses.
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

// TestRuntimeCatalogSatisfiesCapabilityEvidenceReaderSixSeries is the
// benchmark's literal ask (coordinator ACK condition 2): the value handed
// to newExplorerCapabilityResolver completes all three ReadCapabilityEvidence
// reads, with typed per-leg statuses and no panic, on a runtime opened with
// ALL SIX series assigned -- the exact placement the server and the
// benchmark deploy.
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
	// Migrated but unloaded: every leg answers typed; none panics. The
	// inventory sentinel plus available-empty relationships and typed
	// unavailable enrichment is the honest posture (the legs that panicked
	// pre-fix are the relationships and enrichment ones).
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
	// Through the runtime's own write port, the enrichment leg flips to
	// available on the same six-series Catalog value.
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

// TestRuntimeWithoutSourceSeriesKeepsNilPorts pins the other half of the
// posture law: a genuinely unassigned source series leaves the catalog
// ports nil for the server's typed configuration refusal -- the runtime
// must not fabricate a half-wired store, and consumers must never receive
// a value that panics.
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

// newCTLServiceSixSeries mints one isolated database with all six series
// schemas (pgctl_it_* namespace, same drop-and-audit discipline as
// newCTLService) and opens the runtime with every series assigned under
// SetupMigrate. It exists for the benchmark-shaped placement: the server's
// capability resolver always rides the six-series runtime, not the
// two-series minimum.
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
