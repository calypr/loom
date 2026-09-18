package explorer

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/unit"
)

func contractFixture() (recipe.Bundle, []EmittedColumn, PublicOutputContracts) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "contract-test", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "patients", RootResourceType: "Patient", RowGrain: "patient"}, {Name: "specimens", RootResourceType: "Specimen", RowGrain: "specimen"}}}
	emitted := []EmittedColumn{
		{EmissionID: "em_id", OutputID: "patients", CandidateID: "c_id", OccurrenceID: "base", ProjectionMode: "VALUE", PublicColumn: "c_id_base", Label: "Patient ID", LogicalType: "string", Filterable: true, Chartable: false},
		{EmissionID: "em_name", OutputID: "patients", CandidateID: "c_name", OccurrenceID: "base", ProjectionMode: "FIRST", PublicColumn: "c_name_base", Label: "Patient name", LogicalType: "string", Filterable: false, Chartable: true},
		{EmissionID: "em_specimen", OutputID: "specimens", CandidateID: "c_specimen", OccurrenceID: "base", ProjectionMode: "VALUE", PublicColumn: "c_specimen_base", Label: "Specimen ID", LogicalType: "string", Filterable: true, Chartable: true},
	}
	contract := PublicOutputContracts{Outputs: []PublicOutputContract{
		{OutputID: "patients", Columns: []PublicOutputColumn{
			{Column: "c_id_base", Label: "Patient ID", LogicalType: "string", Filterable: true, Chartable: false},
			{Column: "c_name_base", Label: "Patient name", LogicalType: "string", Filterable: false, Chartable: true},
		}},
		{OutputID: "specimens", Columns: []PublicOutputColumn{
			{Column: "c_specimen_base", Label: "Specimen ID", LogicalType: "string", Filterable: true, Chartable: true},
		}},
	}}
	return bundle, emitted, contract
}

func TestDecodePublicOutputContractsIsStrict(t *testing.T) {
	valid := json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[]}]}`)
	if _, err := DecodePublicOutputContracts(valid); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string]json.RawMessage{
		"legacy singular contract": []byte(`{"outputId":"patients","columns":[]}`),
		"missing outputs":          []byte(`{}`),
		"null outputs":             []byte(`{"outputs":null}`),
		"missing columns":          []byte(`{"outputs":[{"outputId":"patients"}]}`),
		"unknown field":            []byte(`{"outputs":[],"aql":"for x in c return x"}`),
		"duplicate field":          []byte(`{"outputs":[],"outputs":[]}`),
		"duplicate output":         []byte(`{"outputs":[{"outputId":"patients","columns":[]},{"outputId":"patients","columns":[]}]}`),
		"trailing value":           []byte(`{"outputs":[]} {}`),
		"not object":               []byte(`[]`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodePublicOutputContracts(raw); !errors.Is(err, ErrReceiptRecompileRequired) {
				t.Fatalf("error=%v, want ErrReceiptRecompileRequired", err)
			}
		})
	}
}

func TestPublicOutputContractsMatchBundleAndOrderedEmissions(t *testing.T) {
	bundle, emitted, contract := contractFixture()
	if err := contract.ValidateAgainst(bundle, emitted); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*recipe.Bundle, []EmittedColumn, *PublicOutputContracts){
		"output identity": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContracts) { c.Outputs[0].OutputID = "missing" },
		"output order": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContracts) {
			c.Outputs[0], c.Outputs[1] = c.Outputs[1], c.Outputs[0]
		},
		"column order": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContracts) {
			c.Outputs[0].Columns[0], c.Outputs[0].Columns[1] = c.Outputs[0].Columns[1], c.Outputs[0].Columns[0]
		},
		"physical column": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContracts) {
			c.Outputs[0].Columns[0].Column = "forged"
		},
		"authored column": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContracts) {
			c.Outputs[0].Columns[0].AuthoredColumns = []string{"forged"}
		},
		"label metadata": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContracts) {
			c.Outputs[0].Columns[0].Label = "forged"
		},
		"column count": func(_ *recipe.Bundle, _ []EmittedColumn, c *PublicOutputContracts) {
			c.Outputs[0].Columns = c.Outputs[0].Columns[:1]
		},
		"duplicate public column": func(_ *recipe.Bundle, e []EmittedColumn, _ *PublicOutputContracts) {
			e[1].PublicColumn = e[0].PublicColumn
		},
		"foreign emitted output": func(_ *recipe.Bundle, e []EmittedColumn, _ *PublicOutputContracts) { e[0].OutputID = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			b, e, c := bundle, append([]EmittedColumn(nil), emitted...), contract
			c.Outputs = append([]PublicOutputContract(nil), contract.Outputs...)
			for i := range c.Outputs {
				c.Outputs[i].Columns = append([]PublicOutputColumn(nil), contract.Outputs[i].Columns...)
			}
			mutate(&b, e, &c)
			if err := c.ValidateAgainst(b, e); !errors.Is(err, ErrReceiptRecompileRequired) {
				t.Fatalf("error=%v, want ErrReceiptRecompileRequired", err)
			}
		})
	}
}

func TestPublicOutputContractRejectsForgedAggregateQualityFlags(t *testing.T) {
	bundle, emitted, contract := contractFixture()
	contract.Outputs[0].Lossless = true
	if err := contract.ValidateAgainst(bundle, emitted); !errors.Is(err, ErrReceiptRecompileRequired) {
		t.Fatalf("forged lossless aggregate accepted: %v", err)
	}
	contract.Outputs[0].Lossless = false
	contract.Outputs[0].MLReady = true
	if err := contract.ValidateAgainst(bundle, emitted); !errors.Is(err, ErrReceiptRecompileRequired) {
		t.Fatalf("forged ML-ready aggregate accepted: %v", err)
	}
	contract.Outputs[0].MLReady = false
	if err := contract.ValidateAgainst(bundle, emitted); err != nil {
		t.Fatalf("matching lossless and ML-ready aggregates rejected: %v", err)
	}
}

func TestPublicOutputContractPinsUnitTargetAndRuleIdentity(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "units", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "out", RootResourceType: "Observation", RowGrain: "observation"}}}
	normalization := &PublicUnitNormalization{Target: unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}, Rules: []PublicUnitRuleIdentity{{ID: "ucum:m-to-cm", Version: "1"}, {ID: "identity-v1", Version: "1"}}}
	emitted := []EmittedColumn{{OutputID: "out", PublicColumn: "height", Label: "Height", LogicalType: "decimal", UnitNormalization: normalization}}
	contract := PublicOutputContracts{Outputs: []PublicOutputContract{{OutputID: "out", Columns: []PublicOutputColumn{{Column: "height", Label: "Height", LogicalType: "decimal", UnitNormalization: &PublicUnitNormalization{Target: unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}, Rules: append([]PublicUnitRuleIdentity(nil), normalization.Rules...)}}}}}}
	if err := contract.ValidateAgainst(bundle, emitted); err != nil {
		t.Fatal(err)
	}
	contract.Outputs[0].Columns[0].UnitNormalization.Target.Code = "m"
	if err := contract.ValidateAgainst(bundle, emitted); !errors.Is(err, ErrReceiptRecompileRequired) {
		t.Fatalf("forged unit target accepted: %v", err)
	}
}

func TestCompilationReceiptValidateRejectsMismatchedPublicContract(t *testing.T) {
	r := testReceipt()
	r.PublicOutputContract = json.RawMessage(`{"outputs":[{"outputId":"out","columns":[{"column":"c_bad","label":"Bad","logicalType":"string","filterable":false,"chartable":false}]}]}`)
	r.OutputContractDigest, _ = CompilationArtifactDigest(r.PublicOutputContract)
	r.CompilationKey, _ = CompilationKey(r)
	r.ID = ""
	if err := r.Validate(); !errors.Is(err, ErrReceiptRecompileRequired) {
		t.Fatalf("error=%v, want ErrReceiptRecompileRequired", err)
	}
}

func TestCompilationReceiptValidateAcceptsCurrentMultiOutputContract(t *testing.T) {
	bundle, emitted, contracts := contractFixture()
	raw, err := json.Marshal(contracts)
	if err != nil {
		t.Fatal(err)
	}
	r := testReceipt()
	r.Bundle = bundle
	r.OutputColumnProvenance = map[string]map[string]string{"patients": {"patient_id": "EXPLICIT"}, "specimens": {"specimen_id": "EXPLICIT"}}
	r.EmittedColumns = emitted
	r.PublicOutputContract = raw
	r.ResolvedRecipeDigest, err = bundle.Digest()
	if err != nil {
		t.Fatal(err)
	}
	r.OutputContractDigest, err = CompilationArtifactDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	r.CompilationKey, err = CompilationKey(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID, err = ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}
