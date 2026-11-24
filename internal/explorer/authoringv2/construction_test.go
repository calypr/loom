package authoringv2

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
)

func stagedConstructionDocument() ws.Document {
	document := workspaceDocument("patients")
	document.Columns = []ws.Column{
		constructionSourceColumn("person_id", "person_id", "Person ID", "integer"),
		constructionSourceColumn("group_id", "group_id", "Group", "string"),
		constructionSourceColumn("category_id", "category", "Category", "string"),
		constructionSourceColumn("value_id", "value", "Value", "integer"),
	}
	group := ws.StageColumn{ID: "group_id", Name: "group_id", Label: "Group", Type: "string"}
	categoryA := ws.StageColumn{ID: "pivot_a", Name: "amount_a", Label: "Amount A", Type: "integer"}
	categoryB := ws.StageColumn{ID: "pivot_b", Name: "amount_b", Label: "Amount B", Type: "integer"}
	pivotOutputs := []ws.StageColumn{group, categoryA, categoryB}
	quotient := ws.StageColumn{ID: "ratio_id", Name: "ratio", Label: "Ratio", Type: "decimal"}
	derivedOutputs := append(append([]ws.StageColumn(nil), pivotOutputs...), quotient)
	filterValue := int64(1)
	filterOutputs := append([]ws.StageColumn(nil), derivedOutputs...)
	finalOutputs := []ws.StageColumn{
		group,
		quotient,
		{ID: "measure_key", Name: "measure", Label: "Measure"},
		{ID: "measure_value", Name: "measure_value", Label: "Measure value", Type: "integer"},
	}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID:     "pivot_step",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
				ConstructionID: "pivot_step", GroupKeyIDs: []string{"group_id"}, CategoryColumnID: "category_id", ValueColumnID: "value_id",
				Categories: []ws.ConstructionPivotCategory{
					{Key: stringScalar("A"), OutputColumnID: "pivot_a"},
					{Key: stringScalar("B"), OutputColumnID: "pivot_b"},
				},
				DuplicatePolicy: ws.ConstructionPivotDuplicateSum, MissingCellPolicy: ws.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
			}},
			Outputs: pivotOutputs,
		},
		{
			ID: "derive_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "pivot_step"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
				ConstructionID: "derive_step", OutputColumnID: "ratio_id", Operation: ws.ConstructionDerivedDivide,
				Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "pivot_a"},
				Right:              ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "pivot_b"},
				MissingInputPolicy: ws.ConstructionMissingInputPropagateNull, DivisionByZeroPolicy: ws.ConstructionDivisionByZeroNull,
			}}, Outputs: derivedOutputs,
		},
		{
			ID: "filter_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive_step"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "ratio_id", Operator: ws.ConstructionFilterGreaterEq,
				Values: []ws.FilterValue{{Kind: ws.ConstructionFilterInteger, Integer: &filterValue}},
			}}, Outputs: filterOutputs,
		},
		{
			ID: "unpivot_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "filter_step"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
				ConstructionID: "unpivot_step",
				Inputs: []ws.ConstructionUnpivotInput{
					{ColumnID: "pivot_a", Key: stringScalar("A")},
					{ColumnID: "pivot_b", Key: stringScalar("B")},
				},
				KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: ws.ConstructionUnpivotPreserve,
			}}, Outputs: finalOutputs,
		},
	}}
	return document
}

func constructionSourceColumn(id, name, label, logicalType string) ws.Column {
	return ws.Column{
		ColumnID: id, Column: name, Label: label, LogicalType: logicalType, OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceProjectID},
	}
}

func stringScalar(value string) ws.TableScalar {
	return ws.TableScalar{Kind: ws.TableScalarString, String: &value}
}

func constructionWorkspace(document ws.Document) ws.Workspace {
	return ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer:  ws.ExplorerMetadata{Title: "ws.Construction test"},
		Documents: []ws.Document{document},
		Tabs:      []ws.Tab{{ID: "tab-patients", Title: "Patients", OutputID: document.Output.ID, Order: 0, Visible: true}},
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
	decoded, err := ws.DecodeWorkspace(encoded)
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
	projection := ws.ConstructionSourceProjection{
		ColumnID: "source_active", OccurrenceID: ws.RootOccurrenceID, FieldPath: "active",
		FHIRType: "boolean", LogicalType: "boolean", Label: "Whether this record is in active use",
	}
	candidate := ws.Construction{
		Version: ws.ConstructionVersion, SourceProjections: []ws.ConstructionSourceProjection{projection},
		Steps: []ws.ConstructionStep{{
			ID: "group_by_active", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group_by_active", Keys: []ws.ConstructionGroupKey{{InputColumnID: projection.ColumnID, OutputColumnID: "active_key"}},
				Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "record_count"}},
			}},
			Outputs: []ws.StageColumn{
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
	if err := grouped.Validate(); err != nil {
		t.Fatalf("validate scalar source GROUP: %v", err)
	}
	if len(grouped.Construction.Steps[0].Outputs) != 2 || grouped.Construction.Steps[0].Outputs[0].ID != "active_key" {
		t.Fatalf("source projection was not incorporated into the group stage: %#v", grouped.Construction.Steps[0].Outputs)
	}
	encoded, err := json.Marshal(grouped)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded ws.Document
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
	document.Columns = []ws.Column{
		constructionSourceColumn("patient_id", "patient_id", "Patient ID", "string"),
		constructionSourceColumn("amount", "amount", "Amount", "integer"),
	}
	projection := ws.ConstructionSourceProjection{
		ColumnID: "pivot_status", OwnerStepID: "pivot_step", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Patient.active",
	}
	sourceColumns := []ws.StageColumn{
		{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
		{ID: "amount", Name: "amount", Label: "Amount", Type: "integer"},
		{ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID), Label: projection.Label, Type: projection.LogicalType},
	}
	relatedSource := ws.ConstructionRelatedFieldSource{
		Kind: capability.ConstructionChoiceSourceField, CandidateID: "observation-status", NodeID: "observation-node",
		ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
	}
	relatedOutputs := append(append([]ws.StageColumn(nil), sourceColumns...), ws.StageColumn{
		ID: "pivot_observation_status", Name: "__pivot_source_status", Label: "Observation.status", Type: "string", Nullable: true,
	})
	active := true
	candidate := ws.Construction{
		Version:           ws.ConstructionVersion,
		SourceProjections: []ws.ConstructionSourceProjection{projection},
		Steps: []ws.ConstructionStep{
			{
				ID: "pivot_status_input", OwnerStepID: "pivot_step",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedField, RelatedField: &ws.ConstructionRelatedField{
					ChoiceID: "stage-bound-choice", Source: relatedSource, OutputColumnID: "pivot_observation_status",
				}},
				Outputs: relatedOutputs,
			},
			{
				ID: "pivot_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "pivot_status_input"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
					ConstructionID: "pivot_step", GroupKeyIDs: []string{"patient_id"},
					CategoryColumnID: projection.ColumnID, ValueColumnID: "pivot_observation_status",
					Categories:      []ws.ConstructionPivotCategory{{Key: ws.TableScalar{Kind: ws.TableScalarBoolean, Boolean: &active}, OutputColumnID: "active_amount"}},
					DuplicatePolicy: ws.ConstructionPivotDuplicateError, MissingCellPolicy: ws.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
				}},
				Outputs: []ws.StageColumn{
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
		withPrefix := cloneDocumentFixtureByJSON(accepted)
		prefix := ws.ConstructionStep{
			ID: "keep_patient", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "patient_id", Operator: ws.ConstructionFilterExists}},
			Outputs:   append([]ws.StageColumn(nil), sourceColumns...),
		}
		withPrefix.Construction.Steps[0].Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: prefix.ID}}
		withPrefix.Construction.Steps = append([]ws.ConstructionStep{prefix}, withPrefix.Construction.Steps...)
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
	document.Columns = []ws.Column{
		constructionSourceColumn("patient_id", "patient_id", "Patient ID", "string"),
		constructionSourceColumn("amount", "amount", "Amount", "integer"),
		constructionSourceColumn("category", "category", "Category", "string"),
	}
	projection := ws.ConstructionSourceProjection{
		ColumnID: "pivot_status", OwnerStepID: "pivot_step", OccurrenceID: ws.RootOccurrenceID,
		FieldPath: "active", FHIRType: "boolean", LogicalType: "boolean", Label: "Patient.active",
	}
	active := true
	filterOutputs := []ws.StageColumn{
		{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
		{ID: "amount", Name: "amount", Label: "Amount", Type: "integer"},
		{ID: "category", Name: "category", Label: "Category", Type: "string"},
		{ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID), Label: projection.Label, Type: projection.LogicalType},
	}
	candidate := ws.Construction{
		Version: ws.ConstructionVersion, SourceProjections: []ws.ConstructionSourceProjection{projection},
		Steps: []ws.ConstructionStep{
			{
				ID: "filter_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
					ColumnID: "patient_id", Operator: ws.ConstructionFilterExists,
				}}, Outputs: filterOutputs,
			},
			{
				ID: "pivot_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "filter_step"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
					ConstructionID: "pivot_step", GroupKeyIDs: []string{"patient_id"},
					CategoryColumnID: projection.ColumnID, ValueColumnID: "amount",
					Categories:      []ws.ConstructionPivotCategory{{Key: ws.TableScalar{Kind: ws.TableScalarBoolean, Boolean: &active}, OutputColumnID: "active_amount"}},
					DuplicatePolicy: ws.ConstructionPivotDuplicateError, MissingCellPolicy: ws.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
				}},
				Outputs: []ws.StageColumn{
					{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: "active_amount", Name: "active_amount", Label: "Active amount", Type: "integer"},
				},
			},
		},
	}
	acceptedFilter := candidate.Steps[0]
	acceptedFilter.Outputs = acceptedFilter.Outputs[:3]
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{acceptedFilter}}
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
	updated.Steps[1].Operation.Pivot.Categories = []ws.ConstructionPivotCategory{{Key: stringScalar("final"), OutputColumnID: "final_amount"}}
	updated.Steps[1].Outputs = []ws.StageColumn{
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
	document.Rows = ws.RecordsRowDefinition()
	document.Columns = []ws.Column{}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion}
	workspace := constructionWorkspace(document)

	encoded, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	if !strings.Contains(string(encoded), `"steps":[]`) {
		t.Fatalf("empty construction steps were not persisted as an array: %s", encoded)
	}

	reloaded, err := ws.DecodeWorkspace(encoded)
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
	upgraded, err := migrateStoredDocumentFixture(fresh)
	if err != nil {
		t.Fatalf("upgrade fresh document: %v", err)
	}
	if upgraded.Construction == nil || upgraded.Construction.Version != ws.ConstructionVersion || len(upgraded.Construction.Steps) != 0 {
		t.Fatalf("fresh construction = %#v", upgraded.Construction)
	}
	if upgraded.Columns[0].ColumnID == "" {
		t.Fatal("fresh source projection has no persisted stable column ID")
	}
	if err := upgraded.Validate(); err != nil {
		t.Fatalf("source-only construction is invalid: %v", err)
	}

	sourceColumn := upgraded.Columns[0]
	sourceStage := ws.StageColumn{ID: sourceColumn.ColumnID, Name: sourceColumn.Column, Label: sourceColumn.Label, Type: sourceColumn.LogicalType}
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "first_filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: sourceColumn.ColumnID, Operator: ws.ConstructionFilterExists,
		}},
		Outputs: []ws.StageColumn{sourceStage},
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
	document.ArchiveRetiredShape = &storedTableShape{Derived: []storedDerivedConstruction{{
		ConstructionID: "legacy-derived", Output: ws.ColumnOutput{Column: "old_result", Label: "Old result"}, Operation: "ADD",
		Left:               storedArithmeticOperand{Kind: "COLUMN", Column: "value"},
		Right:              storedArithmeticOperand{Kind: "LITERAL", Literal: &ws.TableScalar{Kind: ws.TableScalarInteger, Integer: int64Pointer(1)}},
		MissingInputPolicy: "PROPAGATE_NULL",
	}}}
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "stored-data migration") {
		t.Fatalf("mixed construction modes error = %v", err)
	}
	if err := document.ValidateAllowArchive(); err == nil || !strings.Contains(err.Error(), "cannot both define") {
		t.Fatalf("offline mixed modes error = %v", err)
	}

	document.ArchiveRetiredShape = nil
	document.Construction.Steps[2].Operation.Filter.ColumnID = "missing_column"
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "missing column id") {
		t.Fatalf("missing filter column error = %v", err)
	}
}

func TestUpgradeDocumentToConstructionMigratesAndReloadsLegacyShape(t *testing.T) {
	legacy := legacyPivotDocument()
	if err := legacy.ValidateAllowArchive(); err != nil {
		t.Fatalf("legacy document is invalid: %v", err)
	}
	original := legacy
	original.Columns = append([]ws.Column(nil), legacy.Columns...)
	original.ArchiveRetiredShape, _ = cloneStoredShapeFixture(legacy.ArchiveRetiredShape)

	upgraded, err := migrateStoredDocumentFixture(legacy)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if upgraded.ArchiveRetiredShape != nil || upgraded.Construction == nil {
		t.Fatalf("upgrade did not establish one staged plan: %#v", upgraded)
	}
	if len(upgraded.Construction.Steps) != 3 {
		t.Fatalf("migrated steps = %d, want pivot plus two derived steps", len(upgraded.Construction.Steps))
	}
	if upgraded.Construction.Steps[0].Operation.Kind != ws.ConstructionOperationPivot {
		t.Fatalf("first migrated operation = %q, want PIVOT", upgraded.Construction.Steps[0].Operation.Kind)
	}
	if got := upgraded.Construction.Steps[1].Operation.Derive.OutputColumnID; got == "" {
		t.Fatal("migrated derived output has no stable identity")
	}
	if upgraded.Construction.Steps[1].Operation.Derive.OutputColumnID == upgraded.Construction.Steps[2].Operation.Derive.OutputColumnID {
		t.Fatal("migrated derived columns share one stable identity")
	}
	if got := upgraded.Construction.Steps[0].Operation.Pivot.DuplicatePolicy; got != ws.ConstructionPivotDuplicateMax {
		t.Fatalf("pivot duplicate policy = %q", got)
	}
	if got := upgraded.Construction.Steps[0].Operation.Pivot.UnlistedCategoryPolicy; got != ws.ConstructionPivotUnlistedError {
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
	decoded, err := ws.DecodeWorkspace(encoded)
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
	groupOutput, ok := stageColumnByID(normalized.Documents[0].Construction.Steps[0].Outputs, beforeRename)
	if !ok || groupOutput.Name != "group_renamed" || groupOutput.Label != "Renamed group" {
		t.Fatalf("renamed output schema = %#v, found=%t", groupOutput, ok)
	}

	again, err := migrateStoredDocumentFixture(reloaded)
	if err != nil {
		t.Fatalf("reapplying conversion to staged document: %v", err)
	}
	if !reflect.DeepEqual(again.Construction, reloaded.Construction) {
		t.Fatal("reopening and editing a staged document changed its operation identities")
	}
}

func TestConstructionRejectsExcludeWithEvidencePivotBeforeCompilation(t *testing.T) {
	document := stagedConstructionDocument()
	document.Construction.Steps[0].Operation.Pivot.UnlistedCategoryPolicy = ws.ConstructionPivotUnlistedExcludeWithEvidence
	if err := document.Validate(); err == nil || !strings.Contains(err.Error(), "EXCLUDE_WITH_EVIDENCE") {
		t.Fatalf("document validation accepted an unsupported construction pivot policy: %v", err)
	}
}

func TestUpgradeRejectsExcludeWithEvidenceTableShapeBeforeCompilation(t *testing.T) {
	legacy := legacyPivotDocument()
	legacy.ArchiveRetiredShape.Reshape.Pivot.UnlistedCategoryPolicy = "EXCLUDE_WITH_EVIDENCE"
	before := legacy
	before.Columns = append([]ws.Column(nil), legacy.Columns...)
	before.ArchiveRetiredShape, _ = cloneStoredShapeFixture(legacy.ArchiveRetiredShape)
	if err := legacy.ValidateAllowArchive(); err == nil || !strings.Contains(err.Error(), "EXCLUDE_WITH_EVIDENCE") {
		t.Fatalf("document validation accepted an unupgradable tableShape pivot policy: %v", err)
	}
	if _, err := migrateStoredDocumentFixture(legacy); err == nil || !strings.Contains(err.Error(), "EXCLUDE_WITH_EVIDENCE") {
		t.Fatalf("upgrade accepted an unupgradable tableShape pivot policy: %v", err)
	}
	if !reflect.DeepEqual(legacy, before) || legacy.ArchiveRetiredShape == nil || legacy.Construction != nil {
		t.Fatal("failed upgrade changed the accepted legacy document")
	}
}

func TestUpgradeDocumentToConstructionMigratesUnpivotPolicies(t *testing.T) {
	legacy := stagedConstructionDocument()
	for i := range legacy.Columns {
		legacy.Columns[i].ColumnID = ""
	}
	legacy.Construction = nil
	legacy.ArchiveRetiredShape = &storedTableShape{Reshape: &storedTableReshape{Kind: "UNPIVOT", Unpivot: &storedUnpivotConstruction{
		ConstructionID: "legacy_unpivot",
		Inputs: []storedUnpivotInput{
			{Column: "group_id", Key: stringScalar("one")},
			{Column: "category", Key: stringScalar("two")},
		},
		KeyOutput:     ws.ColumnOutput{Column: "metric", Label: "Metric"},
		ValueOutput:   ws.ColumnOutput{Column: "metric_value", Label: "Metric value"},
		NullRowPolicy: "PRESERVE",
	}}}
	upgraded, err := migrateStoredDocumentFixture(legacy)
	if err != nil {
		t.Fatalf("upgrade unpivot: %v", err)
	}
	if len(upgraded.Construction.Steps) != 1 {
		t.Fatalf("migrated step count = %d", len(upgraded.Construction.Steps))
	}
	unpivot := upgraded.Construction.Steps[0].Operation.Unpivot
	if unpivot == nil || unpivot.NullRowPolicy != ws.ConstructionUnpivotPreserve || len(unpivot.Inputs) != 2 {
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
	legacy.ArchiveRetiredShape.Derived[0].Left = storedArithmeticOperand{Kind: "LITERAL", Literal: &ws.TableScalar{Kind: ws.TableScalarString, String: stringPointer("not numeric")}}
	before := legacy
	before.Columns = append([]ws.Column(nil), legacy.Columns...)
	before.ArchiveRetiredShape, _ = cloneStoredShapeFixture(legacy.ArchiveRetiredShape)
	if _, err := migrateStoredDocumentFixture(legacy); err == nil || !strings.Contains(err.Error(), "not numeric") {
		t.Fatalf("lossy conversion error = %v", err)
	}
	if !reflect.DeepEqual(legacy, before) || legacy.ArchiveRetiredShape == nil || legacy.Construction != nil {
		t.Fatal("failed conversion changed the accepted legacy document")
	}
}

func TestAnalyzeStepEditReportsMissingDownstreamColumnAndKeepsAcceptedDocument(t *testing.T) {
	accepted := documentWithDependentSteps()
	var replacement ws.ConstructionStep
	raw, err := json.Marshal(accepted.Construction.Steps[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &replacement); err != nil {
		t.Fatal(err)
	}
	replacement.Operation.Derive.OutputColumnID = "score_v2_id"
	replacement.Outputs = append([]ws.StageColumn(nil), replacement.Outputs[:2]...)
	replacement.Outputs = append(replacement.Outputs, ws.StageColumn{ID: "score_v2_id", Name: "score_v2", Label: "Score v2", Type: "integer"})
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
	lastOutputs := append([]ws.StageColumn(nil), construction.Steps[len(construction.Steps)-1].Outputs...)
	construction.Steps = append(construction.Steps, ws.ConstructionStep{
		ID: "last_filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "unpivot_step"}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: "measure_value", Operator: ws.ConstructionFilterGreaterEq,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterInteger, Integer: &filterValue}},
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
	if got := candidate.Construction.Steps[4].Inputs; !reflect.DeepEqual(got, []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "unpivot_step"}}) {
		t.Fatalf("appended input refs = %#v", got)
	}
	if err := candidate.Validate(); err != nil {
		t.Fatalf("appended candidate invalid: %v", err)
	}
	if len(accepted.Construction.Steps) != 4 {
		t.Fatal("append mutated accepted document")
	}
}

func TestAnalyzeConstructionCandidateRecalculatesChainedSameRouteRelatedSources(t *testing.T) {
	document := workspaceDocument("out_a27d2e89ad150ba83cfa4507")
	patientColumn := constructionSourceColumn(
		"source_c3a8832f4fca3ef4fc094bb0", "col_649f8c7bba4d5b652204c73f", "Patient ID", "string",
	)
	patientColumn.Source = ws.ColumnSource{
		Kind:  ws.SourceField,
		Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"},
	}
	document.Columns = []ws.Column{patientColumn}

	route := []capability.ConstructionRouteStep{{
		EdgeID: "e_e225a17091bb09974fb4afe2", FromNodeID: "n_7855547e43c259d43ab787c6",
		ToNodeID: "n_e8ff24d9e0b8c8f91ea140a6", FromResourceType: "Patient", ToResourceType: "Observation",
		Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
	}}
	makeRelatedSource := func(
		stepID, choiceID, candidateID, fieldPath, logicalType, outputID, outputName, outputLabel string,
		input ws.ConstructionInputRef,
		outputs []ws.StageColumn,
	) ws.ConstructionStep {
		return ws.ConstructionStep{
			ID: stepID, Inputs: []ws.ConstructionInputRef{input},
			Operation: ws.ConstructionOperation{
				Kind: ws.ConstructionOperationRelatedSource,
				RelatedSource: &ws.ConstructionRelatedSource{
					AnchorColumnID: "_key", ChoiceID: choiceID, SourceOccurrenceID: "n_e8ff24d9e0b8c8f91ea140a6",
					Source: ws.ConstructionRelatedFieldSource{
						Kind: capability.ConstructionChoiceSourceField, CandidateID: candidateID,
						NodeID: "n_e8ff24d9e0b8c8f91ea140a6", ResourceType: "Observation", Path: fieldPath,
						Cardinality: "optional_one", LogicalType: logicalType,
					},
					Route:           append([]capability.ConstructionRouteStep(nil), route...),
					ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
					Form:            capability.ConstructionChoiceAll, OutputColumnID: outputID,
				},
			},
			Outputs: append(append([]ws.StageColumn(nil), outputs...), ws.StageColumn{
				ID: outputID, Name: outputName, Label: outputLabel, Type: logicalType,
			}),
		}
	}
	rootOutput := ws.StageColumn{ID: patientColumn.ColumnID, Name: patientColumn.Column, Label: patientColumn.Label, Type: patientColumn.LogicalType}
	first := makeRelatedSource(
		"step-204eaeb8-5b66-45d2-9bae-8a2de6b47a07", "choice-observation-id", "c_31b05f0dcbcf99c2c17e6d95",
		"id", "string", "related-e026caab-7839-460f-a872-acc4504c418b", "related_Observation_id", "Observation IDs",
		ws.ConstructionInputRef{Kind: ws.ConstructionInputSourceProjection}, []ws.StageColumn{rootOutput},
	)
	second := makeRelatedSource(
		"step-b1b10346-9c35-43ef-9861-afdd3ba6ad4f", "choice-observation-value", "c_b84351cf8639d336ef2ff4a5",
		"valueQuantity.value", "decimal", "related-b9c96965-3b12-4037-bdd5-39fe090bf091",
		"related_Observation_valueQuantity_value", "Observation valueQuantity.value",
		ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: first.ID},
		[]ws.StageColumn{rootOutput, first.Outputs[1]},
	)
	candidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{first, second}}

	updated, impact, err := document.AnalyzeConstructionCandidate(candidate, second.ID, nil)
	if err != nil {
		t.Fatalf("analyze retained two-field related-source proposal: %v", err)
	}
	if !reflect.DeepEqual(impact.AffectedStepIDs, []string{first.ID, second.ID}) {
		t.Fatalf("affected steps = %#v, want the complete appended suffix in order", impact.AffectedStepIDs)
	}
	if len(updated.Construction.Steps) != 2 {
		t.Fatalf("updated construction has %d steps, want 2", len(updated.Construction.Steps))
	}
	if got, want := constructionStageIDs(updated.Construction.Steps[0].Outputs), []string{patientColumn.ColumnID, first.Operation.RelatedSource.OutputColumnID}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first appended stage columns = %#v, want %#v", got, want)
	}
	if got, want := constructionStageIDs(updated.Construction.Steps[1].Outputs), []string{
		patientColumn.ColumnID, first.Operation.RelatedSource.OutputColumnID, second.Operation.RelatedSource.OutputColumnID,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second appended stage columns = %#v, want %#v", got, want)
	}
	if updated.Construction.Steps[1].Inputs[0] != (ws.ConstructionInputRef{Kind: ws.ConstructionInputStepOutput, StepID: first.ID}) {
		t.Fatalf("second related source is not chained from the first: %#v", updated.Construction.Steps[1].Inputs)
	}
	if document.Construction != nil {
		t.Fatal("analyzing the appended suffix mutated the accepted source-only document")
	}
	if err := updated.Validate(); err != nil {
		t.Fatalf("recalculated two-field candidate is invalid: %v", err)
	}
	t.Run("appends after unchanged existing filter", func(t *testing.T) {
		filteredDocument := document
		filter := ws.ConstructionStep{
			ID:     "existing_patient_filter",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: patientColumn.ColumnID, Operator: ws.ConstructionFilterExists,
			}},
			Outputs: []ws.StageColumn{rootOutput},
		}
		filteredDocument.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{filter}}
		filteredFirst, filteredSecond := first, second
		filteredFirst.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: filter.ID}}
		filteredSecond.Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: filteredFirst.ID}}
		filteredCandidate := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{filter, filteredFirst, filteredSecond}}

		result, impact, err := filteredDocument.AnalyzeConstructionCandidate(filteredCandidate, filteredSecond.ID, nil)
		if err != nil {
			t.Fatalf("analyze two related fields after existing filter: %v", err)
		}
		if !reflect.DeepEqual(impact.AffectedStepIDs, []string{filteredFirst.ID, filteredSecond.ID}) {
			t.Fatalf("affected steps = %#v, want only the appended suffix", impact.AffectedStepIDs)
		}
		if !reflect.DeepEqual(result.Construction.Steps[0], filter) {
			t.Fatalf("existing prefix changed: %#v", result.Construction.Steps[0])
		}
		if err := result.Validate(); err != nil {
			t.Fatalf("candidate with existing prefix is invalid: %v", err)
		}

		changedPrefix, err := cloneConstruction(&filteredCandidate)
		if err != nil {
			t.Fatal(err)
		}
		changedPrefix.Steps[0].Operation.Filter.Operator = ws.ConstructionFilterMissing
		if _, _, err := filteredDocument.AnalyzeConstructionCandidate(*changedPrefix, filteredSecond.ID, nil); err == nil {
			t.Fatal("appended related-source candidate changed its existing filter prefix")
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*ws.Construction)
	}{
		{
			name: "different route",
			mutate: func(value *ws.Construction) {
				value.Steps[1].Operation.RelatedSource.Route[0].EdgeID = "other-edge"
			},
		},
		{
			name: "broken step chain",
			mutate: func(value *ws.Construction) {
				value.Steps[1].Inputs = []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}
			},
		},
		{
			name: "unrequested operation in suffix",
			mutate: func(value *ws.Construction) {
				interleaved := ws.ConstructionStep{ID: "unrequested-step", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter}}
				value.Steps = append(value.Steps[:1], append([]ws.ConstructionStep{interleaved}, value.Steps[1:]...)...)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid, err := cloneConstruction(&candidate)
			if err != nil {
				t.Fatal(err)
			}
			test.mutate(invalid)
			if _, _, err := document.AnalyzeConstructionCandidate(*invalid, second.ID, nil); err == nil {
				t.Fatal("invalid related-source suffix was accepted")
			}
		})
	}
	if _, _, err := document.AnalyzeConstructionCandidate(candidate, first.ID, nil); err == nil {
		t.Fatal("changedStepID naming the first step of a multi-step suffix was accepted")
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
	changed.Steps[0].Outputs[1] = ws.StageColumn{ID: "pivot_a_replaced", Name: "amount_a_new", Label: "Amount A new", Type: "integer"}
	invalid, missing, err := accepted.AnalyzeConstructionCandidate(*changed, "pivot_step", nil)
	if err != nil {
		t.Fatalf("edit with dependent issue: %v", err)
	}
	wantMissing := []ws.ConstructionDependencyIssue{
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
	if repaired.Construction.Steps[0].Inputs[0].Kind != ws.ConstructionInputSourceProjection {
		t.Fatalf("retained step input was not rewired to source: %#v", repaired.Construction.Steps[0].Inputs)
	}
}

func TestConstructionInputRequiresPinnedTableRevision(t *testing.T) {
	valid := ws.ConstructionInputRef{Kind: ws.ConstructionInputTableRevision, TableID: "source_table", RevisionID: "rev_7", OutputID: "result"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("pinned table input rejected: %v", err)
	}
	if err := (ws.ConstructionInputRef{Kind: ws.ConstructionInputTableRevision, TableID: "source_table", OutputID: "result"}).Validate(); err == nil || !strings.Contains(err.Error(), "revisionId") {
		t.Fatalf("floating table input error = %v", err)
	}
}

func legacyPivotDocument() ws.Document {
	document := stagedConstructionDocument()
	for i := range document.Columns {
		document.Columns[i].ColumnID = ""
	}
	document.Construction = nil
	document.ArchiveRetiredShape = &storedTableShape{
		Reshape: &storedTableReshape{Kind: "PIVOT", Pivot: &storedPivotConstruction{
			ConstructionID: "legacy_pivot", GroupKeys: []string{"group_id"}, CategoryColumn: "category", ValueColumn: "value",
			Categories: []storedPivotCategory{
				{Key: stringScalar("A"), Output: ws.ColumnOutput{Column: "amount_a", Label: "Amount A"}},
				{Key: stringScalar("B"), Output: ws.ColumnOutput{Column: "amount_b", Label: "Amount B"}},
			},
			DuplicatePolicy: "MAX", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
		}},
		Derived: []storedDerivedConstruction{
			{
				ConstructionID: "legacy_ratio", Output: ws.ColumnOutput{Column: "ratio", Label: "Ratio"}, Operation: "ADD",
				Left: storedArithmeticOperand{Kind: "COLUMN", Column: "twice_a"}, Right: storedArithmeticOperand{Kind: "COLUMN", Column: "amount_b"},
				MissingInputPolicy: "PROPAGATE_NULL",
			},
			{
				ConstructionID: "legacy_twice", Output: ws.ColumnOutput{Column: "twice_a", Label: "Twice A"}, Operation: "MULTIPLY",
				Left:               storedArithmeticOperand{Kind: "COLUMN", Column: "amount_a"},
				Right:              storedArithmeticOperand{Kind: "LITERAL", Literal: &ws.TableScalar{Kind: ws.TableScalarInteger, Integer: int64Pointer(2)}},
				MissingInputPolicy: "PROPAGATE_NULL",
			},
		},
	}
	return document
}

func documentWithDependentSteps() ws.Document {
	document := workspaceDocument("patients")
	document.Columns = []ws.Column{
		constructionSourceColumn("person_id", "person_id", "Person ID", "integer"),
		constructionSourceColumn("age_id", "age", "Age", "integer"),
	}
	person := ws.StageColumn{ID: "person_id", Name: "person_id", Label: "Person ID", Type: "integer"}
	age := ws.StageColumn{ID: "age_id", Name: "age", Label: "Age", Type: "integer"}
	base := []ws.StageColumn{person, age}
	score := ws.StageColumn{ID: "score_id", Name: "score", Label: "Score", Type: "integer"}
	scoreStage := append(append([]ws.StageColumn(nil), base...), score)
	threshold := int64(18)
	bonus := ws.StageColumn{ID: "bonus_id", Name: "bonus", Label: "Bonus", Type: "integer"}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{
			ID: "score_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
				ConstructionID: "score_step", OutputColumnID: "score_id", Operation: ws.ConstructionDerivedAdd,
				Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "age_id"},
				Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: int64Pointer(1)}},
				MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
			}}, Outputs: scoreStage,
		},
		{
			ID: "score_filter", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "score_step"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
				ColumnID: "score_id", Operator: ws.ConstructionFilterGreaterEq,
				Values: []ws.FilterValue{{Kind: ws.ConstructionFilterInteger, Integer: &threshold}},
			}}, Outputs: append([]ws.StageColumn(nil), scoreStage...),
		},
		{
			ID: "bonus_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "score_filter"}},
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
				ConstructionID: "bonus_step", OutputColumnID: "bonus_id", Operation: ws.ConstructionDerivedAdd,
				Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "age_id"},
				Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: int64Pointer(2)}},
				MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
			}}, Outputs: append(append([]ws.StageColumn(nil), scoreStage...), bonus),
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
	updated, _, err := ApplyCommands(workspace, commandCatalog(), "rename-derived-output", []Command{{Type: CommandUpdateConstructionOutput, OutputID: "patients", Payload: &ColumnPayload{ConstructionOutput: &ConstructionOutputPresentation{
		StepID:   "bonus_step",
		ColumnID: "score_id",
		Label:    "Patient score",
		Table:    &ws.TablePresentation{Visible: &visible, Order: &order},
	}}}})
	if err != nil {
		t.Fatalf("update construction output presentation: %v", err)
	}

	for index, step := range updated.Documents[0].Construction.Steps {
		output, found := stageColumnByID(step.Outputs, "score_id")
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
	reloaded, err := ws.DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("reload updated construction presentation: %v", err)
	}
	output, found := stageColumnByID(reloaded.Documents[0].Construction.Steps[2].Outputs, "score_id")
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
		{Type: CommandAddColumn, OutputID: "patients", Title: "Added catalog ID", Payload: &ColumnPayload{OccurrenceID: ws.RootOccurrenceID, CandidateID: "patient-id"}},
		{Type: CommandAddColumnSource, OutputID: "patients", Title: "Added source ID", Payload: &ColumnPayload{OccurrenceID: ws.RootOccurrenceID, Source: &ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}},
	})
	if err != nil {
		t.Fatalf("add source columns to staged document: %v", err)
	}
	if len(updated.Documents[0].Columns) != len(oldIDs)+2 {
		t.Fatalf("source column count = %d, want %d", len(updated.Documents[0].Columns), len(oldIDs)+2)
	}
	added := make([]ws.Column, 0, 2)
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
			stageColumn, found := stageColumnByID(step.Outputs, source.ColumnID)
			if !found || stageColumn.Name != source.Column || stageColumn.Label != source.Label {
				t.Fatalf("step %q did not carry source %q through its recalculated schema: %#v", step.ID, source.ColumnID, step.Outputs)
			}
		}
	}

	encoded, err := updated.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ws.DecodeWorkspace(encoded)
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

func TestAddRootScalarSourceAfterRelatedExpandPreservesStageAndSupportsGrouping(t *testing.T) {
	catalog := testCatalog()
	catalog.Nodes = []ws.CatalogNode{{ID: "observation", ResourceType: "Observation", RowRootEligible: true}}
	catalog.Edges = nil
	catalog.Candidates = []ws.CatalogCandidate{{
		ID: "observation-status", NodeID: "observation", FieldPath: "status",
		Cardinality: "optional_one", Label: "Status", LogicalType: "string", ProjectionModes: []string{"VALUE"},
		DefaultProjectionMode: "VALUE",
		ConstructionChoice:    capabilityfixtures.FieldConstructionChoice(catalog.SnapshotToken, "observation-status", "observation", "Observation", "status", "optional_one", capability.ProjectionScalar),
	}}
	document := workspaceDocument("observations")
	document.RootResourceType = "Observation"
	document.Route.ResourceType = "Observation"
	document.Columns = []ws.Column{{
		ColumnID: "observation-id", Column: "observation_id", Label: "Observation ID", LogicalType: "string",
		OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}}
	rootID := ws.StageColumn{ID: "observation-id", Name: "observation_id", Label: "Observation ID", Type: "string"}
	relatedID := ws.StageColumn{ID: "related-medadmin-id", Name: "related_medicationadministration_id", Label: "MedicationAdministration ID", Type: "string", Nullable: true}
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "expand_medicationadministrations", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand, RelatedExpand: &ws.ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: "related-route-choice", TargetNodeID: "medicationadministration",
			TargetResourceType: "MedicationAdministration", Route: []capability.ConstructionRouteStep{
				{EdgeID: "observation-patient", FromNodeID: "observation", ToNodeID: "patient", FromResourceType: "Observation", ToResourceType: "Patient", Relationship: "subject_Patient", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL"},
				{EdgeID: "patient-medicationadministration", FromNodeID: "patient", ToNodeID: "medicationadministration", FromResourceType: "Patient", ToResourceType: "MedicationAdministration", Relationship: "subject_Patient", StorageDirection: "INBOUND", MatchMode: "OPTIONAL"},
			},
			ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
			EmptyPolicy:     ws.ConstructionExpandEmptyPreserveParent, RelatedRecordColumnID: relatedID.ID,
		}}, Outputs: []ws.StageColumn{rootID, relatedID},
	}}}
	workspace := constructionWorkspace(document)
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate RELATED_EXPAND source workspace: %v", err)
	}
	updated, results, err := ApplyCommands(workspace, catalog, "add-observation-status", []Command{{Type: CommandAddColumnSource, OutputID: document.Output.ID, Payload: &ColumnPayload{OccurrenceID: ws.RootOccurrenceID, Source: &ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "status", ProjectionMode: "VALUE"}}}}})
	if err != nil {
		t.Fatalf("ADD_COLUMN_SOURCE after RELATED_EXPAND: %v", err)
	}
	if len(results) != 1 || results[0].Type != CommandResultColumnAdded {
		t.Fatalf("ADD_COLUMN_SOURCE result = %#v", results)
	}
	updatedDocument := updated.Documents[0]
	var added ws.Column
	for _, column := range updatedDocument.Columns {
		if column.ColumnID != rootID.ID {
			added = column
		}
	}
	if added.ColumnID == "" || added.Source.Field == nil || added.Source.Field.Path != "status" {
		t.Fatalf("added root scalar column = %#v", added)
	}
	expand := updatedDocument.Construction.Steps[0]
	if len(updatedDocument.Construction.Steps) != 1 || len(expand.RowValues) != 0 ||
		expand.Operation.RelatedExpand == nil || expand.Operation.RelatedExpand.EmptyPolicy != ws.ConstructionExpandEmptyPreserveParent {
		t.Fatalf("adding a root scalar changed expansion policy or grain: %#v", updatedDocument.Construction)
	}
	if got, found := stageColumnByID(expand.Outputs, relatedID.ID); !found || !got.Nullable {
		t.Fatalf("nullable unmatched MedicationAdministration ID changed: %#v, found=%t", got, found)
	}
	if got, found := stageColumnByID(expand.Outputs, added.ColumnID); !found || got.Type != "string" {
		t.Fatalf("added root field did not remain a scalar pass-through: %#v, found=%t", got, found)
	}

	encoded, err := updated.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ws.DecodeWorkspace(encoded)
	if err != nil {
		t.Fatalf("reload expanded source field: %v", err)
	}
	groupedDocument := reloaded.Documents[0]
	groupStep := ws.ConstructionStep{
		ID: "group_by_observation_status", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: expand.ID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group_by_observation_status", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys:       []ws.ConstructionGroupKey{{InputColumnID: added.ColumnID, OutputColumnID: "status_key"}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "record_count"}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "status_key", Name: "status", Label: "Status", Type: "string"},
			{ID: "record_count", Name: "record_count", Label: "Record count", Type: "integer"},
		},
	}
	candidate := *groupedDocument.Construction
	candidate.Steps = append(append([]ws.ConstructionStep(nil), candidate.Steps...), groupStep)
	if _, _, err := groupedDocument.AnalyzeConstructionCandidate(candidate, groupStep.ID, nil); err != nil {
		t.Fatalf("GROUP could not consume the added public field at the expanded stage: %v", err)
	}
}

func TestConstructionRejectsNonFiniteTypedNumbers(t *testing.T) {
	if err := (ws.FilterValue{Kind: ws.ConstructionFilterDecimal, Decimal: float64Pointer(math.NaN())}).Validate(); err == nil {
		t.Fatal("NaN filter literal was accepted")
	}
	document := stagedConstructionDocument()
	literal := ws.ConstructionLiteral{Kind: ws.ConstructionNumericDecimal, Decimal: float64Pointer(math.Inf(1))}
	document.Construction.Steps[1].Operation.Derive.Left = ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &literal}
	if err := document.Validate(); err == nil {
		t.Fatal("infinite calculation literal was accepted")
	}
}

func TestRelatedSourceAddsColumnAtSelectedStageWithoutRewritingSourceProjection(t *testing.T) {
	document := workspaceDocument("patients")
	document.RootResourceType = "Patient"
	document.Columns = []ws.Column{{
		ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID", LogicalType: "string",
		OccurrenceID: ws.RootOccurrenceID,
		Source:       ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
	}}
	raw := `{"version":1,"steps":[
		{"id":"keep_patients","inputs":[{"kind":"SOURCE_PROJECTION"}],"operation":{"kind":"FILTER","filter":{"columnId":"patient-id","operator":"EXISTS"}},"outputs":[{"id":"patient-id","name":"patient_id","label":"Patient ID","type":"string"}]},
		{"id":"add_observation_status","inputs":[{"kind":"STEP_OUTPUT","stepId":"keep_patients"}],"operation":{"kind":"RELATED_SOURCE","relatedSource":{"anchorColumnId":"_key","choiceId":"choice-token","sourceOccurrenceId":"observation-node","source":{"kind":"FIELD","candidateId":"observation-status","nodeId":"observation-node","resourceType":"Observation","path":"status","cardinality":"optional_one","logicalType":"string"},"route":[{"edgeId":"patient-observation","fromNodeId":"patient-node","toNodeId":"observation-node","fromResourceType":"Patient","toResourceType":"Observation","relationship":"subject_Patient","storageDirection":"INBOUND","matchMode":"OPTIONAL"}],"contributorRule":{"policy":"ALL_MATCHES","predicate":{"candidateId":"observation-status","operator":"EQUALS","value":{"kind":"STRING","string":"registered"}}},"form":"ALL","rowValuePolicy":"ONE","outputColumnId":"observation-status"}},"outputs":[{"id":"patient-id","name":"patient_id","label":"Patient ID","type":"string"},{"id":"observation-status","name":"observation_status","label":"Observation statuses","type":"string"}]}
	]}`
	var candidate ws.Construction
	if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
		t.Fatalf("decode stage-local related source: %v", err)
	}
	if err := candidate.Validate(document.Columns); err != nil {
		t.Fatalf("validate stage-local related source: %v", err)
	}
	if got := len(candidate.Steps[0].Outputs); got != 1 || candidate.Steps[0].Outputs[0].ID != "patient-id" {
		t.Fatalf("source projection changed to %#v; related output must be stage-local", candidate.Steps[0].Outputs)
	}
	if got := candidate.Steps[1].Inputs; len(got) != 1 || got[0].Kind != ws.ConstructionInputStepOutput || got[0].StepID != "keep_patients" {
		t.Fatalf("related source input = %#v, want exact preceding stage output", got)
	}
	if got := candidate.Steps[1].Operation.RelatedSource.OutputColumnID; got != "observation-status" {
		t.Fatalf("related source stable output ID = %q", got)
	}
	if candidate.Steps[1].Operation.RelatedSource.Form != capability.ConstructionChoiceAll ||
		candidate.Steps[1].Operation.RelatedSource.RowValuePolicy != ws.ConstructionRowValueOne {
		t.Fatalf("related source ALL plus ONE row policy did not round-trip: %#v", candidate.Steps[1].Operation.RelatedSource)
	}
	predicate := candidate.Steps[1].Operation.RelatedSource.ContributorRule.Predicate
	if predicate == nil || predicate.CandidateID != "observation-status" || predicate.Operator != ws.ContributorEquals || predicate.Value == nil || predicate.Value.Kind != ws.ContributorString || predicate.Value.String == nil || *predicate.Value.String != "registered" {
		t.Fatalf("related source scalar contributor predicate did not round trip: %#v", predicate)
	}
	if len(document.Columns) != 1 || document.Columns[0].ColumnID != "patient-id" {
		t.Fatalf("related source mutated initial projection columns: %#v", document.Columns)
	}
	for _, test := range []struct {
		name   string
		policy ws.ConstructionRowValuePolicy
		form   capability.ConstructionChoiceForm
	}{
		{name: "unsupported policy", policy: ws.ConstructionRowValuePolicy("SOME"), form: capability.ConstructionChoiceAll},
		{name: "ONE with count form", policy: ws.ConstructionRowValueOne, form: capability.ConstructionChoiceCount},
	} {
		t.Run(test.name, func(t *testing.T) {
			invalid := candidate
			invalid.Steps = append([]ws.ConstructionStep(nil), candidate.Steps...)
			related := *candidate.Steps[1].Operation.RelatedSource
			related.RowValuePolicy, related.Form = test.policy, test.form
			invalid.Steps[1] = candidate.Steps[1]
			invalid.Steps[1].Operation.RelatedSource = &related
			if err := invalid.Validate(document.Columns); err == nil {
				t.Fatal("invalid related-source row value policy was accepted")
			}
		})
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

func TestApplyCommandsPreservesPivotGroupKeyPresentationOrder(t *testing.T) {
	document := stagedConstructionDocument()
	zero := 0
	for index := range document.Columns {
		if document.Columns[index].ColumnID == "group_id" {
			document.Columns[index].Label = "Specimen ID"
			document.Columns[index].Table = &ws.TablePresentation{Order: &zero}
		}
	}
	visible := true
	for index := range document.Construction.Steps[0].Outputs {
		output := &document.Construction.Steps[0].Outputs[index]
		switch output.ID {
		case "group_id":
			output.Name, output.Type = "stale_group", "boolean"
			output.Table = &ws.TablePresentation{Visible: &visible, Order: &zero}
		case "pivot_a":
			output.Name, output.Label = "d", "d"
			output.Table = &ws.TablePresentation{Order: &zero}
		}
	}
	beforeOperation := document.Construction.Steps[0].Operation
	var beforeSource ws.ColumnSource
	for _, column := range document.Columns {
		if column.ColumnID == "group_id" {
			beforeSource = column.Source
			break
		}
	}
	workspace := constructionWorkspace(document)
	one := 1

	updated, _, err := ApplyCommands(workspace, commandCatalog(), "drag-pivot-group-key", []Command{
		{Type: CommandUpdateColumn, OutputID: "patients", Payload: &ColumnPayload{Column: "group_id", ColumnValue: &ws.Column{Label: "Specimen ID", Table: &ws.TablePresentation{Order: &one}}}},
		{Type: CommandUpdateConstructionOutput, OutputID: "patients", Payload: &ColumnPayload{ConstructionOutput: &ConstructionOutputPresentation{
			StepID: "pivot_step", ColumnID: "group_id", Label: "Group",
			Table: &ws.TablePresentation{Order: &one},
		}}},
	})
	if err != nil {
		t.Fatalf("apply source and Pivot group-key presentation updates: %v", err)
	}

	updatedDocument := updated.Documents[0]
	var source ws.Column
	for _, column := range updatedDocument.Columns {
		if column.ColumnID == "group_id" {
			source = column
			break
		}
	}
	if source.ColumnID != "group_id" || source.Table == nil || source.Table.Order == nil || *source.Table.Order != 0 {
		t.Fatalf("authored source column should normalize independently to order 0: %#v", source)
	}
	if !reflect.DeepEqual(source.Source, beforeSource) {
		t.Fatalf("authored source binding changed: before=%#v after=%#v", beforeSource, source.Source)
	}
	pivot := updatedDocument.Construction.Steps[0]
	if !reflect.DeepEqual(pivot.Operation, beforeOperation) {
		t.Fatalf("Pivot compiler operation changed during presentation update")
	}
	groupKey, found := stageColumnByID(pivot.Outputs, "group_id")
	if !found || groupKey.Name != "group_id" || groupKey.Type != "string" || groupKey.Label != "Specimen ID" ||
		groupKey.Table == nil || groupKey.Table.Order == nil || *groupKey.Table.Order != 1 {
		t.Fatalf("Pivot group-key identity/presentation = %#v, found=%v; want declared order 1", groupKey, found)
	}
	generated, found := stageColumnByID(pivot.Outputs, "pivot_a")
	if !found || generated.Name != "d" || generated.Table == nil || generated.Table.Order == nil || *generated.Table.Order != 0 {
		t.Fatalf("generated d presentation = %#v, found=%v; want order 0", generated, found)
	}
	if normalized := updated.NormalizePresentationOrders(); !reflect.DeepEqual(updated, normalized) {
		t.Fatalf("presentation normalization is not idempotent")
	}

	canonical, err := updated.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonicalize updated workspace: %v", err)
	}
	reloaded, err := ws.DecodeWorkspace(canonical)
	if err != nil {
		t.Fatalf("reload updated workspace: %v", err)
	}
	reloadedPivot := reloaded.Documents[0].Construction.Steps[0]
	reloadedGroup, groupFound := stageColumnByID(reloadedPivot.Outputs, "group_id")
	reloadedGenerated, generatedFound := stageColumnByID(reloadedPivot.Outputs, "pivot_a")
	if !groupFound || reloadedGroup.Table == nil || reloadedGroup.Table.Order == nil || *reloadedGroup.Table.Order != 1 {
		t.Fatalf("reloaded Pivot group-key presentation = %#v, found=%v", reloadedGroup, groupFound)
	}
	if !generatedFound || reloadedGenerated.Table == nil || reloadedGenerated.Table.Order == nil || *reloadedGenerated.Table.Order != 0 {
		t.Fatalf("reloaded generated d presentation = %#v, found=%v", reloadedGenerated, generatedFound)
	}
	reencoded, err := reloaded.CanonicalJSON()
	if err != nil || string(reencoded) != string(canonical) {
		t.Fatalf("canonical JSON changed after reload: err=%v", err)
	}
}
