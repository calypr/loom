package acceptance

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func testResource(typ, id, version string, extra map[string]any) []byte {
	value := map[string]any{"resourceType": typ, "id": id, "meta": map[string]any{"versionId": version}}
	for key, item := range extra {
		value[key] = item
	}
	raw, _ := json.Marshal(value)
	return canonicalJSON(raw)
}

func lockResource(typ, id, version string, payload []byte) ResourceLock {
	sum := sha256.Sum256(canonicalJSON(payload))
	return ResourceLock{ID: id, Version: version, Digest: "sha256:" + hex.EncodeToString(sum[:])}
}

func testLock(endpoint string, patientIDs []string, resources map[string][]ResourceLock) FixtureLock {
	counts := make(map[string]int, len(resources))
	for typ, values := range resources {
		counts[typ] = len(values)
	}
	lock := FixtureLock{Version: 1, Endpoint: endpoint, Study: ResourceLock{ID: "study-1", Version: "v1", Digest: "sha256:" + strings.Repeat("0", 64)}, Selection: SelectionRule{ResourceType: "Patient", Search: "part-of-study=ResearchStudy/study-1", Order: "id ascending", Limit: len(patientIDs)}, Resources: resources, Counts: counts, Format: "canonical-ndjson-v1"}
	if studies := resources["ResearchStudy"]; len(studies) == 1 {
		lock.Study = studies[0]
	}
	lock.Digest = lock.ComputedDigest()
	return lock
}

type localTestServer struct {
	URL string
	*http.Server
	Listener net.Listener
}

type rejectRoundTripper func(*http.Request) (*http.Response, error)

func (f rejectRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func newLocalTestServer(t *testing.T, handler http.Handler) *localTestServer {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return &localTestServer{URL: "http://" + listener.Addr().String(), Server: server, Listener: listener}
}

func testServer(t *testing.T, patientIDs []string, byType map[string][][]byte, count *atomic.Int64) *localTestServer {
	t.Helper()
	return newLocalTestServer(t, fixtureHandler(byType, count))
}

func fixtureHandler(byType map[string][][]byte, count *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		path := strings.Trim(r.URL.Path, "/")
		parts := strings.Split(path, "/")
		if len(parts) == 2 {
			typ, id := parts[0], parts[1]
			if typ == "ResearchStudy" && id == "study-1" {
				w.Header().Set("Content-Type", "application/fhir+json")
				_, _ = w.Write(byType[typ][0])
				return
			}
		}
		if len(parts) != 1 {
			http.NotFound(w, r)
			return
		}
		typ := parts[0]
		items := byType[typ]
		if typ == "Patient" {
			items = append([][]byte(nil), items...)
		}
		start, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if start < 0 {
			start = 0
		}
		pageSize := 2
		end := start + pageSize
		if end > len(items) {
			end = len(items)
		}
		entries := make([]map[string]any, 0, end-start)
		for _, item := range items[start:end] {
			var v any
			_ = json.Unmarshal(item, &v)
			entries = append(entries, map[string]any{"resource": v})
		}
		bundle := map[string]any{"resourceType": "Bundle", "type": "searchset", "entry": entries}
		if end < len(items) {
			bundle["link"] = []map[string]string{{"relation": "next", "url": fmt.Sprintf("%s/%s?page=%d", countBase(r), typ, end)}}
		}
		w.Header().Set("Content-Type", "application/fhir+json")
		_ = json.NewEncoder(w).Encode(bundle)
	})
}

func countBase(r *http.Request) string { return "http://" + r.Host }

func fixtureFixture(t *testing.T, serverURL string, n int) (FixtureLock, map[string][][]byte) {
	patientIDs := make([]string, n)
	byType := map[string][][]byte{"ResearchStudy": {testResource("ResearchStudy", "study-1", "v1", nil)}}
	for i := range patientIDs {
		patientIDs[i] = fmt.Sprintf("patient-%02d", n-i)
	}
	for _, typ := range []string{"Patient", "Specimen", "Observation", "DocumentReference", "Condition"} {
		for i, patient := range patientIDs {
			id := fmt.Sprintf("%s-%02d", strings.ToLower(typ), i)
			extra := map[string]any{}
			if typ != "Patient" {
				extra["subject"] = map[string]any{"reference": "Patient/" + patient}
			}
			byType[typ] = append(byType[typ], testResource(typ, id, "v1", extra))
		}
	}
	resources := map[string][]ResourceLock{}
	for typ, items := range byType {
		for _, item := range items {
			resources[typ] = append(resources[typ], lockResource(typ, resourceID(item), "v1", item))
		}
	}
	lock := testLock(serverURL, patientIDs, resources)
	return lock, byType
}

func TestFixtureColdMissWarmHitAndCanonicalNDJSON(t *testing.T) {
	var requests atomic.Int64
	server := testServer(t, nil, nil, &requests)
	defer server.Close()
	lock, byType := fixtureFixture(t, server.URL, 4)
	server.Close()
	server = testServer(t, nil, byType, &requests)
	defer server.Close()
	lock.Endpoint = server.URL
	lock.Study.Digest = lockResource("ResearchStudy", "study-1", "v1", byType["ResearchStudy"][0]).Digest
	lock.Digest = lock.ComputedDigest()
	cache := t.TempDir()
	fetcher := &Fetcher{CacheDir: cache, Lock: lock, Limits: Limits{Patients: 4, Resources: 100, Bytes: 1 << 20, Pages: 10}}
	got, err := fetcher.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.MetaDir == "" || got.Counts["Patient"] != 4 {
		t.Fatalf("fixture=%#v", got)
	}
	coldRequests := requests.Load()
	if coldRequests == 0 {
		t.Fatal("cold acquire made no requests")
	}
	if _, err := fetcher.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != coldRequests {
		t.Fatalf("warm acquire made network requests: before=%d after=%d", coldRequests, requests.Load())
	}
	for typ, want := range lock.Counts {
		count, _, err := canonicalFileStats(filepath.Join(got.MetaDir, typ+".ndjson"))
		if err != nil || count != want {
			t.Fatalf("%s count=%d err=%v want=%d", typ, count, err, want)
		}
	}
	first, _ := os.ReadFile(filepath.Join(got.MetaDir, "Patient.ndjson"))
	second := append([]byte(nil), first...)
	if string(first) != string(second) {
		t.Fatal("canonical fixture changed between reads")
	}
}

func TestFixtureWarmCacheRejectsCorruptLockedLine(t *testing.T) {
	var requests atomic.Int64
	server := testServer(t, nil, nil, &requests)
	defer server.Close()
	lock, byType := fixtureFixture(t, server.URL, 2)
	server.Handler = fixtureHandler(byType, &requests)
	fetcher := &Fetcher{CacheDir: t.TempDir(), Lock: lock, Limits: Limits{Patients: 2, Resources: 100, Bytes: 1 << 20, Pages: 10}}
	fixture, err := fetcher.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fixture.MetaDir, "Patient.ndjson")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lineEnd := bytes.IndexByte(raw, '\n')
	if lineEnd < 0 {
		t.Fatal("patient fixture has no NDJSON line")
	}
	var value map[string]any
	if err := json.Unmarshal(raw[:lineEnd], &value); err != nil {
		t.Fatal(err)
	}
	value["id"] = "patient-corrupt"
	corrupt := append(canonicalJSON(mustJSON(t, value)), raw[lineEnd:]...)
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	fetcher.HTTPClient = &http.Client{Transport: rejectRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network disabled for warm-cache verification")
	})}
	if _, err := fetcher.Acquire(context.Background()); err == nil || !strings.Contains(err.Error(), "network disabled for warm-cache verification") {
		t.Fatalf("corrupt cache was accepted or wrong error returned: %v", err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestFixtureLockInvalidationUsesDifferentCacheAddress(t *testing.T) {
	var requests atomic.Int64
	server := testServer(t, nil, nil, &requests)
	defer server.Close()
	lock, byType := fixtureFixture(t, server.URL, 2)
	server.Handler = fixtureHandler(byType, &requests)
	fetcher := &Fetcher{CacheDir: t.TempDir(), Lock: lock, Limits: Limits{Patients: 2, Resources: 100, Bytes: 1 << 20, Pages: 10}}
	if _, err := fetcher.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	old := fetcher.Lock.Digest
	fetcher.Lock.Endpoint = server.URL + "/different"
	fetcher.Lock.Digest = fetcher.Lock.ComputedDigest()
	if old == fetcher.Lock.Digest {
		t.Fatal("lock invalidation did not change digest")
	}
}

func TestFixtureDriftIsRejectedAndNotCommitted(t *testing.T) {
	var requests atomic.Int64
	var drift atomic.Bool
	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		typ := strings.Trim(r.URL.Path, "/")
		if typ == "ResearchStudy/study-1" {
			payload := testResource("ResearchStudy", "study-1", "v2", nil)
			_, _ = w.Write(payload)
			return
		}
		if typ == "Patient" {
			_, _ = fmt.Fprintf(w, `{"entry":[],"resourceType":"Bundle","type":"searchset"}`)
			return
		}
		if drift.Load() {
			_, _ = fmt.Fprintf(w, `{"entry":[],"resourceType":"Bundle","type":"searchset"}`)
			return
		}
		_, _ = fmt.Fprintf(w, `{"entry":[],"resourceType":"Bundle","type":"searchset"}`)
	}))
	defer server.Close()
	payload := testResource("ResearchStudy", "study-1", "v1", nil)
	lock := testLock(server.URL, []string{"p1"}, map[string][]ResourceLock{"ResearchStudy": {lockResource("ResearchStudy", "study-1", "v1", payload)}, "Patient": {lockResource("Patient", "p1", "v1", testResource("Patient", "p1", "v1", nil))}})
	fetcher := &Fetcher{CacheDir: t.TempDir(), Lock: lock, Limits: Limits{Patients: 1, Resources: 10, Bytes: 1 << 20, Pages: 2}}
	if _, err := fetcher.Acquire(context.Background()); !errors.Is(err, ErrFixtureDrift) {
		t.Fatalf("error=%v want drift", err)
	}
	entries, _ := os.ReadDir(fetcher.CacheDir)
	if len(entries) != 0 {
		t.Fatalf("drift left cache entries: %v", entries)
	}
}

func TestFixtureConcurrentCreationIsAtomic(t *testing.T) {
	var requests atomic.Int64
	server := testServer(t, nil, nil, &requests)
	defer server.Close()
	lock, byType := fixtureFixture(t, server.URL, 2)
	server.Handler = fixtureHandler(byType, &requests)
	cache := t.TempDir()
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			f := &Fetcher{CacheDir: cache, Lock: lock, Limits: Limits{Patients: 2, Resources: 100, Bytes: 1 << 20, Pages: 10}}
			_, errs[i] = f.Acquire(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(cache)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".fixture-") {
			t.Fatalf("temporary cache was left behind: %s", entry.Name())
		}
	}
}

func TestFixtureCapsFailBeforeCacheCommit(t *testing.T) {
	var requests atomic.Int64
	server := testServer(t, nil, nil, &requests)
	defer server.Close()
	lock, byType := fixtureFixture(t, server.URL, 2)
	server.Handler = fixtureHandler(byType, &requests)
	lock.Digest = lock.ComputedDigest()
	fetcher := &Fetcher{CacheDir: t.TempDir(), Lock: lock, Limits: Limits{Patients: 2, Resources: 1, Bytes: 1 << 20, Pages: 10}}
	if _, err := fetcher.Acquire(context.Background()); !errors.Is(err, ErrFixtureCap) {
		t.Fatalf("error=%v want cap", err)
	}
	entries, _ := os.ReadDir(fetcher.CacheDir)
	if len(entries) != 0 {
		t.Fatalf("cap failure left cache entries: %v", entries)
	}
}
