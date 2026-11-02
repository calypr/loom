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
