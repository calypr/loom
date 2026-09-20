package schema

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
