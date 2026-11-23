package lifecycle

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/explorer/receiptmodel"

	"github.com/calypr/loom/internal/authscope"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle/ports"
)

func TestArchivedRecipeArtifactsRemainReadableButCannotExecute(t *testing.T) {
	backend, artifacts, reader, snapshot, receipt := artifactTestFixture(t)
	raw, err := json.Marshal(receipt.Bundle)
	if err != nil {
		t.Fatal(err)
	}
	var packet map[string]json.RawMessage
	if err = json.Unmarshal(raw, &packet); err != nil {
		t.Fatal(err)
	}
	var outputs []map[string]json.RawMessage
	if err = json.Unmarshal(packet["outputs"], &outputs); err != nil {
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
	if err = json.Unmarshal(raw, &receipt.Bundle); err != nil {
		t.Fatal(err)
	}
	receipt.RecipeDigest, err = receipt.Bundle.ArchiveDigest()
	if err != nil {
		t.Fatal(err)
	}
	receipt.ResolvedRecipeDigest = receipt.RecipeDigest
	receipt.CompilationKey, err = receiptmodel.CompilationKey(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	receipt.ID, err = receiptmodel.ReceiptID(*receipt)
	if err != nil {
		t.Fatal(err)
	}
	backend.revision.CompilationReceiptID = receipt.ID
	backend.revision.Recipe = receipt.Bundle
	backend.revision.RecipeDigest = receipt.RecipeDigest
	backend.revision.QualityReports[0].ReceiptID = receipt.ID
	reader.materialization.ReceiptID = receipt.ID
	originalReceipt, _ := json.Marshal(receipt)
	originalRevision, _ := json.Marshal(backend.revision)
	domain, err := explorer.NewService(backend)
	if err != nil {
		t.Fatal(err)
	}
	previewCalled := false
	service, err := New(domain, Config{ArtifactStore: artifacts, PublishedReader: reader,
		PreviewReceipt: func(context.Context, *receiptmodel.CompilationReceipt, recipe.RuntimeBindings, func(map[string]any) error) (dataframeexecution.PreviewSummary, error) {
			previewCalled = true
			return dataframeexecution.PreviewSummary{}, nil
		},
		Capability: ports.CapabilityResolver{ForExecution: func(context.Context, string, string) (ports.AuthorizedCapability, error) {
			return ports.AuthorizedCapability{Snapshot: snapshot, Scope: authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}}, nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = receipt.ValidateArchive(); err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareArtifact(context.Background(), ArtifactRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, RevisionID: "revision-a", OutputID: "patients", IdempotencyKey: "archived-recipe"})
	if err != nil {
		t.Fatal(err)
	}
	opened, record, err := service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: result.Record.ID})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(opened)
	opened.Close()
	if err != nil {
		t.Fatal(err)
	}
	if record.ReceiptID != receipt.ID || record.ExecutionID != "execution-a" || reader.streamCalls != 1 {
		t.Fatalf("archive identity changed: %+v calls=%d", record, reader.streamCalls)
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, member := range archive.File {
		if member.Name != "manifest.json" {
			continue
		}
		body, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := io.ReadAll(body)
		body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var fields struct {
			Selection struct {
				Recipe json.RawMessage `json:"recipe"`
			} `json:"selection"`
		}
		if err = json.Unmarshal(manifest, &fields); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(fields.Selection.Recipe, []byte(`"tableReshape"`)) || !bytes.Contains(fields.Selection.Recipe, []byte(`"archived-shape"`)) {
			t.Fatalf("archive provenance lost retired recipe: %s", fields.Selection.Recipe)
		}
		found = true
	}
	if !found {
		t.Fatal("artifact omitted manifest")
	}
	_, err = service.Preview(context.Background(), PreviewRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ReceiptID: receipt.ID, OutputID: "patients", Limit: 1})
	var failure *ports.Error
	if !errors.As(err, &failure) || failure.Code != "RECEIPT_RECOMPILE_REQUIRED" || previewCalled {
		t.Fatalf("retired execution outcome=%v callback=%v", err, previewCalled)
	}
	afterReceipt, _ := json.Marshal(receipt)
	afterRevision, _ := json.Marshal(backend.revision)
	if string(originalReceipt) != string(afterReceipt) || string(originalRevision) != string(afterRevision) {
		t.Fatal("archive reads rewrote immutable receipt/revision")
	}
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: "other-project", ExplorerID: receipt.ExplorerID, ArtifactID: record.ID})
	if err == nil {
		t.Fatal("foreign project read archived artifact")
	}
	receipt.Bundle.Outputs[0].Name = "changed"
	_, _, err = service.OpenArtifact(context.Background(), ArtifactOpenRequest{Project: receipt.Project, ExplorerID: receipt.ExplorerID, ArtifactID: record.ID})
	if err == nil || !strings.Contains(err.Error(), "integrity") {
		t.Fatalf("tampered archive receipt accepted or misclassified: %v", err)
	}
}
