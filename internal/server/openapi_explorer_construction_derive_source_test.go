package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

type deriveSourceProbe struct {
	Supported  *bool                     `json:"supported"`
	StageID    *string                   `json:"stageId"`
	ReasonCode *string                   `json:"reasonCode"`
	Reason     *string                   `json:"reason"`
	Choices    []filterSourceChoiceProbe `json:"choices"`
}

func decodedDeriveSourceInput(t *testing.T, body string) (map[string]json.RawMessage, *deriveSourceProbe) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode capabilities envelope: %v body=%s", err, body)
	}
	raw, ok := envelope["deriveSourceInput"]
	if !ok {
		t.Fatalf("capabilities wire response dropped deriveSourceInput: %s", body)
	}
	var input deriveSourceProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}

func rawDeriveSourceChoices(t *testing.T, body string) []json.RawMessage {
	t.Helper()
	var envelope struct {
		DeriveSourceInput struct {
			Choices []json.RawMessage `json:"choices"`
		} `json:"deriveSourceInput"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.DeriveSourceInput.Choices
}

func TestConstructionDeriveSourceHTTPContractPreservesCapabilitiesAndProposals(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "c_patient_birth", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "multipleBirthInteger", Label: "Birth count", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
		capability.Candidate{
			ID: "c_patient_active", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "active", Label: "Active", LogicalType: "boolean", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
	)
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
	derivedColumn := authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}
	three := int64(3)
	document.Construction.Steps = []authoringv2.ConstructionStep{{
		ID:     "derive_step",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationDerive,
			Derive: &authoringv2.ConstructionDerive{
				ConstructionID: "derive_step", OutputColumnID: "derived_total", Operation: authoringv2.ConstructionDerivedAdd,
				Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &three}},
				Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &three}},
				MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
			},
		},
		Outputs: append(append([]authoringv2.StageColumn(nil), columns...), derivedColumn),
	}}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate staged derive-source workspace: %v", err)
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
		Registry:  compilerTestRegistry{},
		QueryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil },
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
				t.Fatalf("unexpected derive-source candidate preview receipt/bindings: %#v / %#v", receipt, bindings)
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
		t.Fatalf("derive-source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	inputKeys, input := decodedDeriveSourceInput(t, capabilitiesHTTP.Body)
	for _, key := range []string{"supported", "stageId", "choices"} {
		if _, ok := inputKeys[key]; !ok {
			t.Fatalf("deriveSourceInput omitted required key %q: %s", key, capabilitiesHTTP.Body)
		}
	}
	if input.Supported == nil || !*input.Supported {
		t.Fatalf("derive-source capability was not advertised for the populated numeric root scalar: %s", capabilitiesHTTP.Body)
	}
	if input.StageID == nil || *input.StageID != "source_projection" {
		t.Fatalf("deriveSourceInput.stageId = %v, want the requested capability stage", input.StageID)
	}
	if _, leaked := inputKeys["reasonCode"]; leaked {
		t.Fatalf("supported deriveSourceInput leaked reasonCode: %s", capabilitiesHTTP.Body)
	}
	if _, leaked := inputKeys["reason"]; leaked {
		t.Fatalf("supported deriveSourceInput leaked reason: %s", capabilitiesHTTP.Body)
	}
	if len(input.Choices) != 1 || input.Choices[0].FieldPath == nil || *input.Choices[0].FieldPath != "multipleBirthInteger" {
		t.Fatalf("deriveSourceInput.choices = %#v, want only the numeric root scalar (strings and booleans excluded)", input.Choices)
	}
	choice := input.Choices[0]
	for _, rawChoice := range rawDeriveSourceChoices(t, capabilitiesHTTP.Body) {
		var choiceKeys map[string]json.RawMessage
		if err := json.Unmarshal(rawChoice, &choiceKeys); err != nil {
			t.Fatal(err)
		}
		if len(choiceKeys) != 6 {
			t.Fatalf("derive source choice keys = %d, want exactly the six contract keys: %s", len(choiceKeys), string(rawChoice))
		}
		for _, key := range []string{"choiceId", "fieldPath", "label", "logicalType", "isPopulated", "columnId"} {
			if _, ok := choiceKeys[key]; !ok {
				t.Fatalf("derive source choice omitted contract key %q: %s", key, string(rawChoice))
			}
		}
	}
	if choice.ChoiceID == nil || *choice.ChoiceID == "" {
		t.Fatalf("derive source choice lacked its signed choiceId: %s", capabilitiesHTTP.Body)
	}
	if choice.LogicalType == nil || *choice.LogicalType != "integer" || *choice.LogicalType == "number" {
		t.Fatalf("derive source choice logicalType = %v, want the exact numeric kind", choice.LogicalType)
	}
	if choice.IsPopulated == nil || !*choice.IsPopulated {
		t.Fatalf("derive source choice lost the authorized populated fact: %#v", choice)
	}
	if choice.ColumnID == nil || *choice.ColumnID != "" {
		t.Fatalf("derive source choice columnId = %v, want an honest empty string for a fresh literal-only step", choice.ColumnID)
	}
	choiceID := *choice.ChoiceID

	projectionColumnID := "derive_source_birth"
	candidateConstruction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID:     "derive_step",
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationDerive,
			Derive: &authoringv2.ConstructionDerive{
				ConstructionID: "derive_step", OutputColumnID: "derived_total", Operation: authoringv2.ConstructionDerivedAdd,
				Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: projectionColumnID},
				Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &three}},
				MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
			},
		},
		Outputs: append(append(append([]authoringv2.StageColumn(nil), columns...), authoringv2.StageColumn{
			ID: projectionColumnID, Name: authoringv2.ConstructionSourceProjectionName(projectionColumnID), Label: "Birth count", Type: "integer",
		}), derivedColumn),
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
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s,"deriveSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID, projectionColumnID,
	))
	if validProposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("derive-source proposal status=%d body=%s", validProposalHTTP.StatusCode, validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"sourceProjections"`)) ||
		!bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"fieldPath":"multipleBirthInteger"`)) {
		t.Fatalf("derive-source selection did not reach the lifecycle entry and survive into the candidate: %s", validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(fmt.Sprintf(`"columnId":%q`, projectionColumnID))) {
		t.Fatalf("materialized projection lost the edited Derive operand identity: %s", validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"previewStatus"`)) {
		t.Fatalf("derive-source proposal lacked its preview status: %s", validProposalHTTP.Body)
	}

	duplicateHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s,"deriveSources":[{"choiceId":%q,"columnId":%q},{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID, projectionColumnID, choiceID, projectionColumnID,
	))
	if duplicateHTTP.StatusCode != http.StatusBadRequest && duplicateHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate deriveSources status=%d, want a request validation error: %s", duplicateHTTP.StatusCode, duplicateHTTP.Body)
	}
	if !bytes.Contains([]byte(duplicateHTTP.Body), []byte("duplicate")) {
		t.Fatalf("duplicate deriveSources rejection did not come from the lifecycle entry: %s", duplicateHTTP.Body)
	}

	overCapHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s,"deriveSources":[{"choiceId":"choice-a","columnId":"col-a"},{"choiceId":"choice-b","columnId":"col-b"},{"choiceId":"choice-c","columnId":"col-c"}]}`,
		snapshot.Token, digest, candidateJSON,
	))
	if overCapHTTP.StatusCode != http.StatusBadRequest && overCapHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("over-capacity deriveSources status=%d, want a request validation error: %s", overCapHTTP.StatusCode, overCapHTTP.Body)
	}
	if !bytes.Contains([]byte(overCapHTTP.Body), []byte("at most")) {
		t.Fatalf("over-capacity deriveSources rejection did not come from the lifecycle operand cap: %s", overCapHTTP.Body)
	}

	emptyColumnHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s,"deriveSources":[{"choiceId":%q,"columnId":""}]}`,
		snapshot.Token, digest, candidateJSON, choiceID,
	))
	if emptyColumnHTTP.StatusCode != http.StatusBadRequest && emptyColumnHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty deriveSources columnId status=%d, want a request validation error: %s", emptyColumnHTTP.StatusCode, emptyColumnHTTP.Body)
	}
	if !bytes.Contains([]byte(emptyColumnHTTP.Body), []byte("columnId")) {
		t.Fatalf("empty deriveSources columnId rejection did not identify the invalid field: %s", emptyColumnHTTP.Body)
	}

	forgedTokenHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s,"deriveSources":[{"choiceId":"forged-choice","columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, projectionColumnID,
	))
	if forgedTokenHTTP.StatusCode != http.StatusConflict {
		t.Fatalf("forged deriveSources token status=%d, want the conflict refusal from the lifecycle entry: %s", forgedTokenHTTP.StatusCode, forgedTokenHTTP.Body)
	}
	if !bytes.Contains([]byte(forgedTokenHTTP.Body), []byte("INVALID_DERIVE_SOURCE_CHOICE")) {
		t.Fatalf("forged deriveSources token did not reach choice validation: %s", forgedTokenHTTP.Body)
	}

	// The boolean active choice is not advertised as a derive source; a token
	// forged from the filter capability surface must not compile as an operand.
	nonNumericHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s,"deriveSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID+"x", projectionColumnID,
	))
	if nonNumericHTTP.StatusCode != http.StatusConflict && nonNumericHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("tampered deriveSources choice status=%d, want a refusal: %s", nonNumericHTTP.StatusCode, nonNumericHTTP.Body)
	}

	crossSnapshotHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s,"deriveSources":[{"choiceId":%q,"columnId":%q}]}`,
		digest, digest, candidateJSON, choiceID, projectionColumnID,
	))
	if crossSnapshotHTTP.StatusCode == http.StatusOK {
		t.Fatalf("cross-snapshot deriveSources token was accepted: %s", crossSnapshotHTTP.Body)
	}

	staleOperandHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"derive_step","candidateConstruction":%s}`,
		snapshot.Token, digest, candidateJSON,
	))
	if staleOperandHTTP.StatusCode != http.StatusOK {
		t.Fatalf("candidate consuming an unmaterialized projection status=%d, want an honest preview repair report: %s", staleOperandHTTP.StatusCode, staleOperandHTTP.Body)
	}
	if !bytes.Contains([]byte(staleOperandHTTP.Body), []byte(`"previewStatus":"NEEDS_REPAIR"`)) ||
		!bytes.Contains([]byte(staleOperandHTTP.Body), []byte(fmt.Sprintf(`"columnId":%q`, projectionColumnID))) {
		t.Fatalf("unmaterialized operand was not reported as a missing input identifying the dropped column: %s", staleOperandHTTP.Body)
	}

	storedAfter, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if storedAfter.DraftVersion != storedBefore.DraftVersion || storedAfter.DraftDigest != storedBefore.DraftDigest || !bytes.Equal(storedAfter.DraftConfig, storedBefore.DraftConfig) {
		t.Fatalf("derive-source proposals mutated the stored draft: before=%#v after=%#v", storedBefore, storedAfter)
	}
}
