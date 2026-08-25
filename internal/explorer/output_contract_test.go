package explorer

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
)

func contractFixture() (recipe.Bundle, []EmittedColumn, PublicOutputContract) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "contract-test", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}}}
	emitted := []EmittedColumn{
		{EmissionID: "em_id", OutputID: "patients", CandidateID: "c_id", OccurrenceID: "base", PublicColumn: "c_id_base", LogicalType: "string", Filterable: true, Chartable: false},
		{EmissionID: "em_name", OutputID: "patients", CandidateID: "c_name", OccurrenceID: "base", PublicColumn: "c_name_base", LogicalType: "string", Filterable: false, Chartable: true},
	}
	contract := PublicOutputContract{OutputID: "patients", Columns: []PublicOutputColumn{
		{EmissionID: "em_id", PublicColumn: "c_id_base", CandidateID: "c_id", OccurrenceID: "base", LogicalType: "string", Filterable: true, Chartable: false},
		{EmissionID: "em_name", PublicColumn: "c_name_base", CandidateID: "c_name", OccurrenceID: "base", LogicalType: "string", Filterable: false, Chartable: true},
	}}
	return bundle, emitted, contract
}

func TestDecodePublicOutputContractIsStrict(t *testing.T) {
	valid := json.RawMessage(`{"outputId":"patients","columns":[]}`)
	if _, err := DecodePublicOutputContract(valid); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]json.RawMessage{
		"missing columns": []byte(`{"outputId":"patients"}`),
		"unknown field":   []byte(`{"outputId":"patients","columns":[],"aql":"for x in c return x"}`),
		"duplicate field": []byte(`{"outputId":"patients","columns":[],"columns":[]}`),
		"trailing value":  []byte(`{"outputId":"patients","columns":[]} {}`),
		"not object":      []byte(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePublicOutputContract(raw); !errors.Is(err, ErrReceiptRecompileRequired) {
				t.Fatalf("error=%v, want ErrReceiptRecompileRequired", err)
			}
		})
	}
}

func TestPublicOutputContractMatchesBundleAndOrderedEmissions(t *testing.T) {
	bundle, emitted, contract := contractFixture()
	if err := contract.ValidateAgainst(bundle, emitted); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*recipe.Bundle, []EmittedColumn, *PublicOutputContract){
		"output identity": func(b *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContract) { c.OutputID = "missing" },
		"column order": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContract) {
			c.Columns[0], c.Columns[1] = c.Columns[1], c.Columns[0]
		},
		"emission metadata":  func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContract) { c.Columns[0].Filterable = false },
		"column count":       func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContract) { c.Columns = c.Columns[:1] },
		"duplicate emission": func(_ *recipe.Bundle, e []EmittedColumn, _ *PublicOutputContract) { e[1].EmissionID = e[0].EmissionID },
		"duplicate public column": func(_ *recipe.Bundle, e []EmittedColumn, _ *PublicOutputContract) {
			e[1].PublicColumn = e[0].PublicColumn
		},
		"foreign emitted output": func(_ *recipe.Bundle, e []EmittedColumn, _ *PublicOutputContract) { e[0].OutputID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			b, e, c := bundle, append([]EmittedColumn(nil), emitted...), contract
			c.Columns = append([]PublicOutputColumn(nil), contract.Columns...)
			mutate(&b, e, &c)
			if err := c.ValidateAgainst(b, e); !errors.Is(err, ErrReceiptRecompileRequired) {
				t.Fatalf("error=%v, want ErrReceiptRecompileRequired", err)
			}
		})
	}
}

func TestCompilationReceiptValidateRejectsMismatchedPublicContract(t *testing.T) {
	r := testReceipt()
	r.PublicOutputContract = json.RawMessage(`{"outputId":"out","columns":[{"emissionId":"em_bad","publicColumn":"c_bad","logicalType":"string","filterable":false,"chartable":false}]}`)
	r.OutputContractDigest, _ = CompilationArtifactDigest(r.PublicOutputContract)
	r.CompilationKey, _ = CompilationKey(r)
	r.ID = ""
	if err := r.Validate(); !errors.Is(err, ErrReceiptRecompileRequired) {
		t.Fatalf("error=%v, want ErrReceiptRecompileRequired", err)
	}
}
