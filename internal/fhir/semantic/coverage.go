package semantic

import (
	"sort"

	"github.com/calypr/loom/internal/fhir/schema"
)

// MemberCoverage accounts for schema members, not observed dataset values.
// An unresolved member remains a gap even though it has an inventory row.
type MemberCoverage struct {
	Owner       schema.DefinitionName `json:"owner"`
	Path        string                `json:"path"`
	Datatype    schema.DefinitionName `json:"datatype,omitempty"`
	ChoiceGroup string                `json:"choiceGroup,omitempty"`
	Repeated    bool                  `json:"repeated"`
	Treatment   string                `json:"treatment"`
	KeyPath     string                `json:"keyPath,omitempty"`
	Rule        string                `json:"rule,omitempty"`
}

// Coverage visits each definition once, including inline members. Recursive
// references are represented by datatype identity rather than expanded paths.
func (r *DatatypeRegistry) Coverage(index *schema.Index) []MemberCoverage {
	rows := make([]MemberCoverage, 0)
	var visit func(schema.DefinitionName, string, []schema.Element)
	visit = func(owner schema.DefinitionName, prefix string, elements []schema.Element) {
		for _, element := range elements {
			path := prefix + element.Name
			datatype := element.ReferencedType
			repeated := element.JSONType == schema.JSONTypeArray
			if repeated {
				path += "[]"
				datatype = element.ArrayElementType
			}
			row := MemberCoverage{Owner: owner, Path: path, Datatype: datatype, ChoiceGroup: element.ChoiceGroup, Repeated: repeated}
			switch {
			case datatype != "":
				if descriptor, ok := r.Lookup(datatype); ok && descriptor.Disposition != DispositionAdvancedOnly {
					row.Treatment = string(descriptor.Disposition)
				} else {
					row.Treatment = "UNRESOLVED_STRUCTURE"
				}
			case len(element.Elements) > 0 || len(element.ArrayElements) > 0:
				row.Treatment = "STRUCTURAL_CONTAINER"
			case element.BindingURI != "":
				row.Treatment = "CATEGORICAL_SLOT"
			default:
				row.Treatment = "STANDALONE_VALUE"
			}
			if descriptor, ok := r.Lookup(owner); prefix == "" && ok && descriptor.Disposition != DispositionAdvancedOnly {
				row.Treatment = "DATATYPE_MEMBER"
				row.Rule = string(descriptor.Disposition)
			}
			if prefix == "" {
				if r.HasUnresolvedValueGroup(owner) && element.ChoiceGroup == "value" {
					row.Treatment = "UNRESOLVED_ASSOCIATION"
				}
				for _, pairing := range r.ScopePairings(owner) {
					if pairingOwnsValue(pairing, element) {
						row.Treatment, row.KeyPath, row.Rule = "ASSOCIATED_VALUE", pairing.CategoricalPath, pairing.Rule
					}
				}
			}
			rows = append(rows, row)
			visit(owner, path+".", element.Elements)
			visit(owner, path+".", element.ArrayElements)
		}
	}
	for _, definition := range index.Definitions() {
		visit(definition.Name, "", definition.Elements)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Owner != rows[j].Owner {
			return rows[i].Owner < rows[j].Owner
		}
		return rows[i].Path < rows[j].Path
	})
	return rows
}
