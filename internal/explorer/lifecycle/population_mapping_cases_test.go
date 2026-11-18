package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"sort"
	"testing"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/projectid"
)

type formalPopulationCase struct {
	ID                      string                      `json:"id"`
	AuthorizedScopeMatches  *bool                       `json:"authorizedScopeMatches"`
	SelectionComplete       *bool                       `json:"selectionComplete"`
	SelectionBindingMatches *bool                       `json:"selectionBindingMatches"`
	MemberReadCompletes     *bool                       `json:"memberReadCompletes"`
	MappingCompletes        *bool                       `json:"mappingCompletes"`
	Alias                   *bool                       `json:"alias"`
	Deadline                *bool                       `json:"deadline"`
	Expected                formalPopulationExpectation `json:"expected"`
}

type formalPopulationExpectation struct {
	Outcome        string  `json:"outcome"`
	MemberReads    int     `json:"memberReads"`
	MappingCalls   int     `json:"mappingCalls"`
	Counts         []int64 `json:"counts"`
	BindingCorrect bool    `json:"bindingCorrect"`
}

type formalPopulationResult struct {
	ID             string  `json:"id"`
	Outcome        string  `json:"outcome"`
	MemberReads    int     `json:"memberReads"`
	MappingCalls   int     `json:"mappingCalls"`
	Counts         []int64 `json:"counts"`
	BindingCorrect bool    `json:"bindingCorrect"`
	Error          string  `json:"error"`
}

type formalPopulationStore struct {
	*fakeStore
	completes bool
	deadline  bool
	visits    int
}

func (s *formalPopulationStore) VisitSelectionMembers(ctx context.Context, project, id, after string, limit int, visit func(explorer.SelectionMember) error) (string, error) {
	s.visits++
	if !s.completes {
		if s.deadline {
			return "", context.DeadlineExceeded
		}
		return "", context.Canceled
	}
	return s.fakeStore.VisitSelectionMembers(ctx, project, id, after, limit, visit)
}

func formalPopulationObserve(t *testing.T, value formalPopulationCase) formalPopulationResult {
	t.Helper()
	for _, field := range []*bool{value.AuthorizedScopeMatches, value.SelectionComplete, value.SelectionBindingMatches, value.MemberReadCompletes, value.MappingCompletes, value.Alias, value.Deadline} {
		if field == nil {
			t.Fatal("missing population choice")
		}
	}
	if value.ID == "" {
		t.Fatal("missing population case ID")
	}
	project := "org/project"
	requestProject := project
	if *value.Alias {
		requestProject = "org%2Fproject"
	}
	snapshot := readySnapshot(project, "generation-a", "token", unrestrictedScope())
	receipt := populationTestReceipt(snapshot)
	selection := completedTestSelection(snapshot, "Specimen")
	selection.MemberCount = 3
	selection.Complete = *value.SelectionComplete
	if !*value.SelectionBindingMatches {
		selection.MembershipDigest = "foreign-membership-digest"
	}
	base := &fakeStore{receipt: receipt, selection: selection}
	for _, id := range []string{"file-001", "file-002", "file-004"} {
		base.selectionMembers = append(base.selectionMembers, explorer.SelectionMember{Ref: explorer.ResourceRef{Project: project, Generation: selection.Generation, ResourceType: "Specimen", ID: id}})
	}
	store := &formalPopulationStore{fakeStore: base, completes: *value.MemberReadCompletes, deadline: *value.Deadline}
	authorized := snapshot
	if !*value.AuthorizedScopeMatches {
		authorized.Identity.AuthorizationScopeDigest = "foreign-scope"
	}
	config := testConfig(authorized)
	calls := 0
	config.PopulationMapping = func(_ context.Context, _ *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, output string, ids []string, _ string, _ int) (dataframeexecution.PopulationMappingResult, error) {
		calls++
		sort.Strings(ids)
		if output != "patients" || !reflect.DeepEqual(ids, []string{"file-001", "file-002", "file-004"}) || projectid.Canonical(bindings.SelectionProject) != project {
			t.Fatalf("unexpected mapping arguments %q/%v/%q", output, ids, bindings.SelectionProject)
		}
		status := dataframeexecution.PopulationMappingComplete
		if !*value.MappingCompletes {
			status = dataframeexecution.PopulationMappingIncomplete
		}
		return dataframeexecution.PopulationMappingResult{Status: status, SelectedCount: 3, MappedCount: 2, UnmappedCount: 1, EmittedRows: 1, UnmappedMemberIDs: []string{"file-004"}}, nil
	}
	domain, createErr := explorer.NewService(store)
	if createErr != nil {
		t.Fatal(createErr)
	}
	service, createErr := New(domain, config)
	if createErr != nil {
		t.Fatal(createErr)
	}
	expected := PopulationMappingBinding{ReceiptID: receipt.ID, OutputID: "patients", Project: project, ExplorerID: receipt.ExplorerID, Generation: selection.Generation, ScopeDigest: snapshot.Identity.AuthorizationScopeDigest, SelectionRevisionID: selection.ID, MembershipDigest: "membership-digest", ResourceType: "Specimen"}
	result, err := service.PopulationMapping(context.Background(), PopulationMappingRequest{Project: requestProject, ExplorerID: "patients", ReceiptID: receipt.ID, OutputID: "patients", Limit: 10})
	row := formalPopulationResult{ID: value.ID, Outcome: string(result.Report.Status), MemberReads: store.visits, MappingCalls: calls, Counts: []int64{}}
	if err != nil {
		row.Outcome = "rejected"
		row.Error = err.Error()
	} else if result.Report.Status == dataframeexecution.PopulationMappingComplete {
		row.Outcome = "complete"
	} else if result.Report.Status == dataframeexecution.PopulationMappingIncomplete {
		row.Outcome = "incomplete"
	}
	if result.Report.Counts != nil {
		c := result.Report.Counts
		row.Counts = []int64{c.Selected, c.Mapped, c.Unmapped, c.EmittedRows}
	}
	row.BindingCorrect = reflect.DeepEqual(result.Report.Binding, expected)
	return row
}

func TestPopulationModelReplay(t *testing.T) {
	const scheduleFile = "testdata/population_mapping_cases.json"
	data, err := os.ReadFile(scheduleFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 4<<20 {
		t.Fatalf("schedule file %s exceeds 4 MiB", scheduleFile)
	}
	var request struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Cases         []formalPopulationCase `json:"cases"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("trailing population input: %v", err)
	}
	if request.SchemaVersion != 1 || len(request.Cases) == 0 || len(request.Cases) > 200 {
		t.Fatal("invalid frozen population schedules")
	}
	seen := map[string]bool{}
	for _, value := range request.Cases {
		if value.ID == "" || seen[value.ID] {
			t.Fatalf("duplicate or missing population case ID: %q", value.ID)
		}
		seen[value.ID] = true
		got := formalPopulationObserve(t, value)
		actual := formalPopulationExpectation{Outcome: got.Outcome, MemberReads: got.MemberReads, MappingCalls: got.MappingCalls, Counts: got.Counts, BindingCorrect: got.BindingCorrect}
		if !reflect.DeepEqual(actual, value.Expected) {
			t.Errorf("schedule %s: got %+v, want frozen behavior %+v", value.ID, actual, value.Expected)
		}
	}
}
