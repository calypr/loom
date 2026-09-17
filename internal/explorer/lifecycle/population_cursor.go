package lifecycle

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// PopulationMappingCursor is the complete, non-sensitive binding carried by
// a population report page cursor. It is signed as one payload so callers
// cannot move a cursor between reports or change the member position.
type PopulationMappingCursor struct {
	Version             int    `json:"version"`
	ReceiptID           string `json:"receiptId"`
	OutputID            string `json:"outputId"`
	Project             string `json:"project"`
	ExplorerID          string `json:"explorerId"`
	Generation          string `json:"generation"`
	ScopeDigest         string `json:"scopeDigest"`
	SelectionRevisionID string `json:"selectionRevisionId"`
	MembershipDigest    string `json:"membershipDigest"`
	ResourceType        string `json:"resourceType"`
	MemberKey           string `json:"memberKey"`
}

// PopulationMappingCursorCodec is injected by the deployment boundary. A
// stable configured key, rather than process state, keeps cursors valid across
// replicas and restarts.
type PopulationMappingCursorCodec interface {
	Encode(PopulationMappingCursor) (string, error)
	Decode(string) (PopulationMappingCursor, error)
}

// HMACPopulationMappingCursorCodec signs cursors with a server-configured
// secret. The wire format is base64url(payload).base64url(signature).
type HMACPopulationMappingCursorCodec struct {
	key []byte
}

func NewHMACPopulationMappingCursorCodec(secret string) (*HMACPopulationMappingCursorCodec, error) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 16 {
		return nil, fmt.Errorf("population mapping cursor secret must contain at least 16 characters")
	}
	return &HMACPopulationMappingCursorCodec{key: []byte(secret)}, nil
}

func (c *HMACPopulationMappingCursorCodec) Encode(cursor PopulationMappingCursor) (string, error) {
	if c == nil || len(c.key) == 0 {
		return "", fmt.Errorf("population mapping cursor codec is not configured")
	}
	if err := validatePopulationMappingCursor(cursor); err != nil {
		return "", err
	}
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("encode population mapping cursor: %w", err)
	}
	signature := c.mac(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (c *HMACPopulationMappingCursorCodec) Decode(value string) (PopulationMappingCursor, error) {
	if c == nil || len(c.key) == 0 {
		return PopulationMappingCursor{}, fmt.Errorf("population mapping cursor codec is not configured")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return PopulationMappingCursor{}, fmt.Errorf("invalid population mapping cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return PopulationMappingCursor{}, fmt.Errorf("invalid population mapping cursor")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(signature, c.mac(payload)) {
		return PopulationMappingCursor{}, fmt.Errorf("invalid population mapping cursor signature")
	}
	var cursor PopulationMappingCursor
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return PopulationMappingCursor{}, fmt.Errorf("invalid population mapping cursor")
	}
	if err := validatePopulationMappingCursor(cursor); err != nil {
		return PopulationMappingCursor{}, err
	}
	return cursor, nil
}

func (c *HMACPopulationMappingCursorCodec) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, c.key)
	_, _ = h.Write(payload)
	return h.Sum(nil)
}

func validatePopulationMappingCursor(cursor PopulationMappingCursor) error {
	if cursor.Version != 1 || strings.TrimSpace(cursor.ReceiptID) == "" || strings.TrimSpace(cursor.OutputID) == "" || strings.TrimSpace(cursor.Project) == "" || strings.TrimSpace(cursor.ExplorerID) == "" || strings.TrimSpace(cursor.Generation) == "" || strings.TrimSpace(cursor.ScopeDigest) == "" || strings.TrimSpace(cursor.SelectionRevisionID) == "" || strings.TrimSpace(cursor.MembershipDigest) == "" || strings.TrimSpace(cursor.ResourceType) == "" || strings.TrimSpace(cursor.MemberKey) == "" {
		return fmt.Errorf("invalid population mapping cursor")
	}
	return nil
}
