package construction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/rowsource"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/semantic"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
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
	return constructionfixtures.SemanticFrameCompatibilityObservation(kind)
}

func semanticFrameCompatibilityFamily(t *testing.T, entry semantic.Entry) capability.SemanticFrameFamily {
	return constructionfixtures.SemanticFrameCompatibilityFamily(t, entry)
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
	return constructionfixtures.FilterSourceSnapshot(token, filterSourceProject, filterSourceRootNode, filterSourceRootType, filterSourceIdentity, filterSourceField)
}

func filterSourceDocument() ws.Document {
	return constructionfixtures.FilterSourceDocument(filterSourceOutput, "Body structures", filterSourceRootType, filterSourceIdentity, "body_structure_id", filterSourceField)
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
	return &Service{config: Config{
		RowChoicePlanner: resolver, RowChoiceResolver: resolver,
		SourceLanes: rowsource.NewService(rowsource.Config{RowChoicePlanner: resolver, RowChoiceResolver: resolver}),
	}}
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
	return constructionfixtures.FilterSourceGroupStep(filterSourceIdentity)
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

func filterSourceIdentityColumns() []ws.StageColumn {
	return []ws.StageColumn{{ID: filterSourceIdentity, Name: "body_structure_id", Label: "Body structure ID", Type: "string"}}
}

func filterSourceProjectionColumn() ws.StageColumn {
	return ws.StageColumn{
		ID: filterSourceColumn, Name: ws.ConstructionSourceProjectionName(filterSourceColumn),
		Label: "BodyStructure.active", Type: "boolean",
	}
}

func filterSourceBool(value bool) *bool { return &value }

func filterSourceRowChoice(t *testing.T, snapshot capability.Snapshot, path string) capability.RowChoice {
	return constructionfixtures.FilterSourceRowChoice(t, snapshot, filterSourceDocument(), path)
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

func lifecycleConstructionReceipt(snapshot capability.Snapshot, workspace ws.Workspace) *receiptmodel.CompilationReceipt {
	return constructionfixtures.LifecycleConstructionReceipt(snapshot, workspace)
}
