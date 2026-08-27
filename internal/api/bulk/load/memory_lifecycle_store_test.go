package load

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/calypr/loom/internal/dataset"
)

type memoryLifecycleStore struct {
	mu             sync.RWMutex
	snapshots      map[dataset.Ref]dataset.SnapshotGeneration
	releases       map[string]dataset.ActiveRelease
	releaseRecords map[string]dataset.ProjectRelease
}

func newMemoryLifecycleStore() *memoryLifecycleStore {
	return &memoryLifecycleStore{
		snapshots:      make(map[dataset.Ref]dataset.SnapshotGeneration),
		releases:       make(map[string]dataset.ActiveRelease),
		releaseRecords: make(map[string]dataset.ProjectRelease),
	}
}

func (s *memoryLifecycleStore) SaveRelease(_ context.Context, release dataset.ProjectRelease) (dataset.ProjectRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := release.Project + "\x00" + release.ID
	if existing, ok := s.releaseRecords[key]; ok {
		if !reflect.DeepEqual(existing, release) {
			return dataset.ProjectRelease{}, dataset.ErrSnapshotConflict
		}
		return cloneTestRelease(existing), nil
	}
	s.releaseRecords[key] = cloneTestRelease(release)
	return cloneTestRelease(release), nil
}

func (s *memoryLifecycleStore) ReadRelease(_ context.Context, project, releaseID string) (dataset.ProjectRelease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	release, ok := s.releaseRecords[project+"\x00"+releaseID]
	if !ok {
		return dataset.ProjectRelease{}, dataset.ErrReleaseNotFound
	}
	return cloneTestRelease(release), nil
}

func (s *memoryLifecycleStore) CreateOrResumeSnapshot(_ context.Context, candidate dataset.SnapshotGeneration) (dataset.SnapshotGeneration, error) {
	if err := candidate.Validate(); err != nil {
		return dataset.SnapshotGeneration{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.snapshots[candidate.Dataset]; ok {
		if existing.GitCommit != candidate.GitCommit || existing.AuthResourcePath != candidate.AuthResourcePath || !reflect.DeepEqual(existing.ExpectedResourceTypes, candidate.ExpectedResourceTypes) {
			return dataset.SnapshotGeneration{}, dataset.ErrSnapshotConflict
		}
		return cloneTestSnapshot(existing), nil
	}
	s.snapshots[candidate.Dataset] = cloneTestSnapshot(candidate)
	return cloneTestSnapshot(candidate), nil
}

func (s *memoryLifecycleStore) ReadSnapshot(_ context.Context, ref dataset.Ref) (dataset.SnapshotGeneration, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.snapshots[ref]
	if !ok {
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotNotFound
	}
	return cloneTestSnapshot(snapshot), nil
}

func (s *memoryLifecycleStore) RecordSnapshotUpload(_ context.Context, ref dataset.Ref, upload dataset.ResourceUpload) (dataset.SnapshotGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[ref]
	if !ok {
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotNotFound
	}
	if snapshot.State != dataset.StateLoading {
		if prior, found := snapshot.Upload(upload.ResourceType); found && prior.SHA256 == upload.SHA256 && prior.Size == upload.Size {
			return cloneTestSnapshot(snapshot), nil
		}
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotFinalized
	}
	if !includesResource(snapshot.ExpectedResourceTypes, upload.ResourceType) {
		return dataset.SnapshotGeneration{}, fmt.Errorf("%w: resource type %q was not declared", dataset.ErrSnapshotConflict, upload.ResourceType)
	}
	for _, prior := range snapshot.Uploads {
		if prior.ResourceType != upload.ResourceType {
			continue
		}
		if prior.SHA256 != upload.SHA256 || prior.Size != upload.Size {
			return dataset.SnapshotGeneration{}, dataset.ErrChecksumConflict
		}
		return cloneTestSnapshot(snapshot), nil
	}
	snapshot.Uploads = append(snapshot.Uploads, upload)
	sort.Slice(snapshot.Uploads, func(i, j int) bool { return snapshot.Uploads[i].ResourceType < snapshot.Uploads[j].ResourceType })
	snapshot.UpdatedAt = upload.UploadedAt
	s.snapshots[ref] = snapshot
	return cloneTestSnapshot(snapshot), nil
}

func (s *memoryLifecycleStore) TransitionSnapshot(_ context.Context, ref dataset.Ref, expected, next dataset.State, now time.Time) (dataset.SnapshotGeneration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.snapshots[ref]
	if !ok {
		return dataset.SnapshotGeneration{}, dataset.ErrSnapshotNotFound
	}
	if snapshot.State == next {
		return cloneTestSnapshot(snapshot), nil
	}
	if snapshot.State != expected || expected != dataset.StateLoading || (next != dataset.StateStaged && next != dataset.StateFailed) {
		return dataset.SnapshotGeneration{}, fmt.Errorf("%w: %s -> %s", dataset.ErrSnapshotConflict, snapshot.State, next)
	}
	if next == dataset.StateStaged && len(snapshot.MissingResourceTypes()) != 0 {
		return dataset.SnapshotGeneration{}, dataset.ErrGenerationIncomplete
	}
	snapshot.State = next
	snapshot.UpdatedAt = now.UTC()
	if next == dataset.StateFailed {
		aborted := now.UTC()
		snapshot.AbortedAt = &aborted
	}
	s.snapshots[ref] = snapshot
	return cloneTestSnapshot(snapshot), nil
}

func (s *memoryLifecycleStore) ListSnapshotProjects(context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := make(map[string]struct{})
	for ref := range s.snapshots {
		set[ref.Project] = struct{}{}
	}
	return sortedTestProjects(set), nil
}

func (s *memoryLifecycleStore) ReadActiveRelease(_ context.Context, project string) (dataset.ActiveRelease, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	release, ok := s.releases[project]
	if !ok {
		return dataset.ActiveRelease{}, dataset.ErrNoActiveRelease
	}
	return cloneTestActiveRelease(release), nil
}

func (s *memoryLifecycleStore) CompareAndSwapActivateRelease(_ context.Context, release dataset.ProjectRelease, expectedRevision int64) (dataset.ActiveRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.releases[release.Project]
	if current.Revision == expectedRevision+1 && current.Release.ID == release.ID {
		return cloneTestActiveRelease(current), nil
	}
	if current.Revision != expectedRevision {
		return dataset.ActiveRelease{}, dataset.ErrReleaseActivationConflict
	}
	active := dataset.ActiveRelease{Release: cloneTestRelease(release), Revision: expectedRevision + 1}
	s.releases[release.Project] = active
	return cloneTestActiveRelease(active), nil
}

func (s *memoryLifecycleStore) ListReleaseProjects(context.Context) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	set := make(map[string]struct{}, len(s.releases))
	for project := range s.releases {
		set[project] = struct{}{}
	}
	return sortedTestProjects(set), nil
}

func cloneTestSnapshot(value dataset.SnapshotGeneration) dataset.SnapshotGeneration {
	value.ExpectedResourceTypes = append([]string(nil), value.ExpectedResourceTypes...)
	value.Uploads = append([]dataset.ResourceUpload(nil), value.Uploads...)
	if value.AbortedAt != nil {
		copied := *value.AbortedAt
		value.AbortedAt = &copied
	}
	return value
}

func cloneTestRelease(value dataset.ProjectRelease) dataset.ProjectRelease {
	value.Publications = append([]dataset.ReleasePublication(nil), value.Publications...)
	value.RequiredVerifications = append([]dataset.ContractVerification(nil), value.RequiredVerifications...)
	return value
}

func cloneTestActiveRelease(value dataset.ActiveRelease) dataset.ActiveRelease {
	value.Release = cloneTestRelease(value.Release)
	return value
}
func includesResource(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func sortedTestProjects(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

var _ dataset.SnapshotRepository = (*memoryLifecycleStore)(nil)
var _ dataset.ReleaseRepository = (*memoryLifecycleStore)(nil)
