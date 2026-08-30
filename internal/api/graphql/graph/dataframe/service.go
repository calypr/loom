// Package dataframe adapts one project's published ClickHouse
// dataframe outputs to GraphQL. Project identity is explicit on every read;
// this package never discovers or combines data from other projects.
package dataframe

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

type Service struct {
	reader         *dfmaterialization.Reader
	scopeResolver  *authscope.ScopeResolver
	logger         *slog.Logger
	maxExportRows  int64
	maxExportBytes int64
}

type Config struct {
	Reader         *dfmaterialization.Reader
	ScopeResolver  *authscope.ScopeResolver
	Logger         *slog.Logger
	MaxExportRows  int64
	MaxExportBytes int64
}

func NewService(cfg Config) *Service {
	if cfg.MaxExportRows <= 0 {
		cfg.MaxExportRows = 1_000_000
	}
	if cfg.MaxExportBytes <= 0 {
		cfg.MaxExportBytes = 1 << 30
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Service{reader: cfg.Reader, scopeResolver: cfg.ScopeResolver, logger: cfg.Logger, maxExportRows: cfg.MaxExportRows, maxExportBytes: cfg.MaxExportBytes}
}

func (s *Service) principal(ctx context.Context) (*authscope.Principal, error) {
	principal, ok := authscope.PrincipalFromContext(ctx)
	if !ok || principal == nil {
		return nil, dataframeerrors.NewError(dataframeerrors.CodeUnauthenticated, "")
	}
	return principal, nil
}

type projectAccess struct {
	authResourcePaths []string
	unrestricted      bool
}

func (s *Service) authorizeProject(_ context.Context, principal *authscope.Principal, project string) (projectAccess, error) {
	project = strings.TrimSpace(project)
	if project == "" {
		return projectAccess{}, dataframeerrors.NewError(dataframeerrors.CodeInvalidRequest, "")
	}
	if err := authscope.AuthorizeProject(principal, project, false); err != nil {
		return projectAccess{}, mapReaderError(err)
	}
	return projectAccess{}, nil
}

func (s *Service) authorizeMaterialization(ctx context.Context, principal *authscope.Principal, value dfmaterialization.Materialization) (projectAccess, error) {
	if err := authscope.AuthorizeProject(principal, value.Project, false); err != nil {
		return projectAccess{}, mapReaderError(err)
	}
	if s.scopeResolver == nil {
		if value.ScopeUnrestricted {
			return projectAccess{unrestricted: true}, nil
		}
		if len(principal.AuthResourcePaths) == 0 {
			return projectAccess{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
		}
		return projectAccess{authResourcePaths: append([]string(nil), principal.AuthResourcePaths...)}, nil
	}
	scope, err := s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, value.Project, value.DatasetGeneration, nil)
	if err != nil {
		return projectAccess{}, mapReaderError(err)
	}
	access := projectAccess{authResourcePaths: append([]string(nil), scope.AuthResourcePaths...), unrestricted: scope.Unrestricted()}
	if !access.unrestricted && len(access.authResourcePaths) == 0 {
		return projectAccess{}, dataframeerrors.NewError(dataframeerrors.CodeDatasetNotFound, "")
	}
	return access, nil
}

func (s *Service) currentProjectDataset(ctx context.Context, project string, selector dfmaterialization.DataframeSelector) (dfmaterialization.Materialization, projectAccess, error) {
	if s.reader == nil {
		return dfmaterialization.Materialization{}, projectAccess{}, readerUnavailable()
	}
	principal, err := s.principal(ctx)
	if err != nil {
		return dfmaterialization.Materialization{}, projectAccess{}, err
	}
	return s.currentProjectDatasetForPrincipal(ctx, principal, project, selector)
}

func (s *Service) currentProjectDatasetForPrincipal(ctx context.Context, principal *authscope.Principal, project string, selector dfmaterialization.DataframeSelector) (dfmaterialization.Materialization, projectAccess, error) {
	if state := aggregateStateFromContext(ctx); state != nil && state.service == s {
		return state.projectDataset(ctx, project, selector, func() (dfmaterialization.Materialization, projectAccess, error) {
			return s.resolveCurrentProjectDatasetForPrincipal(ctx, principal, project, selector)
		})
	}
	return s.resolveCurrentProjectDatasetForPrincipal(ctx, principal, project, selector)
}

func (s *Service) resolveCurrentProjectDatasetForPrincipal(ctx context.Context, principal *authscope.Principal, project string, selector dfmaterialization.DataframeSelector) (dfmaterialization.Materialization, projectAccess, error) {
	project = strings.TrimSpace(project)
	if _, err := s.authorizeProject(ctx, principal, project); err != nil {
		return dfmaterialization.Materialization{}, projectAccess{}, err
	}
	value, err := s.reader.CurrentProjectDataset(ctx, project, selector)
	if err != nil {
		return dfmaterialization.Materialization{}, projectAccess{}, mapReaderError(err)
	}
	access, err := s.authorizeMaterialization(ctx, principal, value)
	if err != nil {
		return dfmaterialization.Materialization{}, projectAccess{}, err
	}
	return value, access, nil
}

func (s *Service) logReadFailure(ctx context.Context, phase, resourceType string, err error, attrs ...any) {
	if s.logger == nil {
		return
	}
	fields := []any{"request_id", httpapi.RequestIDFromContext(ctx), "phase", phase, "resource_type", resourceType, "error_chain", errorChain(err)}
	fields = append(fields, attrs...)
	s.logger.Error("dataframe read failed", fields...)
}

func errorChain(err error) string {
	if err == nil {
		return ""
	}
	parts := make([]string, 0, 3)
	for current := err; current != nil; current = errors.Unwrap(current) {
		parts = append(parts, current.Error())
	}
	return strings.Join(parts, " <- ")
}

func mapReaderError(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := dataframeerrors.AsUserError(err); ok {
		return err
	}
	if errors.Is(err, authscope.ErrUnauthenticated) {
		return dataframeerrors.NewError(dataframeerrors.CodeUnauthenticated, "")
	}
	if errors.Is(err, authscope.ErrForbidden) {
		return dataframeerrors.NewError(dataframeerrors.CodeForbidden, "")
	}
	if errors.Is(err, authscope.ErrAuthorizationBackendUnavailable) {
		return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
	}
	return dataframeerrors.Wrap(err, dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}

func readerUnavailable() error {
	return dataframeerrors.NewError(dataframeerrors.CodeBackendUnavailable, "", dataframeerrors.WithRetryable(true))
}
