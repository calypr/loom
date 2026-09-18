package aql

import (
	"fmt"
	"strings"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func (r *physicalPlanRenderer) renderCellTraceReturn(terminal ir.PhysicalCellTraceReturn) ([]string, error) {
	value, err := r.renderExpression(terminal.Value)
	if err != nil {
		return nil, fmt.Errorf("trace value: %w", err)
	}
	contributors, statusCandidates, lossy, omission, err := r.renderTraceContributors(terminal)
	if err != nil {
		return nil, err
	}
	if terminal.OmissionCode != "" {
		omission = terminal.OmissionCode
	}
	identityField := ir.PhysicalCellTraceIdentityPartsField
	identityValue := ""
	if terminal.ExplicitIdentity != nil {
		identityField = ir.PhysicalCellTraceExplicitIdentityField
		identityValue, err = r.renderExpression(*terminal.ExplicitIdentity)
		if err != nil {
			return nil, fmt.Errorf("trace explicit identity: %w", err)
		}
	} else {
		parts := make([]string, 0, len(terminal.IdentityParts))
		for _, part := range terminal.IdentityParts {
			rendered, partErr := r.renderExpression(part.Expression)
			if partErr != nil {
				return nil, fmt.Errorf("trace identity part %q: %w", part.Name, partErr)
			}
			parts = append(parts, rendered)
		}
		identityValue = "[" + strings.Join(parts, ", ") + "]"
	}
	name := func(prefix, value string) string {
		key := r.newInternalBindKey(prefix)
		r.bindVars[key] = value
		return "@" + key
	}
	valueName := name("trace_value_name", ir.PhysicalCellTraceValueField)
	contributionsName := name("trace_contributions_name", ir.PhysicalCellTraceContributionsField)
	identityName := name("trace_identity_name", identityField)
	statusName := name("trace_status_name", ir.PhysicalCellTraceStatusField)
	hasMoreName := name("trace_has_more_name", ir.PhysicalCellTraceHasMoreField)
	omissionName := name("trace_omission_name", ir.PhysicalCellTraceOmissionField)
	statusVariable := r.newInternalVariable("trace_status_candidates")
	pageVariable := r.newInternalVariable("trace_contribution_page")
	status := fmt.Sprintf(`LENGTH(%s) == 0 ? "NO_MATCH" : %s == null ? "RECORDED_NULL" : "VALUE"`, statusVariable, value)
	if lossy {
		status = fmt.Sprintf(`LENGTH(%s) == 0 ? "NO_MATCH" : %s == null ? "RECORDED_NULL" : LENGTH(%s) > 1 ? "AMBIGUOUS" : "VALUE"`, statusVariable, value, statusVariable)
	}
	return []string{
		"LET " + statusVariable + " = " + statusCandidates,
		"LET " + pageVariable + " = " + contributors,
		fmt.Sprintf("RETURN { [%s]: %s, [%s]: SLICE(%s, 0, @%s), [%s]: %s, [%s]: %s, [%s]: LENGTH(%s) > @%s, [%s]: %q }", valueName, value, contributionsName, pageVariable, terminal.LimitBindKey, identityName, identityValue, statusName, status, hasMoreName, pageVariable, terminal.LimitBindKey, omissionName, omission),
	}, nil
}

func (r *physicalPlanRenderer) renderTraceContributors(terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	if terminal.Contribution != nil {
		return r.renderReducedSetTraceContributors(*terminal.Contribution, terminal)
	}
	expression := terminal.Value
	switch expression.Kind {
	case ir.PhysicalExtractExpression:
		if expression.Extract == nil || expression.Extract.Prepared != nil {
			return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
		}
		items := "[" + expression.Extract.Source.Variable + "]"
		if r.setVariables[expression.Extract.Source.Variable] != "" {
			items = expression.Extract.Source.Variable
		}
		return r.renderTraceContributorQueries(items, expression, terminal, expression.Cardinality == ir.PhysicalScalarCardinality)
	case ir.PhysicalAggregateExpression:
		aggregate := expression.Aggregate
		if aggregate == nil || aggregate.Temporal != nil {
			return "[]", "[]", false, "TRACE_TEMPORAL_CONTRIBUTORS_OMITTED", nil
		}
		items, _, itemsErr := r.renderAggregateItems(aggregate)
		if itemsErr != nil {
			return "", "", false, "", itemsErr
		}
		if aggregate.Value == nil {
			return r.renderTraceContributorQueries(items, ir.PhysicalExpression{}, terminal, false)
		}
		lossy := aggregate.Operation == ir.PhysicalFirstAggregate || aggregate.Operation == ir.PhysicalRequireOneAggregate
		return r.renderTraceContributorQueries(items, *aggregate.Value, terminal, lossy)
	default:
		return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
	}
}

func (r *physicalPlanRenderer) renderReducedSetTraceContributors(contribution ir.PhysicalCellTraceContribution, terminal ir.PhysicalCellTraceReturn) (page, status string, lossy bool, omission string, err error) {
	item := r.newInternalVariable("trace_contributor")
	value := r.newInternalVariable("trace_contributor_value")
	values := fmt.Sprintf("(%s.%s == null ? [null] : TO_ARRAY(%s.%s))", item, contribution.ValueField, item, contribution.ValueField)
	record := fmt.Sprintf(`{ resourceType: %s.resourceType, resourceId: %s.id, value: %s }`, item, item, value)
	page = fmt.Sprintf("(FOR %s IN %s FOR %s IN %s LIMIT @%s, @%s RETURN %s)", item, contribution.SetVariable, value, values, terminal.OffsetBindKey, terminal.FetchLimitBindKey, record)
	status = fmt.Sprintf("(FOR %s IN %s FOR %s IN %s LIMIT 2 RETURN %s)", item, contribution.SetVariable, value, values, record)
	return page, status, contribution.Lossy, "", nil
}

func (r *physicalPlanRenderer) renderTraceContributorQueries(items string, valueExpression ir.PhysicalExpression, terminal ir.PhysicalCellTraceReturn, lossy bool) (page, status string, resultLossy bool, omission string, err error) {
	item := r.newInternalVariable("trace_contributor")
	value := "null"
	if valueExpression.Kind != "" {
		if valueExpression.Kind != ir.PhysicalExtractExpression || valueExpression.Extract == nil {
			return "[]", "[]", false, "TRACE_CONTRIBUTORS_UNAVAILABLE", nil
		}
		value, err = r.renderAggregateItemValue(valueExpression, item)
		if err != nil {
			return "", "", false, "", err
		}
	}
	record := fmt.Sprintf(`{ resourceType: %s.resourceType, resourceId: %s.id, value: %s }`, item, item, value)
	page = fmt.Sprintf("(FOR %s IN %s LIMIT @%s, @%s RETURN %s)", item, items, terminal.OffsetBindKey, terminal.FetchLimitBindKey, record)
	status = fmt.Sprintf("(FOR %s IN %s LIMIT 2 RETURN %s)", item, items, record)
	return page, status, lossy, "", nil
}
