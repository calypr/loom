package dataframe

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"arangodb-proto/internal/proto"
	"arangodb-proto/internal/writeapi"
)

const (
	PivotKindCodeableConceptDisplayValue = "CODEABLE_CONCEPT_DISPLAY_VALUE"
	defaultRowLimit                      = 25
)

type Builder struct {
	Project              string
	AuthResourcePaths    []string
	RootResourceType     string
	PlanHint             *PlanHint
	Fields               []FieldSelect
	Pivots               []PivotSelect
	Aggregates           []AggregateSelect
	Slices               []RepresentativeSlice
	Traversals           []TraversalStep
	Sets                 []NamedSet
	DerivedFields        []DerivedField
	RepresentativeSlices []RepresentativeSlice
}

type TraversalStep struct {
	Label                string
	ToResourceType       string
	Alias                string
	Fields               []FieldSelect
	Pivots               []PivotSelect
	Aggregates           []AggregateSelect
	Slices               []RepresentativeSlice
	Traversals           []TraversalStep
	Sets                 []NamedSet
	DerivedFields        []DerivedField
	RepresentativeSlices []RepresentativeSlice
}

type FieldSelect struct {
	Name            string
	FieldRef        string
	Select          string
	FallbackFieldRefs []string
	FallbackSelects []string
	ValueMode       string
}

type PivotSelect struct {
	Name      string
	FieldRef  string
	Select    string
	PivotKind string
	Columns   []string
	ValueFieldRef string
	ValuePath string
}

type AggregateSelect struct {
	Name            string
	Operation       string
	FieldRef        string
	Select          string
	PredicateFieldRef string
	PredicatePath   string
	PredicateEquals string
	ValueMode       string
}

type RunRequest struct {
	Builder      Builder
	Limit        int
}

type Result struct {
	Columns  []string
	Rows     []map[string]any
	RowCount int
}

type ServiceConfig struct {
	ConnectionOptions  proto.ConnectionOptions
	DiscoverReferences func(context.Context, proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error)
	DiscoverFields     func(context.Context, proto.PopulatedFieldOptions) ([]proto.PopulatedField, error)
	ExecuteRows        func(context.Context, proto.ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error
	ScopeResolver      *writeapi.ScopeResolver
}

type Service struct {
	connOpts           proto.ConnectionOptions
	discoverReferences func(context.Context, proto.PopulatedReferenceOptions) ([]proto.PopulatedReference, error)
	discoverFields     func(context.Context, proto.PopulatedFieldOptions) ([]proto.PopulatedField, error)
	executeRows        func(context.Context, proto.ExecuteQueryOptions, string, map[string]any, func(map[string]any) error) error
	scopeResolver      *writeapi.ScopeResolver
}

func NewService(cfg ServiceConfig) *Service {
	svc := &Service{
		connOpts:      cfg.ConnectionOptions,
		scopeResolver: cfg.ScopeResolver,
	}
	if cfg.DiscoverReferences != nil {
		svc.discoverReferences = cfg.DiscoverReferences
	} else {
		svc.discoverReferences = proto.DiscoverPopulatedReferences
	}
	if cfg.DiscoverFields != nil {
		svc.discoverFields = cfg.DiscoverFields
	} else {
		svc.discoverFields = proto.DiscoverPopulatedFields
	}
	if cfg.ExecuteRows != nil {
		svc.executeRows = cfg.ExecuteRows
	} else {
		svc.executeRows = proto.ExecuteQueryRows
	}
	return svc
}

func (s *Service) Run(ctx context.Context, req RunRequest) (*Result, error) {
	if protoBackend := strings.ToLower(strings.TrimSpace(s.connOpts.Backend)); protoBackend != "" && protoBackend != "arango" {
		return nil, fmt.Errorf("runFhirDataframe currently supports only backend \"arango\"")
	}

	spec, err := s.prepareSpec(ctx, req.Builder)
	if err != nil {
		return nil, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = defaultRowLimit
	}
	compiled, err := Compile(spec, limit)
	if err != nil {
		return nil, err
	}
	return s.runQuery(ctx, compiled)
}

func (s *Service) prepareSpec(ctx context.Context, builder Builder) (Builder, error) {
	if builder.Project == "" {
		return Builder{}, fmt.Errorf("project is required")
	}
	if builder.RootResourceType == "" {
		return Builder{}, fmt.Errorf("rootResourceType is required")
	}
	principal, _ := writeapi.PrincipalFromContext(ctx)
	resolvedPaths, err := s.resolveAuthResourcePaths(ctx, principal, builder.Project, builder.AuthResourcePaths)
	if err != nil {
		return Builder{}, err
	}
	if err := authorizeProject(principal, builder.Project, s.scopeResolver != nil); err != nil {
		return Builder{}, err
	}
	builder.AuthResourcePaths = resolvedPaths
	if usesAdvancedBuilder(builder) {
		if err := validateAdvancedBuilder(builder); err != nil {
			return Builder{}, err
		}
		return builder, nil
	}
	if err := s.validateBuilder(ctx, builder); err != nil {
		return Builder{}, err
	}
	expanded, err := s.expandPivotColumns(ctx, builder)
	if err != nil {
		return Builder{}, err
	}
	if planned, matched := lowerGraphQLBuilder(expanded); matched {
		if err := validateAdvancedBuilder(planned); err != nil {
			return Builder{}, err
		}
		return planned, nil
	}
	return expanded, nil
}

func (s *Service) validateBuilder(ctx context.Context, builder Builder) error {
	seenAliases := map[string]struct{}{}
	rootFields, err := s.discoverFields(ctx, proto.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           builder.Project,
		AuthResourcePaths: builder.AuthResourcePaths,
		ResourceType:      builder.RootResourceType,
	})
	if err != nil {
		return err
	}
	rootPivots, err := s.discoverFields(ctx, proto.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           builder.Project,
		AuthResourcePaths: builder.AuthResourcePaths,
		ResourceType:      builder.RootResourceType,
		PivotOnly:         true,
	})
	if err != nil {
		return err
	}
	if err := validateNodeSelections(builder.Fields, builder.Pivots, builder.Aggregates, builder.Slices, rootFields, rootPivots); err != nil {
		return err
	}
	for _, step := range builder.Traversals {
		if err := s.validateTraversal(ctx, builder.Project, builder.AuthResourcePaths, builder.RootResourceType, step, seenAliases); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) validateTraversal(ctx context.Context, project string, authResourcePaths []string, sourceType string, step TraversalStep, seenAliases map[string]struct{}) error {
	if step.Alias == "" {
		return fmt.Errorf("traversal alias is required")
	}
	if _, ok := seenAliases[step.Alias]; ok {
		return fmt.Errorf("traversal alias %q is duplicated", step.Alias)
	}
	seenAliases[step.Alias] = struct{}{}
	refs, err := s.discoverReferences(ctx, proto.PopulatedReferenceOptions{
		ConnectionOptions: s.connOpts,
		Project:           project,
		AuthResourcePaths: authResourcePaths,
		NodeType:          sourceType,
		Mode:              proto.TraversalModeBuilder,
	})
	if err != nil {
		return err
	}
	found := false
	for _, ref := range refs {
		if ref.Label == step.Label && ref.ToType == step.ToResourceType {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("traversal %s -> %s (%s) is not populated", sourceType, step.ToResourceType, step.Label)
	}
	fields, err := s.discoverFields(ctx, proto.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           project,
		AuthResourcePaths: authResourcePaths,
		ResourceType:      step.ToResourceType,
	})
	if err != nil {
		return err
	}
	pivotFields, err := s.discoverFields(ctx, proto.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           project,
		AuthResourcePaths: authResourcePaths,
		ResourceType:      step.ToResourceType,
		PivotOnly:         true,
	})
	if err != nil {
		return err
	}
	if err := validateNodeSelections(step.Fields, step.Pivots, step.Aggregates, step.Slices, fields, pivotFields); err != nil {
		return fmt.Errorf("alias %s: %w", step.Alias, err)
	}
	for _, child := range step.Traversals {
		if err := s.validateTraversal(ctx, project, authResourcePaths, step.ToResourceType, child, seenAliases); err != nil {
			return err
		}
	}
	return nil
}

func validateNodeSelections(fields []FieldSelect, pivots []PivotSelect, aggregates []AggregateSelect, slices []RepresentativeSlice, discovered []proto.PopulatedField, pivotable []proto.PopulatedField) error {
	seenFields := map[string]struct{}{}
	for _, field := range fields {
		if field.Name == "" || field.Select == "" {
			return fmt.Errorf("field selections require name and select")
		}
		if _, ok := seenFields[field.Name]; ok {
			return fmt.Errorf("field name %q is duplicated", field.Name)
		}
		seenFields[field.Name] = struct{}{}
		if _, err := ParseSelector(field.Select); err != nil {
			return fmt.Errorf("invalid selector for field %q: %w", field.Name, err)
		}
		for _, fallback := range field.FallbackSelects {
			if _, err := ParseSelector(fallback); err != nil {
				return fmt.Errorf("invalid fallback selector for field %q: %w", field.Name, err)
			}
		}
	}
	seenPivots := map[string]struct{}{}
	for _, pivot := range pivots {
		if pivot.Name == "" || pivot.Select == "" {
			return fmt.Errorf("pivot selections require name and select")
		}
		if _, ok := seenPivots[pivot.Name]; ok {
			return fmt.Errorf("pivot name %q is duplicated", pivot.Name)
		}
		seenPivots[pivot.Name] = struct{}{}
		sel, err := ParseSelector(pivot.Select)
		if err != nil {
			return fmt.Errorf("invalid selector for pivot %q: %w", pivot.Name, err)
		}
		canonical := sel.CanonicalPath()
		match := findFieldByPath(pivotable, canonical)
		if match == nil || !match.PivotCandidate {
			return fmt.Errorf("pivot selector %q is not pivotable", pivot.Select)
		}
		kind := strings.ToUpper(strings.TrimSpace(pivot.PivotKind))
		if kind == "" {
			kind = PivotKindCodeableConceptDisplayValue
		}
		if kind != PivotKindCodeableConceptDisplayValue {
			return fmt.Errorf("unsupported pivot kind %q", pivot.PivotKind)
		}
		if len(pivot.Columns) == 0 && len(match.PivotColumns) == 0 {
			return fmt.Errorf("pivot %q has no available pivot columns", pivot.Name)
		}
	}
	seenAggregates := map[string]struct{}{}
	for _, agg := range aggregates {
		if strings.TrimSpace(agg.Name) == "" {
			return fmt.Errorf("aggregate selections require name")
		}
		if _, ok := seenAggregates[agg.Name]; ok {
			return fmt.Errorf("aggregate name %q is duplicated", agg.Name)
		}
		seenAggregates[agg.Name] = struct{}{}
		switch strings.ToUpper(strings.TrimSpace(agg.Operation)) {
		case "COUNT", "COUNT_DISTINCT", "EXISTS", "DISTINCT_VALUES":
		default:
			return fmt.Errorf("aggregate %q uses unsupported operation %q", agg.Name, agg.Operation)
		}
		if strings.TrimSpace(agg.Select) != "" {
			sel, err := ParseSelector(agg.Select)
			if err != nil {
				return fmt.Errorf("invalid aggregate selector for %q: %w", agg.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("aggregate selector %q is not present in populated fields", agg.Select)
			}
		}
		if strings.TrimSpace(agg.PredicatePath) != "" {
			sel, err := ParseSelector(agg.PredicatePath)
			if err != nil {
				return fmt.Errorf("invalid aggregate predicate selector for %q: %w", agg.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("aggregate predicate selector %q is not present in populated fields", agg.PredicatePath)
			}
		}
	}
	seenSlices := map[string]struct{}{}
	for _, slice := range slices {
		if strings.TrimSpace(slice.Name) == "" {
			return fmt.Errorf("representative slices require name")
		}
		if _, ok := seenSlices[slice.Name]; ok {
			return fmt.Errorf("representative slice name %q is duplicated", slice.Name)
		}
		seenSlices[slice.Name] = struct{}{}
		if slice.Limit <= 0 {
			return fmt.Errorf("representative slice %q requires positive limit", slice.Name)
		}
		if strings.TrimSpace(slice.PredicatePath) != "" {
			sel, err := ParseSelector(slice.PredicatePath)
			if err != nil {
				return fmt.Errorf("invalid representative slice predicate for %q: %w", slice.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("representative slice predicate %q is not present in populated fields", slice.PredicatePath)
			}
		}
		for _, field := range slice.Fields {
			if strings.TrimSpace(field.Name) == "" || strings.TrimSpace(field.Select) == "" {
				return fmt.Errorf("representative slice %q requires fields with name and select", slice.Name)
			}
			sel, err := ParseSelector(field.Select)
			if err != nil {
				return fmt.Errorf("invalid representative slice field for %q: %w", slice.Name, err)
			}
			if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
				return fmt.Errorf("representative slice selector %q is not present in populated fields", field.Select)
			}
			for _, fallback := range field.FallbackSelects {
				fallbackSel, err := ParseSelector(fallback)
				if err != nil {
					return fmt.Errorf("invalid representative slice fallback selector for %q: %w", slice.Name, err)
				}
				if findFieldByPath(discovered, fallbackSel.CanonicalPath()) == nil {
					return fmt.Errorf("representative slice fallback selector %q is not present in populated fields", fallback)
				}
			}
		}
	}
	for _, field := range fields {
		sel, _ := ParseSelector(field.Select)
		if findFieldByPath(discovered, sel.CanonicalPath()) == nil {
			return fmt.Errorf("selector %q is not present in populated fields", field.Select)
		}
		for _, fallback := range field.FallbackSelects {
			fallbackSel, _ := ParseSelector(fallback)
			if findFieldByPath(discovered, fallbackSel.CanonicalPath()) == nil {
				return fmt.Errorf("fallback selector %q is not present in populated fields", fallback)
			}
		}
	}
	return nil
}

func (s *Service) expandPivotColumns(ctx context.Context, builder Builder) (Builder, error) {
	pivots, err := s.discoverFields(ctx, proto.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           builder.Project,
		AuthResourcePaths: builder.AuthResourcePaths,
		ResourceType:      builder.RootResourceType,
		PivotOnly:         true,
	})
	if err != nil {
		return Builder{}, err
	}
	builder.Pivots = fillPivotColumns(builder.Pivots, pivots)
	for i := range builder.Traversals {
		if err := s.expandTraversalPivotColumns(ctx, builder.Project, builder.AuthResourcePaths, &builder.Traversals[i]); err != nil {
			return Builder{}, err
		}
	}
	return builder, nil
}

func (s *Service) expandTraversalPivotColumns(ctx context.Context, project string, authResourcePaths []string, step *TraversalStep) error {
	pivots, err := s.discoverFields(ctx, proto.PopulatedFieldOptions{
		ConnectionOptions: s.connOpts,
		Project:           project,
		AuthResourcePaths: authResourcePaths,
		ResourceType:      step.ToResourceType,
		PivotOnly:         true,
	})
	if err != nil {
		return err
	}
	step.Pivots = fillPivotColumns(step.Pivots, pivots)
	for i := range step.Traversals {
		if err := s.expandTraversalPivotColumns(ctx, project, authResourcePaths, &step.Traversals[i]); err != nil {
			return err
		}
	}
	return nil
}

func fillPivotColumns(in []PivotSelect, discovered []proto.PopulatedField) []PivotSelect {
	if len(in) == 0 {
		return []PivotSelect{}
	}
	out := make([]PivotSelect, 0, len(in))
	for _, pivot := range in {
		if len(pivot.Columns) == 0 {
			sel, err := ParseSelector(pivot.Select)
			if err == nil {
				if item := findFieldByPath(discovered, sel.CanonicalPath()); item != nil {
					pivot.Columns = cloneStrings(item.PivotColumns)
				}
			}
		}
		out = append(out, pivot)
	}
	return out
}

func findFieldByPath(fields []proto.PopulatedField, path string) *proto.PopulatedField {
	for i := range fields {
		if fields[i].Path == path {
			return &fields[i]
		}
	}
	return nil
}

func (s *Service) runQuery(ctx context.Context, compiled CompiledQuery) (*Result, error) {
	rows := make([]map[string]any, 0, compiled.Limit)
	rowCount := 0
	err := s.executeRows(ctx, proto.ExecuteQueryOptions{
		ConnectionOptions: s.connOpts,
		BatchSize:         1000,
	}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
		rows = append(rows, cloneRow(row))
		rowCount++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Columns:  append([]string(nil), compiled.Columns...),
		Rows:     rows,
		RowCount: rowCount,
	}, nil
}

func cloneRow(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (s *Service) resolveAuthResourcePaths(ctx context.Context, principal *writeapi.Principal, project string, requested []string) ([]string, error) {
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

func authorizeProject(principal *writeapi.Principal, project string, ignorePrincipalProjects bool) error {
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

type Selector struct {
	Steps  []SelectorStep
	Filter *ContainsFilter
}

type SelectorStep struct {
	Field   string
	Iterate bool
	Index   *int
}

type ContainsFilter struct {
	Field  string
	Needle string
}

var containsPattern = regexp.MustCompile(`^([A-Za-z0-9_]+)\s+contains\s+"([^"]*)"$`)

func ParseSelector(input string) (Selector, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return Selector{}, fmt.Errorf("selector is required")
	}
	var filter *ContainsFilter
	pathPart := input
	if before, after, found := strings.Cut(input, " where "); found {
		pathPart = strings.TrimSpace(before)
		match := containsPattern.FindStringSubmatch(strings.TrimSpace(after))
		if len(match) != 3 {
			return Selector{}, fmt.Errorf("unsupported where clause %q", after)
		}
		filter = &ContainsFilter{Field: match[1], Needle: match[2]}
	}
	parts := strings.Split(pathPart, ".")
	steps := make([]SelectorStep, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return Selector{}, fmt.Errorf("invalid path segment in %q", input)
		}
		step := SelectorStep{}
		switch {
		case strings.HasSuffix(part, "[]"):
			step.Field = strings.TrimSuffix(part, "[]")
			step.Iterate = true
		case strings.HasSuffix(part, "]") && strings.Contains(part, "["):
			idxStart := strings.Index(part, "[")
			step.Field = part[:idxStart]
			idx, err := strconv.Atoi(strings.TrimSuffix(part[idxStart+1:], "]"))
			if err != nil {
				return Selector{}, fmt.Errorf("invalid array index in %q", part)
			}
			step.Index = &idx
		default:
			step.Field = part
		}
		if step.Field == "" {
			return Selector{}, fmt.Errorf("invalid field in %q", part)
		}
		steps = append(steps, step)
	}
	return Selector{Steps: steps, Filter: filter}, nil
}

func (s Selector) CanonicalPath() string {
	parts := make([]string, 0, len(s.Steps))
	for _, step := range s.Steps {
		switch {
		case step.Iterate:
			parts = append(parts, step.Field+"[]")
		case step.Index != nil:
			parts = append(parts, step.Field+"[]")
		default:
			parts = append(parts, step.Field)
		}
	}
	return strings.Join(parts, ".")
}

type CompiledQuery struct {
	Project           string
	RootResourceType  string
	AuthResourcePaths []string
	PlanMode          string
	PlanProfile       string
	NamedSetCount     int
	FileSummaries     bool
	StudyLookup       bool
	Query             string
	BindVars          map[string]any
	Columns           []string
	Limit             int
}

func Compile(builder Builder, limit int) (CompiledQuery, error) {
	if usesAdvancedBuilder(builder) {
		return compileAdvanced(builder, limit)
	}
	c := &compiler{
		builder: builder,
		bindVars: map[string]any{
			"project":                          builder.Project,
			"auth_resource_paths":              builder.AuthResourcePaths,
			"auth_resource_paths_unrestricted": builder.AuthResourcePaths == nil,
		},
	}
	if limit > 0 {
		c.bindVars["limit"] = limit
	}
	rootVar := "root"
	objectLines := []string{}
	for _, field := range builder.Fields {
		sel, _ := ParseSelector(field.Select)
		expr, err := c.compileRootFieldSelect(rootVar+".payload", field, sel)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(field.Name), expr))
		c.columns = append(c.columns, field.Name)
	}
	for _, pivot := range builder.Pivots {
		sel, _ := ParseSelector(pivot.Select)
		cols := pivot.Columns
		if len(cols) == 0 {
			cols = []string{"value"}
		}
		for _, col := range cols {
			colName := sanitizeColumnName(pivot.Name + "__" + col)
			expr, err := c.compileRootPivot(rootVar+".payload", sel, col, pivot.ValuePath)
			if err != nil {
				return CompiledQuery{}, err
			}
			objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
			c.columns = append(c.columns, colName)
		}
	}
	for _, agg := range builder.Aggregates {
		expr, err := c.compileRootAggregateExpr(rootVar+".payload", agg)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(agg.Name), expr))
		c.columns = append(c.columns, agg.Name)
	}
	lets := []string{}
	for _, step := range builder.Traversals {
		if err := c.compileTraversal(rootVar, false, step, &lets, &objectLines); err != nil {
			return CompiledQuery{}, err
		}
	}
	for _, slice := range builder.Slices {
		expr, err := c.compileRootSlice(rootVar, slice)
		if err != nil {
			return CompiledQuery{}, err
		}
		objectLines = append(objectLines, fmt.Sprintf("    %s: %s", quoteKey(slice.Name), expr))
		c.columns = append(c.columns, slice.Name)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("FOR %s IN %s\n", rootVar, builder.RootResourceType))
	sb.WriteString(fmt.Sprintf("  FILTER %s.project == @project\n", rootVar))
	sb.WriteString(fmt.Sprintf("  FILTER @auth_resource_paths_unrestricted == true OR %s.auth_resource_path IN @auth_resource_paths\n", rootVar))
	sb.WriteString(fmt.Sprintf("  SORT %s._key\n", rootVar))
	if limit > 0 {
		sb.WriteString("  LIMIT @limit\n")
	}
	for _, let := range lets {
		sb.WriteString(let)
		sb.WriteByte('\n')
	}
	sb.WriteString("  RETURN {\n")
	sb.WriteString(fmt.Sprintf("    %s: %s._key,\n", quoteKey("_key"), rootVar))
	for i, line := range objectLines {
		if i == len(objectLines)-1 {
			sb.WriteString(line)
			sb.WriteByte('\n')
		} else {
			sb.WriteString(line)
			sb.WriteString(",\n")
		}
	}
	sb.WriteString("  }\n")
	return CompiledQuery{
		Project:           builder.Project,
		RootResourceType:  builder.RootResourceType,
		AuthResourcePaths: append([]string(nil), builder.AuthResourcePaths...),
		PlanMode:          planMode(builder.PlanHint),
		PlanProfile:       planProfile(builder.PlanHint),
		NamedSetCount:     planNamedSetCount(builder.PlanHint),
		FileSummaries:     planFileSummaries(builder.PlanHint),
		StudyLookup:       planStudyLookup(builder.PlanHint),
		Query:             sb.String(),
		BindVars:          c.bindVars,
		Columns:           append([]string(nil), c.columns...),
		Limit:             limit,
	}, nil
}

func planMode(hint *PlanHint) string {
	if hint == nil || hint.Mode == "" {
		return "generic_traversal"
	}
	return hint.Mode
}

func planProfile(hint *PlanHint) string {
	if hint == nil {
		return ""
	}
	return hint.Profile
}

func planNamedSetCount(hint *PlanHint) int {
	if hint == nil {
		return 0
	}
	return hint.NamedSetCount
}

func planFileSummaries(hint *PlanHint) bool {
	if hint == nil {
		return false
	}
	return hint.ClassifiedFileSummaries
}

func planStudyLookup(hint *PlanHint) bool {
	if hint == nil {
		return false
	}
	return hint.StudyLookup
}

type compiler struct {
	builder   Builder
	bindVars  map[string]any
	columns   []string
	bindCount int
}

func (c *compiler) compileTraversal(parentVar string, parentIsArray bool, step TraversalStep, lets *[]string, objectLines *[]string) error {
	labelBind := c.newBind(step.Alias+"_label", step.Label)
	toBind := c.newBind(step.Alias+"_to", step.ToResourceType)
	nodeVar := sanitizeColumnName(step.Alias) + "_nodes"
	var let string
	if parentIsArray {
		let = fmt.Sprintf("  LET %s = UNIQUE(FLATTEN(FOR __parent IN %s FOR __node, __edge IN 1..1 INBOUND __parent fhir_edge FILTER __edge.project == @project FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s FILTER __node.resourceType == @%s RETURN [__node]))", nodeVar, parentVar, labelBind, toBind)
	} else {
		let = fmt.Sprintf("  LET %s = UNIQUE(FOR __node, __edge IN 1..1 INBOUND %s fhir_edge FILTER __edge.project == @project FILTER @auth_resource_paths_unrestricted == true OR (__edge.auth_resource_path IN @auth_resource_paths AND __node.auth_resource_path IN @auth_resource_paths) FILTER __edge.label == @%s FILTER __node.resourceType == @%s RETURN __node)", nodeVar, parentVar, labelBind, toBind)
	}
	*lets = append(*lets, let)
	for _, field := range step.Fields {
		sel, _ := ParseSelector(field.Select)
		expr, err := c.compileTraversalFieldSelect(nodeVar, field, sel)
		if err != nil {
			return err
		}
		colName := sanitizeColumnName(step.Alias + "__" + field.Name)
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
	}
	for _, pivot := range step.Pivots {
		sel, _ := ParseSelector(pivot.Select)
		cols := pivot.Columns
		if len(cols) == 0 {
			cols = []string{"value"}
		}
		for _, col := range cols {
			colName := sanitizeColumnName(step.Alias + "__" + pivot.Name + "__" + col)
			expr, err := c.compileTraversalPivot(nodeVar, sel, col, pivot.ValuePath)
			if err != nil {
				return err
			}
			*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
			c.columns = append(c.columns, colName)
		}
	}
	for _, agg := range step.Aggregates {
		expr, err := c.compileSetAggregateExpr(nodeVar, agg)
		if err != nil {
			return err
		}
		colName := sanitizeColumnName(step.Alias + "__" + agg.Name)
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
	}
	for _, slice := range step.Slices {
		expr, err := c.compileSetSlice(nodeVar, setModeNode, slice)
		if err != nil {
			return err
		}
		colName := sanitizeColumnName(step.Alias + "__" + slice.Name)
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
	}
	for _, child := range step.Traversals {
		if err := c.compileTraversal(nodeVar, true, child, lets, objectLines); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) compileRootFieldSelect(payloadVar string, field FieldSelect, sel Selector) (string, error) {
	if len(field.FallbackSelects) > 0 {
		return c.compileFirstNonNullExpr(payloadVar, append([]string{field.Select}, field.FallbackSelects...)), nil
	}
	return c.compileRootField(payloadVar, sel)
}

func (c *compiler) compileRootField(payloadVar string, sel Selector) (string, error) {
	if sel.Filter == nil && selectorHasNoArrays(sel) {
		return compileDirectExpr(payloadVar, sel.Steps), nil
	}
	return "FIRST" + compileSelectorArrayExpr(payloadVar, sel, c), nil
}

func (c *compiler) compileTraversalFieldSelect(nodeVar string, field FieldSelect, sel Selector) (string, error) {
	if len(field.FallbackSelects) > 0 {
		tmp := DerivedField{
			Source:          nodeVar,
			Select:          field.Select,
			FallbackSelects: field.FallbackSelects,
		}
		return c.compileUniqueField("", tmp, map[string]setMode{nodeVar: setModeNode})
	}
	return c.compileTraversalField(nodeVar, sel)
}

func (c *compiler) compileTraversalField(nodeVar string, sel Selector) (string, error) {
	return fmt.Sprintf("UNIQUE(FLATTEN(FOR __n IN %s RETURN %s))", nodeVar, compileSelectorArrayExpr("__n.payload", sel, c)), nil
}

func (c *compiler) compileRootPivot(payloadVar string, sel Selector, column string, valuePath string) (string, error) {
	return fmt.Sprintf("FIRST(%s)", compilePivotValueArrayExpr(payloadVar, sel, column, valuePath, c)), nil
}

func (c *compiler) compileTraversalPivot(nodeVar string, sel Selector, column string, valuePath string) (string, error) {
	return fmt.Sprintf("UNIQUE(FLATTEN(FOR __n IN %s RETURN %s))", nodeVar, compilePivotValueArrayExpr("__n.payload", sel, column, valuePath, c)), nil
}

func selectorHasNoArrays(sel Selector) bool {
	for _, step := range sel.Steps {
		if step.Iterate || step.Index != nil {
			return false
		}
	}
	return true
}

func compileDirectExpr(rootVar string, steps []SelectorStep) string {
	cur := rootVar
	for _, step := range steps {
		if step.Index != nil {
			cur = fmt.Sprintf("((%s.%s ? %s.%s : [])[%d])", cur, step.Field, cur, step.Field, *step.Index)
			continue
		}
		cur = fmt.Sprintf("%s.%s", cur, step.Field)
	}
	return cur
}

func compileSelectorArrayExpr(rootVar string, sel Selector, c *compiler) string {
	prefix := sel.Steps
	if len(prefix) == 0 {
		return "[]"
	}
	last := prefix[len(prefix)-1]
	prefix = prefix[:len(prefix)-1]
	lines := []string{fmt.Sprintf("FOR __root IN [%s]", rootVar)}
	cur := "__root"
	tmpCount := 0
	for _, step := range prefix {
		next := fmt.Sprintf("__s%d", tmpCount)
		tmpCount++
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("  FOR %s IN (%s.%s ? %s.%s : [])", next, cur, step.Field, cur, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("  LET %s = ((%s.%s ? %s.%s : [])[%d])", next, cur, step.Field, cur, step.Field, *step.Index))
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", next))
		default:
			lines = append(lines, fmt.Sprintf("  LET %s = %s.%s", next, cur, step.Field))
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", next))
		}
		cur = next
	}
	if sel.Filter != nil {
		filterBind := c.newBind("contains", sel.Filter.Needle)
		lines = append(lines, fmt.Sprintf("  FILTER CONTAINS(%s.%s ? %s.%s : \"\", @%s)", cur, sel.Filter.Field, cur, sel.Filter.Field, filterBind))
	}
	finalExpr := extractFinalExpr(cur, last)
	lines = append(lines, fmt.Sprintf("  LET __value = %s", finalExpr))
	lines = append(lines, "  FILTER __value != null")
	lines = append(lines, "  RETURN __value")
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )"
}

func compileObjectArrayExpr(rootVar string, sel Selector, c *compiler) string {
	lines := []string{fmt.Sprintf("FOR __root IN [%s]", rootVar)}
	cur := "__root"
	tmpCount := 0
	for _, step := range sel.Steps {
		next := fmt.Sprintf("__o%d", tmpCount)
		tmpCount++
		switch {
		case step.Iterate:
			lines = append(lines, fmt.Sprintf("  FOR %s IN (%s.%s ? %s.%s : [])", next, cur, step.Field, cur, step.Field))
		case step.Index != nil:
			lines = append(lines, fmt.Sprintf("  LET %s = ((%s.%s ? %s.%s : [])[%d])", next, cur, step.Field, cur, step.Field, *step.Index))
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", next))
		default:
			lines = append(lines, fmt.Sprintf("  LET %s = %s.%s", next, cur, step.Field))
			lines = append(lines, fmt.Sprintf("  FILTER %s != null", next))
		}
		cur = next
	}
	lines = append(lines, fmt.Sprintf("  FILTER %s != null", cur))
	lines = append(lines, fmt.Sprintf("  RETURN %s", cur))
	return "(\n    " + strings.Join(lines, "\n    ") + "\n  )"
}

func compilePivotMatchArrayExpr(rootVar string, sel Selector, column string, c *compiler) string {
	colBind := c.newBind("pivot", column)
	objects := compileObjectArrayExpr(rootVar, sel, c)
	return fmt.Sprintf("(\n    FOR __obj IN %s\n      LET __match = FIRST(FOR __coding IN (__obj.coding ? __obj.coding : []) FILTER __coding.display == @%s RETURN (__obj.text ? __obj.text : (__coding.display ? __coding.display : __coding.code)))\n      FILTER __match != null\n      RETURN __match\n  )", objects, colBind)
}

func compilePivotValueArrayExpr(rootVar string, sel Selector, column string, valuePath string, c *compiler) string {
	colBind := c.newBind("pivot", column)
	objects := compileObjectArrayExpr(rootVar, sel, c)
	valueExpr := "__obj.text ? __obj.text : (__coding.display ? __coding.display : __coding.code)"
	if strings.TrimSpace(valuePath) != "" {
		valueSel, err := ParseSelector(valuePath)
		if err == nil {
			if valueSel.Filter == nil && selectorHasNoArrays(valueSel) {
				valueExpr = compileDirectExpr("__obj", valueSel.Steps)
			} else {
				valueExpr = "FIRST" + compileSelectorArrayExpr("__obj", valueSel, c)
			}
		}
	}
	return fmt.Sprintf("(\n    FOR __obj IN %s\n      LET __match = FIRST(FOR __coding IN (__obj.coding ? __obj.coding : []) FILTER __coding.display == @%s RETURN %s)\n      FILTER __match != null\n      RETURN __match\n  )", objects, colBind, valueExpr)
}

func extractFinalExpr(cur string, step SelectorStep) string {
	switch {
	case step.Iterate:
		return fmt.Sprintf("(%s.%s ? %s.%s : [])", cur, step.Field, cur, step.Field)
	case step.Index != nil:
		return fmt.Sprintf("((%s.%s ? %s.%s : [])[%d])", cur, step.Field, cur, step.Field, *step.Index)
	default:
		return fmt.Sprintf("%s.%s", cur, step.Field)
	}
}

func (c *compiler) newBind(prefix string, value any) string {
	name := fmt.Sprintf("__%s_%d", sanitizeColumnName(prefix), c.bindCount)
	c.bindCount++
	c.bindVars[name] = value
	return name
}

func sanitizeColumnName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func quoteKey(key string) string {
	data, _ := json.Marshal(key)
	return string(data)
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
