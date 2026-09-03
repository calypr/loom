package main

import (
	"testing"

	"github.com/calypr/loom/internal/explorer"
)

func TestVerifyExplorerContract(t *testing.T) {
	state := explorer.ExplorerStateV1{
		Management: explorer.ManagementInteractive,
		Runtime: &explorer.ExplorerRuntimeV1{
			Generation: "demo-generation",
			Outputs: []explorer.ExplorerRuntimeOutputV1{{
				OutputID: "patients",
				Title:    "Patient cohort",
				Columns: []explorer.ExplorerRuntimeColumnV1{
					{Column: "patient_id"},
					{Column: "age"},
				},
			}},
		},
	}
	want := smokeExpectation{Management: "INTERACTIVE", Generation: "demo-generation", OutputID: "patients", OutputTitle: "Patient cohort", Columns: []string{"patient_id", "age"}}
	if err := verifyExplorerContract(state, want); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyExplorerContractRejectsWrongDataset(t *testing.T) {
	state := explorer.ExplorerStateV1{
		Management: explorer.ManagementRepository,
		Runtime: &explorer.ExplorerRuntimeV1{
			Generation: "repo-generation",
			Outputs: []explorer.ExplorerRuntimeOutputV1{{
				OutputID: "patients",
				Title:    "Repository output",
				Columns:  []explorer.ExplorerRuntimeColumnV1{{Column: "identifier_system"}},
			}},
		},
	}
	want := smokeExpectation{Management: "INTERACTIVE", Generation: "demo-generation", OutputID: "patients", OutputTitle: "Patient cohort", Columns: []string{"patient_id"}}
	if err := verifyExplorerContract(state, want); err == nil {
		t.Fatal("verifyExplorerContract succeeded for repository state")
	}
}
