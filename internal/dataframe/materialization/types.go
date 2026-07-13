package materialization

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/authscope"
)

type State string

const (
	StatePending State = "PENDING"
	StateLoading State = "LOADING"
	StateReady   State = "READY"
	StateFailed  State = "FAILED"
)

type Column struct {
	Name       string `json:"name"`
	ClickHouse string `json:"clickhouseType"`
}

// SchemaColumn is the explicit output contract for a published dataframe.
// ClickHouse types are intentionally kept as strings so the contract can use
// native types such as Nullable(String), Array(Int64), and DateTime64.
type SchemaColumn = Column

type Materialization struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Project           string                  `json:"project"`
	DatasetGeneration string                  `json:"datasetGeneration"`
	State             State                   `json:"state"`
	AuthScopeMode     authscope.ReadScopeMode `json:"authScopeMode"`
	AuthResourcePaths []string                `json:"authResourcePaths,omitempty"`
	Columns           []Column                `json:"columns"`
	PhysicalTable     string                  `json:"physicalTable"`
	RowCount          int64                   `json:"rowCount"`
	CreatedAt         time.Time               `json:"createdAt"`
	ReadyAt           *time.Time              `json:"readyAt,omitempty"`
	Error             string                  `json:"error,omitempty"`
}

type Registry interface {
	Save(context.Context, Materialization) error
	Get(context.Context, string) (Materialization, error)
	ListReady(context.Context, string) ([]Materialization, error)
}
