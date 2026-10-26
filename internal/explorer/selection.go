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

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/published"
	"github.com/calypr/loom/internal/projectid"
)

var (
	ErrSelectionNotFound        = errors.New("explorer selection not found")
	ErrSelectionIncomplete      = errors.New("explorer selection is incomplete")
	ErrSelectionConflict        = errors.New("explorer selection conflict")
	ErrSelectionLimit           = errors.New("explorer selection limit exceeded")
	ErrSelectionNotAddressable  = publication.ErrSelectionSourceNotAddressable
	ErrSelectionMalformed       = errors.New("explorer selection request is malformed")
	ErrSelectionStaleScope      = errors.New("explorer selection scope or source is stale")
	ErrResourceRefScopeMismatch = errors.New("resource reference scope mismatch")
)

// ResourceRef is the only identity accepted by a selection. A display label,
// table row number, or serialized query value is deliberately not a resource
// reference.
type ResourceRef struct {
	Project      string `json:"project"`
	Generation   string `json:"generation"`
	ResourceType string `json:"resourceType"`
	ID           string `json:"id"`
}

func (r ResourceRef) Canonical() ResourceRef {
	r.Project = projectid.Canonical(r.Project)
	r.Generation = strings.TrimSpace(r.Generation)
	r.ResourceType = strings.TrimSpace(r.ResourceType)
	r.ID = strings.TrimSpace(r.ID)
	return r
}

func (r ResourceRef) Validate(project, generation, resourceType string) error {
	r = r.Canonical()
	if r.Project == "" || r.Generation == "" || r.ResourceType == "" || r.ID == "" {
		return fmt.Errorf("resource reference requires project, generation, resource type, and id")
	}
	if project != "" && r.Project != projectid.Canonical(project) {
		return fmt.Errorf("%w: resource reference project does not match selection project", ErrResourceRefScopeMismatch)
	}
	if generation != "" && r.Generation != strings.TrimSpace(generation) {
		return fmt.Errorf("%w: resource reference generation does not match selection generation", ErrResourceRefScopeMismatch)
	}
	if resourceType != "" && r.ResourceType != strings.TrimSpace(resourceType) {
		return fmt.Errorf("resource reference type does not match selection resource type")
	}
	return nil
}

func (r ResourceRef) key() string {
	return r.Project + "\x00" + r.Generation + "\x00" + r.ResourceType + "\x00" + r.ID
}

// SelectionFilter is the existing published-reader filter contract. Keeping
// one predicate vocabulary prevents selection from persisting AQL/GraphQL
// fragments or inventing a second operator representation.
type SelectionFilter = published.Filter

// SelectionRule describes intent, not the result membership. Reapplying a
// rule always creates a new immutable revision.
type SelectionRule struct {
	Kind    string            `json:"kind"`
	Filters []SelectionFilter `json:"filters,omitempty"`
}

const (
	SelectionRuleExplicit    = "EXPLICIT"
	SelectionRuleAllMatching = "ALL_MATCHING"
	SelectionSourceExplicit  = "EXPLICIT_REFS"
	SelectionSourcePublished = "PUBLISHED_OUTPUT"
	SelectionSourceRevision  = "SELECTION_REVISION"
)

func (r SelectionRule) Canonical() SelectionRule {
	r.Kind = strings.ToUpper(strings.TrimSpace(r.Kind))
	r.Filters = append([]SelectionFilter(nil), r.Filters...)
	sort.SliceStable(r.Filters, func(i, j int) bool {
		leftColumn, rightColumn := strings.TrimSpace(r.Filters[i].Column), strings.TrimSpace(r.Filters[j].Column)
		if leftColumn != rightColumn {
			return leftColumn < rightColumn
		}
		leftOp, rightOp := strings.ToUpper(strings.TrimSpace(r.Filters[i].Op)), strings.ToUpper(strings.TrimSpace(r.Filters[j].Op))
		if leftOp != rightOp {
			return leftOp < rightOp
		}
		return fmt.Sprint(r.Filters[i].Value) < fmt.Sprint(r.Filters[j].Value)
	})
	return r
}

func (r SelectionRule) Validate() error {
	r = r.Canonical()
	if r.Kind != SelectionRuleExplicit && r.Kind != SelectionRuleAllMatching {
		return fmt.Errorf("unsupported selection rule %q", r.Kind)
	}
	if r.Kind == SelectionRuleExplicit && len(r.Filters) != 0 {
		return fmt.Errorf("explicit selections cannot contain filters")
	}
	return published.ValidateTypedFilters(r.Filters)
}

type SelectionSource struct {
	Kind             string `json:"kind"`
	Generation       string `json:"generation,omitempty"`
	RevisionID       string `json:"revisionId,omitempty"`
	ReceiptID        string `json:"receiptId,omitempty"`
	ExecutionID      string `json:"executionId,omitempty"`
	OutputID         string `json:"outputId,omitempty"`
	SchemaDigest     string `json:"schemaDigest,omitempty"`
	ResourceType     string `json:"resourceType,omitempty"`
	SourceIDColumn   string `json:"sourceIdColumn,omitempty"`
	MembershipDigest string `json:"membershipDigest,omitempty"`
}

func (s SelectionSource) Canonical() SelectionSource {
	s.Kind = strings.ToUpper(strings.TrimSpace(s.Kind))
	s.Generation = strings.TrimSpace(s.Generation)
	s.RevisionID = strings.TrimSpace(s.RevisionID)
	s.ReceiptID = strings.TrimSpace(s.ReceiptID)
	s.ExecutionID = strings.TrimSpace(s.ExecutionID)
	s.OutputID = strings.TrimSpace(s.OutputID)
	s.SchemaDigest = strings.TrimSpace(s.SchemaDigest)
	s.ResourceType = strings.TrimSpace(s.ResourceType)
	s.SourceIDColumn = strings.TrimSpace(s.SourceIDColumn)
	s.MembershipDigest = strings.TrimSpace(s.MembershipDigest)
	return s
}

func (s SelectionSource) Validate(project, generation, resourceType string) error {
	s = s.Canonical()
	switch s.Kind {
	case SelectionSourceExplicit:
		if s.RevisionID != "" || s.ReceiptID != "" || s.ExecutionID != "" || s.OutputID != "" || s.SchemaDigest != "" || s.Generation != "" || s.ResourceType != "" || s.SourceIDColumn != "" || s.MembershipDigest != "" {
			return fmt.Errorf("explicit selection source cannot carry source identity")
		}
	case SelectionSourcePublished:
		if s.ExecutionID == "" || s.OutputID == "" || s.RevisionID == "" || s.ReceiptID == "" || s.SchemaDigest == "" {
			return fmt.Errorf("published selection source requires revision, receipt, execution, output, and schema identity")
		}
		if s.Generation != strings.TrimSpace(generation) {
			return fmt.Errorf("published selection source generation does not match selection generation")
		}
		if s.ResourceType == "" || s.SourceIDColumn == "" {
			return ErrSelectionNotAddressable
		}
		if resourceType != "" && s.ResourceType != strings.TrimSpace(resourceType) {
			return fmt.Errorf("published selection source type does not match selection resource type")
		}
		if s.MembershipDigest != "" {
			return fmt.Errorf("published selection source cannot carry membership identity")
		}
	case SelectionSourceRevision:
		if s.RevisionID == "" || s.Generation == "" || s.ResourceType == "" || s.MembershipDigest == "" {
			return fmt.Errorf("selection revision source requires revision, generation, resource type, and membership identity")
		}
		if s.Generation != strings.TrimSpace(generation) || s.ResourceType != strings.TrimSpace(resourceType) {
			return fmt.Errorf("selection revision source does not match selection scope")
		}
		if s.ReceiptID != "" || s.ExecutionID != "" || s.OutputID != "" || s.SchemaDigest != "" || s.SourceIDColumn != "" {
			return fmt.Errorf("selection revision source cannot carry published output identity")
		}
	default:
		return fmt.Errorf("unsupported selection source %q", s.Kind)
	}
	return nil
}

type SelectionMember struct {
	Ref       ResourceRef `json:"ref"`
	Ordinal   int64       `json:"ordinal,omitempty"`
	MemberKey string      `json:"memberKey,omitempty"`
}

func (m SelectionMember) Canonical() SelectionMember {
	m.Ref = m.Ref.Canonical()
	m.MemberKey = strings.TrimSpace(m.MemberKey)
	return m
}

func (m SelectionMember) Validate(project, generation, resourceType string) error {
	return m.Ref.Validate(project, generation, resourceType)
}

type SelectionRevision struct {
	ID               string          `json:"id"`
	Project          string          `json:"project"`
	Generation       string          `json:"generation"`
	ResourceType     string          `json:"resourceType"`
	Rule             SelectionRule   `json:"rule"`
	Source           SelectionSource `json:"source"`
	Exclusions       []ResourceRef   `json:"exclusions,omitempty"`
	ScopeDigest      string          `json:"scopeDigest"`
	RuleDigest       string          `json:"ruleDigest"`
	MembershipDigest string          `json:"membershipDigest"`
	MemberCount      int64           `json:"memberCount"`
	MemberBytes      int64           `json:"memberBytes"`
	Complete         bool            `json:"complete"`
	IdempotencyKey   string          `json:"idempotencyKey,omitempty"`
	CreatedAt        time.Time       `json:"createdAt"`
	CompletedAt      *time.Time      `json:"completedAt,omitempty"`
}

func (s SelectionRevision) Canonical() SelectionRevision {
	s.Project = strings.TrimSpace(s.Project)
	s.Generation = strings.TrimSpace(s.Generation)
	s.ResourceType = strings.TrimSpace(s.ResourceType)
	s.ScopeDigest = strings.TrimSpace(s.ScopeDigest)
	s.RuleDigest = strings.TrimSpace(s.RuleDigest)
	s.MembershipDigest = strings.TrimSpace(s.MembershipDigest)
	s.IdempotencyKey = strings.TrimSpace(s.IdempotencyKey)
	s.Rule = s.Rule.Canonical()
	s.Source = s.Source.Canonical()
	s.Exclusions = append([]ResourceRef(nil), s.Exclusions...)
	for i := range s.Exclusions {
		s.Exclusions[i] = s.Exclusions[i].Canonical()
	}
	sort.Slice(s.Exclusions, func(i, j int) bool { return s.Exclusions[i].key() < s.Exclusions[j].key() })
	return s
}

func (s SelectionRevision) Validate() error {
	s = s.Canonical()
	if strings.TrimSpace(s.ID) == "" || s.Project == "" || s.Generation == "" || s.ResourceType == "" {
		return fmt.Errorf("selection identity, project, generation, and resource type are required")
	}
	if err := s.Rule.Validate(); err != nil {
		return err
	}
	if err := s.Source.Validate(s.Project, s.Generation, s.ResourceType); err != nil {
		return err
	}
	if s.ScopeDigest == "" || s.RuleDigest == "" {
		return fmt.Errorf("selection scope and rule digests are required")
	}
	for _, exclusion := range s.Exclusions {
		if err := exclusion.Validate(s.Project, s.Generation, s.ResourceType); err != nil {
			return err
		}
	}
	if s.Complete && (s.MembershipDigest == "" || s.MemberCount < 0 || s.MemberBytes < 0 || s.CompletedAt == nil) {
		return ErrSelectionIncomplete
	}
	return nil
}

func SelectionRuleDigest(rule SelectionRule, source SelectionSource, exclusions []ResourceRef) (string, error) {
	return SelectionRuleDigestWithMembers(rule, source, exclusions, nil)
}

// SelectionRuleDigestWithMembers binds explicit request membership into the
// immutable intent digest. It is intentionally separate from the final
// MembershipDigest: exclusions can make two requests produce the same final
// rows while their requested inputs remain different and must conflict under
// one idempotency key.
func SelectionRuleDigestWithMembers(rule SelectionRule, source SelectionSource, exclusions []ResourceRef, members []ResourceRef) (string, error) {
	rule = rule.Canonical()
	source = source.Canonical()
	canonicalExclusions := append([]ResourceRef(nil), exclusions...)
	for i := range canonicalExclusions {
		canonicalExclusions[i] = canonicalExclusions[i].Canonical()
	}
	sort.Slice(canonicalExclusions, func(i, j int) bool { return canonicalExclusions[i].key() < canonicalExclusions[j].key() })
	canonicalMembers := append([]ResourceRef(nil), members...)
	for i := range canonicalMembers {
		canonicalMembers[i] = canonicalMembers[i].Canonical()
	}
	requestMembershipDigest := MembershipDigest(resourceRefsToMembers(canonicalMembers))
	b, err := json.Marshal(struct {
		Rule                    SelectionRule   `json:"rule"`
		Source                  SelectionSource `json:"source"`
		Exclusions              []ResourceRef   `json:"exclusions,omitempty"`
		RequestMembershipDigest string          `json:"requestMembershipDigest,omitempty"`
	}{rule, source, canonicalExclusions, requestMembershipDigest})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func resourceRefsToMembers(refs []ResourceRef) []SelectionMember {
	members := make([]SelectionMember, len(refs))
	for i, ref := range refs {
		members[i] = SelectionMember{Ref: ref}
	}
	return members
}

// MembershipDigest computes an immutable digest over canonical,
// deduplicated typed references. Storage adapters use the same framing while
// streaming indexed member records.
func MembershipDigest(members []SelectionMember) string {
	refs := make([]ResourceRef, 0, len(members))
	seen := make(map[string]struct{}, len(members))
	for _, member := range members {
		ref := member.Ref.Canonical()
		if _, ok := seen[ref.key()]; ok {
			continue
		}
		seen[ref.key()] = struct{}{}
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].key() < refs[j].key() })
	hash := sha256.New()
	for _, ref := range refs {
		WriteMembershipFrame(hash, ref)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// WriteMembershipFrame writes an unambiguous length-prefixed identity frame.
// It is shared by lifecycle and persistence finalizers.
func WriteMembershipFrame(w interface{ Write([]byte) (int, error) }, ref ResourceRef) {
	ref = ref.Canonical()
	for _, value := range []string{ref.Project, ref.Generation, ref.ResourceType, ref.ID} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		_, _ = w.Write(length[:])
		_, _ = w.Write([]byte(value))
	}
}
