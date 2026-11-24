package ir

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/unit"
)

func TestCloneUnitIdentityCopiesValueAndPreservesNil(t *testing.T) {
	if CloneUnitIdentity(nil) != nil {
		t.Fatal("CloneUnitIdentity(nil) is not nil")
	}

	source := &unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	clone := CloneUnitIdentity(source)
	if clone == source || *clone != *source {
		t.Fatalf("clone = %p %+v, source = %p %+v", clone, clone, source, source)
	}
	source.Code = "m"
	if clone.Code != "cm" {
		t.Fatalf("source mutation changed cloned unit identity: %+v", clone)
	}
}
