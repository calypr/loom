package authoringv2

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func codedGroupDocument() Document {
	document := workspaceDocument("observations")
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	document.Columns = []Column{
		constructionSourceColumn("observation_id", "observation_id", "Observation ID", "string"),
		constructionSourceColumn("site_id", "site", "Site", "string"),
	}
	family := capability.SemanticFrameFamily{
		BindingID: "observation-type-binding", ResourceType: "Observation", SourcePath: "type.coding",
		OwningScope: "Observation", KeyPath: "type.coding", ValuePath: "type", LogicalType: "string",
		RuleVersion: "1", SchemaVersion: 1,
	}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{
		{
			ID: "coded_input", OwnerStepID: "group_by_type",
			Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationCodedPivot, CodedPivot: &ConstructionCodedPivot{
				ConstructionID:  "coded_input",
				Source:          &ConstructionCodedPivotSource{Family: family, CandidateID: "observation-candidate", NodeID: "observation-node", FieldPath: "type.coding"},
				Categories:      []ConstructionCodedPivotCategory{{System: "urn:example", Code: "specimen_type", OutputColumnID: "specimen_source"}},
				DuplicatePolicy: ConstructionPivotDuplicateError, MissingCellPolicy: ConstructionPivotMissingNull,
			}},
			RowValues: []ConstructionRowValue{{InputColumnID: "site_id", OutputColumnID: "site_input", Policy: ConstructionRowValueOne}},
			Outputs: []StageColumn{
				{ID: "specimen_source", Name: "specimen_type", Label: "Specimen type", Type: "string"},
				{ID: "site_input", Name: "site", Label: "Site", Type: "string"},
			},
		},
		{
			ID: "group_by_type", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "coded_input"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
				ConstructionID: "group_by_type", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
				Keys: []ConstructionGroupKey{
					{InputColumnID: "specimen_source", OutputColumnID: "specimen_group"},
					{InputColumnID: "site_input", OutputColumnID: "site_group"},
				},
				Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "records"}},
			}},
			Outputs: []StageColumn{
				{ID: "specimen_group", Name: "specimen_type", Label: "Specimen type", Type: "string"},
				{ID: "site_group", Name: "site", Label: "Site", Type: "string"},
				{ID: "records", Name: "records", Label: "Records", Type: "integer"},
			},
		},
	}}
	return document
}

func TestGroupOwnedCodedPivotAcceptsExactOneInputsAndRecalculatesOwnedEdit(t *testing.T) {
	accepted := codedGroupDocument()
	if err := accepted.Validate(); err != nil {
		t.Fatalf("valid owned CODED_PIVOT to GROUP chain rejected: %v", err)
	}
	candidate, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Steps[0].Operation.CodedPivot.Categories[0].Code = "specimen_type_v2"
	replaced, impact, err := accepted.AnalyzeConstructionCandidate(*candidate, "group_by_type", nil)
	if err != nil {
		t.Fatalf("edit owned coded selection: %v", err)
	}
	if !reflect.DeepEqual(impact.AffectedStepIDs, []string{"coded_input", "group_by_type"}) {
		t.Fatalf("owned helper edit did not recalculate from its root: %#v", impact)
	}
	if err := replaced.Validate(); err != nil {
		t.Fatalf("edited compound construction invalid: %v", err)
	}
	if replaced.Construction.Steps[0].ID != "coded_input" || replaced.Construction.Steps[0].RowValues[0].OutputColumnID != "site_input" {
		t.Fatalf("edit changed stable owned helper identity: %#v", replaced.Construction.Steps[0])
	}
}

func TestGroupOwnedCodedPivotRejectsAmbiguousPassthroughPolicy(t *testing.T) {
	document := codedGroupDocument()
	document.Construction.Steps[0].RowValues[0].Policy = ConstructionRowValueAll
	if err := document.Validate(); err == nil {
		t.Fatal("GROUP-owned coded helper accepted a multi-value passthrough")
	}
}

func TestGroupEditRemovingCodedKeyPrunesOwnedHelper(t *testing.T) {
	accepted := codedGroupDocument()
	candidate, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	candidate.Steps = []ConstructionStep{{
		ID: "group_by_type", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
			ConstructionID: "group_by_type", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
			Keys:       []ConstructionGroupKey{{InputColumnID: "site_id", OutputColumnID: "site_group"}},
			Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "records"}},
		}},
		Outputs: []StageColumn{
			{ID: "site_group", Name: "site", Label: "Site", Type: "string"},
			{ID: "records", Name: "records", Label: "Records", Type: "integer"},
		},
	}}
	removed, impact, err := accepted.AnalyzeConstructionCandidate(*candidate, "group_by_type", nil)
	if err != nil {
		t.Fatalf("remove the final coded key: %v", err)
	}
	if len(removed.Construction.Steps) != 1 || removed.Construction.Steps[0].ID != "group_by_type" || !reflect.DeepEqual(impact.RemovedStepIDs, []string{"coded_input"}) {
		t.Fatalf("removing the last coded key did not prune its owned helper: construction=%#v impact=%#v", removed.Construction, impact)
	}
}
