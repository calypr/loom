package dataset_test

// TestReleaseRequirementsUnmetActivateAgainstArango exercises the service
// contract for a staged generation whose required publication is not yet
// queryable: activation returns the typed requirements error and leaves release
// visibility unchanged.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	dataset "github.com/calypr/loom/internal/dataset"
	datasetarango "github.com/calypr/loom/internal/dataset/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

const r14DatabasePrefix = "loom_verify_r14_"

// r14FakeVerifier implements the service's publication-verification seam and
// reports the requested selector as pending and not queryable.
type r14FakeVerifier struct {
	calls  int
	sought dataset.DataframeSelector
}

func (v *r14FakeVerifier) VerifyPublication(_ context.Context, _, generation string, selector dataset.DataframeSelector) (dataset.PublicationVerification, error) {
	v.calls++
	v.sought = selector
	// Echo the selector identity so the service sees a valid but incomplete
	// verification result.
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
	database := arangotest.Owned(t, r14DatabasePrefix)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	client := database.Client
	if err := client.Bootstrap(ctx, datasetarango.BootstrapSpec()); err != nil {
		t.Fatalf("bootstrap dataset lifecycle collections: %v", err)
	}
	lifecycle, err := datasetarango.New(client)
	if err != nil {
		t.Fatalf("new dataset lifecycle store: %v", err)
	}
	return ctx, client, lifecycle
}

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

// TestReleaseRequirementsUnmetActivateAgainstArango drives ReleaseService
// against a real datasetarango store with one required selector pending and
// verifies the typed error and unchanged release visibility.
func TestReleaseRequirementsUnmetActivateAgainstArango(t *testing.T) {
	ctx, client, lifecycle := r14Open(t, 180*time.Second)

	const project = "r14-project"
	const generation = "r14-gen-unmet"
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
	active, err := releaseService.Activate(ctx, dataset.ActivationRequest{
		Project:          project,
		Generation:       generation,
		GitCommit:        generation,
		ExpectedRevision: 0,
	})
	after := r14TakeSnapshot(t, ctx, client, lifecycle, project)

	if err == nil {
		t.Fatalf("Activate succeeded for a required selector that is PENDING and not queryable: active=%+v", active)
	}
	var requirementsErr *dataset.ReleaseRequirementsError
	if !errors.As(err, &requirementsErr) {
		t.Fatalf("Activate error = %T (%v), want *ReleaseRequirementsError", err, err)
	}
	if !errors.Is(err, dataset.ErrReleaseRequirementsUnmet) {
		t.Fatalf("Activate error %v does not match ErrReleaseRequirementsUnmet", err)
	}
	if len(requirementsErr.Verifications) != 1 {
		t.Fatalf("requirements verifications = %d, want one: %+v", len(requirementsErr.Verifications), requirementsErr.Verifications)
	}
	verification := requirementsErr.Verifications[0]
	if verification.Selector != required[0] || verification.Generation != generation || verification.State != "PENDING" || verification.Queryable || verification.ErrorCode != "RELEASE_REQUIREMENTS_UNMET" {
		t.Fatalf("requirements verification = %+v; want the requested PENDING, non-queryable selector", verification)
	}
	if verifier.calls != 1 || verifier.sought != required[0] {
		t.Fatalf("publication verifier calls=%d selector=%+v; want one call for %+v", verifier.calls, verifier.sought, required[0])
	}
	if before.canonical != after.canonical || before.totalDocs != after.totalDocs || before.releaseDocCount != after.releaseDocCount || before.releasePointer != after.releasePointer || before.genPointer != after.genPointer || before.activeErr != after.activeErr || before.activeRevision != after.activeRevision || before.activeID != after.activeID {
		t.Fatalf("failed activation changed release visibility or lifecycle state:\nBEFORE:\n%s\nAFTER:\n%s", before.canonical, after.canonical)
	}
}
