package authoringv2

import (
	"encoding/json"
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

func TestTableShapeV8WorkspaceMigratesToCurrentSemantics(t *testing.T) {
	raw := tableShapeWorkspaceJSON(validPivotTableShapeJSON())
	legacy := strings.Replace(string(raw), fmt.Sprintf(`"semanticsVersion":%d`, CurrentSemanticsVersion), fmt.Sprintf(`"semanticsVersion":%d`, CurrentSemanticsVersion-1), 1)
	before, err := DecodeWorkspace(raw)
	if err != nil {
		t.Fatal(err)
	}
	after, err := DecodeWorkspace([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	if after.SemanticsVersion != CurrentSemanticsVersion {
		t.Fatalf("semanticsVersion = %d, want %d", after.SemanticsVersion, CurrentSemanticsVersion)
	}
	if !reflect.DeepEqual(after.Documents[0].TableShape, before.Documents[0].TableShape) || !reflect.DeepEqual(after.Documents[0].Rows, before.Documents[0].Rows) || !reflect.DeepEqual(after.Documents[0].Columns, before.Documents[0].Columns) {
		t.Fatalf("v8 migration changed tableShape, rows, or columns:\nbefore=%#v\nafter=%#v", before.Documents[0], after.Documents[0])
	}
}

func TestTableScalarRejectsNonFiniteProgrammaticDecimal(t *testing.T) {
	value := 0.0
	value /= value
	if err := (TableScalar{Kind: TableScalarDecimal, Decimal: &value}).ValidateStructure(); err == nil || !strings.Contains(err.Error(), "must be finite") {
		t.Fatalf("Validate error = %v, want finite-decimal rejection", err)
	}
}

func TestTableScalarRoundTripsZeroValuesAndSentinels(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		kind TableScalarKind
	}{
		{name: "empty string", raw: `{"kind":"STRING","string":""}`, kind: TableScalarString},
		{name: "zero integer", raw: `{"kind":"INTEGER","integer":0}`, kind: TableScalarInteger},
		{name: "zero decimal", raw: `{"kind":"DECIMAL","decimal":0.0}`, kind: TableScalarDecimal},
		{name: "false boolean", raw: `{"kind":"BOOLEAN","boolean":false}`, kind: TableScalarBoolean},
		{name: "null sentinel", raw: `{"kind":"NULL"}`, kind: TableScalarNull},
		{name: "missing sentinel", raw: `{"kind":"MISSING"}`, kind: TableScalarMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var scalar TableScalar
			if err := json.Unmarshal([]byte(test.raw), &scalar); err != nil {
				t.Fatal(err)
			}
			if scalar.Kind != test.kind {
				t.Fatalf("kind = %q, want %q", scalar.Kind, test.kind)
			}
			concreteErr := scalar.ValidateConcreteValue()
			if sentinel := test.kind == TableScalarNull || test.kind == TableScalarMissing; sentinel != (concreteErr != nil) {
				t.Fatalf("concrete validation error = %v for %q", concreteErr, test.kind)
			}
			encoded, err := json.Marshal(scalar)
			if err != nil {
				t.Fatal(err)
			}
			var roundTripped TableScalar
			if err := json.Unmarshal(encoded, &roundTripped); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTripped, scalar) {
				t.Fatalf("round trip = %#v, want %#v", roundTripped, scalar)
			}
		})
	}

	var emptyString TableScalar
	if err := json.Unmarshal([]byte(`{"kind":"STRING","string":""}`), &emptyString); err != nil || emptyString.String == nil || *emptyString.String != "" {
		t.Fatalf("empty string scalar = %#v, err=%v", emptyString, err)
	}
	var zeroInteger TableScalar
	if err := json.Unmarshal([]byte(`{"kind":"INTEGER","integer":0}`), &zeroInteger); err != nil || zeroInteger.Integer == nil || *zeroInteger.Integer != 0 {
		t.Fatalf("zero integer scalar = %#v, err=%v", zeroInteger, err)
	}
	var zeroDecimal TableScalar
	if err := json.Unmarshal([]byte(`{"kind":"DECIMAL","decimal":0.0}`), &zeroDecimal); err != nil || zeroDecimal.Decimal == nil || *zeroDecimal.Decimal != 0 {
		t.Fatalf("zero decimal scalar = %#v, err=%v", zeroDecimal, err)
	}
	var falseBoolean TableScalar
	if err := json.Unmarshal([]byte(`{"kind":"BOOLEAN","boolean":false}`), &falseBoolean); err != nil || falseBoolean.Boolean == nil || *falseBoolean.Boolean {
		t.Fatalf("false boolean scalar = %#v, err=%v", falseBoolean, err)
	}
}

func TestTableScalarStrictDecoderRejectsMalformedPayloads(t *testing.T) {
	invalid := []string{
		`{"kind":"NULL","string":""}`,
		`{"kind":"NULL","string":null}`,
		`{"kind":"MISSING","boolean":false}`,
		`{"kind":"MISSING","integer":null}`,
		`{"kind":"STRING"}`,
		`{"kind":"STRING","string":null}`,
		`{"kind":"INTEGER"}`,
		`{"kind":"DECIMAL"}`,
		`{"kind":"BOOLEAN"}`,
		`{"kind":"STRING","string":"x","integer":0}`,
		`{"kind":"STRING","integer":0}`,
		`{"kind":"UNKNOWN"}`,
		`{"kind":"NULL","private":true}`,
	}
	for _, raw := range invalid {
		var scalar TableScalar
		if err := json.Unmarshal([]byte(raw), &scalar); err == nil {
			t.Errorf("malformed scalar %s was accepted as %#v", raw, scalar)
		}
	}
}

func TestTableScalarIdentityDistinguishesAllSixKinds(t *testing.T) {
	empty := ""
	zero := int64(0)
	decimal := 0.0
	falseValue := false
	scalars := []TableScalar{
		{Kind: TableScalarNull},
		{Kind: TableScalarMissing},
		{Kind: TableScalarString, String: &empty},
		{Kind: TableScalarInteger, Integer: &zero},
		{Kind: TableScalarDecimal, Decimal: &decimal},
		{Kind: TableScalarBoolean, Boolean: &falseValue},
	}
	identities := map[string]bool{}
	for _, scalar := range scalars {
		if err := scalar.ValidateStructure(); err != nil {
			t.Fatalf("scalar %#v is structurally invalid: %v", scalar, err)
		}
		identity := scalar.identity()
		if identities[identity] {
			t.Fatalf("identity %q is shared by multiple scalar kinds", identity)
		}
		identities[identity] = true
	}
	if len(identities) != 6 {
		t.Fatalf("distinct identities = %d, want 6", len(identities))
	}
}

func TestTableScalarSentinelsAreValidOnlyAsPivotCategoryKeys(t *testing.T) {
	columns := []Column{{Column: "group"}, {Column: "category"}, {Column: "measure"}}
	sentinels := []TableScalar{{Kind: TableScalarNull}, {Kind: TableScalarMissing}}
	pivot := &TableShape{Reshape: &TableReshape{Kind: "PIVOT", Pivot: &PivotConstruction{
		ConstructionID: "pivot_1", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "measure",
		Categories: []PivotCategory{
			{Key: sentinels[0], Output: ColumnOutput{Column: "null_category", Label: "Null category"}},
			{Key: sentinels[1], Output: ColumnOutput{Column: "missing_category", Label: "Missing category"}},
		},
		DuplicatePolicy: "ERROR", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
	}}}
	if err := pivot.Validate(columns); err != nil {
		t.Fatalf("pivot sentinel keys rejected: %v", err)
	}

	duplicatePivot := &TableShape{Reshape: &TableReshape{Kind: "PIVOT", Pivot: &PivotConstruction{
		ConstructionID: "pivot_1", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "measure",
		Categories: []PivotCategory{
			{Key: sentinels[0], Output: ColumnOutput{Column: "null_a", Label: "Null A"}},
			{Key: sentinels[0], Output: ColumnOutput{Column: "null_b", Label: "Null B"}},
		},
		DuplicatePolicy: "ERROR", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
	}}}
	if err := duplicatePivot.Validate(columns); err == nil || !strings.Contains(err.Error(), "duplicate pivot category key") {
		t.Fatalf("duplicate pivot sentinel key error = %v", err)
	}

	zero := int64(1)
	for index, sentinel := range sentinels {
		unpivot := &TableShape{Reshape: &TableReshape{Kind: "UNPIVOT", Unpivot: &UnpivotConstruction{
			ConstructionID: "unpivot_1", Inputs: []UnpivotInput{{Column: "category", Key: sentinel}},
			KeyOutput: ColumnOutput{Column: "metric_name", Label: "Metric"}, ValueOutput: ColumnOutput{Column: "metric_value", Label: "Value"}, NullRowPolicy: "DROP",
		}}}
		if err := unpivot.Validate(columns); err == nil || !strings.Contains(err.Error(), "sentinel scalar is not a concrete value") {
			t.Errorf("unpivot sentinel %q error = %v", sentinel.Kind, err)
		}

		derived := &TableShape{Derived: []DerivedConstruction{{
			ConstructionID: "derived_1", Output: ColumnOutput{Column: fmt.Sprintf("result_%d", index), Label: "Result"}, Operation: "ADD",
			Left: ArithmeticOperand{Kind: "LITERAL", Literal: &sentinel}, Right: ArithmeticOperand{Kind: "LITERAL", Literal: &TableScalar{Kind: TableScalarInteger, Integer: &zero}}, MissingInputPolicy: "PROPAGATE_NULL",
		}}}
		if err := derived.Validate(columns); err == nil || !strings.Contains(err.Error(), "sentinel scalar is not a concrete value") {
			t.Errorf("arithmetic sentinel literal %q error = %v", sentinel.Kind, err)
		}
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
			name:  "multiple scalar payloads",
			shape: strings.Replace(validPivotTableShapeJSON(), `"string":"heart_rate"`, `"string":"heart_rate","integer":2`, 1),
			want:  "cannot contain multiple value payloads",
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
