package authoringv2

import (
	"reflect"
	"testing"
)

func TestDescribeRowIdentityKeepsNetworkEligibilitySeparateFromRows(t *testing.T) {
	document := Document{Output: Output{ID: "subjects"}, RootResourceType: "ResearchSubject", Rows: RecordsRowDefinition(), Route: RouteNode{
		OccurrenceID: "base", ResourceType: "ResearchSubject", Children: []RouteNode{
			{OccurrenceID: "observations", ResourceType: "Observation", CatalogEdgeID: "observation-subject", MatchMode: RouteMatchOptional},
			{OccurrenceID: "specimens", ResourceType: "Specimen", CatalogEdgeID: "specimen-subject", MatchMode: RouteMatchRequired},
		},
	}}
	plan := DescribeRowIdentity(document)
	if len(plan.Stages) != 1 || plan.Stages[0].Identity.Kind != "RECORD" || !plan.Stages[0].MayExcludeRows {
		t.Fatalf("network changed row identity or hid required-match row loss: %#v", plan)
	}
	for index, effect := range plan.Relationships {
		if !effect.PreservesIdentity || effect.MayExcludeRows != (index == 1) || effect.Multiplicity != "UNKNOWN" {
			t.Fatalf("incorrect effect: %#v", effect)
		}
	}
	if plan.Stages[0].Count.Kind != "UNKNOWN" || plan.Stages[0].Count.Value != nil {
		t.Fatal("invented a count from graph metadata")
	}
}

func TestDescribeRelatedExpansionUsesInputRowPairsAndExactAnchor(t *testing.T) {
	document := Document{Output: Output{ID: "subjects"}, RootResourceType: "ResearchSubject", Route: RouteNode{OccurrenceID: "base"}, Rows: RecordsRowDefinition(), Construction: &Construction{Version: 1, Steps: []ConstructionStep{
		{ID: "observations", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}, Operation: ConstructionOperation{Kind: ConstructionOperationRelatedExpand, RelatedExpand: &ConstructionRelatedExpand{
			AnchorColumnID: "subject-id", TargetResourceType: "Observation", RelatedRecordColumnID: "observation-id", EmptyPolicy: ConstructionExpandEmptyPreserveParent,
		}}},
		{ID: "values", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "observations"}}, Operation: ConstructionOperation{Kind: ConstructionOperationRelatedSource}},
		{ID: "required", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "values"}}, Operation: ConstructionOperation{Kind: ConstructionOperationRelatedEligibility}},
	}}}
	plan := DescribeRowIdentity(document)
	identity := plan.Stages[1].Identity
	if identity.Kind != "RELATED_RECORD_PAIR" || identity.Scope != "PER_INPUT_ROW" || identity.InputStageID != "source_projection" || identity.AnchorColumnID != "subject-id" || identity.RelatedRecordColumnID != "observation-id" {
		t.Fatalf("related expansion lost pair identity or retargeted anchor: %#v", identity)
	}
	if !reflect.DeepEqual(plan.Stages[2].Identity, identity) || !reflect.DeepEqual(plan.Stages[3].Identity, identity) || !plan.Stages[3].MayExcludeRows {
		t.Fatalf("later values/filter changed grain: %#v", plan.Stages)
	}
	if plan.SourceDefinition.Kind != RowDefinitionRecords {
		t.Fatal("construction expansion rewrote source Rows")
	}
}

func TestDescribeRowIdentityGroupRetainsCompleteOrderedTuple(t *testing.T) {
	document := Document{Rows: RecordsRowDefinition(), Construction: &Construction{Steps: []ConstructionStep{{ID: "group", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{MissingKeyPolicy: ConstructionGroupMissingKeyExclude, Keys: []ConstructionGroupKey{
			{InputColumnID: "gender", OutputColumnID: "key-gender"}, {InputColumnID: "site", OutputColumnID: "key-site"},
		}}}}}}}
	stage := DescribeRowIdentity(document).Stages[1]
	if stage.Identity.Kind != "KEY_TUPLE" || !reflect.DeepEqual(stage.Identity.KeyColumnIDs, []string{"key-gender", "key-site"}) || stage.Identity.MissingPolicy != "EXCLUDE" || !stage.MayExcludeRows {
		t.Fatalf("incomplete grouping identity: %#v", stage)
	}
}

func TestDescribeRowIdentityDistinguishesSourceAndConstructionItems(t *testing.T) {
	document := Document{Rows: RowDefinition{Kind: RowDefinitionExpanded, Expanded: &ExpandedRows{OccurrenceID: "patient-a", ScopePath: "name", EmptyCollectionPolicy: EmptyCollectionPreserveParent}}, Construction: &Construction{Steps: []ConstructionStep{
		{ID: "expand", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}}, Operation: ConstructionOperation{Kind: ConstructionOperationExpand, Expand: &ConstructionExpand{InputColumnID: "given", EmptyPolicy: ConstructionExpandEmptyExclude}}},
	}}}
	plan := DescribeRowIdentity(document)
	if plan.Stages[0].Identity.Kind != "SOURCE_ITEM" || plan.Stages[0].Identity.OccurrenceID != "patient-a" || plan.Stages[0].Identity.ScopePath != "name" || plan.Stages[0].Identity.MissingPolicy != "PRESERVE_PARENT" {
		t.Fatalf("lost item source: %#v", plan)
	}
	if plan.Stages[1].Identity.Kind != "COLUMN_ITEM" || plan.Stages[1].Identity.Scope != "PER_INPUT_ROW" || !plan.Stages[1].MayExcludeRows {
		t.Fatal("construction item expansion mislabeled")
	}
}

func TestDescribeRowIdentityExplicitGroupsAndUnavailableSourceFieldGroup(t *testing.T) {
	document := Document{Rows: RowDefinition{Kind: RowDefinitionGroups, Groups: &GroupedRows{Source: GroupSource{Kind: GroupSourceExplicit, Explicit: &ExplicitGroupSource{RevisionID: "cohort-v1", UnassignedMemberPolicy: UnassignedMemberExclude}}}}}
	stage := DescribeRowIdentity(document).Stages[0]
	if stage.Identity.Kind != "NAMED_GROUP" || stage.Identity.GroupRevisionID != "cohort-v1" || stage.Identity.MissingPolicy != "EXCLUDE" {
		t.Fatalf("named group identity lost: %#v", stage)
	}
	document.Rows.Groups.Source = GroupSource{Kind: GroupSourceField, Field: &FieldGroupSource{OccurrenceID: "base", FieldPath: "gender", MissingKeyPolicy: MissingKeyExclude}}
	stage = DescribeRowIdentity(document).Stages[0]
	if stage.Identity.Kind != "UNSUPPORTED" || stage.UnsupportedReason == "" {
		t.Fatal("advertised rejected source FIELD grouping")
	}
}

func TestImportedPreserveRootChangeRequiresSourceRootOverlay(t *testing.T) {
	workspace := rowChangeWorkspace()
	workspace.Documents[0].ImportedRecipe = &ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: "immutable", Output: "patients"}
	assessment, err := AssessRowChange(workspace, rowChangeCatalog(), RowChangeRequest{OutputID: "patients", RootNodeID: "encounter", RootOccurrenceID: "encounter"})
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeBlocked || assessment.Proposal != nil || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "IMPORTED_SOURCE_ROOT_OVERLAY_REQUIRED" {
		t.Fatalf("unsafe projected root edit permitted: %#v", assessment)
	}
}

func TestNestedRequiredBelowOptionalDoesNotClaimRootRowLoss(t *testing.T) {
	document := Document{Rows: RecordsRowDefinition(), Route: RouteNode{OccurrenceID: "base", Children: []RouteNode{{OccurrenceID: "optional-patient", MatchMode: RouteMatchOptional, Children: []RouteNode{{OccurrenceID: "required-observation", MatchMode: RouteMatchRequired}}}}}}
	plan := DescribeRowIdentity(document)
	if plan.Stages[0].MayExcludeRows || plan.Relationships[1].MayExcludeRows {
		t.Fatalf("required descendant below optional parent misreported source exclusion: %#v", plan)
	}
	document.Route.Children[0].MatchMode = RouteMatchRequired
	plan = DescribeRowIdentity(document)
	if !plan.Stages[0].MayExcludeRows || !plan.Relationships[1].MayExcludeRows {
		t.Fatalf("complete required prefix failed to report exclusion: %#v", plan)
	}
}

func TestRowChangeSameResourceTypeRespectsSelectedOccurrence(t *testing.T) {
	workspace := rowChangeWorkspace()
	catalog := rowChangeCatalog()
	workspace.Documents[0].Route.Children[0].Children = []RouteNode{{OccurrenceID: "patient-again", ResourceType: "Patient", Relationship: "patient", CatalogEdgeID: "encounter-patient"}}
	request := RowChangeRequest{OutputID: "patients", RootNodeID: "patient", RootOccurrenceID: "patient-again"}
	assessment, err := AssessRowChange(workspace, catalog, request)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status == RowChangeNoChange {
		t.Fatal("distinct Patient occurrence silently treated as current root")
	}
	if assessment.Proposal != nil && assessment.Proposal.RootOccurrenceID != "patient-again" {
		t.Fatalf("selected occurrence retargeted: %#v", assessment)
	}
	workspace.Documents[0].ImportedRecipe = &ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: "immutable", Output: "patients"}
	assessment, err = AssessRowChange(workspace, catalog, request)
	if err != nil {
		t.Fatal(err)
	}
	if assessment.Status != RowChangeBlocked || len(assessment.Unresolved) != 1 || assessment.Unresolved[0].Code != "IMPORTED_SOURCE_ROOT_OVERLAY_REQUIRED" {
		t.Fatalf("imported same-type root escaped source authority: %#v", assessment)
	}
	request.RootOccurrenceID = "base"
	assessment, err = AssessRowChange(workspace, catalog, request)
	if err != nil || assessment.Status != RowChangeNoChange {
		t.Fatalf("current imported root should remain no-change: %#v %v", assessment, err)
	}
}
