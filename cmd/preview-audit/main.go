package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type auditReport struct {
	Name           string       `json:"name"`
	Dataset        string       `json:"dataset"`
	Ordered        bool         `json:"ordered"`
	QueryLimitMS   int64        `json:"queryLimitMs"`
	MemoryLimit    int64        `json:"memoryLimitBytes"`
	FirstGeneric   bool         `json:"firstGeneric"`
	Fast           measurements `json:"fast"`
	Generic        measurements `json:"generic"`
	MatchedPairs   int          `json:"matchedPairs"`
	ResultMismatch bool         `json:"resultMismatch"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	inputPath := flag.String("input", "", "preview workload JSON")
	baseURL := flag.String("arango", "", "owned Arango HTTP endpoint on loopback")
	database := flag.String("database", "", "owned Arango database")
	dataset := flag.String("dataset", "", "dataset identity for the report")
	runs := flag.Int("runs", 9, "number of warm pairs")
	batchSize := flag.Int("batch-size", 10000, "result rows that must fit in one profiled cursor batch")
	timeout := flag.Duration("timeout", 30*time.Second, "server query time limit per sample")
	memory := flag.Int64("memory-limit", 256<<20, "server query memory limit in bytes")
	renderOnly := flag.Bool("render-only", false, "emit the checked query pair without executing it")
	firstGeneric := flag.Bool("generic-first", false, "reverse first pair order")
	flag.Parse()

	input, err := loadWorkload(*inputPath)
	if err != nil {
		return fmt.Errorf("load workload: %w", err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if *renderOnly {
		return encoder.Encode(struct {
			Fast, Generic query
		}{input.Fast, input.Generic})
	}
	endpoint, err := ownedCursorEndpoint(*baseURL, *database)
	if err != nil {
		return err
	}
	if *dataset == "" || *runs < 1 || *batchSize < 1 || *memory < 1 || *timeout < time.Millisecond {
		return fmt.Errorf("dataset and positive run count, batch size, memory limit and timeout are required")
	}
	p := profiler{
		endpoint: endpoint,
		http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
		timeout: *timeout, memoryBytes: *memory, batchSize: *batchSize,
	}
	report := auditReport{
		Name: input.Name, Dataset: *dataset, Ordered: input.Ordered,
		QueryLimitMS: timeout.Milliseconds(), MemoryLimit: *memory, FirstGeneric: *firstGeneric,
		Fast:    measurements{QueryDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(input.Fast.Query)))},
		Generic: measurements{QueryDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(input.Generic.Query)))},
	}
	failed := false
	for i := 0; i <= *runs; i++ {
		var fast, generic sample
		if (i%2 == 1) != *firstGeneric {
			generic = p.run(context.Background(), input.Generic, input.Ordered)
			fast = p.run(context.Background(), input.Fast, input.Ordered)
		} else {
			fast = p.run(context.Background(), input.Fast, input.Ordered)
			generic = p.run(context.Background(), input.Generic, input.Ordered)
		}
		if i == 0 {
			report.Fast.FirstRun, report.Generic.FirstRun = fast, generic
		} else {
			report.Fast.Warm = append(report.Fast.Warm, fast)
			report.Generic.Warm = append(report.Generic.Warm, generic)
		}
		if fast.Error != "" || generic.Error != "" {
			failed = true
			break
		}
		if fast.Digest != generic.Digest || fast.Rows != generic.Rows ||
			fast.Digest != report.Fast.FirstRun.Digest {
			report.ResultMismatch = true
			break
		}
		report.MatchedPairs++
	}
	report.Fast.summarize()
	report.Generic.summarize()
	if err := encoder.Encode(report); err != nil {
		return err
	}
	if report.ResultMismatch {
		return errors.New("preview results differ")
	}
	if failed {
		return errors.New("preview profiling failed; see report")
	}
	return nil
}

func ownedCursorEndpoint(raw, database string) (string, error) {
	endpoint, err := url.Parse(raw)
	if err != nil || endpoint.Scheme != "http" || endpoint.Port() == "" || endpoint.User != nil ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" || (endpoint.Path != "" && endpoint.Path != "/") {
		return "", fmt.Errorf("use an owned loopback Arango HTTP endpoint with a port")
	}
	switch endpoint.Hostname() {
	case "127.0.0.1", "::1", "localhost":
	default:
		return "", fmt.Errorf("Arango endpoint must be on loopback")
	}
	if database == "" || strings.HasPrefix(database, "_") || strings.ContainsAny(database, "/\\") {
		return "", fmt.Errorf("use a dedicated owned Arango database")
	}
	return strings.TrimRight(raw, "/") + "/_db/" + url.PathEscape(database) + "/_api/cursor", nil
}
