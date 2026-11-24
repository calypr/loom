//go:build ignore

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/doc"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const scope = "./internal/..."

type goPackage struct {
	ImportPath   string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
}

type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

type testID struct {
	pkg  string
	name string
}

type coverageSummary struct {
	Packages          int     `json:"packages"`
	TestsPassed       int     `json:"tests_passed"`
	TestsSkipped      int     `json:"tests_skipped"`
	StatementsCovered int     `json:"statements_covered"`
	StatementsTotal   int     `json:"statements_total"`
	CoveragePercent   float64 `json:"coverage_percent"`
	MinimumCovered    int     `json:"minimum_covered"`
}

func main() {
	root := flag.String("repo", ".", "Go module root")
	output := flag.String("out", "", "new directory for coverage evidence")
	minimum := flag.Int("minimum-covered", 0, "minimum covered internal statements")
	timeout := flag.Duration("timeout", 20*time.Minute, "deadline for the complete run")
	flag.Parse()

	if *output == "" || *minimum < 0 || *timeout <= 0 || flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	if err := checkCoverage(ctx, *root, *output, *minimum); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func checkCoverage(ctx context.Context, root, output string, minimum int) error {
	output, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if err := os.Mkdir(output, 0o755); err != nil {
		return fmt.Errorf("create evidence directory: %w", err)
	}

	packageLog, err := goCommand(ctx, root, output, "packages", "list", "-json", scope)
	if err != nil {
		return err
	}
	packages, err := decodeLog[goPackage](packageLog)
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return errors.New("go list found no internal packages")
	}
	packagePaths := make(map[string]bool, len(packages))
	tests := map[testID]bool{}
	for _, pkg := range packages {
		if pkg.ImportPath == "" || packagePaths[pkg.ImportPath] {
			return fmt.Errorf("unexpected package in go list: %q", pkg.ImportPath)
		}
		packagePaths[pkg.ImportPath] = true
		if err := addExpectedTests(pkg, tests); err != nil {
			return err
		}
	}

	expected := make([]string, 0, len(tests))
	for test := range tests {
		expected = append(expected, test.pkg+"\t"+test.name+"\n")
	}
	slices.Sort(expected)
	if err := os.WriteFile(filepath.Join(output, "expected-tests.tsv"), []byte(strings.Join(expected, "")), 0o644); err != nil {
		return err
	}

	profile := filepath.Join(output, "coverage.out")
	testLog, err := goCommand(ctx, root, output, "tests", "test", "-json", "-count=1",
		"-run=.", "-skip=", "-short=false", "-bench=", "-fuzz=", "-list=", "-covermode=set", "-coverpkg="+scope, "-coverprofile="+profile, scope)
	if err != nil {
		return err
	}
	results, err := decodeLog[testEvent](testLog)
	if err != nil {
		return err
	}
	if err := checkPackages(results, packagePaths, "covered execution"); err != nil {
		return err
	}
	passes, skips, err := checkTests(results, tests)
	if err != nil {
		return err
	}
	covered, total, err := statementCoverage(profile)
	if err != nil {
		return err
	}

	summary := coverageSummary{
		Packages: len(packagePaths), TestsPassed: passes, TestsSkipped: skips,
		StatementsCovered: covered, StatementsTotal: total,
		CoveragePercent: 100 * float64(covered) / float64(total), MinimumCovered: minimum,
	}
	report, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(output, "summary.json"), append(report, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d packages, %d tests passed, %d skipped, %d/%d statements covered (%.4f%%)\n",
		summary.Packages, passes, skips, covered, total, summary.CoveragePercent)
	if covered < minimum {
		return fmt.Errorf("coverage floor failed: %d covered statements, need %d", covered, minimum)
	}
	return nil
}

func goCommand(ctx context.Context, root, output, name string, args ...string) (string, error) {
	stdoutPath := filepath.Join(output, name+".jsonl")
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		return "", err
	}
	defer stdout.Close()
	stderr, err := os.Create(filepath.Join(output, name+".stderr"))
	if err != nil {
		return "", err
	}
	defer stderr.Close()

	fmt.Printf("go %s\n", strings.Join(args, " "))
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s failed: %w; see %s and %s", name, errors.Join(err, ctx.Err()), stdoutPath, stderr.Name())
	}
	return stdoutPath, nil
}

func decodeLog[T any](path string) ([]T, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var values []T
	decoder := json.NewDecoder(file)
	for {
		var value T
		if err := decoder.Decode(&value); err != nil {
			if errors.Is(err, io.EOF) {
				return values, nil
			}
			return nil, fmt.Errorf("decode %s: %w", path, err)
		}
		values = append(values, value)
	}
}

func addExpectedTests(pkg goPackage, tests map[testID]bool) error {
	for _, name := range append(pkg.TestGoFiles, pkg.XTestGoFiles...) {
		path := filepath.Join(pkg.Dir, name)
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			name := fn.Name.Name
			if (isTestName(name, "Test") || isTestName(name, "Fuzz")) &&
				(name != "TestMain" || isTestMainTest(fn)) {
				tests[testID{pkg: pkg.ImportPath, name: name}] = true
			}
		}
		for _, example := range doc.Examples(file) {
			if example.Output != "" || example.EmptyOutput {
				tests[testID{pkg: pkg.ImportPath, name: "Example" + example.Name}] = true
			}
		}
	}
	return nil
}

func isTestName(name, prefix string) bool {
	suffix, ok := strings.CutPrefix(name, prefix)
	next, _ := utf8.DecodeRuneInString(suffix)
	return ok && !unicode.IsLower(next)
}

func isTestMainTest(fn *ast.FuncDecl) bool {
	params := fn.Type.Params.List
	if len(params) != 1 {
		return false
	}
	pointer, ok := params[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	switch name := pointer.X.(type) {
	case *ast.Ident:
		return name.Name == "T"
	case *ast.SelectorExpr:
		return name.Sel.Name == "T"
	default:
		return false
	}
}

func checkPackages(events []testEvent, expected map[string]bool, phase string) error {
	completed := map[string]bool{}
	for _, event := range events {
		if event.Test == "" && (event.Action == "pass" || event.Action == "skip") {
			if !expected[event.Package] || completed[event.Package] {
				return fmt.Errorf("%s: unexpected package completion for %s", phase, event.Package)
			}
			completed[event.Package] = true
		}
	}
	for _, pkg := range slices.Sorted(maps.Keys(expected)) {
		if !completed[pkg] {
			return fmt.Errorf("%s: no package completion for %s", phase, pkg)
		}
	}
	return nil
}

func checkTests(events []testEvent, expected map[testID]bool) (passes, skips int, err error) {
	completed := map[testID]bool{}
	for _, event := range events {
		if event.Test == "" || strings.Contains(event.Test, "/") || (event.Action != "pass" && event.Action != "skip") {
			continue
		}
		test := testID{pkg: event.Package, name: event.Test}
		if !expected[test] || completed[test] {
			return 0, 0, fmt.Errorf("unexpected test completion for %s %s", test.pkg, test.name)
		}
		completed[test] = true
		if event.Action == "pass" {
			passes++
		} else {
			skips++
		}
	}
	var missing []string
	for test := range expected {
		if !completed[test] {
			missing = append(missing, test.pkg+" "+test.name)
		}
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return 0, 0, fmt.Errorf("no test completion for %s", strings.Join(missing, ", "))
	}
	return passes, skips, nil
}

var statementLocation = regexp.MustCompile(`^.+:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$`)

type statementBlock struct {
	weight  int
	covered bool
}

func statementCoverage(path string) (covered, total int, err error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() || scanner.Text() != "mode: set" {
		return 0, 0, fmt.Errorf("%s: missing set coverage header", path)
	}
	blocks := map[string]statementBlock{}
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			return 0, 0, fmt.Errorf("%s: invalid coverage line %q", path, scanner.Text())
		}
		key := fields[0]
		weight, weightErr := strconv.Atoi(fields[1])
		count, countErr := strconv.Atoi(fields[2])
		if !statementLocation.MatchString(key) || weightErr != nil || countErr != nil || weight < 0 || (count != 0 && count != 1) {
			return 0, 0, fmt.Errorf("%s: invalid coverage block %s", path, key)
		}
		previous, found := blocks[key]
		if found && previous.weight != weight {
			return 0, 0, fmt.Errorf("%s: conflicting coverage weights for %s", path, key)
		}
		blocks[key] = statementBlock{weight: weight, covered: previous.covered || count > 0}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	for _, block := range blocks {
		total += block.weight
		if block.covered {
			covered += block.weight
		}
	}
	if total == 0 {
		return 0, 0, fmt.Errorf("%s: no executable statements", path)
	}
	return covered, total, nil
}
