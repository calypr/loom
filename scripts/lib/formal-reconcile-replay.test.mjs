import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import test from 'node:test';
import { root } from './formal-tools.mjs';
import { acceptanceFile, acceptanceSourceControls, acceptanceProperties, loadReconcileImplementation, expectedAcceptance, replayReconcileWitness, validateAcceptanceInput } from './formal-reconcile-replay.mjs';
const base = () => ({ currentOwner: 'A', currentFingerprint: 'F0', requestGeneration: 0, currentGeneration: 0,
    requestSnapshot: 0, responseSnapshot: 0, requestOutput: 0, responseOutputs: [0], diagnostic: 'none' });
const inputs = {
    obsolete: { ...base(), currentGeneration: 1 },
    mismatch: { ...base(), responseSnapshot: 1 },
    diagnostics: { ...base(), diagnostic: 'error' },
    accepted: { ...base(), diagnostic: 'warning' },
};
function witness(outcome) {
    return { schemaVersion: 1, kind: 'reconcile-acceptance', model: 'ReconcileAcceptance', property: acceptanceProperties[outcome], input: inputs[outcome], expected: outcome,
        variant: { swapOwner: true, swapFingerprint: true, outputOrder: [0] }, actions: ['ChooseInputs', 'ClassifyResponse'] };
}
test('production helper preserves all four caller branches and warning admission', async () => {
    const implementation = await loadReconcileImplementation();
    assert.equal(typeof implementation.reconcileAcceptance, 'function');
    for (const [outcome, input] of Object.entries(inputs)) {
        assert.equal(expectedAcceptance(input), outcome);
        assert.equal((await replayReconcileWitness(witness(outcome))).status, 'property-held');
    }
    const empty = { ...witness('accepted'), input: { ...base(), requestOutput: 3, responseOutputs: [] }, variant: { outputOrder: [] } };
    assert.equal((await replayReconcileWitness(empty)).status, 'property-held');
});
test('each actual production guard break is reproducible from exact witness inputs', async () => {
    const source = readFileSync(join(root, acceptanceFile), 'utf8');
    for (const control of acceptanceSourceControls) {
        assert.equal(source.split(control.before).length, 2);
        const outcome = Object.keys(acceptanceProperties).find(key => acceptanceProperties[key] === control.property);
        const input = JSON.parse(JSON.stringify(witness(outcome)));
        const broken = source.replace(control.before, control.after);
        const result = await replayReconcileWitness(input, { acceptanceSource: broken });
        assert.equal(result.status, 'reproduced');
        assert.equal(result.property, control.property);
        assert.equal((await replayReconcileWitness(input)).status, 'property-held');
    }
});
test('acceptance witnesses reject malformed domains, triggers, and output permutations', async () => {
    for (const input of [null, { ...base(), currentOwner: 'C' }, { ...base(), requestGeneration: 4 }, { ...base(), responseOutputs: [0, 0] }, { ...base(), diagnostic: 'unknown' }, { ...base(), extra: true }])
        assert.throws(() => validateAcceptanceInput(input));
    for (const input of [
        { ...witness('accepted'), property: 'unknown' },
        { ...witness('accepted'), expected: 'mismatch' },
        { ...witness('accepted'), actions: [] },
        { ...witness('accepted'), variant: { outputOrder: [2] } },
        { ...witness('accepted'), input: inputs.mismatch },
    ])
        await assert.rejects(() => replayReconcileWitness(input));
});
test('a decoded TLC failure becomes executable current-code inputs, not trusted model output', async () => {
    const { acceptanceCounterexampleWitness } = await import('./formal-reconcile-replay.mjs');
    const input = inputs.diagnostics;
    const trace = { property: acceptanceProperties.diagnostics, states: [
            { action: 'Initial', vars: { lastAction: 'Init' } },
            { action: 'ChooseInputs', vars: { lastAction: 'ChooseInputs', acceptanceInput: input } },
            { action: 'ClassifyResponse', vars: { lastAction: 'ClassifyResponse', phase: 'checked', acceptanceInput: input, outcome: 'accepted' } },
        ] };
    const value = acceptanceCounterexampleWitness(trace);
    assert.equal(value.expected, 'diagnostics');
    assert.equal((await replayReconcileWitness(value)).status, 'property-held');
    const source = readFileSync(join(root, acceptanceFile), 'utf8');
    const control = acceptanceSourceControls.find(control => control.property === trace.property);
    assert.equal((await replayReconcileWitness(value, { acceptanceSource: source.replace(control.before, control.after) })).status, 'reproduced');
    assert.throws(() => acceptanceCounterexampleWitness({ ...trace, states: [] }));
});
