package benchmark

import "testing"

func TestOracleMatchesIndependentCounts(t *testing.T) {
	facts := tinyFacts(t)

	// Filter oracle: counted directly here, not via the oracle function.
	want := 0
	for _, p := range facts.Patients {
		if p.Active && p.Gender == "female" {
			want++
		}
	}
	bag, err := OracleFor(WorkloadFilterActiveGender, facts)
	if err != nil || bag == nil {
		t.Fatalf("filter oracle: %v", err)
	}
	if len(*bag) != want {
		t.Fatalf("filter oracle rows=%d, independent count=%d", len(*bag), want)
	}
	if want == 0 {
		t.Fatal("tiny dataset must exercise the filter non-vacuously")
	}
}

func TestOracleAggregateMatchesFactsAggregates(t *testing.T) {
	facts := tinyFacts(t)
	bag, err := OracleFor(WorkloadAggregateSumAgeByPatient, facts)
	if err != nil || bag == nil {
		t.Fatal(err)
	}
	// The oracle bag must have exactly one row per patient with age values.
	rows := 0
	for _, n := range facts.CountByPatient {
		if n > 0 {
			rows++
		}
	}
	// Some patients may have all ages missing (p%19==0), so compare to the
	// set of patients with at least one non-nil age observation.
	withAge := map[string]bool{}
	for _, o := range facts.Observations {
		if o.Code == ObservationCodeAge && o.ValueNumber != nil {
			withAge[o.PatientID] = true
		}
	}
	if len(*bag) != len(withAge) {
		t.Fatalf("aggregate oracle rows=%d, patients-with-age=%d", len(*bag), len(withAge))
	}
	_ = rows
}

func TestOracleGroupCountMatchesCountByPatient(t *testing.T) {
	facts := tinyFacts(t)
	bag, err := OracleFor(WorkloadGroupCountByPatient, facts)
	if err != nil || bag == nil {
		t.Fatal(err)
	}
	if len(*bag) != len(facts.CountByPatient) {
		t.Fatalf("group oracle rows=%d, facts.CountByPatient=%d", len(*bag), len(facts.CountByPatient))
	}
}

func TestOracleStageCountsMatchFacts(t *testing.T) {
	facts := tinyFacts(t)
	bag, err := OracleFor(WorkloadStageCounts, facts)
	if err != nil || bag == nil {
		t.Fatal(err)
	}
	if len(*bag) != len(facts.StageCounts) {
		t.Fatalf("stage oracle rows=%d, facts.StageCounts=%d", len(*bag), len(facts.StageCounts))
	}
}

func TestOracleNullHandlingEmitsNullNotMissing(t *testing.T) {
	facts := tinyFacts(t)
	bag, err := OracleFor(WorkloadConditionNullHandling, facts)
	if err != nil || bag == nil {
		t.Fatal(err)
	}
	if len(*bag) != len(facts.Conditions) {
		t.Fatal("every condition must appear exactly once")
	}
	// The oracle row object carries the key with a nil value: canonicalized
	// to CNull, and must NOT equal the same row with the key dropped.
	rows, _ := oracleRows(WorkloadConditionNullHandling, facts)
	dropped := make([]map[string]any, len(rows))
	for i, r := range rows {
		c := map[string]any{}
		for k, v := range r {
			if v != nil {
				c[k] = v
			}
		}
		dropped[i] = c
	}
	if Bag(CanonicalizeRows([]string{"condition_id", "verification_status"}, rows)).Digest() ==
		Bag(CanonicalizeRows([]string{"condition_id", "verification_status"}, dropped)).Digest() {
		t.Fatal("oracle must keep null distinct from missing")
	}
}

func TestOracleSortLimitTotalOrderTopN(t *testing.T) {
	facts := tinyFacts(t)
	rows, _ := oracleRows(WorkloadSortLimitTopN, facts)
	active := 0
	for _, p := range facts.Patients {
		if p.Active {
			active++
		}
	}
	want := active
	if want > TopNLimit {
		want = TopNLimit
	}
	if len(rows) != want {
		t.Fatalf("topN rows=%d, want=%d", len(rows), want)
	}
	for i := 1; i < len(rows); i++ {
		key := func(r map[string]any) (string, string, string) {
			f, _ := r["family"].(string)
			g, _ := r["given"].(string)
			id, _ := r["patient_id"].(string)
			return f, g, id
		}
		f1, g1, id1 := key(rows[i-1])
		f2, g2, id2 := key(rows[i])
		if f1 > f2 || (f1 == f2 && g1 > g2) || (f1 == f2 && g1 == g2 && id1 >= id2) {
			t.Fatalf("sort-limit oracle order broken at %d: %v then %v", i, rows[i-1], rows[i])
		}
	}
}

func TestOracleDeriveAgeBandEveryAgeObsBanded(t *testing.T) {
	facts := tinyFacts(t)
	rows, _ := oracleRows(WorkloadDeriveAgeBand, facts)
	want := 0
	for _, o := range facts.Observations {
		if o.Code == ObservationCodeAge && o.ValueNumber != nil {
			want++
		}
	}
	if len(rows) != want {
		t.Fatalf("age-band rows=%d, want=%d", len(rows), want)
	}
	seen := map[string]bool{}
	for _, r := range rows {
		id, _ := r["observation_id"].(string)
		band, _ := r["age_band"].(string)
		if id == "" || seen[id] {
			t.Fatalf("observation duplicated or unnamed: %v", r)
		}
		seen[id] = true
		switch band {
		case "young", "middle", "senior":
		default:
			t.Fatalf("unexpected band %q", band)
		}
	}
}

func TestOracleStudyCorrelationCoversAllPatients(t *testing.T) {
	facts := tinyFacts(t)
	rows, _ := oracleRows(WorkloadStudyCorrelation, facts)
	if len(rows) != len(facts.Patients) {
		t.Fatal("one row per patient expected")
	}
	studies := map[string]bool{}
	for _, s := range facts.StudyIDs {
		studies["ResearchStudy/"+s] = true
	}
	for _, r := range rows {
		ref, _ := r["study_ref"].(string)
		if !studies[ref] {
			t.Fatalf("study_ref %q not in study ids", ref)
		}
	}
}

func TestOracleUnknownWorkloadHasNoOracle(t *testing.T) {
	bag, err := OracleFor("does-not-exist", tinyFacts(t))
	if err != nil || bag != nil {
		t.Fatalf("unknown workload must yield (nil, nil): %v %v", bag, err)
	}
}
