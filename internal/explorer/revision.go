package explorer

import "encoding/json"

func (revision Revision) AuthoringWorkspaceBytes() json.RawMessage {
	if len(revision.AuthoringBundle) != 0 {
		return revision.AuthoringBundle
	}
	return revision.Config
}

func NewRevision(revision Revision) Revision {
	revision.Status = RevisionReady
	return revision
}
