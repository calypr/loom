package recipe

type RootColumnNaming string

const (
	RootColumnNamingPrefixed RootColumnNaming = "PREFIXED"
	RootColumnNamingExact    RootColumnNaming = "EXACT"
)

func (n RootColumnNaming) Valid() bool {
	return n == "" || n == RootColumnNamingPrefixed || n == RootColumnNamingExact
}

func (n RootColumnNaming) Normalized() RootColumnNaming {
	if n == "" {
		return RootColumnNamingPrefixed
	}
	return n
}

type TraversalColumnNaming string

const (
	TraversalColumnNamingPath  TraversalColumnNaming = "PATH"
	TraversalColumnNamingAlias TraversalColumnNaming = "ALIAS"
	TraversalColumnNamingExact TraversalColumnNaming = "EXACT"
)

func (n TraversalColumnNaming) Valid() bool {
	return n == "" || n == TraversalColumnNamingPath || n == TraversalColumnNamingAlias || n == TraversalColumnNamingExact
}

func (n TraversalColumnNaming) Normalized() TraversalColumnNaming {
	if n == "" {
		return TraversalColumnNamingPath
	}
	return n
}

type Field struct {
	Name string `json:"name"`

	ColumnID   string       `json:"columnId,omitempty"`
	Label      string       `json:"label,omitempty"`
	FieldRef   string       `json:"fieldRef,omitempty"`
	Expr       Expression   `json:"expr"`
	Fallbacks  []Expression `json:"fallbacks,omitempty"`
	ValueMode  ValueMode    `json:"valueMode,omitempty"`
	Discovered bool         `json:"-"`

	SourceKey string `json:"-"`
}

type ValueMode string

const (
	ValueModeAuto     ValueMode = "AUTO"
	ValueModeFirst    ValueMode = "FIRST"
	ValueModeAll      ValueMode = "ALL"
	ValueModeDistinct ValueMode = "DISTINCT"
)

func (m ValueMode) Valid() bool {
	return m == "" || m == ValueModeAuto || m == ValueModeFirst || m == ValueModeAll || m == ValueModeDistinct
}

func (m ValueMode) Normalized() ValueMode {
	if m == "" {
		return ValueModeAuto
	}
	return m
}
