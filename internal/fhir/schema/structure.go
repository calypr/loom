package schema

import (
	"fmt"
	"sort"
)

// DefinitionStructure is a bounded, detached snapshot of one definition's
// declared JSON shape. Named references remain typed expansion edges instead
// of being recursively expanded, so recursive FHIR definitions cannot make
// the structure infinite.
type DefinitionStructure struct {
	Root             DefinitionName
	Title            string
	Description      string
	RequiredElements []string
	Members          []StructureMember
}

// StructureMember retains the complete generated metadata for one member.
// Members contains inline structure only. ReferencedType identifies the named
// definition a consumer may expand separately through Index.Structure.
type StructureMember struct {
	CanonicalPath  string
	Element        Element
	Repeated       bool
	ReferencedType DefinitionName
	Members        []StructureMember
}

// Structure returns a finite copy of one definition's declared structure.
func (i *Index) Structure(root DefinitionName) (DefinitionStructure, bool) {
	if i == nil {
		return DefinitionStructure{}, false
	}
	definition, ok := i.definitions[root]
	if !ok {
		return DefinitionStructure{}, false
	}
	return DefinitionStructure{
		Root:             root,
		Title:            definition.Title,
		Description:      definition.Description,
		RequiredElements: cloneStrings(definition.RequiredElements),
		Members:          structureMembers(definition.Elements, ""),
	}, true
}

func structureMembers(elements []Element, prefix string) []StructureMember {
	if len(elements) == 0 {
		return nil
	}
	members := make([]StructureMember, 0, len(elements))
	for _, element := range elements {
		path := element.Name
		if prefix != "" {
			path = prefix + "." + element.Name
		}
		repeated := element.JSONType == JSONTypeArray
		if repeated {
			path += "[]"
		}
		referencedType := element.ReferencedType
		children := element.Elements
		if repeated {
			referencedType = element.ArrayElementType
			children = element.ArrayElements
		}
		members = append(members, StructureMember{
			CanonicalPath:  path,
			Element:        cloneElement(element),
			Repeated:       repeated,
			ReferencedType: referencedType,
			Members:        structureMembers(children, path),
		})
	}
	return members
}

// RepeatedCodingPaths returns generated root-relative paths that end at a
// repeated Coding element. Named definitions are followed once per branch so
// recursive FHIR types cannot produce an unbounded list of paths.
func (i *Index) RepeatedCodingPaths(root DefinitionName) ([]string, error) {
	if i == nil {
		return nil, fmt.Errorf("schema index is unavailable")
	}
	definition, ok := i.definitions[root]
	if !ok {
		return nil, fmt.Errorf("schema definition %q is unavailable", root)
	}

	paths := make(map[string]struct{})
	var visit func([]Element, string, map[DefinitionName]bool) error
	var visitDefinition func(DefinitionName, string, map[DefinitionName]bool) error
	visitDefinition = func(name DefinitionName, prefix string, active map[DefinitionName]bool) error {
		if active[name] {
			return nil
		}
		current, exists := i.definitions[name]
		if !exists {
			return fmt.Errorf("schema definition %q is unavailable", name)
		}
		next := cloneDefinitionStack(active, name)
		return visit(current.Elements, prefix, next)
	}
	visit = func(elements []Element, prefix string, active map[DefinitionName]bool) error {
		for _, element := range elements {
			path := element.Name
			if prefix != "" {
				path = prefix + "." + path
			}
			if element.JSONType == JSONTypeArray {
				path += "[]"
				if element.ArrayElementType == "Coding" {
					paths[path] = struct{}{}
				}
				if len(element.ArrayElements) != 0 {
					if err := visit(element.ArrayElements, path, active); err != nil {
						return err
					}
				} else if element.ArrayElementType != "" {
					if err := visitDefinition(element.ArrayElementType, path, active); err != nil {
						return err
					}
				}
				continue
			}
			if len(element.Elements) != 0 {
				if err := visit(element.Elements, path, active); err != nil {
					return err
				}
			} else if element.ReferencedType != "" {
				if err := visitDefinition(element.ReferencedType, path, active); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err := visit(definition.Elements, "", map[DefinitionName]bool{root: true}); err != nil {
		return nil, err
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

// HasRepeatedCodingPath reports whether a reachable element is a repeated Coding.
// It visits named definitions once without enumerating root-relative paths.
func (i *Index) HasRepeatedCodingPath(root DefinitionName) (bool, error) {
	if i == nil {
		return false, fmt.Errorf("schema index is unavailable")
	}
	visited := make(map[DefinitionName]bool)
	found := false
	var visit func([]Element) error
	var visitDefinition func(DefinitionName) error
	visitDefinition = func(name DefinitionName) error {
		if visited[name] {
			return nil
		}
		definition, ok := i.definitions[name]
		if !ok {
			return fmt.Errorf("schema definition %q is unavailable", name)
		}
		visited[name] = true
		return visit(definition.Elements)
	}
	visit = func(elements []Element) error {
		for _, element := range elements {
			if element.JSONType == JSONTypeArray {
				if element.ArrayElementType == "Coding" {
					found = true
				}
				if len(element.ArrayElements) != 0 {
					if err := visit(element.ArrayElements); err != nil {
						return err
					}
				} else if element.ArrayElementType != "" {
					if err := visitDefinition(element.ArrayElementType); err != nil {
						return err
					}
				}
			} else if len(element.Elements) != 0 {
				if err := visit(element.Elements); err != nil {
					return err
				}
			} else if element.ReferencedType != "" {
				if err := visitDefinition(element.ReferencedType); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visitDefinition(root); err != nil {
		return false, err
	}
	return found, nil
}

func cloneDefinitionStack(active map[DefinitionName]bool, name DefinitionName) map[DefinitionName]bool {
	cloned := make(map[DefinitionName]bool, len(active)+1)
	for existing := range active {
		cloned[existing] = true
	}
	cloned[name] = true
	return cloned
}
