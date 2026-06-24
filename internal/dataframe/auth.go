package dataframe

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
)

func (s *Service) resolveAuthResourcePaths(ctx context.Context, principal *authscope.Principal, project string, requested []string) ([]string, error) {
	if s.scopeResolver != nil {
		return s.scopeResolver.ResolveReadAuthResourcePaths(ctx, principal, project, requested)
	}
	if len(requested) == 0 {
		if principal == nil || len(principal.AuthResourcePaths) == 0 {
			return nil, nil
		}
		return append([]string(nil), principal.AuthResourcePaths...), nil
	}
	if principal == nil || len(principal.AuthResourcePaths) == 0 {
		return append([]string(nil), requested...), nil
	}
	for _, path := range requested {
		found := false
		for _, candidate := range principal.AuthResourcePaths {
			if candidate == path {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("authResourcePath %q is outside caller scope", path)
		}
	}
	return append([]string(nil), requested...), nil
}

func authorizeProject(principal *authscope.Principal, project string, ignorePrincipalProjects bool) error {
	if ignorePrincipalProjects {
		return nil
	}
	if principal == nil || len(principal.Projects) == 0 {
		return nil
	}
	for _, candidate := range principal.Projects {
		if candidate == project {
			return nil
		}
	}
	return fmt.Errorf("principal is not authorized for project %q", project)
}
