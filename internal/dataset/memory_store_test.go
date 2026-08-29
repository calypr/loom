package dataset

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"
)

// MemoryLifecycleStore is a concurrency-correct test implementation.
type MemoryLifecycleStore struct {
	mu             sync.RWMutex
	snapshots      map[Ref]SnapshotGeneration
	releases       map[string]ActiveRelease
	releaseRecords map[string]ProjectRelease
}

func NewMemoryLifecycleStore() *MemoryLifecycleStore {
	return &MemoryLifecycleStore{snapshots: make(map[Ref]SnapshotGeneration), releases: make(map[string]ActiveRelease), releaseRecords: make(map[string]ProjectRelease)}
}

func (s *MemoryLifecycleStore) SaveRelease(_ context.Context, release ProjectRelease) (ProjectRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := release.Project + "\x00" + release.ID
	if existing, ok := s.releaseRecords[key]; ok {
		if !reflect.DeepEqual(existing, release) {
			return ProjectRelease{}, ErrSnapshotConflict
		}
		return cloneRelease(existing), nil
	}
	s.releaseRecords[key] = cloneRelease(release)
	return cloneRelease(release), nil
}

func (s *MemoryLifecycleStore) ReadRelease(_ context.Context, project, releaseID string) (ProjectRelease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	release, ok := s.releaseRecords[project+"\x00"+releaseID]
	if !ok {
		return ProjectRelease{}, ErrReleaseNotFound
	}
	return cloneRelease(release), nil
}

func (s *MemoryLifecycleStore) CreateOrResumeSnapshot(_ context.Context, candidate SnapshotGeneration) (SnapshotGeneration, error) {
	if err := candidate.Validate(); err != nil {
		return SnapshotGeneration{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.snapshots[candidate.Dataset]; ok {
		if existing.GitCommit != candidate.GitCommit || existing.AuthResourcePath != candidate.AuthResourcePath || !reflect.DeepEqual(existing.ExpectedResourceTypes, candidate.ExpectedResourceTypes) {
			return SnapshotGeneration{}, ErrSnapshotConflict
		}
		return cloneSnapshot(existing), nil
	}
	s.snapshots[candidate.Dataset] = cloneSnapshot(candidate)
	return cloneSnapshot(candidate), nil
}

func (s *MemoryLifecycleStore) ReadSnapshot(_ context.Context, ref Ref) (SnapshotGeneration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.snapshots[ref]
	if !ok {
		return SnapshotGeneration{}, ErrSnapshotNotFound
	}
	return cloneSnapshot(snapshot), nil
}

func (s *MemoryLifecycleStore) RecordSnapshotUpload(_ context.Context, ref Ref, upload ResourceUpload) (SnapshotGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[ref]
	if !ok {
		return SnapshotGeneration{}, ErrSnapshotNotFound
	}
	if snapshot.State != StateLoading {
		if prior, found := snapshot.Upload(upload.ResourceType); found && prior.SHA256 == upload.SHA256 && prior.Size == upload.Size {
			return cloneSnapshot(snapshot), nil
		}
		return SnapshotGeneration{}, ErrSnapshotFinalized
	}
	if !contains(snapshot.ExpectedResourceTypes, upload.ResourceType) {
		return SnapshotGeneration{}, fmt.Errorf("%w: resource type %q was not declared", ErrSnapshotConflict, upload.ResourceType)
	}
	for i, prior := range snapshot.Uploads {
		if prior.ResourceType != upload.ResourceType {
			continue
		}
		if prior.SHA256 != upload.SHA256 || prior.Size != upload.Size {
			return SnapshotGeneration{}, ErrChecksumConflict
		}
		snapshot.Uploads[i] = prior
		return cloneSnapshot(snapshot), nil
	}
	snapshot.Uploads = append(snapshot.Uploads, upload)
	sort.Slice(snapshot.Uploads, func(i, j int) bool { return snapshot.Uploads[i].ResourceType < snapshot.Uploads[j].ResourceType })
	snapshot.UpdatedAt = upload.UploadedAt
	s.snapshots[ref] = snapshot
	return cloneSnapshot(snapshot), nil
}

func (s *MemoryLifecycleStore) TransitionSnapshot(_ context.Context, ref Ref, expected, next State, now time.Time) (SnapshotGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[ref]
	if !ok {
		return SnapshotGeneration{}, ErrSnapshotNotFound
	}
	if snapshot.State == next {
		return cloneSnapshot(snapshot), nil
	}
	if snapshot.State != expected || expected != StateLoading || (next != StateStaged && next != StateFailed) {
		return SnapshotGeneration{}, fmt.Errorf("%w: %s -> %s", ErrSnapshotConflict, snapshot.State, next)
	}
	if next == StateStaged && len(snapshot.MissingResourceTypes()) != 0 {
		return SnapshotGeneration{}, ErrGenerationIncomplete
	}
	snapshot.State = next
	snapshot.UpdatedAt = now.UTC()
	if next == StateFailed {
		aborted := now.UTC()
		snapshot.AbortedAt = &aborted
	}
	s.snapshots[ref] = snapshot
	return cloneSnapshot(snapshot), nil
}

func (s *MemoryLifecycleStore) ReadActiveRelease(_ context.Context, project string) (ActiveRelease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	release, ok := s.releases[project]
	if !ok {
		return ActiveRelease{}, ErrNoActiveRelease
	}
	return cloneActiveRelease(release), nil
}

func (s *MemoryLifecycleStore) CompareAndSwapActivateRelease(_ context.Context, release ProjectRelease, expectedRevision int64) (ActiveRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.releases[release.Project]
	if current.Revision == expectedRevision+1 && current.Release.ID == release.ID {
		return cloneActiveRelease(current), nil
	}
	if current.Revision != expectedRevision {
		return ActiveRelease{}, ErrReleaseActivationConflict
	}
	if release.Project == "" || release.Generation == "" || release.GitCommit != release.Generation {
		return ActiveRelease{}, fmt.Errorf("invalid project release")
	}
	active := ActiveRelease{Release: cloneRelease(release), Revision: expectedRevision + 1}
	s.releases[release.Project] = active
	return cloneActiveRelease(active), nil
}

func cloneSnapshot(value SnapshotGeneration) SnapshotGeneration {
	value.ExpectedResourceTypes = append([]string(nil), value.ExpectedResourceTypes...)
	value.Uploads = append([]ResourceUpload(nil), value.Uploads...)
	if value.AbortedAt != nil {
		copy := *value.AbortedAt
		value.AbortedAt = &copy
	}
	return value
}

func cloneRelease(value ProjectRelease) ProjectRelease {
	value.Publications = append([]ReleasePublication(nil), value.Publications...)
	value.RequiredVerifications = append([]ContractVerification(nil), value.RequiredVerifications...)
	return value
}

func cloneActiveRelease(value ActiveRelease) ActiveRelease {
	value.Release = cloneRelease(value.Release)
	return value
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var _ SnapshotRepository = (*MemoryLifecycleStore)(nil)
var _ ReleaseRepository = (*MemoryLifecycleStore)(nil)
