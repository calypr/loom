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
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	shared "github.com/arangodb/go-driver/v2/arangodb/shared"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	queryapi "github.com/calypr/loom/internal/api/graphql/graph/query"
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/execution/chartifact"
	chartifactarango "github.com/calypr/loom/internal/dataframe/execution/chartifact/arango"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	bundlearango "github.com/calypr/loom/internal/dataframe/publication/arango"
	publicationclickhouse "github.com/calypr/loom/internal/dataframe/publication/clickhouse"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipe/exec"
	recipearango "github.com/calypr/loom/internal/dataframe/recipe/exec/arango"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	publicationarango "github.com/calypr/loom/internal/dataset/arango"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	"github.com/calypr/loom/internal/explorer/capability"
	explorercompilation "github.com/calypr/loom/internal/explorer/compilation"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	"github.com/calypr/loom/internal/ingest"
	arangostore "github.com/calypr/loom/internal/store/arango"
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

func classifyDataframeQueryError(err error) error {
	if err == nil {
		return err
	}
	switch {
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTablePivotCellCardinality)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTablePivotCellCardinality, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTablePivotUnlistedCategory)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTablePivotUnlistedCategory, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeConstructionRowValueMultipleValues)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeConstructionRowValueMultipleValues, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeConstructionExpansionEmpty)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeConstructionExpansionEmpty, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeExplicitGroupUnassignedMember)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeExplicitGroupUnassignedMember, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeRelationshipCardinalityViolation)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeRelationshipCardinalityViolation, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTemporalAnchorInvalid)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalAnchorInvalid, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTemporalPrecisionUnsupported)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalPrecisionUnsupported, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeTemporalTieAmbiguous)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeTemporalTieAmbiguous, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeUnitIdentityUnknown)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnitIdentityUnknown, "")
	case arangostore.IsQueryUserAssertion(err, string(dataframeerrors.CodeUnitDimensionIncompatible)):
		return dataframeerrors.Wrap(err, dataframeerrors.CodeUnitDimensionIncompatible, "")
	case arangostore.IsQueryMemoryLimitExceeded(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryMemoryLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "query_memory"}),
		)
	case arangostore.IsQueryResourceLimitExceeded(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryResourceLimitExceeded,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "query"}),
		)
	case arangostore.IsQueryOutOfMemory(err):
		return dataframeerrors.Wrap(
			err,
			dataframeerrors.CodeQueryBackendOutOfMemory,
			"",
			dataframeerrors.WithDetails(map[string]any{"backend": "arangodb", "resource": "memory"}),
		)
	default:
		return err
	}
}

func configuredAQLQueryRows(logger *slog.Logger, phase string, execute dataframeexecution.QueryRows) dataframeexecution.QueryRows {
	return func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
		started := time.Now()
		digest := sha256.Sum256([]byte(query))
		queryHash := hex.EncodeToString(digest[:])
		if logger != nil {
			logger.Info("dataframe AQL started", "request_id", requestIDFromContext(ctx), "phase", phase+"_start",
				"query_hash", queryHash, "query_bytes", len(query), "bind_vars", len(bindVars), "cursor_batch_size", batchSize)
		}
		err := preserveConfiguredQueryError(execute(ctx, query, batchSize, bindVars, visit))
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
		}
		logServerDiagnostic(logger, level, "dataframe AQL execution", requestIDFromContext(ctx), phase, time.Since(started), err,
			"query_hash", queryHash)
		return err
	}
}

func preserveConfiguredQueryError(err error) error {
	err = classifyDataframeQueryError(err)
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
	var arangoErr shared.ArangoError
	if errors.As(err, &arangoErr) {
		return arangoDiagnosticCause(arangoErr)
	}
	var arangoErrPointer *shared.ArangoError
	if errors.As(err, &arangoErrPointer) && arangoErrPointer != nil {
		return arangoDiagnosticCause(*arangoErrPointer)
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

func arangoDiagnosticCause(err shared.ArangoError) string {
	cause := fmt.Sprintf("ArangoDB error number %d (HTTP %d)", err.ErrorNum, err.Code)
	if err.ErrorMessage != "" {
		cause += ": " + err.ErrorMessage
	}
	return cause
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

const pivotCategoryScanTimeout = 10 * time.Second

func configuredCategoryScanner(logger *slog.Logger, recipeEngine *dataframeexecution.Engine) lifecycle.CategoryScanner {
	return func(ctx context.Context, receipt *explorer.CompilationReceipt, bindings recipe.RuntimeBindings, request dataframeexecution.CategoryScanRequest) (dataframeexecution.CategoryScanResult, error) {
		// Category discovery is a Pivot-only operation. Keep its deadline aligned
		// with the existing server-side preview runtime limit.
		scanCtx, cancel := context.WithTimeout(ctx, pivotCategoryScanTimeout)
		defer cancel()
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

func run(ctx context.Context, serverConfig Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))
	connOpts := arangostore.ConnectionOptions{
		URL:      serverConfig.Server.URL,
		Database: serverConfig.Server.Database,
	}

	lifecycleClient, err := arangostore.Open(ctx, connOpts.URL, connOpts.Database)
	if err != nil {
		return fmt.Errorf("open dataset lifecycle store: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		_ = lifecycleClient.Close(closeCtx)
	}()
	if err := lifecycleClient.Bootstrap(ctx, publicationarango.BootstrapSpec()); err != nil {
		return fmt.Errorf("bootstrap dataset lifecycle store: %w", err)
	}
	var degradation error
	if err := lifecycleClient.Bootstrap(ctx, recipearango.BootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, recipearango.RevisionBootstrapSpec()); err != nil {
		degradation = recordDegradation(logger, degradation, "bootstrap recipe revision registry", err)
	}
	if err := lifecycleClient.Bootstrap(ctx, explorerarango.BootstrapSpec()); err != nil {
		return fmt.Errorf("bootstrap Explorer store: %w", err)
	}
	recipeRegistry, err := recipearango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create recipe registry: %w", err)
	}
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
	lifecycleStore, err := publicationarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create dataset lifecycle store: %w", err)
	}
	activeManifestResolver := publicationcontract.ActiveResolver(lifecycleStore)

	discoveryCache := catalog.NewCache()
	catalogStore, err := catalogarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create catalog store: %w", err)
	}
	discoverFields := discoveryCache.DiscoverFields(catalogStore.DiscoverFields)
	discoverReferences := discoveryCache.DiscoverReferences(catalogStore.DiscoverReferences)

	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, catalogStore.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	dataframes := dataframeexecution.NewService(dataframeexecution.ServiceConfig{QueryRows: func(ctx context.Context, query string, batch int, binds map[string]any, visit func(map[string]any) error) error {
		return classifyDataframeQueryError(lifecycleClient.QueryRows(ctx, query, batch, binds, visit))
	}})
	// The lifecycle client already owns this Arango database. Reusing it avoids
	// a second connection that can fail independently during optional startup.
	publishedRegistry, err := bundlearango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create published dataframe registry: %w", err)
	}
	publicationReady := true
	if err := lifecycleClient.Bootstrap(ctx, bundlearango.BootstrapSpec()); err != nil {
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
		if err := lifecycleClient.Bootstrap(ctx, chartifactarango.BootstrapSpec()); err != nil {
			return fmt.Errorf("bootstrap private dataframe artifact catalog: %w", err)
		}
		privateArtifactCatalog, err := chartifactarango.NewCatalog(lifecycleClient)
		if err != nil {
			return fmt.Errorf("create private dataframe artifact catalog: %w", err)
		}
		privateClickHouseArtifacts, err = chartifact.New(chartifact.Config{
			Catalog: privateArtifactCatalog, ClickHouse: clickhouse,
			BatchRows: serverConfig.Server.RecipeBatchRows, BatchBytes: serverConfig.Server.RecipeBatchBytes,
		})
		if err != nil {
			return fmt.Errorf("create private ClickHouse artifact manager: %w", err)
		}
		materializationReader = &published.Reader{ClickHouse: clickhouse, Catalog: publishedRegistry, Logger: logger, MaxPage: 1000, ActiveManifestResolver: activeManifestResolver, ActiveReleaseResolver: lifecycleStore}
	}
	recipeRevisions, err := recipearango.NewRevisionRegistry(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create recipe revision registry: %w", err)
	}
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
		Registry:      recipeRegistry,
		Revisions:     recipeRevisions,
		ResolveBundle: recipeSchemaResolver(catalogStore.DiscoverFields, discoveryCache),
		PreparePreviewIndex: loggedPreviewIndexPreparation(logger, func(ctx context.Context, spec compiler.PreviewCoveringIndexSpec) error {
			var err error
			if len(spec.StoredValues) != 0 {
				if spec.Supersedes != nil {
					err = lifecycleClient.EnsurePreviewCoveringIndexWithStoredValuesReplacing(
						ctx, spec.Collection, spec.Name, spec.Fields, spec.StoredValues,
						spec.Supersedes.Name, spec.Supersedes.Fields,
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
		PreviewCollectionRevision: lifecycleClient.CollectionRevision,
		PreviewExplainQuery: func(ctx context.Context, query string, bindVars map[string]any) (arangostore.ExplainResult, error) {
			return lifecycleClient.Explain(ctx, arangostore.ExplainRequest{Query: query, BindVars: bindVars})
		},
		PreviewCollectionCount: lifecycleClient.CollectionCount,
		ClickHouseQueryRows:    clickHouseQueryRows,
		PreviewQueryRows: configuredAQLQueryRows(logger, "preview_query_rows", func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			return lifecycleClient.QueryRowsWithMaxRuntime(ctx, query, batchSize, bindVars, explorerPreviewTimeout, arangostore.RowVisitor(visit))
		}),
		ResolveClickHouseInputs:    resolveClickHouseInputs,
		WithExecutionReadPins:      withExecutionReadPins,
		PrivateClickHouseArtifacts: privateClickHouseArtifacts,
		QueryRows: configuredAQLQueryRows(logger, "query_rows", func(ctx context.Context, query string, batchSize int, bindVars map[string]any, visit func(map[string]any) error) error {
			return lifecycleClient.QueryRows(ctx, query, batchSize, bindVars, arangostore.RowVisitor(visit))
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
	activateExplorerRelease := func(ctx context.Context, project, generation string, selectors []publicationcontract.DataframeSelector) error {
		expectedRevision := int64(0)
		active, err := releaseService.Active(ctx, project)
		if err == nil {
			expectedRevision = active.Revision
		} else if !errors.Is(err, publicationcontract.ErrNoActiveRelease) {
			return err
		}
		_, err = releaseService.Activate(ctx, publicationcontract.ActivationRequest{
			Project: project, Generation: generation, GitCommit: generation,
			ExpectedRevision: expectedRevision, OptionalSelectors: selectors,
		})
		return err
	}
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
	explorerStore, err := explorerarango.New(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create Explorer store: %w", err)
	}
	if err := explorerStore.MigrateLegacyRepositoryConfigs(ctx); err != nil {
		return fmt.Errorf("migrate legacy Explorer repository configs: %w", err)
	}
	capabilitySnapshots, err := explorerarango.NewCapabilitySnapshotStore(lifecycleClient)
	if err != nil {
		return fmt.Errorf("create Explorer capability snapshot store: %w", err)
	}
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
			DiscoverReferences:     discoverReferences,
			DiscoverFields:         discoverFields,
			Dataframes:             dataframes,
			ScopeResolver:          scopeResolver,
			ActiveManifestResolver: activeManifestResolver,
			Explain: func(ctx context.Context, compiled dataframeexecution.CompiledQuery) error {
				_, err := explainCompiledQuery(ctx, lifecycleClient, compiled)
				return err
			},
		},
		MaterializationReader: materializationReader,
		Logger:                logger,
		RecipeControl: dataframeexecution.Control{Engine: recipeEngine, ExplainConnection: func(ctx context.Context, compiled dataframeexecution.CompiledQuery) (dataframeexecution.ExplainAssessment, error) {
			return explainCompiledQuery(ctx, lifecycleClient, compiled)
		}},
		RecipeAuthorizer: recipeAuthorization{resolver: scopeResolver},
		RecipeRevisions:  recipeRevisions,
		RecipeExecutions: graphresolver.NewAuthorizedRecipeExecutionReader(publishedRegistry, scopeResolver),
	})
	ingestRunner := loadapi.IngestRunner{BaseOptions: ingest.LoadOptions{
		ConnectionOptions: connOpts,
		Schema:            serverConfig.Server.Schema,
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
	tableShapeCapabilities, err := explorerarango.NewTableShapeCapabilityRepository(lifecycleClient)
	if err != nil {
		return fmt.Errorf("configure table-shape capability repository: %w", err)
	}
	lifecycleConfig := lifecycle.Config{
		SemanticInventory:                  catalogStore.PageSemanticInventory,
		ResolveSemanticInventorySelections: catalogStore.ResolveSemanticInventorySelections,
		SelectionMembersCollection:         explorerarango.SelectionMembersCollection,
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
