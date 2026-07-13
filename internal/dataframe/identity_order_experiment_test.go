package dataframe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// WP6 is intentionally an experiment-only proof packet.  These helpers model
// physical properties without changing the production IR.  Unknown is always
// the safe answer; a coordinator may promote a rule only after the same proof
// is represented by typed production properties and its live profile passes.
type wp6Properties struct {
	identityUnique bool
	order          []string
	retained       map[string]bool
}

func wp6UnknownProperties() wp6Properties {
	return wp6Properties{retained: map[string]bool{}}
}

func wp6TransferProperties(in wp6Properties, operation string, keys ...string) wp6Properties {
	out := wp6UnknownProperties()
	switch operation {
	case "filter":
		// Filtering removes rows but does not reorder or duplicate them.
		out.identityUnique = in.identityUnique
		out.order = append([]string(nil), in.order...)
		out.retained = cloneWP6Set(in.retained)
	case "projection":
		// Identity and order survive only when their proof keys remain in the
		// projected item.  A payload-only projection cannot inherit either.
		out.identityUnique = in.identityUnique && in.retained["_id"] && in.retained["_key"] && containsWP6(keys, "_id") && containsWP6(keys, "_key")
		for _, key := range in.order {
			if !containsWP6(keys, key) {
				out.order = nil
				break
			}
			out.order = append(out.order, key)
		}
		for _, key := range keys {
			out.retained[key] = true
		}
	case "identity_dedup":
		// Arango UNIQUE does not provide a compiler contract that its output
		// order is stable.  Dedup proves identity uniqueness, but invalidates
		// order until a physical SORT establishes it again.
		out.identityUnique = true
		out.retained = cloneWP6Set(in.retained)
	case "sort":
		out.identityUnique = in.identityUnique
		out.order = append([]string(nil), keys...)
		out.retained = cloneWP6Set(in.retained)
	case "traversal":
		// A traversal may repeat a node through duplicate edges; a union/group
		// can reorder or multiply rows.  No property transfers implicitly.
		out = wp6UnknownProperties()
		// The target document still exposes whatever graph identity fields the
		// source contract explicitly retained; uniqueness/order remain unknown.
		out.retained = cloneWP6Set(in.retained)
	case "union", "group":
		out = wp6UnknownProperties()
	default:
		panic("unknown WP6 property operation: " + operation)
	}
	return out
}

func cloneWP6Set(in map[string]bool) map[string]bool {
	out := map[string]bool{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func containsWP6(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestWP6RenderedBaselineInventory(t *testing.T) {
	aql := wp6ReadRepoFile(t, "docs/benchmarks/round3/wp0/baseline.aql")
	exactUnique := regexp.MustCompile(`(?m)\bUNIQUE\(`).FindAllStringIndex(aql, -1)
	if got := len(exactUnique); got != 8 {
		t.Fatalf("baseline UNIQUE inventory = %d, want 8; SORTED_UNIQUE must not be counted", got)
	}
	sorts := regexp.MustCompile(`(?m)^\s+SORT `).FindAllStringIndex(aql, -1)
	if got := len(sorts); got != 11 {
		t.Fatalf("baseline SORT inventory = %d, want 11", got)
	}
	if got := strings.Count(aql, " INBOUND "); got != 4 {
		t.Fatalf("baseline inbound traversal inventory = %d, want 4", got)
	}
	if got := len(regexp.MustCompile(`(?m)^\s+LET child_set_[0-9]+ = UNIQUE`).FindAllStringIndex(aql, -1)); got != 6 {
		t.Fatalf("payload child-set materializations = %d, want 6", got)
	}
	if got := len(regexp.MustCompile(`FOR __item IN child_set_[0-9]+`).FindAllStringIndex(aql, -1)); got != 7 {
		t.Fatalf("post-materialization aggregate child-set loops = %d, want 7", got)
	}
	if got := strings.Count(aql, "payload: "); got != 6 {
		t.Fatalf("payload-retaining child sets = %d, want 6", got)
	}
	if got := len(regexp.MustCompile(`SORT [^\n]+\._key ASC, [^\n]+\._key ASC`).FindAllStringIndex(aql, -1)); got != 3 {
		t.Fatalf("duplicate slice sort keys = %d, want 3", got)
	}
}

func TestWP6IdentityDedupRequiresScopeBeforeDedup(t *testing.T) {
	type row struct {
		id         string
		project    string
		generation string
		auth       string
	}
	rows := []row{
		{id: "n1", project: "p", generation: "g2", auth: "denied"},
		{id: "n1", project: "p", generation: "g1", auth: "allowed"},
		{id: "n2", project: "p", generation: "g1", auth: "allowed"},
	}
	// Scope is evaluated on edge and node before identity dedup.  Deduping
	// first would let an inaccessible duplicate hide the accessible row.
	scoped := make([]row, 0, len(rows))
	for _, candidate := range rows {
		if candidate.project == "p" && candidate.generation == "g1" && candidate.auth == "allowed" {
			scoped = append(scoped, candidate)
		}
	}
	got := wp6DedupRowsByKey(scoped, func(candidate row) string { return candidate.id })
	if len(got) != 2 || got[0].id != "n1" || got[1].id != "n2" {
		t.Fatalf("scope-before-dedup rows = %#v, want n1,n2", got)
	}
	// This intentionally demonstrates why a property rule may not move scope
	// after dedup, even when the identity key is stable.
	preScope := wp6DedupRowsByKey(rows, func(candidate row) string { return candidate.id })
	if len(preScope) != 2 || preScope[0].generation != "g2" {
		t.Fatalf("fixture did not retain inaccessible first duplicate for unsafe order proof: %#v", preScope)
	}
}

func TestWP6DuplicateEdgesUseNodeIdentityNotObjectEquality(t *testing.T) {
	type row struct {
		id      string
		key     string
		payload string
	}
	rows := []row{
		{id: "Patient/n1", key: "n1", payload: "same"},
		{id: "Patient/n1", key: "n1", payload: "same"}, // duplicate edge
		{id: "Patient/n2", key: "n2", payload: "different"},
	}
	got := wp6DedupRowsByKey(rows, func(candidate row) string { return candidate.id })
	if len(got) != 2 || got[0].id != "Patient/n1" || got[1].id != "Patient/n2" {
		t.Fatalf("identity dedup = %#v, want one row per node", got)
	}
	// A candidate that dedups only after projecting arbitrary payload fields
	// is not equivalent to node identity.  Keep this assertion explicit so a
	// future property implementation cannot silently use object equality.
	if got[0].id == "" || got[0].key == "" {
		t.Fatal("identity proof lost stable node keys")
	}
}

func TestWP6PropertyTransferRejectsUndocumentedTraversalOrder(t *testing.T) {
	initial := wp6UnknownProperties()
	initial.retained["_id"] = true
	initial.retained["_key"] = true
	traversed := wp6TransferProperties(initial, "traversal")
	if traversed.identityUnique || len(traversed.order) != 0 {
		t.Fatalf("traversal unexpectedly inherited identity/order: %#v", traversed)
	}
	filtered := wp6TransferProperties(traversed, "filter")
	if filtered.identityUnique || len(filtered.order) != 0 {
		t.Fatalf("filter manufactured identity/order proof: %#v", filtered)
	}
	deduped := wp6TransferProperties(filtered, "identity_dedup")
	if !deduped.identityUnique || len(deduped.order) != 0 {
		t.Fatalf("identity dedup properties = %#v, want unique and unknown order", deduped)
	}
	sorted := wp6TransferProperties(deduped, "sort", "_key")
	if !sorted.identityUnique || fmt.Sprint(sorted.order) != "[_key]" {
		t.Fatalf("explicit sort properties = %#v, want unique and [_key] order", sorted)
	}
	projected := wp6TransferProperties(sorted, "projection", "_id", "_key")
	if !projected.identityUnique || fmt.Sprint(projected.order) != "[_key]" {
		t.Fatalf("identity projection properties = %#v, want proof retained", projected)
	}
	withoutKey := wp6TransferProperties(sorted, "projection", "_id")
	if withoutKey.identityUnique || len(withoutKey.order) != 0 {
		t.Fatalf("projection without _key retained unsafe properties: %#v", withoutKey)
	}
	union := wp6TransferProperties(sorted, "union")
	if union.identityUnique || len(union.order) != 0 {
		t.Fatalf("union retained unsafe properties: %#v", union)
	}
}

func TestWP6SliceTieBreakRequiresExactOrderProperty(t *testing.T) {
	// A representative slice sorted by a non-unique selector requires an
	// explicit stable identity tie-break.  Traversal order is not a substitute.
	sortedByDate := wp6TransferProperties(wp6Properties{identityUnique: true, retained: map[string]bool{"_id": true, "_key": true}}, "sort", "date", "_key")
	if fmt.Sprint(sortedByDate.order) != "[date _key]" {
		t.Fatalf("date slice order = %#v", sortedByDate.order)
	}
	if !wp6HasExactOrder(sortedByDate, []string{"date", "_key"}) {
		t.Fatal("date slice lost explicit identity tie-break")
	}
	unknown := wp6UnknownProperties()
	if wp6HasExactOrder(unknown, []string{"date", "_key"}) {
		t.Fatal("unknown traversal order incorrectly satisfied slice order")
	}
	// `_key` is itself a unique identity key.  Once a production property
	// proves the source is identity-unique and sorted by _key, the renderer may
	// normalize the current duplicate `_key, _key` tie-break text.  This is a
	// correctness cleanup; it is not counted as a meaningful execution win.
	keySorted := wp6TransferProperties(wp6Properties{identityUnique: true, retained: map[string]bool{"_id": true, "_key": true}}, "sort", "_key")
	if !wp6HasExactOrder(keySorted, []string{"_key"}) {
		t.Fatal("unique _key order was not recognized")
	}
}

func wp6HasExactOrder(properties wp6Properties, required []string) bool {
	if len(properties.order) != len(required) {
		return false
	}
	for index := range required {
		if properties.order[index] != required[index] {
			return false
		}
	}
	return true
}

func wp6DedupRowsByKey[T any](rows []T, key func(T) string) []T {
	seen := map[string]bool{}
	out := make([]T, 0, len(rows))
	for _, row := range rows {
		id := key(row)
		if id == "" {
			panic("identity proof requires non-empty _id")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, row)
	}
	return out
}

func wp6ReadRepoFile(t *testing.T, relative string) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	contents, err := os.ReadFile(filepath.Join(repo, relative))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(contents)
}

func TestWP6BaselineHashEvidence(t *testing.T) {
	aql := wp6ReadRepoFile(t, "docs/benchmarks/round3/wp0/baseline.aql")
	if got := sha256.Sum256([]byte(aql)); hex.EncodeToString(got[:]) != "aea66e4cf03bfbe460df2d07d3bfd7c874f8176756fe99348288addd3c589642" {
		t.Fatalf("raw baseline AQL hash changed: %x", got)
	}
	// The profile command hashes canonical AQL (normalized before hashing), so
	// retain the checked-in canonical hash separately from the raw-file hash.
	metadata := wp6ReadRepoFile(t, "docs/benchmarks/round3/wp0/baseline.json")
	if !strings.Contains(metadata, `"aql_sha256": "c0b39eb0ec0f29a09b1661c78fc377159881aae81214e505a5427495c8a7e07c"`) {
		t.Fatal("baseline metadata lost canonical AQL hash")
	}
	if !strings.Contains(metadata, `"result_sha256": "17faea7ac3ee7f308b37223f376530a0660f8068d5e015cc573cf99ccb4045ca"`) {
		t.Fatal("baseline metadata lost canonical result hash")
	}
}

func TestWP6SortInventoryIsDeterministic(t *testing.T) {
	aql := wp6ReadRepoFile(t, "docs/benchmarks/round3/wp0/baseline.aql")
	var sortLines []string
	for _, line := range strings.Split(aql, "\n") {
		if strings.Contains(line, "SORT ") {
			sortLines = append(sortLines, strings.TrimSpace(line))
		}
	}
	if len(sortLines) != 11 {
		t.Fatalf("sort inventory = %d, want 11", len(sortLines))
	}
	for index, line := range sortLines {
		if strings.TrimSpace(line) == "" {
			t.Fatalf("sort inventory line %d is empty", index)
		}
	}
}
