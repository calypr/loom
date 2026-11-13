import assert from 'node:assert/strict';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, existsSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root, digest } from './formal-tools.mjs';
import { apiDoctor, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const projectionValidationFile = 'internal/explorer/authoringv2/construction_validation.go';
export const projectionAdapterFile = 'internal/explorer/authoringv2/construction_projection_replay_test.go';
export const projectionTestName = 'TestProjectionOwnerBranchReplay';

export const projectionBranchExpectations = {
    'pivot-accept': { accept: true, contains: null },
    'pivot-not-consumed': { accept: false, contains: 'source projection must be consumed by its owner PIVOT step' },
    'group-accept': { accept: true, contains: null },
    'filter-accept': { accept: true, contains: null },
    'filter-wrong-column': { accept: false, contains: 'is not the column filtered by its owner FILTER step' },
    'filter-nil-payload': { accept: false, contains: 'is not the column filtered by its owner FILTER step' },
    'derive-accept': { accept: true, contains: null },
    'derive-not-operand': { accept: false, contains: 'is not a COLUMN operand of its owner DERIVE step' },
    'unpivot-accept': { accept: true, contains: null },
    'unpivot-non-melt': { accept: false, contains: 'is not a melt input of its owner UNPIVOT step' },
    'unpivot-nil-payload': { accept: false, contains: 'has no unpivot payload' },
    'owner-missing': { accept: false, contains: 'does not exist' },
    'owner-stepid-empty': { accept: false, contains: 'ownerStepId must be an exact step id' },
    'default-combine-owner': { accept: false, contains: 'source projection must be consumed by its owner PIVOT step' },
    'carry-cross-noncarrier': { accept: false, contains: 'cannot cross GROUP before its owner PIVOT' },
    'carry-missing-typed-output': { accept: false, contains: 'does not carry source projection' },
};

export const projectionSourceProofs = [
    { branch: 'owner-stepid-empty', arm: 'owner-required-id', edits: [['if !requiredID(projection.OwnerStepID) {', 'if false && !requiredID(projection.OwnerStepID) {']] },
    { branch: 'owner-missing', arm: 'owner-exists', edits: [['if ownerIndex < 0 {', 'if false && ownerIndex < 0 {']] },
    { branch: 'pivot-not-consumed', arm: 'pivot-consumes', edits: [['if !containsConstructionColumnID(owner.inputColumnIDs(), projection.ColumnID) {', 'if false && !containsConstructionColumnID(owner.inputColumnIDs(), projection.ColumnID) {']] },
    { branch: 'filter-wrong-column', arm: 'filter-column', edits: [['if owner.Operation.Filter == nil || owner.Operation.Filter.ColumnID != projection.ColumnID {', 'if false && (owner.Operation.Filter == nil || owner.Operation.Filter.ColumnID != projection.ColumnID) {']] },
    { branch: 'derive-not-operand', arm: 'derive-operand', edits: [['if owner.Operation.Derive == nil || !containsConstructionColumnID(owner.inputColumnIDs(), projection.ColumnID) {', 'if false && (owner.Operation.Derive == nil || !containsConstructionColumnID(owner.inputColumnIDs(), projection.ColumnID)) {']] },
    { branch: 'unpivot-nil-payload', arm: 'unpivot-nil-payload', edits: [['if owner.Operation.Unpivot == nil {', 'if false && owner.Operation.Unpivot == nil {']] },
    { branch: 'unpivot-non-melt', arm: 'unpivot-melt-membership', edits: [['if !melted {', 'if false && !melted {']] },
    { branch: 'carry-cross-noncarrier', arm: 'carry-noncarrier', edits: [['if !ConstructionCanCarrySourceProjection(step.Operation.Kind) {', 'if false && !ConstructionCanCarrySourceProjection(step.Operation.Kind) {']] },
    { branch: 'carry-missing-typed-output', arm: 'carry-typed-output', edits: [['if !found {', 'if false && !found {']] },
];

function projectionFiles() {
    return ['go.mod', 'go.sum', projectionAdapterFile, ...['internal/explorer/authoringv2', 'internal/explorer/capability'].flatMap(directory => readdirSync(join(root, directory)).filter(file => file.endsWith('.go') && (!file.endsWith('_test.go') || file === 'construction_projection_replay_test.go')).map(file => `${directory}/${file}`))].sort();
}


function hashes() {
    return Object.fromEntries(projectionFiles().map(file => [file, digest(join(root, file))]));
}

function runProjectionGo(cases, { productionSource, output } = {}) {
    const scratch = mkdtempSync(join(tmpdir(), 'loom-projection-go-')), tool = apiDoctor(), beforeSources = hashes();
    try {
        const inputPath = join(scratch, 'cases.json'), resultPath = join(scratch, 'results.json');
        writeFileSync(inputPath, JSON.stringify({ schemaVersion: 1, cases }));
        const args = ['test', '-json', `-timeout=${goTestTimeout}`, '-count=1', '-run', `^${projectionTestName}$`];
        if (productionSource !== undefined) {
            const replacement = join(scratch, 'validation.go'), overlay = join(scratch, 'overlay.json');
            writeFileSync(replacement, productionSource);
            writeFileSync(overlay, JSON.stringify({ Replace: { [join(root, projectionValidationFile)]: replacement } }));
            args.push('-overlay', overlay);
        }
        args.push('./internal/explorer/authoringv2');
        const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', LOOM_FORMAL_PROJECTION_INPUT: inputPath, LOOM_FORMAL_PROJECTION_OUTPUT: resultPath };
        if (tool.executable === '/workspace/.loom-setup/go/bin/go') {
            if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod';
            if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache';
        }
        const result = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true });
        if (result.error && result.pid) { try { process.kill(-result.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        if (output) { mkdirSync(output, { recursive: true }); writeFileSync(join(output, 'go.jsonl'), result.stdout || ''); writeFileSync(join(output, 'process.json'), JSON.stringify({ command: [tool.executable, ...args], exit: result.status, error: result.error?.message, stderr: result.stderr }, null, 2)); }
        assert.ifError(result.error);
        if (productionSource !== undefined) {
            if ((result.stdout || '').includes('build-failed')) { const error = new Error('Projection fault broke compilation'); Object.assign(error, { code: 'FAULT_COMPILE' }); throw error; }
            if (result.status !== 0) return { crashed: true, stdout: result.stdout, stderr: result.stderr, status: result.status };
            assert.ok(existsSync(resultPath), 'Overlaid Go run produced no observations');
            return { record: JSON.parse(readFileSync(resultPath, 'utf8')) };
        }
        assert.equal(result.status, 0, result.stdout + result.stderr);
        checkApiEvents(result.stdout, { tests: [projectionTestName] });
        assert.deepEqual(hashes(), beforeSources, 'Projection sources changed during Go execution');
        assert.ok(existsSync(resultPath), 'Go adapter produced no observations');
        return { record: JSON.parse(readFileSync(resultPath, 'utf8')) };
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}

export function projectionCases() {
    return Object.keys(projectionBranchExpectations).map(branch => ({ id: `projection-${branch}`, branch }));
}

export function runProjectionBatch() {
    const cases = projectionCases();
    const run = runProjectionGo(cases);
    assert.ok(!run.crashed, 'Projection batch crashed without overlay');
    return { record: run.record, cases };
}

export function checkProjectionResults(record, cases) {
    assert.deepEqual(Object.keys(record).sort(), ['schemaVersion', 'results'].sort());
    assert.equal(record.schemaVersion, 1);
    assert.ok(Array.isArray(record.results));
    assert.equal(record.results.length, cases.length, 'Missing Go observations');
    const actual = new Map();
    for (const row of record.results) {
        assert.deepEqual(Object.keys(row).sort(), ['id', 'branch', 'accept', 'error'].sort());
        assert.ok(!actual.has(row.id), 'Duplicate Go observation');
        actual.set(row.id, row);
    }
    const divergences = [];
    for (const row of cases) {
        const expectation = projectionBranchExpectations[row.branch];
        assert.ok(expectation, 'Unknown projection branch');
        const observation = actual.get(row.id);
        assert.ok(observation, 'Unknown or missing Go projection case');
        if (observation.accept !== expectation.accept) divergences.push({ branch: row.branch, kind: 'acceptance', expected: expectation.accept, observed: observation.accept, error: observation.error });
        else if (!expectation.accept && !(observation.error || '').includes(expectation.contains)) divergences.push({ branch: row.branch, kind: 'reject-string', contains: expectation.contains, observed: observation.error });
    }
    if (divergences.length) {
        const error = new Error(`Projection branch divergence: ${JSON.stringify(divergences[0])}`);
        Object.assign(error, { code: 'FORMAL_PROPERTY', divergences });
        throw error;
    }
    return { branches: cases.length, accepted: record.results.filter(row => row.accept).length, rejected: record.results.filter(row => !row.accept).length };
}

export async function checkProjectionReplay({ output, productionSource, witnessFile } = {}) {
    const cases = projectionCases(), before = hashes();
    try {
        const run = runProjectionGo(cases, { output, productionSource });
        if (run.crashed) {
            const error = new Error('Projection validator crashed under overlay');
            Object.assign(error, { code: 'FORMAL_PROPERTY', crash: true, testOutput: String(run.stdout ?? '') + String(run.stderr ?? ''), goStatus: run.status ?? null });
            throw error;
        }
        const result = checkProjectionResults(run.record, cases);
        assert.deepEqual(hashes(), before, 'Projection sources changed during replay');
        return { ...result, boundary: 'actual public Construction.Validate for accepts and unguarded rejects; direct validator entry pins exact branch strings for nil-payload and non-owner switches that collide with earlier structural validators (see construction_projection_replay_test.go header)' };
    } catch (error) {
        if (witnessFile && error.code === 'FORMAL_PROPERTY') writeFileSync(witnessFile, JSON.stringify({ schemaVersion: 1, kind: 'projection-branch', divergences: error.divergences ?? null, crash: error.crash ?? false, error: error.message, testOutput: error.testOutput ?? '', goStatus: error.goStatus ?? null }, null, 2));
        throw error;
    }
}

export async function checkProjectionSourceProof(proof, output) {
    const registered = projectionSourceProofs.find(row => row.branch === proof.branch && row.arm === proof.arm);
    assert.deepEqual(proof, registered, 'Unknown projection proof');
    mkdirSync(output, { recursive: true });
    const [[before, after]] = proof.edits;
    const sourcePath = join(root, projectionValidationFile), original = readFileSync(sourcePath, 'utf8');
    assert.equal(original.split(before).length, 2, 'Nonunique projection fault anchor');
    const broken = original.replace(before, after);
    await checkProjectionReplay({ output: join(output, 'positive') });
    const witnessFile = join(output, 'counterexample.json');
    await assert.rejects(() => checkProjectionReplay({ productionSource: broken, output: join(output, 'negative'), witnessFile }), error => error.code === 'FORMAL_PROPERTY');
    assert.ok(existsSync(witnessFile), 'Projection proof produced no counterexample');
    const witness = JSON.parse(readFileSync(witnessFile, 'utf8'));
    const firedBranch = witness.crash ? proof.branch : (witness.divergences?.[0]?.branch ?? null);
    assert.ok(firedBranch, 'Counterexample names no branch');
    if (witness.crash) {
        assert.match(String(witness.testOutput ?? ''), /panic/, `Crash witness without panic output: ${String(witness.error).slice(0, 120)}`);
        assert.match(String(witness.testOutput ?? ''), /TestProjectionOwnerBranchReplay/, 'Panic outside the replay family test');
    } else {
        assert.equal(firedBranch, proof.branch, `Proof ${proof.branch} (${proof.arm}) fired a different branch: ${String(firedBranch)}`);
    }
    await checkProjectionReplay({ output: join(output, 'restored') });
    const evidence = { kind: 'projection-replay', branch: proof.branch, arm: proof.arm, sourceFiles: [projectionValidationFile], anchorCount: 1, positive: true, negative: true, restored: true, firedBranch, witness: 'counterexample.json' };
    writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2));
    return evidence;
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, directory, extra] = process.argv.slice(2);
    if (command === '--generate') {
        assert.ok(!extra || extra === '--self-test');
        mkdirSync(directory, { recursive: true });
        const replay = await checkProjectionReplay({ output: join(directory, 'replay') });
        if (extra === '--self-test') {
            replay.sourceControls = [];
            for (const proof of projectionSourceProofs) replay.sourceControls.push(await checkProjectionSourceProof(proof, join(directory, `proof-${proof.arm}`)));
        }
        writeFileSync(join(directory, 'replay.json'), JSON.stringify(replay, null, 2));
        console.log(JSON.stringify(replay));
    } else throw new Error('Use --generate DIRECTORY [--self-test]');
}
