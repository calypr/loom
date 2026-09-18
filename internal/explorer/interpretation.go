package explorer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

// Interpretation IDs are deliberately opaque to callers.  They are still
// constrained to Arango-safe, URL-safe values so a malformed ID cannot become
// a collection key or an ambiguous share URL.
type InterpretationLibraryID string
type InterpretationRevisionID string
type InterpretationRuleID string
type InterpretationContentDigest string
type InterpretationPriority int32

var interpretationIDPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.:-]{0,127}$`)
var interpretationDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

var (
	ErrInvalidInterpretationID        = errors.New("invalid interpretation id")
	ErrInvalidInterpretationDigest    = errors.New("invalid interpretation content digest")
	ErrInvalidInterpretationProject   = errors.New("invalid interpretation project")
	ErrAmbiguousMapping               = errors.New("AMBIGUOUS_MAPPING")
	ErrNoInterpretationMatch          = errors.New("interpretation has no matching rule")
	ErrInterpretationParentConflict   = errors.New("interpretation library parent conflict")
	ErrImmutableInterpretation        = errors.New("interpretation revision content is immutable")
	ErrInterpretationIdentityMismatch = errors.New("interpretation revision identity mismatch")
)

// InterpretationLibrary is the mutable authoring head for one project and
// library.  Revisions never mutate; only this pointer advances.
type InterpretationLibrary struct {
	ID             InterpretationLibraryID     `json:"id"`
	Project        string                      `json:"project"`
	HeadRevisionID InterpretationRevisionID    `json:"headRevisionId,omitempty"`
	HeadDigest     InterpretationContentDigest `json:"headDigest,omitempty"`
	CreatedAt      time.Time                   `json:"createdAt"`
	UpdatedAt      time.Time                   `json:"updatedAt"`
}

type InterpretationRevision struct {
	ID               InterpretationRevisionID     `json:"id"`
	Project          string                       `json:"project"`
	LibraryID        InterpretationLibraryID      `json:"libraryId"`
	ParentRevisionID *InterpretationRevisionID    `json:"parentRevisionId,omitempty"`
	ParentDigest     *InterpretationContentDigest `json:"parentDigest,omitempty"`
	ContentDigest    InterpretationContentDigest  `json:"contentDigest"`
	Applicability    InterpretationApplicability  `json:"applicability"`
	Rules            []InterpretationRule         `json:"rules"`
	Author           string                       `json:"author"`
	Explanation      string                       `json:"explanation"`
	CreatedAt        time.Time                    `json:"createdAt"`
}

type InterpretationApplicability struct {
	ResourceTypes   []string `json:"resourceTypes,omitempty"`
	SourceProfiles  []string `json:"sourceProfiles,omitempty"`
	SourceCanonical []string `json:"sourceCanonical,omitempty"`
	LogicalTypes    []string `json:"logicalTypes,omitempty"`
	Cardinalities   []string `json:"cardinalities,omitempty"`
	SchemaDigests   []string `json:"schemaDigests,omitempty"`
}

// InterpretationStructuralMatch is a closed conjunction over observed
// catalog identity.  Empty dimensions are wildcards.
type InterpretationStructuralMatch struct {
	ResourceType     string   `json:"resourceType,omitempty"`
	SourceProfile    string   `json:"sourceProfile,omitempty"`
	SourceCanonical  string   `json:"sourceCanonical,omitempty"`
	OwningScope      string   `json:"owningScope,omitempty"`
	System           string   `json:"system,omitempty"`
	Code             string   `json:"code,omitempty"`
	ExtensionURLPath []string `json:"extensionUrlPath,omitempty"`
	LogicalType      string   `json:"logicalType,omitempty"`
	Cardinality      string   `json:"cardinality,omitempty"`
}

// The candidate is intentionally a separate type: a rule can never smuggle
// an arbitrary map or query expression into the matcher.
type InterpretationStructuralCandidate struct {
	ResourceType     string
	SourceProfile    string
	SourceCanonical  string
	OwningScope      string
	System           string
	Code             string
	ExtensionURLPath []string
	LogicalType      string
	Cardinality      string
	SchemaDigest     string
}

type InterpretationRule struct {
	ID         InterpretationRuleID            `json:"id"`
	Priority   *InterpretationPriority         `json:"priority,omitempty"`
	Match      InterpretationStructuralMatch   `json:"match"`
	Definition InterpretationFeatureDefinition `json:"definition"`
}

type InterpretationFeatureDefinition struct {
	Source      authoringv2.ColumnSource          `json:"source"`
	Contributor *authoringv2.ContributorPredicate `json:"contributor,omitempty"`
}

// ResolvedInterpretation is the immutable, occurrence-scoped result of
// resolving one pinned feature against one exact capability snapshot. It is
// kept in the explorer domain so receipts can freeze it without creating an
// import cycle with the compilation package.
type ResolvedInterpretation struct {
	OutputID       string                          `json:"outputId"`
	Column         string                          `json:"column"`
	OccurrenceID   string                          `json:"occurrenceId"`
	Revision       InterpretationRevision          `json:"revision"`
	SelectedRuleID InterpretationRuleID            `json:"selectedRuleId"`
	Definition     InterpretationFeatureDefinition `json:"definition"`
}

func NewInterpretationLibraryID(raw string) (InterpretationLibraryID, error) {
	if err := validateInterpretationID(raw, "library"); err != nil {
		return "", err
	}
	return InterpretationLibraryID(raw), nil
}

func NewInterpretationRevisionID(raw string) (InterpretationRevisionID, error) {
	if err := validateInterpretationID(raw, "revision"); err != nil {
		return "", err
	}
	return InterpretationRevisionID(raw), nil
}

func NewInterpretationRuleID(raw string) (InterpretationRuleID, error) {
	if err := validateInterpretationID(raw, "rule"); err != nil {
		return "", err
	}
	return InterpretationRuleID(raw), nil
}

func NewInterpretationContentDigest(raw string) (InterpretationContentDigest, error) {
	if !interpretationDigestPattern.MatchString(raw) {
		return "", fmt.Errorf("%w: %q", ErrInvalidInterpretationDigest, raw)
	}
	return InterpretationContentDigest(raw), nil
}

func validateInterpretationID(raw, kind string) error {
	if strings.TrimSpace(raw) != raw || !interpretationIDPattern.MatchString(raw) {
		return fmt.Errorf("%w: invalid %s ID %q", ErrInvalidInterpretationID, kind, raw)
	}
	return nil
}

func canonicalInterpretationProject(raw string) (string, error) {
	project := projectid.Canonical(raw)
	if project == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(project, "\x00\r\n\t ") {
		return "", fmt.Errorf("%w: %q", ErrInvalidInterpretationProject, raw)
	}
	parts := strings.Split(project, "/")
	if len(parts) > 2 || (len(parts) == 2 && (parts[0] == "" || parts[1] == "")) {
		return "", fmt.Errorf("%w: %q", ErrInvalidInterpretationProject, raw)
	}
	return project, nil
}

// CanonicalInterpretationProject is the persistence-boundary project parser.
// It accepts the repository's legacy project spelling but always returns the
// canonical slash-separated identity.
func CanonicalInterpretationProject(raw string) (string, error) {
	return canonicalInterpretationProject(raw)
}

func (a InterpretationApplicability) canonical() (InterpretationApplicability, error) {
	var err error
	a.ResourceTypes, err = canonicalSet(a.ResourceTypes, "resourceTypes")
	if err != nil {
		return InterpretationApplicability{}, err
	}
	a.SourceProfiles, err = canonicalSet(a.SourceProfiles, "sourceProfiles")
	if err != nil {
		return InterpretationApplicability{}, err
	}
	a.SourceCanonical, err = canonicalSet(a.SourceCanonical, "sourceCanonical")
	if err != nil {
		return InterpretationApplicability{}, err
	}
	a.LogicalTypes, err = canonicalSet(a.LogicalTypes, "logicalTypes")
	if err != nil {
		return InterpretationApplicability{}, err
	}
	a.Cardinalities, err = canonicalSet(a.Cardinalities, "cardinalities")
	if err != nil {
		return InterpretationApplicability{}, err
	}
	a.SchemaDigests, err = canonicalSet(a.SchemaDigests, "schemaDigests")
	if err != nil {
		return InterpretationApplicability{}, err
	}
	return a, nil
}

func canonicalSet(values []string, field string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, fmt.Errorf("applicability.%s contains an empty value", field)
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func (m InterpretationStructuralMatch) canonical() (InterpretationStructuralMatch, error) {
	m.ResourceType = strings.TrimSpace(m.ResourceType)
	m.SourceProfile = strings.TrimSpace(m.SourceProfile)
	m.SourceCanonical = strings.TrimSpace(m.SourceCanonical)
	m.OwningScope = strings.TrimSpace(m.OwningScope)
	m.System = strings.TrimSpace(m.System)
	m.Code = strings.TrimSpace(m.Code)
	m.LogicalType = strings.TrimSpace(m.LogicalType)
	m.Cardinality = strings.TrimSpace(m.Cardinality)
	if (m.System == "") != (m.Code == "") {
		return InterpretationStructuralMatch{}, fmt.Errorf("match system and code must be supplied together")
	}
	if len(m.ExtensionURLPath) > 0 {
		path := make([]string, len(m.ExtensionURLPath))
		for i, value := range m.ExtensionURLPath {
			value = strings.TrimSpace(value)
			if value == "" {
				return InterpretationStructuralMatch{}, fmt.Errorf("match extensionUrlPath[%d] is empty", i)
			}
			path[i] = value
		}
		m.ExtensionURLPath = path
	}
	return m, nil
}

func (m InterpretationStructuralMatch) Matches(candidate InterpretationStructuralCandidate) bool {
	m, err := m.canonical()
	if err != nil {
		return false
	}
	candidate.ResourceType = strings.TrimSpace(candidate.ResourceType)
	candidate.SourceProfile = strings.TrimSpace(candidate.SourceProfile)
	candidate.SourceCanonical = strings.TrimSpace(candidate.SourceCanonical)
	candidate.OwningScope = strings.TrimSpace(candidate.OwningScope)
	candidate.System = strings.TrimSpace(candidate.System)
	candidate.Code = strings.TrimSpace(candidate.Code)
	candidate.LogicalType = strings.TrimSpace(candidate.LogicalType)
	candidate.Cardinality = strings.TrimSpace(candidate.Cardinality)
	return matchString(m.ResourceType, candidate.ResourceType) &&
		matchString(m.SourceProfile, candidate.SourceProfile) &&
		matchString(m.SourceCanonical, candidate.SourceCanonical) &&
		matchString(m.OwningScope, candidate.OwningScope) &&
		matchString(m.System, candidate.System) &&
		matchString(m.Code, candidate.Code) &&
		matchString(m.LogicalType, candidate.LogicalType) &&
		matchString(m.Cardinality, candidate.Cardinality) &&
		matchPath(m.ExtensionURLPath, candidate.ExtensionURLPath)
}

func matchString(want, got string) bool { return want == "" || want == got }

func matchPath(want, got []string) bool {
	if len(want) == 0 {
		return true
	}
	if len(want) > len(got) {
		return false
	}
	for i := range want {
		if strings.TrimSpace(want[i]) != strings.TrimSpace(got[i]) {
			return false
		}
	}
	return true
}

func (a InterpretationApplicability) Matches(candidate InterpretationStructuralCandidate) bool {
	return matchesSet(a.ResourceTypes, candidate.ResourceType) &&
		matchesSet(a.SourceProfiles, candidate.SourceProfile) &&
		matchesSet(a.SourceCanonical, candidate.SourceCanonical) &&
		matchesSet(a.LogicalTypes, candidate.LogicalType) &&
		matchesSet(a.Cardinalities, candidate.Cardinality) &&
		matchesSet(a.SchemaDigests, candidate.SchemaDigest)
}

func matchesSet(values []string, candidate string) bool {
	if len(values) == 0 {
		return true
	}
	candidate = strings.TrimSpace(candidate)
	for _, value := range values {
		if strings.TrimSpace(value) == candidate {
			return true
		}
	}
	return false
}

func (r InterpretationRevision) canonicalized() (InterpretationRevision, error) {
	n := r
	var err error
	if n.Project, err = canonicalInterpretationProject(n.Project); err != nil {
		return InterpretationRevision{}, err
	}
	if err := validateInterpretationID(string(n.LibraryID), "library"); err != nil {
		return InterpretationRevision{}, err
	}
	if n.ID != "" {
		if err := validateInterpretationID(string(n.ID), "revision"); err != nil {
			return InterpretationRevision{}, err
		}
	}
	if n.ParentRevisionID == nil {
		if n.ParentDigest != nil {
			return InterpretationRevision{}, fmt.Errorf("parentDigest requires parentRevisionId")
		}
	} else {
		if err := validateInterpretationID(string(*n.ParentRevisionID), "parent revision"); err != nil {
			return InterpretationRevision{}, err
		}
		if n.ParentDigest == nil || !interpretationDigestPattern.MatchString(string(*n.ParentDigest)) {
			return InterpretationRevision{}, fmt.Errorf("parentRevisionId requires a valid parentDigest")
		}
		parentID := *n.ParentRevisionID
		n.ParentRevisionID = &parentID
		parentDigest := *n.ParentDigest
		n.ParentDigest = &parentDigest
	}
	if n.ContentDigest != "" && !interpretationDigestPattern.MatchString(string(n.ContentDigest)) {
		return InterpretationRevision{}, fmt.Errorf("%w: %q", ErrInvalidInterpretationDigest, n.ContentDigest)
	}
	n.Author = strings.TrimSpace(n.Author)
	n.Explanation = strings.TrimSpace(n.Explanation)
	if n.Author == "" {
		return InterpretationRevision{}, fmt.Errorf("interpretation author is required")
	}
	if n.Explanation == "" {
		return InterpretationRevision{}, fmt.Errorf("interpretation explanation is required")
	}
	n.Applicability, err = n.Applicability.canonical()
	if err != nil {
		return InterpretationRevision{}, err
	}
	if len(n.Rules) == 0 {
		return InterpretationRevision{}, fmt.Errorf("interpretation requires at least one rule")
	}
	n.Rules = make([]InterpretationRule, len(r.Rules))
	seenIDs := map[InterpretationRuleID]bool{}
	for i, rule := range r.Rules {
		if err := validateInterpretationID(string(rule.ID), "rule"); err != nil {
			return InterpretationRevision{}, fmt.Errorf("rules[%d]: %w", i, err)
		}
		if seenIDs[rule.ID] {
			return InterpretationRevision{}, fmt.Errorf("duplicate interpretation rule id %q", rule.ID)
		}
		seenIDs[rule.ID] = true
		match, err := rule.Match.canonical()
		if err != nil {
			return InterpretationRevision{}, fmt.Errorf("rules[%d]: %w", i, err)
		}
		if match.ResourceType == "" {
			return InterpretationRevision{}, fmt.Errorf("rules[%d].match.resourceType is required", i)
		}
		definition, err := canonicalInterpretationDefinition(rule.Definition, match.ResourceType)
		if err != nil {
			return InterpretationRevision{}, fmt.Errorf("rules[%d]: %w", i, err)
		}
		if rule.Priority != nil {
			priority := *rule.Priority
			rule.Priority = &priority
		}
		n.Rules[i] = InterpretationRule{ID: rule.ID, Priority: rule.Priority, Match: match, Definition: definition}
	}
	sort.Slice(n.Rules, func(i, j int) bool {
		left := interpretationRuleSortKey(n.Rules[i])
		right := interpretationRuleSortKey(n.Rules[j])
		return left < right
	})
	if err := validateStaticAmbiguity(n.Rules); err != nil {
		return InterpretationRevision{}, err
	}
	return n, nil
}

func canonicalInterpretationDefinition(def InterpretationFeatureDefinition, resourceType string) (InterpretationFeatureDefinition, error) {
	def.Source = def.Source.Normalized()
	// Document.Validate is the existing closed authoring validator.  A
	// synthetic document keeps B06 from inventing a second source language.
	document := authoringv2.Document{
		Kind:             authoringv2.Kind,
		Output:           authoringv2.Output{ID: "interpretation", Title: "Interpretation"},
		RootResourceType: resourceType,
		Route:            authoringv2.RouteNode{OccurrenceID: authoringv2.RootOccurrenceID, ResourceType: resourceType},
		Columns:          []authoringv2.Column{{Column: "value", Label: "Value", OccurrenceID: authoringv2.RootOccurrenceID, Source: def.Source, Contributor: def.Contributor}},
	}
	if err := document.Validate(); err != nil {
		return InterpretationFeatureDefinition{}, err
	}
	if def.Contributor != nil {
		contributor := def.Contributor.Normalized()
		def.Contributor = &contributor
	}
	return def, nil
}

func interpretationRuleSortKey(rule InterpretationRule) string {
	return string(rule.ID)
}

func rulesOverlap(left, right InterpretationRule) bool {
	return overlapString(left.Match.ResourceType, right.Match.ResourceType) &&
		overlapString(left.Match.SourceProfile, right.Match.SourceProfile) &&
		overlapString(left.Match.SourceCanonical, right.Match.SourceCanonical) &&
		overlapString(left.Match.OwningScope, right.Match.OwningScope) &&
		overlapString(left.Match.System, right.Match.System) &&
		overlapString(left.Match.Code, right.Match.Code) &&
		overlapString(left.Match.LogicalType, right.Match.LogicalType) &&
		overlapString(left.Match.Cardinality, right.Match.Cardinality) &&
		overlapPath(left.Match.ExtensionURLPath, right.Match.ExtensionURLPath)
}

func overlapString(left, right string) bool { return left == "" || right == "" || left == right }
func overlapPath(left, right []string) bool {
	return len(left) == 0 || len(right) == 0 || matchPath(left, right) || matchPath(right, left)
}

func validateStaticAmbiguity(rules []InterpretationRule) error {
	for i := 0; i < len(rules); i++ {
		for j := i + 1; j < len(rules); j++ {
			if !rulesOverlap(rules[i], rules[j]) {
				continue
			}
			left, right := rules[i], rules[j]
			if left.Priority == nil || right.Priority == nil || *left.Priority == *right.Priority {
				return fmt.Errorf("%w: rules %q and %q overlap without a unique priority", ErrAmbiguousMapping, left.ID, right.ID)
			}
		}
	}
	return nil
}

// CanonicalContent returns executable interpretation content. Project,
// lineage, authorship, explanation, and timestamps are omitted; stable rule
// IDs remain because they are part of selected-rule provenance.
func (r InterpretationRevision) CanonicalContent() ([]byte, error) {
	n, err := r.canonicalized()
	if err != nil {
		return nil, err
	}
	type canonicalRule struct {
		ID         InterpretationRuleID            `json:"id"`
		Priority   *InterpretationPriority         `json:"priority,omitempty"`
		Match      InterpretationStructuralMatch   `json:"match"`
		Definition InterpretationFeatureDefinition `json:"definition"`
	}
	content := struct {
		Applicability InterpretationApplicability `json:"applicability"`
		Rules         []canonicalRule             `json:"rules"`
	}{Applicability: n.Applicability}
	for _, rule := range n.Rules {
		content.Rules = append(content.Rules, canonicalRule{ID: rule.ID, Priority: rule.Priority, Match: rule.Match, Definition: rule.Definition})
	}
	return json.Marshal(content)
}

func (r InterpretationRevision) ComputeContentDigest() (InterpretationContentDigest, error) {
	raw, err := r.CanonicalContent()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return InterpretationContentDigest("sha256:" + hex.EncodeToString(sum[:])), nil
}

func (r InterpretationRevision) RevisionIdentityEnvelope() (map[string]any, error) {
	n, err := r.canonicalized()
	if err != nil {
		return nil, err
	}
	digest := n.ContentDigest
	if digest == "" {
		digest, err = n.ComputeContentDigest()
		if err != nil {
			return nil, err
		}
	}
	parentID, parentDigest := "", ""
	if n.ParentRevisionID != nil {
		parentID = string(*n.ParentRevisionID)
	}
	if n.ParentDigest != nil {
		parentDigest = string(*n.ParentDigest)
	}
	return map[string]any{
		"project": string(n.Project), "libraryId": string(n.LibraryID),
		"parentRevisionId": parentID, "parentDigest": parentDigest,
		"contentDigest": string(digest), "author": n.Author, "explanation": n.Explanation,
	}, nil
}

func (r InterpretationRevision) ComputeRevisionID() (InterpretationRevisionID, error) {
	envelope, err := r.RevisionIdentityEnvelope()
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(envelope)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return InterpretationRevisionID("interpretation_" + hex.EncodeToString(sum[:])), nil
}

// PrepareInterpretationRevision canonicalizes an incoming revision and
// recomputes both immutable identities.  Persistence adapters must call this
// instead of trusting client-supplied IDs or digests.
func PrepareInterpretationRevision(r InterpretationRevision) (InterpretationRevision, error) {
	n, err := r.canonicalized()
	if err != nil {
		return InterpretationRevision{}, err
	}
	digest, err := n.ComputeContentDigest()
	if err != nil {
		return InterpretationRevision{}, err
	}
	n.ContentDigest = digest
	n.ID, err = n.ComputeRevisionID()
	if err != nil {
		return InterpretationRevision{}, err
	}
	if n.CreatedAt.IsZero() {
		n.CreatedAt = time.Now().UTC()
	} else {
		n.CreatedAt = n.CreatedAt.UTC()
	}
	return n, nil
}

func (r InterpretationRevision) Validate() error {
	n, err := r.canonicalized()
	if err != nil {
		return err
	}
	if r.ContentDigest != "" {
		digest, err := n.ComputeContentDigest()
		if err != nil {
			return err
		}
		if r.ContentDigest != digest {
			return fmt.Errorf("%w: content digest", ErrInterpretationIdentityMismatch)
		}
	}
	if r.ID != "" {
		candidate := n
		candidate.ContentDigest = r.ContentDigest
		if candidate.ContentDigest == "" {
			candidate.ContentDigest, err = n.ComputeContentDigest()
			if err != nil {
				return err
			}
		}
		id, err := candidate.ComputeRevisionID()
		if err != nil {
			return err
		}
		if r.ID != id {
			return fmt.Errorf("%w: revision ID", ErrInterpretationIdentityMismatch)
		}
	}
	return nil
}

func (r InterpretationRevision) SelectRule(candidate InterpretationStructuralCandidate) (InterpretationRule, error) {
	n, err := r.canonicalized()
	if err != nil {
		return InterpretationRule{}, err
	}
	if !n.Applicability.Matches(candidate) {
		return InterpretationRule{}, ErrNoInterpretationMatch
	}
	matches := make([]InterpretationRule, 0, len(n.Rules))
	for _, rule := range n.Rules {
		if rule.Match.Matches(candidate) {
			matches = append(matches, rule)
		}
	}
	if len(matches) == 0 {
		return InterpretationRule{}, ErrNoInterpretationMatch
	}
	if len(matches) == 1 {
		return cloneInterpretationRule(matches[0]), nil
	}
	var winner *InterpretationRule
	for i := range matches {
		if matches[i].Priority == nil {
			return InterpretationRule{}, ErrAmbiguousMapping
		}
		if winner == nil || *matches[i].Priority > *winner.Priority {
			winner = &matches[i]
		} else if *matches[i].Priority == *winner.Priority {
			return InterpretationRule{}, ErrAmbiguousMapping
		}
	}
	return cloneInterpretationRule(*winner), nil
}

func cloneInterpretationRule(rule InterpretationRule) InterpretationRule {
	if rule.Priority != nil {
		priority := *rule.Priority
		rule.Priority = &priority
	}
	rule.Match.ExtensionURLPath = append([]string(nil), rule.Match.ExtensionURLPath...)
	rule.Definition.Source = rule.Definition.Source.Normalized()
	if rule.Definition.Contributor != nil {
		contributor := rule.Definition.Contributor.Normalized()
		rule.Definition.Contributor = &contributor
	}
	return rule
}
