package compilation

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/dataframe/compiler/facade"
)

func TestConstructionStageWithoutRelatedAnchorsSurvivesReceiptJSON(t *testing.T) {
	stage, err := ReceiptConstructionStageFromDescriptor(facade.CompiledStageDescriptor{ID: "group", Operation: "GROUP"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stage)
	if err != nil {
		t.Fatal(err)
	}
	var stored receiptmodel.ReceiptConstructionStage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stage, stored) {
		t.Fatalf("stage changed across receipt JSON: before=%#v after=%#v", stage, stored)
	}
}
