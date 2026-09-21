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
	SemanticRuleHintCategoricalCodeV1 = "CATEGORICAL_CODE_V1"
	SemanticRuleHintCategoricalTextV1 = "CATEGORICAL_TEXT_V1"
)

func (p *Profiler) observeSchemaSemantics(
	index *schema.Index,
	payload map[string]any,
	profile string,
	emit func(SemanticObservation, []any),
) error {
	registry, err := fhirsemantic.GeneratedDatatypeRegistry()
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
	choiceKeys := make(map[string]struct{})
	for _, scope := range scopes {
		concept, _, ok := codedChoiceParts(p.resourceType, scope, registry)
		if ok {
			choiceKeys[concept.CanonicalPath] = struct{}{}
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
		case fhirsemantic.DispositionCategorical:
			_, pairedChoiceKey := choiceKeys[scope.canonicalPath]
			if !pairedChoiceKey && !categoricalSourceOwnedByParent(scope, registry) {
				p.emitCategoricalScope(scope, descriptor, profile, emit)
			}
		}
	}
	for _, scope := range scopes {
		p.emitCodedChoiceScope(scope, registry, profile, emit)
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
				Selector: appendSemanticPath(scope.canonicalPath, value.Selector),
				Type:     value.Type,
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
	for _, candidate := range semanticCategoricalCandidates(p.resourceType, descriptor, scope.canonicalPath, scope.value) {
		observation := SemanticObservation{
			SchemaVersion: SemanticObservationSchemaVersion,
			Source: SemanticObservationSource{
				Canonical: semanticCanonical(p.resourceType, scope.canonicalPath),
				Type:      p.resourceType,
				Profile:   profile,
				Path:      scope.canonicalPath,
			},
			Key:          candidate.key,
			Value:        SemanticObservationValue{Selector: candidate.valueSelector, Type: candidate.logicalType},
			OwningScope:  scope.canonicalPath,
			LogicalType:  candidate.logicalType,
			Completeness: candidate.completeness,
			Status:       candidate.status,
			RuleHint:     candidate.ruleHint,
			RuleVersion:  strconv.Itoa(SemanticObservationRuleVersion),
		}
		p.recordSchemaSemanticObservation(scope.canonicalPath, observation, []any{candidate.value}, emit)
	}
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
	concept, values, ok := codedChoiceParts(p.resourceType, scope, registry)
	if !ok {
		return
	}
	conceptValue, ok := decodeSemanticObject(concept.RawJSON)
	if !ok {
		return
	}
	descriptor, ok := registry.Lookup(concept.ReferencedType)
	if !ok {
		return
	}
	conceptPath := relativeSemanticPath(scope.canonicalPath, concept.CanonicalPath)
	for _, candidate := range semanticCategoricalCandidates(p.resourceType, descriptor, conceptPath, conceptValue) {
		for _, value := range values {
			p.emitSchemaCodedValue(scope, candidate, value, profile, emit)
		}
	}
}

func codedChoiceParts(
	resourceType string,
	scope *semanticScope,
	registry *fhirsemantic.DatatypeRegistry,
) (fhirsemantic.MemberFact, []semanticChoiceValue, bool) {
	concepts := make([]fhirsemantic.MemberFact, 0, 1)
	valuesByGroup := make(map[string][]semanticChoiceValue)
	for _, fact := range scope.facts {
		if fact.ArrayItem {
			continue
		}
		if fact.Null {
			continue
		}
		if fact.Element.ChoiceGroup != "" {
			if value, ok := semanticChoiceValueFromFact(resourceType, fact, registry); ok {
				valuesByGroup[fact.Element.ChoiceGroup] = append(valuesByGroup[fact.Element.ChoiceGroup], value)
			}
			continue
		}
		if descriptor, ok := registry.Lookup(fact.ReferencedType); ok && descriptor.Disposition == fhirsemantic.DispositionCategorical && memberPathForRole(descriptor, fhirsemantic.MemberCategoryCode) != "" {
			concepts = append(concepts, fact)
		}
	}
	// One category container can pair with one choice group on the same owner.
	// If several groups exist, only the schema's conventional "value" group is
	// eligible; otherwise no relationship is inferred. Multiple present arms
	// in the selected group remain an explicitly mixed choice.
	if len(concepts) != 1 || len(valuesByGroup) == 0 {
		return fhirsemantic.MemberFact{}, nil, false
	}
	selectedGroup := ""
	if len(valuesByGroup) == 1 {
		for group := range valuesByGroup {
			selectedGroup = group
		}
	} else if len(valuesByGroup["value"]) > 0 {
		selectedGroup = "value"
	}
	values := valuesByGroup[selectedGroup]
	if selectedGroup == "" || len(values) == 0 {
		return fhirsemantic.MemberFact{}, nil, false
	}
	if len(values) > 1 {
		for index := range values {
			values[index].Status = "MIXED_CHOICE"
		}
	}
	return concepts[0], values, true
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
		Source: SemanticObservationSource{
			Canonical: semanticCanonical(p.resourceType, storagePath),
			Type:      p.resourceType,
			Profile:   profile,
			Path:      storagePath,
		},
		Key: candidate.key,
		Value: SemanticObservationValue{
			Selector: value.Selector,
			Type:     value.Type,
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
	if object, structured := value.(map[string]any); structured {
		descriptor, described := registry.Lookup(fact.ReferencedType)
		projection := descriptor.ScalarProjection
		if described && projection != nil {
			selector = appendSemanticPath(selector, projection.Path)
			valueType = semanticGeneratedValueType(resourceType, appendSemanticPath(fact.CanonicalPath, projection.Path), value, projection.LogicalType)
			status = semanticProjectionStatus(object, projection.Path)
		} else {
			valueType = "mixed"
		}
	}
	if selector == "" {
		return semanticChoiceValue{}, false
	}
	return semanticChoiceValue{
		Arm:      fact.Element.Name,
		Selector: selector,
		Type:     valueType,
		Status:   status,
		Value:    value,
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
