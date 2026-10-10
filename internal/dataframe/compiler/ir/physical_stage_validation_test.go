package ir

import "testing"

func TestRelatedEligibilityAcceptsRetainedRootKeyDistinctFromExpandedRowIdentity(t *testing.T) {
	stage := PhysicalConstructionStage{
		InputRowVariable: "input",
		InputColumns: []PhysicalStageColumn{
			{
				ID: "root_key", Name: "_key", Kind: "string", Cardinality: "required_one",
				Internal: true, RootContributorResourceType: "Patient",
			},
			{
				ID: "expanded_identity", Name: "__loom_expansion_identity", Kind: "string", Cardinality: "required_one",
				Internal: true, Identity: true,
			},
		},
	}
	subplan := PhysicalSubplan{
		Captures: []string{"input"},
		Operations: []PhysicalOperation{
			{
				Kind:           PhysicalCollectionScanOp,
				Source:         PhysicalSource{ResourceType: "Patient"},
				CollectionScan: &PhysicalCollectionScan{Variable: "root", CollectionBindKey: "root_collection"},
			},
			{
				Kind: PhysicalFilterOp,
				Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
					Operator: "EQUALS",
					Left:     PhysicalValue{Variable: "root", Path: []string{"_key"}},
					Right:    &PhysicalValue{Variable: "input", Path: []string{"_key"}},
				}},
			},
		},
	}
	bindVars := map[string]any{"root_collection": "Patient"}
	if err := validateRelatedEligibilityAnchor(stage, &subplan, bindVars); err != nil {
		t.Fatalf("retained root-key anchor with a distinct expanded row identity did not validate: %v", err)
	}

	invalidProofs := []struct {
		name   string
		mutate func(*PhysicalStageColumn)
	}{
		{"wrong name", func(column *PhysicalStageColumn) { column.Name = "_id" }},
		{"wrong kind", func(column *PhysicalStageColumn) { column.Kind = "integer" }},
		{"optional key", func(column *PhysicalStageColumn) { column.Cardinality = "optional_one" }},
		{"wrong resource type", func(column *PhysicalStageColumn) { column.RootContributorResourceType = "Observation" }},
		{"public key", func(column *PhysicalStageColumn) { column.Internal = false }},
		{"related-record identity metadata", func(column *PhysicalStageColumn) {
			column.RelatedRecordAnchor = &PhysicalStageRelatedRecordAnchor{NodeID: "patient", ResourceType: "Patient"}
		}},
	}
	for _, test := range invalidProofs {
		t.Run(test.name, func(t *testing.T) {
			invalidStage := stage
			invalidStage.InputColumns = append([]PhysicalStageColumn(nil), stage.InputColumns...)
			test.mutate(&invalidStage.InputColumns[0])
			if err := validateRelatedEligibilityAnchor(invalidStage, &subplan, bindVars); err == nil {
				t.Fatal("invalid root-key anchor proof unexpectedly validated")
			}
		})
	}
}

func TestCohortGroupAcceptsRetainedRootKeyDistinctFromExpandedRowIdentity(t *testing.T) {
	stage, cohort, bindVars := cohortGroupRetainedRootKeyFixture()
	if err := validatePhysicalStageCohortGroup(stage, cohort, bindVars); err != nil {
		t.Fatalf("retained root contributor key with a distinct expanded row identity did not validate: %v", err)
	}
}

func TestCohortMemberRecodeValidationKeepsExactPayloadScope(t *testing.T) {
	selector := PhysicalExpression{
		Kind: PhysicalExtractExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
		Extract: &PhysicalExtract{Source: PhysicalValue{Variable: "cohort_member", Path: []string{"payload"}}},
	}
	literal := func(key string) PhysicalExpression {
		return PhysicalExpression{Kind: PhysicalLiteralExpression, Cardinality: PhysicalScalarCardinality, Literal: &PhysicalLiteral{BindKey: key}}
	}
	equality := func(left, right PhysicalExpression) PhysicalExpression {
		return PhysicalExpression{Kind: PhysicalCallExpression, Cardinality: PhysicalScalarCardinality, Call: &PhysicalCall{Name: "eq", Args: []PhysicalExpression{left, right}}}
	}
	binds := map[string]any{"null": nil, "from": "recorded", "to": "shared", "false": false, "unknown": "CATEGORY_RECODE_UNKNOWN_VALUE"}
	keepOriginal := PhysicalExpression{
		Kind: PhysicalCallExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
		Call: &PhysicalCall{Name: "case", Args: []PhysicalExpression{
			equality(selector, literal("null")), literal("null"),
			equality(selector, literal("from")), literal("to"), selector,
		}},
	}
	if !validCohortMemberValueExpression(keepOriginal, binds) {
		t.Fatal("exact-category KEEP_ORIGINAL expression over the member payload did not validate")
	}
	errorOnUnknown := keepOriginal
	errorOnUnknown.Call = &PhysicalCall{Name: "case", Args: append([]PhysicalExpression(nil), keepOriginal.Call.Args...)}
	errorOnUnknown.Call.Args[len(errorOnUnknown.Call.Args)-1] = PhysicalExpression{
		Kind: PhysicalCallExpression, Cardinality: PhysicalScalarCardinality,
		Call: &PhysicalCall{Name: "assert", Args: []PhysicalExpression{literal("false"), literal("unknown")}},
	}
	if !validCohortMemberValueExpression(errorOnUnknown, binds) {
		t.Fatal("exact-category ERROR expression over the member payload did not validate")
	}
	wrongSelector := selector
	wrongSelector.Extract = &PhysicalExtract{Source: PhysicalValue{Variable: "cohort_member", Path: []string{"resourceType"}}}
	forged := keepOriginal
	forged.Call = &PhysicalCall{Name: "case", Args: append([]PhysicalExpression(nil), keepOriginal.Call.Args...)}
	forged.Call.Args[2] = equality(wrongSelector, literal("from"))
	forged.Call.Args[4] = wrongSelector
	if validCohortMemberValueExpression(forged, binds) {
		t.Fatal("recode expression over a non-payload member field unexpectedly validated")
	}
	arbitrary := PhysicalExpression{
		Kind: PhysicalCallExpression, Cardinality: PhysicalScalarCardinality,
		Call: &PhysicalCall{Name: "concat", Args: []PhysicalExpression{selector}},
	}
	if validCohortMemberValueExpression(arbitrary, binds) {
		t.Fatal("arbitrary call over a cohort member unexpectedly validated as a row value")
	}
}

func TestCohortGroupRejectsForgedRetainedRootKeyProof(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalStageColumn)
	}{
		{"wrong name", func(column *PhysicalStageColumn) { column.Name = "_id" }},
		{"wrong kind", func(column *PhysicalStageColumn) { column.Kind = "integer" }},
		{"optional key", func(column *PhysicalStageColumn) { column.Cardinality = "optional_one" }},
		{"wrong resource type", func(column *PhysicalStageColumn) { column.RootContributorResourceType = "Observation" }},
		{"missing root marker", func(column *PhysicalStageColumn) { column.RootContributorResourceType = "" }},
		{"public key", func(column *PhysicalStageColumn) { column.Internal = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stage, cohort, bindVars := cohortGroupRetainedRootKeyFixture()
			test.mutate(&stage.InputColumns[0])
			if err := validatePhysicalStageCohortGroup(stage, cohort, bindVars); err == nil {
				t.Fatal("forged root contributor proof unexpectedly validated")
			}
		})
	}
}

func TestPhysicalStageCohortGroupRejectsCrossProjectResourceBinding(t *testing.T) {
	stage, cohort, bindVars := cohortGroupRetainedRootKeyFixture()
	bindVars["resource_project"] = "another_program-project-1"
	if err := validatePhysicalStageCohortGroup(stage, cohort, bindVars); err == nil {
		t.Fatal("cohort member source accepted a storage project outside the canonical selection project")
	}
}

func cohortGroupRetainedRootKeyFixture() (PhysicalConstructionStage, PhysicalStageCohortGroup, map[string]any) {
	rows := PhysicalGroupRows{
		RevisionCollectionBindKey:         "revision_collection",
		SelectionCollectionBindKey:        "selection_collection",
		DefinitionsCollectionBindKey:      "definitions_collection",
		MembershipsCollectionBindKey:      "memberships_collection",
		SelectionMembersCollectionBindKey: "selection_members_collection",
		ResourceCollectionBindKey:         "resource_collection",
		RevisionIDBindKey:                 "revision_id",
		ProjectBindKey:                    "project",
		ResourceProjectBindKey:            "resource_project",
		DatasetGenerationBindKey:          "dataset_generation",
		ResourceTypeBindKey:               "resource_type",
		PolicyBindKey:                     "policy",
		AuthResourcePathsBindKey:          "auth_resource_paths",
		AuthUnrestrictedBindKey:           "auth_unrestricted",
	}
	stage := PhysicalConstructionStage{
		InputColumns: []PhysicalStageColumn{
			{
				ID: "root_key", Name: "_key", Kind: "string", Cardinality: "required_one",
				Internal: true, RootContributorResourceType: "Patient",
			},
			{
				ID: "expanded_identity", Name: "__loom_expansion_identity", Kind: "string", Cardinality: "required_one",
				Internal: true, Identity: true,
			},
		},
		OutputColumns: []PhysicalStageColumn{{
			ID: "contributors", Name: "__loom_root_contributor_keys", Kind: "string", Cardinality: "many",
			Internal: true, RootContributorResourceType: "Patient",
		}},
	}
	cohort := PhysicalStageCohortGroup{
		Rows: rows, ContributorInputColumn: "_key", RootContributorOutputColumn: "__loom_root_contributor_keys",
		RootContributorVariable: "__loom_contributors",
	}
	bindVars := map[string]any{
		"revision_collection":          "loom_explorer_explicit_group_revisions",
		"selection_collection":         "loom_explorer_selections",
		"definitions_collection":       "loom_explorer_explicit_group_definitions",
		"memberships_collection":       "loom_explorer_explicit_group_memberships",
		"selection_members_collection": "loom_explorer_selection_members",
		"resource_collection":          "Patient",
		"revision_id":                  "revision-1", "project": "fixture_program/project-1", "resource_project": "fixture_program-project-1", "dataset_generation": "generation-1",
		"resource_type": "Patient", "policy": "EXCLUDE", "auth_resource_paths": []string{"/allowed"},
		"auth_unrestricted": false,
	}
	return stage, cohort, bindVars
}
