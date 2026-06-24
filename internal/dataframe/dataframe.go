package dataframe

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"arangodb-proto/internal/fhirschema"
	"arangodb-proto/internal/proto"
	"arangodb-proto/internal/writeapi"
)

const defaultRowLimit = 25

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
	Name              string
	FieldRef          string
	Select            string
	FallbackFieldRefs []string
	FallbackSelects   []string
	ValueMode         string
}

type PivotSelect struct {
	Name         string
	FieldRef     string
	ColumnSelect string
	ValueSelect  string
	Columns      []string
	PivotFamily  string
}

type AggregateSelect struct {
	Name              string
	Operation         string
	FieldRef          string
	Select            string
	PredicateFieldRef string
	PredicatePath     string
	PredicateEquals   string
	ValueMode         string
}

type RunRequest struct {
	Builder Builder
	Limit   int
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
	if err := s.validateBuilder(ctx, builder); err != nil {
		return Builder{}, err
	}
	expanded, err := s.expandPivotColumns(ctx, builder)
	if err != nil {
		return Builder{}, err
	}
	planned, err := lowerGraphQLBuilder(expanded)
	if err != nil {
		return Builder{}, err
	}
	if err := validateAdvancedBuilder(planned); err != nil {
		return Builder{}, err
	}
	return planned, nil
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
		if pivot.Name == "" || pivot.ColumnSelect == "" || pivot.ValueSelect == "" {
			return fmt.Errorf("pivot selections require name, column selector, and value selector")
		}
		if _, ok := seenPivots[pivot.Name]; ok {
			return fmt.Errorf("pivot name %q is duplicated", pivot.Name)
		}
		seenPivots[pivot.Name] = struct{}{}
		columnSel, err := ParseSelector(pivot.ColumnSelect)
		if err != nil {
			return fmt.Errorf("invalid column selector for pivot %q: %w", pivot.Name, err)
		}
		valueSel, err := ParseSelector(pivot.ValueSelect)
		if err != nil {
			return fmt.Errorf("invalid value selector for pivot %q: %w", pivot.Name, err)
		}
		pivotSpec, err := fhirschema.ValidatePivotSelectors(resourceTypeFromDiscovered(discovered), selectorSpecFromSelector(columnSel), selectorSpecFromSelector(valueSel))
		if err != nil {
			return fmt.Errorf("pivot %q: %w", pivot.Name, err)
		}
		match := findFieldByPath(pivotable, pivotRootPath(resourceTypeFromDiscovered(discovered), pivotSpec.Family, columnSel.CanonicalPath()))
		if match == nil || !match.PivotCandidate {
			return fmt.Errorf("pivot selector %q is not pivotable", pivot.ColumnSelect)
		}
		if len(pivot.Columns) == 0 && len(match.PivotColumns) == 0 {
			return fmt.Errorf("pivot %q has no available pivot columns", pivot.Name)
		}
		pivot.PivotFamily = pivotSpec.Family
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
		if strings.TrimSpace(pivot.PivotFamily) == "" {
			if item := findFieldByPath(discovered, pivotRootPath(resourceTypeFromDiscovered(discovered), pivot.PivotFamily, canonicalColumnSelect(pivot.ColumnSelect))); item != nil {
				pivot.PivotFamily = item.PivotFamily
			}
		}
		out = append(out, pivot)
	}
	return out
}

func canonicalColumnSelect(expr string) string {
	sel, err := ParseSelector(expr)
	if err != nil {
		return ""
	}
	return sel.CanonicalPath()
}

func pivotRootPath(resourceType string, family string, columnCanonical string) string {
	if family == fhirschema.PivotFamilyObservationCodeValue || (resourceType == "Observation" && strings.HasPrefix(columnCanonical, "code")) {
		return "code"
	}
	parts := strings.Split(columnCanonical, ".")
	for i := len(parts); i > 0; i-- {
		path := strings.Join(parts[:i], ".")
		if fhirschema.ResolvesToCodeableConcept(resourceType, path) {
			return path
		}
	}
	return columnCanonical
}

func resourceTypeFromDiscovered(fields []proto.PopulatedField) string {
	if len(fields) == 0 {
		return ""
	}
	return fields[0].ResourceType
}

func selectorSpecFromSelector(sel Selector) fhirschema.FieldSelectorSpec {
	sourcePath := ""
	valuePath := ""
	if len(sel.Steps) > 0 {
		last := len(sel.Steps) - 1
		valuePath = selectorStepText(sel.Steps[last])
		if last > 0 {
			parts := make([]string, 0, last)
			for _, step := range sel.Steps[:last] {
				parts = append(parts, selectorStepText(step))
			}
			sourcePath = strings.Join(parts, ".")
		}
	}
	var where *fhirschema.FieldPredicateSpec
	if sel.Filter != nil {
		where = &fhirschema.FieldPredicateSpec{
			Path:  sel.Filter.Field,
			Op:    fhirschema.PredicateContains,
			Value: sel.Filter.Needle,
		}
	}
	return fhirschema.FieldSelectorSpec{
		SourcePath: sourcePath,
		Where:      where,
		ValuePath:  valuePath,
	}
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
	columns := materializedColumns(compiled.Columns, compiled.PivotFields)
	seenColumns := make(map[string]struct{}, len(columns))
	for _, col := range columns {
		seenColumns[col] = struct{}{}
	}
	err := s.executeRows(ctx, proto.ExecuteQueryOptions{
		ConnectionOptions: s.connOpts,
		BatchSize:         1000,
	}, compiled.Query, compiled.BindVars, func(row map[string]any) error {
		flatRow := flattenPivotFields(cloneRow(row), compiled.PivotFields)
		for key := range flatRow {
			if _, ok := seenColumns[key]; ok {
				continue
			}
			seenColumns[key] = struct{}{}
			columns = append(columns, key)
		}
		rows = append(rows, flatRow)
		rowCount++
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Result{
		Columns:  columns,
		Rows:     rows,
		RowCount: rowCount,
	}, nil
}

func materializedColumns(columns []string, pivotFields []string) []string {
	if len(columns) == 0 {
		return []string{}
	}
	skip := make(map[string]struct{}, len(pivotFields))
	for _, field := range pivotFields {
		skip[field] = struct{}{}
	}
	out := make([]string, 0, len(columns))
	for _, col := range columns {
		if _, ok := skip[col]; ok {
			continue
		}
		out = append(out, col)
	}
	return out
}

func flattenPivotFields(row map[string]any, pivotFields []string) map[string]any {
	for _, field := range pivotFields {
		value, ok := row[field]
		if !ok {
			continue
		}
		obj, ok := value.(map[string]any)
		if !ok {
			continue
		}
		delete(row, field)
		for key, item := range obj {
			row[sanitizeColumnName(field+"__"+key)] = item
		}
	}
	return row
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

type Selector = fhirschema.Selector
type SelectorStep = fhirschema.SelectorStep
type ContainsFilter = fhirschema.ContainsFilter

func ParseSelector(input string) (Selector, error) {
	return fhirschema.ParseSelector(input)
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
	PivotFields       []string
	Limit             int
}

func Compile(builder Builder, limit int) (CompiledQuery, error) {
	if usesAdvancedBuilder(builder) {
		return compileAdvanced(builder, limit)
	}
	return CompiledQuery{}, fmt.Errorf("unsupported dataframe query shape: request was not lowered into the optimized advanced plan")
}

func planMode(hint *PlanHint) string {
	if hint == nil || hint.Mode == "" {
		return "unsupported"
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
	pivotFields []string
	bindCount int
	pivotExprs map[string]string
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
		keySel, _ := ParseSelector(pivot.ColumnSelect)
		valueSel, _ := ParseSelector(pivot.ValueSelect)
		colName := sanitizeColumnName(step.Alias + "__" + pivot.Name)
		expr, err := c.compileTraversalPivot(nodeVar, keySel, valueSel, pivot.Columns)
		if err != nil {
			return err
		}
		*objectLines = append(*objectLines, fmt.Sprintf("    %s: %s", quoteKey(colName), expr))
		c.columns = append(c.columns, colName)
		c.pivotFields = append(c.pivotFields, colName)
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

func (c *compiler) compileRootPivot(payloadVar string, keySel Selector, valueSel Selector, columns []string) (string, error) {
	return c.compilePivotMapExpr("FOR __item IN ["+payloadVar+"]", "__item", keySel, valueSel, columns)
}

func (c *compiler) compileTraversalPivot(nodeVar string, keySel Selector, valueSel Selector, columns []string) (string, error) {
	return c.compilePivotMapExpr("FOR __item IN "+nodeVar, "__item.payload", keySel, valueSel, columns)
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

func (c *compiler) compilePivotMapExpr(itemLoop string, payloadVar string, keySel Selector, valueSel Selector, columns []string) (string, error) {
	keyExpr := compileSelectorArrayExpr(payloadVar, keySel, c)
	valueExpr := compileSelectorArrayExpr(payloadVar, valueSel, c)
	filterLine := ""
	if len(columns) > 0 {
		colBind := c.newBind("pivot_cols", append([]string(nil), columns...))
		filterLine = fmt.Sprintf("\n          FILTER POSITION(@%s, __key, true)", colBind)
	}
  return fmt.Sprintf(`MERGE(
    FOR __pair IN (
      %s
        LET __keys = UNIQUE(%s)
        LET __values = %s
        FILTER LENGTH(__values) > 0
        FOR __key IN __keys%s
          RETURN { key: __key, values: __values }
    )
      COLLECT __key = __pair.key INTO __group
      LET __flat_values = UNIQUE(FLATTEN(__group[*].__pair.values))
      FILTER LENGTH(__flat_values) > 0
      RETURN { [__key]: FIRST(__flat_values) }
  )`, itemLoop, keyExpr, valueExpr, filterLine), nil
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

func selectorStepText(step SelectorStep) string {
	switch {
	case step.Iterate:
		return step.Field + "[]"
	case step.Index != nil:
		return fmt.Sprintf("%s[%d]", step.Field, *step.Index)
	default:
		return step.Field
	}
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
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
