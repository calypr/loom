package benchmark

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, data string) map[string]any {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return m
}

func TestCanonicalTypeDistinctness(t *testing.T) {
	// 1, "1", true, and null must all canonicalize apart.
	a := CanonicalizeValue(decode(t, `{"v": 1}`)["v"])
	b := CanonicalizeValue(decode(t, `{"v": "1"}`)["v"])
	c := CanonicalizeValue(decode(t, `{"v": true}`)["v"])
	d := CanonicalizeValue(decode(t, `{"v": null}`)["v"])
	tokens := map[string]bool{}
	for _, v := range []CanonicalValue{a, b, c, d} {
		tok := canonicalEncode(v)
		if tokens[tok] {
			t.Fatalf("two distinct values share token %q", tok)
		}
		tokens[tok] = true
	}
}

func TestCanonicalNumericSpellingEquivalence(t *testing.T) {
	same := []string{"1", "1.0", "1e0", "1.00"}
	distinct := []string{"0.1", "0.10000000001", "2"}
	bagOf := func(lits ...string) CanonicalBag {
		var rows []CanonicalRow
		for _, lit := range lits {
			rows = append(rows, CanonicalRow{CanonicalizeValue(decode(t, `{"v": `+lit+`}`)["v"])})
		}
		return Bag(rows)
	}
	if d := bagOf(same[0], same[1], same[2], same[3]); len(d) != 4 {
		t.Fatal("bag lost multiplicity")
	}
	// All four spellings encode to ONE distinct token; bag equality of a
	// single 1 vs a single 1.0 must hold.
	if Bag([]CanonicalRow{{CanonicalizeValue(decode(t, `{"v": 1}`)["v"])}}).Digest() !=
		Bag([]CanonicalRow{{CanonicalizeValue(decode(t, `{"v": 1.0}`)["v"])}}).Digest() {
		t.Fatal("1 and 1.0 must canonicalize identically")
	}
	if Bag([]CanonicalRow{{CanonicalizeValue(decode(t, `{"v": 0.1}`)["v"])}}).Digest() ==
		Bag([]CanonicalRow{{CanonicalizeValue(decode(t, `{"v": 0.10000000001}`)["v"])}}).Digest() {
		t.Fatal("near-equal numbers must stay distinct")
	}
	_ = distinct
}

func TestCanonicalNullVsMissing(t *testing.T) {
	nullRow := CanonicalizeRows([]string{"v"}, []map[string]any{{"v": nil}})
	missingRow := CanonicalizeRows([]string{"v"}, []map[string]any{{}})
	if Bag(nullRow).Digest() == Bag(missingRow).Digest() {
		t.Fatal("null must not equal missing")
	}
	if _, ok := missingRow[0][0].(CMissing); !ok {
		t.Fatalf("absent key must canonicalize to CMissing, got %T", missingRow[0][0])
	}
}

func TestCanonicalBagKeepsMultiplicity(t *testing.T) {
	dup := []map[string]any{{"v": "x"}, {"v": "x"}}
	single := []map[string]any{{"v": "x"}}
	if Bag(CanonicalizeRows([]string{"v"}, dup)).Digest() == Bag(CanonicalizeRows([]string{"v"}, single)).Digest() {
		t.Fatal("bag semantics must preserve multiplicity")
	}
}

func TestCanonicalBagIsUnordered(t *testing.T) {
	a := []map[string]any{{"v": "x"}, {"v": "y"}}
	b := []map[string]any{{"v": "y"}, {"v": "x"}}
	if Bag(CanonicalizeRows([]string{"v"}, a)).Digest() != Bag(CanonicalizeRows([]string{"v"}, b)).Digest() {
		t.Fatal("bag comparison must ignore row order")
	}
}

func TestCanonicalNestedStructure(t *testing.T) {
	// Nested list identity: backend A gives ["a","a"], backend B ["a"].
	a := CanonicalizeValue(decode(t, `{"v": ["a","a"]}`)["v"])
	b := CanonicalizeValue(decode(t, `{"v": ["a"]}`)["v"])
	if canonicalEncode(a) == canonicalEncode(b) {
		t.Fatal("nested multiplicity must be preserved")
	}
	// Object key ORDER must not matter, but key presence must.
	c := CanonicalizeValue(decode(t, `{"v": {"a": 1, "b": 2}}`)["v"])
	d := CanonicalizeValue(decode(t, `{"v": {"b": 2, "a": 1}}`)["v"])
	if canonicalEncode(c) != canonicalEncode(d) {
		t.Fatal("object key order must not affect encoding")
	}
	e := CanonicalizeValue(decode(t, `{"v": {"a": 1}}`)["v"])
	if canonicalEncode(c) == canonicalEncode(e) {
		t.Fatal("object key presence must affect encoding")
	}
}

func TestStorageIDMapResolvesArangoSpellings(t *testing.T) {
	facts, err := GenerateFacts(DatasetParams{Preset: PresetTiny, Seed: 7, Project: "p", Generation: "g", AuthResourcePath: "/bench"})
	if err != nil {
		t.Fatal(err)
	}
	idMap := NewStorageIDMap(facts)
	pid := facts.Patients[0].ID
	// The engine's row-identity column carries the storage spelling on the
	// Arango arm and the stored logical id on the PostgreSQL arm.
	columns := []string{"_row_id", "gender"}
	arangoRows := []map[string]any{{"_row_id": "Patient/" + pid, "gender": "female"}}
	postgresRows := []map[string]any{{"_row_id": pid, "gender": "female"}}
	aBag := Bag(CanonicalizeRows(columns, idMap.Apply(columns, arangoRows)))
	pBag := Bag(CanonicalizeRows(columns, idMap.Apply(columns, postgresRows)))
	if aBag.Digest() != pBag.Digest() {
		t.Fatal("storage spelling must map to logical identity on both arms")
	}
	// A foreign identity stays verbatim and must not vanish into the map.
	foreign := []map[string]any{{"_row_id": "Patient/not-a-real-id", "gender": "female"}}
	fBag := Bag(CanonicalizeRows(columns, idMap.Apply(columns, foreign)))
	if fBag.Digest() == pBag.Digest() {
		t.Fatal("foreign identity must remain distinct")
	}
}

func TestFirstDifferenceReportsDivergence(t *testing.T) {
	a := Bag(CanonicalizeRows([]string{"v"}, []map[string]any{{"v": "a"}, {"v": "b"}}))
	b := Bag(CanonicalizeRows([]string{"v"}, []map[string]any{{"v": "a"}, {"v": "z"}}))
	x, y, diff := FirstDifference(a, b)
	if !diff || x == "" || y == "" {
		t.Fatalf("expected divergence report, got %q %q %v", x, y, diff)
	}
	if _, _, diff := FirstDifference(a, a); diff {
		t.Fatal("identical bags must report no difference")
	}
}
