package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func correlationValueSource(correlation ir.PhysicalCorrelation, owner, coding string) string {
	if correlation.ValueScope == fhirschema.CorrelatedValueScopeCoding {
		return coding
	}
	return owner
}

func (r *physicalPlanRenderer) renderCorrelationCodingItems(owner string, correlation ir.PhysicalCorrelation) (string, error) {
	codings, err := r.renderSelectorArrayFromSource(owner, correlation.KeySelector, false, false)
	if err != nil {
		return "", err
	}
	if correlation.KeyIsArray {
		return "FLATTEN(" + codings + ")", nil
	}
	return codings, nil
}

func (r *physicalPlanRenderer) renderCorrelationVersionClause(correlation ir.PhysicalCorrelation, coding, indent string) (string, error) {
	if correlation.VersionSelector == nil {
		return "", nil
	}
	version, err := r.renderCorrelationScalar(coding, *correlation.VersionSelector)
	if err != nil {
		return "", fmt.Errorf("correlation version selector: %w", err)
	}
	return fmt.Sprintf("\n%sLET __correlation_version = NOT_NULL(%s, \"\")\n%sFILTER __correlation_version == @%s", indent, version, indent, correlation.VersionBindKey), nil
}
