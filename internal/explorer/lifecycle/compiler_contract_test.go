package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
)

func TestPreviousDynamicInventoryCompilerReceiptCannotExecute(t *testing.T) {
	snapshot := readySnapshot("project-a", "generation-a", "token", authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted})
	receipt := nativeReceipt(snapshot)
	receipt.CompilerContractVersion = "loom.explorer.compiler/v20"
	var err error
	receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = explorer.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.Validate(); err != nil {
		t.Fatalf("historical generic receipt must remain readable: %v", err)
	}
	service := &Service{config: Config{CompileReceipt: func(context.Context, CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		t.Fatal("must reject without recompiling")
		return nil, nil
	}}}
	err = service.validateReceiptRoute(receipt, "project-a", "patients")
	var typed *Error
	if !errors.As(err, &typed) || typed.Code != "RECEIPT_RECOMPILE_REQUIRED" {
		t.Fatalf("historical compiler receipt was executable: %v", err)
	}
}
