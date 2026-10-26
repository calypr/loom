package semantic

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
)

func TestWalkFindsDatatypesFromSyntheticFutureDefinition(t *testing.T) {
	index := futureIndex(t)
	payload := []byte(`{"identifiers":[{"system":"urn:one","value":"A"}],"measurement":{"value":1,"unit":"mg"},"interpretation":{"coding":[{"system":"urn:test","code":"measured"}]},"component":[{"identifier":{"system":"urn:one","value":"first"},"quantity":{"value":2},"concept":{"text":"first"}},{"identifier":{"system":"urn:two","value":"second"},"quantity":{"value":3},"concept":{"text":"second"}}],"extension":[{"url":"outer","extension":[{"url":"middle","valueIdentifier":{"system":"urn:extension","value":"nested"}}]}]}`)

	occurrences := walkOccurrences(t, index, "FutureRoot", payload)
	gotTypes := make(map[schema.DefinitionName]bool)
	for _, occurrence := range occurrences {
		gotTypes[occurrence.ReferencedType] = true
	}
	for _, want := range []schema.DefinitionName{"Identifier", "Quantity", "CodeableConcept", "Coding", "FutureComponent", "Extension"} {
		if !gotTypes[want] {
			t.Errorf("walker did not report referenced datatype %q", want)
		}
	}
	if got := findOccurrence(t, occurrences, "component[].identifier"); got.OwnerPath != "component[0].identifier" {
		t.Fatalf("first component identifier owner path = %q", got.OwnerPath)
	}
	if got := findOccurrence(t, occurrences, "extension[].extension[].valueIdentifier"); got.ReferencedType != "Identifier" {
		t.Fatalf("nested extension choice occurrence = %#v", got)
	}
}

func TestWalkUsesGeneratedIndexOnObservationFixture(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("GeneratedIndex: %v", err)
	}
	payload := readNDJSONLine(t, "../../../testdata/devloop-fixture/Observation.ndjson", 4)
	occurrences := walkOccurrences(t, index, "Observation", payload)

	concept := findOccurrence(t, occurrences, "code")
	if concept.ReferencedType != "CodeableConcept" {
		t.Fatalf("Observation.code type = %q, want CodeableConcept", concept.ReferencedType)
	}
	coding := findOccurrence(t, occurrences, "code.coding[]")
	if coding.ReferencedType != "Coding" || coding.OwnerPath != "code.coding[0]" {
		t.Fatalf("Observation.code.coding occurrence = %#v", coding)
	}

	quantityOwners := occurrencesAt(t, occurrences, "component[].valueQuantity", "Quantity")
	wantQuantityOwners := []string{"component[0].valueQuantity", "component[1].valueQuantity"}
	if len(quantityOwners) != len(wantQuantityOwners) {
		t.Fatalf("component valueQuantity occurrences = %d, want %d", len(quantityOwners), len(wantQuantityOwners))
	}
	for index, wantOwner := range wantQuantityOwners {
		occurrence := quantityOwners[index]
		if occurrence.OwnerPath != wantOwner || len(occurrence.RepeatedBoundaries) != 1 {
			t.Errorf("component quantity occurrence = %#v, want owner %q and one repeated boundary", occurrence, wantOwner)
			continue
		}
		boundary := occurrence.RepeatedBoundaries[0]
		if boundary.CanonicalPath != "component[]" || boundary.OwnerPath != strings.TrimSuffix(wantOwner, ".valueQuantity") || boundary.Index != index {
			t.Errorf("component quantity boundary = %#v, want owner %q at index %d", boundary, strings.TrimSuffix(wantOwner, ".valueQuantity"), index)
		}
	}

	nestedExtensions := occurrencesAt(t, occurrences, "extension[].extension[]", "Extension")
	wantExtensionOwners := []string{"extension[0].extension[0]", "extension[1].extension[0]"}
	if len(nestedExtensions) != len(wantExtensionOwners) {
		t.Fatalf("nested Extension occurrences = %d, want %d", len(nestedExtensions), len(wantExtensionOwners))
	}
	for index, wantOwner := range wantExtensionOwners {
		occurrence := nestedExtensions[index]
		if occurrence.OwnerPath != wantOwner || len(occurrence.RepeatedBoundaries) != 2 {
			t.Errorf("nested Extension occurrence = %#v, want owner %q and two repeated boundaries", occurrence, wantOwner)
			continue
		}
		outer, inner := occurrence.RepeatedBoundaries[0], occurrence.RepeatedBoundaries[1]
		wantOuterOwner := strings.TrimSuffix(wantOwner, ".extension[0]")
		if outer.CanonicalPath != "extension[]" || outer.OwnerPath != wantOuterOwner || outer.Index != index {
			t.Errorf("outer Extension boundary = %#v, want owner %q at index %d", outer, wantOuterOwner, index)
		}
		if inner.CanonicalPath != "extension[].extension[]" || inner.OwnerPath != wantOwner || inner.Index != 0 {
			t.Errorf("nested Extension boundary = %#v, want owner %q at index 0", inner, wantOwner)
		}
	}
}

func TestWalkKeepsRepeatedOwnersWhenArrayOrderChanges(t *testing.T) {
	index := futureIndex(t)
	first := []byte(`{"component":[{"identifier":{"value":"alpha"},"quantity":{"value":10},"concept":{"text":"alpha"}},{"identifier":{"value":"beta"},"quantity":{"value":20},"concept":{"text":"beta"}}]}`)
	second := []byte(`{"component":[{"identifier":{"value":"beta"},"quantity":{"value":20},"concept":{"text":"beta"}},{"identifier":{"value":"alpha"},"quantity":{"value":10},"concept":{"text":"alpha"}}]}`)
	firstOccurrences := walkOccurrences(t, index, "FutureRoot", first)
	secondOccurrences := walkOccurrences(t, index, "FutureRoot", second)

	assertComponentOwners(t, firstOccurrences, "alpha", "beta")
	assertComponentOwners(t, secondOccurrences, "beta", "alpha")
}

func TestWalkFollowsFiniteRecursiveExtensionInstances(t *testing.T) {
	const nestedDepth = 40
	index := futureIndex(t)
	nested := `{"url":"level-0","valueIdentifier":{"value":"deep"}}`
	for depth := 1; depth <= nestedDepth; depth++ {
		nested = fmt.Sprintf(`{"url":"level-%d","extension":[%s]}`, depth, nested)
	}
	payload := []byte(`{"extension":[` + nested + `]}`)

	occurrences := walkOccurrences(t, index, "FutureRoot", payload)
	var deepest Occurrence
	for _, occurrence := range occurrences {
		if occurrence.ReferencedType == "Identifier" && occurrence.RawJSON != nil {
			deepest = occurrence
		}
	}
	if deepest.OwnerPath == "" {
		t.Fatal("walker did not reach the identifier at the end of the extension chain")
	}
	if got := len(deepest.RepeatedBoundaries); got != nestedDepth+1 {
		t.Fatalf("deep identifier has %d repeated boundaries, want %d", got, nestedDepth+1)
	}
}

func TestWalkReturnsAnExactCopyOfEachReferencedObject(t *testing.T) {
	index := futureIndex(t)
	payload := []byte("{\n  \"identifiers\": [{\"system\": \"urn:one\", \"value\": \"A\"}]\n}")
	occurrences := walkOccurrences(t, index, "FutureRoot", payload)
	got := findOccurrence(t, occurrences, "identifiers[]")
	want := json.RawMessage(`{"system": "urn:one", "value": "A"}`)
	if !bytes.Equal(got.RawJSON, want) {
		t.Fatalf("raw object = %s, want %s", got.RawJSON, want)
	}
	valueOffset := bytes.Index(payload, []byte(`"A"`))
	if valueOffset < 0 {
		t.Fatal("payload does not contain the expected identifier value")
	}
	valueOffset++
	payload[valueOffset] = 'B'
	if !bytes.Equal(got.RawJSON, want) {
		t.Fatal("occurrence raw JSON aliases the root payload")
	}
	got.RawJSON[0] = '['
	if payload[valueOffset] != 'B' {
		t.Fatal("mutating occurrence raw JSON changed the root payload")
	}
}

func TestWalkDoesNotCloneReferencedDefinitionForEveryOccurrence(t *testing.T) {
	const (
		occurrenceCount = 96
		definitionDepth = 48
	)
	index := deepOccurrenceIndex(t, definitionDepth)
	var payload strings.Builder
	payload.WriteString(`{"items":[`)
	for item := 0; item < occurrenceCount; item++ {
		if item > 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{}`)
	}
	payload.WriteString(`]}`)
	manyOccurrences := []byte(payload.String())
	noOccurrences := []byte(`{"items":[]}`)
	visit := func(Occurrence) error { return nil }
	measure := func(json []byte) float64 {
		return testing.AllocsPerRun(5, func() {
			if err := Walk(index, "FutureRoot", json, visit); err != nil {
				t.Fatalf("Walk: %v", err)
			}
		})
	}
	allocationsWithItems := measure(manyOccurrences)
	allocationsWithoutItems := measure(noOccurrences)
	allocationsPerOccurrence := (allocationsWithItems - allocationsWithoutItems) / occurrenceCount
	t.Logf("Walk added %.1f allocations per repeated occurrence", allocationsPerOccurrence)
	if allocationsPerOccurrence > 20 {
		t.Fatalf("Walk used %.1f allocations per repeated occurrence; want at most 20 (deep definition depth %d)", allocationsPerOccurrence, definitionDepth)
	}
}

func TestWalkRejectsInvalidBoundaryInputsAndPropagatesVisitorErrors(t *testing.T) {
	index := futureIndex(t)
	visitorErr := errors.New("stop walk")
	tests := []struct {
		name    string
		index   *schema.Index
		root    schema.DefinitionName
		payload []byte
		visit   func(Occurrence) error
	}{
		{name: "nil index", root: "FutureRoot", payload: []byte(`{}`), visit: func(Occurrence) error { return nil }},
		{name: "unknown root", index: index, root: "Unknown", payload: []byte(`{}`), visit: func(Occurrence) error { return nil }},
		{name: "invalid json", index: index, root: "FutureRoot", payload: []byte(`{`), visit: func(Occurrence) error { return nil }},
		{name: "non-object root", index: index, root: "FutureRoot", payload: []byte(`[]`), visit: func(Occurrence) error { return nil }},
		{name: "nil visitor", index: index, root: "FutureRoot", payload: []byte(`{}`)},
		{name: "visitor error", index: index, root: "FutureRoot", payload: []byte(`{"measurement":{"value":1}}`), visit: func(Occurrence) error { return visitorErr }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := Walk(test.index, test.root, test.payload, test.visit)
			if err == nil {
				t.Fatal("Walk returned no error")
			}
			if test.name == "visitor error" && !errors.Is(err, visitorErr) {
				t.Fatalf("Walk error = %v, want %v", err, visitorErr)
			}
		})
	}
}

func futureIndex(t *testing.T) *schema.Index {
	t.Helper()
	index, err := schema.NewIndex([]schema.Definition{
		{
			Name: "FutureRoot",
			Elements: []schema.Element{
				{Name: "identifiers", JSONType: schema.JSONTypeArray, ArrayElementType: "Identifier"},
				{Name: "measurement", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity", ChoiceGroup: "result", ChoiceGroupRequired: true},
				{Name: "interpretation", JSONType: schema.JSONTypeObject, ReferencedType: "CodeableConcept", ChoiceGroup: "result", ChoiceGroupRequired: true},
				{Name: "component", JSONType: schema.JSONTypeArray, ArrayElementType: "FutureComponent"},
				{Name: "extension", JSONType: schema.JSONTypeArray, ArrayElementType: "Extension"},
			},
		},
		{
			Name: "Identifier",
			Elements: []schema.Element{
				{Name: "system", JSONType: "string"},
				{Name: "value", JSONType: "string"},
				{Name: "extension", JSONType: schema.JSONTypeArray, ArrayElementType: "Extension"},
			},
		},
		{
			Name: "Quantity",
			Elements: []schema.Element{
				{Name: "value", JSONType: "number"},
				{Name: "comparator", JSONType: "string"},
				{Name: "unit", JSONType: "string"},
				{Name: "system", JSONType: "string"},
				{Name: "code", JSONType: "string"},
			},
		},
		{
			Name: "CodeableConcept",
			Elements: []schema.Element{
				{Name: "coding", JSONType: schema.JSONTypeArray, ArrayElementType: "Coding"},
				{Name: "text", JSONType: "string"},
			},
		},
		{
			Name: "Coding",
			Elements: []schema.Element{
				{Name: "system", JSONType: "string"},
				{Name: "version", JSONType: "string"},
				{Name: "code", JSONType: "string"},
				{Name: "display", JSONType: "string"},
			},
		},
		{
			Name: "FutureComponent",
			Elements: []schema.Element{
				{Name: "identifier", JSONType: schema.JSONTypeObject, ReferencedType: "Identifier"},
				{Name: "quantity", JSONType: schema.JSONTypeObject, ReferencedType: "Quantity"},
				{Name: "concept", JSONType: schema.JSONTypeObject, ReferencedType: "CodeableConcept"},
			},
		},
		{
			Name: "Extension",
			Elements: []schema.Element{
				{Name: "url", JSONType: "string"},
				{Name: "valueIdentifier", JSONType: schema.JSONTypeObject, ReferencedType: "Identifier", ChoiceGroup: "value"},
				{Name: "extension", JSONType: schema.JSONTypeArray, ArrayElementType: "Extension"},
			},
		},
	})
	if err != nil {
		t.Fatalf("create future schema index: %v", err)
	}
	return index
}

func deepOccurrenceIndex(t *testing.T, depth int) *schema.Index {
	t.Helper()
	elements := []schema.Element{{Name: "leaf", JSONType: "string"}}
	for level := 0; level < depth; level++ {
		elements = []schema.Element{{
			Name:     fmt.Sprintf("unused%d", level),
			JSONType: schema.JSONTypeObject,
			Elements: elements,
		}}
	}
	index, err := schema.NewIndex([]schema.Definition{
		{
			Name: "FutureRoot",
			Elements: []schema.Element{{
				Name:             "items",
				JSONType:         schema.JSONTypeArray,
				ArrayElementType: "DeepDatatype",
			}},
		},
		{Name: "DeepDatatype", Elements: elements},
	})
	if err != nil {
		t.Fatalf("create deep occurrence index: %v", err)
	}
	return index
}

func walkOccurrences(t *testing.T, index *schema.Index, root schema.DefinitionName, payload []byte) []Occurrence {
	t.Helper()
	var occurrences []Occurrence
	if err := Walk(index, root, payload, func(occurrence Occurrence) error {
		occurrences = append(occurrences, occurrence)
		return nil
	}); err != nil {
		t.Fatalf("Walk: %v", err)
	}
	return occurrences
}

func occurrencesAt(t *testing.T, occurrences []Occurrence, canonicalPath string, referencedType schema.DefinitionName) []Occurrence {
	t.Helper()
	matched := make([]Occurrence, 0)
	for _, occurrence := range occurrences {
		if occurrence.CanonicalPath == canonicalPath && occurrence.ReferencedType == referencedType {
			matched = append(matched, occurrence)
		}
	}
	return matched
}

func readNDJSONLine(t *testing.T, relativePath string, lineNumber int) []byte {
	t.Helper()
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve semantic test source path")
	}
	fixturePath := filepath.Join(filepath.Dir(sourcePath), relativePath)
	file, err := os.Open(fixturePath)
	if err != nil {
		t.Fatalf("open fixture %s: %v", fixturePath, err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close fixture %s: %v", fixturePath, err)
		}
	})

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	for currentLine := 1; currentLine <= lineNumber; currentLine++ {
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				t.Fatalf("read fixture %s line %d: %v", fixturePath, currentLine, err)
			}
			t.Fatalf("fixture %s has no line %d", fixturePath, lineNumber)
		}
		if currentLine == lineNumber {
			return append([]byte(nil), scanner.Bytes()...)
		}
	}
	t.Fatalf("fixture line number %d was not reached", lineNumber)
	return nil
}

func findOccurrence(t *testing.T, occurrences []Occurrence, canonicalPath string) Occurrence {
	t.Helper()
	for _, occurrence := range occurrences {
		if occurrence.CanonicalPath == canonicalPath {
			return occurrence
		}
	}
	t.Fatalf("no occurrence at canonical path %q", canonicalPath)
	return Occurrence{}
}

func assertComponentOwners(t *testing.T, occurrences []Occurrence, first, second string) {
	t.Helper()
	quantityValues := map[string]int{"alpha": 10, "beta": 20}
	for index, want := range []string{first, second} {
		prefix := fmt.Sprintf("component[%d].", index)
		identifierOccurrence := occurrenceAtOwner(t, occurrences, prefix+"identifier")
		var identifier struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(identifierOccurrence.RawJSON, &identifier); err != nil {
			t.Fatalf("decode %sidentifier: %v", prefix, err)
		}
		if identifier.Value != want {
			t.Errorf("%sidentifier has value %q, want %q", prefix, identifier.Value, want)
		}
		if len(identifierOccurrence.RepeatedBoundaries) != 1 || identifierOccurrence.RepeatedBoundaries[0].Index != index {
			t.Errorf("%sidentifier boundaries = %#v, want index %d", prefix, identifierOccurrence.RepeatedBoundaries, index)
		}

		quantity := occurrenceAtOwner(t, occurrences, prefix+"quantity")
		var quantityValue struct {
			Value int `json:"value"`
		}
		if err := json.Unmarshal(quantity.RawJSON, &quantityValue); err != nil {
			t.Fatalf("decode %squantity: %v", prefix, err)
		}
		if quantity.OwnerPath != prefix+"quantity" || quantityValue.Value != quantityValues[want] {
			t.Errorf("%squantity has owner %q and value %d", prefix, quantity.OwnerPath, quantityValue.Value)
		}

		concept := occurrenceAtOwner(t, occurrences, prefix+"concept")
		var conceptText struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(concept.RawJSON, &conceptText); err != nil {
			t.Fatalf("decode %sconcept: %v", prefix, err)
		}
		if concept.OwnerPath != prefix+"concept" || conceptText.Text != want {
			t.Errorf("%sconcept has owner %q and text %q, want %q", prefix, concept.OwnerPath, conceptText.Text, want)
		}
	}
}

func occurrenceAtOwner(t *testing.T, occurrences []Occurrence, ownerPath string) Occurrence {
	t.Helper()
	for _, occurrence := range occurrences {
		if occurrence.OwnerPath == ownerPath {
			return occurrence
		}
	}
	t.Fatalf("no occurrence has concrete owner path %q", ownerPath)
	return Occurrence{}
}
