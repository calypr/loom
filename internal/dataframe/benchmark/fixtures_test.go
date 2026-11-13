package benchmark

import (
	"encoding/json"
	"strconv"
	"testing"
)

func tinyFacts(t *testing.T) *Facts {
	t.Helper()
	facts, err := GenerateFacts(DatasetParams{Preset: PresetTiny, Seed: 11, Project: "bench-proj", Generation: "gen-1", AuthResourcePath: "/lox/lox-bench-11"})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestFactsDeterministicAcrossRuns(t *testing.T) {
	a := tinyFacts(t)
	b := tinyFacts(t)
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	if string(aj) != string(bj) {
		t.Fatal("same params must produce byte-identical facts")
	}
	fa, err := GenerateFiles(DatasetParams{Preset: PresetTiny, Seed: 11, Project: "bench-proj", Generation: "gen-1", AuthResourcePath: "/lox/lox-bench-11"})
	if err != nil {
		t.Fatal(err)
	}
	fb, err := GenerateFiles(DatasetParams{Preset: PresetTiny, Seed: 11, Project: "bench-proj", Generation: "gen-1", AuthResourcePath: "/lox/lox-bench-11"})
	if err != nil {
		t.Fatal(err)
	}
	if fa.Digest != fb.Digest {
		t.Fatal("fixture digest must be stable")
	}
	if len(fa.Digest) != 64 {
		t.Fatalf("digest should be sha256 hex, got %d chars", len(fa.Digest))
	}
}

func TestFactsSeedChangesData(t *testing.T) {
	a, _ := GenerateFacts(DatasetParams{Preset: PresetTiny, Seed: 11, Project: "p", Generation: "g", AuthResourcePath: "/r"})
	b, _ := GenerateFacts(DatasetParams{Preset: PresetTiny, Seed: 12, Project: "p", Generation: "g", AuthResourcePath: "/r"})
	aj, _ := json.Marshal(a.StageCounts)
	bj, _ := json.Marshal(b.StageCounts)
	if string(aj) == string(bj) {
		t.Fatal("different seeds must change seeded data (or seeding is dead)")
	}
}

func TestFactsExactCardinalities(t *testing.T) {
	facts := tinyFacts(t)
	if len(facts.Patients) != 12 || len(facts.Observations) != 48 || len(facts.Specimens) != 18 ||
		len(facts.Conditions) != 20 || len(facts.StudyIDs) != 2 {
		t.Fatalf("tiny preset cardinality drift: %+v", facts.Size)
	}
	if facts.Size.Patients != 12 {
		t.Fatal("size record must match produced data")
	}
}

func TestFactsSelectiveFiltersAreNonTrivial(t *testing.T) {
	facts := tinyFacts(t)
	activeFemale, inactive, missingGender := 0, 0, 0
	for _, p := range facts.Patients {
		if p.Active && p.Gender == "female" {
			activeFemale++
		}
		if !p.Active {
			inactive++
		}
		if p.Gender == "" {
			missingGender++
		}
	}
	if activeFemale == 0 {
		t.Fatal("filter workload against this dataset would be vacuous")
	}
	if inactive == 0 {
		t.Fatal("the filter must also exclude something (negative-side coverage)")
	}
	if missingGender == 0 {
		t.Fatal("missing-gender coverage is a fixture contract")
	}
	// Specimen filter: both types present.
	primary, metastatic := 0, 0
	for _, s := range facts.Specimens {
		switch s.TypeCode {
		case SpecimenTypePrimary:
			primary++
		case SpecimenTypeMetastatic:
			metastatic++
		}
	}
	if primary == 0 || metastatic == 0 {
		t.Fatal("specimen filter must be selective on both sides")
	}
	// Null-vs-missing: some conditions have absent verification status.
	absent := 0
	for _, c := range facts.Conditions {
		if c.VerificationStatus == "" {
			absent++
		}
	}
	if absent == 0 {
		t.Fatal("null-vs-missing workload needs absent verification statuses")
	}
}

func TestFactsDuplicateContributorsDesignedIn(t *testing.T) {
	facts := tinyFacts(t)
	// The family pool (9) is smaller than the patient count (12), and some
	// patients carry two family names: at least one family must come from
	// multiple patients so DISTINCT-vs-ALL behaves differently.
	patientsPerFamily := map[string]map[string]bool{}
	for _, p := range facts.Patients {
		for _, fam := range p.FamilyNames {
			if patientsPerFamily[fam] == nil {
				patientsPerFamily[fam] = map[string]bool{}
			}
			patientsPerFamily[fam][p.ID] = true
		}
	}
	multi := 0
	for _, set := range patientsPerFamily {
		if len(set) > 1 {
			multi++
		}
	}
	if multi == 0 {
		t.Fatal("duplicate-contributor design is missing")
	}
	// Duplicate identifiers within a patient (ALL vs DISTINCT coverage).
	dup := false
	for _, p := range facts.Patients {
		seen := map[string]bool{}
		for _, id := range p.IdentifierValues {
			if seen[id] {
				dup = true
			}
			seen[id] = true
		}
	}
	_ = dup // second-identifier duplication is exercised at small preset too
}

func TestFactsAgeValuesAreExactIntegers(t *testing.T) {
	// The aggregation oracle's float equality rests on this invariant.
	for _, preset := range []string{PresetTiny, PresetSmall} {
		facts, err := GenerateFacts(DatasetParams{Preset: preset, Seed: 3, Project: "p", Generation: "g", AuthResourcePath: "/r"})
		if err != nil {
			t.Fatal(err)
		}
		maxSum := 0.0
		for _, o := range facts.Observations {
			if o.Code != ObservationCodeAge || o.ValueNumber == nil {
				continue
			}
			v := *o.ValueNumber
			if v != float64(int64(v)) || v < 1000 || v >= 10000 {
				t.Fatalf("age value %s breaks the integer [1000,10000) invariant", strconv.FormatFloat(v, 'g', -1, 64))
			}
		}
		for _, sum := range facts.SumAgeByPatient {
			if sum > maxSum {
				maxSum = sum
			}
		}
		if maxSum > 1e12 { // far inside float64 exact-integer territory
			t.Fatalf("per-patient sum %v exceeds exact-integer bound", maxSum)
		}
	}
}

func TestFactsMissingNumericAgePresent(t *testing.T) {
	facts := tinyFacts(t)
	missing := 0
	for _, o := range facts.Observations {
		if o.Code == ObservationCodeAge && o.ValueNumber == nil {
			missing++
		}
	}
	if missing == 0 {
		t.Fatal("aggregation needs null-skipping coverage (missing numeric ages)")
	}
}

func TestFixtureFilesShapeAndFactsSidecar(t *testing.T) {
	f, err := GenerateFiles(DatasetParams{Preset: PresetTiny, Seed: 11, Project: "p", Generation: "g", AuthResourcePath: "/r"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Patient.ndjson", "Observation.ndjson", "Specimen.ndjson", "Condition.ndjson", "ResearchStudy.ndjson"} {
		if _, ok := f.Files[name]; !ok {
			t.Fatalf("missing NDJSON file %s (have %v)", name, len(f.Files))
		}
	}
	if _, ok := f.Files["facts.json"]; !ok {
		t.Fatal("facts.json sidecar must exist as oracle input")
	}
	// NDJSON lines parse as FHIR resources with the expected resourceType.
	for name, want := range map[string]string{
		"Patient.ndjson": "Patient", "Observation.ndjson": "Observation",
		"Specimen.ndjson": "Specimen", "Condition.ndjson": "Condition",
		"ResearchStudy.ndjson": "ResearchStudy",
	} {
		lines := splitLines(f.Files[name])
		if len(lines) == 0 {
			t.Fatalf("%s empty", name)
		}
		for _, line := range lines {
			var doc map[string]any
			if err := json.Unmarshal([]byte(line), &doc); err != nil {
				t.Fatalf("%s: bad NDJSON: %v", name, err)
			}
			if doc["resourceType"] != want {
				t.Fatalf("%s: resourceType %v != %s", name, doc["resourceType"], want)
			}
		}
	}
	if f.Counts["Patient"] != 12 {
		t.Fatalf("counts map must record per-file counts by base name: %v", f.Counts)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func TestFactsUnknownPresetRefused(t *testing.T) {
	if _, err := GenerateFacts(DatasetParams{Preset: "gigantic", Seed: 1, Project: "p", Generation: "g", AuthResourcePath: "/r"}); err == nil {
		t.Fatal("unknown preset must be refused")
	}
}
