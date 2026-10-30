import { describe, expect, it } from 'vitest';
import {
  isColumnFeatureAction,
  isConstructionProposalAction,
  isRowDefinitionAction,
} from './actionGuards';

describe('builder adapter action guards', () => {
  it('routes every construction proposal action through the proposal path', () => {
    expect(isConstructionProposalAction('proposalEndpoint')).toBe(true);
    expect(isConstructionProposalAction('applyEndpoint')).toBe(true);
    expect(isConstructionProposalAction('apply')).toBe(true);
    expect(isConstructionProposalAction('choiceEndpoint')).toBe(false);
    expect(isConstructionProposalAction(undefined)).toBe(false);
  });

  it('keeps row-definition selection and apply actions on the row-definition path', () => {
    expect(isRowDefinitionAction('select_row_shape_or_policy')).toBe(true);
    expect(isRowDefinitionAction('apply_row_definition')).toBe(true);
    expect(isRowDefinitionAction('cancel_row_definition')).toBe(false);
    expect(isRowDefinitionAction(null)).toBe(false);
  });

  it('treats aggregate and related-field reduction as column feature actions', () => {
    expect(isColumnFeatureAction('aggregate_operation')).toBe(true);
    expect(isColumnFeatureAction('related_field_reduction')).toBe(true);
    expect(isColumnFeatureAction('projection')).toBe(false);
  });
});
