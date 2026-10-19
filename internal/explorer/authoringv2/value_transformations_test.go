package authoringv2

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/columntransform"
)

func exactCategoryTransformation() columntransform.ValueTransformation {
	return columntransform.ValueTransformation{
		Kind: columntransform.KindExactCategoryRecode,
		ExactCategoryRecode: &columntransform.ExactCategoryRecode{
			Mappings:      []columntransform.CategoryMapping{{From: "recorded-A", To: "group-1"}},
			UnknownPolicy: columntransform.UnknownKeepOriginal,
		},
	}
}

func TestUpdateColumnTransformationPreservesIdentityAndPersistsRoundTrip(t *testing.T) {
	catalog := commandCatalog()
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{workspaceDocument("patients")},
		Tabs:      []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	original := workspace.Documents[0].Columns[0]
	workspace, results, err := ApplyCommands(workspace, catalog, "recode-status", []Command{{
		Type: CommandUpdateColumnTransformation, OutputID: "patients", Column: original.Column,
		TransformationChange: &ColumnTransformationChange{Kind: "SET", Transformation: ptrTransformation(exactCategoryTransformation())},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Column != original.Column {
		t.Fatalf("command results = %#v, want stable column %q", results, original.Column)
	}
	updated := workspace.Documents[0].Columns[0]
	if updated.Column != original.Column || !reflect.DeepEqual(updated.Source, original.Source) {
		t.Fatalf("transformation changed column identity or source: before=%#v after=%#v", original, updated)
	}
	if updated.ValueTransformation == nil || !reflect.DeepEqual(*updated.ValueTransformation, exactCategoryTransformation()) {
		t.Fatalf("transformation was not saved: %#v", updated.ValueTransformation)
	}

	canonical, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := DecodeWorkspace(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Documents[0].Columns[0]; got.ValueTransformation == nil || !reflect.DeepEqual(*got.ValueTransformation, exactCategoryTransformation()) {
		t.Fatalf("persisted transformation did not round-trip: %#v", got.ValueTransformation)
	}

	reloaded, _, err = ApplyCommands(reloaded, catalog, "remove-recode", []Command{{
		Type: CommandUpdateColumnTransformation, OutputID: "patients", Column: original.Column,
		TransformationChange: &ColumnTransformationChange{Kind: "REMOVE"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Documents[0].Columns[0]; got.ValueTransformation != nil || got.Column != original.Column {
		t.Fatalf("REMOVE did not clear only the transformation: %#v", got)
	}
}

func TestUpdateColumnTransformationRejectsUnsupportedAndIncompleteMappings(t *testing.T) {
	workspace := Workspace{
		APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Patients"},
		Documents: []Document{workspaceDocument("patients")},
		Tabs:      []Tab{{ID: "patients", Title: "Patients", OutputID: "patients", Order: 0, Visible: true}},
	}
	catalog := commandCatalog()
	catalog.Candidates[0].LogicalType = "boolean"
	command := func(transformation columntransform.ValueTransformation) Command {
		return Command{
			Type: CommandUpdateColumnTransformation, OutputID: "patients", Column: "patient_id",
			TransformationChange: &ColumnTransformationChange{Kind: "SET", Transformation: ptrTransformation(transformation)},
		}
	}
	if _, _, err := ApplyCommands(workspace, catalog, "unsupported-type", []Command{command(exactCategoryTransformation())}); err == nil || !strings.Contains(err.Error(), "COLUMN_VALUE_TYPE_UNSUPPORTED") {
		t.Fatalf("unsupported source type error = %v, want COLUMN_VALUE_TYPE_UNSUPPORTED", err)
	}

	incomplete := exactCategoryTransformation()
	incomplete.ExactCategoryRecode.Mappings = nil
	if _, _, err := ApplyCommands(workspace, commandCatalog(), "incomplete-map", []Command{command(incomplete)}); err == nil || !strings.Contains(err.Error(), "mappings must not be empty") {
		t.Fatalf("incomplete map error = %v, want mapping diagnostic", err)
	}
}

func TestCodedValueTransformationRefusalPreservesCodingIdentity(t *testing.T) {
	document := workspaceDocument("patients")
	column := document.Columns[0]
	column.Source = ColumnSource{Kind: SourceCodedValue}
	err := validateColumnValueTransformationForCatalog(document, commandCatalog(), column, column.Source, exactCategoryTransformation())
	if err == nil || !strings.Contains(err.Error(), "CODED_VALUE_RECODE_UNAVAILABLE") || !strings.Contains(err.Error(), "Coding.system and Coding.code") {
		t.Fatalf("coded-value transformation error = %v, want exact identity-preservation refusal", err)
	}
}

func ptrTransformation(value columntransform.ValueTransformation) *columntransform.ValueTransformation {
	return &value
}
