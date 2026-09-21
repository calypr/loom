package authoringv2

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
	semanticfhir "github.com/calypr/loom/internal/fhir/semantic"
)

// MigrateAggregateTemporalPolicy advances v8 workspaces to the split
// contributor-window and ordering contract. Persisted source conversion runs
// in DecodeWorkspace before this version boundary is applied.
func MigrateAggregateTemporalPolicy(workspace Workspace) Workspace {
	if workspace.SemanticsVersion == aggregateTemporalPolicySemanticsVersion-1 {
		workspace.SemanticsVersion = aggregateTemporalPolicySemanticsVersion
	}
	return workspace
}

// MigrateLegacyContributors converts the private v3 aggregate where wire into
// the v4 catalog-bound contributor intent. It is intentionally error-returning
// and transactional: callers must have the pinned catalog before interpreting
// a path, and an ambiguous draft is returned unchanged with an error.
func MigrateLegacyContributors(workspace Workspace, catalog CatalogSnapshot) (Workspace, error) {
	if !workspaceHasLegacyContributors(workspace) {
		return workspace, nil
	}
	migrated, err := cloneWorkspace(workspace)
	if err != nil {
		return workspace, err
	}
	migrated = migrateMissingRowsBeforeCurrent(migrated)
	for documentIndex := range migrated.Documents {
		document := &migrated.Documents[documentIndex]
		for columnIndex := range document.Columns {
			column := &document.Columns[columnIndex]
			where := column.Source.Aggregate
			if column.Source.Kind != SourceAggregate || where == nil || where.Where == nil {
				continue
			}
			occurrence := findRoute(&document.Route, column.OccurrenceID)
			if occurrence == nil {
				return workspace, fmt.Errorf("documents[%d].columns[%d]: legacy aggregate predicate references unknown occurrence %q", documentIndex, columnIndex, column.OccurrenceID)
			}
			candidate, ok, ambiguous := legacyContributorCandidate(catalog, occurrence.ResourceType, where.Where.Path)
			if ambiguous {
				return workspace, fmt.Errorf("documents[%d].columns[%d]: legacy aggregate predicate path %q matches multiple catalog candidates", documentIndex, columnIndex, where.Where.Path)
			}
			if !ok {
				return workspace, fmt.Errorf("documents[%d].columns[%d]: legacy aggregate predicate path %q is not present in the pinned catalog", documentIndex, columnIndex, where.Where.Path)
			}
			predicate := ContributorPredicate{CandidateID: candidate.ID, Operator: ContributorEquals}
			kind := contributorKindForCandidate(occurrence.ResourceType, candidate)
			if kind == ContributorValueKind("") {
				return workspace, fmt.Errorf("documents[%d].columns[%d]: legacy aggregate predicate path %q has unsupported catalog value type", documentIndex, columnIndex, where.Where.Path)
			}
			value := where.Where.Equals
			if strings.TrimSpace(value) == "" {
				predicate.Operator = ContributorExists
			} else if kind == ContributorValueCode {
				predicate.Value = &ContributorValue{Kind: kind, Code: &ContributorCode{Code: value}}
			} else {
				predicate.Value = &ContributorValue{Kind: kind, String: &value}
			}
			if candidate.Repeated || len(candidate.RepeatedBoundaries) != 0 {
				predicate.Quantifier = ContributorAny
			}
			if err := predicate.Validate(); err != nil {
				return workspace, fmt.Errorf("documents[%d].columns[%d]: legacy aggregate predicate cannot be represented: %w", documentIndex, columnIndex, err)
			}
			column.Contributor = &predicate
			where.Where = nil
			decision := fmt.Sprintf("semantics-v4:aggregate-where-to-contributor:%s:%s:%s", document.Output.ID, column.Column, candidate.ID)
			if !contains(migrated.MigrationDecisions, decision) {
				migrated.MigrationDecisions = append(migrated.MigrationDecisions, decision)
			}
		}
	}
	if migrated.SemanticsVersion < contributorSemanticsVersion {
		migrated.SemanticsVersion = contributorSemanticsVersion
	}
	return migrated, nil
}

func workspaceHasLegacyContributors(workspace Workspace) bool {
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Source.Kind == SourceAggregate && column.Source.Aggregate != nil && column.Source.Aggregate.Where != nil {
				return true
			}
		}
	}
	return false
}

func legacyContributorCandidate(catalog CatalogSnapshot, resourceType, path string) (CatalogCandidate, bool, bool) {
	want := strings.TrimPrefix(strings.TrimSpace(path), "root.")
	want = fhirschema.CanonicalizePath(want)
	var found CatalogCandidate
	count := 0
	for _, candidate := range catalog.Candidates {
		if strings.TrimSpace(candidate.ID) == "" {
			continue
		}
		if node, ok := catalogNode(catalog, candidate.NodeID); !ok || node.ResourceType != resourceType {
			continue
		}
		candidatePath := strings.TrimPrefix(strings.TrimSpace(candidate.FieldPath), "root.")
		candidatePath = fhirschema.CanonicalizePath(candidatePath)
		if candidatePath != want {
			continue
		}
		found = candidate
		count++
	}
	return found, count == 1, count > 1
}

func contributorKindForCandidate(resourceType string, candidate CatalogCandidate) ContributorValueKind {
	logicalType := strings.ToLower(strings.TrimSpace(candidate.LogicalType))
	if logicalType == "code" || strings.HasSuffix(strings.ToLower(strings.TrimPrefix(candidate.FieldPath, "root.")), ".code") || strings.EqualFold(strings.TrimPrefix(candidate.FieldPath, "root."), "code") {
		return ContributorValueCode
	}
	if logicalType == "string" || logicalType == "token" || logicalType == "id" || logicalType == "uri" || logicalType == "url" || logicalType == "canonical" {
		return ContributorString
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata(resourceType, strings.TrimPrefix(candidate.FieldPath, "root."))
	if ok {
		if metadata.Primitive == fhirschema.PrimitiveString {
			return ContributorString
		}
	}
	return ""
}

// MigrateLosslessDefaults upgrades mutable workspaces to current semantics. Repeated field
// repetition can migrate to INDEXED when the catalog proves it. Related
// resource FIRST remains an explicit, unacknowledged lossy policy. Published
// receipts are immutable and never pass through this migration.
func MigrateLosslessDefaults(workspace Workspace, catalog CatalogSnapshot) Workspace {
	if workspace.SemanticsVersion >= CurrentSemanticsVersion && !workspaceHasLegacyContributors(workspace) {
		return workspace
	}
	// Install the v6 root-record meaning before the current version is assigned.
	workspace = migrateMissingRowsBeforeCurrent(workspace)
	legacyContributors := workspaceHasLegacyContributors(workspace)
	workspace = cloneWorkspaceForMigration(workspace)
	nodes := make(map[string]CatalogNode, len(catalog.Nodes))
	for _, node := range catalog.Nodes {
		nodes[node.ID] = node
	}
	for documentIndex := range workspace.Documents {
		document := &workspace.Documents[documentIndex]
		for columnIndex := range document.Columns {
			column := &document.Columns[columnIndex]
			if column.Source.Kind != SourceField || column.Source.Field == nil {
				continue
			}
			// Columns are copied above, but their nested source payloads are
			// pointers. Clone the payload before adding migration metadata so
			// callers retain an immutable pre-migration workspace value.
			column.Source = column.Source.Normalized()
			mode := strings.ToUpper(strings.TrimSpace(column.Source.Field.ProjectionMode))
			if mode == "" {
				mode = "FIRST"
			}
			if column.OccurrenceID != RootOccurrenceID && (mode == "VALUE" || mode == "FIRST" || mode == "INDEXED") {
				if column.Source.Field.RelatedSelection == nil {
					column.Source.Field.RelatedSelection = &RelatedSelection{Kind: "first-by-resource-key"}
				}
				decision := fmt.Sprintf("semantics-v3:related-first-requires-ack:%s:%s", document.Output.ID, column.Column)
				if !contains(workspace.MigrationDecisions, decision) {
					workspace.MigrationDecisions = append(workspace.MigrationDecisions, decision)
				}
				continue
			}
			if mode != "FIRST" {
				continue
			}
			// Semantics v2 made FIRST explicit. Only pre-v2 persisted drafts
			// may reinterpret a repeated default FIRST as INDEXED.
			if workspace.SemanticsVersion >= 2 {
				continue
			}
			occurrence := findRoute(&document.Route, column.OccurrenceID)
			if occurrence == nil {
				continue
			}
			fieldPath := strings.TrimPrefix(column.Source.Field.Path, "root.")
			for _, candidate := range catalog.Candidates {
				if nodes[candidate.NodeID].ResourceType != occurrence.ResourceType || strings.TrimPrefix(candidate.FieldPath, "root.") != fieldPath || len(candidate.RepeatedBoundaries) == 0 || !contains(candidate.ProjectionModes, "INDEXED") {
					continue
				}
				column.Source.Field.ProjectionMode = "INDEXED"
				workspace.MigrationDecisions = append(workspace.MigrationDecisions, fmt.Sprintf("semantics-v2:repeated-first-to-indexed:%s:%s", document.Output.ID, column.Column))
				break
			}
		}
	}
	if workspace.SemanticsVersion < structuralFieldSemanticsVersion {
		migrateRawStructuralColumns(&workspace)
	}
	// A legacy aggregate predicate cannot be interpreted without the pinned
	// catalog. MigrateLegacyContributors is the required boundary for it; do
	// not silently claim v4 while retaining the private v3 meaning.
	if !legacyContributors {
		workspace.SemanticsVersion = CurrentSemanticsVersion
	}
	return workspace
}

func cloneWorkspaceForMigration(workspace Workspace) Workspace {
	workspace.MigrationDecisions = append([]string(nil), workspace.MigrationDecisions...)
	workspace.Documents = append([]Document(nil), workspace.Documents...)
	for documentIndex := range workspace.Documents {
		document := &workspace.Documents[documentIndex]
		document.Columns = append([]Column(nil), document.Columns...)
		document.FixedFilters = append([]FixedFilter(nil), document.FixedFilters...)
		document.Actions = append([]Action(nil), document.Actions...)
		for actionIndex := range document.Actions {
			document.Actions[actionIndex].Columns = append([]ActionColumn(nil), document.Actions[actionIndex].Columns...)
		}
	}
	if workspace.SharedFilters != nil {
		filters := make(map[string][]SharedFilterBinding, len(workspace.SharedFilters))
		for name, bindings := range workspace.SharedFilters {
			filters[name] = append([]SharedFilterBinding(nil), bindings...)
		}
		workspace.SharedFilters = filters
	}
	return workspace
}

func migrateRawStructuralColumns(workspace *Workspace) {
	removedByOutput := make(map[string]map[string]bool)
	for documentIndex := range workspace.Documents {
		document := &workspace.Documents[documentIndex]
		removed := make(map[string]bool)
		kept := document.Columns[:0]
		for _, column := range document.Columns {
			if column.Source.Kind != SourceField || column.Source.Field == nil {
				kept = append(kept, column)
				continue
			}
			occurrence := findRoute(&document.Route, column.OccurrenceID)
			resourceType := ""
			if occurrence != nil {
				resourceType = occurrence.ResourceType
			}
			path := strings.TrimPrefix(strings.TrimSpace(column.Source.Field.Path), "root.")
			if directFieldSourceEligible(resourceType, path) {
				kept = append(kept, column)
				continue
			}
			removed[column.Column] = true
			decision := fmt.Sprintf("semantics-v10:remove-raw-structural-column:%s:%s:%s:%s", document.Output.ID, column.Column, resourceType, path)
			if !contains(workspace.MigrationDecisions, decision) {
				workspace.MigrationDecisions = append(workspace.MigrationDecisions, decision)
			}
		}
		document.Columns = kept
		if len(removed) == 0 {
			continue
		}
		removedByOutput[document.Output.ID] = removed
		pruneDocumentDependencies(workspace, document, removed)
	}
	for name, bindings := range workspace.SharedFilters {
		kept := bindings[:0]
		for _, binding := range bindings {
			if removedByOutput[binding.OutputID][binding.Column] {
				decision := fmt.Sprintf("semantics-v10:remove-dependent-shared-filter:%s:%s:%s", name, binding.OutputID, binding.Column)
				if !contains(workspace.MigrationDecisions, decision) {
					workspace.MigrationDecisions = append(workspace.MigrationDecisions, decision)
				}
				continue
			}
			kept = append(kept, binding)
		}
		if len(kept) == 0 {
			delete(workspace.SharedFilters, name)
		} else {
			workspace.SharedFilters[name] = kept
		}
	}
}

func pruneDocumentDependencies(workspace *Workspace, document *Document, removed map[string]bool) {
	filters := document.FixedFilters[:0]
	for _, filter := range document.FixedFilters {
		if removed[filter.Column] {
			decision := fmt.Sprintf("semantics-v10:remove-dependent-fixed-filter:%s:%s", document.Output.ID, filter.Column)
			if !contains(workspace.MigrationDecisions, decision) {
				workspace.MigrationDecisions = append(workspace.MigrationDecisions, decision)
			}
			continue
		}
		filters = append(filters, filter)
	}
	document.FixedFilters = filters
	for actionIndex := range document.Actions {
		columns := document.Actions[actionIndex].Columns[:0]
		for _, binding := range document.Actions[actionIndex].Columns {
			if removed[binding.Column] {
				decision := fmt.Sprintf("semantics-v10:remove-dependent-action-column:%s:%d:%s", document.Output.ID, actionIndex, binding.Column)
				if !contains(workspace.MigrationDecisions, decision) {
					workspace.MigrationDecisions = append(workspace.MigrationDecisions, decision)
				}
				continue
			}
			columns = append(columns, binding)
		}
		document.Actions[actionIndex].Columns = columns
	}
	if tableShapeReferencesAny(document.TableShape, removed) {
		document.TableShape = nil
		decision := fmt.Sprintf("semantics-v10:remove-dependent-table-shape:%s", document.Output.ID)
		if !contains(workspace.MigrationDecisions, decision) {
			workspace.MigrationDecisions = append(workspace.MigrationDecisions, decision)
		}
	}
}

func tableShapeReferencesAny(shape *TableShape, columns map[string]bool) bool {
	if shape == nil {
		return false
	}
	if reshape := shape.Reshape; reshape != nil {
		if reshape.Pivot != nil {
			if columns[reshape.Pivot.CategoryColumn] || columns[reshape.Pivot.ValueColumn] {
				return true
			}
			for _, column := range reshape.Pivot.GroupKeys {
				if columns[column] {
					return true
				}
			}
		}
		if reshape.Unpivot != nil {
			for _, input := range reshape.Unpivot.Inputs {
				if columns[input.Column] {
					return true
				}
			}
		}
	}
	for _, derived := range shape.Derived {
		for _, operand := range []ArithmeticOperand{derived.Left, derived.Right} {
			if operand.Kind == "COLUMN" && columns[operand.Column] {
				return true
			}
		}
	}
	return false
}

func directFieldSourceEligible(resourceType, path string) bool {
	index, err := fhirschema.GeneratedIndex()
	if err != nil {
		return false
	}
	classification, err := semanticfhir.ClassifyDirectField(index, fhirschema.DefinitionName(resourceType), path)
	return err == nil && classification.Eligible
}

func validateDirectFieldSources(workspace Workspace) error {
	for documentIndex, document := range workspace.Documents {
		for columnIndex, column := range document.Columns {
			if column.Source.Kind != SourceField || column.Source.Field == nil {
				continue
			}
			occurrence := findRoute(&document.Route, column.OccurrenceID)
			resourceType := ""
			if occurrence != nil {
				resourceType = occurrence.ResourceType
			}
			path := strings.TrimPrefix(strings.TrimSpace(column.Source.Field.Path), "root.")
			if !directFieldSourceEligible(resourceType, path) {
				return fmt.Errorf("RAW_STRUCTURAL_FIELD_SOURCE: documents[%d].columns[%d] %s.%s must use a semantic source or a scalar member", documentIndex, columnIndex, resourceType, path)
			}
		}
	}
	return nil
}
