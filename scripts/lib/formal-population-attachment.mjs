import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, existsSync, readdirSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { pathToFileURL } from 'node:url';
import { spawnSync } from 'node:child_process';
import { root } from './formal-tools.mjs';
import { apiDoctor, apiSourceFingerprint, checkApiEvents } from './formal-api.mjs';
import { verificationTimeoutMs, goTestTimeout } from './verification-budget.mjs';

export const attachmentFile = 'internal/explorer/lifecycle/population_routes.go';
const attachmentAuthoringFile = 'internal/explorer/lifecycle/authoring.go';
export const attachmentAdapter = 'internal/explorer/lifecycle/population_attachment_formal_replay_test.go';
export const attachmentTest = 'TestPopulationAttachmentGeneratedReplay';
export const attachmentProperties = ['Inv_AttachmentAcceptsAuthorizedChoice', 'Inv_AttachmentPersistsExactChoice', 'Inv_InvalidAttachmentDoesNotReachSave'];
const selections = ['current', 'incomplete', 'foreignProject', 'staleGeneration', 'staleScope'];
const inputFields = ['edge', 'snapshotMatches', 'outputMatches', 'selectionMatches', 'selection', 'route', 'batch'];
const resultFields = ['id', 'accepted', 'searchChoices', 'searchPreservedHead', 'saveAttempts', 'writes', 'preservedHead', 'exactWorkspace', 'stableHead', 'versionBefore', 'versionAfter', 'selectionId', 'route', 'error'];
const canonical = value => Array.isArray(value) ? value.map(canonical) : value && typeof value === 'object' ? Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])])) : value;
const key = value => JSON.stringify(canonical(value));
export function validateAttachmentInput(input) {
    assert.ok(input && typeof input === 'object' && !Array.isArray(input));
    assert.deepEqual(Object.keys(input).sort(), [...inputFields].sort(), 'Unknown or missing attachment input');
    assert.ok(['primary', 'parallel', 'zero'].includes(input.edge));
    for (const name of ['snapshotMatches', 'outputMatches', 'selectionMatches']) assert.equal(typeof input[name], 'boolean');
    assert.ok([...selections, 'revokedScope'].includes(input.selection)); assert.ok(['authorized', 'wrongDirection', 'blockedEdge'].includes(input.route)); assert.ok(['single', 'invalidSecond'].includes(input.batch));
    if (input.edge === 'zero') assert.ok(input.route === 'authorized', 'Zero-hop direction and edge attacks are outside this bound');
    return input;
}
export function attachmentCases() {
    const cases = [];
    for (const edge of ['primary', 'parallel']) for (let mask = 0; mask < 8; mask++) for (const selection of selections) for (const route of ['authorized', 'wrongDirection', 'blockedEdge']) for (const batch of ['single', 'invalidSecond']) cases.push({ id: `attachment-${cases.length}`, edge, snapshotMatches: Boolean(mask & 1), outputMatches: Boolean(mask & 2), selectionMatches: Boolean(mask & 4), selection, route, batch });
    cases.push({ id: 'attachment-zero', edge: 'zero', snapshotMatches: true, outputMatches: true, selectionMatches: true, selection: 'current', route: 'authorized', batch: 'single' });
    cases.push({ id: 'attachment-revoked-scope', edge: 'primary', snapshotMatches: true, outputMatches: true, selectionMatches: true, selection: 'revokedScope', route: 'authorized', batch: 'single' });
    return cases;
}
export function expectedAttachment(input) {
    validateAttachmentInput(input);
    const accepted = input.snapshotMatches && input.outputMatches && input.selectionMatches && input.selection === 'current' && input.route === 'authorized' && input.batch === 'single';
    const route = accepted && input.edge !== 'zero' ? [{ resourceType: 'Observation', relationship: 'subject_Patient', catalogEdgeId: input.edge === 'parallel' ? 'subject-patient-parallel' : 'subject-patient', storageDirection: 'INBOUND' }] : [];
    return { accepted, searchChoices: input.edge === 'zero' ? 1 : 2, searchPreservedHead: true, saveAttempts: +accepted, writes: +accepted, preservedHead: !accepted, exactWorkspace: accepted, stableHead: true, versionBefore: 1, versionAfter: accepted ? 2 : 1, selectionId: accepted ? 'selection-current' : '', route };
}
function validateSource(productionSource) {
    if (productionSource === undefined) return;
    assert.ok(productionSource && typeof productionSource === 'object' && !Array.isArray(productionSource), 'Attachment source override must be a nonempty map');
    assert.ok(Object.keys(productionSource).length, 'Empty attachment source override');
    for (const [file, source] of Object.entries(productionSource)) {
        assert.ok([attachmentFile, attachmentAuthoringFile].includes(file), 'Unknown attachment source override');
        assert.ok(typeof source === 'string' && source.trim(), 'Attachment source override must be nonempty');
    }
}
const fingerprint = () => createHash('sha256').update(apiSourceFingerprint()).update(readFileSync(new URL(import.meta.url))).update(readFileSync(new URL('./formal-tools.mjs', import.meta.url))).digest('hex');
const propertyForMismatch = (expected, actual) => {
    if (expected.accepted && !actual.accepted) return attachmentProperties[0];
    if (expected.accepted && ['accepted', 'writes', 'saveAttempts', 'exactWorkspace', 'stableHead', 'versionBefore', 'versionAfter', 'selectionId', 'route'].some(name => key(expected[name]) !== key(actual[name]))) return attachmentProperties[1];
    if (!expected.accepted && ['accepted', 'saveAttempts', 'writes', 'preservedHead', 'versionBefore', 'versionAfter', 'selectionId', 'route'].some(name => key(expected[name]) !== key(actual[name]))) return attachmentProperties[2];
};
export function checkAttachmentResults(record, cases) {
    assert.ok(Array.isArray(cases) && cases.length > 0 && cases.length <= 1024, 'Missing attachment cases');
    assert.equal(new Set(cases.map(row => row.id)).size, cases.length, 'Duplicate expected case');
    assert.deepEqual(Object.keys(record).sort(), ['results', 'schemaVersion']); assert.equal(record.schemaVersion, 1); assert.ok(Array.isArray(record.results)); assert.equal(record.results.length, cases.length, 'Missing actual attachment results');
    const actual = new Map();
    for (const row of record.results) {
        assert.deepEqual(Object.keys(row).sort(), [...resultFields].sort()); assert.ok(typeof row.id === 'string' && row.id); assert.ok(!actual.has(row.id), 'Duplicate actual attachment result');
        for (const field of ['accepted', 'searchPreservedHead', 'preservedHead', 'exactWorkspace', 'stableHead']) assert.equal(typeof row[field], 'boolean');
        for (const field of ['searchChoices', 'saveAttempts', 'writes', 'versionBefore', 'versionAfter']) assert.ok(Number.isSafeInteger(row[field]) && row[field] >= 0);
        assert.equal(typeof row.selectionId, 'string'); assert.equal(typeof row.error, 'string'); assert.equal(row.accepted, row.error === '', 'Inconsistent attachment outcome'); assert.ok(Array.isArray(row.route));
        for (const step of row.route) {
            assert.ok(step && typeof step === 'object' && !Array.isArray(step));
            assert.ok(Object.keys(step).every(name => ['catalogEdgeId', 'relationship', 'resourceType', 'storageDirection'].includes(name)));
            for (const name of ['resourceType', 'relationship']) assert.equal(typeof step[name], 'string');
            for (const value of Object.values(step)) assert.equal(typeof value, 'string');
        }
        actual.set(row.id, { ...row, route: row.route.map(step => ({ catalogEdgeId: '', storageDirection: '', ...step })) });
    }
    assert.deepEqual([...actual.keys()].sort(), cases.map(row => row.id).sort(), 'Foreign actual attachment results');
    let conformance;
    for (const row of cases) {
        const { id, ...input } = row; validateAttachmentInput(input); const expected = expectedAttachment(input), observed = actual.get(id);
        if (!observed.accepted && input.selection === 'revokedScope') assert.match(observed.error, /STALE_POPULATION_SELECTION/, 'Revocation witness did not reach selection scope reauthorization');
        if (!observed.accepted && !expected.accepted && input.snapshotMatches && input.outputMatches && input.selectionMatches && input.selection === 'current' && input.route === 'authorized' && input.batch === 'invalidSecond') assert.match(observed.error, /outputId does not identify a valid row-rooted table/, 'Atomic-prefix witness did not reach late preparation');
        if (Object.keys(expected).every(name => key(expected[name]) === key(observed[name]))) continue;
        const property = propertyForMismatch(expected, observed);
        if (!property) { conformance ??= Object.assign(new Error('Attachment observation mismatch outside named rules'), { code: 'FORMAL_CONFORMANCE' }); continue; }
        const witness = { schemaVersion: 1, kind: 'population-attachment', property, input, expected, actual: observed };
        throw Object.assign(new Error(`${property}: expected ${key(expected)}, actual ${key(observed)}`), { code: 'FORMAL_PROPERTY', property, witness });
    }
    if (conformance) throw conformance;
    return { cases: cases.length, accepted: record.results.filter(row => row.accepted).length, rejected: record.results.filter(row => !row.accepted).length };
}
function runGo(cases, { output, productionSource } = {}) {
    validateSource(productionSource); const tool = apiDoctor(), before = fingerprint(), scratch = mkdtempSync(join(tmpdir(), 'loom-attachment-go-'));
    try {
        const input = join(scratch, 'cases.json'), result = join(scratch, 'results.json'); writeFileSync(input, JSON.stringify({ schemaVersion: 1, cases }));
        const inputSha256 = createHash('sha256').update(readFileSync(input)).digest('hex');
        const args = ['test', '-json', `-timeout=${goTestTimeout}`, '-count=1', '-run', `^${attachmentTest}$`];
        if (productionSource !== undefined) {
            const replacements = {};
            for (const [index, [file, source]] of Object.entries(productionSource).entries()) {
                const replacement = join(scratch, `source-${index}.go`); writeFileSync(replacement, source); replacements[join(root, file)] = replacement;
            }
            const overlay = join(scratch, 'overlay.json'); writeFileSync(overlay, JSON.stringify({ Replace: replacements })); args.push('-overlay', overlay);
        }
        args.push('./internal/explorer/lifecycle');
        const env = { ...process.env, GOFLAGS: '', GOWORK: 'off', GOTOOLCHAIN: 'local', LOOM_ATTACHMENT_INPUT: input, LOOM_ATTACHMENT_OUTPUT: result };
        if (tool.executable === '/workspace/.loom-setup/go/bin/go') { if (!env.GOMODCACHE && existsSync('/workspace/.loom-setup/gomod')) env.GOMODCACHE = '/workspace/.loom-setup/gomod'; if (!env.GOCACHE && existsSync('/workspace/loom/.gocache')) env.GOCACHE = '/workspace/loom/.gocache'; }
        const processResult = spawnSync(tool.executable, args, { cwd: root, env, encoding: 'utf8', timeout: verificationTimeoutMs, maxBuffer: 8 * 1024 * 1024, detached: true });
        if (processResult.error && processResult.pid) { try { process.kill(-processResult.pid, 'SIGKILL'); } catch (error) { if (error.code !== 'ESRCH') throw error; } }
        assert.equal(createHash('sha256').update(readFileSync(input)).digest('hex'), inputSha256, 'Attachment inputs changed during Go replay');
        if (output) { mkdirSync(output, { recursive: true }); writeFileSync(join(output, 'go.jsonl'), processResult.stdout || ''); writeFileSync(join(output, 'cases.json'), JSON.stringify({ schemaVersion: 1, cases }, null, 2)); writeFileSync(join(output, 'process.json'), JSON.stringify({ command: [tool.executable, ...args], tool, sourceFingerprint: before, inputSha256, sourceOverrideSha256: productionSource === undefined ? undefined : createHash('sha256').update(key(productionSource)).digest('hex'), exit: processResult.status, error: processResult.error?.message, stderr: processResult.stderr }, null, 2)); }
        assert.ifError(processResult.error); assert.equal(processResult.status, 0, processResult.stdout + processResult.stderr); checkApiEvents(processResult.stdout, { tests: [attachmentTest] }); assert.equal(fingerprint(), before, 'Attachment inputs changed during execution'); assert.ok(existsSync(result), 'Missing actual attachment observations'); const record = JSON.parse(readFileSync(result, 'utf8')); if (output) writeFileSync(join(output, 'observations.json'), JSON.stringify(record, null, 2)); return { record, tool };
    } finally { rmSync(scratch, { recursive: true, force: true }); }
}
export function runAttachment({ output, productionSource, cases = attachmentCases(), witnessFile } = {}) {
    validateSource(productionSource); assert.ok(Array.isArray(cases) && cases.length); assert.ok(cases.length <= 1024); assert.equal(new Set(cases.map(row => row.id)).size, cases.length, 'Duplicate attachment inputs'); for (const { id, ...input } of cases) { assert.ok(typeof id === 'string' && id); validateAttachmentInput(input); }
    if (output) assert.ok(!existsSync(output) || readdirSync(output).length === 0, 'Attachment output must be fresh');
    const before = fingerprint();
    try { const { record, tool } = runGo(cases, { output, productionSource }); const result = checkAttachmentResults(record, cases); assert.equal(fingerprint(), before, 'Attachment inputs changed during replay'); return { ...result, sourceFingerprint: before, tool, kind: 'bounded-public-code', boundary: 'actual public SearchPopulationRoutes and ApplyCommands; 480 finite one-hop/parallel cases plus zero-hop and coherent scope-revocation cases; controlled capability provider and CAS store; no TLA, arbitrary histories, database atomicity, query rows or browser proof' }; }
    catch (error) { if (error.witness && witnessFile) writeFileSync(witnessFile, JSON.stringify(error.witness, null, 2)); throw error; }
}
const edgeBefore = 'CatalogEdgeID: step.EdgeID, StorageDirection: step.StorageDirection,';
const rejectBefore = 'snapshot := authorized.Snapshot\n\tif err := validateAuthorizedReadScope';
export const attachmentSourceProofs = [
    { model: 'Behavior', property: attachmentProperties[2], kind: 'attachment-replay', branch: 'stale-choice', edits: [[attachmentAuthoringFile, 'identity.SnapshotToken != request.SnapshotToken || identity.OutputID', 'identity.OutputID'], [attachmentFile, 'identity.SnapshotToken != snapshot.Token || identity.OutputID', 'identity.OutputID'], [attachmentFile, 'expected != command.RouteChoiceID', 'expected == ""']] },
    { model: 'Behavior', property: attachmentProperties[1], kind: 'attachment-replay', branch: 'parallel-edge', edits: [[attachmentFile, edgeBefore, 'CatalogEdgeID: snapshot.Edges[0].ID, StorageDirection: step.StorageDirection,']] },
    { model: 'Behavior', property: attachmentProperties[1], kind: 'attachment-replay', branch: 'direction', edits: [[attachmentFile, edgeBefore, 'CatalogEdgeID: step.EdgeID, StorageDirection: "",']] },
    { model: 'Behavior', property: attachmentProperties[0], kind: 'attachment-replay', branch: 'valid-choice', edits: [[attachmentFile, rejectBefore, 'return nil, invalidPopulationRoute("controlled valid-choice rejection")\n\tsnapshot := authorized.Snapshot\n\tif err := validateAuthorizedReadScope']] },
];
export function replayAttachmentWitness(witness, { productionSource, output } = {}) {
    assert.equal(witness.schemaVersion, 1); assert.equal(witness.kind, 'population-attachment'); assert.ok(attachmentProperties.includes(witness.property)); validateAttachmentInput(witness.input); assert.deepEqual(witness.expected, expectedAttachment(witness.input), 'Changed attachment witness expectation'); assert.ok(witness.expected.accepted ? attachmentProperties.slice(0, 2).includes(witness.property) : witness.property === attachmentProperties[2], 'Attachment property does not apply to input');
    try { runAttachment({ output, productionSource, cases: [{ id: 'witness', ...witness.input }] }); return { status: 'property-held', property: witness.property }; }
    catch (error) { if (error.code !== 'FORMAL_PROPERTY' || error.property !== witness.property) throw error; return { status: 'reproduced', ...error.witness }; }
}
export function checkAttachmentSourceProof(proof, _graph, output) {
    assert.deepEqual(proof, attachmentSourceProofs.find(row => row.branch === proof.branch), 'Unknown attachment proof'); mkdirSync(output, { recursive: true });
    const productionSource = {};
    for (const [file, before, after] of proof.edits) {
        const current = productionSource[file] ?? readFileSync(join(root, file), 'utf8');
        assert.equal(current.split(before).length, 2, 'Nonunique attachment source anchor'); productionSource[file] = current.replace(before, after);
    }
    runAttachment({ output: join(output, 'positive') }); const witnessFile = join(output, 'counterexample.json');
    assert.throws(() => runAttachment({ output: join(output, 'negative'), productionSource, witnessFile }), error => error.code === 'FORMAL_PROPERTY' && error.property === proof.property);
    const witness = JSON.parse(readFileSync(witnessFile, 'utf8')); assert.equal(replayAttachmentWitness(witness).status, 'property-held'); assert.equal(replayAttachmentWitness(witness, { productionSource }).status, 'reproduced'); runAttachment({ output: join(output, 'restored') });
    const evidence = { model: 'Behavior', property: proof.property, kind: proof.kind, branch: proof.branch, positive: true, negative: true, restored: true, sourceFiles: Object.keys(productionSource), witness: 'counterexample.json', boundary: 'bounded public-code generated attachment replay; not TLA' }; writeFileSync(join(output, 'proof.json'), JSON.stringify(evidence, null, 2)); return evidence;
}
if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
    const [command, file, extra] = process.argv.slice(2);
    if (command === '--generate') { assert.ok(!extra || extra === '--self-test'); assert.ok(file); assert.ok(!existsSync(file) || readdirSync(file).length === 0, 'Attachment output must be fresh'); mkdirSync(file, { recursive: true }); const result = runAttachment({ output: join(file, 'go'), witnessFile: join(file, 'counterexample.json') }); if (extra) result.sourceControls = attachmentSourceProofs.map(proof => checkAttachmentSourceProof(proof, undefined, join(file, proof.branch))); writeFileSync(join(file, 'replay.json'), JSON.stringify(result, null, 2)); console.log(JSON.stringify(result)); }
    else if (command === '--witness') { const result = replayAttachmentWitness(JSON.parse(readFileSync(file, 'utf8')), extra !== undefined ? { productionSource: extra.endsWith('.json') ? JSON.parse(readFileSync(extra, 'utf8')) : { [attachmentFile]: readFileSync(extra, 'utf8') } } : {}); console.log(JSON.stringify(result)); process.exitCode = result.status === 'reproduced' ? 1 : 0; }
    else throw new Error('Use --generate OUT [--self-test] or --witness PATH [SOURCE_OR_OVERRIDES_JSON]');
}
