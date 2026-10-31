#!/usr/bin/env node
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { spawnSync } from 'node:child_process';
import { root, digest, tools, lock } from './lib/formal-tools.mjs';
import { validateModelVerdict } from './lib/formal-verdict.mjs';
import { modelControls } from './lib/formal-model-controls.mjs';
import { sourceProofs } from './lib/formal-source-proofs.mjs';
import { behaviorDoctor, runBehavior, features } from './lib/formal-behavior.mjs';
import { traceRules, checkTraceRule } from './lib/formal-trace-controls.mjs';
const options = { model: 'all', phase: 'all', selfTest: false, modelsOnly: false, shard: undefined, out: undefined };
for (let i = 2; i < process.argv.length; i++) {
    const option = process.argv[i];
    if (option === '--list')
        options.list = true;
    else if (option === '--self-test')
        options.selfTest = true;
    else if (option === '--models-only')
        options.modelsOnly = true;
    else {
        const key = { '--model': 'model', '--phase': 'phase', '--shard': 'shard', '--out': 'out', '--feature': 'feature', '--case': 'pattern', '--chrome': 'chrome' }[option];
        assert.ok(key && process.argv[i + 1] && !process.argv[i + 1].startsWith('--'), `Unknown/missing option: ${option}`);
        options[key] = process.argv[++i];
    }
}
const groups = { all: ['BuilderLifecycle', 'ApplySettlementRace', 'ReconcileLifecycle', 'ReconcileAcceptance'], lifecycle: ['BuilderLifecycle', 'ApplySettlementRace'], reconcile: ['ReconcileLifecycle', 'ReconcileAcceptance'], 'reconcile-acceptance': ['ReconcileAcceptance'], 'apply-settlement': ['ApplySettlementRace'] };
const names = groups[options.model];
assert.ok(names, '--model must be all, lifecycle, reconcile, reconcile-acceptance, or apply-settlement');
assert.ok(options.phase !== 'generated' || names.some(name => ['ApplySettlementRace', 'ReconcileAcceptance'].includes(name)), 'No generated-code adapter for selected model');
assert.ok(['all', 'models', 'implementation', 'generated', 'controls', 'source-proofs', 'trace-controls', 'behavior', 'api', 'inventory', 'browser'].includes(options.phase), 'Unknown --phase');
assert.ok(!options.modelsOnly || ['all', 'models', 'controls'].includes(options.phase), '--models-only conflicts with phase');
assert.ok(!options.shard || ['controls', 'source-proofs', 'trace-controls', 'behavior'].includes(options.phase), '--shard is for control/behavior phases');
const shard = options.shard && /^(\d+)\/(\d+)$/.exec(options.shard);
if (options.shard)
    assert.ok(shard && +shard[1] > 0 && +shard[1] <= +shard[2], 'Invalid --shard');
const selectShard = (rows) => rows.filter((_, i) => !shard || i % +shard[2] === +shard[1] - 1);
assert.ok(!(options.feature || options.list) || ['behavior', 'api', 'browser'].includes(options.phase), '--feature/--list require --phase behavior or api');
assert.ok(!options.pattern || options.phase === 'behavior', '--case requires --phase behavior');
assert.ok(!options.chrome || options.phase === 'browser', '--chrome requires --phase browser');
assert.ok(!['behavior', 'api', 'inventory', 'browser'].includes(options.phase) || options.model === 'all', 'Use --feature to select behavior/API checks; --model selects formal checks');
assert.ok(!options.selfTest || options.phase === 'all', '--self-test requires --phase all');
const emit = async (value) => new Promise((resolve, reject) => process.stdout.write(JSON.stringify(value, null, 2) + '\n', error => error ? reject(error) : resolve()));
if (options.phase === 'inventory') {
    assert.ok(!options.out && !options.feature && !options.pattern && !options.list, 'Inventory is a read-only command');
    const { verificationInventory } = await import('./lib/verification-inventory.mjs');
    await emit(await verificationInventory());
    process.exit(0);
}
if (options.list) {
    assert.ok(!options.out && !options.shard && !options.pattern, '--list is a read-only doctor, not a test run');
    if (options.phase === 'browser') {
        const checked = spawnSync(process.execPath, [join(root, 'scripts/browser/run.mjs'), '--doctor', '--feature', options.feature || 'all', ...(options.chrome ? ['--chrome', options.chrome] : [])], { cwd: root, encoding: 'utf8', timeout: 5000 });
        assert.ifError(checked.error);
        assert.equal(checked.status, 0, checked.stdout + checked.stderr);
        await emit(JSON.parse(checked.stdout));
    } else if (options.phase === 'api') {
        const { apiDoctor, selectApiChecks, apiChecks } = await import('./lib/formal-api.mjs');
        const checks = selectApiChecks(options.feature);
        for (const check of checks) for (const key of check.requiredEnvironment || []) assert.ok(process.env[key], `Set ${key} for integration check ${check.id}`);
        await emit({ status: 'ready-for-api-tests', tool: apiDoctor(), checks, availableChecks: apiChecks });
    }
    else {
        assert.ok(!options.feature || features.some(row => row.id === options.feature) || options.feature === 'all', 'Unknown feature');
        await emit({ status: 'ready-for-behavior-tests', features: features.map(({ id, model }) => ({ id, model })), suites: behaviorDoctor() });
    }
    process.exit(0);
}
const reviewed = JSON.parse(readFileSync(join(root, 'verification/builder-interactions/reviewed-sources.json'), 'utf8'));
for (const [file, expected] of Object.entries(reviewed.files))
    if (!['behavior', 'api', 'browser'].includes(options.phase))
        assert.equal(digest(join(root, file)), expected, `Review required: ${file}; baseline ${reviewed.baselineCommit}, reviewed working-tree hashes`);
const declared = (name) => [...readFileSync(join(root, `verification/builder-interactions/${name}.cfg`), 'utf8').matchAll(/^INVARIANT (\w+)$/gm)].map((match) => match[1]).filter((name) => name !== 'TypeOK');
for (const name of groups.all) {
    assert.deepEqual([...new Set(modelControls[name].map((row) => row[0]))].sort(), declared(name).sort(), `Missing/extra model controls: ${name}`);
    assert.deepEqual([...new Set(sourceProofs.filter((row) => row.model === name).map((row) => row.property))].sort(), declared(name).sort(), `Missing/extra real-code proofs: ${name}`);
}
const traceObligations = JSON.parse(readFileSync(join(root, 'verification/builder-interactions/trace-obligations.json'), 'utf8'));
assert.deepEqual(Object.keys(traceObligations).sort(), declared('ImplementationConformance').sort(), 'Trace property evidence map drift');
for (const properties of Object.values(traceObligations))
    for (const property of properties)
        assert.ok(sourceProofs.some((proof) => proof.property === property), `Missing trace source proof: ${property}`);
const runtime = ['behavior', 'api', 'browser'].includes(options.phase) ? undefined : tools();
const reportDirectory = options.out ? resolve(options.out) : mkdtempSync(join(tmpdir(), 'loom-formal-results-'));
if (options.out)
    assert.ok(!existsSync(reportDirectory) || readdirSync(reportDirectory).length === 0, 'Refusing to overwrite nonempty verification output');
mkdirSync(reportDirectory, { recursive: true });
function fingerprint() {
    const hash = createHash('sha256');
    const visit = (directory) => {
        for (const entry of readdirSync(join(root, directory), { withFileTypes: true }).sort((a, b) => a.name.localeCompare(b.name))) {
            const file = `${directory}/${entry.name}`;
            if (entry.isDirectory())
                visit(file);
            else if (entry.isFile())
                hash.update(file).update('\0').update(readFileSync(join(root, file))).update('\0');
        }
    };
    for (const directory of ['verification/builder-interactions', 'ui/packages/loom-ui/src', 'ui/packages/loom-ui/testSupport'])
        visit(directory);
    for (const file of ['ui/package-lock.json', 'ui/packages/loom-ui/vitest.config.ts', 'ui/packages/loom-ui/vitest.setup.ts', 'scripts/verify-builder-formal-core.mjs', 'scripts/setup-formal-tools.mjs', ...readdirSync(join(root, 'scripts/lib')).filter((f) => f.startsWith('formal-') && f.endsWith('.mjs')).map((f) => `scripts/lib/${f}`)])
        hash.update(file).update(readFileSync(join(root, file)));
    return hash.digest('hex');
}
const inputFingerprint = fingerprint(), started = performance.now();
const modelDirectory = join(root, 'verification/builder-interactions');
const summary = { schemaVersion: 1, baselineCommit: reviewed.baselineCommit, sourceFingerprint: inputFingerprint, tools: ['behavior', 'api', 'browser'].includes(options.phase) ? undefined : lock, model: options.model, phase: options.phase, modelsOnly: options.modelsOnly, selfTest: options.selfTest, models: [], sourceProofs: [], traceObligations, generated: undefined, status: 'running' };
let reportNumber = 0;
function checkModel(name, directory, expectedInvariant) {
    const output = join(reportDirectory, 'statecharts', `${++reportNumber}-${name}`), before = performance.now();
    const result = spawnSync(process.execPath, [join(root, 'scripts/lib/formal-statechart-worker.mjs'), join(directory, `${name}.tla`), join(directory, `${name}.cfg`), join(modelDirectory, 'statechart.view.mjs'), output], { cwd: root, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024, detached: true, env: { ...process.env, TLA2TOOLS_JAR: runtime.jar, PSTACK_STATECHART_ROOT: runtime.scripts } });
    if (result.error && result.pid) {
        try {
            process.kill(-result.pid, 'SIGKILL');
        }
        catch (error) {
            if (error.code !== 'ESRCH')
                throw error;
        }
    }
    if (result.error || !existsSync(join(output, 'result.json'))) {
        process.stderr.write(result.stdout || '');
        process.stderr.write(result.stderr || '');
        throw result.error || new Error(`${name}: statechart did not produce a result (exit ${result.status})`);
    }
    const checked = JSON.parse(readFileSync(join(output, 'result.json'), 'utf8'));
    let verdictError;
    try { validateModelVerdict(checked, result.status, expectedInvariant); }
    catch (error) { verdictError = error; }
    if (verdictError) {
        if (['ApplySettlementRace', 'ReconcileAcceptance'].includes(name) && !expectedInvariant && checked.counterexamples?.length) {
            const replay = spawnSync(process.execPath, [join(root, name === 'ApplySettlementRace' ? 'scripts/lib/formal-apply-replay.mjs' : 'scripts/lib/formal-reconcile-replay.mjs'), '--witness', join(output, 'result.json')], { cwd: root, encoding: 'utf8', timeout: 30000 });
            writeFileSync(join(output, 'production-replay.json'), JSON.stringify({ exit: replay.status, output: replay.stdout, error: replay.stderr, blocked: replay.error?.message }, null, 2));
        }
        process.stderr.write(result.stdout || '');
        process.stderr.write(result.stderr || '');
        throw new Error(`${name}: ${expectedInvariant ? `expected only ${expectedInvariant}` : 'verification failed'}; ${verdictError.message}; report ${output}`);
    }
    summary.models.push({ name, control: expectedInvariant, states: checked.states.distinct, chart: checked.chart, seconds: (performance.now() - before) / 1000, report: output });
    console.log(`${name}: ${expectedInvariant ? `caught ${expectedInvariant}` : 'PASS'} (${checked.states.distinct} states); statechart ${output}`);
    return checked;
}
let generated;
async function graph() {
    if (generated)
        return generated;
    const check = summary.models.find((item) => item.name === 'ApplySettlementRace' && !item.control) || checkModel('ApplySettlementRace', modelDirectory);
    const { modelGraph } = await import('./lib/formal-model-graph.mjs');
    generated = await modelGraph(modelDirectory, join(reportDirectory, 'model-graph.json'));
    const stateCount = check.states?.distinct ?? check.states;
    assert.equal(generated.states.length, stateCount, 'Generated graph differs from the fully checked state space');
    return generated;
}
let acceptanceGenerated;
async function acceptanceGraph() {
    if (acceptanceGenerated) return acceptanceGenerated;
    const checked = summary.models.find(item => item.name === 'ReconcileAcceptance' && !item.control) || checkModel('ReconcileAcceptance', modelDirectory);
    const directory = join(reportDirectory, 'acceptance-replay');
    const replay = spawnSync(process.execPath, [join(root, 'scripts/lib/formal-reconcile-replay.mjs'), '--generate', directory], { cwd: root, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
    assert.ifError(replay.error);
    assert.equal(replay.status, 0, replay.stdout + replay.stderr);
    acceptanceGenerated = JSON.parse(readFileSync(join(directory, 'graph.json'), 'utf8'));
    assert.equal(acceptanceGenerated.states.length, checked.states?.distinct ?? checked.states, 'Acceptance graph differs from fully checked states');
    summary.acceptanceReplay = JSON.parse(replay.stdout);
    console.log(`Reconcile actual-code replay PASS: ${JSON.stringify(summary.acceptanceReplay)}`);
    return acceptanceGenerated;
}
function scratchControl(name, [property, action, before, after]) {
    const scratch = mkdtempSync(join(tmpdir(), 'loom-model-control-'));
    try {
        const source = readFileSync(join(modelDirectory, `${name}.tla`), 'utf8');
        const start = source.search(new RegExp(`\\n${action}(?:\\([^\\n]*\\))? ==`));
        const next = source.slice(start + 1).search(/\n[A-Za-z_]\w*(?:\([^\n]*\))? ==/);
        const end = next < 0 ? source.lastIndexOf('\n====') : start + 1 + next;
        assert.ok(start >= 0 && end > start, `Missing model action: ${action}`);
        const body = source.slice(start, end);
        assert.equal(body.split(before).length, 2, `Nonunique model mutation: ${property}`);
        writeFileSync(join(scratch, `${name}.tla`), source.slice(0, start) + body.replace(before, after) + source.slice(end));
        writeFileSync(join(scratch, `${name}.cfg`), `SPECIFICATION Spec\nCHECK_DEADLOCK FALSE\nINVARIANT ${property}\n`);
        checkModel(name, scratch, property);
    }
    finally {
        rmSync(scratch, { recursive: true, force: true });
    }
}
try {
    if (options.phase === 'browser') {
        const directory = join(reportDirectory, 'browser');
        const checked = spawnSync(process.execPath, [join(root, 'scripts/browser/run.mjs'), '--feature', options.feature || 'all', '--out', directory, ...(options.chrome ? ['--chrome', options.chrome] : [])], { cwd: root, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024, detached: true });
        if (checked.error && checked.pid) { try { process.kill(-checked.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        writeFileSync(join(reportDirectory, 'browser-process.json'), JSON.stringify({ exit: checked.status, error: checked.error?.message, stdout: checked.stdout, stderr: checked.stderr }, null, 2));
        assert.ifError(checked.error);
        assert.equal(checked.status, 0, checked.stdout + checked.stderr);
        summary.browser = JSON.parse(readFileSync(join(directory, 'browser.json'), 'utf8'));
        assert.equal(summary.browser.status, 'passed');
        assert.ok(summary.browser.checks.length > 0 && Object.values(summary.browser.cleanup).every(Boolean), 'Missing browser checks or cleanup');
    }
    if (options.phase === 'api') {
        const { runApi } = await import('./lib/formal-api.mjs');
        summary.api = runApi({ feature: options.feature, output: join(reportDirectory, 'api') });
    }
    if (options.phase === 'behavior')
        summary.behavior = runBehavior({ feature: options.feature, pattern: options.pattern, shard: shard && [+shard[1], +shard[2]], output: join(reportDirectory, 'behavior') });
    if (['all', 'models'].includes(options.phase))
        for (const name of names)
            checkModel(name, modelDirectory);
    if (options.phase === 'controls' || (options.phase === 'all' && options.selfTest)) {
        const controls = selectShard(names.flatMap((name) => modelControls[name].map((control) => [name, control])));
        assert.ok(controls.length, 'Empty model-control shard');
        for (const [name, control] of controls)
            scratchControl(name, control);
    }
    if (!options.modelsOnly && ['all', 'generated'].includes(options.phase) && names.includes('ApplySettlementRace')) {
        await graph();
        const replay = spawnSync(process.execPath, [join(root, 'scripts/lib/formal-apply-replay.mjs'), join(reportDirectory, 'model-graph.json'), join(reportDirectory, 'replay-counterexample.json')], { cwd: root, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(replay.error);
        assert.equal(replay.status, 0, replay.stdout + replay.stderr);
        summary.generated = JSON.parse(replay.stdout);
        console.log(`Model-generated real-code replay PASS: ${JSON.stringify(summary.generated)}`);
    }
    if (!options.modelsOnly && ['all', 'generated'].includes(options.phase) && names.includes('ReconcileAcceptance')) await acceptanceGraph();
    if (!options.modelsOnly && ['all', 'implementation', 'trace-controls'].includes(options.phase)) {
        const { checkMountedImplementation } = await import('./lib/formal-implementation-conformance.mjs');
        for (const name of new Set(names.map((name) => name === 'ApplySettlementRace' ? 'BuilderLifecycle' : name === 'ReconcileAcceptance' ? 'ReconcileLifecycle' : name)))
            checkMountedImplementation(root, name, checkModel, reportDirectory);
    }
    if (options.phase === 'source-proofs' || (!options.modelsOnly && options.phase === 'all' && options.selfTest)) {
        const proofs = selectShard(sourceProofs.map((proof, index) => ({ proof, index })).filter(({ proof }) => (names.includes(proof.model) || (proof.model === 'ImplementationConformance' && names.includes('BuilderLifecycle')) || (proof.model === 'Behavior' && options.model === 'all'))));
        assert.ok(proofs.length, 'Empty source-proof shard');
        for (const { proof, index } of proofs) {
            const output = join(reportDirectory, 'source-proofs', `${index}-${proof.property}`);
            if (proof.kind === 'ownership') await graph();
            if (proof.kind === 'reconcile-acceptance') await acceptanceGraph();
            const result = spawnSync(process.execPath, [join(root, 'scripts/lib/formal-source-proof-worker.mjs'), String(index), proof.kind === 'ownership' ? join(reportDirectory, 'model-graph.json') : proof.kind === 'reconcile-acceptance' ? join(reportDirectory, 'acceptance-replay/graph.json') : '-', output], { cwd: root, encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024, detached: true });
            if (result.error && result.pid) {
                try {
                    process.kill(-result.pid, 'SIGKILL');
                }
                catch (error) {
                    if (error.code !== 'ESRCH')
                        throw error;
                }
            }
            process.stdout.write(result.stdout || '');
            assert.ifError(result.error);
            assert.equal(result.status, 0, result.stderr);
            const evidence = JSON.parse(readFileSync(join(output, 'proof.json'), 'utf8'));
            assert.equal(evidence.property, proof.property);
            assert.ok(evidence.positive && evidence.negative);
            summary.sourceProofs.push(evidence);
        }
    }
    if (!options.modelsOnly && (options.phase === 'trace-controls' || (options.phase === 'all' && options.selfTest))) {
        const active = traceRules.filter((property) => property === 'Inv_RenderedReconcileReceiptIsCurrent' ? names.some(name => ['ReconcileLifecycle', 'ReconcileAcceptance'].includes(name)) : names.includes('BuilderLifecycle') || names.includes('ApplySettlementRace'));
        const selected = selectShard(active);
        assert.ok(selected.length, 'Empty trace-control shard');
        for (const property of selected) {
            const family = property === 'Inv_RenderedReconcileReceiptIsCurrent' ? 'ReconcileLifecycle' : 'BuilderLifecycle';
            checkTraceRule(property, join(reportDirectory, family, 'trace.json'), checkModel);
        }
    }
    if (options.phase === 'all' && options.selfTest && !options.modelsOnly) {
        const framework = spawnSync(process.execPath, ['--test', join(root, 'scripts/lib/formal-framework.test.mjs')], { cwd: root, encoding: 'utf8', timeout: 30000 });
        assert.ifError(framework.error);
        assert.equal(framework.status, 0, framework.stdout + framework.stderr);
        summary.frameworkControls = 'passed';
        const result = spawnSync(process.execPath, [join(root, 'ui/node_modules/typescript/bin/tsc'), '-p', 'tsconfig.test.json'], { cwd: join(root, 'ui/packages/loom-ui'), encoding: 'utf8', timeout: 30000, maxBuffer: 8 * 1024 * 1024 });
        assert.ifError(result.error);
        assert.equal(result.status, 0, result.stdout + result.stderr);
        summary.typecheck = 'passed';
    }
    assert.equal(fingerprint(), inputFingerprint, 'Verification inputs changed while running');
    summary.status = summary.behavior?.status || 'passed';
    summary.seconds = (performance.now() - started) / 1000;
    console.log(`${options.phase === 'behavior' ? `Behavior coverage ${summary.status}` : `Verified ${options.model}/${options.phase}`}; evidence ${reportDirectory}`);
}
catch (error) {
    summary.status = 'failed';
    summary.error = { message: error.message, code: error.code };
    throw error;
}
finally {
    writeFileSync(join(reportDirectory, 'verification.json'), JSON.stringify(summary, null, 2));
}
