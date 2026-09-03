package server

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/calypr/loom/internal/projectid"
)

func localWorkspaceWriter(path, configuredProject string) (func(context.Context, string, string, []byte) error, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	configuredProject = projectid.Canonical(configuredProject)
	if configuredProject == "" {
		return nil, fmt.Errorf("local workspace writeback project is required")
	}
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Ext(path), ".json") {
		return nil, fmt.Errorf("local workspace writeback must be an absolute .json file")
	}
	directory := filepath.Dir(path)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("local workspace writeback directory %q is unavailable", directory)
	}
	return func(ctx context.Context, project, _ string, workspace []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if projectid.Canonical(project) != configuredProject {
			return fmt.Errorf("project %q cannot update the local workspace for %q", project, configuredProject)
		}
		if len(workspace) == 0 {
			return fmt.Errorf("published workspace is empty")
		}
		temporary, err := os.CreateTemp(directory, ".loom-workspace-*.json")
		if err != nil {
			return fmt.Errorf("create temporary workspace: %w", err)
		}
		temporaryPath := temporary.Name()
		cleanup := func() { _ = os.Remove(temporaryPath) }
		defer cleanup()
		if err := temporary.Chmod(0o644); err != nil {
			_ = temporary.Close()
			return err
		}
		if _, err := temporary.Write(workspace); err != nil {
			_ = temporary.Close()
			return fmt.Errorf("write temporary workspace: %w", err)
		}
		if len(workspace) == 0 || workspace[len(workspace)-1] != '\n' {
			if _, err := temporary.Write([]byte{'\n'}); err != nil {
				_ = temporary.Close()
				return err
			}
		}
		if err := temporary.Sync(); err != nil {
			_ = temporary.Close()
			return fmt.Errorf("sync temporary workspace: %w", err)
		}
		if err := temporary.Close(); err != nil {
			return err
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return fmt.Errorf("replace local workspace %q: %w", path, err)
		}
		return nil
	}, nil
}
