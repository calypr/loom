// Package repositoryseed validates a repository checkout before the local
// launcher is allowed to mutate Docker or database state.
package repositoryseed

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
)

type Options struct {
	Repository string
	Config     string
	Project    string
}

type File struct {
	Path         string `json:"path"`
	ResourceType string `json:"resourceType,omitempty"`
	Resources    int    `json:"resources"`
	Bytes        int64  `json:"bytes"`
	SHA256       string `json:"sha256"`
}

type Manifest struct {
	Repository   string `json:"repository"`
	MetaDir      string `json:"metaDir"`
	ConfigPath   string `json:"configPath"`
	ConfigDir    string `json:"configDir"`
	ConfigName   string `json:"configName"`
	Project      string `json:"project"`
	Generation   string `json:"generation"`
	MetaDigest   string `json:"metaDigest"`
	ConfigDigest string `json:"configDigest"`
	SourceCommit string `json:"sourceCommit"`
	Files        []File `json:"files"`
	Workspace    []byte `json:"-"`
}

type UnmaterializedError struct{ Paths []string }

func (e *UnmaterializedError) Error() string {
	var b strings.Builder
	b.WriteString("repository data is not materialized; Loom did not start Docker\npointer files:\n")
	for _, path := range e.Paths {
		fmt.Fprintf(&b, "  %s\n", path)
	}
	b.WriteString("materialize these files using this repository's normal Git LFS/DRS workflow, then rerun Loom; the Loom launcher does not download repository objects")
	return b.String()
}

func Prepare(options Options) (Manifest, error) {
	root, err := filepath.Abs(first(options.Repository, "."))
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve repository: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return Manifest{}, fmt.Errorf("repository %q is not a directory", root)
	}
	metaDir := filepath.Join(root, "META")
	metaPaths, err := filepath.Glob(filepath.Join(metaDir, "*.ndjson"))
	if err != nil {
		return Manifest{}, fmt.Errorf("discover META: %w", err)
	}
	sort.Strings(metaPaths)
	if len(metaPaths) == 0 {
		return Manifest{}, fmt.Errorf("repository %q contains no META/*.ndjson files", root)
	}
	configPath, err := resolveConfig(root, options.Config)
	if err != nil {
		return Manifest{}, err
	}
	selected := append(append([]string(nil), metaPaths...), configPath)
	pointers := make([]string, 0)
	for _, path := range selected {
		if err := requireContainedRegularFile(root, path); err != nil {
			return Manifest{}, err
		}
		pointer, readErr := isPointer(path)
		if readErr != nil {
			return Manifest{}, readErr
		}
		if pointer {
			relative, _ := filepath.Rel(root, path)
			pointers = append(pointers, filepath.ToSlash(relative))
		}
	}
	if len(pointers) != 0 {
		return Manifest{}, &UnmaterializedError{Paths: pointers}
	}

	metaHash := sha256.New()
	files := make([]File, 0, len(metaPaths))
	totalResources := 0
	for _, path := range metaPaths {
		file, validateErr := validateNDJSON(root, path)
		if validateErr != nil {
			return Manifest{}, validateErr
		}
		files = append(files, file)
		totalResources += file.Resources
		_, _ = io.WriteString(metaHash, file.Path+"\x00"+file.SHA256+"\x00")
	}
	if totalResources == 0 {
		return Manifest{}, errors.New("META contains no FHIR resources")
	}
	workspace, err := os.ReadFile(configPath)
	if err != nil {
		return Manifest{}, fmt.Errorf("read CONFIG: %w", err)
	}
	decodedWorkspace, err := authoringv2.DecodeWorkspace(workspace)
	if err != nil {
		return Manifest{}, fmt.Errorf("CONFIG %s is not a valid native Loom V2 workspace: %w", filepath.Base(configPath), err)
	}
	if err := decodedWorkspace.ValidateForPublication(); err != nil {
		return Manifest{}, fmt.Errorf("CONFIG %s cannot be published by Loom: %w", filepath.Base(configPath), err)
	}
	canonicalWorkspace, err := decodedWorkspace.CanonicalJSON()
	if err != nil {
		return Manifest{}, fmt.Errorf("canonicalize CONFIG %s: %w", filepath.Base(configPath), err)
	}
	configDigest, err := decodedWorkspace.Digest()
	if err != nil {
		return Manifest{}, fmt.Errorf("digest CONFIG %s: %w", filepath.Base(configPath), err)
	}
	metaDigest := hex.EncodeToString(metaHash.Sum(nil))
	project := projectid.Canonical(options.Project)
	if project == "" {
		project = projectFromFilename(configPath)
	}
	if project == "" {
		return Manifest{}, errors.New("project could not be inferred from CONFIG filename; pass --project PROGRAM/PROJECT")
	}
	return Manifest{
		Repository: root, MetaDir: metaDir, ConfigPath: configPath,
		ConfigDir: filepath.Dir(configPath), ConfigName: filepath.Base(configPath),
		Project: project, Generation: "repo-" + metaDigest[:24], MetaDigest: metaDigest,
		ConfigDigest: configDigest, SourceCommit: "workspace-" + strings.TrimPrefix(configDigest, "sha256:")[:24],
		Files: files, Workspace: canonicalWorkspace,
	}, nil
}

func requireContainedRegularFile(root, path string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve repository %q: %w", root, err)
	}
	resolvedPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve repository input %q: %w", path, err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("repository input %q resolves outside the repository", path)
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("repository input %q is not a regular file", path)
	}
	return nil
}

func resolveConfig(root, configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		path := configured
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		configRoot := filepath.Join(root, "CONFIG") + string(os.PathSeparator)
		if !strings.HasPrefix(path+string(os.PathSeparator), configRoot) {
			return "", errors.New("--config must resolve inside the repository CONFIG directory")
		}
		if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
			return "", fmt.Errorf("CONFIG file %q is not a regular file", path)
		}
		return path, nil
	}
	paths, err := filepath.Glob(filepath.Join(root, "CONFIG", "*.json"))
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return "", errors.New("repository contains no CONFIG/*.json file")
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("repository contains %d CONFIG/*.json files; select one with --config", len(paths))
	}
	return paths[0], nil
}

func isPointer(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	defer file.Close()
	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	value := string(buffer[:n])
	return strings.HasPrefix(value, "version https://git-lfs.github.com/spec/v1\n") && strings.Contains(value, "\noid sha256:"), nil
}

func validateNDJSON(root, path string) (File, error) {
	input, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer input.Close()
	hash := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(input, hash))
	scanner.Buffer(make([]byte, 64*1024), 64<<20)
	resourceType := ""
	rows := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var resource struct {
			ResourceType string `json:"resourceType"`
			ID           string `json:"id"`
		}
		if err := json.Unmarshal(line, &resource); err != nil {
			return File{}, fmt.Errorf("invalid NDJSON %s line %d: %w", filepath.Base(path), rows+1, err)
		}
		if strings.TrimSpace(resource.ResourceType) == "" || strings.TrimSpace(resource.ID) == "" {
			return File{}, fmt.Errorf("invalid FHIR resource in %s line %d: resourceType and id are required", filepath.Base(path), rows+1)
		}
		if resourceType == "" {
			resourceType = resource.ResourceType
		} else if resource.ResourceType != resourceType {
			return File{}, fmt.Errorf("META file %s mixes resource types %s and %s", filepath.Base(path), resourceType, resource.ResourceType)
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		return File{}, fmt.Errorf("read NDJSON %s: %w", filepath.Base(path), err)
	}
	if resourceType == "" {
		resourceType = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	info, err := os.Stat(path)
	if err != nil {
		return File{}, err
	}
	relative, _ := filepath.Rel(root, path)
	return File{Path: filepath.ToSlash(relative), ResourceType: resourceType, Resources: rows, Bytes: info.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func projectFromFilename(path string) string {
	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name = strings.TrimSuffix(name, ".workspace")
	return projectid.Canonical(name)
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
