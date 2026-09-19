package artifactfs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

var ErrArtifactUnavailable = errors.New("artifact is not complete or has expired")

type Store struct {
	root string
	now  func() time.Time
	mu   sync.Mutex
}

func New(root string) (*Store, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "" || root == "." || root == string(filepath.Separator) {
		return nil, fmt.Errorf("artifact directory must be a dedicated path")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create artifact directory: %w", err)
	}
	return &Store{root: root, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *Store) Begin(ctx context.Context, record explorer.ArtifactRecord) (explorer.ArtifactStage, *explorer.ArtifactRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := validateRecord(record); err != nil {
		return nil, nil, err
	}
	if !record.ExpiresAt.After(s.now()) {
		return nil, nil, fmt.Errorf("artifact expiry must be in the future")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, err := s.read(record.ID); err == nil {
		if !sameIdentity(existing, record) {
			return nil, nil, fmt.Errorf("artifact id collision")
		}
		if existing.ExpiresAt.After(s.now()) {
			return nil, &existing, nil
		}
		for _, suffix := range []string{".staging", ".zip", ".json"} {
			if removeErr := os.Remove(s.path(record.ID, suffix)); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				return nil, nil, fmt.Errorf("replace retryable artifact: %w", removeErr)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	stagingPath := s.path(record.ID, ".staging")
	file, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("create artifact staging file: %w", err)
	}
	record.State = explorer.ArtifactPreparing
	record.ArchiveSHA256, record.FailureCode = "", ""
	if record.CreatedAt.IsZero() {
		record.CreatedAt = s.now()
	}
	if err := s.writeRecord(record); err != nil {
		_ = file.Close()
		_ = os.Remove(stagingPath)
		return nil, nil, err
	}
	return &stage{store: s, file: file, record: record, stagingPath: stagingPath}, nil, nil
}

func (s *Store) Get(ctx context.Context, id string) (*explorer.ArtifactRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !safeID(id) {
		return nil, os.ErrNotExist
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, err := s.read(id)
	if err != nil {
		return nil, err
	}
	return &record, nil
}

func (s *Store) Open(ctx context.Context, id string) (io.ReadCloser, *explorer.ArtifactRecord, error) {
	record, err := s.Get(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if record.State != explorer.ArtifactComplete || !record.ExpiresAt.After(s.now()) {
		return nil, record, ErrArtifactUnavailable
	}
	file, err := os.Open(s.path(id, ".zip"))
	if err != nil {
		return nil, record, err
	}
	return file, record, nil
}

type stage struct {
	store       *Store
	file        *os.File
	record      explorer.ArtifactRecord
	stagingPath string
	closed      bool
}

func (s *stage) Write(value []byte) (int, error) {
	if s.closed {
		return 0, os.ErrClosed
	}
	return s.file.Write(value)
}

func (s *stage) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.file.Close()
}

func (s *stage) Commit(ctx context.Context, completion explorer.ArtifactCompletion) (*explorer.ArtifactRecord, error) {
	if err := ctx.Err(); err != nil {
		_, _ = s.Abort(context.WithoutCancel(ctx), "CANCELED")
		return nil, err
	}
	if strings.TrimSpace(completion.ArchiveSHA256) == "" || completion.Bytes <= 0 || completion.Rows < 0 || completion.Features < 0 {
		return nil, fmt.Errorf("artifact completion metadata is invalid")
	}
	if err := s.file.Sync(); err != nil {
		return nil, err
	}
	if err := s.Close(); err != nil {
		return nil, err
	}
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if err := os.Rename(s.stagingPath, s.store.path(s.record.ID, ".zip")); err != nil {
		return nil, fmt.Errorf("finalize artifact archive: %w", err)
	}
	completedAt := completion.CompletedAt.UTC()
	if completedAt.IsZero() {
		completedAt = s.store.now()
	}
	s.record.State = explorer.ArtifactComplete
	s.record.ArchiveSHA256 = completion.ArchiveSHA256
	s.record.Bytes, s.record.Rows, s.record.Features = completion.Bytes, completion.Rows, completion.Features
	s.record.CompletedAt = &completedAt
	if err := s.store.writeRecord(s.record); err != nil {
		_ = os.Remove(s.store.path(s.record.ID, ".zip"))
		return nil, err
	}
	copy := s.record
	return &copy, nil
}

func (s *stage) Abort(_ context.Context, failureCode string) (*explorer.ArtifactRecord, error) {
	_ = s.Close()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	_ = os.Remove(s.stagingPath)
	s.record.State = explorer.ArtifactFailed
	s.record.FailureCode = strings.TrimSpace(failureCode)
	if s.record.FailureCode == "" {
		s.record.FailureCode = "ARTIFACT_PREPARATION_FAILED"
	}
	if err := s.store.writeRecord(s.record); err != nil {
		return nil, err
	}
	copy := s.record
	return &copy, nil
}

func (s *Store) path(id, suffix string) string { return filepath.Join(s.root, id+suffix) }

func (s *Store) read(id string) (explorer.ArtifactRecord, error) {
	raw, err := os.ReadFile(s.path(id, ".json"))
	if err != nil {
		return explorer.ArtifactRecord{}, err
	}
	var record explorer.ArtifactRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return explorer.ArtifactRecord{}, fmt.Errorf("decode artifact metadata: %w", err)
	}
	if record.ID != id {
		return explorer.ArtifactRecord{}, fmt.Errorf("artifact metadata identity mismatch")
	}
	return record, nil
}

func (s *Store) writeRecord(record explorer.ArtifactRecord) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(s.root, record.ID+".metadata-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, s.path(record.ID, ".json"))
}

func validateRecord(record explorer.ArtifactRecord) error {
	if !safeID(record.ID) || strings.TrimSpace(record.Project) == "" || strings.TrimSpace(record.ExplorerID) == "" || strings.TrimSpace(record.RevisionID) == "" || strings.TrimSpace(record.OutputID) == "" || strings.TrimSpace(record.ExecutionID) == "" || record.ExpiresAt.IsZero() {
		return fmt.Errorf("artifact record identity and expiry are required")
	}
	return nil
}

func safeID(id string) bool {
	if !strings.HasPrefix(id, "artifact_") || len(id) != len("artifact_")+64 {
		return false
	}
	for _, char := range strings.TrimPrefix(id, "artifact_") {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func sameIdentity(left, right explorer.ArtifactRecord) bool {
	return left.ID == right.ID && left.Project == right.Project && left.ExplorerID == right.ExplorerID && left.RevisionID == right.RevisionID && left.OutputID == right.OutputID && left.ReceiptID == right.ReceiptID && left.ExecutionID == right.ExecutionID && left.IdempotencyKey == right.IdempotencyKey
}
