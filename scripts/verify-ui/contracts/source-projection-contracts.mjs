import assert from 'node:assert/strict';
import { projectionBranchExpectations } from '../../lib/formal-projection-replay.mjs';

export const projectionContractName = 'source-projection-owner-branches';

export const projectionCaseIds = Object.keys(projectionBranchExpectations).map(branch => `projection-${branch}`);

export function assertProjectionReplayRecord(record) {
    assert.equal(record.schemaVersion, 1, 'projection contract: schema version changed');
    assert.ok(Array.isArray(record.results), 'projection contract: results must be an array');
    const byId = new Map();
    for (const row of record.results) {
        assert.equal(typeof row.id, 'string');
        assert.equal(typeof row.accept, 'boolean');
        assert.equal(typeof row.error, 'string');
        if (row.accept) assert.equal(row.error, '', `projection contract: ${row.id} accepted with error text`);
        else assert.ok(row.error.length > 0, `projection contract: ${row.id} refused with empty error`);
        byId.set(row.id, row);
    }
    for (const [branch, expectation] of Object.entries(projectionBranchExpectations)) {
        const observation = byId.get(`projection-${branch}`);
        assert.ok(observation, `projection contract: missing observation for branch ${branch}`);
        if (expectation.accept) {
            assert.equal(observation.accept, true, `projection contract: ${branch} must be accepted, error=${observation.error}`);
            assert.equal(observation.error, '', `projection contract: ${branch} accepted with error text`);
        } else {
            assert.equal(observation.accept, false, `projection contract: ${branch} must be refused`);
            assert.ok(observation.error.includes(expectation.contains),
                `projection contract: ${branch} reject error must contain ${JSON.stringify(expectation.contains)}, got ${JSON.stringify(observation.error)}`);
        }
    }
    assert.equal(record.results.length, Object.keys(projectionBranchExpectations).length, 'projection contract: unexpected extra observations');
    return { branches: record.results.length, refused: record.results.filter(row => !row.accept).length };
}

export function assertProjectionProofSet(controls) {
    assert.ok(Array.isArray(controls) && controls.length === 9, 'projection contract: expected nine arm proofs');
    for (const control of controls) {
        assert.equal(control.kind, 'projection-replay', `projection contract: ${control.arm ?? 'unknown'} has wrong kind`);
        assert.ok(control.positive && control.negative && control.restored, `projection contract: ${control.arm} falsification pair incomplete`);
        assert.equal(control.firedBranch, control.branch, `projection contract: ${control.arm} must fire its own branch`);
    }
    return controls.length;
}
