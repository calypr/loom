package authoringv2

import (
	"fmt"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func MigrateAggregateTemporalPolicy(workspace Workspace) Workspace {
	if workspace.SemanticsVersion == aggregateTemporalPolicySemanticsVersion-1 {
		workspace.SemanticsVersion = aggregateTemporalPolicySemanticsVersion
	}
	return workspace
}

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

func PrepareWorkspaceForCompilation(workspace Workspace, catalog CatalogSnapshot) (Workspace, error) {
	prepared, err := cloneWorkspace(workspace)
	if err != nil {
		return Workspace{}, err
	}
	prepared, err = MigrateLegacyContributors(prepared, catalog)
	if err != nil {
		return Workspace{}, err
	}
	prepared = MigrateLosslessDefaults(prepared, catalog).NormalizePresentationOrders()
	return prepared, nil
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

func MigrateLosslessDefaults(workspace Workspace, catalog CatalogSnapshot) Workspace {
	if workspace.SemanticsVersion >= CurrentSemanticsVersion && !workspaceHasLegacyContributors(workspace) {
		return workspace
	}
	workspace = migrateMissingRowsBeforeCurrent(workspace)
	legacyContributors := workspaceHasLegacyContributors(workspace)
	workspace.Documents = append([]Document(nil), workspace.Documents...)
	workspace.MigrationDecisions = append([]string(nil), workspace.MigrationDecisions...)
	for index := range workspace.Documents {
		workspace.Documents[index].Columns = append([]Column(nil), workspace.Documents[index].Columns...)
	}
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
	if !legacyContributors {
		workspace.SemanticsVersion = CurrentSemanticsVersion
	}
	return workspace
}
