package benchmark

import (
	"fmt"
	"sort"
)

// The matched workload battery. Every workload runs on BOTH backends against
// the same logical dataset. Coverage families (from the plan): filter,
// projection/derive, sort+limit, group+aggregate, distinct over duplicate
// contributors, reference correlation, null-vs-missing handling, and a
// scoped-auth read. Categories are closed so a report cannot quietly grow a
// family-less entry.

const (
	WorkloadFilterActiveGender       = "filter-active-gender"
	WorkloadDeriveAgeBand            = "derive-age-band"
	WorkloadAggregateSumAgeByPatient = "aggregate-sum-age-by-patient"
	WorkloadGroupCountByPatient      = "group-count-by-patient"
	WorkloadStageCounts              = "stage-counts"
	WorkloadSpecimenTypeFilter       = "specimen-type-filter"
	WorkloadConditionNullHandling    = "condition-null-handling"
	WorkloadSortLimitTopN            = "sort-limit-topn"
	WorkloadDistinctFamily           = "distinct-family"
	WorkloadStudyCorrelation         = "study-correlation"
)

// WorkloadCategory is the closed coverage family label.
type WorkloadCategory string

const (
	CategoryFilter      WorkloadCategory = "filter"
	CategoryDerive      WorkloadCategory = "derive"
	CategoryAggregate   WorkloadCategory = "aggregate"
	CategoryDistinct    WorkloadCategory = "distinct"
	CategoryCorrelation WorkloadCategory = "correlation"
	CategoryNullMissing WorkloadCategory = "null-missing"
	CategorySortLimit   WorkloadCategory = "sort-limit"
)

// WorkloadSpec is the authored contract for one battery entry. Columns is
// the public column order BOTH arms must publish (GateColumnContract).
// ExpectedComplete=false is legal only for explicitly truncated preview
// checks, which the battery does not use for claimable rows.
type WorkloadSpec struct {
	ID              string           `json:"id"`
	Category        WorkloadCategory `json:"category"`
	Columns         []string         `json:"columns"`
	OracleAvailable bool             `json:"oracleAvailable"`
	AuthScoped      bool             `json:"authScoped"`
	Description     string           `json:"description"`
}

// Battery is the closed matrix definition set. Adding an entry requires the
// oracle (tiny preset), the workspace recipe in the harness, and a category.
var Battery = []WorkloadSpec{
	{ID: WorkloadFilterActiveGender, Category: CategoryFilter,
		Columns: []string{"patient_id", "gender"}, OracleAvailable: true,
		Description: "active patients with gender=female"},
	{ID: WorkloadDeriveAgeBand, Category: CategoryDerive,
		Columns: []string{"observation_id", "age_band"}, OracleAvailable: true,
		Description: "closed category-recode derivation of an age band from BENCH-CODE-AGE identifiers over [1,90] days"},
	{ID: WorkloadAggregateSumAgeByPatient, Category: CategoryAggregate,
		Columns: []string{"patient_id", "sum_age", "count_age", "min_age", "max_age"}, OracleAvailable: true,
		Description: "sum/count/min/max of age quantities grouped by patient"},
	{ID: WorkloadGroupCountByPatient, Category: CategoryAggregate,
		Columns: []string{"patient_id", "obs_count"}, OracleAvailable: true,
		Description: "observation count per patient"},
	{ID: WorkloadStageCounts, Category: CategoryAggregate,
		Columns: []string{"stage_code", "stage_count"}, OracleAvailable: true,
		Description: "counts by BENCH-CODE-STAGE codeable concept"},
	{ID: WorkloadSpecimenTypeFilter, Category: CategoryFilter,
		Columns: []string{"specimen_id", "patient_id", "type_code"}, OracleAvailable: true,
		Description: "specimens with type PRIMARY"},
	{ID: WorkloadConditionNullHandling, Category: CategoryNullMissing,
		Columns: []string{"condition_id", "verification_status"}, OracleAvailable: true,
		Description: "verification status where the source may omit the field entirely (null vs missing)"},
	{ID: WorkloadSortLimitTopN, Category: CategorySortLimit,
		Columns: []string{"family", "given", "patient_id"}, OracleAvailable: true,
		Description: fmt.Sprintf("top-%d active patients by family, given, id (total order, ties broken by id)", TopNLimit)},
	{ID: WorkloadDistinctFamily, Category: CategoryDistinct,
		Columns: []string{"family"}, OracleAvailable: true,
		Description: "distinct family names over deliberately duplicated contributors"},
	{ID: WorkloadStudyCorrelation, Category: CategoryCorrelation,
		Columns: []string{"patient_id", "study_ref"}, OracleAvailable: true,
		Description: "patient rows correlated with their ResearchStudy reference"},
}

// workloadColumns resolves the authored column contract.
func workloadColumns(workloadID string) ([]string, bool) {
	for _, spec := range Battery {
		if spec.ID == workloadID {
			return append([]string(nil), spec.Columns...), true
		}
	}
	return nil, false
}

// SpecsByPreset returns the battery entries eligible for a dataset preset.
// All presets run the full battery; representative drops nothing — it only
// drops the hand oracle scale, which OracleFor already handles by profile
// through the caller. This indirection exists so a future preset-specific
// exclusion is a single auditable edit.
func SpecsByPreset(preset string) ([]WorkloadSpec, error) {
	switch preset {
	case PresetTiny, PresetSmall, PresetRepresentative:
		out := make([]WorkloadSpec, 0, len(Battery))
		for _, spec := range Battery {
			out = append(out, spec)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("benchmark: unknown preset %q", preset)
	}
}

// ValidateBattery is called at harness start and from tests: an entry with
// duplicate ids, empty columns, an unknown category, or an oracle-less entry
// that should have one (all tiny-eligible entries must declare oracle
// availability or explicitly live without one) is a config bug, not a
// runtime surprise.
func ValidateBattery() error {
	seen := map[string]bool{}
	categories := map[WorkloadCategory]int{}
	for _, spec := range Battery {
		if spec.ID == "" {
			return fmt.Errorf("benchmark: battery entry with empty id")
		}
		if seen[spec.ID] {
			return fmt.Errorf("benchmark: duplicate workload id %q", spec.ID)
		}
		seen[spec.ID] = true
		if len(spec.Columns) == 0 {
			return fmt.Errorf("benchmark: workload %q has no column contract", spec.ID)
		}
		if hasDuplicates(spec.Columns) {
			return fmt.Errorf("benchmark: workload %q has duplicate columns", spec.ID)
		}
		switch spec.Category {
		case CategoryFilter, CategoryDerive, CategoryAggregate, CategoryDistinct,
			CategoryCorrelation, CategoryNullMissing, CategorySortLimit:
			categories[spec.Category]++
		default:
			return fmt.Errorf("benchmark: workload %q has unknown category %q", spec.ID, spec.Category)
		}
		// Oracle presence must agree with the oracle implementation.
		if _, implemented := oracleRows(spec.ID, &Facts{}); implemented != spec.OracleAvailable {
			return fmt.Errorf("benchmark: workload %q declares OracleAvailable=%v but implementation says %v",
				spec.ID, spec.OracleAvailable, implemented)
		}
	}
	for _, category := range []WorkloadCategory{CategoryFilter, CategoryDerive, CategoryAggregate,
		CategoryDistinct, CategoryCorrelation, CategoryNullMissing, CategorySortLimit} {
		if categories[category] == 0 {
			return fmt.Errorf("benchmark: coverage family %q has no battery entries", category)
		}
	}
	return nil
}

// OracleBackedCount reports how many entries have independent oracles, for
// the report's honesty line.
func OracleBackedCount() int {
	count := 0
	for _, spec := range Battery {
		if spec.OracleAvailable {
			count++
		}
	}
	return count
}

func hasDuplicates(values []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return true
		}
		seen[v] = true
	}
	return false
}

// SortedBattery returns entries by id for deterministic report order.
func SortedBattery() []WorkloadSpec {
	out := append([]WorkloadSpec(nil), Battery...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
