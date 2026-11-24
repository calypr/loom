package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringcore"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	explorerfixtures "github.com/calypr/loom/internal/testkit/explorerfixtures"
)

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
	capabilities, err := harness.Service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
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

func (s builderFullUISeed) key() builderFullUIKey {
	return builderFullUIKey{CaseID: s.CaseID, EntryID: s.EntryID, ActionIndex: s.ActionIndex}
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
	if err := writeLifecycleJSONL(preparedPath, header, rows); err != nil {
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
	var capabilities construction.ConstructionCapabilitiesResponse
	if capRequest != nil {
		capabilities, err = harness.Service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
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

func builderFullUIRegisteredSeedFixture(entryID, seedKind string, state builderFullUIAbstractState) (builderFullUICapabilityFixture, ws.Workspace, json.RawMessage, error) {
	if seedKind == "first_table_creation_initial_state" {
		if entryID != "builder.first_table_from_root" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: empty-root fixture is only registered for exact entryId %q", entryID)
		}
		if state.SeedProfile != "empty-root" || state.WorkspacePhase != "root-uninitialized" || state.AcceptedWorkspace != nil || state.RootContext == nil {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: first-table seed requires exact empty-root ModelState with rootContext")
		}
		return builderFullUIEmptyRootFixture()
	}
	if state.WorkspacePhase != "workspace-loaded" || state.AcceptedWorkspace == nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: seed kind %q needs a workspace-loaded before state; got phase=%q", seedKind, state.WorkspacePhase)
	}
	switch seedKind {
	case "builder.group_filter_derive.edit_saved_group":
		steps := state.AcceptedWorkspace.Construction.Steps
		if state.SeedProfile != "group-filter-derive" || state.AcceptedWorkspace.RootType != "Patient" || len(steps) != 3 || steps[0].ID != "group-step" || steps[0].Operation.Kind != "GROUP" || steps[1].ID != "filter-step" || steps[1].Operation.Kind != "FILTER" || steps[2].ID != "derive-step" || steps[2].Operation.Kind != "DERIVE" || state.SelectedStage == nil || state.SelectedStage.ID != "derive-step" || state.SelectedStage.Operation != "DERIVE" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: seed kind %q is not paired with the exact group-filter-derive ModelState profile", seedKind)
		}
		return builderFullUIGroupFilterDeriveFixture()
	case "rooted_table_filter":
		if entryID != "builder.filter_rows.scalar_condition" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: scalar FILTER fixture does not cover exact entryId %q", entryID)
		}
		if state.SeedProfile != "rooted-source" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID || state.SelectedStage.Operation != "SOURCE_PROJECTION" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: entryId %q requires the rooted-source empty-construction state; profile=%q stage=%#v", entryID, state.SeedProfile, state.SelectedStage)
		}
		return builderFullUIScalarFixture()
	case "rooted_table_calculate":
		if entryID != "builder.calculate.derive" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: scalar DERIVE fixture does not cover exact entryId %q", entryID)
		}
		if state.SeedProfile != "rooted-source" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID || state.SelectedStage.Operation != "SOURCE_PROJECTION" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: entryId %q requires the rooted-source empty-construction state", entryID)
		}
		return builderFullUIScalarFixture()
	case "rooted_table_add_columns":
		if entryID != "builder.add_columns.catalog_field" && entryID != "builder.add_columns.source_selection" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: source projection fixture does not cover exact add-column entryId %q", entryID)
		}
		if state.SeedProfile != "rooted-source" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: entryId %q requires the rooted-source empty-construction state", entryID)
		}
		return builderFullUIScalarFixture()
	case "construction_proposal_lifecycle_initial_state", "construction_step_editor_routing_initial_state", "automatic_preview_lifecycle_initial_state", "automatic_preview_refresh_and_recovery", "one_step_draft_restore_initial_state", "row_root_change_review_initial_state", "server_draft_and_reconciliation_initial_state":
		if seedKind != "construction_proposal_lifecycle_initial_state" || entryID != "builder.construction_proposal_apply_cancel_retry" || state.SeedProfile != "saved-construction" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 1 || state.AcceptedWorkspace.Construction.Steps[0].ID != "saved-filter-step" || state.AcceptedWorkspace.Construction.Steps[0].Operation.Kind != "FILTER" || state.SelectedStage == nil || state.SelectedStage.ID != "saved-filter-step" || state.SelectedStage.Operation != "FILTER" {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: saved FILTER lifecycle fixture does not cover exact entryId %q and before-state", entryID)
		}
		return builderFullUISavedFilterFixture()
	case "rooted_table_row_definition":
		if entryID != "builder.row_definition.expanded_source_rows" || state.SeedProfile != "rooted-source" || state.AcceptedWorkspace.RootType != "Patient" || len(state.AcceptedWorkspace.Construction.Steps) != 0 || state.SelectedStage == nil || state.SelectedStage.ID != recipe.ConstructionSourceProjectionID {
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: expanded row-definition fixture does not cover exact entryId %q and before-state", entryID)
		}
		return builderFullUIRowDefinitionFixture()
	case "two_table_combine":
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: no catalog-consistent two-source workspace and combine receipt fixture is registered")
	default:
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("GAP: backend fixture registry has no entry for seed kind %q", seedKind)
	}
}

func builderFullUIFixtureColumnBindings(modelState json.RawMessage, workspace ws.Workspace, capabilities construction.ConstructionCapabilitiesResponse, fixture builderFullUICapabilityFixture) (builderFullUIFixtureBindings, error) {
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

func builderFullUIGroupFilterDeriveFixture() (builderFullUICapabilityFixture, ws.Workspace, json.RawMessage, error) {
	const project, explorerID, outputID, nodeID = "HTAN_INT/BForePC", "patients", "patients", "node-patient"
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("generated schema index: %w", err)
	}
	gender, err := index.ResolveRowPath(fhirschema.DefinitionName("Patient"), "gender")
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	age, err := index.ResolveRowPath(fhirschema.DefinitionName("Patient"), "multipleBirthInteger")
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	if gender.Shape != fhirschema.RowPathScalar || gender.Cardinality != fhirschema.RowCardinalityOne || gender.FHIRType != "string" ||
		age.Shape != fhirschema.RowPathScalar || age.Cardinality != fhirschema.RowCardinalityOne || age.FHIRType != "integer" {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("unexpected generated Patient field facts: gender=%#v multipleBirthInteger=%#v", gender, age)
	}
	workspace := builderFullUIGroupFilterDeriveWorkspace()
	if err := workspace.Validate(); err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("workspace validation: %w", err)
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
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	return fixture, workspace, provenance, nil
}

func builderFullUIScalarFixture() (builderFullUICapabilityFixture, ws.Workspace, json.RawMessage, error) {
	fixture, _, provenance, err := builderFullUIGroupFilterDeriveFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("generated schema index: %w", err)
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
			return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("generated Patient source %s row facts=%#v err=%v, want scalar %s %s", field.path, facts, resolveErr, field.wantType, field.wantCardinality)
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
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("scalar source workspace validation: %w", err)
	}
	fixture.Catalog = traceWorkspaceCatalog(fixture.Snapshot, fixture.ExplorerID, workspace, "node-patient")
	fixture.CapabilityRequest = &builderFullUICapabilityRequest{StageID: recipe.ConstructionSourceProjectionID}
	var provenanceValue map[string]any
	if err := json.Unmarshal(provenance, &provenanceValue); err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("decode generated schema provenance: %w", err)
	}
	provenanceValue["fields"] = provenanceFields
	provenance, err = json.Marshal(provenanceValue)
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	return fixture, workspace, provenance, nil
}

func builderFullUIEmptyRootFixture() (builderFullUICapabilityFixture, ws.Workspace, json.RawMessage, error) {
	fixture, _, provenance, err := builderFullUIScalarFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	fixture.NoSavedWorkspace = true
	fixture.AcceptedVersion = 0
	fixture.OutputID = ""
	fixture.CapabilityRequest = nil
	empty := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Builder full UI replay"}, Documents: []ws.Document{}, Tabs: []ws.Tab{},
	}
	fixture.Catalog = traceWorkspaceCatalog(fixture.Snapshot, fixture.ExplorerID, empty, "node-patient")
	return fixture, ws.Workspace{}, provenance, nil
}

func builderFullUISavedFilterFixture() (builderFullUICapabilityFixture, ws.Workspace, json.RawMessage, error) {
	fixture, workspace, provenance, err := builderFullUIScalarFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	doc := &workspace.Documents[0]
	outputs := make([]ws.StageColumn, 0, len(doc.Columns))
	for _, column := range doc.Columns {
		outputs = append(outputs, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	doc.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID: "saved-filter-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "age-id", Operator: ws.ConstructionFilterExists}},
		Outputs:   outputs,
	}}}
	if err := workspace.Validate(); err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("saved scalar FILTER workspace validation: %w", err)
	}
	fixture.Catalog = traceWorkspaceCatalog(fixture.Snapshot, fixture.ExplorerID, workspace, "node-patient")
	fixture.CapabilityRequest = &builderFullUICapabilityRequest{StageID: "saved-filter-step"}
	return fixture, workspace, provenance, nil
}

func builderFullUIRowDefinitionFixture() (builderFullUICapabilityFixture, ws.Workspace, json.RawMessage, error) {
	fixture, workspace, _, err := builderFullUIScalarFixture()
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	facts, err := index.ResolveRowPath(fhirschema.DefinitionName("Patient"), "name[].family")
	if err != nil || facts.Shape != fhirschema.RowPathScalar || facts.Cardinality != fhirschema.RowCardinalityMany || facts.FHIRType != "string" {
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, fmt.Errorf("generated Patient name[].family row facts=%#v err=%v", facts, err)
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
		return builderFullUICapabilityFixture{}, ws.Workspace{}, nil, err
	}
	return fixture, workspace, provenanceValue, nil
}

func builderFullUIScalarWorkspace() ws.Workspace {
	workspace := builderFullUIGroupFilterDeriveWorkspace()
	document := &workspace.Documents[0]
	document.Construction = &ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{}}
	document.Columns = append([]ws.Column{
		{ColumnID: "patient-id", Column: "patient_id", Label: "Patient ID", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "VALUE"}}},
		{ColumnID: "active-id", Column: "active", Label: "Active", LogicalType: "boolean", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "active", ProjectionMode: "VALUE"}}},
		{ColumnID: "birth-date-id", Column: "birth_date", Label: "Birth date", LogicalType: "date", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "birthDate", ProjectionMode: "VALUE"}}},
	}, document.Columns...)
	return workspace
}

func builderFullUIGroupFilterDeriveWorkspace() ws.Workspace {
	one := int64(1)
	group := []ws.StageColumn{
		{ID: "gender-group", Name: "gender_group", Label: "Gender", Type: "string"},
		{ID: "row-count", Name: "row_count", Label: "Row count", Type: "integer"},
	}
	filter := append([]ws.StageColumn(nil), group...)
	derive := append(append([]ws.StageColumn(nil), filter...), ws.StageColumn{ID: "adjusted", Name: "adjusted", Label: "Adjusted", Type: "integer"})
	construction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{
		{ID: "group-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationGroup, Group: &ws.ConstructionGroup{
			ConstructionID: "group-step", MissingKeyPolicy: ws.ConstructionGroupMissingKeyGroup,
			Keys:       []ws.ConstructionGroupKey{{InputColumnID: "gender-id", OutputColumnID: "gender-group"}},
			Aggregates: []ws.ConstructionGroupAggregate{{Operation: ws.ConstructionGroupCountRows, OutputColumnID: "row-count"}},
		}}, Outputs: group},
		{ID: "filter-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "group-step"}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationFilter, Filter: &ws.ConstructionFilter{ColumnID: "row-count", Operator: ws.ConstructionFilterExists}}, Outputs: filter},
		{ID: "derive-step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "filter-step"}}, Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
			ConstructionID: "derive-step", OutputColumnID: "adjusted", Operation: ws.ConstructionDerivedAdd,
			Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: "row-count"},
			Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &one}},
			MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
		}}, Outputs: derive},
	}}
	doc := ws.Document{
		Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient",
		Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition(),
		Columns: []ws.Column{
			{ColumnID: "gender-id", Column: "gender", Label: "Gender", LogicalType: "string", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "gender", ProjectionMode: "VALUE"}}},
			{ColumnID: "age-id", Column: "age", Label: "Age", LogicalType: "integer", OccurrenceID: ws.RootOccurrenceID, Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "multipleBirthInteger", ProjectionMode: "VALUE"}}},
		}, Construction: &construction,
	}
	return ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion,
		Explorer: ws.ExplorerMetadata{Title: "Builder full UI replay"}, Documents: []ws.Document{doc},
		Tabs: []ws.Tab{{ID: "patients_tab", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
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
	preparedWorkspace, err := ws.DecodeWorkspace(prepared.AcceptedWorkspace)
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
		capabilities, capabilityErr := harness.Service.GetConstructionCapabilities(context.Background(), construction.ConstructionCapabilitiesRequest{
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

func newBuilderFullUIHarness(fixture builderFullUICapabilityFixture, workspace ws.Workspace) (*builderFullUIHarness, error) {
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
	config.Capability.Catalog = func(capability.Snapshot, string) ws.CatalogSnapshot { return fixture.Catalog }
	config.RowChoicePlanner, config.RowChoiceResolver = resolver, resolver
	config.CompileReceipt = func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
		receipt, compileErr := compileTraceReceipt(ctx, request, fixture.Snapshot, fixture.Catalog)
		if compileErr == nil {

			receipt.RowDefinitionProposal = authoringcore.CloneRowDefinitionProposalBinding(request.RowDefinitionProposal)
			if request.RowDefinitionProposal != nil {
				receipt.CompilationKey, compileErr = receiptmodel.CompilationKey(*receipt)
				if compileErr == nil {
					receipt.ID, compileErr = receiptmodel.ReceiptID(*receipt)
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
	config.PreviewReceipt = func(_ context.Context, receipt *receiptmodel.CompilationReceipt, _ recipe.RuntimeBindings, _ func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
		if receipt == nil {
			return dataframeexecution.PreviewSummary{}, fmt.Errorf("preview received no compiled receipt")
		}
		return dataframeexecution.PreviewSummary{Output: fixture.OutputID, Columns: emittedPublicColumnsForOutput(receipt, fixture.OutputID), Complete: true}, nil
	}
	domain, err := explorerfixtures.NewService(store)
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
