package arango

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCitationLineMismatchAcceptsExactCitedLine(t *testing.T) {
	lines := []string{"package main", "", "wantToken := 1", "}"}
	if mismatch := citationLineMismatch(lines, 3, "wantToken := 1"); mismatch != "" {
		t.Fatalf("exact cited line rejected: %s", mismatch)
	}
}

func TestCitationLineMismatchTrimsSurroundingWhitespace(t *testing.T) {
	lines := []string{"\t\twantToken := 1\t "}
	if mismatch := citationLineMismatch(lines, 1, "  wantToken := 1  "); mismatch != "" {
		t.Fatalf("whitespace-equivalent cited line rejected: %s", mismatch)
	}
}

func TestCitationLineMismatchFailsOnWrongLine(t *testing.T) {
	lines := []string{"package main", "wantToken := 1", "", "", "other := 2"}
	if mismatch := citationLineMismatch(lines, 5, "wantToken := 1"); mismatch == "" {
		t.Fatal("want string present at line 2 but cited at line 5 must fail: weak-gate forgery survived")
	}
}

func TestCitationLineMismatchFailsOnNearMissContent(t *testing.T) {
	lines := []string{"wantToken := 1"}
	if mismatch := citationLineMismatch(lines, 1, "wantToken := 11"); mismatch == "" {
		t.Fatal("near-miss content at the cited line must fail")
	}
}

func TestCitationLineMismatchFailsPastEOF(t *testing.T) {
	lines := []string{"only", "two"}
	for _, line := range []int{3, 4, 1000} {
		if mismatch := citationLineMismatch(lines, line, "two"); mismatch == "" {
			t.Fatalf("cited :%d past EOF must fail", line)
		}
	}
}

func TestCitationLineMismatchFailsNonPositiveLine(t *testing.T) {
	lines := []string{"only"}
	for _, line := range []int{0, -1, -100} {
		if mismatch := citationLineMismatch(lines, line, "only"); mismatch == "" {
			t.Fatalf("cited :%d must fail as non-positive", line)
		}
	}
}

func TestDanglingCitationTableStrictAtCitedLines(t *testing.T) {
	root := danglingRepoRoot(t)
	checkCitationTableStrict(t, root, len(danglingCitations), func(i int) (string, int, string) {
		return danglingCitations[i].file, danglingCitations[i].line, danglingCitations[i].want
	})
}

func TestRrdCitationTableStrictAtCitedLines(t *testing.T) {
	root := danglingRepoRoot(t)
	checkCitationTableStrict(t, root, len(rrdCitations), func(i int) (string, int, string) {
		return rrdCitations[i].file, rrdCitations[i].line, rrdCitations[i].want
	})
}

func checkCitationTableStrict(t *testing.T, root string, n int, at func(int) (string, int, string)) {
	t.Helper()
	contents := map[string][]string{}
	for i := 0; i < n; i++ {
		file, line, want := at(i)
		lines, ok := contents[file]
		if !ok {
			raw, err := os.ReadFile(filepath.Join(root, file))
			if err != nil {
				t.Fatalf("citation source %s unreadable: %v", file, err)
			}
			lines = strings.Split(string(raw), "\n")
			contents[file] = lines
		}
		if mismatch := citationLineMismatch(lines, line, want); mismatch != "" {
			t.Fatalf("citation drift: %s %s (want %q)", file, mismatch, want)
		}
	}
}
