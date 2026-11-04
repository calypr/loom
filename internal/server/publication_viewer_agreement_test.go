package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

// The repository and materializer are explicit test boundaries. Production
// compiler, lifecycle policy, strict HTTP routes and Viewer projection execute;
// this is not a database-restart, real materialization, or browser-row proof.
type agreementPublicationStore struct {
	*testExplorerStore
	publishCalls    int
	receiptID       string
	release         dataset.ProjectRelease
	releaseRevision int64
}

func (s *agreementPublicationStore) PublishAuthoring(_ context.Context, receipt explorer.CompilationReceipt, revision explorer.Revision, release dataset.ProjectRelease, expected int64) (*explorer.Revision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := testExplorerKey(revision.Project, revision.ExplorerID)
	owner, found := s.explorers[key]
	if !found {
		return nil, explorer.ErrNotFound
	}
	s.publishCalls++
	s.receiptID, s.release, s.releaseRevision = receipt.ID, release, expected
	revision.Status = explorer.RevisionActive
	revision.Publication.State = "ACTIVE"
	revision.Publication.RevisionID = revision.ID
	s.revisions[revision.ID] = revision
	owner.ActiveRevisionID = revision.ID
	s.explorers[key] = owner
	return &revision, nil
}

func TestPublicationHTTPReceiptRevisionAndViewerAgreeAfterFreshRead(t *testing.T) {
	app, store, service, receipt, materializeCalls, invalidQuality := publicationAgreementFixture(t)
	path := "/api/v1/projects/project-a/explorers/publication-agreement"
	before := requestJSON(t, app, http.MethodGet, path, "")
	if before.StatusCode != http.StatusOK {
		t.Fatalf("initial Viewer status=%d body=%s", before.StatusCode, before.Body)
	}
	var beforeState explorer.ExplorerStateV1
	if err := json.Unmarshal([]byte(before.Body), &beforeState); err != nil {
		t.Fatal(err)
	}
	if beforeState.Active.RevisionID != "" || beforeState.Runtime != nil {
		t.Fatalf("unpublished Viewer exposed executable state: %#v", beforeState)
	}

	published := requestJSON(t, app, http.MethodPost, path+"/authoring/v2/publish", fmt.Sprintf(`{"receiptId":%q}`, receipt.ID))
	if published.StatusCode != http.StatusOK {
		t.Fatalf("publish status=%d body=%s", published.StatusCode, published.Body)
	}
	var result struct {
		ReceiptID  string `json:"receiptId"`
		RevisionID string `json:"revisionId"`
		State      string `json:"state"`
		Outputs    []struct {
			OutputID          string `json:"outputId"`
			MaterializationID string `json:"materializationId"`
			State             string `json:"state"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal([]byte(published.Body), &result); err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID != receipt.ID || result.RevisionID == "" || result.State != "ACTIVE" || len(result.Outputs) != 1 || result.Outputs[0].OutputID != "patients" || result.Outputs[0].MaterializationID != "execution-agreement" || result.Outputs[0].State != "READY" {
		t.Fatalf("publication response lost reviewed receipt/output identity: %#v", result)
	}
	if *materializeCalls != 1 || store.publishCalls != 1 || store.receiptID != receipt.ID || store.release.ID != "release-agreement" || store.releaseRevision != 7 {
		t.Fatalf("publication crossed the wrong activation boundary: materializations=%d repository=%#v", *materializeCalls, store)
	}
	persisted, err := service.ActiveRevision(context.Background(), "project-a", "publication-agreement")
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ID != result.RevisionID || persisted.CompilationReceiptID != receipt.ID || persisted.IntentDigest != receipt.IntentDigest || persisted.RecipeDigest != receipt.RecipeDigest || persisted.ResolvedSchemaDigest != receipt.ResolvedSchemaDigest || !reflect.DeepEqual(persisted.PublicOutputContract, receipt.PublicOutputContract) {
		t.Fatalf("persisted publication identity diverged from receipt: revision=%#v receipt=%#v", persisted, receipt)
	}
	// Deliberately change the live draft after publication. A new GET must
	// continue projecting the immutable published receipt, not this draft.
	unpublished, err := authoringv2.DecodeWorkspace(receipt.NormalizedBundle)
	if err != nil {
		t.Fatal(err)
	}
	unpublished.Explorer.Title = "Unpublished edit"
	unpublished.Documents[0].Columns[0].Label = "Unpublished patient label"
	unpublishedRaw, err := unpublished.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	unpublishedDigest, err := unpublished.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	owner := store.explorers[testExplorerKey("project-a", "publication-agreement")]
	owner.DraftConfig = unpublishedRaw
	owner.DraftDigest = unpublishedDigest
	owner.DraftVersion++
	store.explorers[testExplorerKey("project-a", "publication-agreement")] = owner
	store.mu.Unlock()
	assertFreshViewer := func() {
		t.Helper()
		response := requestJSON(t, app, http.MethodGet, path, "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("fresh Viewer status=%d body=%s", response.StatusCode, response.Body)
		}
		var state explorer.ExplorerStateV1
		if err := json.Unmarshal([]byte(response.Body), &state); err != nil {
			t.Fatal(err)
		}
		if state.Active.RevisionID != result.RevisionID || state.Generated.Publication.RevisionID != result.RevisionID || state.Generated.RecipeDigest != receipt.RecipeDigest || state.Generated.SourceGeneration != receipt.SourceGeneration || state.Runtime == nil || state.Runtime.Status != "ACTIVE" || len(state.Runtime.Outputs) != 1 {
			t.Fatalf("fresh Viewer lost immutable publication: %#v", state)
		}
		output := state.Runtime.Outputs[0]
		wantSelector := dataset.DataframeSelector{Recipe: receipt.Bundle.Name, TranslationVersion: receipt.Bundle.TranslationVersion, Output: "patients"}
		if output.OutputID != "patients" || output.Selector != wantSelector || output.Materialization == nil || output.Materialization.MaterializationID != "execution-agreement" {
			t.Fatalf("Viewer selected a different published output: %#v", output)
		}
		var got []string
		for _, column := range output.Columns {
			got = append(got, column.Column+":"+column.Label+":"+column.LogicalType)
		}
		if !reflect.DeepEqual(got, []string{"c_patient:Patient ID:string", "patient_count:Patient count:integer"}) {
			t.Fatalf("Viewer public columns/types = %#v", got)
		}
		loadedWorkspace, err := authoringv2.DecodeWorkspace(state.Active.Bundle)
		if err != nil {
			t.Fatal(err)
		}
		loadedCanonical, err := loadedWorkspace.CanonicalJSON()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(loadedCanonical, receipt.NormalizedBundle) {
			t.Fatalf("Viewer used a different workspace: got=%s want=%s", loadedCanonical, receipt.NormalizedBundle)
		}
	}
	assertFreshViewer()
	for _, foreign := range []string{
		"/api/v1/projects/project-b/explorers/publication-agreement",
		"/api/v1/projects/project-a/explorers/other",
	} {
		response := requestJSON(t, app, http.MethodPost, foreign+"/authoring/v2/publish", fmt.Sprintf(`{"receiptId":%q}`, receipt.ID))
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("foreign receipt publish %s status=%d body=%s", foreign, response.StatusCode, response.Body)
		}
	}
	if *materializeCalls != 1 {
		t.Fatalf("foreign receipt reached materialization: calls=%d", *materializeCalls)
	}
	*invalidQuality = true
	rejected := requestJSON(t, app, http.MethodPost, path+"/authoring/v2/publish", fmt.Sprintf(`{"receiptId":%q}`, receipt.ID))
	if rejected.StatusCode != http.StatusServiceUnavailable || !strings.Contains(rejected.Body, `"code":"QUALITY_EVIDENCE_INVALID"`) {
		t.Fatalf("invalid quality publish status=%d body=%s", rejected.StatusCode, rejected.Body)
	}
	if store.publishCalls != 1 {
		t.Fatalf("rejected publish changed active revision: repository calls=%d", store.publishCalls)
	}
	assertFreshViewer()
}

func publicationAgreementFixture(t *testing.T) (*fiber.App, *agreementPublicationStore, *explorer.Service, *explorer.CompilationReceipt, *int, *bool) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	visible, order := true, 1
	workspace.Documents[0].Columns = append(workspace.Documents[0].Columns, authoringv2.Column{
		Column: "patient_count", Label: "Patient count", LogicalType: "integer", OccurrenceID: "base",
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceAggregate, Aggregate: &authoringv2.AggregateSource{Operation: "COUNT"}},
		Table:  &authoringv2.TablePresentation{Visible: &visible, Order: &order},
	})
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := &agreementPublicationStore{testExplorerStore: newTestExplorerStore()}
	_, err = store.create(explorer.Explorer{Project: "project-a", ExplorerID: "publication-agreement", Title: "Publication agreement", ManagementMode: explorer.ManagementInteractive, DraftConfig: raw, DraftDigest: digest, DraftVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{Registry: compilerTestRegistry{}, QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := compileExplorerReceipt(context.Background(), lifecycle.CompileReceiptRequest{
		Project: "project-a", ExplorerID: "publication-agreement", Workspace: workspace, SnapshotToken: snapshot.Token,
		Authorized: lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}},
	}, nil, engine, service, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	calls, badQuality := 0, false
	config := lifecycle.Config{
		Capability:                lifecycle.CapabilityResolver{Token: func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil }},
		ValidateReleaseGeneration: func(context.Context, string, string) error { return nil },
		PrepareRelease: func(_ context.Context, project, generation string, selectors []dataset.DataframeSelector) (dataset.ProjectRelease, int64, error) {
			want := []dataset.DataframeSelector{{Recipe: receipt.Bundle.Name, TranslationVersion: receipt.Bundle.TranslationVersion, Output: "patients"}}
			if project != "project-a" || generation != snapshot.Identity.Generation || !reflect.DeepEqual(selectors, want) {
				t.Fatalf("release selects wrong publication: %q/%q %#v", project, generation, selectors)
			}
			return dataset.ProjectRelease{ID: "release-agreement", Project: project, Generation: generation}, 7, nil
		},
		MaterializeReceipt: func(_ context.Context, actual *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) (lifecycle.Execution, error) {
			calls++
			if actual.ID != receipt.ID || bindings.Project != "project-a" || bindings.DatasetGeneration != snapshot.Identity.Generation {
				t.Fatalf("materializer received foreign receipt or generation: %#v %#v", actual, bindings)
			}
			quality := publication.QualityReport{ID: "quality-patients", ReceiptID: receipt.ID, Project: "project-a", DatasetGeneration: snapshot.Identity.Generation, ScopeDigest: "scope-publication", Output: "patients", PolicyVersion: publication.DefaultQualityPolicyVersion, Completeness: publication.QualityComplete, Verdict: publication.QualityPassed}
			if badQuality {
				quality.ReceiptID = "foreign-receipt"
			}
			return lifecycle.Execution{ID: "execution-agreement", Project: "project-a", SourceGeneration: snapshot.Identity.Generation, ScopeDigest: "scope-publication",
				Outputs: []lifecycle.ExecutionOutput{{Name: "patients", State: "READY", Columns: []publication.PhysicalColumn{
					{Name: "c_patient", LogicalType: "string", ClickHouse: "String"},
					{Name: "patient_count", LogicalType: "integer", ClickHouse: "Int64"},
				}}}, QualityReports: []publication.QualityReport{quality}}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	return app, store, service, receipt, &calls, &badQuality
}
