package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"slices"
	"time"

	store "github.com/calypr/loom/internal/store/arango"
)

type profiler struct {
	endpoint    string
	http        *http.Client
	timeout     time.Duration
	batchSize   int
	memoryBytes int64
}

type cursorOptions struct {
	Profile    int     `json:"profile"`
	MaxRuntime float64 `json:"maxRuntime"`
}

type cursorRequest struct {
	query
	BatchSize   int           `json:"batchSize"`
	Count       bool          `json:"count"`
	Cache       bool          `json:"cache"`
	MemoryLimit int64         `json:"memoryLimit"`
	Options     cursorOptions `json:"options"`
}

type sample struct {
	WallMS      float64                     `json:"wallMs"`
	Rows        int                         `json:"rows"`
	Digest      string                      `json:"digest,omitempty"`
	PeakMemory  uint64                      `json:"peakMemoryBytes"`
	ScannedFull int                         `json:"scannedFull"`
	ScannedKeys int                         `json:"scannedIndex"`
	Profile     store.ProfileSummary        `json:"profile"`
	Phases      store.ProfilePhases         `json:"phases"`
	Indexes     []store.ExplainIndexSummary `json:"indexes,omitempty"`
	Error       string                      `json:"error,omitempty"`
}

type measurements struct {
	QueryDigest string   `json:"queryDigest"`
	FirstRun    sample   `json:"firstRun"`
	Warm        []sample `json:"warm"`
	P50MS       float64  `json:"warmP50Ms,omitempty"`
	P95MS       float64  `json:"warmP95Ms,omitempty"`
	MaxMS       float64  `json:"warmMaxMs,omitempty"`
}

func (p profiler) run(ctx context.Context, q query, ordered bool) sample {
	result, elapsed, err := p.profile(ctx, q)
	s := sample{WallMS: float64(elapsed) / float64(time.Millisecond)}
	if err != nil {
		s.Error = err.Error()
		return s
	}
	if result.Cached {
		s.Error = "Arango served a cached query result"
		return s
	}
	if result.HasMore || result.Count != len(result.Result) {
		s.Error = fmt.Sprintf("complete result did not fit batch: count=%d, received=%d, hasMore=%t", result.Count, len(result.Result), result.HasMore)
		if result.HasMore && result.ID != "" {
			p.closeCursor(ctx, result.ID)
		}
		return s
	}
	if result.Extra.Stats.WritesExecuted != 0 || result.Extra.Stats.WritesIgnored != 0 {
		s.Error = "query reported writes"
		return s
	}
	s.Profile = store.SummarizeProfile(result)
	if result.Extra.Plan != nil {
		s.Indexes = store.AssessExplainResult(store.ExplainResult{Plan: result.Extra.Plan}).Indexes
	}
	if len(s.Profile.Nodes) == 0 {
		s.Error = "Arango returned no execution-node profile"
		return s
	}
	s.Phases = result.Extra.Profile
	s.Rows = len(result.Result)
	s.Digest, err = digestRows(result.Result, ordered)
	if err != nil {
		s.Error = err.Error()
	}
	s.PeakMemory, s.ScannedFull, s.ScannedKeys = s.Profile.PeakMemory, s.Profile.ScannedFull, s.Profile.ScannedIndex
	return s
}

func (p profiler) profile(ctx context.Context, q query) (store.ProfileResult, time.Duration, error) {
	request := cursorRequest{
		query: q, BatchSize: p.batchSize, Count: true, MemoryLimit: p.memoryBytes,
		Options: cursorOptions{Profile: 2, MaxRuntime: p.timeout.Seconds()},
	}
	body, err := json.Marshal(request)
	if err != nil {
		return store.ProfileResult{}, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout+3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return store.ProfileResult{}, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	start := time.Now()
	response, err := p.http.Do(req)
	if err != nil {
		return store.ProfileResult{}, time.Since(start), err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	elapsed := time.Since(start)
	if err != nil {
		return store.ProfileResult{}, elapsed, err
	}
	if response.StatusCode >= 400 {
		return store.ProfileResult{}, elapsed, fmt.Errorf("HTTP %d: %s", response.StatusCode, raw)
	}
	result, err := store.ParseProfileResult(raw)
	return result, elapsed, err
}

func (p profiler) closeCursor(ctx context.Context, id string) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.endpoint+"/"+id, nil)
	if err != nil {
		return
	}
	response, err := p.http.Do(req)
	if err == nil {
		response.Body.Close()
	}
}

func digestRows(rows []json.RawMessage, ordered bool) (string, error) {
	canonical := make([]string, 0, len(rows))
	for i, row := range rows {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(row))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return "", fmt.Errorf("decode result row %d: %w", i, err)
		}
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		canonical = append(canonical, string(data))
	}
	if !ordered {
		slices.Sort(canonical)
	}
	hash := sha256.New()
	for _, row := range canonical {
		io.WriteString(hash, row)
		io.WriteString(hash, "\n")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (m *measurements) summarize() {
	if len(m.Warm) == 0 {
		return
	}
	timings := make([]float64, 0, len(m.Warm))
	for _, run := range m.Warm {
		if run.Error != "" {
			return
		}
		timings = append(timings, run.WallMS)
	}
	slices.Sort(timings)
	percentile := func(p float64) float64 {
		return timings[int(math.Ceil(p*float64(len(timings))))-1]
	}
	m.P50MS, m.P95MS, m.MaxMS = percentile(0.50), percentile(0.95), timings[len(timings)-1]
}
