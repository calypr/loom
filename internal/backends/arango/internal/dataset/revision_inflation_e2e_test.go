package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	store "github.com/calypr/loom/internal/backends/arango/internal/store"
	"github.com/calypr/loom/internal/dataset"
	"github.com/google/uuid"
)

// F-B15 durable control (product repaired in REPAIR-5/Round 31; the red arm
// stays armed as the regression witness and was observed PREVENTED against
// the real store in Round 36): idempotent re-activation through the
// pre-repair production ReleaseService.Activate path was NOT a no-op. Each identical
// activation mints revision+1 into the sha-derived release ID
// (internal/dataset/release.go:227,273-280) so the AQL alreadyActive guard
// (internal/backends/arango/internal/dataset/release_store.go:158-159) can never match for
// callers that re-enter via Create, and both pointer documents churn while the
// release CONTENT (project/generation/gitCommit/publications/verifications) is
// byte-identical. Recorded CONFIRMED in
// verification/publication-bundle/FINDINGS.md Round 10 (runs 22ffa92b and
// 97d6ab0f); this file replaces the broken truncated re-write (quarantined at
// /tmp/opencode/revision_inflation_broken_a3a58c18.go).

const riDatabasePrefix = "loom_verify_ri_"

type riNoopVerifier struct{}

func (riNoopVerifier) VerifyPublication(context.Context, string, string, dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	return dataset.PublicationVerification{State: "PUBLISHED", Queryable: true}, nil
}

func riOpen(t *testing.T, budget time.Duration) (context.Context, *store.Client, *Store) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := riDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, riDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, riDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("RI_DATABASE=%s", name)
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
		t.Logf("RI_DATABASE_REMOVED=%s", name)
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

func riLoadStaged(t *testing.T, ctx context.Context, lifecycle *Store, project, generation, digest string) {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatalf("new ref %s/%s: %v", project, generation, err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:ri-"+generation, "", digest, []string{"Patient"})
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
	staged, err := lifecycle.TransitionManifest(ctx, manifest, dataset.StateStaged)
	if err != nil {
		t.Fatalf("TransitionManifest %s -> STAGED: %v", generation, err)
	}
	_ = staged
}

type riObserved struct {
	revision     int64
	activeErr    error
	id           string
	content      string
	releaseDocs  int
	relDocRev    string
	relDocID     string
	genDocRev    string
	genDocRevNum string
}

func riObserve(t *testing.T, ctx context.Context, client *store.Client, svc *dataset.ReleaseService, project, generation string) riObserved {
	t.Helper()
	out := riObserved{}
	active, err := svc.Active(ctx, project)
	out.activeErr = err
	if err == nil {
		out.revision = active.Revision
		out.id = active.Release.ID
		raw, mErr := json.Marshal(active.Release)
		if mErr != nil {
			t.Fatalf("marshal active release: %v", mErr)
		}
		var m map[string]any
		if uErr := json.Unmarshal(raw, &m); uErr != nil {
			t.Fatalf("unmarshal active release: %v", uErr)
		}
		// Exclude derived, revision-dependent / wall-clock fields: content
		// identity means project/generation/gitCommit/publications/verifications.
		delete(m, "createdAt")
		delete(m, "id")
		canonical, mErr := json.Marshal(m)
		if mErr != nil {
			t.Fatalf("re-marshal content: %v", mErr)
		}
		out.content = string(canonical)
	}
	rows := func(bind map[string]any) []map[string]any {
		var docs []map[string]any
		qErr := client.QueryRows(ctx, fmt.Sprintf("FOR doc IN %s FILTER doc.recordType == @rt RETURN doc", LifecycleCollection), 100, bind, func(row map[string]any) error {
			docs = append(docs, row)
			return nil
		})
		if qErr != nil {
			t.Fatalf("query recordType %v: %v", bind["rt"], qErr)
		}
		return docs
	}
	rels := rows(map[string]any{"rt": "project_release"})
	out.releaseDocs = len(rels)
	for _, d := range rels {
		if fmt.Sprint(d["id"]) == out.id || fmt.Sprint(d["releaseID"]) == out.id {
			out.relDocRev = fmt.Sprint(d["_rev"])
			out.relDocID = fmt.Sprint(d["_key"])
		}
	}
	pointerErr := client.QueryRows(ctx, fmt.Sprintf("FOR doc IN %s FILTER doc.recordType == \"active_project_release\" AND doc.project == @project RETURN doc", LifecycleCollection), 10, map[string]any{"project": project}, func(row map[string]any) error {
		out.relDocRev = fmt.Sprint(row["_rev"])
		out.relDocID = fmt.Sprint(row["releaseId"])
		return nil
	})
	if pointerErr != nil {
		t.Fatalf("read active_project_release pointer: %v", pointerErr)
	}
	genErr := client.QueryRows(ctx, fmt.Sprintf("FOR doc IN %s FILTER doc.recordType == \"active_generation\" AND doc.project == @project RETURN doc", LifecycleCollection), 10, map[string]any{"project": project}, func(row map[string]any) error {
		out.genDocRev = fmt.Sprint(row["_rev"])
		out.genDocRevNum = fmt.Sprint(row["releaseRevision"])
		return nil
	})
	if genErr != nil {
		t.Fatalf("read active_generation pointer: %v", genErr)
	}
	return out
}

func TestRevisionInflationAgainstArango(t *testing.T) {
	ctx, client, lifecycle := riOpen(t, 180*time.Second)
	const project = "ri-project"
	const generation = "ri-gen-a"
	riLoadStaged(t, ctx, lifecycle, project, generation, strings.Repeat("a", 64))
	svc := &dataset.ReleaseService{
		Manifests: lifecycle,
		Releases:  lifecycle,
		Verifier:  riNoopVerifier{},
	}

	t.Log("PRE-STATED EXPECT (bug): 4 identical Activate calls through ReleaseService.Activate (server.go:565-581 pattern: live revision then Activate => Create mints sha ID at currentRevision+1) => revision 1->4, 4 project_release docs, both pointers churn, content byte-identical => FAILED-ON-PURPOSE CONFIRMED.")
	t.Log("PRE-STATED EXPECT (sound): activations 2..4 are no-ops: revision stable, 1 release doc, both pointer _rev stable => PREVENTED with the preventing quoted lines.")

	baseline := riObserve(t, ctx, client, svc, project, generation)
	if baseline.activeErr == nil {
		t.Fatalf("BASELINE PRECONDITION BROKEN: project already active before first activation")
	}
	for attempt := 1; attempt <= 4; attempt++ {
		expected := int64(0)
		if active, aErr := svc.Active(ctx, project); aErr == nil {
			expected = active.Revision
		}
		active, err := svc.Activate(ctx, dataset.ActivationRequest{
			Project:          project,
			Generation:       generation,
			GitCommit:        generation,
			ExpectedRevision: expected,
		})
		obs := riObserve(t, ctx, client, svc, project, generation)
		t.Logf("OBSERVED activation #%d: Activate() rev=%d id=%s err=%v | rev=%d id=%s releaseDocs=%d relPointer{_rev=%s releaseId=%s} genPointer{_rev=%s releaseRevision=%v} content=%s",
			attempt, active.Revision, active.Release.ID, err, obs.revision, obs.id, obs.releaseDocs, obs.relDocRev, obs.relDocID, obs.genDocRev, obs.genDocRevNum, obs.content)
		if err != nil {
			t.Fatalf("PRODUCTION PATH ERROR at activation #%d (unexpected): %v", attempt, err)
		}
		if attempt == 1 {
			baseline = obs
			if obs.revision != 1 || obs.releaseDocs != 1 {
				t.Fatalf("BASELINE PRECONDITION BROKEN: first activation gave rev=%d docs=%d", obs.revision, obs.releaseDocs)
			}
		}
	}

	last := riObserve(t, ctx, client, svc, project, generation)
	inflated := last.revision != baseline.revision || last.id != baseline.id || last.releaseDocs != baseline.releaseDocs || last.relDocRev != baseline.relDocRev || last.genDocRev != baseline.genDocRev
	contentIdentical := last.content == baseline.content && last.content != ""

	t.Logf("OBSERVED VERDICT INPUTS inflated=%v contentIdentical=%v baseline{rev=%d id=%s docs=%d} last{rev=%d id=%s docs=%d}",
		inflated, contentIdentical, baseline.revision, baseline.id, baseline.releaseDocs, last.revision, last.id, last.releaseDocs)

	switch {
	case inflated && contentIdentical:
		t.Fatalf("FAILED-ON-PURPOSE CONFIRMED: 4 identical activations of %q through the production ReleaseService.Activate path (server.go live-revision pattern) are NOT no-ops: revision %d -> %d, release ID %s -> %s, project_release docs %d -> %d, active_project_release._rev %s -> %s, active_generation._rev %s -> %s (releaseRevision %v -> %v), WHILE the release content is byte-identical (%s). Layer: ReleaseService.Create mints release.ID = releaseID(release, currentRevision+1) (internal/dataset/release.go:227,273-280; sha over project/generation/REVISION/publications); the alreadyActive short-circuit requires re-submitting the SAME releaseId (internal/backends/arango/internal/dataset/release_store.go:158-159) which the Create-then-CAS callers never do. Harm: every repeat activation click mints a fresh immutable release document + revision forever (unbounded Arango lifecycle growth, audit inflation, phantom release-ID churn for ID-keyed consumers).",
			generation, baseline.revision, last.revision, baseline.id, last.id, baseline.releaseDocs, last.releaseDocs, baseline.relDocRev, last.relDocRev, baseline.genDocRev, last.genDocRev, baseline.genDocRevNum, last.genDocRevNum, last.content)
	case !inflated && contentIdentical:
		t.Log("PREVENTED (sound): identical re-activation is a true no-op — revision, release ID, doc count, and both pointer _revs stable with byte-identical content.")
	default:
		t.Fatalf("INCONCLUSIVE: inflated=%v contentIdentical=%v baseline=%+v last=%+v", inflated, contentIdentical, baseline, last)
	}
}
