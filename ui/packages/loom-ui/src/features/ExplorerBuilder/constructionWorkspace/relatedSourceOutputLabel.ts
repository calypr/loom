import type { ContributorPredicate } from '../../../types';

export const relatedSourceOutputLabel = (
  resourceType: string,
  fieldPath: string,
  fieldLabel: string,
  form: 'ALL' | 'COUNT' | 'PRESENCE',
  predicate?: ContributorPredicate,
): string => {
  const value = predicate?.value;
  const condition = predicate?.operator === 'EXISTS'
    ? ` with ${fieldPath}`
    : predicate?.operator === 'EQUALS' && value
      ? ` where ${fieldPath} equals ${value.kind === 'CODE' ? value.code.code : value.string}`
      : '';
  const fieldName = fieldLabel.trim() || fieldPath;
  const base = form === 'COUNT'
    ? `Count of related ${resourceType} records`
    : form === 'PRESENCE'
      ? `Has related ${resourceType} record`
      : fieldPath === 'id'
        ? `${resourceType} IDs`
        : fieldName.startsWith(`${resourceType} `) ? fieldName : `${resourceType} ${fieldName}`;
  return `${base}${condition}`;
};
