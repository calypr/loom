package authoringv2

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestImportedSourceCommandsRejectUnresolvedOrInjectedState(t *testing.T) {
	for _, raw := range []string{
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false}}`,
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false,"expr":{"select":"root.id"}}}`,
		`{"type":"REFRESH_IMPORTED_SOURCE","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false}}`,
		`{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"fields","enabled":false},"resolvedImportedDocument":{}}`,
	} {
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("accepted injected or incomplete source change: %s", raw)
		}
	}
	command := Command{Type: CommandRefreshImportedSource, OutputID: "patients", Payload: &ImportedSourcePayload{}}
	migrated, err := migrateStoredDocumentFixture(legacyPivotDocument())
	if err != nil {
		t.Fatal(err)
	}
	workspace := constructionWorkspace(migrated)
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "refresh", []Command{command}); err == nil || !strings.Contains(err.Error(), "lifecycle-resolved") {
		t.Fatalf("unresolved source command error = %v", err)
	}
	request := ApplyCommandsRequest{CommandID: "refresh", SemanticsVersion: ws.CurrentSemanticsVersion, SnapshotToken: "snapshot", Commands: []Command{command}}
	if err := request.Validate(); err == nil {
		t.Fatal("source mutation accepted without exact draft identity")
	}
	request.ExpectedDraftVersion, request.ExpectedDraftDigest = 1, "digest"
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSourceRuleCreateRejectsMismatchedKind(t *testing.T) {
	var rule ws.SourceRuleCreate
	raw := `{"kind":"EXTENSION_COLUMN","parameters":{"dynamic":{"name":"identifiers","source":{"select":"root.identifier[]"}}}}`
	if err := json.Unmarshal([]byte(raw), &rule); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("mismatched source rule error = %v", err)
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
	migrated, err := migrateStoredDocumentFixture(legacyPivotDocument())
	if err != nil {
		t.Fatal(err)
	}
	workspace := constructionWorkspace(migrated)
	document := &workspace.Documents[0]
	document.ImportedRecipe = &ws.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Output: document.Output.ID, Digest: strings.Repeat("a", 64)}
	for i := range document.Columns {
		document.Columns[i].Source = ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: document.Columns[i].Column}}
	}
	for _, command := range []Command{
		{Type: CommandAddRoute, OutputID: document.Output.ID, Payload: &RoutePayload{ParentOccurrenceID: ws.RootOccurrenceID, EdgeID: "edge"}},
		{Type: CommandUpdateRouteEdge, OutputID: document.Output.ID, Payload: &RoutePayload{OccurrenceID: ws.RootOccurrenceID, EdgeID: "edge"}},
		{Type: CommandSetRouteMatchMode, OutputID: document.Output.ID, Payload: &RoutePayload{OccurrenceID: ws.RootOccurrenceID, MatchMode: ws.RouteMatchRequired}},
		{Type: CommandRemoveRoute, OutputID: document.Output.ID, Payload: &RoutePayload{OccurrenceID: ws.RootOccurrenceID}},
	} {
		if _, _, err := ApplyCommands(workspace, commandCatalog(), "imported-guard", []Command{command}); err == nil || !strings.Contains(err.Error(), "IMPORTED_SOURCE_REQUIRED") {
			t.Fatalf("projected route mutation error=%v", err)
		}
	}
	command := Command{Type: CommandApplyRowDefinitionProposal, OutputID: document.Output.ID, Payload: &ProposalPayload{ProposalID: "receipt"}}
	if err := command.ResolveRowDefinitionProposal(ws.RecordsRowDefinition()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "imported-row-guard", []Command{command}); err == nil || !strings.Contains(err.Error(), "IMPORTED_SOURCE_ROWS_REQUIRED") {
		t.Fatalf("projected row mutation error=%v", err)
	}
}

func TestImportedSourceProposalCannotDeleteOrChangeUnrelatedAuthoredWork(t *testing.T) {
	migrated, err := migrateStoredDocumentFixture(legacyPivotDocument())
	if err != nil {
		t.Fatal(err)
	}
	workspace := constructionWorkspace(migrated)
	document := &workspace.Documents[0]
	document.ImportedRecipe = &ws.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Output: document.Output.ID, Digest: strings.Repeat("a", 64)}
	for i := range document.Columns {
		document.Columns[i].Source = ws.ColumnSource{Kind: ws.SourceImported, Imported: &ws.ImportedSource{OutputColumn: document.Columns[i].Column}}
	}
	changed := *document
	changed.Output.Title = "Changed outside source edit"
	command := Command{Type: CommandApplyImportedRelationshipProposal, OutputID: document.Output.ID, Payload: &ImportedSourcePayload{ProposalID: "receipt"}}
	if err := command.ResolveImportedDocument(changed); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "source-scope-guard", []Command{command}); err == nil || !strings.Contains(err.Error(), "downstream authored work") {
		t.Fatalf("source edit scope violation error=%v", err)
	}
}

func TestImportedSourcePlanMigratesLegacyDisabledSteps(t *testing.T) {
	var plan ws.ImportedSourcePlan
	err := json.Unmarshal([]byte(`{
		"steps":[
			{"id":"output","enabled":true},
			{"id":"dynamicColumns[0]","enabled":false},
			{"id":"ruleAdditions[0]","enabled":false},
			{"id":"ruleAdditions[1]","enabled":true}
		],
		"ruleAdditions":[
			{"stepId":"ruleAdditions[0]","enabled":false},
			{"stepId":"ruleAdditions[1]","enabled":true}
		]
	}`), &plan)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].ID != "output" || plan.Steps[1].ID != "ruleAdditions[1]" ||
		len(plan.RuleAdditions) != 1 || plan.RuleAdditions[0].StepID != "ruleAdditions[1]" ||
		plan.NextRuleIndex != 2 || !reflect.DeepEqual(plan.RemovedStepIDs, []string{"dynamicColumns[0]"}) {
		t.Fatalf("unexpected migrated plan: %#v", plan)
	}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"enabled"`)) || bytes.Contains(encoded, []byte(`"ruleAdditions[0]"`)) {
		t.Fatalf("legacy state leaked into current contract: %s", encoded)
	}
	var roundTrip ws.ImportedSourcePlan
	if err := json.Unmarshal(encoded, &roundTrip); err != nil || !reflect.DeepEqual(plan, roundTrip) {
		t.Fatalf("current plan failed round trip: %v", err)
	}
}

func TestImportedSourceStepRemovalRejectsEnablement(t *testing.T) {
	var change ImportedSourceStepChange
	for _, wire := range []string{
		`{"stepId":"fields","enabled":true}`,
		`{"stepId":"fields","enabled":false}`,
		`{"stepId":"fields","remove":true,"maxColumns":1}`,
		`{"stepId":"fields","remove":false}`,
	} {
		if err := json.Unmarshal([]byte(wire), &change); err == nil {
			t.Fatalf("accepted %s", wire)
		}
	}
	if err := json.Unmarshal([]byte(`{"stepId":"fields","remove":true}`), &change); err != nil || !change.Remove {
		t.Fatalf("removal did not decode: %v", err)
	}
}

func TestSourceParametersValidateStructureBeforePinResolution(t *testing.T) {
	valid := `{"stepId":"identity","parameters":{"identity":{"name":"id","expr":{"call":"uuid5","args":[{"literal":"namespace"},{"select":"root.id"}]}}}}`
	var c ImportedSourceStepChange
	if err := json.Unmarshal([]byte(valid), &c); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]string{
		"unknown setting": strings.Replace(valid, `"name":"id"`, `"name":"id","query":"RETURN 1"`, 1),
		"multiple arms":   strings.Replace(valid, `"identity":`, `"output":{"rowGrain":"resource"},"identity":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := json.Unmarshal([]byte(input), &c); err == nil {
				t.Fatal("unsafe source edit admitted")
			}
		})
	}
}
