package sourceload

import (
	"crypto/sha256"
	arangostore "github.com/calypr/loom/internal/store/arango"
	"time"
)

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

func newSourceLeaseOwner() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("create source lease owner: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}

func numericInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	default:
		return 0
	}
}

const (
	sourceLoadClaimMillis = int64(2 * time.Minute / time.Millisecond)
	sourceLoadCopyMillis  = int64(2 * time.Minute / time.Millisecond)
)

type sourceLoadRecord struct {
	Key                  string                       `json:"_key"`
	Project              string                       `json:"project"`
	OperationID          string                       `json:"operation_id"`
	Plan                 SourceLoadPlan               `json:"plan"`
	PlanHash             string                       `json:"plan_hash"`
	Generation           string                       `json:"generation"`
	State                string                       `json:"state"`
	BaseRevision         int64                        `json:"base_revision"`
	SourceGeneration     string                       `json:"source_generation,omitempty"`
	LegacySourceRevision int64                        `json:"legacy_source_revision"`
	LegacySourceTracked  bool                         `json:"legacy_source_tracked"`
	LegacyRepository     string                       `json:"legacy_repository,omitempty"`
	SourceMode           string                       `json:"source_mode"`
	CopyComplete         bool                         `json:"copy_complete"`
	Receipts             map[string]SourceFileReceipt `json:"receipts"`
	FileDigests          map[string]string            `json:"file_digests,omitempty"`
	FileClaims           map[string]sourceLoadClaim   `json:"file_claims,omitempty"`
	CopyOwner            string                       `json:"copy_owner,omitempty"`
	CopyExpiresAt        int64                        `json:"copy_expires_at,omitempty"`
	PublishOwner         string                       `json:"publish_owner,omitempty"`
	PublishExpiresAt     int64                        `json:"publish_expires_at,omitempty"`
	ManifestHash         string                       `json:"manifest_hash,omitempty"`
	ExecutionID          string                       `json:"execution_id,omitempty"`
	SourceRevision       int64                        `json:"source_revision,omitempty"`
	CreatedAt            time.Time                    `json:"created_at"`
	UpdatedAt            time.Time                    `json:"updated_at"`
}

type sourceLoadClaim struct {
	Owner     string `json:"owner"`
	ExpiresAt int64  `json:"expires_at"`
}

type sourceLoadBase struct {
	Revision             int64
	Generation           string
	Mode                 string
	LegacySourceRevision int64
	LegacySourceTracked  bool
	LegacyRepository     string
	LegacyFileScopes     map[string]string
}

type legacySourceSnapshot struct {
	Exists         bool
	SourceTracked  bool
	SourceRevision int64
	Repository     string
	Pending        string
	PendingPhase   string
	LeaseOwner     string
	LeaseExpiresAt int64
	FileScopes     map[string]string
}

func sourceLoadDocumentKey(project, operationID string) string {
	digest := sha256.Sum256([]byte(project + "\x00" + operationID))
	return hex.EncodeToString(digest[:])
}

func sourceLoadGeneration(project, operationID string) string {
	digest := sha256.Sum256([]byte("loom-source-load-generation-v1\x00" + project + "\x00" + operationID))
	return "source-" + hex.EncodeToString(digest[:])
}

func sourceLoadPlanHash(plan SourceLoadPlan) (string, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return "", fmt.Errorf("encode source-load plan identity: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func sourceLoadBootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{
		Name: sourceLoadCollection, Indexes: [][]string{{"project", "operation_id"}, {"project", "state"}},
	}}}
}

func sourceLoadNumber(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		value, _ := typed.Int64()
		return value
	default:
		return 0
	}
}

func legacySourceStateKey(project string) string {
	digest := sha256.Sum256([]byte(project))
	return hex.EncodeToString(digest[:])
}
