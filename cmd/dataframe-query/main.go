// dataframe-query executes a checked-in GraphQL dataframe request and prints
// the response with wall-clock timing. It is deliberately a small developer
// tool: benchmark loops belong in Go benchmarks, while this command makes one
// human-readable dataframe request easy to inspect end to end.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type graphqlRequest struct {
	Query     string          `json:"query"`
	Variables json.RawMessage `json:"variables"`
}

func main() {
	var (
		url           string
		queryPath     string
		variablesPath string
		repeat        int
		limit         int
		timeout       time.Duration
		printResponse bool
	)
	flag.StringVar(&url, "url", "http://127.0.0.1:8080/graphql", "GraphQL endpoint")
	flag.StringVar(&queryPath, "query", "examples/meta_patient_dataframe.graphql", "GraphQL operation file")
	flag.StringVar(&variablesPath, "variables", "examples/meta_patient_dataframe.variables.json", "GraphQL variables JSON file")
	flag.IntVar(&repeat, "repeat", 1, "number of sequential requests; only the final response is printed")
	flag.IntVar(&limit, "limit", 0, "override GraphQL row limit; 0 preserves the variables file")
	flag.DurationVar(&timeout, "timeout", 60*time.Second, "per-request HTTP timeout")
	flag.BoolVar(&printResponse, "print-response", true, "pretty-print the final GraphQL response")
	flag.Parse()

	if repeat < 1 {
		fatalf("repeat must be positive")
	}
	variables, err := os.ReadFile(variablesPath)
	if err != nil {
		fatalf("read variables %q: %v", variablesPath, err)
	}
	if !json.Valid(variables) {
		fatalf("variables file %q is not valid JSON", variablesPath)
	}
	if limit > 0 {
		variables, err = withLimit(variables, limit)
		if err != nil {
			fatalf("override row limit: %v", err)
		}
	}
	query, err := os.ReadFile(queryPath)
	if err != nil {
		fatalf("read query %q: %v", queryPath, err)
	}
	payload, err := json.Marshal(graphqlRequest{Query: string(query), Variables: variables})
	if err != nil {
		fatalf("encode GraphQL request: %v", err)
	}

	client := &http.Client{Timeout: timeout}

	durations := make([]time.Duration, 0, repeat)
	var responseBody []byte
	var responseMetrics dataframeResponseMetrics
	for run := 0; run < repeat; run++ {
		started := time.Now()
		responseBody, err = execute(client, url, payload)
		duration := time.Since(started)
		durations = append(durations, duration)
		if err != nil {
			fatalf("request %d/%d: %v", run+1, repeat, err)
		}
		responseMetrics = inspectDataframeResponse(responseBody, duration)
	}

	min, average, max := summarizeDurations(durations)
	fmt.Printf("GraphQL dataframe request: %s\n", url)
	fmt.Printf("HTTP/server total: runs=%d  cold=%s  warm=%s  min=%s  avg=%s  max=%s\n", repeat, durations[0].Round(time.Microsecond), durations[len(durations)-1].Round(time.Microsecond), min.Round(time.Microsecond), average.Round(time.Microsecond), max.Round(time.Microsecond))
	fmt.Printf("Response: rows=%d  bytes=%d  rows/sec=%.1f\n\n", responseMetrics.Rows, responseMetrics.Bytes, responseMetrics.RowsPerSecond)
	if responseMetrics.Diagnostics != nil {
		d := responseMetrics.Diagnostics
		fmt.Printf("Server stages (ms): input_resolution=%.3f  request_preparation=%.3f  compilation=%.3f  arango_query=%.3f  row_materialization=%.3f  result_assembly=%.3f  dataframe_service_total=%.3f\n", d.InputResolutionMs, d.RequestPreparationMs, d.CompilationMs, d.ArangoQueryMs, d.RowMaterializationMs, d.ResultAssemblyMs, d.TotalMs)
		fmt.Printf("Outside dataframe service (GraphQL serialization + HTTP): %.3f ms\n\n", durationMinusMillis(durations[len(durations)-1], d.TotalMs))
		fmt.Printf("Compiler plan: traversal_sets=%d  shared_traversals=%d  required_match_reuse=%d  scope_safe_sharing_groups=%d  scope_safe_sharing_sets=%d  potential_sharing_groups=%d  potential_sharing_sets=%d\n", d.Plan.TraversalSets, d.Plan.SharedTraversalCount, d.Plan.RequiredMatchReuseCount, d.Plan.ScopedSharingCandidateGroups, d.Plan.ScopedSharingCandidateSets, d.Plan.PotentialSharingOpportunityGroups, d.Plan.PotentialSharingOpportunitySets)
		for _, reuse := range d.Plan.RichSourceReuse {
			fmt.Printf("  rich source reuse: set=%s  total=%d  aggregates=%d  pivots=%d  slices=%d\n", reuse.SourceSet, reuse.TotalConsumers, reuse.AggregateConsumers, reuse.PivotConsumers, reuse.SliceConsumers)
		}
		fmt.Println()
	}
	if printResponse {
		fmt.Println("Response:")
		if err := prettyPrint(os.Stdout, responseBody); err != nil {
			fatalf("format GraphQL response: %v", err)
		}
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

func durationMinusMillis(duration time.Duration, milliseconds float64) float64 {
	remaining := float64(duration)/float64(time.Millisecond) - milliseconds
	if remaining < 0 {
		return 0
	}
	return remaining
}

func withLimit(variables []byte, limit int) ([]byte, error) {
	if limit <= 0 {
		return variables, nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(variables, &decoded); err != nil {
		return nil, err
	}
	decoded["limit"] = limit
	return json.Marshal(decoded)
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

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "dataframe-query: "+format+"\n", args...)
	os.Exit(1)
}
