package construction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
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
	plan := authoringv2.ResolveSemanticSelectionPlan(entry.Observation)
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

func filterSourceBase(snapshot capability.Snapshot, construction authoringv2.Construction) constructionBase {
	return constructionBase{
		Document:     filterSourceDocument(),
		Construction: construction,
		Snapshot:     snapshot,
		Authorized:   ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
		Stages: []explorer.ReceiptConstructionStage{{
			ID: recipe.ConstructionSourceProjectionID, RowIdentityColumn: "_key",
			Columns: []explorer.ReceiptConstructionStageColumn{
				{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"},
			},
		}},
	}
}

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

type staleRowSourceChoicePlanner struct {
	inner  RowChoicePlanner
	issued capability.Snapshot
}

func (p staleRowSourceChoicePlanner) ListRowChoices(ctx context.Context, snapshot capability.Snapshot, document authoringv2.Document) ([]capability.RowChoice, error) {
	return p.inner.ListRowChoices(ctx, p.issued, document)
}

type emptyRowSourceChoicePlanner struct{}

func (emptyRowSourceChoicePlanner) ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error) {
	return nil, nil
}

type failingRowSourcePlanner struct{ err error }

func (p failingRowSourcePlanner) ListRowChoices(context.Context, capability.Snapshot, authoringv2.Document) ([]capability.RowChoice, error) {
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

func lifecycleTestStringScalar(value string) authoringv2.TableScalar {
	return authoringv2.TableScalar{Kind: authoringv2.TableScalarString, String: &value}
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
