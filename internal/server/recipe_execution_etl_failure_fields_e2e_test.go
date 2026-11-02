package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"

	gqlmodel "github.com/calypr/loom/generated/graphql/graph/model"
	"github.com/calypr/loom/internal/api/graphql/graph/dataframe"
	"github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
	dataset "github.com/calypr/loom/internal/dataset"
)

// REPAIR-20 / REPAIR-20b (F-R3). Promise under test: GET
// /api/v1/dataframe/recipe-executions/{id} (the legacy execution document
// consumed by ETL, openapi_recipe_execution.go) publishes the same explicit
// failure fields the GraphQL twin publishes from the same durable record —
// execution error/errorCode/errorRetryable/phase and per-output
// errorCode/errorRetryable/phase, with output.error MIRRORING twin semantics
// exactly (recipe_execution_reader.go:72 sets the output Error FROM
// execution.Error). Durable writers: publication/clickhouse/bundle_store.go
// failPhase:985-993 and the lease-lost reconciliation at :1207-1213 persist
// these fields; the REST reader may not silently drop them.
//
// REPAIR-20b (adversarial review ses_e65cad76effe REQUEST-CHANGES, F-1/F-2/
// F-3/F-5): parity means the twin's FULL encoding chain, not a raw copy. The
// twin encodes the failure trio through dataframeapi.PersistedFailure
// (internal/api/graphql/graph/dataframe/model.go:73-84) exactly as
// executionModel consumes it (recipe_control_graphql.go:319,334), whose
// code-less branch is errors.SanitizePersistedFailure
// (internal/dataframe/errors/errors.go:271-277): whitespace-only raw text
// becomes present-empty/"INTERNAL_ERROR"-free ("","","",false), unknown raw
// text NEVER echoes — it is Normalize-inferred to a public message/code/
// retryable — and a persisted code substitutes the code's stable public
// message (PublicMessage). The oracle for this file is the reviewer's 64-cell
// comparison table (8 shapes x exec/output x 4 fields, verdict-level
// equality per field, ABSENT vs null vs present+type+value; compare_r20.py
// semantics) over shapes.json + the live executionModel twin wire. The twin
// side is pinned separately in
// internal/api/graphql/graph/resolver/recipe_execution_twin_wire_parity_test.go
// (real reader + executionModel == twin_golden.json), so the CONTROL this
// file composes (reader + the same PersistedFailure constructor + the
// non-empty Phase gate recipe_control_graphql.go:329-331/339-341) is anchored
// to the genuine executionModel output.
//
// The 404/503 arms are re-proven here unchanged so the additive fields cannot
// regress the concealment/outage contracts.

const fr20ConcealedErr = "recipe execution not found"

const (
	fr20ExecID       = "fr20-exec-failed"
	fr20SuccessID    = "fr20-exec-success"
	fr20UnknownID    = "fr20-exec-missing"
	fr20Project      = "P"
	fr20FailureMsg   = "clickhouse insert rejected"
	fr20FailureCode  = "PUBLICATION_FAILED"
	fr20FailurePhase = "PUBLICATION"
	fr20OutputName   = "rows"
)

// matrix shapes (REPAIR-20b), verbatim from the adversarial review
// /tmp/r20-out/shapes.json (committed at
// testdata/r20_failure_parity/shapes.json; the twin resolver harness reads
// the identical copy). Shapes 1,2 mirror bundle_store.go failPhase:985-993,
// shape 5 mirrors the lease-lost reconciler :1207-1213; shapes 3,4,6,8 are
// the reviewer's sanctioned synthetics pinning the sanitizer branches the
// durable writers cannot currently produce.
const fr20TestdataDir = "testdata/r20_failure_parity"

type fr20Store struct {
	publication.BundleCatalog
	byID  map[string]publication.BundleExecution
	fault error
}

func (s *fr20Store) GetExecution(_ context.Context, id string) (publication.BundleExecution, error) {
	if s.fault != nil {
		return publication.BundleExecution{}, s.fault
	}
	if execution, ok := s.byID[id]; ok {
		return execution, nil
	}
	return publication.BundleExecution{}, nil
}

// fr20FailedExecution is a FAILED execution exactly as the only failure sink
// persists it (bundle_store.go failPhase:985-993): execution-level Error,
// FailureCode, FailureRetryable, FailurePhase, and per-output Failure* fields.
func fr20FailedExecution() publication.BundleExecution {
	return publication.BundleExecution{
		ID: fr20ExecID,
		BundleIdentity: publication.BundleIdentity{
			Name:              "fr20-recipe",
			Project:           fr20Project,
			DatasetGeneration: "gP",
			RecipeDigest:      "sha256-fr20-recipe",
			SchemaDigest:      "sha256-fr20-schema",
			AuthResourcePaths: []string{"p1"},
		},
		State:            publication.BundleFailed,
		Error:            fr20FailureMsg,
		FailureCode:      fr20FailureCode,
		FailureRetryable: true,
		FailurePhase:     fr20FailurePhase,
		Outputs: []publication.BundleOutputRecord{{
			Name:             fr20OutputName,
			State:            publication.BundleFailed,
			FailureCode:      fr20FailureCode,
			FailureRetryable: true,
			FailurePhase:     fr20FailurePhase,
		}},
	}
}

// fr20SucceededExecution is a healthy execution as the durable store holds it:
// no Error, no FailureCode, no FailurePhase anywhere.
func fr20SucceededExecution() publication.BundleExecution {
	return publication.BundleExecution{
		ID: fr20SuccessID,
		BundleIdentity: publication.BundleIdentity{
			Name:              "fr20-recipe",
			Project:           fr20Project,
			DatasetGeneration: "gP",
			RecipeDigest:      "sha256-fr20-recipe",
			SchemaDigest:      "sha256-fr20-schema",
			AuthResourcePaths: []string{"p1"},
		},
		State: publication.BundlePublished,
		Outputs: []publication.BundleOutputRecord{{
			Name:     fr20OutputName,
			State:    publication.BundleReady,
			RowCount: 41,
		}},
	}
}

func fr20HTTPGet(t *testing.T, store *fr20Store, id string) (int, string, map[string]any) {
	t.Helper()
	principal := &authscope.Principal{Subject: "operator-P", Projects: []string{fr20Project}}
	app := twinHTTPStack(&HTTPRoutes{releases: store}, principal)
	resp, err := app.Test(httptest.NewRequest(http.MethodGet, twinRoutePrefix+id, nil))
	if err != nil {
		t.Fatalf("GET %s%s: %v", twinRoutePrefix, id, err)
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	body := string(payload)
	var fields map[string]any
	if body != "" {
		decoder := json.NewDecoder(strings.NewReader(body))
		decoder.UseNumber()
		if err := decoder.Decode(&fields); err != nil {
			t.Fatalf("body is not JSON (%s): %v", body, err)
		}
	}
	t.Logf("F-R20 HTTP | GET %s%s status=%d body=%s", twinRoutePrefix, id, resp.StatusCode, body)
	return resp.StatusCode, body, fields
}

func fr20FailedOutput(t *testing.T, fields map[string]any) map[string]any {
	t.Helper()
	outputs, ok := fields["outputs"].([]any)
	if !ok || len(outputs) != 1 {
		t.Fatalf("expected exactly one output, got %v", fields["outputs"])
	}
	output, isObject := outputs[0].(map[string]any)
	if !isObject {
		t.Fatalf("output[0] is not an object: %v", outputs[0])
	}
	return output
}

func fr20PointerValue(t *testing.T, object map[string]any, key, want string) {
	t.Helper()
	raw, ok := object[key]
	if !ok {
		t.Fatalf("F-R20 FIDELITY GAP: object omits %q: the twin publishes it for this durable record", key)
	}
	if value, isString := raw.(string); !isString || value != want {
		t.Fatalf("field %q = %#v, want %q", key, raw, want)
	}
}

// fr20RequireExplicitBool pins "retryable is JSON true/false, not absent":
// the decoded value must exist and be a real JSON boolean.
func fr20RequireExplicitBool(t *testing.T, scope string, object map[string]any, key string, want bool) {
	t.Helper()
	raw, ok := object[key]
	if !ok {
		t.Fatalf("F-R20 FIDELITY GAP: %s omits %q: retryability must be an explicit JSON boolean %v, not absent", scope, key, want)
	}
	value, isBool := raw.(bool)
	if !isBool {
		t.Fatalf("%s.%s = %#v, want explicit JSON boolean %v", scope, key, raw, want)
	}
	if value != want {
		t.Fatalf("%s.%s = %v, want %v", scope, key, value, want)
	}
}

// Subject arm 1: the FAILED execution publishes the twin-encoded failure trio
// (message/code/retryable exactly as the twin's PersistedFailure chain
// encodes them for this record — derived here, never hardcoded) plus phase.
// RED on a raw-copy handler: the wire text differs from the twin substitution
// (F-1/F-2), or the keys are absent entirely (pre REPAIR-20).
func TestFR20EtlFailureFieldsPublishedForFailedExecution(t *testing.T) {
	store := &fr20Store{byID: map[string]publication.BundleExecution{fr20ExecID: fr20FailedExecution()}}

	// CONTROL: the GraphQL twin reader over the same durable record pins the
	// persisted values (reader level), and the twin's own encoding chain
	// computes the expected wire values.
	principal := &authscope.Principal{Subject: "operator-P", Projects: []string{fr20Project}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	twin, err := resolver.NewAuthorizedRecipeExecutionReader(store, nil)(ctx, fr20ExecID)
	if err != nil {
		t.Fatalf("control broken: GraphQL twin reader must serve the authorized FAILED execution, got %v", err)
	}
	if twin.State != "FAILED" || twin.Error != fr20FailureMsg || twin.ErrorCode != fr20FailureCode || !twin.ErrorRetryable || twin.Phase != fr20FailurePhase {
		t.Fatalf("control broken: GraphQL twin reader must publish the persisted failure fields, got state=%q error=%q errorCode=%q retryable=%v phase=%q",
			twin.State, twin.Error, twin.ErrorCode, twin.ErrorRetryable, twin.Phase)
	}
	expectMessage, expectCode, expectRetryable := dataframe.PersistedFailure(fr20FailureMsg, fr20FailureCode, true)
	if expectMessage == nil || expectCode == nil || expectRetryable == nil || *expectCode != fr20FailureCode || !*expectRetryable {
		t.Fatalf("control broken: twin chain must encode the persisted failure, got %v %v %v", expectMessage, expectCode, expectRetryable)
	}
	t.Logf("F-R20 CONTROL | reader persisted | error=%q errorCode=%q retryable=%v phase=%q | twin wire encoding | error=%q errorCode=%q retryable=%v",
		twin.Error, twin.ErrorCode, twin.ErrorRetryable, twin.Phase, *expectMessage, *expectCode, *expectRetryable)

	// SUBJECT: the REST/ETL twin over the real generated Fiber stack.
	status, body, fields := fr20HTTPGet(t, store, fr20ExecID)
	if status != http.StatusOK {
		t.Fatalf("authorized member read of own FAILED execution must be 200, got %d %s", status, body)
	}
	if fields["state"] != "FAILED" {
		t.Fatalf("failed execution must render state FAILED on the REST twin, got %v", fields["state"])
	}
	var missing []string
	for _, key := range []string{"error", "errorCode", "errorRetryable", "phase"} {
		if _, ok := fields[key]; !ok {
			missing = append(missing, "execution."+key)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("F-R20 FIDELITY GAP CONFIRMED: the REST/ETL recipe-execution document omits %s that the durable record holds and the GraphQL twin publishes for the same execution; REST/ETL clients cannot see the explicit failure message/code/retryability/phase and must infer from the FAILED state. body=%s",
			strings.Join(missing, ", "), body)
	}
	fr20PointerValue(t, fields, "error", *expectMessage)
	fr20PointerValue(t, fields, "errorCode", *expectCode)
	fr20PointerValue(t, fields, "phase", fr20FailurePhase)
	fr20RequireExplicitBool(t, "execution", fields, "errorRetryable", true)

	output := fr20FailedOutput(t, fields)
	if output["state"] != string(publication.BundleFailed) || output["name"] != fr20OutputName {
		t.Fatalf("failed execution output state/name = %v/%v, want FAILED/%s", output["state"], output["name"], fr20OutputName)
	}
	var outputMissing []string
	for _, key := range []string{"error", "errorCode", "errorRetryable", "phase"} {
		if _, ok := output[key]; !ok {
			outputMissing = append(outputMissing, "outputs[0]."+key)
		}
	}
	if len(outputMissing) > 0 {
		t.Fatalf("F-R20 FIDELITY GAP CONFIRMED: the REST/ETL output document omits %s; the GraphQL twin publishes them with output.error mirroring execution.error through the sanitizer chain (recipe_execution_reader.go:72). body=%s",
			strings.Join(outputMissing, ", "), body)
	}
	fr20PointerValue(t, output, "error", *expectMessage)
	fr20PointerValue(t, output, "errorCode", fr20FailureCode)
	fr20PointerValue(t, output, "phase", fr20FailurePhase)
	fr20RequireExplicitBool(t, "outputs[0]", output, "errorRetryable", true)
	t.Logf("F-R20 VERDICT | PASS | REST twin publishes the twin-encoded failure fields (execution + output, output.error mirrors execution.error through the shared sanitizer chain)")
}

// Subject arm 2: the additive-optional contract. A SUCCESS execution gains NO
// empty-string/zero failure fields — the twin Publishes none for a record with
// no persisted failure (PersistedFailure returns all-nil when neither a raw
// error nor a code is persisted; executionModel emits Phase only when
// non-empty).
func TestFR20EtlFailureFieldsAbsentForSucceededExecution(t *testing.T) {
	store := &fr20Store{byID: map[string]publication.BundleExecution{fr20SuccessID: fr20SucceededExecution()}}
	status, body, fields := fr20HTTPGet(t, store, fr20SuccessID)
	if status != http.StatusOK {
		t.Fatalf("authorized member read of own READY execution must be 200, got %d %s", status, body)
	}
	if fields["state"] != string(publication.BundleReady) {
		t.Fatalf("published execution must render state READY, got %v", fields["state"])
	}
	for _, key := range []string{"error", "errorCode", "errorRetryable", "phase"} {
		if raw, ok := fields[key]; ok {
			t.Fatalf("additive-optional contract: SUCCESS execution must not gain failure field %q, got %v. body=%s", key, raw, body)
		}
	}
	output := fr20FailedOutput(t, fields)
	if output["state"] != string(publication.BundleReady) {
		t.Fatalf("success output state = %v, want READY", output["state"])
	}
	for _, key := range []string{"error", "errorCode", "errorRetryable", "phase"} {
		if raw, ok := output[key]; ok {
			t.Fatalf("additive-optional contract: SUCCESS output must not gain failure field %q, got %v. body=%s", key, raw, body)
		}
	}
}

// Subject arm 3: the concealment and outage arms stay byte-unchanged by the
// additive fidelity (unknown id -> 404 concealed envelope; store fault ->
// retryable 503 BACKEND_UNAVAILABLE).
func TestFR20EtlFailureFieldsLeave404And503ArmsUnchanged(t *testing.T) {
	_, body, fields := fr20HTTPGet(t, &fr20Store{byID: map[string]publication.BundleExecution{fr20ExecID: fr20FailedExecution()}}, fr20UnknownID)
	if !strings.Contains(body, fr20ConcealedErr) || fields["id"] != nil || fields["errorCode"] != nil {
		t.Fatalf("missing execution must keep the concealed not-found envelope with zero failure metadata, got %s", body)
	}

	status, body, fields := fr20HTTPGet(t, &fr20Store{byID: map[string]publication.BundleExecution{fr20ExecID: fr20FailedExecution()}, fault: errors.New("arango connection reset")}, fr20ExecID)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("store fault must stay a retryable outage, got %d %s", status, body)
	}
	envelope, isObject := fields["error"].(map[string]any)
	if !isObject {
		t.Fatalf("outage body must carry the shared service error envelope, got %s", body)
	}
	if envelope["code"] != "BACKEND_UNAVAILABLE" {
		t.Fatalf("outage code = %v, want BACKEND_UNAVAILABLE (%s)", envelope["code"], body)
	}
	if retryable, isBool := envelope["retryable"].(bool); !isBool || !retryable {
		t.Fatalf("outage retryable = %#v, want explicit JSON true (%s)", envelope["retryable"], body)
	}
}

// ---------------------------------------------------------------------------
// REPAIR-20b parity matrix (reviewer harness ses_e65cad76effe absorbed).
// ---------------------------------------------------------------------------

var fr20ParityFields = []string{"error", "errorCode", "errorRetryable", "phase"}

type fr20Shape struct {
	Name string `json:"name"`
}

// fr20ClassifyCell mirrors compare_r20.py cls(): ABSENT / null /
// present(type) canonical-value, with python type names str/bool.
type fr20Cell struct {
	state string
	kind  string
	value string
}

func fr20Classify(object map[string]any, key string) fr20Cell {
	raw, ok := object[key]
	if !ok {
		return fr20Cell{state: "ABSENT"}
	}
	if raw == nil {
		return fr20Cell{state: "null"}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fr20Cell{state: "present", kind: fmt.Sprintf("%T", raw), value: fmt.Sprintf("%v", raw)}
	}
	switch raw.(type) {
	case string:
		return fr20Cell{state: "present", kind: "str", value: string(encoded)}
	case bool:
		return fr20Cell{state: "present", kind: "bool", value: string(encoded)}
	default:
		return fr20Cell{state: "present", kind: fmt.Sprintf("%T", raw), value: string(encoded)}
	}
}

type fr20Row struct {
	shape, level, field, twin, rest, verdict string
}

func fr20VerdictRow(shapeName, level, field string, twin, rest map[string]any) fr20Row {
	tv, rv := fr20Classify(twin, field), fr20Classify(rest, field)
	verdict := "MATCH"
	switch {
	case tv.state == "ABSENT" && rv.state == "ABSENT":
		verdict = "MATCH"
	case (tv.state == "ABSENT" && rv.state == "null") || (tv.state == "null" && rv.state == "ABSENT"):
		verdict = "NULL_EQUIV"
	case tv.state == "present" && rv.state == "present" && tv.kind == rv.kind && tv.value == rv.value:
		verdict = "MATCH"
	default:
		verdict = "DIVERGE"
	}
	render := func(c fr20Cell) string {
		if c.state != "present" {
			return c.state
		}
		return fmt.Sprintf("present(%s) %s", c.kind, c.value)
	}
	return fr20Row{shape: shapeName, level: level, field: field, twin: render(tv), rest: render(rv), verdict: verdict}
}

// fr20TwinWire builds the CONTROL: the live GraphQL twin wire for one shape.
// It feeds the durable record through the production
// resolver.NewAuthorizedRecipeExecutionReader and encodes the failure trio
// with dataframeapi.PersistedFailure + the non-empty Phase gate — exactly the
// primitives executionModel (recipe_control_graphql.go:316-346) consumes and
// marshals to GraphQL JSON. The reviewer-committed executionModel golden
// (testdata/r20_failure_parity/twin_golden.json, produced by the twin-side
// harness over the real executionModel) anchors this composition against
// executionModel drift; the resolver-package harness re-checks live
// executionModel == golden every run.
func fr20TwinWire(t *testing.T, execution publication.BundleExecution) map[string]any {
	t.Helper()
	principal := &authscope.Principal{Subject: "operator-P", Projects: []string{fr20Project}}
	ctx := authscope.ContextWithPrincipal(context.Background(), principal)
	store := &fr20Store{byID: map[string]publication.BundleExecution{execution.ID: execution}}
	result, err := resolver.NewAuthorizedRecipeExecutionReader(store, nil)(ctx, execution.ID)
	if err != nil {
		t.Fatalf("twin reader for %s: %v", execution.ID, err)
	}
	wire := &gqlmodel.DataframeRecipeExecution{
		ID:                   result.ID,
		Name:                 result.Name,
		RecipeDigest:         result.RecipeDigest,
		ResolvedSchemaDigest: result.ResolvedSchemaDigest,
		SourceGeneration:     result.SourceGeneration,
		State:                gqlmodel.DataframeRecipeExecutionState(result.State),
	}
	if result.TranslationVersion != "" {
		version := result.TranslationVersion
		wire.TranslationVersion = &version
	}
	wire.Error, wire.ErrorCode, wire.ErrorRetryable = dataframe.PersistedFailure(result.Error, result.ErrorCode, result.ErrorRetryable)
	if result.Phase != "" {
		phase := result.Phase
		wire.Phase = &phase
	}
	if result.RequestID != "" {
		requestID := result.RequestID
		wire.RequestID = &requestID
	}
	for _, output := range result.Outputs {
		item := &gqlmodel.DataframeRecipeExecutionOutput{
			Name:     output.Name,
			State:    gqlmodel.DataframeRecipeExecutionState(output.State),
			RowCount: output.RowCount,
		}
		item.Error, item.ErrorCode, item.ErrorRetryable = dataframe.PersistedFailure(output.Error, output.ErrorCode, output.ErrorRetryable)
		columns := make([]*gqlmodel.DataframeColumn, 0, len(output.Columns))
		for _, column := range output.Columns {
			columns = append(columns, dataframe.ColumnFromPhysical(column))
		}
		item.Columns = columns
		var selector dataset.DataframeSelector = output.Selector
		if selector.Valid() {
			item.Selector = &gqlmodel.DataframeSelector{Recipe: selector.Recipe, TranslationVersion: selector.TranslationVersion, Output: selector.Output}
		}
		if output.Phase != "" {
			phase := output.Phase
			item.Phase = &phase
		}
		wire.Outputs = append(wire.Outputs, item)
	}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal twin wire for %s: %v", execution.ID, err)
	}
	var fields map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		t.Fatalf("decode twin wire for %s: %v", execution.ID, err)
	}
	return fields
}

func fr20ReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(fr20TestdataDir + "/" + name)
	if err != nil {
		t.Fatalf("review matrix fixture %s missing: %v", name, err)
	}
	return raw
}

// fr20JSONEqual is a canonical (order-insensitive) JSON tree equality check
// used to anchor the composed CONTROL against the reviewer golden without
// pinning key order.
func fr20JSONEqual(path string, left, right any) string {
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
			if mismatch := fr20JSONEqual(path+"."+k, lv[k], rv[k]); mismatch != "" {
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
			if mismatch := fr20JSONEqual(fmt.Sprintf("%s[%d]", path, i), lv[i], rv[i]); mismatch != "" {
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

// TestFR20bEtlFailureFieldsTwinParityMatrix is the durable oracle from the
// adversarial review: 8 shapes x {execution, output[0]} x {error, errorCode,
// errorRetryable, phase} = 64 verdict-level comparisons between the live
// twin wire (CONTROL, real reader + the twin's own encoding primitives,
// golden-anchored) and the generated strict-stack REST body (OBSERVED).
// Pre-fix (raw-copy handler) this reproduces exactly the reviewer's 18
// divergences; post root-fix it must end with 0 DIVERGE / 0 NULL_EQUIV.
func TestFR20bEtlFailureFieldsTwinParityMatrix(t *testing.T) {
	var shapes []json.RawMessage
	if err := json.Unmarshal(fr20ReadTestdata(t, "shapes.json"), &shapes); err != nil {
		t.Fatalf("shapes fixture: %v", err)
	}
	if len(shapes) != 8 {
		t.Fatalf("expected the 8 reviewer shapes, got %d", len(shapes))
	}
	var golden map[string]json.RawMessage
	if err := json.Unmarshal(fr20ReadTestdata(t, "twin_golden.json"), &golden); err != nil {
		t.Fatalf("twin golden fixture: %v", err)
	}

	var rows []fr20Row
	for _, rawShape := range shapes {
		var header fr20Shape
		if err := json.Unmarshal(rawShape, &header); err != nil {
			t.Fatalf("shape header: %v", err)
		}
		name := header.Name
		// Round-trip the same durable JSON into the real durable type the
		// production catalog hands GetExecution back (reviewer harness
		// discipline, shapes.json -> publication.BundleExecution).
		var execution publication.BundleExecution
		if err := json.Unmarshal(rawShape, &execution); err != nil {
			t.Fatalf("%s: durable round-trip: %v", name, err)
		}

		control := fr20TwinWire(t, execution)
		rawGolden, ok := golden[name]
		if !ok {
			t.Fatalf("%s: no reviewer twin golden", name)
		}
		var goldenWire map[string]any
		goldenDecoder := json.NewDecoder(strings.NewReader(string(rawGolden)))
		goldenDecoder.UseNumber()
		if err := goldenDecoder.Decode(&goldenWire); err != nil {
			t.Fatalf("%s: golden twin wire: %v", name, err)
		}
		if mismatch := fr20JSONEqual(name+".twin", control, goldenWire); mismatch != "" {
			t.Fatalf("%s: CONTROL drift — composed live twin wire != reviewer executionModel golden at %s (resolver harness pins live executionModel == golden; the shared sanitizer chain itself changed)", name, mismatch)
		}

		status, body, observed := fr20HTTPGet(t, &fr20Store{byID: map[string]publication.BundleExecution{execution.ID: execution}}, execution.ID)
		if status != http.StatusOK {
			t.Fatalf("%s: REST GET must be 200, got %d %s", name, status, body)
		}
		observedOutput := fr20FailedOutput(t, observed)
		controlOutput := fr20FailedOutput(t, control)
		for _, field := range fr20ParityFields {
			rows = append(rows, fr20VerdictRow(name, "exec", field, control, observed))
			rows = append(rows, fr20VerdictRow(name, "out0", field, controlOutput, observedOutput))
		}
	}

	t.Logf("R20B PARITY TABLE | %d comparisons", len(rows))
	diverging := make([]string, 0)
	for _, row := range rows {
		t.Logf("R20B | %-20s | %-4s | %-14s | twin=%-30s | rest=%-30s | %s", row.shape, row.level, row.field, row.twin, row.rest, row.verdict)
		if row.verdict == "DIVERGE" {
			diverging = append(diverging, row.shape+"/"+row.level+"."+row.field)
		}
	}
	if len(rows) != 64 {
		t.Fatalf("expected 64 comparisons (8 shapes x 2 levels x 4 fields), got %d", len(rows))
	}
	if len(diverging) > 0 {
		t.Fatalf("R20B TWIN PARITY FAILURE: %d of %d comparisons DIVERGE from the GraphQL twin encoding chain (F-1/F-2: the REST reader must publish the twin's FULL PersistedFailure/SanitizePersistedFailure encoding, not a raw copy): %s",
			len(diverging), len(rows), strings.Join(diverging, ", "))
	}
	t.Logf("R20B VERDICT | PASS | 64/64 comparisons MATCH the twin oracle")
}
