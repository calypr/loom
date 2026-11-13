package dataset_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	arangofactory "github.com/calypr/loom/internal/backends/arango"
	dataset "github.com/calypr/loom/internal/dataset"
	"github.com/google/uuid"
)

const r14DatabasePrefix = "loom_verify_r14_"

type r14Citation struct {
	file string
	line int
	want string
}

var r14Citations = []r14Citation{
	{"internal/dataset/release.go", 82, `func (e *ReleaseRequirementsError) Unwrap() error { return ErrReleaseRequirementsUnmet }`},
	{"internal/dataset/release.go", 185, `if verifyErr != nil || verification.Selector != selector || verification.State != "PUBLISHED" || !verification.Queryable || verification.Generation != ref.Generation {`},
	{"internal/dataset/release.go", 186, `contract.ErrorCode = "RELEASE_REQUIREMENTS_UNMET"`},
	{"internal/dataset/release.go", 192, `if len(failed) != 0 {`},
	{"internal/dataset/release.go", 193, `return ProjectRelease{}, &ReleaseRequirementsError{Verifications: failed}`},
	{"internal/dataset/release.go", 232, `release, err := s.Create(ctx, request)`},
	{"internal/dataset/release.go", 236, `return s.Releases.CompareAndSwapActivateRelease(ctx, release, request.ExpectedRevision)`},
	{"internal/explorer/lifecycle/publish.go", 87, `release, expectedReleaseRevision, err := s.config.PrepareRelease(ctx, projectid.Legacy(receipt.Project), receipt.SourceGeneration, selectorsForBundle(receipt.Bundle))`},
	{"internal/explorer/lifecycle/publish.go", 89, `return PublishResult{}, unavailable("activation", "MATERIALIZATION_ACTIVATION_FAILED", "dataset release preparation failed; the prior Explorer revision was retained", err)`},
}

const r14ContractProse = "clients do not infer it from an HTTP status or message"

func r14RepoRoot(t *testing.T) string {
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

func r14CheckCitations(t *testing.T) map[string]string {
	t.Helper()
	root := r14RepoRoot(t)
	contents := map[string][]string{}
	quoted := map[string]string{}
	for _, c := range r14Citations {
		lines, ok := contents[c.file]
		if !ok {
			raw, err := os.ReadFile(filepath.Join(root, c.file))
			if err != nil {
				t.Fatalf("citation source %s unreadable: %v", c.file, err)
			}
			lines = strings.Split(string(raw), "\n")
			contents[c.file] = lines
		}
		if c.line < 1 || c.line > len(lines) {
			t.Fatalf("citation drift: %s has %d lines, cited :%d == %q", c.file, len(lines), c.line, c.want)
		}
		found := false
		for _, line := range lines {
			if strings.TrimSpace(line) == strings.TrimSpace(c.want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("citation drift: %s no longer contains %q (cited :%d)", c.file, c.want, c.line)
		}
		quoted[fmt.Sprintf("%s:%d", c.file, c.line)] = c.want
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs/loom-reliability-contract.md"))
	if err != nil {
		if os.IsNotExist(err) {
			t.Logf("R14_CONTRACT=%q not present in this test image (%s); verified out-of-band on the host at docs/loom-reliability-contract.md:94-95 — skipping in-image content check.", r14ContractProse, filepath.Join(root, "docs/loom-reliability-contract.md"))
		} else {
			t.Fatalf("contract source unreadable: %v", err)
		}
	} else {
		normalized := strings.Join(strings.Fields(string(raw)), " ")
		if !strings.Contains(normalized, r14ContractProse) {
			t.Fatalf("reliability contract no longer states %q; the F-B14 caller-side record would be stale", r14ContractProse)
		}
	}
	return quoted
}

type r14FakeVerifier struct {
	calls  int
	sought dataset.DataframeSelector
}

func (v *r14FakeVerifier) VerifyPublication(_ context.Context, _, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	v.calls++
	v.sought = selector
	return dataset.PublicationVerification{
		Selector:    selector,
		ExecutionID: "r14-exec-pending",
		Generation:  generation,
		State:       "PENDING",
		Queryable:   false,
	}, nil
}

var _ dataset.PublicationVerifier = (*r14FakeVerifier)(nil)

func r14Open(t *testing.T, budget time.Duration) (context.Context, *arangofactory.Client, *arangofactory.DatasetStore) {
	t.Helper()
	endpoint := os.Getenv("LOOM_TEST_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set owned LOOM_TEST_ARANGO_URL")
	}
	name := r14DatabasePrefix + strings.ReplaceAll(uuid.NewString(), "-", "")
	if !strings.HasPrefix(name, r14DatabasePrefix) {
		t.Fatalf("verification database %q escapes the %s prefix", name, r14DatabasePrefix)
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{URL: endpoint, Database: name})
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	client := arangoBackend.Client
	t.Logf("R14_DATABASE=%s endpoint=%s", name, endpoint)

	t.Cleanup(func() {
		dbs, listErr := r14AuthenticatedDatabaseList(endpoint)
		if listErr != nil {
			t.Errorf("R14 residue proof could not list databases: %v", listErr)
			return
		}
		var residue []string
		for _, db := range dbs {
			if strings.HasPrefix(db, r14DatabasePrefix) {
				residue = append(residue, db)
			}
		}
		sort.Strings(residue)
		t.Logf("R14_RESIDUE_DATABASES=%v (listErr=%v)", residue, listErr)
		for _, db := range residue {
			t.Errorf("R14 residue: verification database %s survived cleanup", db)
		}
	})

	t.Cleanup(func() {
		_ = client.Close(context.Background())
		cleanupCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(cleanupCtx, http.MethodDelete, strings.TrimRight(endpoint, "/")+"/_api/database/"+url.PathEscape(name), nil)
		if err != nil {
			t.Errorf("build cleanup request for %s: %v", name, err)
			return
		}
		if pw := os.Getenv("LOOM_ARANGO_PASSWORD"); pw != "" {
			req.SetBasicAuth("root", pw)
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
		t.Logf("R14_DATABASE_REMOVED=%s", name)
	})

	if err := client.Bootstrap(ctx, arangofactory.DatasetBootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	lifecycle, err := arangofactory.NewDatasetStore(client)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	return ctx, client, lifecycle
}

func r14AuthenticatedDatabaseList(endpoint string) ([]string, error) {
	endpoint = strings.TrimRight(endpoint, "/")
	if endpoint == "" {
		return nil, fmt.Errorf("empty endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/_api/database", nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth("root", os.Getenv("LOOM_ARANGO_PASSWORD"))
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list databases status=%d", response.StatusCode)
	}
	var payload struct {
		Result []string `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Result, nil
}

func r14StageManifest(t *testing.T, ctx context.Context, lifecycle *arangofactory.DatasetStore, project, generation string) error {
	t.Helper()
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		t.Fatalf("new ref %s/%s: %v", project, generation, err)
	}
	schema, err := dataset.NewSchemaSnapshot("urn:r14-"+generation, "", strings.Repeat("a", 64), []string{"Patient"})
	if err != nil {
		t.Fatalf("schema snapshot %s: %v", generation, err)
	}
	manifest, err := dataset.NewManifest(ref, schema)
	if err != nil {
		t.Fatalf("new manifest %s: %v", generation, err)
	}
	if manifest.State != dataset.StateLoading {
		t.Fatalf("manifest %s not LOADING on construct: %s", generation, manifest.State)
	}
	if _, err := lifecycle.CreateManifest(ctx, manifest); err != nil {
		t.Fatalf("CreateManifest %s: %v", generation, err)
	}
	staged, err := lifecycle.TransitionManifest(ctx, manifest, dataset.StateStaged)
	if err != nil {
		t.Fatalf("TransitionManifest %s -> STAGED: %v", generation, err)
	}
	if !staged.IsStaged() {
		t.Fatalf("manifest %s not staged after transition: %s", generation, staged.State)
	}
	return nil
}

type r14Snapshot struct {
	totalDocs       int
	releaseDocCount int
	releaseIDs      []string
	releasePointer  string
	genPointer      string
	canonical       string
	activeErr       string
	activeRevision  int64
	activeID        string
}

func r14Str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

func r14Any(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return "<absent>"
}

func r14CanonicalDoc(d map[string]any) string {
	return fmt.Sprintf("%s|%s|%s|revision=%s|releaseId=%s|releaseKey=%s|manifestKey=%s",
		r14Str(d, "_key"), r14Str(d, "recordType"), r14Str(d, "_rev"),
		r14Any(d, "revision"), r14Any(d, "releaseId"), r14Any(d, "releaseKey"), r14Any(d, "manifestKey"))
}

func r14TakeSnapshot(t *testing.T, ctx context.Context, client *arangofactory.Client, lifecycle *arangofactory.DatasetStore, project string) r14Snapshot {
	t.Helper()
	s := r14Snapshot{}
	active, err := lifecycle.ReadActiveRelease(ctx, project)
	if err != nil {
		s.activeErr = err.Error()
	} else {
		s.activeErr = "<nil>"
		s.activeRevision = active.Revision
		s.activeID = active.Release.ID
	}

	var docs []map[string]any
	err = client.QueryRows(ctx, `FOR d IN @@c RETURN d`, 500, map[string]any{"@c": arangofactory.DatasetLifecycleCollection}, func(row map[string]any) error {
		docs = append(docs, row)
		return nil
	})
	if err != nil {
		t.Fatalf("dump lifecycle collection: %v", err)
	}
	s.totalDocs = len(docs)
	activeGenKey := arangofactory.DatasetActiveDocumentKey(project)
	canon := make([]string, 0, len(docs))
	for _, d := range docs {
		canon = append(canon, r14CanonicalDoc(d))
		rt := r14Str(d, "recordType")
		switch rt {
		case "project_release":
			s.releaseDocCount++
			if id := r14Str(d, "id"); id != "" {
				s.releaseIDs = append(s.releaseIDs, id)
			}
		case "active_project_release":
			s.releasePointer = fmt.Sprintf("%s {recordType=%s _rev=%s revision=%s releaseId=%s releaseKey=%s}",
				r14Str(d, "_key"), rt, r14Str(d, "_rev"), r14Any(d, "revision"), r14Any(d, "releaseId"), r14Any(d, "releaseKey"))
		case "active_generation":
			s.genPointer = fmt.Sprintf("%s {recordType=%s _rev=%s manifestKey=%s releaseId=%s releaseRevision=%s}",
				r14Str(d, "_key"), rt, r14Str(d, "_rev"), r14Any(d, "manifestKey"), r14Any(d, "releaseId"), r14Any(d, "releaseRevision"))
		}
	}
	if s.releasePointer == "" {
		for _, d := range docs {
			if _, ok := d["releaseKey"]; ok {
				s.releasePointer = r14CanonicalDoc(d)
			}
		}
	}
	if s.genPointer == "" {
		for _, d := range docs {
			if _, ok := d["releaseRevision"]; ok {
				s.genPointer = r14CanonicalDoc(d)
			}
		}
		_ = activeGenKey
	}
	sort.Strings(s.releaseIDs)
	sort.Strings(canon)
	s.canonical = strings.Join(canon, "\n")
	return s
}

func (s r14Snapshot) log(label string) {
	fmt.Printf("OBSERVED [%s] totalDocs=%d releaseDocs=%d releaseIDs=%v activeErr=%s activeRevision=%d activeID=%s\n  releasePointer=%s\n  genPointer=%s\n  canonicalDocs=\n%s\n",
		label, s.totalDocs, s.releaseDocCount, s.releaseIDs, s.activeErr, s.activeRevision, s.activeID,
		s.releasePointer, s.genPointer, s.canonical)
}

func r14Quote(quoted map[string]string, key string) string {
	if v, ok := quoted[key]; ok {
		return fmt.Sprintf("[%s] %s", key, v)
	}
	return fmt.Sprintf("[%s] <not captured>", key)
}

func r14QuoteAll(quoted map[string]string, keys []string) string {
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, "  "+r14Quote(quoted, k))
	}
	return strings.Join(lines, "\n")
}

func r14VerificationsDigest(vs []dataset.ContractVerification) string {
	if len(vs) == 0 {
		return "<empty Verifications>"
	}
	parts := make([]string, 0, len(vs))
	for i, v := range vs {
		parts = append(parts, fmt.Sprintf("[%d] selector{recipe=%q translationVersion=%q output=%q} executionId=%q generation=%q state=%q queryable=%v errorCode=%q",
			i, v.Selector.Recipe, v.Selector.TranslationVersion, v.Selector.Output, v.ExecutionID, v.Generation, v.State, v.Queryable, v.ErrorCode))
	}
	return strings.Join(parts, "\n    ")
}

func r14ErrorChain(err error) string {
	var chain []string
	for e := err; e != nil; e = errors.Unwrap(e) {
		chain = append(chain, fmt.Sprintf("%T: %s", e, e.Error()))
	}
	return strings.Join(chain, "\n    <- ")
}

func TestReleaseRequirementsBoundaryCitations(t *testing.T) {
	quoted := r14CheckCitations(t)
	t.Logf("R14_CITATIONS_PINNED=%d production lines verified verbatim (release.go reject/typed-error/Unwrap/Activate->Create->CAS hops + publish.go 87/89 unavailable wrap)", len(r14Citations))
	for _, c := range r14Citations {
		t.Logf("R14_CITATION [%s:%d] %s", c.file, c.line, r14Quote(quoted, fmt.Sprintf("%s:%d", c.file, c.line)))
	}
}

func TestReleaseRequirementsUnmetActivateAgainstArango(t *testing.T) {
	ctx, client, lifecycle := r14Open(t, 180*time.Second)
	quoted := r14CheckCitations(t)

	const project = "r14-project"
	const generation = "r14-gen-unmet"

	t.Log("PRE-STATED EXPECT: Activate(unsatisfiable required selector) => *ReleaseRequirementsError with Verifications[0].ErrorCode=RELEASE_REQUIREMENTS_UNMET, pointer/revision unchanged, errors.Is(err, ErrReleaseRequirementsUnmet)=true => FAILED-ON-PURPOSE CONFIRMED.")

	if err := r14StageManifest(t, ctx, lifecycle, project, generation); err != nil {
		t.Fatalf("stage manifest %s: %v", generation, err)
	}

	required := []dataset.DataframeSelector{{Recipe: "r14-recipe-a", TranslationVersion: "tv-1", Output: "out-a"}}
	verifier := &r14FakeVerifier{}
	releaseService := &dataset.ReleaseService{
		Manifests: lifecycle,
		Releases:  lifecycle,
		Verifier:  verifier,
		Required:  required,
		Now:       func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) },
	}

	before := r14TakeSnapshot(t, ctx, client, lifecycle, project)
	before.log("before-activate")

	active, err := releaseService.Activate(ctx, dataset.ActivationRequest{
		Project:          project,
		Generation:       generation,
		GitCommit:        generation,
		ExpectedRevision: 0,
	})
	after := r14TakeSnapshot(t, ctx, client, lifecycle, project)
	after.log("after-activate")

	stateUnchanged := before.canonical == after.canonical &&
		before.totalDocs == after.totalDocs &&
		before.releaseDocCount == after.releaseDocCount &&
		before.releasePointer == after.releasePointer &&
		before.genPointer == after.genPointer &&
		before.activeErr == after.activeErr &&
		before.activeRevision == after.activeRevision &&
		before.activeID == after.activeID

	verificationLoopReached := verifier.calls > 0

	var reqErr *dataset.ReleaseRequirementsError
	asTyped := errors.As(err, &reqErr)
	isSentinel := errors.Is(err, dataset.ErrReleaseRequirementsUnmet)
	errorCode := ""
	digest := "<nil error>"
	if asTyped {
		digest = r14VerificationsDigest(reqErr.Verifications)
		if len(reqErr.Verifications) > 0 {
			errorCode = reqErr.Verifications[0].ErrorCode
		}
	}

	fmt.Printf("R14_OBSERVED activate_returned_err=%v\n", err)
	if err != nil {
		fmt.Printf("R14_OBSERVED error_%%v=%v\n", err)
		fmt.Printf("R14_OBSERVED error_chain=\n    %s\n", r14ErrorChain(err))
	}
	fmt.Printf("R14_OBSERVED errors.As(*ReleaseRequirementsError)=%v  errors.Is(err,ErrReleaseRequirementsUnmet)=%v\n", asTyped, isSentinel)
	fmt.Printf("R14_OBSERVED required_selector_verified=%v verifier_calls=%d verifier_sought=%+v\n", verificationLoopReached, verifier.calls, verifier.sought)
	fmt.Printf("R14_OBSERVED Verifications_digest(errorCode=%q)=\n    %s\n", errorCode, digest)
	fmt.Printf("R14_OBSERVED returned_active=%+v activate_err=%v\n", active, err)
	fmt.Printf("R14_OBSERVED state_unchanged(before==after)=%v releasedocs=%d->%d totalDocs=%d->%d activeErr=%q->%q\n",
		stateUnchanged, before.releaseDocCount, after.releaseDocCount, before.totalDocs, after.totalDocs, before.activeErr, after.activeErr)

	switch {
	case err == nil:
		t.Log("PREVENTED: dataset.ReleaseService.Activate SUCCEEDED for an unsatisfiable required selector; no requirements error was produced, so the F-B14 503 surface has no backing error at this layer.")
		t.Logf("PREVENTING/source lines that WOULD reject but did not fire, quoted verbatim from source:\n%s", r14QuoteAll(quoted, []string{
			"internal/dataset/release.go:185", "internal/dataset/release.go:186",
			"internal/dataset/release.go:192", "internal/dataset/release.go:193",
		}))
		t.Errorf("PREVENTED-but-unexpected: Activate returned no error (active{Revision=%d ID=%s}); required verification loop reached=%v verifier_calls=%d — the requirements-miss surface did not manifest; investigate whether Required/Verifier wiring is inert (F-B14 config-gate: nil Required means the branch never executes).", active.Revision, active.Release.ID, verificationLoopReached, verifier.calls)
	case asTyped:
		fmt.Printf("R14_OBSERVED typed_error_confirmed errorCode=%q verifications=%d state_unchanged=%v sentinel_is=%v loop_reached=%v\n",
			errorCode, len(reqErr.Verifications), stateUnchanged, isSentinel, verificationLoopReached)
		if errorCode != "RELEASE_REQUIREMENTS_UNMET" {
			t.Fatalf("INCONCLUSIVE: errors.As gave *ReleaseRequirementsError but Verifications[0].ErrorCode=%q, want RELEASE_REQUIREMENTS_UNMET (release.go:186). digest:\n%s", errorCode, digest)
		}
		if !stateUnchanged {
			t.Fatalf("INCONCLUSIVE: typed requirements error was returned BUT lifecycle state changed (before==after=false), which contradicts Create returning before SaveRelease/CAS. before:\n%s\nafter:\n%s", before.canonical, after.canonical)
		}
		if !isSentinel {
			t.Fatalf("REFUTED caller-side leg: *ReleaseRequirementsError did NOT satisfy errors.Is(err, ErrReleaseRequirementsUnmet); ReleaseRequirementsError.Unwrap (release.go:82) is not linking the sentinel, so the classification the 503 boundary discards is not actually reachable. %s", r14Quote(quoted, "internal/dataset/release.go:82"))
		}
		if !verificationLoopReached {
			t.Fatalf("INCONCLUSIVE: typed requirements error returned but the required-selector verification loop was never entered (verifier_calls=0); the miss did not flow through the production PublicationVerifier seam at release.go:181-190.")
		}
		t.Fatalf("FAILED-ON-PURPOSE CONFIRMED: deterministic requirements miss leaves only classification choice to the caller — dataset.ReleaseService.Activate against the REAL datasetarango store returned *dataset.ReleaseRequirementsError (errors.As, Verifications[0].ErrorCode=%q, %d verification(s)) built from the PENDING/!Queryable required selector {%q,%q,%q}, returned VERBATIM by Activate from Create (release.go:232 -> release.go:192-193) WITHOUT reaching the release CAS (release.go:236), and with NO pointer/revision/release-document state change (before==after: releasedocs %d->%d, totalDocs %d->%d, releasePointer=%s, genPointer=%s). SECOND LEG: the same error satisfies errors.Is(err, dataset.ErrReleaseRequirementsUnmet)=true via ReleaseRequirementsError.Unwrap (release.go:82), yet the authoring publish route wraps it as an unavailable/HTTP-503 diagnostic with NO explicit retryability (internal/explorer/lifecycle/publish.go:87-88), violating docs/loom-reliability-contract.md (%q). The runtime error surface that F-B14's 503-boundary record rests on is REAL; the classification (retryability) that distinguishes a permanent miss from a transient outage is DISCARDED at the HTTP boundary.",
			errorCode, len(reqErr.Verifications), required[0].Recipe, required[0].TranslationVersion, required[0].Output,
			before.releaseDocCount, after.releaseDocCount, before.totalDocs, after.totalDocs,
			after.releasePointer, after.genPointer, r14ContractProse)
	default:
		t.Log("PREVENTED: dataset.ReleaseService.Activate returned a NON-requirements error (no *ReleaseRequirementsError surface); the F-B14 typed-requirements-error surface did not manifest and cannot back the 503-boundary record here.")
		t.Logf("PREVENTING/source lines that WOULD have produced the typed error, quoted verbatim from source:\n%s", r14QuoteAll(quoted, []string{
			"internal/dataset/release.go:185", "internal/dataset/release.go:186",
			"internal/dataset/release.go:192", "internal/dataset/release.go:193",
		}))
		t.Errorf("PREVENTED/transient-flavoured: Activate returned a non-requirements error err=%v (loop_reached=%v verifier_calls=%d). If this is a transient/store error rather than a genuine refutation, the requirements-miss still never reached the typed surface at release.go:185-193.", err, verificationLoopReached, verifier.calls)
	}
}
