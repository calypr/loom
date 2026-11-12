// dataframe-query executes a checked-in GraphQL dataframe request and prints
// the response with wall-clock timing. It is deliberately a small developer
// tool: benchmark loops belong in Go benchmarks, while this command makes one
// human-readable dataframe request easy to inspect end to end.
//
// A GraphQL operation error is a command failure even when the server answers
// with HTTP 200, so automation cannot mistake a failed query for an empty one.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

type graphqlRequest struct {
	Query     string          `json:"query"`
	Variables json.RawMessage `json:"variables"`
}

func main() {
	err := run(os.Args[1:], newHTTPClient, os.Stdout, os.Stderr)
	if err == nil {
		return
	}
	if errors.Is(err, flag.ErrHelp) {
		os.Exit(0)
	}
	code := 1
	var usage usageError
	if errors.As(err, &usage) {
		// Preserve the historical exit status for malformed flags.
		code = 2
	}
	fmt.Fprintf(os.Stderr, "dataframe-query: %v\n", err)
	os.Exit(code)
}

// usageError marks flag parsing failures so main can keep the exit status the
// standard flag package used while the rest of the command reports status 1.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func newHTTPClient(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }

func run(args []string, clientFor func(time.Duration) *http.Client, stdout, stderr io.Writer) error {
	var (
		url           string
		queryPath     string
		variablesPath string
		repeat        int
		limit         int
		timeout       time.Duration
		printResponse bool
	)
	flags := flag.NewFlagSet("dataframe-query", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&url, "url", "http://127.0.0.1:8080/graphql/dataframe", "GraphQL endpoint")
	flags.StringVar(&queryPath, "query", "examples/meta_patient_dataframe.graphql", "GraphQL operation file")
	flags.StringVar(&variablesPath, "variables", "examples/meta_patient_dataframe.variables.json", "GraphQL variables JSON file")
	flags.IntVar(&repeat, "repeat", 1, "number of sequential requests; only the final response is printed")
	flags.IntVar(&limit, "limit", 0, "override GraphQL row limit; 0 preserves the variables file")
	flags.DurationVar(&timeout, "timeout", 60*time.Second, "per-request HTTP timeout")
	flags.BoolVar(&printResponse, "print-response", true, "pretty-print the final GraphQL response")
	if err := flags.Parse(args); err != nil {
		return usageError{err: err}
	}

	if repeat < 1 {
		return fmt.Errorf("repeat must be positive")
	}
	variables, err := os.ReadFile(variablesPath)
	if err != nil {
		return fmt.Errorf("read variables %q: %w", variablesPath, err)
	}
	if !json.Valid(variables) {
		return fmt.Errorf("variables file %q is not valid JSON", variablesPath)
	}
	if limit > 0 {
		variables, err = withLimit(variables, limit)
		if err != nil {
			return fmt.Errorf("override row limit: %w", err)
		}
	}
	query, err := os.ReadFile(queryPath)
	if err != nil {
		return fmt.Errorf("read query %q: %w", queryPath, err)
	}
	payload, err := encodeGraphQLRequest(query, variables)
	if err != nil {
		return fmt.Errorf("encode GraphQL request: %w", err)
	}

	durations, responseBody, responseMetrics, err := requestSequence(clientFor(timeout), url, payload, repeat, stderr)
	if err != nil {
		return err
	}

	min, average, max := summarizeDurations(durations)
	fmt.Fprintf(stdout, "GraphQL dataframe request: %s\n", url)
	fmt.Fprintf(stdout, "HTTP/server total: runs=%d  cold=%s  warm=%s  min=%s  avg=%s  max=%s\n", repeat, durations[0].Round(time.Microsecond), durations[len(durations)-1].Round(time.Microsecond), min.Round(time.Microsecond), average.Round(time.Microsecond), max.Round(time.Microsecond))
	fmt.Fprintf(stdout, "Response: rows=%d  bytes=%d  rows/sec=%.1f\n\n", responseMetrics.Rows, responseMetrics.Bytes, responseMetrics.RowsPerSecond)
	if responseMetrics.Diagnostics != nil {
		d := responseMetrics.Diagnostics
		fmt.Fprintf(stdout, "Server stages (ms): input_resolution=%.3f  request_preparation=%.3f  compilation=%.3f  source_query=%.3f  row_materialization=%.3f  result_assembly=%.3f  dataframe_service_total=%.3f\n", d.InputResolutionMs, d.RequestPreparationMs, d.CompilationMs, d.sourceQueryMs(), d.RowMaterializationMs, d.ResultAssemblyMs, d.TotalMs)
		fmt.Fprintf(stdout, "Outside dataframe service (GraphQL serialization + HTTP): %.3f ms\n\n", durationMinusMillis(durations[len(durations)-1], d.TotalMs))
		fmt.Fprintf(stdout, "Compiler plan: traversal_sets=%d  shared_traversals=%d  required_match_reuse=%d  scope_safe_sharing_groups=%d  scope_safe_sharing_sets=%d  potential_sharing_groups=%d  potential_sharing_sets=%d\n", d.Plan.TraversalSets, d.Plan.SharedTraversalCount, d.Plan.RequiredMatchReuseCount, d.Plan.ScopedSharingCandidateGroups, d.Plan.ScopedSharingCandidateSets, d.Plan.PotentialSharingOpportunityGroups, d.Plan.PotentialSharingOpportunitySets)
		for _, reuse := range d.Plan.RichSourceReuse {
			fmt.Fprintf(stdout, "  rich source reuse: set=%s  total=%d  aggregates=%d  pivots=%d  slices=%d\n", reuse.SourceSet, reuse.TotalConsumers, reuse.AggregateConsumers, reuse.PivotConsumers, reuse.SliceConsumers)
		}
		fmt.Fprintln(stdout)
	}
	if printResponse {
		fmt.Fprintln(stdout, "Response:")
		if err := prettyPrint(stdout, responseBody); err != nil {
			return fmt.Errorf("format GraphQL response: %w", err)
		}
	}
	return nil
}

// requestSequence issues the repeat loop and inspects every response, not only
// the last one, so an operation error in any request fails the command with its
// request index. A failing request stops the loop.
func requestSequence(client *http.Client, url string, payload []byte, repeat int, stderr io.Writer) ([]time.Duration, []byte, dataframeResponseMetrics, error) {
	durations := make([]time.Duration, 0, repeat)
	var responseBody []byte
	var responseMetrics dataframeResponseMetrics
	for run := 1; run <= repeat; run++ {
		started := time.Now()
		body, err := execute(client, url, payload)
		duration := time.Since(started)
		if err != nil {
			return nil, nil, dataframeResponseMetrics{}, fmt.Errorf("request %d/%d: %w", run, repeat, err)
		}
		if err := classifyGraphQLEnvelope(body); err != nil {
			// Keep the payload visible even with -print-response=false: a
			// rejected envelope is exactly what the caller needs to see.
			printEnvelopeForDiagnosis(stderr, body)
			return nil, nil, dataframeResponseMetrics{}, fmt.Errorf("request %d/%d: %w", run, repeat, err)
		}
		durations = append(durations, duration)
		responseBody = body
		responseMetrics = inspectDataframeResponse(body, duration)
	}
	return durations, responseBody, responseMetrics, nil
}

// printEnvelopeForDiagnosis writes the raw response payload for a rejected
// envelope, pretty-printing it when possible.
func printEnvelopeForDiagnosis(writer io.Writer, body []byte) {
	fmt.Fprintf(writer, "dataframe-query: response preserved for diagnosis (%d bytes):\n", len(body))
	if err := prettyPrint(writer, body); err != nil {
		fmt.Fprintln(writer, strings.TrimSpace(string(body)))
	}
}

// dataframeResponseMetrics is intentionally derived from the GraphQL response
// rather than server internals. Servers that select the diagnostics field also
// return service-stage timings; older Loom servers remain compatible but omit
// that optional measurement block.
type dataframeResponseMetrics struct {
	Rows          int
	Bytes         int
	RowsPerSecond float64
	Diagnostics   *dataframeDiagnostics
}

type dataframeDiagnostics struct {
	InputResolutionMs    float64                          `json:"inputResolutionMs"`
	RequestPreparationMs float64                          `json:"requestPreparationMs"`
	CompilationMs        float64                          `json:"compilationMs"`
	SourceQueryMs        *float64                         `json:"sourceQueryMs"`
	ArangoQueryMs        *float64                         `json:"arangoQueryMs"`
	RowMaterializationMs float64                          `json:"rowMaterializationMs"`
	ResultAssemblyMs     float64                          `json:"resultAssemblyMs"`
	TotalMs              float64                          `json:"totalMs"`
	Plan                 dataframeCompilerPlanDiagnostics `json:"plan"`
}

// sourceQueryMs reports the neutral source-query measurement under the
// agreed schema: the server's sourceQueryMs when present, falling back to
// the legacy arangoQueryMs only for older servers that predate the field. A
// modern PostgreSQL server reports arangoQueryMs as an explicit zero, so the
// legacy field is consulted only when sourceQueryMs is missing entirely.
func (d dataframeDiagnostics) sourceQueryMs() float64 {
	if d.SourceQueryMs != nil {
		return *d.SourceQueryMs
	}
	if d.ArangoQueryMs != nil {
		return *d.ArangoQueryMs
	}
	return 0
}

type dataframeCompilerPlanDiagnostics struct {
	TraversalSets                     int                        `json:"traversalSets"`
	SharedTraversalCount              int                        `json:"sharedTraversalCount"`
	RequiredMatchReuseCount           int                        `json:"requiredMatchReuseCount"`
	ScopedSharingCandidateGroups      int                        `json:"scopedSharingCandidateGroups"`
	ScopedSharingCandidateSets        int                        `json:"scopedSharingCandidateSets"`
	PotentialSharingOpportunityGroups int                        `json:"potentialSharingOpportunityGroups"`
	PotentialSharingOpportunitySets   int                        `json:"potentialSharingOpportunitySets"`
	RichSourceReuse                   []dataframeRichSourceReuse `json:"richSourceReuse"`
}

type dataframeRichSourceReuse struct {
	SourceSet          string `json:"sourceSet"`
	AggregateConsumers int    `json:"aggregateConsumers"`
	PivotConsumers     int    `json:"pivotConsumers"`
	SliceConsumers     int    `json:"sliceConsumers"`
	TotalConsumers     int    `json:"totalConsumers"`
}

func inspectDataframeResponse(body []byte, duration time.Duration) dataframeResponseMetrics {
	metrics := dataframeResponseMetrics{Bytes: len(body)}
	var envelope struct {
		Data struct {
			Run struct {
				RowCount    int                   `json:"rowCount"`
				Rows        []json.RawMessage     `json:"rows"`
				Diagnostics *dataframeDiagnostics `json:"diagnostics"`
			} `json:"runFhirDataframe"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return metrics
	}
	metrics.Rows = envelope.Data.Run.RowCount
	metrics.Diagnostics = envelope.Data.Run.Diagnostics
	if metrics.Rows == 0 {
		metrics.Rows = len(envelope.Data.Run.Rows)
	}
	if duration > 0 {
		metrics.RowsPerSecond = float64(metrics.Rows) / duration.Seconds()
	}
	return metrics
}

// graphqlResponseError explains why a response that arrived with a successful
// HTTP status must not be reported as a complete dataframe result. GraphQL
// servers routinely answer operation failures with HTTP 200 plus an "errors"
// array, and a dataframe CLI that ignores them tells its caller "0 rows" when
// the query never ran.
type graphqlResponseError struct {
	Kind   string
	Reason string
	Errors []string
}

func (e *graphqlResponseError) Error() string {
	message := e.Kind
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	if len(e.Errors) == 0 {
		return message
	}
	detail := strings.Join(e.Errors[:min(len(e.Errors), maxGraphQLErrorDetails)], "; ")
	if len(e.Errors) > maxGraphQLErrorDetails {
		detail = fmt.Sprintf("%s; +%d more", detail, len(e.Errors)-maxGraphQLErrorDetails)
	}
	return fmt.Sprintf("%s (%d error(s)): %s", message, len(e.Errors), detail)
}

const maxGraphQLErrorDetails = 5

type graphqlErrorDetail struct {
	Message   string `json:"message"`
	Path      []any  `json:"path"`
	Locations []struct {
		Line   int `json:"line"`
		Column int `json:"column"`
	} `json:"locations"`
}

func (e graphqlErrorDetail) String() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = "unspecified GraphQL error"
	}
	if path := graphqlPathString(e.Path); path != "" {
		message += " at " + path
	}
	for _, location := range e.Locations {
		message += fmt.Sprintf(" (line %d, column %d)", location.Line, location.Column)
	}
	return message
}

func graphqlPathString(path []any) string {
	if len(path) == 0 {
		return ""
	}
	segments := make([]string, 0, len(path))
	for _, segment := range path {
		switch typed := segment.(type) {
		case string:
			segments = append(segments, typed)
		case float64:
			segments = append(segments, strconv.Itoa(int(typed)))
		default:
			segments = append(segments, fmt.Sprint(typed))
		}
	}
	return strings.Join(segments, ".")
}

// classifyGraphQLEnvelope returns nil only for a GraphQL response the command
// may report as complete: no operation errors and a runFhirDataframe payload.
// Data mixed with errors is rejected rather than silently reported as rows.
// Each envelope field is decoded on its own so one malformed field cannot hide
// the others.
func classifyGraphQLEnvelope(body []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return &graphqlResponseError{
			Kind:   "invalid GraphQL response",
			Reason: fmt.Sprintf("body is not a GraphQL JSON envelope: %v", err),
		}
	}
	errs, err := graphQLErrors(envelope["errors"])
	if err != nil {
		return &graphqlResponseError{
			Kind:   "invalid GraphQL response",
			Reason: fmt.Sprintf("errors field is not a GraphQL error list: %v", err),
		}
	}
	hasPayload := graphqlDataframePayload(envelope["data"])
	if len(errs) > 0 {
		if hasPayload {
			return &graphqlResponseError{
				Kind:   "incomplete GraphQL response",
				Reason: "data was returned alongside operation errors, so the result is partial and must not look complete",
				Errors: errs,
			}
		}
		return &graphqlResponseError{
			Kind:   "GraphQL operation reported errors",
			Reason: "HTTP succeeded but the response carries no dataframe payload, so a row count of zero is not a successful query",
			Errors: errs,
		}
	}
	if !hasPayload {
		return &graphqlResponseError{
			Kind:   "GraphQL response returned no dataframe payload",
			Reason: "a successful response must carry runFhirDataframe; without it the query produced no usable result",
		}
	}
	return nil
}

func graphQLErrors(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil, nil
	}
	var details []graphqlErrorDetail
	if err := json.Unmarshal(raw, &details); err != nil {
		return nil, err
	}
	rendered := make([]string, 0, len(details))
	for _, detail := range details {
		rendered = append(rendered, detail.String())
	}
	return rendered, nil
}

// graphqlDataframePayload reports whether "data" carries a runFhirDataframe
// object. A missing or null data, or a null field, means the operation did not
// produce a usable result.
func graphqlDataframePayload(data json.RawMessage) bool {
	if len(data) == 0 || strings.TrimSpace(string(data)) == "null" {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return false
	}
	run, ok := fields["runFhirDataframe"]
	if !ok {
		return false
	}
	var payload map[string]json.RawMessage
	return json.Unmarshal(run, &payload) == nil && payload != nil
}

func durationMinusMillis(duration time.Duration, milliseconds float64) float64 {
	remaining := float64(duration)/float64(time.Millisecond) - milliseconds
	if remaining < 0 {
		return 0
	}
	return remaining
}

// encodeGraphQLRequest builds the GraphQL POST body from the raw operation and
// variables bytes so neither passes through a numeric decode.
func encodeGraphQLRequest(query, variables []byte) ([]byte, error) {
	return json.Marshal(graphqlRequest{Query: string(query), Variables: variables})
}

// withLimit replaces or adds the top-level limit field, leaving every other
// byte of the variables document untouched. Fields are kept as RawMessage
// because decoding into any would route numbers through float64, which silently
// rewrites integers above 2^53 and reformats exponent and decimal literals.
func withLimit(variables []byte, limit int) ([]byte, error) {
	if limit <= 0 {
		return variables, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(variables, &fields); err != nil {
		return nil, err
	}
	// json.Unmarshal accepts "null" into a nil map without an error.
	if fields == nil {
		return nil, fmt.Errorf("variables must be a JSON object")
	}
	fields["limit"] = json.RawMessage(strconv.AppendInt(nil, int64(limit), 10))
	return json.Marshal(fields)
}

func execute(client *http.Client, url string, payload []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func summarizeDurations(durations []time.Duration) (time.Duration, time.Duration, time.Duration) {
	minimum, maximum := durations[0], durations[0]
	var total time.Duration
	for _, duration := range durations {
		if duration < minimum {
			minimum = duration
		}
		if duration > maximum {
			maximum = duration
		}
		total += duration
	}
	return minimum, total / time.Duration(len(durations)), maximum
}

func prettyPrint(writer io.Writer, body []byte) error {
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, body, "", "  "); err != nil {
		return err
	}
	_, err := fmt.Fprintln(writer, formatted.String())
	return err
}
