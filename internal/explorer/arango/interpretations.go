package arango

import (
	"context"
	"fmt"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

var _ explorer.InterpretationRepository = (*Store)(nil)

func interpretationLibraryKey(project string, id explorer.InterpretationLibraryID) string {
	return "interpretation_library_" + key(project, string(id))
}

func interpretationRevisionKey(id explorer.InterpretationRevisionID) string {
	return string(id)
}

func (s *Store) ListInterpretationLibraries(ctx context.Context, rawProject string) ([]explorer.InterpretationLibrary, error) {
	project, err := explorer.CanonicalInterpretationProject(rawProject)
	if err != nil {
		return nil, err
	}
	result := make([]explorer.InterpretationLibrary, 0)
	err = s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project SORT d.id ASC RETURN d`, 1000, map[string]any{
		"@c": InterpretationLibrariesCollection, "project": project,
	}, func(row map[string]any) error {
		value, err := decode[explorer.InterpretationLibrary](row)
		if err != nil {
			return fmt.Errorf("decode interpretation library: %w", err)
		}
		if err := validateInterpretationLibrary(value, project); err != nil {
			return err
		}
		result = append(result, value)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Store) GetInterpretationRevision(ctx context.Context, rawProject string, id explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	project, err := explorer.CanonicalInterpretationProject(rawProject)
	if err != nil {
		return nil, err
	}
	if _, err := explorer.NewInterpretationRevisionID(string(id)); err != nil {
		return nil, err
	}
	var out *explorer.InterpretationRevision
	err = s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d.id == @id AND d.project == @project RETURN d`, 1, map[string]any{
		"@c": InterpretationRevisionsCollection, "key": interpretationRevisionKey(id), "id": string(id), "project": project,
	}, func(row map[string]any) error {
		value, err := decode[explorer.InterpretationRevision](row)
		if err != nil {
			return fmt.Errorf("decode interpretation revision: %w", err)
		}
		if err := value.Validate(); err != nil {
			return fmt.Errorf("invalid persisted interpretation revision: %w", err)
		}
		out = &value
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}

func (s *Store) CreateInterpretationRevision(ctx context.Context, input explorer.InterpretationRevision, expectedParent *explorer.InterpretationRevisionID) (*explorer.InterpretationRevision, error) {
	revision, err := explorer.PrepareInterpretationRevision(input)
	if err != nil {
		return nil, err
	}
	project := revision.Project
	expected := ""
	if expectedParent != nil {
		expected = string(*expectedParent)
	}
	parent := ""
	if revision.ParentRevisionID != nil {
		parent = string(*revision.ParentRevisionID)
	}
	if expected != parent {
		return nil, explorer.ErrInterpretationParentConflict
	}

	doc, err := document(revision, interpretationRevisionKey(revision.ID))
	if err != nil {
		return nil, err
	}
	identity, err := revision.RevisionIdentityEnvelope()
	if err != nil {
		return nil, err
	}
	doc["identity"] = identity
	now := revision.CreatedAt
	libraryID := revision.LibraryID
	library := map[string]any{
		"_key": interpretationLibraryKey(project, libraryID),
		"id":   string(libraryID), "project": project,
		"headRevisionId": string(revision.ID), "headDigest": string(revision.ContentDigest),
		"createdAt": now, "updatedAt": now,
	}
	patch := map[string]any{"headRevisionId": string(revision.ID), "headDigest": string(revision.ContentDigest), "updatedAt": now}
	parentKey := ""
	if revision.ParentRevisionID != nil {
		parentKey = interpretationRevisionKey(*revision.ParentRevisionID)
	}

	var response map[string]any
	err = s.client.WithTransaction(ctx, store.TransactionCollections{
		Write: []string{InterpretationLibrariesCollection, InterpretationRevisionsCollection},
	}, func(txCtx context.Context, tx store.RowQueryer) error {
		return tx.QueryRows(txCtx, interpretationCreateAQL, 1, map[string]any{
			"@libraries": InterpretationLibrariesCollection,
			"@revisions": InterpretationRevisionsCollection,
			"project":    project, "libraryId": string(libraryID),
			"libraryKey":  interpretationLibraryKey(project, libraryID),
			"revisionKey": interpretationRevisionKey(revision.ID),
			"parentKey":   parentKey, "parentId": parent,
			"expectedParent": expected, "revisionId": string(revision.ID),
			"parentDigest": func() string {
				if revision.ParentDigest == nil {
					return ""
				}
				return string(*revision.ParentDigest)
			}(),
			"identity": identity, "revision": doc, "library": library, "libraryPatch": patch,
		}, func(row map[string]any) error { response = row; return nil })
	})
	if err != nil {
		return nil, err
	}
	if response == nil {
		return nil, explorer.ErrInterpretationParentConflict
	}
	status, _ := response["status"].(string)
	switch status {
	case "collision":
		return nil, explorer.ErrImmutableInterpretation
	case "parent_conflict":
		return nil, explorer.ErrInterpretationParentConflict
	case "create", "idempotent":
	default:
		return nil, explorer.ErrInterpretationParentConflict
	}
	value := response["existing"]
	if status == "create" {
		value = response["revision"]
	}
	if value == nil {
		return nil, fmt.Errorf("interpretation create returned no revision")
	}
	stored, err := decode[explorer.InterpretationRevision](value)
	if err != nil {
		return nil, fmt.Errorf("decode created interpretation revision: %w", err)
	}
	if err := stored.Validate(); err != nil {
		return nil, fmt.Errorf("created interpretation revision failed identity validation: %w", err)
	}
	return &stored, nil
}

const interpretationCreateAQL = `
LET library = FIRST(
  FOR d IN @@libraries
    FILTER d._key == @libraryKey AND d.project == @project AND d.id == @libraryId
    RETURN d
)
LET existing = FIRST(
  FOR d IN @@revisions
    FILTER d._key == @revisionKey AND d.project == @project AND d.id == @revisionId
    RETURN d
)
LET parent = FIRST(
  FOR d IN @@revisions
    FILTER @parentId != "" AND d._key == @parentKey AND d.project == @project AND d.libraryId == @libraryId
    RETURN d
)
LET currentHead = library == null ? "" : NOT_NULL(library.headRevisionId, "")
LET headMatches = currentHead == @expectedParent AND (library != null OR @expectedParent == "")
LET parentMatches = @parentId == "" OR (parent != null AND parent.contentDigest == @parentDigest)
LET status = existing != null ? (existing.identity == @identity ? "idempotent" : "collision") : (headMatches AND parentMatches ? "create" : "parent_conflict")
LET inserted = (
  FOR marker IN [1]
    FILTER status == "create"
    INSERT @revision INTO @@revisions OPTIONS { overwriteMode: "ignore" }
    RETURN NEW
)
LET advanced = (
  FOR marker IN [1]
    FILTER status == "create"
    UPSERT { _key: @libraryKey }
      INSERT @library
      UPDATE MERGE(OLD, @libraryPatch) IN @@libraries
    RETURN NEW
)
RETURN {status: status, existing: existing, revision: FIRST(inserted), library: FIRST(advanced)}`

func validateInterpretationLibrary(value explorer.InterpretationLibrary, project string) error {
	if value.Project != project {
		return fmt.Errorf("interpretation library project is not canonical")
	}
	if _, err := explorer.NewInterpretationLibraryID(string(value.ID)); err != nil {
		return err
	}
	if value.HeadRevisionID != "" {
		if _, err := explorer.NewInterpretationRevisionID(string(value.HeadRevisionID)); err != nil {
			return err
		}
		if _, err := explorer.NewInterpretationContentDigest(string(value.HeadDigest)); err != nil {
			return err
		}
	}
	return nil
}
