package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

// ResolveWorkspaceInterpretations is the pure B06 resolver. The caller owns
// loading exact revisions; this function only interprets the retained
// capability snapshot and revision map, so it has no moving-head lookup.
func ResolveWorkspaceInterpretations(project string, workspace authoringv2.Workspace, snapshot capability.Snapshot, revisions map[explorer.InterpretationRevisionID]explorer.InterpretationRevision) (ResolvedInputs, error) {
	project = projectid.Canonical(project)
	inputs := ResolvedInputs{}
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Interpretation == nil || column.Interpretation.Kind != authoringv2.FeatureInterpretationPinned {
				continue
			}
			if column.Interpretation.Pinned == nil {
				return inputs, fmt.Errorf("pinned interpretation for %s/%s requires a revision", document.Output.ID, column.Column)
			}
			id, err := explorer.NewInterpretationRevisionID(strings.TrimSpace(column.Interpretation.Pinned.RevisionID))
			if err != nil {
				return inputs, fmt.Errorf("interpretation %s/%s: %w", document.Output.ID, column.Column, err)
			}
			revision, ok := revisions[id]
			if !ok {
				return inputs, fmt.Errorf("interpretation revision %q was not resolved", id)
			}
			if err := revision.Validate(); err != nil {
				return inputs, fmt.Errorf("interpretation revision %q is invalid: %w", id, err)
			}
			if revision.ID != id {
				return inputs, fmt.Errorf("interpretation revision %q returned as %q", id, revision.ID)
			}
			if projectid.Canonical(revision.Project) != project {
				return inputs, fmt.Errorf("interpretation revision %q belongs to a different project", id)
			}

			candidate, err := structuralCandidate(document, column, snapshot)
			if err != nil {
				return inputs, fmt.Errorf("interpretation %q for %s/%s: %w", id, document.Output.ID, column.Column, err)
			}
			if !revision.Applicability.Matches(candidate) {
				return inputs, fmt.Errorf("interpretation revision %q is not applicable to %s/%s", id, document.Output.ID, column.Column)
			}
			rule, err := revision.SelectRule(candidate)
			if err != nil {
				return inputs, fmt.Errorf("interpretation revision %q for %s/%s: %w", id, document.Output.ID, column.Column, err)
			}
			inputs.Interpretations = append(inputs.Interpretations, ResolvedInterpretation{
				OutputID: document.Output.ID, Column: column.Column, OccurrenceID: column.OccurrenceID,
				Revision: revision, SelectedRuleID: rule.ID, Definition: cloneInterpretationDefinition(rule.Definition),
			})
		}
	}
	return inputs.Canonical(), nil
}

func structuralCandidate(document authoringv2.Document, column authoringv2.Column, snapshot capability.Snapshot) (explorer.InterpretationStructuralCandidate, error) {
	occurrences, _, err := resolveSemanticRoute(document, snapshot)
	if err != nil {
		return explorer.InterpretationStructuralCandidate{}, fmt.Errorf("resolve occurrence %q: %w", column.OccurrenceID, err)
	}
	occurrence, ok := occurrences[column.OccurrenceID]
	if !ok {
		return explorer.InterpretationStructuralCandidate{}, fmt.Errorf("occurrence %q is not present in route", column.OccurrenceID)
	}
	resourceType := occurrence.graph.ResourceType
	paths := interpretationSourcePaths(column.Source)
	capabilities := make([]capability.Candidate, 0, 1)
	for _, candidate := range snapshot.Candidates {
		if candidate.NodeID != occurrence.graph.ID || candidate.ResourceType != resourceType || (len(paths) > 0 && !containsString(paths, normalizeFieldPath(candidate.FieldPath))) {
			continue
		}
		capabilities = append(capabilities, candidate)
	}
	if len(capabilities) == 0 {
		return explorer.InterpretationStructuralCandidate{}, fmt.Errorf("source is not present in the exact capability snapshot")
	}
	if len(capabilities) > 1 {
		return explorer.InterpretationStructuralCandidate{}, fmt.Errorf("source is ambiguous in the exact capability snapshot")
	}
	capabilityCandidate := capabilities[0]
	concepts := matchingConceptCandidates(capabilityCandidate.ConceptCandidates, column.Source)
	if len(concepts) > 1 {
		return explorer.InterpretationStructuralCandidate{}, fmt.Errorf("source concept is ambiguous in the exact capability snapshot")
	}
	if len(concepts) == 1 {
		concept := concepts[0]
		return explorer.InterpretationStructuralCandidate{
			ResourceType:  firstNonEmpty(concept.SourceResourceType, capabilityCandidate.ResourceType),
			SourceProfile: concept.SourceProfile, SourceCanonical: concept.SourceCanonical, OwningScope: concept.OwningScope,
			System: concept.System, Code: concept.Code, ExtensionURLPath: append([]string(nil), concept.ExtensionURLPath...),
			LogicalType: firstNonEmpty(concept.LogicalType, capabilityCandidate.LogicalType), Cardinality: capabilityCandidate.Cardinality,
			SchemaDigest: snapshot.Identity.SchemaDigest,
		}, nil
	}
	return explorer.InterpretationStructuralCandidate{
		ResourceType: capabilityCandidate.ResourceType, LogicalType: capabilityCandidate.LogicalType,
		Cardinality: capabilityCandidate.Cardinality, System: sourceSystem(column.Source), Code: sourceCode(column.Source),
		ExtensionURLPath: sourceExtensionPath(column.Source), SchemaDigest: snapshot.Identity.SchemaDigest,
	}, nil
}

func interpretationSourcePaths(source authoringv2.ColumnSource) []string {
	paths := []string{}
	if path := source.FieldPath(); path != "" {
		paths = append(paths, normalizeFieldPath(path))
	}
	if source.Lookup != nil {
		if source.Lookup.Binding != nil {
			paths = append(paths, normalizeFieldPath(source.Lookup.Binding.ValuePath))
		}
		if source.Lookup.Extension != nil {
			paths = append(paths, normalizeFieldPath(source.Lookup.Extension.ValuePath))
		}
	}
	return uniqueStrings(paths)
}

func matchingConceptCandidates(values []capability.ConceptCandidate, source authoringv2.ColumnSource) []capability.ConceptCandidate {
	paths := interpretationSourcePaths(source)
	system, code := sourceSystem(source), sourceCode(source)
	extensionPath := sourceExtensionPath(source)
	matched := make([]capability.ConceptCandidate, 0, len(values))
	for _, value := range values {
		if len(paths) > 0 && !containsString(paths, normalizeFieldPath(value.SourcePath)) {
			continue
		}
		if system != "" && value.System != system {
			continue
		}
		if code != "" && value.Code != code {
			continue
		}
		if len(extensionPath) > 0 && !sameStrings(extensionPath, value.ExtensionURLPath) {
			continue
		}
		matched = append(matched, value)
	}
	return matched
}

func sourceSystem(source authoringv2.ColumnSource) string {
	if source.Lookup != nil && source.Lookup.Key != nil {
		return strings.TrimSpace(source.Lookup.Key.System)
	}
	return ""
}

func sourceCode(source authoringv2.ColumnSource) string {
	if source.Lookup != nil && source.Lookup.Key != nil {
		return strings.TrimSpace(source.Lookup.Key.Code)
	}
	return ""
}

func sourceExtensionPath(source authoringv2.ColumnSource) []string {
	if source.Lookup != nil && source.Lookup.Extension != nil {
		return append([]string(nil), source.Lookup.Extension.URLPath...)
	}
	return nil
}

func normalizeFieldPath(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "root.")
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if strings.TrimSpace(left[index]) != strings.TrimSpace(right[index]) {
			return false
		}
	}
	return true
}
