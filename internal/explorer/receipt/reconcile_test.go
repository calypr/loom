package receipt

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/recipetest"
	receipttest "github.com/calypr/loom/internal/testkit/recipetest"
)

func TestReconcileFinalOutputMetadataUsesCompilerSchemaTypes(t *testing.T) {
	translated, resolved := reconciliationFixture()
	reconciled, err := ReconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatal(err)
	}
	wantColumns := []string{"patient_id", "score", "scaled_score"}
	if got := emittedPublicColumnNames(reconciled.EmittedColumns); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("emitted public columns = %#v, want %#v", got, wantColumns)
	}
	if got := []string{reconciled.Presentations[0].Columns[0].PublicColumn, reconciled.Presentations[0].Columns[1].PublicColumn, reconciled.Presentations[0].Columns[2].PublicColumn}; !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("presentation columns = %#v, want %#v", got, wantColumns)
	}
	for index, column := range reconciled.Presentations[0].Columns {
		if column.PhysicalOrder != index {
			t.Fatalf("presentation column %q physical order = %d, want %d", column.PublicColumn, column.PhysicalOrder, index)
		}
	}
	if !reflect.DeepEqual(reconciled.IdentityMappings, []receiptmodel.IdentityMapping{{OutputID: "patients", CandidateID: "candidate", EmissionIDs: []string{"emit_patient_id", "emit_score"}}}) {
		t.Fatalf("identity mappings retained non-final emission: %#v", reconciled.IdentityMappings)
	}

	id := reconciled.EmittedColumns[0]
	if id.NodeID != "node-patient" || id.CandidateID != "candidate-id" || id.SourcePath != "id" || !id.Lossless || !id.MLReady {
		t.Fatalf("source metadata was not preserved: %#v", id)
	}
	score := reconciled.EmittedColumns[1]
	wantUnit := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}
	if score.LogicalType != "decimal" || score.Cardinality != "required_one" || score.Nullable || !reflect.DeepEqual(score.ResultUnit, wantUnit) {
		t.Fatalf("compiler metadata for base column = %#v", score)
	}
	wantSourcePolicy := &viewer.PublicUnitNormalization{Target: unit.UnitIdentity{System: "urn:test:source", Code: "source-unit"}, Rules: []viewer.PublicUnitRuleIdentity{{ID: "source-to-target", Version: "1"}}}
	if !reflect.DeepEqual(score.UnitNormalization, wantSourcePolicy) {
		t.Fatalf("source unit normalization policy changed: %#v", score.UnitNormalization)
	}
	derived := reconciled.EmittedColumns[2]
	if derived.Label != "Scaled score" || derived.ConstructionID != "scale_score" || !reflect.DeepEqual(derived.InputColumns, []string{"score"}) || !reflect.DeepEqual(derived.AuthoredColumns, []string{"score"}) {
		t.Fatalf("derived table-shape metadata = %#v", derived)
	}
	if derived.LogicalType != "decimal" || derived.Cardinality != "optional_one" || !derived.Nullable || !reflect.DeepEqual(derived.ResultUnit, wantUnit) {
		t.Fatalf("compiler metadata for derived column = %#v", derived)
	}
	if derived.Shape != "scalar" || derived.Lossless || derived.MLReady || derived.StructuralSuitability != "requires-review" || !reflect.DeepEqual(derived.LossReasons, []string{"TABLE_SHAPE_DERIVED_MULTIPLY_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}) || derived.SourcePath != "" || derived.SourceResourceType != "" || derived.CandidateID != "" || !derived.Filterable || !derived.Chartable || derived.UnitNormalization != nil {
		t.Fatalf("derived column fabricated source or browser metadata: %#v", derived)
	}

	contract := reconciled.OutputContracts[0]
	if got := publicContractColumnNames(contract.Columns); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("contract columns = %#v, want %#v", got, wantColumns)
	}
	if contract.Lossless || contract.MLReady || contract.StructuralSuitability != "requires-review" || !reflect.DeepEqual(contract.LossReasons, []string{"source-loss", "TABLE_SHAPE_DERIVED_MULTIPLY_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}) {
		t.Fatalf("aggregate contract quality = %#v", contract)
	}
	if err := (receiptmodel.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(reconciled.Bundle, reconciled.EmittedColumns); err != nil {
		t.Fatalf("validate reconciled output contract: %v", err)
	}
}

func TestReconcileFinalOutputMetadataPreservesAuthoredOrderWithoutChangingExecutionOrder(t *testing.T) {
	translated, resolved := reconciliationFixture()
	for index := range translated.Presentations[0].Columns {
		switch translated.Presentations[0].Columns[index].PublicColumn {
		case "patient_id":
			translated.Presentations[0].Columns[index].Order = 1
		case "score":
			translated.Presentations[0].Columns[index].Order = 0
		}
	}

	reconciled, err := ReconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatal(err)
	}
	wantPresentation := []string{"score", "patient_id", "scaled_score"}
	if got := emittedPublicColumnNames(reconciled.EmittedColumns); !reflect.DeepEqual(got, wantPresentation) {
		t.Fatalf("receipt emitted columns = %#v, want authored order %#v", got, wantPresentation)
	}
	if got := publicContractColumnNames(reconciled.OutputContracts[0].Columns); !reflect.DeepEqual(got, wantPresentation) {
		t.Fatalf("public output contract columns = %#v, want authored order %#v", got, wantPresentation)
	}
	if err := (receiptmodel.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(reconciled.Bundle, reconciled.EmittedColumns); err != nil {
		t.Fatalf("validate ordered receipt contract: %v", err)
	}
	if got := []string{
		resolved.Compiled.Outputs[0].OutputSchema[0].Name,
		resolved.Compiled.Outputs[0].OutputSchema[1].Name,
		resolved.Compiled.Outputs[0].OutputSchema[2].Name,
	}; !reflect.DeepEqual(got, []string{"patient_id", "score", "scaled_score"}) {
		t.Fatalf("compiler execution schema order changed: %#v", got)
	}
}

func TestReconcileFinalOutputMetadataRejectsMissingOutputIdentity(t *testing.T) {
	translated, resolved := reconciliationFixture()
	resolved.Compiled.Outputs[0].Name = ""
	if _, err := ReconcileFinalOutputMetadata(translated, resolved); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("missing compiler output identity error = %v", err)
	}
}

func TestReconcileFinalOutputMetadataRejectsMissingConstructedDependency(t *testing.T) {
	translated, resolved := reconciliationFixture()
	translated.Workspace.Documents[0].Construction.Steps[0].Operation.Derive.Left.ColumnID = "missing"
	var missing *ConstructionInputMissingError
	if _, err := ReconcileFinalOutputMetadata(translated, resolved); err == nil || !errors.As(err, &missing) || missing.ColumnID != "missing" || missing.StepID != "scale_score" || missing.OutputID != "scaled_score" {
		t.Fatalf("missing constructed prior-stage input error = %v", err)
	}
}

func TestReconcileFinalOutputMetadataRejectsConstructedDependencyCycle(t *testing.T) {
	translated, resolved := reconciliationFixture()
	translated.Workspace.Documents[0].Construction.Steps[0].Operation.Derive.Left.ColumnID = "scaled_score"

	var missing *ConstructionInputMissingError
	if _, err := ReconcileFinalOutputMetadata(translated, resolved); err == nil || !errors.As(err, &missing) || missing.ColumnID != "scaled_score" || missing.StepID != "scale_score" || missing.OutputID != "scaled_score" {
		t.Fatalf("constructed self-dependency error = %v", err)
	}
}

func TestReconcileFinalOutputMetadataRejectsConstructedEmissionCollision(t *testing.T) {
	translated, resolved := reconciliationFixture()
	translated.EmittedColumns[2].EmissionID = constructedEmissionID("scale_score", "scaled_score")
	if _, err := ReconcileFinalOutputMetadata(translated, resolved); err == nil || !strings.Contains(err.Error(), "duplicates translated emission identity") {
		t.Fatalf("constructed emission collision error = %v", err)
	}
}

func TestAuthoredOutputColumnsDeduplicatesDirectInputsInAuthoringOrder(t *testing.T) {
	document := receipttest.StageFixture("patients", []ws.StageColumn{{ID: "score", Name: "score", Label: "Score", Type: "decimal"}, {ID: "patient_count", Name: "patient_count", Label: "Patient count", Type: "integer"}})
	receipttest.AppendDeriveFixture(&document, "same_input", ws.StageColumn{ID: "same_result", Name: "same_result", Label: "Same result", Type: "decimal"}, ws.ConstructionDerivedAdd, receipttest.ColumnOperand("score"), receipttest.ColumnOperand("score"), ws.ConstructionMissingInputError)
	receipttest.AppendDeriveFixture(&document, "ordered_inputs", ws.StageColumn{ID: "ordered_result", Name: "ordered_result", Label: "Ordered result", Type: "decimal"}, ws.ConstructionDerivedSubtract, receipttest.ColumnOperand("score"), receipttest.ColumnOperand("patient_count"), ws.ConstructionMissingInputError)
	workspace := receipttest.StageWorkspace(document)

	_, columns, err := authoredOutputColumns(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := columns["patients"]["same_result"].InputColumns; !reflect.DeepEqual(got, []string{"score"}) {
		t.Errorf("repeated direct inputs = %#v, want one occurrence", got)
	}
	if got := columns["patients"]["ordered_result"].InputColumns; !reflect.DeepEqual(got, []string{"score", "patient_count"}) {
		t.Errorf("direct inputs = %#v, want authoring order", got)
	}
}

func TestConstructedOutputProfileRequiresArrayPolicyForManyCardinality(t *testing.T) {
	_, err := constructedOutputProfileFor(
		ConstructedOutputQuality{Lossless: true, StructuralSuitability: "scalar"},
		facade.CompiledOutputColumn{Name: "statuses", Kind: "string", Cardinality: "many"},
	)
	if err == nil || !strings.Contains(err.Error(), "requires an explicit array policy") {
		t.Fatalf("many-valued constructed output without array policy error = %v", err)
	}
}

func TestRelatedSourceOutputProfileMatchesSelectedForm(t *testing.T) {
	for _, test := range []struct {
		name           string
		form           capability.ConstructionChoiceForm
		rowValuePolicy ws.ConstructionRowValuePolicy
		kind           string
		cardinality    string
		shape          string
	}{
		{name: "list", form: capability.ConstructionChoiceAll, kind: "string", cardinality: "many", shape: "array"},
		{name: "one distinct value", form: capability.ConstructionChoiceAll, rowValuePolicy: ws.ConstructionRowValueOne, kind: "string", cardinality: "optional_one", shape: "scalar"},
		{name: "count", form: capability.ConstructionChoiceCount, kind: "integer", cardinality: "required_one", shape: "scalar"},
		{name: "presence", form: capability.ConstructionChoicePresence, kind: "boolean", cardinality: "required_one", shape: "scalar"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const columnName = "related_observation"
			document := ws.Document{
				Output: ws.Output{ID: "patients"},
				Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
					ID: "observation-step",
					Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedSource, RelatedSource: &ws.ConstructionRelatedSource{
						Form: test.form, RowValuePolicy: test.rowValuePolicy, OutputColumnID: "observation-column",
						ContributorRule: ws.ConstructionRelatedContributorRule{Policy: ws.ConstructionRelatedAllMatches},
					}},
					Outputs: []ws.StageColumn{{ID: "observation-column", Name: columnName, Label: "Related observation", Type: test.kind}},
				}}},
			}
			authored := map[string]AuthoredOutputColumn{}
			if err := authoredConstructionOutputs(document, authored); err != nil {
				t.Fatal(err)
			}
			profile, err := constructedOutputProfileFor(authored[columnName].Quality, facade.CompiledOutputColumn{
				Name: columnName, Kind: test.kind, Cardinality: test.cardinality,
			})
			if err != nil {
				t.Fatal(err)
			}
			if profile.Shape != test.shape {
				t.Fatalf("profile shape = %q, want %q", profile.Shape, test.shape)
			}
		})
	}
}

func TestCodedPivotOutputKeepsSourceProvenanceAndSelectedCategoryLoss(t *testing.T) {
	document := ws.Document{
		Output: ws.Output{ID: "specimens"},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "coded-pivot-step",
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCodedPivot, CodedPivot: &ws.ConstructionCodedPivot{
				ConstructionID: "coded-pivot-step",
				Source: &ws.ConstructionCodedPivotSource{
					Family:      capability.SemanticFrameFamily{ResourceType: "Specimen", ValuePath: "type.coding[].display"},
					CandidateID: "candidate-id", NodeID: "node-id",
				},
				Categories:      []ws.ConstructionCodedPivotCategory{{System: "system", Code: "code", OutputColumnID: "category-column"}},
				DuplicatePolicy: ws.ConstructionPivotDuplicateError, MissingCellPolicy: ws.ConstructionPivotMissingNull,
			}},
			Outputs: []ws.StageColumn{{ID: "category-column", Name: "specimen_type", Label: "Specimen type", Type: "string"}},
		}}},
	}
	authored := map[string]AuthoredOutputColumn{}
	if err := authoredConstructionOutputs(document, authored); err != nil {
		t.Fatal(err)
	}
	got := authored["specimen_type"]
	if got.ConstructionID != "coded-pivot-step" || got.NodeID != "node-id" || got.CandidateID != "candidate-id" || got.SourceResourceType != "Specimen" || got.SourcePath != "type.coding[].display" {
		t.Fatalf("coded pivot provenance = %+v", got)
	}
	if !reflect.DeepEqual(got.Quality.LossReasons, []string{"CODED_PIVOT_SELECTED_CATEGORIES_ONLY"}) {
		t.Fatalf("coded pivot loss reasons = %v", got.Quality.LossReasons)
	}
}

func TestPivotPromotesPrivateRowKeyIntoAuthoredOutput(t *testing.T) {
	document := ws.Document{Construction: &ws.Construction{
		SourceProjections: []ws.ConstructionSourceProjection{{ColumnID: "specimen_ref", OwnerStepID: "pivot"}},
		Steps: []ws.ConstructionStep{{
			ID: "pivot",
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"specimen_ref"},
				DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
				MissingCellPolicy:      ws.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
			}},
			Outputs: []ws.StageColumn{{ID: "specimen_ref", Name: "specimen_reference", Label: "Specimen reference", Type: "string"}},
		}},
	}}
	authored := make(map[string]AuthoredOutputColumn)
	if err := authoredConstructionOutputs(document, authored); err != nil {
		t.Fatal(err)
	}
	key, found := authored["specimen_reference"]
	if !found || key.ConstructionID != "pivot" || key.Label != "Specimen reference" || !key.TypedStageOutput {
		t.Fatalf("private row key has no public Pivot identity: %#v", authored)
	}
	if len(key.InputColumns) != 0 {
		t.Fatalf("private source leaked into public lineage: %#v", key.InputColumns)
	}
	document.Construction.SourceProjections = nil
	document.Construction.Steps = append([]ws.ConstructionStep{{
		ID: "input", OwnerStepID: "pivot",
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedField, RelatedField: &ws.ConstructionRelatedField{
			OutputColumnID: "specimen_ref", Source: ws.ConstructionRelatedFieldSource{NodeID: "observation", CandidateID: "specimen-reference"},
		}},
		Outputs: []ws.StageColumn{{ID: "specimen_ref", Name: "__pivot_source_reference", Label: "Observation.specimen.reference", Type: "string"}},
	}}, document.Construction.Steps...)
	authored = make(map[string]AuthoredOutputColumn)
	if err := authoredConstructionOutputs(document, authored); err != nil {
		t.Fatal(err)
	}
	key, found = authored["specimen_reference"]
	if !found || key.NodeID != "observation" || key.CandidateID != "specimen-reference" || key.Label != "Specimen reference" {
		t.Fatalf("promoted related row key lost its source identity: %#v", authored)
	}
}

func TestAuthoredOutputColumnsResolvesFinalTypedConstructionLineage(t *testing.T) {
	group := ws.StageColumn{ID: "group-id", Name: "group", Label: "Group", Type: "string"}
	category := ws.StageColumn{ID: "category-id", Name: "category", Label: "Category", Type: "string"}
	value := ws.StageColumn{ID: "value-id", Name: "value", Label: "Value", Type: "integer"}
	pivotValue := ws.StageColumn{ID: "pivot-value-id", Name: "active_value", Label: "Active value", Type: "integer"}
	derivedValue := ws.StageColumn{ID: "derived-value-id", Name: "adjusted_value", Label: "Adjusted value", Type: "integer"}
	measureKey := ws.StageColumn{ID: "measure-key-id", Name: "measure", Label: "Measure", Type: "string"}
	measureValue := ws.StageColumn{ID: "measure-value-id", Name: "measure_value", Label: "Measure value", Type: "integer"}
	one := int64(1)
	active := "active"
	document := ws.Document{
		Output: ws.Output{ID: "patients"},
		Columns: []ws.Column{
			{ColumnID: group.ID, Column: group.Name, Label: group.Label},
			{ColumnID: category.ID, Column: category.Name, Label: category.Label},
			{ColumnID: value.ID, Column: value.Name, Label: value.Label},
		},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
			{
				ID: "pivot-step",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
					ConstructionID: "pivot-step", GroupKeyIDs: []string{group.ID}, CategoryColumnID: category.ID, ValueColumnID: value.ID,
					Categories:      []ws.ConstructionPivotCategory{{Key: ws.TableScalar{Kind: "STRING", String: &active}, OutputColumnID: pivotValue.ID}},
					DuplicatePolicy: ws.ConstructionPivotDuplicateError, MissingCellPolicy: ws.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
				}},
				Outputs: []ws.StageColumn{group, pivotValue},
			},
			{
				ID: "derive-step",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
					ConstructionID: "derive-step", OutputColumnID: derivedValue.ID, Operation: ws.ConstructionDerivedAdd,
					Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: pivotValue.ID},
					Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &one}},
					MissingInputPolicy: ws.ConstructionMissingInputError,
				}},
				Outputs: []ws.StageColumn{group, pivotValue, derivedValue},
			},
			{
				ID:        "filter-step",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: derivedValue.ID, Operator: ws.ConstructionFilterExists}},
				Outputs:   []ws.StageColumn{group, pivotValue, derivedValue},
			},
			{
				ID: "unpivot-step",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
					ConstructionID: "unpivot-step", Inputs: []ws.ConstructionUnpivotInput{{ColumnID: pivotValue.ID, Key: ws.TableScalar{Kind: "STRING", String: &active}}, {ColumnID: derivedValue.ID, Key: ws.TableScalar{Kind: "STRING", String: &active}}},
					KeyOutputColumnID: measureKey.ID, ValueOutputColumnID: measureValue.ID, NullRowPolicy: ws.ConstructionUnpivotPreserve,
				}},
				Outputs: []ws.StageColumn{group, measureKey, measureValue},
			},
		}},
	}
	_, columns, err := authoredOutputColumns(ws.Workspace{Documents: []ws.Document{document}})
	if err != nil {
		t.Fatal(err)
	}
	final := columns["patients"]
	if len(final) != 2 {
		t.Fatalf("final constructed columns = %#v, want only measure and measure_value", final)
	}
	emitted := map[string]viewer.EmittedColumn{
		"group":    {AuthoredColumns: []string{"group"}},
		"category": {AuthoredColumns: []string{"category"}},
		"value":    {AuthoredColumns: []string{"value"}},
	}
	lineage, err := resolveAuthoredOutputLineage(final, emitted, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"category", "group", "value"}
	for _, name := range []string{"measure", "measure_value"} {
		if _, exists := final[name]; !exists || !reflect.DeepEqual(lineage[name], want) {
			t.Errorf("final typed output %q lineage = %#v, want %#v", name, lineage[name], want)
		}
	}
}

func TestReconcileFinalOutputMetadataResolvesGroupedSameNameKeyLineage(t *testing.T) {
	const columnName = "col_f49356bac08742bd007bfd24"
	source := viewer.EmittedColumn{
		EmissionID: "source-system", OutputID: "body-structures", NodeID: "body-structure-node",
		CandidateID: "system-candidate", OccurrenceID: "base", AuthoredColumns: []string{columnName},
		PublicColumn: columnName, Label: "System", LogicalType: "string", Cardinality: "optional_one",
		Nullable: true, Shape: "scalar", SourceResourceType: "BodyStructure",
		SourcePath: "includedStructure[].structure.coding[].system", Lossless: true,
		StructuralSuitability: "scalar",
	}
	bundle := recipe.Bundle{Outputs: []recipe.Output{{
		Name: "body-structures", RootResourceType: "BodyStructure", RowGrain: "groups",
	}}}
	groupOutput := ws.StageColumn{ID: "grouped-system", Name: columnName, Label: "System", Type: "string", Nullable: true}
	sourceContractColumn := receiptTestPublicColumn(source)
	sourceContractColumn.Cardinality = source.Cardinality
	document := ws.Document{
		Output:  ws.Output{ID: "body-structures", Title: "Body structures"},
		Columns: []ws.Column{{ColumnID: "source-system-id", Column: columnName, Label: "System"}},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "group-step",
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
				ConstructionID: "group-by-system",
				Keys:           []ws.ConstructionGroupKey{{InputColumnID: "source-system-id", OutputColumnID: groupOutput.ID}},
			}},
			Outputs: []ws.StageColumn{groupOutput},
		}}},
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: ws.Workspace{Documents: []ws.Document{document}},
		EmittedColumns: []viewer.EmittedColumn{source},
		OutputContracts: []receiptmodel.PublicOutputContract{{
			OutputID: "body-structures", RootResourceType: "BodyStructure", RowGrain: "groups",
			Lossless: true, MLReady: false, StructuralSuitability: "scalar",
			Columns: []receiptmodel.PublicOutputColumn{sourceContractColumn},
		}},
		Presentations: []explorercompilation.PresentationConfig{{
			OutputID: "body-structures", Title: "Body structures",
			Columns: []explorercompilation.PresentationColumn{{
				EmissionID: source.EmissionID, PublicColumn: columnName, Label: source.Label, Visible: true, Order: 0,
			}},
		}},
	}
	resolved := dataframeexecution.Resolved{
		Bundle: bundle,
		Compiled: facade.CompiledRecipe{Outputs: []facade.CompiledRecipeOutput{{
			Name: "body-structures", OutputSchema: []facade.CompiledOutputColumn{{
				Name: columnName, Kind: "string", Cardinality: "optional_one", Nullable: true,
			}},
		}}},
	}

	reconciled, err := ReconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatalf("reconcile a grouped key that retains its source name: %v", err)
	}
	if len(reconciled.EmittedColumns) != 1 {
		t.Fatalf("reconciled emissions = %#v, want exactly the grouped key", reconciled.EmittedColumns)
	}
	grouped := reconciled.EmittedColumns[0]
	if grouped.ConstructionID != "group-by-system" || grouped.EmissionID != constructedEmissionID("group-by-system", columnName) {
		t.Fatalf("grouped key construction metadata = %#v", grouped)
	}
	if !reflect.DeepEqual(grouped.InputColumns, []string{columnName}) || !reflect.DeepEqual(grouped.AuthoredColumns, []string{columnName}) {
		t.Fatalf("grouped key inputs/lineage = %#v/%#v", grouped.InputColumns, grouped.AuthoredColumns)
	}
	if grouped.NodeID != source.NodeID || grouped.CandidateID != source.CandidateID || grouped.SourceResourceType != source.SourceResourceType || grouped.SourcePath != source.SourcePath {
		t.Fatalf("grouped key lost source identity: %#v", grouped)
	}
	if grouped.Lossless || grouped.MLReady || grouped.StructuralSuitability != "requires-review" || !reflect.DeepEqual(grouped.LossReasons, []string{"TABLE_SHAPE_GROUP_CHANGES_ROW_GRAIN", TableShapeMLReadinessUnassessed}) {
		t.Fatalf("grouped key quality = %#v", grouped)
	}
}

func TestReconcileFinalOutputMetadataPublishesOnlyScalarGroupOutputs(t *testing.T) {
	const (
		inputID         = "source_active"
		keyOutputID     = "active_key"
		countOutputID   = "record_count"
		keyOutputName   = "active"
		countOutputName = "record_count"
	)
	groupOutputs := []ws.StageColumn{
		{ID: keyOutputID, Name: keyOutputName, Label: "Whether this record is in active use", Type: "boolean", Nullable: true},
		{ID: countOutputID, Name: countOutputName, Label: "Record count", Type: "integer"},
	}
	bundle := recipe.Bundle{Outputs: []recipe.Output{{Name: "body-structures", RootResourceType: "BodyStructure", RowGrain: "groups"}}}
	document := ws.Document{
		Output: ws.Output{ID: "body-structures", Title: "Body structures"},
		Construction: &ws.Construction{
			Version: ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{{
				ColumnID: inputID, OccurrenceID: ws.RootOccurrenceID, FieldPath: "active",
				FHIRType: "boolean", LogicalType: "boolean", Label: "Whether this record is in active use",
			}},
			Steps: []ws.ConstructionStep{{
				ID: "group_by_active", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
					ConstructionID: "group_by_active",
					Keys:           []ws.ConstructionGroupKey{{InputColumnID: inputID, OutputColumnID: keyOutputID}},
					Aggregates:     []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: countOutputID}},
				}},
				Outputs: groupOutputs,
			}},
		},
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle:    bundle,
		Workspace: ws.Workspace{Documents: []ws.Document{document}},
		OutputContracts: []receiptmodel.PublicOutputContract{{
			OutputID: "body-structures", RootResourceType: "BodyStructure", RowGrain: "groups",
			Lossless: true, MLReady: true,
		}},
		Presentations: []explorercompilation.PresentationConfig{{OutputID: "body-structures", Title: "Body structures"}},
	}
	resolved := dataframeexecution.Resolved{
		Bundle: bundle,
		Compiled: facade.CompiledRecipe{Outputs: []facade.CompiledRecipeOutput{{
			Name: "body-structures",
			OutputSchema: []facade.CompiledOutputColumn{
				{Name: keyOutputName, SemanticPath: "construction:group_by_active", Kind: "boolean", Cardinality: "optional_one", Nullable: true},
				{Name: countOutputName, SemanticPath: "construction:group_by_active", Kind: "integer", Cardinality: "required_one"},
				{Name: "internal_identity", SemanticPath: "internal:identity", Kind: "string", Cardinality: "required_one", Identity: true},
				{Name: "__loom_row_id", SemanticPath: "internal:row", Kind: "object", Cardinality: "required_one", Internal: true, Identity: true},
			},
		}}},
	}

	reconciled, err := ReconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatalf("reconcile scalar source GROUP outputs: %v", err)
	}
	wantNames := []string{keyOutputName, countOutputName}
	if got := emittedPublicColumnNames(reconciled.EmittedColumns); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("public emissions = %#v, want only group outputs %#v", got, wantNames)
	}
	if got := publicContractColumnNames(reconciled.OutputContracts[0].Columns); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("public contract columns = %#v, want only group outputs %#v", got, wantNames)
	}
	if got := []string{reconciled.Presentations[0].Columns[0].PublicColumn, reconciled.Presentations[0].Columns[1].PublicColumn}; !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("presentation columns = %#v, want only group outputs %#v", got, wantNames)
	}
	for _, emitted := range reconciled.EmittedColumns {
		if emitted.ConstructionID != "group_by_active" || len(emitted.InputColumns) != 0 || len(emitted.AuthoredColumns) != 0 ||
			emitted.PublicColumn == inputID || emitted.PublicColumn == ws.ConstructionSourceProjectionName(inputID) {
			t.Fatalf("hidden source input leaked into public emission: %#v", emitted)
		}
	}
}

func TestReconcileFinalOutputMetadataPreservesInheritedExplicitGroupKeyThroughExpand(t *testing.T) {
	const (
		outputID      = "patients"
		groupRevision = "grouprev_fixture"
		genderSource  = "gender-source"
		genderItemID  = "gender-item-id"
		genderItem    = "gender_item"
	)
	source := viewer.EmittedColumn{
		EmissionID: "source-gender", OutputID: outputID, AuthoredColumns: []string{"gender"},
		PublicColumn: "gender", Label: "Gender", LogicalType: "string", Cardinality: "optional_one",
		Nullable: true, Shape: "scalar", SourceResourceType: "Patient", SourcePath: "gender",
		Lossless: true, MLReady: true, StructuralSuitability: "scalar", Filterable: true, Chartable: true,
	}
	bundle := recipe.Bundle{Outputs: []recipe.Output{{
		Name: outputID, RootResourceType: "Patient", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: groupRevision, UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED"},
	}}}
	groupLabel := ws.StageColumn{ID: "group-label-output", Name: "group_label", Label: "Group label", Type: "string"}
	genderKey := ws.StageColumn{ID: "gender-key-output", Name: genderItem, Label: "Gender item", Type: "string", Nullable: true}
	rowCount := ws.StageColumn{ID: "row-count-output", Name: "row_count", Label: "Row count", Type: "integer"}
	document := ws.Document{
		Output: ws.Output{ID: outputID, Title: "Two Patient cohort"},
		Rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{Source: ws.GroupSource{
			Kind: ws.GroupSourceExplicit,
			Explicit: &ws.ExplicitGroupSource{
				RevisionID: groupRevision, UnassignedMemberPolicy: ws.UnassignedMemberGroupAsUnassigned,
			},
		}}},
		Columns: []ws.Column{{ColumnID: genderSource, Column: "gender", Label: "Gender"}},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
			{
				ID:     "expand-members",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group_rows"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationExpand, Expand: &ws.ConstructionExpand{
					ConstructionID: "expand-members", InputColumnID: genderSource, OutputColumnID: genderItemID,
					EmptyPolicy: ws.ConstructionExpandEmptyPreserveParent,
				}},
				Outputs: []ws.StageColumn{
					{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
					{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
					{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
					{ID: "members", Name: "members", Label: "Members", Type: "array"},
					{ID: genderItemID, Name: genderItem, Label: "Gender item", Type: "string", Nullable: true},
				},
			},
			{
				ID:     "group-by-gender",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "expand-members"}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
					ConstructionID: "group-by-gender", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
					Keys: []ws.ConstructionGroupKey{
						{InputColumnID: "group_label", OutputColumnID: groupLabel.ID},
						{InputColumnID: genderItemID, OutputColumnID: genderKey.ID},
					},
					Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: rowCount.ID}},
				}},
				Outputs: []ws.StageColumn{groupLabel, genderKey, rowCount},
			},
		}},
	}
	sourceContractColumn := receiptTestPublicColumn(source)
	sourceContractColumn.Cardinality = source.Cardinality
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: ws.Workspace{Documents: []ws.Document{document}},
		EmittedColumns: []viewer.EmittedColumn{source},
		OutputContracts: []receiptmodel.PublicOutputContract{{
			OutputID: outputID, RootResourceType: "Patient", RowGrain: "groups", Lossless: true, MLReady: true,
			StructuralSuitability: "scalar",
			Columns:               []receiptmodel.PublicOutputColumn{sourceContractColumn},
		}},
		Presentations: []explorercompilation.PresentationConfig{{OutputID: outputID, Title: "Two Patient cohort", Columns: []explorercompilation.PresentationColumn{{
			EmissionID: source.EmissionID, PublicColumn: source.PublicColumn, Label: source.Label, Visible: true, Order: 0,
		}}}},
	}
	resolved := dataframeexecution.Resolved{
		Bundle: bundle,
		Compiled: facade.CompiledRecipe{Outputs: []facade.CompiledRecipeOutput{{Name: outputID, OutputSchema: []facade.CompiledOutputColumn{
			{Name: "group_label", SemanticPath: "groups.label", Kind: "string", Cardinality: "required_one"},
			{Name: genderItem, SemanticPath: "construction:group-by-gender", Kind: "string", Cardinality: "optional_one", Nullable: true},
			{Name: "row_count", SemanticPath: "construction:group-by-gender", Kind: "integer", Cardinality: "required_one"},
			{Name: "__loom_row_id", Kind: "object", Cardinality: "required_one", Internal: true, Identity: true},
		}}}},
	}

	reconciled, err := ReconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatalf("reconcile Group key inherited from explicit groups through Expand: %v", err)
	}
	if got, want := emittedPublicColumnNames(reconciled.EmittedColumns), []string{"group_label", genderItem, "row_count"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("reconciled output columns = %#v, want %#v", got, want)
	}
	groupLabelEmission := reconciled.EmittedColumns[0]
	if groupLabelEmission.EmissionID != explicitGroupEmissionID(groupRevision, "group_label") || groupLabelEmission.ConstructionID != "" ||
		len(groupLabelEmission.AuthoredColumns) != 0 || len(groupLabelEmission.InputColumns) != 0 || groupLabelEmission.Label != "Group label" {
		t.Fatalf("inherited explicit group label metadata = %#v", groupLabelEmission)
	}
	genderEmission := reconciled.EmittedColumns[1]
	if genderEmission.ConstructionID != "group-by-gender" || !reflect.DeepEqual(genderEmission.InputColumns, []string{"gender"}) ||
		!reflect.DeepEqual(genderEmission.AuthoredColumns, []string{"gender"}) || genderEmission.LogicalType != "string" || genderEmission.Nullable != true {
		t.Fatalf("grouped expanded Gender metadata = %#v", genderEmission)
	}
	rowCountEmission := reconciled.EmittedColumns[2]
	if rowCountEmission.ConstructionID != "group-by-gender" || len(rowCountEmission.InputColumns) != 0 || len(rowCountEmission.AuthoredColumns) != 0 ||
		rowCountEmission.LogicalType != "integer" || rowCountEmission.Nullable {
		t.Fatalf("group row-count metadata = %#v", rowCountEmission)
	}
	if err := (receiptmodel.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(bundle, reconciled.EmittedColumns); err != nil {
		t.Fatalf("validate Group→Expand public output contract: %v", err)
	}
}

func TestReconcileFinalOutputMetadataPublishesExplicitGroupRowsSchema(t *testing.T) {
	const outputID = "specimens"
	bundle := recipe.Bundle{Outputs: []recipe.Output{{
		Name: outputID, RootResourceType: "Specimen", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"},
	}}}
	document := ws.Document{
		Output: ws.Output{ID: outputID, Title: "Specimens"},
		Rows: ws.RowDefinition{Kind: ws.RowDefinitionGroups, Groups: &ws.GroupedRows{Source: ws.GroupSource{
			Kind:     ws.GroupSourceExplicit,
			Explicit: &ws.ExplicitGroupSource{RevisionID: "grouprev_1", UnassignedMemberPolicy: ws.UnassignedMemberExclude},
		}}},
	}
	source := viewer.EmittedColumn{
		EmissionID: "source-specimen-id", OutputID: outputID, PublicColumn: "specimen_id", Label: "Specimen ID",
		LogicalType: "string", Cardinality: "optional_one", Nullable: true, Shape: "scalar",
		SourceResourceType: "Specimen", SourcePath: "id", Lossless: true, MLReady: true,
		StructuralSuitability: "scalar", Filterable: true, Chartable: true,
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: ws.Workspace{Documents: []ws.Document{document}},
		EmittedColumns: []viewer.EmittedColumn{source},
		OutputContracts: []receiptmodel.PublicOutputContract{{
			OutputID: outputID, RootResourceType: "Specimen", RowGrain: "groups", RowMultiplication: "none",
			Lossless: true, MLReady: true, StructuralSuitability: "scalar",
			Columns: []receiptmodel.PublicOutputColumn{publicOutputColumnFromEmission(source)},
		}},
		Presentations: []explorercompilation.PresentationConfig{{
			OutputID: outputID, Title: "Specimens",
			Columns: []explorercompilation.PresentationColumn{{
				EmissionID: source.EmissionID, PublicColumn: source.PublicColumn, Label: source.Label, Visible: true, Order: 0,
			}},
		}},
	}
	resolved := dataframeexecution.Resolved{
		Bundle: bundle,
		Compiled: facade.CompiledRecipe{Outputs: []facade.CompiledRecipeOutput{{
			Name: outputID,
			OutputSchema: []facade.CompiledOutputColumn{
				{Name: "group_revision_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
				{Name: "group_id", Kind: "string", Cardinality: "required_one", Identity: true},
				{Name: "group_label", SemanticPath: "groups.label", Kind: "string", Cardinality: "required_one"},
				{Name: "group_ordinal", SemanticPath: "groups.ordinal", Kind: "integer", Cardinality: "required_one"},
				{Name: "members", SemanticPath: "groups.members", Kind: "object", Cardinality: "many"},
				{Name: "__loom_row_id", Kind: "object", Cardinality: "required_one", Internal: true, Identity: true},
			},
		}}},
	}

	reconciled, err := ReconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatalf("reconcile explicit group row outputs: %v", err)
	}
	wantNames := []string{"group_label", "group_ordinal", "members"}
	if got := emittedPublicColumnNames(reconciled.EmittedColumns); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("explicit group emissions = %#v, want %#v", got, wantNames)
	}
	if got := publicContractColumnNames(reconciled.OutputContracts[0].Columns); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("explicit group contract columns = %#v, want %#v", got, wantNames)
	}
	if got := []string{reconciled.Presentations[0].Columns[0].Label, reconciled.Presentations[0].Columns[1].Label, reconciled.Presentations[0].Columns[2].Label}; !reflect.DeepEqual(got, []string{"Group label", "Group ordinal", "Members"}) {
		t.Fatalf("explicit group presentation labels = %#v", got)
	}
	for index, emitted := range reconciled.EmittedColumns {
		if emitted.EmissionID != explicitGroupEmissionID("grouprev_1", wantNames[index]) || emitted.ConstructionID != "" ||
			len(emitted.AuthoredColumns) != 0 || len(emitted.InputColumns) != 0 || emitted.SourceResourceType != "" || emitted.SourcePath != "" ||
			emitted.Lossless || emitted.MLReady || emitted.StructuralSuitability != "requires-review" ||
			!reflect.DeepEqual(emitted.LossReasons, []string{"TABLE_SHAPE_GROUP_CHANGES_ROW_GRAIN", TableShapeMLReadinessUnassessed}) {
			t.Fatalf("explicit group output metadata[%d] = %#v", index, emitted)
		}
	}
	if got := reconciled.EmittedColumns[2]; got.LogicalType != "object" || got.Cardinality != "many" || got.Nullable || got.Shape != "record_list" || got.Filterable || got.Chartable {
		t.Fatalf("explicit group members metadata = %#v", got)
	}
	if got := reconciled.EmittedColumns[0]; got.LogicalType != "string" || got.Cardinality != "required_one" || got.Nullable {
		t.Fatalf("explicit group label metadata = %#v", got)
	}
	if got := reconciled.EmittedColumns[1]; got.LogicalType != "integer" || got.Cardinality != "required_one" || got.Nullable {
		t.Fatalf("explicit group ordinal metadata = %#v", got)
	}
	if err := (receiptmodel.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(bundle, reconciled.EmittedColumns); err != nil {
		t.Fatalf("validate explicit group public output contract: %v", err)
	}

	compiledOutput := resolved.Compiled.Outputs[0]
	t.Run("member_field_order", func(t *testing.T) {
		withMember := translated
		withMember.Presentations = append([]explorercompilation.PresentationConfig(nil), translated.Presentations...)
		withMember.Presentations[0].Columns = append([]explorercompilation.PresentationColumn(nil), translated.Presentations[0].Columns...)
		withMember.Presentations[0].Columns[0].Order = 3
		memberResolved := resolved
		memberResolved.Compiled.Outputs = append([]facade.CompiledRecipeOutput(nil), resolved.Compiled.Outputs...)
		memberResolved.Compiled.Outputs[0].OutputSchema = append(append([]facade.CompiledOutputColumn(nil), compiledOutput.OutputSchema...), facade.CompiledOutputColumn{
			Name: source.PublicColumn, Kind: source.LogicalType, Cardinality: source.Cardinality, Nullable: source.Nullable,
		})
		got, err := ReconcileFinalOutputMetadata(withMember, memberResolved)
		if err != nil {
			t.Fatal(err)
		}
		var orders []int
		for _, column := range got.Presentations[0].Columns {
			orders = append(orders, column.Order)
		}
		if !reflect.DeepEqual(orders, []int{0, 1, 2, 3}) {
			t.Fatalf("cohort column order changed across reconciliation: got %v, want [0 1 2 3]", orders)
		}
	})
	compiledOutput.OutputSchema = append(append([]facade.CompiledOutputColumn(nil), compiledOutput.OutputSchema...), facade.CompiledOutputColumn{
		Name: "group_count", Kind: "integer", Cardinality: "required_one",
	})
	resolvedWithUnknownGroupField := resolved
	resolvedWithUnknownGroupField.Compiled.Outputs = []facade.CompiledRecipeOutput{compiledOutput}
	if _, err := ReconcileFinalOutputMetadata(translated, resolvedWithUnknownGroupField); err == nil || !strings.Contains(err.Error(), "no translated emission or authored table-shape identity") {
		t.Fatalf("unmapped compiler group output error = %v", err)
	}
}

func TestAuthoredOutputColumnsKeepsExactRelatedFieldScalarAndSource(t *testing.T) {
	patient := ws.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}
	status := ws.StageColumn{ID: "observation-status", Name: "observation_status", Label: "Observation status", Type: "string", Nullable: true}
	document := ws.Document{
		Output:  ws.Output{ID: "patients"},
		Columns: []ws.Column{{ColumnID: patient.ID, Column: patient.Name, Label: patient.Label}},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
			ID: "observation-status-step",
			Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedField, RelatedField: &ws.ConstructionRelatedField{
				ChoiceID: "choice", OutputColumnID: status.ID,
				Source: ws.ConstructionRelatedFieldSource{
					Kind: capability.ConstructionChoiceSourceField, CandidateID: "status-candidate", NodeID: "observation-node",
					ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
				},
			}},
			Outputs: []ws.StageColumn{patient, status},
		}}},
	}
	_, columns, err := authoredOutputColumns(ws.Workspace{Documents: []ws.Document{document}})
	if err != nil {
		t.Fatal(err)
	}
	field := columns["patients"][status.Name]
	if field.CandidateID != "status-candidate" || field.NodeID != "observation-node" || field.SourceResourceType != "Observation" || field.SourcePath != "status" {
		t.Fatalf("exact field source metadata = %#v", field)
	}
	profile, err := constructedOutputProfileFor(field.Quality, facade.CompiledOutputColumn{Name: status.Name, Kind: "string", Cardinality: "optional_one"})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Shape != "scalar" {
		t.Fatalf("exact field profile shape = %q, want scalar", profile.Shape)
	}
}

func TestReconcileFinalOutputMetadataUsesAuthoredReshapeOutputs(t *testing.T) {
	one := int64(1)
	bundle := recipe.Bundle{Outputs: []recipe.Output{{Name: "pivoted"}, {Name: "unpivoted"}}}
	pivoted := receipttest.StageFixture("pivoted", []ws.StageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient", Type: "string"}, {ID: "kind", Name: "kind", Label: "Kind", Type: "string"}, {ID: "value", Name: "value", Label: "Value", Type: "decimal"}})
	category := "systolic"
	pivoted.Construction.Steps = []ws.ConstructionStep{{ID: "pivot_vitals", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{ConstructionID: "pivot_vitals", GroupKeyIDs: []string{"patient_id"}, CategoryColumnID: "kind", ValueColumnID: "value", Categories: []ws.ConstructionPivotCategory{{Key: ws.TableScalar{Kind: "STRING", String: &category}, OutputColumnID: "systolic"}}, DuplicatePolicy: ws.ConstructionPivotDuplicateError, MissingCellPolicy: ws.ConstructionPivotMissingNull, UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError}}, Outputs: []ws.StageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient", Type: "string"}, {ID: "systolic", Name: "systolic", Label: "Systolic", Type: "decimal", Nullable: true}}}}
	receipttest.AppendDeriveFixture(&pivoted, "adjust_systolic", ws.StageColumn{ID: "adjusted_systolic", Name: "adjusted_systolic", Label: "Adjusted systolic", Type: "decimal", Nullable: true}, ws.ConstructionDerivedAdd, receipttest.ColumnOperand("systolic"), receipttest.IntegerOperand(&one), ws.ConstructionMissingInputError)
	unpivoted := receipttest.StageFixture("unpivoted", []ws.StageColumn{{ID: "height", Name: "height", Label: "Height", Type: "decimal"}, {ID: "weight", Name: "weight", Label: "Weight", Type: "decimal"}})
	height, weight := "height", "weight"
	unpivoted.Construction.Steps = []ws.ConstructionStep{{ID: "unpivot_vitals", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{ConstructionID: "unpivot_vitals", Inputs: []ws.ConstructionUnpivotInput{{ColumnID: "height", Key: ws.TableScalar{Kind: "STRING", String: &height}}, {ColumnID: "weight", Key: ws.TableScalar{Kind: "STRING", String: &weight}}}, KeyOutputColumnID: "measure_name", ValueOutputColumnID: "measure_value", NullRowPolicy: ws.ConstructionUnpivotDrop}}, Outputs: []ws.StageColumn{{ID: "measure_name", Name: "measure_name", Label: "Measure", Type: "string"}, {ID: "measure_value", Name: "measure_value", Label: "Value", Type: "decimal", Nullable: true}}}}
	workspace := receipttest.StageWorkspace(pivoted, unpivoted)

	emitted := []viewer.EmittedColumn{
		{EmissionID: "emit_patient_id", OutputID: "pivoted", AuthoredColumns: []string{"person_root", "shared_root"}, PublicColumn: "patient_id", Label: "Patient", LogicalType: "string", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_kind", OutputID: "pivoted", AuthoredColumns: []string{"category_root", "shared_root"}, PublicColumn: "kind", Label: "Kind", LogicalType: "string", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_value", OutputID: "pivoted", AuthoredColumns: []string{"result_root"}, PublicColumn: "value", Label: "Value", LogicalType: "decimal", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_height", OutputID: "unpivoted", AuthoredColumns: []string{"height_root"}, PublicColumn: "height", Label: "Height", LogicalType: "decimal", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_weight", OutputID: "unpivoted", AuthoredColumns: []string{"weight_root", "shared_root"}, PublicColumn: "weight", Label: "Weight", LogicalType: "decimal", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: workspace, EmittedColumns: emitted,
		OutputContracts: []receiptmodel.PublicOutputContract{
			{OutputID: "pivoted", Lossless: true, MLReady: true, StructuralSuitability: "scalar", Columns: []receiptmodel.PublicOutputColumn{receiptTestPublicColumn(emitted[0]), receiptTestPublicColumn(emitted[1]), receiptTestPublicColumn(emitted[2])}},
			{OutputID: "unpivoted", Lossless: true, MLReady: true, StructuralSuitability: "scalar", Columns: []receiptmodel.PublicOutputColumn{receiptTestPublicColumn(emitted[3]), receiptTestPublicColumn(emitted[4])}},
		},
		Presentations: []explorercompilation.PresentationConfig{
			{OutputID: "pivoted", Title: "Pivoted", Columns: []explorercompilation.PresentationColumn{
				{EmissionID: "emit_patient_id", PublicColumn: "patient_id", Label: "Patient", Visible: true, Order: 0},
				{EmissionID: "emit_kind", PublicColumn: "kind", Label: "Kind", Visible: true, Order: 1},
				{EmissionID: "emit_value", PublicColumn: "value", Label: "Value", Visible: true, Order: 2},
			}},
			{OutputID: "unpivoted", Title: "Unpivoted", Columns: []explorercompilation.PresentationColumn{
				{EmissionID: "emit_height", PublicColumn: "height", Label: "Height", Visible: true, Order: 0},
				{EmissionID: "emit_weight", PublicColumn: "weight", Label: "Weight", Visible: true, Order: 1},
			}},
		},
	}
	resolved := dataframeexecution.Resolved{Bundle: bundle, Compiled: facade.CompiledRecipe{Outputs: []facade.CompiledRecipeOutput{
		{Name: "pivoted", OutputSchema: []facade.CompiledOutputColumn{{Name: "patient_id", Kind: "string", Cardinality: "required_one"}, {Name: "systolic", Kind: "decimal", Cardinality: "optional_one", Nullable: true}, {Name: "adjusted_systolic", Kind: "decimal", Cardinality: "optional_one", Nullable: true}}},
		{Name: "unpivoted", OutputSchema: []facade.CompiledOutputColumn{{Name: "measure_name", Kind: "string", Cardinality: "required_one"}, {Name: "measure_value", Kind: "decimal", Cardinality: "optional_one", Nullable: true}}},
	}}}
	reconciled, err := ReconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatal(err)
	}
	want := []viewer.EmittedColumn{
		{EmissionID: "emit_patient_id", OutputID: "pivoted", AuthoredColumns: []string{"person_root", "shared_root"}, PublicColumn: "patient_id", Label: "Patient", LogicalType: "string", Cardinality: "required_one", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "construction:pivot_vitals:systolic", OutputID: "pivoted", AuthoredColumns: []string{"category_root", "person_root", "result_root", "shared_root"}, InputColumns: []string{"patient_id", "kind", "value"}, ConstructionID: "pivot_vitals", PublicColumn: "systolic", Label: "Systolic", LogicalType: "decimal", Cardinality: "optional_one", Nullable: true, Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_PIVOT_BASE_COLUMNS_DROPPED", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
		{EmissionID: "construction:adjust_systolic:adjusted_systolic", OutputID: "pivoted", AuthoredColumns: []string{"category_root", "person_root", "result_root", "shared_root"}, InputColumns: []string{"patient_id", "kind", "value"}, ConstructionID: "adjust_systolic", PublicColumn: "adjusted_systolic", Label: "Adjusted systolic", LogicalType: "decimal", Cardinality: "optional_one", Nullable: true, Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_DERIVED_ADD_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
		{EmissionID: "construction:unpivot_vitals:measure_name", OutputID: "unpivoted", AuthoredColumns: []string{"height_root", "shared_root", "weight_root"}, InputColumns: []string{"height", "weight"}, ConstructionID: "unpivot_vitals", PublicColumn: "measure_name", Label: "Measure", LogicalType: "string", Cardinality: "required_one", Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_UNPIVOT_NULL_ROWS_DROPPED", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
		{EmissionID: "construction:unpivot_vitals:measure_value", OutputID: "unpivoted", AuthoredColumns: []string{"height_root", "shared_root", "weight_root"}, InputColumns: []string{"height", "weight"}, ConstructionID: "unpivot_vitals", PublicColumn: "measure_value", Label: "Value", LogicalType: "decimal", Cardinality: "optional_one", Nullable: true, Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_UNPIVOT_NULL_ROWS_DROPPED", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
	}
	if len(reconciled.EmittedColumns) != len(want) {
		t.Fatalf("reconciled output column count = %d, want %d", len(reconciled.EmittedColumns), len(want))
	}
	for index, expected := range want {
		actual := reconciled.EmittedColumns[index]
		if !reflect.DeepEqual(actual, expected) {
			t.Errorf("reshape output %d = %#v, want %#v", index, actual, expected)
		}
	}
	if contract := reconciled.OutputContracts[0]; contract.Lossless || contract.MLReady || contract.StructuralSuitability != "requires-review" || !reflect.DeepEqual(contract.LossReasons, []string{"TABLE_SHAPE_PIVOT_BASE_COLUMNS_DROPPED", "TABLE_SHAPE_ML_READINESS_UNASSESSED", "TABLE_SHAPE_DERIVED_ADD_NON_LOSSLESS"}) {
		t.Errorf("pivot plus derived contract aggregation = %#v", contract)
	}
	if contract := reconciled.OutputContracts[1]; contract.Lossless || contract.MLReady || contract.StructuralSuitability != "requires-review" || !reflect.DeepEqual(contract.LossReasons, []string{"TABLE_SHAPE_UNPIVOT_NULL_ROWS_DROPPED", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}) {
		t.Errorf("lossy unpivot contract aggregation = %#v", contract)
	}
}

func reconciliationFixture() (explorercompilation.WorkspaceResult, dataframeexecution.Resolved) {
	document := receipttest.StageFixture("patients", []ws.StageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"}, {ID: "score", Name: "score", Label: "Authored score", Type: "decimal"}, {ID: "stale", Name: "stale", Label: "Stale", Type: "string"}})
	receipttest.AppendDeriveFixture(&document, "scale_score", ws.StageColumn{ID: "scaled_score", Name: "scaled_score", Label: "Scaled score", Type: "decimal", Nullable: true}, ws.ConstructionDerivedMultiply, receipttest.ColumnOperand("score"), receipttest.DecimalOperand(float64Pointer(2)), ws.ConstructionMissingInputPropagateNull)

	policy := &viewer.PublicUnitNormalization{Target: unit.UnitIdentity{System: "urn:test:source", Code: "source-unit"}, Rules: []viewer.PublicUnitRuleIdentity{{ID: "source-to-target", Version: "1"}}}
	emitted := []viewer.EmittedColumn{
		{EmissionID: "emit_patient_id", OutputID: "patients", NodeID: "node-patient", CandidateID: "candidate-id", AuthoredColumns: []string{"patient_id"}, PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string", Nullable: true, Shape: "scalar", SourceResourceType: "Patient", SourcePath: "id", Lossless: true, MLReady: true, StructuralSuitability: "scalar", Filterable: true, Chartable: true},
		{EmissionID: "emit_score", OutputID: "patients", NodeID: "node-observation", CandidateID: "candidate-score", AuthoredColumns: []string{"score"}, PublicColumn: "score", Label: "Authored score", LogicalType: "string", Nullable: true, Shape: "scalar", SourceResourceType: "Observation", SourcePath: "valueQuantity.value", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"source-loss"}, Filterable: true, Chartable: true, UnitNormalization: policy},
		{EmissionID: "emit_stale", OutputID: "patients", NodeID: "node-observation", CandidateID: "candidate-stale", AuthoredColumns: []string{"stale"}, PublicColumn: "stale", Label: "Stale", LogicalType: "string", Nullable: true, Shape: "scalar", SourceResourceType: "Observation", SourcePath: "value", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"dropped-loss"}},
	}
	contractColumns := []receiptmodel.PublicOutputColumn{
		receiptTestPublicColumn(emitted[0]), receiptTestPublicColumn(emitted[1]), receiptTestPublicColumn(emitted[2]),
	}
	bundle := recipe.Bundle{Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}}}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, EmittedColumns: emitted,
		OutputContracts: []receiptmodel.PublicOutputContract{{OutputID: "patients", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"source-loss", "dropped-loss"}, Columns: contractColumns}},
		Presentations: []explorercompilation.PresentationConfig{{OutputID: "patients", Title: "Patients", Columns: []explorercompilation.PresentationColumn{
			{EmissionID: "emit_patient_id", PublicColumn: "patient_id", Label: "Patient ID", Visible: true, Order: 0, PhysicalOrder: 0},
			{EmissionID: "emit_score", PublicColumn: "score", Label: "Authored score", Visible: true, Order: 1, PhysicalOrder: 1},
			{EmissionID: "emit_stale", PublicColumn: "stale", Label: "Stale", Visible: true, Order: 50, PhysicalOrder: 2},
		}}},
		IdentityMappings: []receiptmodel.IdentityMapping{{OutputID: "patients", CandidateID: "candidate", EmissionIDs: []string{"emit_patient_id", "emit_score", "emit_stale"}}},
		Workspace:        receipttest.StageWorkspace(document),
	}
	resolved := dataframeexecution.Resolved{
		Bundle: bundle,
		Compiled: facade.CompiledRecipe{Outputs: []facade.CompiledRecipeOutput{{Name: "patients", OutputSchema: []facade.CompiledOutputColumn{
			{Name: "patient_id", SemanticPath: "Patient.id", Kind: "string", Cardinality: "required_one", Nullable: false},
			{Name: "score", SemanticPath: "Observation.score", Kind: "decimal", Cardinality: "required_one", Nullable: false, NormalizedUnit: &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}},
			{Name: "scaled_score", SemanticPath: "derived:scale_score", Kind: "decimal", Cardinality: "optional_one", Nullable: true, NormalizedUnit: &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}},
			{Name: "internal_identity", SemanticPath: "internal:identity", Kind: "string", Cardinality: "required_one", Identity: true},
			{Name: "__loom_row_id", SemanticPath: "internal:row", Kind: "object", Cardinality: "required_one", Internal: true, Identity: true},
		}}}},
	}
	return translated, resolved
}

func receiptTestPublicColumn(emitted viewer.EmittedColumn) receiptmodel.PublicOutputColumn {
	return receiptmodel.PublicOutputColumn{
		Column: emitted.PublicColumn, AuthoredColumns: append([]string(nil), emitted.AuthoredColumns...), InputColumns: append([]string(nil), emitted.InputColumns...),
		Label: emitted.Label, LogicalType: emitted.LogicalType, Nullable: emitted.Nullable, Shape: emitted.Shape,
		SourceResourceType: emitted.SourceResourceType, SourcePath: emitted.SourcePath,
		Lossless: emitted.Lossless, MLReady: emitted.MLReady, StructuralSuitability: emitted.StructuralSuitability,
		LossReasons: append([]string(nil), emitted.LossReasons...), Filterable: emitted.Filterable, Chartable: emitted.Chartable,
		UnitNormalization: emitted.UnitNormalization,
	}
}

func emittedPublicColumnNames(values []viewer.EmittedColumn) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.PublicColumn)
	}
	return names
}

func publicContractColumnNames(values []receiptmodel.PublicOutputColumn) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Column)
	}
	return names
}

func float64Pointer(value float64) *float64 { return &value }

func unpivotSourceReconcileFixture(t *testing.T) (ws.Document, facade.CompiledRecipeOutput, capability.Snapshot, ws.ConstructionSourceProjection) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_birth", NodeID: "n_patient", ResourceType: "Patient",
		FieldPath: "multipleBirthInteger", Label: "Birth count", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 135,
	})
	facts, ok := lifecycle.ConstructionUnpivotSourceFacts(snapshot, "Patient", "multipleBirthInteger")
	if !ok {
		t.Fatal("the authorized snapshot does not admit Patient.multipleBirthInteger as an unpivot source")
	}
	projection := facts
	projection.ColumnID = "unpivot-col-birth"
	projection.OwnerStepID = "melt_step"
	projectionName := ws.ConstructionSourceProjectionName(projection.ColumnID)
	keyScalar := func(value string) ws.TableScalar {
		kind := ws.TableScalarString
		return ws.TableScalar{Kind: kind, String: &value}
	}
	document := ws.Document{
		Output:           ws.Output{ID: "patients"},
		RootResourceType: "Patient",
		Columns:          []ws.Column{{ColumnID: "patient-col", Column: "patient_id"}},
		Construction: &ws.Construction{
			Version:           ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{projection},
			Steps: []ws.ConstructionStep{
				{
					ID: "derive_step",
					Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
						ConstructionID: "derive_step", OutputColumnID: "derived-total", Operation: ws.ConstructionDerivedAdd,
						Left:               ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger}},
						Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger}},
						MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
					}},
					Outputs: []ws.StageColumn{
						{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
						{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, Type: projection.LogicalType},
						{ID: "derived-total", Name: "derived_total", Label: "Derived total", Type: "integer"},
					},
				},
				{
					ID: "melt_step",
					Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
						ConstructionID:      "melt_step",
						Inputs:              []ws.ConstructionUnpivotInput{{ColumnID: projection.ColumnID, Key: keyScalar("variant-a")}, {ColumnID: "derived-total", Key: keyScalar("variant-b")}},
						KeyOutputColumnID:   "unpivot-key",
						ValueOutputColumnID: "unpivot-value",
						NullRowPolicy:       ws.ConstructionUnpivotDrop,
					}},
					Outputs: []ws.StageColumn{
						{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
						{ID: "unpivot-key", Name: "unpivot_key", Label: "Variant", Type: "string"},
						{ID: "unpivot-value", Name: "unpivot_value", Label: "Value", Type: "integer"},
					},
				},
			},
		},
	}
	compiled := facade.CompiledRecipeOutput{Stages: []facade.CompiledStageDescriptor{{
		ID: recipe.ConstructionSourceProjectionID,
		Columns: []facade.CompiledOutputColumn{
			{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Kind: "string", Cardinality: "optional_one"},
			{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, SemanticPath: projection.FieldPath, Kind: projection.LogicalType, Cardinality: "optional_one"},
		},
	}}}
	return document, compiled, snapshot, projection
}

func TestUnpivotSourceMeltClaimsUnselectedFieldQualityWithoutAuthoringTheSlot(t *testing.T) {
	document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
	authored := make(map[string]AuthoredOutputColumn)
	if err := AuthoredConstructionOutputsWithCombineInputs(document, authored, compiled, nil, snapshot); err != nil {
		t.Fatalf("reconcile unpivot-owned projection: %v", err)
	}
	slotName := ws.ConstructionSourceProjectionName(projection.ColumnID)
	if _, claimed := authored[slotName]; claimed {
		t.Fatalf("a consumed melt slot was claimed as an authored output: %#v", authored[slotName])
	}
	for _, name := range []string{"unpivot_key", "unpivot_value"} {
		claimed, ok := authored[name]
		if !ok {
			t.Fatalf("melt output %q missing from the ledger: %#v", name, authored)
		}
		found := false
		for _, reason := range claimed.Quality.LossReasons {
			if reason == "CONSTRUCTION_UNPIVOT_SOURCE_ROWS_UNPIVOTED_FROM_UNSELECTED_FIELD" {
				found = true
			}
		}
		if !found {
			t.Fatalf("melt output %q omits the unselected-field melt reason: %v", name, claimed.Quality.LossReasons)
		}
	}
}

func TestUnpivotSourceProjectionReconcileRefusesDriftedClaims(t *testing.T) {
	t.Run("compiled slot no longer matches the durable claim", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		compiled.Stages[0].Columns[1].SemanticPath = "active"
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "does not carry the compiled source column identity") {
			t.Fatalf("drifted compiled slot error = %v", err)
		}
	})

	t.Run("claim needs an authorized capability snapshot", func(t *testing.T) {
		document, compiled, _, _ := unpivotSourceReconcileFixture(t)
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, capability.Snapshot{})
		if err == nil || !strings.Contains(err.Error(), "without an authorized capability snapshot") {
			t.Fatalf("unarmed snapshot error = %v", err)
		}
	})

	t.Run("claim no longer matches the authorized derivation", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].FieldPath = "gender"
		compiled.Stages[0].Columns[1].SemanticPath = "gender"
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("drifted claim error = %v", err)
		}
	})

	t.Run("claim label drifted while the compiled slot mirrors it", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].Label = "TAMPERED-LABEL"
		compiled.Stages[0].Columns[1].Label = document.Construction.SourceProjections[0].Label
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("mirrored tampered label error = %v, want the facts-branch refusal", err)
		}
	})

	t.Run("claim fhir type drifted from the rederived schema facts", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].FHIRType = "TAMPERED-TYPE"
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("tampered fhirType error = %v, want the facts-branch refusal", err)
		}
	})

	t.Run("owner step must melt the projection as an input", func(t *testing.T) {
		document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
		for index := range document.Construction.Steps[1].Operation.Unpivot.Inputs {
			if document.Construction.Steps[1].Operation.Unpivot.Inputs[index].ColumnID == projection.ColumnID {
				document.Construction.Steps[1].Operation.Unpivot.Inputs[index].ColumnID = "derived-total"
			}
		}
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "it does not melt as an input") {
			t.Fatalf("unconsumed melt input error = %v", err)
		}
	})

	t.Run("carrier step must declare the slot it carries", func(t *testing.T) {
		document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
		carrier := &document.Construction.Steps[0]
		kept := make([]ws.StageColumn, 0, len(carrier.Outputs))
		for _, output := range carrier.Outputs {
			if output.ID != projection.ColumnID {
				kept = append(kept, output)
			}
		}
		carrier.Outputs = kept
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "missing a declared stable ID, name, or label") {
			t.Fatalf("missing carrier declaration error = %v", err)
		}
	})
}

func unpivotMeltFirstReconcileFixture(t *testing.T) (ws.Document, facade.CompiledRecipeOutput, capability.Snapshot) {
	t.Helper()
	base, _, snapshot, projection := unpivotSourceReconcileFixture(t)
	projectionName := ws.ConstructionSourceProjectionName(projection.ColumnID)
	keyScalar := func(value string) ws.TableScalar {
		kind := ws.TableScalarString
		return ws.TableScalar{Kind: kind, String: &value}
	}
	document := ws.Document{
		Output:           ws.Output{ID: "patients"},
		RootResourceType: "Patient",
		Columns: []ws.Column{
			{ColumnID: "patient-col", Column: "patient_id"},
			{ColumnID: "note-col", Column: "note"},
		},
		Construction: &ws.Construction{
			Version:           ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{projection},
			Steps: []ws.ConstructionStep{{
				ID:     "melt_step",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
					ConstructionID:      "melt_step",
					Inputs:              []ws.ConstructionUnpivotInput{{ColumnID: projection.ColumnID, Key: keyScalar("variant-a")}, {ColumnID: "note-col", Key: keyScalar("variant-b")}},
					KeyOutputColumnID:   "unpivot-key",
					ValueOutputColumnID: "unpivot-value",
					NullRowPolicy:       ws.ConstructionUnpivotDrop,
				}},
				Outputs: []ws.StageColumn{
					{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: "unpivot-key", Name: "unpivot_key", Label: "Variant", Type: "string"},
					{ID: "unpivot-value", Name: "unpivot_value", Label: "Value", Type: "integer"},
				},
			}},
		},
	}
	compiled := facade.CompiledRecipeOutput{Stages: []facade.CompiledStageDescriptor{{
		ID: recipe.ConstructionSourceProjectionID,
		Columns: []facade.CompiledOutputColumn{
			{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Kind: "string", Cardinality: "optional_one"},
			{ID: "note-col", Name: "note", Label: "Note", Kind: "string", Cardinality: "optional_one"},
			{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, SemanticPath: projection.FieldPath, Kind: projection.LogicalType, Cardinality: "optional_one"},
		},
	}}}
	_ = base
	return document, compiled, snapshot
}

func TestUnpivotSourceProjectionReconcileMeltFirst(t *testing.T) {
	t.Run("compiler-materialized first-step slot reconciles", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		if err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot); err != nil {
			t.Fatalf("legit melt-first signed state was refused (pre-fix this is the F-A catch-all): %v", err)
		}
	})

	t.Run("compiled slot identity drift still falls to the declaration refusal", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		compiled.Stages[0].Columns[2].Label = "DRIFTED-COMPILED-LABEL"
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "missing a declared stable ID, name, or label") {
			t.Fatalf("drifted compiled slot label error = %v, want the slot-equality-gated declaration refusal", err)
		}
	})

	t.Run("compiled slot marked identity stays unclaimable", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		compiled.Stages[0].Columns[2].Identity = true
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "missing a declared stable ID, name, or label") {
			t.Fatalf("identity-flagged compiled slot error = %v, want the slot-equality-gated declaration refusal", err)
		}
	})

	t.Run("claim label drift still dies on the facts predicate", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		document.Construction.SourceProjections[0].Label = "TAMPERED-LABEL"
		compiled.Stages[0].Columns[2].Label = document.Construction.SourceProjections[0].Label
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("melt-first mirrored tampered label error = %v, want the facts-branch refusal", err)
		}
	})

	t.Run("melt-first still requires an authorized capability snapshot", func(t *testing.T) {
		document, compiled, _ := unpivotMeltFirstReconcileFixture(t)
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, capability.Snapshot{})
		if err == nil || !strings.Contains(err.Error(), "cannot be claimed without an authorized capability snapshot") {
			t.Fatalf("melt-first unarmed snapshot error = %v, want the authorized-snapshot refusal", err)
		}
	})
}

const filterSourceF1Column = "col_reconcile_integer"

const filterSourceF1HonestField = "multipleBirthInteger"

const filterSourceF1OwnerStep = "only_step"

func filterSourceF1Snapshot() capability.Snapshot {
	snapshot := capabilityfixtures.AuthoringV2CapabilitySnapshot(capabilityresolver.ScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}))
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "c_patient_active", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "active", Label: "Active", LogicalType: "boolean", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
		capability.Candidate{
			ID: "c_patient_multiple_birth_integer", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: filterSourceF1HonestField, Label: "Multiple birth integer", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
	)
	return snapshot
}

func filterSourceF1HonestProjection() ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: filterSourceF1Column, OwnerStepID: filterSourceF1OwnerStep,
		OccurrenceID: ws.RootOccurrenceID, FieldPath: filterSourceF1HonestField,
		FHIRType: "integer", LogicalType: "integer", Label: "Multiple birth integer",
	}
}

func filterSourceF1Workspace(t *testing.T, snapshot capability.Snapshot, projection ws.ConstructionSourceProjection) ws.Workspace {
	t.Helper()
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	columns = append(columns, ws.StageColumn{
		ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID),
		Label: projection.Label, Type: projection.LogicalType,
	})
	document.Construction.Steps = []ws.ConstructionStep{{
		ID:     filterSourceF1OwnerStep,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind: ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{
				ColumnID: projection.ColumnID, Operator: ws.ConstructionFilterEquals,
				Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: filterSourceHTTPBool(true)}},
			},
		},
		Outputs: columns,
	}}
	document.Construction.SourceProjections = []ws.ConstructionSourceProjection{projection}
	workspace.Documents[0] = document
	catalog := ws.CatalogFromCapability(snapshot, "custom")
	workspace, err = ws.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate durable filter-source workspace: %v", err)
	}
	return workspace
}

func filterSourceHTTPBool(value bool) *bool { return &value }

func filterSourceF1Compile(t *testing.T, snapshot capability.Snapshot) (ws.Workspace, dataframeexecution.Resolved) {
	t.Helper()
	workspace := filterSourceF1Workspace(t, snapshot, filterSourceF1HonestProjection())
	translated, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "custom", workspace, snapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatalf("compile honest filter-source plan: %v", err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), translated.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
	if err != nil {
		t.Fatalf("compile honest filter-source recipe: %v", err)
	}
	return workspace, resolved
}

func filterSourceF1Refuse(t *testing.T, tampered ws.ConstructionSourceProjection, want string) {
	t.Helper()
	snapshot := filterSourceF1Snapshot()
	_, resolved := filterSourceF1Compile(t, snapshot)
	workspace := filterSourceF1Workspace(t, snapshot, tampered)
	honest := filterSourceF1HonestProjection()
	if tampered.ColumnID != honest.ColumnID || tampered.OwnerStepID != honest.OwnerStepID {
		t.Fatal("tampered probe must keep honest owner and column identity")
	}
	compiledByOutput := map[string]facade.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	_, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, snapshot)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("reconcile FILTER branch wrongly accepted tampered projection %s: err=%v want=%q", tampered.FieldPath, err, want)
	}
}

func TestConstructionFilterSourceReconcileRefusesFieldPathSwap(t *testing.T) {
	tampered := filterSourceF1HonestProjection()
	tampered.FieldPath = "active"
	tampered.FHIRType = "boolean"
	tampered.LogicalType = "boolean"
	tampered.Label = "Patient.active"
	filterSourceF1Refuse(t, tampered, "compiled source column identity")
}

func TestConstructionFilterSourceReconcileRefusesLogicalTypeFlip(t *testing.T) {
	tampered := filterSourceF1HonestProjection()
	tampered.LogicalType = "boolean"
	filterSourceF1Refuse(t, tampered, "compiled source column identity")
}

func TestConstructionFilterSourceReconcileClaimsHonestProjection(t *testing.T) {
	snapshot := filterSourceF1Snapshot()
	workspace, resolved := filterSourceF1Compile(t, snapshot)
	compiledByOutput := map[string]facade.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	if _, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, snapshot); err != nil {
		t.Fatalf("reconcile FILTER branch refused the honest projection: %v", err)
	}
}

func TestConstructionFilterSourceReconcileClaimsOnlyWithAuthorizedSnapshot(t *testing.T) {
	armedSnapshot := filterSourceF1Snapshot()
	workspace, resolved := filterSourceF1Compile(t, armedSnapshot)
	compiledByOutput := map[string]facade.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	stale := filterSourceF1Snapshot()
	for index := range stale.Candidates {
		if stale.Candidates[index].ID == "c_patient_multiple_birth_integer" {
			stale.Candidates[index].Populated = false
		}
	}
	blankToken := stale
	blankToken.Token = ""

	_, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, blankToken)
	if err == nil || !strings.Contains(err.Error(), "without an authorized capability snapshot") {
		t.Fatalf("unarmed reconcile wrongly published an unpopulated filter-owned claim: err=%v", err)
	}
	if _, _, err := AuthoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, stale); err == nil || !strings.Contains(err.Error(), "authorized filter source derivation") {
		t.Fatalf("armed reconcile wrongly published the same unpopulated claim: err=%v", err)
	}
}

func TestAuthoredSemanticGroupOutputCanBeRemovedByDownstreamUnpivot(t *testing.T) {
	semanticPath := "construction_semantic_group:binding_specimen_type:family_specimen_type"
	measureName := "specimen_type"
	category := &ws.ConstructionGroupSemanticCategory{
		Kind: "BINDING", BindingID: "binding_specimen_type", FamilyID: "family_specimen_type",
		ResourceType: "Specimen", CodingPath: "type.coding[]",
	}
	document := ws.Document{
		Output:  ws.Output{ID: "specimens"},
		Columns: []ws.Column{{Column: "subject_reference", ColumnID: "source_subject_reference"}},
		Construction: &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
			{
				ID: "semantic_group",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
					ConstructionID: "semantic_group", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
					Keys:       []ws.ConstructionGroupKey{{OutputColumnID: "specimen_type_group", SemanticCategory: category}},
					Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row_count"}},
				}},
				Outputs: []ws.StageColumn{
					{ID: "specimen_type_group", Name: "specimen_type_group", Label: "Specimen type", Type: "string"},
					{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
				},
			},
			{
				ID: "unpivot_semantic_key", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
					ConstructionID:    "unpivot_semantic_key",
					Inputs:            []ws.ConstructionUnpivotInput{{ColumnID: "specimen_type_group", Key: ws.TableScalar{Kind: ws.TableScalarString, String: &measureName}}},
					KeyOutputColumnID: "measure_key", ValueOutputColumnID: "measure_value", NullRowPolicy: ws.ConstructionUnpivotPreserve,
				}},
				Outputs: []ws.StageColumn{
					{ID: "row_count", Name: "row_count", Label: "Count", Type: "integer"},
					{ID: "measure_key", Name: "measure", Label: "Measure", Type: "string"},
					{ID: "measure_value", Name: "measure_value", Label: "Measure value", Type: "string"},
				},
			},
		}},
	}
	compiled := facade.CompiledRecipeOutput{
		OutputSchema: []facade.CompiledOutputColumn{
			{ID: "row_count", Name: "row_count", Kind: "integer", Cardinality: "required_one"},
			{ID: "measure_key", Name: "measure", Kind: "string", Cardinality: "required_one"},
			{ID: "measure_value", Name: "measure_value", Kind: "string", Cardinality: "required_one"},
		},
		Stages: []facade.CompiledStageDescriptor{
			{ID: "semantic_group", Operation: "GROUP", Columns: []facade.CompiledOutputColumn{{ID: "specimen_type_group", Name: "specimen_type_group", SemanticPath: semanticPath}}},
			{ID: "unpivot_semantic_key", Operation: "UNPIVOT"},
		},
	}
	if err := AuthoredConstructionOutputsWithCombineInputs(document, map[string]AuthoredOutputColumn{}, compiled, nil, capability.Snapshot{}); err != nil {
		t.Fatalf("reconcile semantic Group key removed by downstream UNPIVOT: %v", err)
	}
}
