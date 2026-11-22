package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

type StoredWorkspaceMigration struct {
	Workspace                        Workspace
	CanonicalJSON                    []byte
	AcceptedOriginalDigests          []string
	InputSHA256                      string
	OriginalCanonicalDigest          string
	OriginalLegacyNilStepsDigest     string
	MigratedCanonicalDigest          string
	OriginalNilConstructionStepLists int
	DigestChanged                    bool
}

func MigrateStoredWorkspace(raw []byte, catalog CatalogSnapshot) (StoredWorkspaceMigration, error) {
	var result StoredWorkspaceMigration
	inputSum := sha256.Sum256(raw)
	result.InputSHA256 = "sha256:" + hex.EncodeToString(inputSum[:])

	original, err := DecodeStoredWorkspaceForMigration(raw)
	if err != nil {
		return result, fmt.Errorf("decode stored workspace for migration: %w", err)
	}
	for _, document := range original.Documents {
		if document.Construction != nil && document.Construction.Steps == nil {
			result.OriginalNilConstructionStepLists++
		}
	}
	if originalCanonical, canonicalErr := CanonicalStoredWorkspaceForMigration(original); canonicalErr == nil {
		result.OriginalCanonicalDigest = workspaceDigest(originalCanonical)
	}
	if legacyDigest, digestErr := original.LegacyNilConstructionStepsDigest(); digestErr == nil {
		result.OriginalLegacyNilStepsDigest = legacyDigest
	}
	legacySourceDigest, err := legacyImportedSourceDigest(raw, original)
	if err != nil {
		return result, fmt.Errorf("verify legacy imported source encoding: %w", err)
	}
	for _, digest := range []string{result.OriginalCanonicalDigest, result.OriginalLegacyNilStepsDigest, legacySourceDigest} {
		if digest != "" && !contains(result.AcceptedOriginalDigests, digest) {
			result.AcceptedOriginalDigests = append(result.AcceptedOriginalDigests, digest)
		}
	}

	migrated, err := MigrateWorkspaceForCompilation(original, catalog)
	if err != nil {
		return result, fmt.Errorf("migrate stored workspace: %w", err)
	}
	migrated.SemanticsVersion = CurrentSemanticsVersion
	canonical, err := CanonicalStoredWorkspaceForMigration(migrated)
	if err != nil {
		return result, fmt.Errorf("canonicalize migrated workspace: %w", err)
	}

	var detached Workspace
	if err := json.Unmarshal(canonical, &detached); err != nil {
		return result, fmt.Errorf("decode migrated canonical workspace: %w", err)
	}
	result.Workspace = detached
	result.CanonicalJSON = append([]byte(nil), canonical...)
	result.MigratedCanonicalDigest = workspaceDigest(canonical)
	for _, originalDigest := range result.AcceptedOriginalDigests {
		if originalDigest != result.MigratedCanonicalDigest {
			result.DigestChanged = true
		}
	}
	return result, nil
}

func DecodeStoredWorkspaceForMigration(raw []byte) (Workspace, error) {
	var workspace Workspace
	if err := strictDecode(raw, &workspace); err == nil {
		workspace = MigrateAggregateTemporalPolicy(workspace)
		workspace = migrateMissingRowsBeforeCurrent(workspace)
	} else {
		legacy, legacyErr := decodePersistedLegacyWorkspace(raw)
		if legacyErr != nil {
			return Workspace{}, err
		}
		workspace = MigrateAggregateTemporalPolicy(legacy)
		workspace = migrateMissingRowsBeforeCurrent(workspace)
	}
	if err := workspace.validate(true); err != nil {
		return workspace, err
	}
	for i := range workspace.Documents {
		if workspace.Documents[i].Columns == nil {
			workspace.Documents[i].Columns = []Column{}
		}
	}
	return workspace, nil
}

func workspaceDigest(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}
