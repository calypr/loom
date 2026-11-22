package receipt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	recipetest "github.com/calypr/loom/internal/testkit/recipetest"

	"github.com/calypr/loom/internal/testkit/explorerfixtures"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	receipttest "github.com/calypr/loom/internal/testkit/recipetest"

	"github.com/calypr/loom/internal/testkit/recipefixtures"

	reciperuntime "github.com/calypr/loom/internal/dataframe/recipe/runtime"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"

	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestClassifyReceiptRecipeErrorPreservesPivotReducerValidation(t *testing.T) {
	cause := fmt.Errorf("compile output: %w", &lower.PivotReducerTypeError{Policy: recipe.PivotDuplicateMin, ValueColumn: "specimen_id"})
	classified := ClassifyRecipeError(cause)
	var diagnostic *explorercompilation.Error
	if !errors.As(classified, &diagnostic) || diagnostic.Code != "PIVOT_REDUCER_REQUIRES_NUMERIC" || diagnostic.Stage != "construction" {
		t.Fatalf("classified error = %v, want construction validation diagnostic", classified)
	}
	if !errors.Is(classified, cause) {
		t.Fatalf("classified error lost original cause: %v", classified)
	}
	unexpected := errors.New("storage failed")
	if got := ClassifyRecipeError(unexpected); got != unexpected {
		t.Fatalf("unrelated error = %v, want original error", got)
	}
}

func TestClassifyReceiptRecipeErrorIdentifiesInvalidRelatedRowAnchor(t *testing.T) {
	cause := fmt.Errorf("compile output: %w", &lower.RelatedEligibilityAnchorError{StepID: "related_step"})
	classified := ClassifyRecipeError(cause)
	var diagnostic *explorercompilation.Error
	if !errors.As(classified, &diagnostic) || diagnostic.Code != "CONSTRUCTION_ANCHOR_INVALID" || diagnostic.Stage != "construction" {
		t.Fatalf("classified error = %v, want related row anchor diagnostic", classified)
	}
	if diagnostic.Details["stepId"] != "related_step" || !errors.Is(classified, cause) {
		t.Fatalf("classified error lost the affected step or cause: %#v", diagnostic)
	}
}

func TestClassifyReceiptRecipeErrorSanitizesInvalidRelatedSourceAnchor(t *testing.T) {
	cause := fmt.Errorf("compile output: %w", &lower.RelatedSourceAnchorError{
		OutputID: "specimens", StepID: "related_status", Reason: "row_identity_changed",
	})
	classified := ClassifyRecipeError(cause)
	var diagnostic *explorercompilation.Error
	if !errors.As(classified, &diagnostic) || diagnostic.Code != "CONSTRUCTION_ANCHOR_INVALID" || diagnostic.Stage != "construction" {
		t.Fatalf("classified error = %v, want related source anchor diagnostic", classified)
	}
	if diagnostic.Message != "This related source does not match the current table rows. Reopen the step and choose a source for this table." ||
		diagnostic.Details["outputId"] != "specimens" || diagnostic.Details["stepId"] != "related_status" ||
		diagnostic.Details["reason"] != "row_identity_changed" || !errors.Is(classified, cause) {
		t.Fatalf("diagnostic did not retain safe table/step context and original cause: %#v", diagnostic)
	}
}

func TestCompileExplorerReceiptReconcilesAuthoredDerivedOutput(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
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
	started, err := authoringv2.StartDocumentConstruction(*document)
	if err != nil {
		t.Fatal(err)
	}
	*document = started
	receipttest.AppendDeriveFixture(document, "scale_patient_count", authoringv2.StageColumn{ID: "scaled_patient_count", Name: "scaled_patient_count", Label: "Scaled patient count", Type: "integer"}, authoringv2.ConstructionDerivedMultiply, receipttest.ColumnOperand(document.Columns[1].ColumnID), receipttest.IntegerOperand(&two), authoringv2.ConstructionMissingInputError)
	receipttest.AppendDeriveFixture(document, "add_one", authoringv2.StageColumn{ID: "doubled_patient_count", Name: "doubled_patient_count", Label: "Doubled patient count", Type: "integer"}, authoringv2.ConstructionDerivedAdd, receipttest.ColumnOperand("scaled_patient_count"), receipttest.IntegerOperand(&one), authoringv2.ConstructionMissingInputError)

	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(explorerfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	request := ports.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}
	receipt, err := Compile(context.Background(), request, nil, recipeEngine, service, nil, nil)
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
	if got := receipttest.EmittedPublicColumnNames(receipt.EmittedColumns); !reflect.DeepEqual(got, wantColumns) {
		t.Fatalf("receipt public columns = %#v, want %#v", got, wantColumns)
	}
	scaled := receipt.EmittedColumns[2]
	doubled := receipt.EmittedColumns[3]
	if !reflect.DeepEqual(scaled.InputColumns, []string{"patient_count"}) || !reflect.DeepEqual(scaled.AuthoredColumns, []string{"patient_count"}) {
		t.Fatalf("first derived direct inputs/roots = %#v/%#v, emission=%#v", scaled.InputColumns, scaled.AuthoredColumns, scaled)
	}
	if !reflect.DeepEqual(doubled.InputColumns, []string{"patient_count"}) || !reflect.DeepEqual(doubled.AuthoredColumns, []string{"patient_count"}) {
		t.Fatalf("chained derived source inputs/roots = %#v/%#v", doubled.InputColumns, doubled.AuthoredColumns)
	}
	wantConstructed := []explorer.EmittedColumn{
		{EmissionID: "construction:scale_patient_count:scaled_patient_count", OutputID: "patients", AuthoredColumns: []string{"patient_count"}, InputColumns: []string{"patient_count"}, ConstructionID: "scale_patient_count", PublicColumn: "scaled_patient_count", Label: "Scaled patient count", LogicalType: "integer", Cardinality: "required_one", Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_DERIVED_MULTIPLY_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
		{EmissionID: "construction:add_one:doubled_patient_count", OutputID: "patients", AuthoredColumns: []string{"patient_count"}, InputColumns: []string{"patient_count"}, ConstructionID: "add_one", PublicColumn: "doubled_patient_count", Label: "Doubled patient count", LogicalType: "integer", Cardinality: "required_one", Shape: "scalar", Lossless: false, MLReady: false, StructuralSuitability: "requires-review", LossReasons: []string{"TABLE_SHAPE_DERIVED_ADD_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED"}, Filterable: true, Chartable: true},
	}
	if !reflect.DeepEqual([]explorer.EmittedColumn{scaled, doubled}, wantConstructed) {
		t.Fatalf("constructed output metadata = %#v, want %#v", []explorer.EmittedColumn{scaled, doubled}, wantConstructed)
	}
	contract := contracts.Outputs[0]
	if contract.Lossless || contract.MLReady || contract.StructuralSuitability != "requires-review" || !reflect.DeepEqual(contract.LossReasons, []string{"AGGREGATE_REDUCTION", "TABLE_SHAPE_DERIVED_MULTIPLY_NON_LOSSLESS", "TABLE_SHAPE_ML_READINESS_UNASSESSED", "TABLE_SHAPE_DERIVED_ADD_NON_LOSSLESS"}) {
		t.Fatalf("derived receipt contract aggregation = %#v", contract)
	}
	repeated, err := Compile(context.Background(), request, nil, recipeEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile identical derived receipt again: %v", err)
	}
	if receipt.CompilationKey != repeated.CompilationKey || receipt.ID != repeated.ID {
		t.Fatalf("derived receipt identity changed: first=(%q,%q) second=(%q,%q)", receipt.CompilationKey, receipt.ID, repeated.CompilationKey, repeated.ID)
	}
}

func TestCompileExplorerReceiptPersistsCompilerConstructionStages(t *testing.T) {
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: capabilityresolver.ScopeDigest(readScope),
			SchemaDigest: strings.Repeat("c", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships",
			FieldDigest: "fields", ShapeDigest: strings.Repeat("d", 64), ProtocolVersion: capabilityresolver.ProtocolVersion,
			CompilerVersion: capabilityresolver.CompilerVersion, TraversalPolicyVersion: capabilityresolver.TraversalPolicyVersion,
			ProjectionPolicyVersion: capabilityresolver.ProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: capabilityresolver.TraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: capabilityresolver.ProjectionPolicyVersion},
		},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_specimen", ResourceType: "Specimen", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{
			ID: "c_specimen_id", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "id", Label: "Specimen ID",
			LogicalType: "string", Cardinality: "OPTIONAL_ONE", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		}},
		nil,
	)
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.Output.ID = "specimens"
	document.Output.Title = "Named cohort QA"
	document.RootResourceType = "Specimen"
	document.Route.ResourceType = "Specimen"
	document.Columns[0].ColumnID = "source_c7df487a53e218785ad00f6bd656f320"
	document.Columns[0].Column = "col_26a18d9205b062fde575cdf7"
	document.Columns[0].Label = "Original Specimen ID"
	document, err = authoringv2.StartDocumentConstruction(document)
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	sourceID := "b7cad184-db67-5542-a975-10fffa3e89e7"
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "qa-source-filter", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterEquals,
				Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterString, String: &sourceID}}},
		},
		Outputs: columns,
	}}
	document.Rows = authoringv2.RowDefinition{Kind: authoringv2.RowDefinitionGroups, Groups: &authoringv2.GroupedRows{
		Source: authoringv2.GroupSource{
			Kind:     authoringv2.GroupSourceExplicit,
			Explicit: &authoringv2.ExplicitGroupSource{RevisionID: "grouprev_test", UnassignedMemberPolicy: authoringv2.UnassignedMemberGroupAsUnassigned},
		},
		AfterStepID: "qa-source-filter",
	}}
	document.Population = &authoringv2.Population{SelectionRevisionID: "selection_fddd4c3ae159949b4c7f0c7c0e80b713739ae4f390d8d4ebc3944723f4060fc5", Route: []authoringv2.PopulationRouteStep{}}
	workspace.Documents[0] = document
	workspace.Tabs[0].OutputID = "specimens"
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate typed construction workspace: %v", err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(explorerfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	request := ports.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		SelectionMembersCollection: "loom_explorer_selection_members",
		ResolvedInputs: explorercompilation.ResolvedInputs{Populations: []explorercompilation.ResolvedPopulation{{
			OutputID: "specimens", SelectionRevisionID: "selection_fddd4c3ae159949b4c7f0c7c0e80b713739ae4f390d8d4ebc3944723f4060fc5", MembershipDigest: "sha256:selection-members-qa",
			MemberCount: 2, ResourceType: "Specimen", Route: []authoringv2.PopulationRouteStep{},
		}}},
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}
	receipt, err := Compile(context.Background(), request, nil, recipeEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile receipt with typed construction stages: %v", err)
	}
	stages := receipt.ConstructionStages["specimens"]
	if len(stages) != 3 || stages[0].ID != recipe.ConstructionSourceProjectionID || stages[0].Operation != "" || stages[1].ID != "qa-source-filter" || stages[1].InputStageID != recipe.ConstructionSourceProjectionID || stages[1].Operation != "FILTER" || stages[2].ID != recipe.ConstructionCohortGroupStageID || stages[2].InputStageID != "qa-source-filter" || stages[2].Operation != "COHORT_GROUP" {
		t.Fatalf("receipt stages = %#v", stages)
	}
	if len(stages[0].Columns) != 1 || stages[0].Columns[0].ID != document.Columns[0].ColumnID || stages[0].Columns[0].Name != "col_26a18d9205b062fde575cdf7" {
		t.Fatalf("source stage descriptor = %#v", stages[0])
	}
	if stages[0].Columns[0].Cardinality != "optional_one" {
		t.Fatalf("source stage cardinality = %q, want optional_one", stages[0].Columns[0].Cardinality)
	}
	compiledSchemas := receipt.CompiledOutputSchemas["specimens"]
	if len(compiledSchemas) == 0 {
		t.Fatal("receipt omitted lowerer's finalized output schema")
	}
	var compiledColumn *explorer.ReceiptCompiledOutputColumn
	for index := range compiledSchemas {
		if compiledSchemas[index].ID == "group_id" {
			compiledColumn = &compiledSchemas[index]
			break
		}
	}
	if compiledColumn == nil || compiledColumn.Name != "group_id" || compiledColumn.LogicalType != "string" || compiledColumn.Cardinality != "required_one" || compiledColumn.Nullable {
		t.Fatalf("receipt final schema did not preserve the compiler-final output identity and typing: %#v", compiledSchemas)
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
	bindings := recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project-a", DatasetGeneration: snapshot.Identity.Generation,
		AuthScopeMode: authscope.ReadScopeUnrestricted, SelectionMembersCollection: request.SelectionMembersCollection,
	}
	if _, err := CompileValidatedResolution(context.Background(), recipeEngine, receipt, bindings); err != nil {
		t.Fatalf("cohort construction receipt failed deterministic re-lowering: %v", err)
	}
	repeated, err := Compile(context.Background(), request, nil, recipeEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("recompile identical typed construction receipt: %v", err)
	}
	if receipt.CompilationKey != repeated.CompilationKey || receipt.ID != repeated.ID {
		t.Fatalf("construction receipt identity changed across exact recompilation: (%q,%q) != (%q,%q)", receipt.CompilationKey, receipt.ID, repeated.CompilationKey, repeated.ID)
	}
}

func TestCompileExplorerReceiptReconcilesCodedGroupProposalOutputsAndLineage(t *testing.T) {
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a",
			AuthorizationScopeDigest: capabilityresolver.ScopeDigest(readScope),
			SchemaDigest:             strings.Repeat("c", 64), ResourceInventoryDigest: "inventory",
			RelationshipDigest: "relationships", FieldDigest: "fields", ShapeDigest: strings.Repeat("d", 64),
			ProtocolVersion: capabilityresolver.ProtocolVersion, CompilerVersion: capabilityresolver.CompilerVersion,
			TraversalPolicyVersion: capabilityresolver.TraversalPolicyVersion, ProjectionPolicyVersion: capabilityresolver.ProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: capabilityresolver.TraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: capabilityresolver.ProjectionPolicyVersion},
		},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "n_specimen", ResourceType: "Specimen", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil,
		[]capability.Candidate{{
			ID: "c_specimen_id", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "id", Label: "ID",
			LogicalType: "string", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true,
		}, {
			ID: "c_specimen_coding_code", NodeID: "n_specimen", ResourceType: "Specimen", FieldPath: "type.coding[].code", Label: "Code",
			LogicalType: "string", Cardinality: "OPTIONAL_ONE",
			RepeatedBoundaries:  []capability.RepeatedBoundary{{Path: "type.coding[]", MaxItems: 4}},
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true, ObservedDocumentCount: 1,
		}},
		nil,
	)
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document := workspace.Documents[0]
	document.Output.ID = "specimens"
	document.Output.Title = "Specimens"
	document.RootResourceType = "Specimen"
	document.Route.ResourceType = "Specimen"
	document.Columns[0].Column = "specimen_id"
	document.Columns[0].Label = "Specimen ID"
	workspace.Tabs[0].OutputID = "specimens"
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringv2.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringv2.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
	baseWorkspace := workspace
	document = workspace.Documents[0]
	document, err = authoringv2.StartDocumentConstruction(document)
	if err != nil {
		t.Fatal(err)
	}

	const (
		stepID    = "coded_group_step"
		systemID  = "coded_system"
		versionID = "coded_version"
		codeID    = "coded_code"
		countID   = "source_count"
	)
	groupOutputs := []authoringv2.StageColumn{
		{ID: systemID, Name: "system", Label: "System", Type: "string", Nullable: true},
		{ID: versionID, Name: "version", Label: "Version", Type: "string", Nullable: true},
		{ID: codeID, Name: "code", Label: "Code", Type: "string", Nullable: true},
		{ID: countID, Name: "source_count", Label: "Distinct source records", Type: "integer"},
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{
		{
			ID:     stepID,
			Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationCodedGroup,
				CodedGroup: &authoringv2.ConstructionCodedGroup{
					ConstructionID: stepID,
					Source: authoringv2.ConstructionCodedGroupSource{
						OccurrenceID: "base", ResourceType: "Specimen", CodingPath: "type.coding[]",
						FHIRType: "Coding", Cardinality: "MANY", Shape: "ARRAY",
					},
					MissingKeyPolicy:     authoringv2.ConstructionGroupMissingKeyGroup,
					SystemOutputColumnID: systemID, VersionOutputColumnID: versionID,
					CodeOutputColumnID: codeID, DistinctSourceCountOutputColumnID: countID,
				},
			},
			Outputs: groupOutputs,
		},
		{
			ID:     "filter_coded_rows",
			Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: stepID}},
			Operation: authoringv2.ConstructionOperation{
				Kind:   authoringv2.ConstructionOperationFilter,
				Filter: &authoringv2.ConstructionFilter{ColumnID: codeID, Operator: authoringv2.ConstructionFilterExists},
			},
			Outputs: groupOutputs,
		},
	}
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate CODED_GROUP proposal workspace: %v", err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	draftConfig, err := baseWorkspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	draftDigest, err := baseWorkspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Specimens",
		DraftConfig: draftConfig, DraftVersion: 1, DraftDigest: draftDigest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	authorized := ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}
	application, err := lifecycle.New(service, lifecycle.Config{
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil },
			ForExecution:   func(context.Context, string, string) (ports.AuthorizedCapability, error) { return authorized, nil },
			Catalog:        authoringv2.CatalogFromCapability,
		},
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(_ context.Context, _ *explorer.CompilationReceipt, _ recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if err := visit(map[string]any{"system": "https://example.org", "version": nil, "code": "TUMOR", "source_count": 1}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "specimens", Columns: []string{"system", "version", "code", "source_count"}, RowCount: 1, Complete: true}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := ports.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope},
	}
	receipt, err := Compile(context.Background(), request, nil, recipeEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile CODED_GROUP proposal through receipt reconciliation: %v", err)
	}
	contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.ValidateAgainst(receipt.Bundle, receipt.EmittedColumns); err != nil {
		t.Fatalf("validate reconciled CODED_GROUP contract: %v", err)
	}
	if len(receipt.EmittedColumns) != 4 {
		t.Fatalf("reconciled CODED_GROUP emissions = %#v, want the four declared outputs", receipt.EmittedColumns)
	}
	wantPaths := map[string]string{
		"system":       "type.coding[].system",
		"version":      "type.coding[].version",
		"code":         "type.coding[].code",
		"source_count": "type.coding[]",
	}
	for _, emitted := range receipt.EmittedColumns {
		wantPath, exists := wantPaths[emitted.PublicColumn]
		if !exists || emitted.SourceResourceType != "Specimen" || emitted.SourcePath != wantPath || emitted.OccurrenceID != "base" {
			t.Errorf("coded output source facts for %q = %#v, want Specimen %q at root occurrence", emitted.PublicColumn, emitted, wantPath)
		}
		if emitted.ConstructionID != stepID || !reflect.DeepEqual(emitted.InputColumns, []string{"specimen_id"}) ||
			!reflect.DeepEqual(emitted.AuthoredColumns, []string{"specimen_id"}) {
			t.Errorf("coded output lineage for %q = %#v, want root source projection lineage", emitted.PublicColumn, emitted)
		}
		if emitted.Lossless || emitted.StructuralSuitability != "requires-review" {
			t.Errorf("coded output quality for %q = %#v, want non-lossless review", emitted.PublicColumn, emitted)
		}
		delete(wantPaths, emitted.PublicColumn)
	}
	if len(wantPaths) != 0 {
		t.Errorf("receipt omitted coded outputs %v", wantPaths)
	}
	stages := receipt.ConstructionStages["specimens"]
	if len(stages) != 3 || stages[1].Operation != "CODED_GROUP" || stages[2].InputStageID != stepID || stages[2].Operation != "FILTER" {
		t.Fatalf("recompiled proposal stages = %#v, want CODED_GROUP then downstream FILTER", stages)
	}
	repeated, err := Compile(context.Background(), request, nil, recipeEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("recompile saved CODED_GROUP proposal without proposal token: %v", err)
	}
	if receipt.CompilationKey != repeated.CompilationKey || receipt.ID != repeated.ID {
		t.Fatalf("CODED_GROUP saved meaning changed after exact recompile: first=(%q,%q), second=(%q,%q)", receipt.CompilationKey, receipt.ID, repeated.CompilationKey, repeated.ID)
	}

	capabilities, err := application.GetConstructionCapabilities(context.Background(), lifecycle.ConstructionCapabilitiesRequest{
		Project: "project-a", ExplorerID: "custom", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: draftDigest,
		OutputID: "specimens", StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatalf("get coded-group capability choice: %v", err)
	}
	if len(capabilities.SelectedStage.CodedGroupChoices) != 1 || capabilities.SelectedStage.CodedGroupChoices[0].CodingPath != "type.coding[]" {
		t.Fatalf("coded-group capability choices = %#v, want only populated Specimen.type.coding[]", capabilities.SelectedStage.CodedGroupChoices)
	}
	document.Construction.Steps[0].Operation.CodedGroup.ChoiceID = capabilities.SelectedStage.CodedGroupChoices[0].ChoiceID
	proposal, err := application.ProposeConstruction(context.Background(), lifecycle.ConstructionProposalRequest{
		Project: "project-a", ExplorerID: "custom", SnapshotToken: snapshot.Token,
		ExpectedDraftVersion: 1, ExpectedDraftDigest: draftDigest,
		OutputID: "specimens", ChangedStepID: stepID,
		CandidateConstruction: *document.Construction,
	})
	if err != nil {
		t.Fatalf("propose coded grouping through lifecycle compilation: %v", err)
	}
	if proposal.ProposalID == "" || proposal.PreviewStatus != "PREVIEW_PENDING" {
		t.Fatalf("coded-group proposal response = %#v", proposal)
	}
	proposalReceipt, err := service.CompilationReceiptForExplorer(context.Background(), "project-a", "custom", proposal.ProposalID)
	if err != nil || proposalReceipt == nil || proposalReceipt.ConstructionProposal == nil {
		t.Fatalf("coded-group proposal receipt = %#v, error = %v", proposalReceipt, err)
	}
	if len(proposalReceipt.EmittedColumns) != 4 || proposalReceipt.EmittedColumns[0].SourceResourceType != "Specimen" {
		t.Fatalf("proposal receipt did not pass coded outputs through reconciliation: %#v", proposalReceipt.EmittedColumns)
	}
	_, err = application.ApplyCommands(context.Background(), "project-a", "custom", authoringv2.ApplyCommandsRequest{
		CommandID: "apply-coded-group", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: snapshot.Token, ExpectedDraftVersion: 1, ExpectedDraftDigest: draftDigest,
		Commands: []authoringv2.Command{{
			Type: authoringv2.CommandApplyConstructionProposal, OutputID: "specimens", ProposalID: proposal.ProposalID,
		}},
	}, "test")
	if err != nil {
		t.Fatalf("apply coded-group proposal: %v", err)
	}
	updated, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := authoringv2.DecodeWorkspace(updated.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DraftVersion != 2 || accepted.Documents[0].Construction == nil ||
		accepted.Documents[0].Construction.Steps[0].Operation.CodedGroup == nil {
		t.Fatalf("applied draft lost its CODED_GROUP stage: version=%d workspace=%#v", updated.DraftVersion, accepted)
	}
	acceptedRequest := request
	acceptedRequest.Workspace = accepted
	acceptedReceipt, err := Compile(context.Background(), acceptedRequest, nil, recipeEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("recompile applied CODED_GROUP draft without a fresh proposal token: %v", err)
	}
	if acceptedReceipt.ResolvedRecipeDigest != proposalReceipt.ResolvedRecipeDigest ||
		!reflect.DeepEqual(acceptedReceipt.EmittedColumns, proposalReceipt.EmittedColumns) {
		t.Fatalf("applied CODED_GROUP recompile changed recipe or source lineage: proposal=(%q,%#v), accepted=(%q,%#v)",
			proposalReceipt.ResolvedRecipeDigest, proposalReceipt.EmittedColumns,
			acceptedReceipt.ResolvedRecipeDigest, acceptedReceipt.EmittedColumns)
	}
}

func TestCompileExplorerReceiptReconcilesTypedConstructionOutputs(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, authoringv2.Column{
		Column: "patient_count", Label: "Patient count", LogicalType: "integer", OccurrenceID: "base",
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceAggregate, Aggregate: &authoringv2.AggregateSource{Operation: "COUNT"}},
	})
	document, err := authoringv2.StartDocumentConstruction(workspace.Documents[0])
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
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringv2.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringv2.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate typed derived construction: %v", err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(explorerfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	request := ports.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}
	receipt, err := Compile(context.Background(), request, nil, recipeEngine, service, nil, nil)
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

func TestCompileExplorerReceiptBindsRowDefinitionProposalBeforeIdentity(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(explorerfixtures.NewStore())
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
	receipt, err := Compile(context.Background(), ports.CompileReceiptRequest{
		Project:               "project-a",
		ExplorerID:            "custom",
		Workspace:             workspace,
		SnapshotToken:         snapshot.Token,
		Authorized:            ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		RowDefinitionProposal: binding,
	}, nil, recipeEngine, service, nil, nil)
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

func TestResolvedOutputFingerprintExcludesOptimizerAndTransientProvenance(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: recipetest.Registry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "fingerprint", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "patient_id", Expr: recipe.Expression{Select: "root.id"}}}}}}
	resolved, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	beforeArtifacts, _, err := ResolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	before := beforeArtifacts["patients"]
	resolved.Compiled.Outputs[0].OutputSchema[0].Discovered = !resolved.Compiled.Outputs[0].OutputSchema[0].Discovered
	resolved.Compiled.Outputs[0].OptimizedPlan.OptimizationPolicy.Decisions = append(resolved.Compiled.Outputs[0].OptimizedPlan.OptimizationPolicy.Decisions, ir.PhysicalOptimizationDecision{Reason: "diagnostic-only"})
	afterArtifacts, _, err := ResolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	after := afterArtifacts["patients"]
	if before != after {
		t.Fatalf("transient compiler metadata changed canonical fingerprint: %q != %q", before, after)
	}
	resolved.Compiled.Outputs[0].OutputSchema[0].NormalizedUnit = &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	unitArtifacts, _, err := ResolvedOutputArtifacts(resolved)
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
	changedArtifacts, _, err := ResolvedOutputArtifacts(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if changed := changedArtifacts["patients"]; changed == before {
		t.Fatal("execution bind change did not change canonical fingerprint")
	}
}

func TestPreviewSourceIdentityDoesNotChangeReceiptArtifacts(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "preview-source-identity", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
			Fields: []recipe.Field{
				{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
				{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
			},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
				Steps: []recipe.ConstructionStep{{
					ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
						ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
						OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
					}},
					Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
				}},
			},
		}},
	}
	base, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a", IncludeSourceIdentity: true})
	if err != nil {
		t.Fatal(err)
	}
	baseFingerprints, _, err := ResolvedOutputArtifacts(base)
	if err != nil {
		t.Fatal(err)
	}
	previewFingerprints, _, err := ResolvedOutputArtifacts(preview)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseFingerprints, previewFingerprints) {
		t.Fatalf("preview-only source identity changed receipt fingerprints: base=%#v preview=%#v", baseFingerprints, previewFingerprints)
	}
	if !reflect.DeepEqual(base.Compiled.Outputs[0].Stages, preview.Compiled.Outputs[0].Stages) {
		t.Fatalf("preview-only source identity changed frozen construction stages: base=%#v preview=%#v", base.Compiled.Outputs[0].Stages, preview.Compiled.Outputs[0].Stages)
	}
}

func TestPreviewSourceIdentityDoesNotChangeUnpivotReceiptArtifacts(t *testing.T) {
	var renderedQuery string
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: recipetest.Registry{}, QueryRows: func(_ context.Context, query string, _ int, _ map[string]any, _ func(map[string]any) error) error {
		renderedQuery = query
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	stringKeyOne, stringKeyTwo := "subject", "body_site"
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "unpivot-preview-source-identity", TranslationVersion: "test",
		Outputs: []recipe.Output{{
			Name: "specimens", RootResourceType: "Specimen", RowGrain: "resource",
			Fields: []recipe.Field{
				{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "subject_reference", ColumnID: "subject_reference", Expr: recipe.Expression{Select: "root.subject.reference"}},
				{Name: "body_site_reference", ColumnID: "body_site_reference", Expr: recipe.Expression{Select: "root.collection.bodySite.reference.reference"}},
			},
			Construction: &recipe.Construction{
				Version: 1,
				SourceColumns: []recipe.StageColumn{
					{ID: "specimen_id", Name: "specimen_id", Type: "string"},
					{ID: "subject_reference", Name: "subject_reference", Type: "string"},
					{ID: "body_site_reference", Name: "body_site_reference", Type: "string"},
				},
				Steps: []recipe.ConstructionStep{{
					ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{
						ConstructionID: "unpivot", Inputs: []recipe.ConstructionUnpivotInput{
							{ColumnID: "subject_reference", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringKeyOne}},
							{ColumnID: "body_site_reference", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &stringKeyTwo}},
						},
						KeyOutputColumnID: "key", ValueOutputColumnID: "value", NullRowPolicy: recipe.UnpivotNullPreserve,
					}},
					Outputs: []recipe.StageColumn{
						{ID: "specimen_id", Name: "specimen_id", Type: "string"},
						{ID: "key", Name: "variable", Type: "string"},
						{ID: "value", Name: "value", Type: "string"},
					},
				}},
			},
		}},
	}
	baseBindings := recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: "generation-a"}
	base, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, baseBindings)
	if err != nil {
		t.Fatal(err)
	}
	previewBindings := baseBindings
	previewBindings.IncludeRowIdentity = true
	previewBindings.IncludeSourceIdentity = true
	preview, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, previewBindings)
	if err != nil {
		t.Fatal(err)
	}
	baseFingerprints, _, err := ResolvedOutputArtifacts(base)
	if err != nil {
		t.Fatal(err)
	}
	previewFingerprints, _, err := ResolvedOutputArtifacts(preview)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseFingerprints, previewFingerprints) {
		t.Fatalf("preview-only identity bindings changed Unpivot receipt fingerprint: base=%#v preview=%#v", baseFingerprints, previewFingerprints)
	}
	_, provenance, err := ResolvedOutputArtifacts(base)
	if err != nil {
		t.Fatal(err)
	}
	stages, err := ConstructionStages(&base)
	if err != nil {
		t.Fatal(err)
	}
	bundleDigest, err := base.Bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := explorer.CompilationReceipt{
		Bundle: base.Bundle, RecipeDigest: base.StoredRecipeDigest, ResolvedRecipeDigest: bundleDigest,
		ResolvedSchemaDigest: base.ResolvedSchemaDigest, OutputFingerprints: baseFingerprints,
		OutputColumnProvenance: provenance, ConstructionStages: stages,
		EmittedColumns: []explorer.EmittedColumn{
			{OutputID: "specimens", PublicColumn: "specimen_id"},
			{OutputID: "specimens", PublicColumn: "variable"},
			{OutputID: "specimens", PublicColumn: "value"},
		},
	}
	rawReceipt, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.CompilationReceipt
	if err := json.Unmarshal(rawReceipt, &stored); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileValidatedResolution(context.Background(), recipeEngine, &stored, previewBindings); err != nil {
		t.Fatalf("JSON round-trip Unpivot receipt did not recompile under Builder preview bindings: %v", err)
	}
	if _, err := recipeEngine.PreviewOutput(context.Background(), preview, dataframeexecution.PreviewRequest{
		Output: "specimens", Limit: 25, IncludeRowIdentity: true,
	}, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("Builder preview failed while lowering the Unpivot execution plan: %v", err)
	}
	if !strings.Contains(renderedQuery, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("Builder preview query omitted source identity projection: %q", renderedQuery)
	}
}

func TestCompiledExplorerWorkspaceConfigPreservesSemanticOrderForPresentationTies(t *testing.T) {
	compiled := explorercompilation.WorkspaceResult{
		Workspace: authoringv2.Workspace{
			APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
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
	raw, err := CompiledWorkspaceConfigV2("project-a", "explorer-a", compiled)
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

func TestCompileExplorerReceiptAllowsEmptyConstructionTargetWithSiblingOutputs(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	template := workspace.Documents[0]
	emptyTarget := template
	emptyTarget.Output = authoringv2.Output{ID: "empty_target", Title: "Empty target"}
	emptyTarget.Columns = []authoringv2.Column{}
	emptyTarget, err = authoringv2.StartDocumentConstruction(emptyTarget)
	if err != nil {
		t.Fatal(err)
	}
	emptyTarget.Construction.Steps = []authoringv2.ConstructionStep{}
	workspace.Documents = []authoringv2.Document{emptyTarget}
	workspace.Tabs = []authoringv2.Tab{{ID: "tab_empty_target", Title: "Empty target", OutputID: "empty_target", Order: 0, Visible: true}}
	for index := 1; index <= 3; index++ {
		document := template
		document.Output = authoringv2.Output{ID: fmt.Sprintf("source_%d", index), Title: fmt.Sprintf("Source %d", index)}
		document.Columns = append([]authoringv2.Column(nil), template.Columns...)
		document.Columns[0].ColumnID = fmt.Sprintf("source_column_%d", index)
		document.Columns[0].Column = fmt.Sprintf("patient_id_%d", index)
		workspace.Documents = append(workspace.Documents, document)
		workspace.Tabs = append(workspace.Tabs, authoringv2.Tab{ID: fmt.Sprintf("tab_source_%d", index), Title: fmt.Sprintf("Source %d", index), OutputID: document.Output.ID, Order: index, Visible: true})
	}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate empty target plus source siblings: %v", err)
	}

	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(explorerfixtures.NewStore())
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := Compile(context.Background(), ports.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}, nil, recipeEngine, service, nil, nil)
	if err != nil {
		t.Fatalf("compile and persist receipt for empty target with sibling inputs: %v", err)
	}
	contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Bundle.Outputs) != 4 || len(contracts.Outputs) != 4 {
		t.Fatalf("receipt outputs/contracts = %d/%d, want target plus all three siblings", len(receipt.Bundle.Outputs), len(contracts.Outputs))
	}
	if empty := contracts.Outputs[0]; empty.OutputID != "empty_target" || len(empty.Columns) != 0 || empty.StructuralSuitability != "" {
		t.Fatalf("empty target contract = %#v, want empty compiler-owned contract", empty)
	}
	for index := 1; index <= 3; index++ {
		wantOutput, wantColumn := fmt.Sprintf("source_%d", index), fmt.Sprintf("patient_id_%d", index)
		contract := contracts.Outputs[index]
		if receipt.Bundle.Outputs[index].Name != wantOutput || contract.OutputID != wantOutput || len(contract.Columns) != 1 || contract.Columns[0].Column != wantColumn {
			t.Fatalf("sibling %d receipt output/contract = %#v / %#v", index, receipt.Bundle.Outputs[index], contract)
		}
	}
	if err := contracts.ValidateAgainst(receipt.Bundle, receipt.EmittedColumns); err != nil {
		t.Fatalf("validate persisted multi-output receipt contract: %v", err)
	}
}

func TestCompileExplorerReceiptReconcilesSparsePublishedAppendWithExactSourceSchema(t *testing.T) {
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := testAuthoringV2CapabilitySnapshot()
	bindings := recipe.RuntimeBindings{
		Project: "project-a", DatasetGeneration: snapshot.Identity.Generation,
		AuthScopeMode: readScope.Mode,
	}
	left := recipefixtures.CombineInputReference("labs", "source_one", "execution-left")
	right := recipefixtures.CombineInputReference("labs", "source_two", "execution-right")
	sparse := recipefixtures.CombineInputReference("labs", "source_three", "execution-sparse")
	leftMaterialization := recipefixtures.CombineInputMaterialization(left, bindings, reciperuntime.ScopeDigest(bindings))
	leftMaterialization.Columns = []published.Column{
		{ID: "left-row", Name: "__loom_row_id", ClickHouse: "String", LogicalType: "string", LoomOwned: true},
		{ID: "left-display-name", Name: "display_name", ClickHouse: "String", LogicalType: "string"},
	}
	rightMaterialization := recipefixtures.CombineInputMaterialization(right, bindings, reciperuntime.ScopeDigest(bindings))
	rightMaterialization.Columns = []published.Column{
		{ID: "right-row", Name: "__loom_row_id", ClickHouse: "String", LogicalType: "string", LoomOwned: true},
		{ID: "right-display-name", Name: "display_name", ClickHouse: "Nullable(String)", LogicalType: "string", Nullable: true},
	}
	sparseMaterialization := recipefixtures.CombineInputMaterialization(sparse, bindings, reciperuntime.ScopeDigest(bindings))
	sparseMaterialization.Columns = []published.Column{
		{ID: "sparse-row", Name: "__loom_row_id", ClickHouse: "String", LogicalType: "string", LoomOwned: true},
		{ID: "sparse-other", Name: "other_value", ClickHouse: "String", LogicalType: "string"},
	}
	reader := &recipefixtures.CombineMaterializationReader{Values: map[string]published.Materialization{
		left.RevisionID + "/" + left.OutputID:     leftMaterialization,
		right.RevisionID + "/" + right.OutputID:   rightMaterialization,
		sparse.RevisionID + "/" + sparse.OutputID: sparseMaterialization,
	}}
	var pinned []string
	withPins := dataframeexecution.WithExecutionReadPins(func(ctx context.Context, executionIDs []string, visit func(context.Context) error) error {
		pinned = append([]string(nil), executionIDs...)
		return visit(ctx)
	})
	resolveSchemas := func(ctx context.Context, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings) (ResolvedCombineInputSchema, error) {
		return ExactPublishedCombineInputSchemas(ctx, output, bindings, reader, withPins, nil)
	}

	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Rows:             authoringv2.RecordsRowDefinition(),
		Output:           authoringv2.Output{ID: "combined", Title: "Combined"},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: "append_sources",
			Inputs: []authoringv2.ConstructionInputRef{
				{Kind: authoringv2.ConstructionInputTableRevision, TableID: left.TableID, RevisionID: left.RevisionID, OutputID: left.OutputID},
				{Kind: authoringv2.ConstructionInputTableRevision, TableID: right.TableID, RevisionID: right.RevisionID, OutputID: right.OutputID},
				{Kind: authoringv2.ConstructionInputTableRevision, TableID: sparse.TableID, RevisionID: sparse.RevisionID, OutputID: sparse.OutputID},
			},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
				Kind: authoringv2.ConstructionCombineAppend,
				Projections: []authoringv2.ConstructionCombineProjection{
					{OutputColumnID: "display-name", InputIndex: 0, InputColumnID: "left-display-name"},
					{OutputColumnID: "display-name", InputIndex: 1, InputColumnID: "right-display-name"},
				},
			}},
			Outputs: []authoringv2.StageColumn{{ID: "display-name", Name: "display_name", Label: "Display name", Type: "string", Nullable: true}},
		}}},
	}
	templateWorkspace, err := authoringv2.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	sourceIDs := []string{left.OutputID, right.OutputID, sparse.OutputID}
	sourceDocuments := make([]authoringv2.Document, 0, len(sourceIDs))
	tabs := make([]authoringv2.Tab, 0, len(sourceIDs)+1)
	for index, outputID := range sourceIDs {
		source := templateWorkspace.Documents[0]
		source.Output = authoringv2.Output{ID: outputID, Title: outputID}
		source.Columns = append([]authoringv2.Column(nil), source.Columns...)
		source.Columns[0].ColumnID = fmt.Sprintf("source_column_%d", index+1)
		source.Columns[0].Column = fmt.Sprintf("patient_id_%d", index+1)
		source, err = authoringv2.StartDocumentConstruction(source)
		if err != nil {
			t.Fatalf("upgrade non-Combine source output %q: %v", outputID, err)
		}
		sourceDocuments = append(sourceDocuments, source)
		tabs = append(tabs, authoringv2.Tab{ID: fmt.Sprintf("%s-tab", outputID), Title: outputID, OutputID: outputID, Order: index, Visible: true})
	}
	documents := append(sourceDocuments, document)
	tabs = append(tabs, authoringv2.Tab{ID: "combined-tab", Title: "Combined", OutputID: "combined", Order: len(tabs), Visible: true})
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer:  authoringv2.ExplorerMetadata{Title: "Combined"},
		Documents: documents,
		Tabs:      tabs,
	}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate target plus three source outputs: %v", err)
	}

	engine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  recipetest.Registry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	store := explorerfixtures.NewStore()
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	request := ports.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope},
	}
	receipt, err := Compile(context.Background(), request, nil, engine, service, nil, resolveSchemas)
	if err != nil {
		t.Fatalf("compile sparse published APPEND receipt: %v", err)
	}
	if !reflect.DeepEqual(reader.Calls, []string{
		"execution-left/source_one", "execution-right/source_two", "execution-sparse/source_three",
	}) || !reflect.DeepEqual(pinned, []string{"execution-left", "execution-right", "execution-sparse"}) {
		t.Fatalf("exact metadata resolution calls=%#v pinned=%#v", reader.Calls, pinned)
	}
	if len(receipt.Bundle.Outputs) != 4 {
		t.Fatalf("receipt output count = %d, want three source outputs plus Combine target", len(receipt.Bundle.Outputs))
	}
	var combinedOutput *recipe.Output
	for index := range receipt.Bundle.Outputs {
		if receipt.Bundle.Outputs[index].Name == "combined" {
			combinedOutput = &receipt.Bundle.Outputs[index]
			break
		}
	}
	if combinedOutput == nil || combinedOutput.Construction == nil {
		t.Fatalf("Combine target recipe = %#v", combinedOutput)
	}
	step := combinedOutput.Construction.Steps[0]
	for _, outputID := range sourceIDs {
		stages := receipt.ConstructionStages[outputID]
		if len(stages) != 1 || stages[0].ID != recipe.ConstructionSourceProjectionID || stages[0].InputStageID != "" || stages[0].Operation != "" {
			t.Fatalf("non-Combine source output %q stages = %#v", outputID, stages)
		}
	}
	combineStages := receipt.ConstructionStages["combined"]
	if len(combineStages) != 1 || combineStages[0].ID != "append_sources" || combineStages[0].InputStageID != "" || combineStages[0].Operation != "APPEND" {
		t.Fatalf("external APPEND first stage = %#v", combineStages)
	}
	if err := receipt.Validate(); err != nil {
		t.Fatalf("validate sparse APPEND receipt before/after persistence: %v", err)
	}
	persisted, err := service.CompilationReceiptForExplorer(context.Background(), "project-a", "custom", receipt.ID)
	if err != nil {
		t.Fatalf("load persisted sparse APPEND receipt: %v", err)
	}
	if err := persisted.Validate(); err != nil {
		t.Fatalf("validate persisted sparse APPEND receipt: %v", err)
	}
	if !reflect.DeepEqual(persisted.ConstructionStages, receipt.ConstructionStages) {
		t.Fatalf("persisted receipt stage contract = %#v, want %#v", persisted.ConstructionStages, receipt.ConstructionStages)
	}
	wantInputs := []recipe.ConstructionInputRef{
		{Kind: recipe.ConstructionTableRevisionInput, TableID: left.TableID, RevisionID: left.RevisionID, OutputID: left.OutputID},
		{Kind: recipe.ConstructionTableRevisionInput, TableID: right.TableID, RevisionID: right.RevisionID, OutputID: right.OutputID},
		{Kind: recipe.ConstructionTableRevisionInput, TableID: sparse.TableID, RevisionID: sparse.RevisionID, OutputID: sparse.OutputID},
	}
	wantProjections := []recipe.ConstructionCombineProjection{
		{OutputColumnID: "display-name", InputIndex: 0, InputColumnID: "left-display-name"},
		{OutputColumnID: "display-name", InputIndex: 1, InputColumnID: "right-display-name"},
	}
	if !reflect.DeepEqual(step.Inputs, wantInputs) || step.Operation.Combine == nil || !reflect.DeepEqual(step.Operation.Combine.Projections, wantProjections) {
		t.Fatalf("receipt lost typed exact APPEND bindings: inputs=%#v combine=%#v", step.Inputs, step.Operation.Combine)
	}
	var combinedEmissions []explorer.EmittedColumn
	for _, emitted := range receipt.EmittedColumns {
		if emitted.OutputID == "combined" {
			combinedEmissions = append(combinedEmissions, emitted)
		}
	}
	if len(combinedEmissions) != 1 {
		t.Fatalf("Combine target emitted columns = %#v", combinedEmissions)
	}
	emitted := combinedEmissions[0]
	if emitted.PublicColumn != "display_name" || emitted.Label != "Display name" || emitted.ConstructionID != "append_sources" ||
		!reflect.DeepEqual(emitted.InputColumns, []string{"display_name"}) || len(emitted.AuthoredColumns) != 0 ||
		emitted.LogicalType != "string" || emitted.Cardinality != "optional_one" || !emitted.Nullable {
		t.Fatalf("reconciled sparse APPEND emission = %#v", emitted)
	}
	var finalSchema *explorer.ReceiptCompiledOutputColumn
	for index := range receipt.CompiledOutputSchemas["combined"] {
		column := &receipt.CompiledOutputSchemas["combined"][index]
		if column.ID == "display-name" {
			finalSchema = column
			break
		}
	}
	if finalSchema == nil || finalSchema.Name != emitted.PublicColumn || finalSchema.LogicalType != "string" || finalSchema.Cardinality != "optional_one" || !finalSchema.Nullable {
		t.Fatalf("receipt compiler-final sparse schema = %#v", receipt.CompiledOutputSchemas["combined"])
	}
	contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		t.Fatal(err)
	}
	if err := contracts.ValidateAgainst(receipt.Bundle, receipt.EmittedColumns); err != nil {
		t.Fatalf("validate reconciled sparse APPEND contract: %v", err)
	}
}
