package load

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/calypr/loom/internal/dataset"
)

type SnapshotBlobStore interface {
	Put(context.Context, dataset.Ref, dataset.ResourceUpload, []byte) error
	Directory(context.Context, dataset.Ref) (string, error)
	DeleteGeneration(context.Context, dataset.Ref) error
}

type SnapshotService struct {
	Repository dataset.SnapshotRepository
	Blobs      SnapshotBlobStore
	Runner     GenerationRunner
	Now        func() time.Time
	locks      sync.Map
}

func (s *SnapshotService) CreateOrResume(ctx context.Context, project, gitCommit, authResourcePath string, resourceTypes []string) (dataset.SnapshotGeneration, error) {
	if s == nil || s.Repository == nil || s.Blobs == nil || s.Runner == nil {
		return dataset.SnapshotGeneration{}, fmt.Errorf("snapshot service dependencies are required")
	}
	now := s.now()
	generation, err := dataset.NewSnapshotGeneration(project, gitCommit, authResourcePath, resourceTypes, now)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	return s.Repository.CreateOrResumeSnapshot(ctx, generation)
}

func (s *SnapshotService) Status(ctx context.Context, project, generation string) (dataset.SnapshotGeneration, error) {
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	return s.Repository.ReadSnapshot(ctx, ref)
}

func (s *SnapshotService) Upload(ctx context.Context, project, generation, resourceType, declaredChecksum string, body []byte) (dataset.SnapshotGeneration, error) {
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	digest := sha256.Sum256(body)
	actualChecksum := hex.EncodeToString(digest[:])
	if strings.ToLower(strings.TrimSpace(declaredChecksum)) != actualChecksum {
		return dataset.SnapshotGeneration{}, fmt.Errorf("%w: declared checksum does not match request body", dataset.ErrChecksumConflict)
	}
	upload, err := dataset.NewResourceUpload(resourceType, actualChecksum, int64(len(body)), s.now())
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	snapshot, err := s.Repository.ReadSnapshot(ctx, ref)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	if prior, ok := snapshot.Upload(resourceType); ok {
		if prior.SHA256 != upload.SHA256 || prior.Size != upload.Size {
			return dataset.SnapshotGeneration{}, dataset.ErrChecksumConflict
		}
		return snapshot, nil
	}
	if snapshot.State != dataset.StateLoading {
		if snapshot.State == dataset.StateFailed {
			return dataset.SnapshotGeneration{}, dataset.ErrSnapshotAborted
		}
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotFinalized
	}
	declared := false
	for _, expected := range snapshot.ExpectedResourceTypes {
		if expected == resourceType {
			declared = true
			break
		}
	}
	if !declared {
		return dataset.SnapshotGeneration{}, fmt.Errorf("%w: resource type %q was not declared", dataset.ErrSnapshotConflict, resourceType)
	}
	if err := s.Blobs.Put(ctx, ref, upload, body); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	return s.Repository.RecordSnapshotUpload(ctx, ref, upload)
}

func (s *SnapshotService) Finalize(ctx context.Context, project, generation, submittedBy string) (dataset.SnapshotGeneration, *GenerationLoadResult, error) {
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, nil, err
	}
	lockValue, _ := s.locks.LoadOrStore(ref, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	snapshot, err := s.Repository.ReadSnapshot(ctx, ref)
	if err != nil {
		return dataset.SnapshotGeneration{}, nil, err
	}
	if snapshot.State == dataset.StateStaged {
		return snapshot, nil, nil
	}
	if snapshot.State == dataset.StateFailed {
		return dataset.SnapshotGeneration{}, nil, dataset.ErrSnapshotAborted
	}
	if missing := snapshot.MissingResourceTypes(); len(missing) != 0 {
		return dataset.SnapshotGeneration{}, nil, fmt.Errorf("%w: missing %s", dataset.ErrGenerationIncomplete, strings.Join(missing, ", "))
	}
	directory, err := s.Blobs.Directory(ctx, ref)
	if err != nil {
		return dataset.SnapshotGeneration{}, nil, err
	}
	result, err := runGeneration(s.Runner, ctx, GenerationLoadRequest{
		Project: project, Generation: generation, AuthResourcePath: snapshot.AuthResourcePath,
		StagedDir: directory, SubmittedBy: submittedBy, StageOnly: true,
	})
	if err != nil {
		if reader, ok := s.Repository.(interface {
			ReadManifest(context.Context, dataset.Ref) (dataset.Manifest, error)
		}); ok {
			if manifest, readErr := reader.ReadManifest(ctx, ref); readErr == nil && manifest.IsStaged() {
				staged, transitionErr := s.Repository.TransitionSnapshot(ctx, ref, dataset.StateLoading, dataset.StateStaged, s.now())
				return staged, nil, transitionErr
			}
		}
		_, transitionErr := s.Repository.TransitionSnapshot(context.WithoutCancel(ctx), ref, dataset.StateLoading, dataset.StateFailed, s.now())
		return dataset.SnapshotGeneration{}, nil, errors.Join(err, transitionErr)
	}
	staged, err := s.Repository.TransitionSnapshot(ctx, ref, dataset.StateLoading, dataset.StateStaged, s.now())
	if err != nil {
		return dataset.SnapshotGeneration{}, nil, err
	}
	return staged, result, nil
}

func (s *SnapshotService) Abort(ctx context.Context, project, generation string) (dataset.SnapshotGeneration, error) {
	ref, err := dataset.NewRef(project, generation)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	snapshot, err := s.Repository.ReadSnapshot(ctx, ref)
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	if snapshot.State == dataset.StateStaged {
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotFinalized
	}
	if snapshot.State == dataset.StateFailed {
		if err := s.Blobs.DeleteGeneration(ctx, ref); err != nil {
			return dataset.SnapshotGeneration{}, err
		}
		return snapshot, nil
	}
	aborted, err := s.Repository.TransitionSnapshot(ctx, ref, dataset.StateLoading, dataset.StateFailed, s.now())
	if err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	if err := s.Blobs.DeleteGeneration(ctx, ref); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	return aborted, nil
}

func runGeneration(runner GenerationRunner, ctx context.Context, request GenerationLoadRequest) (*GenerationLoadResult, error) {
	summary, err := runner.RunGeneration(ctx, request, nil)
	if err != nil {
		return nil, err
	}
	return &GenerationLoadResult{Project: request.Project, Generation: request.Generation, AuthResourcePath: request.AuthResourcePath, SubmittedBy: request.SubmittedBy, Summary: &summary}, nil
}

func (s *SnapshotService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

type LocalSnapshotBlobs struct{ Root string }

func (b LocalSnapshotBlobs) Put(_ context.Context, ref dataset.Ref, upload dataset.ResourceUpload, body []byte) error {
	directory := b.generationDirectory(ref)
	if err := os.MkdirAll(directory, 0o750); err != nil {
		return err
	}
	path := filepath.Join(directory, upload.ResourceType+".ndjson")
	if existing, err := os.ReadFile(path); err == nil {
		digest := sha256.Sum256(existing)
		if hex.EncodeToString(digest[:]) != upload.SHA256 {
			return dataset.ErrChecksumConflict
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".upload-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(body); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		existing, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		digest := sha256.Sum256(existing)
		if hex.EncodeToString(digest[:]) != upload.SHA256 {
			return dataset.ErrChecksumConflict
		}
	}
	return nil
}

func (b LocalSnapshotBlobs) Directory(_ context.Context, ref dataset.Ref) (string, error) {
	directory := b.generationDirectory(ref)
	info, err := os.Stat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("snapshot path is not a directory")
	}
	return directory, nil
}

func (b LocalSnapshotBlobs) DeleteGeneration(_ context.Context, ref dataset.Ref) error {
	return os.RemoveAll(b.generationDirectory(ref))
}

func (b LocalSnapshotBlobs) generationDirectory(ref dataset.Ref) string {
	root := b.Root
	if strings.TrimSpace(root) == "" {
		root = filepath.Join(os.TempDir(), "loom-snapshots")
	}
	digest := sha256.Sum256([]byte(ref.Project + "\x00" + ref.Generation))
	return filepath.Join(root, hex.EncodeToString(digest[:]))
}

var _ SnapshotBlobStore = LocalSnapshotBlobs{}
var _ dataset.GenerationBlobCleaner = LocalSnapshotBlobs{}
