package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

type fs1Env struct {
	t              *testing.T
	app            *fiber.App
	basePath       string
	service        *explorer.Service
	snapshot       capability.Snapshot
	initialVersion int
	initialDigest  string
}

func fs1New(t *testing.T) *fs1Env {
	t.Helper()
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
		SourceRows: func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
			command := request.Command
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
	return &fs1Env{t: t, app: app, basePath: "/api/v1/projects/project-a/explorers/custom/authoring/v2", service: service, snapshot: snapshot, initialVersion: 1, initialDigest: digest}
}

func (e *fs1Env) fs1AddColumn(commandID, candidateID, title string) (authoringv2.Document, int64, string) {
	e.t.Helper()
	stored, err := e.service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		e.t.Fatal(err)
	}
	current, err := authoringv2.DecodeWorkspace(stored.DraftConfig)
	if err != nil {
		e.t.Fatal(err)
	}
	digest, err := current.Digest()
	if err != nil {
		e.t.Fatal(err)
	}
	httpResponse := requestJSON(e.t, e.app, http.MethodPost, e.basePath+"/commands", fmt.Sprintf(
		`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[{"type":"ADD_COLUMN","outputId":"patients","occurrenceId":"base","candidateId":%q,"projectionMode":"VALUE","initialPresentation":"TABLE","title":%q}]}`,
		commandID, authoringv2.CurrentSemanticsVersion, e.snapshot.Token, stored.DraftVersion, digest, candidateID, title,
	))
	if httpResponse.StatusCode != http.StatusOK {
		e.t.Fatalf("ADD_COLUMN %s status=%d body=%s", candidateID, httpResponse.StatusCode, httpResponse.Body)
	}
	stored, err = e.service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		e.t.Fatal(err)
	}
	current, err = authoringv2.DecodeWorkspace(stored.DraftConfig)
	if err != nil {
		e.t.Fatal(err)
	}
	upgraded, err := authoringv2.UpgradeDocumentToConstruction(current.Documents[0])
	if err != nil {
		e.t.Fatal(err)
	}
	digest, err = current.Digest()
	if err != nil {
		e.t.Fatal(err)
	}
	return upgraded, stored.DraftVersion, digest
}

func (e *fs1Env) fs1SignedChoice(stageID, fieldPath string) string {
	e.t.Helper()
	stored, err := e.service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		e.t.Fatal(err)
	}
	current, err := authoringv2.DecodeWorkspace(stored.DraftConfig)
	if err != nil {
		e.t.Fatal(err)
	}
	digest, err := current.Digest()
	if err != nil {
		e.t.Fatal(err)
	}
	capabilitiesHTTP := requestJSON(e.t, e.app, http.MethodPost, e.basePath+"/construction-capabilities", fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","stageId":%q}`,
		e.snapshot.Token, stored.DraftVersion, digest, stageID,
	))
	if capabilitiesHTTP.StatusCode != http.StatusOK {
		e.t.Fatalf("capabilities stage=%s status=%d body=%s", stageID, capabilitiesHTTP.StatusCode, capabilitiesHTTP.Body)
	}
	if stageID == "source_projection" {
		if _, input := decodedDeriveSourceInput(e.t, capabilitiesHTTP.Body); true {
			for _, choice := range input.Choices {
				if choice.FieldPath != nil && *choice.FieldPath == fieldPath && choice.ChoiceID != nil {
					return *choice.ChoiceID
				}
			}
		}
		if _, input := decodedFilterSourceInput(e.t, capabilitiesHTTP.Body); true {
			for _, choice := range input.Choices {
				if choice.FieldPath != nil && *choice.FieldPath == fieldPath && choice.ChoiceID != nil {
					return *choice.ChoiceID
				}
			}
		}
	}
	e.t.Fatalf("no signed choice for field %q in stage %q: %s", fieldPath, stageID, capabilitiesHTTP.Body)
	return ""
}

func (e *fs1Env) fs1Propose(stepID, candidateJSON, sourcesJSON string, version int64, digest string) testHTTPResponse {
	e.t.Helper()
	body := fmt.Sprintf(
		`{"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"outputId":"patients","changedStepId":%q,"candidateConstruction":%s%s}`,
		e.snapshot.Token, version, digest, stepID, candidateJSON, sourcesJSON,
	)
	return requestJSON(e.t, e.app, http.MethodPost, e.basePath+"/construction-proposals", body)
}

func fs1StageColumns(document authoringv2.Document) []authoringv2.StageColumn {
	columns := make([]authoringv2.StageColumn, 0, len(document.Columns))
	for _, column := range document.Columns {
		columns = append(columns, authoringv2.StageColumn{ID: column.ColumnID, Name: column.Column, Label: column.Label, Type: column.LogicalType})
	}
	return columns
}

func fs1DeriveStep(stepID, outputID, slotOrExisting string, extraSlot *authoringv2.StageColumn, columns []authoringv2.StageColumn, literal int64) authoringv2.ConstructionStep {
	value := literal
	outputs := append(append([]authoringv2.StageColumn(nil), columns...), derivedTotalColumn())
	if extraSlot != nil {
		outputs = append(outputs, *extraSlot)
	}
	return authoringv2.ConstructionStep{
		ID:     stepID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID: stepID, OutputColumnID: outputID, Operation: authoringv2.ConstructionDerivedAdd,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: slotOrExisting},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &value}},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}},
		Outputs: outputs,
	}
}

func derivedTotalColumn() authoringv2.StageColumn {
	return authoringv2.StageColumn{ID: "derived_total", Name: "derived_total", Label: "Derived total", Type: "integer"}
}

func fs1FilterStep(stepID, slotColumnID string, columns []authoringv2.StageColumn) authoringv2.ConstructionStep {
	truth := true
	outputs := append(append([]authoringv2.StageColumn(nil), columns...), authoringv2.StageColumn{
		ID: slotColumnID, Name: authoringv2.ConstructionSourceProjectionName(slotColumnID), Label: "Active", Type: "boolean",
	})
	return authoringv2.ConstructionStep{
		ID:     stepID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationFilter, Filter: &authoringv2.ConstructionFilter{
			ColumnID: slotColumnID, Operator: authoringv2.ConstructionFilterEquals,
			Values: []authoringv2.FilterValue{{Kind: authoringv2.ConstructionFilterBoolean, Boolean: &truth}},
		}},
		Outputs: outputs,
	}
}

func fs1MarshalConstruction(t *testing.T, construction authoringv2.Construction) string {
	t.Helper()
	raw, err := json.Marshal(construction)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestFS1DeriveSignedSlotSameFieldRefused(t *testing.T) {
	env := fs1New(t)
	document, version, digest := env.fs1AddColumn("fs1-derive-add", "c_patient_birth", "Birth count")
	choiceID := env.fs1SignedChoice("source_projection", "multipleBirthInteger")
	const slot = "derive_input_fs1_a"
	step := fs1DeriveStep("derive_step", "derived_total", slot, &authoringv2.StageColumn{
		ID: slot, Name: authoringv2.ConstructionSourceProjectionName(slot), Label: "Birth count", Type: "integer",
	}, fs1StageColumns(document), 3)
	candidateJSON := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}})
	response := env.fs1Propose("derive_step", candidateJSON, fmt.Sprintf(`,"deriveSources":[{"choiceId":%q,"columnId":%q}]`, choiceID, slot), version, digest)
	t.Logf("F-DS1 derive collision status=%d body=%s", response.StatusCode, response.Body)
	if response.StatusCode != http.StatusUnprocessableEntity ||
		!bytes.Contains([]byte(response.Body), []byte(`DERIVE_SOURCE_FIELD_ALREADY_PROJECTED`)) {
		t.Errorf("signed derive slot for a user-projected field must be a named 422 DERIVE_SOURCE_FIELD_ALREADY_PROJECTED; got %d body=%s",
			response.StatusCode, response.Body)
	}
}

func TestFS1DeriveSignedSlotDistinctFieldStaysOK(t *testing.T) {
	env := fs1New(t)
	document, version, digest := env.fs1AddColumn("fs1-distinct-add", "c_patient_id", "ID")
	choiceID := env.fs1SignedChoice("source_projection", "multipleBirthInteger")
	const slot = "derive_input_fs1_b"
	step := fs1DeriveStep("derive_step", "derived_total", slot, &authoringv2.StageColumn{
		ID: slot, Name: authoringv2.ConstructionSourceProjectionName(slot), Label: "Birth count", Type: "integer",
	}, fs1StageColumns(document), 3)
	candidateJSON := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}})
	response := env.fs1Propose("derive_step", candidateJSON, fmt.Sprintf(`,"deriveSources":[{"choiceId":%q,"columnId":%q}]`, choiceID, slot), version, digest)
	t.Logf("distinct-field derive proposal status=%d body=%.300s", response.StatusCode, response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains([]byte(response.Body), []byte(`"previewStatus"`)) {
		t.Fatalf("distinct-field signed derive slot regressed: status=%d body=%s", response.StatusCode, response.Body)
	}
}

func TestFS1DeriveOperandExistingColumnStaysOK(t *testing.T) {
	env := fs1New(t)
	document, version, digest := env.fs1AddColumn("fs1-dedupe-add", "c_patient_birth", "Birth count")
	userColumnID := ""
	for _, column := range document.Columns {
		if column.Source.Field != nil && column.Source.Field.Path == "multipleBirthInteger" {
			userColumnID = column.ColumnID
		}
	}
	if userColumnID == "" {
		t.Fatal("ADD_COLUMN persisted no multipleBirthInteger user column")
	}
	step := fs1DeriveStep("derive_step", "derived_total", userColumnID, nil, fs1StageColumns(document), 3)
	candidateJSON := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}})
	response := env.fs1Propose("derive_step", candidateJSON, "", version, digest)
	t.Logf("case-D dedupe proposal status=%d body=%.300s", response.StatusCode, response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains([]byte(response.Body), []byte(`"previewStatus"`)) {
		t.Fatalf("bind-to-existing-column dedupe regressed: status=%d body=%s", response.StatusCode, response.Body)
	}
}

func TestFS1FilterSignedSlotSameFieldRefused(t *testing.T) {
	env := fs1New(t)
	document, version, digest := env.fs1AddColumn("fs1-filter-add", "c_patient_active", "Active")
	choiceID := env.fs1SignedChoice("source_projection", "active")
	const slot = "filter_input_fs1_a"
	step := fs1FilterStep("filter_step", slot, fs1StageColumns(document))
	candidateJSON := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}})
	response := env.fs1Propose("filter_step", candidateJSON, fmt.Sprintf(`,"filterSources":[{"choiceId":%q,"columnId":%q}]`, choiceID, slot), version, digest)
	t.Logf("F-DS1 filter twin status=%d body=%s", response.StatusCode, response.Body)
	if response.StatusCode != http.StatusUnprocessableEntity ||
		!bytes.Contains([]byte(response.Body), []byte(`FILTER_SOURCE_FIELD_ALREADY_PROJECTED`)) {
		t.Errorf("signed filter slot for a user-projected field must be a named 422 FILTER_SOURCE_FIELD_ALREADY_PROJECTED; got %d body=%s",
			response.StatusCode, response.Body)
	}
}

func TestFS1FilterSignedSlotDistinctFieldStaysOK(t *testing.T) {
	env := fs1New(t)
	document, version, digest := env.fs1AddColumn("fs1-filter-distinct-add", "c_patient_id", "ID")
	choiceID := env.fs1SignedChoice("source_projection", "active")
	const slot = "filter_input_fs1_b"
	step := fs1FilterStep("filter_step", slot, fs1StageColumns(document))
	candidateJSON := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}})
	response := env.fs1Propose("filter_step", candidateJSON, fmt.Sprintf(`,"filterSources":[{"choiceId":%q,"columnId":%q}]`, choiceID, slot), version, digest)
	t.Logf("distinct-field filter proposal status=%d body=%.300s", response.StatusCode, response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains([]byte(response.Body), []byte(`"previewStatus"`)) {
		t.Fatalf("distinct-field signed filter slot regressed: status=%d body=%s", response.StatusCode, response.Body)
	}
}

func (e *fs1Env) fs1V2Pin() (int64, string, authoringv2.Document) {
	e.t.Helper()
	stored, err := e.service.Get(context.Background(), "project-a", "custom")
	if err != nil {
		e.t.Fatal(err)
	}
	current, err := authoringv2.DecodeWorkspace(stored.DraftConfig)
	if err != nil {
		e.t.Fatal(err)
	}
	digest, err := current.Digest()
	if err != nil {
		e.t.Fatal(err)
	}
	upgraded, err := authoringv2.UpgradeDocumentToConstruction(current.Documents[0])
	if err != nil {
		e.t.Fatal(err)
	}
	return stored.DraftVersion, digest, upgraded
}

func (e *fs1Env) fs1V2Command(commandID, commandsJSON string, version int64, digest string) testHTTPResponse {
	e.t.Helper()
	body := fmt.Sprintf(
		`{"commandId":%q,"semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":%d,"expectedDraftDigest":%q,"commands":[%s]}`,
		commandID, authoringv2.CurrentSemanticsVersion, e.snapshot.Token, version, digest, commandsJSON,
	)
	return requestJSON(e.t, e.app, http.MethodPost, e.basePath+"/commands", body)
}

func TestFS1RootPrefixedUserColumnStaysOK(t *testing.T) {
	env := fs1New(t)
	document, _, _ := env.fs1AddColumn("fs1-prefix-add", "c_patient_birth", "Birth count")
	userColumnName := ""
	for _, column := range document.Columns {
		if column.Source.Field != nil && column.Source.Field.Path == "multipleBirthInteger" {
			userColumnName = column.Column
		}
	}
	if userColumnName == "" {
		t.Fatal("no user column on multipleBirthInteger")
	}
	version, digest, _ := env.fs1V2Pin()
	update := fmt.Sprintf(`{"type":"UPDATE_COLUMN_SOURCE","outputId":"patients","column":%q,"source":{"kind":"field","field":{"path":"root.multipleBirthInteger","projectionMode":"VALUE"}}}`, userColumnName)
	updateHTTP := env.fs1V2Command("fs1-prefix-update", update, version, digest)
	t.Logf("PREFIX update-column-source status=%d body=%.200s", updateHTTP.StatusCode, updateHTTP.Body)
	if updateHTTP.StatusCode != http.StatusOK {
		t.Fatalf("shipped UPDATE_COLUMN_SOURCE refused root-prefixed path: %d %s", updateHTTP.StatusCode, updateHTTP.Body)
	}
	version, digest, persisted := env.fs1V2Pin()
	persistedPath := ""
	for _, column := range persisted.Columns {
		if column.Column == userColumnName && column.Source.Field != nil {
			persistedPath = column.Source.Field.Path
		}
	}
	if persistedPath != "root.multipleBirthInteger" {
		t.Fatalf("root-prefixed path not persisted verbatim (got %q)", persistedPath)
	}
	choiceID := env.fs1SignedChoice("source_projection", "multipleBirthInteger")
	const slot = "derive_input_fs1_prefix"
	step := fs1DeriveStep("derive_step", "derived_total", slot, &authoringv2.StageColumn{
		ID: slot, Name: authoringv2.ConstructionSourceProjectionName(slot), Label: "Birth count", Type: "integer",
	}, fs1StageColumns(persisted), 3)
	candidateJSON := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{step}})
	response := env.fs1Propose("derive_step", candidateJSON, fmt.Sprintf(`,"deriveSources":[{"choiceId":%q,"columnId":%q}]`, choiceID, slot), version, digest)
	t.Logf("PREFIX propose status=%d body=%.300s", response.StatusCode, response.Body)
	if response.StatusCode != http.StatusOK || !bytes.Contains([]byte(response.Body), []byte(`"previewStatus"`)) {
		t.Errorf("root-prefixed user column + verbatim-distinct signed slot must stay 200 (guard compares verbatim); got %d body=%s", response.StatusCode, response.Body)
	}
}

func fs1AppliedStep(stepID, outputColID, slot string, columnsOut []authoringv2.StageColumn, literal int64) authoringv2.ConstructionStep {
	value := literal
	return authoringv2.ConstructionStep{
		ID:     stepID,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
			ConstructionID: stepID, OutputColumnID: outputColID, Operation: authoringv2.ConstructionDerivedAdd,
			Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionColumnOperand, ColumnID: slot},
			Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger, Integer: &value}},
			MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
		}},
		Outputs: append(append([]authoringv2.StageColumn(nil), columnsOut...),
			authoringv2.StageColumn{ID: slot, Name: authoringv2.ConstructionSourceProjectionName(slot), Label: "Birth count", Type: "integer"}),
	}
}

func TestFS1SecondSignedSlotConflictsAppliedSlot(t *testing.T) {
	env := fs1New(t)
	document, version, digest := env.fs1AddColumn("fs1-two-add", "c_patient_id", "ID")
	baseCols := fs1StageColumns(document)
	choiceID := env.fs1SignedChoice("source_projection", "multipleBirthInteger")

	const slotA = "derive_input_fs1_two_a"
	stepA := fs1AppliedStep("derive_a", "derived_a", slotA,
		append(append([]authoringv2.StageColumn(nil), baseCols...), authoringv2.StageColumn{ID: "derived_a", Name: "derived_a", Label: "Derived A", Type: "integer"}), 1)
	candA := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{stepA}})
	proposeA := env.fs1Propose("derive_a", candA, fmt.Sprintf(`,"deriveSources":[{"choiceId":%q,"columnId":%q}]`, choiceID, slotA), version, digest)
	if proposeA.StatusCode != http.StatusOK {
		t.Fatalf("proposal A failed: %d %s", proposeA.StatusCode, proposeA.Body)
	}
	var prA struct {
		ProposalID string `json:"proposalId"`
	}
	if err := json.Unmarshal([]byte(proposeA.Body), &prA); err != nil || prA.ProposalID == "" {
		t.Fatalf("no proposalId for A: %v %s", err, proposeA.Body)
	}
	version, digest, _ = env.fs1V2Pin()
	applyA := env.fs1V2Command("fs1-two-apply-a",
		fmt.Sprintf(`{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"patients","proposalId":%q}`, prA.ProposalID), version, digest)
	if applyA.StatusCode != http.StatusOK {
		t.Fatalf("apply A failed: %d %s", applyA.StatusCode, applyA.Body)
	}
	version, digest, persisted := env.fs1V2Pin()
	persistedA := false
	if persisted.Construction != nil {
		for _, projection := range persisted.Construction.SourceProjections {
			if projection.ColumnID == slotA && projection.FieldPath == "multipleBirthInteger" {
				persistedA = true
			}
		}
	}
	if !persistedA {
		t.Fatalf("slot A projection not persisted: %+v", persisted.Construction)
	}

	const slotB = "derive_input_fs1_two_b"
	stepB := fs1AppliedStep("derive_b", "derived_b", slotB,
		append(append([]authoringv2.StageColumn(nil), baseCols...),
			authoringv2.StageColumn{ID: "derived_a", Name: "derived_a", Label: "Derived A", Type: "integer"},
			authoringv2.StageColumn{ID: "derived_b", Name: "derived_b", Label: "Derived B", Type: "integer"},
			authoringv2.StageColumn{ID: slotA, Name: authoringv2.ConstructionSourceProjectionName(slotA), Label: "Birth count", Type: "integer"}), 2)
	stepsB := append(append([]authoringv2.ConstructionStep(nil), persisted.Construction.Steps...), stepB)
	candB := fs1MarshalConstruction(t, authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: stepsB})
	proposeB := env.fs1Propose("derive_b", candB, fmt.Sprintf(`,"deriveSources":[{"choiceId":%q,"columnId":%q}]`, choiceID, slotB), version, digest)
	t.Logf("TWO proposal B status=%d body=%.300s", proposeB.StatusCode, proposeB.Body)
	if proposeB.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains([]byte(proposeB.Body), []byte(`DERIVE_SOURCE_FIELD_ALREADY_PROJECTED`)) {
		t.Errorf("second signed slot on an applied slot's field must be a named 422 DERIVE_SOURCE_FIELD_ALREADY_PROJECTED; got %d body=%s",
			proposeB.StatusCode, proposeB.Body)
	}
}
