package explorer

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestExplicitGroupDefinitionsCanonicalizeWithoutChangingIdentity(t *testing.T) {
	input := []ExplicitGroupDefinition{
		{ID: " group-b ", Label: " Beta ", Ordinal: 1},
		{ID: "group-a", Label: "Alpha", Ordinal: 0},
	}
	canonical, err := CanonicalExplicitGroupDefinitions(input)
	if err != nil {
		t.Fatal(err)
	}
	want := []ExplicitGroupDefinition{
		{ID: "group-a", Label: "Alpha", Ordinal: 0},
		{ID: "group-b", Label: "Beta", Ordinal: 1},
	}
	if !reflect.DeepEqual(canonical, want) {
		t.Fatalf("canonical groups = %#v, want %#v", canonical, want)
	}
	if input[0].ID != " group-b " || input[0].Label != " Beta " {
		t.Fatalf("canonicalization mutated caller input: %#v", input)
	}

	definitionDigest, err := ExplicitGroupDefinitionDigest(input)
	if err != nil {
		t.Fatal(err)
	}
	if definitionDigest != "c0624434be97bc243d9131b7611cdd8c0b38ec39edae0f7a87cebd35918ff3c0" {
		t.Fatalf("definition digest = %s", definitionDigest)
	}
	reorderedDigest, err := ExplicitGroupDefinitionDigest(want)
	if err != nil || reorderedDigest != definitionDigest {
		t.Fatalf("reordered definition digest = %s err=%v, want %s", reorderedDigest, err, definitionDigest)
	}
	changedPresentation := append([]ExplicitGroupDefinition(nil), want...)
	changedPresentation[0].Label = "Renamed"
	changedDigest, err := ExplicitGroupDefinitionDigest(changedPresentation)
	if err != nil || changedDigest == definitionDigest {
		t.Fatalf("definition presentation change digest = %s err=%v, want a changed definition digest", changedDigest, err)
	}
	if want[0].ID != "group-a" {
		t.Fatalf("presentation change altered stable group identity: %q", want[0].ID)
	}
}

func TestExplicitGroupMembershipDigestPreservesOverlapsAndDeduplicatesPairs(t *testing.T) {
	revision := testExplicitGroupRevision()
	groups := []ExplicitGroupDefinition{
		{ID: "group-a", Label: "Alpha", Ordinal: 0},
		{ID: "group-b", Label: "Beta", Ordinal: 1},
	}
	shared := ResourceRef{Project: revision.Project, Generation: revision.Generation, ResourceType: revision.ResourceType, ID: "patient-1"}
	onlyA := ResourceRef{Project: revision.Project, Generation: revision.Generation, ResourceType: revision.ResourceType, ID: "patient-2"}
	memberships := []ExplicitGroupMembership{
		{GroupID: "group-b", Ref: shared},
		{GroupID: "group-a", Ref: onlyA},
		{GroupID: "group-a", Ref: shared},
		{GroupID: "group-a", Ref: shared},
	}
	canonical, err := CanonicalExplicitGroupMemberships(revision, groups, memberships)
	if err != nil {
		t.Fatal(err)
	}
	want := []ExplicitGroupMembership{
		{GroupID: "group-a", Ref: shared},
		{GroupID: "group-a", Ref: onlyA},
		{GroupID: "group-b", Ref: shared},
	}
	if !reflect.DeepEqual(canonical, want) {
		t.Fatalf("canonical memberships = %#v, want %#v", canonical, want)
	}
	digest, err := ExplicitGroupMembershipDigest(revision, groups, memberships)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "a0445982c9a1edf1c5ac5f506dbc2cbf5e722b2568584064fcf3204827809cda" {
		t.Fatalf("membership digest = %s", digest)
	}
	deduplicatedDigest, err := ExplicitGroupMembershipDigest(revision, groups, canonical)
	if err != nil || deduplicatedDigest != digest {
		t.Fatalf("deduplicated digest = %s err=%v, want %s", deduplicatedDigest, err, digest)
	}
	singleGroupDigest, err := ExplicitGroupMembershipDigest(revision, groups, []ExplicitGroupMembership{{GroupID: "group-a", Ref: shared}, {GroupID: "group-a", Ref: onlyA}})
	if err != nil || singleGroupDigest == digest {
		t.Fatalf("single-group digest = %s err=%v, want overlap to affect membership", singleGroupDigest, err)
	}
	definitionDigest, err := ExplicitGroupDefinitionDigest(groups)
	if err != nil {
		t.Fatal(err)
	}
	changedGroups := append([]ExplicitGroupDefinition(nil), groups...)
	changedGroups[0].Label = "Renamed"
	changedDefinitionDigest, err := ExplicitGroupDefinitionDigest(changedGroups)
	if err != nil || changedDefinitionDigest == definitionDigest {
		t.Fatalf("definition digest did not record label change: %s err=%v", changedDefinitionDigest, err)
	}
	unchangedMembershipDigest, err := ExplicitGroupMembershipDigest(revision, changedGroups, memberships)
	if err != nil || unchangedMembershipDigest != digest {
		t.Fatalf("presentation changed membership digest: %s err=%v, want %s", unchangedMembershipDigest, err, digest)
	}
}

func TestExplicitGroupRevisionCompletesWithAnEmptyDeclaredGroup(t *testing.T) {
	revision := testExplicitGroupRevision()
	group := ExplicitGroupDefinition{ID: "empty", Label: "No members", Ordinal: 0}
	definitionDigest, err := ExplicitGroupDefinitionDigest([]ExplicitGroupDefinition{group})
	if err != nil {
		t.Fatal(err)
	}
	membershipDigest, err := ExplicitGroupMembershipDigest(revision, []ExplicitGroupDefinition{group}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if definitionDigest != "8634f188dfb0a48bf15df39d92dc0aa25392e0c52e63799c4c599cdb12af9f43" || membershipDigest != "51a9413396bfc791022568e5db6dbc5001629854b2c17d7b07dfdb35a820ec09" {
		t.Fatalf("empty group digests = %s/%s", definitionDigest, membershipDigest)
	}
	completedAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	revision.State = ExplicitGroupRevisionComplete
	revision.DefinitionDigest = definitionDigest
	revision.MembershipDigest = membershipDigest
	revision.GroupCount = 1
	revision.MemberCount = 0
	revision.CompletedAt = &completedAt
	if err := revision.Validate(); err != nil {
		t.Fatalf("complete empty group revision is invalid: %v", err)
	}
}

func TestExplicitGroupValidationRejectsAmbiguousOrUnscopedData(t *testing.T) {
	tests := []struct {
		name   string
		groups []ExplicitGroupDefinition
	}{
		{name: "no groups"},
		{name: "missing label", groups: []ExplicitGroupDefinition{{ID: "group-a", Ordinal: 0}}},
		{name: "negative ordinal", groups: []ExplicitGroupDefinition{{ID: "group-a", Label: "A", Ordinal: -1}}},
		{name: "duplicate ID", groups: []ExplicitGroupDefinition{{ID: "group-a", Label: "A", Ordinal: 0}, {ID: "group-a", Label: "B", Ordinal: 1}}},
		{name: "duplicate ordinal", groups: []ExplicitGroupDefinition{{ID: "group-a", Label: "A", Ordinal: 0}, {ID: "group-b", Label: "B", Ordinal: 0}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := CanonicalExplicitGroupDefinitions(tt.groups); err == nil {
				t.Fatal("invalid group definitions unexpectedly succeeded")
			}
		})
	}

	revision := testExplicitGroupRevision()
	groups := []ExplicitGroupDefinition{{ID: "group-a", Label: "A", Ordinal: 0}}
	if _, err := CanonicalExplicitGroupMemberships(revision, groups, []ExplicitGroupMembership{{GroupID: "missing", Ref: ResourceRef{Project: revision.Project, Generation: revision.Generation, ResourceType: revision.ResourceType, ID: "patient-1"}}}); err == nil {
		t.Fatal("membership in undeclared group unexpectedly succeeded")
	}
	if _, err := CanonicalExplicitGroupMemberships(revision, groups, []ExplicitGroupMembership{{GroupID: "group-a", Ref: ResourceRef{Project: "other-project", Generation: revision.Generation, ResourceType: revision.ResourceType, ID: "patient-1"}}}); !errors.Is(err, ErrResourceRefScopeMismatch) {
		t.Fatalf("cross-project membership error = %v", err)
	}
}

func TestExplicitGroupRevisionRejectsSourceSelectionMismatch(t *testing.T) {
	revision := testCompleteExplicitGroupRevision()
	selection := testCompleteSelection()
	if err := revision.ValidateSource(selection); err != nil {
		t.Fatalf("matching source selection rejected: %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*SelectionRevision)
	}{
		{name: "revision ID", mutate: func(s *SelectionRevision) { s.ID = "another-selection" }},
		{name: "project", mutate: func(s *SelectionRevision) { s.Project = "another-project" }},
		{name: "generation", mutate: func(s *SelectionRevision) { s.Generation = "another-generation" }},
		{name: "resource type", mutate: func(s *SelectionRevision) { s.ResourceType = "Encounter" }},
		{name: "scope", mutate: func(s *SelectionRevision) { s.ScopeDigest = "another-scope" }},
		{name: "membership", mutate: func(s *SelectionRevision) { s.MembershipDigest = "another-membership" }},
		{name: "incomplete", mutate: func(s *SelectionRevision) { s.Complete = false }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrong := testCompleteSelection()
			tt.mutate(&wrong)
			if err := revision.ValidateSource(wrong); !errors.Is(err, ErrExplicitGroupRevisionStaleSource) {
				t.Fatalf("source mismatch error = %v", err)
			}
		})
	}
}

func TestExplicitGroupRevisionStateIsClosedAndConsistent(t *testing.T) {
	revision := testExplicitGroupRevision()
	if err := revision.Validate(); err != nil {
		t.Fatalf("valid staging revision rejected: %v", err)
	}
	revision.State = "UNKNOWN"
	if err := revision.Validate(); err == nil {
		t.Fatal("unknown revision state unexpectedly succeeded")
	}
	revision = testExplicitGroupRevision()
	revision.State = ExplicitGroupRevisionComplete
	if err := revision.Validate(); !errors.Is(err, ErrExplicitGroupRevisionIncomplete) {
		t.Fatalf("incomplete completed revision error = %v", err)
	}
}

func testExplicitGroupRevision() ExplicitGroupRevision {
	return ExplicitGroupRevision{
		ID: "groups-1", Project: "project-a", Generation: "generation-a", ScopeDigest: "scope-a",
		ResourceType: "Patient", SourceSelectionRevisionID: "selection-1", SourceMembershipDigest: "selection-membership",
		State: ExplicitGroupRevisionStaging, IdempotencyKey: "create-groups-1", CreatedAt: time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
}

func testCompleteExplicitGroupRevision() ExplicitGroupRevision {
	revision := testExplicitGroupRevision()
	completedAt := revision.CreatedAt.Add(time.Minute)
	revision.State = ExplicitGroupRevisionComplete
	revision.DefinitionDigest = "definition-digest"
	revision.MembershipDigest = "membership-digest"
	revision.GroupCount = 1
	revision.MemberCount = 0
	revision.CompletedAt = &completedAt
	return revision
}

func testCompleteSelection() SelectionRevision {
	completedAt := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	return SelectionRevision{
		ID: "selection-1", Project: "project-a", Generation: "generation-a", ResourceType: "Patient",
		Rule: SelectionRule{Kind: SelectionRuleExplicit}, Source: SelectionSource{Kind: SelectionSourceExplicit},
		ScopeDigest: "scope-a", RuleDigest: "selection-rule", MembershipDigest: "selection-membership",
		MemberCount: 1, Complete: true, IdempotencyKey: "selection-create", CreatedAt: completedAt, CompletedAt: &completedAt,
	}
}
