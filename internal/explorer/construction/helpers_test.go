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

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/semanticauthoring"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
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

func validateFilterSourceColumns(steps []ws.ConstructionStep, filterID string, sources []ConstructionFilterSourceSelection) error {
	index := constructionStepIndex(steps, filterID)
	if index < 0 || steps[index].Operation.Filter == nil {
		return fmt.Errorf("the selected Filter step is unavailable")
	}
	for _, source := range sources {
		if steps[index].Operation.Filter.ColumnID != source.ColumnID {
			return fmt.Errorf("filterSources columnId %q is not the column filtered by the Filter step", source.ColumnID)
		}
	}
	return nil
}

func clearConstructionFilterOwnedSources(base, candidate ws.Construction, filterID string) (ws.Construction, error) {
	filterIndex := constructionStepIndex(candidate.Steps, filterID)
	if filterIndex < 0 {
		return ws.Construction{}, fmt.Errorf("the edited Filter step is missing")
	}
	owned := make(map[string]bool)
	for _, projection := range base.SourceProjections {
		if projection.OwnerStepID == filterID {
			owned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == filterID {
			owned[projection.ColumnID] = true
		}
	}
	steps := make([]ws.ConstructionStep, 0, len(candidate.Steps))
	for index, step := range candidate.Steps {
		if index <= filterIndex {
			outputs := make([]ws.StageColumn, 0, len(step.Outputs))
			for _, output := range step.Outputs {
				if owned[output.ID] {
					continue
				}
				outputs = append(outputs, output)
			}
			step.Outputs = outputs
		}
		steps = append(steps, step)
	}
	projections := make([]ws.ConstructionSourceProjection, 0, len(candidate.SourceProjections))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != filterID {
			projections = append(projections, projection)
		}
	}
	candidate.Steps, candidate.SourceProjections = steps, projections
	return candidate, nil
}

func constructionOwnedFilterSourceColumnID(construction ws.Construction, stageID, path string) string {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" || canonicalRowPath(projection.FieldPath) != canonicalRowPath(path) {
			continue
		}
		index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
		if index < 0 {
			continue
		}
		step := construction.Steps[index]
		if step.Operation.Kind != ws.ConstructionOperationFilter || step.Operation.Filter == nil ||
			step.Operation.Filter.ColumnID != projection.ColumnID || constructionStepInputStageID(step) != stageID {
			continue
		}
		return projection.ColumnID
	}
	return ""
}

func ConstructionFilterSourceFacts(snapshot capability.Snapshot, rootResourceType, fieldPath string) (ws.ConstructionSourceProjection, bool) {
	path := canonicalRowPath(fieldPath)
	if strings.TrimSpace(rootResourceType) == "" || path == "" {
		return ws.ConstructionSourceProjection{}, false
	}
	for _, node := range snapshot.Nodes {
		if !node.RowRootEligible || node.ResourceType != rootResourceType {
			continue
		}
		candidate, ok := uniqueRowSourceCandidate(snapshot, node.ID, rootResourceType, path)
		if !ok || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
			!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
			!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
			continue
		}
		schemaIndex, err := fhirschema.GeneratedIndex()
		if err != nil {
			continue
		}
		facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootResourceType), path)
		if err != nil || facts.CanonicalPath != path || facts.Shape != fhirschema.RowPathScalar ||
			facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference {
			continue
		}
		return ws.ConstructionSourceProjection{
			OccurrenceID: ws.RootOccurrenceID, FieldPath: facts.CanonicalPath,
			FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType,
			Label: constructionFieldDisplayLabel(rootResourceType, facts.CanonicalPath, candidate.Label),
		}, true
	}
	return ws.ConstructionSourceProjection{}, false
}

func constructionHasFilterSourceInput(construction ws.Construction) bool {
	for _, projection := range construction.SourceProjections {
		if projection.OwnerStepID == "" {
			continue
		}
		if !isFilterOwnedSourceProjection(construction, projection) {
			continue
		}
		return true
	}
	return false
}

func isFilterOwnedSourceProjection(construction ws.Construction, projection ws.ConstructionSourceProjection) bool {
	index := constructionStepIndex(construction.Steps, projection.OwnerStepID)
	if index < 0 {
		return false
	}
	step := construction.Steps[index]
	return step.Operation.Kind == ws.ConstructionOperationFilter && step.Operation.Filter != nil &&
		step.Operation.Filter.ColumnID == projection.ColumnID
}

func (s *Service) testResolveConstructionFilterSource(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	acceptedStageID string,
	selection ConstructionFilterSourceSelection,
) (ws.ConstructionSourceProjection, error) {
	identity, err := capability.DecodeRowChoiceID(selection.ChoiceID)
	if err != nil {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "the selected filter source field choice is invalid", nil, err)
	}
	if s.config.RowChoiceResolver == nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "ROW_CHOICE_UNAVAILABLE", "authorized filter source choice resolution is not configured", nil)
	}
	if identity.SnapshotToken != base.Snapshot.Token || identity.SchemaDigest != base.Snapshot.Identity.SchemaDigest {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, nil)
	}
	resolved, resolveErr := s.config.RowChoiceResolver.ResolveRowChoiceID(ctx, RowChoiceResolveRequest{
		Project: request.Project, ExplorerID: request.ExplorerID, OutputID: request.OutputID,
		Snapshot: base.Snapshot.Clone(), Route: base.Document.Route,
		RowChoiceID: selection.ChoiceID, ExpectedKind: RowChoiceFieldGroup,
	})
	if resolveErr != nil || identity.Kind != capability.RowChoiceFieldGroupKey ||
		identity.Occurrence.OccurrenceID != ws.RootOccurrenceID ||
		identity.Path != resolved.FieldPath || identity.Reference ||
		identity.Cardinality != capability.RowChoiceOne || identity.Shape != capability.RowChoiceScalar ||
		resolved.OccurrenceID != ws.RootOccurrenceID {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "reload the current populated root scalar field choice", nil, resolveErr)
	}
	rootNode, ok := base.Snapshot.Node(identity.Occurrence.NodeID)
	if !ok || !rootNode.RowRootEligible || rootNode.ResourceType != base.Document.RootResourceType {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "INVALID_FILTER_SOURCE_CHOICE", "the selected field no longer identifies the authorized root resource", nil, nil)
	}
	candidate, found := uniqueRowSourceCandidate(base.Snapshot, rootNode.ID, rootNode.ResourceType, resolved.FieldPath)
	if !found || !candidate.Populated || len(candidate.RepeatedBoundaries) != 0 ||
		!containsSourceProjectionMode(candidate.ProjectionModes, capability.ProjectionScalar) ||
		!capability.IsSupportedConstructionSourceType(candidate.LogicalType) {
		return ws.ConstructionSourceProjection{}, unprocessable("construction-proposal", "UNSUPPORTED_FILTER_SOURCE_FIELD", "the selected root field is not a populated scalar source field", nil)
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return ws.ConstructionSourceProjection{}, unavailable("construction-proposal", "FHIR_SCHEMA_UNAVAILABLE", "FHIR field metadata is unavailable", err)
	}
	facts, err := schemaIndex.ResolveRowPath(fhirschema.DefinitionName(rootNode.ResourceType), resolved.FieldPath)
	if err != nil || facts.CanonicalPath != resolved.FieldPath || facts.Shape != fhirschema.RowPathScalar ||
		facts.Cardinality != fhirschema.RowCardinalityOne || facts.Reference || facts.FHIRType != identity.FHIRType {
		return ws.ConstructionSourceProjection{}, conflict("construction-proposal", "STALE_FILTER_SOURCE_SCHEMA", "the selected field no longer matches generated scalar FHIR metadata", nil, err)
	}
	label := constructionFieldDisplayLabel(base.Document.RootResourceType, facts.CanonicalPath, candidate.Label)
	return ws.ConstructionSourceProjection{
		ColumnID: selection.ColumnID, OwnerStepID: request.ChangedStepID, OccurrenceID: resolved.OccurrenceID,
		FieldPath: facts.CanonicalPath, FHIRType: facts.FHIRType, LogicalType: candidate.LogicalType, Label: label,
	}, nil
}

func (s *Service) testConstructionCandidateWithFilterSources(
	ctx context.Context,
	base Base,
	request ConstructionProposalRequest,
	candidate ws.Construction,
) (ws.Construction, error) {
	changedIndex := constructionStepIndex(candidate.Steps, request.ChangedStepID)
	applyFilterSources := request.FilterSources != nil && changedIndex >= 0 &&
		candidate.Steps[changedIndex].Operation.Kind == ws.ConstructionOperationFilter
	if !applyFilterSources {
		return candidate, nil
	}
	filter := candidate.Steps[changedIndex].Operation.Filter
	if filter == nil {
		return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_REQUIRES_FILTER", "filterSources can only be applied to a Filter step", nil)
	}
	if len(request.FilterSources) == 0 {
		cleared, err := clearConstructionFilterOwnedSources(base.Construction, candidate, request.ChangedStepID)
		if err != nil {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_STAGE_UNAVAILABLE", err.Error(), err)
		}
		return cleared, nil
	}
	acceptedStageID := constructionStepInputStageID(candidate.Steps[changedIndex])
	if acceptedStageID == "" {
		return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_STAGE_UNAVAILABLE", "the Filter input stage cannot be resolved", nil)
	}
	if _, found := constructionReceiptStage(base.Stages, acceptedStageID); !found {
		return ws.Construction{}, conflict("construction-proposal", "STALE_STAGE_REFERENCE", "the filter source choices are not bound to an accepted stage", nil, nil)
	}
	for _, selection := range request.FilterSources {
		if selection.ColumnID != filter.ColumnID {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_COLUMN_MISMATCH",
				fmt.Sprintf("filterSources columnId %q is not the column filtered by the edited Filter step", selection.ColumnID), nil)
		}
	}

	resolved := make([]ws.ConstructionSourceProjection, 0, len(request.FilterSources))
	for _, selection := range request.FilterSources {
		projection, err := s.testResolveConstructionFilterSource(ctx, base, request, acceptedStageID, selection)
		if err != nil {
			return ws.Construction{}, err
		}
		resolved = append(resolved, projection)
	}
	sort.Slice(resolved, func(i, j int) bool { return resolved[i].ColumnID < resolved[j].ColumnID })

	droppedOwned := make(map[string]bool)
	for _, projection := range base.Construction.SourceProjections {
		if projection.OwnerStepID == request.ChangedStepID {
			droppedOwned[projection.ColumnID] = true
		}
	}
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID == request.ChangedStepID {
			droppedOwned[projection.ColumnID] = true
		}
	}
	reowned := make(map[string]bool, len(resolved))
	for _, projection := range resolved {
		reowned[projection.ColumnID] = true
	}

	projections := make([]ws.ConstructionSourceProjection, 0, len(candidate.SourceProjections)+len(base.Construction.SourceProjections)+len(resolved))
	for _, projection := range candidate.SourceProjections {
		if projection.OwnerStepID != request.ChangedStepID {
			projections = append(projections, projection)
		}
	}
	for _, projection := range base.Construction.SourceProjections {
		if projection.OwnerStepID != "" && projection.OwnerStepID != request.ChangedStepID && !hasPivotSourceProjection(projections, projection.ColumnID) &&
			!constructionOwnerInputsClearedFromCandidate(base.Construction, candidate, projection.OwnerStepID) {
			projections = append(projections, projection)
		}
	}
	projections = append(projections, resolved...)

	for _, projection := range resolved {
		if constructionRootFieldAlreadyProjected(base.Document, candidate.Steps, projections, projection.ColumnID, projection.FieldPath) {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_FIELD_ALREADY_PROJECTED",
				fmt.Sprintf("root field %q is already projected as a column of this table; remove that column or choose a different field for this filter source", projection.FieldPath), nil)
		}
	}

	steps := make([]ws.ConstructionStep, len(candidate.Steps))
	copy(steps, candidate.Steps)
	for index := 0; index <= changedIndex; index++ {
		step := &steps[index]
		if !ws.ConstructionCanCarrySourceProjection(step.Operation.Kind) {
			return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_CANNOT_CROSS_STAGE", "a selected root field cannot be carried through an earlier row-changing operation", nil)
		}
		outputs := make([]ws.StageColumn, 0, len(step.Outputs)+len(resolved))
		for _, output := range step.Outputs {
			if droppedOwned[output.ID] && !reowned[output.ID] {
				continue
			}
			outputs = append(outputs, output)
		}
		for _, projection := range resolved {
			outputs = appendStageColumnIfAbsent(outputs, ws.StageColumn{
				ID: projection.ColumnID, Name: ws.ConstructionSourceProjectionName(projection.ColumnID),
				Label: projection.Label, Type: projection.LogicalType,
			})
		}
		step.Outputs = outputs
	}

	candidate.Steps, candidate.SourceProjections = steps, projections
	if err := validateFilterSourceColumns(candidate.Steps, request.ChangedStepID, request.FilterSources); err != nil {
		return ws.Construction{}, unprocessable("construction-proposal", "FILTER_SOURCE_COLUMN_MISMATCH", err.Error(), err)
	}
	return candidate, nil
}
