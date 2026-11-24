package interpretation

import "time"

type ConfiguredColumnContextRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
}

type InterpretationRevisionSummary struct {
	ID            string    `json:"id"`
	LibraryID     string    `json:"libraryId"`
	ContentDigest string    `json:"contentDigest"`
	Author        string    `json:"author"`
	Explanation   string    `json:"explanation"`
	CreatedAt     time.Time `json:"createdAt"`
}

type InterpretationLibrarySummary struct {
	ID             string                         `json:"id"`
	HeadRevisionID string                         `json:"headRevisionId,omitempty"`
	HeadDigest     string                         `json:"headDigest,omitempty"`
	Head           *InterpretationRevisionSummary `json:"head,omitempty"`
	UpdatedAt      time.Time                      `json:"updatedAt"`
}
