export function classifyNullableSourceScalar({ ownProperty, isNull, value }) {
  if (typeof ownProperty !== 'boolean' || typeof isNull !== 'boolean') {
    throw new TypeError('Raw nullable-scalar evidence must include boolean ownProperty and isNull flags.');
  }
  if (isNull) {
    if (value !== null) throw new TypeError('A null source value must be represented as JSON null.');
    return { state: ownProperty ? 'explicit-null' : 'missing', ownProperty, isNull: true };
  }
  if (!ownProperty || value === null || value === undefined) {
    throw new TypeError('Raw nullable-scalar evidence is internally inconsistent.');
  }
  return { state: 'populated', ownProperty, isNull: false };
}

export function expectedNullableRelatedAll({ relatedCount, sourceState }) {
  if (!Number.isSafeInteger(relatedCount) || relatedCount < 0) {
    throw new RangeError('relatedCount must be a non-negative safe integer.');
  }
  if (relatedCount === 0) return [];
  if (relatedCount !== 1) {
    throw new RangeError('This witness models exactly one linked Patient; supply a source value for every owner to model a larger route.');
  }
  if (sourceState !== 'missing' && sourceState !== 'explicit-null') {
    throw new TypeError('The gender-null ALL mode accepts only a missing or explicit-null source value.');
  }
  // ALL retains one member per linked owner. A missing optional scalar reads as
  // null in the projection, while an empty owner population has no members.
  return [null];
}
