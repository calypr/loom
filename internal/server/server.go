package server

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/ingest"
)

// Run starts the Loom HTTP server using the process command-line flags.
func Run() {
	options, err := parseServerOptions(os.Args[1:], flag.ContinueOnError)
	if err != nil {
		if err == flag.ErrHelp {
			return
		}
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, options); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

const cleanupTimeout = 10 * time.Second

func recordDegradation(logger *slog.Logger, current error, stage string, cause error) error {
	if cause == nil {
		return current
	}
	if logger != nil {
		logger.Error("dataframe startup degraded", "stage", stage, "error", cause)
	}
	return errors.Join(current, fmt.Errorf("%s: %w", stage, cause))
}

// preserveConfiguredQueryError maps already-classified adapter failures onto
// the preview route contract. Vendor classification happened at the adapter
// seam; composition only routes.
func preserveConfiguredQueryError(err error) error {
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, context.Canceled) {
		return previewRouteError(err)
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		switch userErr.Code() {
		case string(dataframeerrors.CodeUnauthenticated):
			return &explorer.AuthoringError{Status: http.StatusUnauthorized, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		case string(dataframeerrors.CodeForbidden), string(dataframeerrors.CodeUnauthorizedProject):
			return &explorer.AuthoringError{Status: http.StatusForbidden, Diagnostic: explorer.AuthoringDiagnostic{
				Severity: "ERROR", Stage: "preview", Code: userErr.Code(), Message: dataframeerrors.PublicMessage(err),
			}, Cause: err}
		}
		return previewRouteError(err)
	}
	return err
}

func loggedPreviewIndexPreparation(logger *slog.Logger, prepare func(context.Context, compiler.PreviewCoveringIndexSpec) error) func(context.Context, compiler.PreviewCoveringIndexSpec) error {
	return func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) error {
		started := time.Now()
		err := prepare(ctx, spec)
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelWarn
		}
		logServerDiagnostic(logger, level, "dataframe preview index preparation", requestIDFromContext(ctx), "index_prepare", time.Since(started), err,
			"collection", spec.Collection, "index", spec.Name, "prewarm", spec.PrepareAfterPreview)
		return err
	}
}

func diagnosticErrorAttrs(err error) []any {
	if err == nil {
		return []any{"success", true}
	}
	attrs := []any{"success", false, "error_type", fmt.Sprintf("%T", err)}
	if code := diagnosticErrorCode(err); code != "" {
		attrs = append(attrs, "error_code", code)
	}
	attrs = append(attrs, "cause", diagnosticErrorCause(err))
	return attrs
}

func diagnosticErrorCause(err error) string {
	return capDiagnosticCause(redactDiagnosticCause(diagnosticErrorCauseValue(err)))
}

func diagnosticErrorCauseValue(err error) string {
	if err == nil {
		return ""
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return dataframeerrors.PublicMessage(err)
	}
	var lifecycleErr *lifecycle.Error
	if errors.As(err, &lifecycleErr) && lifecycleErr.Message != "" {
		return lifecycleErr.Message
	}
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) && authoringErr.Diagnostic.Message != "" {
		return authoringErr.Diagnostic.Message
	}
	var compilationErr *explorercompilation.Error
	if errors.As(err, &compilationErr) && compilationErr.Message != "" {
		return compilationErr.Message
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "operation canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "operation deadline exceeded"
	}
	// The original ArangoDB cause reaches server-side logs through the
	// adapter's own safe vendor rendering, behind the redaction and length
	// guards below; composition never imports the driver to interpret it.
	if cause, ok := arangofactory.DiagnosticCause(err); ok {
		return cause
	}
	return err.Error()
}

const maxDiagnosticCauseBytes = 256

func capDiagnosticCause(cause string) string {
	if len(cause) <= maxDiagnosticCauseBytes {
		return cause
	}
	return strings.ToValidUTF8(cause[:maxDiagnosticCauseBytes-3], "") + "..."
}

var (
	diagnosticCredentialAssignment = regexp.MustCompile(`(?i)((?:"|')?(?:authorization|proxy-authorization|access[_ -]?token|refresh[_ -]?token|token|password|secret|api[_ -]?key|apikey|credential)(?:"|')?\s*[:=]\s*)(?:(?:bearer|basic)\s+)?(?:"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;}\]]+)`)
	diagnosticBearerCredential     = regexp.MustCompile(`(?i)\b(?:bearer|basic)\s+[A-Za-z0-9._~+/-]+=*`)
	diagnosticURLCredential        = regexp.MustCompile(`(?i)(https?://)[^/\s:@]+:[^/\s@]+@`)
	diagnosticPayloadAssignment    = regexp.MustCompile(`(?i)\b((?:"|')?(?:query|bind[_ -]?vars?|bind[_ -]?values?|(?:request|response)[_ -]?body|body)(?:"|')?\s*[:=]\s*)`)
)

func redactDiagnosticCause(cause string) string {
	cause = diagnosticCredentialAssignment.ReplaceAllString(cause, "${1}[redacted]")
	cause = diagnosticBearerCredential.ReplaceAllString(cause, "[redacted credential]")
	cause = diagnosticURLCredential.ReplaceAllString(cause, "${1}[redacted]@")
	if match := diagnosticPayloadAssignment.FindStringIndex(cause); match != nil {
		return cause[:match[0]] + cause[match[0]:match[1]] + "[redacted]"
	}
	return cause
}

func diagnosticErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if userErr, ok := dataframeerrors.AsUserError(err); ok {
		return userErr.Code()
	}
	var lifecycleErr *lifecycle.Error
	if errors.As(err, &lifecycleErr) {
		return lifecycleErr.Code
	}
	var compilationErr *explorercompilation.Error
	if errors.As(err, &compilationErr) {
		return compilationErr.Code
	}
	var authoringErr *explorer.AuthoringError
	if errors.As(err, &authoringErr) {
		return authoringErr.Diagnostic.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "CANCELED"
	case errors.Is(err, context.DeadlineExceeded):
		return "DEADLINE_EXCEEDED"
	default:
		return ""
	}
}

func logServerDiagnostic(logger *slog.Logger, level slog.Level, message, requestID, phase string, duration time.Duration, err error, fields ...any) {
	if logger == nil {
		return
	}
	attrs := []any{"request_id", requestID, "phase", phase, "duration_ms", duration.Milliseconds()}
	attrs = append(attrs, diagnosticErrorAttrs(err)...)
	attrs = append(attrs, fields...)
	logger.Log(context.Background(), level, message, attrs...)
}

func categoryScanReceiptResolutionError(receiptID string, err error) error {
	classified := classifyReceiptPreviewResolutionError(receiptID, err)
	var resolution *receiptPreviewResolutionError
	if errors.As(classified, &resolution) {
		return receiptPreviewConflict(classified)
	}
	return classified
}

const pivotCategoryScanTimeout = 30 * time.Second

type pivotPreviewQueryBudgetContextKey struct{}

func withPivotPreviewQueryBudget(ctx context.Context) context.Context {
	return context.WithValue(ctx, pivotPreviewQueryBudgetContextKey{}, true)
}

func previewQueryMaxRuntime(ctx context.Context) time.Duration {
	if ctx != nil {
		if extended, _ := ctx.Value(pivotPreviewQueryBudgetContextKey{}).(bool); extended {
			return pivotCategoryScanTimeout
		}
	}
	return explorerPreviewTimeout
}

func configuredCategoryScanner(logger *slog.Logger, recipeEngine *dataframeexecution.Engine) lifecycle.CategoryScanner {
	return func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		// Category discovery is Pivot-only and uses the extended query runtime limit.
		scanCtx, cancel := context.WithTimeout(ctx, pivotCategoryScanTimeout)
		defer cancel()
		scanCtx = withPivotPreviewQueryBudget(scanCtx)
		started := time.Now()
		attrs := []any{"output_id", request.Output, "stage_id", request.StageID, "category_column_id", request.ColumnID, "value_column_id", request.ValueColumnID}
		if receipt == nil {
			err := fmt.Errorf("compilation receipt is missing")
			logServerDiagnostic(logger, slog.LevelError, "Explorer table-shape category scan resolution", requestIDFromContext(ctx), "category_scan_resolution", time.Since(started), err, attrs...)
			return dataframeexecution.CategoryScanResult{}, err
		}
		resolved, err := compileValidatedReceiptResolution(scanCtx, recipeEngine, receipt, bindings)
		if err != nil {
			resultErr := categoryScanReceiptResolutionError(receipt.ID, err)
			logServerDiagnostic(logger, slog.LevelError, "Explorer table-shape category scan resolution", requestIDFromContext(ctx), "category_scan_resolution", time.Since(started), resultErr, attrs...)
			return dataframeexecution.CategoryScanResult{}, resultErr
		}
		logServerDiagnostic(logger, slog.LevelInfo, "Explorer table-shape category scan resolution", requestIDFromContext(ctx), "category_scan_resolution", time.Since(started), nil, attrs...)

		scanStarted := time.Now()
		result, err := recipeEngine.ScanCategories(scanCtx, resolved, request)
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		logServerDiagnostic(logger, level, "Explorer table-shape category scan", requestIDFromContext(ctx), "category_scan", time.Since(scanStarted), err, attrs...)
		return result, err
	}
}

// newExplorerReleaseActivator builds the dataset release activation hook for
// the repository publish path (lifecycle repository_publish.go ActivateRelease).
// It reads the live release revision and submits it as the store's
// compare-and-swap input, so a concurrent winning publication makes the
// store's own guard reject: internal/backends/arango/internal/dataset/release_store.go
// activateReleaseAQL compares it with `FILTER currentRevision ==
// @expected_revision OR alreadyActive`, and the emptied result becomes
// dataset.ErrReleaseActivationConflict (release_store.go:99-100), which the
// lifecycle types as the repair-34 409 repository_publish
// PUBLICATION_ACTIVATION_CONFLICT with the prior revision retained.
//
// devReleaseCASFault is the dev-only repository sibling of the
// prepareExplorerRelease fault (LOOM_DEV_ACTIVATION_CONFLICT_ONCE) below: it
// is armed only through applyDevFaultOverrides, therefore only under the
// local no-auth mode. Consumed exactly once, it corrupts the expected-revision
// CAS input after a successful Active read so the release CAS legitimately
// loses while the retained candidate release stays durable. The seam sits
// above the store on purpose: the second, Explorer-side activation leg
// (internal/backends/arango/internal/explorer/activation.go compositeActivationReadAQL guards
// plus activateRevisionAndOwner) never yields dataset.ErrReleaseActivationConflict
// — every guard on that leg produces only explorer.ErrDraftConflict or plain
// faults — so the sentinel this contract classifies can only come from the
// release CAS input computed here. A rival document could not be activated
// instead: activateReleaseAQL's alreadyActive guard makes same-content
// re-activation a zero-write no-op, so bumping the pointer revision would
// require minting a competing release document that no publish flow created
// (invented state) and would itself leave the dataset pointer on the rival
// while the Explorer pointer stayed on the prior revision (split-brain).
// The fault therefore corrupts one number instead: the store still decides
// the outcome, zero writes happen on the loss, and no successful activation
// is ever hidden behind a synthetic error.
func newExplorerReleaseActivator(releaseService *publicationcontract.ReleaseService, devReleaseCASFault *atomic.Bool) func(context.Context, string, string, []publicationcontract.DataframeSelector) error {
	return func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) error {
		expectedRevision := int64(0)
		// Consume the one-shot arm at request entry, never conditionally: an
		// armed request whose Active read has no pointer to corrupt must not
		// leave the fault live to silently hijack a LATER publish request.
		devFaultArmed := devReleaseCASFault != nil && devReleaseCASFault.CompareAndSwap(true, false)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision
			// Feed a stale expected revision so the real
			// store CAS loses exactly once. Corrupt only after a successful
			// Active read: with no prior pointer the store rejects on the
			// missing-pointer precondition instead of the compare-and-swap.
			if devFaultArmed {
				expectedRevision++
			}
		} else if !errors.Is(err, publicationcontract.ErrNoActiveRelease) {
			return err
		}
		_, err = releaseService.Activate(ctx, publicationcontract.ActivationRequest{
			Project: project, Generation: generation, GitCommit: generation,
			ExpectedRevision: expectedRevision, OptionalSelectors: selectors,
		})
		return err
	}
}

// ErrPostgreSQLRuntimeNotWired is the named, proven-closed startup gate for
// the explicitly selected PostgreSQL source backend (arch-backend-06 D1).
// The PostgreSQL factory lives at internal/backends/postgres. Since
// pg-runtime-server-composition, the postgres backend has two consumable
// shapes: the complete six-series placement ladder (runPostgreSQLFullRuntime
// in runtime.go) and the first-query-route pair (server.postgresql.schemas
// with exactly the source and dataset_release entries). Every configuration
// that carries neither shape receives this sentinel before any store
// connection is opened, so selecting postgres can never silently fall
// through to the ArangoDB stores below. The sentinel text is byte-stable and
// pinned by postgres_refusal_contract_test.go.
var ErrPostgreSQLRuntimeNotWired = errors.New("server.backend postgres is configured, but the PostgreSQL source runtime is not wired yet; refusing to start on the ArangoDB backend")

// RunWithConfig executes the server composition for an already-parsed
// Config, the same path Run() takes after flag parsing. It is the
// composition entry point exercised by contract tests that must witness the
// backend-selection gate without driving process flags or signals.
func RunWithConfig(ctx context.Context, serverConfig Config) error {
	return run(ctx, serverConfig)
}

func run(ctx context.Context, serverConfig Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	// The PostgreSQL source backend is a configuration-only selector until its
	// runtime wiring lands. Fail explicitly instead of silently falling back
	// to the ArangoDB stores below. The message names no credentials.
	backend, err := normalizeSourceBackend(serverConfig.Server.Backend)
	if err != nil {
		return err
	}
	if backend == sourceBackendPostgres {
		// pg-runtime-server-composition owns the explicitly selected
		// PostgreSQL source backend. A configuration carrying the complete
		// six-series placement ladder composes the full PostgreSQL runtime
		// over the backend factory (runtime.go) -- no ArangoDB store, client,
		// or AQL surface is constructed on that branch. Every other shape
		// falls through to the first-query route, whose own consumability
		// gate keeps the pinned startup-refusal contract byte-stable:
		// configurations it cannot consume still receive
		// ErrPostgreSQLRuntimeNotWired before any dial. Nothing in either
		// PostgreSQL branch reaches an ArangoDB store.
		if _, _, err := postgresRuntimeAssignment(serverConfig.Server.PostgreSQL); err == nil {
			return runPostgreSQLFullRuntime(ctx, serverConfig, logger)
		}
		return runPostgreSQLFirstQueryRoute(ctx, serverConfig, logger)
	}
	// The ArangoDB runtime composes and serves through the one seam in
	// compose.go (composeArangoRuntime -> serveRuntime). run() owns backend
	// selection only: nothing below this dispatch touches a backend adapter.
	return runArangoRuntime(ctx, serverConfig, logger)
}

// ---------------------------------------------------------------------------
// pg-runtime-first-query-route: the explicitly selected PostgreSQL source
// backend's first scoped scalar read through the public GraphQL query route.
// ---------------------------------------------------------------------------

// postgreSQLFirstQueryRouteAcquireTimeout bounds waiting for a pooled
// connection, matching the ingest lane's default (ingest.PGLoadConfig).
const postgreSQLFirstQueryRouteAcquireTimeout = 30 * time.Second

// postgreSQLFirstQueryRouteWiringError is the single typed refusal used by
// every PostgreSQL route this slice deliberately does not serve. It is the
// explicit-unavailable posture required by the architecture contract: a
// named, retryable-false, backend-tagged typed error that never falls
// through to the ArangoDB stores and never degrades to a silent zero value.
func postgreSQLFirstQueryRouteWiringError(operation, enablingTicket string) error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable,
		operation+" is not served by the PostgreSQL first query route; it belongs to "+enablingTicket,
		dataframeerrors.WithRetryable(false),
		dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "wiring", "enablingTicket": enablingTicket}))
}

// postgresFirstQueryRouteAssignment resolves the only consumable first-query-
// route configuration shape: server.postgresql.schemas carrying exactly the
// source and dataset_release series assignments, distinct and non-blank.
// Every other shape — missing, blank, duplicated across the two series, or
// carrying an entry for a series this route does not consume — keeps the
// arch-backend-06 startup refusal: ErrPostgreSQLRuntimeNotWired is returned
// before any credential validation and before any dial, so the pinned
// byte-stable sentinel contract (postgres_refusal_contract_test.go,
// TestPostgresFactoryOwnershipContract) stays intact for configurations the
// route cannot honestly serve. Deep schema-identifier and cross-series
// ledger validation stays with the factory's authoritative
// control.Config.Validate, which also runs pre-dial.
func postgresFirstQueryRouteAssignment(pg PostgreSQLConfig) (backendspostgres.SeriesSchemas, string, string, error) {
	source, hasSource := pg.Schemas[string(backendspostgres.SeriesSource)]
	manifest, hasManifest := pg.Schemas[string(backendspostgres.SeriesDatasetRelease)]
	source, manifest = strings.TrimSpace(source), strings.TrimSpace(manifest)
	if !hasSource || !hasManifest || source == "" || manifest == "" || source == manifest || len(pg.Schemas) != 2 {
		return nil, "", "", ErrPostgreSQLRuntimeNotWired
	}
	return backendspostgres.SeriesSchemas{
		backendspostgres.SeriesSource:         source,
		backendspostgres.SeriesDatasetRelease: manifest,
	}, source, manifest, nil
}

// postgresFirstQueryRouteDSN renders the configured PostgreSQL connection as
// a URL DSN. The returned string carries the credential and must never be
// logged or interpolated into an error; route errors pass through
// postgresFirstQueryRouteError, which scrubs every credential form.
func postgresFirstQueryRouteDSN(pg PostgreSQLConfig) string {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(pg.User, pg.Password),
		Host:     fmt.Sprintf("%s:%d", pg.Host, pg.Port),
		Path:     "/" + pg.Database,
		RawQuery: "sslmode=" + url.QueryEscape(strings.ToLower(strings.TrimSpace(pg.SSLMode))),
	}
	return u.String()
}

// redactedRouteError keeps the cause reachable while rendering a credential-
// scrubbed text.
type redactedRouteError struct {
	msg   string
	cause error
}

func (e *redactedRouteError) Error() string { return e.msg }
func (e *redactedRouteError) Unwrap() error { return e.cause }

// postgresFirstQueryRouteError scrubs every credential-bearing form of the
// connection configuration from an error text. The factory's own open path
// already redacts the DSN password; this is the composition's defensive
// second layer so no route error can echo a credential. The percent-encoded
// forms are covered too: url.UserPassword stores the userinfo in escaped
// form, so a driver echo of the parsed credential shows the escaped shape,
// not the raw one (review e22c4825 optional 2, defense-in-depth).
func postgresFirstQueryRouteError(err error, pg PostgreSQLConfig, dsn string) error {
	if err == nil {
		return nil
	}
	text := err.Error()
	queryEscaped := url.QueryEscape(pg.Password)
	pathEscaped := url.PathEscape(pg.Password)
	secrets := []string{
		dsn,
		pg.Password,
		pg.User + ":" + pg.Password,
		queryEscaped,
		pathEscaped,
		pg.User + ":" + queryEscaped,
		pg.User + ":" + pathEscaped,
	}
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "<redacted>")
		}
	}
	if text == err.Error() {
		return err
	}
	return &redactedRouteError{msg: text, cause: err}
}

// PostgreSQL readiness-state tokens (pg-runtime-health-readiness): the
// fixed, credential-free vocabulary the CoreState classifier maps a
// control.Runtime.Readiness failure onto. The vocabulary is closed: an
// unclassified failure reports core_state_unknown, never the raw error text,
// so no DSN, SQL, driver echo, or credential can reach the readiness body.
const (
	pgReadinessStateClosed           = "runtime_closed"
	pgReadinessStateConnectivityDown = "connectivity_down"
	pgReadinessStateUnsupportedVer   = "unsupported_version"
	pgReadinessStateSchemaMissing    = "schema_missing"
	pgReadinessStateSchemaUnmigrated = "schema_unmigrated"
	pgReadinessStateSchemaMismatch   = "schema_mismatch"
	pgReadinessStateUnknown          = "core_state_unknown"
)

// postgreSQLCoreReadinessState classifies a Readiness failure into one of
// the fixed tokens above via the backend's published sentinel taxonomy
// (errors.Is only). Connectivity failures (ErrConnect, which the
// readiness-checked pool returns for acquire and statement faults), the
// PostgreSQL 18 version gate, a caller-never-created schema, a schema behind
// the embedded history, and an edited/future/vanished/out-of-order ledger
// are each distinguishable from one another and from each other's causes
// without any string matching and without leaking error text.
func postgreSQLCoreReadinessState(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, backendspostgres.ErrRuntimeClosed):
		return pgReadinessStateClosed
	case errors.Is(err, backendspostgres.ErrConnect):
		return pgReadinessStateConnectivityDown
	case errors.Is(err, backendspostgres.ErrUnsupportedVersion):
		return pgReadinessStateUnsupportedVer
	case errors.Is(err, backendspostgres.ErrInvalidSchemaName):
		return pgReadinessStateSchemaMissing
	case errors.Is(err, backendspostgres.ErrMigrationPending):
		return pgReadinessStateSchemaUnmigrated
	case errors.Is(err, backendspostgres.ErrMigrationChecksumMismatch),
		errors.Is(err, backendspostgres.ErrFutureSchemaVersion),
		errors.Is(err, backendspostgres.ErrMigrationMissing),
		errors.Is(err, backendspostgres.ErrMigrationOutOfOrder):
		return pgReadinessStateSchemaMismatch
	default:
		return pgReadinessStateUnknown
	}
}

// postgreSQLGenerationStatusReader answers generation status reads from the
// runtime's manifest lifecycle port — a real PostgreSQL read, never an
// ArangoDB one — and refuses server-side activation: the activation CAS is
// driven by the loom-data CLI's ingest lane until pg-runtime-server-composition.
type postgreSQLGenerationStatusReader struct {
	manifests publicationcontract.ManifestLifecycle
}

func (r postgreSQLGenerationStatusReader) ReadManifest(ctx context.Context, ref publicationcontract.Ref) (publicationcontract.Manifest, error) {
	if r.manifests == nil {
		return publicationcontract.Manifest{}, postgreSQLFirstQueryRouteWiringError("Generation status reads", "pg-runtime-server-composition")
	}
	return r.manifests.ReadManifest(ctx, ref)
}

func (r postgreSQLGenerationStatusReader) Activate(context.Context, publicationcontract.Manifest) error {
	return postgreSQLFirstQueryRouteWiringError("Server-side generation activation", "pg-runtime-server-composition")
}

// postgreSQLFirstQueryRecipeControl keeps every recipe-control operation
// explicitly unavailable while the PostgreSQL recipe registry, revision
// chain, and execution registries land in later slices. The embedded
// dataframeexecution.Control carries the live PostgreSQL EXPLAIN callback
// over the runtime's PlanJSON port (retiring the documented nil stopgap on
// this branch); it stays unreachable until a recipe Engine is composed, and
// that seam is registered rather than silently AQL-routed: an AQL-tagged
// command is never handed to ExplainPostgreSQLConnection and a PostgreSQL-
// tagged command is never handed to the AQL policy surface.
type postgreSQLFirstQueryRecipeControl struct {
	dataframeexecution.Control
}

func (c postgreSQLFirstQueryRecipeControl) Validate(ctx context.Context, name string, bindings recipe.RuntimeBindings) (dataframeexecution.Validation, error) {
	if c.Control.Engine == nil {
		return dataframeexecution.Validation{}, postgreSQLFirstQueryRouteWiringError("Recipe validation", "pg-runtime-server-composition")
	}
	return c.Control.Validate(ctx, name, bindings)
}

func (c postgreSQLFirstQueryRecipeControl) Explain(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	if c.Control.Engine == nil {
		return semantic.RecipePlanExplanation{}, postgreSQLFirstQueryRouteWiringError("Recipe explain", "pg-runtime-server-composition")
	}
	return c.Control.Explain(ctx, name, bindings)
}

func (c postgreSQLFirstQueryRecipeControl) ExplainPhysical(ctx context.Context, name string, bindings recipe.RuntimeBindings, live bool) (dataframeexecution.PhysicalExplanation, error) {
	if c.Control.Engine == nil {
		return dataframeexecution.PhysicalExplanation{}, postgreSQLFirstQueryRouteWiringError("Recipe physical explain", "pg-runtime-server-composition")
	}
	return c.Control.ExplainPhysical(ctx, name, bindings, live)
}

func (c postgreSQLFirstQueryRecipeControl) Resolve(ctx context.Context, name string, bindings recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error) {
	if c.Control.Engine == nil {
		return semantic.ResolvedRecipePlan{}, postgreSQLFirstQueryRouteWiringError("Recipe resolution", "pg-runtime-server-composition")
	}
	return c.Control.Resolve(ctx, name, bindings)
}

func (c postgreSQLFirstQueryRecipeControl) Preview(ctx context.Context, name string, bindings recipe.RuntimeBindings) (dataframeexecution.Preview, error) {
	if c.Control.Engine == nil {
		return dataframeexecution.Preview{}, postgreSQLFirstQueryRouteWiringError("Recipe preview", "pg-runtime-server-composition")
	}
	return c.Control.Preview(ctx, name, bindings)
}

func (c postgreSQLFirstQueryRecipeControl) Run(ctx context.Context, name string, bindings recipe.RuntimeBindings) (dataframeexecution.Preview, error) {
	if c.Control.Engine == nil {
		return dataframeexecution.Preview{}, postgreSQLFirstQueryRouteWiringError("Recipe run", "pg-runtime-server-composition")
	}
	return c.Control.Run(ctx, name, bindings)
}

// postgreSQLFirstQueryDataframeReleases keeps the dataframe-release read
// port explicitly unavailable: server-side activation switches a dataframe
// publication pointer, and the PostgreSQL publication lane belongs to
// pg-runtime-server-composition. Answering with the loadapi lane's nil-store
// guard would surface a 500 INTERNAL_ERROR for what is a known un-served
// posture; naming the refusal here keeps every activation attempt typed,
// retryable-false, and backend-tagged -- and it is the load-bearing check
// that the route never half-activates from stale release evidence.
type postgreSQLFirstQueryDataframeReleases struct{}

func (postgreSQLFirstQueryDataframeReleases) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, postgreSQLFirstQueryRouteWiringError("Dataframe release reads", "pg-runtime-server-composition")
}

func (postgreSQLFirstQueryDataframeReleases) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	return publication.BundlePointer{}, postgreSQLFirstQueryRouteWiringError("Dataframe release pointer reads", "pg-runtime-server-composition")
}

// runPostgreSQLFirstQueryRoute composes the explicitly selected PostgreSQL
// source backend for the first scoped scalar read. Order of laws: (1) the
// consumability gate runs first and pre-dial — configs it cannot consume
// keep receiving ErrPostgreSQLRuntimeNotWired byte-stably, preserving the
// pinned startup-refusal contracts; (2) connection-contract validation and
// the ClickHouse refusal also run pre-dial; (3) the runtime opens exactly
// through the backend factory (internal/backends/postgres) with the source
// and dataset_release series assigned and SetupNone — migrations belong to
// the ingest lane (ingest.PGLoadConfig convention), and a schema the caller
// has not arranged is a named factory refusal, never a silent create;
// (4) every port is PostgreSQL: the compile target is selected once from the
// source schema binding, the row port is the factory's tagged executor
// behind configuredPostgreSQLSourceRows, the active generation resolves
// through the runtime's manifest port, and no ArangoDB client, adapter, or
// AQL surface is constructed anywhere in this branch. Explorer/row/recipe/
// graph/traversal/publication routes stay explicitly unavailable through the
// typed refusals above and the nil-port guards, never silently Arango-backed.
// composePostgreSQLFirstQueryRoute is the listen-free composition seam: every
// port, refusal, and route registration the production route installs,
// returned as a fully wired HTTPServer plus the runtime-close function. The
// production path below adds listen/shutdown around it; the route-census
// test drives the returned app through fiber's in-process app.Test so the
// explicit-unavailable posture is asserted through the PRODUCTION handler
// stack without a listen-port race.
func composePostgreSQLFirstQueryRoute(ctx context.Context, serverConfig Config, logger *slog.Logger) (server *httpapi.HTTPServer, closeRuntime func() error, err error) {
	assignment, sourceSchema, _, err := postgresFirstQueryRouteAssignment(serverConfig.Server.PostgreSQL)
	if err != nil {
		return nil, nil, err
	}
	pg := serverConfig.Server.PostgreSQL
	if err := pg.validate(); err != nil {
		return nil, nil, err
	}
	if serverConfig.Server.ClickHouse.Enabled {
		return nil, nil, errors.New("server.clickhouse.enabled cannot be combined with server.backend postgres in this slice; the PostgreSQL publication composition belongs to pg-runtime-server-composition")
	}

	// Cleanup law (mirroring the full runtime compose): the one deferred
	// statement is the error-path sweep; on success the same stack is
	// returned as the caller's closer and serveRuntime owns it.
	var cleanups []func() error
	defer func() {
		if err == nil {
			return
		}
		_ = runRuntimeCloseSteps(cleanups)
	}()
	dsn := postgresFirstQueryRouteDSN(pg)
	runtime, err := openPostgresBackend(ctx, backendspostgres.Config{
		DSN:            dsn,
		MaxConns:       int32(pg.MaxOpenConns),
		MinConns:       int32(pg.MaxIdleConns),
		AcquireTimeout: postgreSQLFirstQueryRouteAcquireTimeout,
		Schemas:        assignment,
		Setup:          backendspostgres.SetupNone,
	})
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("open PostgreSQL source runtime: %w", err), pg, dsn)
	}
	cleanups = append(cleanups, func() error {
		return postgresFirstQueryRouteError(runtime.Close(), pg, dsn)
	})
	closeRuntime = func() error { return runRuntimeCloseSteps(cleanups) }

	// The compile target is selected exactly once from the source schema
	// placement, mirroring the executor witnesses: per-request dialect
	// selection is not available to any caller.
	binding, err := ir.NewPostgresStorageBinding(sourceSchema)
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("bind PostgreSQL storage placement: %w", err), pg, dsn)
	}
	sourceCompileTarget, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("select PostgreSQL compile target: %w", err), pg, dsn)
	}

	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{
		SourceRows: configuredPostgreSQLSourceRows(logger, "query_rows", runtime.SourceRows),
	})

	// Catalog discovery rides the backend's own field-profile read lane
	// (Ask C) behind the same catalog.NewCache the Arango lane uses. The
	// reference reads fhir_field_catalog; the PostgreSQL mirror of that
	// reference table is the persisted field-profile rows, so the auth-
	// path lister is derived there too -- never an Arango catalog surface.
	discoveryCache := catalog.NewCache()
	discoverFields := discoveryCache.DiscoverFields(runtime.DiscoverFields)

	// Auth wiring mirrors the Arango lane's shape (server.go:490-498): the
	// existing-auth-path lister is the backend read lane's own listing;
	// noAuth and basic modes never call it, and calypr mode resolves
	// against PostgreSQL-persisted paths only.
	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, runtime.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return nil, nil, fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			CompileTarget: sourceCompileTarget,
			// Field discovery is the backend read lane's own port behind the
			// discovery cache. Reference discovery stays nil on purpose:
			// reference-backed traversal fails closed as typed
			// BACKEND_UNAVAILABLE (catalogUnavailable) until the reference
			// read lane lands -- never a partial read, never Arango.
			// Graph queries that resolve without references still reach
			// compilation, where the PostgreSQL renderer honestly refuses
			// the graph physical plan (see the fhirGraph census leg).
			DiscoverReferences:     nil,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: runtime.Manifests,
			// The AQL explain policy surface is not wired on this branch;
			// the graph service already refuses a nil explain as typed
			// unavailable.
			Explain: nil,
		},
		MaterializationReader: nil,
		Logger:                logger,
		RecipeControl: postgreSQLFirstQueryRecipeControl{Control: dataframeexecution.Control{
			ExplainPostgreSQLConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
				return explainPostgreSQLCompiledQuery(ctx, runtime.PlanJSON, compiled)
			},
		}},
		// The PostgreSQL lane's authorizer leaves SelectionMembersCollection
		// empty (the zero value is the explicit PostgreSQL stamp policy: the
		// AQL group-rows renderer is its only consumer and this lane never
		// reaches one).
		RecipeAuthorizer: recipeAuthorization{resolver: scopeResolver},
		RecipeRevisions:  nil,
		RecipeExecutions: nil,
	})

	generationService, err := loadapi.NewService(loadapi.ServiceConfig{
		LoadGeneration: func(context.Context, loadapi.GenerationLoadRequest, ingest.EventSink) (ingest.LoadSummary, error) {
			return ingest.LoadSummary{}, postgreSQLFirstQueryRouteWiringError("Server-side PostgreSQL generation load", "pg-runtime-server-composition (the loom-data CLI drives the production ingest path today)")
		},
		GenerationActivator: postgreSQLGenerationStatusReader{manifests: runtime.Manifests},
		DataframeReleases:   postgreSQLFirstQueryDataframeReleases{},
		Logger:              logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create generation load service: %w", err)
	}

	server, err = httpapi.NewHTTPServer(httpapi.HTTPConfig{
		Authenticator: authenticator,
		Authorizer:    authorizer,
		Logger:        logger,
		// Same full probe as the full runtime: reachability + version gate +
		// read-only ledger probe over the two assigned series. The route
		// serves reads only from schemas whose ledger agrees with the
		// embedded history; CoreState classifies failures safely.
		CoreReadyCheck: func(ctx context.Context) error {
			return runtime.Readiness(ctx)
		},
		CoreState:            postgreSQLCoreReadinessState,
		ClickHouseReadyCheck: nil,
		ClickHouseEnabled:    false,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create HTTP server: %w", err)
	}
	// Explorer handlers stay nil: every Explorer/authoring/row/preview/trace
	// route already returns its typed AUTHORING_UNAVAILABLE/TRACE_UNAVAILABLE/
	// ROW_LINEAGE_UNAVAILABLE refusal through the nil-handler guards, and the
	// route census test pins that none of them ever serves Arango-backed data.
	// The read-only first-query lane deliberately carries no releases
	// port (nil is this lane's pinned posture, asserted by the route-census
	// test) and the auth-mode-derived explicit scope policy. Typed route
	// composition contract (arch-backend-08).
	if err := registerRoutes(server, generationService, authorizer, resolver, nil, routeDependencies{scopes: auth.scopePolicy}); err != nil {
		return nil, nil, fmt.Errorf("register HTTP routes: %w", err)
	}
	return server, closeRuntime, nil
}

func runPostgreSQLFirstQueryRoute(ctx context.Context, serverConfig Config, logger *slog.Logger) error {
	server, closeRuntime, err := composePostgreSQLFirstQueryRoute(ctx, serverConfig, logger)
	if err != nil {
		return err
	}
	// The startup line keeps the source-schema placement visible for
	// operators; recomputing the validated assignment is free and cannot
	// diverge from what compose opened (the gate is pure).
	_, startSourceSchema, _, _ := postgresFirstQueryRouteAssignment(serverConfig.Server.PostgreSQL)
	return serveRuntime(ctx, server, serverConfig.Server.Listen, closeRuntime, "close PostgreSQL source runtime", logger,
		"listen", serverConfig.Server.Listen, "backend", serverConfig.Server.Backend,
		"source_schema", startSourceSchema, "no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
}
