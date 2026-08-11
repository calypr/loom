package recipe

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExplorerBuilderContractFixtureManifest(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	dir := filepath.Join(root, "docs", "contracts", "explorer-builder", "v1")
	manifest, err := os.Open(filepath.Join(dir, "MANIFEST.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	scanner := bufio.NewScanner(manifest)
	checked := 0
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) != 2 {
			t.Fatalf("invalid manifest line %q", scanner.Text())
		}
		data, err := os.ReadFile(filepath.Join(dir, parts[1]))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != parts[0] {
			t.Fatalf("fixture %s hash=%s want=%s", parts[1], got, parts[0])
		}
		checked++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("manifest contains no fixtures")
	}
}
