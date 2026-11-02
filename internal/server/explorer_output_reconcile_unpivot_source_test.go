package server

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func unpivotSourceReconcileFixture(t *testing.T) (authoringv2.Document, lower.CompiledRecipeOutput, capability.Snapshot, authoringv2.ConstructionSourceProjection) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_birth", NodeID: "n_patient", ResourceType: "Patient",
		FieldPath: "multipleBirthInteger", Label: "Birth count", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 135,
	})
	facts, ok := lifecycle.ConstructionUnpivotSourceFacts(snapshot, "Patient", "multipleBirthInteger")
	if !ok {
		t.Fatal("the authorized snapshot does not admit Patient.multipleBirthInteger as an unpivot source")
	}
	projection := facts
	projection.ColumnID = "unpivot-col-birth"
	projection.OwnerStepID = "melt_step"
	projectionName := authoringv2.ConstructionSourceProjectionName(projection.ColumnID)
	keyScalar := func(value string) authoringv2.TableScalar {
		kind := authoringv2.TableScalarString
		return authoringv2.TableScalar{Kind: kind, String: &value}
	}
	document := authoringv2.Document{
		Output:           authoringv2.Output{ID: "patients"},
		RootResourceType: "Patient",
		Columns:          []authoringv2.Column{{ColumnID: "patient-col", Column: "patient_id"}},
		Construction: &authoringv2.Construction{
			Version:           authoringv2.ConstructionVersion,
			SourceProjections: []authoringv2.ConstructionSourceProjection{projection},
			Steps: []authoringv2.ConstructionStep{
				{
					ID: "derive_step",
					Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationDerive, Derive: &authoringv2.ConstructionDerive{
						ConstructionID: "derive_step", OutputColumnID: "derived-total", Operation: authoringv2.ConstructionDerivedAdd,
						Left:               authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger}},
						Right:              authoringv2.ConstructionOperand{Kind: authoringv2.ConstructionLiteralOperand, Literal: &authoringv2.ConstructionLiteral{Kind: authoringv2.ConstructionNumericInteger}},
						MissingInputPolicy: authoringv2.ConstructionMissingInputPropagateNull,
					}},
					Outputs: []authoringv2.StageColumn{
						{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
						{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, Type: projection.LogicalType},
						{ID: "derived-total", Name: "derived_total", Label: "Derived total", Type: "integer"},
					},
				},
				{
					ID: "melt_step",
					Operation: authoringv2.ConstructionOperation{Kind: authoringv2.ConstructionOperationUnpivot, Unpivot: &authoringv2.ConstructionUnpivot{
						ConstructionID:      "melt_step",
						Inputs:              []authoringv2.ConstructionUnpivotInput{{ColumnID: projection.ColumnID, Key: keyScalar("variant-a")}, {ColumnID: "derived-total", Key: keyScalar("variant-b")}},
						KeyOutputColumnID:   "unpivot-key",
						ValueOutputColumnID: "unpivot-value",
						NullRowPolicy:       authoringv2.ConstructionUnpivotDrop,
					}},
					Outputs: []authoringv2.StageColumn{
						{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
						{ID: "unpivot-key", Name: "unpivot_key", Label: "Variant", Type: "string"},
						{ID: "unpivot-value", Name: "unpivot_value", Label: "Value", Type: "integer"},
					},
				},
			},
		},
	}
	compiled := lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{{
		ID: recipe.ConstructionSourceProjectionID,
		Columns: []lower.CompiledOutputColumn{
			{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Kind: "string", Cardinality: "optional_one"},
			{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, SemanticPath: projection.FieldPath, Kind: projection.LogicalType, Cardinality: "optional_one"},
		},
	}}}
	return document, compiled, snapshot, projection
}

func TestUnpivotSourceMeltClaimsUnselectedFieldQualityWithoutAuthoringTheSlot(t *testing.T) {
	document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
	authored := make(map[string]authoredOutputColumn)
	if err := authoredConstructionOutputsWithCombineInputs(document, authored, compiled, nil, snapshot); err != nil {
		t.Fatalf("reconcile unpivot-owned projection: %v", err)
	}
	slotName := authoringv2.ConstructionSourceProjectionName(projection.ColumnID)
	if _, claimed := authored[slotName]; claimed {
		t.Fatalf("a consumed melt slot was claimed as an authored output: %#v", authored[slotName])
	}
	for _, name := range []string{"unpivot_key", "unpivot_value"} {
		claimed, ok := authored[name]
		if !ok {
			t.Fatalf("melt output %q missing from the ledger: %#v", name, authored)
		}
		found := false
		for _, reason := range claimed.Quality.LossReasons {
			if reason == "CONSTRUCTION_UNPIVOT_SOURCE_ROWS_UNPIVOTED_FROM_UNSELECTED_FIELD" {
				found = true
			}
		}
		if !found {
			t.Fatalf("melt output %q omits the unselected-field melt reason: %v", name, claimed.Quality.LossReasons)
		}
	}
}

func TestUnpivotSourceProjectionReconcileRefusesDriftedClaims(t *testing.T) {
	t.Run("compiled slot no longer matches the durable claim", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		compiled.Stages[0].Columns[1].SemanticPath = "active"
		err := authoredConstructionOutputsWithCombineInputs(document, make(map[string]authoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "does not carry the compiled source column identity") {
			t.Fatalf("drifted compiled slot error = %v", err)
		}
	})

	t.Run("claim needs an authorized capability snapshot", func(t *testing.T) {
		document, compiled, _, _ := unpivotSourceReconcileFixture(t)
		err := authoredConstructionOutputsWithCombineInputs(document, make(map[string]authoredOutputColumn), compiled, nil, capability.Snapshot{})
		if err == nil || !strings.Contains(err.Error(), "without an authorized capability snapshot") {
			t.Fatalf("unarmed snapshot error = %v", err)
		}
	})

	t.Run("claim no longer matches the authorized derivation", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].FieldPath = "gender"
		compiled.Stages[0].Columns[1].SemanticPath = "gender"
		err := authoredConstructionOutputsWithCombineInputs(document, make(map[string]authoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("drifted claim error = %v", err)
		}
	})

	// Fact-branch exclusivity (finding F-2): the compiled-slot arm mirrors the
	// claim's Label, so keeping the compiled column in sync leaves ONLY the
	// durable-claim versus re-derived-facts comparison to catch a drifted
	// Label. Mutation-proven: removing the facts DeepEquals must kill this.
	t.Run("claim label drifted while the compiled slot mirrors it", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].Label = "TAMPERED-LABEL"
		compiled.Stages[0].Columns[1].Label = document.Construction.SourceProjections[0].Label
		err := authoredConstructionOutputsWithCombineInputs(document, make(map[string]authoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("mirrored tampered label error = %v, want the facts-branch refusal", err)
		}
	})

	// The compiled-slot arm checks neither FHIRType nor OccurrenceID, so this
	// kind of drift is reachable only through the facts comparison.
	t.Run("claim fhir type drifted from the rederived schema facts", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].FHIRType = "TAMPERED-TYPE"
		err := authoredConstructionOutputsWithCombineInputs(document, make(map[string]authoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("tampered fhirType error = %v, want the facts-branch refusal", err)
		}
	})

	t.Run("owner step must melt the projection as an input", func(t *testing.T) {
		document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
		for index := range document.Construction.Steps[1].Operation.Unpivot.Inputs {
			if document.Construction.Steps[1].Operation.Unpivot.Inputs[index].ColumnID == projection.ColumnID {
				document.Construction.Steps[1].Operation.Unpivot.Inputs[index].ColumnID = "derived-total"
			}
		}
		err := authoredConstructionOutputsWithCombineInputs(document, make(map[string]authoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "it does not melt as an input") {
			t.Fatalf("unconsumed melt input error = %v", err)
		}
	})

	t.Run("carrier step must declare the slot it carries", func(t *testing.T) {
		document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
		carrier := &document.Construction.Steps[0]
		kept := make([]authoringv2.StageColumn, 0, len(carrier.Outputs))
		for _, output := range carrier.Outputs {
			if output.ID != projection.ColumnID {
				kept = append(kept, output)
			}
		}
		carrier.Outputs = kept
		err := authoredConstructionOutputsWithCombineInputs(document, make(map[string]authoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "missing a declared stable ID, name, or label") {
			t.Fatalf("missing carrier declaration error = %v", err)
		}
	})
}
