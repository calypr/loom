// Package tableshapecap contains immutable, compiler-neutral capability
// artifacts used to author table-shape proposals. It deliberately does not
// depend on transport, FHIR schema, or persisted authoring-model packages.
package tableshapecap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/calypr/loom/internal/projectid"
)

var (
	ErrInvalid       = errors.New("invalid table-shape capability artifact")
	ErrNotFound      = errors.New("table-shape capability artifact not found")
	ErrIdentityClash = errors.New("table-shape capability identity collision")
)

type Binding struct {
	Project                  string `json:"project"`
	ExplorerID               string `json:"explorerId"`
	OutputID                 string `json:"outputId"`
	SnapshotToken            string `json:"snapshotToken"`
	AuthorizationScope       string `json:"authorizationScope"`
	SourceGeneration         string `json:"sourceGeneration"`
	DraftVersion             uint64 `json:"draftVersion"`
	DraftDigest              string `json:"draftDigest"`
	BaseDocumentDigest       string `json:"baseDocumentDigest"`
	BaseCompilationReceiptID string `json:"baseCompilationReceiptId"`
	OutputFingerprint        string `json:"outputFingerprint"`
	CompilerSchemaDigest     string `json:"compilerSchemaDigest"`
}

// CatalogLookup is the request-bound subset available before loading the
// persisted base compilation receipt referenced by a catalog binding.
type CatalogLookup struct {
	Project            string `json:"project"`
	ExplorerID         string `json:"explorerId"`
	OutputID           string `json:"outputId"`
	SnapshotToken      string `json:"snapshotToken"`
	AuthorizationScope string `json:"authorizationScope"`
	SourceGeneration   string `json:"sourceGeneration"`
	DraftVersion       uint64 `json:"draftVersion"`
	DraftDigest        string `json:"draftDigest"`
	BaseDocumentDigest string `json:"baseDocumentDigest"`
}

func (l CatalogLookup) Validate() error {
	if l.Project == "" || projectid.Canonical(l.Project) != l.Project {
		return invalid("lookup project must be a canonical project identity")
	}
	for name, value := range map[string]string{
		"explorerId": l.ExplorerID, "outputId": l.OutputID, "snapshotToken": l.SnapshotToken,
		"authorizationScope": l.AuthorizationScope, "sourceGeneration": l.SourceGeneration,
		"draftDigest": l.DraftDigest, "baseDocumentDigest": l.BaseDocumentDigest,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return invalid("lookup %s must be non-empty and trimmed", name)
		}
	}
	if l.DraftVersion == 0 {
		return invalid("lookup draftVersion must be positive")
	}
	return nil
}

func (l CatalogLookup) Matches(binding Binding) bool {
	return l.Project == binding.Project && l.ExplorerID == binding.ExplorerID && l.OutputID == binding.OutputID &&
		l.SnapshotToken == binding.SnapshotToken && l.AuthorizationScope == binding.AuthorizationScope &&
		l.SourceGeneration == binding.SourceGeneration && l.DraftVersion == binding.DraftVersion &&
		l.DraftDigest == binding.DraftDigest && l.BaseDocumentDigest == binding.BaseDocumentDigest
}

func (b Binding) Validate() error {
	if b.Project == "" || projectid.Canonical(b.Project) != b.Project {
		return invalid("project must be a canonical project identity")
	}
	for name, value := range map[string]string{
		"explorerId": b.ExplorerID, "outputId": b.OutputID, "snapshotToken": b.SnapshotToken,
		"authorizationScope": b.AuthorizationScope, "sourceGeneration": b.SourceGeneration,
		"draftDigest": b.DraftDigest, "baseDocumentDigest": b.BaseDocumentDigest,
		"baseCompilationReceiptId": b.BaseCompilationReceiptID, "outputFingerprint": b.OutputFingerprint,
		"compilerSchemaDigest": b.CompilerSchemaDigest,
	} {
		if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value {
			return invalid("%s must be non-empty and trimmed", name)
		}
	}
	if b.DraftVersion == 0 {
		return invalid("draftVersion must be positive")
	}
	return nil
}

type LogicalType string

const (
	LogicalString   LogicalType = "STRING"
	LogicalInteger  LogicalType = "INTEGER"
	LogicalDecimal  LogicalType = "DECIMAL"
	LogicalBoolean  LogicalType = "BOOLEAN"
	LogicalDate     LogicalType = "DATE"
	LogicalDateTime LogicalType = "DATETIME"
	LogicalObject   LogicalType = "OBJECT"
)

type PublicColumn struct {
	Key          string      `json:"key"`
	Label        string      `json:"label"`
	LogicalType  LogicalType `json:"logicalType"`
	Nullable     bool        `json:"nullable"`
	UnitIdentity string      `json:"unitIdentity,omitempty"`
}

func (c PublicColumn) Validate() error {
	if strings.TrimSpace(c.Key) == "" || strings.TrimSpace(c.Key) != c.Key {
		return invalid("column key must be non-empty and trimmed")
	}
	if strings.TrimSpace(c.Label) == "" || strings.TrimSpace(c.Label) != c.Label {
		return invalid("column label must be non-empty and trimmed")
	}
	switch c.LogicalType {
	case LogicalString, LogicalInteger, LogicalDecimal, LogicalBoolean, LogicalDate, LogicalDateTime, LogicalObject:
	default:
		return invalid("column %q has unknown logical type %q", c.Key, c.LogicalType)
	}
	if strings.TrimSpace(c.UnitIdentity) != c.UnitIdentity {
		return invalid("column %q unit identity must be trimmed", c.Key)
	}
	return nil
}

type ChoiceRole string

const (
	RolePivotGroup           ChoiceRole = "PIVOT_GROUP"
	RolePivotCategory        ChoiceRole = "PIVOT_CATEGORY"
	RolePivotCategoryValue   ChoiceRole = "PIVOT_CATEGORY_VALUE"
	RolePivotValue           ChoiceRole = "PIVOT_VALUE"
	RoleUnpivotInput         ChoiceRole = "UNPIVOT_INPUT"
	RoleDerivedOperand       ChoiceRole = "DERIVED_OPERAND"
	RoleDerivedOperator      ChoiceRole = "DERIVED_OPERATOR"
	RolePolicyDuplicate      ChoiceRole = "POLICY_DUPLICATE"
	RolePolicyMissing        ChoiceRole = "POLICY_MISSING"
	RolePolicyUnlisted       ChoiceRole = "POLICY_UNLISTED"
	RolePolicyUnpivotNull    ChoiceRole = "POLICY_UNPIVOT_NULL"
	RolePolicyDerivedMissing ChoiceRole = "POLICY_DERIVED_MISSING"
	RolePolicyDivisionByZero ChoiceRole = "POLICY_DIVISION_BY_ZERO"
)

func (r ChoiceRole) Valid() bool {
	switch r {
	case RolePivotGroup, RolePivotCategory, RolePivotCategoryValue, RolePivotValue, RoleUnpivotInput, RoleDerivedOperand,
		RoleDerivedOperator, RolePolicyDuplicate, RolePolicyMissing, RolePolicyUnlisted,
		RolePolicyUnpivotNull, RolePolicyDerivedMissing, RolePolicyDivisionByZero:
		return true
	default:
		return false
	}
}

type AvailabilityState string

const (
	AvailabilitySupported AvailabilityState = "SUPPORTED"
	AvailabilityRefused   AvailabilityState = "REFUSED"
)

type RoleAvailability struct {
	Role       ChoiceRole        `json:"role"`
	State      AvailabilityState `json:"state"`
	ReasonCode string            `json:"reasonCode,omitempty"`
	Message    string            `json:"message,omitempty"`
}

func (a RoleAvailability) Validate() error {
	if !a.Role.Valid() {
		return invalid("unknown choice role %q", a.Role)
	}
	switch a.State {
	case AvailabilitySupported:
		if a.ReasonCode != "" || a.Message != "" {
			return invalid("supported role %q cannot include refusal details", a.Role)
		}
	case AvailabilityRefused:
		if strings.TrimSpace(a.ReasonCode) == "" || strings.TrimSpace(a.Message) == "" {
			return invalid("refused role %q requires a stable reason code and message", a.Role)
		}
		if strings.TrimSpace(a.ReasonCode) != a.ReasonCode || strings.TrimSpace(a.Message) != a.Message {
			return invalid("refusal details for role %q must be trimmed", a.Role)
		}
	default:
		return invalid("unknown availability state %q", a.State)
	}
	return nil
}

// Scalar is a strict tagged union. Pointer payloads preserve empty string,
// zero, and false as values distinct from NULL and MISSING.
type Scalar struct {
	Kind    ScalarKind `json:"kind"`
	String  *string    `json:"string,omitempty"`
	Integer *int64     `json:"integer,omitempty"`
	Decimal *float64   `json:"decimal,omitempty"`
	Boolean *bool      `json:"boolean,omitempty"`
}

type ScalarKind string

const (
	ScalarString  ScalarKind = "STRING"
	ScalarInteger ScalarKind = "INTEGER"
	ScalarDecimal ScalarKind = "DECIMAL"
	ScalarBoolean ScalarKind = "BOOLEAN"
	ScalarNull    ScalarKind = "NULL"
	ScalarMissing ScalarKind = "MISSING"
)

func StringScalar(value string) Scalar   { return Scalar{Kind: ScalarString, String: &value} }
func IntegerScalar(value int64) Scalar   { return Scalar{Kind: ScalarInteger, Integer: &value} }
func DecimalScalar(value float64) Scalar { return Scalar{Kind: ScalarDecimal, Decimal: &value} }
func BooleanScalar(value bool) Scalar    { return Scalar{Kind: ScalarBoolean, Boolean: &value} }
func NullScalar() Scalar                 { return Scalar{Kind: ScalarNull} }
func MissingScalar() Scalar              { return Scalar{Kind: ScalarMissing} }

func (s Scalar) Validate() error {
	arms := 0
	if s.String != nil {
		arms++
	}
	if s.Integer != nil {
		arms++
	}
	if s.Decimal != nil {
		arms++
	}
	if s.Boolean != nil {
		arms++
	}
	switch s.Kind {
	case ScalarString:
		if s.String == nil || arms != 1 {
			return invalid("STRING scalar requires only string payload")
		}
	case ScalarInteger:
		if s.Integer == nil || arms != 1 {
			return invalid("INTEGER scalar requires only integer payload")
		}
	case ScalarDecimal:
		if s.Decimal == nil || arms != 1 || math.IsNaN(*s.Decimal) || math.IsInf(*s.Decimal, 0) {
			return invalid("DECIMAL scalar requires only a finite decimal payload")
		}
	case ScalarBoolean:
		if s.Boolean == nil || arms != 1 {
			return invalid("BOOLEAN scalar requires only boolean payload")
		}
	case ScalarNull, ScalarMissing:
		if arms != 0 {
			return invalid("%s scalar cannot carry a value", s.Kind)
		}
	default:
		return invalid("unknown scalar kind %q", s.Kind)
	}
	return nil
}

func (s *Scalar) UnmarshalJSON(data []byte) error {
	type scalarAlias Scalar
	var parsed scalarAlias
	if err := decodeStrict(data, &parsed); err != nil {
		return err
	}
	value := Scalar(parsed)
	if err := value.Validate(); err != nil {
		return err
	}
	*s = value
	return nil
}

type OperandKind string

const OperandColumn OperandKind = "COLUMN"

// OperandRef is the immutable server payload behind a catalog's opaque base
// operand choice. Derived receipts reference its choice ID, never a client
// supplied column key.
type OperandRef struct {
	Kind      OperandKind `json:"kind"`
	ColumnKey string      `json:"columnKey"`
}

func (o OperandRef) Validate() error {
	if o.Kind != OperandColumn || strings.TrimSpace(o.ColumnKey) == "" || strings.TrimSpace(o.ColumnKey) != o.ColumnKey {
		return invalid("catalog operand must be a COLUMN with a trimmed columnKey")
	}
	return nil
}

type ResolvedOperandKind string

const (
	ResolvedOperandCatalogChoice ResolvedOperandKind = "CATALOG_CHOICE"
	ResolvedOperandResolution    ResolvedOperandKind = "RESOLUTION_OUTPUT"
	ResolvedOperandLiteral       ResolvedOperandKind = "LITERAL"
)

// ResolvedOperand is an expression input frozen into a derived receipt. Base
// columns remain opaque catalog choices, while prior derived outputs remain
// explicit references to earlier child receipts.
type ResolvedOperand struct {
	Kind         ResolvedOperandKind `json:"kind"`
	ChoiceID     string              `json:"choiceId,omitempty"`
	ResolutionID string              `json:"resolutionId,omitempty"`
	OutputIndex  *int                `json:"outputIndex,omitempty"`
	Literal      *Scalar             `json:"literal,omitempty"`
}

func (o ResolvedOperand) Validate() error {
	choices := 0
	if o.ChoiceID != "" {
		choices++
	}
	if o.ResolutionID != "" || o.OutputIndex != nil {
		choices++
	}
	if o.Literal != nil {
		choices++
	}
	switch o.Kind {
	case ResolvedOperandCatalogChoice:
		if choices != 1 || !validChoiceID(o.ChoiceID) || o.ResolutionID != "" || o.OutputIndex != nil || o.Literal != nil {
			return invalid("CATALOG_CHOICE operand requires only a valid choiceId")
		}
	case ResolvedOperandResolution:
		if choices != 1 || !validRecordID(o.ResolutionID, resolutionIDPrefix) || o.OutputIndex == nil || *o.OutputIndex != 0 || o.ChoiceID != "" || o.Literal != nil {
			return invalid("RESOLUTION_OUTPUT operand requires an earlier receipt ID and output index zero")
		}
	case ResolvedOperandLiteral:
		if choices != 1 || o.Literal == nil || o.ChoiceID != "" || o.ResolutionID != "" || o.OutputIndex != nil {
			return invalid("LITERAL operand requires only literal")
		}
		if err := o.Literal.Validate(); err != nil {
			return err
		}
		if o.Literal.Kind != ScalarInteger && o.Literal.Kind != ScalarDecimal {
			return invalid("derived literals must be numeric INTEGER or DECIMAL values")
		}
	default:
		return invalid("unknown resolved operand kind %q", o.Kind)
	}
	return nil
}

type ColumnChoice struct {
	ID        string     `json:"id"`
	Role      ChoiceRole `json:"role"`
	ColumnKey string     `json:"columnKey"`
}

type OperatorChoice struct {
	ID       string     `json:"id"`
	Role     ChoiceRole `json:"role"`
	Operator string     `json:"operator"`
}

type PolicyChoice struct {
	ID       string     `json:"id"`
	Role     ChoiceRole `json:"role"`
	PolicyID string     `json:"policyId"`
}

type OperandChoice struct {
	ID      string     `json:"id"`
	Role    ChoiceRole `json:"role"`
	Operand OperandRef `json:"operand"`
}

type CatalogChoices struct {
	Columns   []ColumnChoice   `json:"columns,omitempty"`
	Operators []OperatorChoice `json:"operators,omitempty"`
	Policies  []PolicyChoice   `json:"policies,omitempty"`
	Operands  []OperandChoice  `json:"operands,omitempty"`
}

type SavedShapeSummary struct {
	ReshapeKind    string `json:"reshapeKind,omitempty"`
	DerivedColumns int    `json:"derivedColumns"`
	ShapeDigest    string `json:"shapeDigest,omitempty"`
}

type CatalogReceipt struct {
	ID            string             `json:"id"`
	ContentDigest string             `json:"contentDigest"`
	Binding       Binding            `json:"binding"`
	Columns       []PublicColumn     `json:"columns"`
	Availability  []RoleAvailability `json:"availability"`
	Choices       CatalogChoices     `json:"choices"`
	SavedShape    SavedShapeSummary  `json:"savedShape"`
	CreatedAt     string             `json:"createdAt,omitempty"`
}

func (c CatalogReceipt) Validate() error { return validateCatalog(c) }

func (c CatalogReceipt) FindColumn(role ChoiceRole, id string) (ColumnChoice, error) {
	if !isColumnRole(role) || strings.TrimSpace(id) == "" {
		return ColumnChoice{}, invalid("column choice role and ID are required")
	}
	if err := c.Validate(); err != nil {
		return ColumnChoice{}, err
	}
	return c.findColumn(role, id)
}

func (c CatalogReceipt) FindOperator(id string) (OperatorChoice, error) {
	if strings.TrimSpace(id) == "" {
		return OperatorChoice{}, invalid("operator choice ID is required")
	}
	if err := c.Validate(); err != nil {
		return OperatorChoice{}, err
	}
	return c.findOperator(id)
}

func (c CatalogReceipt) FindPolicy(role ChoiceRole, id string) (PolicyChoice, error) {
	if !isPolicyRole(role) || strings.TrimSpace(id) == "" {
		return PolicyChoice{}, invalid("policy choice role and ID are required")
	}
	if err := c.Validate(); err != nil {
		return PolicyChoice{}, err
	}
	return c.findPolicy(role, id)
}

func (c CatalogReceipt) FindOperand(id string) (OperandChoice, error) {
	if strings.TrimSpace(id) == "" {
		return OperandChoice{}, invalid("operand choice ID is required")
	}
	if err := c.Validate(); err != nil {
		return OperandChoice{}, err
	}
	return c.findOperand(id)

}

func (c CatalogReceipt) findColumn(role ChoiceRole, id string) (ColumnChoice, error) {
	for _, choice := range c.Choices.Columns {
		if choice.ID == id && choice.Role == role {
			return choice, nil
		}
	}
	return ColumnChoice{}, ErrNotFound
}
func (c CatalogReceipt) findOperator(id string) (OperatorChoice, error) {
	for _, choice := range c.Choices.Operators {
		if choice.ID == id {
			return choice, nil
		}
	}
	return OperatorChoice{}, ErrNotFound
}
func (c CatalogReceipt) findPolicy(role ChoiceRole, id string) (PolicyChoice, error) {
	for _, choice := range c.Choices.Policies {
		if choice.ID == id && choice.Role == role {
			return choice, nil
		}
	}
	return PolicyChoice{}, ErrNotFound
}
func (c CatalogReceipt) findOperand(id string) (OperandChoice, error) {
	for _, choice := range c.Choices.Operands {
		if choice.ID == id {
			return choice, nil
		}
	}
	return OperandChoice{}, ErrNotFound
}

type ResolutionKind string

const (
	ResolutionPivot   ResolutionKind = "PIVOT"
	ResolutionUnpivot ResolutionKind = "UNPIVOT"
	ResolutionDerived ResolutionKind = "DERIVED"
)

type TypeFact struct {
	LogicalType  LogicalType `json:"logicalType"`
	Nullable     bool        `json:"nullable"`
	UnitIdentity string      `json:"unitIdentity,omitempty"`
}

func (t TypeFact) Validate() error {
	return (PublicColumn{Key: "type", Label: "type", LogicalType: t.LogicalType, Nullable: t.Nullable, UnitIdentity: t.UnitIdentity}).Validate()
}

type CategoryProof struct {
	Complete          bool   `json:"complete"`
	Overflow          bool   `json:"overflow"`
	DistinctCount     int    `json:"distinctCount"`
	MaxCategories     int    `json:"maxCategories"`
	ValuesDigest      string `json:"valuesDigest"`
	SourceGeneration  string `json:"sourceGeneration"`
	OutputFingerprint string `json:"outputFingerprint"`
	ScanFingerprint   string `json:"scanFingerprint"`
	QueryProof        string `json:"queryProof"`
}

// DiscoveredCategory is a typed value made selectable by one complete,
// compiler-owned scan receipt. Its ID is opaque and scoped to that receipt.
type DiscoveredCategory struct {
	ChoiceID string `json:"choiceId"`
	Value    Scalar `json:"value"`
}

type CategoryScanReceipt struct {
	ID                     string               `json:"id"`
	ContentDigest          string               `json:"contentDigest"`
	Binding                Binding              `json:"binding"`
	ParentCatalogID        string               `json:"parentCatalogId"`
	CategoryColumnChoiceID string               `json:"categoryColumnChoiceId"`
	ValueColumnChoiceID    string               `json:"valueColumnChoiceId"`
	Categories             []DiscoveredCategory `json:"categories"`
	Proof                  CategoryProof        `json:"proof"`
	CreatedAt              string               `json:"createdAt,omitempty"`
}

func (r CategoryScanReceipt) Validate() error { return validateCategoryScan(r) }

func (r CategoryScanReceipt) FindCategoryChoice(id string) (DiscoveredCategory, error) {
	if strings.TrimSpace(id) == "" {
		return DiscoveredCategory{}, invalid("category choice ID is required")
	}
	if err := r.Validate(); err != nil {
		return DiscoveredCategory{}, err
	}
	for _, category := range r.Categories {
		if category.ChoiceID == id {
			return category, nil
		}
	}
	return DiscoveredCategory{}, ErrNotFound
}

type FrozenCategory struct {
	ChoiceID     string `json:"choiceId"`
	Value        Scalar `json:"value"`
	OutputColumn string `json:"outputColumn"`
	OutputLabel  string `json:"outputLabel"`
}

type PivotResolution struct {
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

// PivotDerivedOperand describes a compiler-typed output that remains after
// the pivot and can feed a subsequent derived construction.
type PivotDerivedOperand struct {
	ChoiceID     string   `json:"choiceId"`
	OutputColumn string   `json:"outputColumn"`
	OutputLabel  string   `json:"outputLabel"`
	Type         TypeFact `json:"type"`
}

type UnpivotResolution struct {
	InputColumnChoiceIDs []string               `json:"inputColumnChoiceIds"`
	Inputs               []ResolvedUnpivotInput `json:"inputs"`
	KeyOutput            NamedOutput            `json:"keyOutput"`
	ValueOutput          NamedOutput            `json:"valueOutput"`
	KeyResult            TypeFact               `json:"keyResult"`
	ValueResult          TypeFact               `json:"valueResult"`
	NullPolicyChoiceID   string                 `json:"nullPolicyChoiceId"`
}

// ResolvedUnpivotInput freezes the server-owned emitted key for a selected
// source column. The browser selects only ChoiceID and cannot author Key.
type ResolvedUnpivotInput struct {
	ChoiceID string `json:"choiceId"`
	Key      Scalar `json:"key"`
}

type DerivedResolution struct {
	Output                       NamedOutput     `json:"output"`
	PivotResolutionID            string          `json:"pivotResolutionId,omitempty"`
	OperatorChoiceID             string          `json:"operatorChoiceId"`
	Left                         ResolvedOperand `json:"left"`
	Right                        ResolvedOperand `json:"right"`
	Result                       TypeFact        `json:"result"`
	MissingPolicyChoiceID        string          `json:"missingPolicyChoiceId"`
	DivisionByZeroPolicyChoiceID string          `json:"divisionByZeroPolicyChoiceId,omitempty"`
}

type ResolutionReceipt struct {
	ID              string             `json:"id"`
	ContentDigest   string             `json:"contentDigest"`
	Binding         Binding            `json:"binding"`
	ParentCatalogID string             `json:"parentCatalogId"`
	Kind            ResolutionKind     `json:"kind"`
	Pivot           *PivotResolution   `json:"pivot,omitempty"`
	Unpivot         *UnpivotResolution `json:"unpivot,omitempty"`
	Derived         *DerivedResolution `json:"derived,omitempty"`
	CreatedAt       string             `json:"createdAt,omitempty"`
}

func (r ResolutionReceipt) Validate() error { return validateResolution(r) }

func (r ResolutionReceipt) Choice(role ChoiceRole, id string) (Scalar, error) {
	if role != RolePivotCategoryValue || r.Pivot == nil || strings.TrimSpace(id) == "" {
		return Scalar{}, invalid("resolution choice role and ID are not valid for this receipt")
	}
	if err := r.Validate(); err != nil {
		return Scalar{}, err
	}
	for _, category := range r.Pivot.Categories {
		if category.ChoiceID == id {
			return cloneScalar(category.Value), nil
		}
	}
	return Scalar{}, ErrNotFound
}

type IntentKind string

const (
	IntentSet    IntentKind = "SET"
	IntentRemove IntentKind = "REMOVE"
)

type NamedOutput struct {
	Name  string `json:"name"`
	Label string `json:"label"`
}

type SetIntent struct {
	CatalogID     string        `json:"catalogId"`
	ResolutionIDs []string      `json:"resolutionIds"`
	Outputs       []NamedOutput `json:"outputs,omitempty"`
}

type RemoveIntent struct{}

type ProposalIntent struct {
	Kind   IntentKind    `json:"kind"`
	Set    *SetIntent    `json:"set,omitempty"`
	Remove *RemoveIntent `json:"remove,omitempty"`
}

func (i ProposalIntent) Validate() error {
	switch i.Kind {
	case IntentSet:
		if i.Set == nil || i.Remove != nil {
			return invalid("SET intent requires only set payload")
		}
		if !validRecordID(i.Set.CatalogID, catalogIDPrefix) || len(i.Set.ResolutionIDs) == 0 {
			return invalid("SET intent requires catalog and resolution references")
		}
		seen := make(map[string]struct{}, len(i.Set.ResolutionIDs))
		for _, id := range i.Set.ResolutionIDs {
			if !validRecordID(id, resolutionIDPrefix) {
				return invalid("SET intent contains malformed resolution ID")
			}
			if _, exists := seen[id]; exists {
				return invalid("SET intent contains duplicate resolution ID")
			}
			seen[id] = struct{}{}
		}
		outputNames := make(map[string]struct{}, len(i.Set.Outputs))
		for _, out := range i.Set.Outputs {
			if strings.TrimSpace(out.Name) == "" || strings.TrimSpace(out.Name) != out.Name || strings.TrimSpace(out.Label) == "" || strings.TrimSpace(out.Label) != out.Label {
				return invalid("SET intent output names and labels must be non-empty and trimmed")
			}
			if _, exists := outputNames[out.Name]; exists {
				return invalid("SET intent output names must be unique")
			}
			outputNames[out.Name] = struct{}{}
		}
	case IntentRemove:
		if i.Remove == nil || i.Set != nil {
			return invalid("REMOVE intent requires only empty remove payload")
		}
	default:
		return invalid("unknown proposal intent kind %q", i.Kind)
	}
	return nil
}

func DecodeStrict[T any](data []byte) (T, error) {
	var value T
	err := decodeStrict(data, &value)
	return value, err
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: multiple JSON values", ErrInvalid)
		}
		return err
	}
	return nil
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}
