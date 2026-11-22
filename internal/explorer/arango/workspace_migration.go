package arango

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/projectid"
	store "github.com/calypr/loom/internal/store/arango"
)

type WorkspaceMigrationReport struct {
	Version            string `json:"version"`
	DryRun             bool   `json:"dryRun"`
	AlreadyApplied     bool   `json:"alreadyApplied"`
	Owners             int    `json:"owners"`
	Projections        int    `json:"projections"`
	RepositoryConfigs  int    `json:"repositoryConfigs"`
	RetiredReceipts    int    `json:"retiredReceipts"`
	ChangedDigests     int    `json:"changedDigests"`
	HistoryRows        int    `json:"historyRows"`
	WouldRecordVersion bool   `json:"wouldRecordVersion"`
}

type workspaceMigrationDocument struct {
	collection, key, revision string
	raw                       json.RawMessage
}

type workspaceMigrationPlan struct {
	report       WorkspaceMigrationReport
	owners       []workspaceOwnerMigration
	projections  []explorer.WorkspaceProjection
	repositories []workspaceRepositoryMigration
}

type workspaceOwnerMigration struct {
	original explorer.Explorer
	migrated authoringv2.StoredWorkspaceMigration
	revision string
}

type workspaceRepositoryMigration struct {
	source   workspaceMigrationDocument
	migrated authoringv2.StoredWorkspaceMigration
}

func MigrateStoredWorkspaces(ctx context.Context, client *store.Client, dryRun bool) (WorkspaceMigrationReport, error) {
	return migrateWorkspaces(ctx, client, dryRun, workspaceEncodingTarget)
}

func MigrateStoredTableShapes(ctx context.Context, client *store.Client, dryRun bool) (WorkspaceMigrationReport, error) {
	return migrateWorkspaces(ctx, client, dryRun, tableShapeConstructionTarget)
}

func migrateWorkspaces(ctx context.Context, client *store.Client, dryRun bool, target workspaceMigrationTarget) (WorkspaceMigrationReport, error) {
	adapter, err := New(client)
	if err != nil {
		return WorkspaceMigrationReport{}, err
	}
	plan, err := adapter.planStoredWorkspaces(ctx, client, target)
	if err != nil {
		return plan.report, err
	}
	plan.report.DryRun = dryRun
	plan.report.WouldRecordVersion = dryRun && !plan.report.AlreadyApplied
	if dryRun {
		return plan.report, nil
	}
	if plan.report.AlreadyApplied {
		if len(plan.owners)+len(plan.repositories)+len(plan.projections) != 0 {
			return plan.report, fmt.Errorf("workspace migration ledger exists but unmigrated packets remain")
		}
		return plan.report, nil
	}
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{workspaceProjectionCollectionSpec(), {Name: workspaceMigrationLedger}}}); err != nil {
		return plan.report, err
	}
	for _, projection := range plan.projections {
		if err := insertWorkspaceProjectionFor(ctx, client, projection, target); err != nil {
			return plan.report, err
		}
	}
	for _, migration := range plan.owners {
		owner := migration.original
		owner.DraftConfig = append([]byte(nil), migration.migrated.CanonicalJSON...)
		owner.DraftDigest = migration.migrated.MigratedCanonicalDigest
		owner.LastAuthoringCommandID, owner.LastAuthoringCommandDigest = "", ""
		owner.LastAuthoringCommandResults = nil
		_, err := adapter.saveDraft(ctx, owner, owner.DraftVersion, []string{migration.original.DraftDigest}, &draftMigrationWrite{
			collections: []string{workspaceProjectionCollection},
			revision:    migration.revision,
			beforeHistory: func(txCtx context.Context, tx store.RowQueryer, previous explorer.Explorer, history explorer.DraftRevision) error {
				expected := draftRevisionFromExplorer(migration.original)
				expected.ID = history.ID
				if explorer.DraftWorkspaceArtifact(expected) != explorer.DraftWorkspaceArtifact(history) {
					return explorer.ErrDraftConflict
				}
				projection := newWorkspaceProjection(explorer.DraftWorkspaceArtifact(history), migration.migrated)
				return insertWorkspaceProjectionFor(txCtx, tx, projection, target)
			},
		})
		if err != nil {
			return plan.report, fmt.Errorf("migrate owner %s/%s: %w", owner.Project, owner.ExplorerID, err)
		}
	}
	for _, migration := range plan.repositories {
		updated := false
		err := client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d._rev == @revision
UPDATE d WITH {workspace:@workspace, intentDigest:@digest} IN @@c RETURN NEW`, 1,
			map[string]any{"@c": migration.source.collection, "key": migration.source.key, "revision": migration.source.revision, "workspace": json.RawMessage(migration.migrated.CanonicalJSON), "digest": migration.migrated.MigratedCanonicalDigest}, func(map[string]any) error { updated = true; return nil })
		if err != nil {
			return plan.report, err
		}
		if !updated {
			return plan.report, explorer.ErrDraftConflict
		}
	}

	final, err := adapter.planStoredWorkspaces(ctx, client, target)
	if err != nil {
		return plan.report, err
	}
	if len(final.owners)+len(final.repositories)+len(final.projections) != 0 {
		return plan.report, fmt.Errorf("workspace migration did not converge")
	}
	marker := map[string]any{"_key": target.version(), "version": target.version(), "completedAt": time.Now().UTC()}
	if err := client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS {overwriteMode:"ignore"} RETURN NEW`, 1, map[string]any{"@c": workspaceMigrationLedger, "doc": marker}, func(map[string]any) error { return nil }); err != nil {
		return plan.report, err
	}
	sealed := false
	if err := client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1,
		map[string]any{"@c": workspaceMigrationLedger, "key": target.version()}, func(row map[string]any) error {
			sealed = row["version"] == target.version()
			return nil
		}); err != nil {
		return plan.report, err
	}
	if !sealed {
		return plan.report, fmt.Errorf("workspace migration version could not be verified")
	}
	return plan.report, nil
}

func (s *Store) planStoredWorkspaces(ctx context.Context, client *store.Client, target workspaceMigrationTarget) (workspaceMigrationPlan, error) {
	plan := workspaceMigrationPlan{report: WorkspaceMigrationReport{Version: target.version()}}
	names, err := client.CollectionNamesReadOnly(ctx)
	if err != nil {
		return plan, err
	}
	if target == tableShapeConstructionTarget {
		if !slices.Contains(names, workspaceMigrationLedger) {
			return plan, fmt.Errorf("run %s before %s", workspaceMigrationVersion, target.version())
		}
		predecessor, err := migrationVersionRecorded(ctx, client, workspaceMigrationVersion)
		if err != nil {
			return plan, err
		}
		if !predecessor {
			return plan, fmt.Errorf("run %s before %s", workspaceMigrationVersion, target.version())
		}
	}
	audit := WorkspaceAuditReport{}
	documents := map[string]workspaceMigrationDocument{}
	retiredReceipts := map[string]bool{}
	for _, name := range names {
		err := client.VisitCollectionDocumentsRaw(ctx, name, func(raw json.RawMessage) error {
			if err := audit.ObserveWorkspaceAuditDocument(name, raw); err != nil {
				return err
			}
			if _, known := workspaceAuditSlots[name]; !known {
				return nil
			}
			var identity struct {
				Key      string `json:"_key"`
				Revision string `json:"_rev"`
			}
			if err := json.Unmarshal(raw, &identity); err != nil {
				return err
			}
			documents[name+"/"+identity.Key] = workspaceMigrationDocument{name, identity.Key, identity.Revision, append([]byte(nil), raw...)}
			return nil
		})
		if err != nil {
			return plan, err
		}
	}
	if audit.DiscoveryCandidates != 0 {
		return plan, fmt.Errorf("workspace audit found %d unclassified copies; migration refused", audit.DiscoveryCandidates)
	}
	for _, entry := range audit.Entries {
		if !entry.Authoritative || entry.Collection == workspaceProjectionCollection {
			continue
		}
		document := documents[entry.Collection+"/"+entry.Key]
		if entry.Collection == RevisionsCollection && entry.Path == "$.config" {
			revision, err := decode[explorer.Revision](document.raw)
			if err != nil {
				return plan, err
			}
			if len(revision.AuthoringBundle) != 0 {
				if auditNormalizedJSON(revision.Config) != auditNormalizedJSON(revision.AuthoringBundle) {
					return plan, fmt.Errorf("revision %s contains differing workspace packets; migration refused", entry.Key)
				}
				continue
			}
		}
		if target == tableShapeConstructionTarget && entry.TableShapeDocuments == 0 {
			continue
		}
		features := slices.DeleteFunc(slices.Clone(entry.Features), func(feature string) bool { return feature == "legacy-table-shape" })

		if target == workspaceEncodingTarget && entry.SemanticsVersion == authoringv2.CurrentSemanticsVersion && len(features) == 0 && entry.NilConstructionStepLists == 0 && entry.DigestRelation == "matches-decoded-canonical" {
			continue
		}
		if slices.Contains(names, workspaceProjectionCollection) && (entry.Collection == DraftRevisionsCollection || entry.Collection == RevisionsCollection) {
			var source explorer.WorkspaceArtifact
			if entry.Collection == DraftRevisionsCollection {
				history, err := decode[explorer.DraftRevision](document.raw)
				if err != nil {
					return plan, err
				}
				source = explorer.DraftWorkspaceArtifact(history)
			} else {
				revision, err := decode[explorer.Revision](document.raw)
				if err != nil {
					return plan, err
				}
				source = explorer.RevisionWorkspaceArtifact(revision)
			}
			found := false
			err := client.QueryRows(ctx, migrationJSONQuery(`FOR d IN @@c FILTER d._key == @key RETURN d`, true), 1,
				map[string]any{"@c": workspaceProjectionCollection, "key": target.projectionKey(source)}, func(row map[string]any) error {
					projection, err := decodeMigrationJSON[explorer.WorkspaceProjection](row["migrationJSON"])
					if err != nil {
						return err
					}
					err = target.validateProjection(projection, source)
					found = err == nil
					return err
				})
			if err != nil {
				return plan, err
			}
			if found {
				continue
			}
		}
		if entry.Collection == CompilationReceiptsCollection {
			receipt, err := decode[explorer.CompilationReceipt](document.raw)
			if err != nil {
				return plan, err
			}
			if err := validateReceipt(receipt); err != nil {
				return plan, err
			}
			if !retiredReceipts[entry.Key] {
				plan.report.RetiredReceipts++
				retiredReceipts[entry.Key] = true
			}
			continue
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(document.raw, &object); err != nil {
			return plan, err
		}
		field := strings.TrimPrefix(entry.Path, "$.")
		raw := object[field]
		if text := auditString(raw); text != "" {
			raw = json.RawMessage(text)
		}
		token := entry.SnapshotToken
		project, explorerID := auditString(object["project"]), auditString(object["explorerId"])
		generation, scope := "", ""
		switch entry.Collection {
		case ExplorersCollection:
			owner, err := decode[explorer.Explorer](document.raw)
			if err != nil {
				return plan, err
			}
			generation, scope = owner.DraftSourceGeneration, owner.DraftAuthorizationScopeDigest
		case DraftRevisionsCollection:
			history, err := decode[explorer.DraftRevision](document.raw)
			if err != nil {
				return plan, err
			}
			generation, scope = history.SourceGeneration, history.AuthorizationScopeDigest
		case RevisionsCollection, LegacyRepositoryConfigsCollection:
			revisionID := auditString(object["id"])
			if entry.Collection == LegacyRepositoryConfigsCollection {
				revisionID = auditString(object["activeRevisionId"])
			}
			revisionDocument, found := documents[RevisionsCollection+"/"+revisionID]
			if !found {
				return plan, explorer.ErrNotFound
			}
			revision, err := decode[explorer.Revision](revisionDocument.raw)
			if err != nil {
				return plan, err
			}
			receiptDocument, found := documents[CompilationReceiptsCollection+"/"+revision.CompilationReceiptID]
			if !found {
				return plan, explorer.ErrNotFound
			}
			receipt, err := decode[explorer.CompilationReceipt](receiptDocument.raw)
			if err != nil {
				return plan, err
			}
			if receipt.Project != revision.Project || receipt.ExplorerID != revision.ExplorerID {
				return plan, explorer.ErrNotFound
			}
			if err := validateReceipt(receipt); err != nil {
				return plan, err
			}
			if revision.IntentDigest != receipt.IntentDigest || revision.SourceGeneration != receipt.SourceGeneration {
				return plan, fmt.Errorf("revision and receipt source identity mismatch")
			}
			token, project, explorerID = receipt.SnapshotToken, receipt.Project, receipt.ExplorerID
			generation, scope = receipt.SourceGeneration, receipt.AuthorizationScopeDigest
		default:
			return plan, fmt.Errorf("unsupported authoritative workspace slot %s", entry.Collection)
		}
		var catalog authoringv2.CatalogSnapshot
		if entry.NeedsPinnedCatalog {
			if token == "" {
				return plan, fmt.Errorf("%s/%s requires a retained capability token", entry.Collection, entry.Key)
			}
			snapshots, err := NewCapabilitySnapshotStore(client)
			if err != nil {
				return plan, err
			}
			snapshot, err := snapshots.GetByToken(ctx, token)
			if err != nil {
				return plan, err
			}
			if projectid.Canonical(snapshot.Identity.Project) != projectid.Canonical(project) || (generation != "" && snapshot.Identity.Generation != generation) || (scope != "" && snapshot.Identity.AuthorizationScopeDigest != scope) {
				return plan, fmt.Errorf("retained workspace catalog context mismatch")
			}
			catalog = authoringv2.CatalogFromCapability(*snapshot, explorerID)
		}
		migrated, err := target.migrate(raw, catalog)
		if err != nil {
			return plan, fmt.Errorf("migrate %s/%s%s: %w", entry.Collection, entry.Key, entry.Path, err)
		}
		if entry.StoredDigest == "" || !slices.Contains(migrated.AcceptedOriginalDigests, entry.StoredDigest) {
			return plan, fmt.Errorf("stored workspace digest unrecognized for %s/%s%s", entry.Collection, entry.Key, entry.Path)
		}
		if entry.StoredDigest != migrated.MigratedCanonicalDigest {
			plan.report.ChangedDigests++
		}
		switch entry.Collection {
		case ExplorersCollection:
			owner, err := decode[explorer.Explorer](document.raw)
			if err != nil {
				return plan, err
			}
			plan.owners = append(plan.owners, workspaceOwnerMigration{owner, migrated, document.revision})
			plan.report.Owners++
			plan.report.HistoryRows++
			plan.report.Projections++
		case DraftRevisionsCollection:
			history, err := decode[explorer.DraftRevision](document.raw)
			if err != nil {
				return plan, err
			}
			plan.projections = append(plan.projections, newWorkspaceProjection(explorer.DraftWorkspaceArtifact(history), migrated))
			plan.report.Projections++
		case RevisionsCollection:
			revision, err := decode[explorer.Revision](document.raw)
			if err != nil {
				return plan, err
			}
			plan.projections = append(plan.projections, newWorkspaceProjection(explorer.RevisionWorkspaceArtifact(revision), migrated))
			plan.report.Projections++
		case LegacyRepositoryConfigsCollection:
			plan.repositories = append(plan.repositories, workspaceRepositoryMigration{document, migrated})
			plan.report.RepositoryConfigs++
		}
	}
	if slices.Contains(names, workspaceMigrationLedger) {
		recorded, err := migrationVersionRecorded(ctx, client, target.version())
		if err != nil {
			return plan, err
		}
		plan.report.AlreadyApplied = recorded
	}
	return plan, nil
}

func newWorkspaceProjection(source explorer.WorkspaceArtifact, migrated authoringv2.StoredWorkspaceMigration) explorer.WorkspaceProjection {
	projection := explorer.WorkspaceProjection{Source: source, Workspace: append([]byte(nil), migrated.CanonicalJSON...), Digest: migrated.MigratedCanonicalDigest}
	projection.ID = projection.ContentID()
	return projection
}

func insertWorkspaceProjectionFor(ctx context.Context, query store.RowQueryer, projection explorer.WorkspaceProjection, target workspaceMigrationTarget) error {
	if err := target.validateProjection(projection, projection.Source); err != nil {
		return err
	}
	doc, err := migrationDocument(projection, target.projectionKey(projection.Source))
	if err != nil {
		return err
	}
	var stored *explorer.WorkspaceProjection
	if err := query.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS {overwriteMode:"ignore"} RETURN NEW`, 1, map[string]any{"@c": workspaceProjectionCollection, "doc": doc}, func(map[string]any) error { return nil }); err != nil {
		return err
	}
	if err := query.QueryRows(ctx, migrationJSONQuery(`FOR d IN @@c FILTER d._key == @key RETURN d`, true), 1, map[string]any{"@c": workspaceProjectionCollection, "key": target.projectionKey(projection.Source)}, func(row map[string]any) error {
		value, err := decodeMigrationJSON[explorer.WorkspaceProjection](row["migrationJSON"])
		stored = &value
		return err
	}); err != nil {
		return err
	}
	if stored == nil || stored.ID != projection.ID {
		return fmt.Errorf("immutable workspace projection collision")
	}
	return target.validateProjection(*stored, projection.Source)
}

func migrationVersionRecorded(ctx context.Context, query store.RowQueryer, version string) (bool, error) {
	recorded := false
	err := query.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": workspaceMigrationLedger, "key": version}, func(row map[string]any) error {
		if row["version"] != version {
			return fmt.Errorf("workspace migration version record is invalid")
		}
		recorded = true
		return nil
	})
	return recorded, err
}
