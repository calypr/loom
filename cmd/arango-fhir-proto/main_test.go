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
