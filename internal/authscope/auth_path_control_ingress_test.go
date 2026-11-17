package authscope

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeAuthResourcePathRejectsControlCharacters(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		want  string
		archv string
	}{
		{"nul interior", "a\x00b", "", "a\x00b"},
		{"tab interior", "a\tb", "", "a\tb"},
		{"newline", "a\nb", "", "a\nb"},
		{"del", "a\x7fb", "", "a\x7fb"},
		{"legit untouched", " a/b ", "a/b", ""},
		{"legit dash colon", "svc:proj/proj-a_x.1", "svc:proj/proj-a_x.1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeAuthResourcePath(tc.in)
			if got == tc.want {
				t.Logf("VERDICT=REPAIRED NormalizeAuthResourcePath(%q)=%q", tc.in, got)
				return
			}
			if got == tc.archv {
				t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): NormalizeAuthResourcePath(%q)=%q retains control characters; a path set containing it can fold with a different two-path set under the \\x00 join of the explorer digest", tc.in, got)
			}
			t.Fatalf("VERDICT=UNEXPECTED NormalizeAuthResourcePath(%q)=%q want %q archived %q", tc.in, got, tc.want, tc.archv)
		})
	}
}

func TestCalyprKeyExtractionRejectsControlCharacters(t *testing.T) {
	in := "/programs/prog\x00x/projects/proj-a"
	key, ok := calyprProjectKeyFromResourcePath(in)
	if !ok {
		t.Logf("VERDICT=REPAIRED calypr key rejected for control character in %q", in)
		return
	}
	if key == "prog\x00x-proj-a" {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): calyprProjectKeyFromResourcePath accepted %q yielding key %q; fence-sourced scope paths can carry the control byte", in, key)
	}
	t.Fatalf("VERDICT=UNEXPECTED calyprProjectKeyFromResourcePath(%q)=(%q,%v)", in, key, ok)
}

func TestNormalizedPathListDropsControlCharEntries(t *testing.T) {
	left := normalizeAuthResourcePathList([]string{"a\x00b", "c"})
	right := normalizeAuthResourcePathList([]string{"a", "b\x00c"})
	join := func(paths []string) string { return strings.Join(paths, "\x00") }
	if join(left) != join(right) {
		t.Logf("VERDICT=REPAIRED fold broken: left=%q right=%q", left, right)
		return
	}
	if len(left) == 2 && left[0] == "a\x00b" && right[1] == "b\x00c" {
		t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): grant list %q and %q both normalize fully and fold to identical \\x00-join %q, so two distinct restricted scopes share one capability digest", left, right, join(left))
	}
	t.Fatalf("VERDICT=UNEXPECTED left=%q right=%q join=%q", left, right, join(left))
}

func TestNormalizedListDropsControlCharEntriesStable(t *testing.T) {
	got := normalizeAuthResourcePathList([]string{"b/", "/a", "a", "bad\x00path", " ", ""})
	want := []string{"a", "b"}
	if !reflect.DeepEqual(got, want) {
		original := normalizeAuthResourcePathListRaw([]string{"b/", "/a", "a", "bad\x00path", " ", ""})
		if reflect.DeepEqual(got, original) || strings.Contains(strings.Join(got, "\x00"), "bad\x00path") {
			t.Fatalf("VERDICT=CONFIRMED (pre-fix archived shape): exported list normalization kept raw entries %q (control-character entry retained)", got)
		}
		t.Fatalf("VERDICT=UNEXPECTED NormalizeAuthResourcePaths=%q want %q", got, want)
	}
	t.Logf("VERDICT=REPAIRED NormalizeAuthResourcePaths=%q (deduped, normalized, control-character entries dropped)", got)
}

func normalizeAuthResourcePathListRaw(paths []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, p := range paths {
		p = normalizeAuthResourcePathListProbe(p)
		if _, ok := seen[p]; ok || p == "" {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

func normalizeAuthResourcePathListProbe(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if key, ok := calyprProjectKeyFromResourcePath(value); ok {
		return key
	}
	return strings.Trim(value, "/")
}
