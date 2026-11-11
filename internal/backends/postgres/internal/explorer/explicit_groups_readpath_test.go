package postgres

// Read-path verification tests for GetExplicitGroupRevision and
// ListExplicitGroupRevisions (ticket pg-explicit-group-revision-readpath-verify,
// the E2 defense-in-depth follow-up to the #103 CAS arc). The COMPLETE
// header is served only if its stored definition children still re-derive
// the frozen group_count and definition_digest -- the same verified child
// path ListExplicitGroupDefinitions runs. List is whole-page fail-fast:
// one corrupted header refuses the entire call with the typed
// explorer.ErrCorruptExplicitGroupRevision and zero rows.
//
// OWNED namespace: verify_e2rv_* databases and schemas, created through
// newGRPMFixtureWithPrefix with the shared creation registry;
// TestZZZeroLeftoverVerifyE2rvFixtures is the namespace-scoped zero-leftover
// audit. Drift seeding uses ALTER TABLE ... DISABLE TRIGGER on the
// DEFINITION GUARD ONLY inside these test-owned databases -- documented,
// test-only, never production: migration 000011 makes new direct-SQL drift
// unreachable, so this is the only honest way to reproduce PRE-EXISTING or
// hypothetical corruption for a verify-on-read test. The guard is always
// re-enabled (deferred, runs even on t.Fatal), and a post-re-enable probe
// asserts the guard still refuses normal writes -- a disabled-and-forgotten
// trigger is its own incident class. The header guard is never touched:
// the frozen header stays honest, only the children drift.

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/jackc/pgx/v5/pgxpool"
)

const e2rvPrefix = "verify_e2rv"

// Byte-frozen goldens for the two-group gold set (group-a/group-b with the
// single membership (group-a, r-a)), reused from the memberships suite so
// healthy serving is asserted against literals, not just self-consistency.
const (
	e2rvDefDigestGolden      = "0711212b6e8e2eee92a289bec97557775e4435545972b8f21c93f03e532f3321"
	e2rvMembershipSetGolden  = "639035f95e895c5e581f0a5fe66b0dfd20c241549833edbe10f20c87a5daa922"
	e2rvMembershipEmptyGoldn = "51a9413396bfc791022568e5db6dbc5001629854b2c17d7b07dfdb35a820ec09"
)

func newE2RVFixture(t *testing.T) *grpmFixture {
	t.Helper()
	return newGRPMFixtureWithPrefix(t, e2rvPrefix)
}

// e2rvSeedDrift runs one raw statement against the definitions table with
// the definition guard temporarily disabled, always re-enabling it
// (deferred -- runs even when the seeded statement or a later t.Fatal
// aborts the test).
func e2rvSeedDrift(t *testing.T, f *grpmFixture, step, stmt string, args ...any) {
	t.Helper()
	f.execOnGRPM(t, f.witness, step+": disable definition guard",
		`ALTER TABLE `+f.definitionsTable()+` DISABLE TRIGGER explorer_explicit_group_definitions_guard`)
	t.Cleanup(func() {
		f.execOnGRPM(t, f.witness, step+": re-enable definition guard (cleanup)",
			`ALTER TABLE `+f.definitionsTable()+` ENABLE TRIGGER explorer_explicit_group_definitions_guard`)
	})
	f.execOnGRPM(t, f.witness, step, stmt, args...)
	f.execOnGRPM(t, f.witness, step+": re-enable definition guard",
		`ALTER TABLE `+f.definitionsTable()+` ENABLE TRIGGER explorer_explicit_group_definitions_guard`)
}

// e2rvCompleteHonest freezes one staged revision with re-derived facts.
func e2rvCompleteHonest(t *testing.T, f *grpmFixture, revision explorer.ExplicitGroupRevision, token string) {
	t.Helper()
	def, mem, groups, members, err := f.store.DigestExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("e2rv digest", err)
	}
	if _, err := f.store.CompleteExplicitGroupRevision(f.ctx, revision.ID, token,
		def, mem, groups, members, time.Now().UTC()); err != nil {
		f.fail("e2rv completion", err)
	}
}

// e2rvStagedCompleted stages one honest COMPLETE revision (two gold groups;
// one optional membership) under the shared fixture scope.
func e2rvStagedCompleted(t *testing.T, f *grpmFixture, suffix string, seedMembership bool) (explorer.ExplicitGroupRevision, string) {
	t.Helper()
	revision, token, _ := f.stagedGroupFixture(t, suffix, "r-a", "r-b")
	if seedMembership {
		if _, err := f.store.AppendExplicitGroupMemberships(f.ctx, revision.ID, token,
			[]explorer.ExplicitGroupMembership{grpmMembership(grpmGroupA, "r-a")}); err != nil {
			f.fail("e2rv seed append", err)
		}
	}
	e2rvCompleteHonest(t, f, revision, token)
	return revision, token
}

func requireE2RVCorrupt(t *testing.T, err error, step string) {
	t.Helper()
	if !errors.Is(err, explorer.ErrCorruptExplicitGroupRevision) {
		t.Fatalf("%s: err = %v, want errors.Is(ErrCorruptExplicitGroupRevision)", step, err)
	}
}

// TestPostgresExplicitGroupRevisionsReadpathServesHealthyByteIdentical is
// the control: with intact children, Get and List serve the frozen header
// unchanged, field-for-field against the suite's byte-frozen goldens --
// verify-on-read must not alter any healthy result.
func TestPostgresExplicitGroupRevisionsReadpathServesHealthyByteIdentical(t *testing.T) {
	f := newE2RVFixture(t)
	f.migrateWithSelections(t)

	revision, _ := e2rvStagedCompleted(t, f, "healthy", true)

	got, err := f.store.GetExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("healthy get", err)
	}
	if got.ID != revision.ID || got.GroupCount != 2 || got.MemberCount != 1 ||
		got.DefinitionDigest != e2rvDefDigestGolden ||
		got.MembershipDigest != e2rvMembershipSetGolden ||
		got.State != explorer.ExplicitGroupRevisionComplete || got.CompletedAt == nil {
		t.Fatalf("healthy Get served %+v, want the frozen gold header", got)
	}
	// Independent cross-check of the membership literal (never trust a
	// constant against its own mirror).
	if rederived := grpmFrameDigest(t, [2]string{string(grpmGroupA), "r-a"}); rederived != e2rvMembershipSetGolden {
		t.Fatalf("membership golden %s disagrees with independent re-derivation %s", e2rvMembershipSetGolden, rederived)
	}

	listed, err := f.store.ListExplicitGroupRevisions(f.ctx, grpmProject, grpmGeneration, revision.ScopeDigest, grpmResourceType, 10)
	if err != nil {
		f.fail("healthy list", err)
	}
	if len(listed) != 1 || !reflect.DeepEqual(listed[0], *got) {
		t.Fatalf("healthy List served %+v, want exactly the Get row", listed)
	}

	// The already-refusing child path is untouched: ListDefinitions
	// still serves the two gold rows.
	defs, err := f.store.ListExplicitGroupDefinitions(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("healthy list definitions", err)
	}
	if len(defs) != 2 || string(defs[0].ID) != string(grpmGroupA) || string(defs[1].ID) != string(grpmGroupB) {
		t.Fatalf("healthy ListDefinitions served %+v", defs)
	}
}

// TestPostgresExplicitGroupRevisionsGetRefusesCountDrift deletes a
// definition child under the temporarily disabled definition guard (the
// pre-000011 E1 shape) and asserts Get refuses with the typed corruption
// category instead of serving the frozen 2-group header over one child.
// The ListDefinitions refusal must still refuse (no regression) and the
// guard must still refuse normal writes after the toggle is undone.
func TestPostgresExplicitGroupRevisionsGetRefusesCountDrift(t *testing.T) {
	f := newE2RVFixture(t)
	f.migrateWithSelections(t)

	revision, _ := e2rvStagedCompleted(t, f, "countdrift", true)
	if _, err := f.store.GetExplicitGroupRevision(f.ctx, grpmProject, revision.ID); err != nil {
		f.fail("pre-drift get (control)", err)
	}

	e2rvSeedDrift(t, f, "delete one gold definition",
		`DELETE FROM `+f.definitionsTable()+` WHERE revision_id = $1 AND group_id = $2`,
		string(revision.ID), string(grpmGroupB))

	_, err := f.store.GetExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	requireE2RVCorrupt(t, err, "Get over count-drifted children")

	if _, err := f.store.ListExplicitGroupDefinitions(f.ctx, grpmProject, revision.ID); !errors.Is(err, explorer.ErrCorruptExplicitGroupRevision) {
		t.Fatalf("ListDefinitions regression: err = %v, want the typed corruption refusal", err)
	}

	// Guard-alive probe after the toggle: with the header COMPLETE, a
	// normal definition INSERT must again be refused BY NAME -- the
	// drift seeding left the guard enabled, not disabled-and-forgotten.
	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+
		` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
		string(revision.ID), "group-guard-probe", "Probe", int64(53))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "only STAGING revisions accept definitions",
		"guard-alive probe after the drift toggle")
}

// TestPostgresExplicitGroupRevisionsGetRefusesDigestOnlyDrift isolates the
// digest check: a label UPDATE keeps the child COUNT identical (so a
// count-only verification would still serve) while the re-derived
// definition digest diverges from the frozen one. This is the shape that
// kills a removed digest comparison -- deleting the check serves happily.
func TestPostgresExplicitGroupRevisionsGetRefusesDigestOnlyDrift(t *testing.T) {
	f := newE2RVFixture(t)
	f.migrateWithSelections(t)

	revision, _ := e2rvStagedCompleted(t, f, "digestdrift", false)
	healthy, err := f.store.GetExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("pre-drift get (control)", err)
	}
	if healthy.MembershipDigest != e2rvMembershipEmptyGoldn || healthy.MemberCount != 0 {
		t.Fatalf("pre-drift control served membership %q/%d, want the frozen empty set",
			healthy.MembershipDigest, healthy.MemberCount)
	}

	e2rvSeedDrift(t, f, "tamper one gold label",
		`UPDATE `+f.definitionsTable()+` SET label = 'tampered label'`+
			` WHERE revision_id = $1 AND group_id = $2`,
		string(revision.ID), string(grpmGroupA))

	_, err = f.store.GetExplicitGroupRevision(f.ctx, grpmProject, revision.ID)
	requireE2RVCorrupt(t, err, "Get over digest-drifted children (count intact)")
}

// TestPostgresExplicitGroupRevisionsListRefusesWholePageOnDrift proves the
// whole-page fail-fast semantic with two COMPLETE headers in one scope:
// the healthy page serves both; corrupting one child refuses the ENTIRE
// call with the typed error and zero rows served (the List API has no
// per-row error channel, so skipping would silently re-enact the served
// divergence), while the healthy sibling still serves on its own -- the
// refusal is the page check meeting the bad row, not fixture damage.
func TestPostgresExplicitGroupRevisionsListRefusesWholePageOnDrift(t *testing.T) {
	f := newE2RVFixture(t)
	f.migrateWithSelections(t)

	source := f.stageSelection(t, "pagedrift", "r-a", "r-b")
	revisionA := f.beginGroupRevision(t, source, "e2rv-key-a", "e2rv-tok-a")
	f.putDefinitions(t, revisionA.ID, "e2rv-tok-a")
	e2rvCompleteHonest(t, f, revisionA, "e2rv-tok-a")
	revisionB := f.beginGroupRevision(t, source, "e2rv-key-b", "e2rv-tok-b")
	f.putDefinitions(t, revisionB.ID, "e2rv-tok-b")
	e2rvCompleteHonest(t, f, revisionB, "e2rv-tok-b")

	listed, err := f.store.ListExplicitGroupRevisions(f.ctx, grpmProject, grpmGeneration, revisionA.ScopeDigest, grpmResourceType, 10)
	if err != nil {
		f.fail("healthy two-header list", err)
	}
	// Set equality, not order: created_at can tie within the clock
	// resolution and the documented tiebreak is ID byte order.
	if len(listed) != 2 ||
		!((listed[0].ID == revisionA.ID && listed[1].ID == revisionB.ID) ||
			(listed[0].ID == revisionB.ID && listed[1].ID == revisionA.ID)) {
		t.Fatalf("healthy page served %+v, want exactly both headers", listed)
	}

	e2rvSeedDrift(t, f, "delete a child under header B",
		`DELETE FROM `+f.definitionsTable()+` WHERE revision_id = $1 AND group_id = $2`,
		string(revisionB.ID), string(grpmGroupB))

	listed, err = f.store.ListExplicitGroupRevisions(f.ctx, grpmProject, grpmGeneration, revisionA.ScopeDigest, grpmResourceType, 10)
	requireE2RVCorrupt(t, err, "List over a page containing one drifted header")
	if len(listed) != 0 {
		t.Fatalf("refused List still served %d rows -- partial serve re-enacted", len(listed))
	}

	// Non-vacuity for the fixture: the untouched sibling still serves.
	if _, err := f.store.GetExplicitGroupRevision(f.ctx, grpmProject, revisionA.ID); err != nil {
		f.fail("healthy sibling get after page refusal", err)
	}
}

// TestPostgresExplicitGroupRevisionsVerifyCostEvidence measures the
// verify-on-read cost the plan committed to state: per served header the
// check is one indexed PK-range child scan (O(G) rows + O(G) sha256),
// and a List page is bounded by explicitGroupListLimit (100). Twelve
// completed headers of forty groups each are served through List plus a
// full Get sweep; the generous bound guards only against accidental
// quadratic behavior, and the measured milliseconds are reported.
func TestPostgresExplicitGroupRevisionsVerifyCostEvidence(t *testing.T) {
	f := newE2RVFixture(t)
	f.migrateWithSelections(t)

	const headers, groupsPer = 12, 40
	source := f.stageSelection(t, "cost", "r-a", "r-b", "r-c", "r-d", "r-e")
	ids := make([]explorer.ExplicitGroupRevisionID, 0, headers)
	var scopeDigest string
	for h := 0; h < headers; h++ {
		token := fmt.Sprintf("e2rv-cost-tok-%d", h)
		revision := f.beginGroupRevision(t, source, fmt.Sprintf("e2rv-cost-key-%d", h), token)
		defs := make([]explorer.ExplicitGroupDefinition, 0, groupsPer)
		for g := 0; g < groupsPer; g++ {
			defs = append(defs, explorer.ExplicitGroupDefinition{
				ID:      explorer.ExplicitGroupID(fmt.Sprintf("e2rv-cost-group-%03d", g)),
				Label:   fmt.Sprintf("Cost group %03d", g),
				Ordinal: int64(g),
			})
		}
		if err := f.store.PutExplicitGroupDefinitions(f.ctx, revision.ID, token, defs); err != nil {
			f.fail("cost put definitions", err)
		}
		e2rvCompleteHonest(t, f, revision, token)
		ids = append(ids, revision.ID)
		scopeDigest = revision.ScopeDigest
	}

	start := time.Now()
	listed, err := f.store.ListExplicitGroupRevisions(f.ctx, grpmProject, grpmGeneration, scopeDigest, grpmResourceType, 100)
	if err != nil {
		f.fail("cost list", err)
	}
	if len(listed) != headers {
		t.Fatalf("cost list served %d rows, want %d", len(listed), headers)
	}
	listMs := time.Since(start).Milliseconds()

	start = time.Now()
	for _, id := range ids {
		if _, err := f.store.GetExplicitGroupRevision(f.ctx, grpmProject, id); err != nil {
			f.fail("cost get", err)
		}
	}
	getMs := time.Since(start).Milliseconds()

	t.Logf("verify-on-read cost evidence: %d headers x %d groups: List %d ms, %d Gets %d ms",
		headers, groupsPer, listMs, headers, getMs)
	if bound := int64(8000); listMs > bound || getMs > bound {
		t.Fatalf("verify-on-read cost runaway: list %d ms, get sweep %d ms, bound %d ms", listMs, getMs, bound)
	}
}

// TestZZZeroLeftoverVerifyE2rvFixtures is the namespace-scoped zero-leftover
// audit for this ticket's verify_e2rv_* namespace (mirrors the merged
// verify_grpm_* discipline: registry-first reasoning, prefix-scoped scan,
// never a global baseline).
func TestZZZeroLeftoverVerifyE2rvFixtures(t *testing.T) {
	raw := pgstate06TestPostgresURL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, raw)
	if err != nil {
		t.Fatalf("connect for leftover audit: %v", err)
	}
	defer admin.Close()
	var dbLeftovers int
	if err := admin.QueryRow(ctx, `SELECT count(*) FROM pg_database WHERE datname LIKE $1 ESCAPE '\'`,
		grpmLikePattern(e2rvPrefix)).Scan(&dbLeftovers); err != nil {
		t.Fatalf("audit query: %v", err)
	}
	if dbLeftovers != 0 {
		t.Fatalf("%d verify_e2rv_* databases survive the suite", dbLeftovers)
	}
	if owned := grpmSnapshotRegistry(); len(owned) != 0 {
		t.Fatalf("creation registry still holds %d entries: %v", len(owned), owned)
	}
}
