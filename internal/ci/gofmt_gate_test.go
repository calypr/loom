package ci

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func gofmtBinary(t *testing.T) string {
	t.Helper()
	root := runtime.GOROOT()
	if root == "" {
		t.Fatal("runtime.GOROOT() empty; cannot locate gofmt for the formatting gate")
	}
	name := "gofmt"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(root, "bin", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("gofmt not found at %s: %v", path, err)
	}
	return path
}

func gofmtList(t *testing.T, gofmt string, files []string) (string, error) {
	t.Helper()
	var dirty bytes.Buffer
	const batch = 300
	for start := 0; start < len(files); start += batch {
		end := min(start+batch, len(files))
		cmd := exec.Command(gofmt, append([]string{"-l"}, files[start:end]...)...)
		cmd.Dir = repoRoot(t)
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		dirty.Write(out)
		if err != nil {
			return "", fmt.Errorf("gofmt batch %d-%d: %w", start, end, err)
		}
	}
	return string(bytes.TrimSpace(dirty.Bytes())), nil
}

func trackedGoFiles(t *testing.T) []string {
	t.Helper()
	cmd := exec.Command("git", "ls-files", "-z", "*.go")
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, name := range strings.Split(string(out), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	if len(files) < 500 {
		t.Fatalf("tracked .go file count %d is implausibly low; wrong worktree root?", len(files))
	}
	return files
}

func TestGofmtGateTrackedFilesAreCanonical(t *testing.T) {
	gofmt := gofmtBinary(t)

	scratch := t.TempDir()
	dirtyPath := filepath.Join(scratch, "dirty.go")
	if err := os.WriteFile(dirtyPath, []byte("package scratch\nfunc  f( ) int {\nreturn 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err := gofmtList(t, gofmt, []string{dirtyPath})
	if err != nil {
		t.Fatalf("gofmt on known-dirty input: %v", err)
	}
	if !strings.Contains(dirty, "dirty.go") {
		t.Fatalf("sensitivity arm: gofmt did not flag the known-dirty file (output %q); the gate would be vacuous", dirty)
	}

	files := trackedGoFiles(t)
	report, err := gofmtList(t, gofmt, files)
	if err != nil {
		t.Fatalf("gofmt repository run: %v", err)
	}
	if report != "" {
		t.Fatalf("gofmt drift in tracked files:\n%s", report)
	}

	cleanPath := filepath.Join(scratch, "clean.go")
	if err := os.WriteFile(cleanPath, []byte("package scratch\n\nfunc f() int {\n\treturn 1\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	clean, err := gofmtList(t, gofmt, []string{cleanPath})
	if err != nil {
		t.Fatalf("gofmt on clean input: %v", err)
	}
	if clean != "" {
		t.Fatalf("gofmt flagged canonical input: %q", clean)
	}

	brokenPath := filepath.Join(scratch, "broken.go")
	if err := os.WriteFile(brokenPath, []byte("package scratch\nfunc f( {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gofmtList(t, gofmt, []string{dirtyPath, brokenPath}); err == nil {
		t.Fatal("gofmt exit-2 with a non-empty list was silently passed; unparseable tracked files would escape the gate")
	}
	t.Logf("GOFMT_GATE tracked=%d dirty=0 sensitivity=proven exit2=fatal", len(files))
}
