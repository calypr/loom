package explorer

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrExplicitGroupRevisionNotFound    = errors.New("explicit group revision not found")
	ErrExplicitGroupRevisionConflict    = errors.New("explicit group revision conflict")
	ErrExplicitGroupRevisionIncomplete  = errors.New("explicit group revision is incomplete")
	ErrExplicitGroupRevisionStaleSource = errors.New("explicit group revision source is stale or mismatched")
	ErrCorruptExplicitGroupRevision     = errors.New("corrupt explicit group revision")
)

type ExplicitGroupRevisionID string

type ExplicitGroupID string

type ExplicitGroupRevisionState string

const (
	ExplicitGroupRevisionStaging  ExplicitGroupRevisionState = "STAGING"
	ExplicitGroupRevisionComplete ExplicitGroupRevisionState = "COMPLETE"
)

// ExplicitGroupRevisionIDFor makes the idempotency key the primary identity
// reservation. Concurrent retries for one project and key contend on the
// same Arango document key.
func ExplicitGroupRevisionIDFor(project, idempotencyKey string) ExplicitGroupRevisionID {
	project = strings.TrimSpace(project)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if project == "" || idempotencyKey == "" {
		return ""
	}
	hash := sha256.New()
	writeDigestField(hash, "loom-explicit-group-revision-id-v1")
	writeDigestField(hash, project)
	writeDigestField(hash, idempotencyKey)
	return ExplicitGroupRevisionID("grouprev_" + hex.EncodeToString(hash.Sum(nil)))
}

// ExplicitGroupDefinition preserves each declared group independently of its
// members, so a group with no members still has a durable identity and order.
type ExplicitGroupDefinition struct {
	ID      ExplicitGroupID `json:"id"`
	Label   string          `json:"label"`
	Ordinal int64           `json:"ordinal"`
}

func (g ExplicitGroupDefinition) Canonical() ExplicitGroupDefinition {
	g.ID = ExplicitGroupID(strings.TrimSpace(string(g.ID)))
	g.Label = strings.TrimSpace(g.Label)
	return g
}

// ExplicitGroupMembership binds one group to one exact resource identity.
// A resource may appear in more than one relation under different group IDs.
type ExplicitGroupMembership struct {
	GroupID ExplicitGroupID `json:"groupId"`
	Ref     ResourceRef     `json:"ref"`
}

func (m ExplicitGroupMembership) Canonical() ExplicitGroupMembership {
	m.GroupID = ExplicitGroupID(strings.TrimSpace(string(m.GroupID)))
	m.Ref = m.Ref.Canonical()
	return m
}

type ExplicitGroupRevision struct {
	ID                        ExplicitGroupRevisionID    `json:"id"`
	Project                   string                     `json:"project"`
	Generation                string                     `json:"generation"`
	ScopeDigest               string                     `json:"scopeDigest"`
	ResourceType              string                     `json:"resourceType"`
	SourceSelectionRevisionID string                     `json:"sourceSelectionRevisionId"`
	SourceMembershipDigest    string                     `json:"sourceMembershipDigest"`
	DefinitionDigest          string                     `json:"definitionDigest"`
	MembershipDigest          string                     `json:"membershipDigest"`
	GroupCount                int64                      `json:"groupCount"`
	MemberCount               int64                      `json:"memberCount"`
	State                     ExplicitGroupRevisionState `json:"state"`
	IdempotencyKey            string                     `json:"idempotencyKey"`
	CreatedAt                 time.Time                  `json:"createdAt"`
	CompletedAt               *time.Time                 `json:"completedAt,omitempty"`
}

func (r ExplicitGroupRevision) Canonical() ExplicitGroupRevision {
	r.ID = ExplicitGroupRevisionID(strings.TrimSpace(string(r.ID)))
	r.Project = strings.TrimSpace(r.Project)
	r.Generation = strings.TrimSpace(r.Generation)
	r.ScopeDigest = strings.TrimSpace(r.ScopeDigest)
	r.ResourceType = strings.TrimSpace(r.ResourceType)
	r.SourceSelectionRevisionID = strings.TrimSpace(r.SourceSelectionRevisionID)
	r.SourceMembershipDigest = strings.TrimSpace(r.SourceMembershipDigest)
	r.DefinitionDigest = strings.TrimSpace(r.DefinitionDigest)
	r.MembershipDigest = strings.TrimSpace(r.MembershipDigest)
	r.IdempotencyKey = strings.TrimSpace(r.IdempotencyKey)
	r.CreatedAt = r.CreatedAt.UTC()
	if r.CompletedAt != nil {
		completedAt := r.CompletedAt.UTC()
		r.CompletedAt = &completedAt
	}
	return r
}

func (r ExplicitGroupRevision) Validate() error {
	r = r.Canonical()
	if r.ID == "" || r.Project == "" || r.Generation == "" || r.ScopeDigest == "" || r.ResourceType == "" || r.SourceSelectionRevisionID == "" || r.SourceMembershipDigest == "" || r.IdempotencyKey == "" || r.CreatedAt.IsZero() {
		return fmt.Errorf("explicit group revision identity, project, generation, scope, resource type, source selection, idempotency key, and creation time are required")
	}
	if r.ID != ExplicitGroupRevisionIDFor(r.Project, r.IdempotencyKey) {
		return fmt.Errorf("explicit group revision ID does not match its project and idempotency key")
	}
	switch r.State {
	case ExplicitGroupRevisionStaging:
		if r.DefinitionDigest != "" || r.MembershipDigest != "" || r.GroupCount != 0 || r.MemberCount != 0 || r.CompletedAt != nil {
			return fmt.Errorf("staging explicit group revision cannot contain completion values")
		}
	case ExplicitGroupRevisionComplete:
		if r.DefinitionDigest == "" || r.MembershipDigest == "" || r.GroupCount <= 0 || r.MemberCount < 0 || r.CompletedAt == nil || r.CompletedAt.IsZero() {
			return ErrExplicitGroupRevisionIncomplete
		}
	default:
		return fmt.Errorf("unsupported explicit group revision state %q", r.State)
	}
	return nil
}

// ValidateSource prevents a group definition created against one source
// selection from being attached to a different project, generation, type, or
// authorization scope later.
func (r ExplicitGroupRevision) ValidateSource(selection SelectionRevision) error {
	r = r.Canonical()
	if err := r.Validate(); err != nil {
		return fmt.Errorf("explicit group revision is invalid: %w", err)
	}
	if r.State != ExplicitGroupRevisionComplete {
		return ErrExplicitGroupRevisionIncomplete
	}
	return r.ValidateSourceSelection(selection)
}

func (r ExplicitGroupRevision) ValidateSourceSelection(selection SelectionRevision) error {
	r = r.Canonical()
	if err := r.Validate(); err != nil {
		return fmt.Errorf("explicit group revision is invalid: %w", err)
	}
	selection = selection.Canonical()
	if err := selection.Validate(); err != nil {
		return fmt.Errorf("%w: source selection is invalid: %v", ErrExplicitGroupRevisionStaleSource, err)
	}
	if !selection.Complete || selection.ID != r.SourceSelectionRevisionID || selection.Project != r.Project || selection.Generation != r.Generation || selection.ResourceType != r.ResourceType || selection.ScopeDigest != r.ScopeDigest || selection.MembershipDigest != r.SourceMembershipDigest {
		return ErrExplicitGroupRevisionStaleSource
	}
	return nil
}

// CanonicalExplicitGroupDefinitions returns presentation order and rejects
// ambiguous IDs or ordinals before a revision is staged.
func CanonicalExplicitGroupDefinitions(groups []ExplicitGroupDefinition) ([]ExplicitGroupDefinition, error) {
	canonical := append([]ExplicitGroupDefinition(nil), groups...)
	ids := make(map[ExplicitGroupID]struct{}, len(canonical))
	ordinals := make(map[int64]struct{}, len(canonical))
	for i := range canonical {
		canonical[i] = canonical[i].Canonical()
		group := canonical[i]
		if group.ID == "" || group.Label == "" || group.Ordinal < 0 {
			return nil, fmt.Errorf("explicit group requires a stable ID, label, and non-negative ordinal")
		}
		if _, exists := ids[group.ID]; exists {
			return nil, fmt.Errorf("duplicate explicit group ID %q", group.ID)
		}
		ids[group.ID] = struct{}{}
		if _, exists := ordinals[group.Ordinal]; exists {
			return nil, fmt.Errorf("duplicate explicit group ordinal %d", group.Ordinal)
		}
		ordinals[group.Ordinal] = struct{}{}
	}
	if len(canonical) == 0 {
		return nil, fmt.Errorf("explicit group revision requires at least one declared group")
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].Ordinal != canonical[j].Ordinal {
			return canonical[i].Ordinal < canonical[j].Ordinal
		}
		return canonical[i].ID < canonical[j].ID
	})
	return canonical, nil
}

func ExplicitGroupDefinitionDigest(groups []ExplicitGroupDefinition) (string, error) {
	canonical, err := CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return "", err
	}
	payload := struct {
		Version int                       `json:"version"`
		Groups  []ExplicitGroupDefinition `json:"groups"`
	}{Version: 1, Groups: canonical}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func CanonicalExplicitGroupMemberships(revision ExplicitGroupRevision, groups []ExplicitGroupDefinition, memberships []ExplicitGroupMembership) ([]ExplicitGroupMembership, error) {
	revision = revision.Canonical()
	canonicalGroups, err := CanonicalExplicitGroupDefinitions(groups)
	if err != nil {
		return nil, err
	}
	groupIDs := make(map[ExplicitGroupID]struct{}, len(canonicalGroups))
	for _, group := range canonicalGroups {
		groupIDs[group.ID] = struct{}{}
	}
	canonical := make([]ExplicitGroupMembership, 0, len(memberships))
	seen := make(map[string]struct{}, len(memberships))
	for _, raw := range memberships {
		membership := raw.Canonical()
		if _, exists := groupIDs[membership.GroupID]; !exists {
			return nil, fmt.Errorf("membership references undeclared explicit group %q", membership.GroupID)
		}
		if err := membership.Ref.Validate(revision.Project, revision.Generation, revision.ResourceType); err != nil {
			return nil, err
		}
		identity := explicitGroupMembershipKey(membership)
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		canonical = append(canonical, membership)
	}
	sort.Slice(canonical, func(i, j int) bool {
		if canonical[i].GroupID != canonical[j].GroupID {
			return canonical[i].GroupID < canonical[j].GroupID
		}
		return resourceRefLess(canonical[i].Ref, canonical[j].Ref)
	})
	return canonical, nil
}

func ExplicitGroupMembershipDigest(revision ExplicitGroupRevision, groups []ExplicitGroupDefinition, memberships []ExplicitGroupMembership) (string, error) {
	canonical, err := CanonicalExplicitGroupMemberships(revision, groups, memberships)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	StartExplicitGroupMembershipDigest(hash)
	for _, membership := range canonical {
		WriteExplicitGroupMembershipFrame(hash, membership)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func StartExplicitGroupMembershipDigest(w interface{ Write([]byte) (int, error) }) {
	writeDigestField(w, "loom-explicit-group-membership-v1")
}

// WriteExplicitGroupMembershipFrame writes the stable relation identity used
// by both in-memory canonicalization and paged persistence finalization.
func WriteExplicitGroupMembershipFrame(w interface{ Write([]byte) (int, error) }, membership ExplicitGroupMembership) {
	membership = membership.Canonical()
	writeDigestField(w, string(membership.GroupID))
	ref := membership.Ref
	for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
		writeDigestField(w, value)
	}
}

func writeDigestField(w interface{ Write([]byte) (int, error) }, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = w.Write(length[:])
	_, _ = w.Write([]byte(value))
}

func explicitGroupMembershipKey(membership ExplicitGroupMembership) string {
	membership = membership.Canonical()
	var identity strings.Builder
	WriteExplicitGroupMembershipFrame(&identity, membership)
	return identity.String()
}

func resourceRefLess(left, right ResourceRef) bool {
	left, right = left.Canonical(), right.Canonical()
	if left.Project != right.Project {
		return left.Project < right.Project
	}
	if left.Generation != right.Generation {
		return left.Generation < right.Generation
	}
	if left.ResourceType != right.ResourceType {
		return left.ResourceType < right.ResourceType
	}
	return left.ID < right.ID
}
