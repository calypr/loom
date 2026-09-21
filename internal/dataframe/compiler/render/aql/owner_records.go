package aql

import (
	"fmt"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderOwnerRecords(expression ir.PhysicalExpression) (string, error) {
	operation := expression.OwnerRecords
	if operation == nil {
		return "", fmt.Errorf("OWNER_RECORDS expression is missing payload")
	}
	correlation := operation.Correlation
	sourceEntries, err := r.renderOwnerRecordSources(correlation.Source)
	if err != nil {
		return "", err
	}
	source := r.newInternalVariable("owner_record_source")
	payload := source + ".payload"
	owners, err := r.renderSelectorArrayFromSource(payload, correlation.OwnerSelector, false, false)
	if err != nil {
		return "", fmt.Errorf("owner records owner selector: %w", err)
	}
	ownerList := r.newInternalVariable("owner_record_owners")
	ordinal := r.newInternalVariable("owner_record_ordinal")
	owner := r.newInternalVariable("owner_record_owner")
	coding := r.newInternalVariable("owner_record_coding")
	codings, err := r.renderSelectorArrayFromSource(owner, correlation.KeySelector, false, false)
	if err != nil {
		return "", fmt.Errorf("owner records coding selector: %w", err)
	}
	system, err := r.renderCorrelationScalar(coding, correlation.SystemSelector)
	if err != nil {
		return "", err
	}
	code, err := r.renderCorrelationScalar(coding, correlation.CodeSelector)
	if err != nil {
		return "", err
	}
	valueSource, err := correlationValueSource(owner, coding, correlation)
	if err != nil {
		return "", err
	}
	values, err := r.renderCorrelationValues(valueSource, correlation.ValueSelector, correlation.ValueFallbacks)
	if err != nil {
		return "", err
	}
	unsupported, err := r.renderCorrelationUnsupportedChoiceValues(valueSource, correlation)
	if err != nil {
		return "", err
	}
	unit := "null"
	if correlation.UnitSelector != nil {
		units, unitErr := r.renderSelectorArrayFromSource(valueSource, *correlation.UnitSelector, false, false)
		if unitErr != nil {
			return "", fmt.Errorf("owner records unit selector: %w", unitErr)
		}
		unit = "FIRST(FLATTEN(" + units + "))"
	}
	matchingCodings := r.newInternalVariable("owner_record_matching_codings")
	flatValues := r.newInternalVariable("owner_record_values")
	unsupportedValues := r.newInternalVariable("owner_record_unsupported_values")
	status := r.newInternalVariable("owner_record_status")
	value := r.newInternalVariable("owner_record_value")
	return fmt.Sprintf(`(
  FOR %s IN %s
    LET %s = FLATTEN(%s)
    FOR %s IN LENGTH(%s) == 0 ? [] : 0..(LENGTH(%s) - 1)
      LET %s = %s[%s]
      LET %s = (
        FOR %s IN FLATTEN(%s)
          LET __owner_record_system = %s
          LET __owner_record_code = %s
          FILTER __owner_record_system == @%s
          FILTER __owner_record_code == @%s
          RETURN %s
      )
      FILTER LENGTH(%s) > 0
      LET %s = FLATTEN(%s)
      LET %s = FLATTEN(%s)
      LET %s = LENGTH(%s) > 0 ? "INVALID_CHOICE_ARM" : LENGTH(%s) > 1 ? "INVALID_MULTIPLE_VALUES" : LENGTH(%s) == 0 ? "ABSENT" : "VALUE"
      LET %s = %s == "VALUE" ? FIRST(%s) : null
      RETURN {
        source: { resourceType: %s.resourceType, resourceId: %s.id, ownerPath: @%s, ownerOrdinal: %s },
        codings: %s,
        choiceArm: @%s,
        logicalType: @%s,
        value: %s,
        values: %s,
        unit: %s,
        status: %s,
        owner: %s
      }
)`, source, sourceEntries, ownerList, owners,
		ordinal, ownerList, ownerList, owner, ownerList, ordinal,
		matchingCodings, coding, codings, system, code, correlation.SystemBindKey, correlation.CodeBindKey, coding,
		matchingCodings, flatValues, values, unsupportedValues, unsupported,
		status, unsupportedValues, flatValues, flatValues, value, status, flatValues,
		source, source, operation.OwnerPathBindKey, ordinal, matchingCodings,
		operation.ChoiceArmBindKey, operation.LogicalTypeBindKey, value, flatValues, unit, status, owner), nil
}

func (r *physicalPlanRenderer) renderOwnerRecordSources(source ir.PhysicalValue) (string, error) {
	raw, err := r.renderValue(source)
	if err != nil {
		return "", err
	}
	if source.Variable != "" && r.setVariables[source.Variable] != "" {
		return raw, nil
	}
	if source.Variable == "" || len(source.Path) != 0 {
		return "", fmt.Errorf("owner records require a resource document or resource set source")
	}
	return "[" + raw + "]", nil
}
