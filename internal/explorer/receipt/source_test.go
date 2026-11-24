package receipt

import (
	"reflect"
	"strings"
	"testing"

	ws "github.com/calypr/loom/internal/explorer/workspace"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/rowsource"
)

func deriveSourceReconcileFixture(t *testing.T) (ws.Document, facade.CompiledRecipeOutput, capability.Snapshot, ws.ConstructionSourceProjection) {
	t.Helper()
	snapshot := testAuthoringV2CapabilitySnapshot()
	snapshot.Candidates = append(snapshot.Candidates, capability.Candidate{
		ID: "c_patient_birth", NodeID: "n_patient", ResourceType: "Patient",
		FieldPath: "multipleBirthInteger", Label: "Birth count", LogicalType: "integer", Cardinality: "OPTIONAL_ONE",
		ProjectionModes:     []capability.ProjectionMode{capability.ProjectionScalar},
		SupportedOperations: []capability.Operation{capability.OperationSelect},
		Observed:            true, Populated: true, ObservedDocumentCount: 135,
	})
	facts, ok := rowsource.ConstructionDeriveSourceFacts(snapshot, "Patient", "multipleBirthInteger")
	if !ok {
		t.Fatal("the authorized snapshot does not admit Patient.multipleBirthInteger as a derive source")
	}
	projection := facts
	projection.ColumnID = "derive-col-birth"
	projection.OwnerStepID = "derive_step"
	three := int64(3)
	projectionName := ws.ConstructionSourceProjectionName(projection.ColumnID)
	document := ws.Document{
		Output:           ws.Output{ID: "patients"},
		RootResourceType: "Patient",
		Columns:          []ws.Column{{ColumnID: "patient-col", Column: "patient_id"}},
		Construction: &ws.Construction{
			Version:           ws.ConstructionVersion,
			SourceProjections: []ws.ConstructionSourceProjection{projection},
			Steps: []ws.ConstructionStep{{
				ID: "derive_step",
				Operation: ws.ConstructionOperation{Kind: ws.ConstructionOperationDerive, Derive: &ws.ConstructionDerive{
					ConstructionID:     "derive_step",
					OutputColumnID:     "derived-total",
					Operation:          ws.ConstructionDerivedAdd,
					Left:               ws.ConstructionOperand{Kind: ws.ConstructionColumnOperand, ColumnID: projection.ColumnID},
					Right:              ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: &three}},
					MissingInputPolicy: ws.ConstructionMissingInputPropagateNull,
				}},
				Outputs: []ws.StageColumn{
					{ID: "patient-col", Name: "patient_id", Label: "Patient ID", Type: "string"},
					{ID: projection.ColumnID, Name: projectionName, Label: projection.Label, Type: projection.LogicalType},
					{ID: "derived-total", Name: "derived_total", Label: "Derived total", Type: "integer"},
				},
			}},
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

func TestDeriveSourceProjectionOutputClaimsArithmeticLossFromUnselectedField(t *testing.T) {
	document, compiled, snapshot, projection := deriveSourceReconcileFixture(t)
	authored := make(map[string]AuthoredOutputColumn)
	if err := AuthoredConstructionOutputsWithCombineInputs(document, authored, compiled, nil, snapshot); err != nil {
		t.Fatalf("reconcile derive-owned projection: %v", err)
	}
	name := ws.ConstructionSourceProjectionName(projection.ColumnID)
	claimed := authored[name]
	if !claimed.TypedStageOutput || claimed.ConstructionID != projection.ColumnID {
		t.Fatalf("derive-owned projection claim = %+v, want the durable derive step identity", claimed)
	}
	if claimed.Quality.Lossless {
		t.Fatalf("derive-owned projection claimed lossless: %+v", claimed.Quality)
	}
	if claimed.Quality.StructuralSuitability != "requires-review" {
		t.Fatalf("derive-owned projection suitability = %q, want requires-review", claimed.Quality.StructuralSuitability)
	}
	if !reflect.DeepEqual(claimed.Quality.LossReasons, []string{"CONSTRUCTION_DERIVE_SOURCE_ARITHMETIC_FROM_UNSELECTED_FIELD"}) {
		t.Fatalf("derive-owned projection loss reasons = %v, want the unselected-field arithmetic reason", claimed.Quality.LossReasons)
	}
	derived, ok := authored["derived_total"]
	if !ok || derived.ConstructionID != "derive_step" {
		t.Fatalf("derived arithmetic output missing from the ledger: %#v", authored)
	}
}

func TestDeriveSourceProjectionReconcileRefusesDriftedClaims(t *testing.T) {
	t.Run("compiled slot no longer matches the durable claim", func(t *testing.T) {
		document, compiled, snapshot, projection := deriveSourceReconcileFixture(t)
		compiled.Stages[0].Columns[1].SemanticPath = "active"
		_ = projection
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "does not carry the compiled source column identity") {
			t.Fatalf("drifted compiled slot error = %v", err)
		}
	})

	t.Run("claim needs an authorized capability snapshot", func(t *testing.T) {
		document, compiled, _, _ := deriveSourceReconcileFixture(t)
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, capability.Snapshot{})
		if err == nil || !strings.Contains(err.Error(), "without an authorized capability snapshot") {
			t.Fatalf("unarmed snapshot error = %v", err)
		}
	})

	t.Run("claim no longer matches the authorized derivation", func(t *testing.T) {
		document, compiled, snapshot, projection := deriveSourceReconcileFixture(t)
		document.Construction.SourceProjections[0].FieldPath = "gender"
		compiled.Stages[0].Columns[1].SemanticPath = "gender"
		_ = projection
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "no longer matches the authorized derive source derivation") {
			t.Fatalf("foreign field error = %v", err)
		}
	})

	t.Run("projection the derive step does not consume", func(t *testing.T) {
		document, compiled, snapshot, _ := deriveSourceReconcileFixture(t)
		document.Construction.Steps[0].Operation.Derive.Left = ws.ConstructionOperand{Kind: ws.ConstructionLiteralOperand, Literal: &ws.ConstructionLiteral{Kind: ws.ConstructionNumericInteger, Integer: func() *int64 { v := int64(1); return &v }()}}
		err := AuthoredConstructionOutputsWithCombineInputs(document, make(map[string]AuthoredOutputColumn), compiled, nil, snapshot)
		if err == nil || !strings.Contains(err.Error(), "it does not consume") {
			t.Fatalf("unconsumed projection error = %v", err)
		}
	})
}
