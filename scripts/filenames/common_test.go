package main

import (
	"strings"
	"testing"
)

func TestParseSelector(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		kind string
	}{
		{"file:^openapi_", true, "file"},
		{"name:^Build", true, "name"},
		{"list:A,B", true, "list"},
		{"prefix:construction", true, "prefix"},
		{"bogus", false, ""},
		{"file:[", false, ""},
		{"list:,", false, ""},
	}
	for _, tc := range cases {
		sel, err := parseSelector(tc.in)
		if tc.ok && err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.in, err)
		}
		if !tc.ok && err == nil {
			t.Fatalf("%q: expected error", tc.in)
		}
		if tc.ok && sel.kind != tc.kind {
			t.Fatalf("%q: kind = %q", tc.in, sel.kind)
		}
	}
}

func TestSelectorMatches(t *testing.T) {
	sel, _ := parseSelector("file:^source_load_")
	if !sel.matchesFile("source_load_catalog.go") {
		t.Fatal("file selector did not match basename")
	}
	if sel.matchesFile("catalog.go") {
		t.Fatal("file selector matched unrelated basename")
	}
	sel, _ = parseSelector("list:Foo,Bar.Baz")
	if !sel.matchesName("Foo") || !sel.matchesName("Bar.Baz") {
		t.Fatal("list selector missed a name")
	}
	if sel.matchesName("Qux") {
		t.Fatal("list selector matched an unrelated name")
	}
	sel, _ = parseSelector("name:^render")
	if !sel.matchesName("renderStage") || sel.matchesName("stageRenderer") {
		t.Fatal("name regexp matched incorrectly")
	}
}

func TestPrefixToken(t *testing.T) {
	cases := map[string]string{
		"source_load_catalog.go":  "source",
		"openapi_explorer_x.go":   "openapi",
		"plain.go":                "plain",
		"physical_stage_x_val.go": "physical",
	}
	for in, want := range cases {
		if got := prefixToken(in); got != want {
			t.Fatalf("prefixToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeTokens(t *testing.T) {
	_, norm := normalizeTokens("openapi_explorer_construction.go", "internal/api/explorer")
	if norm != "openapi;construction" {
		t.Fatalf("normalized = %q", norm)
	}
	_, norm = normalizeTokens("source_load_validate.go", "internal/ingest/sourceload")
	if norm != "validate" {
		t.Fatalf("normalized split package repeat = %q", norm)
	}
	_, norm = normalizeTokens("validations.go", "internal/pkg")
	if norm != "validate" {
		t.Fatalf("normalized plural/fold = %q", norm)
	}
}

func TestLowerFirst(t *testing.T) {
	if got := lowerFirst("FooBar"); got != "fooBar" {
		t.Fatalf("lowerFirst = %q", got)
	}
	if got := lowerFirst(""); got != "" {
		t.Fatalf("lowerFirst empty = %q", got)
	}
}

func TestParseRanges(t *testing.T) {
	got := parseRanges("a.go:10-20;b.go:30-40")
	if len(got) != 2 || got[0].file != "a.go" || got[0].start != 10 || got[0].end != 20 {
		t.Fatalf("parseRanges = %#v", got)
	}
}

func TestRemoveRanges(t *testing.T) {
	src := []byte("0123456789")
	out := string(removeRanges(src, []applyRange{{start: 2, end: 4}, {start: 6, end: 8}}))
	if out != "014589" {
		t.Fatalf("removeRanges = %q", out)
	}
}

func TestRemoveUnusedImports(t *testing.T) {
	src := []byte(`package p

import (
	"fmt"
	"strings"
)

func F() { fmt.Println("x") }
`)
	out := string(removeUnusedImports(src, "p.go"))
	if strings.Contains(out, `"strings"`) {
		t.Fatalf("unused import retained:\n%s", out)
	}
	if !strings.Contains(out, `"fmt"`) {
		t.Fatalf("used import removed:\n%s", out)
	}
}

func TestForbiddenEdgeAndRanks(t *testing.T) {
	if r, ok := layerRank("internal/explorer/x"); !ok || r != 5 {
		t.Fatalf("explorer rank = %d, %v", r, ok)
	}
	if msg := forbiddenEdge("internal/explorer", "internal/dataframe/compiler/ir"); msg == "" {
		t.Fatal("expected compiler forbidden edge")
	}
	if msg := forbiddenEdge("internal/ingest/sourceload", "internal/dataframe/x"); msg == "" {
		t.Fatal("expected sourceload forbidden edge")
	}
	if msg := forbiddenEdge("internal/server", "internal/api/explorer"); msg != "" {
		t.Fatalf("unexpected forbidden edge: %s", msg)
	}
}
