package tableshapecap

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

const (
	catalogIDPrefix      = "tsc_"
	categoryScanIDPrefix = "tsd_"
	resolutionIDPrefix   = "tsr_"
	choiceIDPrefix       = "tsch_"
)

type catalogIdentity struct {
	Binding      Binding               `json:"binding"`
	Columns      []PublicColumn        `json:"columns"`
	Availability []RoleAvailability    `json:"availability"`
	Choices      catalogChoiceIdentity `json:"choices"`
	SavedShape   SavedShapeSummary     `json:"savedShape"`
}

type catalogChoiceIdentity struct {
	Columns   []columnChoicePayload   `json:"columns,omitempty"`
	Operators []operatorChoicePayload `json:"operators,omitempty"`
	Policies  []policyChoicePayload   `json:"policies,omitempty"`
	Operands  []operandChoicePayload  `json:"operands,omitempty"`
}

type columnChoicePayload struct {
	Role      ChoiceRole `json:"role"`
	ColumnKey string     `json:"columnKey"`
}
type operatorChoicePayload struct {
	Role     ChoiceRole `json:"role"`
	Operator string     `json:"operator"`
}
type policyChoicePayload struct {
	Role     ChoiceRole `json:"role"`
	PolicyID string     `json:"policyId"`
}
type operandChoicePayload struct {
	Role    ChoiceRole `json:"role"`
	Operand OperandRef `json:"operand"`
}

type resolutionIdentity struct {
	Binding         Binding            `json:"binding"`
	ParentCatalogID string             `json:"parentCatalogId"`
	Kind            ResolutionKind     `json:"kind"`
	Pivot           *pivotIdentity     `json:"pivot,omitempty"`
	Unpivot         *UnpivotResolution `json:"unpivot,omitempty"`
	Derived         *DerivedResolution `json:"derived,omitempty"`
}

type categoryScanIdentity struct {
	Binding                Binding       `json:"binding"`
	ParentCatalogID        string        `json:"parentCatalogId"`
	CategoryColumnChoiceID string        `json:"categoryColumnChoiceId"`
	ValueColumnChoiceID    string        `json:"valueColumnChoiceId"`
	Values                 []Scalar      `json:"values"`
	Proof                  CategoryProof `json:"proof"`
}

type pivotIdentity struct {
	GroupColumnChoiceIDs    []string              `json:"groupColumnChoiceIds"`
	CategoryColumnChoiceID  string                `json:"categoryColumnChoiceId"`
	ValueColumnChoiceID     string                `json:"valueColumnChoiceId"`
	CategoryDiscoveryID     string                `json:"categoryDiscoveryId"`
	Categories              []FrozenCategory      `json:"categories"`
	DerivedOperands         []PivotDerivedOperand `json:"derivedOperands,omitempty"`
	CategoryProof           CategoryProof         `json:"categoryProof"`
	DuplicatePolicyChoiceID string                `json:"duplicatePolicyChoiceId"`
	MissingPolicyChoiceID   string                `json:"missingPolicyChoiceId"`
	UnlistedPolicyChoiceID  string                `json:"unlistedPolicyChoiceId"`
}

const maxDiscoveredCategories = 256

func NewCategoryScanReceipt(binding Binding, parentCatalogID, categoryColumnChoiceID, valueColumnChoiceID string, values []Scalar, proof CategoryProof, createdAt string) (CategoryScanReceipt, error) {
	receipt := CategoryScanReceipt{
		Binding: binding, ParentCatalogID: parentCatalogID,
		CategoryColumnChoiceID: categoryColumnChoiceID, ValueColumnChoiceID: valueColumnChoiceID,
		Categories: make([]DiscoveredCategory, len(values)), Proof: proof, CreatedAt: createdAt,
	}
	for i, value := range values {
		receipt.Categories[i].Value = cloneScalar(value)
	}
	if err := validateCategoryScanContent(receipt); err != nil {
		return CategoryScanReceipt{}, err
	}
	payload, err := categoryScanIdentityBytes(receipt)
	if err != nil {
		return CategoryScanReceipt{}, err
	}
	receipt.ContentDigest = digest(payload)
	receipt.ID = shortID(categoryScanIDPrefix, payload)
	for i := range receipt.Categories {
		category := &receipt.Categories[i]
		category.ChoiceID = makeChoiceID(receipt.ID, RolePivotCategoryValue, category.Value)
	}
	if err := receipt.Validate(); err != nil {
		return CategoryScanReceipt{}, err
	}
	return receipt, nil
}

// NewPivotDerivedOperandChoiceID creates an opaque choice scoped to the exact
// post-pivot output mapping. The pivot's own ID is intentionally excluded to
// avoid a content-addressing cycle; every other semantic pivot field is bound.
func NewPivotDerivedOperandChoiceID(parentCatalogID string, pivot PivotResolution, output NamedOutput, fact TypeFact) (string, error) {
	pivot.DerivedOperands = nil
	payload, err := canonical(struct {
		Pivot  PivotResolution `json:"pivot"`
		Output NamedOutput     `json:"output"`
		Type   TypeFact        `json:"type"`
	}{pivot, output, fact})
	if err != nil {
		return "", err
	}
	return makeChoiceID(parentCatalogID, RoleDerivedOperand, payload), nil
}

func (r CategoryScanReceipt) ValidateAgainstCatalog(catalog CatalogReceipt) error {
	return ValidateCategoryScanAgainstCatalog(catalog, r)
}

func ValidateCategoryScanAgainstCatalog(catalog CatalogReceipt, scan CategoryScanReceipt) error {
	if err := catalog.Validate(); err != nil {
		return err
	}
	if err := scan.Validate(); err != nil {
		return err
	}
	if catalog.ID != scan.ParentCatalogID || catalog.Binding != scan.Binding {
		return invalid("category scan does not belong to this catalog binding")
	}
	if _, err := catalog.findColumn(RolePivotCategory, scan.CategoryColumnChoiceID); err != nil {
		return invalid("category scan column choice is not in parent catalog")
	}
	if _, err := catalog.findColumn(RolePivotValue, scan.ValueColumnChoiceID); err != nil {
		return invalid("category scan value choice is not in parent catalog")
	}
	return nil
}

func validateCategoryScan(r CategoryScanReceipt) error {
	if err := validateCategoryScanContent(r); err != nil {
		return err
	}
	if !validRecordID(r.ID, categoryScanIDPrefix) {
		return invalid("category scan ID is malformed")
	}
	payload, err := categoryScanIdentityBytes(r)
	if err != nil {
		return err
	}
	if r.ContentDigest != digest(payload) || r.ID != shortID(categoryScanIDPrefix, payload) {
		return invalid("category scan identity does not match its content")
	}
	for _, category := range r.Categories {
		if category.ChoiceID != makeChoiceID(r.ID, RolePivotCategoryValue, category.Value) {
			return invalid("category choice ID does not match scan receipt and value")
		}
	}
	return nil
}

func validateCategoryScanContent(r CategoryScanReceipt) error {
	if err := r.Binding.Validate(); err != nil {
		return err
	}
	if !validRecordID(r.ParentCatalogID, catalogIDPrefix) {
		return invalid("category scan requires a valid parent catalog ID")
	}
	if !validChoiceID(r.CategoryColumnChoiceID) || !validChoiceID(r.ValueColumnChoiceID) || r.CategoryColumnChoiceID == r.ValueColumnChoiceID {
		return invalid("category scan requires distinct valid category and value choices")
	}
	if r.Categories == nil {
		return invalid("category scan categories must be present, even when empty")
	}
	if r.CreatedAt != "" && strings.TrimSpace(r.CreatedAt) != r.CreatedAt {
		return invalid("createdAt must be trimmed")
	}
	values := make([]Scalar, len(r.Categories))
	seen := make(map[string]struct{}, len(r.Categories))
	for i, category := range r.Categories {
		if err := category.Value.Validate(); err != nil {
			return err
		}
		key, err := canonical(category.Value)
		if err != nil {
			return err
		}
		if _, ok := seen[string(key)]; ok {
			return invalid("category scan values must be unique")
		}
		seen[string(key)] = struct{}{}
		if category.ChoiceID != "" && !validChoiceID(category.ChoiceID) {
			return invalid("category scan choice ID is malformed")
		}
		values[i] = category.Value
	}
	proof := r.Proof
	if !proof.Complete || proof.Overflow || proof.MaxCategories <= 0 || proof.MaxCategories > maxDiscoveredCategories || proof.DistinctCount != len(values) || proof.DistinctCount > proof.MaxCategories || proof.SourceGeneration != r.Binding.SourceGeneration || proof.OutputFingerprint != r.Binding.OutputFingerprint || strings.TrimSpace(proof.ScanFingerprint) == "" || strings.TrimSpace(proof.ScanFingerprint) != proof.ScanFingerprint || strings.TrimSpace(proof.QueryProof) == "" || strings.TrimSpace(proof.QueryProof) != proof.QueryProof {
		return invalid("category scan proof must certify the complete set for the bound source and output")
	}
	valueDigest, err := CategoryValuesDigest(values)
	if err != nil {
		return err
	}
	if proof.ValuesDigest != valueDigest {
		return invalid("category scan proof digest does not match discovered membership")
	}
	return nil
}

func categoryScanIdentityBytes(r CategoryScanReceipt) ([]byte, error) {
	values := make([]Scalar, len(r.Categories))
	for i := range r.Categories {
		values[i] = r.Categories[i].Value
	}
	return canonical(categoryScanIdentity{
		Binding: r.Binding, ParentCatalogID: r.ParentCatalogID,
		CategoryColumnChoiceID: r.CategoryColumnChoiceID, ValueColumnChoiceID: r.ValueColumnChoiceID,
		Values: values, Proof: r.Proof,
	})
}

func NewCatalogReceipt(binding Binding, columns []PublicColumn, availability []RoleAvailability, choices CatalogChoices, saved SavedShapeSummary, createdAt string) (CatalogReceipt, error) {
	receipt := CatalogReceipt{Binding: binding, Columns: clone(columns), Availability: clone(availability), Choices: cloneCatalogChoices(choices), SavedShape: saved, CreatedAt: createdAt}
	if err := validateCatalogContent(receipt); err != nil {
		return CatalogReceipt{}, err
	}
	payload, err := catalogIdentityBytes(receipt)
	if err != nil {
		return CatalogReceipt{}, err
	}
	receipt.ContentDigest = digest(payload)
	receipt.ID = shortID(catalogIDPrefix, payload)
	for i := range receipt.Choices.Columns {
		choice := &receipt.Choices.Columns[i]
		choice.ID = makeChoiceID(receipt.ID, choice.Role, columnChoicePayload{Role: choice.Role, ColumnKey: choice.ColumnKey})
	}
	for i := range receipt.Choices.Operators {
		choice := &receipt.Choices.Operators[i]
		choice.ID = makeChoiceID(receipt.ID, choice.Role, operatorChoicePayload{Role: choice.Role, Operator: choice.Operator})
	}
	for i := range receipt.Choices.Policies {
		choice := &receipt.Choices.Policies[i]
		choice.ID = makeChoiceID(receipt.ID, choice.Role, policyChoicePayload{Role: choice.Role, PolicyID: choice.PolicyID})
	}
	for i := range receipt.Choices.Operands {
		choice := &receipt.Choices.Operands[i]
		choice.ID = makeChoiceID(receipt.ID, choice.Role, operandChoicePayload{Role: choice.Role, Operand: choice.Operand})
	}
	if err := receipt.Validate(); err != nil {
		return CatalogReceipt{}, err
	}
	return receipt, nil
}

func NewResolutionReceipt(binding Binding, parentCatalogID string, kind ResolutionKind, pivot *PivotResolution, unpivot *UnpivotResolution, derived *DerivedResolution, createdAt string) (ResolutionReceipt, error) {
	receipt := ResolutionReceipt{Binding: binding, ParentCatalogID: parentCatalogID, Kind: kind, Pivot: clonePivot(pivot), Unpivot: cloneUnpivot(unpivot), Derived: cloneDerived(derived), CreatedAt: createdAt}
	if err := validateResolutionContent(receipt); err != nil {
		return ResolutionReceipt{}, err
	}
	payload, err := resolutionIdentityBytes(receipt)
	if err != nil {
		return ResolutionReceipt{}, err
	}
	receipt.ContentDigest = digest(payload)
	receipt.ID = shortID(resolutionIDPrefix, payload)
	if err := receipt.Validate(); err != nil {
		return ResolutionReceipt{}, err
	}
	return receipt, nil
}

func ValidateResolutionAgainstCategoryScan(catalog CatalogReceipt, scan CategoryScanReceipt, resolution ResolutionReceipt) error {
	if err := ValidateCategoryScanAgainstCatalog(catalog, scan); err != nil {
		return err
	}
	if err := ValidateResolutionAgainstCatalog(catalog, resolution); err != nil {
		return err
	}
	if resolution.Kind != ResolutionPivot || resolution.Pivot == nil {
		return invalid("category scan can only bind a pivot resolution")
	}
	pivot := resolution.Pivot
	if pivot.CategoryDiscoveryID != scan.ID || pivot.CategoryColumnChoiceID != scan.CategoryColumnChoiceID || pivot.ValueColumnChoiceID != scan.ValueColumnChoiceID || pivot.CategoryProof != scan.Proof {
		return invalid("pivot does not match its complete category discovery receipt")
	}
	discovered := make(map[string]Scalar, len(scan.Categories))
	for _, category := range scan.Categories {
		discovered[category.ChoiceID] = category.Value
	}
	for _, category := range pivot.Categories {
		value, ok := discovered[category.ChoiceID]
		if !ok || !sameScalar(value, category.Value) {
			return invalid("pivot category is not selected from its complete category discovery receipt")
		}
	}
	return nil
}

func validateCatalog(c CatalogReceipt) error {
	if err := validateCatalogContent(c); err != nil {
		return err
	}
	if !validRecordID(c.ID, catalogIDPrefix) {
		return invalid("catalog ID is malformed")
	}
	payload, err := catalogIdentityBytes(c)
	if err != nil {
		return err
	}
	if c.ContentDigest != digest(payload) || c.ID != shortID(catalogIDPrefix, payload) {
		return invalid("catalog identity does not match its content")
	}
	for _, choice := range c.Choices.Columns {
		want := makeChoiceID(c.ID, choice.Role, columnChoicePayload{Role: choice.Role, ColumnKey: choice.ColumnKey})
		if choice.ID != want {
			return invalid("column choice ID does not match catalog, role, and payload")
		}
	}
	for _, choice := range c.Choices.Operators {
		want := makeChoiceID(c.ID, choice.Role, operatorChoicePayload{Role: choice.Role, Operator: choice.Operator})
		if choice.ID != want {
			return invalid("operator choice ID does not match catalog, role, and payload")
		}
	}
	for _, choice := range c.Choices.Policies {
		want := makeChoiceID(c.ID, choice.Role, policyChoicePayload{Role: choice.Role, PolicyID: choice.PolicyID})
		if choice.ID != want {
			return invalid("policy choice ID does not match catalog, role, and payload")
		}
	}
	for _, choice := range c.Choices.Operands {
		want := makeChoiceID(c.ID, choice.Role, operandChoicePayload{Role: choice.Role, Operand: choice.Operand})
		if choice.ID != want {
			return invalid("operand choice ID does not match catalog, role, and payload")
		}
	}
	return nil
}

func validateCatalogContent(c CatalogReceipt) error {
	if err := c.Binding.Validate(); err != nil {
		return err
	}
	if c.CreatedAt != "" && strings.TrimSpace(c.CreatedAt) != c.CreatedAt {
		return invalid("createdAt must be trimmed")
	}
	if c.Columns == nil {
		return invalid("catalog columns must be present, even when empty")
	}
	columnKeys := make(map[string]struct{}, len(c.Columns))
	for _, col := range c.Columns {
		if err := col.Validate(); err != nil {
			return err
		}
		if _, exists := columnKeys[col.Key]; exists {
			return invalid("duplicate public column key %q", col.Key)
		}
		columnKeys[col.Key] = struct{}{}
	}
	availability := make(map[ChoiceRole]RoleAvailability, len(c.Availability))
	for _, entry := range c.Availability {
		if err := entry.Validate(); err != nil {
			return err
		}
		if _, exists := availability[entry.Role]; exists {
			return invalid("duplicate availability role %q", entry.Role)
		}
		availability[entry.Role] = entry
	}
	for _, role := range capabilityRoles() {
		if _, ok := availability[role]; !ok {
			return invalid("catalog availability is missing role %q", role)
		}
	}
	choiceIDs := map[string]struct{}{}
	for _, choice := range c.Choices.Columns {
		if choice.ID != "" && !validChoiceID(choice.ID) {
			return invalid("malformed column choice ID")
		}
		if !isColumnRole(choice.Role) {
			return invalid("column choice has non-column role %q", choice.Role)
		}
		if _, ok := columnKeys[choice.ColumnKey]; !ok {
			return invalid("choice points to unknown public column %q", choice.ColumnKey)
		}
		if err := requireSupported(availability, choice.Role); err != nil {
			return err
		}
		if err := uniqueID(choiceIDs, choice.ID); err != nil {
			return err
		}
	}
	for _, choice := range c.Choices.Operators {
		if choice.ID != "" && !validChoiceID(choice.ID) {
			return invalid("malformed operator choice ID")
		}
		if choice.Role != RoleDerivedOperator {
			return invalid("operator choice has invalid role %q", choice.Role)
		}
		if !validOperator(choice.Operator) {
			return invalid("unsupported operator %q", choice.Operator)
		}
		if err := requireSupported(availability, choice.Role); err != nil {
			return err
		}
		if err := uniqueID(choiceIDs, choice.ID); err != nil {
			return err
		}
	}
	for _, choice := range c.Choices.Policies {
		if choice.ID != "" && !validChoiceID(choice.ID) {
			return invalid("malformed policy choice ID")
		}
		if !isPolicyRole(choice.Role) || !validPolicy(choice.Role, choice.PolicyID) {
			return invalid("policy choice has invalid role or policy %q/%q", choice.Role, choice.PolicyID)
		}
		if err := requireSupported(availability, choice.Role); err != nil {
			return err
		}
		if err := uniqueID(choiceIDs, choice.ID); err != nil {
			return err
		}
	}
	for _, choice := range c.Choices.Operands {
		if choice.ID != "" && !validChoiceID(choice.ID) {
			return invalid("malformed operand choice ID")
		}
		if choice.Role != RoleDerivedOperand {
			return invalid("operand choice has invalid role %q", choice.Role)
		}
		if err := choice.Operand.Validate(); err != nil {
			return err
		}
		if choice.Operand.Kind == OperandColumn {
			if _, ok := columnKeys[choice.Operand.ColumnKey]; !ok {
				return invalid("derived operand points to unknown public column %q", choice.Operand.ColumnKey)
			}
		}
		if err := requireSupported(availability, choice.Role); err != nil {
			return err
		}
		if err := uniqueID(choiceIDs, choice.ID); err != nil {
			return err
		}
	}
	if c.SavedShape.DerivedColumns < 0 {
		return invalid("saved derived column count cannot be negative")
	}
	switch c.SavedShape.ReshapeKind {
	case "", "PIVOT", "UNPIVOT":
	default:
		return invalid("saved reshape kind is invalid")
	}
	if strings.TrimSpace(c.SavedShape.ShapeDigest) != c.SavedShape.ShapeDigest {
		return invalid("saved shape digest must be trimmed")
	}
	return nil
}

func validateResolution(r ResolutionReceipt) error {
	if err := validateResolutionContent(r); err != nil {
		return err
	}
	if !validRecordID(r.ID, resolutionIDPrefix) {
		return invalid("resolution ID is malformed")
	}
	payload, err := resolutionIdentityBytes(r)
	if err != nil {
		return err
	}
	if r.ContentDigest != digest(payload) || r.ID != shortID(resolutionIDPrefix, payload) {
		return invalid("resolution identity does not match its content")
	}
	return nil
}

func validateResolutionContent(r ResolutionReceipt) error {
	if err := r.Binding.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ParentCatalogID) == "" || !validRecordID(r.ParentCatalogID, catalogIDPrefix) {
		return invalid("resolution requires a valid parent catalog ID")
	}
	if r.CreatedAt != "" && strings.TrimSpace(r.CreatedAt) != r.CreatedAt {
		return invalid("createdAt must be trimmed")
	}
	count := 0
	if r.Pivot != nil {
		count++
	}
	if r.Unpivot != nil {
		count++
	}
	if r.Derived != nil {
		count++
	}
	if count != 1 {
		return invalid("resolution must contain exactly one typed payload")
	}
	switch r.Kind {
	case ResolutionPivot:
		if r.Pivot == nil || r.Unpivot != nil || r.Derived != nil {
			return invalid("PIVOT resolution requires only pivot payload")
		}
		return validatePivot(r.Binding, *r.Pivot)
	case ResolutionUnpivot:
		if r.Unpivot == nil || r.Pivot != nil || r.Derived != nil {
			return invalid("UNPIVOT resolution requires only unpivot payload")
		}
		return validateUnpivot(*r.Unpivot)
	case ResolutionDerived:
		if r.Derived == nil || r.Pivot != nil || r.Unpivot != nil {
			return invalid("DERIVED resolution requires only derived payload")
		}
		return validateDerived(*r.Derived)
	default:
		return invalid("unknown resolution kind %q", r.Kind)
	}
}

func validatePivot(binding Binding, p PivotResolution) error {
	if len(p.GroupColumnChoiceIDs) == 0 {
		return invalid("pivot requires at least one ordered group column")
	}
	for _, id := range append(append([]string{}, p.GroupColumnChoiceIDs...), p.CategoryColumnChoiceID, p.ValueColumnChoiceID) {
		if strings.TrimSpace(id) == "" || !validChoiceID(id) {
			return invalid("pivot contains malformed column choice ID")
		}
	}
	groupIDs := map[string]struct{}{}
	for _, id := range p.GroupColumnChoiceIDs {
		if _, exists := groupIDs[id]; exists {
			return invalid("pivot group columns must be unique")
		}
		groupIDs[id] = struct{}{}
	}
	if p.CategoryColumnChoiceID == p.ValueColumnChoiceID {
		return invalid("pivot category and value columns must differ")
	}
	if !validRecordID(p.CategoryDiscoveryID, categoryScanIDPrefix) {
		return invalid("pivot requires a valid category discovery receipt ID")
	}
	if len(p.Categories) == 0 {
		return invalid("pivot requires a non-empty complete category set")
	}
	values := make(map[string]struct{}, len(p.Categories))
	outputNames := make(map[string]struct{}, len(p.Categories))
	for _, category := range p.Categories {
		if err := category.Value.Validate(); err != nil {
			return err
		}
		key, err := canonical(category.Value)
		if err != nil {
			return err
		}
		if _, exists := values[string(key)]; exists {
			return invalid("pivot category values must be unique")
		}
		values[string(key)] = struct{}{}
		if !validChoiceID(category.ChoiceID) {
			return invalid("pivot category choice ID is malformed")
		}
		if strings.TrimSpace(category.OutputColumn) == "" || strings.TrimSpace(category.OutputColumn) != category.OutputColumn || strings.TrimSpace(category.OutputLabel) == "" || strings.TrimSpace(category.OutputLabel) != category.OutputLabel {
			return invalid("pivot category output column and label must be non-empty and trimmed")
		}
		if _, exists := outputNames[category.OutputColumn]; exists {
			return invalid("pivot category output columns must be unique")
		}
		outputNames[category.OutputColumn] = struct{}{}
	}
	proof := p.CategoryProof
	if !proof.Complete || proof.Overflow || proof.MaxCategories <= 0 || proof.MaxCategories > maxDiscoveredCategories || proof.DistinctCount <= 0 || proof.DistinctCount < len(p.Categories) || proof.DistinctCount > proof.MaxCategories || proof.SourceGeneration != binding.SourceGeneration || proof.OutputFingerprint != binding.OutputFingerprint || strings.TrimSpace(proof.ScanFingerprint) == "" || strings.TrimSpace(proof.ScanFingerprint) != proof.ScanFingerprint || strings.TrimSpace(proof.QueryProof) == "" || strings.TrimSpace(proof.QueryProof) != proof.QueryProof {
		return invalid("pivot category proof must certify the complete set for the bound source and output")
	}
	if strings.TrimSpace(proof.ValuesDigest) == "" || strings.TrimSpace(proof.ValuesDigest) != proof.ValuesDigest {
		return invalid("pivot category proof values digest is required")
	}
	for _, id := range []string{p.DuplicatePolicyChoiceID, p.MissingPolicyChoiceID, p.UnlistedPolicyChoiceID} {
		if strings.TrimSpace(id) == "" || !validChoiceID(id) {
			return invalid("pivot policy choice ID is malformed")
		}
	}
	choiceIDs := make(map[string]struct{}, len(p.DerivedOperands))
	for _, operand := range p.DerivedOperands {
		if !validChoiceID(operand.ChoiceID) {
			return invalid("post-pivot derived operand choice ID is malformed")
		}
		if _, exists := choiceIDs[operand.ChoiceID]; exists {
			return invalid("post-pivot derived operand choices must be unique")
		}
		choiceIDs[operand.ChoiceID] = struct{}{}
		if err := validateNamedOutput(NamedOutput{Name: operand.OutputColumn, Label: operand.OutputLabel}); err != nil {
			return invalid("post-pivot derived operand output: %v", err)
		}
		if err := operand.Type.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func validateUnpivot(p UnpivotResolution) error {
	if len(p.InputColumnChoiceIDs) < 2 || len(p.Inputs) != len(p.InputColumnChoiceIDs) {
		return invalid("unpivot requires at least two ordered input columns")
	}
	seen := map[string]struct{}{}
	keyKind := ScalarKind("")
	for index, id := range p.InputColumnChoiceIDs {
		if !validChoiceID(id) {
			return invalid("unpivot input choice ID is malformed")
		}
		if _, ok := seen[id]; ok {
			return invalid("unpivot inputs must be unique")
		}
		seen[id] = struct{}{}
		input := p.Inputs[index]
		if input.ChoiceID != id {
			return invalid("unpivot resolved inputs must preserve choice order")
		}
		if err := input.Key.Validate(); err != nil {
			return err
		}
		if keyKind == "" {
			keyKind = input.Key.Kind
		} else if keyKind != input.Key.Kind {
			return invalid("unpivot keys must have one canonical scalar type")
		}
	}
	if err := p.KeyResult.Validate(); err != nil {
		return err
	}
	if err := p.ValueResult.Validate(); err != nil {
		return err
	}
	if err := validateNamedOutput(p.KeyOutput); err != nil {
		return invalid("unpivot key output: %v", err)
	}
	if err := validateNamedOutput(p.ValueOutput); err != nil {
		return invalid("unpivot value output: %v", err)
	}
	if p.KeyOutput.Name == p.ValueOutput.Name {
		return invalid("unpivot output names must be distinct")
	}
	if !validChoiceID(p.NullPolicyChoiceID) {
		return invalid("unpivot null policy choice ID is malformed")
	}
	return nil
}

func validateDerived(p DerivedResolution) error {
	if err := validateNamedOutput(p.Output); err != nil {
		return invalid("derived output: %v", err)
	}
	if !validChoiceID(p.OperatorChoiceID) {
		return invalid("derived operator choice ID is malformed")
	}
	if !validChoiceID(p.MissingPolicyChoiceID) {
		return invalid("derived missing policy choice ID is malformed")
	}
	if p.DivisionByZeroPolicyChoiceID != "" && !validChoiceID(p.DivisionByZeroPolicyChoiceID) {
		return invalid("derived division-by-zero policy choice ID is malformed")
	}
	if err := p.Result.Validate(); err != nil {
		return err
	}
	if err := p.Left.Validate(); err != nil {
		return err
	}
	if err := p.Right.Validate(); err != nil {
		return err
	}
	return nil
}

func validateNamedOutput(output NamedOutput) error {
	if strings.TrimSpace(output.Name) == "" || output.Name != strings.TrimSpace(output.Name) || strings.TrimSpace(output.Label) == "" || output.Label != strings.TrimSpace(output.Label) {
		return invalid("name and label must be non-empty and trimmed")
	}
	return nil
}

func ValidateResolutionAgainstCatalog(catalog CatalogReceipt, resolution ResolutionReceipt, priorReceipts ...ResolutionReceipt) error {
	if err := catalog.Validate(); err != nil {
		return err
	}
	if err := resolution.Validate(); err != nil {
		return err
	}
	if catalog.ID != resolution.ParentCatalogID || catalog.Binding != resolution.Binding {
		return invalid("resolution does not belong to this catalog binding")
	}
	prior := make(map[string]ResolutionReceipt, len(priorReceipts))
	for _, receipt := range priorReceipts {
		if err := receipt.Validate(); err != nil {
			return err
		}
		if receipt.Binding != catalog.Binding || receipt.ParentCatalogID != catalog.ID {
			return invalid("prior capability receipt does not belong to this catalog binding")
		}
		if receipt.ID == resolution.ID {
			return invalid("derived receipt cannot depend on itself")
		}
		if _, exists := prior[receipt.ID]; exists {
			return invalid("duplicate prior resolution receipt")
		}
		switch receipt.Kind {
		case ResolutionPivot:
			if receipt.Pivot == nil || receipt.Unpivot != nil || receipt.Derived != nil {
				return invalid("pivot context receipt has an invalid payload")
			}
			if err := validatePivotContext(catalog, *receipt.Pivot); err != nil {
				return err
			}
		case ResolutionDerived:
			if receipt.Derived == nil || receipt.Pivot != nil || receipt.Unpivot != nil {
				return invalid("prior derived receipt has an invalid payload")
			}
			if err := validateDerivedAgainstCatalog(catalog, *receipt.Derived, prior); err != nil {
				return err
			}
		default:
			return invalid("prior capability receipt must be a pivot context or derived resolution")
		}
		prior[receipt.ID] = receipt
	}
	if resolution.Pivot != nil {
		p := resolution.Pivot
		usedColumns := map[string]struct{}{}
		for _, id := range p.GroupColumnChoiceIDs {
			choice, err := catalog.findColumn(RolePivotGroup, id)
			if err != nil {
				return invalid("pivot group choice is not in parent catalog")
			}
			column := choice.ColumnKey
			if _, exists := usedColumns[column]; exists {
				return invalid("pivot column roles must select distinct public columns")
			}
			usedColumns[column] = struct{}{}
		}
		category, err := catalog.findColumn(RolePivotCategory, p.CategoryColumnChoiceID)
		if err != nil {
			return invalid("pivot category choice is not in parent catalog")
		}
		value, err := catalog.findColumn(RolePivotValue, p.ValueColumnChoiceID)
		if err != nil {
			return invalid("pivot value choice is not in parent catalog")
		}
		for _, column := range []string{category.ColumnKey, value.ColumnKey} {
			if _, exists := usedColumns[column]; exists {
				return invalid("pivot column roles must select distinct public columns")
			}
			usedColumns[column] = struct{}{}
		}
		for _, ref := range []struct {
			role ChoiceRole
			id   string
		}{{RolePolicyDuplicate, p.DuplicatePolicyChoiceID}, {RolePolicyMissing, p.MissingPolicyChoiceID}, {RolePolicyUnlisted, p.UnlistedPolicyChoiceID}} {
			if _, err := catalog.findPolicy(ref.role, ref.id); err != nil {
				return invalid("pivot policy choice is not in parent catalog")
			}
		}
	}
	if resolution.Unpivot != nil {
		for index, id := range resolution.Unpivot.InputColumnChoiceIDs {
			choice, err := catalog.findColumn(RoleUnpivotInput, id)
			if err != nil {
				return invalid("unpivot input choice is not in parent catalog")
			}
			if resolution.Unpivot.Inputs[index].ChoiceID != choice.ID {
				return invalid("unpivot resolved input does not match parent catalog choice")
			}
		}
		if _, err := catalog.findPolicy(RolePolicyUnpivotNull, resolution.Unpivot.NullPolicyChoiceID); err != nil {
			return invalid("unpivot policy choice is not in parent catalog")
		}
	}
	if resolution.Derived != nil {
		if err := validateDerivedAgainstCatalog(catalog, *resolution.Derived, prior); err != nil {
			return err
		}
	}
	return nil
}

func validatePivotContext(catalog CatalogReceipt, pivot PivotResolution) error {
	for _, id := range pivot.GroupColumnChoiceIDs {
		if _, err := catalog.findColumn(RolePivotGroup, id); err != nil {
			return invalid("pivot context group choice is not in parent catalog")
		}
	}
	if _, err := catalog.findColumn(RolePivotCategory, pivot.CategoryColumnChoiceID); err != nil {
		return invalid("pivot context category choice is not in parent catalog")
	}
	if _, err := catalog.findColumn(RolePivotValue, pivot.ValueColumnChoiceID); err != nil {
		return invalid("pivot context value choice is not in parent catalog")
	}
	for _, operand := range pivot.DerivedOperands {
		id, err := NewPivotDerivedOperandChoiceID(catalog.ID, pivot, NamedOutput{Name: operand.OutputColumn, Label: operand.OutputLabel}, operand.Type)
		if err != nil || id != operand.ChoiceID {
			return invalid("post-pivot derived operand choice does not match its output mapping")
		}
	}
	return nil
}

func validateDerivedAgainstCatalog(catalog CatalogReceipt, d DerivedResolution, prior map[string]ResolutionReceipt) error {
	operator, err := catalog.findOperator(d.OperatorChoiceID)
	if err != nil {
		return invalid("derived operator choice is not in parent catalog")
	}
	if operator.Operator == "DIVIDE" && d.DivisionByZeroPolicyChoiceID == "" {
		return invalid("division requires a division-by-zero policy")
	}
	if operator.Operator != "DIVIDE" && d.DivisionByZeroPolicyChoiceID != "" {
		return invalid("non-division operation cannot select division-by-zero policy")
	}
	var pivot *PivotResolution
	if d.PivotResolutionID != "" {
		context, ok := prior[d.PivotResolutionID]
		if !ok || context.Kind != ResolutionPivot || context.Pivot == nil || context.Binding != catalog.Binding || context.ParentCatalogID != catalog.ID {
			return invalid("derived resolution pivot context is not an earlier pivot from this catalog")
		}
		pivot = context.Pivot
	}
	for _, operand := range []ResolvedOperand{d.Left, d.Right} {
		switch operand.Kind {
		case ResolvedOperandCatalogChoice:
			if pivot != nil {
				if _, ok := findPivotDerivedOperand(*pivot, operand.ChoiceID); !ok {
					return invalid("derived operand is not present in the selected pivot output")
				}
			} else {
				choice, err := catalog.findOperand(operand.ChoiceID)
				if err != nil || choice.Operand.Kind != OperandColumn {
					return invalid("derived base operand is not a choice in parent catalog")
				}
			}
		case ResolvedOperandResolution:
			previous, ok := prior[operand.ResolutionID]
			if !ok || previous.Kind != ResolutionDerived || previous.Derived == nil || previous.Derived.PivotResolutionID != d.PivotResolutionID || operand.OutputIndex == nil || *operand.OutputIndex != 0 {
				return invalid("derived resolution operand must reference an earlier receipt under the same catalog")
			}
		case ResolvedOperandLiteral:
			if err := operand.Validate(); err != nil {
				return err
			}
		default:
			return invalid("unknown derived operand kind")
		}
	}
	if _, err := catalog.findPolicy(RolePolicyDerivedMissing, d.MissingPolicyChoiceID); err != nil {
		return invalid("derived missing policy choice is not in parent catalog")
	}
	if d.DivisionByZeroPolicyChoiceID != "" {
		if _, err := catalog.findPolicy(RolePolicyDivisionByZero, d.DivisionByZeroPolicyChoiceID); err != nil {
			return invalid("derived division-by-zero policy choice is not in parent catalog")
		}
	}
	return nil
}

func findPivotDerivedOperand(pivot PivotResolution, choiceID string) (PivotDerivedOperand, bool) {
	for _, operand := range pivot.DerivedOperands {
		if operand.ChoiceID == choiceID {
			return operand, true
		}
	}
	return PivotDerivedOperand{}, false
}

func catalogIdentityBytes(c CatalogReceipt) ([]byte, error) {
	availability := clone(c.Availability)
	sort.Slice(availability, func(i, j int) bool { return availability[i].Role < availability[j].Role })
	identity := catalogIdentity{Binding: c.Binding, Columns: clone(c.Columns), Availability: availability, SavedShape: c.SavedShape}
	for _, v := range c.Choices.Columns {
		identity.Choices.Columns = append(identity.Choices.Columns, columnChoicePayload{Role: v.Role, ColumnKey: v.ColumnKey})
	}
	for _, v := range c.Choices.Operators {
		identity.Choices.Operators = append(identity.Choices.Operators, operatorChoicePayload{Role: v.Role, Operator: v.Operator})
	}
	for _, v := range c.Choices.Policies {
		identity.Choices.Policies = append(identity.Choices.Policies, policyChoicePayload{Role: v.Role, PolicyID: v.PolicyID})
	}
	for _, v := range c.Choices.Operands {
		identity.Choices.Operands = append(identity.Choices.Operands, operandChoicePayload{Role: v.Role, Operand: v.Operand})
	}
	return canonical(identity)
}

func resolutionIdentityBytes(r ResolutionReceipt) ([]byte, error) {
	identity := resolutionIdentity{Binding: r.Binding, ParentCatalogID: r.ParentCatalogID, Kind: r.Kind}
	if r.Pivot != nil {
		identity.Pivot = &pivotIdentity{
			GroupColumnChoiceIDs: clone(r.Pivot.GroupColumnChoiceIDs), CategoryColumnChoiceID: r.Pivot.CategoryColumnChoiceID,
			ValueColumnChoiceID: r.Pivot.ValueColumnChoiceID, CategoryDiscoveryID: r.Pivot.CategoryDiscoveryID, Categories: cloneFrozenCategories(r.Pivot.Categories), CategoryProof: r.Pivot.CategoryProof,
			DerivedOperands:         clonePivotDerivedOperands(r.Pivot.DerivedOperands),
			DuplicatePolicyChoiceID: r.Pivot.DuplicatePolicyChoiceID, MissingPolicyChoiceID: r.Pivot.MissingPolicyChoiceID,
			UnlistedPolicyChoiceID: r.Pivot.UnlistedPolicyChoiceID,
		}
	}
	identity.Unpivot = clonePtr(r.Unpivot)
	identity.Derived = clonePtr(r.Derived)
	return canonical(identity)
}

func clonePivotDerivedOperands(values []PivotDerivedOperand) []PivotDerivedOperand {
	if values == nil {
		return nil
	}
	out := append([]PivotDerivedOperand(nil), values...)
	return out
}

func makeChoiceID(parentID string, role ChoiceRole, payload any) string {
	bytes, _ := canonical(struct {
		Parent  string     `json:"parent"`
		Role    ChoiceRole `json:"role"`
		Payload any        `json:"payload"`
	}{parentID, role, payload})
	hash := sha256.Sum256(bytes)
	return choiceIDPrefix + hex.EncodeToString(hash[:16])
}

func categoryValuesDigest(values []Scalar) (string, error) {
	payload, err := canonical(values)
	if err != nil {
		return "", err
	}
	return digest(payload), nil
}

// CategoryValuesDigest computes the canonical ordered-membership digest used
// in compiler scan proofs. The caller supplies the authoritative scan facts;
// this helper only proves the frozen values match that proof.
func CategoryValuesDigest(values []Scalar) (string, error) {
	for _, value := range values {
		if err := value.Validate(); err != nil {
			return "", err
		}
	}
	return categoryValuesDigest(values)
}
func categoriesAsScalars(categories []FrozenCategory) []Scalar {
	out := make([]Scalar, len(categories))
	for i := range categories {
		out[i] = categories[i].Value
	}
	return out
}
func canonical(value any) ([]byte, error) { return json.Marshal(value) }
func digest(payload []byte) string {
	hash := sha256.Sum256(payload)
	return "sha256:" + hex.EncodeToString(hash[:])
}
func shortID(prefix string, payload []byte) string {
	hash := sha256.Sum256(payload)
	return prefix + hex.EncodeToString(hash[:16])
}
func validRecordID(value, prefix string) bool {
	suffix := strings.TrimPrefix(value, prefix)
	return len(value) == len(prefix)+32 && strings.HasPrefix(value, prefix) && suffix == strings.ToLower(suffix) && isHex(suffix)
}
func validChoiceID(value string) bool { return validRecordID(value, choiceIDPrefix) }
func isHex(value string) bool         { _, err := hex.DecodeString(value); return err == nil }
func uniqueID(seen map[string]struct{}, id string) error {
	if id == "" {
		return nil
	}
	if _, ok := seen[id]; ok {
		return invalid("duplicate opaque choice ID %q", id)
	}
	seen[id] = struct{}{}
	return nil
}
func requireSupported(all map[ChoiceRole]RoleAvailability, role ChoiceRole) error {
	entry, ok := all[role]
	if !ok || entry.State != AvailabilitySupported {
		return invalid("choice exists without supported availability for role %q", role)
	}
	return nil
}
func isColumnRole(role ChoiceRole) bool {
	switch role {
	case RolePivotGroup, RolePivotCategory, RolePivotValue, RoleUnpivotInput, RoleDerivedOperand:
		return true
	default:
		return false
	}
}
func isPolicyRole(role ChoiceRole) bool {
	switch role {
	case RolePolicyDuplicate, RolePolicyMissing, RolePolicyUnlisted, RolePolicyUnpivotNull, RolePolicyDerivedMissing, RolePolicyDivisionByZero:
		return true
	default:
		return false
	}
}
func validOperator(op string) bool {
	switch op {
	case "ADD", "SUBTRACT", "MULTIPLY", "DIVIDE":
		return true
	default:
		return false
	}
}
func validPolicy(role ChoiceRole, id string) bool {
	switch role {
	case RolePolicyDuplicate:
		return id == "ERROR" || id == "SUM" || id == "MIN" || id == "MAX"
	case RolePolicyMissing:
		return id == "NULL" || id == "ERROR"
	case RolePolicyUnlisted:
		return id == "ERROR" || id == "EXCLUDE_WITH_EVIDENCE"
	case RolePolicyUnpivotNull:
		return id == "DROP" || id == "PRESERVE"
	case RolePolicyDerivedMissing:
		return id == "PROPAGATE_NULL" || id == "ERROR"
	case RolePolicyDivisionByZero:
		return id == "NULL" || id == "ERROR"
	default:
		return false
	}
}

func capabilityRoles() []ChoiceRole {
	return []ChoiceRole{
		RolePivotGroup, RolePivotCategory, RolePivotValue, RoleUnpivotInput, RoleDerivedOperand,
		RoleDerivedOperator, RolePolicyDuplicate, RolePolicyMissing, RolePolicyUnlisted,
		RolePolicyUnpivotNull, RolePolicyDerivedMissing, RolePolicyDivisionByZero,
	}
}

func clone[T any](value []T) []T {
	if value == nil {
		return nil
	}
	out := make([]T, len(value))
	copy(out, value)
	return out
}
func clonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}

func cloneScalar(value Scalar) Scalar {
	if value.String != nil {
		copy := *value.String
		value.String = &copy
	}
	if value.Integer != nil {
		copy := *value.Integer
		value.Integer = &copy
	}
	if value.Decimal != nil {
		copy := *value.Decimal
		value.Decimal = &copy
	}
	if value.Boolean != nil {
		copy := *value.Boolean
		value.Boolean = &copy
	}
	return value
}

func cloneOperand(value OperandRef) OperandRef { return value }

func cloneResolvedOperand(value ResolvedOperand) ResolvedOperand {
	value.OutputIndex = clonePtr(value.OutputIndex)
	if value.Literal != nil {
		copy := cloneScalar(*value.Literal)
		value.Literal = &copy
	}
	return value
}

func cloneCatalogChoices(value CatalogChoices) CatalogChoices {
	value.Columns = clone(value.Columns)
	value.Operators = clone(value.Operators)
	value.Policies = clone(value.Policies)
	value.Operands = clone(value.Operands)
	for i := range value.Operands {
		value.Operands[i].Operand = cloneOperand(value.Operands[i].Operand)
	}
	return value
}

func clonePivot(value *PivotResolution) *PivotResolution {
	if value == nil {
		return nil
	}
	out := *value
	out.GroupColumnChoiceIDs = clone(value.GroupColumnChoiceIDs)
	out.Categories = clone(value.Categories)
	for i := range out.Categories {
		out.Categories[i].Value = cloneScalar(out.Categories[i].Value)
	}
	return &out
}

func cloneFrozenCategories(values []FrozenCategory) []FrozenCategory {
	if values == nil {
		return nil
	}
	out := make([]FrozenCategory, len(values))
	copy(out, values)
	for i := range out {
		out[i].Value = cloneScalar(out[i].Value)
	}
	return out
}

func sameScalar(left, right Scalar) bool {
	leftBytes, leftErr := canonical(left)
	rightBytes, rightErr := canonical(right)
	return leftErr == nil && rightErr == nil && string(leftBytes) == string(rightBytes)
}

func cloneUnpivot(value *UnpivotResolution) *UnpivotResolution {
	if value == nil {
		return nil
	}
	out := *value
	out.InputColumnChoiceIDs = clone(value.InputColumnChoiceIDs)
	out.Inputs = append([]ResolvedUnpivotInput(nil), value.Inputs...)
	for index := range out.Inputs {
		out.Inputs[index].Key = cloneScalar(out.Inputs[index].Key)
	}
	return &out
}

func cloneDerived(value *DerivedResolution) *DerivedResolution {
	if value == nil {
		return nil
	}
	out := *value
	out.Left = cloneResolvedOperand(value.Left)
	out.Right = cloneResolvedOperand(value.Right)
	return &out
}

func (c *CatalogReceipt) UnmarshalJSON(data []byte) error {
	type alias CatalogReceipt
	var value alias
	if err := decodeStrict(data, &value); err != nil {
		return err
	}
	result := CatalogReceipt(value)
	if err := result.Validate(); err != nil {
		return err
	}
	*c = result
	return nil
}

func (r *CategoryScanReceipt) UnmarshalJSON(data []byte) error {
	type alias CategoryScanReceipt
	var value alias
	if err := decodeStrict(data, &value); err != nil {
		return err
	}
	result := CategoryScanReceipt(value)
	if err := result.Validate(); err != nil {
		return err
	}
	*r = result
	return nil
}

func (r *ResolutionReceipt) UnmarshalJSON(data []byte) error {
	type alias ResolutionReceipt
	var value alias
	if err := decodeStrict(data, &value); err != nil {
		return err
	}
	result := ResolutionReceipt(value)
	if err := result.Validate(); err != nil {
		return err
	}
	*r = result
	return nil
}

func (i *ProposalIntent) UnmarshalJSON(data []byte) error {
	type alias ProposalIntent
	var value alias
	if err := decodeStrict(data, &value); err != nil {
		return err
	}
	result := ProposalIntent(value)
	if err := result.Validate(); err != nil {
		return err
	}
	*i = result
	return nil
}

func (c CatalogReceipt) CanonicalContent() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return catalogIdentityBytes(c)
}
func (r CategoryScanReceipt) CanonicalContent() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return categoryScanIdentityBytes(r)
}
func (r ResolutionReceipt) CanonicalContent() ([]byte, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return resolutionIdentityBytes(r)
}
