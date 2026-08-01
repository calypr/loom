package materializationapi

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
)

func TestConvertFiltersDecodesGraphQLJSONValues(t *testing.T) {
	filters := convertFilters([]*model.DataframeFilterInput{
		{Column: "project", Op: "EQ", Value: json.RawMessage(`"HTAN_INT-BForePC"`)},
		{Column: "status", Op: "IN", Value: json.RawMessage(`["active","pending"]`)},
		{Column: "score", Op: "GTE", Value: json.RawMessage(`10`)},
		{Column: "deleted_at", Op: "IS_NULL", Value: json.RawMessage(`null`)},
	})

	if got, want := filters[0].Value, any("HTAN_INT-BForePC"); got != want {
		t.Fatalf("EQ value = %#v, want %#v", got, want)
	}
	if got, want := filters[1].Value, []any{"active", "pending"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("IN value = %#v, want %#v", got, want)
	}
	if got, want := filters[2].Value, any(float64(10)); got != want {
		t.Fatalf("numeric value = %#v, want %#v", got, want)
	}
	if filters[3].Value != nil {
		t.Fatalf("null value = %#v, want nil", filters[3].Value)
	}
}
