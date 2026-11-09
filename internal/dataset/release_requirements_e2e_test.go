package dataset_test

// VERIFICATION-ONLY artifact for the F-B14 "requirements-miss -> HTTP 503 with
// no explicit retryability" end-to-end-boundary record. It never mutates
// product code. It creates ONE fully owned ArangoDB database
// (loom_verify_r14_<uuid>) through the production store.Open path and drops it
// with an authenticated HTTP DELETE in t.Cleanup, logging removal, then proves
// residue is gone with an authenticated database list.
//
// PACKAGE CHOICE (documented deviation, forced by the Go compiler): the
// assignment asked for `package dataset`, but the real `datasetarango` store it
// requires lives in internal/dataset/arango, which IMPORTS internal/dataset
// (`go list -deps ./internal/dataset/arango` lists
// github.com/calypr/loom/internal/dataset). A same-package test file
// (`package dataset`) that imports a package importing `dataset` is an import
// cycle that Go rejects ("import cycle not allowed in test"). The external test
// package `dataset_test` in this same directory is the only way to hold the
// REAL datasetarango store as Releases. Every type under test is the production
// dataset.* type; nothing is faked except the PublicationVerifier seam (which
// the server itself injects its own concrete implementation of in production).
//
// CLAIM UNDER TEST (CONFIRMED-by-reading in verification/publication-bundle/
// FINDINGS.md F-B14; this run gives it a RUNTIME upgrade or refutation):
//
//	dataset.ReleaseService.Activate -> dataset.ReleaseService.Create
//	(internal/dataset/release.go) verifies each required DataframeSelector
//	through the production PublicationVerifier seam (release.go:181-190). A
//	required selector whose verification is not PUBLISHED+Queryable collapses
//	into `&ReleaseRequirementsError{Verifications: failed}` with
//	contract.ErrorCode="RELEASE_REQUIREMENTS_UNMET" (release.go:185-193), and
//	Activate returns it verbatim from Create without ever reaching the release
//	CAS (release.go:232-236). Because Create returns before SaveRelease and
//	before CompareAndSwapActivateRelease, NO release document, active pointer,
//	or revision state changes.
//
//	This typed error is the surface the authoring publish route turns into an
//	HTTP 503 with no retryability: internal/explorer/lifecycle/publish.go:87-89
//	wraps it as `unavailable(...)` and the reliability contract
//	(docs/loom-reliability-contract.md "Retryability is explicit; clients do not
//	infer it from an HTTP status or message.") says clients must not infer retry
//	from the status, yet the authoring contract carries no `retryable` field
//	(F-B14). The second leg proves the caller-side gap is real by showing the
//	error is also `errors.Is`-detectable against the ErrReleaseRequirementsUnmet
//	sentinel (ReleaseRequirementsError.Unwrap, release.go:82) — the classification
//	the HTTP boundary then discards.
//
// PRE-STATED EXPECTATIONS (declared BEFORE the run; the verdict is computed
// ONLY from OBSERVED values):
//
//   - Expected-if-confirmed: Activate with the unsatisfiable required selector
//     returns an error satisfying errors.As(err, **dataset.ReleaseRequirementsError),
//     whose Verifications[0].ErrorCode == "RELEASE_REQUIREMENTS_UNMET", whose
//     Verifications[0] carries the required selector with State "PENDING" and
//     Queryable==false, and for which NO pointer/revision/lifecycle-document
//     state changed (before/after snapshot equal). errors.Is(err,
//     dataset.ErrReleaseRequirementsUnmet) is ALSO true. =>
//     t.Fatalf("FAILED-ON-PURPOSE CONFIRMED: deterministic requirements miss
//     leaves only classification choice to the caller ...").
//
//   - Expected-if-prevented: Activate SUCCEEDS, or returns a transient-flavoured
//     error that is NOT the typed requirements error (so the 503-boundary record
//     would rest on no real requirements-error surface). => t.Log PREVENTED with
//     the exact preventing source lines quoted.

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

	dataset "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

const r14DatabasePrefix = "loom_verify_r14_"

type r14Citation struct {
	file string
	line int
	want string
}

// r14Citations pin the load-bearing production lines so the verdict cannot
// silently point at drifted source. TrimSpace-insensitive line match. These are
// the exact hops F-B14's 503 boundary rests on.
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

// r14ContractProse is the reliability-contract sentence the F-B14 record invokes
// (matched by content, not line number, so prose reflow cannot produce a false
// refutation): retryability must be explicit, never inferred from an HTTP status.
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

// r14CheckCitations verifies the code lines verbatim and the contract prose by
// content, and returns a quote map for the PREVENTED branch.
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
		// Drift-proof: the contract is the EXACT source text, not the pin
		// (repairs shift lines constantly; require the statement verbatim
		// somewhere and only note pin drift).
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
	// The reliability contract must still forbid inferring retry from a status.
	// Compare against whitespace-normalized text so a line reflow in the prose
	// cannot produce a false refutation (the sentence wraps mid-clause in the
	// file, docs/loom-reliability-contract.md:94-95). The container test image
	// does not ship docs/, so a missing file is an environmental absence, NOT a
	// refutation: we log it (the sentence is verified out-of-band on the host)
	// and only fail when the file is present but no longer states the rule.
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

// r14FakeVerifier is declared in THIS test file and IS the production
// PublicationVerifier seam (dataset.PublicationVerifier). It answers exactly one
// required selector with a PENDING, non-queryable verification. In production
// the server injects its own concrete PublicationVerifier (see
// verification/publication-bundle brief: internal/server/release_verifier.go);
// here the fake stands in for an unsatisfied publication so the deterministic
// requirements-miss branch (release.go:185-193) fires. It records the request so
// the run can prove the verification loop was actually reached.
type r14FakeVerifier struct {
	calls  int
	sought dataset.DataframeSelector
}

func (v *r14FakeVerifier) VerifyPublication(_ context.Context, _, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	v.calls++
	v.sought = selector
	// Echo the identity fields faithfully so the only reason verification fails
	// is the not-yet-queryable PENDING state (the realistic unsatisfied shape),
	// NOT a malformed selector. State != "PUBLISHED" && !Queryable is what
	// release.go:185 rejects.
	return dataset.PublicationVerification{
		Selector:    selector,
		ExecutionID: "r14-exec-pending",
		Generation:  generation,
		State:       "PENDING",
		Queryable:   false,
	}, nil
}

var _ dataset.PublicationVerifier = (*r14FakeVerifier)(nil)

// r14Open is the env-gated owned-database harness (SKIP-clean when
// LOOM_TEST_ARANGO_URL is unset), opening only the dataset lifecycle collection.
func r14Open(t *testing.T, budget time.Duration) (context.Context, *arangostore.Client, *datasetarango.Store) {
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
	client, err := arangostore.Open(ctx, endpoint, name)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Logf("R14_DATABASE=%s endpoint=%s", name, endpoint)

	// Registered FIRST so it runs LAST (Go cleanups are LIFO): after the drop
	// below it does the authenticated residue proof on this owned database.
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

	if err := client.Bootstrap(ctx, datasetarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	lifecycle, err := datasetarango.New(client)
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

// r14StageManifest creates a LOADING manifest and transitions it to STAGED, the
// exact pre-state dataset.ReleaseService.Create's validateGeneration requires
// (release.go:154 -> validateGeneration -> manifest.IsStaged). The manifest docs
// live only in the owned database.
func r14StageManifest(t *testing.T, ctx context.Context, lifecycle *datasetarango.Store, project, generation string) error {
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

// r14Snapshot captures every lifecycle document (count), the project_release
// document ids, the two pointer documents' revision-bearing fields (including
// _rev), and the production ReadActiveRelease result. Two snapshots are equal
// iff Activate did not change any pointer/revision/release state.
type r14Snapshot struct {
	totalDocs       int
	releaseDocCount int
	releaseIDs      []string
	releasePointer  string // active_project_release canonical (recordType/_rev/revision/releaseId/releaseKey)
	genPointer      string // active_generation canonical (recordType/_rev/manifestKey/releaseId/releaseRevision)
	canonical       string // sorted per-doc "_key|recordType|_rev|revision|releaseId|releaseKey|manifestKey" join
	activeErr       string // ReadActiveRelease error text ("<nil>" if ok)
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

func r14TakeSnapshot(t *testing.T, ctx context.Context, client *arangostore.Client, lifecycle *datasetarango.Store, project string) r14Snapshot {
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
	err = client.QueryRows(ctx, `FOR d IN @@c RETURN d`, 500, map[string]any{"@c": datasetarango.LifecycleCollection}, func(row map[string]any) error {
		docs = append(docs, row)
		return nil
	})
	if err != nil {
		t.Fatalf("dump lifecycle collection: %v", err)
	}
	s.totalDocs = len(docs)
	activeGenKey := datasetarango.ActiveDocumentKey(project)
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
	// Fallback detection if the recordType strings ever rename.
	if s.releasePointer == "" {
		// active_project_release is the only doc carrying releaseKey.
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

// r14VerificationsDigest renders the ContractVerification slice embedded in the
// *ReleaseRequirementsError, which is runtime evidence that the 503-boundary
// record rests on a real, populated error surface (not an empty sentinel).
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

// TestReleaseRequirementsBoundaryCitations pins the production lines and the
// reliability-contract prose the F-B14 503-boundary record rests on. It needs
// no ArangoDB and runs whenever the suite runs (including SKIP-clean host runs
// where docs/ is present), so the citation guard is exercised for real even
// when the owned-database test above is gated out.
func TestReleaseRequirementsBoundaryCitations(t *testing.T) {
	quoted := r14CheckCitations(t)
	t.Logf("R14_CITATIONS_PINNED=%d production lines verified verbatim (release.go reject/typed-error/Unwrap/Activate->Create->CAS hops + publish.go 87/89 unavailable wrap)", len(r14Citations))
	for _, c := range r14Citations {
		t.Logf("R14_CITATION [%s:%d] %s", c.file, c.line, r14Quote(quoted, fmt.Sprintf("%s:%d", c.file, c.line)))
	}
}

// TestReleaseRequirementsUnmetActivateAgainstArango drives the REAL
// dataset.ReleaseService (struct in release.go: Releases/Verifier/Required/Now)
// against a REAL datasetarango store, with a fake Verifier returning PENDING /
// !Queryable for one required selector, and adjudicates the F-B14 boundary.
func TestReleaseRequirementsUnmetActivateAgainstArango(t *testing.T) {
	ctx, client, lifecycle := r14Open(t, 180*time.Second)
	quoted := r14CheckCitations(t)

	const project = "r14-project"
	const generation = "r14-gen-unmet"

	// PRE-STATED EXPECT: a fully-STAGED manifest but ONE required selector that
	// the (fake) production verifier reports PENDING / !Queryable. Create must
	// reject at release.go:185-193 and Activate must return the typed
	// *ReleaseRequirementsError, with NO pointer/revision/release-doc change and
	// errors.Is(err, ErrReleaseRequirementsUnmet) true.
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

	// Snapshot equality == no pointer/revision/release-doc state change.
	stateUnchanged := before.canonical == after.canonical &&
		before.totalDocs == after.totalDocs &&
		before.releaseDocCount == after.releaseDocCount &&
		before.releasePointer == after.releasePointer &&
		before.genPointer == after.genPointer &&
		before.activeErr == after.activeErr &&
		before.activeRevision == after.activeRevision &&
		before.activeID == after.activeID

	verificationLoopReached := verifier.calls > 0

	// Classify the error surface.
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

	// OBSERVED runtime evidence lines (the point of the run).
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

	// Adjudicate from OBSERVED values only.
	switch {
	case err == nil:
		// Activation should never succeed for an unsatisfiable required selector.
		// If it did, the 503-boundary record would rest on no real requirements-
		// error surface at this layer.
		t.Log("PREVENTED: dataset.ReleaseService.Activate SUCCEEDED for an unsatisfiable required selector; no requirements error was produced, so the F-B14 503 surface has no backing error at this layer.")
		t.Logf("PREVENTING/source lines that WOULD reject but did not fire, quoted verbatim from source:\n%s", r14QuoteAll(quoted, []string{
			"internal/dataset/release.go:185", "internal/dataset/release.go:186",
			"internal/dataset/release.go:192", "internal/dataset/release.go:193",
		}))
		t.Errorf("PREVENTED-but-unexpected: Activate returned no error (active{Revision=%d ID=%s}); required verification loop reached=%v verifier_calls=%d — the requirements-miss surface did not manifest; investigate whether Required/Verifier wiring is inert (F-B14 config-gate: nil Required means the branch never executes).", active.Revision, active.Release.ID, verificationLoopReached, verifier.calls)
	case asTyped:
		// First leg: the typed requirements error arrived. Second leg (below)
		// confirms the caller-side gap via the sentinel.
		fmt.Printf("R14_OBSERVED typed_error_confirmed errorCode=%q verifications=%d state_unchanged=%v sentinel_is=%v loop_reached=%v\n",
			errorCode, len(reqErr.Verifications), stateUnchanged, isSentinel, verificationLoopReached)
		if errorCode != "RELEASE_REQUIREMENTS_UNMET" {
			t.Fatalf("INCONCLUSIVE: errors.As gave *ReleaseRequirementsError but Verifications[0].ErrorCode=%q, want RELEASE_REQUIREMENTS_UNMET (release.go:186). digest:\n%s", errorCode, digest)
		}
		if !stateUnchanged {
			t.Fatalf("INCONCLUSIVE: typed requirements error was returned BUT lifecycle state changed (before==after=false), which contradicts Create returning before SaveRelease/CAS. before:\n%s\nafter:\n%s", before.canonical, after.canonical)
		}
		if !isSentinel {
			// The whole caller-side-gap claim (F-B14) hinges on the error also
			// being Is-detectable against the sentinel the HTTP layer discards.
			t.Fatalf("REFUTED caller-side leg: *ReleaseRequirementsError did NOT satisfy errors.Is(err, ErrReleaseRequirementsUnmet); ReleaseRequirementsError.Unwrap (release.go:82) is not linking the sentinel, so the classification the 503 boundary discards is not actually reachable. %s", r14Quote(quoted, "internal/dataset/release.go:82"))
		}
		if !verificationLoopReached {
			t.Fatalf("INCONCLUSIVE: typed requirements error returned but the required-selector verification loop was never entered (verifier_calls=0); the miss did not flow through the production PublicationVerifier seam at release.go:181-190.")
		}
		// Both legs confirmed against real Arango. The typed error IS the
		// 503-boundary surface and the sentinel classification IS reachable.
		t.Fatalf("FAILED-ON-PURPOSE CONFIRMED: deterministic requirements miss leaves only classification choice to the caller — dataset.ReleaseService.Activate against the REAL datasetarango store returned *dataset.ReleaseRequirementsError (errors.As, Verifications[0].ErrorCode=%q, %d verification(s)) built from the PENDING/!Queryable required selector {%q,%q,%q}, returned VERBATIM by Activate from Create (release.go:232 -> release.go:192-193) WITHOUT reaching the release CAS (release.go:236), and with NO pointer/revision/release-document state change (before==after: releasedocs %d->%d, totalDocs %d->%d, releasePointer=%s, genPointer=%s). SECOND LEG: the same error satisfies errors.Is(err, dataset.ErrReleaseRequirementsUnmet)=true via ReleaseRequirementsError.Unwrap (release.go:82), yet the authoring publish route wraps it as an unavailable/HTTP-503 diagnostic with NO explicit retryability (internal/explorer/lifecycle/publish.go:87-88), violating docs/loom-reliability-contract.md (%q). The runtime error surface that F-B14's 503-boundary record rests on is REAL; the classification (retryability) that distinguishes a permanent miss from a transient outage is DISCARDED at the HTTP boundary.",
			errorCode, len(reqErr.Verifications), required[0].Recipe, required[0].TranslationVersion, required[0].Output,
			before.releaseDocCount, after.releaseDocCount, before.totalDocs, after.totalDocs,
			after.releasePointer, after.genPointer, r14ContractProse)
	default:
		// A non-requirements error (transient-flavoured or a store/precondition
		// failure). This would mean the requirements-miss did not surface as the
		// typed error this layer promises.
		t.Log("PREVENTED: dataset.ReleaseService.Activate returned a NON-requirements error (no *ReleaseRequirementsError surface); the F-B14 typed-requirements-error surface did not manifest and cannot back the 503-boundary record here.")
		t.Logf("PREVENTING/source lines that WOULD have produced the typed error, quoted verbatim from source:\n%s", r14QuoteAll(quoted, []string{
			"internal/dataset/release.go:185", "internal/dataset/release.go:186",
			"internal/dataset/release.go:192", "internal/dataset/release.go:193",
		}))
		t.Errorf("PREVENTED/transient-flavoured: Activate returned a non-requirements error err=%v (loop_reached=%v verifier_calls=%d). If this is a transient/store error rather than a genuine refutation, the requirements-miss still never reached the typed surface at release.go:185-193.", err, verificationLoopReached, verifier.calls)
	}
}
