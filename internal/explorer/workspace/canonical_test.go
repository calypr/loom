package workspace

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestStoredArchiveRetirementPreservesHistoricalCanonicalIdentity(t *testing.T) {
	for _, test := range []struct{ name, digest, canonical string }{
		{"WP17_ORACLE/pivot-derived-chain", "sha256:f6ea73df23fc6934b0963cda54fab80a8bb17700f1eb9fa4db5dbe1b18dc295b", "{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":12,\"explorer\":{\"title\":\"Construction test\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"output\":{\"id\":\"patients\",\"title\":\"patients\"},\"rootResourceType\":\"Patient\",\"route\":{\"occurrenceId\":\"base\",\"resourceType\":\"Patient\"},\"rows\":{\"kind\":\"RECORDS\",\"records\":{}},\"columns\":[{\"column\":\"category\",\"label\":\"Category\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"group_id\",\"label\":\"Group\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"person_id\",\"label\":\"Person ID\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"value\",\"label\":\"Value\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}}],\"tableShape\":{\"reshape\":{\"kind\":\"PIVOT\",\"pivot\":{\"constructionId\":\"legacy_pivot\",\"groupKeys\":[\"group_id\"],\"categoryColumn\":\"category\",\"valueColumn\":\"value\",\"categories\":[{\"key\":{\"kind\":\"STRING\",\"string\":\"A\"},\"output\":{\"column\":\"amount_a\",\"label\":\"Amount A\"}},{\"key\":{\"kind\":\"STRING\",\"string\":\"B\"},\"output\":{\"column\":\"amount_b\",\"label\":\"Amount B\"}}],\"duplicatePolicy\":\"MAX\",\"missingCellPolicy\":\"NULL\",\"unlistedCategoryPolicy\":\"ERROR\"}},\"derived\":[{\"constructionId\":\"legacy_ratio\",\"output\":{\"column\":\"ratio\",\"label\":\"Ratio\"},\"operation\":\"ADD\",\"left\":{\"kind\":\"COLUMN\",\"column\":\"twice_a\"},\"right\":{\"kind\":\"COLUMN\",\"column\":\"amount_b\"},\"missingInputPolicy\":\"PROPAGATE_NULL\"},{\"constructionId\":\"legacy_twice\",\"output\":{\"column\":\"twice_a\",\"label\":\"Twice A\"},\"operation\":\"MULTIPLY\",\"left\":{\"kind\":\"COLUMN\",\"column\":\"amount_a\"},\"right\":{\"kind\":\"LITERAL\",\"literal\":{\"kind\":\"INTEGER\",\"integer\":2}},\"missingInputPolicy\":\"PROPAGATE_NULL\"}]}}],\"tabs\":[{\"id\":\"tab-patients\",\"title\":\"Patients\",\"outputId\":\"patients\",\"order\":0,\"visible\":true}]}"},
		{"WP18_ORACLE/pivot-derived-chain", "sha256:f6ea73df23fc6934b0963cda54fab80a8bb17700f1eb9fa4db5dbe1b18dc295b", "{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":12,\"explorer\":{\"title\":\"Construction test\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"output\":{\"id\":\"patients\",\"title\":\"patients\"},\"rootResourceType\":\"Patient\",\"route\":{\"occurrenceId\":\"base\",\"resourceType\":\"Patient\"},\"rows\":{\"kind\":\"RECORDS\",\"records\":{}},\"columns\":[{\"column\":\"category\",\"label\":\"Category\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"group_id\",\"label\":\"Group\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"person_id\",\"label\":\"Person ID\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"value\",\"label\":\"Value\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}}],\"tableShape\":{\"reshape\":{\"kind\":\"PIVOT\",\"pivot\":{\"constructionId\":\"legacy_pivot\",\"groupKeys\":[\"group_id\"],\"categoryColumn\":\"category\",\"valueColumn\":\"value\",\"categories\":[{\"key\":{\"kind\":\"STRING\",\"string\":\"A\"},\"output\":{\"column\":\"amount_a\",\"label\":\"Amount A\"}},{\"key\":{\"kind\":\"STRING\",\"string\":\"B\"},\"output\":{\"column\":\"amount_b\",\"label\":\"Amount B\"}}],\"duplicatePolicy\":\"MAX\",\"missingCellPolicy\":\"NULL\",\"unlistedCategoryPolicy\":\"ERROR\"}},\"derived\":[{\"constructionId\":\"legacy_ratio\",\"output\":{\"column\":\"ratio\",\"label\":\"Ratio\"},\"operation\":\"ADD\",\"left\":{\"kind\":\"COLUMN\",\"column\":\"twice_a\"},\"right\":{\"kind\":\"COLUMN\",\"column\":\"amount_b\"},\"missingInputPolicy\":\"PROPAGATE_NULL\"},{\"constructionId\":\"legacy_twice\",\"output\":{\"column\":\"twice_a\",\"label\":\"Twice A\"},\"operation\":\"MULTIPLY\",\"left\":{\"kind\":\"COLUMN\",\"column\":\"amount_a\"},\"right\":{\"kind\":\"LITERAL\",\"literal\":{\"kind\":\"INTEGER\",\"integer\":2}},\"missingInputPolicy\":\"PROPAGATE_NULL\"}]}}],\"tabs\":[{\"id\":\"tab-patients\",\"title\":\"Patients\",\"outputId\":\"patients\",\"order\":0,\"visible\":true}]}"},
		{"WP17_ORACLE/explicit-pivot", "sha256:2ea380e6b05e756c837074ec8ee57d82da425513a0ea2761a24f9b4a105d7022", "{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":12,\"explorer\":{\"title\":\"Table shape\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"output\":{\"id\":\"observations\",\"title\":\"Observations\"},\"rootResourceType\":\"Observation\",\"route\":{\"occurrenceId\":\"base\",\"resourceType\":\"Observation\"},\"rows\":{\"kind\":\"RECORDS\",\"records\":{}},\"columns\":[{\"column\":\"category\",\"label\":\"Category\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"group\",\"label\":\"Group\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"measure\",\"label\":\"Measure\",\"logicalType\":\"decimal\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}}],\"tableShape\":{\"reshape\":{\"kind\":\"PIVOT\",\"pivot\":{\"constructionId\":\"pivot_1\",\"groupKeys\":[\"group\"],\"categoryColumn\":\"category\",\"valueColumn\":\"measure\",\"categories\":[{\"key\":{\"kind\":\"STRING\",\"string\":\"heart_rate\"},\"output\":{\"column\":\"heart_rate\",\"label\":\"Heart rate\"}}],\"duplicatePolicy\":\"ERROR\",\"missingCellPolicy\":\"NULL\",\"unlistedCategoryPolicy\":\"ERROR\"}}}}],\"tabs\":[{\"id\":\"observations\",\"title\":\"Observations\",\"outputId\":\"observations\",\"order\":0,\"visible\":true}]}"},
		{"WP18_ORACLE/explicit-pivot", "sha256:2ea380e6b05e756c837074ec8ee57d82da425513a0ea2761a24f9b4a105d7022", "{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":12,\"explorer\":{\"title\":\"Table shape\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"output\":{\"id\":\"observations\",\"title\":\"Observations\"},\"rootResourceType\":\"Observation\",\"route\":{\"occurrenceId\":\"base\",\"resourceType\":\"Observation\"},\"rows\":{\"kind\":\"RECORDS\",\"records\":{}},\"columns\":[{\"column\":\"category\",\"label\":\"Category\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"group\",\"label\":\"Group\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"column\":\"measure\",\"label\":\"Measure\",\"logicalType\":\"decimal\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}}],\"tableShape\":{\"reshape\":{\"kind\":\"PIVOT\",\"pivot\":{\"constructionId\":\"pivot_1\",\"groupKeys\":[\"group\"],\"categoryColumn\":\"category\",\"valueColumn\":\"measure\",\"categories\":[{\"key\":{\"kind\":\"STRING\",\"string\":\"heart_rate\"},\"output\":{\"column\":\"heart_rate\",\"label\":\"Heart rate\"}}],\"duplicatePolicy\":\"ERROR\",\"missingCellPolicy\":\"NULL\",\"unlistedCategoryPolicy\":\"ERROR\"}}}}],\"tabs\":[{\"id\":\"observations\",\"title\":\"Observations\",\"outputId\":\"observations\",\"order\":0,\"visible\":true}]}"},
		{"WP17_ORACLE/typed-unpivot", "sha256:25aaa21500265cbbe876a6544af3b72bec1e38d0265e32acbc23e2f25ad25107", "{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":12,\"explorer\":{\"title\":\"Construction test\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"output\":{\"id\":\"patients\",\"title\":\"patients\"},\"rootResourceType\":\"Patient\",\"route\":{\"occurrenceId\":\"base\",\"resourceType\":\"Patient\"},\"rows\":{\"kind\":\"RECORDS\",\"records\":{}},\"columns\":[{\"columnId\":\"category_id\",\"column\":\"category\",\"label\":\"Category\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"columnId\":\"group_id\",\"column\":\"group_id\",\"label\":\"Group\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"columnId\":\"person_id\",\"column\":\"person_id\",\"label\":\"Person ID\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"columnId\":\"value_id\",\"column\":\"value\",\"label\":\"Value\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}}],\"tableShape\":{\"reshape\":{\"kind\":\"UNPIVOT\",\"unpivot\":{\"constructionId\":\"legacy_unpivot\",\"inputs\":[{\"column\":\"group_id\",\"key\":{\"kind\":\"INTEGER\",\"integer\":10}},{\"column\":\"category\",\"key\":{\"kind\":\"INTEGER\",\"integer\":20}}],\"keyOutput\":{\"column\":\"metric\",\"label\":\"Metric\"},\"valueOutput\":{\"column\":\"metric_value\",\"label\":\"Metric value\"},\"nullRowPolicy\":\"PRESERVE\"}}}}],\"tabs\":[{\"id\":\"tab-patients\",\"title\":\"Patients\",\"outputId\":\"patients\",\"order\":0,\"visible\":true}]}"},
		{"WP18_ORACLE/typed-unpivot", "sha256:25aaa21500265cbbe876a6544af3b72bec1e38d0265e32acbc23e2f25ad25107", "{\"apiVersion\":\"loom.calypr.org/explorer-authoring/v2\",\"kind\":\"ExplorerBuilderWorkspace\",\"semanticsVersion\":12,\"explorer\":{\"title\":\"Construction test\"},\"documents\":[{\"kind\":\"ExplorerBuilderDocument\",\"output\":{\"id\":\"patients\",\"title\":\"patients\"},\"rootResourceType\":\"Patient\",\"route\":{\"occurrenceId\":\"base\",\"resourceType\":\"Patient\"},\"rows\":{\"kind\":\"RECORDS\",\"records\":{}},\"columns\":[{\"columnId\":\"category_id\",\"column\":\"category\",\"label\":\"Category\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"columnId\":\"group_id\",\"column\":\"group_id\",\"label\":\"Group\",\"logicalType\":\"string\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"columnId\":\"person_id\",\"column\":\"person_id\",\"label\":\"Person ID\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}},{\"columnId\":\"value_id\",\"column\":\"value\",\"label\":\"Value\",\"logicalType\":\"integer\",\"occurrenceId\":\"base\",\"source\":{\"kind\":\"projectId\"}}],\"tableShape\":{\"reshape\":{\"kind\":\"UNPIVOT\",\"unpivot\":{\"constructionId\":\"legacy_unpivot\",\"inputs\":[{\"column\":\"group_id\",\"key\":{\"kind\":\"INTEGER\",\"integer\":10}},{\"column\":\"category\",\"key\":{\"kind\":\"INTEGER\",\"integer\":20}}],\"keyOutput\":{\"column\":\"metric\",\"label\":\"Metric\"},\"valueOutput\":{\"column\":\"metric_value\",\"label\":\"Metric value\"},\"nullRowPolicy\":\"PRESERVE\"}}}}],\"tabs\":[{\"id\":\"tab-patients\",\"title\":\"Patients\",\"outputId\":\"patients\",\"order\":0,\"visible\":true}]}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := []byte(test.canonical)
			workspace, err := VerifyStoredWorkspaceForMigration(raw, test.digest)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := CanonicalStoredWorkspaceForMigration(workspace)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(canonical, raw) || workspaceDigest(canonical) != test.digest {
				t.Fatalf("archived identity changed: %s", canonical)
			}
			if _, err := VerifyStoredWorkspaceForMigration(raw, "sha256:wrong"); err == nil {
				t.Fatal("unverified archive accepted")
			}
			for _, decode := range []func([]byte) (Workspace, error){DecodeWorkspace, DecodeCurrentWorkspace} {
				if _, err := decode(raw); err == nil {
					t.Fatal("runtime decoder activated archive")
				}
			}
			if err := workspace.Validate(); err == nil {
				t.Fatal("runtime validation activated archive")
			}
			if _, err := workspace.CanonicalJSON(); err == nil {
				t.Fatal("runtime canonical writer activated archive")
			}
			if _, err := workspace.Digest(); err == nil {
				t.Fatal("runtime digest activated archive")
			}
			if _, err := CloneCurrentWorkspace(workspace); err == nil {
				t.Fatal("runtime clone activated archive")
			}
			if _, err := PrepareWorkspaceForCompilation(workspace, CatalogSnapshot{}); err == nil {
				t.Fatal("runtime preparation activated archive")
			}
			before, _ := json.Marshal(workspace)
			if _, err := StartDocumentConstruction(workspace.Documents[0]); err == nil {
				t.Fatal("construction initializer converted archive")
			}
			after, _ := json.Marshal(workspace)
			if !bytes.Equal(before, after) {
				t.Fatal("rejection mutated archive")
			}
			migrated, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeWorkspace(migrated.CanonicalJSON); err != nil {
				t.Fatal(err)
			}
			if reflect.DeepEqual(migrated.CanonicalJSON, raw) {
				t.Fatal("migration failed to retire archive")
			}
			if migrated.Workspace.Documents[0].ArchiveRetiredShape != nil || migrated.Workspace.Documents[0].Construction == nil {
				t.Fatal("migration retained archive")
			}
		})
	}
}
