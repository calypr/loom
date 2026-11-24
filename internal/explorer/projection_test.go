package explorer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	explorerconfig "github.com/calypr/loom/internal/explorer/config"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
)

const archiveIntegrationRecipe = `{"recipeSchemaVersion":1,"name":"archived-table","translationVersion":"v1","outputs":[{"name":"patients","rootResourceType":"Patient","rowGrain":"patient","tableReshape":{"kind":"UNPIVOT","unpivot":{"constructionId":"shape","inputs":[{"column":"value","key":{"kind":"STRING","string":"value"}}],"keyOutput":"name","keyLabel":"Name","valueOutput":"result","valueLabel":"Result","nullRowPolicy":"DROP"}}}]}`

func integrationArchiveBundle(t *testing.T) recipe.Bundle {
	t.Helper()
	var b recipe.Bundle
	if err := json.Unmarshal([]byte(archiveIntegrationRecipe), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestArchivedRecipeReceiptGoldenIdentity(t *testing.T) {
	b := integrationArchiveBundle(t)
	digest, err := b.ArchiveDigest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != "2ffdf4f14e5f6a339de8fcb2dcd1c8c0ac0bd6877be5d59b4a7f462d0eba4f6b" {
		t.Fatal(digest)
	}
	r := receiptmodel.CompilationReceipt{Project: "project", ExplorerID: "explorer", Bundle: b, RecipeDigest: digest}
	r.ID, err = receiptmodel.ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != "receipt_27dc8735710fecff3d05e0bd3ce97e9a3ab4970627491587529357c87b227d29" {
		t.Fatal(r.ID)
	}
	if err = r.ValidateArchive(); err != nil {
		t.Fatal(err)
	}
	if err = r.ValidateID(); err != nil {
		t.Fatal(err)
	}
	if receiptmodel.ReceiptContractSupportedForExecution(r) {
		t.Fatal("retired recipe admitted for execution")
	}
	if _, err = b.CurrentBundle(); err == nil {
		t.Fatal("archive converted to executable bundle")
	}
	before, _ := json.Marshal(r)
	var changed receiptmodel.CompilationReceipt
	if err = json.Unmarshal(before, &changed); err != nil {
		t.Fatal(err)
	}
	changed.Bundle.Outputs[0].Name = "tampered"
	if err = changed.ValidateArchive(); err == nil {
		t.Fatal("changed archived body authenticated under original receipt ID")
	}
	after, _ := json.Marshal(r)
	if string(before) != string(after) {
		t.Fatal("receipt archive read changed immutable packet")
	}
}

func TestArchivedRecipeReceiptPreservesFullContractAuthentication(t *testing.T) {
	r := testReceipt()
	r.Bundle = integrationArchiveBundle(t)
	r.RecipeDigest, _ = r.Bundle.ArchiveDigest()
	r.ResolvedRecipeDigest = r.RecipeDigest
	r.PublicOutputContract = json.RawMessage(`{"outputs":[{"outputId":"patients","columns":[],"lossless":true,"mlReady":true}]}`)
	r.OutputColumnProvenance = map[string]map[string]string{"patients": {"__loom_row_id": "EXPLICIT"}}
	r.OutputContractDigest, _ = receiptmodel.CompilationArtifactDigest(r.PublicOutputContract)
	r.CompilationKey, _ = receiptmodel.CompilationKey(r)
	r.ID, _ = receiptmodel.ReceiptID(r)
	if err := r.ValidateArchive(); err != nil {
		t.Fatal(err)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if receiptmodel.ReceiptContractSupportedForExecution(r) {
		t.Fatal("current contract version revived a retired recipe")
	}
	r.ResolvedRecipeDigest = "changed"
	if err := r.ValidateArchive(); err == nil {
		t.Fatal("archive authentication skipped resolved recipe digest")
	}
}

func TestArchivedRecipeViewerUsesFrozenMetadata(t *testing.T) {
	b := integrationArchiveBundle(t)
	config := explorerconfig.ConfigV2{APIVersion: explorerconfig.ConfigV2APIVersion, Kind: "ExplorerConfig", Recipe: json.RawMessage(archiveIntegrationRecipe), Views: []explorerconfig.ConfigView{{ID: "patients", Output: "patients", Title: "Frozen"}}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	revision := Revision{ID: "original-revision", Recipe: b, RecipeDigest: "original-recipe-digest", CompilationReceiptID: "original-receipt", Config: raw, Dataset: viewer.DatasetMetadata{Outputs: []viewer.DatasetOutput{{Name: "patients", Queryable: true, Columns: []publication.PhysicalColumn{{Name: "name", LogicalType: "string"}, {Name: "result", LogicalType: "string"}}}}}}
	before, _ := json.Marshal(revision)
	runtime, err := BuildViewerProjection(&revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 || runtime.Outputs[0].OutputID != "patients" {
		t.Fatalf("lost frozen output: %+v", runtime)
	}
	after, _ := json.Marshal(revision)
	if string(before) != string(after) {
		t.Fatal("Viewer rewrote immutable recipe/revision metadata")
	}
	config.Recipe = json.RawMessage(strings.Replace(archiveIntegrationRecipe, `"nullRowPolicy":"DROP"`, `"nullRowPolicy":"INVALID"`, 1))
	revision.Config, _ = json.Marshal(config)
	if _, err = BuildViewerProjection(&revision); err == nil {
		t.Fatal("Viewer accepted malformed archived payload")
	}
}

func TestArchivedTableShapeBindingStillAuthenticatesButCannotExecute(t *testing.T) {
	r := testReceipt()
	r.TableShapeProposal = &receiptmodel.TableShapeProposalBinding{DraftVersion: 1, DraftDigest: "draft", OutputID: "out", BaseDocumentDigest: "base", CandidateWorkspaceDigest: r.IntentDigest, SnapshotToken: r.SnapshotToken}
	var err error
	r.CompilationKey, err = receiptmodel.CompilationKey(r)
	if err != nil {
		t.Fatal(err)
	}
	r.ID, err = receiptmodel.ReceiptID(r)
	if err != nil {
		t.Fatal(err)
	}
	if err = r.ValidateArchive(); err != nil {
		t.Fatal(err)
	}
	if receiptmodel.ReceiptContractSupportedForExecution(r) {
		t.Fatal("retired TableShape proposal binding admitted for execution")
	}
	changed := *r.TableShapeProposal
	changed.BaseDocumentDigest = "tampered"
	r.TableShapeProposal = &changed
	if err = r.ValidateArchive(); err == nil {
		t.Fatal("stored proposal binding no longer participates in archive identity")
	}
}

func TestBuildViewerProjectionPreservesPublicContractColumnOrder(t *testing.T) {
	emitted := []viewer.EmittedColumn{
		{EmissionID: "subject-emission", OutputID: "observations", PublicColumn: "subject.reference", Label: "Subject", LogicalType: "string", Shape: "scalar"},
		{EmissionID: "status-emission", OutputID: "observations", PublicColumn: "status", Label: "Status", LogicalType: "string", Shape: "scalar"},
	}
	contracts := receiptmodel.PublicOutputContracts{Outputs: []receiptmodel.PublicOutputContract{{
		OutputID: "observations",
		Columns: []receiptmodel.PublicOutputColumn{
			{Column: "subject.reference", Label: "Subject", LogicalType: "string", Shape: "scalar"},
			{Column: "status", Label: "Status", LogicalType: "string", Shape: "scalar"},
		},
	}}}
	contractJSON, err := json.Marshal(contracts)
	if err != nil {
		t.Fatal(err)
	}
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                "viewer-order",
		TranslationVersion:  "1",
		Outputs: []recipe.Output{{
			Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		}},
	}
	revision := &Revision{
		Config: mustJSON(t, explorerconfig.ConfigV2{
			APIVersion: explorerconfig.ConfigV2APIVersion,
			Kind:       "ExplorerConfig",
			Recipe:     mustJSON(t, bundle),
			Views: []explorerconfig.ConfigView{{
				ID: "observations", Output: "observations",
				Table: explorerconfig.ConfigTable{Columns: []explorerconfig.ConfigColumn{
					{Column: "status", Visible: true},
					{Column: "subject.reference", Visible: true},
				}},
			}},
		}),
		Recipe: bundle,
		Dataset: viewer.DatasetMetadata{Outputs: []viewer.DatasetOutput{{
			Name: "observations", Queryable: true,
			Columns: []publication.PhysicalColumn{
				{Name: "status", LogicalType: "string", ClickHouse: "String"},
				{Name: "subject.reference", LogicalType: "string", ClickHouse: "String"},
			},
		}}},
		EmittedColumns:       emitted,
		PublicOutputContract: contractJSON,
	}

	runtime, err := BuildViewerProjection(revision)
	if err != nil {
		t.Fatal(err)
	}
	if runtime == nil || len(runtime.Outputs) != 1 {
		t.Fatalf("runtime outputs = %#v", runtime)
	}
	columns := runtime.Outputs[0].Columns
	if len(columns) != 2 || columns[0].Column != "subject.reference" || columns[1].Column != "status" {
		t.Fatalf("runtime columns = %#v, want frozen public-contract order", columns)
	}
	table := runtime.Outputs[0].Table.Columns
	if len(table) != 2 || table[0].Column != "subject.reference" || table[1].Column != "status" {
		t.Fatalf("runtime table columns = %#v, want frozen public-contract order", table)
	}

	saved := &explorerconfig.ProjectConfig{Views: []explorerconfig.ConfigView{{
		ID: "observations", Output: "observations",
		Table: explorerconfig.ConfigTable{Columns: []explorerconfig.ConfigColumn{
			{Column: "status", Label: "Saved status", Visible: true, Pinned: true},
		}},
	}}}
	runtime, err = buildViewerProjection(revision, saved)
	if err != nil {
		t.Fatal(err)
	}
	output := runtime.Outputs[0]
	if len(output.Table.Columns) != 1 || output.Table.Columns[0].Column != "status" || !output.Table.Columns[0].Pinned {
		t.Fatalf("runtime table does not match the saved view: %#v", output.Table.Columns)
	}
	if output.Columns[1].Label != "Saved status" {
		t.Fatalf("runtime status label = %q", output.Columns[1].Label)
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
