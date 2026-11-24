package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringcore"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/rowdef"
	"github.com/calypr/loom/internal/explorer/viewer"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

func TestBuilderFullUIRequestReplay(t *testing.T) {
	phase := os.Getenv("BUILDER_FULL_UI_PHASE")
	if phase == "" {
		t.Skip("set BUILDER_FULL_UI_PHASE=prepare or full")
	}
	inventoryPath := os.Getenv("BUILDER_FULL_UI_INVENTORY_JSONL")
	inventoryManifestPath := os.Getenv("BUILDER_FULL_UI_INVENTORY_MANIFEST")
	modelStatesPath := os.Getenv("BUILDER_FULL_UI_MODEL_STATES_JSONL")
	modelStatesSHA := os.Getenv("BUILDER_FULL_UI_MODEL_STATES_SHA256")
	modelJSONLSHA := os.Getenv("BUILDER_FULL_UI_MODEL_JSONL_SHA256")
	preparedSeedsPath := os.Getenv("BUILDER_FULL_UI_PREPARED_SEEDS_JSONL")
	uiObservationsPath := os.Getenv("BUILDER_FULL_UI_UI_OBSERVATIONS")
	goObservationsPath := os.Getenv("BUILDER_FULL_UI_GO_OBSERVATIONS")
	if inventoryPath == "" || inventoryManifestPath == "" || modelStatesPath == "" || modelStatesSHA == "" || modelJSONLSHA == "" {
		t.Fatal("set BUILDER_FULL_UI_INVENTORY_JSONL, BUILDER_FULL_UI_INVENTORY_MANIFEST, BUILDER_FULL_UI_MODEL_STATES_JSONL, BUILDER_FULL_UI_MODEL_STATES_SHA256, and BUILDER_FULL_UI_MODEL_JSONL_SHA256")
	}
	if phase != "prepare" && phase != "full" {
		t.Fatalf("unsupported BUILDER_FULL_UI_PHASE %q; want prepare or full", phase)
	}

	inventoryHeader, inventory, guards, inventorySHA, err := readBuilderFullUIInventory(inventoryPath)
	if err != nil {
		t.Fatalf("read state-space inventory: %v", err)
	}
	if len(inventory) == 0 && len(guards) == 0 {
		t.Fatal("state-space inventory has no case or guard rows")
	}
	if modelJSONLSHA != inventorySHA {
		t.Fatalf("BUILDER_FULL_UI_MODEL_JSONL_SHA256 %q differs from exact model JSONL SHA %q", modelJSONLSHA, inventorySHA)
	}
	manifest, manifestSHA, err := readBuilderFullUIInventoryManifest(inventoryManifestPath, inventoryHeader)
	if err != nil {
		t.Fatalf("read SHA-bound inventory manifest: %v", err)
	}
	if err := bindBuilderFullUIInventorySeeds(inventory, manifest); err != nil {
		t.Fatalf("bind exact inventory seed recipes: %v", err)
	}
	modelStates, actualModelStatesSHA, err := readBuilderFullUIModelStates(modelStatesPath, modelStatesSHA, inventoryHeader)
	if err != nil {
		t.Fatalf("read SHA-bound ModelState artifact: %v", err)
	}
	if err := bindBuilderFullUIModelStates(inventory, modelStates); err != nil {
		t.Fatalf("bind model rows to their before-state references: %v", err)
	}
	guardStatesResolved := bindBuilderFullUIGuardStates(guards, modelStates)
	if phase == "prepare" {
		if preparedSeedsPath == "" {
			t.Fatal("set BUILDER_FULL_UI_PREPARED_SEEDS_JSONL for prepare phase")
		}
		prepareBuilderFullUISeeds(t, inventoryHeader, inventory, guards, guardStatesResolved, manifest, preparedSeedsPath, inventorySHA, manifestSHA, actualModelStatesSHA)
		return
	}
	if preparedSeedsPath == "" || uiObservationsPath == "" || goObservationsPath == "" {
		t.Fatal("set BUILDER_FULL_UI_PREPARED_SEEDS_JSONL, BUILDER_FULL_UI_UI_OBSERVATIONS, and BUILDER_FULL_UI_GO_OBSERVATIONS for full phase")
	}
	preparedSeeds, preparedSeedsHeader, preparedSeedsSHA, err := readBuilderFullUIPreparedSeeds(preparedSeedsPath)
	if err != nil {
		t.Fatalf("read Go prepared seed artifact: %v", err)
	}
	if got := builderFullUIInventorySourceSHA(preparedSeedsHeader); got != builderFullUIInventorySourceSHA(inventoryHeader) {
		t.Fatalf("prepared seed inventorySha256 %q differs from current inventory %q", got, builderFullUIInventorySourceSHA(inventoryHeader))
	}
	if got := builderFullUIPrepareHeaderField(preparedSeedsHeader, "inventoryManifestSha256"); got != manifestSHA {
		t.Fatalf("prepared inventoryManifestSha256 %q differs from current inventory manifest %q", got, manifestSHA)
	}
	if got := builderFullUIPrepareHeaderField(preparedSeedsHeader, "modelStatesSha256"); got != actualModelStatesSHA {
		t.Fatalf("prepared modelStatesSha256 %q differs from current ModelState artifact %q", got, actualModelStatesSHA)
	}
	if got := builderFullUIPrepareHeaderField(preparedSeedsHeader, "modelJsonlSha256"); got != inventorySHA {
		t.Fatalf("prepared modelJsonlSha256 %q differs from current model JSONL %q", got, inventorySHA)
	}
	uiObservations, uiObservationsSHA, err := readBuilderFullUIObservations(uiObservationsPath)
	if err != nil {
		t.Fatalf("read UI observation artifact: %v", err)
	}
	if len(preparedSeeds) != len(inventory) {
		t.Fatalf("inventory rows=%d prepared seed rows=%d; require exact keyed coverage", len(inventory), len(preparedSeeds))
	}
	if len(inventory) != len(uiObservations) {
		t.Fatalf("inventory rows=%d UI observations=%d; require exact keyed coverage", len(inventory), len(uiObservations))
	}
	inventoryKeys := make(map[string]bool, len(inventory))
	for _, row := range inventory {
		key := row.key().string()
		inventoryKeys[key] = true
		if _, exists := uiObservations[key]; !exists {
			t.Fatalf("UI observation artifact is missing inventory key %q/%q/%d", row.CaseID, row.EntryID, row.ActionIndex)
		}
		if _, exists := preparedSeeds[key]; !exists {
			t.Fatalf("prepared-seed artifact is missing inventory key %q/%q/%d", row.CaseID, row.EntryID, row.ActionIndex)
		}
	}
	for key := range uiObservations {
		if !inventoryKeys[key] {
			t.Fatalf("UI observation artifact has unexpected key %q", key)
		}
	}
	for key := range preparedSeeds {
		if !inventoryKeys[key] {
			t.Fatalf("prepared-seed artifact has unexpected key %q", key)
		}
	}

	observations := make([]builderFullUIObservation, 0, len(inventory))
	counts := builderFullUIObservationCounts{}
	emittedRows := 0
	harnesses := make(map[string]*builderFullUIHarness)
	orderedRows := append([]builderFullUIInventoryRow(nil), inventory...)
	sort.SliceStable(orderedRows, func(i, j int) bool {
		if orderedRows[i].CaseID != orderedRows[j].CaseID {
			return orderedRows[i].CaseID < orderedRows[j].CaseID
		}
		if orderedRows[i].ActionIndex != orderedRows[j].ActionIndex {
			return orderedRows[i].ActionIndex < orderedRows[j].ActionIndex
		}
		return orderedRows[i].EntryID < orderedRows[j].EntryID
	})
	for _, row := range orderedRows {
		if row.RequestMode != "emitted" {
			continue
		}
		emittedRows++
		key := row.key()
		ui, exists := uiObservations[key.string()]
		prepared, hasPrepared := preparedSeeds[key.string()]
		if !hasPrepared {
			observations = append(observations, builderFullUIObservation{
				CaseID: row.CaseID, EntryID: row.EntryID, ActionIndex: row.ActionIndex,
				RequestMode: row.RequestMode, Classification: "SETUP_FAILURE",
				Errors: []string{"prepared seed identity missing for inventory request key"},
			})
			counts.SetupFailure++
			continue
		}
		if !exists {
			observations = append(observations, builderFullUIObservation{
				CaseID: row.CaseID, EntryID: row.EntryID, ActionIndex: row.ActionIndex,
				RequestMode: row.RequestMode, Classification: "SETUP_FAILURE",
				Errors: []string{"UI observation missing for inventory request key"},
			})
			counts.SetupFailure++
			continue
		}
		if prepared.Status == "GAP" {
			observations = append(observations, builderFullUIObservation{
				RecordType: "go-observation", SchemaVersion: 1, CaseID: row.CaseID, EntryID: row.EntryID,
				ActionIndex: row.ActionIndex, RequestMode: row.RequestMode, Classification: "GAP",
				Errors: []string{prepared.Error},
			})
			counts.Gap++
			continue
		}
		if prepared.Status != "PREPARED" {
			observations = append(observations, builderFullUIObservation{
				RecordType: "go-observation", SchemaVersion: 1, CaseID: row.CaseID, EntryID: row.EntryID,
				ActionIndex: row.ActionIndex, RequestMode: row.RequestMode, Classification: "SETUP_FAILURE",
				Errors: []string{fmt.Sprintf("prepared seed status %q: %s", prepared.Status, prepared.Error)},
			})
			counts.SetupFailure++
			continue
		}
		harness := harnesses[row.CaseID]
		if harness == nil {
			var harnessErr error
			harness, harnessErr = newBuilderFullUIHarnessFromPrepared(prepared)
			if harnessErr != nil {
				observation := builderFullUIObservation{
					RecordType: "go-observation", SchemaVersion: 1, CaseID: row.CaseID, EntryID: row.EntryID,
					ActionIndex: row.ActionIndex, RequestMode: row.RequestMode, Classification: "SETUP_FAILURE",
					Errors: []string{"create per-trace lifecycle/compiler harness: " + harnessErr.Error()},
				}
				observations = append(observations, observation)
				counts.SetupFailure++
				continue
			}
			harnesses[row.CaseID] = harness
		}
		observation := observeBuilderFullUIRequest(row, ui, prepared, harness)
		observations = append(observations, observation)
		switch observation.Classification {
		case "OBSERVED":
			counts.Observed++
		case "GAP":
			counts.Gap++
		case "SETUP_FAILURE":
			counts.SetupFailure++
		default:
			counts.SetupFailure++
			observation.Classification = "SETUP_FAILURE"
			observation.Errors = append(observation.Errors, "replay produced an unknown classification")
			observations[len(observations)-1] = observation
		}
	}

	outputHeader := builderFullUIObservationHeader{
		RecordType: "header", SchemaVersion: 1,
		InventorySHA256: builderFullUIInventorySourceSHA(inventoryHeader), InventoryManifestSHA256: manifestSHA,
		ModelStatesSHA256: actualModelStatesSHA, ModelJSONLSHA256: inventorySHA,
		PreparedSeedsSHA256: preparedSeedsSHA, UIObservationsSHA256: uiObservationsSHA,
		InventoryRows: len(inventory), EmittedRows: emittedRows, UIObservationRows: len(uiObservations), PreparedSeedRows: len(preparedSeeds),
		GuardRows: len(guards), GuardStatesResolved: guardStatesResolved,
		Counts: counts,
	}
	if err := writeLifecycleJSONL(goObservationsPath, outputHeader, observations); err != nil {
		t.Fatalf("write Go observation artifact: %v", err)
	}

	if counts.Gap+counts.SetupFailure != 0 {
		t.Errorf("Builder full-UI replay found adapter gaps/setup failures: gap=%d setup=%d; complete observations written to %s", counts.Gap, counts.SetupFailure, goObservationsPath)
	}
}

func TestBuilderFullUIScalarFilterAndDeriveRequestsReplayThroughRealService(t *testing.T) {
	tests := []struct {
		name      string
		entryID   string
		seedKind  string
		candidate ws.Construction
		outputID  string
		wantType  string
	}{
		{
			name: "scalar FILTER", entryID: "builder.filter_rows.scalar_condition", seedKind: "rooted_table_filter",
			candidate: builderFullUIScalarFilterCandidate(), wantType: "string",
		},
		{
			name: "scalar DERIVE", entryID: "builder.calculate.derive", seedKind: "rooted_table_calculate",
			candidate: builderFullUIScalarDeriveCandidate(), outputID: "derived-age", wantType: "integer",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateID := "state:builder-full-ui-scalar-operation"
			modelState := builderFullUITestModelState(t, stateID, "rooted-source", recipe.ConstructionSourceProjectionID, nil)
			seedJSON, err := json.Marshal(map[string]any{"id": test.entryID + ".seed", "kind": test.seedKind})
			if err != nil {
				t.Fatal(err)
			}
			row := builderFullUIInventoryRow{CaseID: "scalar-operation-case", EntryID: test.entryID, ActionIndex: 0, RequestMode: "emitted", StateRefBefore: stateID, Before: modelState}
			prepared, err := prepareBuilderFullUISeed(row, builderFullUIInventoryManifestEntry{ID: row.EntryID, Seed: seedJSON})
			if err != nil {
				t.Fatalf("prepare scalar operation seed: %v (%s)", err, prepared.Error)
			}
			harness, err := newBuilderFullUIHarnessFromPrepared(prepared)
			if err != nil {
				t.Fatal(err)
			}
			before := append([]byte(nil), harness.Store.created.DraftConfig...)
			body, err := json.Marshal(map[string]any{
				"snapshotToken": prepared.SnapshotToken, "expectedDraftVersion": prepared.AcceptedVersion,
				"expectedDraftDigest": prepared.AcceptedDigest, "outputId": prepared.OutputID,
				"changedStepId": test.candidate.Steps[0].ID, "candidateConstruction": test.candidate,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := builderFullUIRequest{
				Kind: "constructionProposal", Method: "POST", Endpoint: "/construction-proposals",
				Body: body, BodyRaw: mustJSONRawString(t, body), HasBody: true,
			}
			actualValue, layers, err := executeBuilderFullUIRequest(row, request, prepared, harness)
			if err != nil {
				t.Fatalf("dispatch actual %s proposal request: %v", test.name, err)
			}
			actual := actualValue.(builderFullUIActual)
			if actual.Proposal == nil || actual.Proposal.ProposalID == "" || actual.Preview == nil || actual.Preview.ReceiptID != actual.Proposal.ProposalID || actual.CompiledReceipt == nil {
				t.Fatalf("real %s response omitted proposal/compiler/preview evidence: %#v", test.name, actual)
			}
			if actual.Disposition != "PREVIEWED_INTERNAL_PENDING" || !reflect.DeepEqual(layers, []string{"lifecycle_analyzer_compiler", "preview_orchestration"}) {
				t.Fatalf("real %s disposition/layers=%q/%v", test.name, actual.Disposition, layers)
			}
			if actual.Proposal.CandidateConstruction.Steps[0].ID != test.candidate.Steps[0].ID || harness.Store.saveDraftCalls != 0 || !bytes.Equal(harness.Store.created.DraftConfig, before) {
				t.Fatalf("real %s proposal changed accepted workspace or step identity", test.name)
			}
			if test.outputID != "" {
				compiled := actual.CompiledReceipt.CompiledOutputSchemas[prepared.OutputID]
				found := false
				for _, column := range compiled {
					if column.ID == test.outputID {
						found = column.LogicalType == test.wantType
					}
				}
				if !found {
					t.Fatalf("actual lowered output does not contain %s with type %s: %#v", test.outputID, test.wantType, compiled)
				}
			}
		})
	}
}

func builderFullUIScalarFilterCandidate() ws.Construction {
	value := "x"
	return ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "filter-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{
			ColumnID: "gender-id", Operator: ws.ConstructionFilterEquals,
			Values: []ws.FilterValue{{Kind: ws.ConstructionFilterString, String: &value}},
		}},
		Outputs: []ws.StageColumn{{ID: "gender-id", Name: "gender", Label: "Gender", Type: "string"}, {ID: "age-id", Name: "age", Label: "Age", Type: "integer"}},
	}}}
}

func builderFullUIScalarDeriveCandidate() ws.Construction {
	one := int64(1)
	return ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "derive-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
			ConstructionID: "derive-step", OutputColumnID: "derived-age", Operation: ws.ConstructionDerivedAdd,
			Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "age-id"},
			Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &one}},
			MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
		}},
		Outputs: []ws.StageColumn{{ID: "gender-id", Name: "gender", Label: "Gender", Type: "string"}, {ID: "age-id", Name: "age", Label: "Age", Type: "integer"}, {ID: "derived-age", Name: "derived_age", Label: "Derived age", Type: "integer"}},
	}}}
}

func mustJSONRawString(t *testing.T, body []byte) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(string(body))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestBuilderFullUIGroupFilterDeriveAggregateEditReplaysLifecycle(t *testing.T) {

	fixture, workspace, _, err := builderFullUIGroupFilterDeriveFixture()
	if err != nil {
		t.Fatal(err)
	}
	harness, err := newBuilderFullUIHarness(fixture, workspace)
	if err != nil {
		t.Fatalf("real accepted-workspace reconcile and compile: %v", err)
	}
	acceptedBytes := append([]byte(nil), harness.Store.created.DraftConfig...)
	acceptedDigest := harness.Store.created.DraftDigest
	candidate := cloneLifecycleTestConstruction(t, *workspace.Documents[0].Construction)
	candidate.Steps[0].Operation.Group.Aggregates[0] = ws.ConstructionGroupAggregate{
		Operation: ws.ConstructionGroupMean, InputColumnID: "age-id", OutputColumnID: "row-count",
	}
	proposal, err := harness.Service.ProposeConstruction(context.Background(), construction.ConstructionProposalRequest{
		Project: fixture.Project, ExplorerID: fixture.ExplorerID, SnapshotToken: fixture.Snapshot.Token,
		ExpectedDraftVersion: harness.Store.created.DraftVersion, ExpectedDraftDigest: acceptedDigest,
		OutputID: fixture.OutputID, ChangedStepID: "group-step", CandidateConstruction: candidate,
	})
	if err != nil {
		t.Fatalf("actual ProposeConstruction with GROUP COUNT_ROWS→MEAN and downstream dependents: %v", err)
	}
	if proposal.PreviewStatus != "PREVIEW_PENDING" || proposal.ProposalID == "" {
		t.Fatalf("actual proposal disposition/id=%q/%q", proposal.PreviewStatus, proposal.ProposalID)
	}
	for _, check := range []struct{ stepID, columnID, wantType string }{
		{"group-step", "gender-group", "string"}, {"group-step", "row-count", "decimal"},
		{"filter-step", "row-count", "decimal"}, {"derive-step", "row-count", "decimal"}, {"derive-step", "adjusted", "decimal"},
	} {
		if got := traceReceiptStageColumnType(harness.Store.receipt, check.stepID, check.columnID); got != check.wantType {
			t.Errorf("actual compiler/lowering stage %s column %s type=%q, want %q", check.stepID, check.columnID, got, check.wantType)
		}
	}
	compiledColumns := make(map[string]receiptmodel.ReceiptCompiledOutputColumn)
	for _, column := range harness.Store.receipt.CompiledOutputSchemas[fixture.OutputID] {
		compiledColumns[column.ID] = column
	}
	for _, check := range []struct {
		columnID string
		wantType string
		nullable bool
	}{{"gender-group", "string", true}, {"row-count", "decimal", true}, {"adjusted", "decimal", true}} {
		column, exists := compiledColumns[check.columnID]
		if !exists || column.LogicalType != check.wantType || column.Nullable != check.nullable {
			t.Errorf("actual lower compiled output column %q=%#v present=%t, want logicalType=%s nullable=%t", check.columnID, column, exists, check.wantType, check.nullable)
		}
	}
	if len(proposal.CandidateConstruction.Steps) != 3 || proposal.CandidateConstruction.Steps[0].ID != "group-step" || proposal.CandidateConstruction.Steps[1].ID != "filter-step" || proposal.CandidateConstruction.Steps[2].ID != "derive-step" {
		t.Fatalf("actual proposal did not preserve the dependent step order and IDs: %#v", proposal.CandidateConstruction.Steps)
	}
	if harness.Store.saveDraftCalls != 0 || harness.Store.created.DraftDigest != acceptedDigest || !bytes.Equal(harness.Store.created.DraftConfig, acceptedBytes) {
		t.Fatal("proposal exchange mutated the accepted fake-store workspace")
	}
	preview, err := harness.Service.Preview(context.Background(), authoringcore.PreviewRequest{
		Project: fixture.Project, ExplorerID: fixture.ExplorerID, ReceiptID: proposal.ProposalID, OutputID: fixture.OutputID,
		SinkFactory: func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	if err != nil || preview.Receipt == nil || preview.Receipt.ID != proposal.ProposalID {
		t.Fatalf("actual proposal preview receipt=%#v err=%v", preview.Receipt, err)
	}
	apply, err := harness.Service.ApplyCommands(context.Background(), fixture.Project, fixture.ExplorerID, authoringv2.ApplyCommandsRequest{
		CommandID: "builder-full-ui-apply-mean", SemanticsVersion: ws.CurrentSemanticsVersion,
		SnapshotToken: fixture.Snapshot.Token, ExpectedDraftVersion: fixture.AcceptedVersion, ExpectedDraftDigest: acceptedDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: fixture.OutputID, Payload: &authoringv2.ProposalPayload{ProposalID: proposal.ProposalID}}},
	}, "builder-full-ui-replay")
	if err != nil {
		t.Fatalf("actual ApplyCommands for reviewed proposal: %v", err)
	}
	if harness.Store.saveDraftCalls != 1 || harness.Store.created.DraftVersion != fixture.AcceptedVersion+1 || harness.Store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("actual apply acknowledgement did not save once and advance the reviewed draft: apply=%#v saves=%d storeVersion=%d storeDigest=%q proposalDigest=%q", apply, harness.Store.saveDraftCalls, harness.Store.created.DraftVersion, harness.Store.created.DraftDigest, proposal.CandidateWorkspaceDigest)
	}
	persisted, err := ws.DecodeWorkspace(harness.Store.created.DraftConfig)
	if err != nil {
		t.Fatalf("decode persisted fake-store draft: %v", err)
	}
	persistedJSON, err := persisted.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	applyJSON, err := apply.Workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(persistedJSON, applyJSON) {
		t.Fatalf("persisted fake-store draft differs from Apply response workspace: persisted=%s apply=%s", persistedJSON, applyJSON)
	}
}

type builderFullUIKey struct {
	CaseID      string
	EntryID     string
	ActionIndex int
}

func (k builderFullUIKey) string() string {
	return fmt.Sprintf("%s\x00%s\x00%d", k.CaseID, k.EntryID, k.ActionIndex)
}

type builderFullUIInventoryRow struct {
	CaseID               string
	EntryID              string
	ActionIndex          int
	RequestMode          string
	StateRefBefore       string
	StateResolveError    string
	SeedID               string
	SeedKind             string
	Request              json.RawMessage
	Requests             json.RawMessage
	Before               json.RawMessage
	Action               json.RawMessage
	ActionRequest        json.RawMessage
	ActionExpectation    json.RawMessage
	RequestStep          json.RawMessage
	Seed                 json.RawMessage
	InputState           json.RawMessage
	ExpectedState        json.RawMessage
	Assignment           json.RawMessage
	Actions              json.RawMessage
	TracePrefix          json.RawMessage
	CapabilityProvenance json.RawMessage
	Expectation          json.RawMessage
	Raw                  json.RawMessage
}

type builderFullUIModelStateRow struct {
	RecordType    string          `json:"recordType"`
	SchemaVersion int             `json:"schemaVersion"`
	StateID       string          `json:"stateId"`
	ModelState    json.RawMessage `json:"modelState"`
}

type builderFullUIGuardRow struct {
	GuardID           string          `json:"guardId"`
	EntryID           string          `json:"entryId"`
	StateID           string          `json:"stateId"`
	Raw               json.RawMessage `json:"-"`
	State             json.RawMessage `json:"-"`
	StateResolveError string          `json:"-"`
}

type builderFullUIStateRefs struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

type builderFullUIInventoryManifest struct {
	SchemaVersion int                                   `json:"schemaVersion"`
	SourceCommit  string                                `json:"sourceCommit"`
	Affordances   []builderFullUIInventoryManifestEntry `json:"affordances"`
}

type builderFullUIInventoryManifestEntry struct {
	ID                   string          `json:"id"`
	Seed                 json.RawMessage `json:"seed"`
	CapabilityProvenance json.RawMessage `json:"capabilityProvenance"`
}

type builderFullUIManifestSeed struct {
	ID                    string          `json:"id"`
	Kind                  string          `json:"kind"`
	Requirements          json.RawMessage `json:"requirements"`
	IdentityPrerequisites []string        `json:"identityPrerequisites"`
	CapabilityFixture     json.RawMessage `json:"capabilityFixture"`
	Provenance            json.RawMessage `json:"provenance"`
}

type builderFullUIRequest struct {
	Kind     string
	Method   string
	Endpoint string
	BodyRaw  json.RawMessage
	Body     json.RawMessage
	HasBody  bool
	Raw      json.RawMessage
}

type builderFullUISeed struct {
	RecordType           string          `json:"recordType"`
	SchemaVersion        int             `json:"schemaVersion"`
	CaseID               string          `json:"caseId"`
	EntryID              string          `json:"entryId"`
	ActionIndex          int             `json:"actionIndex"`
	RequestMode          string          `json:"requestMode"`
	Status               string          `json:"status"`
	Project              string          `json:"project,omitempty"`
	ExplorerID           string          `json:"explorerId,omitempty"`
	AcceptedVersion      int64           `json:"acceptedVersion,omitempty"`
	AcceptedDigest       string          `json:"acceptedDigest,omitempty"`
	SnapshotToken        string          `json:"snapshotToken,omitempty"`
	OutputID             string          `json:"outputId,omitempty"`
	BaseReceiptID        string          `json:"baseReceiptId,omitempty"`
	AcceptedWorkspace    json.RawMessage `json:"acceptedWorkspace,omitempty"`
	CapabilityFixture    json.RawMessage `json:"capabilityFixture,omitempty"`
	CapabilityProvenance json.RawMessage `json:"capabilityProvenance,omitempty"`
	BuilderState         json.RawMessage `json:"builderState,omitempty"`
	Catalog              json.RawMessage `json:"catalog,omitempty"`
	Capabilities         json.RawMessage `json:"capabilities,omitempty"`
	CapabilityRequest    json.RawMessage `json:"capabilityRequest,omitempty"`
	FixtureBindings      json.RawMessage `json:"fixtureBindings,omitempty"`
	CompileReceiptID     string          `json:"compileReceiptId,omitempty"`
	Error                string          `json:"error,omitempty"`
	SeedID               string          `json:"seedId,omitempty"`
	SeedKind             string          `json:"seedKind,omitempty"`
	SeedProfile          string          `json:"seedProfile,omitempty"`
	WorkspacePhase       string          `json:"workspacePhase,omitempty"`
	ModelSeed            json.RawMessage `json:"modelSeed,omitempty"`
	ModelStateID         string          `json:"modelStateId,omitempty"`
	ModelState           json.RawMessage `json:"modelState,omitempty"`
	StateRefBefore       string          `json:"stateRefBefore,omitempty"`
	StateResolveError    string          `json:"-"`
	Raw                  json.RawMessage `json:"-"`
}

type builderFullUIFixtureBindings struct {
	Columns        map[string]builderFullUIColumnBinding `json:"columns"`
	UnboundColumns []builderFullUIUnboundColumn          `json:"unboundColumns,omitempty"`
}

type builderFullUIColumnBinding struct {
	ClassID        string `json:"classId"`
	ActualColumnID string `json:"actualColumnId"`
}

type builderFullUIUnboundColumn struct {
	ModelColumnID string `json:"modelColumnId"`
	ClassID       string `json:"classId"`
	Reason        string `json:"reason"`
}

type builderFullUICapabilityFixture struct {
	Project           string                          `json:"project"`
	ExplorerID        string                          `json:"explorerId"`
	AcceptedVersion   int64                           `json:"acceptedVersion"`
	OutputID          string                          `json:"outputId"`
	Snapshot          capability.Snapshot             `json:"snapshot"`
	Catalog           ws.CatalogSnapshot              `json:"catalog"`
	CapabilityRequest *builderFullUICapabilityRequest `json:"capabilityRequest,omitempty"`
	NoSavedWorkspace  bool                            `json:"noSavedWorkspace,omitempty"`
}

type builderFullUICapabilityRequest struct {
	StageID     string `json:"stageId"`
	GroupStepID string `json:"groupStepId,omitempty"`
}

type builderFullUIHarness struct {
	Service      *Service
	Store        *fakeStore
	Workspace    ws.Workspace
	Fixture      builderFullUICapabilityFixture
	Base         *receiptmodel.CompilationReceipt
	Capabilities construction.ConstructionCapabilitiesResponse
	LastPreview  *authoringcore.PreviewResult
}

type builderFullUIObservation struct {
	RecordType     string          `json:"recordType"`
	SchemaVersion  int             `json:"schemaVersion"`
	CaseID         string          `json:"caseId"`
	EntryID        string          `json:"entryId"`
	ActionIndex    int             `json:"actionIndex"`
	RequestMode    string          `json:"requestMode"`
	RequestKind    string          `json:"requestKind,omitempty"`
	DispatchKind   string          `json:"dispatchKind,omitempty"`
	Endpoint       string          `json:"endpoint,omitempty"`
	Method         string          `json:"method,omitempty"`
	Classification string          `json:"classification"`
	LayerReached   []string        `json:"layerReached,omitempty"`
	RequestSHA256  string          `json:"requestSHA256,omitempty"`
	Request        json.RawMessage `json:"request,omitempty"`
	Actual         json.RawMessage `json:"actual,omitempty"`
	Errors         []string        `json:"errors,omitempty"`
}

type builderFullUIObservationHeader struct {
	RecordType              string                         `json:"recordType"`
	SchemaVersion           int                            `json:"schemaVersion"`
	InventorySHA256         string                         `json:"inventorySha256"`
	InventoryManifestSHA256 string                         `json:"inventoryManifestSha256"`
	ModelStatesSHA256       string                         `json:"modelStatesSha256"`
	ModelJSONLSHA256        string                         `json:"modelJsonlSha256"`
	PreparedSeedsSHA256     string                         `json:"preparedSeedsSha256"`
	UIObservationsSHA256    string                         `json:"uiObservationsSha256"`
	InventoryRows           int                            `json:"inventoryRows"`
	EmittedRows             int                            `json:"emittedRows"`
	UIObservationRows       int                            `json:"uiObservationRows"`
	PreparedSeedRows        int                            `json:"preparedSeedRows"`
	GuardRows               int                            `json:"guardRows"`
	GuardStatesResolved     int                            `json:"guardStatesResolved"`
	Counts                  builderFullUIObservationCounts `json:"counts"`
}

type builderFullUIObservationCounts struct {
	Observed     int `json:"observed"`
	Gap          int `json:"gap"`
	SetupFailure int `json:"setupFailure"`
}

type builderFullUIPrepareHeader struct {
	RecordType              string `json:"recordType"`
	SchemaVersion           int    `json:"schemaVersion"`
	Phase                   string `json:"phase"`
	InventorySHA256         string `json:"inventorySha256"`
	InventoryManifestSHA256 string `json:"inventoryManifestSha256"`
	ModelStatesSHA256       string `json:"modelStatesSha256"`
	ModelJSONLSHA256        string `json:"modelJsonlSha256"`
	SeedCount               int    `json:"seedCount"`
	GuardCount              int    `json:"guardCount"`
	GuardStatesResolved     int    `json:"guardStatesResolved"`
	PreparedCount           int    `json:"preparedCount"`
	BlockedCount            int    `json:"blockedCount"`
	LocalCount              int    `json:"localCount"`
	GapCount                int    `json:"gapCount"`
	SetupFailureCount       int    `json:"setupFailureCount"`
}

type builderFullUIAbstractState struct {
	StateID        string `json:"stateId"`
	SeedProfile    string `json:"seedProfile"`
	WorkspacePhase string `json:"workspacePhase"`
	RootContext    *struct {
		Project             string          `json:"project"`
		ExplorerID          string          `json:"explorerId"`
		SnapshotToken       string          `json:"snapshotToken"`
		SelectionRevisionID string          `json:"selectionRevisionId"`
		DraftVersion        int64           `json:"draftVersion"`
		DraftDigest         string          `json:"draftDigest"`
		CatalogSnapshot     json.RawMessage `json:"catalogSnapshot"`
	} `json:"rootContext"`
	AcceptedWorkspace *struct {
		RootType string `json:"rootType"`
		Identity struct {
			OutputID string `json:"outputId"`
		} `json:"identity"`
		Construction struct {
			Steps []struct {
				ID        string `json:"id"`
				Operation struct {
					Kind string `json:"kind"`
				} `json:"operation"`
			} `json:"steps"`
		} `json:"construction"`
	} `json:"acceptedWorkspace"`
	SelectedStage *struct {
		ID        string `json:"id"`
		Operation string `json:"operation"`
	} `json:"selectedStage"`
}

func decodeBuilderFullUIAbstractState(source builderFullUIInventoryRow) (builderFullUIAbstractState, error) {
	var state builderFullUIAbstractState
	if err := json.Unmarshal(source.Before, &state); err != nil {
		return state, err
	}
	if state.StateID != source.StateRefBefore {
		return state, fmt.Errorf("stateId %q differs from stateRefs.before %q", state.StateID, source.StateRefBefore)
	}
	return state, nil
}

func TestBuilderFullUIReaderKeepsKeyedSetupFailureLocal(t *testing.T) {
	fixture, workspace, _, err := builderFullUIGroupFilterDeriveFixture()
	if err != nil {
		t.Fatal(err)
	}
	harness, err := newBuilderFullUIHarness(fixture, workspace)
	if err != nil {
		t.Fatalf("real accepted-workspace reconcile and compile: %v", err)
	}
	acceptedWorkspace, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	prepared := builderFullUISeed{
		RecordType: "prepared-seed", SchemaVersion: 1, RequestMode: "emitted", Status: "PREPARED",
		Project: fixture.Project, ExplorerID: fixture.ExplorerID, AcceptedVersion: fixture.AcceptedVersion,
		AcceptedDigest: digest, SnapshotToken: fixture.Snapshot.Token, OutputID: fixture.OutputID,
		AcceptedWorkspace: acceptedWorkspace,
	}
	const caseID = "reader-row-local-setup-failure"
	const entryID = "builder.group_filter_derive.edit_saved_group"
	firstKey := builderFullUIKey{CaseID: caseID, EntryID: entryID, ActionIndex: 0}
	secondKey := builderFullUIKey{CaseID: caseID, EntryID: entryID, ActionIndex: 1}
	preparedFirst, preparedSecond := prepared, prepared
	preparedFirst.CaseID, preparedFirst.EntryID, preparedFirst.ActionIndex = firstKey.CaseID, firstKey.EntryID, firstKey.ActionIndex
	preparedSecond.CaseID, preparedSecond.EntryID, preparedSecond.ActionIndex = secondKey.CaseID, secondKey.EntryID, secondKey.ActionIndex

	firstRow := map[string]any{
		"recordType": "ui-observation", "schemaVersion": 1, "caseId": caseID, "entryId": entryID, "actionIndex": 0,
		"model":  map[string]any{"requestMode": "emitted"},
		"actual": map[string]any{"acceptedWorkspace": json.RawMessage(acceptedWorkspace), "request": nil},
	}
	candidate := cloneLifecycleTestConstruction(t, *workspace.Documents[0].Construction)
	candidate.Steps[0].Operation.Group.Aggregates[0] = ws.ConstructionGroupAggregate{
		Operation: ws.ConstructionGroupMean, InputColumnID: "age-id", OutputColumnID: "row-count",
	}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"snapshotToken": fixture.Snapshot.Token, "expectedDraftVersion": fixture.AcceptedVersion,
		"expectedDraftDigest": digest, "outputId": fixture.OutputID, "changedStepId": "group-step",
		"candidateConstruction": json.RawMessage(candidateJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, err := json.Marshal(map[string]any{
		"kind": "constructionProposal", "method": "POST",
		"endpoint": fmt.Sprintf("/api/v1/projects/%s/explorers/%s/authoring/v2/construction-proposals?trace=preserve-me", fixture.Project, fixture.ExplorerID),
		"bodyRaw":  string(body), "body": json.RawMessage(body),
	})
	if err != nil {
		t.Fatal(err)
	}
	secondRow := map[string]any{
		"recordType": "ui-observation", "schemaVersion": 1, "caseId": caseID, "entryId": entryID, "actionIndex": 1,
		"requestMode": "emitted", "model": map[string]any{"requestMode": "emitted"},
		"actual": map[string]any{"acceptedWorkspace": json.RawMessage(acceptedWorkspace), "request": json.RawMessage(requestJSON)},
	}
	var artifact bytes.Buffer
	for _, row := range []map[string]any{firstRow, secondRow} {
		encoded, marshalErr := json.Marshal(row)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		artifact.Write(encoded)
		artifact.WriteByte('\n')
	}
	path := filepath.Join(t.TempDir(), "ui-observations.jsonl")
	if err := os.WriteFile(path, artifact.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	uiRows, _, err := readBuilderFullUIObservations(path)
	if err != nil {
		t.Fatalf("read keyed UI observations: %v", err)
	}
	if got := uiRows[firstKey.string()].RequestMode; got != "" {
		t.Fatalf("missing top-level requestMode fell back to nested model value %q", got)
	}
	first := observeBuilderFullUIRequest(builderFullUIInventoryRow{CaseID: caseID, EntryID: entryID, ActionIndex: 0, RequestMode: "emitted"}, uiRows[firstKey.string()], preparedFirst, harness)
	if first.Classification != "SETUP_FAILURE" || len(first.Errors) == 0 || !strings.Contains(first.Errors[0], "requestMode differs") {
		t.Fatalf("missing request should be a row-local setup failure, got %#v", first)
	}
	second := observeBuilderFullUIRequest(builderFullUIInventoryRow{CaseID: caseID, EntryID: entryID, ActionIndex: 1, RequestMode: "emitted"}, uiRows[secondKey.string()], preparedSecond, harness)
	if second.Classification != "OBSERVED" {
		t.Fatalf("valid row after setup failure did not reach real service replay: %#v", second)
	}
	wantEndpoint := fmt.Sprintf("/api/v1/projects/%s/explorers/%s/authoring/v2/construction-proposals?trace=preserve-me", fixture.Project, fixture.ExplorerID)
	if second.Endpoint != wantEndpoint {
		t.Fatalf("replayed concrete query/path endpoint=%q, want unchanged %q", second.Endpoint, wantEndpoint)
	}
	var actual builderFullUIActual
	if err := json.Unmarshal(second.Actual, &actual); err != nil {
		t.Fatal(err)
	}
	if actual.Proposal == nil || actual.CompiledReceipt == nil || actual.Preview == nil || actual.Preview.ReceiptID != actual.Proposal.ProposalID {
		t.Fatalf("subsequent valid row lacks actual proposal/compiler/preview evidence: %#v", actual)
	}
}

type builderFullUIUIObservation struct {
	RecordType    string          `json:"recordType"`
	SchemaVersion int             `json:"schemaVersion"`
	CaseID        string          `json:"caseId"`
	EntryID       string          `json:"entryId"`
	ActionIndex   int             `json:"actionIndex"`
	RequestMode   string          `json:"requestMode"`
	Actual        json.RawMessage `json:"actual"`
	Raw           json.RawMessage `json:"-"`
}

func observeBuilderFullUIRequest(row builderFullUIInventoryRow, ui builderFullUIUIObservation, prepared builderFullUISeed, harness *builderFullUIHarness) builderFullUIObservation {
	observation := builderFullUIObservation{
		RecordType: "go-observation", SchemaVersion: 1, CaseID: row.CaseID, EntryID: row.EntryID,
		ActionIndex: row.ActionIndex, RequestMode: row.RequestMode,
	}
	if ui.CaseID != row.CaseID || ui.EntryID != row.EntryID || ui.ActionIndex != row.ActionIndex || ui.RequestMode != row.RequestMode {
		observation.Classification = "SETUP_FAILURE"
		observation.Errors = []string{"UI observation key or requestMode differs from inventory"}
		return observation
	}
	if prepared.CaseID != row.CaseID || prepared.EntryID != row.EntryID || prepared.ActionIndex != row.ActionIndex || prepared.RequestMode != row.RequestMode {
		observation.Classification = "SETUP_FAILURE"
		observation.Errors = []string{"prepared seed key or requestMode differs from inventory"}
		return observation
	}
	if prepared.StateRefBefore != row.StateRefBefore || prepared.ModelStateID != row.StateRefBefore || !bytes.Equal(prepared.ModelState, row.Before) || prepared.SeedID != row.SeedID || prepared.SeedKind != row.SeedKind {
		observation.Classification = "SETUP_FAILURE"
		observation.Errors = []string{"prepared exact seed/ModelState binding differs from the model row's manifest entryId and stateRefs.before"}
		return observation
	}
	var captured struct {
		AcceptedWorkspace json.RawMessage `json:"acceptedWorkspace"`
		Request           json.RawMessage `json:"request"`
	}
	if err := json.Unmarshal(ui.Actual, &captured); err != nil {
		observation.Classification = "SETUP_FAILURE"
		observation.Errors = []string{"decode UI actual fields: " + err.Error()}
		return observation
	}
	if isJSONNullOrEmpty(captured.Request) {
		observation.Classification = "SETUP_FAILURE"
		observation.Errors = []string{"model-emitted UI checkpoint captured no request"}
		return observation
	}
	request, err := decodeBuilderFullUIRequest(captured.Request)
	if err != nil {
		observation.Classification = "SETUP_FAILURE"
		observation.Errors = []string{"decode actual UI request: " + err.Error()}
		return observation
	}
	observation.RequestKind, observation.Endpoint, observation.Method = request.Kind, request.Endpoint, request.Method
	observation.DispatchKind = builderFullUIDispatchKind(request.Kind)
	observation.Request = append(json.RawMessage(nil), request.Raw...)
	if request.HasBody {
		bodyRaw, _, bodyErr := builderFullUISerializedBody(request)
		if bodyErr != nil {
			observation.Classification = "SETUP_FAILURE"
			observation.Errors = []string{"decode exact UI request body: " + bodyErr.Error()}
			return observation
		}
		sum := sha256.Sum256([]byte(bodyRaw))
		observation.RequestSHA256 = hex.EncodeToString(sum[:])
	}
	if harness.Fixture.NoSavedWorkspace {
		if !isJSONNullOrEmpty(captured.AcceptedWorkspace) {
			observation.Classification = "SETUP_FAILURE"
			observation.Errors = []string{"root-uninitialized UI checkpoint unexpectedly carried a saved acceptedWorkspace"}
			return observation
		}
	} else {
		if isJSONNullOrEmpty(captured.AcceptedWorkspace) {
			observation.Classification = "SETUP_FAILURE"
			observation.Errors = []string{"emitted UI checkpoint lacks acceptedWorkspace"}
			return observation
		}
		workspace, err := ws.DecodeWorkspace(captured.AcceptedWorkspace)
		if err != nil {
			observation.Classification = "SETUP_FAILURE"
			observation.Errors = []string{"decode captured acceptedWorkspace: " + err.Error()}
			return observation
		}
		currentDigest, digestErr := workspace.Digest()
		if digestErr != nil {
			observation.Classification = "SETUP_FAILURE"
			observation.Errors = []string{"digest captured acceptedWorkspace: " + digestErr.Error()}
			return observation
		}
		if currentDigest != harness.Store.created.DraftDigest {
			observation.Classification = "SETUP_FAILURE"
			observation.Errors = []string{fmt.Sprintf("captured accepted workspace digest %q differs from current fake-store digest %q", currentDigest, harness.Store.created.DraftDigest)}
			return observation
		}
	}
	actual, layers, replayErr := executeBuilderFullUIRequest(row, request, prepared, harness)
	if replayErr != nil {
		observation.Classification = "SETUP_FAILURE"
		if strings.HasPrefix(replayErr.Error(), "GAP: ") {
			observation.Classification = "GAP"
		}
		observation.Errors = []string{replayErr.Error()}
		return observation
	}
	observation.Actual, err = json.Marshal(actual)
	if err != nil {
		observation.Classification = "SETUP_FAILURE"
		observation.Errors = []string{"encode actual lifecycle observation: " + err.Error()}
		return observation
	}
	observation.LayerReached = layers
	observation.Classification = "OBSERVED"
	return observation
}

type builderFullUIActualServiceError struct {
	Class   ports.ErrorClass `json:"class"`
	Stage   string           `json:"stage"`
	Code    string           `json:"code"`
	Path    string           `json:"path,omitempty"`
	Message string           `json:"message"`
	Details json.RawMessage  `json:"details,omitempty"`
}

type builderFullUIActualReceipt struct {
	ID                    string                                                `json:"id"`
	IntentDigest          string                                                `json:"intentDigest"`
	OutputSchemas         map[string][]builderFullUIActualCol                   `json:"outputSchemas"`
	CompiledOutputSchemas map[string][]receiptmodel.ReceiptCompiledOutputColumn `json:"compiledOutputSchemas"`
	ConstructionStages    map[string][]receiptmodel.ReceiptConstructionStage    `json:"constructionStages"`
}

type builderFullUIActualCol struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	Nullable    bool   `json:"nullable"`
}

type builderFullUIActual struct {
	Kind                  string                                         `json:"kind"`
	Disposition           string                                         `json:"disposition"`
	Proposal              *construction.ConstructionProposalResponse     `json:"proposal,omitempty"`
	RowDefinitionProposal *rowdef.RowDefinitionProposal                  `json:"rowDefinitionProposal,omitempty"`
	Capabilities          *construction.ConstructionCapabilitiesResponse `json:"capabilities,omitempty"`
	Suggestions           *builderFullUIActualSuggestions                `json:"suggestions,omitempty"`
	RowDefinitionChoices  *rowdef.RowDefinitionChoicesResponse           `json:"rowDefinitionChoices,omitempty"`
	CompiledReceipt       *builderFullUIActualReceipt                    `json:"compiledReceipt,omitempty"`
	Preview               *builderFullUIPreviewObservation               `json:"preview,omitempty"`
	Applied               *authoringv2.ApplyCommandsResponse             `json:"applied,omitempty"`
	AcceptedWorkspace     json.RawMessage                                `json:"acceptedWorkspace,omitempty"`
	ServiceError          *builderFullUIActualServiceError               `json:"serviceError,omitempty"`
	FakeStore             *builderFullUIFakeStoreObservation             `json:"fakeStorePersistence,omitempty"`
}

type builderFullUIActualSuggestions struct {
	SnapshotToken string                `json:"snapshotToken"`
	NodeID        string                `json:"nodeId"`
	Candidates    []ws.CatalogCandidate `json:"candidates"`
}

type builderFullUIPreviewObservation struct {
	ReceiptID             string   `json:"receiptId"`
	OutputID              string   `json:"outputId"`
	Columns               []string `json:"columns"`
	DataExecutionObserved bool     `json:"dataExecutionObserved"`
}

type builderFullUIFakeStoreObservation struct {
	SaveDraftCalls int    `json:"saveDraftCalls"`
	DraftVersion   int64  `json:"draftVersion"`
	DraftDigest    string `json:"draftDigest"`
}

func lifecycleErrorObservation(err error) *builderFullUIActualServiceError {
	var appErr *ports.Error
	if errors.As(err, &appErr) {
		details, _ := json.Marshal(appErr.Details)
		return &builderFullUIActualServiceError{Class: appErr.Class, Stage: appErr.Stage, Code: appErr.Code, Path: appErr.Path, Message: appErr.Message, Details: details}
	}
	return &builderFullUIActualServiceError{Class: ports.ClassInternal, Message: err.Error()}
}

func actualReceipt(receipt *receiptmodel.CompilationReceipt) *builderFullUIActualReceipt {
	if receipt == nil {
		return nil
	}
	result := &builderFullUIActualReceipt{
		ID: receipt.ID, IntentDigest: receipt.IntentDigest, OutputSchemas: map[string][]builderFullUIActualCol{},
		CompiledOutputSchemas: receipt.CompiledOutputSchemas, ConstructionStages: receipt.ConstructionStages,
	}
	for _, emitted := range receipt.EmittedColumns {
		result.OutputSchemas[emitted.OutputID] = append(result.OutputSchemas[emitted.OutputID], builderFullUIActualCol{ID: emitted.EmissionID, Name: emitted.PublicColumn, Label: emitted.Label, LogicalType: emitted.LogicalType, Nullable: emitted.Nullable})
	}
	return result
}

func normalizeBuilderFullUIKind(kind string) string {
	kind = strings.ToLower(kind)
	kind = strings.ReplaceAll(kind, "-", "")
	kind = strings.ReplaceAll(kind, "_", "")
	return kind
}

func decodeBuilderFullUIRequest(raw json.RawMessage) (builderFullUIRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return builderFullUIRequest{}, err
	}
	request := builderFullUIRequest{Raw: append(json.RawMessage(nil), raw...)}
	for field, target := range map[string]*string{"kind": &request.Kind, "method": &request.Method, "endpoint": &request.Endpoint} {
		if value := copyRawField(fields, field); len(value) > 0 {
			if err := json.Unmarshal(value, target); err != nil {
				return builderFullUIRequest{}, fmt.Errorf("request.%s: %w", field, err)
			}
		}
	}
	if !hasRawField(fields, "bodyRaw") {
		return builderFullUIRequest{}, fmt.Errorf("request.bodyRaw is required and must be null or a serialized JSON string")
	}
	request.BodyRaw = copyRawField(fields, "bodyRaw")
	if !hasRawField(fields, "body") {
		return builderFullUIRequest{}, fmt.Errorf("request.body is required and must be null or a JSON object")
	}
	request.Body = copyRawField(fields, "body")
	if request.Kind == "" || request.Method == "" || request.Endpoint == "" {
		return builderFullUIRequest{}, fmt.Errorf("captured request lacks kind, method, or endpoint")
	}
	var bodyValue any
	if err := json.Unmarshal(request.Body, &bodyValue); err != nil {
		return builderFullUIRequest{}, fmt.Errorf("request.body must be null or a JSON object: %w", err)
	}
	if bodyValue == nil {
		if !isJSONNullOrEmpty(request.BodyRaw) {
			return builderFullUIRequest{}, fmt.Errorf("request.body is null but request.bodyRaw is not null")
		}
		return request, nil
	}
	if _, ok := bodyValue.(map[string]any); !ok {
		return builderFullUIRequest{}, fmt.Errorf("request.body must be null or a JSON object")
	}
	rawBody, hasBody, err := builderFullUISerializedBody(request)
	if err != nil {
		return builderFullUIRequest{}, err
	}
	if !hasBody {
		return builderFullUIRequest{}, fmt.Errorf("request.body is an object but request.bodyRaw is null")
	}
	request.HasBody = true
	rawValue, err := decodeJSONValue(json.RawMessage(rawBody))
	if err != nil {
		return builderFullUIRequest{}, fmt.Errorf("decode request.bodyRaw: %w", err)
	}
	if _, ok := rawValue.(map[string]any); !ok {
		return builderFullUIRequest{}, fmt.Errorf("request.bodyRaw must serialize a JSON object when request.body is an object")
	}
	bodyValue, err = decodeJSONValue(request.Body)
	if err != nil {
		return builderFullUIRequest{}, fmt.Errorf("decode parsed body: %w", err)
	}
	if !reflect.DeepEqual(rawValue, bodyValue) {
		return builderFullUIRequest{}, fmt.Errorf("captured body and bodyRaw parse to different JSON values")
	}
	return request, nil
}

func decodeJSONValue(raw json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}

func builderFullUIGapReason(row builderFullUIInventoryRow) string {
	var expected struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(row.ActionExpectation, &expected)
	if expected.Reason == "" {
		_ = json.Unmarshal(row.Expectation, &expected)
	}
	if expected.Reason != "" {
		return expected.Reason
	}
	return "inventory marks this request checkpoint as a coverage gap"
}

func copyRawField(fields map[string]json.RawMessage, field string) json.RawMessage {
	if raw, ok := fields[field]; ok {
		return append(json.RawMessage(nil), raw...)
	}
	return nil
}

func decodeRawField(fields map[string]json.RawMessage, field string, target any) error {
	raw, ok := fields[field]
	if !ok {
		return fmt.Errorf("missing %s", field)
	}
	return json.Unmarshal(raw, target)
}

func hasRawField(fields map[string]json.RawMessage, field string) bool {
	_, ok := fields[field]
	return ok
}

func rawIntField(fields map[string]json.RawMessage, field string) (int, bool) {
	raw, ok := fields[field]
	if !ok {
		return 0, false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

func isJSONNullOrEmpty(raw json.RawMessage) bool {
	return len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
