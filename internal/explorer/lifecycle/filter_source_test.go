package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

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

// filterSourceDocument keeps only the row identity selected, so every other
// eligible root scalar must reach a filter through a signed source choice.
func filterSourceDocument() authoringv2.Document {
	return authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: filterSourceOutput, Title: "Body structures"},
		RootResourceType: filterSourceRootType,
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: filterSourceRootType},
		Rows:             authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{{
			ColumnID: filterSourceIdentity, Column: "body_structure_id", Label: "Body structure ID",
			LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID,
			Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}},
		}},
	}
}

func filterSourceIdentityColumns() []authoringv2.StageColumn {
	return []authoringv2.StageColumn{{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"}}
}

func filterSourceProjectionColumn() authoringv2.StageColumn {
	return authoringv2.StageColumn{
		ID: filterSourceColumn, Name: authoringv2.ConstructionSourceProjectionName(filterSourceColumn),
		Label: "BodyStructure.active", Type: "boolean",
	}
}

func filterSourceResolver(t *testing.T) *SchemaRowChoiceResolver {
	t.Helper()
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
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

func filterSourceBase(snapshot capability.Snapshot, construction authoringv2.Construction) constructionBase {
	return constructionBase{
		document:     filterSourceDocument(),
		construction: construction,
		snapshot:     snapshot,
		authorized:   AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []explorer.ReceiptConstructionStageColumn{
				{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			},
		}},
	}
}

func filterSourceFilterStep(columnID string, outputs []authoringv2.StageColumn) authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     "filter-active",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: columnID, Operator: authoringv2.ConstructionFilterEquals,
			Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterBoolean, Boolean: filterSourceBool(true)}},
		}},
		Outputs: outputs,
	}
}

func filterSourceBool(value bool) *bool { return &value }

func filterSourceGroupStep() authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     "group-first",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: "group-first", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys:       []authoringv2.ConstructionGroupKey{{InputColumnID: filterSourceIdentity, OutputColumnID: "group_key"}},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "rows"}},
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
			{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
		},
	}
}

// filterSourceRowChoice returns the signed choice the planner issues for one
// exact root field so a test can hold a legitimate, current token.
func filterSourceRowChoice(t *testing.T, snapshot capability.Snapshot, path string) capability.RowChoice {
	t.Helper()
	choices, err := filterSourceResolver(t).ListRowChoices(context.Background(), snapshot, filterSourceDocument())
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		if choice.Kind == capability.RowChoiceFieldGroupKey && choice.OccurrenceID == authoringv2.RootOccurrenceID && choice.Path == path {
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

// (a) Discovery exposes the populated unselected root scalar with a signed
// choice id and never advertises a schema-only field.
func TestConstructionFilterSourceCapabilityExposesPopulatedUnselectedRootScalar(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-discovery")
	result, err := filterSourceService(t).constructionFilterSourceCapability(context.Background(),
		filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), filterSourceOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Supported || result.ReasonCode != "" || result.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("filter source capability = %#v, want a supported source-stage capability", result)
	}
	byPath := make(map[string]ConstructionFilterSourceChoice, len(result.Choices))
	for _, choice := range result.Choices {
		byPath[choice.FieldPath] = choice
	}
	active, found := byPath[filterSourceField]
	if !found {
		t.Fatalf("populated unselected root scalar was not offered: %#v", result.Choices)
	}
	if active.Label != "Whether this record is in active use" || active.LogicalType != "boolean" || !active.IsPopulated {
		t.Fatalf("offered choice facts = %#v, want generated label, boolean logical type, populated", active)
	}
	if active.ColumnID != "" {
		t.Fatalf("a field with no durable server-owned materialization advertised columnId %q", active.ColumnID)
	}
	identity, err := capability.DecodeRowChoiceID(active.ChoiceID)
	if err != nil {
		t.Fatalf("offered choice id is not a signed row choice: %v", err)
	}
	if identity.SnapshotToken != snapshot.Token || identity.SchemaDigest != snapshot.Identity.SchemaDigest ||
		identity.Kind != capability.RowChoiceFieldGroupKey || identity.Path != filterSourceField ||
		identity.Shape != capability.RowChoiceScalar || identity.Cardinality != capability.RowChoiceOne || identity.Reference {
		t.Fatalf("offered choice identity = %#v, want a token/schema-pinned non-reference root scalar", identity)
	}
	for _, excluded := range []string{"description", "resourceType", "patient"} {
		if offered, exists := byPath[excluded]; exists {
			t.Fatalf("field %q was offered as filter source data: %#v", excluded, offered)
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"supported":true`, `"stageId":"source_projection"`, `"choiceId":"rc1.`, `"fieldPath":"active"`, `"label":"Whether this record is in active use"`, `"logicalType":"boolean"`, `"isPopulated":true`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("filterSourceInput wire shape omitted %s: %s", want, encoded)
		}
	}
}

// ROW-SOURCE-03 S-C: the single display-label builder must surface the exact
// schema Title on discovery and write the identical string into the durable
// claim, so the advertised label and the server-derived projection for one
// field are the same bytes (the compiled artifact copies those bytes).
func TestConstructionFilterSourceTitleLabelIsSharedByDiscoveryAndDurableClaim(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-title-parity")
	service := filterSourceService(t)
	const titleLabel = "Whether this record is in active use"

	result, err := service.constructionFilterSourceCapability(context.Background(),
		filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), filterSourceOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	advertisedLabel := ""
	for _, choice := range result.Choices {
		if choice.FieldPath == filterSourceField {
			advertisedLabel = choice.Label
		}
	}
	if advertisedLabel != titleLabel {
		t.Fatalf("offered label = %q, want the exact schema Title %q", advertisedLabel, titleLabel)
	}

	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		filterSourceFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
	}}
	candidate := construction
	candidate.Steps[0].Operation.Filter.ColumnID = filterSourceColumn
	candidate.Steps[0].Outputs = append(filterSourceIdentityColumns(), filterSourceProjectionColumn())
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, construction),
		filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
	if err != nil {
		t.Fatalf("prepare filter sources: %v", err)
	}
	if len(prepared.SourceProjections) != 1 {
		t.Fatalf("prepared projections = %#v, want exactly one filter-owned projection", prepared.SourceProjections)
	}
	if prepared.SourceProjections[0].Label != advertisedLabel {
		t.Fatalf("durable label %q drifted from advertised label %q: one-builder atomicity broken", prepared.SourceProjections[0].Label, advertisedLabel)
	}
	if prepared.SourceProjections[0].Label != titleLabel {
		t.Fatalf("durable label = %q, want the exact schema Title %q", prepared.SourceProjections[0].Label, titleLabel)
	}
}

// Candidate-label fallback shape (rs03-label-owner.md pinned decision): the
// capability snapshot builder collapses an empty schema Title to the bare
// field path (builder.go:296 first(TrimSpace(f.Label), path)), so the lane
// label for such a field is the raw path — not the legacy 'Type.path'.
func TestConstructionFilterSourceEmptyTitleCandidateLabelIsRawPath(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-empty-title")
	for index := range snapshot.Candidates {
		if snapshot.Candidates[index].ID == "bs-active" {
			snapshot.Candidates[index].Label = snapshot.Candidates[index].FieldPath
		}
	}
	service := filterSourceService(t)
	result, err := service.constructionFilterSourceCapability(context.Background(),
		filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), filterSourceOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	advertisedLabel := ""
	for _, choice := range result.Choices {
		if choice.FieldPath == filterSourceField {
			advertisedLabel = choice.Label
		}
	}
	if advertisedLabel != filterSourceField {
		t.Fatalf("offered label for an empty-Title candidate = %q, want the raw path %q (no resource prefix)", advertisedLabel, filterSourceField)
	}

	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		filterSourceFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
	}}
	candidate := construction
	candidate.Steps[0].Operation.Filter.ColumnID = filterSourceColumn
	candidate.Steps[0].Outputs = append(filterSourceIdentityColumns(), filterSourceProjectionColumn())
	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, construction),
		filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
	if err != nil {
		t.Fatalf("prepare filter sources with empty-Title candidate: %v", err)
	}
	if len(prepared.SourceProjections) != 1 || prepared.SourceProjections[0].Label != filterSourceField {
		t.Fatalf("durable label = %#v, want the raw path %q matching the advertised label", prepared.SourceProjections, filterSourceField)
	}
}

// labeledRowSourceChoicePlanner rewrites the planner's display label after
// signing, so a test can pin the builder chain when the snapshot candidate
// carries no label at all. Signing stays with the real resolver.
type labeledRowSourceChoicePlanner struct {
	inner *SchemaRowChoiceResolver
	label string
}

func (p labeledRowSourceChoicePlanner) ListRowChoices(ctx context.Context, snapshot capability.Snapshot, document authoringv2.Document) ([]capability.RowChoice, error) {
	choices, err := p.inner.ListRowChoices(ctx, snapshot, document)
	if err != nil {
		return nil, err
	}
	for index := range choices {
		choices[index].Label = p.label
	}
	return choices, nil
}

// With no snapshot candidate label, advertise falls through the builder
// chain to the row choice display label, and to the exact legacy
// 'ResourceType.path' when that is blank too; the durable claim never sees
// the choice label and lands on 'ResourceType.path'.
func TestConstructionFilterSourceBlankCandidateLabelFallsThroughBuilderChain(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-blank-label")
	for index := range snapshot.Candidates {
		if snapshot.Candidates[index].ID == "bs-active" {
			snapshot.Candidates[index].Label = ""
		}
	}
	advertise := func(label string) string {
		t.Helper()
		service := &Service{config: Config{RowChoicePlanner: labeledRowSourceChoicePlanner{inner: filterSourceResolver(t), label: label}}}
		result, err := service.constructionFilterSourceCapability(context.Background(),
			filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), filterSourceOutput, recipe.ConstructionSourceProjectionID)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range result.Choices {
			if choice.FieldPath == filterSourceField {
				return choice.Label
			}
		}
		t.Fatalf("blank-label field was not advertised: %#v", result.Choices)
		return ""
	}
	if got := advertise("  Generated display  "); got != "Generated display" {
		t.Fatalf("advertised label = %q, want the trimmed row choice display label", got)
	}
	if got := advertise("   "); got != "BodyStructure.active" {
		t.Fatalf("advertised label = %q, want the exact legacy 'Type.path' fallback", got)
	}

	service := filterSourceService(t)
	projection, err := service.resolveConstructionFilterSource(context.Background(),
		filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}),
		filterSourceProposalRequest("filter-active", nil), recipe.ConstructionSourceProjectionID,
		filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn))
	if err != nil {
		t.Fatalf("resolve with blank candidate label: %v", err)
	}
	if projection.Label != "BodyStructure.active" {
		t.Fatalf("durable label = %q, want the exact legacy 'Type.path' fallback", projection.Label)
	}
	// ConstructionFilterSourceFacts is the reconcile-side twin of the durable
	// write; it must produce the identical string for the identical claim.
	facts, ok := ConstructionFilterSourceFacts(snapshot, filterSourceRootType, filterSourceField)
	if !ok || facts.Label != projection.Label {
		t.Fatalf("facts label = %q (ok %v), want the durable label %q", facts.Label, ok, projection.Label)
	}
}

func TestConstructionFilterSourceCapabilityRefusesStaleStageAndRowChangingPrefix(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-stages")
	service := filterSourceService(t)
	stale, err := service.constructionFilterSourceCapability(context.Background(),
		filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), filterSourceOutput, "frontend-invented-stage")
	if err != nil {
		t.Fatal(err)
	}
	if stale.Supported || stale.ReasonCode != "STALE_STAGE_REFERENCE" || len(stale.Choices) != 0 {
		t.Fatalf("invented stage capability = %#v, want a stale-stage refusal without choices", stale)
	}
	blocked := filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{filterSourceGroupStep()}})
	blocked.stages = append(blocked.stages, explorer.ReceiptConstructionStage{ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"})
	blockedStage, err := service.constructionFilterSourceCapability(context.Background(), blocked, filterSourceOutput, "group-first")
	if err != nil {
		t.Fatal(err)
	}
	if blockedStage.Supported || len(blockedStage.Choices) != 0 {
		t.Fatalf("capability past a row-changing stage = %#v, want no choices offered", blockedStage)
	}
}

// (d) The observed/populated gate is enforced on admission, not only display.
func TestConstructionFilterSourceRejectsUnpopulatedFieldOnDiscoveryAndAdmission(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-unpopulated")
	service := filterSourceService(t)
	result, err := service.constructionFilterSourceCapability(context.Background(),
		filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}), filterSourceOutput, recipe.ConstructionSourceProjectionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range result.Choices {
		if !choice.IsPopulated {
			t.Fatalf("unpopulated field offered: %#v", choice)
		}
	}
	// The planner still issues a signed token for the schema-only field, so the
	// proposal boundary must refuse it rather than trust the picker.
	unpopulated := filterSourceRowChoice(t, snapshot, "description")
	if _, err := capability.DecodeRowChoiceID(unpopulated.ChoiceID); err != nil {
		t.Fatalf("crafted schema-only candidate produced no signed token: %v", err)
	}
	projection, err := service.resolveConstructionFilterSource(context.Background(),
		filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion}),
		filterSourceProposalRequest("filter-active", nil), recipe.ConstructionSourceProjectionID,
		ConstructionFilterSourceSelection{ChoiceID: unpopulated.ChoiceID, ColumnID: filterSourceColumn})
	if err == nil {
		t.Fatalf("unpopulated field was admitted as a filter source projection: %#v", projection)
	}
	if code := lifecycleErrorCode(err); code != "UNSUPPORTED_FILTER_SOURCE_FIELD" {
		t.Fatalf("unpopulated admission error = %v, want UNSUPPORTED_FILTER_SOURCE_FIELD", err)
	}
}

// (c) A token from another snapshot, or a tampered token, is refused.
func TestConstructionFilterSourceRefusesForgedAndCrossSnapshotChoices(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-current")
	other := filterSourceSnapshot("filter-source-other")
	service := filterSourceService(t)
	base := filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion})
	request := filterSourceProposalRequest("filter-active", nil)

	foreign := filterSourceRowChoice(t, other, filterSourceField)
	if _, err := service.resolveConstructionFilterSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionFilterSourceSelection{ChoiceID: foreign.ChoiceID, ColumnID: filterSourceColumn}); err == nil {
		t.Fatal("a choice issued against another snapshot was accepted")
	} else if code := lifecycleErrorCode(err); code != "INVALID_FILTER_SOURCE_CHOICE" {
		t.Fatalf("cross-snapshot error = %v, want INVALID_FILTER_SOURCE_CHOICE", err)
	}

	parts := strings.Split(filterSourceRowChoice(t, snapshot, filterSourceField).ChoiceID, ".")
	if len(parts) != 3 {
		t.Fatalf("signed choice is not a three-part token: %q", parts)
	}
	forged := parts[0] + "." + parts[1] + "." + strings.Repeat("0", len(parts[2]))
	if _, err := service.resolveConstructionFilterSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionFilterSourceSelection{ChoiceID: forged, ColumnID: filterSourceColumn}); err == nil {
		t.Fatal("a tampered choice token was accepted")
	} else if code := lifecycleErrorCode(err); code != "INVALID_FILTER_SOURCE_CHOICE" {
		t.Fatalf("tampered token error = %v, want INVALID_FILTER_SOURCE_CHOICE", err)
	}

	if _, err := service.resolveConstructionFilterSource(context.Background(), base, request, recipe.ConstructionSourceProjectionID,
		ConstructionFilterSourceSelection{ChoiceID: "rc1.not-a-real-token.deadbeef", ColumnID: filterSourceColumn}); err == nil {
		t.Fatal("an invented choice token was accepted")
	}
}

// (b) A valid selection compiles the filter and durably owns exactly one
// server-derived projection for the unselected field.
func TestConstructionCandidateWithFilterSourcesMaterializesOwnedProjection(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-apply")
	service := filterSourceService(t)
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		filterSourceFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
	}}
	base := filterSourceBase(snapshot, construction)

	candidate := construction
	candidate.Steps[0].Operation.Filter.ColumnID = filterSourceColumn
	candidate.Steps[0].Outputs = append(filterSourceIdentityColumns(), filterSourceProjectionColumn())

	prepared, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
	if err != nil {
		t.Fatalf("prepare filter sources: %v", err)
	}
	if len(prepared.SourceProjections) != 1 {
		t.Fatalf("prepared projections = %#v, want exactly one filter-owned projection", prepared.SourceProjections)
	}
	projection := prepared.SourceProjections[0]
	if projection.ColumnID != filterSourceColumn || projection.OwnerStepID != "filter-active" ||
		projection.OccurrenceID != authoringv2.RootOccurrenceID || projection.FieldPath != filterSourceField ||
		projection.FHIRType != "boolean" || projection.LogicalType != "boolean" || projection.Label != "Whether this record is in active use" {
		t.Fatalf("durable projection = %#v, want server-derived facts for the unselected field", projection)
	}
	carried := false
	for _, output := range prepared.Steps[0].Outputs {
		if output.ID == filterSourceColumn && output.Name == authoringv2.ConstructionSourceProjectionName(filterSourceColumn) && output.Type == "boolean" {
			carried = true
		}
	}
	if !carried {
		t.Fatalf("filter step omitted the projection output: %#v", prepared.Steps[0].Outputs)
	}
	analyzing := filterSourceDocument()
	analyzing.Construction = &prepared
	analyzed, impact, err := analyzing.AnalyzeConstructionCandidate(prepared, "filter-active", nil)
	if err != nil {
		t.Fatalf("filter-consuming candidate was refused by the real authoring validator: %v", err)
	}
	if impact.HasMissingInputs() {
		t.Fatalf("filter-consuming candidate needs repair: %#v", impact.MissingInputs)
	}
	if len(analyzed.Construction.SourceProjections) != 1 || analyzed.Construction.SourceProjections[0].OwnerStepID != "filter-active" {
		t.Fatalf("analysis dropped or re-owned the filter projection: %#v", analyzed.Construction.SourceProjections)
	}
	// Independently derived expectation: the surviving rows still expose just the
	// selected identity, and the filtered-on field is carried as an input slot,
	// so the output schema still equals the input schema.
	wantIDs := []string{filterSourceIdentity, filterSourceColumn}
	gotIDs := make([]string, 0, len(analyzed.Construction.Steps[0].Outputs))
	for _, output := range analyzed.Construction.Steps[0].Outputs {
		gotIDs = append(gotIDs, output.ID)
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("filter output ids = %v, want %v", gotIDs, wantIDs)
	}
}

func TestConstructionFilterSourceRequiresMatchingColumnAndIgnoresNonFilterSteps(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-mismatch")
	service := filterSourceService(t)
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		filterSourceFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
	}}
	base := filterSourceBase(snapshot, construction)

	_, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, "filter_source_other")}), construction)
	if err == nil {
		t.Fatal("a filter source whose columnId is not its filter column was accepted")
	}
	if code := lifecycleErrorCode(err); code != "FILTER_SOURCE_COLUMN_MISMATCH" {
		t.Fatalf("mismatched column error = %v, want FILTER_SOURCE_COLUMN_MISMATCH", err)
	}

	// Mirroring Pivot: filterSources that do not belong to the edited FILTER
	// step leave the construction untouched.
	unedited, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest("missing-step", []ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), construction)
	if err != nil {
		t.Fatalf("filterSources with an unknown changed step failed: %v", err)
	}
	if !reflect.DeepEqual(unedited, construction) {
		t.Fatalf("filterSources on a non-filter step mutated the candidate: %#v", unedited)
	}
}

// The row-preserving prefix allow-list is enforced, not trust-me coded.
func TestConstructionFilterSourceCannotCrossRowChangingPrefix(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-prefix")
	service := filterSourceService(t)
	group := filterSourceGroupStep()
	filterInputs := []authoringv2.StageColumn{
		{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
		{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
	}
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{group, filterSourceFilterStep("group_key", filterInputs)}}
	candidate.Steps[1].Operation.Filter.ColumnID = filterSourceColumn
	candidate.Steps[1].Outputs = append(append([]authoringv2.StageColumn(nil), filterInputs...), filterSourceProjectionColumn())

	base := filterSourceBase(snapshot, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{group}})
	base.stages = append(base.stages, explorer.ReceiptConstructionStage{
		ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP",
		Columns: []explorer.ReceiptConstructionStageColumn{
			{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
			{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
		},
	})

	_, err := service.constructionCandidateWithFilterSources(context.Background(), base,
		filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{filterSourceSelection(t, snapshot, filterSourceField, filterSourceColumn)}), candidate)
	if err == nil {
		t.Fatal("a root filter source was carried across a GROUP")
	}
	if code := lifecycleErrorCode(err); code != "FILTER_SOURCE_CANNOT_CROSS_STAGE" {
		t.Fatalf("root filter source across a GROUP error = %v, want FILTER_SOURCE_CANNOT_CROSS_STAGE", err)
	}
}

// A projection the filter does not own is refused, and so is one whose owner
// filter names a different column.
func TestConstructionFilterSourceProjectionMustMatchItsFilterColumn(t *testing.T) {
	document := filterSourceDocument()

	foreign := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			filterSourceFilterStep(filterSourceIdentity, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: filterSourceColumn, OwnerStepID: "another-step", OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
		}},
	}
	foreignDocument := document
	foreignDocument.Construction = &foreign
	if err := foreignDocument.Validate(); err == nil {
		t.Fatal("a projection owned by a step that does not consume it was accepted")
	}

	mismatched := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			filterSourceFilterStep(filterSourceIdentity, append(filterSourceIdentityColumns(), filterSourceProjectionColumn())),
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: filterSourceColumn, OwnerStepID: "filter-active", OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
		}},
	}
	mismatchedDocument := document
	mismatchedDocument.Construction = &mismatched
	if err := mismatchedDocument.Validate(); err == nil || !strings.Contains(err.Error(), filterSourceColumn) {
		t.Fatalf("projection not matching its filter columnId error = %v, want it named %q", err, filterSourceColumn)
	}
}

// (e) Dropping the selection strips its projection, and a filter left pointing
// at the removed source fails instead of silently keeping it.
func TestConstructionFilterSourceRemovalStripsProjectionAndRefusesDanglingFilter(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-removal")
	service := filterSourceService(t)
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: filterSourceColumn, OwnerStepID: "filter-active", OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
	}
	outputs := append(filterSourceIdentityColumns(), filterSourceProjectionColumn())
	saved := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{filterSourceFilterStep(filterSourceColumn, outputs)},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}

	savedDocument := filterSourceDocument()
	savedDocument.Construction = &saved
	if err := savedDocument.Validate(); err != nil {
		t.Fatalf("a saved filter-owned construction is not durable authoring state: %v", err)
	}

	cleared, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, saved),
		filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{}), saved)
	if err != nil {
		t.Fatalf("clearing the filter source selection failed: %v", err)
	}
	if len(cleared.SourceProjections) != 0 {
		t.Fatalf("cleared selection kept a filter-owned projection: %#v", cleared.SourceProjections)
	}
	for _, output := range cleared.Steps[0].Outputs {
		if output.ID == filterSourceColumn {
			t.Fatalf("cleared selection left the projection column in the filter output: %#v", cleared.Steps[0].Outputs)
		}
	}

	dangling := filterSourceDocument()
	dangling.Construction = &authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{filterSourceFilterStep(filterSourceColumn, outputs)},
	}
	if err := dangling.Validate(); err == nil || !strings.Contains(err.Error(), filterSourceColumn) {
		t.Fatalf("dangling filter validation error = %v, want the missing projection column named", err)
	}
	analysisBase := filterSourceDocument()
	analysisBase.Construction = &saved
	if _, impact, err := analysisBase.AnalyzeConstructionCandidate(cleared, "filter-active", nil); err == nil && !impact.HasMissingInputs() {
		t.Fatal("analysis silently kept a filter whose source selection was removed")
	}
}

// (f) Selected-column filter behavior is unchanged by filter source support.
func TestConstructionFilterSourceLeavesSelectedColumnFilterBehaviorUnchanged(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-regression")
	service := filterSourceService(t)
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		filterSourceFilterStep(filterSourceIdentity, filterSourceIdentityColumns()),
	}}
	document := filterSourceDocument()
	document.Construction = &construction
	if _, _, err := document.AnalyzeConstructionCandidate(construction, "filter-active", nil); err != nil {
		t.Fatalf("existing selected-column filter no longer compiles: %v", err)
	}
	unchanged, err := service.constructionCandidateWithFilterSources(context.Background(), filterSourceBase(snapshot, construction),
		filterSourceProposalRequest("filter-active", nil), construction)
	if err != nil {
		t.Fatalf("filter request without filterSources failed: %v", err)
	}
	if !reflect.DeepEqual(unchanged, construction) {
		t.Fatalf("omitted filterSources mutated the candidate: %#v", unchanged)
	}
}

// Duplicate or blank selection facts are refused at the request boundary.
func TestConstructionFilterSourceSelectionsRequireExactDeduplicatedFacts(t *testing.T) {
	duplicate := filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{
		{ChoiceID: "rc1.a.b", ColumnID: "c1"}, {ChoiceID: "rc1.a.b", ColumnID: "c2"},
	})
	if err := duplicate.Validate(); err == nil || !strings.Contains(err.Error(), "filterSources") {
		t.Fatalf("duplicate filterSources error = %v, want a filterSources dedup refusal", err)
	}
	blank := filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{{ChoiceID: " ", ColumnID: "c1"}})
	if err := blank.Validate(); err == nil || !strings.Contains(err.Error(), "filterSources[0].choiceId") {
		t.Fatalf("blank choiceId error = %v, want filterSources[0].choiceId refusal", err)
	}
	blankColumn := filterSourceProposalRequest("filter-active", []ConstructionFilterSourceSelection{{ChoiceID: "rc1.a.b", ColumnID: " "}})
	if err := blankColumn.Validate(); err == nil || !strings.Contains(err.Error(), "filterSources[0].columnId") {
		t.Fatalf("blank columnId error = %v, want filterSources[0].columnId refusal", err)
	}
	encoded, err := json.Marshal(ConstructionFilterSourceSelection{ChoiceID: "rc1.x.y", ColumnID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"choiceId":"rc1.x.y","columnId":"c"}` {
		t.Fatalf("filterSources selection wire shape = %s", encoded)
	}
}

// (g) The advertised choice object is EXACTLY the six pinned wire fields. Any
// pivot-style extra field (occurrenceId/fhirType/valueType/isIdentifier/
// isReference) makes the strict UI schema reject the whole capabilities
// response, so the shape is pinned structurally and on the wire.
func TestConstructionFilterSourceChoiceWireShapeIsExactlySixFields(t *testing.T) {
	wantFields := map[string]bool{"choiceId": true, "fieldPath": true, "label": true, "logicalType": true, "isPopulated": true, "columnId": true}
	structFields := make(map[string]bool, reflect.TypeOf(ConstructionFilterSourceChoice{}).NumField())
	for index := range reflect.TypeOf(ConstructionFilterSourceChoice{}).NumField() {
		tag := strings.Split(reflect.TypeOf(ConstructionFilterSourceChoice{}).Field(index).Tag.Get("json"), ",")[0]
		structFields[tag] = true
	}
	if !reflect.DeepEqual(structFields, wantFields) {
		t.Fatalf("ConstructionFilterSourceChoice struct fields = %v, want exactly %v", structFields, wantFields)
	}
	for _, pivotExtra := range []string{"occurrenceId", "fhirType", "valueType", "isIdentifier", "isReference"} {
		if structFields[pivotExtra] {
			t.Fatalf("pivot-style field %q must never appear on a filter source choice: %#v", pivotExtra, structFields)
		}
	}
	encoded, err := json.Marshal(ConstructionFilterSourceChoice{
		ChoiceID: "rc1.a.b", FieldPath: filterSourceField, Label: "BodyStructure.active", LogicalType: "boolean", IsPopulated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(wantFields) {
		t.Fatalf("encoded choice has %d keys, want exactly %d: %s", len(decoded), len(wantFields), encoded)
	}
	for field := range wantFields {
		if _, present := decoded[field]; !present {
			t.Fatalf("encoded choice omitted the pinned field %q: %s", field, encoded)
		}
	}
}

// The advertised columnId is the signed materialized column the UI uses to
// restore a saved source-backed filter. It must come only from durable
// server-derived state that is still live for exactly this stage: a vanished,
// re-pointed, or foreign-owned materialization is advertised as absent.
func TestConstructionFilterSourceCapabilityAdvertisesDurableColumnIdOnlyWhileSigned(t *testing.T) {
	snapshot := filterSourceSnapshot("filter-source-column-binding")
	service := filterSourceService(t)
	projection := authoringv2.ConstructionSourceProjection{
		ColumnID: filterSourceColumn, OwnerStepID: "filter-active", OccurrenceID: authoringv2.RootOccurrenceID,
		FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
	}
	outputs := append(filterSourceIdentityColumns(), filterSourceProjectionColumn())
	choiceColumnID := func(t *testing.T, construction authoringv2.Construction, stageID string) string {
		t.Helper()
		base := filterSourceBase(snapshot, construction)
		if stageID != recipe.ConstructionSourceProjectionID {
			base.stages = append(base.stages, explorer.ReceiptConstructionStage{ID: stageID, InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"})
		}
		result, err := service.constructionFilterSourceCapability(context.Background(), base, filterSourceOutput, stageID)
		if err != nil {
			t.Fatal(err)
		}
		for _, choice := range result.Choices {
			if choice.FieldPath == filterSourceField {
				return choice.ColumnID
			}
		}
		return "<missing choice>"
	}
	live := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{filterSourceFilterStep(filterSourceColumn, outputs)},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	if got := choiceColumnID(t, live, recipe.ConstructionSourceProjectionID); got != filterSourceColumn {
		t.Fatalf("live durable materialization advertised columnId %q, want %q", got, filterSourceColumn)
	}
	repointed := authoringv2.Construction{
		Version:           authoringv2.ConstructionVersion,
		Steps:             []authoringv2.ConstructionStep{filterSourceFilterStep(filterSourceIdentity, outputs)},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	if got := choiceColumnID(t, repointed, recipe.ConstructionSourceProjectionID); got != "" {
		t.Fatalf("saved condition column no longer present was still advertised as columnId %q", got)
	}
	foreignOwner := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps:   []authoringv2.ConstructionStep{filterSourceFilterStep(filterSourceColumn, outputs)},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: filterSourceColumn, OwnerStepID: "another-step", OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: filterSourceField, FHIRType: "boolean", LogicalType: "boolean", Label: "BodyStructure.active",
		}},
	}
	if got := choiceColumnID(t, foreignOwner, recipe.ConstructionSourceProjectionID); got != "" {
		t.Fatalf("materialization not owned by this stage's filter was advertised as columnId %q", got)
	}
	group := filterSourceGroupStep()
	pastPrefix := filterSourceFilterStep(filterSourceColumn, []authoringv2.StageColumn{
		{ID: "group_key", Name: "group_key", Label: "Group", Type: "string"},
		{ID: "rows", Name: "rows", Label: "Rows", Type: "integer"},
		filterSourceProjectionColumn(),
	})
	pastPrefix.Inputs = []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "group-first"}}
	otherStage := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			group, pastPrefix,
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
	}
	if got := choiceColumnID(t, otherStage, recipe.ConstructionSourceProjectionID); got != "" {
		t.Fatalf("materialization owned past the row-changing prefix was advertised at the source stage as columnId %q", got)
	}
}

// (a, wiring) GetConstructionCapabilities assembles filterSourceInput beside
// pivotSourceInput on the owned-style fixture.
func TestGetConstructionCapabilitiesAssemblesFilterSourceInputBesidePivotSourceInput(t *testing.T) {
	harness := newFilterSourceHarness(t)
	capabilities, err := harness.service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatalf("capabilities with filter source discovery: %v", err)
	}
	if capabilities.PivotSourceInput.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("pivot source input was displaced: %#v", capabilities.PivotSourceInput)
	}
	result := capabilities.FilterSourceInput
	if result.StageID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("filter source input stage = %#v, want the selected source stage", result)
	}
	byPath := make(map[string]ConstructionFilterSourceChoice, len(result.Choices))
	for _, choice := range result.Choices {
		byPath[choice.FieldPath] = choice
	}
	birth, found := byPath["multipleBirthInteger"]
	if !found {
		t.Fatalf("populated unselected root scalar missing from capabilities: %#v", result.Choices)
	}
	if !birth.IsPopulated || birth.LogicalType != "integer" || birth.Label != "Birth count" {
		t.Fatalf("birth count choice = %#v, want populated integer with the generated label", birth)
	}
	if birth.ColumnID != "" {
		t.Fatalf("birth count choice advertised columnId %q before any filter source was materialized", birth.ColumnID)
	}
	identity, err := capability.DecodeRowChoiceID(birth.ChoiceID)
	if err != nil || identity.SnapshotToken != harness.snapshot.Token || identity.SchemaDigest != harness.snapshot.Identity.SchemaDigest {
		t.Fatalf("capability choice id is not pinned to the current snapshot: %v %#v", err, identity)
	}
	if _, offered := byPath["maritalStatus"]; offered {
		t.Fatal("a non-observed field was advertised as a filter source")
	}
	encoded, err := json.Marshal(capabilities)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"filterSourceInput":{`) {
		t.Fatalf("capabilities response omitted the pinned filterSourceInput field: %s", encoded)
	}
}

// (b) Through ProposeConstruction with the real compiler, the accepted
// candidate filters on the unselected field's compiled slot.
func TestProposeConstructionFilterSourceFiltersUnselectedFieldWithRealCompiler(t *testing.T) {
	harness := newFilterSourceHarness(t)
	harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		filterSourcePatientFilterStep("patient-id"),
	}})
	version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest
	acceptedBefore := append([]byte(nil), harness.store.created.DraftConfig...)

	proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
		ChangedStepID:         "filter-birth",
		CandidateConstruction: harness.candidateConstruction(),
		FilterSources:         []ConstructionFilterSourceSelection{{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: filterSourceColumn}},
	})
	if err != nil {
		t.Fatalf("ProposeConstruction with filterSources: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("filter source proposal = %#v, want a compiled preview proposal", proposal)
	}
	if len(proposal.CandidateConstruction.SourceProjections) != 1 {
		t.Fatalf("proposal projections = %#v, want the durable filter-owned projection", proposal.CandidateConstruction.SourceProjections)
	}
	durable := proposal.CandidateConstruction.SourceProjections[0]
	if durable.OwnerStepID != "filter-birth" || durable.ColumnID != filterSourceColumn || durable.FieldPath != "multipleBirthInteger" ||
		durable.FHIRType != "integer" || durable.LogicalType != "integer" || durable.OccurrenceID != authoringv2.RootOccurrenceID {
		t.Fatalf("durable projection = %#v, want server-derived facts for the unselected field", durable)
	}
	if harness.store.saveDraftCalls != 0 || string(acceptedBefore) != string(harness.store.created.DraftConfig) {
		t.Fatalf("proposal changed the accepted draft: saves=%d", harness.store.saveDraftCalls)
	}

	receipt := harness.store.receipt
	if receipt == nil || len(receipt.Bundle.Outputs) != 1 {
		t.Fatalf("compiled receipt omits the candidate bundle: %#v", receipt)
	}
	output := receipt.Bundle.Outputs[0]
	name := authoringv2.ConstructionSourceProjectionName(filterSourceColumn)
	var slotColumnID, slotFieldRef, slotSelect string
	for index := range output.Fields {
		if output.Fields[index].Name == name {
			slotColumnID, slotFieldRef, slotSelect = output.Fields[index].ColumnID, output.Fields[index].FieldRef, output.Fields[index].Expr.Select
		}
	}
	if slotColumnID != filterSourceColumn || slotFieldRef != "multipleBirthInteger" || !strings.HasSuffix(slotSelect, ".multipleBirthInteger") {
		t.Fatalf("compiled slot for the unselected field = column %q fieldRef %q select %q, want it to read the unselected field", slotColumnID, slotFieldRef, slotSelect)
	}
	var filterColumnID string
	carried := false
	for _, step := range output.Construction.Steps {
		if step.Operation.Kind != recipe.ConstructionFilterOp || step.Operation.Filter == nil {
			continue
		}
		filterColumnID = step.Operation.Filter.ColumnID
		for _, column := range step.Outputs {
			if column.ID == filterSourceColumn {
				carried = true
			}
		}
	}
	if filterColumnID != filterSourceColumn {
		t.Fatalf("compiled FILTER columnId = %q, want the signed unselected field slot", filterColumnID)
	}
	if !carried {
		t.Fatalf("compiled FILTER step did not carry the projection: %#v", output.Construction.Steps)
	}
}

// (6) An accepted proposal whose field stops qualifying is refused at Apply.
func TestApplyConstructionProposalRefusesStaleFilterSourceBinding(t *testing.T) {
	for _, stale := range []bool{false, true} {
		name := "current"
		if stale {
			name = "stale"
		}
		t.Run(name, func(t *testing.T) {
			harness := newFilterSourceHarness(t)
			harness.saveDraft(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
				filterSourcePatientFilterStep("patient-id"),
			}})
			version, digest := harness.store.created.DraftVersion, harness.store.created.DraftDigest
			proposal, err := harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
				Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: version, ExpectedDraftDigest: digest, OutputID: "patients",
				ChangedStepID:         "filter-birth",
				CandidateConstruction: harness.candidateConstruction(),
				FilterSources:         []ConstructionFilterSourceSelection{{ChoiceID: harness.choice(t, "multipleBirthInteger").ChoiceID, ColumnID: filterSourceColumn}},
			})
			if err != nil {
				t.Fatalf("ProposeConstruction: %v", err)
			}
			if stale {
				harness.unobserveBirthCount()
			}
			_, err = harness.service.ApplyCommands(context.Background(), harness.store.created.Project, harness.store.created.ExplorerID, authoringv2.ApplyCommandsRequest{
				CommandID: "filter-source-apply", SemanticsVersion: authoringv2.CurrentSemanticsVersion, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: version, ExpectedDraftDigest: digest,
				Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: "patients", ProposalID: proposal.ProposalID}},
			}, "filter-source-actor")
			if stale {
				if err == nil || lifecycleErrorCode(err) != "STALE_FILTER_SOURCE_BINDING" {
					t.Fatalf("stale filter source apply error = %v, want STALE_FILTER_SOURCE_BINDING", err)
				}
				if harness.store.created.DraftVersion != version || harness.store.created.DraftDigest != digest || harness.store.saveDraftCalls != 0 {
					t.Fatalf("stale apply changed the accepted draft: version=%d digest=%q saves=%d", harness.store.created.DraftVersion, harness.store.created.DraftDigest, harness.store.saveDraftCalls)
				}
				return
			}
			if err != nil {
				t.Fatalf("ApplyCommands with a current filter source: %v", err)
			}
			applied := harness.store.created
			if applied == nil {
				t.Fatal("apply did not save the draft")
			}
			workspace, decodeErr := authoringv2.DecodeWorkspace(applied.DraftConfig)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			saved := workspace.Documents[0].Construction
			if saved == nil || len(saved.SourceProjections) != 1 || saved.SourceProjections[0].OwnerStepID != "filter-birth" ||
				saved.SourceProjections[0].FieldPath != "multipleBirthInteger" {
				t.Fatalf("applied construction lost the durable filter projection: %#v", saved)
			}
			capabilities, capErr := harness.service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
				Project: applied.Project, ExplorerID: applied.ExplorerID, SnapshotToken: harness.snapshot.Token,
				ExpectedDraftVersion: applied.DraftVersion, ExpectedDraftDigest: applied.DraftDigest,
				OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
			})
			if capErr != nil {
				t.Fatalf("capabilities after the durable filter source applied: %v", capErr)
			}
			restored := ""
			for _, choice := range capabilities.FilterSourceInput.Choices {
				if choice.FieldPath == "multipleBirthInteger" {
					restored = choice.ColumnID
				}
			}
			if restored != filterSourceColumn {
				t.Fatalf("durable materialization should advertise columnId %q for UI restore, got %q", filterSourceColumn, restored)
			}
		})
	}
}

// A restore of a saved source-backed filter whose field stopped presenting
// data must fail closed at proposal time like a forged token: the durable
// columnId alone never vouches for the vanished field.
func TestProposeConstructionFilterSourceRefusesVanishedFieldRestore(t *testing.T) {
	harness := newFilterSourceHarness(t)
	durable := authoringv2.Construction{
		Version: authoringv2.ConstructionVersion,
		Steps: []authoringv2.ConstructionStep{
			{
				ID:     "filter-birth",
				Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
				Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
					ColumnID: filterSourceColumn, Operator: authoringv2.ConstructionFilterExists,
				}},
				Outputs: []authoringv2.StageColumn{
					{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: filterSourceColumn, Name: authoringv2.ConstructionSourceProjectionName(filterSourceColumn),
						Label: "Patient.multipleBirthInteger", Type: "integer"},
				},
			},
		},
		SourceProjections: []authoringv2.ConstructionSourceProjection{{
			ColumnID: filterSourceColumn, OwnerStepID: "filter-birth", OccurrenceID: authoringv2.RootOccurrenceID,
			FieldPath: "multipleBirthInteger", FHIRType: "integer", LogicalType: "integer", Label: "Patient.multipleBirthInteger",
		}},
	}
	harness.saveDraft(t, durable)
	restoreChoice := harness.choice(t, "multipleBirthInteger").ChoiceID
	capabilities, err := harness.service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", StageID: recipe.ConstructionSourceProjectionID,
	})
	if err != nil {
		t.Fatalf("capabilities before the field vanished: %v", err)
	}
	advertised := ""
	for _, choice := range capabilities.FilterSourceInput.Choices {
		if choice.FieldPath == "multipleBirthInteger" {
			advertised = choice.ColumnID
		}
	}
	if advertised != filterSourceColumn {
		t.Fatalf("capabilities should offer the durable materialization columnId %q for restore, got %q", filterSourceColumn, advertised)
	}

	harness.unobserveBirthCount()
	acceptedBefore := append([]byte(nil), harness.store.created.DraftConfig...)
	_, err = harness.service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
		Project: harness.store.created.Project, ExplorerID: harness.store.created.ExplorerID, SnapshotToken: harness.snapshot.Token,
		ExpectedDraftVersion: harness.store.created.DraftVersion, ExpectedDraftDigest: harness.store.created.DraftDigest,
		OutputID: "patients", ChangedStepID: "filter-birth",
		CandidateConstruction: harness.candidateConstruction(),
		FilterSources:         []ConstructionFilterSourceSelection{{ChoiceID: restoreChoice, ColumnID: filterSourceColumn}},
	})
	if err == nil {
		t.Fatal("a restore of a vanished field via its durable columnId was accepted")
	}
	if code := lifecycleErrorCode(err); code != "UNSUPPORTED_FILTER_SOURCE_FIELD" && code != "INVALID_FILTER_SOURCE_CHOICE" {
		t.Fatalf("vanished restore error = %v, want a forged-token-style refusal at the proposal boundary", err)
	}
	if harness.store.saveDraftCalls != 0 || string(acceptedBefore) != string(harness.store.created.DraftConfig) {
		t.Fatalf("refused restore changed the accepted draft: saves=%d", harness.store.saveDraftCalls)
	}
}

func filterSourcePatientFilterStep(columnID string) authoringv2.ConstructionStep {
	return authoringv2.ConstructionStep{
		ID:     "filter-birth",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: columnID, Operator: authoringv2.ConstructionFilterExists,
		}},
		Outputs: []authoringv2.StageColumn{{ID: "patient-id", Name: "patient_id", Label: "Patient ID", Type: "string"}},
	}
}

// filterSourceHarness wires the real workspace compiler/lowering path and the
// real schema-backed row-choice planner around the patients fixture.
type filterSourceHarness struct {
	service  *Service
	store    *fakeStore
	snapshot capability.Snapshot
	proposal *explorer.CompilationReceipt
}

func filterSourceBirthCandidate(populated bool) capability.Candidate {
	birth := capability.Candidate{
		ID: "candidate-birth-count", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "multipleBirthInteger",
		Label: "Birth count", LogicalType: "integer", Cardinality: "optional_one",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 3,
	}
	if !populated {
		birth.Observed, birth.Populated, birth.ObservedDocumentCount = false, false, 0
	}
	return birth
}

func filterSourcePatientCandidates(populated bool) []capability.Candidate {
	return []capability.Candidate{
		{ID: "candidate-id", NodeID: "node-patient", ResourceType: "Patient", FieldPath: "id", Label: "Patient ID",
			LogicalType: "string", Cardinality: "optional_one", ProjectionModes: []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect}, Observed: true, Populated: true, ObservedDocumentCount: 3},
		filterSourceBirthCandidate(populated),
	}
}

func newFilterSourceHarness(t *testing.T) *filterSourceHarness {
	t.Helper()
	service, store, snapshot := constructionProposalService(t)
	snapshot.Nodes = []capability.Node{{ID: "node-patient", ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient", Populated: true, DocumentCount: 3}}
	snapshot.Candidates = filterSourcePatientCandidates(true)
	resolver := filterSourceResolver(t)
	service.config.RowChoicePlanner, service.config.RowChoiceResolver = resolver, resolver
	harness := &filterSourceHarness{service: service, store: store, snapshot: snapshot}
	authorize := func() AuthorizedCapability {
		return AuthorizedCapability{Snapshot: harness.snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}
	}
	service.config.Capability.Token = func(context.Context, string, string) (capability.Snapshot, error) { return harness.snapshot, nil }
	service.config.Capability.ForCompilation = func(context.Context, string, string) (AuthorizedCapability, error) { return authorize(), nil }
	service.config.Capability.ForExecution = func(context.Context, string, string) (AuthorizedCapability, error) { return authorize(), nil }
	service.config.Capability.Catalog = func(got capability.Snapshot, explorerID string) authoringv2.CatalogSnapshot {
		catalog := lifecycleInterpretationCatalog(got, explorerID)
		for _, candidate := range harness.snapshot.Candidates {
			if candidate.FieldPath == "id" {
				continue
			}
			choice, err := capability.NewFieldConstructionChoice(got.Token, candidate)
			if err != nil {
				t.Fatalf("make source choice for %s: %v", candidate.ID, err)
			}
			catalog.Candidates = append(catalog.Candidates, authoringv2.CatalogCandidate{
				ID: candidate.ID, NodeID: candidate.NodeID, FieldPath: candidate.FieldPath, Label: candidate.Label,
				LogicalType: candidate.LogicalType, Cardinality: candidate.Cardinality,
				ProjectionModes: []string{"VALUE"}, DefaultProjectionMode: "VALUE", ConstructionChoice: &choice,
			})
		}
		return catalog
	}
	service.config.ReceiptLookup = func(_ context.Context, _, _, receiptID string) (*explorer.CompilationReceipt, error) {
		if harness.proposal != nil && harness.proposal.ID == receiptID {
			return harness.proposal, nil
		}
		if store.receipt != nil && store.receipt.ID == receiptID {
			return store.receipt, nil
		}
		return nil, explorer.ErrNotFound
	}
	service.config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt, err := compileTraceReceipt(ctx, request, harness.snapshot, harness.service.catalog(harness.snapshot, request.ExplorerID))
		if err != nil {
			return nil, err
		}
		receipt.RowDefinitionProposal = cloneRowDefinitionProposalBinding(request.RowDefinitionProposal)
		store.receipt = receipt
		if request.ConstructionProposal != nil {
			harness.proposal = receipt
		}
		return receipt, nil
	}
	service.config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview did not receive the proposal receipt")
		}
		return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"patient_id"}, Complete: true}, nil
	}
	return harness
}

// saveDraft stores an owned-style construction whose only selected column is
// the row identity.
func (h *filterSourceHarness) saveDraft(t *testing.T, construction authoringv2.Construction) {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(h.store.created.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	workspace.SemanticsVersion = authoringv2.CurrentSemanticsVersion
	document := workspace.Documents[0]
	document.Rows = authoringv2.RecordsRowDefinition()
	for index := range document.Columns {
		if document.Columns[index].Column == "patient_id" && document.Columns[index].ColumnID == "" {
			document.Columns[index].ColumnID = "patient-id"
		}
	}
	document.TableShape = nil
	document.Construction = &construction
	workspace.Documents[0] = document
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate filter source draft: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	h.store.created.DraftConfig, h.store.created.DraftDigest = draft, digest
}

func (h *filterSourceHarness) candidateConstruction() authoringv2.Construction {
	candidate := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		filterSourcePatientFilterStep(filterSourceColumn),
	}}
	candidate.Steps[0].Outputs = append(candidate.Steps[0].Outputs, authoringv2.StageColumn{
		ID: filterSourceColumn, Name: authoringv2.ConstructionSourceProjectionName(filterSourceColumn),
		Label: "Patient.multipleBirthInteger", Type: "integer",
	})
	return candidate
}

func (h *filterSourceHarness) choice(t *testing.T, path string) capability.RowChoice {
	t.Helper()
	document := authoringv2.Document{
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
	}
	choices, err := filterSourceResolver(t).ListRowChoices(context.Background(), h.snapshot, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		if choice.Kind == capability.RowChoiceFieldGroupKey && choice.OccurrenceID == authoringv2.RootOccurrenceID && choice.Path == path {
			return choice
		}
	}
	t.Fatalf("planner omitted the %s choice: %#v", path, choices)
	return capability.RowChoice{}
}

// unobserveBirthChange makes the selected field stop presenting observed data
// while keeping the same snapshot identity, mimicking data going empty.
func (h *filterSourceHarness) unobserveBirthCount() {
	for index := range h.snapshot.Candidates {
		if h.snapshot.Candidates[index].FieldPath == "multipleBirthInteger" {
			h.snapshot.Candidates[index] = filterSourceBirthCandidate(false)
		}
	}
}

// --- ROW-SOURCE-03 S-A: precise per-skipped-class lane reason codes -------

// staleRowSourceChoicePlanner signs every enumerated choice against a
// different snapshot, mimicking a planner hand-off race between two dataset
// versions. The real SchemaRowChoiceResolver always pins the passed snapshot,
// so this only exists to exercise the version skip class.
type staleRowSourceChoicePlanner struct {
	inner  *SchemaRowChoiceResolver
	issued capability.Snapshot
}

func (p staleRowSourceChoicePlanner) ListRowChoices(ctx context.Context, _ capability.Snapshot, document authoringv2.Document) ([]capability.RowChoice, error) {
	return p.inner.ListRowChoices(ctx, p.issued, document)
}

type emptyRowSourceChoicePlanner struct{}

func (emptyRowSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error) {
	return nil, nil
}

func rowSourceServiceWithPlanner(planner RowChoicePlanner) *Service {
	return &Service{config: Config{RowChoicePlanner: planner}}
}

func rowSourceSnapshotWithGeneration(snapshot capability.Snapshot, generation string) capability.Snapshot {
	identity := snapshot.Identity
	identity.Generation = generation
	return capability.NewSnapshot(identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
}

func rowSourceLaneCandidate(node, resourceType, id, path, logicalType, cardinality string, populated bool) capability.Candidate {
	candidate := capability.Candidate{
		ID: id, NodeID: node, ResourceType: resourceType, FieldPath: path,
		Label: path, LogicalType: logicalType, Cardinality: cardinality,
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
	}
	if populated {
		candidate.Observed, candidate.Populated, candidate.ObservedDocumentCount = true, true, 4
	}
	return candidate
}

func TestConstructionFilterSourceCapabilityReportsPreciseSkipClassReasonCodes(t *testing.T) {
	resolver := filterSourceResolver(t)
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	rowChanging := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{filterSourceGroupStep()}}
	blockedStages := []explorer.ReceiptConstructionStage{{ID: "group-first", InputStageID: recipe.ConstructionSourceProjectionID, Operation: "GROUP"}}
	bs := func(id, path, logicalType, cardinality string, populated bool) capability.Candidate {
		return rowSourceLaneCandidate(filterSourceRootNode, filterSourceRootType, id, path, logicalType, cardinality, populated)
	}
	candidatesSnapshot := func(token string, candidates ...capability.Candidate) capability.Snapshot {
		snapshot := filterSourceSnapshot(token)
		snapshot.Candidates = candidates
		return snapshot
	}
	rows := []struct {
		name         string
		snapshot     capability.Snapshot
		service      *Service
		construction authoringv2.Construction
		stageID      string
		stages       []explorer.ReceiptConstructionStage
		wantCode     string
	}{
		{
			name: "every eligible field has no observed values",
			snapshot: candidatesSnapshot("filter-source-precise-empty",
				bs("bs-id", "id", "string", "optional_one", false),
				bs("bs-active", filterSourceField, "boolean", "optional_one", false)),
			service: filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_OBSERVED_VALUES_FILTER_SOURCE_FIELDS",
		},
		{
			name: "every field carries a logical type the Filter lane refuses",
			snapshot: candidatesSnapshot("filter-source-precise-type",
				bs("bs-id", "id", "quantity", "optional_one", true),
				bs("bs-active", filterSourceField, "quantity", "optional_one", true)),
			service: filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_TYPE_ELIGIBLE_FILTER_SOURCE_FIELDS",
		},
		{
			name:     "every field is repeated at the row root",
			snapshot: candidatesSnapshot("filter-source-precise-route", bs("bs-active", filterSourceField, "boolean", "many", true)),
			service:  filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ROUTE_ELIGIBLE_FILTER_SOURCE_FIELDS",
		},
		{
			name:     "choices were issued for a previous dataset snapshot",
			snapshot: filterSourceSnapshot("filter-source-precise-stale"),
			service: rowSourceServiceWithPlanner(staleRowSourceChoicePlanner{
				inner:  resolver,
				issued: rowSourceSnapshotWithGeneration(filterSourceSnapshot("filter-source-precise-stale"), "filter-source-precise-stale-issued"),
			}),
			construction: empty, stageID: recipe.ConstructionSourceProjectionID,
			wantCode: "STALE_FILTER_SOURCE_SNAPSHOT",
		},
		{
			name:     "the construction prefix cannot carry root fields to the stage",
			snapshot: filterSourceSnapshot("filter-source-precise-reach"),
			service:  filterSourceService(t), construction: rowChanging, stageID: "group-first",
			stages:   blockedStages,
			wantCode: "ROOT_SOURCE_PROJECTION_UNAVAILABLE",
		},
		{
			name:     "the authorized choice planner is not configured",
			snapshot: filterSourceSnapshot("filter-source-precise-planner"),
			service:  rowSourceServiceWithPlanner(nil), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "ROW_CHOICE_UNAVAILABLE",
		},
		{
			name:     "the planner enumerates no fields at all",
			snapshot: filterSourceSnapshot("filter-source-precise-none"),
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_FILTER_SOURCE_FIELDS",
		},
		{
			name: "distinct skipped classes stay on the generic fallback",
			snapshot: candidatesSnapshot("filter-source-precise-mixed",
				bs("bs-id", "id", "string", "optional_one", false),
				bs("bs-active", filterSourceField, "quantity", "optional_one", true)),
			service: filterSourceService(t), construction: empty,
			stageID:  recipe.ConstructionSourceProjectionID,
			wantCode: "NO_ELIGIBLE_POPULATED_FILTER_SOURCE_FIELDS",
		},
	}
	genericCode := "NO_ELIGIBLE_POPULATED_FILTER_SOURCE_FIELDS"
	distinct := map[string]string{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			base := filterSourceBase(row.snapshot, row.construction)
			base.stages = append(base.stages, row.stages...)
			result, err := row.service.constructionFilterSourceCapability(context.Background(), base, filterSourceOutput, row.stageID)
			if err != nil {
				t.Fatalf("filter source capability: %v", err)
			}
			if result.Supported || len(result.Choices) != 0 {
				t.Fatalf("capability = %#v, want unsupported with no advertised choices", result)
			}
			if result.ReasonCode != row.wantCode {
				t.Fatalf("reasonCode = %q (reason %q), want %q", result.ReasonCode, result.Reason, row.wantCode)
			}
			if strings.TrimSpace(result.Reason) == "" {
				t.Fatalf("reason is blank for %s", row.wantCode)
			}
			if previous, seen := distinct[result.ReasonCode]; seen && previous != row.name && result.ReasonCode != genericCode {
				t.Fatalf("rows %q and %q share reasonCode %q", previous, row.name, result.ReasonCode)
			}
			if _, seen := distinct[result.ReasonCode]; !seen {
				distinct[result.ReasonCode] = row.name
			}
			if result.StageID != row.stageID {
				t.Fatalf("stageId = %q, want %q", result.StageID, row.stageID)
			}
		})
	}
	if len(distinct) != 7 {
		t.Fatalf("expected seven distinct lane reason codes, got %#v", distinct)
	}
}

// --- ROW-SOURCE-03 S-B: inventory-error abort verdicts ----------------------

// failingRowSourcePlanner aborts enumeration the way an unreachable row-choice
// inventory does; it is the injection point for the S-B verdict that must stay
// distinguishable from the healthy empty-data codes.
type failingRowSourcePlanner struct{ err error }

func (p failingRowSourcePlanner) ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error) {
	return nil, p.err
}

func TestConstructionFilterSourceCapabilityNamesInventoryFailureDistinctly(t *testing.T) {
	const internalDetail = "capability-store-dsn=secret-token"
	inventoryErr := errors.New("row-choice inventory store unreachable: " + internalDetail)
	empty := authoringv2.Construction{Version: authoringv2.ConstructionVersion}
	observedSnapshot := filterSourceSnapshot("filter-source-inventory-observed")
	observedSnapshot.Candidates = []capability.Candidate{
		rowSourceLaneCandidate(filterSourceRootNode, filterSourceRootType, "bs-id", "id", "string", "optional_one", false),
		rowSourceLaneCandidate(filterSourceRootNode, filterSourceRootType, "bs-active", filterSourceField, "boolean", "optional_one", false),
	}
	rows := []struct {
		name     string
		service  *Service
		snapshot capability.Snapshot
		wantCode string
		wantFail bool
	}{
		{name: "planner enumeration failure names the inventory class",
			service:  rowSourceServiceWithPlanner(failingRowSourcePlanner{err: inventoryErr}),
			snapshot: filterSourceSnapshot("filter-source-inventory-failure"),
			wantCode: "FILTER_SOURCE_INVENTORY_UNAVAILABLE", wantFail: true},
		{name: "a planner that enumerates nothing stays generic",
			service:  rowSourceServiceWithPlanner(emptyRowSourceChoicePlanner{}),
			snapshot: filterSourceSnapshot("filter-source-inventory-empty"),
			wantCode: "NO_ELIGIBLE_POPULATED_FILTER_SOURCE_FIELDS"},
		{name: "truly empty data names the observed class, not the inventory class",
			service: filterSourceService(t), snapshot: observedSnapshot,
			wantCode: "NO_OBSERVED_VALUES_FILTER_SOURCE_FIELDS"},
	}
	distinct := map[string]bool{}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			result, err := row.service.constructionFilterSourceCapability(context.Background(),
				filterSourceBase(row.snapshot, empty), filterSourceOutput, recipe.ConstructionSourceProjectionID)
			if row.wantFail {
				if err == nil {
					t.Fatal("a failed enumeration was swallowed: the capability must still return the wrapped error")
				}
				if !errors.Is(err, inventoryErr) {
					t.Fatalf("the planner cause was not wrapped through the typed error: %v", err)
				}
				if code := lifecycleErrorCode(err); code != row.wantCode {
					t.Fatalf("typed error code = %q, want %q", code, row.wantCode)
				}
				if strings.Contains(result.Reason, internalDetail) || strings.Contains(result.ReasonCode, internalDetail) {
					t.Fatalf("planner error detail %q crossed into advertised payload: %#v", internalDetail, result)
				}
			} else if err != nil {
				t.Fatalf("soft empty verdict must not surface as an error: %v", err)
			}
			if result.Supported || len(result.Choices) != 0 {
				t.Fatalf("capability = %#v, want fail-closed unsupported with no advertised choices", result)
			}
			if result.ReasonCode != row.wantCode || strings.TrimSpace(result.Reason) == "" {
				t.Fatalf("reasonCode = %q (reason %q), want %q with a non-blank reason", result.ReasonCode, result.Reason, row.wantCode)
			}
			if result.StageID != recipe.ConstructionSourceProjectionID {
				t.Fatalf("stageId = %q, want the requested stage even inside the failure class", result.StageID)
			}
			distinct[result.ReasonCode] = true
		})
	}
	if len(distinct) != 3 {
		t.Fatalf("inventory-error and empty-data verdicts collapsed: %#v", distinct)
	}
}
