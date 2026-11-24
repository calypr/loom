package main

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/viewer"
)

func TestVerifyExplorerContract(t *testing.T) {
	state := viewer.ExplorerStateV1{
		Management: viewer.ManagementInteractive,
		Runtime: &viewer.ExplorerRuntimeV1{
			Generation: "demo-generation",
			Outputs: []viewer.ExplorerRuntimeOutputV1{{
				OutputID: "patients",
				Title:    "Patient cohort",
				Columns: []viewer.ExplorerRuntimeColumnV1{
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
	state := viewer.ExplorerStateV1{
		Management: viewer.ManagementRepository,
		Runtime: &viewer.ExplorerRuntimeV1{
			Generation: "repo-generation",
			Outputs: []viewer.ExplorerRuntimeOutputV1{{
				OutputID: "patients",
				Title:    "Repository output",
				Columns:  []viewer.ExplorerRuntimeColumnV1{{Column: "identifier_system"}},
			}},
		},
	}
	want := smokeExpectation{Management: "INTERACTIVE", Generation: "demo-generation", OutputID: "patients", OutputTitle: "Patient cohort", Columns: []string{"patient_id"}}
	if err := verifyExplorerContract(state, want); err == nil {
		t.Fatal("verifyExplorerContract succeeded for repository state")
	}
}
