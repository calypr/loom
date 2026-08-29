package materializationapi

import (
	"context"
	"sort"
	"strings"

	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

type projectsCacheEntry struct {
	ready    chan struct{}
	projects []string
	err      error
}

type federationCacheEntry struct {
	ready   chan struct{}
	dataset dfmaterialization.FederatedDataset
	access  map[string]dfmaterialization.SourceAccess
	err     error
}

func (s *Service) projects(ctx context.Context, principal *authscope.Principal) ([]string, error) {
	state := aggregateStateFromContext(ctx)
	if state == nil || state.service != s {
		return s.projectsUncached(ctx, principal)
	}
	state.mu.Lock()
	if entry := state.projects; entry != nil {
		state.mu.Unlock()
		if s.logger != nil {
			s.logger.Debug("dataframe project discovery cache wait", "request_id", httpapi.RequestIDFromContext(ctx))
		}
		select {
		case <-entry.ready:
			return append([]string(nil), entry.projects...), entry.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	entry := &projectsCacheEntry{ready: make(chan struct{})}
	state.projects = entry
	state.mu.Unlock()
	if s.logger != nil {
		s.logger.Debug("dataframe project discovery cache miss", "request_id", httpapi.RequestIDFromContext(ctx))
	}

	entry.projects, entry.err = s.projectsUncached(ctx, principal)
	entry.projects = append([]string(nil), entry.projects...)
	close(entry.ready)
	if s.logger != nil {
		s.logger.Debug("dataframe project discovery resolved", "request_id", httpapi.RequestIDFromContext(ctx), "project_count", len(entry.projects), "error", entry.err)
	}
	return append([]string(nil), entry.projects...), entry.err
}

func (s *Service) authorizedFederation(ctx context.Context, principal *authscope.Principal, selector dfmaterialization.DataframeSelector, filters []dfmaterialization.Filter) (dfmaterialization.FederatedDataset, map[string]dfmaterialization.SourceAccess, error) {
	candidates, err := s.projects(ctx, principal)
	if err != nil {
		s.logReadFailure(ctx, "authorized_project_discovery", selector.Output, err)
		return dfmaterialization.FederatedDataset{}, nil, mapReaderError(err)
	}
	candidates = filterProjects(candidates, filters)
	key := federationCacheKey(selector, candidates)
	state := aggregateStateFromContext(ctx)
	if state == nil || state.service != s {
		return s.authorizedFederationUncached(ctx, principal, selector, candidates)
	}
	state.mu.Lock()
	if entry := state.federations[key]; entry != nil {
		state.mu.Unlock()
		if s.logger != nil {
			s.logger.Debug("dataframe federation cache wait", "request_id", httpapi.RequestIDFromContext(ctx), "selector", selector.Key(), "candidate_project_count", len(candidates))
		}
		select {
		case <-entry.ready:
			return entry.dataset, entry.access, entry.err
		case <-ctx.Done():
			return dfmaterialization.FederatedDataset{}, nil, ctx.Err()
		}
	}
	entry := &federationCacheEntry{ready: make(chan struct{})}
	state.federations[key] = entry
	state.mu.Unlock()
	if s.logger != nil {
		s.logger.Debug("dataframe federation cache miss", "request_id", httpapi.RequestIDFromContext(ctx), "selector", selector.Key(), "candidate_project_count", len(candidates))
	}

	entry.dataset, entry.access, entry.err = s.authorizedFederationUncached(ctx, principal, selector, candidates)
	close(entry.ready)
	if s.logger != nil {
		s.logger.Debug("dataframe federation resolved", "request_id", httpapi.RequestIDFromContext(ctx), "selector", selector.Key(), "source_count", len(entry.dataset.Sources), "authorized_project_count", len(entry.access), "error", entry.err)
	}
	return entry.dataset, entry.access, entry.err
}

func federationCacheKey(selector dfmaterialization.DataframeSelector, projects []string) string {
	values := append([]string(nil), projects...)
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	sort.Strings(values)
	return selector.Key() + "\x00" + strings.Join(values, "\x1f")
}
