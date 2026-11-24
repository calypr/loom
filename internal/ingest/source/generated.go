package source

import fhir "github.com/calypr/loom/fhir/model"

type GeneratedLoadResource interface {
	fhir.ConcreteResource
	Validate() error
	ExtractEdgeDocuments(string) ([]fhir.EdgeDocument, error)
}
