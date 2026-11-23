package construction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/rowdef"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
)

type constructionBase = Base

func readySnapshot(project, generation, token string, scope authscope.ReadScope) capability.Snapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	sum := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	return capability.Snapshot{Identity: capability.SnapshotIdentity{Project: project, Generation: generation, AuthorizationScopeDigest: hex.EncodeToString(sum[:]), SchemaDigest: "schema", ShapeDigest: "shape"}, Status: capability.StatusReady, Complete: true, Token: token}
}

func lifecycleErrorCode(err error) string {
	var lifecycleErr *ports.Error
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Code
	}
	return ""
}

func semanticFrameCompatibilityObservation(kind string) semantic.Observation {
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

func semanticFrameCompatibilityFamily(t *testing.T, entry semantic.Entry) capability.SemanticFrameFamily {
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

func semanticAuthoringInventory(snapshot capability.Snapshot, entries []semantic.Entry) semantic.ResolveResult {
	build := semantic.NewBuild(snapshot.Identity.Project, snapshot.Identity.Generation, "")
	build.State = semantic.Complete
	build.SourceAvailability = semantic.SourceAvailabilityVerified
	return semantic.ResolveResult{Build: build, State: semantic.Complete, Entries: entries}
}

func assertLifecycleError(t *testing.T, err error, class ports.ErrorClass, code string) {
	t.Helper()
	var lifecycleErr *ports.Error
	if !errors.As(err, &lifecycleErr) || lifecycleErr.Class != class || lifecycleErr.Code != code {
		t.Fatalf("error=%v, want %s %s", err, class, code)
	}
}

const (
	filterSourceProject  = "project-a"
	filterSourceExplorer = "body-structures"
	filterSourceOutput   = "body_structures"
	filterSourceRootType = "BodyStructure"
	filterSourceRootNode = "body-structure"
	filterSourceIdentity = "bs-id"
	filterSourceField    = "active"
	filterSourceColumn   = "filter_source_active"
)

func filterSourceSnapshot(token string) capability.Snapshot {
	return capability.NewSnapshot(
		capability.SnapshotIdentity{
			Project: filterSourceProject, Generation: token, AuthorizationScopeDigest: "scope",
			SchemaDigest: "schema-a", ResourceInventoryDigest: "resources", RelationshipDigest: "relationships",
			FieldDigest: "fields", ProtocolVersion: "protocol", CompilerVersion: "compiler",
			TraversalPolicyVersion: "traversal", ProjectionPolicyVersion: "projection",
		},
		capability.Policy{Route: capability.RoutePolicy{Version: "traversal", AllowsRepeatedEdges: true, AllowsSelfLoops: true}},
		capability.StatusReady, true, false,
		[]capability.Node{{ID: filterSourceRootNode, ResourceType: filterSourceRootType, RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 4}},
		nil,
		[]capability.Candidate{
			{ID: "bs-id", NodeID: filterSourceRootNode, ResourceType: filterSourceRootType, FieldPath: "id", Label: "Logical id", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
			{ID: "bs-active", NodeID: filterSourceRootNode, ResourceType: filterSourceRootType, FieldPath: filterSourceField, Label: "Whether this record is in active use", LogicalType: "boolean", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
			{ID: "bs-description", NodeID: filterSourceRootNode, ResourceType: filterSourceRootType, FieldPath: "description", Label: "Description", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: false, Populated: false},
			{ID: "bs-patient", NodeID: filterSourceRootNode, ResourceType: filterSourceRootType, FieldPath: "patient", Label: "Patient", LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar}, Observed: true, Populated: true, ObservedDocumentCount: 4},
		},
		nil,
	)
}

func filterSourceDocument() ws.Document {
	return ws.Document{
		Kind:             ws.Kind,
		Output:           ws.Output{ID: filterSourceOutput, Title: "Body structures"},
		RootResourceType: filterSourceRootType,
		Route:            ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: filterSourceRootType},
		Rows:             ws.RecordsRowDefinition(),
		Columns: []ws.Column{{
			ColumnID: filterSourceIdentity, Column: "body_structure_id", Label: "Body structure ID",
			LogicalType: "string", OccurrenceID: ws.RootOccurrenceID,
			Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func filterSourceResolver(t *testing.T) *rowdef.SchemaRowChoiceResolver {
	t.Helper()
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := rowdef.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func filterSourceService(t *testing.T) *Service {
	t.Helper()
	resolver := filterSourceResolver(t)
	return &Service{config: Config{RowChoicePlanner: resolver, RowChoiceResolver: resolver}}
}

func filterSourceBase(snapshot capability.Snapshot, construction ws.Construction) constructionBase {
	return constructionBase{
		Document:     filterSourceDocument(),
		Construction: construction,
		Snapshot:     snapshot,
		Authorized:   ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		Stages: []receiptmodel.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []receiptmodel.ReceiptConstructionStageColumn{
				{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			},
		}},
	}
}

func filterSourceGroupStep() ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     "group-first",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group-first", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys:       []ws.ConstructionGroupKey{{InputColumnID: filterSourceIdentity, OutputColumnID: "group_key"}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "rows"}},
		}},
		Outputs: []ws.StageColumn{
			{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
			{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
		},
	}
}

type staleRowSourceChoicePlanner struct {
	inner  RowChoicePlanner
	issued capability.Snapshot
}

func (p staleRowSourceChoicePlanner) ListRowChoices(ctx context.Context, snapshot capability.Snapshot, document ws.Document) ([]capability.RowChoice, error) {
	return p.inner.ListRowChoices(ctx, p.issued, document)
}

type emptyRowSourceChoicePlanner struct{}

func (emptyRowSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error) {
	return nil, nil
}

type failingRowSourcePlanner struct{ err error }

func (p failingRowSourcePlanner) ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error) {
	return nil, p.err
}

func rowSourceServiceWithPlanner(planner RowChoicePlanner) *Service {
	return &Service{config: Config{RowChoicePlanner: planner}}
}

func rowSourceSnapshotWithGeneration(snapshot capability.Snapshot, generation string) capability.Snapshot {
	snapshot.Identity.Generation = generation
	snapshot.Token = generation
	return snapshot
}

func rowSourceLaneCandidate(node, resourceType, id, path, logicalType, cardinality string, populated bool) capability.Candidate {
	return capability.Candidate{
		ID: id, NodeID: node, ResourceType: resourceType, FieldPath: path, Label: path,
		LogicalType: logicalType, Cardinality: cardinality,
		ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
		Observed:        populated, Populated: populated,
	}
}

func lifecycleTestStringScalar(value string) ws.TableScalar {
	return ws.TableScalar{Kind: ws.TableScalarString, String: &value}
}

func semanticInventoryConstructionChoice(snapshot capability.Snapshot, contextToken, buildID string, entry semantic.Entry, candidate capability.Candidate, ownerRecordsProved bool) (capability.ConstructionChoice, error) {
	return semanticInventoryConstructionChoiceForRoute(snapshot, contextToken, buildID, nil, entry, candidate, ownerRecordsProved)
}

func schemaFieldsTestSnapshot(scope authscope.ReadScope, candidates []capability.Candidate) capability.Snapshot {
	paths := append([]string(nil), scope.AuthResourcePaths...)
	sort.Strings(paths)
	scopeHash := sha256.Sum256([]byte(string(scope.Mode) + "\x00" + strings.Join(paths, "\x00")))
	identity := capability.SnapshotIdentity{
		Project: "loom_dev_cda_fhir", Generation: "cda-fhir-v1",
		AuthorizationScopeDigest: hex.EncodeToString(scopeHash[:]), SchemaDigest: strings.Repeat("a", 64),
		ResourceInventoryDigest: "inventory", RelationshipDigest: "relationships", FieldDigest: "fields",
		ProtocolVersion: "protocol", CompilerVersion: "compiler", TraversalPolicyVersion: "route", ProjectionPolicyVersion: "projection",
	}
	return capability.NewSnapshot(identity, capability.Policy{}, capability.StatusReady, true, false, []capability.Node{
		{ID: "patient-node", ResourceType: "Patient", Populated: true},
		{ID: "observation-node", ResourceType: "Observation", Populated: true},
	}, nil, candidates, nil)
}

func filterSourceIdentityColumns() []ws.StageColumn {
	return []ws.StageColumn{{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"}}
}

func filterSourceProjectionColumn() ws.StageColumn {
	return ws.StageColumn{
		ID: filterSourceColumn, Name: ws.ConstructionSourceProjectionName(filterSourceColumn),
		Label: "BodyStructure.active", Type: "boolean",
	}
}

func filterSourceFilterStep(columnID string, outputs []ws.StageColumn) ws.ConstructionStep {
	return ws.ConstructionStep{
		ID:     "filter-active",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: columnID, Operator: ws.ConstructionFilterEquals,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: filterSourceBool(true)}},
		}},
		Outputs: outputs,
	}
}

func filterSourceBool(value bool) *bool { return &value }

func filterSourceRowChoice(t *testing.T, snapshot capability.Snapshot, path string) capability.RowChoice {
	t.Helper()
	choices, err := filterSourceResolver(t).ListRowChoices(context.Background(), snapshot, filterSourceDocument())
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

func filterSourceSelection(t *testing.T, snapshot capability.Snapshot, path, columnID string) ConstructionFilterSourceSelection {
	t.Helper()
	return ConstructionFilterSourceSelection{ChoiceID: filterSourceRowChoice(t, snapshot, path).ChoiceID, ColumnID: columnID}
}

func filterSourceProposalRequest(changedStepID string, sources []ConstructionFilterSourceSelection) ConstructionProposalRequest {
	return ConstructionProposalRequest{
		Project: filterSourceProject, ExplorerID: filterSourceExplorer, SnapshotToken: "filter-source-token",
		ExpectedDraftVersion: 1, ExpectedDraftDigest: "sha256:filter-source-draft", OutputID: filterSourceOutput,
		ChangedStepID: changedStepID, FilterSources: sources,
	}
}

type constructionReceiptColumn struct {
	name, label, logical string
	nullable             bool
	construction         string
}

func nativeConstructionReceipt(snapshot capability.Snapshot) *receiptmodel.CompilationReceipt {
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

func lifecycleConstructionReceipt(snapshot capability.Snapshot, workspace ws.Workspace) *receiptmodel.CompilationReceipt {
	receipt := nativeConstructionReceipt(snapshot)
	receipt.IntentDigest, _ = workspace.Digest()
	receipt.NormalizedBundle, _ = workspace.CanonicalJSON()
	columns := []constructionReceiptColumn{
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
