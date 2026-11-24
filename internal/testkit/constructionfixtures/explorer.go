package constructionfixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
)

func ConstructionStageDescriptors(workspace ws.Workspace, outputID string) []receiptmodel.ReceiptConstructionStage {
	index := documentIndex(workspace, outputID)
	if index < 0 {
		return nil
	}
	document := workspace.Documents[index]
	columns := make([]receiptmodel.ReceiptConstructionStageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, receiptmodel.ReceiptConstructionStageColumn{
			ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType, Cardinality: expression.OptionalOne,
		})
	}
	allChoices := func() []receiptmodel.ReceiptConstructionOperationChoice {
		return []receiptmodel.ReceiptConstructionOperationChoice{
			{Kind: "PIVOT", Supported: true}, {Kind: "DERIVE", Supported: true},
			{Kind: "FILTER", Supported: true}, {Kind: "UNPIVOT", Supported: true},
			{Kind: "RELATED_EXPAND", Supported: true},
		}
	}
	rootAnchor := receiptmodel.ReceiptConstructionRelatedExpandAnchor{
		AnchorColumnID: "_key", Kind: "root", ResourceType: document.RootResourceType,
		Label: "Original " + document.RootResourceType,
	}
	stages := []receiptmodel.ReceiptConstructionStage{{
		ID: recipe.ConstructionSourceProjectionID, Columns: columns, Capabilities: allChoices(),
		RelatedExpandAnchors: []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor},
	}}
	var activeAnchor *receiptmodel.ReceiptConstructionActiveRelatedRecord
	if document.Construction == nil {
		return stages
	}
	for _, step := range document.Construction.Steps {
		stageColumns := make([]receiptmodel.ReceiptConstructionStageColumn, 0, len(step.Outputs))
		for _, column := range step.Outputs {
			stageColumns = append(stageColumns, receiptmodel.ReceiptConstructionStageColumn{
				ID: column.ID, Name: column.Name, Label: column.Label, Type: column.Type, Cardinality: expression.OptionalOne,
			})
		}
		inputStageID := recipe.ConstructionSourceProjectionID
		if len(stages) > 1 {
			inputStageID = stages[len(stages)-1].ID
		}
		stage := receiptmodel.ReceiptConstructionStage{
			ID: step.ID, InputStageID: inputStageID, Operation: string(step.Operation.Kind),
			Columns: stageColumns, Capabilities: allChoices(),
		}
		if related := step.Operation.RelatedExpand; related != nil {
			route := make([]recipe.ConstructionRelatedRouteStep, 0, len(related.Route))
			for _, hop := range related.Route {
				route = append(route, recipe.ConstructionRelatedRouteStep{
					EdgeID: hop.EdgeID, FromNodeID: hop.FromNodeID, ToNodeID: hop.ToNodeID,
					FromResourceType: hop.FromResourceType, ToResourceType: hop.ToResourceType,
					Relationship: hop.Relationship, StorageDirection: hop.StorageDirection, MatchMode: hop.MatchMode,
				})
			}
			stage.RelatedExpand = &receiptmodel.ReceiptConstructionRelatedExpand{
				AnchorColumnID: related.AnchorColumnID, AnchorColumn: related.AnchorColumnID,
				AnchorKind: "root", AnchorNodeID: related.Route[0].FromNodeID,
				AnchorResourceType:     related.Route[0].FromResourceType,
				RelatedRecordColumnID:  related.RelatedRecordColumnID,
				ParentIdentityColumnID: "__test_parent_identity", ParentIdentityColumn: "__test_parent_identity",
				TerminalIdentityColumn: "__test_terminal_identity", TargetNodeID: related.TargetNodeID,
				TargetResourceType: related.TargetResourceType, Route: route,
			}
			activeAnchor = &receiptmodel.ReceiptConstructionActiveRelatedRecord{
				TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
				TerminalIdentityColumn: "__test_terminal_identity",
			}
		} else if step.Operation.Kind != ws.ConstructionOperationFilter &&
			step.Operation.Kind != ws.ConstructionOperationDerive &&
			step.Operation.Kind != ws.ConstructionOperationRelatedSource &&
			step.Operation.Kind != ws.ConstructionOperationRelatedField {
			activeAnchor = nil
		}
		stage.RelatedExpandAnchors = []receiptmodel.ReceiptConstructionRelatedExpandAnchor{rootAnchor}
		if activeAnchor != nil {
			stage.ActiveRelatedRecord = activeAnchor
			stage.RelatedExpandAnchors = append(stage.RelatedExpandAnchors, receiptmodel.ReceiptConstructionRelatedExpandAnchor{
				AnchorColumnID: activeAnchor.TerminalIdentityColumn, Kind: "activeRelatedRecord",
				NodeID: activeAnchor.TargetNodeID, ResourceType: activeAnchor.TargetResourceType,
				Label: "Current related " + activeAnchor.TargetResourceType,
			})
		}
		stages = append(stages, stage)
	}
	return stages
}

func RelatedExpandRemovalStages(t *testing.T, workspace ws.Workspace, outputID string) []receiptmodel.ReceiptConstructionStage {
	t.Helper()
	stages := ConstructionStageDescriptors(workspace, outputID)
	index := documentIndex(workspace, outputID)
	if index < 0 || workspace.Documents[index].Construction == nil {
		t.Fatal("related expansion test workspace has no construction")
	}
	rootAnchor := stages[0].RelatedExpandAnchors[0]
	var active *receiptmodel.ReceiptConstructionActiveRelatedRecord
	for stepIndex, step := range workspace.Documents[index].Construction.Steps {
		related := step.Operation.RelatedExpand
		if related == nil {
			if step.Operation.Kind != ws.ConstructionOperationFilter &&
				step.Operation.Kind != ws.ConstructionOperationDerive &&
				step.Operation.Kind != ws.ConstructionOperationRelatedSource &&
				step.Operation.Kind != ws.ConstructionOperationRelatedField {
				active = nil
			}
			if active != nil {
				stage := &stages[stepIndex+1]
				stage.ActiveRelatedRecord = active
				stage.RelatedExpandAnchors = []receiptmodel.ReceiptConstructionRelatedExpandAnchor{
					rootAnchor,
					{AnchorColumnID: active.TerminalIdentityColumn, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related " + active.TargetResourceType},
				}
			}
			continue
		}
		stage := &stages[stepIndex+1]
		anchorKind, anchorNodeID, anchorResourceType := "root", related.Route[0].FromNodeID, related.Route[0].FromResourceType
		if related.AnchorColumnID != "_key" {
			if active == nil || active.TerminalIdentityColumn != related.AnchorColumnID {
				t.Fatalf("step %q anchor %q does not match its prior active identity", step.ID, related.AnchorColumnID)
			}
			anchorKind, anchorNodeID, anchorResourceType = "activeRelatedRecord", active.TargetNodeID, active.TargetResourceType
		}
		terminalColumnID := "test_terminal_identity_" + step.ID
		stage.RelatedExpand.AnchorColumnID = related.AnchorColumnID
		stage.RelatedExpand.AnchorColumn = related.AnchorColumnID
		stage.RelatedExpand.AnchorKind = anchorKind
		stage.RelatedExpand.AnchorNodeID = anchorNodeID
		stage.RelatedExpand.AnchorResourceType = anchorResourceType
		stage.RelatedExpand.TerminalIdentityColumn = terminalColumnID
		active = &receiptmodel.ReceiptConstructionActiveRelatedRecord{
			TargetNodeID: related.TargetNodeID, TargetResourceType: related.TargetResourceType,
			TerminalIdentityColumn: terminalColumnID,
		}
		stage.ActiveRelatedRecord = active
		stage.RelatedExpandAnchors = []receiptmodel.ReceiptConstructionRelatedExpandAnchor{
			rootAnchor,
			{AnchorColumnID: terminalColumnID, Kind: "activeRelatedRecord", NodeID: active.TargetNodeID, ResourceType: active.TargetResourceType, Label: "Current related " + active.TargetResourceType},
		}
	}
	return stages
}

func NativeReceipt(snapshot capability.Snapshot) *receiptmodel.CompilationReceipt {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "native-test",
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}},
	}
	emitted := []viewer.EmittedColumn{{OutputID: "patients", EmissionID: "em_patient", PublicColumn: "patient_id", Label: "Patient ID", LogicalType: "string", Filterable: true, Chartable: true}}
	contract := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[{"column":"patient_id","label":"Patient ID","logicalType":"string","filterable":true,"chartable":true}]}]}`)
	bundleDigest, _ := bundle.Digest()
	workspace := ws.NewWorkspace("Patients")
	workspace.Documents = []ws.Document{{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Rows: ws.RecordsRowDefinition(), Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Columns: []ws.Column{{Column: "patient_id", Label: "Patient ID", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}}}}}
	workspace.Tabs = []ws.Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}}
	normalized, err := workspace.CanonicalJSON()
	if err != nil {
		panic(err)
	}
	intentDigest, err := workspace.Digest()
	if err != nil {
		panic(err)
	}
	receipt := &receiptmodel.CompilationReceipt{
		ReceiptFormatVersion:     receiptmodel.CurrentReceiptFormatVersion,
		CompilerContractVersion:  receiptmodel.CurrentCompilerContractVersion,
		Project:                  snapshot.Identity.Project,
		ExplorerID:               "patients",
		IntentDigest:             intentDigest,
		NormalizedBundle:         normalized,
		SnapshotToken:            snapshot.Token,
		AuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
		CapabilitySchemaDigest:   snapshot.Identity.SchemaDigest,
		ShapeDigest:              snapshot.Identity.ShapeDigest,
		SourceGeneration:         snapshot.Identity.Generation,
		RecipeDigest:             bundleDigest,
		ResolvedRecipeDigest:     bundleDigest,
		ResolvedSchemaDigest:     "resolved-schema",
		CompiledConfig:           json.RawMessage(`{}`),
		PublicOutputContract:     contract,
		Bundle:                   bundle,
		EmittedColumns:           emitted,
		OutputColumnProvenance:   map[string]map[string]string{"patients": {"patient_id": "EXPLICIT"}},
	}
	receipt.OutputContractDigest, _ = receiptmodel.CompilationArtifactDigest(contract)
	receipt.CompilationKey, _ = receiptmodel.CompilationKey(*receipt)
	receipt.ID, _ = receiptmodel.ReceiptID(*receipt)
	return receipt
}

func LifecycleConstructionReceipt(snapshot capability.Snapshot, workspace ws.Workspace) *receiptmodel.CompilationReceipt {
	receipt := NativeReceipt(snapshot)
	receipt.IntentDigest, _ = workspace.Digest()
	receipt.NormalizedBundle, _ = workspace.CanonicalJSON()
	columns := []struct {
		name, label, logical string
		nullable             bool
		construction         string
	}{
		{name: "patient_id", label: "Patient ID", logical: "string"},
		{name: "category", label: "Category", logical: "string", nullable: true},
		{name: "value", label: "Value", logical: "decimal", nullable: true},
		{name: "value_alt", label: "Alternate value", logical: "decimal"},
		{name: "category_number", label: "Numeric category", logical: "integer"},
		{name: "category_boolean", label: "Boolean category", logical: "boolean"},
		{name: "text_value", label: "Text value", logical: "string"},
		{name: "overflow", label: "Overflow category", logical: "string"},
	}
	public := receiptmodel.PublicOutputContract{OutputID: "patients", Columns: make([]receiptmodel.PublicOutputColumn, 0, len(columns))}
	receipt.EmittedColumns = make([]viewer.EmittedColumn, 0, len(columns))
	provenance := make(map[string]string, len(columns))
	for index, column := range columns {
		constructionID := column.construction
		public.Columns = append(public.Columns, receiptmodel.PublicOutputColumn{
			Column: column.name, ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		receipt.EmittedColumns = append(receipt.EmittedColumns, viewer.EmittedColumn{
			EmissionID: fmt.Sprintf("em_%02d", index), OutputID: "patients", PublicColumn: column.name,
			ConstructionID: constructionID, Label: column.label, LogicalType: column.logical,
			Cardinality: "optional_one", Nullable: column.nullable, Shape: "scalar",
		})
		provenance[column.name] = "EXPLICIT"
	}
	contract, _ := json.Marshal(receiptmodel.PublicOutputContracts{Outputs: []receiptmodel.PublicOutputContract{public}})
	receipt.PublicOutputContract = contract
	receipt.OutputContractDigest, _ = receiptmodel.CompilationArtifactDigest(contract)
	receipt.OutputFingerprints = map[string]string{"patients": "fingerprint-" + receipt.IntentDigest}
	receipt.OutputColumnProvenance = map[string]map[string]string{"patients": provenance}
	receipt.CompilationKey, _ = receiptmodel.CompilationKey(*receipt)
	receipt.ID, _ = receiptmodel.ReceiptID(*receipt)
	return receipt
}

func documentIndex(workspace ws.Workspace, outputID string) int {
	index := -1
	for candidate := range workspace.Documents {
		if workspace.Documents[candidate].Output.ID != outputID {
			continue
		}
		if index >= 0 {
			return -1
		}
		index = candidate
	}
	return index
}

func CodedCategoryFamily(sourceKind, resourceType, sourcePath, system, version string) semantic.Family {
	reference := semantic.FamilyReference{
		BindingID: "binding-" + resourceType, SourceKind: sourceKind, ResourceType: resourceType,
		SourcePath: sourcePath, System: system, Version: version,
	}
	reference.FamilyID = semantic.FamilyID(reference)
	return semantic.Family{
		FamilyReference: reference,
		Source:          semantic.Source{Type: resourceType, Path: sourcePath},
		KeySelector:     "code.coding[]",
		Value: semantic.Value{
			Selector: "code.coding[].display", Type: "string",
			FallbackSelectors: []string{"code.coding[].code"}, FallbackOnBlankString: true,
		},
		LogicalType: "string", SchemaVersion: semantic.ObservationSchemaVersion,
		RuleVersion:  strconv.Itoa(semantic.SemanticObservationRuleVersion),
		Completeness: semantic.CompletenessComplete, Status: "SUPPORTED", Population: 2,
		Examples: []string{"CARBOXYAMIDOTRIAZOLE", "another compound"},
	}
}

func SemanticFrameCompatibilityObservation(kind string) semantic.Observation {
	observation := semantic.Observation{
		SchemaVersion: semantic.ObservationSchemaVersion,
		Source:        semantic.Source{Type: "Observation", Canonical: "Observation.code", Path: "code"},
		Key:           semantic.Key{Selector: "code.coding[]", System: "urn:study", Code: "stage"},
		ChoiceArm:     "valueQuantity", LogicalType: "decimal", Completeness: semantic.CompletenessComplete,
		Status: "SUPPORTED", RuleHint: semantic.RuleHintCodedValueV1, RuleVersion: strconv.Itoa(semantic.SemanticObservationRuleVersion),
		Value: semantic.Value{Selector: "valueQuantity.value", Type: "decimal"},
	}
	if kind == "codeable concept" {
		observation.Value = semantic.Value{
			Selector: "valueCodeableConcept.text", Type: "string",
			FallbackSelectors: []string{"valueCodeableConcept.coding[].display"}, FallbackOnBlankString: true,
		}
		observation.ChoiceArm = "valueCodeableConcept"
		observation.LogicalType = "string"
	}
	return observation
}

func SemanticFrameCompatibilityFamily(t *testing.T, entry semantic.Entry) capability.SemanticFrameFamily {
	t.Helper()
	plan := semanticauthoring.ResolveSemanticSelectionPlan(entry.Observation)
	if !plan.Readiness.Addable() || plan.Source == nil || plan.Source.Lookup == nil || plan.Source.Lookup.Binding == nil {
		t.Fatalf("semantic compatibility fixture is not compiler-addable: %#v", plan)
	}
	binding := plan.Source.Lookup.Binding
	return capability.SemanticFrameFamily{
		BindingID: entry.BindingID, ResourceType: entry.Observation.Source.Type,
		SourcePath: entry.Observation.Source.Path, SourceCanonical: entry.Observation.Source.Canonical, SourceProfile: entry.Observation.Source.Profile,
		OwningScope: binding.OwnerPath, KeyPath: binding.KeyPath, ValuePath: binding.ValuePath,
		ValueFallbacks: append([]string(nil), binding.ValueFallback...), FallbackOnBlankString: binding.FallbackOnBlankString,
		ChoiceArms: append([]string(nil), binding.ChoiceArms...), LogicalType: binding.LogicalType,
		RuleVersion: entry.Observation.RuleVersion, SchemaVersion: entry.Observation.SchemaVersion,
	}
}

func CloneConstruction(t *testing.T, construction ws.Construction) ws.Construction {
	t.Helper()
	raw, err := json.Marshal(construction)
	if err != nil {
		t.Fatal(err)
	}
	var clone ws.Construction
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func CloneDocument(t *testing.T, document ws.Document) ws.Document {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var clone ws.Document
	if err := json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func CloneConstructionSlices(construction ws.Construction) ws.Construction {
	out := construction
	out.Steps = append([]ws.ConstructionStep(nil), construction.Steps...)
	for index := range out.Steps {
		step := out.Steps[index]
		if step.Inputs != nil {
			step.Inputs = append([]ws.ConstructionInputRef(nil), step.Inputs...)
		}
		if step.Outputs != nil {
			step.Outputs = append([]ws.StageColumn(nil), step.Outputs...)
		}
		out.Steps[index] = step
	}
	if construction.SourceProjections != nil {
		out.SourceProjections = append([]ws.ConstructionSourceProjection(nil), construction.SourceProjections...)
	} else {
		out.SourceProjections = nil
	}
	return out
}

func ConstructionWithoutStep(construction ws.Construction, stepID string) ws.Construction {
	candidate := construction
	candidate.Steps = make([]ws.ConstructionStep, 0, len(construction.Steps)-1)
	for _, step := range construction.Steps {
		if step.ID != stepID {
			candidate.Steps = append(candidate.Steps, step)
		}
	}
	return candidate
}

func FilterSourceSnapshot(token, project, rootNode, rootType, identityColumn, fieldPath string) capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: project, Generation: token, AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema-a", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: rootNode, ResourceType: rootType, RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 4}},
		nil,
		[]capability.Candidate{
			{ID: identityColumn, NodeID: rootNode, ResourceType: rootType, FieldPath: "id", Label: "Logical id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
			{ID: "bs-active", NodeID: rootNode, ResourceType: rootType, FieldPath: fieldPath, Label: "Whether this record is in active use", LogicalType: "boolean", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
			{ID: "bs-description", NodeID: rootNode, ResourceType: rootType, FieldPath: "description", Label: "Description", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: false, Populated: false},
			{ID: "bs-patient", NodeID: rootNode, ResourceType: rootType, FieldPath: "patient", Label: "Patient", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
		},
		nil,
	)
}

func FilterSourceDocument(outputID, outputTitle, rootType, identityColumn, identityName, fieldPath string) ws.Document {
	return ws.Document{
		Kind:             ws.Kind,
		Output:           ws.Output{ID: outputID, Title: outputTitle},
		RootResourceType: rootType,
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: rootType},
		Rows:             ws.RecordsRowDefinition(),
		Columns: []ws.Column{{
			ColumnID: identityColumn, Column: identityName, Label: "Body structure ID",
			LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func FilterSourceGroupStep(identityColumn string) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     "group-first",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group-first", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys:       []ws.ConstructionGroupKey{{InputColumnID: identityColumn, OutputColumnID: "group_key"}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "rows"}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
			{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
		},
	}
}

func FilterSourceFilterStep(columnID string, outputs []ws.StageColumn) ws.ConstructionStep {
	value := true
	return ws.ConstructionStep{
		ID:     "filter-active",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: columnID, Operator: ws.ConstructionFilterEquals,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: &value}},
		}},
		Outputs: outputs,
	}
}

func FilterSourceRowChoice(t *testing.T, snapshot capability.Snapshot, document ws.Document, path string) capability.RowChoice {
	t.Helper()
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	planner, err := rowdef.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	choices, err := planner.ListRowChoices(context.Background(), snapshot, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		if choice.Kind == capability.RowChoiceFieldGroupKey && choice.OccurrenceID == ws.RootOccurrenceID && choice.Path == path {
			return choice
		}
	}
	t.Fatalf("planner omitted the %s root field choice: %#v", path, choices)
	return capability.RowChoice{}
}

func FilterStep(stepID, columnID string, outputs []ws.StageColumn) ws.ConstructionStep {
	value := true
	return ws.ConstructionStep{
		ID:     stepID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: columnID, Operator: ws.ConstructionFilterEquals,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: &value}},
		}},
		Outputs: outputs,
	}
}

func PivotSourceRootProjection(columnID, ownerStepID, fieldPath string) ws.ConstructionSourceProjection {
	return ws.ConstructionSourceProjection{
		ColumnID: columnID, OwnerStepID: ownerStepID, OccurrenceID: ws.RootOccurrenceID,
		FieldPath: fieldPath, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
	}
}

func DeriveLaneStep(stepID, identityColumn string, literal int64) ws.ConstructionStep {
	value := literal
	three := int64(3)
	return ws.ConstructionStep{
		ID:     stepID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
			ConstructionID:     stepID,
			OutputColumnID:     "derived_total",
			Operation:          ws.ConstructionDerivedAdd,
			Left:               ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &value}},
			Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &three}},
			MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
		}},
		Outputs: []ws.StageColumn{
			{ID: identityColumn, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"},
		},
	}
}

func PivotSourceHelperStep(pivotID, helperID, helperColumn, rootNode, rootType, fieldPath, identityColumn string) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID: helperID, OwnerStepID: pivotID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedField, RelatedField: &ws.ConstructionRelatedField{
			ChoiceID: "seed-choice", OutputColumnID: helperColumn,
			Source: ws.ConstructionRelatedFieldSource{
				Kind: capability.ConstructionChoiceSourceField, CandidateID: "seed-cand", NodeID: rootNode,
				ResourceType: rootType, Path: fieldPath, Cardinality: "optional_one", LogicalType: "boolean",
			},
		}},
		Outputs: []ws.StageColumn{
			{ID: identityColumn, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: helperColumn, Name: "pv_col", Label: "BodyStructure.active", Type: "boolean", Nullable: true},
		},
	}
}

func PivotSourceStep(stepID string, groupKeys []string, categoryColumn, valueColumn string) ws.ConstructionStep {
	value := "t"
	return ws.ConstructionStep{
		ID:     stepID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
			ConstructionID: stepID, GroupKeyIDs: groupKeys,
			CategoryColumnID: categoryColumn, ValueColumnID: valueColumn,
			Categories:             []ws.ConstructionPivotCategory{{Key: ws.TableScalar{Kind: ws.TableScalarString, String: &value}, OutputColumnID: "pval"}},
			DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
			MissingCellPolicy:      ws.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
		}},
		Outputs: []ws.StageColumn{{ID: "pval", Name: "pval", Label: "pval", Type: "string"}},
	}
}

func PivotSourceSeedPivotStep(pivotID, helperID, helperColumn, identityColumn string) ws.ConstructionStep {
	value := "t"
	return ws.ConstructionStep{
		ID:     pivotID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: helperID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot, Pivot: &ws.ConstructionPivot{
			ConstructionID: pivotID, GroupKeyIDs: []string{identityColumn},
			CategoryColumnID: helperColumn, ValueColumnID: "pval",
			Categories:             []ws.ConstructionPivotCategory{{Key: ws.TableScalar{Kind: ws.TableScalarString, String: &value}, OutputColumnID: "pval"}},
			DuplicatePolicy:        ws.ConstructionPivotDuplicateError,
			MissingCellPolicy:      ws.ConstructionPivotMissingNull,
			UnlistedCategoryPolicy: ws.ConstructionPivotUnlistedError,
		}},
		Outputs: []ws.StageColumn{
			{ID: identityColumn, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			{ID: "pval", Name: "pval", Label: "pval", Type: "integer"},
		},
	}
}

func PivotSourceSeedDurable(filterID, filterColumn, helperID, helperColumn, pivotID, identityColumn, fieldPath string, identityOutputs []ws.StageColumn) ws.Construction {
	return ws.Construction{
		Version: ws.ConstructionVersion,
		Steps: []ws.ConstructionStep{
			FilterStep(filterID, filterColumn, identityOutputs),
			PivotSourceHelperStep(pivotID, helperID, helperColumn, "body-structure", "BodyStructure", fieldPath, identityColumn),
			PivotSourceSeedPivotStep(pivotID, helperID, helperColumn, identityColumn),
		},
		SourceProjections: []ws.ConstructionSourceProjection{PivotSourceRootProjection(helperColumn, pivotID, fieldPath)},
	}
}

func PivotFilterStep(stepID, inputStepID, columnID, groupColumnID, groupColumnName, groupLabel, valueColumnID, valueColumnName, valueLabel string) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID: stepID, Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: inputStepID}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: columnID, Operator: ws.ConstructionFilterExists,
		}},
		Outputs: []ws.StageColumn{
			{ID: groupColumnID, Name: groupColumnName, Label: groupLabel, Type: "string"},
			{ID: valueColumnID, Name: valueColumnName, Label: valueLabel, Type: "string"},
		},
	}
}
