package receipt

import (
	"errors"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"reflect"
	"strings"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
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
	if !reflect.DeepEqual(reconciled.IdentityMappings, []explorer.IdentityMapping{{OutputID: "patients", CandidateID: "candidate", EmissionIDs: []string{"emit_patient_id", "emit_score"}}}) {
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
	wantSourcePolicy := &explorer.PublicUnitNormalization{Target: unit.UnitIdentity{System: "urn:test:source", Code: "source-unit"}, Rules: []explorer.PublicUnitRuleIdentity{{ID: "source-to-target", Version: "1"}}}
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
	if err := (explorer.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(reconciled.Bundle, reconciled.EmittedColumns); err != nil {
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
	if err := (explorer.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(reconciled.Bundle, reconciled.EmittedColumns); err != nil {
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
	document := receipttest.StageFixture("patients", []authoringv2.StageColumn{{ID: "score", Name: "score", Label: "Score", Type: "decimal"}, {ID: "patient_count", Name: "patient_count", Label: "Patient count", Type: "integer"}})
	receipttest.AppendDeriveFixture(&document, "same_input", authoringv2.StageColumn{ID: "same_result", Name: "same_result", Label: "Same result", Type: "decimal"}, authoringv2.ConstructionDerivedAdd, receipttest.ColumnOperand("score"), receipttest.ColumnOperand("score"), authoringv2.ConstructionMissingInputError)
	receipttest.AppendDeriveFixture(&document, "ordered_inputs", authoringv2.StageColumn{ID: "ordered_result", Name: "ordered_result", Label: "Ordered result", Type: "decimal"}, authoringv2.ConstructionDerivedSubtract, receipttest.ColumnOperand("score"), receipttest.ColumnOperand("patient_count"), authoringv2.ConstructionMissingInputError)
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
		rowValuePolicy authoringv2.ConstructionRowValuePolicy
		kind           string
		cardinality    string
		shape          string
	}{
		{name: "list", form: capability.ConstructionChoiceAll, kind: "string", cardinality: "many", shape: "array"},
		{name: "one distinct value", form: capability.ConstructionChoiceAll, rowValuePolicy: authoringv2.ConstructionRowValueOne, kind: "string", cardinality: "optional_one", shape: "scalar"},
		{name: "count", form: capability.ConstructionChoiceCount, kind: "integer", cardinality: "required_one", shape: "scalar"},
		{name: "presence", form: capability.ConstructionChoicePresence, kind: "boolean", cardinality: "required_one", shape: "scalar"},
	} {
		t.Run(test.name, func(t *testing.T) {
			const columnName = "related_observation"
			document := authoringv2.Document{
				Output: authoringv2.Output{ID: "patients"},
				Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
					ID: "observation-step",
					Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedSource, RelatedSource: &authoringv2.ConstructionRelatedSource{
						Form: test.form, RowValuePolicy: test.rowValuePolicy, OutputColumnID: "observation-column",
						ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
					}},
					Outputs: []authoringv2.StageColumn{{ID: "observation-column", Name: columnName, Label: "Related observation", Type: test.kind}},
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
	document := authoringv2.Document{
		Output: authoringv2.Output{ID: "specimens"},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: "coded-pivot-step",
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCodedPivot, CodedPivot: &authoringv2.ConstructionCodedPivot{
				ConstructionID: "coded-pivot-step",
				Source: &authoringv2.ConstructionCodedPivotSource{
					Family:      capability.SemanticFrameFamily{ResourceType: "Specimen", ValuePath: "type.coding[].display"},
					CandidateID: "candidate-id", NodeID: "node-id",
				},
				Categories:      []authoringv2.ConstructionCodedPivotCategory{{System: "system", Code: "code", OutputColumnID: "category-column"}},
				DuplicatePolicy: authoringv2.ConstructionPivotDuplicateError, MissingCellPolicy: authoringv2.ConstructionPivotMissingNull,
			}},
			Outputs: []authoringv2.StageColumn{{ID: "category-column", Name: "specimen_type", Label: "Specimen type", Type: "string"}},
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
	document := authoringv2.Document{Construction: &authoringv2.Construction{
		SourceProjections: []authoringv2.ConstructionSourceProjection{{ColumnID: "specimen_ref", OwnerStepID: "pivot"}},
		Steps: []authoringv2.ConstructionStep{{
			ID: "pivot",
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
				ConstructionID: "pivot", GroupKeyIDs: []string{"specimen_ref"},
				DuplicatePolicy:        authoringv2.ConstructionPivotDuplicateError,
				MissingCellPolicy:      authoringv2.ConstructionPivotMissingNull,
				UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
			}},
			Outputs: []authoringv2.StageColumn{{ID: "specimen_ref", Name: "specimen_reference", Label: "Specimen reference", Type: "string"}},
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
	document.Construction.Steps = append([]authoringv2.ConstructionStep{{
		ID: "input", OwnerStepID: "pivot",
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
			OutputColumnID: "specimen_ref", Source: authoringv2.ConstructionRelatedFieldSource{NodeID: "observation", CandidateID: "specimen-reference"},
		}},
		Outputs: []authoringv2.StageColumn{{ID: "specimen_ref", Name: "__pivot_source_reference", Label: "Observation.specimen.reference", Type: "string"}},
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
	group := authoringv2.StageColumn{ID: "group-id", Name: "group", Label: "Group", Type: "string"}
	category := authoringv2.StageColumn{ID: "category-id", Name: "category", Label: "Category", Type: "string"}
	value := authoringv2.StageColumn{ID: "value-id", Name: "value", Label: "Value", Type: "integer"}
	pivotValue := authoringv2.StageColumn{ID: "pivot-value-id", Name: "active_value", Label: "Active value", Type: "integer"}
	derivedValue := authoringv2.StageColumn{ID: "derived-value-id", Name: "adjusted_value", Label: "Adjusted value", Type: "integer"}
	measureKey := authoringv2.StageColumn{ID: "measure-key-id", Name: "measure", Label: "Measure", Type: "string"}
	measureValue := authoringv2.StageColumn{ID: "measure-value-id", Name: "measure_value", Label: "Measure value", Type: "integer"}
	one := int64(1)
	active := "active"
	document := authoringv2.Document{
		Output: authoringv2.Output{ID: "patients"},
		Columns: []authoringv2.Column{
			{ColumnID: group.ID, Column: group.Name, Label: group.Label},
			{ColumnID: category.ID, Column: category.Name, Label: category.Label},
			{ColumnID: value.ID, Column: value.Name, Label: value.Label},
		},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
			{
				ID: "pivot-step",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{
					ConstructionID: "pivot-step", GroupKeyIDs: []string{group.ID}, CategoryColumnID: category.ID, ValueColumnID: value.ID,
					Categories:      []authoringv2.ConstructionPivotCategory{{Key: authoringv2.TableScalar{Kind: "STRING", String: &active}, OutputColumnID: pivotValue.ID}},
					DuplicatePolicy: authoringv2.ConstructionPivotDuplicateError, MissingCellPolicy: authoringv2.ConstructionPivotMissingNull,
					UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError,
				}},
				Outputs: []authoringv2.StageColumn{group, pivotValue},
			},
			{
				ID: "derive-step",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
					ConstructionID: "derive-step", OutputColumnID: derivedValue.ID, Operation: authoringv2.ConstructionDerivedAdd,
					Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: pivotValue.ID},
					Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &one}},
					MissingInputPolicy: authoringv2.ConstructionMissingInputError,
				}},
				Outputs: []authoringv2.StageColumn{group, pivotValue, derivedValue},
			},
			{
				ID:        "filter-step",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: derivedValue.ID, Operator: authoringv2.ConstructionFilterExists}},
				Outputs:   []authoringv2.StageColumn{group, pivotValue, derivedValue},
			},
			{
				ID: "unpivot-step",
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID: "unpivot-step", Inputs: []authoringv2.ConstructionUnpivotInput{{ColumnID: pivotValue.ID, Key: authoringv2.TableScalar{Kind: "STRING", String: &active}}, {ColumnID: derivedValue.ID, Key: authoringv2.TableScalar{Kind: "STRING", String: &active}}},
					KeyOutputColumnID: measureKey.ID, ValueOutputColumnID: measureValue.ID, NullRowPolicy: authoringv2.ConstructionUnpivotPreserve,
				}},
				Outputs: []authoringv2.StageColumn{group, measureKey, measureValue},
			},
		}},
	}
	_, columns, err := authoredOutputColumns(authoringv2.Workspace{Documents: []authoringv2.Document{document}})
	if err != nil {
		t.Fatal(err)
	}
	final := columns["patients"]
	if len(final) != 2 {
		t.Fatalf("final constructed columns = %#v, want only measure and measure_value", final)
	}
	emitted := map[string]explorer.EmittedColumn{
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
	source := explorer.EmittedColumn{
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
	groupOutput := authoringv2.StageColumn{ID: "grouped-system", Name: columnName, Label: "System", Type: "string", Nullable: true}
	sourceContractColumn := receiptTestPublicColumn(source)
	sourceContractColumn.Cardinality = source.Cardinality
	document := authoringv2.Document{
		Output:  authoringv2.Output{ID: "body-structures", Title: "Body structures"},
		Columns: []authoringv2.Column{{ColumnID: "source-system-id", Column: columnName, Label: "System"}},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: "group-step",
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
				ConstructionID: "group-by-system",
				Keys:           []authoringv2.ConstructionGroupKey{{InputColumnID: "source-system-id", OutputColumnID: groupOutput.ID}},
			}},
			Outputs: []authoringv2.StageColumn{groupOutput},
		}}},
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: authoringv2.Workspace{Documents: []authoringv2.Document{document}},
		EmittedColumns: []explorer.EmittedColumn{source},
		OutputContracts: []explorer.PublicOutputContract{{
			OutputID: "body-structures", RootResourceType: "BodyStructure", RowGrain: "groups",
			Lossless: true, MLReady: false, StructuralSuitability: "scalar",
			Columns: []explorer.PublicOutputColumn{sourceContractColumn},
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
	groupOutputs := []authoringv2.StageColumn{
		{ID: keyOutputID, Name: keyOutputName, Label: "Whether this record is in active use", Type: "boolean", Nullable: true},
		{ID: countOutputID, Name: countOutputName, Label: "Record count", Type: "integer"},
	}
	bundle := recipe.Bundle{Outputs: []recipe.Output{{Name: "body-structures", RootResourceType: "BodyStructure", RowGrain: "groups"}}}
	document := authoringv2.Document{
		Output: authoringv2.Output{ID: "body-structures", Title: "Body structures"},
		Construction: &authoringv2.Construction{
			Version: authoringv2.ConstructionVersion,
			SourceProjections: []authoringv2.ConstructionSourceProjection{{
				ColumnID: inputID, OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: "active",
				FHIRType: "boolean", LogicalType: "boolean", Label: "Whether this record is in active use",
			}},
			Steps: []authoringv2.ConstructionStep{{
				ID: "group_by_active", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
					ConstructionID: "group_by_active",
					Keys:           []authoringv2.ConstructionGroupKey{{InputColumnID: inputID, OutputColumnID: keyOutputID}},
					Aggregates:     []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: countOutputID}},
				}},
				Outputs: groupOutputs,
			}},
		},
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle:    bundle,
		Workspace: authoringv2.Workspace{Documents: []authoringv2.Document{document}},
		OutputContracts: []explorer.PublicOutputContract{{
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
			emitted.PublicColumn == inputID || emitted.PublicColumn == authoringv2.ConstructionSourceProjectionName(inputID) {
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
	source := explorer.EmittedColumn{
		EmissionID: "source-gender", OutputID: outputID, AuthoredColumns: []string{"gender"},
		PublicColumn: "gender", Label: "Gender", LogicalType: "string", Cardinality: "optional_one",
		Nullable: true, Shape: "scalar", SourceResourceType: "Patient", SourcePath: "gender",
		Lossless: true, MLReady: true, StructuralSuitability: "scalar", Filterable: true, Chartable: true,
	}
	bundle := recipe.Bundle{Outputs: []recipe.Output{{
		Name: outputID, RootResourceType: "Patient", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: groupRevision, UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED"},
	}}}
	groupLabel := authoringv2.StageColumn{ID: "group-label-output", Name: "group_label", Label: "Group label", Type: "string"}
	genderKey := authoringv2.StageColumn{ID: "gender-key-output", Name: genderItem, Label: "Gender item", Type: "string", Nullable: true}
	rowCount := authoringv2.StageColumn{ID: "row-count-output", Name: "row_count", Label: "Row count", Type: "integer"}
	document := authoringv2.Document{
		Output: authoringv2.Output{ID: outputID, Title: "Two Patient cohort"},
		Rows: authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{Source: authoringv2.GroupSource{
			Kind: authoringv2.GroupSourceExplicit,
			Explicit: &authoringv2.ExplicitGroupSource{
				RevisionID: groupRevision, UnassignedMemberPolicy: authoringv2.UnassignedMemberGroupAsUnassigned,
			},
		}}},
		Columns: []authoringv2.Column{{ColumnID: genderSource, Column: "gender", Label: "Gender"}},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
			{
				ID:     "expand-members",
				Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "group_rows"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationExpand, Expand: &authoringv2.ConstructionExpand{
					ConstructionID: "expand-members", InputColumnID: genderSource, OutputColumnID: genderItemID,
					EmptyPolicy: authoringv2.ConstructionExpandEmptyPreserveParent,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "group_id", Name: "group_id", Label: "Group ID", Type: "string"},
					{ID: "group_label", Name: "group_label", Label: "Group label", Type: "string"},
					{ID: "group_ordinal", Name: "group_ordinal", Label: "Group ordinal", Type: "integer"},
					{ID: "members", Name: "members", Label: "Members", Type: "array"},
					{ID: genderItemID, Name: genderItem, Label: "Gender item", Type: "string", Nullable: true},
				},
			},
			{
				ID:     "group-by-gender",
				Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "expand-members"}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
					ConstructionID: "group-by-gender", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
					Keys: []authoringv2.ConstructionGroupKey{
						{InputColumnID: "group_label", OutputColumnID: groupLabel.ID},
						{InputColumnID: genderItemID, OutputColumnID: genderKey.ID},
					},
					Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: rowCount.ID}},
				}},
				Outputs: []authoringv2.StageColumn{groupLabel, genderKey, rowCount},
			},
		}},
	}
	sourceContractColumn := receiptTestPublicColumn(source)
	sourceContractColumn.Cardinality = source.Cardinality
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: authoringv2.Workspace{Documents: []authoringv2.Document{document}},
		EmittedColumns: []explorer.EmittedColumn{source},
		OutputContracts: []explorer.PublicOutputContract{{
			OutputID: outputID, RootResourceType: "Patient", RowGrain: "groups", Lossless: true, MLReady: true,
			StructuralSuitability: "scalar",
			Columns:               []explorer.PublicOutputColumn{sourceContractColumn},
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
	if err := (explorer.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(bundle, reconciled.EmittedColumns); err != nil {
		t.Fatalf("validate Group→Expand public output contract: %v", err)
	}
}

func TestReconcileFinalOutputMetadataPublishesExplicitGroupRowsSchema(t *testing.T) {
	const outputID = "specimens"
	bundle := recipe.Bundle{Outputs: []recipe.Output{{
		Name: outputID, RootResourceType: "Specimen", RowGrain: "groups",
		GroupRows: &recipe.GroupRows{RevisionID: "grouprev_1", UnassignedMemberPolicy: "EXCLUDE"},
	}}}
	document := authoringv2.Document{
		Output: authoringv2.Output{ID: outputID, Title: "Specimens"},
		Rows: authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{Source: authoringv2.GroupSource{
			Kind:     authoringv2.GroupSourceExplicit,
			Explicit: &authoringv2.ExplicitGroupSource{RevisionID: "grouprev_1", UnassignedMemberPolicy: authoringv2.UnassignedMemberExclude},
		}}},
	}
	source := explorer.EmittedColumn{
		EmissionID: "source-specimen-id", OutputID: outputID, PublicColumn: "specimen_id", Label: "Specimen ID",
		LogicalType: "string", Cardinality: "optional_one", Nullable: true, Shape: "scalar",
		SourceResourceType: "Specimen", SourcePath: "id", Lossless: true, MLReady: true,
		StructuralSuitability: "scalar", Filterable: true, Chartable: true,
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: authoringv2.Workspace{Documents: []authoringv2.Document{document}},
		EmittedColumns: []explorer.EmittedColumn{source},
		OutputContracts: []explorer.PublicOutputContract{{
			OutputID: outputID, RootResourceType: "Specimen", RowGrain: "groups", RowMultiplication: "none",
			Lossless: true, MLReady: true, StructuralSuitability: "scalar",
			Columns: []explorer.PublicOutputColumn{publicOutputColumnFromEmission(source)},
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
	if err := (explorer.PublicOutputContracts{Outputs: reconciled.OutputContracts}).ValidateAgainst(bundle, reconciled.EmittedColumns); err != nil {
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
	patient := authoringv2.StageColumn{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}
	status := authoringv2.StageColumn{ID: "observation-status", Name: "observation_status", Label: "Observation status", Type: "string", Nullable: true}
	document := authoringv2.Document{
		Output:  authoringv2.Output{ID: "patients"},
		Columns: []authoringv2.Column{{ColumnID: patient.ID, Column: patient.Name, Label: patient.Label}},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: "observation-status-step",
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationRelatedField, RelatedField: &authoringv2.ConstructionRelatedField{
				ChoiceID: "choice", OutputColumnID: status.ID,
				Source: authoringv2.ConstructionRelatedFieldSource{
					Kind: capability.ConstructionChoiceSourceField, CandidateID: "status-candidate", NodeID: "observation-node",
					ResourceType: "Observation", Path: "status", Cardinality: "optional_one", LogicalType: "string",
				},
			}},
			Outputs: []authoringv2.StageColumn{patient, status},
		}}},
	}
	_, columns, err := authoredOutputColumns(authoringv2.Workspace{Documents: []authoringv2.Document{document}})
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
	pivoted := receipttest.StageFixture("pivoted", []authoringv2.StageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient", Type: "string"}, {ID: "kind", Name: "kind", Label: "Kind", Type: "string"}, {ID: "value", Name: "value", Label: "Value", Type: "decimal"}})
	category := "systolic"
	pivoted.Construction.Steps = []authoringv2.ConstructionStep{{ID: "pivot_vitals", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationPivot, Pivot: &authoringv2.ConstructionPivot{ConstructionID: "pivot_vitals", GroupKeyIDs: []string{"patient_id"}, CategoryColumnID: "kind", ValueColumnID: "value", Categories: []authoringv2.ConstructionPivotCategory{{Key: authoringv2.TableScalar{Kind: "STRING", String: &category}, OutputColumnID: "systolic"}}, DuplicatePolicy: authoringv2.ConstructionPivotDuplicateError, MissingCellPolicy: authoringv2.ConstructionPivotMissingNull, UnlistedCategoryPolicy: authoringv2.ConstructionPivotUnlistedError}}, Outputs: []authoringv2.StageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient", Type: "string"}, {ID: "systolic", Name: "systolic", Label: "Systolic", Type: "decimal", Nullable: true}}}}
	receipttest.AppendDeriveFixture(&pivoted, "adjust_systolic", authoringv2.StageColumn{ID: "adjusted_systolic", Name: "adjusted_systolic", Label: "Adjusted systolic", Type: "decimal", Nullable: true}, authoringv2.ConstructionDerivedAdd, receipttest.ColumnOperand("systolic"), receipttest.IntegerOperand(&one), authoringv2.ConstructionMissingInputError)
	unpivoted := receipttest.StageFixture("unpivoted", []authoringv2.StageColumn{{ID: "height", Name: "height", Label: "Height", Type: "decimal"}, {ID: "weight", Name: "weight", Label: "Weight", Type: "decimal"}})
	height, weight := "height", "weight"
	unpivoted.Construction.Steps = []authoringv2.ConstructionStep{{ID: "unpivot_vitals", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{ConstructionID: "unpivot_vitals", Inputs: []authoringv2.ConstructionUnpivotInput{{ColumnID: "height", Key: authoringv2.TableScalar{Kind: "STRING", String: &height}}, {ColumnID: "weight", Key: authoringv2.TableScalar{Kind: "STRING", String: &weight}}}, KeyOutputColumnID: "measure_name", ValueOutputColumnID: "measure_value", NullRowPolicy: authoringv2.ConstructionUnpivotDrop}}, Outputs: []authoringv2.StageColumn{{ID: "measure_name", Name: "measure_name", Label: "Measure", Type: "string"}, {ID: "measure_value", Name: "measure_value", Label: "Value", Type: "decimal", Nullable: true}}}}
	workspace := receipttest.StageWorkspace(pivoted, unpivoted)

	emitted := []explorer.EmittedColumn{
		{EmissionID: "emit_patient_id", OutputID: "pivoted", AuthoredColumns: []string{"person_root", "shared_root"}, PublicColumn: "patient_id", Label: "Patient", LogicalType: "string", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_kind", OutputID: "pivoted", AuthoredColumns: []string{"category_root", "shared_root"}, PublicColumn: "kind", Label: "Kind", LogicalType: "string", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_value", OutputID: "pivoted", AuthoredColumns: []string{"result_root"}, PublicColumn: "value", Label: "Value", LogicalType: "decimal", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_height", OutputID: "unpivoted", AuthoredColumns: []string{"height_root"}, PublicColumn: "height", Label: "Height", LogicalType: "decimal", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "emit_weight", OutputID: "unpivoted", AuthoredColumns: []string{"weight_root", "shared_root"}, PublicColumn: "weight", Label: "Weight", LogicalType: "decimal", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
	}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, Workspace: workspace, EmittedColumns: emitted,
		OutputContracts: []explorer.PublicOutputContract{
			{OutputID: "pivoted", Lossless: true, MLReady: true, StructuralSuitability: "scalar", Columns: []explorer.PublicOutputColumn{receiptTestPublicColumn(emitted[0]), receiptTestPublicColumn(emitted[1]), receiptTestPublicColumn(emitted[2])}},
			{OutputID: "unpivoted", Lossless: true, MLReady: true, StructuralSuitability: "scalar", Columns: []explorer.PublicOutputColumn{receiptTestPublicColumn(emitted[3]), receiptTestPublicColumn(emitted[4])}},
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
	want := []explorer.EmittedColumn{
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
	document := receipttest.StageFixture("patients", []authoringv2.StageColumn{{ID: "patient_id", Name: "patient_id", Label: "Patient ID", Type: "string"}, {ID: "score", Name: "score", Label: "Authored score", Type: "decimal"}, {ID: "stale", Name: "stale", Label: "Stale", Type: "string"}})
	receipttest.AppendDeriveFixture(&document, "scale_score", authoringv2.StageColumn{ID: "scaled_score", Name: "scaled_score", Label: "Scaled score", Type: "decimal", Nullable: true}, authoringv2.ConstructionDerivedMultiply, receipttest.ColumnOperand("score"), receipttest.DecimalOperand(float64Pointer(2)), authoringv2.ConstructionMissingInputPropagateNull)

	policy := &explorer.PublicUnitNormalization{Target: unit.UnitIdentity{System: "urn:test:source", Code: "source-unit"}, Rules: []explorer.PublicUnitRuleIdentity{{ID: "source-to-target", Version: "1"}}}
	emitted := []explorer.EmittedColumn{
		{EmissionID: "emit_patient_id", OutputID: "patients", NodeID: "node-patient", CandidateID: "candidate-id", AuthoredColumns: []string{"patient_id"}, PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string", Nullable: true, Shape: "scalar", SourceResourceType: "Patient", SourcePath: "id", Lossless: true, MLReady: true, StructuralSuitability: "scalar", Filterable: true, Chartable: true},
		{EmissionID: "emit_score", OutputID: "patients", NodeID: "node-observation", CandidateID: "candidate-score", AuthoredColumns: []string{"score"}, PublicColumn: "score", Label: "Authored score", LogicalType: "string", Nullable: true, Shape: "scalar", SourceResourceType: "Observation", SourcePath: "valueQuantity.value", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"source-loss"}, Filterable: true, Chartable: true, UnitNormalization: policy},
		{EmissionID: "emit_stale", OutputID: "patients", NodeID: "node-observation", CandidateID: "candidate-stale", AuthoredColumns: []string{"stale"}, PublicColumn: "stale", Label: "Stale", LogicalType: "string", Nullable: true, Shape: "scalar", SourceResourceType: "Observation", SourcePath: "value", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"dropped-loss"}},
	}
	contractColumns := []explorer.PublicOutputColumn{
		receiptTestPublicColumn(emitted[0]), receiptTestPublicColumn(emitted[1]), receiptTestPublicColumn(emitted[2]),
	}
	bundle := recipe.Bundle{Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}}}
	translated := explorercompilation.WorkspaceResult{
		Bundle: bundle, EmittedColumns: emitted,
		OutputContracts: []explorer.PublicOutputContract{{OutputID: "patients", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"source-loss", "dropped-loss"}, Columns: contractColumns}},
		Presentations: []explorercompilation.PresentationConfig{{OutputID: "patients", Title: "Patients", Columns: []explorercompilation.PresentationColumn{
			{EmissionID: "emit_patient_id", PublicColumn: "patient_id", Label: "Patient ID", Visible: true, Order: 0, PhysicalOrder: 0},
			{EmissionID: "emit_score", PublicColumn: "score", Label: "Authored score", Visible: true, Order: 1, PhysicalOrder: 1},
			{EmissionID: "emit_stale", PublicColumn: "stale", Label: "Stale", Visible: true, Order: 50, PhysicalOrder: 2},
		}}},
		IdentityMappings: []explorer.IdentityMapping{{OutputID: "patients", CandidateID: "candidate", EmissionIDs: []string{"emit_patient_id", "emit_score", "emit_stale"}}},
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

func receiptTestPublicColumn(emitted explorer.EmittedColumn) explorer.PublicOutputColumn {
	return explorer.PublicOutputColumn{
		Column: emitted.PublicColumn, AuthoredColumns: append([]string(nil), emitted.AuthoredColumns...), InputColumns: append([]string(nil), emitted.InputColumns...),
		Label: emitted.Label, LogicalType: emitted.LogicalType, Nullable: emitted.Nullable, Shape: emitted.Shape,
		SourceResourceType: emitted.SourceResourceType, SourcePath: emitted.SourcePath,
		Lossless: emitted.Lossless, MLReady: emitted.MLReady, StructuralSuitability: emitted.StructuralSuitability,
		LossReasons: append([]string(nil), emitted.LossReasons...), Filterable: emitted.Filterable, Chartable: emitted.Chartable,
		UnitNormalization: emitted.UnitNormalization,
	}
}

func emittedPublicColumnNames(values []explorer.EmittedColumn) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.PublicColumn)
	}
	return names
}

func publicContractColumnNames(values []explorer.PublicOutputColumn) []string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, value.Column)
	}
	return names
}

func float64Pointer(value float64) *float64 { return &value }
