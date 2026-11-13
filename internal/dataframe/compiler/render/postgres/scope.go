package postgres

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

const (
	projectBindKey        = "project"
	datasetGenerationBind = "dataset_generation"
	authPathsBindKey      = "auth_resource_paths"
	authUnrestrictedBind  = "auth_resource_paths_unrestricted"
	scopeAllowedBind      = "scope_allowed"
)

type planScope struct {
	projectIndex    int
	generationIndex int
	authPathsIndex  int
	denied          bool
}

func (r *planRenderer) loadScope(plan ir.PhysicalPlan) error {
	project, ok := plan.BindVars[projectBindKey].(string)
	if !ok || strings.TrimSpace(project) == "" {
		return fmt.Errorf("%w: %q bind must be a non-empty string", ErrInvalidPostgresPlanScope, projectBindKey)
	}
	generation, ok := plan.BindVars[datasetGenerationBind].(string)
	if !ok {
		return fmt.Errorf("%w: %q bind must be a string (the empty string is the legacy namespace)",
			ErrInvalidPostgresPlanScope, datasetGenerationBind)
	}
	unrestricted, ok := plan.BindVars[authUnrestrictedBind].(bool)
	if !ok {
		return fmt.Errorf("%w: %q bind must be set explicitly; there is no implicit auth scope",
			ErrInvalidPostgresPlanScope, authUnrestrictedBind)
	}
	paths, ok := plan.BindVars[authPathsBindKey].([]string)
	if !ok {
		return fmt.Errorf("%w: %q bind must be a []string", ErrInvalidPostgresPlanScope, authPathsBindKey)
	}
	if unrestricted && len(paths) != 0 {
		return fmt.Errorf("%w: unrestricted mode must not carry auth resource paths (%d given)",
			ErrInvalidPostgresPlanScope, len(paths))
	}
	allowed, ok := plan.BindVars[scopeAllowedBind].(bool)
	if !ok {
		return fmt.Errorf("%w: %q bind must be set explicitly to a boolean; false is the canonical deny state (zero rows) and absence or a mistyped value is a plan defect",
			ErrInvalidPostgresPlanScope, scopeAllowedBind)
	}

	r.scope = planScope{
		projectIndex:    r.textParameter(project, "text:"+project),
		generationIndex: r.textParameter(generation, "text:"+generation),
		denied:          !allowed,
	}
	if !unrestricted {
		copied := append([]string(nil), paths...)
		raw, err := json.Marshal(copied)
		if err != nil {
			return fmt.Errorf("%w: auth resource paths are not JSON-encodable: %v", ErrInvalidPostgresPlanScope, err)
		}
		if string(raw) == "null" {
			raw = json.RawMessage("[]")
		}
		r.scope.authPathsIndex = r.jsonbParameter(raw)
	}
	return nil
}

func (r *planRenderer) scopedResourcePredicate(alias string, typeIndex int) string {
	return r.scopedResourcePredicateDenyConjunct(alias, typeIndex, " AND FALSE")
}

func (r *planRenderer) scopedResourcePredicateDenyConjunct(alias string, typeIndex int, denyConjunct string) string {
	var builder strings.Builder
	builder.WriteString(alias + "." + r.storage.Resource.MustColumn("project").SQLText() + " = $" + strconv.Itoa(r.scope.projectIndex))
	builder.WriteString(" AND " + alias + "." + r.storage.Resource.MustColumn("dataset_generation").SQLText() + " = $" + strconv.Itoa(r.scope.generationIndex))
	if r.scope.authPathsIndex != 0 {
		builder.WriteString(" AND " + alias + "." + r.storage.Resource.MustColumn("auth_resource_path").SQLText())
		builder.WriteString(" = ANY (SELECT jsonb_array_elements_text($" + strconv.Itoa(r.scope.authPathsIndex) + "::jsonb))")
	}
	if typeIndex != 0 {
		builder.WriteString(" AND " + alias + "." + r.storage.Resource.MustColumn("resource_type").SQLText() + " = $" + strconv.Itoa(typeIndex))
	}
	if r.scope.denied {
		builder.WriteString(denyConjunct)
	}
	return builder.String()
}

func (r *planRenderer) scopedEdgePredicate(alias string) string {
	columns := r.storage.Edge
	predicate := alias + "." + columns.MustColumn("project").SQLText() + " = $" + strconv.Itoa(r.scope.projectIndex)
	predicate += " AND " + alias + "." + columns.MustColumn("dataset_generation").SQLText() + " = $" + strconv.Itoa(r.scope.generationIndex)
	if r.scope.authPathsIndex != 0 {
		predicate += " AND " + alias + "." + columns.MustColumn("auth_resource_path").SQLText() +
			" = ANY (SELECT jsonb_array_elements_text($" + strconv.Itoa(r.scope.authPathsIndex) + "::jsonb))"
	}
	if r.scope.denied {
		predicate += " AND FALSE"
	}
	return predicate
}
