package compilation

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestSemanticCategoricalPivotKeepsSystemAsBoundNamespace(t *testing.T) {
	source := authoringv2.ColumnSource{
		Kind: authoringv2.SourceCategoricalBySystem,
		Categorical: &authoringv2.CategoricalSource{
			System: "urn:example:a",
			Binding: fhirschema.CategoricalBinding{
				OwnerPath: "code", KeyPath: "code.coding[]", SystemPath: "system", ValuePath: "code",
				ValueFallback: []string{"display"}, LogicalType: "string", ValuePresentation: fhirschema.ValuePresentationDisplayOrCode,
			},
		},
	}
	pivot, err := semanticCategoricalPivot(authoringv2.Column{Column: "category", Source: source}, "category")
	if err != nil {
		t.Fatal(err)
	}
	if pivot.Categorical == nil || pivot.CategoricalSystem != "urn:example:a" || len(pivot.Columns) != 1 || pivot.Columns[0] != "urn:example:a" {
		t.Fatalf("categorical pivot = %#v", pivot)
	}
	if pivot.Correlation != nil || pivot.CorrelationCode != "" || pivot.ColumnAliases["urn:example:a"] != "category" {
		t.Fatalf("categorical pivot acquired coded-value identity = %#v", pivot)
	}
}
