package semantic

import (
	"container/heap"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/loom/internal/fhir/schema"
)

type WalkDiagnosticCode string

const (
	WalkUnknownMember          WalkDiagnosticCode = "UNKNOWN_MEMBER"
	WalkMissingRequiredElement WalkDiagnosticCode = "MISSING_REQUIRED_ELEMENT"
	WalkMissingRequiredChoice  WalkDiagnosticCode = "MISSING_REQUIRED_CHOICE"
	WalkUnresolvedResourceType WalkDiagnosticCode = "UNRESOLVED_RESOURCE_TYPE"
)

const (
	WalkMembersWithResourceTypesMaxFacts       = 100_000
	WalkMembersWithResourceTypesMaxDiagnostics = 4_096
	WalkMembersWithResourceTypesMaxDepth       = 128
)

type WalkLimitKind string

const (
	WalkLimitVisitedFacts WalkLimitKind = "VISITED_FACTS"
	WalkLimitDiagnostics  WalkLimitKind = "DIAGNOSTICS"
	WalkLimitDepth        WalkLimitKind = "DEPTH"
)

type WalkLimitError struct {
	Kind  WalkLimitKind
	Limit int
}

func (e *WalkLimitError) Error() string {
	return fmt.Sprintf("walk FHIR members: %s limit %d reached", e.Kind, e.Limit)
}

type WalkDiagnostic struct {
	Code          WalkDiagnosticCode
	CanonicalPath string
	OwnerPath     string
	Member        string
	ChoiceGroup   string
}

type ResourceBoundary struct {
	CanonicalPath string
	OwnerPath     string
	ResourceType  schema.ResourceType
}

type MemberFact struct {
	RootDefinition     schema.DefinitionName
	DeclaringType      schema.DefinitionName
	ReferencedType     schema.DefinitionName
	CanonicalPath      string
	OwnerPath          string
	RepeatedBoundaries []RepeatedBoundary
	ResourceBoundaries []ResourceBoundary
	Element            schema.Element
	RawJSON            json.RawMessage
	Repeated           bool
	ArrayItem          bool
	ItemIndex          *int
	Null               bool
	Value              any
}

func WalkMembers(
	index *schema.Index,
	root schema.DefinitionName,
	payload []byte,
	visit func(MemberFact) error,
) ([]WalkDiagnostic, error) {
	return walkMembers(index, root, payload, visit, memberWalkOptions{})
}

func WalkMemberValues(
	index *schema.Index,
	root schema.DefinitionName,
	payload map[string]any,
	visit func(MemberFact) error,
) ([]WalkDiagnostic, error) {
	if payload == nil {
		return nil, fmt.Errorf("walk FHIR members for %q: expected an object", root)
	}
	return walkMemberTree(index, root, decodedMemberObject(payload), visit, memberWalkOptions{})
}

func WalkMemberValuesWithResourceTypes(
	index *schema.Index,
	root schema.DefinitionName,
	payload map[string]any,
	allowed []schema.ResourceType,
	visit func(MemberFact) error,
) ([]WalkDiagnostic, error) {
	if payload == nil {
		return nil, fmt.Errorf("walk FHIR members for %q: expected an object", root)
	}
	allowedTypes, err := validateAllowedResourceTypes(root, allowed)
	if err != nil {
		return nil, err
	}
	return walkMemberTree(index, root, decodedMemberObject(payload), visit, memberWalkOptions{
		allowedResourceTypes: allowedTypes,
		limits: &memberWalkLimits{
			maxFacts:       WalkMembersWithResourceTypesMaxFacts,
			maxDiagnostics: WalkMembersWithResourceTypesMaxDiagnostics,
			maxDepth:       WalkMembersWithResourceTypesMaxDepth,
		},
	})
}

func WalkMembersWithResourceTypes(
	index *schema.Index,
	root schema.DefinitionName,
	payload []byte,
	allowed []schema.ResourceType,
	visit func(MemberFact) error,
) ([]WalkDiagnostic, error) {
	return walkMembersWithResourceTypesAndLimits(
		index,
		root,
		payload,
		allowed,
		visit,
		memberWalkLimits{
			maxFacts:       WalkMembersWithResourceTypesMaxFacts,
			maxDiagnostics: WalkMembersWithResourceTypesMaxDiagnostics,
			maxDepth:       WalkMembersWithResourceTypesMaxDepth,
		},
	)
}

type memberWalkOptions struct {
	allowedResourceTypes map[string]struct{}
	limits               *memberWalkLimits
}

type memberWalkLimits struct {
	maxFacts       int
	maxDiagnostics int
	maxDepth       int
}

type memberWalkState struct {
	options      memberWalkOptions
	visitedFacts int
	limitErr     *WalkLimitError
}

type memberScopeCacheKey struct {
	first         schema.ElementView
	elementCount  int
	requiredNames string
}

type memberScopePlan struct {
	declared      map[string]schema.ElementView
	requiredOrder []string
	choiceGroups  map[string][]string
	choiceOrder   []string
}

const maxMemberScopePlans = 8192

var memberScopePlans sync.Map
var memberScopePlanOrder = struct {
	sync.Mutex
	keys []memberScopeCacheKey
	next int
}{keys: make([]memberScopeCacheKey, 0, maxMemberScopePlans)}

func walkMembersWithResourceTypesAndLimits(
	index *schema.Index,
	root schema.DefinitionName,
	payload []byte,
	allowed []schema.ResourceType,
	visit func(MemberFact) error,
	limits memberWalkLimits,
) ([]WalkDiagnostic, error) {
	allowedTypes, err := validateAllowedResourceTypes(root, allowed)
	if err != nil {
		return nil, err
	}
	return walkMembers(index, root, payload, visit, memberWalkOptions{
		allowedResourceTypes: allowedTypes,
		limits:               &limits,
	})
}

func validateAllowedResourceTypes(root schema.DefinitionName, allowed []schema.ResourceType) (map[string]struct{}, error) {
	if len(allowed) == 0 {
		return nil, fmt.Errorf("walk FHIR members for %q: allowed resource types are empty", root)
	}
	registry := schema.ResourceTypes()
	permitted := make(map[string]struct{}, len(allowed))
	for _, resourceType := range allowed {
		value := string(resourceType)
		position := sort.SearchStrings(registry, value)
		if position == len(registry) || registry[position] != value {
			return nil, fmt.Errorf("walk FHIR members for %q: allowed resource type %q is not a canonical generated resource type", root, value)
		}
		if _, exists := permitted[value]; exists {
			return nil, fmt.Errorf("walk FHIR members for %q: allowed resource type %q is duplicated", root, value)
		}
		permitted[value] = struct{}{}
	}
	return permitted, nil
}

func walkMembers(
	index *schema.Index,
	root schema.DefinitionName,
	payload []byte,
	visit func(MemberFact) error,
	options memberWalkOptions,
) ([]WalkDiagnostic, error) {
	object, err := decodeObject(payload, "root")
	if err != nil {
		return nil, err
	}
	return walkMemberTree(index, root, rawMemberObject(object), visit, options)
}

func walkMemberTree(
	index *schema.Index,
	root schema.DefinitionName,
	object memberObject,
	visit func(MemberFact) error,
	options memberWalkOptions,
) ([]WalkDiagnostic, error) {
	if index == nil {
		return nil, fmt.Errorf("walk FHIR members for %q: schema index is nil", root)
	}
	if visit == nil {
		return nil, fmt.Errorf("walk FHIR members for %q: visitor is nil", root)
	}
	definition, ok := index.ReadDefinition(root)
	if !ok {
		return nil, fmt.Errorf("walk FHIR members for %q: definition is not in the schema index", root)
	}
	diagnostics := make([]WalkDiagnostic, 0)
	state := &memberWalkState{options: options}
	err := walkMemberScope(
		index, root, root, definition.Elements(), definition.RequiredElements(),
		object, "", "", nil, nil, 0, visit, &diagnostics, state,
	)
	if err != nil {
		if state.limitErr != nil && err == state.limitErr {
			return diagnostics, err
		}
		return nil, err
	}
	return diagnostics, nil
}

func walkMemberScope(
	index *schema.Index,
	root schema.DefinitionName,
	declaringType schema.DefinitionName,
	elements schema.ElementListView,
	requiredNames []string,
	object memberObject,
	canonicalPrefix string,
	ownerPrefix string,
	repeated []RepeatedBoundary,
	resources []ResourceBoundary,
	depth int,
	visit func(MemberFact) error,
	diagnostics *[]WalkDiagnostic,
	state *memberWalkState,
) error {
	if state.options.limits != nil && depth > state.options.limits.maxDepth {
		return state.newLimitError(WalkLimitDepth, state.options.limits.maxDepth)
	}
	plan, err := cachedMemberScopePlan(elements, requiredNames, root)
	if err != nil {
		return err
	}

	unknown := unknownMemberNames(object, plan.declared, len(*diagnostics), state)
	for _, name := range unknown {
		if err := appendWalkDiagnostic(diagnostics, WalkDiagnostic{
			Code:          WalkUnknownMember,
			CanonicalPath: joinPath(canonicalPrefix, name),
			OwnerPath:     joinPath(ownerPrefix, name),
			Member:        name,
		}, state); err != nil {
			return err
		}
	}

	for _, name := range plan.requiredOrder {
		value, ok := object.member(name)
		if ok && !value.isNull() || !ok && depth == 0 && name == "resourceType" {
			continue
		}
		canonicalPath := joinPath(canonicalPrefix, name)
		if element, found := plan.declared[name]; found && element.JSONType() == schema.JSONTypeArray {
			canonicalPath += "[]"
		}
		if err := appendWalkDiagnostic(diagnostics, WalkDiagnostic{
			Code:          WalkMissingRequiredElement,
			CanonicalPath: canonicalPath,
			OwnerPath:     joinPath(ownerPrefix, name),
			Member:        name,
		}, state); err != nil {
			return err
		}
	}
	for _, group := range plan.choiceOrder {
		present := false
		for _, arm := range plan.choiceGroups[group] {
			if value, ok := object.member(arm); ok && !value.isNull() {
				present = true
				break
			}
		}
		if !present {
			path := canonicalPrefix
			if path == "" {
				path = "$"
			}
			owner := ownerPrefix
			if owner == "" {
				owner = "root"
			}
			if err := appendWalkDiagnostic(diagnostics, WalkDiagnostic{
				Code:          WalkMissingRequiredChoice,
				CanonicalPath: path,
				OwnerPath:     owner,
				ChoiceGroup:   group,
			}, state); err != nil {
				return err
			}
		}
	}

	for elementIndex := 0; elementIndex < elements.Len(); elementIndex++ {
		element, _ := elements.At(elementIndex)
		value, exists := object.member(element.Name())
		if !exists {
			continue
		}
		if err := visitMemberValue(index, root, declaringType, element, value, canonicalPrefix, ownerPrefix, repeated, resources, depth, visit, diagnostics, state); err != nil {
			return err
		}
	}
	return nil
}

func cachedMemberScopePlan(
	elements schema.ElementListView,
	requiredNames []string,
	root schema.DefinitionName,
) (*memberScopePlan, error) {
	first, elementCount := elements.Identity()
	key := memberScopeCacheKey{first: first, elementCount: elementCount, requiredNames: requiredNamesIdentity(requiredNames)}
	if cached, ok := memberScopePlans.Load(key); ok {
		return cached.(*memberScopePlan), nil
	}
	plan := &memberScopePlan{
		declared:     make(map[string]schema.ElementView, elements.Len()),
		choiceGroups: make(map[string][]string),
	}
	required := make(map[string]struct{}, len(requiredNames)+elements.Len())
	for elementIndex := 0; elementIndex < elements.Len(); elementIndex++ {
		element, ok := elements.At(elementIndex)
		if !ok {
			return nil, fmt.Errorf("walk FHIR members for %q: schema element index %d is unavailable", root, elementIndex)
		}
		plan.declared[element.Name()] = element
	}
	for _, name := range requiredNames {
		required[name] = struct{}{}
	}
	for elementIndex := 0; elementIndex < elements.Len(); elementIndex++ {
		element, _ := elements.At(elementIndex)
		metadata := element.Snapshot()
		if metadata.ElementRequired && metadata.ChoiceGroup == "" {
			required[element.Name()] = struct{}{}
		}
		if metadata.ChoiceGroup != "" && metadata.ChoiceGroupRequired {
			plan.choiceGroups[metadata.ChoiceGroup] = append(plan.choiceGroups[metadata.ChoiceGroup], element.Name())
		}
	}
	plan.requiredOrder = make([]string, 0, len(required))
	for name := range required {
		plan.requiredOrder = append(plan.requiredOrder, name)
	}
	sort.Strings(plan.requiredOrder)
	plan.choiceOrder = make([]string, 0, len(plan.choiceGroups))
	for group := range plan.choiceGroups {
		plan.choiceOrder = append(plan.choiceOrder, group)
	}
	sort.Strings(plan.choiceOrder)
	actual, loaded := memberScopePlans.LoadOrStore(key, plan)
	if !loaded {
		memberScopePlanOrder.Lock()
		if len(memberScopePlanOrder.keys) < maxMemberScopePlans {
			memberScopePlanOrder.keys = append(memberScopePlanOrder.keys, key)
		} else {
			memberScopePlans.Delete(memberScopePlanOrder.keys[memberScopePlanOrder.next])
			memberScopePlanOrder.keys[memberScopePlanOrder.next] = key
			memberScopePlanOrder.next = (memberScopePlanOrder.next + 1) % maxMemberScopePlans
		}
		memberScopePlanOrder.Unlock()
	}
	return actual.(*memberScopePlan), nil
}

func requiredNamesIdentity(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return "\x01" + names[0]
	default:
		var builder strings.Builder
		builder.WriteByte(2)
		var length [binary.MaxVarintLen64]byte
		for _, name := range names {
			encoded := binary.PutUvarint(length[:], uint64(len(name)))
			builder.Write(length[:encoded])
			builder.WriteString(name)
		}
		return builder.String()
	}
}

func visitMemberValue(
	index *schema.Index,
	root schema.DefinitionName,
	declaringType schema.DefinitionName,
	element schema.ElementView,
	value memberValue,
	canonicalPrefix string,
	ownerPrefix string,
	repeated []RepeatedBoundary,
	resources []ResourceBoundary,
	depth int,
	visit func(MemberFact) error,
	diagnostics *[]WalkDiagnostic,
	state *memberWalkState,
) error {
	metadata := element.Snapshot()
	memberPath := joinPath(canonicalPrefix, element.Name())
	ownerPath := joinPath(ownerPrefix, element.Name())
	if element.JSONType() != schema.JSONTypeArray {
		fact := memberFact(root, declaringType, element.ReferencedType(), memberPath, ownerPath, repeated, metadata, value)
		fact.ResourceBoundaries = cloneResourceBoundaries(resources)
		referencedType := element.ReferencedType()
		if !fact.Null && resourceUnion(referencedType) && state.options.allowedResourceTypes != nil {
			selectedType, boundary, resolved, err := resolveResourceType(index, value, memberPath, ownerPath, state.options.allowedResourceTypes)
			if err != nil {
				return err
			}
			if resolved {
				fact.ResourceBoundaries = appendResourceBoundary(resources, boundary)
				referencedType = schema.DefinitionName(selectedType)
			} else if err := appendUnresolvedResourceDiagnostic(diagnostics, memberPath, ownerPath, state); err != nil {
				return err
			}
		}
		if err := visitMemberFact(fact, visit, state); err != nil {
			return err
		}
		if fact.Null || !memberIsObject(element) {
			return nil
		}
		object, err := value.object(ownerPath)
		if err != nil {
			return err
		}
		return walkObjectMembers(index, root, declaringType, referencedType, element.Elements(), metadata.RequiredChildren, object, memberPath, ownerPath, repeated, fact.ResourceBoundaries, depth+1, visit, diagnostics, state)
	}

	canonicalPath := memberPath + "[]"
	container := memberFact(root, declaringType, element.ArrayElementType(), canonicalPath, ownerPath, repeated, metadata, value)
	container.Repeated = true
	container.ResourceBoundaries = cloneResourceBoundaries(resources)
	if err := visitMemberFact(container, visit, state); err != nil {
		return err
	}
	if container.Null {
		return nil
	}
	items, err := value.array(ownerPath)
	if err != nil {
		return err
	}
	for itemIndex, item := range items {
		itemOwnerPath := ownerPath + "[" + fmt.Sprint(itemIndex) + "]"
		boundaries := appendRepeatedBoundary(repeated, RepeatedBoundary{
			CanonicalPath: canonicalPath,
			OwnerPath:     itemOwnerPath,
			Index:         itemIndex,
		})
		indexCopy := itemIndex
		fact := memberFact(root, declaringType, element.ArrayElementType(), canonicalPath, itemOwnerPath, boundaries, metadata, item)
		fact.Repeated = true
		fact.ArrayItem = true
		fact.ItemIndex = &indexCopy
		fact.ResourceBoundaries = cloneResourceBoundaries(resources)
		itemType := element.ArrayElementType()
		if !fact.Null && resourceUnion(itemType) && state.options.allowedResourceTypes != nil {
			selectedType, boundary, resolved, err := resolveResourceType(index, item, canonicalPath, itemOwnerPath, state.options.allowedResourceTypes)
			if err != nil {
				return err
			}
			if resolved {
				fact.ResourceBoundaries = appendResourceBoundary(resources, boundary)
				itemType = schema.DefinitionName(selectedType)
			} else if err := appendUnresolvedResourceDiagnostic(diagnostics, canonicalPath, itemOwnerPath, state); err != nil {
				return err
			}
		}
		if err := visitMemberFact(fact, visit, state); err != nil {
			return err
		}
		if fact.Null || !memberArrayItemIsObject(element) {
			continue
		}
		object, err := item.object(itemOwnerPath)
		if err != nil {
			return err
		}
		if err := walkObjectMembers(index, root, declaringType, itemType, element.ArrayElements(), metadata.RequiredChildren, object, canonicalPath, itemOwnerPath, boundaries, fact.ResourceBoundaries, depth+1, visit, diagnostics, state); err != nil {
			return err
		}
	}
	return nil
}

func resourceUnion(name schema.DefinitionName) bool {
	return name == "ResourceList" || name == "Resource"
}

func walkObjectMembers(
	index *schema.Index,
	root schema.DefinitionName,
	parentDeclaringType schema.DefinitionName,
	referencedType schema.DefinitionName,
	inlineElements schema.ElementListView,
	requiredChildren []string,
	object memberObject,
	canonicalPrefix string,
	ownerPrefix string,
	repeated []RepeatedBoundary,
	resources []ResourceBoundary,
	depth int,
	visit func(MemberFact) error,
	diagnostics *[]WalkDiagnostic,
	state *memberWalkState,
) error {
	declaringType := parentDeclaringType
	elements := inlineElements
	required := append([]string(nil), requiredChildren...)
	if referencedType != "" {
		definition, ok := index.ReadDefinition(referencedType)
		if !ok {
			return fmt.Errorf("walk FHIR path %q: referenced definition %q is missing from the schema index", ownerPrefix, referencedType)
		}
		declaringType = referencedType
		elements = definition.Elements()
		required = append(required, definition.RequiredElements()...)
	}
	return walkMemberScope(index, root, declaringType, elements, required, object, canonicalPrefix, ownerPrefix, repeated, resources, depth, visit, diagnostics, state)
}

func memberFact(
	root schema.DefinitionName,
	declaringType schema.DefinitionName,
	referencedType schema.DefinitionName,
	canonicalPath string,
	ownerPath string,
	repeated []RepeatedBoundary,
	element schema.Element,
	value memberValue,
) MemberFact {
	return MemberFact{
		RootDefinition:     root,
		DeclaringType:      declaringType,
		ReferencedType:     referencedType,
		CanonicalPath:      canonicalPath,
		OwnerPath:          ownerPath,
		RepeatedBoundaries: cloneRepeatedBoundaries(repeated),
		ResourceBoundaries: nil,
		Element:            element,
		RawJSON:            append(json.RawMessage(nil), value.raw...),
		Null:               value.isNull(),
		Value:              value.decoded,
	}
}

func visitMemberFact(fact MemberFact, visit func(MemberFact) error, state *memberWalkState) error {
	if limits := state.options.limits; limits != nil && state.visitedFacts >= limits.maxFacts {
		return state.newLimitError(WalkLimitVisitedFacts, limits.maxFacts)
	}
	if err := visit(fact); err != nil {
		return err
	}
	state.visitedFacts++
	return nil
}

func appendWalkDiagnostic(diagnostics *[]WalkDiagnostic, diagnostic WalkDiagnostic, state *memberWalkState) error {
	if limits := state.options.limits; limits != nil && len(*diagnostics) >= limits.maxDiagnostics {
		return state.newLimitError(WalkLimitDiagnostics, limits.maxDiagnostics)
	}
	*diagnostics = append(*diagnostics, diagnostic)
	return nil
}

func (state *memberWalkState) newLimitError(kind WalkLimitKind, limit int) *WalkLimitError {
	state.limitErr = &WalkLimitError{Kind: kind, Limit: limit}
	return state.limitErr
}

type memberObject interface {
	member(string) (memberValue, bool)
	forEachName(func(string))
}

type rawMemberObject map[string]json.RawMessage

func (object rawMemberObject) member(name string) (memberValue, bool) {
	raw, ok := object[name]
	return memberValue{raw: raw}, ok
}

func (object rawMemberObject) forEachName(visit func(string)) {
	for name := range object {
		visit(name)
	}
}

type decodedMemberObject map[string]any

func (object decodedMemberObject) member(name string) (memberValue, bool) {
	value, ok := object[name]
	return memberValue{decoded: value, isDecoded: true}, ok
}

func (object decodedMemberObject) forEachName(visit func(string)) {
	for name := range object {
		visit(name)
	}
}

type memberValue struct {
	raw       json.RawMessage
	decoded   any
	isDecoded bool
}

func (value memberValue) decodedValue() (any, bool) {
	return value.decoded, value.isDecoded
}

func (value memberValue) isNull() bool {
	if !value.isDecoded {
		return isNull(value.raw)
	}
	switch decoded := value.decoded.(type) {
	case nil:
		return true
	case map[string]any:
		return decoded == nil
	case []any:
		return decoded == nil
	default:
		return false
	}
}

func (value memberValue) object(path string) (memberObject, error) {
	if value.isDecoded {
		object, ok := value.decoded.(map[string]any)
		if !ok || object == nil {
			return nil, fmt.Errorf("walk FHIR path %q: expected an object", path)
		}
		return decodedMemberObject(object), nil
	}
	object, err := decodeObject(value.raw, path)
	if err != nil {
		return nil, err
	}
	return rawMemberObject(object), nil
}

func (value memberValue) array(path string) ([]memberValue, error) {
	if value.isDecoded {
		items, ok := value.decoded.([]any)
		if !ok {
			return nil, fmt.Errorf("walk FHIR path %q: decode array: expected an array", path)
		}
		values := make([]memberValue, len(items))
		for index, item := range items {
			values[index] = memberValue{decoded: item, isDecoded: true}
		}
		return values, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(value.raw, &items); err != nil {
		return nil, fmt.Errorf("walk FHIR path %q: decode array: %w", path, err)
	}
	values := make([]memberValue, len(items))
	for index, item := range items {
		values[index] = memberValue{raw: item}
	}
	return values, nil
}

func unknownMemberNames(object memberObject, declared map[string]schema.ElementView, diagnosticCount int, state *memberWalkState) []string {
	if state.options.limits == nil {
		unknown := make([]string, 0)
		object.forEachName(func(name string) {
			if _, ok := declared[name]; !ok {
				unknown = append(unknown, name)
			}
		})
		sort.Strings(unknown)
		return unknown
	}

	capacity := state.options.limits.maxDiagnostics - diagnosticCount + 1
	if capacity < 1 {
		capacity = 1
	}
	unknown := make(stringMaxHeap, 0, capacity)
	object.forEachName(func(name string) {
		if _, ok := declared[name]; ok {
			return
		}
		if len(unknown) < capacity {
			heap.Push(&unknown, name)
			return
		}
		if name < unknown[0] {
			unknown[0] = name
			heap.Fix(&unknown, 0)
		}
	})
	sort.Strings(unknown)
	return unknown
}

type stringMaxHeap []string

func (values stringMaxHeap) Len() int           { return len(values) }
func (values stringMaxHeap) Less(i, j int) bool { return values[i] > values[j] }
func (values stringMaxHeap) Swap(i, j int)      { values[i], values[j] = values[j], values[i] }
func (values *stringMaxHeap) Push(value any)    { *values = append(*values, value.(string)) }
func (values *stringMaxHeap) Pop() any {
	old := *values
	last := len(old) - 1
	value := old[last]
	*values = old[:last]
	return value
}

func appendUnresolvedResourceDiagnostic(diagnostics *[]WalkDiagnostic, canonicalPath, ownerPath string, state *memberWalkState) error {
	return appendWalkDiagnostic(diagnostics, WalkDiagnostic{
		Code:          WalkUnresolvedResourceType,
		CanonicalPath: canonicalPath,
		OwnerPath:     ownerPath,
		Member:        "resourceType",
	}, state)
}

func resolveResourceType(
	index *schema.Index,
	value memberValue,
	canonicalPath string,
	ownerPath string,
	allowed map[string]struct{},
) (string, ResourceBoundary, bool, error) {
	object, err := value.object(ownerPath)
	if err != nil {
		return "", ResourceBoundary{}, false, err
	}
	var discriminator any
	resourceValue, exists := object.member("resourceType")
	if !exists {
		return "", ResourceBoundary{}, false, nil
	}
	discriminator, ok := resourceValue.decodedValue()
	if !ok {
		if err := json.Unmarshal(resourceValue.raw, &discriminator); err != nil {
			return "", ResourceBoundary{}, false, nil
		}
	}
	resourceType, ok := discriminator.(string)
	if !ok {
		return "", ResourceBoundary{}, false, nil
	}
	if _, permitted := allowed[resourceType]; !permitted {
		return "", ResourceBoundary{}, false, nil
	}
	if _, exists := index.ReadDefinition(schema.DefinitionName(resourceType)); !exists {
		return "", ResourceBoundary{}, false, nil
	}
	return resourceType, ResourceBoundary{
		CanonicalPath: canonicalPath,
		OwnerPath:     ownerPath,
		ResourceType:  schema.ResourceType(resourceType),
	}, true, nil
}

func appendResourceBoundary(boundaries []ResourceBoundary, next ResourceBoundary) []ResourceBoundary {
	copyOfBoundaries := make([]ResourceBoundary, len(boundaries)+1)
	copy(copyOfBoundaries, boundaries)
	copyOfBoundaries[len(boundaries)] = next
	return copyOfBoundaries
}

func cloneResourceBoundaries(boundaries []ResourceBoundary) []ResourceBoundary {
	return append([]ResourceBoundary(nil), boundaries...)
}

func memberIsObject(element schema.ElementView) bool {
	return element.JSONType() == schema.JSONTypeObject || element.ReferencedType() != "" || element.Elements().Len() > 0
}

func memberArrayItemIsObject(element schema.ElementView) bool {
	return element.ArrayElementType() != "" || element.ItemJSONType() == schema.JSONTypeObject || element.ArrayElements().Len() > 0
}
