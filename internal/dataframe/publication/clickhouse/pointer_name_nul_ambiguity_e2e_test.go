package clickhouse

// Candidate finding F-B21 e2e: BundleIdentity.PointerName (internal/dataframe/
// publication/bundle.go:88-97) is a bare "\x00"-join of
// (Project, DatasetGeneration, Name[, TranslationVersion][, OutputName]) while
// Key() (bundle.go:99-115) JSON-marshals every field distinctly. Two
// logically distinct identities
//
//	A: Name = "a\x00b", TranslationVersion = ""
//	B: Name = "a",      TranslationVersion = "b"
//
// under one project/generation/share an OutputName fold to the SAME
// PointerName ("proj\0gen\0a\0b\0out") while their Keys stay distinct. No
// control-character validation guards publication identity: the shipped
// control-char gate internal/dataset/validation.go:40-44
// (validateOpaqueIdentifier) is called ONLY from internal/dataset/ref.go:26,29
// (dataset project/generation refs) and internal/dataset/schema.go:51
// (generatedResourceTypes). The catalog layer already length-prefixes for
// exactly this attack (internal/catalog/helpers.go:50-62 comment).
//
// This test drives real Arango + real ClickHouse through production entries
// (publication.Publish -> ClickHouseBundleStore -> arango.Registry, and the
// pointer-gated published.Reader). Repair-5 (length-prefix PointerName)
// converted it to a natural PASS: the STEP-2 identity comparison now reports
// VERDICT PREVENTED before any store is touched. The full STEP 3-5 hijack
// choreography with its archived FAILED-ON-PURPOSE F-B21 CONFIRMED message
// stays wired as the REGRESSION direction: if the PointerName encoding ever
// folds the two identities again, the test fails with the original red text.
//
// Fixture necessity (honest): publication.ValidateOutputs requires a non-blank
// output name and the physical table suffix is the output name
// (bundle_store.go:908-910), so BOTH identities share one identical non-blank
// OutputName ("pnn_out_shared"). OutputName is therefore deliberately kept
// OUT of the colliding fields: only Name/TranslationVersion vary, and the
// collision needs only those two because PointerName appends them
// conditionally and joins with an unescaped "\x00".
//
// Opt-in (SKIP-clean without LOOM_TEST_ARANGO_URL + LOOM_CLICKHOUSE_URL, plus
// the LOOM_CLICKHOUSE_* vars the sibling harnesses use):
//
//	docker exec loom-dev-qwen-current-loom-api-1 sh -c \
//	  'cd /workspace && LOOM_TEST_ARANGO_URL=http://arangodb:8529 \
//	   LOOM_CLICKHOUSE_URL=http://clickhouse:8123 LOOM_CLICKHOUSE_DATABASE=loom_dev \
//	   LOOM_CLICKHOUSE_USERNAME=loom_dev LOOM_CLICKHOUSE_PASSWORD=loom_dev_password \
//	   go test -count=1 -run TestPointerNameNulAmbiguity -v ./internal/dataframe/publication/clickhouse/'

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	pubarango "github.com/calypr/loom/internal/dataframe/publication/arango"
	"github.com/calypr/loom/internal/dataframe/published"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/store/clickhouse"
	"github.com/google/uuid"
)

const pnnDatabasePrefix = "loom_verify_pnn_"

// pnnChOwnedPrefix owns this suite's ClickHouse canary marker. Production
// staging tables are named loom_bundle_<executionID-hex>_<outputName>
// (bundle_store.go:908-910); the shared output name embeds "pnn", so every
// artifact this test creates in ClickHouse matches LIKE '%pnn%'.
const pnnChOwnedPrefix = "ch_pnn"

const (
	pnnProject        = "pnn-project"
	pnnSharedOutput   = "pnn_out_shared"
	pnnRecipeDigest   = "sha256:pnn-recipe-shared"
	pnnSchemaDigest   = "sha256:pnn-schema-shared"
	pnnScopeDigest    = "sha256:pnn-scope-shared"
	pnnEngineVersion  = "pnn-engine"
	pnnNameA          = "a\x00b" // embedded NUL: the ambiguous component
	pnnNameB          = "a"      // with TranslationVersion "b" => same PointerName
	pnnTranslationB   = "b"
	pnnStreamBaseA    = 101
	pnnStreamBaseB    = 201
	pnnStreamRowCount = 3
)

// ---------------------------------------------------------------------------
// STEP 0 drift gate: quote the live production sources the claim rests on and
// fail on drift. Also negative-scans the whole publication package tree for
// any control-char validator (its presence would PREVENT the finding).
// ---------------------------------------------------------------------------

var pnnRequiredQuotations = []struct {
	file    string
	needles []string
}{
	{
		// Repair-5 re-pin: the pointer encoding must stay length-prefixed.
		// Removal of these anchors re-opens F-B21 and the STEP-2 identity
		// comparison below then falls through to the archived CONFIRMED
		// hijack assertions (the REGRESSION direction).
		file: "internal/dataframe/publication/bundle.go",
		needles: []string{
			`parts := []string{i.Project, i.DatasetGeneration, i.Name}`,
			`if strings.TrimSpace(i.TranslationVersion) != "" {`,
			`if strings.TrimSpace(i.OutputName) != "" {`,
			"name.WriteString(strconv.Itoa(len(part)))",
			`name.WriteByte(':')`,
			"name.WriteString(part)",
		},
	},
	{
		file: "internal/dataset/validation.go",
		needles: []string{
			`func validateOpaqueIdentifier(field, value string) error {`,
			`if unicode.IsControl(r) {`,
			`must not contain control characters`,
		},
	},
	{
		file: "internal/dataset/ref.go",
		needles: []string{
			`validateOpaqueIdentifier("project", r.Project)`,
			`validateOpaqueIdentifier("generation", r.Generation)`,
		},
	},
	{
		file: "internal/catalog/helpers.go",
		needles: []string{
			`catalogIdentityDigest hashes a length-prefixed sequence rather than a`,
			`binary.BigEndian.PutUint64(length[:], uint64(len(part)))`,
		},
	},
}

func pnnRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func pnnQuoteVerifiedSource(t *testing.T) (quotations string, controlCharHits []string) {
	t.Helper()
	root := pnnRepoRoot(t)
	var builder strings.Builder
	for _, entry := range pnnRequiredQuotations {
		raw, err := os.ReadFile(filepath.Join(root, entry.file))
		if err != nil {
			t.Fatalf("claim source %s unreadable: %v", entry.file, err)
		}
		lines := strings.Split(string(raw), "\n")
		builder.WriteString(fmt.Sprintf("--- %s ---\n", entry.file))
		for _, needle := range entry.needles {
			found := false
			for i, line := range lines {
				if strings.Contains(line, needle) {
					builder.WriteString(fmt.Sprintf("  %s:%d: %s\n", entry.file, i+1, strings.TrimSpace(line)))
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("claim source drift: %s no longer contains %q — re-verify F-B21 against current production before trusting any verdict", entry.file, needle)
			}
		}
	}
	// Quote the exact control-char block (validation.go:40-44 by assignment).
	controlNeedles := []string{
		`for _, r := range value {`,
		`if unicode.IsControl(r) {`,
		`return fmt.Errorf("%s must not contain control characters", field)`,
	}
	for _, needle := range controlNeedles {
		for i, line := range strings.Split(string(mustReadPnnFile(t, root, "internal/dataset/validation.go")), "\n") {
			if strings.Contains(line, needle) {
				builder.WriteString(fmt.Sprintf("  internal/dataset/validation.go:%d: %s\n", i+1, strings.TrimSpace(line)))
				break
			}
		}
	}
	// Negative scan: no control-char/identifier validator anywhere on the
	// publication path (the shipped gate lives in internal/dataset only).
	err := filepath.Walk(filepath.Join(root, "internal/dataframe/publication"), func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "validateOpaqueIdentifier") || strings.Contains(line, "unicode.IsControl") || strings.Contains(line, "must not contain control characters") {
				controlCharHits = append(controlCharHits, fmt.Sprintf("%s:%d: %s", rel, i+1, strings.TrimSpace(line)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("negative scan of publication tree failed: %v", err)
	}
	return builder.String(), controlCharHits
}

func mustReadPnnFile(t *testing.T, root, rel string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return data
}

// ---------------------------------------------------------------------------
// owned-database + owned-ClickHouse harness (SKIP-clean opt-in), mirroring
// the sibling pppOpen pattern with pnn-owned identifiers only.
// ---------------------------------------------------------------------------

type pnnHarness struct {
	ctx      context.Context
	client   *storearango.Client
	registry *pubarango.Registry
	ch       *clickhouse.Client
	database string
	suffix   string

	mu            sync.Mutex
	trackedTables []string
	canaryTable   string
}

func (h *pnnHarness) trackTable(table string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, existing := range h.trackedTables {
		if existing == table {
			return
		}
	}
	h.trackedTables = append(h.trackedTables, table)
}

func pnnOpen(t *testing.T) *pnnHarness {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("LOOM_TEST_ARANGO_URL"))
	chURL := strings.TrimSpace(os.Getenv("LOOM_CLICKHOUSE_URL"))
	if endpoint == "" || chURL == "" {
		t.Skip("set LOOM_TEST_ARANGO_URL and LOOM_CLICKHOUSE_URL to run the F-B21 pointer-name NUL ambiguity reproduction")
	}
	database := pnnDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(database, pnnDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", database, pnnDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	t.Cleanup(cancel)
	client, err := storearango.Open(ctx, endpoint, database)
	if err != nil {
		t.Fatalf("open %s: %v", database, err)
	}
	t.Logf("PNN_DATABASE=%s", database)
	// Registered FIRST so it runs LAST: ClickHouse artifacts must be dropped
	// before the Arango database disappears (LIFO cleanup order).
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		pnnDropDatabase(t, endpoint, database)
	})
	registry, err := pubarango.New(client)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := client.Bootstrap(ctx, pubarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap bundle collections: %v", err)
	}
	ch, err := clickhouse.New(clickhouse.Options{
		URL:      chURL,
		Database: os.Getenv("LOOM_CLICKHOUSE_DATABASE"),
		Username: os.Getenv("LOOM_CLICKHOUSE_USERNAME"),
		Password: os.Getenv("LOOM_CLICKHOUSE_PASSWORD"),
		Timeout:  20 * time.Second,
	})
	if err != nil {
		t.Fatalf("clickhouse client: %v", err)
	}
	if err := ch.Ping(ctx); err != nil {
		t.Fatalf("ping clickhouse: %v", err)
	}
	harness := &pnnHarness{
		ctx:      ctx,
		client:   client,
		registry: registry,
		ch:       ch,
		database: database,
		suffix:   database[len(pnnDatabasePrefix):],
	}
	// Registered second: drops every tracked ClickHouse table by exact name
	// and asserts the '%pnn%' residue gate empty before LIFO proceeds to the
	// Arango database removal.
	t.Cleanup(func() {
		harness.dropAndVerifyClickHouse(t)
	})
	harness.canaryTable = pnnChOwnedPrefix + "_canary_" + harness.suffix
	if err := ch.CreateTable(ctx, harness.canaryTable, []clickhouse.Column{{Name: "__loom_row_id", Type: "UInt64"}}); err != nil {
		t.Fatalf("create canary table %s: %v", harness.canaryTable, err)
	}
	harness.trackTable(harness.canaryTable)
	t.Logf("PNN_CLICKHOUSE_DATABASE=%s PNN_CANARY_TABLE=%s", os.Getenv("LOOM_CLICKHOUSE_DATABASE"), harness.canaryTable)
	return harness
}

func (h *pnnHarness) dropAndVerifyClickHouse(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	tables := append([]string(nil), h.trackedTables...)
	h.mu.Unlock()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, table := range tables {
		if err := h.ch.DropTable(cleanupCtx, table); err != nil {
			t.Errorf("PNN CH drop %s: %v", table, err)
		}
	}
	names, err := h.ch.QueryRowsArgs(cleanupCtx, "SELECT name FROM system.tables WHERE database = currentDatabase() AND name LIKE '%pnn%'", []string{"name"})
	if err != nil {
		t.Errorf("PNN CH residue query (name LIKE '%%pnn%%'): %v", err)
		return
	}
	if len(names) != 0 {
		t.Errorf("PNN CH residue under '%%pnn%%' is not empty: %v", names)
		return
	}
	for _, table := range tables {
		rows, err := h.ch.QueryRowsArgs(cleanupCtx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
		if err != nil {
			t.Errorf("PNN CH exact check %s: %v", table, err)
			continue
		}
		if len(rows) != 1 {
			t.Errorf("PNN CH exact check %s returned %d rows", table, len(rows))
			continue
		}
		if count, ok := pnnCount(rows[0]["c"]); !ok {
			t.Errorf("PNN CH exact check %s unreadable: %#v", table, rows[0]["c"])
		} else if count != 0 {
			t.Errorf("PNN CH residue: tracked table %s still present", table)
		}
	}
	t.Logf("PNN_CH_CLEANUP_VERIFIED dropped=%v and name LIKE '%%pnn%%' empty in system.tables", tables)
	_ = h.ch.Close()
}

func pnnDropDatabase(t *testing.T, endpoint, database string) {
	t.Helper()
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete,
		strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(database), nil)
	if err != nil {
		t.Errorf("build cleanup request for %s: %v", database, err)
		return
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Errorf("drop database %s: %v", database, err)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusForbidden {
		t.Errorf("drop database %s: status %d", database, response.StatusCode)
		return
	}
	t.Logf("PNN_DATABASE_REMOVED=%s", database)
}

// ---------------------------------------------------------------------------
// identities: the ONLY difference between A and B is the ambiguous field pair
// (Name, TranslationVersion); every other identity component is identical, so
// a Key() difference can only come from the per-field JSON marshalling and a
// PointerName equality can only come from the bare-join ambiguity.
// ---------------------------------------------------------------------------

type pnnPublication struct {
	identity publication.PublicationIdentity
	key      string
	pointer  string
	outputs  []publication.OutputStream
}

func pnnBundleIdentity(id publication.PublicationIdentity) publication.BundleIdentity {
	return publication.BundleIdentity{
		Name: id.Name, TranslationVersion: id.TranslationVersion, OutputName: id.OutputName,
		Project: id.Project, DatasetGeneration: id.DatasetGeneration,
		RecipeDigest: id.RecipeDigest, SchemaDigest: id.SchemaDigest,
		ScopeDigest: id.ScopeDigest, EngineVersion: id.EngineVersion,
		ReceiptID: id.ReceiptID,
	}
}

func pnnNewPublication(name, translationVersion, generation string, streamBase int) pnnPublication {
	identity := publication.PublicationIdentity{
		Name: name, TranslationVersion: translationVersion, OutputName: pnnSharedOutput,
		Project: pnnProject, DatasetGeneration: generation,
		RecipeDigest: pnnRecipeDigest, SchemaDigest: pnnSchemaDigest,
		ScopeDigest: pnnScopeDigest, EngineVersion: pnnEngineVersion,
	}
	base := streamBase
	outputs := []publication.OutputStream{{
		Name: pnnSharedOutput,
		Columns: []publication.LogicalColumn{{
			ID: "col-pnn-value", Name: "value", Kind: "integer", Provenance: publication.ColumnExplicit,
		}},
		Stream: func(ctx context.Context, visit func(map[string]any) error) error {
			for i := 1; i <= pnnStreamRowCount; i++ {
				if err := visit(map[string]any{"value": int64(base + i)}); err != nil {
					return err
				}
			}
			return nil
		},
	}}
	bundle := pnnBundleIdentity(identity)
	return pnnPublication{identity: identity, key: bundle.Key(), pointer: bundle.PointerName(), outputs: outputs}
}

// pnnProductionPublish is the same production entry recipe_publication.go uses.
func pnnProductionPublish(ctx context.Context, store *ClickHouseBundleStore, p pnnPublication) (publication.Result, error) {
	return publication.Publish(ctx, store, p.identity, p.outputs, publication.Limits{})
}

// ---------------------------------------------------------------------------
// direct fact readers
// ---------------------------------------------------------------------------

func pnnCount(value any) (int64, bool) {
	switch typed := value.(type) {
	case uint64:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		if typed == float64(int64(typed)) {
			return int64(typed), true
		}
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func pnnTablePresent(t *testing.T, ch *clickhouse.Client, ctx context.Context, table string) bool {
	t.Helper()
	rows, err := ch.QueryRowsArgs(ctx, "SELECT count() AS c FROM system.tables WHERE database = currentDatabase() AND name = ?", []string{"c"}, table)
	if err != nil {
		t.Fatalf("pnn system.tables check for %s: %v", table, err)
	}
	if len(rows) != 1 {
		t.Fatalf("pnn system.tables check for %s returned %d rows", table, len(rows))
	}
	count, ok := pnnCount(rows[0]["c"])
	if !ok {
		t.Fatalf("pnn system.tables count for %s unreadable: %#v", table, rows[0]["c"])
	}
	return count == 1
}

func pnnTableValues(t *testing.T, ch *clickhouse.Client, ctx context.Context, table string) []int64 {
	t.Helper()
	rows, err := ch.QueryRowsArgs(ctx, fmt.Sprintf("SELECT value AS v FROM `%s` ORDER BY value", table), []string{"v"})
	if err != nil {
		t.Fatalf("pnn value read from %s: %v", table, err)
	}
	values := make([]int64, 0, len(rows))
	for _, row := range rows {
		value, ok := pnnCount(row["v"])
		if !ok {
			t.Fatalf("pnn value row unreadable from %s: %#v", table, row)
		}
		values = append(values, value)
	}
	return values
}

// pnnIsValidationRejection reports whether a publish error is the fingerprint
// of a control-character/identifier validator blocking the NUL name (a
// PREVENTED verdict), as opposed to an unrelated infrastructure failure.
func pnnIsValidationRejection(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	for _, needle := range []string{
		"control character", "invalid dataset reference", "invalid name",
		"must not contain control", "invalid_name", "invalid character",
	} {
		if strings.Contains(strings.ToLower(text), needle) {
			return true
		}
	}
	return false
}

func pnnVerdictPrevented(t *testing.T, stage string, err error, hits []string) {
	t.Helper()
	var witness strings.Builder
	if err != nil {
		witness.WriteString(fmt.Sprintf("production publish rejected the NUL identity: %v\n", err))
	}
	for _, hit := range hits {
		witness.WriteString("preventing validation line: " + hit + "\n")
	}
	t.Logf("VERDICT PREVENTED (stage %s):\n%s", stage, witness.String())
}

// ---------------------------------------------------------------------------
// the reproduction
// ---------------------------------------------------------------------------

func TestPointerNameNulAmbiguityHijacksVisibilityPointerAgainstArango(t *testing.T) {
	// =========================================================================
	// STEP 0 — drift gate.
	// =========================================================================
	quotations, controlCharHits := pnnQuoteVerifiedSource(t)
	t.Logf("OBSERVED STEP 0 quoted verified source (anchors located live; drift fails the test):\n%s", quotations)
	if len(controlCharHits) > 0 {
		pnnVerdictPrevented(t, "step0-negative-scan", nil, controlCharHits)
		return
	}
	t.Logf("OBSERVED STEP 0 negative scan: zero validateOpaqueIdentifier/unicode.IsControl/control-character references anywhere under internal/dataframe/publication (non-test files); identity components are NOT control-char validated on the publication path")

	h := pnnOpen(t)
	ctx := h.ctx

	store, err := NewBundleStore(h.ch, h.registry)
	if err != nil {
		t.Fatalf("NewBundleStore: %v", err)
	}

	// =========================================================================
	// STEP 1/2 — build the colliding identities. OutputName is identical on
	// both (fixture necessity: the shared non-blank output keeps outputs OUT
	// of the collision; only Name/TranslationVersion vary).
	// =========================================================================
	generation := "pnn-gen-" + h.suffix
	pubA := pnnNewPublication(pnnNameA, "", generation, pnnStreamBaseA)
	pubB := pnnNewPublication(pnnNameB, pnnTranslationB, generation, pnnStreamBaseB)

	if pubA.key == pubB.key {
		t.Fatalf("precondition broken: Key() must marshal (Name, TranslationVersion) distinctly (both %s) — Key() fold regression, not F-B21", pubA.key)
	}
	if pubA.pointer != pubB.pointer {
		pnnVerdictPrevented(t, "step2-identity", nil, nil)
		t.Logf("VERDICT PREVENTED: production BundleIdentity.PointerName() keeps the two identities distinct (A=%q B=%q); the bare-join ambiguity is gone", pubA.pointer, pubB.pointer)
		return
	}
	// Mechanism evidence: equality produced by the PRODUCTION PointerName().
	t.Logf("OBSERVED STEP 2 mechanism (production BundleIdentity.PointerName, bundle.go:88-97): nameA=%q tvA=%q outA=%q | nameB=%q tvB=%q outB=%q -> PointerName A==B==%q while Key A=%s != Key B=%s",
		pubA.identity.Name, pubA.identity.TranslationVersion, pubA.identity.OutputName,
		pubB.identity.Name, pubB.identity.TranslationVersion, pubB.identity.OutputName,
		pubA.pointer, pubA.key, pubB.key)

	// =========================================================================
	// STEP 3 — publication A through production publication.Publish.
	// =========================================================================
	resultA, errA := pnnProductionPublish(ctx, store, pubA)
	if errA != nil {
		if pnnIsValidationRejection(errA) {
			pnnVerdictPrevented(t, "step3-publish-A", errA, nil)
			return
		}
		t.Fatalf("INCONCLUSIVE: publication A (Go-level identity construction, publication entry) failed with an unexpected error — name the blocking boundary: %v", errA)
	}
	if len(resultA.Outputs) != 1 {
		t.Fatalf("publication A result malformed: %+v", resultA.Outputs)
	}
	tableA := resultA.Outputs[0].PhysicalName
	h.trackTable(tableA)
	execA, err := h.registry.FindExecutionByKey(ctx, pubA.key)
	if err != nil {
		t.Fatalf("A FindExecutionByKey(%s): %v", pubA.key[:12], err)
	}
	if execA.ID == "" || execA.ID == pubA.key {
		t.Fatalf("A execution identity malformed: %+v", execA)
	}
	pointerAfterA, err := h.registry.GetPointer(ctx, pubA.pointer)
	if err != nil || pointerAfterA.ExecutionID != execA.ID {
		t.Fatalf("A precondition: pointer %q does not name exec A %s (pointer=%+v err=%v)", pubA.pointer, execA.ID, pointerAfterA, err)
	}
	if !pnnTablePresent(t, h.ch, ctx, tableA) {
		t.Fatalf("A precondition: physical table %s missing", tableA)
	}
	valuesA := pnnTableValues(t, h.ch, ctx, tableA)
	if execA.State.Canonical() != publication.BundlePublished {
		t.Fatalf("A precondition: state %q, want PUBLISHED", execA.State)
	}
	t.Logf("OBSERVED STEP 3 publish A: execA=%q state=%q pointer names execA; tableA=%q present rows=%v", execA.ID, execA.State, tableA, valuesA)

	// =========================================================================
	// STEP 4 — publication B: same pointer name, logically distinct identity.
	// =========================================================================
	resultB, errB := pnnProductionPublish(ctx, store, pubB)
	if errB != nil {
		if pnnIsValidationRejection(errB) {
			pnnVerdictPrevented(t, "step4-publish-B", errB, nil)
			return
		}
		t.Fatalf("INCONCLUSIVE: publication B failed with an unexpected error — name the blocking boundary: %v", errB)
	}
	if len(resultB.Outputs) != 1 {
		t.Fatalf("publication B result malformed: %+v", resultB.Outputs)
	}
	tableB := resultB.Outputs[0].PhysicalName
	h.trackTable(tableB)
	execB, err := h.registry.FindExecutionByKey(ctx, pubB.key)
	if err != nil {
		t.Fatalf("B FindExecutionByKey(%s): %v", pubB.key[:12], err)
	}
	if execB.State.Canonical() != publication.BundlePublished {
		t.Fatalf("B precondition: state %q, want PUBLISHED", execB.State)
	}

	// The hijack: the SAME pointer name now names execB.
	pointerAfterB, err := h.registry.GetPointer(ctx, pubA.pointer)
	if err != nil {
		t.Fatalf("Step 4 GetPointer(%q) after publishing B: %v", pubA.pointer, err)
	}
	flipped := pointerAfterB.ExecutionID == execB.ID
	execANow, err := h.registry.GetExecution(ctx, execA.ID)
	if err != nil {
		t.Fatalf("Step 4 GetExecution(execA %s): %v", execA.ID, err)
	}
	aStillPublished := execANow.State.Canonical() == publication.BundlePublished
	aNoLongerAdvertised := pointerAfterB.ExecutionID != execA.ID
	valuesB := pnnTableValues(t, h.ch, ctx, tableB)
	t.Logf("OBSERVED STEP 4 publish B: execB=%q; GetPointer(<samePointerName>) executionId=%q flipped-to-B=%v; execA still PUBLISHED=%v (state=%q) but no longer pointer-advertised=%v; tableB=%q rows=%v",
		execB.ID, pointerAfterB.ExecutionID, flipped, aStillPublished, execANow.State, aNoLongerAdvertised, tableB, valuesB)

	// =========================================================================
	// STEP 5 — reader cross-check on the production pointer-gated path.
	// published.Reader.CurrentProjectDatasets gates every serving decision on
	// GetPointer(execution.PointerName()).ExecutionID == execution.ID
	// (internal/dataframe/published/current.go:158-164).
	// =========================================================================
	reader := &published.Reader{
		Catalog: h.registry,
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	datasets, readerErr := reader.CurrentProjectDatasets(ctx, pnnProject)
	if readerErr != nil {
		t.Fatalf("Step 5 production reader CurrentProjectDatasets(%q): %v", pnnProject, readerErr)
	}
	served := make([]string, 0, len(datasets))
	for _, dataset := range datasets {
		served = append(served, fmt.Sprintf("%s(rows=%d)", dataset.PhysicalTable, dataset.RowCount))
	}
	onlyB := len(datasets) == 1 && datasets[0].PhysicalTable == tableB
	t.Logf("OBSERVED STEP 5a pointer-gated project reader exposes exactly %v (only-B=%v); PUBLISHED execA (%d rows in %s) is unservable", served, onlyB, len(valuesA), tableA)

	// Selector resolution of A's logical name via the production reader.
	selectorA := publication.DataframeSelector{Recipe: pnnNameA, TranslationVersion: "", Output: pnnSharedOutput}
	selectorB := publication.DataframeSelector{Recipe: pnnNameB, TranslationVersion: pnnTranslationB, Output: pnnSharedOutput}
	matA, errAQuery := reader.CurrentProjectDataset(ctx, pnnProject, selectorA)
	t.Logf("OBSERVED STEP 5b reader.CurrentProjectDataset(selectorA recipe=%q tv=%q output=%q): materialization.table=%q err=%v (DataframeSelector.Validate requires non-blank translationVersion, so A's empty-TV logical name is not a valid reader selector; the resolvable query in this namespace returns B)",
		pnnNameA, selectorA.TranslationVersion, pnnSharedOutput, matA.PhysicalTable, errAQuery)
	matB, errBQuery := reader.CurrentProjectDataset(ctx, pnnProject, selectorB)
	if errBQuery != nil || matB.PhysicalTable != tableB {
		t.Fatalf("Step 5b broken: reader must resolve the B selector to tableB (table=%q err=%v)", matB.PhysicalTable, errBQuery)
	}
	// Registry evidence for the name-latest selector (no pointer filter).
	selExecB, selOutB, selErr := h.registry.FindExecutionBySelector(ctx, pnnProject, generation, selectorB)
	_, _, selErrA := h.registry.FindExecutionBySelector(ctx, pnnProject, generation, selectorA)
	t.Logf("OBSERVED STEP 5c name-latest selector: B-selector->exec %q physical %q err=%v; A-selector rejected by selector.Validate (empty TV) err=%v", selExecB.ID, selOutB.PhysicalTable, selErr, selErrA)

	// The direct harm chain: resolving the shared pointer NAME (the visibility
	// name the first dataset was advertised under) via production GetPointer
	// yields B's execution and B's physical table with B's rows.
	hijackedExec, err := h.registry.GetExecution(ctx, pointerAfterB.ExecutionID)
	if err != nil {
		t.Fatalf("Step 5 resolution-chain GetExecution(%q): %v", pointerAfterB.ExecutionID, err)
	}
	hijackedTable := ""
	if len(hijackedExec.Outputs) == 1 {
		hijackedTable = hijackedExec.Outputs[0].PhysicalTable
	}
	hijackedValues := []int64(nil)
	if hijackedTable != "" {
		hijackedValues = pnnTableValues(t, h.ch, ctx, hijackedTable)
	}
	hijackChainHarm := hijackedTable == tableB && len(hijackedValues) == pnnStreamRowCount && hijackedValues[0] == int64(pnnStreamBaseB+1)
	t.Logf("OBSERVED STEP 5d resolution chain: GetPointer(<pointerName>).executionId=%q -> physical table %q rows=%v (B's rows=%v served where A's rows=%v were advertised) chainHarm=%v",
		pointerAfterB.ExecutionID, hijackedTable, hijackedValues, valuesB, valuesA, hijackChainHarm)

	// =========================================================================
	// VERDICT
	// =========================================================================
	if flipped && aStillPublished && aNoLongerAdvertised && onlyB && hijackChainHarm {
		t.Fatalf("FAILED-ON-PURPOSE F-B21 CONFIRMED: production BundleIdentity.PointerName() (bundle.go:88-97) folded Name=%q/TV=%q and Name=%q/TV=%q to the SAME visibility pointer %q (Keys %s/%s distinct via Key() at :99-115). The shipped publication path carries NO control-char validation on identity components (negative scan clean; the shipped gate internal/dataset/validation.go:40-44 wraps only dataset refs at internal/dataset/ref.go:26,29), so publishing the second identity silently repointed the first dataset's readers: GetPointer flipped execA %q -> execB %q, execA remains state=%q PUBLISHED but the pointer-gated production reader (published/current.go:158-164) serves ONLY %q (B's rows %v where A's rows %v were advertised). Intra-project namespace hijack; contrast the length-prefix defense the catalog already uses (internal/catalog/helpers.go:50-62).",
			pnnNameA, "", pnnNameB, pnnTranslationB, pubA.pointer, pubA.key[:12], pubB.key[:12], execA.ID, execB.ID, execANow.State, tableB, hijackedValues, valuesA)
	}
	t.Fatalf("INCONCLUSIVE: mechanism equality survived (PointerNames equal, Keys distinct) but the hijack assertions did not all hold: flipped=%v aStillPublished=%v aNoLongerAdvertised=%v readerOnlyB=%v chainHarm=%v — inspect OBSERVED lines; do not read this as prevented",
		flipped, aStillPublished, aNoLongerAdvertised, onlyB, hijackChainHarm)
}
