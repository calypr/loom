package capability

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const ConstructionChoiceIDMaxLength = 16384

type ConstructionChoiceForm string

const (
	ConstructionChoiceValue        ConstructionChoiceForm = "VALUE"
	ConstructionChoiceFirst        ConstructionChoiceForm = "FIRST"
	ConstructionChoiceAll          ConstructionChoiceForm = "ALL"
	ConstructionChoiceDistinct     ConstructionChoiceForm = "DISTINCT"
	ConstructionChoiceOwnerRecords ConstructionChoiceForm = "OWNER_RECORDS"
)

type ConstructionChoiceDecision string

const (
	ConstructionChoiceDefault          ConstructionChoiceDecision = "DEFAULT"
	ConstructionChoiceRequiresDecision ConstructionChoiceDecision = "REQUIRES_DECISION"
)

type ConstructionChoicePreservation string

const (
	ConstructionChoicePreserving ConstructionChoicePreservation = "PRESERVING"
	ConstructionChoiceReducing   ConstructionChoicePreservation = "REDUCING"
)

type ConstructionChoiceShape string

const (
	ConstructionChoiceScalar ConstructionChoiceShape = "SCALAR"
	ConstructionChoiceList   ConstructionChoiceShape = "LIST"
)

type ConstructionChoiceRowEffect string

const ConstructionChoicePreservesRows ConstructionChoiceRowEffect = "PRESERVES_ROW_GRAIN"

type ConstructionChoiceSupport string

const ConstructionChoiceSupported ConstructionChoiceSupport = "SUPPORTED"

type ConstructionChoiceSourceKind string

const (
	ConstructionChoiceSourceField    ConstructionChoiceSourceKind = "FIELD"
	ConstructionChoiceSourceSemantic ConstructionChoiceSourceKind = "SEMANTIC"
)

// ConstructionChoiceSource is closed to the variants defined by this package.
type ConstructionChoiceSource interface {
	constructionChoiceSource()
}

// FieldChoiceSource identifies one exact, compiler-proved capability field.
// Labels stay outside the identity so presentation changes do not select data.
type FieldChoiceSource struct {
	Kind               ConstructionChoiceSourceKind `json:"kind"`
	CandidateID        string                       `json:"candidateId"`
	NodeID             string                       `json:"nodeId"`
	ResourceType       string                       `json:"resourceType"`
	Path               string                       `json:"path"`
	Cardinality        string                       `json:"cardinality"`
	RepeatedBoundaries []RepeatedBoundary           `json:"repeatedBoundaries,omitempty"`
}

func (FieldChoiceSource) constructionChoiceSource() {}

// SemanticBindingChoiceSource preserves the observed FHIR owner and terminology
// identity. Display text is deliberately excluded from computational identity.
type SemanticBindingChoiceSource struct {
	Kind               ConstructionChoiceSourceKind `json:"kind"`
	ConceptID          string                       `json:"conceptId"`
	BindingID          string                       `json:"bindingId"`
	CandidateID        string                       `json:"candidateId"`
	NodeID             string                       `json:"nodeId"`
	ResourceType       string                       `json:"resourceType"`
	SourcePath         string                       `json:"sourcePath"`
	SourceCanonical    string                       `json:"sourceCanonical,omitempty"`
	SourceProfile      string                       `json:"sourceProfile,omitempty"`
	FieldPath          string                       `json:"fieldPath"`
	OwningScope        string                       `json:"owningScope,omitempty"`
	ExtensionURLPath   []string                     `json:"extensionUrlPath,omitempty"`
	KeySelector        string                       `json:"keySelector,omitempty"`
	System             string                       `json:"system,omitempty"`
	Version            string                       `json:"version,omitempty"`
	Code               string                       `json:"code,omitempty"`
	ValueSelector      string                       `json:"valueSelector"`
	ChoiceArm          string                       `json:"choiceArm,omitempty"`
	LogicalType        string                       `json:"logicalType"`
	RuleHint           string                       `json:"ruleHint"`
	RuleVersion        string                       `json:"ruleVersion"`
	SchemaVersion      int                          `json:"schemaVersion"`
	Cardinality        string                       `json:"cardinality"`
	RepeatedBoundaries []RepeatedBoundary           `json:"repeatedBoundaries,omitempty"`
}

func (SemanticBindingChoiceSource) constructionChoiceSource() {}

type ConstructionChoiceOption struct {
	Form         ConstructionChoiceForm         `json:"form"`
	Shape        ConstructionChoiceShape        `json:"shape"`
	Decision     ConstructionChoiceDecision     `json:"decision"`
	Preservation ConstructionChoicePreservation `json:"preservation"`
	RowEffect    ConstructionChoiceRowEffect    `json:"rowEffect"`
	Support      ConstructionChoiceSupport      `json:"support"`
	Reason       string                         `json:"reason"`
}

type ConstructionChoice struct {
	ChoiceID     string                         `json:"choiceId"`
	Source       ConstructionChoiceSource       `json:"source"`
	Route        []ConstructionRouteStep        `json:"route"`
	Presentation ConstructionChoicePresentation `json:"presentation"`
	Options      []ConstructionChoiceOption     `json:"options"`
}

type ConstructionChoicePresentation struct {
	Summary string                   `json:"summary"`
	Facts   []ConstructionChoiceFact `json:"facts"`
}

type ConstructionChoiceFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ConstructionRouteStep pins one directed traversal in a construction choice.
// Capability edge identity, endpoint identity, generated relationship label,
// and physical storage direction are all retained so apply cannot substitute
// a different route that happens to reach the same resource type.
type ConstructionRouteStep struct {
	EdgeID           string `json:"edgeId"`
	FromNodeID       string `json:"fromNodeId"`
	ToNodeID         string `json:"toNodeId"`
	FromResourceType string `json:"fromResourceType"`
	ToResourceType   string `json:"toResourceType"`
	Relationship     string `json:"relationship"`
	StorageDirection string `json:"storageDirection"`
	MatchMode        string `json:"matchMode"`
}

// ConstructionChoiceIdentity is the decoded, pinned identity used to
// re-resolve and reconstruct a server-issued choice during apply.
type ConstructionChoiceIdentity struct {
	Version              string
	Kind                 ConstructionChoiceSourceKind
	SnapshotToken        string
	SemanticContextToken string
	BuildID              string
	Source               ConstructionChoiceSource
	Route                []ConstructionRouteStep
}

type constructionChoiceToken struct {
	Version              string                       `json:"version"`
	Kind                 ConstructionChoiceSourceKind `json:"kind"`
	SnapshotToken        string                       `json:"snapshotToken"`
	SemanticContextToken string                       `json:"semanticContextToken,omitempty"`
	BuildID              string                       `json:"buildId,omitempty"`
	Source               json.RawMessage              `json:"source"`
	Route                []ConstructionRouteStep      `json:"route"`
}

// NewFieldConstructionChoice exposes only projection modes already proven by
// the compiler. Indexed is a Builder annotation, not a compiler projection.
func NewFieldConstructionChoice(snapshotToken string, candidate Candidate) (ConstructionChoice, error) {
	return NewFieldConstructionChoiceForRoute(snapshotToken, nil, candidate)
}

// NewFieldConstructionChoiceForRoute exposes only projection modes already
// proven by the compiler for this exact route. A nil or empty route represents
// the zero-hop row-root choice.
func NewFieldConstructionChoiceForRoute(snapshotToken string, route []ConstructionRouteStep, candidate Candidate) (ConstructionChoice, error) {
	if strings.TrimSpace(snapshotToken) == "" || strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.NodeID) == "" || strings.TrimSpace(candidate.ResourceType) == "" || strings.TrimSpace(candidate.FieldPath) == "" {
		return ConstructionChoice{}, fmt.Errorf("snapshot token and exact candidate identity are required")
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionChoice{}, err
	}
	source := FieldChoiceSource{
		Kind: ConstructionChoiceSourceField, CandidateID: candidate.ID, NodeID: candidate.NodeID,
		ResourceType: candidate.ResourceType, Path: candidate.FieldPath,
		Cardinality:        candidate.Cardinality,
		RepeatedBoundaries: append([]RepeatedBoundary(nil), candidate.RepeatedBoundaries...),
	}
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceField,
		SnapshotToken: snapshotToken, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionChoice{}, err
	}
	choice := ConstructionChoice{ChoiceID: choiceID, Source: source, Route: cloneConstructionRoute(route), Presentation: fieldChoicePresentation(source, candidate), Options: constructionOptions(candidate)}
	if len(choice.Options) == 0 {
		return ConstructionChoice{}, fmt.Errorf("candidate %q has no compiler-proved construction output", candidate.ID)
	}
	return choice, nil
}

// NewSemanticConstructionChoice binds an inventory observation to the exact
// compiler candidate for its value path. Snapshot, semantic context, and build
// identities pin the choice without adding separate mutable context fields.
func NewSemanticConstructionChoice(snapshotToken, semanticContextToken, buildID string, source SemanticBindingChoiceSource, candidate Candidate, ownerRecordsProved ...bool) (ConstructionChoice, error) {
	return NewSemanticConstructionChoiceForRoute(snapshotToken, semanticContextToken, buildID, nil, source, candidate, ownerRecordsProved...)
}

// NewSemanticConstructionChoiceForRoute pins an observed semantic binding to
// the exact compiler-proved route and terminal candidate.
func NewSemanticConstructionChoiceForRoute(snapshotToken, semanticContextToken, buildID string, route []ConstructionRouteStep, source SemanticBindingChoiceSource, candidate Candidate, ownerRecordsProved ...bool) (ConstructionChoice, error) {
	if strings.TrimSpace(snapshotToken) == "" || strings.TrimSpace(semanticContextToken) == "" || strings.TrimSpace(buildID) == "" ||
		strings.TrimSpace(source.ConceptID) == "" || strings.TrimSpace(source.BindingID) == "" ||
		strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.SourcePath) == "" ||
		strings.TrimSpace(source.FieldPath) == "" || strings.TrimSpace(source.ValueSelector) == "" || strings.TrimSpace(source.LogicalType) == "" ||
		strings.TrimSpace(source.RuleVersion) == "" || source.SchemaVersion <= 0 {
		return ConstructionChoice{}, fmt.Errorf("snapshot, semantic context, build, and exact semantic source identity are required")
	}
	if err := validateConstructionRoute(route); err != nil {
		return ConstructionChoice{}, err
	}
	resolvedValuePath := canonicalPath(source.FieldPath)
	selectorPath := canonicalOwnedSemanticPath(source.OwningScope, source.ValueSelector)
	candidatePath := canonicalPath(candidate.FieldPath)
	if candidate.ID == "" || candidate.NodeID == "" || candidate.ResourceType != source.ResourceType ||
		resolvedValuePath == "" || selectorPath == "" || candidatePath == "" ||
		candidatePath != candidate.FieldPath || candidatePath != resolvedValuePath || resolvedValuePath != selectorPath {
		return ConstructionChoice{}, fmt.Errorf("semantic source has no matching compiler-proved capability candidate")
	}
	source.Kind = ConstructionChoiceSourceSemantic
	source.CandidateID = candidate.ID
	source.NodeID = candidate.NodeID
	source.FieldPath = resolvedValuePath
	source.ValueSelector = selectorPath
	source.ExtensionURLPath = append([]string(nil), source.ExtensionURLPath...)
	source.Cardinality = candidate.Cardinality
	source.RepeatedBoundaries = append([]RepeatedBoundary(nil), candidate.RepeatedBoundaries...)
	choiceID, err := encodeConstructionChoiceID(ConstructionChoiceIdentity{
		Version: "construction-choice/v2", Kind: ConstructionChoiceSourceSemantic,
		SnapshotToken: snapshotToken, SemanticContextToken: semanticContextToken,
		BuildID: buildID, Source: source, Route: cloneConstructionRoute(route),
	})
	if err != nil {
		return ConstructionChoice{}, err
	}
	choice := ConstructionChoice{ChoiceID: choiceID, Source: source, Route: cloneConstructionRoute(route), Presentation: semanticChoicePresentation(source, candidate), Options: semanticConstructionOptions(candidate, source, route)}
	if len(ownerRecordsProved) > 0 && ownerRecordsProved[0] && ownerRecordsSourceSupported(source) {
		choice.Options = append(choice.Options, ConstructionChoiceOption{
			Form: ConstructionChoiceOwnerRecords, Shape: ConstructionChoiceList,
			Decision: ConstructionChoiceRequiresDecision, Preservation: ConstructionChoicePreserving,
			RowEffect: ConstructionChoicePreservesRows, Support: ConstructionChoiceSupported,
			Reason: "Keep each FHIR owner as an ordered record with its value and source evidence.",
		})
	}
	if len(choice.Options) == 0 {
		return ConstructionChoice{}, fmt.Errorf("semantic source %q has no compiler-proved construction output", source.SourcePath)
	}
	return choice, nil
}

func semanticConstructionOptions(candidate Candidate, source SemanticBindingChoiceSource, route []ConstructionRouteStep) []ConstructionChoiceOption {
	options := constructionOptions(candidate)
	if len(route) == 0 || strings.TrimSpace(source.KeySelector) == "" ||
		strings.TrimSpace(source.System) == "" || strings.TrimSpace(source.Code) == "" ||
		IsRepeatedCardinality(candidate.Cardinality) {
		return options
	}
	// A scalar value on one related resource is not necessarily scalar at the
	// table row grain. Several related resources may carry the same code.
	// The correlated pivot compiler can preserve those matches as a typed list.
	allFound := false
	for index := range options {
		switch options[index].Form {
		case ConstructionChoiceValue:
			options[index].Decision = ConstructionChoiceRequiresDecision
			options[index].Reason = "Use a scalar only when this table row reaches exactly one matching value."
		case ConstructionChoiceAll:
			allFound = true
			options[index].Decision = ConstructionChoiceDefault
			options[index].Reason = "Keep every matching value across related FHIR records."
		}
	}
	if !allFound {
		options = append(options, ConstructionChoiceOption{
			Form: ConstructionChoiceAll, Shape: ConstructionChoiceList,
			Decision: ConstructionChoiceDefault, Preservation: ConstructionChoicePreserving,
			RowEffect: ConstructionChoicePreservesRows, Support: ConstructionChoiceSupported,
			Reason: "Keep every matching value across related FHIR records.",
		})
	}
	return options
}

func ownerRecordsSourceSupported(source SemanticBindingChoiceSource) bool {
	return strings.TrimSpace(source.System) != "" && strings.TrimSpace(source.Code) != "" &&
		strings.TrimSpace(source.KeySelector) != "" && strings.TrimSpace(source.ValueSelector) != ""
}

// DecodeConstructionChoiceID validates the token envelope and returns the
// typed pinned identity. The caller must still re-authorize and re-resolve it.
func DecodeConstructionChoiceID(choiceID string) (ConstructionChoiceIdentity, error) {
	if len(choiceID) == 0 || len(choiceID) > ConstructionChoiceIDMaxLength {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id has an invalid length")
	}
	parts := strings.Split(choiceID, ".")
	if len(parts) != 3 || (parts[0] != "cc1" && parts[0] != "cc2") {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id has an unsupported version or format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id payload is invalid")
	}
	providedDigest, err := hex.DecodeString(parts[2])
	if err != nil || len(providedDigest) != sha256.Size {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id digest is invalid")
	}
	expectedDigest := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(providedDigest, expectedDigest[:]) != 1 {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id digest does not match its payload")
	}
	var token constructionChoiceToken
	if err := decodeChoiceJSON(payload, &token); err != nil {
		return ConstructionChoiceIdentity{}, fmt.Errorf("decode construction choice id payload: %w", err)
	}
	if (parts[0] == "cc1" && token.Version != "construction-choice/v1") ||
		(parts[0] == "cc2" && token.Version != "construction-choice/v2") || strings.TrimSpace(token.SnapshotToken) == "" {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id is missing its pinned version or snapshot")
	}
	if parts[0] == "cc1" && len(token.Route) != 0 {
		return ConstructionChoiceIdentity{}, fmt.Errorf("legacy construction choice cannot carry a route")
	}
	if err := validateConstructionRoute(token.Route); err != nil {
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice route is invalid: %w", err)
	}
	identity := ConstructionChoiceIdentity{
		Version: token.Version, Kind: token.Kind, SnapshotToken: token.SnapshotToken,
		SemanticContextToken: token.SemanticContextToken, BuildID: token.BuildID,
		Route: cloneConstructionRoute(token.Route),
	}
	switch token.Kind {
	case ConstructionChoiceSourceField:
		var source FieldChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode field choice source: %w", err)
		}
		if source.Kind != token.Kind || strings.TrimSpace(source.CandidateID) == "" || strings.TrimSpace(source.NodeID) == "" || strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.Path) == "" {
			return ConstructionChoiceIdentity{}, fmt.Errorf("field choice source identity is incomplete")
		}
		if token.SemanticContextToken != "" || token.BuildID != "" {
			return ConstructionChoiceIdentity{}, fmt.Errorf("field choice id cannot carry semantic inventory context")
		}
		identity.Source = source
	case ConstructionChoiceSourceSemantic:
		var source SemanticBindingChoiceSource
		if err := decodeChoiceJSON(token.Source, &source); err != nil {
			return ConstructionChoiceIdentity{}, fmt.Errorf("decode semantic choice source: %w", err)
		}
		if token.SemanticContextToken == "" || token.BuildID == "" || source.Kind != token.Kind ||
			strings.TrimSpace(source.ConceptID) == "" || strings.TrimSpace(source.BindingID) == "" ||
			strings.TrimSpace(source.CandidateID) == "" || strings.TrimSpace(source.NodeID) == "" ||
			strings.TrimSpace(source.ResourceType) == "" || strings.TrimSpace(source.SourcePath) == "" ||
			strings.TrimSpace(source.FieldPath) == "" || strings.TrimSpace(source.ValueSelector) == "" ||
			strings.TrimSpace(source.RuleVersion) == "" || source.SchemaVersion <= 0 {
			return ConstructionChoiceIdentity{}, fmt.Errorf("semantic choice source identity is incomplete")
		}
		identity.Source = source
	default:
		return ConstructionChoiceIdentity{}, fmt.Errorf("construction choice id has an unsupported source kind")
	}
	return identity, nil
}

func encodeConstructionChoiceID(identity ConstructionChoiceIdentity) (string, error) {
	if identity.Version != "construction-choice/v2" || strings.TrimSpace(identity.SnapshotToken) == "" {
		return "", fmt.Errorf("construction choice identity has no supported version or snapshot")
	}
	if err := validateConstructionRoute(identity.Route); err != nil {
		return "", fmt.Errorf("construction choice route is invalid: %w", err)
	}
	var kind ConstructionChoiceSourceKind
	switch source := identity.Source.(type) {
	case FieldChoiceSource:
		kind = source.Kind
	case SemanticBindingChoiceSource:
		kind = source.Kind
	default:
		return "", fmt.Errorf("construction choice identity has an unsupported source")
	}
	if kind != identity.Kind {
		return "", fmt.Errorf("construction choice identity kind does not match its source")
	}
	if kind == ConstructionChoiceSourceSemantic && (identity.SemanticContextToken == "" || identity.BuildID == "") {
		return "", fmt.Errorf("semantic choice identity requires inventory context and build")
	}
	if kind == ConstructionChoiceSourceField && (identity.SemanticContextToken != "" || identity.BuildID != "") {
		return "", fmt.Errorf("field choice identity cannot include semantic inventory context")
	}
	source, err := json.Marshal(identity.Source)
	if err != nil {
		return "", fmt.Errorf("marshal construction choice source: %w", err)
	}
	payload, err := json.Marshal(constructionChoiceToken{
		Version: identity.Version, Kind: identity.Kind, SnapshotToken: identity.SnapshotToken,
		SemanticContextToken: identity.SemanticContextToken, BuildID: identity.BuildID, Source: source,
		Route: cloneConstructionRoute(identity.Route),
	})
	if err != nil {
		return "", fmt.Errorf("marshal construction choice identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	choiceID := "cc2." + base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(digest[:])
	if len(choiceID) > ConstructionChoiceIDMaxLength {
		return "", fmt.Errorf("construction choice identity exceeds the maximum token length")
	}
	return choiceID, nil
}

func validateConstructionRoute(route []ConstructionRouteStep) error {
	for index, step := range route {
		if strings.TrimSpace(step.EdgeID) == "" || strings.TrimSpace(step.FromNodeID) == "" || strings.TrimSpace(step.ToNodeID) == "" ||
			strings.TrimSpace(step.FromResourceType) == "" || strings.TrimSpace(step.ToResourceType) == "" ||
			strings.TrimSpace(step.Relationship) == "" || strings.TrimSpace(step.StorageDirection) == "" {
			return fmt.Errorf("route step %d is missing exact edge identity", index)
		}
		if step.StorageDirection != "INBOUND" && step.StorageDirection != "OUTBOUND" {
			return fmt.Errorf("route step %d has an unsupported storage direction", index)
		}
		if step.MatchMode != "OPTIONAL" && step.MatchMode != "REQUIRED" {
			return fmt.Errorf("route step %d has an unsupported match mode", index)
		}
		if index > 0 && (route[index-1].ToNodeID != step.FromNodeID || route[index-1].ToResourceType != step.FromResourceType) {
			return fmt.Errorf("route is discontinuous at step %d", index)
		}
	}
	return nil
}

func cloneConstructionRoute(route []ConstructionRouteStep) []ConstructionRouteStep {
	if route == nil {
		return []ConstructionRouteStep{}
	}
	return append([]ConstructionRouteStep{}, route...)
}

func fieldChoicePresentation(source FieldChoiceSource, candidate Candidate) ConstructionChoicePresentation {
	summary := strings.TrimSpace(candidate.Label)
	if summary == "" {
		summary = strings.TrimSpace(source.ResourceType + "." + source.Path)
	}
	facts := []ConstructionChoiceFact{
		{Label: "FHIR field", Value: strings.TrimSpace(source.ResourceType + "." + source.Path)},
		{Label: "Value type", Value: strings.TrimSpace(candidate.LogicalType)},
		{Label: "Repetition", Value: cardinalityPresentation(source.Cardinality)},
	}
	return ConstructionChoicePresentation{Summary: summary, Facts: nonEmptyConstructionFacts(facts)}
}

func semanticChoicePresentation(source SemanticBindingChoiceSource, candidate Candidate) ConstructionChoicePresentation {
	summary := strings.TrimSpace(candidate.Label)
	if summary == "" {
		summary = strings.TrimSpace(source.Code)
	}
	facts := []ConstructionChoiceFact{
		{Label: "Code system", Value: strings.TrimSpace(source.System)},
		{Label: "Code", Value: strings.TrimSpace(source.Code)},
		{Label: "Value field", Value: strings.TrimSpace(source.ResourceType + "." + source.FieldPath)},
		{Label: "Value type", Value: strings.TrimSpace(source.LogicalType)},
		{Label: "Source", Value: strings.TrimSpace(source.SourcePath)},
		{Label: "Owner", Value: strings.TrimSpace(source.OwningScope)},
		{Label: "Choice arm", Value: strings.TrimSpace(source.ChoiceArm)},
	}
	return ConstructionChoicePresentation{Summary: summary, Facts: nonEmptyConstructionFacts(facts)}
}

func cardinalityPresentation(cardinality string) string {
	if IsRepeatedCardinality(cardinality) {
		return "Repeated values"
	}
	return "Single value"
}

func nonEmptyConstructionFacts(facts []ConstructionChoiceFact) []ConstructionChoiceFact {
	result := make([]ConstructionChoiceFact, 0, len(facts))
	for _, fact := range facts {
		if fact.Label != "" && fact.Value != "" {
			result = append(result, fact)
		}
	}
	return result
}

func decodeChoiceJSON(raw []byte, target interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func constructionOptions(candidate Candidate) []ConstructionChoiceOption {
	options := make([]ConstructionChoiceOption, 0, 4)
	for _, projection := range []ProjectionMode{ProjectionScalar, ProjectionFirst, ProjectionArray, ProjectionDistinctArray} {
		form, found := constructionForm(projection)
		if !found || !containsProjection(candidate.ProjectionModes, projection) {
			continue
		}
		repeated := IsRepeatedCardinality(candidate.Cardinality)
		if form == ConstructionChoiceFirst && !repeated {
			continue
		}
		option := ConstructionChoiceOption{
			Form: form, Decision: ConstructionChoiceRequiresDecision,
			Shape: ConstructionChoiceScalar, Preservation: ConstructionChoiceReducing,
			RowEffect: ConstructionChoicePreservesRows,
			Support:   ConstructionChoiceSupported, Reason: "The compiler proved this output form for the exact field path.",
		}
		switch form {
		case ConstructionChoiceValue:
			option.Preservation = ConstructionChoicePreserving
			if !repeated {
				option.Decision = ConstructionChoiceDefault
			}
		case ConstructionChoiceAll:
			option.Shape = ConstructionChoiceList
			option.Preservation = ConstructionChoicePreserving
			if repeated {
				option.Decision = ConstructionChoiceDefault
			}
		case ConstructionChoiceDistinct:
			option.Shape = ConstructionChoiceList
		}
		options = append(options, option)
	}
	return options
}

func constructionForm(mode ProjectionMode) (ConstructionChoiceForm, bool) {
	switch mode {
	case ProjectionScalar:
		return ConstructionChoiceValue, true
	case ProjectionFirst:
		return ConstructionChoiceFirst, true
	case ProjectionArray:
		return ConstructionChoiceAll, true
	case ProjectionDistinctArray:
		return ConstructionChoiceDistinct, true
	default:
		return "", false
	}
}

func canonicalSemanticPath(path string) string {
	path = strings.TrimPrefix(strings.TrimSpace(path), "root.")
	return canonicalPath(path)
}

func canonicalOwnedSemanticPath(ownerPath, valuePath string) string {
	ownerPath = canonicalSemanticPath(ownerPath)
	valuePath = canonicalSemanticPath(valuePath)
	if ownerPath == "" || valuePath == "" || valuePath == ownerPath || strings.HasPrefix(valuePath, ownerPath+".") {
		return valuePath
	}
	return ownerPath + "." + valuePath
}

func containsProjection(modes []ProjectionMode, want ProjectionMode) bool {
	for _, mode := range modes {
		if mode == want {
			return true
		}
	}
	return false
}
