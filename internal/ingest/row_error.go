package ingest

import (
	"errors"
	"fmt"

	fhir "github.com/calypr/loom/generated/fhir"
)

// RowError locates a rejected source row without including its payload in
// the public diagnostic. The original validation cause remains unwrap-able.
type RowError struct {
	File         string
	Row          int
	ResourceType string
	Category     string
	Cause        error
}

func (e *RowError) Error() string {
	return fmt.Sprintf("%s %s row %d: %v", e.File, e.ResourceType, e.Row, e.Cause)
}

func (e *RowError) Unwrap() error { return e.Cause }

func (e *RowError) PublicMessage() string {
	if e.Category != "validation" {
		return fmt.Sprintf("Loom could not process %s row %d during %s for resource type %s.",
			e.File, e.Row, e.Category, e.ResourceType)
	}
	var missing *fhir.MissingRequiredFieldError
	if errors.As(e.Cause, &missing) {
		return fmt.Sprintf("%s row %d: required field %q is missing from %s.",
			e.File, e.Row, missing.Field, e.ResourceType)
	}
	return fmt.Sprintf("%s row %d failed %s for resource type %s. Check this row against the configured FHIR schema.",
		e.File, e.Row, e.Category, e.ResourceType)
}
