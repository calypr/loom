import assert from 'node:assert/strict';

const compareText = (left, right) => String(left).localeCompare(String(right));

export function expectedVisibleListText(values) {
  assert(Array.isArray(values), 'Expected preview list value must be an array');
  const visible = values.filter(value => value !== null && value !== undefined);
  assert(visible.every(value => typeof value === 'string'), 'Expected preview list items must be strings or null');
  return visible.length ? visible.join('; ') : '—';
}

export function assertVisibleListCell(cell, expectedValues, description) {
  assert.equal(cell?.text, expectedVisibleListText(expectedValues), description);
}

function inspectObservation(resource, project, generation) {
  if (resource?.resourceType !== 'Observation' || resource?.generation !== generation) return undefined;
  if (typeof resource.id !== 'string' || !resource.id || typeof resource.sourceKey !== 'string' || !resource.sourceKey) return undefined;
  if (!Array.isArray(resource.components) || resource.components.length === 0 || resource.components.length > 8) return undefined;

  const labels = [];
  const codingShapes = [];
  const codings = [];
  let hasEmptyCoding = false;
  let hasMissingCoding = false;
  let hasMultiCodingComponent = false;
  let invalidCoding = false;

  resource.components.forEach((component, componentOrdinal) => {
    const label = component?.valueString;
    if (typeof label !== 'string' || !label.trim()) return;
    labels.push(label);

    const coding = component?.code?.coding;
    if (!Array.isArray(coding)) {
      hasMissingCoding = true;
      codingShapes.push({ componentOrdinal, state: 'missing' });
      return;
    }
    if (coding.length === 0) {
      hasEmptyCoding = true;
      codingShapes.push({ componentOrdinal, state: 'empty' });
      return;
    }
    if (coding.length > 6) {
      invalidCoding = true;
      return;
    }
    if (coding.length >= 2) hasMultiCodingComponent = true;
    codingShapes.push({ componentOrdinal, state: 'present', count: coding.length });
    coding.forEach((item, codingOrdinal) => {
      if (typeof item?.code !== 'string' || !item.code.trim()) {
        invalidCoding = true;
        return;
      }
      codings.push({
        componentOrdinal,
        codingOrdinal,
        ordinal: codings.length,
        componentLabel: label,
        code: item.code,
        system: typeof item.system === 'string' && item.system.trim() ? item.system : null,
      });
    });
  });

  if (labels.length !== resource.components.length || invalidCoding) return undefined;
  return {
    id: resource.id,
    sourceKey: resource.sourceKey,
    project,
    generation,
    componentLabels: labels,
    codingShapes,
    codings,
    hasEmptyCoding,
    hasMissingCoding,
    hasMultiCodingComponent,
  };
}

const hasDuplicateCoding = record => {
  const counts = new Map();
  for (const coding of record.codings) {
    const key = JSON.stringify([coding.system, coding.code]);
    const count = (counts.get(key) ?? 0) + 1;
    counts.set(key, count);
    if (count > 1) return true;
  }
  return false;
};

/**
 * Select a bounded raw Observation pair for one explicit nested-list shape.
 * The default requires a component with multiple codings; the separate
 * single-coding-per-component mode proves the flattened outer component list.
 * The empty-parent row is selected when available and otherwise stays a gap.
 */
export function selectNestedAuthoredExpandWitnesses(rows, {
  project,
  generation,
  scanLimit = 1000,
  witnessMode = 'multi-coding-component',
} = {}) {
  assert(Array.isArray(rows), 'Raw CDA Observation scan must be an array');
  assert(typeof project === 'string' && project.length > 0, 'Raw CDA oracle project is required');
  assert(typeof generation === 'string' && generation.length > 0, 'Raw CDA oracle generation is required');
  assert(Number.isInteger(scanLimit) && scanLimit > 0 && scanLimit <= 1000, 'Raw CDA scan limit must be between one and 1000');
  assert(['multi-coding-component', 'single-coding-per-component'].includes(witnessMode),
    'Raw CDA nested EXPAND witness mode must be multi-coding-component or single-coding-per-component');
  assert(rows.length <= scanLimit, `Raw CDA scan returned more than its ${scanLimit}-Observation bound`);

  const ids = new Set();
  for (const row of rows) {
    assert.equal(row?.resourceType, 'Observation', 'Raw CDA query returned a non-Observation');
    assert.equal(row?.generation, generation, 'Raw CDA query returned another dataset generation');
    assert.equal(row?.project, project, 'Raw CDA query returned another project');
    assert(typeof row.id === 'string' && row.id, 'Raw Observation is missing its public ID');
    assert(typeof row.sourceKey === 'string' && row.sourceKey, 'Raw Observation is missing its Arango source key');
    assert(!ids.has(row.id), `Raw Observation query returned duplicate ID ${row.id}`);
    ids.add(row.id);
  }

  const inspected = rows.map(row => inspectObservation(row, project, generation)).filter(Boolean)
    .sort((left, right) => compareText(left.id, right.id));
  const empty = inspected.find(record => record.codings.length === 0
    && record.codingShapes.length > 0
    && record.codingShapes.every(shape => shape.state === 'empty' || shape.state === 'missing'));
  const populated = inspected.find(record => record.codings.length >= 2
    && (witnessMode === 'multi-coding-component'
      ? record.hasMultiCodingComponent
      : !record.hasMultiCodingComponent && record.codingShapes.every(shape => shape.state !== 'present' || shape.count === 1))
    && record.codings.length + (empty ? 1 : 0) <= 25);

  const missingWitness = witnessMode === 'multi-coding-component'
    ? 'No bounded Observation has a populated nested coding list with a multi-coding component'
    : 'No bounded Observation has at least two nested coding values with one coding per populated component';
  assert(populated, `${missingWitness} (scanned ${rows.length})`);

  const selected = [populated, ...(empty ? [empty] : [])].map(record => ({
    id: record.id,
    sourceKey: record.sourceKey,
    project: record.project,
    generation: record.generation,
    resourceType: 'Observation',
    componentLabels: record.componentLabels,
    codingShapes: record.codingShapes,
    codings: record.codings,
  }));
  const expectedRows = [
    ...populated.codings.map(coding => ({
      id: populated.id,
      sourceKey: populated.sourceKey,
      componentOrdinal: coding.componentOrdinal,
      codingOrdinal: coding.codingOrdinal,
      ordinal: coding.ordinal,
      componentLabel: coding.componentLabel,
      code: coding.code,
      system: coding.system,
      itemPresent: true,
    })),
    ...(empty ? [{
      id: empty.id,
      sourceKey: empty.sourceKey,
      componentOrdinal: null,
      codingOrdinal: null,
      ordinal: null,
      componentLabel: null,
      code: null,
      system: null,
      itemPresent: false,
    }] : []),
  ];
  assert(expectedRows.length <= 25, 'Raw CDA nested EXPAND witness exceeds the complete preview row limit');
  assert(expectedRows.some(row => row.itemPresent && row.ordinal === 0), 'Populated witness has no first nested coding value');
  const gaps = empty ? [] : [{
    assertion: 'PRESERVE_PARENT emits an explicit row when the nested coding list is empty',
    status: 'untested',
    reason: `The bounded ${scanLimit}-Observation scan found no second root whose nested coding arrays are all empty or missing.`,
  }];

  return {
    source: 'project/generation-scoped raw Arango Observation payloads',
    scanLimit,
    scanned: rows.length,
    selected,
    expectedRows,
    gaps,
    witness: {
      mode: witnessMode,
      populatedObservationID: populated.id,
      emptyObservationID: empty?.id ?? null,
      hasMultiCodingComponent: populated.hasMultiCodingComponent,
      hasEmptyInnerCoding: populated.hasEmptyCoding,
      hasMissingInnerCoding: populated.hasMissingCoding,
      hasDuplicateSystemAndCode: hasDuplicateCoding(populated),
      hasEmptyParentWitness: Boolean(empty),
    },
  };
}
