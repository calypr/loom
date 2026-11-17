package authoringv2

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// StoredWorkspaceMigration is an offline migration result. The raw-input digest
// identifies the bytes read from storage; the original canonical digests identify
// the workspace representation understood by the old receipt verifier; and the
// migrated digest identifies the bytes safe to persist as the current workspace.
// A changed migrated digest invalidates receipts bound to the old canonical digest.
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

// MigrateStoredWorkspace decodes historical persisted workspace shapes and
// applies all deterministic compilation migrations using the caller's pinned
// catalog. This is an offline-only entrypoint for an explicit storage migration;
// request decoding must continue to use DecodeWorkspace's strict current contract.
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
	for _, digest := range []string{result.OriginalCanonicalDigest, result.OriginalLegacyNilStepsDigest} {
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
	// Decode the canonical bytes into an owned value. The returned workspace and
	// bytes must not alias caller-owned raw data or migration intermediates.
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

// DecodeStoredWorkspaceForMigration is deliberately separate from the runtime
// DecodeWorkspace API so removing its persisted-draft compatibility fallback
// cannot disable the one-time storage migration. The fallback accepts only the
// repository's known historical persisted shapes and still applies current
// workspace validation after version migrations.
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
