package server

import (
	"context"
	"encoding/json"
	"errors"
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

func TestClassifyReceiptRecipeErrorPreservesPivotReducerValidation(t *testing.T) {
	cause := fmt.Errorf("compile output: %w", &lower.PivotReducerTypeError{Policy: recipe.PivotDuplicateMin, ValueColumn: "specimen_id"})
	classified := classifyReceiptRecipeError(cause)
	var diagnostic *explorercompilation.Error
	if !errors.As(classified, &diagnostic) || diagnostic.Code != "PIVOT_REDUCER_REQUIRES_NUMERIC" || diagnostic.Stage != "construction" {
		t.Fatalf("classified error = %v, want construction validation diagnostic", classified)
	}
	if !errors.Is(classified, cause) {
		t.Fatalf("classified error lost original cause: %v", classified)
	}
	unexpected := errors.New("storage failed")
	if got := classifyReceiptRecipeError(unexpected); got != unexpected {
		t.Fatalf("unrelated error = %v, want original error", got)
	}
}

func TestClassifyReceiptRecipeErrorIdentifiesInvalidRelatedRowAnchor(t *testing.T) {
	cause := fmt.Errorf("compile output: %w", &lower.RelatedEligibilityAnchorError{StepID: "related_step"})
	classified := classifyReceiptRecipeError(cause)
	var diagnostic *explorercompilation.Error
	if !errors.As(classified, &diagnostic) || diagnostic.Code != "CONSTRUCTION_ANCHOR_INVALID" || diagnostic.Stage != "construction" {
		t.Fatalf("classified error = %v, want related row anchor diagnostic", classified)
	}
	if diagnostic.Details["stepId"] != "related_step" || !errors.Is(classified, cause) {
		t.Fatalf("classified error lost the affected step or cause: %#v", diagnostic)
	}
}

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
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: explorerScopeDigest(readScope),
			SchemaDigest: strings.Repeat("c", 64), ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships",
			FieldDigest: "fields", ShapeDigest: strings.Repeat("d", 64), ProtocolVersion: explorerCapabilityProtocolVersion,
			CompilerVersion: explorerCapabilityCompilerVersion, TraversalPolicyVersion: explorerTraversalPolicyVersion,
			ProjectionPolicyVersion: explorerProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion},
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
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
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
	document, err = authoringv2.UpgradeDocumentToConstruction(document)
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
		SelectionMembersCollection: "loom_explorer_selection_members",
		ResolvedInputs: explorercompilation.ResolvedInputs{Populations: []explorercompilation.ResolvedPopulation{{
			OutputID: "specimens", SelectionRevisionID: "selection_fddd4c3ae159949b4c7f0c7c0e80b713739ae4f390d8d4ebc3944723f4060fc5", MembershipDigest: "sha256:selection-members-qa",
			MemberCount: 2, ResourceType: "Specimen", Route: []authoringv2.PopulationRouteStep{},
		}}},
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
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
	if _, err := compileValidatedReceiptResolution(context.Background(), recipeEngine, receipt, bindings); err != nil {
		t.Fatalf("cohort construction receipt failed deterministic re-lowering: %v", err)
	}
	repeated, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
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
			AuthorizationScopeDigest: explorerScopeDigest(readScope),
			SchemaDigest:             strings.Repeat("c", 64), ResourceInventoryDigest: "inventory",
			RelationshipDigest: "relationships", FieldDigest: "fields", ShapeDigest: strings.Repeat("d", 64),
			ProtocolVersion: explorerCapabilityProtocolVersion, CompilerVersion: explorerCapabilityCompilerVersion,
			TraversalPolicyVersion: explorerTraversalPolicyVersion, ProjectionPolicyVersion: explorerProjectionPolicyVersion,
		},
		capability.Policy{
			Route:      capability.RoutePolicy{Version: explorerTraversalPolicyVersion, AllowsRepeatedEdges: true, AllowsSelfLoops: true},
			Projection: capability.ProjectionPolicy{Version: explorerProjectionPolicyVersion},
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
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
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
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	baseWorkspace := workspace
	document = workspace.Documents[0]
	document, err = authoringv2.UpgradeDocumentToConstruction(document)
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
		Registry:  compilerTestRegistry{},
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
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Specimens",
		DraftConfig: draftConfig, DraftVersion: 1, DraftDigest: draftDigest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	authorized := lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}
	application, err := lifecycle.New(service, lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) { return authorized, nil },
			ForExecution:   func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) { return authorized, nil },
			Catalog:        authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil)
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
	request := lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "custom", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope},
	}
	receipt, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
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
	repeated, err := compileExplorerReceipt(context.Background(), request, nil, recipeEngine, service, nil)
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
	acceptedReceipt, err := compileExplorerReceipt(context.Background(), acceptedRequest, nil, recipeEngine, service, nil)
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

func TestConstructionStageWithoutRelatedAnchorsSurvivesReceiptJSON(t *testing.T) {
	stage, err := receiptConstructionStageFromDescriptor(lower.CompiledStageDescriptor{ID: "group", Operation: "GROUP"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stage)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.ReceiptConstructionStage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stage, stored) {
		t.Fatalf("stage changed across receipt JSON: before=%#v after=%#v", stage, stored)
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

func TestRelatedSourceOutputProfileMatchesSelectedForm(t *testing.T) {
	for _, test := range []struct {
		name        string
		form        capability.ConstructionChoiceForm
		kind        string
		cardinality string
		shape       string
	}{
		{name: "list", form: capability.ConstructionChoiceAll, kind: "string", cardinality: "many", shape: "array"},
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
						Form: test.form, OutputColumnID: "observation-column",
						ContributorRule: authoringv2.ConstructionRelatedContributorRule{Policy: authoringv2.ConstructionRelatedAllMatches},
					}},
					Outputs: []authoringv2.StageColumn{{ID: "observation-column", Name: columnName, Label: "Related observation", Type: test.kind}},
				}}},
			}
			authored := map[string]authoredOutputColumn{}
			if err := authoredConstructionOutputs(document, authored); err != nil {
				t.Fatal(err)
			}
			profile, err := constructedOutputProfileFor(authored[columnName].Quality, lower.CompiledOutputColumn{
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
	authored := map[string]authoredOutputColumn{}
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
	authored := make(map[string]authoredOutputColumn)
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
	authored = make(map[string]authoredOutputColumn)
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
	lineage, err := resolveAuthoredOutputLineage(final, emitted)
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
		Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{
			Name: "body-structures", OutputSchema: []lower.CompiledOutputColumn{{
				Name: columnName, Kind: "string", Cardinality: "optional_one", Nullable: true,
			}},
		}}},
	}

	reconciled, err := reconcileFinalOutputMetadata(translated, resolved)
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
	if grouped.Lossless || grouped.MLReady || grouped.StructuralSuitability != "requires-review" || !reflect.DeepEqual(grouped.LossReasons, []string{"TABLE_SHAPE_GROUP_CHANGES_ROW_GRAIN", tableShapeMLReadinessUnassessed}) {
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
		Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{
			Name: "body-structures",
			OutputSchema: []lower.CompiledOutputColumn{
				{Name: keyOutputName, SemanticPath: "construction:group_by_active", Kind: "boolean", Cardinality: "optional_one", Nullable: true},
				{Name: countOutputName, SemanticPath: "construction:group_by_active", Kind: "integer", Cardinality: "required_one"},
				{Name: "internal_identity", SemanticPath: "internal:identity", Kind: "string", Cardinality: "required_one", Identity: true},
				{Name: "__loom_row_id", SemanticPath: "internal:row", Kind: "object", Cardinality: "required_one", Internal: true, Identity: true},
			},
		}}},
	}

	reconciled, err := reconcileFinalOutputMetadata(translated, resolved)
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
		Compiled: lower.CompiledRecipe{Outputs: []lower.CompiledRecipeOutput{{
			Name: outputID,
			OutputSchema: []lower.CompiledOutputColumn{
				{Name: "group_revision_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
				{Name: "group_id", Kind: "string", Cardinality: "required_one", Identity: true},
				{Name: "group_label", SemanticPath: "groups.label", Kind: "string", Cardinality: "required_one"},
				{Name: "group_ordinal", SemanticPath: "groups.ordinal", Kind: "integer", Cardinality: "required_one"},
				{Name: "members", SemanticPath: "groups.members", Kind: "object", Cardinality: "many"},
				{Name: "__loom_row_id", Kind: "object", Cardinality: "required_one", Internal: true, Identity: true},
			},
		}}},
	}

	reconciled, err := reconcileFinalOutputMetadata(translated, resolved)
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
			!reflect.DeepEqual(emitted.LossReasons, []string{"TABLE_SHAPE_GROUP_CHANGES_ROW_GRAIN", tableShapeMLReadinessUnassessed}) {
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
		memberResolved.Compiled.Outputs = append([]lower.CompiledRecipeOutput(nil), resolved.Compiled.Outputs...)
		memberResolved.Compiled.Outputs[0].OutputSchema = append(append([]lower.CompiledOutputColumn(nil), compiledOutput.OutputSchema...), lower.CompiledOutputColumn{
			Name: source.PublicColumn, Kind: source.LogicalType, Cardinality: source.Cardinality, Nullable: source.Nullable,
		})
		got, err := reconcileFinalOutputMetadata(withMember, memberResolved)
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
	compiledOutput.OutputSchema = append(append([]lower.CompiledOutputColumn(nil), compiledOutput.OutputSchema...), lower.CompiledOutputColumn{
		Name: "group_count", Kind: "integer", Cardinality: "required_one",
	})
	resolvedWithUnknownGroupField := resolved
	resolvedWithUnknownGroupField.Compiled.Outputs = []lower.CompiledRecipeOutput{compiledOutput}
	if _, err := reconcileFinalOutputMetadata(translated, resolvedWithUnknownGroupField); err == nil || !strings.Contains(err.Error(), "no translated emission or authored table-shape identity") {
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
	profile, err := constructedOutputProfileFor(field.Quality, lower.CompiledOutputColumn{Name: status.Name, Kind: "string", Cardinality: "optional_one"})
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

func TestCompileValidatedReceiptResolutionSurvivesCohortConstructionRoundTrip(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry: compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	filterValue := "specimen-a"
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "cohort-receipt-round-trip",
		TranslationVersion:  explorercompilation.TranslationVersion,
		Outputs: []recipe.Output{{
			Name: "specimens", RootResourceType: "Specimen", RowGrain: "groups",
			Fields: []recipe.Field{{Name: "specimen_id", ColumnID: "specimen_id", Expr: recipe.Expression{Select: "root.id"}}},
			Construction: &recipe.Construction{
				Version:       1,
				SourceColumns: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
				Steps: []recipe.ConstructionStep{{
					ID:     "qa-source-filter",
					Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
						ColumnID: "specimen_id", Operator: recipe.FilterEquals,
						Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &filterValue}},
					}},
					Outputs: []recipe.StageColumn{{ID: "specimen_id", Name: "specimen_id", Type: "string"}},
				}},
			},
			GroupRows: &recipe.GroupRows{
				RevisionID: "grouprev_qa", UnassignedMemberPolicy: "GROUP_AS_UNASSIGNED", AfterStepID: "qa-source-filter",
				RowValues: []recipe.GroupRowValuePolicy{{ColumnID: "specimen_id", Policy: recipe.ConstructionRowValueOne}},
			},
		}},
	}
	bindings := recipe.RuntimeBindings{
		Project: "project-a", SelectionProject: "project-a", DatasetGeneration: "generation-a",
		AuthScopeMode: authscope.ReadScopeUnrestricted,
	}
	compiled, err := recipeEngine.CompileResolvedBundle(context.Background(), bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	fingerprints, provenance, err := resolvedOutputArtifacts(compiled)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := compiled.Bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	stages, err := receiptConstructionStages(&compiled)
	if err != nil {
		t.Fatal(err)
	}
	receipt := explorer.CompilationReceipt{
		Bundle: compiled.Bundle, RecipeDigest: compiled.StoredRecipeDigest, ResolvedRecipeDigest: digest,
		ResolvedSchemaDigest: compiled.ResolvedSchemaDigest, OutputFingerprints: fingerprints,
		OutputColumnProvenance: provenance, ConstructionStages: stages,
		EmittedColumns: []explorer.EmittedColumn{{OutputID: "specimens", PublicColumn: "group_label"}},
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored explorer.CompilationReceipt
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if _, err := compileValidatedReceiptResolution(context.Background(), recipeEngine, &stored, bindings); err != nil {
		t.Fatalf("cohort construction receipt failed deterministic re-lowering: %v", err)
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

func TestPreviewSourceIdentityDoesNotChangeReceiptArtifacts(t *testing.T) {
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		Registry:  compilerTestRegistry{},
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
	baseFingerprints, _, err := resolvedOutputArtifacts(base)
	if err != nil {
		t.Fatal(err)
	}
	previewFingerprints, _, err := resolvedOutputArtifacts(preview)
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
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: func(_ context.Context, query string, _ int, _ map[string]any, _ func(map[string]any) error) error {
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
	baseFingerprints, _, err := resolvedOutputArtifacts(base)
	if err != nil {
		t.Fatal(err)
	}
	previewFingerprints, _, err := resolvedOutputArtifacts(preview)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseFingerprints, previewFingerprints) {
		t.Fatalf("preview-only identity bindings changed Unpivot receipt fingerprint: base=%#v preview=%#v", baseFingerprints, previewFingerprints)
	}
	_, provenance, err := resolvedOutputArtifacts(base)
	if err != nil {
		t.Fatal(err)
	}
	stages, err := receiptConstructionStages(&base)
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
	if _, err := compileValidatedReceiptResolution(context.Background(), recipeEngine, &stored, previewBindings); err != nil {
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
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCalls++
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("builder preview context has no deadline")
			}
			if receipt == nil || bindings.AuthScopeMode != authscope.ReadScopeUnrestricted || bindings.IncludeAuthResourcePath || !bindings.IncludeRowIdentity || !bindings.IncludeSourceIdentity {
				t.Fatalf("preview bindings changed authorization or omitted preview-only row metadata: receipt=%#v bindings=%#v", receipt, bindings)
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
