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
	"github.com/calypr/loom/internal/backends/postgres/control"
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

func newExplorerReleaseActivator(releaseService *publicationcontract.ReleaseService, devReleaseCASFault *atomic.Bool) func(context.Context, string, string, []publicationcontract.DataframeSelector) error {
	return func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) error {
		expectedRevision := int64(0)
		devFaultArmed := devReleaseCASFault != nil && devReleaseCASFault.CompareAndSwap(true, false)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision
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

var ErrPostgreSQLRuntimeNotWired = errors.New("server.backend postgres is configured, but the PostgreSQL source runtime is not wired yet; refusing to start on the ArangoDB backend")

func RunWithConfig(ctx context.Context, serverConfig Config) error {
	return run(ctx, serverConfig)
}

func run(ctx context.Context, serverConfig Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	backend, err := normalizeSourceBackend(serverConfig.Server.Backend)
	if err != nil {
		return err
	}
	if backend == sourceBackendPostgres {
		if _, _, err := postgresRuntimeAssignment(serverConfig.Server.PostgreSQL); err == nil {
			return runPostgreSQLFullRuntime(ctx, serverConfig, logger)
		}
		return runPostgreSQLFirstQueryRoute(ctx, serverConfig, logger)
	}
	return runArangoRuntime(ctx, serverConfig, logger)
}

const postgreSQLFirstQueryRouteAcquireTimeout = 30 * time.Second

func postgreSQLFirstQueryRouteWiringError(operation, enablingTicket string) error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable,
		operation+" is not served by the PostgreSQL first query route; it belongs to "+enablingTicket,
		dataframeerrors.WithRetryable(false),
		dataframeerrors.WithDetails(map[string]any{"backend": "postgresql", "stage": "wiring", "enablingTicket": enablingTicket}))
}

func postgresFirstQueryRouteAssignment(pg PostgreSQLConfig) (control.SeriesSchemas, string, string, error) {
	source, hasSource := pg.Schemas[string(control.SeriesSource)]
	manifest, hasManifest := pg.Schemas[string(control.SeriesDatasetRelease)]
	source, manifest = strings.TrimSpace(source), strings.TrimSpace(manifest)
	if !hasSource || !hasManifest || source == "" || manifest == "" || source == manifest || len(pg.Schemas) != 2 {
		return nil, "", "", ErrPostgreSQLRuntimeNotWired
	}
	return control.SeriesSchemas{
		control.SeriesSource:         source,
		control.SeriesDatasetRelease: manifest,
	}, source, manifest, nil
}

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

type redactedRouteError struct {
	msg   string
	cause error
}

func (e *redactedRouteError) Error() string { return e.msg }
func (e *redactedRouteError) Unwrap() error { return e.cause }

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

const (
	pgReadinessStateClosed           = "runtime_closed"
	pgReadinessStateConnectivityDown = "connectivity_down"
	pgReadinessStateUnsupportedVer   = "unsupported_version"
	pgReadinessStateSchemaMissing    = "schema_missing"
	pgReadinessStateSchemaUnmigrated = "schema_unmigrated"
	pgReadinessStateSchemaMismatch   = "schema_mismatch"
	pgReadinessStateUnknown          = "core_state_unknown"
)

func postgreSQLCoreReadinessState(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, control.ErrRuntimeClosed):
		return pgReadinessStateClosed
	case errors.Is(err, control.ErrConnect):
		return pgReadinessStateConnectivityDown
	case errors.Is(err, control.ErrUnsupportedVersion):
		return pgReadinessStateUnsupportedVer
	case errors.Is(err, control.ErrInvalidSchemaName):
		return pgReadinessStateSchemaMissing
	case errors.Is(err, control.ErrMigrationPending):
		return pgReadinessStateSchemaUnmigrated
	case errors.Is(err, control.ErrMigrationChecksumMismatch),
		errors.Is(err, control.ErrFutureSchemaVersion),
		errors.Is(err, control.ErrMigrationMissing),
		errors.Is(err, control.ErrMigrationOutOfOrder):
		return pgReadinessStateSchemaMismatch
	default:
		return pgReadinessStateUnknown
	}
}

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

type postgreSQLFirstQueryDataframeReleases struct{}

func (postgreSQLFirstQueryDataframeReleases) GetExecution(context.Context, string) (publication.BundleExecution, error) {
	return publication.BundleExecution{}, postgreSQLFirstQueryRouteWiringError("Dataframe release reads", "pg-runtime-server-composition")
}

func (postgreSQLFirstQueryDataframeReleases) GetPointer(context.Context, string) (publication.BundlePointer, error) {
	return publication.BundlePointer{}, postgreSQLFirstQueryRouteWiringError("Dataframe release pointer reads", "pg-runtime-server-composition")
}

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

	var cleanups []func() error
	defer func() {
		if err == nil {
			return
		}
		_ = runRuntimeCloseSteps(cleanups)
	}()
	dsn := postgresFirstQueryRouteDSN(pg)
	runtime, err := openPostgresBackend(ctx, control.Config{
		DSN:            dsn,
		MaxConns:       int32(pg.MaxOpenConns),
		MinConns:       int32(pg.MaxIdleConns),
		AcquireTimeout: postgreSQLFirstQueryRouteAcquireTimeout,
		Schemas:        assignment,
		Setup:          control.SetupNone,
	})
	if err != nil {
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("open PostgreSQL source runtime: %w", err), pg, dsn)
	}
	cleanups = append(cleanups, func() error {
		return postgresFirstQueryRouteError(runtime.Close(), pg, dsn)
	})
	closeRuntime = func() error { return runRuntimeCloseSteps(cleanups) }

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

	discoveryCache := catalog.NewCache()
	discoverFields := discoveryCache.DiscoverFields(runtime.DiscoverFields)

	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, runtime.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return nil, nil, fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			CompileTarget:          sourceCompileTarget,
			DiscoverReferences:     nil,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: runtime.Manifests,
			Explain:                nil,
		},
		MaterializationReader: nil,
		Logger:                logger,
		RecipeControl: postgreSQLFirstQueryRecipeControl{Control: dataframeexecution.Control{
			ExplainPostgreSQLConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
				return explainPostgreSQLCompiledQuery(ctx, runtime.PlanJSON, compiled)
			},
		}},
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
	_, startSourceSchema, _, _ := postgresFirstQueryRouteAssignment(serverConfig.Server.PostgreSQL)
	return serveRuntime(ctx, server, serverConfig.Server.Listen, closeRuntime, "close PostgreSQL source runtime", logger,
		"listen", serverConfig.Server.Listen, "backend", serverConfig.Server.Backend,
		"source_schema", startSourceSchema, "no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
}
