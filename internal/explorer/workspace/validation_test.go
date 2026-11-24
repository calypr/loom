package workspace

import (
	"strings"
	"testing"
)

func TestConstructionUnpivotRejectsMixedKeyScalarKinds(t *testing.T) {
	document := stagedConstructionDocument()
	integer := int64(1)
	document.Construction.Steps[3].Operation.Unpivot.Inputs[1].Key = TableScalar{Kind: TableScalarInteger, Integer: &integer}

	err := document.Validate()
	if err == nil || !strings.Contains(err.Error(), "unpivot input keys must have one canonical scalar type") {
		t.Fatalf("mixed unpivot key kinds error = %v", err)
	}
}

func TestConstructionUnpivotRejectsIncompatibleValueTypes(t *testing.T) {
	document := stagedConstructionDocument()
	document.Construction.Steps[2].Outputs[2].Type = "string"

	err := document.Validate()
	if err == nil || !strings.Contains(err.Error(), "incompatible with previous inputs") {
		t.Fatalf("incompatible unpivot value types error = %v", err)
	}
}

func TestConstructionUnpivotAcceptsIntegerDecimalValueTypes(t *testing.T) {
	document := stagedConstructionDocument()
	document.Construction.Steps[2].Outputs[2].Type = "decimal"

	if err := document.Validate(); err != nil {
		t.Fatalf("integer and decimal unpivot inputs rejected: %v", err)
	}
}

func TestConstructionUnpivotCanonicalizesValueTypeAliases(t *testing.T) {
	for _, test := range []struct {
		logicalType string
		wantError   bool
	}{
		{logicalType: "number"},
		{logicalType: "date-time", wantError: true},
		{logicalType: "datetime", wantError: true},
	} {
		t.Run(test.logicalType, func(t *testing.T) {
			document := stagedConstructionDocument()
			document.Construction.Steps[2].Outputs[2].Type = test.logicalType
			err := document.Validate()
			if test.wantError {
				if err == nil || !strings.Contains(err.Error(), "incompatible with previous inputs") {
					t.Fatalf("unpivot %s value type error = %v, want incompatibility rejection", test.logicalType, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unpivot %s value type rejected: %v", test.logicalType, err)
			}
		})
	}
}

func TestConstructionUnpivotValueKindHandlesConstructionLogicalTypes(t *testing.T) {
	for _, test := range []struct {
		logicalType string
		want        string
	}{
		{logicalType: "string", want: "STRING"},
		{logicalType: "code", want: "STRING"},
		{logicalType: "uuid", want: "STRING"},
		{logicalType: "date", want: "STRING"},
		{logicalType: "date_time", want: "STRING"},
		{logicalType: "date-time", want: "STRING"},
		{logicalType: "datetime", want: "STRING"},
		{logicalType: "integer", want: "INTEGER"},
		{logicalType: "decimal", want: "DECIMAL"},
		{logicalType: "number", want: "DECIMAL"},
		{logicalType: "boolean", want: "BOOLEAN"},
	} {
		got, ok := constructionUnpivotValueKind(test.logicalType)
		if !ok || got != test.want {
			t.Errorf("constructionUnpivotValueKind(%q) = %q, %t; want %q, true", test.logicalType, got, ok, test.want)
		}
	}
	if _, ok := constructionUnpivotValueKind("object"); ok {
		t.Error("constructionUnpivotValueKind(object) accepted a non-scalar construction logical type")
	}
}

func TestConstructionCodedPivotRejectsDeclaredOutputTypeMismatch(t *testing.T) {
	document := codedGroupDocument()
	document.Construction.Steps[0].Outputs[0].Type = "integer"

	err := document.Validate()
	if err == nil || !strings.Contains(err.Error(), "does not match source type") {
		t.Fatalf("coded pivot output type mismatch error = %v", err)
	}
}

func TestConstructionCodedPivotAcceptsInferredOrMatchingOutputTypes(t *testing.T) {
	for _, declaredType := range []string{"", "INFER", "infer", "string"} {
		document := codedGroupDocument()
		document.Construction.Steps[0].Outputs[0].Type = declaredType
		if err := document.Validate(); err != nil {
			t.Errorf("coded pivot output type %q rejected: %v", declaredType, err)
		}
	}
}

func TestConstructionCodedPivotRejectsArrayOutputTypeCaseInsensitively(t *testing.T) {
	for _, declaredType := range []string{"array", "ARRAY"} {
		document := codedGroupDocument()
		document.Construction.Steps[0].Outputs[0].Type = declaredType
		err := document.Validate()
		if err == nil || !strings.Contains(err.Error(), "does not match source type") {
			t.Errorf("coded pivot output type %q error = %v, want source type mismatch", declaredType, err)
		}
	}
}

func TestConstructionCodedPivotSumRequiresNumericSourceLogicalType(t *testing.T) {
	document := codedGroupDocument()
	pivot := document.Construction.Steps[0].Operation.CodedPivot
	pivot.DuplicatePolicy = ConstructionPivotDuplicateSum

	err := document.Validate()
	if err == nil || !strings.Contains(err.Error(), "SUM requires an INTEGER or DECIMAL source logical type") {
		t.Fatalf("coded pivot SUM with string source error = %v, want numeric source rejection", err)
	}
}

func TestConstructionCodedPivotRejectsUnsupportedSourceLogicalType(t *testing.T) {
	document := codedGroupDocument()
	document.Construction.Steps[0].Operation.CodedPivot.Source.Family.LogicalType = "money"

	err := document.Validate()
	if err == nil || !strings.Contains(err.Error(), "not a supported table scalar") {
		t.Fatalf("coded pivot unsupported source type error = %v", err)
	}
}

func TestConstructionCodedPivotAcceptsCanonicalizableSourceLogicalTypes(t *testing.T) {
	for _, test := range []struct{ sourceType, outputType string }{
		{sourceType: "date_time", outputType: "date_time"},
		{sourceType: "date-time", outputType: "datetime"},
		{sourceType: "number", outputType: "DECIMAL"},
	} {
		document := codedGroupDocument()
		document.Construction.Steps[0].Operation.CodedPivot.Source.Family.LogicalType = test.sourceType
		document.Construction.Steps[0].Outputs[0].Type = test.outputType
		if err := document.Validate(); err != nil {
			t.Errorf("coded pivot source type %q output %q rejected: %v", test.sourceType, test.outputType, err)
		}
	}
}

func workspaceOutputCombineDocument(id string, workspaceOutputID string) Document {
	document := terminalCombineDocument()
	document.Output.ID = id
	document.Output.Title = id
	document.Construction.Steps[0].Inputs[0] = ConstructionInputRef{Kind: ConstructionInputWorkspaceOutput, OutputID: workspaceOutputID}
	return document
}

func dependencyWorkspace(documents ...Document) Workspace {
	w := Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, Explorer: ExplorerMetadata{Title: "Dependencies"}, Documents: documents}
	for index, document := range documents {
		w.Tabs = append(w.Tabs, Tab{ID: "tab_" + document.Output.ID, Title: document.Output.Title, OutputID: document.Output.ID, Order: index, Visible: true})
	}
	return w
}

func TestWorkspaceOutputDependenciesResolveForwardReferencesInStableOrder(t *testing.T) {
	w := dependencyWorkspace(workspaceOutputCombineDocument("combined", "grouped"), workspaceDocument("grouped"))
	if err := w.Validate(); err != nil {
		t.Fatalf("workspace with exact sibling output rejected: %v", err)
	}
	order, err := w.OutputDependencyOrder()
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 0 {
		t.Fatalf("dependency order = %v, want [1 0]", order)
	}
}

func TestWorkspaceOutputDependenciesRejectMissingSelfAndCycles(t *testing.T) {
	tests := []struct {
		name string
		w    Workspace
		want string
	}{
		{name: "missing", w: dependencyWorkspace(workspaceOutputCombineDocument("combined", "unknown"), workspaceDocument("base")), want: "WORKSPACE_OUTPUT_NOT_FOUND"},
		{name: "self", w: dependencyWorkspace(workspaceOutputCombineDocument("combined", "combined"), workspaceDocument("base")), want: "WORKSPACE_OUTPUT_SELF_REFERENCE"},
		{name: "cycle", w: dependencyWorkspace(workspaceOutputCombineDocument("first", "second"), workspaceOutputCombineDocument("second", "first")), want: "WORKSPACE_OUTPUT_CYCLE"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.w.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %s", err, test.want)
			}
		})
	}
}
