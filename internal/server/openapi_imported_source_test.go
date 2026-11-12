package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/gofiber/fiber/v3"
)

func TestImportedSourceCommandSurvivesPublicTransport(t *testing.T) {
	var body loomapi.ApplyExplorerBuilderCommandsJSONRequestBody
	err := json.Unmarshal([]byte(`{"commandId":"source-edit","semanticsVersion":11,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"dynamicColumns[0]","enabled":false,"maxColumns":32,"valueMode":"ALL"}}]}`), &body)
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
	step := request.Commands[0].SourceStep
	if step == nil || step.Enabled || step.StepID != "dynamicColumns[0]" || step.MaxColumns == nil || *step.MaxColumns != 32 || step.ValueMode != "ALL" {
		t.Fatalf("source settings lost in public transport: %#v", step)
	}
	plan := &authoringv2.ImportedSourcePlan{Steps: []authoringv2.ImportedSourceStep{{ID: "fields", Kind: "FIELDS", Title: "Fields", Summary: "Source fields", Enabled: false, Editable: true}}}
	var public loomapi.Document
	public, err = directAuthoringJSON[loomapi.Document](authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RecordsRowDefinition(), Columns: []authoringv2.Column{}, ImportedRecipe: &authoringv2.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}, ImportedSource: plan})
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := directAuthoringJSON[authoringv2.Document](public)
	if err != nil {
		t.Fatal(err)
	}
	if roundtrip.ImportedSource == nil || len(roundtrip.ImportedSource.Steps) != 1 || roundtrip.ImportedSource.Steps[0].Enabled {
		t.Fatal("disabled source state lost in public response")
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

func TestImportedSourceHTTPRequiresExplicitEnabled(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		status        int
		enabled       bool
	}{
		{"missing", `{"stepId":"fields"}`, 400, false},
		{"null", `{"stepId":"fields","enabled":null}`, 400, false},
		{"injected", `{"stepId":"fields","enabled":false,"unexpected":"data"}`, 400, false},
		{"disabled", `{"stepId":"fields","enabled":false}`, 200, false},
		{"enabled", `{"stepId":"fields","enabled":true}`, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			capture := &sourceStepTransportCapture{}
			app := fiber.New()
			loomapi.RegisterHandlersWithOptions(app, loomapi.NewStrictHandler(capture, nil), loomapi.FiberServerOptions{})
			raw := fmt.Sprintf(`{"commandId":"edit-source","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":%s}]}`, authoringv2.CurrentSemanticsVersion, test.payload)
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
			} else if capture.request == nil || capture.request.Commands[0].SourceStep == nil || capture.request.Commands[0].SourceStep.Enabled != test.enabled {
				t.Fatal("valid source enablement changed in transport")
			}
		})
	}
}

func TestImportedSourceTransportRejectsIncompleteOrInjectedSettings(t *testing.T) {
	for _, sourceStep := range []string{
		`{"stepId":"fields"}`,
		`{"stepId":"fields","enabled":null}`,
		`{"stepId":"fields","enabled":false,"expr":{"select":"root.id"}}`,
	} {
		t.Run(sourceStep, func(t *testing.T) {
			var body loomapi.ApplyExplorerBuilderCommandsJSONRequestBody
			raw := fmt.Sprintf(`{"commandId":"source-edit","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":%s}]}`, authoringv2.CurrentSemanticsVersion, sourceStep)
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
