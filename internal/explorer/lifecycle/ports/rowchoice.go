package ports

import (
	"context"

	"github.com/calypr/loom/internal/explorer/capability"
	ws "github.com/calypr/loom/internal/explorer/workspace"
)

type RowChoiceKind string

const (
	RowChoiceFieldGroup RowChoiceKind = "FIELD_GROUP"
	RowChoiceExpanded   RowChoiceKind = "EXPANDED"
)

type RowChoiceResolveRequest struct {
	Project      string
	ExplorerID   string
	OutputID     string
	Snapshot     capability.Snapshot
	Route        ws.RouteNode
	RowChoiceID  string
	ExpectedKind RowChoiceKind
}

type ResolvedRowChoice struct {
	Kind         RowChoiceKind
	OccurrenceID string
	FieldPath    string
	ScopePath    string
}

type RowChoiceResolver interface {
	ResolveRowChoiceID(context.Context, RowChoiceResolveRequest) (ResolvedRowChoice, error)
}

type RowChoicePlanner interface {
	ListRowChoices(context.Context, capability.Snapshot, ws.Document) ([]capability.RowChoice, error)
}
