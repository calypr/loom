package catalog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/calypr/loom/internal/fhir/schema"
	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

func TestExtensionURLAncestryMatchesScanOracle(t *testing.T) {
	cases := []struct {
		name  string
		owner string
		urls  map[string]string
	}{
		{
			name:  "nested extension scopes",
			owner: "extension.extension.value.ofTypeCoding",
			urls: map[string]string{
				"extension":                                   "urn:outer",
				"extension.extension":                         "urn:inner",
				"extension.extension.value.ofTypeCoding":      "urn:leaf",
				"extension.extension.value.ofTypeCoding.code": "urn:not-owner",
			},
		},
		{
			name:  "prefixes require a dot boundary",
			owner: "extensionFoo.extensionBar",
			urls: map[string]string{
				"extension":                 "urn:wrong-prefix",
				"extensionFoo":              "urn:root",
				"extensionFoo.extensionBar": "urn:leaf",
			},
		},
		{
			name:  "empty URLs are omitted",
			owner: "extension.value",
			urls: map[string]string{
				"extension":       "",
				"extension.value": "urn:value",
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			want := extensionURLAncestryScanOracle(test.owner, test.urls)
			got := extensionURLAncestry(test.owner, test.urls)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ancestry = %#v, want %#v", got, want)
			}
		})
	}
}

func extensionURLAncestryScanOracle(ownerPath string, urls map[string]string) []string {
	type ancestor struct {
		owner string
		url   string
	}
	ancestors := make([]ancestor, 0)
	for owner, url := range urls {
		if url == "" || (owner != ownerPath && !strings.HasPrefix(ownerPath, owner+".")) {
			continue
		}
		ancestors = append(ancestors, ancestor{owner: owner, url: url})
	}
	sort.Slice(ancestors, func(left, right int) bool {
		return len(ancestors[left].owner) < len(ancestors[right].owner)
	})
	result := make([]string, 0, len(ancestors))
	for _, ancestor := range ancestors {
		result = append(result, ancestor.url)
	}
	return result
}

func TestSchemaSemanticDiscoveryUsesResolvedDatatypesUnderUnfamiliarNames(t *testing.T) {
	index, err := schema.NewIndex([]schema.Definition{
		{
			Name: "SignalPacket",
			Elements: []schema.Element{
				{Name: "measurements", JSONType: schema.JSONTypeArray, ArrayElementType: "SignalReading"},
				{Name: "keys", JSONType: schema.JSONTypeArray, ArrayElementType: "Identifier"},
				{Name: "addons", JSONType: schema.JSONTypeArray, ArrayElementType: "Extension"},
			},
		},
		{
			Name: "SignalReading",
			Elements: []schema.Element{
				{Name: "meaning", JSONType: schema.JSONTypeObject, ReferencedType: "CodeableConcept"},
				{Name: "resultQuantity", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity", ChoiceGroup: "result", ChoiceGroupRequired: true},
				{Name: "resultString", JSONType: "string", ChoiceGroup: "result", ChoiceGroupRequired: true},
			},
		},
		{Name: "CodeableConcept", Elements: []schema.Element{
			{Name: "coding", JSONType: schema.JSONTypeArray, ArrayElementType: "Coding"},
			{Name: "text", JSONType: "string"},
		}},
		{Name: "Coding", Elements: []schema.Element{
			{Name: "system", JSONType: "string"},
			{Name: "code", JSONType: "string"},
			{Name: "display", JSONType: "string"},
		}},
		{Name: "Quantity", Elements: []schema.Element{
			{Name: "value", JSONType: "number"},
			{Name: "unit", JSONType: "string"},
		}},
		{Name: "Identifier", Elements: []schema.Element{
			{Name: "system", JSONType: "string"},
			{Name: "value", JSONType: "string"},
		}},
		{Name: "Extension", Elements: []schema.Element{
			{Name: "url", JSONType: "string"},
			{Name: "valueString", JSONType: "string", ChoiceGroup: "extension-value"},
		}},
	})
	if err != nil {
		t.Fatalf("NewIndex: %v", err)
	}
	p := NewProfilerForGeneration("project", "generation", "scope", "SignalPacket", nil)
	payload := map[string]any{
		"measurements": []any{map[string]any{
			"meaning": map[string]any{"coding": []any{map[string]any{
				"system": "urn:signals", "code": "zero", "display": "Zero signal",
			}}},
			"resultQuantity": map[string]any{"value": 0.0, "unit": "mg"},
		}},
		"keys": []any{map[string]any{
			"system": "urn:packet", "value": "packet-1",
		}},
		"addons": []any{map[string]any{
			"url": "urn:addon", "valueString": "present",
		}},
	}
	var observations []schemaSemanticEmission
	if err := p.observeSchemaSemantics(index, payload, "", func(observation SemanticObservation, examples []any) {
		observations = append(observations, schemaSemanticEmission{observation: observation, examples: examples})
	}); err != nil {
		t.Fatalf("observeSchemaSemantics: %v", err)
	}

	coded := findSchemaObservation(t, observations, SemanticRuleHintCodedValueV1)
	if coded.observation.Source.Path != "measurements[]" || coded.observation.OwningScope != "measurements[]" || coded.observation.Key.Selector != "meaning.coding[]" {
		t.Fatalf("coded owner = %#v", coded)
	}
	if coded.observation.Key.System != "urn:signals" || coded.observation.Key.Code != "zero" || coded.observation.Value.Selector != "resultQuantity.value" || coded.observation.Value.Type != "decimal" {
		t.Fatalf("coded pairing = %#v", coded)
	}
	if len(coded.examples) != 1 || fmt.Sprint(coded.examples[0]) != "0" {
		t.Fatalf("coded zero example = %#v", coded.examples)
	}
	if len(coded.observation.ObservedUnits) != 1 || coded.observation.ObservedUnits[0] != "mg" {
		t.Fatalf("coded units = %#v", coded.observation.ObservedUnits)
	}

	identifier := findSchemaObservation(t, observations, "IDENTIFIER_SYSTEM_VALUE")
	if identifier.observation.Source.Path != "keys[]" || identifier.observation.Key.Selector != "keys[].system" || identifier.observation.Value.Selector != "keys[].value" || identifier.observation.Key.System != "urn:packet" {
		t.Fatalf("identifier association = %#v", identifier)
	}

	extension := findSchemaObservation(t, observations, "EXTENSION_URL_VALUE")
	if extension.observation.Source.Path != "addons[]" || extension.observation.Key.Selector != "addons[].url" || extension.observation.Value.Selector != "addons[].valueString" {
		t.Fatalf("extension association = %#v", extension)
	}
	if fmt.Sprint(extension.observation.ExtensionURLPath) != "[urn:addon]" {
		t.Fatalf("extension ancestry = %#v", extension.observation.ExtensionURLPath)
	}
}

type schemaSemanticEmission struct {
	observation SemanticObservation
	examples    []any
}

func findSchemaObservation(t *testing.T, observations []schemaSemanticEmission, rule string) schemaSemanticEmission {
	t.Helper()
	for _, observation := range observations {
		if observation.observation.RuleHint == rule {
			return observation
		}
	}
	t.Fatalf("semantic observation with rule %q not found in %#v", rule, observations)
	return schemaSemanticEmission{}
}

func TestSchemaSemanticValueWalkMatchesByteWalkObservations(t *testing.T) {
	index, err := schema.NewIndex([]schema.Definition{
		{Name: "SignalPacket", Elements: []schema.Element{
			{Name: "keys", JSONType: schema.JSONTypeArray, ArrayElementType: "Identifier"},
			{Name: "addons", JSONType: schema.JSONTypeArray, ArrayElementType: "Extension"},
			{Name: "measurements", JSONType: schema.JSONTypeArray, ArrayElementType: "SignalReading"},
		}},
		{Name: "SignalReading", Elements: []schema.Element{
			{Name: "meaning", JSONType: schema.JSONTypeObject, ReferencedType: "CodeableConcept"},
			{Name: "resultQuantity", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity", ChoiceGroup: "result", ChoiceGroupRequired: true},
			{Name: "resultRange", JSONType: schema.JSONTypeObject, ReferencedType: "Range", ChoiceGroup: "result", ChoiceGroupRequired: true},
			{Name: "resultRatio", JSONType: schema.JSONTypeObject, ReferencedType: "Ratio", ChoiceGroup: "result", ChoiceGroupRequired: true},
			{Name: "resultString", JSONType: "string", ChoiceGroup: "result", ChoiceGroupRequired: true},
		}},
		{Name: "CodeableConcept", Elements: []schema.Element{
			{Name: "coding", JSONType: schema.JSONTypeArray, ArrayElementType: "Coding"},
			{Name: "text", JSONType: "string"},
		}},
		{Name: "Coding", Elements: []schema.Element{
			{Name: "system", JSONType: "string"}, {Name: "code", JSONType: "string"}, {Name: "display", JSONType: "string"},
		}},
		{Name: "Quantity", Elements: []schema.Element{{Name: "value", JSONType: "number"}, {Name: "unit", JSONType: "string"}}},
		{Name: "Range", Elements: []schema.Element{
			{Name: "low", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity"},
			{Name: "high", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity"},
		}},
		{Name: "Ratio", Elements: []schema.Element{
			{Name: "numerator", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity"},
			{Name: "denominator", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity"},
		}},
		{Name: "Identifier", Elements: []schema.Element{{Name: "system", JSONType: "string"}, {Name: "value", JSONType: "string"}}},
		{Name: "Extension", Elements: []schema.Element{
			{Name: "url", JSONType: "string"},
			{Name: "valueString", JSONType: "string", ChoiceGroup: "extension-value"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"keys": []any{map[string]any{"system": "urn:id", "value": "abc"}, nil},
		"addons": []any{
			map[string]any{"url": "urn:outer", "extension": []any{map[string]any{"url": "urn:inner", "valueString": "nested"}}},
			map[string]any{"url": "urn:blank", "valueString": ""},
		},
		"measurements": []any{
			map[string]any{
				"meaning": map[string]any{"text": "reading", "coding": []any{
					map[string]any{"system": "urn:one", "code": "1", "display": "One"},
					map[string]any{"system": "urn:two", "code": "2", "display": "Two"},
				}},
				"resultQuantity": map[string]any{"value": 0.0, "unit": "mg"},
			},
			map[string]any{"meaning": map[string]any{"coding": []any{}}, "resultString": "done"},
			map[string]any{"meaning": nil, "resultQuantity": nil},
			map[string]any{"meaning": map[string]any{"text": "large quantity"}, "resultQuantity": map[string]any{"value": int64(9007199254740993), "unit": "count"}},
			map[string]any{"meaning": map[string]any{"text": "large range"}, "resultRange": map[string]any{
				"low":  map[string]any{"value": int64(9007199254740993), "unit": "count"},
				"high": map[string]any{"value": int64(9007199254740995), "unit": "count"},
			}},
			map[string]any{"meaning": map[string]any{"text": "large ratio"}, "resultRatio": map[string]any{
				"numerator":   map[string]any{"value": int64(9007199254740993), "unit": "count"},
				"denominator": map[string]any{"value": int64(9007199254740995), "unit": "count"},
			}},
		},
	}
	input, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := sonic.ConfigFastest.Unmarshal(input, &decoded); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var byteFacts []fhirsemantic.MemberFact
	if _, err := fhirsemantic.WalkMembers(index, "SignalPacket", raw, func(fact fhirsemantic.MemberFact) error {
		value, valid := decodeSemanticValue(fact.RawJSON)
		if valid {
			fact.Value = value
		}
		byteFacts = append(byteFacts, fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var valueFacts []fhirsemantic.MemberFact
	if _, err := fhirsemantic.WalkMemberValues(index, "SignalPacket", decoded, func(fact fhirsemantic.MemberFact) error {
		valueFacts = append(valueFacts, fact)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(byteFacts) != len(valueFacts) {
		t.Fatalf("fact counts differ: byte=%d value=%d", len(byteFacts), len(valueFacts))
	}
	for i := range byteFacts {
		byteFact, valueFact := byteFacts[i], valueFacts[i]
		byteFact.RawJSON, valueFact.RawJSON = nil, nil
		byteFact.Value, valueFact.Value = nil, nil
		if !reflect.DeepEqual(byteFact, valueFact) {
			t.Fatalf("fact %d differs: byte=%#v value=%#v", i, byteFact, valueFact)
		}
		rawValue, ok := decodeSemanticValue(byteFacts[i].RawJSON)
		if !ok || !reflect.DeepEqual(rawValue, normalizeSemanticWalkValue(valueFacts[i].Value)) {
			t.Fatalf("fact %d value differs from raw JSON: raw=%#v value=%#v", i, rawValue, valueFacts[i].Value)
		}
	}
	byteEmissions := observeSchemaFactsForTest(t, index, "SignalPacket", "fixture/SignalPacket.ndjson:1", decoded, byteFacts)
	valueEmissions := observeSchemaFactsForTest(t, index, "SignalPacket", "fixture/SignalPacket.ndjson:1", decoded, valueFacts)
	if !reflect.DeepEqual(byteEmissions, valueEmissions) {
		t.Fatalf("semantic emission sequence differs:\nbyte=%#v\nvalue=%#v", byteEmissions, valueEmissions)
	}
}

type semanticEmissionForTest struct {
	Observation SemanticObservation
	Examples    []any
	Evidence    SemanticCoverageEmissionEvidence
}

func observeSchemaFactsForTest(t *testing.T, index *schema.Index, resourceType, sourceID string, payload map[string]any, facts []fhirsemantic.MemberFact) []semanticEmissionForTest {
	t.Helper()
	profiler := NewProfilerForGeneration("project", "generation", "scope", resourceType, nil)
	var emissions []semanticEmissionForTest
	err := profiler.observeSchemaSemanticsFromFacts(payload, "", sourceID, facts, &semanticWalkScratch{}, func(observation SemanticObservation, examples []any, evidence SemanticCoverageEmissionEvidence) {
		emissions = append(emissions, semanticEmissionForTest{observation, examples, evidence})
	})
	if err != nil {
		t.Fatal(err)
	}
	return emissions
}

func normalizeSemanticWalkValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized[key] = normalizeSemanticWalkValue(item)
		}
		return normalized
	case []any:
		normalized := make([]any, len(typed))
		for index, item := range typed {
			normalized[index] = normalizeSemanticWalkValue(item)
		}
		return normalized
	default:
		return semanticUseJSONNumber(value)
	}
}

func TestSemanticValueWalkMatchesFixtureNDJSON(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	fixtures := filepath.Join("..", "..", "tests", "fixtures")
	var files []string
	if err := filepath.WalkDir(fixtures, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".ndjson") {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no fixture NDJSON files found")
	}
	if metaDir := os.Getenv("LOOM_INGEST_PERF_META"); metaDir != "" {
		if err := filepath.WalkDir(metaDir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".ndjson") {
				files = append(files, path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows := 0
	for _, path := range files {
		resourceType := strings.TrimSuffix(filepath.Base(path), ".ndjson")
		profiler := NewProfilerForGeneration("project", "generation", "scope", resourceType, nil)
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 1024), 64*1024*1024)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := append([]byte(nil), scanner.Bytes()...)
			var payload map[string]any
			if err := sonic.ConfigFastest.Unmarshal(line, &payload); err != nil {
				file.Close()
				t.Fatalf("%s:%d decode: %v", path, lineNumber, err)
			}
			encoded, err := json.Marshal(payload)
			if err != nil {
				file.Close()
				t.Fatalf("%s:%d encode: %v", path, lineNumber, err)
			}
			var byteFacts []fhirsemantic.MemberFact
			byteDiagnostics, err := fhirsemantic.WalkMembers(index, schema.DefinitionName(resourceType), encoded, func(fact fhirsemantic.MemberFact) error {
				value, valid := decodeSemanticValue(fact.RawJSON)
				if valid {
					fact.Value = value
				}
				byteFacts = append(byteFacts, fact)
				return nil
			})
			if err != nil {
				file.Close()
				t.Fatalf("%s:%d byte walk: %v", path, lineNumber, err)
			}
			var valueFacts []fhirsemantic.MemberFact
			valueDiagnostics, err := fhirsemantic.WalkMemberValues(index, schema.DefinitionName(resourceType), payload, func(fact fhirsemantic.MemberFact) error {
				valueFacts = append(valueFacts, fact)
				return nil
			})
			if err != nil {
				file.Close()
				t.Fatalf("%s:%d value walk: %v", path, lineNumber, err)
			}
			if !reflect.DeepEqual(byteDiagnostics, valueDiagnostics) || len(byteFacts) != len(valueFacts) {
				file.Close()
				t.Fatalf("%s:%d diagnostics/fact count differ: byte %d/%d value %d/%d", path, lineNumber, len(byteDiagnostics), len(byteFacts), len(valueDiagnostics), len(valueFacts))
			}
			for factIndex := range byteFacts {
				oldFact, newFact := byteFacts[factIndex], valueFacts[factIndex]
				oldFact.RawJSON, newFact.RawJSON = nil, nil
				oldFact.Value, newFact.Value = nil, nil
				if !reflect.DeepEqual(oldFact, newFact) {
					file.Close()
					t.Fatalf("%s:%d fact %d differs: byte=%#v value=%#v", path, lineNumber, factIndex, oldFact, newFact)
				}
			}
			oldEmissions := observeSchemaFactsForTest(t, index, resourceType, fmt.Sprintf("%s:%d", path, lineNumber), payload, byteFacts)
			var newEmissions []semanticEmissionForTest
			if err := profiler.observeSchemaSemanticsWithEvidence(index, payload, "", fmt.Sprintf("%s:%d", path, lineNumber), func(observation SemanticObservation, examples []any, evidence SemanticCoverageEmissionEvidence) {
				newEmissions = append(newEmissions, semanticEmissionForTest{observation, examples, evidence})
			}); err != nil {
				file.Close()
				t.Fatalf("%s:%d observe with reused profiler: %v", path, lineNumber, err)
			}
			if !reflect.DeepEqual(oldEmissions, newEmissions) {
				file.Close()
				t.Fatalf("%s:%d semantic emissions differ: byte=%#v value=%#v", path, lineNumber, oldEmissions, newEmissions)
			}
			rows++
		}
		if err := scanner.Err(); err != nil {
			file.Close()
			t.Fatalf("%s scan: %v", path, err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("compared byte and value walks for %d fixture rows in %d files", rows, len(files))
}

func TestSemanticProfilerRecoversAfterPartialWalk(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatal(err)
	}
	newProfiler := func() *Profiler { return NewProfilerForGeneration("project", "generation", "scope", "Patient", nil) }
	profiler := newProfiler()
	bad := map[string]any{
		"extension":  []any{map[string]any{"url": "urn:example", "valueString": "discard"}},
		"identifier": map[string]any{"system": "urn:invalid"},
	}
	if err := profiler.observeSchemaSemanticsWithEvidence(index, bad, "", "bad", func(SemanticObservation, []any, SemanticCoverageEmissionEvidence) {
		t.Fatal("a partial walk emitted a semantic observation")
	}); err == nil {
		t.Fatal("expected the identifier to fail the member walk")
	}
	good := map[string]any{
		"identifier": []any{map[string]any{"system": "urn:patient", "value": "new"}},
	}
	observe := func(profiler *Profiler) []semanticEmissionForTest {
		var result []semanticEmissionForTest
		err := profiler.observeSchemaSemanticsWithEvidence(index, good, "", "good", func(observation SemanticObservation, examples []any, evidence SemanticCoverageEmissionEvidence) {
			result = append(result, semanticEmissionForTest{observation, examples, evidence})
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	reused, fresh := observe(profiler), observe(newProfiler())
	if len(fresh) != 1 || !reflect.DeepEqual(reused, fresh) {
		t.Fatalf("reused profiler = %#v, fresh profiler = %#v", reused, fresh)
	}
}
