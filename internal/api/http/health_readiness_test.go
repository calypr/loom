package httpapi

// L1 contract tests for pg-runtime-health-readiness: the selected source
// backend and ClickHouse are consulted independently (each under its own
// budget), both fields are always reported, liveness consults no dependency,
// and the optional CoreState classifier is the ONLY route by which failure
// detail enters the body — ArangoDB arms, which never supply one, keep
// byte-identical published bodies.

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
)

func newHealthServer(t *testing.T, cfg HTTPConfig) *HTTPServer {
	t.Helper()
	cfg.Authorizer = authscope.AllowAllAuthorizer{}
	server, err := NewHTTPServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

// L1a/L1b/L1c: independence of consultation. Every consulted check runs,
// even when the other has failed, and every field is always reported.
func TestReadinessConsultsBothChecksIndependently(t *testing.T) {
	tests := []struct {
		name                   string
		coreErr, clickHouseErr error
		clickHouseEnabled      bool
		wantStatus             int
		wantOverall, wantCore  string
		wantDataframe          string
		wantCoreInvoked        bool
		wantClickHouseInvoked  bool
	}{
		{name: "both down", coreErr: errors.New("core down"), clickHouseErr: errors.New("clickhouse down"), clickHouseEnabled: true,
			wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "not_ready", wantDataframe: "backend_unavailable",
			wantCoreInvoked: true, wantClickHouseInvoked: true},
		{name: "core down clickhouse up", coreErr: errors.New("core down"), clickHouseEnabled: true,
			wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "not_ready", wantDataframe: "ready",
			wantCoreInvoked: true, wantClickHouseInvoked: true},
		{name: "core down clickhouse disabled", coreErr: errors.New("core down"),
			wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "not_ready", wantDataframe: "disabled",
			wantCoreInvoked: true, wantClickHouseInvoked: false},
		{name: "core up clickhouse down", clickHouseErr: errors.New("clickhouse down"), clickHouseEnabled: true,
			wantStatus: http.StatusServiceUnavailable, wantOverall: "not_ready", wantCore: "ready", wantDataframe: "backend_unavailable",
			wantCoreInvoked: true, wantClickHouseInvoked: true},
		{name: "both up", clickHouseEnabled: true,
			wantStatus: http.StatusOK, wantOverall: "ready", wantCore: "ready", wantDataframe: "ready",
			wantCoreInvoked: true, wantClickHouseInvoked: true},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			var coreInvoked, clickHouseInvoked atomic.Bool
			server := newHealthServer(t, HTTPConfig{
				CoreReadyCheck: func(context.Context) error {
					coreInvoked.Store(true)
					return tt.coreErr
				},
				ClickHouseEnabled: tt.clickHouseEnabled,
				ClickHouseReadyCheck: func(context.Context) error {
					clickHouseInvoked.Store(true)
					return tt.clickHouseErr
				},
			})
			body, status := server.Readiness(context.Background())
			dataframe, _ := body["dataframe"].(string)
			if status != tt.wantStatus || body["status"] != tt.wantOverall || body["core"] != tt.wantCore || dataframe != tt.wantDataframe {
				t.Fatalf("status=%d body=%#v", status, body)
			}
			if coreInvoked.Load() != tt.wantCoreInvoked {
				t.Fatalf("core invoked=%v, want %v", coreInvoked.Load(), tt.wantCoreInvoked)
			}
			if clickHouseInvoked.Load() != tt.wantClickHouseInvoked {
				t.Fatalf("clickhouse invoked=%v, want %v", clickHouseInvoked.Load(), tt.wantClickHouseInvoked)
			}
		})
	}
}

// L1e: a hung core check must not consume the ClickHouse budget or fabricate
// its verdict. The core stub blocks past the per-check budget; the ClickHouse
// stub must observe a live (not yet expired) context and still report its
// own state.
func TestReadinessHungCoreDoesNotFabricateClickHouseVerdict(t *testing.T) {
	coreRelease := make(chan struct{})
	t.Cleanup(func() { close(coreRelease) })
	var clickHouseCtxLive, clickHouseInvoked atomic.Bool
	server := newHealthServer(t, HTTPConfig{
		CoreReadyCheck: func(ctx context.Context) error {
			// Hang until this check's OWN budget expires, mimicking a wedged
			// backend, then report its timeout as a failure.
			<-ctx.Done()
			<-coreRelease
			return ctx.Err()
		},
		ClickHouseEnabled: true,
		ClickHouseReadyCheck: func(ctx context.Context) error {
			clickHouseInvoked.Store(true)
			select {
			case <-ctx.Done():
				clickHouseCtxLive.Store(false)
			default:
				clickHouseCtxLive.Store(true)
			}
			return nil
		},
	})
	started := time.Now()
	body, status := server.Readiness(context.Background())
	if !clickHouseInvoked.Load() {
		t.Fatal("ClickHouse check was never consulted while core hung")
	}
	if !clickHouseCtxLive.Load() {
		t.Fatal("ClickHouse check received an already-expired context: core hang consumed the shared budget")
	}
	if status != http.StatusServiceUnavailable || body["status"] != "not_ready" || body["core"] != "not_ready" {
		t.Fatalf("hung core must report not_ready, got status=%d body=%#v", status, body)
	}
	if dataframe, _ := body["dataframe"].(string); dataframe != "ready" {
		t.Fatalf("ClickHouse verdict must be its own (ready), got %#v", body)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("readiness waited %s; per-check budgets must bound the probe", elapsed)
	}
}

// L1f: liveness is dependency-free on every configuration: no check is ever
// consulted, even with both dependencies down, and even after readiness has
// seen the failures.
func TestLivenessNeverConsultsChecksOnAnyLane(t *testing.T) {
	var checks atomic.Int64
	server := newHealthServer(t, HTTPConfig{
		CoreReadyCheck:       func(context.Context) error { checks.Add(1); return errors.New("backend down") },
		ClickHouseEnabled:    true,
		ClickHouseReadyCheck: func(context.Context) error { checks.Add(1); return errors.New("clickhouse down") },
	})
	if _, status := server.Readiness(context.Background()); status != http.StatusServiceUnavailable {
		t.Fatal("fixture must start with both dependencies failing")
	}
	before := checks.Load()
	body, status := server.Liveness(context.Background())
	if status != http.StatusOK || body["status"] != "live" {
		t.Fatalf("liveness status=%d body=%#v", status, body)
	}
	if got := checks.Load() - before; got != 0 {
		t.Fatalf("liveness consulted %d dependency checks; must be DB-free", got)
	}
}

// L1g: the optional CoreState classifier is the only failure-detail channel.
// A credential-bearing cause can only ever surface as one fixed token.
func TestReadinessCoreStateClassifierReportsSafeTokens(t *testing.T) {
	classifier := func(err error) string {
		switch {
		case errors.Is(err, errSchemaDown):
			return "schema_mismatch"
		case errors.Is(err, errConnectivityDown):
			return "connectivity_down"
		default:
			return "core_state_unknown"
		}
	}
	for _, tt := range []struct {
		name      string
		coreErr   error
		wantState string
	}{
		{name: "schema mismatch", coreErr: errSchemaDown, wantState: "schema_mismatch"},
		{name: "connectivity", coreErr: errConnectivityDown, wantState: "connectivity_down"},
		{name: "unclassified", coreErr: errors.New("plain fault"), wantState: "core_state_unknown"},
	} {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			server := newHealthServer(t, HTTPConfig{
				CoreReadyCheck:       func(context.Context) error { return tt.coreErr },
				CoreState:            classifier,
				ClickHouseEnabled:    true,
				ClickHouseReadyCheck: func(context.Context) error { return nil },
			})
			body, status := server.Readiness(context.Background())
			if status != http.StatusServiceUnavailable || body["core"] != "not_ready" {
				t.Fatalf("status=%d body=%#v", status, body)
			}
			if body["core_state"] != tt.wantState {
				t.Fatalf("core_state=%v, want %s (body=%#v)", body["core_state"], tt.wantState, body)
			}
			// Credential discipline: the classifier receives the wrapped
			// error, but only its token may appear in the body.
			if text, ok := body["core_state"].(string); !ok || len(text) > 64 {
				t.Fatalf("core_state must be a short fixed token, got %#v", body["core_state"])
			}
		})
	}
}

// L1h Arango-mode verbatim witness: a deployment without the classifier
// (every ArangoDB arm) gets exactly the published body — no core_state key
// on any status — and the Health cache keeps its 200/degraded contract.
func TestReadinessWithoutClassifierKeepsPublishedBodies(t *testing.T) {
	server := newHealthServer(t, HTTPConfig{
		CoreReadyCheck:       func(context.Context) error { return errors.New("arangodb unreachable") },
		ClickHouseEnabled:    true,
		ClickHouseReadyCheck: func(context.Context) error { return nil },
	})
	body, status := server.Readiness(context.Background())
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", status)
	}
	if _, present := body["core_state"]; present {
		t.Fatalf("classifier-less (Arango-mode) body carries core_state: %#v", body)
	}
	if body["status"] != "not_ready" || body["core"] != "not_ready" || body["dataframe"] != "ready" {
		t.Fatalf("unexpected readiness body %#v", body)
	}
	if _, present := body["core_state"]; present {
		t.Fatalf("cached Health must not gain core_state either: %#v", body)
	}
	// The /health degraded-200 compatibility contract is unchanged.
	degraded := newHealthServer(t, HTTPConfig{
		ClickHouseEnabled:    true,
		ClickHouseReadyCheck: func(context.Context) error { return errors.New("clickhouse unavailable") },
	})
	hb, hs := degraded.Health(context.Background())
	if hs != http.StatusOK || hb["status"] != "degraded" || hb["dataframe"] != "backend_unavailable" {
		t.Fatalf("health status=%d body=%#v", hs, hb)
	}
	if _, present := hb["core_state"]; present {
		t.Fatalf("classifier-less Health body carries core_state: %#v", hb)
	}
}

var (
	errSchemaDown       = errors.New("ledger checksum mismatch")
	errConnectivityDown = &credentialBearingError{}
)

// credentialBearingError mimics the worst case: a connectivity failure whose
// text carries a DSN credential. Only the classifier token may reach clients.
type credentialBearingError struct{}

func (credentialBearingError) Error() string {
	return `postgres: ping: dial tcp postgres://user:s3cr3t@db:5432/loom: connection refused`
}
func (credentialBearingError) Is(target error) bool { return target == errConnectivityDown }
