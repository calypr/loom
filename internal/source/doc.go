// Package source defines the backend-neutral logical contracts for FHIR
// resources held in a Loom source store. Ingestion builds these values from
// built source documents; backend adapters (for example the PostgreSQL
// source store) persist them and answer reads with their own physical row
// forms. The contracts carry logical identity only: project, dataset
// generation, canonical resource type, logical FHIR id, authorization path,
// and the resource document itself.
//
// Ownership rules this package enforces (witnessed by
// TestSourceResourceOwnershipContract):
//   - It imports only the standard library: no backend packages, no storage
//     drivers, and not even the generic catalog package.
//   - It exposes no physical storage details: no sequence values, no
//     column/JSON encodings chosen by one backend, and no database-only
//     pagination cursors. Storage identity (for example a server-assigned
//     resource sequence) belongs to the storage implementation that assigns
//     it, never to the contract a caller offers.
//   - Logical identity is defined once. Backends embed or copy these types
//     instead of re-declaring the identity fields.
package source
