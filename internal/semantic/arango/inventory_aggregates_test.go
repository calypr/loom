package arango

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestSemanticInventoryAggregatesUseCoveredInputs(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate semantic inventory query source")
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "inventory.go"))
	if err != nil {
		t.Fatal(err)
	}
	reducer := regexp.MustCompile(`\b(?:SUM|MIN|MAX)\(([^)]*)\)`)
	queryConstant := regexp.MustCompile("(?s)const (\\w+) = `([^`]*)`")
	covered := map[string]bool{
		"1":                        true,
		"d.example":                true,
		"d.observation.population": true,
		"d.observation.examples_truncated ? 1 : 0":     true,
		"d.observation.source.canonical":               true,
		"d.observation.source.type":                    true,
		"d.observation.source.profile":                 true,
		"d.observation.source.path":                    true,
		"d.observation.key.selector":                   true,
		"d.observation.key.system":                     true,
		"d.observation.key.version":                    true,
		"d.observation.key.code":                       true,
		"d.observation.key.display":                    true,
		"d.observation.value.selector":                 true,
		"d.observation.value.type":                     true,
		"d.observation.value.fallback_selectors":       true,
		"d.observation.value.fallback_on_blank_string": true,
		"d.observation.owning_scope":                   true,
		"d.observation.extension_url_path":             true,
		"d.observation.choice_arm":                     true,
		"d.observation.logical_type":                   true,
		"d.observation.observed_units":                 true,
		"d.observation.completeness":                   true,
		"d.observation.status":                         true,
		"d.observation.rule_hint":                      true,
		"d.observation.rule_version":                   true,
		"displays":                                     true,
	}
	queryCount := 0
	for _, constant := range queryConstant.FindAllSubmatch(source, -1) {
		name := string(constant[1])
		query := string(constant[2])
		if !strings.Contains(query, "AGGREGATE") {
			continue
		}
		queryCount++
		for _, match := range reducer.FindAllStringSubmatch(query, -1) {
			input := match[1]
			if !covered[input] {
				t.Errorf("%s query aggregate input %q is not covered by the file contribution projection", name, input)
			}
		}
	}
	if queryCount < 4 {
		t.Fatalf("found %d aggregate queries, expected all semantic inventory materialize and resolve queries", queryCount)
	}
}
