package benchmark

import (
	"fmt"
	"sort"
)

// Independent oracles for the tiny preset. These are derived directly from
// Facts (the fixture's own truth record) with plain Go arithmetic — no Loom
// planner, compiler, or storage is involved — so agreement between both
// backends AND the oracle is three-way evidence, not a tautology.
//
// Oracle rows are produced in the same public column shape the workloads
// publish, so they canonicalize and bag through the identical path.

// OracleFor returns the independently expected bag for one workload under the
// tiny preset, or (nil, nil) when the workload has no independent oracle
// (then only cross-backend equality gates apply, recorded as such).
func OracleFor(workloadID string, facts *Facts) (*CanonicalBag, error) {
	rows, ok := oracleRows(workloadID, facts)
	if !ok {
		return nil, nil
	}
	columns, ok := workloadColumns(workloadID)
	if !ok {
		return nil, fmt.Errorf("benchmark: workload %q has an oracle but no column contract", workloadID)
	}
	bag := Bag(CanonicalizeRows(columns, rows))
	return &bag, nil
}

func oracleRows(workloadID string, facts *Facts) ([]map[string]any, bool) {
	switch workloadID {
	case WorkloadFilterActiveGender:
		return oracleFilterActiveGender(facts), true
	case WorkloadAggregateSumAgeByPatient:
		return oracleAggregateSum(facts), true
	case WorkloadGroupCountByPatient:
		return oracleGroupCount(facts), true
	case WorkloadStageCounts:
		return oracleStageCounts(facts), true
	case WorkloadSpecimenTypeFilter:
		return oracleSpecimenTypeFilter(facts), true
	case WorkloadConditionNullHandling:
		return oracleConditionNulls(facts), true
	case WorkloadDistinctFamily:
		return oracleDistinctFamily(facts), true
	case WorkloadStudyCorrelation:
		return oracleStudyCorrelation(facts), true
	case WorkloadSortLimitTopN:
		return oracleSortLimit(facts), true
	case WorkloadDeriveAgeBand:
		return oracleDeriveAgeBand(facts), true
	default:
		return nil, false
	}
}

func oracleFilterActiveGender(facts *Facts) []map[string]any {
	var rows []map[string]any
	for _, p := range facts.Patients {
		if !p.Active || p.Gender != "female" {
			continue
		}
		rows = append(rows, map[string]any{"patient_id": p.ID, "gender": p.Gender})
	}
	return rows
}

func oracleAggregateSum(facts *Facts) []map[string]any {
	type acc struct {
		sum   float64
		count int
		min   float64
		max   float64
		seen  bool
	}
	byPatient := map[string]*acc{}
	for _, o := range facts.Observations {
		if o.Code != ObservationCodeAge || o.ValueNumber == nil {
			continue
		}
		a := byPatient[o.PatientID]
		if a == nil {
			a = &acc{}
			byPatient[o.PatientID] = a
		}
		v := *o.ValueNumber
		a.sum += v
		a.count++
		if !a.seen || v < a.min {
			a.min = v
		}
		if !a.seen || v > a.max {
			a.max = v
		}
		a.seen = true
	}
	patients := make([]string, 0, len(byPatient))
	for patient := range byPatient {
		patients = append(patients, patient)
	}
	sort.Strings(patients)
	rows := make([]map[string]any, 0, len(patients))
	for _, patient := range patients {
		a := byPatient[patient]
		rows = append(rows, map[string]any{
			"patient_id": patient,
			"sum_age":    a.sum,
			"count_age":  float64(a.count),
			"min_age":    a.min,
			"max_age":    a.max,
		})
	}
	return rows
}

func oracleGroupCount(facts *Facts) []map[string]any {
	rows := make([]map[string]any, 0, len(facts.CountByPatient))
	for patient, count := range facts.CountByPatient {
		rows = append(rows, map[string]any{"patient_id": patient, "obs_count": float64(count)})
	}
	return rows
}

func oracleStageCounts(facts *Facts) []map[string]any {
	rows := make([]map[string]any, 0, len(facts.StageCounts))
	for stage, count := range facts.StageCounts {
		rows = append(rows, map[string]any{"stage_code": stage, "stage_count": float64(count)})
	}
	return rows
}

func oracleSpecimenTypeFilter(facts *Facts) []map[string]any {
	var rows []map[string]any
	for _, s := range facts.Specimens {
		if s.TypeCode != SpecimenTypePrimary {
			continue
		}
		rows = append(rows, map[string]any{"specimen_id": s.ID, "patient_id": s.PatientID, "type_code": s.TypeCode})
	}
	return rows
}

// oracleConditionNulls emits null (not missing) for absent verification
// statuses: the backend must produce an explicit null too, or the
// null-vs-missing gate fails — which is the point.
func oracleConditionNulls(facts *Facts) []map[string]any {
	rows := make([]map[string]any, 0, len(facts.Conditions))
	for _, c := range facts.Conditions {
		var status any
		if c.VerificationStatus != "" {
			status = c.VerificationStatus
		}
		rows = append(rows, map[string]any{"condition_id": c.ID, "verification_status": status})
	}
	return rows
}

func oracleDistinctFamily(facts *Facts) []map[string]any {
	seen := map[string]bool{}
	var rows []map[string]any
	for _, p := range facts.Patients {
		for _, family := range p.FamilyNames {
			if seen[family] {
				continue
			}
			seen[family] = true
			rows = append(rows, map[string]any{"family": family})
		}
	}
	return rows
}

func oracleStudyCorrelation(facts *Facts) []map[string]any {
	var rows []map[string]any
	for _, p := range facts.Patients {
		rows = append(rows, map[string]any{"patient_id": p.ID, "study_ref": p.StudyRef})
	}
	return rows
}

// oracleSortLimit mirrors the workload's deterministic total order: family
// asc, then given asc, then patient_id asc; the top N by that order. The
// tie-break on patient_id makes the ordering total, so both backends must
// agree on exactly the same N rows with multiplicity.
func oracleSortLimit(facts *Facts) []map[string]any {
	type entry struct {
		family, given, id string
	}
	var entries []entry
	for _, p := range facts.Patients {
		if !p.Active {
			continue
		}
		family, given := "", ""
		if len(p.FamilyNames) > 0 {
			family = p.FamilyNames[0]
		}
		if len(p.GivenNames) > 0 {
			given = p.GivenNames[0]
		}
		entries = append(entries, entry{family, given, p.ID})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].family != entries[j].family {
			return entries[i].family < entries[j].family
		}
		if entries[i].given != entries[j].given {
			return entries[i].given < entries[j].given
		}
		return entries[i].id < entries[j].id
	})
	limit := TopNLimit
	if limit > len(entries) {
		limit = len(entries)
	}
	rows := make([]map[string]any, 0, limit)
	for _, e := range entries[:limit] {
		rows = append(rows, map[string]any{"family": e.family, "given": e.given, "patient_id": e.id})
	}
	return rows
}

func oracleDeriveAgeBand(facts *Facts) []map[string]any {
	var rows []map[string]any
	for _, o := range facts.Observations {
		if o.Code != ObservationCodeAge || o.ValueNumber == nil {
			continue
		}
		rows = append(rows, map[string]any{"observation_id": o.ID, "age_band": AgeBandFor(*o.ValueNumber)})
	}
	return rows
}

// AgeBandThresholds are shared between the recipe (authoring recode
// mappings, matrix domain) and the oracle so the two cannot drift apart
// silently; a change is a single edit here.
const (
	AgeBandYoungUnder  = 30
	AgeBandMiddleUnder = 60
	TopNLimit          = 25
	// AgeBandFixtureMax is the inclusive upper bound of the fixture age
	// domain [1, AgeBandFixtureMax]: it straddles both thresholds so all
	// three bands occur, and it is the domain the workspace recode map
	// enumerates (the offline witness pins this coverage).
	AgeBandFixtureMax = 90
)

// AgeBandFor is the single banding function: the oracle applies it to the
// numeric quantity, the workspace recode maps are generated from it, and
// the fixture domain is checked against it. The derived labels therefore
// cannot drift from the independent threshold expectation by construction.
func AgeBandFor(v float64) string {
	switch {
	case v < AgeBandYoungUnder:
		return "young"
	case v < AgeBandMiddleUnder:
		return "middle"
	default:
		return "senior"
	}
}

// Exact float equality note: age valueQuantity values are integers in
// [1, AgeBandFixtureMax] (fixtures.go), and per-patient sums stay far
// inside float64 exact integer range at every preset, so
// canonicalNumberText's shortest-round-trip spelling matches backend output
// exactly; the fixtures test re-asserts the integer invariant so the oracle
// cannot drift silently.
