package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

type filterSourceChoiceProbe struct {
	ChoiceID    *string `json:"choiceId"`
	FieldPath   *string `json:"fieldPath"`
	Label       *string `json:"label"`
	LogicalType *string `json:"logicalType"`
	IsPopulated *bool   `json:"isPopulated"`
	ColumnID    *string `json:"columnId"`
}

type filterSourceProbe struct {
	Supported  *bool                     `json:"supported"`
	StageID    *string                   `json:"stageId"`
	ReasonCode *string                   `json:"reasonCode"`
	Reason     *string                   `json:"reason"`
	Choices    []filterSourceChoiceProbe `json:"choices"`
}

func decodedFilterSourceInput(t *testing.T, body string) (map[string]json.RawMessage, *filterSourceProbe) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode capabilities envelope: %v body=%s", err, body)
	}
	raw, ok := envelope["filterSourceInput"]
	if !ok {
		t.Fatalf("capabilities wire response dropped filterSourceInput: %s", body)
	}
	var input filterSourceProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}

func TestConstructionFilterSourceHTTPContractPreservesCapabilitiesAndProposals(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_active", NodeID: "n_patient", ResourceType: "Patient",
		FieldPath: "active", Label: "Active", LogicalType: "boolean", Cardinality: "OPTIONAL_ONE",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 135,
	})
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID: "only_step", Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind:   authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{ColumnID: columns[0].ID, Operator: authoringv2.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate staged filter-source workspace: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorer.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      compilerTestRegistry{},
		QueryRows: func(ctx context.Context, command command.CompiledCommand, _ int, visit dataframeexecution.RowVisitor) error {
			_, _, deriveErr := command.AQLParameters()
			if deriveErr != nil {
				return deriveErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	rowChoices, err := lifecycle.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	readScope := authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}
	config := lifecycle.Config{
		Capability: lifecycle.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (lifecycle.AuthorizedCapability, error) {
				return lifecycle.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: authoringV2Catalog,
		},
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		RowChoicePlanner:  rowChoices,
		RowChoiceResolver: rowChoices,
		CompileReceipt: func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
			return compileExplorerReceipt(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(previewCtx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || receipt.ConstructionProposal == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "patients" {
				t.Fatalf("unexpected filter-source candidate preview receipt/bindings: %#v / %#v", receipt, bindings)
			}
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, Complete: true, PartialValidation: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"

	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection"}`,
		snapshot.Token, digest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("filter-source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	inputKeys, input := decodedFilterSourceInput(t, capabilitiesHTTP.Body)
	for _, key := range []string{"supported", "stageId", "choices"} {
		if _, ok := inputKeys[key]; !ok {
			t.Fatalf("filterSourceInput omitted required key %q: %s", key, capabilitiesHTTP.Body)
		}
	}
	if input.Supported == nil || !*input.Supported {
		t.Fatalf("filter-source capability was not advertised for the populated root scalar snapshot: %s", capabilitiesHTTP.Body)
	}
	if input.StageID == nil || *input.StageID != "source_projection" {
		t.Fatalf("filterSourceInput.stageId = %v, want the requested capability stage", input.StageID)
	}
	if _, leaked := inputKeys["reasonCode"]; leaked {
		t.Fatalf("supported filterSourceInput leaked reasonCode: %s", capabilitiesHTTP.Body)
	}
	if _, leaked := inputKeys["reason"]; leaked {
		t.Fatalf("supported filterSourceInput leaked reason: %s", capabilitiesHTTP.Body)
	}
	if len(input.Choices) != 2 || input.Choices[0].FieldPath == nil || *input.Choices[0].FieldPath != "active" ||
		input.Choices[1].FieldPath == nil || *input.Choices[1].FieldPath != "id" {
		t.Fatalf("filterSourceInput.choices = %#v, want the authorized active and id root scalars in fieldPath order", input.Choices)
	}
	choice := input.Choices[0]
	for _, rawChoice := range rawFilterSourceChoices(t, capabilitiesHTTP.Body) {
		var choiceKeys map[string]json.RawMessage
		if err := json.Unmarshal(rawChoice, &choiceKeys); err != nil {
			t.Fatal(err)
		}
		if len(choiceKeys) != 6 {
			t.Fatalf("filter source choice keys = %d, want exactly the six contract keys: %s", len(choiceKeys), string(rawChoice))
		}
		for _, key := range []string{"choiceId", "fieldPath", "label", "logicalType", "isPopulated", "columnId"} {
			if _, ok := choiceKeys[key]; !ok {
				t.Fatalf("filter source choice omitted contract key %q: %s", key, string(rawChoice))
			}
		}
	}
	if choice.ChoiceID == nil || *choice.ChoiceID == "" {
		t.Fatalf("filter source choice lacked its signed choiceId: %s", capabilitiesHTTP.Body)
	}
	if choice.Label == nil || *choice.Label != "Active" || choice.LogicalType == nil || *choice.LogicalType != "boolean" {
		t.Fatalf("filter source choice facts = %#v, want the snapshot candidate Title Active/boolean", choice)
	}
	if choice.IsPopulated == nil || !*choice.IsPopulated {
		t.Fatalf("filter source choice lost the authorized populated fact: %#v", choice)
	}
	if choice.ColumnID == nil || *choice.ColumnID != "" {
		t.Fatalf("filter source choice columnId = %v, want an honest empty string for a fresh Filter step", choice.ColumnID)
	}
	choiceID := *choice.ChoiceID

	projectionColumnID := "filter_source_active_patient"
	candidateConstruction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID:     "only_step",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{
				ColumnID: projectionColumnID, Operator: authoringv2.ConstructionFilterEquals,
				Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterBoolean, Boolean: filterSourceHTTPBool(true)}},
			},
		},
		Outputs: append(append([]authoringv2.StageColumn(nil), columns...), authoringv2.StageColumn{
			ID: projectionColumnID, Name: authoringv2.ConstructionSourceProjectionName(projectionColumnID), Label: "Active", Type: "boolean",
		}),
	}}}
	candidateJSON, err := json.Marshal(candidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	storedBefore, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}

	validProposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"only_step","candidateConstruction":%s,"filterSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID, projectionColumnID,
	))
	if validProposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("filter-source proposal status=%d body=%s", validProposalHTTP.StatusCode, validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"sourceProjections"`)) ||
		!bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"fieldPath":"active"`)) {
		t.Fatalf("filter-source selection did not reach the lifecycle entry and survive into the candidate: %s", validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(fmt.Sprintf(`"columnId":%q`, projectionColumnID))) {
		t.Fatalf("materialized projection lost the edited Filter column identity: %s", validProposalHTTP.Body)
	}

	duplicateHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"only_step","candidateConstruction":%s,"filterSources":[{"choiceId":%q,"columnId":%q},{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID, projectionColumnID, choiceID, projectionColumnID,
	))
	if duplicateHTTP.StatusCode != http.StatusBadRequest && duplicateHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate filterSources status=%d, want a request validation error: %s", duplicateHTTP.StatusCode, duplicateHTTP.Body)
	}
	if !bytes.Contains([]byte(duplicateHTTP.Body), []byte("duplicate")) {
		t.Fatalf("duplicate filterSources rejection did not come from the lifecycle entry: %s", duplicateHTTP.Body)
	}

	emptyColumnHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"only_step","candidateConstruction":%s,"filterSources":[{"choiceId":%q,"columnId":""}]}`,
		snapshot.Token, digest, candidateJSON, choiceID,
	))
	if emptyColumnHTTP.StatusCode != http.StatusBadRequest && emptyColumnHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty filterSources columnId status=%d, want a request validation error: %s", emptyColumnHTTP.StatusCode, emptyColumnHTTP.Body)
	}
	if !bytes.Contains([]byte(emptyColumnHTTP.Body), []byte("columnId")) {
		t.Fatalf("empty filterSources columnId rejection did not identify the invalid field: %s", emptyColumnHTTP.Body)
	}

	forgedTokenHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"only_step","candidateConstruction":%s,"filterSources":[{"choiceId":"forged-choice","columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, projectionColumnID,
	))
	if forgedTokenHTTP.StatusCode != http.StatusConflict {
		t.Fatalf("forged filterSources token status=%d, want the conflict refusal from the lifecycle entry: %s", forgedTokenHTTP.StatusCode, forgedTokenHTTP.Body)
	}
	if !bytes.Contains([]byte(forgedTokenHTTP.Body), []byte("INVALID_FILTER_SOURCE_CHOICE")) {
		t.Fatalf("forged filterSources token did not reach choice validation: %s", forgedTokenHTTP.Body)
	}

	storedAfter, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if storedAfter.DraftVersion != storedBefore.DraftVersion || storedAfter.DraftDigest != storedBefore.DraftDigest || !bytes.Equal(storedAfter.DraftConfig, storedBefore.DraftConfig) {
		t.Fatalf("filter-source proposals mutated the stored draft: before=%#v after=%#v", storedBefore, storedAfter)
	}
}

func rawFilterSourceChoices(t *testing.T, body string) []json.RawMessage {
	t.Helper()
	var envelope struct {
		FilterSourceInput struct {
			Choices []json.RawMessage `json:"choices"`
		} `json:"filterSourceInput"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.FilterSourceInput.Choices
}

func filterSourceHTTPBool(value bool) *bool { return &value }

// --- Reviewer F-1: durable filter-owned projections against reconcile ---
//
// The applied-draft threat model: a projection persisted with honest owner
// and column identity (the checks that existed) but tampered field facts.
// These probes pair the durable claims with the compiled artifact of the
// honest accepted plan -- the state reconcile must refuse.

const filterSourceF1Column = "col_reconcile_integer"
const filterSourceF1HonestField = "multipleBirthInteger"
const filterSourceF1OwnerStep = "only_step"

func filterSourceF1Snapshot() capability.Snapshot {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "c_patient_active", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "active", Label: "Active", LogicalType: "boolean", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
		capability.Candidate{
			ID: "c_patient_multiple_birth_integer", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: filterSourceF1HonestField, Label: "Multiple birth integer", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
	)
	return snapshot
}

func filterSourceF1HonestProjection() authoringv2.ConstructionSourceProjection {
	return authoringv2.ConstructionSourceProjection{
		ColumnID: filterSourceF1Column, OwnerStepID: filterSourceF1OwnerStep,
		OccurrenceID: authoringv2.RootOccurrenceID, FieldPath: filterSourceF1HonestField,
		FHIRType: "integer", LogicalType: "integer", Label: "Multiple birth integer",
	}
}

func filterSourceF1Workspace(t *testing.T, snapshot capability.Snapshot, projection authoringv2.ConstructionSourceProjection) authoringv2.Workspace {
	t.Helper()
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
	}
	document, err := authoringv2.UpgradeDocumentToConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns)+1)
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	columns = append(columns, authoringv2.StageColumn{
		ID: projection.ColumnID, Name: authoringv2.ConstructionSourceProjectionName(projection.ColumnID),
		Label: projection.Label, Type: projection.LogicalType,
	})
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID:     filterSourceF1OwnerStep,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationFilter,
			Filter: &authoringv2.ConstructionFilter{
				ColumnID: projection.ColumnID, Operator: authoringv2.ConstructionFilterEquals,
				Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterBoolean, Boolean: filterSourceHTTPBool(true)}},
			},
		},
		Outputs: columns,
	}}
	document.Construction.SourceProjections = []authoringv2.ConstructionSourceProjection{projection}
	workspace.Documents[0] = document
	catalog := authoringV2Catalog(snapshot, "custom")
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, catalog)
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, catalog).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate durable filter-source workspace: %v", err)
	}
	return workspace
}

// filterSourceF1Compile compiles the honest accepted plan; its compiled
// source column identity is the artifact a tampered durable projection is
// measured against, mirroring the production compileExplorerReceipt pairing.
func filterSourceF1Compile(t *testing.T, snapshot capability.Snapshot) (authoringv2.Workspace, dataframeexecution.Resolved) {
	t.Helper()
	workspace := filterSourceF1Workspace(t, snapshot, filterSourceF1HonestProjection())
	translated, err := explorercompilation.CompileWorkspace(context.Background(), "project-a", "custom", workspace, snapshot, explorercompilation.ResolvedInputs{})
	if err != nil {
		t.Fatalf("compile honest filter-source plan: %v", err)
	}
	engine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: compiler.ArangoTarget(),
		Registry:      compilerTestRegistry{},
		QueryRows: func(ctx context.Context, command command.CompiledCommand, _ int, visit dataframeexecution.RowVisitor) error {
			_, _, deriveErr := command.AQLParameters()
			if deriveErr != nil {
				return deriveErr
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.CompileResolvedBundle(context.Background(), translated.Bundle, recipe.RuntimeBindings{Project: "project-a", DatasetGeneration: snapshot.Identity.Generation})
	if err != nil {
		t.Fatalf("compile honest filter-source recipe: %v", err)
	}
	return workspace, resolved
}

func filterSourceF1Refuse(t *testing.T, tampered authoringv2.ConstructionSourceProjection, want string) {
	t.Helper()
	snapshot := filterSourceF1Snapshot()
	_, resolved := filterSourceF1Compile(t, snapshot)
	workspace := filterSourceF1Workspace(t, snapshot, tampered)
	honest := filterSourceF1HonestProjection()
	if tampered.ColumnID != honest.ColumnID || tampered.OwnerStepID != honest.OwnerStepID {
		t.Fatal("tampered probe must keep honest owner and column identity")
	}
	compiledByOutput := map[string]lower.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	_, _, err := authoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, snapshot)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("reconcile FILTER branch wrongly accepted tampered projection %s: err=%v want=%q", tampered.FieldPath, err, want)
	}
}

func TestConstructionFilterSourceReconcileRefusesFieldPathSwap(t *testing.T) {
	tampered := filterSourceF1HonestProjection()
	tampered.FieldPath = "active"
	tampered.FHIRType = "boolean"
	tampered.LogicalType = "boolean"
	tampered.Label = "Patient.active"
	filterSourceF1Refuse(t, tampered, "compiled source column identity")
}

func TestConstructionFilterSourceReconcileRefusesLogicalTypeFlip(t *testing.T) {
	tampered := filterSourceF1HonestProjection()
	tampered.LogicalType = "boolean"
	filterSourceF1Refuse(t, tampered, "compiled source column identity")
}

// The honest durable projection must keep passing the branch with the
// authorized snapshot threaded through.
func TestConstructionFilterSourceReconcileClaimsHonestProjection(t *testing.T) {
	snapshot := filterSourceF1Snapshot()
	workspace, resolved := filterSourceF1Compile(t, snapshot)
	compiledByOutput := map[string]lower.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	if _, _, err := authoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, snapshot); err != nil {
		t.Fatalf("reconcile FILTER branch refused the honest projection: %v", err)
	}
}

// Reviewer R1/P5: reconcile runs only from compileExplorerReceipt, which
// always threads Authorized.Snapshot, so an unarmed call is test-only today
// -- but the armed facts gate must never SKIP silently. An honest durable
// filter-owned claim whose field stopped qualifying (unpopulated here) must
// be refused, not published, when the caller brings a blank snapshot token.
func TestConstructionFilterSourceReconcileClaimsOnlyWithAuthorizedSnapshot(t *testing.T) {
	armedSnapshot := filterSourceF1Snapshot()
	workspace, resolved := filterSourceF1Compile(t, armedSnapshot)
	compiledByOutput := map[string]lower.CompiledRecipeOutput{}
	for _, output := range resolved.Compiled.Outputs {
		compiledByOutput[output.Name] = output
	}
	stale := filterSourceF1Snapshot()
	for index := range stale.Candidates {
		if stale.Candidates[index].ID == "c_patient_multiple_birth_integer" {
			stale.Candidates[index].Populated = false
		}
	}
	blankToken := stale
	blankToken.Token = ""

	_, _, err := authoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, blankToken)
	if err == nil || !strings.Contains(err.Error(), "without an authorized capability snapshot") {
		t.Fatalf("unarmed reconcile wrongly published an unpopulated filter-owned claim: err=%v", err)
	}
	if _, _, err := authoredOutputColumnsWithCombineInputs(workspace, compiledByOutput, nil, stale); err == nil || !strings.Contains(err.Error(), "authorized filter source derivation") {
		t.Fatalf("armed reconcile wrongly published the same unpopulated claim: err=%v", err)
	}
}
