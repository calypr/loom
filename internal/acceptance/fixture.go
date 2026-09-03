package acceptance

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultFHIRBase            = "https://google-fhir.fhir-aggregator.org"
	DefaultStudyID             = "638f6162-e000-5167-8216-962d86b74a98"
	DefaultPatientLimit        = 100
	DefaultResourceLimit       = 50000
	DefaultBytesLimit    int64 = 512 << 20
	DefaultPageLimit           = 250
	DefaultFetchTimeout        = 5 * time.Minute
)

// FixtureDigest identifies both a fixture lock and its content-addressed cache directory.
type FixtureDigest string

type RunID string

type ResourceLock struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}

type SelectionRule struct {
	ResourceType string `json:"resource_type"`
	Search       string `json:"search"`
	Order        string `json:"order"`
	Limit        int    `json:"limit"`
}

// FixtureLock is intentionally metadata-only.  Resources contains the exact
// IDs, upstream version IDs, and canonical payload digests needed to detect
// drift without redistributing potentially restricted FHIR payloads.
type FixtureLock struct {
	Version   int                       `json:"version"`
	Endpoint  string                    `json:"endpoint"`
	Study     ResourceLock              `json:"study"`
	Selection SelectionRule             `json:"selection"`
	Resources map[string][]ResourceLock `json:"resources"`
	Counts    map[string]int            `json:"counts"`
	Format    string                    `json:"format"`
	Digest    FixtureDigest             `json:"lock_digest"`
}

type Limits struct {
	Patients  int
	Resources int
	Bytes     int64
	Pages     int
	Timeout   time.Duration
}

func DefaultLimits() Limits {
	return Limits{Patients: DefaultPatientLimit, Resources: DefaultResourceLimit, Bytes: DefaultBytesLimit, Pages: DefaultPageLimit, Timeout: DefaultFetchTimeout}
}

func (l Limits) normalize() Limits {
	d := DefaultLimits()
	if l.Patients <= 0 {
		l.Patients = d.Patients
	}
	if l.Resources <= 0 {
		l.Resources = d.Resources
	}
	if l.Bytes <= 0 {
		l.Bytes = d.Bytes
	}
	if l.Pages <= 0 {
		l.Pages = d.Pages
	}
	if l.Timeout <= 0 {
		l.Timeout = d.Timeout
	}
	return l
}

type ValidatedFixture struct {
	Digest  FixtureDigest
	MetaDir string
	Counts  map[string]int
}

type Fetcher struct {
	HTTPClient *http.Client
	CacheDir   string
	Lock       FixtureLock
	Limits     Limits
	mu         sync.Mutex
}

var (
	ErrFixtureDrift       = errors.New("upstream fixture drift")
	ErrFixtureCap         = errors.New("fixture cap exceeded")
	ErrFixtureLockInvalid = errors.New("invalid fixture lock")
)

func LoadLock(path string) (FixtureLock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return FixtureLock{}, fmt.Errorf("read fixture lock: %w", err)
	}
	var lock FixtureLock
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lock); err != nil {
		return FixtureLock{}, fmt.Errorf("decode fixture lock: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return FixtureLock{}, fmt.Errorf("fixture lock has trailing JSON")
	}
	if err := lock.Validate(); err != nil {
		return FixtureLock{}, err
	}
	return lock, nil
}

func (l FixtureLock) Validate() error {
	if l.Version != 1 {
		return fmt.Errorf("%w: unsupported version %d", ErrFixtureLockInvalid, l.Version)
	}
	if strings.TrimSpace(l.Endpoint) == "" || strings.TrimSpace(l.Study.ID) == "" || strings.TrimSpace(l.Study.Version) == "" || !strings.HasPrefix(l.Study.Digest, "sha256:") {
		return fmt.Errorf("%w: endpoint and study identity are required", ErrFixtureLockInvalid)
	}
	if l.Selection.ResourceType != "Patient" || l.Selection.Limit <= 0 || strings.TrimSpace(l.Selection.Search) == "" {
		return fmt.Errorf("%w: patient selection rule is incomplete", ErrFixtureLockInvalid)
	}
	if l.Format != "canonical-ndjson-v1" {
		return fmt.Errorf("%w: unsupported fixture format %q", ErrFixtureLockInvalid, l.Format)
	}
	if len(l.Resources) == 0 || len(l.Counts) == 0 {
		return fmt.Errorf("%w: resources and counts are required", ErrFixtureLockInvalid)
	}
	for typ, resources := range l.Resources {
		if typ == "" || l.Counts[typ] != len(resources) {
			return fmt.Errorf("%w: %s count does not match resource lock", ErrFixtureLockInvalid, typ)
		}
		seen := make(map[string]bool, len(resources))
		for _, resource := range resources {
			if strings.TrimSpace(resource.ID) == "" || strings.TrimSpace(resource.Version) == "" || !strings.HasPrefix(resource.Digest, "sha256:") {
				return fmt.Errorf("%w: %s contains incomplete resource identity", ErrFixtureLockInvalid, typ)
			}
			if seen[resource.ID] {
				return fmt.Errorf("%w: %s contains duplicate id %q", ErrFixtureLockInvalid, typ, resource.ID)
			}
			seen[resource.ID] = true
		}
	}
	if l.Counts["Patient"] != l.Selection.Limit {
		return fmt.Errorf("%w: patient count %d != selection limit %d", ErrFixtureLockInvalid, l.Counts["Patient"], l.Selection.Limit)
	}
	if strings.TrimSpace(string(l.Digest)) == "" {
		return fmt.Errorf("%w: lock_digest is required", ErrFixtureLockInvalid)
	}
	if got := l.ComputedDigest(); got != l.Digest {
		return fmt.Errorf("%w: lock_digest %q != %q", ErrFixtureLockInvalid, l.Digest, got)
	}
	return nil
}

func (l FixtureLock) ComputedDigest() FixtureDigest {
	copy := l
	copy.Digest = ""
	raw, _ := json.Marshal(copy)
	sum := sha256.Sum256(raw)
	return FixtureDigest("sha256:" + hex.EncodeToString(sum[:]))
}

func (f *Fetcher) Acquire(ctx context.Context) (ValidatedFixture, error) {
	if f == nil {
		return ValidatedFixture{}, errors.New("fixture fetcher is nil")
	}
	if err := f.Lock.Validate(); err != nil {
		return ValidatedFixture{}, err
	}
	if strings.TrimSpace(f.CacheDir) == "" {
		return ValidatedFixture{}, errors.New("fixture cache directory is required")
	}
	limits := f.Limits.normalize()
	digest := f.Lock.Digest
	cache := filepath.Join(f.CacheDir, strings.TrimPrefix(string(digest), "sha256:"))
	f.mu.Lock()
	defer f.mu.Unlock()
	if fixture, err := validateCache(cache, f.Lock, limits); err == nil {
		return fixture, nil
	}
	if err := os.MkdirAll(f.CacheDir, 0o755); err != nil {
		return ValidatedFixture{}, fmt.Errorf("create fixture cache: %w", err)
	}
	// Every process writes a unique temporary directory. The lock is held only
	// in-process; the rename is atomic across processes and an already-created
	// valid directory wins a race harmlessly.
	tmp, err := os.MkdirTemp(f.CacheDir, ".fixture-")
	if err != nil {
		return ValidatedFixture{}, fmt.Errorf("create fixture staging directory: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := f.fetchInto(ctx, tmp, limits); err != nil {
		return ValidatedFixture{}, err
	}
	if err := writeCacheMetadata(tmp, f.Lock); err != nil {
		return ValidatedFixture{}, err
	}
	validated, err := validateCache(tmp, f.Lock, limits)
	if err != nil {
		return ValidatedFixture{}, err
	}
	if existing, err := validateCache(cache, f.Lock, limits); err == nil {
		return existing, nil
	}
	if err := os.Rename(tmp, cache); err != nil {
		if existing, checkErr := validateCache(cache, f.Lock, limits); checkErr == nil {
			return existing, nil
		}
		return ValidatedFixture{}, fmt.Errorf("commit fixture cache: %w", err)
	}
	return ValidatedFixture{Digest: digest, MetaDir: filepath.Join(cache, "meta"), Counts: validated.Counts}, nil
}

func validateCache(cache string, lock FixtureLock, limits Limits) (ValidatedFixture, error) {
	if lock.Selection.Limit > limits.Patients {
		return ValidatedFixture{}, fmt.Errorf("%w: patients=%d want at most %d", ErrFixtureCap, lock.Selection.Limit, limits.Patients)
	}
	meta := filepath.Join(cache, "meta")
	if _, err := os.Stat(meta); err != nil {
		return ValidatedFixture{}, err
	}
	marker, err := os.ReadFile(filepath.Join(cache, "fixture.json"))
	if err != nil {
		return ValidatedFixture{}, err
	}
	var got struct {
		Digest FixtureDigest  `json:"digest"`
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(marker, &got); err != nil || got.Digest != lock.Digest {
		return ValidatedFixture{}, fmt.Errorf("fixture cache marker mismatch: digest=%q want=%q err=%v", got.Digest, lock.Digest, err)
	}
	if !sameCounts(got.Counts, lock.Counts) {
		return ValidatedFixture{}, fmt.Errorf("fixture cache marker counts mismatch: got=%v want=%v", got.Counts, lock.Counts)
	}
	total, totalBytes := 0, int64(0)
	for typ, want := range lock.Counts {
		path := filepath.Join(meta, typ+".ndjson")
		count, bytes, err := validateCanonicalNDJSON(path, typ, lock.Resources[typ], &total, &totalBytes, limits)
		if err != nil || count != want {
			return ValidatedFixture{}, fmt.Errorf("fixture cache contents mismatch for %s: count=%d want=%d bytes=%d err=%v", typ, count, want, bytes, err)
		}
	}
	entries, err := os.ReadDir(meta)
	if err != nil {
		return ValidatedFixture{}, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".ndjson") {
			continue
		}
		typ := strings.TrimSuffix(name, ".ndjson")
		if _, ok := lock.Counts[typ]; !ok {
			return ValidatedFixture{}, fmt.Errorf("%w: unexpected cached resource type %s", ErrFixtureDrift, typ)
		}
	}
	return ValidatedFixture{Digest: lock.Digest, MetaDir: meta, Counts: lock.Counts}, nil
}

func validateCanonicalNDJSON(path, typ string, locks []ResourceLock, total *int, totalBytes *int64, limits Limits) (int, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	byID := make(map[string]ResourceLock, len(locks))
	for _, lock := range locks {
		byID[lock.ID] = lock
	}
	seen := make(map[string]bool, len(locks))
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	count := 0
	var bytes int64
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			return count, bytes, fmt.Errorf("%w: %s contains a blank NDJSON line", ErrFixtureDrift, typ)
		}
		canonical := canonicalJSON([]byte(line))
		if string(canonical) != line {
			return count, bytes, fmt.Errorf("%w: %s contains non-canonical NDJSON", ErrFixtureDrift, typ)
		}
		var value struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(canonical, &value); err != nil {
			return count, bytes, fmt.Errorf("%w: decode %s cache line: %v", ErrFixtureDrift, typ, err)
		}
		lock, ok := byID[value.ID]
		if !ok {
			return count, bytes, fmt.Errorf("%w: unexpected %s/%s in cache", ErrFixtureDrift, typ, value.ID)
		}
		if seen[value.ID] {
			return count, bytes, fmt.Errorf("%w: duplicate %s/%s in cache", ErrFixtureDrift, typ, value.ID)
		}
		if err := (&Fetcher{}).checkLocked(typ, canonical, lock); err != nil {
			return count, bytes, err
		}
		seen[value.ID] = true
		lineBytes := int64(len(line)) + 1
		count++
		bytes += lineBytes
		*total++
		*totalBytes += lineBytes
		if *total > limits.Resources || *totalBytes > limits.Bytes {
			return count, bytes, fmt.Errorf("%w: resources=%d bytes=%d", ErrFixtureCap, *total, *totalBytes)
		}
	}
	if err := scanner.Err(); err != nil {
		return count, bytes, err
	}
	if len(seen) != len(locks) {
		return count, bytes, fmt.Errorf("%w: %s cache IDs=%d want=%d", ErrFixtureDrift, typ, len(seen), len(locks))
	}
	return count, bytes, nil
}

func sameCounts(got, want map[string]int) bool {
	if len(got) != len(want) {
		return false
	}
	for typ, count := range want {
		if got[typ] != count {
			return false
		}
	}
	return true
}

func writeCacheMetadata(dir string, lock FixtureLock) error {
	marker := struct {
		Digest FixtureDigest  `json:"digest"`
		Counts map[string]int `json:"counts"`
	}{lock.Digest, lock.Counts}
	raw, _ := json.Marshal(marker)
	return os.WriteFile(filepath.Join(dir, "fixture.json"), append(raw, '\n'), 0o644)
}

func (f *Fetcher) fetchInto(ctx context.Context, dir string, limits Limits) error {
	ctx, cancel := context.WithTimeout(ctx, limits.Timeout)
	defer cancel()
	if f.HTTPClient == nil {
		f.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	endpoint := strings.TrimRight(f.Lock.Endpoint, "/")
	resources := make(map[string]map[string]json.RawMessage)
	add := func(typ string, payload json.RawMessage) error {
		if len(resources) == 0 {
			resources = make(map[string]map[string]json.RawMessage)
		}
		var value map[string]any
		if err := json.Unmarshal(payload, &value); err != nil {
			return fmt.Errorf("decode %s resource: %w", typ, err)
		}
		id, _ := value["id"].(string)
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("%s resource is missing id", typ)
		}
		if actual, _ := value["resourceType"].(string); actual != "" && actual != typ {
			return fmt.Errorf("resource %s/%s declares type %s", typ, id, actual)
		}
		if resources[typ] == nil {
			resources[typ] = map[string]json.RawMessage{}
		}
		if _, exists := resources[typ][id]; exists {
			return nil
		}
		resources[typ][id] = canonicalJSON(payload)
		return nil
	}
	study, err := f.getResource(ctx, endpoint, "ResearchStudy", f.Lock.Study.ID)
	if err != nil {
		return err
	}
	if err := f.checkLocked("ResearchStudy", study, f.Lock.Study); err != nil {
		return err
	}
	if err := add("ResearchStudy", study); err != nil {
		return err
	}
	patients, err := f.search(ctx, endpoint, "Patient", url.Values{"part-of-study": {"ResearchStudy/" + f.Lock.Study.ID}, "_count": {strconv.Itoa(minInt(limits.Patients, 1000))}, "_total": {"accurate"}}, limits.Pages)
	if err != nil {
		return err
	}
	sort.Slice(patients, func(i, j int) bool { return resourceID(patients[i]) < resourceID(patients[j]) })
	if len(patients) < f.Lock.Selection.Limit {
		return fmt.Errorf("%w: upstream returned %d patients, need %d", ErrFixtureDrift, len(patients), f.Lock.Selection.Limit)
	}
	selected := make(map[string]bool, f.Lock.Selection.Limit)
	for _, patient := range patients[:f.Lock.Selection.Limit] {
		selected[resourceID(patient)] = true
		if err := add("Patient", patient); err != nil {
			return err
		}
	}
	for typ, parameter := range map[string]string{"Specimen": "subject", "Observation": "subject", "DocumentReference": "subject", "Condition": "subject"} {
		ids := sortedKeys(selected)
		for start := 0; start < len(ids); start += 25 {
			end := minInt(start+25, len(ids))
			refs := make([]string, end-start)
			for i := range refs {
				refs[i] = "Patient/" + ids[start+i]
			}
			values := url.Values{parameter: {strings.Join(refs, ",")}, "_count": {"1000"}, "_total": {"accurate"}}
			items, err := f.search(ctx, endpoint, typ, values, limits.Pages)
			if err != nil {
				return err
			}
			for _, item := range items {
				if err := add(typ, item); err != nil {
					return err
				}
			}
		}
	}
	if err := f.writeCanonical(dir, resources, limits); err != nil {
		return err
	}
	return nil
}

func (f *Fetcher) checkLocked(typ string, payload json.RawMessage, expected ResourceLock) error {
	canonical := canonicalJSON(payload)
	sum := sha256.Sum256(canonical)
	got := "sha256:" + hex.EncodeToString(sum[:])
	var value struct {
		ID   string `json:"id"`
		Meta struct {
			VersionID string `json:"versionId"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(canonical, &value); err != nil {
		return err
	}
	if value.ID != expected.ID || value.Meta.VersionID != expected.Version || got != expected.Digest {
		return fmt.Errorf("%w: %s/%s expected version=%s digest=%s got version=%s digest=%s", ErrFixtureDrift, typ, expected.ID, expected.Version, expected.Digest, value.Meta.VersionID, got)
	}
	return nil
}

func (f *Fetcher) writeCanonical(dir string, resources map[string]map[string]json.RawMessage, limits Limits) error {
	if len(resources) == 0 {
		return errors.New("fixture contains no resources")
	}
	total, totalBytes := 0, int64(0)
	for typ, locks := range f.Lock.Resources {
		values := resources[typ]
		if values == nil {
			values = map[string]json.RawMessage{}
		}
		if len(values) != len(locks) {
			return fmt.Errorf("%w: %s count expected %d got %d", ErrFixtureDrift, typ, len(locks), len(values))
		}
		byID := make(map[string]ResourceLock, len(locks))
		for _, lock := range locks {
			byID[lock.ID] = lock
		}
		ids := sortedRawKeys(values)
		var payloadBytes int64
		for _, id := range ids {
			lock, exists := byID[id]
			if !exists {
				return fmt.Errorf("%w: unexpected %s/%s", ErrFixtureDrift, typ, id)
			}
			if err := f.checkLocked(typ, values[id], lock); err != nil {
				return err
			}
			payloadBytes += int64(len(values[id])) + 1
			total++
			totalBytes += int64(len(values[id])) + 1
			if total > limits.Resources || totalBytes > limits.Bytes {
				return fmt.Errorf("%w: resource=%d bytes=%d", ErrFixtureCap, total, totalBytes)
			}
		}
		if err := os.MkdirAll(filepath.Join(dir, "meta"), 0o755); err != nil {
			return err
		}
		file, err := os.OpenFile(filepath.Join(dir, "meta", typ+".ndjson"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		w := bufio.NewWriter(file)
		for _, id := range ids {
			if _, err := w.Write(append(values[id], '\n')); err != nil {
				_ = file.Close()
				return err
			}
		}
		if err := w.Flush(); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
	}
	for typ := range resources {
		if _, ok := f.Lock.Resources[typ]; !ok {
			return fmt.Errorf("%w: lock has no %s resources", ErrFixtureDrift, typ)
		}
	}
	if total > limits.Resources {
		return fmt.Errorf("%w: resources=%d", ErrFixtureCap, total)
	}
	return nil
}

func (f *Fetcher) getResource(ctx context.Context, endpoint, typ, id string) (json.RawMessage, error) {
	u := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(typ) + "/" + url.PathEscape(id)
	return f.request(ctx, u)
}

func (f *Fetcher) search(ctx context.Context, endpoint, typ string, values url.Values, pageLimit int) ([]json.RawMessage, error) {
	u := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(typ) + "?" + values.Encode()
	var all []json.RawMessage
	for page := 0; page < pageLimit; page++ {
		body, err := f.request(ctx, u)
		if err != nil {
			return nil, err
		}
		var bundle struct {
			Entry []struct {
				Resource json.RawMessage `json:"resource"`
			} `json:"entry"`
			Link []struct {
				Relation string `json:"relation"`
				URL      string `json:"url"`
			} `json:"link"`
		}
		if err := json.Unmarshal(body, &bundle); err != nil {
			return nil, fmt.Errorf("decode %s Bundle: %w", typ, err)
		}
		for _, entry := range bundle.Entry {
			if len(entry.Resource) > 0 {
				all = append(all, canonicalJSON(entry.Resource))
			}
		}
		next := ""
		for _, link := range bundle.Link {
			if link.Relation == "next" {
				next = link.URL
				break
			}
		}
		if next == "" {
			return all, nil
		}
		u = next
	}
	return nil, fmt.Errorf("%w: %s exceeded %d result pages", ErrFixtureCap, typ, pageLimit)
}

func (f *Fetcher) request(ctx context.Context, target string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/fhir+json")
	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("FHIR request: %w", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("FHIR request %s: HTTP %s: %s", target, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func canonicalJSON(raw []byte) []byte {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return append([]byte(nil), raw...)
	}
	out, _ := json.Marshal(value)
	return out
}
func canonicalFileStats(path string) (int, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 64<<20)
	count := 0
	var bytes int64
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		canonical := canonicalJSON([]byte(line))
		if string(canonical) != line {
			return 0, 0, errors.New("non-canonical NDJSON")
		}
		count++
		bytes += int64(len(line)) + 1
	}
	return count, bytes, scanner.Err()
}
func resourceID(raw []byte) string {
	var value struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(raw, &value)
	return value.ID
}
func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func sortedRawKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// RefreshLock discovers a new metadata-only lock. It is intentionally an
// explicit operation: normal acceptance runs call LoadLock and never rewrite
// the checked-in oracle or lock after an upstream response changes.
func RefreshLock(ctx context.Context, endpoint, studyID, path string, client *http.Client, limits Limits) (FixtureLock, error) {
	limits = limits.normalize()
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" || strings.TrimSpace(studyID) == "" || strings.TrimSpace(path) == "" {
		return FixtureLock{}, errors.New("fixture refresh requires endpoint, study ID, and output path")
	}
	f := &Fetcher{HTTPClient: client, CacheDir: filepath.Dir(path), Limits: limits}
	if f.HTTPClient == nil {
		f.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	study, err := f.getResource(ctx, endpoint, "ResearchStudy", studyID)
	if err != nil {
		return FixtureLock{}, err
	}
	resources := map[string][]json.RawMessage{"ResearchStudy": {canonicalJSON(study)}}
	patients, err := f.search(ctx, endpoint, "Patient", url.Values{"part-of-study": {"ResearchStudy/" + studyID}, "_count": {strconv.Itoa(minInt(limits.Patients, 1000))}, "_total": {"accurate"}}, limits.Pages)
	if err != nil {
		return FixtureLock{}, err
	}
	sort.Slice(patients, func(i, j int) bool { return resourceID(patients[i]) < resourceID(patients[j]) })
	if len(patients) < limits.Patients {
		return FixtureLock{}, fmt.Errorf("%w: upstream returned %d patients, need %d", ErrFixtureDrift, len(patients), limits.Patients)
	}
	resources["Patient"] = append(resources["Patient"], patients[:limits.Patients]...)
	ids := make([]string, limits.Patients)
	for i := range ids {
		ids[i] = resourceID(patients[i])
	}
	sort.Strings(ids)
	for typ, parameter := range map[string]string{"Specimen": "subject", "Observation": "subject", "DocumentReference": "subject", "Condition": "subject"} {
		if resources[typ] == nil {
			resources[typ] = []json.RawMessage{}
		}
		for start := 0; start < len(ids); start += 25 {
			end := minInt(start+25, len(ids))
			refs := make([]string, end-start)
			for i := range refs {
				refs[i] = "Patient/" + ids[start+i]
			}
			values := url.Values{parameter: {strings.Join(refs, ",")}, "_count": {"1000"}, "_total": {"accurate"}}
			items, err := f.search(ctx, endpoint, typ, values, limits.Pages)
			if err != nil {
				return FixtureLock{}, err
			}
			resources[typ] = append(resources[typ], items...)
		}
	}
	lock := FixtureLock{Version: 1, Endpoint: endpoint, Study: identityFor("ResearchStudy", study), Selection: SelectionRule{ResourceType: "Patient", Search: "part-of-study=ResearchStudy/" + studyID, Order: "id ascending", Limit: limits.Patients}, Resources: make(map[string][]ResourceLock, len(resources)), Counts: make(map[string]int, len(resources)), Format: "canonical-ndjson-v1"}
	for typ, items := range resources {
		lock.Resources[typ] = []ResourceLock{}
		seen := map[string]bool{}
		for _, item := range items {
			identity := identityFor(typ, item)
			if identity.ID == "" || seen[identity.ID] {
				continue
			}
			seen[identity.ID] = true
			lock.Resources[typ] = append(lock.Resources[typ], identity)
		}
		sort.Slice(lock.Resources[typ], func(i, j int) bool { return lock.Resources[typ][i].ID < lock.Resources[typ][j].ID })
		lock.Counts[typ] = len(lock.Resources[typ])
	}
	lock.Digest = lock.ComputedDigest()
	if err := WriteLock(path, lock); err != nil {
		return FixtureLock{}, err
	}
	return lock, nil
}

func identityFor(typ string, payload []byte) ResourceLock {
	canonical := canonicalJSON(payload)
	var value struct {
		ID   string `json:"id"`
		Meta struct {
			VersionID string `json:"versionId"`
		} `json:"meta"`
	}
	_ = json.Unmarshal(canonical, &value)
	sum := sha256.Sum256(canonical)
	return ResourceLock{ID: value.ID, Version: value.Meta.VersionID, Digest: "sha256:" + hex.EncodeToString(sum[:])}
}

func WriteLock(path string, lock FixtureLock) error {
	if err := lock.Validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
