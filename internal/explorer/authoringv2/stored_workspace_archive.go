package authoringv2

import "fmt"

func CanonicalStoredWorkspaceForMigration(workspace Workspace) ([]byte, error) {
	return workspace.canonicalJSON(true)
}

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
