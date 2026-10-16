package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
)

type cohortRecoveryStore struct {
	explorer.ExplicitGroupRepository
	selection explorer.SelectionRevision
	recent    []explorer.ExplicitGroupRevision
	pinned    explorer.ExplicitGroupRevision
	lookups   int
}

func (s *cohortRecoveryStore) ListExplicitGroupRevisions(_ context.Context, _, _, _, _ string, limit int) ([]explorer.ExplicitGroupRevision, error) {
	return s.recent, nil
}

func (s *cohortRecoveryStore) GetExplicitGroupRevision(_ context.Context, project string, id explorer.ExplicitGroupRevisionID) (*explorer.ExplicitGroupRevision, error) {
	s.lookups++
	value := s.pinned
	return &value, nil
}

func (s *cohortRecoveryStore) GetSelection(_ context.Context, _, _ string) (*explorer.SelectionRevision, error) {
	value := s.selection
	return &value, nil
}

func TestCohortSelectionRecoveryLooksUpPinnedRevisionOutsideRecentList(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "cohort-recovery", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	at := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	selection := explorer.SelectionRevision{
		ID: "selection-source", Project: "project-a", Generation: "generation-a", ResourceType: "Patient",
		ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, RuleDigest: "rule", MembershipDigest: "members",
		Rule: explorer.SelectionRule{Kind: explorer.SelectionRuleExplicit}, Source: explorer.SelectionSource{Kind: explorer.SelectionSourceExplicit},
		Complete: true, CreatedAt: at, CompletedAt: &at,
	}
	pinned := explorer.ExplicitGroupRevision{
		ID: explorer.ExplicitGroupRevisionIDFor(selection.Project, "older-cohort"), IdempotencyKey: "older-cohort",
		Project: selection.Project, Generation: selection.Generation, ResourceType: selection.ResourceType, ScopeDigest: selection.ScopeDigest,
		SourceSelectionRevisionID: selection.ID, SourceMembershipDigest: selection.MembershipDigest,
		State: explorer.ExplicitGroupRevisionComplete, DefinitionDigest: "definitions", MembershipDigest: "cohort-members",
		GroupCount: 1, CreatedAt: at, CompletedAt: &at,
	}
	for _, scenario := range []struct {
		name   string
		mutate func(*cohortRecoveryStore)
		fail   bool
	}{
		{name: "older pinned cohort"},
		{name: "wrong project", mutate: func(s *cohortRecoveryStore) { s.pinned.Project = "foreign" }, fail: true},
		{name: "wrong generation", mutate: func(s *cohortRecoveryStore) { s.pinned.Generation = "foreign" }, fail: true},
		{name: "wrong authorization", mutate: func(s *cohortRecoveryStore) { s.pinned.ScopeDigest = "foreign" }, fail: true},
		{name: "wrong resource type", mutate: func(s *cohortRecoveryStore) { s.pinned.ResourceType = "Specimen" }, fail: true},
		{name: "stale membership", mutate: func(s *cohortRecoveryStore) { s.selection.MembershipDigest = "changed" }, fail: true},
		{name: "incomplete cohort", mutate: func(s *cohortRecoveryStore) { s.pinned.State = explorer.ExplicitGroupRevisionStaging }, fail: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := &cohortRecoveryStore{selection: selection, pinned: pinned}
			for i := 0; i < 100; i++ {
				recent := pinned
				recent.ID = explorer.ExplicitGroupRevisionIDFor(selection.Project, at.Add(time.Duration(i)*time.Hour).String())
				recent.IdempotencyKey = at.Add(time.Duration(i) * time.Hour).String()
				store.recent = append(store.recent, recent)
			}
			if scenario.mutate != nil {
				scenario.mutate(store)
			}
			resolver, err := NewRepositoryExplicitGroupRevisionResolver(store)
			if err != nil {
				t.Fatal(err)
			}
			choices, err := resolver.ListExplicitGroupRevisions(context.Background(), ExplicitGroupRevisionListRequest{
				Project: selection.Project, Snapshot: snapshot, RootResourceType: selection.ResourceType, PinnedRevisionID: string(pinned.ID),
			})
			if scenario.fail {
				if err == nil || len(choices) != 0 {
					t.Fatalf("invalid pinned cohort exposed choices: %#v, error %v", choices, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if store.lookups != 1 || len(choices) != 101 || choices[100].RevisionID != string(pinned.ID) || choices[100].SourceSelectionRevisionID != selection.ID {
				t.Fatalf("older cohort selection not recovered exactly: lookups=%d choices=%#v", store.lookups, choices)
			}
		})
	}
}
