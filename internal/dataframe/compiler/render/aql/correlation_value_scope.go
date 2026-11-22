package aql

import (
	"fmt"
	fhirbinding "github.com/calypr/loom/internal/fhir/binding"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func correlationValueSource(correlation ir.PhysicalCorrelation, owner, coding string) string {
	if correlation.ValueScope == fhirbinding.CorrelatedValueScopeCoding {
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
