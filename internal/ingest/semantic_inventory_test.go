package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bmeg/jsonschemagraph/graph"
	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
	arangostore "github.com/calypr/loom/internal/store/arango"
)

func TestLoadFilePersistsSemanticInventoryFromSharedEmitter(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Observation.ndjson")
	data := []byte(`{"resourceType":"Observation","id":"obs-1","status":"final","code":{"coding":[{"system":"urn:inventory","version":"v1","code":"panel"}]},"component":[{"code":{"coding":[{"system":"urn:inventory","version":"v1","code":"glucose","display":"Glucose"}]},"valueQuantity":{"value":1,"unit":"mg"}}]}` + "\n")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	opts := normalizeLoadOptions(LoadOptions{
		Project:     "project",
		MetaDir:     root,
		BatchSize:   1,
		WorkerCount: 1,
		WriterCount: 1,
	})
	var inventoryDocs []json.RawMessage
	var inventoryOverwrite []bool
	result, err := loadFile(context.Background(), opts, nil, schema, file, "generation-1", false, time.Now(), 0, 0,
		func(_ context.Context, _ *arangostore.Client, collection string, docs []json.RawMessage, overwrite bool, _ string) error {
			if collection == catalog.SemanticInventoryCollection {
				inventoryDocs = append(inventoryDocs, docs...)
				inventoryOverwrite = append(inventoryOverwrite, overwrite)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventoryDocs) != 2 || result.SemanticInventoryBatches != 2 {
		message := "no row error sample"
		if len(result.RowErrors) > 0 {
			message = result.RowErrors[0].Message
		}
		t.Fatalf("inventory docs=%d batches=%d rows=%d validation=%d sample=%s", len(inventoryDocs), result.SemanticInventoryBatches, result.Rows, result.ValidationErrors, message)
	}
	if len(inventoryOverwrite) != 2 || !inventoryOverwrite[0] || !inventoryOverwrite[1] {
		t.Fatalf("inventory overwrite modes = %v, want source-key replacement", inventoryOverwrite)
	}
	var contribution catalog.SemanticInventoryContribution
	if err := json.Unmarshal(inventoryDocs[0], &contribution); err != nil {
		t.Fatal(err)
	}
	if contribution.SourceID != "Observation.ndjson#1" || contribution.AuthResourcePath != "" {
		t.Fatalf("inventory source/scope = %q/%q, want stable relative source locator and unscoped path", contribution.SourceID, contribution.AuthResourcePath)
	}
	if contribution.ConceptID == "" || contribution.BindingID == "" || contribution.Observation.Key.Version != "v1" {
		t.Fatalf("inventory identity/observation = %+v", contribution)
	}
}

func TestLoadFilePersistsCoverageSentinelBeforeReturningForCheckpoint(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Patient.ndjson")
	if err := os.WriteFile(file, []byte(`{"resourceType":"Patient","id":"patient-1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation-1")
	coverageWriter := &coverageContributionWriterFake{}
	identity := catalogarango.SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation-1", BuildID: buildID,
		SchemaIdentity: strings.Repeat("a", 64), SourceKind: catalog.SemanticInventorySourceFile,
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", MetaDir: root, AuthResourcePath: "scope-a", BatchSize: 1, WorkerCount: 1, WriterCount: 1})
	sourceSet := catalogarango.NewSemanticCoverageSourceSetAccumulator("Patient", true)
	result, err := loadFile(context.Background(), opts, nil, schema, file, "generation-1", false, time.Now(), 0, 0,
		func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil },
		semanticCoverageLoadContext{Identity: identity, Writer: coverageWriter, SourceSet: sourceSet},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.SemanticCoverageSources != 1 || result.SemanticCoverageBatches != 1 || result.SemanticInventoryBatches != 0 {
		t.Fatalf("coverage file result = %+v, want one coverage source and no semantic emissions", result)
	}
	if len(coverageWriter.contributions) != 1 {
		t.Fatalf("coverage contributions = %d, want one zero-emission source sentinel", len(coverageWriter.contributions))
	}
	contribution := coverageWriter.contributions[0]
	if result.SemanticCoverageRows != int64(1+len(contribution.Paths)) {
		var facetRows int
		for _, path := range contribution.Paths {
			facetRows += len(path.Facets)
		}
		if result.SemanticCoverageRows != int64(1+len(contribution.Paths)+facetRows) {
			t.Fatalf("coverage rows=%d paths=%d facets=%d, want source/path/facet aggregates", result.SemanticCoverageRows, len(contribution.Paths), facetRows)
		}
	}
	if contribution.Identity.SourceID != "Patient.ndjson#1" || contribution.Identity.AuthResourcePath != "scope-a" || contribution.Identity.SchemaIdentity != identity.SchemaIdentity || contribution.BuildID != buildID || contribution.SourceRecordCount != 1 {
		t.Fatalf("coverage source identity = %+v build=%q, want exact file/build/schema identity", contribution.Identity, contribution.BuildID)
	}
	expectation, err := sourceSet.Snapshot(true)
	if err != nil {
		t.Fatal(err)
	}
	if expectation.SourceCount != 1 || len(expectation.Partitions) != 1 || expectation.Partitions[0].AuthResourcePath != "scope-a" || expectation.Partitions[0].SourceCount != 1 {
		t.Fatalf("file source set expectation = %+v, want one exact source in scope-a", expectation)
	}
}

type coverageContributionWriterFake struct {
	contributions []catalog.SemanticCoverageSourceContribution
}

func (f *coverageContributionWriterFake) WriteSemanticCoverageContributions(_ context.Context, buildID string, contributions []catalog.SemanticCoverageSourceContribution, _ int) (catalogarango.SemanticCoverageWriteStats, error) {
	f.contributions = append(f.contributions, contributions...)
	stats := catalogarango.SemanticCoverageWriteStats{}
	for _, contribution := range contributions {
		if contribution.BuildID != buildID {
			return stats, errors.New("wrong semantic coverage build id")
		}
		stats.Rows += 1 + int64(len(contribution.Paths))
		for _, path := range contribution.Paths {
			stats.Rows += int64(len(path.Facets))
		}
	}
	return stats, nil
}

func TestLoadFileMissingSchemaKeepsInventoryWorkerRunningWithIncompleteCoverage(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "Patient.ndjson")
	if err := os.WriteFile(file, []byte(`{"resourceType":"Patient","id":"patient-1"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	schema, err := graph.Load(repoPath(t, "schemas", "graph-fhir.json"))
	if err != nil {
		t.Fatal(err)
	}
	buildID := catalog.SemanticInventoryBuildID("project", "generation-1")
	coverageWriter := &coverageContributionWriterFake{}
	identity := catalogarango.SemanticCoverageBuildIdentity{
		Project: "project", DatasetGeneration: "generation-1", BuildID: buildID,
		SchemaIdentity: "", SourceKind: catalog.SemanticInventorySourceFile,
	}
	opts := normalizeLoadOptions(LoadOptions{Project: "project", MetaDir: root, BatchSize: 1, WorkerCount: 1, WriterCount: 1})
	result, err := loadFile(context.Background(), opts, nil, schema, file, "generation-1", false, time.Now(), 0, 0,
		func(context.Context, *arangostore.Client, string, []json.RawMessage, bool, string) error { return nil },
		semanticCoverageLoadContext{Identity: identity, Writer: coverageWriter, SourceSet: catalogarango.NewSemanticCoverageSourceSetAccumulator("Patient", true)},
	)
	if err != nil {
		t.Fatalf("load file with absent coverage schema identity: %v", err)
	}
	if result.Rows != 1 || result.SemanticCoverageSources != 1 || len(coverageWriter.contributions) != 1 {
		t.Fatalf("worker result=%+v contributions=%d, want ordinary row and one incomplete coverage source", result, len(coverageWriter.contributions))
	}
	if contribution := coverageWriter.contributions[0]; contribution.Identity.SchemaIdentity != "" || contribution.Complete {
		t.Fatalf("missing-schema contribution = %+v, want incomplete exact empty identity", contribution)
	}
}

func TestSemanticInventorySourceLocatorIncludesRelativeDirectory(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "a", "Observation.ndjson")
	second := filepath.Join(root, "b", "Observation.ndjson")
	firstID := semanticInventorySourceRecordID(semanticInventorySourceFileID(root, first), 7)
	secondID := semanticInventorySourceRecordID(semanticInventorySourceFileID(root, second), 7)
	if firstID == secondID {
		t.Fatalf("same-basename source files collided at %q", firstID)
	}
	if firstID != "a/Observation.ndjson#7" || secondID != "b/Observation.ndjson#7" {
		t.Fatalf("relative source IDs = %q and %q", firstID, secondID)
	}
}
