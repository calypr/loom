// Package semantic converts validated dataframe requests and generated FHIR
// shape metadata into backend-neutral logical plans and researcher-facing
// concepts without choosing storage routes or emitting AQL. Concept rules use
// extensible string IDs and retain raw selector provenance in trace metadata.
package semantic
