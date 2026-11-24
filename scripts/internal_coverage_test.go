//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoverageIncludesCrossPackageCalls(t *testing.T) {
	root := coverageFixture(t)
	err := checkCoverage(testContext(t), root, filepath.Join(root, "evidence"), 2)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "evidence", "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var summary coverageSummary
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Packages != 3 || summary.TestsPassed != 6 || summary.TestsSkipped != 1 {
		t.Fatalf("incomplete suite inventory: %+v", summary)
	}
	if summary.StatementsCovered != 2 || summary.StatementsTotal != 3 {
		t.Fatalf("wrong statement union: %+v", summary)
	}
}

func TestCoverageRunsSubtestsDespiteInheritedFilters(t *testing.T) {
	root := coverageFixture(t)
	writeFixture(t, root, "internal/core/subtest_test.go", `package core
import "testing"
func TestAllowed(t *testing.T) {
	t.Run("keep", func(t *testing.T) {})
	t.Run("omitted", func(t *testing.T) { t.Fatal("must run") })
}
`)
	t.Setenv("GOFLAGS", "-p=1 -run=TestAllowed/keep -skip=TestAllowed/omitted")
	output := filepath.Join(root, "evidence")
	err := checkCoverage(testContext(t), root, output, 0)
	if err == nil || !strings.Contains(err.Error(), "tests failed") {
		t.Fatalf("subtest failure was omitted: %v", err)
	}
	log, err := os.ReadFile(filepath.Join(output, "tests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "must run") {
		t.Fatal("covered test run did not reach the omitted subtest")
	}
}

func TestCoverageRejectsPackageBuildFailure(t *testing.T) {
	root := coverageFixture(t)
	writeFixture(t, root, "internal/broken/broken.go", "package broken\nfunc Bad() { missing() }\n")
	writeFixture(t, root, "internal/broken/broken_test.go", "package broken\nimport \"testing\"\nfunc TestBroken(t *testing.T) {}\n")
	output := filepath.Join(root, "evidence")
	err := checkCoverage(testContext(t), root, output, 0)
	if err == nil || !strings.Contains(err.Error(), "tests failed") {
		t.Fatalf("broken package returned %v", err)
	}
	if _, err := os.Stat(filepath.Join(output, "summary.json")); !os.IsNotExist(err) {
		t.Fatalf("broken package produced a success report: %v", err)
	}
}

func TestCoverageRejectsTestMainBypass(t *testing.T) {
	root := coverageFixture(t)
	writeFixture(t, root, "internal/caller/main_test.go",
		"package caller\nimport (\"os\"; \"testing\")\nfunc TestMain(m *testing.M) { os.Exit(0) }\n")
	err := checkCoverage(testContext(t), root, filepath.Join(root, "evidence"), 0)
	if err == nil || !strings.Contains(err.Error(), "no test completion") {
		t.Fatalf("bypassing TestMain returned %v", err)
	}
}

func TestCoverageRejectsInitBypass(t *testing.T) {
	root := coverageFixture(t)
	writeFixture(t, root, "internal/core/bypass_test.go",
		"package core\nimport \"os\"\nfunc init() { os.Exit(0) }\n")
	err := checkCoverage(testContext(t), root, filepath.Join(root, "evidence"), 1)
	if err == nil || !strings.Contains(err.Error(), "no test completion") || !strings.Contains(err.Error(), "TestOwn") {
		t.Fatalf("bypassing init returned %v", err)
	}
}

func TestCoverageRejectsMalformedProfile(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "malformed.out")
	writeFixture(t, root, "malformed.out", "mode: set\ninvalid-location 999999 7\n")
	if covered, total, err := statementCoverage(profile); err == nil {
		t.Fatalf("malformed profile returned %d/%d", covered, total)
	}
}

func TestCoverageRejectsStatementLoss(t *testing.T) {
	root := coverageFixture(t)
	err := checkCoverage(testContext(t), root, filepath.Join(root, "evidence"), 3)
	if err == nil || !strings.Contains(err.Error(), "2 covered statements, need 3") {
		t.Fatalf("coverage floor returned %v", err)
	}
}

func coverageFixture(t *testing.T) string {
	t.Helper()
	t.Setenv("GOFLAGS", "-p=1")
	t.Setenv("GOWORK", "off")
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.org/coverage\n\ngo 1.27.4\n")
	writeFixture(t, root, "internal/core/core.go", `package core
func Own() int { return 1 }
func Cross() int { return 2 }
func Uncovered() int { return 3 }
`)
	writeFixture(t, root, "internal/core/core_test.go", `package core
import "testing"
func TestOwn(t *testing.T) {
	if Own() != 1 { t.Fatal("wrong value") }
}
func TestSkipped(t *testing.T) { t.Skip("optional fixture") }
func TestMain(t *testing.T) { Own() }
func BenchmarkOwn(b *testing.B) { Own() }
func FuzzOwn(f *testing.F) {
	f.Add(1)
	f.Fuzz(func(t *testing.T, value int) { Own() })
}
func ExampleOwn() {
	Own()
	// Output:
}
func ExampleUncovered() { Uncovered() }
`)
	writeFixture(t, root, "internal/caller/caller_test.go", `package caller
import ("testing"; "example.org/coverage/internal/core")
func TestCrossPackage(t *testing.T) {
	if core.Cross() != 2 { t.Fatal("wrong cross-package value") }
}
`)
	writeFixture(t, root, "internal/core/external_test.go", `package core_test
import "testing"
func TestExternal(t *testing.T) {}
`)
	writeFixture(t, root, "internal/notests/empty.go", "package notests\n")
	return root
}

func writeFixture(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	t.Cleanup(cancel)
	return ctx
}
