package loomapi_test

import (
	"testing"

	loomapi "github.com/calypr/loom/api/loomapi"
)

func TestGeneratedEnumNullMembersValidateAsNullNotNilPlaceholder(t *testing.T) {
	tests := []struct {
		name      string
		nullValue string
		nullValid bool
		nilValid  bool
		missing   string
		missingOk bool
	}{
		{
			name:      "ConstructionTableScalarKind",
			nullValue: string(loomapi.ConstructionTableScalarKindNULL),
			nullValid: loomapi.ConstructionTableScalarKindNULL.Valid(),
			nilValid:  loomapi.ConstructionTableScalarKind("<nil>").Valid(),
			missing:   string(loomapi.ConstructionTableScalarKindMISSING),
			missingOk: loomapi.ConstructionTableScalarKindMISSING.Valid(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.nullValue != "NULL" {
				t.Fatalf("NULL enum member = %q, want %q", tc.nullValue, "NULL")
			}
			if !tc.nullValid {
				t.Fatalf("%q must be a valid %s member", tc.nullValue, tc.name)
			}
			if tc.nilValid {
				t.Fatalf("%q must not be a valid %s member; NULL must not serialize as YAML null", "<nil>", tc.name)
			}
			if tc.missing != "MISSING" || !tc.missingOk {
				t.Fatalf("%s MISSING member = %q (valid=%v), want valid %q", tc.name, tc.missing, tc.missingOk, "MISSING")
			}
		})
	}
}

func TestGeneratedEnumConstructionExtensionsValidate(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected string
		valid    bool
	}{
		{
			name:     "ConstructionChoiceProposalSelectionForm COUNT",
			value:    string(loomapi.ConstructionChoiceProposalSelectionFormCOUNT),
			expected: "COUNT",
			valid:    loomapi.ConstructionChoiceProposalSelectionFormCOUNT.Valid(),
		},
		{
			name:     "ConstructionChoiceProposalSelectionForm PRESENCE",
			value:    string(loomapi.ConstructionChoiceProposalSelectionFormPRESENCE),
			expected: "PRESENCE",
			valid:    loomapi.ConstructionChoiceProposalSelectionFormPRESENCE.Valid(),
		},
		{
			name:     "ConstructionInputRefKind WORKSPACE_OUTPUT",
			value:    string(loomapi.ConstructionInputRefKindWORKSPACEOUTPUT),
			expected: "WORKSPACE_OUTPUT",
			valid:    loomapi.ConstructionInputRefKindWORKSPACEOUTPUT.Valid(),
		},
		{
			name:     "ConstructionCategoryDiscoveryResponseOutcome NO_VALUES",
			value:    string(loomapi.ConstructionCategoryDiscoveryResponseOutcomeNOVALUES),
			expected: "NO_VALUES",
			valid:    loomapi.ConstructionCategoryDiscoveryResponseOutcomeNOVALUES.Valid(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.value != tc.expected {
				t.Fatalf("generated value = %q, want %q", tc.value, tc.expected)
			}
			if !tc.valid {
				t.Fatalf("%q must be a valid generated enum member", tc.value)
			}
		})
	}
}
