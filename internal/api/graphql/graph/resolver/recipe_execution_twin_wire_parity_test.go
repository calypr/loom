package resolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"

	dataframeapi "github.com/calypr/loom/internal/api/graphql/graph/dataframe"
	"github.com/calypr/loom/internal/authscope"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
)

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
		r20AssertHealthyOutputsPublishNoFailureTrio(t, header.Name, execution, liveWire)
		t.Logf("R20-TWIN PIN | %s | executionModel wire == reviewer golden | %s", header.Name, string(wire))
	}
}

func r20AssertHealthyOutputsPublishNoFailureTrio(t *testing.T, shapeName string, durable materialization.BundleExecution, liveWire map[string]any) {
	t.Helper()
	outputs, ok := liveWire["outputs"].([]any)
	if !ok || len(outputs) != len(durable.Outputs) {
		t.Fatalf("%s: wire outputs do not mirror the durable set: %v", shapeName, liveWire["outputs"])
	}
	for i, output := range durable.Outputs {
		healthy := output.State == materialization.BundleReady || output.State == materialization.BundlePublished
		if !healthy || output.FailureCode != "" {
			continue
		}
		wireOutput, isObject := outputs[i].(map[string]any)
		if !isObject {
			t.Fatalf("%s: outputs[%d] is not an object: %v", shapeName, i, outputs[i])
		}
		for _, key := range []string{"error", "errorCode", "errorRetryable"} {
			if raw, present := wireOutput[key]; present {
				t.Fatalf("R20 PHANTOM FAILURE | %s: healthy outputs[%d] (durable state %s, no own persisted failure code) publishes %s=%v; the execution-level failure must not mirror onto a healthy output's own trio (REPAIR-20c: recipe_execution_reader.go:72 mirror must be gated by output health)",
					shapeName, i, output.State, key, raw)
			}
		}
	}
}

func r20GenuineOutputFailureRecord(outputState materialization.BundleState) materialization.BundleExecution {
	return materialization.BundleExecution{
		ID: "shape-genuine-" + string(outputState),
		BundleIdentity: materialization.BundleIdentity{
			Name:              "genuine-output-failure",
			Project:           "P",
			DatasetGeneration: "gP",
			RecipeDigest:      "sha256-recipe",
			SchemaDigest:      "sha256-schema",
			AuthResourcePaths: []string{"p1"},
		},
		State: materialization.BundlePublished,
		Outputs: []materialization.BundleOutputRecord{{
			Name: "rows", State: outputState, RowCount: 3,
			FailureCode: "PUBLICATION_FAILED", FailureRetryable: true, FailurePhase: "VALIDATING",
		}},
	}
}

func TestR20GenuineOutputFailureProjectsDespiteHealthyExecution(t *testing.T) {
	principal := &authscope.Principal{Subject: "operator-P", Projects: []string{"P"}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	records := []struct {
		name        string
		outputState materialization.BundleState
	}{
		{name: "failed-output_healthy-execution", outputState: materialization.BundleFailed},
		{name: "ready-output_own-failure-code", outputState: materialization.BundleReady},
	}
	for _, record := range records {
		durable := r20GenuineOutputFailureRecord(record.outputState)
		store := &r20TwinStore{byID: map[string]materialization.BundleExecution{durable.ID: durable}}
		result, readerErr := NewAuthorizedRecipeExecutionReader(store, nil)(ctx, durable.ID)
		if readerErr != nil {
			t.Fatalf("%s: twin reader: %v", record.name, readerErr)
		}
		wire, err := json.Marshal(executionModel(*result))
		if err != nil {
			t.Fatalf("%s: marshal: %v", record.name, err)
		}
		var fields map[string]any
		decoder := json.NewDecoder(bytes.NewReader(wire))
		decoder.UseNumber()
		if err := decoder.Decode(&fields); err != nil {
			t.Fatalf("%s: decode: %v", record.name, err)
		}
		for _, key := range []string{"error", "errorCode", "errorRetryable"} {
			if raw, present := fields[key]; present {
				t.Fatalf("%s: healthy execution page must publish no exec-level trio, got %s=%v (%s)", record.name, key, raw, wire)
			}
		}
		outputs, isList := fields["outputs"].([]any)
		if !isList || len(outputs) != 1 {
			t.Fatalf("%s: outputs = %v, want exactly one output", record.name, fields["outputs"])
		}
		output, isObject := outputs[0].(map[string]any)
		if !isObject {
			t.Fatalf("%s: outputs[0] is not an object: %v", record.name, outputs[0])
		}
		wantMessage, wantCode, wantRetryable := dataframeapi.PersistedFailure("", string(durable.Outputs[0].FailureCode), durable.Outputs[0].FailureRetryable)
		if wantMessage == nil || wantCode == nil || wantRetryable == nil {
			t.Fatalf("%s: control broken: the twin chain must encode a persisted per-output failure code", record.name)
		}
		for key, want := range map[string]any{"error": *wantMessage, "errorCode": *wantCode} {
			if got, present := output[key]; !present || got != want {
				t.Fatalf("R20 SUPPRESSION MUTANT | %s: genuine per-output failure (own state %s + own persisted code) lost its twin trio field %s: got %v (present=%v), want %v (%s)",
					record.name, record.outputState, key, got, present, want, wire)
			}
		}
		retryable, isBool := output["errorRetryable"].(bool)
		if !isBool || retryable != *wantRetryable {
			t.Fatalf("R20 SUPPRESSION MUTANT | %s: genuine per-output failure errorRetryable = %#v, want explicit %v (%s)", record.name, output["errorRetryable"], *wantRetryable, wire)
		}
		if phase, present := output["phase"]; !present || phase != durable.Outputs[0].FailurePhase {
			t.Fatalf("R20 SUPPRESSION MUTANT | %s: genuine per-output failure phase = %v (present=%v), want %q (%s)", record.name, phase, present, durable.Outputs[0].FailurePhase, wire)
		}
		t.Logf("R20 GENUINE PIN | %s | per-output failure projects despite a healthy execution page | %s", record.name, wire)
	}
}
