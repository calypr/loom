package execution

type ServiceConfig struct {
	SourceRows SourceRows
}

type Service struct {
	sourceRows SourceRows
}

func NewService(cfg ServiceConfig) *Service {
	return &Service{sourceRows: cfg.SourceRows}
}
