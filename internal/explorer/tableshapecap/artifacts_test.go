package tableshapecap

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func testBinding() Binding {
	return Binding{
		Project: "project-a", ExplorerID: "explorer-a", OutputID: "output-a", SnapshotToken: "snapshot-a",
		AuthorizationScope: "scope-a", SourceGeneration: "generation-a", DraftVersion: 7, DraftDigest: "draft-a",
		BaseDocumentDigest: "document-a", BaseCompilationReceiptID: "compilation-a", OutputFingerprint: "output-fingerprint-a",
		CompilerSchemaDigest: "schema-a",
	}
}

func ptr[T any](value T) *T { return &value }

func testCatalog(t *testing.T, binding Binding, createdAt string) CatalogReceipt {
	t.Helper()
	columns := []PublicColumn{
		{Key: "patient", Label: "Patient", LogicalType: LogicalString},
		{Key: "category", Label: "Category", LogicalType: LogicalString, Nullable: true},
		{Key: "value", Label: "Value", LogicalType: LogicalDecimal, Nullable: true, UnitIdentity: "mg"},
	}
	roles := []ChoiceRole{RolePivotGroup, RolePivotCategory, RolePivotValue, RoleUnpivotInput, RoleDerivedOperand, RoleDerivedOperator, RolePolicyDuplicate, RolePolicyMissing, RolePolicyUnlisted, RolePolicyUnpivotNull, RolePolicyDerivedMissing, RolePolicyDivisionByZero}
	availability := make([]RoleAvailability, 0, len(roles))
	for _, role := range roles {
		availability = append(availability, RoleAvailability{Role: role, State: AvailabilitySupported})
	}
	choices := CatalogChoices{
		Columns: []ColumnChoice{
			{Role: RolePivotGroup, ColumnKey: "patient"},
			{Role: RolePivotCategory, ColumnKey: "category"},
			{Role: RolePivotValue, ColumnKey: "value"},
			{Role: RoleUnpivotInput, ColumnKey: "category"},
			{Role: RoleUnpivotInput, ColumnKey: "value"},
		},
		Operators: []OperatorChoice{{Role: RoleDerivedOperator, Operator: "ADD"}},
		Policies: []PolicyChoice{
			{Role: RolePolicyDuplicate, PolicyID: "ERROR"},
			{Role: RolePolicyMissing, PolicyID: "NULL"},
			{Role: RolePolicyUnlisted, PolicyID: "ERROR"},
			{Role: RolePolicyUnpivotNull, PolicyID: "PRESERVE"},
			{Role: RolePolicyDerivedMissing, PolicyID: "ERROR"},
			{Role: RolePolicyDivisionByZero, PolicyID: "ERROR"},
		},
		Operands: []OperandChoice{{Role: RoleDerivedOperand, Operand: OperandRef{Kind: OperandColumn, ColumnKey: "value"}}},
	}
	receipt, err := NewCatalogReceipt(binding, columns, availability, choices, SavedShapeSummary{}, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	return receipt
}

func testPivot(t *testing.T, catalog CatalogReceipt) ResolutionReceipt {
	t.Helper()
	byRole := map[ChoiceRole]string{}
	for _, choice := range catalog.Choices.Columns {
		if _, ok := byRole[choice.Role]; !ok {
			byRole[choice.Role] = choice.ID
		}
	}
	policies := map[ChoiceRole]string{}
	for _, choice := range catalog.Choices.Policies {
		policies[choice.Role] = choice.ID
	}
	pivot := &PivotResolution{
		GroupColumnChoiceIDs:    []string{byRole[RolePivotGroup]},
		CategoryColumnChoiceID:  byRole[RolePivotCategory],
		ValueColumnChoiceID:     byRole[RolePivotValue],
		Categories:              []FrozenCategory{{Value: StringScalar("")}, {Value: IntegerScalar(0)}, {Value: NullScalar()}, {Value: MissingScalar()}},
		DuplicatePolicyChoiceID: policies[RolePolicyDuplicate], MissingPolicyChoiceID: policies[RolePolicyMissing], UnlistedPolicyChoiceID: policies[RolePolicyUnlisted],
	}
	values := categoriesAsScalars(pivot.Categories)
	valuesDigest, err := CategoryValuesDigest(values)
	if err != nil {
		t.Fatal(err)
	}
	pivot.CategoryProof = CategoryProof{
		Complete: true, DistinctCount: len(values), MaxCategories: 256, ValuesDigest: valuesDigest,
		SourceGeneration: catalog.Binding.SourceGeneration, OutputFingerprint: catalog.Binding.OutputFingerprint,
		ScanFingerprint: "scan-fingerprint-v1", QueryProof: "query-proof-v1",
	}
	resolution, err := NewResolutionReceipt(catalog.Binding, catalog.ID, ResolutionPivot, pivot, nil, nil, "2026-09-19T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	return resolution
}

func TestReceiptAndChoiceIdentityIsDeterministicAndExcludesCreatedAt(t *testing.T) {
	binding := testBinding()
	left := testCatalog(t, binding, "2026-09-19T00:00:00Z")
	right := testCatalog(t, binding, "2026-09-20T00:00:00Z")
	if left.ID != right.ID || left.ContentDigest != right.ContentDigest {
		t.Fatalf("catalog identity changed with CreatedAt: %#v %#v", left, right)
	}
	for i := range left.Choices.Columns {
		if left.Choices.Columns[i].ID != right.Choices.Columns[i].ID {
			t.Fatal("column choice identity is not deterministic")
		}
	}
	resolutionA := testPivot(t, left)
	resolutionB := testPivot(t, right)
	if resolutionA.ID != resolutionB.ID || resolutionA.ContentDigest != resolutionB.ContentDigest {
		t.Fatal("resolution identity is not deterministic")
	}
	for i := range resolutionA.Pivot.Categories {
		if resolutionA.Pivot.Categories[i].ChoiceID != resolutionB.Pivot.Categories[i].ChoiceID {
			t.Fatal("category choice identity is not deterministic")
		}
	}
}

func TestEveryBindingFieldChangesCatalogIdentity(t *testing.T) {
	base := testBinding()
	original := testCatalog(t, base, "")
	originalResolution := testPivot(t, original)
	cases := map[string]func(*Binding){
		"project":            func(b *Binding) { b.Project = "project-b" },
		"explorer":           func(b *Binding) { b.ExplorerID += "-changed" },
		"output":             func(b *Binding) { b.OutputID += "-changed" },
		"snapshot":           func(b *Binding) { b.SnapshotToken += "-changed" },
		"authorization":      func(b *Binding) { b.AuthorizationScope += "-changed" },
		"generation":         func(b *Binding) { b.SourceGeneration += "-changed" },
		"draft version":      func(b *Binding) { b.DraftVersion++ },
		"draft digest":       func(b *Binding) { b.DraftDigest += "-changed" },
		"base document":      func(b *Binding) { b.BaseDocumentDigest += "-changed" },
		"base compilation":   func(b *Binding) { b.BaseCompilationReceiptID += "-changed" },
		"output fingerprint": func(b *Binding) { b.OutputFingerprint += "-changed" },
		"compiler schema":    func(b *Binding) { b.CompilerSchemaDigest += "-changed" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := base
			mutate(&changed)
			candidate := testCatalog(t, changed, "")
			if candidate.ID == original.ID {
				t.Fatal("binding mutation did not change identity")
			}
			candidateResolution := testPivot(t, candidate)
			if candidateResolution.ID == originalResolution.ID {
				t.Fatal("binding mutation did not change resolution identity")
			}
		})
	}
}

func TestRoleRefusalHasStableReasonAndCannotExposeChoices(t *testing.T) {
	binding := testBinding()
	base := testCatalog(t, binding, "")
	availability := clone(base.Availability)
	for i := range availability {
		if availability[i].Role == RolePivotGroup {
			availability[i] = RoleAvailability{Role: RolePivotGroup, State: AvailabilityRefused, ReasonCode: "GROUPS_UNSUPPORTED", Message: "Grouping is not available for this output."}
		}
	}
	choices := cloneCatalogChoices(base.Choices)
	filtered := choices.Columns[:0]
	for _, choice := range choices.Columns {
		if choice.Role != RolePivotGroup {
			filtered = append(filtered, choice)
		}
	}
	choices.Columns = filtered
	first, err := NewCatalogReceipt(binding, base.Columns, availability, choices, base.SavedShape, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewCatalogReceipt(binding, base.Columns, availability, choices, base.SavedShape, "different-created-at")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatal("refusal identity is not stable")
	}
	availability[0].Message = ""
	if _, err := NewCatalogReceipt(binding, base.Columns, availability, choices, base.SavedShape, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("refusal without message accepted: %v", err)
	}
}

func TestProposalIntentIsClosedAndRemoveCarriesNoShape(t *testing.T) {
	remove := ProposalIntent{Kind: IntentRemove, Remove: &RemoveIntent{}}
	if err := remove.Validate(); err != nil {
		t.Fatal(err)
	}
	remove.Set = &SetIntent{CatalogID: "tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	if err := remove.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("REMOVE carrying shape accepted: %v", err)
	}
	set := ProposalIntent{Kind: IntentSet, Set: &SetIntent{CatalogID: "tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ResolutionIDs: []string{"tsr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, Outputs: []NamedOutput{{Name: "total", Label: "Total"}}}}
	if err := set.Validate(); err != nil {
		t.Fatalf("valid SET references rejected: %v", err)
	}
	if _, err := DecodeStrict[ProposalIntent]([]byte(`{"kind":"SET","set":{"catalogId":"tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","resolutionIds":["tsr_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"],"choiceIds":["client-selected"]}}`)); err == nil {
		t.Fatal("SET accepted redundant client choice bags")
	}
}

func TestChoiceLookupIsRoleAndParentScoped(t *testing.T) {
	one := testCatalog(t, testBinding(), "")
	var groupID string
	for _, choice := range one.Choices.Columns {
		if choice.Role == RolePivotGroup {
			groupID = choice.ID
			break
		}
	}
	if _, err := one.FindColumn(RolePivotValue, groupID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong-role choice lookup error=%v", err)
	}
	twoBinding := testBinding()
	twoBinding.OutputID = "output-b"
	two := testCatalog(t, twoBinding, "")
	if _, err := two.FindColumn(RolePivotGroup, groupID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-receipt choice lookup error=%v", err)
	}
	resolution := testPivot(t, one)
	categoryChoiceID := resolution.Pivot.Categories[0].ChoiceID
	if _, err := resolution.Choice(RolePivotCategoryValue, categoryChoiceID); err != nil {
		t.Fatalf("child category choice lookup failed: %v", err)
	}
	if _, err := resolution.Choice(RolePivotValue, categoryChoiceID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong child choice role accepted: %v", err)
	}
	if _, err := testPivot(t, two).Choice(RolePivotCategoryValue, categoryChoiceID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-receipt category choice accepted: %v", err)
	}
	if err := ValidateResolutionAgainstCatalog(two, resolution); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-output resolution accepted: %v", err)
	}
	wrongParent := resolution
	wrongParent.ParentCatalogID = two.ID
	wrongParent.ID = ""
	wrongParent.ContentDigest = ""
	wrongParent, err := NewResolutionReceipt(wrongParent.Binding, wrongParent.ParentCatalogID, wrongParent.Kind, wrongParent.Pivot, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResolutionAgainstCatalog(one, wrongParent); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong-parent resolution accepted: %v", err)
	}
}

func TestPolicyChoicesMatchAcceptedCompilerContract(t *testing.T) {
	binding := testBinding()
	base := testCatalog(t, binding, "")
	for _, unsupported := range []struct {
		role   ChoiceRole
		policy string
	}{{RolePolicyDuplicate, "MEAN"}, {RolePolicyUnpivotNull, "KEEP"}} {
		choices := cloneCatalogChoices(base.Choices)
		for i := range choices.Policies {
			if choices.Policies[i].Role == unsupported.role {
				choices.Policies[i].PolicyID = unsupported.policy
				break
			}
		}
		if _, err := NewCatalogReceipt(binding, base.Columns, base.Availability, choices, base.SavedShape, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("unsupported %s policy %q accepted: %v", unsupported.role, unsupported.policy, err)
		}
	}
}

func TestPivotCompleteCategoryMembershipIsContentAddressed(t *testing.T) {
	catalog := testCatalog(t, testBinding(), "")
	resolution := testPivot(t, catalog)
	if err := ValidateResolutionAgainstCatalog(catalog, resolution); err != nil {
		t.Fatal(err)
	}
	mutated := resolution
	mutated.Pivot.Categories = append(clone(mutated.Pivot.Categories), FrozenCategory{Value: BooleanScalar(false), ChoiceID: ""})
	if err := mutated.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("membership mutation passed validation: %v", err)
	}
	mutated = resolution
	mutated.Pivot.CategoryProof.Complete = false
	if err := mutated.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("incomplete category proof passed validation: %v", err)
	}
}

func TestCategoryProofMustComeFromCompleteBoundedCompilerScan(t *testing.T) {
	catalog := testCatalog(t, testBinding(), "")
	base := testPivot(t, catalog)
	changedProof := *base.Pivot
	changedProof.CategoryProof.ScanFingerprint = "another-scan"
	withChangedProof, err := NewResolutionReceipt(catalog.Binding, catalog.ID, ResolutionPivot, &changedProof, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if withChangedProof.ID == base.ID {
		t.Fatal("scan fingerprint did not affect resolution identity")
	}

	cases := map[string]func(*CategoryProof){
		"incomplete":          func(p *CategoryProof) { p.Complete = false },
		"overflow":            func(p *CategoryProof) { p.Overflow = true },
		"bound too small":     func(p *CategoryProof) { p.MaxCategories = 1 },
		"wrong count":         func(p *CategoryProof) { p.DistinctCount++ },
		"wrong values digest": func(p *CategoryProof) { p.ValuesDigest = "sha256:wrong" },
		"missing query proof": func(p *CategoryProof) { p.QueryProof = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			changed := *base.Pivot
			mutate(&changed.CategoryProof)
			if _, err := NewResolutionReceipt(catalog.Binding, catalog.ID, ResolutionPivot, &changed, nil, nil, ""); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid proof accepted: %v", err)
			}
		})
	}
}

func TestDerivedOperandsAreNumericAndNestedRefsRequireEarlierReceipts(t *testing.T) {
	catalog := testCatalog(t, testBinding(), "")
	operatorID := catalog.Choices.Operators[0].ID
	baseChoiceID := catalog.Choices.Operands[0].ID
	missingPolicyID := ""
	for _, choice := range catalog.Choices.Policies {
		if choice.Role == RolePolicyDerivedMissing {
			missingPolicyID = choice.ID
		}
	}
	validLiteral := ResolvedOperand{Kind: ResolvedOperandLiteral, Literal: ptr(DecimalScalar(2.5))}
	for name, value := range map[string]Scalar{"string": StringScalar("2"), "boolean": BooleanScalar(false), "null": NullScalar(), "missing": MissingScalar()} {
		t.Run(name, func(t *testing.T) {
			operand := ResolvedOperand{Kind: ResolvedOperandLiteral, Literal: ptr(value)}
			if err := operand.Validate(); !errors.Is(err, ErrInvalid) {
				t.Fatalf("non-numeric literal accepted: %v", err)
			}
		})
	}
	for _, value := range []Scalar{IntegerScalar(0), DecimalScalar(0)} {
		if err := (ResolvedOperand{Kind: ResolvedOperandLiteral, Literal: ptr(value)}).Validate(); err != nil {
			t.Fatalf("numeric zero literal rejected: %v", err)
		}
	}

	first, err := NewResolutionReceipt(catalog.Binding, catalog.ID, ResolutionDerived, nil, nil, &DerivedResolution{
		OperatorChoiceID: operatorID, Left: ResolvedOperand{Kind: ResolvedOperandCatalogChoice, ChoiceID: baseChoiceID}, Right: validLiteral,
		Result: TypeFact{LogicalType: LogicalDecimal, UnitIdentity: "mg"}, MissingPolicyChoiceID: missingPolicyID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResolutionAgainstCatalog(catalog, first); err != nil {
		t.Fatalf("base derived receipt rejected: %v", err)
	}
	zero := 0
	second, err := NewResolutionReceipt(catalog.Binding, catalog.ID, ResolutionDerived, nil, nil, &DerivedResolution{
		OperatorChoiceID: operatorID, Left: ResolvedOperand{Kind: ResolvedOperandResolution, ResolutionID: first.ID, OutputIndex: &zero}, Right: ResolvedOperand{Kind: ResolvedOperandCatalogChoice, ChoiceID: baseChoiceID},
		Result: TypeFact{LogicalType: LogicalDecimal, UnitIdentity: "mg"}, MissingPolicyChoiceID: missingPolicyID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResolutionAgainstCatalog(catalog, second); !errors.Is(err, ErrInvalid) {
		t.Fatalf("prior ref accepted without receipt: %v", err)
	}
	if err := ValidateResolutionAgainstCatalog(catalog, second, first); err != nil {
		t.Fatalf("ordered nested derived ref rejected: %v", err)
	}
	wrongBinding := testBinding()
	wrongBinding.OutputID = "other-output"
	wrongPrior, err := NewResolutionReceipt(wrongBinding, catalog.ID, ResolutionDerived, nil, nil, &DerivedResolution{
		OperatorChoiceID: operatorID, Left: ResolvedOperand{Kind: ResolvedOperandCatalogChoice, ChoiceID: baseChoiceID}, Right: validLiteral,
		Result: TypeFact{LogicalType: LogicalDecimal}, MissingPolicyChoiceID: missingPolicyID,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateResolutionAgainstCatalog(catalog, second, wrongPrior); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-output prior resolution accepted: %v", err)
	}
}

func TestScalarTaggedUnionPreservesAllTypedValues(t *testing.T) {
	values := []Scalar{StringScalar(""), IntegerScalar(0), DecimalScalar(0), BooleanScalar(false), NullScalar(), MissingScalar()}
	seen := map[string]struct{}{}
	for _, value := range values {
		if err := value.Validate(); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := seen[string(encoded)]; ok {
			t.Fatalf("typed value identity collided: %s", encoded)
		}
		seen[string(encoded)] = struct{}{}
		decoded, err := DecodeStrict[Scalar](encoded)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(value, decoded) {
			t.Fatalf("round-trip=%#v want %#v", decoded, value)
		}
	}
	bad := DecimalScalar(math.NaN())
	if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NaN accepted: %v", err)
	}
	bad = DecimalScalar(math.Inf(1))
	if err := bad.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("infinite decimal accepted: %v", err)
	}
	if _, err := DecodeStrict[Scalar]([]byte(`{"kind":"NULL","string":"unexpected"}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid scalar union accepted: %v", err)
	}
	if _, err := DecodeStrict[Scalar]([]byte(`{"kind":"NULL","extra":true}`)); err == nil {
		t.Fatal("unknown scalar field accepted")
	}
}

func TestReceiptDecodingRejectsUnknownFieldsAndWrongResolutionUnion(t *testing.T) {
	catalog := testCatalog(t, testBinding(), "")
	resolution := testPivot(t, catalog)
	raw, err := json.Marshal(resolution)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	object["unexpected"] = true
	withUnknown, _ := json.Marshal(object)
	if _, err := DecodeStrict[ResolutionReceipt](withUnknown); err == nil {
		t.Fatal("unknown receipt field accepted")
	}
	delete(object, "unexpected")
	object["unpivot"] = map[string]any{"inputColumnChoiceIds": []string{"x", "y"}, "keyResult": map[string]any{"logicalType": "STRING"}, "valueResult": map[string]any{"logicalType": "STRING"}, "nullPolicyChoiceId": "x"}
	wrongUnion, _ := json.Marshal(object)
	if _, err := DecodeStrict[ResolutionReceipt](wrongUnion); !errors.Is(err, ErrInvalid) {
		t.Fatalf("multiple payload arms accepted: %v", err)
	}
}

func TestReadValidationRejectsTamperedContentAddress(t *testing.T) {
	catalog := testCatalog(t, testBinding(), "")
	data, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	object["binding"].(map[string]any)["outputId"] = "other-output"
	tampered, _ := json.Marshal(object)
	if _, err := DecodeStrict[CatalogReceipt](tampered); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered content address accepted: %v", err)
	}
	badID := catalog
	badID.ID = "tsc_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := badID.Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered ID accepted: %v", err)
	}
}
