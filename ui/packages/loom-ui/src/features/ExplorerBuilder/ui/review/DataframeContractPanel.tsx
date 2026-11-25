const lossLabels: Readonly<Record<string, string>> = {
  RELATED_RESOURCE_FIRST_LOSSY: 'Only the first related record is kept. Other records are omitted.',
  RELATED_RESOURCE_ALL_LOSSY: 'Values from related records are flattened. Their association with each record is not retained.',
  FIELD_FIRST_REDUCTION: 'Only the first value in a repeated field is kept. Other values are omitted.',
  DISTINCT_VALUES_REDUCTION: 'Repeated values are deduplicated. Their frequency and order are not retained.',
  AGGREGATE_REDUCTION: 'Records are summarized. Individual input values are not retained.',
  RELATED_LOOKUP_REDUCTION: 'The lookup does not preserve every related record.',
};
export const lossLabel = (reason: string): string => lossLabels[reason] ?? reason;
