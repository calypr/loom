package main

import (
	"flag"
	"strings"
	"testing"
)

func TestParseGenerationLoadWiresImmutableDataset(t *testing.T) {
	config, err := parseLoadCommand([]string{
		"--project", "project-a",
		"--generation", "load:2026-07-11/v1",
		"--meta-dir", "META_SMALL",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseLoadCommand() error = %v", err)
	}
	if config.Options.Truncate {
		t.Fatal("generation load truncate = true, want false")
	}
	if config.Options.Dataset == nil {
		t.Fatal("generation load Dataset = nil, want immutable dataset reference")
	}
	if got, want := config.Options.Dataset.Project, "project-a"; got != want {
		t.Fatalf("generation dataset project = %q, want %q", got, want)
	}
	if got, want := config.Options.Dataset.Generation, "load:2026-07-11/v1"; got != want {
		t.Fatalf("generation dataset generation = %q, want %q", got, want)
	}
	if config.MemoryBytes <= 0 || config.Options.WorkerCount != 2 {
		t.Fatalf("resource defaults = memory %d workers %d, want finite memory and two workers", config.MemoryBytes, config.Options.WorkerCount)
	}
}

func TestParseMemoryBytes(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{input: "4GiB", want: 4 << 30},
		{input: "512Mi", want: 512 << 20},
		{input: "1000", want: 1000},
		{input: "0", want: 0},
	}
	for _, test := range tests {
		got, err := parseMemoryBytes(test.input)
		if err != nil || got != test.want {
			t.Fatalf("parseMemoryBytes(%q) = %d, %v; want %d", test.input, got, err, test.want)
		}
	}
	if _, err := parseMemoryBytes("not-memory"); err == nil {
		t.Fatal("parseMemoryBytes accepted invalid input")
	}
}

func TestParseGenerationLoadRejectsUnsafeOrInvalidInput(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "missing generation",
			args: []string{"--project", "project-a"},
			want: "--generation is required",
		},
		{
			name: "invalid opaque generation",
			args: []string{"--project", "project-a", "--generation", " generation-a"},
			want: "invalid --generation",
		},
		{
			name: "truncate is not exposed",
			args: []string{"--project", "project-a", "--generation", "generation-a", "--truncate=true"},
			want: "flag provided but not defined",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseLoadCommand(test.args, flag.ContinueOnError)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseLoadCommand() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseRepairCommandStagesDistinctImmutableGeneration(t *testing.T) {
	config, err := parseRepairCommand([]string{
		"--project", "project-a",
		"--source-generation", "generation-old",
		"--generation", "generation-new",
		"--meta-dir", "META_SMALL",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseRepairCommand() error = %v", err)
	}
	if config.Load.Options.Dataset == nil || config.Load.Options.Dataset.Generation != "generation-new" {
		t.Fatalf("repair dataset = %#v, want generation-new", config.Load.Options.Dataset)
	}
	if !config.Load.Options.StageOnly {
		t.Fatal("repair command must stage before optional activation")
	}
	if config.Activate {
		t.Fatal("repair command activated without --activate")
	}
}

func TestParseRepairCommandRejectsSameGeneration(t *testing.T) {
	_, err := parseRepairCommand([]string{
		"--project", "project-a",
		"--source-generation", "generation-a",
		"--generation", "generation-a",
	}, flag.ContinueOnError)
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("parseRepairCommand() error = %v, want distinct-generation error", err)
	}
}

func TestParseActivateCommandRequiresValidGeneration(t *testing.T) {
	config, err := parseActivateCommand([]string{
		"--project", "project-a",
		"--generation", "generation-new",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseActivateCommand() error = %v", err)
	}
	if config.Project != "project-a" || config.Generation != "generation-new" {
		t.Fatalf("activation config = %#v", config)
	}
	if _, err := parseActivateCommand([]string{"--project", "project-a"}, flag.ContinueOnError); err == nil || !strings.Contains(err.Error(), "--generation is required") {
		t.Fatalf("missing generation error = %v", err)
	}
}

func TestParseDiscoveryCommandsPassExplicitDatasetGeneration(t *testing.T) {
	fields, _, err := parseDiscoverPopulatedFieldOptions([]string{
		"--project", "project-a",
		"--dataset-generation", "generation-a",
		"--resource-type", "Patient",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseDiscoverPopulatedFieldOptions() error = %v", err)
	}
	if got, want := fields.DatasetGeneration, "generation-a"; got != want {
		t.Fatalf("field discovery generation = %q, want %q", got, want)
	}
	if got, want := fields.ResourceType, "Patient"; got != want {
		t.Fatalf("field discovery resource type = %q, want %q", got, want)
	}

	references, _, err := parseDiscoverPopulatedReferenceOptions([]string{
		"--project", "project-a",
		"--dataset-generation", "generation-a",
		"--from-type", "Specimen",
	}, flag.ContinueOnError)
	if err != nil {
		t.Fatalf("parseDiscoverPopulatedReferenceOptions() error = %v", err)
	}
	if got, want := references.DatasetGeneration, "generation-a"; got != want {
		t.Fatalf("reference discovery generation = %q, want %q", got, want)
	}
	if got, want := references.FromType, "Specimen"; got != want {
		t.Fatalf("reference discovery source type = %q, want %q", got, want)
	}
}
