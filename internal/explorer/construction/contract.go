package construction

import (
	"github.com/calypr/loom/internal/explorer/capability"
	ws "github.com/calypr/loom/internal/explorer/workspace"
	"github.com/calypr/loom/internal/semantic"
)

type ConstructionChoiceSearchSource struct {
	Kind          capability.ConstructionChoiceSourceKind `json:"kind"`
	CandidateID   string                                  `json:"candidateId,omitempty"`
	ContextToken  string                                  `json:"contextToken,omitempty"`
	BuildID       string                                  `json:"buildId,omitempty"`
	ConceptID     string                                  `json:"conceptId,omitempty"`
	BindingID     string                                  `json:"bindingId,omitempty"`
	ChoiceID      string                                  `json:"choiceId,omitempty"`
	SelectionMode semantic.SelectionMode                  `json:"selectionMode,omitempty"`
}

type ConstructionChoiceSearchRequest struct {
	Project       string
	ExplorerID    string
	SnapshotToken string
	OutputID      string
	OccurrenceID  string
	Source        ConstructionChoiceSearchSource
	Limit         int
	Cursor        string
}

type ConstructionChoiceSearchResponse struct {
	SnapshotToken string                          `json:"snapshotToken"`
	OutputID      string                          `json:"outputId"`
	Complete      bool                            `json:"complete"`
	Truncated     bool                            `json:"truncated"`
	NextCursor    string                          `json:"nextCursor,omitempty"`
	Choices       []capability.ConstructionChoice `json:"choices"`
}

type RelatedExpandChoiceSearchRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	AnchorColumnID       string
	TargetResourceType   string
	Limit                int
	Cursor               string
	SavedGraphOnly       bool
}

type RelatedExpandRouteChoice struct {
	ChoiceID           string                             `json:"choiceId"`
	AnchorColumnID     string                             `json:"anchorColumnId"`
	AnchorKind         string                             `json:"kind"`
	NodeID             string                             `json:"nodeId"`
	ResourceType       string                             `json:"resourceType"`
	AnchorLabel        string                             `json:"label"`
	TargetNodeID       string                             `json:"targetNodeId"`
	TargetResourceType string                             `json:"targetResourceType"`
	Route              []capability.ConstructionRouteStep `json:"route"`
}

type RelatedExpandChoiceSearchResponse struct {
	SnapshotToken string                     `json:"snapshotToken"`
	DraftVersion  int64                      `json:"draftVersion"`
	DraftDigest   string                     `json:"draftDigest"`
	OutputID      string                     `json:"outputId"`
	StageID       string                     `json:"stageId"`
	Complete      bool                       `json:"complete"`
	Truncated     bool                       `json:"truncated"`
	NextCursor    string                     `json:"nextCursor,omitempty"`
	Choices       []RelatedExpandRouteChoice `json:"choices"`
}

type RelatedFieldChoiceSearchRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	Query                string
	Limit                int
	Cursor               string
}

type RelatedFieldChoice struct {
	ChoiceID string                            `json:"choiceId"`
	Label    string                            `json:"label"`
	Source   ws.ConstructionRelatedFieldSource `json:"source"`
}

type RelatedFieldChoiceSearchResponse struct {
	SnapshotToken string               `json:"snapshotToken"`
	DraftVersion  int64                `json:"draftVersion"`
	DraftDigest   string               `json:"draftDigest"`
	OutputID      string               `json:"outputId"`
	StageID       string               `json:"stageId"`
	Complete      bool                 `json:"complete"`
	Truncated     bool                 `json:"truncated"`
	NextCursor    string               `json:"nextCursor,omitempty"`
	Choices       []RelatedFieldChoice `json:"choices"`
}

type RelatedExpandContributorChoiceSearchRequest struct {
	Project              string
	ExplorerID           string
	SnapshotToken        string
	ExpectedDraftVersion int64
	ExpectedDraftDigest  string
	OutputID             string
	StageID              string
	RouteChoiceID        string
	Query                string
	Limit                int
	Cursor               string
}

type RelatedExpandContributorChoice struct {
	ChoiceID             string                            `json:"choiceId"`
	Source               ws.ConstructionRelatedFieldSource `json:"source"`
	Label                string                            `json:"label"`
	Operators            []string                          `json:"operators"`
	SuggestedValues      []string                          `json:"suggestedValues"`
	SuggestionsComplete  bool                              `json:"suggestionsComplete"`
	SuggestionsTruncated bool                              `json:"suggestionsTruncated"`
	SuggestionsSource    string                            `json:"suggestionsSource"`
}

type RelatedExpandContributorChoiceSearchResponse struct {
	SnapshotToken string                           `json:"snapshotToken"`
	DraftVersion  int64                            `json:"draftVersion"`
	DraftDigest   string                           `json:"draftDigest"`
	OutputID      string                           `json:"outputId"`
	StageID       string                           `json:"stageId"`
	RouteChoiceID string                           `json:"routeChoiceId"`
	Complete      bool                             `json:"complete"`
	Truncated     bool                             `json:"truncated"`
	NextCursor    string                           `json:"nextCursor,omitempty"`
	Choices       []RelatedExpandContributorChoice `json:"choices"`
}

type relatedExpandContributorChoiceCursor struct {
	Version      int    `json:"version"`
	Snapshot     string `json:"snapshot"`
	DraftVersion int64  `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
	OutputID     string `json:"outputId"`
	StageID      string `json:"stageId"`
	RouteChoice  string `json:"routeChoice"`
	Query        string `json:"query"`
	Offset       int    `json:"offset"`
}

type relatedFieldChoiceCursor struct {
	Version      int    `json:"version"`
	Snapshot     string `json:"snapshot"`
	DraftVersion int64  `json:"draftVersion"`
	DraftDigest  string `json:"draftDigest"`
	OutputID     string `json:"outputId"`
	StageID      string `json:"stageId"`
	Query        string `json:"query"`
	Offset       int    `json:"offset"`
}
