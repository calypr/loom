package explorer

// ProjectCatalogSettings stores project publication catalog anchors. The persisted
// document also retains legacy default fields for compatibility with existing records.
type ProjectCatalogSettings struct {
	Project         string            `json:"project"`
	Version         int64             `json:"version"`
	DefaultConfigID string            `json:"defaultConfigId,omitempty"`
	DefaultFrameID  string            `json:"defaultFrameId,omitempty"`
	Catalogs        map[string]string `json:"catalogs"`
}
