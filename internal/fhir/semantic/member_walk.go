package semantic

import (
	"container/heap"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/calypr/loom/internal/fhir/schema"
)

// WalkDiagnosticCode is a closed classification of payload/schema drift.
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

// WalkLimitKind classifies a hard stop in the resource-aware member walker.
type WalkLimitKind string

const (
	WalkLimitVisitedFacts WalkLimitKind = "VISITED_FACTS"
	WalkLimitDiagnostics  WalkLimitKind = "DIAGNOSTICS"
	WalkLimitDepth        WalkLimitKind = "DEPTH"
)

// WalkLimitError reports a hard limit reached by WalkMembersWithResourceTypes.
type WalkLimitError struct {
	Kind  WalkLimitKind
	Limit int
}

func (e *WalkLimitError) Error() string {
	return fmt.Sprintf("walk FHIR members: %s limit %d reached", e.Kind, e.Limit)
}

// WalkDiagnostic identifies one structural mismatch at a concrete owner.
type WalkDiagnostic struct {
	Code          WalkDiagnosticCode
	CanonicalPath string
	OwnerPath     string
	Member        string
	ChoiceGroup   string
}

// ResourceBoundary records one concrete abstract Resource reference resolved
// to an allowed concrete resource type.
type ResourceBoundary struct {
	CanonicalPath string
	OwnerPath     string
	ResourceType  schema.ResourceType
}

// MemberFact is one present member or repeated item resolved through the
// generated schema. RawJSON is an owned copy of the exact source value.
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
}

// WalkMembers visits every present schema member and repeated item while
// returning structural diagnostics. Named references are resolved only for
// concrete values in the payload, so recursive definitions remain finite.
func WalkMembers(
	index *schema.Index,
	root schema.DefinitionName,
	payload []byte,
	visit func(MemberFact) error,
) ([]WalkDiagnostic, error) {
	return walkMembers(index, root, payload, visit, memberWalkOptions{})
}

// WalkMembersWithResourceTypes walks present members and resolves schema-
// declared abstract Resource references only when their exact resourceType is
// permitted by allowed and represented in index.
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
	object, err := decodeObject(payload, "root")
	if err != nil {
		return nil, err
	}
	diagnostics := make([]WalkDiagnostic, 0)
	state := &memberWalkState{options: options}
	err = walkMemberScope(
		index,
		root,
		root,
		definition.Elements(),
		definition.RequiredElements(),
		object,
		"",
		"",
		nil,
		nil,
		0,
		visit,
		&diagnostics,
		state,
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
	object map[string]json.RawMessage,
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
	declared := make(map[string]schema.ElementView, elements.Len())
	for elementIndex := 0; elementIndex < elements.Len(); elementIndex++ {
		element, ok := elements.At(elementIndex)
		if !ok {
			return fmt.Errorf("walk FHIR members for %q: schema element index %d is unavailable", root, elementIndex)
		}
		declared[element.Name()] = element
	}

	unknown := unknownMemberNames(object, declared, len(*diagnostics), state)
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

	required := make(map[string]struct{}, len(requiredNames)+elements.Len())
	for _, name := range requiredNames {
		required[name] = struct{}{}
	}
	choiceGroups := make(map[string][]string)
	for elementIndex := 0; elementIndex < elements.Len(); elementIndex++ {
		element, _ := elements.At(elementIndex)
		metadata := element.Snapshot()
		if metadata.ElementRequired && metadata.ChoiceGroup == "" {
			required[element.Name()] = struct{}{}
		}
		if metadata.ChoiceGroup != "" && metadata.ChoiceGroupRequired {
			choiceGroups[metadata.ChoiceGroup] = append(choiceGroups[metadata.ChoiceGroup], element.Name())
		}
	}
	requiredOrder := make([]string, 0, len(required))
	for name := range required {
		requiredOrder = append(requiredOrder, name)
	}
	sort.Strings(requiredOrder)
	for _, name := range requiredOrder {
		value, ok := object[name]
		if ok && !isNull(value) {
			continue
		}
		canonicalPath := joinPath(canonicalPrefix, name)
		if element, found := declared[name]; found && element.JSONType() == schema.JSONTypeArray {
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
	groupNames := make([]string, 0, len(choiceGroups))
	for group := range choiceGroups {
		groupNames = append(groupNames, group)
	}
	sort.Strings(groupNames)
	for _, group := range groupNames {
		present := false
		for _, arm := range choiceGroups[group] {
			if value, ok := object[arm]; ok && !isNull(value) {
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
		value, exists := object[element.Name()]
		if !exists {
			continue
		}
		if err := visitMemberValue(index, root, declaringType, element, value, canonicalPrefix, ownerPrefix, repeated, resources, depth, visit, diagnostics, state); err != nil {
			return err
		}
	}
	return nil
}

func visitMemberValue(
	index *schema.Index,
	root schema.DefinitionName,
	declaringType schema.DefinitionName,
	element schema.ElementView,
	value json.RawMessage,
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
		if !fact.Null && referencedType == "Resource" && state.options.allowedResourceTypes != nil {
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
		object, err := decodeObject(value, ownerPath)
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
	var items []json.RawMessage
	if err := json.Unmarshal(value, &items); err != nil {
		return fmt.Errorf("walk FHIR path %q: decode array: %w", ownerPath, err)
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
		if !fact.Null && itemType == "Resource" && state.options.allowedResourceTypes != nil {
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
		object, err := decodeObject(item, itemOwnerPath)
		if err != nil {
			return err
		}
		if err := walkObjectMembers(index, root, declaringType, itemType, element.ArrayElements(), metadata.RequiredChildren, object, canonicalPath, itemOwnerPath, boundaries, fact.ResourceBoundaries, depth+1, visit, diagnostics, state); err != nil {
			return err
		}
	}
	return nil
}

func walkObjectMembers(
	index *schema.Index,
	root schema.DefinitionName,
	parentDeclaringType schema.DefinitionName,
	referencedType schema.DefinitionName,
	inlineElements schema.ElementListView,
	requiredChildren []string,
	object map[string]json.RawMessage,
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
	raw json.RawMessage,
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
		RawJSON:            append(json.RawMessage(nil), raw...),
		Null:               isNull(raw),
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

func unknownMemberNames(object map[string]json.RawMessage, declared map[string]schema.ElementView, diagnosticCount int, state *memberWalkState) []string {
	if state.options.limits == nil {
		unknown := make([]string, 0)
		for name := range object {
			if _, ok := declared[name]; !ok {
				unknown = append(unknown, name)
			}
		}
		sort.Strings(unknown)
		return unknown
	}

	// Keep only the lexicographically first remaining diagnostic names plus
	// one overflow witness, so a wide unknown object cannot grow scratch memory.
	capacity := state.options.limits.maxDiagnostics - diagnosticCount + 1
	if capacity < 1 {
		capacity = 1
	}
	unknown := make(stringMaxHeap, 0, capacity)
	for name := range object {
		if _, ok := declared[name]; ok {
			continue
		}
		if len(unknown) < capacity {
			heap.Push(&unknown, name)
			continue
		}
		if name < unknown[0] {
			unknown[0] = name
			heap.Fix(&unknown, 0)
		}
	}
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
	raw json.RawMessage,
	canonicalPath string,
	ownerPath string,
	allowed map[string]struct{},
) (string, ResourceBoundary, bool, error) {
	object, err := decodeObject(raw, ownerPath)
	if err != nil {
		return "", ResourceBoundary{}, false, err
	}
	var discriminator any
	value, exists := object["resourceType"]
	if !exists || json.Unmarshal(value, &discriminator) != nil {
		return "", ResourceBoundary{}, false, nil
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
