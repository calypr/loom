package explorerapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	receiptoutput "github.com/calypr/loom/internal/explorer/receipt/output"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	compiler "github.com/calypr/loom/internal/dataframe/compiler/facade"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/construction"
	"github.com/calypr/loom/internal/explorer/interpretation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
	receiptapi "github.com/calypr/loom/internal/explorer/receipt"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"
	httpctx "github.com/calypr/loom/internal/foundation/httpctx"
	"github.com/calypr/loom/internal/testkit"
	"github.com/gofiber/fiber/v3"
)

func TestAuthoringWorkspaceColumnIDMatchesOpenAPIContract(t *testing.T) {
	const stableID = "source_column_1"
	columnJSON := `{"columnId":"source_column_1","column":"member_id","label":"Member","occurrenceId":"base","source":{"kind":"field","field":{"path":"id"}}}`

	var generatedColumn loomapi.Column
	if err := json.Unmarshal([]byte(columnJSON), &generatedColumn); err != nil {
		t.Fatalf("generated Column rejects columnId: %v", err)
	}
	if generatedColumn.ColumnId == nil || *generatedColumn.ColumnId != stableID || generatedColumn.Column != "member_id" {
		t.Fatalf("generated Column = %#v, want stable columnId %q separate from public column %q", generatedColumn, stableID, "member_id")
	}

	spec, err := loomapi.GetSpec()
	if err != nil {
		t.Fatalf("load generated OpenAPI spec: %v", err)
	}
	columnSchemaRef, ok := spec.Components.Schemas["Column"]
	if !ok || columnSchemaRef.Value == nil {
		t.Fatal("OpenAPI Column schema is missing")
	}
	if columnSchemaRef.Value.Properties["columnId"] == nil {
		t.Fatal("OpenAPI Column schema is missing optional columnId")
	}
	for _, required := range columnSchemaRef.Value.Required {
		if required == "columnId" {
			t.Fatal("columnId must remain optional for legacy saved columns")
		}
	}

	stableWorkspace := columnIDContractWorkspace(stableID)
	stableState, err := directAuthoringJSON[loomapi.BuilderState](ws.BuilderState{
		APIVersion: ws.APIVersion,
		Kind:       ws.StateKind,
		Workspace:  &stableWorkspace,
	})
	if err != nil {
		t.Fatalf("convert saved Builder state through generated response type: %v", err)
	}
	stablePayload, err := json.Marshal(stableState)
	if err != nil {
		t.Fatalf("marshal generated Builder response: %v", err)
	}
	stableColumn := workspaceColumnJSON(t, stablePayload)
	if got := stableColumn["columnId"]; got != stableID {
		t.Fatalf("saved workspace columnId = %v, want %q; payload=%s", got, stableID, stablePayload)
	}
	if got := stableColumn["column"]; got != "member_id" {
		t.Fatalf("saved workspace public column = %v, want %q", got, "member_id")
	}
	if err := spec.ValidateSchemaJSON(columnSchemaRef.Value, stableColumn); err != nil {
		t.Fatalf("stable saved source column is rejected by OpenAPI: %v", err)
	}

	legacyWorkspace := columnIDContractWorkspace("")
	legacyState, err := directAuthoringJSON[loomapi.BuilderState](ws.BuilderState{
		APIVersion: ws.APIVersion,
		Kind:       ws.StateKind,
		Workspace:  &legacyWorkspace,
	})
	if err != nil {
		t.Fatalf("convert legacy Builder state through generated response type: %v", err)
	}
	legacyPayload, err := json.Marshal(legacyState)
	if err != nil {
		t.Fatalf("marshal legacy Builder response: %v", err)
	}
	legacyColumn := workspaceColumnJSON(t, legacyPayload)
	if _, present := legacyColumn["columnId"]; present {
		t.Fatalf("legacy saved column unexpectedly emitted columnId: %s", legacyPayload)
	}
	if err := spec.ValidateSchemaJSON(columnSchemaRef.Value, legacyColumn); err != nil {
		t.Fatalf("legacy saved source column without columnId is rejected by OpenAPI: %v", err)
	}
}

func TestSystemVersionCategoryColumnPassesOpenAPIAndExactEmptyCodeIsRejected(t *testing.T) {
	column := ws.Column{
		Column: "condition_category", Label: "Condition category", OccurrenceID: ws.RootOccurrenceID,
		Source: ws.ColumnSource{Kind: ws.SourceCodedValue, Lookup: &ws.LookupSource{
			Binding: &fhirbinding.CorrelatedBinding{
				KeyPath: "code.coding[]", ValueScope: fhirbinding.CorrelatedValueScopeCoding,
				SelectionScope: fhirbinding.CorrelatedSelectionScopeSystemVersion, VersionPath: "version",
				SystemPath: "system", CodePath: "code", ValuePath: "display", LogicalType: "string",
				ValueFallback: []string{"code"}, FallbackOnBlankString: true,
			},
			Key: &fhirbinding.CorrelatedKey{System: "https://example.org/condition", Code: "", Version: ""},
		}},
	}
	document := ws.Document{
		Rows: ws.RecordsRowDefinition(), Kind: ws.Kind,
		Output: ws.Output{ID: "conditions", Title: "Conditions"}, RootResourceType: "Condition",
		Route:   ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Condition"},
		Columns: []ws.Column{column},
	}
	if err := document.Validate(); err != nil {
		t.Fatalf("SYSTEM_VERSION category document rejected by authoring validation: %v", err)
	}
	encoded, err := json.Marshal(column)
	if err != nil {
		t.Fatal(err)
	}
	var generated loomapi.Column
	if err := json.Unmarshal(encoded, &generated); err != nil {
		t.Fatalf("generated Column rejects SYSTEM_VERSION key.code empty: %v", err)
	}
	payload, err := json.Marshal(generated)
	if err != nil {
		t.Fatal(err)
	}
	var columnJSON map[string]any
	if err := json.Unmarshal(payload, &columnJSON); err != nil {
		t.Fatal(err)
	}
	spec, err := loomapi.GetSpec()
	if err != nil {
		t.Fatal(err)
	}
	columnSchema := spec.Components.Schemas["Column"]
	if columnSchema == nil || columnSchema.Value == nil {
		t.Fatal("OpenAPI Column schema is missing")
	}
	if err := spec.ValidateSchemaJSON(columnSchema.Value, columnJSON); err != nil {
		t.Fatalf("encoded SYSTEM_VERSION category is rejected by the OpenAPI Column schema: %v", err)
	}
	workspace := ws.Workspace{
		APIVersion: ws.APIVersion, Kind: ws.WorkspaceKind,
		Explorer: ws.ExplorerMetadata{Title: "Builder"}, Documents: []ws.Document{document},
		Tabs: []ws.Tab{{ID: "conditions_tab", Title: "Conditions", OutputID: "conditions", Order: 0, Visible: true}},
	}
	if err := workspace.Validate(); err != nil {
		t.Fatalf("SYSTEM_VERSION category workspace rejected by authoring validation: %v", err)
	}
	workspacePayload, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var workspaceJSON map[string]any
	if err := json.Unmarshal(workspacePayload, &workspaceJSON); err != nil {
		t.Fatal(err)
	}
	workspaceSchema := spec.Components.Schemas["Workspace"]
	if workspaceSchema == nil || workspaceSchema.Value == nil {
		t.Fatal("OpenAPI Workspace schema is missing")
	}
	if err := spec.ValidateSchemaJSON(workspaceSchema.Value, workspaceJSON); err != nil {
		t.Fatalf("encoded SYSTEM_VERSION category workspace is rejected by the OpenAPI schema: %v", err)
	}

	exact := column
	lookup := *column.Source.Lookup
	binding := *lookup.Binding
	binding.SelectionScope = ""
	lookup.Binding = &binding
	exact.Source.Lookup = &lookup
	document.Columns = []ws.Column{exact}
	if err := document.Validate(); err == nil {
		t.Fatal("exact coded-value key with an empty code passed authoring validation")
	}
}

func columnIDContractWorkspace(columnID string) ws.Workspace {
	return ws.Workspace{
		APIVersion: ws.APIVersion,
		Kind:       ws.WorkspaceKind,
		Documents: []ws.Document{{
			Kind:             ws.Kind,
			Output:           ws.Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient",
			Columns: []ws.Column{{
				ColumnID:     columnID,
				Column:       "member_id",
				Label:        "Member",
				OccurrenceID: ws.RootOccurrenceID,
				Source: ws.ColumnSource{
					Kind:  ws.SourceField,
					Field: &ws.FieldSource{Path: "id"},
				},
			}},
		}},
	}
}

func workspaceColumnJSON(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	var response struct {
		Workspace struct {
			Documents []struct {
				Columns []map[string]any `json:"columns"`
			} `json:"documents"`
		} `json:"workspace"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		t.Fatalf("decode Builder response: %v; payload=%s", err, payload)
	}
	if len(response.Workspace.Documents) != 1 || len(response.Workspace.Documents[0].Columns) != 1 {
		t.Fatalf("Builder response has no single saved source column: %s", payload)
	}
	return response.Workspace.Documents[0].Columns[0]
}

func TestImportedSourceRemovalOpenAPIContractHasNoEnabledState(t *testing.T) {
	spec, err := loomapi.GetSpec()
	if err != nil {
		t.Fatalf("load generated OpenAPI spec: %v", err)
	}
	for _, name := range []string{"ImportedSourceStep", "ImportedSourceRuleAddition", "ImportedSourceStepChange"} {
		schema := spec.Components.Schemas[name]
		if schema == nil || schema.Value == nil {
			t.Fatalf("OpenAPI schema %q is missing", name)
		}
		if _, exists := schema.Value.Properties["enabled"]; exists {
			t.Errorf("OpenAPI schema %q still exposes enabled state", name)
		}
	}
	change := spec.Components.Schemas["ImportedSourceStepChange"].Value
	if _, exists := change.Properties["remove"]; !exists {
		t.Fatal("ImportedSourceStepChange is missing remove")
	}
}

func TestImportedSourceCommandSurvivesPublicTransport(t *testing.T) {
	var body loomapi.ApplyExplorerBuilderCommandsJSONRequestBody
	err := json.Unmarshal([]byte(fmt.Sprintf(`{"commandId":"source-edit","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"dynamicColumns[0]","maxColumns":32,"valueMode":"ALL"}}]}`, ws.CurrentSemanticsVersion)), &body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := directAuthoringJSON[authoringv2.ApplyCommandsRequest](body)
	if err != nil {
		t.Fatal(err)
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	step := request.Commands[0].SourceStep()
	if step == nil || step.Remove || step.StepID != "dynamicColumns[0]" || step.MaxColumns == nil || *step.MaxColumns != 32 || step.ValueMode != "ALL" {
		t.Fatalf("source settings lost in public transport: %#v", step)
	}
	removeBody := fmt.Sprintf(`{"commandId":"source-remove","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"dynamicColumns[0]","remove":true}}]}`, ws.CurrentSemanticsVersion)
	if err := json.Unmarshal([]byte(removeBody), &body); err != nil {
		t.Fatal(err)
	}
	request, err = directAuthoringJSON[authoringv2.ApplyCommandsRequest](body)
	if err != nil {
		t.Fatal(err)
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	if step := request.Commands[0].SourceStep(); step == nil || !step.Remove {
		t.Fatalf("source removal lost in public transport: %#v", step)
	}
	plan := &ws.ImportedSourcePlan{RemovedStepIDs: []string{"fields"}}
	var public loomapi.Document
	public, err = directAuthoringJSON[loomapi.Document](ws.Document{Kind: ws.Kind, Output: ws.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: ws.RouteNode{OccurrenceID: ws.RootOccurrenceID, ResourceType: "Patient"}, Rows: ws.RecordsRowDefinition(), Columns: []ws.Column{}, ImportedRecipe: &ws.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}, ImportedSource: plan})
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := directAuthoringJSON[ws.Document](public)
	if err != nil {
		t.Fatal(err)
	}
	if roundtrip.ImportedSource == nil || len(roundtrip.ImportedSource.Steps) != 0 || len(roundtrip.ImportedSource.RemovedStepIDs) != 1 || roundtrip.ImportedSource.RemovedStepIDs[0] != "fields" {
		t.Fatal("source removal lost in public response")
	}
}

type sourceStepTransportCapture struct {
	loomapi.StrictServerInterface
	request *authoringv2.ApplyCommandsRequest
}

func (c *sourceStepTransportCapture) ApplyExplorerBuilderCommands(_ context.Context, request loomapi.ApplyExplorerBuilderCommandsRequestObject) (loomapi.ApplyExplorerBuilderCommandsResponseObject, error) {
	value, err := directAuthoringJSON[authoringv2.ApplyCommandsRequest](*request.Body)
	if err != nil {
		return nil, err
	}
	if err := value.Validate(); err != nil {
		return nil, err
	}
	c.request = &value
	return loomapi.ApplyExplorerBuilderCommands200JSONResponse{}, nil
}

func TestImportedSourceHTTPUsesRemoveInsteadOfEnabled(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		status        int
		remove        bool
	}{
		{"legacy enabled rejected", `{"stepId":"fields","enabled":false}`, 400, false},
		{"injected", `{"stepId":"fields","remove":true,"unexpected":"data"}`, 400, false},
		{"settings", `{"stepId":"fields","maxColumns":12}`, 200, false},
		{"remove", `{"stepId":"fields","remove":true}`, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &sourceStepTransportCapture{}
			app := fiber.New()
			loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(capture, nil), loomapi.FiberServerOptions{})
			raw := fmt.Sprintf(`{"commandId":"edit-source","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":%s}]}`, ws.CurrentSemanticsVersion, test.payload)
			request := httptest.NewRequest("POST", "/api/v1/projects/project-a/explorers/patients/authoring/v2/commands", strings.NewReader(raw))
			request.Header.Set("Content-Type", "application/json")
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("HTTP %d, want %d", response.StatusCode, test.status)
			}
			if test.status == 400 {
				if capture.request != nil {
					t.Fatal("invalid source mutation reached application")
				}
			} else if capture.request == nil || capture.request.Commands[0].SourceStep() == nil || capture.request.Commands[0].SourceStep().Remove != test.remove {
				t.Fatal("source removal intent changed in transport")
			}
		})
	}
}

func TestImportedSourceTransportRejectsIncompleteOrInjectedSettings(t *testing.T) {
	for _, sourceStep := range []string{
		`{}`,
		`{"stepId":"fields","enabled":false}`,
		`{"stepId":"fields","remove":true,"parameters":{"fieldGroup":{"fields":[]}}}`,
	} {
		t.Run(sourceStep, func(t *testing.T) {
			var body loomapi.ApplyExplorerBuilderCommandsJSONRequestBody
			raw := fmt.Sprintf(`{"commandId":"source-edit","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":%s}]}`, ws.CurrentSemanticsVersion, sourceStep)
			if err := json.Unmarshal([]byte(raw), &body); err != nil {
				return
			}
			request, err := directAuthoringJSON[authoringv2.ApplyCommandsRequest](body)
			if err == nil {
				err = request.Validate()
			}
			if err == nil {
				t.Fatal("public transport accepted incomplete or injected source settings")
			}
		})
	}
}

func TestAuthoringErrorForOpenAPIMapsConstructionCategoryScanFailures(t *testing.T) {
	privateCause := errors.New("private query bind value")
	tests := []struct {
		name         string
		err          error
		wantStatus   int
		wantCode     string
		wantMsg      string
		wantLog      bool
		wantLogCause string
	}{
		{
			name: "compiler refusal",
			err: &ports.Error{
				Class: ports.ClassUnprocessable, Stage: "construction-category-discovery",
				Code: string(compiler.CategoryScanStageUnknown), Message: "complete compiler-owned pivot categories are unavailable for this stage and pair",
				Cause: privateCause,
			},
			wantStatus: 422, wantCode: string(compiler.CategoryScanStageUnknown),
			wantMsg: "complete compiler-owned pivot categories are unavailable for this stage and pair",
		},
		{
			name: "deadline",
			err: &ports.Error{
				Class: ports.ClassUnavailable, Stage: "construction-category-discovery",
				Code: "CATEGORY_SCAN_TIMEOUT", Message: "finding all category values exceeded the preview time limit; filter the source rows and try again",
				Cause: fmt.Errorf("execution deadline: %w", context.DeadlineExceeded),
			},
			wantStatus: 503, wantCode: "CATEGORY_SCAN_TIMEOUT",
			wantMsg: "finding all category values exceeded the preview time limit; filter the source rows and try again",
			wantLog: true, wantLogCause: context.DeadlineExceeded.Error(),
		},
		{
			name: "internal execution failure",
			err: &ports.Error{
				Class: ports.ClassInternal, Stage: "construction-category-discovery",
				Code: "CATEGORY_DISCOVERY_FAILED", Message: "category discovery failed unexpectedly while reading the catalog; inspect the server log for the request ID",
				Cause: fmt.Errorf("execute category query with private bind value: %w", privateCause),
			},
			wantStatus: 500, wantCode: "CATEGORY_DISCOVERY_FAILED",
			wantMsg: "category discovery failed unexpectedly while reading the catalog; inspect the server log for the request ID",
			wantLog: true, wantLogCause: "private bind value",
		},
	}

	const requestID = "request-category-discovery-1"
	logs := &openAPIErrorLogCapture{}
	previous := slog.Default()
	slog.SetDefault(slog.New(logs))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := httpctx.ContextWithRequestID(context.Background(), requestID)
			status, response := authoringErrorForOpenAPI(ctx, "discoverExplorerConstructionCategories", test.err)
			if status != test.wantStatus {
				t.Fatalf("authoringErrorForOpenAPI status = %d, want %d", status, test.wantStatus)
			}
			if response.Error.Code != test.wantCode || response.Error.Message != test.wantMsg {
				t.Fatalf("authoringErrorForOpenAPI body = %#v, want code %q and safe message %q", response.Error, test.wantCode, test.wantMsg)
			}
			if response.Error.RequestId == nil || *response.Error.RequestId != requestID {
				t.Fatalf("response requestId = %v, want %q", response.Error.RequestId, requestID)
			}
			wire, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(wire), privateCause.Error()) || strings.Contains(string(wire), "private bind value") {
				t.Fatalf("public error response leaked cause: %s", wire)
			}
			if test.wantLog && !logs.hasRequestID(requestID) {
				t.Fatalf("captured authoring error log does not contain request_id %q", requestID)
			}
			if test.wantLogCause != "" && !logs.hasCause(test.wantLogCause) {
				t.Fatalf("captured authoring error log does not retain cause %q", test.wantLogCause)
			}
		})
	}
}

type openAPIErrorLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *openAPIErrorLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *openAPIErrorLogCapture) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, record.Clone())
	return nil
}

func (h *openAPIErrorLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *openAPIErrorLogCapture) WithGroup(string) slog.Handler { return h }

func (h *openAPIErrorLogCapture) hasRequestID(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		found := false
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "request_id" && attr.Value.String() == want {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func (h *openAPIErrorLogCapture) hasCause(want string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, record := range h.records {
		found := false
		record.Attrs(func(attr slog.Attr) bool {
			if attr.Key == "cause" && strings.Contains(fmt.Sprint(attr.Value.Any()), want) {
				found = true
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func generatedInterpretationReadTestApp(readErr error) *fiber.App {
	app := fiber.New()
	routes := &HTTPRoutes{explorer: &Handlers{
		authorizer:    authscope.AllowAllAuthorizer{},
		authorizeRead: func(context.Context, *authscope.Principal, string) error { return readErr },
		application:   &lifecycle.Service{},
	}}
	registerGeneratedInterpretationRoutes(app, routes.explorer)
	return app
}

func registerGeneratedInterpretationRoutes(app *fiber.App, handlers *Handlers) {

	loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(strictExplorerHandlers(handlers), nil), loomapi.FiberServerOptions{})
}

func TestInterpretationReadRoutesEnforceProjectReadAuthorization(t *testing.T) {
	app := generatedInterpretationReadTestApp(errors.New("read denied"))
	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "libraries", method: http.MethodGet, path: "/api/v1/projects/project/interpretation-libraries"},
		{name: "revision", method: http.MethodGet, path: "/api/v1/projects/project/interpretation-revisions/revision-1"},
		{name: "preview", method: http.MethodPost, path: "/api/v1/projects/project/explorers/explorer/authoring/v2/interpretation-preview", body: `{"snapshotToken":"token","expectedDraftVersion":1,"expectedDraftDigest":"digest","outputId":"patients","column":"code","revisionId":"revision-1"}`},
		{name: "configured column context", method: http.MethodPost, path: "/api/v1/projects/project/explorers/explorer/authoring/v2/configured-column-context", body: `{"snapshotToken":"token","expectedDraftVersion":1,"expectedDraftDigest":"digest"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			response, err := app.Test(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusForbidden)
			}
		})
	}
}

func assertAuthoringRouteRejectsMissingBody(t *testing.T, route string) {
	t.Helper()
	app := generatedInterpretationReadTestApp(nil)
	request := httptest.NewRequest(http.MethodPost, route, strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d, want %d", response.StatusCode, http.StatusBadRequest)
	}
}

func TestInterpretationPreviewRouteRejectsMissingBody(t *testing.T) {
	assertAuthoringRouteRejectsMissingBody(t, "/api/v1/projects/project/explorers/explorer/authoring/v2/interpretation-preview")
}

func TestConfiguredColumnContextRouteRejectsMissingBody(t *testing.T) {
	assertAuthoringRouteRejectsMissingBody(t, "/api/v1/projects/project/explorers/explorer/authoring/v2/configured-column-context")
}

func TestConfiguredColumnContextResponseEncodesEmptyReadyListsAsArrays(t *testing.T) {
	response, err := configuredColumnContextResponse(lifecycle.ConfiguredColumnContextResult{
		SnapshotToken:   "snapshot",
		DraftVersion:    1,
		DraftDigest:     "digest",
		Libraries:       []interpretation.InterpretationLibrarySummary{},
		PinnedRevisions: []interpretation.InterpretationRevisionSummary{},
		Columns: []lifecycle.ConfiguredColumnContext{{
			OutputID:     "patients",
			Column:       "patient_id",
			OccurrenceID: "base",
			Resolution: lifecycle.ConfiguredColumnReady{
				CapabilityCandidateIDs: []string{"candidate-id"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"applicableRevisionIds":[]`) {
		t.Fatalf("configured column context JSON encoded an empty ready list as null: %s", payload)
	}
}

func TestConstructionInputNamesReportsTheMissingColumn(t *testing.T) {
	_, err := receiptoutput.ConstructionInputNames(map[string]string{"present": "name"}, []string{"present", "missing"})
	var missing *receiptoutput.ConstructionInputMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want receiptoutput.ConstructionInputMissingError", err)
	}
	if missing.ColumnID != "missing" {
		t.Fatalf("column ID = %q, want missing", missing.ColumnID)
	}
}

func TestReceiptContractFailureIsDescriptive(t *testing.T) {
	const columnID = "source_child_v1_missing"
	err := receiptapi.ContractFailure(fmt.Errorf("reconcile output metadata: %w", &receiptoutput.ConstructionInputMissingError{ColumnID: columnID, StepID: "pivot_1", OutputName: "missing_value"}))
	status, response := authoringErrorForOpenAPI(context.Background(), "proposeExplorerConstruction", err)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "CONSTRUCTION_RECEIPT_INPUT_MISSING" {
		t.Fatalf("code = %q, want CONSTRUCTION_RECEIPT_INPUT_MISSING", response.Error.Code)
	}
	if !strings.Contains(response.Error.Message, columnID) || !strings.Contains(response.Error.Message, "pivot_1") || !strings.Contains(response.Error.Message, "missing_value") {
		t.Fatalf("message = %q, want step, output, and input column", response.Error.Message)
	}
	if response.Error.Diagnostic == nil || response.Error.Diagnostic.Details == nil {
		t.Fatalf("diagnostic details missing: %#v", response.Error)
	}
	details := *response.Error.Diagnostic.Details
	if details["inputColumnId"] != columnID || details["stepId"] != "pivot_1" || details["outputName"] != "missing_value" {
		t.Fatalf("details = %#v, want step/output/inputColumnId", details)
	}
	if response.Error.Diagnostic.Stage != "reconcile_receipt" {
		t.Fatalf("stage = %q, want reconcile_receipt", response.Error.Diagnostic.Stage)
	}
}

func TestReceiptContractFailureDoesNotLeakPrivateCause(t *testing.T) {
	private := errors.New("private compiler memory address")
	err := receiptapi.ContractFailure(fmt.Errorf("wrapped: %w", private))
	_, response := authoringErrorForOpenAPI(context.Background(), "proposeExplorerConstruction", err)
	wire, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(wire), private.Error()) {
		t.Fatalf("response leaked private cause: %s", wire)
	}
	if response.Error.Code != "CONSTRUCTION_RECEIPT_CONTRACT_INVALID" {
		t.Fatalf("code = %q, want CONSTRUCTION_RECEIPT_CONTRACT_INVALID", response.Error.Code)
	}
	if response.Error.Diagnostic == nil || response.Error.Diagnostic.Details == nil {
		t.Fatalf("diagnostic details missing: %#v", response.Error)
	}
	if got := (*response.Error.Diagnostic.Details)["causeType"]; got == nil {
		t.Fatalf("cause type missing from diagnostic details: %#v", response.Error.Diagnostic.Details)
	}
}

func TestAuthoringErrorForOpenAPIUnknownErrorNamesTheOperation(t *testing.T) {
	private := errors.New("private socket detail")
	status, response := authoringErrorForOpenAPI(context.Background(), "proposeExplorerConstruction", private)
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", status, http.StatusInternalServerError)
	}
	if response.Error.Code != "INTERNAL_ERROR" {
		t.Fatalf("code = %q, want INTERNAL_ERROR", response.Error.Code)
	}
	if !strings.Contains(response.Error.Message, "proposeExplorerConstruction") {
		t.Fatalf("message = %q, want operation name", response.Error.Message)
	}
	if response.Error.Diagnostic == nil || response.Error.Diagnostic.Details == nil {
		t.Fatalf("diagnostic details missing: %#v", response.Error)
	}
	details := *response.Error.Diagnostic.Details
	if details["operation"] != "proposeExplorerConstruction" || details["causeType"] == nil {
		t.Fatalf("details = %#v, want operation and causeType", details)
	}
	wire, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(wire), private.Error()) {
		t.Fatalf("response leaked private cause: %s", wire)
	}
}

func TestFieldPresentationReadySharedFixtureMatchesGeneratedWireTypes(t *testing.T) {
	fixturePath := testkit.FixturePath(t, "semantic-field-presentation-ready.json")
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	response, err := directAuthoringJSON[loomapi.SemanticInventoryBrowseResponse](json.RawMessage(raw))
	if err != nil {
		t.Fatalf("decode shared field-presentation fixture: %v", err)
	}
	if response.FieldPresentation == nil {
		t.Fatal("shared fixture omitted fieldPresentation")
	}
	ready, err := response.FieldPresentation.AsFieldPresentationReady()
	if err != nil || ready.Kind != loomapi.FieldPresentationReadyKindREADY {
		t.Fatalf("fieldPresentation=%#v err=%v", ready, err)
	}
	if ready.Context.Project != "project-1" || ready.Context.SnapshotToken != "snapshot-1" ||
		ready.Context.InventoryBuildId != response.BuildId || ready.Context.OutputId != "specimens" ||
		ready.Context.DraftVersion != 3 || ready.Context.DraftDigest != "draft-digest-3" ||
		ready.Context.SourceNodeId != "specimen-node" || ready.Context.OccurrenceId != "base" ||
		len(ready.Context.Route) != 0 || ready.Context.PresentationRevision != "semantic-field-presentation/v1" ||
		ready.Context.CandidateBatchDigest == "" {
		t.Fatalf("field presentation context=%#v", ready.Context)
	}
	if len(ready.Decisions) != 1 || len(ready.Replacements) != 1 {
		t.Fatalf("field presentation decisions=%#v replacements=%#v", ready.Decisions, ready.Replacements)
	}
	group, err := ready.Decisions[0].AsFieldPresentationGroupUnderSemantic()
	if err != nil || group.Kind != "GROUP_UNDER_SEMANTIC" ||
		group.CandidateId != "raw-code" || group.Access != "SOURCE_FIELDS" ||
		len(group.ReplacementIds) != 1 {
		t.Fatalf("group decision=%#v err=%v", group, err)
	}
	exact, err := group.ReplacementIds[0].AsExactSemanticInventoryItemIdentity()
	if err != nil || exact.Kind != loomapi.ExactSemanticInventoryItemIdentityKindEXACTCONCEPT ||
		exact.ConceptId != "concept-1" || exact.BindingId != "binding-1" {
		t.Fatalf("replacement identity=%#v err=%v", exact, err)
	}
	choice := ready.Replacements[0].ConstructionChoice
	if choice == nil {
		t.Fatal("replacement is missing its current construction choice")
	}
	source, err := choice.Source.AsSemanticBindingChoiceSource()
	if err != nil || source.ConceptId != exact.ConceptId || source.BindingId != exact.BindingId ||
		source.NodeId != ready.Context.SourceNodeId || source.ResourceType != "Specimen" {
		t.Fatalf("replacement choice source=%#v err=%v", source, err)
	}

	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var expected, actual any
	if err := json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &actual); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("generated Go wire round-trip changed fixture\nwant: %#v\n got: %#v", expected, actual)
	}
}

func TestConstructionCapabilitiesHTTPPreservesOptionalNodeOccurrences(t *testing.T) {
	for _, test := range []struct {
		name        string
		occurrences map[string]string
	}{
		{name: "catalog nodes mapped to authored occurrences", occurrences: map[string]string{"n_patient": "root", "n_specimen": "specimen_subject"}},
		{name: "optional mapping omitted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			converted, err := directAuthoringJSON[loomapi.ConstructionCapabilitiesResponse](construction.ConstructionCapabilitiesResponse{NodeOccurrences: test.occurrences})
			if err != nil {
				t.Fatal(err)
			}
			response := loomapi.GetExplorerConstructionCapabilities200JSONResponse(converted)
			app := fiber.New()
			app.Get("/", func(ctx fiber.Ctx) error { return response.VisitGetExplorerConstructionCapabilitiesResponse(ctx) })
			wire, err := app.Test(httptest.NewRequest("GET", "/", nil))
			if err != nil {
				t.Fatal(err)
			}
			defer wire.Body.Close()
			if wire.StatusCode != 200 {
				t.Fatalf("status=%d", wire.StatusCode)
			}
			body, err := io.ReadAll(wire.Body)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatal(err)
			}
			raw, present := decoded["nodeOccurrences"]
			if test.occurrences == nil {
				if present {
					t.Fatalf("optional mapping emitted: %s", body)
				}
				return
			}
			if !present {
				t.Fatalf("typed HTTP conversion dropped populated nodeOccurrences: %s", body)
			}
			var got map[string]string
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.occurrences) {
				t.Fatalf("nodeOccurrences=%v want%v", got, test.occurrences)
			}
		})
	}
}
