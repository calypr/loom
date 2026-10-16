package catalog

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/fhir/schema"
	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

func TestSemanticCoverageCensusCountsOwnersAndCodingsByConcretePath(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	payload := json.RawMessage(`{
		"resourceType":"Observation",
		"status":"final",
		"code":{"coding":[{"system":"urn:loinc","code":"root"}]},
		"component":[
			{
				"code":{"coding":[{"system":"urn:test","code":"component-a"}]},
				"valueCodeableConcept":{"coding":[
					{"system":"urn:value","code":"one"},
					{"system":"urn:value","code":"two"},
					{"system":"urn:value","code":"three"}
				]}
			},
			{
				"code":{"coding":[{"system":"urn:test","code":"component-b"}]},
				"valueCodeableConcept":{"coding":[{"system":"urn:value","code":"three"}]}
			}
		],
		"extension":[{"url":"parent","extension":[
			{"url":"leaf","valueCodeableConcept":{"text":"nested text"}}
		]}]
	}`)

	census, err := CensusSemanticCoverage(index, "Observation", "source-1", payload)
	if err != nil {
		t.Fatalf("census semantic coverage: %v", err)
	}
	if census.Status != SemanticCoverageCensusComplete || len(census.IncompleteGaps) != 0 {
		t.Fatalf("census status/gaps = %q/%#v, want complete with no census gaps", census.Status, census.IncompleteGaps)
	}
	if census.Counts.CodeableConceptOwners != 6 {
		t.Fatalf("CodeableConcept owner count = %d, want 6", census.Counts.CodeableConceptOwners)
	}
	if census.Counts.CodingOccurrences != 7 {
		t.Fatalf("Coding occurrence count = %d, want 7", census.Counts.CodingOccurrences)
	}
	if census.Counts.CodeableConceptOwners == census.Counts.CodingOccurrences {
		t.Fatal("CodeableConcept owners and Coding occurrences were conflated")
	}

	var componentValueOwners []string
	for _, path := range census.Paths {
		if path.Kind != SemanticCoverageCodeableConcept || path.CanonicalPath != "component[].valueCodeableConcept" {
			continue
		}
		componentValueOwners = append(componentValueOwners, path.OwnerPathWitnesses...)
	}
	want := []string{
		"component[0].valueCodeableConcept",
		"component[1].valueCodeableConcept",
	}
	if !reflect.DeepEqual(componentValueOwners, want) {
		t.Fatalf("component value owner witnesses = %#v, want %#v", componentValueOwners, want)
	}
}

func containsCoverageGap(gaps []SemanticCoverageCensusGap, target SemanticCoverageCensusGap) bool {
	for _, gap := range gaps {
		if gap == target {
			return true
		}
	}
	return false
}

func TestSemanticCoverageGeneratedTemplatesIncludeRecursiveChoiceAndCompositeWitnesses(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	templates, err := DiscoverSemanticCodeableConceptTemplates(index)
	if err != nil {
		t.Fatalf("discover generated CodeableConcept templates: %v", err)
	}
	if len(templates) == 0 {
		t.Fatal("generated schema discovery returned no CodeableConcept templates")
	}

	var componentChoice, recursiveExtension, codeableReference bool
	seen := make(map[string]struct{}, len(templates))
	for _, template := range templates {
		key := template.ResourceType + "\x00" + template.CanonicalPath + "\x00" + template.DeclaringType
		if _, duplicate := seen[key]; duplicate {
			t.Errorf("duplicate generated schema witness %q", key)
		}
		seen[key] = struct{}{}
		if template.ResourceType == "Observation" && template.CanonicalPath == "component[].valueCodeableConcept" {
			componentChoice = template.Repeated && template.ChoiceGroup != "" && containsString(template.DefinitionPath, "ObservationComponent")
		}
		if template.ResourceType == "Observation" && template.CanonicalPath == "extension[].valueCodeableConcept" {
			recursiveExtension = template.RecursiveDefinition == "Extension" && template.RecursivePath != ""
		}
		if containsString(template.DefinitionPath, "CodeableReference") && strings.HasSuffix(template.CanonicalPath, ".concept") {
			codeableReference = true
		}
	}
	if !componentChoice {
		t.Fatal("missing generated repeated-backbone value[x] CodeableConcept witness")
	}
	if !recursiveExtension {
		t.Fatal("missing cycle-aware recursive Extension CodeableConcept witness")
	}
	if !codeableReference {
		t.Fatal("missing generated CodeableReference.concept CodeableConcept witness")
	}
}

func TestSemanticCoverageGeneratedTemplatesHavePayloadWitnesses(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	templates, err := DiscoverSemanticCodeableConceptTemplates(index)
	if err != nil {
		t.Fatalf("discover generated CodeableConcept templates: %v", err)
	}
	t.Logf("validated %d generated CodeableConcept templates", len(templates))
	for _, template := range templates {
		template := template
		t.Run(template.ResourceType+"/"+template.CanonicalPath, func(t *testing.T) {
			payload := semanticCoverageTemplateWitness(t, index, template)
			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("marshal generated witness: %v", err)
			}
			census, err := CensusSemanticCoverage(index, template.ResourceType, "schema-witness", raw)
			if err != nil {
				t.Fatalf("census generated witness: %v", err)
			}
			concept, found := semanticCoveragePath(census, SemanticCoverageCodeableConcept, template.CanonicalPath)
			if !found {
				t.Fatalf("generated path %q was not rediscovered by census; paths=%#v", template.CanonicalPath, census.Paths)
			}
			if concept.OccurrenceCount != 1 {
				t.Fatalf("generated concept path %q count = %d, want 1; all counts=%#v", template.CanonicalPath, concept.OccurrenceCount, census.Counts)
			}
			coding, found := semanticCoveragePath(census, SemanticCoverageCoding, template.CanonicalPath+".coding[]")
			if !found || coding.OccurrenceCount != 1 {
				t.Fatalf("generated Coding child for %q = %#v, found=%t; want exactly one", template.CanonicalPath, coding, found)
			}
		})
	}
}

func TestSemanticCoverageDiscoveryMatchesIndependentGeneratedDefinitionDenominator(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	discovered, err := DiscoverSemanticCodeableConceptTemplates(index)
	if err != nil {
		t.Fatalf("discover generated CodeableConcept templates: %v", err)
	}
	actual := make(map[string]SemanticCodeableConceptTemplate, len(discovered))
	independentCycleTargets := make(map[string]struct{})
	for _, template := range discovered {
		key := generatedCoverageTemplateKey(template.ResourceType, template.DeclaringType, lastCoverageMember(template.CanonicalPath), template.ChoiceGroup, template.CanonicalPath)
		actual[key] = template
		if template.RecursiveDefinition == "Extension" && template.RecursivePath != "" {
			independentCycleTargets[generatedCoverageCycleKey(template.ResourceType, template.RecursiveDefinition, template.RecursivePath)] = struct{}{}
		}
	}
	expected, witnessedExtensionCycle, err := independentGeneratedCodeableConceptDenominator(index, independentCycleTargets)
	if err != nil {
		t.Fatalf("walk independent generated-schema denominator: %v", err)
	}
	if len(expected) == 0 {
		t.Fatal("independent generated-schema denominator is empty")
	}
	for key := range expected {
		if _, ok := actual[key]; !ok {
			t.Errorf("generated discovery omitted independently reachable CodeableConcept template %s", key)
		}
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			t.Errorf("generated discovery returned template absent from independent denominator %s", key)
		}
	}
	if len(actual) != len(expected) {
		t.Errorf("discovery returned %d templates; independent denominator has %d", len(actual), len(expected))
	}

	if !witnessedExtensionCycle {
		t.Fatal("generated discovery did not retain an independently observed recursive Extension edge witness")
	}
	t.Logf("independent generated-definition denominator validated %d generated template fixtures and a recursive Extension witness", len(expected))
}

func independentGeneratedCodeableConceptDenominator(index *schema.Index, cycleWitnessTargets map[string]struct{}) (map[string]struct{}, bool, error) {
	denominator := make(map[string]struct{})
	witnessedExtensionCycle := false
	for _, resourceType := range schema.ResourceTypes() {
		root := schema.DefinitionName(resourceType)
		if _, ok := index.ReadDefinition(root); !ok {
			return nil, false, fmt.Errorf("resource definition %q is missing", root)
		}
		var visitDefinition func(schema.DefinitionName, string, []schema.DefinitionName) error
		var visitElements func(schema.DefinitionName, string, []schema.DefinitionName, []schema.Element) error
		visitDefinition = func(name schema.DefinitionName, prefix string, stack []schema.DefinitionName) error {
			definition, ok := index.Definition(name)
			if !ok {
				return fmt.Errorf("definition %q is missing", name)
			}
			stack = append(append([]schema.DefinitionName(nil), stack...), name)
			return visitElements(name, prefix, stack, definition.Elements)
		}
		visitElements = func(declaringType schema.DefinitionName, prefix string, stack []schema.DefinitionName, elements []schema.Element) error {
			for _, element := range elements {
				member := element.Name
				path := joinCoveragePath(prefix, member)
				child := element.ReferencedType
				inline := element.Elements
				if element.JSONType == schema.JSONTypeArray {
					path += "[]"
					child = element.ArrayElementType
					inline = element.ArrayElements
				}
				if child == "CodeableConcept" {
					key := generatedCoverageTemplateKey(resourceType, string(declaringType), member, element.ChoiceGroup, path)
					denominator[key] = struct{}{}
					continue
				}
				if child != "" {
					if containsDefinition(stack, child) {
						if child == "Extension" {
							if _, ok := cycleWitnessTargets[generatedCoverageCycleKey(resourceType, string(child), path)]; ok {
								witnessedExtensionCycle = true
							}
						}
						continue
					}
					if err := visitDefinition(child, path, stack); err != nil {
						return err
					}
					continue
				}
				if len(inline) > 0 {
					if err := visitElements(declaringType, path, stack, inline); err != nil {
						return err
					}
				}
			}
			return nil
		}
		if err := visitDefinition(root, "", nil); err != nil {
			return nil, false, fmt.Errorf("resource %q: %w", resourceType, err)
		}
	}
	return denominator, witnessedExtensionCycle, nil
}

func generatedCoverageTemplateKey(resourceType, declaringType, member, choiceGroup, path string) string {
	return strings.Join([]string{resourceType, declaringType, member, choiceGroup, path}, "\x00")
}

func generatedCoverageCycleKey(resourceType, definition, path string) string {
	return strings.Join([]string{resourceType, definition, path}, "\x00")
}

func lastCoverageMember(path string) string {
	if index := strings.LastIndex(path, "."); index >= 0 {
		path = path[index+1:]
	}
	return strings.TrimSuffix(path, "[]")
}

func semanticCoverageTemplateWitness(t *testing.T, index *schema.Index, template SemanticCodeableConceptTemplate) map[string]any {
	t.Helper()
	rootDefinition, ok := index.ReadDefinition(schema.DefinitionName(template.ResourceType))
	if !ok {
		t.Fatalf("missing root schema definition %q", template.ResourceType)
	}
	root := map[string]any{"resourceType": template.ResourceType}
	current := root
	elements := rootDefinition.Elements()
	parts := strings.Split(template.CanonicalPath, ".")
	for partIndex, part := range parts {
		repeated := strings.HasSuffix(part, "[]")
		name := strings.TrimSuffix(part, "[]")
		element, found := semanticCoverageSchemaElement(elements, name)
		if !found {
			t.Fatalf("path %q: schema element %q was not found", template.CanonicalPath, name)
		}
		if partIndex == len(parts)-1 {
			concept := map[string]any{"coding": []any{map[string]any{"system": "urn:witness", "code": "witness"}}}
			if repeated {
				current[name] = []any{concept}
			} else {
				current[name] = concept
			}
			break
		}
		child := make(map[string]any)
		if repeated {
			current[name] = []any{child}
		} else {
			current[name] = child
		}
		childElements := element.Elements()
		childDefinition := element.ReferencedType()
		if element.JSONType() == schema.JSONTypeArray {
			childElements = element.ArrayElements()
			childDefinition = element.ArrayElementType()
		}
		if childDefinition != "" {
			definition, exists := index.ReadDefinition(childDefinition)
			if !exists {
				t.Fatalf("path %q: referenced definition %q is missing", template.CanonicalPath, childDefinition)
			}
			childElements = definition.Elements()
			semanticCoverageFillRequired(child, definition)
		}
		elements = childElements
		current = child
	}
	return root
}

func semanticCoverageSchemaElement(elements schema.ElementListView, name string) (schema.ElementView, bool) {
	for index := 0; index < elements.Len(); index++ {
		element, ok := elements.At(index)
		if ok && element.Name() == name {
			return element, true
		}
	}
	return schema.ElementView{}, false
}

func semanticCoverageFillRequired(object map[string]any, definition schema.DefinitionView) {
	for _, name := range definition.RequiredElements() {
		if _, exists := object[name]; exists {
			continue
		}
		element, ok := semanticCoverageSchemaElement(definition.Elements(), name)
		if !ok {
			continue
		}
		switch element.JSONType() {
		case schema.JSONTypeArray:
			object[name] = []any{}
		case schema.JSONTypeObject:
			continue
		default:
			object[name] = "witness"
		}
	}
}

func semanticCoveragePath(census SemanticCoverageCensus, kind SemanticCoverageOccurrenceKind, path string) (SemanticCoveragePathAggregate, bool) {
	for _, aggregate := range census.Paths {
		if aggregate.Kind == kind && aggregate.CanonicalPath == path {
			return aggregate, true
		}
	}
	return SemanticCoveragePathAggregate{}, false
}

func findMemberFactForCoverage(facts []fhirsemantic.MemberFact, canonicalPath string) (fhirsemantic.MemberFact, bool) {
	for _, fact := range facts {
		if fact.CanonicalPath == canonicalPath {
			return fact, true
		}
	}
	return fhirsemantic.MemberFact{}, false
}

func hasWalkDiagnostic(diagnostics []fhirsemantic.WalkDiagnostic, code fhirsemantic.WalkDiagnosticCode, canonicalPath string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code && diagnostic.CanonicalPath == canonicalPath {
			return true
		}
	}
	return false
}

func TestSemanticCoverageCensusRetainsMissingNullEmptyAndBlankEvidence(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	payload := json.RawMessage(`{
		"resourceType":"Observation",
		"status":"final",
		"code":null,
		"component":[{
			"code":{},
			"valueCodeableConcept":{"text":" ","coding":[]}
		},{
			"code":{"coding":[null,{}, {"system":"","code":" ","userSelected":false}]}
		}]
	}`)
	census, err := CensusSemanticCoverage(index, "Observation", "source-2", payload)
	if err != nil {
		t.Fatalf("census semantic coverage: %v", err)
	}
	if census.Counts.CodeableConceptOwners != 4 || census.Counts.PopulatedCodeableConceptValues != 2 ||
		census.Counts.NullCodeableConceptOwners != 1 || census.Counts.EmptyCodeableConceptOwners != 1 {
		t.Fatalf("CodeableConcept presence counts = %#v", census.Counts)
	}
	if census.Counts.CodingOccurrences != 3 || census.Counts.PopulatedCodingValues != 1 ||
		census.Counts.NullCodingOccurrences != 1 || census.Counts.EmptyCodingOccurrences != 1 {
		t.Fatalf("Coding presence counts = %#v", census.Counts)
	}
	componentCode := findSemanticCoveragePath(t, census, SemanticCoverageCodeableConcept, "component[].code")
	if componentCode.FacetPresenceCounts["component[].code.text"][SemanticCoverageMissing] != 2 ||
		componentCode.FacetPresenceCounts["component[].code.coding[]"][SemanticCoverageMissing] != 1 ||
		componentCode.FacetPresenceCounts["component[].code.coding[]"][SemanticCoveragePopulated] != 1 {
		t.Fatalf("component code facets = %#v", componentCode.FacetPresenceCounts)
	}
	componentValue := findSemanticCoveragePath(t, census, SemanticCoverageCodeableConcept, "component[].valueCodeableConcept")
	if componentValue.FacetPresenceCounts["component[].valueCodeableConcept.text"][SemanticCoverageBlank] != 1 ||
		componentValue.FacetPresenceCounts["component[].valueCodeableConcept.coding[]"][SemanticCoverageEmpty] != 1 {
		t.Fatalf("component value facets = %#v", componentValue.FacetPresenceCounts)
	}
	codingPath := findSemanticCoveragePath(t, census, SemanticCoverageCoding, "component[].code.coding[]")
	if codingPath.FacetPresenceCounts["component[].code.coding[].system"][SemanticCoverageMissing] != 1 ||
		codingPath.FacetPresenceCounts["component[].code.coding[].system"][SemanticCoverageBlank] != 1 ||
		codingPath.FacetPresenceCounts["component[].code.coding[].userSelected"][SemanticCoveragePopulated] != 1 {
		t.Fatalf("coding facets = %#v", codingPath.FacetPresenceCounts)
	}
	for _, value := range []json.RawMessage{json.RawMessage(`false`), json.RawMessage(`0`)} {
		presence, err := semanticCoveragePresence(value)
		if err != nil || presence != SemanticCoveragePopulated {
			t.Errorf("presence of %s = %q, %v; want populated", value, presence, err)
		}
	}
}

func findSemanticCoveragePath(t *testing.T, census SemanticCoverageCensus, kind SemanticCoverageOccurrenceKind, path string) SemanticCoveragePathAggregate {
	t.Helper()
	for _, aggregate := range census.Paths {
		if aggregate.Kind == kind && aggregate.CanonicalPath == path {
			return aggregate
		}
	}
	t.Fatalf("missing coverage path %s %q", kind, path)
	return SemanticCoveragePathAggregate{}
}

func TestSemanticCoverageCensusRejectsMalformedJSON(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	if _, err := CensusSemanticCoverage(index, "Observation", "source-3", []byte(`{"resourceType":"Observation"`)); err == nil {
		t.Fatal("malformed resource JSON was accepted")
	}
	if _, err := semanticCoveragePresence(json.RawMessage(`{`)); err == nil {
		t.Fatal("malformed occurrence JSON was classified as populated")
	}
}

func TestSemanticCoverageCensusDispatchesContainedResourceAndLegacyWalkerStaysAbstract(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	payload := json.RawMessage(`{
		"resourceType":"Observation",
		"status":"final",
		"code":{"text":"outer"},
		"contained":[{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"urn:test","code":"nested"}]}}]
	}`)
	census, err := CensusSemanticCoverage(index, "Observation", "contained-source", payload)
	if err != nil {
		t.Fatalf("census contained Resource union: %v", err)
	}
	if census.Counts.CodeableConceptOwners != 2 || census.Counts.CodingOccurrences != 1 {
		t.Fatalf("resource-aware census counts = %#v, want 2 CodeableConcept owners and 1 Coding", census.Counts)
	}
	if census.Status != SemanticCoverageCensusComplete || len(census.IncompleteGaps) != 0 {
		t.Fatalf("resolved contained Observation was incomplete: status=%q gaps=%#v", census.Status, census.IncompleteGaps)
	}
	containedCode := findSemanticCoveragePath(t, census, SemanticCoverageCodeableConcept, "contained[].code")
	if containedCode.ResourceBoundarySignature == "" || containedCode.OccurrenceCount != 1 ||
		!containsString(containedCode.OwnerPathWitnesses, "contained[0].code") {
		t.Fatalf("contained code aggregate lost its exact owner/boundary: %#v", containedCode)
	}

	var legacyFacts []fhirsemantic.MemberFact
	legacyDiagnostics, err := fhirsemantic.WalkMembers(index, "Observation", payload, func(fact fhirsemantic.MemberFact) error {
		legacyFacts = append(legacyFacts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("legacy WalkMembers: %v", err)
	}
	if _, found := findMemberFactForCoverage(legacyFacts, "contained[].code"); found {
		t.Fatalf("legacy emitter walker crossed abstract Resource boundary: %#v", legacyFacts)
	}
	if !hasWalkDiagnostic(legacyDiagnostics, fhirsemantic.WalkUnknownMember, "contained[].code") {
		t.Fatalf("legacy abstract Resource diagnostic missing: %#v", legacyDiagnostics)
	}

	definitions := index.Definitions()
	definitions = append(definitions,
		schema.Definition{Name: "Bundle", Elements: []schema.Element{{Name: "entry", JSONType: schema.JSONTypeArray, ArrayElementType: "CoverageBundleEntry"}}},
		schema.Definition{Name: "CoverageBundleEntry", Elements: []schema.Element{{Name: "resource", ReferencedType: "Resource"}}},
	)
	unionIndex, err := schema.NewIndex(definitions)
	if err != nil {
		t.Fatalf("build Bundle/Resource union fixture schema: %v", err)
	}
	var bundleFacts []fhirsemantic.MemberFact
	bundleDiagnostics, err := fhirsemantic.WalkMembers(unionIndex, "Bundle", json.RawMessage(`{
		"resourceType":"Bundle","type":"collection","entry":[{"resource":{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"urn:test","code":"nested"}]}}}]
	}`), func(fact fhirsemantic.MemberFact) error {
		bundleFacts = append(bundleFacts, fact)
		return nil
	})
	if err != nil {
		t.Fatalf("legacy Bundle fixture walk: %v", err)
	}
	if _, found := findMemberFactForCoverage(bundleFacts, "entry[].resource.code"); found ||
		!hasWalkDiagnostic(bundleDiagnostics, fhirsemantic.WalkUnknownMember, "entry[].resource.code") {
		t.Fatalf("synthetic Bundle fixture established unsupported union traversal: facts=%#v diagnostics=%#v", bundleFacts, bundleDiagnostics)
	}
}

func TestSemanticCoverageResourceBoundarySignatureKeepsRepeatedAndHeterogeneousOwnersDistinct(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	payload := json.RawMessage(`{
		"resourceType":"Observation",
		"status":"final",
		"contained":[
			{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"urn:same","version":"1","code":"same"}]}},
			{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"urn:same","version":"1","code":"same"}]}},
			{"resourceType":"Condition","code":{"coding":[{"system":"urn:same","version":"1","code":"same"}]},"subject":{}}
		]
	}`)
	census, err := CensusSemanticCoverage(index, "Observation", "repeated-contained-source", payload)
	if err != nil {
		t.Fatalf("census repeated contained resources: %v", err)
	}
	if census.Counts.CodeableConceptOwners != 3 || census.Counts.CodingOccurrences != 3 {
		t.Fatalf("same-code contained multiplicity was lost: %#v", census.Counts)
	}
	var observation, condition SemanticCoveragePathAggregate
	for _, path := range census.Paths {
		if path.Kind != SemanticCoverageCodeableConcept || path.CanonicalPath != "contained[].code" {
			continue
		}
		switch path.ResourceBoundarySignature {
		case semanticCoverageResourceBoundarySignature([]fhirsemantic.ResourceBoundary{{
			CanonicalPath: "contained[]", ResourceType: "Observation",
		}}):
			observation = path
		case semanticCoverageResourceBoundarySignature([]fhirsemantic.ResourceBoundary{{
			CanonicalPath: "contained[]", ResourceType: "Condition",
		}}):
			condition = path
		default:
			t.Errorf("unexpected boundary signature %q", path.ResourceBoundarySignature)
		}
	}
	if observation.ResourceBoundarySignature == "" || observation.OccurrenceCount != 2 || observation.OwnerCount != 2 ||
		!containsString(observation.OwnerPathWitnesses, "contained[0].code") || !containsString(observation.OwnerPathWitnesses, "contained[1].code") {
		t.Errorf("repeated Observation owners were not preserved: %#v", observation)
	}
	if condition.ResourceBoundarySignature == "" || condition.OccurrenceCount != 1 || condition.OwnerCount != 1 ||
		!containsString(condition.OwnerPathWitnesses, "contained[2].code") {
		t.Errorf("heterogeneous Condition owner was not separated: %#v", condition)
	}
	if observation.ResourceBoundarySignature == condition.ResourceBoundarySignature {
		t.Fatal("heterogeneous subtype boundaries shared an aggregate signature")
	}
	census.Identity.SchemaIdentity = "generated-test-schema"
	contribution, err := ReconcileSemanticCoverage(census, nil, nil)
	if err != nil {
		t.Fatalf("reconcile heterogeneous contained owners: %v", err)
	}
	if !contribution.Complete {
		t.Fatalf("fully enumerated unsupported selectors did not produce complete source accounting: %#v", contribution.IncompleteGaps)
	}
	var reconciledBoundarySignatures []string
	for _, path := range contribution.Paths {
		if path.Kind == SemanticCoverageCodeableConcept && path.CanonicalPath == "contained[].code" {
			reconciledBoundarySignatures = append(reconciledBoundarySignatures, path.ResourceBoundarySignature)
		}
	}
	if len(reconciledBoundarySignatures) != 2 || reconciledBoundarySignatures[0] == reconciledBoundarySignatures[1] {
		t.Fatalf("reconciled path groups lost subtype boundaries: %#v", reconciledBoundarySignatures)
	}
}

func TestSemanticCoverageResourceBoundarySignatureRetainsNestedChain(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	payload := json.RawMessage(`{
		"resourceType":"Observation",
		"status":"final",
		"contained":[{
			"resourceType":"Observation",
			"status":"final",
			"contained":[{
				"resourceType":"Condition",
				"clinicalStatus":{"text":"active"},
				"code":{"coding":[{"system":"urn:nested","code":"nested"}]},
				"subject":{}
			}]
		}]
	}`)
	census, err := CensusSemanticCoverage(index, "Observation", "nested-contained-source", payload)
	if err != nil {
		t.Fatalf("census nested contained resources: %v", err)
	}
	want := semanticCoverageResourceBoundarySignature([]fhirsemantic.ResourceBoundary{
		{CanonicalPath: "contained[]", ResourceType: "Observation"},
		{CanonicalPath: "contained[].contained[]", ResourceType: "Condition"},
	})
	var found *SemanticCoveragePathAggregate
	for index := range census.Paths {
		path := &census.Paths[index]
		if path.Kind == SemanticCoverageCodeableConcept && path.CanonicalPath == "contained[].contained[].code" {
			found = path
			break
		}
	}
	if found == nil {
		t.Fatal("nested CodeableConcept owner was not discovered")
	}
	if found.ResourceBoundarySignature != want || found.OccurrenceCount != 1 ||
		!containsString(found.OwnerPathWitnesses, "contained[0].contained[0].code") {
		t.Fatalf("nested boundary chain/signature was lost: got %#v, want signature %q", found, want)
	}
}

func TestSemanticCoverageUnresolvedResourceDiscriminatorsStayPartial(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	cases := []struct {
		name     string
		resource string
	}{
		{name: "missing", resource: `{"code":{"text":"not dispatched"}}`},
		{name: "nonstring", resource: `{"resourceType":7,"code":{"text":"not dispatched"}}`},
		{name: "unknown", resource: `{"resourceType":"MadeUp","code":{"text":"not dispatched"}}`},
		{name: "abstract", resource: `{"resourceType":"Resource","code":{"text":"not dispatched"}}`},
		{name: "datatype", resource: `{"resourceType":"Coding","code":{"text":"not dispatched"}}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			payload := json.RawMessage(fmt.Sprintf(`{"resourceType":"Observation","status":"final","contained":[%s]}`, test.resource))
			census, err := CensusSemanticCoverage(index, "Observation", "unresolved-resource", payload)
			if err != nil {
				t.Fatalf("census unresolved Resource: %v", err)
			}
			if census.Counts.CodeableConceptOwners != 0 || census.Counts.CodingOccurrences != 0 {
				t.Fatalf("unresolved subtype was dispatched: %#v", census.Counts)
			}
			if census.Status != SemanticCoverageCensusPartial || !containsCoverageGap(census.IncompleteGaps, SemanticCoverageGapUnknownSubtrees) {
				t.Fatalf("unresolved subtype did not retain a partial gap: status=%q gaps=%#v", census.Status, census.IncompleteGaps)
			}
			if !hasWalkDiagnostic(census.Diagnostics, fhirsemantic.WalkUnresolvedResourceType, "contained[]") {
				t.Fatalf("unresolved discriminator diagnostic missing: %#v", census.Diagnostics)
			}
		})
	}

	unknownPayload := json.RawMessage(`{"resourceType":"Observation","status":"final","mystery":{"resourceType":"Observation","code":{"text":"not dispatched"}}}`)
	unknownCensus, err := CensusSemanticCoverage(index, "Observation", "unknown-object", unknownPayload)
	if err != nil {
		t.Fatalf("census unknown object: %v", err)
	}
	if unknownCensus.Counts.CodeableConceptOwners != 0 || !hasWalkDiagnostic(unknownCensus.Diagnostics, fhirsemantic.WalkUnknownMember, "mystery") {
		t.Fatalf("unknown object was dispatched based on a discriminator: counts=%#v diagnostics=%#v", unknownCensus.Counts, unknownCensus.Diagnostics)
	}
}

func TestSemanticCoverageResourceBoundaryOccurrencesCannotBecomeSemanticOrRaw(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	payload := json.RawMessage(`{
		"resourceType":"Observation",
		"status":"final",
		"code":{"text":"outer"},
		"contained":[{"resourceType":"Observation","status":"final","code":{"coding":[{"system":"urn:test","version":"1","code":"same"}]}}]
	}`)
	census, err := CensusSemanticCoverage(index, "Observation", "contained-source", payload)
	if err != nil {
		t.Fatalf("census contained source: %v", err)
	}
	census.Identity.SchemaIdentity = "generated-test-schema"
	evidence := SemanticCoverageEmissionEvidence{
		Owner:       SemanticCoverageOwnerLocator{SourceID: "contained-source", ResourceType: "Observation", CanonicalPath: "contained[].code", OwnerPath: "contained[0].code"},
		KeyOwner:    SemanticCoverageOwnerLocator{SourceID: "contained-source", ResourceType: "Observation", CanonicalPath: "contained[].code.coding[]", OwnerPath: "contained[0].code.coding[0]"},
		KeySelector: "code.coding[].code", KeySystem: "urn:test", KeyVersion: "1", KeyCode: "same",
		SourceKind: "RESOURCE", SourcePath: "contained[].code.coding[]", RuleHint: "CODED_CONCEPT",
		ConceptID: "concept-same", BindingID: "binding-same", Status: "SUPPORTED",
		Roles: []SemanticCoverageRole{SemanticCoverageRoleConceptKey, SemanticCoverageRoleCodingMetadata},
	}
	contribution, err := ReconcileSemanticCoverage(census, []SemanticCoverageEmissionEvidence{evidence}, map[string]struct{}{
		"contained[].code":                       {},
		"contained[].code.coding[]":              {},
		"contained[].code.coding[].system":       {},
		"contained[].code.coding[].version":      {},
		"contained[].code.coding[].code":         {},
		"contained[].code.coding[].display":      {},
		"contained[].code.coding[].userSelected": {},
	})
	if err != nil {
		t.Fatalf("reconcile contained Resource source: %v", err)
	}
	if contribution.SourceRecordCount != 1 || contribution.Identity.ResourceType != "Observation" || !contribution.Complete {
		t.Fatalf("outer source identity/count or source completeness changed: %#v", contribution)
	}
	var containedConcept *SemanticCoveragePathAggregate
	for index := range contribution.Paths {
		path := &contribution.Paths[index]
		if path.CanonicalPath == "contained[].code" && path.Kind == SemanticCoverageCodeableConcept {
			containedConcept = path
			break
		}
	}
	if containedConcept == nil {
		t.Fatal("contained CodeableConcept aggregate missing")
	}
	if containedConcept.Disposition != SemanticCoverageUnsupportedDisposition ||
		containedConcept.Reason != SemanticCoverageReasonUnsupportedSelector || len(containedConcept.References) != 0 ||
		containedConcept.ResourceBoundarySignature == "" {
		t.Fatalf("crossed boundary acquired semantic/raw proof: %#v", containedConcept)
	}
	for _, facet := range containedConcept.Facets {
		if facet.ResourceBoundarySignature != containedConcept.ResourceBoundarySignature ||
			facet.Disposition != SemanticCoverageUnsupportedDisposition || facet.Reason != SemanticCoverageReasonUnsupportedSelector || len(facet.References) != 0 {
			t.Errorf("crossed boundary facet acquired proof: %#v", facet)
		}
	}
}

func TestSemanticCoverageWalkerLimitRetainsPartialCounts(t *testing.T) {
	index, err := schema.GeneratedIndex()
	if err != nil {
		t.Fatalf("load generated FHIR schema: %v", err)
	}
	var payload strings.Builder
	payload.WriteString(`{"resourceType":"Observation","status":"final","component":[`)
	for component := 0; component < 40000; component++ {
		if component != 0 {
			payload.WriteByte(',')
		}
		payload.WriteString(`{"code":{"coding":[{"system":"urn:test","code":"x"}]}}`)
	}
	payload.WriteString(`]}`)
	census, err := CensusSemanticCoverage(index, "Observation", "walker-limit-source", []byte(payload.String()))
	if err != nil {
		t.Fatalf("walker processing limit escaped as a census failure: %v", err)
	}
	if census.Status != SemanticCoverageCensusPartial || !containsCoverageGap(census.IncompleteGaps, SemanticCoverageGapWalkerLimit) {
		t.Fatalf("walker limit was not represented as partial coverage: status=%q gaps=%#v", census.Status, census.IncompleteGaps)
	}
	if census.Counts.CodeableConceptOwners == 0 || census.Counts.CodingOccurrences == 0 || len(census.Paths) == 0 {
		t.Fatalf("walker limit discarded already enumerated counts: counts=%#v paths=%d", census.Counts, len(census.Paths))
	}
	census.Identity.SchemaIdentity = "generated-test-schema"
	contribution, err := ReconcileSemanticCoverage(census, nil, nil)
	if err != nil {
		t.Fatalf("reconcile partial walker census: %v", err)
	}
	if contribution.Complete || !containsCoverageGap(contribution.IncompleteGaps, SemanticCoverageGapWalkerLimit) ||
		contribution.Counts.CodeableConceptOwners != census.Counts.CodeableConceptOwners {
		t.Fatalf("walker limit was not preserved as incomplete source evidence: %#v", contribution)
	}
}

func TestSemanticCoverageEmissionEvidencePreservesSameCodeRepeatedOwners(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Observation",
		"status":       "final",
		"component": []any{
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:loinc", "version": "2.77", "code": "8302-2"}}},
				"valueQuantity": map[string]any{"value": 170, "unit": "cm", "system": "http://unitsofmeasure.org", "code": "cm"},
			},
			map[string]any{
				"code":          map[string]any{"coding": []any{map[string]any{"system": "urn:loinc", "version": "2.77", "code": "8302-2"}}},
				"valueQuantity": map[string]any{"value": 5.5, "unit": "g", "system": "http://unitsofmeasure.org", "code": "g"},
			},
		},
	}
	var semantic []SemanticInventoryContribution
	var emissions []SemanticCoverageEmissionEvidence
	var coverage []SemanticCoverageSourceContribution
	emitter := NewSemanticInventoryEmitter("coverage-test", "generation-1")
	if err := emitter.ObservePayloadWithCoverage(
		payload,
		"Observation",
		"scope-a",
		"retained:Observation/repeated-owner",
		"schema-sha256-test",
		func(contribution SemanticInventoryContribution) { semantic = append(semantic, contribution) },
		func(evidence SemanticCoverageEmissionEvidence) { emissions = append(emissions, evidence) },
		func(contribution SemanticCoverageSourceContribution) error {
			coverage = append(coverage, contribution)
			return nil
		},
	); err != nil {
		t.Fatalf("observe payload with coverage: %v", err)
	}
	if len(semantic) != 2 || len(emissions) != 2 {
		t.Fatalf("semantic observations/evidence = %d/%d, want two repeated owners", len(semantic), len(emissions))
	}
	if len(coverage) != 1 || coverage[0].SourceRecordCount != 1 {
		t.Fatalf("source coverage contributions = %#v, want one source-level aggregate", coverage)
	}
	if !coverage[0].Complete || coverage[0].Identity.SchemaIdentity != "schema-sha256-test" {
		t.Fatalf("coverage completeness/schema identity = %v/%q, want complete with exact source schema identity", coverage[0].Complete, coverage[0].Identity.SchemaIdentity)
	}
	wantOwners := []string{"component[0]", "component[1]"}
	wantKeys := []string{"component[0].code.coding[0]", "component[1].code.coding[0]"}
	wantValues := []string{"component[0].valueQuantity", "component[1].valueQuantity"}
	wantExamples := []string{"170", "5.5"}
	wantUnits := []string{"cm", "g"}
	for index, evidence := range emissions {
		if evidence.Owner.OwnerPath != wantOwners[index] || evidence.ConceptOwner.OwnerPath != wantOwners[index]+".code" || evidence.KeyOwner.OwnerPath != wantKeys[index] || evidence.ValueOwner.OwnerPath != wantValues[index] {
			t.Errorf("evidence[%d] owner locators = %#v / %#v / %#v / %#v", index, evidence.Owner, evidence.ConceptOwner, evidence.KeyOwner, evidence.ValueOwner)
		}
		if evidence.KeySystem != "urn:loinc" || evidence.KeyVersion != "2.77" || evidence.KeyCode != "8302-2" {
			t.Errorf("evidence[%d] key identity = %q|%q|%q", index, evidence.KeySystem, evidence.KeyVersion, evidence.KeyCode)
		}
		if evidence.Status != "SUPPORTED" || evidence.KeySelector != "code.coding[]" || evidence.ValueSelector != "valueQuantity.value" || len(evidence.Roles) == 0 {
			t.Errorf("evidence[%d] selectors/status/roles = %q %q %q %#v", index, evidence.Status, evidence.KeySelector, evidence.ValueSelector, evidence.Roles)
		}
		if semantic[index].ConceptID != semantic[0].ConceptID || semantic[index].BindingID != semantic[0].BindingID {
			t.Errorf("same-code emissions acquired different semantic identities: %#v", semantic)
		}
		if semantic[index].Example != wantExamples[index] || len(semantic[index].Observation.ObservedUnits) == 0 || semantic[index].Observation.ObservedUnits[0] != wantUnits[index] {
			t.Errorf("semantic[%d] example/unit = %q/%#v", index, semantic[index].Example, semantic[index].Observation.ObservedUnits)
		}
	}
	if semantic[0].Example == semantic[1].Example {
		t.Fatal("same-code repeated owners lost their distinct value examples")
	}
}

func TestSemanticCoverageCollapsesSupportedCodedConceptsByExactFamilyScope(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Observation",
		"status":       "final",
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:test", "version": "v1", "code": "one"},
			map[string]any{"system": "urn:test", "version": "v1", "code": "two"},
			map[string]any{"system": "urn:test", "version": "v2", "code": "three"},
		}},
	}
	var contribution SemanticCoverageSourceContribution
	emitter := NewSemanticInventoryEmitter("coverage-test", "generation-1")
	if err := emitter.ObservePayloadWithCoverage(
		payload, "Observation", "scope-a", "retained:Observation/family", "schema-sha256-test", nil, nil,
		func(value SemanticCoverageSourceContribution) error { contribution = value; return nil },
	); err != nil {
		t.Fatalf("observe coded concept coverage: %v", err)
	}
	var codingPath *SemanticCoveragePathAggregate
	for index := range contribution.Paths {
		path := &contribution.Paths[index]
		if path.CanonicalPath == "code.coding[]" && path.Kind == SemanticCoverageCoding {
			codingPath = path
			break
		}
	}
	if codingPath == nil {
		t.Fatalf("coverage omitted generated Coding path: %#v", contribution.Paths)
	}
	if codingPath.Disposition != SemanticCoverageSemanticDisposition || len(codingPath.References) != 2 {
		t.Fatalf("Coding path disposition/references = %q/%#v, want semantic with one family per version", codingPath.Disposition, codingPath.References)
	}
	conceptPath := findContributionCoveragePath(t, contribution, SemanticCoverageCodeableConcept, "code")
	var codingFacet *SemanticCoverageFacetAggregate
	for index := range conceptPath.Facets {
		if conceptPath.Facets[index].Path == "code.coding[]" {
			codingFacet = &conceptPath.Facets[index]
			break
		}
	}
	if codingFacet == nil || codingFacet.Disposition != SemanticCoverageSemanticDisposition || len(codingFacet.References) != 2 {
		t.Fatalf("CodeableConcept coding facet disposition/references = %#v, want semantic with one family per version", codingFacet)
	}
	families := make(map[string]SemanticInventoryFamilyReference)
	for _, reference := range codingPath.References {
		if reference.Kind != SemanticCoverageFamilyReference || reference.ConceptID != "" {
			t.Errorf("supported CODED_CONCEPT was not collapsed to a family reference: %#v", reference)
		}
		families[reference.Family.Version] = reference.Family
	}
	for _, version := range []string{"v1", "v2"} {
		family, ok := families[version]
		if !ok {
			t.Errorf("family reference for exact version %q is missing: %#v", version, families)
			continue
		}
		if family.FamilyID == "" || family.BindingID == "" || family.SourceKind != SemanticInventorySourceRetained || family.ResourceType != "Observation" || family.SourcePath != "code.coding[]" || family.System != "urn:test" {
			t.Errorf("family identity lost exact source/binding/system/version scope: %#v", family)
		}
	}
	versionOne := families["v1"]
	otherVersion := versionOne
	otherVersion.Version = "v2"
	if SemanticInventoryFamilyID(versionOne) == SemanticInventoryFamilyID(otherVersion) {
		t.Fatal("family identity merged distinct versions")
	}
	otherBinding := versionOne
	otherBinding.BindingID = "different-binding"
	if SemanticInventoryFamilyID(versionOne) == SemanticInventoryFamilyID(otherBinding) {
		t.Fatal("family identity merged distinct bindings")
	}
}

func TestSemanticCoverageDirectCodingChoiceLeafFacetsUseExactOwnerEvidence(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Task",
		"status":       "completed",
		"intent":       "order",
		"input": []any{map[string]any{
			"type":        map[string]any{"text": "result"},
			"valueCoding": map[string]any{"system": "urn:test", "version": "v1", "code": "choice-code"},
		}},
	}
	var emissions []SemanticCoverageEmissionEvidence
	var contribution SemanticCoverageSourceContribution
	emitter := NewSemanticInventoryEmitter("coverage-test", "generation-1")
	if err := emitter.ObservePayloadWithCoverage(
		payload, "Task", "scope-a", "retained:Task/direct-coding", "schema-sha256-test", nil,
		func(evidence SemanticCoverageEmissionEvidence) { emissions = append(emissions, evidence) },
		func(value SemanticCoverageSourceContribution) error { contribution = value; return nil },
	); err != nil {
		t.Fatalf("observe direct Coding choice coverage: %v", err)
	}
	path := findContributionCoveragePath(t, contribution, SemanticCoverageCoding, "input[].valueCoding")
	if path.Disposition != SemanticCoverageSemanticDisposition || len(path.References) != 1 || path.References[0].Kind != SemanticCoverageFamilyReference {
		t.Fatalf("direct Coding path disposition/references = %q/%#v", path.Disposition, path.References)
	}
	wantOwner := "input[0].valueCoding"
	matchedOwner := false
	for _, evidence := range emissions {
		if evidence.Owner.OwnerPath == wantOwner && evidence.KeyOwner.OwnerPath == wantOwner && evidence.KeySelector == "valueCoding" && evidence.Status == "SUPPORTED" {
			matchedOwner = true
		}
	}
	if !matchedOwner {
		t.Fatalf("direct Coding emission omitted exact owner/key selector %q: %#v", wantOwner, emissions)
	}
	for _, leaf := range []string{"input[].valueCoding.system", "input[].valueCoding.code"} {
		found := false
		for _, facet := range path.Facets {
			if facet.Path == leaf && facet.Disposition == SemanticCoverageSemanticDisposition && len(facet.References) == 1 {
				found = true
			}
		}
		if !found {
			t.Errorf("direct Coding leaf facet %q lacks owner-matched semantic reference: %#v", leaf, path.Facets)
		}
	}
}

func findContributionCoveragePath(t *testing.T, contribution SemanticCoverageSourceContribution, kind SemanticCoverageOccurrenceKind, path string) SemanticCoveragePathAggregate {
	t.Helper()
	for _, aggregate := range contribution.Paths {
		if aggregate.Kind == kind && aggregate.CanonicalPath == path {
			return aggregate
		}
	}
	t.Fatalf("missing contribution coverage path %s %q", kind, path)
	return SemanticCoveragePathAggregate{}
}

func TestSemanticCoverageEmitsSourceSentinelWithoutSemanticOccurrences(t *testing.T) {
	var contribution SemanticCoverageSourceContribution
	emitter := NewSemanticInventoryEmitter("coverage-test", "generation-1")
	if err := emitter.ObservePayloadWithCoverage(
		map[string]any{"resourceType": "Patient", "id": "patient-1"},
		"Patient", "scope-a", "retained:Patient/1", "schema-sha256-test", nil, nil,
		func(value SemanticCoverageSourceContribution) error { contribution = value; return nil },
	); err != nil {
		t.Fatalf("observe zero-occurrence source: %v", err)
	}
	if contribution.SourceRecordCount != 1 || len(contribution.Paths) != 0 || contribution.Identity.SourceID != "retained:Patient/1" {
		t.Fatalf("zero-occurrence source sentinel = %#v", contribution)
	}
}

func TestSemanticCoverageKeepsUnrepresentedCodingMetadataOutOfSemanticDisposition(t *testing.T) {
	var contribution SemanticCoverageSourceContribution
	emitter := NewSemanticInventoryEmitter("coverage-test", "generation-1")
	if err := emitter.ObservePayloadWithCoverage(
		map[string]any{
			"resourceType": "Observation",
			"status":       "final",
			"code": map[string]any{"coding": []any{map[string]any{
				"system": "urn:test", "version": "v1", "code": "one", "userSelected": true,
			}}},
		},
		"Observation", "scope-a", "retained:Observation/metadata", "schema-sha256-test", nil, nil,
		func(value SemanticCoverageSourceContribution) error { contribution = value; return nil },
	); err != nil {
		t.Fatalf("observe coding metadata coverage: %v", err)
	}
	for _, path := range contribution.Paths {
		if path.CanonicalPath != "code.coding[]" || path.Kind != SemanticCoverageCoding {
			continue
		}
		if path.Disposition != SemanticCoverageRawRetainedDisposition || path.Reason != SemanticCoverageReasonUnrepresentedMetadata {
			t.Fatalf("Coding with unrepresented metadata disposition/reason = %q/%q", path.Disposition, path.Reason)
		}
		coveredCodingLeaves := 0
		for _, facet := range path.Facets {
			switch facet.Kind {
			case "system", "code":
				if facet.Disposition != SemanticCoverageSemanticDisposition || len(facet.References) != 1 || facet.References[0].Kind != SemanticCoverageFamilyReference {
					t.Errorf("covered %s facet disposition/references = %q/%#v", facet.Kind, facet.Disposition, facet.References)
				}
				coveredCodingLeaves++
			case "userSelected":
				if facet.Disposition != SemanticCoverageRawRetainedDisposition || facet.Reason != SemanticCoverageReasonUnrepresentedMetadata || len(facet.References) != 0 {
					t.Fatalf("userSelected facet disposition/reason/references = %q/%q/%#v", facet.Disposition, facet.Reason, facet.References)
				}
			}
		}
		if coveredCodingLeaves != 2 {
			t.Fatalf("semantic leaf facets with refs = %d, want 2", coveredCodingLeaves)
		}
		return
	}
	t.Fatalf("coverage omitted Coding path: %#v", contribution.Paths)
}

func TestSemanticCoverageTruncationAndMissingSchemaIdentityStayIncomplete(t *testing.T) {
	census := SemanticCoverageCensus{
		ResourceType:      "Observation",
		SourceID:          "source-truncated",
		Status:            SemanticCoverageCensusComplete,
		EvidenceTruncated: true,
		Identity: SemanticCoverageSourceIdentity{
			Project: "project", DatasetGeneration: "generation", SchemaIdentity: "schema-sha256-test",
		},
		Counts: SemanticCoverageCounts{CodeableConceptOwners: 1, PopulatedCodeableConceptValues: 1},
		Paths: []SemanticCoveragePathAggregate{{
			CanonicalPath: "code", Kind: SemanticCoverageCodeableConcept, OccurrenceCount: 1, OwnerCount: 1,
			PresenceCounts: map[SemanticCoveragePresence]int64{SemanticCoveragePopulated: 1},
			FacetPresenceCounts: map[string]map[SemanticCoveragePresence]int64{
				"code.text": {SemanticCoverageMissing: 1},
			},
		}},
	}
	truncated, err := ReconcileSemanticCoverage(census, nil, map[string]struct{}{"code": {}})
	if err != nil {
		t.Fatalf("reconcile truncated coverage: %v", err)
	}
	if truncated.Complete || !truncated.ReconciliationTruncated || !truncated.ReferencesTruncated ||
		!hasSemanticCoverageGap(truncated.IncompleteGaps, SemanticCoverageGapEmissionEvidence) {
		t.Fatalf("truncated source was not kept incomplete: %#v", truncated)
	}
	if len(truncated.Paths) != 1 || truncated.Paths[0].Disposition != SemanticCoverageRawRetainedDisposition || truncated.Paths[0].Reason != SemanticCoverageReasonTruncatedEvidence {
		t.Fatalf("truncated path disposition/reason = %#v", truncated.Paths)
	}

	emptySchema := SemanticCoverageCensus{ResourceType: "Patient", SourceID: "source-empty-schema", Status: SemanticCoverageCensusComplete}
	emptySchemaContribution, err := ReconcileSemanticCoverage(emptySchema, nil, nil)
	if err != nil {
		t.Fatalf("reconcile missing schema identity: %v", err)
	}
	if emptySchemaContribution.Complete || emptySchemaContribution.SourceRecordCount != 1 ||
		!hasSemanticCoverageGap(emptySchemaContribution.IncompleteGaps, SemanticCoverageGapSchemaIdentity) {
		t.Fatalf("missing schema identity did not retain an incomplete source sentinel: %#v", emptySchemaContribution)
	}
}

func TestSemanticCoverageClassifiesIdentifierExtensionAndPrimitiveCode(t *testing.T) {
	payload := map[string]any{
		"resourceType": "Observation",
		"status":       "final",
		"identifier": []any{map[string]any{
			"system": "urn:mrn", "value": "identifier-value",
		}},
		"extension": []any{map[string]any{
			"url": "urn:test:note", "valueString": "note-value",
		}},
	}
	var contribution SemanticCoverageSourceContribution
	emitter := NewSemanticInventoryEmitter("coverage-test", "generation-1")
	if err := emitter.ObservePayloadWithCoverage(
		payload, "Observation", "scope-a", "retained:Observation/known-kinds", "schema-sha256-test", nil, nil,
		func(value SemanticCoverageSourceContribution) error { contribution = value; return nil },
	); err != nil {
		t.Fatalf("observe known coverage kinds: %v", err)
	}
	if contribution.Counts.IdentifierOccurrences != 1 || contribution.Counts.ExtensionValueOccurrences != 1 || contribution.Counts.PrimitiveCodeOccurrences == 0 {
		t.Fatalf("known occurrence-kind counts = %#v", contribution.Counts)
	}
	if contribution.Counts.UnknownStructureOccurrences != 0 || len(contribution.IncompleteGaps) != 0 || !contribution.Complete {
		t.Fatalf("known kinds did not produce a reconciled complete source: complete=%v gaps=%#v counts=%#v", contribution.Complete, contribution.IncompleteGaps, contribution.Counts)
	}
	var identifierSemantic, extensionSemantic, primitiveRaw bool
	for _, path := range contribution.Paths {
		switch path.Kind {
		case SemanticCoverageIdentifier:
			identifierSemantic = path.Disposition == SemanticCoverageSemanticDisposition && len(path.References) == 1
		case SemanticCoverageExtensionValue:
			extensionSemantic = path.Disposition == SemanticCoverageSemanticDisposition && len(path.References) == 1
		case SemanticCoveragePrimitiveCode:
			if path.CanonicalPath == "status" {
				primitiveRaw = path.Disposition == SemanticCoverageRawRetainedDisposition && path.Reason == SemanticCoverageReasonUnknownCodeClassification
			}
		}
	}
	if !identifierSemantic || !extensionSemantic || !primitiveRaw {
		t.Fatalf("known-kind dispositions identifier/extension/primitive = %v/%v/%v; paths=%#v", identifierSemantic, extensionSemantic, primitiveRaw, contribution.Paths)
	}
	report, err := json.Marshal(contribution)
	if err != nil {
		t.Fatalf("marshal default coverage report: %v", err)
	}
	for _, sourceValue := range []string{"identifier-value", "note-value"} {
		if strings.Contains(string(report), sourceValue) {
			t.Errorf("default coverage report leaked source value %q", sourceValue)
		}
	}
}

func TestSemanticCoverageRuleVersionChangesOnlyInventoryBuildIdentity(t *testing.T) {
	project, generation := "coverage-version-test", "generation-1"
	currentBuildID := SemanticInventoryBuildID(project, generation)
	nextCoverageBuildID := semanticInventoryBuildIDWithCoverageRuleVersion(project, generation, SemanticCoverageRuleVersion+1)
	if currentBuildID == nextCoverageBuildID {
		t.Fatal("coverage-rule version change did not invalidate the inventory build")
	}
	if currentBuildID != semanticInventoryBuildIDWithCoverageRuleVersion(project, generation, SemanticCoverageRuleVersion) {
		t.Fatal("public inventory build ID does not use the declared coverage-rule version")
	}
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Source:        SemanticObservationSource{Type: "Observation", Path: "component[]"},
		Key:           SemanticObservationKey{Selector: "code.coding[]", System: "urn:test", Version: "v1", Code: "same"},
		Value:         SemanticObservationValue{Selector: "valueQuantity.value", Type: "decimal"},
		RuleVersion:   "5",
	}
	profiler := NewProfilerForGenerationWithLimits(project, generation, "", "Observation", nil, ProfileLimits{})
	contribution := profiler.semanticInventoryContribution("source-1", 0, observation, nil)
	if contribution.ConceptID != "9440619cb450e82e32e6ba4a090ed1820b18596b9b2587e55601108442c41091" {
		t.Fatalf("coverage-only update changed stable concept identity: %s", contribution.ConceptID)
	}
	if contribution.BindingID != "f023b734da3591fdc95dd4cb380e85db8f53c8aa3c3f9ce6109f36deda91a710" {
		t.Fatalf("coverage-only update changed stable binding identity: %s", contribution.BindingID)
	}
	if contribution.BuildID != currentBuildID {
		t.Fatalf("semantic contribution build ID = %q, want current coverage build %q", contribution.BuildID, currentBuildID)
	}
	if SemanticObservationRuleVersion != 5 || SemanticObservationSchemaVersion != 4 {
		t.Fatalf("coverage-only update changed observation versions: rule=%d schema=%d", SemanticObservationRuleVersion, SemanticObservationSchemaVersion)
	}
}
