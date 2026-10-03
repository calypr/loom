package capability

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type RowChoiceKind string

const (
	RowChoiceFieldGroupKey RowChoiceKind = "FIELD_GROUP_KEY"
	RowChoiceExpandedScope RowChoiceKind = "EXPANDED_SCOPE"
)

type RowChoiceCardinality string

const (
	RowChoiceOne  RowChoiceCardinality = "ONE"
	RowChoiceMany RowChoiceCardinality = "MANY"
)

type RowChoiceShape string

const (
	RowChoiceScalar RowChoiceShape = "SCALAR"
	RowChoiceObject RowChoiceShape = "OBJECT"
	RowChoiceArray  RowChoiceShape = "ARRAY"
)

// RowChoiceFacts are detached values adapted from generated schema metadata.
type RowChoiceFacts struct {
	ResourceType  string
	CanonicalPath string
	FHIRType      string
	Cardinality   RowChoiceCardinality
	Shape         RowChoiceShape
	Reference     bool
	Title         string
	Description   string
}

// RowChoiceOccurrence identifies one exact occurrence in an authorized route.
type RowChoiceOccurrence struct {
	OccurrenceID string
	NodeID       string
	ResourceType string
	Route        []ConstructionRouteStep
}

type RowChoice struct {
	ChoiceID     string
	Kind         RowChoiceKind
	Label        string
	Description  string
	ValueType    string
	OccurrenceID string
	NodeID       string
	ResourceType string
	Route        []ConstructionRouteStep
	Path         string
	FHIRType     string
	Cardinality  RowChoiceCardinality
	Reference    bool
	Presentation ConstructionChoicePresentation
}

type RowChoiceIdentity struct {
	Version       string
	Kind          RowChoiceKind
	SnapshotToken string
	SchemaDigest  string
	Occurrence    RowChoiceOccurrence
	Path          string
	FHIRType      string
	Cardinality   RowChoiceCardinality
	Shape         RowChoiceShape
	Reference     bool
}

type rowChoiceToken struct {
	Version       string               `json:"version"`
	Kind          RowChoiceKind        `json:"kind"`
	SnapshotToken string               `json:"snapshotToken"`
	SchemaDigest  string               `json:"schemaDigest"`
	Occurrence    RowChoiceOccurrence  `json:"occurrence"`
	Path          string               `json:"path"`
	FHIRType      string               `json:"fhirType"`
	Cardinality   RowChoiceCardinality `json:"cardinality"`
	Shape         RowChoiceShape       `json:"shape"`
	Reference     bool                 `json:"reference"`
}

type RowChoicePathResolver func(resourceType, canonicalPath string) (RowChoiceFacts, error)

// NewRowChoice binds schema facts to exactly one authorized route occurrence.
func NewRowChoice(snapshot Snapshot, occurrences []RowChoiceOccurrence, occurrenceID string, kind RowChoiceKind, facts RowChoiceFacts) (RowChoice, error) {
	if err := snapshot.ValidateToken(snapshot.Token); err != nil {
		return RowChoice{}, err
	}
	if strings.TrimSpace(snapshot.Identity.SchemaDigest) == "" {
		return RowChoice{}, fmt.Errorf("row choice requires a pinned schema digest")
	}
	occurrence, err := uniqueRowOccurrence(occurrences, occurrenceID)
	if err != nil {
		return RowChoice{}, err
	}
	if err := validateRowOccurrence(snapshot, occurrence); err != nil {
		return RowChoice{}, err
	}
	if facts.ResourceType != occurrence.ResourceType || strings.TrimSpace(facts.CanonicalPath) == "" || facts.CanonicalPath != strings.TrimSpace(facts.CanonicalPath) || strings.TrimSpace(facts.FHIRType) == "" {
		return RowChoice{}, fmt.Errorf("row choice schema facts do not match the route occurrence")
	}
	switch kind {
	case RowChoiceFieldGroupKey:
		if facts.Shape != RowChoiceScalar || facts.Cardinality != RowChoiceOne || facts.Reference {
			return RowChoice{}, fmt.Errorf("field group key must resolve to a non-repeated scalar")
		}
	case RowChoiceExpandedScope:
		if facts.Shape != RowChoiceArray || facts.Cardinality != RowChoiceMany || facts.Reference {
			return RowChoice{}, fmt.Errorf("expanded scope must resolve to a supported repeated value")
		}
	default:
		return RowChoice{}, fmt.Errorf("row choice kind %q is unsupported", kind)
	}
	identity := RowChoiceIdentity{
		Version: "row-choice/v1", Kind: kind, SnapshotToken: snapshot.Token, SchemaDigest: snapshot.Identity.SchemaDigest,
		Occurrence: cloneRowOccurrence(occurrence), Path: facts.CanonicalPath, FHIRType: facts.FHIRType,
		Cardinality: facts.Cardinality, Shape: facts.Shape, Reference: facts.Reference,
	}
	choiceID, err := encodeRowChoiceID(identity)
	if err != nil {
		return RowChoice{}, err
	}
	label := strings.TrimSpace(facts.Title)
	if label == "" {
		switch kind {
		case RowChoiceFieldGroupKey:
			label = "Grouping field"
		case RowChoiceExpandedScope:
			label = "Expanded collection"
		}
	}
	presentationFacts := []ConstructionChoiceFact{
		{Label: "Occurrence", Value: occurrence.OccurrenceID},
		{Label: "FHIR field", Value: occurrence.ResourceType + "." + facts.CanonicalPath},
		{Label: "FHIR type", Value: facts.FHIRType},
		{Label: "Cardinality", Value: string(facts.Cardinality)},
	}
	if description := strings.TrimSpace(facts.Description); description != "" {
		presentationFacts = append(presentationFacts, ConstructionChoiceFact{Label: "Description", Value: description})
	}
	return RowChoice{
		ChoiceID: choiceID, Kind: kind, Label: label, Description: strings.TrimSpace(facts.Description),
		ValueType: rowChoiceValueType(facts), OccurrenceID: occurrence.OccurrenceID, NodeID: occurrence.NodeID,
		ResourceType: occurrence.ResourceType, Route: cloneConstructionRoute(occurrence.Route), Path: facts.CanonicalPath,
		FHIRType: facts.FHIRType, Cardinality: facts.Cardinality, Reference: facts.Reference,
		Presentation: ConstructionChoicePresentation{Summary: label, Facts: presentationFacts},
	}, nil
}

func rowChoiceValueType(facts RowChoiceFacts) string {
	if facts.Shape == RowChoiceArray {
		return "ARRAY"
	}
	if facts.Shape == RowChoiceObject {
		return "OBJECT"
	}
	switch strings.ToLower(facts.FHIRType) {
	case "boolean":
		return "BOOLEAN"
	case "integer", "decimal":
		return "NUMBER"
	default:
		return "STRING"
	}
}

// ResolveRowChoiceID rechecks a choice against the current schema and route.
func ResolveRowChoiceID(snapshot Snapshot, occurrences []RowChoiceOccurrence, choiceID string, resolve RowChoicePathResolver) (RowChoice, error) {
	identity, err := DecodeRowChoiceID(choiceID)
	if err != nil {
		return RowChoice{}, err
	}
	if snapshot.Token != identity.SnapshotToken || snapshot.Identity.SchemaDigest != identity.SchemaDigest {
		return RowChoice{}, ErrStaleSnapshot
	}
	if resolve == nil {
		return RowChoice{}, fmt.Errorf("row choice schema resolver is unavailable")
	}
	occurrence, err := uniqueRowOccurrence(occurrences, identity.Occurrence.OccurrenceID)
	if err != nil {
		return RowChoice{}, err
	}
	if !sameRowOccurrence(occurrence, identity.Occurrence) {
		return RowChoice{}, fmt.Errorf("row choice route occurrence is stale or tampered")
	}
	facts, err := resolve(occurrence.ResourceType, identity.Path)
	if err != nil {
		return RowChoice{}, fmt.Errorf("resolve row choice path: %w", err)
	}
	choice, err := NewRowChoice(snapshot, occurrences, occurrence.OccurrenceID, identity.Kind, facts)
	if err != nil {
		return RowChoice{}, err
	}
	if choice.ChoiceID != choiceID {
		return RowChoice{}, fmt.Errorf("row choice identity no longer matches resolved schema facts")
	}
	return choice, nil
}

func DecodeRowChoiceID(choiceID string) (RowChoiceIdentity, error) {
	if choiceID == "" || len(choiceID) > ConstructionChoiceIDMaxLength {
		return RowChoiceIdentity{}, fmt.Errorf("row choice id has an invalid length")
	}
	parts := strings.Split(choiceID, ".")
	if len(parts) != 3 || parts[0] != "rc1" {
		return RowChoiceIdentity{}, fmt.Errorf("row choice id has an unsupported version or format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 {
		return RowChoiceIdentity{}, fmt.Errorf("row choice id payload is invalid")
	}
	provided, err := hex.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size {
		return RowChoiceIdentity{}, fmt.Errorf("row choice id digest is invalid")
	}
	digest := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(provided, digest[:]) != 1 {
		return RowChoiceIdentity{}, fmt.Errorf("row choice id digest does not match its payload")
	}
	var token rowChoiceToken
	if err := decodeChoiceJSON(payload, &token); err != nil {
		return RowChoiceIdentity{}, fmt.Errorf("decode row choice id payload: %w", err)
	}
	if token.Version != "row-choice/v1" || strings.TrimSpace(token.SnapshotToken) == "" || strings.TrimSpace(token.SchemaDigest) == "" ||
		strings.TrimSpace(token.Path) == "" || token.Path != strings.TrimSpace(token.Path) || strings.TrimSpace(token.FHIRType) == "" {
		return RowChoiceIdentity{}, fmt.Errorf("row choice id is incomplete")
	}
	if err := validateRowOccurrenceShape(token.Occurrence); err != nil {
		return RowChoiceIdentity{}, err
	}
	if err := validateRowChoiceFacts(token.Kind, token.Cardinality, token.Shape, token.Reference); err != nil {
		return RowChoiceIdentity{}, err
	}
	return RowChoiceIdentity{
		Version: token.Version, Kind: token.Kind, SnapshotToken: token.SnapshotToken, SchemaDigest: token.SchemaDigest,
		Occurrence: cloneRowOccurrence(token.Occurrence), Path: token.Path, FHIRType: token.FHIRType,
		Cardinality: token.Cardinality, Shape: token.Shape, Reference: token.Reference,
	}, nil
}

func encodeRowChoiceID(identity RowChoiceIdentity) (string, error) {
	if identity.Version != "row-choice/v1" || strings.TrimSpace(identity.SnapshotToken) == "" || strings.TrimSpace(identity.SchemaDigest) == "" {
		return "", fmt.Errorf("row choice identity has no supported version or snapshot")
	}
	if err := validateRowOccurrenceShape(identity.Occurrence); err != nil {
		return "", err
	}
	if err := validateRowChoiceFacts(identity.Kind, identity.Cardinality, identity.Shape, identity.Reference); err != nil {
		return "", err
	}
	if strings.TrimSpace(identity.Path) == "" || identity.Path != strings.TrimSpace(identity.Path) || strings.TrimSpace(identity.FHIRType) == "" {
		return "", fmt.Errorf("row choice identity requires an exact schema path and type")
	}
	payload, err := json.Marshal(rowChoiceToken{
		Version: identity.Version, Kind: identity.Kind, SnapshotToken: identity.SnapshotToken, SchemaDigest: identity.SchemaDigest,
		Occurrence: cloneRowOccurrence(identity.Occurrence), Path: identity.Path, FHIRType: identity.FHIRType,
		Cardinality: identity.Cardinality, Shape: identity.Shape, Reference: identity.Reference,
	})
	if err != nil {
		return "", fmt.Errorf("marshal row choice identity: %w", err)
	}
	digest := sha256.Sum256(payload)
	return "rc1." + base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(digest[:]), nil
}

func validateRowChoiceFacts(kind RowChoiceKind, cardinality RowChoiceCardinality, shape RowChoiceShape, reference bool) error {
	if reference {
		return fmt.Errorf("row choices do not support reference shapes")
	}
	switch kind {
	case RowChoiceFieldGroupKey:
		if shape != RowChoiceScalar || cardinality != RowChoiceOne {
			return fmt.Errorf("field group key must resolve to a non-repeated scalar")
		}
	case RowChoiceExpandedScope:
		if shape != RowChoiceArray || cardinality != RowChoiceMany {
			return fmt.Errorf("expanded scope must resolve to a supported repeated value")
		}
	default:
		return fmt.Errorf("row choice kind %q is unsupported", kind)
	}
	return nil
}

func uniqueRowOccurrence(occurrences []RowChoiceOccurrence, occurrenceID string) (RowChoiceOccurrence, error) {
	if strings.TrimSpace(occurrenceID) == "" {
		return RowChoiceOccurrence{}, fmt.Errorf("row choice occurrence is required")
	}
	var match RowChoiceOccurrence
	for _, occurrence := range occurrences {
		if occurrence.OccurrenceID != occurrenceID {
			continue
		}
		if match.OccurrenceID != "" {
			return RowChoiceOccurrence{}, fmt.Errorf("route occurrence %q is ambiguous", occurrenceID)
		}
		match = occurrence
	}
	if match.OccurrenceID == "" {
		return RowChoiceOccurrence{}, fmt.Errorf("route occurrence %q is not authorized", occurrenceID)
	}
	return cloneRowOccurrence(match), nil
}

func validateRowOccurrence(snapshot Snapshot, occurrence RowChoiceOccurrence) error {
	if err := validateRowOccurrenceShape(occurrence); err != nil {
		return err
	}
	rootNodeID := occurrence.NodeID
	rootResourceType := occurrence.ResourceType
	if len(occurrence.Route) != 0 {
		rootNodeID = occurrence.Route[0].FromNodeID
		rootResourceType = occurrence.Route[0].FromResourceType
	}
	var rootMatches int
	for _, node := range snapshot.Nodes {
		if node.ResourceType == rootResourceType && node.RowRootEligible {
			rootMatches++
			if node.ID != rootNodeID {
				return fmt.Errorf("row choice route root is ambiguous for resource type %q", rootResourceType)
			}
		}
	}
	if rootMatches != 1 {
		return fmt.Errorf("row choice route root is unavailable or ambiguous")
	}
	for index, step := range occurrence.Route {
		var match *Edge
		for edgeIndex := range snapshot.Edges {
			edge := &snapshot.Edges[edgeIndex]
			if edge.ID != step.EdgeID {
				continue
			}
			if match != nil {
				return fmt.Errorf("row choice route edge %q is ambiguous", step.EdgeID)
			}
			match = edge
		}
		if match == nil || match.FromNodeID != step.FromNodeID || match.ToNodeID != step.ToNodeID ||
			match.SourceResourceType != step.FromResourceType || match.TargetResourceType != step.ToResourceType ||
			match.Label != step.Relationship || strings.ToUpper(match.StorageDirection) != step.StorageDirection {
			return fmt.Errorf("row choice route step %d is not in the capability snapshot", index)
		}
	}
	lastNodeID := occurrence.NodeID
	lastResourceType := occurrence.ResourceType
	if len(occurrence.Route) > 0 {
		last := occurrence.Route[len(occurrence.Route)-1]
		lastNodeID = last.ToNodeID
		lastResourceType = last.ToResourceType
	}
	if lastNodeID != occurrence.NodeID || lastResourceType != occurrence.ResourceType {
		return fmt.Errorf("row choice route endpoint does not match occurrence %q", occurrence.OccurrenceID)
	}
	return nil
}

func validateRowOccurrenceShape(occurrence RowChoiceOccurrence) error {
	if strings.TrimSpace(occurrence.OccurrenceID) == "" || occurrence.OccurrenceID != strings.TrimSpace(occurrence.OccurrenceID) ||
		strings.TrimSpace(occurrence.NodeID) == "" || strings.TrimSpace(occurrence.ResourceType) == "" {
		return fmt.Errorf("row choice route occurrence identity is incomplete")
	}
	return validateConstructionRoute(occurrence.Route)
}

func cloneRowOccurrence(occurrence RowChoiceOccurrence) RowChoiceOccurrence {
	occurrence.Route = cloneConstructionRoute(occurrence.Route)
	return occurrence
}

func sameRowOccurrence(left, right RowChoiceOccurrence) bool {
	if left.OccurrenceID != right.OccurrenceID || left.NodeID != right.NodeID || left.ResourceType != right.ResourceType || len(left.Route) != len(right.Route) {
		return false
	}
	for index := range left.Route {
		if left.Route[index] != right.Route[index] {
			return false
		}
	}
	return true
}
