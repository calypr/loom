package catalog

import (
	"reflect"
	"testing"
)

func TestNewAvailableColumnWitnessCarriesGenerationScopeAndStableRouteIdentity(t *testing.T) {
	build := NewAvailableColumnWitnessBuild("project-a", "generation-7", "Patient", "snapshot-a")
	build.State = AvailableColumnWitnessComplete
	build.Exhaustive = true
	build.AllRoots = true
	build.Unrestricted = true
	witness := AvailabilityWitness{
		Feature: AvailabilityFeature{
			Kind:         "FIELD",
			ResourceType: "Observation",
			FieldPath:    "valueQuantity.value",
		},
		RootID:   "Patient/root-1",
		SourceID: "Observation/source-1",
		Route: []AvailabilityRelation{{
			FromResourceType: "Patient",
			ToResourceType:   "Observation",
			Relationship:     "HAS_OBSERVATION",
			StorageDirection: "OUTBOUND",
		}},
	}

	first, err := NewAvailableColumnWitness(build, witness)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewAvailableColumnWitness(build, witness)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("repeated witness identity changed: first=%#v second=%#v", first, second)
	}
	if first.Key == "" || first.Project != build.Project || first.DatasetGeneration != build.DatasetGeneration ||
		first.RootResourceType != build.RootResourceType || first.InputDigest != build.InputDigest || first.SourceResourceType != "Observation" ||
		first.Source.FieldPath != witness.Feature.FieldPath || first.SourceWitnessID != witness.SourceID || first.RootWitnessID != witness.RootID {
		t.Fatalf("stored witness lost scope or source identity: %#v", first)
	}
	if err := ValidateAvailableColumnWitness(first); err != nil {
		t.Fatalf("validate constructed witness: %v", err)
	}

	changedRoute := witness
	changedRoute.Route = []AvailabilityRelation{{
		FromResourceType: "Patient",
		ToResourceType:   "Observation",
		Relationship:     "HAS_DIFFERENT_OBSERVATION",
		StorageDirection: "OUTBOUND",
	}}
	third, err := NewAvailableColumnWitness(build, changedRoute)
	if err != nil {
		t.Fatal(err)
	}
	if third.Key == first.Key {
		t.Fatalf("different route shared stable key %q", first.Key)
	}

	changedDigest := NewAvailableColumnWitnessBuild("project-a", "generation-7", "Patient", "snapshot-b")
	changedDigest.State = AvailableColumnWitnessComplete
	changedDigest.Exhaustive = true
	changedDigest.AllRoots = true
	changedDigest.Unrestricted = true
	if changedDigest.Key == build.Key {
		t.Fatalf("different input digest shared build key %q", build.Key)
	}
	fourth, err := NewAvailableColumnWitness(changedDigest, witness)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.Key == first.Key {
		t.Fatalf("different source/rule digest shared stable key %q", first.Key)
	}
}

func TestAvailableColumnWitnessBuildRequiresExhaustiveCompletion(t *testing.T) {
	build := NewAvailableColumnWitnessBuild("project-a", "generation-7", "Patient", "snapshot-a")
	if build.State != AvailableColumnWitnessBuilding || build.SchemaVersion != AvailableColumnWitnessSchemaVersion || build.Key == "" {
		t.Fatalf("new build marker = %#v", build)
	}
	if err := ValidateAvailableColumnWitnessBuild(build); err != nil {
		t.Fatalf("validate building marker: %v", err)
	}

	build.State = AvailableColumnWitnessComplete
	if err := ValidateAvailableColumnWitnessBuild(build); err == nil {
		t.Fatal("non-exhaustive build was accepted as complete")
	}
	build.Exhaustive = true
	if err := ValidateAvailableColumnWitnessBuild(build); err == nil {
		t.Fatal("restricted build was accepted as a complete all-roots proof")
	}
	build.AllRoots = true
	build.Unrestricted = true
	if err := ValidateAvailableColumnWitnessBuild(build); err != nil {
		t.Fatalf("validate exhaustive complete marker: %v", err)
	}
	build.Unrestricted = false
	if err := ValidateAvailableColumnWitnessBuild(build); err == nil {
		t.Fatal("restricted build was accepted as an unrestricted complete proof")
	}
	build.State = AvailableColumnWitnessFailed
	if err := ValidateAvailableColumnWitnessBuild(build); err != nil {
		t.Fatalf("validate failed marker: %v", err)
	}
}

func TestNewAvailableColumnWitnessRejectsDisconnectedRouteAndWrongWitnessIDs(t *testing.T) {
	build := NewAvailableColumnWitnessBuild("project-a", "generation-7", "Patient", "snapshot-a")
	base := AvailabilityWitness{
		Feature: AvailabilityFeature{Kind: "FIELD", ResourceType: "Observation", FieldPath: "status"},
		RootID:  "Patient/root-1", SourceID: "Observation/source-1",
		Route: []AvailabilityRelation{{
			FromResourceType: "Specimen",
			ToResourceType:   "Observation",
			Relationship:     "HAS_OBSERVATION",
			StorageDirection: "OUTBOUND",
		}},
	}
	if _, err := NewAvailableColumnWitness(build, base); err == nil {
		t.Fatal("disconnected route was accepted")
	}

	base.Route[0].FromResourceType = "Patient"
	base.RootID = "Observation/root-1"
	if _, err := NewAvailableColumnWitness(build, base); err == nil {
		t.Fatal("root witness ID for a different resource type was accepted")
	}
}
