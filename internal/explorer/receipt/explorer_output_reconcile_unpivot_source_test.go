package receipt

import (
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func unpivotSourceReconcileFixture(t *testing.T) (ws.Document, facade.CompiledRecipeOutput, capability.Snapshot, ws.ConstructionSourceProjection) {
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
	projectionName := ws.ConstructionSourceProjectionName(projection.ColumnID)
	keyScalar := func(value string) ws.TableScalar {
		kind := ws.TableScalarString
		return ws.TableScalar{Kind: kind, String: &value}
	}
	document := ws.Document{
		Output:           ws.Output{ID: "patients"},
		RootResourceType: "Patient",
		Columns:          []ws.Column{{ColumnID: "patient-col", Column: "patient_id"}},
		Construction: &ws.Construction{
			Version:           ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{projection},
			Steps: []ws.ConstructionStep{
				{
					ID: "derive_step",
					Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
						ConstructionID: "derive_step", OutputColumnID: "derived-total", Operation: ws.ConstructionDerivedAdd,
						Left:               ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger}},
						Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger}},
						MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
					}},
					Outputs: []ws.StageColumn{
						{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
						{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, Type: projection.LogicalType},
						{ID: "derived-total", Name: "derived_total", Label: "Derived total", Type: "integer"},
					},
				},
				{
					ID: "melt_step",
					Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
						ConstructionID:      "melt_step",
						Inputs:              []ws.ConstructionUnpivotInput{{ColumnID: projection.ColumnID, Key: keyScalar("variant-a")}, {ColumnID: "derived-total", Key: keyScalar("variant-b")}},
						KeyOutputColumnID:   "unpivot-key",
						ValueOutputColumnID: "unpivot-value",
						NullRowPolicy:       ws.ConstructionUnpivotDrop,
					}},
					Outputs: []ws.StageColumn{
						{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
						{ID: "unpivot-key", Name: "unpivot_key", Label: "Variant", Type: "string"},
						{ID: "unpivot-value", Name: "unpivot_value", Label: "Value", Type: "integer"},
					},
				},
			},
		},
	}
	compiled := facade.CompiledRecipeOutput{Stages: []facade.CompiledStageDescriptor{{
		ID: recipe.ConstructionSourceProjectionID,
		Columns: []facade.CompiledOutputColumn{
			{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Kind: "string", Cardinality: "optional_one"},
			{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, SemanticPath: projection.FieldPath, Kind: projection.LogicalType, Cardinality: "optional_one"},
		},
	}}}
	return document, compiled, snapshot, projection
}

func TestUnpivotSourceMeltClaimsUnselectedFieldQualityWithoutAuthoringTheSlot(t *testing.T) {
	document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
	authored := make(map[string]AuthoredOutputColumn)
	if err := AuthoredConstructionOutputsWithCombineInputs(document, authored, compiled, nil, snapshot); err != nil {
		t.Fatalf("reconcile unpivot-owned projection: %v", err)
	}
	slotName := ws.ConstructionSourceProjectionName(projection.ColumnID)
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
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "does not carry the compiled source column identity") {
			t.Fatalf("drifted compiled slot error = %v", err)
		}
	})

	t.Run("claim needs an authorized capability snapshot", func(t *testing.T) {
		document, compiled, _, _ := unpivotSourceReconcileFixture(t)
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, capability.Snapshot{})
		if err == nil || !strings.Contains(err.Error(), "without an authorized capability snapshot") {
			t.Fatalf("unarmed snapshot error = %v", err)
		}
	})

	t.Run("claim no longer matches the authorized derivation", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].FieldPath = "gender"
		compiled.Stages[0].Columns[1].SemanticPath = "gender"
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("drifted claim error = %v", err)
		}
	})

	t.Run("claim label drifted while the compiled slot mirrors it", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].Label = "TAMPERED-LABEL"
		compiled.Stages[0].Columns[1].Label = document.Construction.SourceProjections[0].Label
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("mirrored tampered label error = %v, want the facts-branch refusal", err)
		}
	})

	t.Run("claim fhir type drifted from the rederived schema facts", func(t *testing.T) {
		document, compiled, snapshot, _ := unpivotSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].FHIRType = "TAMPERED-TYPE"
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
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
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "it does not melt as an input") {
			t.Fatalf("unconsumed melt input error = %v", err)
		}
	})

	t.Run("carrier step must declare the slot it carries", func(t *testing.T) {
		document, compiled, snapshot, projection := unpivotSourceReconcileFixture(t)
		carrier := &document.Construction.Steps[0]
		kept := make([]ws.StageColumn, 0, len(carrier.Outputs))
		for _, output := range carrier.Outputs {
			if output.ID != projection.ColumnID {
				kept = append(kept, output)
			}
		}
		carrier.Outputs = kept
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "missing a declared stable ID, name, or label") {
			t.Fatalf("missing carrier declaration error = %v", err)
		}
	})
}

func unpivotMeltFirstReconcileFixture(t *testing.T) (ws.Document, facade.CompiledRecipeOutput, capability.Snapshot) {
	t.Helper()
	base, _, snapshot, projection := unpivotSourceReconcileFixture(t)
	projectionName := ws.ConstructionSourceProjectionName(projection.ColumnID)
	keyScalar := func(value string) ws.TableScalar {
		kind := ws.TableScalarString
		return ws.TableScalar{Kind: kind, String: &value}
	}
	document := ws.Document{
		Output:           ws.Output{ID: "patients"},
		RootResourceType: "Patient",
		Columns: []ws.Column{
			{ColumnID: "patient-col", Column: "patient_id"},
			{ColumnID: "note-col", Column: "note"},
		},
		Construction: &ws.Construction{
			Version:           ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{projection},
			Steps: []ws.ConstructionStep{{
				ID:     "melt_step",
				Inputs: []ws.ConstructionInputRef{{Kind: ws.ConstructionInputSourceProjection}},
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationUnpivot, Unpivot: &ws.ConstructionUnpivot{
					ConstructionID:      "melt_step",
					Inputs:              []ws.ConstructionUnpivotInput{{ColumnID: projection.ColumnID, Key: keyScalar("variant-a")}, {ColumnID: "note-col", Key: keyScalar("variant-b")}},
					KeyOutputColumnID:   "unpivot-key",
					ValueOutputColumnID: "unpivot-value",
					NullRowPolicy:       ws.ConstructionUnpivotDrop,
				}},
				Outputs: []ws.StageColumn{
					{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: "unpivot-key", Name: "unpivot_key", Label: "Variant", Type: "string"},
					{ID: "unpivot-value", Name: "unpivot_value", Label: "Value", Type: "integer"},
				},
			}},
		},
	}
	compiled := facade.CompiledRecipeOutput{Stages: []facade.CompiledStageDescriptor{{
		ID: recipe.ConstructionSourceProjectionID,
		Columns: []facade.CompiledOutputColumn{
			{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Kind: "string", Cardinality: "optional_one"},
			{ID: "note-col", Name: "note", Label: "Note", Kind: "string", Cardinality: "optional_one"},
			{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, SemanticPath: projection.FieldPath, Kind: projection.LogicalType, Cardinality: "optional_one"},
		},
	}}}
	_ = base
	return document, compiled, snapshot
}

func TestUnpivotSourceProjectionReconcileMeltFirst(t *testing.T) {
	t.Run("compiler-materialized first-step slot reconciles", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		if err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot); err != nil {
			t.Fatalf("legit melt-first signed state was refused (pre-fix this is the F-A catch-all): %v", err)
		}
	})

	t.Run("compiled slot identity drift still falls to the declaration refusal", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		compiled.Stages[0].Columns[2].Label = "DRIFTED-COMPILED-LABEL"
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "missing a declared stable ID, name, or label") {
			t.Fatalf("drifted compiled slot label error = %v, want the slot-equality-gated declaration refusal", err)
		}
	})

	t.Run("compiled slot marked identity stays unclaimable", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		compiled.Stages[0].Columns[2].Identity = true
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "missing a declared stable ID, name, or label") {
			t.Fatalf("identity-flagged compiled slot error = %v, want the slot-equality-gated declaration refusal", err)
		}
	})

	t.Run("claim label drift still dies on the facts predicate", func(t *testing.T) {
		document, compiled, snapshot := unpivotMeltFirstReconcileFixture(t)
		document.Construction.SourceProjections[0].Label = "TAMPERED-LABEL"
		compiled.Stages[0].Columns[2].Label = document.Construction.SourceProjections[0].Label
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized unpivot source derivation") {
			t.Fatalf("melt-first mirrored tampered label error = %v, want the facts-branch refusal", err)
		}
	})

	t.Run("melt-first still requires an authorized capability snapshot", func(t *testing.T) {
		document, compiled, _ := unpivotMeltFirstReconcileFixture(t)
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, capability.Snapshot{})
		if err == nil || !strings.Contains(err.Error(), "cannot be claimed without an authorized capability snapshot") {
			t.Fatalf("melt-first unarmed snapshot error = %v, want the authorized-snapshot refusal", err)
		}
	})
}
