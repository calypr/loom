package dataset

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

type MemoryLifecycleStore struct {
	mu             sync.RWMutex
	manifests      map[Ref]Manifest
	releases       map[string]ActiveRelease
	releaseRecords map[string]ProjectRelease
}

func NewMemoryLifecycleStore() *MemoryLifecycleStore {
	return &MemoryLifecycleStore{manifests: make(map[Ref]Manifest), releases: make(map[string]ActiveRelease), releaseRecords: make(map[string]ProjectRelease)}
}

func (s *MemoryLifecycleStore) ReadManifest(_ context.Context, ref Ref) (Manifest, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	manifest, ok := s.manifests[ref]
	if !ok {
		return Manifest{}, ErrManifestNotFound
	}
	return manifest, nil
}

func (s *MemoryLifecycleStore) SaveRelease(_ context.Context, release ProjectRelease) (ProjectRelease, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := release.Project + "\x00" + release.ID
	if existing, ok := s.releaseRecords[key]; ok {
		if !reflect.DeepEqual(existing, release) {
			return ProjectRelease{}, fmt.Errorf("release already exists with different content")
		}
		return cloneRelease(existing), nil
	}
	s.releaseRecords[key] = cloneRelease(release)
	return cloneRelease(release), nil
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

func cloneRelease(value ProjectRelease) ProjectRelease {
	value.Publications = append([]ReleasePublication(nil), value.Publications...)
	value.RequiredVerifications = append([]ContractVerification(nil), value.RequiredVerifications...)
	return value
}

func cloneActiveRelease(value ActiveRelease) ActiveRelease {
	value.Release = cloneRelease(value.Release)
	return value
}

var _ ManifestReader = (*MemoryLifecycleStore)(nil)
var _ ReleaseRepository = (*MemoryLifecycleStore)(nil)
