package lifecycle

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

// This replay consumes the state-space inventory and the exact UI observation
// capture as separate inputs. It intentionally keys both by the stable
// (caseId, entryId, actionIndex) tuple, never by a lossy affordance label.
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
	if err := writeBuilderFullUIObservations(goObservationsPath, outputHeader, observations); err != nil {
		t.Fatalf("write Go observation artifact: %v", err)
	}

	if counts.Gap+counts.SetupFailure != 0 {
		t.Errorf("Builder full-UI replay found adapter gaps/setup failures: gap=%d setup=%d; complete observations written to %s", counts.Gap, counts.SetupFailure, goObservationsPath)
	}
}

func TestBuilderFullUIRegisteredGroupFilterDeriveFixtureCompiles(t *testing.T) {
	fixture, workspace, provenance, err := builderFullUIGroupFilterDeriveFixture()
	if err != nil {
		t.Fatal(err)
	}
	provenanceObject := rawObject(provenance)
	if provenanceObject == nil {
		t.Fatal("generated-schema fixture provenance is missing")
	}
	var source string
	_ = json.Unmarshal(provenanceObject["source"], &source)
	if source != "backend_generated_fhir_schema_fixture" {
		t.Fatalf("fixture provenance source=%q", source)
	}
	harness, err := newBuilderFullUIHarness(fixture, workspace)
	if err != nil {
		t.Fatalf("real base reconcile and compile: %v", err)
	}
	for _, check := range []struct{ stepID, columnID, wantType string }{
		{"group-step", "gender-group", "string"}, {"group-step", "row-count", "integer"},
		{"filter-step", "gender-group", "string"}, {"filter-step", "row-count", "integer"},
		{"derive-step", "adjusted", "integer"},
	} {
		if got := traceReceiptStageColumnType(harness.Base, check.stepID, check.columnID); got != check.wantType {
			t.Errorf("base compiler stage %s column %s type=%q, want %q", check.stepID, check.columnID, got, check.wantType)
		}
	}
	if harness.Store.saveDraftCalls != 0 {
		t.Fatalf("base compile saved the draft %d times", harness.Store.saveDraftCalls)
	}
	if fixture.CapabilityRequest == nil {
		t.Fatal("registered fixture capability request is missing")
	}
	capabilities, err := harness.Service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
		Project: fixture.Project, ExplorerID: fixture.ExplorerID, SnapshotToken: fixture.Snapshot.Token,
		ExpectedDraftVersion: fixture.AcceptedVersion, ExpectedDraftDigest: harness.Store.created.DraftDigest,
		OutputID: fixture.OutputID, StageID: fixture.CapabilityRequest.StageID, GroupStepID: fixture.CapabilityRequest.GroupStepID,
	})
	if err != nil {
		t.Fatalf("actual group-stage capability discovery: %v", err)
	}
	if capabilities.SelectedStage.ID != recipe.ConstructionSourceProjectionID {
		t.Fatalf("actual capability selected stage=%q, want exact Group input stage %q", capabilities.SelectedStage.ID, recipe.ConstructionSourceProjectionID)
	}
}

func TestBuilderFullUIRegisteredSeedProfilesPrepareWithRealCompilerAndCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		entryID  string
		seedKind string
		profile  string
		stageID  string
		steps    []map[string]any
	}{
		{name: "scalar FILTER source workspace", entryID: "builder.filter_rows.scalar_condition", seedKind: "rooted_table_filter", profile: "rooted-source", stageID: recipe.ConstructionSourceProjectionID},
		{name: "scalar DERIVE source workspace", entryID: "builder.calculate.derive", seedKind: "rooted_table_calculate", profile: "rooted-source", stageID: recipe.ConstructionSourceProjectionID},
		{name: "saved construction workspace", entryID: "builder.construction_proposal_apply_cancel_retry", seedKind: "construction_proposal_lifecycle_initial_state", profile: "saved-construction", stageID: "saved-filter-step", steps: []map[string]any{{"id": "saved-filter-step", "operation": map[string]any{"kind": "FILTER"}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stateID := "state:builder-full-ui-" + strings.ReplaceAll(test.name, " ", "-")
			modelState := builderFullUITestModelState(t, stateID, test.profile, test.stageID, test.steps)
			seedJSON, err := json.Marshal(map[string]any{"id": "fixture.seed", "kind": test.seedKind})
			if err != nil {
				t.Fatal(err)
			}
			entry := builderFullUIInventoryManifestEntry{ID: test.entryID, Seed: seedJSON}
			row := builderFullUIInventoryRow{CaseID: "fixture-case", EntryID: entry.ID, ActionIndex: 0, RequestMode: "emitted", StateRefBefore: stateID, Before: modelState}
			prepared, err := prepareBuilderFullUISeed(row, entry)
			if err != nil {
				t.Fatalf("prepare registered %s seed: %v (%s)", test.seedKind, err, prepared.Error)
			}
			if prepared.Status != "PREPARED" || prepared.CompileReceiptID == "" || len(prepared.AcceptedWorkspace) == 0 || len(prepared.Capabilities) == 0 {
				t.Fatalf("registered seed did not return compiled workspace and actual capability response: %#v", prepared)
			}
			harness, err := newBuilderFullUIHarnessFromPrepared(prepared)
			if err != nil {
				t.Fatalf("recreate lifecycle from prepared exact workspace: %v", err)
			}
			if harness.Base == nil || harness.Store.saveDraftCalls != 0 {
				t.Fatalf("prepared fixture base receipt=%#v saves=%d", harness.Base, harness.Store.saveDraftCalls)
			}
			var binding builderFullUIFixtureBindings
			if err := json.Unmarshal(prepared.FixtureBindings, &binding); err != nil {
				t.Fatal(err)
			}
			if test.profile == "rooted-source" {
				if binding.Columns["model_column_string_required"].ActualColumnID != "patient-id" ||
					binding.Columns["model_column_string_optional"].ActualColumnID != "gender-id" ||
					binding.Columns["model_column_boolean_optional"].ActualColumnID != "active-id" ||
					binding.Columns["model_column_integer_optional"].ActualColumnID != "age-id" ||
					binding.Columns["model_column_date_optional"].ActualColumnID != "birth-date-id" {
					t.Fatalf("source-backed binding did not preserve exact generated Patient fields: %#v", binding)
				}
				if len(binding.UnboundColumns) != 0 {
					t.Fatalf("supported Patient scalar classes unexpectedly remain unbound: %#v", binding.UnboundColumns)
				}
			} else if len(binding.Columns) != 0 || len(binding.UnboundColumns) != 5 {
				t.Fatalf("saved-stage source bindings should remain visibly unbound when output IDs are reused across stages: %#v", binding)
			}
		})
	}
}

func TestBuilderFullUIScalarFilterAndDeriveRequestsReplayThroughRealService(t *testing.T) {
	tests := []struct {
		name      string
		entryID   string
		seedKind  string
		candidate authoringv2.Construction
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

func builderFullUIScalarFilterCandidate() authoringv2.Construction {
	value := "x"
	return authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "filter-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: "gender-id", Operator: authoringv2.ConstructionFilterEquals,
			Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterString, String: &value}},
		}},
		Outputs: []authoringv2.StageColumn{{ID: "gender-id", Name: "gender", Label: "Gender", Type: "string"}, {ID: "age-id", Name: "age", Label: "Age", Type: "integer"}},
	}}}
}

func builderFullUIScalarDeriveCandidate() authoringv2.Construction {
	one := int64(1)
	return authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "derive-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID: "derive-step", OutputColumnID: "derived-age", Operation: authoringv2.ConstructionDerivedAdd,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "age-id"},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &one}},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}},
		Outputs: []authoringv2.StageColumn{{ID: "gender-id", Name: "gender", Label: "Gender", Type: "string"}, {ID: "age-id", Name: "age", Label: "Age", Type: "integer"}, {ID: "derived-age", Name: "derived_age", Label: "Derived age", Type: "integer"}},
	}}}
}

func TestBuilderFullUIRowDefinitionProposalDispatchUsesRealService(t *testing.T) {
	stateID := "state:builder-full-ui-row-definition"
	modelState := builderFullUITestModelState(t, stateID, "rooted-source", recipe.ConstructionSourceProjectionID, nil)
	seedJSON, err := json.Marshal(map[string]any{"id": "fixture.row-definition.seed", "kind": "rooted_table_row_definition"})
	if err != nil {
		t.Fatal(err)
	}
	row := builderFullUIInventoryRow{CaseID: "row-definition-case", EntryID: "builder.row_definition.expanded_source_rows", ActionIndex: 0, RequestMode: "emitted", StateRefBefore: stateID, Before: modelState}
	prepared, err := prepareBuilderFullUISeed(row, builderFullUIInventoryManifestEntry{ID: row.EntryID, Seed: seedJSON})
	if err != nil {
		t.Fatalf("prepare row-definition seed: %v (%s)", err, prepared.Error)
	}
	harness, err := newBuilderFullUIHarnessFromPrepared(prepared)
	if err != nil {
		t.Fatal(err)
	}
	choices, err := harness.Service.ListRowDefinitionChoices(context.Background(), RowDefinitionChoicesRequest{
		Project: prepared.Project, ExplorerID: prepared.ExplorerID, SnapshotToken: prepared.SnapshotToken, OutputID: prepared.OutputID,
	})
	if err != nil {
		t.Fatalf("actual ListRowDefinitionChoices: %v", err)
	}
	var expandedID string
	for _, choice := range choices.Choices {
		if choice.Kind == RowChoiceExpanded {
			expandedID = choice.ChoiceID
			break
		}
	}
	if expandedID == "" {
		t.Fatalf("actual row-choice resolver returned no expanded choice: %#v", choices)
	}
	body, err := json.Marshal(map[string]any{
		"snapshotToken": prepared.SnapshotToken, "expectedDraftVersion": prepared.AcceptedVersion,
		"expectedDraftDigest": prepared.AcceptedDigest, "outputId": prepared.OutputID,
		"selection": RowDefinitionSelection{Kind: RowDefinitionSelectionExpanded, Expanded: &ExpandedSelection{RowChoiceID: expandedID, EmptyCollectionPolicy: authoringv2.EmptyCollectionPreserveParent}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := builderFullUIRequest{Kind: "rowDefinitionProposal", Method: "POST", Endpoint: "/row-definition-proposals", Body: body, BodyRaw: mustJSONRawString(t, body), HasBody: true}
	actualValue, layers, err := executeBuilderFullUIRequest(row, request, prepared, harness)
	if err != nil {
		t.Fatalf("dispatch exact row-definition request through Service.ProposeRowDefinition: %v", err)
	}
	actual, ok := actualValue.(builderFullUIActual)
	if !ok || actual.RowDefinitionProposal == nil || actual.RowDefinitionProposal.ProposalID == "" || actual.RowDefinitionProposal.Mode != RowDefinitionSelectionExpanded {
		t.Fatalf("actual row-definition proposal response disposition=%q error=%#v proposal=%#v", actual.Disposition, actual.ServiceError, actual.RowDefinitionProposal)
	}
	if !reflect.DeepEqual(layers, []string{"lifecycle_analyzer_compiler", "preview_orchestration"}) || harness.Store.saveDraftCalls != 0 {
		t.Fatalf("row-definition request layers=%v saves=%d, want lifecycle+preview and no persistence", layers, harness.Store.saveDraftCalls)
	}
}

func TestBuilderFullUIEmptyRootCreationDispatchUsesRealService(t *testing.T) {
	stateID := "state:builder-full-ui-empty-root"
	modelState := builderFullUITestRootModelState(t, stateID)
	seedJSON, err := json.Marshal(map[string]any{"id": "fixture.first-table.seed", "kind": "first_table_creation_initial_state"})
	if err != nil {
		t.Fatal(err)
	}
	row := builderFullUIInventoryRow{CaseID: "first-table-case", EntryID: "builder.first_table_from_root", ActionIndex: 0, RequestMode: "emitted", StateRefBefore: stateID, Before: modelState}
	prepared, err := prepareBuilderFullUISeed(row, builderFullUIInventoryManifestEntry{ID: row.EntryID, Seed: seedJSON})
	if err != nil {
		t.Fatalf("prepare empty-root seed: %v (%s)", err, prepared.Error)
	}
	if prepared.Status != "PREPARED" || !isJSONNullOrEmpty(prepared.AcceptedWorkspace) || prepared.AcceptedVersion != 0 || prepared.OutputID != "" {
		t.Fatalf("root-uninitialized seed fabricated saved workspace identity: %#v", prepared)
	}
	var builderState authoringv2.BuilderState
	if err := json.Unmarshal(prepared.BuilderState, &builderState); err != nil {
		t.Fatal(err)
	}
	if builderState.LifecycleState != authoringv2.LifecycleNew || builderState.Workspace != nil {
		t.Fatalf("actual Builder root state=%#v, want new lifecycle without workspace", builderState)
	}
	harness, err := newBuilderFullUIHarnessFromPrepared(prepared)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(authoringv2.ApplyCommandsRequest{
		CommandID: "create-first-table", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: prepared.SnapshotToken, ExpectedDraftVersion: 0,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandCreateTable, Title: "Patients", RootNodeID: "node-patient"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := builderFullUIRequest{Kind: "commands", Method: "POST", Endpoint: "/commands", Body: body, BodyRaw: mustJSONRawString(t, body), HasBody: true}
	actualValue, layers, err := executeBuilderFullUIRequest(row, request, prepared, harness)
	if err != nil {
		t.Fatalf("dispatch CREATE_TABLE through actual Service.ApplyCommands: %v", err)
	}
	actual, ok := actualValue.(builderFullUIActual)
	if !ok || actual.Applied == nil || len(actual.Applied.Workspace.Documents) != 1 || len(actual.Applied.Workspace.Tabs) != 1 {
		t.Fatalf("actual first-table command response=%#v", actualValue)
	}
	if harness.Store.saveDraftCalls != 1 || harness.Store.created.DraftVersion != 1 || len(harness.Store.created.DraftConfig) == 0 || !reflect.DeepEqual(layers, []string{"lifecycle_analyzer_compiler", "fake_store_persistence"}) {
		t.Fatalf("actual root creation save/layers=%d/%v version=%d workspaceBytes=%d", harness.Store.saveDraftCalls, layers, harness.Store.created.DraftVersion, len(harness.Store.created.DraftConfig))
	}
}

func builderFullUITestModelState(t *testing.T, stateID, profile, stageID string, steps []map[string]any) json.RawMessage {
	t.Helper()
	stageOperation := "SOURCE_PROJECTION"
	if stageID != recipe.ConstructionSourceProjectionID {
		stageOperation = "FILTER"
	}
	columns := []map[string]any{
		{"id": "model_column_string_required", "classId": "string.required.nonnullable.scalar", "logicalType": "string", "cardinality": "required_one"},
		{"id": "model_column_string_optional", "classId": "string.optional.nullable.scalar", "logicalType": "string", "cardinality": "optional_one"},
		{"id": "model_column_boolean_optional", "classId": "boolean.optional.nullable.scalar", "logicalType": "boolean", "cardinality": "optional_one"},
		{"id": "model_column_integer_optional", "classId": "integer.optional.nullable.scalar", "logicalType": "integer", "cardinality": "optional_one"},
		{"id": "model_column_date_optional", "classId": "date.optional.nullable.scalar", "logicalType": "date", "cardinality": "optional_one"},
	}
	state, err := json.Marshal(map[string]any{
		"stateId": stateID, "seedProfile": profile, "workspacePhase": "workspace-loaded",
		"acceptedWorkspace": map[string]any{
			"rootType":     "Patient",
			"identity":     map[string]any{"outputId": "model_output"},
			"construction": map[string]any{"steps": steps},
		},
		"selectedStage": map[string]any{"id": stageID, "operation": stageOperation},
		"columns":       columns,
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func builderFullUITestRootModelState(t *testing.T, stateID string) json.RawMessage {
	t.Helper()
	state, err := json.Marshal(map[string]any{
		"stateId": stateID, "seedProfile": "empty-root", "workspacePhase": "root-uninitialized",
		"acceptedWorkspace": nil, "selectedStage": nil, "columns": []any{},
		"rootContext": map[string]any{
			"project": "model_project", "explorerId": "model_explorer", "snapshotToken": "model_snapshot",
			"selectionRevisionId": "model_selection_revision", "draftVersion": 1, "draftDigest": "model_digest",
			"catalogSnapshot": nil,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
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
	// This backend-only diagnostic constructs its own candidate. It proves the
	// real lifecycle/compiler path, but is not frontend payload evidence; the
	// artifact replay below consumes the mounted editor's raw request unchanged.
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
	candidate.Steps[0].Operation.Group.Aggregates[0] = authoringv2.ConstructionGroupAggregate{
		Operation: authoringv2.ConstructionGroupMean, InputColumnID: "age-id", OutputColumnID: "row-count",
	}
	proposal, err := harness.Service.ProposeConstruction(context.Background(), ConstructionProposalRequest{
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
	compiledColumns := make(map[string]explorer.ReceiptCompiledOutputColumn)
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
	preview, err := harness.Service.Preview(context.Background(), PreviewRequest{
		Project: fixture.Project, ExplorerID: fixture.ExplorerID, ReceiptID: proposal.ProposalID, OutputID: fixture.OutputID,
		SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		},
	})
	if err != nil || preview.Receipt == nil || preview.Receipt.ID != proposal.ProposalID {
		t.Fatalf("actual proposal preview receipt=%#v err=%v", preview.Receipt, err)
	}
	apply, err := harness.Service.ApplyCommands(context.Background(), fixture.Project, fixture.ExplorerID, authoringv2.ApplyCommandsRequest{
		CommandID: "builder-full-ui-apply-mean", SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		SnapshotToken: fixture.Snapshot.Token, ExpectedDraftVersion: fixture.AcceptedVersion, ExpectedDraftDigest: acceptedDigest,
		Commands: []authoringv2.Command{{Type: authoringv2.CommandApplyConstructionProposal, OutputID: fixture.OutputID, ProposalID: proposal.ProposalID}},
	}, "builder-full-ui-replay")
	if err != nil {
		t.Fatalf("actual ApplyCommands for reviewed proposal: %v", err)
	}
	if harness.Store.saveDraftCalls != 1 || harness.Store.created.DraftVersion != fixture.AcceptedVersion+1 || harness.Store.created.DraftDigest != proposal.CandidateWorkspaceDigest {
		t.Fatalf("actual apply acknowledgement did not save once and advance the reviewed draft: apply=%#v saves=%d storeVersion=%d storeDigest=%q proposalDigest=%q", apply, harness.Store.saveDraftCalls, harness.Store.created.DraftVersion, harness.Store.created.DraftDigest, proposal.CandidateWorkspaceDigest)
	}
	persisted, err := authoringv2.DecodeWorkspace(harness.Store.created.DraftConfig)
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

func (r builderFullUIInventoryRow) key() builderFullUIKey {
	return builderFullUIKey{CaseID: r.CaseID, EntryID: r.EntryID, ActionIndex: r.ActionIndex}
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

func (s builderFullUISeed) key() builderFullUIKey {
	return builderFullUIKey{CaseID: s.CaseID, EntryID: s.EntryID, ActionIndex: s.ActionIndex}
}

type builderFullUICapabilityFixture struct {
	Project           string                          `json:"project"`
	ExplorerID        string                          `json:"explorerId"`
	AcceptedVersion   int64                           `json:"acceptedVersion"`
	OutputID          string                          `json:"outputId"`
	Snapshot          capability.Snapshot             `json:"snapshot"`
	Catalog           authoringv2.CatalogSnapshot     `json:"catalog"`
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
	Workspace    authoringv2.Workspace
	Fixture      builderFullUICapabilityFixture
	Base         *explorer.CompilationReceipt
	Capabilities ConstructionCapabilitiesResponse
	LastPreview  *PreviewResult
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

func prepareBuilderFullUISeeds(t *testing.T, inventoryHeader json.RawMessage, inventory []builderFullUIInventoryRow, guards []builderFullUIGuardRow, guardStatesResolved int, manifest map[string]builderFullUIInventoryManifestEntry, preparedPath, modelJSONLSHA, manifestSHA, modelStatesSHA string) {
	t.Helper()
	header := builderFullUIPrepareHeader{RecordType: "header", SchemaVersion: 1, Phase: "prepare", InventorySHA256: builderFullUIInventorySourceSHA(inventoryHeader), InventoryManifestSHA256: manifestSHA, ModelStatesSHA256: modelStatesSHA, ModelJSONLSHA256: modelJSONLSHA, SeedCount: len(inventory), GuardCount: len(guards), GuardStatesResolved: guardStatesResolved}
	rows := make([]builderFullUISeed, 0, len(inventory))
	for _, source := range inventory {
		row := builderFullUISeed{RecordType: "prepared-seed", SchemaVersion: 1, CaseID: source.CaseID, EntryID: source.EntryID, ActionIndex: source.ActionIndex, RequestMode: source.RequestMode}
		manifestEntry, found := manifest[source.EntryID]
		if !found {
			row.Status, row.Error = "GAP", "normalized inventory has no affordance with this exact entryId"
			header.GapCount++
			rows = append(rows, row)
			continue
		}
		setBuilderFullUISeedContext(&row, source, manifestEntry)
		switch source.RequestMode {
		case "gap":
			row.Status, row.Error = "GAP", builderFullUIGapReason(source)
			header.GapCount++
		case "emitted", "blocked", "local":
			var err error
			fixtureRow, err := prepareBuilderFullUISeed(source, manifestEntry)
			if err != nil && fixtureRow.Error == "" {
				fixtureRow.Error = err.Error()
			}
			row = mergeBuilderFullUISeedContext(fixtureRow, row)
			if source.RequestMode != "emitted" && row.Status == "PREPARED" {
				row.Status = "PREPARED_UI_ONLY"
			}
			switch row.Status {
			case "PREPARED":
				header.PreparedCount++
			case "PREPARED_UI_ONLY":
				if source.RequestMode == "blocked" {
					header.BlockedCount++
				} else {
					header.LocalCount++
				}
			case "GAP":
				header.GapCount++
			default:
				row.Status = "SETUP_FAILURE"
				header.SetupFailureCount++
			}
		default:
			row.Status, row.Error = "SETUP_FAILURE", "unsupported requestMode"
			header.SetupFailureCount++
		}
		rows = append(rows, row)
	}
	if err := writeBuilderFullUIPreparedSeeds(preparedPath, header, rows); err != nil {
		t.Fatalf("write prepared seed artifact: %v", err)
	}
	fmt.Printf("BUILDER_FULL_UI_PREPARE path=%s rows=%d prepared=%d blocked=%d local=%d gap=%d setupFailure=%d inventorySha256=%s inventoryManifestSha256=%s modelStatesSha256=%s modelJsonlSha256=%s\n",
		preparedPath, len(rows), header.PreparedCount, header.BlockedCount, header.LocalCount, header.GapCount, header.SetupFailureCount, header.InventorySHA256, header.InventoryManifestSHA256, header.ModelStatesSHA256, header.ModelJSONLSHA256)
	if len(rows) != len(inventory) || header.SetupFailureCount != 0 {
		t.Errorf("Builder full-UI prepare has missing rows or setup failures: rows=%d/%d setupFailure=%d; artifact=%s", len(rows), len(inventory), header.SetupFailureCount, preparedPath)
	}
}

func setBuilderFullUISeedContext(target *builderFullUISeed, source builderFullUIInventoryRow, manifestEntry builderFullUIInventoryManifestEntry) {
	target.CaseID, target.EntryID, target.ActionIndex = source.CaseID, source.EntryID, source.ActionIndex
	target.RequestMode = source.RequestMode
	target.StateRefBefore = source.StateRefBefore
	target.ModelSeed = append(json.RawMessage(nil), manifestEntry.Seed...)
	var descriptor builderFullUIManifestSeed
	if err := json.Unmarshal(manifestEntry.Seed, &descriptor); err != nil {
		target.StateResolveError = "decode normalized inventory seed: " + err.Error()
	} else {
		target.SeedID, target.SeedKind = descriptor.ID, descriptor.Kind
	}
	if source.StateResolveError != "" {
		target.StateResolveError = source.StateResolveError
	}
	target.ModelStateID = source.StateRefBefore
	target.ModelState = append(json.RawMessage(nil), source.Before...)
	var abstractState struct {
		SeedProfile    string `json:"seedProfile"`
		WorkspacePhase string `json:"workspacePhase"`
	}
	if len(source.Before) != 0 && json.Unmarshal(source.Before, &abstractState) == nil {
		target.SeedProfile = abstractState.SeedProfile
		target.WorkspacePhase = abstractState.WorkspacePhase
	}
	provenance := source.CapabilityProvenance
	if len(provenance) == 0 {
		provenance = manifestEntry.CapabilityProvenance
	}
	if len(provenance) == 0 {
		var descriptor builderFullUIManifestSeed
		if json.Unmarshal(manifestEntry.Seed, &descriptor) == nil {
			provenance = descriptor.Provenance
		}
	}
	target.CapabilityProvenance = append(json.RawMessage(nil), provenance...)
}

func mergeBuilderFullUISeedContext(fixture, context builderFullUISeed) builderFullUISeed {
	fixture.RecordType, fixture.SchemaVersion = "prepared-seed", 1
	fixture.CaseID, fixture.EntryID, fixture.ActionIndex = context.CaseID, context.EntryID, context.ActionIndex
	fixture.RequestMode = context.RequestMode
	fixture.SeedID, fixture.SeedKind = context.SeedID, context.SeedKind
	fixture.SeedProfile = context.SeedProfile
	fixture.WorkspacePhase = context.WorkspacePhase
	fixture.ModelSeed = append(json.RawMessage(nil), context.ModelSeed...)
	fixture.ModelStateID = context.ModelStateID
	fixture.ModelState = append(json.RawMessage(nil), context.ModelState...)
	fixture.StateRefBefore = context.StateRefBefore
	if len(fixture.CapabilityProvenance) == 0 {
		fixture.CapabilityProvenance = append(json.RawMessage(nil), context.CapabilityProvenance...)
	}
	if context.StateResolveError != "" {
		fixture.Status = "SETUP_FAILURE"
		fixture.Error = context.StateResolveError
	}
	return fixture
}

func prepareBuilderFullUISeed(source builderFullUIInventoryRow, manifestEntry builderFullUIInventoryManifestEntry) (builderFullUISeed, error) {
	seed := builderFullUISeed{RecordType: "prepared-seed", SchemaVersion: 1, CaseID: source.CaseID, EntryID: source.EntryID, ActionIndex: source.ActionIndex, Status: "SETUP_FAILURE"}
	setBuilderFullUISeedContext(&seed, source, manifestEntry)
	seed.ModelSeed = append(json.RawMessage(nil), manifestEntry.Seed...)
	var descriptor builderFullUIManifestSeed
	if err := json.Unmarshal(manifestEntry.Seed, &descriptor); err != nil {
		seed.Error = "decode abstract seed descriptor: " + err.Error()
		return seed, err
	}
	seed.SeedID, seed.SeedKind = descriptor.ID, descriptor.Kind
	if source.StateResolveError != "" {
		seed.Status, seed.Error = "SETUP_FAILURE", source.StateResolveError
		return seed, fmt.Errorf("resolve exact ModelState before ref: %s", source.StateResolveError)
	}
	if source.StateRefBefore == "" || len(source.Before) == 0 {
		seed.Status, seed.Error = "SETUP_FAILURE", "request row is missing its exact stateRefs.before ModelState"
		return seed, fmt.Errorf("missing exact stateRefs.before ModelState")
	}
	if descriptor.ID == "" || descriptor.Kind == "" {
		seed.Status, seed.Error = "GAP", "normalized inventory seed is missing id or kind"
		return seed, nil
	}
	modelState, err := decodeBuilderFullUIAbstractState(source)
	if err != nil {
		seed.Status, seed.Error = "SETUP_FAILURE", "decode exact before ModelState: "+err.Error()
		return seed, err
	}
	fixture, workspace, provenance, err := builderFullUIRegisteredSeedFixture(source.EntryID, descriptor.Kind, modelState)
	if err != nil {
		if strings.HasPrefix(err.Error(), "GAP: ") {
			seed.Status, seed.Error = "GAP", strings.TrimPrefix(err.Error(), "GAP: ")
			return seed, nil
		}
		seed.Error = "construct registered fixture: " + err.Error()
		return seed, err
	}
	harness, err := newBuilderFullUIHarness(fixture, workspace)
	if err != nil {
		seed.Error = "real accepted-workspace reconcile/compile: " + err.Error()
		return seed, err
	}
	capRequest := fixture.CapabilityRequest
	if capRequest == nil && !fixture.NoSavedWorkspace {
		seed.Error = "registered fixture has no stage capability request"
		return seed, fmt.Errorf("%s", seed.Error)
	}
	var capabilities ConstructionCapabilitiesResponse
	if capRequest != nil {
		capabilities, err = harness.Service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
			Project: fixture.Project, ExplorerID: fixture.ExplorerID, SnapshotToken: fixture.Snapshot.Token,
			ExpectedDraftVersion: fixture.AcceptedVersion, ExpectedDraftDigest: harness.Store.created.DraftDigest,
			OutputID: fixture.OutputID, StageID: capRequest.StageID, GroupStepID: capRequest.GroupStepID,
		})
		if err != nil {
			seed.Error = "actual GetConstructionCapabilities failed: " + err.Error()
			return seed, err
		}
	}
	state, err := harness.Service.Builder(context.Background(), BuilderRequest{Project: fixture.Project, ExplorerID: fixture.ExplorerID})
	if err != nil {
		seed.Error = "actual Builder state failed: " + err.Error()
		return seed, err
	}
	var workspaceJSON json.RawMessage
	if fixture.NoSavedWorkspace {
		workspaceJSON = json.RawMessage("null")
	} else {
		encoded, encodeErr := workspace.CanonicalJSON()
		if encodeErr != nil {
			seed.Error = "encode accepted workspace: " + encodeErr.Error()
			return seed, encodeErr
		}
		workspaceJSON = encoded
	}
	seed.Project, seed.ExplorerID = fixture.Project, fixture.ExplorerID
	seed.AcceptedVersion, seed.AcceptedDigest = fixture.AcceptedVersion, harness.Store.created.DraftDigest
	seed.SnapshotToken, seed.OutputID = fixture.Snapshot.Token, fixture.OutputID
	if harness.Base != nil {
		seed.BaseReceiptID = harness.Base.ID
	}
	seed.AcceptedWorkspace = workspaceJSON
	seed.CapabilityRequest, _ = json.Marshal(capRequest)
	seed.CapabilityProvenance = provenance
	seed.BuilderState, err = json.Marshal(state)
	if err != nil {
		seed.Error = "encode actual Builder state: " + err.Error()
		return seed, err
	}
	seed.Catalog, err = json.Marshal(fixture.Catalog)
	if err != nil {
		seed.Error = "encode fixture catalog: " + err.Error()
		return seed, err
	}
	if capRequest != nil {
		seed.Capabilities, err = json.Marshal(capabilities)
		if err != nil {
			seed.Error = "encode actual capability response: " + err.Error()
			return seed, err
		}
	}
	seed.CapabilityFixture, err = json.Marshal(fixture)
	if err != nil {
		seed.Error = "encode fixture identity: " + err.Error()
		return seed, err
	}
	if !fixture.NoSavedWorkspace {
		bindings, bindingErr := builderFullUIFixtureColumnBindings(source.Before, workspace, capabilities, fixture)
		if bindingErr != nil {
			seed.Error = "resolve source-backed abstract-to-real column bindings: " + bindingErr.Error()
			return seed, bindingErr
		}
		seed.FixtureBindings, err = json.Marshal(bindings)
		if err != nil {
			seed.Error = "encode fixture column bindings: " + err.Error()
			return seed, err
		}
	}
	if harness.Base != nil {
		seed.CompileReceiptID = harness.Base.ID
	}
	seed.Status = "PREPARED"
	return seed, nil
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

func builderFullUIRegisteredSeedFixture(entryID, seedKind string, state builderFullUIAbstractState) (builderFullUICapabilityFixture, authoringv2.Workspace, json.RawMessage, error) {
	if seedKind == "first_table_creation_initial_state" {
		if entryID != "builder.first_table_from_root" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: empty-root fixture is only registered for exact entryId %q", entryID)
		}
		if state.SeedProfile != "empty-root" || state.WorkspacePhase != "root-uninitialized" || state.AcceptedWorkspace != nil || state.RootContext == nil {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: first-table seed requires exact empty-root ModelState with rootContext")
		}
		return builderFullUIEmptyRootFixture()
	}
	if state.WorkspacePhase != "workspace-loaded" || state.AcceptedWorkspace == nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: seed kind %q needs a workspace-loaded before state; got phase=%q", seedKind, state.WorkspacePhase)
	}
	switch seedKind {
	case "builder.group_filter_derive.edit_saved_group":
		steps := state.AcceptedWorkspace.Construction.Steps
		if state.SeedProfile != "group-filter-derive" || state.AcceptedWorkspace.RootType != "Patient" || len(steps) != 3 || steps[0].ID != "group-step" || steps[0].Operation.Kind != "GROUP" || steps[1].ID != "filter-step" || steps[1].Operation.Kind != "FILTER" || steps[2].ID != "derive-step" || steps[2].Operation.Kind != "DERIVE" || state.SelectedStage == nil || state.SelectedStage.ID != "derive-step" || state.SelectedStage.Operation != "DERIVE" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: seed kind %q is not paired with the exact group-filter-derive ModelState profile", seedKind)
		}
		return builderFullUIGroupFilterDeriveFixture()
	case "rooted_table_filter":
		if entryID != "builder.filter_rows.scalar_condition" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: scalar FILTER fixture does not cover exact entryId %q", entryID)
		}
		if state.SeedProfile != "rooted-source" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID || state.SelectedStage.Operation != "SOURCE_PROJECTION" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: entryId %q requires the rooted-source empty-construction state; profile=%q stage=%#v", entryID, state.SeedProfile, state.SelectedStage)
		}
		return builderFullUIScalarFixture()
	case "rooted_table_calculate":
		if entryID != "builder.calculate.derive" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: scalar DERIVE fixture does not cover exact entryId %q", entryID)
		}
		if state.SeedProfile != "rooted-source" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID || state.SelectedStage.Operation != "SOURCE_PROJECTION" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: entryId %q requires the rooted-source empty-construction state", entryID)
		}
		return builderFullUIScalarFixture()
	case "rooted_table_add_columns":
		if entryID != "builder.add_columns.catalog_field" && entryID != "builder.add_columns.source_selection" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: source projection fixture does not cover exact add-column entryId %q", entryID)
		}
		if state.SeedProfile != "rooted-source" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: entryId %q requires the rooted-source empty-construction state", entryID)
		}
		return builderFullUIScalarFixture()
	case "construction_proposal_lifecycle_initial_state", "construction_step_editor_routing_initial_state", "automatic_preview_lifecycle_initial_state", "automatic_preview_refresh_and_recovery", "one_step_draft_restore_initial_state", "row_root_change_review_initial_state", "server_draft_and_reconciliation_initial_state":
		if seedKind != "construction_proposal_lifecycle_initial_state" || entryID != "builder.construction_proposal_apply_cancel_retry" || state.SeedProfile != "saved-construction" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 1 || state.AcceptedWorkspace.Construction.Steps[0].ID != "saved-filter-step" || state.AcceptedWorkspace.Construction.Steps[0].Operation.Kind != "FILTER" || state.SelectedStage == nil || state.SelectedStage.ID != "saved-filter-step" || state.SelectedStage.Operation != "FILTER" {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: saved FILTER lifecycle fixture does not cover exact entryId %q and before-state", entryID)
		}
		return builderFullUISavedFilterFixture()
	case "rooted_table_row_definition":
		if entryID != "builder.row_definition.expanded_source_rows" || state.SeedProfile != "rooted-source" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: expanded row-definition fixture does not cover exact entryId %q and before-state", entryID)
		}
		return builderFullUIRowDefinitionFixture()
	case "two_table_combine":
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: no catalog-consistent two-source workspace and combine receipt fixture is registered")
	default:
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("GAP: backend fixture registry has no entry for seed kind %q", seedKind)
	}
}

func builderFullUIFixtureColumnBindings(modelState json.RawMessage, workspace authoringv2.Workspace, capabilities ConstructionCapabilitiesResponse, fixture builderFullUICapabilityFixture) (builderFullUIFixtureBindings, error) {
	var state struct {
		Columns []struct {
			ID          string `json:"id"`
			ClassID     string `json:"classId"`
			LogicalType string `json:"logicalType"`
			Cardinality string `json:"cardinality"`
		} `json:"columns"`
	}
	if err := json.Unmarshal(modelState, &state); err != nil {
		return builderFullUIFixtureBindings{}, err
	}
	bindings := builderFullUIFixtureBindings{Columns: map[string]builderFullUIColumnBinding{}}
	workspaceIDs := map[string]int{}
	for _, doc := range workspace.Documents {
		for _, column := range doc.Columns {
			workspaceIDs[column.ColumnID]++
		}
		if doc.Construction != nil {
			for _, step := range doc.Construction.Steps {
				for _, column := range step.Outputs {
					workspaceIDs[column.ID]++
				}
			}
		}
	}
	capabilityIDs := map[string]int{}
	for _, column := range capabilities.SelectedStage.Columns {
		capabilityIDs[column.ID]++
	}
	actualByClass := map[string]struct{ id, path, logical, cardinality string }{
		"string.required.nonnullable.scalar": {id: "patient-id", path: "id", logical: "string", cardinality: "required_one"},
		"string.optional.nullable.scalar":    {id: "gender-id", path: "gender", logical: "string", cardinality: "optional_one"},
		"boolean.optional.nullable.scalar":   {id: "active-id", path: "active", logical: "boolean", cardinality: "optional_one"},
		"integer.optional.nullable.scalar":   {id: "age-id", path: "multipleBirthInteger", logical: "integer", cardinality: "optional_one"},
		"date.optional.nullable.scalar":      {id: "birth-date-id", path: "birthDate", logical: "date", cardinality: "optional_one"},
	}
	usedActualIDs := map[string]string{}
	for _, column := range state.Columns {
		if column.ID == "" || column.ClassID == "" {
			continue
		}
		source, supported := actualByClass[column.ClassID]
		if !supported || source.logical != column.LogicalType || source.cardinality != column.Cardinality {
			bindings.UnboundColumns = append(bindings.UnboundColumns, builderFullUIUnboundColumn{ModelColumnID: column.ID, ClassID: column.ClassID, Reason: "no unique generated Patient source with this exact logical type/cardinality is registered"})
			continue
		}
		if workspaceIDs[source.id] != 1 || capabilityIDs[source.id] != 1 {
			bindings.UnboundColumns = append(bindings.UnboundColumns, builderFullUIUnboundColumn{ModelColumnID: column.ID, ClassID: column.ClassID, Reason: fmt.Sprintf("generated source %q occurs workspace=%d capability=%d times, want exactly once", source.id, workspaceIDs[source.id], capabilityIDs[source.id])})
			continue
		}
		if priorModelColumn, used := usedActualIDs[source.id]; used {
			bindings.UnboundColumns = append(bindings.UnboundColumns, builderFullUIUnboundColumn{ModelColumnID: column.ID, ClassID: column.ClassID, Reason: fmt.Sprintf("generated source %q is already bound to abstract column %q", source.id, priorModelColumn)})
			continue
		}
		if fixture.Project == "" {
			return builderFullUIFixtureBindings{}, fmt.Errorf("fixture project identity is empty")
		}
		bindings.Columns[column.ID] = builderFullUIColumnBinding{ClassID: column.ClassID, ActualColumnID: source.id}
		usedActualIDs[source.id] = column.ID
	}
	return bindings, nil
}

func builderFullUIGroupFilterDeriveFixture() (builderFullUICapabilityFixture, authoringv2.Workspace, json.RawMessage, error) {
	const project, explorerID, outputID, nodeID = "HTAN_INT/BForePC", "patients", "patients", "node-patient"
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("generated schema index: %w", err)
	}
	gender, err := index.ResolveRowPath(fhirschema.DefinitionName("Patient"), "gender")
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	age, err := index.ResolveRowPath(fhirschema.DefinitionName("Patient"), "multipleBirthInteger")
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	if gender.Shape != fhirschema.RowPathScalar || gender.Cardinality != fhirschema.RowCardinalityOne || gender.FHIRType != "string" ||
		age.Shape != fhirschema.RowPathScalar || age.Cardinality != fhirschema.RowCardinalityOne || age.FHIRType != "integer" {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("unexpected generated Patient field facts: gender=%#v multipleBirthInteger=%#v", gender, age)
	}
	workspace := builderFullUIGroupFilterDeriveWorkspace()
	if err := workspace.Validate(); err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("workspace validation: %w", err)
	}
	scope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	scopeHash := sha256.Sum256([]byte(string(scope.Mode) + "\x00"))
	identity := capability.SnapshotIdentity{
		Project: project, Generation: "builder-full-ui-generated-schema-fixture-v1", AuthorizationScopeDigest: hex.EncodeToString(scopeHash[:]),
		SchemaDigest: "generated-fhir-schema-v1", ShapeDigest: "generated-fhir-shape-v1", ResourceInventoryDigest: "generated-fhir-resource-inventory-v1",
		RelationshipDigest: "generated-fhir-relationship-inventory-v1", FieldDigest: "generated-fhir-field-inventory-v1",
		ProtocolVersion: "builder-full-ui-fixture-v1", CompilerVersion: "current", TraversalPolicyVersion: "unbounded-v1", ProjectionPolicyVersion: "scalar-v1",
	}
	candidates := []capability.Candidate{}
	for _, field := range []struct{ id, path, logical string }{{"gender-id", "gender", "string"}, {"age-id", "multipleBirthInteger", "integer"}} {
		candidate := traceFieldCandidate("", field.id, field.path, field.logical)
		candidate.Observed, candidate.Populated = true, true
		candidate.ObservedDocumentCount = 1
		candidate.SuggestionsComplete = true
		candidates = append(candidates, candidate)
	}
	snapshot := capability.NewSnapshot(identity, capability.Policy{
		Route:      capability.RoutePolicy{Version: "unbounded-v1"},
		Projection: capability.ProjectionPolicy{Version: "scalar-v1", Modes: []capability.ProjectionMode{capability.ProjectionScalar}, SuggestionLimit: 100},
	}, capability.StatusReady, true, false,
		[]capability.Node{{ID: nodeID, ResourceType: "Patient", RowRootEligible: true, RowGrain: "patient", Populated: true, DocumentCount: 1, SupportedOperations: []capability.Operation{capability.OperationSelect}}},
		nil, candidates, nil)
	catalog := traceWorkspaceCatalog(snapshot, explorerID, workspace, nodeID)
	fixture := builderFullUICapabilityFixture{
		Project: project, ExplorerID: explorerID, AcceptedVersion: 1, OutputID: outputID, Snapshot: snapshot, Catalog: catalog,
		CapabilityRequest: &builderFullUICapabilityRequest{StageID: recipe.ConstructionSourceProjectionID, GroupStepID: "group-step"},
	}
	provenance, err := json.Marshal(map[string]any{
		"source": "backend_generated_fhir_schema_fixture", "resourceType": "Patient", "runtimeCatalog": false, "dataPopulationClaimed": false,
		"snapshotToken": snapshot.Token, "sourceGeneration": snapshot.Identity.Generation,
		"fields": []map[string]any{
			{"columnId": "gender-id", "canonicalPath": gender.CanonicalPath, "fhirType": gender.FHIRType, "shape": gender.Shape, "cardinality": gender.Cardinality},
			{"columnId": "age-id", "canonicalPath": age.CanonicalPath, "fhirType": age.FHIRType, "shape": age.Shape, "cardinality": age.Cardinality},
		},
	})
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	return fixture, workspace, provenance, nil
}

func builderFullUIScalarFixture() (builderFullUICapabilityFixture, authoringv2.Workspace, json.RawMessage, error) {
	fixture, _, provenance, err := builderFullUIGroupFilterDeriveFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("generated schema index: %w", err)
	}
	fields := []struct {
		id, path, wantType string
		wantCardinality    fhirschema.RowCardinality
	}{
		{"patient-id", "id", "string", fhirschema.RowCardinalityOne},
		{"active-id", "active", "boolean", fhirschema.RowCardinalityOne},
		{"birth-date-id", "birthDate", "date", fhirschema.RowCardinalityOne},
	}
	provenanceFields := []map[string]any{
		{"columnId": "gender-id", "canonicalPath": "gender", "fhirType": "string", "cardinality": fhirschema.RowCardinalityOne},
		{"columnId": "age-id", "canonicalPath": "multipleBirthInteger", "fhirType": "integer", "cardinality": fhirschema.RowCardinalityOne},
	}
	for _, field := range fields {
		facts, resolveErr := index.ResolveRowPath(fhirschema.DefinitionName("Patient"), field.path)
		if resolveErr != nil || facts.Shape != fhirschema.RowPathScalar || facts.Cardinality != field.wantCardinality || facts.FHIRType != field.wantType {
			return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("generated Patient source %s row facts=%#v err=%v, want scalar %s %s", field.path, facts, resolveErr, field.wantType, field.wantCardinality)
		}
		provenanceFields = append(provenanceFields, map[string]any{
			"columnId": field.id, "canonicalPath": facts.CanonicalPath, "fhirType": facts.FHIRType,
			"shape": facts.Shape, "cardinality": facts.Cardinality,
		})
		candidate := traceFieldCandidate(fixture.Snapshot.Token, field.id, field.path, field.wantType)
		candidate.NodeID, candidate.ResourceType = "node-patient", "Patient"
		candidate.Observed, candidate.Populated, candidate.ObservedDocumentCount = true, true, 1
		candidate.SuggestionsComplete = true
		if field.path == "id" {
			candidate.Cardinality = "required_one"
		}
		fixture.Snapshot.Candidates = append(fixture.Snapshot.Candidates, candidate)
	}
	fixture.Snapshot = capability.NewSnapshot(fixture.Snapshot.Identity, fixture.Snapshot.Policy, fixture.Snapshot.Status, fixture.Snapshot.Complete, fixture.Snapshot.Truncated, fixture.Snapshot.Nodes, fixture.Snapshot.Edges, fixture.Snapshot.Candidates, fixture.Snapshot.Diagnostics)
	workspace := builderFullUIScalarWorkspace()
	if err := workspace.Validate(); err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("scalar source workspace validation: %w", err)
	}
	fixture.Catalog = traceWorkspaceCatalog(fixture.Snapshot, fixture.ExplorerID, workspace, "node-patient")
	fixture.CapabilityRequest = &builderFullUICapabilityRequest{StageID: recipe.ConstructionSourceProjectionID}
	var provenanceValue map[string]any
	if err := json.Unmarshal(provenance, &provenanceValue); err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("decode generated schema provenance: %w", err)
	}
	provenanceValue["fields"] = provenanceFields
	provenance, err = json.Marshal(provenanceValue)
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	return fixture, workspace, provenance, nil
}

func builderFullUIEmptyRootFixture() (builderFullUICapabilityFixture, authoringv2.Workspace, json.RawMessage, error) {
	fixture, _, provenance, err := builderFullUIScalarFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	fixture.NoSavedWorkspace = true
	fixture.AcceptedVersion = 0
	fixture.OutputID = ""
	fixture.CapabilityRequest = nil
	empty := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer: authoringv2.ExplorerMetadata{Title: "Builder full UI replay"}, Documents: []authoringv2.Document{}, Tabs: []authoringv2.Tab{},
	}
	fixture.Catalog = traceWorkspaceCatalog(fixture.Snapshot, fixture.ExplorerID, empty, "node-patient")
	return fixture, authoringv2.Workspace{}, provenance, nil
}

func builderFullUISavedFilterFixture() (builderFullUICapabilityFixture, authoringv2.Workspace, json.RawMessage, error) {
	fixture, workspace, provenance, err := builderFullUIScalarFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	doc := &workspace.Documents[0]
	outputs := make([]authoringv2.StageColumn, 0, len(doc.Columns))
	for _, column := range doc.Columns {
		outputs = append(outputs, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	doc.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID: "saved-filter-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: "age-id", Operator: authoringv2.ConstructionFilterExists}},
		Outputs:   outputs,
	}}}
	if err := workspace.Validate(); err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("saved scalar FILTER workspace validation: %w", err)
	}
	fixture.Catalog = traceWorkspaceCatalog(fixture.Snapshot, fixture.ExplorerID, workspace, "node-patient")
	fixture.CapabilityRequest = &builderFullUICapabilityRequest{StageID: "saved-filter-step"}
	return fixture, workspace, provenance, nil
}

func builderFullUIRowDefinitionFixture() (builderFullUICapabilityFixture, authoringv2.Workspace, json.RawMessage, error) {
	fixture, workspace, _, err := builderFullUIScalarFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName("Patient"), "name[].family")
	if err != nil || facts.Shape != fhirschema.RowPathScalar || facts.Cardinality != fhirschema.RowCardinalityMany || facts.FHIRType != "string" {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, fmt.Errorf("generated Patient name[].family row facts=%#v err=%v", facts, err)
	}
	candidates := append([]capability.Candidate(nil), fixture.Snapshot.Candidates...)
	family := traceFieldCandidate(fixture.Snapshot.Token, "family-many", "name[].family", "string")
	family.Cardinality = "many"
	family.RepeatedBoundaries = []capability.RepeatedBoundary{{Path: "name[]", MaxItems: 2}}
	family.ProjectionModes = []capability.ProjectionMode{capability.ProjectionFirst, capability.ProjectionArray}
	candidates = append(candidates, family)
	fixture.Snapshot = capability.NewSnapshot(fixture.Snapshot.Identity, fixture.Snapshot.Policy, fixture.Snapshot.Status, fixture.Snapshot.Complete, fixture.Snapshot.Truncated, fixture.Snapshot.Nodes, fixture.Snapshot.Edges, candidates, fixture.Snapshot.Diagnostics)
	fixture.Catalog = traceWorkspaceCatalog(fixture.Snapshot, fixture.ExplorerID, workspace, "node-patient")
	provenanceValue, err := json.Marshal(map[string]any{
		"source": "backend_generated_fhir_schema_fixture", "resourceType": "Patient", "rowChoicePath": facts.CanonicalPath,
		"rowChoiceShape": facts.Shape, "rowChoiceCardinality": facts.Cardinality, "dataPopulationClaimed": false,
	})
	if err != nil {
		return builderFullUICapabilityFixture{}, authoringv2.Workspace{}, nil, err
	}
	return fixture, workspace, provenanceValue, nil
}

func builderFullUIScalarWorkspace() authoringv2.Workspace {
	workspace := builderFullUIGroupFilterDeriveWorkspace()
	document := &workspace.Documents[0]
	document.Construction = &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{}}
	document.Columns = append([]authoringv2.Column{
		{ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
		{ColumnID: "active-id", Column: "active", Label: "Active", LogicalType: "boolean", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "active", ProjectionMode: "VALUE"}}},
		{ColumnID: "birth-date-id", Column: "birth_date", Label: "Birth date", LogicalType: "date", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}}},
	}, document.Columns...)
	return workspace
}

func builderFullUIGroupFilterDeriveWorkspace() authoringv2.Workspace {
	one := int64(1)
	group := []authoringv2.StageColumn{
		{ID: "gender-group", Name: "gender_group", Label: "Gender", Type: "string"},
		{ID: "row-count", Name: "row_count", Label: "Row count", Type: "integer"},
	}
	filter := append([]authoringv2.StageColumn(nil), group...)
	derive := append(append([]authoringv2.StageColumn(nil), filter...), authoringv2.StageColumn{ID: "adjusted", Name: "adjusted", Label: "Adjusted", Type: "integer"})
	construction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{
		{ID: "group-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationGroup, Group: &authoringv2.ConstructionGroup{
			ConstructionID: "group-step", MissingKeyPolicy: authoringv2.ConstructionGroupMissingKeyGroup,
			Keys:       []authoringv2.ConstructionGroupKey{{InputColumnID: "gender-id", OutputColumnID: "gender-group"}},
			Aggregates: []authoringv2.ConstructionGroupAggregate{{Operation: authoringv2.ConstructionGroupCountRows, OutputColumnID: "row-count"}},
		}}, Outputs: group},
		{ID: "filter-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "group-step"}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{ColumnID: "row-count", Operator: authoringv2.ConstructionFilterExists}}, Outputs: filter},
		{ID: "derive-step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "filter-step"}}, Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID: "derive-step", OutputColumnID: "adjusted", Operation: authoringv2.ConstructionDerivedAdd,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: "row-count"},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &one}},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}}, Outputs: derive},
	}}
	doc := authoringv2.Document{
		Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RecordsRowDefinition(),
		Columns: []authoringv2.Column{
			{ColumnID: "gender-id", Column: "gender", Label: "Gender", LogicalType: "string", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "gender", ProjectionMode: "VALUE"}}},
			{ColumnID: "age-id", Column: "age", Label: "Age", LogicalType: "integer", OccurrenceID: authoringv2.RootOccurrenceID, Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "multipleBirthInteger", ProjectionMode: "VALUE"}}},
		}, Construction: &construction,
	}
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, SemanticsVersion: authoringv2.CurrentSemanticsVersion,
		Explorer: authoringv2.ExplorerMetadata{Title: "Builder full UI replay"}, Documents: []authoringv2.Document{doc},
		Tabs: []authoringv2.Tab{{ID: "patients_tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
}

func newBuilderFullUIHarnessFromPrepared(prepared builderFullUISeed) (*builderFullUIHarness, error) {
	modelSource := builderFullUIInventoryRow{StateRefBefore: prepared.StateRefBefore, Before: prepared.ModelState}
	state, err := decodeBuilderFullUIAbstractState(modelSource)
	if err != nil {
		return nil, fmt.Errorf("decode prepared exact ModelState: %w", err)
	}
	fixture, workspace, _, err := builderFullUIRegisteredSeedFixture(prepared.EntryID, prepared.SeedKind, state)
	if err != nil {
		return nil, fmt.Errorf("recreate registered %s seed fixture: %w", prepared.SeedKind, err)
	}
	if fixture.NoSavedWorkspace {
		if !isJSONNullOrEmpty(prepared.AcceptedWorkspace) || prepared.AcceptedVersion != 0 || prepared.AcceptedDigest != "" || prepared.OutputID != "" {
			return nil, fmt.Errorf("uninitialized prepared seed carries a saved workspace identity")
		}
		if fixture.Snapshot.Token != prepared.SnapshotToken || fixture.Project != prepared.Project || fixture.ExplorerID != prepared.ExplorerID {
			return nil, fmt.Errorf("uninitialized prepared seed context differs from its registered catalog")
		}
		harness, harnessErr := newBuilderFullUIHarness(fixture, workspace)
		if harnessErr != nil {
			return nil, harnessErr
		}
		if err := verifyBuilderFullUIPreparedPayload(prepared.Catalog, fixture.Catalog, "catalog"); err != nil {
			return nil, err
		}
		state, stateErr := harness.Service.Builder(context.Background(), BuilderRequest{Project: fixture.Project, ExplorerID: fixture.ExplorerID})
		if stateErr != nil {
			return nil, fmt.Errorf("rebuild actual no-draft Builder state: %w", stateErr)
		}
		if err := verifyBuilderFullUIPreparedPayload(prepared.BuilderState, state, "Builder state"); err != nil {
			return nil, err
		}
		return harness, nil
	}
	preparedWorkspace, err := authoringv2.DecodeWorkspace(prepared.AcceptedWorkspace)
	if err != nil {
		return nil, fmt.Errorf("decode prepared acceptedWorkspace: %w", err)
	}
	fixtureJSON, err := workspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	preparedJSON, err := preparedWorkspace.CanonicalJSON()
	if err != nil {
		return nil, err
	}
	if digest, digestErr := preparedWorkspace.Digest(); digestErr != nil || digest != prepared.AcceptedDigest || fixture.Snapshot.Token != prepared.SnapshotToken || fixture.OutputID != prepared.OutputID || fixture.Project != prepared.Project || fixture.ExplorerID != prepared.ExplorerID || !bytes.Equal(fixtureJSON, preparedJSON) {
		return nil, fmt.Errorf("prepared wire workspace/context differs from its canonical identity")
	}
	harness, err := newBuilderFullUIHarness(fixture, preparedWorkspace)
	if err != nil {
		return nil, err
	}
	if err := verifyBuilderFullUIPreparedPayload(prepared.Catalog, fixture.Catalog, "catalog"); err != nil {
		return nil, err
	}
	builderState, err := harness.Service.Builder(context.Background(), BuilderRequest{Project: fixture.Project, ExplorerID: fixture.ExplorerID})
	if err != nil {
		return nil, fmt.Errorf("rebuild actual Builder state: %w", err)
	}
	if err := verifyBuilderFullUIPreparedPayload(prepared.BuilderState, builderState, "Builder state"); err != nil {
		return nil, err
	}
	if fixture.CapabilityRequest != nil {
		capRequest := fixture.CapabilityRequest
		capabilities, capabilityErr := harness.Service.GetConstructionCapabilities(context.Background(), ConstructionCapabilitiesRequest{
			Project: fixture.Project, ExplorerID: fixture.ExplorerID, SnapshotToken: fixture.Snapshot.Token,
			ExpectedDraftVersion: fixture.AcceptedVersion, ExpectedDraftDigest: prepared.AcceptedDigest,
			OutputID: fixture.OutputID, StageID: capRequest.StageID, GroupStepID: capRequest.GroupStepID,
		})
		if capabilityErr != nil {
			return nil, fmt.Errorf("rebuild actual stage capability response: %w", capabilityErr)
		}
		if err := verifyBuilderFullUIPreparedPayload(prepared.Capabilities, capabilities, "construction capabilities"); err != nil {
			return nil, err
		}
	}
	return harness, nil
}

func verifyBuilderFullUIPreparedPayload(prepared json.RawMessage, actual any, label string) error {
	if isJSONNullOrEmpty(prepared) {
		return fmt.Errorf("prepared %s payload is missing", label)
	}
	actualJSON, err := json.Marshal(actual)
	if err != nil {
		return fmt.Errorf("encode actual %s payload: %w", label, err)
	}
	preparedValue, err := decodeJSONValue(prepared)
	if err != nil {
		return fmt.Errorf("decode prepared %s payload: %w", label, err)
	}
	actualValue, err := decodeJSONValue(actualJSON)
	if err != nil {
		return fmt.Errorf("decode actual %s payload: %w", label, err)
	}
	if !reflect.DeepEqual(preparedValue, actualValue) {
		return fmt.Errorf("prepared %s payload differs from exact service output", label)
	}
	return nil
}

func newBuilderFullUIHarness(fixture builderFullUICapabilityFixture, workspace authoringv2.Workspace) (*builderFullUIHarness, error) {
	var canonical []byte
	digest := ""
	if fixture.NoSavedWorkspace {
		if fixture.AcceptedVersion != 0 || fixture.OutputID != "" || fixture.Snapshot.Token == "" {
			return nil, fmt.Errorf("uninitialized fixture must have version zero, no output, and a catalog token")
		}
	} else {
		if err := workspace.Validate(); err != nil {
			return nil, fmt.Errorf("validate prepared acceptedWorkspace: %w", err)
		}
		var err error
		canonical, err = workspace.CanonicalJSON()
		if err != nil {
			return nil, err
		}
		digest, err = workspace.Digest()
		if err != nil {
			return nil, err
		}
		if fixture.AcceptedVersion < 1 || fixture.OutputID == "" || fixture.Snapshot.Token == "" {
			return nil, fmt.Errorf("fixture context lacks workspace identity")
		}
	}
	title := workspace.Explorer.Title
	if fixture.NoSavedWorkspace {
		title = "Builder full UI replay"
	}
	store := &fakeStore{created: &explorer.Explorer{Project: fixture.Project, ExplorerID: fixture.ExplorerID, Title: title, DraftConfig: canonical, DraftVersion: fixture.AcceptedVersion, DraftDigest: digest}, copyGet: true}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return nil, err
	}
	resolver, err := NewSchemaRowChoiceResolver(index)
	if err != nil {
		return nil, err
	}
	config := testConfig(fixture.Snapshot)
	config.Capability.Current = func(context.Context, string, string, string) (capability.Snapshot, error) {
		return fixture.Snapshot, nil
	}
	config.Capability.Catalog = func(capability.Snapshot, string) authoringv2.CatalogSnapshot { return fixture.Catalog }
	config.RowChoicePlanner, config.RowChoiceResolver = resolver, resolver
	config.CompileReceipt = func(ctx context.Context, request CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		receipt, compileErr := compileTraceReceipt(ctx, request, fixture.Snapshot, fixture.Catalog)
		if compileErr == nil {
			// compileTraceReceipt runs the real workspace compiler/lowering path;
			// this test adapter mirrors the production receipt wrapper's exact
			// proposal identity binding, which is not a compiler-derived field.
			receipt.RowDefinitionProposal = cloneRowDefinitionProposalBinding(request.RowDefinitionProposal)
			if request.RowDefinitionProposal != nil {
				receipt.CompilationKey, compileErr = explorer.CompilationKey(*receipt)
				if compileErr == nil {
					receipt.ID, compileErr = explorer.ReceiptID(*receipt)
				}
				if compileErr == nil {
					compileErr = receipt.Validate()
				}
			}
			if compileErr != nil {
				return nil, compileErr
			}
			store.receipt = receipt
		}
		return receipt, compileErr
	}
	config.PreviewReceipt = func(_ context.Context, receipt *explorer.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview received no compiled receipt")
		}
		return dataframeexecution.PreviewSummary{Output: fixture.OutputID, Columns: emittedPublicColumnsForOutput(receipt, fixture.OutputID), Complete: true}, nil
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		return nil, err
	}
	service, err := New(domain, config)
	if err != nil {
		return nil, err
	}
	if fixture.NoSavedWorkspace {
		return &builderFullUIHarness{Service: service, Store: store, Fixture: fixture}, nil
	}
	base, err := service.ReconcileWithWorkspace(context.Background(), ReconcileRequest{Project: fixture.Project, ExplorerID: fixture.ExplorerID, SnapshotToken: fixture.Snapshot.Token, DraftVersion: fixture.AcceptedVersion, DraftDigest: digest, OutputID: fixture.OutputID})
	if err != nil {
		return nil, fmt.Errorf("real base ReconcileWithWorkspace: %w", err)
	}
	if base.Receipt == nil || base.Receipt.ID == "" || store.receipt == nil || base.Receipt.ID != store.receipt.ID || store.saveDraftCalls != 0 {
		return nil, fmt.Errorf("base reconcile did not return exact compiled receipt without saving draft")
	}
	return &builderFullUIHarness{Service: service, Store: store, Workspace: workspace, Fixture: fixture, Base: base.Receipt}, nil
}

func readBuilderFullUIInventory(path string) (json.RawMessage, []builderFullUIInventoryRow, []builderFullUIGuardRow, string, error) {
	lines, artifactSHA, err := readBuilderFullUILines(path)
	if err != nil {
		return nil, nil, nil, "", err
	}
	if len(lines) < 2 {
		return nil, nil, nil, "", fmt.Errorf("inventory JSONL requires a header and at least one case or guard row")
	}
	var headerMap map[string]json.RawMessage
	if err := json.Unmarshal(lines[0], &headerMap); err != nil {
		return nil, nil, nil, "", fmt.Errorf("decode inventory header: %w", err)
	}
	if hasRawField(headerMap, "entryId") {
		return nil, nil, nil, "", fmt.Errorf("first inventory JSONL record has entryId; expected header")
	}
	var headerType string
	_ = json.Unmarshal(headerMap["recordType"], &headerType)
	if headerType != "header" {
		return nil, nil, nil, "", fmt.Errorf("inventory first recordType=%q, want header", headerType)
	}
	if schemaVersion, ok := rawIntField(headerMap, "schemaVersion"); !ok || schemaVersion != 1 {
		return nil, nil, nil, "", fmt.Errorf("inventory header schemaVersion=%d present=%t, want 1", schemaVersion, ok)
	}

	rows := make([]builderFullUIInventoryRow, 0, len(lines)-1)
	guards := make([]builderFullUIGuardRow, 0)
	seen := map[string]bool{}
	seenGuards := map[string]bool{}
	for lineIndex, raw := range lines[1:] {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, nil, nil, "", fmt.Errorf("decode inventory row %d: %w", lineIndex+2, err)
		}
		var recordType string
		_ = json.Unmarshal(fields["recordType"], &recordType)
		if recordType == "guard" {
			var guard builderFullUIGuardRow
			if err := json.Unmarshal(raw, &guard); err != nil {
				return nil, nil, nil, "", fmt.Errorf("decode guard row %d: %w", lineIndex+2, err)
			}
			guard.Raw = append(json.RawMessage(nil), raw...)
			if guard.GuardID == "" || guard.EntryID == "" || guard.StateID == "" {
				return nil, nil, nil, "", fmt.Errorf("guard row %d omits guardId, entryId, or stateId", lineIndex+2)
			}
			if version, ok := rawIntField(fields, "schemaVersion"); !ok || version != 1 {
				return nil, nil, nil, "", fmt.Errorf("guard row %d schemaVersion=%d present=%t, want 1", lineIndex+2, version, ok)
			}
			if seenGuards[guard.GuardID] {
				return nil, nil, nil, "", fmt.Errorf("duplicate guardId %q", guard.GuardID)
			}
			seenGuards[guard.GuardID] = true
			guards = append(guards, guard)
			continue
		}
		if recordType != "case" {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d recordType=%q, want case or guard", lineIndex+2, recordType)
		}
		row := builderFullUIInventoryRow{Raw: append(json.RawMessage(nil), raw...)}
		if err := decodeRawField(fields, "caseId", &row.CaseID); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d caseId: %w", lineIndex+2, err)
		}
		if err := decodeRawField(fields, "entryId", &row.EntryID); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d entryId: %w", lineIndex+2, err)
		}
		if err := decodeRawField(fields, "actionIndex", &row.ActionIndex); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d actionIndex: %w", lineIndex+2, err)
		}
		if err := decodeRawField(fields, "requestMode", &row.RequestMode); err != nil {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d requestMode: %w", lineIndex+2, err)
		}
		if version, ok := rawIntField(fields, "schemaVersion"); !ok || version != 1 {
			return nil, nil, nil, "", fmt.Errorf("case row %d schemaVersion=%d present=%t, want 1", lineIndex+2, version, ok)
		}
		row.Request = copyRawField(fields, "request")
		row.Requests = copyRawField(fields, "requests")
		row.Before = copyRawField(fields, "before")
		if stateRefs := copyRawField(fields, "stateRefs"); len(stateRefs) != 0 {
			var refs builderFullUIStateRefs
			if err := json.Unmarshal(stateRefs, &refs); err != nil {
				row.StateResolveError = "decode stateRefs: " + err.Error()
			} else {
				row.StateRefBefore = refs.Before
				if row.StateRefBefore == "" {
					row.StateResolveError = "stateRefs.before is missing"
				}
			}
		} else {
			row.StateResolveError = "stateRefs.before is missing"
		}
		row.Action = copyRawField(fields, "action")
		if action := rawObject(row.Action); action != nil {
			row.ActionRequest = copyRawField(action, "request")
			row.ActionExpectation = copyRawField(action, "expectation")
			row.RequestStep = copyRawField(action, "requestStep")
		}
		row.Seed = copyRawField(fields, "seed")
		row.InputState = copyRawField(fields, "inputState")
		row.ExpectedState = copyRawField(fields, "expectedState")
		row.Assignment = copyRawField(fields, "assignment")
		row.Actions = copyRawField(fields, "actions")
		row.TracePrefix = copyRawField(fields, "tracePrefix")
		row.CapabilityProvenance = copyRawField(fields, "capabilityProvenance")
		row.Expectation = copyRawField(fields, "expectation")
		if len(row.Expectation) == 0 {
			row.Expectation = row.ActionExpectation
		}
		if row.CaseID == "" || row.EntryID == "" || row.ActionIndex < 0 {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d has invalid key %q/%q/%d", lineIndex+2, row.CaseID, row.EntryID, row.ActionIndex)
		}
		if row.RequestMode != "emitted" && row.RequestMode != "blocked" && row.RequestMode != "gap" && row.RequestMode != "local" {
			return nil, nil, nil, "", fmt.Errorf("inventory row %d has unsupported requestMode %q", lineIndex+2, row.RequestMode)
		}
		if seen[row.key().string()] {
			return nil, nil, nil, "", fmt.Errorf("duplicate inventory request key %q/%q/%d", row.CaseID, row.EntryID, row.ActionIndex)
		}
		seen[row.key().string()] = true
		rows = append(rows, row)
	}
	return append(json.RawMessage(nil), lines[0]...), rows, guards, artifactSHA, nil
}

func readBuilderFullUIInventoryManifest(path string, modelHeader json.RawMessage) (map[string]builderFullUIInventoryManifestEntry, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(content)
	manifestSHA := hex.EncodeToString(sum[:])
	if want := builderFullUIInventorySourceSHA(modelHeader); want == "" || want != manifestSHA {
		return nil, "", fmt.Errorf("inventory manifest SHA-256 %q differs from model header %q", manifestSHA, want)
	}
	var manifest builderFullUIInventoryManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, "", fmt.Errorf("decode inventory manifest: %w", err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Affordances) == 0 {
		return nil, "", fmt.Errorf("inventory manifest schemaVersion=%d affordances=%d, want schemaVersion 1 and nonempty affordances", manifest.SchemaVersion, len(manifest.Affordances))
	}
	entries := make(map[string]builderFullUIInventoryManifestEntry, len(manifest.Affordances))
	for index, entry := range manifest.Affordances {
		if strings.TrimSpace(entry.ID) == "" {
			return nil, "", fmt.Errorf("inventory manifest affordances[%d] has no exact id", index)
		}
		if _, duplicate := entries[entry.ID]; duplicate {
			return nil, "", fmt.Errorf("inventory manifest repeats exact entry id %q", entry.ID)
		}
		entries[entry.ID] = entry
	}
	return entries, manifestSHA, nil
}

func readBuilderFullUIModelStates(path, expectedSHA string, modelHeader json.RawMessage) (map[string]json.RawMessage, string, error) {
	lines, artifactSHA, err := readBuilderFullUILines(path)
	if err != nil {
		return nil, "", err
	}
	if expectedSHA == "" || artifactSHA != expectedSHA {
		return nil, "", fmt.Errorf("ModelState artifact SHA-256 %q differs from supplied %q", artifactSHA, expectedSHA)
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal(modelHeader, &header); err != nil {
		return nil, "", fmt.Errorf("decode model header: %w", err)
	}
	var artifact struct {
		Path          string `json:"path"`
		SchemaVersion int    `json:"schemaVersion"`
		SHA256        string `json:"sha256"`
		StateCount    int    `json:"stateCount"`
	}
	if err := json.Unmarshal(header["stateArtifact"], &artifact); err != nil {
		return nil, "", fmt.Errorf("decode model stateArtifact header binding: %w", err)
	}
	if artifact.SchemaVersion != 1 || artifact.SHA256 != artifactSHA || filepath.Base(artifact.Path) != filepath.Base(path) {
		return nil, "", fmt.Errorf("ModelState artifact binding differs: header=%#v actualSha=%s path=%s", artifact, artifactSHA, path)
	}
	var sidecarHeader struct {
		RecordType    string `json:"recordType"`
		SchemaVersion int    `json:"schemaVersion"`
		StateCount    int    `json:"stateCount"`
		DomainSHA256  string `json:"domainSHA256"`
		ModelSHA256   string `json:"modelSHA256"`
	}
	if err := json.Unmarshal(lines[0], &sidecarHeader); err != nil {
		return nil, "", fmt.Errorf("decode ModelState sidecar header: %w", err)
	}
	var modelBinding struct {
		DomainSHA256 string `json:"domainSHA256"`
		ModelSHA256  string `json:"modelSHA256"`
	}
	if err := json.Unmarshal(modelHeader, &modelBinding); err != nil {
		return nil, "", fmt.Errorf("decode main model domain/model hashes: %w", err)
	}
	if sidecarHeader.RecordType != "header" || sidecarHeader.SchemaVersion != 1 || sidecarHeader.StateCount != artifact.StateCount || sidecarHeader.DomainSHA256 != modelBinding.DomainSHA256 || sidecarHeader.ModelSHA256 != modelBinding.ModelSHA256 {
		return nil, "", fmt.Errorf("ModelState sidecar header differs from model header: sidecar=%#v model=%#v stateArtifact=%#v", sidecarHeader, modelBinding, artifact)
	}
	stateLines := lines[1:]
	if artifact.StateCount != len(stateLines) {
		return nil, "", fmt.Errorf("ModelState stateCount=%d but sidecar has %d state rows", artifact.StateCount, len(stateLines))
	}
	states := make(map[string]json.RawMessage, len(stateLines))
	for index, raw := range stateLines {
		var row builderFullUIModelStateRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, "", fmt.Errorf("decode ModelState row %d: %w", index+2, err)
		}
		if row.RecordType != "state" || row.SchemaVersion != 1 || row.StateID == "" || isJSONNullOrEmpty(row.ModelState) || rawObject(row.ModelState) == nil {
			return nil, "", fmt.Errorf("ModelState row %d has invalid record type/id/value", index+2)
		}
		if _, duplicate := states[row.StateID]; duplicate {
			return nil, "", fmt.Errorf("ModelState artifact repeats stateId %q", row.StateID)
		}
		states[row.StateID] = append(json.RawMessage(nil), row.ModelState...)
	}
	return states, artifactSHA, nil
}

func bindBuilderFullUIGuardStates(rows []builderFullUIGuardRow, states map[string]json.RawMessage) int {
	resolved := 0
	for index := range rows {
		row := &rows[index]
		state, exists := states[row.StateID]
		if !exists {
			row.StateResolveError = fmt.Sprintf("guard stateId %q is missing from the bound ModelState artifact", row.StateID)
			continue
		}
		row.State = append(json.RawMessage(nil), state...)
		resolved++
	}
	return resolved
}

func bindBuilderFullUIModelStates(rows []builderFullUIInventoryRow, states map[string]json.RawMessage) error {
	for index := range rows {
		row := &rows[index]
		if row.StateResolveError != "" {
			continue
		}
		state, exists := states[row.StateRefBefore]
		if !exists {
			row.StateResolveError = fmt.Sprintf("stateRefs.before %q is missing from the bound ModelState artifact", row.StateRefBefore)
			continue
		}
		var stateID struct {
			StateID string `json:"stateId"`
		}
		if err := json.Unmarshal(state, &stateID); err != nil {
			row.StateResolveError = "decode referenced ModelState: " + err.Error()
			continue
		}
		if stateID.StateID != row.StateRefBefore {
			row.StateResolveError = fmt.Sprintf("ModelState stateId %q differs from exact stateRefs.before %q", stateID.StateID, row.StateRefBefore)
			continue
		}
		row.Before = append(json.RawMessage(nil), state...)
	}
	return nil
}

func bindBuilderFullUIInventorySeeds(rows []builderFullUIInventoryRow, manifest map[string]builderFullUIInventoryManifestEntry) error {
	for index := range rows {
		row := &rows[index]
		entry, exists := manifest[row.EntryID]
		if !exists {
			continue // A missing manifest entry is recorded as a row-local GAP during prepare.
		}
		if entry.ID != row.EntryID {
			return fmt.Errorf("manifest lookup key %q resolved entry id %q", row.EntryID, entry.ID)
		}
		var seed builderFullUIManifestSeed
		if err := json.Unmarshal(entry.Seed, &seed); err != nil {
			row.StateResolveError = "decode exact normalized inventory seed: " + err.Error()
			continue
		}
		if seed.ID == "" || seed.Kind == "" {
			row.StateResolveError = "exact normalized inventory seed omits id or kind"
			continue
		}
		row.Seed = append(json.RawMessage(nil), entry.Seed...)
		row.SeedID, row.SeedKind = seed.ID, seed.Kind
	}
	return nil
}

func builderFullUIPrepareHeaderField(header json.RawMessage, name string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(header, &fields) != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(fields[name], &value)
	return value
}

func readBuilderFullUIPreparedSeeds(path string) (map[string]builderFullUISeed, json.RawMessage, string, error) {
	lines, artifactSHA, err := readBuilderFullUILines(path)
	if err != nil {
		return nil, nil, "", err
	}
	if len(lines) < 1 {
		return nil, nil, "", fmt.Errorf("prepared seed JSONL is empty")
	}
	var header map[string]json.RawMessage
	if err := json.Unmarshal(lines[0], &header); err != nil {
		return nil, nil, "", fmt.Errorf("decode prepared seed header: %w", err)
	}
	var recordType, phase string
	_ = json.Unmarshal(header["recordType"], &recordType)
	_ = json.Unmarshal(header["phase"], &phase)
	if recordType != "header" || phase != "prepare" {
		return nil, nil, "", fmt.Errorf("prepared seed header recordType/phase=%q/%q, want header/prepare", recordType, phase)
	}
	if version, ok := rawIntField(header, "schemaVersion"); !ok || version != 1 {
		return nil, nil, "", fmt.Errorf("prepared seed schemaVersion=%d present=%t, want 1", version, ok)
	}
	prepared := make(map[string]builderFullUISeed, len(lines)-1)
	preparedCount, blockedCount, localCount, gapCount, setupFailureCount := 0, 0, 0, 0, 0
	for index, raw := range lines[1:] {
		var row builderFullUISeed
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, nil, "", fmt.Errorf("decode prepared seed row %d: %w", index+2, err)
		}
		row.Raw = append(json.RawMessage(nil), raw...)
		if row.RecordType != "prepared-seed" || row.SchemaVersion != 1 || row.CaseID == "" || row.EntryID == "" || row.ActionIndex < 0 {
			return nil, nil, "", fmt.Errorf("prepared seed row %d has invalid record type or key", index+2)
		}
		if row.RequestMode != "emitted" && row.RequestMode != "blocked" && row.RequestMode != "local" && row.RequestMode != "gap" {
			return nil, nil, "", fmt.Errorf("prepared seed row %d has unsupported requestMode %q", index+2, row.RequestMode)
		}
		key := row.key().string()
		switch row.Status {
		case "PREPARED":
			if row.RequestMode != "emitted" {
				return nil, nil, "", fmt.Errorf("prepared backend row %q has non-emitted requestMode %q", key, row.RequestMode)
			}
			preparedCount++
		case "PREPARED_UI_ONLY":
			if row.RequestMode == "blocked" {
				blockedCount++
			} else if row.RequestMode == "local" {
				localCount++
			} else {
				return nil, nil, "", fmt.Errorf("prepared UI-only row %q has emitted/gap requestMode %q", key, row.RequestMode)
			}
		case "GAP":
			gapCount++
		case "SETUP_FAILURE":
			setupFailureCount++
		default:
			return nil, nil, "", fmt.Errorf("prepared seed row %q has unsupported status %q", key, row.Status)
		}
		if _, duplicate := prepared[key]; duplicate {
			return nil, nil, "", fmt.Errorf("duplicate prepared seed key %q/%q/%d", row.CaseID, row.EntryID, row.ActionIndex)
		}
		prepared[key] = row
	}
	for _, name := range []string{"seedCount", "preparedCount", "blockedCount", "localCount", "gapCount", "setupFailureCount"} {
		if _, ok := rawIntField(header, name); !ok {
			return nil, nil, "", fmt.Errorf("prepared seed header omits integer count %q", name)
		}
	}
	for name, gotWant := range map[string][2]int{
		"seedCount": {len(prepared), len(lines) - 1}, "preparedCount": {preparedCount, rawHeaderCount(header, "preparedCount")},
		"blockedCount": {blockedCount, rawHeaderCount(header, "blockedCount")}, "localCount": {localCount, rawHeaderCount(header, "localCount")},
		"gapCount": {gapCount, rawHeaderCount(header, "gapCount")}, "setupFailureCount": {setupFailureCount, rawHeaderCount(header, "setupFailureCount")},
	} {
		if gotWant[0] != gotWant[1] {
			return nil, nil, "", fmt.Errorf("prepared seed header %s=%d does not match row-derived count %d", name, gotWant[1], gotWant[0])
		}
	}
	return prepared, append(json.RawMessage(nil), lines[0]...), artifactSHA, nil
}

func rawHeaderCount(header map[string]json.RawMessage, name string) int {
	count, _ := rawIntField(header, name)
	return count
}

func writeBuilderFullUIPreparedSeeds(path string, header builderFullUIPrepareHeader, rows []builderFullUISeed) error {
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	if err := encoder.Encode(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content.Bytes(), 0o600)
}

func builderFullUIInventorySourceSHA(header json.RawMessage) string {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(header, &fields); err != nil {
		return ""
	}
	for _, name := range []string{"inventorySha256", "inventorySHA256"} {
		var value string
		if err := json.Unmarshal(fields[name], &value); err == nil && value != "" {
			return value
		}
	}
	return ""
}

func rawObject(raw json.RawMessage) map[string]json.RawMessage {
	var value map[string]json.RawMessage
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}

func readBuilderFullUIObservations(path string) (map[string]builderFullUIUIObservation, string, error) {
	lines, artifactSHA, err := readBuilderFullUILines(path)
	if err != nil {
		return nil, "", err
	}
	observations := make(map[string]builderFullUIUIObservation, len(lines))
	start := 0
	var first map[string]json.RawMessage
	if err := json.Unmarshal(lines[0], &first); err != nil {
		return nil, "", fmt.Errorf("decode UI observation first row: %w", err)
	}
	var firstType string
	_ = json.Unmarshal(first["recordType"], &firstType)
	if firstType == "header" {
		if schemaVersion, ok := rawIntField(first, "schemaVersion"); !ok || schemaVersion != 1 {
			return nil, "", fmt.Errorf("UI observation header schemaVersion=%d present=%t, want 1", schemaVersion, ok)
		}
		start = 1
	}
	for lineIndex, raw := range lines[start:] {
		var ui builderFullUIUIObservation
		if err := json.Unmarshal(raw, &ui); err != nil {
			return nil, "", fmt.Errorf("decode UI observation row %d: %w", lineIndex+start+1, err)
		}
		ui.Raw = append(json.RawMessage(nil), raw...)
		if ui.RecordType != "ui-observation" || ui.SchemaVersion != 1 || ui.CaseID == "" || ui.EntryID == "" || ui.ActionIndex < 0 {
			return nil, "", fmt.Errorf("UI observation row %d has invalid record/key: %#v", lineIndex+start+1, ui)
		}
		key := builderFullUIKey{CaseID: ui.CaseID, EntryID: ui.EntryID, ActionIndex: ui.ActionIndex}.string()
		if _, exists := observations[key]; exists {
			return nil, "", fmt.Errorf("duplicate UI observation key %q/%q/%d", ui.CaseID, ui.EntryID, ui.ActionIndex)
		}
		observations[key] = ui
	}
	return observations, artifactSHA, nil
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
	candidate.Steps[0].Operation.Group.Aggregates[0] = authoringv2.ConstructionGroupAggregate{
		Operation: authoringv2.ConstructionGroupMean, InputColumnID: "age-id", OutputColumnID: "row-count",
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

func TestBuilderFullUIRequestDecoderPreservesNullableBodyAndEndpoint(t *testing.T) {
	noBodyEndpoint := "/api/v1/projects/p/explorers/e/authoring/v2/row-definition-choices?outputId=patients&limit=0"
	noBody, err := decodeBuilderFullUIRequest(json.RawMessage(fmt.Sprintf(`{"kind":"rowDefinitionChoices","method":"POST","endpoint":%q,"bodyRaw":null,"body":null}`, noBodyEndpoint)))
	if err != nil {
		t.Fatalf("decode explicit no-body request: %v", err)
	}
	if noBody.HasBody || string(noBody.BodyRaw) != "null" || string(noBody.Body) != "null" || noBody.Endpoint != noBodyEndpoint {
		t.Fatalf("explicit null body or concrete query/path was changed: %#v", noBody)
	}

	objectEndpoint := "/api/v1/projects/p/explorers/e/authoring/v2/suggestions?nodeId=node-1&query=gender"
	objectBody, err := decodeBuilderFullUIRequest(json.RawMessage(fmt.Sprintf(`{"kind":"suggestions","method":"GET","endpoint":%q,"bodyRaw":"{}","body":{}}`, objectEndpoint)))
	if err != nil {
		t.Fatalf("decode empty-object body independent of HTTP method: %v", err)
	}
	if !objectBody.HasBody || string(objectBody.BodyRaw) != `"{}"` || string(objectBody.Body) != "{}" || objectBody.Endpoint != objectEndpoint {
		t.Fatalf("empty-object body or concrete query/path was changed: %#v", objectBody)
	}

	emptyRawBody := json.RawMessage(`{"kind":"suggestions","method":"POST","endpoint":"/suggestions","bodyRaw":"","body":null}`)
	if _, err := decodeBuilderFullUIRequest(emptyRawBody); err == nil {
		t.Fatal(`bodyRaw:"" must remain distinct from an absent body represented by bodyRaw:null`)
	}
	missingBodyRaw := json.RawMessage(`{"kind":"suggestions","method":"POST","endpoint":"/suggestions","body":null}`)
	if _, err := decodeBuilderFullUIRequest(missingBodyRaw); err == nil {
		t.Fatal("missing bodyRaw must not be inferred from the HTTP method")
	}
}

func TestBuilderFullUIDispatchKindSeparatesCapturedAndSemanticNames(t *testing.T) {
	for _, test := range []struct{ captured, semantic string }{
		{captured: "constructionProposal", semantic: "construction-proposal"},
		{captured: "rowDefinitionProposal", semantic: "row-definition-proposal"},
		{captured: "rowDefinitionChoices", semantic: "row-definition-choices"},
		{captured: "explicitGroups", semantic: "explicit-groups"},
	} {
		if got := builderFullUIDispatchKind(test.captured); got != test.semantic {
			t.Errorf("captured UI request kind %q dispatch kind=%q, want semantic model kind %q", test.captured, got, test.semantic)
		}
	}
	if got := builderFullUIDispatchKind("unknownAdapterRequest"); got != "" {
		t.Fatalf("unknown UI request kind produced semantic dispatch kind %q", got)
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
		workspace, err := authoringv2.DecodeWorkspace(captured.AcceptedWorkspace)
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
	Class   ErrorClass      `json:"class"`
	Stage   string          `json:"stage"`
	Code    string          `json:"code"`
	Path    string          `json:"path,omitempty"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

type builderFullUIActualReceipt struct {
	ID                    string                                            `json:"id"`
	IntentDigest          string                                            `json:"intentDigest"`
	OutputSchemas         map[string][]builderFullUIActualCol               `json:"outputSchemas"`
	CompiledOutputSchemas map[string][]explorer.ReceiptCompiledOutputColumn `json:"compiledOutputSchemas"`
	ConstructionStages    map[string][]explorer.ReceiptConstructionStage    `json:"constructionStages"`
}

type builderFullUIActualCol struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	LogicalType string `json:"logicalType"`
	Nullable    bool   `json:"nullable"`
}

type builderFullUIActual struct {
	Kind                  string                             `json:"kind"`
	Disposition           string                             `json:"disposition"`
	Proposal              *ConstructionProposalResponse      `json:"proposal,omitempty"`
	RowDefinitionProposal *RowDefinitionProposal             `json:"rowDefinitionProposal,omitempty"`
	Capabilities          *ConstructionCapabilitiesResponse  `json:"capabilities,omitempty"`
	Suggestions           *builderFullUIActualSuggestions    `json:"suggestions,omitempty"`
	RowDefinitionChoices  *RowDefinitionChoicesResponse      `json:"rowDefinitionChoices,omitempty"`
	CompiledReceipt       *builderFullUIActualReceipt        `json:"compiledReceipt,omitempty"`
	Preview               *builderFullUIPreviewObservation   `json:"preview,omitempty"`
	Applied               *authoringv2.ApplyCommandsResponse `json:"applied,omitempty"`
	AcceptedWorkspace     json.RawMessage                    `json:"acceptedWorkspace,omitempty"`
	ServiceError          *builderFullUIActualServiceError   `json:"serviceError,omitempty"`
	FakeStore             *builderFullUIFakeStoreObservation `json:"fakeStorePersistence,omitempty"`
}

type builderFullUIActualSuggestions struct {
	SnapshotToken string                         `json:"snapshotToken"`
	NodeID        string                         `json:"nodeId"`
	Candidates    []authoringv2.CatalogCandidate `json:"candidates"`
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
	var appErr *Error
	if errors.As(err, &appErr) {
		details, _ := json.Marshal(appErr.Details)
		return &builderFullUIActualServiceError{Class: appErr.Class, Stage: appErr.Stage, Code: appErr.Code, Path: appErr.Path, Message: appErr.Message, Details: details}
	}
	return &builderFullUIActualServiceError{Class: ClassInternal, Message: err.Error()}
}

func actualReceipt(receipt *explorer.CompilationReceipt) *builderFullUIActualReceipt {
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

func executeBuilderFullUIRequest(row builderFullUIInventoryRow, request builderFullUIRequest, prepared builderFullUISeed, harness *builderFullUIHarness) (any, []string, error) {
	actual := builderFullUIActual{Kind: request.Kind}
	layers := []string{}
	ctx := context.Background()
	identity := prepared
	resultError := func(err error, reached ...string) (any, []string, error) {
		actual.Disposition = "SERVICE_ERROR"
		actual.ServiceError = lifecycleErrorObservation(err)
		if harness.Store != nil && harness.Store.created != nil {
			actual.AcceptedWorkspace = append(json.RawMessage(nil), harness.Store.created.DraftConfig...)
			actual.FakeStore = &builderFullUIFakeStoreObservation{SaveDraftCalls: harness.Store.saveDraftCalls, DraftVersion: harness.Store.created.DraftVersion, DraftDigest: harness.Store.created.DraftDigest}
		}
		return actual, append(layers, reached...), nil
	}
	attachAccepted := func() {
		actual.AcceptedWorkspace = append(json.RawMessage(nil), harness.Store.created.DraftConfig...)
		actual.FakeStore = &builderFullUIFakeStoreObservation{SaveDraftCalls: harness.Store.saveDraftCalls, DraftVersion: harness.Store.created.DraftVersion, DraftDigest: harness.Store.created.DraftDigest}
	}
	switch normalizeBuilderFullUIKind(request.Kind) {
	case "suggestions":
		var body struct {
			SnapshotToken string `json:"snapshotToken"`
			NodeID        string `json:"nodeId"`
			Query         string `json:"query"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode Suggestions request: %w", err)
		}
		value, err := harness.Service.Suggestions(ctx, SuggestionsRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken, NodeID: body.NodeID, Query: body.Query})
		layers = append(layers, "lifecycle_analyzer_compiler")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "SUGGESTIONS"
		actual.Suggestions = &builderFullUIActualSuggestions{SnapshotToken: value.SnapshotToken, NodeID: value.NodeID, Candidates: value.Candidates}
		attachAccepted()
		return actual, layers, nil
	case "rowdefinitionchoices":
		parsed, parseErr := url.Parse(request.Endpoint)
		if parseErr != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: parse row-definition-choices endpoint: %w", parseErr)
		}
		query := parsed.Query()
		value, err := harness.Service.ListRowDefinitionChoices(ctx, RowDefinitionChoicesRequest{
			Project: identity.Project, ExplorerID: identity.ExplorerID,
			SnapshotToken: query.Get("snapshotToken"), OutputID: query.Get("outputId"),
		})
		layers = append(layers, "lifecycle_analyzer_compiler")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "ROW_DEFINITION_CHOICES"
		actual.RowDefinitionChoices = &value
		attachAccepted()
		return actual, layers, nil
	case "constructionproposal":
		var body struct {
			ExpectedDraftDigest   string                              `json:"expectedDraftDigest"`
			ExpectedDraftVersion  int64                               `json:"expectedDraftVersion"`
			SnapshotToken         string                              `json:"snapshotToken"`
			OutputID              string                              `json:"outputId"`
			ChangedStepID         *string                             `json:"changedStepId"`
			RemoveStepIDs         *[]string                           `json:"removeStepIds"`
			CandidateConstruction json.RawMessage                     `json:"candidateConstruction"`
			GroupSource           *ConstructionGroupSourceSelection   `json:"groupSource"`
			GroupSources          *[]ConstructionGroupSourceSelection `json:"groupSources"`
			Limit                 *int                                `json:"limit"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: construction proposal request body is not decodable: %w", err)
		}
		if len(body.CandidateConstruction) == 0 || isJSONNullOrEmpty(body.CandidateConstruction) {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: construction proposal body omits candidateConstruction")
		}
		var candidate authoringv2.Construction
		if err := json.Unmarshal(body.CandidateConstruction, &candidate); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode candidateConstruction: %w", err)
		}
		requestValue := ConstructionProposalRequest{
			Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputID, CandidateConstruction: candidate,
		}
		if body.ChangedStepID != nil {
			requestValue.ChangedStepID = *body.ChangedStepID
		}
		if body.RemoveStepIDs != nil {
			requestValue.RemoveStepIDs = append([]string(nil), (*body.RemoveStepIDs)...)
		}
		if body.GroupSource != nil {
			groupSource := *body.GroupSource
			requestValue.GroupSource = &groupSource
		}
		if body.GroupSources != nil {
			requestValue.GroupSources = append([]ConstructionGroupSourceSelection(nil), (*body.GroupSources)...)
		}
		if body.Limit != nil {
			requestValue.Limit = *body.Limit
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		proposal, err := harness.Service.ProposeConstruction(ctx, requestValue)
		if err != nil {
			return resultError(err)
		}
		actual.Proposal, actual.CompiledReceipt = &proposal, actualReceipt(harness.Store.receipt)
		actual.Disposition = proposal.PreviewStatus
		if proposal.PreviewStatus == "PREVIEW_PENDING" && proposal.ProposalID != "" {
			preview, previewErr := harness.Service.Preview(ctx, PreviewRequest{
				Project: identity.Project, ExplorerID: identity.ExplorerID, ReceiptID: proposal.ProposalID, OutputID: proposal.OutputID,
				SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
					return func(map[string]any) error { return nil }, nil
				},
			})
			layers = append(layers, "preview_orchestration")
			if previewErr != nil {
				return resultError(previewErr, "preview_orchestration")
			}
			harness.LastPreview = &preview
			actual.Preview = &builderFullUIPreviewObservation{ReceiptID: preview.Receipt.ID, OutputID: proposal.OutputID, Columns: emittedPublicColumnsForOutput(preview.Receipt, proposal.OutputID), DataExecutionObserved: false}
			actual.Disposition = "PREVIEWED_INTERNAL_PENDING"
		}
		attachAccepted()
		return actual, layers, nil
	case "rowdefinitionproposal":
		var body struct {
			SnapshotToken        string                 `json:"snapshotToken"`
			ExpectedDraftVersion int64                  `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string                 `json:"expectedDraftDigest"`
			OutputID             string                 `json:"outputId"`
			Selection            RowDefinitionSelection `json:"selection"`
			Limit                int                    `json:"limit"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode RowDefinitionProposal request: %w", err)
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		proposal, err := harness.Service.ProposeRowDefinition(ctx, RowDefinitionProposalRequest{
			Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken,
			ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest,
			OutputID: body.OutputID, Selection: body.Selection, Limit: body.Limit,
		})
		if err != nil {
			return resultError(err)
		}
		actual.RowDefinitionProposal = &proposal
		actual.Disposition = "ROW_DEFINITION_PROPOSED"
		attachAccepted()
		return actual, append(layers, "preview_orchestration"), nil
	case "commands":
		var command authoringv2.ApplyCommandsRequest
		if err := json.Unmarshal(request.Body, &command); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode ApplyCommands request: %w", err)
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		applied, err := harness.Service.ApplyCommands(ctx, identity.Project, identity.ExplorerID, command, "builder-full-ui-replay")
		if err != nil {
			return resultError(err)
		}
		actual.Applied = applied
		actual.Disposition = "APPLIED"
		actual.AcceptedWorkspace = append(json.RawMessage(nil), harness.Store.created.DraftConfig...)
		actual.FakeStore = &builderFullUIFakeStoreObservation{SaveDraftCalls: harness.Store.saveDraftCalls, DraftVersion: harness.Store.created.DraftVersion, DraftDigest: harness.Store.created.DraftDigest}
		return actual, append(layers, "fake_store_persistence"), nil
	case "reconcile":
		var body struct {
			SnapshotToken string `json:"snapshotToken"`
			DraftVersion  int64  `json:"draftVersion"`
			DraftDigest   string `json:"draftDigest"`
			OutputID      string `json:"outputId"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode Reconcile request: %w", err)
		}
		layers = append(layers, "lifecycle_analyzer_compiler")
		reconciled, err := harness.Service.ReconcileWithWorkspace(ctx, ReconcileRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken, DraftVersion: body.DraftVersion, DraftDigest: body.DraftDigest, OutputID: body.OutputID})
		if err != nil {
			return resultError(err)
		}
		actual.Disposition, actual.CompiledReceipt = "RECONCILED", actualReceipt(reconciled.Receipt)
		attachAccepted()
		return actual, layers, nil
	case "preview":
		var body struct {
			ReceiptID string `json:"receiptId"`
			OutputID  string `json:"outputId"`
			Limit     *int   `json:"limit"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode preview request: %w", err)
		}
		limit := 0
		if body.Limit != nil {
			limit = *body.Limit
		}
		preview, err := harness.Service.Preview(ctx, PreviewRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, ReceiptID: body.ReceiptID, OutputID: body.OutputID, Limit: limit, SinkFactory: func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error) {
			return func(map[string]any) error { return nil }, nil
		}})
		layers = append(layers, "preview_orchestration")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "PREVIEWED"
		actual.CompiledReceipt = actualReceipt(preview.Receipt)
		actual.Preview = &builderFullUIPreviewObservation{ReceiptID: preview.Receipt.ID, OutputID: body.OutputID, Columns: emittedPublicColumnsForOutput(preview.Receipt, body.OutputID), DataExecutionObserved: false}
		attachAccepted()
		return actual, layers, nil
	case "constructioncapabilities":
		var body struct {
			SnapshotToken        string `json:"snapshotToken"`
			ExpectedDraftVersion int64  `json:"expectedDraftVersion"`
			ExpectedDraftDigest  string `json:"expectedDraftDigest"`
			OutputID             string `json:"outputId"`
			StageID              string `json:"stageId"`
			GroupStepID          string `json:"groupStepId"`
		}
		if err := json.Unmarshal(request.Body, &body); err != nil {
			return nil, nil, fmt.Errorf("SETUP_FAILURE: decode ConstructionCapabilities request: %w", err)
		}
		value, err := harness.Service.GetConstructionCapabilities(ctx, ConstructionCapabilitiesRequest{Project: identity.Project, ExplorerID: identity.ExplorerID, SnapshotToken: body.SnapshotToken, ExpectedDraftVersion: body.ExpectedDraftVersion, ExpectedDraftDigest: body.ExpectedDraftDigest, OutputID: body.OutputID, StageID: body.StageID, GroupStepID: body.GroupStepID})
		layers = append(layers, "lifecycle_analyzer_compiler")
		if err != nil {
			return resultError(err)
		}
		actual.Disposition = "CAPABILITIES"
		actual.Capabilities = &value
		attachAccepted()
		return actual, layers, nil
	default:
		return nil, nil, fmt.Errorf("GAP: no lifecycle adapter for emitted request kind=%q endpoint=%q method=%q", request.Kind, request.Endpoint, request.Method)
	}
}

func normalizeBuilderFullUIKind(kind string) string {
	kind = strings.ToLower(kind)
	kind = strings.ReplaceAll(kind, "-", "")
	kind = strings.ReplaceAll(kind, "_", "")
	return kind
}

func builderFullUIDispatchKind(kind string) string {
	switch normalizeBuilderFullUIKind(kind) {
	case "suggestions":
		return "suggestions"
	case "commands":
		return "commands"
	case "reconcile":
		return "reconcile"
	case "preview":
		return "preview"
	case "constructionproposal":
		return "construction-proposal"
	case "explicitgroups":
		return "explicit-groups"
	case "rowdefinitionproposal":
		return "row-definition-proposal"
	case "rowdefinitionchoices":
		return "row-definition-choices"
	default:
		return ""
	}
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

func builderFullUISerializedBody(request builderFullUIRequest) (string, bool, error) {
	if isJSONNullOrEmpty(request.BodyRaw) {
		return "", false, nil
	}
	var bodyRaw string
	if err := json.Unmarshal(request.BodyRaw, &bodyRaw); err != nil {
		return "", false, fmt.Errorf("request.bodyRaw must be null or a serialized body string: %w", err)
	}
	return bodyRaw, true, nil
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

func readBuilderFullUILines(path string) ([][]byte, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, "", err
	}
	if len(content) == 0 {
		return nil, "", fmt.Errorf("artifact %s is empty", path)
	}
	lines := make([][]byte, 0)
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			return nil, "", fmt.Errorf("artifact %s contains a blank line at %d", path, lineNumber)
		}
		if !json.Valid(line) {
			return nil, "", fmt.Errorf("artifact %s line %d is not valid JSON", path, lineNumber)
		}
		lines = append(lines, append([]byte(nil), line...))
	}
	if err := scanner.Err(); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(content)
	return lines, hex.EncodeToString(sum[:]), nil
}

func writeBuilderFullUIObservations(path string, header builderFullUIObservationHeader, rows []builderFullUIObservation) error {
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	if err := encoder.Encode(header); err != nil {
		return err
	}
	for _, row := range rows {
		if err := encoder.Encode(row); err != nil {
			return err
		}
	}
	return os.WriteFile(path, content.Bytes(), 0o600)
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
