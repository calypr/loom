package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
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
	ID                      string `json:"id"`
	AuthorizedScopeMatches  *bool  `json:"authorizedScopeMatches"`
	SelectionComplete       *bool  `json:"selectionComplete"`
	SelectionBindingMatches *bool  `json:"selectionBindingMatches"`
	MemberReadCompletes     *bool  `json:"memberReadCompletes"`
	MappingCompletes        *bool  `json:"mappingCompletes"`
	Alias                   *bool  `json:"alias"`
	Deadline                *bool  `json:"deadline"`
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
	input, output := os.Getenv("LOOM_FORMAL_POPULATION_INPUT"), os.Getenv("LOOM_FORMAL_POPULATION_OUTPUT")
	if input == "" && output == "" {
		yes, no := true, false
		valid := formalPopulationCase{ID: "complete", AuthorizedScopeMatches: &yes, SelectionComplete: &yes, SelectionBindingMatches: &yes, MemberReadCompletes: &yes, MappingCompletes: &yes, Alias: &no, Deadline: &no}
		if row := formalPopulationObserve(t, valid); row.Outcome != "complete" || !row.BindingCorrect || !reflect.DeepEqual(row.Counts, []int64{3, 2, 1, 1}) {
			t.Fatalf("complete smoke: %#v", row)
		}
		invalid := valid
		invalid.ID = "untrusted"
		invalid.SelectionBindingMatches = &no
		if row := formalPopulationObserve(t, invalid); row.Outcome != "rejected" || row.MappingCalls != 0 || len(row.Counts) != 0 {
			t.Fatalf("untrusted smoke: %#v", row)
		}
		partial := valid
		partial.ID = "incomplete"
		partial.MappingCompletes = &no
		if row := formalPopulationObserve(t, partial); row.Outcome != "incomplete" || len(row.Counts) != 0 {
			t.Fatalf("incomplete smoke: %#v", row)
		}
		return
	}
	if input == "" || output == "" {
		t.Fatal("population input and output paths must be paired")
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 4*1024*1024 {
		t.Fatal("population input exceeds bound")
	}
	file, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var request struct {
		SchemaVersion int                    `json:"schemaVersion"`
		Cases         []formalPopulationCase `json:"cases"`
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("trailing population input: %v", err)
	}
	if request.SchemaVersion != 1 || len(request.Cases) == 0 || len(request.Cases) > 4096 {
		t.Fatal("invalid population case inventory")
	}
	seen := map[string]bool{}
	results := make([]formalPopulationResult, 0, len(request.Cases))
	for _, value := range request.Cases {
		if seen[value.ID] {
			t.Fatal("duplicate population case ID")
		}
		seen[value.ID] = true
		results = append(results, formalPopulationObserve(t, value))
	}
	encoded, err := json.Marshal(struct {
		SchemaVersion int                      `json:"schemaVersion"`
		Results       []formalPopulationResult `json:"results"`
	}{1, results})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, encoded, 0600); err != nil {
		t.Fatal(fmt.Errorf("write population observations: %w", err))
	}
}
