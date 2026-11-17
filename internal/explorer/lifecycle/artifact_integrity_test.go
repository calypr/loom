package lifecycle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/artifactfs"
)

func TestArchivedRecipeArtifactFilesystemDownloadRetainsAuthorizedBytes(t *testing.T) {
	backend, _, reader, snapshot, receipt := artifactTestFixture(t)
	raw, err := json.Marshal(receipt.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	var packet map[string]json.RawMessage
	if err := json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	var outputs []map[string]json.RawMessage
	if err := json.Unmarshal(packet["outputs"], &outputs); err != nil {
		t.Fatal(err)
	}
	outputs[0]["tableReshape"] = json.RawMessage(`{"kind":"UNPIVOT","unpivot":{"constructionId":"archived-shape","inputs":[{"column":"patient_id","key":{"kind":"STRING","string":"id"}}],"keyOutput":"key","keyLabel":"Key","valueOutput":"value","valueLabel":"Value","nullRowPolicy":"DROP"}}`)
	packet["outputs"], err = json.Marshal(outputs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &receipt.Bundle); err != nil {
		t.Fatal(err)
	}
	receipt.RecipeDigest, err = receipt.Bundle.ArchiveDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.ResolvedRecipeDigest = receipt.RecipeDigest
	receipt.CompilationKey, err = explorer.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = explorer.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	backend.revision.CompilationReceiptID = receipt.ID
	backend.revision.Recipe = receipt.Bundle
	backend.revision.RecipeDigest = receipt.RecipeDigest
	backend.revision.QualityReports[0].ReceiptID = receipt.ID
	reader.materialization.ReceiptID = receipt.ID

	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	store, err := artifactfs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(domain, Config{
		ArtifactStore:   store,
		PublishedReader: reader,
		PreviewReceipt: func(context.Context, *explorer.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			return dataframeexecution.PreviewSummary{}, nil
		},
		Capability: CapabilityResolver{ForExecution: func(context.Context, string, string) (AuthorizedCapability, error) {
			return AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareArtifact(context.Background(), ArtifactRequest{
		Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "archive-integrity",
	})
	if err != nil {
		t.Fatal(err)
	}
	body, record, err := service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: result.Record.ID})
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	archiveBytes, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archiveBytes)
	if hex.EncodeToString(digest[:]) != record.ArchiveSHA256 || int64(len(archiveBytes)) != record.Bytes {
		t.Fatalf("opened artifact bytes do not match its digest/size record: digest=%s record=%s bytes=%d recorded=%d", hex.EncodeToString(digest[:]), record.ArchiveSHA256, len(archiveBytes), record.Bytes)
	}
	archive, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range archive.File {
		if member.Name != "manifest.json" {
			continue
		}
		file, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(manifest, []byte(`"tableReshape"`)) || !bytes.Contains(manifest, []byte(`"archived-shape"`)) {
			t.Fatalf("authorized archived-recipe download lost its historical recipe: %s", manifest)
		}
		return
	}
	t.Fatal("authorized archived-recipe download omitted its manifest")
}
