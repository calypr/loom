package arango

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	arangostore "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/dataset"
	"github.com/google/uuid"
)

// R2 durable control: a required->optional reclassification of the SAME selector
// under the SAME execution is a genuinely different immutable snapshot. The
// pre-widening releaseID hashed only project/generation plus each publication's
// (Selector.Key(), ExecutionID), so SaveRelease's full-content comparison
// rejected the new candidate with "release already exists with different
// content" while the identity said the document already existed. This test runs
// the production ReleaseService.Create/Activate against a real Arango and
// asserts the policy change saves AND activates a distinct immutable candidate,
// that the superseded candidate keeps its bytes and _rev, that identical
// retries converge, and that a pre-widening stored id transitions at most once.

const rpcDatabasePrefix = "loom_verify_rpc_"

// rpcHistoricalReleaseID replays the pre-widening digest exactly as shipped
// (project and generation joined by a bare NUL, then one NUL-joined
// (Selector.Key(), ExecutionID) group per publication). It is the fault
// premise: the required and optional snapshots collapse HERE and must not
// collapse in production releaseID.
func rpcHistoricalReleaseID(release dataset.ProjectRelease) string {
	var b strings.Builder
	b.WriteString(release.Project)
	b.WriteByte(0)
	b.WriteString(release.Generation)
	for _, publication := range release.Publications {
		b.WriteByte(0)
		b.WriteString(publication.Selector.Key())
		b.WriteByte(0)
		b.WriteString(publication.ExecutionID)
	}
	digest := sha256.Sum256([]byte(b.String()))
	return "release_" + hex.EncodeToString(digest[:])
}

type rpcVerifier struct {
	generation string
	verifiedAt time.Time
}

func (v rpcVerifier) VerifyPublication(_ context.Context, project, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	if generation != v.generation {
		return dataset.PublicationVerification{}, fmt.Errorf("%w: generation %s is not %s", dataset.ErrPublicationRecordMissing, generation, v.generation)
	}
	return dataset.PublicationVerification{Selector: selector, ExecutionID: "execution-r2", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: v.verifiedAt}, nil
}

func rpcOpen(t *testing.T, budget time.Duration) (context.Context, *arangostore.Client, *Store) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := rpcDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, rpcDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, rpcDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := arangostore.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open owned verification database %s: %v", name, err)
	}
	t.Logf("RPC_DATABASE_CREATED=%s (via production store.Open)", name)
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
		t.Logf("RPC_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	lifecycle, err := New(client)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	return ctx, client, lifecycle
}

func rpcStageGeneration(t *testing.T, ctx context.Context, lifecycle *Store, project, generation string) {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatalf("new ref %s/%s: %v", project, generation, err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:rpc-"+generation, "", strings.Repeat("b", 64), []string{"Patient"})
	if err != nil {
		t.Fatalf("schema snapshot %s: %v", generation, err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("new manifest %s: %v", generation, err)
	}
	if _, err := lifecycle.CreateManifest(ctx, manifest); err != nil {
		t.Fatalf("CreateManifest %s: %v", generation, err)
	}
	if _, err := lifecycle.TransitionManifest(ctx, manifest, dataset.StateStaged); err != nil {
		t.Fatalf("TransitionManifest %s -> STAGED: %v", generation, err)
	}
}

// rpcRawRelease reads the persisted project_release document by release id
// through raw AQL, so the assertions observe stored bytes and _rev rather than
// the decoded view.
func rpcRawRelease(t *testing.T, ctx context.Context, client *arangostore.Client, id string) map[string]any {
	t.Helper()
	var doc map[string]any
	err := client.QueryRows(ctx, fmt.Sprintf("FOR doc IN %s FILTER doc._key == @key RETURN doc", LifecycleCollection), 2,
		map[string]any{"key": releaseDocumentKey(id)}, func(row map[string]any) error {
			doc = row
			return nil
		})
	if err != nil {
		t.Fatalf("read raw release %s: %v", id, err)
	}
	if doc == nil {
		t.Fatalf("persisted candidate %s is missing", id)
	}
	return doc
}

func rpcReleaseCount(t *testing.T, ctx context.Context, client *arangostore.Client) int {
	t.Helper()
	count := 0
	err := client.QueryRows(ctx, fmt.Sprintf("FOR doc IN %s FILTER doc.recordType == @rt RETURN {present: true}", LifecycleCollection), 1000,
		map[string]any{"rt": releaseRecordType}, func(row map[string]any) error {
			count++
			return nil
		})
	if err != nil {
		t.Fatalf("count project_release documents: %v", err)
	}
	return count
}

func rpcPointer(t *testing.T, ctx context.Context, client *arangostore.Client, project string) (releaseID string, revision any, rev string) {
	t.Helper()
	found := false
	err := client.QueryRows(ctx, fmt.Sprintf("FOR doc IN %s FILTER doc.recordType == @rt AND doc.project == @project RETURN doc", LifecycleCollection), 4,
		map[string]any{"rt": activeReleaseRecordType, "project": project}, func(row map[string]any) error {
			found = true
			releaseID, revision, rev = fmt.Sprint(row["releaseId"]), row["revision"], fmt.Sprint(row["_rev"])
			return nil
		})
	if err != nil {
		t.Fatalf("read active_project_release pointer: %v", err)
	}
	if !found {
		t.Fatalf("active_project_release pointer for %s is missing", project)
	}
	return releaseID, revision, rev
}

// rpcCanonicalContent renders the persisted immutable content, excluding the
// document metadata, the derived id and the first-creation timestamp.
func rpcCanonicalContent(t *testing.T, doc map[string]any) string {
	t.Helper()
	content := map[string]any{}
	for key, value := range doc {
		switch key {
		case "_key", "_id", "_rev", "recordType", "createdAt", "id":
		default:
			content[key] = value
		}
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal persisted content: %v", err)
	}
	return string(encoded)
}

func rpcPublicationRequired(t *testing.T, doc map[string]any) any {
	t.Helper()
	publications, ok := doc["publications"].([]any)
	if !ok || len(publications) != 1 {
		t.Fatalf("persisted publications = %#v (want exactly one binding)", doc["publications"])
	}
	binding, ok := publications[0].(map[string]any)
	if !ok {
		t.Fatalf("publication binding = %#v", publications[0])
	}
	return binding["required"]
}

func TestReleaseRequiredPolicyChangeArango(t *testing.T) {
	ctx, client, lifecycle := rpcOpen(t, 300*time.Second)
	const project, generation = "rpc-project", "rpc-generation-a"
	rpcStageGeneration(t, ctx, lifecycle, project, generation)
	selector := dataset.DataframeSelector{Recipe: "core", TranslationVersion: "v1", Output: "Patient"}
	verifiedAt := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	verifier := rpcVerifier{generation: generation, verifiedAt: verifiedAt}
	newService := func(required ...dataset.DataframeSelector) *dataset.ReleaseService {
		return &dataset.ReleaseService{Manifests: lifecycle, Releases: lifecycle, Verifier: verifier, Required: required, Now: func() time.Time { return verifiedAt }}
	}

	// Phase 1: the publication is required.
	requiredActive, err := newService(selector).Activate(ctx, dataset.ActivationRequest{Project: project, Generation: generation, ExpectedRevision: 0})
	if err != nil || requiredActive.Revision != 1 {
		t.Fatalf("required activation = %#v, %v (want revision 1)", requiredActive, err)
	}
	requiredDoc := rpcRawRelease(t, ctx, client, requiredActive.Release.ID)
	requiredContent := rpcCanonicalContent(t, requiredDoc)
	requiredRev := fmt.Sprint(requiredDoc["_rev"])
	if got := rpcPublicationRequired(t, requiredDoc); got != true {
		t.Fatalf("phase 1 persisted required = %#v, want true", got)
	}

	// Fault premise: the pre-widening digest cannot tell the two snapshots apart.
	if rpcHistoricalReleaseID(requiredActive.Release) != rpcHistoricalReleaseID(dataset.ProjectRelease{
		Project: project, Generation: generation,
		Publications: []dataset.ReleasePublication{{Selector: selector, ExecutionID: "execution-r2", Generation: generation}},
	}) {
		t.Fatalf("fault premise broken: the historical framing must fold the required/optional pair")
	}

	// Phase 2: the same selector and execution, now optional. This must save a
	// distinct immutable candidate and activate it.
	optionalActive, err := newService().Activate(ctx, dataset.ActivationRequest{Project: project, Generation: generation, ExpectedRevision: 1, OptionalSelectors: []dataset.DataframeSelector{selector}})
	if err != nil {
		t.Fatalf("optional reclassification did not save and activate: %v", err)
	}
	if optionalActive.Revision != 2 {
		t.Fatalf("optional reactivation = revision %d, want 2", optionalActive.Revision)
	}
	if optionalActive.Release.ID == requiredActive.Release.ID {
		t.Fatalf("REGRESSION: required and optional snapshots share the immutable id %s while the store compares them as different content", optionalActive.Release.ID)
	}
	optionalDoc := rpcRawRelease(t, ctx, client, optionalActive.Release.ID)
	if got := rpcPublicationRequired(t, optionalDoc); got != false {
		t.Fatalf("phase 2 persisted required = %#v, want false", got)
	}
	if len(optionalActive.Release.Publications) != 1 || optionalActive.Release.Publications[0].Required {
		t.Fatalf("active optional snapshot = %#v", optionalActive.Release.Publications)
	}
	if rpcCanonicalContent(t, optionalDoc) == requiredContent {
		t.Fatalf("REGRESSION: the two candidates stored identical content: %s", requiredContent)
	}

	// The superseded candidate is untouched: same bytes, same _rev.
	reReadRequired := rpcRawRelease(t, ctx, client, requiredActive.Release.ID)
	if got := rpcCanonicalContent(t, reReadRequired); got != requiredContent {
		t.Fatalf("REGRESSION: the required candidate content mutated: got %s want %s", got, requiredContent)
	}
	if got := fmt.Sprint(reReadRequired["_rev"]); got != requiredRev {
		t.Fatalf("REGRESSION: the required candidate was rewritten: _rev %s -> %s", requiredRev, got)
	}
	if got := rpcPublicationRequired(t, reReadRequired); got != true {
		t.Fatalf("REGRESSION: the superseded candidate lost required=true: %#v", got)
	}

	// Immutable-content comparison is NOT weakened: mutating the persisted
	// snapshot under its own id must still be refused.
	tampered := requiredActive.Release
	tampered.Publications = append([]dataset.ReleasePublication(nil), tampered.Publications...)
	tampered.Publications[0].Required = false
	tampered.CreatedAt = verifiedAt.Add(time.Hour)
	if _, err := lifecycle.SaveRelease(ctx, tampered); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("tampered re-save of the required candidate = %v, want the different-content refusal", err)
	}
	// Re-saving byte-identical content (drifted createdAt only) is a no-op that
	// keeps the original document bytes.
	drifted := requiredActive.Release
	drifted.CreatedAt = verifiedAt.Add(42 * time.Minute)
	saved, err := lifecycle.SaveRelease(ctx, drifted)
	if err != nil {
		t.Fatalf("identical-content re-save failed: %v", err)
	}
	if saved.CreatedAt.Equal(drifted.CreatedAt) {
		t.Fatalf("REGRESSION: identical re-save replaced the first-creation timestamp")
	}
	if got := rpcRawRelease(t, ctx, client, requiredActive.Release.ID); fmt.Sprint(got["_rev"]) != requiredRev || rpcCanonicalContent(t, got) != requiredContent {
		t.Fatalf("REGRESSION: identical re-save churned the immutable candidate")
	}

	// Identical retries of the optional snapshot: no revision bump, no repeated
	// identities, no new documents, no pointer churn.
	optionalRequest := dataset.ActivationRequest{Project: project, Generation: generation, ExpectedRevision: 2, OptionalSelectors: []dataset.DataframeSelector{selector}}
	pointerID, pointerRevision, pointerRev := rpcPointer(t, ctx, client, project)
	if pointerID != optionalActive.Release.ID {
		t.Fatalf("active pointer = %s, want %s", pointerID, optionalActive.Release.ID)
	}
	docsBefore := rpcReleaseCount(t, ctx, client)
	for attempt := 1; attempt <= 3; attempt++ {
		repeat, err := newService().Activate(ctx, optionalRequest)
		if err != nil {
			t.Fatalf("identical optional reactivation #%d = %v", attempt, err)
		}
		if repeat.Revision != 2 || repeat.Release.ID != optionalActive.Release.ID {
			t.Fatalf("REGRESSION: identical reactivation #%d churned revision %d id %s (want 2/%s)", attempt, repeat.Revision, repeat.Release.ID, optionalActive.Release.ID)
		}
		created, err := newService().Create(ctx, optionalRequest)
		if err != nil {
			t.Fatalf("identical optional save #%d = %v", attempt, err)
		}
		if created.ID != optionalActive.Release.ID {
			t.Fatalf("REGRESSION: identical save #%d minted %s, want %s", attempt, created.ID, optionalActive.Release.ID)
		}
	}
	if got := rpcReleaseCount(t, ctx, client); got != docsBefore {
		t.Fatalf("REGRESSION: identical retries created %d repeated identities (before %d)", got-docsBefore, docsBefore)
	}
	if id, revision, rev := rpcPointer(t, ctx, client, project); id != pointerID || rev != pointerRev || fmt.Sprint(revision) != fmt.Sprint(pointerRevision) {
		t.Fatalf("REGRESSION: identical retries churned the active pointer: %s/%v/%s -> %s/%v/%s", pointerID, pointerRevision, pointerRev, id, revision, rev)
	}

	// Upgrade boundary: a candidate saved AND activated by the pre-widening
	// binary keeps its old id; the widened framing transitions it exactly once
	// and every later retry converges.
	legacyProbe := dataset.ProjectRelease{Project: project, Generation: generation, GitCommit: generation,
		Publications: []dataset.ReleasePublication{{Selector: selector, ExecutionID: "execution-r2", Generation: generation, VerifiedAt: verifiedAt}}}
	legacyID := rpcHistoricalReleaseID(legacyProbe)
	if legacyID == optionalActive.Release.ID || legacyID == requiredActive.Release.ID {
		t.Fatalf("witness precondition broken: the historical id %s must differ from both widened ids", legacyID)
	}
	legacyDoc, err := lifecycleDocument(dataset.ProjectRelease{
		ID: legacyID, Project: project, GitCommit: generation, Generation: generation,
		Publications:          legacyProbe.Publications,
		RequiredVerifications: []dataset.ContractVerification{{Selector: selector, ExecutionID: "execution-r2", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: verifiedAt}},
		CreatedAt:             verifiedAt,
	})
	if err != nil {
		t.Fatalf("encode legacy candidate: %v", err)
	}
	legacyDoc["_key"] = releaseDocumentKey(legacyID)
	legacyDoc["recordType"] = releaseRecordType
	var inserted map[string]any
	if err := client.QueryRows(ctx, fmt.Sprintf("INSERT @document INTO %s RETURN NEW", LifecycleCollection), 2,
		map[string]any{"document": legacyDoc}, func(row map[string]any) error {
			inserted = row
			return nil
		}); err != nil {
		t.Fatalf("seed the pre-widening candidate: %v", err)
	}
	if inserted == nil {
		t.Fatalf("seeding the pre-widening candidate stored nothing")
	}
	legacyStored := dataset.ProjectRelease{ID: legacyID, Project: project, GitCommit: generation, Generation: generation,
		Publications:          legacyProbe.Publications,
		RequiredVerifications: []dataset.ContractVerification{{Selector: selector, ExecutionID: "execution-r2", Generation: generation, State: "PUBLISHED", Queryable: true, VerifiedAt: verifiedAt}},
		CreatedAt:             verifiedAt,
	}
	if _, err := lifecycle.CompareAndSwapActivateRelease(ctx, legacyStored, 2); err != nil {
		t.Fatalf("activate the pre-widening candidate: %v", err)
	}
	legacyRaw := rpcRawRelease(t, ctx, client, legacyID)
	legacyContent := rpcCanonicalContent(t, legacyRaw)
	legacyRev := fmt.Sprint(legacyRaw["_rev"])

	legacyRequest := dataset.ActivationRequest{Project: project, Generation: generation, ExpectedRevision: 3}
	transitioned, err := newService(selector).Activate(ctx, legacyRequest)
	if err != nil {
		t.Fatalf("first reactivation of the stored pre-widening id: %v", err)
	}
	if transitioned.Release.ID == legacyID {
		t.Fatalf("REGRESSION: the stored pre-widening id %s never transitioned to the widened id", legacyID)
	}
	if transitioned.Revision != 4 {
		t.Fatalf("REGRESSION: the legacy transition must bump the revision exactly once, got %d", transitioned.Revision)
	}
	for attempt := 5; attempt <= 7; attempt++ {
		repeat, err := newService(selector).Activate(ctx, legacyRequest)
		if err != nil {
			t.Fatalf("repeated reactivation after the transition #%d = %v", attempt, err)
		}
		if repeat.Revision != 4 || repeat.Release.ID != transitioned.Release.ID {
			t.Fatalf("REGRESSION: reactivation #%d made a second new-id transition: revision %d id %s (want 4/%s)", attempt, repeat.Revision, repeat.Release.ID, transitioned.Release.ID)
		}
	}
	if got := rpcRawRelease(t, ctx, client, legacyID); rpcCanonicalContent(t, got) != legacyContent || fmt.Sprint(got["_rev"]) != legacyRev {
		t.Fatalf("REGRESSION: the pre-widening candidate was mutated during the transition")
	}
	if got, want := rpcReleaseCount(t, ctx, client), docsBefore+1; got != want {
		t.Fatalf("REGRESSION: expected %d immutable candidates (the seeded pre-widening one, with the new-id transition reusing the existing widened candidate), got %d", want, got)
	}
	t.Logf("VERDICT=PASS required %s -> optional %s saved and activated on real Arango; required candidate _rev %s immutable; identical retries and the legacy id %s converged after one transition",
		requiredActive.Release.ID, optionalActive.Release.ID, requiredRev, legacyID)
}
