package authoringv2

type CatalogAvailabilitySourceReadiness struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

type CatalogAvailabilityReadiness struct {
	Status            string                             `json:"status"`
	Reason            string                             `json:"reason,omitempty"`
	ResourceInventory CatalogAvailabilitySourceReadiness `json:"resourceInventory"`
	Relationships     CatalogAvailabilitySourceReadiness `json:"relationships"`
	FieldProfiles     CatalogAvailabilitySourceReadiness `json:"fieldProfiles"`
}

type CatalogAvailability struct {
	Readiness     CatalogAvailabilityReadiness  `json:"readiness"`
	Resources     []CatalogObservedResource     `json:"resources"`
	Fields        []CatalogObservedField        `json:"fields"`
	Relationships []CatalogObservedRelationship `json:"relationships"`
}

type CatalogObservedResource struct {
	ResourceType  string `json:"resourceType"`
	DocumentCount int64  `json:"documentCount"`
	Populated     bool   `json:"populated"`
}

type CatalogObservedField struct {
	ResourceType              string             `json:"resourceType"`
	Path                      string             `json:"path"`
	ObservedShapeKind         string             `json:"observedShapeKind"`
	PathObservedDocumentCount int64              `json:"pathObservedDocumentCount"`
	Examples                  []string           `json:"examples"`
	ExamplesTruncated         bool               `json:"examplesTruncated"`
	SemanticObservations      []ConceptCandidate `json:"semanticObservations"`
}

type CatalogObservedRelationship struct {
	SourceResourceType string `json:"sourceResourceType"`
	TargetResourceType string `json:"targetResourceType"`
	Label              string `json:"label"`
	StorageDirection   string `json:"storageDirection"`
	BuilderDirection   string `json:"builderDirection"`
	ObservedEdgeCount  int64  `json:"observedEdgeCount"`
}
