package construction

import (
	"context"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/rowdef"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestConstructionSourceProjectionAvailabilityUsesAuthoringCarryPolicy(t *testing.T) {
	construction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{ID: "filter_before", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter}},
		{ID: "expand_first", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand}},
		{ID: "filter_between", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter}},
		{ID: "expand_second", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationRelatedExpand}},
		{ID: "group_rows", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup}},
		{ID: "pivot_rows", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationPivot}},
		{ID: "append_rows", Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationCombine}},
	}}
	for _, stageID := range []string{recipe.ConstructionSourceProjectionID, "filter_before", "expand_first", "filter_between", "expand_second"} {
		availability := constructionSourceProjectionAvailability(construction, stageID)
		if !availability.Supported || availability.StageID != stageID || availability.ReasonCode != "" {
			t.Fatalf("root source projection availability at %q = %#v, want supported and stage-bound", stageID, availability)
		}
	}
	for _, stageID := range []string{"group_rows", "pivot_rows", "append_rows", "missing_stage"} {
		availability := constructionSourceProjectionAvailability(construction, stageID)
		if availability.Supported || availability.StageID != stageID || availability.ReasonCode != "ROOT_SOURCE_PROJECTION_UNAVAILABLE" || availability.Reason == "" {
			t.Fatalf("root source projection availability at %q = %#v, want explicit unsupported reason", stageID, availability)
		}
	}
}

func TestConstructionCodedGroupChoicesRequirePopulatedCodeAndShowPathBreadcrumb(t *testing.T) {
	const populatedPath = "includedStructure[].structure.coding[]"
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "body-structure", ResourceType: "BodyStructure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 1}},
		nil,
		[]capability.Candidate{
			{ID: "populated-code", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: populatedPath + ".code", Observed: true, Populated: true, ObservedDocumentCount: 1},
			{ID: "schema-only-code", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "_active.extension[].valueCodeableConcept.coding[].code", Observed: false, Populated: false},
		},
		nil,
	)
	base := constructionBase{
		Snapshot: snapshot,
		Document: ws.Document{
			RootResourceType: "BodyStructure",
			Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "BodyStructure"}, Rows: ws.RecordsRowDefinition(),
		},
	}
	stage := receiptmodel.ReceiptConstructionStage{
		ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
		Capabilities: []receiptmodel.ReceiptConstructionOperationChoice{{Kind: string(recipe.ConstructionCodedGroupOp), Supported: true}},
	}
	choices, err := constructionCodedGroupChoices(base, "body-structures", stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(choices) != 1 || choices[0].CodingPath != populatedPath {
		t.Fatalf("coded group choices = %#v, want only populated path %q", choices, populatedPath)
	}
	if want := "BodyStructure · includedStructure[] › structure › coding[]"; choices[0].Label != want {
		t.Fatalf("coded group choice label = %q, want breadcrumb %q", choices[0].Label, want)
	}
}

func TestConstructionGroupSourceExposesPopulatedGeneratedScalarWhenObserved(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "body-structure", ResourceType: "BodyStructure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 2}},
		nil,
		[]capability.Candidate{
			{ID: "active", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "active", Label: "Whether this record is in active use", LogicalType: "boolean", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 2},
			{ID: "id", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "id", Label: "Logical id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 2},
			{ID: "patient", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "patient", Label: "Patient", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 2},
		},
		nil,
	)
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := rowdef.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{config: Config{RowChoicePlanner: resolver}}
	base := constructionBase{
		Snapshot: snapshot,
		Document: ws.Document{
			RootResourceType: "BodyStructure",
			Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "BodyStructure"}, Rows: ws.RecordsRowDefinition(),
		},
	}
	capability, err := service.constructionSources().constructionGroupSourceCapability(context.Background(), base, "body-structures", recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if !capability.Supported || capability.ReasonCode != "" || len(capability.Choices) != 1 {
		t.Fatalf("BodyStructure source choices = %#v, want one eligible root scalar", capability)
	}
	choice := capability.Choices[0]
	if choice.FieldPath != "active" || choice.Label != "Active" || choice.FHIRType != "boolean" ||
		choice.LogicalType != "boolean" || choice.ValueType != "BOOLEAN" || choice.IsIdentifier || choice.IsReference || !choice.IsPopulated {
		t.Fatalf("BodyStructure active choice = %#v", choice)
	}
	transformed, err := service.constructionSources().constructionGroupSourceCapability(context.Background(), base, "body-structures", "group-step")
	if err != nil {
		t.Fatal(err)
	}
	if transformed.Supported || transformed.ReasonCode != "TRANSFORMED_STAGE_SOURCE_INPUT_UNSUPPORTED" || len(transformed.Choices) != 0 {
		t.Fatalf("transformed-stage source choices = %#v", transformed)
	}
}

func TestConstructionGroupSourceChoicesExcludeConstantResourceTypeMetadata(t *testing.T) {
	snapshot := capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: "project-a", Generation: "generation-a", AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: "body-structure", ResourceType: "BodyStructure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 135}},
		nil,
		[]capability.Candidate{{
			ID: "resource-type", NodeID: "body-structure", ResourceType: "BodyStructure", FieldPath: "resourceType",
			Label: "Resource Type", LogicalType: "string", Cardinality: "required_one",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 135,
		}},
		nil,
	)
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := rowdef.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	document := ws.Document{
		RootResourceType: "BodyStructure",
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "BodyStructure"}, Rows: ws.RecordsRowDefinition(),
	}
	rowChoices, err := resolver.ListRowChoices(context.Background(), snapshot, document)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowChoices) != 1 || rowChoices[0].Path != "resourceType" {
		t.Fatalf("fixture row choices = %#v, want only the observed resourceType choice", rowChoices)
	}
	service := &Service{config: Config{RowChoicePlanner: resolver, RowChoiceResolver: resolver}}
	base := constructionBase{Snapshot: snapshot, Document: document}
	sourceInput, err := service.constructionSources().constructionGroupSourceCapability(context.Background(), base, "body-structures", recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if sourceInput.Supported || sourceInput.ReasonCode != "NO_ELIGIBLE_POPULATED_SCALAR_GROUP_FIELDS" || len(sourceInput.Choices) != 0 {
		t.Fatalf("constant resourceType was offered as a useful source group: %#v", sourceInput)
	}
	_, err = service.constructionSources().resolveConstructionGroupSource(context.Background(), base, ConstructionProposalRequest{
		Project: "project-a", ExplorerID: "explorer-a", OutputID: "body-structures",
		GroupSource: &ConstructionGroupSourceSelection{RowChoiceID: rowChoices[0].ChoiceID, ColumnID: "source_resource_type"},
	}, "source_resource_type")
	if lifecycleErrorCode(err) != "RESOURCE_METADATA_GROUP_SOURCE_UNSUPPORTED" {
		t.Fatalf("resourceType group request error = %v, want metadata-field rejection", err)
	}
}

func TestConstructionGroupEditCannotIntroduceUnsignedSourceProjection(t *testing.T) {
	accepted := ws.ConstructionSourceProjection{ColumnID: "accepted", FieldPath: "status", OwnerStepID: "group"}
	forged := ws.ConstructionSourceProjection{ColumnID: "forged", FieldPath: "secret", OwnerStepID: "group"}
	base := constructionBase{Construction: ws.Construction{SourceProjections: []ws.ConstructionSourceProjection{accepted}}}
	service := &Service{}
	candidate, err := service.constructionSources().constructionCandidateWithGroupSource(context.Background(), base, ConstructionProposalRequest{CandidateConstruction: ws.Construction{SourceProjections: []ws.ConstructionSourceProjection{forged}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.SourceProjections) != 1 || candidate.SourceProjections[0].ColumnID != "accepted" {
		t.Fatalf("unsigned source metadata accepted: %#v", candidate.SourceProjections)
	}
}

func TestConstructionWorkspaceInputsUseFinalSchemasAndExcludeTargetDependents(t *testing.T) {
	workspaceOutput := func(id string, steps ...ws.ConstructionStep) ws.Document {
		return ws.Document{Output: ws.Output{ID: id, Title: "Table " + id}, Construction: &ws.Construction{Version: 1, Steps: steps}, Rows: ws.RecordsRowDefinition()}
	}
	combineInput := func(outputID string) ws.ConstructionStep {
		return ws.ConstructionStep{Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputWorkspaceOutput, OutputID: outputID}}}
	}
	base := constructionBase{
		Workspace: ws.Workspace{Documents: []ws.Document{
			{Output: ws.Output{ID: "upstream", Title: "Upstream"}, Rows: ws.RecordsRowDefinition()},
			workspaceOutput("target"),
			workspaceOutput("depends-on-target", combineInput("target")),
			workspaceOutput("transitive-dependent", combineInput("depends-on-target")),
			{Output: ws.Output{ID: "independent", Title: "Independent"}, Rows: ws.RecordsRowDefinition()},
		}, SemanticsVersion: ws.CurrentSemanticsVersion,
		},
		Receipt: &receiptmodel.CompilationReceipt{CompiledOutputSchemas: map[string][]receiptmodel.ReceiptCompiledOutputColumn{
			"upstream": {
				{ID: "patient-id", Name: "patient_id", Label: "Patient ID", LogicalType: "string", Cardinality: "required_one"},
				{ID: "birth-date", Name: "birth_date", Label: "Birth date", LogicalType: "date", Cardinality: "optional_one", Nullable: true},
				{ID: "tags", Name: "tags", Label: "Tags", LogicalType: "string", Cardinality: "many"},
				{ID: "__row_id", Name: "__row_id", Label: "Internal row ID", LogicalType: "string", Cardinality: "required_one", Internal: true, Identity: true},
			},
			"target":               {{ID: "target-col", Name: "x", Label: "X", LogicalType: "string", Cardinality: "required_one"}},
			"depends-on-target":    {{ID: "dependent-col", Name: "x", Label: "X", LogicalType: "string", Cardinality: "required_one"}},
			"transitive-dependent": {{ID: "transitive-col", Name: "x", Label: "X", LogicalType: "string", Cardinality: "required_one"}},
			"independent":          {{ID: "independent-col", Name: "id", Label: "Identifier", LogicalType: "uuid", Cardinality: "required_one", Identity: true}},
		}},
	}

	inputs, err := constructionWorkspaceInputs(base, "target")
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 2 || inputs[0].OutputID != "upstream" || inputs[1].OutputID != "independent" {
		t.Fatalf("eligible current outputs = %#v, want upstream and independent only", inputs)
	}
	columns := inputs[0].Columns
	if len(columns) != 3 || columns[0].ID != "patient-id" || columns[0].JoinCompatibilityKey != "String" || columns[0].AppendCompatibilityKey != "string:String" {
		t.Fatalf("compiler source field identity/compatibility = %#v", columns)
	}
	if columns[1].ID != "birth-date" || columns[1].Nullable != true || columns[1].JoinCompatibilityKey != "Date" || columns[1].AppendCompatibilityKey != "date:Date" {
		t.Fatalf("nullable scalar compatibility = %#v", columns[1])
	}
	if columns[2].ID != "tags" || columns[2].Cardinality != "many" || columns[2].JoinCompatibilityKey != "" || columns[2].AppendCompatibilityKey != "" {
		t.Fatalf("repeated scalar compatibility = %#v", columns[2])
	}
	if inputs[1].Columns[0].ID != "independent-col" {
		t.Fatalf("visible identity column was omitted from compiler schema: %#v", inputs[1].Columns)
	}
}

func TestConstructionWorkspaceInputsRejectMissingFinalSiblingSchema(t *testing.T) {
	base := constructionBase{
		Workspace: ws.Workspace{Documents: []ws.Document{
			{Output: ws.Output{ID: "target"}, Rows: ws.RecordsRowDefinition()},
			{Output: ws.Output{ID: "sibling"}, Rows: ws.RecordsRowDefinition()},
		}, SemanticsVersion: ws.CurrentSemanticsVersion,
		},
		Receipt: &receiptmodel.CompilationReceipt{CompiledOutputSchemas: map[string][]receiptmodel.ReceiptCompiledOutputColumn{
			"target": {{ID: "target-id", Name: "target", Label: "Target", LogicalType: "string", Cardinality: "required_one"}},
		}},
	}
	_, err := constructionWorkspaceInputs(base, "target")
	if lifecycleErrorCode(err) != "WORKSPACE_OUTPUT_SCHEMA_UNAVAILABLE" {
		t.Fatalf("missing compiler-final sibling schema error = %v", err)
	}
}

func TestConstructionWorkspaceCompatibilityKeysMatchCompilerCombineTypeRules(t *testing.T) {
	for _, test := range []struct {
		logical     string
		cardinality string
		nullable    bool
		joinKey     string
		appendKey   string
	}{
		{logical: "string", cardinality: "required_one", joinKey: "String", appendKey: "string:String"},
		{logical: "code", cardinality: "optional_one", nullable: true, joinKey: "String", appendKey: "code:String"},
		{logical: "uuid", cardinality: "required_one", appendKey: "uuid:UUID"},
		{logical: "date", cardinality: "required_one", joinKey: "Date", appendKey: "date:Date"},
		{logical: "date_time", cardinality: "required_one", joinKey: "DateTime64(3)", appendKey: "date-time:DateTime64(3)"},
		{logical: "boolean", cardinality: "required_one", joinKey: "Bool", appendKey: "boolean:Bool"},
		{logical: "integer", cardinality: "required_one", joinKey: "Int64", appendKey: "integer:Int64"},
		{logical: "decimal", cardinality: "required_one", appendKey: "decimal:Float64"},
		{logical: "string", cardinality: "many"},
		{logical: "quantity", cardinality: "required_one"},
	} {
		logical, physical, err := facade.ConstructionCombineColumnType(recipe.StageColumn{Type: test.logical})
		if err != nil {
			gotJoin, gotAppend := constructionCombineCompatibilityKeys(test.logical, test.cardinality, test.nullable)
			if gotJoin != "" || gotAppend != "" {
				t.Errorf("unsupported logical type %q received compatibility keys (%q, %q)", test.logical, gotJoin, gotAppend)
			}
			continue
		}
		physicalType := physical
		if test.nullable {
			physicalType = "Nullable(" + physicalType + ")"
		}
		wantJoin, joinOK := facade.ClickHouseCombineScalarBaseType(physicalType, facade.PhysicalCombineKeyJoin)
		wantAppend, appendOK := facade.ClickHouseCombineScalarBaseType(physicalType, facade.PhysicalCombineAppend)
		if test.cardinality == "many" {
			joinOK, appendOK = false, false
		}
		if !joinOK {
			wantJoin = ""
		}
		if !appendOK {
			wantAppend = ""
		} else {
			wantAppend = logical + ":" + wantAppend
		}
		gotJoin, gotAppend := constructionCombineCompatibilityKeys(test.logical, test.cardinality, test.nullable)
		if gotJoin != wantJoin || gotAppend != wantAppend || gotJoin != test.joinKey || gotAppend != test.appendKey {
			t.Errorf("compatibility keys for %s/%s nullable=%t = (%q, %q), want IR (%q, %q) and contract (%q, %q)", test.logical, test.cardinality, test.nullable, gotJoin, gotAppend, wantJoin, wantAppend, test.joinKey, test.appendKey)
		}
	}
}
