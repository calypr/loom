package sourceload

import (
	"encoding/json"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest"
	"testing"
)

func TestSourceCopyPreservesTheIdentityFormatOfAnInProgressManifest(t *testing.T) {
	for _, format := range []struct {
		name  string
		value string
	}{
		{name: "legacy omitted manifest field", value: ""},
		{name: "explicit legacy", value: publication.IdentityFormatLegacy},
		{name: "compact", value: publication.IdentityFormatCompact},
	} {
		t.Run(format.name, func(t *testing.T) {
			doc := map[string]any{
				"_key": "old-physical-key", "logical_key": "patient-1", "project": "project-a",
				"payload": map[string]any{"id": "patient-1"},
			}
			copied, err := copyVertexDocument("Patient", "project-a", "generation-a", format.value)(sourceCopyRow{Document: doc})
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Key string `json:"_key"`
			}
			if err := json.Unmarshal(copied, &decoded); err != nil {
				t.Fatal(err)
			}
			wantFormat := format.value
			if wantFormat == "" {
				wantFormat = publication.IdentityFormatLegacy
			}
			want := ingest.GenerationDocumentKeyWithFormat("project-a", "generation-a", "Patient", "patient-1", wantFormat)
			if got := decoded.Key; got != want {
				t.Fatalf("copied _key = %q, want %q", got, want)
			}
		})
	}
}

func TestSourceSchemaChangeRequiresFullReload(t *testing.T) {
	current := publication.SchemaSnapshot{
		SchemaSHA256: "current", GeneratedResourceTypes: []string{"Observation", "Patient"},
	}
	previous := publication.SchemaSnapshot{SchemaSHA256: "previous"}
	if copySource, err := sourceSchemaAllowsCopy(current, current, []string{"Patient"}); err != nil || !copySource {
		t.Fatalf("same schema partial update copy = %t, err = %v", copySource, err)
	}
	if copySource, err := sourceSchemaAllowsCopy(previous, current, []string{"Patient"}); err == nil || copySource {
		t.Fatalf("different schema partial update copy = %t, err = %v", copySource, err)
	}
	if copySource, err := sourceSchemaAllowsCopy(previous, current, []string{"Patient", "Observation"}); err != nil || copySource {
		t.Fatalf("different schema full update copy = %t, err = %v", copySource, err)
	}
}
