package recipe

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func semanticConceptFixtureDir(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "docs", "contracts", "explorer-builder", "v2"))
}

func readSemanticConceptFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(semanticConceptFixtureDir(t), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return document
}

func semanticMap(t *testing.T, value any, context string) map[string]any {
	t.Helper()
	result, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object: %#v", context, value)
	}
	return result
}

func semanticSlice(t *testing.T, value any, context string) []any {
	t.Helper()
	result, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is not an array: %#v", context, value)
	}
	return result
}

func semanticString(t *testing.T, value any, context string) string {
	t.Helper()
	result, ok := value.(string)
	if !ok || strings.TrimSpace(result) == "" {
		t.Fatalf("%s is not a non-empty string: %#v", context, value)
	}
	return result
}

func TestExplorerBuilderSemanticConceptFixtureManifest(t *testing.T) {
	dir := semanticConceptFixtureDir(t)
	manifestData, err := os.ReadFile(filepath.Join(dir, "MANIFEST.sha256"))
	if err != nil {
		t.Fatal(err)
	}

	checked := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(string(manifestData)))
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 {
			t.Fatalf("invalid manifest line %q", scanner.Text())
		}
		if checked[parts[1]] {
			t.Fatalf("duplicate manifest entry %s", parts[1])
		}
		data, err := os.ReadFile(filepath.Join(dir, parts[1]))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != parts[0] {
			t.Fatalf("fixture %s hash=%s want=%s", parts[1], got, parts[0])
		}
		checked[parts[1]] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(checked) == 0 {
		t.Fatal("manifest contains no fixtures")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var fixtureNames []string
	for _, entry := range entries {
		if !entry.IsDir() && entry.Name() != "MANIFEST.sha256" {
			fixtureNames = append(fixtureNames, entry.Name())
		}
	}
	sort.Strings(fixtureNames)
	for _, name := range fixtureNames {
		if !checked[name] {
			t.Fatalf("fixture %s is not covered by MANIFEST.sha256", name)
		}
	}
}

func TestSemanticConceptCatalogKeepsMetadataGenericAndAuditable(t *testing.T) {
	document := readSemanticConceptFixture(t, "semantic-concept-catalog.json")
	if document["schemaVersion"] != float64(2) {
		t.Fatalf("schemaVersion=%v, want 2", document["schemaVersion"])
	}
	catalog := semanticMap(t, document["catalog"], "catalog")
	completeness := semanticMap(t, catalog["completeness"], "catalog.completeness")
	if semanticString(t, completeness["state"], "completeness.state") != "partial" {
		t.Fatalf("catalog should explicitly be partial")
	}

	resources := semanticSlice(t, catalog["resources"], "catalog.resources")
	if len(resources) != 2 {
		t.Fatalf("resource count=%d, want 2", len(resources))
	}
	var future, language, repeated, suppressed map[string]any
	for _, resourceValue := range resources {
		resource := semanticMap(t, resourceValue, "resource")
		for _, familyValue := range semanticSlice(t, resource["families"], "resource.families") {
			family := semanticMap(t, familyValue, "family")
			semanticString(t, family["id"], "family.id")
			semanticString(t, family["label"], "family.label")
			for _, conceptValue := range semanticSlice(t, family["concepts"], "family.concepts") {
				concept := semanticMap(t, conceptValue, "concept")
				semanticString(t, concept["id"], "concept.id")
				semanticString(t, concept["family"], "concept.family")
				semanticString(t, concept["ruleId"], "concept.ruleId")
				source := semanticMap(t, concept["source"], "concept.source")
				semanticString(t, source["system"], "source.system")
				semanticString(t, source["kind"], "source.kind")
				semanticString(t, source["resourceType"], "source.resourceType")
				if len(semanticSlice(t, source["keyPaths"], "source.keyPaths")) == 0 || len(semanticSlice(t, source["valuePaths"], "source.valuePaths")) == 0 {
					t.Fatalf("concept %s must provide key and value source paths", concept["id"])
				}
				selector := semanticMap(t, concept["selector"], "concept.selector")
				semanticString(t, selector["sourcePath"], "selector.sourcePath")
				semanticString(t, selector["valuePath"], "selector.valuePath")
				column := semanticMap(t, concept["column"], "concept.column")
				semanticString(t, column["name"], "column.name")
				semanticString(t, column["logicalType"], "column.logicalType")
				if _, closedMappingEnumWasAdded := concept["mappingKind"]; closedMappingEnumWasAdded {
					t.Fatalf("concept %s introduced a closed mappingKind enum", concept["id"])
				}
				id := semanticString(t, concept["id"], "concept.id")
				switch id {
				case "observation.future-score":
					future = concept
				case "patient.preferred-language":
					language = concept
				case "observation.anatomical-sites":
					repeated = concept
				}
				examples := semanticMap(t, concept["examples"], "concept.examples")
				if examples["suppressed"] == true {
					suppressed = concept
					if values, ok := examples["values"]; ok && len(semanticSlice(t, values, "suppressed examples.values")) != 0 {
						t.Fatalf("suppressed concept %s contains example values", id)
					}
				}
			}
		}
	}
	if future == nil || language == nil || repeated == nil || suppressed == nil {
		t.Fatalf("catalog did not cover future, terminology, repeated, and suppressed concepts")
	}

	futureSource := semanticMap(t, future["source"], "future.source")
	if semanticString(t, futureSource["system"], "future.source.system") != "FutureClinicalSource" || semanticString(t, futureSource["kind"], "future.source.kind") != "future-value-family" {
		t.Fatalf("future source metadata was constrained unexpectedly: %#v", futureSource)
	}
	futureColumn := semanticMap(t, future["column"], "future.column")
	if semanticString(t, futureColumn["logicalType"], "future.column.logicalType") != "futureDecimal128" {
		t.Fatalf("future logical type was constrained unexpectedly")
	}
	if semanticString(t, future["family"], "future.family") != "future-clinical-domain-v9" {
		t.Fatalf("future family was constrained unexpectedly")
	}

	languageSource := semanticMap(t, language["source"], "language.source")
	terminology := semanticMap(t, languageSource["terminology"], "language.source.terminology")
	for _, key := range []string{"system", "version", "code", "display"} {
		semanticString(t, terminology[key], "language terminology."+key)
	}

	repeatedColumn := semanticMap(t, repeated["column"], "repeated.column")
	if repeatedColumn["repeated"] != true {
		t.Fatalf("repeated concept column is not marked repeated")
	}
	repetition := semanticMap(t, repeated["repetition"], "repeated.repetition")
	if semanticString(t, repetition["shape"], "repetition.shape") != "array" || semanticString(t, repetition["rowExpansion"], "repetition.rowExpansion") != "none" {
		t.Fatalf("repetition semantics are not an array without row expansion")
	}

	diagnostics := semanticSlice(t, catalog["diagnostics"], "catalog.diagnostics")
	if len(diagnostics) < 2 {
		t.Fatalf("partial catalog must include truncation and partial diagnostics")
	}
	seenCodes := map[string]bool{}
	for _, diagnosticValue := range diagnostics {
		diagnostic := semanticMap(t, diagnosticValue, "diagnostic")
		seenCodes[semanticString(t, diagnostic["code"], "diagnostic.code")] = true
		semanticString(t, diagnostic["severity"], "diagnostic.severity")
		semanticString(t, diagnostic["message"], "diagnostic.message")
	}
	if !seenCodes["CONCEPT_LIMIT_REACHED"] || !seenCodes["DISCOVERY_PARTIAL"] {
		t.Fatalf("missing truncation/partial diagnostics: %#v", seenCodes)
	}
}

func TestSemanticConceptSelectionResolvesToPublishedColumn(t *testing.T) {
	selection := readSemanticConceptFixture(t, "concept-selections.json")
	selectionBody := semanticMap(t, selection["selection"], "selection")
	selected := semanticSlice(t, selectionBody["concepts"], "selection.concepts")
	if len(selected) != 3 {
		t.Fatalf("selected concept count=%d, want 3", len(selected))
	}

	publication := readSemanticConceptFixture(t, "authored-resolved-publication.json")
	authored := semanticMap(t, publication["authored"], "authored")
	resolved := semanticMap(t, publication["resolved"], "resolved")
	authoredOutputs := semanticSlice(t, authored["outputs"], "authored.outputs")
	resolvedOutputs := semanticSlice(t, resolved["outputs"], "resolved.outputs")
	if len(authoredOutputs) != 1 || len(resolvedOutputs) != 1 {
		t.Fatalf("authored/resolved output count mismatch")
	}
	authoredOutput := semanticMap(t, authoredOutputs[0], "authored output")
	resolvedOutput := semanticMap(t, resolvedOutputs[0], "resolved output")
	authoredConcepts := semanticSlice(t, authoredOutput["conceptSelections"], "authored concept selections")
	resolvedColumns := semanticSlice(t, resolvedOutput["columns"], "resolved columns")
	if len(authoredConcepts) != len(resolvedColumns) {
		t.Fatalf("authored concepts=%d resolved columns=%d", len(authoredConcepts), len(resolvedColumns))
	}
	for index, authoredValue := range authoredConcepts {
		authoredConcept := semanticMap(t, authoredValue, "authored concept")
		resolvedColumn := semanticMap(t, resolvedColumns[index], "resolved column")
		if semanticString(t, authoredConcept["conceptId"], "authored conceptId") != semanticString(t, resolvedColumn["conceptId"], "resolved conceptId") || semanticString(t, authoredConcept["ruleId"], "authored ruleId") != semanticString(t, resolvedColumn["ruleId"], "resolved ruleId") {
			t.Fatalf("resolved column %d lost authored concept identity", index)
		}
		selector := semanticMap(t, resolvedColumn["selector"], "resolved selector")
		semanticString(t, selector["sourcePath"], "resolved selector.sourcePath")
		semanticString(t, selector["valuePath"], "resolved selector.valuePath")
	}
	materialization := semanticMap(t, resolvedOutput["materialization"], "materialization")
	if semanticString(t, materialization["status"], "materialization.status") != "READY" {
		t.Fatalf("publication fixture is not READY")
	}
	publicationState := semanticMap(t, publication["publication"], "publication")
	if semanticString(t, publicationState["status"], "publication.status") != "READY" {
		t.Fatalf("publication state is not READY")
	}
}
