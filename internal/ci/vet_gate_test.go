package ci

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const vetProbeSourceTemplate = `package authoringv2

import "sync"

var r67VetProbeLock_R67Nonce sync.Mutex

func r67VetProbe_R67Nonce() {
	copied := r67VetProbeLock_R67Nonce
	_ = &copied
}
`

func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller must name this test file to locate the repo root")
	}
	dir := filepath.Dir(thisFile)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found walking up from %s", filepath.Dir(thisFile))
		}
		dir = parent
	}
}

func runGoVet(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"vet"}, args...)...)
	cmd.Dir = repoRoot(t)
	out, runErr := cmd.CombinedOutput()
	return string(out), runErr
}

func TestVetGateRepoWideDefaultSet(t *testing.T) {
	out, runErr := runGoVet(t, "./...")
	if runErr != nil {
		t.Fatalf("go vet ./... (default analyzer set, which demonstrably includes copylocks) must stay green so the shipped-green class is gated repo-wide:\n%v\n%s", runErr, out)
	}
}

func TestVetGateFiresOnInjectedCopylocksFault(t *testing.T) {
	root := repoRoot(t)
	virtual := filepath.Join(root, "internal", "explorer", "authoringv2", "r67_vet_probe.go")
	assertAbsentFromDisk := func(phase string) {
		t.Helper()
		if _, err := os.Stat(virtual); !errors.Is(err, syscall.ENOENT) {
			t.Fatalf("%s: the probe must live only in the -overlay map, so %s must be ENOENT on disk; os.Stat err=%v", phase, virtual, err)
		}
	}
	assertAbsentFromDisk("before")

	temp := t.TempDir()
	nonce := strconv.FormatInt(time.Now().UnixNano(), 16)
	probe := filepath.Join(temp, "p.go")
	probeSource := strings.ReplaceAll(vetProbeSourceTemplate, "R67Nonce", nonce)
	if err := os.WriteFile(probe, []byte(probeSource), 0o644); err != nil {
		t.Fatalf("writing probe file %s failed: %v", probe, err)
	}
	overlay := filepath.Join(temp, "overlay.json")
	overlayBytes, err := json.Marshal(map[string]map[string]string{"Replace": {virtual: probe}})
	if err != nil {
		t.Fatalf("marshalling overlay.json failed: %v", err)
	}
	if err := os.WriteFile(overlay, overlayBytes, 0o644); err != nil {
		t.Fatalf("writing overlay %s failed: %v", overlay, err)
	}

	const pkg = "./internal/explorer/authoringv2"
	out, runErr := runGoVet(t, "-overlay="+overlay, pkg)
	if runErr == nil {
		t.Fatalf("the injected copylocks fault must make the default-set go vet fail (that is the shipped-green class this gate exists for):\n%s", out)
	}
	for _, want := range []string{"copies lock value", temp} {
		if !strings.Contains(out, want) {
			t.Fatalf("default-set go vet output must contain %q for the injected probe; got:\n%s", want, out)
		}
	}

	out, runErr = runGoVet(t, "-copylocks=false", "-overlay="+overlay, pkg)
	if runErr != nil {
		t.Fatalf("with -copylocks=false the same overlay must vet clean; anything failing there is not the copylocks class:\n%v\n%s", runErr, out)
	}
	assertAbsentFromDisk("after")
}

func TestVetGateCopylocksExplicitPassCleanFocused(t *testing.T) {
	out, runErr := runGoVet(t, "-copylocks", "./internal/explorer/authoringv2")
	if runErr != nil {
		t.Fatalf("the explicit copylocks pass must stay clean on the focused package:\n%v\n%s", runErr, out)
	}
}

func TestNodeVetCliSmoke(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatalf("environment blocker: node is not on PATH, so the scripts/ci/vet.mjs CLI cannot be exercised (not a code fault): %v", err)
	}
	cmd := exec.Command("node", "scripts/ci/vet.mjs", "--packages=./internal/ci/...")
	cmd.Dir = repoRoot(t)
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		t.Fatalf("node scripts/ci/vet.mjs --packages=./internal/ci/... must exit 0:\n%v\n%s", runErr, out)
	}
	if !strings.Contains(string(out), "VET-GATE PASS") {
		t.Fatalf("vet CLI must print VET-GATE PASS on success; got:\n%s", out)
	}
}
