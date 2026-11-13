package authoringv2

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestImportedSourceCommandsRejectUnresolvedOrInjectedState(t *testing.T) {
	for _, raw := range []string{
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields"}}`,
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false,"expr":{"select":"root.id"}}}`,
		`{"type":"REFRESH_IMPORTED_SOURCE","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false}}`,
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false},"resolvedImportedDocument":{}}`,
	} {
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("accepted injected or incomplete source change: %s", raw)
		}
	}
	command := Command{Type: CommandRefreshImportedSource, OutputID: "patients"}
	workspace := constructionWorkspace(legacyPivotDocument())
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "refresh", []Command{command}); err == nil || !strings.Contains(err.Error(), "lifecycle-resolved") {
		t.Fatalf("unresolved source command error = %v", err)
	}
	request := ApplyCommandsRequest{CommandID: "refresh", SemanticsVersion: CurrentSemanticsVersion, SnapshotToken: "snapshot", Commands: []Command{command}}
	if err := request.Validate(); err == nil {
		t.Fatal("source mutation accepted without exact draft identity")
	}
	request.ExpectedDraftVersion, request.ExpectedDraftDigest = 1, "digest"
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestImportedRelationshipIntentRejectsExecutableOrAmbiguousState(t *testing.T) {
	for _, raw := range []string{
		`{"action":"ADD","parentOccurrenceId":"base","edgeId":"edge","matchMode":"OPTIONAL","resourceType":"Patient"}`,
		`{"action":"ADD","parentOccurrenceId":"base","edgeId":"edge","matchMode":"OPTIONAL","occurrenceId":"client_owned"}`,
		`{"action":"MATCH","occurrenceId":"r","matchMode":"FIRST"}`,
		`{"action":"REMOVE","occurrenceId":" r"}`,
	} {
		var change ImportedRelationshipChange
		if err := json.Unmarshal([]byte(raw), &change); err == nil {
			t.Fatalf("accepted invalid relationship intent: %s", raw)
		}
	}
	for _, raw := range []string{
		`{"type":"APPLY_IMPORTED_RELATIONSHIP_PROPOSAL","outputId":"patients","proposalId":"receipt","edgeId":"injected"}`,
		`{"type":"APPLY_IMPORTED_RELATIONSHIP_PROPOSAL","outputId":"patients","proposalId":"receipt","sourceStep":{"stepId":"fields","enabled":false}}`,
	} {
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("accepted injected proposal Apply: %s", raw)
		}
	}
}

func TestImportedGraphAndRowsCannotMutateProjectedDocument(t *testing.T) {
	workspace := constructionWorkspace(legacyPivotDocument())
	document := &workspace.Documents[0]
	document.ImportedRecipe = &ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Output: document.Output.ID, Digest: strings.Repeat("a", 64)}
	for i := range document.Columns {
		document.Columns[i].Source = ColumnSource{Kind: SourceImported, Imported: &ImportedSource{OutputColumn: document.Columns[i].Column}}
	}
	for _, command := range []Command{
		{Type: CommandAddRoute, OutputID: document.Output.ID, ParentOccurrenceID: RootOccurrenceID, EdgeID: "edge"},
		{Type: CommandUpdateRouteEdge, OutputID: document.Output.ID, OccurrenceID: RootOccurrenceID, EdgeID: "edge"},
		{Type: CommandSetRouteMatchMode, OutputID: document.Output.ID, OccurrenceID: RootOccurrenceID, MatchMode: RouteMatchRequired},
		{Type: CommandRemoveRoute, OutputID: document.Output.ID, OccurrenceID: RootOccurrenceID},
	} {
		if _, _, err := ApplyCommands(workspace, commandCatalog(), "imported-guard", []Command{command}); err == nil || !strings.Contains(err.Error(), "IMPORTED_SOURCE_REQUIRED") {
			t.Fatalf("projected route mutation error=%v", err)
		}
	}
	command := Command{Type: CommandApplyRowDefinitionProposal, OutputID: document.Output.ID, ProposalID: "receipt"}
	if err := command.ResolveRowDefinitionProposal(RecordsRowDefinition()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "imported-row-guard", []Command{command}); err == nil || !strings.Contains(err.Error(), "IMPORTED_SOURCE_ROWS_REQUIRED") {
		t.Fatalf("projected row mutation error=%v", err)
	}
}

func TestImportedSourceProposalCannotDeleteOrChangeUnrelatedAuthoredWork(t *testing.T) {
	workspace := constructionWorkspace(legacyPivotDocument())
	document := &workspace.Documents[0]
	document.ImportedRecipe = &ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Output: document.Output.ID, Digest: strings.Repeat("a", 64)}
	for i := range document.Columns {
		document.Columns[i].Source = ColumnSource{Kind: SourceImported, Imported: &ImportedSource{OutputColumn: document.Columns[i].Column}}
	}
	changed := *document
	changed.Output.Title = "Changed outside source edit"
	command := Command{Type: CommandApplyImportedRelationshipProposal, OutputID: document.Output.ID, ProposalID: "receipt"}
	if err := command.ResolveImportedDocument(changed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "source-scope-guard", []Command{command}); err == nil || !strings.Contains(err.Error(), "downstream authored work") {
		t.Fatalf("source edit scope violation error=%v", err)
	}
}
