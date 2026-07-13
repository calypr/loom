package runtime

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/authscope"
)

// resolveReadScope returns both the effective paths and whether an empty path
// set may bypass scope checks. Callers must retain the mode through catalog
// discovery and compilation; returning paths alone is not safe for a
// restricted caller whose permitted dataset has no matching paths.
// resolveReadScopeForGeneration keeps authorization-path discovery aligned
// with the exact generation selected for catalog and dataframe reads. The
// empty generation retains the legacy null-generation behavior.
func (s *Service) resolveReadScopeForGeneration(ctx context.Context, principal *authscope.Principal, project, datasetGeneration string, requested []string) (authscope.ReadScope, error) {
	if s.scopeResolver != nil {
		return s.scopeResolver.ResolveReadScopeForGeneration(ctx, principal, project, datasetGeneration, requested)
	}
	if len(requested) == 0 {
		if principal == nil || len(principal.AuthResourcePaths) == 0 {
			return authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}, nil
		}
		return authscope.ReadScope{
			AuthResourcePaths: append([]string(nil), principal.AuthResourcePaths...),
			Mode:              authscope.ReadScopeRestricted,
		}, nil
	}
	if principal == nil || len(principal.AuthResourcePaths) == 0 {
		return authscope.ReadScope{
			AuthResourcePaths: append([]string(nil), requested...),
			Mode:              authscope.ReadScopeRestricted,
		}, nil
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
			return authscope.ReadScope{}, fmt.Errorf("authResourcePath %q is outside caller scope", path)
		}
	}
	return authscope.ReadScope{
		AuthResourcePaths: append([]string(nil), requested...),
		Mode:              authscope.ReadScopeRestricted,
	}, nil
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
