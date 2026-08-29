package dataset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

var (
	ErrSnapshotNotFound     = errors.New("snapshot generation was not found")
	ErrSnapshotConflict     = errors.New("snapshot generation metadata conflicts with immutable content")
	ErrChecksumConflict     = errors.New("snapshot resource checksum conflict")
	ErrGenerationIncomplete = errors.New("snapshot generation is incomplete")
	ErrSnapshotFinalized    = errors.New("snapshot generation is already finalized")
	ErrSnapshotAborted      = errors.New("snapshot generation was aborted")
)

// ResourceUpload is immutable checksum evidence for one resource type.
type ResourceUpload struct {
	ResourceType string    `json:"resourceType"`
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
	UploadedAt   time.Time `json:"uploadedAt"`
}

// SnapshotGeneration tracks resumable uploads independently of the graph
// manifest. Finalization stages the graph manifest; it never activates reads.
type SnapshotGeneration struct {
	Dataset               Ref              `json:"dataset"`
	GitCommit             string           `json:"gitCommit"`
	State                 State            `json:"state"`
	ExpectedResourceTypes []string         `json:"expectedResourceTypes"`
	Uploads               []ResourceUpload `json:"uploads,omitempty"`
	AuthResourcePath      string           `json:"authResourcePath,omitempty"`
	CreatedAt             time.Time        `json:"createdAt"`
	UpdatedAt             time.Time        `json:"updatedAt"`
	AbortedAt             *time.Time       `json:"abortedAt,omitempty"`
}

func NewSnapshotGeneration(project, gitCommit, authResourcePath string, expected []string, now time.Time) (SnapshotGeneration, error) {
	ref, err := NewRef(strings.TrimSpace(project), strings.TrimSpace(gitCommit))
	if err != nil {
		return SnapshotGeneration{}, err
	}
	expected = normalizeResourceTypes(expected)
	if len(expected) == 0 {
		return SnapshotGeneration{}, fmt.Errorf("%w: expectedResourceTypes is required", ErrGenerationIncomplete)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return SnapshotGeneration{Dataset: ref, GitCommit: ref.Generation, State: StateLoading, ExpectedResourceTypes: expected, AuthResourcePath: strings.TrimSpace(authResourcePath), CreatedAt: now.UTC(), UpdatedAt: now.UTC()}, nil
}

func normalizeResourceTypes(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[strings.Clone(value)] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (g SnapshotGeneration) Validate() error {
	if err := g.Dataset.Validate(); err != nil {
		return err
	}
	if g.GitCommit != g.Dataset.Generation {
		return fmt.Errorf("gitCommit must equal dataset generation")
	}
	if g.State != StateLoading && g.State != StateStaged && g.State != StateFailed {
		return fmt.Errorf("invalid snapshot state %q", g.State)
	}
	if len(g.ExpectedResourceTypes) == 0 || !sort.StringsAreSorted(g.ExpectedResourceTypes) {
		return fmt.Errorf("expectedResourceTypes must be non-empty and sorted")
	}
	seen := make(map[string]struct{}, len(g.Uploads))
	for _, upload := range g.Uploads {
		if _, ok := seen[upload.ResourceType]; ok {
			return fmt.Errorf("duplicate upload for %q", upload.ResourceType)
		}
		seen[upload.ResourceType] = struct{}{}
		if !validSHA256(upload.SHA256) || upload.Size < 0 {
			return fmt.Errorf("invalid upload metadata for %q", upload.ResourceType)
		}
	}
	return nil
}

func (g SnapshotGeneration) MissingResourceTypes() []string {
	uploaded := make(map[string]struct{}, len(g.Uploads))
	for _, upload := range g.Uploads {
		uploaded[upload.ResourceType] = struct{}{}
	}
	missing := make([]string, 0)
	for _, resourceType := range g.ExpectedResourceTypes {
		if _, ok := uploaded[resourceType]; !ok {
			missing = append(missing, resourceType)
		}
	}
	return missing
}

func (g SnapshotGeneration) Upload(resourceType string) (ResourceUpload, bool) {
	for _, upload := range g.Uploads {
		if upload.ResourceType == resourceType {
			return upload, true
		}
	}
	return ResourceUpload{}, false
}

func NewResourceUpload(resourceType, checksum string, size int64, now time.Time) (ResourceUpload, error) {
	resourceType = strings.TrimSpace(resourceType)
	checksum = strings.ToLower(strings.TrimSpace(checksum))
	if resourceType == "" || strings.ContainsAny(resourceType, "/\\") {
		return ResourceUpload{}, fmt.Errorf("invalid resource type")
	}
	if !validSHA256(checksum) {
		return ResourceUpload{}, fmt.Errorf("checksum must be a lower-case SHA-256 digest")
	}
	if size < 0 {
		return ResourceUpload{}, fmt.Errorf("size must be non-negative")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return ResourceUpload{ResourceType: resourceType, SHA256: checksum, Size: size, UploadedAt: now.UTC()}, nil
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

type SnapshotRepository interface {
	CreateOrResumeSnapshot(context.Context, SnapshotGeneration) (SnapshotGeneration, error)
	ReadSnapshot(context.Context, Ref) (SnapshotGeneration, error)
	RecordSnapshotUpload(context.Context, Ref, ResourceUpload) (SnapshotGeneration, error)
	TransitionSnapshot(context.Context, Ref, State, State, time.Time) (SnapshotGeneration, error)
}
