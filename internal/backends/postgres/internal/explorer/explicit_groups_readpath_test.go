package postgres

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

const (
	e2rvDefDigestGolden      = "0711212b6e8e2eee92a289bec97557775e4435545972b8f21c93f03e532f3321"
	e2rvMembershipSetGolden  = "639035f95e895c5e581f0a5fe66b0dfd20c241549833edbe10f20c87a5daa922"
	e2rvMembershipEmptyGoldn = "51a9413396bfc791022568e5db6dbc5001629854b2c17d7b07dfdb35a820ec09"
)

func newE2RVFixture(t *testing.T) *grpmFixture {
	t.Helper()
	return newGRPMFixtureWithPrefix(t, e2rvPrefix)
}

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

	defs, err := f.store.ListExplicitGroupDefinitions(f.ctx, grpmProject, revision.ID)
	if err != nil {
		f.fail("healthy list definitions", err)
	}
	if len(defs) != 2 || string(defs[0].ID) != string(grpmGroupA) || string(defs[1].ID) != string(grpmGroupB) {
		t.Fatalf("healthy ListDefinitions served %+v", defs)
	}
}

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

	_, err = f.witness.Exec(f.ctx, `INSERT INTO `+f.definitionsTable()+
		` (revision_id, group_id, label, ordinal) VALUES ($1, $2, $3, $4)`,
		string(revision.ID), "group-guard-probe", "Probe", int64(53))
	requirePgErrTriggerGRPM(t, err, grpmCheckViolation, "only STAGING revisions accept definitions",
		"guard-alive probe after the drift toggle")
}

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

	if _, err := f.store.GetExplicitGroupRevision(f.ctx, grpmProject, revisionA.ID); err != nil {
		f.fail("healthy sibling get after page refusal", err)
	}
}

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
