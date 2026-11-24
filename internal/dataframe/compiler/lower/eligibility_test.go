package lower

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileRelatedEligibilityPreservesRowsAndCountsDistinctTerminalResources(t *testing.T) {
	exactValue := "final"
	contributorSource := func(candidate, path, cardinality string) *recipe.ConstructionRelatedFieldSource {
		return &recipe.ConstructionRelatedFieldSource{
			CandidateID: candidate, NodeID: "observation-node", ResourceType: "Observation",
			Path: path, Cardinality: cardinality, LogicalType: "string",
		}
	}
	for _, match := range []struct {
		kind                 string
		threshold            *int
		contributorSource    *recipe.ConstructionRelatedFieldSource
		contributorPredicate *recipe.ConstructionRelatedPredicate
	}{
		{kind: recipe.RelatedEligibilityExists,
			contributorSource:    contributorSource("observation-value-string", "Observation.valueString", "optional_one"),
			contributorPredicate: &recipe.ConstructionRelatedPredicate{CandidateID: "observation-value-string", Operator: recipe.FilterExists}},
		{kind: recipe.RelatedEligibilityAbsent},
		{kind: recipe.RelatedEligibilityCountAtLeast, threshold: intPointer(2),
			contributorSource: contributorSource("observation-id", "Observation.id", "required_one"),
			contributorPredicate: &recipe.ConstructionRelatedPredicate{CandidateID: "observation-id", Operator: recipe.FilterEquals,
				Value: &recipe.FilterValue{Kind: recipe.FilterString, String: &exactValue}}},
	} {
		t.Run(match.kind, func(t *testing.T) {
			output := constructionTestOutput()
			output.Construction = &recipe.Construction{
				Version: 1,
				SourceColumns: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"},
					{ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"},
				},
				Steps: []recipe.ConstructionStep{{
					ID: "eligible_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
						AnchorColumnID: "_key", ChoiceID: "patient-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
						Route: []recipe.ConstructionRelatedRouteStep{{
							EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
							FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
							StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
						}}, ContributorPolicy: "ALL_MATCHES", MatchKind: match.kind, Threshold: match.threshold,
						ContributorSource: match.contributorSource, ContributorPredicate: match.contributorPredicate,
						ContributorChoiceID: func() string {
							if match.contributorPredicate != nil {
								return "observation-contributor-choice"
							}
							return ""
						}(),
					}},
					Outputs: []recipe.StageColumn{
						{ID: "group_id", Name: "group", Label: "Group"},
						{ID: "category_id", Name: "category", Label: "Category"},
						{ID: "amount_id", Name: "amount", Label: "Amount"},
					},
				}},
			}

			compiled := compileDerivedTestOutput(t, output)
			if len(compiled.Plan.StageSequence.Stages) != 1 {
				t.Fatalf("related eligibility stage count = %d, want 1", len(compiled.Plan.StageSequence.Stages))
			}
			stage := compiled.Plan.StageSequence.Stages[0]
			if stage.Kind != ir.PhysicalStageRelatedEligibilityOp || stage.Filter == nil {
				t.Fatalf("lowered stage = %#v, want typed related eligibility filter", stage)
			}
			if stage.RowIdentityColumn != stage.InputColumns[len(stage.InputColumns)-1].Name {
				t.Fatalf("eligibility changed row identity: input=%#v output=%q", stage.InputColumns, stage.RowIdentityColumn)
			}
			if len(stage.OutputColumns) != len(stage.InputColumns) {
				t.Fatalf("eligibility changed output schema width: in=%d out=%d", len(stage.InputColumns), len(stage.OutputColumns))
			}
			rendered, err := aql.Render(compiled.Plan, aql.Options{})
			if err != nil {
				t.Fatal(err)
			}
			switch match.kind {
			case recipe.RelatedEligibilityExists:
				if stage.Filter.Expression.Kind != ir.PhysicalExistsPredicate || !strings.Contains(rendered.Query, "LIMIT 1") || !strings.Contains(rendered.Query, "valueString") {
					t.Fatalf("EXISTS must compile to bounded existence: %#v\n%s", stage.Filter, rendered.Query)
				}
			case recipe.RelatedEligibilityAbsent:
				if stage.Filter.Expression.Kind != ir.PhysicalNotPredicate || !strings.Contains(rendered.Query, "FILTER NOT (LENGTH(") {
					t.Fatalf("ABSENT must negate bounded existence: %#v\n%s", stage.Filter, rendered.Query)
				}
			case recipe.RelatedEligibilityCountAtLeast:
				if len(stage.DerivedLets) != 1 || stage.DerivedLets[0].ExpressionLet.Expression.Call == nil || !strings.Contains(rendered.Query, "SORTED_UNIQUE") || !strings.Contains(rendered.Query, ">= @related_eligibility_threshold") || !strings.Contains(rendered.Query, "__loom_physical_construction_filter_value == @related_eligibility_contributor_value") {
					t.Fatalf("COUNT_AT_LEAST must count distinct terminal IDs through a scalar LET: lets=%#v\n%s", stage.DerivedLets, rendered.Query)
				}
				if rendered.BindVars["related_eligibility_threshold"] != 2 {
					t.Fatalf("threshold bind = %#v, want 2", rendered.BindVars["related_eligibility_threshold"])
				}
				if rendered.BindVars["related_eligibility_contributor_value"] != exactValue {
					t.Fatalf("contributor exact-value bind = %#v, want %q", rendered.BindVars["related_eligibility_contributor_value"], exactValue)
				}
			}
		})
	}
}

func TestRelatedEligibilityAfterGroupUsesCompilerOwnedRootContributors(t *testing.T) {
	for _, match := range []struct {
		name      string
		kind      string
		threshold *int
	}{
		{name: "exists", kind: recipe.RelatedEligibilityExists},
		{name: "absent", kind: recipe.RelatedEligibilityAbsent},
		{name: "distinct count", kind: recipe.RelatedEligibilityCountAtLeast, threshold: intPointer(2)},
	} {
		t.Run(match.name, func(t *testing.T) {
			output := constructionTestOutput()
			output.Fields = []recipe.Field{{Name: "gender", ColumnID: "gender_id", Expr: recipe.Expression{Select: "root.gender"}}}
			groupColumns := []recipe.StageColumn{
				{ID: "group_gender_id", Name: "gender", Type: "string"},
				{ID: "patient_count_id", Name: "patient_count", Type: "integer"},
			}
			output.Construction = &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "gender_id", Name: "gender", Label: "Gender", Type: "string"}},
				Steps: []recipe.ConstructionStep{
					{ID: "group_patients", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
						Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
							ConstructionID: "group_patients",
							Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "gender_id", OutputColumnID: "group_gender_id"}},
							Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "patient_count_id"}},
						}}, Outputs: groupColumns},
					{ID: "eligible_groups", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "group_patients"}},
						Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
							AnchorColumnID: rootContributorSetColumn, ChoiceID: "patient-observation-choice",
							TargetNodeID: "observation-node", TargetResourceType: "Observation",
							Route: []recipe.ConstructionRelatedRouteStep{{
								EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
								FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
								StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
							}}, ContributorPolicy: "ALL_MATCHES", MatchKind: match.kind, Threshold: match.threshold,
						}}, Outputs: groupColumns},
				},
			}

			compiled := compileDerivedTestOutput(t, output)
			if len(compiled.Stages) != 3 || len(compiled.Plan.StageSequence.Stages) != 2 {
				t.Fatalf("compiled stages = %#v, want source, Group, and related eligibility", compiled.Stages)
			}
			groupStage := compiled.Plan.StageSequence.Stages[0]
			eligibility := compiled.Plan.StageSequence.Stages[1]
			if groupStage.Group == nil || groupStage.Group.RootContributorOutputColumn != rootContributorSetColumn {
				t.Fatalf("Group did not materialize contributors for filtering: %#v", groupStage.Group)
			}
			if eligibility.Kind != ir.PhysicalStageRelatedEligibilityOp || eligibility.Filter == nil {
				t.Fatalf("lowered stage = %#v, want related-eligibility filter", eligibility)
			}
			if eligibility.Filter.Expression.Kind != ir.PhysicalExistsPredicate && match.kind == recipe.RelatedEligibilityExists ||
				eligibility.Filter.Expression.Kind != ir.PhysicalNotPredicate && match.kind == recipe.RelatedEligibilityAbsent ||
				len(eligibility.DerivedLets) != 1 && match.kind == recipe.RelatedEligibilityCountAtLeast {
				t.Fatalf("match %s lowered to unexpected filter/LETs: %#v / %#v", match.kind, eligibility.Filter, eligibility.DerivedLets)
			}
			capable := false
			for _, capability := range compiled.Stages[1].Capabilities {
				if capability.Operation == recipe.ConstructionRelatedEligibilityOp {
					capable = capability.Supported
				}
			}
			if !capable || len(compiled.Stages[1].RelatedExpandAnchors) != 1 ||
				compiled.Stages[1].RelatedExpandAnchors[0].Kind != "rootContributors" ||
				compiled.Stages[1].RelatedExpandAnchors[0].AnchorColumnID != rootContributorSetColumn {
				t.Fatalf("Group stage did not advertise its compiler-owned eligibility anchor: %#v", compiled.Stages[1])
			}
			var subplan *ir.PhysicalSubplan
			switch match.kind {
			case recipe.RelatedEligibilityExists:
				subplan = eligibility.Filter.Expression.Exists
			case recipe.RelatedEligibilityAbsent:
				subplan = eligibility.Filter.Expression.Children[0].Exists
			case recipe.RelatedEligibilityCountAtLeast:
				subplan = eligibility.DerivedLets[0].ExpressionLet.Expression.Call.Args[0].Subplan
			}
			if subplan == nil || len(subplan.Operations) < 2 || subplan.Operations[0].CollectionScan == nil ||
				subplan.Operations[1].Filter == nil || subplan.Operations[1].Filter.Predicate.Operator != "IN" ||
				subplan.Operations[1].Filter.Predicate.Right == nil ||
				!equalStringSlices(subplan.Operations[1].Filter.Predicate.Right.Path, []string{rootContributorSetColumn}) {
				t.Fatalf("eligibility did not scan the exact compiler-owned contributor set: %#v", subplan)
			}
			if err := compiled.Plan.Validate(); err != nil {
				t.Fatalf("valid grouped eligibility plan does not validate: %v", err)
			}
			if match.kind == recipe.RelatedEligibilityCountAtLeast {
				scanSource := subplan.Operations[0].Source.ResourceType
				if scanSource != "Patient" {
					t.Fatalf("COUNT root scan source type = %q, want Patient", scanSource)
				}
				_ = ir.CanonicalExecutionPhysicalPlan(compiled.Plan)
				if got := subplan.Operations[0].Source.ResourceType; got != scanSource {
					t.Fatalf("canonical fingerprint changed source plan metadata from %q to %q", scanSource, got)
				}
				if err := compiled.Plan.Validate(); err != nil {
					t.Fatalf("COUNT plan no longer validates after canonical fingerprinting: %v", err)
				}
			}
			originalCollectionBind := subplan.Operations[0].CollectionScan.CollectionBindKey
			compiled.Plan.BindVars["alternate_root_collection"] = "Patient"
			subplan.Operations[0].CollectionScan.CollectionBindKey = "alternate_root_collection"
			if err := compiled.Plan.Validate(); err == nil {
				t.Fatal("eligibility scan with an alternate collection binding unexpectedly validated")
			}
			subplan.Operations[0].CollectionScan.CollectionBindKey = originalCollectionBind
			delete(compiled.Plan.BindVars, "alternate_root_collection")
			eligibility.InputColumns = append([]ir.PhysicalStageColumn(nil), eligibility.InputColumns...)
			for index := range eligibility.InputColumns {
				if eligibility.InputColumns[index].Name == rootContributorSetColumn {
					eligibility.InputColumns[index].RootContributorResourceType = "Observation"
				}
			}
			compiled.Plan.StageSequence.Stages[1] = eligibility
			if err := compiled.Plan.Validate(); err == nil {
				t.Fatal("eligibility with a mismatched contributor resource type unexpectedly validated")
			}
		})
	}
}

func TestRelatedEligibilityKeepsRootAnchorAfterRowExpansion(t *testing.T) {
	output := constructionTestOutput()
	output.RootOccurrenceID = "patient-root"
	output.RowGrain = "expanded"
	output.Expand = &recipe.Expansion{
		OwnerOccurrenceID: "patient-root", From: recipe.Expression{Select: "root.identifier[]"},
		As: "item", Ordinality: "position", EmptyPolicy: recipe.ExpansionPreserveParent,
	}
	output.Identity = &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}}
	output.Construction.Steps = []recipe.ConstructionStep{{
		ID: "eligible_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
			AnchorColumnID: "_key", ChoiceID: "patient-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			Route: []recipe.ConstructionRelatedRouteStep{{
				EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorPolicy: "ALL_MATCHES", MatchKind: recipe.RelatedEligibilityExists,
		}},
		Outputs: output.Construction.SourceColumns,
	}}

	compiled := compileDerivedTestOutput(t, output)
	if got := compiled.Plan.StageSequence.SourceRowIdentity; got != "__loom_expansion_identity" {
		t.Fatalf("source row identity = %q, want expanded-row identity", got)
	}
	if got := compiled.Stages[0].RelatedExpandAnchors; len(got) != 1 || got[0].AnchorColumnID != "_key" {
		t.Fatalf("expanded source lost exact root resource anchor: %#v", got)
	}
	if compiled.Plan.StageSequence.Stages[0].Kind != ir.PhysicalStageRelatedEligibilityOp {
		t.Fatalf("related eligibility did not compile after row expansion")
	}
}

func TestRelatedEligibilityUsesActiveRelatedRecordAnchor(t *testing.T) {
	output := constructionTestOutput()
	_, activeObservationID := relatedExpandIdentityColumnNames("expand_observations")
	output.Construction = &recipe.Construction{
		Version: 1,
		SourceColumns: []recipe.StageColumn{
			{ID: "group_id", Name: "group", Label: "Group"},
			{ID: "category_id", Name: "category", Label: "Category"},
			{ID: "amount_id", Name: "amount", Label: "Amount"},
		},
		Steps: []recipe.ConstructionStep{
			{
				ID: "expand_observations", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedExpandOp, RelatedExpand: &recipe.ConstructionRelatedExpand{
					AnchorColumnID: "_key", ChoiceID: "patient-observation-choice", TargetNodeID: "observation-node", TargetResourceType: "Observation",
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node", FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", EmptyPolicy: recipe.ExpansionPreserveParent, RelatedRecordColumnID: "observation_id",
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"}, {ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"}, {ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
				},
			},
			{
				ID: "eligible_specimens", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_observations"}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionRelatedEligibilityOp, RelatedEligibility: &recipe.ConstructionRelatedEligibility{
					AnchorColumnID: activeObservationID, ChoiceID: "observation-specimen-choice", TargetNodeID: "specimen-node", TargetResourceType: "Specimen",
					Route:             []recipe.ConstructionRelatedRouteStep{{EdgeID: "observation-specimen", FromNodeID: "observation-node", ToNodeID: "specimen-node", FromResourceType: "Observation", ToResourceType: "Specimen", Relationship: "specimen_Specimen", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"}},
					ContributorPolicy: "ALL_MATCHES", MatchKind: recipe.RelatedEligibilityExists,
				}},
				Outputs: []recipe.StageColumn{
					{ID: "group_id", Name: "group", Label: "Group"}, {ID: "category_id", Name: "category", Label: "Category"},
					{ID: "amount_id", Name: "amount", Label: "Amount"}, {ID: "observation_id", Name: "observation_id", Label: "Observation ID", Type: "string", Nullable: true},
				},
			},
		},
	}

	compiled := compileDerivedTestOutput(t, output)
	stage := compiled.Plan.StageSequence.Stages[1]
	if stage.Kind != ir.PhysicalStageRelatedEligibilityOp || stage.RowIdentityColumn != compiled.Plan.StageSequence.Stages[0].RowIdentityColumn {
		t.Fatalf("active-anchor eligibility changed row identity or operation: %#v", stage)
	}
	if compiled.Stages[2].ActiveRelatedRecord == nil || compiled.Stages[2].ActiveRelatedRecord.TargetNodeID != "observation-node" {
		t.Fatalf("eligibility did not preserve active related-record capability: %#v", compiled.Stages[2])
	}
	rendered, err := aql.Render(compiled.Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "DOCUMENT(@@related_eligibility_anchor_collection") || !strings.Contains(rendered.Query, "PARSE_IDENTIFIER(") {
		t.Fatalf("active related anchor must use exact document lookup: %s", rendered.Query)
	}
}
