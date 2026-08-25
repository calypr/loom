package arango

import "testing"

func TestCollectionSpecsArePersistentAndIndexed(t *testing.T) {
	s := CollectionSpecs()
	if len(s) != 5 {
		t.Fatalf("specs=%#v", s)
	}
	for _, spec := range s {
		if spec.Truncate {
			t.Fatalf("%s truncates persisted Explorer state", spec.Name)
		}
		if len(spec.Indexes) == 0 {
			t.Fatalf("%s has no indexes", spec.Name)
		}
	}
}
