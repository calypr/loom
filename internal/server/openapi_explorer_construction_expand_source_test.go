package server

// Wire-level contract for the server-signed EXPAND source lane over HTTP.
// The lane signs ConstructionExpand.ExpandedField from resolved root primitive
// array facts; the capabilities DTO once dropped that field entirely (only a
// browser journey caught it). These assertions parse the RAW HTTP body, which
// is sound precisely because the handlers marshal through the generated
// loomapi DTOs (getConstructionCapabilitiesDirect / construction-proposal
// return typed loomapi responses via directAuthoringJSON); if a refactor ever
// returned raw authoring JSON directly, the DTO-drop class would be
// structurally impossible but this comment's premise must be re-verified.

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
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/gofiber/fiber/v3"
)

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

// expandSourceHTTPProcedureSnapshot is the authorized catalog for a
// Procedure-rooted table. Procedure.instantiatesUri[] is the generated
// primitive direct-root array (canonical instantiatesUri[], ARRAY/MANY,
// FHIRType string, non-reference) that the generated schema admits for the
// signed EXPAND lane; Patient admits none.
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

// expandSourceHTTPProcedureWorkspace mirrors baselineExplorerWorkspaceV2 for a
// Procedure root with one saved output column; the construction prefix stays
// empty so the EXPAND step arrives as the first proposed step over HTTP.
// The signed EXPAND lane only accepts a direct-root primitive many-cardinality
// non-reference array, which the generated schema admits for Procedure via
// instantiatesUri[].
func expandSourceHTTPProcedureWorkspace() []byte {
	visible, order := true, 0
	workspace := authoringv2.Workspace{APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind, Explorer: authoringv2.ExplorerMetadata{Title: "Procedures"}, Documents: []authoringv2.Document{{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "procedures", Title: "Procedures", RowLabel: "Procedures"}, RootResourceType: "Procedure", Route: authoringv2.RouteNode{OccurrenceID: "base", ResourceType: "Procedure"}, Columns: []authoringv2.Column{{Column: "procedure_id", Label: "Procedure ID", LogicalType: "string", OccurrenceID: "base", Source: authoringv2.ColumnSource{Kind: authoringv2.SourceField, Field: &authoringv2.FieldSource{Path: "id", ProjectionMode: "FIRST"}}, Table: &authoringv2.TablePresentation{Visible: &visible, Order: &order}}}}}, Tabs: []authoringv2.Tab{{ID: "procedures-tab", Title: "Procedures", OutputID: "procedures", Order: 0, Visible: true}}}
	raw, _ := json.Marshal(workspace)
	return raw
}

func expandSourceHTTPDecodedInput(t *testing.T, body string) (map[string]json.RawMessage, *expandSourceHTTPInputProbe) {
	t.Helper()
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode capabilities envelope: %v body=%s", err, body)
	}
	raw, ok := envelope["expandSourceInput"]
	if !ok {
		t.Fatalf("capabilities wire response dropped expandSourceInput: %s", body)
	}
	var input expandSourceHTTPInputProbe
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys, &input
}

// expandSourceHTTPSignedField walks the RAW wire construction JSON down to
// steps[0].operation.expand and returns both the raw expand subtree and the
// server-signed expandedField parsed as a string map. Nothing here decodes
// into generated loomapi DTOs: a DTO that silently drops expandedField must
// fail this helper, which is the exact regression the browser journey used to
// be the only witness for.
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
	workspace, err := authoringv2.DecodeWorkspace(expandSourceHTTPProcedureWorkspace())
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
	workspace.Documents[0] = document
	workspace, err = authoringv2.MigrateLegacyContributors(workspace, authoringV2Catalog(snapshot, "custom"))
	if err != nil {
		t.Fatal(err)
	}
	workspace = authoringv2.MigrateLosslessDefaults(workspace, authoringV2Catalog(snapshot, "custom")).NormalizePresentationOrders()
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
	store := newTestExplorerStore()
	if _, err := store.create(explorer.Explorer{
		Project: "project-a", ExplorerID: "custom", Title: "Procedures",
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

	// --- 1. capabilities advertises the signed EXPAND source lane on raw wire ---
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

	// --- 2. propose with a forged expandedField: the server signs over it ---
	candidateConstruction := authoringv2.Construction{Version: authoringv2.ConstructionVersion, Steps: []authoringv2.ConstructionStep{{
		ID:     expandSourceHTTPStep,
		Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
		Operation: authoringv2.ConstructionOperation{
			Kind: authoringv2.ConstructionOperationExpand,
			Expand: &authoringv2.ConstructionExpand{
				ConstructionID: expandSourceHTTPStep, InputColumnID: expandSourceHTTPInput, OutputColumnID: expandSourceHTTPItem,
				EmptyPolicy: authoringv2.ConstructionExpandEmptyExclude,
				ExpandedField: &authoringv2.ConstructionExpandedFieldSource{
					Path: "status", Cardinality: "many", FHIRType: "string", LogicalType: "code",
				},
			},
		},
		Outputs: append(append([]authoringv2.StageColumn(nil), columns...), authoringv2.StageColumn{
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

	// --- 3. negative: garbage expandSources choiceId is refused by code ---
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

	// --- 4. apply advances the draft; the persisted signed lane is durable ---
	applyHTTP := requestJSON(t, app, http.MethodPost, basePath+"/commands", fmt.Sprintf(
		`{"commandId":"expand-source-apply","semanticsVersion":%d,"snapshotToken":%q,"expectedDraftVersion":1,"expectedDraftDigest":%q,"commands":[{"type":"APPLY_CONSTRUCTION_PROPOSAL","outputId":"procedures","proposalId":%q}]}`,
		authoringv2.CurrentSemanticsVersion, snapshot.Token, digest, proposal.ProposalID,
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

	// --- 5. PARITY GUARD: after the draft advanced, capabilities must still
	// carry the server-signed expandedField on the RAW wire. This is the wire
	// contract the capabilities DTO once silently dropped. ---
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

	// The lane restores the durable materialized column into the advertised
	// choice only while the owner step still carries the coherent signed
	// payload — the same state the parity guard just proved is persisted.
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
