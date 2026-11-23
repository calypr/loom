package workspace

import "fmt"

func NewWorkspace(title string) Workspace {
	return Workspace{APIVersion: APIVersion, Kind: WorkspaceKind, SemanticsVersion: CurrentSemanticsVersion,
		Explorer: ExplorerMetadata{Title: title}, Documents: []Document{}, Tabs: []Tab{}}
}

func DecodeCurrentWorkspace(raw []byte) (Workspace, error) {
	var workspace Workspace
	if err := strictDecode(raw, &workspace); err != nil {
		return workspace, err
	}
	if workspace.SemanticsVersion != CurrentSemanticsVersion {
		return workspace, fmt.Errorf("workspace requires stored-data migration to semantics version %d", CurrentSemanticsVersion)
	}
	if err := workspace.Validate(); err != nil {
		return workspace, err
	}
	for index := range workspace.Documents {
		document := &workspace.Documents[index]
		if document.Construction != nil && document.Construction.Steps == nil {
			return workspace, fmt.Errorf("documents[%d].construction.steps requires stored-data migration", index)
		}
		if document.Columns == nil {
			document.Columns = []Column{}
		}
	}
	return workspace, nil
}

func CloneCurrentWorkspace(workspace Workspace) (Workspace, error) {
	if workspaceHasArchivedShape(workspace) {
		return Workspace{}, fmt.Errorf("tableShape requires explicit stored-data migration")
	}
	if workspace.SemanticsVersion != CurrentSemanticsVersion {
		return Workspace{}, fmt.Errorf("workspace requires stored-data migration")
	}
	if workspaceHasLegacyContributors(workspace) {
		return Workspace{}, fmt.Errorf("legacy aggregate contributors require stored-data migration")
	}
	return CloneWorkspace(workspace)
}

func PrepareWorkspaceForCompilation(workspace Workspace, _ CatalogSnapshot) (Workspace, error) {
	prepared, err := CloneCurrentWorkspace(workspace)
	if err != nil {
		return Workspace{}, err
	}
	return prepared.NormalizePresentationOrders(), nil
}

func workspaceHasLegacyContributors(workspace Workspace) bool {
	for _, document := range workspace.Documents {
		for _, column := range document.Columns {
			if column.Source.Kind == SourceAggregate && column.Source.Aggregate != nil && column.Source.Aggregate.Where != nil {
				return true
			}
		}
	}
	return false
}
