package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"sort"
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

// SourceFile is one canonical, repository-owned META resource file. The file
// part in the HTTP request carries the bytes; Path and SHA256 bind those bytes
// to the immutable source manifest.
type SourceFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// SourceManifest is the caller's immutable Git source identity plus the
// selected files. Commit is provenance; SourceRevision is the numeric Loom CAS.
type SourceManifest struct {
	Repository string       `json:"repository"`
	Commit     string       `json:"commit"`
	Files      []SourceFile `json:"files"`
}

type SourcePublishConflictError struct{ Reason string }

func (e *SourcePublishConflictError) Error() string {
	if e == nil || e.Reason == "" {
		return "source publication conflicts with current project state"
	}
	return "source publication conflict: " + e.Reason
}

type SourcePublishMigrationRequiredError struct{ Reason string }

func (e *SourcePublishMigrationRequiredError) Error() string {
	if e == nil || e.Reason == "" {
		return "source ownership migration is required"
	}
	return "source ownership migration is required: " + e.Reason
}

type SourcePublishLimitError struct {
	Limit   string
	Maximum int64
}

func (e *SourcePublishLimitError) Error() string {
	if e == nil || e.Limit == "" {
		return "source publication exceeds configured limits"
	}
	return "source publication exceeds configured " + e.Limit + " limit"
}

// SourcePublishReceipt is returned by the file-authoritative publish API.
type SourcePublishReceipt struct {
	OperationID    string `json:"operation_id"`
	SourceRevision int64  `json:"source_revision"`
	ManifestHash   string `json:"manifest_hash"`
	ExecutionID    string `json:"execution_id"`
	State          string `json:"state"`
	Reused         bool   `json:"reused"`
}

const maxSourceManifestBytes = 1 << 20

var maxSourceFileCount = len(fhirschema.ResourceTypes())

func ParseSourceManifest(data []byte) (SourceManifest, string, error) {
	if len(data) == 0 || len(data) > maxSourceManifestBytes {
		return SourceManifest{}, "", fmt.Errorf("source manifest size must be between 1 and %d bytes", maxSourceManifestBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var manifest SourceManifest
	if err := decoder.Decode(&manifest); err != nil {
		return SourceManifest{}, "", fmt.Errorf("decode source manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return SourceManifest{}, "", fmt.Errorf("source manifest must contain one JSON value")
		}
		return SourceManifest{}, "", fmt.Errorf("decode source manifest trailer: %w", err)
	}
	manifest.Repository = strings.TrimSpace(manifest.Repository)
	manifest.Commit = strings.TrimSpace(manifest.Commit)
	if manifest.Repository == "" || manifest.Commit == "" || len(manifest.Files) == 0 || len(manifest.Files) > maxSourceFileCount {
		return SourceManifest{}, "", fmt.Errorf("source manifest requires repository, commit, and 1 to %d files", maxSourceFileCount)
	}
	if err := validateSourceRepository(manifest.Repository); err != nil {
		return SourceManifest{}, "", err
	}
	if (len(manifest.Commit) != 40 && len(manifest.Commit) != 64) || strings.ToLower(manifest.Commit) != manifest.Commit {
		return SourceManifest{}, "", fmt.Errorf("source commit must be a full lowercase 40- or 64-character hexadecimal object ID")
	}
	if _, err := hex.DecodeString(manifest.Commit); err != nil {
		return SourceManifest{}, "", fmt.Errorf("source commit must be a full lowercase 40- or 64-character hexadecimal object ID")
	}
	seenTypes := make(map[string]struct{}, len(manifest.Files))
	seenPaths := make(map[string]struct{}, len(manifest.Files))
	for index := range manifest.Files {
		file := &manifest.Files[index]
		resourceType, err := canonicalSourceResourceType(file.Path)
		if err != nil {
			return SourceManifest{}, "", fmt.Errorf("source manifest file %d: %w", index, err)
		}
		file.Path = "META/" + resourceType + ".ndjson"
		file.SHA256 = strings.ToLower(strings.TrimSpace(file.SHA256))
		if file.SHA256 != "" {
			if len(file.SHA256) != 64 {
				return SourceManifest{}, "", fmt.Errorf("source manifest file %q has an invalid SHA-256 digest", file.Path)
			}
			if _, err := hex.DecodeString(file.SHA256); err != nil {
				return SourceManifest{}, "", fmt.Errorf("source manifest file %q has an invalid SHA-256 digest", file.Path)
			}
		}
		if _, exists := seenTypes[resourceType]; exists {
			return SourceManifest{}, "", fmt.Errorf("source manifest contains more than one owner for resource type %q", resourceType)
		}
		seenTypes[resourceType] = struct{}{}
		if _, exists := seenPaths[file.Path]; exists {
			return SourceManifest{}, "", fmt.Errorf("source manifest contains duplicate file %q", file.Path)
		}
		seenPaths[file.Path] = struct{}{}
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	manifestHash, err := SourceManifestHash(manifest)
	if err != nil {
		return SourceManifest{}, "", err
	}
	return manifest, manifestHash, nil
}

func SourceManifestHash(manifest SourceManifest) (string, error) {
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("encode canonical source manifest: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return hex.EncodeToString(digest[:]), nil
}

func validateSourceRepository(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("source repository must be a canonical https://github.com/OWNER/REPOSITORY URL")
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" || strings.HasSuffix(parts[1], ".git") || path.Clean(parsed.Path) != parsed.Path {
		return fmt.Errorf("source repository must be a canonical https://github.com/OWNER/REPOSITORY URL")
	}
	return nil
}

func canonicalSourceResourceType(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.Contains(value, "\\") || path.Clean(value) != value {
		return "", fmt.Errorf("file path %q is not canonical", value)
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] != "META" || !strings.HasSuffix(parts[1], ".ndjson") {
		return "", fmt.Errorf("file path %q must be META/<FHIRResourceType>.ndjson", value)
	}
	resourceType := strings.TrimSuffix(parts[1], ".ndjson")
	canonical, ok := fhirschema.ConcreteResourceType(resourceType)
	if !ok || canonical != resourceType {
		return "", fmt.Errorf("file path %q has an unsupported FHIR resource type", value)
	}
	return resourceType, nil
}
