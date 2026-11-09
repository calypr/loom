import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { root, digest } from './formal-tools.mjs';
import { publicationModel, publicationProperties, workspaceFile, expectedPublicationAcknowledgement, validatePublicationInput, extractPublicationCallback, loadPublicationImplementation, replayPublicationInput, validatePublicationGraph, publicationSourceProofs, publicationCounterexampleWitness, replayPublicationWitness } from './formal-publication-replay.mjs';
const fields = ['workspaceAttached', 'ownerMatches', 'explorerMatches', 'draftDigestMatches', 'intentMatches', 'snapshotMatches', 'previewRecompiled'];
const input = previewRecompiled => Object.fromEntries(fields.map(field => [field, field === 'previewRecompiled' ? previewRecompiled : true]));
const initial = () => ({ acknowledgementPhase: 'choose', acknowledgementInput: input(false), accepted: false });
function graph() {
    const states = [{ id: 'initial', start: true, vars: initial() }], steps = [];
    for (let mask = 0; mask < 128; mask++) {
        const value = Object.fromEntries(fields.map((name, i) => [name, Boolean(mask & 1 << i)]));
        states.push({ id: `pending-${mask}`, start: false, vars: { acknowledgementPhase: 'pending', acknowledgementInput: value, accepted: false } }, { id: `done-${mask}`, start: false, vars: { acknowledgementPhase: 'acknowledged', acknowledgementInput: value, accepted: expectedPublicationAcknowledgement(value) } });
        steps.push({ from: 'initial', to: `pending-${mask}`, action: 'ChooseAcknowledgement' }, { from: `pending-${mask}`, to: `done-${mask}`, action: 'ReceiveAcknowledgement' });
    }
    const files = [workspaceFile, 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/reconcileRecovery.ts', 'ui/packages/loom-ui/src/features/ExplorerBuilder/authoring/model.ts'];
    return { schemaVersion: 1, model: publicationModel, modelSha256: digest(join(root, `verification/builder-interactions/${publicationModel}.tla`)), configSha256: digest(join(root, `verification/builder-interactions/${publicationModel}.cfg`)), productionSha256: Object.fromEntries(files.map(file => [file, digest(join(root, file))])), states, steps };
}
test('acknowledgement identity ignores only preview epoch and rejects each changed context field', () => {
    assert.equal(expectedPublicationAcknowledgement(input(false)), true); assert.equal(expectedPublicationAcknowledgement(input(true)), true);
    for (const field of fields.filter(field => field !== 'previewRecompiled')) assert.equal(expectedPublicationAcknowledgement({ ...input(true), [field]: false }), false);
    for (const invalid of [null, {}, { ...input(false), extra: true }, { ...input(false), ownerMatches: 'true' }]) assert.throws(() => validatePublicationInput(invalid));
});
test('production callback extraction rejects missing, duplicate or changed dependency boundaries', () => {
    const text = readFileSync(join(root, workspaceFile), 'utf8'); assert.match(extractPublicationCallback(text), /^async/);
    assert.throws(() => extractPublicationCallback(text + '\n  const executePublish = useCallback(\n    '));
    assert.throws(() => extractPublicationCallback(text.replace('  const executePublish = useCallback(', '  const otherPublish = useCallback(')));
    assert.throws(() => extractPublicationCallback(text.replace('      publishBuilder,\n      reconcileCurrent,', '      unreviewed,\n      reconcileCurrent,')));
});
test('actual async callback preserves matching acknowledgements and rejects changed context', async () => {
    const implementation = await loadPublicationImplementation();
    for (const rename of [false, true]) {
        for (const advanced of [false, true]) assert.equal((await replayPublicationInput(input(advanced), { implementation, rename })).accepted, true);
        for (const field of fields.filter(field => field !== 'previewRecompiled')) assert.deepEqual((await replayPublicationInput({ ...input(true), [field]: false }, { implementation, rename })).events, []);
    }
});
test('each original failure mode has a concrete source witness and restored pass', async () => {
    const original = readFileSync(join(root, workspaceFile), 'utf8');
    for (const proof of publicationSourceProofs) {
        const [, before, after] = proof.edits[0], broken = original.replace(before, after), value = proof.property === publicationProperties[0] ? { ...input(false), [proof.branch === 'detached-acknowledgement' ? 'workspaceAttached' : 'ownerMatches']: false } : input(true);
        let witness;
        await assert.rejects(() => replayPublicationInput(value, { workspaceSource: broken }), error => { witness = error.witness; return error.code === 'FORMAL_PROPERTY' && error.property === proof.property; });
        assert.equal((await replayPublicationWitness(witness)).status, 'property-held'); assert.equal((await replayPublicationWitness(witness, { workspaceSource: broken })).status, 'reproduced');
        await assert.rejects(() => replayPublicationWitness({ ...witness, expectedAccepted: !witness.expectedAccepted }));
    }
    for (const workspaceSource of ['', ' \n ', null, false, 0]) await assert.rejects(() => replayPublicationInput(input(true), { workspaceSource }), /Publication source override/);
    const implementation = await loadPublicationImplementation();
    await assert.rejects(() => replayPublicationInput(input(true), { implementation, workspaceSource: original }), /Do not mix/);
    await assert.rejects(() => replayPublicationInput(input(true), { implementation, workspaceSource: '' }), /nonempty/);
});
test('full model frontier rejects missing, duplicate, stale and fictional transitions', () => {
    const original = graph(); assert.equal(validatePublicationGraph(original).length, 128);
    const changes = [g => g.states.pop(), g => g.steps.pop(), g => g.states.push(g.states[0]), g => g.steps.push(g.steps[0]), g => { g.steps[0].action = 'Unknown'; }, g => { g.steps[0].to = 'missing'; }, g => { g.modelSha256 = 'stale'; }, g => { g.productionSha256[workspaceFile] = 'stale'; }, g => { g.states.find(row => row.vars.acknowledgementPhase === 'acknowledged').vars.accepted = true; }];
    for (const change of changes) { const bad = structuredClone(original); change(bad); assert.throws(() => validatePublicationGraph(bad)); }
});
test('decoded TLC paths map to independent actual-callback witnesses', async () => {
    const value = input(true), record = { property: publicationProperties[1], states: [{ action: '', vars: initial() }, { action: 'ChooseAcknowledgement', vars: { acknowledgementPhase: 'pending', acknowledgementInput: value, accepted: false } }, { action: 'ReceiveAcknowledgement', vars: { acknowledgementPhase: 'acknowledged', acknowledgementInput: value, accepted: false } }] };
    const witness = publicationCounterexampleWitness(record); assert.equal((await replayPublicationWitness(witness)).status, 'property-held');
    const wrong = structuredClone(record); wrong.states[2].action = 'Unknown'; assert.throws(() => publicationCounterexampleWitness(wrong));
    assert.throws(() => publicationCounterexampleWitness({ ...record, states: record.states.slice(1) }));
    assert.throws(() => publicationCounterexampleWitness({ ...record, property: publicationProperties[0] }));
});
