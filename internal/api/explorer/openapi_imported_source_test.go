package explorerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/gofiber/fiber/v3"
)

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
	err := json.Unmarshal([]byte(fmt.Sprintf(`{"commandId":"source-edit","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"dynamicColumns[0]","maxColumns":32,"valueMode":"ALL"}}]}`, authoringv2.CurrentSemanticsVersion)), &body)
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
	if step == nil || step.Remove || step.StepID != "dynamicColumns[0]" || step.MaxColumns == nil || *step.MaxColumns != 32 || step.ValueMode != "ALL" {
		t.Fatalf("source settings lost in public transport: %#v", step)
	}
	removeBody := fmt.Sprintf(`{"commandId":"source-remove","semanticsVersion":%d,"snapshotToken":"snapshot","expectedDraftVersion":1,"expectedDraftDigest":"digest","commands":[{"type":"UPDATE_IMPORTED_SOURCE_STEP","outputId":"patients","sourceStep":{"stepId":"dynamicColumns[0]","remove":true}}]}`, authoringv2.CurrentSemanticsVersion)
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
	if step := request.Commands[0].SourceStep; step == nil || !step.Remove {
		t.Fatalf("source removal lost in public transport: %#v", step)
	}
	plan := &authoringv2.ImportedSourcePlan{RemovedStepIDs: []string{"fields"}}
	var public loomapi.Document
	public, err = directAuthoringJSON[loomapi.Document](authoringv2.Document{Kind: authoringv2.Kind, Output: authoringv2.Output{ID: "patients", Title: "Patients"}, RootResourceType: "Patient", Route: authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Patient"}, Rows: authoringv2.RecordsRowDefinition(), Columns: []authoringv2.Column{}, ImportedRecipe: &authoringv2.ImportedRecipeRef{Name: "default", TranslationVersion: "v1", Digest: strings.Repeat("a", 64), Output: "patients"}, ImportedSource: plan})
	if err != nil {
		t.Fatal(err)
	}
	roundtrip, err := directAuthoringJSON[authoringv2.Document](public)
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
			} else if capture.request == nil || capture.request.Commands[0].SourceStep == nil || capture.request.Commands[0].SourceStep.Remove != test.remove {
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
