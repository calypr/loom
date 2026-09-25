package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func TestCompileExplorerReceiptReconcilesAuthoredDerivedOutput(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	visible, order := true, 1
	two := int64(2)
	one := int64(1)
	document := &workspace.Documents[0]
	document.Columns = append(document.Columns, authoringv2.Column{
		Column: "patient_count", Label: "Patient count", LogicalType: "integer", OccurrenceID: "base",
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceAggregate, Aggregate: &authoringv2.AggregateSource{Operation: "COUNT"}},
		Table:  &authoringv2.TablePresentation{Visible: &visible, Order: &order},
	})
	document.TableShape = &authoringv2.TableShape{Derived: []authoringv2.DerivedConstruction{
		{
			ConstructionID: "scale_patient_count", Output: authoringv2.ColumnOutput{Column: "scaled_patient_count", Label: "Scaled patient count"},
			Operation: "MULTIPLY", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "patient_count"},
			Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "INTEGER", Integer: &two}},
			MissingInputPolicy: "ERROR",
		},
		{
			ConstructionID: "add_one", Output: authoringv2.ColumnOutput{Column: "doubled_patient_count", Label: "Doubled patient count"},
			Operation: "ADD", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "scaled_patient_count"},
			Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "INTEGER", Integer: &one}},
			MissingInputPolicy: "ERROR",
		},
	}}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
	if err != nil {
		t.Fatalf("compile receipt with an authored derived column: %v", err)
	}
	contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.ValidateAgainst(receipt.Bundle, receipt.EmittedColumns); err != nil {
		t.Fatalf("validate reconciled public output contract: %v", err)
	}
	wantColumns := []string{"c_patient", "patient_count", "scaled_patient_count", "doubled_patient_count"}
	if got := emittedPublicColumnNames(receipt.EmittedColumns); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("receipt public columns = %#v, want %#v", got, wantColumns)
	}
	scaled := receipt.EmittedColumns[2]
	doubled := receipt.EmittedColumns[3]
	if !reflect.DeepEqual(scaled.InputColumns, []string{"patient_count"}) || !reflect.DeepEqual(scaled.AuthoredColumns, []string{"patient_count"}) {
		t.Fatalf("first derived direct inputs/roots = %#v/%#v, emission=%#v", scaled.InputColumns, scaled.AuthoredColumns, scaled)
	}
	if !reflect.DeepEqual(doubled.InputColumns, []string{"scaled_patient_count"}) || !reflect.DeepEqual(doubled.AuthoredColumns, []string{"patient_count"}) {
		t.Fatalf("chained derived direct inputs/roots = %#v/%#v", doubled.InputColumns, doubled.AuthoredColumns)
	}
	wantConstructed := []explorer.EmittedColumn{
		{EmissionID: "construction:scale_patient_count:scaled_patient_count", OutputID: "patients", AuthoredColumns: []string{"patient_count"}, InputColumns: []string{"patient_count"}, ConstructionID: "scale_patient_count", PublicColumn: "scaled_patient_count", Label: "Scaled patient count", LogicalType: "integer", Cardinality: "required_one", Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_DERIVED_MULTIPLY_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
		{EmissionID: "construction:add_one:doubled_patient_count", OutputID: "patients", AuthoredColumns: []string{"patient_count"}, InputColumns: []string{"scaled_patient_count"}, ConstructionID: "add_one", PublicColumn: "doubled_patient_count", Label: "Doubled patient count", LogicalType: "integer", Cardinality: "required_one", Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_DERIVED_ADD_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
	}
	if !reflect.DeepEqual([]explorer.EmittedColumn{scaled, doubled}, wantConstructed) {
		t.Fatalf("constructed output metadata = %#v, want %#v", []explorer.EmittedColumn{scaled, doubled}, wantConstructed)
	}
	contract := contracts.Outputs[0]
	if contract.Lossless || contract.MLReady || contract.StructuralSuitability != "requires-review" || !reflect.DeepEqual(contract.LossReasons, []string{"AGGREGATE_REDUCTION", "TABLE_SHAPE_DERIVED_MULTIPLY_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED", "TABLE_SHAPE_DERIVED_ADD_NON_LOSSLESS"}) {
		t.Fatalf("derived receipt contract aggregation = %#v", contract)
	}
	repeated, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
	if err != nil {
		t.Fatalf("compile identical derived receipt again: %v", err)
	}
	if receipt.CompilationKey != repeated.CompilationKey || receipt.ID != repeated.ID {
		t.Fatalf("derived receipt identity changed: first=(%q,%q) second=(%q,%q)", receipt.CompilationKey, receipt.ID, repeated.CompilationKey, repeated.ID)
	}
}

func TestCompileExplorerReceiptPersistsCompilerConstructionStages(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "filter_step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate typed construction workspace: %v", err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
	if err != nil {
		t.Fatalf("compile receipt with typed construction stages: %v", err)
	}
	stages := receipt.ConstructionStages["patients"]
	if len(stages) != 2 || stages[0].ID != recipe.ConstructionSourceProjectionID || stages[0].Operation != "" || stages[1].ID != "filter_step" || stages[1].InputStageID != recipe.ConstructionSourceProjectionID || stages[1].Operation != "FILTER" {
		t.Fatalf("receipt stages = %#v", stages)
	}
	if len(stages[0].Columns) != 1 || stages[0].Columns[0].ID != document.Columns[0].ColumnID || stages[0].Columns[0].Name != "c_patient" {
		t.Fatalf("source stage descriptor = %#v", stages[0])
	}
	if stages[0].Columns[0].Cardinality != "optional_one" {
		t.Fatalf("source stage cardinality = %q, want optional_one", stages[0].Columns[0].Cardinality)
	}
	wantCapabilities := map[string]bool{"PIVOT": true, "DERIVE": true, "FILTER": true, "UNPIVOT": true, "GROUP": true, "EXPAND": true}
	for _, capability := range stages[0].Capabilities {
		delete(wantCapabilities, capability.Kind)
	}
	if len(wantCapabilities) != 0 {
		t.Fatalf("source stage capabilities omitted %v: %#v", wantCapabilities, stages[0].Capabilities)
	}
	if len(stages[1].Columns) != 1 || stages[1].Columns[0].ID != document.Columns[0].ColumnID || stages[1].Columns[0].Type == "" || stages[1].Columns[0].Cardinality != "optional_one" {
		t.Fatalf("filter stage descriptor = %#v", stages[1])
	}
	repeated, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
	if err != nil {
		t.Fatalf("recompile identical typed construction receipt: %v", err)
	}
	if receipt.CompilationKey != repeated.CompilationKey || receipt.ID != repeated.ID {
		t.Fatalf("construction receipt identity changed across exact recompilation: (%q,%q) != (%q,%q)", receipt.CompilationKey, receipt.ID, repeated.CompilationKey, repeated.ID)
	}
}

func TestCompileExplorerReceiptReconcilesTypedConstructionOutputs(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, authoringv2.Column{
		Column: "patient_count", Label: "Patient count", LogicalType: "integer", OccurrenceID: "base",
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceAggregate, Aggregate: &authoringv2.AggregateSource{Operation: "COUNT"}},
	})
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	outputs := make([]authoringv2.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	countID := document.Columns[1].ColumnID
	outputs = append(outputs, authoringv2.StageColumn{ID: "scaled_count_id", Name: "scaled_count", Label: "Scaled count", Type: "integer"})
	one := int64(1)
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "derive_count", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationDerive,
			Derive: &authoringv2.ConstructionDerive{
				ConstructionID: "derive_count", OutputColumnID: "scaled_count_id", Operation: authoringv2.ConstructionDerivedAdd,
				Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: countID},
				Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &one}},
				MissingInputPolicy: authoringv2.ConstructionMissingInputError,
			},
		},
		Outputs: outputs,
	}}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate typed derived construction: %v", err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	request := lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
	if err != nil {
		t.Fatalf("compile and reconcile typed derived construction output: %v", err)
	}
	contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.ValidateAgainst(receipt.Bundle, receipt.EmittedColumns); err != nil {
		t.Fatalf("validate typed construction public output contract: %v", err)
	}
	var scaled *explorer.EmittedColumn
	for index := range receipt.EmittedColumns {
		if receipt.EmittedColumns[index].PublicColumn == "scaled_count" {
			scaled = &receipt.EmittedColumns[index]
			break
		}
	}
	if scaled == nil || scaled.ConstructionID != "derive_count" || scaled.Label != "Scaled count" || !reflect.DeepEqual(scaled.InputColumns, []string{"patient_count"}) {
		t.Fatalf("typed derived output metadata was not reconciled: %#v", scaled)
	}
}

func TestReconcileFinalOutputMetadataUsesCompilerSchemaOrderAndTypes(t *testing.T) {
	translated, resolved := reconciliationFixture()
	reconciled, err := reconcileFinalOutputMetadata(translated, resolved)
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

func TestReconcileFinalOutputMetadataRejectsMissingOutputIdentity(t *testing.T) {
	translated, resolved := reconciliationFixture()
	resolved.Compiled.Outputs[0].Name = ""
	if _, err := reconcileFinalOutputMetadata(translated, resolved); err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("missing compiler output identity error = %v", err)
	}
}

func TestReconcileFinalOutputMetadataRejectsMissingConstructedDependency(t *testing.T) {
	translated, resolved := reconciliationFixture()
	translated.Workspace.Documents[0].TableShape.Derived[0].Left.Column = "missing"
	if _, err := reconcileFinalOutputMetadata(translated, resolved); err == nil || !strings.Contains(err.Error(), "missing dependency") {
		t.Fatalf("missing constructed dependency error = %v", err)
	}
}

func TestReconcileFinalOutputMetadataRejectsConstructedDependencyCycle(t *testing.T) {
	translated, resolved := reconciliationFixture()
	translated.Workspace.Documents[0].TableShape.Derived[0].Left.Column = "scaled_score"
	if _, err := reconcileFinalOutputMetadata(translated, resolved); err == nil || !strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("constructed dependency cycle error = %v", err)
	}
}

func TestReconcileFinalOutputMetadataRejectsConstructedEmissionCollision(t *testing.T) {
	translated, resolved := reconciliationFixture()
	translated.EmittedColumns[2].EmissionID = constructedEmissionID("scale_score", "scaled_score")
	if _, err := reconcileFinalOutputMetadata(translated, resolved); err == nil || !strings.Contains(err.Error(), "duplicates translated emission identity") {
		t.Fatalf("constructed emission collision error = %v", err)
	}
}

func TestAuthoredOutputColumnsDeduplicatesDirectInputsInAuthoringOrder(t *testing.T) {
	workspace := authoringv2.Workspace{Documents: []authoringv2.Document{{
		Output: authoringv2.Output{ID: "patients"},
		TableShape: &authoringv2.TableShape{Derived: []authoringv2.DerivedConstruction{
			{
				ConstructionID: "same_input", Output: authoringv2.ColumnOutput{Column: "same_result", Label: "Same result"},
				Operation: "ADD", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "score"},
				Right: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "score"}, MissingInputPolicy: "ERROR",
			},
			{
				ConstructionID: "ordered_inputs", Output: authoringv2.ColumnOutput{Column: "ordered_result", Label: "Ordered result"},
				Operation: "SUBTRACT", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "score"},
				Right: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "patient_count"}, MissingInputPolicy: "ERROR",
			},
		}},
	}}}
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
		constructedOutputQuality{Lossless: true, StructuralSuitability: "scalar"},
		lower.CompiledOutputColumn{Name: "statuses", Kind: "string", Cardinality: "many"},
	)
	if err == nil || !strings.Contains(err.Error(), "requires an explicit array policy") {
		t.Fatalf("many-valued constructed output without array policy error = %v", err)
	}
}

func TestAuthoredOutputColumnsRecognizesTypedConstructionOutputs(t *testing.T) {
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
	want := map[string][]string{
		"active_value":   {"group", "category", "value"},
		"adjusted_value": {"active_value"},
		"measure":        {"active_value", "adjusted_value"},
		"measure_value":  {"active_value", "adjusted_value"},
	}
	for name, inputColumns := range want {
		got, exists := columns["patients"][name]
		if !exists || !reflect.DeepEqual(got.InputColumns, inputColumns) {
			t.Errorf("typed construction output %q = %#v, want inputs %#v", name, got, inputColumns)
		}
	}
}

func TestReconcileFinalOutputMetadataUsesAuthoredReshapeOutputs(t *testing.T) {
	one := int64(1)
	bundle := recipe.Bundle{Outputs: []recipe.Output{{Name: "pivoted"}, {Name: "unpivoted"}}}
	workspace := authoringv2.Workspace{Documents: []authoringv2.Document{
		{Output: authoringv2.Output{ID: "pivoted"}, TableShape: &authoringv2.TableShape{
			Reshape: &authoringv2.TableReshape{
				Kind: "PIVOT",
				Pivot: &authoringv2.PivotConstruction{
					ConstructionID: "pivot_vitals", GroupKeys: []string{"patient_id"}, CategoryColumn: "kind", ValueColumn: "value",
					Categories:      []authoringv2.PivotCategory{{Output: authoringv2.ColumnOutput{Column: "systolic", Label: "Systolic"}}},
					DuplicatePolicy: "ERROR", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
				},
			},
			Derived: []authoringv2.DerivedConstruction{{
				ConstructionID: "adjust_systolic", Output: authoringv2.ColumnOutput{Column: "adjusted_systolic", Label: "Adjusted systolic"},
				Operation: "ADD", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "systolic"},
				Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "INTEGER", Integer: &one}},
				MissingInputPolicy: "ERROR",
			}},
		}},
		{Output: authoringv2.Output{ID: "unpivoted"}, TableShape: &authoringv2.TableShape{Reshape: &authoringv2.TableReshape{
			Kind: "UNPIVOT",
			Unpivot: &authoringv2.UnpivotConstruction{
				ConstructionID: "unpivot_vitals", Inputs: []authoringv2.UnpivotInput{{Column: "height"}, {Column: "weight"}},
				KeyOutput:     authoringv2.ColumnOutput{Column: "measure_name", Label: "Measure"},
				ValueOutput:   authoringv2.ColumnOutput{Column: "measure_value", Label: "Value"},
				NullRowPolicy: "DROP",
			},
		}}},
	}}
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
	resolved := dataframeexecution.Resolved{Bundle: bundle, Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{
		{Name: "pivoted", OutputSchema: []lower.CompiledOutputColumn{{Name: "patient_id", Kind: "string", Cardinality: "required_one"}, {Name: "systolic", Kind: "decimal", Cardinality: "optional_one", Nullable: true}, {Name: "adjusted_systolic", Kind: "decimal", Cardinality: "optional_one", Nullable: true}}},
		{Name: "unpivoted", OutputSchema: []lower.CompiledOutputColumn{{Name: "measure_name", Kind: "string", Cardinality: "required_one"}, {Name: "measure_value", Kind: "decimal", Cardinality: "optional_one", Nullable: true}}},
	}}}
	reconciled, err := reconcileFinalOutputMetadata(translated, resolved)
	if err != nil {
		t.Fatal(err)
	}
	want := []explorer.EmittedColumn{
		{EmissionID: "emit_patient_id", OutputID: "pivoted", AuthoredColumns: []string{"person_root", "shared_root"}, PublicColumn: "patient_id", Label: "Patient", LogicalType: "string", Cardinality: "required_one", Shape: "scalar", Lossless: true, MLReady: true, StructuralSuitability: "scalar"},
		{EmissionID: "construction:pivot_vitals:systolic", OutputID: "pivoted", AuthoredColumns: []string{"category_root", "person_root", "result_root", "shared_root"}, InputColumns: []string{"patient_id", "kind", "value"}, ConstructionID: "pivot_vitals", PublicColumn: "systolic", Label: "Systolic", LogicalType: "decimal", Cardinality: "optional_one", Nullable: true, Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_PIVOT_BASE_COLUMNS_DROPPED", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
		{EmissionID: "construction:adjust_systolic:adjusted_systolic", OutputID: "pivoted", AuthoredColumns: []string{"category_root", "person_root", "result_root", "shared_root"}, InputColumns: []string{"systolic"}, ConstructionID: "adjust_systolic", PublicColumn: "adjusted_systolic", Label: "Adjusted systolic", LogicalType: "decimal", Cardinality: "optional_one", Nullable: true, Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_DERIVED_ADD_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
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
	shape := &authoringv2.TableShape{Derived: []authoringv2.DerivedConstruction{{
		ConstructionID: "scale_score", Output: authoringv2.ColumnOutput{Column: "scaled_score", Label: "Scaled score"},
		Operation: "MULTIPLY", Left: authoringv2.ArithmeticOperand{Kind: "COLUMN", Column: "score"},
		Right:              authoringv2.ArithmeticOperand{Kind: "LITERAL", Literal: &authoringv2.TableScalar{Kind: "DECIMAL", Decimal: float64Pointer(2)}},
		MissingInputPolicy: "PROPAGATE_NULL",
	}}}
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
		Workspace:        authoringv2.Workspace{Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, TableShape: shape}}},
	}
	resolved := dataframeexecution.Resolved{
		Bundle: bundle,
		Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{Name: "patients", OutputSchema: []lower.CompiledOutputColumn{
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

func TestCompileExplorerReceiptBindsRowDefinitionProposalBeforeIdentity(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	binding := &explorer.RowDefinitionProposalBinding{
		DraftVersion:             3,
		DraftDigest:              "sha256:draft",
		OutputID:                 "patients",
		BaseDocumentDigest:       "sha256:document",
		CandidateWorkspaceDigest: intentDigest,
		SnapshotToken:            snapshot.Token,
	}
	receipt, err := compileExplorerReceipt(context.Background(), lifecycle.CompileReceiptRequest{
		Project:               "project-a",
		ExplorerID:            "custom",
		Workspace:             workspace,
		SnapshotToken:         snapshot.Token,
		Authorized:            lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		RowDefinitionProposal: binding,
	}, nil, recipeEngine, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt.RowDefinitionProposal, binding) {
		t.Fatalf("stored proposal binding = %#v, want %#v", receipt.RowDefinitionProposal, binding)
	}
	if receipt.RowDefinitionProposal == binding {
		t.Fatal("stored receipt aliases the caller-owned proposal binding")
	}
	withoutBinding := *receipt
	withoutBinding.RowDefinitionProposal = nil
	withoutBinding.CompilationKey = ""
	withoutBinding.ID = ""
	keyWithoutBinding, err := explorer.CompilationKey(withoutBinding)
	if err != nil {
		t.Fatal(err)
	}
	idWithoutBinding, err := explorer.ReceiptID(withoutBinding)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CompilationKey == keyWithoutBinding || receipt.ID == idWithoutBinding {
		t.Fatalf("proposal binding did not enter receipt identity: with=(%q,%q) without=(%q,%q)", receipt.CompilationKey, receipt.ID, keyWithoutBinding, idWithoutBinding)
	}
}

func TestCompileExplorerReceiptBindsTableShapeProposalBeforeIdentity(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	binding := &explorer.TableShapeProposalBinding{
		DraftVersion: 3, DraftDigest: "sha256:draft", OutputID: "patients",
		BaseDocumentDigest: "sha256:document", CandidateWorkspaceDigest: intentDigest,
		SnapshotToken: snapshot.Token,
	}
	receipt, err := compileExplorerReceipt(context.Background(), lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized:         lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		TableShapeProposal: binding,
	}, nil, recipeEngine, service, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(receipt.TableShapeProposal, binding) {
		t.Fatalf("stored proposal binding = %#v, want %#v", receipt.TableShapeProposal, binding)
	}
	if receipt.TableShapeProposal == binding {
		t.Fatal("stored receipt aliases the caller-owned proposal binding")
	}
	withoutBinding := *receipt
	withoutBinding.TableShapeProposal = nil
	withoutBinding.CompilationKey = ""
	withoutBinding.ID = ""
	keyWithoutBinding, err := explorer.CompilationKey(withoutBinding)
	if err != nil {
		t.Fatal(err)
	}
	idWithoutBinding, err := explorer.ReceiptID(withoutBinding)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.CompilationKey == keyWithoutBinding || receipt.ID == idWithoutBinding {
		t.Fatalf("proposal binding did not enter receipt identity: with=(%q,%q) without=(%q,%q)", receipt.CompilationKey, receipt.ID, keyWithoutBinding, idWithoutBinding)
	}
}

func TestCompileValidatedReceiptResolutionChecksAllOutputsForScopedPreview(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "multi-output-preview",
		TranslationVersion:  explorercompilation.TranslationVersion,
		Outputs: []recipe.Output{
			{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{
				{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "patient_gender", Expr: recipe.Expression{Select: "root.gender"}},
			}},
			{Name: "specimens", RootResourceType: "Specimen", RowGrain: "resource", Fields: []recipe.Field{{Name: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}}},
		},
	}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolvedDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := resolvedOutputArtifacts(compiled)
	if err != nil {
		t.Fatal(err)
	}
	receipt := &explorer.CompilationReceipt{
		Bundle:                 bundle,
		RecipeDigest:           compiled.StoredRecipeDigest,
		ResolvedRecipeDigest:   resolvedDigest,
		ResolvedSchemaDigest:   compiled.ResolvedSchemaDigest,
		OutputFingerprints:     fingerprints,
		OutputColumnProvenance: provenance,
		EmittedColumns: []explorer.EmittedColumn{
			// Presentation order is intentionally different from compiler field
			// order. It must not invalidate an otherwise identical receipt.
			{OutputID: "patients", PublicColumn: "patient_gender"},
			{OutputID: "patients", PublicColumn: "patient_id"},
			{OutputID: "specimens", PublicColumn: "specimen_id"},
		},
	}
	previewBindings := bindings
	previewBindings.OutputNames = []string{"patients"}
	resolved, err := compileValidatedReceiptResolution(context.Background(), recipeEngine, receipt, previewBindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved.Compiled.Outputs) != 2 {
		t.Fatalf("validated outputs=%d, want complete receipt with 2 outputs", len(resolved.Compiled.Outputs))
	}
	if len(previewBindings.OutputNames) != 1 || previewBindings.OutputNames[0] != "patients" {
		t.Fatalf("preview execution selection was mutated: %#v", previewBindings.OutputNames)
	}
}

func TestCompileValidatedReceiptResolutionSurvivesCompilerMetadataJSONRoundTrip(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "round-trip", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}, Discovered: true}}}}}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := resolvedOutputArtifacts(compiled)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := explorer.CompilationReceipt{Bundle: bundle, RecipeDigest: compiled.StoredRecipeDigest, ResolvedRecipeDigest: digest, ResolvedSchemaDigest: compiled.ResolvedSchemaDigest, OutputFingerprints: fingerprints, OutputColumnProvenance: provenance, EmittedColumns: []explorer.EmittedColumn{{OutputID: "patients", PublicColumn: "patient_id"}}}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.CompilationReceipt
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Bundle.Outputs[0].Fields[0].Discovered {
		t.Fatal("compiler-local provenance unexpectedly survived recipe JSON")
	}
	resolved, err := compileValidatedReceiptResolution(context.Background(), recipeEngine, &stored, bindings)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	for _, column := range resolved.Compiled.Outputs[0].OutputSchema {
		if column.Name == "patient_id" {
			restored = column.Discovered
		}
	}
	if !restored {
		t.Fatal("durable receipt provenance was not restored")
	}
}

func TestPersistValidatedReceiptRejectsRuntimeMismatchBeforeStore(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "pre-store-validation", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	bindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	resolved, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := resolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := &explorer.CompilationReceipt{Bundle: bundle, RecipeDigest: resolved.StoredRecipeDigest, ResolvedRecipeDigest: bundleDigest, ResolvedSchemaDigest: resolved.ResolvedSchemaDigest, OutputFingerprints: fingerprints, OutputColumnProvenance: provenance, EmittedColumns: []explorer.EmittedColumn{{OutputID: "patients", PublicColumn: "patient_id"}}}
	bad := *receipt
	bad.OutputFingerprints = map[string]string{"patients": "runtime-drift"}
	stores := 0
	persist := func(context.Context, explorer.CompilationReceipt) (*explorer.CompilationReceipt, error) {
		stores++
		return receipt, nil
	}
	if _, err := persistValidatedReceipt(context.Background(), recipeEngine, &bad, bindings, persist); err == nil || !strings.Contains(err.Error(), "COMPILATION_CONTRACT_MISMATCH") {
		t.Fatalf("runtime mismatch error = %v, want COMPILATION_CONTRACT_MISMATCH", err)
	}
	if stores != 0 {
		t.Fatalf("runtime mismatch reached receipt store %d times", stores)
	}
	if _, err := persistValidatedReceipt(context.Background(), recipeEngine, receipt, bindings, persist); err != nil {
		t.Fatalf("corrected receipt rejected: %v", err)
	}
	if stores != 1 {
		t.Fatalf("corrected receipt store calls = %d, want 1", stores)
	}
}

func TestResolvedOutputFingerprintExcludesOptimizerAndTransientProvenance(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "fingerprint", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	resolved, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	beforeArtifacts, _, err := resolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	before := beforeArtifacts["patients"]
	resolved.Compiled.Outputs[0].OutputSchema[0].Discovered = !resolved.Compiled.Outputs[0].OutputSchema[0].Discovered
	resolved.Compiled.Outputs[0].OptimizedPlan.OptimizationPolicy.Decisions = append(resolved.Compiled.Outputs[0].OptimizedPlan.OptimizationPolicy.Decisions, ir.PhysicalOptimizationDecision{Reason: "diagnostic-only"})
	afterArtifacts, _, err := resolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	after := afterArtifacts["patients"]
	if before != after {
		t.Fatalf("transient compiler metadata changed canonical fingerprint: %q != %q", before, after)
	}
	resolved.Compiled.Outputs[0].OutputSchema[0].NormalizedUnit = &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	unitArtifacts, _, err := resolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if changed := unitArtifacts["patients"]; changed == before {
		t.Fatal("normalized output unit did not change canonical fingerprint")
	}
	resolved.Compiled.Outputs[0].OutputSchema[0].NormalizedUnit = nil
	for key := range resolved.Compiled.Outputs[0].Plan.BindVars {
		resolved.Compiled.Outputs[0].Plan.BindVars[key] = "semantically-different"
		break
	}
	changedArtifacts, _, err := resolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if changed := changedArtifacts["patients"]; changed == before {
		t.Fatal("execution bind change did not change canonical fingerprint")
	}
}

func TestCompiledExplorerWorkspaceConfigPreservesSemanticOrderForPresentationTies(t *testing.T) {
	compiled := explorercompilation.WorkspaceResult{
		Workspace: authoringv2.Workspace{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
			Explorer:  authoringv2.ExplorerMetadata{Title: "Stable"},
			Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "out", Title: "Out"}}},
			Tabs:      []authoringv2.Tab{{ID: "tab", Title: "Out", OutputID: "out", Order: 0, Visible: true}},
		},
		Bundle: recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Outputs: []recipe.Output{{Name: "out"}}},
		EmittedColumns: []explorer.EmittedColumn{
			{EmissionID: "column_b", OutputID: "out", PublicColumn: "column_b"},
			{EmissionID: "column_a", OutputID: "out", PublicColumn: "column_a"},
		},
		Presentations: []explorercompilation.PresentationConfig{{OutputID: "out", Columns: []explorercompilation.PresentationColumn{
			{EmissionID: "column_b", PublicColumn: "column_b", Label: "B", Visible: false, Order: 0, PhysicalOrder: 1, FilterLabel: "B", FilterOrder: 0, ChartType: "bar", ChartOrder: 0},
			{EmissionID: "column_a", PublicColumn: "column_a", Label: "A", Visible: true, Order: 0, PhysicalOrder: 0, FilterLabel: "A", FilterOrder: 0, ChartType: "line", ChartOrder: 1},
		}}},
	}
	raw, err := compiledExplorerWorkspaceConfigV2("project-a", "explorer-a", compiled)
	if err != nil {
		t.Fatal(err)
	}
	var config explorer.ConfigV2
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	view := config.Views[0]
	if view.Table.Columns[0].Column != "column_a" || view.Table.Columns[1].Column != "column_b" {
		t.Fatalf("table order=%#v", view.Table.Columns)
	}
	if view.Filters[0].Column != "column_b" || view.Filters[1].Column != "column_a" {
		t.Fatalf("filter order=%#v", view.Filters)
	}
	if view.Table.Columns[1].Visible || view.Filters[0].Column != view.Table.Columns[1].Column {
		t.Fatalf("hidden filter-only column was not preserved: table=%#v filters=%#v", view.Table.Columns, view.Filters)
	}
	if view.Charts[0].Column != "column_b" || view.Charts[1].Column != "column_a" {
		t.Fatalf("chart order=%#v", view.Charts)
	}
}

func TestNativeV2RouteUsesAuthorizedPersistedReceipt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	executionScope := scope
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "custom", "Custom", "", "test"); err != nil {
		t.Fatal(err)
	}
	previewCalls := 0
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: executionScope}, nil
			},
		},
		ReceiptLookup: service.CompilationReceiptForExplorer,
		PreviewReceipt: func(_ context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCalls++
			if receipt == nil || bindings.AuthScopeMode != authscope.ReadScopeUnrestricted || bindings.IncludeAuthResourcePath || bindings.IncludeRowIdentity {
				t.Fatalf("preview bindings widened or requested publication metadata: receipt=%#v bindings=%#v", receipt, bindings)
			}
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, PlanMode: "physical", PlanProfile: "generic_fhir_graph_recipe", PlanFingerprint: "test", TraversalCount: 0}, nil
		},
	}
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := persistTestNativeReceipt(context.Background(), t, service, lifecycle.CompileReceiptRequest{Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token, Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: scope}}, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	preview := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+receipt.ID+`","outputId":"patients","limit":5}`)
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview status=%d body=%s", preview.StatusCode, preview.Body)
	}
	ownerAfterPreview, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if ownerAfterPreview.ActiveRevisionID != "" || len(ownerAfterPreview.DraftConfig) != 0 {
		t.Fatalf("preview mutated active or draft state: %#v", ownerAfterPreview)
	}
	if previewCalls != 1 {
		t.Fatalf("preview calls=%d", previewCalls)
	}
	unknown := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+receipt.ID+`","outputId":"missing","limit":5}`)
	if unknown.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(unknown.Body, `"code":"UNKNOWN_AUTHORING_OUTPUT"`) || previewCalls != 1 {
		t.Fatalf("unknown output status=%d calls=%d body=%s", unknown.StatusCode, previewCalls, unknown.Body)
	}
	for _, path := range []string{
		"/api/v1/projects/project-b/explorers/custom/authoring/v2/preview",
		"/api/v1/projects/project-a/explorers/other/authoring/v2/preview",
	} {
		foreign := requestJSON(t, app, http.MethodPost, path, `{"receiptId":"`+receipt.ID+`","outputId":"patients","limit":5}`)
		if foreign.StatusCode != http.StatusNotFound || !strings.Contains(foreign.Body, `"code":"COMPILE_RECEIPT_NOT_FOUND"`) || previewCalls != 1 {
			t.Fatalf("foreign receipt path=%s status=%d calls=%d body=%s", path, foreign.StatusCode, previewCalls, foreign.Body)
		}
	}
	executionScope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted}
	widened := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/preview", `{"receiptId":"`+receipt.ID+`","outputId":"patients","limit":5}`)
	if widened.StatusCode != http.StatusConflict || !strings.Contains(widened.Body, `"code":"RECEIPT_STALE"`) || previewCalls != 1 {
		t.Fatalf("scope mismatch status=%d calls=%d body=%s", widened.StatusCode, previewCalls, widened.Body)
	}
}

func TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	service, err := explorer.NewService(newTestExplorerStore())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateInteractiveFrom(context.Background(), "project-a", "custom", "Custom", "", "test"); err != nil {
		t.Fatal(err)
	}
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return persistTestNativeReceipt(ctx, t, service, request, snapshot)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)

	commandBody := fmt.Sprintf(`{"commandId":"browser-command-1","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":0,"commands":[{"type":"CREATE_TABLE","title":"Patients","rootNodeId":"n_patient"}]}`, authoringv2.CurrentSemanticsVersion, snapshot.Token)
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if created.StatusCode != http.StatusOK {
		t.Fatalf("command status=%d body=%s", created.StatusCode, created.Body)
	}
	if !strings.Contains(created.Body, `"columns":[]`) {
		t.Fatalf("command response omitted required empty columns array: %s", created.Body)
	}
	var response authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(created.Body), &response); err != nil {
		t.Fatal(err)
	}
	if response.DraftVersion != 1 || len(response.Workspace.Documents) != 1 || !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(response.Workspace.Documents[0].Output.ID) {
		t.Fatalf("command response=%#v", response)
	}
	replayed := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if replayed.StatusCode != http.StatusOK || !strings.Contains(replayed.Body, `"draftVersion":1`) {
		t.Fatalf("replay status=%d body=%s", replayed.StatusCode, replayed.Body)
	}
	conflicting := strings.Replace(commandBody, `"Patients"`, `"Different"`, 1)
	conflict := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", conflicting)
	if conflict.StatusCode != http.StatusConflict || !strings.Contains(conflict.Body, `"code":"COMMAND_ID_CONFLICT"`) {
		t.Fatalf("command ID conflict status=%d body=%s", conflict.StatusCode, conflict.Body)
	}
	secondBody := fmt.Sprintf(`{"commandId":"browser-command-2","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"commands":[{"type":"CREATE_TABLE","title":"Visits","rootNodeId":"n_patient"}]}`, authoringv2.CurrentSemanticsVersion, snapshot.Token)
	second := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", secondBody)
	if second.StatusCode != http.StatusOK || !strings.Contains(second.Body, `"draftVersion":2`) {
		t.Fatalf("second command status=%d body=%s", second.StatusCode, second.Body)
	}
	var secondResponse authoringv2.ApplyCommandsResponse
	if err := json.Unmarshal([]byte(second.Body), &secondResponse); err != nil {
		t.Fatal(err)
	}
	oldRetry := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/commands", commandBody)
	if oldRetry.StatusCode != http.StatusConflict || !strings.Contains(oldRetry.Body, `"code":"DRAFT_CONFLICT"`) {
		t.Fatalf("older command retry status=%d body=%s", oldRetry.StatusCode, oldRetry.Body)
	}
	reconcileBody := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":%d,"draftDigest":%q}`, snapshot.Token, secondResponse.DraftVersion, secondResponse.DraftDigest)
	reconciled := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers/custom/authoring/v2/reconcile", reconcileBody)
	if reconciled.StatusCode != http.StatusOK || !strings.Contains(reconciled.Body, `"kind":"ExplorerBuilderReceipt"`) {
		t.Fatalf("reconcile status=%d body=%s", reconciled.StatusCode, reconciled.Body)
	}
}

func TestCreateExplorerFromCurrentClonesWorkspaceOnServer(t *testing.T) {
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{Project: "project-a", ExplorerID: "source", Title: "Source", ManagementMode: explorer.ManagementInteractive, DraftConfig: canonical, DraftVersion: 1, DraftDigest: digest}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, lifecycle.Config{})
	created := requestJSON(t, app, http.MethodPost, "/api/v1/projects/project-a/explorers", `{"name":"Cloned Explorer","title":"Cloned Explorer","sourceExplorerId":"source"}`)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("clone status=%d body=%s", created.StatusCode, created.Body)
	}
	clone, err := service.Get(context.Background(), "project-a", explorer.StableExplorerID("Cloned Explorer"))
	if err != nil {
		t.Fatal(err)
	}
	clonedWorkspace, err := authoringv2.DecodeWorkspace(clone.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if clone.DraftVersion != 1 || clonedWorkspace.Explorer.Title != "Cloned Explorer" || len(clonedWorkspace.Documents) != len(workspace.Documents) || clonedWorkspace.Documents[0].Output.ID != workspace.Documents[0].Output.ID {
		t.Fatalf("cloned Explorer=%#v workspace=%#v", clone, clonedWorkspace)
	}
}

func persistTestNativeReceipt(ctx context.Context, t *testing.T, service *explorer.Service, request lifecycle.CompileReceiptRequest, snapshot capability.Snapshot) (*explorer.CompilationReceipt, error) {
	t.Helper()
	workspace := request.Workspace
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		return nil, err
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "native-test", TranslationVersion: explorercompilation.TranslationVersion, Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "c_patient", Expr: recipe.Expression{Select: "root.id"}}}}}}
	bundleDigest, err := bundle.Digest()
	if err != nil {
		return nil, err
	}
	contract := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"c_patient","label":"Patient ID","logicalType":"string","filterable":true,"chartable":true}]}]}`)
	contractDigest, err := explorer.CompilationArtifactDigest(contract)
	if err != nil {
		return nil, err
	}
	receipt := explorer.CompilationReceipt{
		ReceiptFormatVersion: explorer.CurrentReceiptFormatVersion, CompilerContractVersion: explorer.CurrentCompilerContractVersion,
		Project: snapshot.Identity.Project, ExplorerID: request.ExplorerID, IntentDigest: intentDigest, SnapshotToken: snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest, CapabilitySchemaDigest: snapshot.Identity.SchemaDigest,
		ShapeDigest:      snapshot.Identity.ShapeDigest,
		SourceGeneration: snapshot.Identity.Generation, RecipeDigest: bundleDigest, ResolvedRecipeDigest: bundleDigest,
		ResolvedSchemaDigest: "resolved-schema", OutputContractDigest: contractDigest,
		NormalizedBundle: normalized, Bundle: bundle, CompiledConfig: json.RawMessage(`{"apiVersion":"` + explorer.ConfigV2APIVersion + `","kind":"ExplorerConfig"}`), PublicOutputContract: contract,
		RowDefinitionProposal: request.RowDefinitionProposal, TableShapeProposal: request.TableShapeProposal,
		ConstructionProposal: request.ConstructionProposal,
		EmittedColumns:       []explorer.EmittedColumn{{EmissionID: "em_patient", OutputID: "patients", CandidateID: "c_patient_id", OccurrenceID: authoringv2.RootOccurrenceID, ProjectionMode: "FIRST", PublicColumn: "c_patient", Label: "Patient ID", LogicalType: "string", Filterable: true, Chartable: true}},
		OutputFingerprints:   map[string]string{"patients": "fingerprint"}, OutputColumnProvenance: map[string]map[string]string{"patients": {"c_patient": "EXPLICIT"}}, CreatedAt: time.Now().UTC(),
	}
	receipt.CompilationKey, err = explorer.CompilationKey(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ID, err = explorer.ReceiptID(receipt)
	if err != nil {
		return nil, err
	}
	return service.StoreCompilationReceipt(ctx, receipt)
}
