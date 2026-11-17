package authoringv2

import "fmt"

// CanonicalStoredWorkspaceForMigration is the offline canonical writer for
// historical receipt verification and explicit storage migrations. Runtime
// consumers must use Workspace.CanonicalJSON, which rejects retired intent.
func CanonicalStoredWorkspaceForMigration(workspace Workspace) ([]byte, error) {
	return workspace.canonicalJSON(true)
}

// VerifyStoredWorkspaceForMigration authenticates historical canonical identity
// without activating retired operations. The returned archive is not executable.
func VerifyStoredWorkspaceForMigration(raw []byte, expectedDigest string) (Workspace, error) {
	workspace, err := DecodeStoredWorkspaceForMigration(raw)
	if err != nil {
		return Workspace{}, err
	}
	canonical, err := CanonicalStoredWorkspaceForMigration(workspace)
	if err != nil {
		return Workspace{}, err
	}
	digest := workspaceDigest(canonical)
	if expectedDigest != digest {
		legacy, err := workspace.LegacyNilConstructionStepsDigest()
		if err != nil || legacy == "" || expectedDigest != legacy {
			return Workspace{}, fmt.Errorf("stored workspace canonical digest mismatch")
		}
	}
	return workspace, nil
}

func workspaceHasArchivedShape(workspace Workspace) bool {
	for _, document := range workspace.Documents {
		if document.ArchiveRetiredShape != nil {
			return true
		}
	}
	return false
}
