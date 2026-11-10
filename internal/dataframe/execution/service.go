package execution

// ServiceConfig wires the one source row port the service may invoke.
// SourceRows accepts only a parent-tagged command.CompiledCommand carried by
// RowsRequest; there is no loose query-text entry point.
type ServiceConfig struct {
	SourceRows SourceRows
}

type Service struct {
	sourceRows SourceRows
}

func NewService(cfg ServiceConfig) *Service {
	return &Service{sourceRows: cfg.SourceRows}
}
