package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCorrelatedSelectionScopeRequiresExplicitCodingVersion(t *testing.T) {
	binding := CorrelatedBinding{
		KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", VersionPath: "version",
		ValueScope: CorrelatedValueScopeCoding, SelectionScope: CorrelatedSelectionScopeSystemVersion,
		ValuePath: "display", ValueFallback: []string{"code"}, LogicalType: "string",
	}
	checked, err := ValidateCorrelatedBinding("Condition", binding)
	if err != nil {
		t.Fatal(err)
	}
	if checked.SelectionScope != CorrelatedSelectionScopeSystemVersion || checked.VersionSelector == nil {
		t.Fatalf("checked category binding = %#v", checked)
	}
	for name, mutate := range map[string]func(*CorrelatedBinding){
		"missing version": func(b *CorrelatedBinding) { b.VersionPath = "" },
		"owner value": func(b *CorrelatedBinding) {
			b.ValueScope, b.ValuePath, b.VersionPath = CorrelatedValueScopeOwner, "code.text", ""
			b.ValueFallback = nil
		},
		"unknown selection":     func(b *CorrelatedBinding) { b.SelectionScope = "ALL_CODES" },
		"wrong system selector": func(b *CorrelatedBinding) { b.SystemPath = "display" },
		"wrong code selector":   func(b *CorrelatedBinding) { b.CodePath = "display" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := binding
			mutate(&invalid)
			if _, err := ValidateCorrelatedBinding("Condition", invalid); err == nil {
				t.Fatal("invalid category binding accepted")
			}
		})
	}
	for _, version := range []string{"", "v1"} {
		if err := ValidateCorrelatedKey(CorrelatedSelectionScopeSystemVersion, CorrelatedKey{System: "urn:test", Version: version}); err != nil {
			t.Fatalf("category version %q: %v", version, err)
		}
	}
	for _, test := range []struct {
		scope CorrelatedSelectionScope
		key   CorrelatedKey
	}{
		{key: CorrelatedKey{System: "urn:test"}},
		{scope: CorrelatedSelectionScopeExact, key: CorrelatedKey{System: "urn:test"}},
		{scope: CorrelatedSelectionScopeSystemVersion, key: CorrelatedKey{System: "urn:test", Code: "normal"}},
		{scope: CorrelatedSelectionScopeSystemVersion},
	} {
		if err := ValidateCorrelatedKey(test.scope, test.key); err == nil {
			t.Fatalf("invalid selection accepted: %#v", test)
		}
	}
}

func TestCorrelatedSelectionScopePreservesLegacyBindingJSON(t *testing.T) {
	for name, test := range map[string]struct {
		binding CorrelatedBinding
		want    string
	}{
		"CODED_CONCEPT_V1": {
			binding: CorrelatedBinding{KeyPath: "code.coding[]", VersionPath: "version", ValueScope: CorrelatedValueScopeCoding, SystemPath: "system", CodePath: "code", ValuePath: "display", ValueFallback: []string{"code"}, FallbackOnBlankString: true, LogicalType: "string"},
			want:    `{"keyPath":"code.coding[]","versionPath":"version","valueScope":"CODING","systemPath":"system","codePath":"code","valuePath":"display","valueFallback":["code"],"fallbackOnBlankString":true,"logicalType":"string"}`,
		},
		"CODED_VALUE_V1": {
			binding: CorrelatedBinding{KeyPath: "code.coding[]", SystemPath: "system", CodePath: "code", ValuePath: "valueQuantity.value", LogicalType: "decimal"},
			want:    `{"keyPath":"code.coding[]","systemPath":"system","codePath":"code","valuePath":"valueQuantity.value","logicalType":"decimal"}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(test.binding)
			if err != nil || string(encoded) != test.want {
				t.Fatalf("legacy binding JSON = %s, error=%v, want %s", encoded, err, test.want)
			}
			test.binding.SelectionScope = CorrelatedSelectionScopeSystemVersion
			encoded, err = json.Marshal(test.binding)
			if err != nil || !strings.Contains(string(encoded), `"selectionScope":"SYSTEM_VERSION"`) {
				t.Fatalf("explicit category JSON = %s, error=%v", encoded, err)
			}
			var decoded CorrelatedBinding
			if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.SelectionScope != CorrelatedSelectionScopeSystemVersion {
				t.Fatalf("category JSON round-trip = %#v, error=%v", decoded, err)
			}
		})
	}
}
