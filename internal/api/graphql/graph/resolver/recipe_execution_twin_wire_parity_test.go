package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

// REPAIR-20b (adversarial review ses_e65cad76effe): absorbed twin-side half
// of the reviewer's parity harness (/tmp/r20-out/harness/
// r20_parity_twin_wire_test.go). This is the twin-side pin for the shared
// failure-field oracle: for each reviewed durable shape, the REAL reader +
// executionModel wire (the GraphQL twin consumers get exactly this) must
// equal the reviewer-committed golden. The REST-side matrix
// (internal/server/recipe_execution_etl_failure_fields_e2e_test.go) compares
// the generated strict-stack body against the same oracle, so any drift in
// executionModel / dataframeapi.PersistedFailure /
// errors.SanitizePersistedFailure breaks this file, and any REST divergence
// breaks that one. Shapes and golden live in testdata/r20_failure_parity/
// (canonical copy of the reviewer's /tmp/r20-out artifacts; the server test
// carries the identical fixture pair).

type r20TwinStore struct {
	byID map[string]materialization.BundleExecution
}

func (s *r20TwinStore) GetExecution(_ context.Context, id string) (materialization.BundleExecution, error) {
	if re, ok := s.byID[id]; ok {
		return re, nil
	}
	return materialization.BundleExecution{}, nil
}

func r20ReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/r20_failure_parity/" + name)
	if err != nil {
		t.Fatalf("reviewer twin fixture %s missing: %v", name, err)
	}
	return raw
}

func r20JSONEqual(path string, left, right any) string {
	switch lv := left.(type) {
	case map[string]any:
		rv, ok := right.(map[string]any)
		if !ok {
			return path + ": object vs " + fmt.Sprintf("%T", right)
		}
		keys := map[string]bool{}
		for k := range lv {
			keys[k] = true
		}
		for k := range rv {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if mismatch := r20JSONEqual(path+"."+k, lv[k], rv[k]); mismatch != "" {
				return mismatch
			}
		}
		return ""
	case []any:
		rv, ok := right.([]any)
		if !ok || len(lv) != len(rv) {
			return fmt.Sprintf("%s: array len/shape mismatch %v vs %T", path, len(lv), right)
		}
		for i := range lv {
			if mismatch := r20JSONEqual(fmt.Sprintf("%s[%d]", path, i), lv[i], rv[i]); mismatch != "" {
				return mismatch
			}
		}
		return ""
	case json.Number:
		lf, lerr := lv.Float64()
		rf, rerr := json.Number(fmt.Sprint(right)).Float64()
		if lerr != nil || rerr != nil || lf != rf {
			return fmt.Sprintf("%s: number %v vs %v", path, left, right)
		}
		return ""
	default:
		if fmt.Sprintf("%T|%v", left, left) != fmt.Sprintf("%T|%v", right, right) {
			return path + fmt.Sprintf(": %T %v vs %T %v", left, left, right, right)
		}
		return ""
	}
}

// TestR20TwinWireMatchesReviewedGolden drives every reviewed shape through
// the production reader + executionModel — the genuine twin pipeline — and
// requires the marshaled GraphQL wire to equal the reviewer golden verbatim.
func TestR20TwinWireMatchesReviewedGolden(t *testing.T) {
	var shapes []json.RawMessage
	if err := json.Unmarshal(r20ReadTestdata(t, "shapes.json"), &shapes); err != nil {
		t.Fatalf("shapes fixture: %v", err)
	}
	if len(shapes) != 8 {
		t.Fatalf("expected the 8 reviewer shapes, got %d", len(shapes))
	}
	var golden map[string]json.RawMessage
	if err := json.Unmarshal(r20ReadTestdata(t, "twin_golden.json"), &golden); err != nil {
		t.Fatalf("twin golden fixture: %v", err)
	}
	principal := &authscope.Principal{Subject: "operator-P", Projects: []string{"P"}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	for _, rawShape := range shapes {
		var header struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(rawShape, &header); err != nil {
			t.Fatalf("shape header: %v", err)
		}
		var execution materialization.BundleExecution
		if err := json.Unmarshal(rawShape, &execution); err != nil {
			t.Fatalf("%s: %v", header.Name, err)
		}
		store := &r20TwinStore{byID: map[string]materialization.BundleExecution{execution.ID: execution}}
		result, readerErr := NewAuthorizedRecipeExecutionReader(store, nil)(ctx, execution.ID)
		if readerErr != nil {
			t.Fatalf("%s: twin reader: %v", header.Name, readerErr)
		}
		wire, err := json.Marshal(executionModel(*result))
		if err != nil {
			t.Fatalf("%s: marshal: %v", header.Name, err)
		}
		rawGolden, ok := golden[header.Name]
		if !ok {
			t.Fatalf("%s: no reviewer golden", header.Name)
		}
		var goldenWire map[string]any
		decoder := json.NewDecoder(bytes.NewReader(rawGolden))
		decoder.UseNumber()
		if err := decoder.Decode(&goldenWire); err != nil {
			t.Fatalf("%s: golden: %v", header.Name, err)
		}
		var liveWire map[string]any
		liveDecoder := json.NewDecoder(bytes.NewReader(wire))
		liveDecoder.UseNumber()
		if err := liveDecoder.Decode(&liveWire); err != nil {
			t.Fatalf("%s: live: %v", header.Name, err)
		}
		if mismatch := r20JSONEqual(header.Name, liveWire, goldenWire); mismatch != "" {
			t.Fatalf("R20 TWIN-SIDE DRIFT | %s: real executionModel wire != reviewer golden at %s\nlive=%s\ngolden=%s",
				header.Name, mismatch, wire, string(rawGolden))
		}
		t.Logf("R20-TWIN PIN | %s | executionModel wire == reviewer golden | %s", header.Name, string(wire))
	}
}
