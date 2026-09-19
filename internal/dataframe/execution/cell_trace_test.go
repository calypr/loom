package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestCellTraceCompiledFindsPublishedDefaultIdentityAndReturnsEvidence(t *testing.T) {
	parts := []any{"Patient/123", "generation-a"}
	encoded, _ := json.Marshal(parts)
	digest := sha256.Sum256(encoded)
	rowID := hex.EncodeToString(digest[:])
	query := compiler.CompiledCellTraceQuery{
		Query: "trace", BindVars: map[string]any{"project": "p"}, ValueColumn: "value", ContributionsColumn: "contributors",
		StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", IdentityPartsColumn: "identity",
		RowIdentity: &spec.RowIdentity{Fields: []string{"id", "generation"}}, ContributionLimit: 1,
	}
	engine := &Engine{queryRows: func(_ context.Context, queryText string, _ int, bindVars map[string]any, visit func(map[string]any) error) error {
		if queryText != "trace" || bindVars["project"] != "p" {
			t.Fatalf("unexpected query invocation: %q %#v", queryText, bindVars)
		}
		for _, row := range []map[string]any{
			{"identity": []any{"other", "generation-a"}},
			{"identity": parts, "value": "female", "status": "AMBIGUOUS", "hasMore": true, "omission": "", "contributors": []any{map[string]any{"resourceType": "Patient", "resourceId": "123", "value": "female"}}},
		} {
			if err := visit(row); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: rowID, Column: "gender", Offset: 5, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Complete || result.Status != CellTraceAmbiguous || result.Value != "female" || !result.HasMore || result.NextOffset != 6 {
		t.Fatalf("unexpected trace result: %#v", result)
	}
	if len(result.Contributions) != 1 || result.Contributions[0].ResourceID != "123" {
		t.Fatalf("unexpected contributions: %#v", result.Contributions)
	}
}

func TestCellTraceCompiledMatchesExplicitPublishedIdentity(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		return visit(map[string]any{"identity": "patient-7", "value": nil, "status": "NO_MATCH", "contributors": []any{}, "hasMore": false})
	}}
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "patient-7", Column: "condition", Limit: 10})
	if err != nil || result.Status != CellTraceNoMatch || !result.Complete {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCellTraceCompiledReportsWitnessBoundAsIncomplete(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
		for _, id := range []string{"one", "two"} {
			if err := visit(map[string]any{"identity": id}); err != nil {
				return err
			}
		}
		return nil
	}}
	result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "later", Column: "gender", MaxWitnessRows: 1})
	if err != nil || result.Complete || result.Status != CellTraceIncomplete || result.OmissionCode == "" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestCellTraceCompiledReturnsTypedSemanticFailureStatuses(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	for _, test := range []struct {
		code   dataframeerrors.ErrorCode
		status CellTraceStatus
	}{
		{code: dataframeerrors.CodeTemporalTieAmbiguous, status: CellTraceAmbiguous},
		{code: dataframeerrors.CodeUnitDimensionIncompatible, status: CellTraceInvalidUnit},
		{code: dataframeerrors.CodeInvalidData, status: CellTraceInvalidType},
	} {
		t.Run(string(test.code), func(t *testing.T) {
			engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error {
				return dataframeerrors.NewError(test.code, "")
			}}
			result, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "patients", RowID: "row-1", Column: "feature"})
			if err != nil || !result.Complete || result.Status != test.status || result.OmissionCode != string(test.code) || result.Contributions == nil {
				t.Fatalf("result=%#v err=%v", result, err)
			}
		})
	}
}

func TestCellTraceCompiledRejectsMalformedEvidenceAndMissingRows(t *testing.T) {
	query := compiler.CompiledCellTraceQuery{Query: "trace", ValueColumn: "value", ContributionsColumn: "contributors", StatusColumn: "status", HasMoreColumn: "hasMore", OmissionColumn: "omission", ExplicitIdentityColumn: "identity"}
	t.Run("malformed", func(t *testing.T) {
		engine := &Engine{queryRows: func(_ context.Context, _ string, _ int, _ map[string]any, visit func(map[string]any) error) error {
			return visit(map[string]any{"identity": "row", "status": "MAYBE", "contributors": []any{}})
		}}
		if _, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "o", RowID: "row", Column: "c"}); err == nil {
			t.Fatal("expected malformed trace error")
		}
	})
	t.Run("missing", func(t *testing.T) {
		engine := &Engine{queryRows: func(context.Context, string, int, map[string]any, func(map[string]any) error) error { return nil }}
		_, err := engine.CellTraceCompiled(context.Background(), query, CellTraceRequest{Output: "o", RowID: "row", Column: "c"})
		if err == nil || errors.Is(err, errCellTraceFound) {
			t.Fatalf("missing row error = %v", err)
		}
	})
}
