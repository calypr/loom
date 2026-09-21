package authoringv2

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestTableShapeRoundTripsCanonicalWorkspace(t *testing.T) {
	workspace, err := DecodeWorkspace(tableShapeWorkspaceJSON(validPivotDerivedTableShapeJSON()))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := workspace.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	roundTripped, err := DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTripped.Documents[0].TableShape, workspace.Documents[0].TableShape) {
		t.Fatalf("round-tripped table shape differs:\nwant: %#v\n got: %#v", workspace.Documents[0].TableShape, roundTripped.Documents[0].TableShape)
	}
	roundTrippedRaw, err := roundTripped.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(roundTrippedRaw) != string(raw) {
		t.Fatalf("canonical table shape is not idempotent:\nfirst: %s\n next: %s", raw, roundTrippedRaw)
	}
	cloned, err := cloneWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cloned.Documents[0].TableShape, workspace.Documents[0].TableShape) {
		t.Fatalf("cloned table shape differs:\nwant: %#v\n got: %#v", workspace.Documents[0].TableShape, cloned.Documents[0].TableShape)
	}
}

func TestTableShapeRequiresCurrentSemanticsVersion(t *testing.T) {
	raw := tableShapeWorkspaceJSON(validPivotTableShapeJSON())
	legacy := strings.Replace(string(raw), fmt.Sprintf(`"semanticsVersion":%d`, CurrentSemanticsVersion), fmt.Sprintf(`"semanticsVersion":%d`, CurrentSemanticsVersion-1), 1)
	if _, err := DecodeWorkspace([]byte(legacy)); err == nil || !strings.Contains(err.Error(), "tableShape requires semanticsVersion") {
		t.Fatalf("DecodeWorkspace error = %v, want tableShape version rejection", err)
	}
}

func TestTableScalarRejectsNonFiniteProgrammaticDecimal(t *testing.T) {
	value := 0.0
	value /= value
	if err := (TableScalar{Kind: "DECIMAL", Decimal: &value}).Validate(); err == nil || !strings.Contains(err.Error(), "must be finite") {
		t.Fatalf("Validate error = %v, want finite-decimal rejection", err)
	}
}

func TestDecodeWorkspaceRejectsMalformedTableShapeUnions(t *testing.T) {
	tests := []struct {
		name  string
		shape string
		want  string
	}{
		{
			name:  "reshape payload does not match kind",
			shape: `{"reshape":{"kind":"PIVOT","unpivot":{}},"derived":[]}`,
			want:  "reshape must contain exactly one payload matching kind",
		},
		{
			name:  "operand payload does not match kind",
			shape: strings.Replace(validPivotDerivedTableShapeJSON(), `"kind":"COLUMN","column":"heart_rate"`, `"kind":"COLUMN","literal":{"kind":"INTEGER","integer":2}`, 1),
			want:  "operand must contain exactly one payload matching kind",
		},
		{
			name:  "unknown reshape field",
			shape: `{"reshape":{"kind":"PIVOT","pivot":{},"privateSelector":"value"},"derived":[]}`,
			want:  `unknown field "privateSelector"`,
		},
		{
			name:  "unknown scalar field",
			shape: strings.Replace(validPivotTableShapeJSON(), `"string":"heart_rate"`, `"string":"heart_rate","integer":2`, 1),
			want:  `unknown field "integer"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeWorkspace(tableShapeWorkspaceJSON(test.shape))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeWorkspace error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodeWorkspaceRejectsInvalidTableShapeReferencesAndPolicies(t *testing.T) {
	valid := validPivotDerivedTableShapeJSON()
	tests := []struct {
		name  string
		shape string
		want  string
	}{
		{
			name:  "unknown derived input",
			shape: strings.Replace(valid, `"column":"heart_rate"`, `"column":"missing"`, 1),
			want:  "unknown column reference",
		},
		{
			name:  "missing explicit pivot policy",
			shape: strings.Replace(validPivotTableShapeJSON(), `"duplicatePolicy":"ERROR",`, "", 1),
			want:  "duplicatePolicy must be ERROR, SUM, MIN, or MAX",
		},
		{
			name:  "duplicate typed pivot keys",
			shape: `{"reshape":{"kind":"PIVOT","pivot":{"constructionId":"pivot_1","groupKeys":["group"],"categoryColumn":"category","valueColumn":"measure","categories":[{"key":{"kind":"STRING","string":"heart_rate"},"output":{"column":"heart_rate","label":"Heart rate"}},{"key":{"kind":"STRING","string":"heart_rate"},"output":{"column":"heart_rate_2","label":"Heart rate 2"}}],"duplicatePolicy":"ERROR","missingCellPolicy":"NULL","unlistedCategoryPolicy":"ERROR"}}}`,
			want:  "duplicate pivot category key",
		},
		{
			name:  "duplicate construction IDs",
			shape: strings.Replace(valid, `"constructionId":"derived_1"`, `"constructionId":"pivot_1"`, 1),
			want:  "duplicate constructionId",
		},
		{
			name:  "dependency cycle",
			shape: `{"reshape":{"kind":"PIVOT","pivot":{"constructionId":"pivot_1","groupKeys":["group"],"categoryColumn":"category","valueColumn":"measure","categories":[{"key":{"kind":"STRING","string":"heart_rate"},"output":{"column":"heart_rate","label":"Heart rate"}}],"duplicatePolicy":"ERROR","missingCellPolicy":"NULL","unlistedCategoryPolicy":"ERROR"}},"derived":[{"constructionId":"derived_1","output":{"column":"calc_a","label":"A"},"operation":"ADD","left":{"kind":"COLUMN","column":"calc_b"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":1}},"missingInputPolicy":"PROPAGATE_NULL"},{"constructionId":"derived_2","output":{"column":"calc_b","label":"B"},"operation":"ADD","left":{"kind":"COLUMN","column":"calc_a"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":1}},"missingInputPolicy":"PROPAGATE_NULL"}]}`,
			want:  "dependency cycle",
		},
		{
			name:  "unpivot with derived definitions",
			shape: `{"reshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"unpivot_1","inputs":[{"column":"category","key":{"kind":"STRING","string":"category"}},{"column":"measure","key":{"kind":"STRING","string":"measure"}}],"keyOutput":{"column":"metric_name","label":"Metric"},"valueOutput":{"column":"metric_value","label":"Value"},"nullRowPolicy":"DROP"}},"derived":[{"constructionId":"derived_1","output":{"column":"copy_value","label":"Copy"},"operation":"ADD","left":{"kind":"COLUMN","column":"metric_value"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":1}},"missingInputPolicy":"PROPAGATE_NULL"}]}`,
			want:  "cannot combine UNPIVOT with derived definitions",
		},
		{
			name:  "DIVIDE requires a zero policy",
			shape: strings.Replace(valid, `"operation":"MULTIPLY"`, `"operation":"DIVIDE"`, 1),
			want:  "DIVIDE requires divisionByZeroPolicy",
		},
		{
			name:  "non-DIVIDE rejects a zero policy",
			shape: strings.Replace(valid, `"missingInputPolicy":"PROPAGATE_NULL"`, `"missingInputPolicy":"PROPAGATE_NULL","divisionByZeroPolicy":"NULL"`, 1),
			want:  "divisionByZeroPolicy is only valid for DIVIDE",
		},
		{
			name:  "unsafe public output name",
			shape: strings.Replace(valid, `"column":"double_rate"`, `"column":"double-rate"`, 1),
			want:  "not a valid public column name",
		},
		{
			name:  "constructed output collides with base column",
			shape: strings.Replace(validPivotTableShapeJSON(), `"column":"heart_rate","label":"Heart rate"`, `"column":"group","label":"Heart rate"`, 1),
			want:  "duplicate public column name",
		},
		{
			name:  "duplicate group key reference",
			shape: strings.Replace(validPivotTableShapeJSON(), `"groupKeys":["group"]`, `"groupKeys":["group","group"]`, 1),
			want:  "groupKeys contains duplicate column",
		},
		{
			name:  "unknown group key reference",
			shape: strings.Replace(validPivotTableShapeJSON(), `"groupKeys":["group"]`, `"groupKeys":["unknown"]`, 1),
			want:  "unknown group key column",
		},
		{
			name:  "empty unpivot inputs",
			shape: `{"reshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"unpivot_1","inputs":[],"keyOutput":{"column":"metric_name","label":"Metric"},"valueOutput":{"column":"metric_value","label":"Value"},"nullRowPolicy":"DROP"}}}`,
			want:  "inputs must be non-empty",
		},
		{
			name:  "duplicate unpivot input reference",
			shape: `{"reshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"unpivot_1","inputs":[{"column":"category","key":{"kind":"STRING","string":"category"}},{"column":"category","key":{"kind":"STRING","string":"measure"}}],"keyOutput":{"column":"metric_name","label":"Metric"},"valueOutput":{"column":"metric_value","label":"Value"},"nullRowPolicy":"DROP"}}}`,
			want:  "inputs contains duplicate column",
		},
		{
			name:  "unknown unpivot input reference",
			shape: `{"reshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"unpivot_1","inputs":[{"column":"missing","key":{"kind":"STRING","string":"missing"}}],"keyOutput":{"column":"metric_name","label":"Metric"},"valueOutput":{"column":"metric_value","label":"Value"},"nullRowPolicy":"DROP"}}}`,
			want:  "unknown unpivot input column",
		},
		{
			name:  "duplicate unpivot typed keys",
			shape: `{"reshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"unpivot_1","inputs":[{"column":"category","key":{"kind":"STRING","string":"same"}},{"column":"measure","key":{"kind":"STRING","string":"same"}}],"keyOutput":{"column":"metric_name","label":"Metric"},"valueOutput":{"column":"metric_value","label":"Value"},"nullRowPolicy":"DROP"}}}`,
			want:  "duplicate unpivot input key",
		},
		{
			name:  "unpivot output collides with base column",
			shape: `{"reshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"unpivot_1","inputs":[{"column":"category","key":{"kind":"STRING","string":"category"}}],"keyOutput":{"column":"group","label":"Metric"},"valueOutput":{"column":"metric_value","label":"Value"},"nullRowPolicy":"DROP"}}}`,
			want:  "duplicate public column name",
		},
		{
			name:  "empty group key list",
			shape: strings.Replace(validPivotTableShapeJSON(), `"groupKeys":["group"]`, `"groupKeys":[]`, 1),
			want:  "groupKeys must be non-empty",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeWorkspace(tableShapeWorkspaceJSON(test.shape))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DecodeWorkspace error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDecodeWorkspaceBoundsTableShapeDefinitions(t *testing.T) {
	categories := make([]string, 257)
	for index := range categories {
		categories[index] = fmt.Sprintf(`{"key":{"kind":"STRING","string":"category_%d"},"output":{"column":"category_%d","label":"Category %d"}}`, index, index, index)
	}
	shape := `{"reshape":{"kind":"PIVOT","pivot":{"constructionId":"pivot_1","groupKeys":["group"],"categoryColumn":"category","valueColumn":"measure","categories":[` + strings.Join(categories, ",") + `],"duplicatePolicy":"ERROR","missingCellPolicy":"NULL","unlistedCategoryPolicy":"ERROR"}}}`
	if _, err := DecodeWorkspace(tableShapeWorkspaceJSON(shape)); err == nil || !strings.Contains(err.Error(), "categories exceed maximum") {
		t.Fatalf("oversized category list error = %v", err)
	}

	derived := make([]string, 65)
	for index := range derived {
		derived[index] = fmt.Sprintf(`{"constructionId":"derived_%d","output":{"column":"calc_%d","label":"Calculation %d"},"operation":"ADD","left":{"kind":"COLUMN","column":"measure"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":1}},"missingInputPolicy":"PROPAGATE_NULL"}`, index, index, index)
	}
	shape = `{"derived":[` + strings.Join(derived, ",") + `]}`
	if _, err := DecodeWorkspace(tableShapeWorkspaceJSON(shape)); err == nil || !strings.Contains(err.Error(), "derived definitions exceed maximum") {
		t.Fatalf("oversized derived list error = %v", err)
	}
}

func validPivotTableShapeJSON() string {
	return `{"reshape":{"kind":"PIVOT","pivot":{"constructionId":"pivot_1","groupKeys":["group"],"categoryColumn":"category","valueColumn":"measure","categories":[{"key":{"kind":"STRING","string":"heart_rate"},"output":{"column":"heart_rate","label":"Heart rate"}}],"duplicatePolicy":"ERROR","missingCellPolicy":"NULL","unlistedCategoryPolicy":"ERROR"}}}`
}

func validPivotDerivedTableShapeJSON() string {
	return `{"reshape":{"kind":"PIVOT","pivot":{"constructionId":"pivot_1","groupKeys":["group"],"categoryColumn":"category","valueColumn":"measure","categories":[{"key":{"kind":"STRING","string":"heart_rate"},"output":{"column":"heart_rate","label":"Heart rate"}}],"duplicatePolicy":"ERROR","missingCellPolicy":"NULL","unlistedCategoryPolicy":"ERROR"}},"derived":[{"constructionId":"derived_1","output":{"column":"double_rate","label":"Double rate"},"operation":"MULTIPLY","left":{"kind":"COLUMN","column":"heart_rate"},"right":{"kind":"LITERAL","literal":{"kind":"INTEGER","integer":2}},"missingInputPolicy":"PROPAGATE_NULL"}]}`
}

func tableShapeWorkspaceJSON(shape string) []byte {
	return []byte(fmt.Sprintf(`{"apiVersion":%q,"kind":%q,"semanticsVersion":%d,"explorer":{"title":"Table shape"},"documents":[{"kind":%q,"output":{"id":"observations","title":"Observations"},"rootResourceType":"Observation","route":{"occurrenceId":"base","resourceType":"Observation"},"rows":{"kind":"RECORDS","records":{}},"columns":[{"column":"group","label":"Group","logicalType":"string","occurrenceId":"base","source":{"kind":"projectId"}},{"column":"category","label":"Category","logicalType":"string","occurrenceId":"base","source":{"kind":"projectId"}},{"column":"measure","label":"Measure","logicalType":"decimal","occurrenceId":"base","source":{"kind":"projectId"}}],"tableShape":%s}],"tabs":[{"id":"observations","title":"Observations","outputId":"observations","order":0,"visible":true}]}`, APIVersion, WorkspaceKind, CurrentSemanticsVersion, Kind, shape))
}
