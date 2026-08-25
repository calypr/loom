// Package capabilitystore contains persistence adapters for immutable
// Explorer capability snapshots.
package capabilitystore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"github.com/calypr/loom/internal/explorer/capability"
)

var (
	ErrNotFound        = errors.New("capability snapshot not found")
	ErrInvalidSnapshot = errors.New("invalid capability snapshot")
	ErrTokenCollision  = errors.New("capability snapshot token collision")
)

// Repository persists complete snapshots. Put is content-addressed and never
// changes an existing document. GetByIdentity returns a READY snapshot when
// one exists; otherwise it returns the best available diagnostic snapshot.
type Repository interface {
	GetByToken(context.Context, string) (*capability.Snapshot, error)
	GetByIdentity(context.Context, capability.SnapshotIdentity) (*capability.Snapshot, error)
	Put(context.Context, capability.Snapshot) (*capability.Snapshot, error)
}

// Store is retained as a short name for callers that use stores throughout
// the rest of Explorer.
type Store = Repository

type identityKey struct {
	Identity capability.SnapshotIdentity
}

// MemoryStore is a concurrency-safe, defensive-copy repository for tests and
// local deployments. The values held by the store are never handed directly
// to callers.
type MemoryStore struct {
	mu         sync.RWMutex
	byToken    map[string]capability.Snapshot
	byIdentity map[identityKey]map[string]struct{}
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byToken: make(map[string]capability.Snapshot), byIdentity: make(map[identityKey]map[string]struct{})}
}

func NewMemoryRepository() *MemoryStore { return NewMemoryStore() }

func (s *MemoryStore) Put(ctx context.Context, snapshot capability.Snapshot) (*capability.Snapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if err := validate(snapshot); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.byToken[snapshot.Token]; ok {
		if !bytes.Equal(previous.CanonicalPayload(), snapshot.CanonicalPayload()) {
			return nil, fmt.Errorf("%w: %s", ErrTokenCollision, snapshot.Token)
		}
		out := clone(previous)
		return &out, nil
	}

	stored := clone(snapshot)
	s.byToken[stored.Token] = stored
	key := identityKey{Identity: stored.Identity}
	if s.byIdentity[key] == nil {
		s.byIdentity[key] = make(map[string]struct{})
	}
	s.byIdentity[key][stored.Token] = struct{}{}
	out := clone(stored)
	return &out, nil
}

func (s *MemoryStore) GetByToken(ctx context.Context, token string) (*capability.Snapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.byToken[token]
	if !ok {
		return nil, ErrNotFound
	}
	out := clone(value)
	return &out, nil
}

func (s *MemoryStore) GetByIdentity(ctx context.Context, identity capability.SnapshotIdentity) (*capability.Snapshot, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tokens := s.byIdentity[identityKey{Identity: identity}]
	if len(tokens) == 0 {
		return nil, ErrNotFound
	}
	ordered := make([]string, 0, len(tokens))
	for token := range tokens {
		ordered = append(ordered, token)
	}
	sort.Strings(ordered)
	for _, status := range []capability.Status{capability.StatusReady, capability.StatusBuilding, capability.StatusFailed} {
		for _, token := range ordered {
			value := s.byToken[token]
			if value.Status == status {
				out := clone(value)
				return &out, nil
			}
		}
	}
	return nil, ErrNotFound
}

func validate(snapshot capability.Snapshot) error {
	if snapshot.Token == "" {
		return fmt.Errorf("%w: token is required", ErrInvalidSnapshot)
	}
	if snapshot.Status == capability.StatusReady && !snapshot.Usable() {
		return fmt.Errorf("%w: READY snapshot must be complete and not truncated", ErrInvalidSnapshot)
	}
	want := snapshotToken(snapshot)
	if snapshot.Token != want {
		return fmt.Errorf("%w: token does not match canonical snapshot content", ErrInvalidSnapshot)
	}
	return nil
}

// ValidateSnapshot applies the repository invariants without requiring a
// caller to select a particular adapter.
func ValidateSnapshot(snapshot capability.Snapshot) error { return validate(snapshot) }

func snapshotToken(snapshot capability.Snapshot) string {
	sum := sha256.Sum256(snapshot.CanonicalPayload())
	return "sha256:" + hex.EncodeToString(sum[:])
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func clone(in capability.Snapshot) capability.Snapshot {
	out := in.Clone()
	for i := range out.Diagnostics {
		out.Diagnostics[i].Details = cloneDetails(out.Diagnostics[i].Details)
	}
	return out
}

// cloneDetails recursively copies JSON-like diagnostic values. Snapshot.Clone
// already copies the outer map; this closes the less obvious alias through a
// nested map or slice in Details.
func cloneDetails(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		cloned := cloneValue(reflect.ValueOf(value))
		if cloned.IsValid() {
			out[key] = cloned.Interface()
		} else {
			out[key] = nil
		}
	}
	return out
}

func cloneValue(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return reflect.ValueOf((*any)(nil)).Elem()
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		copied := cloneValue(value.Elem())
		out := reflect.New(value.Type()).Elem()
		out.Set(copied)
		return out
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			out.SetMapIndex(cloneValue(iter.Key()), cloneValue(iter.Value()))
		}
		return out
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		reflect.Copy(out, value)
		for i := 0; i < out.Len(); i++ {
			out.Index(i).Set(cloneValue(value.Index(i)))
		}
		return out
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		out := reflect.New(value.Type().Elem())
		out.Elem().Set(cloneValue(value.Elem()))
		return out
	default:
		return value
	}
}

var _ Repository = (*MemoryStore)(nil)
