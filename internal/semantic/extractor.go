package semantic

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/fhir/schema"
)

type semanticScope struct {
	canonicalPath string
	ownerPath     string
	definition    schema.DefinitionName
	value         map[string]any
	facts         []*MemberFact
}

const (
	maxRetainedSemanticFacts  = 1024
	maxRetainedSemanticScopes = 256
	maxRetainedFactsPerScope  = 64
)

type semanticWalkScratch struct {
	inUse   bool
	facts   []MemberFact
	scopes  []semanticScope
	byOwner map[string]int
}

type semanticFactEnumerator struct {
	resourceType string
	scratch      *semanticWalkScratch
}

type semanticObservationRun struct {
	resourceType string
	profile      string
	scratch      *semanticWalkScratch
	record       func(Record)
}

type Record struct {
	StoragePath      string
	ScopeOwnerPath   string
	ConceptOwnerPath string
	KeyOwnerPath     string
	ValueOwnerPath   string
	Observation      Observation
	Examples         []any
}

type Extractor struct {
	scratch semanticWalkScratch
}

func (e *Extractor) Extract(index *schema.Index, resourceType string, payload map[string]any, profile string, record func(Record)) error {
	scratch := &e.scratch
	if scratch.inUse {
		scratch = &semanticWalkScratch{}
	}
	scratch.inUse = true
	defer func() {
		scratch.inUse = false
		if cap(scratch.facts) > maxRetainedSemanticFacts {
			scratch.facts = nil
		} else {
			clear(scratch.facts)
			scratch.facts = scratch.facts[:0]
		}
	}()
	enumerator := semanticFactEnumerator{resourceType: resourceType, scratch: scratch}
	facts, err := enumerator.collectFacts(index, payload)
	if err != nil {
		return err
	}
	run := semanticObservationRun{resourceType: resourceType, profile: profile, scratch: scratch, record: record}
	return run.observeSchemaSemanticsFromFacts(payload, facts)
}

func (enumerator semanticFactEnumerator) collectFacts(
	index *schema.Index,
	payload map[string]any,
) ([]MemberFact, error) {
	if _, err := WalkMemberValues(index, schema.DefinitionName(enumerator.resourceType), payload, func(fact MemberFact) error {
		enumerator.scratch.facts = append(enumerator.scratch.facts, fact)
		return nil
	}); err != nil {
		return nil, err
	}
	return enumerator.scratch.facts, nil
}

type semanticDatatypeAssociation struct {
	keyMember   string
	valueMember string
	ruleHint    string
}

type semanticValueProjectionAssociation struct {
	selector              string
	fallbackSelectors     []string
	fallbackOnBlankString bool
	logicalType           string
}

var semanticDatatypeAssociations = map[schema.DefinitionName]semanticDatatypeAssociation{
	"Identifier": {
		keyMember:   "system",
		valueMember: "value",
		ruleHint:    "IDENTIFIER_SYSTEM_VALUE",
	},
	"Extension": {
		keyMember: "url",
		ruleHint:  "EXTENSION_URL_VALUE",
	},
}

var semanticValueProjectionAssociations = map[schema.DefinitionName]semanticValueProjectionAssociation{
	"Quantity": {selector: "value", logicalType: "decimal"},
	"CodeableConcept": {
		selector: "text", fallbackSelectors: []string{"coding[].display"},
		fallbackOnBlankString: true, logicalType: "string",
	},
	"Period": {selector: "start", logicalType: "date_time"},
	"Range":  {selector: "low.value", logicalType: "decimal"},
	"Ratio":  {selector: "numerator.value", logicalType: "decimal"},
}

func (run *semanticObservationRun) observeSchemaSemanticsFromFacts(
	payload map[string]any,
	facts []MemberFact,
) error {
	scratch := run.scratch
	if scratch.byOwner == nil {
		scratch.byOwner = make(map[string]int)
	}
	scratch.addScope(semanticScope{definition: schema.DefinitionName(run.resourceType), value: payload})
	defer scratch.resetScopes()

	for i := range facts {
		fact := &facts[i]
		if fact.Repeated && !fact.ArrayItem {
			continue
		}
		value, ok := fact.Value.(map[string]any)
		if !ok {
			continue
		}
		definition := fact.ReferencedType
		if definition == "" {
			definition = fact.DeclaringType
		}
		scratch.addScope(semanticScope{
			canonicalPath: fact.CanonicalPath,
			ownerPath:     fact.OwnerPath,
			definition:    definition,
			value:         value,
		})
	}
	scopes := scratch.scopes
	for i := range facts {
		fact := &facts[i]
		if fact.ArrayItem {
			continue
		}
		if index, ok := scratch.byOwner[directOwnerPath(fact.OwnerPath)]; ok {
			scope := &scopes[index]
			scope.facts = append(scope.facts, fact)
		}
	}

	extensionURLs := make(map[string]string)
	for i := range scopes {
		scope := &scopes[i]
		if scope.definition != "Extension" {
			continue
		}
		if url, ok := directSemanticFact(scope, "url"); ok {
			extensionURLs[scope.ownerPath] = stringValue(url.Value)
		}
	}

	pairedCodeableConcepts := make(map[string]struct{})
	identifierOrExtensionOwners := make([]string, 0)
	for i := range scopes {
		scope := &scopes[i]
		if scope.definition == "Identifier" || scope.definition == "Extension" {
			identifierOrExtensionOwners = append(identifierOrExtensionOwners, scope.ownerPath)
		}
	}
	for i := range scopes {
		scope := &scopes[i]
		association, hasAssociation := semanticDatatypeAssociations[scope.definition]
		switch {
		case hasAssociation && scope.definition == "Identifier":
			run.emitIdentifierScope(scope, association)
		case hasAssociation && scope.definition == "Extension":
			run.emitExtensionScope(scope, association, extensionURLs)
		default:
			run.emitCodedChoiceScope(scope, func(observation Observation) {
				if observation.RuleHint == RuleHintCodedValueV1 {
					if strings.HasSuffix(observation.Key.Selector, ".coding[]") {
						conceptPath := strings.TrimSuffix(observation.Key.Selector, ".coding[]")
						pairedCodeableConcepts[appendSemanticPath(scope.ownerPath, conceptPath)] = struct{}{}
					}
					if observation.ChoiceArm == "valueCodeableConcept" && strings.HasSuffix(observation.Value.Selector, ".text") {
						conceptPath := strings.TrimSuffix(observation.Value.Selector, ".text")
						pairedCodeableConcepts[appendSemanticPath(scope.ownerPath, conceptPath)] = struct{}{}
					}
				}
			})
		}
	}
	for i := range scopes {
		scope := &scopes[i]
		if scope.definition != "Coding" {
			continue
		}
		if semanticScopeIsBelowAny(scope.ownerPath, identifierOrExtensionOwners) {
			continue
		}
		if conceptPath := semanticCodingConceptPath(scope); conceptPath != "" {
			if _, paired := pairedCodeableConcepts[conceptPath]; paired {
				continue
			}
		}
		run.emitStandaloneCodingScope(scope)
	}
	return nil
}

func (scratch *semanticWalkScratch) addScope(scope semanticScope) {
	index := len(scratch.scopes)
	if index < cap(scratch.scopes) {
		scratch.scopes = scratch.scopes[:index+1]
		scope.facts = scratch.scopes[index].facts
		scratch.scopes[index] = scope
	} else {
		scratch.scopes = append(scratch.scopes, scope)
	}
	scratch.byOwner[scope.ownerPath] = index
}

func (scratch *semanticWalkScratch) resetScopes() {
	if cap(scratch.scopes) > maxRetainedSemanticScopes {
		scratch.scopes = nil
		scratch.byOwner = nil
		return
	}
	for i := range scratch.scopes {
		facts := scratch.scopes[i].facts
		scratch.scopes[i] = semanticScope{}
		if cap(facts) <= maxRetainedFactsPerScope {
			clear(facts)
			scratch.scopes[i].facts = facts[:0]
		}
	}
	scratch.scopes = scratch.scopes[:0]
	clear(scratch.byOwner)
}

func (run *semanticObservationRun) emitStandaloneCodingScope(scope *semanticScope) {
	system := stringValue(scope.value["system"])
	code := stringValue(scope.value["code"])
	if strings.TrimSpace(system) == "" || strings.TrimSpace(code) == "" {
		return
	}

	ownerPath := semanticNearestRepeatedOwner(scope.canonicalPath)
	keyPath := relativeSemanticPath(ownerPath, scope.canonicalPath)
	if keyPath == scope.canonicalPath && ownerPath != "" {
		return
	}
	valuePath := appendSemanticPath(keyPath, "display")
	fallbackPath := appendSemanticPath(keyPath, "code")
	display := stringValue(scope.value["display"])
	example := display
	if strings.TrimSpace(example) == "" {
		example = code
	}
	observation := Observation{
		SchemaVersion: ObservationSchemaVersion,
		Source: Source{
			Canonical: semanticCanonical(run.resourceType, scope.canonicalPath),
			Type:      run.resourceType,
			Profile:   run.profile,
			Path:      scope.canonicalPath,
		},
		Key: Key{
			Selector: keyPath,
			System:   system,
			Version:  stringValue(scope.value["version"]),
			Code:     code,
			Display:  display,
		},
		Value: Value{
			Selector:              valuePath,
			Type:                  "string",
			FallbackSelectors:     []string{fallbackPath},
			FallbackOnBlankString: true,
		},
		OwningScope:  ownerPath,
		LogicalType:  "string",
		Completeness: CompletenessComplete,
		Status:       "SUPPORTED",
		RuleHint:     RuleHintCodedConceptV1,
		RuleVersion:  strconv.Itoa(RuleVersion),
	}
	run.record(Record{
		StoragePath: scope.canonicalPath, ScopeOwnerPath: scope.ownerPath,
		ConceptOwnerPath: scope.ownerPath, KeyOwnerPath: scope.ownerPath, ValueOwnerPath: scope.ownerPath,
		Observation: observation, Examples: []any{example},
	})
}

func semanticScopeIsBelowAny(path string, ancestors []string) bool {
	for _, ancestor := range ancestors {
		if ancestor != "" && (path == ancestor || strings.HasPrefix(path, ancestor+".")) {
			return true
		}
	}
	return false
}

func semanticCodingConceptPath(scope *semanticScope) string {
	if scope == nil || !strings.HasSuffix(scope.canonicalPath, ".coding[]") {
		return ""
	}
	itemStart := strings.LastIndex(scope.ownerPath, "[")
	if itemStart < 0 || !strings.HasSuffix(scope.ownerPath, "]") {
		return ""
	}
	arrayPath := strings.TrimSuffix(scope.ownerPath, scope.ownerPath[itemStart:])
	return strings.TrimSuffix(arrayPath, ".coding")
}

func semanticNearestRepeatedOwner(path string) string {
	lastBoundary := strings.LastIndex(path, "[]")
	if lastBoundary < 0 {
		return ""
	}
	if !strings.HasSuffix(path, "[]") {
		return path[:lastBoundary+2]
	}
	previousBoundary := strings.LastIndex(path[:lastBoundary], "[]")
	if previousBoundary < 0 {
		return ""
	}
	return path[:previousBoundary+2]
}

func (run *semanticObservationRun) emitIdentifierScope(
	scope *semanticScope,
	association semanticDatatypeAssociation,
) {
	valueFact, ok := directSemanticFact(scope, association.valueMember)
	if !ok || valueFact.Null {
		return
	}
	value := semanticUseJSONNumber(valueFact.Value)
	keyFact, _ := directSemanticFact(scope, association.keyMember)
	system := stringValue(keyFact.Value)
	logicalType := semanticGeneratedValueType(run.resourceType, valueFact.CanonicalPath, value, semanticValueType(value))
	observation := Observation{
		SchemaVersion: ObservationSchemaVersion,
		Source: Source{
			Canonical: semanticCanonical(run.resourceType, scope.canonicalPath),
			Type:      run.resourceType,
			Profile:   run.profile,
			Path:      scope.canonicalPath,
		},
		Key: Key{
			Selector: appendSemanticPath(scope.canonicalPath, association.keyMember),
			System:   system,
		},
		Value: Value{
			Selector: appendSemanticPath(scope.canonicalPath, association.valueMember),
			Type:     logicalType,
		},
		OwningScope:  scope.canonicalPath,
		LogicalType:  logicalType,
		Completeness: CompletenessComplete,
		Status:       "SUPPORTED",
		RuleHint:     association.ruleHint,
		RuleVersion:  strconv.Itoa(RuleVersion),
	}
	if strings.TrimSpace(system) == "" {
		observation.Status = "UNRESOLVED_SYSTEM"
	}
	run.record(Record{
		StoragePath: scope.canonicalPath, ScopeOwnerPath: scope.ownerPath,
		ConceptOwnerPath: scope.ownerPath, KeyOwnerPath: scope.ownerPath, ValueOwnerPath: valueFact.OwnerPath,
		Observation: observation, Examples: []any{value},
	})
}

func (run *semanticObservationRun) emitExtensionScope(
	scope *semanticScope,
	association semanticDatatypeAssociation,
	extensionURLs map[string]string,
) {
	keyFact, ok := directSemanticFact(scope, association.keyMember)
	if !ok {
		return
	}
	url := stringValue(keyFact.Value)
	if strings.TrimSpace(url) == "" {
		return
	}
	ancestry := extensionURLAncestry(scope.ownerPath, extensionURLs)
	for _, fact := range scope.facts {
		if fact.Element.ChoiceGroup == "" || fact.Null {
			continue
		}
		value, ok := semanticChoiceValueFromFact(run.resourceType, fact)
		if !ok {
			continue
		}
		status := value.Status
		if status == "" {
			status = "SUPPORTED"
		}
		if value.Type == "mixed" {
			status = "UNRESOLVED_VALUE_TYPE"
		}
		observation := Observation{
			SchemaVersion: ObservationSchemaVersion,
			Source: Source{
				Canonical: semanticCanonical(run.resourceType, scope.canonicalPath),
				Type:      run.resourceType,
				Profile:   run.profile,
				Path:      scope.canonicalPath,
			},
			Key: Key{
				Selector: appendSemanticPath(scope.canonicalPath, association.keyMember),
				Display:  url,
			},
			Value: Value{
				Selector:              appendSemanticPath(scope.canonicalPath, value.Selector),
				Type:                  value.Type,
				FallbackSelectors:     prefixSemanticPaths(scope.canonicalPath, value.FallbackSelectors),
				FallbackOnBlankString: value.FallbackOnBlankString,
			},
			OwningScope:      scope.canonicalPath,
			ExtensionURLPath: ancestry,
			ChoiceArm:        value.Arm,
			LogicalType:      value.Type,
			ObservedUnits:    semanticObservedUnits(value.Value),
			Completeness:     CompletenessComplete,
			Status:           status,
			RuleHint:         association.ruleHint,
			RuleVersion:      strconv.Itoa(RuleVersion),
		}
		run.record(Record{
			StoragePath: scope.canonicalPath, ScopeOwnerPath: scope.ownerPath,
			ConceptOwnerPath: scope.ownerPath, KeyOwnerPath: scope.ownerPath, ValueOwnerPath: value.OwnerPath,
			Observation: observation, Examples: semanticExampleValues(value),
		})
	}
}

func (run *semanticObservationRun) emitCodedChoiceScope(
	scope *semanticScope,
	onPair func(Observation),
) {
	concepts := make([]*MemberFact, 0, 1)
	values := make([]semanticChoiceValue, 0, 2)
	for _, fact := range scope.facts {
		if fact.Null {
			continue
		}
		if fact.Element.ChoiceGroup != "" {
			if value, ok := semanticChoiceValueFromFact(run.resourceType, fact); ok {
				values = append(values, value)
			}
			continue
		}
		if fact.ReferencedType == "CodeableConcept" && !fact.Repeated {
			concepts = append(concepts, fact)
		}
	}
	if len(concepts) != 1 || len(values) == 0 {
		return
	}
	conceptValue, ok := concepts[0].Value.(map[string]any)
	if !ok {
		return
	}
	conceptPath := relativeSemanticPath(scope.canonicalPath, concepts[0].CanonicalPath)
	codings := semanticCodings(conceptValue)
	if len(codings) == 0 {
		for _, value := range values {
			run.emitSchemaCodedValue(scope, conceptPath, concepts[0].OwnerPath, concepts[0].OwnerPath, conceptValue, nil, value, onPair)
		}
		return
	}
	for codingIndex, coding := range codings {
		keyOwnerPath := fmt.Sprintf("%s.coding[%d]", concepts[0].OwnerPath, codingIndex)
		for _, value := range values {
			run.emitSchemaCodedValue(scope, conceptPath, concepts[0].OwnerPath, keyOwnerPath, conceptValue, coding, value, onPair)
		}
	}
}

func (run *semanticObservationRun) emitSchemaCodedValue(
	scope *semanticScope,
	conceptPath string,
	conceptOwnerPath string,
	keyOwnerPath string,
	concept map[string]any,
	coding map[string]any,
	value semanticChoiceValue,
	onPair func(Observation),
) {
	key := Key{Selector: appendSemanticPath(conceptPath, "coding[]")}
	if coding == nil {
		key.Selector = appendSemanticPath(conceptPath, "text")
		key.Display = stringValue(concept["text"])
	} else {
		key.System = stringValue(coding["system"])
		key.Version = stringValue(coding["version"])
		key.Code = stringValue(coding["code"])
		key.Display = stringValue(coding["display"])
	}
	status := value.Status
	if status == "" || status == "SUPPORTED" {
		status = "SUPPORTED"
		if strings.TrimSpace(key.System) == "" {
			status = "UNRESOLVED_SYSTEM"
		}
		if coding != nil && strings.TrimSpace(key.Code) == "" {
			status = "UNRESOLVED_CODE"
		}
		if coding == nil {
			status = "UNRESOLVED_BINDING"
		}
	}
	storagePath := scope.canonicalPath
	if storagePath == "" {
		storagePath = conceptPath
	}
	observation := Observation{
		SchemaVersion: ObservationSchemaVersion,
		Source: Source{
			Canonical: semanticCanonical(run.resourceType, storagePath),
			Type:      run.resourceType,
			Profile:   run.profile,
			Path:      storagePath,
		},
		Key: key,
		Value: Value{
			Selector:              value.Selector,
			Type:                  value.Type,
			FallbackSelectors:     append([]string(nil), value.FallbackSelectors...),
			FallbackOnBlankString: value.FallbackOnBlankString,
		},
		OwningScope:   scope.canonicalPath,
		ChoiceArm:     value.Arm,
		LogicalType:   value.Type,
		ObservedUnits: semanticObservedUnits(value.Value),
		Completeness:  CompletenessComplete,
		Status:        status,
		RuleHint:      RuleHintCodedValueV1,
		RuleVersion:   strconv.Itoa(RuleVersion),
	}
	run.record(Record{
		StoragePath: storagePath, ScopeOwnerPath: scope.ownerPath,
		ConceptOwnerPath: conceptOwnerPath, KeyOwnerPath: keyOwnerPath, ValueOwnerPath: value.OwnerPath,
		Observation: observation, Examples: semanticExampleValues(value),
	})
	onPair(observation)
}

func directSemanticFact(scope *semanticScope, member string) (MemberFact, bool) {
	for _, fact := range scope.facts {
		if fact.Element.Name == member {
			return *fact, true
		}
	}
	return MemberFact{}, false
}

func semanticUseJSONNumber(value any) any {
	switch typed := value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		encoded, err := json.Marshal(typed)
		if err == nil {
			return json.Number(encoded)
		}
	}
	return value
}

func semanticChoiceValueFromFact(resourceType string, fact *MemberFact) (semanticChoiceValue, bool) {
	value := semanticUseJSONNumber(fact.Value)
	selector := fact.Element.Name
	valueType := semanticGeneratedValueType(resourceType, fact.CanonicalPath, value, semanticValueType(value))
	status := "SUPPORTED"
	var fallbackSelectors []string
	fallbackOnBlankString := false
	if object, structured := value.(map[string]any); structured {
		projection, projected := semanticValueProjectionAssociations[fact.ReferencedType]
		if projected {
			selector = appendSemanticPath(selector, projection.selector)
			valueType = semanticGeneratedValueType(resourceType, appendSemanticPath(fact.CanonicalPath, projection.selector), value, projection.logicalType)
			status = semanticProjectionStatusWithFallback(object, projection.selector, projection.fallbackSelectors, projection.fallbackOnBlankString)
			fallbackSelectors = prefixSemanticPaths(fact.Element.Name, projection.fallbackSelectors)
			fallbackOnBlankString = projection.fallbackOnBlankString
		} else {
			valueType = "mixed"
		}
	}
	if selector == "" {
		return semanticChoiceValue{}, false
	}
	return semanticChoiceValue{
		Arm:                   fact.Element.Name,
		OwnerPath:             fact.OwnerPath,
		Selector:              selector,
		FallbackSelectors:     fallbackSelectors,
		FallbackOnBlankString: fallbackOnBlankString,
		Type:                  valueType,
		Status:                status,
		Value:                 value,
	}, true
}

func prefixSemanticPaths(prefix string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		result = append(result, appendSemanticPath(prefix, path))
	}
	return result
}

func semanticProjectionStatusWithFallback(value map[string]any, selector string, fallbacks []string, fallbackOnBlankString bool) string {
	primary := semanticValueAtPath(value, selector)
	if text, ok := primary.(string); ok {
		if strings.TrimSpace(text) != "" || !fallbackOnBlankString {
			return semanticProjectionStatus(value, selector)
		}
	} else if primary != nil || !fallbackOnBlankString {
		return semanticProjectionStatus(value, selector)
	}
	for _, fallback := range fallbacks {
		for _, candidate := range semanticValuesAtPath(value, fallback) {
			if text, ok := candidate.(string); ok && strings.TrimSpace(text) != "" {
				return "SUPPORTED"
			}
		}
	}
	return StatusUnsupportedValueProjection
}

func directOwnerPath(path string) string {
	if dot := strings.LastIndex(path, "."); dot >= 0 {
		return path[:dot]
	}
	return ""
}

func relativeSemanticPath(scopePath, path string) string {
	if scopePath == "" {
		return path
	}
	return strings.TrimPrefix(path, scopePath+".")
}

func appendSemanticPath(prefix, member string) string {
	if prefix == "" {
		return member
	}
	if member == "" {
		return prefix
	}
	return prefix + "." + member
}

func semanticCanonical(resourceType, path string) string {
	return appendSemanticPath(resourceType, path)
}

func extensionURLAncestry(ownerPath string, urls map[string]string) []string {
	depth := strings.Count(ownerPath, ".") + 1
	result := make([]string, 0, depth)
	for end := 0; end < len(ownerPath); end++ {
		if ownerPath[end] != '.' {
			continue
		}
		if url := urls[ownerPath[:end]]; url != "" {
			result = append(result, url)
		}
	}
	if url := urls[ownerPath]; url != "" {
		result = append(result, url)
	}
	return result
}
