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

type unpivotSourceProbe struct {
	Supported  *bool                     `json:"supported"`
	StageID    *string                   `json:"stageId"`
	ReasonCode *string                   `json:"reasonCode"`
	Reason     *string                   `json:"reason"`
	Choices    []filterSourceChoiceProbe `json:"choices"`
}

func decodedUnpivotSourceInput(t *testing.T, body string) (map[string]json.RawMessage, *unpivotSourceProbe) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode capabilities envelope: %v body=%s", err, body)
	}
	raw, ok := envelope["unpivotSourceInput"]
	if !ok {
		t.Fatalf("capabilities wire response dropped unpivotSourceInput: %s", body)
	}
	var input unpivotSourceProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}

func rawUnpivotSourceChoices(t *testing.T, body string) []json.RawMessage {
	t.Helper()
	var envelope struct {
		UnpivotSourceInput struct {
			Choices []json.RawMessage `json:"choices"`
		} `json:"unpivotSourceInput"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.UnpivotSourceInput.Choices
}

func TestConstructionUnpivotSourceHTTPContractPreservesCapabilitiesAndProposals(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "c_patient_birth", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "multipleBirthInteger", Label: "Birth count", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
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
	keyScalar := func(value string) authoringv2.TableScalar {
		kind := authoringv2.TableScalarString
		return authoringv2.TableScalar{Kind: kind, String: &value}
	}
	document.Construction.Steps = []authoringv2.ConstructionStep{
		{
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
		},
		{
			ID:     "unpivot_step",
			Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive_step"}},
			Operation: authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationUnpivot,
				Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID: "unpivot_step",
					Inputs: []authoringv2.ConstructionUnpivotInput{
						{ColumnID: "derived_total", Key: keyScalar("variant-a")},
					},
					KeyOutputColumnID:   "unpivot_key",
					ValueOutputColumnID: "unpivot_value",
					NullRowPolicy:       authoringv2.ConstructionUnpivotDrop,
				},
			},
			Outputs: append(append([]authoringv2.StageColumn(nil), columns...),
				authoringv2.StageColumn{ID: "unpivot_key", Name: "unpivot_key", Label: "Variant", Type: "string"},
				authoringv2.StageColumn{ID: "unpivot_value", Name: "unpivot_value", Label: "Value", Type: "integer"}),
		},
	}
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate staged unpivot-source workspace: %v", err)
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
				t.Fatalf("unexpected unpivot-source candidate preview receipt/bindings: %#v / %#v", receipt, bindings)
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
		t.Fatalf("unpivot-source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	inputKeys, input := decodedUnpivotSourceInput(t, capabilitiesHTTP.Body)
	for _, key := range []string{"supported", "stageId", "choices"} {
		if _, ok := inputKeys[key]; !ok {
			t.Fatalf("unpivotSourceInput omitted required key %q: %s", key, capabilitiesHTTP.Body)
		}
	}
	if input.Supported == nil || !*input.Supported {
		t.Fatalf("unpivot-source capability was not advertised for the populated root scalars: %s", capabilitiesHTTP.Body)
	}
	if input.StageID == nil || *input.StageID != "source_projection" {
		t.Fatalf("unpivotSourceInput.stageId = %v, want the requested capability stage", input.StageID)
	}
	if _, leaked := inputKeys["reasonCode"]; leaked {
		t.Fatalf("supported unpivotSourceInput leaked reasonCode: %s", capabilitiesHTTP.Body)
	}
	if _, leaked := inputKeys["reason"]; leaked {
		t.Fatalf("supported unpivotSourceInput leaked reason: %s", capabilitiesHTTP.Body)
	}
	var birth *filterSourceChoiceProbe
	for index := range input.Choices {
		choice := input.Choices[index]
		if choice.FieldPath != nil && *choice.FieldPath == "multipleBirthInteger" {
			birth = &choice
		}
	}
	if birth == nil {
		t.Fatalf("unpivotSourceInput.choices omitted the populated integer root scalar: %#v", input.Choices)
	}
	for _, rawChoice := range rawUnpivotSourceChoices(t, capabilitiesHTTP.Body) {
		var choiceKeys map[string]json.RawMessage
		if err := json.Unmarshal(rawChoice, &choiceKeys); err != nil {
			t.Fatal(err)
		}
		if len(choiceKeys) != 6 {
			t.Fatalf("unpivot source choice keys = %d, want exactly the six contract keys: %s", len(choiceKeys), string(rawChoice))
		}
		for _, key := range []string{"choiceId", "fieldPath", "label", "logicalType", "isPopulated", "columnId"} {
			if _, ok := choiceKeys[key]; !ok {
				t.Fatalf("unpivot source choice omitted contract key %q: %s", key, string(rawChoice))
			}
		}
	}
	if birth.ChoiceID == nil || *birth.ChoiceID == "" {
		t.Fatalf("unpivot source choice lacked its signed choiceId: %s", capabilitiesHTTP.Body)
	}
	if birth.LogicalType == nil || *birth.LogicalType != "integer" {
		t.Fatalf("unpivot source choice logicalType = %v, want the exact melt kind", birth.LogicalType)
	}
	if birth.IsPopulated == nil || !*birth.IsPopulated {
		t.Fatalf("unpivot source choice lost the authorized populated fact: %#v", birth)
	}
	if birth.ColumnID == nil || *birth.ColumnID != "" {
		t.Fatalf("unpivot source choice columnId = %v, want an honest empty string for a fresh melt", birth.ColumnID)
	}
	choiceID := *birth.ChoiceID

	projectionColumnID := "unpivot_source_birth"
	meltStep := func(includeSigned bool) authoringv2.ConstructionStep {
		inputs := []authoringv2.ConstructionUnpivotInput{{ColumnID: "derived_total", Key: keyScalar("variant-a")}}
		outputs := append(append([]authoringv2.StageColumn(nil), columns...),
			authoringv2.StageColumn{ID: "unpivot_key", Name: "unpivot_key", Label: "Variant", Type: "string"},
			authoringv2.StageColumn{ID: "unpivot_value", Name: "unpivot_value", Label: "Value", Type: "integer"})
		if includeSigned {
			inputs = append(inputs, authoringv2.ConstructionUnpivotInput{ColumnID: projectionColumnID, Key: keyScalar("variant-b")})
		}
		return authoringv2.ConstructionStep{
			ID:     "unpivot_step",
			Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive_step"}},
			Operation: authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationUnpivot,
				Unpivot: &authoringv2.ConstructionUnpivot{
					ConstructionID: "unpivot_step", Inputs: inputs,
					KeyOutputColumnID: "unpivot_key", ValueOutputColumnID: "unpivot_value",
					NullRowPolicy: authoringv2.ConstructionUnpivotDrop,
				},
			},
			Outputs: outputs,
		}
	}
	carrierStep := document.Construction.Steps[0]
	candidateConstruction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrierStep, meltStep(true)}}
	candidateJSON, err := json.Marshal(candidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	storedBefore, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}

	validProposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID, projectionColumnID,
	))
	if validProposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("unpivot-source proposal status=%d body=%s", validProposalHTTP.StatusCode, validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"sourceProjections"`)) ||
		!bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"fieldPath":"multipleBirthInteger"`)) {
		t.Fatalf("unpivot-source selection did not reach the lifecycle entry and survive into the candidate: %s", validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(fmt.Sprintf(`"columnId":%q`, projectionColumnID))) {
		t.Fatalf("materialized projection lost the edited Unpivot melt identity: %s", validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"previewStatus"`)) {
		t.Fatalf("unpivot-source proposal lacked its preview status: %s", validProposalHTTP.Body)
	}

	duplicateHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q},{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID, projectionColumnID, choiceID, projectionColumnID,
	))
	if duplicateHTTP.StatusCode != http.StatusBadRequest && duplicateHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate unpivotSources status=%d, want a request validation error: %s", duplicateHTTP.StatusCode, duplicateHTTP.Body)
	}
	if !bytes.Contains([]byte(duplicateHTTP.Body), []byte("duplicate")) {
		t.Fatalf("duplicate unpivotSources rejection did not come from the lifecycle entry: %s", duplicateHTTP.Body)
	}

	overWideHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":"choice-a","columnId":"col-a"},{"choiceId":"choice-b","columnId":"col-b"},{"choiceId":"choice-c","columnId":"col-c"}]}`,
		snapshot.Token, digest, candidateJSON,
	))
	if overWideHTTP.StatusCode != http.StatusBadRequest && overWideHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("over-declared unpivotSources status=%d, want a request validation error: %s", overWideHTTP.StatusCode, overWideHTTP.Body)
	}
	if !bytes.Contains([]byte(overWideHTTP.Body), []byte("melts")) {
		t.Fatalf("over-declared unpivotSources rejection did not come from the lifecycle melt-cardinality guard: %s", overWideHTTP.Body)
	}

	emptyColumnHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":""}]}`,
		snapshot.Token, digest, candidateJSON, choiceID,
	))
	if emptyColumnHTTP.StatusCode != http.StatusBadRequest && emptyColumnHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("empty unpivotSources columnId status=%d, want a request validation error: %s", emptyColumnHTTP.StatusCode, emptyColumnHTTP.Body)
	}
	if !bytes.Contains([]byte(emptyColumnHTTP.Body), []byte("columnId")) {
		t.Fatalf("empty unpivotSources columnId rejection did not identify the invalid field: %s", emptyColumnHTTP.Body)
	}

	forgedTokenHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":"forged-choice","columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, projectionColumnID,
	))
	if forgedTokenHTTP.StatusCode != http.StatusConflict {
		t.Fatalf("forged unpivotSources token status=%d, want the conflict refusal from the lifecycle entry: %s", forgedTokenHTTP.StatusCode, forgedTokenHTTP.Body)
	}
	if !bytes.Contains([]byte(forgedTokenHTTP.Body), []byte("INVALID_UNPIVOT_SOURCE_CHOICE")) {
		t.Fatalf("forged unpivotSources token did not reach choice validation: %s", forgedTokenHTTP.Body)
	}

	tamperedHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, candidateJSON, choiceID+"x", projectionColumnID,
	))
	if tamperedHTTP.StatusCode != http.StatusConflict && tamperedHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("tampered unpivotSources choice status=%d, want a refusal: %s", tamperedHTTP.StatusCode, tamperedHTTP.Body)
	}

	crossSnapshotHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q}]}`,
		digest, digest, candidateJSON, choiceID, projectionColumnID,
	))
	if crossSnapshotHTTP.StatusCode == http.StatusOK {
		t.Fatalf("cross-snapshot unpivotSources token was accepted: %s", crossSnapshotHTTP.Body)
	}

	incompatibleHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest,
		func() string {
			// Melt a string-valued identity column together with the signed
			// integer slot: the joint kind set is outside the compiler's melt
			// rule (tableScalarKindsCompatible), so the lifecycle lane must
			// refuse before the candidate reaches the compiler.
			incompatibleMelt := authoringv2.ConstructionStep{
				ID:     "unpivot_step",
				Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputStepOutput, StepID: "derive_step"}},
				Operation: authoringv2.ConstructionOperation{
					Kind: authoringv2.ConstructionOperationUnpivot,
					Unpivot: &authoringv2.ConstructionUnpivot{
						ConstructionID: "unpivot_step",
						Inputs: []authoringv2.ConstructionUnpivotInput{
							{ColumnID: columns[0].ID, Key: keyScalar("variant-a")},
							{ColumnID: projectionColumnID, Key: keyScalar("variant-b")},
						},
						KeyOutputColumnID: "unpivot_key", ValueOutputColumnID: "unpivot_value",
						NullRowPolicy: authoringv2.ConstructionUnpivotDrop,
					},
				},
				Outputs: append(append([]authoringv2.StageColumn(nil), columns[1:]...),
					authoringv2.StageColumn{ID: "unpivot_key", Name: "unpivot_key", Label: "Variant", Type: "string"},
					authoringv2.StageColumn{ID: "unpivot_value", Name: "unpivot_value", Label: "Value", Type: "integer"}),
			}
			incompatible := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{carrierStep, incompatibleMelt}}
			encoded, encodeErr := json.Marshal(incompatible)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			return string(encoded)
		}(), choiceID, projectionColumnID,
	))
	if incompatibleHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("incompatible melt kinds status=%d, want the compiler-rule refusal: %s", incompatibleHTTP.StatusCode, incompatibleHTTP.Body)
	}
	if !bytes.Contains([]byte(incompatibleHTTP.Body), []byte("UNPIVOT_SOURCE_TYPES_INCOMPATIBLE")) {
		t.Fatalf("incompatible melt kinds rejection omitted the named refusal: %s", incompatibleHTTP.Body)
	}

	staleOperandHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":"unpivot_step","candidateConstruction":%s}`,
		snapshot.Token, digest, candidateJSON,
	))
	if staleOperandHTTP.StatusCode != http.StatusOK {
		t.Fatalf("candidate melting an unmaterialized projection status=%d, want an honest preview repair report: %s", staleOperandHTTP.StatusCode, staleOperandHTTP.Body)
	}
	if !bytes.Contains([]byte(staleOperandHTTP.Body), []byte(`"previewStatus":"NEEDS_REPAIR"`)) ||
		!bytes.Contains([]byte(staleOperandHTTP.Body), []byte(fmt.Sprintf(`"columnId":%q`, projectionColumnID))) {
		t.Fatalf("unmaterialized melt input was not reported as a missing input identifying the dropped column: %s", staleOperandHTTP.Body)
	}

	storedAfter, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if storedAfter.DraftVersion != storedBefore.DraftVersion || storedAfter.DraftDigest != storedBefore.DraftDigest || !bytes.Equal(storedAfter.DraftConfig, storedBefore.DraftConfig) {
		t.Fatalf("unpivot-source proposals mutated the stored draft: before=%#v after=%#v", storedBefore, storedAfter)
	}
}

// --- F-A durable: melt-first signed UNPIVOT at the shipped handler boundary ---
// (Journaled defect F-A, authority /tmp/opencode/fa-report.md §1-§4; durable
// port of the reviewer's /tmp/opencode/fa-repro/fa_melt_first_overlay_test.go,
// renamed to the lane family. Same harness as the shipped contract test:
// fiber + real compileExplorerReceipt + recipe engine.)

type unpivotMeltFirstEnv struct {
	t         *testing.T
	app       *fiber.App
	basePath  string
	snapshot  capability.Snapshot
	draftVer  int64
	draftDgst string
}

func newUnpivotMeltFirstEnv(t *testing.T) *unpivotMeltFirstEnv {
	t.Helper()
	return newUnpivotMeltFirstEnvWithCandidates(t)
}

func newUnpivotMeltFirstEnvWithCandidates(t *testing.T, extra ...capability.Candidate) *unpivotMeltFirstEnv {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "c_patient_gender", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "gender", Label: "Gender", LogicalType: "string", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 135,
		},
	)
	snapshot.Candidates = append(snapshot.Candidates, extra...)
	workspace, err := authoringv2.DecodeWorkspace(baselineExplorerWorkspaceV2())
	if err != nil {
		t.Fatal(err)
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
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
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
			if err := visit(map[string]any{"c_patient": "patient-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "patients", Columns: []string{"c_patient"}, RowCount: 1, Complete: true, PartialValidation: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	return &unpivotMeltFirstEnv{t: t, app: app, basePath: "/api/v1/projects/project-a/explorers/custom/authoring/v2", snapshot: snapshot, draftVer: 1, draftDgst: digest}
}

func (e *unpivotMeltFirstEnv) meltFirstCapabilities(t *testing.T) string {
	t.Helper()
	resp := requestJSON(t, e.app, http.MethodPost, e.basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":"source_projection"}`,
		e.snapshot.Token, e.draftVer, e.draftDgst,
	))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", resp.StatusCode, resp.Body)
	}
	return string(resp.Body)
}

func (e *unpivotMeltFirstEnv) meltFirstSignedChoice(t *testing.T, fieldPath string) string {
	t.Helper()
	_, input := decodedUnpivotSourceInput(t, e.meltFirstCapabilities(t))
	for _, choice := range input.Choices {
		if choice.FieldPath != nil && *choice.FieldPath == fieldPath && choice.ChoiceID != nil && *choice.ChoiceID != "" {
			return *choice.ChoiceID
		}
	}
	t.Fatalf("no signed unpivot choice for %q", fieldPath)
	return ""
}

// meltFirstCompiledColumnID returns the COMPILED source_projection stage
// column id for an authored name; the melt reads stage columns, not authored
// column ids, and using an authored id short-circuits to NEEDS_REPAIR before
// compile+reconcile, masking F-A.
func (e *unpivotMeltFirstEnv) meltFirstCompiledColumnID(t *testing.T, authorName string) string {
	t.Helper()
	var envelope struct {
		SelectedStage *struct {
			Columns []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"columns"`
		} `json:"selectedStage"`
		Stages []struct {
			ID      string `json:"id"`
			Columns []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"columns"`
		} `json:"stages"`
	}
	if err := json.Unmarshal([]byte(e.meltFirstCapabilities(t)), &envelope); err != nil {
		t.Fatal(err)
	}
	columns := []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{}
	if envelope.SelectedStage != nil {
		columns = envelope.SelectedStage.Columns
	} else if n := len(envelope.Stages); n > 0 {
		columns = envelope.Stages[n-1].Columns
	}
	for _, column := range columns {
		if column.Name == authorName {
			return column.ID
		}
	}
	t.Fatalf("no compiled stage column named %q in source_projection", authorName)
	return ""
}

func unpivotMeltFirstConstruction(publicColumnID, stepID, signedColumnID string, includeSigned bool) authoringv2.Construction {
	scalar := func(value string) authoringv2.TableScalar {
		kind := authoringv2.TableScalarString
		return authoringv2.TableScalar{Kind: kind, String: &value}
	}
	mkey, mval := stepID+"_key", stepID+"_value"
	inputs := []authoringv2.ConstructionUnpivotInput{{ColumnID: publicColumnID, Key: scalar("public")}}
	if includeSigned {
		inputs = append(inputs, authoringv2.ConstructionUnpivotInput{ColumnID: signedColumnID, Key: scalar("signed")})
	}
	return authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID:     stepID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
			ConstructionID: stepID, Inputs: inputs, KeyOutputColumnID: mkey, ValueOutputColumnID: mval,
			NullRowPolicy: authoringv2.ConstructionUnpivotDrop,
		}},
		Outputs: []authoringv2.StageColumn{
			{ID: mkey, Name: mkey, Label: "Variable", Type: "string"},
			{ID: mval, Name: mval, Label: "Value", Type: "string"},
		},
	}}}
}

// F-A regression: the compiler legitimately seeds the melt-signed projection
// into SOURCE_PROJECTION (compilation/construction.go -> sourceColumns ->
// lower/recipe_construction.go resolveConstructionSourceSchema), so the melt
// as FIRST step is a legit compiled state with no possible authored carrier:
// reconcile must never answer the catch-all 500
// CONSTRUCTION_RECEIPT_CONTRACT_INVALID. RED pre-fix (500), GREEN post-fix
// (honest 200 preview; reviewer cross-check /tmp/opencode/fa-repro).
func TestConstructionUnpivotSourceMeltFirstSignedIsNotContractInvalid(t *testing.T) {
	env := newUnpivotMeltFirstEnv(t)
	choiceID := env.meltFirstSignedChoice(t, "gender")
	publicID := env.meltFirstCompiledColumnID(t, "c_patient")
	const stepID = "unpivot_first"
	const signedColumnID = "unpivot-input_first_gender"
	candidate := unpivotMeltFirstConstruction(publicID, stepID, signedColumnID, true)
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q}]}`,
		env.snapshot.Token, env.draftVer, env.draftDgst, stepID, candidateJSON, choiceID, signedColumnID,
	)
	resp := requestJSON(t, env.app, http.MethodPost, env.basePath+"/construction-proposals", body)
	t.Logf("melt-first signed status=%d body=%.1200s", resp.StatusCode, resp.Body)
	if resp.StatusCode == http.StatusInternalServerError && bytes.Contains([]byte(resp.Body), []byte(`CONSTRUCTION_RECEIPT_CONTRACT_INVALID`)) {
		t.Fatalf("F-A reproduced: melt-first signed UNPIVOT returned the catch-all 500; the melt is first (stepIndex==0) so no strictly-before step declares the consumed melt-input column even though the compiler seeds it into the source stage. body=%s", resp.Body)
	}
	if resp.StatusCode != http.StatusOK || !bytes.Contains([]byte(resp.Body), []byte(`"previewStatus"`)) {
		t.Fatalf("melt-first signed UNPIVOT must reach an honest 200 preview; got %d body=%s", resp.StatusCode, resp.Body)
	}
}

// Control, isolating F-A to the signed-melt-first gap: the same first-step
// melt without a signed binding stays a clean 200 preview pre- and post-fix.
func TestConstructionUnpivotSourceMeltFirstPublicOnlyStaysGreen(t *testing.T) {
	env := newUnpivotMeltFirstEnv(t)
	const stepID = "unpivot_public_first"
	publicID := env.meltFirstCompiledColumnID(t, "c_patient")
	candidate := unpivotMeltFirstConstruction(publicID, stepID, "", false)
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s}`,
		env.snapshot.Token, env.draftVer, env.draftDgst, stepID, candidateJSON,
	)
	resp := requestJSON(t, env.app, http.MethodPost, env.basePath+"/construction-proposals", body)
	t.Logf("melt-first public-only control status=%d body=%.800s", resp.StatusCode, resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Contains([]byte(resp.Body), []byte(`"previewStatus"`)) ||
		bytes.Contains([]byte(resp.Body), []byte("NEEDS_REPAIR")) ||
		bytes.Contains([]byte(resp.Body), []byte(`CONSTRUCTION_RECEIPT_CONTRACT_INVALID`)) {
		t.Fatalf("melt-first PUBLIC-only UNPIVOT regressed (must stay a clean 200 preview): status=%d body=%s", resp.StatusCode, resp.Body)
	}
}

// --- dated date-family melt refusal (operator decision #4) ---
// A signed date or date_time melt through the shipped proposal handler must
// return the named 422 refusal, discovery must hide the date-family choices,
// and the permitted STRING class must stay a clean 200 preview. Pre-fix, the
// signed effectiveDateTime melt reached a 200 decorated preview on the
// accepted binary (authority /tmp/opencode/jrev/probe-date-melt-dp4.log).

func unpivotDateRejectCandidates() []capability.Candidate {
	return []capability.Candidate{
		{
			ID: "c_patient_birth_date", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "birthDate", Label: "Birth date", LogicalType: "date", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 12,
		},
		{
			ID: "c_patient_deceased_date_time", NodeID: "n_patient", ResourceType: "Patient",
			FieldPath: "deceasedDateTime", Label: "Deceased", LogicalType: "date_time", Cardinality: "OPTIONAL_ONE",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 12,
		},
	}
}

// The date family is hidden from discovery by the refusal, so the request
// token is minted through the RowChoicePlanner directly (same authority the
// lifecycle boundary tests use), proving discovery and admission fail closed
// together.
func unpivotPlannerSignedChoice(t *testing.T, snapshot capability.Snapshot, path string) string {
	t.Helper()
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := lifecycle.NewSchemaRowChoiceResolver(index)
	if err != nil {
		t.Fatal(err)
	}
	document := authoringv2.Document{RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}}
	choices, err := resolver.ListRowChoices(context.Background(), snapshot, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		if choice.Kind == capability.RowChoiceFieldGroupKey && choice.OccurrenceID == authoringv2.RootOccurrenceID && choice.Path == path {
			return choice.ChoiceID
		}
	}
	t.Fatalf("planner omitted the Patient.%s root field choice", path)
	return ""
}

func TestConstructionUnpivotSourceDateFamilyMeltIsRefused(t *testing.T) {
	for _, row := range []struct{ fieldPath, logicalType string }{
		{"birthDate", "date"},
		{"deceasedDateTime", "date_time"},
	} {
		env := newUnpivotMeltFirstEnvWithCandidates(t, unpivotDateRejectCandidates()...)
		choiceID := unpivotPlannerSignedChoice(t, env.snapshot, row.fieldPath)
		publicID := env.meltFirstCompiledColumnID(t, "c_patient")
		stepID := "unpivot_date_first_" + row.logicalType
		signedColumnID := "unpivot-input_first_" + row.fieldPath
		candidate := unpivotMeltFirstConstruction(publicID, stepID, signedColumnID, true)
		candidateJSON, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(
			`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q}]}`,
			env.snapshot.Token, env.draftVer, env.draftDgst, stepID, candidateJSON, choiceID, signedColumnID,
		)
		resp := requestJSON(t, env.app, http.MethodPost, env.basePath+"/construction-proposals", body)
		t.Logf("signed %s melt status=%d body=%.600s", row.logicalType, resp.StatusCode, resp.Body)
		if resp.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains([]byte(resp.Body), []byte(`UNSUPPORTED_UNPIVOT_SOURCE_DATE_FIELD`)) {
			t.Fatalf("signed %s melt must be a named 422 refusal; got %d body=%.900s", row.logicalType, resp.StatusCode, resp.Body)
		}
	}

	env := newUnpivotMeltFirstEnvWithCandidates(t, unpivotDateRejectCandidates()...)
	capabilitiesBody := env.meltFirstCapabilities(t)
	for _, hidden := range []string{"birthDate", "deceasedDateTime"} {
		_, input := decodedUnpivotSourceInput(t, capabilitiesBody)
		for _, choice := range input.Choices {
			if choice.FieldPath != nil && *choice.FieldPath == hidden {
				t.Fatalf("discovery still advertises refused date-family melt kind %q: %#v", hidden, choice)
			}
		}
	}
	{
		_, input := decodedUnpivotSourceInput(t, capabilitiesBody)
		genderOffered := false
		for _, choice := range input.Choices {
			if choice.FieldPath != nil && *choice.FieldPath == "gender" {
				genderOffered = true
			}
		}
		if !genderOffered {
			t.Fatalf("discovery hid the permitted STRING melt kind beside the date family: %.900s", capabilitiesBody)
		}
	}
	{
		choiceID := env.meltFirstSignedChoice(t, "gender")
		publicID := env.meltFirstCompiledColumnID(t, "c_patient")
		const stepID = "unpivot_string_first"
		candidate := unpivotMeltFirstConstruction(publicID, stepID, "unpivot-input_first_gender", true)
		candidateJSON, err := json.Marshal(candidate)
		if err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(
			`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s,"unpivotSources":[{"choiceId":%q,"columnId":%q}]}`,
			env.snapshot.Token, env.draftVer, env.draftDgst, stepID, candidateJSON, choiceID, "unpivot-input_first_gender",
		)
		resp := requestJSON(t, env.app, http.MethodPost, env.basePath+"/construction-proposals", body)
		if resp.StatusCode != http.StatusOK || !bytes.Contains([]byte(resp.Body), []byte(`"previewStatus"`)) {
			t.Fatalf("signed STRING melt regressed beside the date refusal: status=%d body=%.900s", resp.StatusCode, resp.Body)
		}
	}
}
