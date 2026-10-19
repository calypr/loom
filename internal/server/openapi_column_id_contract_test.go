package server

import (
	"encoding/json"
	"testing"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
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
	stableState, err := directAuthoringJSON[loomapi.BuilderState](authoringv2.BuilderState{
		APIVersion: authoringv2.APIVersion,
		Kind:       authoringv2.StateKind,
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
	legacyState, err := directAuthoringJSON[loomapi.BuilderState](authoringv2.BuilderState{
		APIVersion: authoringv2.APIVersion,
		Kind:       authoringv2.StateKind,
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
	column := authoringv2.Column{
		Column: "condition_category", Label: "Condition category", OccurrenceID: authoringv2.RootOccurrenceID,
		Source: authoringv2.ColumnSource{Kind: authoringv2.SourceCodedValue, Lookup: &authoringv2.LookupSource{
			Binding: &fhirschema.CorrelatedBinding{
				KeyPath: "code.coding[]", ValueScope: fhirschema.CorrelatedValueScopeCoding,
				SelectionScope: fhirschema.CorrelatedSelectionScopeSystemVersion, VersionPath: "version",
				SystemPath: "system", CodePath: "code", ValuePath: "display", LogicalType: "string",
				ValueFallback: []string{"code"}, FallbackOnBlankString: true,
			},
			Key: &fhirschema.CorrelatedKey{System: "https://example.org/condition", Code: "", Version: ""},
		}},
	}
	document := authoringv2.Document{
		Rows: authoringv2.RecordsRowDefinition(), Kind: authoringv2.Kind,
		Output: authoringv2.Output{ID: "conditions", Title: "Conditions"}, RootResourceType: "Condition",
		Route:   authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: "Condition"},
		Columns: []authoringv2.Column{column},
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
	workspace := authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion, Kind: authoringv2.WorkspaceKind,
		Explorer: authoringv2.ExplorerMetadata{Title: "Builder"}, Documents: []authoringv2.Document{document},
		Tabs: []authoringv2.Tab{{ID: "conditions_tab", Title: "Conditions", OutputID: "conditions", Order: 0, Visible: true}},
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
	document.Columns = []authoringv2.Column{exact}
	if err := document.Validate(); err == nil {
		t.Fatal("exact coded-value key with an empty code passed authoring validation")
	}
}

func columnIDContractWorkspace(columnID string) authoringv2.Workspace {
	return authoringv2.Workspace{
		APIVersion: authoringv2.APIVersion,
		Kind:       authoringv2.WorkspaceKind,
		Documents: []authoringv2.Document{{
			Kind:             authoringv2.Kind,
			Output:           authoringv2.Output{ID: "patients", Title: "Patients"},
			RootResourceType: "Patient",
			Columns: []authoringv2.Column{{
				ColumnID:     columnID,
				Column:       "member_id",
				Label:        "Member",
				OccurrenceID: authoringv2.RootOccurrenceID,
				Source: authoringv2.ColumnSource{
					Kind:  authoringv2.SourceField,
					Field: &authoringv2.FieldSource{Path: "id"},
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
