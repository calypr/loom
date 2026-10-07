import assert from 'node:assert/strict';
import { classifyNetworkRecord } from './report.mjs';

const compareIDs = (left, right) => String(left).localeCompare(String(right));
const sortRows = rows => rows.slice().sort((left, right) => compareIDs(left[0], right[0]));

export function missingComponentGroupSkipReason(result) {
  assert(result && typeof result === 'object', 'Missing-component GROUP workflow result is required');
  if (result.status === 'passed') return undefined;
  if (result.status === 'unverified') {
    const boundedWitnessGap = result.oracle?.status === 'unverified' &&
      Array.isArray(result.gaps) && result.gaps.length === 1 &&
      result.gaps[0]?.assertion === 'bounded missing-component GROUP source oracle' &&
      result.gaps[0]?.status === 'unverified' &&
      typeof result.gaps[0]?.reason === 'string' && result.gaps[0].reason.trim().length > 0 &&
      (result.failures?.length ?? 0) === 0 && (result.invalidations?.length ?? 0) === 0 &&
      result.skipReason === result.gaps[0].reason;
    assert(boundedWitnessGap, 'Only an explicit bounded missing-component witness gap may be skipped');
    return result.gaps[0].reason;
  }
  throw new Error(`Missing-component GROUP workflow ${String(result.status)} must fail the native case`);
}

export function assertCountRowsGroupLabelEditIdentity(beforeStep, editedStep, expectedLabel) {
  assert(beforeStep && beforeStep.operation?.kind === 'GROUP', 'Saved baseline step must be GROUP');
  assert(editedStep && editedStep.operation?.kind === 'GROUP', 'Edited candidate step must remain GROUP');
  assert.equal(editedStep.id, beforeStep.id, 'Saved GROUP label edit must preserve its stable step ID');
  assert(typeof expectedLabel === 'string' && expectedLabel.trim().length > 0, 'Edited GROUP output label must be explicit');

  const baselineKeys = beforeStep.operation.group?.keys ?? [];
  const editedKeys = editedStep.operation.group?.keys ?? [];
  assert.deepEqual(editedKeys, baselineKeys, 'Saved GROUP label edit must preserve exact key bindings and output IDs');
  const baselineAggregates = beforeStep.operation.group?.aggregates ?? [];
  const editedAggregates = editedStep.operation.group?.aggregates ?? [];
  assert.equal(baselineAggregates.length, 1, 'Baseline GROUP must contain exactly one COUNT_ROWS aggregate');
  assert.equal(editedAggregates.length, 1, 'Edited GROUP must contain exactly one COUNT_ROWS aggregate');
  const baselineAggregate = baselineAggregates[0];
  const editedAggregate = editedAggregates[0];
  assert.equal(baselineAggregate.operation, 'COUNT_ROWS');
  assert.equal(editedAggregate.operation, 'COUNT_ROWS', 'Saved GROUP label edit must preserve COUNT_ROWS semantics');
  assert.equal(editedAggregate.outputColumnId, baselineAggregate.outputColumnId, 'Saved GROUP label edit must preserve the COUNT_ROWS output ID');
  assert.deepEqual(editedStep.operation, beforeStep.operation, 'Saved GROUP label edit must not change Group operation semantics');
  assert.deepEqual(editedStep.operation.group.missingKeyPolicy, beforeStep.operation.group.missingKeyPolicy,
    'Saved GROUP label edit must preserve the missing-key policy');

  const baselineOutputs = beforeStep.outputs ?? [];
  const editedOutputs = editedStep.outputs ?? [];
  assert.equal(editedOutputs.length, baselineOutputs.length, 'Saved GROUP label edit must preserve output count');
  const countOutputId = baselineAggregate.outputColumnId;
  const expectedOutputs = baselineOutputs.map(output => output.id === countOutputId
    ? { ...output, label: expectedLabel }
    : output);
  assert.deepEqual(editedOutputs, expectedOutputs, 'Saved GROUP label edit must preserve every output identity and change only the COUNT_ROWS label');
  const keyOutput = editedOutputs.find(output => output.id === editedKeys[0]?.outputColumnId);
  const countOutput = editedOutputs.find(output => output.id === countOutputId);
  assert(keyOutput?.name && countOutput?.name, 'Edited GROUP candidate must retain exact key and COUNT_ROWS outputs');
  assert.equal(countOutput.label, expectedLabel);

  return {
    stepId: editedStep.id,
    keyOutputId: keyOutput.id,
    countOutputId: countOutput.id,
    countOutputLabel: countOutput.label,
  };
}

export function assertCountRowsGroupLabelEditProposal({ beforeStep, editedStep, preview, expectedRows, expectedLabel }) {
  const identity = assertCountRowsGroupLabelEditIdentity(beforeStep, editedStep, expectedLabel);
  const keyOutput = editedStep.outputs.find(output => output.id === identity.keyOutputId);
  const countOutput = editedStep.outputs.find(output => output.id === identity.countOutputId);
  assert(preview && Array.isArray(preview.columns) && Array.isArray(preview.rows), 'Edited GROUP proposal must include complete preview columns and rows');
  assert.equal(preview.rowCount, expectedRows.length, 'Edited GROUP preview row count must match the raw oracle');
  assert.equal(preview.rows.length, expectedRows.length, 'Edited GROUP preview row array count must match the raw oracle');
  const keyColumn = preview.columns.find(column => column.column === keyOutput.name);
  const countColumn = preview.columns.find(column => column.column === countOutput.name);
  assert(keyColumn && countColumn, 'Edited GROUP preview must retain both authored output columns');
  assert.equal(keyColumn.label, keyOutput.label, 'Edited GROUP preview changed the key label');
  assert.equal(countColumn.label, expectedLabel, 'Edited GROUP preview must show the exact edited COUNT_ROWS label');
  const actualRows = sortRows(preview.rows.map(row => [String(row[keyColumn.column]), Number(row[countColumn.column])]));
  assert.deepEqual(actualRows, expectedRows, 'Edited GROUP preview must preserve the exact raw COUNT_ROWS rows');
  return {
    ...identity,
    rowCount: preview.rowCount,
    rows: actualRows,
  };
}

export function assertBuilderDraftAdvanced(beforeBuilder, afterBuilder) {
  assert(beforeBuilder && Number.isInteger(beforeBuilder.draftVersion), 'Baseline BuilderState must own an integer draftVersion');
  assert(afterBuilder && Number.isInteger(afterBuilder.draftVersion), 'Updated BuilderState must own an integer draftVersion');
  assert(typeof beforeBuilder.draftDigest === 'string' && beforeBuilder.draftDigest.length > 0,
    'Baseline BuilderState must own a draftDigest');
  assert(typeof afterBuilder.draftDigest === 'string' && afterBuilder.draftDigest.length > 0,
    'Updated BuilderState must own a draftDigest');
  assert(afterBuilder.draftVersion > beforeBuilder.draftVersion, 'Applying the saved GROUP label edit must advance the BuilderState draftVersion');
  assert.notEqual(afterBuilder.draftDigest, beforeBuilder.draftDigest,
    'Applying the saved GROUP label edit must advance the BuilderState draftDigest');
  return {
    beforeDraftVersion: beforeBuilder.draftVersion,
    afterDraftVersion: afterBuilder.draftVersion,
    beforeDraftDigest: beforeBuilder.draftDigest,
    afterDraftDigest: afterBuilder.draftDigest,
  };
}

export function selectMissingComponentGroupOracle(rows, { project, generation, scanLimit = 1000 } = {}) {
  assert(Array.isArray(rows), 'Raw Observation scan must be an array');
  assert(Number.isInteger(scanLimit) && scanLimit > 0 && scanLimit <= 1000, 'Raw Observation scan limit must be within 1..1000');
  assert(rows.length <= scanLimit, `Raw Observation scan exceeded its ${scanLimit}-row bound`);
  assert(typeof project === 'string' && project.length > 0, 'Raw Observation oracle requires the exact project');
  assert(typeof generation === 'string' && generation.length > 0, 'Raw Observation oracle requires the exact generation');

  const seen = new Set();
  for (const row of rows) {
    assert.equal(row.project, project, 'Raw Observation row belongs to another project');
    assert.equal(row.generation, generation, 'Raw Observation row belongs to another generation');
    assert.equal(row.payload?.resourceType, 'Observation', 'Raw row is not an Observation');
    assert(typeof row.id === 'string' && row.id.length > 0, 'Raw Observation row has no source ID');
    assert(!seen.has(row.id), `Raw Observation scan contains duplicate ID ${row.id}`);
    seen.add(row.id);
  }

  const positive = rows
    .filter(row => Array.isArray(row.payload?.component) && row.payload.component.length === 2)
    .map(row => ({ row, values: row.payload.component.map((item, ordinal) => ({ ordinal, value: item?.valueString })) }))
    .find(candidate => candidate.values.every(item => typeof item.value === 'string' && item.value.trim().length > 0)
      && new Set(candidate.values.map(item => item.value)).size === 2);
  const missingOwners = rows
    .filter(row => !Object.hasOwn(row.payload ?? {}, 'component'))
    .slice()
    .sort((left, right) => compareIDs(left.id, right.id))
    .slice(0, 3);

  if (!positive || missingOwners.length !== 3) {
    return {
      status: 'unverified',
      scanned: rows.length,
      reason: 'The bounded scan needs one Observation with exactly two distinct non-empty component values and three Observations whose component property is absent.',
      selected: [],
    };
  }

  const selected = [
    {
      id: positive.row.id,
      project,
      generation,
      resourceType: 'Observation',
      componentValues: positive.values,
    },
    ...missingOwners.map(row => ({
      id: row.id,
      project,
      generation,
      resourceType: 'Observation',
      emptyComponentKind: 'missing',
    })),
  ];
  const expectedComponentRows = positive.values.map(({ ordinal, value }) => ({ id: positive.row.id, ordinal, value }));
  const expectedMissingOwnerIDs = missingOwners.map(row => row.id);
  const expectedGroupRows = sortRows([
    [positive.row.id, 2],
    ...expectedMissingOwnerIDs.map(id => [id, 1]),
  ]);

  return {
    status: 'ready',
    scanned: rows.length,
    selected,
    expectedComponentRows,
    expectedRecordIDs: selected.map(item => item.id),
    expectedMissingOwnerIDs,
    expectedExpandedRowCount: 5,
    expectedGroupRows,
  };
}

export function assertGroupRemovalCancelRestoration({ beforeWorkspace, afterWorkspace, beforePreview, afterPreview, expectedRows }) {
  assert.deepEqual(afterWorkspace, beforeWorkspace, 'Canceling GROUP removal changed the saved Builder workspace');
  assert.deepEqual(beforePreview?.rows, expectedRows, 'Saved GROUP preview before removal Cancel differs from the raw oracle');
  assert.deepEqual(afterPreview?.rows, expectedRows, 'GROUP removal Cancel did not restore the raw-oracle preview');
  assert.equal(beforePreview?.rowCount, expectedRows.length, 'Saved GROUP preview before removal Cancel has the wrong row count');
  assert.equal(afterPreview?.rowCount, expectedRows.length, 'GROUP removal Cancel has the wrong row count');
  assert.deepEqual(afterPreview, beforePreview, 'Canceling GROUP removal changed the rendered saved preview');
  return { rowCount: afterPreview.rowCount, rows: afterPreview.rows };
}

export function assertNoUnexpectedCdaDiagnostics(report) {
  assert(report && typeof report === 'object', 'CDA diagnostic gate requires the live fixture report');
  const unexpectedNetwork = (report.network ?? []).filter(entry =>
    !entry.expectedHttpFailure && classifyNetworkRecord(entry) === 'unexpected-error');
  const unexpectedErrors = (report.errors ?? []).filter(entry => {
    if (entry.expected === true || entry.expectedCancellation || entry.expectedInjectedFault || entry.expectedHttpFailure) return false;
    if (entry.kind === 'expected-injected' && entry.injectedFault === true) return false;
    return true;
  });
  assert.deepEqual(unexpectedNetwork, [], 'Official CDA fixture recorded an unexpected network failure');
  assert.deepEqual(unexpectedErrors, [], 'Official CDA fixture recorded an unexpected browser or HTTP error');
  return { networkEntries: (report.network ?? []).length, errorEntries: (report.errors ?? []).length };
}
