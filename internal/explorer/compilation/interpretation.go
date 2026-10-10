package compilation

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/projectid"
)

type InterpretationCandidateResolutionState string

const (
	InterpretationCandidateReady       InterpretationCandidateResolutionState = "READY"
	InterpretationCandidateMissing     InterpretationCandidateResolutionState = "MISSING"
	InterpretationCandidateAmbiguous   InterpretationCandidateResolutionState = "AMBIGUOUS"
	InterpretationCandidateUnsupported InterpretationCandidateResolutionState = "UNSUPPORTED"
)

// InterpretationCandidateMatch is the semantic payload available only from
// a READY resolution. Candidate IDs are copied out of the exact snapshot.
type InterpretationCandidateMatch struct {
	CapabilityCandidateIDs []string
	StructuralCandidate    explorer.InterpretationStructuralCandidate
}

// InterpretationCandidateResolution is a closed result. Implementations are
// private so callers can inspect a READY match or an unavailable reason, but
// cannot construct a state with the wrong payload.
type InterpretationCandidateResolution interface {
	State() InterpretationCandidateResolutionState
	Reason() string
	Match() (InterpretationCandidateMatch, bool)
	interpretationCandidateResolution()
}

type readyInterpretationCandidate struct{ match InterpretationCandidateMatch }

func (readyInterpretationCandidate) State() InterpretationCandidateResolutionState {
	return InterpretationCandidateReady
}
func (readyInterpretationCandidate) Reason() string { return "" }
func (ready readyInterpretationCandidate) Match() (InterpretationCandidateMatch, bool) {
	match := ready.match
	match.CapabilityCandidateIDs = append([]string(nil), match.CapabilityCandidateIDs...)
	match.StructuralCandidate.ExtensionURLPath = append([]string(nil), match.StructuralCandidate.ExtensionURLPath...)
	return match, true
}
func (readyInterpretationCandidate) interpretationCandidateResolution() {}

type missingInterpretationCandidate struct{ reason string }

func (missingInterpretationCandidate) State() InterpretationCandidateResolutionState {
	return InterpretationCandidateMissing
}
func (missing missingInterpretationCandidate) Reason() string { return missing.reason }
func (missingInterpretationCandidate) Match() (InterpretationCandidateMatch, bool) {
	return InterpretationCandidateMatch{}, false
}
func (missingInterpretationCandidate) interpretationCandidateResolution() {}

type ambiguousInterpretationCandidate struct{ reason string }

func (ambiguousInterpretationCandidate) State() InterpretationCandidateResolutionState {
	return InterpretationCandidateAmbiguous
}
func (ambiguous ambiguousInterpretationCandidate) Reason() string { return ambiguous.reason }
func (ambiguousInterpretationCandidate) Match() (InterpretationCandidateMatch, bool) {
	return InterpretationCandidateMatch{}, false
}
func (ambiguousInterpretationCandidate) interpretationCandidateResolution() {}

type unsupportedInterpretationCandidate struct{ reason string }

func (unsupportedInterpretationCandidate) State() InterpretationCandidateResolutionState {
	return InterpretationCandidateUnsupported
}
func (unsupported unsupportedInterpretationCandidate) Reason() string { return unsupported.reason }
func (unsupportedInterpretationCandidate) Match() (InterpretationCandidateMatch, bool) {
	return InterpretationCandidateMatch{}, false
}
func (unsupportedInterpretationCandidate) interpretationCandidateResolution() {}

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

			resolution, err := ResolveInterpretationCandidate(document, column, snapshot)
			if err != nil {
				return inputs, fmt.Errorf("interpretation %q for %s/%s: %w", id, document.Output.ID, column.Column, err)
			}
			match, ready := resolution.Match()
			if !ready {
				return inputs, fmt.Errorf("interpretation %q for %s/%s: source resolution is %s: %s", id, document.Output.ID, column.Column, resolution.State(), resolution.Reason())
			}
			candidate := match.StructuralCandidate
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

// ResolveInterpretationCandidate matches one configured source against an
// exact capability snapshot. Invalid routes are request-level errors; valid
// routes with unresolved sources return a closed per-column state.
func ResolveInterpretationCandidate(document authoringv2.Document, column authoringv2.Column, snapshot capability.Snapshot) (InterpretationCandidateResolution, error) {
	occurrences, _, err := resolveSemanticRoute(document, snapshot)
	if err != nil {
		return nil, fmt.Errorf("resolve occurrence %q: %w", column.OccurrenceID, err)
	}
	occurrence, ok := occurrences[column.OccurrenceID]
	if !ok {
		return nil, fmt.Errorf("occurrence %q is not present in route", column.OccurrenceID)
	}
	resourceType := occurrence.graph.ResourceType
	paths := interpretationSourcePaths(column.Source)
	if len(paths) == 0 {
		return unsupportedInterpretationCandidate{reason: "source does not expose a supported FHIR field path"}, nil
	}
	capabilities := make([]capability.Candidate, 0, 1)
	for _, candidate := range snapshot.Candidates {
		if candidate.NodeID != occurrence.graph.ID || candidate.ResourceType != resourceType || !containsString(paths, normalizeFieldPath(candidate.FieldPath)) {
			continue
		}
		capabilities = append(capabilities, candidate)
	}
	if len(capabilities) == 0 {
		return missingInterpretationCandidate{reason: "source is not present in the exact capability snapshot"}, nil
	}
	if len(capabilities) > 1 {
		return ambiguousInterpretationCandidate{reason: "source is ambiguous in the exact capability snapshot"}, nil
	}
	capabilityCandidate := capabilities[0]
	concepts := matchingConceptCandidates(capabilityCandidate.ConceptCandidates, column.Source)
	if len(concepts) > 1 {
		return ambiguousInterpretationCandidate{reason: "source concept is ambiguous in the exact capability snapshot"}, nil
	}
	var candidate explorer.InterpretationStructuralCandidate
	if len(concepts) == 1 {
		concept := concepts[0]
		candidate = explorer.InterpretationStructuralCandidate{
			ResourceType:  firstNonEmpty(concept.SourceResourceType, capabilityCandidate.ResourceType),
			SourceProfile: concept.SourceProfile, SourceCanonical: concept.SourceCanonical, OwningScope: concept.OwningScope,
			System: concept.System, Code: concept.Code, ExtensionURLPath: append([]string(nil), concept.ExtensionURLPath...),
			LogicalType: firstNonEmpty(concept.LogicalType, capabilityCandidate.LogicalType), Cardinality: capabilityCandidate.Cardinality,
			SchemaDigest: snapshot.Identity.SchemaDigest,
		}
	} else {
		candidate = explorer.InterpretationStructuralCandidate{
			ResourceType: capabilityCandidate.ResourceType, LogicalType: capabilityCandidate.LogicalType,
			Cardinality: capabilityCandidate.Cardinality, System: sourceSystem(column.Source), Code: sourceCode(column.Source),
			ExtensionURLPath: sourceExtensionPath(column.Source), SchemaDigest: snapshot.Identity.SchemaDigest,
		}
	}
	return readyInterpretationCandidate{match: InterpretationCandidateMatch{
		CapabilityCandidateIDs: []string{capabilityCandidate.ID}, StructuralCandidate: candidate,
	}}, nil
}

// structuralCandidate adapts the closed resolution for compiler validation.
// All source and concept matching remains in ResolveInterpretationCandidate.
func structuralCandidate(document authoringv2.Document, column authoringv2.Column, snapshot capability.Snapshot) (explorer.InterpretationStructuralCandidate, error) {
	resolution, err := ResolveInterpretationCandidate(document, column, snapshot)
	if err != nil {
		return explorer.InterpretationStructuralCandidate{}, err
	}
	match, ready := resolution.Match()
	if !ready {
		return explorer.InterpretationStructuralCandidate{}, fmt.Errorf("source resolution is %s: %s", resolution.State(), resolution.Reason())
	}
	return match.StructuralCandidate, nil
}

func interpretationSourcePaths(source authoringv2.ColumnSource) []string {
	paths := []string{}
	if path := source.FieldPath(); path != "" {
		paths = append(paths, normalizeFieldPath(path))
	}
	if source.Lookup != nil {
		if source.Lookup.Extension != nil {
			path := strings.Trim(strings.TrimSpace(source.Lookup.Extension.OwnerPath)+"."+strings.TrimSpace(source.Lookup.Extension.ValuePath), ".")
			paths = append(paths, normalizeFieldPath(path))
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
	if source.Lookup == nil {
		return ""
	}
	if source.Lookup.Identifier != nil {
		return strings.TrimSpace(source.Lookup.Identifier.SystemURI)
	}
	if source.Lookup.Key != nil {
		return strings.TrimSpace(source.Lookup.Key.System)
	}
	if source.Kind == authoringv2.SourceIdentifierBySystem {
		return strings.TrimSpace(source.Lookup.Match)
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
