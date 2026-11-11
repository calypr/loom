package server

// pg-runtime-first-query-route: offline witnesses for the PostgreSQL first
// query route composition. The live scalar-read tests ride the backend
// catalog read lane (the DiscoverFields/DiscoverReferences ports); until
// that lane lands, this file pins what the composition owes on its own:
// (1) every configuration the route cannot consume keeps the byte-stable
// ErrPostgreSQLRuntimeNotWired refusal, dial-free, credential-free;
// (2) the route's own error scrubber removes every credential-bearing form
// of the connection configuration from any error it returns;
// (3) every deliberately-unsupported route posture is a named, typed,
// retryable-false, backend-tagged BACKEND_UNAVAILABLE refusal -- the
// explicit-unavailable law, never a silent zero value and never an
// ArangoDB fall-through.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
)

// qRoutePostgreSQLConfig is a fully valid PostgreSQL connection shape on the
// non-routable TEST-NET-1 address: any composition leg that regressed into
// dialing before refusing would hang until the deadline instead of returning
// the named refusal, so the elapsed-time bound is a real dial-free witness.
func qRoutePostgreSQLConfig() PostgreSQLConfig {
	pg := validPostgreSQLConfig()
	pg.Host = "192.0.2.1"
	pg.Port = 60543
	pg.Database = "verify_qroute_predial_witness"
	pg.User = "verify_qroute_predial_user"
	pg.Password = "qroute-Pg-Pa55w0rd-canary"
	return pg
}

// TestPostgreSQLFirstRoutePreDialRefusals mirrors the pinned startup-refusal
// invariant (postgres_refusal_contract_test.go) across the NEW gate region:
// the shapes the consumability gate cannot consume. The sentinel must arrive
// byte-stably (the pinned contract text, unchanged), before any credential
// validation and before any dial, and must echo no connection material.
func TestPostgreSQLFirstRoutePreDialRefusals(t *testing.T) {
	cases := []struct {
		name    string
		schemas map[string]string
	}{
		{
			name:    "absent schemas keep the arch-backend-06 refusal",
			schemas: nil,
		},
		{
			name:    "empty schemas map keeps the arch-backend-06 refusal",
			schemas: map[string]string{},
		},
		{
			name:    "source alone is not a consumable assignment",
			schemas: map[string]string{"source": "verify_qroute_source"},
		},
		{
			name:    "dataset_release alone is not a consumable assignment",
			schemas: map[string]string{"dataset_release": "verify_qroute_release"},
		},
		{
			name: "an unconsumed series beside the pair stays refused",
			schemas: map[string]string{
				"source": "verify_qroute_source", "dataset_release": "verify_qroute_release",
				"explorer_control": "verify_qroute_explorer",
			},
		},
		{
			name: "blank source is refused at the gate, not dialed with",
			schemas: map[string]string{
				"source": "  ", "dataset_release": "verify_qroute_release",
			},
		},
		{
			name: "the two consumable series sharing one schema are refused",
			schemas: map[string]string{
				"source": "verify_qroute_shared", "dataset_release": "verify_qroute_shared",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{}
			cfg.Server.Backend = sourceBackendPostgres
			cfg.Server.PostgreSQL = qRoutePostgreSQLConfig()
			cfg.Server.PostgreSQL.Schemas = tc.schemas

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			started := time.Now()
			err := RunWithConfig(ctx, cfg)
			elapsed := time.Since(started)

			if !errors.Is(err, ErrPostgreSQLRuntimeNotWired) {
				t.Fatalf("RunWithConfig(postgres, schemas=%v) err = %v, want errors.Is(err, ErrPostgreSQLRuntimeNotWired)", tc.schemas, err)
			}
			if got := err.Error(); got != postgreSQLRefusalMessage {
				t.Fatalf("refusal message drifted: got %q, want byte-stable %q", got, postgreSQLRefusalMessage)
			}
			if elapsed > 1500*time.Millisecond {
				t.Fatalf("refusal took %v: the consumability gate must refuse before any connection attempt", elapsed)
			}
			pg := cfg.Server.PostgreSQL
			for _, secret := range []string{pg.Host, pg.User, pg.Password, pg.Database} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("refusal error leaked connection material %q: %v", secret, err)
				}
			}
		})
	}
}

// TestPostgreSQLFirstQueryRouteScrubberPinsCredentials is the route file's
// own Exec-04-style scrubber pin: every credential-bearing form the
// composition can put into an error -- the full DSN, the bare password, and
// the user:password pair -- must be replaced, the cause must stay reachable
// through Unwrap, and a secret-free error must pass through untouched
// (identity, so wrapping is never fabricated).
func TestPostgreSQLFirstQueryRouteScrubberPinsCredentials(t *testing.T) {
	pg := qRoutePostgreSQLConfig()
	dsn := postgresFirstQueryRouteDSN(pg)
	if !strings.Contains(dsn, pg.Password) {
		t.Fatalf("fixture DSN must actually carry the password for the scrub to be exercised: %q", dsn)
	}
	cause := errors.New("dial failed")
	for _, raw := range []string{
		"open PostgreSQL source runtime: " + dsn,
		"auth failed for user " + pg.User + ":" + pg.Password + " (sqlstate 28P01)",
		"server rejected password " + pg.Password,
	} {
		got := postgresFirstQueryRouteError(fmt.Errorf("%s: %w", raw, cause), pg, dsn)
		if got == nil {
			t.Fatalf("scrubber dropped a non-nil error %q", raw)
		}
		for _, secret := range []string{dsn, pg.Password, pg.User + ":" + pg.Password} {
			if strings.Contains(got.Error(), secret) {
				t.Fatalf("scrubbed error %q still contains credential %q", got.Error(), secret)
			}
		}
		if !strings.Contains(got.Error(), "<redacted>") {
			t.Fatalf("scrubbed error %q lost the redaction marker", got.Error())
		}
		if !strings.Contains(got.Error(), "dial failed") {
			t.Fatalf("scrubbed error %q lost the non-secret cause text", got.Error())
		}
		var wrapped *redactedRouteError
		if !errors.As(got, &wrapped) || wrapped.Unwrap() == nil {
			t.Fatalf("scrubbed error %v must expose the original cause through Unwrap", got)
		}
		if !errors.Is(got, cause) {
			t.Fatalf("scrubbed error %v lost the original cause identity", got)
		}
	}

	// Secret-free errors pass through by identity: no fabricated wrapper.
	plain := errors.New("schema loom_source does not exist")
	if got := postgresFirstQueryRouteError(plain, pg, dsn); got != plain {
		t.Fatalf("secret-free error was rewritten: %v", got)
	}
	if postgresFirstQueryRouteError(nil, pg, dsn) != nil {
		t.Fatal("scrubber must map nil to nil")
	}
}

// TestPostgreSQLFirstQueryRouteWiringErrorShape pins the explicit-unavailable
// law for the route's deliberate refusals: named typed BACKEND_UNAVAILABLE,
// retryable false, backend-tagged, naming the operation and the enabling
// ticket -- never a nil/zero value and never an ArangoDB fall-through
// message.
func TestPostgreSQLFirstQueryRouteWiringErrorShape(t *testing.T) {
	err := postgreSQLFirstQueryRouteWiringError("Server-side generation activation", "pg-runtime-server-composition")
	var typed *dataframeerrors.Error
	if !errors.As(err, &typed) {
		t.Fatalf("refusal %v is not a typed dataframe error", err)
	}
	if typed.Code() != string(dataframeerrors.CodeBackendUnavailable) {
		t.Fatalf("refusal code = %q, want %q", typed.Code(), dataframeerrors.CodeBackendUnavailable)
	}
	if typed.Retryable() {
		t.Fatal("a wiring refusal must not advertise retry: the route will not serve it on retry either")
	}
	details := typed.Details()
	if details["backend"] != "postgresql" || details["stage"] != "wiring" {
		t.Fatalf("refusal details %v must tag backend=postgresql stage=wiring", details)
	}
	if details["enablingTicket"] != "pg-runtime-server-composition" {
		t.Fatalf("refusal details %v must name the enabling ticket", details)
	}
	if !strings.Contains(typed.Error(), "Server-side generation activation") ||
		!strings.Contains(typed.Error(), "pg-runtime-server-composition") {
		t.Fatalf("refusal text %q must name both the operation and the enabling ticket", typed.Error())
	}
}
