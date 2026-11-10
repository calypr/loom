package execution

// ServiceConfig wires the single AQL row-stream adapter the service may
// invoke. QueryRows accepts only a parent-tagged compiler.CompiledCommand;
// there is no loose query-text entry point.
type ServiceConfig struct {
	QueryRows QueryRows
}

type Service struct {
	queryRows QueryRows
}

func NewService(cfg ServiceConfig) *Service {
	return &Service{queryRows: cfg.QueryRows}
}
