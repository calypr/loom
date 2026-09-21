package recipe

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestTableScalarCanonicalIdentityKeepsTypedZeroDistinct(t *testing.T) {
	positiveZero := float64(0)
	negativeZero := math.Copysign(0, -1)
	positive := TableScalar{Kind: TableScalarDecimal, Decimal: &positiveZero}
	negative := TableScalar{Kind: TableScalarDecimal, Decimal: &negativeZero}
	integer := TableScalar{Kind: TableScalarInteger, Integer: int64Pointer(0)}
	if positive.identity() != negative.identity() {
		t.Fatalf("decimal zero identities differ: %q vs %q", positive.identity(), negative.identity())
	}
	if positive.identity() == integer.identity() {
		t.Fatalf("INTEGER zero and DECIMAL zero share canonical identity %q", positive.identity())
	}

	pivot := GroupedPivot{
		ConstructionID: "shape_zero", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
		Categories: []GroupedPivotCategory{
			{Key: positive, Output: "positive", Label: "Positive"},
			{Key: negative, Output: "negative", Label: "Negative"},
		},
		DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
		UnlistedCategoryPolicy: PivotUnlistedCategoryError,
	}
	if err := validateGroupedPivot(pivot); err == nil || !strings.Contains(err.Error(), "category keys must be unique") {
		t.Fatalf("duplicate signed decimal zero validation = %v, want canonical duplicate rejection", err)
	}
}

func TestTableScalarValidateStructure(t *testing.T) {
	empty := ""
	integer := int64(0)
	decimal := float64(1.25)
	boolean := false
	notFinite := math.NaN()

	tests := []struct {
		name    string
		scalar  TableScalar
		wantErr bool
	}{
		{name: "empty string payload", scalar: TableScalar{Kind: TableScalarString, String: &empty}},
		{name: "integer payload", scalar: TableScalar{Kind: TableScalarInteger, Integer: &integer}},
		{name: "decimal payload", scalar: TableScalar{Kind: TableScalarDecimal, Decimal: &decimal}},
		{name: "boolean payload", scalar: TableScalar{Kind: TableScalarBoolean, Boolean: &boolean}},
		{name: "null sentinel", scalar: TableScalar{Kind: TableScalarNull}},
		{name: "missing sentinel", scalar: TableScalar{Kind: TableScalarMissing}},
		{name: "missing string payload", scalar: TableScalar{Kind: TableScalarString}, wantErr: true},
		{name: "mismatched payload", scalar: TableScalar{Kind: TableScalarString, Integer: &integer}, wantErr: true},
		{name: "multiple payloads", scalar: TableScalar{Kind: TableScalarString, String: &empty, Integer: &integer}, wantErr: true},
		{name: "null with payload", scalar: TableScalar{Kind: TableScalarNull, Boolean: &boolean}, wantErr: true},
		{name: "missing with payload", scalar: TableScalar{Kind: TableScalarMissing, Integer: &integer}, wantErr: true},
		{name: "unknown kind", scalar: TableScalar{Kind: "UNKNOWN"}, wantErr: true},
		{name: "non-finite decimal", scalar: TableScalar{Kind: TableScalarDecimal, Decimal: &notFinite}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.scalar.ValidateStructure()
			if (err != nil) != test.wantErr {
				t.Fatalf("ValidateStructure() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestTableScalarJSONRejectsMalformedPayloads(t *testing.T) {
	invalid := []string{
		`{"kind":"NULL","string":""}`,
		`{"kind":"NULL","string":null}`,
		`{"kind":"MISSING","integer":null}`,
		`{"kind":"STRING","string":"x","integer":null}`,
		`{"kind":"STRING","integer":0}`,
		`{"kind":"STRING"}`,
		`{"kind":"STRING","string":null}`,
		`{"kind":"UNKNOWN"}`,
		`{"kind":"NULL","extra":true}`,
	}
	for _, raw := range invalid {
		var scalar TableScalar
		if err := json.Unmarshal([]byte(raw), &scalar); err == nil {
			t.Errorf("malformed scalar %s was accepted as %#v", raw, scalar)
		}
	}
}

func TestTableScalarJSONRoundTripsAllKinds(t *testing.T) {
	empty := ""
	integer := int64(0)
	decimal := 1.25
	boolean := false
	tests := []struct {
		raw  string
		want TableScalar
	}{
		{raw: `{"kind":"STRING","string":""}`, want: TableScalar{Kind: TableScalarString, String: &empty}},
		{raw: `{"kind":"INTEGER","integer":0}`, want: TableScalar{Kind: TableScalarInteger, Integer: &integer}},
		{raw: `{"kind":"DECIMAL","decimal":1.25}`, want: TableScalar{Kind: TableScalarDecimal, Decimal: &decimal}},
		{raw: `{"kind":"BOOLEAN","boolean":false}`, want: TableScalar{Kind: TableScalarBoolean, Boolean: &boolean}},
		{raw: `{"kind":"NULL"}`, want: TableScalar{Kind: TableScalarNull}},
		{raw: `{"kind":"MISSING"}`, want: TableScalar{Kind: TableScalarMissing}},
	}
	for _, test := range tests {
		t.Run(string(test.want.Kind), func(t *testing.T) {
			var scalar TableScalar
			if err := json.Unmarshal([]byte(test.raw), &scalar); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(scalar, test.want) {
				t.Fatalf("decoded scalar = %#v, want %#v", scalar, test.want)
			}
			if err := scalar.ValidateStructure(); err != nil {
				t.Fatalf("scalar is structurally invalid: %v", err)
			}
			encoded, err := json.Marshal(scalar)
			if err != nil {
				t.Fatal(err)
			}
			var roundTripped TableScalar
			if err := json.Unmarshal(encoded, &roundTripped); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTripped, test.want) {
				t.Fatalf("round-trip scalar = %#v, want %#v", roundTripped, test.want)
			}
		})
	}
}

func TestTableScalarCanonicalIdentityDistinguishesAllSixKinds(t *testing.T) {
	empty := ""
	integer := int64(0)
	decimal := float64(0)
	boolean := false
	scalars := []TableScalar{
		{Kind: TableScalarString, String: &empty},
		{Kind: TableScalarInteger, Integer: &integer},
		{Kind: TableScalarDecimal, Decimal: &decimal},
		{Kind: TableScalarBoolean, Boolean: &boolean},
		{Kind: TableScalarNull},
		{Kind: TableScalarMissing},
	}

	identities := make(map[string]bool, len(scalars))
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

func TestGroupedPivotAcceptsNullAndMissingCategoryKeys(t *testing.T) {
	pivot := TableReshape{
		Kind: TableReshapeGroupedPivot,
		GroupedPivot: &GroupedPivot{
			ConstructionID: "pivot_sentinels", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories: []GroupedPivotCategory{
				{Key: TableScalar{Kind: TableScalarNull}, Output: "null_category", Label: "Null"},
				{Key: TableScalar{Kind: TableScalarMissing}, Output: "missing_category", Label: "Missing"},
			},
			DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
			UnlistedCategoryPolicy: PivotUnlistedCategoryError,
		},
	}
	if err := validateTableReshapeBundle(pivot); err != nil {
		t.Fatalf("pivot sentinel keys rejected: %v", err)
	}
}

func TestGroupedPivotRejectsDuplicateSentinelCategoryKeys(t *testing.T) {
	for _, kind := range []TableScalarKind{TableScalarNull, TableScalarMissing} {
		t.Run(string(kind), func(t *testing.T) {
			key := TableScalar{Kind: kind}
			pivot := TableReshape{
				Kind: TableReshapeGroupedPivot,
				GroupedPivot: &GroupedPivot{
					ConstructionID: "pivot_duplicate_sentinel", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
					Categories: []GroupedPivotCategory{
						{Key: key, Output: "first", Label: "First"},
						{Key: key, Output: "second", Label: "Second"},
					},
					DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
					UnlistedCategoryPolicy: PivotUnlistedCategoryError,
				},
			}
			if err := validateTableReshapeBundle(pivot); err == nil || !strings.Contains(err.Error(), "category keys must be unique") {
				t.Fatalf("duplicate %s category error = %v", kind, err)
			}
		})
	}
}

func TestUnpivotRejectsNullAndMissingKeys(t *testing.T) {
	for _, kind := range []TableScalarKind{TableScalarNull, TableScalarMissing} {
		t.Run(string(kind), func(t *testing.T) {
			unpivot := TableReshape{
				Kind: TableReshapeUnpivot,
				Unpivot: &Unpivot{
					ConstructionID: "unpivot_sentinel",
					Inputs:         []UnpivotInput{{Column: "value", Key: TableScalar{Kind: kind}}},
					KeyOutput:      "name", KeyLabel: "Name", ValueOutput: "result", ValueLabel: "Result",
					NullRowPolicy: UnpivotNullDrop,
				},
			}
			if err := validateTableReshapeBundle(unpivot); err == nil || !strings.Contains(err.Error(), "sentinel scalar is not a concrete value") {
				t.Fatalf("unpivot %s key error = %v", kind, err)
			}
		})
	}
}

func validateTableReshapeBundle(reshape TableReshape) error {
	return (Bundle{
		RecipeSchemaVersion: CurrentSchemaVersion,
		Name:                "table-reshape-test",
		TranslationVersion:  "test",
		Outputs: []Output{{
			Name: "patients", RootResourceType: "Patient", RowGrain: "patient", TableReshape: &reshape,
		}},
	}).Validate()
}

func int64Pointer(value int64) *int64 { return &value }
