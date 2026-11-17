package benchmark

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Deterministic matched-fixture generation. One generator produces the FHIR
// NDJSON graph files and the independently-derived facts that feed the
// correctness oracles. Both benchmark arms load byte-identical files, so any
// cross-backend output difference is backend behavior, never fixture drift.
//
// Determinism law: every byte derives from (profile, seed, entity index)
// through fnv hashing only -- no map iteration, no time, no goroutines.
// Re-generating the same DatasetParams must reproduce the same digest.

// Presets are the closed size ladder. representative is the profile the
// comparative-workload-matrix check runs; real data is a separate operator
// supplied profile and is never synthesized under this name.
const (
	PresetTiny           = "tiny"
	PresetSmall          = "small"
	PresetRepresentative = "representative"
)

// DatasetParams pins one matched dataset. All identity strings must be fixed
// per run so both arms load the same logical scope.
type DatasetParams struct {
	Preset     string
	Seed       uint64
	Project    string
	Generation string
	// AuthResourcePath is stamped onto every vertex/edge by the production
	// loader (ingest LoadOptions.AuthResourcePath). The scoped negative
	// authorization control runs a Fence grant list that excludes it.
	AuthResourcePath string
}

// DatasetSize is the closed cardinality plan per preset. Sizes are exact,
// not sampled, so facts can be derived exactly.
type DatasetSize struct {
	Patients        int
	Observations    int // total, distributed deterministically per patient
	Specimens       int
	Conditions      int
	ResearchStudies int
}

func datasetSize(preset string) (DatasetSize, error) {
	switch preset {
	case PresetTiny:
		return DatasetSize{Patients: 12, Observations: 48, Specimens: 18, Conditions: 20, ResearchStudies: 2}, nil
	case PresetSmall:
		return DatasetSize{Patients: 200, Observations: 1600, Specimens: 400, Conditions: 500, ResearchStudies: 3}, nil
	case PresetRepresentative:
		return DatasetSize{Patients: 20000, Observations: 160000, Specimens: 40000, Conditions: 50000, ResearchStudies: 4}, nil
	default:
		return DatasetSize{}, fmt.Errorf("benchmark: unknown dataset preset %q", preset)
	}
}

// Observation code families used by the workload matrix. Codes are stable
// identities; changing one is a fixture-contract change, not a refactor.
const (
	ObservationCodeAge    = "BENCH-CODE-AGE"    // valueQuantity.value feeds SUM/MIN/MAX/MEAN
	ObservationCodeStage  = "BENCH-CODE-STAGE"  // valueCodeableConcept feeds Pivot
	ObservationCodeGender = "BENCH-CODE-GENDER" // valueString feeds Unpivot column keys
)

// BenchAgeIdentifierSystem is the identifier namespace on age observations
// whose value is the age as a decimal string. It is the scalar-string source
// the derive-age-band workspace recodes through EXACT_CATEGORY_RECODE. It is
// emitted exactly when valueQuantity.value is (the missing-value observations
// carry neither, so the oracle's skip and the workspace's null-exclusion
// agree by construction).
const BenchAgeIdentifierSystem = "https://bench.example/identifier/age-days"

// Specimen type codes (duplicate-contributor and filter workloads).
const (
	SpecimenTypePrimary    = "PRIMARY"
	SpecimenTypeMetastatic = "METASTATIC"
)

// Condition verification statuses, including one deliberately absent.
const (
	ConditionStatusConfirmed   = "confirmed"
	ConditionStatusProvisional = "provisional"
	// ConditionClinicalActive is the FHIR R4 required clinicalStatus. No
	// workload predicates on it, so it is constant and never an oracle input.
	ConditionClinicalActive = "active"
)

// PatientNamePool sizes control duplicate contributors and repeated-owner
// correlation: the family-name pool is deliberately much smaller than the
// patient count so multiple patients share each family name.
const (
	familyNamePoolSize = 9
	givenNamePoolSize  = 7
)

func hashU64(seed uint64, tags ...string) uint64 {
	h := seed ^ 0x9e3779b97f4a7c15
	for _, tag := range tags {
		for _, b := range []byte(tag) {
			h ^= uint64(b)
			h *= 0x100000001b3
		}
		h ^= h >> 31
		h *= 0xbf58476d1ce4e5b9
	}
	return h
}

func pick[T any](seed uint64, tags []string, choices []T) T {
	h := hashU64(seed, tags...)
	return choices[h%uint64(len(choices))]
}

func patientID(index int) string {
	return fmt.Sprintf("bench-patient-%07d", index)
}

func observationID(index int) string {
	return fmt.Sprintf("bench-observation-%08d", index)
}

func specimenID(index int) string {
	return fmt.Sprintf("bench-specimen-%07d", index)
}

func conditionID(index int) string {
	return fmt.Sprintf("bench-condition-%07d", index)
}

func studyID(index int) string {
	return fmt.Sprintf("bench-study-%03d", index)
}

const partOfStudyURL = "http://fhir-aggregator.org/fhir/StructureDefinition/part-of-study"

// studyRefFor maps a patient deterministically to one study. Every study is
// populated so inbound-study traversal workloads cover all studies.
func (d DatasetParams) studyRefFor(patientIndex int) string {
	size, err := datasetSize(d.Preset)
	if err != nil || size.ResearchStudies == 0 {
		return ""
	}
	return "ResearchStudy/" + studyID(int(hashU64(d.Seed, "study", strconv.Itoa(patientIndex))%uint64(size.ResearchStudies)))
}

func partOfStudyExtension(studyRef string) map[string]any {
	return map[string]any{
		"url":            partOfStudyURL,
		"valueReference": map[string]any{"reference": studyRef},
	}
}

// PatientFacts is the independently known per-patient truth used by oracles.
// Facts are derived from the generation inputs, never read back from a
// backend, which is what makes the correctness gate independent.
type PatientFacts struct {
	ID               string   `json:"id"`
	Active           bool     `json:"active"`
	Gender           string   `json:"gender"` // male|female|other; "" means deliberately missing
	FamilyNames      []string `json:"familyNames"`
	GivenNames       []string `json:"givenNames"`
	IdentifierValues []string `json:"identifierValues"`
	Deceased         *bool    `json:"deceased,omitempty"`
	TelecomValues    []string `json:"telecomValues"`
	ObservationIDs   []string `json:"observationIds"`
	SpecimenIDs      []string `json:"specimenIds"`
	ConditionIDs     []string `json:"conditionIds"`
	StudyRef         string   `json:"studyRef"`
}

// ObservationFacts is per-observation truth, including the numeric value for
// aggregation oracles and the pivot/unpivot payloads.
type ObservationFacts struct {
	ID          string   `json:"id"`
	PatientID   string   `json:"patientId"`
	Status      string   `json:"status"`
	Code        string   `json:"code"`
	ValueNumber *float64 `json:"valueNumber,omitempty"`
	ValueString string   `json:"valueString,omitempty"`
	StageCode   string   `json:"stageCode,omitempty"`
	Effective   string   `json:"effective"`
	HasSpecimen bool     `json:"hasSpecimen"`
	SpecimenID  string   `json:"specimenId,omitempty"`
}

type SpecimenFacts struct {
	ID        string `json:"id"`
	PatientID string `json:"patientId"`
	TypeCode  string `json:"typeCode"`
}

type ConditionFacts struct {
	ID                 string `json:"id"`
	PatientID          string `json:"patientId"`
	CodeDisplay        string `json:"codeDisplay"`
	VerificationStatus string `json:"verificationStatus,omitempty"` // "" = deliberately missing
}

// Facts is the complete independently-known truth for one dataset.
type Facts struct {
	Params       DatasetParams      `json:"params"`
	Size         DatasetSize        `json:"size"`
	Patients     []PatientFacts     `json:"patients"`
	Observations []ObservationFacts `json:"observations"`
	Specimens    []SpecimenFacts    `json:"specimens"`
	Conditions   []ConditionFacts   `json:"conditions"`
	StudyIDs     []string           `json:"studyIds"`
	// Derived aggregates used by grouped-aggregation oracles.
	SumAgeByPatient    map[string]float64  `json:"sumAgeByPatient"`
	CountByPatient     map[string]int      `json:"countByPatient"`
	StageCounts        map[string]int      `json:"stageCounts"`
	FamilyContributors map[string][]string `json:"familyContributors"` // family -> observation ids (duplicate contributors)
}

var genderChoices = []string{"male", "female", "other"}
var observationStatuses = []string{"final", "amended", "registered", "cancelled"}
var stageCodes = []string{"stage-a", "stage-b", "stage-c", "stage-d"}
var familyNames = []string{"alpha", "bravo", "charlie", "delta", "echo", "foxtrot", "golf", "hotel", "india"}
var givenNames = []string{"ada", "bo", "cy", "di", "ed", "fi", "gi"}
var telecomSystems = []string{"phone", "email", "fax"}
var conditionDisplays = []string{"hypertension", "diabetes", "asthma", "anemia", "arrhythmia"}

// statusFor makes exactly the observations with index%17==3 cancelled and
// index%23==5 registered; everything else is final. Oracles reuse statusFor,
// so selective-filter expectations are derived, not transcribed.
func observationStatusFor(index int) string {
	switch {
	case index%17 == 3:
		return "cancelled"
	case index%23 == 5:
		return "registered"
	default:
		return "final"
	}
}

func observationCodeFor(index int) string {
	switch index % 3 {
	case 0:
		return ObservationCodeAge
	case 1:
		return ObservationCodeStage
	default:
		return ObservationCodeGender
	}
}

// GenerateFacts derives the complete independent truth for the params.
func GenerateFacts(params DatasetParams) (*Facts, error) {
	size, err := datasetSize(params.Preset)
	if err != nil {
		return nil, err
	}
	if size.Patients == 0 {
		return nil, fmt.Errorf("benchmark: preset %s has no patients", params.Preset)
	}
	facts := &Facts{
		Params: params, Size: size,
		SumAgeByPatient:    map[string]float64{},
		CountByPatient:     map[string]int{},
		StageCounts:        map[string]int{},
		FamilyContributors: map[string][]string{},
	}
	for s := 0; s < size.ResearchStudies; s++ {
		facts.StudyIDs = append(facts.StudyIDs, studyID(s))
	}
	// Deterministic round-robin with a hash-derived remainder keeps every
	// patient owning at least one of each child type once size permits, and
	// keeps distributions reproducible.
	obsPerPatient := size.Observations / size.Patients
	extraObs := size.Observations % size.Patients
	specPerPatient := size.Specimens / size.Patients
	extraSpec := size.Specimens % size.Patients
	condPerPatient := size.Conditions / size.Patients
	extraCond := size.Conditions % size.Patients
	obsCursor, specCursor, condCursor := 0, 0, 0
	for p := 0; p < size.Patients; p++ {
		id := patientID(p)
		family := []string{familyNames[p%familyNamePoolSize]}
		given := []string{givenNames[p%givenNamePoolSize]}
		// Duplicate contributors: every 4th patient carries a second family
		// name and a second identifier with the SAME value as the first, so
		// DISTINCT vs ALL behave differently and are exercised.
		if p%4 == 0 && len(familyNames) > 1 {
			family = append(family, familyNames[(p/familyNamePoolSize+1)%familyNamePoolSize])
		}
		pf := PatientFacts{
			ID:               id,
			Active:           p%9 != 0,
			FamilyNames:      family,
			GivenNames:       given,
			IdentifierValues: []string{"MRN-" + strconv.Itoa(p)},
			StudyRef:         params.studyRefFor(p),
		}
		if p%9 != 4 {
			pf.Gender = pick(params.Seed, []string{"gender", id}, genderChoices)
		}
		if p%11 == 0 {
			deceased := p%22 == 0
			pf.Deceased = &deceased
		}
		if p%13 != 0 {
			pf.TelecomValues = []string{"+1-555-" + strconv.Itoa(1000+p), strconv.Itoa(p) + "@bench.example"}
			_ = telecomSystems
		}
		count := obsPerPatient
		if p < extraObs {
			count++
		}
		for o := 0; o < count; o++ {
			obs := ObservationFacts{
				ID:        observationID(obsCursor),
				PatientID: id,
				Status:    observationStatusFor(obsCursor),
				Code:      observationCodeFor(obsCursor),
				Effective: fmt.Sprintf("20%02d-%02d-%02d", 15+obsCursor%10, 1+obsCursor%12, 1+obsCursor%28),
			}
			switch obs.Code {
			case ObservationCodeAge:
				// Integer ages in [1, AgeBandFixtureMax] days: the domain
				// straddles both band thresholds so young/middle/senior all
				// occur, and it is exactly the domain the workspace recode
				// map enumerates (fixtures tests pin both invariants).
				value := float64(1 + hashU64(params.Seed, "age", obs.ID)%uint64(AgeBandFixtureMax))
				if p%19 == 0 {
					obs.ValueNumber = nil // missing numeric: aggregation must skip, not zero
				} else {
					obs.ValueNumber = &value
					facts.SumAgeByPatient[id] += value
				}
			case ObservationCodeStage:
				obs.StageCode = pick(params.Seed, []string{"stage", obs.ID}, stageCodes)
				facts.StageCounts[obs.StageCode]++
			case ObservationCodeGender:
				obs.ValueString = "self-reported:" + pick(params.Seed, []string{"selfrep", obs.ID}, []string{"yes", "no"})
			}
			if obsCursor%2 == 0 && specPerPatient+specCursor < size.Specimens {
				obs.HasSpecimen = true
			}
			pf.ObservationIDs = append(pf.ObservationIDs, obs.ID)
			facts.CountByPatient[id]++
			for _, fam := range family {
				facts.FamilyContributors[fam] = append(facts.FamilyContributors[fam], obs.ID)
			}
			facts.Observations = append(facts.Observations, obs)
			obsCursor++
		}
		count = specPerPatient
		if p < extraSpec {
			count++
		}
		for s := 0; s < count; s++ {
			// Type comes from the GLOBAL cursor: with 1-2 specimens per
			// patient, a per-patient index can never reach the metastatic
			// stride and the filter workload would be vacuous at every
			// preset.
			typeCode := SpecimenTypePrimary
			if specCursor%3 == 2 {
				typeCode = SpecimenTypeMetastatic
			}
			sf := SpecimenFacts{ID: specimenID(specCursor), PatientID: id, TypeCode: typeCode}
			pf.SpecimenIDs = append(pf.SpecimenIDs, sf.ID)
			facts.Specimens = append(facts.Specimens, sf)
			specCursor++
		}
		count = condPerPatient
		if p < extraCond {
			count++
		}
		for c := 0; c < count; c++ {
			cf := ConditionFacts{
				ID:          conditionID(condCursor),
				PatientID:   id,
				CodeDisplay: pick(params.Seed, []string{"cond", conditionID(condCursor)}, conditionDisplays),
			}
			if condCursor%7 != 0 {
				cf.VerificationStatus = pick(params.Seed, []string{"verif", conditionID(condCursor)}, []string{ConditionStatusConfirmed, ConditionStatusProvisional})
			}
			pf.ConditionIDs = append(pf.ConditionIDs, cf.ID)
			facts.Conditions = append(facts.Conditions, cf)
			condCursor++
		}
		facts.Patients = append(facts.Patients, pf)
	}
	if obsCursor != size.Observations || specCursor != size.Specimens || condCursor != size.Conditions {
		return nil, fmt.Errorf("benchmark: generator cursor drift obs=%d/%d spec=%d/%d cond=%d/%d", obsCursor, size.Observations, specCursor, size.Specimens, condCursor, size.Conditions)
	}
	return facts, nil
}

// jsonMap marshals through JSON so map ordering can never leak into bytes:
// encoding/json sorts map keys, making each NDJSON line deterministic.
func jsonLine(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("benchmark: marshal fixture document: %w", err)
	}
	return string(raw), nil
}

// Files returns the NDJSON graph files (one per resource type) plus the
// facts sidecar, keyed by file name. The loader ingests only the *.ndjson
// entries; facts.json is the oracle input, never loaded.
type FixtureFiles struct {
	Files  map[string]string
	Digest string
	Facts  *Facts
	Counts map[string]int
}

func (d DatasetParams) buildPatientDocument(f PatientFacts) map[string]any {
	doc := map[string]any{
		"resourceType": "Patient",
		"id":           f.ID,
		"active":       f.Active,
	}
	if f.Gender != "" {
		doc["gender"] = f.Gender
	}
	if f.Deceased != nil {
		doc["deceasedBoolean"] = *f.Deceased
	}
	// FHIR R4 HumanName.family is a single string, so each family name gets
	// its own name entry; name[].family still yields every family the patient
	// carries, which keeps DISTINCT-vs-ALL selective. The given list rides on
	// the first entry only so name[].given[] never duplicates a given value
	// across entries: the sort-limit oracle emits exactly one row per patient.
	names := make([]any, 0, len(f.FamilyNames))
	for i, family := range f.FamilyNames {
		entry := map[string]any{"family": family}
		if i == 0 {
			giv := make([]any, 0, len(f.GivenNames))
			for _, name := range f.GivenNames {
				giv = append(giv, name)
			}
			entry["given"] = giv
		}
		names = append(names, entry)
	}
	doc["name"] = names
	ident := make([]any, 0, len(f.IdentifierValues))
	for _, v := range f.IdentifierValues {
		ident = append(ident, map[string]any{"use": "official", "system": "https://bench.example/mrn", "value": v})
	}
	doc["identifier"] = ident
	if len(f.TelecomValues) > 0 {
		telecom := make([]any, 0, len(f.TelecomValues))
		for i, v := range f.TelecomValues {
			telecom = append(telecom, map[string]any{"system": telecomSystems[i%len(telecomSystems)], "value": v})
		}
		doc["telecom"] = telecom
	}
	if f.StudyRef != "" {
		doc["extension"] = []any{partOfStudyExtension(f.StudyRef)}
	}
	return doc
}

func (d DatasetParams) buildObservationDocument(f ObservationFacts) map[string]any {
	doc := map[string]any{
		"resourceType":      "Observation",
		"id":                f.ID,
		"status":            f.Status,
		"subject":           map[string]any{"reference": "Patient/" + f.PatientID},
		"effectiveDateTime": f.Effective,
		"code": map[string]any{"coding": []any{map[string]any{
			"system": "https://bench.example/codes", "code": f.Code,
		}}},
	}
	if studyRef := d.studyRefFor(patientIndexFromID(f.PatientID)); studyRef != "" {
		doc["extension"] = []any{partOfStudyExtension(studyRef)}
	}
	switch f.Code {
	case ObservationCodeAge:
		if f.ValueNumber != nil {
			doc["valueQuantity"] = map[string]any{
				"value": *f.ValueNumber, "unit": "days",
				"system": "http://unitsofmeasure.org", "code": "d",
			}
			// Scalar-string twin of the quantity for the recode workload.
			doc["identifier"] = []any{map[string]any{
				"use": "secondary", "system": BenchAgeIdentifierSystem,
				"value": strconv.FormatFloat(*f.ValueNumber, 'f', -1, 64),
			}}
		}
	case ObservationCodeStage:
		doc["valueCodeableConcept"] = map[string]any{"coding": []any{map[string]any{
			"system": "https://bench.example/stages", "code": f.StageCode, "display": strings.ToUpper(f.StageCode),
		}}}
	case ObservationCodeGender:
		doc["valueString"] = f.ValueString
	}
	if f.HasSpecimen && f.SpecimenID != "" {
		doc["specimen"] = map[string]any{"reference": "Specimen/" + f.SpecimenID}
	}
	return doc
}

func patientIndexFromID(id string) int {
	v, err := strconv.Atoi(strings.TrimPrefix(id, "bench-patient-"))
	if err != nil {
		return 0
	}
	return v
}

func (d DatasetParams) buildSpecimenDocument(f SpecimenFacts) map[string]any {
	doc := map[string]any{
		"resourceType": "Specimen",
		"id":           f.ID,
		"status":       "available",
		"subject":      map[string]any{"reference": "Patient/" + f.PatientID},
		"type": map[string]any{"coding": []any{map[string]any{
			"system": "https://bench.example/specimen-type", "code": f.TypeCode,
		}}},
	}
	if ref := d.studyRefFor(patientIndexFromID(f.PatientID)); ref != "" {
		doc["extension"] = []any{partOfStudyExtension(ref)}
	}
	return doc
}

func (d DatasetParams) buildConditionDocument(f ConditionFacts) map[string]any {
	doc := map[string]any{
		"resourceType": "Condition",
		"id":           f.ID,
		"subject":      map[string]any{"reference": "Patient/" + f.PatientID},
		"code": map[string]any{"coding": []any{map[string]any{
			"system": "https://bench.example/conditions", "code": strings.ReplaceAll(f.CodeDisplay, " ", "-"), "display": f.CodeDisplay,
		}}},
	}
	if f.VerificationStatus != "" {
		doc["verificationStatus"] = map[string]any{"coding": []any{map[string]any{
			"system": "http://terminology.hl7.org/CodeSystem/condition-ver-status", "code": f.VerificationStatus,
		}}}
	}
	doc["clinicalStatus"] = map[string]any{"coding": []any{map[string]any{
		"system": "http://terminology.hl7.org/CodeSystem/condition-clinical", "code": ConditionClinicalActive,
	}}}
	if ref := d.studyRefFor(patientIndexFromID(f.PatientID)); ref != "" {
		doc["extension"] = []any{partOfStudyExtension(ref)}
	}
	return doc
}

func (d DatasetParams) buildStudyDocument(id string) map[string]any {
	return map[string]any{
		"resourceType": "ResearchStudy",
		"id":           id,
		"status":       "active",
		"title":        "Loom benchmark matched dataset " + d.Preset,
		"extension":    []any{partOfStudyExtension("ResearchStudy/" + id)},
	}
}

// GenerateFiles produces byte-deterministic fixture files for the params.
func GenerateFiles(params DatasetParams) (*FixtureFiles, error) {
	facts, err := GenerateFacts(params)
	if err != nil {
		return nil, err
	}
	// Link observations that own a specimen slot to that patient's first
	// specimen, so Observation->Specimen traversal is exercised with stable
	// ownership.
	specimenByPatient := map[string]string{}
	for _, s := range facts.Specimens {
		if _, ok := specimenByPatient[s.PatientID]; !ok {
			specimenByPatient[s.PatientID] = s.ID
		}
	}
	obsDocs := make([]string, 0, len(facts.Observations))
	for i := range facts.Observations {
		f := facts.Observations[i]
		if f.HasSpecimen {
			f.SpecimenID = specimenByPatient[f.PatientID]
			facts.Observations[i] = f
		}
		line, err := jsonLine(params.buildObservationDocument(f))
		if err != nil {
			return nil, err
		}
		obsDocs = append(obsDocs, line)
	}
	patDocs := make([]string, 0, len(facts.Patients))
	for _, f := range facts.Patients {
		line, err := jsonLine(params.buildPatientDocument(f))
		if err != nil {
			return nil, err
		}
		patDocs = append(patDocs, line)
	}
	specDocs := make([]string, 0, len(facts.Specimens))
	for _, f := range facts.Specimens {
		line, err := jsonLine(params.buildSpecimenDocument(f))
		if err != nil {
			return nil, err
		}
		specDocs = append(specDocs, line)
	}
	condDocs := make([]string, 0, len(facts.Conditions))
	for _, f := range facts.Conditions {
		line, err := jsonLine(params.buildConditionDocument(f))
		if err != nil {
			return nil, err
		}
		condDocs = append(condDocs, line)
	}
	studyDocs := make([]string, 0, len(facts.StudyIDs))
	for _, id := range facts.StudyIDs {
		line, err := jsonLine(params.buildStudyDocument(id))
		if err != nil {
			return nil, err
		}
		studyDocs = append(studyDocs, line)
	}
	files := map[string]string{
		"Patient.ndjson":       strings.Join(patDocs, "\n") + "\n",
		"Observation.ndjson":   strings.Join(obsDocs, "\n") + "\n",
		"Specimen.ndjson":      strings.Join(specDocs, "\n") + "\n",
		"Condition.ndjson":     strings.Join(condDocs, "\n") + "\n",
		"ResearchStudy.ndjson": strings.Join(studyDocs, "\n") + "\n",
	}
	factsRaw, err := json.Marshal(facts)
	if err != nil {
		return nil, fmt.Errorf("benchmark: marshal facts: %w", err)
	}
	files["facts.json"] = string(factsRaw) + "\n"
	// Digest over sorted file names, sizes, and per-file digests: stable
	// across maps, goroutines, and time.
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	counts := map[string]int{}
	for _, name := range names {
		if strings.HasSuffix(name, ".ndjson") {
			counts[strings.TrimSuffix(name, ".ndjson")] = strings.Count(files[name], "\n")
		}
		fmt.Fprintf(h, "%s\x00%d\x00%x\n", name, len(files[name]), sha256.Sum256([]byte(files[name])))
	}
	return &FixtureFiles{Files: files, Digest: hex.EncodeToString(h.Sum(nil)), Facts: facts, Counts: counts}, nil
}
