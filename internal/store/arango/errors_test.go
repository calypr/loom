package arango

// Error-boundary acceptance for the ArangoDB adapter. Golden literals in this
// file were captured from the server composition classifier at base
// 7c422ee8 (arch-backend-03 verbatim-port condition): codes, public messages,
// and details must not change when vendor interpretation moves here.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	driver "github.com/arangodb/go-driver/v2/arangodb"
	"github.com/arangodb/go-driver/v2/arangodb/shared"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

type translateGolden struct {
	name        string
	driverErr   shared.ArangoError
	wantCode    string
	wantPublic  string
	wantDetails map[string]any
}

// translateGoldens are the frozen public semantics of the 14 classification
// branches. wantPublic is the message the classifier produced pre-move
// (dataframeerrors defaultMessage for the code, since the branch passes "").
var translateGoldens = []translateGolden{
	{
		name:       "pivot_cell_cardinality",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: TABLE_PIVOT_CELL_CARDINALITY (while executing)"},
		wantCode:   "TABLE_PIVOT_CELL_CARDINALITY",
		wantPublic: "More than one record matched a Pivot cell; choose how to handle duplicates or filter the input rows.",
	},
	{
		name:       "pivot_unlisted_category",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: TABLE_PIVOT_UNLISTED_CATEGORY (while executing)"},
		wantCode:   "TABLE_PIVOT_UNLISTED_CATEGORY",
		wantPublic: "Pivot found an unlisted category; select all discovered categories or filter rows before Pivot.",
	},
	{
		name:       "grouped_row_multiple_values",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES (while executing)"},
		wantCode:   "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES",
		wantPublic: "Some grouped records have different values for this column. Keep all distinct values or choose more specific grouping fields.",
	},
	{
		name:       "construction_expansion_empty",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: CONSTRUCTION_EXPANSION_EMPTY: related expansion construction related_1 has no related records for row row-17 (while executing)"},
		wantCode:   "CONSTRUCTION_EXPANSION_EMPTY",
		wantPublic: "At least one row has an empty list or no matching related records. Choose 'Drop the original row' or 'Keep the row with a missing item' for lists, or 'Leave that current row out' or 'Keep that current row once, with no related record ID' for related records.",
	},
	{
		name:       "explicit_group_unassigned_member",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: EXPLICIT_GROUP_UNASSIGNED_MEMBER (while executing)"},
		wantCode:   "EXPLICIT_GROUP_UNASSIGNED_MEMBER",
		wantPublic: "Some records do not belong to a group. Choose 'Leave out records without a group' or 'Put records without a group in their own group', or assign them to a group.",
	},
	{
		name:       "relationship_cardinality_violation",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: RELATIONSHIP_CARDINALITY_VIOLATION (while executing)"},
		wantCode:   "RELATIONSHIP_CARDINALITY_VIOLATION",
		wantPublic: "more than one value matched a feature that requires zero or one",
	},
	{
		name:       "temporal_anchor_invalid",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: TEMPORAL_ANCHOR_INVALID (while executing)"},
		wantCode:   "TEMPORAL_ANCHOR_INVALID",
		wantPublic: "the row date required by a date-aware feature is missing or is not a complete instant",
	},
	{
		name:       "temporal_precision_unsupported",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: TEMPORAL_PRECISION_UNSUPPORTED (while executing)"},
		wantCode:   "TEMPORAL_PRECISION_UNSUPPORTED",
		wantPublic: "a record date required by a date-aware feature is missing or is not a complete instant",
	},
	{
		name:       "temporal_tie_ambiguous",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: TEMPORAL_TIE_AMBIGUOUS (while executing)"},
		wantCode:   "TEMPORAL_TIE_AMBIGUOUS",
		wantPublic: "multiple values share the selected date; choose how equal dates should be resolved",
	},
	{
		name:       "unit_identity_unknown",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: UNIT_IDENTITY_UNKNOWN (while executing)"},
		wantCode:   "UNIT_IDENTITY_UNKNOWN",
		wantPublic: "a measurement has no approved source unit identity",
	},
	{
		name:       "unit_dimension_incompatible",
		driverErr:  shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert, ErrorMessage: "AQL: UNIT_DIMENSION_INCOMPATIBLE (while executing)"},
		wantCode:   "UNIT_DIMENSION_INCOMPATIBLE",
		wantPublic: "a measurement unit is dimensionally incompatible with the feature target",
	},
	{
		name:        "query_memory_limit",
		driverErr:   shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed"},
		wantCode:    "QUERY_MEMORY_LIMIT_EXCEEDED",
		wantPublic:  "the dataframe query exceeded the database's configured memory limit",
		wantDetails: map[string]any{"backend": "arangodb", "resource": "query_memory"},
	},
	{
		name:        "query_resource_limit",
		driverErr:   shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "resource limit exceeded"},
		wantCode:    "QUERY_RESOURCE_LIMIT_EXCEEDED",
		wantPublic:  "the dataframe query exceeded a configured database resource limit",
		wantDetails: map[string]any{"backend": "arangodb", "resource": "query"},
	},
	{
		name:        "backend_out_of_memory",
		driverErr:   shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrOutOfMemory, ErrorMessage: "out of memory"},
		wantCode:    "QUERY_BACKEND_OUT_OF_MEMORY",
		wantPublic:  "the dataframe database ran out of memory while executing the query",
		wantDetails: map[string]any{"backend": "arangodb", "resource": "memory"},
	},
}

func TestTranslateQueryErrorGoldenTablePreservesPreMoveSemantics(t *testing.T) {
	if len(translateGoldens) != 14 {
		t.Fatalf("golden table has %d branches, want 14 (11 assertions + 3 resource limits)", len(translateGoldens))
	}
	for _, golden := range translateGoldens {
		t.Run(golden.name, func(t *testing.T) {
			wrapped := fmt.Errorf("arango query: %w", golden.driverErr)
			translated, ok := TranslateQueryError(wrapped)
			if !ok {
				t.Fatalf("TranslateQueryError(%v) = ok false, want classified", golden.driverErr)
			}
			userErr, isUser := dataframeerrors.AsUserError(translated)
			if !isUser {
				t.Fatalf("translated error is not a Loom user error: %v", translated)
			}
			if userErr.Code() != golden.wantCode {
				t.Fatalf("code = %q, want %q", userErr.Code(), golden.wantCode)
			}
			if got := dataframeerrors.PublicMessage(translated); got != golden.wantPublic {
				t.Fatalf("public message = %q, want %q", got, golden.wantPublic)
			}
			if userErr.Retryable() {
				t.Fatal("classified error must not be retryable")
			}
			if golden.wantDetails == nil {
				if len(userErr.Details()) != 0 {
					t.Fatalf("details = %v, want none", userErr.Details())
				}
			} else if !reflect.DeepEqual(userErr.Details(), golden.wantDetails) {
				t.Fatalf("details = %v, want %v", userErr.Details(), golden.wantDetails)
			}
			// Cause identity: the driver error stays reachable through
			// errors.Is/errors.As exactly as the deleted server classifier
			// preserved it.
			if !errors.Is(translated, golden.driverErr) {
				t.Fatal("driver cause lost through errors.Is")
			}
			var fromChain shared.ArangoError
			if !errors.As(translated, &fromChain) || fromChain.ErrorNum != golden.driverErr.ErrorNum {
				t.Fatalf("driver cause lost through errors.As: %v", fromChain)
			}
		})
	}
}

func TestTranslateQueryErrorNeverInfersDomainCodeFromUnrecognizedAssert(t *testing.T) {
	unrecognized := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrQueryUserAssert,
		ErrorMessage: "AQL: row expansion construction expand_tags has no items for row row-17 (while executing)",
	}
	if got, ok := TranslateQueryError(unrecognized); ok || got != error(unrecognized) {
		t.Fatalf("TranslateQueryError() = (%v, %t), want unrecognized ASSERT returned untouched", got, ok)
	}
	// A message merely containing a domain code without the AQL ASSERT
	// structure must not classify.
	textOnly := errors.New("AQL: RELATIONSHIP_CARDINALITY_VIOLATION")
	if got, ok := TranslateQueryError(textOnly); ok || got != error(textOnly) {
		t.Fatalf("text-only error classified: (%v, %t)", got, ok)
	}
}

func TestTranslateQueryErrorIdempotenceAndPassThrough(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if translated, ok := TranslateQueryError(nil); translated != nil || ok {
			t.Fatalf("TranslateQueryError(nil) = (%v, %t), want (nil, false)", translated, ok)
		}
	})
	t.Run("existing_user_error_untouched", func(t *testing.T) {
		userErr := dataframeerrors.NewError(dataframeerrors.CodeInvalidLimit, "")
		translated, ok := TranslateQueryError(userErr)
		if !ok || translated != error(userErr) {
			t.Fatalf("existing user error rewritten: (%v, %t)", translated, ok)
		}
	})
	t.Run("unrecognized_identity_preserved", func(t *testing.T) {
		want := errors.New("visitor failed")
		translated, ok := TranslateQueryError(want)
		if ok || translated != want {
			t.Fatalf("unrecognized error changed: (%v, %t)", translated, ok)
		}
	})
	t.Run("context_sentinels_pass_through", func(t *testing.T) {
		for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded} {
			if translated, ok := TranslateQueryError(sentinel); ok || translated != sentinel {
				t.Fatalf("context sentinel %v classified: (%v, %t)", sentinel, translated, ok)
			}
			wrapped := fmt.Errorf("wrap: %w", sentinel)
			translated, ok := TranslateQueryError(wrapped)
			if ok || !errors.Is(translated, sentinel) {
				t.Fatalf("wrapped context sentinel changed identity: (%v, %t)", translated, ok)
			}
		}
	})
	t.Run("double_application_stable", func(t *testing.T) {
		driverErr := shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed"}
		once, ok := TranslateQueryError(fmt.Errorf("arango query: %w", driverErr))
		if !ok {
			t.Fatal("first classification failed")
		}
		twice, ok := TranslateQueryError(once)
		if !ok || twice != once {
			t.Fatalf("double application changed the error: %#v vs %#v", twice, once)
		}
	})
}

// choke-point witnesses: classification must apply on all three row-query
// entry paths independently, not just the one production happens to use.

func resourceLimitDriverError() shared.ArangoError {
	return shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "AQL: query would use more memory than allowed"}
}

func wantClassifiedResourceLimit(t *testing.T, err error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: error lost", path)
	}
	userErr, ok := dataframeerrors.AsUserError(err)
	if !ok || userErr.Code() != string(dataframeerrors.CodeQueryMemoryLimitExceeded) {
		t.Fatalf("%s: error %#v not classified by the adapter seam", path, err)
	}
	if !errors.Is(err, resourceLimitDriverError()) {
		t.Fatalf("%s: driver cause lost through errors.Is", path)
	}
}

func TestQueryRowsClassifiesAtSeam(t *testing.T) {
	queryer := &fakeQueryer{err: fmt.Errorf("driver: %w", resourceLimitDriverError())}
	err := queryRows(context.Background(), queryer, "RETURN 1", 1, nil, func(map[string]any) error { return nil })
	wantClassifiedResourceLimit(t, err, "QueryRows")
}

func TestQueryRowsWithMaxRuntimeClassifiesAtSeam(t *testing.T) {
	queryer := &fakeQueryer{err: fmt.Errorf("driver: %w", resourceLimitDriverError())}
	err := queryRowsWithMaxRuntime(context.Background(), queryer, "RETURN 1", 1, nil, time.Second, func(map[string]any) error { return nil })
	wantClassifiedResourceLimit(t, err, "QueryRowsWithMaxRuntime")
}

type fakeDatabase struct {
	driver.Database
	queryer driver.DatabaseQuery
}

func (d fakeDatabase) Query(ctx context.Context, _ string, options *driver.QueryOptions) (driver.Cursor, error) {
	return d.queryer.Query(ctx, "", options)
}

func TestTransactionClientQueryRowsClassifiesAtSeam(t *testing.T) {
	queryer := &fakeQueryer{err: fmt.Errorf("driver: %w", resourceLimitDriverError())}
	client := fakeDatabase{queryer: queryer}
	transaction := transactionClient{queryer: client}
	err := transaction.QueryRows(context.Background(), "RETURN 1", 1, nil, func(map[string]any) error { return nil })
	wantClassifiedResourceLimit(t, err, "transaction RowQueryer.QueryRows")
}

// Condition 2 also requires the two public Client entry points to classify;
// they route through the same seam but through the exported surface callers
// actually use.

func TestClientQueryRowsClassifiesThroughExportedSurface(t *testing.T) {
	queryer := &fakeQueryer{err: fmt.Errorf("driver: %w", resourceLimitDriverError())}
	client := &Client{db: fakeDatabase{queryer: queryer}}
	err := client.QueryRows(context.Background(), "RETURN 1", 1, nil, func(map[string]any) error { return nil })
	wantClassifiedResourceLimit(t, err, "Client.QueryRows")
}

func TestClientQueryRowsWithMaxRuntimeClassifiesThroughExportedSurface(t *testing.T) {
	queryer := &fakeQueryer{err: fmt.Errorf("driver: %w", resourceLimitDriverError())}
	client := &Client{db: fakeDatabase{queryer: queryer}}
	err := client.QueryRowsWithMaxRuntime(context.Background(), "RETURN 1", 1, nil, time.Second, func(map[string]any) error { return nil })
	wantClassifiedResourceLimit(t, err, "Client.QueryRowsWithMaxRuntime")
}

func TestQueryErrorBoundaryJoinsIncludeClassification(t *testing.T) {
	// A read failure classified by the boundary joined with a close failure
	// still classifies: the seam translates after the join.
	readErr := resourceLimitDriverError()
	cursor := &fakeCursor{rows: []map[string]any{{"id": "1"}}, readErr: fmt.Errorf("read: %w", readErr), closeErr: errors.New("close failed")}
	queryer := &fakeQueryer{cursor: cursor}
	err := queryRows(context.Background(), queryer, "RETURN 1", 1, nil, func(map[string]any) error { return nil })
	wantClassifiedResourceLimit(t, err, "joined read+close")
	if !errors.Is(err, cursor.closeErr) {
		t.Fatal("close failure lost")
	}
}

func TestTranslateQueryErrorNeverLeaksVendorMessageIntoPublicError(t *testing.T) {
	driverErr := shared.ArangoError{
		HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit,
		ErrorMessage: `AQL: memory limit exceeded while binding {"secret":"hunter2","bind":"RETURN SECRET_DO_NOT_RENDER"}`,
	}
	translated, ok := TranslateQueryError(fmt.Errorf("arango query: %w", driverErr))
	if !ok {
		t.Fatal("driver failure not classified")
	}
	encoded, err := json.Marshal(translated)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"hunter2", "SECRET_DO_NOT_RENDER", "RETURN", "bind"} {
		if strings.Contains(string(encoded), forbidden) || strings.Contains(translated.Error(), forbidden) {
			t.Fatalf("public rendering leaked %q: %s / %v", forbidden, encoded, translated)
		}
	}
}

func TestDiagnosticCauseGoldenStrings(t *testing.T) {
	t.Run("value_with_message", func(t *testing.T) {
		err := fmt.Errorf("arango query: %w", shared.ArangoError{HasError: true, Code: 500, ErrorNum: shared.ErrResourceLimit, ErrorMessage: "boom"})
		cause, ok := DiagnosticCause(err)
		if !ok {
			t.Fatal("DiagnosticCause missed the driver error")
		}
		if want := "ArangoDB error number 32 (HTTP 500): boom"; cause != want {
			t.Fatalf("cause = %q, want %q", cause, want)
		}
	})
	t.Run("pointer_without_message", func(t *testing.T) {
		driverErr := &shared.ArangoError{HasError: true, Code: 400, ErrorNum: 1023}
		cause, ok := DiagnosticCause(fmt.Errorf("wrapped: %w", driverErr))
		if !ok {
			t.Fatal("pointer driver error missed")
		}
		if want := "ArangoDB error number 1023 (HTTP 400)"; cause != want {
			t.Fatalf("cause = %q, want %q", cause, want)
		}
	})
	t.Run("typed_nil_pointer_absent", func(t *testing.T) {
		if _, ok := DiagnosticCause(fmt.Errorf("wrapped: %w", (*shared.ArangoError)(nil))); ok {
			t.Fatal("typed-nil pointer must not yield a diagnostic cause")
		}
	})
	t.Run("non_arango_absent", func(t *testing.T) {
		if _, ok := DiagnosticCause(errors.New("foreign failure")); ok {
			t.Fatal("foreign error yielded an ArangoDB diagnostic cause")
		}
		if _, ok := DiagnosticCause(nil); ok {
			t.Fatal("nil yielded a diagnostic cause")
		}
	})
}

func TestQueryMemoryLimitFromContextRoundTrips(t *testing.T) {
	if _, ok := QueryMemoryLimitFromContext(context.Background()); ok {
		t.Fatal("unset context reported a memory limit")
	}
	ctx := WithQueryMemoryLimit(context.Background(), 42)
	if limit, ok := QueryMemoryLimitFromContext(ctx); !ok || limit != 42 {
		t.Fatalf("limit = %d/%t, want 42/true", limit, ok)
	}
}
