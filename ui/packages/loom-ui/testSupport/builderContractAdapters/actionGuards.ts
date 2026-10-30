export function isConstructionProposalAction(action: string | null | undefined): boolean {
  return action === 'proposalEndpoint' || action === 'applyEndpoint' || action === 'apply';
}

export function isRowDefinitionAction(action: string | null | undefined): boolean {
  return action === 'select_row_shape_or_policy' || action === 'apply_row_definition';
}

export function isColumnFeatureAction(action: string | null | undefined): boolean {
  return action === 'aggregate_operation' || action === 'related_field_reduction';
}
