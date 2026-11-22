package compilation

import (
	"encoding/json"
	"github.com/calypr/loom/internal/dataframe/compiler/facade"
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/explorer"
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
	var stored explorer.ReceiptConstructionStage
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stage, stored) {
		t.Fatalf("stage changed across receipt JSON: before=%#v after=%#v", stage, stored)
	}
}
