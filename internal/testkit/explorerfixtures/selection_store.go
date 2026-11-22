package explorerfixtures

import (
	"context"
	explorerselection "github.com/calypr/loom/internal/explorer/selection"
	"sort"
	"sync"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

type SelectionStore struct {
	*Store
	SelectionMu sync.Mutex
	Selections  map[string]explorerselection.SelectionRevision
	Members     map[string][]explorerselection.SelectionMember
	SaveCalls   int
}

func (s *SelectionStore) SaveDraft(ctx context.Context, value explorer.Explorer, expected int64, expectedDigest ...string) (*explorer.Explorer, error) {
	s.SaveCalls++
	return s.Store.SaveDraft(ctx, value, expected, expectedDigest...)
}

func (s *SelectionStore) GetSelection(_ context.Context, project, id string) (*explorerselection.SelectionRevision, error) {
	s.SelectionMu.Lock()
	defer s.SelectionMu.Unlock()
	header, ok := s.Selections[id]
	if !ok || header.Project != project {
		return nil, explorerselection.ErrSelectionNotFound
	}
	copy := header.Canonical()
	return &copy, nil
}

func (s *SelectionStore) VisitSelectionMembers(_ context.Context, project, id, after string, limit int, visit func(explorerselection.SelectionMember) error) (string, error) {
	s.SelectionMu.Lock()
	members, ok := s.Members[id]
	if !ok || s.Selections[id].Project != project {
		s.SelectionMu.Unlock()
		return "", explorerselection.ErrSelectionNotFound
	}
	ordered := append([]explorerselection.SelectionMember(nil), members...)
	s.SelectionMu.Unlock()
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Ref.ID < ordered[j].Ref.ID })
	start := 0
	for start < len(ordered) && ordered[start].Ref.ID <= after {
		start++
	}
	if limit <= 0 {
		limit = len(ordered)
	}
	end := start + limit
	if end > len(ordered) {
		end = len(ordered)
	}
	for _, member := range ordered[start:end] {
		if err := visit(member.Canonical()); err != nil {
			return "", err
		}
	}
	if end == start || end == len(ordered) {
		return "", nil
	}
	return ordered[end-1].Ref.ID, nil
}

func (s *SelectionStore) BeginSelection(_ context.Context, header explorerselection.SelectionRevision, _ string) (*explorerselection.SelectionRevision, error) {
	s.SelectionMu.Lock()
	defer s.SelectionMu.Unlock()
	if current, ok := s.Selections[header.ID]; ok && current.Complete {
		copy := current.Canonical()
		return &copy, nil
	}
	header = header.Canonical()
	s.Selections[header.ID] = header
	s.Members[header.ID] = []explorerselection.SelectionMember{}
	copy := header.Canonical()
	return &copy, nil
}

func (s *SelectionStore) AppendSelectionMembers(_ context.Context, id, _ string, members []explorerselection.SelectionMember) ([]explorerselection.SelectionMember, error) {
	s.SelectionMu.Lock()
	defer s.SelectionMu.Unlock()
	if _, ok := s.Selections[id]; !ok {
		return nil, explorerselection.ErrSelectionNotFound
	}
	added := make([]explorerselection.SelectionMember, 0, len(members))
	for _, member := range members {
		member = member.Canonical()
		duplicate := false
		for _, prior := range s.Members[id] {
			if prior.Ref == member.Ref {
				duplicate = true
				break
			}
		}
		if !duplicate {
			s.Members[id] = append(s.Members[id], member)
			added = append(added, member)
		}
	}
	return added, nil
}

func (s *SelectionStore) DigestSelectionMembers(_ context.Context, project, id string) (string, int64, int64, error) {
	s.SelectionMu.Lock()
	defer s.SelectionMu.Unlock()
	members, ok := s.Members[id]
	if !ok || s.Selections[id].Project != project {
		return "", 0, 0, explorerselection.ErrSelectionNotFound
	}
	var bytes int64
	for _, member := range members {
		ref := member.Ref.Canonical()
		for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
			bytes += int64(8 + len(value))
		}
	}
	return explorerselection.MembershipDigest(members), int64(len(members)), bytes, nil
}

func (s *SelectionStore) CompleteSelection(_ context.Context, id, _ string, digest string, count, bytes int64, completedAt time.Time) (*explorerselection.SelectionRevision, error) {
	s.SelectionMu.Lock()
	defer s.SelectionMu.Unlock()
	header, ok := s.Selections[id]
	if !ok {
		return nil, explorerselection.ErrSelectionNotFound
	}
	header.MembershipDigest, header.MemberCount, header.MemberBytes = digest, count, bytes
	header.Complete, header.CompletedAt = true, &completedAt
	s.Selections[id] = header
	copy := header.Canonical()
	return &copy, nil
}

func (s *SelectionStore) AbortSelection(_ context.Context, id, _ string) error {
	s.SelectionMu.Lock()
	defer s.SelectionMu.Unlock()
	delete(s.Selections, id)
	delete(s.Members, id)
	return nil
}

func (s *SelectionStore) CleanupSelectionStaging(context.Context, time.Time, int) error {
	return nil
}
