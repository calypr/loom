package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/testkit/constructionfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/gofiber/fiber/v3"
)

type unpivotSourceProbe struct {
	Supported  *bool                                      `json:"supported"`
	StageID    *string                                    `json:"stageId"`
	ReasonCode *string                                    `json:"reasonCode"`
	Reason     *string                                    `json:"reason"`
	Choices    []explorerfixtures.FilterSourceChoiceProbe `json:"choices"`
}

func decodedUnpivotSourceInput(t *testing.T, body string) (map[string]json.RawMessage, *unpivotSourceProbe) {
	t.Helper()
	keys, raw := explorerfixtures.DecodeSourceInput(t, body, "unpivotSourceInput")
	var input unpivotSourceProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}

func rawSourceInputChoices(t *testing.T, body, inputName string) []json.RawMessage {
	t.Helper()
	keys, _ := explorerfixtures.DecodeSourceInput(t, body, inputName)
	var choices []json.RawMessage
	if err := json.Unmarshal(keys["choices"], &choices); err != nil {
		t.Fatal(err)
	}
	return choices
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
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	derivedColumn := ws.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}
	three := int64(3)
	keyScalar := func(value string) ws.TableScalar {
		kind := ws.TableScalarString
		return ws.TableScalar{Kind: kind, String: &value}
	}
	document.Construction.Steps = []ws.ConstructionStep{
		{
			ID:     "derive_step",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
			Operation: ws.ConstructionOperation{
				Kind: ws.ConstructionOperationDerive,
				Derive: &ws.ConstructionDerive{
					ConstructionID: "derive_step", OutputColumnID: "derived_total", Operation: ws.ConstructionDerivedAdd,
					Left:               ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &three}},
					Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &three}},
					MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
				},
			},
			Outputs: append(append([]ws.StageColumn(nil), columns...), derivedColumn),
		},
		{
			ID:     "unpivot_step",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive_step"}},
			Operation: ws.ConstructionOperation{
				Kind: ws.ConstructionOperationUnpivot,
				Unpivot: &ws.ConstructionUnpivot{
					ConstructionID: "unpivot_step",
					Inputs: []ws.ConstructionUnpivotInput{
						{ColumnID: "derived_total", Key: keyScalar("variant-a")},
					},
					KeyOutputColumnID:   "unpivot_key",
					ValueOutputColumnID: "unpivot_value",
					NullRowPolicy:       ws.ConstructionUnpivotDrop,
				},
			},
			Outputs: append(append([]ws.StageColumn(nil), columns...),
				ws.StageColumn{ID: "unpivot_key", Name: "unpivot_key", Label: "Variant", Type: "string"},
				ws.StageColumn{ID: "unpivot_value", Name: "unpivot_value", Label: "Value", Type: "integer"}),
		},
	}
	workspace.Documents[0] = document
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
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
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		RowChoicePlanner:  rowChoices,
		RowChoiceResolver: rowChoices,
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(previewCtx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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
	var birth *explorerfixtures.FilterSourceChoiceProbe
	for index := range input.Choices {
		choice := input.Choices[index]
		if choice.FieldPath != nil && *choice.FieldPath == "multipleBirthInteger" {
			birth = &choice
		}
	}
	if birth == nil {
		t.Fatalf("unpivotSourceInput.choices omitted the populated integer root scalar: %#v", input.Choices)
	}
	for _, rawChoice := range rawSourceInputChoices(t, capabilitiesHTTP.Body, "unpivotSourceInput") {
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
	meltStep := func(includeSigned bool) ws.ConstructionStep {
		inputs := []ws.ConstructionUnpivotInput{{ColumnID: "derived_total", Key: keyScalar("variant-a")}}
		outputs := append(append([]ws.StageColumn(nil), columns...),
			ws.StageColumn{ID: "unpivot_key", Name: "unpivot_key", Label: "Variant", Type: "string"},
			ws.StageColumn{ID: "unpivot_value", Name: "unpivot_value", Label: "Value", Type: "integer"})
		if includeSigned {
			inputs = append(inputs, ws.ConstructionUnpivotInput{ColumnID: projectionColumnID, Key: keyScalar("variant-b")})
		}
		return ws.ConstructionStep{
			ID:     "unpivot_step",
			Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive_step"}},
			Operation: ws.ConstructionOperation{
				Kind: ws.ConstructionOperationUnpivot,
				Unpivot: &ws.ConstructionUnpivot{
					ConstructionID: "unpivot_step", Inputs: inputs,
					KeyOutputColumnID: "unpivot_key", ValueOutputColumnID: "unpivot_value",
					NullRowPolicy: ws.ConstructionUnpivotDrop,
				},
			},
			Outputs: outputs,
		}
	}
	carrierStep := document.Construction.Steps[0]
	candidateConstruction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{carrierStep, meltStep(true)}}
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

			incompatibleMelt := ws.ConstructionStep{
				ID:     "unpivot_step",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputStepOutput, StepID: "derive_step"}},
				Operation: ws.ConstructionOperation{
					Kind: ws.ConstructionOperationUnpivot,
					Unpivot: &ws.ConstructionUnpivot{
						ConstructionID: "unpivot_step",
						Inputs: []ws.ConstructionUnpivotInput{
							{ColumnID: columns[0].ID, Key: keyScalar("variant-a")},
							{ColumnID: projectionColumnID, Key: keyScalar("variant-b")},
						},
						KeyOutputColumnID: "unpivot_key", ValueOutputColumnID: "unpivot_value",
						NullRowPolicy: ws.ConstructionUnpivotDrop,
					},
				},
				Outputs: append(append([]ws.StageColumn(nil), columns[1:]...),
					ws.StageColumn{ID: "unpivot_key", Name: "unpivot_key", Label: "Variant", Type: "string"},
					ws.StageColumn{ID: "unpivot_value", Name: "unpivot_value", Label: "Value", Type: "integer"}),
			}
			incompatible := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{carrierStep, incompatibleMelt}}
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
	workspace, err := ws.DecodeWorkspace(constructionfixtures.BaselineExplorerWorkspace())
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Patients",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
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
		Capability: ports.CapabilityResolver{
			Current: func(context.Context, string, string, string) (capability.Snapshot, error) { return snapshot, nil },
			Token:   func(context.Context, string, string) (capability.Snapshot, error) { return snapshot, nil },
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		RowChoicePlanner:  rowChoices,
		RowChoiceResolver: rowChoices,
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(previewCtx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
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

func unpivotMeltFirstConstruction(publicColumnID, stepID, signedColumnID string, includeSigned bool) ws.Construction {
	scalar := func(value string) ws.TableScalar {
		kind := ws.TableScalarString
		return ws.TableScalar{Kind: kind, String: &value}
	}
	mkey, mval := stepID+"_key", stepID+"_value"
	inputs := []ws.ConstructionUnpivotInput{{ColumnID: publicColumnID, Key: scalar("public")}}
	if includeSigned {
		inputs = append(inputs, ws.ConstructionUnpivotInput{ColumnID: signedColumnID, Key: scalar("signed")})
	}
	return ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID:     stepID,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
			ConstructionID: stepID, Inputs: inputs, KeyOutputColumnID: mkey, ValueOutputColumnID: mval,
			NullRowPolicy: ws.ConstructionUnpivotDrop,
		}},
		Outputs: []ws.StageColumn{
			{ID: mkey, Name: mkey, Label: "Variable", Type: "string"},
			{ID: mval, Name: mval, Label: "Value", Type: "string"},
		},
	}}}
}

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
	document := ws.Document{RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}}
	choices, err := resolver.ListRowChoices(context.Background(), snapshot, document)
	if err != nil {
		t.Fatal(err)
	}
	for _, choice := range choices {
		if choice.Kind == capability.RowChoiceFieldGroupKey && choice.OccurrenceID == ws.RootOccurrenceID && choice.Path == path {
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

const (
	expandSourceHTTPStep      = "expand_uri"
	expandSourceHTTPInput     = "expand_source_uri"
	expandSourceHTTPItem      = "expand_uri_item"
	expandSourceHTTPFieldPath = "instantiatesUri[]"
)

type expandSourceHTTPChoiceProbe struct {
	ChoiceID    *string `json:"choiceId"`
	FieldPath   *string `json:"fieldPath"`
	Label       *string `json:"label"`
	LogicalType *string `json:"logicalType"`
	IsPopulated *bool   `json:"isPopulated"`
	ColumnID    *string `json:"columnId"`
}

type expandSourceHTTPInputProbe struct {
	Supported  *bool                         `json:"supported"`
	StageID    *string                       `json:"stageId"`
	ReasonCode *string                       `json:"reasonCode"`
	Reason     *string                       `json:"reason"`
	Choices    []expandSourceHTTPChoiceProbe `json:"choices"`
}

func expandSourceHTTPProcedureSnapshot() capability.Snapshot {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Nodes = append(snapshot.Nodes, capability.Node{
		ID: "procedure", ResourceType: "Procedure", RowRootEligible: true, RowGrain: "RESOURCE", Populated: true, DocumentCount: 4,
	})
	snapshot.Candidates = append(snapshot.Candidates,
		capability.Candidate{
			ID: "c_procedure_id", NodeID: "procedure", ResourceType: "Procedure",
			FieldPath: "id", Label: "ID", LogicalType: "string", Cardinality: "optional_one",
			ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar, capability.ProjectionFirst},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 4,
		},
		capability.Candidate{
			ID: "c_procedure_uri", NodeID: "procedure", ResourceType: "Procedure",
			FieldPath: expandSourceHTTPFieldPath, Label: "Instantiates uri", LogicalType: "string", Cardinality: "many",
			ProjectionModes: []capability.ProjectionMode{capability.ProjectionArray, capability.ProjectionFirst, capability.ProjectionDistinctArray},
			RepeatedBoundaries: []capability.RepeatedBoundary{{
				Path: expandSourceHTTPFieldPath, MaxItems: 2,
			}},
			SupportedOperations: []capability.Operation{capability.OperationSelect},
			Observed:            true, Populated: true, ObservedDocumentCount: 4,
		},
	)
	return snapshot
}

func expandSourceHTTPProcedureWorkspace() []byte {
	visible, order := true, 0
	workspace := ws.Workspace{APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind, SemanticsVersion: ws.CurrentSemanticsVersion, Explorer: ws.ExplorerMetadata{Title: "Procedures"}, Documents: []ws.Document{{Kind: ws.Kind, Rows: ws.RecordsRowDefinition(), Output: ws.Output{ID: "procedures", Title: "Procedures", RowLabel: "Procedures"}, RootResourceType: "Procedure", Route: ws.RouteNode{OccurrenceID: "base", ResourceType: "Procedure"}, Columns: []ws.Column{{Column: "procedure_id", Label: "Procedure ID", LogicalType: "string", OccurrenceID: "base", Source: ws.ColumnSource{Kind: ws.SourceField, Field: &ws.FieldSource{Path: "id", ProjectionMode: "FIRST"}}, Table: &ws.TablePresentation{Visible: &visible, Order: &order}}}}}, Tabs: []ws.Tab{{ID: "procedures-tab", Title: "Procedures", OutputID: "procedures", Order: 0, Visible: true}}}
	raw, _ := json.Marshal(workspace)
	return raw
}

func expandSourceHTTPDecodedInput(t *testing.T, body string) (map[string]json.RawMessage, *expandSourceHTTPInputProbe) {
	t.Helper()
	keys, raw := explorerfixtures.DecodeSourceInput(t, body, "expandSourceInput")
	var input expandSourceHTTPInputProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}

func expandSourceHTTPSignedField(t *testing.T, rawConstruction []byte, contextText string) (json.RawMessage, map[string]string) {
	t.Helper()
	var construction map[string]json.RawMessage
	if err := json.Unmarshal(rawConstruction, &construction); err != nil {
		t.Fatalf("decode %s construction: %v", contextText, err)
	}
	var steps []map[string]json.RawMessage
	if err := json.Unmarshal(construction["steps"], &steps); err != nil {
		t.Fatalf("%s: steps array missing/corrupt: %v body=%s", contextText, err, rawConstruction)
	}
	if len(steps) != 1 {
		t.Fatalf("%s: want exactly one raw wire step, got %d: %s", contextText, len(steps), rawConstruction)
	}
	var operation map[string]json.RawMessage
	if err := json.Unmarshal(steps[0]["operation"], &operation); err != nil {
		t.Fatalf("%s: operation missing/corrupt: %v", contextText, err)
	}
	expandRaw, ok := operation["expand"]
	if !ok {
		t.Fatalf("%s: wire step lost its expand operation: %s", contextText, rawConstruction)
	}
	var expand map[string]json.RawMessage
	if err := json.Unmarshal(expandRaw, &expand); err != nil {
		t.Fatalf("%s: expand operation corrupt: %v", contextText, err)
	}
	signedRaw, ok := expand["expandedField"]
	if !ok {
		t.Fatalf("%s: wire dropped the server-signed expandedField: %s", contextText, rawConstruction)
	}
	var signed map[string]string
	if err := json.Unmarshal(signedRaw, &signed); err != nil {
		t.Fatalf("%s: signed expandedField corrupt: %v body=%s", contextText, err, signedRaw)
	}
	return expandRaw, signed
}

func expandSourceHTTPAssertSigned(t *testing.T, expandRaw json.RawMessage, signed map[string]string, contextText string) {
	t.Helper()
	want := map[string]string{
		"path":        expandSourceHTTPFieldPath,
		"cardinality": "many",
		"fhirType":    "string",
		"logicalType": "string",
	}
	if len(signed) != len(want) {
		t.Fatalf("%s: signed expandedField keys = %d %#v, want exactly %d signed fields: %s", contextText, len(signed), signed, len(want), expandRaw)
	}
	for key, value := range want {
		if signed[key] != value {
			t.Fatalf("%s: signed expandedField[%q] = %q, want %q: %s", contextText, key, signed[key], value, expandRaw)
		}
	}
	for _, forged := range []string{"status", "code"} {
		if bytes.Contains(expandRaw, []byte(forged)) {
			t.Fatalf("%s: expand wire subtree still mentions forged fact %q: %s", contextText, forged, expandRaw)
		}
	}
}

func TestConstructionExpandSourceHTTPContractSignsAndPersistsExpandedField(t *testing.T) {
	snapshot := expandSourceHTTPProcedureSnapshot()
	workspace, err := ws.DecodeWorkspace(expandSourceHTTPProcedureWorkspace())
	if err != nil {
		t.Fatal(err)
	}
	document, err := ws.StartDocumentConstruction(workspace.Documents[0])
	if err != nil {
		t.Fatal(err)
	}
	columns := make([]ws.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, ws.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	workspace.Documents[0] = document
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
	if err := workspace.Validate(); err != nil {
		t.Fatalf("validate staged expand-source workspace: %v", err)
	}
	draft, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := workspace.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Procedures",
		DraftConfig: draft, DraftVersion: 1, DraftDigest: digest,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := explorerfixtures.NewService(store)
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
		Capability: ports.CapabilityResolver{
			ForCompilation: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
				return ports.AuthorizedCapability{Snapshot: snapshot, Scope: readScope}, nil
			},
			Catalog: ws.CatalogFromCapability,
		},
		ConstructionSourceStage: func(ctx context.Context, request ports.ConstructionSourceStageRequest) (receiptmodel.ReceiptConstructionStage, error) {
			return receiptapi.CompileConstructionSourceStage(ctx, request, recipeEngine)
		},
		RowChoicePlanner:  rowChoices,
		RowChoiceResolver: rowChoices,
		CompileReceipt: func(ctx context.Context, request ports.CompileReceiptRequest) (*receiptmodel.CompilationReceipt, error) {
			return receiptapi.Compile(ctx, request, nil, recipeEngine, service, nil, nil)
		},
		PreviewReceipt: func(previewCtx context.Context, receipt *receiptmodel.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil || len(bindings.OutputNames) != 1 || bindings.OutputNames[0] != "procedures" {
				t.Fatalf("unexpected expand-source candidate preview receipt/bindings: %#v / %#v", receipt, bindings)
			}
			if err := visit(map[string]any{"procedure_id": "procedure-1"}); err != nil {
				return dataframeexecution.PreviewSummary{}, err
			}
			return dataframeexecution.PreviewSummary{Output: "procedures", Columns: []string{"procedure_id"}, RowCount: 1, Complete: true, PartialValidation: true}, nil
		},
	}
	app := fiber.New()
	registerGeneratedExplorerTestRoutes(app, authscope.AllowAllAuthorizer{}, func(context.Context, *authscope.Principal, string) error { return nil }, service, config)
	basePath := "/api/v1/projects/project-a/explorers/custom/authoring/v2"

	capabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"procedures","stageId":"source_projection"}`,
		snapshot.Token, digest,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("expand-source capabilities status=%d body=%s", capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	inputKeys, input := expandSourceHTTPDecodedInput(t, capabilitiesHTTP.Body)
	for _, key := range []string{"supported", "stageId", "choices"} {
		if _, ok := inputKeys[key]; !ok {
			t.Fatalf("expandSourceInput omitted required key %q: %s", key, capabilitiesHTTP.Body)
		}
	}
	if input.Supported == nil || !*input.Supported {
		t.Fatalf("expand-source capability was not advertised for the populated root primitive array snapshot: %s", capabilitiesHTTP.Body)
	}
	if input.StageID == nil || *input.StageID != "source_projection" {
		t.Fatalf("expandSourceInput.stageId = %v, want the requested capability stage", input.StageID)
	}
	if _, leaked := inputKeys["reasonCode"]; leaked {
		t.Fatalf("supported expandSourceInput leaked reasonCode: %s", capabilitiesHTTP.Body)
	}
	if _, leaked := inputKeys["reason"]; leaked {
		t.Fatalf("supported expandSourceInput leaked reason: %s", capabilitiesHTTP.Body)
	}
	if len(input.Choices) != 1 {
		t.Fatalf("expandSourceInput.choices = %#v, want only the authorized Procedure root primitive array", input.Choices)
	}
	choice := input.Choices[0]
	var rawChoices []json.RawMessage
	if err := json.Unmarshal(inputKeys["choices"], &rawChoices); err != nil {
		t.Fatal(err)
	}
	for _, rawChoice := range rawChoices {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(rawChoice, &keys); err != nil {
			t.Fatal(err)
		}
		if len(keys) != 6 {
			t.Fatalf("expand source choice keys = %d, want exactly the six contract keys: %s", len(keys), string(rawChoice))
		}
		for _, key := range []string{"choiceId", "fieldPath", "label", "logicalType", "isPopulated", "columnId"} {
			if _, ok := keys[key]; !ok {
				t.Fatalf("expand source choice omitted contract key %q: %s", key, string(rawChoice))
			}
		}
	}
	if choice.FieldPath == nil || *choice.FieldPath != expandSourceHTTPFieldPath {
		t.Fatalf("expand source choice fieldPath = %v, want the generated root primitive array %q", choice.FieldPath, expandSourceHTTPFieldPath)
	}
	if choice.ChoiceID == nil || *choice.ChoiceID == "" {
		t.Fatalf("expand source choice lacked its signed choiceId: %s", capabilitiesHTTP.Body)
	}
	if choice.IsPopulated == nil || !*choice.IsPopulated {
		t.Fatalf("expand source choice lost the authorized populated fact: %#v", choice)
	}
	if choice.LogicalType == nil || *choice.LogicalType != "string" {
		t.Fatalf("expand source choice logicalType = %v, want the array element logical type", choice.LogicalType)
	}
	if choice.ColumnID == nil || *choice.ColumnID != "" {
		t.Fatalf("expand source choice columnId = %v, want an honest empty string for a fresh Expand step", choice.ColumnID)
	}
	choiceID := *choice.ChoiceID

	candidateConstruction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID:     expandSourceHTTPStep,
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind: ws.ConstructionOperationExpand,
			Expand: &ws.ConstructionExpand{
				ConstructionID: expandSourceHTTPStep, InputColumnID: expandSourceHTTPInput, OutputColumnID: expandSourceHTTPItem,
				EmptyPolicy: ws.ConstructionExpandEmptyExclude,
				ExpandedField: &ws.ConstructionExpandedFieldSource{
					Path: "status", Cardinality: "many", FHIRType: "string", LogicalType: "code",
				},
			},
		},
		Outputs: append(append([]ws.StageColumn(nil), columns...), ws.StageColumn{
			ID: expandSourceHTTPItem, Name: "expand_uri_item", Label: "Uri item", Type: "string",
		}),
	}}}
	candidateJSON, err := json.Marshal(candidateConstruction)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(candidateJSON, []byte(`"path":"status"`)) {
		t.Fatal("the forged expandedField must reach the wire to be caught")
	}
	storedBefore, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}

	validProposalHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"procedures","changedStepId":%q,"candidateConstruction":%s,"expandSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, expandSourceHTTPStep, candidateJSON, choiceID, expandSourceHTTPInput,
	))
	if validProposalHTTP.StatusCode != http.StatusOK {
		t.Fatalf("expand-source proposal status=%d body=%s", validProposalHTTP.StatusCode, validProposalHTTP.Body)
	}
	if !bytes.Contains([]byte(validProposalHTTP.Body), []byte(`"sourceProjections"`)) ||
		!bytes.Contains([]byte(validProposalHTTP.Body), []byte(fmt.Sprintf(`"fieldPath":%q`, expandSourceHTTPFieldPath))) {
		t.Fatalf("expand-source selection did not reach the lifecycle entry and survive into the candidate: %s", validProposalHTTP.Body)
	}
	var proposalEnvelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(validProposalHTTP.Body), &proposalEnvelope); err != nil {
		t.Fatal(err)
	}
	proposalStepsRaw, ok := proposalEnvelope["candidateConstruction"]
	if !ok {
		t.Fatalf("proposal response lost candidateConstruction: %s", validProposalHTTP.Body)
	}
	expandRaw, signed := expandSourceHTTPSignedField(t, proposalStepsRaw, "proposal candidateConstruction")
	expandSourceHTTPAssertSigned(t, expandRaw, signed, "proposal candidateConstruction")
	proposalProjections := struct {
		SourceProjections []struct {
			ColumnID    string `json:"columnId"`
			OwnerStepID string `json:"ownerStepId"`
			FieldPath   string `json:"fieldPath"`
		} `json:"sourceProjections"`
	}{}
	if err := json.Unmarshal(proposalStepsRaw, &proposalProjections); err != nil {
		t.Fatal(err)
	}
	if len(proposalProjections.SourceProjections) != 1 ||
		proposalProjections.SourceProjections[0].ColumnID != expandSourceHTTPInput ||
		proposalProjections.SourceProjections[0].OwnerStepID != expandSourceHTTPStep ||
		proposalProjections.SourceProjections[0].FieldPath != expandSourceHTTPFieldPath {
		t.Fatalf("proposal candidate sourceProjections = %#v, want the %q column owned by the expand step: %s", proposalProjections.SourceProjections, expandSourceHTTPInput, validProposalHTTP.Body)
	}
	var proposal struct {
		ProposalID string `json:"proposalId"`
	}
	if err := json.Unmarshal([]byte(validProposalHTTP.Body), &proposal); err != nil {
		t.Fatal(err)
	}
	if proposal.ProposalID == "" {
		t.Fatalf("expand-source proposal lacked its proposalId: %s", validProposalHTTP.Body)
	}

	storedUntouched, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if storedUntouched.DraftVersion != storedBefore.DraftVersion || storedUntouched.DraftDigest != storedBefore.DraftDigest || !bytes.Equal(storedUntouched.DraftConfig, storedBefore.DraftConfig) {
		t.Fatalf("expand-source proposal mutated the stored draft: before=%#v after=%#v", storedBefore, storedUntouched)
	}

	garbageChoiceHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-proposals", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"outputId":"procedures","changedStepId":%q,"candidateConstruction":%s,"expandSources":[{"choiceId":%q,"columnId":%q}]}`,
		snapshot.Token, digest, expandSourceHTTPStep, candidateJSON, "forged-choice", expandSourceHTTPInput,
	))
	if garbageChoiceHTTP.StatusCode != http.StatusBadRequest && garbageChoiceHTTP.StatusCode != http.StatusConflict && garbageChoiceHTTP.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("garbage expandSources choiceId status=%d, want a 4xx refusal: %s", garbageChoiceHTTP.StatusCode, garbageChoiceHTTP.Body)
	}
	if !bytes.Contains([]byte(garbageChoiceHTTP.Body), []byte("INVALID_EXPAND_SOURCE_CHOICE")) {
		t.Fatalf("garbage expandSources choiceId did not reach choice validation: %s", garbageChoiceHTTP.Body)
	}

	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"expand-source-apply","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"procedures","proposalId":%q}]}`,
		ws.CurrentSemanticsVersion, snapshot.Token, digest, proposal.ProposalID,
	))
	if applyHTTP.StatusCode != http.StatusOK {
		t.Fatalf("apply expand-source proposal status=%d body=%s", applyHTTP.StatusCode, applyHTTP.Body)
	}
	var applied struct {
		DraftVersion int    `json:"draftVersion"`
		DraftDigest  string `json:"draftDigest"`
	}
	if err := json.Unmarshal([]byte(applyHTTP.Body), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.DraftVersion != 2 || applied.DraftDigest == "" {
		t.Fatalf("expand-source apply did not advance the draft: %#v body=%s", applied, applyHTTP.Body)
	}

	advancedCapabilitiesHTTP := requestJSON(t, app, http.MethodPost, basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":2,"expectedDraftDigest":%q,"outputId":"procedures","stageId":"source_projection"}`,
		snapshot.Token, applied.DraftDigest,
	))
	if advancedCapabilitiesHTTP.StatusCode != http.StatusOK {
		t.Fatalf("post-apply expand-source capabilities status=%d body=%s", advancedCapabilitiesHTTP.StatusCode, advancedCapabilitiesHTTP.Body)
	}
	var advancedEnvelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(advancedCapabilitiesHTTP.Body), &advancedEnvelope); err != nil {
		t.Fatal(err)
	}
	baseConstructionRaw, ok := advancedEnvelope["baseConstruction"]
	if !ok {
		t.Fatalf("capabilities wire response dropped baseConstruction: %s", advancedCapabilitiesHTTP.Body)
	}
	if !strings.Contains(string(baseConstructionRaw), `"expandedField"`) {
		t.Fatalf("capabilities baseConstruction dropped the server-signed expandedField: %s", baseConstructionRaw)
	}
	baseExpandRaw, baseSigned := expandSourceHTTPSignedField(t, baseConstructionRaw, "capabilities baseConstruction")
	expandSourceHTTPAssertSigned(t, baseExpandRaw, baseSigned, "capabilities baseConstruction")
	baseProjections := struct {
		SourceProjections []struct {
			ColumnID    string `json:"columnId"`
			OwnerStepID string `json:"ownerStepId"`
			FieldPath   string `json:"fieldPath"`
		} `json:"sourceProjections"`
	}{}
	if err := json.Unmarshal(baseConstructionRaw, &baseProjections); err != nil {
		t.Fatal(err)
	}
	if len(baseProjections.SourceProjections) != 1 ||
		baseProjections.SourceProjections[0].ColumnID != expandSourceHTTPInput ||
		baseProjections.SourceProjections[0].OwnerStepID != expandSourceHTTPStep ||
		baseProjections.SourceProjections[0].FieldPath != expandSourceHTTPFieldPath {
		t.Fatalf("durable baseConstruction sourceProjections = %#v, want the expand-owned %q projection: %s", baseProjections.SourceProjections, expandSourceHTTPInput, baseConstructionRaw)
	}

	_, advancedInput := expandSourceHTTPDecodedInput(t, advancedCapabilitiesHTTP.Body)
	if len(advancedInput.Choices) != 1 || advancedInput.Choices[0].ColumnID == nil || *advancedInput.Choices[0].ColumnID != expandSourceHTTPInput {
		t.Fatalf("post-apply expandSourceInput.choices = %#v, want the owned materialized columnId %q", advancedInput.Choices, expandSourceHTTPInput)
	}

	storedAdvanced, err := service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		t.Fatal(err)
	}
	if storedAdvanced.DraftVersion != 2 || storedAdvanced.DraftDigest != applied.DraftDigest {
		t.Fatalf("stored draft did not converge with the accepted apply: stored=%#v applied=%#v", storedAdvanced, applied)
	}
	if !bytes.Contains(storedAdvanced.DraftConfig, []byte(`"ownerStepId":"`+expandSourceHTTPStep+`"`)) ||
		!bytes.Contains(storedAdvanced.DraftConfig, []byte(`"path":"`+expandSourceHTTPFieldPath+`"`)) {
		t.Fatalf("persisted draft lost the expand-owned signed projection state: %s", storedAdvanced.DraftConfig)
	}
}
