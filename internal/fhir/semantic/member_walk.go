package semantic

import (
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
)

// WalkDiagnostic identifies one structural mismatch at a concrete owner.
type WalkDiagnostic struct {
	Code          WalkDiagnosticCode
	CanonicalPath string
	OwnerPath     string
	Member        string
	ChoiceGroup   string
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
		visit,
		&diagnostics,
	)
	if err != nil {
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
	visit func(MemberFact) error,
	diagnostics *[]WalkDiagnostic,
) error {
	declared := make(map[string]schema.ElementView, elements.Len())
	for elementIndex := 0; elementIndex < elements.Len(); elementIndex++ {
		element, ok := elements.At(elementIndex)
		if !ok {
			return fmt.Errorf("walk FHIR members for %q: schema element index %d is unavailable", root, elementIndex)
		}
		declared[element.Name()] = element
	}

	unknown := make([]string, 0)
	for name := range object {
		if _, ok := declared[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		*diagnostics = append(*diagnostics, WalkDiagnostic{
			Code:          WalkUnknownMember,
			CanonicalPath: joinPath(canonicalPrefix, name),
			OwnerPath:     joinPath(ownerPrefix, name),
			Member:        name,
		})
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
		*diagnostics = append(*diagnostics, WalkDiagnostic{
			Code:          WalkMissingRequiredElement,
			CanonicalPath: canonicalPath,
			OwnerPath:     joinPath(ownerPrefix, name),
			Member:        name,
		})
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
			*diagnostics = append(*diagnostics, WalkDiagnostic{
				Code:          WalkMissingRequiredChoice,
				CanonicalPath: path,
				OwnerPath:     owner,
				ChoiceGroup:   group,
			})
		}
	}

	for elementIndex := 0; elementIndex < elements.Len(); elementIndex++ {
		element, _ := elements.At(elementIndex)
		value, exists := object[element.Name()]
		if !exists {
			continue
		}
		if err := visitMemberValue(index, root, declaringType, element, value, canonicalPrefix, ownerPrefix, repeated, visit, diagnostics); err != nil {
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
	visit func(MemberFact) error,
	diagnostics *[]WalkDiagnostic,
) error {
	metadata := element.Snapshot()
	memberPath := joinPath(canonicalPrefix, element.Name())
	ownerPath := joinPath(ownerPrefix, element.Name())
	if element.JSONType() != schema.JSONTypeArray {
		fact := memberFact(root, declaringType, element.ReferencedType(), memberPath, ownerPath, repeated, metadata, value)
		if err := visit(fact); err != nil {
			return err
		}
		if fact.Null || !memberIsObject(element) {
			return nil
		}
		object, err := decodeObject(value, ownerPath)
		if err != nil {
			return err
		}
		return walkObjectMembers(index, root, declaringType, element.ReferencedType(), element.Elements(), metadata.RequiredChildren, object, memberPath, ownerPath, repeated, visit, diagnostics)
	}

	canonicalPath := memberPath + "[]"
	container := memberFact(root, declaringType, element.ArrayElementType(), canonicalPath, ownerPath, repeated, metadata, value)
	container.Repeated = true
	if err := visit(container); err != nil {
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
		if err := visit(fact); err != nil {
			return err
		}
		if fact.Null || !memberArrayItemIsObject(element) {
			continue
		}
		object, err := decodeObject(item, itemOwnerPath)
		if err != nil {
			return err
		}
		if err := walkObjectMembers(index, root, declaringType, element.ArrayElementType(), element.ArrayElements(), metadata.RequiredChildren, object, canonicalPath, itemOwnerPath, boundaries, visit, diagnostics); err != nil {
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
	visit func(MemberFact) error,
	diagnostics *[]WalkDiagnostic,
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
	return walkMemberScope(index, root, declaringType, elements, required, object, canonicalPrefix, ownerPrefix, repeated, visit, diagnostics)
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
		Element:            element,
		RawJSON:            append(json.RawMessage(nil), raw...),
		Null:               isNull(raw),
	}
}

func memberIsObject(element schema.ElementView) bool {
	return element.JSONType() == schema.JSONTypeObject || element.ReferencedType() != "" || element.Elements().Len() > 0
}

func memberArrayItemIsObject(element schema.ElementView) bool {
	return element.ArrayElementType() != "" || element.ItemJSONType() == schema.JSONTypeObject || element.ArrayElements().Len() > 0
}
