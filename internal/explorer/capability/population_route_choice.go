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

const PopulationRouteChoiceIDMaxLength = 16384

// PopulationRouteChoiceIdentity pins an exact route to one current table and
// immutable resource selection. It is identity, not authorization: apply
// must still load the selection and re-authorize every route edge.
type PopulationRouteChoiceIdentity struct {
	Version             int
	SnapshotToken       string
	OutputID            string
	SelectionRevisionID string
	Route               []ConstructionRouteStep
}

type populationRouteChoiceToken struct {
	Version             int                     `json:"version"`
	SnapshotToken       string                  `json:"snapshotToken"`
	OutputID            string                  `json:"outputId"`
	SelectionRevisionID string                  `json:"selectionRevisionId"`
	Route               []ConstructionRouteStep `json:"route"`
}

// NewPopulationRouteChoiceID creates a stable opaque identifier for a route.
func NewPopulationRouteChoiceID(identity PopulationRouteChoiceIdentity) (string, error) {
	if identity.Version == 0 {
		identity.Version = 1
	}
	if identity.Version != 1 || strings.TrimSpace(identity.SnapshotToken) == "" ||
		strings.TrimSpace(identity.OutputID) == "" || strings.TrimSpace(identity.SelectionRevisionID) == "" {
		return "", fmt.Errorf("population route choice requires snapshot, output, and selection identity")
	}
	if err := validateConstructionRoute(identity.Route); err != nil {
		return "", err
	}
	payload, err := json.Marshal(populationRouteChoiceToken{
		Version: identity.Version, SnapshotToken: identity.SnapshotToken,
		OutputID: identity.OutputID, SelectionRevisionID: identity.SelectionRevisionID,
		Route: cloneConstructionRoute(identity.Route),
	})
	if err != nil {
		return "", fmt.Errorf("encode population route choice: %w", err)
	}
	digest := sha256.Sum256(payload)
	return "pr1." + base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(digest[:]), nil
}

// DecodePopulationRouteChoiceID checks the token envelope and exact route.
// The caller must still re-authorize the snapshot, selection, and every edge.
func DecodePopulationRouteChoiceID(value string) (PopulationRouteChoiceIdentity, error) {
	if len(value) == 0 || len(value) > PopulationRouteChoiceIDMaxLength {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("population route choice id has an invalid length")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != "pr1" {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("population route choice id has an unsupported format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) == 0 {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("population route choice payload is invalid")
	}
	provided, err := hex.DecodeString(parts[2])
	if err != nil || len(provided) != sha256.Size {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("population route choice digest is invalid")
	}
	digest := sha256.Sum256(payload)
	if subtle.ConstantTimeCompare(provided, digest[:]) != 1 {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("population route choice digest does not match its payload")
	}
	var token populationRouteChoiceToken
	if err := decodeChoiceJSON(payload, &token); err != nil {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("decode population route choice: %w", err)
	}
	if token.Version != 1 || strings.TrimSpace(token.SnapshotToken) == "" ||
		strings.TrimSpace(token.OutputID) == "" || strings.TrimSpace(token.SelectionRevisionID) == "" {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("population route choice identity is incomplete")
	}
	if err := validateConstructionRoute(token.Route); err != nil {
		return PopulationRouteChoiceIdentity{}, fmt.Errorf("population route choice route is invalid: %w", err)
	}
	return PopulationRouteChoiceIdentity{
		Version: token.Version, SnapshotToken: token.SnapshotToken, OutputID: token.OutputID,
		SelectionRevisionID: token.SelectionRevisionID, Route: cloneConstructionRoute(token.Route),
	}, nil
}
