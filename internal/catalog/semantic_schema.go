package catalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/fhir/schema"
	fhirsemantic "github.com/calypr/loom/internal/fhir/semantic"
)

type semanticScope struct {
	canonicalPath string
	ownerPath     string
	definition    schema.DefinitionName
	value         map[string]any
	facts         []fhirsemantic.MemberFact
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

// These datatype projections are versioned semantic metadata, not resource or
// JSON-member dispatch. The containing resource may use any valid choice-arm
// name; its resolved FHIR datatype determines the scalar preview projection.
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

func (p *Profiler) observeSchemaSemantics(
	index *schema.Index,
	payload map[string]any,
	profile string,
	emit func(SemanticObservation, []any),
) error {
	return p.observeSchemaSemanticsWithEvidence(index, payload, profile, "", func(observation SemanticObservation, examples []any, _ SemanticCoverageEmissionEvidence) {
		emit(observation, examples)
	})
}

func (p *Profiler) observeSchemaSemanticsWithEvidence(
	index *schema.Index,
	payload map[string]any,
	profile string,
	sourceID string,
	emit semanticCoverageObservationEmitter,
) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode FHIR payload for semantic discovery: %w", err)
	}
	facts := make([]fhirsemantic.MemberFact, 0, 32)
	if _, err := fhirsemantic.WalkMembers(index, schema.DefinitionName(p.resourceType), raw, func(fact fhirsemantic.MemberFact) error {
		facts = append(facts, fact)
		return nil
	}); err != nil {
		return err
	}

	root := &semanticScope{
		definition: schema.DefinitionName(p.resourceType),
		value:      payload,
	}
	scopes := []*semanticScope{root}
	byOwner := map[string]*semanticScope{"": root}
	for _, fact := range facts {
		if fact.Repeated && !fact.ArrayItem {
			continue
		}
		value, ok := decodeSemanticObject(fact.RawJSON)
		if !ok {
			continue
		}
		definition := fact.ReferencedType
		if definition == "" {
			definition = fact.DeclaringType
		}
		scope := &semanticScope{
			canonicalPath: fact.CanonicalPath,
			ownerPath:     fact.OwnerPath,
			definition:    definition,
			value:         value,
		}
		scopes = append(scopes, scope)
		byOwner[scope.ownerPath] = scope
	}
	for _, fact := range facts {
		owner := directOwnerPath(fact.OwnerPath)
		if scope := byOwner[owner]; scope != nil {
			scope.facts = append(scope.facts, fact)
		}
	}

	extensionURLs := make(map[string]string)
	for _, scope := range scopes {
		if scope.definition != "Extension" {
			continue
		}
		if url, ok := directSemanticFact(scope, "url"); ok {
			extensionURLs[scope.ownerPath] = semanticStringFromRaw(url.RawJSON)
		}
	}

	pairedCodeableConcepts := make(map[string]struct{})
	identifierOrExtensionOwners := make([]string, 0)
	for _, scope := range scopes {
		if scope.definition == "Identifier" || scope.definition == "Extension" {
			identifierOrExtensionOwners = append(identifierOrExtensionOwners, scope.ownerPath)
		}
	}
	for _, scope := range scopes {
		association, hasAssociation := semanticDatatypeAssociations[scope.definition]
		switch {
		case hasAssociation && scope.definition == "Identifier":
			p.emitIdentifierScope(scope, association, profile, sourceID, emit)
		case hasAssociation && scope.definition == "Extension":
			p.emitExtensionScope(scope, association, extensionURLs, profile, sourceID, emit)
		default:
			p.emitCodedChoiceScope(scope, profile, sourceID, func(observation SemanticObservation, examples []any, evidence SemanticCoverageEmissionEvidence) {
				if observation.RuleHint == SemanticRuleHintCodedValueV1 {
					if strings.HasSuffix(observation.Key.Selector, ".coding[]") {
						conceptPath := strings.TrimSuffix(observation.Key.Selector, ".coding[]")
						pairedCodeableConcepts[appendSemanticPath(scope.ownerPath, conceptPath)] = struct{}{}
					}
					if observation.ChoiceArm == "valueCodeableConcept" && strings.HasSuffix(observation.Value.Selector, ".text") {
						conceptPath := strings.TrimSuffix(observation.Value.Selector, ".text")
						pairedCodeableConcepts[appendSemanticPath(scope.ownerPath, conceptPath)] = struct{}{}
					}
				}
				emit(observation, examples, evidence)
			})
		}
	}
	for _, scope := range scopes {
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
		p.emitStandaloneCodingScope(scope, profile, sourceID, emit)
	}
	return nil
}

func (p *Profiler) emitStandaloneCodingScope(
	scope *semanticScope,
	profile string,
	sourceID string,
	emit semanticCoverageObservationEmitter,
) {
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
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Source: SemanticObservationSource{
			Canonical: semanticCanonical(p.resourceType, scope.canonicalPath),
			Type:      p.resourceType,
			Profile:   profile,
			Path:      scope.canonicalPath,
		},
		Key: SemanticObservationKey{
			Selector: keyPath,
			System:   system,
			Version:  stringValue(scope.value["version"]),
			Code:     code,
			Display:  display,
		},
		Value: SemanticObservationValue{
			Selector:              valuePath,
			Type:                  "string",
			FallbackSelectors:     []string{fallbackPath},
			FallbackOnBlankString: true,
		},
		OwningScope:  ownerPath,
		LogicalType:  "string",
		Completeness: SemanticComplete,
		Status:       "SUPPORTED",
		RuleHint:     SemanticRuleHintCodedConceptV1,
		RuleVersion:  strconv.Itoa(SemanticObservationRuleVersion),
	}
	p.recordSchemaSemanticObservation(scope.canonicalPath, scope, scope.ownerPath, scope.ownerPath, scope.ownerPath, observation, []any{example}, emit, sourceID)
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

func (p *Profiler) emitIdentifierScope(
	scope *semanticScope,
	association semanticDatatypeAssociation,
	profile string,
	sourceID string,
	emit semanticCoverageObservationEmitter,
) {
	valueFact, ok := directSemanticFact(scope, association.valueMember)
	if !ok || valueFact.Null {
		return
	}
	value, ok := decodeSemanticValue(valueFact.RawJSON)
	if !ok {
		return
	}
	keyFact, _ := directSemanticFact(scope, association.keyMember)
	system := semanticStringFromRaw(keyFact.RawJSON)
	logicalType := semanticGeneratedValueType(p.resourceType, valueFact.CanonicalPath, value, semanticValueType(value))
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Source: SemanticObservationSource{
			Canonical: semanticCanonical(p.resourceType, scope.canonicalPath),
			Type:      p.resourceType,
			Profile:   profile,
			Path:      scope.canonicalPath,
		},
		Key: SemanticObservationKey{
			Selector: appendSemanticPath(scope.canonicalPath, association.keyMember),
			System:   system,
		},
		Value: SemanticObservationValue{
			Selector: appendSemanticPath(scope.canonicalPath, association.valueMember),
			Type:     logicalType,
		},
		OwningScope:  scope.canonicalPath,
		LogicalType:  logicalType,
		Completeness: SemanticComplete,
		Status:       "SUPPORTED",
		RuleHint:     association.ruleHint,
		RuleVersion:  strconv.Itoa(SemanticObservationRuleVersion),
	}
	if strings.TrimSpace(system) == "" {
		observation.Status = "UNRESOLVED_SYSTEM"
	}
	p.recordSchemaSemanticObservation(scope.canonicalPath, scope, scope.ownerPath, scope.ownerPath, valueFact.OwnerPath, observation, []any{value}, emit, sourceID)
}

func (p *Profiler) emitExtensionScope(
	scope *semanticScope,
	association semanticDatatypeAssociation,
	extensionURLs map[string]string,
	profile string,
	sourceID string,
	emit semanticCoverageObservationEmitter,
) {
	keyFact, ok := directSemanticFact(scope, association.keyMember)
	if !ok {
		return
	}
	url := semanticStringFromRaw(keyFact.RawJSON)
	if strings.TrimSpace(url) == "" {
		return
	}
	ancestry := extensionURLAncestry(scope.ownerPath, extensionURLs)
	for _, fact := range scope.facts {
		if fact.ArrayItem || fact.Element.ChoiceGroup == "" || fact.Null {
			continue
		}
		value, ok := semanticChoiceValueFromFact(p.resourceType, fact)
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
		observation := SemanticObservation{
			SchemaVersion: SemanticObservationSchemaVersion,
			Source: SemanticObservationSource{
				Canonical: semanticCanonical(p.resourceType, scope.canonicalPath),
				Type:      p.resourceType,
				Profile:   profile,
				Path:      scope.canonicalPath,
			},
			Key: SemanticObservationKey{
				Selector: appendSemanticPath(scope.canonicalPath, association.keyMember),
				Display:  url,
			},
			Value: SemanticObservationValue{
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
			Completeness:     SemanticComplete,
			Status:           status,
			RuleHint:         association.ruleHint,
			RuleVersion:      strconv.Itoa(SemanticObservationRuleVersion),
		}
		p.recordSchemaSemanticObservation(scope.canonicalPath, scope, scope.ownerPath, scope.ownerPath, value.OwnerPath, observation, semanticExampleValues(value), emit, sourceID)
	}
}

func (p *Profiler) emitCodedChoiceScope(
	scope *semanticScope,
	profile string,
	sourceID string,
	emit semanticCoverageObservationEmitter,
) {
	concepts := make([]fhirsemantic.MemberFact, 0, 1)
	values := make([]semanticChoiceValue, 0, 2)
	for _, fact := range scope.facts {
		if fact.ArrayItem {
			continue
		}
		if fact.Null {
			continue
		}
		if fact.Element.ChoiceGroup != "" {
			if value, ok := semanticChoiceValueFromFact(p.resourceType, fact); ok {
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
	conceptValue, ok := decodeSemanticObject(concepts[0].RawJSON)
	if !ok {
		return
	}
	conceptPath := relativeSemanticPath(scope.canonicalPath, concepts[0].CanonicalPath)
	codings := semanticCodings(conceptValue)
	if len(codings) == 0 {
		for _, value := range values {
			p.emitSchemaCodedValue(scope, conceptPath, concepts[0].OwnerPath, concepts[0].OwnerPath, conceptValue, nil, value, profile, sourceID, emit)
		}
		return
	}
	for codingIndex, coding := range codings {
		keyOwnerPath := fmt.Sprintf("%s.coding[%d]", concepts[0].OwnerPath, codingIndex)
		for _, value := range values {
			p.emitSchemaCodedValue(scope, conceptPath, concepts[0].OwnerPath, keyOwnerPath, conceptValue, coding, value, profile, sourceID, emit)
		}
	}
}

func (p *Profiler) emitSchemaCodedValue(
	scope *semanticScope,
	conceptPath string,
	conceptOwnerPath string,
	keyOwnerPath string,
	concept map[string]any,
	coding map[string]any,
	value semanticChoiceValue,
	profile string,
	sourceID string,
	emit semanticCoverageObservationEmitter,
) {
	key := SemanticObservationKey{Selector: appendSemanticPath(conceptPath, "coding[]")}
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
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Source: SemanticObservationSource{
			Canonical: semanticCanonical(p.resourceType, storagePath),
			Type:      p.resourceType,
			Profile:   profile,
			Path:      storagePath,
		},
		Key: key,
		Value: SemanticObservationValue{
			Selector:              value.Selector,
			Type:                  value.Type,
			FallbackSelectors:     append([]string(nil), value.FallbackSelectors...),
			FallbackOnBlankString: value.FallbackOnBlankString,
		},
		OwningScope:   scope.canonicalPath,
		ChoiceArm:     value.Arm,
		LogicalType:   value.Type,
		ObservedUnits: semanticObservedUnits(value.Value),
		Completeness:  SemanticComplete,
		Status:        status,
		RuleHint:      SemanticRuleHintCodedValueV1,
		RuleVersion:   strconv.Itoa(SemanticObservationRuleVersion),
	}
	p.recordSchemaSemanticObservation(storagePath, scope, conceptOwnerPath, keyOwnerPath, value.OwnerPath, observation, semanticExampleValues(value), emit, sourceID)
}

func (p *Profiler) recordSchemaSemanticObservation(
	storagePath string,
	scope *semanticScope,
	conceptOwnerPath string,
	keyOwnerPath string,
	valueOwnerPath string,
	observation SemanticObservation,
	examples []any,
	emit semanticCoverageObservationEmitter,
	sourceID string,
) {
	if stat := p.ensureSemanticStat(storagePath); stat != nil {
		stat.addSemanticObservation(observation, examples)
	}
	emit(observation, examples, semanticCoverageEmissionEvidence(p.resourceType, p.semanticSourceKind, sourceID, scope, conceptOwnerPath, keyOwnerPath, valueOwnerPath, observation))
}

func directSemanticFact(scope *semanticScope, member string) (fhirsemantic.MemberFact, bool) {
	for _, fact := range scope.facts {
		if !fact.ArrayItem && fact.Element.Name == member {
			return fact, true
		}
	}
	return fhirsemantic.MemberFact{}, false
}

func semanticChoiceValueFromFact(resourceType string, fact fhirsemantic.MemberFact) (semanticChoiceValue, bool) {
	value, ok := decodeSemanticValue(fact.RawJSON)
	if !ok {
		return semanticChoiceValue{}, false
	}
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
	return SemanticStatusUnsupportedValueProjection
}

func decodeSemanticObject(raw json.RawMessage) (map[string]any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return nil, false
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil || value == nil {
		return nil, false
	}
	return value, true
}

func decodeSemanticValue(raw json.RawMessage) (any, bool) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	return value, true
}

func semanticStringFromRaw(raw json.RawMessage) string {
	value, ok := decodeSemanticValue(raw)
	if !ok {
		return ""
	}
	return stringValue(value)
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
