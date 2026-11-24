package explorerapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"

	"github.com/calypr/loom/internal/testkit/explorerfixtures"

	"github.com/calypr/loom/internal/testkit/constructionfixtures"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

func TestConstructionFilterSourceHTTPContractPreservesCapabilitiesAndProposals(t *testing.T) {
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_active", NodeID: "n_patient", ResourceType: "Patient",
		FieldPath: "active", Label: "Active", LogicalType: "boolean", Cardinality: "OPTIONAL_ONE",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 135,
	})
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
	document.Construction.Steps = []ws.ConstructionStep{{
		ID: "only_step", Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind:   ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{ColumnID: columns[0].ID, Operator: ws.ConstructionFilterExists},
		},
		Outputs: columns,
	}}
	workspace.Documents[0] = document
	workspace, err = ws.MigrateLegacyContributors(workspace, ws.CatalogFromCapability(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = ws.MigrateLosslessDefaults(workspace, ws.CatalogFromCapability(snapshot, "custom")).NormalizePresentationOrders()
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
	store := explorerfixtures.NewStore()
	if _, err := store.Create(context.Background(), explorer.Explorer{
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
	inputKeys, input := explorerfixtures.DecodeFilterSourceInput(t, capabilitiesHTTP.Body)
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
	for _, rawChoice := range rawSourceInputChoices(t, capabilitiesHTTP.Body, "filterSourceInput") {
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
	candidateConstruction := ws.Construction{Version: ws.ConstructionVersion, Steps: []ws.ConstructionStep{{
		ID:     "only_step",
		Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
		Operation: ws.ConstructionOperation{
			Kind: ws.ConstructionOperationFilter,
			Filter: &ws.ConstructionFilter{
				ColumnID: projectionColumnID, Operator: ws.ConstructionFilterEquals,
				Values: []ws.FilterValue{{Kind: ws.ConstructionFilterBoolean, Boolean: filterSourceHTTPBool(true)}},
			},
		},
		Outputs: append(append([]ws.StageColumn(nil), columns...), ws.StageColumn{
			ID: projectionColumnID, Name: ws.ConstructionSourceProjectionName(projectionColumnID), Label: "Active", Type: "boolean",
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

func filterSourceHTTPBool(value bool) *bool { return &value }
