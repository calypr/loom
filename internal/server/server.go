package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/calypr/loom/internal/authscope"
	arangofactory "github.com/calypr/loom/internal/backends/arango"
	backendspostgres "github.com/calypr/loom/internal/backends/postgres"
	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/command"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	"github.com/calypr/loom/internal/dataframe/semantic"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/ingest"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
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

func configuredAQLSourceRows(logger *slog.Logger, phase string, execute dataframeexecution.SourceRows) dataframeexecution.SourceRows {
	return func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		// This deployment selected the Arango target, so the source row port
		// serves AQL-tagged commands only. The logging and hash policy
		// derives the AQL view through closed dispatch: a command that is
		// not AQL-tagged is rejected here, before execute and long before
		// any driver call, and PostgreSQL statement SQL is never logged or
		// hashed as if it were AQL.
		query, bindVars, dispatchErr := request.Command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		batchSize := request.BatchSize
		// Composition wires execution's neutral query-memory budget into the
		// Arango adapter's own mechanism. Vendor interpretation of failures
		// stays inside the adapter; this only requests a capability.
		if budget, ok := dataframeexecution.QueryMemoryBudgetFromContext(ctx); ok && budget > 0 {
			ctx = arangofactory.WithQueryMemoryLimit(ctx, budget)
		}
		started := time.Now()
		digest := sha256.Sum256([]byte(query))
		queryHash := hex.EncodeToString(digest[:])
		if logger != nil {
			logger.Info("dataframe AQL started", "request_id", requestIDFromContext(ctx), "phase", phase+"_start",
				"query_hash", queryHash, "query_bytes", len(query), "bind_vars", len(bindVars), "cursor_batch_size", batchSize)
		}
		err := preserveConfiguredQueryError(execute(ctx, request, visit))
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		logServerDiagnostic(logger, level, "dataframe AQL execution", requestIDFromContext(ctx), phase, time.Since(started), err,
			"query_hash", queryHash)
		return err
	}
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
	// The factory owns the lifecycle client: one connection, the required
	// collection bootstrap policy (dataset lifecycle, Explorer), and the
	// adapter assembly. Deployment-gated degradable steps (recipe
	// registries, dataframe registry) stay here so the degradation and
	// logging policy remains composition-owned.
	arangoBackend, err := arangofactory.Open(ctx, arangofactory.Config{
		URL:      serverConfig.Server.URL,
		Database: serverConfig.Server.Database,
	})
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = arangoBackend.Close(closeCtx)
	}()
	lifecycleClient := arangoBackend.Client
	var degradation error
	if err := lifecycleClient.Bootstrap(ctx, arangofactory.RecipeBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, arangofactory.RevisionBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe revision registry", err)
	}
	recipeRegistry := arangoBackend.RecipeRegistry
	if serverConfig.Server.ClickHouse.Enabled {
		data, err := os.ReadFile(serverConfig.Server.Dataframer.Recipe)
		if err != nil {
			return fmt.Errorf("read dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		defaultBundle, err := recipe.Parse(data)
		if err != nil {
			return fmt.Errorf("parse dataframer recipe %q: %w", serverConfig.Server.Dataframer.Recipe, err)
		}
		if _, err := (exec.PersistentRegistry{Store: recipeRegistry}).RegisterVersion(ctx, defaultBundle); err != nil {
			degradation = recordDegradation(logger, degradation, "register default dataframe recipe", err)
		}
	}
	lifecycleStore := arangoBackend.Dataset
	activeManifestResolver := publicationcontract.ActiveResolver(lifecycleStore)

	discoveryCache := catalog.NewCache()
	catalogStore := arangoBackend.Catalog
	discoverFields := discoveryCache.DiscoverFields(catalogStore.DiscoverFields)
	discoverReferences := discoveryCache.DiscoverReferences(catalogStore.DiscoverReferences)

	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, catalogStore.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{SourceRows: func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		query, binds, dispatchErr := request.Command.AQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		return lifecycleClient.QueryRows(ctx, query, request.BatchSize, binds, arangofactory.RowVisitor(visit))
	}})
	// The lifecycle client already owns this Arango database. Reusing it avoids
	// a second connection that can fail independently during optional startup.
	// The compile target is selected once here, from the source store
	// configuration: this deployment serves sources from Arango, so every
	// compiler boundary (recipe engine, FHIR query compilation, graph mode,
	// and preview) uses the AQL target through the same target-aware
	// compiler operations. A PostgreSQL deployment would select
	// compiler.PostgreSQLTarget once instead; per-request dialect selection
	// is not available to any caller.
	sourceCompileTarget := compiler.ArangoTarget()

	publishedRegistry := arangoBackend.PublishedRegistry
	publicationReady := true
	if err := lifecycleClient.Bootstrap(ctx, arangofactory.PublishedBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap dataframe registry", err)
		publicationReady = false
	}
	var clickhouse *clickhousestore.Client
	var materializationReader *published.Reader
	var privateClickHouseArtifacts *chartifact.Manager
	if serverConfig.Server.ClickHouse.Enabled {
		clickhouse, err = clickhousestore.New(clickhousestore.Options{URL: serverConfig.Server.ClickHouse.URL, Database: serverConfig.Server.ClickHouse.Database, Username: serverConfig.Server.ClickHouse.Username, Password: serverConfig.Server.ClickHouse.Password})
		if err != nil {
			return fmt.Errorf("create ClickHouse client: %w", err)
		}
		defer clickhouse.Close()
		// The Arango-backed dataframe loader publishes into this database. Create
		// it during server startup so a fresh ClickHouse instance does not require
		// an operator to run a separate DDL/API step before materialization.
		if err := clickhouse.EnsureDatabase(ctx); err != nil {
			degradation = recordDegradation(logger, degradation, "ClickHouse database", err)
			publicationReady = false
		}
		if err := lifecycleClient.Bootstrap(ctx, arangofactory.ArtifactBootstrapSpec()); err != nil {
			return fmt.Errorf("bootstrap private dataframe artifact catalog: %w", err)
		}
		privateArtifactCatalog, err := arangofactory.NewArtifactCatalog(lifecycleClient)
		if err != nil {
			return fmt.Errorf("create private dataframe artifact catalog: %w", err)
		}
		privateClickHouseArtifacts, err = chartifact.New(chartifact.Config{Catalog: privateArtifactCatalog, ClickHouse: clickhouse,
			BatchRows: serverConfig.Server.RecipeBatchRows, BatchBytes: serverConfig.Server.RecipeBatchBytes,
		})
		if err != nil {
			return fmt.Errorf("create private ClickHouse artifact manager: %w", err)
		}
		materializationReader = &published.Reader{ClickHouse: clickhouse, Catalog: publishedRegistry, Logger: logger, MaxPage: 1000, ActiveManifestResolver: activeManifestResolver, ActiveReleaseResolver: lifecycleStore}
	}
	recipeRevisions := arangoBackend.RecipeRevisions
	var clickHouseQueryRows dataframeexecution.ClickHouseQueryRows
	if clickhouse != nil {
		clickHouseQueryRows = clickhouse.QueryRowsArgsVisit
	}
	var resolveClickHouseInputs dataframeexecution.ResolveClickHouseInputs
	var withExecutionReadPins dataframeexecution.WithExecutionReadPins
	if materializationReader != nil {
		resolver := clickHouseCombineInputResolver{reader: materializationReader, scopes: scopeResolver}
		resolveClickHouseInputs = resolver.resolve
		withExecutionReadPins = materializationReader.WithExecutionReadPins
	}
	recipeEngine, err := dataframeexecution.New(dataframeexecution.Config{
		CompileTarget: sourceCompileTarget,
		Registry:      recipeRegistry,
		Revisions:     recipeRevisions,
		ResolveBundle: recipeSchemaResolver(catalogStore.DiscoverFields, discoveryCache),
		PreparePreviewIndex: loggedPreviewIndexPreparation(logger, func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) error {
			var err error
			if len(spec.StoredValues) != 0 {
				if spec.Supersedes != nil {
					err = lifecycleClient.EnsurePreviewCoveringIndexWithStoredValuesReplacing(
						ctx, spec.Collection, spec.Name, spec.Fields, spec.StoredValues,
						spec.Supersedes.Name, spec.Supersedes.Fields, spec.Supersedes.StoredValues,
					)
				} else {
					err = lifecycleClient.EnsurePreviewCoveringIndexWithStoredValues(ctx, spec.Collection, spec.Name, spec.Fields, spec.StoredValues)
				}
			} else if spec.Supersedes != nil {
				err = lifecycleClient.EnsurePreviewCoveringIndexReplacing(ctx, spec.Collection, spec.Name, spec.Fields, spec.Supersedes.Name, spec.Supersedes.Fields)
			} else {
				err = lifecycleClient.EnsurePreviewCoveringIndex(ctx, spec.Collection, spec.Name, spec.Fields)
			}
			return err
		}),
		PreviewCoveringIndexes: func(ctx context.Context, collection string) ([]dataframeexecution.PreviewCollectionIndex, error) {
			indexes, err := lifecycleClient.ListPreviewCoveringIndexes(ctx, collection)
			if err != nil {
				return nil, err
			}
			return previewCollectionIndexesFromStore(indexes), nil
		},
		PreviewCollectionRevision: lifecycleClient.CollectionRevision,
		PreviewCollectionScans: func(ctx context.Context, command command.CompiledCommand, collection string) (dataframeexecution.PreviewCollectionScanEvidence, error) {
			query, bindVars, dispatchErr := command.AQLParameters()
			if dispatchErr != nil {
				return dataframeexecution.PreviewCollectionScanEvidence{}, dispatchErr
			}
			result, err := lifecycleClient.Explain(ctx, arangofactory.ExplainRequest{Query: query, BindVars: bindVars})
			if err != nil {
				return dataframeexecution.PreviewCollectionScanEvidence{}, err
			}
			return PreviewCollectionScanEvidenceFromExplain(result, collection), nil
		},
		PreviewCollectionCount: lifecycleClient.CollectionCount,
		ClickHouseQueryRows:    clickHouseQueryRows,
		PreviewSourceRows: configuredAQLSourceRows(logger, "preview_query_rows", func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
			query, bindVars, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return lifecycleClient.QueryRowsWithMaxRuntime(ctx, query, request.BatchSize, bindVars, previewQueryMaxRuntime(ctx), arangofactory.RowVisitor(visit))
		}),
		ResolveClickHouseInputs:    resolveClickHouseInputs,
		WithExecutionReadPins:      withExecutionReadPins,
		PrivateClickHouseArtifacts: privateClickHouseArtifacts,
		SourceRows: configuredAQLSourceRows(logger, "query_rows", func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
			query, bindVars, dispatchErr := request.Command.AQLParameters()
			if dispatchErr != nil {
				return dispatchErr
			}
			return lifecycleClient.QueryRows(ctx, query, request.BatchSize, bindVars, arangofactory.RowVisitor(visit))
		}),
		ScopeDigest:  recipeScopeDigest,
		RootPageRows: serverConfig.Server.RecipeQueryPageRows,
	})
	if err != nil {
		return fmt.Errorf("create dataframe recipe engine: %w", err)
	}
	var bundleTarget publication.Target
	if serverConfig.Server.ClickHouse.Enabled && publicationReady {
		bundleStore, err := publicationclickhouse.NewBundleStore(clickhouse, publishedRegistry)
		if err != nil {
			return fmt.Errorf("create dataframe bundle store: %w", err)
		}
		bundleStore.SetActiveReleaseReader(lifecycleStore)
		if err := bundleStore.Reconcile(ctx, time.Now().UTC().Add(-2*time.Minute)); err != nil {
			degradation = recordDegradation(logger, degradation, "dataframe publication reconciliation", err)
			publicationReady = false
		}
		if publicationReady {
			bundleTarget = bundleStore
		}
	}
	verificationStore := publicationVerificationStore{executions: publishedRegistry}
	releaseService := &publicationcontract.ReleaseService{Manifests: lifecycleStore, Releases: lifecycleStore, Verifier: verificationStore, Required: serverConfig.Server.RequiredDataframeSelectors}
	var activationConflictOnce atomic.Bool
	activationConflictOnce.Store(serverConfig.Server.DevActivationConflictOnce)
	var repositoryActivationConflictOnce atomic.Bool
	repositoryActivationConflictOnce.Store(serverConfig.Server.DevRepositoryActivationConflictOnce)
	activateExplorerRelease := newExplorerReleaseActivator(releaseService, &repositoryActivationConflictOnce)
	prepareExplorerRelease := func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) (publicationcontract.ProjectRelease, int64, error) {
		expectedRevision := int64(0)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision
		} else if !errors.Is(err, publicationcontract.ErrNoActiveRelease) {
			return publicationcontract.ProjectRelease{}, 0, err
		}
		release, err := releaseService.Create(ctx, publicationcontract.ActivationRequest{
			Project: project, Generation: generation, GitCommit: generation,
			OptionalSelectors: selectors,
		})
		// Return a deliberately stale CAS revision exactly once in the isolated
		// dev fault scenario. The candidate remains retained for audit, while
		// PublishAuthoring's atomic activation fails without moving the prior
		// active release pointer or exposing candidate rows.
		if err == nil && activationConflictOnce.CompareAndSwap(true, false) {
			expectedRevision++
		}
		return release, expectedRevision, err
	}
	validateExplorerReleaseGeneration := func(ctx context.Context, project, generation string) error {
		return releaseService.ValidateGeneration(ctx, project, generation)
	}
	explorerStore := arangoBackend.Explorer
	if err := explorerStore.MigrateLegacyRepositoryConfigs(ctx); err != nil {
		return fmt.Errorf("migrate legacy Explorer repository configs: %w", err)
	}
	capabilitySnapshots := arangoBackend.ExplorerCapabilitySnapshots
	capabilityResolver, err := newExplorerCapabilityResolver(catalogStore, scopeResolver, activeManifestResolver, capabilitySnapshots)
	if err != nil {
		return fmt.Errorf("create Explorer capability resolver: %w", err)
	}
	explorerService, err := explorer.NewService(explorerStore)
	if err != nil {
		return fmt.Errorf("create Explorer service: %w", err)
	}
	artifactStore, err := artifactfs.New(serverConfig.Server.ArtifactDirectory)
	if err != nil {
		return fmt.Errorf("create Explorer artifact store: %w", err)
	}
	resolver := graphresolver.NewResolver(graphresolver.ResolverConfig{
		DataframeQuery: queryapi.Config{
			CompileTarget:          sourceCompileTarget,
			DiscoverReferences:     discoverReferences,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: activeManifestResolver,
			Explain: func(ctx context.Context, compiled compiler.CompiledQuery) error {
				_, err := explainCompiledQuery(ctx, lifecycleClient, compiled)
				return err
			},
		},
		MaterializationReader: materializationReader,
		Logger:                logger,
		RecipeControl: dataframeexecution.Control{Engine: recipeEngine, ExplainConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
			return explainCompiledQuery(ctx, lifecycleClient, compiled)
		}, ExplainPostgreSQLConnection: func(ctx context.Context, compiled compiler.CompiledQuery) (dataframeexecution.PostgreSQLPlanAssessment, error) {
			// The PostgreSQL source runtime is configuration-only until its
			// wiring slice lands (run() refuses to start on that backend), so
			// no pool-shaped executor exists here yet. A PostgreSQL-tagged
			// command therefore fails closed with a typed backend failure;
			// it is never routed to the Arango EXPLAIN policy surface. When
			// the runtime lands, pass Runtime.PlanJSON (postgres.Open) over the
			// readiness-checked pool instead of nil.
			return explainPostgreSQLCompiledQuery(ctx, nil, compiled)
		}},
		RecipeAuthorizer: recipeAuthorization{resolver: scopeResolver},
		RecipeRevisions:  recipeRevisions,
		RecipeExecutions: graphresolver.NewAuthorizedRecipeExecutionReader(publishedRegistry, scopeResolver),
	})
	ingestRunner := loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
		ConnectionOptions: arangofactory.Config{
			URL:      serverConfig.Server.URL,
			Database: serverConfig.Server.Database,
		},
		Schema: serverConfig.Server.Schema,
	}}
	generationService, err := loadapi.NewService(loadapi.ServiceConfig{
		LoadGeneration:      ingestRunner.RunGeneration,
		GenerationActivator: lifecycleStore,
		DataframeReleases:   publishedRegistry,
		Logger:              logger,
		OnSuccess: func(project string) {
			discoveryCache.InvalidateProject(project)
			if scopeResolver != nil {
				scopeResolver.InvalidateProject(project)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("create generation load service: %w", err)
	}
	compileReceipt := func(ctx context.Context, request lifecycle.CompileReceiptRequest) (*explorer.CompilationReceipt, error) {
		var resolveCombineSchemas combineInputSchemaResolver
		if materializationReader != nil {
			resolveCombineSchemas = func(ctx context.Context, output lower.CompiledRecipeOutput, bindings recipe.RuntimeBindings) (resolvedCombineInputSchema, error) {
				return exactPublishedCombineInputSchemas(ctx, output, bindings, materializationReader, materializationReader.WithExecutionReadPins, scopeResolver)
			}
		}
		return compileExplorerReceipt(ctx, request, capabilityResolver, recipeEngine, explorerService, logger, resolveCombineSchemas)
	}
	persistPublishedWorkspace, err := localWorkspaceWriter(serverConfig.Server.LocalWorkspaceWriteback, serverConfig.Server.LocalWorkspaceProject)
	if err != nil {
		return fmt.Errorf("configure local workspace writeback: %w", err)
	}
	populationMappingCursorCodec, err := lifecycle.NewHMACPopulationMappingCursorCodec(serverConfig.Server.PopulationMappingCursorSecret)
	if err != nil {
		return fmt.Errorf("configure population mapping cursor signing: %w", err)
	}
	var artifactPublishedReader lifecycle.ArtifactPublishedReader
	if materializationReader != nil {
		artifactPublishedReader = materializationReader
		if serverConfig.Server.DevArtifactRowDelay > 0 {
			artifactPublishedReader = artifactDelayReader{next: artifactPublishedReader, delay: serverConfig.Server.DevArtifactRowDelay}
		}
	}
	schemaIndex, err := fhirschema.GeneratedIndex()
	if err != nil {
		return fmt.Errorf("load generated schema index for row choices: %w", err)
	}
	rowChoiceResolver, err := lifecycle.NewSchemaRowChoiceResolver(schemaIndex)
	if err != nil {
		return fmt.Errorf("configure row-choice schema resolver: %w", err)
	}
	explicitGroupResolver, err := lifecycle.NewRepositoryExplicitGroupRevisionResolver(explorerStore)
	if err != nil {
		return fmt.Errorf("configure explicit group revision resolver: %w", err)
	}
	tableShapeCapabilities, err := arangofactory.NewTableShapeCapabilityRepository(lifecycleClient)
	if err != nil {
		return fmt.Errorf("configure table-shape capability repository: %w", err)
	}
	lifecycleConfig := lifecycle.Config{
		SemanticInventory:                  catalogStore.PageSemanticInventory,
		ResolveSemanticInventorySelections: catalogStore.ResolveSemanticInventorySelections,
		SelectionMembersCollection:         arangofactory.SelectionMembersCollection,
		InterpretationRepository:           explorerStore,
		PopulationMappingCursorCodec:       populationMappingCursorCodec,
		SelectionSourceResolver:            published.SelectionSourceAdapter{Reader: materializationReader},
		SelectionReferenceValidator:        explorerStore.ValidateSelectionReferences,
		RowChoiceResolver:                  rowChoiceResolver,
		RowChoicePlanner:                   rowChoiceResolver,
		ExplicitGroupResolver:              explicitGroupResolver,
		ExplicitGroupRepository:            explorerStore,
		TableShapeCapabilities:             tableShapeCapabilities,
		ScanCategories:                     configuredCategoryScanner(logger, recipeEngine),
		CompileReceipt:                     compileReceipt,
		ConstructionSourceStage: func(ctx context.Context, request lifecycle.ConstructionSourceStageRequest) (explorer.ReceiptConstructionStage, error) {
			return compileConstructionSourceStage(ctx, request, recipeEngine)
		},
		Capability: lifecycle.CapabilityResolver{
			Current: func(ctx context.Context, project, _ string, generation string) (capability.Snapshot, error) {
				return capabilityResolver.Resolve(ctx, project, generation)
			},
			Token:          capabilityResolver.ResolveToken,
			ForCompilation: capabilityResolver.ResolveForCompilation,
			ForExecution:   capabilityResolver.ResolveForExecution,
			Catalog:        authoringV2Catalog,
		},
		ReceiptLookup: func(ctx context.Context, project, explorerID, receiptID string) (*explorer.CompilationReceipt, error) {
			return explorerService.CompilationReceiptForExplorer(ctx, project, explorerID, receiptID)
		},
		ArtifactStore:    artifactStore,
		PublishedReader:  artifactPublishedReader,
		ArtifactTTL:      serverConfig.Server.ArtifactTTL,
		ArtifactMaxRows:  serverConfig.Server.ArtifactMaxRows,
		ArtifactMaxBytes: serverConfig.Server.ArtifactMaxBytes,
		PreviewReceipt: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, visit func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			if receipt == nil {
				return dataframeexecution.PreviewSummary{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt preview resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.PreviewSummary{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			output := ""
			if len(bindings.OutputNames) > 0 {
				output = bindings.OutputNames[0]
			}
			summary, previewErr := recipeEngine.PreviewOutput(ctx, resolved, dataframeexecution.PreviewRequest{Output: output, Limit: bindings.PreviewLimit, IncludeRowIdentity: bindings.IncludeRowIdentity}, visit)
			if previewErr != nil {
				logReceiptPreviewContractFailure(logger, requestIDFromContext(ctx), receipt.ID, output, previewErr)
			}
			return summary, previewErr
		},
		ValidateReceiptStream: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings) error {
			err := validateReceiptFullStream(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer row-value policy full-stream validation failed", "receipt_id", receipt.ID, "error", err)
			}
			return err
		},
		PopulationMapping: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, output string, memberIDs []string, after string, limit int) (dataframeexecution.PopulationMappingResult, error) {
			if receipt == nil {
				return dataframeexecution.PopulationMappingResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt population mapping resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.PopulationMappingResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			reader := dataframeexecution.PopulationMemberReaderFunc(func(_ context.Context, visit func(string) error) error {
				for _, id := range memberIDs {
					if err := visit(id); err != nil {
						return err
					}
				}
				return nil
			})
			return recipeEngine.PopulationMapping(ctx, resolved, dataframeexecution.PopulationMappingRequest{Output: output, AfterMemberID: after, MaxUnmapped: limit}, reader)
		},
		CellTrace: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CellTraceRequest) (dataframeexecution.CellTraceResult, error) {
			if receipt == nil {
				return dataframeexecution.CellTraceResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt cell trace resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.CellTraceResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.CellTrace(ctx, resolved, request)
		},
		RowLineage: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.RowLineageRequest) (dataframeexecution.RowLineageResult, error) {
			if receipt == nil {
				return dataframeexecution.RowLineageResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt row lineage resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.RowLineageResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.RowLineage(ctx, resolved, request)
		},
		TableShapeExclusions: func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.TableShapeExclusionRequest) (dataframeexecution.TableShapeExclusionResult, error) {
			if receipt == nil {
				return dataframeexecution.TableShapeExclusionResult{}, fmt.Errorf("compilation receipt is required")
			}
			resolved, err := compileValidatedReceiptResolution(ctx, recipeEngine, receipt, bindings)
			if err != nil {
				logger.Error("Explorer receipt table-shape exclusion resolution failed", "receipt_id", receipt.ID, "error", err)
				return dataframeexecution.TableShapeExclusionResult{}, classifyReceiptPreviewResolutionError(receipt.ID, err)
			}
			return recipeEngine.TableShapeExclusions(ctx, resolved, request)
		},
		MaterializeReceipt: explorerReceiptMaterializer(
			recipeEngine, bundleTarget, publishedRegistry, degradation, logger,
			serverConfig.Server.RecipeBatchRows, serverConfig.Server.RecipeBatchBytes,
			serverConfig.Server.RecipeQualityMaxRows, serverConfig.Server.RecipeQualityMaxDistinctKeys,
		),
		ValidateReleaseGeneration: validateExplorerReleaseGeneration,
		ActivateRelease:           activateExplorerRelease,
		PrepareRelease:            prepareExplorerRelease,
		PersistPublishedWorkspace: persistPublishedWorkspace,
	}
	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return lifecycleClient.QueryRows(ctx, "RETURN {ready: true}", 1, nil, func(map[string]any) error { return nil })
		},
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if degradation != nil {
				return degradation
			}
			if clickhouse == nil {
				return nil
			}
			return clickhouse.Ping(ctx)
		}, ClickHouseEnabled: serverConfig.Server.ClickHouse.Enabled})
	if err != nil {
		return fmt.Errorf("create HTTP server: %w", err)
	}
	explorerHandlers := newExplorerHTTPHandlers(authorizer, func(ctx context.Context, principal *authscope.Principal, project string) error {
		if scopeResolver == nil {
			return nil
		}
		return scopeResolver.AuthorizeReadProject(ctx, principal, project)
	}, explorerService, lifecycleConfig)
	explorerHandlers.constructionInputs = constructionInputsCatalog{
		reader: materializationReader, catalog: publishedRegistry, capabilities: lifecycleConfig.Capability,
		scopes: scopeResolver, explorers: explorerService, revisions: explorerService,
	}
	if err := registerRoutes(server, generationService, authorizer, resolver, explorerHandlers, publishedRegistry, scopeResolver); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}
	if privateClickHouseArtifacts != nil {
		reconcileCtx, cancelReconciler := context.WithCancel(ctx)
		reconcilerDone := startPrivateArtifactReconciler(reconcileCtx, privateClickHouseArtifacts, logger)
		defer func() {
			cancelReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-reconcilerDone:
			case <-timer.C:
				logger.Error("private ClickHouse artifact reconciler did not stop before shutdown deadline")
			}
		}()
	}
	if artifactStore != nil {
		artifactReconcileCtx, cancelArtifactReconciler := context.WithCancel(ctx)
		artifactReconcilerDone := startArtifactFSReconciler(artifactReconcileCtx, artifactStore, logger)
		defer func() {
			cancelArtifactReconciler()
			timer := time.NewTimer(cleanupTimeout)
			defer timer.Stop()
			select {
			case <-artifactReconcilerDone:
			case <-timer.C:
				logger.Error("filesystem artifact reconciler did not stop before shutdown deadline")
			}
		}()
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "listen", serverConfig.Server.Listen, "database", serverConfig.Server.Database, "no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
		errCh <- server.App().Listen(serverConfig.Server.Listen)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server stopped: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down HTTP server", "reason", ctx.Err())
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := server.App().ShutdownWithContext(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown failed: %w", err)
		}
	}
	return nil
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

// configuredPostgreSQLSourceRows is the PostgreSQL sibling of
// configuredAQLSourceRows for the deployment that selected the PostgreSQL
// target. The dispatch guard derives the SQL view through closed dispatch:
// a command that is not PostgreSQL-tagged is rejected here, before execute
// and long before any driver call, with the compiler's typed routing
// sentinel. The logging and hash policy records only the SHA-256 of the
// statement text plus its byte length and bind-value count; SQL text and
// bound values are never logged, hashed as if they were AQL, or routed to
// any AQL surface (this branch constructs no ArangoDB adapter at all).
func configuredPostgreSQLSourceRows(logger *slog.Logger, phase string, execute dataframeexecution.SourceRows) dataframeexecution.SourceRows {
	return func(ctx context.Context, request dataframeexecution.RowsRequest, visit dataframeexecution.RowVisitor) error {
		sqlText, bindValues, dispatchErr := request.Command.PostgreSQLParameters()
		if dispatchErr != nil {
			return dispatchErr
		}
		started := time.Now()
		digest := sha256.Sum256([]byte(sqlText))
		queryHash := hex.EncodeToString(digest[:])
		if logger != nil {
			logger.Info("dataframe SQL started", "request_id", requestIDFromContext(ctx), "phase", phase+"_start",
				"query_hash", queryHash, "statement_bytes", len(sqlText), "bind_values", len(bindValues))
		}
		err := preserveConfiguredQueryError(execute(ctx, request, visit))
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		logServerDiagnostic(logger, level, "dataframe SQL execution", requestIDFromContext(ctx), phase, time.Since(started), err,
			"query_hash", queryHash)
		return err
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
func composePostgreSQLFirstQueryRoute(ctx context.Context, serverConfig Config, logger *slog.Logger) (*httpapi.HTTPServer, func() error, error) {
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

	dsn := postgresFirstQueryRouteDSN(pg)
	runtime, err := backendspostgres.Open(ctx, backendspostgres.Config{
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
	closeRuntime := func() error {
		return postgresFirstQueryRouteError(runtime.Close(), pg, dsn)
	}

	// The compile target is selected exactly once from the source schema
	// placement, mirroring the executor witnesses: per-request dialect
	// selection is not available to any caller.
	binding, err := ir.NewPostgresStorageBinding(sourceSchema)
	if err != nil {
		_ = closeRuntime()
		return nil, nil, postgresFirstQueryRouteError(fmt.Errorf("bind PostgreSQL storage placement: %w", err), pg, dsn)
	}
	sourceCompileTarget, err := compiler.PostgreSQLTarget(binding)
	if err != nil {
		_ = closeRuntime()
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
		_ = closeRuntime()
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
		_ = closeRuntime()
		return nil, nil, fmt.Errorf("create generation load service: %w", err)
	}

	server, err := httpapi.NewHTTPServer(httpapi.HTTPConfig{
		Authenticator: authenticator,
		Authorizer:    authorizer,
		Logger:        logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return runtime.Health(ctx)
		},
		ClickHouseReadyCheck: nil,
		ClickHouseEnabled:    false,
	})
	if err != nil {
		_ = closeRuntime()
		return nil, nil, fmt.Errorf("create HTTP server: %w", err)
	}
	// Explorer handlers stay nil: every Explorer/authoring/row/preview/trace
	// route already returns its typed AUTHORING_UNAVAILABLE/TRACE_UNAVAILABLE/
	// ROW_LINEAGE_UNAVAILABLE refusal through the nil-handler guards, and the
	// route census test pins that none of them ever serves Arango-backed data.
	if err := registerRoutes(server, generationService, authorizer, resolver, nil, scopeResolver); err != nil {
		_ = closeRuntime()
		return nil, nil, fmt.Errorf("register HTTP routes: %w", err)
	}
	return server, closeRuntime, nil
}

func runPostgreSQLFirstQueryRoute(ctx context.Context, serverConfig Config, logger *slog.Logger) error {
	server, closeRuntime, err := composePostgreSQLFirstQueryRoute(ctx, serverConfig, logger)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := closeRuntime(); closeErr != nil {
			logger.Error("close PostgreSQL source runtime", "error", closeErr)
		}
	}()

	// The startup line keeps the source-schema placement visible for
	// operators; recomputing the validated assignment is free and cannot
	// diverge from what compose opened (the gate is pure).
	_, startSourceSchema, _, _ := postgresFirstQueryRouteAssignment(serverConfig.Server.PostgreSQL)
	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "listen", serverConfig.Server.Listen, "backend", serverConfig.Server.Backend,
			"source_schema", startSourceSchema, "no_auth", serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated)
		errCh <- server.App().Listen(serverConfig.Server.Listen)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("server stopped: %w", err)
		}
	case <-ctx.Done():
		logger.Info("shutting down HTTP server", "reason", ctx.Err())
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := server.App().ShutdownWithContext(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown failed: %w", err)
		}
	}
	return nil
}
