package arango

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataset"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

// These opt-in tests reproduce the registry-level discriminator gap behind
// candidate F-B6 against a real ArangoDB in a fully owned database:
//
//	Registry.FindExecutionBySelector (bundle_registry.go:174-182) filters only
//	project/generation/name/translationVersion/output plus state PUBLISHED|READY
//	and returns publishedAt-DESC LIMIT 1. It never binds Key, RecipeDigest,
//	SchemaDigest, or ReceiptID, so two PUBLISHED executions for the same
//	selector are indistinguishable to the verifier path
//	(internal/server/release_verifier.go:18-35 -> dataset/release.go:181-190),
//	which echoes whatever execution the finder returned.
//
// Databases use the loom_verify_nlb_ prefix so this suite's residue is
// checkable independently of other verification workers.

const nameBindingDatabasePrefix = "loom_verify_nlb_"

const nameBindingSortAQLMarker = "SORT doc.publishedAt DESC"

type nameBindingExecSpec struct {
	id                 string
	key                string
	project            string
	generation         string
	recipe             string
	translationVersion string
	output             string
	recipeDigest       string
	schemaDigest       string
	receiptID          string
	publishedAt        time.Time
}

type nameBindingRecorder struct {
	*store.Client
	mu      sync.Mutex
	queries []nameBindingQuery
}

type nameBindingQuery struct {
	aql      string
	bindVars map[string]interface{}
}

func (c *nameBindingRecorder) QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit store.RowVisitor) error {
	copied := make(map[string]interface{}, len(bindVars))
	for key, value := range bindVars {
		copied[key] = value
	}
	c.mu.Lock()
	c.queries = append(c.queries, nameBindingQuery{aql: query, bindVars: copied})
	c.mu.Unlock()
	return c.Client.QueryRows(ctx, query, batchSize, bindVars, visit)
}

func (c *nameBindingRecorder) selectorQueries() []nameBindingQuery {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []nameBindingQuery
	for _, recorded := range c.queries {
		if strings.Contains(recorded.aql, nameBindingSortAQLMarker) {
			out = append(out, recorded)
		}
	}
	return out
}

func (c *nameBindingRecorder) lastSelectorQuery(t *testing.T) nameBindingQuery {
	t.Helper()
	records := c.selectorQueries()
	if len(records) == 0 {
		t.Fatalf("no FindExecutionBySelector AQL was recorded; recorded queries = %d", len(c.queries))
	}
	return records[len(records)-1]
}

// nameBindingOpen mirrors the owned-database harness used by
// internal/explorer/arango/selections_cleanup_e2e_test.go: opt-in via
// LOOM_TEST_ARANGO_URL, uuid-suffixed name under the suite prefix, created via
// store.Open (client.go:111-151 creates the database when absent), dropped by
// HTTP DELETE /_api/database/<name> in t.Cleanup.
func nameBindingOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *Registry, *nameBindingRecorder) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := nameBindingDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, nameBindingDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, nameBindingDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("NLB_DATABASE=%s", name)
	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", name, err)
			return
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Errorf("cleanup %s: %v", name, err)
			return
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("cleanup %s status=%d", name, response.StatusCode)
			return
		}
		t.Logf("NLB_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap %s: %v", name, err)
	}
	recorder := &nameBindingRecorder{Client: client}
	registry, err := New(recorder)
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	return ctx, client, registry, recorder
}

// nameBindingSeedPublished writes one PUBLISHED execution through the
// production catalog path: AcquireBundleLease then SaveExecution. SaveExecution
// is lease-fenced (bundle_registry.go:44-51 requires a live lease for the
// execution Key), so production lease acquisition is the honest seed, matching
// the lease-guarded flow the fake in bundle_registry_test.go asserts.
func nameBindingSeedPublished(t *testing.T, ctx context.Context, registry *Registry, spec nameBindingExecSpec) publication.BundleExecution {
	t.Helper()
	owner := "nlb-publisher-" + spec.id
	if claimed, err := registry.AcquireBundleLease(ctx, spec.key, owner, time.Now().UTC().Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("acquire lease for %s: claimed=%v err=%v", spec.key, claimed, err)
	}
	publishedAt := spec.publishedAt
	createdAt := publishedAt.Add(-10 * time.Minute)
	execution := publication.BundleExecution{
		ID:  spec.id,
		Key: spec.key,
		BundleIdentity: publication.BundleIdentity{
			Name: spec.recipe, Project: spec.project, DatasetGeneration: spec.generation,
			TranslationVersion: spec.translationVersion, OutputName: spec.output,
			RecipeDigest: spec.recipeDigest, SchemaDigest: spec.schemaDigest, ReceiptID: spec.receiptID,
			ScopeDigest: "scope-" + spec.id, EngineVersion: "nlb-engine",
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name: spec.output, PhysicalTable: "ch_" + spec.id,
			Selector: publication.DataframeSelector{Recipe: spec.recipe, TranslationVersion: spec.translationVersion, Output: spec.output},
			RowCount: 7, ByteCount: 700, State: publication.BundlePublished,
		}},
		CreatedAt:   createdAt,
		UpdatedAt:   publishedAt,
		ReadyAt:     &publishedAt,
		PublishedAt: &publishedAt,
		OwnerID:     owner,
	}
	if err := registry.SaveExecution(ctx, execution, owner); err != nil {
		t.Fatalf("SaveExecution %s: %v", spec.id, err)
	}
	return execution
}

func nameBindingIdentity(exec publication.BundleExecution) string {
	publishedAt := "nil"
	if exec.PublishedAt != nil {
		publishedAt = exec.PublishedAt.UTC().Format(time.RFC3339Nano)
	}
	readyAt := "nil"
	if exec.ReadyAt != nil {
		readyAt = exec.ReadyAt.UTC().Format(time.RFC3339Nano)
	}
	outputs := make([]string, 0, len(exec.Outputs))
	for _, output := range exec.Outputs {
		outputs = append(outputs, fmt.Sprintf("{name=%q physicalTable=%q state=%q}", output.Name, output.PhysicalTable, output.State))
	}
	return fmt.Sprintf("{ID=%q Key=%q Name=%q Project=%q DatasetGeneration=%q TranslationVersion=%q RecipeDigest=%q SchemaDigest=%q ReceiptID=%q State=%q publishedAt=%s readyAt=%s updatedAt=%s outputs=%v}",
		exec.ID, exec.Key, exec.Name, exec.Project, exec.DatasetGeneration, exec.TranslationVersion,
		exec.RecipeDigest, exec.SchemaDigest, exec.ReceiptID, exec.State, publishedAt, readyAt,
		exec.UpdatedAt.UTC().Format(time.RFC3339Nano), outputs)
}

func nameBindingQuoteAQL(t *testing.T, recorded nameBindingQuery) {
	t.Helper()
	t.Logf("FIND_EXECUTION_BY_SELECTOR_AQL=%q", recorded.aql)
	bindKeys := make([]string, 0, len(recorded.bindVars))
	for key := range recorded.bindVars {
		bindKeys = append(bindKeys, key)
	}
	t.Logf("FIND_EXECUTION_BY_SELECTOR_BIND_VARS=%v", strings.Join(bindKeys, ","))
	t.Logf("FIND_EXECUTION_BY_SELECTOR_BIND_VALUES=%#v", recorded.bindVars)
}

// nameBindingMissingIdentityFields are the candidate F-B6 inputs that the
// parent verified are absent from the selector AQL and its bind variables.
var nameBindingMissingIdentityFields = []string{"recipeDigest", "RecipeDigest", "schemaDigest", "SchemaDigest", "receiptId", "ReceiptID", "doc.key", "doc.Key"}

func nameBindingDiscriminatorsPresent(recorded nameBindingQuery) []string {
	loweredQuery := strings.ToLower(recorded.aql)
	var present []string
	for _, field := range nameBindingMissingIdentityFields {
		if strings.Contains(loweredQuery, strings.ToLower(field)) {
			present = append(present, field)
		}
	}
	for key := range recorded.bindVars {
		loweredKey := strings.ToLower(key)
		switch {
		case strings.Contains(loweredKey, "digest"), strings.Contains(loweredKey, "receipt"),
			loweredKey == "key", loweredKey == "executionkey", loweredKey == "leasekey":
			present = append(present, "bindVar:"+key)
		}
	}
	return present
}

// TestFindExecutionBySelectorIgnoresRecipeIdentityAgainstArango is the
// finding-assert for candidate F-B6: two PUBLISHED executions share
// project/generation/recipe/translationVersion/output but carry distinct
// Key/RecipeDigest/SchemaDigest/ReceiptID. The production finder must return
// the publishedAt-newest doc no matter which identity the caller intended.
func TestFindExecutionBySelectorIgnoresRecipeIdentityAgainstArango(t *testing.T) {
	ctx, _, registry, recorder := nameBindingOpen(t, 60*time.Second)

	project := "nlb-project"
	generation := "nlb-gen-" + uuid.NewString()
	selector := publication.DataframeSelector{Recipe: "nlb-recipe", TranslationVersion: "nlb-tv", Output: "nlb-output"}
	t1 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 30, 13, 0, 0, 0, time.UTC)

	intended := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-intended", key: "nlb-key-intended", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:intended-recipe", schemaDigest: "sha256:intended-schema",
		receiptID: "receipt-intended", publishedAt: t1,
	})
	newer := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-newer", key: "nlb-key-newer", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:newer-recipe", schemaDigest: "sha256:newer-schema",
		receiptID: "receipt-newer", publishedAt: t2,
	})
	if intended.RecipeDigest == newer.RecipeDigest || intended.SchemaDigest == newer.SchemaDigest || intended.Key == newer.Key || intended.ReceiptID == newer.ReceiptID {
		t.Fatalf("seed did not produce distinct recipe identities: intended=%s newer=%s", nameBindingIdentity(intended), nameBindingIdentity(newer))
	}

	execution, output, err := registry.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("FindExecutionBySelector: %v", err)
	}
	recorded := recorder.lastSelectorQuery(t)
	nameBindingQuoteAQL(t, recorded)

	if discriminators := nameBindingDiscriminatorsPresent(recorded); len(discriminators) != 0 {
		t.Logf("DISPROVEN: FindExecutionBySelector discriminates recipe identity; present=%v; AQL=%q; intended=%s newer=%s",
			discriminators, recorded.aql, nameBindingIdentity(intended), nameBindingIdentity(newer))
		return
	}
	if execution.ID != newer.ID {
		t.Logf("DISPROVEN: FindExecutionBySelector returned the intended doc, not publishedAt-newest; returned=%s intended=%s newer=%s AQL=%q",
			nameBindingIdentity(execution), nameBindingIdentity(intended), nameBindingIdentity(newer), recorded.aql)
		return
	}
	t.Logf("CONFIRMED: FindExecutionBySelector ignored RecipeDigest/SchemaDigest/Key/ReceiptID and returned publishedAt-newest. selector=%+v returned=%s intended-but-shadowed=%s AQL_FILTER=%q",
		selector, nameBindingIdentity(execution), nameBindingIdentity(intended), recorded.aql)
	if output.Name != selector.Output {
		t.Fatalf("returned output %q does not match selector output %q", output.Name, selector.Output)
	}
	if execution.RecipeDigest != "sha256:newer-recipe" || execution.SchemaDigest != "sha256:newer-schema" || execution.ReceiptID != "receipt-newer" || execution.Key != "nlb-key-newer" {
		t.Fatalf("returned doc is not the newer identity: %s", nameBindingIdentity(execution))
	}
}

// TestFindExecutionBySelectorLegacyCaseAsymmetryAgainstArango reproduces the
// brief's Bug-3: the selector AQL matches capitalized Project/Name/... in the
// FILTER but SORT only reads lowercase publishedAt/readyAt/updatedAt. A legacy
// capitalized doc storing the newest publish time in "PublishedAt" (capital P)
// is FILTER-visible but sorts as if it had no publish time.
func TestFindExecutionBySelectorLegacyCaseAsymmetryAgainstArango(t *testing.T) {
	ctx, client, registry, recorder := nameBindingOpen(t, 60*time.Second)

	project := "nlb-case-project"
	generation := "nlb-case-gen-" + uuid.NewString()
	selector := publication.DataframeSelector{Recipe: "nlb-case-recipe", TranslationVersion: "nlb-case-tv", Output: "nlb-case-output"}
	t1 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 31, 9, 0, 0, 0, time.UTC)

	lowercaseOlder := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-case-lower", key: "nlb-case-key-lower", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:case-lower-recipe", schemaDigest: "sha256:case-lower-schema",
		receiptID: "receipt-case-lower", publishedAt: t1,
	})

	// The legacy doc keeps lowercase "state" and the "outputs" array key
	// because the AQL FILTER has no capitalised branch for either
	// (bundle_registry.go:177-178), so a doc without them would be invisible
	// rather than merely mis-sorted. Everything else, including "PublishedAt",
	// uses the legacy capitalised shape the FILTER accepts via its OR branches.
	legacyDoc := map[string]any{
		"_key":               "nlb-case-legacy",
		"id":                 "nlb-case-legacy",
		"key":                "nlb-case-key-legacy",
		"Project":            project,
		"DatasetGeneration":  generation,
		"Name":               selector.Recipe,
		"TranslationVersion": selector.TranslationVersion,
		"state":              "PUBLISHED",
		"outputs": []any{map[string]any{
			"Name":          selector.Output,
			"PhysicalTable": "ch_nlb_case_legacy",
			"RowCount":      3,
			"ByteCount":     300,
		}},
		"RecipeDigest": "sha256:case-legacy-recipe",
		"SchemaDigest": "sha256:case-legacy-schema",
		"ReceiptID":    "receipt-case-legacy",
		"PublishedAt":  t2,
		"ReadyAt":      t2,
		"UpdatedAt":    t2,
		"createdAt":    t1,
	}
	if err := client.ExecuteAQL(ctx, `INSERT @doc INTO @@collection`, map[string]interface{}{"@collection": BundleExecutionsCollection, "doc": legacyDoc}); err != nil {
		t.Fatalf("insert legacy-shaped doc: %v", err)
	}
	legacyForReport := publication.BundleExecution{
		ID: "nlb-case-legacy", Key: "nlb-case-key-legacy",
		BundleIdentity: publication.BundleIdentity{
			Name: selector.Recipe, Project: project, DatasetGeneration: generation,
			TranslationVersion: selector.TranslationVersion, RecipeDigest: "sha256:case-legacy-recipe",
			SchemaDigest: "sha256:case-legacy-schema", ReceiptID: "receipt-case-legacy",
		},
		State: publication.BundlePublished, PublishedAt: &t2, ReadyAt: &t2, UpdatedAt: t2,
	}

	execution, _, err := registry.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("FindExecutionBySelector: %v", err)
	}
	recorded := recorder.lastSelectorQuery(t)
	nameBindingQuoteAQL(t, recorded)
	t.Logf("AQL_FILTER_AND_SORT=%q", recorded.aql)

	switch execution.ID {
	case lowercaseOlder.ID:
		t.Logf("CONFIRMED: Bug-3 case asymmetry — the lowercase OLDER doc (publishedAt=%s) won over the newer legacy doc whose publish time sits in capitalised \"PublishedAt\"=%s. winner=%s loser=%s",
			t1.Format(time.RFC3339Nano), t2.Format(time.RFC3339Nano), nameBindingIdentity(lowercaseOlder), nameBindingIdentity(legacyForReport))
	case "nlb-case-legacy":
		t.Logf("DISPROVEN: the newer legacy-capitalised doc won the publishedAt-DESC sort; winner=%s shadowed-lowercase=%s",
			nameBindingIdentity(execution), nameBindingIdentity(lowercaseOlder))
	default:
		t.Fatalf("unexpected winner %q; lowercase=%s legacy=%s AQL=%q",
			execution.ID, nameBindingIdentity(lowercaseOlder), nameBindingIdentity(legacyForReport), recorded.aql)
	}
}

// nameBindingReplicatedVerificationStore is a line-for-line replica of
// internal/server/release_verifier.go:14-35 (publicationVerificationStore).
// The original is unreachable from this package because internal/server/
// server.go:35 imports this package, so importing server from this test would
// be an import cycle; the brief authorises replicating these ~15 lines.
type nameBindingReplicatedVerificationStore struct {
	executions publication.ExactExecutionCatalog
}

func (s nameBindingReplicatedVerificationStore) VerifyPublication(ctx context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if s.executions == nil {
		return dataset.PublicationVerification{}, fmt.Errorf("publication verification store is unavailable")
	}
	execution, output, err := s.executions.FindExecutionBySelector(ctx, project, generation, selector)
	if err != nil {
		return dataset.PublicationVerification{}, err
	}
	verifiedAt := execution.UpdatedAt
	if output.VerifiedAt != nil {
		verifiedAt = *output.VerifiedAt
	}
	return dataset.PublicationVerification{
		Selector: selector, ExecutionID: execution.ID, Generation: execution.DatasetGeneration,
		State: string(execution.State.Canonical()), Queryable: execution.State.Successful() && output.Queryable(),
		VerifiedAt: verifiedAt, PhysicalTable: output.PhysicalTable,
	}, nil
}

type nameBindingManifestReader struct {
	manifest dataset.Manifest
}

func (r nameBindingManifestReader) ReadManifest(context.Context, dataset.Ref) (dataset.Manifest, error) {
	return r.manifest, nil
}

type nameBindingReleaseRepository struct {
	mu    sync.Mutex
	saved dataset.ProjectRelease
}

func (r *nameBindingReleaseRepository) SaveRelease(_ context.Context, release dataset.ProjectRelease) (dataset.ProjectRelease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.saved = release
	return release, nil
}

func (r *nameBindingReleaseRepository) ReadActiveRelease(context.Context, string) (dataset.ActiveRelease, error) {
	return dataset.ActiveRelease{}, dataset.ErrNoActiveRelease
}

func (r *nameBindingReleaseRepository) CompareAndSwapActivateRelease(context.Context, dataset.ProjectRelease, int64) (dataset.ActiveRelease, error) {
	return dataset.ActiveRelease{}, errors.New("CompareAndSwapActivateRelease must not run during Create")
}

func (r *nameBindingReleaseRepository) savedRelease() dataset.ProjectRelease {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.saved
}

// TestVerifierToBindingChainEchoAgainstArango drives the seam the candidate
// says is load-bearing: the (replicated) publicationVerificationStore over the
// production registry, then production dataset.ReleaseService.Create, whose
// publicationBinding echoes verification.ExecutionID into the release that
// PublishAuthoring CAS-activates (explorer/arango/publication.go:57). It shows
// the binding follows the newest doc while a distinct intended execution exists.
func TestVerifierToBindingChainEchoAgainstArango(t *testing.T) {
	ctx, _, registry, recorder := nameBindingOpen(t, 60*time.Second)

	project := "nlb-chain-project"
	generation := "nlb-chain-gen-" + uuid.NewString()
	selector := publication.DataframeSelector{Recipe: "nlb-chain-recipe", TranslationVersion: "nlb-chain-tv", Output: "nlb-chain-output"}
	t1 := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 10, 30, 18, 0, 0, 0, time.UTC)

	intended := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-chain-intended", key: "nlb-chain-key-intended", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:chain-intended-recipe", schemaDigest: "sha256:chain-intended-schema",
		receiptID: "receipt-chain-intended", publishedAt: t1,
	})
	newer := nameBindingSeedPublished(t, ctx, registry, nameBindingExecSpec{
		id: "nlb-chain-newer", key: "nlb-chain-key-newer", project: project, generation: generation,
		recipe: selector.Recipe, translationVersion: selector.TranslationVersion, output: selector.Output,
		recipeDigest: "sha256:chain-newer-recipe", schemaDigest: "sha256:chain-newer-schema",
		receiptID: "receipt-chain-newer", publishedAt: t2,
	})

	verifier := nameBindingReplicatedVerificationStore{executions: registry}
	verification, err := verifier.VerifyPublication(ctx, project, generation, selector)
	if err != nil {
		t.Fatalf("VerifyPublication: %v", err)
	}
	recorded := recorder.lastSelectorQuery(t)
	nameBindingQuoteAQL(t, recorded)
	if discriminators := nameBindingDiscriminatorsPresent(recorded); len(discriminators) != 0 {
		t.Logf("DISPROVEN chain premise: verification store discriminates recipe identity; present=%v", discriminators)
		return
	}

	// The intended distinct execution must still exist for the echo to mean
	// anything: it is shadowed at the finder, not absent.
	storedIntended, err := registry.GetExecution(ctx, intended.ID)
	if err != nil {
		t.Fatalf("intended execution %s missing: %v", intended.ID, err)
	}

	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatalf("NewRef: %v", err)
	}
	snapshot, err := dataset.NewSchemaSnapshot("nlb-schema", "4.6.0", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatalf("NewSchemaSnapshot (skip condition: snapshot validation): %v", err)
	}
	manifest, err := dataset.NewManifest(ref, snapshot)
	if err != nil {
		t.Fatalf("NewManifest (skip condition: manifest validation): %v", err)
	}
	manifest, err = manifest.Transition(dataset.StateStaged)
	if err != nil {
		t.Fatalf("manifest Transition (skip condition: staging transition): %v", err)
	}
	releases := &nameBindingReleaseRepository{}
	releaseService := dataset.ReleaseService{
		Manifests: nameBindingManifestReader{manifest: manifest},
		Releases:  releases,
		Verifier:  verifier,
	}
	release, err := releaseService.Create(ctx, dataset.ActivationRequest{
		Project: project, Generation: generation, GitCommit: generation,
		OptionalSelectors: []dataset.DataframeSelector{selector},
	})
	if err != nil {
		t.Fatalf("ReleaseService.Create: %v", err)
	}
	if len(release.Publications) != 1 {
		t.Fatalf("release publications = %+v, want exactly one binding", release.Publications)
	}
	binding := release.Publications[0]

	if verification.ExecutionID != newer.ID || binding.ExecutionID != newer.ID {
		t.Logf("DISPROVEN chain: verification/binding did not follow publishedAt-newest. verification=%+v binding=%+v intended=%s newer=%s",
			verification, binding, nameBindingIdentity(storedIntended), nameBindingIdentity(newer))
		return
	}
	t.Logf("CONFIRMED chain: replicated publicationVerificationStore.VerifyPublication echoed ExecutionID=%q (publishedAt-newest) and production dataset.ReleaseService.Create bound it verbatim while the distinct intended execution still exists. verification=%+v binding=%+v intended-still-persisted=%s newest=%s",
		verification.ExecutionID, verification, binding, nameBindingIdentity(storedIntended), nameBindingIdentity(newer))
	t.Logf("CHAIN_NOTE: explorer/arango/publication.go:57 CompareAndSwapActivateRelease activates exactly this dataset.ProjectRelease, while explorer/lifecycle/publish.go:87 prepared it via selectorsForBundle(receipt.Bundle) without passing MaterializeReceipt's execution; the explorer revision meanwhile records execution.ID at publish.go:93.")
}
