package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/population"

	explorerselection "github.com/calypr/loom/internal/explorer/selection"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	explorerfixtures "github.com/calypr/loom/internal/testkit/explorerfixtures"
)

type attachmentCase struct {
	ID               string                `json:"id"`
	Edge             string                `json:"edge"`
	SnapshotMatches  *bool                 `json:"snapshotMatches"`
	OutputMatches    *bool                 `json:"outputMatches"`
	SelectionMatches *bool                 `json:"selectionMatches"`
	Selection        string                `json:"selection"`
	Route            string                `json:"route"`
	Batch            string                `json:"batch"`
	Expected         attachmentExpectation `json:"expected"`
}

type attachmentExpectation struct {
	Accepted            bool                     `json:"accepted"`
	SearchChoices       int                      `json:"searchChoices"`
	SearchPreservedHead bool                     `json:"searchPreservedHead"`
	SaveAttempts        int                      `json:"saveAttempts"`
	Writes              int                      `json:"writes"`
	PreservedHead       bool                     `json:"preservedHead"`
	ExactWorkspace      bool                     `json:"exactWorkspace"`
	StableHead          bool                     `json:"stableHead"`
	VersionBefore       int64                    `json:"versionBefore"`
	VersionAfter        int64                    `json:"versionAfter"`
	SelectionID         string                   `json:"selectionId"`
	Route               []ws.PopulationRouteStep `json:"route"`
}
type attachmentResult struct {
	ID                  string                   `json:"id"`
	Accepted            bool                     `json:"accepted"`
	SearchChoices       int                      `json:"searchChoices"`
	SearchPreservedHead bool                     `json:"searchPreservedHead"`
	SaveAttempts        int                      `json:"saveAttempts"`
	Writes              int                      `json:"writes"`
	PreservedHead       bool                     `json:"preservedHead"`
	ExactWorkspace      bool                     `json:"exactWorkspace"`
	StableHead          bool                     `json:"stableHead"`
	VersionBefore       int64                    `json:"versionBefore"`
	VersionAfter        int64                    `json:"versionAfter"`
	SelectionID         string                   `json:"selectionId"`
	Route               []ws.PopulationRouteStep `json:"route"`
	Error               string                   `json:"error"`
}
type attachmentStore struct {
	*fakeStore
	selections       map[string]explorerselection.SelectionRevision
	attempts, writes int
}

func (s *attachmentStore) GetSelection(_ context.Context, _, id string) (*explorerselection.SelectionRevision, error) {
	value, ok := s.selections[id]
	if !ok {
		return nil, explorerselection.ErrSelectionNotFound
	}
	return &value, nil
}
func (s *attachmentStore) SaveDraft(ctx context.Context, value explorer.Explorer, version int64, digests ...string) (*explorer.Explorer, error) {
	s.attempts++
	if version != s.created.DraftVersion || len(digests) != 1 || digests[0] != s.created.DraftDigest {
		return nil, explorer.ErrDraftConflict
	}
	result, err := s.fakeStore.SaveDraft(ctx, value, version, digests...)
	if err == nil {
		s.writes++
	}
	return result, err
}
func attachmentHead(t *testing.T, value *explorer.Explorer) explorer.Explorer {
	t.Helper()
	result := *value
	result.DraftConfig = append([]byte(nil), value.DraftConfig...)
	result.LastAuthoringCommandResults = append([]byte(nil), value.LastAuthoringCommandResults...)
	return result
}
func attachmentObserve(t *testing.T, c attachmentCase) attachmentResult {
	t.Helper()
	if c.ID == "" || c.SnapshotMatches == nil || c.OutputMatches == nil || c.SelectionMatches == nil {
		t.Fatal("missing attachment input")
	}
	if c.Edge != "primary" && c.Edge != "parallel" && c.Edge != "zero" {
		t.Fatal("unknown attachment edge")
	}
	if c.Route != "authorized" && c.Route != "wrongDirection" && c.Route != "blockedEdge" {
		t.Fatal("unknown attachment route")
	}
	if c.Batch != "single" && c.Batch != "invalidSecond" {
		t.Fatal("unknown attachment batch")
	}
	selectionIDs := map[string]string{"current": "selection-current", "incomplete": "selection-incomplete", "foreignProject": "selection-foreign-project", "staleGeneration": "selection-stale-generation", "staleScope": "selection-stale-scope"}
	selectionID, ok := selectionIDs[c.Selection]
	if c.Selection == "revokedScope" {
		selectionID = "selection-current"
		ok = true
	}
	if !ok {
		t.Fatal("unknown attachment selection")
	}
	setup, _ := inboundPatientObservationRouteSetup(t)
	base, snapshot, catalog := setup.Store, setup.Snapshot, setup.Catalog
	snapshot, catalog = addParallelObservationRoute(setup.Config, snapshot, catalog)
	snapshot = capability.NewSnapshot(snapshot.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
	blocked := snapshot.Edges[0]
	blocked.ID = "subject-patient-blocked"
	blocked.BlockedReason = "not granted in this snapshot"
	snapshot.AuditEdges = []capability.Edge{blocked}
	catalog.SnapshotToken = snapshot.Token
	for index := range catalog.Candidates {
		for _, candidate := range snapshot.Candidates {
			if candidate.ID == catalog.Candidates[index].ID {
				choice, e := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
				if e != nil {
					t.Fatal(e)
				}
				catalog.Candidates[index].ConstructionChoice = &choice
			}
		}
	}

	initial, setupErr := ws.DecodeWorkspace(base.created.DraftConfig)
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	initial.SemanticsVersion = ws.CurrentSemanticsVersion
	initial.Documents[0].Rows = ws.RecordsRowDefinition()
	initial.Documents[0].Columns[0].ColumnID = "patient-id"
	initial.Explorer.Title = base.created.Title
	base.created.DraftConfig, setupErr = initial.CanonicalJSON()
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	base.created.DraftDigest, setupErr = initial.Digest()
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	base.copyGet = true
	store := &attachmentStore{fakeStore: base, selections: map[string]explorerselection.SelectionRevision{}}
	resourceType := "Observation"
	if c.Edge == "zero" {
		resourceType = "Patient"
	}
	for kind, id := range selectionIDs {
		value := *completedTestSelection(snapshot, resourceType)
		value.ID = id
		switch kind {
		case "incomplete":
			value.Complete = false
			value.CompletedAt = nil
		case "foreignProject":
			value.Project = "foreign-project"
		case "staleGeneration":
			value.Generation = "foreign-generation"
		case "staleScope":
			value.ScopeDigest = "foreign-scope"
		}
		store.selections[id] = value
	}
	decoy := store.selections["selection-current"]
	decoy.ID = "selection-decoy"
	store.selections[decoy.ID] = decoy
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := *setup.Config
	config.Capability.ForCompilation = func(context.Context, string, string) (ports.AuthorizedCapability, error) {
		return ports.AuthorizedCapability{Snapshot: snapshot, Scope: scope}, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot { return catalog }
	domain, err := explorerfixtures.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, config)
	if err != nil {
		t.Fatal(err)
	}
	before := attachmentHead(t, store.created)
	choices, err := service.SearchPopulationRoutes(context.Background(), population.PopulationRoutesRequest{Project: "project-a", ExplorerID: "patients", SnapshotToken: snapshot.Token, OutputID: "patients", SelectionRevisionID: "selection-current"})
	if err != nil {
		t.Fatalf("valid attachment search failed: %v", err)
	}
	wantSearch := 2
	if c.Edge == "zero" {
		wantSearch = 1
	}
	if len(choices.Choices) != wantSearch || !choices.Complete {
		t.Fatalf("attachment search choices=%d complete=%v", len(choices.Choices), choices.Complete)
	}
	searchPreserved := reflect.DeepEqual(before, attachmentHead(t, store.created)) && store.attempts == 0
	edgeID := "subject-patient"
	if c.Edge == "parallel" {
		edgeID = "subject-patient-parallel"
	}
	var identity capability.PopulationRouteChoiceIdentity
	found := false
	for _, choice := range choices.Choices {
		decoded, e := capability.DecodePopulationRouteChoiceID(choice.RouteChoiceID)
		if e != nil {
			t.Fatal(e)
		}
		if c.Edge == "zero" && len(decoded.Route) == 0 || c.Edge != "zero" && len(decoded.Route) == 1 && decoded.Route[0].EdgeID == edgeID {
			identity = decoded
			found = true
			break
		}
	}
	if !found {
		t.Fatal("search omitted the literal requested edge")
	}
	if c.Selection == "revokedScope" {
		scope = authscope.ReadScope{Mode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"current-authorized-path"}}
		narrow := readySnapshot("project-a", "generation-a", "narrow-attachment", scope)
		snapshot = capability.NewSnapshot(narrow.Identity, snapshot.Policy, snapshot.Status, snapshot.Complete, snapshot.Truncated, snapshot.Nodes, snapshot.Edges, snapshot.Candidates, snapshot.Diagnostics)
		catalog.SnapshotToken = snapshot.Token
		catalog.AuthorizationScopeDigest = snapshot.Identity.AuthorizationScopeDigest
		for index := range catalog.Candidates {
			for _, candidate := range snapshot.Candidates {
				if candidate.ID == catalog.Candidates[index].ID {
					choice, e := capability.NewFieldConstructionChoice(snapshot.Token, candidate)
					if e != nil {
						t.Fatal(e)
					}
					catalog.Candidates[index].ConstructionChoice = &choice
				}
			}
		}
		identity.SnapshotToken = snapshot.Token
	}
	identity.SelectionRevisionID = selectionID
	if !*c.SnapshotMatches {
		identity.SnapshotToken = "foreign-snapshot"
	}
	if !*c.OutputMatches {
		identity.OutputID = "foreign-output"
	}
	if !*c.SelectionMatches {
		identity.SelectionRevisionID = "selection-decoy"
	}
	if c.Route == "wrongDirection" {
		if len(identity.Route) != 1 {
			t.Fatal("direction requires one hop")
		}
		identity.Route[0].StorageDirection = "OUTBOUND"
	}
	if c.Route == "blockedEdge" {
		if len(identity.Route) != 1 {
			t.Fatal("blocked edge requires one hop")
		}
		identity.Route[0].EdgeID = "subject-patient-blocked"
	}
	choiceID, err := capability.NewPopulationRouteChoiceID(identity)
	if err != nil {
		t.Fatalf("forged identity must have a valid digest: %v", err)
	}
	commands := []authoringv2.Command{{Type: authoringv2.CommandSetTablePopulation, OutputID: "patients", Payload: &authoringv2.TablePayload{SelectionRevisionID: selectionID, RouteChoiceID: choiceID}}}
	if c.Batch == "invalidSecond" {
		second := identity
		second.SnapshotToken = snapshot.Token
		second.OutputID = "missing-output"
		second.SelectionRevisionID = selectionID
		token, e := capability.NewPopulationRouteChoiceID(second)
		if e != nil {
			t.Fatal(e)
		}
		commands = append(commands, authoringv2.Command{Type: authoringv2.CommandSetTablePopulation, OutputID: "missing-output", Payload: &authoringv2.TablePayload{SelectionRevisionID: selectionID, RouteChoiceID: token}})
	}
	expected, err := ws.DecodeWorkspace(before.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	literalRoute := []ws.PopulationRouteStep{}
	if c.Edge != "zero" {
		literalRoute = append(literalRoute, ws.PopulationRouteStep{ResourceType: "Observation", Relationship: "subject_Patient", CatalogEdgeID: edgeID, StorageDirection: "INBOUND"})
	}
	expected.Documents[0].Population = &ws.Population{SelectionRevisionID: selectionID, Route: literalRoute}
	expectedBytes, err := expected.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	_, applyErr := service.ApplyCommands(context.Background(), "project-a", "patients", authoringv2.ApplyCommandsRequest{CommandID: "attachment-" + c.ID, SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: snapshot.Token, ExpectedDraftVersion: before.DraftVersion, ExpectedDraftDigest: before.DraftDigest, Commands: commands}, "alice")
	reloaded, err := domain.Get(context.Background(), "project-a", "patients")
	if err != nil {
		t.Fatal(err)
	}
	after := attachmentHead(t, reloaded)
	result := attachmentResult{ID: c.ID, Accepted: applyErr == nil, SearchChoices: len(choices.Choices), SearchPreservedHead: searchPreserved, SaveAttempts: store.attempts, Writes: store.writes, PreservedHead: reflect.DeepEqual(before, after), ExactWorkspace: bytes.Equal(after.DraftConfig, expectedBytes), StableHead: before.Project == after.Project && before.ExplorerID == after.ExplorerID && before.Title == after.Title && before.ManagementMode == after.ManagementMode && before.ActiveRevisionID == after.ActiveRevisionID, VersionBefore: before.DraftVersion, VersionAfter: after.DraftVersion, Route: []ws.PopulationRouteStep{}}
	if applyErr != nil {
		result.Error = applyErr.Error()
	}
	workspace, err := ws.DecodeWorkspace(after.DraftConfig)
	if err != nil {
		t.Fatal(err)
	}
	if pop := workspace.Documents[0].Population; pop != nil {
		result.SelectionID = pop.SelectionRevisionID
		result.Route = append(result.Route, pop.Route...)
	}
	return result
}

func TestPopulationAttachmentGeneratedReplay(t *testing.T) {
	const scheduleFile = "testdata/population_attachment_cases.json"
	data, err := os.ReadFile(scheduleFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4<<20 {
		t.Fatalf("schedule file %s exceeds 4 MiB", scheduleFile)
	}
	var request struct {
		SchemaVersion int              `json:"schemaVersion"`
		Cases         []attachmentCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err = decoder.Decode(&trailing); err != io.EOF {
		t.Fatal("trailing attachment data")
	}
	if request.SchemaVersion != 1 || len(request.Cases) == 0 || len(request.Cases) > 200 {
		t.Fatal("invalid frozen attachment schedules")
	}
	seen := map[string]bool{}
	for _, c := range request.Cases {
		if c.ID == "" || seen[c.ID] {
			t.Fatalf("duplicate or missing attachment case ID: %q", c.ID)
		}
		seen[c.ID] = true
		got := attachmentObserve(t, c)
		actual := attachmentExpectation{Accepted: got.Accepted, SearchChoices: got.SearchChoices, SearchPreservedHead: got.SearchPreservedHead, SaveAttempts: got.SaveAttempts, Writes: got.Writes, PreservedHead: got.PreservedHead, ExactWorkspace: got.ExactWorkspace, StableHead: got.StableHead, VersionBefore: got.VersionBefore, VersionAfter: got.VersionAfter, SelectionID: got.SelectionID, Route: got.Route}
		if !reflect.DeepEqual(actual, c.Expected) {
			t.Errorf("schedule %s: got %+v, want frozen behavior %+v", c.ID, actual, c.Expected)
		}
		if !got.Accepted && c.Selection == "revokedScope" && !strings.Contains(got.Error, "STALE_POPULATION_SELECTION") {
			t.Errorf("schedule %s: revocation failure did not reach selection scope reauthorization: %q", c.ID, got.Error)
		}
		if !got.Accepted && c.SnapshotMatches != nil && *c.SnapshotMatches && c.OutputMatches != nil && *c.OutputMatches && c.SelectionMatches != nil && *c.SelectionMatches && c.Selection == "current" && c.Route == "authorized" && c.Batch == "invalidSecond" && !strings.Contains(got.Error, "outputId does not identify a valid row-rooted table") {
			t.Errorf("schedule %s: invalid suffix failure did not reach late preparation: %q", c.ID, got.Error)
		}
	}
}
