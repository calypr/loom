package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

const (
	scopedCompileProject   = "project-a"
	scopedCompileExplorer  = "core-protocol"
	scopedCompileBasePath  = "/api/v1/projects/project-a/explorers/core-protocol/authoring/v2"
	scopedCompileExplorerT = "Core scoped compilation"

	scopedOutputPatients   = "patients"
	scopedOutputEncounters = "encounters"
	scopedOutputVisits     = "visits"
	scopedOutputConditions = "conditions"

	scopedPatientsTitle    = "Patients table"
	scopedEncountersTitle  = "Encounters table"
	scopedVisitsTitle      = "Visits table"
	scopedConditionsTitle  = "Conditions table"
	scopedPatientsRowLabel = "Patients"

	scopedPatientsColumnID    = "patient_id_column"
	scopedPatientsColumn      = "c_patient"
	scopedPatientsLabel       = "Patient ID"
	scopedVisitsColumnID      = "visit_subject_column"
	scopedVisitsColumn        = "c_visit_subject"
	scopedVisitsLabel         = "Visit Subject"
	scopedConditionsColumnID  = "condition_code_column"
	scopedConditionsColumn    = "c_condition"
	scopedConditionsLabel     = "Condition Code"
	scopedPatientsFieldPath   = "id"
	scopedLogicalTypeString   = "string"
	scopedEncounterStepID     = "encounter_subject_rows"
	scopedEncounterColumnID   = "encounter_subject"
	scopedEncounterColumn     = "encounter_subject"
	scopedEncounterLabel      = "Encounter Subject"
	scopedCombineEmissionCard = "optional_one"

	scopedUnknownOutputID = "core-protocol-ghost-output"
)

func scopedCompileTestWorkspace() authoringv2.Workspace {
	fieldColumn := func(columnID, column, label string) authoringv2.Column {
		return authoringv2.Column{
			ColumnID: columnID, Column: column, Label: label, LogicalType: scopedLogicalTypeString,
			OccurrenceID: authoringv2.RootOccurrenceID,
			Source:       authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: scopedPatientsFieldPath, ProjectionMode: "VALUE"}},
		}
	}
	rootDocument := func(outputID, title string, column authoringv2.Column) authoringv2.Document {
		return authoringv2.Document{
			Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(),
			Output:           authoringv2.Output{ID: outputID, Title: title, RowLabel: scopedPatientsRowLabel},
			RootResourceType: "Patient",
			Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
			Columns:          []authoringv2.Column{column},
		}
	}
	encounters := authoringv2.Document{
		Kind: authoringv2.Kind, Rows: authoringv2.RecordsRowDefinition(),
		Output:           authoringv2.Output{ID: scopedOutputEncounters, Title: scopedEncountersTitle, RowLabel: scopedPatientsRowLabel},
		RootResourceType: "Patient",
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"},
		Construction: &authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
			ID: scopedEncounterStepID,
			Inputs: []authoringv2.ConstructionInputRef{
				{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: scopedOutputPatients},
				{Kind: authoringv2.ConstructionInputWorkspaceOutput, OutputID: scopedOutputVisits},
			},
			Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationCombine, Combine: &authoringv2.ConstructionCombine{
				Kind: authoringv2.ConstructionCombineAppend,
				Projections: []authoringv2.ConstructionCombineProjection{
					{OutputColumnID: scopedEncounterColumnID, InputIndex: 0, InputColumnID: scopedPatientsColumnID},
					{OutputColumnID: scopedEncounterColumnID, InputIndex: 1, InputColumnID: scopedVisitsColumnID},
				},
			}},
			Outputs: []authoringv2.StageColumn{{ID: scopedEncounterColumnID, Name: scopedEncounterColumn, Label: scopedEncounterLabel, Type: scopedLogicalTypeString, Nullable: true}},
		}}},
	}
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer: authoringv2.ExplorerMetadata{Title: scopedCompileExplorerT},
		Documents: []authoringv2.Document{
			rootDocument(scopedOutputPatients, scopedPatientsTitle, fieldColumn(scopedPatientsColumnID, scopedPatientsColumn, scopedPatientsLabel)),
			encounters,
			rootDocument(scopedOutputVisits, scopedVisitsTitle, fieldColumn(scopedVisitsColumnID, scopedVisitsColumn, scopedVisitsLabel)),
			rootDocument(scopedOutputConditions, scopedConditionsTitle, fieldColumn(scopedConditionsColumnID, scopedConditionsColumn, scopedConditionsLabel)),
		},
		Tabs: []authoringv2.Tab{
			{ID: "patients-tab", Title: scopedPatientsTitle, OutputID: scopedOutputPatients, Order: 0, Visible: true},
			{ID: "encounters-tab", Title: scopedEncountersTitle, OutputID: scopedOutputEncounters, Order: 1, Visible: true},
			{ID: "visits-tab", Title: scopedVisitsTitle, OutputID: scopedOutputVisits, Order: 2, Visible: true},
			{ID: "conditions-tab", Title: scopedConditionsTitle, OutputID: scopedOutputConditions, Order: 3, Visible: true},
		},
	}
}

type scopedLiteralColumn struct {
	public      string
	label       string
	logicalType string
	nullable    *bool
	cardinality *string
}

func scopedBool(value bool) *bool       { return &value }
func scopedString(value string) *string { return &value }

var scopedLiteralColumns = map[string][]scopedLiteralColumn{
	scopedOutputPatients:   {{public: scopedPatientsColumn, label: scopedPatientsLabel, logicalType: scopedLogicalTypeString}},
	scopedOutputVisits:     {{public: scopedVisitsColumn, label: scopedVisitsLabel, logicalType: scopedLogicalTypeString}},
	scopedOutputConditions: {{public: scopedConditionsColumn, label: scopedConditionsLabel, logicalType: scopedLogicalTypeString}},
	scopedOutputEncounters: {{public: scopedEncounterColumn, label: scopedEncounterLabel, logicalType: scopedLogicalTypeString, nullable: scopedBool(true), cardinality: scopedString(scopedCombineEmissionCard)}},
}

var (
	scopedClosureEncounters = []string{scopedOutputEncounters, scopedOutputPatients, scopedOutputVisits}
	scopedClosureConditions = []string{scopedOutputConditions}
	scopedClosureFull       = []string{scopedOutputConditions, scopedOutputEncounters, scopedOutputPatients, scopedOutputVisits}
)

type scopedCompileFixture struct {
	t             *testing.T
	app           *fiber.App
	store         *testExplorerStore
	snapshot      capability.Snapshot
	workspace     authoringv2.Workspace
	initialDigest string
}

func newScopedCompileFixture(t *testing.T) *scopedCompileFixture {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	workspace := scopedCompileTestWorkspace()
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: scopedCompileProject, ExplorerID: scopedCompileExplorer, Title: scopedCompileExplorerT, ManagementMode: explorer.ManagementInteractive,
		DraftConfig: raw, DraftVersion: 1, DraftDigest: digest,
		DraftSnapshotToken: snapshot.Token, DraftSourceGeneration: snapshot.Identity.Generation,
		DraftAuthorizationScopeDigest: snapshot.Identity.AuthorizationScopeDigest,
	}); err != nil {
		t.Fatal(err)
	}
	domain, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	queryRows := func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		command := request.Command
		_, _, deriveErr := command.AQLParameters()
		if deriveErr != nil {
			return deriveErr
		}
		return nil
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{CompileTarget: compiler.ArangoTarget(), Registry: compilerTestRegistry{}, SourceRows: queryRows, PreviewSourceRows: queryRows})
	if err != nil {
		t.Fatal(err)
	}
	serviceConfig := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
			},
			Catalog: authoringV2Catalog,
		},
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, engine, domain, nil, nil)
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return domain.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			resolved, resolveErr := compileValidatedReceiptResolution(ctx, engine, receipt, bindings)
			if resolveErr != nil {
				return dataframeexecution.PreviewSummary{}, resolveErr
			}
			return engine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{
				Output: bindings.OutputNames[0], Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity,
			}, visit)
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, domain, serviceConfig)
	return &scopedCompileFixture{t: t, app: app, store: store, snapshot: snapshot, workspace: workspace, initialDigest: digest}
}

func (f *scopedCompileFixture) reconcile(outputID, requestID string) testHTTPResponse {
	f.t.Helper()
	body := fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":1,"draftDigest":%q}`, f.snapshot.Token, f.initialDigest)
	if outputID != "" {
		body = fmt.Sprintf(`{"snapshotToken":%q,"draftVersion":1,"draftDigest":%q,"outputId":%q}`, f.snapshot.Token, f.initialDigest, outputID)
	}
	response, err := coreProtocolDo(f.app, http.MethodPost, scopedCompileBasePath+"/reconcile", body, requestID)
	if err != nil {
		f.t.Fatal(err)
	}
	return response
}

func (f *scopedCompileFixture) scopedCompileDecodeReceipt(t *testing.T, response testHTTPResponse, stage string) (loomapi.CompileResponse, authoringv2.Workspace, *explorer.CompilationReceipt) {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s: reconcile status=%d body=%s", stage, response.StatusCode, response.Body)
	}
	var compiled loomapi.CompileResponse
	if err := json.Unmarshal([]byte(response.Body), &compiled); err != nil {
		t.Fatalf("%s: decode compile response: %v body=%s", stage, err, response.Body)
	}
	if compiled.ReceiptId == "" {
		t.Fatalf("%s: compile response has no receipt identity: %s", stage, response.Body)
	}
	persisted, ok := f.store.receipts[compiled.ReceiptId]
	if !ok {
		t.Fatalf("%s: receipt %s not persisted", stage, compiled.ReceiptId)
	}
	var decoded struct {
		Builder authoringv2.Workspace `json:"builder"`
	}
	if err := json.Unmarshal([]byte(response.Body), &decoded); err != nil {
		t.Fatalf("%s: decode builder workspace: %v", stage, err)
	}
	return compiled, decoded.Builder, &persisted
}

func scopedExpectCompleteWorkspace(t *testing.T, stage string, builder authoringv2.Workspace, initialDigest string) {
	t.Helper()
	if responseDigest, err := builder.Digest(); err != nil || responseDigest != initialDigest {
		t.Fatalf("%s: returned workspace digest=%v (err %v) does not match the validated head %s", stage, responseDigest, err, initialDigest)
	}
	if len(builder.Documents) != 4 {
		t.Fatalf("%s: returned workspace must be the complete saved head: %#v", stage, builder.Documents)
	}
	seen := map[string]bool{}
	for _, document := range builder.Documents {
		seen[document.Output.ID] = true
	}
	for _, want := range scopedClosureFull {
		if !seen[want] {
			t.Fatalf("%s: returned workspace is missing output %q: %v", stage, want, seen)
		}
	}
	if len(builder.Tabs) != 4 {
		t.Fatalf("%s: returned workspace lost tabs: %#v", stage, builder.Tabs)
	}
}

func scopedSortedNames(values []string) []string {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	return sorted
}

func scopedExpectStrings(t *testing.T, stage, component string, got, want []string) {
	t.Helper()
	sortedGot, sortedWant := scopedSortedNames(got), scopedSortedNames(want)
	if !reflect.DeepEqual(sortedGot, sortedWant) {
		t.Fatalf("%s: %s membership = %v, want %v", stage, component, sortedGot, sortedWant)
	}
}

func scopedExpectReceipt(t *testing.T, stage string, receipt *explorer.CompilationReceipt, wantOutputs, absentTokens []string) {
	t.Helper()
	bundleNames := make([]string, 0, len(receipt.Bundle.Outputs))
	byName := map[string]recipe.Output{}
	for _, output := range receipt.Bundle.Outputs {
		bundleNames = append(bundleNames, output.Name)
		byName[output.Name] = output
	}
	scopedExpectStrings(t, stage, "recipe bundle outputs", bundleNames, wantOutputs)

	emittedOutputs := make([]string, 0, len(receipt.EmittedColumns))
	emittedByOutput := map[string][]explorer.EmittedColumn{}
	for _, emitted := range receipt.EmittedColumns {
		emittedOutputs = append(emittedOutputs, emitted.OutputID)
		emittedByOutput[emitted.OutputID] = append(emittedByOutput[emitted.OutputID], emitted)
	}
	scopedExpectStrings(t, stage, "emitted column outputs", emittedOutputs, wantOutputs)

	fingerprintOutputs := make([]string, 0, len(receipt.OutputFingerprints))
	for output := range receipt.OutputFingerprints {
		fingerprintOutputs = append(fingerprintOutputs, output)
	}
	scopedExpectStrings(t, stage, "output execution fingerprints", fingerprintOutputs, wantOutputs)

	schemaOutputs := make([]string, 0, len(receipt.CompiledOutputSchemas))
	for output := range receipt.CompiledOutputSchemas {
		schemaOutputs = append(schemaOutputs, output)
	}
	scopedExpectStrings(t, stage, "compiled output schemas", schemaOutputs, wantOutputs)

	contracts, err := explorer.DecodePublicOutputContracts(receipt.PublicOutputContract)
	if err != nil {
		t.Fatalf("%s: decode public output contracts: %v", stage, err)
	}
	contractOutputs := make([]string, 0, len(contracts.Outputs))
	contractsByID := map[string]explorer.PublicOutputContract{}
	for _, contract := range contracts.Outputs {
		contractOutputs = append(contractOutputs, contract.OutputID)
		contractsByID[contract.OutputID] = contract
	}
	scopedExpectStrings(t, stage, "public output contracts", contractOutputs, wantOutputs)

	wanted := map[string]bool{}
	for _, output := range wantOutputs {
		wanted[output] = true
	}
	for _, output := range wantOutputs {
		for _, want := range scopedLiteralColumns[output] {
			var matched *explorer.EmittedColumn
			for index := range emittedByOutput[output] {
				if emittedByOutput[output][index].PublicColumn == want.public {
					matched = &emittedByOutput[output][index]
				}
			}
			if matched == nil {
				t.Fatalf("%s: output %q lacks emitted contract column %q: %#v", stage, output, want.public, emittedByOutput[output])
			}
			if matched.Label != want.label || matched.LogicalType != want.logicalType {
				t.Fatalf("%s: output %q emitted column %q = label %q type %q, want label %q type %q", stage, output, want.public, matched.Label, matched.LogicalType, want.label, want.logicalType)
			}
			if want.nullable != nil && matched.Nullable != *want.nullable {
				t.Fatalf("%s: output %q emitted column %q nullable=%v want %v", stage, output, want.public, matched.Nullable, *want.nullable)
			}
			if want.cardinality != nil && matched.Cardinality != *want.cardinality {
				t.Fatalf("%s: output %q emitted column %q cardinality=%q want %q", stage, output, want.public, matched.Cardinality, *want.cardinality)
			}
			if matched.OutputID != output {
				t.Fatalf("%s: emitted column %q belongs to output %q not %q", stage, want.public, matched.OutputID, output)
			}
			var contractColumn *explorer.PublicOutputColumn
			contract := contractsByID[output]
			for index := range contract.Columns {
				if contract.Columns[index].Column == want.public {
					contractColumn = &contract.Columns[index]
				}
			}
			if contractColumn == nil {
				t.Fatalf("%s: output %q public contract lacks column %q: %#v", stage, output, want.public, contract.Columns)
			}
			if contractColumn.Label != want.label || contractColumn.LogicalType != want.logicalType {
				t.Fatalf("%s: output %q contract column %q = label %q type %q, want label %q type %q", stage, output, want.public, contractColumn.Label, contractColumn.LogicalType, want.label, want.logicalType)
			}
		}
		if len(contractsByID[output].Columns) != len(scopedLiteralColumns[output]) {
			t.Fatalf("%s: output %q contract columns = %#v, want exactly the literal set", stage, output, contractsByID[output].Columns)
		}

		outputRecipe, found := byName[output]
		if !found {
			t.Fatalf("%s: recipe bundle lacks output %q", stage, output)
		}
		if output == scopedOutputEncounters {
			scopedExpectCombineRecipe(t, stage, outputRecipe)
			continue
		}
		if outputRecipe.Construction != nil {
			t.Fatalf("%s: field-backed output %q carries a construction recipe: %#v", stage, output, outputRecipe.Construction)
		}
		if len(outputRecipe.Fields) != 1 || outputRecipe.Fields[0].Name != scopedLiteralColumns[output][0].public {
			t.Fatalf("%s: field-backed output %q recipe fields = %#v, want the single authored projection", stage, output, outputRecipe.Fields)
		}
	}

	for output := range receipt.ConstructionStages {
		if !wanted[output] {
			t.Fatalf("%s: receipt carries construction stages for %q outside the requested closure %v", stage, output, wantOutputs)
		}
	}
	if wanted[scopedOutputEncounters] {
		stages := receipt.ConstructionStages[scopedOutputEncounters]
		if len(stages) != 1 || stages[0].ID != scopedEncounterStepID || stages[0].InputStageID != "" || stages[0].Operation != "APPEND" {
			t.Fatalf("%s: encounters construction stages = %#v, want the single terminal APPEND step %q", stage, stages, scopedEncounterStepID)
		}
	}

	bundleJSON, err := json.Marshal(receipt.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	denyScopes := [][]byte{bundleJSON, receipt.NormalizedBundle, receipt.PublicOutputContract}
	emittedJSON, err := json.Marshal(receipt.EmittedColumns)
	if err != nil {
		t.Fatal(err)
	}
	denyScopes = append(denyScopes, emittedJSON)
	for _, token := range absentTokens {
		marker := `"` + token + `"`
		for _, scope := range denyScopes {
			if strings.Contains(string(scope), marker) {
				t.Fatalf("%s: receipt unexpectedly contains %q: %s", stage, marker, string(scope))
			}
		}
	}
}

func scopedExpectCombineRecipe(t *testing.T, stage string, output recipe.Output) {
	t.Helper()
	if output.Construction == nil || len(output.Construction.Steps) != 1 {
		t.Fatalf("%s: encounters recipe lost its terminal combine: %#v", stage, output.Construction)
	}
	step := output.Construction.Steps[0]
	if step.ID != scopedEncounterStepID {
		t.Fatalf("%s: encounters combine step id=%q want %q", stage, step.ID, scopedEncounterStepID)
	}
	wantInputs := []recipe.ConstructionInputRef{
		{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: scopedOutputPatients},
		{Kind: recipe.ConstructionWorkspaceOutputInput, OutputID: scopedOutputVisits},
	}
	if !reflect.DeepEqual(step.Inputs, wantInputs) {
		t.Fatalf("%s: encounters combine inputs = %#v, want %#v", stage, step.Inputs, wantInputs)
	}
	if step.Operation.Combine == nil || step.Operation.Combine.Kind != recipe.ConstructionCombineAppend {
		t.Fatalf("%s: encounters combine kind = %#v, want terminal APPEND", stage, step.Operation.Combine)
	}
	wantProjections := []recipe.ConstructionCombineProjection{
		{OutputColumnID: scopedEncounterColumnID, InputIndex: 0, InputColumnID: scopedPatientsColumnID},
		{OutputColumnID: scopedEncounterColumnID, InputIndex: 1, InputColumnID: scopedVisitsColumnID},
	}
	if !reflect.DeepEqual(step.Operation.Combine.Projections, wantProjections) {
		t.Fatalf("%s: encounters combine projections = %#v, want %#v", stage, step.Operation.Combine.Projections, wantProjections)
	}
}

func TestCoreAuthoringProtocolOutputScopedCompilation(t *testing.T) {
	fixture := newScopedCompileFixture(t)

	first := fixture.reconcile(scopedOutputEncounters, "c06-scoped-encounters")
	compiledEncounters, builderEncounters, receiptEncounters := fixture.scopedCompileDecodeReceipt(t, first, "output-scoped encounters reconcile")
	scopedExpectReceipt(t, "output-scoped encounters reconcile", receiptEncounters, scopedClosureEncounters, []string{scopedOutputConditions})
	scopedExpectCompleteWorkspace(t, "output-scoped encounters reconcile", builderEncounters, fixture.initialDigest)
	responseEncounters := make([]string, 0, len(compiledEncounters.Outputs))
	for _, receiptOutput := range compiledEncounters.Outputs {
		responseEncounters = append(responseEncounters, receiptOutput.OutputId)
	}
	scopedExpectStrings(t, "output-scoped encounters reconcile", "response outputs", responseEncounters, scopedClosureEncounters)
	if len(fixture.store.receipts) != 1 {
		t.Fatalf("output-scoped encounters reconcile persisted %d receipts, want exactly 1", len(fixture.store.receipts))
	}

	second := fixture.reconcile(scopedOutputConditions, "c06-scoped-conditions")
	compiledConditions, builderConditions, receiptConditions := fixture.scopedCompileDecodeReceipt(t, second, "output-scoped conditions reconcile")
	scopedExpectReceipt(t, "output-scoped conditions reconcile", receiptConditions, scopedClosureConditions, []string{scopedOutputEncounters, scopedOutputPatients, scopedOutputVisits})
	scopedExpectCompleteWorkspace(t, "output-scoped conditions reconcile", builderConditions, fixture.initialDigest)
	if len(compiledConditions.Outputs) != 1 || compiledConditions.Outputs[0].OutputId != scopedOutputConditions {
		t.Fatalf("output-scoped conditions response outputs = %v, want conditions only", compiledConditions.Outputs)
	}
	if compiledConditions.ReceiptId == compiledEncounters.ReceiptId {
		t.Fatal("the unrelated output reconcile reused the encounters receipt identity")
	}
	if len(fixture.store.receipts) != 2 {
		t.Fatalf("output-scoped conditions reconcile persisted %d receipts, want exactly 2", len(fixture.store.receipts))
	}

	full := fixture.reconcile("", "c06-full-workspace")
	compiledFull, builderFull, receiptFull := fixture.scopedCompileDecodeReceipt(t, full, "complete workspace reconcile")
	scopedExpectReceipt(t, "complete workspace reconcile", receiptFull, scopedClosureFull, nil)
	scopedExpectCompleteWorkspace(t, "complete workspace reconcile", builderFull, fixture.initialDigest)
	if compiledFull.ReceiptId == compiledEncounters.ReceiptId || compiledFull.ReceiptId == compiledConditions.ReceiptId {
		t.Fatal("the complete workspace reconcile reused a scoped receipt identity")
	}
	if receiptFull.IntentDigest == receiptEncounters.IntentDigest || receiptFull.IntentDigest == receiptConditions.IntentDigest {
		t.Fatal("the complete workspace reconcile reused a scoped compile intent")
	}
	if len(fixture.store.receipts) != 3 {
		t.Fatalf("the three successful reconciles persisted %d receipts, want exactly 3", len(fixture.store.receipts))
	}
	if receiptFull.RecipeDigest == receiptEncounters.RecipeDigest {
		t.Fatal("the complete workspace recipe digest matches the scoped encounters recipe digest")
	}

	receiptsBefore := len(fixture.store.receipts)
	unknown := fixture.reconcile(scopedUnknownOutputID, "c06-scoped-unknown")
	coreProtocolBoundaryFailure(t, unknown, "output-scoped reconcile of unknown outputId", http.StatusUnprocessableEntity, "INVALID_OUTPUT_SCOPE", "reconcile", scopedUnknownOutputID)
	if len(fixture.store.receipts) != receiptsBefore {
		t.Fatalf("unknown outputId reconcile persisted %d receipts", len(fixture.store.receipts)-receiptsBefore)
	}
	for _, id := range []string{compiledEncounters.ReceiptId, compiledConditions.ReceiptId, compiledFull.ReceiptId} {
		if _, ok := fixture.store.receipts[id]; !ok {
			t.Fatalf("prior accepted receipt %s no longer persisted after the rejection", id)
		}
	}
	if stored := fixture.store.explorers[testExplorerKey(scopedCompileProject, scopedCompileExplorer)]; stored.DraftVersion != 1 || stored.DraftDigest != fixture.initialDigest {
		t.Fatalf("rejected reconcile moved the saved head: %+v", stored)
	}
}
