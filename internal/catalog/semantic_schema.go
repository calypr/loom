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
	declaringType schema.DefinitionName
	member        schema.Element
	arrayItem     bool
	value         map[string]any
	facts         []fhirsemantic.MemberFact
}

const (
	SemanticRuleHintCategoricalSlotV1 = "CATEGORICAL_SLOT_V1"
	// SemanticRuleHintCategoricalCodeV1 is retained as a source-compatible
	// alias for older callers. New observations always use the categorical-slot
	// rule because one schema slot, not one observed Coding, is the feature.
	SemanticRuleHintCategoricalCodeV1 = SemanticRuleHintCategoricalSlotV1
	SemanticRuleHintCategoricalTextV1 = "CATEGORICAL_TEXT_V1"
)

func (p *Profiler) observeSchemaSemantics(
	index *schema.Index,
	payload map[string]any,
	profile string,
	emit func(SemanticObservation, []any),
) error {
	registry, err := fhirsemantic.RegistryForIndex(index)
	if err != nil {
		return err
	}
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
			declaringType: fact.DeclaringType,
			member:        fact.Element,
			arrayItem:     fact.ArrayItem,
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

	type pendingCategoricalSlot struct {
		observation SemanticObservation
		examples    []any
	}
	pending := make(map[string]*pendingCategoricalSlot)
	emitObservation := func(observation SemanticObservation, examples []any) {
		if SemanticObservationRoleOf(observation) != SemanticRoleCategoricalSlot {
			emit(observation, examples)
			return
		}
		key := semanticObservationKey(observation)
		slot := pending[key]
		if slot == nil {
			slot = &pendingCategoricalSlot{observation: observation}
			pending[key] = slot
		}
		slot.examples = append(slot.examples, examples...)
	}

	extensionURLs := make(map[string]string)
	for _, scope := range scopes {
		descriptor, ok := registry.Lookup(scope.definition)
		if !ok || descriptor.Disposition != fhirsemantic.DispositionValueAssociation || descriptor.ValueChoiceGroup == "" {
			continue
		}
		keyMember := memberPathForRole(descriptor, fhirsemantic.MemberAssociationKey)
		if key, ok := directSemanticFact(scope, keyMember); ok {
			extensionURLs[scope.ownerPath] = semanticStringFromRaw(key.RawJSON)
		}
	}
	for _, scope := range scopes {
		descriptor, ok := registry.Lookup(scope.definition)
		if !ok {
			continue
		}
		switch descriptor.Disposition {
		case fhirsemantic.DispositionValueAssociation:
			p.emitValueAssociationScope(scope, descriptor, extensionURLs, registry, profile, emit)
		case fhirsemantic.DispositionCompositeValue:
			if scope.canonicalPath != "" && !semanticScopeHasOwner(scope, byOwner, registry) {
				p.emitStructuredScope(scope, profile, emitObservation)
			}
		case fhirsemantic.DispositionCategorical:
			if scope.member.ChoiceGroup == "" && !categoricalSourceOwnedByParent(scope, registry) {
				p.emitCategoricalScope(scope, descriptor, profile, emitObservation)
			}
		}
	}
	for _, fact := range facts {
		if !isPrimitiveCategoricalFact(fact) || fact.Element.ChoiceGroup != "" || (fact.Repeated && !fact.ArrayItem) {
			continue
		}
		owner := byOwner[directOwnerPath(fact.OwnerPath)]
		if owner == nil {
			continue
		}
		if owner.canonicalPath != "" {
			if descriptor, owned := registry.Lookup(owner.definition); owned && descriptor.Disposition != fhirsemantic.DispositionAdvancedOnly {
				continue
			}
		}
		p.emitPrimitiveCategoricalFact(owner, fact, profile, emitObservation)
	}
	for _, scope := range scopes {
		p.emitCodedChoiceScope(scope, registry, profile, emitObservation)
	}
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		slot := pending[key]
		emit(slot.observation, slot.examples)
	}
	return nil
}

func (p *Profiler) emitValueAssociationScope(
	scope *semanticScope,
	descriptor fhirsemantic.DatatypeDescriptor,
	extensionURLs map[string]string,
	registry *fhirsemantic.DatatypeRegistry,
	profile string,
	emit func(SemanticObservation, []any),
) {
	if descriptor.ValueChoiceGroup != "" {
		p.emitExtensionScope(scope, descriptor, extensionURLs, registry, profile, emit)
		return
	}
	keyMember := memberPathForRole(descriptor, fhirsemantic.MemberAssociationKey)
	valueMember := memberPathForRole(descriptor, fhirsemantic.MemberAssociationValue)
	valueFact, ok := directSemanticFact(scope, valueMember)
	if !ok || valueFact.Null {
		return
	}
	value, ok := decodeSemanticValue(valueFact.RawJSON)
	if !ok {
		return
	}
	keyFact, _ := directSemanticFact(scope, keyMember)
	key := SemanticObservationKey{
		Selector: appendSemanticPath(scope.canonicalPath, keyMember),
		System:   semanticStringFromRaw(keyFact.RawJSON),
	}
	logicalType := semanticGeneratedValueType(p.resourceType, valueFact.CanonicalPath, value, semanticValueType(value))
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Role:          SemanticObservationRoleForRule(descriptor.RuleHint),
		Source: SemanticObservationSource{
			Canonical: semanticCanonical(p.resourceType, scope.canonicalPath),
			Type:      p.resourceType,
			Profile:   profile,
			Path:      scope.canonicalPath,
		},
		Key: key,
		Value: SemanticObservationValue{
			Selector: appendSemanticPath(scope.canonicalPath, valueMember),
			Type:     logicalType,
		},
		OwningScope:  scope.canonicalPath,
		LogicalType:  logicalType,
		Completeness: SemanticComplete,
		Status:       "SUPPORTED",
		RuleHint:     descriptor.RuleHint,
		RuleVersion:  strconv.Itoa(SemanticObservationRuleVersion),
	}
	if strings.TrimSpace(key.System) == "" {
		observation.Status = "UNRESOLVED_SYSTEM"
	}
	p.recordSchemaSemanticObservation(scope.canonicalPath, observation, []any{value}, emit)
}

func (p *Profiler) emitExtensionScope(
	scope *semanticScope,
	descriptor fhirsemantic.DatatypeDescriptor,
	extensionURLs map[string]string,
	registry *fhirsemantic.DatatypeRegistry,
	profile string,
	emit func(SemanticObservation, []any),
) {
	keyMember := memberPathForRole(descriptor, fhirsemantic.MemberAssociationKey)
	keyFact, ok := directSemanticFact(scope, keyMember)
	if !ok {
		return
	}
	url := semanticStringFromRaw(keyFact.RawJSON)
	if strings.TrimSpace(url) == "" {
		return
	}
	ancestry := extensionURLAncestry(scope.ownerPath, extensionURLs)
	for _, fact := range scope.facts {
		if fact.ArrayItem || fact.Element.ChoiceGroup != descriptor.ValueChoiceGroup || fact.Null {
			continue
		}
		value, ok := semanticChoiceValueFromFact(p.resourceType, fact, registry)
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
			Role:          SemanticObservationRoleForRule(descriptor.RuleHint),
			Source: SemanticObservationSource{
				Canonical: semanticCanonical(p.resourceType, scope.canonicalPath),
				Type:      p.resourceType,
				Profile:   profile,
				Path:      scope.canonicalPath,
			},
			Key: SemanticObservationKey{
				Selector: appendSemanticPath(scope.canonicalPath, keyMember),
				Display:  url,
			},
			Value: SemanticObservationValue{
				Selector:     appendSemanticPath(scope.canonicalPath, value.Selector),
				Type:         value.Type,
				Presentation: value.Presentation,
			},
			OwningScope:      scope.canonicalPath,
			ExtensionURLPath: ancestry,
			ChoiceArm:        value.Arm,
			LogicalType:      value.Type,
			ObservedUnits:    semanticObservedUnits(value.Value),
			Completeness:     SemanticComplete,
			Status:           status,
			RuleHint:         descriptor.RuleHint,
			RuleVersion:      strconv.Itoa(SemanticObservationRuleVersion),
		}
		p.recordSchemaSemanticObservation(scope.canonicalPath, observation, []any{semanticExampleValue(value)}, emit)
	}
}

func (p *Profiler) emitCategoricalScope(
	scope *semanticScope,
	descriptor fhirsemantic.DatatypeDescriptor,
	profile string,
	emit func(SemanticObservation, []any),
) {
	for _, candidate := range semanticCategoricalSlotCandidates(p.resourceType, descriptor, scope.canonicalPath, scope.value) {
		observation := SemanticObservation{
			SchemaVersion: SemanticObservationSchemaVersion,
			Role:          SemanticRoleCategoricalSlot,
			Source: SemanticObservationSource{
				Canonical: semanticCanonical(p.resourceType, scope.canonicalPath),
				Type:      p.resourceType,
				Profile:   profile,
				Path:      scope.canonicalPath,
			},
			Key:          candidate.key,
			Value:        SemanticObservationValue{Selector: candidate.valueSelector, Type: candidate.logicalType, Presentation: candidate.presentation},
			OwningScope:  scope.canonicalPath,
			LogicalType:  candidate.logicalType,
			Completeness: candidate.completeness,
			Status:       candidate.status,
			RuleHint:     SemanticRuleHintCategoricalSlotV1,
			RuleVersion:  strconv.Itoa(SemanticObservationRuleVersion),
		}
		observation.SlotLabel, observation.SlotDescription = semanticSlotPresentation(scope.member, scope.canonicalPath)
		p.recordSchemaSemanticObservation(scope.canonicalPath, observation, candidate.examples, emit)
	}
}

type semanticCategoricalSlotCandidate struct {
	key           SemanticObservationKey
	valueSelector string
	logicalType   string
	presentation  string
	examples      []any
	status        string
	completeness  SemanticObservationCompleteness
}

func semanticCategoricalSlotCandidates(
	resourceType string,
	descriptor fhirsemantic.DatatypeDescriptor,
	basePath string,
	object map[string]any,
) []semanticCategoricalSlotCandidate {
	collection := memberPathForRole(descriptor, fhirsemantic.MemberCategoryCoding)
	codePath := memberPathForRole(descriptor, fhirsemantic.MemberCategoryCode)
	systemPath := memberPathForRole(descriptor, fhirsemantic.MemberCategorySystem)
	versionPath := memberPathForRole(descriptor, fhirsemantic.MemberCategoryVersion)
	displayPath := memberPathForRole(descriptor, fhirsemantic.MemberCategoryDisplay)
	textPath := memberPathForRole(descriptor, fhirsemantic.MemberCategoryText)
	codePath = categoryMemberPath(descriptor, fhirsemantic.MemberCategoryCode, collection)
	systemPath = categoryMemberPath(descriptor, fhirsemantic.MemberCategorySystem, collection)
	versionPath = categoryMemberPath(descriptor, fhirsemantic.MemberCategoryVersion, collection)
	displayPath = categoryMemberPath(descriptor, fhirsemantic.MemberCategoryDisplay, collection)
	keySelector := appendSemanticPath(basePath, collection)
	valueMember := codePath
	if collection != "" {
		valueMember = collection + "." + codePath
	}
	valueSelector := appendSemanticPath(basePath, valueMember)
	// DISPLAY_OR_CODE is evaluated on the same Coding item. The logical output
	// is therefore string even when the fallback source member is the FHIR
	// `code` primitive.
	logicalType := "string"
	const presentation = schema.ValuePresentationDisplayOrCode
	type namespaceDomain struct {
		system   string
		examples []any
	}
	domains := map[string]*namespaceDomain{}
	addCoding := func(coding map[string]any, index int) {
		system := strings.TrimSpace(stringValueAt(coding, systemPath))
		if system == "" {
			return
		}
		domain := categoricalDomainRecord("coding", keySelector, coding, codePath, systemPath, versionPath, displayPath)
		if collection != "" {
			domain["index"] = index
		}
		entry := domains[system]
		if entry == nil {
			entry = &namespaceDomain{system: system}
			domains[system] = entry
		}
		entry.examples = append(entry.examples, semanticDomainExample(domain))
	}
	if collection != "" {
		collectionMember := strings.TrimSuffix(collection, "[]")
		if rawItems, ok := semanticValueAt(object, collectionMember); ok {
			if items, ok := rawItems.([]any); ok {
				for index, item := range items {
					coding, ok := item.(map[string]any)
					if ok {
						addCoding(coding, index)
					}
				}
			}
		}
	} else if strings.TrimSpace(stringValueAt(object, systemPath)) != "" {
		addCoding(object, 0)
	}
	if len(domains) != 0 {
		systems := make([]string, 0, len(domains))
		for system := range domains {
			systems = append(systems, system)
		}
		sort.Strings(systems)
		candidates := make([]semanticCategoricalSlotCandidate, 0, len(systems))
		for _, system := range systems {
			candidates = append(candidates, semanticCategoricalSlotCandidate{
				key:           SemanticObservationKey{Selector: keySelector, System: system},
				valueSelector: valueSelector,
				logicalType:   semanticGeneratedValueType(resourceType, valueSelector, nil, logicalType),
				presentation:  presentation,
				examples:      domains[system].examples,
				status:        "SUPPORTED",
				completeness:  SemanticComplete,
			})
		}
		return candidates
	}
	// A text-only CodeableConcept is a scalar categorical slot, not a Coding
	// namespace. It remains directly selectable and never receives a fake
	// empty-system binding.
	if textPath != "" {
		if text, ok := semanticValueAt(object, textPath); ok && strings.TrimSpace(stringValue(text)) != "" {
			selector := appendSemanticPath(basePath, textPath)
			return []semanticCategoricalSlotCandidate{{
				key:           SemanticObservationKey{Selector: selector},
				valueSelector: selector,
				logicalType:   semanticGeneratedValueType(resourceType, selector, text, "string"),
				examples:      []any{semanticDomainExample(map[string]any{"kind": "text", "selector": selector, "value": text})},
				status:        "SUPPORTED",
				completeness:  SemanticComplete,
			}}
		}
	}
	return nil
}

func semanticDomainExample(value map[string]any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func categoricalDomainRecord(kind, selector string, object map[string]any, codePath, systemPath, versionPath, displayPath string) map[string]any {
	return map[string]any{
		"kind":     kind,
		"selector": selector,
		"system":   stringValueAt(object, systemPath),
		"version":  stringValueAt(object, versionPath),
		"code":     stringValueAt(object, codePath),
		"display":  stringValueAt(object, displayPath),
	}
}

func stringValueAt(value any, path string) string {
	if path == "" {
		return ""
	}
	child, ok := semanticValueAt(value, path)
	if !ok {
		return ""
	}
	return stringValue(child)
}

func semanticSlotPresentation(element schema.Element, path string) (string, string) {
	label := strings.TrimSpace(element.Title)
	if label == "" {
		label = strings.TrimSpace(path)
	}
	return label, strings.TrimSpace(element.Description)
}

func isPrimitiveCategoricalFact(fact fhirsemantic.MemberFact) bool {
	if strings.TrimSpace(fact.Element.BindingURI) == "" || fact.Null {
		return false
	}
	if fact.ArrayItem {
		return fact.Element.ArrayElementType == "" && len(fact.Element.ArrayElements) == 0 && fact.Element.ItemJSONType != schema.JSONTypeObject && fact.Element.ItemJSONType != schema.JSONTypeArray
	}
	return fact.Element.ReferencedType == "" && len(fact.Element.Elements) == 0 && fact.Element.JSONType != schema.JSONTypeObject && fact.Element.JSONType != schema.JSONTypeArray
}

func (p *Profiler) emitPrimitiveCategoricalFact(
	owner *semanticScope,
	fact fhirsemantic.MemberFact,
	profile string,
	emit func(SemanticObservation, []any),
) {
	value, ok := decodeSemanticValue(fact.RawJSON)
	if !ok {
		return
	}
	logicalType := semanticGeneratedValueType(p.resourceType, fact.CanonicalPath, value, semanticValueType(value))
	selector := fact.CanonicalPath
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Role:          SemanticRoleCategoricalSlot,
		Source: SemanticObservationSource{
			Canonical: semanticCanonical(p.resourceType, selector),
			Type:      p.resourceType,
			Profile:   profile,
			Path:      selector,
		},
		Key:          SemanticObservationKey{Selector: selector},
		Value:        SemanticObservationValue{Selector: selector, Type: logicalType},
		OwningScope:  owner.canonicalPath,
		LogicalType:  logicalType,
		Completeness: SemanticComplete,
		Status:       "SUPPORTED",
		RuleHint:     SemanticRuleHintCategoricalSlotV1,
		RuleVersion:  strconv.Itoa(SemanticObservationRuleVersion),
	}
	observation.SlotLabel, observation.SlotDescription = semanticSlotPresentation(fact.Element, selector)
	p.recordSchemaSemanticObservation(selector, observation, []any{value}, emit)
}

type semanticCategoricalCandidate struct {
	key           SemanticObservationKey
	valueSelector string
	logicalType   string
	value         any
	status        string
	completeness  SemanticObservationCompleteness
	ruleHint      string
}

func semanticCategoricalCandidates(
	resourceType string,
	descriptor fhirsemantic.DatatypeDescriptor,
	basePath string,
	object map[string]any,
) []semanticCategoricalCandidate {
	codePath := memberPathForRole(descriptor, fhirsemantic.MemberCategoryCode)
	if codePath == "" {
		return nil
	}
	collection := memberPathForRole(descriptor, fhirsemantic.MemberCategoryCoding)
	collectionMember := strings.TrimSuffix(collection, "[]")
	codePath = categoryMemberPath(descriptor, fhirsemantic.MemberCategoryCode, collection)
	systemPath := categoryMemberPath(descriptor, fhirsemantic.MemberCategorySystem, collection)
	versionPath := categoryMemberPath(descriptor, fhirsemantic.MemberCategoryVersion, collection)
	displayPath := categoryMemberPath(descriptor, fhirsemantic.MemberCategoryDisplay, collection)
	textPath := memberPathForRole(descriptor, fhirsemantic.MemberCategoryText)
	candidates := make([]semanticCategoricalCandidate, 0, 2)

	if collection == "" {
		if candidate, ok := categoricalCandidateFromItem(resourceType, descriptor, basePath, basePath, object, codePath, systemPath, versionPath, displayPath); ok {
			candidates = append(candidates, candidate)
		}
	} else if rawItems, ok := semanticValueAt(object, collectionMember); ok {
		if items, ok := rawItems.([]any); ok {
			for _, item := range items {
				coding, ok := item.(map[string]any)
				if !ok {
					continue
				}
				keySelector := appendSemanticPath(basePath, collection)
				if candidate, ok := categoricalCandidateFromItem(resourceType, descriptor, keySelector, keySelector, coding, codePath, systemPath, versionPath, displayPath); ok {
					candidates = append(candidates, candidate)
				}
			}
		}
	}
	if len(candidates) != 0 || textPath == "" {
		return candidates
	}
	text, ok := semanticValueAt(object, textPath)
	if !ok || strings.TrimSpace(stringValue(text)) == "" {
		return candidates
	}
	selector := appendSemanticPath(basePath, textPath)
	candidates = append(candidates, semanticCategoricalCandidate{
		key: SemanticObservationKey{
			Selector: selector,
			Display:  stringValue(text),
		},
		valueSelector: selector,
		logicalType:   semanticGeneratedValueType(resourceType, selector, text, "string"),
		value:         text,
		status:        "UNRESOLVED_BINDING",
		completeness:  SemanticPartial,
		ruleHint:      SemanticRuleHintCategoricalTextV1,
	})
	return candidates
}

func categoricalCandidateFromItem(
	resourceType string,
	descriptor fhirsemantic.DatatypeDescriptor,
	keySelector string,
	valueSelector string,
	item map[string]any,
	codePath string,
	systemPath string,
	versionPath string,
	displayPath string,
) (semanticCategoricalCandidate, bool) {
	codeValue, codePresent := semanticValueAt(item, codePath)
	code := stringValue(codeValue)
	displayValue, displayPresent := semanticValueAt(item, displayPath)
	display := stringValue(displayValue)
	if strings.TrimSpace(code) == "" && strings.TrimSpace(display) == "" {
		return semanticCategoricalCandidate{}, false
	}
	systemValue, _ := semanticValueAt(item, systemPath)
	versionValue, _ := semanticValueAt(item, versionPath)
	selectedPath := codePath
	selectedValue := codeValue
	if strings.TrimSpace(code) == "" && displayPresent {
		selectedPath = displayPath
		selectedValue = displayValue
	}
	if !codePresent && strings.TrimSpace(display) != "" {
		selectedPath = displayPath
		selectedValue = displayValue
	}
	valueSelector = appendSemanticPath(valueSelector, selectedPath)
	status := "SUPPORTED"
	completeness := SemanticComplete
	if strings.TrimSpace(code) == "" {
		status = "UNRESOLVED_CODE"
		completeness = SemanticPartial
	} else if strings.TrimSpace(stringValue(systemValue)) == "" {
		status = "UNRESOLVED_SYSTEM"
		completeness = SemanticPartial
	}
	logicalType := "code"
	ruleHint := descriptor.RuleHint
	if ruleHint == "" {
		ruleHint = SemanticRuleHintCategoricalCodeV1
	}
	if selectedPath == displayPath {
		logicalType = "string"
	}
	return semanticCategoricalCandidate{
		key: SemanticObservationKey{
			Selector: keySelector,
			System:   stringValue(systemValue),
			Version:  stringValue(versionValue),
			Code:     code,
			Display:  display,
		},
		valueSelector: valueSelector,
		logicalType:   semanticGeneratedValueType(resourceType, valueSelector, selectedValue, logicalType),
		value:         selectedValue,
		status:        status,
		completeness:  completeness,
		ruleHint:      ruleHint,
	}, true
}

func categoryMemberPath(descriptor fhirsemantic.DatatypeDescriptor, role fhirsemantic.MemberRole, collection string) string {
	path := memberPathForRole(descriptor, role)
	if collection == "" {
		return path
	}
	prefix := collection + "."
	return strings.TrimPrefix(path, prefix)
}

func semanticValueAt(value any, path string) (any, bool) {
	if path == "" {
		return value, true
	}
	current := value
	for _, part := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok || current == nil {
			return nil, false
		}
	}
	return current, true
}

func categoricalSourceOwnedByParent(scope *semanticScope, registry *fhirsemantic.DatatypeRegistry) bool {
	if !scope.arrayItem || scope.declaringType == "" {
		return false
	}
	parent, ok := registry.Lookup(scope.declaringType)
	if !ok || parent.Disposition != fhirsemantic.DispositionCategorical {
		return false
	}
	codingMember := memberPathForRole(parent, fhirsemantic.MemberCategoryCoding)
	return codingMember != "" && strings.TrimSuffix(codingMember, "[]") == scope.member.Name
}

func memberPathForRole(descriptor fhirsemantic.DatatypeDescriptor, role fhirsemantic.MemberRole) string {
	for _, member := range descriptor.MemberRoles {
		if member.Role == role {
			return member.Path
		}
	}
	return ""
}

func (p *Profiler) emitCodedChoiceScope(
	scope *semanticScope,
	registry *fhirsemantic.DatatypeRegistry,
	profile string,
	emit func(SemanticObservation, []any),
) {
	if registry.HasUnresolvedValueGroup(scope.definition) {
		values := pairingValues(scope, fhirsemantic.ScopePairingDescriptor{ValueChoiceGroup: "value"}, p.resourceType, registry)
		for _, value := range values {
			candidate := semanticCategoricalCandidate{status: "UNRESOLVED_BINDING", completeness: SemanticPartial}
			p.emitSchemaCodedValue(scope, candidate, value, profile, emit)
		}
	}
	for _, pairing := range registry.ScopePairings(scope.definition) {
		values := pairingValues(scope, pairing, p.resourceType, registry)
		if len(values) == 0 {
			continue
		}
		if len(values) > 1 {
			for index := range values {
				values[index].Status = "MIXED_CHOICE"
			}
		}
		var candidates []semanticCategoricalCandidate
		if concept, ok := directSemanticFactPath(scope, pairing.CategoricalPath); ok && !concept.Null {
			if object, ok := decodeSemanticObject(concept.RawJSON); ok {
				if descriptor, ok := registry.Lookup(concept.ReferencedType); ok {
					candidates = semanticCategoricalCandidates(p.resourceType, descriptor, pairing.CategoricalPath, object)
				}
			}
		}
		if len(candidates) == 0 {
			candidates = []semanticCategoricalCandidate{{key: SemanticObservationKey{Selector: pairing.CategoricalPath}, status: "UNRESOLVED_BINDING", completeness: SemanticPartial}}
		}
		for _, candidate := range candidates {
			for _, value := range values {
				p.emitSchemaCodedValue(scope, candidate, value, profile, emit)
			}
		}
	}
}

func directSemanticFactPath(scope *semanticScope, path string) (fhirsemantic.MemberFact, bool) {
	if path == "" {
		return fhirsemantic.MemberFact{}, false
	}
	for _, fact := range scope.facts {
		if !fact.ArrayItem && fact.Element.Name == path {
			return fact, true
		}
	}
	return fhirsemantic.MemberFact{}, false
}

func pairingValues(
	scope *semanticScope,
	pairing fhirsemantic.ScopePairingDescriptor,
	resourceType string,
	registry *fhirsemantic.DatatypeRegistry,
) []semanticChoiceValue {
	values := make([]semanticChoiceValue, 0, 2)
	for _, fact := range scope.facts {
		if fact.ArrayItem || fact.Null {
			continue
		}
		if pairing.ValueChoiceGroup != "" && fact.Element.ChoiceGroup != pairing.ValueChoiceGroup {
			continue
		}
		if pairing.ValuePath != "" && fact.Element.Name != pairing.ValuePath {
			continue
		}
		if pairing.ValueChoiceGroup == "" && pairing.ValuePath == "" {
			continue
		}
		value, ok := semanticChoiceValueFromFact(resourceType, fact, registry)
		if ok {
			values = append(values, value)
		}
	}
	return values
}

func (p *Profiler) emitSchemaCodedValue(
	scope *semanticScope,
	candidate semanticCategoricalCandidate,
	value semanticChoiceValue,
	profile string,
	emit func(SemanticObservation, []any),
) {
	status := value.Status
	if status == "" || status == "SUPPORTED" {
		status = candidate.status
	}
	storagePath := scope.canonicalPath
	if storagePath == "" {
		storagePath = candidate.key.Selector
	}
	observation := SemanticObservation{
		SchemaVersion: SemanticObservationSchemaVersion,
		Role:          SemanticRoleCodedValue,
		Source: SemanticObservationSource{
			Canonical: semanticCanonical(p.resourceType, storagePath),
			Type:      p.resourceType,
			Profile:   profile,
			Path:      storagePath,
		},
		Key: candidate.key,
		Value: SemanticObservationValue{
			Selector:     value.Selector,
			Type:         value.Type,
			Presentation: value.Presentation,
		},
		OwningScope:   scope.canonicalPath,
		ChoiceArm:     value.Arm,
		LogicalType:   value.Type,
		ObservedUnits: semanticObservedUnits(value.Value),
		Completeness:  candidate.completeness,
		Status:        status,
		RuleHint:      SemanticRuleHintCodedValueV1,
		RuleVersion:   strconv.Itoa(SemanticObservationRuleVersion),
	}
	conceptName := strings.TrimSuffix(strings.Split(candidate.key.Selector, ".")[0], "[]")
	if fact, ok := directSemanticFact(scope, conceptName); ok {
		_, observation.SlotDescription = semanticSlotPresentation(fact.Element, candidate.key.Selector)
	}
	observation.SlotLabel = strings.TrimSpace(candidate.key.Display)
	if observation.SlotLabel == "" {
		observation.SlotLabel = strings.TrimSpace(candidate.key.Code)
	}
	p.recordSchemaSemanticObservation(storagePath, observation, []any{semanticExampleValue(value)}, emit)
}

func (p *Profiler) recordSchemaSemanticObservation(
	storagePath string,
	observation SemanticObservation,
	examples []any,
	emit func(SemanticObservation, []any),
) {
	if stat := p.ensureSemanticStat(storagePath); stat != nil {
		stat.addSemanticObservation(observation, examples)
	}
	emit(observation, examples)
}

func directSemanticFact(scope *semanticScope, member string) (fhirsemantic.MemberFact, bool) {
	for _, fact := range scope.facts {
		if !fact.ArrayItem && fact.Element.Name == member {
			return fact, true
		}
	}
	return fhirsemantic.MemberFact{}, false
}

func semanticChoiceValueFromFact(resourceType string, fact fhirsemantic.MemberFact, registry *fhirsemantic.DatatypeRegistry) (semanticChoiceValue, bool) {
	value, ok := decodeSemanticValue(fact.RawJSON)
	if !ok {
		return semanticChoiceValue{}, false
	}
	selector := fact.Element.Name
	valueType := semanticGeneratedValueType(resourceType, fact.CanonicalPath, value, semanticValueType(value))
	status := "SUPPORTED"
	presentation := ""
	if object, structured := value.(map[string]any); structured {
		descriptor, described := registry.Lookup(fact.ReferencedType)
		projection := descriptor.ScalarProjection
		if described && projection != nil {
			selector = appendSemanticPath(selector, projection.Path)
			valueType = semanticGeneratedValueType(resourceType, appendSemanticPath(fact.CanonicalPath, projection.Path), value, projection.LogicalType)
			presentation = projection.Presentation
			if presentation == schema.ValuePresentationDisplayOrCode {
				codings, _ := object["coding"].([]any)
				if len(codings) == 0 {
					selector = appendSemanticPath(fact.Element.Name, "text")
					presentation = ""
					status = semanticProjectionStatus(object, "text")
				}
			} else {
				status = semanticProjectionStatus(object, projection.Path)
			}
		} else {
			valueType = "object"
		}
	}
	if selector == "" {
		return semanticChoiceValue{}, false
	}
	arm := ""
	if fact.Element.ChoiceGroup != "" {
		arm = fact.Element.Name
	}
	return semanticChoiceValue{
		Arm:          arm,
		Selector:     selector,
		Type:         valueType,
		Status:       status,
		Value:        value,
		Presentation: presentation,
	}, true
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
