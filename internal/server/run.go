package server

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/calypr/loom/internal/catalog/discoverycache"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	publicationcontract "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/explorer/artifactfs"
	arangostore "github.com/calypr/loom/internal/store/arango"
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
	storage, err := wireStorage(ctx, serverConfig, logger, lifecycleClient)
	if err != nil {
		return err
	}
	discoveryCache := discoverycache.NewCache()
	auth, err := wireAuth(serverConfig, serverConfig.Server.AllowUnauthenticated || serverConfig.Auth.AllowUnauthenticated, storage.catalog.DiscoverExistingAuthResourcePaths)
	if err != nil {
		return fmt.Errorf("configure authentication: %w", err)
	}
	authenticator, authorizer, scopeResolver := auth.authenticator, auth.authorizer, auth.scopeResolver

	dataframe, err := wireDataframe(ctx, serverConfig, logger, lifecycleClient, storage, discoveryCache, scopeResolver)
	if err != nil {
		return err
	}
	if dataframe.clickhouse != nil {
		defer dataframe.clickhouse.Close()
	}
	publishedRegistry, lifecycleStore := dataframe.registry, storage.lifecycle
	verificationStore := publication.NewPublicationVerifier(publishedRegistry)
	releaseService := &publicationcontract.ReleaseService{Manifests: lifecycleStore, Releases: lifecycleStore, Verifier: verificationStore, Required: serverConfig.Server.RequiredDataframeSelectors}
	releaseRuntime := publicationcontract.NewReleaseRuntime(releaseService, serverConfig.Server.DevActivationConflictOnce, serverConfig.Server.DevRepositoryActivationConflictOnce)

	explorerDeps, err := wireExplorer(ctx, serverConfig, lifecycleClient, storage, scopeResolver)
	if err != nil {
		return err
	}
	dataframeReads := wireDataframeReads(logger, lifecycleClient, storage, dataframe, discoveryCache, scopeResolver)
	generationService, sourceLoader, sourceLoadLimits, err := wireLoad(serverConfig, logger, connOpts, lifecycleClient, storage, dataframe, discoveryCache, scopeResolver, releaseService)
	if err != nil {
		return err
	}
	lifecycleConfig, err := wireLifecycle(serverConfig, logger, storage, dataframe, explorerDeps, scopeResolver, releaseService, releaseRuntime)
	if err != nil {
		return err
	}
	server, err := wireHTTP(lifecycleClient, dataframe.clickhouse, serverConfig.Server.ClickHouse.Enabled, dataframe.degradation, authenticator, authorizer, logger)
	if err != nil {
		return err
	}
	explorerHandlers := wireExplorerHandlers(authorizer, scopeResolver, dataframe, explorerDeps, lifecycleConfig)
	if err := registerRoutes(server, generationService, authorizer, explorerHandlers, publishedRegistry, scopeResolver, sourceLoader, sourceLoadLimits, dataframeReads); err != nil {
		return fmt.Errorf("register HTTP routes: %w", err)
	}
	privateClickHouseArtifacts, artifactStore := dataframe.artifacts, explorerDeps.artifacts
	if privateClickHouseArtifacts != nil {
		reconcileCtx, cancelReconciler := context.WithCancel(ctx)
		reconcilerDone := artifactfs.StartPrivateArtifactReconciler(reconcileCtx, privateClickHouseArtifacts, logger)
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
		artifactReconcilerDone := artifactfs.StartArtifactFSReconciler(artifactReconcileCtx, artifactStore, logger)
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
