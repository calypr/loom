package arango

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	arangostore "github.com/calypr/loom/internal/backends/arango/internal/store"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/google/uuid"
)

const gacDatabasePrefix = "loom_verify_gac_"

type gacCitation struct {
	file string
	line int
	want string
}

var gacCitations = []gacCitation{
	{"internal/backends/arango/internal/dataset/manifest_lifecycle.go", 198, `if active, err := s.ResolveActiveManifest(ctx, candidate.Dataset.Project); err == nil {`},
	{"internal/backends/arango/internal/dataset/manifest_lifecycle.go", 199, `if manifestIdentityEqual(active, candidate) {`},
	{"internal/backends/arango/internal/dataset/manifest_lifecycle.go", 200, `return nil`},
	{"internal/backends/arango/internal/dataset/manifest_lifecycle.go", 202, `} else if !errors.Is(err, publication.ErrNoActiveGeneration) {`},
	{"internal/backends/arango/internal/dataset/manifest_lifecycle.go", 243, `return fmt.Errorf("%w: candidate %s/%s was not a persisted staged manifest with a valid active pointer", ErrActivationConflict, candidate.Dataset.Project, candidate.Dataset.Generation)`},
	{"internal/backends/arango/internal/dataset/manifest_lifecycle.go", 334, `UPDATE active WITH { dataset: candidate.dataset, manifestKey: candidate._key } IN @@lifecycle_collection`},
	{"internal/backends/arango/internal/dataset/manifest_lifecycle.go", 335, `OPTIONS { ignoreRevs: false, mergeObjects: false }`},
	{"internal/backends/arango/internal/dataset/release_store.go", 223, `FILTER currentRevision == @expected_revision OR alreadyActive`},
	{"internal/api/bulk/load/service.go", 206, `manifest, err := s.generationActivator.ReadManifest(ctx, ref)`},
	{"internal/api/bulk/load/service.go", 210, `if !manifest.IsStaged() {`},
	{"internal/api/bulk/load/service.go", 217, `if err := s.generationActivator.Activate(ctx, manifest); err != nil {`},
	{"internal/backends/arango/internal/explorer/activation.go", 98, `if err := updateActivationDocument(txCtx, tx, datasetarango.LifecycleCollection, activeKey, map[string]any{"dataset": dataset, "manifestKey": manifestKey}); err != nil {`},
	{"internal/backends/arango/internal/explorer/activation.go", 38, `UPDATE d WITH @patch IN @@c`},
}

var gacActivateAQLMustNotHold = []string{
	"active.dataset ==",
	"active.dataset !=",
	"FILTER active.dataset",
	"active.manifestKey ==",
	"active.manifestKey !=",
	"FILTER active.manifestKey",
	"_rev",
	"expected",
	"revision",
	"lease",
}

var gacActivateAQLMustHold = []string{
	"UPDATE active WITH { dataset: candidate.dataset, manifestKey: candidate._key } IN @@lifecycle_collection",
	"OPTIONS { ignoreRevs: false, mergeObjects: false }",
	"RETURN { dataset: NEW.dataset }",
}

func gacRepoRoot(t *testing.T) string {
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

func gacReadSource(t *testing.T, file string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(gacRepoRoot(t), file))
	if err != nil {
		t.Fatalf("citation source %s unreadable: %v", file, err)
	}
	return strings.Split(string(raw), "\n")
}

func gacCheckCitations(t *testing.T) {
	t.Helper()
	contents := map[string][]string{}
	for _, citation := range gacCitations {
		lines, ok := contents[citation.file]
		if !ok {
			lines = gacReadSource(t, citation.file)
			contents[citation.file] = lines
		}
		if citation.line < 1 || citation.line > len(lines) {
			t.Fatalf("citation drift: %s has %d lines, cited :%d == %q", citation.file, len(lines), citation.line, citation.want)
		}
		found := false
		for _, line := range lines {
			if strings.TrimSpace(line) == strings.TrimSpace(citation.want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("citation drift: %s no longer contains %q (cited :%d)", citation.file, citation.want, citation.line)
		}
	}
}

func gacShippedActivateAQL(t *testing.T) string {
	t.Helper()
	lines := gacReadSource(t, "internal/backends/arango/internal/dataset/manifest_lifecycle.go")
	start, end := -1, -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "const activateAQL = `") {
			start = i
		}
		if start >= 0 && i > start && strings.HasSuffix(strings.TrimSpace(line), "`") {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		t.Fatalf("could not extract activateAQL const from manifest_lifecycle.go")
	}
	shipped := strings.Join(lines[start+1:end], "\n")
	if strings.TrimSpace(shipped) != strings.TrimSpace(activateAQL) {
		t.Fatalf("activateAQL const drifted from shipped source:\ncompiled=%q\nshipped=%q", activateAQL, shipped)
	}
	return shipped
}

func gacActivateAQLHasNoCompare(t *testing.T) (bool, []string) {
	t.Helper()
	missing := make([]string, 0, len(gacActivateAQLMustHold))
	for _, required := range gacActivateAQLMustHold {
		if !strings.Contains(activateAQL, required) {
			missing = append(missing, required)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("activateAQL no longer has the audited blind-patch shape (missing %v);\n%s", missing, activateAQL)
	}
	present := make([]string, 0, len(gacActivateAQLMustNotHold))
	for _, forbidden := range gacActivateAQLMustNotHold {
		if strings.Contains(activateAQL, forbidden) {
			present = append(present, forbidden)
		}
	}
	source := strings.Join(gacReadSource(t, "internal/backends/arango/internal/dataset/manifest_lifecycle.go"), "\n")
	if !strings.Contains(source, "FILTER manifest.dataset == active.dataset") {
		t.Fatalf("readActiveAQL lost its consistency filter; re-audit the read/write asymmetry claim")
	}
	return len(present) == 0, present
}

type gacRecorder struct {
	inner   arangostore.RowQueryer
	queries []string
}

func (r *gacRecorder) QueryRows(ctx context.Context, query string, batch int, bindVars map[string]interface{}, visit arangostore.RowVisitor) error {
	r.queries = append(r.queries, query)
	return r.inner.QueryRows(ctx, query, batch, bindVars, visit)
}

func (r *gacRecorder) since(mark int) []string {
	if mark > len(r.queries) {
		return nil
	}
	return r.queries[mark:]
}

func gacCountWriteLegs(queries []string) int {
	writes := 0
	for _, query := range queries {
		if strings.Contains(query, "UPDATE active WITH") {
			writes++
		}
	}
	return writes
}

func gacOpen(t *testing.T, budget time.Duration) (context.Context, *arangostore.Client, *Store, *gacRecorder) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := gacDatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, gacDatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, gacDatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client, err := arangostore.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("GAC_DATABASE_CREATED=%s (via production store.Open)", name)
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
		t.Logf("GAC_DATABASE_REMOVED=%s", name)
	})
	if err := client.Bootstrap(ctx, BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	recorder := &gacRecorder{inner: client}
	lifecycle, err := New(recorder)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	return ctx, client, lifecycle, recorder
}

func gacLoadStaged(t *testing.T, ctx context.Context, lifecycle *Store, project, generation, digest string) publication.Manifest {
	t.Helper()
	ref, err := publication.NewRef(project, generation)
	if err != nil {
		t.Fatalf("new ref %s/%s: %v", project, generation, err)
	}
	schema, err := publication.NewSchemaSnapshot("urn:gac-fb17-"+generation, "", digest, []string{"Patient"})
	if err != nil {
		t.Fatalf("schema snapshot %s: %v", generation, err)
	}
	manifest, err := publication.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("new manifest %s: %v", generation, err)
	}
	if _, err := lifecycle.CreateManifest(ctx, manifest); err != nil {
		t.Fatalf("CreateManifest %s: %v", generation, err)
	}
	staged, err := lifecycle.TransitionManifest(ctx, manifest, publication.StateStaged)
	if err != nil {
		t.Fatalf("TransitionManifest %s -> STAGED: %v", generation, err)
	}
	if !staged.IsStaged() {
		t.Fatalf("manifest %s not staged after transition: %s", generation, staged.State)
	}
	read, err := lifecycle.ReadManifest(ctx, ref)
	if err != nil || !read.IsStaged() {
		t.Fatalf("PERSISTED %s not staged: manifest=%+v err=%v", generation, read, err)
	}
	t.Logf("OBSERVED setup: generation %s persisted STAGED digest=%s... schemaIdentity.resourceTypes=%v manifestKey=%s", generation, digest[:12], read.SchemaIdentity.GeneratedResourceTypes, manifestDocumentKey(read.Dataset))
	return staged
}

func gacJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<unmarshalable %T>", value)
	}
	return string(raw)
}

func gacPointerDoc(t *testing.T, ctx context.Context, client *arangostore.Client, project string) map[string]any {
	t.Helper()
	key := activeDocumentKey(project)
	var doc map[string]any
	err := client.QueryRows(ctx, `LET d = DOCUMENT(@@c, @key) FILTER d != null RETURN d`, 1,
		map[string]interface{}{"@c": LifecycleCollection, "key": key},
		func(row map[string]any) error {
			data, err := json.Marshal(row)
			if err != nil {
				return err
			}
			var decoded map[string]any
			if err := json.Unmarshal(data, &decoded); err != nil {
				return err
			}
			doc = decoded
			return nil
		})
	if err != nil {
		t.Fatalf("read active_generation pointer %s: %v", key, err)
	}
	if doc == nil {
		t.Fatalf("missing active_generation pointer document %s", key)
	}
	return doc
}

func gacPointerRoleCount(t *testing.T, ctx context.Context, client *arangostore.Client, project string) int {
	t.Helper()
	count := 0
	err := client.QueryRows(ctx, `FOR d IN @@c FILTER d.recordType == @recordType AND d.project == @project RETURN {key: d._key, rev: d._rev}`, 8,
		map[string]interface{}{"@c": LifecycleCollection, "recordType": "active_generation", "project": project},
		func(row map[string]any) error {
			count++
			return nil
		})
	if err != nil {
		t.Fatalf("role-read active_generation pointer: %v", err)
	}
	return count
}

func gacField(doc map[string]any, field string) string {
	value, _ := doc[field].(string)
	return value
}

func gacPointerGeneration(doc map[string]any) string {
	datasetValue, _ := doc["dataset"].(map[string]any)
	generation, _ := datasetValue["generation"].(string)
	return generation
}

func gacPointerLine(doc map[string]any) string {
	return fmt.Sprintf("{_id=%s _rev=%s recordType=%v project=%v dataset.generation=%s manifestKey=%s}",
		gacField(doc, "_id"), gacField(doc, "_rev"), doc["recordType"], doc["project"], gacPointerGeneration(doc), gacField(doc, "manifestKey"))
}

type gacFlip struct {
	label       string
	candidate   string
	err         error
	conflict    bool
	roundTrips  int
	writeLegs   int
	before      map[string]any
	after       map[string]any
	resolvedGen string
	resolveErr  error
}

func (f gacFlip) blind() bool {
	return f.err == nil && !f.conflict &&
		gacPointerGeneration(f.after) == f.candidate && f.resolvedGen == f.candidate &&
		f.writeLegs >= 1 && gacField(f.before, "_rev") != gacField(f.after, "_rev")
}

func TestGenerationActivationBlindlyFlipsSharedPointerAgainstArango(t *testing.T) {
	ctx, client, lifecycle, recorder := gacOpen(t, 240*time.Second)
	gacCheckCitations(t)
	shippedActivateAQL := gacShippedActivateAQL(t)
	noCompare, forbiddenPresent := gacActivateAQLHasNoCompare(t)

	const project = "gac-fb17-project"
	const g1 = "gac-fb17-gen-1"
	const g2 = "gac-fb17-gen-2"
	const g3 = "gac-fb17-gen-3"

	staged := map[string]publication.Manifest{}
	for _, spec := range []struct{ generation, digest string }{
		{g1, strings.Repeat("a", 64)},
		{g2, strings.Repeat("b", 64)},
		{g3, strings.Repeat("c", 64)},
	} {
		staged[spec.generation] = gacLoadStaged(t, ctx, lifecycle, project, spec.generation, spec.digest)
	}
	if before, err := lifecycle.ResolveActiveManifest(ctx, project); !errors.Is(err, publication.ErrNoActiveGeneration) {
		t.Fatalf("PRECONDITION BROKEN: no generation activated yet, want ErrNoActiveGeneration; got generation=%q err=%v", before.Dataset.Generation, err)
	}
	t.Logf("OBSERVED precondition: three STAGED manifests (G1=%s G2=%s G3=%s), distinct schemaIdentity digests; ResolveActiveManifest= ErrNoActiveGeneration as expected", g1, g2, g3)

	runActivate := func(label, generation string) gacFlip {
		before := gacPointerDoc(t, ctx, client, project)
		mark := len(recorder.queries)
		err := lifecycle.Activate(ctx, staged[generation])
		queries := recorder.since(mark)
		after := gacPointerDoc(t, ctx, client, project)
		flip := gacFlip{
			label: label, candidate: generation, err: err,
			conflict:   errors.Is(err, ErrActivationConflict),
			roundTrips: len(queries), writeLegs: gacCountWriteLegs(queries),
			before: before, after: after,
		}
		resolved, resolveErr := lifecycle.ResolveActiveManifest(ctx, project)
		flip.resolveErr = resolveErr
		if resolveErr == nil {
			flip.resolvedGen = resolved.Dataset.Generation
		}
		t.Logf("OBSERVED [%s] Activate(%s): err=%v ErrActivationConflict=%v roundTrips=%d activateAQL-write-legs=%d", label, generation, err, flip.conflict, flip.roundTrips, flip.writeLegs)
		t.Logf("OBSERVED [%s] pointer.doc BEFORE: %s", label, gacPointerLine(before))
		t.Logf("OBSERVED [%s] pointer.doc AFTER : %s", label, gacPointerLine(after))
		t.Logf("OBSERVED [%s] pointer.full-doc AFTER: %s", label, gacJSON(t, after))
		t.Logf("OBSERVED [%s] ResolveActiveManifest -> generation=%q err=%v", label, flip.resolvedGen, flip.resolveErr)
		if flip.err != nil {
			t.Logf("OBSERVED [%s] error text: %v", label, flip.err)
		}
		return flip
	}

	t.Log("PRE-STATED EXPECT LEG A.1: Activate(G1) -> err=nil, pointer.dataset.generation == G1 (first real activation).")
	t.Log("PRE-STATED EXPECT LEG A.2 (BUG): Activate(G2) -> err=nil, NO ErrActivationConflict, pointer.dataset.generation == G2, _rev churns: a cross-generation flip is accepted blindly.")
	t.Log("PRE-STATED EXPECT LEG A.3 (BUG): Activate(G1) AGAIN after G2 -> err=nil, NO ErrActivationConflict, pointer.dataset.generation == G1 again, _rev churns: last writer wins, silent flip back, no conflict error of any kind.")
	a1 := runActivate("A.1 G1 first", g1)
	if a1.err != nil || gacPointerGeneration(a1.after) != g1 {
		t.Fatalf("PRECONDITION BROKEN: baseline Activate(G1) failed (err=%v) or did not name G1 (pointer=%q)", a1.err, gacPointerGeneration(a1.after))
	}
	a2 := runActivate("A.2 G2 over G1", g2)
	a3 := runActivate("A.3 G1 again over G2", g1)

	t.Log("PRE-STATED EXPECT LEG B (BUG): reader resolves the active generation (observes G1 after LEG A), resolves its candidate G3 manifest (service.go:206) and only rechecks IsStaged (:210); a competing Activate(G2) moves the pointer in between; the reader's Activate(G3) (:213) then returns err=nil, the pointer lands on G3, and the competing G2 activation is silently discarded. No error, no revalidation of the stale read.")
	readerObserved, readerObserveErr := lifecycle.ResolveActiveManifest(ctx, project)
	t.Logf("OBSERVED [B.1 reader resolves active]: generation=%q err=%v (this is what the bulk-load caller sees before it picks a candidate)", readerObserved.Dataset.Generation, readerObserveErr)
	if readerObserveErr != nil || readerObserved.Dataset.Generation != g1 {
		t.Fatalf("PRECONDITION BROKEN: reader's initial active resolve should observe G1=%s, got %q err=%v", g1, readerObserved.Dataset.Generation, readerObserveErr)
	}
	candidateRef, refErr := publication.NewRef(project, g3)
	if refErr != nil {
		t.Fatalf("new ref: %v", refErr)
	}
	readerCandidate, candidateErr := lifecycle.ReadManifest(ctx, candidateRef)
	if candidateErr != nil || !readerCandidate.IsStaged() {
		t.Fatalf("PRECONDITION BROKEN: service.go:206-equivalent ReadManifest(G3) failed: %+v err=%v", readerCandidate, candidateErr)
	}
	t.Logf("OBSERVED [B.2 reader resolves candidate]: ReadManifest(G3).IsStaged=%v state=%s (the only thing rechecked before :213 is IsStaged)", readerCandidate.IsStaged(), readerCandidate.State)
	competitor := runActivate("B.3 competing Activate(G2)", g2)
	if competitor.err != nil || gacPointerGeneration(competitor.after) != g2 {
		t.Fatalf("PRECONDITION BROKEN: interleaved Activate(G2) failed (err=%v) or did not name G2 (pointer=%q)", competitor.err, gacPointerGeneration(competitor.after))
	}
	reader := runActivate("B.4 stale reader Activate(G3)", g3)
	lost := gacPointerGeneration(competitor.after)
	t.Logf("OBSERVED [B.5 lost update]: reader read active=%q at B.1; pointer moved to %q at B.3; reader's Activate(G3) err=%v landed %q => %q activation was overwritten with no error and no revalidation",
		readerObserved.Dataset.Generation, lost, reader.err, gacPointerGeneration(reader.after), lost)

	t.Log("PRE-STATED EXPECT LEG C (SOUND): with G3 already active, Activate(G3) is the idempotent no-op branch of manifest_lifecycle.go:198-204 (`if active, err := s.ResolveActiveManifest(...); err == nil { if manifestIdentityEqual(active, candidate) { return nil } }`): err=nil, exactly ONE round trip (the pre-check read), ZERO activateAQL write legs, and pointer _rev does NOT churn. Same-generation re-activation must look different from LEG A/B cross-generation flips.")
	revBefore := gacField(gacPointerDoc(t, ctx, client, project), "_rev")
	c1 := runActivate("C.1 same generation again", g3)
	c2 := runActivate("C.2 same generation once more", g3)
	revAfter := gacField(gacPointerDoc(t, ctx, client, project), "_rev")
	soundLegHeld := c1.err == nil && c2.err == nil && revBefore == revAfter && c1.writeLegs == 0 && c2.writeLegs == 0 && c1.roundTrips == 1 && c2.roundTrips == 1
	if !soundLegHeld {
		t.Errorf("SOUND-LEG DEVIATION (not F-B17 itself, but it would contradict the idempotency design): same-generation re-activation churned the pointer. _rev %s -> %s; C.1 roundTrips=%d writeLegs=%d err=%v; C.2 roundTrips=%d writeLegs=%d err=%v",
			revBefore, revAfter, c1.roundTrips, c1.writeLegs, c1.err, c2.roundTrips, c2.writeLegs, c2.err)
	} else {
		t.Logf("OBSERVED LEG C (sound control, as pre-stated): same-generation re-activation is the :198-204 no-op branch — err=nil, 1 round trip, 0 write legs, _rev stable at %s across both calls.", revBefore)
	}

	roleMatches := gacPointerRoleCount(t, ctx, client, project)
	t.Logf("OBSERVED pointer role-read cross-check: %d document(s) match recordType==\"active_generation\" AND project==%s (the selector used by readActiveAQL)", roleMatches, project)

	crossGeneration := []gacFlip{a2, a3, competitor, reader}
	blindCount, refused := 0, make([]string, 0, len(crossGeneration))
	for _, flip := range crossGeneration {
		if flip.blind() {
			blindCount++
			continue
		}
		refused = append(refused, fmt.Sprintf("[%s candidate=%s err=%v conflict=%v pointerAfter=%q resolved=%q writeLegs=%d revChurn=%v]",
			flip.label, flip.candidate, flip.err, flip.conflict, gacPointerGeneration(flip.after), flip.resolvedGen, flip.writeLegs,
			gacField(flip.before, "_rev") != gacField(flip.after, "_rev")))
	}
	t.Logf("VERDICT INPUTS: cross-generation activations=%d blind(err=nil, pointer moved, write leg, _rev churned)=%d refused=%d; activateAQL has no value/revision/lease compare=%v forbidden-tokens-present=%v",
		len(crossGeneration), blindCount, len(refused), noCompare, forbiddenPresent)

	if blindCount >= 1 && noCompare {
		t.Fatalf("FAILED-ON-PURPOSE: F-B17 CONFIRMED against real Arango (database owned+dropped by this test). %d/%d cross-generation Store.Activate calls blindly overwrote the shared active_generation pointer with err=nil and no revalidation of any kind: A.2 G1->G2 err=%v, A.3 G2->G1(err=%v), B.3 ->G2 err=%v, B.4 stale reader ->G3 err=%v (the competing %q activation was silently discarded after the reader's resolve; the pointer had already moved). The pointer's previous value is never compared: the missing guard is at internal/backends/arango/internal/dataset/manifest_lifecycle.go:334-335 — `UPDATE active WITH { dataset: candidate.dataset, manifestKey: candidate._key } IN @@lifecycle_collection OPTIONS { ignoreRevs: false, mergeObjects: false }` — which lacks any `FILTER active.dataset == @candidate.dataset` (and any `FILTER active.manifestKey != ...`, expected-revision bind, or lease token; forbidden tokens present in the shipped query: %v). ignoreRevs:false supplies no protection here because no _rev is submitted with the patch. The read-then-act half is manifest_lifecycle.go:198-204 (`if active, err := s.ResolveActiveManifest(ctx, candidate.Dataset.Project); err == nil { if manifestIdentityEqual(active, candidate) { return nil } }`) — a separate HTTP round trip whose result the write never re-reads or re-checks (OBSERVED roundTrips for a cross-generation Activate = %d, i.e. pre-check read then blind write); the bulk-load caller adds no protection either (service.go:206 resolves the candidate, :210 rechecks only IsStaged, :213 activates). The single rejection this path can raise, manifest_lifecycle.go:243 ErrActivationConflict, is about the CANDIDATE (\"was not a persisted staged manifest with a valid active pointer\"), never about the pointer having moved — OBSERVED ErrActivationConflict=%v on the cross-generation flips.\nFIX SHAPE (already present in this package for the release pointer): internal/backends/arango/internal/dataset/release_store.go:167 `FILTER currentRevision == @expected_revision OR alreadyActive` — a compare against the pointer's current state whose failure yields ErrReleaseActivationConflict instead of a silent write; activateAQL needs the equivalent (FILTER on active.dataset/active.manifestKey, or a submitted _rev / revision token) instead of the unconditional UPDATE.\nDISTINCTNESS: this is the manifest-generation activation writer (activateAQL), not F-B13's CompareAndSwapActivateRelease generationPointer patch (release_store.go:170-173), which was never called here (no publication/explorer collections bootstrapped). Third writer on record, not driven: internal/backends/arango/internal/explorer/activation.go:98 + :38.\nSOUND LEG (correctly behaving, not part of the finding): same-generation re-activation hit the idempotent branch, err=nil with 0 write legs and stable _rev (%s) => held=%v.\nSHIPPED activateAQL (internal/backends/arango/internal/dataset/manifest_lifecycle.go:318-337) quoted verbatim:\n%s",
			blindCount, len(crossGeneration), a2.err, a3.err, competitor.err, reader.err, lost, forbiddenPresent, a2.roundTrips, a2.conflict, revBefore, soundLegHeld, shippedActivateAQL)
	}
	if blindCount == 0 && len(refused) > 0 {
		t.Logf("F-B17 PREVENTED: every cross-generation Store.Activate was refused or left the pointer alone (no blind err=nil overwrite observed): %s", strings.Join(refused, " "))
		t.Logf("OBSERVED PREVENTED quotes the guarding logic to re-verify by hand: manifest_lifecycle.go:334-335 (UPDATE), :198-204 (pre-check), :242-243 (ErrActivationConflict). PASS.")
		return
	}
	t.Fatalf("INCONCLUSIVE F-B17 adjudication: blind=%d/%d but activateAQL-no-compare=%v (forbidden tokens present: %v); refused shapes: %s", blindCount, len(crossGeneration), noCompare, forbiddenPresent, strings.Join(refused, " "))
}
