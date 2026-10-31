import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { root, digest } from './formal-tools.mjs';
import { exclusionModel, exclusionProperties, exclusionSourceHashes, initialExclusionState, exclusionFrontier, validateExclusionGraph, checkExclusionGraphControls, exclusionCounterexampleWitness, replayExclusionWitness, loadExclusionImplementation, replayExclusionSchedule, exclusionSourceProofs, exclusionModelControls } from './formal-exclusion-replay.mjs';
const canonical = v => Array.isArray(v) ? v.map(canonical) : v && typeof v === 'object' ? Object.fromEntries(Object.keys(v).sort().map(k => [k, canonical(v[k])])) : v;
const key = v => JSON.stringify(canonical(v));
const copy = v => structuredClone(v);
function graph() {
    const frontier = exclusionFrontier(), ids = new Map(frontier.states.map((v, i) => [key(v), String(i)]));
    return { schemaVersion: 1, model: exclusionModel, modelSha256: digest(join(root, `verification/builder-interactions/${exclusionModel}.tla`)), configSha256: digest(join(root, `verification/builder-interactions/${exclusionModel}.cfg`)), productionSha256: exclusionSourceHashes(), states: frontier.states.map((vars, i) => ({ id: String(i), start: i === 0, vars })), steps: frontier.edges.map(([from, action, to]) => ({ from: ids.get(from), action, to: ids.get(to) })) };
}
function pathTo(predicate) {
    const g = graph(), states = new Map(g.states.map(s => [s.id, s.vars])), pending = [{ id: '0', path: [] }], visited = new Set();
    for (let i = 0; i < pending.length; i++) {
        const item = pending[i]; if (visited.has(item.id)) continue; visited.add(item.id); if (predicate(states.get(item.id))) return item.path;
        for (const edge of g.steps.filter(e => e.from === item.id)) pending.push({ id: edge.to, path: [...item.path, { action: edge.action, from: states.get(edge.from), to: states.get(edge.to) }] });
    }
    throw new Error('No fixture path');
}
function counterexample(positive = false) {
    const schedule = pathTo(v => v.exclusionPhase === 'drain' && v.changeKind === (positive ? 'none' : 'base') && v.routeCalls === 1);
    const waiting = schedule.at(-1).to;
    schedule.push({ action: 'DrainReturnsAndSubmit', from: waiting, to: { ...waiting, exclusionPhase: 'done', submitted: positive } });
    const states = [{ action: 'Initial', vars: initialExclusionState() }, ...schedule.map(e => ({ action: e.action, vars: copy(e.to) }))];
    states.at(-1).vars.submitted = !positive;
    return { property: exclusionProperties[positive ? 1 : 0], states };
}
test('exclusion frontier closes every bounded state and transition', () => {
    const g = graph(); assert.equal(g.states.length, 86); assert.equal(g.steps.length, 107); assert.equal(validateExclusionGraph(g), g);
    assert.ok(g.states.some(s => s.vars.submitted && s.vars.serverVersion === 2 && s.vars.localBase === 'A'));
});
test('missing duplicate stale and unsupported graph evidence fails closed', async () => {
    assert.equal(await checkExclusionGraphControls(graph()), 8);
    for (const change of [g => { g.states[1].start = true; }, g => { g.states[1].id = g.states[0].id; }, g => { g.extra = true; }, g => { g.states[0].vars.extra = undefined; }, g => { delete g.productionSha256[Object.keys(g.productionSha256)[0]]; }]) { const g = graph(); change(g); assert.throws(() => validateExclusionGraph(g)); }
});
test('source fault inventory has unique edits and both declared properties', () => {
    assert.equal(exclusionSourceProofs.length, 6); assert.equal(exclusionModelControls.length, 6);
    assert.deepEqual([...new Set(exclusionSourceProofs.map(p => p.property))].sort(), exclusionProperties.slice().sort());
    const source = readFileSync(join(root, 'ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx'), 'utf8');
    for (const proof of exclusionSourceProofs) for (const [, before] of proof.edits) assert.equal(source.split(before).length, 2, proof.branch);
});
test('real submission counterexample decodes only its named invariant', () => {
    const trace = counterexample(), witness = exclusionCounterexampleWitness(trace);
    assert.equal(witness.property, exclusionProperties[0]); assert.equal(witness.schedule.at(-1).to.submitted, false);
    for (const change of [v => { v.property = exclusionProperties[1]; }, v => { v.states[0].action = 'Other'; }, v => { v.states[1].action = 'Other'; }, v => { v.states.at(-1).vars.routeCalls++; }, v => { v.states.at(-1).vars.submitted = false; }, v => { v.states.splice(1, 1); }]) { const changed = copy(trace); change(changed); assert.throws(() => exclusionCounterexampleWitness(changed)); }
});
test('reject-all counterexample keeps a valid current-input witness', () => {
    const witness = exclusionCounterexampleWitness(counterexample(true));
    assert.equal(witness.property, exclusionProperties[1]); assert.equal(witness.schedule.at(-1).to.submitted, true);
});
test('malformed witness and source overrides cannot silently select pristine code', async () => {
    const witness = exclusionCounterexampleWitness(counterexample());
    for (const change of [v => { v.property = exclusionProperties[1]; }, v => { v.schedule[0].to.localBase = 'other'; }, v => { v.schedule[0].action = 'Other'; }, v => { v.extra = true; }]) { const changed = copy(witness); change(changed); await assert.rejects(replayExclusionWitness(changed)); }
    await assert.rejects(loadExclusionImplementation({ workspaceSource: '' }), /nonempty/);
    await assert.rejects(replayExclusionSchedule(witness.schedule, { workspaceSource: '', implementation: {} }), /Cannot mix/);
});
