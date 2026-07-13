package compilerfixture

import (
	"path/filepath"
	"testing"
)

func TestCompilerOracleFixtures(t *testing.T) {
	fixtures, err := LoadDir(filepath.Join("fixtures"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.ID, func(t *testing.T) {
			if err := Verify(fixture); err != nil {
				t.Fatal(err)
			}
		})
	}
}
