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
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

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

		code = 2
	}
	fmt.Fprintf(os.Stderr, "dataframe-query: %v\n", err)
	os.Exit(code)
}

type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func newHTTPClient(timeout time.Duration) *http.Client { return &http.Client{Timeout: timeout} }

func run(args []string, clientFor func(time.Duration) *http.Client, stdout, stderr io.Writer) error {
	var (
		baseURL       string
		requestPath   string
		repeat        int
		limit         int
		timeout       time.Duration
		printResponse bool
	)
	flags := flag.NewFlagSet("dataframe-query", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&baseURL, "url", "http://127.0.0.1:8080", "Loom base URL")
	flags.StringVar(&requestPath, "request", "examples/meta_patient_dataframe.json", "dataframe request JSON file")
	flags.IntVar(&repeat, "repeat", 1, "number of sequential requests; only the final response is printed")
	flags.IntVar(&limit, "limit", 0, "override row limit; 0 preserves the request file")
	flags.DurationVar(&timeout, "timeout", 60*time.Second, "per-request HTTP timeout")
	flags.BoolVar(&printResponse, "print-response", true, "pretty-print the final response")
	if err := flags.Parse(args); err != nil {
		return usageError{err: err}
	}

	if repeat < 1 {
		return fmt.Errorf("repeat must be positive")
	}
	payload, err := os.ReadFile(requestPath)
	if err != nil {
		return fmt.Errorf("read request %q: %w", requestPath, err)
	}
	var request struct {
		Input struct {
			Project string `json:"project"`
		} `json:"input"`
	}
	if err := json.Unmarshal(payload, &request); err != nil {
		return fmt.Errorf("decode request %q: %w", requestPath, err)
	}
	if strings.TrimSpace(request.Input.Project) == "" {
		return fmt.Errorf("request %q requires input.project", requestPath)
	}
	if limit > 0 {
		payload, err = withLimit(payload, limit)
		if err != nil {
			return fmt.Errorf("override row limit: %w", err)
		}
	}
	endpoint := strings.TrimRight(baseURL, "/") + "/api/v1/projects/" + url.PathEscape(url.PathEscape(request.Input.Project)) + "/dataframes/run"

	durations, responseBody, responseMetrics, err := requestSequence(clientFor(timeout), endpoint, payload, repeat, stderr)
	if err != nil {
		return err
	}

	min, average, max := summarizeDurations(durations)
	fmt.Fprintf(stdout, "REST dataframe request: %s\n", endpoint)
	fmt.Fprintf(stdout, "HTTP/server total: runs=%d  cold=%s  warm=%s  min=%s  avg=%s  max=%s\n", repeat, durations[0].Round(time.Microsecond), durations[len(durations)-1].Round(time.Microsecond), min.Round(time.Microsecond), average.Round(time.Microsecond), max.Round(time.Microsecond))
	fmt.Fprintf(stdout, "Response: rows=%d  bytes=%d  rows/sec=%.1f\n\n", responseMetrics.Rows, responseMetrics.Bytes, responseMetrics.RowsPerSecond)
	if responseMetrics.Diagnostics != nil {
		d := responseMetrics.Diagnostics
		fmt.Fprintf(stdout, "Server stages (ms): input_resolution=%.3f  request_preparation=%.3f  compilation=%.3f  arango_query=%.3f  row_materialization=%.3f  result_assembly=%.3f  dataframe_service_total=%.3f\n", d.InputResolutionMs, d.RequestPreparationMs, d.CompilationMs, d.ArangoQueryMs, d.RowMaterializationMs, d.ResultAssemblyMs, d.TotalMs)
		fmt.Fprintf(stdout, "Outside dataframe service (serialization + HTTP): %.3f ms\n\n", durationMinusMillis(durations[len(durations)-1], d.TotalMs))
		fmt.Fprintf(stdout, "Compiler plan: traversal_sets=%d  shared_traversals=%d  required_match_reuse=%d  scope_safe_sharing_groups=%d  scope_safe_sharing_sets=%d  potential_sharing_groups=%d  potential_sharing_sets=%d\n", d.Plan.TraversalSets, d.Plan.SharedTraversalCount, d.Plan.RequiredMatchReuseCount, d.Plan.ScopedSharingCandidateGroups, d.Plan.ScopedSharingCandidateSets, d.Plan.PotentialSharingOpportunityGroups, d.Plan.PotentialSharingOpportunitySets)
		for _, reuse := range d.Plan.RichSourceReuse {
			fmt.Fprintf(stdout, "  rich source reuse: set=%s  total=%d  aggregates=%d  pivots=%d  slices=%d\n", reuse.SourceSet, reuse.TotalConsumers, reuse.AggregateConsumers, reuse.PivotConsumers, reuse.SliceConsumers)
		}
		fmt.Fprintln(stdout)
	}
	if printResponse {
		fmt.Fprintln(stdout, "Response:")
		if err := prettyPrint(stdout, responseBody); err != nil {
			return fmt.Errorf("format dataframe response: %w", err)
		}
	}
	return nil
}

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
		var result dataframeResponse
		if err := json.Unmarshal(body, &result); err != nil || result.RowCount == nil || result.Rows == nil {
			printResponseForDiagnosis(stderr, body)
			return nil, nil, dataframeResponseMetrics{}, fmt.Errorf("request %d/%d: invalid dataframe response", run, repeat)
		}
		durations = append(durations, duration)
		responseBody = body
		responseMetrics = result.metrics(len(body), duration)
	}
	return durations, responseBody, responseMetrics, nil
}

func printResponseForDiagnosis(writer io.Writer, body []byte) {
	fmt.Fprintf(writer, "dataframe-query: response preserved for diagnosis (%d bytes):\n", len(body))
	if err := prettyPrint(writer, body); err != nil {
		fmt.Fprintln(writer, strings.TrimSpace(string(body)))
	}
}

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
	ArangoQueryMs        float64                          `json:"arangoQueryMs"`
	RowMaterializationMs float64                          `json:"rowMaterializationMs"`
	ResultAssemblyMs     float64                          `json:"resultAssemblyMs"`
	TotalMs              float64                          `json:"totalMs"`
	Plan                 dataframeCompilerPlanDiagnostics `json:"plan"`
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

type dataframeResponse struct {
	RowCount    *int                  `json:"rowCount"`
	Rows        json.RawMessage       `json:"rows"`
	Diagnostics *dataframeDiagnostics `json:"diagnostics"`
}

func (r dataframeResponse) metrics(size int, duration time.Duration) dataframeResponseMetrics {
	metrics := dataframeResponseMetrics{Bytes: size, Rows: *r.RowCount, Diagnostics: r.Diagnostics}
	if duration > 0 {
		metrics.RowsPerSecond = float64(metrics.Rows) / duration.Seconds()
	}
	return metrics
}

func durationMinusMillis(duration time.Duration, milliseconds float64) float64 {
	remaining := float64(duration)/float64(time.Millisecond) - milliseconds
	if remaining < 0 {
		return 0
	}
	return remaining
}

func withLimit(payload []byte, limit int) ([]byte, error) {
	if limit <= 0 {
		return payload, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		return nil, err
	}

	if fields == nil {
		return nil, fmt.Errorf("request must be a JSON object")
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
