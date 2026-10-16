package lifecycle

import (
	"testing"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
)

func TestFrameSourceFormsExposeOnlyCompilerSupportedForms(t *testing.T) {
	forms := supportedFrameForms([]capability.ConstructionChoiceOption{
		{Form: capability.ConstructionChoiceValue, Decision: capability.ConstructionChoiceDefault, Support: capability.ConstructionChoiceSupported},
		{Form: capability.ConstructionChoiceFirst, Support: capability.ConstructionChoiceSupport("UNSUPPORTED")},
		{Form: capability.ConstructionChoiceAll, Decision: capability.ConstructionChoiceDefault, Support: capability.ConstructionChoiceSupported},
		{Form: capability.ConstructionChoiceDistinct, Decision: capability.ConstructionChoiceRequiresDecision, Support: capability.ConstructionChoiceSupported},
	})
	if len(forms) != 3 || forms[0].Form != capability.ConstructionChoiceValue || forms[1].Form != capability.ConstructionChoiceAll || forms[2].Form != capability.ConstructionChoiceDistinct {
		t.Fatalf("forms = %#v, want only compiler-supported VALUE, ALL, and DISTINCT", forms)
	}
	if form, ok := preservingFrameDefault(forms); !ok || form != capability.ConstructionChoiceValue {
		t.Fatalf("compiler-marked VALUE form was not recognized as the preserving default: form=%q forms=%#v", form, forms)
	}
	if form, ok := preservingFrameDefault([]FrameSourceForm{{Form: capability.ConstructionChoiceAll, ZeroPolicy: authoringv2.FrameZeroEmptyList, ManyPolicy: authoringv2.FrameManyAll, Decision: string(capability.ConstructionChoiceDefault)}}); !ok || form != capability.ConstructionChoiceAll {
		t.Fatalf("compiler-marked ALL was not accepted as a preserving default: form=%q ok=%t", form, ok)
	}
	if _, ok := preservingFrameDefault([]FrameSourceForm{{Form: capability.ConstructionChoiceFirst, ZeroPolicy: authoringv2.FrameZeroNull, ManyPolicy: authoringv2.FrameManyFirst, Decision: string(capability.ConstructionChoiceDefault)}}); ok {
		t.Fatal("lossy FIRST must not be accepted as a preserving default")
	}
}
