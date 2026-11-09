import assert from 'node:assert/strict';
import test from 'node:test';
import { projectionBranchExpectations, runProjectionBatch } from '../../lib/formal-projection-replay.mjs';
import { assertProjectionReplayRecord, assertProjectionProofSet, projectionCaseIds } from './source-projection-contracts.mjs';

test('projection contract case table mirrors every branch with a refusal string', () => {
    assert.equal(projectionCaseIds.length, Object.keys(projectionBranchExpectations).length);
    const refusals = Object.entries(projectionBranchExpectations).filter(([, value]) => !value.accept);
    assert.ok(refusals.length >= 10, 'reject direction must dominate the family');
    for (const [branch, value] of refusals) {
        assert.ok(typeof value.contains === 'string' && value.contains.length > 0, `${branch} lacks an exact reject string`);
    }
    assert.ok(refusals.some(([, value]) => value.contains.includes('melt input')), 'UNPIVOT melt-membership string present');
    assert.ok(refusals.some(([, value]) => value.contains.includes('unpivot payload')), 'UNPIVOT nil-payload string present');
});

test('projection contract asserts live Go observations per branch', () => {
    const { record } = runProjectionBatch();
    const summary = assertProjectionReplayRecord(record);
    assert.equal(summary.branches, projectionCaseIds.length);
    assert.equal(summary.refused, Object.values(projectionBranchExpectations).filter(value => !value.accept).length);
});

test('projection contract rejects tampered observations (self-validation)', () => {
    const { record } = runProjectionBatch();
    const tamperedAccept = structuredClone(record);
    const melt = tamperedAccept.results.find(row => row.id === 'projection-unpivot-non-melt');
    melt.accept = true;
    melt.error = '';
    assert.throws(() => assertProjectionReplayRecord(tamperedAccept), /must be refused/);
    const tamperedString = structuredClone(record);
    const nil = tamperedFind(tamperedAccept, tamperedString);
    nil.error = 'some other validator complained';
    assert.throws(() => assertProjectionReplayRecord(tamperedString), /has no unpivot payload/);
    const missing = structuredClone(record);
    missing.results = missing.results.filter(row => row.id !== 'projection-derive-not-operand');
    assert.throws(() => assertProjectionReplayRecord(missing), /missing observation for branch derive-not-operand/);
});

function tamperedFind(reference, target) {
    const index = reference.results.findIndex(row => row.id === 'projection-unpivot-nil-payload');
    assert.ok(index >= 0);
    return target.results[index];
}

test('projection contract proof-set validator fires on incomplete pairs (self-validation)', () => {
    const base = { kind: 'projection-replay', branch: 'unpivot-non-melt', arm: 'unpivot-melt-membership', positive: true, negative: true, restored: true, firedBranch: 'unpivot-non-melt' };
    const nine = Array.from({ length: 9 }, (_, index) => ({ ...base, arm: `arm-${index}` }));
    assert.equal(assertProjectionProofSet(nine), 9);
    assert.throws(() => assertProjectionProofSet(nine.slice(1)), /nine arm proofs/);
    assert.throws(() => assertProjectionProofSet(nine.map((row, index) => index === 3 ? { ...row, restored: false } : row)), /falsification pair incomplete/);
    assert.throws(() => assertProjectionProofSet(nine.map((row, index) => index === 5 ? { ...row, firedBranch: 'pivot-accept' } : row)), /must fire its own branch/);
});
