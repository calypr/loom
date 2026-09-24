package semantic

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/calypr/loom/internal/fhir/schema"
)

// DatatypeRegistryVersion versions the generated-schema semantic descriptors.
const DatatypeRegistryVersion = 3

// Disposition is the closed semantic treatment for a generated complex datatype.
type Disposition string

const (
	DispositionValueAssociation Disposition = "VALUE_ASSOCIATION"
	DispositionCategorical      Disposition = "CATEGORICAL"
	DispositionCompositeValue   Disposition = "COMPOSITE_VALUE"
	DispositionNavigationOnly   Disposition = "NAVIGATION_ONLY"
	DispositionAdvancedOnly     Disposition = "ADVANCED_ONLY"
)

// MemberRole identifies the semantic part played by a member of one datatype.
type MemberRole string

const (
	MemberAssociationKey   MemberRole = "ASSOCIATION_KEY"
	MemberAssociationValue MemberRole = "ASSOCIATION_VALUE"
	MemberValueProjection  MemberRole = "VALUE_PROJECTION"
	MemberCategorySystem   MemberRole = "CATEGORY_SYSTEM"
	MemberCategoryVersion  MemberRole = "CATEGORY_VERSION"
	MemberCategoryCode     MemberRole = "CATEGORY_CODE"
	MemberCategoryDisplay  MemberRole = "CATEGORY_DISPLAY"
	MemberCategoryCoding   MemberRole = "CATEGORY_CODING"
	MemberCategoryText     MemberRole = "CATEGORY_TEXT_FALLBACK"
)

// MemberRoleDescriptor assigns a generated member path a semantic role. Paths
// are relative to the datatype and use [] for repeated boundaries.
type MemberRoleDescriptor struct {
	Path string
	Role MemberRole
}

// ScalarProjection selects one scalar preview from a structured datatype.
type ScalarProjection struct {
	Path         string
	LogicalType  string
	Presentation string
}

// DatatypeDescriptor describes how one generated complex datatype participates
// in semantic observations. A descriptor never depends on its containing
// resource or the spelling of that resource's member name.
type DatatypeDescriptor struct {
	Datatype         schema.DefinitionName
	Disposition      Disposition
	MemberRoles      []MemberRoleDescriptor
	ValueChoiceGroup string
	ScalarProjection *ScalarProjection
	RuleHint         string
}

// ScopePairingDescriptor proves that one categorical member discriminates a
// value carried by the same owner scope. Datatype descriptors describe the
// shape of one reusable FHIR datatype; scope pairings describe the small
// number of generated backbone/resource shapes whose members form one
// correlated observation. Keeping this data declarative avoids resource-name
// switches in the semantic walker.
type ScopePairingDescriptor struct {
	OwnerType        schema.DefinitionName
	CategoricalPath  string
	ValuePath        string
	ValueChoiceGroup string
	Rule             string
}

// DatatypeRegistry is an immutable, versioned view of datatype semantics.
type DatatypeRegistry struct {
	version          int
	entries          map[schema.DefinitionName]DatatypeDescriptor
	scopePairings    map[schema.DefinitionName][]ScopePairingDescriptor
	unresolvedValues map[schema.DefinitionName]bool
}

// ScopePairings returns the generated-schema pairing proofs for one owner
// definition. The returned descriptors are detached from the registry.
func (r *DatatypeRegistry) ScopePairings(owner schema.DefinitionName) []ScopePairingDescriptor {
	if r == nil {
		return nil
	}
	return append([]ScopePairingDescriptor(nil), r.scopePairings[owner]...)
}

// Version reports the registry version carried by this snapshot.
func (r *DatatypeRegistry) Version() int {
	if r == nil {
		return 0
	}
	return r.version
}

// Lookup returns a detached descriptor for datatype. Unknown generated types
// are represented by an explicit ADVANCED_ONLY disposition in the registry.
func (r *DatatypeRegistry) Lookup(datatype schema.DefinitionName) (DatatypeDescriptor, bool) {
	if r == nil {
		return DatatypeDescriptor{}, false
	}
	descriptor, ok := r.entries[datatype]
	if !ok {
		return DatatypeDescriptor{}, false
	}
	return cloneDatatypeDescriptor(descriptor), true
}

// Descriptors returns the complete registry in datatype-name order.
func (r *DatatypeRegistry) Descriptors() []DatatypeDescriptor {
	if r == nil || len(r.entries) == 0 {
		return []DatatypeDescriptor{}
	}
	definitions := make([]schema.DefinitionName, 0, len(r.entries))
	for definition := range r.entries {
		definitions = append(definitions, definition)
	}
	sort.Slice(definitions, func(left, right int) bool { return definitions[left] < definitions[right] })
	descriptors := make([]DatatypeDescriptor, 0, len(definitions))
	for _, definition := range definitions {
		descriptors = append(descriptors, cloneDatatypeDescriptor(r.entries[definition]))
	}
	return descriptors
}

// Validate checks registry coverage and every referenced member/projection
// against an authoritative generated-schema index.
func (r *DatatypeRegistry) Validate(index *schema.Index) error {
	if r == nil {
		return fmt.Errorf("validate semantic datatype registry: registry is nil")
	}
	if index == nil {
		return fmt.Errorf("validate semantic datatype registry: schema index is nil")
	}
	for _, definition := range index.Definitions() {
		if len(definition.Elements) == 0 {
			continue
		}
		if _, ok := r.entries[definition.Name]; !ok {
			return fmt.Errorf("validate semantic datatype registry: complex datatype %q has no disposition", definition.Name)
		}
	}
	for datatype, descriptor := range r.entries {
		if _, ok := index.Definition(datatype); !ok {
			return fmt.Errorf("validate semantic datatype registry: datatype %q is absent from generated schema", datatype)
		}
		if err := validateDatatypeDescriptor(index, descriptor); err != nil {
			return fmt.Errorf("validate semantic datatype registry: %w", err)
		}
	}
	for owner, pairings := range r.scopePairings {
		if _, ok := index.Definition(owner); !ok {
			return fmt.Errorf("validate semantic datatype registry: pairing owner %q is absent from generated schema", owner)
		}
		for _, pairing := range pairings {
			if strings.TrimSpace(pairing.CategoricalPath) == "" {
				return fmt.Errorf("validate semantic datatype registry: pairing owner %q has no categorical path", owner)
			}
			if (strings.TrimSpace(pairing.ValuePath) == "") == (strings.TrimSpace(pairing.ValueChoiceGroup) == "") {
				return fmt.Errorf("validate semantic datatype registry: pairing owner %q must define exactly one value path or choice group", owner)
			}
			category, err := resolveDatatypeMember(index, owner, pairing.CategoricalPath)
			if err != nil {
				return fmt.Errorf("validate semantic datatype registry: pairing owner %q categorical path %q: %w", owner, pairing.CategoricalPath, err)
			}
			if category.ReferencedType != "CodeableConcept" && category.ReferencedType != "Coding" {
				return fmt.Errorf("validate semantic datatype registry: pairing owner %q categorical path %q is not CodeableConcept or Coding", owner, pairing.CategoricalPath)
			}
			if pairing.ValuePath != "" {
				if _, err := resolveDatatypeMember(index, owner, pairing.ValuePath); err != nil {
					return fmt.Errorf("validate semantic datatype registry: pairing owner %q value path %q: %w", owner, pairing.ValuePath, err)
				}
			}
			if pairing.ValueChoiceGroup != "" {
				definition, _ := index.Definition(owner)
				found := false
				for _, element := range definition.Elements {
					if element.ChoiceGroup == pairing.ValueChoiceGroup {
						found = true
						break
					}
				}
				if !found {
					return fmt.Errorf("validate semantic datatype registry: pairing owner %q has no value choice group %q", owner, pairing.ValueChoiceGroup)
				}
			}
		}
	}
	return nil
}

// NewDatatypeRegistry creates a descriptor set for every complex definition in
// index. Datatypes without curated semantic behavior receive an explicit
// ADVANCED_ONLY descriptor, so adding a generated complex type cannot silently
// fall through to an unclassified state.
func NewDatatypeRegistry(index *schema.Index) (*DatatypeRegistry, error) {
	if index == nil {
		return nil, fmt.Errorf("create semantic datatype registry: schema index is nil")
	}
	registry := &DatatypeRegistry{
		version:          DatatypeRegistryVersion,
		entries:          make(map[schema.DefinitionName]DatatypeDescriptor),
		scopePairings:    make(map[schema.DefinitionName][]ScopePairingDescriptor),
		unresolvedValues: make(map[schema.DefinitionName]bool),
	}
	for _, definition := range index.Definitions() {
		if len(definition.Elements) == 0 {
			continue
		}
		registry.entries[definition.Name] = DatatypeDescriptor{
			Datatype:    definition.Name,
			Disposition: DispositionAdvancedOnly,
		}
	}
	for _, descriptor := range curatedDatatypeDescriptors() {
		if _, ok := index.Definition(descriptor.Datatype); !ok {
			continue
		}
		registry.entries[descriptor.Datatype] = cloneDatatypeDescriptor(descriptor)
	}
	for _, pairing := range curatedScopePairingDescriptors() {
		if _, ok := index.Definition(pairing.OwnerType); !ok {
			continue
		}
		registry.scopePairings[pairing.OwnerType] = append(registry.scopePairings[pairing.OwnerType], pairing)
	}
	for _, definition := range index.Definitions() {
		if len(registry.scopePairings[definition.Name]) != 0 {
			continue
		}
		if pairing, ok := structuralScopePairing(definition); ok {
			registry.scopePairings[definition.Name] = []ScopePairingDescriptor{pairing}
		} else if registry.entries[definition.Name].Disposition == DispositionAdvancedOnly {
			for _, element := range definition.Elements {
				if element.ChoiceGroup == "value" {
					registry.unresolvedValues[definition.Name] = true
				}
			}
		}
	}
	return registry, nil
}

// RegistryForIndex reuses the generated plan; alternate schema indexes own
// their descriptors instead of borrowing decisions from the generated schema.
func RegistryForIndex(index *schema.Index) (*DatatypeRegistry, error) {
	generated, err := schema.GeneratedIndex()
	if err != nil {
		return nil, err
	}
	if generated == index {
		return GeneratedDatatypeRegistry()
	}
	return NewDatatypeRegistry(index)
}

// GeneratedDatatypeRegistry returns the process-wide registry validated
// against GeneratedIndex.
func GeneratedDatatypeRegistry() (*DatatypeRegistry, error) {
	generatedRegistryOnce.Do(func() {
		index, err := schema.GeneratedIndex()
		if err != nil {
			generatedRegistryErr = fmt.Errorf("load generated schema for semantic datatype registry: %w", err)
			return
		}
		generatedRegistry, generatedRegistryErr = NewDatatypeRegistry(index)
		if generatedRegistryErr != nil {
			return
		}
		if err := generatedRegistry.Validate(index); err != nil {
			generatedRegistryErr = err
			generatedRegistry = nil
		}
	})
	return generatedRegistry, generatedRegistryErr
}

var (
	generatedRegistryOnce sync.Once
	generatedRegistry     *DatatypeRegistry
	generatedRegistryErr  error
)

func curatedDatatypeDescriptors() []DatatypeDescriptor {
	return []DatatypeDescriptor{
		{
			Datatype:    "Identifier",
			Disposition: DispositionValueAssociation,
			MemberRoles: []MemberRoleDescriptor{
				{Path: "system", Role: MemberAssociationKey},
				{Path: "value", Role: MemberAssociationValue},
			},
			RuleHint: "IDENTIFIER_SYSTEM_VALUE",
		},
		{
			Datatype:    "Extension",
			Disposition: DispositionValueAssociation,
			MemberRoles: []MemberRoleDescriptor{
				{Path: "url", Role: MemberAssociationKey},
			},
			ValueChoiceGroup: "value",
			RuleHint:         "EXTENSION_URL_VALUE",
		},
		{
			Datatype:    "Coding",
			Disposition: DispositionCategorical,
			MemberRoles: []MemberRoleDescriptor{
				{Path: "system", Role: MemberCategorySystem},
				{Path: "version", Role: MemberCategoryVersion},
				{Path: "code", Role: MemberCategoryCode},
				{Path: "display", Role: MemberCategoryDisplay},
			},
			RuleHint: "CATEGORICAL_SLOT_V1",
		},
		{
			Datatype:    "CodeableConcept",
			Disposition: DispositionCategorical,
			MemberRoles: []MemberRoleDescriptor{
				{Path: "coding[]", Role: MemberCategoryCoding},
				{Path: "coding[].system", Role: MemberCategorySystem},
				{Path: "coding[].version", Role: MemberCategoryVersion},
				{Path: "coding[].code", Role: MemberCategoryCode},
				{Path: "coding[].display", Role: MemberCategoryDisplay},
				{Path: "text", Role: MemberCategoryText},
			},
			ScalarProjection: &ScalarProjection{Path: "coding[].code", LogicalType: "string", Presentation: schema.ValuePresentationDisplayOrCode},
			RuleHint:         "CATEGORICAL_SLOT_V1",
		},
		{
			Datatype:    "Quantity",
			Disposition: DispositionCompositeValue,
			MemberRoles: []MemberRoleDescriptor{
				{Path: "value", Role: MemberValueProjection},
			},
			ScalarProjection: &ScalarProjection{Path: "value", LogicalType: "decimal"},
		},
		{
			Datatype:    "Period",
			Disposition: DispositionCompositeValue,
		},
		{
			Datatype:    "Range",
			Disposition: DispositionCompositeValue,
		},
		{
			Datatype:    "Ratio",
			Disposition: DispositionCompositeValue,
		},
		{Datatype: "Reference", Disposition: DispositionNavigationOnly},
		{Datatype: "Resource", Disposition: DispositionNavigationOnly},
	}
}

func curatedScopePairingDescriptors() []ScopePairingDescriptor {
	// The generated SubstanceDefinition representation backbone has a
	// CodeableConcept `format` sibling and a primitive `representation` sibling,
	// but the latter is not a value[x] choice group. The generic walker cannot
	// prove that relationship from the datatype descriptors alone, so retain
	// this schema-owned proof rather than guessing from a resource name in the
	// semantic extractor.
	return []ScopePairingDescriptor{
		{OwnerType: "SubstanceDefinitionStructureRepresentation", CategoricalPath: "format", ValuePath: "representation", Rule: "FORMAT_REPRESENTATION"},
		{OwnerType: "DosageDoseAndRate", CategoricalPath: "type", ValueChoiceGroup: "dose", Rule: "DOSE_AND_RATE_TYPE"},
		{OwnerType: "DosageDoseAndRate", CategoricalPath: "type", ValueChoiceGroup: "rate", Rule: "DOSE_AND_RATE_TYPE"},
	}
}

func cloneDatatypeDescriptor(descriptor DatatypeDescriptor) DatatypeDescriptor {
	descriptor.MemberRoles = append([]MemberRoleDescriptor(nil), descriptor.MemberRoles...)
	if descriptor.ScalarProjection != nil {
		projection := *descriptor.ScalarProjection
		descriptor.ScalarProjection = &projection
	}
	return descriptor
}

func validateDatatypeDescriptor(index *schema.Index, descriptor DatatypeDescriptor) error {
	definition, ok := index.Definition(descriptor.Datatype)
	if !ok {
		return fmt.Errorf("datatype %q is absent", descriptor.Datatype)
	}
	if len(definition.Elements) == 0 {
		return fmt.Errorf("datatype %q has no complex members", descriptor.Datatype)
	}
	switch descriptor.Disposition {
	case DispositionValueAssociation, DispositionCategorical, DispositionCompositeValue, DispositionNavigationOnly, DispositionAdvancedOnly:
	default:
		return fmt.Errorf("datatype %q has unknown disposition %q", descriptor.Datatype, descriptor.Disposition)
	}
	if descriptor.Disposition == DispositionValueAssociation {
		keyCount, valueCount := 0, 0
		for _, role := range descriptor.MemberRoles {
			switch role.Role {
			case MemberAssociationKey:
				keyCount++
			case MemberAssociationValue:
				valueCount++
			}
		}
		if keyCount != 1 || (valueCount != 1 && descriptor.ValueChoiceGroup == "") || (valueCount == 1 && descriptor.ValueChoiceGroup != "") {
			return fmt.Errorf("datatype %q must describe one key and either one value member or one value choice group", descriptor.Datatype)
		}
	}
	if descriptor.Disposition == DispositionCategorical {
		if len(memberPathsForRole(descriptor, MemberCategoryCode)) != 1 {
			return fmt.Errorf("categorical datatype %q must describe exactly one code path", descriptor.Datatype)
		}
	}
	if descriptor.Disposition == DispositionNavigationOnly || descriptor.Disposition == DispositionAdvancedOnly {
		if len(descriptor.MemberRoles) != 0 || descriptor.ValueChoiceGroup != "" || descriptor.ScalarProjection != nil {
			return fmt.Errorf("datatype %q with disposition %q cannot define semantic member roles or projections", descriptor.Datatype, descriptor.Disposition)
		}
	}
	for _, role := range descriptor.MemberRoles {
		if role.Path == "" {
			return fmt.Errorf("datatype %q has an empty member role path", descriptor.Datatype)
		}
		if !validMemberRole(role.Role) {
			return fmt.Errorf("datatype %q member %q has unknown role %q", descriptor.Datatype, role.Path, role.Role)
		}
		member, err := resolveDatatypeMember(index, descriptor.Datatype, role.Path)
		if err != nil {
			return fmt.Errorf("datatype %q member role %q: %w", descriptor.Datatype, role.Path, err)
		}
		if role.Role == MemberCategoryCoding && (member.JSONType != schema.JSONTypeArray || member.ArrayElementType != "Coding") {
			return fmt.Errorf("datatype %q categorical coding member %q must be a repeated Coding", descriptor.Datatype, role.Path)
		}
		if role.Role == MemberAssociationKey || role.Role == MemberAssociationValue || role.Role == MemberValueProjection || role.Role == MemberCategorySystem || role.Role == MemberCategoryVersion || role.Role == MemberCategoryCode || role.Role == MemberCategoryDisplay || role.Role == MemberCategoryText {
			if !primitiveLeaf(member, strings.HasSuffix(role.Path, "[]")) {
				return fmt.Errorf("datatype %q member role %q must resolve to a primitive leaf", descriptor.Datatype, role.Path)
			}
		}
	}
	if descriptor.ValueChoiceGroup != "" {
		found := false
		for _, element := range definition.Elements {
			if element.ChoiceGroup == descriptor.ValueChoiceGroup {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("datatype %q has no member in value choice group %q", descriptor.Datatype, descriptor.ValueChoiceGroup)
		}
	}
	if projection := descriptor.ScalarProjection; projection != nil {
		if projection.LogicalType == "" {
			return fmt.Errorf("datatype %q scalar projection has no logical type", descriptor.Datatype)
		}
		if _, err := resolveDatatypeMember(index, descriptor.Datatype, projection.Path); err != nil {
			return fmt.Errorf("datatype %q scalar projection %q: %w", descriptor.Datatype, projection.Path, err)
		}
	}
	return nil
}

func validMemberRole(role MemberRole) bool {
	switch role {
	case MemberAssociationKey, MemberAssociationValue, MemberValueProjection, MemberCategorySystem, MemberCategoryVersion, MemberCategoryCode, MemberCategoryDisplay, MemberCategoryCoding, MemberCategoryText:
		return true
	default:
		return false
	}
}

func memberPathsForRole(descriptor DatatypeDescriptor, role MemberRole) []string {
	paths := make([]string, 0, 1)
	for _, member := range descriptor.MemberRoles {
		if member.Role == role {
			paths = append(paths, member.Path)
		}
	}
	return paths
}

func descriptorBlocksDirectField(descriptor DatatypeDescriptor) bool {
	switch descriptor.Disposition {
	case DispositionValueAssociation, DispositionCategorical, DispositionCompositeValue, DispositionNavigationOnly:
		return true
	default:
		return false
	}
}

// DirectFieldReason is the classification explanation returned for a path.
type DirectFieldReason string

const (
	DirectFieldPrimitiveLeaf    DirectFieldReason = "PRIMITIVE_LEAF"
	DirectFieldSemanticDatatype DirectFieldReason = "SEMANTIC_DATATYPE_MEMBER"
	DirectFieldNonPrimitive     DirectFieldReason = "NON_PRIMITIVE_LEAF"
	DirectFieldPairedValue      DirectFieldReason = "ASSOCIATED_VALUE_MEMBER"
	DirectFieldCategorical      DirectFieldReason = "CATEGORICAL_SLOT"
)

// DirectFieldClassification says whether a generated-schema path can stand as
// one direct dataframe field. OwningDatatype names the descriptor or generated
// datatype that supplies the classification.
type DirectFieldClassification struct {
	Eligible       bool
	Reason         DirectFieldReason
	OwningDatatype schema.DefinitionName
	Disposition    Disposition
}

// ClassifyDirectField allows scalar leaves unless a semantic datatype owns
// their pairing. Paths are resolved against generated metadata, with Resource
// base members used as a generic fallback for omitted root-level inherited
// fields such as id and resourceType.
func ClassifyDirectField(index *schema.Index, resourceType schema.DefinitionName, canonicalPath string) (DirectFieldClassification, error) {
	if index == nil {
		return DirectFieldClassification{}, fmt.Errorf("classify direct FHIR field: schema index is nil")
	}
	segments, err := parseDatatypePath(canonicalPath)
	if err != nil {
		return DirectFieldClassification{}, err
	}
	registry, err := RegistryForIndex(index)
	if err != nil {
		return DirectFieldClassification{}, err
	}
	currentType := resourceType
	var currentInline []schema.Element
	var semanticOwner schema.DefinitionName
	var semanticDisposition Disposition
	for partIndex, segment := range segments {
		if descriptor, ok := registry.Lookup(currentType); ok && descriptorBlocksDirectField(descriptor) && semanticOwner == "" {
			semanticOwner = currentType
			semanticDisposition = descriptor.Disposition
		}
		elements, found := definitionElements(index, currentType, currentInline)
		if !found && partIndex == 0 {
			// Resource is the generated base definition for root-level members
			// omitted from a resource-specific flattened definition.
			elements, found = definitionElements(index, "Resource", nil)
			if found {
				currentType = "Resource"
				currentInline = nil
			}
		}
		if !found {
			return DirectFieldClassification{}, fmt.Errorf("classify direct FHIR field %q: definition %q is unavailable", canonicalPath, currentType)
		}
		element, ok := findSchemaElement(elements, segment.name)
		if !ok && partIndex == 0 && currentType != "Resource" {
			elements, found = definitionElements(index, "Resource", nil)
			if found {
				if baseElement, baseOK := findSchemaElement(elements, segment.name); baseOK {
					element = baseElement
					ok = true
					currentType = "Resource"
					currentInline = nil
				}
			}
		}
		if !ok {
			return DirectFieldClassification{}, fmt.Errorf("classify direct FHIR field %q: member %q is not declared by %q", canonicalPath, segment.name, currentType)
		}
		if segment.repeated != (element.JSONType == schema.JSONTypeArray) {
			return DirectFieldClassification{}, fmt.Errorf("classify direct FHIR field %q: repeated marker does not match member %q", canonicalPath, segment.name)
		}
		if semanticOwner == "" {
			if registry.HasUnresolvedValueGroup(currentType) && element.ChoiceGroup == "value" {
				return DirectFieldClassification{Reason: DirectFieldPairedValue, OwningDatatype: currentType, Disposition: DispositionAdvancedOnly}, nil
			}
			for _, pairing := range registry.ScopePairings(currentType) {
				if pairingOwnsValue(pairing, element) {
					return DirectFieldClassification{Reason: DirectFieldPairedValue, OwningDatatype: currentType, Disposition: DispositionValueAssociation}, nil
				}
			}
		}
		if partIndex == len(segments)-1 {
			owner := currentType
			if semanticOwner != "" {
				return DirectFieldClassification{
					Eligible:       false,
					Reason:         DirectFieldSemanticDatatype,
					OwningDatatype: semanticOwner,
					Disposition:    semanticDisposition,
				}, nil
			}
			if !primitiveLeaf(element, segment.repeated) {
				if element.ReferencedType != "" {
					owner = element.ReferencedType
				} else if element.ArrayElementType != "" {
					owner = element.ArrayElementType
				}
				disposition := Disposition("")
				if descriptor, ok := registry.Lookup(owner); ok {
					disposition = descriptor.Disposition
				}
				return DirectFieldClassification{Reason: DirectFieldNonPrimitive, OwningDatatype: owner, Disposition: disposition}, nil
			}
			if element.BindingURI != "" && element.ChoiceGroup == "" {
				return DirectFieldClassification{Reason: DirectFieldCategorical, OwningDatatype: owner, Disposition: DispositionCategorical}, nil
			}
			return DirectFieldClassification{
				Eligible:       true,
				Reason:         DirectFieldPrimitiveLeaf,
				OwningDatatype: owner,
			}, nil
		}
		if segment.repeated {
			currentType = element.ArrayElementType
			currentInline = element.ArrayElements
		} else {
			currentType = element.ReferencedType
			currentInline = element.Elements
		}
		if currentType == "" && len(currentInline) == 0 {
			return DirectFieldClassification{}, fmt.Errorf("classify direct FHIR field %q: member %q is not an object", canonicalPath, segment.name)
		}
	}
	return DirectFieldClassification{}, fmt.Errorf("classify direct FHIR field %q: path is empty", canonicalPath)
}

type datatypePathSegment struct {
	name     string
	repeated bool
}

func parseDatatypePath(path string) ([]datatypePathSegment, error) {
	if path == "" || strings.TrimSpace(path) != path {
		return nil, fmt.Errorf("classify direct FHIR field: path must be non-empty and canonical")
	}
	parts := strings.Split(path, ".")
	segments := make([]datatypePathSegment, 0, len(parts))
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part || strings.ContainsAny(strings.TrimSuffix(part, "[]"), "[]") || strings.Contains(part, "[") && !strings.HasSuffix(part, "[]") {
			return nil, fmt.Errorf("classify direct FHIR field: path %q is not canonical", path)
		}
		segment := datatypePathSegment{name: part}
		if strings.HasSuffix(part, "[]") {
			segment.name = strings.TrimSuffix(part, "[]")
			segment.repeated = true
		}
		if segment.name == "" {
			return nil, fmt.Errorf("classify direct FHIR field: path %q is not canonical", path)
		}
		segments = append(segments, segment)
	}
	return segments, nil
}

func resolveDatatypeMember(index *schema.Index, root schema.DefinitionName, path string) (schema.Element, error) {
	segments, err := parseDatatypePath(path)
	if err != nil {
		return schema.Element{}, err
	}
	currentType := root
	var currentInline []schema.Element
	var terminal schema.Element
	for partIndex, segment := range segments {
		elements, ok := definitionElements(index, currentType, currentInline)
		if !ok {
			return schema.Element{}, fmt.Errorf("definition %q is unavailable", currentType)
		}
		element, ok := findSchemaElement(elements, segment.name)
		if !ok {
			return schema.Element{}, fmt.Errorf("member %q is unavailable in %q", segment.name, currentType)
		}
		if segment.repeated != (element.JSONType == schema.JSONTypeArray) {
			return schema.Element{}, fmt.Errorf("repeated marker does not match member %q", segment.name)
		}
		terminal = element
		if partIndex == len(segments)-1 {
			break
		}
		if segment.repeated {
			currentType = element.ArrayElementType
			currentInline = element.ArrayElements
		} else {
			currentType = element.ReferencedType
			currentInline = element.Elements
		}
		if currentType == "" && len(currentInline) == 0 {
			return schema.Element{}, fmt.Errorf("member %q is not a structured member", segment.name)
		}
	}
	return terminal, nil
}

func definitionElements(index *schema.Index, definition schema.DefinitionName, inline []schema.Element) ([]schema.Element, bool) {
	if len(inline) != 0 {
		return inline, true
	}
	metadata, ok := index.Definition(definition)
	if !ok {
		return nil, false
	}
	return metadata.Elements, true
}

func findSchemaElement(elements []schema.Element, name string) (schema.Element, bool) {
	for _, element := range elements {
		if element.Name == name {
			return element, true
		}
	}
	return schema.Element{}, false
}

func primitiveLeaf(element schema.Element, repeated bool) bool {
	if repeated {
		return element.ArrayElementType == "" && len(element.ArrayElements) == 0 && element.ItemJSONType != "" && element.ItemJSONType != schema.JSONTypeObject && element.ItemJSONType != schema.JSONTypeArray
	}
	return element.ReferencedType == "" && len(element.Elements) == 0 && element.JSONType != "" && element.JSONType != schema.JSONTypeObject && element.JSONType != schema.JSONTypeArray
}
