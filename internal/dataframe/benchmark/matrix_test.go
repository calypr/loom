package benchmark

import "testing"

func TestValidateBattery(t *testing.T) {
	if err := ValidateBattery(); err != nil {
		t.Fatalf("battery invalid: %v", err)
	}
	if OracleBackedCount() != len(Battery) {
		t.Fatalf("every battery entry is expected to carry an independent oracle: %d/%d", OracleBackedCount(), len(Battery))
	}
}

func TestSpecsByPreset(t *testing.T) {
	for _, preset := range []string{PresetTiny, PresetSmall, PresetRepresentative} {
		specs, err := SpecsByPreset(preset)
		if err != nil {
			t.Fatal(err)
		}
		if len(specs) != len(Battery) {
			t.Fatalf("preset %s runs the full battery", preset)
		}
	}
	if _, err := SpecsByPreset("huge"); err == nil {
		t.Fatal("unknown preset must be refused")
	}
}

func TestBatteryColumnContractsUnique(t *testing.T) {
	for _, spec := range Battery {
		if _, ok := workloadColumns(spec.ID); !ok {
			t.Fatalf("workload %q missing column contract", spec.ID)
		}
	}
	if _, ok := workloadColumns("nope"); ok {
		t.Fatal("unknown workload must not resolve")
	}
}
