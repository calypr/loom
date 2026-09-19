package explorer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type ArtifactState string

const (
	ArtifactPreparing ArtifactState = "PREPARING"
	ArtifactComplete  ArtifactState = "COMPLETE"
	ArtifactFailed    ArtifactState = "FAILED"
)

// ArtifactRecord is the durable server-side identity of one exact training
// artifact. The archive is downloadable only when State is COMPLETE.
type ArtifactRecord struct {
	ID                       string        `json:"id"`
	Project                  string        `json:"project"`
	ExplorerID               string        `json:"explorerId"`
	RevisionID               string        `json:"revisionId"`
	OutputID                 string        `json:"outputId"`
	ReceiptID                string        `json:"receiptId"`
	ExecutionID              string        `json:"executionId"`
	DatasetGeneration        string        `json:"datasetGeneration"`
	SchemaDigest             string        `json:"schemaDigest"`
	AuthorizationScopeDigest string        `json:"authorizationScopeDigest"`
	SnapshotToken            string        `json:"snapshotToken"`
	IdempotencyKey           string        `json:"idempotencyKey"`
	State                    ArtifactState `json:"state"`
	Filename                 string        `json:"filename"`
	MediaType                string        `json:"mediaType"`
	ArchiveSHA256            string        `json:"archiveSha256,omitempty"`
	Bytes                    int64         `json:"bytes,omitempty"`
	Rows                     int64         `json:"rows,omitempty"`
	Features                 int           `json:"features,omitempty"`
	FailureCode              string        `json:"failureCode,omitempty"`
	CreatedAt                time.Time     `json:"createdAt"`
	CompletedAt              *time.Time    `json:"completedAt,omitempty"`
	ExpiresAt                time.Time     `json:"expiresAt"`
}

func ArtifactID(record ArtifactRecord) (string, error) {
	identity := struct {
		Project, ExplorerID, RevisionID, OutputID, ReceiptID, ExecutionID, IdempotencyKey string
	}{
		Project: strings.TrimSpace(record.Project), ExplorerID: strings.TrimSpace(record.ExplorerID),
		RevisionID: strings.TrimSpace(record.RevisionID), OutputID: strings.TrimSpace(record.OutputID),
		ReceiptID: strings.TrimSpace(record.ReceiptID), ExecutionID: strings.TrimSpace(record.ExecutionID),
		IdempotencyKey: strings.TrimSpace(record.IdempotencyKey),
	}
	if identity.Project == "" || identity.ExplorerID == "" || identity.RevisionID == "" || identity.OutputID == "" || identity.ReceiptID == "" || identity.ExecutionID == "" || identity.IdempotencyKey == "" {
		return "", fmt.Errorf("artifact identity and idempotency key are required")
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return "artifact_" + hex.EncodeToString(digest[:]), nil
}

type ArtifactCompletion struct {
	ArchiveSHA256 string
	Bytes         int64
	Rows          int64
	Features      int
	CompletedAt   time.Time
}

// ArtifactStage is an uncommitted archive. Abort must be idempotent and Commit
// must make the completed file and metadata visible atomically to readers.
type ArtifactStage interface {
	io.WriteCloser
	Commit(context.Context, ArtifactCompletion) (*ArtifactRecord, error)
	Abort(context.Context, string) (*ArtifactRecord, error)
}

type ArtifactStore interface {
	Begin(context.Context, ArtifactRecord) (ArtifactStage, *ArtifactRecord, error)
	Get(context.Context, string) (*ArtifactRecord, error)
	Open(context.Context, string) (io.ReadCloser, *ArtifactRecord, error)
}
