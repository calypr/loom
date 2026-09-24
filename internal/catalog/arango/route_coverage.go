package arango

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

var coveragePathSegment = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(\[\])?$`)
var coverageIndexName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

var semanticCoverageIndexFields = []string{"project", "dataset_generation", "build_id", "source_kind", "binding_id", "concept_id", "auth_resource_path"}
var fieldSourceMembershipIndexFields = []string{"project", "dataset_generation", "resource_type", "scalar_paths[*]"}

// MeasureRouteCoverage counts distinct default-cohort roots with an actual
// value at one exact source. It does not treat project-wide edge counts as row
// evidence. The caller handles custom row memberships separately.
func (s *Store) MeasureRouteCoverage(ctx context.Context, opts catalog.RouteCoverageOptions) (catalog.RouteCoverage, error) {
	indexHint, err := s.coverageIndexHint(ctx, opts.Source.Kind)
	if err != nil {
		return catalog.RouteCoverage{}, err
	}
	query, vars, err := routeCoverageQueryWithHint(opts, indexHint)
	if err != nil {
		return catalog.RouteCoverage{}, err
	}
	result := catalog.RouteCoverage{}
	rows := 0
	err = s.client.QueryRows(ctx, query, 1, vars, func(row map[string]any) error {
		rows++
		count, decodeErr := decodeInt64(row["rows_with_value"])
		if decodeErr != nil {
			return fmt.Errorf("decode covered table rows: %w", decodeErr)
		}
		result.RowsWithValue = count
		return nil
	})
	if err != nil {
		return catalog.RouteCoverage{}, err
	}
	if rows != 1 {
		return catalog.RouteCoverage{}, fmt.Errorf("route coverage query returned %d summaries", rows)
	}
	return result, nil
}

// HasRouteValue reports whether at least one authorized row in the default
// cohort reaches a source with a value. Unlike MeasureRouteCoverage, it stops
// at the first match and does not count or materialize all covered roots.
func (s *Store) HasRouteValue(ctx context.Context, opts catalog.RouteCoverageOptions) (bool, error) {
	if opts.Source.Kind == catalog.RouteCoverageField && len(opts.Route) > 0 {
		if _, _, err := routeCoverageFieldOptions(opts); err != nil {
			return false, err
		}
		complete, err := s.fieldSourceMembershipBuildComplete(ctx, opts.Project, opts.DatasetGeneration)
		if err != nil {
			return false, err
		}
		if complete {
			if indexName, found := s.fieldSourceMembershipIndexName(ctx); found {
				query, vars, err := routeCoverageMembershipFieldExistenceQuery(opts, indexName)
				if err != nil {
					return false, err
				}
				return s.hasRouteValueQuery(ctx, query, vars)
			}
		}
	}
	indexHint, err := s.coverageIndexHint(ctx, opts.Source.Kind)
	if err != nil {
		return false, err
	}
	query, vars, err := routeCoverageExistenceQueryWithHint(opts, indexHint)
	if err != nil {
		return false, err
	}
	return s.hasRouteValueQuery(ctx, query, vars)
}

func (s *Store) hasRouteValueQuery(ctx context.Context, query string, vars map[string]any) (bool, error) {
	found := false
	err := s.client.QueryRows(ctx, query, 1, vars, func(map[string]any) error {
		found = true
		return nil
	})
	return found, err
}

const fieldSourceMembershipBuildStatusAQL = `
FOR d IN fhir_field_source_membership_builds
  FILTER d._key == @key AND d.project == @project AND d.dataset_generation == @dataset_generation
  LIMIT 1
  RETURN {schema_version: d.schema_version, state: d.state}`

func (s *Store) fieldSourceMembershipBuildComplete(ctx context.Context, project, datasetGeneration string) (bool, error) {
	exists, err := s.client.CollectionExists(ctx, catalog.FieldSourceMembershipBuildCollection)
	if err != nil || !exists {
		return false, err
	}
	build := catalog.NewFieldSourceMembershipBuild(project, datasetGeneration)
	complete := false
	err = s.client.QueryRows(ctx, fieldSourceMembershipBuildStatusAQL, 1, map[string]any{
		"key":                build.Key,
		"project":            project,
		"dataset_generation": generation(datasetGeneration),
	}, func(row map[string]any) error {
		version, versionErr := decodeInt64(row["schema_version"])
		state, _ := row["state"].(string)
		complete = versionErr == nil && version == int64(catalog.FieldSourceMembershipSchemaVersion) && state == string(catalog.FieldSourceMembershipComplete)
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("read field-source membership build marker: %w", err)
	}
	return complete, nil
}

func (s *Store) fieldSourceMembershipIndexName(ctx context.Context) (string, bool) {
	resolver, ok := s.client.(interface {
		PersistentIndexName(context.Context, string, []string) (string, error)
	})
	if !ok {
		return "", false
	}
	name, err := resolver.PersistentIndexName(ctx, catalog.FieldSourceMembershipCollection, fieldSourceMembershipIndexFields)
	return name, err == nil && name != ""
}

func (s *Store) coverageIndexHint(ctx context.Context, sourceKind catalog.RouteCoverageSourceKind) (string, error) {
	if sourceKind != catalog.RouteCoverageSemantic {
		return "", nil
	}
	locator, ok := s.client.(interface {
		PersistentIndexName(context.Context, string, []string) (string, error)
	})
	if !ok {
		return "", nil
	}
	indexHint, err := locator.PersistentIndexName(ctx, catalog.SemanticInventoryCollection, semanticCoverageIndexFields)
	if err != nil {
		return "", fmt.Errorf("resolve exact semantic coverage index: %w", err)
	}
	return indexHint, nil
}

func routeCoverageQuery(opts catalog.RouteCoverageOptions) (string, map[string]any, error) {
	return routeCoverageQueryWithHint(opts, "")
}

func routeCoverageQueryWithHint(opts catalog.RouteCoverageOptions, indexHint string) (string, map[string]any, error) {
	lines, vars, current, err := routeCoverageBaseAndPath(opts, indexHint, true)
	if err != nil {
		return "", nil, err
	}
	lines = append(lines, "  COLLECT root_id = "+current+"._id", "  COLLECT WITH COUNT INTO rows_with_value", "  RETURN { rows_with_value }")
	return strings.Join(lines, "\n"), vars, nil
}

func routeCoverageExistenceQuery(opts catalog.RouteCoverageOptions, indexHint string) (string, map[string]any, error) {
	return routeCoverageExistenceQueryWithHint(opts, indexHint)
}

func routeCoverageExistenceQueryWithHint(opts catalog.RouteCoverageOptions, indexHint string) (string, map[string]any, error) {
	if opts.Source.Kind == catalog.RouteCoverageField && len(opts.Route) > 0 {
		return routeCoverageFieldExistenceQuery(opts)
	}
	lines, vars, current, err := routeCoverageBaseAndPath(opts, indexHint, false)
	if err != nil {
		return "", nil, err
	}
	lines = append(lines, "  FILTER "+current+" != null", "  LIMIT 1", "  RETURN { matched: true }")
	return strings.Join(lines, "\n"), vars, nil
}

func routeCoverageFieldExistenceQuery(opts catalog.RouteCoverageOptions) (string, map[string]any, error) {
	presence, vars, err := routeCoverageFieldOptions(opts)
	if err != nil {
		return "", nil, err
	}

	last := len(opts.Route) - 1
	lastStep := opts.Route[last]
	childEndpoint, parentEndpoint, parentTypeField, childTypeField, err := routeCoveragePhysicalEndpoints(lastStep)
	if err != nil {
		return "", nil, err
	}
	anchor := fmt.Sprintf("edge_%d", last)
	lastParent := fmt.Sprintf("vertex_%d", last)
	lines := []string{
		"FOR " + anchor + " IN fhir_edge",
		"  FILTER " + anchor + ".project == @project AND " + anchor + ".dataset_generation == @generation",
		"  FILTER " + anchor + ".label == @label_" + fmt.Sprint(last),
		"  FILTER " + anchor + "." + parentTypeField + " == @parent_type_" + fmt.Sprint(last),
		"  FILTER " + anchor + "." + childTypeField + " == @child_type_" + fmt.Sprint(last),
		"  FILTER @unrestricted == true OR " + anchor + ".auth_resource_path IN @auth_paths",
		"  LET source = DOCUMENT(" + anchor + "." + childEndpoint + ")",
		"  FILTER source != null AND source.project == @project AND source.dataset_generation == @generation",
		"  FILTER source.resourceType == @source_type",
		"  FILTER @unrestricted == true OR source.auth_resource_path IN @auth_paths",
		"  FILTER " + presence,
		"  LET " + lastParent + " = DOCUMENT(" + anchor + "." + parentEndpoint + ")",
		"  FILTER " + lastParent + " != null AND " + lastParent + ".project == @project AND " + lastParent + ".dataset_generation == @generation",
		"  FILTER " + lastParent + ".resourceType == @parent_type_" + fmt.Sprint(last),
		"  FILTER @unrestricted == true OR " + lastParent + ".auth_resource_path IN @auth_paths",
	}
	current := lastParent
	for index := last - 1; index >= 0; index-- {
		step := opts.Route[index]
		childEndpoint, parentEndpoint, parentTypeField, childTypeField, err = routeCoveragePhysicalEndpoints(step)
		if err != nil {
			return "", nil, err
		}
		edge := fmt.Sprintf("edge_%d", index)
		parent := fmt.Sprintf("vertex_%d", index)
		lines = append(lines,
			"  FOR "+edge+" IN fhir_edge",
			"    FILTER "+edge+"."+childEndpoint+" == "+current+"._id AND "+edge+".label == @label_"+fmt.Sprint(index),
			"    FILTER "+edge+".project == @project AND "+edge+".dataset_generation == @generation",
			"    FILTER "+edge+"."+parentTypeField+" == @parent_type_"+fmt.Sprint(index)+" AND "+edge+"."+childTypeField+" == @child_type_"+fmt.Sprint(index),
			"    FILTER @unrestricted == true OR "+edge+".auth_resource_path IN @auth_paths",
			"    FILTER "+current+".resourceType == @child_type_"+fmt.Sprint(index),
			"    LET "+parent+" = DOCUMENT("+edge+"."+parentEndpoint+")",
			"    FILTER "+parent+" != null AND "+parent+".project == @project AND "+parent+".dataset_generation == @generation",
			"    FILTER "+parent+".resourceType == @parent_type_"+fmt.Sprint(index),
			"    FILTER @unrestricted == true OR "+parent+".auth_resource_path IN @auth_paths",
		)
		current = parent
	}
	lines = append(lines,
		"  FILTER "+current+".resourceType == @root_type",
		"  LIMIT 1",
		"  RETURN { matched: true }",
	)
	return strings.Join(lines, "\n"), vars, nil
}

func routeCoverageMembershipFieldExistenceQuery(opts catalog.RouteCoverageOptions, indexHint string) (string, map[string]any, error) {
	presence, vars, err := routeCoverageFieldOptions(opts)
	if err != nil {
		return "", nil, err
	}
	indexClause := ""
	if indexHint != "" {
		if !coverageIndexName.MatchString(indexHint) {
			return "", nil, fmt.Errorf("unsafe field-source membership index name")
		}
		indexClause = fmt.Sprintf(` OPTIONS { indexHint: %q, forceIndexHint: true }`, indexHint)
	}
	lines := []string{
		"FOR membership IN fhir_field_source_membership" + indexClause,
		"  FILTER membership.project == @project AND membership.dataset_generation == @generation",
		"  FILTER membership.resource_type == @source_type",
		"  FILTER @field_path IN membership.scalar_paths",
		"  FILTER @unrestricted == true OR membership.auth_resource_path IN @auth_paths",
		"  LET source = DOCUMENT(membership.vertex_id)",
		"  FILTER source != null AND source._id == membership.vertex_id",
		"  FILTER source.project == @project AND source.dataset_generation == @generation",
		"  FILTER source.resourceType == @source_type",
		"  FILTER @unrestricted == true OR source.auth_resource_path IN @auth_paths",
		"  FILTER " + presence,
	}
	current := "source"
	for index := len(opts.Route) - 1; index >= 0; index-- {
		step := opts.Route[index]
		childEndpoint, parentEndpoint, parentTypeField, childTypeField, endpointErr := routeCoveragePhysicalEndpoints(step)
		if endpointErr != nil {
			return "", nil, endpointErr
		}
		edge := fmt.Sprintf("edge_%d", index)
		parent := fmt.Sprintf("vertex_%d", index)
		lines = append(lines,
			"  FOR "+edge+" IN fhir_edge",
			"    FILTER "+current+".resourceType == @child_type_"+fmt.Sprint(index),
			"    FILTER "+edge+"."+childEndpoint+" == "+current+"._id AND "+edge+".label == @label_"+fmt.Sprint(index),
			"    FILTER "+edge+".project == @project AND "+edge+".dataset_generation == @generation",
			"    FILTER "+edge+"."+parentTypeField+" == @parent_type_"+fmt.Sprint(index)+" AND "+edge+"."+childTypeField+" == @child_type_"+fmt.Sprint(index),
			"    FILTER @unrestricted == true OR "+edge+".auth_resource_path IN @auth_paths",
			"    LET "+parent+" = DOCUMENT("+edge+"."+parentEndpoint+")",
			"    FILTER "+parent+" != null AND "+parent+".project == @project AND "+parent+".dataset_generation == @generation",
			"    FILTER "+parent+".resourceType == @parent_type_"+fmt.Sprint(index),
			"    FILTER @unrestricted == true OR "+parent+".auth_resource_path IN @auth_paths",
		)
		current = parent
	}
	lines = append(lines,
		"  FILTER "+current+".resourceType == @root_type",
		"  LIMIT 1",
		"  RETURN { matched: true }",
	)
	return strings.Join(lines, "\n"), vars, nil
}

func routeCoverageFieldOptions(opts catalog.RouteCoverageOptions) (string, map[string]any, error) {
	if opts.Project == "" || opts.DatasetGeneration == "" || opts.RootResourceType == "" || opts.SourceResourceType == "" {
		return "", nil, fmt.Errorf("route coverage requires project, generation, root, and source")
	}
	if _, ok := fhirschema.ConcreteResourceType(opts.RootResourceType); !ok {
		return "", nil, fmt.Errorf("unsupported root resource type %q", opts.RootResourceType)
	}
	if _, ok := fhirschema.ConcreteResourceType(opts.SourceResourceType); !ok {
		return "", nil, fmt.Errorf("unsupported source resource type %q", opts.SourceResourceType)
	}
	if err := opts.Source.Validate(); err != nil {
		return "", nil, err
	}
	if err := validateRouteCoveragePath(opts); err != nil {
		return "", nil, err
	}
	presence, err := routeCoverageFieldPresence(opts.Source.FieldPath)
	if err != nil {
		return "", nil, err
	}
	vars := map[string]any{
		"project": opts.Project, "generation": generation(opts.DatasetGeneration),
		"auth_paths":   append([]string(nil), opts.AuthResourcePaths...),
		"unrestricted": opts.AuthResourcePathsUnrestricted,
		"root_type":    opts.RootResourceType, "source_type": opts.SourceResourceType,
		"field_path": opts.Source.FieldPath,
	}
	for index, step := range opts.Route {
		vars[fmt.Sprintf("label_%d", index)] = step.Relationship
		vars[fmt.Sprintf("parent_type_%d", index)] = step.FromResourceType
		vars[fmt.Sprintf("child_type_%d", index)] = step.ToResourceType
	}
	return presence, vars, nil
}

func routeCoveragePhysicalEndpoints(step catalog.RouteCoverageStep) (childEndpoint, parentEndpoint, parentTypeField, childTypeField string, err error) {
	switch step.StorageDirection {
	case "INBOUND":
		return "_from", "_to", "to_type", "from_type", nil
	case "OUTBOUND":
		return "_to", "_from", "from_type", "to_type", nil
	default:
		return "", "", "", "", fmt.Errorf("unsupported route storage direction %q", step.StorageDirection)
	}
}

func validateRouteCoveragePath(opts catalog.RouteCoverageOptions) error {
	if len(opts.Route) == 0 {
		if opts.RootResourceType != opts.SourceResourceType {
			return fmt.Errorf("route does not begin at the requested row root")
		}
		return nil
	}
	if opts.Route[0].FromResourceType != opts.RootResourceType {
		return fmt.Errorf("route does not begin at the requested row root")
	}
	for index, step := range opts.Route {
		if _, ok := fhirschema.ConcreteResourceType(step.FromResourceType); !ok {
			return fmt.Errorf("unsupported route resource type %q", step.FromResourceType)
		}
		if _, ok := fhirschema.ConcreteResourceType(step.ToResourceType); !ok {
			return fmt.Errorf("unsupported route resource type %q", step.ToResourceType)
		}
		if step.Relationship == "" {
			return fmt.Errorf("route relationship is required")
		}
		if step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND" {
			return fmt.Errorf("unsupported route storage direction %q", step.StorageDirection)
		}
		if index < len(opts.Route)-1 && step.ToResourceType != opts.Route[index+1].FromResourceType {
			return fmt.Errorf("route does not end at the source")
		}
	}
	if opts.Route[len(opts.Route)-1].ToResourceType != opts.SourceResourceType {
		return fmt.Errorf("route does not end at the source")
	}
	return nil
}

func routeCoverageBaseAndPath(opts catalog.RouteCoverageOptions, indexHint string, deduplicateSources bool) ([]string, map[string]any, string, error) {
	if opts.Project == "" || opts.DatasetGeneration == "" || opts.RootResourceType == "" || opts.SourceResourceType == "" {
		return nil, nil, "", fmt.Errorf("route coverage requires project, generation, root, and source")
	}
	if _, ok := fhirschema.ConcreteResourceType(opts.RootResourceType); !ok {
		return nil, nil, "", fmt.Errorf("unsupported root resource type %q", opts.RootResourceType)
	}
	if _, ok := fhirschema.ConcreteResourceType(opts.SourceResourceType); !ok {
		return nil, nil, "", fmt.Errorf("unsupported source resource type %q", opts.SourceResourceType)
	}
	if err := opts.Source.Validate(); err != nil {
		return nil, nil, "", err
	}
	if err := validateRouteCoveragePath(opts); err != nil {
		return nil, nil, "", err
	}
	vars := map[string]any{
		"project": opts.Project, "generation": generation(opts.DatasetGeneration),
		"auth_paths":   append([]string(nil), opts.AuthResourcePaths...),
		"unrestricted": opts.AuthResourcePathsUnrestricted,
		"source_type":  opts.SourceResourceType,
	}
	lines := make([]string, 0, 20+len(opts.Route)*9)
	switch opts.Source.Kind {
	case catalog.RouteCoverageSemantic:
		if opts.BuildID == "" {
			return nil, nil, "", fmt.Errorf("semantic route coverage requires an inventory build")
		}
		vars["build_id"], vars["concept_id"], vars["binding_id"] = opts.BuildID, opts.Source.ConceptID, opts.Source.BindingID
		inventorySource := "FOR contribution IN fhir_semantic_inventory"
		if indexHint != "" {
			if !coverageIndexName.MatchString(indexHint) {
				return nil, nil, "", fmt.Errorf("unsafe semantic coverage index name")
			}
			inventorySource += fmt.Sprintf(` OPTIONS { indexHint: %q, forceIndexHint: true }`, indexHint)
		}
		lines = append(lines,
			inventorySource,
			"  FILTER contribution.project == @project AND contribution.dataset_generation == @generation",
			"  FILTER contribution.build_id == @build_id AND contribution.source_kind == 'retained_vertex'",
			"  FILTER contribution.concept_id == @concept_id AND contribution.binding_id == @binding_id",
			"  FILTER @unrestricted == true OR contribution.auth_resource_path IN @auth_paths",
		)
		if deduplicateSources {
			lines = append(lines, "  COLLECT source_id = contribution.source_id", "  LET source = DOCUMENT(SUBSTRING(source_id, 9))")
		} else {
			lines = append(lines, "  LET source = DOCUMENT(SUBSTRING(contribution.source_id, 9))")
		}
	case catalog.RouteCoverageField:
		vars["@source_collection"] = opts.SourceResourceType
		lines = append(lines, "FOR source IN @@source_collection")
	}
	lines = append(lines,
		"  FILTER source != null AND source.project == @project AND source.dataset_generation == @generation",
		"  FILTER source.resourceType == @source_type",
		"  FILTER @unrestricted == true OR source.auth_resource_path IN @auth_paths",
	)
	if opts.Source.Kind == catalog.RouteCoverageField {
		presence, err := routeCoverageFieldPresence(opts.Source.FieldPath)
		if err != nil {
			return nil, nil, "", err
		}
		lines = append(lines, "  FILTER "+presence)
	}
	current := "source"
	for index := len(opts.Route) - 1; index >= 0; index-- {
		step := opts.Route[index]
		edge := fmt.Sprintf("edge_%d", index)
		parent := fmt.Sprintf("vertex_%d", index)
		endpoint, targetEndpoint := "", ""
		switch step.StorageDirection {
		case "INBOUND":
			endpoint, targetEndpoint = "_from", "_to"
		case "OUTBOUND":
			endpoint, targetEndpoint = "_to", "_from"
		default:
			return nil, nil, "", fmt.Errorf("unsupported route storage direction %q", step.StorageDirection)
		}
		vars[fmt.Sprintf("label_%d", index)] = step.Relationship
		vars[fmt.Sprintf("parent_type_%d", index)] = step.FromResourceType
		lines = append(lines,
			"  FOR "+edge+" IN fhir_edge",
			"    FILTER "+edge+"."+endpoint+" == "+current+"._id AND "+edge+".label == @label_"+fmt.Sprint(index),
			"    FILTER "+edge+".project == @project AND "+edge+".dataset_generation == @generation",
			"    FILTER @unrestricted == true OR "+edge+".auth_resource_path IN @auth_paths",
			"    LET "+parent+" = DOCUMENT("+edge+"."+targetEndpoint+")",
			"    FILTER "+parent+" != null AND "+parent+".project == @project AND "+parent+".dataset_generation == @generation",
			"    FILTER "+parent+".resourceType == @parent_type_"+fmt.Sprint(index),
			"    FILTER @unrestricted == true OR "+parent+".auth_resource_path IN @auth_paths",
		)
		current = parent
	}
	return lines, vars, current, nil
}

func routeCoverageFieldPresence(path string) (string, error) {
	segments := strings.Split(path, ".")
	if len(segments) == 0 || len(segments) > 16 {
		return "", fmt.Errorf("invalid coverage field path")
	}
	parts := []string{"LENGTH(FOR value_0 IN [source.payload]"}
	current := "value_0"
	for index, segment := range segments {
		if !coveragePathSegment.MatchString(segment) {
			return "", fmt.Errorf("unsupported coverage field path %q", path)
		}
		repeated := strings.HasSuffix(segment, "[]")
		member := strings.TrimSuffix(segment, "[]")
		next := fmt.Sprintf("value_%d", index+1)
		access := current + "." + member
		if repeated {
			parts = append(parts, "FOR "+next+" IN (IS_ARRAY("+access+") ? "+access+" : [])")
		} else {
			parts = append(parts, "LET "+next+" = "+access)
		}
		current = next
	}
	parts = append(parts, "FILTER "+current+" != null LIMIT 1 RETURN 1) > 0")
	return strings.Join(parts, " "), nil
}
