package queryapi

// pg-runtime-first-query-route: the PostgreSQL first scoped scalar read
// through the public fhirDataframe route. The live exact-rows, auth-scope,
// generation-scope, outage, and cancellation legs ride the backend catalog
// read lane (the PG DiscoverFields/DiscoverReferences ports); this file's
// offline witnesses hold on their own and prove the wiring posture: with
// discovery ports unwired the service fails closed as the named typed
// BACKEND_UNAVAILABLE before any row-port contact -- the route is driven by
// its ports, and an unwired port can never leak into a zero-value "success"
// or an ArangoDB fall-through.

import (
	"context"
	"errors"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

// qRoutePostgreSQLCompileTarget builds the same closed PostgreSQL compile
// target the server composition selects once from the source schema
// placement, so this file exercises the route's real target, not the Arango
// default the rest of this package's fixtures compile against.
func qRoutePostgreSQLCompileTarget(t *testing.T) compiler.Target {
	t.Helper()
	binding, err := ir.NewPostgresStorageBinding("verify_qroute_source")
	if err != nil {
		t.Fatalf("bind PostgreSQL storage placement: %v", err)
	}
	target, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		t.Fatalf("select PostgreSQL compile target: %v", err)
	}
	return target
}

// TestFhirDataframeNilDiscoveryRefusesTyped pins the composition-side claim
// on the query service: with DiscoverFields/DiscoverReferences unwired (the
// posture runPostgreSQLFirstQueryRoute uses until the backend catalog read
// lane lands), every discovery-backed query fails closed as the named typed
// BACKEND_UNAVAILABLE -- and the row port is never contacted, so a refusal
// can never be "answered" with rows from any other backend.
func TestFhirDataframeNilDiscoveryRefusesTyped(t *testing.T) {
	rowPortCalls := 0
	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{
		SourceRows: func(context.Context, dataframeexecution.RowsRequest, dataframeexecution.RowVisitor) error {
			rowPortCalls++
			return nil
		},
	})
	service := NewService(Config{
		CompileTarget:      qRoutePostgreSQLCompileTarget(t),
		Dataframes:         dataframes,
		DiscoverFields:     nil,
		DiscoverReferences: nil,
	})

	result, err := service.Run(context.Background(), queryapiGenderInput(), nil)
	if err == nil {
		t.Fatalf("Run with unwired discovery ports returned %+v; it must fail closed, not degrade", result)
	}
	if rowPortCalls != 0 {
		t.Fatalf("unwired discovery still contacted the row port %d times: the refusal must precede any backend read", rowPortCalls)
	}
	var typed *dataframeerrors.Error
	if !errors.As(err, &typed) {
		t.Fatalf("discovery refusal %v is not a typed dataframe error", err)
	}
	if typed.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		t.Fatalf("discovery refusal code = %q, want %q", typed.Code(), dataframeerrors.CodeBackendUnavailable)
	}
	if result != nil {
		t.Fatalf("typed refusal must not carry a partial result: %+v", result)
	}
}
