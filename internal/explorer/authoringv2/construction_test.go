package authoringv2

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/capability"
)

func stagedConstructionDocument() Document {
	document := workspaceDocument("patients")
	document.Columns = []Column{
		constructionSourceColumn("person_id", "person_id", "Person ID", "integer"),
		constructionSourceColumn("group_id", "group_id", "Group", "string"),
		constructionSourceColumn("category_id", "category", "Category", "string"),
		constructionSourceColumn("value_id", "value", "Value", "integer"),
	}
	group := StageColumn{ID: "group_id", Name: "group_id", Label: "Group", Type: "string"}
	categoryA := StageColumn{ID: "pivot_a", Name: "amount_a", Label: "Amount A", Type: "integer"}
	categoryB := StageColumn{ID: "pivot_b", Name: "amount_b", Label: "Amount B", Type: "integer"}
	pivotOutputs := []StageColumn{group, categoryA, categoryB}
	quotient := StageColumn{ID: "ratio_id", Name: "ratio", Label: "Ratio", Type: "decimal"}
	derivedOutputs := append(append([]StageColumn(nil), pivotOutputs...), quotient)
	filterValue := int64(1)
	filterOutputs := append([]StageColumn(nil), derivedOutputs...)
	finalOutputs := []StageColumn{
		group,
		quotient,
		{ID: "measure_key", Name: "measure", Label: "Measure"},
		{ID: "measure_value", Name: "measure_value", Label: "Measure value", Type: "integer"},
	}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{
		{
			ID:     "pivot_step",
			Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &ConstructionPivot{
				ConstructionID: "pivot_step", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "value_id",
				Categories: []ConstructionPivotCategory{
					{Key: stringScalar("A"), OutputColumnID: "pivot_a"},
					{Key: stringScalar("B"), OutputColumnID: "pivot_b"},
				},
				DuplicatePolicy: ConstructionPivotDuplicateSum, MissingCellPolicy: ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ConstructionPivotUnlistedError,
			}},
			Outputs: pivotOutputs,
		},
		{
			ID: "derive_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "pivot_step"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &ConstructionDerive{
				ConstructionID: "derive_step", OutputColumnID: "ratio_id", Operation: ConstructionDerivedDivide,
				Left:               ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: "pivot_a"},
				Right:              ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: "pivot_b"},
				MissingInputPolicy: ConstructionMissingInputPropagateNull, DivisionByZeroPolicy: ConstructionDivisionByZeroNull,
			}}, Outputs: derivedOutputs,
		},
		{
			ID: "filter_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "derive_step"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{
				ColumnID: "ratio_id", Operator: ConstructionFilterGreaterEq,
				Values: []FilterValue{{Kind: ConstructionFilterInteger, Integer: &filterValue}},
			}}, Outputs: filterOutputs,
		},
		{
			ID: "unpivot_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "filter_step"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationUnpivot, Unpivot: &ConstructionUnpivot{
				ConstructionID: "unpivot_step",
				Inputs: []ConstructionUnpivotInput{
					{ColumnID: "pivot_a", Key: stringScalar("A")},
					{ColumnID: "pivot_b", Key: stringScalar("B")},
				},
				KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: ConstructionUnpivotPreserve,
			}}, Outputs: finalOutputs,
		},
	}}
	return document
}

func constructionSourceColumn(id, name, label, logicalType string) Column {
	return Column{
		ColumnID: id, Column: name, Label: label, LogicalType: logicalType, OccurrenceID: RootOccurrenceID,
		Source: ColumnSource{Kind: SourceProjectID},
	}
}

func stringScalar(value string) TableScalar {
	return TableScalar{Kind: TableScalarString, String: &value}
}

func constructionWorkspace(document Document) Workspace {
	return Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: CurrentSemanticsVersion,
		Explorer:  ExplorerMetadata{Title: "Construction test"},
		Documents: []Document{document},
		Tabs:      []Tab{{ID: "tab-patients", Title: "Patients", OutputID: document.Output.ID, Order: 0, Visible: true}},
	}
}

func TestConstructionValidatesAndRoundTripsTypedOperationChain(t *testing.T) {
	document := stagedConstructionDocument()
	if err := document.Validate(); err != nil {
		t.Fatalf("valid typed construction rejected: %v", err)
	}

	workspace := constructionWorkspace(document)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	decoded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("decode canonical workspace: %v", err)
	}
	if got := decoded.Documents[0].Construction; !reflect.DeepEqual(got, document.Construction) {
		t.Fatalf("construction changed during save/reload:\n got: %#v\nwant: %#v", got, document.Construction)
	}
}

func TestScalarGroupSourceProjectionRoundTripsAndPrunesOnRemoval(t *testing.T) {
	document := workspaceDocument("patients")
	document.Columns = nil
	projection := ConstructionSourceProjection{
		ColumnID: "source_active", OccurrenceID: RootOccurrenceID, FieldPath: "active",
		FHIRType: "boolean", LogicalType: "boolean", Label: "Whether this record is in active use",
	}
	candidate := Construction{
		Version: ConstructionVersion, SourceProjections: []ConstructionSourceProjection{projection},
		Steps: []ConstructionStep{{
			ID: "group_by_active", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationGroup, Group: &ConstructionGroup{
				ConstructionID: "group_by_active", Keys: []ConstructionGroupKey{{InputColumnID: projection.ColumnID, OutputColumnID: "active_key"}},
				Aggregates: []ConstructionGroupAggregate{{Operation: ConstructionGroupCountRows, OutputColumnID: "record_count"}},
			}},
			Outputs: []StageColumn{
				{ID: "active_key", Name: "active", Label: projection.Label, Type: "boolean"},
				{ID: "record_count", Name: "record_count", Label: "Record count", Type: "integer"},
			},
		}},
	}
	grouped, _, err := document.AnalyzeConstructionCandidate(candidate, "group_by_active", nil)
	if err != nil {
		t.Fatalf("analyze scalar source GROUP: %v", err)
	}
	if len(grouped.Columns) != 0 || len(grouped.Construction.SourceProjections) != 1 {
		t.Fatalf("grouped document public columns/source projections = %d/%#v", len(grouped.Columns), grouped.Construction.SourceProjections)
	}
	source, err := constructionSourceColumnsWithChildrenAndProjections(grouped.Columns, grouped.Construction.SourceProjections,
		grouped.Construction.Steps[0].Operation.inputColumnIDs(), grouped.Construction.Steps[0].Outputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(source) != 1 || source[0].ID != projection.ColumnID || len(grouped.Construction.Steps[0].Outputs) != 2 {
		t.Fatalf("source stage/final group schema = %#v/%#v", source, grouped.Construction.Steps[0].Outputs)
	}
	encoded, err := json.Marshal(grouped)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded Document
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Validate(); err != nil {
		t.Fatalf("reloaded scalar source GROUP: %v", err)
	}
	removed, _, err := reloaded.ProposeStepRemoval("group_by_active", nil)
	if err != nil {
		t.Fatalf("remove scalar source GROUP: %v", err)
	}
	if len(removed.Construction.SourceProjections) != 0 || len(removed.Construction.Steps) != 0 {
		t.Fatalf("removed construction retained source projections or steps: %#v", removed.Construction)
	}
}

func TestPivotOwnedInputsStayPrivateAndAreRemovedWithPivot(t *testing.T) {
	document := workspaceDocument("patients")
	document.Columns = []Column{
		constructionSourceColumn("patient_id", "patient_id", "Patient ID", "string"),
		constructionSourceColumn("amount", "amount", "Amount", "integer"),
	}
	projection := ConstructionSourceProjection{
		ColumnID: "pivot_status", OwnerStepID: "pivot_step", OccurrenceID: RootOccurrenceID,
		FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Patient.active",
	}
	sourceColumns := []StageColumn{
		{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
		{ID: "amount", Name: "amount", Label: "Amount", Type: "integer"},
		{ID: projection.ColumnID, Name: ConstructionSourceProjectionName(projection.ColumnID), Label: projection.Label, Type: projection.LogicalType},
	}
	relatedSource := ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-status", NodeID: "observation-node",
		ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
	}
	relatedOutputs := append(append([]StageColumn(nil), sourceColumns...), StageColumn{
		ID: "pivot_observation_status", Name: "__pivot_source_status", Label: "Observation.status", Type: "string", Nullable: true,
	})
	active := true
	candidate := Construction{
		Version:           ConstructionVersion,
		SourceProjections: []ConstructionSourceProjection{projection},
		Steps: []ConstructionStep{
			{
				ID: "pivot_status_input", OwnerStepID: "pivot_step",
				Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
				Operation: ConstructionOperation{Kind: ConstructionOperationRelatedField, RelatedField: &ConstructionRelatedField{
					ChoiceID: "stage-bound-choice", Source: relatedSource, OutputColumnID: "pivot_observation_status",
				}},
				Outputs: relatedOutputs,
			},
			{
				ID: "pivot_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "pivot_status_input"}},
				Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &ConstructionPivot{
					ConstructionID: "pivot_step", GroupKeyIDs: []string{"patient_id"},
					CategoryColumnID: projection.ColumnID, ValueColumnID: "pivot_observation_status",
					Categories:      []ConstructionPivotCategory{{Key: TableScalar{Kind: TableScalarBoolean, Boolean: &active}, OutputColumnID: "active_amount"}},
					DuplicatePolicy: ConstructionPivotDuplicateError, MissingCellPolicy: ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: ConstructionPivotUnlistedError,
				}},
				Outputs: []StageColumn{
					{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: "active_amount", Name: "active_amount", Label: "Active amount", Type: "string"},
				},
			},
		},
	}
	accepted, _, err := document.AnalyzeConstructionCandidate(candidate, "pivot_step", nil)
	if err != nil {
		t.Fatalf("accept Pivot-owned source and related inputs: %v", err)
	}
	if len(accepted.Columns) != 2 || len(accepted.Construction.SourceProjections) != 1 ||
		accepted.Construction.SourceProjections[0].OwnerStepID != "pivot_step" || len(accepted.Construction.Steps) != 2 {
		t.Fatalf("Pivot inputs leaked into source columns or were not persisted privately: %#v", accepted.Construction)
	}
	t.Run("removal rebuilds preceding passthrough stages", func(t *testing.T) {
		withPrefix := cloneDocumentForConstructionChange(accepted)
		prefix := ConstructionStep{
			ID: "keep_patient", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{ColumnID: "patient_id", Operator: ConstructionFilterExists}},
			Outputs:   append([]StageColumn(nil), sourceColumns...),
		}
		withPrefix.Construction.Steps[0].Inputs = []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: prefix.ID}}
		withPrefix.Construction.Steps = append([]ConstructionStep{prefix}, withPrefix.Construction.Steps...)
		if err := withPrefix.Validate(); err != nil {
			t.Fatal(err)
		}
		removed, _, err := withPrefix.ProposeStepRemoval("pivot_step", nil)
		if err != nil || len(removed.Construction.Steps) != 1 || len(removed.Construction.Steps[0].Outputs) != 2 {
			t.Fatalf("preceding schema was not restored: %v %#v", err, removed.Construction)
		}
		candidate := *withPrefix.Construction
		candidate.Steps = candidate.Steps[:1]
		removed, _, err = withPrefix.AnalyzeConstructionCandidate(candidate, "", []string{"pivot_step"})
		if err != nil || len(removed.Construction.Steps[0].Outputs) != 2 {
			t.Fatalf("candidate removal did not restore preceding schema: %v", err)
		}
		if _, _, err := withPrefix.AnalyzeConstructionCandidate(*removed.Construction, "", []string{"pivot_step"}); err != nil {
			t.Fatalf("canonical removal candidate cannot be committed: %v", err)
		}

	})
	removed, impact, err := accepted.ProposeStepRemoval("pivot_step", nil)
	if err != nil {
		t.Fatalf("remove Pivot and owned inputs: %v", err)
	}
	if len(removed.Construction.Steps) != 0 || len(removed.Construction.SourceProjections) != 0 || len(impact.RemovedStepIDs) != 2 {
		t.Fatalf("removing Pivot did not restore the base construction: construction=%#v impact=%#v", removed.Construction, impact)
	}
}

func TestPivotEditCanDropOwnedRootProjectionAcrossPrefix(t *testing.T) {
	document := workspaceDocument("patients")
	document.Columns = []Column{
		constructionSourceColumn("patient_id", "patient_id", "Patient ID", "string"),
		constructionSourceColumn("amount", "amount", "Amount", "integer"),
		constructionSourceColumn("category", "category", "Category", "string"),
	}
	projection := ConstructionSourceProjection{
		ColumnID: "pivot_status", OwnerStepID: "pivot_step", OccurrenceID: RootOccurrenceID,
		FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Patient.active",
	}
	active := true
	filterOutputs := []StageColumn{
		{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
		{ID: "amount", Name: "amount", Label: "Amount", Type: "integer"},
		{ID: "category", Name: "category", Label: "Category", Type: "string"},
		{ID: projection.ColumnID, Name: ConstructionSourceProjectionName(projection.ColumnID), Label: projection.Label, Type: projection.LogicalType},
	}
	candidate := Construction{
		Version: ConstructionVersion, SourceProjections: []ConstructionSourceProjection{projection},
		Steps: []ConstructionStep{
			{
				ID: "filter_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
				Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{
					ColumnID: "patient_id", Operator: ConstructionFilterExists,
				}}, Outputs: filterOutputs,
			},
			{
				ID: "pivot_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "filter_step"}},
				Operation: ConstructionOperation{Kind: ConstructionOperationPivot, Pivot: &ConstructionPivot{
					ConstructionID: "pivot_step", GroupKeyIDs: []string{"patient_id"},
					CategoryColumnID: projection.ColumnID, ValueColumnID: "amount",
					Categories:      []ConstructionPivotCategory{{Key: TableScalar{Kind: TableScalarBoolean, Boolean: &active}, OutputColumnID: "active_amount"}},
					DuplicatePolicy: ConstructionPivotDuplicateError, MissingCellPolicy: ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: ConstructionPivotUnlistedError,
				}},
				Outputs: []StageColumn{
					{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: "active_amount", Name: "active_amount", Label: "Active amount", Type: "integer"},
				},
			},
		},
	}
	acceptedFilter := candidate.Steps[0]
	acceptedFilter.Outputs = acceptedFilter.Outputs[:3]
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{acceptedFilter}}
	accepted, _, err := document.AnalyzeConstructionCandidate(candidate, "pivot_step", nil)
	if err != nil {
		t.Fatalf("accept Pivot-owned root projection through filter: %v", err)
	}
	updated, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	updated.SourceProjections = nil
	updated.Steps[0].Outputs = updated.Steps[0].Outputs[:3]
	updated.Steps[1].Operation.Pivot.CategoryColumnID = "category"
	updated.Steps[1].Operation.Pivot.Categories = []ConstructionPivotCategory{{Key: stringScalar("final"), OutputColumnID: "final_amount"}}
	updated.Steps[1].Outputs = []StageColumn{
		{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
		{ID: "final_amount", Name: "final_amount", Label: "Final amount", Type: "integer"},
	}
	replaced, _, err := accepted.AnalyzeConstructionCandidate(*updated, "pivot_step", nil)
	if err != nil {
		t.Fatalf("replace Pivot bindings and drop owned root projection: %v", err)
	}
	if len(replaced.Construction.SourceProjections) != 0 || len(replaced.Construction.Steps[0].Outputs) != 3 {
		t.Fatalf("replaced Pivot retained its private source field: %#v", replaced.Construction)
	}
}

func TestEmptyConstructionSerializesStepsAsArray(t *testing.T) {
	document := workspaceDocument("patients")
	document.Rows = RecordsRowDefinition()
	document.Columns = []Column{}
	document.Construction = &Construction{Version: ConstructionVersion}
	workspace := constructionWorkspace(document)

	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	if !strings.Contains(string(encoded), `"steps":[]`) {
		t.Fatalf("empty construction steps were not persisted as an array: %s", encoded)
	}

	reloaded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("decode canonical workspace: %v", err)
	}
	steps := reloaded.Documents[0].Construction.Steps
	if steps == nil || len(steps) != 0 {
		t.Fatalf("empty construction steps reloaded as %#v, want non-nil empty slice", steps)
	}
}

func TestFreshDocumentUpgradesToSourceOnlyConstructionAndAcceptsFirstStep(t *testing.T) {
	fresh := workspaceDocument("patients")
	upgraded, err := UpgradeDocumentToConstruction(fresh)
	if err != nil {
		t.Fatalf("upgrade fresh document: %v", err)
	}
	if upgraded.Construction == nil || upgraded.Construction.Version != ConstructionVersion || len(upgraded.Construction.Steps) != 0 {
		t.Fatalf("fresh construction = %#v", upgraded.Construction)
	}
	if upgraded.Columns[0].ColumnID == "" {
		t.Fatal("fresh source projection has no persisted stable column ID")
	}
	if err := upgraded.Validate(); err != nil {
		t.Fatalf("source-only construction is invalid: %v", err)
	}

	sourceColumn := upgraded.Columns[0]
	sourceStage := StageColumn{ID: sourceColumn.ColumnID, Name: sourceColumn.Column, Label: sourceColumn.Label, Type: sourceColumn.LogicalType}
	candidate := Construction{Version: ConstructionVersion, Steps: []ConstructionStep{{
		ID: "first_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
		Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{
			ColumnID: sourceColumn.ColumnID, Operator: ConstructionFilterExists,
		}},
		Outputs: []StageColumn{sourceStage},
	}}}
	firstStep, impact, err := upgraded.AnalyzeConstructionCandidate(candidate, "first_filter", nil)
	if err != nil {
		t.Fatalf("append first step to source-only construction: %v", err)
	}
	if !reflect.DeepEqual(impact.AffectedStepIDs, []string{"first_filter"}) {
		t.Fatalf("first-step impact = %#v", impact)
	}
	if err := firstStep.Validate(); err != nil {
		t.Fatalf("first-step candidate is invalid: %v", err)
	}
	if len(upgraded.Construction.Steps) != 0 {
		t.Fatal("adding the first step mutated the source-only accepted document")
	}
}

func TestConstructionRejectsMixedLegacyAndStagedSemantics(t *testing.T) {
	document := stagedConstructionDocument()
	document.TableShape = &TableShape{Derived: []DerivedConstruction{{
		ConstructionID: "legacy-derived", Output: ColumnOutput{Column: "old_result", Label: "Old result"}, Operation: "ADD",
		Left:               ArithmeticOperand{Kind: "COLUMN", Column: "value"},
		Right:              ArithmeticOperand{Kind: "LITERAL", Literal: &TableScalar{Kind: TableScalarInteger, Integer: int64Pointer(1)}},
		MissingInputPolicy: "PROPAGATE_NULL",
	}}}
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "cannot both define") {
		t.Fatalf("mixed construction modes error = %v", err)
	}

	document.TableShape = nil
	document.Construction.Steps[2].Operation.Filter.ColumnID = "missing_column"
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "missing column id") {
		t.Fatalf("missing filter column error = %v", err)
	}
}

func TestUpgradeDocumentToConstructionMigratesAndReloadsLegacyShape(t *testing.T) {
	legacy := legacyPivotDocument()
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy document is invalid: %v", err)
	}
	original := legacy
	original.Columns = append([]Column(nil), legacy.Columns...)
	original.TableShape, _ = cloneTableShape(legacy.TableShape)

	upgraded, err := UpgradeDocumentToConstruction(legacy)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if upgraded.TableShape != nil || upgraded.Construction == nil {
		t.Fatalf("upgrade did not establish one staged plan: %#v", upgraded)
	}
	if len(upgraded.Construction.Steps) != 3 {
		t.Fatalf("migrated steps = %d, want pivot plus two derived steps", len(upgraded.Construction.Steps))
	}
	if upgraded.Construction.Steps[0].Operation.Kind != ConstructionOperationPivot {
		t.Fatalf("first migrated operation = %q, want PIVOT", upgraded.Construction.Steps[0].Operation.Kind)
	}
	if got := upgraded.Construction.Steps[1].Operation.Derive.OutputColumnID; got == "" {
		t.Fatal("migrated derived output has no stable identity")
	}
	if upgraded.Construction.Steps[1].Operation.Derive.OutputColumnID == upgraded.Construction.Steps[2].Operation.Derive.OutputColumnID {
		t.Fatal("migrated derived columns share one stable identity")
	}
	if got := upgraded.Construction.Steps[0].Operation.Pivot.DuplicatePolicy; got != ConstructionPivotDuplicateMax {
		t.Fatalf("pivot duplicate policy = %q", got)
	}
	if got := upgraded.Construction.Steps[0].Operation.Pivot.UnlistedCategoryPolicy; got != ConstructionPivotUnlistedExcludeWithEvidence {
		t.Fatalf("pivot unlisted policy = %q", got)
	}
	for _, column := range upgraded.Columns {
		if column.ColumnID == "" {
			t.Fatalf("source column %q did not receive a stable id", column.Column)
		}
	}
	if !reflect.DeepEqual(legacy, original) {
		t.Fatal("migration mutated the accepted legacy document")
	}

	workspace := constructionWorkspace(upgraded)
	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical upgraded workspace: %v", err)
	}
	decoded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("decode upgraded workspace: %v", err)
	}
	reloaded := decoded.Documents[0]
	if !reflect.DeepEqual(reloaded.Construction, upgraded.Construction) {
		t.Fatal("migrated operation identities or policies changed after reload")
	}
	idsByName := make(map[string]string, len(upgraded.Columns))
	for _, column := range upgraded.Columns {
		idsByName[column.Column] = column.ColumnID
	}
	for _, column := range reloaded.Columns {
		if column.ColumnID != idsByName[column.Column] {
			t.Fatalf("source column id changed for %q: %q != %q", column.Column, column.ColumnID, idsByName[column.Column])
		}
	}

	beforeRename := upgraded.Columns[1].ColumnID
	upgraded.Columns[1].Column = "group_renamed"
	upgraded.Columns[1].Label = "Renamed group"
	if err := upgraded.Validate(); err != nil {
		t.Fatalf("rename with stable identity: %v", err)
	}
	if upgraded.Columns[1].ColumnID != beforeRename || upgraded.Construction.Steps[0].Operation.Pivot.GroupKeyIDs[0] != beforeRename {
		t.Fatal("renaming a source label or public name changed the column identity")
	}
	normalized := constructionWorkspace(upgraded).NormalizePresentationOrders()
	groupOutput, ok := findStageColumnByID(normalized.Documents[0].Construction.Steps[0].Outputs, beforeRename)
	if !ok || groupOutput.Name != "group_renamed" || groupOutput.Label != "Renamed group" {
		t.Fatalf("renamed output schema = %#v, found=%t", groupOutput, ok)
	}

	again, err := UpgradeDocumentToConstruction(reloaded)
	if err != nil {
		t.Fatalf("reapplying conversion to staged document: %v", err)
	}
	if !reflect.DeepEqual(again.Construction, reloaded.Construction) {
		t.Fatal("reopening and editing a staged document changed its operation identities")
	}
}

func TestUpgradeDocumentToConstructionMigratesUnpivotPolicies(t *testing.T) {
	legacy := stagedConstructionDocument()
	for i := range legacy.Columns {
		legacy.Columns[i].ColumnID = ""
	}
	legacy.Construction = nil
	legacy.TableShape = &TableShape{Reshape: &TableReshape{Kind: "UNPIVOT", Unpivot: &UnpivotConstruction{
		ConstructionID: "legacy_unpivot",
		Inputs: []UnpivotInput{
			{Column: "value", Key: stringScalar("one")},
			{Column: "category", Key: stringScalar("two")},
		},
		KeyOutput:     ColumnOutput{Column: "metric", Label: "Metric"},
		ValueOutput:   ColumnOutput{Column: "metric_value", Label: "Metric value"},
		NullRowPolicy: "PRESERVE",
	}}}
	upgraded, err := UpgradeDocumentToConstruction(legacy)
	if err != nil {
		t.Fatalf("upgrade unpivot: %v", err)
	}
	if len(upgraded.Construction.Steps) != 1 {
		t.Fatalf("migrated step count = %d", len(upgraded.Construction.Steps))
	}
	unpivot := upgraded.Construction.Steps[0].Operation.Unpivot
	if unpivot == nil || unpivot.NullRowPolicy != ConstructionUnpivotPreserve || len(unpivot.Inputs) != 2 {
		t.Fatalf("migrated unpivot = %#v", unpivot)
	}
	if unpivot.Inputs[0].Key.String == nil || *unpivot.Inputs[0].Key.String != "one" || unpivot.Inputs[1].Key.String == nil || *unpivot.Inputs[1].Key.String != "two" {
		t.Fatalf("unpivot keys changed during migration: %#v", unpivot.Inputs)
	}
	if err := upgraded.Validate(); err != nil {
		t.Fatalf("migrated unpivot invalid: %v", err)
	}
}

func TestUpgradeDocumentToConstructionRejectsLossyShapeWithoutMutation(t *testing.T) {
	legacy := legacyPivotDocument()
	legacy.TableShape.Derived[0].Left = ArithmeticOperand{Kind: "LITERAL", Literal: &TableScalar{Kind: TableScalarString, String: stringPointer("not numeric")}}
	before := legacy
	before.Columns = append([]Column(nil), legacy.Columns...)
	before.TableShape, _ = cloneTableShape(legacy.TableShape)
	if _, err := UpgradeDocumentToConstruction(legacy); err == nil || !strings.Contains(err.Error(), "not numeric") {
		t.Fatalf("lossy conversion error = %v", err)
	}
	if !reflect.DeepEqual(legacy, before) || legacy.TableShape == nil || legacy.Construction != nil {
		t.Fatal("failed conversion changed the accepted legacy document")
	}
}

func TestAnalyzeStepEditReportsMissingDownstreamColumnAndKeepsAcceptedDocument(t *testing.T) {
	accepted := documentWithDependentSteps()
	var replacement ConstructionStep
	raw, err := json.Marshal(accepted.Construction.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &replacement); err != nil {
		t.Fatal(err)
	}
	replacement.Operation.Derive.OutputColumnID = "score_v2_id"
	replacement.Outputs = append([]StageColumn(nil), replacement.Outputs[:2]...)
	replacement.Outputs = append(replacement.Outputs, StageColumn{ID: "score_v2_id", Name: "score_v2", Label: "Score v2", Type: "integer"})
	proposal, impact, err := accepted.AnalyzeStepEdit(replacement)
	if err != nil {
		t.Fatalf("analyze edit: %v", err)
	}
	if !reflect.DeepEqual(impact.AffectedStepIDs, []string{"score_filter", "bonus_step"}) {
		t.Fatalf("affected steps = %#v", impact.AffectedStepIDs)
	}
	if len(impact.MissingInputs) != 1 || impact.MissingInputs[0].StepID != "score_filter" || impact.MissingInputs[0].ColumnID != "score_id" {
		t.Fatalf("missing downstream inputs = %#v", impact.MissingInputs)
	}
	if accepted.Construction.Steps[0].Operation.Derive.OutputColumnID != "score_id" {
		t.Fatal("proposed edit mutated the accepted construction")
	}
	if proposal.Construction.Steps[0].Operation.Derive.OutputColumnID != "score_v2_id" {
		t.Fatal("proposal did not retain the edited step")
	}
}

func TestAnalyzeConstructionCandidateSupportsAppendAndRecalculatesStage(t *testing.T) {
	accepted := stagedConstructionDocument()
	construction, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	filterValue := int64(0)
	lastOutputs := append([]StageColumn(nil), construction.Steps[len(construction.Steps)-1].Outputs...)
	construction.Steps = append(construction.Steps, ConstructionStep{
		ID: "last_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "unpivot_step"}},
		Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{
			ColumnID: "measure_value", Operator: ConstructionFilterGreaterEq,
			Values: []FilterValue{{Kind: ConstructionFilterInteger, Integer: &filterValue}},
		}},
		Outputs: lastOutputs,
	})

	candidate, impact, err := accepted.AnalyzeConstructionCandidate(*construction, "last_filter", nil)
	if err != nil {
		t.Fatalf("append candidate: %v", err)
	}
	if impact.ChangedStepID != "last_filter" || !reflect.DeepEqual(impact.AffectedStepIDs, []string{"last_filter"}) {
		t.Fatalf("append impact = %#v", impact)
	}
	if got := candidate.Construction.Steps[4].Inputs; !reflect.DeepEqual(got, []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "unpivot_step"}}) {
		t.Fatalf("appended input refs = %#v", got)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatalf("appended candidate invalid: %v", err)
	}
	if len(accepted.Construction.Steps) != 4 {
		t.Fatal("append mutated accepted document")
	}
}

func TestAnalyzeConstructionCandidateRequiresExplicitRemovalsAndReportsMissingInputs(t *testing.T) {
	accepted := stagedConstructionDocument()
	withoutFilter, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	withoutFilter.Steps = append(withoutFilter.Steps[:2], withoutFilter.Steps[3:]...)
	if _, _, err := accepted.AnalyzeConstructionCandidate(*withoutFilter, "", nil); err == nil || !strings.Contains(err.Error(), "without listing it in removeStepIDs") {
		t.Fatalf("unlisted removal error = %v", err)
	}

	removed, impact, err := accepted.AnalyzeConstructionCandidate(*withoutFilter, "", []string{"filter_step"})
	if err != nil {
		t.Fatalf("explicit removal candidate: %v", err)
	}
	if !reflect.DeepEqual(impact.RemovedStepIDs, []string{"filter_step"}) || !reflect.DeepEqual(impact.AffectedStepIDs, []string{"unpivot_step"}) {
		t.Fatalf("removal impact = %#v", impact)
	}
	if err := removed.Validate(); err != nil {
		t.Fatalf("removal candidate invalid: %v", err)
	}

	changed, err := cloneConstruction(accepted.Construction)
	if err != nil {
		t.Fatal(err)
	}
	changed.Steps[0].Operation.Pivot.Categories[0].OutputColumnID = "pivot_a_replaced"
	changed.Steps[0].Outputs[1] = StageColumn{ID: "pivot_a_replaced", Name: "amount_a_new", Label: "Amount A new", Type: "integer"}
	invalid, missing, err := accepted.AnalyzeConstructionCandidate(*changed, "pivot_step", nil)
	if err != nil {
		t.Fatalf("edit with dependent issue: %v", err)
	}
	wantMissing := []ConstructionDependencyIssue{
		{StepID: "derive_step", ColumnID: "pivot_a"},
		{StepID: "unpivot_step", ColumnID: "pivot_a"},
	}
	if !reflect.DeepEqual(missing.MissingInputs, wantMissing) {
		t.Fatalf("missing dependent inputs = %#v, want %#v", missing.MissingInputs, wantMissing)
	}
	if err := invalid.Validate(); err == nil {
		t.Fatal("candidate with missing stable reference unexpectedly validated")
	}
}

func TestProposeStepRemovalRequiresExplicitDependentRemovalAndKeepsUnrelatedStep(t *testing.T) {
	accepted := documentWithDependentSteps()
	proposal, impact, err := accepted.ProposeStepRemoval("score_step", nil)
	if err != nil {
		t.Fatalf("propose removal: %v", err)
	}
	if !reflect.DeepEqual(impact.RemovedStepIDs, []string{"score_step"}) {
		t.Fatalf("removed steps = %#v", impact.RemovedStepIDs)
	}
	if !reflect.DeepEqual(impact.AffectedStepIDs, []string{"score_filter", "bonus_step"}) {
		t.Fatalf("affected steps = %#v", impact.AffectedStepIDs)
	}
	if len(impact.MissingInputs) != 1 || impact.MissingInputs[0].StepID != "score_filter" || impact.MissingInputs[0].ColumnID != "score_id" {
		t.Fatalf("missing inputs = %#v", impact.MissingInputs)
	}
	if len(proposal.Construction.Steps) != 2 || proposal.Construction.Steps[1].ID != "bonus_step" {
		t.Fatalf("unrelated later step was removed: %#v", proposal.Construction.Steps)
	}
	if accepted.Construction.Steps[0].ID != "score_step" {
		t.Fatal("proposing removal mutated the accepted construction")
	}

	repaired, resolved, err := accepted.ProposeStepRemoval("score_step", []string{"score_filter"})
	if err != nil {
		t.Fatalf("remove selected dependent explicitly: %v", err)
	}
	if resolved.HasMissingInputs() {
		t.Fatalf("explicit removal left unresolved inputs: %#v", resolved.MissingInputs)
	}
	if len(repaired.Construction.Steps) != 1 || repaired.Construction.Steps[0].ID != "bonus_step" {
		t.Fatalf("unrelated operation was not retained: %#v", repaired.Construction.Steps)
	}
	if err := repaired.Validate(); err != nil {
		t.Fatalf("repaired construction is invalid: %v", err)
	}
	if repaired.Construction.Steps[0].Inputs[0].Kind != ConstructionInputSourceProjection {
		t.Fatalf("retained step input was not rewired to source: %#v", repaired.Construction.Steps[0].Inputs)
	}
}

func TestConstructionInputRequiresPinnedTableRevision(t *testing.T) {
	valid := ConstructionInputRef{Kind: ConstructionInputTableRevision, TableID: "source_table", RevisionID: "rev_7", OutputID: "result"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("pinned table input rejected: %v", err)
	}
	if err := (ConstructionInputRef{Kind: ConstructionInputTableRevision, TableID: "source_table", OutputID: "result"}).Validate(); err == nil || !strings.Contains(err.Error(), "revisionId") {
		t.Fatalf("floating table input error = %v", err)
	}
}

func legacyPivotDocument() Document {
	document := stagedConstructionDocument()
	for i := range document.Columns {
		document.Columns[i].ColumnID = ""
	}
	document.Construction = nil
	document.TableShape = &TableShape{
		Reshape: &TableReshape{Kind: "PIVOT", Pivot: &PivotConstruction{
			ConstructionID: "legacy_pivot", GroupKeys: []string{"group_id"}, CategoryColumn: "category", ValueColumn: "value",
			Categories: []PivotCategory{
				{Key: stringScalar("A"), Output: ColumnOutput{Column: "amount_a", Label: "Amount A"}},
				{Key: stringScalar("B"), Output: ColumnOutput{Column: "amount_b", Label: "Amount B"}},
			},
			DuplicatePolicy: "MAX", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "EXCLUDE_WITH_EVIDENCE",
		}},
		Derived: []DerivedConstruction{
			{
				ConstructionID: "legacy_ratio", Output: ColumnOutput{Column: "ratio", Label: "Ratio"}, Operation: "ADD",
				Left: ArithmeticOperand{Kind: "COLUMN", Column: "twice_a"}, Right: ArithmeticOperand{Kind: "COLUMN", Column: "amount_b"},
				MissingInputPolicy: "PROPAGATE_NULL",
			},
			{
				ConstructionID: "legacy_twice", Output: ColumnOutput{Column: "twice_a", Label: "Twice A"}, Operation: "MULTIPLY",
				Left:               ArithmeticOperand{Kind: "COLUMN", Column: "amount_a"},
				Right:              ArithmeticOperand{Kind: "LITERAL", Literal: &TableScalar{Kind: TableScalarInteger, Integer: int64Pointer(2)}},
				MissingInputPolicy: "PROPAGATE_NULL",
			},
		},
	}
	return document
}

func documentWithDependentSteps() Document {
	document := workspaceDocument("patients")
	document.Columns = []Column{
		constructionSourceColumn("person_id", "person_id", "Person ID", "integer"),
		constructionSourceColumn("age_id", "age", "Age", "integer"),
	}
	person := StageColumn{ID: "person_id", Name: "person_id", Label: "Person ID", Type: "integer"}
	age := StageColumn{ID: "age_id", Name: "age", Label: "Age", Type: "integer"}
	base := []StageColumn{person, age}
	score := StageColumn{ID: "score_id", Name: "score", Label: "Score", Type: "integer"}
	scoreStage := append(append([]StageColumn(nil), base...), score)
	threshold := int64(18)
	bonus := StageColumn{ID: "bonus_id", Name: "bonus", Label: "Bonus", Type: "integer"}
	document.Construction = &Construction{Version: ConstructionVersion, Steps: []ConstructionStep{
		{
			ID: "score_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputSourceProjection}},
			Operation: ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &ConstructionDerive{
				ConstructionID: "score_step", OutputColumnID: "score_id", Operation: ConstructionDerivedAdd,
				Left:               ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: "age_id"},
				Right:              ConstructionOperand{Kind: ConstructionLiteralOperand, Literal: &ConstructionLiteral{Kind: ConstructionNumericInteger, Integer: int64Pointer(1)}},
				MissingInputPolicy: ConstructionMissingInputPropagateNull,
			}}, Outputs: scoreStage,
		},
		{
			ID: "score_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "score_step"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationFilter, Filter: &ConstructionFilter{
				ColumnID: "score_id", Operator: ConstructionFilterGreaterEq,
				Values: []FilterValue{{Kind: ConstructionFilterInteger, Integer: &threshold}},
			}}, Outputs: append([]StageColumn(nil), scoreStage...),
		},
		{
			ID: "bonus_step", Inputs: []ConstructionInputRef{{Kind: ConstructionInputStepOutput, StepID: "score_filter"}},
			Operation: ConstructionOperation{Kind: ConstructionOperationDerive, Derive: &ConstructionDerive{
				ConstructionID: "bonus_step", OutputColumnID: "bonus_id", Operation: ConstructionDerivedAdd,
				Left:               ConstructionOperand{Kind: ConstructionColumnOperand, ColumnID: "age_id"},
				Right:              ConstructionOperand{Kind: ConstructionLiteralOperand, Literal: &ConstructionLiteral{Kind: ConstructionNumericInteger, Integer: int64Pointer(2)}},
				MissingInputPolicy: ConstructionMissingInputPropagateNull,
			}}, Outputs: append(append([]StageColumn(nil), scoreStage...), bonus),
		},
	}}
	return document
}

func int64Pointer(value int64) *int64 { return &value }

func stringPointer(value string) *string { return &value }

func TestConstructionOutputPresentationPersistsWithoutChangingCompilerIdentity(t *testing.T) {
	document := documentWithDependentSteps()
	workspace := constructionWorkspace(document)
	visible, order := false, 7
	updated, _, err := ApplyCommands(workspace, commandCatalog(), "rename-derived-output", []Command{{
		Type:     CommandUpdateConstructionOutput,
		OutputID: "patients",
		ConstructionOutput: &ConstructionOutputPresentation{
			StepID:   "bonus_step",
			ColumnID: "score_id",
			Label:    "Patient score",
			Table:    &TablePresentation{Visible: &visible, Order: &order},
		},
	}})
	if err != nil {
		t.Fatalf("update construction output presentation: %v", err)
	}

	for index, step := range updated.Documents[0].Construction.Steps {
		output, found := findStageColumnByID(step.Outputs, "score_id")
		if !found {
			continue
		}
		if output.Name != "score" || output.Type != "integer" || output.Label != "Patient score" {
			t.Fatalf("step %q output identity or label = %#v", step.ID, output)
		}
		if output.Table == nil || output.Table.Visible == nil || *output.Table.Visible ||
			output.Table.Order == nil || *output.Table.Order != order {
			t.Fatalf("step %q output table presentation = %#v", step.ID, output.Table)
		}
		if !reflect.DeepEqual(step.Operation, document.Construction.Steps[index].Operation) {
			t.Fatalf("step %q compiler operation changed during presentation update", step.ID)
		}
	}

	encoded, err := updated.CanonicalJSON()
	if err != nil {
		t.Fatalf("serialize updated construction presentation: %v", err)
	}
	reloaded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("reload updated construction presentation: %v", err)
	}
	output, found := findStageColumnByID(reloaded.Documents[0].Construction.Steps[2].Outputs, "score_id")
	if !found || output.Label != "Patient score" || output.Table == nil || output.Table.Visible == nil || *output.Table.Visible {
		t.Fatalf("reloaded construction output presentation = %#v, found=%v", output, found)
	}
}

func TestSourceColumnsAddedAfterConstructionReceiveStableIDsAndFlowThroughStages(t *testing.T) {
	accepted := constructionWorkspace(documentWithDependentSteps())
	oldIDs := make(map[string]string, len(accepted.Documents[0].Columns))
	for _, column := range accepted.Documents[0].Columns {
		oldIDs[column.Column] = column.ColumnID
	}
	catalog := commandCatalog()
	updated, _, err := ApplyCommands(accepted, catalog, "add-sources-after-step", []Command{
		{Type: CommandAddColumn, OutputID: "patients", OccurrenceID: RootOccurrenceID, CandidateID: "patient-id", Title: "Added catalog ID"},
		{Type: CommandAddColumnSource, OutputID: "patients", OccurrenceID: RootOccurrenceID, Title: "Added source ID", Source: &ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
	})
	if err != nil {
		t.Fatalf("add source columns to staged document: %v", err)
	}
	if len(updated.Documents[0].Columns) != len(oldIDs)+2 {
		t.Fatalf("source column count = %d, want %d", len(updated.Documents[0].Columns), len(oldIDs)+2)
	}
	added := make([]Column, 0, 2)
	for _, column := range updated.Documents[0].Columns {
		if _, existed := oldIDs[column.Column]; existed {
			if oldIDs[column.Column] != column.ColumnID {
				t.Fatalf("existing source ID changed for %q", column.Column)
			}
			continue
		}
		if column.ColumnID == "" {
			t.Fatalf("new staged source %q has no stable ColumnID", column.Column)
		}
		added = append(added, column)
	}
	if len(added) != 2 || added[0].ColumnID == added[1].ColumnID {
		t.Fatalf("new source identities = %#v", added)
	}
	for _, step := range updated.Documents[0].Construction.Steps {
		for _, source := range added {
			stageColumn, found := findStageColumnByID(step.Outputs, source.ColumnID)
			if !found || stageColumn.Name != source.Column || stageColumn.Label != source.Label {
				t.Fatalf("step %q did not carry source %q through its recalculated schema: %#v", step.ID, source.ColumnID, step.Outputs)
			}
		}
	}

	encoded, err := updated.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("reload staged sources: %v", err)
	}
	for _, source := range added {
		found := false
		for _, column := range reloaded.Documents[0].Columns {
			if column.Column == source.Column {
				found = true
				if column.ColumnID != source.ColumnID {
					t.Fatalf("source ColumnID changed on reload: %q != %q", column.ColumnID, source.ColumnID)
				}
			}
		}
		if !found {
			t.Fatalf("source %q disappeared on reload", source.Column)
		}
	}
}

func TestConstructionRejectsNonFiniteTypedNumbers(t *testing.T) {
	if err := (FilterValue{Kind: ConstructionFilterDecimal, Decimal: float64Pointer(math.NaN())}).Validate(); err == nil {
		t.Fatal("NaN filter literal was accepted")
	}
	if err := validateConstructionLiteral(ConstructionLiteral{Kind: ConstructionNumericDecimal, Decimal: float64Pointer(math.Inf(1))}); err == nil {
		t.Fatal("infinite calculation literal was accepted")
	}
}

func TestRelatedSourceAddsColumnAtSelectedStageWithoutRewritingSourceProjection(t *testing.T) {
	document := workspaceDocument("patients")
	document.RootResourceType = "Patient"
	document.Columns = []Column{{
		ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID", LogicalType: "string",
		OccurrenceID: RootOccurrenceID,
		Source:       ColumnSource{Kind: SourceField, Field: &FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}}
	raw := `{"version":1,"steps":[
		{"id":"keep_patients","inputs":[{"kind":"SOURCE_PROJECTION"}],"operation":{"kind":"FILTER","filter":{"columnId":"patient-id","operator":"EXISTS"}},"outputs":[{"id":"patient-id","name":"patient_id","label":"Patient ID","type":"string"}]},
		{"id":"add_observation_status","inputs":[{"kind":"STEP_OUTPUT","stepId":"keep_patients"}],"operation":{"kind":"RELATED_SOURCE","relatedSource":{"anchorColumnId":"_key","choiceId":"choice-token","sourceOccurrenceId":"observation-node","source":{"kind":"FIELD","candidateId":"observation-status","nodeId":"observation-node","resourceType":"Observation","path":"status","cardinality":"optional_one","logicalType":"string"},"route":[{"edgeId":"patient-observation","fromNodeId":"patient-node","toNodeId":"observation-node","fromResourceType":"Patient","toResourceType":"Observation","relationship":"subject_Patient","storageDirection":"INBOUND","matchMode":"OPTIONAL"}],"contributorRule":{"policy":"ALL_MATCHES","predicate":{"candidateId":"observation-status","operator":"EQUALS","value":{"kind":"STRING","string":"registered"}}},"form":"ALL","outputColumnId":"observation-status"}},"outputs":[{"id":"patient-id","name":"patient_id","label":"Patient ID","type":"string"},{"id":"observation-status","name":"observation_status","label":"Observation statuses","type":"string"}]}
	]}`
	var candidate Construction
	if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
		t.Fatalf("decode stage-local related source: %v", err)
	}
	if err := candidate.Validate(document.Columns); err != nil {
		t.Fatalf("validate stage-local related source: %v", err)
	}
	if got := len(candidate.Steps[0].Outputs); got != 1 || candidate.Steps[0].Outputs[0].ID != "patient-id" {
		t.Fatalf("source projection changed to %#v; related output must be stage-local", candidate.Steps[0].Outputs)
	}
	if got := candidate.Steps[1].Inputs; len(got) != 1 || got[0].Kind != ConstructionInputStepOutput || got[0].StepID != "keep_patients" {
		t.Fatalf("related source input = %#v, want exact preceding stage output", got)
	}
	if got := candidate.Steps[1].Operation.RelatedSource.OutputColumnID; got != "observation-status" {
		t.Fatalf("related source stable output ID = %q", got)
	}
	predicate := candidate.Steps[1].Operation.RelatedSource.ContributorRule.Predicate
	if predicate == nil || predicate.CandidateID != "observation-status" || predicate.Operator != ContributorEquals || predicate.Value == nil || predicate.Value.Kind != ContributorString || predicate.Value.String == nil || *predicate.Value.String != "registered" {
		t.Fatalf("related source scalar contributor predicate did not round trip: %#v", predicate)
	}
	if len(document.Columns) != 1 || document.Columns[0].ColumnID != "patient-id" {
		t.Fatalf("related source mutated initial projection columns: %#v", document.Columns)
	}
}

func float64Pointer(value float64) *float64 { return &value }

func TestOldV2DocumentSerializationOmitsConstructionFields(t *testing.T) {
	document := workspaceDocument("patients")
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "construction") || strings.Contains(string(raw), "columnId") {
		t.Fatalf("old V2 document acquired staged fields without migration: %s", raw)
	}
}
