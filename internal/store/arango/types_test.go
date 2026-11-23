package arango

import (
	"context"
	"reflect"
	"testing"

	driver "github.com/arangodb/go-driver/v2/arangodb"
)

func TestEnsurePersistentIndexSpecsRequiresExactStoredValues(t *testing.T) {
	spec := PersistentIndexSpec{
		Fields:       []string{"project", "dataset_generation", "auth_resource_path", "from_type", "label", "to_type"},
		StoredValues: []string{"source_file"},
	}
	t.Run("accepts exact definition", func(t *testing.T) {
		ensurer := &fakePersistentIndexEnsurer{response: previewIndexResponseWithStoredValues(
			"idx", "fhir_edge/123", spec.Fields, spec.StoredValues,
		)}
		if err := ensurePersistentIndexSpecs(context.Background(), ensurer, []PersistentIndexSpec{spec}); err != nil {
			t.Fatalf("%v; response=%#v spec=%+v", err, ensurer.response, spec)
		}
		if !reflect.DeepEqual(ensurer.fields, spec.Fields) || !reflect.DeepEqual(ensurer.storedValues, spec.StoredValues) {
			t.Fatalf("index definition request fields=%v stored=%v", ensurer.fields, ensurer.storedValues)
		}
	})
	t.Run("rejects existing same-fields index without stored value", func(t *testing.T) {
		ensurer := &fakePersistentIndexEnsurer{response: previewIndexResponse("idx", "fhir_edge/123", spec.Fields)}
		if err := ensurePersistentIndexSpecs(context.Background(), ensurer, []PersistentIndexSpec{spec}); err == nil {
			t.Fatal("bootstrap accepted an index whose stored values differ")
		}
	})
}

type fakePersistentIndexEnsurer struct {
	response     driver.IndexResponse
	fields       []string
	storedValues []string
}

func (fake *fakePersistentIndexEnsurer) EnsurePersistentIndex(_ context.Context, fields []string, options *driver.CreatePersistentIndexOptions) (driver.IndexResponse, bool, error) {
	fake.fields = append([]string(nil), fields...)
	fake.storedValues = append([]string(nil), options.StoredValues...)
	return fake.response, false, nil
}
