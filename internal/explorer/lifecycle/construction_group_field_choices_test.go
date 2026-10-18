package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestGroupConsumedFieldBoundariesFollowExactCompiledOccurrencePrefix(t *testing.T) {
	expansion := &explorer.ReceiptConstructionGroupFieldSourceExpansion{
		OwnerOccurrenceID: "observation-owner",
		OwnerResourceType: "Observation",
		Selector:          "component[].extension[]",
		Ancestors: []explorer.ReceiptConstructionGroupFieldSourceAncestor{
			{StepIndex: 0, BoundarySelector: "component[]", ResourceType: "ObservationComponent"},
		},
	}
	ancestor := explorer.ReceiptConstructionGroupFieldOwner{
		Role: "ANCESTOR", OccurrenceID: "observation-owner", OwnerResourceType: "Observation",
		ResourceType: "ObservationComponent", BoundarySelector: "component[]", StepIndex: 0,
	}
	got, ok := groupConsumedFieldBoundaries(expansion, ancestor)
	if !ok || !reflect.DeepEqual(got, []string{"component[]"}) {
		t.Fatalf("consumed ancestor prefix = %#v, %t, want exact component[] boundary", got, ok)
	}

	staleStep := ancestor
	staleStep.StepIndex = 1
	if got, ok := groupConsumedFieldBoundaries(expansion, staleStep); ok {
		t.Fatalf("accepted stale ancestor step index with same apparent role: %#v", got)
	}

	currentItem := explorer.ReceiptConstructionGroupFieldOwner{
		Role: "CURRENT_ITEM", OccurrenceID: "observation-owner", OwnerResourceType: "Observation",
		ResourceType: "Extension", BoundarySelector: "component[].extension[]",
	}
	got, ok = groupConsumedFieldBoundaries(expansion, currentItem)
	if !ok || !reflect.DeepEqual(got, []string{"component[]", "component[].extension[]"}) {
		t.Fatalf("consumed current-item prefix = %#v, %t, want ordered ancestor plus active item selector", got, ok)
	}

	wrongOwner := currentItem
	wrongOwner.OccurrenceID = "other-observation"
	if got, ok := groupConsumedFieldBoundaries(expansion, wrongOwner); ok {
		t.Fatalf("accepted a different owner occurrence: %#v", got)
	}
}

func TestResolveConstructionGroupFieldSourcesRejectsChoiceFromChangedSourceShape(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := readySnapshot("project-a", "generation-a", "snapshot-a", scope)
	snapshot.Nodes = []capability.Node{{
		ID: "observation-node", ResourceType: "Observation", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true,
	}}
	snapshot.Candidates = []capability.Candidate{{
		ID: "status-candidate", NodeID: "observation-node", ResourceType: "Observation",
		FieldPath: "status", Label: "Status", LogicalType: "string", Cardinality: "optional_one",
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true,
	}}
	oldChoice, err := capability.NewGroupFieldConstructionChoice("snapshot-a", nil, capability.GroupFieldChoiceSource{
		SourceStageID: "source_projection", SourceShapeID: "shape-old", SourceIdentityColumn: "_key",
		SourceRole: "ROOT", OwnerOccurrenceID: authoringv2.RootOccurrenceID, OwnerResourceType: "Observation",
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation", CandidateID: "status-candidate",
		NodeID: "observation-node", CanonicalPath: "status", FHIRType: "code", Cardinality: "ONE", Shape: "SCALAR", ValueKind: "SCALAR",
	}, snapshot.Candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	base := constructionBase{
		snapshot:   snapshot,
		authorized: AuthorizedCapability{Snapshot: snapshot, Scope: scope},
		document: authoringv2.Document{
			RootResourceType: "Observation",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Observation"},
		},
		stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			GroupFieldSourceContext: &explorer.ReceiptConstructionGroupFieldSourceContext{
				SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "shape-new", SourceIdentityColumn: "_key",
				FieldOwners: []explorer.ReceiptConstructionGroupFieldOwner{{
					Role: "ROOT", OccurrenceID: authoringv2.RootOccurrenceID,
					OwnerResourceType: "Observation", ResourceType: "Observation",
				}},
			},
		}},
	}
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "group", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: "group", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys: []authoringv2.ConstructionGroupKey{{
				FieldSource:    &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: oldChoice.ChoiceID},
				OutputColumnID: "status-group",
			}},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row-count"}},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "status-group", Name: "status_group", Label: "Status", Type: "string"},
			{ID: "row-count", Name: "row_count", Label: "Rows", Type: "integer"},
		},
	}}}
	service := &Service{}
	_, err = service.resolveConstructionGroupFieldSources(context.Background(), base, "observations", candidate)
	if got := lifecycleErrorCode(err); got != "STALE_GROUP_FIELD_CHOICE" {
		t.Fatalf("choice from old source shape error = %v (code %q), want stale-choice rejection", err, got)
	}
}

func TestMarkSavedGroupFieldChoicesUsesExactGroupInputAndOutputKey(t *testing.T) {
	candidate := capability.Candidate{
		ID: "status-candidate", NodeID: "patient-node", ResourceType: "Patient", FieldPath: "active",
		LogicalType: "boolean", Cardinality: "optional_one", Populated: true,
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
	}
	choice, err := capability.NewGroupFieldConstructionChoice("snapshot-a", nil, capability.GroupFieldChoiceSource{
		SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "shape-a", SourceIdentityColumn: "_key",
		SourceRole: "ROOT", OwnerOccurrenceID: authoringv2.RootOccurrenceID, OwnerResourceType: "Patient",
		OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient", CandidateID: candidate.ID,
		NodeID: candidate.NodeID, CanonicalPath: candidate.FieldPath, FHIRType: "boolean", Cardinality: "ONE", Shape: "SCALAR", ValueKind: "SCALAR",
	}, candidate)
	if err != nil {
		t.Fatal(err)
	}
	base := constructionBase{construction: authoringv2.Construction{Steps: []authoringv2.ConstructionStep{{
		ID: "group", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			Keys: []authoringv2.ConstructionGroupKey{{
				OutputColumnID: "active_key",
				FieldSource:    constructionGroupFieldBinding(choice.Source.(capability.GroupFieldChoiceSource)),
			}},
		}},
	}}}}
	choices := []ConstructionGroupFieldChoice{{
		Kind: "FIELD_SOURCE", ValueKind: "SCALAR",
		FieldSource: &authoringv2.ConstructionGroupFieldSource{Kind: "CHOICE", ChoiceID: choice.ChoiceID},
	}}
	markSavedGroupFieldChoices(base, recipe.ConstructionSourceProjectionID, "group", choices)
	if choices[0].MatchedOutputKeyID != "active_key" {
		t.Fatalf("matchedOutputKeyId = %q, want exact saved output key", choices[0].MatchedOutputKeyID)
	}

	choices[0].MatchedOutputKeyID = ""
	markSavedGroupFieldChoices(base, "different-stage", "group", choices)
	if choices[0].MatchedOutputKeyID != "" {
		t.Fatalf("marked a binding from a different Group input stage: %q", choices[0].MatchedOutputKeyID)
	}
	choices[0].MatchedOutputKeyID = ""
	markSavedGroupFieldChoices(base, recipe.ConstructionSourceProjectionID, "different-group", choices)
	if choices[0].MatchedOutputKeyID != "" {
		t.Fatalf("marked a binding from a different Group step: %q", choices[0].MatchedOutputKeyID)
	}
}

func TestGroupRelatedChoiceCandidatesRequireObservedPopulatedFields(t *testing.T) {
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	scalar := capability.Candidate{
		ID: "status", NodeID: "observation-node", ResourceType: "Observation", FieldPath: "status",
		LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		Observed: true, Populated: true,
	}
	if _, ok := populatedGroupScalarCandidate(scalar, index); !ok {
		t.Fatal("omitted a populated observed scalar source")
	}
	unobserved := scalar
	unobserved.Observed = false
	if _, ok := populatedGroupScalarCandidate(unobserved, index); ok {
		t.Fatal("offered a schema-only scalar source with no observed data")
	}
	unpopulated := scalar
	unpopulated.Populated = false
	if _, ok := populatedGroupScalarCandidate(unpopulated, index); ok {
		t.Fatal("offered a scalar source with no populated values")
	}
	id := capability.Candidate{
		ID: "specimen-id", NodeID: "specimen-node", ResourceType: "Specimen", FieldPath: "id",
		LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		Observed: true, Populated: true,
	}
	if facts, ok := populatedGroupScalarCandidate(id, index); !ok || facts.FHIRType != "string" {
		t.Fatalf("omitted canonical id or lost its generated FHIR datatype: facts=%#v ok=%t", facts, ok)
	}
	if !fhirNonGroupableGroupFieldPath("contained.id") || fhirNonGroupableGroupFieldPath("id") {
		t.Fatal("Group field identifier exception must allow only canonical id, not other identifier paths")
	}
	identifier := capability.Candidate{
		ID: "specimen-identifier-value", NodeID: "specimen-node", ResourceType: "Specimen", FieldPath: "identifier[].value",
		LogicalType: "string", Cardinality: "many", RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "identifier[]"}},
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray}, Observed: true, Populated: true,
	}
	if _, ok := populatedGroupScalarCandidate(identifier, index); ok {
		t.Fatal("offered repeated Identifier[].value as an ordinary scalar Group field")
	}

	coding := capability.Candidate{
		ID: "coding-code", NodeID: "observation-node", ResourceType: "Observation",
		FieldPath: "component[].code.coding[].code", LogicalType: "string", Cardinality: "many",
		RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "component[].code.coding[]"}}, Observed: true, Populated: true,
	}
	if !observedGroupCodingSelector(coding, coding.NodeID, coding.ResourceType, "component[].code.coding[]") {
		t.Fatal("omitted an observed, populated Coding selector")
	}
	coding.Populated = false
	if observedGroupCodingSelector(coding, coding.NodeID, coding.ResourceType, "component[].code.coding[]") {
		t.Fatal("offered a schema-only Coding selector without populated evidence")
	}
}

func TestConstructionGroupRelatedChoicesIssueObservedScalarAndCodingOnSharedRoute(t *testing.T) {
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	seed := readySnapshot("project-a", "generation-a", "related-group-choice-snapshot", scope)
	candidates := []capability.Candidate{
		{
			ID: "patient-id", NodeID: "patient-node", ResourceType: "Patient",
			FieldPath: "id", Label: "Patient ID", LogicalType: "string", Cardinality: "optional_one",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true,
		},
		{
			ID: "specimen-id", NodeID: "specimen-node", ResourceType: "Specimen",
			FieldPath: "id", Label: "Specimen ID", LogicalType: "string", Cardinality: "optional_one",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true,
		},
		{
			ID: "specimen-identifier-value", NodeID: "specimen-node", ResourceType: "Specimen",
			FieldPath: "identifier[].value", Label: "Identifier", LogicalType: "string", Cardinality: "many",
			RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "identifier[]"}},
			ProjectionModes:    []capability.ProjectionMode{capability.ProjectionArray}, Observed: true, Populated: true,
		},
		{
			ID: "specimen-coding-code", NodeID: "specimen-node", ResourceType: "Specimen",
			FieldPath: "type.coding[].code", Label: "Specimen type", LogicalType: "string", Cardinality: "many",
			RepeatedBoundaries: []capability.RepeatedBoundary{{Path: "type.coding[]"}},
			ProjectionModes:    []capability.ProjectionMode{capability.ProjectionArray}, Observed: true, Populated: true,
		},
	}
	snapshot := capability.NewSnapshot(seed.Identity, seed.Policy, capability.StatusReady, true, false,
		[]capability.Node{
			{ID: "patient-node", ResourceType: "Patient", RowRootEligible: true, Populated: true},
			{ID: "specimen-node", ResourceType: "Specimen", Populated: true},
		},
		[]capability.Edge{{
			ID: "patient-specimen", FromNodeID: "patient-node", ToNodeID: "specimen-node",
			SourceResourceType: "Patient", TargetResourceType: "Specimen", Label: "subject_Patient", StorageDirection: "INBOUND",
		}}, candidates, nil)
	base := constructionBase{
		snapshot: snapshot, authorized: AuthorizedCapability{Snapshot: snapshot, Scope: scope},
		document: authoringv2.Document{
			RootResourceType: "Patient",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		},
	}
	stage := explorer.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		GroupFieldSourceContext: &explorer.ReceiptConstructionGroupFieldSourceContext{
			SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "source-shape", SourceIdentityColumn: "_key",
			FieldOwners: []explorer.ReceiptConstructionGroupFieldOwner{{
				Role: "ROOT", OccurrenceID: authoringv2.RootOccurrenceID,
				OwnerResourceType: "Patient", ResourceType: "Patient",
			}},
		},
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	if got := observedRelatedCodingSelectors(candidates[3], index); len(got) != 1 || got[0].path != "type.coding[]" {
		t.Fatalf("observed Specimen Coding selectors = %#v, want type.coding[]", got)
	}
	choices, incomplete, err := (&Service{}).constructionGroupFieldChoices(context.Background(), base, "patients", stage, nil, false, "", "")
	if err != nil {
		t.Fatalf("issue related Group choices: %v", err)
	}
	if incomplete {
		t.Fatal("one authorized route should be fully discovered")
	}
	var rootID, relatedID, coding *ConstructionGroupFieldChoice
	for index := range choices {
		choice := &choices[index]
		if choice.SourceRole == "ROOT" && choice.CanonicalPath == "id" {
			rootID = choice
		}
		if choice.SourceRole == "RELATED_OCCURRENCE" && choice.ValueKind == "SCALAR" && choice.CanonicalPath == "id" {
			relatedID = choice
		}
		if choice.SourceRole == "RELATED_OCCURRENCE" && choice.ValueKind == "CODING_IDENTITY" && choice.CanonicalPath == "type.coding[]" {
			coding = choice
		}
	}
	if rootID == nil || relatedID == nil || coding == nil {
		t.Fatalf("Group choices = %#v, want Patient.id, related Specimen.id and Coding identity", choices)
	}
	for _, choice := range choices {
		if choice.CanonicalPath == "identifier[].value" {
			t.Fatal("ordinary Group choices exposed a repeated Identifier[].value field")
		}
	}
	if rootID.ResourceType != "Patient" || rootID.OwnerResourceType != "Patient" ||
		relatedID.ResourceType != "string" || relatedID.OwnerResourceType != "Specimen" ||
		coding.ResourceType != "Coding" || coding.OwnerResourceType != "Specimen" {
		t.Fatalf("Group choice datatype/owner metadata = root(%q,%q) related(%q,%q) coding(%q,%q)", rootID.ResourceType, rootID.OwnerResourceType, relatedID.ResourceType, relatedID.OwnerResourceType, coding.ResourceType, coding.OwnerResourceType)
	}
	if relatedID.RelatedOccurrence == nil || coding.RelatedOccurrence == nil ||
		relatedID.RelatedOccurrence.RouteID == "" || relatedID.RelatedOccurrence.RouteID != coding.RelatedOccurrence.RouteID {
		t.Fatalf("scalar and Coding choices do not share the exact route identity: scalar=%#v coding=%#v", relatedID.RelatedOccurrence, coding.RelatedOccurrence)
	}
	for _, choice := range []*ConstructionGroupFieldChoice{relatedID, coding} {
		identity, decodeErr := capability.DecodeConstructionChoiceID(choice.FieldSource.ChoiceID)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		source, ok := identity.Source.(capability.GroupFieldChoiceSource)
		if !ok || source.Input == nil || source.Input.Kind != "RELATED_OCCURRENCE" || source.Input.RelatedOccurrence == nil ||
			source.Input.RelatedOccurrence.TargetResourceType != "Specimen" || source.Input.RelatedOccurrence.RouteID != "" {
			t.Fatalf("signed related choice lost route/value-owner distinction: %#v", identity.Source)
		}
	}
}

type countingGroupRowChoicePlanner struct {
	calls int
}

func (p *countingGroupRowChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error) {
	p.calls++
	return nil, nil
}

func TestConstructionGroupRowValueChoicesSkipCodingRowChoiceDiscovery(t *testing.T) {
	planner := &countingGroupRowChoicePlanner{}
	service := &Service{config: Config{RowChoicePlanner: planner}}
	stage := explorer.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "__loom_expansion_identity",
		GroupFieldSourceContext: &explorer.ReceiptConstructionGroupFieldSourceContext{
			SourceStageID: recipe.ConstructionSourceProjectionID, SourceShapeID: "shape-expanded", SourceIdentityColumn: "__loom_expansion_identity",
			SourceExpansion: &explorer.ReceiptConstructionGroupFieldSourceExpansion{
				OwnerOccurrenceID: "observation-owner", OwnerResourceType: "Observation", Selector: "component[].code.coding[]",
			},
		},
	}
	base := constructionBase{construction: authoringv2.Construction{Steps: []authoringv2.ConstructionStep{{
		ID: "group", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{}},
	}}}}

	choices, err := service.constructionGroupRowValueChoices(context.Background(), base, "observations", stage, "group")
	if err != nil {
		t.Fatalf("row-value choices: %v", err)
	}
	if len(choices) != 0 {
		t.Fatalf("row-value choices = %#v, want no scalar sources from empty field-owner fixture", choices)
	}
	if planner.calls != 0 {
		t.Fatalf("row-choice planner calls = %d, want none for scalar-only RowValues", planner.calls)
	}
}

func TestConstructionGroupRelatedFieldChoicesSkipExpandedSource(t *testing.T) {
	service := &Service{}
	fieldContext := &explorer.ReceiptConstructionGroupFieldSourceContext{
		SourceStageID: "source_projection", SourceShapeID: "expanded-shape",
		SourceExpansion: &explorer.ReceiptConstructionGroupFieldSourceExpansion{
			OwnerOccurrenceID: "observation-owner", OwnerResourceType: "Observation", Selector: "component[]",
		},
	}
	choices := map[string]ConstructionGroupFieldChoice{}
	incomplete, err := service.appendRelatedGroupFieldChoices(
		context.Background(), constructionBase{}, explorer.ReceiptConstructionStage{}, fieldContext, nil, choices,
	)
	if err != nil {
		t.Fatalf("related choices for expanded source: %v", err)
	}
	if incomplete {
		t.Fatal("expanded source should skip related-choice discovery without reporting incomplete discovery")
	}
	if len(choices) != 0 {
		t.Fatalf("expanded source related choices = %#v, want none", choices)
	}
}

func TestConstructionGroupFieldChoicesSortSameLabelBySourceIdentity(t *testing.T) {
	choices := []ConstructionGroupFieldChoice{
		{Kind: "FIELD_SOURCE", Label: "Code", CanonicalPath: "status", SourceRole: "ROOT", OwnerResourceType: "Observation", OwnerOccurrenceID: "root", FieldSource: &authoringv2.ConstructionGroupFieldSource{ChoiceID: "choice-root"}},
		{Kind: "FIELD_SOURCE", Label: "Code", CanonicalPath: "status", SourceRole: "OWNER", OwnerResourceType: "Patient", OwnerOccurrenceID: "patient-owner", FieldSource: &authoringv2.ConstructionGroupFieldSource{ChoiceID: "choice-patient"}},
		{Kind: "FIELD_SOURCE", Label: "Code", CanonicalPath: "status", SourceRole: "OWNER", OwnerResourceType: "Observation", OwnerOccurrenceID: "owner-b", FieldSource: &authoringv2.ConstructionGroupFieldSource{ChoiceID: "choice-b"}},
		{Kind: "FIELD_SOURCE", Label: "Code", CanonicalPath: "status", SourceRole: "OWNER", OwnerResourceType: "Observation", OwnerOccurrenceID: "owner-a", FieldSource: &authoringv2.ConstructionGroupFieldSource{ChoiceID: "choice-z"}},
		{Kind: "FIELD_SOURCE", Label: "Code", CanonicalPath: "status", SourceRole: "OWNER", OwnerResourceType: "Observation", OwnerOccurrenceID: "owner-a", FieldSource: &authoringv2.ConstructionGroupFieldSource{ChoiceID: "choice-a"}},
	}

	sortConstructionGroupFieldChoices(choices)
	got := make([]string, 0, len(choices))
	for _, choice := range choices {
		got = append(got, choice.FieldSource.ChoiceID)
	}
	want := []string{"choice-a", "choice-z", "choice-b", "choice-patient", "choice-root"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted source choice IDs = %#v, want %#v", got, want)
	}
}
