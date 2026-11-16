import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, mkdirSync, mkdtempSync, writeFileSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { spawnSync } from 'node:child_process';
import { prepareTraces } from './formal-implementation-conformance.mjs';
import { transitionPaths, replayApplyPath, loadApplyImplementation } from './formal-apply-replay.mjs';
import { scriptsDigest, root } from './formal-tools.mjs';
import { reviewedSourceDrift, refreshReviewedSources } from './reviewed-sources.mjs';
import { sourceProofs } from './formal-source-proofs.mjs';
import { modelControls } from './formal-model-controls.mjs';
import { state, step } from '../../verification/builder-interactions/statechart.view.mjs';
const expected = { sourceProfile: 'formal', sourceCommit: 'a'.repeat(40), contractSourceCommit: 'a'.repeat(40), sourceFingerprint: 'b'.repeat(64) };
function observation(overrides = {}) {
    return { workspace: { project: 'org/project', explorerId: 'E', snapshotToken: 'S', expectedDraftVersion: 1, expectedDraftDigest: 'D', outputId: 'O', stageId: 'stage' },
        status: 'idle', proposalId: null, proposalOutputId: null, proposalSnapshotToken: null, proposalDraftVersion: null, proposalDraftDigest: null,
        previewReceiptId: null, previewOutputId: null, canApply: false, rowsPresent: false, ...overrides };
}
function header(actions = [{ label: 'Start', event: 'propose-construction', owner: { kind: 'proposal', attempt: 'P1' }, observation: observation({ status: 'previewing' }) }]) {
    return { schemaVersion: 1, ...expected, traces: [{ name: 'actual-fixture', model: 'BuilderLifecycle', expectedFailure: false, initialObservation: observation(), actions, unmodeledEvents: [] }] };
}
test('reject stale, empty, unsupported, and incomplete evidence', () => {
    assert.throws(() => prepareTraces({ ...header(), sourceFingerprint: 'stale' }, expected));
    assert.throws(() => prepareTraces({ ...header(), traces: [] }, expected));
    for (const mutate of [
        (h) => { h.traces[0].actions = []; },
        (h) => { h.traces[0].model = 'unknown'; },
        (h) => { h.traces[0].actions[0].event = 'unknown'; },
        (h) => { h.traces[0].actions[0].observation.status = 'invented'; },
        (h) => { delete h.traces[0].actions[0].owner; },
        (h) => { delete h.traces[0].initialObservation.proposalId; },
        (h) => { h.traces[0].actions[0].completionAccepted = 'yes'; },
        (h) => { h.traces[0].expectedFailure = true; },
        (h) => { h.traces[0].unmodeledEvents = [{ kind: 'unknown' }]; },
    ]) {
        const h = header();
        mutate(h);
        assert.throws(() => prepareTraces(h, expected));
    }
});
test('actor identity and current inputs classify stale responses, not human labels', () => {
    const actions = [
        { label: 'LateDefinitelyIgnored', event: 'propose-construction', owner: { kind: 'proposal', attempt: 'P1' }, observation: observation({ status: 'previewing' }) },
        { label: 'not a special label', event: 'proposal-response', owner: { kind: 'proposal', attempt: 'P1' }, observation: observation({ status: 'previewing' }) },
        { label: 'Start second', event: 'propose-construction', owner: { kind: 'proposal', attempt: 'P2' }, observation: observation({ status: 'previewing' }) },
        { label: 'ordinary return', event: 'proposal-response', owner: { kind: 'proposal', attempt: 'P1' }, observation: observation({ status: 'previewing' }) },
    ];
    const records = prepareTraces(header(actions), expected)[0];
    assert.equal(records[2].staleEvent, false);
    assert.equal(records[4].staleEvent, true);
});
test('reject mixed trace families before constructing a model', () => {
    const h = header();
    h.traces.push({ ...h.traces[0], model: 'ReconcileLifecycle' });
    assert.throws(() => prepareTraces(h, expected), /Mixed trace/);
});
test('every actual model action has an explicit reader label', () => {
    for (const name of Object.keys(modelControls)) {
        const source = readFileSync(join(root, `verification/builder-interactions/${name}.tla`), 'utf8');
        const definitions = [...source.matchAll(/^([A-Z]\w*)(?:\([^\n]*\))? ==([\s\S]*?)(?=^\w+(?:\([^\n]*\))? ==|^={3})/gm)];
        const actions = definitions.filter((match) => /\w+'/.test(match[2])).map((match) => match[1]);
        assert.ok(actions.length > 0);
        for (const action of actions)
            assert.equal(typeof step(action, {}, {}), 'string', `${name}.${action}`);
    }
    assert.throws(() => step('new-unknown-action', {}, {}), /No reader label/);
});
test('view separates bad reviewed readiness from a healthy state', () => {
    const o = { kind: 'lifecycle', status: 'ready', proposalId: 'P', proposalOutput: 'O', currentOutput: 'O', proposalSnapshot: 'S', currentSnapshot: 'S', proposalVersion: 1, currentVersion: 1, proposalDigest: 'D', currentDigest: 'D', previewReceipt: 'P', previewOutput: 'O', rowsPresent: true, canApply: true, event: 'proposal-response' };
    assert.notEqual(state({ observation: o }), state({ observation: { ...o, previewReceipt: 'other' } }));
    assert.notEqual(state({ observation: o }), state({ observation: { ...o, canApply: false } }));
});
test('view preserves the reason an Apply began without prior readiness', () => {
    const o = { kind: 'lifecycle', status: 'applying', proposalId: 'P', proposalOutput: 'O', currentOutput: 'O', proposalSnapshot: 'S', currentSnapshot: 'S', proposalVersion: 1, currentVersion: 1, proposalDigest: 'D', currentDigest: 'D', previewReceipt: 'P', previewOutput: 'O', rowsPresent: true, canApply: false, event: 'begin-apply' };
    assert.notEqual(state({ observation: o, previousObservation: { status: 'ready', canApply: true, proposalId: 'P' } }), state({ observation: o, previousObservation: { status: 'ready', canApply: false, proposalId: 'P' } }));
});
test('source and model proof inventory covers every declared behavioral rule', () => {
    for (const proof of sourceProofs) if (proof.checkedProperties) {
        assert.ok(proof.checkedProperties.includes(proof.property));
        assert.equal(new Set(proof.checkedProperties).size, proof.checkedProperties.length);
    }
    for (const model of Object.keys(modelControls)) {
        const declared = [...readFileSync(join(root, `verification/builder-interactions/${model}.cfg`), 'utf8').matchAll(/^INVARIANT (\w+)$/gm)].map((m) => m[1]).filter((name) => name !== 'TypeOK').sort();
        assert.deepEqual([...new Set(modelControls[model].map((control) => control[0]))].sort(), declared);
        assert.deepEqual([...new Set(sourceProofs.filter((proof) => proof.model === model).flatMap((proof) => proof.checkedProperties || [proof.property]))].sort(), declared);
    }
});
test('tool tree digests detect a modified dependency byte', () => {
    const dir = mkdtempSync(join(tmpdir(), 'loom-tool-hash-'));
    try {
        writeFileSync(join(dir, 'a.ts'), 'real code');
        const actual = scriptsDigest(dir);
        const expected = createHash('sha256').update('a.ts\0real code\0').digest('hex');
        assert.equal(actual, expected);
        writeFileSync(join(dir, 'a.ts'), 'changed code');
        assert.notEqual(scriptsDigest(dir), expected);
    }
    finally {
        rmSync(dir, { recursive: true, force: true });
    }
});
test('graph replay refuses dangling or unreachable model states', () => {
    assert.throws(() => transitionPaths({ schemaVersion: 1, states: [{ id: 'A', start: true, vars: {} }], steps: [{ from: 'A', to: 'missing' }] }), /Dangling/);
    assert.throws(() => transitionPaths({ schemaVersion: 1, states: [{ id: 'A', start: true, vars: {} }, { id: 'B', start: false, vars: {} }], steps: [] }), /unreachable/);
});
test('graph action labels cannot be replaced by a different state annotation', () => {
    assert.throws(() => transitionPaths({ schemaVersion: 1,
        states: [{ id: 'A', start: true, vars: { lastAction: 'Init' } }, { id: 'B', start: false, vars: { lastAction: 'SetCurrentOwner' } }],
        steps: [{ from: 'A', to: 'B', action: 'Cancel' }] }), /edge label/);
});
test('real ownership component replay cannot silently skip an unknown action', async () => {
    const implementation = await loadApplyImplementation();
    assert.throws(() => replayApplyPath([{ action: 'unmapped', expected: {} }], implementation), /Unmapped/);
});
test('a TLC counterexample replays on production and reproduces a broken guard', async () => {
    const { replayWitness, counterexampleWitness } = await import('./formal-apply-replay.mjs');
    const actions = [
        ['SetCurrentOwner', { inputOwner: 'A' }], ['BeginApply', {}], ['Cancel', {}],
        ['SetCurrentOwner', { inputOwner: 'B' }], ['BeginApply', {}],
        ['ApplyReturns', { inputOwner: 'A', responseOwner: 'A', lastApplied: true }],
    ];
    const counterexample = { property: 'Inv_OldReturnPreservesCurrentApply', states: [
            { action: 'Initial', vars: { lastAction: 'Init' } },
            ...actions.map(([action, vars]) => ({ action, vars: { ...vars, lastAction: action } })),
        ] };
    const witness = counterexampleWitness(counterexample);
    assert.equal((await replayWitness(witness)).status, 'property-held');
    const source = readFileSync(join(root, 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/constructionApplyOwnership.ts'), 'utf8');
    const anchor = 'if (slot.current !== attempt) return false;';
    assert.equal(source.split(anchor).length, 2);
    assert.equal((await replayWitness(witness, { source: source.replace(anchor, '') })).status, 'reproduced');
    await assert.rejects(() => replayWitness({ ...witness, property: 'unknown' }));
    await assert.rejects(() => replayWitness({ ...witness, path: [] }));
    await assert.rejects(() => replayWitness({ ...witness, path: [{ action: 'ApplyReturns', expected: {} }] }));
});
test('unsupported generated adapter cannot produce an empty passing audit', () => {
    const result = spawnSync(process.execPath, ['scripts/verify-builder-formal-core.mjs', '--model', 'unknown-adapter', '--phase', 'generated'], { cwd: root, encoding: 'utf8', timeout: 30000 });
    assert.equal(result.status, 1);
    assert.match(result.stderr, /--model must be/);
    assert.doesNotMatch(result.stdout, /Verified/);
});
test('generation input failures retain a witness that breaks and restores', async () => {
    const { checkApplyIdentityInputs, replayWitness } = await import('./formal-apply-replay.mjs');
    const source = readFileSync(join(root, 'ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/constructionApplyOwnership.ts'), 'utf8');
    const broken = source.replace('live.generation !== identity.generation', 'false');
    let witness;
    await assert.rejects(() => checkApplyIdentityInputs(broken), error => {
        assert.equal(error.code, 'FORMAL_PROPERTY');
        witness = JSON.parse(JSON.stringify(error.witness));
        assert.equal(witness.kind, 'identity-input');
        assert.notEqual(witness.inputs.submittedGeneration, witness.inputs.liveGeneration);
        return true;
    });
    assert.equal((await replayWitness(witness, { source: broken })).status, 'reproduced');
    assert.equal((await replayWitness(witness)).status, 'property-held');
    await assert.rejects(() => replayWitness({ ...witness, inputs: { ...witness.inputs, liveGeneration: 'bad' } }));
});
test('every registered source fault has an exact current production anchor', () => {
    for (const proof of sourceProofs) {
        const sources = new Map();
        assert.ok(proof.edits.length > 0, `Missing source fault: ${proof.property}`);
        for (const [file, before, after] of proof.edits) {
            const source = sources.get(file) ?? readFileSync(join(root, file), 'utf8');
            assert.ok(before.length > 0, `Empty source fault anchor: ${proof.property}`);
            assert.notEqual(before, after, `Ineffective source fault: ${proof.property}`);
            assert.equal(source.split(before).length, 2, `Nonunique source fault: ${proof.property} in ${file}`);
            sources.set(file, source.replace(before, after));
        }
    }
});
test('every bounded predicate mutation preserves a replayable concrete input', async () => {
    const { checkSourceProof, replayPredicateWitness } = await import('./formal-source-proofs.mjs');
    const directory = mkdtempSync(join(tmpdir(), 'loom-predicate-witness-test-'));
    try {
        for (const [index, proof] of sourceProofs.entries()) {
            if (proof.kind !== 'predicate')
                continue;
            const output = join(directory, String(index));
            await checkSourceProof(proof, undefined, output);
            const witness = JSON.parse(readFileSync(join(output, 'counterexample.json'), 'utf8'));
            assert.equal(witness.property, proof.property);
            assert.ok(witness.inputs);
            assert.notEqual(witness.actual, witness.expected);
            let broken = readFileSync(join(root, proof.edits[0][0]), 'utf8');
            for (const [, before, after] of proof.edits)
                broken = broken.replace(before, after);
            assert.equal((await replayPredicateWitness(witness, broken)).status, 'reproduced');
            assert.equal((await replayPredicateWitness(witness)).status, 'property-held');
            await assert.rejects(() => replayPredicateWitness({ ...witness, check: 'unknown' }));
        }
    }
    finally {
        rmSync(directory, { recursive: true, force: true });
    }
});
test('every remaining UI suite is attached exactly once to a feature', async () => {
    const { behaviorInventory, features } = await import('./formal-behavior.mjs');
    const inventory = behaviorInventory();
    assert.ok(inventory.length > 0);
    assert.equal(new Set(inventory.map(row => row.file)).size, inventory.length);
    for (const feature of features) {
        assert.ok(inventory.some(row => row.feature === feature.id), `Empty feature: ${feature.id}`);
        assert.ok(readFileSync(join(root, `.codex/skills/builder-interaction-formal-verification/features/${feature.id}.md`), 'utf8').includes(`--feature ${feature.id}`));
    }
    const doctor = spawnSync(process.execPath, ['scripts/verify-builder-formal-core.mjs', '--phase', 'behavior', '--list'], { cwd: root, encoding: 'utf8', timeout: 30000, env: { ...process.env, TLA2TOOLS_JAR: '/nonexistent', PSTACK_STATECHART_ROOT: '/nonexistent' } });
    if (existsSync(join(root, 'ui/node_modules/vitest/vitest.mjs'))) {
        assert.equal(doctor.status, 0, doctor.stderr);
        assert.deepEqual(JSON.parse(doctor.stdout).suites, inventory);
    }
    else {
        assert.equal(doctor.status, 1);
        assert.match(doctor.stderr, /Install UI dependencies/);
    }
});

function gitIn(cwd, args) {
    const result = spawnSync('git', args, { cwd, encoding: 'utf8' });
    assert.equal(result.status, 0, result.stderr || result.stdout);
    return result.stdout.trim();
}

test('git-blob pins pass clean, fail on modification, and refresh after a commit', () => {
    const directory = mkdtempSync(join(tmpdir(), 'loom-reviewed-sources-'));
    try {
        gitIn(directory, ['init', '-q']);
        gitIn(directory, ['config', 'user.email', 'reviewed-sources@example.test']);
        gitIn(directory, ['config', 'user.name', 'Reviewed Sources']);
        mkdirSync(join(directory, 'src'));
        writeFileSync(join(directory, 'src/a.ts'), 'first\n');
        gitIn(directory, ['add', '.']);
        gitIn(directory, ['-c', 'commit.gpgsign=false', 'commit', '-qm', 'first']);
        const manifestPath = join(directory, 'reviewed-sources.json');
        const firstCommit = gitIn(directory, ['rev-parse', 'HEAD']);
        const firstBlob = gitIn(directory, ['rev-parse', 'HEAD:src/a.ts']);
        writeFileSync(manifestPath, `${JSON.stringify({ baselineCommit: firstCommit, pinFormat: 'git-blob-sha1', review: 'retain this review', files: { 'src/a.ts': firstBlob } }, null, 2)}\n`);

        const clean = reviewedSourceDrift(directory, JSON.parse(readFileSync(manifestPath, 'utf8')));
        assert.equal(clean.clean, true);
        assert.equal(clean.pinFormat, 'git-blob-sha1');
        assert.equal(clean.drift.length, 0);

        writeFileSync(join(directory, 'src/a.ts'), 'second\n');
        const drifted = reviewedSourceDrift(directory, JSON.parse(readFileSync(manifestPath, 'utf8')));
        assert.equal(drifted.clean, false);
        assert.deepEqual(drifted.drift.map((row) => row.path), ['src/a.ts']);
        assert.equal(drifted.drift[0].expected, firstBlob);
        assert.equal(drifted.drift[0].actual, gitIn(directory, ['hash-object', '--', 'src/a.ts']));
        assert.equal(drifted.drift[0].diff, `git diff ${firstCommit}..HEAD -- src/a.ts`);
        assert.throws(() => refreshReviewedSources({ root: directory, manifestPath, note: 'dirty worker' }), /dirty/i);

        gitIn(directory, ['-c', 'commit.gpgsign=false', 'commit', '-qam', 'second']);
        const refreshed = refreshReviewedSources({ root: directory, manifestPath, note: 'reviewed second revision' });
        const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
        assert.equal(manifest.pinFormat, 'git-blob-sha1');
        assert.equal(manifest.baselineCommit, gitIn(directory, ['rev-parse', 'HEAD']));
        assert.equal(manifest.files['src/a.ts'], gitIn(directory, ['rev-parse', 'HEAD:src/a.ts']));
        assert.equal(manifest.review, 'retain this review');
        assert.equal(manifest.repin.commit, refreshed.baselineCommit);
        assert.equal(manifest.repin.note, 'reviewed second revision');
        assert.match(manifest.repin.at, /^\d{4}-\d{2}-\d{2}T/);
        assert.equal(readFileSync(manifestPath, 'utf8'), `${JSON.stringify(manifest, null, 2)}\n`);
        assert.equal(reviewedSourceDrift(directory, manifest).clean, true);
    }
    finally {
        rmSync(directory, { recursive: true, force: true });
    }
});

test('legacy sha256 reviewed pins still detect a modified working file', () => {
    const directory = mkdtempSync(join(tmpdir(), 'loom-reviewed-legacy-'));
    try {
        writeFileSync(join(directory, 'a.ts'), 'real code');
        const expected = createHash('sha256').update('real code').digest('hex');
        const manifest = { baselineCommit: 'a'.repeat(40), files: { 'a.ts': expected } };
        const clean = reviewedSourceDrift(directory, manifest);
        assert.equal(clean.pinFormat, 'sha256');
        assert.equal(clean.clean, true);
        writeFileSync(join(directory, 'a.ts'), 'changed code');
        const drifted = reviewedSourceDrift(directory, manifest);
        assert.equal(drifted.clean, false);
        assert.equal(drifted.drift[0].path, 'a.ts');
        assert.equal(drifted.drift[0].expected, expected);
        assert.equal(drifted.drift[0].actual, createHash('sha256').update('changed code').digest('hex'));
        assert.equal(drifted.drift[0].diff, `git diff ${'a'.repeat(40)}..HEAD -- a.ts`);
        assert.throws(() => reviewedSourceDrift(directory, { ...manifest, pinFormat: 'unknown' }), /Unsupported/);
        assert.throws(() => reviewedSourceDrift(directory, { ...manifest, files: {} }), /at least one file/);
    }
    finally {
        rmSync(directory, { recursive: true, force: true });
    }
});
